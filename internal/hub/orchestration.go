package hub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

type orchestrationCreateRequest struct {
	AgentID  string                       `json:"agentId"`
	Title    string                       `json:"title"`
	Mode     string                       `json:"mode"`
	Prompt   string                       `json:"prompt"`
	CWD      string                       `json:"cwd"`
	MaxTurns int                          `json:"maxTurns"`
	Files    []protocol.AttachmentPayload `json:"files"`
}

type orchestrationStartRequest struct {
	AgentID  string
	Title    string
	Mode     string
	Prompt   string
	CWD      string
	MaxTurns int
	Files    []protocol.AttachmentPayload
}

func (s *Server) handleListOrchestrations(w http.ResponseWriter, r *http.Request, uid string) {
	runs, err := s.store.ListOrchestrationRuns(r.Context(), uid, 100)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to list orchestration runs")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

func (s *Server) handleCreateOrchestration(w http.ResponseWriter, r *http.Request, uid string) {
	var req orchestrationCreateRequest
	maxBytes := s.cfg.Hub.MaxPromptBytes + s.cfg.Hub.MaxAttachmentBytes*16 + 128*1024
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes)).Decode(&req); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid orchestration payload")
		return
	}
	startReq := orchestrationStartRequest{
		AgentID:  req.AgentID,
		Title:    req.Title,
		Mode:     req.Mode,
		Prompt:   req.Prompt,
		CWD:      req.CWD,
		MaxTurns: req.MaxTurns,
		Files:    req.Files,
	}
	normalized, ok := s.normalizeOrchestrationStart(w, startReq)
	if !ok {
		return
	}
	agentID, err := s.resolveAgentID(r.Context(), uid, normalized.AgentID)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusBadRequest
		}
		serverutil.WriteError(w, status, "BAD_AGENT", err.Error())
		return
	}
	if err := s.validateOrchestrationCapabilities(agentID); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", err.Error())
		return
	}
	files := orchestrationFileMeta(normalized.Files)
	run, err := s.store.CreateOrchestrationRun(r.Context(), store.CreateOrchestrationRunParams{
		UserID:   uid,
		AgentID:  agentID,
		Title:    normalized.Title,
		Mode:     normalized.Mode,
		Prompt:   normalized.Prompt,
		CWD:      normalized.CWD,
		MaxTurns: normalized.MaxTurns,
		Files:    files,
	})
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to create orchestration run")
		return
	}
	if err := s.startOrchestration(r.Context(), run, normalized, nil, false); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "AGENT_OFFLINE", err.Error())
		return
	}
	run.Status = store.OrchestrationRunning
	serverutil.WriteJSON(w, http.StatusCreated, map[string]any{"run": run})
}

func (s *Server) handleContinueOrchestration(w http.ResponseWriter, r *http.Request, uid string) {
	runID := r.PathValue("runID")
	run, err := s.store.OrchestrationRunByID(r.Context(), runID, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration run")
		return
	}
	if !orchestrationTerminalStatus(run.Status) {
		serverutil.WriteError(w, http.StatusConflict, "RUN_ACTIVE", "orchestration run is still active")
		return
	}
	var req orchestrationCreateRequest
	maxBytes := s.cfg.Hub.MaxPromptBytes + s.cfg.Hub.MaxAttachmentBytes*16 + 128*1024
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes)).Decode(&req); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "invalid orchestration payload")
		return
	}
	startReq := orchestrationStartRequest{
		AgentID:  req.AgentID,
		Title:    req.Title,
		Mode:     req.Mode,
		Prompt:   req.Prompt,
		CWD:      req.CWD,
		MaxTurns: req.MaxTurns,
		Files:    req.Files,
	}
	if startReq.AgentID == "" {
		startReq.AgentID = run.AgentID
	}
	if startReq.Mode == "" {
		startReq.Mode = run.Mode
	}
	if startReq.CWD == "" {
		startReq.CWD = run.CWD
	}
	if startReq.MaxTurns <= 0 {
		startReq.MaxTurns = run.MaxTurns
	}
	normalized, ok := s.normalizeOrchestrationStart(w, startReq)
	if !ok {
		return
	}
	agentID, err := s.resolveAgentID(r.Context(), uid, normalized.AgentID)
	if err != nil {
		status := http.StatusConflict
		if errors.Is(err, store.ErrNotFound) {
			status = http.StatusBadRequest
		}
		serverutil.WriteError(w, status, "BAD_AGENT", err.Error())
		return
	}
	if err := s.validateOrchestrationCapabilities(agentID); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "ORCHESTRATION_CAPABILITY_UNAVAILABLE", err.Error())
		return
	}
	normalized.AgentID = agentID
	files := orchestrationFileMeta(normalized.Files)

	events, err := s.store.ListOrchestrationEvents(r.Context(), run.ID, 1000)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration context")
		return
	}
	contextSummary := compactOrchestrationContext(run, events)
	if err := s.store.UpdateOrchestrationRunSettings(r.Context(), run.ID, agentID, normalized.Mode, normalized.CWD, normalized.MaxTurns, files); err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to update orchestration run")
		return
	}
	run.AgentID = agentID
	run.Mode = normalized.Mode
	run.CWD = normalized.CWD
	run.MaxTurns = normalized.MaxTurns
	run.Files = files
	if err := s.startOrchestration(r.Context(), run, normalized, []string{contextSummary}, true); err != nil {
		serverutil.WriteError(w, http.StatusConflict, "AGENT_OFFLINE", err.Error())
		return
	}
	run.Status = store.OrchestrationRunning
	run.Error = ""
	run.FinishedAt = 0
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (s *Server) normalizeOrchestrationStart(w http.ResponseWriter, req orchestrationStartRequest) (orchestrationStartRequest, bool) {
	req.Prompt = strings.TrimSpace(req.Prompt)
	if req.Prompt == "" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "prompt is required")
		return req, false
	}
	if int64(len(req.Prompt)) > s.cfg.Hub.MaxPromptBytes {
		serverutil.WriteError(w, http.StatusBadRequest, "PROMPT_TOO_LARGE", "prompt is too large")
		return req, false
	}
	if req.Mode == "" {
		req.Mode = "collaboration"
	}
	if req.Mode != "collaboration" && req.Mode != "debate" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_MODE", "mode must be collaboration or debate")
		return req, false
	}
	if req.MaxTurns <= 0 {
		req.MaxTurns = 4
	}
	if req.MaxTurns > 12 {
		req.MaxTurns = 12
	}
	if err := s.validateOrchestrationFiles(req.Files); err != nil {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_FILE", err.Error())
		return req, false
	}
	return req, true
}

func (s *Server) startOrchestration(ctx context.Context, run store.OrchestrationRun, req orchestrationStartRequest, contextParts []string, resume bool) error {
	event, err := s.store.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
		RunID:   run.ID,
		Kind:    "user.message",
		Role:    "user",
		Content: req.Prompt,
		Status:  store.OrchestrationQueued,
		Data:    orchestrationUserMessageData(req.Files),
	})
	if err != nil {
		return err
	}
	s.pool.BroadcastToOrchestrationBrowsers(run.ID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(event)))
	if err := s.store.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationRunning, ""); err != nil {
		slog.Error("[hub] update orchestration status failed", "run_id", run.ID, "error", err)
	}
	payload := protocol.OrchestrationStartPayload{
		RunID:     run.ID,
		Mode:      req.Mode,
		Prompt:    req.Prompt,
		Context:   strings.Join(cleanContextParts(contextParts), "\n\n"),
		Resume:    resume,
		PromptSeq: event.Seq,
		MaxTurns:  req.MaxTurns,
		CWD:       req.CWD,
		Files:     req.Files,
	}
	if err := s.pool.SendToAgent(run.AgentID, protocol.MustEnvelope(protocol.TypeOrchestrationStart, "", payload)); err != nil {
		_ = s.store.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationFailed, err.Error())
		_, _ = s.store.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
			RunID:  run.ID,
			Kind:   "run.error",
			Status: store.OrchestrationFailed,
			Error:  err.Error(),
		})
		return err
	}
	return nil
}

func (s *Server) validateOrchestrationCapabilities(agentID string) error {
	if agentID == "" {
		return errors.New("agent id is required")
	}
	caps, ok := s.pool.AgentCapabilities(agentID)
	if !ok {
		return errors.New("selected CLI endpoint is offline or did not advertise orchestration approval capabilities")
	}
	missingCLI := missingOrchestrationCLIs(caps)
	if len(missingCLI) > 0 {
		return fmt.Errorf("selected CLI endpoint cannot execute %s; reconnect the endpoint after installing the missing CLI commands or fixing its service PATH", strings.Join(missingCLI, " and "))
	}
	if strings.EqualFold(caps.ApprovalPolicy, "never") && strings.EqualFold(caps.Sandbox, "danger-full-access") {
		return nil
	}
	if strings.EqualFold(caps.Metadata["approvalMode"], permissionProfileAutoExecute) {
		return nil
	}
	missing := missingOrchestrationBrowserApproval(caps)
	if len(missing) > 0 {
		return fmt.Errorf("review-required orchestration needs browser approval for %s; reconnect the endpoint with a review-required bridge that supports app-server orchestration", strings.Join(missing, " and "))
	}
	return nil
}

func missingOrchestrationCLIs(caps *protocol.BridgeCapabilities) []string {
	if caps == nil {
		return []string{"Claude", "Codex"}
	}
	var missing []string
	for _, cli := range []string{"claude", "codex"} {
		capability, ok := caps.Orchestration[cli]
		if !ok || !capability.Available {
			missing = append(missing, cliDisplayName(cli))
		}
	}
	return missing
}

func missingOrchestrationBrowserApproval(caps *protocol.BridgeCapabilities) []string {
	if caps == nil {
		return []string{"Claude", "Codex"}
	}
	var missing []string
	for _, cli := range []string{"claude", "codex"} {
		capability, ok := caps.Orchestration[cli]
		if !ok || !capability.Available || !capability.BrowserApproval {
			missing = append(missing, cliDisplayName(cli))
		}
	}
	return missing
}

func cliDisplayName(cli string) string {
	switch strings.ToLower(cli) {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	default:
		return cli
	}
}

func (s *Server) handleGetOrchestration(w http.ResponseWriter, r *http.Request, uid string) {
	run, err := s.store.OrchestrationRunByID(r.Context(), r.PathValue("runID"), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration run")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"run": run})
}

func (s *Server) handleOrchestrationEvents(w http.ResponseWriter, r *http.Request, uid string) {
	runID := r.PathValue("runID")
	_, err := s.store.OrchestrationRunByID(r.Context(), runID, uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration run")
		return
	}
	events, err := s.store.ListOrchestrationEvents(r.Context(), runID, 1000)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to list orchestration events")
		return
	}
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"events": events})
}

func (s *Server) handleCancelOrchestration(w http.ResponseWriter, r *http.Request, uid string) {
	run, err := s.store.OrchestrationRunByID(r.Context(), r.PathValue("runID"), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration run")
		return
	}
	if orchestrationTerminalStatus(run.Status) || run.Status == store.OrchestrationCanceling {
		serverutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "status": run.Status})
		return
	}
	if !orchestrationCancelableStatus(run.Status) {
		serverutil.WriteError(w, http.StatusConflict, "NOT_CANCELABLE", "orchestration run is not cancelable")
		return
	}
	if err := s.store.UpdateOrchestrationRunStatus(r.Context(), run.ID, store.OrchestrationCanceling, ""); err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to update orchestration run")
		return
	}
	event, err := s.store.AddOrchestrationEvent(r.Context(), store.OrchestrationEvent{
		RunID:  run.ID,
		Kind:   "run.canceling",
		Status: store.OrchestrationCanceling,
	})
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to persist orchestration event")
		return
	}
	s.pool.BroadcastToOrchestrationBrowsers(run.ID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(event)))
	_ = s.pool.SendToAgent(run.AgentID, protocol.MustEnvelope(protocol.TypeOrchestrationCancel, "", protocol.OrchestrationCancelPayload{RunID: run.ID}))
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "status": store.OrchestrationCanceling})
}

func orchestrationCancelableStatus(status string) bool {
	return status == store.OrchestrationQueued || status == store.OrchestrationRunning
}

func orchestrationTerminalStatus(status string) bool {
	return status == store.OrchestrationCompleted || status == store.OrchestrationFailed || status == store.OrchestrationCanceled
}

func cleanContextParts(parts []string) []string {
	clean := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			clean = append(clean, part)
		}
	}
	return clean
}

func compactOrchestrationContext(run store.OrchestrationRun, events []store.OrchestrationEvent) string {
	events = mergeOrchestrationDeltasForContext(events)
	var userMessages []string
	var turnNotes []string
	var commands []string
	var outcomes []string
	var blockers []string
	for _, event := range events {
		switch {
		case event.Kind == "user.message":
			userMessages = append(userMessages, trimForContext(event.Content, 900))
		case event.Kind == "turn.end":
			if event.Status != "" || event.Error != "" {
				outcomes = append(outcomes, formatOrchestrationActor(event)+": "+trimForContext(joinNonEmpty(event.Status, event.Error), 300))
			}
		case event.Kind == "turn.delta" && strings.TrimSpace(event.Content) != "":
			turnNotes = append(turnNotes, formatOrchestrationActor(event)+": "+trimForContext(event.Content, 900))
		case event.Kind == "command.end" || event.Kind == "command.start":
			command := stringFromMap(event.Data, "command")
			if command == "" {
				continue
			}
			status := stringFromMap(event.Data, "status")
			if status == "" {
				status = event.Status
			}
			output := trimForContext(stringFromMap(event.Data, "output"), 300)
			commands = append(commands, trimForContext(joinNonEmpty(command, status, output), 700))
		case event.Kind == "run.error" || event.Kind == "run.cancelled":
			blockers = append(blockers, trimForContext(joinNonEmpty(event.Error, event.Content, event.Status), 600))
		}
	}

	var b strings.Builder
	b.WriteString("Compacted orchestration context from previous work.\n")
	b.WriteString("Carry this state forward for continuity. Prefer the latest user task when it conflicts with older details.\n\n")
	b.WriteString("Run state:\n")
	b.WriteString(fmt.Sprintf("- Run ID: %s\n", run.ID))
	b.WriteString(fmt.Sprintf("- Mode: %s\n", run.Mode))
	if run.CWD != "" {
		b.WriteString(fmt.Sprintf("- Working directory: %s\n", run.CWD))
	}
	if run.Status != "" {
		b.WriteString(fmt.Sprintf("- Previous status: %s\n", run.Status))
	}
	writeContextSection(&b, "User goals so far", lastN(userMessages, 8))
	writeContextSection(&b, "Recent agent outputs", lastN(turnNotes, 10))
	writeContextSection(&b, "Tool outcomes and commands", lastN(commands, 12))
	writeContextSection(&b, "Run outcomes", lastN(outcomes, 8))
	writeContextSection(&b, "Unresolved blockers or errors", lastN(blockers, 6))
	return trimForContext(b.String(), 14000)
}

func mergeOrchestrationDeltasForContext(events []store.OrchestrationEvent) []store.OrchestrationEvent {
	merged := make([]store.OrchestrationEvent, 0, len(events))
	deltaIndexes := make(map[string]int)
	for _, event := range events {
		if event.Kind != "turn.delta" {
			merged = append(merged, event)
			continue
		}
		content := strings.TrimSpace(event.Content)
		if content == "" {
			continue
		}
		key := contextDeltaKey(event)
		index, ok := deltaIndexes[key]
		if !ok {
			event.Content = content
			deltaIndexes[key] = len(merged)
			merged = append(merged, event)
			continue
		}
		previous := merged[index]
		previous.Content = mergeContextDeltaContent(previous.Content, content)
		if previous.Status == "" {
			previous.Status = event.Status
		}
		if previous.Error == "" {
			previous.Error = event.Error
		}
		merged[index] = previous
	}
	return merged
}

func contextDeltaKey(event store.OrchestrationEvent) string {
	return strings.Join([]string{event.RunID, event.TurnID, event.Role, event.CLI}, "\x1f")
}

func mergeContextDeltaContent(previous, next string) string {
	if previous == "" {
		return next
	}
	if next == "" || strings.HasSuffix(previous, next) {
		return previous
	}
	if strings.HasPrefix(next, previous) {
		return next
	}
	return previous + next
}

func writeContextSection(b *strings.Builder, title string, items []string) {
	if len(items) == 0 {
		return
	}
	b.WriteString("\n")
	b.WriteString(title)
	b.WriteString(":\n")
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(strings.ReplaceAll(item, "\n", "\n  "))
		b.WriteByte('\n')
	}
}

func formatOrchestrationActor(event store.OrchestrationEvent) string {
	parts := []string{}
	if event.Role != "" {
		parts = append(parts, event.Role)
	}
	if event.CLI != "" {
		parts = append(parts, event.CLI)
	}
	if event.TurnID != "" {
		parts = append(parts, event.TurnID)
	}
	if len(parts) == 0 {
		return event.Kind
	}
	return strings.Join(parts, " via ")
}

func joinNonEmpty(values ...string) string {
	var parts []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, " | ")
}

func stringFromMap(data map[string]any, key string) string {
	if data == nil {
		return ""
	}
	value, _ := data[key].(string)
	return value
}

func lastN(values []string, n int) []string {
	if len(values) <= n {
		return values
	}
	return values[len(values)-n:]
}

func trimForContext(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	if max <= len("\n[truncated]") {
		return value[:max]
	}
	return value[:max-len("\n[truncated]")] + "\n[truncated]"
}

func (s *Server) handleOrchestrationWS(w http.ResponseWriter, r *http.Request, uid string) {
	runID := r.URL.Query().Get("runId")
	if runID == "" {
		serverutil.WriteError(w, http.StatusBadRequest, "BAD_REQUEST", "missing runId")
		return
	}
	if _, err := s.store.OrchestrationRunByID(r.Context(), runID, uid); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
			return
		}
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration run")
		return
	}
	upgrader := websocket.Upgrader{CheckOrigin: s.checkOrigin}
	ws, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer ws.Close()
	ws.SetReadLimit(64 * 1024)
	conn := NewBrowserConn(runID, ws, s.cfg.Hub.MaxBrowserSendQueue)
	s.pool.AddOrchestrationBrowser(runID, conn)
	go conn.WriteLoop()
	defer func() {
		s.pool.RemoveOrchestrationBrowser(runID, conn)
		conn.Close()
	}()
	_ = conn.Send(protocol.MustEnvelope(protocol.TypeStatus, "", map[string]any{"status": "connected", "runId": runID}))
	_ = ws.SetReadDeadline(time.Now().Add(s.browserReadTimeout()))
	ws.SetPongHandler(func(string) error {
		return ws.SetReadDeadline(time.Now().Add(s.browserReadTimeout()))
	})
	for {
		var env protocol.Envelope
		if err := ws.ReadJSON(&env); err != nil {
			return
		}
		_ = ws.SetReadDeadline(time.Now().Add(s.browserReadTimeout()))
		switch env.Type {
		case protocol.TypeHeartbeat:
			_ = conn.Send(protocol.MustEnvelope(protocol.TypeHeartbeat, "", map[string]any{"ts": time.Now().Unix()}))
		case protocol.TypeApprovalResponse:
			payload, err := protocol.Decode[protocol.ApprovalResponsePayload](env)
			decision := strings.ToLower(strings.TrimSpace(payload.Decision))
			if err != nil || payload.RequestID == "" || !validApprovalDecision(decision) {
				_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Code: "BAD_APPROVAL_RESPONSE", Message: "invalid approval response"}))
				continue
			}
			payload.Decision = decision
			latest, err := s.store.OrchestrationRunByID(r.Context(), runID, uid)
			if err != nil {
				_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Code: "RUN_NOT_FOUND", Message: "orchestration run not found"}))
				continue
			}
			if err := s.pool.SendToAgent(latest.AgentID, protocol.MustEnvelope(protocol.TypeApprovalResponse, "", payload)); err != nil {
				_ = conn.Send(protocol.MustEnvelope(protocol.TypeError, "", protocol.ErrorPayload{Code: "AGENT_OFFLINE", Message: err.Error()}))
			}
		}
	}
}

func validApprovalDecision(decision string) bool {
	return decision == "accept" || decision == "decline" || decision == "cancel"
}

func (s *Server) handleOrchestrationEvent(ctx context.Context, env protocol.Envelope) {
	payload, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
	if err != nil || payload.RunID == "" {
		return
	}
	if suppressEmptyPagesReadFailure(payload) {
		return
	}
	status := payload.Status
	runStatus := ""
	switch payload.Kind {
	case "run.start":
		runStatus = store.OrchestrationRunning
		if status == "" {
			status = runStatus
		}
	case "run.end":
		runStatus = store.OrchestrationCompleted
		status = runStatus
	case "run.error":
		runStatus = store.OrchestrationFailed
		status = runStatus
	case "run.cancelled":
		runStatus = store.OrchestrationCanceled
		status = runStatus
	case "run.canceling":
		runStatus = store.OrchestrationCanceling
		status = runStatus
	}
	if runStatus != "" {
		if err := s.store.UpdateOrchestrationRunStatus(ctx, payload.RunID, runStatus, payload.Error); err != nil {
			slog.Error("[hub] update orchestration status failed", "run_id", payload.RunID, "error", err)
		}
	}
	event, err := s.store.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
		RunID:   payload.RunID,
		Kind:    payload.Kind,
		Role:    payload.Role,
		CLI:     payload.CLI,
		TurnID:  payload.TurnID,
		Content: payload.Content,
		Status:  status,
		Error:   payload.Error,
		Data:    payload.Data,
	})
	if err != nil {
		slog.Error("[hub] persist orchestration event failed", "run_id", payload.RunID, "error", err)
		return
	}
	s.pool.BroadcastToOrchestrationBrowsers(payload.RunID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(event)))
}

func suppressEmptyPagesReadFailure(payload protocol.OrchestrationEventPayload) bool {
	if !strings.HasPrefix(payload.Kind, "command.") {
		return false
	}
	status := strings.ToLower(strings.TrimSpace(payload.Status))
	if status != "failed" && status != "error" {
		return false
	}
	output := strings.TrimSpace(payload.Error + "\n" + payload.Content)
	if payload.Data != nil {
		if value, ok := payload.Data["output"].(string); ok {
			output += "\n" + value
		}
	}
	return strings.Contains(output, `Invalid pages parameter: ""`) && strings.Contains(output, "Pages are 1-indexed")
}

func (s *Server) validateOrchestrationFiles(files []protocol.AttachmentPayload) error {
	if len(files) > 12 {
		return errors.New("at most 12 files can be uploaded")
	}
	maxBytes := s.cfg.Hub.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = 8 * 1024 * 1024
	}
	for _, file := range files {
		if strings.TrimSpace(file.Name) == "" {
			return errors.New("file name is required")
		}
		if file.Size <= 0 || file.Size > maxBytes {
			return errors.New("file is too large")
		}
		if strings.TrimSpace(file.Data) == "" {
			return errors.New("file data is missing")
		}
	}
	return nil
}

func orchestrationFileMeta(files []protocol.AttachmentPayload) []store.OrchestrationFile {
	metas := make([]store.OrchestrationFile, 0, len(files))
	for _, file := range files {
		metas = append(metas, store.OrchestrationFile{
			Name:     file.Name,
			MimeType: file.MimeType,
			Size:     file.Size,
		})
	}
	return metas
}

func orchestrationUserMessageData(files []protocol.AttachmentPayload) map[string]any {
	metas := orchestrationFileMeta(files)
	if len(metas) == 0 {
		return nil
	}
	return map[string]any{"files": metas}
}

func (s *Server) resolveAgentID(ctx context.Context, uid, requested string) (string, error) {
	if requested != "" {
		if _, err := s.visibleAgentByID(ctx, uid, requested); err != nil {
			return "", store.ErrNotFound
		}
		return requested, nil
	}
	agents, err := s.visibleAgents(ctx, uid)
	if err != nil {
		return "", err
	}
	for _, agent := range agents {
		if s.pool.AgentOnline(agent.ID) {
			return agent.ID, nil
		}
	}
	if len(agents) == 0 {
		return "", errors.New("no bridge agent has enrolled yet")
	}
	return agents[0].ID, nil
}

func eventToPayload(event store.OrchestrationEvent) protocol.OrchestrationEventPayload {
	return protocol.OrchestrationEventPayload{
		ID:        event.ID,
		RunID:     event.RunID,
		Seq:       event.Seq,
		TurnID:    event.TurnID,
		Kind:      event.Kind,
		Role:      event.Role,
		CLI:       event.CLI,
		Content:   event.Content,
		Status:    event.Status,
		Error:     event.Error,
		Data:      event.Data,
		CreatedAt: event.CreatedAt,
	}
}
