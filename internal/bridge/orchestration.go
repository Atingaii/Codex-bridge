package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

type OrchestrationManager struct {
	cfg    *config.Config
	mu     sync.Mutex
	runs   map[string]context.CancelFunc
	output chan<- protocol.Envelope
}

type orchestrationTurn struct {
	TurnID  string
	Role    string
	CLI     string
	Content string
	Err     string
}

var safeOrchestrationFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func NewOrchestrationManager(cfg *config.Config) *OrchestrationManager {
	return &OrchestrationManager{
		cfg:  cfg,
		runs: make(map[string]context.CancelFunc),
	}
}

func (m *OrchestrationManager) AttachOut(out chan<- protocol.Envelope) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.output = out
}

func (m *OrchestrationManager) DetachOut(out chan<- protocol.Envelope) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.output == out {
		m.output = nil
	}
}

func (m *OrchestrationManager) Start(parent context.Context, payload protocol.OrchestrationStartPayload) {
	if payload.RunID == "" {
		m.send(protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
			Kind:  "run.error",
			Error: "orchestration run id is required",
		}))
		return
	}
	ctx, cancel := context.WithCancel(parent)
	m.mu.Lock()
	if old := m.runs[payload.RunID]; old != nil {
		old()
	}
	m.runs[payload.RunID] = cancel
	m.mu.Unlock()

	go func() {
		defer func() {
			cancel()
			m.mu.Lock()
			delete(m.runs, payload.RunID)
			m.mu.Unlock()
		}()
		m.run(ctx, payload)
	}()
}

func (m *OrchestrationManager) Cancel(runID string) {
	m.mu.Lock()
	cancel := m.runs[runID]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (m *OrchestrationManager) CloseAll() {
	m.mu.Lock()
	var cancels []context.CancelFunc
	for runID, cancel := range m.runs {
		cancels = append(cancels, cancel)
		delete(m.runs, runID)
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *OrchestrationManager) run(ctx context.Context, payload protocol.OrchestrationStartPayload) {
	preparedPrompt, _, err := PrepareOrchestrationPromptFiles(m.cfg, payload.RunID, payload.Prompt, payload.Files)
	if err != nil {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:   "run.error",
			Status: store.OrchestrationFailed,
			Error:  err.Error(),
		})
		return
	}
	payload.Prompt = preparedPrompt
	mode := payload.Mode
	if mode != "collaboration" && mode != "debate" {
		mode = "collaboration"
	}
	maxTurns := payload.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 4
	}
	if maxTurns > 12 {
		maxTurns = 12
	}
	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "run.start",
		Status:  store.OrchestrationRunning,
		Content: fmt.Sprintf("Starting %s run with %d turns.", mode, maxTurns),
	})

	var history []orchestrationTurn
	for turn := 1; turn <= maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.cancelled",
				Status: store.OrchestrationCanceled,
				Error:  "canceled",
			})
			return
		}
		role, cli := roleForTurn(mode, turn)
		turnID := fmt.Sprintf("%s-%02d", payload.RunID, turn)
		if payload.PromptSeq > 0 {
			turnID = fmt.Sprintf("%s-p%03d-%02d", payload.RunID, payload.PromptSeq, turn)
		}
		prompt := composeOrchestrationPrompt(mode, payload.Prompt, payload.Context, payload.Resume, role, cli, turn, maxTurns, history)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.start",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: promptHeader(role, cli, turn),
		})
		content, err := m.runCLI(ctx, payload, turnID, role, cli, prompt)
		record := orchestrationTurn{TurnID: turnID, Role: role, CLI: cli, Content: content}
		if err != nil {
			record.Err = err.Error()
			history = append(history, record)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "turn.end",
				TurnID: turnID,
				Role:   role,
				CLI:    cli,
				Status: "error",
				Error:  err.Error(),
			})
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				m.emit(payload.RunID, protocol.OrchestrationEventPayload{
					Kind:   "run.cancelled",
					Status: store.OrchestrationCanceled,
					Error:  "canceled",
				})
				return
			}
			continue
		}
		history = append(history, record)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:   "turn.end",
			TurnID: turnID,
			Role:   role,
			CLI:    cli,
			Status: "success",
		})
		if mode == "debate" && turn >= 2 && strings.Contains(strings.ToLower(content), "resolved") {
			break
		}
	}

	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "run.end",
		Status:  store.OrchestrationCompleted,
		Content: "Orchestration completed.",
	})
}

func (m *OrchestrationManager) runCLI(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, cli, prompt string) (string, error) {
	switch cli {
	case "claude":
		return m.runClaude(ctx, payload, turnID, role, prompt)
	default:
		return m.runCodex(ctx, payload, turnID, role, prompt)
	}
}

func (m *OrchestrationManager) runCodex(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt string) (string, error) {
	args := []string{"exec", "--json", "--color", "never", "--skip-git-repo-check"}
	if m.cfg.Bridge.Model != "" {
		args = append(args, "--model", m.cfg.Bridge.Model)
	}
	if strings.EqualFold(m.cfg.Bridge.ApprovalPolicy, "never") && strings.EqualFold(m.cfg.Bridge.Sandbox, "danger-full-access") {
		args = append(args, "--dangerously-bypass-approvals-and-sandbox")
	} else if m.cfg.Bridge.Sandbox != "" {
		args = append(args, "--sandbox", m.cfg.Bridge.Sandbox)
	}
	if m.cfg.Bridge.ApprovalPolicy != "" && !(strings.EqualFold(m.cfg.Bridge.ApprovalPolicy, "never") && strings.EqualFold(m.cfg.Bridge.Sandbox, "danger-full-access")) {
		args = append(args, "-c", "approval_policy="+quoteTomlString(m.cfg.Bridge.ApprovalPolicy))
	}
	cwd := m.cwd(payload)
	if cwd != "" {
		args = append(args, "--cd", cwd)
	}
	args = append(args, "-")

	cmd := exec.CommandContext(ctx, m.codexPath(), args...)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	if err := cmd.Start(); err != nil {
		return "", err
	}
	_, _ = io.WriteString(stdin, prompt)
	_ = stdin.Close()

	content, scanErr := m.scanCodexJSONL(stdout, payload.RunID, turnID, role)
	waitErr := cmd.Wait()
	if scanErr != nil {
		return content, scanErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return content, errors.New(msg)
	}
	if content == "" {
		content = strings.TrimSpace(stderr.String())
	}
	return content, nil
}

func (m *OrchestrationManager) runClaude(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt string) (string, error) {
	args := m.claudeArgs(payload, prompt)
	cmd := exec.CommandContext(ctx, m.claudePath(), args...)
	if cwd := m.cwd(payload); cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return "", err
	}
	content, scanErr := m.scanClaudeJSONL(stdout, payload.RunID, turnID, role)
	waitErr := cmd.Wait()
	if scanErr != nil {
		return content, scanErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return content, errors.New(msg)
	}
	return content, nil
}

func (m *OrchestrationManager) claudeArgs(payload protocol.OrchestrationStartPayload, prompt string) []string {
	args := []string{"--print", "--output-format=stream-json"}
	if cwd := m.cwd(payload); cwd != "" {
		args = append(args, "--add-dir", cwd)
	}
	args = append(args, "--verbose")
	if m.bypassApprovalsAndSandbox() {
		if runningAsRoot() {
			args = append(args, "--permission-mode", "acceptEdits")
		} else {
			args = append(args, "--permission-mode", "bypassPermissions")
		}
	}
	if m.cfg.Bridge.ClaudeModel != "" {
		args = append(args, "--model", m.cfg.Bridge.ClaudeModel)
	} else if m.cfg.Bridge.Model != "" {
		args = append(args, "--model", m.cfg.Bridge.Model)
	}
	if m.cfg.Bridge.ClaudeEffort != "" {
		args = append(args, "--effort", m.cfg.Bridge.ClaudeEffort)
	}
	args = append(args, prompt)
	return args
}

func (m *OrchestrationManager) bypassApprovalsAndSandbox() bool {
	return strings.EqualFold(m.cfg.Bridge.ApprovalPolicy, "never") &&
		strings.EqualFold(m.cfg.Bridge.Sandbox, "danger-full-access")
}

func runningAsRoot() bool {
	if os.Geteuid() == 0 {
		return true
	}
	current, err := user.Current()
	return err == nil && current.Uid == "0"
}

func (m *OrchestrationManager) scanCodexJSONL(stdout io.Reader, runID, turnID, role string) (string, error) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	var content strings.Builder
	var eventErr string
	for {
		line, err := readJSONLLine(reader, 32*1024*1024)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return content.String(), err
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
		case "item.agent_message.delta", "item.agentMessage.delta", "agent_message.delta", "agentMessage.delta", "response.output_text.delta":
			if delta := extractDelta(msg); delta != "" {
				content.WriteString(delta)
				m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: delta})
			}
		case "item.completed":
			item, _ := msg["item"].(map[string]any)
			itemType, _ := item["type"].(string)
			if itemType == "agent_message" || itemType == "agentMessage" {
				if text := agentMessageText(item); text != "" && content.Len() == 0 {
					content.WriteString(text)
					m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: text})
				}
			}
			if itemType == "command_execution" || itemType == "commandExecution" {
				if tool := commandExecutionEvent(item); tool != nil {
					m.emitTool(runID, turnID, role, "codex", tool)
				}
			}
		case "item.started", "item.updated":
			item, _ := msg["item"].(map[string]any)
			itemType, _ := item["type"].(string)
			if itemType == "command_execution" || itemType == "commandExecution" {
				if tool := commandExecutionEvent(item); tool != nil {
					m.emitTool(runID, turnID, role, "codex", tool)
				}
			}
		}
	}
	if eventErr != "" {
		return content.String(), errors.New(eventErr)
	}
	return strings.TrimSpace(content.String()), nil
}

func (m *OrchestrationManager) scanClaudeJSONL(stdout io.Reader, runID, turnID, role string) (string, error) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	var content strings.Builder
	for {
		line, err := readJSONLLine(reader, 32*1024*1024)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return content.String(), err
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
		switch typ {
		case "assistant":
			if message := firstString(msg, "error"); message != "" {
				return content.String(), errors.New(message)
			}
			for _, tool := range claudeToolEvents(msg) {
				m.emitTool(runID, turnID, role, "claude", tool)
			}
			if delta := claudeAssistantText(msg); delta != "" {
				content.WriteString(delta)
				m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "claude", Content: delta})
			}
		case "user":
			for _, tool := range claudeToolEvents(msg) {
				m.emitTool(runID, turnID, role, "claude", tool)
			}
		case "result":
			if isErr, _ := msg["is_error"].(bool); isErr {
				if text := firstString(msg, "result", "error"); text != "" {
					return content.String(), errors.New(text)
				}
				return content.String(), errors.New("claude returned an error")
			}
			if text := firstString(msg, "result"); text != "" && content.Len() == 0 {
				content.WriteString(text)
				m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "claude", Content: text})
			}
		case "error":
			if message := eventErrorMessage(msg); message != "" {
				return content.String(), errors.New(message)
			}
		}
	}
	return strings.TrimSpace(content.String()), nil
}

func (m *OrchestrationManager) emitTool(runID, turnID, role, cli string, tool *RunnerToolEvent) {
	kind := "command.end"
	if strings.EqualFold(tool.Status, "in_progress") || strings.EqualFold(tool.Status, "running") || strings.EqualFold(tool.Status, "started") {
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
	m.emit(runID, protocol.OrchestrationEventPayload{
		Kind:   kind,
		TurnID: turnID,
		Role:   role,
		CLI:    cli,
		Status: tool.Status,
		Data:   data,
	})
}

func (m *OrchestrationManager) emit(runID string, event protocol.OrchestrationEventPayload) {
	event.RunID = runID
	m.send(protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", event))
}

func (m *OrchestrationManager) send(env protocol.Envelope) {
	m.mu.Lock()
	out := m.output
	m.mu.Unlock()
	if out == nil {
		slog.Warn("[bridge] orchestration event dropped: bridge disconnected", "type", env.Type)
		return
	}
	send(out, env)
}

func (m *OrchestrationManager) cwd(payload protocol.OrchestrationStartPayload) string {
	if payload.CWD != "" {
		return expandHome(payload.CWD)
	}
	if m.cfg.Bridge.CWD != "" {
		return expandHome(m.cfg.Bridge.CWD)
	}
	return "."
}

func (m *OrchestrationManager) codexPath() string {
	if m.cfg.Bridge.CodexPath == "" {
		return "codex"
	}
	return m.cfg.Bridge.CodexPath
}

func (m *OrchestrationManager) claudePath() string {
	if m.cfg.Bridge.ClaudePath == "" {
		return "claude"
	}
	return m.cfg.Bridge.ClaudePath
}

func roleForTurn(mode string, turn int) (string, string) {
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

func promptHeader(role, cli string, turn int) string {
	return fmt.Sprintf("%s via %s, turn %d", role, cli, turn)
}

func composeOrchestrationPrompt(mode, userPrompt, contextSummary string, resume bool, role, cli string, turn, maxTurns int, history []orchestrationTurn) string {
	var b strings.Builder
	b.WriteString("You are participating in a local CLI orchestration run.\n")
	b.WriteString("Use your native file, shell, MCP, and skill capabilities when useful. Do not assume the other CLI can see your private reasoning.\n\n")
	if resume {
		b.WriteString("This is a continuation of the same user-visible orchestration conversation. Maintain continuity with the compacted context, while treating the latest user task as authoritative.\n\n")
	}
	if mode == "debate" {
		if role == "proposer" {
			b.WriteString("Role: proposer. Make concrete progress on the task, edit files if needed, and run verification commands when appropriate.\n")
		} else {
			b.WriteString("Role: critic. Review the previous work, identify concrete issues, and run verification commands when appropriate. Prefer actionable fixes over vague critique.\n")
		}
	} else {
		if role == "implementer" {
			b.WriteString("Role: implementer. Make concrete progress on the task and leave clear notes for the reviewer.\n")
		} else {
			b.WriteString("Role: reviewer. Review the implementer's work, fix clear issues, and verify the result when appropriate.\n")
		}
	}
	b.WriteString(fmt.Sprintf("Turn: %d of %d. CLI: %s.\n\n", turn, maxTurns, cli))
	if strings.TrimSpace(contextSummary) != "" {
		b.WriteString("Compacted context from earlier tasks in this conversation:\n")
		b.WriteString(trimForPrompt(contextSummary, 14000))
		b.WriteString("\n\n")
	}
	b.WriteString("Original user task:\n")
	b.WriteString(userPrompt)
	b.WriteString("\n\n")
	if len(history) > 0 {
		b.WriteString("Prior turns:\n")
		for _, item := range history {
			b.WriteString(fmt.Sprintf("[%s via %s]\n", item.Role, item.CLI))
			if item.Content != "" {
				b.WriteString(trimForPrompt(item.Content, 5000))
				b.WriteByte('\n')
			}
			if item.Err != "" {
				b.WriteString("Error: ")
				b.WriteString(trimForPrompt(item.Err, 1500))
				b.WriteByte('\n')
			}
			b.WriteByte('\n')
		}
	}
	if turn == maxTurns {
		b.WriteString("This is the final scheduled turn. Summarize the final state, verification results, and remaining risks.\n")
	}
	return b.String()
}

func trimForPrompt(value string, max int) string {
	value = strings.TrimSpace(value)
	if len(value) <= max {
		return value
	}
	return value[:max] + "\n[truncated]"
}

func claudeAssistantText(msg map[string]any) string {
	message, _ := msg["message"].(map[string]any)
	if message == nil {
		return ""
	}
	parts, _ := message["content"].([]any)
	var b strings.Builder
	for _, part := range parts {
		block, _ := part.(map[string]any)
		if block == nil {
			continue
		}
		if firstString(block, "type") == "text" {
			b.WriteString(firstString(block, "text"))
		}
	}
	return b.String()
}

func claudeToolEvents(msg map[string]any) []*RunnerToolEvent {
	message, _ := msg["message"].(map[string]any)
	if message == nil {
		return nil
	}
	parts, _ := message["content"].([]any)
	if len(parts) == 0 {
		return nil
	}
	events := make([]*RunnerToolEvent, 0, len(parts))
	for _, part := range parts {
		block, _ := part.(map[string]any)
		if block == nil {
			continue
		}
		switch firstString(block, "type") {
		case "tool_use":
			tool := claudeToolUseEvent(block)
			if tool != nil {
				events = append(events, tool)
			}
		case "tool_result":
			tool := claudeToolResultEvent(block)
			if tool != nil {
				events = append(events, tool)
			}
		}
	}
	return events
}

func claudeToolUseEvent(block map[string]any) *RunnerToolEvent {
	name := firstString(block, "name")
	id := firstString(block, "id")
	input, _ := block["input"].(map[string]any)
	command := claudeToolCommand(name, input)
	if command == "" {
		command = name
	}
	if command == "" && id == "" {
		return nil
	}
	return &RunnerToolEvent{ID: id, Status: "in_progress", Command: command}
}

func claudeToolResultEvent(block map[string]any) *RunnerToolEvent {
	id := firstString(block, "tool_use_id", "id")
	status := "completed"
	if isErr, _ := block["is_error"].(bool); isErr {
		status = "failed"
	}
	output := claudeToolResultContent(block["content"])
	if output == "" && id == "" {
		return nil
	}
	return &RunnerToolEvent{ID: id, Status: status, Output: output}
}

func claudeToolCommand(name string, input map[string]any) string {
	if input == nil {
		return name
	}
	switch name {
	case "Bash":
		if command := firstString(input, "command"); command != "" {
			return command
		}
	case "Read":
		if path := firstString(input, "file_path", "path"); path != "" {
			return "Read " + path
		}
	case "Write":
		if path := firstString(input, "file_path", "path"); path != "" {
			return "Write " + path
		}
	case "Edit", "MultiEdit":
		if path := firstString(input, "file_path", "path"); path != "" {
			return name + " " + path
		}
	case "Glob":
		if pattern := firstString(input, "pattern"); pattern != "" {
			return "Glob " + pattern
		}
	case "Grep":
		if pattern := firstString(input, "pattern"); pattern != "" {
			return "Grep " + pattern
		}
	}
	if description := firstString(input, "description"); description != "" {
		return name + ": " + description
	}
	return name
}

func claudeToolResultContent(value any) string {
	switch content := value.(type) {
	case string:
		return content
	case []any:
		var b strings.Builder
		for _, item := range content {
			switch part := item.(type) {
			case string:
				b.WriteString(part)
			case map[string]any:
				if text := firstString(part, "text", "content"); text != "" {
					b.WriteString(text)
				}
			}
		}
		return b.String()
	default:
		return ""
	}
}

func PrepareOrchestrationPromptFiles(cfg *config.Config, runID, prompt string, files []protocol.AttachmentPayload) (string, []store.OrchestrationFile, error) {
	if len(files) == 0 {
		return strings.TrimSpace(prompt), nil, nil
	}
	if len(files) > 12 {
		return "", nil, errors.New("at most 12 files can be uploaded")
	}
	baseDir := cfg.Bridge.CWD
	if baseDir == "" {
		baseDir = "."
	}
	uploadDir := filepath.Join(expandHome(baseDir), ".codex-bridge", "orchestrations", safeFileName(runID))
	if err := os.MkdirAll(uploadDir, 0o700); err != nil {
		return "", nil, fmt.Errorf("create orchestration upload directory: %w", err)
	}
	maxBytes := cfg.Hub.MaxAttachmentBytes
	if maxBytes <= 0 {
		maxBytes = 8 * 1024 * 1024
	}

	var metas []store.OrchestrationFile
	var paths []string
	for i, file := range files {
		if file.Size <= 0 || file.Size > maxBytes {
			return "", nil, fmt.Errorf("file %q is too large", file.Name)
		}
		raw, err := base64.StdEncoding.DecodeString(file.Data)
		if err != nil {
			return "", nil, fmt.Errorf("decode file %q: %w", file.Name, err)
		}
		if int64(len(raw)) > maxBytes {
			return "", nil, fmt.Errorf("file %q is too large", file.Name)
		}
		name := safeOrchestrationUploadName(file.Name)
		if name == "" {
			name = fmt.Sprintf("upload-%02d.bin", i+1)
		}
		path := filepath.Join(uploadDir, fmt.Sprintf("%s-%s", attachmentID(i), name))
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			return "", nil, fmt.Errorf("write file %q: %w", file.Name, err)
		}
		abs, err := filepath.Abs(path)
		if err == nil {
			path = abs
		}
		paths = append(paths, path)
		metas = append(metas, store.OrchestrationFile{Name: file.Name, MimeType: file.MimeType, Size: int64(len(raw))})
	}

	var b strings.Builder
	b.WriteString(strings.TrimSpace(prompt))
	b.WriteString("\n\nUploaded files for this orchestration run:\n")
	for _, path := range paths {
		b.WriteString("- ")
		b.WriteString(path)
		b.WriteByte('\n')
	}
	b.WriteString("\nUse these local file paths directly when the task refers to uploaded files.")
	return b.String(), metas, nil
}

func safeOrchestrationUploadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = safeOrchestrationFileName.ReplaceAllString(name, "-")
	return strings.Trim(name, ".-")
}
