package bridge

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	bridgeprofiles "github.com/tencent/codex-bridge/internal/bridge/profiles"
	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type OrchestrationManager struct {
	cfg         *config.Config
	mu          sync.Mutex
	runs        map[string]*orchestrationRunHandle
	sessions    map[string]*orchestrationNativeSession
	output      chan<- protocol.Envelope
	pending     []protocol.Envelope
	approvals   map[string]orchestrationApproval
	conclusions map[string]bool
}

type orchestrationApproval struct {
	runID string
	ch    chan protocol.ApprovalResponsePayload
}

type orchestrationRunHandle struct {
	cancel context.CancelFunc
}

type orchestrationSessionState struct {
	CodexThreadID        string
	ClaudeSessionID      string
	ClaudeSessionStarted bool
	CodexResumeMode      string
	ClaudeResumeMode     string
	NativeSession        *orchestrationNativeSession
	CommandFingerprints  map[string]bridgeprofiles.CommandFingerprint
}

type orchestrationNativeSession struct {
	runID                   string
	cwd                     string
	nativeContextCompaction string
	mu                      sync.Mutex
	codex                   *orchestrationCodexSession
	claude                  *orchestrationClaudeSession
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

const (
	orchestrationTurnContinuationMaxAttempts = 3
	orchestrationTurnContinuationIdleWait    = 200 * time.Millisecond
)

func NewOrchestrationManager(cfg *config.Config) *OrchestrationManager {
	return &OrchestrationManager{
		cfg:         cfg,
		runs:        make(map[string]*orchestrationRunHandle),
		sessions:    make(map[string]*orchestrationNativeSession),
		approvals:   make(map[string]orchestrationApproval),
		conclusions: make(map[string]bool),
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
	var oldSession *orchestrationNativeSession
	if old := m.runs[payload.RunID]; old != nil {
		old.cancel()
		oldSession = m.sessions[payload.RunID]
		delete(m.sessions, payload.RunID)
	}
	handle := &orchestrationRunHandle{cancel: cancel}
	m.runs[payload.RunID] = handle
	delete(m.conclusions, payload.RunID)
	m.mu.Unlock()
	if oldSession != nil {
		oldSession.close()
	}

	go func() {
		defer func() {
			cancel()
			m.mu.Lock()
			current := m.runs[payload.RunID]
			if m.runs[payload.RunID] == handle {
				delete(m.runs, payload.RunID)
			}
			m.mu.Unlock()
			if current == handle {
				m.cancelApprovals(payload.RunID)
			}
		}()
		m.run(ctx, payload)
	}()
}

func (m *OrchestrationManager) Cancel(runID string) {
	m.mu.Lock()
	handle := m.runs[runID]
	m.mu.Unlock()
	if handle != nil {
		handle.cancel()
	}
	m.closeNativeSession(runID)
	m.cancelApprovals(runID)
}

func (m *OrchestrationManager) CloseAll() {
	m.mu.Lock()
	var cancels []context.CancelFunc
	runIDs := make([]string, 0, len(m.runs))
	for runID, handle := range m.runs {
		if handle != nil {
			cancels = append(cancels, handle.cancel)
		}
		runIDs = append(runIDs, runID)
		delete(m.runs, runID)
	}
	var sessions []*orchestrationNativeSession
	for runID, session := range m.sessions {
		sessions = append(sessions, session)
		delete(m.sessions, runID)
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	for _, session := range sessions {
		session.close()
	}
	for _, runID := range runIDs {
		m.cancelApprovals(runID)
	}
}

func (m *OrchestrationManager) nativeSession(runID, cwd string) *orchestrationNativeSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sessions == nil {
		m.sessions = make(map[string]*orchestrationNativeSession)
	}
	session := m.sessions[runID]
	if session == nil {
		session = &orchestrationNativeSession{runID: runID, cwd: cwd}
		m.sessions[runID] = session
	} else if cwd != "" {
		session.cwd = cwd
	}
	return session
}

func (m *OrchestrationManager) closeNativeSession(runID string) {
	m.mu.Lock()
	session := m.sessions[runID]
	delete(m.sessions, runID)
	m.mu.Unlock()
	if session != nil {
		session.close()
	}
}

func (s *orchestrationNativeSession) close() {
	s.mu.Lock()
	codex := s.codex
	claude := s.claude
	s.codex = nil
	s.claude = nil
	s.mu.Unlock()
	if codex != nil && codex.client != nil {
		codex.client.unsubscribeThreadWithTimeout(codex.threadID)
		codex.client.close()
	}
	if claude != nil {
		_ = claude.stdin.Close()
		if claude.cmd != nil && claude.cmd.Process != nil {
			_ = terminateProcessGroup(claude.cmd.Process.Pid)
		}
		if claude.cmd != nil {
			_ = claude.cmd.Wait()
		}
		if claude.release != nil {
			claude.release()
		}
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
	runCWD := m.cwd(payload)
	preparedPrompt, _, err := PrepareOrchestrationPromptFiles(m.cfg, runCWD, payload.RunID, payload.Prompt, payload.Files)
	if err != nil {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:   "run.error",
			Status: store.OrchestrationFailed,
			Error:  err.Error(),
		})
		return
	}
	payload.Prompt = preparedPrompt
	mode := payload.Mode
	if mode != "collaboration" && mode != "debate" {
		mode = "collaboration"
	}
	firstCLI := normalizeRelayFirstCLI(payload.FirstCLI)
	maxTurns := payload.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 2
	}
	maxTurnsRequested := payload.MaxTurnsRequested
	if maxTurnsRequested <= 0 {
		maxTurnsRequested = maxTurns
	}
	if maxTurns > 12 {
		maxTurns = 12
	}
	profile := normalizeOrchestrationProfile(payload.Profile)
	nativeContextCompaction := protocol.NormalizeNativeContextCompaction(payload.NativeContextCompaction)
	nativeSession := m.nativeSession(payload.RunID, runCWD)
	nativeSession.mu.Lock()
	nativeSession.nativeContextCompaction = nativeContextCompaction
	nativeSession.mu.Unlock()
	sessionState := orchestrationSessionState{
		ClaudeSessionID:     stableOrchestrationSessionID(payload.RunID, "claude"),
		NativeSession:       nativeSession,
		CommandFingerprints: map[string]bridgeprofiles.CommandFingerprint{},
	}
	if payload.Resume {
		sessionState.CodexThreadID = payload.CodexThreadID
		sessionState.ClaudeSessionStarted = payload.ClaudeStarted
	}
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "run.start",
		Status:  store.OrchestrationRunning,
		Content: fmt.Sprintf("Starting relay orchestration with %d CLI turns.", maxTurns),
		RunStartData: &protocol.RunStartData{
			CWD:                     runCWD,
			Mode:                    mode,
			FirstCLI:                firstCLI,
			MaxTurnsRequested:       maxTurnsRequested,
			MaxTurnsApplied:         maxTurns,
			PromptSeq:               payload.PromptSeq,
			Profile:                 profile,
			NativeContextCompaction: nativeContextCompaction,
		},
		Data: map[string]any{
			"cwd":                     runCWD,
			"mode":                    mode,
			"firstCli":                firstCLI,
			"maxTurns":                maxTurns,
			"maxTurnsRequested":       maxTurnsRequested,
			"maxTurnsApplied":         maxTurns,
			"promptSeq":               payload.PromptSeq,
			"profile":                 profile,
			"nativeContextCompaction": nativeContextCompaction,
		},
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
		role, cli := roleForTurnWithFirstCLI(mode, firstCLI, turn)
		turnID := fmt.Sprintf("%s-%02d", payload.RunID, turn)
		if payload.PromptSeq > 0 {
			turnID = fmt.Sprintf("%s-p%03d-%02d", payload.RunID, payload.PromptSeq, turn)
		}
		clearRelayResumeMode(cli, &sessionState)
		prompt := composeRelayPromptWithFirstCLI(mode, firstCLI, profile, payload.Prompt, payload.Context, payload.Resume, role, cli, turn, maxTurns, history)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.start",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: orchestrationTurnStartContent(cli, &sessionState, turn, maxTurns, role),
			TurnStartData: &protocol.TurnStartData{
				CLI:        cli,
				Turn:       turn,
				MaxTurns:   maxTurns,
				PromptText: prompt,
				Profile:    profile,
				ResumeMode: plannedRelayResumeMode(cli, sessionState),
			},
			Data: map[string]any{
				"cwd":        m.cwd(payload),
				"cli":        cli,
				"turn":       turn,
				"maxTurns":   maxTurns,
				"promptText": prompt,
				"profile":    profile,
				"relayOnly":  true,
				"resumeMode": plannedRelayResumeMode(cli, sessionState),
			},
		})
		record, turnStatus, err := m.runRelayTurnWithContinuations(ctx, payload, turnID, role, cli, prompt, &sessionState, runCWD)
		if err != nil {
			record.Err = visibleCLIError(err)
			history = append(history, record)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:       "turn.end",
				TurnID:     turnID,
				Role:       role,
				CLI:        cli,
				Content:    relayTerminalContent([]orchestrationTurn{record}),
				Status:     "error",
				Error:      record.Err,
				RunEndData: m.relayRunEndData(cli, sessionState, runCWD),
				Data:       relayTurnEndData(cli, sessionState),
			})
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				m.emit(payload.RunID, protocol.OrchestrationEventPayload{
					Kind:          "run.cancelled",
					Status:        store.OrchestrationCanceled,
					Error:         "canceled",
					RunConclusion: runConclusionForStatus(store.OrchestrationCanceled, "canceled", history),
				})
				return
			}
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:          "run.error",
				Status:        store.OrchestrationFailed,
				CLI:           cli,
				Error:         record.Err,
				Content:       relayTerminalContent(history),
				RunConclusion: runConclusionForStatus(store.OrchestrationFailed, record.Err, history),
				Data: map[string]any{
					"relayOnly": true,
					"error":     record.Err,
				},
			})
			return
		}
		history = append(history, record)
		content := record.Content
		if strings.TrimSpace(content) == "" {
			content = relayTerminalContent([]orchestrationTurn{record})
		}
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:       "turn.end",
			TurnID:     turnID,
			Role:       role,
			CLI:        cli,
			Content:    content,
			Status:     turnStatus,
			RunEndData: m.relayRunEndData(cli, sessionState, runCWD),
			Data:       relayTurnEndData(cli, sessionState),
		})
		if turn < maxTurns && turnStatus == "success" {
			m.runPostTurnNativeMaintenance(ctx, payload.RunID, turnID, role, cli, &sessionState)
		}
	}
	finalContent := relayTerminalContent(history)
	finalRunEndData := runEndDataWithNativeResume(&protocol.RunEndData{
		CodexThreadID:      sessionState.CodexThreadID,
		ClaudeSessionID:    sessionState.ClaudeSessionID,
		CodexNativeResume:  codexNativeResumeInfo(sessionState.CodexThreadID, runCWD),
		ClaudeNativeResume: m.claudeNativeResumeInfo(sessionState.ClaudeSessionID, runCWD),
	}, runCWD)
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:          "run.end",
		Status:        store.OrchestrationCompleted,
		Content:       finalContent,
		RunEndData:    finalRunEndData,
		RunConclusion: runConclusionForStatus(store.OrchestrationCompleted, finalContent, history),
		Data: map[string]any{
			"relayOnly":          true,
			"codexThreadId":      sessionState.CodexThreadID,
			"claudeSessionId":    sessionState.ClaudeSessionID,
			"codexNativeResume":  finalRunEndData.CodexNativeResume,
			"claudeNativeResume": finalRunEndData.ClaudeNativeResume,
			"nativeResume":       finalRunEndData.NativeResume,
		},
	})
	m.runFinalNativeMaintenance(ctx, mode, firstCLI, maxTurns, &sessionState)
}

func (m *OrchestrationManager) runRelayTurnWithContinuations(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, cli, prompt string, state *orchestrationSessionState, runCWD string) (orchestrationTurn, string, error) {
	var combined orchestrationTurn
	status := "success"
	nextPrompt := prompt
	var lastErr error
	for attempt := 0; attempt <= orchestrationTurnContinuationMaxAttempts; attempt++ {
		if attempt > 0 {
			if err := waitOrchestrationTurnContinuationIdle(ctx); err != nil {
				return combined, status, err
			}
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "info",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Content:  fmt.Sprintf("CLI did not return a final conclusion or handoff summary; Bridge is continuing this same turn (%d/%d).", attempt, orchestrationTurnContinuationMaxAttempts),
				Data: map[string]any{
					"relayOnly": true,
					"category":  "turn-continuation-retry",
					"attempt":   attempt,
					"max":       orchestrationTurnContinuationMaxAttempts,
				},
			})
		}
		content, tools, err := m.runRelayCLI(ctx, payload, turnID, role, cli, nextPrompt, state)
		recordCommandFingerprints(state, runCWD, tools)
		record := newOrchestrationTurnRecord(turnID, role, cli, content, tools)
		if err != nil {
			record.Err = visibleCLIError(err)
		}
		combined = mergeOrchestrationTurnAttempts(combined, record)
		if err == nil && !orchestrationTurnNeedsContinuation(record, err) {
			return combined, status, nil
		}
		if recoverableRelayCLIError(cli, content, err) && orchestrationTurnHasFinalConclusion(record) {
			warning := visibleCLIError(err)
			m.resetCodexInteractiveSessionAfterRecoverableError(state)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "warning",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Content:  "Codex app-server reported an empty tail error after visible output; Bridge kept the visible reply and continued the orchestration.",
				Error:    warning,
				Data: map[string]any{
					"relayOnly":   true,
					"recoverable": true,
					"error":       warning,
					"category":    "codex-empty-tail-error-after-visible-output",
				},
			})
			return combined, status, nil
		}
		if !shouldContinueInterruptedRelayTurn(record, err) {
			return combined, status, err
		}
		lastErr = err
		if attempt >= orchestrationTurnContinuationMaxAttempts {
			if combined.Err == "" {
				combined.Err = visibleCLIError(err)
			}
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "warning",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Content:  fmt.Sprintf("CLI still did not return a final conclusion or handoff summary after %d continuation attempts; Bridge is preserving this turn's command events and moving to the next turn.", orchestrationTurnContinuationMaxAttempts),
				Error:    combined.Err,
				Data: map[string]any{
					"relayOnly": true,
					"category":  "turn-continuation-exhausted",
					"attempts":  orchestrationTurnContinuationMaxAttempts,
				},
			})
			return combined, "warning", nil
		}
		m.resetNativeInteractiveSessionForContinuation(cli, state)
		nextPrompt = composeInterruptedTurnContinuationPrompt(prompt, combined, attempt+1, orchestrationTurnContinuationMaxAttempts)
	}
	return combined, status, lastErr
}

func waitOrchestrationTurnContinuationIdle(ctx context.Context) error {
	timer := time.NewTimer(orchestrationTurnContinuationIdleWait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func mergeOrchestrationTurnAttempts(current, next orchestrationTurn) orchestrationTurn {
	if current.TurnID == "" {
		return next
	}
	if strings.TrimSpace(next.Content) != "" {
		current.Content = mergeOrchestrationTurnContent(current.Content, next.Content)
	}
	if strings.TrimSpace(next.Handoff) != "" {
		current.Handoff = next.Handoff
	}
	if strings.TrimSpace(next.Err) != "" {
		current.Err = next.Err
	}
	current.Tools = append(current.Tools, next.Tools...)
	return current
}

func mergeOrchestrationTurnContent(current, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if current == "" {
		return next
	}
	if next == "" {
		return current
	}
	if strings.HasPrefix(next, current) {
		return next
	}
	if strings.HasSuffix(current, next) {
		return current
	}
	return current + "\n\n" + next
}

func orchestrationTurnNeedsContinuation(record orchestrationTurn, err error) bool {
	if err != nil {
		return true
	}
	return !orchestrationTurnHasFinalConclusion(record)
}

func shouldContinueInterruptedRelayTurn(record orchestrationTurn, err error) bool {
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return false
	}
	if err == nil {
		return true
	}
	return strings.TrimSpace(record.Content) != "" || len(record.Tools) > 0
}

func (m *OrchestrationManager) resetNativeInteractiveSessionForContinuation(cli string, state *orchestrationSessionState) {
	if state == nil || state.NativeSession == nil {
		return
	}
	session := state.NativeSession
	session.mu.Lock()
	defer session.mu.Unlock()
	switch cli {
	case "codex":
		codex := session.codex
		if codex == nil {
			return
		}
		if codex.threadID != "" {
			state.CodexThreadID = codex.threadID
		}
		if codex.client != nil {
			codex.client.close()
		}
		session.codex = nil
	case "claude":
		claude := session.claude
		if claude == nil {
			return
		}
		_ = claude.stdin.Close()
		if claude.cmd != nil && claude.cmd.Process != nil {
			_ = terminateProcessGroup(claude.cmd.Process.Pid)
		}
		if claude.cmd != nil {
			_ = claude.cmd.Wait()
		}
		if claude.release != nil {
			claude.release()
		}
		session.claude = nil
	}
}

func composeInterruptedTurnContinuationPrompt(original string, record orchestrationTurn, attempt, max int) string {
	var b strings.Builder
	b.WriteString("Codex Bridge is continuing the same orchestration turn because the previous CLI invocation returned command events or partial visible output but no final conclusion or handoff summary. Do not treat this as a new user request, and do not discard completed work.\n\n")
	b.WriteString(orchestrationLanguageRule)
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Continuation attempt: %d of %d.\n\n", attempt, max))
	if strings.TrimSpace(record.Content) != "" {
		b.WriteString("Visible output already produced in this turn:\n")
		b.WriteString(trimForPrompt(record.Content, 3000))
		b.WriteString("\n\n")
	}
	if len(record.Tools) > 0 {
		b.WriteString("Command events already observed in this turn:\n")
		for _, line := range relayCommandSummaries(record.Tools, 8) {
			b.WriteString("- ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	if strings.TrimSpace(record.Err) != "" {
		b.WriteString("Last interruption detail:\n")
		b.WriteString(trimForPrompt(record.Err, 1200))
		b.WriteString("\n\n")
	}
	b.WriteString("Continue from the current state and finish this same turn with a concise final conclusion and handoff summary. If a command failed, explain how you handled it or what remains blocked instead of ending on the raw command event.\n\n")
	b.WriteString("Original turn prompt:\n")
	b.WriteString(trimForPrompt(original, 12000))
	return b.String()
}

func stableOrchestrationSessionID(runID, cli string) string {
	sum := sha1.Sum([]byte("codex-bridge/orchestration/" + runID + "/" + cli))
	raw := append([]byte(nil), sum[:16]...)
	raw[6] = (raw[6] & 0x0f) | 0x50
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw)
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func nativeSessionDisplayName(runID, cli string) string {
	cli = strings.TrimSpace(cli)
	if cli == "" {
		cli = "cli"
	}
	runID = strings.TrimSpace(runID)
	if runID == "" {
		return "Codex Bridge " + cli
	}
	return "Codex Bridge " + cli + " " + runID
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (m *OrchestrationManager) cwd(payload protocol.OrchestrationStartPayload) string {
	if strings.TrimSpace(payload.RunCWD) != "" {
		path := expandHome(payload.RunCWD)
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		return path
	}
	raw := payload.CWD
	if strings.TrimSpace(raw) == "" {
		raw = m.cfg.Bridge.CWD
	}
	if strings.TrimSpace(raw) == "" {
		raw = "."
	}
	path := expandHome(raw)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return path
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

func PrepareOrchestrationPromptFiles(cfg *config.Config, runCWD, runID, prompt string, files []protocol.AttachmentPayload) (string, []store.OrchestrationFile, error) {
	if len(files) == 0 {
		return strings.TrimSpace(prompt), nil, nil
	}
	if len(files) > 12 {
		return "", nil, errors.New("at most 12 files can be uploaded")
	}
	baseDir := runCWD
	if baseDir == "" {
		baseDir = cfg.Bridge.CWD
	}
	if baseDir == "" {
		baseDir = "."
	}
	baseDir = expandHome(baseDir)
	if abs, err := filepath.Abs(baseDir); err == nil {
		baseDir = abs
	}
	uploadDir := filepath.Join(baseDir, ".codex-bridge", "orchestrations", safeFileName(runID))
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
	return b.String(), metas, nil
}

func safeOrchestrationUploadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = safeOrchestrationFileName.ReplaceAllString(name, "-")
	return strings.Trim(name, ".-")
}
