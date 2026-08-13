package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/tencent/codex-bridge/internal/bridge/profiles/registry"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

type orchestrationTurn struct {
	TurnID     string
	Role       string
	CLI        string
	WorkerSlot string
	Content    string
	Handoff    string
	Relay      orchestrationRelayPacket
	Err        string
	Tools      []RunnerToolEvent
	Usage      orchestrationUsage
}

func (m *OrchestrationManager) emitTurnUsage(runID string, record orchestrationTurn) {
	usage := record.Usage
	m.emit(runID, protocol.OrchestrationEventPayload{
		Kind:   "turn.usage",
		TurnID: record.TurnID,
		Role:   record.Role,
		CLI:    record.CLI,
		Data: map[string]any{
			"cli":              record.CLI,
			"model":            usage.Model,
			"inputTokens":      usage.InputTokens,
			"outputTokens":     usage.OutputTokens,
			"cacheReadTokens":  usage.CacheReadTokens,
			"cacheWriteTokens": usage.CacheWriteTokens,
			"estimatedCostUsd": usage.EstimatedCostUSD,
			"estimated":        usage.Estimated,
			"native":           usage.Native,
			"costKnown":        usage.CostKnown,
			"costSource":       usage.CostSource,
			"pricingModel":     usage.PricingModel,
		},
	})
}

type orchestrationRelayPacket struct {
	To         string
	Intent     string
	Need       string
	Status     string
	Changed    string
	Verified   string
	Next       string
	Risks      string
	Structured bool
}

type orchestrationTurnAssignment struct {
	Role       string
	CLI        string
	WorkerSlot string
}

type durableTaskPromptScope struct {
	Name      string
	Role      string
	Round     int
	MaxRounds int
}

func (scope *durableTaskPromptScope) finalReviewer() bool {
	return scope != nil && scope.Role == store.TaskRoleReviewer && scope.MaxRounds > 0 && scope.Round >= scope.MaxRounds
}

func (m *OrchestrationManager) runRelayCLI(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, cli, workerSlot, prompt string, state *orchestrationSessionState) (string, []RunnerToolEvent, error) {
	switch cli {
	case "claude":
		if state == nil {
			return m.runClaude(ctx, payload, turnID, role, prompt)
		}
		content, tools, resumeMode, err := m.runClaudeInteractive(ctx, payload, turnID, role, prompt, state)
		state.ClaudeResumeMode = resumeMode
		if err == nil {
			state.ClaudeSessionStarted = true
		}
		return content, tools, err
	default:
		if state == nil {
			return m.runCodex(ctx, payload, turnID, role, prompt)
		}
		workerSlot = normalizeCodexWorkerSlot(workerSlot)
		content, tools, threadID, resumeMode, err := m.runCodexInteractive(ctx, payload, turnID, role, workerSlot, prompt, state)
		state.setCodexResumeMode(workerSlot, resumeMode)
		if threadID != "" {
			state.setCodexThreadID(workerSlot, threadID)
		}
		return content, tools, err
	}
}

// runRelayCLIWithCapacityRetries keeps a transient provider-capacity failure
// inside the same visible turn. The retry has the same prompt, turn ID, and
// run-scoped workspace; it is not a continuation or a new orchestration run.
func (m *OrchestrationManager) runRelayCLIWithCapacityRetries(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, cli, workerSlot, prompt string, state *orchestrationSessionState) (string, []RunnerToolEvent, error) {
	waits := m.modelCapacityRetryWaits
	if len(waits) == 0 {
		waits = defaultModelCapacityRetryWaits
	}
	for retry := 0; ; retry++ {
		content, tools, err := m.runRelayCLI(ctx, payload, turnID, role, cli, workerSlot, prompt, state)
		if err == nil || !isModelCapacityError(err) {
			return content, tools, err
		}
		if ctx.Err() != nil {
			return content, tools, ctx.Err()
		}
		if retry >= len(waits) {
			message := visibleCLIError(err)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "error",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Content:  fmt.Sprintf("%s 模型容量持续不足，已完成 %d 次退避重试，无法继续此回合。", cliDisplay(cli), len(waits)),
				Error:    message,
				Data: map[string]any{
					"relayOnly": true,
					"category":  "model-capacity-retry-exhausted",
					"attempts":  len(waits),
					"error":     message,
				},
			})
			return content, tools, err
		}

		wait := waits[retry]
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:     "turn.delta",
			Source:   "bridge",
			Severity: "warning",
			TurnID:   turnID,
			Role:     role,
			CLI:      cli,
			Content:  fmt.Sprintf("%s 模型当前容量已满，将在 %s 后重试（第 %d/%d 次重试）。", cliDisplay(cli), humanRetryWait(wait), retry+1, len(waits)),
			Error:    visibleCLIError(err),
			Data: map[string]any{
				"relayOnly":         true,
				"category":          "model-capacity-retry-wait",
				"retry":             retry + 1,
				"maxRetries":        len(waits),
				"retryAfterSeconds": int(wait.Seconds()),
			},
		})
		// A capacity response can leave a native interactive process in an
		// unusable state. Recreate only that CLI session before retrying.
		m.resetNativeInteractiveSessionForContinuation(cli, workerSlot, state)
		if err := waitModelCapacityRetry(ctx, wait); err != nil {
			return content, tools, err
		}
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:     "turn.delta",
			Source:   "bridge",
			Severity: "info",
			TurnID:   turnID,
			Role:     role,
			CLI:      cli,
			Content:  fmt.Sprintf("正在重试 %s（第 %d/%d 次重试）。", cliDisplay(cli), retry+1, len(waits)),
			Data: map[string]any{
				"relayOnly":  true,
				"category":   "model-capacity-retry-start",
				"retry":      retry + 1,
				"maxRetries": len(waits),
			},
		})
	}
}

// releaseManagedCodexSessionAfterActiveWriter tears down only the app-server
// process launched and retained by this OrchestrationManager. It intentionally
// cannot terminate a user-owned Codex TUI or an unowned process merely because
// it happens to reference the same native thread.
func (m *OrchestrationManager) releaseManagedCodexSessionAfterActiveWriter(workerSlot string, state *orchestrationSessionState) {
	if state == nil || state.NativeSession == nil {
		return
	}
	session := state.NativeSession
	workerSlot = normalizeCodexWorkerSlot(workerSlot)
	session.mu.Lock()
	codex := session.codexSessionLocked(workerSlot)
	if codex != nil {
		if codex.threadID != "" {
			state.setCodexThreadID(workerSlot, codex.threadID)
		}
		session.setCodexSessionLocked(workerSlot, nil)
	}
	session.mu.Unlock()
	if codex == nil || codex.client == nil {
		return
	}
	codex.client.unsubscribeThreadWithTimeout(codex.threadID)
	// A normal close waits up to 30 seconds. Active-writer recovery needs a
	// bounded cleanup: after the unsubscribe grace period the managed process
	// group is terminated, allowing the next retry to create a clean client.
	codex.client.closeWithTimeout(appServerPrepareCloseTimeout)
}

// runRelayCLIWithSubmissionRetries handles errors raised before Codex accepts
// turn/start. Unlike stream recovery, the original prompt is safe and required
// here because an active writer rejection means no new turn was created.
func (m *OrchestrationManager) runRelayCLIWithSubmissionRetries(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, cli, workerSlot, prompt string, state *orchestrationSessionState) (string, []RunnerToolEvent, error) {
	waits := m.codexThreadBusyRetryWaits
	if len(waits) == 0 {
		waits = defaultCodexThreadBusyRetryWaits
	}
	for retry := 0; ; retry++ {
		content, tools, err := m.runRelayCLIWithCapacityRetries(ctx, payload, turnID, role, cli, workerSlot, prompt, state)
		if err == nil || !isCodexThreadActiveWriterError(cli, err) {
			return content, tools, err
		}
		if ctx.Err() != nil {
			return content, tools, ctx.Err()
		}
		if retry >= len(waits) {
			message := visibleCLIError(err)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "error",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Content:  fmt.Sprintf("Codex 原生会话持续被上一回合占用，已完成 %d 次退避重试，当前消息尚未提交。", len(waits)),
				Error:    message,
				Data: map[string]any{
					"relayOnly": true,
					"category":  "codex-thread-busy-retry-exhausted",
					"attempts":  len(waits),
					"error":     message,
				},
			})
			return content, tools, err
		}

		// A rejected turn/start did not accept the prompt, but the current Bridge
		// may still own an app-server subscription from an interrupted prior turn.
		// Release only that managed client before waiting.
		m.releaseManagedCodexSessionAfterActiveWriter(workerSlot, state)
		wait := waits[retry]
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:     "turn.delta",
			Source:   "bridge",
			Severity: "warning",
			TurnID:   turnID,
			Role:     role,
			CLI:      cli,
			Content:  fmt.Sprintf("Codex 原生会话的上一回合仍在收尾，将在 %s后重新提交当前消息（第 %d/%d 次重试）。", humanRetryWait(wait), retry+1, len(waits)),
			Error:    visibleCLIError(err),
			Data: map[string]any{
				"relayOnly":         true,
				"category":          "codex-thread-busy-retry-wait",
				"retry":             retry + 1,
				"maxRetries":        len(waits),
				"retryAfterSeconds": int(wait.Seconds()),
			},
		})
		if waitErr := waitModelCapacityRetry(ctx, wait); waitErr != nil {
			return content, tools, waitErr
		}
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:     "turn.delta",
			Source:   "bridge",
			Severity: "info",
			TurnID:   turnID,
			Role:     role,
			CLI:      cli,
			Content:  fmt.Sprintf("正在向同一 Codex 原生会话重新提交当前消息（第 %d/%d 次重试）。", retry+1, len(waits)),
			Data: map[string]any{
				"relayOnly":  true,
				"category":   "codex-thread-busy-retry-start",
				"retry":      retry + 1,
				"maxRetries": len(waits),
			},
		})
	}
}

func isModelCapacityError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(stripANSI(err.Error()))
	return strings.Contains(message, "selected model is at capacity") ||
		(strings.Contains(message, "model") && strings.Contains(message, "at capacity"))
}

func isCodexThreadActiveWriterError(cli string, err error) bool {
	if cli != "codex" || err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(stripANSI(err.Error()))
	return strings.Contains(message, "thread ") && strings.Contains(message, "already has an active writer")
}

func waitModelCapacityRetry(ctx context.Context, wait time.Duration) error {
	if wait <= 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			return nil
		}
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func humanRetryWait(wait time.Duration) string {
	if wait > 0 && wait%time.Minute == 0 {
		return fmt.Sprintf("%d 分钟", int(wait/time.Minute))
	}
	return fmt.Sprintf("%d 秒", int(wait.Round(time.Second)/time.Second))
}

func clearRelayResumeMode(cli, workerSlot string, state *orchestrationSessionState) {
	if state == nil {
		return
	}
	switch cli {
	case "codex":
		state.setCodexResumeMode(workerSlot, "")
	case "claude":
		state.ClaudeResumeMode = ""
	}
}

func relayTerminalContent(history []orchestrationTurn) string {
	if len(history) == 0 {
		return "Relay orchestration ended without a CLI response."
	}
	record := history[len(history)-1]
	content := strings.TrimSpace(record.Content)
	if content != "" {
		return content
	}
	if len(record.Tools) > 0 {
		return "CLI returned without a final text response. Command events are shown above."
	}
	if record.Err != "" {
		return "CLI process failed before returning a final text response.\n\nError: " + trimForPrompt(record.Err, 3000)
	}
	return "CLI returned without a final text response."
}

func visibleCLIError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(stripANSI(err.Error()))
	value = redactSensitiveText(value)
	if value == "" {
		return "unknown CLI process error"
	}
	return trimForPrompt(value, 3000)
}

func recoverableRelayCLIError(cli, content string, err error) bool {
	return cli == "codex" && strings.TrimSpace(content) != "" && isAppServerEmptyErrorAfterVisibleOutput(err)
}

func runnerNoticeOrchestrationEvent(turnID, role, cli string, notice *RunnerNotice) protocol.OrchestrationEventPayload {
	event := protocol.OrchestrationEventPayload{
		Kind:     "turn.delta",
		Source:   "bridge",
		TurnID:   turnID,
		Role:     role,
		CLI:      cli,
		Severity: "info",
		Data:     map[string]any{"relayOnly": true},
	}
	if notice == nil {
		return event
	}
	event.Content = notice.Content
	event.Error = notice.Error
	if notice.Severity != "" {
		event.Severity = notice.Severity
	}
	for key, value := range notice.Data {
		event.Data[key] = value
	}
	if notice.Category != "" {
		event.Data["category"] = notice.Category
	}
	return event
}

func isRecoverableCLITransportError(cli string, err error) bool {
	if cli != "codex" || err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(stripANSI(err.Error()))
	for _, marker := range []string{
		"reconnecting",
		"stream disconnected before completion",
		"stream closed before response.completed",
		"connection reset by peer",
		"transport closed before completion",
		"unexpected eof",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func (m *OrchestrationManager) resetCodexInteractiveSessionAfterRecoverableError(workerSlot string, state *orchestrationSessionState) {
	if state == nil || state.NativeSession == nil {
		return
	}
	session := state.NativeSession
	session.mu.Lock()
	defer session.mu.Unlock()
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
}

func relayTurnEndData(cli, workerSlot string, state orchestrationSessionState) map[string]any {
	data := map[string]any{"relayOnly": true}
	if workerSlot != "" {
		data["workerSlot"] = workerSlot
	}
	switch cli {
	case "codex":
		workerSlot = normalizeCodexWorkerSlot(workerSlot)
		if resumeMode := state.codexResumeMode(workerSlot); resumeMode != "" {
			data["resumeMode"] = resumeMode
		}
		if threadID := state.codexThreadID(workerSlot); threadID != "" {
			data["codexThreadId"] = threadID
		}
		if threadIDs := state.codexThreadIDsCopy(); len(threadIDs) > 0 {
			data["codexThreadIds"] = threadIDs
		}
	case "claude":
		if state.ClaudeResumeMode != "" {
			data["resumeMode"] = state.ClaudeResumeMode
		}
		if state.ClaudeSessionID != "" {
			data["sessionId"] = state.ClaudeSessionID
		}
	}
	return data
}

func (m *OrchestrationManager) relayRunEndData(cli, workerSlot, workerPair string, state orchestrationSessionState, cwd string) *protocol.RunEndData {
	data := &protocol.RunEndData{WorkerPair: protocol.NormalizeOrchestrationWorkerPair(workerPair)}
	switch cli {
	case "codex":
		workerSlot = normalizeCodexWorkerSlot(workerSlot)
		data.CodexThreadID = state.codexThreadID(workerSlot)
		data.CodexThreadIDs = state.codexThreadIDsCopy()
		data.CodexNativeResume = codexNativeResumeInfoForSlot(workerSlot, data.CodexThreadID, cwd)
	case "claude":
		data.ClaudeSessionID = state.ClaudeSessionID
		data.ClaudeNativeResume = m.claudeNativeResumeInfo(state.ClaudeSessionID, cwd)
	}
	data = runEndDataWithNativeResume(data, cwd)
	if data.CodexThreadID == "" && len(data.CodexThreadIDs) == 0 && data.ClaudeSessionID == "" {
		return nil
	}
	return data
}

func orchestrationTurnStartContent(cli, workerSlot string, state *orchestrationSessionState, turn, maxTurns int, role string) string {
	mode := ""
	if state != nil {
		mode = plannedRelayResumeMode(cli, workerSlot, *state)
	}
	label := cliDisplay(cli)
	if role != "" {
		label = cliDisplay(cli) + " " + role
	}
	if cli == "codex" && workerSlot != "" && normalizeCodexWorkerSlot(workerSlot) != orchestrationCodexDefaultSlot {
		label = label + " (" + normalizeCodexWorkerSlot(workerSlot) + ")"
	}
	if turn > 0 && maxTurns > 0 {
		label = fmt.Sprintf("%s turn %d/%d", label, turn, maxTurns)
	}
	switch {
	case cli == "codex" && mode == "codex-interactive-resume":
		return "Starting " + label + " in the saved native Codex thread."
	case cli == "codex" && mode == "codex-interactive-thread":
		return "Starting " + label + " in a native Codex thread."
	case cli == "codex" && mode == "codex-thread-resume":
		return "Starting " + label + " with the saved native thread when available."
	case cli == "claude" && mode == "claude-interactive-resume":
		return "Starting " + label + " in the saved native Claude session."
	case cli == "claude" && mode == "claude-interactive-session":
		return "Starting " + label + " in a native Claude session."
	case cli == "claude" && mode == "claude-resume":
		return "Starting " + label + " with the saved native session when available."
	case cli == "claude" && mode == "claude-new":
		return "Starting " + label + " with a deterministic native session id."
	case cli != "":
		return "Starting " + label + "."
	default:
		return "Starting orchestration turn."
	}
}

func cliDisplay(cli string) string {
	switch strings.ToLower(strings.TrimSpace(cli)) {
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	default:
		if strings.TrimSpace(cli) == "" {
			return "CLI"
		}
		return strings.TrimSpace(cli)
	}
}

func plannedRelayResumeMode(cli, workerSlot string, state orchestrationSessionState) string {
	switch cli {
	case "codex":
		workerSlot = normalizeCodexWorkerSlot(workerSlot)
		if resumeMode := state.codexResumeMode(workerSlot); resumeMode != "" {
			return resumeMode
		}
		if state.NativeSession != nil {
			state.NativeSession.mu.Lock()
			codex := state.NativeSession.codexSessionLocked(workerSlot)
			state.NativeSession.mu.Unlock()
			if codex != nil && codex.mode != "" {
				return codex.mode
			}
		}
		if state.NativeSession != nil {
			if state.codexThreadID(workerSlot) != "" {
				return "codex-interactive-resume"
			}
			return "codex-interactive-thread"
		}
		if state.codexThreadID(workerSlot) != "" {
			return "codex-thread-resume"
		}
		return "codex-fresh"
	case "claude":
		if state.ClaudeResumeMode != "" {
			return state.ClaudeResumeMode
		}
		if state.NativeSession != nil && state.NativeSession.claude != nil && state.NativeSession.claude.mode != "" {
			return state.NativeSession.claude.mode
		}
		if state.NativeSession != nil {
			if state.ClaudeSessionStarted {
				return "claude-interactive-resume"
			}
			return "claude-interactive-session"
		}
		if state.ClaudeSessionStarted {
			return "claude-resume"
		}
		return "claude-new"
	default:
		return ""
	}
}

func roleForTurnWithWorkerPair(mode, workerPair, firstCLI string, turn int) orchestrationTurnAssignment {
	if protocol.NormalizeOrchestrationWorkerPair(workerPair) == protocol.WorkerPairCodexCodex {
		if mode == "debate" {
			if turn%2 == 1 {
				return orchestrationTurnAssignment{Role: "proposer", CLI: "codex", WorkerSlot: orchestrationCodexSlotA}
			}
			return orchestrationTurnAssignment{Role: "critic", CLI: "codex", WorkerSlot: orchestrationCodexSlotB}
		}
		if turn%2 == 1 {
			return orchestrationTurnAssignment{Role: "implementer", CLI: "codex", WorkerSlot: orchestrationCodexSlotA}
		}
		return orchestrationTurnAssignment{Role: "reviewer", CLI: "codex", WorkerSlot: orchestrationCodexSlotB}
	}
	role, cli := roleForTurnWithFirstCLI(mode, firstCLI, turn)
	return orchestrationTurnAssignment{Role: role, CLI: cli, WorkerSlot: workerSlotForCLI(cli)}
}

func relayTurnPlan(workerPair, mode, firstCLI string, turn int) orchestrationTurnAssignment {
	return roleForTurnWithWorkerPair(mode, workerPair, firstCLI, turn)
}

func roleForTurnWithFirstCLI(mode, firstCLI string, turn int) (string, string) {
	cli := normalizeRelayFirstCLI(firstCLI)
	if turn%2 == 0 {
		if cli == "codex" {
			cli = "claude"
		} else {
			cli = "codex"
		}
	}
	if mode == "debate" {
		if turn%2 == 1 {
			return "proposer", cli
		}
		return "critic", cli
	}
	if turn%2 == 1 {
		return "implementer", cli
	}
	return "reviewer", cli
}

func workerSlotForCLI(cli string) string {
	switch strings.ToLower(strings.TrimSpace(cli)) {
	case "codex":
		return orchestrationCodexDefaultSlot
	case "claude":
		return "claude"
	default:
		return strings.TrimSpace(cli)
	}
}

func normalizeCodexWorkerSlot(workerSlot string) string {
	switch strings.ToLower(strings.TrimSpace(workerSlot)) {
	case orchestrationCodexSlotA:
		return orchestrationCodexSlotA
	case orchestrationCodexSlotB:
		return orchestrationCodexSlotB
	default:
		return orchestrationCodexDefaultSlot
	}
}

func normalizeRelayFirstCLI(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "codex":
		return "codex"
	default:
		return "claude"
	}
}

func newOrchestrationTurnRecord(turnID, role, cli, content string, tools []RunnerToolEvent) orchestrationTurn {
	return newOrchestrationTurnRecordWithSlot(turnID, role, cli, workerSlotForCLI(cli), content, tools)
}

func newOrchestrationTurnRecordWithSlot(turnID, role, cli, workerSlot, content string, tools []RunnerToolEvent) orchestrationTurn {
	content = scrubOrchestrationTurnContent(content)
	return orchestrationTurn{
		TurnID:     turnID,
		Role:       role,
		CLI:        cli,
		WorkerSlot: workerSlot,
		Content:    content,
		Handoff:    extractHandoffSummary(content),
		Relay:      parseOrchestrationRelayPacket(content),
		Tools:      tools,
	}
}

func parseOrchestrationRelayPacket(content string) orchestrationRelayPacket {
	msg, msgOK := anchoredMachineFields(content, "Msg:")
	handoff, handoffOK := anchoredMachineFields(content, "Handoff:")
	packet := orchestrationRelayPacket{
		To:       strings.ToLower(msg["to"]),
		Intent:   strings.ToLower(msg["intent"]),
		Need:     msg["need"],
		Status:   strings.ToLower(handoff["status"]),
		Changed:  handoff["changed"],
		Verified: handoff["verified"],
		Next:     handoff["next"],
		Risks:    handoff["risks"],
	}
	packet.Structured = msgOK && handoffOK &&
		machineFieldsPresent(msg, "to", "intent", "need") &&
		machineFieldsPresent(handoff, "status", "changed", "verified", "next", "risks") &&
		packet.To != "" && packet.Intent != "" && packet.Status != ""
	return packet
}

func machineFieldsPresent(fields map[string]string, keys ...string) bool {
	for _, key := range keys {
		if _, ok := fields[key]; !ok {
			return false
		}
	}
	return true
}

func anchoredMachineFields(content, marker string) (map[string]string, bool) {
	match, ok := lastAnchoredMarkerMatch(content, []string{marker})
	if !ok {
		return nil, false
	}
	line := content[match.payloadStart:]
	if newline := strings.IndexByte(line, '\n'); newline >= 0 {
		line = line[:newline]
	}
	fields := parseMachineFields(line)
	return fields, len(fields) > 0
}

func parseMachineFields(line string) map[string]string {
	fields := make(map[string]string)
	for _, part := range strings.Split(line, ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		switch key {
		case "to", "intent", "need", "status", "changed", "verified", "next", "risks":
			fields[key] = strings.TrimSpace(value)
		}
	}
	return fields
}

func orchestrationTurnHasFinalConclusion(record orchestrationTurn) bool {
	content := strings.TrimSpace(record.Content)
	if content == "" {
		return false
	}
	if strings.TrimSpace(record.Handoff) != "" {
		return true
	}
	if _, ok := lastAnchoredMarkerMatch(content, finalConclusionMarkers()); ok {
		return true
	}
	return false
}

func scrubOrchestrationTurnContent(content string) string {
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
	if match, ok := lastAnchoredMarkerMatch(content, finalConclusionMarkers()); ok && shouldTrimConclusionPrefix(content[:match.markerStart]) {
		return match.markerStart
	}
	if match, ok := lastAnchoredMarkerMatch(content, []string{
		"审查结论", "本轮结论", "结论：", "结论:", "conclusion:", "summary:",
	}); ok && shouldTrimConclusionPrefix(content[:match.markerStart]) {
		return match.markerStart
	}
	return -1
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

type assessmentDimension struct {
	NameZH   string
	NameEN   string
	StatusZH string
	StatusEN string
	DetailZH string
	DetailEN string
}

type orchestrationCommandState struct {
	ID       string
	Status   string
	Command  string
	Output   string
	ExitCode *int
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

const orchestrationLanguageRule = "Language rule: write all user-visible prose, including the 交接总结 handoff summary, in Chinese by default unless the user explicitly asks for another language."

const relayHistoryPromptBudget = 6 * 1024

func composeRelayPromptWithFirstCLI(mode, firstCLI, profile, userPrompt, contextSummary string, resume bool, role, cli string, turn, maxTurns int, history []orchestrationTurn) string {
	return composeRelayPromptWithWorkerSlot(mode, firstCLI, profile, userPrompt, contextSummary, resume, role, cli, workerSlotForCLI(cli), turn, maxTurns, history)
}

func composeRelayPromptWithWorkerSlot(mode, firstCLI, profile, userPrompt, contextSummary string, resume bool, role, cli, workerSlot string, turn, maxTurns int, history []orchestrationTurn) string {
	return composeRelayPromptWithTaskScope(mode, firstCLI, profile, userPrompt, contextSummary, resume, role, cli, workerSlot, turn, maxTurns, history, nil)
}

func composeRelayPromptWithTaskScope(mode, firstCLI, profile, userPrompt, contextSummary string, resume bool, role, cli, workerSlot string, turn, maxTurns int, history []orchestrationTurn, taskScope *durableTaskPromptScope) string {
	profile = normalizeOrchestrationProfile(profile)
	profileActive := registry.UsesSpecialRules(profile)
	var b strings.Builder
	b.WriteString("ProofBridge is relaying this browser orchestration like a human handoff between local CLIs. Treat this as a real user instruction, use your normal capabilities, and do not wait for Bridge to validate strategy choices.\n\n")
	b.WriteString(orchestrationLanguageRule)
	b.WriteString("\n\n")
	if taskScope != nil {
		b.WriteString(durableTaskRoleContract(*taskScope))
		b.WriteString("\n")
		b.WriteString("Maximize useful progress during this assignment: inspect the real workspace, make all safe in-scope changes you can justify, and run proportionate checks. Continue through viable approaches when a command fails or one approach stalls; one failure, one long checker run, or one incomplete slice is not a reason to stop. Hand off an unresolved blocker only after the materially same obstacle recurs without new evidence and alternative paths no longer produce useful progress.\n\n")
	} else {
		b.WriteString(relayModeRoleContract(profile, mode, role, turn, maxTurns))
		b.WriteString("\n")
	}
	returningWorker := priorSameWorkerTurns(history, cli, workerSlot) > 0
	if returningWorker {
		b.WriteString("You are receiving this message in the same native " + cli + " conversation used for your earlier turn(s) in this orchestration run. Keep using your existing local context and remembered work from that native session. Do not assume shell process state persists unless your CLI explicitly preserves it between turns.\n\n")
	}
	if taskScope == nil && turn == 1 && profileActive {
		b.WriteString(registry.InitialStrategy(profile, mode, firstCLI, userPrompt))
		b.WriteString("\n")
	}
	if taskScope != nil {
		b.WriteString(fmt.Sprintf("This is collaboration round %d of %d, durable node %q. The internal runner envelope is one assignment only; it does not make this the first or final collaboration round. Start from the current workspace and supplied evidence rather than restarting a baseline scan.\n\n", taskScope.Round, taskScope.MaxRounds, taskScope.Name))
	} else if turn == 1 {
		b.WriteString("You are the first CLI handling the user's task. Your visible result will be handed to another CLI afterward, so include the important files changed, commands run, blockers, and useful next context in your final response.\n\n")
	} else {
		b.WriteString("You are continuing from the previous CLI's visible result. Treat the prior result as context from another person, decide independently what to do next, and continue the same user task.\n\n")
	}
	if taskScope != nil && taskScope.finalReviewer() {
		if guidance := registry.FinalTurnGuidance(profile, mode); guidance != "" {
			b.WriteString(guidance)
			b.WriteString(" This final section serves as the handoff summary.\n\n")
		} else {
			b.WriteString("This is the final configured round's independent review. End with a user-ready section titled \"最终结论：\" or \"最终测试结果：\" that synthesizes actual command evidence, distinguishes verified facts from assumptions, and names remaining risks. Do not claim completion unless the original goal is actually resolved.\n\n")
		}
	} else if taskScope != nil {
		b.WriteString("A later durable node or collaboration round is scheduled after this assignment. End with a concise Chinese \"交接总结：\" that gives the next engineer the exact goal state, changes made, commands and results, remaining blocker if any, approaches already attempted, assumptions ruled out, and one executable next entry point. Do not write a user-final conclusion or imply that no later peer will run.\n\n")
	} else if turn == maxTurns {
		if guidance := registry.FinalTurnGuidance(profile, mode); guidance != "" {
			b.WriteString(guidance)
			b.WriteString(" This final section serves as the handoff summary.\n\n")
		} else {
			b.WriteString("This is the last scheduled turn. End with a user-ready section titled \"最终结论：\" or \"最终测试结果：\" that synthesizes the whole run, compares claims with actual command evidence, distinguishes verified facts from unresolved assumptions, and names remaining risks. Do not hand work to another CLI that will not run. This final section serves as the handoff summary.\n\n")
		}
	} else {
		b.WriteString("Always end your visible reply with a short handoff summary titled \"交接总结：\" — 2-4 Chinese sentences covering what you did, what you verified and with which commands, what is still blocked, and the single most useful next step for the following CLI. Bridge forwards this summary to the next CLI as a reading guide and separately forwards your actually executed commands and their exit codes as objective evidence, so keep the summary honest and specific rather than a bare success claim. If you already write a \"最终结论/最终测试结果\" section (for example on formal-proof tasks), that section serves as the handoff summary and you need not repeat it.\n\n")
	}
	if taskScope != nil {
		b.WriteString(durableTaskMachineHandoffContract(*taskScope))
	} else {
		b.WriteString(relayMachineHandoffContract(mode, role))
	}
	b.WriteString("\n")
	if resume {
		b.WriteString("This is a continuation of the same user-visible orchestration conversation. Use the compact context below when relevant, and treat the latest user task as authoritative.\n\n")
	}
	if strings.TrimSpace(contextSummary) != "" {
		b.WriteString("Compacted context from earlier tasks in this conversation:\n")
		b.WriteString(trimForPrompt(contextSummary, 12000))
		b.WriteString("\n\n")
	}
	if boundary := registry.TimeoutBoundary(profile, userPrompt); boundary != "" {
		b.WriteString(boundary)
		b.WriteString("\n")
	}
	if guidance := registry.RelayGuidance(profile, userPrompt, mode, role); guidance != "" {
		b.WriteString(guidance)
		b.WriteString("\n")
	}
	if taskScope != nil {
		b.WriteString(fmt.Sprintf("Collaboration round: %d of %d. Durable node: %s. Current CLI: %s/%s.\n\n", taskScope.Round, taskScope.MaxRounds, taskScope.Name, role, cli))
	} else {
		b.WriteString(fmt.Sprintf("Relay turn: %d of %d. Mode: %s. First CLI: %s. Current CLI: %s/%s.\n\n", turn, maxTurns, mode, normalizeRelayFirstCLI(firstCLI), role, cli))
	}
	relayHistory := relayHistoryForWorker(history, relayHistoryPromptBudget, cli, workerSlot, returningWorker)
	if len(relayHistory) > 0 {
		b.WriteString("Previous CLI handoff summary, result, and command evidence:\n")
		for _, item := range relayHistory {
			b.WriteString(formatRelayPriorTurn(item))
		}
		if note := relayRoutingNote(role, relayHistory[len(relayHistory)-1].Relay); note != "" {
			b.WriteString(note)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString("User task:\n")
	b.WriteString(strings.TrimSpace(userPrompt))
	b.WriteString("\n")
	return b.String()
}

func durableTaskRoleContract(scope durableTaskPromptScope) string {
	switch scope.Name {
	case "plan":
		return "Planning duty: decompose the original request into a bounded, dependency-aware checklist with stable ids. Inspect only enough context to make the plan executable; do not implement the user's requested changes in this node."
	case "plan-review":
		return "Plan Reviewer duty: independently audit and correct the proposed checklist for coverage, dependencies, verification, and formal-proof branches. Do not implement the user's requested changes in this node."
	case "candidate-a":
		return "Candidate A duty: choose the strongest viable route toward the user's goal, implement it deeply in the actual workspace, and validate it. Own progress rather than stopping at planning, a baseline scan, or the first proof/code fragment."
	case "candidate-b":
		return "Candidate B duty: inspect the workspace and Candidate A evidence first, then improve the existing result or pursue a materially different viable route. Do not repeat Candidate A's baseline investigation or wait for another worker."
	case "integrate":
		return "Integrator duty: reconcile the candidates in the actual workspace, resolve conflicts and gaps, continue implementing what is still missing, and run integration checks. You are an active engineer, not a summary-only coordinator."
	case "review":
		return "Independent Reviewer duty: try to falsify the current result against the original goal, run independent checks, inspect risks and shortcuts, and directly fix safe in-scope defects before reporting the remaining state. Review is active engineering, not passive approval."
	default:
		return fmt.Sprintf("Durable %s duty: continue the user's task from the actual workspace and compact evidence, make useful in-scope progress, validate it, and leave an exact peer handoff.", scope.Role)
	}
}

func durableTaskMachineHandoffContract(scope durableTaskPromptScope) string {
	to := "peer"
	intent := "continue"
	if scope.finalReviewer() {
		to = "user"
		intent = "final"
	}
	return "After the visible summary, append exactly two compact machine-readable lines:\n" +
		"Msg: to=<" + to + ">; intent=<" + intent + ", review, revise, or continue>; need=<single requested result or none>\n" +
		"Handoff: status=<needs_next, blocked, or resolved>; changed=<files or none>; verified=<exact commands and results or none>; next=<single executable action or none>; risks=<exact blocker, repeated attempts, ruled-out assumptions, and remaining risk or none>\n" +
		"Use blocked only after the materially same blocker repeated without new evidence. Use resolved/to=user/intent=final only for the final Reviewer when independent evidence shows the original goal is complete. Intermediate resolved means this node finished its assignment and still hands control to the next peer."
}

func relayRoutingNote(scheduledRole string, packet orchestrationRelayPacket) string {
	if !packet.Structured || packet.To == "user" || packet.To == "peer" || strings.EqualFold(packet.To, scheduledRole) {
		return ""
	}
	return fmt.Sprintf("Bridge routing note: the sender addressed %q, but the configured deterministic schedule selected %q. Keep the scheduled role and use the requested need as advisory context.", packet.To, scheduledRole)
}

func relayMachineHandoffContract(mode, role string) string {
	nextRole := "reviewer"
	if mode == "debate" {
		nextRole = "critic"
	}
	if role == "reviewer" || role == "critic" {
		nextRole = "implementer"
		if mode == "debate" {
			nextRole = "proposer"
		}
	}
	return "After the visible summary, append exactly two compact machine-readable lines:\n" +
		"Msg: to=<" + nextRole + " or user>; intent=<review, continue, revise, or final>; need=<single requested result or none>\n" +
		"Handoff: status=<needs_next, blocked, or resolved>; changed=<files or none>; verified=<checks or none>; next=<single action or none>; risks=<remaining risk or none>\n" +
		"Use resolved/to=user/intent=final only when the task is actually complete. Bridge may end early only from an independently evidenced reviewer/critic handoff; implementer/proposer completion claims still require peer review."
}

func relayHistoryForWorker(history []orchestrationTurn, budget int, cli, workerSlot string, returningWorker bool) []orchestrationTurn {
	if !returningWorker {
		return relayHistoryWithinBudget(history, budget)
	}
	for index := len(history) - 1; index >= 0; index-- {
		if !sameRelayWorker(history[index], cli, workerSlot) {
			return relayHistoryWithinBudget(history[index:index+1], budget)
		}
	}
	if len(history) == 0 {
		return nil
	}
	return relayHistoryWithinBudget(history[len(history)-1:], budget)
}

func sameRelayWorker(item orchestrationTurn, cli, workerSlot string) bool {
	if !strings.EqualFold(item.CLI, cli) {
		return false
	}
	if strings.EqualFold(cli, "codex") {
		return strings.EqualFold(normalizeCodexWorkerSlot(item.WorkerSlot), normalizeCodexWorkerSlot(workerSlot))
	}
	if strings.TrimSpace(workerSlot) == "" {
		return true
	}
	return strings.EqualFold(item.WorkerSlot, workerSlot)
}

func relayModeRoleContract(profile, mode, role string, turn, maxTurns int) string {
	if contract := registry.ModeRoleContract(profile, mode, role, turn); contract != "" {
		return contract
	}
	cycle := (turn + 1) / 2
	var duty string
	switch mode {
	case "debate":
		switch role {
		case "critic":
			duty = "Critic duty: test the strongest version of the proposal, actively seek counterexamples and hidden assumptions, run disconfirming checks, and provide a stronger alternative or safe in-scope fix instead of abstract objections."
			if turn == 1 {
				duty += " No prior proposal exists in this run, so first establish a baseline, falsifiable acceptance criteria, and the main assumptions the next proposer must address."
			}
		default:
			duty = "Proposer duty: state one falsifiable claim, its assumptions, concrete evidence, and an implementation or experiment when the task requests code; revise the claim when evidence contradicts it."
		}
		if cycle > 1 {
			duty += " This is a later debate cycle: answer the strongest unresolved counterargument and update the working conclusion rather than restarting the thesis."
		}
		return fmt.Sprintf("Debate contract (cycle %d): %s", cycle, duty)
	default:
		switch role {
		case "reviewer":
			duty = "Reviewer duty: independently inspect the implementation, run relevant disconfirming checks, fix safe in-scope defects you find, and state what is accepted, rejected, or still unverified; do not merely agree with the implementer."
			if turn == 1 {
				duty += " No prior implementation exists in this run, so first establish the baseline and root cause, define the evidence required for review, and make safe in-scope fixes when appropriate."
			}
		default:
			duty = "Implementer duty: find the root cause, make the smallest complete in-scope change, run focused validation, and leave an exact ledger of files, commands, blockers, and remaining risk."
		}
		if cycle > 1 {
			duty += " This is a later collaboration cycle: resolve the prior review evidence and remaining blockers rather than restarting the task."
		}
		return fmt.Sprintf("Collaboration contract (cycle %d): %s", cycle, duty)
	}
}

func relayHistoryWithinBudget(history []orchestrationTurn, budget int) []orchestrationTurn {
	if len(history) == 0 || budget <= 0 {
		return nil
	}
	start := len(history)
	used := 0
	for index := len(history) - 1; index >= 0; index-- {
		size := len(formatRelayPriorTurn(history[index]))
		if used > 0 && used+size > budget {
			break
		}
		start = index
		used += size
		if used >= budget {
			break
		}
	}
	return history[start:]
}

func formatRelayPriorTurn(item orchestrationTurn) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("- %s/%s", item.Role, item.CLI))
	if item.Err != "" {
		b.WriteString(" error=")
		b.WriteString(trimForPrompt(oneLine(item.Err), 220))
	}
	if item.Relay.Structured {
		b.WriteString("\n  message: to=")
		b.WriteString(trimForPrompt(oneLine(item.Relay.To), 80))
		b.WriteString("; intent=")
		b.WriteString(trimForPrompt(oneLine(item.Relay.Intent), 80))
		b.WriteString("; need=")
		b.WriteString(trimForPrompt(oneLine(item.Relay.Need), 360))
		b.WriteString("\n  state: status=")
		b.WriteString(trimForPrompt(oneLine(item.Relay.Status), 80))
		b.WriteString("; changed=")
		b.WriteString(trimForPrompt(oneLine(item.Relay.Changed), 500))
		b.WriteString("; verified=")
		b.WriteString(trimForPrompt(oneLine(item.Relay.Verified), 500))
		b.WriteString("; next=")
		b.WriteString(trimForPrompt(oneLine(item.Relay.Next), 500))
		b.WriteString("; risks=")
		b.WriteString(trimForPrompt(oneLine(item.Relay.Risks), 500))
	}
	summary := strings.TrimSpace(item.Handoff)
	if item.Relay.Structured {
		summary = ""
	}
	if summary != "" {
		b.WriteString("\n  handoff: ")
		b.WriteString(strings.ReplaceAll(trimForPrompt(summary, 600), "\n", "\n  "))
	}
	summaries := relayCommandSummaries(item.Tools, 6)
	hasCommands := len(summaries) > 0
	if !item.Relay.Structured && (summary == "" || hasCommands) {
		body := strings.TrimSpace(item.Content)
		if summary != "" {
			body = strings.TrimSpace(contentWithoutHandoffSummary(item.Content))
		}
		if body != "" {
			limit := 1800
			if hasCommands && summary != "" {
				limit = 1000
			}
			b.WriteString("\n  result: ")
			b.WriteString(strings.ReplaceAll(trimForPrompt(body, limit), "\n", "\n  "))
		}
	}
	if hasCommands {
		b.WriteString("\n  commands:\n")
		for _, cmd := range summaries {
			b.WriteString("  - ")
			b.WriteString(cmd)
			b.WriteByte('\n')
		}
	}
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteByte('\n')
	}
	return b.String()
}

func relayCanConverge(mode, profile string, history []orchestrationTurn) bool {
	if len(history) == 0 {
		return false
	}
	record := history[len(history)-1]
	if len(history) > 1 && ((mode == "collaboration" && record.Role != "reviewer") || (mode == "debate" && record.Role != "critic")) {
		return false
	}
	packet := record.Relay
	if !packet.Structured || packet.Status != "resolved" || packet.To != "user" || packet.Intent != "final" || !machineExplicitNone(packet.Next) || !machineExplicitNone(packet.Risks) {
		return false
	}
	if len(history) > 1 && (relayParticipantCount(history) < 2 || (machineNone(packet.Verified) && !relayHasSuccessfulCommand(record.Tools))) {
		return false
	}
	if normalizeOrchestrationProfile(profile) == "formal-proof" {
		return relayHasSuccessfulFormalCheck(record.Tools)
	}
	return true
}

func machineNone(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || strings.EqualFold(value, "none")
}

func machineExplicitNone(value string) bool {
	return strings.EqualFold(strings.TrimSpace(value), "none")
}

func relayParticipantCount(history []orchestrationTurn) int {
	seen := make(map[string]bool)
	for _, item := range history {
		worker := strings.ToLower(strings.TrimSpace(item.CLI)) + "/" + strings.ToLower(strings.TrimSpace(item.WorkerSlot))
		seen[worker] = true
	}
	return len(seen)
}

func relayHasSuccessfulCommand(tools []RunnerToolEvent) bool {
	for _, tool := range coalesceRelayToolEvents(tools) {
		status := strings.ToLower(strings.TrimSpace(tool.Status))
		if tool.ExitCode != nil && *tool.ExitCode == 0 && (status == "" || status == "completed" || status == "success" || status == "succeeded") {
			return true
		}
	}
	return false
}

func relayHasSuccessfulFormalCheck(tools []RunnerToolEvent) bool {
	for _, tool := range coalesceRelayToolEvents(tools) {
		if !relayHasSuccessfulCommand([]RunnerToolEvent{tool}) {
			continue
		}
		command := strings.ToLower(tool.Command)
		for _, marker := range []string{"coqc", "coqtop", "rocq", "lake build", "lean ", "isabelle build", "isabelle process", "print assumptions", "thm_oracles"} {
			if strings.Contains(command, marker) {
				return true
			}
		}
	}
	return false
}

func relayCommandSummaries(tools []RunnerToolEvent, max int) []string {
	if max <= 0 || len(tools) == 0 {
		return nil
	}
	tools = coalesceRelayToolEvents(tools)
	if len(tools) > max {
		tools = tools[len(tools)-max:]
	}
	var out []string
	for _, tool := range tools {
		if strings.TrimSpace(tool.Command) == "" {
			continue
		}
		status := strings.TrimSpace(tool.Status)
		if status == "" {
			status = "observed"
		}
		summary := trimForPrompt(oneLine(tool.Command), 260) + " | " + status
		if tool.ExitCode != nil {
			summary += fmt.Sprintf(" | exit=%d", *tool.ExitCode)
		}
		if strings.TrimSpace(tool.Output) != "" {
			summary += " | " + trimForPrompt(oneLine(tool.Output), 260)
		}
		out = append(out, summary)
		if len(out) >= max {
			break
		}
	}
	return out
}

func coalesceRelayToolEvents(tools []RunnerToolEvent) []RunnerToolEvent {
	latest := make(map[string]RunnerToolEvent)
	lastPosition := make(map[string]int)
	keys := make([]string, len(tools))
	for index, tool := range tools {
		key := strings.TrimSpace(tool.ID)
		if key == "" {
			key = "command:" + strings.TrimSpace(tool.Command)
		}
		keys[index] = key
		if previous, ok := latest[key]; ok {
			if strings.TrimSpace(tool.Command) == "" {
				tool.Command = previous.Command
			}
			if strings.TrimSpace(tool.Output) == "" {
				tool.Output = previous.Output
			}
			if tool.ExitCode == nil {
				tool.ExitCode = previous.ExitCode
			}
		}
		latest[key] = tool
		lastPosition[key] = index
	}
	out := make([]RunnerToolEvent, 0, len(latest))
	for index, key := range keys {
		if lastPosition[key] == index {
			out = append(out, latest[key])
		}
	}
	return out
}

func priorSameCLITurns(history []orchestrationTurn, cli string) int {
	count := 0
	for _, item := range history {
		if strings.EqualFold(item.CLI, cli) {
			count++
		}
	}
	return count
}

func priorSameWorkerTurns(history []orchestrationTurn, cli, workerSlot string) int {
	if strings.EqualFold(cli, "codex") {
		workerSlot = normalizeCodexWorkerSlot(workerSlot)
	}
	if workerSlot == "" {
		return priorSameCLITurns(history, cli)
	}
	count := 0
	for _, item := range history {
		if strings.EqualFold(item.CLI, cli) && strings.EqualFold(item.WorkerSlot, workerSlot) {
			count++
		}
	}
	return count
}

func trimForPrompt(value string, max int) string {
	value = sanitizePromptText(strings.TrimSpace(value))
	if max <= 0 || len(value) <= max {
		return value
	}
	if utf8.ValidString(value[:max]) {
		return value[:max] + "\n[truncated]"
	}
	end := 0
	for i := range value {
		if i > max {
			break
		}
		end = i
	}
	return value[:end] + "\n[truncated]"
}

func sanitizePromptText(value string) string {
	return strings.ToValidUTF8(value, "\uFFFD")
}

func handoffSummaryMarkers() []string {
	return []string{
		"交接总结", "交接摘要", "handoff summary",
		"Handoff:",
		"Handoff summary:",
		"最终结论", "最终总结", "final conclusion", "final summary",
	}
}

func finalConclusionMarkers() []string {
	return []string{
		"最终结论", "最终总结", "最终测试结果", "本轮结论", "审查结论",
		"结论：", "结论:",
		"final conclusion", "final summary", "conclusion:",
	}
}

type anchoredMarkerMatch struct {
	sectionStart int
	markerStart  int
	markerEnd    int
	payloadStart int
}

func lastAnchoredMarkerMatch(content string, markers []string) (anchoredMarkerMatch, bool) {
	var best anchoredMarkerMatch
	found := false
	for lineStart := 0; lineStart <= len(content); {
		lineEnd := len(content)
		nextLineStart := len(content) + 1
		if newline := strings.IndexByte(content[lineStart:], '\n'); newline >= 0 {
			lineEnd = lineStart + newline
			nextLineStart = lineEnd + 1
		}
		line := content[lineStart:lineEnd]
		if strings.HasSuffix(line, "\r") {
			line = line[:len(line)-1]
		}
		if markerOffset, markerLen, ok := anchoredMarkerInLine(line, markers); ok {
			match := anchoredMarkerMatch{
				sectionStart: lineStart,
				markerStart:  lineStart + markerOffset,
				markerEnd:    lineStart + markerOffset + markerLen,
			}
			match.payloadStart = markerPayloadStart(content, match.markerEnd)
			if !found || match.sectionStart > best.sectionStart || (match.sectionStart == best.sectionStart && match.markerEnd > best.markerEnd) {
				best = match
				found = true
			}
		}
		if nextLineStart > len(content) {
			break
		}
		lineStart = nextLineStart
	}
	return best, found
}

func anchoredMarkerInLine(line string, markers []string) (int, int, bool) {
	offset := anchoredMarkerCandidateOffset(line)
	candidate := line[offset:]
	bestLen := 0
	for _, marker := range markers {
		normalized := normalizedConclusionMarker(marker)
		if normalized == "" || len(candidate) < len(normalized) {
			continue
		}
		if !strings.EqualFold(candidate[:len(normalized)], normalized) {
			continue
		}
		if !anchoredMarkerBoundary(candidate[len(normalized):]) {
			continue
		}
		if len(normalized) > bestLen {
			bestLen = len(normalized)
		}
	}
	if bestLen == 0 {
		return 0, 0, false
	}
	return offset, bestLen, true
}

func anchoredMarkerCandidateOffset(line string) int {
	i := 0
	for i < len(line) {
		next := skipASCIISpace(line, i)
		if next != i {
			i = next
			continue
		}
		switch line[i] {
		case '#':
			for i < len(line) && line[i] == '#' {
				i++
			}
		case '-':
			i++
		case '*':
			for i < len(line) && line[i] == '*' {
				i++
			}
		default:
			return i
		}
		i = skipASCIISpace(line, i)
	}
	return i
}

func skipASCIISpace(value string, start int) int {
	for start < len(value) {
		switch value[start] {
		case ' ', '\t':
			start++
		default:
			return start
		}
	}
	return start
}

func normalizedConclusionMarker(marker string) string {
	return strings.TrimRight(strings.TrimSpace(marker), "：:")
}

func anchoredMarkerBoundary(rest string) bool {
	rest = strings.TrimLeft(rest, "*")
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return r == ':' || r == '：' || unicode.IsSpace(r)
}

func markerPayloadStart(content string, start int) int {
	for start < len(content) {
		r, size := utf8.DecodeRuneInString(content[start:])
		if r == ':' || r == '：' || r == '*' || unicode.IsSpace(r) {
			start += size
			continue
		}
		break
	}
	return start
}

func extractHandoffSummary(content string) string {
	match, ok := lastAnchoredMarkerMatch(content, handoffSummaryMarkers())
	if !ok {
		return ""
	}
	return strings.TrimSpace(content[match.payloadStart:])
}

func contentWithoutHandoffSummary(content string) string {
	match, ok := lastAnchoredMarkerMatch(content, handoffSummaryMarkers())
	if !ok {
		return content
	}
	return strings.TrimSpace(content[:match.sectionStart])
}
