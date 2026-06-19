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
	// done is closed when the run goroutine has fully exited. Start waits on it
	// before launching a replacement goroutine for the same run id so a
	// superseded run cannot interleave stale terminal events after the new
	// run's start.
	done chan struct{}
}

type orchestrationSessionState struct {
	WorkerPair           string
	CodexThreadID        string
	CodexThreadIDs       map[string]string
	ClaudeSessionID      string
	ClaudeSessionStarted bool
	CodexResumeMode      string
	CodexResumeModes     map[string]string
	ClaudeResumeMode     string
	NativeSession        *orchestrationNativeSession
	CommandFingerprints  map[string]bridgeprofiles.CommandFingerprint
}

type orchestrationNativeSession struct {
	runID                   string
	cwd                     string
	nativeContextCompaction string
	mu                      sync.Mutex
	codex                   map[string]*orchestrationCodexSession
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
	orchestrationCodexDefaultSlot            = "codex"
	orchestrationCodexSlotA                  = "codex-a"
	orchestrationCodexSlotB                  = "codex-b"
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
	// Cancel and join any goroutine still owning this run id before starting
	// the replacement: otherwise its stale terminal events interleave after
	// the new run.start and the hub records the wrong final status.
	for {
		m.mu.Lock()
		old := m.runs[payload.RunID]
		if old == nil {
			break
		}
		oldSession := m.sessions[payload.RunID]
		delete(m.sessions, payload.RunID)
		m.mu.Unlock()
		old.cancel()
		if oldSession != nil {
			oldSession.close()
		}
		if old.done != nil {
			<-old.done
		}
	}
	handle := &orchestrationRunHandle{cancel: cancel, done: make(chan struct{})}
	m.runs[payload.RunID] = handle
	delete(m.conclusions, payload.RunID)
	m.mu.Unlock()

	go func() {
		defer close(handle.done)
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
	for runID := range m.conclusions {
		delete(m.conclusions, runID)
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
		session = &orchestrationNativeSession{runID: runID, cwd: cwd, codex: map[string]*orchestrationCodexSession{}}
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
	var codexSessions []*orchestrationCodexSession
	for _, codex := range s.codex {
		if codex != nil {
			codexSessions = append(codexSessions, codex)
		}
	}
	claude := s.claude
	s.codex = nil
	s.claude = nil
	s.mu.Unlock()
	for _, codex := range codexSessions {
		if codex.client != nil {
			codex.client.unsubscribeThreadWithTimeout(codex.threadID)
			codex.client.close()
		}
	}
	if claude != nil {
		_ = claude.stdin.Close()
		if claude.cmd != nil && claude.cmd.Process != nil {
			_ = terminateProcessGroup(claude.cmd.Process.Pid)
		}
		waitClaudeSessionExit(claude)
		if claude.release != nil {
			claude.release()
		}
	}
}

func (s *orchestrationNativeSession) codexSessionLocked(workerSlot string) *orchestrationCodexSession {
	if s == nil {
		return nil
	}
	slot := normalizeCodexWorkerSlot(workerSlot)
	return s.codex[slot]
}

func (s *orchestrationNativeSession) setCodexSessionLocked(workerSlot string, codex *orchestrationCodexSession) {
	if s == nil {
		return
	}
	if s.codex == nil {
		s.codex = map[string]*orchestrationCodexSession{}
	}
	slot := normalizeCodexWorkerSlot(workerSlot)
	if codex == nil {
		delete(s.codex, slot)
		return
	}
	s.codex[slot] = codex
}

func (s *orchestrationSessionState) setCodexThreadID(workerSlot, threadID string) {
	if s == nil {
		return
	}
	workerSlot = normalizeCodexWorkerSlot(workerSlot)
	threadID = strings.TrimSpace(threadID)
	if s.CodexThreadIDs == nil {
		s.CodexThreadIDs = map[string]string{}
	}
	if threadID == "" {
		delete(s.CodexThreadIDs, workerSlot)
		s.refreshLegacyCodexThreadID()
		return
	}
	s.CodexThreadIDs[workerSlot] = threadID
	s.refreshLegacyCodexThreadID()
}

func (s *orchestrationSessionState) codexThreadID(workerSlot string) string {
	if s == nil {
		return ""
	}
	workerSlot = normalizeCodexWorkerSlot(workerSlot)
	if threadID := strings.TrimSpace(s.CodexThreadIDs[workerSlot]); threadID != "" {
		return threadID
	}
	if workerSlot == orchestrationCodexDefaultSlot {
		return strings.TrimSpace(s.CodexThreadID)
	}
	return ""
}

func (s *orchestrationSessionState) codexThreadIDsCopy() map[string]string {
	if s == nil {
		return nil
	}
	out := make(map[string]string, len(s.CodexThreadIDs)+1)
	for key, value := range s.CodexThreadIDs {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 && strings.TrimSpace(s.CodexThreadID) != "" {
		out[orchestrationCodexDefaultSlot] = strings.TrimSpace(s.CodexThreadID)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (s *orchestrationSessionState) refreshLegacyCodexThreadID() {
	if s == nil {
		return
	}
	s.CodexThreadID = firstNonEmpty(
		s.CodexThreadIDs[orchestrationCodexDefaultSlot],
		s.CodexThreadIDs[orchestrationCodexSlotA],
		s.CodexThreadIDs[orchestrationCodexSlotB],
		s.CodexThreadID,
	)
}

func orchestrationDefaultCodexSlot(workerPair string) string {
	if protocol.NormalizeOrchestrationWorkerPair(workerPair) == protocol.WorkerPairCodexCodex {
		return orchestrationCodexSlotA
	}
	return orchestrationCodexDefaultSlot
}

func (s *orchestrationSessionState) setCodexResumeMode(workerSlot, mode string) {
	if s == nil {
		return
	}
	workerSlot = normalizeCodexWorkerSlot(workerSlot)
	mode = strings.TrimSpace(mode)
	if s.CodexResumeModes == nil {
		s.CodexResumeModes = map[string]string{}
	}
	if mode == "" {
		delete(s.CodexResumeModes, workerSlot)
		if workerSlot == orchestrationCodexDefaultSlot {
			s.CodexResumeMode = ""
		}
		return
	}
	s.CodexResumeModes[workerSlot] = mode
	if workerSlot == orchestrationCodexDefaultSlot || s.CodexResumeMode == "" {
		s.CodexResumeMode = mode
	}
}

func (s orchestrationSessionState) codexResumeMode(workerSlot string) string {
	workerSlot = normalizeCodexWorkerSlot(workerSlot)
	if mode := strings.TrimSpace(s.CodexResumeModes[workerSlot]); mode != "" {
		return mode
	}
	if workerSlot == orchestrationCodexDefaultSlot {
		return strings.TrimSpace(s.CodexResumeMode)
	}
	return ""
}

func cleanCodexThreadIDs(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
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
	profile := normalizeOrchestrationProfile(payload.Profile)
	var bootstrapNote string
	if profile == bridgeprofiles.Formal() {
		harness, err := prepareFormalProofHarness(m.cfg, payload, runCWD)
		if err != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.error",
				Status: store.OrchestrationFailed,
				Error:  err.Error(),
			})
			return
		}
		runCWD = harness.RunDir
		payload.CWD = runCWD
		payload.RunCWD = runCWD
		payload.Prompt = harness.Prompt
		payload.Files = nil
		bootstrapNote = harness.BootstrapNote
	} else {
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
	}
	mode := payload.Mode
	if mode != "collaboration" && mode != "debate" {
		mode = "collaboration"
	}
	workerPair := protocol.NormalizeOrchestrationWorkerPair(payload.WorkerPair)
	firstCLI := normalizeRelayFirstCLI(payload.FirstCLI)
	if workerPair == protocol.WorkerPairCodexCodex {
		firstCLI = "codex"
	}
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
	nativeContextCompaction := protocol.NormalizeNativeContextCompaction(payload.NativeContextCompaction)
	nativeSession := m.nativeSession(payload.RunID, runCWD)
	nativeSession.mu.Lock()
	nativeSession.nativeContextCompaction = nativeContextCompaction
	nativeSession.mu.Unlock()
	sessionState := orchestrationSessionState{
		WorkerPair:          workerPair,
		CodexThreadIDs:      cleanCodexThreadIDs(payload.CodexThreadIDs),
		CodexResumeModes:    map[string]string{},
		ClaudeSessionID:     stableOrchestrationSessionID(payload.RunID, "claude"),
		NativeSession:       nativeSession,
		CommandFingerprints: map[string]bridgeprofiles.CommandFingerprint{},
	}
	if payload.Resume {
		sessionState.CodexThreadID = payload.CodexThreadID
		if sessionState.CodexThreadID != "" && len(sessionState.CodexThreadIDs) == 0 {
			sessionState.setCodexThreadID(orchestrationDefaultCodexSlot(workerPair), sessionState.CodexThreadID)
		}
		sessionState.ClaudeSessionStarted = payload.ClaudeStarted
	}
	if sessionState.CodexThreadID == "" {
		sessionState.CodexThreadID = firstNonEmpty(sessionState.CodexThreadIDs[orchestrationCodexDefaultSlot], sessionState.CodexThreadIDs[orchestrationCodexSlotA])
	}
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "run.start",
		Status:  store.OrchestrationRunning,
		Content: fmt.Sprintf("Starting relay orchestration with %d CLI turns.", maxTurns),
		RunStartData: &protocol.RunStartData{
			CWD:                     runCWD,
			Mode:                    mode,
			WorkerPair:              workerPair,
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
			"workerPair":              workerPair,
			"firstCli":                firstCLI,
			"maxTurns":                maxTurns,
			"maxTurnsRequested":       maxTurnsRequested,
			"maxTurnsApplied":         maxTurns,
			"promptSeq":               payload.PromptSeq,
			"profile":                 profile,
			"nativeContextCompaction": nativeContextCompaction,
		},
	})
	if bootstrapNote != "" {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:     "turn.delta",
			Source:   "bridge",
			Severity: "info",
			Role:     "bootstrap",
			CLI:      "bridge",
			Content:  bootstrapNote,
			BridgeNoteData: &protocol.BridgeNoteData{
				Category: "formal-proof-harness-bootstrap",
			},
			Data: map[string]any{
				"category": "formal-proof-harness-bootstrap",
				"cwd":      runCWD,
			},
		})
	}

	var history []orchestrationTurn
	formalHarnessSyncNote := ""
	for turn := 1; turn <= maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.cancelled",
				Status: store.OrchestrationCanceled,
				Error:  "canceled",
			})
			return
		}
		turnPlan := roleForTurnWithWorkerPair(mode, workerPair, firstCLI, turn)
		role, cli, workerSlot := turnPlan.Role, turnPlan.CLI, turnPlan.WorkerSlot
		turnID := fmt.Sprintf("%s-%02d", payload.RunID, turn)
		if payload.PromptSeq > 0 {
			turnID = fmt.Sprintf("%s-p%03d-%02d", payload.RunID, payload.PromptSeq, turn)
		}
		clearRelayResumeMode(cli, workerSlot, &sessionState)
		contextForTurn := appendFormalProofHarnessSyncContext(payload.Context, formalHarnessSyncNote)
		prompt := composeRelayPromptWithWorkerSlot(mode, firstCLI, profile, payload.Prompt, contextForTurn, payload.Resume, role, cli, workerSlot, turn, maxTurns, history)
		resumeMode := plannedRelayResumeMode(cli, workerSlot, sessionState)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.start",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: orchestrationTurnStartContent(cli, workerSlot, &sessionState, turn, maxTurns, role),
			TurnStartData: &protocol.TurnStartData{
				CLI:        cli,
				WorkerSlot: workerSlot,
				Turn:       turn,
				MaxTurns:   maxTurns,
				PromptText: prompt,
				Profile:    profile,
				ResumeMode: resumeMode,
			},
			Data: map[string]any{
				"cwd":        m.cwd(payload),
				"cli":        cli,
				"workerSlot": workerSlot,
				"turn":       turn,
				"maxTurns":   maxTurns,
				"promptText": prompt,
				"profile":    profile,
				"relayOnly":  true,
				"resumeMode": resumeMode,
			},
		})
		record, turnStatus, err := m.runRelayTurnWithContinuations(ctx, payload, turnID, role, cli, workerSlot, prompt, &sessionState, runCWD)
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
				RunEndData: m.relayRunEndData(cli, workerSlot, workerPair, sessionState, runCWD),
				Data:       relayTurnEndData(cli, workerSlot, sessionState),
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
			RunEndData: m.relayRunEndData(cli, workerSlot, workerPair, sessionState, runCWD),
			Data:       relayTurnEndData(cli, workerSlot, sessionState),
		})
		if turn < maxTurns && turnStatus == "success" {
			m.runPostTurnNativeMaintenance(ctx, payload.RunID, turnID, role, cli, workerSlot, &sessionState)
		}
		if profile == bridgeprofiles.Formal() {
			formalHarnessSyncNote = m.emitFormalProofHarnessSync(payload.RunID, turnID, runCWD)
		}
	}
	finalContent := relayTerminalContent(history)
	finalRunEndData := runEndDataWithNativeResume(&protocol.RunEndData{
		WorkerPair:         workerPair,
		CodexThreadID:      sessionState.CodexThreadID,
		CodexThreadIDs:     cleanCodexThreadIDs(sessionState.CodexThreadIDs),
		ClaudeSessionID:    sessionState.ClaudeSessionID,
		CodexNativeResume:  codexNativeResumeInfoForSlot(orchestrationDefaultCodexSlot(workerPair), sessionState.CodexThreadID, runCWD),
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
			"workerPair":         workerPair,
			"codexThreadId":      sessionState.CodexThreadID,
			"codexThreadIds":     cleanCodexThreadIDs(sessionState.CodexThreadIDs),
			"claudeSessionId":    sessionState.ClaudeSessionID,
			"codexNativeResume":  finalRunEndData.CodexNativeResume,
			"claudeNativeResume": finalRunEndData.ClaudeNativeResume,
			"nativeResume":       finalRunEndData.NativeResume,
		},
	})
	m.runFinalNativeMaintenance(ctx, workerPair, mode, firstCLI, maxTurns, &sessionState)
}

func (m *OrchestrationManager) runRelayTurnWithContinuations(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, cli, workerSlot, prompt string, state *orchestrationSessionState, runCWD string) (orchestrationTurn, string, error) {
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
		content, tools, err := m.runRelayCLI(ctx, payload, turnID, role, cli, workerSlot, nextPrompt, state)
		recordCommandFingerprints(state, runCWD, tools)
		record := newOrchestrationTurnRecordWithSlot(turnID, role, cli, workerSlot, content, tools)
		if err != nil {
			record.Err = visibleCLIError(err)
		}
		combined = mergeOrchestrationTurnAttempts(combined, record)
		if err == nil && !orchestrationTurnNeedsContinuation(record, err) {
			return combined, status, nil
		}
		if recoverableRelayCLIError(cli, content, err) && orchestrationTurnHasFinalConclusion(record) {
			warning := visibleCLIError(err)
			m.resetCodexInteractiveSessionAfterRecoverableError(workerSlot, state)
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
		m.resetNativeInteractiveSessionForContinuation(cli, workerSlot, state)
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

func (m *OrchestrationManager) resetNativeInteractiveSessionForContinuation(cli, workerSlot string, state *orchestrationSessionState) {
	if state == nil || state.NativeSession == nil {
		return
	}
	session := state.NativeSession
	session.mu.Lock()
	defer session.mu.Unlock()
	switch cli {
	case "codex":
		workerSlot = normalizeCodexWorkerSlot(workerSlot)
		codex := session.codexSessionLocked(workerSlot)
		if codex == nil {
			return
		}
		if codex.threadID != "" {
			state.setCodexThreadID(workerSlot, codex.threadID)
		}
		if codex.client != nil {
			codex.client.close()
		}
		session.setCodexSessionLocked(workerSlot, nil)
	case "claude":
		claude := session.claude
		if claude == nil {
			return
		}
		_ = claude.stdin.Close()
		if claude.cmd != nil && claude.cmd.Process != nil {
			_ = terminateProcessGroup(claude.cmd.Process.Pid)
		}
		waitClaudeSessionExit(claude)
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
