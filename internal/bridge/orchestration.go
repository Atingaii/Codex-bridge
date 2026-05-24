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
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

type OrchestrationManager struct {
	cfg       *config.Config
	mu        sync.Mutex
	runs      map[string]context.CancelFunc
	output    chan<- protocol.Envelope
	pending   []protocol.Envelope
	approvals map[string]orchestrationApproval
}

type orchestrationApproval struct {
	runID string
	ch    chan protocol.ApprovalResponsePayload
}

type orchestrationTurn struct {
	TurnID        string
	Role          string
	CLI           string
	Msg           string
	Content       string
	Handoff       string
	HandoffFields orchestrationHandoffFields
	Err           string
	Tools         []RunnerToolEvent
	Verifier      bool
}

type orchestrationHandoffFields struct {
	Status   string
	Changed  string
	Verified string
	Next     string
	Risks    string
}

var safeOrchestrationFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func NewOrchestrationManager(cfg *config.Config) *OrchestrationManager {
	return &OrchestrationManager{
		cfg:       cfg,
		runs:      make(map[string]context.CancelFunc),
		approvals: make(map[string]orchestrationApproval),
	}
}

func (m *OrchestrationManager) AttachOut(out chan<- protocol.Envelope) {
	m.mu.Lock()
	pending := append([]protocol.Envelope(nil), m.pending...)
	m.pending = nil
	for _, env := range pending {
		send(out, env)
	}
	m.output = out
	m.mu.Unlock()
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
			m.cancelApprovals(payload.RunID)
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
	m.cancelApprovals(runID)
}

func (m *OrchestrationManager) CloseAll() {
	m.mu.Lock()
	var cancels []context.CancelFunc
	runIDs := make([]string, 0, len(m.runs))
	for runID, cancel := range m.runs {
		cancels = append(cancels, cancel)
		runIDs = append(runIDs, runID)
		delete(m.runs, runID)
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	for _, runID := range runIDs {
		m.cancelApprovals(runID)
	}
}

type orchestrationApprovalRequester struct {
	manager *OrchestrationManager
	runID   string
	turnID  string
	role    string
	cli     string
	cwd     string
}

func (r orchestrationApprovalRequester) RequestApproval(ctx context.Context, req protocol.ApprovalRequestPayload) (protocol.ApprovalResponsePayload, error) {
	if req.RequestID == "" {
		req.RequestID = fmt.Sprintf("apr_%d", time.Now().UnixNano())
	}
	req.RunID = r.runID
	req.TurnID = r.turnID
	if req.CWD == "" {
		req.CWD = r.cwd
	}
	if req.Kind == "" {
		req.Kind = "orchestration.approval"
	}
	ch := make(chan protocol.ApprovalResponsePayload, 1)
	m := r.manager
	m.mu.Lock()
	if m.approvals == nil {
		m.approvals = make(map[string]orchestrationApproval)
	}
	m.approvals[req.RequestID] = orchestrationApproval{runID: r.runID, ch: ch}
	m.mu.Unlock()

	m.send(protocol.MustEnvelope(protocol.TypeApprovalRequest, "", req))
	select {
	case res := <-ch:
		return res, nil
	case <-ctx.Done():
		m.removeApproval(req.RequestID)
		return protocol.ApprovalResponsePayload{}, ctx.Err()
	}
}

func (m *OrchestrationManager) ApprovalResponse(res protocol.ApprovalResponsePayload) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	pending := m.approvals[res.RequestID]
	if pending.ch == nil {
		return false
	}
	delete(m.approvals, res.RequestID)
	pending.ch <- res
	return true
}

func (m *OrchestrationManager) removeApproval(requestID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.approvals, requestID)
}

func (m *OrchestrationManager) cancelApprovals(runID string) {
	m.mu.Lock()
	var pending []orchestrationApproval
	for requestID, approval := range m.approvals {
		if approval.runID == runID {
			pending = append(pending, approval)
			delete(m.approvals, requestID)
		}
	}
	m.mu.Unlock()
	for _, approval := range pending {
		approval.ch <- protocol.ApprovalResponsePayload{Decision: "cancel"}
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
		content, tools, err := m.runCLI(ctx, payload, turnID, role, cli, prompt)
		record := newOrchestrationTurnRecord(turnID, role, cli, content, tools)
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
		if summary := finalTurnFallbackSummary(payload.Prompt, turn, maxTurns, history, record); summary != "" {
			delta := summary
			if strings.TrimSpace(content) != "" {
				delta = "\n\n" + summary
				record.Content = strings.TrimSpace(content + "\n\n" + summary)
			} else {
				record.Content = summary
			}
			record.Msg = extractMsg(record.Content)
			record.Handoff = extractHandoff(record.Content)
			record.HandoffFields = parseHandoffFields(record.Handoff)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:    "turn.delta",
				TurnID:  turnID,
				Role:    role,
				CLI:     cli,
				Content: delta,
			})
		}
		history = append(history, record)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:   "turn.end",
			TurnID: turnID,
			Role:   role,
			CLI:    cli,
			Status: "success",
		})
		if turn >= 2 && resolvedHandoffReady(record.Content) {
			break
		}
	}
	if m.shouldRunFinalVerifier(history) {
		if err := ctx.Err(); err != nil {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "run.cancelled",
				Status: store.OrchestrationCanceled,
				Error:  "canceled",
			})
			return
		}
		role, cli := verifierRoleCLI(mode, history)
		turnID := fmt.Sprintf("%s-verifier", payload.RunID)
		if payload.PromptSeq > 0 {
			turnID = fmt.Sprintf("%s-p%03d-verifier", payload.RunID, payload.PromptSeq)
		}
		prompt := composeFinalVerifierPrompt(mode, payload.Prompt, payload.Context, payload.Resume, role, cli, history)
		m.emit(payload.RunID, protocol.OrchestrationEventPayload{
			Kind:    "turn.start",
			TurnID:  turnID,
			Role:    role,
			CLI:     cli,
			Content: "final verifier via " + cli,
		})
		content, tools, err := m.runCLI(ctx, payload, turnID, role, cli, prompt)
		record := newOrchestrationTurnRecord(turnID, role, cli, content, tools)
		record.Verifier = true
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
		} else {
			history = append(history, record)
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{
				Kind:   "turn.end",
				TurnID: turnID,
				Role:   role,
				CLI:    cli,
				Status: "success",
			})
		}
	}

	m.emit(payload.RunID, protocol.OrchestrationEventPayload{
		Kind:    "run.end",
		Status:  store.OrchestrationCompleted,
		Content: "Orchestration completed.",
	})
}

func (m *OrchestrationManager) runCLI(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, cli, prompt string) (string, []RunnerToolEvent, error) {
	switch cli {
	case "claude":
		return m.runClaude(ctx, payload, turnID, role, prompt)
	default:
		return m.runCodex(ctx, payload, turnID, role, prompt)
	}
}

func (m *OrchestrationManager) runCodex(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt string) (string, []RunnerToolEvent, error) {
	if m.shouldRunCodexAppServer() {
		return m.runCodexAppServer(ctx, payload, turnID, role, prompt)
	}
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
		return "", nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", nil, err
	}
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	_, _ = io.WriteString(stdin, prompt)
	_ = stdin.Close()

	content, tools, scanErr := m.scanCodexJSONL(stdout, payload.RunID, turnID, role)
	waitErr := cmd.Wait()
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
	if content == "" {
		content = strings.TrimSpace(stderr.String())
	}
	return content, tools, nil
}

func (m *OrchestrationManager) runCodexAppServer(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt string) (string, []RunnerToolEvent, error) {
	runner := NewCodexAppServerRunner(m.cfg)
	defer runner.Close()
	var tools []RunnerToolEvent
	result, err := runner.Prompt(ctx, RunnerRequest{
		Content:  prompt,
		RunID:    payload.RunID,
		PromptID: turnID,
		CWD:      m.cwd(payload),
		Approvals: orchestrationApprovalRequester{
			manager: m,
			runID:   payload.RunID,
			turnID:  turnID,
			role:    role,
			cli:     "codex",
			cwd:     m.cwd(payload),
		},
	}, func(update RunnerUpdate) {
		if update.Delta != "" {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: update.Delta})
		}
		if update.Content != "" {
			m.emit(payload.RunID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: update.Content})
		}
		if update.Tool != nil {
			tools = append(tools, *update.Tool)
			m.emitTool(payload.RunID, turnID, role, "codex", update.Tool)
		}
	})
	return strings.TrimSpace(result.Content), tools, err
}

func (m *OrchestrationManager) runClaude(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role, prompt string) (string, []RunnerToolEvent, error) {
	approvalServer, cleanup, err := m.prepareClaudeApprovalServer(ctx, payload, turnID, role)
	if err != nil {
		return "", nil, err
	}
	defer cleanup()
	args := m.claudeArgs(payload, prompt)
	if approvalServer != nil {
		args = m.withClaudeApprovalArgs(args, approvalServer.configPath)
	}
	cmd := exec.CommandContext(ctx, m.claudePath(), args...)
	if cwd := m.cwd(payload); cwd != "" {
		cmd.Dir = cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	content, tools, scanErr := m.scanClaudeJSONL(stdout, payload.RunID, turnID, role)
	waitErr := cmd.Wait()
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

type claudeApprovalServer struct {
	configPath string
}

func (m *OrchestrationManager) prepareClaudeApprovalServer(ctx context.Context, payload protocol.OrchestrationStartPayload, turnID, role string) (*claudeApprovalServer, func(), error) {
	if !m.shouldBridgeClaudeApproval() {
		return nil, func() {}, nil
	}
	tmpDir, err := os.MkdirTemp("", "codex-bridge-claude-approval-*")
	if err != nil {
		return nil, nil, fmt.Errorf("create claude approval temp dir: %w", err)
	}
	cleanup := func() {
		_ = os.RemoveAll(tmpDir)
	}
	socketPath := filepath.Join(tmpDir, "approval.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		cleanup()
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
	go serveClaudeApprovalSocket(serverCtx, listener, requester)

	exe, err := os.Executable()
	if err != nil {
		listener.Close()
		cancel()
		cleanup()
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
		cleanup()
		return nil, nil, fmt.Errorf("marshal claude approval mcp config: %w", err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		listener.Close()
		cancel()
		cleanup()
		return nil, nil, fmt.Errorf("write claude approval mcp config: %w", err)
	}
	return &claudeApprovalServer{configPath: configPath}, func() {
		cancel()
		listener.Close()
		cleanup()
	}, nil
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

func (m *OrchestrationManager) bypassApprovalsAndSandbox() bool {
	return strings.EqualFold(m.cfg.Bridge.ApprovalPolicy, "never") &&
		strings.EqualFold(m.cfg.Bridge.Sandbox, "danger-full-access")
}

func (m *OrchestrationManager) shouldBridgeClaudeApproval() bool {
	return !m.bypassApprovalsAndSandbox()
}

func (m *OrchestrationManager) shouldRunCodexAppServer() bool {
	return !m.bypassApprovalsAndSandbox()
}

func (m *OrchestrationManager) shouldRunFinalVerifier(history []orchestrationTurn) bool {
	if len(history) == 0 {
		return false
	}
	last := history[len(history)-1]
	if strings.EqualFold(last.HandoffFields.Status, "resolved") && meaningfulHandoffValue(last.HandoffFields.Verified) && !meaningfulHandoffValue(last.HandoffFields.Changed) && !hasRiskyHandoff(last) && failedCommandCount(history) == 0 {
		return false
	}
	for _, item := range history {
		if item.Err != "" || failedCommandCount([]orchestrationTurn{item}) > 0 || hasRiskyHandoff(item) || meaningfulHandoffValue(item.HandoffFields.Changed) {
			return true
		}
	}
	return false
}

func hasRiskyHandoff(item orchestrationTurn) bool {
	if meaningfulHandoffValue(item.HandoffFields.Risks) {
		return true
	}
	status := strings.ToLower(strings.TrimSpace(item.HandoffFields.Status))
	return status == "blocked"
}

func verifierRoleCLI(mode string, history []orchestrationTurn) (string, string) {
	lastCLI := ""
	if len(history) > 0 {
		lastCLI = history[len(history)-1].CLI
	}
	if strings.EqualFold(lastCLI, "codex") {
		if mode == "debate" {
			return "proposer", "claude"
		}
		return "implementer", "claude"
	}
	return "verifier", "codex"
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

func serveClaudeApprovalSocket(ctx context.Context, listener net.Listener, requester orchestrationApprovalRequester) {
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
		go handleClaudeApprovalSocketConn(ctx, conn, requester)
	}
}

func handleClaudeApprovalSocketConn(ctx context.Context, conn net.Conn, requester orchestrationApprovalRequester) {
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
	res, err := requester.RequestApproval(ctx, payload)
	if err != nil {
		_ = json.NewEncoder(conn).Encode(claudeApprovalSocketResponse{RequestID: payload.RequestID, Decision: "cancel", Error: err.Error()})
		return
	}
	_ = json.NewEncoder(conn).Encode(claudeApprovalSocketResponse{RequestID: res.RequestID, Decision: res.Decision})
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

func (m *OrchestrationManager) scanCodexJSONL(stdout io.Reader, runID, turnID, role string) (string, []RunnerToolEvent, error) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	var content strings.Builder
	var eventErr string
	var tools []RunnerToolEvent
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
				if text := agentMessageText(item); text != "" {
					if delta := appendAgentMessageContent(&content, text); delta != "" {
						m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "codex", Content: delta})
					}
				}
			}
			if itemType == "command_execution" || itemType == "commandExecution" {
				if tool := commandExecutionEvent(item); tool != nil {
					tools = append(tools, *tool)
					m.emitTool(runID, turnID, role, "codex", tool)
				}
			}
		case "item.started", "item.updated":
			item, _ := msg["item"].(map[string]any)
			itemType, _ := item["type"].(string)
			if itemType == "command_execution" || itemType == "commandExecution" {
				if tool := commandExecutionEvent(item); tool != nil {
					tools = append(tools, *tool)
					m.emitTool(runID, turnID, role, "codex", tool)
				}
			}
		}
	}
	if eventErr != "" {
		return content.String(), tools, errors.New(eventErr)
	}
	return strings.TrimSpace(content.String()), tools, nil
}

func (m *OrchestrationManager) scanClaudeJSONL(stdout io.Reader, runID, turnID, role string) (string, []RunnerToolEvent, error) {
	reader := bufio.NewReaderSize(stdout, 64*1024)
	var content strings.Builder
	var tools []RunnerToolEvent
	toolCommands := make(map[string]string)
	deferredReadStarts := make(map[string]*RunnerToolEvent)
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
			deferredReadStarts[tool.ID] = &copy
			return
		}
		if isClaudeEmptyPagesReadFailure(tool) {
			delete(deferredReadStarts, tool.ID)
			return
		}
		if tool.ID != "" {
			if start := deferredReadStarts[tool.ID]; start != nil {
				tools = append(tools, *start)
				m.emitTool(runID, turnID, role, "claude", start)
				delete(deferredReadStarts, tool.ID)
			}
		}
		tools = append(tools, *tool)
		m.emitTool(runID, turnID, role, "claude", tool)
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
				m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "claude", Content: delta})
			}
		case "user":
			for _, tool := range claudeToolEvents(msg) {
				emitClaudeTool(tool)
			}
		case "result":
			if isErr, _ := msg["is_error"].(bool); isErr {
				if text := firstString(msg, "result", "error"); text != "" {
					return content.String(), tools, errors.New(text)
				}
				return content.String(), tools, errors.New("claude returned an error")
			}
			if text := firstString(msg, "result"); text != "" && content.Len() == 0 {
				content.WriteString(text)
				m.emit(runID, protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: turnID, Role: role, CLI: "claude", Content: text})
			}
		case "error":
			if message := eventErrorMessage(msg); message != "" {
				return content.String(), tools, errors.New(message)
			}
		}
	}
	return strings.TrimSpace(content.String()), tools, nil
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
	buffered := false
	if out == nil && env.Type == protocol.TypeOrchestrationEvent {
		m.pending = append(m.pending, env)
		if len(m.pending) > 1000 {
			m.pending = m.pending[len(m.pending)-1000:]
		}
		buffered = true
	}
	m.mu.Unlock()
	if out == nil {
		if buffered {
			slog.Warn("[bridge] orchestration event buffered: bridge disconnected", "type", env.Type)
		} else {
			slog.Warn("[bridge] orchestration event dropped: bridge disconnected", "type", env.Type)
		}
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

func nextRoleCLI(mode string, turn int) (string, string) {
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

func newOrchestrationTurnRecord(turnID, role, cli, content string, tools []RunnerToolEvent) orchestrationTurn {
	handoff := extractHandoff(content)
	return orchestrationTurn{
		TurnID:        turnID,
		Role:          role,
		CLI:           cli,
		Content:       content,
		Msg:           extractMsg(content),
		Handoff:       handoff,
		HandoffFields: parseHandoffFields(handoff),
		Tools:         tools,
	}
}

func resolvedHandoffReady(content string) bool {
	handoff := strings.ToLower(extractHandoff(content))
	if !strings.Contains(handoff, "status=resolved") {
		return false
	}
	return hasUserVisibleConclusion(content)
}

func hasUserVisibleConclusion(content string) bool {
	visible := strings.TrimSpace(strings.Replace(content, extractHandoff(content), "", 1))
	if visible == "" {
		return false
	}
	lower := strings.ToLower(visible)
	signals := []string{
		"final conclusion", "conclusion:", "summary:", "verified", "verification", "completed", "remaining risk",
		"最终结论", "结论：", "总结：", "验证", "通过", "完成", "剩余风险",
	}
	for _, signal := range signals {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func finalTurnFallbackSummary(userPrompt string, turn, maxTurns int, history []orchestrationTurn, current orchestrationTurn) string {
	if turn != maxTurns || !finalResponseNeedsFallback(current.Content) {
		return ""
	}
	zh := !explicitEnglishResponseRequested(userPrompt)
	prior := latestMeaningfulConclusion(history)
	verified := completedVerificationSummaries([]orchestrationTurn{current}, zh, 3)
	if len(verified) == 0 {
		verified = completedVerificationSummaries(history, zh, 3)
	}
	failed := failedCommandCount(append(history, current))

	var b strings.Builder
	if zh {
		b.WriteString("最终结论：本次编排已完成。")
		if prior != "" {
			b.WriteString("\n\n结果概览：")
			b.WriteString(prior)
		}
		if len(verified) > 0 {
			b.WriteString("\n\n已验证：")
			for _, item := range verified {
				b.WriteString("\n- ")
				b.WriteString(item)
			}
		} else {
			b.WriteString("\n\n已验证：没有可提炼的命令摘要；可展开命令详情审计原始事件。")
		}
		if failed > 0 {
			b.WriteString("\n\n剩余风险：命令详情里仍有失败命令，需要结合具体输出判断。")
		} else {
			b.WriteString("\n\n剩余风险：未发现新的阻塞问题；如需审计细节，可展开命令详情查看原始事件。")
		}
		return b.String()
	}

	b.WriteString("Final conclusion: this orchestration completed.")
	if prior != "" {
		b.WriteString("\n\nResult overview: ")
		b.WriteString(prior)
	}
	if len(verified) > 0 {
		b.WriteString("\n\nVerified:")
		for _, item := range verified {
			b.WriteString("\n- ")
			b.WriteString(item)
		}
	} else {
		b.WriteString("\n\nVerified: no concise command summary was available; expand command details to audit raw events.")
	}
	if failed > 0 {
		b.WriteString("\n\nRemaining risk: some command events failed; check command details for raw output.")
	} else {
		b.WriteString("\n\nRemaining risk: no new blocking issue was reported. Expand command details to audit raw events.")
	}
	return b.String()
}

func explicitEnglishResponseRequested(value string) bool {
	lower := strings.ToLower(value)
	for _, signal := range []string{
		"reply in english",
		"respond in english",
		"answer in english",
		"use english",
		"用英文",
		"使用英文",
		"英文回复",
	} {
		if strings.Contains(lower, signal) {
			return true
		}
	}
	return false
}

func finalResponseNeedsFallback(content string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return true
	}
	lower := strings.ToLower(content)
	progressStarts := []string{
		"我会", "我将", "接下来", "正在", "i will", "i'll", "i am going to", "next i",
	}
	for _, prefix := range progressStarts {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	signals := []string{
		"final", "conclusion", "summary", "verified", "verification", "passed", "completed", "remaining", "risk",
		"最终", "结论", "总结", "确认", "验证", "通过", "完成", "剩余", "风险", "正确",
	}
	count := 0
	for _, signal := range signals {
		if strings.Contains(lower, signal) {
			count++
		}
	}
	if count >= 2 {
		return false
	}
	return count < 2 && len([]rune(content)) < 320
}

func latestMeaningfulConclusion(history []orchestrationTurn) string {
	for i := len(history) - 1; i >= 0; i-- {
		content := strings.TrimSpace(history[i].Content)
		if content == "" {
			continue
		}
		lower := strings.ToLower(content)
		if strings.Contains(lower, "结论") || strings.Contains(lower, "确认") ||
			strings.Contains(lower, "通过") || strings.Contains(lower, "正确") ||
			strings.Contains(lower, "conclusion") || strings.Contains(lower, "verified") ||
			strings.Contains(lower, "passed") || strings.Contains(lower, "correct") {
			return trimForPrompt(oneLine(content), 700)
		}
	}
	return ""
}

func completedCommandSummaries(turns []orchestrationTurn, max int) []string {
	commands := commandStates(turns)
	var out []string
	for i := len(commands) - 1; i >= 0 && len(out) < max; i-- {
		command := commands[i]
		if !strings.EqualFold(command.Status, "completed") {
			continue
		}
		out = append(out, formatCommandSummary(command))
	}
	return reverseStrings(out)
}

func completedVerificationSummaries(turns []orchestrationTurn, zh bool, max int) []string {
	commands := commandStates(turns)
	var readFiles []string
	var commandLabels []string
	for _, command := range commands {
		if !strings.EqualFold(command.Status, "completed") {
			continue
		}
		label := strings.TrimSpace(command.Command)
		if isClaudeReadCommand(label) {
			path := strings.TrimSpace(strings.TrimPrefix(label, "Read "))
			if path != "" {
				name := filepath.Base(path)
				if name != "." && name != string(filepath.Separator) {
					readFiles = append(readFiles, name)
				}
			}
			continue
		}
		if label != "" {
			commandLabels = append(commandLabels, trimForPrompt(oneLine(label), 120))
		}
	}

	var out []string
	if len(readFiles) > 0 && len(out) < max {
		if zh {
			out = append(out, fmt.Sprintf("读取并检查了 %d 个文件：%s。", len(readFiles), formatInlineList(readFiles, "、")))
		} else {
			out = append(out, fmt.Sprintf("Read and checked %d file(s): %s.", len(readFiles), formatInlineList(readFiles, ", ")))
		}
	}
	for _, label := range commandLabels {
		if len(out) >= max {
			break
		}
		if zh {
			out = append(out, fmt.Sprintf("执行完成：`%s`。", label))
		} else {
			out = append(out, fmt.Sprintf("Completed `%s`.", label))
		}
	}
	return out
}

func formatInlineList(values []string, separator string) string {
	var out []string
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, "`"+value+"`")
	}
	if separator == "" {
		separator = ", "
	}
	return strings.Join(out, separator)
}

func failedCommandCount(turns []orchestrationTurn) int {
	count := 0
	for _, command := range commandStates(turns) {
		if commandFailed(command) {
			count++
		}
	}
	return count
}

func failedCommandSummaries(turns []orchestrationTurn, max int) []string {
	commands := commandStates(turns)
	var out []string
	for i := len(commands) - 1; i >= 0 && len(out) < max; i-- {
		command := commands[i]
		if commandFailed(command) {
			out = append(out, formatCommandSummary(command))
		}
	}
	return reverseStrings(out)
}

func commandFailed(command orchestrationCommandState) bool {
	if strings.EqualFold(command.Status, "failed") || strings.EqualFold(command.Status, "error") {
		return true
	}
	return command.ExitCode != nil && *command.ExitCode != 0
}

type orchestrationCommandState struct {
	ID       string
	Status   string
	Command  string
	Output   string
	ExitCode *int
}

func commandStates(turns []orchestrationTurn) []orchestrationCommandState {
	var states []orchestrationCommandState
	indexes := map[string]int{}
	for _, turn := range turns {
		for _, tool := range turn.Tools {
			toolKey := tool.ID
			if toolKey == "" {
				toolKey = tool.Command
			}
			if toolKey == "" {
				toolKey = fmt.Sprintf("tool-%d", len(states))
			}
			key := turn.TurnID + ":" + toolKey
			index, ok := indexes[key]
			if !ok {
				index = len(states)
				indexes[key] = index
				states = append(states, orchestrationCommandState{ID: tool.ID})
			}
			state := states[index]
			if tool.ID != "" {
				state.ID = tool.ID
			}
			if tool.Status != "" {
				state.Status = tool.Status
			}
			if tool.Command != "" {
				state.Command = tool.Command
			}
			if tool.Output != "" {
				state.Output = tool.Output
			}
			if tool.ExitCode != nil {
				state.ExitCode = tool.ExitCode
			}
			states[index] = state
		}
	}
	return states
}

func formatCommandSummary(command orchestrationCommandState) string {
	label := strings.TrimSpace(command.Command)
	if label == "" {
		label = "command"
	}
	status := command.Status
	if status == "" {
		status = "completed"
	}
	parts := []string{fmt.Sprintf("`%s` %s", label, status)}
	if command.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *command.ExitCode))
	}
	if output := trimForPrompt(oneLine(command.Output), 160); output != "" {
		parts = append(parts, "output: "+output)
	}
	return strings.Join(parts, "; ")
}

func reverseStrings(values []string) []string {
	for i, j := 0, len(values)-1; i < j; i, j = i+1, j-1 {
		values[i], values[j] = values[j], values[i]
	}
	return values
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func containsCJK(value string) bool {
	for _, r := range value {
		if r >= '\u4e00' && r <= '\u9fff' {
			return true
		}
	}
	return false
}

const orchestrationHandoffContract = "Handoff: status=<needs_next|blocked|resolved>; changed=<files or none>; verified=<commands or none>; next=<one action>; risks=<open issue or none>"
const orchestrationMsgContract = "Msg: to=<next-role|user>; intent=<implement|review|challenge|final>; need=<one request or none>"
const orchestrationLanguageRule = "Language rule: write all user-visible prose in Chinese by default unless the user explicitly asks for another language. Keep the machine-readable Msg: and Handoff: field names and values in the required English shape."

func composeOrchestrationPrompt(mode, userPrompt, contextSummary string, resume bool, role, cli string, turn, maxTurns int, history []orchestrationTurn) string {
	var b strings.Builder
	b.WriteString("You are participating in a local CLI orchestration run.\n")
	b.WriteString("Use your native file, shell, MCP, and skill capabilities when useful. Do not assume the other CLI can see your private reasoning.\n\n")
	toRole, toCLI := nextRoleCLI(mode, turn)
	if turn >= maxTurns {
		toRole, toCLI = "user", ""
	}
	b.WriteString(fmt.Sprintf("From: %s/%s\n", role, cli))
	if toCLI != "" {
		b.WriteString(fmt.Sprintf("To: %s/%s\n", toRole, toCLI))
	} else {
		b.WriteString("To: user\n")
	}
	b.WriteString(fmt.Sprintf("Mode: %s\n\n", mode))
	b.WriteString(orchestrationLanguageRule)
	b.WriteString("\n\n")
	if resume {
		b.WriteString("This is a continuation of the same user-visible orchestration conversation. Maintain continuity with the compacted context, while treating the latest user task as authoritative.\n\n")
	}
	if mode == "debate" {
		if role == "proposer" {
			b.WriteString("Strategy: evidence-focused debate. Keep claims testable, cite files or command results, and avoid repeating the full transcript.\n")
			b.WriteString("Role: proposer. State the strongest concrete thesis or patch, make progress if needed, and leave a falsifiable handoff for the critic.\n")
		} else {
			b.WriteString("Strategy: evidence-focused debate. Keep claims testable, cite files or command results, and avoid repeating the full transcript.\n")
			b.WriteString("Role: critic. Try to falsify the proposer with concrete counterexamples, command output, or code evidence. Fix clear issues when cheaper than describing them.\n")
		}
	} else {
		if role == "implementer" {
			b.WriteString("Strategy: builder-reviewer collaboration. Optimize for shared workspace progress with short, auditable handoffs.\n")
			b.WriteString("Role: implementer. Make concrete progress on the task, edit files when appropriate, and leave only the key state the reviewer needs.\n")
		} else {
			b.WriteString("Strategy: builder-reviewer collaboration. Optimize for shared workspace progress with short, auditable handoffs.\n")
			b.WriteString("Role: reviewer. Independently inspect the implementer's result, fix obvious issues, and verify with focused commands when appropriate.\n")
		}
	}
	b.WriteString(fmt.Sprintf("Turn: %d of %d. CLI: %s.\n\n", turn, maxTurns, cli))
	b.WriteString("Token budget rules: do not restate the full history, do not quote large files, and keep inter-agent notes compact. Prefer file paths, command names, and exact unresolved blockers.\n")
	b.WriteString("End your visible response with two compact machine-scannable lines in exactly these shapes:\n")
	b.WriteString(orchestrationMsgContract)
	b.WriteByte('\n')
	b.WriteString(orchestrationHandoffContract)
	b.WriteString("\nUse status=resolved only when you also give a concise user-visible conclusion before the handoff.\n\n")
	if strings.TrimSpace(contextSummary) != "" {
		b.WriteString("Compacted context from earlier tasks in this conversation:\n")
		b.WriteString(trimForPrompt(contextSummary, 14000))
		b.WriteString("\n\n")
	}
	b.WriteString("Original user task:\n")
	b.WriteString(userPrompt)
	b.WriteString("\n\n")
	if len(history) > 0 {
		b.WriteString("Compact prior-turn handoffs:\n")
		for _, item := range history {
			b.WriteString(formatCompactPriorTurn(item))
		}
		b.WriteByte('\n')
	}
	if turn == maxTurns {
		b.WriteString("This is the final scheduled turn. Summarize the final state, verification results, and remaining risks.\n")
		b.WriteString("Return a concise user-visible final answer. Do not rely on command logs alone; state what was accomplished, what was verified, and any remaining issue.\n")
	}
	return b.String()
}

func composeFinalVerifierPrompt(mode, userPrompt, contextSummary string, resume bool, role, cli string, history []orchestrationTurn) string {
	var b strings.Builder
	b.WriteString("You are the lightweight final verifier for a local CLI orchestration run.\n")
	b.WriteString("Inspect only the reported changes, failed commands, and unresolved risks. Avoid broad new work; make a small fix only if it is clearly required to complete verification.\n\n")
	b.WriteString(fmt.Sprintf("From: %s/%s\n", role, cli))
	b.WriteString("To: user\n")
	b.WriteString(fmt.Sprintf("Mode: %s\n\n", mode))
	b.WriteString(orchestrationLanguageRule)
	b.WriteString("\n\n")
	if resume {
		b.WriteString("This is a continuation of the same user-visible orchestration conversation. Prefer the latest user task over older details.\n\n")
	}
	b.WriteString("End your visible response with a concise final conclusion and the same compact lines:\n")
	b.WriteString(orchestrationMsgContract)
	b.WriteByte('\n')
	b.WriteString(orchestrationHandoffContract)
	b.WriteString("\n\n")
	if strings.TrimSpace(contextSummary) != "" {
		b.WriteString("Compacted context from earlier tasks in this conversation:\n")
		b.WriteString(trimForPrompt(contextSummary, 6000))
		b.WriteString("\n\n")
	}
	b.WriteString("Original user task:\n")
	b.WriteString(userPrompt)
	b.WriteString("\n\nStructured prior-turn state:\n")
	for _, item := range history {
		b.WriteString(formatCompactPriorTurn(item))
	}
	if failures := failedCommandSummaries(history, 4); len(failures) > 0 {
		b.WriteString("\nFailed or suspicious command outcomes:\n")
		for _, failure := range failures {
			b.WriteString("- ")
			b.WriteString(failure)
			b.WriteByte('\n')
		}
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

func formatCompactPriorTurn(item orchestrationTurn) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("- [%s via %s] ", item.Role, item.CLI))
	if item.Msg != "" {
		b.WriteString(oneLine(item.Msg))
		if item.Handoff != "" {
			b.WriteString("; ")
		}
	}
	summary := formatHandoffFields(item.HandoffFields)
	if summary == "" {
		summary = item.Handoff
	}
	if summary == "" {
		summary = compactTurnContent(item.Content, 700)
	}
	if summary == "" {
		summary = "no visible answer"
	}
	b.WriteString(oneLine(summary))
	if commands := completedCommandSummaries([]orchestrationTurn{item}, 2); len(commands) > 0 {
		b.WriteString("; verified: ")
		b.WriteString(strings.Join(commands, " | "))
	}
	if item.Err != "" {
		b.WriteString("; error: ")
		b.WriteString(oneLine(trimForPrompt(item.Err, 300)))
	}
	b.WriteByte('\n')
	return b.String()
}

func formatHandoffFields(fields orchestrationHandoffFields) string {
	var parts []string
	if fields.Status != "" {
		parts = append(parts, "status="+fields.Status)
	}
	if meaningfulHandoffValue(fields.Changed) {
		parts = append(parts, "changed="+fields.Changed)
	}
	if meaningfulHandoffValue(fields.Verified) {
		parts = append(parts, "verified="+fields.Verified)
	}
	if meaningfulHandoffValue(fields.Next) {
		parts = append(parts, "next="+fields.Next)
	}
	if meaningfulHandoffValue(fields.Risks) {
		parts = append(parts, "risks="+fields.Risks)
	}
	if len(parts) == 0 {
		return ""
	}
	return "Handoff: " + strings.Join(parts, "; ")
}

func meaningfulHandoffValue(value string) bool {
	value = strings.TrimSpace(strings.ToLower(value))
	return value != "" && value != "none" && value != "n/a" && value != "na"
}

func compactTurnContent(content string, max int) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if handoff := extractHandoff(content); handoff != "" {
		return handoff
	}
	lines := strings.Split(content, "\n")
	var selected []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		lower := strings.ToLower(line)
		if strings.Contains(lower, "final") || strings.Contains(lower, "conclusion") ||
			strings.Contains(lower, "summary") || strings.Contains(lower, "verified") ||
			strings.Contains(lower, "remaining") || strings.Contains(lower, "risk") ||
			strings.Contains(lower, "结论") || strings.Contains(lower, "总结") ||
			strings.Contains(lower, "验证") || strings.Contains(lower, "风险") ||
			len(selected) < 2 {
			selected = append(selected, line)
		}
		if len(selected) >= 5 {
			break
		}
	}
	if len(selected) == 0 {
		return trimForPrompt(oneLine(content), max)
	}
	return trimForPrompt(oneLine(strings.Join(selected, " ")), max)
}

func extractHandoff(content string) string {
	return extractTrailingLine(content, "handoff:")
}

func extractMsg(content string) string {
	return extractTrailingLine(content, "msg:")
}

func parseHandoffFields(line string) orchestrationHandoffFields {
	line = strings.TrimSpace(line)
	line = strings.TrimPrefix(line, "Handoff:")
	line = strings.TrimPrefix(line, "handoff:")
	out := orchestrationHandoffFields{}
	for _, part := range strings.Split(line, ";") {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "status":
			out.Status = value
		case "changed":
			out.Changed = value
		case "verified":
			out.Verified = value
		case "next":
			out.Next = value
		case "risks":
			out.Risks = value
		}
	}
	return out
}

func extractTrailingLine(content, prefix string) string {
	lines := strings.Split(content, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(strings.ToLower(line), prefix) {
			return line
		}
	}
	return ""
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
	b.WriteString("\nFor uploaded source/text files such as .thy, ROOT, .go, .md, and .txt, inspect them with shell commands like sed -n '1,220p' <path>, cat <path>, or wc -l <path>; do not use Claude's Read tool for these uploaded text/source files.")
	b.WriteString("\nDo not send an empty pages field to any file-reading tool. Only include pages when a non-empty page range is required for a real PDF/document; otherwise omit the page filter.")
	return b.String(), metas, nil
}

func safeOrchestrationUploadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = safeOrchestrationFileName.ReplaceAllString(name, "-")
	return strings.Trim(name, ".-")
}
