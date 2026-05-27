package bridge

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

type OrchestrationManager struct {
	cfg       *config.Config
	mu        sync.Mutex
	runs      map[string]context.CancelFunc
	output    chan<- protocol.Envelope
	pending   []protocol.Envelope
	approvals map[string]orchestrationApproval
}

type orchestrationApproval struct {
	runID string
	ch    chan protocol.ApprovalResponsePayload
}

type orchestrationTurn struct {
	TurnID        string
	Role          string
	CLI           string
	Msg           string
	Content       string
	Handoff       string
	HandoffFields orchestrationHandoffFields
	Err           string
	Tools         []RunnerToolEvent
	Verifier      bool
}

type orchestrationHandoffFields struct {
	Status   string
	Changed  string
	Verified string
	Next     string
	Risks    string
}

type workspaceSnapshot struct {
	Root      string
	Files     map[string]workspaceFileState
	Available bool
	Truncated bool
	Err       string
}

type workspaceFileState struct {
	Size    int64
	ModTime int64
}

type workspaceChangeReport struct {
	Root      string
	Changed   []string
	Available bool
	Truncated bool
	Err       string
}

var safeOrchestrationFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func NewOrchestrationManager(cfg *config.Config) *OrchestrationManager {
	return &OrchestrationManager{
		cfg:       cfg,
		runs:      make(map[string]context.CancelFunc),
		approvals: make(map[string]orchestrationApproval),
	}
}

func (m *OrchestrationManager) AttachOut(out chan<- protocol.Envelope) {
	m.mu.Lock()
	pending := append([]protocol.Envelope(nil), m.pending...)
	m.pending = nil
	for _, env := range pending {
		send(out, env)
	}
	m.output = out
	m.mu.Unlock()
}

func (m *OrchestrationManager) DetachOut(out chan<- protocol.Envelope) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.output == out {
		m.output = nil
	}
}

func (m *OrchestrationManager) Start(parent context.Context, payload protocol.OrchestrationStartPayload) {
	if payload.RunID == "" {
		m.send(protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
			Kind:  "run.error",
			Error: "orchestration run id is required",
		}))
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	if old := m.runs[payload.RunID]; old != nil {
		old()
	}
	m.runs[payload.RunID] = cancel
	m.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			m.mu.Lock()
			delete(m.runs, payload.RunID)
			m.mu.Unlock()
			m.cancelApprovals(payload.RunID)
		}()
		m.run(ctx, payload)
	}()
}

func (m *OrchestrationManager) Cancel(runID string) {
	m.mu.Lock()
	cancel := m.runs[runID]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	m.cancelApprovals(runID)
}

func (m *OrchestrationManager) CloseAll() {
	m.mu.Lock()
	var cancels []context.CancelFunc
	runIDs := make([]string, 0, len(m.runs))
	for runID, cancel := range m.runs {
		cancels = append(cancels, cancel)
		runIDs = append(runIDs, runID)
		delete(m.runs, runID)
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	for _, runID := range runIDs {
		m.cancelApprovals(runID)
	}
}

type orchestrationApprovalRequester struct {
	manager *OrchestrationManager
	runID   string
	turnID  string
	role    string
	cli     string
	cwd     string
}

func (r orchestrationApprovalRequester) RequestApproval(ctx context.Context, req protocol.ApprovalRequestPayload) (protocol.ApprovalResponsePayload, error) {
	if req.RequestID == "" {
		req.RequestID = fmt.Sprintf("apr_%d", time.Now().UnixNano())
	}
	req.RunID = r.runID
	req.TurnID = r.turnID
	if req.CWD == "" {
		req.CWD = r.cwd
	}
	if req.Kind == "" {
		req.Kind = "orchestration.approval"
	}
	ch := make(chan protocol.ApprovalResponsePayload, 1)
	m := r.manager
	m.mu.Lock()
	if m.approvals == nil {
		m.approvals = make(map[string]orchestrationApproval)
	}
	m.approvals[req.RequestID] = orchestrationApproval{runID: r.runID, ch: ch}
	m.mu.Unlock()

	m.send(protocol.MustEnvelope(protocol.TypeApprovalRequest, "", req))
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case res := <-ch:
			return res, nil
		case <-ticker.C:
			m.send(protocol.MustEnvelope(protocol.TypeApprovalRequest, "", req))
		case <-ctx.Done():
			m.removeApproval(req.RequestID)
			return protocol.ApprovalResponsePayload{}, ctx.Err()
		}
	}
}

func (m *OrchestrationManager) ApprovalResponse(res protocol.ApprovalResponsePayload) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	pending := m.approvals[res.RequestID]
	if pending.ch == nil {
		return false
	}
	delete(m.approvals, res.RequestID)
	pending.ch <- res
	return true
}

func (m *OrchestrationManager) removeApproval(requestID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.approvals, requestID)
}

func (m *OrchestrationManager) cancelApprovals(runID string) {
	m.mu.Lock()
	var pending []orchestrationApproval
	for requestID, approval := range m.approvals {
		if approval.runID == runID {
			pending = append(pending, approval)
			delete(m.approvals, requestID)
		}
	}
	m.mu.Unlock()
	for _, approval := range pending {
		approval.ch <- protocol.ApprovalResponsePayload{Decision: "cancel"}
	}
}

func (m *OrchestrationManager) run(ctx context.Context, payload protocol.OrchestrationStartPayload) {
	preparedPrompt, _, err := PrepareOrchestrationPromptFiles(m.cfg, payload.RunID, payload.Prompt, payload.Files)
	if err != nil {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:   "run.error",
			Status: store.OrchestrationFailed,
			Error:  err.Error(),
		})
		return
	}
	originalPrompt := payload.Prompt
	payload.Prompt = preparedPrompt
	workspaceBefore := snapshotWorkspace(m.cwd(payload), workspaceSnapshotIgnoredPaths(m.cfg)...)
	mode := payload.Mode
	if mode != "collaboration" && mode != "debate" {
		mode = "collaboration"
	}
	maxTurns := payload.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 4
	}
	if maxTurns > 12 {
		maxTurns = 12
	}
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "run.start",
		Status:  store.OrchestrationRunning,
		Content: fmt.Sprintf("Starting %s run with %d turns.", mode, maxTurns),
	})

	var history []orchestrationTurn
	for turn := 1; turn <= maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.cancelled",
				Status: store.OrchestrationCanceled,
				Error:  "canceled",
			})
			return
		}
		role, cli := roleForTurn(mode, turn)
		turnID := fmt.Sprintf("%s-%02d", payload.RunID, turn)
		if payload.PromptSeq > 0 {
			turnID = fmt.Sprintf("%s-p%03d-%02d", payload.RunID, payload.PromptSeq, turn)
		}
		prompt := composeOrchestrationPrompt(mode, payload.Prompt, payload.Context, payload.Resume, role, cli, turn, maxTurns, history)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.start",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: promptHeader(role, cli, turn),
		})
		content, tools, err := m.runCLI(ctx, payload, turnID, role, cli, prompt)
		record := newOrchestrationTurnRecord(turnID, role, cli, content, tools)
		if err != nil {
			record.Err = err.Error()
			if summary := erroredTurnFallbackSummary(payload.Prompt, turn >= maxTurns, history, record); summary != "" {
				appendConclusionToTurnRecord(&record, summary)
			}
			history = append(history, record)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "turn.end",
				TurnID:  turnID,
				Role:    role,
				CLI:     cli,
				Content: record.Content,
				Status:  "error",
				Error:   err.Error(),
			})
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				m.emit(payload.RunID, protocol.OrchestrationEventPayload{
					Kind:   "run.cancelled",
					Status: store.OrchestrationCanceled,
					Error:  "canceled",
				})
				return
			}
			if blocker, ok := repeatedBlockingHandoff(history); ok {
				m.emit(payload.RunID, protocol.OrchestrationEventPayload{
					Kind:    "run.error",
					Status:  store.OrchestrationFailed,
					Error:   "repeated blocker: " + blocker,
					Content: "Orchestration stopped because the same blocker repeated without concrete progress.",
				})
				return
			}
			continue
		}
		if summary := turnConclusionFallbackSummary(payload.Prompt, turn, maxTurns, history, record); summary != "" {
			delta := appendConclusionToTurnRecord(&record, summary)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "turn.delta",
				TurnID:  turnID,
				Role:    role,
				CLI:     cli,
				Content: delta,
			})
		}
		history = append(history, record)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.end",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: record.Content,
			Status:  "success",
		})
		if blocker, ok := repeatedBlockingHandoff(history); ok {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "run.error",
				Status:  store.OrchestrationFailed,
				Error:   "repeated blocker: " + blocker,
				Content: "Orchestration stopped because the same blocker repeated without concrete progress.",
			})
			return
		}
		if turn >= 2 && resolvedHandoffReady(record.Content) {
			break
		}
	}
	if m.shouldRunFinalVerifier(history) {
		if err := ctx.Err(); err != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.cancelled",
				Status: store.OrchestrationCanceled,
				Error:  "canceled",
			})
			return
		}
		role, cli := verifierRoleCLI(mode, history)
		turnID := fmt.Sprintf("%s-verifier", payload.RunID)
		if payload.PromptSeq > 0 {
			turnID = fmt.Sprintf("%s-p%03d-verifier", payload.RunID, payload.PromptSeq)
		}
		prompt := composeFinalVerifierPrompt(mode, payload.Prompt, payload.Context, payload.Resume, role, cli, history)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.start",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: "final verifier via " + cli,
		})
		content, tools, err := m.runCLI(ctx, payload, turnID, role, cli, prompt)
		record := newOrchestrationTurnRecord(turnID, role, cli, content, tools)
		record.Verifier = true
		if err != nil {
			record.Err = err.Error()
			if summary := erroredTurnFallbackSummary(payload.Prompt, true, history, record); summary != "" {
				appendConclusionToTurnRecord(&record, summary)
			}
			history = append(history, record)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "turn.end",
				TurnID:  turnID,
				Role:    role,
				CLI:     cli,
				Content: record.Content,
				Status:  "error",
				Error:   err.Error(),
			})
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				m.emit(payload.RunID, protocol.OrchestrationEventPayload{
					Kind:   "run.cancelled",
					Status: store.OrchestrationCanceled,
					Error:  "canceled",
				})
				return
			}
			if blocker, ok := repeatedBlockingHandoff(history); ok {
				if !m.shouldDeferRepeatedBlockerForWorkspaceRemediation(payload, originalPrompt, history, workspaceBefore) {
					m.emit(payload.RunID, protocol.OrchestrationEventPayload{
						Kind:    "run.error",
						Status:  store.OrchestrationFailed,
						Error:   "repeated blocker: " + blocker,
						Content: "Orchestration stopped because the same blocker repeated without concrete progress.",
					})
					return
				}
			}
		} else {
			if summary := verifierConclusionFallbackSummary(payload.Prompt, history, record); summary != "" {
				delta := appendConclusionToTurnRecord(&record, summary)
				m.emit(payload.RunID, protocol.OrchestrationEventPayload{
					Kind:    "turn.delta",
					TurnID:  turnID,
					Role:    role,
					CLI:     cli,
					Content: delta,
				})
			}
			history = append(history, record)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "turn.end",
				TurnID:  turnID,
				Role:    role,
				CLI:     cli,
				Content: record.Content,
				Status:  "success",
			})
			if blocker, ok := repeatedBlockingHandoff(history); ok {
				if !m.shouldDeferRepeatedBlockerForWorkspaceRemediation(payload, originalPrompt, history, workspaceBefore) {
					m.emit(payload.RunID, protocol.OrchestrationEventPayload{
						Kind:    "run.error",
						Status:  store.OrchestrationFailed,
						Error:   "repeated blocker: " + blocker,
						Content: "Orchestration stopped because the same blocker repeated without concrete progress.",
					})
					return
				}
			}
		}
	}

	workspaceAfter := snapshotWorkspace(m.cwd(payload), workspaceSnapshotIgnoredPaths(m.cfg)...)
	workspaceChanges := diffWorkspaceSnapshots(workspaceBefore, workspaceAfter)
	if shouldRunWorkspaceChangeRemediation(originalPrompt, history, workspaceChanges) {
		reason := missingWorkspaceChangeReason(workspaceChanges, history)
		var stop bool
		history, stop = m.runWorkspaceChangeRemediation(ctx, payload, mode, history, reason)
		if stop {
			return
		}
		workspaceAfter = snapshotWorkspace(m.cwd(payload), workspaceSnapshotIgnoredPaths(m.cfg)...)
		workspaceChanges = diffWorkspaceSnapshots(workspaceBefore, workspaceAfter)
	}
	if reason, ok := unresolvedFinalRun(originalPrompt, history, workspaceChanges); ok {
		if shouldRunFinalAssessmentRemediation(originalPrompt, history, reason) {
			var stop bool
			history, stop = m.runFinalAssessmentRemediation(ctx, payload, mode, history, workspaceChanges, reason)
			if stop {
				return
			}
			workspaceAfter = snapshotWorkspace(m.cwd(payload), workspaceSnapshotIgnoredPaths(m.cfg)...)
			workspaceChanges = diffWorkspaceSnapshots(workspaceBefore, workspaceAfter)
			reason, ok = unresolvedFinalRun(originalPrompt, history, workspaceChanges)
			if !ok {
				m.emit(payload.RunID, protocol.OrchestrationEventPayload{
					Kind:    "run.end",
					Status:  store.OrchestrationCompleted,
					Content: finalRunAssessmentSummary(originalPrompt, history, workspaceChanges, ""),
				})
				return
			}
		}
		assessment := finalRunAssessmentSummary(originalPrompt, history, workspaceChanges, reason)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "run.error",
			Status:  store.OrchestrationFailed,
			Error:   reason,
			Content: assessment,
		})
		return
	}

	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "run.end",
		Status:  store.OrchestrationCompleted,
		Content: finalRunAssessmentSummary(originalPrompt, history, workspaceChanges, ""),
	})
}

func (m *OrchestrationManager) shouldDeferRepeatedBlockerForWorkspaceRemediation(payload protocol.OrchestrationStartPayload, userPrompt string, history []orchestrationTurn, before workspaceSnapshot) bool {
	after := snapshotWorkspace(m.cwd(payload), workspaceSnapshotIgnoredPaths(m.cfg)...)
	return shouldRunWorkspaceChangeRemediation(userPrompt, history, diffWorkspaceSnapshots(before, after))
}

func (m *OrchestrationManager) runWorkspaceChangeRemediation(ctx context.Context, payload protocol.OrchestrationStartPayload, mode string, history []orchestrationTurn, reason string) ([]orchestrationTurn, bool) {
	if err := ctx.Err(); err != nil {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:   "run.cancelled",
			Status: store.OrchestrationCanceled,
			Error:  "canceled",
		})
		return history, true
	}
	role, cli := remediationRoleCLI(mode, history)
	turnID := fmt.Sprintf("%s-remediation", payload.RunID)
	if payload.PromptSeq > 0 {
		turnID = fmt.Sprintf("%s-p%03d-remediation", payload.RunID, payload.PromptSeq)
	}
	prompt := composeWorkspaceChangeRemediationPrompt(mode, payload.Prompt, payload.Context, payload.Resume, role, cli, history, reason)
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "turn.start",
		TurnID:  turnID,
		Role:    role,
		CLI:     cli,
		Content: "workspace-change remediation via " + cli,
	})
	content, tools, err := m.runCLI(ctx, payload, turnID, role, cli, prompt)
	record := newOrchestrationTurnRecord(turnID, role, cli, content, tools)
	if err != nil {
		record.Err = err.Error()
		if summary := erroredTurnFallbackSummary(payload.Prompt, true, history, record); summary != "" {
			appendConclusionToTurnRecord(&record, summary)
		}
		history = append(history, record)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.end",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: record.Content,
			Status:  "error",
			Error:   err.Error(),
		})
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.cancelled",
				Status: store.OrchestrationCanceled,
				Error:  "canceled",
			})
			return history, true
		}
		return history, false
	}
	if summary := verifierConclusionFallbackSummary(payload.Prompt, history, record); summary != "" {
		delta := appendConclusionToTurnRecord(&record, summary)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.delta",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: delta,
		})
	}
	history = append(history, record)
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "turn.end",
		TurnID:  turnID,
		Role:    role,
		CLI:     cli,
		Content: record.Content,
		Status:  "success",
	})
	if blocker, ok := repeatedBlockingHandoff(history); ok {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "run.error",
			Status:  store.OrchestrationFailed,
			Error:   "repeated blocker: " + blocker,
			Content: "Orchestration stopped because the same blocker repeated without concrete progress.",
		})
		return history, true
	}
	return history, false
}

func shouldRunFinalAssessmentRemediation(userPrompt string, history []orchestrationTurn, reason string) bool {
	if strings.TrimSpace(reason) == "" || len(history) == 0 {
		return false
	}
	last := history[len(history)-1]
	if strings.HasSuffix(last.TurnID, "-assessment-remediation") || strings.Contains(last.TurnID, "-assessment-remediation-") {
		return false
	}
	if last.Err != "" {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(last.HandoffFields.Status), "blocked") {
		return false
	}
	lower := strings.ToLower(reason)
	if strings.Contains(lower, "repeated blocker") {
		return false
	}
	return userTaskRequiresWorkspaceChange(userPrompt) || looksLikeFormalProofTask(userPrompt) || strings.Contains(lower, "acceptance")
}

func (m *OrchestrationManager) runFinalAssessmentRemediation(ctx context.Context, payload protocol.OrchestrationStartPayload, mode string, history []orchestrationTurn, changes workspaceChangeReport, reason string) ([]orchestrationTurn, bool) {
	if err := ctx.Err(); err != nil {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:   "run.cancelled",
			Status: store.OrchestrationCanceled,
			Error:  "canceled",
		})
		return history, true
	}
	role, cli := remediationRoleCLI(mode, history)
	turnID := fmt.Sprintf("%s-assessment-remediation", payload.RunID)
	if payload.PromptSeq > 0 {
		turnID = fmt.Sprintf("%s-p%03d-assessment-remediation", payload.RunID, payload.PromptSeq)
	}
	prompt := composeFinalAssessmentRemediationPrompt(mode, payload.Prompt, payload.Context, payload.Resume, role, cli, history, changes, reason)
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "turn.start",
		TurnID:  turnID,
		Role:    role,
		CLI:     cli,
		Content: "final-assessment remediation via " + cli,
	})
	content, tools, err := m.runCLI(ctx, payload, turnID, role, cli, prompt)
	record := newOrchestrationTurnRecord(turnID, role, cli, content, tools)
	if err != nil {
		record.Err = err.Error()
		if summary := erroredTurnFallbackSummary(payload.Prompt, true, history, record); summary != "" {
			appendConclusionToTurnRecord(&record, summary)
		}
		history = append(history, record)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.end",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: record.Content,
			Status:  "error",
			Error:   err.Error(),
		})
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.cancelled",
				Status: store.OrchestrationCanceled,
				Error:  "canceled",
			})
			return history, true
		}
		return history, false
	}
	if summary := verifierConclusionFallbackSummary(payload.Prompt, history, record); summary != "" {
		delta := appendConclusionToTurnRecord(&record, summary)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.delta",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: delta,
		})
	}
	history = append(history, record)
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "turn.end",
		TurnID:  turnID,
		Role:    role,
		CLI:     cli,
		Content: record.Content,
		Status:  "success",
	})
	if blocker, ok := repeatedBlockingHandoff(history); ok {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "run.error",
			Status:  store.OrchestrationFailed,
			Error:   "repeated blocker: " + blocker,
			Content: "Orchestration stopped because the same blocker repeated without concrete progress.",
		})
		return history, true
	}
	return history, false
}

func (m *OrchestrationManager) runCCB(ctx context.Context, payload protocol.OrchestrationStartPayload) {
	preparedPrompt, _, err := PrepareOrchestrationPromptFiles(m.cfg, payload.RunID, payload.Prompt, payload.Files)
	if err != nil {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:   "run.error",
			Status: store.OrchestrationFailed,
			Error:  err.Error(),
		})
		return
	}
	originalPrompt := payload.Prompt
	payload.Prompt = preparedPrompt
	workspaceBefore := snapshotWorkspace(m.cwd(payload), workspaceSnapshotIgnoredPaths(m.cfg)...)
	target := m.ccbTarget()
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "run.start",
		Status:  store.OrchestrationRunning,
		CLI:     "ccb",
		Content: fmt.Sprintf("Starting local CCB job for %s.", target),
		Data: map[string]any{
			"target": target,
		},
	})
	turnID := payload.RunID + "-ccb"
	if payload.PromptSeq > 0 {
		turnID = fmt.Sprintf("%s-p%03d-ccb", payload.RunID, payload.PromptSeq)
	}
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "turn.start",
		TurnID:  turnID,
		Role:    "ccb",
		CLI:     "ccb",
		Content: "dispatching task to local CCB",
		Data: map[string]any{
			"target": target,
		},
	})
	if err := m.ensureCCBConfig(payload); err != nil {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "run.error",
			TurnID:  turnID,
			Role:    "ccb",
			CLI:     "ccb",
			Status:  store.OrchestrationFailed,
			Error:   err.Error(),
			Content: "failed to prepare local CCB config",
		})
		return
	}
	startOutput, err := m.startCCBRuntime(ctx, payload)
	if startOutput != "" {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.delta",
			TurnID:  turnID,
			Role:    "ccb",
			CLI:     "ccb",
			Content: startOutput,
		})
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.cancelled",
				Status: store.OrchestrationCanceled,
				Error:  "canceled",
			})
			return
		}
		m.emitCCBAgentConsoleSnapshots(ctx, payload, turnID, 80)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "run.error",
			TurnID:  turnID,
			Role:    "ccb",
			CLI:     "ccb",
			Status:  store.OrchestrationFailed,
			Error:   err.Error(),
			Content: strings.TrimSpace(startOutput),
		})
		return
	}
	ccbSocketPath := parseCCBSocketPath(startOutput)

	jobID, submitOutput, err := m.submitCCBJob(ctx, payload)
	if submitOutput != "" {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.delta",
			TurnID:  turnID,
			Role:    "ccb",
			CLI:     "ccb",
			Content: submitOutput,
		})
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.cancelled",
				Status: store.OrchestrationCanceled,
				Error:  "canceled",
			})
			return
		}
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "run.error",
			TurnID:  turnID,
			Role:    "ccb",
			CLI:     "ccb",
			Status:  store.OrchestrationFailed,
			Error:   err.Error(),
			Content: strings.TrimSpace(submitOutput),
		})
		return
	}
	if jobID == "" {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "run.error",
			TurnID:  turnID,
			Role:    "ccb",
			CLI:     "ccb",
			Status:  store.OrchestrationFailed,
			Error:   "ccb did not return a job id",
			Content: strings.TrimSpace(submitOutput),
		})
		return
	}
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "command.end",
		TurnID:  turnID,
		Role:    "ccb",
		CLI:     "ccb",
		Status:  "completed",
		Content: "ccb job accepted: " + jobID,
		Data: map[string]any{
			"id":      jobID,
			"command": "ccb ask",
			"target":  target,
			"output":  strings.TrimSpace(submitOutput),
		},
	})

	result, watchOutput, streamState, err := m.watchCCBJobWithEvents(ctx, payload, turnID, jobID, ccbSocketPath)
	if watchOutput != "" && !ccbWatchOutputWasStreamed(watchOutput) {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.delta",
			TurnID:  turnID,
			Role:    "ccb",
			CLI:     "ccb",
			Content: watchOutput,
		})
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || ctx.Err() != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.cancelled",
				Status: store.OrchestrationCanceled,
				Error:  "canceled",
			})
			return
		}
		m.emitCCBAgentConsoleSnapshots(ctx, payload, turnID, 80)
		errContent := strings.TrimSpace(watchOutput)
		if errContent == "" {
			errContent = ccbEmptyFinalReplySummary(result, streamState, target)
		}
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "run.error",
			TurnID:  turnID,
			Role:    "ccb",
			CLI:     "ccb",
			Status:  store.OrchestrationFailed,
			Error:   err.Error(),
			Content: errContent,
			Data: map[string]any{
				"jobId": jobID,
			},
		})
		return
	}
	reply, synthesizedEmptyReply := ccbFinalReply(result, watchOutput, streamState, target)
	result.Reply = reply
	m.emitCCBAgentConsoleSnapshots(ctx, payload, turnID, 80)
	tools := ccbAssessmentTools(streamState, jobID)
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "turn.end",
		TurnID:  turnID,
		Role:    "ccb",
		CLI:     "ccb",
		Status:  result.Status,
		Content: result.Reply,
		Data: map[string]any{
			"jobId":  jobID,
			"target": target,
		},
	})
	if strings.EqualFold(result.Status, "completed") {
		if synthesizedEmptyReply {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "run.error",
				Status:  store.OrchestrationFailed,
				CLI:     "ccb",
				Error:   "ccb completed without a final user-visible reply",
				Content: result.Reply,
				Data: map[string]any{
					"jobId": jobID,
				},
			})
			return
		}
		history := ccbAssessmentHistory(turnID, result.Reply, streamState, tools)
		workspaceAfter := snapshotWorkspace(m.cwd(payload), workspaceSnapshotIgnoredPaths(m.cfg)...)
		workspaceChanges := diffWorkspaceSnapshots(workspaceBefore, workspaceAfter)
		if reason, ok := unresolvedFinalRun(originalPrompt, history, workspaceChanges); ok {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "run.error",
				Status:  store.OrchestrationFailed,
				CLI:     "ccb",
				Error:   reason,
				Content: finalRunAssessmentSummary(originalPrompt, history, workspaceChanges, reason),
				Data: map[string]any{
					"jobId": jobID,
				},
			})
			return
		}
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "run.end",
			Status:  store.OrchestrationCompleted,
			CLI:     "ccb",
			Content: finalRunAssessmentSummary(originalPrompt, history, workspaceChanges, ""),
			Data: map[string]any{
				"jobId": jobID,
			},
		})
		return
	}
	if strings.EqualFold(result.Status, "cancelled") || strings.EqualFold(result.Status, "canceled") {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:   "run.cancelled",
			Status: store.OrchestrationCanceled,
			CLI:    "ccb",
			Error:  "ccb job canceled",
			Data: map[string]any{
				"jobId": jobID,
			},
		})
		return
	}
	errText := "ccb job ended with status " + result.Status
	if result.Status == "" {
		errText = "ccb job ended without a terminal status"
	}
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "run.error",
		Status:  store.OrchestrationFailed,
		CLI:     "ccb",
		Error:   errText,
		Content: result.Reply,
		Data: map[string]any{
			"jobId": jobID,
		},
	})
}

type ccbJobResult struct {
	Status string
	Reply  string
}

type ccbWatchStreamEvent struct {
	Line    string
	Content string
	Data    map[string]any
}

type ccbWatchStreamState struct {
	agentContent      map[string]string
	events            []ccbWatchStreamEvent
	jobAgents         map[string]string
	jobSessionPaths   map[string]string
	sessionLines      map[string]int
	toolStarts        map[string]time.Time
	terminalApprovals map[string]string
}

type ccbTraceReplyEvent struct {
	Role    string
	CLI     string
	Content string
	Data    map[string]any
}

func (m *OrchestrationManager) startCCBRuntime(ctx context.Context, payload protocol.OrchestrationStartPayload) (string, error) {
	args := []string{}
	if !bridgeBypassApprovalsAndSandbox(m.cfg) {
		args = append(args, "-s")
	}
	return m.runCCBCommand(ctx, payload, "", args...)
}

func (m *OrchestrationManager) submitCCBJob(ctx context.Context, payload protocol.OrchestrationStartPayload) (string, string, error) {
	args := []string{"ask", "--compact", m.ccbTarget()}
	out, err := m.runCCBCommand(ctx, payload, m.ccbPrompt(payload), args...)
	if err != nil {
		return "", out, err
	}
	jobID := parseCCBJobID(out)
	return jobID, out, nil
}

func ccbFinalReply(result ccbJobResult, watchOutput string, state *ccbWatchStreamState, target string) (string, bool) {
	if reply := strings.TrimSpace(result.Reply); reply != "" {
		return reply, false
	}
	if reply := ccbReadableWatchOutput(watchOutput); reply != "" {
		return reply, false
	}
	return ccbEmptyFinalReplySummary(result, state, target), true
}

func ccbReadableWatchOutput(output string) string {
	var lines []string
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || isCCBWatchMetadataLine(line) || isCCBOperationalLine(line) {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func isCCBOperationalLine(line string) bool {
	line = strings.TrimSpace(line)
	return strings.HasPrefix(line, "[CCB_") ||
		strings.HasPrefix(line, "accepted job=") ||
		strings.HasPrefix(line, "start_status:") ||
		strings.HasPrefix(line, "project:") ||
		strings.HasPrefix(line, "agents:") ||
		strings.HasPrefix(line, "socket_path:") ||
		strings.HasPrefix(line, "status:") ||
		strings.HasPrefix(line, "reply:")
}

func ccbEmptyFinalReplySummary(result ccbJobResult, state *ccbWatchStreamState, target string) string {
	status := strings.TrimSpace(result.Status)
	if status == "" {
		status = "unknown"
	}
	var lines []string
	lines = append(lines, "CCB ended without a final user-visible reply.")
	lines = append(lines, "Status: "+status+".")
	if target = strings.TrimSpace(target); target != "" {
		lines = append(lines, "Initial target: "+target+".")
	}
	lines = append(lines, ccbObservedAgentSummaryLines(state)...)
	lines = append(lines, "Raw CCB events and agent console snapshots are available above for audit. If this task expected a delegated result, check the local CCB callback state and Claude login.")
	return strings.Join(lines, "\n")
}

func ccbObservedAgentSummaryLines(state *ccbWatchStreamState) []string {
	if state == nil {
		return nil
	}
	type agentState struct {
		accepted  bool
		started   bool
		completed bool
		failed    bool
		text      bool
		callback  bool
		childJobs []string
	}
	agents := map[string]*agentState{}
	get := func(agent string) *agentState {
		agent = strings.ToLower(strings.TrimSpace(agent))
		if agent == "" {
			agent = "ccb"
		}
		if agents[agent] == nil {
			agents[agent] = &agentState{}
		}
		return agents[agent]
	}
	for _, event := range state.events {
		data := event.Data
		if data == nil {
			continue
		}
		agent := ccbString(data["agent"])
		if agent == "" {
			agent = ccbString(data["target"])
		}
		current := get(agent)
		switch ccbString(data["eventType"]) {
		case "job_accepted", "job_queued":
			current.accepted = true
		case "job_started":
			current.started = true
		case "completion_terminal", "job_completed":
			current.completed = true
		case "job_failed", "job_incomplete", "job_cancelled":
			current.failed = true
		case "job_delegated_callback", "callback_edge_created":
			current.callback = true
			if payload, _ := data["payload"].(map[string]any); payload != nil {
				for _, key := range []string{"callback_child_job_id", "child_job_id", "continuation_job_id"} {
					if child := ccbString(payload[key]); child != "" {
						current.childJobs = appendUniqueString(current.childJobs, child)
					}
				}
			}
		}
		if ccbString(data["contentKind"]) == "agent_text" {
			current.text = true
		}
	}
	names := make([]string, 0, len(agents))
	for name := range agents {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines []string
	for _, name := range names {
		state := agents[name]
		var parts []string
		if state.accepted {
			parts = append(parts, "accepted")
		}
		if state.started {
			parts = append(parts, "started")
		}
		if state.text {
			parts = append(parts, "streamed text")
		}
		if state.completed {
			parts = append(parts, "completed")
		}
		if state.failed {
			parts = append(parts, "failed/incomplete")
		}
		if state.callback {
			callback := "delegated callback"
			if len(state.childJobs) > 0 {
				callback += " (" + strings.Join(state.childJobs, ", ") + ")"
			}
			parts = append(parts, callback)
		}
		if len(parts) > 0 {
			lines = append(lines, fmt.Sprintf("Observed %s: %s.", name, strings.Join(parts, ", ")))
		}
	}
	return lines
}

func ccbAssessmentTools(state *ccbWatchStreamState, rootJobID string) []RunnerToolEvent {
	if state == nil {
		return nil
	}
	var tools []RunnerToolEvent
	seenSessions := map[string]bool{}
	for _, path := range state.jobSessionPaths {
		path = strings.TrimSpace(path)
		if path == "" || seenSessions[path] {
			continue
		}
		seenSessions[path] = true
		_, sessionTools := ccbReadProviderSessionEvents(path, 0)
		for _, tool := range sessionTools {
			if tool != nil {
				tools = append(tools, *tool)
			}
		}
	}
	if len(tools) == 0 && strings.TrimSpace(rootJobID) != "" {
		tools = append(tools, RunnerToolEvent{
			ID:      "ccb:" + rootJobID,
			Status:  "completed",
			Command: "ccb ask",
		})
	}
	return tools
}

func ccbAssessmentHistory(turnID, reply string, state *ccbWatchStreamState, tools []RunnerToolEvent) []orchestrationTurn {
	reply = strings.TrimSpace(reply)
	var agentText []string
	if state != nil {
		for _, event := range state.events {
			if event.Data == nil || ccbString(event.Data["contentKind"]) != "agent_text" {
				continue
			}
			content := strings.TrimSpace(event.Content)
			if content != "" && content != reply {
				agentText = append(agentText, content)
			}
		}
	}
	content := reply
	if len(agentText) > 0 {
		prefix := trimForPrompt(strings.Join(agentText, "\n\n"), 6000)
		if content != "" {
			content = prefix + "\n\nCCB final reply:\n" + content
		} else {
			content = prefix
		}
	}
	if extractHandoff(content) == "" {
		content = strings.TrimSpace(content + "\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=see CCB workspace diff; verified=see CCB command events; next=none; risks=none")
	}
	return []orchestrationTurn{newOrchestrationTurnRecord(turnID, "ccb", "ccb", content, tools)}
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

type ccbAgentRuntimeInfo struct {
	Agent      string
	PaneID     string
	State      string
	Health     string
	SocketPath string
}

type ccbTerminalPrompt struct {
	Type    string
	Input   string
	Command string
	Reason  string
}

func (m *OrchestrationManager) emitCCBAgentConsoleSnapshots(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID string, tail int) {
	if tail <= 0 {
		tail = 80
	}
	for _, agent := range []string{"codex", "claude"} {
		info, lines, err := m.ccbAgentConsoleSnapshot(ctx, payload, agent, tail)
		if err != nil || len(lines) == 0 {
			continue
		}
		content := fmt.Sprintf("CCB %s console snapshot:\n%s", agent, strings.Join(lines, "\n"))
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.delta",
			TurnID:  turnID,
			Role:    agent,
			CLI:     ccbAgentCLI(agent),
			Content: content,
			Data: map[string]any{
				"source":        "ccb",
				"contentKind":   "agent_console",
				"agent":         agent,
				"paneId":        info.PaneID,
				"state":         info.State,
				"health":        info.Health,
				"tail":          len(lines),
				"lastUpdatedAt": time.Now().UTC().Format(time.RFC3339),
			},
		})
	}
}

func (m *OrchestrationManager) ccbAgentConsoleSnapshot(ctx context.Context, payload protocol.OrchestrationStartPayload, agent string, tail int) (ccbAgentRuntimeInfo, []string, error) {
	info, err := m.ccbAgentRuntimeInfo(payload, agent)
	if err != nil {
		return info, nil, err
	}
	if info.SocketPath == "" || info.PaneID == "" {
		return info, nil, errors.New("ccb agent tmux pane unavailable")
	}
	if ctx == nil || ctx.Err() != nil {
		ctx = context.Background()
	}
	snapshotCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	args := []string{"-S", info.SocketPath, "capture-pane", "-p", "-t", info.PaneID, "-S", fmt.Sprintf("-%d", tail)}
	cmd := exec.CommandContext(snapshotCtx, "tmux", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return info, nil, err
	}
	lines := sanitizeCCBConsoleLines(out.String(), tail)
	return info, lines, nil
}

func (m *OrchestrationManager) ccbAgentRuntimeInfo(payload protocol.OrchestrationStartPayload, agent string) (ccbAgentRuntimeInfo, error) {
	root := expandHome(m.cwd(payload))
	if root == "" {
		root = "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	runtimePath := filepath.Join(root, ".ccb", "agents", safeFileName(agent), "runtime.json")
	raw, err := os.ReadFile(runtimePath)
	if err != nil {
		return ccbAgentRuntimeInfo{Agent: agent}, err
	}
	var payloadMap map[string]any
	if err := json.Unmarshal(raw, &payloadMap); err != nil {
		return ccbAgentRuntimeInfo{Agent: agent}, err
	}
	return ccbAgentRuntimeInfo{
		Agent:      firstNonEmptyCCBString(payloadMap, "agent_name", "provider"),
		PaneID:     firstNonEmptyCCBString(payloadMap, "pane_id", "active_pane_id"),
		State:      firstNonEmptyCCBString(payloadMap, "state", "lifecycle_state"),
		Health:     firstNonEmptyCCBString(payloadMap, "health", "pane_state"),
		SocketPath: firstNonEmptyCCBString(payloadMap, "tmux_socket_path"),
	}, nil
}

func (m *OrchestrationManager) handleCCBTerminalApprovals(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID string, state *ccbWatchStreamState) (bool, error) {
	if state == nil {
		return false, nil
	}
	if state.terminalApprovals == nil {
		state.terminalApprovals = make(map[string]string)
	}
	forwarded := false
	for _, agent := range []string{"codex", "claude"} {
		info, lines, err := m.ccbAgentConsoleSnapshot(ctx, payload, agent, 40)
		if err != nil || len(lines) == 0 || info.SocketPath == "" || info.PaneID == "" {
			continue
		}
		prompt, ok := detectCCBTerminalPrompt(lines)
		if !ok {
			continue
		}
		key := ccbTerminalApprovalKey(info, prompt, lines)
		if state.terminalApprovals[key] != "" {
			continue
		}
		state.terminalApprovals[key] = "pending"
		requester := orchestrationApprovalRequester{
			manager: m,
			runID:   payload.RunID,
			turnID:  turnID,
			role:    agent,
			cli:     ccbAgentCLI(agent),
			cwd:     m.cwd(payload),
		}
		params, _ := json.Marshal(map[string]any{
			"agent":  agent,
			"paneId": info.PaneID,
			"input":  prompt.Input,
			"type":   prompt.Type,
		})
		res, err := requester.RequestApproval(ctx, protocol.ApprovalRequestPayload{
			RequestID: safeRequestID("apr_ccb_"+payload.RunID+"_"+agent+"_"+prompt.Type, key),
			Kind:      "ccb.terminal_prompt",
			Command:   prompt.Command,
			Reason:    prompt.Reason,
			Params:    params,
		})
		if err != nil {
			return forwarded, err
		}
		decision := strings.ToLower(strings.TrimSpace(res.Decision))
		state.terminalApprovals[key] = decision
		if decision != "accept" {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "turn.delta",
				TurnID:  turnID,
				Role:    agent,
				CLI:     ccbAgentCLI(agent),
				Content: "CCB terminal prompt was not approved in the browser.",
				Data: map[string]any{
					"source":      "ccb",
					"contentKind": "terminal_approval",
					"agent":       agent,
					"decision":    decision,
				},
			})
			continue
		}
		if err := sendCCBTerminalInput(ctx, info.SocketPath, info.PaneID, prompt.Input); err != nil {
			return forwarded, err
		}
		forwarded = true
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.delta",
			TurnID:  turnID,
			Role:    agent,
			CLI:     ccbAgentCLI(agent),
			Content: fmt.Sprintf("Browser approved CCB %s terminal prompt; forwarded %s.", agent, prompt.Command),
			Data: map[string]any{
				"source":      "ccb",
				"contentKind": "terminal_approval",
				"agent":       agent,
				"paneId":      info.PaneID,
				"decision":    decision,
				"input":       prompt.Input,
			},
		})
	}
	return forwarded, nil
}

func (m *OrchestrationManager) ccbAgentConsoleHasTerminalPrompt(ctx context.Context, payload protocol.OrchestrationStartPayload) bool {
	for _, agent := range []string{"codex", "claude"} {
		_, lines, err := m.ccbAgentConsoleSnapshot(ctx, payload, agent, 40)
		if err != nil || len(lines) == 0 {
			continue
		}
		if _, ok := detectCCBTerminalPrompt(lines); ok {
			return true
		}
	}
	return false
}

func detectCCBTerminalPrompt(lines []string) (ccbTerminalPrompt, bool) {
	joined := strings.ToLower(strings.Join(lines, "\n"))
	reason := ccbTerminalPromptReason(lines)
	if strings.Contains(joined, "do you trust the contents of this directory") &&
		(strings.Contains(joined, "yes, continue") || strings.Contains(joined, "press enter to continue")) {
		return ccbTerminalPrompt{
			Type:    "workspace_trust",
			Input:   "Enter",
			Command: "press Enter to trust and continue",
			Reason:  reason,
		}, true
	}
	if strings.Contains(joined, "press enter to continue") {
		return ccbTerminalPrompt{
			Type:    "continue",
			Input:   "Enter",
			Command: "press Enter to continue",
			Reason:  reason,
		}, true
	}
	return ccbTerminalPrompt{}, false
}

func ccbTerminalPromptReason(lines []string) string {
	var kept []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		kept = append(kept, line)
	}
	if len(kept) > 12 {
		kept = kept[len(kept)-12:]
	}
	const max = 2000
	reason := strings.Join(kept, "\n")
	if len(reason) > max {
		reason = reason[len(reason)-max:]
	}
	return reason
}

func ccbTerminalApprovalKey(info ccbAgentRuntimeInfo, prompt ccbTerminalPrompt, lines []string) string {
	tail := ccbTerminalPromptReason(lines)
	if len(tail) > 300 {
		tail = tail[len(tail)-300:]
	}
	return strings.Join([]string{strings.ToLower(strings.TrimSpace(info.Agent)), info.SocketPath, info.PaneID, prompt.Type, prompt.Input, tail}, "\x00")
}

func safeRequestID(prefix, key string) string {
	prefix = safeOrchestrationFileName.ReplaceAllString(prefix, "_")
	sum := sha1.Sum([]byte(key))
	return strings.Trim(prefix, "_") + "_" + hex.EncodeToString(sum[:])[:16]
}

func sendCCBTerminalInput(ctx context.Context, socketPath, paneID, input string) error {
	socketPath = strings.TrimSpace(socketPath)
	paneID = strings.TrimSpace(paneID)
	input = strings.TrimSpace(input)
	if socketPath == "" || paneID == "" {
		return errors.New("ccb terminal pane is unavailable")
	}
	if input == "" {
		input = "Enter"
	}
	sendCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(sendCtx, "tmux", ccbTerminalInputArgs(socketPath, paneID, input)...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(out.String())
		if msg != "" {
			return fmt.Errorf("send ccb terminal input: %w: %s", err, msg)
		}
		return fmt.Errorf("send ccb terminal input: %w", err)
	}
	return nil
}

func ccbTerminalInputArgs(socketPath, paneID, input string) []string {
	return []string{"-S", socketPath, "send-keys", "-t", paneID, input}
}

func sanitizeCCBConsoleLines(output string, tail int) []string {
	output = stripANSI(output)
	rawLines := strings.Split(output, "\n")
	lines := make([]string, 0, len(rawLines))
	for _, raw := range rawLines {
		line := strings.TrimRight(raw, " \t\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		line = redactSensitiveText(line)
		if len(line) > 500 {
			line = line[:500] + "..."
		}
		lines = append(lines, line)
	}
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return lines
}

func stripANSI(value string) string {
	return ansiControlPattern.ReplaceAllString(value, "")
}

func redactSensitiveText(value string) string {
	out := bearerTokenPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	for _, pattern := range sensitiveValuePatterns {
		out = pattern.ReplaceAllString(out, "$1[REDACTED]")
	}
	return out
}

func (m *OrchestrationManager) watchCCBJob(ctx context.Context, payload protocol.OrchestrationStartPayload, jobID string) (ccbJobResult, string, error) {
	turnID := payload.RunID + "-ccb"
	if payload.PromptSeq > 0 {
		turnID = fmt.Sprintf("%s-p%03d-ccb", payload.RunID, payload.PromptSeq)
	}
	result, out, _, err := m.watchCCBJobWithEvents(ctx, payload, turnID, jobID, "")
	return result, out, err
}

func (m *OrchestrationManager) watchCCBJobWithEvents(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, jobID, socketPath string) (ccbJobResult, string, *ccbWatchStreamState, error) {
	timeout := m.ccbTimeout()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	streamState := &ccbWatchStreamState{}
	emitEvent := func(event ccbWatchStreamEvent) {
		streamState.events = append(streamState.events, event)
		cli := "ccb"
		role := "ccb"
		if agent, _ := event.Data["agent"].(string); agent != "" {
			role = agent
			cli = ccbAgentCLI(agent)
		}
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.delta",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: event.Content,
			Data:    event.Data,
		})
		ccbRecordStreamState(streamState, event.Data)
	}
	if resolvedSocketPath := m.resolveCCBSocketPath(payload, socketPath); resolvedSocketPath != "" {
		result, out, err := m.watchCCBJobViaSocket(ctx, payload, turnID, jobID, resolvedSocketPath, streamState, emitEvent)
		if err == nil {
			m.emitCCBTraceEvents(ctx, payload, turnID, jobID, resolvedSocketPath, streamState, emitEvent, &result)
			return result, out, streamState, nil
		}
		if errors.Is(ctx.Err(), context.Canceled) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return result, out, streamState, fmt.Errorf("ccb job %s timed out after %s", jobID, timeout)
			}
			return result, out, streamState, err
		}
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.delta",
			TurnID:  turnID,
			Role:    "ccb",
			CLI:     "ccb",
			Content: "CCB structured watch unavailable; falling back to CLI watch: " + err.Error(),
			Data: map[string]any{
				"source":      "ccb",
				"contentKind": "watch_fallback",
				"socketPath":  resolvedSocketPath,
				"jobId":       jobID,
			},
		})
	}
	out, err := m.runCCBCommandStreaming(ctx, payload, "", func(event ccbWatchStreamEvent) {
		emitEvent(event)
	}, streamState, "pend", "--watch", jobID)
	result := parseCCBWatchResult(out)
	if result.Status == "" && m.ccbAgentConsoleHasTerminalPrompt(ctx, payload) {
		if _, approvalErr := m.handleCCBTerminalApprovals(ctx, payload, turnID, streamState); approvalErr != nil {
			return result, out, streamState, approvalErr
		}
		out, err = m.runCCBCommandStreaming(ctx, payload, "", func(event ccbWatchStreamEvent) {
			emitEvent(event)
		}, streamState, "pend", "--watch", jobID)
		result = parseCCBWatchResult(out)
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return result, out, streamState, fmt.Errorf("ccb job %s timed out after %s", jobID, timeout)
		}
		return result, out, streamState, err
	}
	traceOutput, traceErr := m.fetchCCBTrace(ctx, payload, jobID)
	if traceErr == nil && traceOutput != "" {
		for _, event := range ccbTraceReplyEvents(traceOutput) {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "turn.delta",
				TurnID:  turnID,
				Role:    event.Role,
				CLI:     event.CLI,
				Content: event.Content,
				Data:    event.Data,
			})
		}
	}
	return result, out, streamState, nil
}

type ccbSocketWatchJob struct {
	Cursor          int
	Terminal        bool
	PendingCallback bool
}

type ccbWatchBatch struct {
	JobID              string
	AgentName          string
	TargetName         string
	Cursor             int
	Terminal           bool
	Status             string
	Reply              string
	CompletionReason   string
	VisibleReplySource string
	Events             []map[string]any
}

func (m *OrchestrationManager) watchCCBJobViaSocket(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, jobID, socketPath string, streamState *ccbWatchStreamState, emitEvent func(ccbWatchStreamEvent)) (ccbJobResult, string, error) {
	jobs := map[string]*ccbSocketWatchJob{jobID: {}}
	rootBatch := ccbWatchBatch{JobID: jobID}
	pollInterval := 100 * time.Millisecond
	consecutiveErrors := 0
	for {
		progressed := false
		for _, currentJobID := range sortedCCBJobIDs(jobs) {
			job := jobs[currentJobID]
			if job.Terminal && !(currentJobID == jobID && job.PendingCallback && strings.TrimSpace(rootBatch.Reply) == "") {
				continue
			}
			batch, err := ccbSocketWatch(ctx, socketPath, currentJobID, job.Cursor)
			if err != nil {
				if ccbSocketWatchRetriableError(err) && ctx.Err() == nil && consecutiveErrors < 50 {
					consecutiveErrors++
					progressed = false
					break
				}
				return ccbJobResult{Status: rootBatch.Status, Reply: rootBatch.Reply}, "", err
			}
			consecutiveErrors = 0
			if batch.JobID == "" {
				batch.JobID = currentJobID
			}
			if batch.Cursor > job.Cursor {
				job.Cursor = batch.Cursor
				progressed = true
			}
			if currentJobID == jobID && batch.Reply != rootBatch.Reply {
				progressed = true
			}
			for _, record := range batch.Events {
				for _, relatedJobID := range ccbRelatedJobIDs(record) {
					if _, ok := jobs[relatedJobID]; !ok {
						jobs[relatedJobID] = &ccbSocketWatchJob{}
						progressed = true
					}
				}
				if currentJobID == jobID && strings.TrimSpace(batch.Reply) == "" {
					if reply := ccbTerminalReplyFromRecord(record); reply != "" {
						batch.Reply = reply
						progressed = true
					}
				}
				if event, ok := ccbStructuredWatchStreamEvent(record, streamState); ok {
					emitEvent(event)
				}
			}
			if currentJobID == jobID {
				rootBatch = batch
			}
			m.emitCCBProviderSessionEvents(payload, turnID, streamState, currentJobID, batch.AgentName)
			if _, err := m.handleCCBTerminalApprovals(ctx, payload, turnID, streamState); err != nil {
				return ccbJobResult{Status: rootBatch.Status, Reply: rootBatch.Reply}, "", err
			}
			if batch.Terminal {
				if !job.Terminal {
					progressed = true
				}
				job.Terminal = true
				job.PendingCallback = currentJobID == jobID && ccbBatchCallbackPending(batch)
			}
		}
		if ccbSocketWatchComplete(jobs, jobID, rootBatch) {
			return ccbJobResult{Status: rootBatch.Status, Reply: rootBatch.Reply}, "", nil
		}
		if err := ctx.Err(); err != nil {
			return ccbJobResult{Status: rootBatch.Status, Reply: rootBatch.Reply}, "", err
		}
		if !progressed {
			if _, err := m.handleCCBTerminalApprovals(ctx, payload, turnID, streamState); err != nil {
				return ccbJobResult{Status: rootBatch.Status, Reply: rootBatch.Reply}, "", err
			}
			timer := time.NewTimer(pollInterval)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ccbJobResult{Status: rootBatch.Status, Reply: rootBatch.Reply}, "", ctx.Err()
			case <-timer.C:
			}
		}
	}
}

func ccbSocketWatchRetriableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unterminated string") ||
		strings.Contains(msg, "unexpected end of json input") ||
		strings.Contains(msg, "invalid character") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "resource temporarily unavailable") {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func ccbSocketWatch(ctx context.Context, socketPath, jobID string, cursor int) (ccbWatchBatch, error) {
	payload, err := ccbSocketRequest(ctx, socketPath, "watch", map[string]any{
		"target": jobID,
		"cursor": cursor,
	})
	if err != nil {
		return ccbWatchBatch{}, err
	}
	return ccbWatchBatchFromPayload(payload), nil
}

func ccbSocketTrace(ctx context.Context, socketPath, jobID string) (map[string]any, error) {
	return ccbSocketRequest(ctx, socketPath, "trace", map[string]any{"target": jobID})
}

func ccbSocketRequest(ctx context.Context, socketPath, op string, request map[string]any) (map[string]any, error) {
	if strings.TrimSpace(socketPath) == "" {
		return nil, errors.New("ccb socket path is empty")
	}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{}).DialContext(reqCtx, "unix", socketPath)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if deadline, ok := reqCtx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	body, err := json.Marshal(map[string]any{
		"api_version": 2,
		"op":          op,
		"request":     request,
	})
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	if _, err := conn.Write(body); err != nil {
		return nil, err
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	var response map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(line), &response); err != nil {
		return nil, err
	}
	if !ccbBool(response["ok"]) {
		if msg := ccbString(response["error"]); msg != "" {
			return nil, errors.New(msg)
		}
		return nil, errors.New("ccbd request failed")
	}
	delete(response, "api_version")
	delete(response, "ok")
	return response, nil
}

func ccbWatchBatchFromPayload(payload map[string]any) ccbWatchBatch {
	return ccbWatchBatch{
		JobID:              ccbString(payload["job_id"]),
		AgentName:          ccbString(payload["agent_name"]),
		TargetName:         ccbString(payload["target_name"]),
		Cursor:             ccbInt(payload["cursor"]),
		Terminal:           ccbBool(payload["terminal"]),
		Status:             ccbString(payload["status"]),
		Reply:              ccbString(payload["reply"]),
		CompletionReason:   ccbString(payload["completion_reason"]),
		VisibleReplySource: ccbString(payload["visible_reply_source"]),
		Events:             ccbMapSlice(payload["events"]),
	}
}

func ccbBatchCallbackPending(batch ccbWatchBatch) bool {
	return strings.EqualFold(batch.CompletionReason, "callback_pending") ||
		strings.EqualFold(batch.VisibleReplySource, "callback_delegated_pending")
}

func ccbTerminalReplyFromRecord(record map[string]any) string {
	eventType := ccbString(record["type"])
	if eventType != "completion_terminal" && eventType != "job_completed" {
		return ""
	}
	payload, _ := record["payload"].(map[string]any)
	if payload == nil {
		return ""
	}
	return strings.TrimSpace(firstNonEmptyCCBString(payload,
		"reply",
		"last_agent_message",
		"final_answer",
		"result_text",
		"text",
	))
}

func sortedCCBJobIDs(jobs map[string]*ccbSocketWatchJob) []string {
	ids := make([]string, 0, len(jobs))
	for id := range jobs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func ccbAllSocketWatchJobsTerminal(jobs map[string]*ccbSocketWatchJob) bool {
	for _, job := range jobs {
		if job == nil || !job.Terminal {
			return false
		}
	}
	return true
}

func ccbSocketWatchComplete(jobs map[string]*ccbSocketWatchJob, rootJobID string, rootBatch ccbWatchBatch) bool {
	root := jobs[rootJobID]
	if root == nil || !root.Terminal {
		return false
	}
	if strings.TrimSpace(rootBatch.Reply) != "" {
		return true
	}
	if root.PendingCallback {
		return ccbHasRelatedSocketWatchJob(jobs, rootJobID) && ccbAllSocketWatchJobsTerminal(jobs)
	}
	return true
}

func ccbHasRelatedSocketWatchJob(jobs map[string]*ccbSocketWatchJob, rootJobID string) bool {
	for jobID := range jobs {
		if jobID != rootJobID {
			return true
		}
	}
	return false
}

func ccbRecordStreamState(state *ccbWatchStreamState, data map[string]any) {
	if state == nil || data == nil {
		return
	}
	jobID := ccbString(data["jobId"])
	if jobID == "" {
		return
	}
	agent := strings.ToLower(strings.TrimSpace(ccbString(data["agent"])))
	if agent != "" {
		if state.jobAgents == nil {
			state.jobAgents = make(map[string]string)
		}
		state.jobAgents[jobID] = agent
	}
	if sessionPath := ccbSessionPathFromEventData(data); sessionPath != "" {
		if state.jobSessionPaths == nil {
			state.jobSessionPaths = make(map[string]string)
		}
		state.jobSessionPaths[jobID] = expandHome(sessionPath)
	}
}

func ccbSessionPathFromEventData(data map[string]any) string {
	payload, _ := data["payload"].(map[string]any)
	if payload == nil {
		return ""
	}
	itemPayload, _ := payload["payload"].(map[string]any)
	if sessionPath := firstNonEmptyCCBString(itemPayload, "session_path"); sessionPath != "" {
		return sessionPath
	}
	return firstNonEmptyCCBString(payload, "session_path")
}

func (m *OrchestrationManager) emitCCBProviderSessionEvents(payload protocol.OrchestrationStartPayload, turnID string, state *ccbWatchStreamState, jobID, agentName string) {
	if state == nil {
		return
	}
	if state.sessionLines == nil {
		state.sessionLines = make(map[string]int)
	}
	path := ccbSessionPathForJobEvent(state, jobID)
	if path == "" {
		return
	}
	agent := strings.ToLower(strings.TrimSpace(agentName))
	if agent == "" {
		agent = strings.ToLower(strings.TrimSpace(ccbAgentForJobEvent(state, jobID)))
	}
	if agent == "" {
		agent = "ccb"
	}
	nextLine, events := ccbReadProviderSessionEvents(path, state.sessionLines[path])
	if nextLine <= state.sessionLines[path] {
		return
	}
	state.sessionLines[path] = nextLine
	if state.toolStarts == nil {
		state.toolStarts = make(map[string]time.Time)
	}
	for _, tool := range events {
		if tool == nil {
			continue
		}
		tool.ID = ccbProviderToolID(jobID, agent, tool.ID)
		stampToolTiming(tool, state.toolStarts)
		if tool.Command != "" {
			tool.Command = redactSensitiveText(stripANSI(tool.Command))
		}
		if tool.Output != "" {
			tool.Output = ccbTrimProviderToolOutput(redactSensitiveText(stripANSI(tool.Output)))
		}
		m.emitTool(payload.RunID, turnID, agent, ccbAgentCLI(agent), tool)
	}
}

func ccbSessionPathForJobEvent(state *ccbWatchStreamState, jobID string) string {
	if state == nil {
		return ""
	}
	if path := strings.TrimSpace(state.jobSessionPaths[jobID]); path != "" {
		return expandHome(path)
	}
	for i := len(state.events) - 1; i >= 0; i-- {
		data := state.events[i].Data
		if data == nil || ccbString(data["jobId"]) != jobID {
			continue
		}
		if sessionPath := ccbSessionPathFromEventData(data); sessionPath != "" {
			return expandHome(sessionPath)
		}
	}
	return ""
}

func ccbAgentForJobEvent(state *ccbWatchStreamState, jobID string) string {
	if state == nil {
		return ""
	}
	if agent := strings.TrimSpace(state.jobAgents[jobID]); agent != "" {
		return agent
	}
	for i := len(state.events) - 1; i >= 0; i-- {
		data := state.events[i].Data
		if data == nil || ccbString(data["jobId"]) != jobID {
			continue
		}
		if agent := ccbString(data["agent"]); agent != "" {
			return agent
		}
	}
	return ""
}

func ccbReadProviderSessionEvents(path string, startLine int) (int, []*RunnerToolEvent) {
	file, err := os.Open(path)
	if err != nil {
		return startLine, nil
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	lineNo := 0
	var events []*RunnerToolEvent
	for scanner.Scan() {
		lineNo++
		if lineNo <= startLine {
			continue
		}
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if event := ccbProviderSessionToolEvent(line); event != nil {
			events = append(events, event)
		}
	}
	if lineNo < startLine {
		return startLine, events
	}
	return lineNo, events
}

func ccbProviderSessionToolEvent(line []byte) *RunnerToolEvent {
	var msg map[string]any
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil
	}
	payload, _ := msg["payload"].(map[string]any)
	if payload == nil {
		return nil
	}
	switch ccbString(payload["type"]) {
	case "function_call":
		return ccbFunctionCallToolEvent(msg, payload)
	case "function_call_output":
		return ccbFunctionCallOutputToolEvent(msg, payload)
	default:
		return nil
	}
}

func ccbFunctionCallToolEvent(msg, payload map[string]any) *RunnerToolEvent {
	name := ccbString(payload["name"])
	callID := ccbString(payload["call_id"])
	argsText := ccbString(payload["arguments"])
	command := ccbProviderToolCommand(name, argsText)
	if command == "" && callID == "" {
		return nil
	}
	return &RunnerToolEvent{
		ID:        callID,
		Status:    "in_progress",
		Command:   command,
		StartedAt: ccbParseTimestamp(ccbString(msg["timestamp"])),
	}
}

func ccbFunctionCallOutputToolEvent(msg, payload map[string]any) *RunnerToolEvent {
	callID := ccbString(payload["call_id"])
	output := ccbString(payload["output"])
	if callID == "" && output == "" {
		return nil
	}
	status := "completed"
	if ccbProviderToolOutputFailed(output) {
		status = "failed"
	}
	return &RunnerToolEvent{
		ID:          callID,
		Status:      status,
		Output:      ccbProviderToolReadableOutput(output),
		CompletedAt: ccbParseTimestamp(ccbString(msg["timestamp"])),
	}
}

func ccbProviderToolCommand(name, argsText string) string {
	name = strings.TrimSpace(name)
	if argsText == "" {
		return name
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(argsText), &args); err != nil {
		return strings.TrimSpace(name + " " + argsText)
	}
	if name == "exec_command" {
		if cmd := ccbString(args["cmd"]); cmd != "" {
			return cmd
		}
	}
	if name == "write_stdin" {
		sessionID := ccbString(args["session_id"])
		if sessionID == "" {
			sessionID = fmt.Sprint(args["session_id"])
		}
		if sessionID != "" && sessionID != "<nil>" {
			return "poll running command session " + sessionID
		}
	}
	if pretty, err := json.Marshal(args); err == nil {
		return strings.TrimSpace(name + " " + string(pretty))
	}
	return strings.TrimSpace(name + " " + argsText)
}

func ccbProviderToolReadableOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	var lines []string
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		if strings.HasPrefix(line, "Chunk ID:") ||
			strings.HasPrefix(line, "Wall time:") ||
			strings.HasPrefix(line, "Original token count:") ||
			strings.HasPrefix(line, "Output:") {
			continue
		}
		lines = append(lines, line)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func ccbProviderToolOutputFailed(output string) bool {
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "Process exited with code ") {
			return !strings.HasSuffix(line, " 0")
		}
	}
	return false
}

func ccbProviderToolID(jobID, agent, callID string) string {
	if strings.TrimSpace(callID) == "" {
		callID = "unknown"
	}
	parts := []string{"ccb", strings.TrimSpace(jobID), strings.TrimSpace(agent), strings.TrimSpace(callID)}
	return strings.Join(parts, ":")
}

func ccbTrimProviderToolOutput(output string) string {
	const max = 12000
	output = strings.TrimSpace(output)
	if len(output) <= max {
		return output
	}
	return output[:max] + "\n... output truncated ..."
}

func ccbParseTimestamp(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed
		}
	}
	return time.Time{}
}

func (m *OrchestrationManager) emitCCBTraceEvents(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, jobID, socketPath string, streamState *ccbWatchStreamState, emitEvent func(ccbWatchStreamEvent), result *ccbJobResult) {
	traceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	tracePayload, err := ccbSocketTrace(traceCtx, socketPath, jobID)
	if err == nil {
		if result != nil && strings.TrimSpace(result.Reply) == "" {
			result.Reply = ccbTraceFinalReply(tracePayload)
		}
		for _, event := range ccbTraceReplyEventsFromPayload(tracePayload, streamState) {
			emitEvent(ccbWatchStreamEvent{Content: event.Content, Data: event.Data})
		}
		return
	}
	traceOutput, traceErr := m.fetchCCBTrace(ctx, payload, jobID)
	if traceErr == nil && traceOutput != "" {
		for _, event := range ccbTraceReplyEvents(traceOutput) {
			emitEvent(ccbWatchStreamEvent{Content: event.Content, Data: event.Data})
		}
	}
}

func (m *OrchestrationManager) fetchCCBTrace(ctx context.Context, payload protocol.OrchestrationStartPayload, jobID string) (string, error) {
	traceCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return m.runCCBCommand(traceCtx, payload, "", "trace", jobID)
}

func (m *OrchestrationManager) runCCBCommand(ctx context.Context, payload protocol.OrchestrationStartPayload, stdin string, args ...string) (string, error) {
	return m.runCCBCommandStreaming(ctx, payload, stdin, nil, nil, args...)
}

func (m *OrchestrationManager) runCCBCommandStreaming(ctx context.Context, payload protocol.OrchestrationStartPayload, stdin string, onLine func(ccbWatchStreamEvent), streamState *ccbWatchStreamState, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, m.ccbPath(), args...)
	if cwd := m.cwd(payload); cwd != "" {
		cmd.Dir = cwd
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	cmd.Env = append(orchestrationCCBEnv(os.Environ(), m.cfg),
		"CCB_NO_ATTACH=1",
		fmt.Sprintf("CCB_WATCH_TIMEOUT_S=%d", int(m.ccbTimeout().Seconds())),
	)
	var out bytes.Buffer
	if onLine == nil {
		cmd.Stdout = &out
		cmd.Stderr = &out
		err := cmd.Run()
		return strings.TrimSpace(out.String()), err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	var wg sync.WaitGroup
	var scanMu sync.Mutex
	scan := func(r io.Reader) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := scanner.Text()
			scanMu.Lock()
			if out.Len() > 0 {
				out.WriteByte('\n')
			}
			out.WriteString(line)
			scanMu.Unlock()
			if event, ok := ccbWatchStreamLineEvent(line, streamState); ok {
				onLine(event)
			}
		}
	}
	wg.Add(2)
	go scan(stdout)
	go scan(stderr)
	wg.Wait()
	err = cmd.Wait()
	return strings.TrimSpace(out.String()), err
}

func orchestrationCCBEnv(base []string, cfg *config.Config) []string {
	env := append([]string{}, base...)
	if cfg == nil {
		return env
	}
	path := envValue(env, "PATH")
	path = prependPathDirs(path,
		executableDir(cfg.Bridge.CodexPath),
		executableDir(cfg.Bridge.ClaudePath),
		executableDir(cfg.Bridge.CCBPath),
	)
	env = setEnvValue(env, "PATH", path)
	if cfg.Bridge.CodexPath != "" {
		env = setEnvValue(env, "BRIDGE_CODEX_PATH", cfg.Bridge.CodexPath)
	}
	if cfg.Bridge.ClaudePath != "" {
		env = setEnvValue(env, "BRIDGE_CLAUDE_PATH", cfg.Bridge.ClaudePath)
	}
	if cfg.Bridge.CCBPath != "" {
		env = setEnvValue(env, "BRIDGE_CCB_PATH", cfg.Bridge.CCBPath)
	}
	return env
}

func executableDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !strings.ContainsRune(path, filepath.Separator) {
		return ""
	}
	return filepath.Dir(expandHome(path))
}

func prependPathDirs(path string, dirs ...string) string {
	parts := splitPathList(path)
	seen := make(map[string]bool, len(parts)+len(dirs))
	for _, part := range parts {
		if part != "" {
			seen[part] = true
		}
	}
	prefix := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || seen[dir] {
			continue
		}
		seen[dir] = true
		prefix = append(prefix, dir)
	}
	return strings.Join(append(prefix, parts...), string(os.PathListSeparator))
}

func splitPathList(path string) []string {
	if path == "" {
		return nil
	}
	return strings.Split(path, string(os.PathListSeparator))
}

func envValue(env []string, key string) string {
	prefix := key + "="
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			return strings.TrimPrefix(env[i], prefix)
		}
	}
	return ""
}

func setEnvValue(env []string, key, value string) []string {
	prefix := key + "="
	entry := prefix + value
	for i := len(env) - 1; i >= 0; i-- {
		if strings.HasPrefix(env[i], prefix) {
			env[i] = entry
			return env
		}
	}
	return append(env, entry)
}

func (m *OrchestrationManager) ensureCCBConfig(payload protocol.OrchestrationStartPayload) error {
	cwd := m.cwd(payload)
	if cwd == "" {
		cwd = "."
	}
	root := expandHome(cwd)
	configPath := filepath.Join(root, ".ccb", "ccb.config")
	if _, err := os.Stat(configPath); err == nil {
		raw, err := os.ReadFile(configPath)
		if err != nil {
			return err
		}
		return validateBridgeCCBConfig(configPath, string(raw))
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return err
	}
	const defaultConfig = "codex:codex, claude:claude\n"
	return os.WriteFile(configPath, []byte(defaultConfig), 0o644)
}

func validateBridgeCCBConfig(path, text string) error {
	agents := make(map[string]string)
	currentAgent := ""
	for _, rawLine := range strings.Split(text, "\n") {
		line := stripCCBConfigComment(rawLine)
		if line == "" {
			continue
		}
		if match := ccbTOMLAgentPattern.FindStringSubmatch(line); len(match) == 2 {
			currentAgent = strings.ToLower(strings.TrimSpace(match[1]))
			continue
		}
		if strings.HasPrefix(line, "[") {
			currentAgent = ""
			continue
		}
		if match := ccbTOMLProviderPattern.FindStringSubmatch(line); len(match) == 2 {
			if currentAgent != "" {
				agents[currentAgent] = strings.ToLower(strings.TrimSpace(match[1]))
			}
			continue
		}
		if strings.Contains(line, "=") {
			continue
		}
		for _, match := range ccbCompactAgentPattern.FindAllStringSubmatch(line, -1) {
			if len(match) == 3 {
				agents[strings.ToLower(strings.TrimSpace(match[1]))] = strings.ToLower(strings.TrimSpace(match[2]))
			}
		}
	}
	if len(agents) == 2 && agents["codex"] == "codex" && agents["claude"] == "claude" {
		return nil
	}
	return fmt.Errorf("%s must declare only the CCB agents `codex:codex, claude:claude` for Codex Bridge orchestration; adjust it or remove it so Bridge can create the minimal config", path)
}

func stripCCBConfigComment(line string) string {
	line = strings.TrimSpace(line)
	if line == "" {
		return ""
	}
	if before, _, ok := strings.Cut(line, "#"); ok {
		line = before
	}
	if before, _, ok := strings.Cut(line, "//"); ok {
		line = before
	}
	return strings.TrimSpace(line)
}

func (m *OrchestrationManager) ccbPrompt(payload protocol.OrchestrationStartPayload) string {
	var b strings.Builder
	if strings.TrimSpace(payload.Context) != "" {
		b.WriteString("Previous conversation context:\n")
		b.WriteString(strings.TrimSpace(payload.Context))
		b.WriteString("\n\n")
	}
	if payload.Resume {
		b.WriteString("This is a continuation of the same user-visible task. Use the context above when relevant.\n\n")
	}
	b.WriteString(strings.TrimSpace(payload.Prompt))
	b.WriteString("\n\n")
	b.WriteString("Use only the local CCB Codex and Claude Code agents for any coordination. Do not involve Gemini, OpenCode, Droid, or other CLI providers.\n")
	b.WriteString("Codex Bridge will independently assess the final CCB result against the user's real acceptance criterion. Return a final user-visible conclusion that includes concrete files changed, commands run, blockers, risks, verification, and next actions only when relevant.\n")
	if proofTask := formalProofTaskGuidance(payload.Prompt, payload.Mode, "implementer"); proofTask != "" {
		b.WriteString("\n")
		b.WriteString(proofTask)
		b.WriteString("\n")
	}
	if proofVerifier := formalProofVerifierGuidance(payload.Prompt, payload.Mode); proofVerifier != "" {
		b.WriteString("\n")
		b.WriteString(proofVerifier)
		b.WriteString("\n")
	}
	if looksLikeCoqUploadProofBenchmark(payload.Prompt) {
		b.WriteString("\nBefore returning a completed final answer for this Coq upload benchmark, explicitly report these evidence dimensions: Model.thy/Termination.thy/ROOT input mapping, new Coq project folder path, make/coqc result, source-only placeholder scan result, Coq Print Assumptions showing Closed under the global context, and termination modify_lin original obligation audit. Scan source for Axiom, Parameter, Conjecture, Admitted, admit, Abort, sorry, TODO, placeholder, quick_and_dirty, Guard Checking, and bypass_check. If modify_lin_fuel/default_fuel or any bounded fuel wrapper exists, mark the task unresolved unless equivalence, decrease/well-foundedness, and fuel sufficiency are proved in the same result.\n")
	}
	b.WriteString("End the final answer with the compact lines:\n")
	b.WriteString(orchestrationMsgContract)
	b.WriteByte('\n')
	b.WriteString(orchestrationHandoffContract)
	return b.String()
}

func (m *OrchestrationManager) ccbPath() string {
	return bridgeCCBPath(m.cfg)
}

func (m *OrchestrationManager) ccbTarget() string {
	target := strings.TrimSpace(m.cfg.Bridge.CCBTarget)
	if target == "" {
		return "codex"
	}
	return target
}

func (m *OrchestrationManager) ccbTimeout() time.Duration {
	if m.cfg.Bridge.CCBTimeout.Duration > 0 {
		return m.cfg.Bridge.CCBTimeout.Duration
	}
	return time.Hour
}

var ccbJobIDPattern = regexp.MustCompile(`\bjob_[A-Za-z0-9_-]+\b`)
var ccbCompactAgentPattern = regexp.MustCompile(`(?:^|[;,\s(])([A-Za-z0-9_-]+)\s*:\s*([A-Za-z0-9_-]+)(?:\([^)]*\))?`)
var ccbTOMLAgentPattern = regexp.MustCompile(`^\s*\[agents\.([A-Za-z0-9_-]+)\]\s*$`)
var ccbTOMLProviderPattern = regexp.MustCompile(`^\s*provider\s*=\s*["']?([A-Za-z0-9_-]+)["']?\s*$`)
var ansiControlPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\a]*(?:\a|\x1b\\)`)
var bearerTokenPattern = regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9._~+/=-]+`)
var sensitiveValuePatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b((?:api[_-]?key|token|secret|password|session|authorization)\s*[:=]\s*)["']?[^"'\s]+`),
	regexp.MustCompile(`(?i)\b((?:OPENAI_API_KEY|ANTHROPIC_API_KEY|CLAUDE_API_KEY|GEMINI_API_KEY|AWS_SECRET_ACCESS_KEY|AWS_SESSION_TOKEN)=)["']?[^"'\s]+`),
}

func parseCCBJobID(output string) string {
	if match := ccbJobIDPattern.FindString(output); match != "" {
		return match
	}
	return ""
}

func parseCCBSocketPath(output string) string {
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(key), "socket_path") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (m *OrchestrationManager) resolveCCBSocketPath(payload protocol.OrchestrationStartPayload, explicit string) string {
	if path := strings.TrimSpace(explicit); path != "" {
		return expandHome(path)
	}
	cwd := m.cwd(payload)
	if cwd == "" {
		cwd = "."
	}
	root := expandHome(cwd)
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	candidates := []string{
		filepath.Join(root, ".ccb", "ccbd", "ccbd.sock"),
	}
	if relocated := ccbRelocatedRuntimeRoot(root); relocated != "" {
		candidates = append([]string{filepath.Join(relocated, "ccbd", "ccbd.sock")}, candidates...)
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			return candidate
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	return ""
}

func ccbRelocatedRuntimeRoot(projectRoot string) string {
	refPath := filepath.Join(projectRoot, ".ccb", "runtime-root-ref.json")
	raw, err := os.ReadFile(refPath)
	if err != nil {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	if ccbString(payload["record_type"]) != "ccb_runtime_root_ref" {
		return ""
	}
	return ccbString(payload["runtime_state_root"])
}

func ccbStructuredWatchStreamEvent(record map[string]any, state *ccbWatchStreamState) (ccbWatchStreamEvent, bool) {
	eventType := ccbString(record["type"])
	if eventType == "" {
		return ccbWatchStreamEvent{}, false
	}
	data := map[string]any{
		"source":    "ccb",
		"eventId":   ccbString(record["event_id"]),
		"jobId":     ccbString(record["job_id"]),
		"agent":     ccbString(record["target_name"]),
		"target":    ccbString(record["target_name"]),
		"eventType": eventType,
		"timestamp": ccbString(record["timestamp"]),
	}
	if data["agent"] == "" {
		data["agent"] = ccbString(record["agent_name"])
	}
	if data["target"] == "" {
		data["target"] = data["agent"]
	}
	payload, _ := record["payload"].(map[string]any)
	if payload != nil {
		data["payload"] = payload
	}
	if content := ccbCompletionItemContent(data, state); content != "" {
		data["contentKind"] = "agent_text"
		data["rawContent"] = content
		return ccbWatchStreamEvent{Content: content, Data: data}, true
	}
	if content := ccbStructuredEventContent(eventType, payload, data); content != "" {
		return ccbWatchStreamEvent{Content: content, Data: data}, true
	}
	return ccbWatchStreamEvent{}, false
}

func ccbStructuredEventContent(eventType string, payload, data map[string]any) string {
	switch eventType {
	case "job_started":
		if agent := ccbString(data["agent"]); agent != "" {
			return "CCB " + agent + ": started"
		}
	case "job_accepted", "job_queued":
		if status := firstNonEmptyCCBString(payload, "status"); status != "" {
			if agent := ccbString(data["agent"]); agent != "" {
				return "CCB " + agent + ": " + status
			}
			return "CCB status: " + status
		}
	case "job_delegated_callback":
		child := firstNonEmptyCCBString(payload, "callback_child_job_id")
		if child != "" {
			return "CCB callback waiting for " + child
		}
		return "CCB callback waiting for delegated task"
	case "callback_edge_created":
		child := firstNonEmptyCCBString(payload, "child_job_id")
		if child == "" {
			child = ccbString(data["jobId"])
		}
		parent := firstNonEmptyCCBString(payload, "parent_job_id")
		if child != "" && parent != "" {
			return fmt.Sprintf("CCB callback linked: %s -> %s", parent, child)
		}
	case "callback_continuation_submitted":
		continuation := firstNonEmptyCCBString(payload, "continuation_job_id")
		if continuation != "" {
			return "CCB callback continuation submitted: " + continuation
		}
	case "completion_terminal", "job_completed", "job_failed", "job_incomplete", "job_cancelled":
		if status := firstNonEmptyCCBString(payload, "status"); status != "" {
			if agent := ccbString(data["agent"]); agent != "" {
				return "CCB " + agent + ": " + status
			}
			return "CCB status: " + status
		}
	}
	return ""
}

func ccbRelatedJobIDs(record map[string]any) []string {
	seen := map[string]bool{}
	var ids []string
	add := func(value any) {
		text := ccbString(value)
		if !strings.HasPrefix(text, "job_") || seen[text] {
			return
		}
		seen[text] = true
		ids = append(ids, text)
	}
	add(record["job_id"])
	if payload, _ := record["payload"].(map[string]any); payload != nil {
		for _, key := range []string{"callback_child_job_id", "child_job_id", "parent_job_id", "continuation_job_id", "reply_delivery_job_id", "job_id"} {
			add(payload[key])
		}
	}
	return ids
}

func parseCCBWatchResult(output string) ccbJobResult {
	var result ccbJobResult
	lines := strings.Split(output, "\n")
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "status:") {
			result.Status = strings.TrimSpace(strings.TrimPrefix(line, "status:"))
			continue
		}
		if strings.HasPrefix(line, "reply:") {
			parts := []string{strings.TrimSpace(strings.TrimPrefix(line, "reply:"))}
			for _, extra := range lines[i+1:] {
				extra = strings.TrimSpace(extra)
				if extra == "" {
					if len(parts) > 0 && parts[len(parts)-1] != "" {
						parts = append(parts, "")
					}
					continue
				}
				if isCCBWatchMetadataLine(extra) {
					break
				}
				parts = append(parts, extra)
			}
			result.Reply = strings.TrimSpace(strings.Join(parts, "\n"))
		}
	}
	if result.Reply == "" {
		for i := len(lines) - 1; i >= 0; i-- {
			line := strings.TrimSpace(lines[i])
			if line != "" && !strings.Contains(line, ":") && !strings.HasPrefix(line, "[") {
				result.Reply = line
				break
			}
		}
	}
	return result
}

func ccbWatchStreamLineEvent(line string, state *ccbWatchStreamState) (ccbWatchStreamEvent, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return ccbWatchStreamEvent{}, false
	}
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return ccbWatchStreamEvent{
			Line:    line,
			Content: line,
			Data: map[string]any{
				"source": "ccb",
				"line":   line,
			},
		}, true
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	data := map[string]any{
		"source": "ccb",
		"line":   line,
		"key":    key,
		"value":  value,
	}
	switch key {
	case "event":
		eventData := parseCCBEventLine(value)
		if content := ccbCompletionItemContent(eventData, state); content != "" {
			for k, v := range eventData {
				data[k] = v
			}
			data["contentKind"] = "agent_text"
			data["rawContent"] = content
			return ccbWatchStreamEvent{
				Line:    line,
				Content: content,
				Data:    data,
			}, true
		}
		for k, v := range eventData {
			data[k] = v
		}
		return ccbWatchStreamEvent{
			Line:    line,
			Content: ccbEventLineContent(eventData, line),
			Data:    data,
		}, true
	case "watch_status":
		return ccbWatchStreamEvent{Line: line, Content: "CCB watch: " + value, Data: data}, true
	case "status":
		return ccbWatchStreamEvent{Line: line, Content: "CCB status: " + value, Data: data}, true
	case "reply":
		return ccbWatchStreamEvent{Line: line, Content: strings.TrimSpace(value), Data: data}, value != ""
	case "observer_notice":
		return ccbWatchStreamEvent{Line: line, Content: value, Data: data}, value != ""
	default:
		if strings.HasPrefix(key, "observer_") || isCCBWatchMetadataLine(line) {
			return ccbWatchStreamEvent{}, false
		}
		return ccbWatchStreamEvent{Line: line, Content: line, Data: data}, true
	}
}

func ccbWatchOutputWasStreamed(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		if _, ok := ccbWatchStreamLineEvent(line, nil); ok {
			return true
		}
	}
	return false
}

func parseCCBEventLine(value string) map[string]any {
	fields := strings.Fields(value)
	out := map[string]any{}
	if len(fields) > 0 {
		out["eventId"] = fields[0]
	}
	if len(fields) > 1 {
		out["jobId"] = fields[1]
	}
	if len(fields) > 2 {
		out["target"] = fields[2]
	}
	if len(fields) > 3 {
		out["eventType"] = fields[3]
	}
	if len(fields) > 4 {
		rest := strings.Join(fields[4:], " ")
		if strings.HasPrefix(rest, "{") {
			var payload map[string]any
			if err := json.Unmarshal([]byte(rest), &payload); err == nil {
				out["payload"] = payload
				return out
			}
		}
		out["timestamp"] = fields[4]
		if len(fields) > 5 {
			rawPayload := strings.Join(fields[5:], " ")
			if strings.HasPrefix(rawPayload, "{") {
				var payload map[string]any
				if err := json.Unmarshal([]byte(rawPayload), &payload); err == nil {
					out["payload"] = payload
				}
			}
		}
	}
	return out
}

func ccbCompletionItemContent(data map[string]any, state *ccbWatchStreamState) string {
	if data["eventType"] != "completion_item" {
		return ""
	}
	payload, _ := data["payload"].(map[string]any)
	if payload == nil {
		return ""
	}
	agent := ccbString(data["target"])
	if agent == "" {
		agent = ccbString(payload["agent_name"])
	}
	if agent != "" {
		data["agent"] = agent
	}
	itemPayload, _ := payload["payload"].(map[string]any)
	if itemPayload == nil {
		return ""
	}
	kind := ccbString(payload["kind"])
	data["completionKind"] = kind
	text := ccbCompletionItemText(kind, itemPayload)
	if text == "" {
		return ""
	}
	if state == nil {
		return text
	}
	if state.agentContent == nil {
		state.agentContent = make(map[string]string)
	}
	key := agent
	if key == "" {
		key = ccbString(data["jobId"])
	}
	switch kind {
	case "assistant_chunk", "assistant_final", "result", "turn_boundary", "turn_aborted",
		"cancel_info", "error", "pane_dead", "session_snapshot", "session_mutation":
		current := state.agentContent[key]
		delta := appendAgentMessageContentString(&current, text)
		state.agentContent[key] = current
		return delta
	default:
		return text
	}
}

func ccbCompletionItemText(kind string, payload map[string]any) string {
	switch kind {
	case "assistant_chunk":
		return firstNonEmptyCCBString(payload, "delta", "text", "content", "reply", "fallback_text", "merged_text")
	case "assistant_final", "result":
		return firstNonEmptyCCBString(payload, "last_agent_message", "final_answer", "result_text", "reply", "text", "merged_text", "fallback_text")
	case "turn_boundary", "turn_aborted", "cancel_info", "error", "pane_dead":
		return firstNonEmptyCCBString(payload, "last_agent_message", "final_answer", "result_text", "reply", "text", "error_message", "merged_text", "fallback_text")
	case "session_snapshot", "session_mutation":
		return firstNonEmptyCCBString(payload, "reply", "content", "text", "merged_text", "fallback_text")
	default:
		return firstNonEmptyCCBString(payload, "last_agent_message", "final_answer", "result_text", "reply", "text", "content", "merged_text", "fallback_text")
	}
}

func firstNonEmptyCCBString(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, key := range keys {
		if value := ccbString(m[key]); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func ccbString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case json.Number:
		return v.String()
	default:
		return ""
	}
}

func ccbBool(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	default:
		return false
	}
}

func ccbInt(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		i, _ := v.Int64()
		return int(i)
	case string:
		var i int
		_, _ = fmt.Sscanf(strings.TrimSpace(v), "%d", &i)
		return i
	default:
		return 0
	}
}

func ccbMapSlice(value any) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			out = append(out, m)
		}
	}
	return out
}

func ccbAgentCLI(agent string) string {
	switch strings.ToLower(strings.TrimSpace(agent)) {
	case "codex":
		return "codex"
	case "claude":
		return "claude"
	default:
		return "ccb"
	}
}

func ccbTraceReplyEvents(output string) []ccbTraceReplyEvent {
	var events []ccbTraceReplyEvent
	for _, rawLine := range strings.Split(output, "\n") {
		line := strings.TrimSpace(rawLine)
		if !strings.HasPrefix(line, "reply: ") {
			continue
		}
		fields := parseCCBTraceFields(strings.TrimSpace(strings.TrimPrefix(line, "reply: ")))
		agent := fields["agent"]
		content := fields["reply"]
		if content == "" {
			content = fields["preview"]
		}
		content = strings.TrimSpace(content)
		if agent == "" || content == "" {
			continue
		}
		events = append(events, ccbTraceReplyEvent{
			Role:    agent,
			CLI:     ccbAgentCLI(agent),
			Content: content,
			Data: map[string]any{
				"source":         "ccb",
				"contentKind":    "agent_reply",
				"agent":          agent,
				"replyId":        fields["id"],
				"messageId":      fields["message"],
				"attemptId":      fields["attempt"],
				"terminalStatus": fields["terminal"],
				"reason":         fields["reason"],
				"finishedAt":     fields["finished"],
			},
		})
	}
	return events
}

func ccbTraceReplyEventsFromPayload(payload map[string]any, state *ccbWatchStreamState) []ccbTraceReplyEvent {
	replies := ccbMapSlice(payload["replies"])
	events := make([]ccbTraceReplyEvent, 0, len(replies))
	for _, reply := range replies {
		agent := ccbString(reply["agent_name"])
		content := ccbString(reply["reply"])
		if content == "" {
			content = ccbString(reply["reply_preview"])
		}
		content = strings.TrimSpace(content)
		if agent == "" || content == "" {
			continue
		}
		data := map[string]any{
			"source":         "ccb",
			"contentKind":    "agent_reply",
			"agent":          agent,
			"replyId":        ccbString(reply["reply_id"]),
			"messageId":      ccbString(reply["message_id"]),
			"attemptId":      ccbString(reply["attempt_id"]),
			"terminalStatus": ccbString(reply["terminal_status"]),
			"reason":         ccbString(reply["reason"]),
			"finishedAt":     ccbString(reply["finished_at"]),
		}
		if delta := ccbTraceReplyDelta(agent, content, state); delta != "" {
			events = append(events, ccbTraceReplyEvent{
				Role:    agent,
				CLI:     ccbAgentCLI(agent),
				Content: delta,
				Data:    data,
			})
		}
	}
	return events
}

func ccbTraceReplyDelta(agent, content string, state *ccbWatchStreamState) string {
	if state == nil {
		return content
	}
	if state.agentContent == nil {
		state.agentContent = make(map[string]string)
	}
	current := state.agentContent[agent]
	delta := appendAgentMessageContentString(&current, content)
	state.agentContent[agent] = current
	return delta
}

func ccbTraceFinalReply(payload map[string]any) string {
	if value := ccbString(payload["reply"]); strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	replies := ccbMapSlice(payload["replies"])
	for i := len(replies) - 1; i >= 0; i-- {
		if value := ccbString(replies[i]["reply"]); strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func parseCCBTraceFields(value string) map[string]string {
	out := make(map[string]string)
	for _, key := range []string{"id", "message", "attempt", "agent", "terminal", "size", "notice", "kind", "reason", "finished"} {
		if val, ok := ccbTraceField(value, key); ok {
			out[key] = val
		}
	}
	if val, ok := ccbTraceField(value, "preview"); ok {
		out["preview"] = val
	}
	return out
}

func ccbTraceField(value, key string) (string, bool) {
	prefix := key + "="
	start := strings.Index(value, prefix)
	if start < 0 {
		return "", false
	}
	start += len(prefix)
	end := len(value)
	for _, nextKey := range []string{" id=", " message=", " attempt=", " agent=", " terminal=", " size=", " notice=", " kind=", " reason=", " finished=", " preview="} {
		if strings.TrimSpace(nextKey) == prefix {
			continue
		}
		if idx := strings.Index(value[start:], nextKey); idx >= 0 && start+idx < end {
			end = start + idx
		}
	}
	return strings.TrimSpace(value[start:end]), true
}

func ccbEventLineContent(data map[string]any, fallback string) string {
	target, _ := data["target"].(string)
	eventType, _ := data["eventType"].(string)
	if eventType == "" {
		return fallback
	}
	label := strings.ReplaceAll(eventType, "_", " ")
	if target != "" {
		return fmt.Sprintf("CCB %s: %s", target, label)
	}
	return "CCB: " + label
}

func isCCBWatchMetadataLine(line string) bool {
	key, _, ok := strings.Cut(strings.TrimSpace(line), ":")
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "watch_status", "observer_view", "observer_authority", "observer_terminal", "observer_notice",
		"job_id", "agent_name", "target_name", "project_id", "mode", "resolved_kind",
		"expected_count", "received_count", "terminal_count", "notice_count", "waited_s",
		"reply_id", "message_id", "attempt_id", "provider", "provider_instance", "event":
		return true
	default:
		return false
	}
}

func (m *OrchestrationManager) runCLI(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, cli, prompt string) (string, []RunnerToolEvent, error) {
	switch cli {
	case "claude":
		return m.runClaude(ctx, payload, turnID, role, prompt)
	default:
		return m.runCodex(ctx, payload, turnID, role, prompt)
	}
}

func (m *OrchestrationManager) runCodex(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt string) (string, []RunnerToolEvent, error) {
	if m.shouldRunCodexAppServer() {
		return m.runCodexAppServer(ctx, payload, turnID, role, prompt)
	}
	args := []string{"exec", "--json", "--color", "never", "--skip-git-repo-check"}
	if m.cfg.Bridge.Model != "" {
		args = append(args, "--model", m.cfg.Bridge.Model)
	}
	if strings.EqualFold(m.cfg.Bridge.ApprovalPolicy, "never") && strings.EqualFold(m.cfg.Bridge.Sandbox, "danger-full-access") {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	} else if m.cfg.Bridge.Sandbox != "" {
		args = append(args, "--sandbox", m.cfg.Bridge.Sandbox)
	}
	if m.cfg.Bridge.ApprovalPolicy != "" && !(strings.EqualFold(m.cfg.Bridge.ApprovalPolicy, "never") && strings.EqualFold(m.cfg.Bridge.Sandbox, "danger-full-access")) {
		args = append(args, "-c", "approval_policy="+quoteTomlString(m.cfg.Bridge.ApprovalPolicy))
	}
	cwd := m.cwd(payload)
	if cwd != "" {
		args = append(args, "--cd", cwd)
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, m.codexPath(), args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", nil, err
	}
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	_, _ = io.WriteString(stdin, prompt)
	_ = stdin.Close()

	content, tools, scanErr := m.scanCodexJSONL(stdout, payload.RunID, turnID, role)
	waitErr := cmd.Wait()
	if scanErr != nil {
		return content, tools, scanErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return content, tools, errors.New(msg)
	}
	if content == "" {
		content = strings.TrimSpace(stderr.String())
	}
	return content, tools, nil
}

func (m *OrchestrationManager) runCodexAppServer(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt string) (string, []RunnerToolEvent, error) {
	runner := NewCodexAppServerRunner(m.cfg)
	defer runner.Close()
	var tools []RunnerToolEvent
	toolStarts := make(map[string]time.Time)
	result, err := runner.Prompt(ctx, RunnerRequest{
		Content:  prompt,
		RunID:    payload.RunID,
		PromptID: turnID,
		CWD:      m.cwd(payload),
		Approvals: orchestrationApprovalRequester{
			manager: m,
			runID:   payload.RunID,
			turnID:  turnID,
			role:    role,
			cli:     "codex",
			cwd:     m.cwd(payload),
		},
	}, func(update RunnerUpdate) {
		if update.Delta != "" {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: update.Delta})
		}
		if update.Content != "" {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: update.Content})
		}
		if update.Tool != nil {
			stampToolTiming(update.Tool, toolStarts)
			tools = append(tools, *update.Tool)
			m.emitTool(payload.RunID, turnID, role, "codex", update.Tool)
		}
	})
	return strings.TrimSpace(result.Content), tools, err
}

func (m *OrchestrationManager) runClaude(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt string) (string, []RunnerToolEvent, error) {
	approvalServer, cleanup, err := m.prepareClaudeApprovalServer(ctx, payload, turnID, role)
	if err != nil {
		return "", nil, err
	}
	defer cleanup()
	args := m.claudeArgs(payload, prompt)
	if approvalServer != nil {
		args = m.withClaudeApprovalArgs(args, approvalServer.configPath)
	}
	cmd := exec.CommandContext(ctx, m.claudePath(), args...)
	if cwd := m.cwd(payload); cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	content, tools, scanErr := m.scanClaudeJSONL(stdout, payload.RunID, turnID, role)
	waitErr := cmd.Wait()
	if scanErr != nil {
		return content, tools, scanErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return content, tools, errors.New(msg)
	}
	return content, tools, nil
}

type claudeApprovalServer struct {
	configPath string
}

func (m *OrchestrationManager) prepareClaudeApprovalServer(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role string) (*claudeApprovalServer, func(), error) {
	if !m.shouldBridgeClaudeApproval() {
		return nil, func() {}, nil
	}
	tmpDir, err := os.MkdirTemp("", "codex-bridge-claude-approval-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create claude approval temp dir: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}
	socketPath := filepath.Join(tmpDir, "approval.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		cleanup()
		return nil, nil, fmt.Errorf("listen claude approval socket: %w", err)
	}
	serverCtx, cancel := context.WithCancel(ctx)
	requester := orchestrationApprovalRequester{
		manager: m,
		runID:   payload.RunID,
		turnID:  turnID,
		role:    role,
		cli:     "claude",
		cwd:     m.cwd(payload),
	}
	go serveClaudeApprovalSocket(serverCtx, listener, requester)

	exe, err := os.Executable()
	if err != nil {
		listener.Close()
		cancel()
		cleanup()
		return nil, nil, fmt.Errorf("locate codex-bridge executable for claude approval mcp: %w", err)
	}
	configPath := filepath.Join(tmpDir, "mcp.json")
	config := map[string]any{
		"mcpServers": map[string]any{
			"codex_bridge": map[string]any{
				"type":    "stdio",
				"command": exe,
				"args":    []string{"claude-approval-mcp", "--socket", socketPath},
			},
		},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		listener.Close()
		cancel()
		cleanup()
		return nil, nil, fmt.Errorf("marshal claude approval mcp config: %w", err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		listener.Close()
		cancel()
		cleanup()
		return nil, nil, fmt.Errorf("write claude approval mcp config: %w", err)
	}
	return &claudeApprovalServer{configPath: configPath}, func() {
		cancel()
		listener.Close()
		cleanup()
	}, nil
}

func (m *OrchestrationManager) claudeArgs(payload protocol.OrchestrationStartPayload, prompt string) []string {
	args := []string{"--print", "--output-format=stream-json"}
	if cwd := m.cwd(payload); cwd != "" {
		args = append(args, "--add-dir", cwd)
	}
	args = append(args, "--verbose")
	if m.bypassApprovalsAndSandbox() {
		if runningAsRoot() {
			args = append(args, "--permission-mode", "acceptEdits")
		} else {
			args = append(args, "--permission-mode", "bypassPermissions")
		}
	}
	if m.cfg.Bridge.ClaudeModel != "" {
		args = append(args, "--model", m.cfg.Bridge.ClaudeModel)
	} else if m.cfg.Bridge.Model != "" {
		args = append(args, "--model", m.cfg.Bridge.Model)
	}
	if m.cfg.Bridge.ClaudeEffort != "" {
		args = append(args, "--effort", m.cfg.Bridge.ClaudeEffort)
	}
	args = append(args, prompt)
	return args
}

func (m *OrchestrationManager) withClaudeApprovalArgs(args []string, configPath string) []string {
	if configPath == "" {
		return args
	}
	insertAt := len(args)
	if insertAt > 0 {
		insertAt--
	}
	extra := []string{
		"--permission-mode", "default",
		"--mcp-config", configPath,
		"--permission-prompt-tool", "mcp__codex_bridge__browser_approval",
	}
	next := make([]string, 0, len(args)+len(extra))
	next = append(next, args[:insertAt]...)
	next = append(next, extra...)
	next = append(next, args[insertAt:]...)
	return next
}

func (m *OrchestrationManager) bypassApprovalsAndSandbox() bool {
	return strings.EqualFold(m.cfg.Bridge.ApprovalPolicy, "never") &&
		strings.EqualFold(m.cfg.Bridge.Sandbox, "danger-full-access")
}

func (m *OrchestrationManager) shouldBridgeClaudeApproval() bool {
	return !m.bypassApprovalsAndSandbox()
}

func (m *OrchestrationManager) shouldRunCodexAppServer() bool {
	return !m.bypassApprovalsAndSandbox()
}

func (m *OrchestrationManager) shouldRunFinalVerifier(history []orchestrationTurn) bool {
	if len(history) == 0 {
		return false
	}
	last := history[len(history)-1]
	if strings.EqualFold(last.HandoffFields.Status, "resolved") && meaningfulHandoffValue(last.HandoffFields.Verified) && !meaningfulHandoffValue(last.HandoffFields.Changed) && !hasRiskyHandoff(last) && failedCommandCount(history) == 0 {
		return false
	}
	for _, item := range history {
		if item.Err != "" || failedCommandCount([]orchestrationTurn{item}) > 0 || hasRiskyHandoff(item) || meaningfulHandoffValue(item.HandoffFields.Changed) {
			return true
		}
	}
	return false
}

func hasRiskyHandoff(item orchestrationTurn) bool {
	if meaningfulHandoffValue(item.HandoffFields.Risks) {
		return true
	}
	status := strings.ToLower(strings.TrimSpace(item.HandoffFields.Status))
	return status == "blocked"
}

func verifierRoleCLI(mode string, history []orchestrationTurn) (string, string) {
	lastCLI := ""
	if len(history) > 0 {
		lastCLI = history[len(history)-1].CLI
	}
	if strings.EqualFold(lastCLI, "codex") {
		if mode == "debate" {
			return "proposer", "claude"
		}
		return "implementer", "claude"
	}
	return "verifier", "codex"
}

func remediationRoleCLI(mode string, history []orchestrationTurn) (string, string) {
	lastCLI := ""
	if len(history) > 0 {
		lastCLI = history[len(history)-1].CLI
	}
	if strings.EqualFold(lastCLI, "codex") {
		if mode == "debate" {
			return "proposer", "claude"
		}
		return "implementer", "claude"
	}
	if mode == "debate" {
		return "proposer", "codex"
	}
	return "implementer", "codex"
}

type claudeApprovalSocketRequest struct {
	RequestID string          `json:"requestId,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Command   string          `json:"command,omitempty"`
	CWD       string          `json:"cwd,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	ToolName  string          `json:"toolName,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

type claudeApprovalSocketResponse struct {
	RequestID string `json:"requestId,omitempty"`
	Decision  string `json:"decision,omitempty"`
	Error     string `json:"error,omitempty"`
}

func serveClaudeApprovalSocket(ctx context.Context, listener net.Listener, requester orchestrationApprovalRequester) {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("[bridge] claude approval socket accept failed", "error", err)
			return
		}
		go handleClaudeApprovalSocketConn(ctx, conn, requester)
	}
}

func handleClaudeApprovalSocketConn(ctx context.Context, conn net.Conn, requester orchestrationApprovalRequester) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
	var req claudeApprovalSocketRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(claudeApprovalSocketResponse{Error: err.Error()})
		return
	}
	raw := req.Input
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	payload := protocol.ApprovalRequestPayload{
		RequestID: req.RequestID,
		Kind:      req.Kind,
		Command:   req.Command,
		CWD:       req.CWD,
		Reason:    req.Reason,
		Params:    raw,
	}
	if payload.Kind == "" {
		payload.Kind = "claude.permission_prompt"
	}
	if payload.Command == "" {
		payload.Command = claudeApprovalCommand(req.ToolName, raw)
	}
	if payload.Reason == "" {
		payload.Reason = claudeApprovalReason(raw)
	}
	res, err := requester.RequestApproval(ctx, payload)
	if err != nil {
		_ = json.NewEncoder(conn).Encode(claudeApprovalSocketResponse{RequestID: payload.RequestID, Decision: "cancel", Error: err.Error()})
		return
	}
	_ = json.NewEncoder(conn).Encode(claudeApprovalSocketResponse{RequestID: res.RequestID, Decision: res.Decision})
}

func claudeApprovalCommand(toolName string, raw json.RawMessage) string {
	input := map[string]any{}
	_ = json.Unmarshal(raw, &input)
	for _, key := range []string{"command", "cmd", "shellCommand", "tool_input", "input"} {
		if value := stringifyApprovalValue(input[key]); value != "" {
			return value
		}
	}
	name := firstString(input, "tool_name", "toolName", "name")
	switch name {
	case "Bash":
		if value := stringifyApprovalValue(input["command"]); value != "" {
			return value
		}
	case "Read", "Write", "Edit", "MultiEdit":
		if value := stringifyApprovalValue(input["file_path"]); value != "" {
			return name + " " + value
		}
		if value := stringifyApprovalValue(input["path"]); value != "" {
			return name + " " + value
		}
	}
	if name == "" {
		name = toolName
	}
	if name != "" {
		return name
	}
	return trimForPrompt(oneLine(string(raw)), 240)
}

func claudeApprovalReason(raw json.RawMessage) string {
	input := map[string]any{}
	_ = json.Unmarshal(raw, &input)
	for _, key := range []string{"reason", "description", "permission", "prompt", "message"} {
		if value := stringifyApprovalValue(input[key]); value != "" {
			return value
		}
	}
	return ""
}

func stringifyApprovalValue(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if part := stringifyApprovalValue(item); part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		for _, key := range []string{"command", "file_path", "path", "description", "reason", "name"} {
			if part := stringifyApprovalValue(v[key]); part != "" {
				return part
			}
		}
	}
	return ""
}

func runningAsRoot() bool {
	if os.Geteuid() == 0 {
		return true
	}
	current, err := user.Current()
	return err == nil && current.Uid == "0"
}

func (m *OrchestrationManager) scanCodexJSONL(stdout io.Reader, runID, turnID, role string) (string, []RunnerToolEvent, error) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	var content strings.Builder
	var eventErr string
	var tools []RunnerToolEvent
	toolStarts := make(map[string]time.Time)
	emitCodexTool := func(tool *RunnerToolEvent) {
		if tool == nil {
			return
		}
		stampToolTiming(tool, toolStarts)
		tools = append(tools, *tool)
		m.emitTool(runID, turnID, role, "codex", tool)
	}
	for {
		line, err := readJSONLLine(reader, 32*1024*1024)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return content.String(), tools, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		typ, _ := msg["type"].(string)
		if isErrorEvent(typ) {
			if message := eventErrorMessage(msg); message != "" {
				eventErr = message
			}
		}
		switch typ {
		case "item.agent_message.delta", "item.agentMessage.delta", "agent_message.delta", "agentMessage.delta", "response.output_text.delta":
			if delta := extractDelta(msg); delta != "" {
				content.WriteString(delta)
				m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: delta})
			}
		case "item.completed":
			item, _ := msg["item"].(map[string]any)
			itemType, _ := item["type"].(string)
			if itemType == "agent_message" || itemType == "agentMessage" {
				if text := agentMessageText(item); text != "" {
					if delta := appendAgentMessageContent(&content, text); delta != "" {
						m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: delta})
					}
				}
			}
			if itemType == "command_execution" || itemType == "commandExecution" {
				emitCodexTool(commandExecutionEvent(item))
			}
		case "item.started", "item.updated":
			item, _ := msg["item"].(map[string]any)
			itemType, _ := item["type"].(string)
			if itemType == "command_execution" || itemType == "commandExecution" {
				emitCodexTool(commandExecutionEvent(item))
			}
		}
	}
	if eventErr != "" {
		return content.String(), tools, errors.New(eventErr)
	}
	return strings.TrimSpace(content.String()), tools, nil
}

func (m *OrchestrationManager) scanClaudeJSONL(stdout io.Reader, runID, turnID, role string) (string, []RunnerToolEvent, error) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	var content strings.Builder
	var tools []RunnerToolEvent
	toolCommands := make(map[string]string)
	toolStarts := make(map[string]time.Time)
	deferredReadStarts := make(map[string]*RunnerToolEvent)
	emitClaudeTool := func(tool *RunnerToolEvent) {
		if tool == nil {
			return
		}
		if tool.ID != "" && tool.Command != "" {
			toolCommands[tool.ID] = tool.Command
		}
		if tool.ID != "" && tool.Command == "" {
			tool.Command = toolCommands[tool.ID]
		}
		if tool.ID != "" && strings.EqualFold(tool.Status, "in_progress") && isClaudeReadCommand(tool.Command) {
			copy := *tool
			deferredReadStarts[tool.ID] = &copy
			return
		}
		if isClaudeEmptyPagesReadFailure(tool) {
			delete(deferredReadStarts, tool.ID)
			return
		}
		stampToolTiming(tool, toolStarts)
		if tool.ID != "" {
			if start := deferredReadStarts[tool.ID]; start != nil {
				tools = append(tools, *start)
				m.emitTool(runID, turnID, role, "claude", start)
				delete(deferredReadStarts, tool.ID)
			}
		}
		tools = append(tools, *tool)
		m.emitTool(runID, turnID, role, "claude", tool)
	}
	for {
		line, err := readJSONLLine(reader, 32*1024*1024)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return content.String(), tools, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		typ, _ := msg["type"].(string)
		switch typ {
		case "assistant":
			if message := firstString(msg, "error"); message != "" {
				return content.String(), tools, errors.New(message)
			}
			for _, tool := range claudeToolEvents(msg) {
				emitClaudeTool(tool)
			}
			if delta := claudeAssistantText(msg); delta != "" {
				content.WriteString(delta)
				m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "claude", Content: delta})
			}
		case "user":
			for _, tool := range claudeToolEvents(msg) {
				emitClaudeTool(tool)
			}
		case "result":
			if isErr, _ := msg["is_error"].(bool); isErr {
				if text := firstString(msg, "result", "error"); text != "" {
					return content.String(), tools, errors.New(text)
				}
				return content.String(), tools, errors.New("claude returned an error")
			}
			if text := firstString(msg, "result"); text != "" && content.Len() == 0 {
				content.WriteString(text)
				m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "claude", Content: text})
			}
		case "error":
			if message := eventErrorMessage(msg); message != "" {
				return content.String(), tools, errors.New(message)
			}
		}
	}
	return strings.TrimSpace(content.String()), tools, nil
}

func isClaudeReadCommand(command string) bool {
	return strings.HasPrefix(strings.TrimSpace(command), "Read ")
}

func isClaudeEmptyPagesReadFailure(tool *RunnerToolEvent) bool {
	if tool == nil || !strings.EqualFold(tool.Status, "failed") || !isClaudeReadCommand(tool.Command) {
		return false
	}
	output := tool.Output
	return strings.Contains(output, `Invalid pages parameter: ""`) && strings.Contains(output, "Pages are 1-indexed")
}

func stampToolTiming(tool *RunnerToolEvent, starts map[string]time.Time) {
	if tool == nil {
		return
	}
	now := time.Now()
	if isRunningToolStatus(tool.Status) {
		if tool.StartedAt.IsZero() {
			tool.StartedAt = now
		}
		if tool.ID != "" && starts[tool.ID].IsZero() {
			starts[tool.ID] = tool.StartedAt
		}
		return
	}
	if tool.ID != "" {
		if start := starts[tool.ID]; !start.IsZero() {
			tool.StartedAt = start
			delete(starts, tool.ID)
		}
	}
	if tool.StartedAt.IsZero() {
		tool.StartedAt = now
	}
	if tool.CompletedAt.IsZero() {
		tool.CompletedAt = now
	}
}

func isRunningToolStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "in_progress", "running", "started":
		return true
	default:
		return false
	}
}

func (m *OrchestrationManager) emitTool(runID, turnID, role, cli string, tool *RunnerToolEvent) {
	kind := "command.end"
	if isRunningToolStatus(tool.Status) {
		kind = "command.start"
	}
	data := map[string]any{
		"id":      tool.ID,
		"status":  tool.Status,
		"command": tool.Command,
		"output":  tool.Output,
	}
	if tool.ExitCode != nil {
		data["exitCode"] = *tool.ExitCode
	}
	if !tool.StartedAt.IsZero() {
		data["startedAt"] = tool.StartedAt.Unix()
	}
	if !tool.CompletedAt.IsZero() {
		data["completedAt"] = tool.CompletedAt.Unix()
		if !tool.StartedAt.IsZero() {
			data["durationMs"] = tool.CompletedAt.Sub(tool.StartedAt).Milliseconds()
		}
	}
	m.emit(runID, protocol.OrchestrationEventPayload{
		Kind:   kind,
		TurnID: turnID,
		Role:   role,
		CLI:    cli,
		Status: tool.Status,
		Data:   data,
	})
}

func (m *OrchestrationManager) emit(runID string, event protocol.OrchestrationEventPayload) {
	event.RunID = runID
	m.send(protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", event))
}

func (m *OrchestrationManager) send(env protocol.Envelope) {
	m.mu.Lock()
	out := m.output
	buffered := false
	if out == nil && env.Type == protocol.TypeOrchestrationEvent {
		m.pending = append(m.pending, env)
		if len(m.pending) > 1000 {
			m.pending = m.pending[len(m.pending)-1000:]
		}
		buffered = true
	}
	m.mu.Unlock()
	if out == nil {
		if buffered {
			slog.Warn("[bridge] orchestration event buffered: bridge disconnected", "type", env.Type)
		} else {
			slog.Warn("[bridge] orchestration event dropped: bridge disconnected", "type", env.Type)
		}
		return
	}
	send(out, env)
}

func (m *OrchestrationManager) cwd(payload protocol.OrchestrationStartPayload) string {
	if payload.CWD != "" {
		return expandHome(payload.CWD)
	}
	if m.cfg.Bridge.CWD != "" {
		return expandHome(m.cfg.Bridge.CWD)
	}
	return "."
}

func (m *OrchestrationManager) codexPath() string {
	if m.cfg.Bridge.CodexPath == "" {
		return "codex"
	}
	return m.cfg.Bridge.CodexPath
}

func (m *OrchestrationManager) claudePath() string {
	if m.cfg.Bridge.ClaudePath == "" {
		return "claude"
	}
	return m.cfg.Bridge.ClaudePath
}

func roleForTurn(mode string, turn int) (string, string) {
	if mode == "debate" {
		if turn%2 == 1 {
			return "proposer", "claude"
		}
		return "critic", "codex"
	}
	if turn%2 == 1 {
		return "implementer", "claude"
	}
	return "reviewer", "codex"
}

func promptHeader(role, cli string, turn int) string {
	return fmt.Sprintf("%s via %s, turn %d", role, cli, turn)
}

func nextRoleCLI(mode string, turn int) (string, string) {
	if mode == "debate" {
		if turn%2 == 1 {
			return "critic", "codex"
		}
		return "proposer", "claude"
	}
	if turn%2 == 1 {
		return "reviewer", "codex"
	}
	return "implementer", "claude"
}

func newOrchestrationTurnRecord(turnID, role, cli, content string, tools []RunnerToolEvent) orchestrationTurn {
	content = cleanOrchestrationTurnContent(content)
	handoff := extractHandoff(content)
	return orchestrationTurn{
		TurnID:        turnID,
		Role:          role,
		CLI:           cli,
		Content:       content,
		Msg:           extractMsg(content),
		Handoff:       handoff,
		HandoffFields: parseHandoffFields(handoff),
		Tools:         tools,
	}
}

func resolvedHandoffReady(content string) bool {
	handoff := strings.ToLower(extractHandoff(content))
	if !strings.Contains(handoff, "status=resolved") {
		return false
	}
	if hasUnresolvedAcceptanceSignal(content, "") {
		return false
	}
	return hasUserVisibleConclusion(content)
}

func hasUserVisibleConclusion(content string) bool {
	visible := humanVisibleContent(content)
	if visible == "" {
		return false
	}
	lower := strings.ToLower(visible)
	signals := []string{
		"final conclusion", "conclusion:", "summary:", "verified", "verification", "completed", "remaining risk",
		"最终结论", "结论：", "总结：", "验证", "通过", "完成", "剩余风险",
	}
	for _, signal := range signals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func turnConclusionFallbackSummary(userPrompt string, turn, maxTurns int, history []orchestrationTurn, current orchestrationTurn) string {
	if !turnResponseNeedsFallback(current.Content) {
		return ""
	}
	final := turn >= maxTurns || strings.EqualFold(current.HandoffFields.Status, "resolved")
	return buildTurnConclusionSummary(userPrompt, final, history, current, current.Err != "")
}

func verifierConclusionFallbackSummary(userPrompt string, history []orchestrationTurn, current orchestrationTurn) string {
	if !turnResponseNeedsFallback(current.Content) {
		return ""
	}
	return buildTurnConclusionSummary(userPrompt, true, history, current, current.Err != "")
}

func erroredTurnFallbackSummary(userPrompt string, final bool, history []orchestrationTurn, current orchestrationTurn) string {
	if !turnResponseNeedsFallback(current.Content) {
		return ""
	}
	return buildTurnConclusionSummary(userPrompt, final, history, current, true)
}

func finalTurnFallbackSummary(userPrompt string, turn, maxTurns int, history []orchestrationTurn, current orchestrationTurn) string {
	if turn != maxTurns {
		return ""
	}
	return turnConclusionFallbackSummary(userPrompt, turn, maxTurns, history, current)
}

func appendConclusionToTurnRecord(record *orchestrationTurn, summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}
	base := cleanOrchestrationTurnContent(record.Content)
	if turnResponseNeedsFallback(base) && !hasMachineContractLines(base) {
		base = ""
	}
	record.Content = summary
	if base != "" {
		record.Content = base + "\n\n" + summary
	}
	record.Msg = extractMsg(record.Content)
	record.Handoff = extractHandoff(record.Content)
	record.HandoffFields = parseHandoffFields(record.Handoff)
	if base != "" {
		return "\n\n" + summary
	}
	return summary
}

func cleanOrchestrationTurnContent(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if idx := conclusionTrimIndex(content); idx > 0 {
		return strings.TrimSpace(content[idx:])
	}
	return content
}

func conclusionTrimIndex(content string) int {
	if idx := lastMarkerIndexFold(content, []string{
		"最终结论", "最终总结", "final conclusion", "final summary",
	}); idx >= 0 && shouldTrimConclusionPrefix(content[:idx]) {
		return idx
	}
	if idx := lastMarkerIndexFold(content, []string{
		"审查结论", "本轮结论", "结论：", "结论:", "conclusion:", "summary:",
	}); idx >= 0 && shouldTrimConclusionPrefix(content[:idx]) {
		return idx
	}
	return -1
}

func lastMarkerIndexFold(content string, markers []string) int {
	lower := strings.ToLower(content)
	best := -1
	for _, marker := range markers {
		if idx := strings.LastIndex(lower, strings.ToLower(marker)); idx > best {
			best = idx
		}
	}
	return best
}

func shouldTrimConclusionPrefix(prefix string) bool {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return false
	}
	lower := strings.ToLower(prefix)
	progressSignals := []string{
		"我会", "我先", "我将", "接下来", "正在", "不展开新的",
		"i will", "i'll", "i am going to", "next i",
	}
	count := 0
	for _, signal := range progressSignals {
		count += strings.Count(lower, signal)
	}
	return count >= 2 || strings.HasPrefix(lower, "我会") || strings.HasPrefix(lower, "我先") || len([]rune(prefix)) > 240
}

func hasMachineContractLines(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "msg:") || strings.Contains(lower, "handoff:")
}

func buildTurnConclusionSummary(userPrompt string, final bool, history []orchestrationTurn, current orchestrationTurn, failed bool) string {
	zh := !explicitEnglishResponseRequested(userPrompt)
	prior := latestMeaningfulConclusion(history)
	verified := completedVerificationSummaries([]orchestrationTurn{current}, zh, 3)
	if len(verified) == 0 {
		verified = completedVerificationSummaries(history, zh, 3)
	}
	failedCommands := failedCommandCount(append(history, current))
	blocker := compactBlockerSummary(current)
	if blocker == "" {
		blocker, _ = latestBlocker(history)
	}
	if acceptanceBlocker := acceptanceBlockerSummary(userPrompt, append(history, current)); acceptanceBlocker != "" {
		blocker = acceptanceBlocker
		failed = true
	}

	var b strings.Builder
	if zh {
		if failed {
			if final {
				b.WriteString("最终结论：本次编排未完成。")
			} else {
				b.WriteString("本轮结论：本轮未完成，当前阻塞点已记录。")
			}
		} else if final {
			b.WriteString("最终结论：本次编排已完成。")
		} else {
			b.WriteString("本轮结论：本轮编排已完成，并已记录当前可确认的结果。")
		}
		if prior != "" {
			b.WriteString("\n\n结果概览：")
			b.WriteString(prior)
		}
		if current.Err != "" {
			b.WriteString("\n\n错误：")
			b.WriteString(trimForPrompt(oneLine(current.Err), 500))
		}
		if blocker != "" {
			b.WriteString("\n\n阻塞点：")
			b.WriteString(blocker)
		}
		if len(verified) > 0 {
			b.WriteString("\n\n已验证：")
			for _, item := range verified {
				b.WriteString("\n- ")
				b.WriteString(item)
			}
		} else {
			b.WriteString("\n\n已验证：没有可提炼的命令摘要；可展开命令详情审计原始事件。")
		}
		if failedCommands > 0 {
			b.WriteString("\n\n剩余风险：命令详情里仍有失败命令，需要结合具体输出判断。")
		} else if failed && blocker != "" {
			b.WriteString("\n\n剩余风险：上述阻塞点尚未解除，不能把当前状态视为已满足用户要求。")
		} else {
			b.WriteString("\n\n剩余风险：未发现新的阻塞问题；如需审计细节，可展开命令详情查看原始事件。")
		}
		return b.String()
	}

	if failed {
		if final {
			b.WriteString("Final conclusion: this orchestration did not complete.")
		} else {
			b.WriteString("Turn conclusion: this turn did not complete; the current blocker was recorded.")
		}
	} else if final {
		b.WriteString("Final conclusion: this orchestration completed.")
	} else {
		b.WriteString("Turn conclusion: this orchestration turn completed and the current confirmed state was recorded.")
	}
	if prior != "" {
		b.WriteString("\n\nResult overview: ")
		b.WriteString(prior)
	}
	if current.Err != "" {
		b.WriteString("\n\nError: ")
		b.WriteString(trimForPrompt(oneLine(current.Err), 500))
	}
	if blocker != "" {
		b.WriteString("\n\nBlocker: ")
		b.WriteString(blocker)
	}
	if len(verified) > 0 {
		b.WriteString("\n\nVerified:")
		for _, item := range verified {
			b.WriteString("\n- ")
			b.WriteString(item)
		}
	} else {
		b.WriteString("\n\nVerified: no concise command summary was available; expand command details to audit raw events.")
	}
	if failedCommands > 0 {
		b.WriteString("\n\nRemaining risk: some command events failed; check command details for raw output.")
	} else if failed && blocker != "" {
		b.WriteString("\n\nRemaining risk: the blocker above is still unresolved, so the current state must not be treated as satisfying the user request.")
	} else {
		b.WriteString("\n\nRemaining risk: no new blocking issue was reported. Expand command details to audit raw events.")
	}
	return b.String()
}

func finalRunAssessmentSummary(userPrompt string, history []orchestrationTurn, changes workspaceChangeReport, unresolvedReason string) string {
	zh := !explicitEnglishResponseRequested(userPrompt)
	dimensions := finalRunAssessmentDimensions(userPrompt, history, changes)
	if strings.TrimSpace(unresolvedReason) != "" {
		dimensions = append(dimensions, assessmentDimension{
			NameZH:   "最终验收",
			NameEN:   "Final acceptance",
			StatusZH: "未通过",
			StatusEN: "failed",
			DetailZH: trimForPrompt(oneLine(unresolvedReason), 360),
			DetailEN: trimForPrompt(oneLine(unresolvedReason), 360),
		})
	}
	if zh {
		var b strings.Builder
		if strings.TrimSpace(unresolvedReason) != "" {
			b.WriteString("最终测试结果：未通过，不能视为满足用户要求。")
		} else {
			b.WriteString("最终测试结果：通过，当前记录显示用户要求已满足。")
		}
		b.WriteString("\n\n验收维度：")
		for _, item := range dimensions {
			b.WriteString("\n- ")
			b.WriteString(item.NameZH)
			b.WriteString("：")
			b.WriteString(item.StatusZH)
			if item.DetailZH != "" {
				b.WriteString("。")
				b.WriteString(item.DetailZH)
			}
		}
		if strings.TrimSpace(unresolvedReason) != "" {
			b.WriteString("\n\n后续动作：继续修复上面的未通过维度后再重新运行全量验证。")
		} else {
			b.WriteString("\n\n后续动作：无需继续编排；如需审计细节，可展开页面里的命令详情。")
		}
		return b.String()
	}

	var b strings.Builder
	if strings.TrimSpace(unresolvedReason) != "" {
		b.WriteString("Final test result: failed; the current record must not be treated as satisfying the user request.")
	} else {
		b.WriteString("Final test result: passed; the current record shows the user request was satisfied.")
	}
	b.WriteString("\n\nAssessment dimensions:")
	for _, item := range dimensions {
		b.WriteString("\n- ")
		b.WriteString(item.NameEN)
		b.WriteString(": ")
		b.WriteString(item.StatusEN)
		if item.DetailEN != "" {
			b.WriteString(". ")
			b.WriteString(item.DetailEN)
		}
	}
	if strings.TrimSpace(unresolvedReason) != "" {
		b.WriteString("\n\nNext action: fix the failed dimensions above, then rerun full verification.")
	} else {
		b.WriteString("\n\nNext action: no further orchestration is required; expand command details for audit evidence.")
	}
	return b.String()
}

type assessmentDimension struct {
	NameZH   string
	NameEN   string
	StatusZH string
	StatusEN string
	DetailZH string
	DetailEN string
}

func finalRunAssessmentDimensions(userPrompt string, history []orchestrationTurn, changes workspaceChangeReport) []assessmentDimension {
	dimensions := []assessmentDimension{
		{
			NameZH:   "任务理解",
			NameEN:   "Task acceptance criterion",
			StatusZH: "已检查",
			StatusEN: "checked",
			DetailZH: trimForPrompt(oneLine(assessmentTaskCriterion(userPrompt)), 260),
			DetailEN: trimForPrompt(oneLine(assessmentTaskCriterion(userPrompt)), 260),
		},
		finalWorkspaceAssessmentDimension(userPrompt, history, changes),
		finalCommandAssessmentDimension(history),
	}
	if looksLikeFormalProofTask(userPrompt) {
		dimensions = append(dimensions, formalProofAssessmentDimensions(userPrompt, history)...)
	}
	dimensions = append(dimensions, finalRiskAssessmentDimension(userPrompt, history))
	return dimensions
}

func assessmentTaskCriterion(userPrompt string) string {
	prompt := strings.TrimSpace(userPrompt)
	if prompt == "" {
		return "latest user task"
	}
	return prompt
}

func finalWorkspaceAssessmentDimension(userPrompt string, history []orchestrationTurn, changes workspaceChangeReport) assessmentDimension {
	if !userTaskRequiresWorkspaceChange(userPrompt) {
		return assessmentDimension{
			NameZH:   "工作区变更",
			NameEN:   "Workspace changes",
			StatusZH: "不适用",
			StatusEN: "not applicable",
			DetailZH: "该任务没有明确要求写入或修改文件。",
			DetailEN: "The task did not explicitly require writing or editing files.",
		}
	}
	if hasWorkspaceChangeEvidence(history, changes) {
		detail := workspaceChangeDetail(history, changes)
		return assessmentDimension{
			NameZH:   "工作区变更",
			NameEN:   "Workspace changes",
			StatusZH: "通过",
			StatusEN: "passed",
			DetailZH: detail,
			DetailEN: detail,
		}
	}
	reason := missingWorkspaceChangeReason(changes, history)
	return assessmentDimension{
		NameZH:   "工作区变更",
		NameEN:   "Workspace changes",
		StatusZH: "未通过",
		StatusEN: "failed",
		DetailZH: reason,
		DetailEN: reason,
	}
}

func workspaceChangeDetail(history []orchestrationTurn, changes workspaceChangeReport) string {
	if len(changes.Changed) > 0 {
		return "记录到文件变更：" + trimForPrompt(strings.Join(changes.Changed, ", "), 260)
	}
	for _, item := range history {
		if meaningfulHandoffValue(item.HandoffFields.Changed) {
			return "Handoff 记录的变更：" + trimForPrompt(oneLine(item.HandoffFields.Changed), 260)
		}
	}
	return "记录到写入型命令。"
}

func finalCommandAssessmentDimension(history []orchestrationTurn) assessmentDimension {
	completed := 0
	failed := 0
	for _, command := range commandStates(history) {
		if strings.EqualFold(command.Status, "completed") {
			completed++
		}
		if commandFailed(command) {
			failed++
		}
	}
	if failed > 0 {
		detail := fmt.Sprintf("记录到 %d 个失败命令、%d 个完成命令。", failed, completed)
		return assessmentDimension{
			NameZH:   "命令验证",
			NameEN:   "Command verification",
			StatusZH: "未通过",
			StatusEN: "failed",
			DetailZH: detail,
			DetailEN: detail,
		}
	}
	detail := fmt.Sprintf("记录到 %d 个完成命令，未记录失败命令。", completed)
	if completed == 0 {
		detail = "未记录可审计的完成命令。"
	}
	return assessmentDimension{
		NameZH:   "命令验证",
		NameEN:   "Command verification",
		StatusZH: "通过",
		StatusEN: "passed",
		DetailZH: detail,
		DetailEN: detail,
	}
}

func finalRiskAssessmentDimension(userPrompt string, history []orchestrationTurn) assessmentDimension {
	if reason, ok := acceptanceFailureSignal(userPrompt, history); ok {
		return assessmentDimension{
			NameZH:   "剩余风险",
			NameEN:   "Remaining risk",
			StatusZH: "未通过",
			StatusEN: "failed",
			DetailZH: reason,
			DetailEN: reason,
		}
	}
	if len(history) > 0 {
		last := history[len(history)-1]
		if last.Err != "" || hasRiskyHandoff(last) {
			detail := compactBlockerSummary(last)
			if detail == "" {
				detail = "最后一轮仍记录错误或风险。"
			}
			return assessmentDimension{
				NameZH:   "剩余风险",
				NameEN:   "Remaining risk",
				StatusZH: "未通过",
				StatusEN: "failed",
				DetailZH: detail,
				DetailEN: detail,
			}
		}
	}
	return assessmentDimension{
		NameZH:   "剩余风险",
		NameEN:   "Remaining risk",
		StatusZH: "通过",
		StatusEN: "passed",
		DetailZH: "最后一轮未记录未解决风险。",
		DetailEN: "The last turn did not report unresolved risks.",
	}
}

func formalProofAssessmentDimensions(userPrompt string, history []orchestrationTurn) []assessmentDimension {
	evidence := collectProofAssessmentEvidence(history, workspaceChangeReport{})
	var dimensions []assessmentDimension
	dimensions = append(dimensions,
		boolAssessmentDimension("证明构建", "Proof build", evidence.proofBuild, "记录到 proof-assistant 构建或编译证据。", "缺少 proof-assistant 构建或编译证据。"),
		boolAssessmentDimension("占位符扫描", "Placeholder scan", evidence.placeholderScan, "记录到源代码占位符/假证明扫描证据。", "缺少源代码占位符/假证明扫描证据。"),
	)
	if containsAny(strings.ToLower(userPrompt), []string{"coq", ".v"}) {
		dimensions = append(dimensions, boolAssessmentDimension("假设审计", "Assumption audit", evidence.assumptionAudit, "记录到 Coq Print Assumptions 或 global context 审计证据。", "缺少 Coq Print Assumptions/global context 审计证据。"))
	}
	if looksLikeCoqUploadProofBenchmark(userPrompt) {
		dimensions = append(dimensions,
			boolAssessmentDimension("上传文件映射", "Uploaded input mapping", evidence.coqInputs, "记录到 Model.thy、Termination.thy、ROOT 均已纳入检查。", "缺少 Model.thy、Termination.thy、ROOT 全部被使用的证据。"),
			boolAssessmentDimension("原始证明义务", "Original proof obligation", evidence.originalObligation, "记录到 termination modify_lin 原始终止性/等价义务审计。", "缺少 termination modify_lin 原始终止性/等价义务审计。"),
		)
		if evidence.fuelShortcut {
			dimensions = append(dimensions, boolAssessmentDimension("燃料包装审计", "Fuel-wrapper audit", evidence.fuelJustified, "记录到 fuel/default_fuel 等价、下降和足够性证明证据。", "出现 fuel/default_fuel 迹象但缺少等价、下降和足够性证明证据。"))
		} else {
			dimensions = append(dimensions, boolAssessmentDimension("燃料包装审计", "Fuel-wrapper audit", true, "未记录 modify_lin_fuel/default_fuel 绕过迹象。", ""))
		}
	}
	return dimensions
}

func boolAssessmentDimension(nameZH, nameEN string, ok bool, passDetail, failDetail string) assessmentDimension {
	if ok {
		return assessmentDimension{
			NameZH:   nameZH,
			NameEN:   nameEN,
			StatusZH: "通过",
			StatusEN: "passed",
			DetailZH: passDetail,
			DetailEN: passDetail,
		}
	}
	return assessmentDimension{
		NameZH:   nameZH,
		NameEN:   nameEN,
		StatusZH: "未通过",
		StatusEN: "failed",
		DetailZH: failDetail,
		DetailEN: failDetail,
	}
}

func humanVisibleContent(content string) string {
	lines := strings.Split(content, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		if strings.HasPrefix(lower, "msg:") || strings.HasPrefix(lower, "handoff:") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func explicitEnglishResponseRequested(value string) bool {
	lower := strings.ToLower(value)
	for _, signal := range []string{
		"reply in english",
		"respond in english",
		"answer in english",
		"use english",
		"用英文",
		"使用英文",
		"英文回复",
	} {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func finalResponseNeedsFallback(content string) bool {
	return turnResponseNeedsFallback(content)
}

func turnResponseNeedsFallback(content string) bool {
	visible := humanVisibleContent(content)
	if visible == "" {
		return true
	}
	lower := strings.ToLower(visible)
	progressStarts := []string{
		"我会", "我将", "接下来", "正在", "i will", "i'll", "i am going to", "next i",
	}
	for _, prefix := range progressStarts {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	signals := []string{
		"final", "conclusion", "summary", "verified", "verification", "passed", "completed", "remaining", "risk",
		"最终", "结论", "总结", "确认", "验证", "通过", "完成", "剩余", "风险", "正确",
	}
	count := 0
	for _, signal := range signals {
		if strings.Contains(lower, signal) {
			count++
		}
	}
	if count >= 2 {
		return false
	}
	return count < 2 && len([]rune(visible)) < 320
}

func latestMeaningfulConclusion(history []orchestrationTurn) string {
	for i := len(history) - 1; i >= 0; i-- {
		content := summarizeTurnForContinuity(history[i], 700)
		if content == "" {
			continue
		}
		lower := strings.ToLower(content)
		if strings.Contains(lower, "结论") || strings.Contains(lower, "确认") ||
			strings.Contains(lower, "通过") || strings.Contains(lower, "正确") ||
			strings.Contains(lower, "conclusion") || strings.Contains(lower, "verified") ||
			strings.Contains(lower, "passed") || strings.Contains(lower, "correct") {
			return trimForPrompt(oneLine(content), 700)
		}
	}
	return ""
}

func summarizeTurnForContinuity(item orchestrationTurn, max int) string {
	if item.Handoff != "" || item.HandoffFields != (orchestrationHandoffFields{}) {
		if summary := formatHandoffFields(item.HandoffFields); summary != "" {
			return trimForPrompt(summary, max)
		}
		return trimForPrompt(item.Handoff, max)
	}
	if blocker := compactBlockerSummary(item); blocker != "" {
		return trimForPrompt("Blocker: "+blocker, max)
	}
	content := humanVisibleContent(item.Content)
	if content == "" {
		return ""
	}
	lines := strings.Split(content, "\n")
	var selected []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || isLowValueFallbackLine(line) {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "阻塞") || strings.Contains(lower, "风险") ||
			strings.Contains(lower, "未完成") || strings.Contains(lower, "未消除") ||
			strings.Contains(lower, "blocker") || strings.Contains(lower, "risk") ||
			strings.Contains(lower, "not complete") || strings.Contains(lower, "failed") {
			selected = append(selected, line)
			continue
		}
		if len(selected) < 2 {
			selected = append(selected, line)
		}
		if len(selected) >= 4 {
			break
		}
	}
	if len(selected) == 0 {
		return trimForPrompt(oneLine(content), max)
	}
	return trimForPrompt(oneLine(strings.Join(selected, " ")), max)
}

func isLowValueFallbackLine(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return true
	}
	lowValue := []string{
		"结果概览", "已验证：没有可提炼", "可展开命令详情", "如需审计细节",
		"本轮编排已完成，并已记录当前可确认的结果",
		"this orchestration turn completed and the current confirmed state was recorded",
		"result overview", "expand command details", "no concise command summary",
	}
	for _, signal := range lowValue {
		if strings.Contains(lower, strings.ToLower(signal)) {
			return true
		}
	}
	return false
}

func completedCommandSummaries(turns []orchestrationTurn, max int) []string {
	commands := commandStates(turns)
	var out []string
	for i := len(commands) - 1; i >= 0 && len(out) < max; i-- {
		command := commands[i]
		if !strings.EqualFold(command.Status, "completed") {
			continue
		}
		out = append(out, formatCommandSummary(command))
	}
	return reverseStrings(out)
}

func completedVerificationSummaries(turns []orchestrationTurn, zh bool, max int) []string {
	commands := commandStates(turns)
	var readFiles []string
	var commandLabels []string
	for _, command := range commands {
		if !strings.EqualFold(command.Status, "completed") {
			continue
		}
		label := strings.TrimSpace(command.Command)
		if isClaudeReadCommand(label) {
			path := strings.TrimSpace(strings.TrimPrefix(label, "Read "))
			if path != "" {
				name := filepath.Base(path)
				if name != "." && name != string(filepath.Separator) {
					readFiles = append(readFiles, name)
				}
			}
			continue
		}
		if label != "" {
			commandLabels = append(commandLabels, trimForPrompt(oneLine(label), 120))
		}
	}

	var out []string
	if len(readFiles) > 0 && len(out) < max {
		if zh {
			out = append(out, fmt.Sprintf("读取并检查了 %d 个文件：%s。", len(readFiles), formatInlineList(readFiles, "、")))
		} else {
			out = append(out, fmt.Sprintf("Read and checked %d file(s): %s.", len(readFiles), formatInlineList(readFiles, ", ")))
		}
	}
	for _, label := range commandLabels {
		if len(out) >= max {
			break
		}
		if zh {
			out = append(out, fmt.Sprintf("执行完成：`%s`。", label))
		} else {
			out = append(out, fmt.Sprintf("Completed `%s`.", label))
		}
	}
	return out
}

func formatInlineList(values []string, separator string) string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, "`"+value+"`")
	}
	if separator == "" {
		separator = ", "
	}
	return strings.Join(out, separator)
}

func failedCommandCount(turns []orchestrationTurn) int {
	count := 0
	for _, command := range commandStates(turns) {
		if commandFailed(command) {
			count++
		}
	}
	return count
}

func failedCommandSummaries(turns []orchestrationTurn, max int) []string {
	commands := commandStates(turns)
	var out []string
	for i := len(commands) - 1; i >= 0 && len(out) < max; i-- {
		command := commands[i]
		if commandFailed(command) {
			out = append(out, formatCommandSummary(command))
		}
	}
	return reverseStrings(out)
}

func commandFailed(command orchestrationCommandState) bool {
	if strings.EqualFold(command.Status, "failed") || strings.EqualFold(command.Status, "error") {
		return true
	}
	return command.ExitCode != nil && *command.ExitCode != 0
}

type orchestrationCommandState struct {
	ID       string
	Status   string
	Command  string
	Output   string
	ExitCode *int
}

func commandStates(turns []orchestrationTurn) []orchestrationCommandState {
	var states []orchestrationCommandState
	indexes := map[string]int{}
	for _, turn := range turns {
		for _, tool := range turn.Tools {
			toolKey := tool.ID
			if toolKey == "" {
				toolKey = tool.Command
			}
			if toolKey == "" {
				toolKey = fmt.Sprintf("tool-%d", len(states))
			}
			key := turn.TurnID + ":" + toolKey
			index, ok := indexes[key]
			if !ok {
				index = len(states)
				indexes[key] = index
				states = append(states, orchestrationCommandState{ID: tool.ID})
			}
			state := states[index]
			if tool.ID != "" {
				state.ID = tool.ID
			}
			if tool.Status != "" {
				state.Status = tool.Status
			}
			if tool.Command != "" {
				state.Command = tool.Command
			}
			if tool.Output != "" {
				state.Output = tool.Output
			}
			if tool.ExitCode != nil {
				state.ExitCode = tool.ExitCode
			}
			states[index] = state
		}
	}
	return states
}

func formatCommandSummary(command orchestrationCommandState) string {
	label := strings.TrimSpace(command.Command)
	if label == "" {
		label = "command"
	}
	status := command.Status
	if status == "" {
		status = "completed"
	}
	parts := []string{fmt.Sprintf("`%s` %s", label, status)}
	if command.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *command.ExitCode))
	}
	if output := trimForPrompt(oneLine(command.Output), 160); output != "" {
		parts = append(parts, "output: "+output)
	}
	return strings.Join(parts, "; ")
}

func reverseStrings(values []string) []string {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
	return values
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func containsCJK(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

const orchestrationHandoffContract = "Handoff: status=<needs_next|blocked|resolved>; changed=<files or none>; verified=<commands or none>; next=<one action>; risks=<open issue or none>"
const orchestrationMsgContract = "Msg: to=<next-role|user>; intent=<implement|review|challenge|final>; need=<one request or none>"
const orchestrationLanguageRule = "Language rule: write all user-visible prose in Chinese by default unless the user explicitly asks for another language. Keep the machine-readable Msg: and Handoff: field names and values in the required English shape."

func composeOrchestrationPrompt(mode, userPrompt, contextSummary string, resume bool, role, cli string, turn, maxTurns int, history []orchestrationTurn) string {
	var b strings.Builder
	b.WriteString("You are participating in a local CLI orchestration run.\n")
	b.WriteString("Use your native file, shell, MCP, and skill capabilities when useful. Do not assume the other CLI can see your private reasoning.\n\n")
	toRole, toCLI := nextRoleCLI(mode, turn)
	if turn >= maxTurns {
		toRole, toCLI = "user", ""
	}
	b.WriteString(fmt.Sprintf("From: %s/%s\n", role, cli))
	if toCLI != "" {
		b.WriteString(fmt.Sprintf("To: %s/%s\n", toRole, toCLI))
	} else {
		b.WriteString("To: user\n")
	}
	b.WriteString(fmt.Sprintf("Mode: %s\n\n", mode))
	b.WriteString(orchestrationLanguageRule)
	b.WriteString("\n\n")
	if resume {
		b.WriteString("This is a continuation of the same user-visible orchestration conversation. Maintain continuity with the compacted context, while treating the latest user task as authoritative.\n\n")
	}
	b.WriteString("Latest user task is authoritative. Track the user's core acceptance criterion explicitly, and do not declare success unless that criterion is met.\n")
	if mode == "debate" {
		if role == "proposer" {
			b.WriteString("Strategy: evidence-focused debate. Keep claims testable, cite files or command results, and avoid repeating the full transcript.\n")
			b.WriteString("Role: proposer. State the strongest concrete thesis or patch, make progress if needed, and leave a falsifiable handoff for the critic.\n")
		} else {
			b.WriteString("Strategy: evidence-focused debate. Keep claims testable, cite files or command results, and avoid repeating the full transcript.\n")
			b.WriteString("Role: critic. Try to falsify the proposer with concrete counterexamples, command output, or code evidence. Fix clear issues when cheaper than describing them.\n")
		}
	} else {
		if role == "implementer" {
			b.WriteString("Strategy: builder-reviewer collaboration. Optimize for shared workspace progress with short, auditable handoffs.\n")
			b.WriteString("Role: implementer. Make concrete progress on the user's core acceptance criterion, edit files when appropriate, and leave only the key state the reviewer needs.\n")
		} else {
			b.WriteString("Strategy: builder-reviewer collaboration. Optimize for shared workspace progress with short, auditable handoffs.\n")
			b.WriteString("Role: reviewer. Independently inspect the implementer's result, fix obvious issues, and verify with focused commands when appropriate. Explicitly audit whether the previous turn advanced the user's core acceptance criterion; do not treat a narrow validation such as compiling as resolved when the user asked for a stronger outcome.\n")
		}
	}
	b.WriteString(fmt.Sprintf("Turn: %d of %d. CLI: %s.\n\n", turn, maxTurns, cli))
	if proofTask := formalProofTaskGuidance(userPrompt, mode, role); proofTask != "" {
		b.WriteString(proofTask)
		b.WriteString("\n")
	}
	b.WriteString("Token budget rules: do not restate the full history, do not quote large files, and keep inter-agent notes compact. Prefer file paths, command names, and exact unresolved blockers.\n")
	b.WriteString("End your visible response with two compact machine-scannable lines in exactly these shapes:\n")
	b.WriteString(orchestrationMsgContract)
	b.WriteByte('\n')
	b.WriteString(orchestrationHandoffContract)
	b.WriteString("\nUse status=resolved only when you also give a concise user-visible conclusion before the handoff.\n\n")
	if strings.TrimSpace(contextSummary) != "" {
		b.WriteString("Compacted context from earlier tasks in this conversation:\n")
		b.WriteString(trimForPrompt(contextSummary, 14000))
		b.WriteString("\n\n")
	}
	b.WriteString("Original user task:\n")
	b.WriteString(userPrompt)
	b.WriteString("\n\n")
	if len(history) > 0 {
		b.WriteString("Compact prior-turn handoffs:\n")
		for _, item := range history {
			b.WriteString(formatCompactPriorTurn(item))
		}
		b.WriteByte('\n')
	}
	if turn == maxTurns {
		b.WriteString("This is the final scheduled turn. Summarize the final state, verification results, and remaining risks.\n")
		b.WriteString("Return a concise user-visible final answer. Do not rely on command logs alone; state what was accomplished, what was verified, and any remaining issue.\n")
	}
	return b.String()
}

func composeFinalVerifierPrompt(mode, userPrompt, contextSummary string, resume bool, role, cli string, history []orchestrationTurn) string {
	var b strings.Builder
	b.WriteString("You are the lightweight final verifier for a local CLI orchestration run.\n")
	b.WriteString("Inspect only the reported changes, failed commands, and unresolved risks. Avoid broad new work; make a small fix only if it is clearly required to complete verification.\n\n")
	b.WriteString("Verify the actual acceptance criterion from the latest user task against concrete files or command output. If the user named a concrete completion condition, inspect the relevant evidence and do not mark resolved while that condition remains unmet.\n\n")
	b.WriteString(fmt.Sprintf("From: %s/%s\n", role, cli))
	b.WriteString("To: user\n")
	b.WriteString(fmt.Sprintf("Mode: %s\n\n", mode))
	b.WriteString(orchestrationLanguageRule)
	b.WriteString("\n\n")
	if resume {
		b.WriteString("This is a continuation of the same user-visible orchestration conversation. Prefer the latest user task over older details.\n\n")
	}
	if proofTask := formalProofVerifierGuidance(userPrompt, mode); proofTask != "" {
		b.WriteString(proofTask)
		b.WriteString("\n")
	}
	b.WriteString("End your visible response with a concise final conclusion and the same compact lines:\n")
	b.WriteString(orchestrationMsgContract)
	b.WriteByte('\n')
	b.WriteString(orchestrationHandoffContract)
	b.WriteString("\n\n")
	if strings.TrimSpace(contextSummary) != "" {
		b.WriteString("Compacted context from earlier tasks in this conversation:\n")
		b.WriteString(trimForPrompt(contextSummary, 6000))
		b.WriteString("\n\n")
	}
	b.WriteString("Original user task:\n")
	b.WriteString(userPrompt)
	b.WriteString("\n\nStructured prior-turn state:\n")
	for _, item := range history {
		b.WriteString(formatCompactPriorTurn(item))
	}
	if failures := failedCommandSummaries(history, 4); len(failures) > 0 {
		b.WriteString("\nFailed or suspicious command outcomes:\n")
		for _, failure := range failures {
			b.WriteString("- ")
			b.WriteString(failure)
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func composeWorkspaceChangeRemediationPrompt(mode, userPrompt, contextSummary string, resume bool, role, cli string, history []orchestrationTurn, reason string) string {
	var b strings.Builder
	b.WriteString("You are the remediation implementer for a local CLI orchestration run.\n")
	b.WriteString("The prior turns did not produce concrete workspace file changes for a change-oriented user task. Do not only inspect or re-verify. Make the smallest real workspace file change that advances the user's latest acceptance criterion, then verify it. If a real file change is impossible, report status=blocked with the exact blocker.\n\n")
	b.WriteString(fmt.Sprintf("From: %s/%s\n", role, cli))
	b.WriteString("To: user\n")
	b.WriteString(fmt.Sprintf("Mode: %s\n\n", mode))
	b.WriteString(orchestrationLanguageRule)
	b.WriteString("\n\n")
	if resume {
		b.WriteString("This is a continuation of the same user-visible orchestration conversation. Prefer the latest user task over older details.\n\n")
	}
	if proofTask := formalProofTaskGuidance(userPrompt, mode, role); proofTask != "" {
		b.WriteString(proofTask)
		b.WriteString("\n")
	}
	if strings.TrimSpace(contextSummary) != "" {
		b.WriteString("Compacted context from earlier tasks in this conversation:\n")
		b.WriteString(trimForPrompt(contextSummary, 14000))
		b.WriteString("\n\n")
	}
	b.WriteString("Remediation trigger:\n")
	b.WriteString(trimForPrompt(reason, 1000))
	b.WriteString("\n\n")
	if len(history) > 0 {
		b.WriteString("Compact prior-turn handoffs:\n")
		for _, item := range history {
			b.WriteString(formatCompactPriorTurn(item))
		}
		b.WriteByte('\n')
	}
	b.WriteString("Original user task:\n")
	b.WriteString(userPrompt)
	b.WriteString("\n\n")
	b.WriteString("End your visible response with these compact lines, and set changed to the concrete files you actually changed:\n")
	b.WriteString(orchestrationMsgContract)
	b.WriteByte('\n')
	b.WriteString(orchestrationHandoffContract)
	return b.String()
}

func composeFinalAssessmentRemediationPrompt(mode, userPrompt, contextSummary string, resume bool, role, cli string, history []orchestrationTurn, changes workspaceChangeReport, reason string) string {
	var b strings.Builder
	b.WriteString("You are the final-assessment remediation implementer for a local CLI orchestration run.\n")
	b.WriteString("The post-test multi-dimensional assessment found that the latest user task is still not satisfied. Continue fixing now: make concrete workspace changes or add missing proof/verification evidence, then rerun the relevant checks. Do not merely restate the failure. If the gap cannot be fixed in this environment, report status=blocked with the exact blocker.\n\n")
	b.WriteString(fmt.Sprintf("From: %s/%s\n", role, cli))
	b.WriteString("To: user\n")
	b.WriteString(fmt.Sprintf("Mode: %s\n\n", mode))
	b.WriteString(orchestrationLanguageRule)
	b.WriteString("\n\n")
	if resume {
		b.WriteString("This is a continuation of the same user-visible orchestration conversation. Prefer the latest user task over older details.\n\n")
	}
	if proofTask := formalProofTaskGuidance(userPrompt, mode, role); proofTask != "" {
		b.WriteString(proofTask)
		b.WriteString("\n")
	}
	b.WriteString("Assessment failure to fix:\n")
	b.WriteString(trimForPrompt(reason, 1200))
	b.WriteString("\n\nCurrent terminal assessment before remediation:\n")
	b.WriteString(trimForPrompt(finalRunAssessmentSummary(userPrompt, history, changes, reason), 2400))
	b.WriteString("\n\n")
	if strings.TrimSpace(contextSummary) != "" {
		b.WriteString("Compacted context from earlier tasks in this conversation:\n")
		b.WriteString(trimForPrompt(contextSummary, 10000))
		b.WriteString("\n\n")
	}
	if len(history) > 0 {
		b.WriteString("Compact prior-turn handoffs:\n")
		for _, item := range history {
			b.WriteString(formatCompactPriorTurn(item))
		}
		b.WriteByte('\n')
	}
	b.WriteString("Original user task:\n")
	b.WriteString(userPrompt)
	b.WriteString("\n\n")
	b.WriteString("End your visible response with a concise remediation result, including commands run, and these compact lines:\n")
	b.WriteString(orchestrationMsgContract)
	b.WriteByte('\n')
	b.WriteString(orchestrationHandoffContract)
	return b.String()
}

func formalProofTaskGuidance(userPrompt, mode, role string) string {
	if !looksLikeFormalProofTask(userPrompt) {
		return ""
	}
	var b strings.Builder
	b.WriteString("Formal proof task guardrails:\n")
	b.WriteString("- Treat build success as a smoke check only. The acceptance criterion is the requested proof obligation, not merely compiling.\n")
	b.WriteString("- Do not weaken theorem statements, change the target definition's semantics, move the obligation elsewhere, or add trust assumptions such as Axiom, Parameter, Conjecture, Admitted, admit, Abort, sorry, quick_and_dirty, Guard Checking changes, bypass_check, TODO, or placeholders.\n")
	b.WriteString("- If you introduce a bounded/fuel wrapper or default fuel for a recursive function, you must also prove equivalence to the original recursive semantics, the required termination/decrease measure, and that the default fuel is sufficient for every intended input. Otherwise report status=needs_next or blocked.\n")
	b.WriteString("- Include a proof audit when relevant: placeholder scans with rg, Coq Print Assumptions <target> showing Closed under the global context, Lean #print axioms <target>, Isabelle thm_oracles <target>, and the project build command.\n")
	b.WriteString("- Keep a proof-obligation ledger in the handoff: target theorem/definition, missing obligation, semantic constraints, attempted proof path, exact blocker, and verification command.\n")
	if mode == "debate" {
		b.WriteString("- Debate proof workflow: the proposer must leave a falsifiable proof claim or patch, and the critic must decide whether the original obligation is actually discharged; unresolved falsification blocks status=resolved.\n")
		if role == "critic" {
			b.WriteString("- Debate critic strategy: first try to falsify the proof by checking for weakened statements, fuel/default_fuel shortcuts, hidden axioms/admissions, missing equivalence lemmas, or obligations that were made unprovable but hidden by wrappers.\n")
		} else {
			b.WriteString("- Debate proposer strategy: present the strongest proof plan or patch, name the exact lemmas/audit commands that would validate it, and explicitly state why it preserves the original statement without fuel shortcuts or added assumptions.\n")
		}
	} else if role == "reviewer" || role == "verifier" {
		b.WriteString("- Reviewer strategy: inspect the diff and proof script for semantic weakening before accepting any successful build; reject compile-only evidence when the proof obligation remains open.\n")
	} else {
		b.WriteString("- Implementer strategy: prefer proving the original obligation directly or proving a well-founded decrease/equivalence lemma before changing definitions.\n")
	}
	return b.String()
}

func formalProofVerifierGuidance(userPrompt, mode string) string {
	if !looksLikeFormalProofTask(userPrompt) {
		return ""
	}
	lines := []string{
		"Formal proof final verifier guardrails:",
		"- Verify the original proof obligation, not just that Coq/Isabelle/Lean accepts the project.",
		"- Reject status=resolved if any target theorem/definition was weakened, any proof obligation was replaced by a bounded/fuel wrapper without equivalence and fuel-sufficiency proofs, or any Axiom/Parameter/Conjecture/Admitted/admit/Abort/sorry/quick_and_dirty/Guard Checking/bypass_check/TODO/placeholder remains.",
		"- Prefer proof-assistant dependency checks when available: Coq Print Assumptions <target> with Closed under the global context, Lean #print axioms <target>, Isabelle thm_oracles <target>, plus placeholder scans and the project build command.",
		"- For termination tasks, require evidence of the actual decrease/well-founded measure or a proof that the encoded recursion is equivalent to the original semantics for all intended inputs.",
		"- End with a multi-dimensional result assessment that is visible to the browser: uploaded inputs accounted for, new project/workspace path, build result, placeholder scan, proof-assistant assumption/oracle check, original obligation/equivalence or termination audit, and remaining risks.",
	}
	if looksLikeCoqUploadProofBenchmark(userPrompt) {
		lines = append(lines,
			"- This task matches the Coq upload benchmark with Model.thy, Termination.thy, and ROOT. Your visible final conclusion must explicitly assess: Model.thy/Termination.thy/ROOT were used, a new Coq project folder was written under the requested cwd, make/coqc passed, source-only placeholder scan found no forbidden tokens, Coq Print Assumptions showed Closed under the global context, and termination modify_lin was solved without modify_lin_fuel/default_fuel or with proved equivalence, decrease, and fuel sufficiency.",
		)
	}
	if mode == "debate" {
		lines = append(lines, "- Debate verifier strategy: synthesize the adversarial result; concrete critic falsification of weakened semantics, fuel/default_fuel shortcuts, missing equivalence, or hidden assumptions overrides proposer confidence.")
	}
	return strings.Join(lines, "\n")
}

func looksLikeFormalProofTask(text string) bool {
	lower := strings.ToLower(text)
	return containsAny(lower, []string{
		"coq", "isabelle", "lean", ".v", ".thy", ".lean", "_coqproject",
		"theorem", "lemma", "proof", "termination", "well-founded", "well founded",
		"sorry", "admitted", "admit", "axiom", "parameter", "conjecture", "quick_and_dirty", "bypass_check", "placeholder",
		"定理", "引理", "证明", "终止", "递归", "补全缺失的证明", "占位符",
	})
}

func looksLikeCoqUploadProofBenchmark(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "coq") &&
		strings.Contains(lower, "model.thy") &&
		strings.Contains(lower, "termination.thy") &&
		strings.Contains(lower, "root") &&
		containsAny(lower, []string{"补全缺失的证明", "占位符", "placeholder", "modify_lin", "termination"})
}

func trimForPrompt(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "\n[truncated]"
}

func formatCompactPriorTurn(item orchestrationTurn) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("- [%s via %s] ", item.Role, item.CLI))
	if item.Msg != "" {
		b.WriteString(oneLine(item.Msg))
		if item.Handoff != "" {
			b.WriteString("; ")
		}
	}
	summary := formatHandoffFields(item.HandoffFields)
	if summary == "" {
		summary = item.Handoff
	}
	if summary == "" {
		summary = summarizeTurnForContinuity(item, 700)
	}
	if summary == "" {
		summary = "no visible answer"
	}
	b.WriteString(oneLine(summary))
	if commands := completedCommandSummaries([]orchestrationTurn{item}, 2); len(commands) > 0 {
		b.WriteString("; verified: ")
		b.WriteString(strings.Join(commands, " | "))
	}
	if failures := failedCommandSummaries([]orchestrationTurn{item}, 2); len(failures) > 0 {
		b.WriteString("; failed: ")
		b.WriteString(strings.Join(failures, " | "))
	}
	if item.Err != "" {
		b.WriteString("; error: ")
		b.WriteString(oneLine(trimForPrompt(item.Err, 300)))
	}
	b.WriteByte('\n')
	return b.String()
}

func formatHandoffFields(fields orchestrationHandoffFields) string {
	var parts []string
	if fields.Status != "" {
		parts = append(parts, "status="+fields.Status)
	}
	if meaningfulHandoffValue(fields.Changed) {
		parts = append(parts, "changed="+fields.Changed)
	}
	if meaningfulHandoffValue(fields.Verified) {
		parts = append(parts, "verified="+fields.Verified)
	}
	if meaningfulHandoffValue(fields.Next) {
		parts = append(parts, "next="+fields.Next)
	}
	if meaningfulHandoffValue(fields.Risks) {
		parts = append(parts, "risks="+fields.Risks)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Handoff: " + strings.Join(parts, "; ")
}

func meaningfulHandoffValue(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value != "" && value != "none" && value != "n/a" && value != "na"
}

func compactTurnContent(content string, max int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if handoff := extractHandoff(content); handoff != "" {
		return handoff
	}
	lines := strings.Split(content, "\n")
	var selected []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "final") || strings.Contains(lower, "conclusion") ||
			strings.Contains(lower, "summary") || strings.Contains(lower, "verified") ||
			strings.Contains(lower, "remaining") || strings.Contains(lower, "risk") ||
			strings.Contains(lower, "结论") || strings.Contains(lower, "总结") ||
			strings.Contains(lower, "验证") || strings.Contains(lower, "风险") ||
			len(selected) < 2 {
			selected = append(selected, line)
		}
		if len(selected) >= 5 {
			break
		}
	}
	if len(selected) == 0 {
		return trimForPrompt(oneLine(content), max)
	}
	return trimForPrompt(oneLine(strings.Join(selected, " ")), max)
}

func compactBlockerSummary(item orchestrationTurn) string {
	var parts []string
	if item.Err != "" {
		parts = append(parts, "error="+trimForPrompt(oneLine(item.Err), 180))
	}
	if meaningfulHandoffValue(item.HandoffFields.Next) {
		parts = append(parts, "next="+trimForPrompt(oneLine(item.HandoffFields.Next), 220))
	}
	if meaningfulHandoffValue(item.HandoffFields.Risks) {
		parts = append(parts, "risks="+trimForPrompt(oneLine(item.HandoffFields.Risks), 260))
	}
	if len(parts) > 0 {
		return strings.Join(parts, "; ")
	}
	if strings.EqualFold(strings.TrimSpace(item.HandoffFields.Status), "blocked") && item.Handoff != "" {
		return trimForPrompt(oneLine(item.Handoff), 500)
	}
	for _, command := range failedCommandSummaries([]orchestrationTurn{item}, 2) {
		parts = append(parts, command)
	}
	if len(parts) > 0 {
		return strings.Join(parts, " | ")
	}
	return ""
}

func latestBlocker(history []orchestrationTurn) (string, bool) {
	for i := len(history) - 1; i >= 0; i-- {
		if blocker := compactBlockerSummary(history[i]); blocker != "" {
			return blocker, true
		}
	}
	return "", false
}

func repeatedBlockingHandoff(history []orchestrationTurn) (string, bool) {
	const threshold = 3
	seen := 0
	last := ""
	for i := len(history) - 1; i >= 0; i-- {
		item := history[i]
		if !turnReportsBlocking(item) {
			break
		}
		blocker := normalizeBlockerKey(item)
		if blocker == "" {
			break
		}
		if last == "" {
			last = blocker
		}
		if blocker != last {
			break
		}
		seen++
		if seen >= threshold {
			return compactBlockerSummary(item), true
		}
	}
	return "", false
}

func snapshotWorkspace(root string, ignoredPaths ...string) workspaceSnapshot {
	root = strings.TrimSpace(root)
	if root == "" {
		root = "."
	}
	root = expandHome(root)
	abs, err := filepath.Abs(root)
	if err == nil {
		root = abs
	}
	root = filepath.Clean(root)
	snapshot := workspaceSnapshot{
		Root:      root,
		Files:     map[string]workspaceFileState{},
		Available: true,
	}
	ignored := normalizeWorkspaceSnapshotIgnoredPaths(root, ignoredPaths)
	const maxSnapshotFiles = 20000
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		name := entry.Name()
		rel, relOK := workspaceSnapshotRel(root, path)
		if relOK {
			if shouldIgnoreWorkspaceSnapshotPath(rel, ignored) {
				if entry.IsDir() && path != root {
					return filepath.SkipDir
				}
				return nil
			}
		}
		if entry.IsDir() {
			if path != root && shouldSkipWorkspaceSnapshotDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&fs.ModeType != 0 {
			return nil
		}
		if !relOK {
			return nil
		}
		if shouldSkipWorkspaceSnapshotFile(rel) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		snapshot.Files[filepath.ToSlash(rel)] = workspaceFileState{
			Size:    info.Size(),
			ModTime: info.ModTime().UnixNano(),
		}
		if len(snapshot.Files) > maxSnapshotFiles {
			snapshot.Truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		snapshot.Available = false
		snapshot.Err = err.Error()
	}
	return snapshot
}

func workspaceSnapshotIgnoredPaths(cfg *config.Config) []string {
	if cfg == nil {
		return nil
	}
	var paths []string
	if cfg.Hub.DBPath != "" {
		db := expandHome(cfg.Hub.DBPath)
		paths = append(paths, db, db+"-wal", db+"-shm")
	}
	if cfg.Bridge.MachineIDFile != "" {
		paths = append(paths, expandHome(cfg.Bridge.MachineIDFile))
	}
	return paths
}

func normalizeWorkspaceSnapshotIgnoredPaths(root string, paths []string) map[string]bool {
	ignored := map[string]bool{}
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		path = expandHome(path)
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		path = filepath.Clean(path)
		if rel, err := filepath.Rel(root, path); err == nil && rel != "." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
			ignored[filepath.ToSlash(rel)] = true
		}
	}
	return ignored
}

func workspaceSnapshotRel(root, path string) (string, bool) {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

func shouldIgnoreWorkspaceSnapshotPath(rel string, ignored map[string]bool) bool {
	return ignored[filepath.ToSlash(rel)]
}

func shouldSkipWorkspaceSnapshotDir(name string) bool {
	switch name {
	case ".git", ".hg", ".svn", ".codex-bridge", "node_modules", ".next", "dist", "build", "target", ".lake", ".elan":
		return true
	default:
		return false
	}
}

func shouldSkipWorkspaceSnapshotFile(rel string) bool {
	base := filepath.Base(rel)
	if strings.HasSuffix(base, "~") || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".swp") {
		return true
	}
	return false
}

func diffWorkspaceSnapshots(before, after workspaceSnapshot) workspaceChangeReport {
	report := workspaceChangeReport{
		Root:      after.Root,
		Available: before.Available && after.Available,
		Truncated: before.Truncated || after.Truncated,
	}
	if !before.Available {
		report.Err = before.Err
	}
	if !after.Available {
		if report.Err != "" {
			report.Err += "; "
		}
		report.Err += after.Err
	}
	if !report.Available {
		return report
	}
	changed := map[string]bool{}
	for path, afterState := range after.Files {
		beforeState, ok := before.Files[path]
		if !ok || beforeState != afterState {
			changed[path] = true
		}
	}
	for path := range before.Files {
		if _, ok := after.Files[path]; !ok {
			changed[path] = true
		}
	}
	report.Changed = make([]string, 0, len(changed))
	for path := range changed {
		report.Changed = append(report.Changed, path)
	}
	sort.Strings(report.Changed)
	return report
}

func unresolvedFinalRun(userPrompt string, history []orchestrationTurn, changes workspaceChangeReport) (string, bool) {
	if len(history) == 0 {
		return "no turn result was produced", true
	}
	if reason, ok := acceptanceFailureSignal(userPrompt, history); ok {
		return reason, true
	}
	if userTaskRequiresWorkspaceChange(userPrompt) && !hasWorkspaceChangeEvidence(history, changes) {
		return missingWorkspaceChangeReason(changes, history), true
	}
	if reason, ok := formalProofAssessmentGap(userPrompt, history, changes); ok {
		return reason, true
	}
	last := history[len(history)-1]
	if last.Err != "" {
		return "last turn errored: " + trimForPrompt(oneLine(last.Err), 300), true
	}
	status := strings.ToLower(strings.TrimSpace(last.HandoffFields.Status))
	if status == "resolved" {
		if !resolvedHandoffReady(last.Content) {
			return "resolved handoff is missing a user-visible conclusion", true
		}
		return "", false
	}
	if status == "blocked" {
		if blocker := compactBlockerSummary(last); blocker != "" {
			return blocker, true
		}
		return "last turn reported blocked", true
	}
	if status == "needs_next" {
		if blocker := compactBlockerSummary(last); blocker != "" {
			return blocker, true
		}
		return "last turn still needs next action", true
	}
	if failedCommandCount(history) > 0 {
		if failure, ok := latestBlocker(history); ok {
			return failure, true
		}
		return "one or more commands failed", true
	}
	if hasRiskyHandoff(last) {
		if blocker := compactBlockerSummary(last); blocker != "" {
			return blocker, true
		}
		return "last turn reported unresolved risk", true
	}
	return "", false
}

func shouldRunWorkspaceChangeRemediation(userPrompt string, history []orchestrationTurn, changes workspaceChangeReport) bool {
	if len(history) == 0 {
		return false
	}
	return userTaskRequiresWorkspaceChange(userPrompt) && !hasWorkspaceChangeEvidence(history, changes)
}

func userTaskRequiresWorkspaceChange(prompt string) bool {
	lower := strings.ToLower(prompt)
	signals := []string{
		"修改", "改造", "改正", "修复", "实现", "生成", "创建", "新建", "添加", "删除", "移除", "替换",
		"写入", "保存", "消除", "补全", "落地", "做出改变", "文件做出改变",
		"modify", "change", "fix", "repair", "implement", "generate", "create", "add", "delete",
		"remove", "replace", "write", "save", "edit", "update", "fill in",
	}
	for _, signal := range signals {
		if strings.Contains(lower, strings.ToLower(signal)) {
			return true
		}
	}
	return false
}

func hasWorkspaceChangeEvidence(history []orchestrationTurn, changes workspaceChangeReport) bool {
	if len(changes.Changed) > 0 {
		return true
	}
	if changes.Available {
		return false
	}
	for _, item := range history {
		if meaningfulHandoffValue(item.HandoffFields.Changed) {
			return true
		}
		for _, command := range commandStates([]orchestrationTurn{item}) {
			if commandFailed(command) || !strings.EqualFold(command.Status, "completed") {
				continue
			}
			if commandLooksLikeWorkspaceWrite(command.Command) {
				return true
			}
		}
	}
	return false
}

func commandLooksLikeWorkspaceWrite(command string) bool {
	command = strings.TrimSpace(strings.ToLower(command))
	if command == "" {
		return false
	}
	signals := []string{
		"apply_patch", "cat >", "cat <<", "tee ", "python", "perl -", "ruby -", "node -e",
		"touch ", "mkdir ", "cp ", "mv ", "rm ", "sed -i", "writefile", "write_file",
		"create file", "edit file",
	}
	for _, signal := range signals {
		if strings.Contains(command, signal) {
			return true
		}
	}
	return false
}

func missingWorkspaceChangeReason(changes workspaceChangeReport, history []orchestrationTurn) string {
	withBlocker := func(reason string) string {
		if blocker, ok := latestBlocker(history); ok && blocker != "" {
			return reason + "; latest blocker: " + blocker
		}
		return reason
	}
	if !changes.Available {
		if changes.Err != "" {
			return withBlocker("workspace change check failed: " + trimForPrompt(oneLine(changes.Err), 300))
		}
		return withBlocker("workspace change check failed")
	}
	if changes.Truncated {
		return withBlocker("no concrete file change was recorded for a change-oriented task; workspace snapshot was truncated")
	}
	return withBlocker("no concrete file change was recorded for a change-oriented task")
}

func formalProofAssessmentGap(userPrompt string, history []orchestrationTurn, changes workspaceChangeReport) (string, bool) {
	if !looksLikeFormalProofTask(userPrompt) {
		return "", false
	}
	if reason, ok := acceptanceFailureSignal(userPrompt, history); ok {
		return reason, true
	}
	if looksLikeCoqUploadProofBenchmark(userPrompt) {
		return coqUploadProofAssessmentGap(history, changes)
	}
	if !requiresStrictFormalProofAssessment(userPrompt) {
		evidence := collectProofAssessmentEvidence(history, changes)
		if evidence.fuelShortcut && !evidence.fuelJustified {
			return "formal proof assessment failed: bounded/fuel wrapper is present without explicit equivalence, decrease, and sufficiency evidence", true
		}
		return "", false
	}
	return genericFormalProofAssessmentGap(userPrompt, history)
}

func requiresStrictFormalProofAssessment(userPrompt string) bool {
	lower := strings.ToLower(userPrompt)
	return containsAny(lower, []string{
		"不能用", "不使用", "不要用", "无占位", "占位符", "补全缺失的证明", "完整证明", "正式 proof", "正式证明",
		"no placeholder", "without placeholder", "without any placeholder", "no admitted", "without admitted",
		"no axiom", "without axiom", "no sorry", "without sorry", "complete proof",
	})
}

func coqUploadProofAssessmentGap(history []orchestrationTurn, changes workspaceChangeReport) (string, bool) {
	evidence := collectProofAssessmentEvidence(history, changes)
	var missing []string
	if !evidence.coqInputs {
		missing = append(missing, "uploaded Model.thy/Termination.thy/ROOT were not accounted for")
	}
	if !evidence.projectPath {
		missing = append(missing, "new Coq project folder under the requested cwd is not evidenced")
	}
	if !evidence.coqBuild {
		missing = append(missing, "Coq build evidence is missing")
	}
	if !evidence.placeholderScan {
		missing = append(missing, "source placeholder scan evidence is missing")
	}
	if !evidence.assumptionAudit {
		missing = append(missing, "Coq Print Assumptions/global-context audit evidence is missing")
	}
	if !evidence.originalObligation {
		missing = append(missing, "original termination/modify_lin obligation audit evidence is missing")
	}
	if evidence.fuelShortcut && !evidence.fuelJustified {
		return "formal proof assessment failed: modify_lin_fuel/default_fuel or fuel shortcut is present without explicit equivalence, decrease, and fuel-sufficiency evidence", true
	}
	if len(missing) > 0 {
		return "formal proof assessment incomplete: " + strings.Join(missing, "; "), true
	}
	return "", false
}

func genericFormalProofAssessmentGap(userPrompt string, history []orchestrationTurn) (string, bool) {
	evidence := collectProofAssessmentEvidence(history, workspaceChangeReport{})
	var missing []string
	lowerPrompt := strings.ToLower(userPrompt)
	if !evidence.proofBuild {
		missing = append(missing, "proof-assistant build evidence is missing")
	}
	if containsAny(lowerPrompt, []string{"占位符", "placeholder", "sorry", "admitted", "admit", "axiom", "quick_and_dirty"}) && !evidence.placeholderScan {
		missing = append(missing, "placeholder/assumption scan evidence is missing")
	}
	if containsAny(lowerPrompt, []string{"coq", ".v"}) && !evidence.assumptionAudit {
		missing = append(missing, "Coq Print Assumptions/global-context audit evidence is missing")
	}
	if containsAny(lowerPrompt, []string{"lean", ".lean"}) && !evidence.assumptionAudit {
		missing = append(missing, "Lean #print axioms audit evidence is missing")
	}
	if containsAny(lowerPrompt, []string{"isabelle", ".thy"}) && !evidence.assumptionAudit && containsAny(lowerPrompt, []string{"oracle", "quick_and_dirty", "sorry", "占位符", "placeholder"}) {
		missing = append(missing, "Isabelle thm_oracles or placeholder audit evidence is missing")
	}
	if evidence.fuelShortcut && !evidence.fuelJustified {
		return "formal proof assessment failed: bounded/fuel wrapper is present without explicit equivalence, decrease, and sufficiency evidence", true
	}
	if len(missing) > 0 {
		return "formal proof assessment incomplete: " + strings.Join(missing, "; "), true
	}
	return "", false
}

type proofAssessmentEvidence struct {
	coqInputs          bool
	projectPath        bool
	proofBuild         bool
	coqBuild           bool
	placeholderScan    bool
	assumptionAudit    bool
	originalObligation bool
	fuelShortcut       bool
	fuelJustified      bool
}

func collectProofAssessmentEvidence(history []orchestrationTurn, changes workspaceChangeReport) proofAssessmentEvidence {
	text := strings.ToLower(proofAssessmentText(history))
	commandText, outputText := proofAssessmentCommandText(history)
	commandLower := strings.ToLower(commandText)
	outputLower := strings.ToLower(outputText)
	combined := strings.Join([]string{text, commandLower, outputLower, strings.ToLower(strings.Join(changes.Changed, "\n"))}, "\n")
	return proofAssessmentEvidence{
		coqInputs:          containsAll(combined, []string{"model.thy", "termination.thy", "root"}),
		projectPath:        proofProjectPathEvidence(combined, changes),
		proofBuild:         proofBuildEvidence(combined),
		coqBuild:           coqBuildEvidence(combined),
		placeholderScan:    placeholderScanEvidence(commandLower, outputLower, text),
		assumptionAudit:    proofAssumptionAuditEvidence(combined),
		originalObligation: originalProofObligationEvidence(combined),
		fuelShortcut:       fuelShortcutEvidence(outputLower, text),
		fuelJustified:      fuelJustificationEvidence(combined),
	}
}

func proofAssessmentText(history []orchestrationTurn) string {
	var b strings.Builder
	for _, item := range history {
		b.WriteString(item.Content)
		b.WriteByte('\n')
		b.WriteString(item.Handoff)
		b.WriteByte('\n')
		b.WriteString(item.HandoffFields.Changed)
		b.WriteByte('\n')
		b.WriteString(item.HandoffFields.Verified)
		b.WriteByte('\n')
		b.WriteString(item.HandoffFields.Next)
		b.WriteByte('\n')
		b.WriteString(item.HandoffFields.Risks)
		b.WriteByte('\n')
	}
	return b.String()
}

func proofAssessmentCommandText(history []orchestrationTurn) (string, string) {
	var commands strings.Builder
	var outputs strings.Builder
	for _, command := range commandStates(history) {
		commands.WriteString(command.Command)
		commands.WriteByte('\n')
		outputs.WriteString(command.Output)
		outputs.WriteByte('\n')
	}
	return commands.String(), outputs.String()
}

func proofProjectPathEvidence(text string, changes workspaceChangeReport) bool {
	if len(changes.Changed) > 0 {
		return true
	}
	return containsAny(text, []string{
		"new coq project", "coq project", "新建文件夹", "新建 coq", "项目目录", "project=", "/root/tencent/coq-", "coq-lin-lattice",
	})
}

func proofBuildEvidence(text string) bool {
	return containsAny(text, []string{
		"make", "coqc", "coq_makefile", "dune build", "lake build", "lean --make", "isabelle build",
		"build completed", "compiled", "构建通过", "编译通过",
	})
}

func coqBuildEvidence(text string) bool {
	return containsAny(text, []string{
		"coq build", "coqc", "coq_makefile", "make", "rocq make", "rocq compile", "构建通过", "编译通过",
	})
}

func placeholderScanEvidence(commandText, outputText, proseText string) bool {
	if containsAny(proseText, []string{
		"no placeholders", "no forbidden tokens", "没有占位符", "未发现占位符", "无占位符",
		"未发现 admitted", "未发现 axiom", "未发现 sorry", "no admitted", "no axiom", "no sorry",
		"source-only placeholder scan",
	}) {
		return true
	}
	if containsAny(commandText, []string{"rg ", "grep ", "ripgrep"}) &&
		containsAny(commandText, forbiddenProofShortcutSignals()) &&
		!placeholderScanFoundForbiddenOutput(outputText) {
		return true
	}
	return false
}

func placeholderScanFoundForbiddenOutput(outputText string) bool {
	if strings.TrimSpace(outputText) == "" {
		return false
	}
	lines := strings.Split(outputText, "\n")
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" {
			continue
		}
		if strings.Contains(lower, "no matches") || strings.Contains(lower, "no output") || strings.Contains(lower, "not found") {
			continue
		}
		if containsAny(lower, forbiddenProofShortcutSignals()) {
			return true
		}
	}
	return false
}

func forbiddenProofShortcutSignals() []string {
	return []string{
		"admitted", "admit", "axiom", "parameter", "conjecture", "abort", "sorry", "todo", "placeholder",
		"quick_and_dirty", "guard checking", "guardchecking", "bypass_check", "bypass check",
	}
}

func proofAssumptionAuditEvidence(text string) bool {
	for _, line := range strings.Split(text, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" || lineNegatesAssumptionAudit(lower) {
			continue
		}
		if containsAny(lower, []string{
			"print assumptions", "closed under the global context", "closed under global context",
			"#print axioms", "no axioms", "no unexpected axioms", "thm_oracles", "no oracle", "no oracles",
			"无额外公理", "没有额外公理", "无 oracle", "无 oracles",
		}) {
			return true
		}
	}
	return false
}

func lineNegatesAssumptionAudit(lower string) bool {
	return containsAny(lower, []string{
		"没有执行 print assumptions", "未执行 print assumptions", "未运行 print assumptions",
		"缺少 print assumptions", "没有 print assumptions 审计", "缺少 coq print assumptions",
		"print assumptions missing", "missing print assumptions", "without print assumptions",
		"did not run print assumptions", "not run print assumptions", "print assumptions not run",
		"not executed print assumptions", "print assumptions was not executed",
		"缺少 global context", "global-context audit evidence is missing", "global context audit evidence is missing",
		"assumption audit evidence is missing", "missing assumption audit", "without assumption audit",
	})
}

func originalProofObligationEvidence(text string) bool {
	if containsAny(text, []string{
		"original proof obligation", "original recursive semantics", "original semantics",
		"termination modify_lin", "modify_lin termination", "well-founded", "well founded",
		"decrease", "decreases", "measure", "distance", "structural recursion",
		"原始证明义务", "原始递归语义", "终止性", "下降", "良基", "度量", "等价",
	}) {
		return true
	}
	return false
}

func fuelShortcutEvidence(outputText, proseText string) bool {
	combined := strings.Join([]string{outputText, proseText}, "\n")
	var affirmative []string
	for _, line := range strings.Split(combined, "\n") {
		lower := strings.ToLower(strings.TrimSpace(line))
		if lower == "" || lineNegatesFuelShortcut(lower) {
			continue
		}
		affirmative = append(affirmative, lower)
	}
	combined = strings.Join(affirmative, "\n")
	return containsAny(combined, []string{"modify_lin_fuel", "default_fuel", "bounded fuel", "fuel wrapper", "固定 fuel", "燃料包装"})
}

func fuelJustificationEvidence(text string) bool {
	return containsAny(text, []string{"equivalence", "fuel sufficiency", "sufficient fuel", "decrease", "well-founded", "well founded", "等价", "燃料足够", "足够模拟", "下降", "良基"}) &&
		!containsAny(text, []string{"without equivalence", "lacks equivalence", "缺少等价", "没有证明", "未证明", "not proved", "not prove"})
}

func containsAll(value string, signals []string) bool {
	value = strings.ToLower(value)
	for _, signal := range signals {
		if !strings.Contains(value, strings.ToLower(signal)) {
			return false
		}
	}
	return true
}

func acceptanceFailureSignal(userPrompt string, history []orchestrationTurn) (string, bool) {
	if len(history) == 0 {
		return "", false
	}
	if reason := acceptanceFailureInTurn(userPrompt, history[len(history)-1]); reason != "" {
		return reason, true
	}
	return "", false
}

func acceptanceFailureInTurn(userPrompt string, item orchestrationTurn) string {
	prose := strings.Join([]string{
		item.Content,
		item.Handoff,
		item.HandoffFields.Next,
		item.HandoffFields.Risks,
	}, "\n")
	context, score := acceptanceFailureContextScore(prose, userPrompt)
	var commandText strings.Builder
	for _, command := range commandStates([]orchestrationTurn{item}) {
		output := strings.TrimSpace(command.Output)
		if output == "" {
			continue
		}
		commandText.WriteString("\n")
		commandText.WriteString(output)
	}
	if commandContext, commandScore := acceptanceFailureContextScore(commandText.String(), userPrompt); commandScore > score {
		context, score = commandContext, commandScore
	}
	if context != "" {
		return "acceptance check failed: " + trimForPrompt(oneLine(context), 500)
	}
	return ""
}

func acceptanceFailureContext(text, userPrompt string) string {
	context, _ := acceptanceFailureContextScore(text, userPrompt)
	return context
}

func acceptanceFailureContextScore(text, userPrompt string) (string, int) {
	text = strings.TrimSpace(text)
	lower := strings.ToLower(text)
	if lower == "" {
		return "", 0
	}
	if context := fuelTerminationGapContext(text); context != "" {
		return context, 100
	}
	for _, signal := range acceptanceFailureSignals {
		if strings.Contains(lower, strings.ToLower(signal)) {
			return signalContext(text, signal), acceptanceFailureSignalScore(signal)
		}
	}
	if hasUnresolvedAcceptanceSignal(text, userPrompt) {
		return unresolvedAcceptanceContext(text, userPrompt), 80
	}
	return "", 0
}

var acceptanceFailureSignals = []string{
	"acceptance check failed",
	"acceptance criterion failed",
	"acceptance criterion is not satisfied",
	"user request is not satisfied",
	"does not satisfy the user request",
	"cannot be considered complete",
	"cannot be marked complete",
	"must not be treated as complete",
	"should not be marked complete",
	"验收失败",
	"验收标准未满足",
	"没有满足用户要求",
	"未满足用户要求",
	"不能把当前状态视为完成",
	"不能视为完成",
	"不能算完成",
	"不能判为完成",
	"不能判定为完成",
	"不应标记完成",
	"不应该标记完成",
	"不能把验收标为 resolved",
	"不能标为 resolved",
	"不能把验收标为完成",
	"没有实质进展",
}

func acceptanceFailureSignalScore(signal string) int {
	lower := strings.ToLower(signal)
	if strings.Contains(lower, "acceptance criterion is not satisfied") ||
		strings.Contains(lower, "acceptance criterion failed") {
		return 90
	}
	if strings.Contains(lower, "验收标准未满足") ||
		strings.Contains(lower, "没有满足用户要求") ||
		strings.Contains(lower, "未满足用户要求") {
		return 70
	}
	if strings.Contains(lower, "不能") || strings.Contains(lower, "resolved") {
		return 65
	}
	return 60
}

func acceptanceBlockerSummary(userPrompt string, history []orchestrationTurn) string {
	for i := len(history) - 1; i >= 0; i-- {
		if reason := acceptanceFailureInTurn(userPrompt, history[i]); reason != "" {
			return reason
		}
	}
	return ""
}

func hasUnresolvedAcceptanceSignal(text, userPrompt string) bool {
	lower := strings.ToLower(text)
	if lower == "" {
		return false
	}
	if hasResolvedSorrySignal(lower) && !hasExplicitUnresolvedSorryRisk(lower) {
		return false
	}
	promptLower := strings.ToLower(userPrompt)
	promptRequiresSorryRemoval := containsAny(promptLower, []string{
		"消除", "去掉", "移除", "删除", "补全", "填上", "主定理", "完全证明", "完整证明",
		"remove", "eliminate", "fill", "replace", "complete proof", "main theorem",
	}) && containsAny(promptLower, []string{"sorry", "quick_and_dirty", "termination modify_lin", "modify_lin", "主定理"})
	if containsAny(lower, []string{
		"main theorem", "主定理", "termination modify_lin", "modify_lin",
	}) && containsAny(lower, []string{
		"sorry", "未消除", "没有消除", "还保留", "placeholder", "占位",
	}) {
		if lineNegatesFuelShortcut(lower) || containsAny(lower, []string{"no placeholders", "no forbidden tokens", "没有占位符", "无占位符", "source-only placeholder scan"}) {
			return false
		}
		return true
	}
	if containsAny(lower, []string{
		"只是通过编译",
		"只能说通过编译",
		"没有实质上的进展",
		"not a completed proof",
	}) {
		return true
	}
	if promptRequiresSorryRemoval && hasExplicitUnresolvedSorryRisk(lower) {
		return true
	}
	return false
}

func hasExplicitUnresolvedSorryRisk(lower string) bool {
	return containsAny(lower, []string{
		"sorry placeholder", "sorry placeholders", "still contains sorry", "contains sorry",
		"quick_and_dirty", "可编译的证明框架", "证明框架可编译", "不是完整证明",
		"不是完全无 sorry", "not without sorry", "not fully without sorry", "not a completed proof",
	})
}

func hasResolvedSorrySignal(lower string) bool {
	if containsAny(lower, []string{
		"without sorry", "without any sorry", "no sorry placeholders", "no remaining sorry",
		"无 sorry", "无sorry", "没有 sorry", "without quick_and_dirty", "quick_and_dirty = false",
	}) {
		return true
	}
	return regexp.MustCompile(`\bno\s+sorry\b`).MatchString(lower)
}

func unresolvedAcceptanceContext(text, userPrompt string) string {
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if hasUnresolvedAcceptanceSignal(line, userPrompt) {
			return strings.TrimSpace(line)
		}
	}
	if context := fuelTerminationGapContext(text); context != "" {
		return context
	}
	for _, line := range lines {
		if lineLooksLikeAcceptanceExplanation(line) {
			return strings.TrimSpace(line)
		}
	}
	return "unresolved acceptance criterion remains"
}

func fuelTerminationGapContext(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		lower := strings.ToLower(line)
		if !containsAny(lower, []string{"modify_lin", "default_fuel", "fuel", "燃料", "termination", "distance"}) {
			continue
		}
		if lineNegatesFuelShortcut(lower) {
			continue
		}
		if containsAny(lower, []string{
			"没有证明", "未证明", "没有证", "下降", "等价", "足够模拟", "固定", "绕过",
			"not prove", "not proved", "without proving", "equivalence", "decrease", "sufficient",
		}) {
			return fuelTerminationContextLines(lines, i)
		}
	}
	return ""
}

func fuelTerminationContextLines(lines []string, index int) string {
	selected := []string{strings.TrimSpace(lines[index])}
	if index+1 < len(lines) {
		next := strings.TrimSpace(lines[index+1])
		if lineLooksLikeFuelTerminationDetail(next) {
			selected = append(selected, next)
		}
	}
	if len(selected) == 1 && index > 0 {
		prev := strings.TrimSpace(lines[index-1])
		if lineLooksLikeFuelTerminationDetail(prev) {
			selected = append([]string{prev}, selected...)
		}
	}
	return strings.TrimSpace(strings.Join(selected, " "))
}

func lineLooksLikeFuelTerminationDetail(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	if lineNegatesFuelShortcut(lower) {
		return false
	}
	return containsAny(lower, []string{"modify_lin", "default_fuel", "fuel", "燃料", "termination", "distance", "递归", "证明"}) &&
		containsAny(lower, []string{"没有证明", "未证明", "没有证", "下降", "等价", "足够模拟", "固定", "绕过", "not prove", "not proved", "without proving", "equivalence", "decrease", "sufficient"})
}

func lineNegatesFuelShortcut(lower string) bool {
	return containsAny(lower, []string{
		"没有 modify_lin_fuel", "没有 default_fuel", "没有 fuel wrapper", "没有 bounded/default fuel",
		"没有 modify_lin_fuel/default_fuel", "没有 modify_lin_fuel/default_fuel/fuel",
		"无 modify_lin_fuel", "无 default_fuel", "无 fuel wrapper",
		"no modify_lin_fuel", "no default_fuel", "no fuel wrapper", "without modify_lin_fuel", "without default_fuel",
		"without fuel wrapper", "no bounded/default fuel", "没有 runtime distance guard",
	})
}

func lineLooksLikeAcceptanceExplanation(line string) bool {
	lower := strings.ToLower(strings.TrimSpace(line))
	if lower == "" {
		return false
	}
	return containsAny(lower, []string{"验收", "acceptance", "modify_lin", "termination", "证明", "proof", "distance", "fuel", "燃料"}) &&
		containsAny(lower, []string{"不能", "未", "没有", "not", "failed", "incomplete", "风险", "risk", "缺失", "missing"})
}

func containsAny(value string, signals []string) bool {
	for _, signal := range signals {
		if strings.Contains(value, strings.ToLower(signal)) {
			return true
		}
	}
	return false
}

func signalContext(text, signal string) string {
	lines := strings.Split(text, "\n")
	signal = strings.ToLower(signal)
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), signal) {
			return strings.TrimSpace(line)
		}
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return signal
	}
	return text
}

func turnReportsBlocking(item orchestrationTurn) bool {
	status := strings.ToLower(strings.TrimSpace(item.HandoffFields.Status))
	if status == "blocked" {
		return true
	}
	if item.Err != "" && compactBlockerSummary(item) != "" {
		return true
	}
	if failedCommandCount([]orchestrationTurn{item}) > 0 && meaningfulHandoffValue(item.HandoffFields.Risks) {
		return true
	}
	return false
}

func normalizeBlockerKey(item orchestrationTurn) string {
	values := []string{
		item.HandoffFields.Next,
		item.HandoffFields.Risks,
		item.Err,
	}
	var parts []string
	for _, value := range values {
		value = strings.ToLower(oneLine(value))
		value = normalizeBlockerText(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		for _, command := range failedCommandSummaries([]orchestrationTurn{item}, 1) {
			parts = append(parts, normalizeBlockerText(strings.ToLower(command)))
		}
	}
	return strings.Join(parts, "|")
}

func normalizeBlockerText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	var b strings.Builder
	lastSpace := false
	for _, r := range value {
		keep := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '/' || r == '_' || r == '-' || (r >= '\u4e00' && r <= '\u9fff')
		if keep {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

func extractHandoff(content string) string {
	return extractTrailingLine(content, "handoff:")
}

func extractMsg(content string) string {
	return extractTrailingLine(content, "msg:")
}

func parseHandoffFields(line string) orchestrationHandoffFields {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "Handoff:")
	line = strings.TrimPrefix(line, "handoff:")
	out := orchestrationHandoffFields{}
	for _, part := range strings.Split(line, ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "status":
			out.Status = value
		case "changed":
			out.Changed = value
		case "verified":
			out.Verified = value
		case "next":
			out.Next = value
		case "risks":
			out.Risks = value
		}
	}
	return out
}

func extractTrailingLine(content, prefix string) string {
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return line
		}
	}
	return ""
}

func claudeAssistantText(msg map[string]any) string {
	message, _ := msg["message"].(map[string]any)
	if message == nil {
		return ""
	}
	parts, _ := message["content"].([]any)
	var b strings.Builder
	for _, part := range parts {
		block, _ := part.(map[string]any)
		if block == nil {
			continue
		}
		if firstString(block, "type") == "text" {
			b.WriteString(firstString(block, "text"))
		}
	}
	return b.String()
}

func claudeToolEvents(msg map[string]any) []*RunnerToolEvent {
	message, _ := msg["message"].(map[string]any)
	if message == nil {
		return nil
	}
	parts, _ := message["content"].([]any)
	if len(parts) == 0 {
		return nil
	}
	events := make([]*RunnerToolEvent, 0, len(parts))
	for _, part := range parts {
		block, _ := part.(map[string]any)
		if block == nil {
			continue
		}
		switch firstString(block, "type") {
		case "tool_use":
			tool := claudeToolUseEvent(block)
			if tool != nil {
				events = append(events, tool)
			}
		case "tool_result":
			tool := claudeToolResultEvent(block)
			if tool != nil {
				events = append(events, tool)
			}
		}
	}
	return events
}

func claudeToolUseEvent(block map[string]any) *RunnerToolEvent {
	name := firstString(block, "name")
	id := firstString(block, "id")
	input, _ := block["input"].(map[string]any)
	command := claudeToolCommand(name, input)
	if command == "" {
		command = name
	}
	if command == "" && id == "" {
		return nil
	}
	return &RunnerToolEvent{ID: id, Status: "in_progress", Command: command}
}

func claudeToolResultEvent(block map[string]any) *RunnerToolEvent {
	id := firstString(block, "tool_use_id", "id")
	status := "completed"
	if isErr, _ := block["is_error"].(bool); isErr {
		status = "failed"
	}
	output := claudeToolResultContent(block["content"])
	if output == "" && id == "" {
		return nil
	}
	return &RunnerToolEvent{ID: id, Status: status, Output: output}
}

func claudeToolCommand(name string, input map[string]any) string {
	if input == nil {
		return name
	}
	switch name {
	case "Bash":
		if command := firstString(input, "command"); command != "" {
			return command
		}
	case "Read":
		if path := firstString(input, "file_path", "path"); path != "" {
			return "Read " + path
		}
	case "Write":
		if path := firstString(input, "file_path", "path"); path != "" {
			return "Write " + path
		}
	case "Edit", "MultiEdit":
		if path := firstString(input, "file_path", "path"); path != "" {
			return name + " " + path
		}
	case "Glob":
		if pattern := firstString(input, "pattern"); pattern != "" {
			return "Glob " + pattern
		}
	case "Grep":
		if pattern := firstString(input, "pattern"); pattern != "" {
			return "Grep " + pattern
		}
	}
	if description := firstString(input, "description"); description != "" {
		return name + ": " + description
	}
	return name
}

func claudeToolResultContent(value any) string {
	switch content := value.(type) {
	case string:
		return content
	case []any:
		var b strings.Builder
		for _, item := range content {
			switch part := item.(type) {
			case string:
				b.WriteString(part)
			case map[string]any:
				if text := firstString(part, "text", "content"); text != "" {
					b.WriteString(text)
				}
			}
		}
		return b.String()
	default:
		return ""
	}
}

func PrepareOrchestrationPromptFiles(cfg *config.Config, runID, prompt string, files []protocol.AttachmentPayload) (string, []store.OrchestrationFile, error) {
	if len(files) == 0 {
		return strings.TrimSpace(prompt), nil, nil
	}
	if len(files) > 12 {
		return "", nil, errors.New("at most 12 files can be uploaded")
	}
	baseDir := cfg.Bridge.CWD
	if baseDir == "" {
		baseDir = "."
	}
	uploadDir := filepath.Join(expandHome(baseDir), ".codex-bridge", "orchestrations", safeFileName(runID))
	if err := os.MkdirAll(uploadDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("create orchestration upload directory: %w", err)
	}
	maxBytes := cfg.Hub.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = 8 * 1024 * 1024
	}

	var metas []store.OrchestrationFile
	var paths []string
	for i, file := range files {
		if file.Size <= 0 || file.Size > maxBytes {
			return "", nil, fmt.Errorf("file %q is too large", file.Name)
		}
		raw, err := base64.StdEncoding.DecodeString(file.Data)
		if err != nil {
			return "", nil, fmt.Errorf("decode file %q: %w", file.Name, err)
		}
		if int64(len(raw)) > maxBytes {
			return "", nil, fmt.Errorf("file %q is too large", file.Name)
		}
		name := safeOrchestrationUploadName(file.Name)
		if name == "" {
			name = fmt.Sprintf("upload-%02d.bin", i+1)
		}
		path := filepath.Join(uploadDir, fmt.Sprintf("%s-%s", attachmentID(i), name))
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return "", nil, fmt.Errorf("write file %q: %w", file.Name, err)
		}
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
		paths = append(paths, path)
		metas = append(metas, store.OrchestrationFile{Name: file.Name, MimeType: file.MimeType, Size: int64(len(raw))})
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n\nUploaded files for this orchestration run:\n")
	for _, path := range paths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteByte('\n')
	}
	b.WriteString("\nUse these local file paths directly when the task refers to uploaded files.")
	b.WriteString("\nFor uploaded source/text files such as .thy, ROOT, .go, .md, and .txt, inspect them with shell commands like sed -n '1,220p' <path>, cat <path>, or wc -l <path>; do not use Claude's Read tool for these uploaded text/source files.")
	b.WriteString("\nDo not send an empty pages field to any file-reading tool. Only include pages when a non-empty page range is required for a real PDF/document; otherwise omit the page filter.")
	return b.String(), metas, nil
}

func safeOrchestrationUploadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = safeOrchestrationFileName.ReplaceAllString(name, "-")
	return strings.Trim(name, ".-")
}
