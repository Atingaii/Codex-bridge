package bridge

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

var (
	errSessionNotOpen = errors.New("session is not open")
	errSessionBusy    = errors.New("session already has an active prompt")
)

type SessionManager struct {
	cfg      *config.Config
	mu       sync.Mutex
	sessions map[string]*Session
}

type Session struct {
	sid            string
	remoteThreadID string
	runner         Runner
	cancel         context.CancelFunc
	busy           bool
	out            chan<- protocol.Envelope
	runID          string
	promptID       string
	pending        []protocol.Envelope
	approvals      map[string]chan protocol.ApprovalResponsePayload
}

func NewSessionManager(cfg *config.Config) *SessionManager {
	return &SessionManager{
		cfg:      cfg,
		sessions: make(map[string]*Session),
	}
}

func (m *SessionManager) Open(sid, remoteThreadID string, out chan<- protocol.Envelope) error {
	if sid == "" {
		return errors.New("sid is required")
	}
	m.mu.Lock()
	if existing := m.sessions[sid]; existing != nil {
		if remoteThreadID != "" {
			existing.remoteThreadID = remoteThreadID
		}
		existing.out = out
		pending := append([]protocol.Envelope(nil), existing.pending...)
		existing.pending = nil
		opened := protocol.MustEnvelope(protocol.TypeSessionOpened, sid, protocol.SessionOpenedPayload{
			RemoteThreadID: existing.remoteThreadID,
			Runner:         m.cfg.Bridge.Runner,
		})
		m.mu.Unlock()
		for _, env := range pending {
			send(out, env)
		}
		send(out, opened)
		return nil
	}
	if max := m.cfg.Bridge.MaxSessions; max > 0 && len(m.sessions) >= max {
		m.mu.Unlock()
		return fmt.Errorf("max sessions reached (%d)", max)
	}
	runner, err := NewRunner(m.cfg)
	if err != nil {
		m.mu.Unlock()
		return err
	}
	s := &Session{sid: sid, remoteThreadID: remoteThreadID, runner: runner, out: out, approvals: make(map[string]chan protocol.ApprovalResponsePayload)}
	m.sessions[sid] = s
	m.mu.Unlock()
	send(out, protocol.MustEnvelope(protocol.TypeSessionOpened, sid, protocol.SessionOpenedPayload{
		RemoteThreadID: remoteThreadID,
		Runner:         runner.Name(),
	}))
	return nil
}

func (m *SessionManager) Prompt(parent context.Context, sid string, payload protocol.PromptPayload, out chan<- protocol.Envelope) {
	content := payload.Content
	runID := payload.RunID
	promptID := payload.PromptID
	s, remoteThreadID, ctx, cancel, err := m.beginPrompt(sid, runID, promptID, out)
	if err != nil {
		code := "NO_SESSION"
		if errors.Is(err, errSessionBusy) {
			code = "RUN_ACTIVE"
		}
		send(out, protocol.MustEnvelope(protocol.TypeError, sid, protocol.ErrorPayload{Code: code, Message: err.Error(), RunID: runID, PromptID: promptID}))
		return
	}
	preparedContent, cleanup, err := m.preparePromptContent(sid, content, payload.Attachments)
	if err != nil {
		cancel()
		m.mu.Lock()
		s.cancel = nil
		s.busy = false
		s.runID = ""
		s.promptID = ""
		m.mu.Unlock()
		send(out, protocol.MustEnvelope(protocol.TypeError, sid, protocol.ErrorPayload{Code: "ATTACHMENT_ERROR", Message: err.Error(), RunID: runID, PromptID: promptID}))
		return
	}
	defer cleanup()

	approvals := sessionApprovalRequester{manager: m, sid: sid, runID: runID, promptID: promptID}
	result, err := s.runner.Prompt(ctx, RunnerRequest{
		SID:            sid,
		Content:        preparedContent,
		RemoteThreadID: remoteThreadID,
		RunID:          runID,
		PromptID:       promptID,
		Approvals:      approvals,
	}, func(update RunnerUpdate) {
		if update.Delta == "" && update.Content == "" && update.Tool == nil {
			return
		}
		var tool *protocol.ToolEvent
		if update.Tool != nil {
			tool = &protocol.ToolEvent{
				ID:       update.Tool.ID,
				Status:   update.Tool.Status,
				Command:  update.Tool.Command,
				Output:   update.Tool.Output,
				ExitCode: update.Tool.ExitCode,
			}
		}
		m.sendSessionEnvelope(sid, protocol.MustEnvelope(protocol.TypeSessionUpdate, sid, protocol.SessionUpdatePayload{
			Delta:    update.Delta,
			Content:  update.Content,
			RunID:    runID,
			PromptID: promptID,
			Event:    updateEvent(update),
			Tool:     tool,
		}))
	})
	cancel()

	m.mu.Lock()
	if result.RemoteThreadID != "" {
		s.remoteThreadID = result.RemoteThreadID
	}
	s.cancel = nil
	s.busy = false
	s.runID = ""
	s.promptID = ""
	m.mu.Unlock()

	if err != nil {
		code := "RUNNER_ERROR"
		if errors.Is(err, context.Canceled) {
			code = "CANCELED"
		}
		message := err.Error()
		if code == "CANCELED" {
			message = "canceled by user"
		}
		m.sendSessionEnvelope(sid, protocol.MustEnvelope(protocol.TypeError, sid, protocol.ErrorPayload{Code: code, Message: message, RunID: runID, PromptID: promptID}))
		return
	}
	m.sendSessionEnvelope(sid, protocol.MustEnvelope(protocol.TypePromptComplete, sid, protocol.PromptCompletePayload{
		Content:        result.Content,
		Usage:          result.Usage,
		RemoteThreadID: result.RemoteThreadID,
		RunID:          runID,
		PromptID:       promptID,
	}))
}

func (m *SessionManager) beginPrompt(sid, runID, promptID string, out chan<- protocol.Envelope) (*Session, string, context.Context, context.CancelFunc, error) {
	ctx, cancel := context.WithCancel(context.Background())
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[sid]
	if s == nil {
		cancel()
		return nil, "", nil, nil, errSessionNotOpen
	}
	if s.busy {
		cancel()
		return nil, "", nil, nil, errSessionBusy
	}
	s.cancel = cancel
	s.busy = true
	s.out = out
	s.runID = runID
	s.promptID = promptID
	return s, s.remoteThreadID, ctx, cancel, nil
}

type sessionApprovalRequester struct {
	manager  *SessionManager
	sid      string
	runID    string
	promptID string
}

func updateEvent(update RunnerUpdate) string {
	if update.Tool != nil {
		return "tool"
	}
	if update.Content != "" {
		return "content"
	}
	if update.Delta != "" {
		return "delta"
	}
	return ""
}

func (m *SessionManager) sendSessionEnvelope(sid string, env protocol.Envelope) {
	m.mu.Lock()
	s := m.sessions[sid]
	var out chan<- protocol.Envelope
	if s != nil {
		out = s.out
		if out == nil {
			s.pending = appendPending(s.pending, env)
		}
	}
	m.mu.Unlock()
	if out != nil {
		if !send(out, env) {
			var retry chan<- protocol.Envelope
			m.mu.Lock()
			if s := m.sessions[sid]; s != nil {
				if s.out != nil && s.out != out {
					retry = s.out
				} else {
					s.pending = appendPending(s.pending, env)
				}
			}
			m.mu.Unlock()
			if retry != nil {
				send(retry, env)
			}
		}
	}
}

func (r sessionApprovalRequester) RequestApproval(ctx context.Context, req protocol.ApprovalRequestPayload) (protocol.ApprovalResponsePayload, error) {
	if req.RequestID == "" {
		req.RequestID = fmt.Sprintf("apr_%d", time.Now().UnixNano())
	}
	req.RunID = r.runID
	req.PromptID = r.promptID
	ch := make(chan protocol.ApprovalResponsePayload, 1)
	m := r.manager
	m.mu.Lock()
	s := m.sessions[r.sid]
	if s == nil {
		m.mu.Unlock()
		return protocol.ApprovalResponsePayload{}, errSessionNotOpen
	}
	if s.approvals == nil {
		s.approvals = make(map[string]chan protocol.ApprovalResponsePayload)
	}
	s.approvals[req.RequestID] = ch
	m.mu.Unlock()

	m.sendSessionEnvelope(r.sid, protocol.MustEnvelope(protocol.TypeApprovalRequest, r.sid, req))
	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		m.removeApproval(r.sid, req.RequestID)
		return protocol.ApprovalResponsePayload{}, ctx.Err()
	}
}

func (m *SessionManager) ApprovalResponse(sid string, res protocol.ApprovalResponsePayload) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[sid]
	if s == nil || s.approvals == nil {
		return false
	}
	ch := s.approvals[res.RequestID]
	if ch == nil {
		return false
	}
	delete(s.approvals, res.RequestID)
	ch <- res
	return true
}

func (m *SessionManager) removeApproval(sid, requestID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s := m.sessions[sid]; s != nil && s.approvals != nil {
		delete(s.approvals, requestID)
	}
}

func appendPending(pending []protocol.Envelope, env protocol.Envelope) []protocol.Envelope {
	const maxPending = 256
	pending = append(pending, env)
	if len(pending) > maxPending {
		copy(pending, pending[len(pending)-maxPending:])
		pending = pending[:maxPending]
	}
	return pending
}

func (m *SessionManager) DetachOut(out chan<- protocol.Envelope) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.out == out {
			s.out = nil
		}
	}
}

func (m *SessionManager) AttachOut(out chan<- protocol.Envelope) {
	m.mu.Lock()
	var pending []protocol.Envelope
	for _, s := range m.sessions {
		s.out = out
		if len(s.pending) > 0 {
			pending = append(pending, s.pending...)
			s.pending = nil
		}
	}
	m.mu.Unlock()
	for _, env := range pending {
		send(out, env)
	}
}

func (m *SessionManager) Cancel(sid, runID, promptID string) {
	var cancel context.CancelFunc
	var currentRunID string
	var currentPromptID string
	m.mu.Lock()
	if s := m.sessions[sid]; s != nil {
		cancel = s.cancel
		currentRunID = s.runID
		currentPromptID = s.promptID
		if runID == "" {
			runID = currentRunID
		}
		if promptID == "" {
			promptID = currentPromptID
		}
	}
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *SessionManager) Close(sid string) {
	m.mu.Lock()
	s := m.sessions[sid]
	delete(m.sessions, sid)
	m.mu.Unlock()
	if s == nil {
		return
	}
	if s.cancel != nil {
		s.cancel()
	}
	for _, ch := range s.approvals {
		ch <- protocol.ApprovalResponsePayload{Decision: "cancel"}
	}
	s.runner.Close()
}

func (m *SessionManager) CloseAll() {
	m.mu.Lock()
	var sessions []*Session
	for sid, s := range m.sessions {
		sessions = append(sessions, s)
		delete(m.sessions, sid)
	}
	m.mu.Unlock()
	for _, s := range sessions {
		if s.cancel != nil {
			s.cancel()
		}
		for _, ch := range s.approvals {
			ch <- protocol.ApprovalResponsePayload{Decision: "cancel"}
		}
		s.runner.Close()
	}
}
