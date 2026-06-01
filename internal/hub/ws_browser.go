package hub

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

func (s *Server) handleBrowserWS(w http.ResponseWriter, r *http.Request, uid string) {
	sid := r.URL.Query().Get("sid")
	if sid == "" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "missing sid")
		return
	}
	session, err := s.store.SessionByID(r.Context(), sid, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "session not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load session")
		return
	}

	upgrader := websocket.Upgrader{CheckOrigin: s.checkOrigin}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(s.browserReadLimit())
	_ = ws.SetReadDeadline(time.Now().Add(s.browserReadTimeout()))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(s.browserReadTimeout()))
	})

	conn := NewBrowserConn(sid, ws, s.cfg.Hub.MaxBrowserSendQueue)
	reattached := s.tryReattach(sid)
	s.pool.AddBrowser(sid, conn)
	go conn.WriteLoop()
	defer func() {
		last := s.pool.RemoveBrowser(sid, conn)
		conn.Close()
		if last {
			s.startBrowserLease(session)
		}
	}()

	openPayload := protocol.OpenSessionPayload{
		Sid:            sid,
		RemoteThreadID: session.RemoteThreadID,
	}
	if err := s.pool.SendToAgent(session.AgentID, protocol.MustEnvelope(protocol.TypeOpenSession, sid, openPayload)); err != nil {
		_ = conn.Send(s.bridgeErrorToBrowser(sid, err))
	} else {
		status := "opening"
		if reattached {
			status = "reattached"
		}
		_ = conn.Send(protocol.MustEnvelope(protocol.TypeStatus, sid, map[string]any{"status": status}))
	}

	for {
		var env protocol.Envelope
		if err := ws.ReadJSON(&env); err != nil {
			return
		}
		_ = ws.SetReadDeadline(time.Now().Add(s.browserReadTimeout()))
		if env.Sid == "" {
			env.Sid = sid
		}
		if env.Sid != sid {
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, sid, protocol.ErrorPayload{Code: "BAD_SID", Message: "sid mismatch"}))
			continue
		}
		s.handleBrowserEnvelope(r, uid, session, conn, env)
	}
}

func (s *Server) browserReadLimit() int64 {
	maxAttachmentBytes := s.cfg.Hub.MaxAttachmentBytes
	if maxAttachmentBytes <= 0 {
		maxAttachmentBytes = 8 * 1024 * 1024
	}
	// Base64 expands payloads by roughly 4/3. Allow up to 4 images plus JSON overhead.
	return s.cfg.Hub.MaxPromptBytes + (maxAttachmentBytes * 4 * 2) + 64*1024
}

func (s *Server) browserReadTimeout() time.Duration {
	timeout := 3 * s.cfg.Hub.HeartbeatInterval.Duration
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return timeout
}

func (s *Server) handleBrowserEnvelope(r *http.Request, uid string, session store.Session, conn *BrowserConn, env protocol.Envelope) {
	switch env.Type {
	case protocol.TypeHeartbeat:
		_ = conn.Send(protocol.MustEnvelope(protocol.TypeHeartbeat, session.ID, map[string]any{"ts": time.Now().Unix()}))
	case protocol.TypePrompt:
		payload, err := protocol.Decode[protocol.PromptPayload](env)
		if err != nil || payload.Content == "" {
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{Code: "BAD_PROMPT", Message: "prompt content is required"}))
			return
		}
		if int64(len(payload.Content)) > s.cfg.Hub.MaxPromptBytes {
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{Code: "PROMPT_TOO_LARGE", Message: "prompt is too large"}))
			return
		}
		if err := s.validatePromptAttachments(payload.Attachments); err != nil {
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{Code: "BAD_ATTACHMENT", Message: err.Error()}))
			return
		}
		if active, err := s.store.ActiveRunBySession(r.Context(), session.ID); err == nil && active.ID != "" {
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{
				Code:     "RUN_ACTIVE",
				Message:  "session already has an active prompt",
				RunID:    active.ID,
				PromptID: active.PromptID,
			}))
			return
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			slog.Error("[hub] active run lookup failed", "sid", session.ID, "error", err)
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{Code: "STORE_ERROR", Message: "failed to check active run"}))
			return
		}
		run, err := s.store.CreateRun(r.Context(), session.ID, payload.PromptID)
		if err != nil {
			if payload.PromptID != "" {
				if existing, findErr := s.store.RunByPromptID(r.Context(), session.ID, payload.PromptID); findErr == nil {
					_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{
						Code:     "DUPLICATE_PROMPT",
						Message:  "prompt was already submitted",
						RunID:    existing.ID,
						PromptID: existing.PromptID,
					}))
					return
				}
			}
			slog.Error("[hub] create run failed", "sid", session.ID, "error", err)
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{Code: "STORE_ERROR", Message: "failed to create run"}))
			return
		}
		if _, err := s.store.AddMessage(r.Context(), session.ID, "user", payload.Content, ""); err != nil {
			slog.Error("[hub] persist user message failed", "sid", session.ID, "error", err)
			_ = s.store.UpdateRunStatus(r.Context(), run.ID, store.RunFailed, "failed to save prompt", "")
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{Code: "STORE_ERROR", Message: "failed to save prompt"}))
			return
		}
		s.clearAssistantBuffer(session.ID)
		payload.RunID = run.ID
		payload.PromptID = run.PromptID
		env = protocol.MustEnvelope(protocol.TypePrompt, session.ID, payload)
		_ = conn.Send(protocol.MustEnvelope(protocol.TypeStatus, session.ID, map[string]any{"status": "running", "runId": run.ID, "promptId": run.PromptID}))
		if err := s.pool.SendToAgent(session.AgentID, env); err != nil {
			_ = s.store.UpdateRunStatus(r.Context(), run.ID, store.RunFailed, err.Error(), "")
			_ = conn.Send(s.bridgeErrorToBrowser(session.ID, err))
			return
		}
	case protocol.TypeCancel:
		if active, err := s.store.ActiveRunBySession(r.Context(), session.ID); err == nil && active.ID != "" {
			payload := protocol.PromptPayload{RunID: active.ID, PromptID: active.PromptID}
			env = protocol.MustEnvelope(protocol.TypeCancel, session.ID, payload)
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeStatus, session.ID, map[string]any{"status": "canceling", "runId": active.ID, "promptId": active.PromptID}))
		} else if err != nil && !errors.Is(err, store.ErrNotFound) {
			slog.Error("[hub] active run lookup for cancel failed", "sid", session.ID, "error", err)
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{Code: "STORE_ERROR", Message: "failed to cancel run"}))
			return
		}
		if err := s.pool.SendToAgent(session.AgentID, env); err != nil {
			if payload, decErr := protocol.Decode[protocol.PromptPayload](env); decErr == nil && payload.RunID != "" {
				_ = s.store.UpdateRunStatus(r.Context(), payload.RunID, store.RunCanceled, err.Error(), "")
			}
			_ = conn.Send(s.bridgeErrorToBrowser(session.ID, err))
			return
		}
	case protocol.TypeApprovalResponse:
		payload, err := protocol.Decode[protocol.ApprovalResponsePayload](env)
		if err != nil || payload.RequestID == "" {
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{Code: "BAD_APPROVAL_RESPONSE", Message: "approval response is invalid"}))
			return
		}
		decision := strings.ToLower(strings.TrimSpace(payload.Decision))
		if decision != "accept" && decision != "decline" && decision != "cancel" {
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{Code: "BAD_APPROVAL_DECISION", Message: "approval decision must be accept, decline, or cancel"}))
			return
		}
		payload.Decision = decision
		if err := s.pool.SendToAgent(session.AgentID, protocol.MustEnvelope(protocol.TypeApprovalResponse, session.ID, payload)); err != nil {
			_ = conn.Send(s.bridgeErrorToBrowser(session.ID, err))
			return
		}
	case protocol.TypeCloseSession:
		if err := s.pool.SendToAgent(session.AgentID, env); err != nil {
			_ = conn.Send(s.bridgeErrorToBrowser(session.ID, err))
			return
		}
	default:
		_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, session.ID, protocol.ErrorPayload{Code: "BAD_TYPE", Message: "unsupported browser frame type"}))
	}
}

func (s *Server) validatePromptAttachments(attachments []protocol.AttachmentPayload) error {
	if len(attachments) > 4 {
		return errors.New("at most 4 images can be uploaded at once")
	}
	maxBytes := s.cfg.Hub.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = 8 * 1024 * 1024
	}
	for _, attachment := range attachments {
		if !strings.HasPrefix(attachment.MimeType, "image/") {
			return errors.New("only image uploads are supported")
		}
		if attachment.Size <= 0 || attachment.Size > maxBytes {
			return errors.New("image is too large")
		}
		if strings.TrimSpace(attachment.Data) == "" {
			return errors.New("image data is missing")
		}
	}
	return nil
}
