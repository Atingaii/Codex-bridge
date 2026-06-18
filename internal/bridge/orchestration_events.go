package bridge

import (
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

func runConclusionForStatus(status, detail string, history []orchestrationTurn) *protocol.RunConclusion {
	outcome := "blocked"
	switch status {
	case store.OrchestrationCompleted:
		outcome = "satisfied"
	case store.OrchestrationCanceled:
		outcome = "canceled"
	case store.OrchestrationFailed:
		outcome = "errored"
	}
	summary := strings.TrimSpace(detail)
	if summary == "" {
		summary = relayTerminalContent(history)
	}
	if summary == "" {
		summary = "Orchestration ended without a final CLI response."
	}
	conclusion := &protocol.RunConclusion{
		Outcome:              outcome,
		Summary:              summary,
		BuildOrAuditCommands: conclusionCommands(history),
		EvidenceRefs:         conclusionEvidenceRefs(history),
	}
	if outcome != "satisfied" {
		if detail != "" {
			conclusion.UnmetObligations = []string{detail}
		} else {
			conclusion.UnmetObligations = []string{"The orchestration did not complete successfully."}
		}
	}
	return conclusion
}

func conclusionCommands(history []orchestrationTurn) []string {
	seen := make(map[string]bool)
	var out []string
	for _, turn := range history {
		for _, tool := range turn.Tools {
			command := strings.TrimSpace(tool.Command)
			if command == "" || seen[command] {
				continue
			}
			seen[command] = true
			out = append(out, command)
			if len(out) >= 12 {
				return out
			}
		}
	}
	return out
}

func conclusionEvidenceRefs(history []orchestrationTurn) []string {
	seen := make(map[string]bool)
	var out []string
	pattern := regexp.MustCompile(`(?:^|[\s:])((?:\.{0,2}/|/)?[A-Za-z0-9._/-]+\.(?:log|txt|json|md|thy|v|` + "le" + `an|out))`)
	for _, turn := range history {
		for _, match := range pattern.FindAllStringSubmatch(turn.Content, -1) {
			ref := strings.Trim(strings.TrimSpace(match[1]), ".,;:)")
			if ref == "" || seen[ref] {
				continue
			}
			seen[ref] = true
			out = append(out, ref)
			if len(out) >= 12 {
				return out
			}
		}
	}
	return out
}

func (m *OrchestrationManager) emitTool(runID, turnID, role, cli string, tool *RunnerToolEvent) {
	if tool != nil {
		tool.Command = redactSensitiveText(stripANSI(tool.Command))
		tool.Output = redactSensitiveText(stripANSI(tool.Output))
	}
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
	if tool.WillSuppressOnFailure {
		data["willSuppressOnFailure"] = true
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
	commandData := &protocol.CommandData{
		ID:                    tool.ID,
		Status:                tool.Status,
		Command:               tool.Command,
		Output:                tool.Output,
		ExitCode:              tool.ExitCode,
		StartedAt:             unixOrZero(tool.StartedAt),
		CompletedAt:           unixOrZero(tool.CompletedAt),
		WillSuppressOnFailure: tool.WillSuppressOnFailure,
	}
	if !tool.StartedAt.IsZero() && !tool.CompletedAt.IsZero() {
		commandData.DurationMs = tool.CompletedAt.Sub(tool.StartedAt).Milliseconds()
	}
	m.emit(runID, protocol.OrchestrationEventPayload{
		Kind:        kind,
		TurnID:      turnID,
		Role:        role,
		CLI:         cli,
		Status:      tool.Status,
		CommandData: commandData,
		Data:        data,
	})
}

func unixOrZero(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.Unix()
}

func (m *OrchestrationManager) emit(runID string, event protocol.OrchestrationEventPayload) {
	event.RunID = runID
	sourceProvided := strings.TrimSpace(event.Source) != ""
	if event.Severity == "" {
		event.Severity = severityFromLegacyStatus(event.Status)
		// turn.end carries a semantic lifecycle status (success/error/warning),
		// not a log level; the hub's claude_started error guard and the public
		// timeline both need it preserved.
		if event.Severity != "" && event.Kind != "turn.end" {
			event.Status = ""
		}
	}
	event.Source = normalizeEventSource(event.Source, event.Kind)
	if !sourceProvided && event.Severity != "" {
		event.Source = "bridge"
	}
	if event.Kind == "command.cancelled" && event.CommandData == nil {
		event.Kind = "command.end"
	}
	if event.Kind == "run.end" || event.Kind == "run.error" || event.Kind == "run.cancelled" {
		m.emitConclusionIfNeeded(runID, event)
	}
	m.send(protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", event))
}

func (m *OrchestrationManager) emitConclusionIfNeeded(runID string, terminal protocol.OrchestrationEventPayload) {
	conclusion := terminal.RunConclusion
	if conclusion == nil {
		conclusion = runConclusionFromTerminalEvent(terminal)
	}
	if conclusion == nil {
		return
	}
	m.mu.Lock()
	if m.conclusions[runID] {
		m.mu.Unlock()
		return
	}
	m.conclusions[runID] = true
	m.mu.Unlock()
	m.send(protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID:         runID,
		Kind:          "run.conclusion",
		Source:        "bridge",
		Role:          "summary",
		CLI:           terminal.CLI,
		TurnID:        terminal.TurnID,
		Content:       conclusion.Summary,
		Status:        terminal.Status,
		Error:         terminal.Error,
		RunConclusion: conclusion,
	}))
}

func runConclusionFromTerminalEvent(event protocol.OrchestrationEventPayload) *protocol.RunConclusion {
	status := event.Status
	if status == "" {
		switch event.Kind {
		case "run.end":
			status = store.OrchestrationCompleted
		case "run.cancelled":
			status = store.OrchestrationCanceled
		default:
			status = store.OrchestrationFailed
		}
	}
	return runConclusionForStatus(status, firstNonEmpty(event.Content, event.Error), nil)
}

func normalizeEventSource(source, kind string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "cli", "bridge", "user":
		return strings.ToLower(strings.TrimSpace(source))
	}
	switch kind {
	case "user.message":
		return "user"
	case "run.start", "run.end", "run.error", "run.canceling", "run.cancelled", "run.conclusion", "turn.start":
		return "bridge"
	default:
		return "cli"
	}
}

func severityFromLegacyStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "info", "warning", "error":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return ""
	}
}

// orchestrationSendTimeout bounds how long an orchestration event may wait for
// space in the outbound write queue before falling back to the pending buffer.
// A queue that stays full this long means the hub connection is dead or wedged.
const orchestrationSendTimeout = 3 * time.Second

func (m *OrchestrationManager) send(env protocol.Envelope) {
	m.mu.Lock()
	out := m.output
	if out == nil {
		buffered := m.bufferPendingLocked(env)
		m.mu.Unlock()
		if buffered {
			slog.Warn("[bridge] orchestration event buffered: bridge disconnected", "type", env.Type)
		} else {
			slog.Warn("[bridge] orchestration event dropped: bridge disconnected", "type", env.Type)
		}
		return
	}
	m.mu.Unlock()
	if env.Type != protocol.TypeOrchestrationEvent {
		send(out, env)
		return
	}
	// Orchestration events must not be silently dropped on a full queue:
	// losing a terminal run.end/run.cancelled wedges the hub-side run forever.
	// Apply bounded backpressure; if the connection was replaced while we
	// waited, retry against the fresh channel, otherwise buffer for the next
	// reconnect flush.
	for {
		select {
		case out <- env:
			return
		default:
		}
		timer := time.NewTimer(orchestrationSendTimeout)
		select {
		case out <- env:
			timer.Stop()
			return
		case <-timer.C:
		}
		m.mu.Lock()
		current := m.output
		if current == nil || current == out {
			m.bufferPendingLocked(env)
			m.mu.Unlock()
			slog.Warn("[bridge] orchestration event buffered: outbound queue saturated", "type", env.Type)
			return
		}
		out = current
		m.mu.Unlock()
	}
}

func (m *OrchestrationManager) bufferPendingLocked(env protocol.Envelope) bool {
	if env.Type != protocol.TypeOrchestrationEvent {
		return false
	}
	m.pending = append(m.pending, env)
	if len(m.pending) > 1000 {
		m.pending = m.pending[len(m.pending)-1000:]
	}
	return true
}
