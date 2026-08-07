package bridge

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/tencent/codex-bridge/internal/protocol"
)

const (
	chatRecoveryDelay          = 125 * time.Millisecond
	chatDefaultTranscriptLimit = 4 * 1024 * 1024
	chatEvidenceToolLimit      = 6
	chatEvidenceToolFieldLimit = 600
)

type chatPromptAttemptRequest struct {
	sid            string
	content        string
	remoteThreadID string
	runID          string
	promptID       string
	cwd            string
	approvals      ApprovalRequester
}

type chatPromptEvidence struct {
	content      []byte
	contentLimit int
	tools        []RunnerToolEvent
}

func newChatPromptEvidence(contentLimit int) *chatPromptEvidence {
	if contentLimit <= 0 {
		contentLimit = chatDefaultTranscriptLimit
	}
	return &chatPromptEvidence{contentLimit: contentLimit}
}

func (m *SessionManager) chatTranscriptLimit() int {
	if m != nil && m.cfg != nil && m.cfg.Hub.MaxAssistantMessageBytes > 0 && m.cfg.Hub.MaxAssistantMessageBytes <= int64(^uint(0)>>1) {
		return int(m.cfg.Hub.MaxAssistantMessageBytes)
	}
	return chatDefaultTranscriptLimit
}

func (e *chatPromptEvidence) observe(update RunnerUpdate) {
	if e == nil {
		return
	}
	if update.Delta != "" {
		e.addDelta(update.Delta)
	}
	if update.Content != "" {
		e.addContent(update.Content)
	}
	if update.Tool != nil {
		e.addTool(*update.Tool)
	}
}

func (e *chatPromptEvidence) addContent(value string) {
	if e == nil || strings.TrimSpace(value) == "" || len(e.content) >= e.contentLimit {
		return
	}
	content := string(e.content)
	appendAgentMessageContentString(&content, value)
	e.content = append(e.content[:0], truncateUTF8(content, e.contentLimit)...)
}

func (e *chatPromptEvidence) addDelta(value string) {
	if e == nil || value == "" || len(e.content) >= e.contentLimit {
		return
	}
	remaining := e.contentLimit - len(e.content)
	value = truncateUTF8(value, remaining)
	e.content = append(e.content, value...)
}

func (e *chatPromptEvidence) contentText() string {
	if e == nil {
		return ""
	}
	return string(e.content)
}

func (e *chatPromptEvidence) addTool(tool RunnerToolEvent) {
	if e == nil {
		return
	}
	tool.Command = truncateUTF8(strings.TrimSpace(tool.Command), chatEvidenceToolFieldLimit)
	tool.Output = truncateUTF8(strings.TrimSpace(tool.Output), chatEvidenceToolFieldLimit)
	for i := range e.tools {
		if tool.ID == "" || e.tools[i].ID != tool.ID {
			continue
		}
		mergeChatToolEvidence(&e.tools[i], tool)
		return
	}
	if len(e.tools) >= chatEvidenceToolLimit {
		copy(e.tools, e.tools[1:])
		e.tools = e.tools[:chatEvidenceToolLimit-1]
	}
	e.tools = append(e.tools, tool)
}

func mergeChatToolEvidence(dst *RunnerToolEvent, src RunnerToolEvent) {
	if src.Status != "" {
		dst.Status = src.Status
	}
	if src.Command != "" {
		dst.Command = src.Command
	}
	if src.Output != "" {
		dst.Output = src.Output
	}
	if src.ExitCode != nil {
		dst.ExitCode = src.ExitCode
	}
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func (m *SessionManager) runChatPromptAttempt(ctx context.Context, runner Runner, sessionRunner SessionRunner, req chatPromptAttemptRequest, evidence *chatPromptEvidence) (RunnerResult, error) {
	onUpdate := func(update RunnerUpdate) {
		if update.Delta == "" && update.Content == "" && update.Tool == nil {
			return
		}
		evidence.observe(update)
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
		m.sendSessionEnvelope(req.sid, protocol.MustEnvelope(protocol.TypeSessionUpdate, req.sid, protocol.SessionUpdatePayload{
			Delta:    update.Delta,
			Content:  update.Content,
			RunID:    req.runID,
			PromptID: req.promptID,
			Event:    updateEvent(update),
			Tool:     tool,
		}))
	}

	var result RunnerResult
	var err error
	if sessionRunner != nil {
		result, err = sessionRunner.PromptSession(ctx, PromptSessionRequest{
			SID:       req.sid,
			Content:   req.content,
			RunID:     req.runID,
			PromptID:  req.promptID,
			Approvals: req.approvals,
		}, onUpdate)
	} else {
		result, err = runner.Prompt(ctx, RunnerRequest{
			SID:            req.sid,
			Content:        req.content,
			RemoteThreadID: req.remoteThreadID,
			RunID:          req.runID,
			PromptID:       req.promptID,
			CWD:            req.cwd,
			Approvals:      req.approvals,
		}, onUpdate)
	}
	evidence.addContent(result.Content)
	return result, err
}

func shouldContinueChatPrompt(ctx context.Context, result RunnerResult, err error, evidence *chatPromptEvidence) bool {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if strings.TrimSpace(result.RemoteThreadID) == "" {
		return false
	}
	if err == nil {
		return evidence == nil || strings.TrimSpace(evidence.contentText()) == ""
	}
	message := strings.ToLower(err.Error())
	for _, blocked := range []string{
		"approval denied", "denied by user", "rejected by user", "permission denied",
		"unauthorized", "forbidden", "authentication", "not configured", "is required",
		"invalid request", "validation failed",
	} {
		if strings.Contains(message, blocked) {
			return false
		}
	}
	return true
}

func shouldRetryChatSessionOpen(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, blocked := range []string{
		"not configured", "is required", "unknown runner", "unauthorized", "forbidden",
		"authentication", "permission denied", "invalid request", "validation failed",
	} {
		if strings.Contains(message, blocked) {
			return false
		}
	}
	return true
}

func waitForChatRecovery(ctx context.Context) error {
	timer := time.NewTimer(chatRecoveryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func composeChatRecoveryPrompt(evidence *chatPromptEvidence, previousErr error) string {
	var b strings.Builder
	b.WriteString("The preceding turn in this same CLI thread ended without a reliable final response. It may already have changed files or executed commands. Inspect the current conversation and workspace state; do not repeat completed work or replay the user's request. Return only the missing final answer, including any remaining blocker. Match the language used by the user.\n")
	if evidence != nil && strings.TrimSpace(evidence.contentText()) != "" {
		b.WriteString("\nVisible assistant text already delivered:\n")
		b.WriteString(trimForPrompt(evidence.contentText(), 3000))
		b.WriteByte('\n')
	}
	if evidence != nil && len(evidence.tools) > 0 {
		b.WriteString("\nObserved command events (do not blindly repeat them):\n")
		for _, summary := range relayCommandSummaries(evidence.tools, chatEvidenceToolLimit) {
			b.WriteString("- ")
			b.WriteString(summary)
			b.WriteByte('\n')
		}
	}
	if previousErr != nil {
		b.WriteString("\nTransport/runner detail:\n")
		b.WriteString(trimForPrompt(previousErr.Error(), 1000))
		b.WriteByte('\n')
	}
	b.WriteString("\nContinue from current state and provide a concise final response now.")
	return b.String()
}
