package bridge

import (
	"context"
	"fmt"
	"github.com/tencent/codex-bridge/internal/bridge/profiles/registry"
	"github.com/tencent/codex-bridge/internal/protocol"
	"strings"
	"unicode/utf8"
)

type orchestrationTurn struct {
	TurnID  string
	Role    string
	CLI     string
	Content string
	Handoff string
	Err     string
	Tools   []RunnerToolEvent
}

func (m *OrchestrationManager) runRelayCLI(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, cli, prompt string, state *orchestrationSessionState) (string, []RunnerToolEvent, error) {
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
		content, tools, threadID, resumeMode, err := m.runCodexInteractive(ctx, payload, turnID, role, prompt, state)
		state.CodexResumeMode = resumeMode
		if threadID != "" {
			state.CodexThreadID = threadID
		}
		return content, tools, err
	}
}

func clearRelayResumeMode(cli string, state *orchestrationSessionState) {
	if state == nil {
		return
	}
	switch cli {
	case "codex":
		state.CodexResumeMode = ""
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

func (m *OrchestrationManager) resetCodexInteractiveSessionAfterRecoverableError(state *orchestrationSessionState) {
	if state == nil || state.NativeSession == nil {
		return
	}
	session := state.NativeSession
	session.mu.Lock()
	defer session.mu.Unlock()
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
}

func relayTurnEndData(cli string, state orchestrationSessionState) map[string]any {
	data := map[string]any{"relayOnly": true}
	switch cli {
	case "codex":
		if state.CodexResumeMode != "" {
			data["resumeMode"] = state.CodexResumeMode
		}
		if state.CodexThreadID != "" {
			data["codexThreadId"] = state.CodexThreadID
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

func (m *OrchestrationManager) relayRunEndData(cli string, state orchestrationSessionState, cwd string) *protocol.RunEndData {
	data := &protocol.RunEndData{}
	switch cli {
	case "codex":
		data.CodexThreadID = state.CodexThreadID
		data.CodexNativeResume = codexNativeResumeInfo(state.CodexThreadID, cwd)
	case "claude":
		data.ClaudeSessionID = state.ClaudeSessionID
		data.ClaudeNativeResume = m.claudeNativeResumeInfo(state.ClaudeSessionID, cwd)
	}
	data = runEndDataWithNativeResume(data, cwd)
	if data.CodexThreadID == "" && data.ClaudeSessionID == "" {
		return nil
	}
	return data
}

func orchestrationTurnStartContent(cli string, state *orchestrationSessionState, turn, maxTurns int, role string) string {
	mode := ""
	if state != nil {
		mode = plannedRelayResumeMode(cli, *state)
	}
	label := cliDisplay(cli)
	if role != "" {
		label = cliDisplay(cli) + " " + role
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

func plannedRelayResumeMode(cli string, state orchestrationSessionState) string {
	switch cli {
	case "codex":
		if state.CodexResumeMode != "" {
			return state.CodexResumeMode
		}
		if state.NativeSession != nil && state.NativeSession.codex != nil && state.NativeSession.codex.mode != "" {
			return state.NativeSession.codex.mode
		}
		if state.NativeSession != nil {
			if state.CodexThreadID != "" {
				return "codex-interactive-resume"
			}
			return "codex-interactive-thread"
		}
		if state.CodexThreadID != "" {
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

func roleForTurnWithFirstCLI(mode, firstCLI string, turn int) (string, string) {
	if normalizeRelayFirstCLI(firstCLI) == "codex" {
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

func normalizeRelayFirstCLI(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "codex":
		return "codex"
	default:
		return "claude"
	}
}

func newOrchestrationTurnRecord(turnID, role, cli, content string, tools []RunnerToolEvent) orchestrationTurn {
	content = scrubOrchestrationTurnContent(content)
	return orchestrationTurn{
		TurnID:  turnID,
		Role:    role,
		CLI:     cli,
		Content: content,
		Handoff: extractHandoffSummary(content),
		Tools:   tools,
	}
}

func orchestrationTurnHasFinalConclusion(record orchestrationTurn) bool {
	content := strings.TrimSpace(record.Content)
	if content == "" {
		return false
	}
	if strings.TrimSpace(record.Handoff) != "" {
		return true
	}
	if lastMarkerIndexFold(content, finalConclusionMarkers()) >= 0 {
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
	if idx := lastMarkerIndexFold(content, finalConclusionMarkers()); idx >= 0 && shouldTrimConclusionPrefix(content[:idx]) {
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

func composeRelayPromptWithFirstCLI(mode, firstCLI, profile, userPrompt, contextSummary string, resume bool, role, cli string, turn, maxTurns int, history []orchestrationTurn) string {
	profile = normalizeOrchestrationProfile(profile)
	profileActive := registry.UsesSpecialRules(profile)
	var b strings.Builder
	b.WriteString("Codex Bridge is relaying this browser orchestration like a human handoff between local CLIs. Treat this as a real user instruction, use your normal capabilities, and do not wait for Bridge to validate strategy choices.\n\n")
	b.WriteString(orchestrationLanguageRule)
	b.WriteString("\n\n")
	if priorSameCLITurns(history, cli) > 0 {
		b.WriteString("You are receiving this message in the same native " + cli + " conversation used for your earlier turn(s) in this orchestration run. Keep using your existing local context and remembered work from that native session. Do not assume shell process state persists unless your CLI explicitly preserves it between turns.\n\n")
	}
	if turn == 1 && profileActive && maxTurns >= 4 {
		b.WriteString(registry.InitialStrategy(profile, mode, firstCLI, userPrompt))
		b.WriteString("\n")
	}
	if turn == 1 {
		b.WriteString("You are the first CLI handling the user's task. Your visible result will be handed to another CLI afterward, so include the important files changed, commands run, blockers, and useful next context in your final response.\n\n")
	} else {
		b.WriteString("You are continuing from the previous CLI's visible result. Treat the prior result as context from another person, decide independently what to do next, and continue the same user task.\n\n")
	}
	b.WriteString("Always end your visible reply with a short handoff summary titled \"交接总结：\" — 2-4 Chinese sentences covering what you did, what you verified and with which commands, what is still blocked, and the single most useful next step for the following CLI. Bridge forwards this summary to the next CLI as a reading guide and separately forwards your actually executed commands and their exit codes as objective evidence, so keep the summary honest and specific rather than a bare success claim. If you already write a \"最终结论/最终测试结果\" section (for example on formal-proof tasks), that section serves as the handoff summary and you need not repeat it.\n\n")
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
	b.WriteString(fmt.Sprintf("Relay turn: %d of %d. Mode: %s. First CLI: %s. Current CLI: %s/%s.\n\n", turn, maxTurns, mode, normalizeRelayFirstCLI(firstCLI), role, cli))
	if len(history) > 0 {
		b.WriteString("Previous CLI handoff summary, result, and command evidence:\n")
		for _, item := range history {
			b.WriteString(formatRelayPriorTurn(item))
		}
		b.WriteByte('\n')
	}
	b.WriteString("User task:\n")
	b.WriteString(strings.TrimSpace(userPrompt))
	b.WriteString("\n")
	return b.String()
}

func formatRelayPriorTurn(item orchestrationTurn) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("- %s/%s", item.Role, item.CLI))
	if item.Err != "" {
		b.WriteString(" error=")
		b.WriteString(trimForPrompt(oneLine(item.Err), 220))
	}
	summary := strings.TrimSpace(item.Handoff)
	if summary != "" {
		b.WriteString("\n  handoff: ")
		b.WriteString(strings.ReplaceAll(trimForPrompt(summary, 600), "\n", "\n  "))
	}
	summaries := relayCommandSummaries(item.Tools, 6)
	hasCommands := len(summaries) > 0
	if summary == "" || hasCommands {
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

func relayCommandSummaries(tools []RunnerToolEvent, max int) []string {
	if max <= 0 || len(tools) == 0 {
		return nil
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

func priorSameCLITurns(history []orchestrationTurn, cli string) int {
	count := 0
	for _, item := range history {
		if strings.EqualFold(item.CLI, cli) {
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

func extractHandoffSummary(content string) string {
	idx := lastMarkerIndexFold(content, handoffSummaryMarkers())
	if idx < 0 {
		return ""
	}
	rest := content[idx:]
	for _, marker := range handoffSummaryMarkers() {
		if len(rest) >= len(marker) && strings.EqualFold(rest[:len(marker)], marker) {
			rest = rest[len(marker):]
			break
		}
	}
	rest = strings.TrimLeft(rest, "：: \t\r\n")
	return strings.TrimSpace(rest)
}

func contentWithoutHandoffSummary(content string) string {
	idx := lastMarkerIndexFold(content, handoffSummaryMarkers())
	if idx < 0 {
		return content
	}
	return strings.TrimSpace(content[:idx])
}
