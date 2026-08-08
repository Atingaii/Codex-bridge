package bridge

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

type CodexAppServerRunner struct {
	cfg *config.Config
}

func NewCodexAppServerRunner(cfg *config.Config) *CodexAppServerRunner {
	return &CodexAppServerRunner{cfg: cfg}
}

func (r *CodexAppServerRunner) Name() string { return "codex-app-server" }

func (r *CodexAppServerRunner) Close() {}

func (r *CodexAppServerRunner) Prompt(ctx context.Context, req RunnerRequest, onUpdate func(update RunnerUpdate)) (RunnerResult, error) {
	req.Content = sanitizePromptText(req.Content)
	var client *appServerClient
	var threadID string
	var err error
	for attempt := 1; attempt <= appServerPrepareAttempts; attempt++ {
		client, threadID, err = r.prepare(ctx, req)
		if err == nil {
			break
		}
		if client != nil {
			client.unsubscribeThreadWithTimeout(threadID)
			client.closeWithTimeout(appServerPrepareCloseTimeout)
			err = client.withDiagnostics(err)
		}
		if attempt == appServerPrepareAttempts || ctx.Err() != nil {
			return RunnerResult{}, fmt.Errorf("codex app-server preparation failed after %d attempt(s): %w", attempt, err)
		}
		timer := time.NewTimer(appServerPrepareRetryDelay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return RunnerResult{}, ctx.Err()
		case <-timer.C:
		}
	}
	defer func() {
		client.unsubscribeThreadWithTimeout(threadID)
		client.close()
	}()

	done := make(chan appServerTurnResult, 1)
	scope := newAppServerTurnScope(threadID)
	go r.readEvents(ctx, client, req, scope, onUpdate, done)

	res, err := client.request(ctx, "turn/start", r.turnStartParams(threadID, req.Content, req))
	if err != nil {
		return RunnerResult{RemoteThreadID: threadID}, err
	}
	scope.setTurnID(appServerTurnIDFromResponse(res))
	select {
	case result := <-done:
		result.result.RemoteThreadID = threadID
		return result.result, result.err
	case <-ctx.Done():
		return RunnerResult{RemoteThreadID: threadID}, ctx.Err()
	}
}

func (r *CodexAppServerRunner) prepare(ctx context.Context, req RunnerRequest) (*appServerClient, string, error) {
	client, err := r.start(ctx, req)
	if err != nil {
		return nil, "", err
	}
	if _, err := client.request(ctx, "initialize", map[string]any{
		"clientInfo": map[string]string{"name": "codex-bridge", "title": "Codex Bridge", "version": "dev"},
		"capabilities": map[string]any{
			"experimentalApi":    true,
			"requestAttestation": false,
		},
	}); err != nil {
		return client, "", err
	}

	threadID := req.RemoteThreadID
	if threadID == "" {
		res, err := client.request(ctx, "thread/start", r.threadStartParams(req))
		if err != nil {
			return client, "", err
		}
		threadID = nestedString(appServerResultMap(res), "thread", "id")
	} else if _, err := client.request(ctx, "thread/resume", r.threadResumeParams(threadID, req)); err != nil {
		return client, threadID, err
	}
	if threadID == "" {
		return client, "", errors.New("codex app-server did not return a thread id")
	}
	return client, threadID, nil
}

const appServerThreadUnsubscribeTimeout = 2 * time.Second
const appServerProcessCloseTimeout = 30 * time.Second
const appServerPrepareAttempts = 2
const appServerPrepareRetryDelay = 150 * time.Millisecond
const appServerPrepareCloseTimeout = 2 * time.Second
const appServerDiagnosticTailBytes = 16 * 1024
const appServerUnscopedErrorGracePeriod = 2 * time.Second

func (r *CodexAppServerRunner) start(ctx context.Context, req RunnerRequest) (*appServerClient, error) {
	cmd := exec.CommandContext(ctx, r.codexPath(), "app-server", "--listen", "stdio://")
	configureManagedCommand(cmd)
	if cwd := r.cwd(req); cwd != "" {
		cmd.Dir = cwd
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	client := &appServerClient{
		cmd:         cmd,
		stdin:       stdin,
		pending:     make(map[int64]chan appServerResponse),
		events:      make(chan appServerMessage, 128),
		stop:        make(chan struct{}),
		waitDone:    make(chan struct{}),
		diagnostics: &appServerDiagnosticTail{limit: appServerDiagnosticTailBytes},
	}
	go client.read(stdout)
	go io.Copy(client.diagnostics, stderr)
	return client, nil
}

func (r *CodexAppServerRunner) threadStartParams(req ...RunnerRequest) map[string]any {
	params := map[string]any{
		"cwd":                   r.cwd(req...),
		"approvalPolicy":        r.approvalPolicy(),
		"approvalsReviewer":     "user",
		"sandbox":               r.sandbox(),
		"experimentalRawEvents": false,
		"ephemeral":             false,
		"threadSource":          "user",
	}
	if r.cfg.Bridge.Model != "" {
		params["model"] = r.cfg.Bridge.Model
	}
	return params
}

func (r *CodexAppServerRunner) threadResumeParams(threadID string, req ...RunnerRequest) map[string]any {
	params := map[string]any{
		"threadId":          threadID,
		"cwd":               r.cwd(req...),
		"approvalPolicy":    r.approvalPolicy(),
		"approvalsReviewer": "user",
		"sandbox":           r.sandbox(),
	}
	if r.cfg.Bridge.Model != "" {
		params["model"] = r.cfg.Bridge.Model
	}
	return params
}

func (r *CodexAppServerRunner) turnStartParams(threadID, content string, req ...RunnerRequest) map[string]any {
	return map[string]any{
		"threadId": threadID,
		"input": []map[string]any{
			{"type": "text", "text": content, "text_elements": []any{}},
		},
		"approvalPolicy":    r.approvalPolicy(),
		"approvalsReviewer": "user",
		"sandboxPolicy":     r.sandboxPolicy(req...),
	}
}

func (r *CodexAppServerRunner) approvalPolicy() string {
	if r.cfg.Bridge.ApprovalPolicy == "" {
		return "untrusted"
	}
	return r.cfg.Bridge.ApprovalPolicy
}

func (r *CodexAppServerRunner) sandbox() string {
	if r.cfg.Bridge.Sandbox == "" {
		return "workspace-write"
	}
	return r.cfg.Bridge.Sandbox
}

func (r *CodexAppServerRunner) sandboxPolicy(req ...RunnerRequest) map[string]any {
	switch strings.ToLower(r.sandbox()) {
	case "danger-full-access":
		return map[string]any{"type": "dangerFullAccess"}
	case "read-only":
		return map[string]any{"type": "readOnly", "networkAccess": false}
	default:
		return map[string]any{
			"type":                "workspaceWrite",
			"writableRoots":       []string{r.cwd(req...)},
			"networkAccess":       false,
			"excludeTmpdirEnvVar": false,
			"excludeSlashTmp":     false,
		}
	}
}

func (r *CodexAppServerRunner) cwd(req ...RunnerRequest) string {
	cwd := r.cfg.Bridge.CWD
	if len(req) > 0 && req[0].CWD != "" {
		cwd = req[0].CWD
	}
	if cwd == "" {
		cwd = "."
	}
	if abs, err := filepath.Abs(expandHome(cwd)); err == nil {
		return abs
	}
	return expandHome(cwd)
}

func (r *CodexAppServerRunner) codexPath() string {
	if r.cfg.Bridge.CodexPath == "" {
		return "codex"
	}
	return r.cfg.Bridge.CodexPath
}

type appServerTurnResult struct {
	result RunnerResult
	err    error
}

type appServerTurnScope struct {
	mu        sync.RWMutex
	readyOnce sync.Once
	ready     chan struct{}
	threadID  string
	turnID    string
	observed  string
	resolved  bool
}

func newAppServerTurnScope(threadID string) *appServerTurnScope {
	return &appServerTurnScope{threadID: threadID, ready: make(chan struct{})}
}

func (s *appServerTurnScope) setTurnID(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if s == nil {
		return
	}
	s.mu.Lock()
	s.resolved = true
	if turnID != "" {
		s.turnID = turnID
	} else if s.turnID == "" {
		s.turnID = s.observed
	}
	ready := s.turnID != ""
	s.mu.Unlock()
	if ready {
		s.readyOnce.Do(func() {
			close(s.ready)
		})
	}
}

func (s *appServerTurnScope) observeTurnID(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if s == nil || turnID == "" {
		return
	}
	s.mu.Lock()
	s.observed = turnID
	if s.resolved && s.turnID == "" {
		s.turnID = turnID
	}
	ready := s.turnID != ""
	s.mu.Unlock()
	if ready {
		s.readyOnce.Do(func() {
			close(s.ready)
		})
	}
}

func (s *appServerTurnScope) current() (threadID, turnID string) {
	if s == nil {
		return "", ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.threadID, s.turnID
}

func (s *appServerTurnScope) waitForTurnID(ctx context.Context) {
	if s == nil {
		return
	}
	select {
	case <-s.ready:
		return
	default:
	}
	select {
	case <-ctx.Done():
		return
	default:
	}
	timer := time.NewTimer(250 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-s.ready:
	case <-ctx.Done():
	case <-timer.C:
	}
}

func (s *appServerTurnScope) matches(ctx context.Context, msg appServerMessage) bool {
	if messageRequiresTurnID(msg.Method) {
		s.waitForTurnID(ctx)
	}
	threadID, turnID := s.current()
	if msgThreadID := appServerMessageThreadID(msg); msgThreadID != "" && threadID != "" && msgThreadID != threadID {
		return false
	}
	msgTurnID := appServerMessageTurnID(msg)
	if msgTurnID != "" && turnID == "" {
		s.waitForTurnID(ctx)
		_, turnID = s.current()
	}
	if msg.Method == "turn/completed" || msg.Method == "item/agentMessage/delta" {
		return turnID != "" && msgTurnID == turnID
	}
	if msgTurnID != "" && turnID != "" && msgTurnID != turnID {
		return false
	}
	return true
}

func messageRequiresTurnID(method string) bool {
	switch method {
	case "turn/completed",
		"thread/tokenUsage/updated",
		"raw/responseCompleted",
		"item/agentMessage/delta",
		"item/completed",
		"item/started",
		"item/commandExecution/outputDelta":
		return true
	default:
		return false
	}
}

func (s *appServerTurnScope) matchesNonTerminal(ctx context.Context, msg appServerMessage) bool {
	if appServerMessageTurnID(msg) != "" {
		s.waitForTurnID(ctx)
	}
	threadID, turnID := s.current()
	if msgThreadID := appServerMessageThreadID(msg); msgThreadID != "" && threadID != "" && msgThreadID != threadID {
		return false
	}
	msgTurnID := appServerMessageTurnID(msg)
	if msgTurnID != "" {
		return turnID != "" && msgTurnID == turnID
	}
	return true
}

func (r *CodexAppServerRunner) readEvents(ctx context.Context, client *appServerClient, req RunnerRequest, scope *appServerTurnScope, onUpdate func(update RunnerUpdate), done chan<- appServerTurnResult) {
	var result RunnerResult
	var text strings.Builder
	var pendingFailedTool *RunnerToolEvent
	var unscopedEmptyError error
	var unscopedErrorTimer *time.Timer
	var unscopedErrorTimeout <-chan time.Time
	defer func() {
		if unscopedErrorTimer != nil {
			unscopedErrorTimer.Stop()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			done <- appServerTurnResult{result: result, err: ctx.Err()}
			return
		case <-unscopedErrorTimeout:
			if text.Len() > 0 {
				result.Content = text.String()
			}
			if strings.TrimSpace(result.Content) != "" && isAppServerEmptyErrorAfterVisibleOutput(unscopedEmptyError) {
				done <- appServerTurnResult{result: result}
			} else {
				done <- appServerTurnResult{result: result, err: unscopedEmptyError}
			}
			return
		case msg, ok := <-client.events:
			if !ok {
				if text.Len() > 0 {
					result.Content = text.String()
				}
				if pendingFailedTool != nil {
					done <- appServerTurnResult{result: result, err: failedToolWithoutFollowupError("codex app-server", *pendingFailedTool)}
					return
				}
				if strings.TrimSpace(result.Content) != "" {
					done <- appServerTurnResult{result: result}
					return
				}
				done <- appServerTurnResult{result: result, err: client.exitError()}
				return
			}
			if msg.Method == "" {
				continue
			}
			if strings.HasSuffix(msg.Method, "/requestApproval") || msg.Method == "execCommandApproval" || msg.Method == "applyPatchApproval" {
				if scope.matchesNonTerminal(ctx, msg) {
					threadID, _ := scope.current()
					r.handleApproval(ctx, client, msg, req, threadID)
				}
				continue
			}
			if msg.Method == "turn/started" {
				threadID, _ := scope.current()
				if msgThreadID := appServerMessageThreadID(msg); msgThreadID == "" || threadID == "" || msgThreadID == threadID {
					scope.observeTurnID(appServerMessageTurnID(msg))
				}
				continue
			}
			if msg.Method == "thread/tokenUsage/updated" || msg.Method == "raw/responseCompleted" {
				if !scope.matchesNonTerminal(ctx, msg) {
					continue
				}
				var payload any
				if msg.Method == "thread/tokenUsage/updated" {
					payload = msg.Params["tokenUsage"]
				} else {
					payload = msg.Params
				}
				if payload != nil {
					if raw, marshalErr := json.Marshal(payload); marshalErr == nil && len(raw) > 0 {
						result.Usage = raw
					}
				}
				continue
			}
			if !scope.matches(ctx, msg) {
				continue
			}
			switch msg.Method {
			case "item/agentMessage/delta":
				delta := nestedString(map[string]any{"params": msg.Params}, "params", "delta")
				if delta != "" {
					text.WriteString(delta)
					pendingFailedTool = nil
					onUpdate(RunnerUpdate{Delta: delta})
				}
			case "item/completed":
				item, _ := appServerNestedMap(msg.Params, "item")
				if itemType, _ := item["type"].(string); itemType == "agentMessage" {
					if content, _ := item["text"].(string); content != "" {
						if delta := appendAgentMessageContent(&text, content); delta != "" {
							pendingFailedTool = nil
							onUpdate(RunnerUpdate{Content: delta})
						}
						result.Content = text.String()
					}
				}
				if tool := appServerToolEvent(item); tool != nil {
					onUpdate(RunnerUpdate{Tool: tool})
					if runnerToolEventFailed(*tool) {
						copy := *tool
						pendingFailedTool = &copy
					}
				}
			case "item/started":
				item, _ := appServerNestedMap(msg.Params, "item")
				if tool := appServerToolEvent(item); tool != nil {
					onUpdate(RunnerUpdate{Tool: tool})
				}
			case "item/commandExecution/outputDelta":
				if id := nestedString(map[string]any{"params": msg.Params}, "params", "itemId"); id != "" {
					onUpdate(RunnerUpdate{Tool: &RunnerToolEvent{ID: id, Output: nestedString(map[string]any{"params": msg.Params}, "params", "delta"), Status: "running"}})
				}
			case "turn/completed":
				if terminal, err := appServerCompletedTurnState(msg); !terminal {
					continue
				} else if err != nil {
					if text.Len() > 0 {
						result.Content = text.String()
					}
					done <- appServerTurnResult{result: result, err: err}
					return
				}
				if content := appServerTurnContent(msg); content != "" {
					if delta := appendAgentMessageContent(&text, content); delta != "" {
						pendingFailedTool = nil
						onUpdate(RunnerUpdate{Content: delta})
					}
				}
				if text.Len() > 0 {
					result.Content = text.String()
				}
				if pendingFailedTool != nil {
					done <- appServerTurnResult{result: result, err: failedToolWithoutFollowupError("codex app-server", *pendingFailedTool)}
					return
				}
				if strings.TrimSpace(result.Content) == "" {
					done <- appServerTurnResult{result: result, err: errors.New("codex app-server completed the turn without an assistant response")}
					return
				}
				done <- appServerTurnResult{result: result}
				return
			case "error":
				if text.Len() > 0 && strings.TrimSpace(result.Content) == "" {
					result.Content = text.String()
				}
				err := appServerEventError(msg, result.Content)
				if isAppServerUnscopedEmptyError(msg, err) {
					unscopedEmptyError = err
					if unscopedErrorTimer == nil {
						unscopedErrorTimer = time.NewTimer(appServerUnscopedErrorGracePeriod)
						unscopedErrorTimeout = unscopedErrorTimer.C
					}
					continue
				}
				if isAppServerEmptyErrorAfterVisibleOutput(err) {
					done <- appServerTurnResult{result: result}
					return
				}
				done <- appServerTurnResult{result: result, err: err}
				return
			}
		}
	}
}

func appServerCompletedTurnState(msg appServerMessage) (bool, error) {
	status := normalizeToolStatus(appServerTurnStatus(msg))
	if status == "" {
		return true, nil
	}
	switch status {
	case "completed", "success", "succeeded":
		return true, nil
	case "failed", "error":
		return true, appServerTurnError(msg)
	case "cancelled", "canceled":
		return true, context.Canceled
	case "in_progress", "running", "started", "pending", "queued":
		return false, nil
	default:
		return true, nil
	}
}

func appServerTurnStatus(msg appServerMessage) string {
	if value := firstString(msg.Params, "status"); value != "" {
		return value
	}
	if turn, _ := appServerNestedMap(msg.Params, "turn"); turn != nil {
		return firstString(turn, "status")
	}
	return ""
}

func appServerTurnError(msg appServerMessage) error {
	if message := eventErrorMessage(msg.Params); message != "" {
		return errors.New(message)
	}
	if message := firstString(msg.Params, "message", "error"); message != "" {
		return errors.New(message)
	}
	if turn, _ := appServerNestedMap(msg.Params, "turn"); turn != nil {
		if message := firstString(turn, "message", "error"); message != "" {
			return errors.New(message)
		}
		if errObj, _ := turn["error"].(map[string]any); errObj != nil {
			if message := firstString(errObj, "message", "code", "type"); message != "" {
				return errors.New(message)
			}
		}
	}
	if msg.Error != nil && strings.TrimSpace(msg.Error.Message) != "" {
		return errors.New(strings.TrimSpace(msg.Error.Message))
	}
	return errors.New("codex app-server turn failed")
}

func appServerTurnContent(msg appServerMessage) string {
	turn, _ := appServerNestedMap(msg.Params, "turn")
	if turn == nil {
		return ""
	}
	items, _ := turn["items"].([]any)
	var content strings.Builder
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if firstString(item, "type") != "agentMessage" {
			continue
		}
		if text := agentMessageText(item); text != "" {
			appendAgentMessageContent(&content, text)
		}
	}
	return content.String()
}

func appServerTurnIDFromResponse(raw json.RawMessage) string {
	return nestedString(appServerResultMap(raw), "turn", "id")
}

func appServerMessageThreadID(msg appServerMessage) string {
	wrapped := map[string]any{"params": msg.Params}
	if id := nestedString(wrapped, "params", "threadId"); id != "" {
		return id
	}
	if child, _ := msg.Params["thread"].(map[string]any); child != nil {
		if id, _ := child["id"].(string); id != "" {
			return id
		}
	}
	if child, _ := msg.Params["item"].(map[string]any); child != nil {
		if id, _ := child["threadId"].(string); id != "" {
			return id
		}
	}
	return ""
}

func appServerMessageTurnID(msg appServerMessage) string {
	wrapped := map[string]any{"params": msg.Params}
	if id := nestedString(wrapped, "params", "turnId"); id != "" {
		return id
	}
	if child, _ := msg.Params["turn"].(map[string]any); child != nil {
		if id, _ := child["id"].(string); id != "" {
			return id
		}
		if id, _ := child["turnId"].(string); id != "" {
			return id
		}
	}
	if child, _ := msg.Params["item"].(map[string]any); child != nil {
		if id, _ := child["turnId"].(string); id != "" {
			return id
		}
	}
	return ""
}

type appServerEmptyErrorAfterVisibleOutput struct {
	visibleOutput string
}

func (e *appServerEmptyErrorAfterVisibleOutput) Error() string {
	return fmt.Sprintf("codex app-server returned an empty error after producing visible output; last output: %s", trimForPrompt(e.visibleOutput, 1000))
}

func appServerEventError(msg appServerMessage, visibleOutput string) error {
	message := strings.TrimSpace(eventErrorMessage(msg.Params))
	if message == "" && msg.Error != nil {
		message = strings.TrimSpace(msg.Error.Message)
	}
	if message != "" {
		return errors.New(message)
	}
	if output := strings.TrimSpace(visibleOutput); output != "" {
		return &appServerEmptyErrorAfterVisibleOutput{visibleOutput: output}
	}
	return errors.New("codex app-server returned an empty error")
}

func isAppServerUnscopedEmptyError(msg appServerMessage, err error) bool {
	if err == nil || appServerMessageThreadID(msg) != "" || appServerMessageTurnID(msg) != "" {
		return false
	}
	return err.Error() == "codex app-server returned an empty error" || isAppServerEmptyErrorAfterVisibleOutput(err)
}

func isAppServerEmptyErrorAfterVisibleOutput(err error) bool {
	var target *appServerEmptyErrorAfterVisibleOutput
	return errors.As(err, &target)
}

func (r *CodexAppServerRunner) handleApproval(ctx context.Context, client *appServerClient, msg appServerMessage, req RunnerRequest, threadID string) {
	if req.Approvals == nil || msg.ID == nil {
		return
	}
	raw, _ := json.Marshal(msg.Params)
	payload := protocol.ApprovalRequestPayload{
		RequestID: fmt.Sprintf("%v", msg.ID),
		Kind:      msg.Method,
		Command:   approvalCommand(msg.Params),
		CWD:       nestedString(map[string]any{"params": msg.Params}, "params", "cwd"),
		Reason:    nestedString(map[string]any{"params": msg.Params}, "params", "reason"),
		ThreadID:  threadID,
		TurnID:    nestedString(map[string]any{"params": msg.Params}, "params", "turnId"),
		ItemID:    nestedString(map[string]any{"params": msg.Params}, "params", "itemId"),
		RunID:     req.RunID,
		PromptID:  req.PromptID,
		Params:    raw,
	}
	if payload.CWD == "" {
		payload.CWD = r.cwd(req)
	}
	if isCodexCommandApproval(msg.Method) && isProofCommandAutoApprovable(payload.Command, payload.CWD) {
		_ = client.respond(msg.ID, automaticApprovalResponseFor(msg.Method))
		return
	}
	res, err := req.Approvals.RequestApproval(ctx, payload)
	if err != nil {
		res.Decision = "cancel"
	}
	response := approvalResponseFor(msg.Method, res.Decision)
	_ = client.respond(msg.ID, response)
}

func isCodexCommandApproval(method string) bool {
	return method == "item/commandExecution/requestApproval" || method == "execCommandApproval"
}

func automaticApprovalResponseFor(method string) any {
	if method == "execCommandApproval" {
		return map[string]any{"decision": "approved"}
	}
	return map[string]any{"decision": "accept"}
}

func appServerToolEvent(item map[string]any) *RunnerToolEvent {
	if item == nil {
		return nil
	}
	itemType, _ := item["type"].(string)
	if itemType != "commandExecution" {
		return nil
	}
	tool := &RunnerToolEvent{}
	tool.ID, _ = item["id"].(string)
	tool.Command, _ = item["command"].(string)
	tool.Status, _ = item["status"].(string)
	if output, _ := item["aggregatedOutput"].(string); output != "" {
		tool.Output = output
	}
	if exit, ok := numericInt(item["exitCode"]); ok {
		tool.ExitCode = &exit
	}
	return tool
}

func appServerResultMap(raw json.RawMessage) map[string]any {
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func approvalCommand(params map[string]any) string {
	if command, _ := params["command"].(string); command != "" {
		return command
	}
	if command, ok := params["command"].([]any); ok {
		parts := make([]string, 0, len(command))
		for _, part := range command {
			parts = append(parts, fmt.Sprint(part))
		}
		return strings.Join(parts, " ")
	}
	return ""
}

func approvalResponseFor(method, decision string) any {
	allow := decision == "accept"
	switch method {
	case "item/commandExecution/requestApproval":
		if allow {
			return map[string]any{"decision": "acceptForSession"}
		}
		return map[string]any{"decision": "decline"}
	case "item/fileChange/requestApproval":
		if allow {
			return map[string]any{"decision": "accept"}
		}
		return map[string]any{"decision": "decline"}
	case "item/permissions/requestApproval":
		if allow {
			return map[string]any{"permissions": map[string]any{}, "scope": "session"}
		}
		return map[string]any{"permissions": map[string]any{}, "scope": "turn", "strictAutoReview": true}
	case "execCommandApproval":
		if allow {
			return map[string]any{"decision": "approved_for_session"}
		}
		return map[string]any{"decision": "denied"}
	case "applyPatchApproval":
		if allow {
			return map[string]any{"decision": "approved_for_session"}
		}
		return map[string]any{"decision": "denied"}
	default:
		if allow {
			return map[string]any{"decision": "accept"}
		}
		return map[string]any{"decision": "decline"}
	}
}

type appServerClient struct {
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	mu          sync.Mutex
	writeMu     sync.Mutex
	nextID      int64
	pending     map[int64]chan appServerResponse
	events      chan appServerMessage
	stop        chan struct{}
	stopOnce    sync.Once
	closed      bool
	wait        sync.Once
	waitDone    chan struct{}
	diagnostics *appServerDiagnosticTail
}

type appServerDiagnosticTail struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (t *appServerDiagnosticTail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	written := len(p)
	if t.limit <= 0 {
		t.data = nil
		return written, nil
	}
	if cap(t.data) < t.limit {
		t.data = make([]byte, 0, t.limit)
	}
	if len(p) >= t.limit {
		t.data = t.data[:t.limit]
		copy(t.data, p[len(p)-t.limit:])
		return written, nil
	}
	overflow := len(t.data) + len(p) - t.limit
	if overflow > 0 {
		copy(t.data, t.data[overflow:])
		t.data = t.data[:len(t.data)-overflow]
	}
	t.data = append(t.data, p...)
	return written, nil
}

func (t *appServerDiagnosticTail) String() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return strings.TrimSpace(string(t.data))
}

type appServerMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      any             `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  map[string]any  `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type appServerResponse struct {
	result json.RawMessage
	err    error
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *appServerClient) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, errors.New("codex app-server exited")
	}
	c.nextID++
	id := c.nextID
	ch := make(chan appServerResponse, 1)
	c.pending[id] = ch
	stdin := c.stdin
	c.mu.Unlock()
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	b, err := json.Marshal(req)
	if err == nil {
		c.writeMu.Lock()
		_, err = stdin.Write(append(b, '\n'))
		c.writeMu.Unlock()
	}
	if err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case res := <-ch:
		if res.err != nil && strings.TrimSpace(res.err.Error()) == "" {
			return res.result, fmt.Errorf("codex app-server %s failed without an error message", method)
		}
		return res.result, res.err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (c *appServerClient) respond(id any, result any) error {
	res := map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
	b, err := json.Marshal(res)
	if err != nil {
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return errors.New("codex app-server exited")
	}
	stdin := c.stdin
	c.mu.Unlock()
	c.writeMu.Lock()
	_, err = stdin.Write(append(b, '\n'))
	c.writeMu.Unlock()
	return err
}

func (c *appServerClient) isClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func (c *appServerClient) unsubscribeThread(ctx context.Context, threadID string) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	_, err := c.request(ctx, "thread/unsubscribe", map[string]any{"threadId": threadID})
	return err
}

func (c *appServerClient) unsubscribeThreadWithTimeout(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), appServerThreadUnsubscribeTimeout)
	defer cancel()
	_ = c.unsubscribeThread(ctx, threadID)
}

func (c *appServerClient) read(stdout io.Reader) {
	defer func() {
		c.mu.Lock()
		pending := c.pending
		c.pending = make(map[int64]chan appServerResponse)
		c.closed = true
		c.mu.Unlock()
		c.signalStop()
		for _, ch := range pending {
			ch <- appServerResponse{err: c.exitError()}
		}
		close(c.events)
	}()
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		var msg appServerMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			_, _ = c.diagnostics.Write([]byte("invalid stdout frame: "))
			_, _ = c.diagnostics.Write(scanner.Bytes())
			_, _ = c.diagnostics.Write([]byte{'\n'})
			continue
		}
		if msg.ID != nil && msg.Method == "" {
			if id, ok := idInt(msg.ID); ok {
				c.mu.Lock()
				ch := c.pending[id]
				delete(c.pending, id)
				c.mu.Unlock()
				if ch != nil {
					var err error
					if msg.Error != nil {
						message := strings.TrimSpace(msg.Error.Message)
						if message == "" {
							message = fmt.Sprintf("codex app-server RPC error %d returned no message", msg.Error.Code)
						}
						err = errors.New(message)
					}
					ch <- appServerResponse{result: msg.Result, err: err}
					continue
				}
			}
		}
		select {
		case c.events <- msg:
		case <-c.stopped():
			return
		}
	}
	if err := scanner.Err(); err != nil {
		_, _ = c.diagnostics.Write([]byte("stdout read error: " + err.Error()))
	}
}

func (c *appServerClient) exitError() error {
	if detail := c.diagnostics.String(); detail != "" {
		return fmt.Errorf("codex app-server exited: %s", trimForPrompt(detail, 2000))
	}
	return errors.New("codex app-server exited")
}

func (c *appServerClient) withDiagnostics(err error) error {
	if err == nil {
		return nil
	}
	if detail := c.diagnostics.String(); detail != "" && !strings.Contains(err.Error(), detail) {
		return fmt.Errorf("%w; app-server diagnostics: %s", err, trimForPrompt(detail, 2000))
	}
	return err
}

func (c *appServerClient) close() {
	c.closeWithTimeout(appServerProcessCloseTimeout)
}

func (c *appServerClient) closeWithTimeout(timeout time.Duration) {
	c.mu.Lock()
	terminate := !c.closed
	if terminate {
		c.closed = true
	}
	stdin := c.stdin
	c.mu.Unlock()
	c.signalStop()
	if terminate && stdin != nil {
		_ = stdin.Close()
	}
	if c.cmd == nil {
		return
	}
	done := c.waitProcess()
	if timeout <= 0 {
		<-done
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		return
	case <-timer.C:
		if c.cmd.Process != nil {
			_ = terminateProcessGroup(c.cmd.Process.Pid)
		}
		<-done
	}
}

func (c *appServerClient) stopped() <-chan struct{} {
	return c.stop
}

func (c *appServerClient) signalStop() {
	if c.stop == nil {
		return
	}
	c.stopOnce.Do(func() {
		close(c.stop)
	})
}

func (c *appServerClient) waitProcess() <-chan struct{} {
	if c.waitDone == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	c.wait.Do(func() {
		go func() {
			_ = c.cmd.Wait()
			close(c.waitDone)
		}()
	})
	return c.waitDone
}

func idInt(value any) (int64, bool) {
	switch v := value.(type) {
	case float64:
		return int64(v), true
	case int64:
		return v, true
	case int:
		return int64(v), true
	default:
		return 0, false
	}
}

func numericInt(value any) (int, bool) {
	switch v := value.(type) {
	case float64:
		return int(v), true
	case int:
		return v, true
	default:
		return 0, false
	}
}

func appServerNestedMap(m map[string]any, keys ...string) (map[string]any, bool) {
	var cur any = m
	for _, key := range keys {
		next, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next[key]
	}
	out, ok := cur.(map[string]any)
	return out, ok
}
