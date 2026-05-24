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
	client, err := r.start(ctx, req)
	if err != nil {
		return RunnerResult{}, err
	}
	defer client.close()

	if _, err := client.request(ctx, "initialize", map[string]any{
		"clientInfo":   map[string]string{"name": "codex-bridge", "version": "dev"},
		"capabilities": nil,
	}); err != nil {
		return RunnerResult{}, err
	}

	threadID := req.RemoteThreadID
	if threadID == "" {
		res, err := client.request(ctx, "thread/start", r.threadStartParams(req))
		if err != nil {
			return RunnerResult{}, err
		}
		threadID = nestedString(appServerResultMap(res), "thread", "id")
	} else if _, err := client.request(ctx, "thread/resume", r.threadResumeParams(threadID, req)); err != nil {
		return RunnerResult{}, err
	}
	if threadID == "" {
		return RunnerResult{}, errors.New("codex app-server did not return a thread id")
	}

	done := make(chan appServerTurnResult, 1)
	go r.readEvents(ctx, client, req, threadID, onUpdate, done)

	if _, err := client.request(ctx, "turn/start", r.turnStartParams(threadID, req.Content, req)); err != nil {
		return RunnerResult{RemoteThreadID: threadID}, err
	}
	select {
	case result := <-done:
		result.result.RemoteThreadID = threadID
		return result.result, result.err
	case <-ctx.Done():
		return RunnerResult{RemoteThreadID: threadID}, ctx.Err()
	}
}

func (r *CodexAppServerRunner) start(ctx context.Context, req RunnerRequest) (*appServerClient, error) {
	cmd := exec.CommandContext(ctx, r.codexPath(), "app-server", "--listen", "stdio://")
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
		cmd:     cmd,
		stdin:   stdin,
		pending: make(map[int64]chan appServerResponse),
		events:  make(chan appServerMessage, 128),
	}
	go client.read(stdout)
	go io.Copy(io.Discard, stderr)
	return client, nil
}

func (r *CodexAppServerRunner) threadStartParams(req ...RunnerRequest) map[string]any {
	params := map[string]any{
		"cwd":                    r.cwd(req...),
		"approvalPolicy":         r.approvalPolicy(),
		"approvalsReviewer":      "user",
		"sandbox":                r.sandbox(),
		"experimentalRawEvents":  false,
		"persistExtendedHistory": false,
	}
	if r.cfg.Bridge.Model != "" {
		params["model"] = r.cfg.Bridge.Model
	}
	return params
}

func (r *CodexAppServerRunner) threadResumeParams(threadID string, req ...RunnerRequest) map[string]any {
	params := map[string]any{
		"threadId":               threadID,
		"cwd":                    r.cwd(req...),
		"approvalPolicy":         r.approvalPolicy(),
		"approvalsReviewer":      "user",
		"sandbox":                r.sandbox(),
		"persistExtendedHistory": false,
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

func (r *CodexAppServerRunner) readEvents(ctx context.Context, client *appServerClient, req RunnerRequest, threadID string, onUpdate func(update RunnerUpdate), done chan<- appServerTurnResult) {
	var result RunnerResult
	var text strings.Builder
	for {
		select {
		case <-ctx.Done():
			done <- appServerTurnResult{result: result, err: ctx.Err()}
			return
		case msg, ok := <-client.events:
			if !ok {
				if text.Len() > 0 {
					result.Content = text.String()
				}
				done <- appServerTurnResult{result: result, err: errors.New("codex app-server exited")}
				return
			}
			if msg.Method == "" {
				continue
			}
			switch msg.Method {
			case "item/agentMessage/delta":
				delta := nestedString(map[string]any{"params": msg.Params}, "params", "delta")
				if delta != "" {
					text.WriteString(delta)
					onUpdate(RunnerUpdate{Delta: delta})
				}
			case "item/completed":
				item, _ := appServerNestedMap(msg.Params, "item")
				if itemType, _ := item["type"].(string); itemType == "agentMessage" {
					if content, _ := item["text"].(string); content != "" {
						result.Content = content
						text.Reset()
						text.WriteString(content)
						onUpdate(RunnerUpdate{Content: content})
					}
				}
				if tool := appServerToolEvent(item); tool != nil {
					onUpdate(RunnerUpdate{Tool: tool})
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
				if text.Len() > 0 {
					result.Content = text.String()
				}
				done <- appServerTurnResult{result: result}
				return
			case "error":
				done <- appServerTurnResult{result: result, err: errors.New(nestedString(map[string]any{"params": msg.Params}, "params", "message"))}
				return
			}
			if strings.HasSuffix(msg.Method, "/requestApproval") || msg.Method == "execCommandApproval" || msg.Method == "applyPatchApproval" {
				r.handleApproval(ctx, client, msg, req, threadID)
			}
		}
	}
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
	res, err := req.Approvals.RequestApproval(ctx, payload)
	if err != nil {
		res.Decision = "cancel"
	}
	response := approvalResponseFor(msg.Method, res.Decision)
	_ = client.respond(msg.ID, response)
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
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan appServerResponse
	events  chan appServerMessage
	closed  bool
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
	c.nextID++
	id := c.nextID
	ch := make(chan appServerResponse, 1)
	c.pending[id] = ch
	req := map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}
	b, err := json.Marshal(req)
	if err == nil {
		_, err = c.stdin.Write(append(b, '\n'))
	}
	if err != nil {
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}
	c.mu.Unlock()

	select {
	case res := <-ch:
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
	defer c.mu.Unlock()
	_, err = c.stdin.Write(append(b, '\n'))
	return err
}

func (c *appServerClient) read(stdout io.Reader) {
	defer close(c.events)
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		var msg appServerMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
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
						err = errors.New(msg.Error.Message)
					}
					ch <- appServerResponse{result: msg.Result, err: err}
					continue
				}
			}
		}
		c.events <- msg
	}
}

func (c *appServerClient) close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	_ = c.stdin.Close()
	c.mu.Unlock()
	_ = c.cmd.Wait()
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
