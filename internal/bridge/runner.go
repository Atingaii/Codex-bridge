package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

type Runner interface {
	Name() string
	Prompt(ctx context.Context, req RunnerRequest, onUpdate func(update RunnerUpdate)) (RunnerResult, error)
	Close()
}

type RunnerRequest struct {
	SID            string
	Content        string
	RemoteThreadID string
	RunID          string
	PromptID       string
	CWD            string
	Approvals      ApprovalRequester
}

type RunnerResult struct {
	Content        string
	Usage          json.RawMessage
	RemoteThreadID string
}

type RunnerUpdate struct {
	Delta   string
	Content string
	Tool    *RunnerToolEvent
}

type RunnerToolEvent struct {
	ID       string
	Status   string
	Command  string
	Output   string
	ExitCode *int
}

type ApprovalRequester interface {
	RequestApproval(ctx context.Context, req protocol.ApprovalRequestPayload) (protocol.ApprovalResponsePayload, error)
}

func NewRunner(cfg *config.Config) (Runner, error) {
	switch strings.ToLower(cfg.Bridge.Runner) {
	case "", "echo":
		return EchoRunner{}, nil
	case "codex", "codex-exec":
		return NewCodexExecRunner(cfg), nil
	case "codex-app-server", "codex-appserver", "app-server":
		return NewCodexAppServerRunner(cfg), nil
	default:
		return nil, fmt.Errorf("unknown runner %q", cfg.Bridge.Runner)
	}
}

type EchoRunner struct{}

func (EchoRunner) Name() string { return "echo" }

func (EchoRunner) Prompt(ctx context.Context, req RunnerRequest, onUpdate func(update RunnerUpdate)) (RunnerResult, error) {
	text := "echo: " + req.Content
	select {
	case <-ctx.Done():
		return RunnerResult{}, ctx.Err()
	default:
		onUpdate(RunnerUpdate{Delta: text})
		return RunnerResult{Content: text, RemoteThreadID: req.RemoteThreadID}, nil
	}
}

func (EchoRunner) Close() {}

type CodexExecRunner struct {
	cfg *config.Config
}

func NewCodexExecRunner(cfg *config.Config) *CodexExecRunner {
	return &CodexExecRunner{cfg: cfg}
}

func (r *CodexExecRunner) Name() string { return "codex-exec" }

func (r *CodexExecRunner) Close() {}

func (r *CodexExecRunner) Prompt(ctx context.Context, req RunnerRequest, onUpdate func(update RunnerUpdate)) (RunnerResult, error) {
	args := r.args(req)
	cmd := exec.CommandContext(ctx, r.codexPath(), args...)
	if r.cfg.Bridge.CWD != "" {
		cmd.Dir = expandHome(r.cfg.Bridge.CWD)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return RunnerResult{}, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return RunnerResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return RunnerResult{}, err
	}
	_, _ = io.WriteString(stdin, req.Content)
	_ = stdin.Close()

	result, scanErr := r.scanJSONL(stdout, req.RemoteThreadID, onUpdate)
	waitErr := cmd.Wait()
	if scanErr != nil {
		return result, scanErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return result, errors.New(msg)
	}
	if result.Content == "" {
		result.Content = strings.TrimSpace(stderr.String())
	}
	return result, nil
}

func (r *CodexExecRunner) args(req RunnerRequest) []string {
	if req.RemoteThreadID != "" {
		args := append([]string{"exec", "resume"}, r.resumeArgs()...)
		args = append(args, req.RemoteThreadID, "-")
		return args
	}
	args := append([]string{"exec"}, r.execArgs()...)
	args = append(args, "-")
	return args
}

func (r *CodexExecRunner) execArgs() []string {
	common := []string{"--json", "--color", "never", "--skip-git-repo-check"}
	if r.cfg.Bridge.Model != "" {
		common = append(common, "--model", r.cfg.Bridge.Model)
	}
	if r.bypassApprovalsAndSandbox() {
		common = append(common, "--dangerously-bypass-approvals-and-sandbox")
	} else if r.cfg.Bridge.Sandbox != "" {
		common = append(common, "--sandbox", r.cfg.Bridge.Sandbox)
	}
	if r.cfg.Bridge.CWD != "" {
		common = append(common, "--cd", expandHome(r.cfg.Bridge.CWD))
	}
	if r.cfg.Bridge.ApprovalPolicy != "" && !r.bypassApprovalsAndSandbox() {
		common = append(common, "-c", "approval_policy="+quoteTomlString(r.cfg.Bridge.ApprovalPolicy))
	}
	return common
}

func (r *CodexExecRunner) resumeArgs() []string {
	common := []string{"--json", "--skip-git-repo-check"}
	if r.cfg.Bridge.Model != "" {
		common = append(common, "--model", r.cfg.Bridge.Model)
	}
	if r.bypassApprovalsAndSandbox() {
		common = append(common, "--dangerously-bypass-approvals-and-sandbox")
	} else {
		if r.cfg.Bridge.Sandbox != "" {
			common = append(common, "-c", "sandbox_mode="+quoteTomlString(r.cfg.Bridge.Sandbox))
		}
		if r.cfg.Bridge.ApprovalPolicy != "" {
			common = append(common, "-c", "approval_policy="+quoteTomlString(r.cfg.Bridge.ApprovalPolicy))
		}
	}
	return common
}

func (r *CodexExecRunner) bypassApprovalsAndSandbox() bool {
	return strings.EqualFold(r.cfg.Bridge.ApprovalPolicy, "never") &&
		strings.EqualFold(r.cfg.Bridge.Sandbox, "danger-full-access")
}

func (r *CodexExecRunner) codexPath() string {
	if r.cfg.Bridge.CodexPath == "" {
		return "codex"
	}
	return r.cfg.Bridge.CodexPath
}

func quoteTomlString(value string) string {
	b, _ := json.Marshal(value)
	return string(b)
}

func (r *CodexExecRunner) scanJSONL(stdout io.Reader, fallbackThreadID string, onUpdate func(update RunnerUpdate)) (RunnerResult, error) {
	result := RunnerResult{RemoteThreadID: fallbackThreadID}
	reader := bufio.NewReaderSize(stdout, 64*1024)
	var eventErr string
	for {
		line, err := readJSONLLine(reader, 32*1024*1024)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return result, err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var msg map[string]any
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		typ, _ := msg["type"].(string)
		if isErrorEvent(typ) {
			if message := eventErrorMessage(msg); message != "" {
				eventErr = message
			}
		}
		switch typ {
		case "thread.started":
			if id, _ := msg["thread_id"].(string); id != "" {
				result.RemoteThreadID = id
			}
			if id, _ := msg["threadId"].(string); id != "" {
				result.RemoteThreadID = id
			}
			if id := nestedString(msg, "thread", "id"); id != "" {
				result.RemoteThreadID = id
			}
		case "item.completed":
			item, _ := msg["item"].(map[string]any)
			itemType, _ := item["type"].(string)
			if itemType == "agent_message" || itemType == "agentMessage" {
				text := agentMessageText(item)
				if text != "" {
					result.Content = text
					onUpdate(RunnerUpdate{Content: text})
				}
			}
			if itemType == "command_execution" || itemType == "commandExecution" {
				if tool := commandExecutionEvent(item); tool != nil {
					onUpdate(RunnerUpdate{Tool: tool})
				}
			}
		case "item.started":
			item, _ := msg["item"].(map[string]any)
			itemType, _ := item["type"].(string)
			if itemType == "command_execution" || itemType == "commandExecution" {
				if tool := commandExecutionEvent(item); tool != nil {
					onUpdate(RunnerUpdate{Tool: tool})
				}
			}
		case "item.updated":
			item, _ := msg["item"].(map[string]any)
			itemType, _ := item["type"].(string)
			if itemType == "command_execution" || itemType == "commandExecution" {
				if tool := commandExecutionEvent(item); tool != nil {
					onUpdate(RunnerUpdate{Tool: tool})
				}
			}
		case "item.agent_message.delta", "item.agentMessage.delta", "agent_message.delta", "agentMessage.delta", "response.output_text.delta":
			if delta := extractDelta(msg); delta != "" {
				result.Content += delta
				onUpdate(RunnerUpdate{Delta: delta})
			}
		case "turn.completed":
			if usage, ok := msg["usage"]; ok {
				if b, err := json.Marshal(usage); err == nil {
					result.Usage = b
				}
			}
		}
	}
	if eventErr != "" {
		return result, errors.New(eventErr)
	}
	return result, nil
}

func readJSONLLine(reader *bufio.Reader, maxBytes int) ([]byte, error) {
	var line []byte
	for {
		part, err := reader.ReadBytes('\n')
		line = append(line, part...)
		if len(line) > maxBytes {
			return nil, fmt.Errorf("codex json event exceeds %d bytes", maxBytes)
		}
		if err == nil {
			return line, nil
		}
		if errors.Is(err, io.EOF) {
			if len(line) == 0 {
				return nil, io.EOF
			}
			return line, nil
		}
		return nil, err
	}
}

func commandExecutionEvent(item map[string]any) *RunnerToolEvent {
	command := commandString(item)
	output := outputString(item)
	status := firstString(item, "status")
	id := firstString(item, "id")
	var exitCode *int
	switch value := item["exit_code"].(type) {
	case float64:
		v := int(value)
		exitCode = &v
	case int:
		v := value
		exitCode = &v
	}
	if command == "" && output == "" && status == "" && id == "" {
		return nil
	}
	return &RunnerToolEvent{ID: id, Status: status, Command: command, Output: output, ExitCode: exitCode}
}

func commandString(item map[string]any) string {
	if command := firstString(item, "command", "cmd"); command != "" {
		return command
	}
	for _, key := range []string{"command", "cmd"} {
		if parts, _ := item[key].([]any); len(parts) > 0 {
			out := make([]string, 0, len(parts))
			for _, part := range parts {
				out = append(out, fmt.Sprint(part))
			}
			return strings.Join(out, " ")
		}
	}
	if child, _ := item["command"].(map[string]any); child != nil {
		if command := firstString(child, "text", "command", "cmd"); command != "" {
			return command
		}
	}
	return ""
}

func outputString(item map[string]any) string {
	if output := firstString(item, "aggregated_output", "output", "stdout", "stderr"); output != "" {
		return output
	}
	var b strings.Builder
	for _, key := range []string{"stdout", "stderr"} {
		if parts, _ := item[key].([]any); len(parts) > 0 {
			for _, part := range parts {
				b.WriteString(fmt.Sprint(part))
			}
		}
	}
	return b.String()
}

func agentMessageText(item map[string]any) string {
	if text := firstString(item, "text", "content"); text != "" {
		return text
	}
	if message, _ := item["message"].(map[string]any); message != nil {
		if text := firstString(message, "text", "content"); text != "" {
			return text
		}
	}
	parts, _ := item["content"].([]any)
	if len(parts) == 0 {
		return ""
	}
	var b strings.Builder
	for _, part := range parts {
		switch value := part.(type) {
		case string:
			b.WriteString(value)
		case map[string]any:
			b.WriteString(firstString(value, "text", "content"))
		}
	}
	return b.String()
}

func appendAgentMessageContentString(current *string, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	base := strings.TrimSpace(*current)
	switch {
	case base == "":
		*current = text
		return text
	case strings.HasSuffix(base, text):
		*current = base
		return ""
	case strings.HasPrefix(text, base):
		delta := strings.TrimPrefix(text, base)
		*current = text
		return delta
	default:
		*current = base + "\n\n" + text
		return "\n\n" + text
	}
}

func appendAgentMessageContent(current *strings.Builder, text string) string {
	content := current.String()
	delta := appendAgentMessageContentString(&content, text)
	current.Reset()
	current.WriteString(content)
	return delta
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, _ := m[key].(string); value != "" {
			return value
		}
	}
	return ""
}

func nestedString(m map[string]any, outer, inner string) string {
	child, _ := m[outer].(map[string]any)
	if child == nil {
		return ""
	}
	value, _ := child[inner].(string)
	return value
}

func extractDelta(msg map[string]any) string {
	if delta := firstString(msg, "delta", "text", "content"); delta != "" {
		return delta
	}
	for _, key := range []string{"params", "item", "message", "event"} {
		if child, _ := msg[key].(map[string]any); child != nil {
			if delta := firstString(child, "delta", "text", "content"); delta != "" {
				return delta
			}
		}
	}
	return ""
}

func isErrorEvent(typ string) bool {
	typ = strings.ToLower(typ)
	return typ == "error" || strings.HasSuffix(typ, ".error") || strings.Contains(typ, "failed")
}

func eventErrorMessage(msg map[string]any) string {
	if text := firstString(msg, "message", "error", "reason"); text != "" {
		return text
	}
	if errObj, _ := msg["error"].(map[string]any); errObj != nil {
		if text := firstString(errObj, "message", "code", "type"); text != "" {
			return text
		}
	}
	return ""
}
