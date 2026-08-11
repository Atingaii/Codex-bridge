package hub

import (
	"context"
	"errors"
	"fmt"
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
	enroll, err := s.store.ConsumeEnrollTokenInfo(r.Context(), token, reg.MachineID)
	if err != nil {
		slog.Warn("[hub] bridge enroll rejected", "machine_id", reg.MachineID, "error", err)
		_ = ws.WriteJSON(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Message: "invalid enroll token"}))
		return
	}
	prevAgent, prevErr := s.store.AgentByMachineID(r.Context(), reg.MachineID)
	name := reg.Name
	if strings.TrimSpace(name) == "" && strings.TrimSpace(enroll.Label) != "" {
		name = enroll.Label
	}
	agent, err := s.store.UpsertAgentForUser(r.Context(), enroll.UserID, name, reg.MachineID, reg.Hostname, reg.Instance, reg.WorkingDirs)
	if err != nil {
		slog.Error("[hub] bridge agent upsert failed", "error", err)
		_ = ws.WriteJSON(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Message: "failed to register agent"}))
		return
	}
	if reg.Instance != "" && prevErr == nil && prevAgent.Instance != "" && prevAgent.Instance != reg.Instance {
		s.scheduleAgentRunFailure(agent.ID, reg.Instance, 0, "bridge process restarted while run was active")
	}

	conn := NewBridgeConn(agent.ID, ws, s.cfg.Hub.MaxBridgeSendQueue, reg.Capabilities, strings.TrimSpace(reg.Version))
	s.pool.RegisterAgent(conn)
	disconnectReason := "bridge reverse WebSocket closed"
	defer func() {
		s.pool.UnregisterAgent(agent.ID, conn)
		s.scheduleAgentRunFailure(agent.ID, reg.Instance, s.bridgeReconnectGrace(), disconnectReason)
	}()
	go conn.WriteLoop(s.websocketPingInterval())
	defer conn.Close()

	if err := conn.Send(protocol.MustEnvelope(protocol.TypeRegistered, "", protocol.RegisteredPayload{AgentID: agent.ID})); err != nil {
		return
	}
	// Registration acknowledgement must be the first outbound frame. The
	// Bridge waits for it before starting its normal reader, so reconnect work
	// queued earlier would be mistaken for a failed handshake.
	s.dispatchReadyTaskGraphsForAgent(r.Context(), agent.ID)
	slog.Info("[hub] bridge connected", "agent_id", agent.ID, "machine_id", agent.MachineID, "name", agent.Name)
	s.scheduleHistoricalUsageBackfill(agent.ID)

	ticker := time.NewTicker(s.websocketPingInterval())
	defer ticker.Stop()
	readErr := make(chan error, 1)
	go func() {
		_ = ws.SetReadDeadline(time.Now().Add(s.bridgeReadTimeout()))
		for {
			var env protocol.Envelope
			if err := ws.ReadJSON(&env); err != nil {
				readErr <- err
				return
			}
			_ = ws.SetReadDeadline(time.Now().Add(s.bridgeReadTimeout()))
			s.handleBridgeEnvelope(r.Context(), agent.ID, env)
		}
	}()

	for {
		select {
		case <-r.Context().Done():
			disconnectReason = "bridge request context ended: " + boundedTransportReason(r.Context().Err())
			return
		case err := <-readErr:
			disconnectReason = "bridge reverse WebSocket ended: " + boundedTransportReason(err)
			return
		case <-ticker.C:
			_ = s.store.TouchAgent(r.Context(), agent.ID)
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeHeartbeat, "", map[string]any{"ts": time.Now().Unix()}))
		}
	}
}

func (s *Server) scheduleHistoricalUsageBackfill(agentID string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		runs, err := s.store.ListTerminalOrchestrationRunsByAgent(ctx, agentID, 50)
		if err != nil {
			slog.Warn("[hub] historical usage run lookup failed", "agent_id", agentID, "error", err)
			return
		}
		for _, run := range runs {
			syncs, err := s.store.ListOrchestrationUsageSyncs(ctx, run.ID)
			if err != nil || len(syncs) > 0 {
				continue
			}
			events, err := s.store.ListOrchestrationEvents(ctx, run.ID, 10000)
			if err != nil {
				continue
			}
			if err := s.requestOrchestrationUsageSync(run, events); err != nil {
				slog.Debug("[hub] historical usage backfill unavailable", "run_id", run.ID, "error", err)
			}
		}
	}()
}

func boundedTransportReason(err error) string {
	if err == nil {
		return "unknown transport closure"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "unknown transport closure"
	}
	const maxLength = 240
	if len(message) > maxLength {
		message = message[:maxLength] + "..."
	}
	return fmt.Sprintf("%s", message)
}

func (s *Server) bridgeReadTimeout() time.Duration {
	return resilientWebSocketReadTimeout(s.cfg.Hub.BridgeReadTimeout.Duration, s.cfg.Hub.HeartbeatInterval.Duration)
}

func (s *Server) websocketPingInterval() time.Duration {
	interval := s.cfg.Hub.HeartbeatInterval.Duration
	if interval <= 0 {
		interval = 15 * time.Second
	}
	return interval
}

func (s *Server) bridgeReconnectGrace() time.Duration {
	minDelay := s.cfg.Bridge.ReconnectMin.Duration
	if minDelay <= 0 {
		minDelay = 5 * time.Second
	}
	maxDelay := s.cfg.Bridge.ReconnectMax.Duration
	if maxDelay < minDelay {
		maxDelay = minDelay
	}
	// Client reconnects use exponential backoff with up to 50% jitter. Keep
	// active runs alive long enough for one full max-delay attempt plus the
	// websocket heartbeat/read-deadline detection window. A short transport
	// flap must not be persisted as a failed orchestration before the Bridge
	// has a chance to reconnect and flush its buffered events.
	heartbeat := s.cfg.Hub.HeartbeatInterval.Duration
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	return maxDelay + maxDelay/2 + heartbeat + time.Second
}

func resilientWebSocketReadTimeout(configured, heartbeat time.Duration) time.Duration {
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	timeout := configured
	if heartbeatWindow := 6 * heartbeat; timeout < heartbeatWindow {
		timeout = heartbeatWindow
	}
	if timeout < 90*time.Second {
		timeout = 90 * time.Second
	}
	return timeout
}

func (s *Server) scheduleAgentRunFailure(agentID, instance string, delay time.Duration, disconnectReason string) {
	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}
		if delay > 0 && s.pool.AgentOnline(agentID) {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		reason := strings.TrimSpace(disconnectReason)
		if reason == "" {
			reason = "bridge disconnected while run was active"
		}
		if delay == 0 {
			reason = "bridge process restarted while run was active"
		} else {
			reason = fmt.Sprintf("Bridge did not reconnect within %s after transport loss: %s", delay, reason)
		}
		if n, err := s.store.MarkActiveRunsForAgentFailed(ctx, agentID, reason); err != nil {
			slog.Error("[hub] mark active agent runs failed", "agent_id", agentID, "error", err)
		} else if n > 0 {
			slog.Warn("[hub] marked active runs failed", "agent_id", agentID, "instance", instance, "count", n, "reason", reason)
		}
		runs, err := s.store.MarkActiveOrchestrationRunsForAgentFailed(ctx, agentID, reason)
		if err != nil {
			slog.Error("[hub] mark active orchestration runs failed", "agent_id", agentID, "error", err)
			return
		}
		for _, run := range runs {
			events, err := s.store.ListOrchestrationEvents(ctx, run.ID, 10000)
			if err != nil {
				slog.Error("[hub] list failed orchestration events failed", "run_id", run.ID, "error", err)
				continue
			}
			for i := len(events) - 1; i >= 0; i-- {
				if events[i].Kind == "run.error" {
					s.pool.BroadcastToOrchestrationBrowsers(run.ID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(events[i])))
					break
				}
			}
		}
		if len(runs) > 0 {
			slog.Warn("[hub] marked active orchestration runs failed", "agent_id", agentID, "instance", instance, "count", len(runs), "reason", reason)
		}
	}()
}

func (s *Server) handleBridgeEnvelope(ctx context.Context, agentID string, env protocol.Envelope) {
	if bridgeChatFrame(env) && !s.bridgeOwnsSession(ctx, agentID, env.Sid) {
		return
	}
	switch env.Type {
	case protocol.TypeHeartbeat:
		payload, err := protocol.Decode[protocol.HeartbeatPayload](env)
		if err == nil && payload.WorkingDirs != nil {
			_ = s.store.TouchAgentWorkingDirs(ctx, agentID, payload.WorkingDirs)
			return
		}
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
	case protocol.TypeApprovalRequest:
		payload, err := protocol.Decode[protocol.ApprovalRequestPayload](env)
		// Chat approvals also carry a run id. The envelope sid is the unambiguous
		// routing boundary: only sid-less requests belong to orchestration.
		if err == nil && env.Sid == "" && payload.RunID != "" {
			s.pool.BroadcastToOrchestrationBrowsers(payload.RunID, protocol.MustEnvelope(protocol.TypeApprovalRequest, "", payload))
			return
		}
		s.pool.BroadcastToBrowsers(env.Sid, env)
	case protocol.TypeOrchestrationEvent:
		s.handleOrchestrationEventFromAgent(ctx, agentID, env)
	case protocol.TypeOrchestrationUsageSyncResult:
		s.handleOrchestrationUsageSyncResult(ctx, agentID, env)
	case protocol.TypeCLIConfigResult:
		s.handleCLIConfigResult(agentID, env)
	case protocol.TypeError:
		s.handleBridgeError(ctx, env)
		s.pool.BroadcastToBrowsers(env.Sid, env)
	default:
		s.pool.BroadcastToBrowsers(env.Sid, env)
	}
}

func bridgeChatFrame(env protocol.Envelope) bool {
	if env.Sid == "" {
		return false
	}
	switch env.Type {
	case protocol.TypeSessionOpened,
		protocol.TypeSessionUpdate,
		protocol.TypePromptComplete,
		protocol.TypeApprovalRequest,
		protocol.TypeError:
		return true
	default:
		return false
	}
}

func (s *Server) bridgeOwnsSession(ctx context.Context, agentID, sid string) bool {
	s.ownersMu.RLock()
	owner, cached := s.owners[sid]
	s.ownersMu.RUnlock()
	if cached {
		return owner == agentID
	}
	session, err := s.store.SessionByIDAnyUser(ctx, sid)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			slog.Error("[hub] validate bridge session failed", "agent_id", agentID, "sid", sid, "error", err)
		}
		return false
	}
	if session.AgentID != agentID {
		slog.Warn("[hub] rejected bridge frame for foreign session", "agent_id", agentID, "session_agent_id", session.AgentID, "sid", sid)
		return false
	}
	s.ownersMu.Lock()
	s.owners[sid] = session.AgentID
	s.ownersMu.Unlock()
	return true
}

func (s *Server) forgetSessionOwner(sid string) {
	s.ownersMu.Lock()
	delete(s.owners, sid)
	s.ownersMu.Unlock()
}

func (s *Server) blockSessionOwner(sid string) {
	s.ownersMu.Lock()
	s.owners[sid] = ""
	s.ownersMu.Unlock()
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
	if strings.TrimSpace(content) == "" {
		const message = "runner completed without an assistant response"
		if payload.RunID != "" {
			if err := s.store.UpdateRunStatus(ctx, payload.RunID, store.RunFailed, message, ""); err != nil {
				slog.Error("[hub] update empty run failed", "run_id", payload.RunID, "error", err)
			}
		}
		s.pool.BroadcastToBrowsers(env.Sid, protocol.MustEnvelope(protocol.TypeError, env.Sid, protocol.ErrorPayload{
			Code:     "EMPTY_RESPONSE",
			Message:  message,
			RunID:    payload.RunID,
			PromptID: payload.PromptID,
		}))
		return
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

func (s *Server) currentAssistantBuffer(sid string) string {
	s.buffersMu.Lock()
	defer s.buffersMu.Unlock()
	return s.buffers[sid]
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
