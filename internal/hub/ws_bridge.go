package hub

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func (s *Server) handleBridgeWS(w http.ResponseWriter, r *http.Request) {
	upgrader := websocket.Upgrader{CheckOrigin: s.checkOrigin}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(1 << 20)

	token := r.URL.Query().Get("token")
	_ = ws.SetReadDeadline(time.Now().Add(10 * time.Second))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(s.bridgeReadTimeout()))
	})
	var first protocol.Envelope
	if err := ws.ReadJSON(&first); err != nil {
		slog.Warn("[hub] bridge register read failed", "error", err)
		return
	}
	_ = ws.SetReadDeadline(time.Time{})
	if first.Type != protocol.TypeRegister {
		_ = ws.WriteJSON(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Message: "first bridge frame must be register"}))
		return
	}
	reg, err := protocol.Decode[protocol.RegisterPayload](first)
	if err != nil || reg.MachineID == "" {
		_ = ws.WriteJSON(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Message: "invalid register payload"}))
		return
	}
	if err := s.store.ConsumeEnrollToken(r.Context(), token, reg.MachineID); err != nil {
		slog.Warn("[hub] bridge enroll rejected", "machine_id", reg.MachineID, "error", err)
		_ = ws.WriteJSON(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Message: "invalid enroll token"}))
		return
	}
	prevAgent, prevErr := s.store.AgentByMachineID(r.Context(), reg.MachineID)
	agent, err := s.store.UpsertAgent(r.Context(), reg.Name, reg.MachineID, reg.Hostname, reg.Instance)
	if err != nil {
		slog.Error("[hub] bridge agent upsert failed", "error", err)
		_ = ws.WriteJSON(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Message: "failed to register agent"}))
		return
	}
	if reg.Instance != "" && prevErr == nil && prevAgent.Instance != "" && prevAgent.Instance != reg.Instance {
		s.scheduleAgentRunFailure(agent.ID, reg.Instance, 0)
	}

	conn := NewBridgeConn(agent.ID, ws, s.cfg.Hub.MaxBridgeSendQueue)
	s.pool.RegisterAgent(conn)
	defer func() {
		s.pool.UnregisterAgent(agent.ID, conn)
		s.scheduleAgentRunFailure(agent.ID, reg.Instance, s.cfg.Bridge.ReconnectMax.Duration+time.Second)
	}()
	go conn.WriteLoop()
	defer conn.Close()

	if err := conn.Send(protocol.MustEnvelope(protocol.TypeRegistered, "", protocol.RegisteredPayload{AgentID: agent.ID})); err != nil {
		return
	}
	slog.Info("[hub] bridge connected", "agent_id", agent.ID, "machine_id", agent.MachineID, "name", agent.Name)

	ticker := time.NewTicker(s.cfg.Hub.HeartbeatInterval.Duration)
	defer ticker.Stop()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = ws.SetReadDeadline(time.Now().Add(s.bridgeReadTimeout()))
		for {
			var env protocol.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				return
			}
			_ = ws.SetReadDeadline(time.Now().Add(s.bridgeReadTimeout()))
			s.handleBridgeEnvelope(r.Context(), agent.ID, env)
		}
	}()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-done:
			return
		case <-ticker.C:
			_ = s.store.TouchAgent(r.Context(), agent.ID)
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeHeartbeat, "", map[string]any{"ts": time.Now().Unix()}))
		}
	}
}

func (s *Server) bridgeReadTimeout() time.Duration {
	timeout := s.cfg.Hub.BridgeReadTimeout.Duration
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	return timeout
}

func (s *Server) scheduleAgentRunFailure(agentID, instance string, delay time.Duration) {
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		if delay > 0 && s.pool.AgentOnline(agentID) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		reason := "bridge disconnected while run was active"
		if delay == 0 {
			reason = "bridge process restarted while run was active"
		}
		if n, err := s.store.MarkActiveRunsForAgentFailed(ctx, agentID, reason); err != nil {
			slog.Error("[hub] mark active agent runs failed", "agent_id", agentID, "error", err)
		} else if n > 0 {
			slog.Warn("[hub] marked active runs failed", "agent_id", agentID, "instance", instance, "count", n, "reason", reason)
		}
	}()
}

func (s *Server) handleBridgeEnvelope(ctx context.Context, agentID string, env protocol.Envelope) {
	switch env.Type {
	case protocol.TypeHeartbeat:
		_ = s.store.TouchAgent(ctx, agentID)
	case protocol.TypeSessionOpened:
		payload, err := protocol.Decode[protocol.SessionOpenedPayload](env)
		if err == nil && payload.RemoteThreadID != "" {
			_ = s.updateSessionRemoteThreadBySID(ctx, env.Sid, payload.RemoteThreadID)
		}
		s.pool.BroadcastToBrowsers(env.Sid, env)
	case protocol.TypeSessionUpdate:
		payload, err := protocol.Decode[protocol.SessionUpdatePayload](env)
		if err == nil {
			s.appendAssistantDelta(env.Sid, payload.Delta, payload.Content)
		}
		s.pool.BroadcastToBrowsers(env.Sid, env)
	case protocol.TypePromptComplete:
		s.handlePromptComplete(ctx, env)
	case protocol.TypeError:
		s.handleBridgeError(ctx, env)
		s.pool.BroadcastToBrowsers(env.Sid, env)
	default:
		s.pool.BroadcastToBrowsers(env.Sid, env)
	}
}

func (s *Server) handlePromptComplete(ctx context.Context, env protocol.Envelope) {
	payload, err := protocol.Decode[protocol.PromptCompletePayload](env)
	if err != nil {
		s.pool.BroadcastToBrowsers(env.Sid, protocol.MustEnvelope(protocol.TypeError, env.Sid, protocol.ErrorPayload{Message: "invalid prompt_complete payload"}))
		return
	}
	if payload.RemoteThreadID != "" {
		_ = s.updateSessionRemoteThreadBySID(ctx, env.Sid, payload.RemoteThreadID)
	}
	content := payload.Content
	if content == "" {
		content = s.consumeAssistantBuffer(env.Sid)
	} else {
		s.clearAssistantBuffer(env.Sid)
	}
	if content != "" {
		usage := ""
		if len(payload.Usage) > 0 {
			usage = string(payload.Usage)
		}
		if int64(len(content)) > s.cfg.Hub.MaxAssistantMessageBytes {
			content = content[:s.cfg.Hub.MaxAssistantMessageBytes] + "\n\n[truncated by hub]"
		}
		if _, err := s.store.AddMessage(ctx, env.Sid, "assistant", content, usage); err != nil {
			slog.Error("[hub] persist assistant message failed", "sid", env.Sid, "error", err)
			if payload.RunID != "" {
				_ = s.store.UpdateRunStatus(ctx, payload.RunID, store.RunFailed, "failed to persist assistant message", usage)
			}
			s.pool.BroadcastToBrowsers(env.Sid, protocol.MustEnvelope(protocol.TypeError, env.Sid, protocol.ErrorPayload{
				Code:     "STORE_ERROR",
				Message:  "failed to persist assistant message",
				RunID:    payload.RunID,
				PromptID: payload.PromptID,
			}))
			return
		}
	}
	if payload.RunID != "" {
		usage := ""
		if len(payload.Usage) > 0 {
			usage = string(payload.Usage)
		}
		if err := s.store.UpdateRunStatus(ctx, payload.RunID, store.RunSucceeded, "", usage); err != nil {
			slog.Error("[hub] update run succeeded failed", "run_id", payload.RunID, "error", err)
		}
	}
	s.pool.BroadcastToBrowsers(env.Sid, env)
}

func (s *Server) handleBridgeError(ctx context.Context, env protocol.Envelope) {
	payload, err := protocol.Decode[protocol.ErrorPayload](env)
	if err != nil || payload.RunID == "" {
		return
	}
	status := store.RunFailed
	if payload.Code == "CANCELED" {
		status = store.RunCanceled
	}
	if err := s.store.UpdateRunStatus(ctx, payload.RunID, status, payload.Message, ""); err != nil {
		slog.Error("[hub] update run error failed", "run_id", payload.RunID, "error", err)
	}
}

func (s *Server) updateSessionRemoteThreadBySID(ctx context.Context, sid, remoteThreadID string) error {
	if sid == "" || remoteThreadID == "" {
		return nil
	}
	return s.store.UpdateSessionRemoteThreadByID(ctx, sid, remoteThreadID)
}

func (s *Server) appendAssistantDelta(sid, delta, content string) {
	s.buffersMu.Lock()
	defer s.buffersMu.Unlock()
	if delta != "" {
		s.buffers[sid] += delta
		return
	}
	if content != "" {
		s.buffers[sid] = content
	}
}

func (s *Server) consumeAssistantBuffer(sid string) string {
	s.buffersMu.Lock()
	defer s.buffersMu.Unlock()
	content := s.buffers[sid]
	delete(s.buffers, sid)
	return content
}

func (s *Server) clearAssistantBuffer(sid string) {
	s.buffersMu.Lock()
	defer s.buffersMu.Unlock()
	delete(s.buffers, sid)
}

func (s *Server) checkOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	originURL, err := url.Parse(origin)
	if err != nil || originURL.Scheme == "" || originURL.Host == "" {
		return false
	}
	if strings.EqualFold(originURL.Host, r.Host) && (originURL.Scheme == "https" || originURL.Scheme == "http") {
		return true
	}
	for _, allowed := range s.cfg.Hub.AllowedOrigins {
		if allowed == "*" {
			return true
		}
		allowedURL, err := url.Parse(allowed)
		if err != nil || allowedURL.Scheme == "" || allowedURL.Host == "" {
			continue
		}
		if strings.EqualFold(allowedURL.Scheme, originURL.Scheme) && strings.EqualFold(allowedURL.Host, originURL.Host) {
			return true
		}
	}
	return false
}

func (s *Server) bridgeErrorToBrowser(sid string, err error) protocol.Envelope {
	code := "BRIDGE_ERROR"
	status := http.StatusInternalServerError
	if errors.Is(err, ErrAgentOffline) {
		code = "AGENT_OFFLINE"
		status = http.StatusConflict
	}
	_ = status
	return protocol.MustEnvelope(protocol.TypeError, sid, protocol.ErrorPayload{Code: code, Message: err.Error()})
}

func writeWSError(ws *websocket.Conn, message string) {
	_ = ws.WriteJSON(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Message: message}))
}
