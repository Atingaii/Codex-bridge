package bridge

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/tencent/codex-bridge/internal/protocol"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type orchestrationClaudeSession struct {
	cmd            *exec.Cmd
	stdin          io.WriteCloser
	stdout         *bufio.Reader
	stderr         *bytes.Buffer
	sessionID      string
	mode           string
	approvalServer *claudeApprovalServer
	release        func()
}

const defaultLongCommandObserverAfter = 2 * time.Minute

const claudeStreamInputIdleCloseAfter = 45 * time.Second

type claudeStreamNudge struct {
	After   time.Duration
	Message string
}

type claudeScanOptions struct {
	Input               io.Writer
	CanNudge            bool
	NudgeAfter          time.Duration
	IdleCloseAfter      time.Duration
	ReturnAfterResult   bool
	LongCommandObserver longCommandObserverConfig
}

type longCommandObserverConfig struct {
	Enabled         bool
	After           time.Duration
	CommandPatterns []string
	AppliesTo       []string
}

func (m *OrchestrationManager) runClaude(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt string) (string, []RunnerToolEvent, error) {
	content, tools, _, err := m.runClaudeWithSession(ctx, payload, turnID, role, prompt, "", false)
	return content, tools, err
}

func (m *OrchestrationManager) runClaudeInteractive(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt string, state *orchestrationSessionState) (string, []RunnerToolEvent, string, error) {
	if state == nil || state.NativeSession == nil {
		return m.runClaudeWithSession(ctx, payload, turnID, role, prompt, "", false)
	}
	session := state.NativeSession
	session.mu.Lock()
	claude, err := m.ensureClaudeInteractiveSessionLocked(ctx, payload, state)
	compactAfterTurn := protocol.NormalizeNativeContextCompaction(session.nativeContextCompaction) == protocol.NativeContextCompactionAfterTurn
	sessionCWD := session.cwd
	session.mu.Unlock()
	if err != nil {
		return "", nil, "claude-interactive-error", err
	}
	if err := writeClaudeStreamUserMessage(claude.stdin, prompt); err != nil {
		return "", nil, claude.mode, err
	}
	if claude.approvalServer != nil {
		claude.approvalServer.updateTurn(turnID, role)
	}
	observer := m.longCommandObserverConfig()
	content, tools, err := m.scanClaudeJSONLWithOptions(claude.stdout, payload.RunID, turnID, role, claudeScanOptions{
		Input:               claude.stdin,
		CanNudge:            observer.Enabled && observerAppliesTo(observer, "claude"),
		LongCommandObserver: observer,
		NudgeAfter:          observer.After,
		ReturnAfterResult:   true,
	})
	if err != nil && claude.stderr != nil {
		msg := strings.TrimSpace(claude.stderr.String())
		if msg != "" {
			err = errors.New(msg)
		}
	}
	if err == nil {
		m.registerClaudeNativeResume(state.NativeSession, claude, payload.RunID, sessionCWD)
		m.runNativeContextCompaction(ctx, payload.RunID, turnID, role, "claude", compactAfterTurn, state.NativeSession, claude)
	}
	return content, tools, claude.mode, err
}

func (m *OrchestrationManager) ensureClaudeInteractiveSessionLocked(ctx context.Context, payload protocol.OrchestrationStartPayload, state *orchestrationSessionState) (*orchestrationClaudeSession, error) {
	session := state.NativeSession
	if session.claude != nil && session.claude.stdin != nil && session.claude.stdout != nil {
		return session.claude, nil
	}
	resume := state.ClaudeSessionStarted
	args := m.claudeArgsWithStreamInput(payload, state.ClaudeSessionID, resume)
	// Don't set --name, let Claude CLI use default project name based on cwd
	approvalServer, releaseApprovalServer, err := m.prepareClaudeApprovalServer(ctx, payload, "", "")
	if err != nil {
		return nil, err
	}
	if approvalServer != nil {
		args = m.withClaudeStreamApprovalArgs(args, approvalServer.configPath)
	}
	cmd := exec.CommandContext(context.Background(), m.claudePath(), args...)
	configureManagedCommand(cmd)
	configureClaudeCommandEnv(cmd)
	cwd := m.cwd(payload)
	if cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		releaseApprovalServer()
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		releaseApprovalServer()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		releaseApprovalServer()
		return nil, err
	}
	mode := "claude-interactive-session"
	if resume {
		mode = "claude-interactive-resume"
	}
	claude := &orchestrationClaudeSession{
		cmd:            cmd,
		stdin:          stdin,
		stdout:         bufio.NewReaderSize(stdout, 64*1024),
		stderr:         &stderr,
		sessionID:      state.ClaudeSessionID,
		mode:           mode,
		approvalServer: approvalServer,
		release:        releaseApprovalServer,
	}
	session.claude = claude
	return claude, nil
}

func (m *OrchestrationManager) runClaudeWithSession(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt, sessionID string, resume bool) (string, []RunnerToolEvent, string, error) {
	approvalServer, releaseApprovalServer, err := m.prepareClaudeApprovalServer(ctx, payload, turnID, role)
	if err != nil {
		return "", nil, "", err
	}
	defer releaseApprovalServer()
	content, tools, err := m.runClaudeWithSessionAttempt(ctx, payload, turnID, role, prompt, sessionID, resume, approvalServer)
	if resume && err != nil && ctx.Err() == nil && isClaudeMissingSessionError(err) {
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.delta",
			TurnID:  turnID,
			Role:    role,
			CLI:     "claude",
			Status:  "warning",
			Content: "Claude native resume could not find the saved session. Bridge will retry once with the deterministic session id and keep the compacted orchestration context.",
			Data: map[string]any{
				"sessionId":  sessionID,
				"resumeMode": "claude-new-after-resume-miss",
				"relayOnly":  true,
			},
		})
		content, tools, err = m.runClaudeWithSessionAttempt(ctx, payload, turnID, role, prompt, sessionID, false, approvalServer)
		return content, tools, "claude-new-after-resume-miss", err
	}
	mode := "claude-new"
	if resume {
		mode = "claude-resume"
	}
	return content, tools, mode, err
}

func (m *OrchestrationManager) runClaudeWithSessionAttempt(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt, sessionID string, resume bool, approvalServer *claudeApprovalServer) (string, []RunnerToolEvent, error) {
	cmdCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	args := m.claudeArgsWithSession(payload, prompt, sessionID, resume)
	observer := m.longCommandObserverConfig()
	useStreamInput := observer.Enabled && observerAppliesTo(observer, "claude")
	if useStreamInput {
		args = m.claudeArgsWithStreamInput(payload, sessionID, resume)
	}
	// Don't set --name, let Claude CLI use default project name based on cwd
	if approvalServer != nil {
		args = m.withClaudeApprovalArgs(args, approvalServer.configPath)
	}
	cmd := exec.CommandContext(cmdCtx, m.claudePath(), args...)
	configureManagedCommand(cmd)
	configureClaudeCommandEnv(cmd)
	if cwd := m.cwd(payload); cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	var stdin io.WriteCloser
	if useStreamInput {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return "", nil, err
		}
	} else {
		cmd.Stdin = nil
	}
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	if useStreamInput {
		if err := writeClaudeStreamUserMessage(stdin, prompt); err != nil {
			cancel()
			_ = stdin.Close()
			_ = cmd.Wait()
			return "", nil, err
		}
	}
	var input io.Writer
	if stdin != nil {
		input = stdin
		defer stdin.Close()
	}
	content, tools, scanErr := m.scanClaudeJSONLWithOptions(stdout, payload.RunID, turnID, role, claudeScanOptions{
		Input:               input,
		CanNudge:            useStreamInput,
		NudgeAfter:          observer.After,
		LongCommandObserver: observer,
	})
	if scanErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return content, tools, err
	}
	if scanErr != nil {
		return content, tools, scanErr
	}
	if waitErr != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = waitErr.Error()
		}
		return content, tools, errors.New(msg)
	}
	return content, tools, nil
}

func isClaudeMissingSessionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(stripANSI(err.Error()))
	if strings.Contains(msg, "session") {
		return strings.Contains(msg, "not found") ||
			strings.Contains(msg, "does not exist") ||
			strings.Contains(msg, "missing") ||
			strings.Contains(msg, "unknown") ||
			strings.Contains(msg, "invalid")
	}
	return strings.Contains(msg, "--resume") && (strings.Contains(msg, "not found") || strings.Contains(msg, "missing"))
}

type claudeApprovalServer struct {
	configPath string
	mu         sync.Mutex
	requester  orchestrationApprovalRequester
}

func (m *OrchestrationManager) prepareClaudeApprovalServer(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role string) (*claudeApprovalServer, func(), error) {
	if !m.shouldBridgeClaudeApproval() {
		return nil, func() {}, nil
	}
	tmpDir, err := os.MkdirTemp("", "codex-bridge-claude-approval-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create claude approval temp dir: %w", err)
	}
	releaseTempDir := func() {
		_ = os.RemoveAll(tmpDir)
	}
	socketPath := filepath.Join(tmpDir, "approval.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		releaseTempDir()
		return nil, nil, fmt.Errorf("listen claude approval socket: %w", err)
	}
	serverCtx, cancel := context.WithCancel(ctx)
	requester := orchestrationApprovalRequester{
		manager: m,
		runID:   payload.RunID,
		turnID:  turnID,
		role:    role,
		cli:     "claude",
		cwd:     m.cwd(payload),
	}
	server := &claudeApprovalServer{requester: requester}
	go serveClaudeApprovalSocket(serverCtx, listener, server)

	exe, err := os.Executable()
	if err != nil {
		listener.Close()
		cancel()
		releaseTempDir()
		return nil, nil, fmt.Errorf("locate codex-bridge executable for claude approval mcp: %w", err)
	}
	configPath := filepath.Join(tmpDir, "mcp.json")
	config := map[string]any{
		"mcpServers": map[string]any{
			"codex_bridge": map[string]any{
				"type":    "stdio",
				"command": exe,
				"args":    []string{"claude-approval-mcp", "--socket", socketPath},
			},
		},
	}
	raw, err := json.Marshal(config)
	if err != nil {
		listener.Close()
		cancel()
		releaseTempDir()
		return nil, nil, fmt.Errorf("marshal claude approval mcp config: %w", err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		listener.Close()
		cancel()
		releaseTempDir()
		return nil, nil, fmt.Errorf("write claude approval mcp config: %w", err)
	}
	server.configPath = configPath
	return server, func() {
		cancel()
		listener.Close()
		releaseTempDir()
	}, nil
}

func (s *claudeApprovalServer) updateTurn(turnID, role string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.requester.turnID = turnID
	s.requester.role = role
	s.mu.Unlock()
}

func (m *OrchestrationManager) claudeArgsWithSession(payload protocol.OrchestrationStartPayload, prompt, sessionID string, resume bool) []string {
	args := []string{"--output-format=stream-json"}
	if cwd := m.cwd(payload); cwd != "" {
		args = append(args, "--add-dir", cwd)
	}
	args = append(args, "--verbose")
	if sessionID != "" {
		if resume {
			args = append(args, "--resume", sessionID)
		} else {
			args = append(args, "--session-id", sessionID)
		}
	}
	if m.bypassApprovalsAndSandbox() {
		args = append(args, "--permission-mode", "bypassPermissions")
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

func (m *OrchestrationManager) claudeArgsWithStreamInput(payload protocol.OrchestrationStartPayload, sessionID string, resume bool) []string {
	args := []string{"--input-format=stream-json", "--output-format=stream-json"}
	if cwd := m.cwd(payload); cwd != "" {
		args = append(args, "--add-dir", cwd)
	}
	args = append(args, "--verbose")
	if sessionID != "" {
		if resume {
			args = append(args, "--resume", sessionID)
		} else {
			args = append(args, "--session-id", sessionID)
		}
	}
	if m.bypassApprovalsAndSandbox() {
		args = append(args, "--permission-mode", "bypassPermissions")
	}
	if m.cfg.Bridge.ClaudeModel != "" {
		args = append(args, "--model", m.cfg.Bridge.ClaudeModel)
	} else if m.cfg.Bridge.Model != "" {
		args = append(args, "--model", m.cfg.Bridge.Model)
	}
	if m.cfg.Bridge.ClaudeEffort != "" {
		args = append(args, "--effort", m.cfg.Bridge.ClaudeEffort)
	}
	return args
}

func (m *OrchestrationManager) withClaudeApprovalArgs(args []string, configPath string) []string {
	if configPath == "" {
		return args
	}
	insertAt := len(args)
	if insertAt > 0 {
		insertAt--
	}
	extra := []string{
		"--permission-mode", "default",
		"--mcp-config", configPath,
		"--permission-prompt-tool", "mcp__codex_bridge__browser_approval",
	}
	next := make([]string, 0, len(args)+len(extra))
	next = append(next, args[:insertAt]...)
	next = append(next, extra...)
	next = append(next, args[insertAt:]...)
	return next
}

func (m *OrchestrationManager) withClaudeStreamApprovalArgs(args []string, configPath string) []string {
	if configPath == "" {
		return args
	}
	extra := []string{
		"--permission-mode", "default",
		"--mcp-config", configPath,
		"--permission-prompt-tool", "mcp__codex_bridge__browser_approval",
	}
	next := make([]string, 0, len(args)+len(extra))
	next = append(next, args...)
	next = append(next, extra...)
	return next
}

func (m *OrchestrationManager) bypassApprovalsAndSandbox() bool {
	return strings.EqualFold(m.cfg.Bridge.ApprovalPolicy, "never") &&
		strings.EqualFold(m.cfg.Bridge.Sandbox, "danger-full-access")
}

func (m *OrchestrationManager) shouldBridgeClaudeApproval() bool {
	return !m.bypassApprovalsAndSandbox()
}

func configureClaudeCommandEnv(cmd *exec.Cmd) {
	if runningAsRoot() {
		appendCommandEnv(cmd, "IS_SANDBOX=1")
	}
}

func (m *OrchestrationManager) compactClaudeInteractiveSession(ctx context.Context, runID, turnID, role string, claude *orchestrationClaudeSession) orchestrationMaintenanceResult {
	return orchestrationMaintenanceResult{Skipped: true, Reason: "Claude Code stream-json does not expose a verified native context-compaction control channel; Bridge skipped automatic /compact to avoid injecting it as a normal user message."}
}

type claudeApprovalSocketRequest struct {
	RequestID string          `json:"requestId,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Command   string          `json:"command,omitempty"`
	CWD       string          `json:"cwd,omitempty"`
	Reason    string          `json:"reason,omitempty"`
	ToolName  string          `json:"toolName,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
}

type claudeApprovalSocketResponse struct {
	RequestID string `json:"requestId,omitempty"`
	Decision  string `json:"decision,omitempty"`
	Error     string `json:"error,omitempty"`
}

func serveClaudeApprovalSocket(ctx context.Context, listener net.Listener, server *claudeApprovalServer) {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			slog.Warn("[bridge] claude approval socket accept failed", "error", err)
			return
		}
		go handleClaudeApprovalSocketConn(ctx, conn, server)
	}
}

func handleClaudeApprovalSocketConn(ctx context.Context, conn net.Conn, server *claudeApprovalServer) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Minute))
	var req claudeApprovalSocketRequest
	if err := json.NewDecoder(conn).Decode(&req); err != nil {
		_ = json.NewEncoder(conn).Encode(claudeApprovalSocketResponse{Error: err.Error()})
		return
	}
	raw := req.Input
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	payload := protocol.ApprovalRequestPayload{
		RequestID: req.RequestID,
		Kind:      req.Kind,
		Command:   req.Command,
		CWD:       req.CWD,
		Reason:    req.Reason,
		Params:    raw,
	}
	if payload.Kind == "" {
		payload.Kind = "claude.permission_prompt"
	}
	if payload.Command == "" {
		payload.Command = claudeApprovalCommand(req.ToolName, raw)
	}
	if payload.Reason == "" {
		payload.Reason = claudeApprovalReason(raw)
	}
	requester := server.requesterForPayload()
	res, err := requester.RequestApproval(ctx, payload)
	if err != nil {
		_ = json.NewEncoder(conn).Encode(claudeApprovalSocketResponse{RequestID: payload.RequestID, Decision: "cancel", Error: err.Error()})
		return
	}
	_ = json.NewEncoder(conn).Encode(claudeApprovalSocketResponse{RequestID: res.RequestID, Decision: res.Decision})
}

func (s *claudeApprovalServer) requesterForPayload() orchestrationApprovalRequester {
	if s == nil {
		return orchestrationApprovalRequester{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.requester
}

func claudeApprovalCommand(toolName string, raw json.RawMessage) string {
	input := map[string]any{}
	_ = json.Unmarshal(raw, &input)
	for _, key := range []string{"command", "cmd", "shellCommand", "tool_input", "input"} {
		if value := stringifyApprovalValue(input[key]); value != "" {
			return value
		}
	}
	name := firstString(input, "tool_name", "toolName", "name")
	switch name {
	case "Bash":
		if value := stringifyApprovalValue(input["command"]); value != "" {
			return value
		}
	case "Read", "Write", "Edit", "MultiEdit":
		if value := stringifyApprovalValue(input["file_path"]); value != "" {
			return name + " " + value
		}
		if value := stringifyApprovalValue(input["path"]); value != "" {
			return name + " " + value
		}
	}
	if name == "" {
		name = toolName
	}
	if name != "" {
		return name
	}
	return trimForPrompt(oneLine(string(raw)), 240)
}

func claudeApprovalReason(raw json.RawMessage) string {
	input := map[string]any{}
	_ = json.Unmarshal(raw, &input)
	for _, key := range []string{"reason", "description", "permission", "prompt", "message"} {
		if value := stringifyApprovalValue(input[key]); value != "" {
			return value
		}
	}
	return ""
}

func stringifyApprovalValue(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			if part := stringifyApprovalValue(item); part != "" {
				parts = append(parts, part)
			}
		}
		return strings.Join(parts, " ")
	case map[string]any:
		for _, key := range []string{"command", "file_path", "path", "description", "reason", "name"} {
			if part := stringifyApprovalValue(v[key]); part != "" {
				return part
			}
		}
	}
	return ""
}

func runningAsRoot() bool {
	if os.Geteuid() == 0 {
		return true
	}
	current, err := user.Current()
	return err == nil && current.Uid == "0"
}

func (m *OrchestrationManager) scanClaudeJSONLWithOptions(stdout io.Reader, runID, turnID, role string, options claudeScanOptions) (string, []RunnerToolEvent, error) {
	if options.ReturnAfterResult && options.IdleCloseAfter == 0 {
		options.IdleCloseAfter = -1
	}
	reader, ok := stdout.(*bufio.Reader)
	if !ok {
		reader = bufio.NewReaderSize(stdout, 64*1024)
	}
	var content strings.Builder
	receivedResult := false
	var tools []RunnerToolEvent
	toolCommands := make(map[string]string)
	toolStarts := make(map[string]time.Time)
	deferredReadStarts := make(map[string]*RunnerToolEvent)
	var pendingFailedTool *RunnerToolEvent
	activeNudges := make(map[string]context.CancelFunc)
	activeTools := make(map[string]bool)
	var idleCloseCancel context.CancelFunc
	streamInputClosed := false
	closeStreamInput := func(reason string) {
		if options.ReturnAfterResult {
			return
		}
		if streamInputClosed {
			return
		}
		streamInputClosed = true
		closeClaudeStreamInput(options.Input)
		if reason != "" {
			m.emit(runID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "info",
				TurnID:   turnID,
				Role:     role,
				CLI:      "claude",
				Content:  reason,
				BridgeNoteData: &protocol.BridgeNoteData{
					Category: "stream-input-idle-close",
				},
				Data: map[string]any{
					"relayOnly": true,
					"category":  "stream-input-idle-close",
				},
			})
		}
	}
	scheduleIdleClose := func() {
		if !options.CanNudge || options.Input == nil || streamInputClosed || len(activeTools) > 0 {
			return
		}
		if idleCloseCancel != nil {
			idleCloseCancel()
		}
		after := options.IdleCloseAfter
		if after <= 0 {
			after = claudeStreamInputIdleCloseAfter
		}
		ctx, cancel := context.WithCancel(context.Background())
		idleCloseCancel = cancel
		go func() {
			timer := time.NewTimer(after)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				closeStreamInput("Bridge closed Claude stream input after an idle window; the CLI process was not interrupted.")
			}
		}()
	}
	defer func() {
		if idleCloseCancel != nil {
			idleCloseCancel()
		}
		for _, cancel := range activeNudges {
			cancel()
		}
	}()
	emitClaudeTool := func(tool *RunnerToolEvent) {
		if tool == nil {
			return
		}
		if tool.ID != "" && tool.Command != "" {
			toolCommands[tool.ID] = tool.Command
		}
		if tool.ID != "" && tool.Command == "" {
			tool.Command = toolCommands[tool.ID]
		}
		if tool.ID != "" && strings.EqualFold(tool.Status, "in_progress") && isClaudeReadCommand(tool.Command) {
			copy := *tool
			copy.Command = tool.Command
			copy.Status = tool.Status
			markWillSuppressOnFailure(&copy)
			deferredReadStarts[tool.ID] = &copy
			stampToolTiming(&copy, toolStarts)
			tools = append(tools, copy)
			m.emitTool(runID, turnID, role, "claude", &copy)
			return
		}
		if isClaudeEmptyPagesReadFailure(tool) {
			delete(deferredReadStarts, tool.ID)
			cancelClaudeDeferredRead(tool)
			stampToolTiming(tool, toolStarts)
			tools = append(tools, *tool)
			m.emitTool(runID, turnID, role, "claude", tool)
			pendingFailedTool = nil
			return
		}
		stampToolTiming(tool, toolStarts)
		if tool.ID != "" {
			if isRunningToolStatus(tool.Status) {
				activeTools[tool.ID] = true
				if idleCloseCancel != nil {
					idleCloseCancel()
					idleCloseCancel = nil
				}
			} else {
				delete(activeTools, tool.ID)
			}
		}
		if options.CanNudge && tool.ID != "" {
			if isRunningToolStatus(tool.Status) && m.longCommandObserverMatches(options.LongCommandObserver, "claude", tool.Command) {
				if activeNudges[tool.ID] == nil {
					activeNudges[tool.ID] = m.scheduleLongCommandObserver(runID, turnID, role, "claude", tool, options)
				}
			} else if !isRunningToolStatus(tool.Status) {
				if cancel := activeNudges[tool.ID]; cancel != nil {
					cancel()
					delete(activeNudges, tool.ID)
				}
			}
		}
		if tool.ID != "" {
			if start := deferredReadStarts[tool.ID]; start != nil {
				delete(deferredReadStarts, tool.ID)
			}
		}
		tools = append(tools, *tool)
		m.emitTool(runID, turnID, role, "claude", tool)
		if runnerToolEventFailed(*tool) {
			copy := *tool
			pendingFailedTool = &copy
		}
		scheduleIdleClose()
	}
	for {
		line, err := readJSONLLine(reader, 32*1024*1024)
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return content.String(), tools, err
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
				return content.String(), tools, errors.New(message)
			}
			for _, tool := range claudeToolEvents(msg) {
				emitClaudeTool(tool)
			}
			if delta := claudeAssistantText(msg); delta != "" {
				content.WriteString(delta)
				pendingFailedTool = nil
				m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "claude", Content: delta})
			}
		case "user":
			for _, tool := range claudeToolEvents(msg) {
				emitClaudeTool(tool)
			}
		case "result":
			receivedResult = true
			if isErr, _ := msg["is_error"].(bool); isErr {
				if text := firstString(msg, "result", "error"); text != "" {
					return content.String(), tools, errors.New(text)
				}
				return content.String(), tools, errors.New("claude returned an error")
			}
			if text := firstString(msg, "result"); text != "" {
				before := content.String()
				delta := ""
				if content.Len() == 0 {
					content.WriteString(text)
					delta = text
				} else {
					delta = appendAgentMessageContent(&content, text)
				}
				if strings.TrimSpace(delta) != "" && strings.TrimSpace(content.String()) != strings.TrimSpace(before) {
					pendingFailedTool = nil
					m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "claude", Content: delta})
				}
			}
			if pendingFailedTool != nil {
				return strings.TrimSpace(content.String()), tools, failedToolWithoutFollowupError("claude", *pendingFailedTool)
			}
			if options.ReturnAfterResult {
				return strings.TrimSpace(content.String()), tools, nil
			}
			closeStreamInput("")
		case "error":
			if message := eventErrorMessage(msg); message != "" {
				return content.String(), tools, errors.New(message)
			}
		}
		scheduleIdleClose()
	}
	if options.ReturnAfterResult && !receivedResult {
		return strings.TrimSpace(content.String()), tools, errors.New("claude stream ended before result")
	}
	if pendingFailedTool != nil {
		return strings.TrimSpace(content.String()), tools, failedToolWithoutFollowupError("claude", *pendingFailedTool)
	}
	return strings.TrimSpace(content.String()), tools, nil
}

func closeClaudeStreamInput(w io.Writer) {
	if closer, ok := w.(io.Closer); ok {
		_ = closer.Close()
	}
}

func (m *OrchestrationManager) scheduleLongCommandObserver(runID, turnID, role, cli string, tool *RunnerToolEvent, options claudeScanOptions) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	after := options.NudgeAfter
	if after <= 0 {
		after = defaultLongCommandObserverAfter
	}
	command := strings.TrimSpace(tool.Command)
	go func() {
		timer := time.NewTimer(after)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		message := longCommandObserverMessage(command, after)
		if options.Input == nil {
			m.emit(runID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "info",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Content:  "Bridge observed a long-running command in " + cli + "; no stdin side-channel is available, so the CLI was not interrupted.",
				BridgeNoteData: &protocol.BridgeNoteData{
					Category:     "long-command-observer-visible-note",
					Command:      command,
					AfterSeconds: int(after.Seconds()),
					InjectedText: message,
				},
				Data: map[string]any{
					"command":      command,
					"afterSeconds": int(after.Seconds()),
					"category":     "long-command-observer-visible-note",
					"injectedText": message,
					"relayOnly":    true,
				},
			})
			return
		}
		if err := writeClaudeStreamUserMessage(options.Input, message); err != nil {
			m.emit(runID, protocol.OrchestrationEventPayload{
				Kind:     "turn.delta",
				Source:   "bridge",
				Severity: "warning",
				TurnID:   turnID,
				Role:     role,
				CLI:      cli,
				Error:    "failed to send long-command observer note to " + cli + ": " + err.Error(),
				BridgeNoteData: &protocol.BridgeNoteData{
					Category:     "long-command-observer-error",
					Command:      command,
					AfterSeconds: int(after.Seconds()),
					InjectedText: message,
				},
			})
			return
		}
		m.emit(runID, protocol.OrchestrationEventPayload{
			Kind:     "turn.delta",
			Source:   "bridge",
			Severity: "info",
			TurnID:   turnID,
			Role:     role,
			CLI:      cli,
			Content:  "Bridge sent a long-command observer note to " + cli + " without interrupting the running CLI turn.",
			BridgeNoteData: &protocol.BridgeNoteData{
				Category:     "long-command-observer-injection",
				Command:      command,
				AfterSeconds: int(after.Seconds()),
				InjectedText: message,
			},
			Data: map[string]any{
				"command":      command,
				"afterSeconds": int(after.Seconds()),
				"category":     "long-command-observer-injection",
				"injectedText": message,
				"relayOnly":    true,
			},
		})
	}()
	return cancel
}

func writeClaudeStreamUserMessage(w io.Writer, text string) error {
	if w == nil {
		return errors.New("claude stream input is unavailable")
	}
	msg := map[string]any{
		"type": "user",
		"message": map[string]any{
			"role": "user",
			"content": []map[string]string{{
				"type": "text",
				"text": text,
			}},
		},
	}
	encoded, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(encoded))
	return err
}

func longCommandObserverMessage(command string, after time.Duration) string {
	command = strings.TrimSpace(command)
	if command == "" {
		command = "the current long-running command"
	}
	return fmt.Sprintf("[Codex Bridge observer note] The command `%s` has been running for about %s. This note was injected by Bridge, not by the user. Do not discard current work and do not restart the command. Check whether it has already produced enough evidence; if it is still running too long, report the latest output/log and continue with available information.", command, after.Round(time.Second))
}

func (m *OrchestrationManager) longCommandObserverConfig() longCommandObserverConfig {
	cfg := m.cfg.Bridge.LongCommandObserver
	out := longCommandObserverConfig{
		Enabled:         cfg.Enabled,
		After:           cfg.After.Duration,
		CommandPatterns: append([]string(nil), cfg.CommandPatterns...),
		AppliesTo:       append([]string(nil), cfg.AppliesTo...),
	}
	if out.After <= 0 {
		out.After = defaultLongCommandObserverAfter
	}
	if len(out.CommandPatterns) == 0 {
		out.CommandPatterns = []string{"python -m slow_build"}
	}
	if len(out.AppliesTo) == 0 {
		out.AppliesTo = []string{"claude", "codex"}
	}
	return out
}

func (m *OrchestrationManager) longCommandObserverMatches(observer longCommandObserverConfig, cli, command string) bool {
	if !observer.Enabled || !observerAppliesTo(observer, cli) {
		return false
	}
	command = strings.TrimSpace(command)
	if command == "" {
		return false
	}
	for _, pattern := range observer.CommandPatterns {
		if commandPatternMatches(pattern, command) {
			return true
		}
	}
	return false
}

func observerAppliesTo(observer longCommandObserverConfig, cli string) bool {
	cli = strings.ToLower(strings.TrimSpace(cli))
	for _, allowed := range observer.AppliesTo {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "*" || allowed == cli {
			return true
		}
	}
	return false
}

func commandPatternMatches(pattern, command string) bool {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return false
	}
	if re, err := regexp.Compile("(?i)" + pattern); err == nil && re.MatchString(command) {
		return true
	}
	return strings.Contains(strings.ToLower(command), strings.ToLower(pattern))
}

func isClaudeReadCommand(command string) bool {
	return strings.HasPrefix(strings.TrimSpace(command), "Read ")
}

func isClaudeEmptyPagesReadFailure(tool *RunnerToolEvent) bool {
	if tool == nil || !strings.EqualFold(tool.Status, "failed") || !isClaudeReadCommand(tool.Command) {
		return false
	}
	output := tool.Output
	return strings.Contains(output, `Invalid pages parameter: ""`) && strings.Contains(output, "Pages are 1-indexed")
}

func markWillSuppressOnFailure(tool *RunnerToolEvent) {
	if tool != nil {
		tool.WillSuppressOnFailure = true
	}
}

func cancelClaudeDeferredRead(tool *RunnerToolEvent) {
	if tool != nil {
		tool.Status = "cancelled"
		tool.Output = ""
	}
}

func stampToolTiming(tool *RunnerToolEvent, starts map[string]time.Time) {
	if tool == nil {
		return
	}
	now := time.Now()
	if isRunningToolStatus(tool.Status) {
		if tool.StartedAt.IsZero() {
			tool.StartedAt = now
		}
		if tool.ID != "" && starts[tool.ID].IsZero() {
			starts[tool.ID] = tool.StartedAt
		}
		return
	}
	if tool.ID != "" {
		if start := starts[tool.ID]; !start.IsZero() {
			tool.StartedAt = start
			delete(starts, tool.ID)
		}
	}
	if tool.StartedAt.IsZero() {
		tool.StartedAt = now
	}
	if tool.CompletedAt.IsZero() {
		tool.CompletedAt = now
	}
}

func isRunningToolStatus(status string) bool {
	switch normalizeToolStatus(status) {
	case "in_progress", "running", "started":
		return true
	default:
		return false
	}
}

func runnerToolEventFailed(tool RunnerToolEvent) bool {
	status := normalizeToolStatus(tool.Status)
	if status == "failed" || status == "error" {
		return true
	}
	return tool.ExitCode != nil && *tool.ExitCode != 0
}

func failedToolWithoutFollowupError(cli string, tool RunnerToolEvent) error {
	command := strings.TrimSpace(tool.Command)
	if command == "" {
		command = strings.TrimSpace(tool.ID)
	}
	if command == "" {
		command = "a command"
	}
	detail := strings.TrimSpace(tool.Output)
	if detail == "" {
		detail = strings.TrimSpace(tool.Status)
	}
	if tool.ExitCode != nil {
		exit := fmt.Sprintf("exit code %d", *tool.ExitCode)
		if detail == "" {
			detail = exit
		} else {
			detail += "\n" + exit
		}
	}
	if detail == "" {
		return fmt.Errorf("%s turn ended after %s failed without a follow-up response", cli, command)
	}
	return fmt.Errorf("%s turn ended after %s failed without a follow-up response: %s", cli, command, trimForPrompt(detail, 1000))
}

func normalizeToolStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return ""
	}
	status = strings.ReplaceAll(status, "-", "_")
	status = regexp.MustCompile(`([a-z0-9])([A-Z])`).ReplaceAllString(status, `${1}_${2}`)
	return strings.ToLower(status)
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
