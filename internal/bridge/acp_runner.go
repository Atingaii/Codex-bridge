package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

// ACPRunner keeps one resident Agent Client Protocol adapter process per chat
// session. It maps ACP session/update notifications to RunnerUpdate streams and
// routes session/request_permission reverse requests through the existing
// browser approval channel. It implements both Runner (one-shot Prompt) and
// SessionRunner (interactive long session).
type ACPRunner struct {
	cfg *config.Config

	mu       sync.Mutex
	sessions map[string]*acpSession
}

// acpSession is one resident ACP conversation tied to a chat sid.
type acpSession struct {
	client       *acpClient
	acpSessionID string
	nativeResume string
	cwd          string
	initialized  bool
	loadSession  bool
}

const (
	acpProtocolVersion = 1
	acpPromptTimeout   = 30 * time.Minute
)

func NewACPRunner(cfg *config.Config) *ACPRunner {
	return &ACPRunner{cfg: cfg, sessions: make(map[string]*acpSession)}
}

func (r *ACPRunner) Name() string { return "acp" }

// Prompt implements the one-shot Runner interface for callers that do not use
// the SessionRunner long-session path. It transparently opens (or reuses) the
// resident session, sends the prompt, and keeps the process alive afterward so
// subsequent prompts on the same sid stay in the same conversation.
func (r *ACPRunner) Prompt(ctx context.Context, req RunnerRequest, onUpdate func(update RunnerUpdate)) (RunnerResult, error) {
	handle, err := r.OpenSession(ctx, OpenSessionRequest{
		SID:            req.SID,
		CWD:            req.CWD,
		RemoteThreadID: req.RemoteThreadID,
		Approvals:      req.Approvals,
	})
	if err != nil {
		return RunnerResult{}, err
	}
	result, err := r.PromptSession(ctx, PromptSessionRequest{
		SID:       req.SID,
		Content:   req.Content,
		RunID:     req.RunID,
		PromptID:  req.PromptID,
		Approvals: req.Approvals,
	}, onUpdate)
	if result.RemoteThreadID == "" {
		result.RemoteThreadID = handle.ACPSessionID
	}
	return result, err
}

// OpenSession starts or reuses the resident adapter process for a sid.
func (r *ACPRunner) OpenSession(ctx context.Context, req OpenSessionRequest) (SessionHandle, error) {
	cwd := r.resolveCWD(req.CWD)

	r.mu.Lock()
	if existing := r.sessions[req.SID]; existing != nil && existing.client != nil && !existing.client.isClosed() {
		handle := r.handleFor(existing)
		r.mu.Unlock()
		return handle, nil
	}
	r.mu.Unlock()

	command, args, err := r.adapterCommand()
	if err != nil {
		return SessionHandle{}, err
	}
	client, err := startACPClient(ctx, command, args, cwd, nil)
	if err != nil {
		return SessionHandle{}, fmt.Errorf("start acp adapter %q: %w", command, err)
	}

	sess := &acpSession{client: client, cwd: cwd}
	if err := r.initialize(ctx, sess); err != nil {
		client.close()
		return SessionHandle{}, err
	}

	// Resume an existing ACP session when Hub passed a persisted id and the
	// adapter advertised session/load; otherwise create a new one.
	if strings.TrimSpace(req.RemoteThreadID) != "" && sess.loadSession {
		if err := r.loadSession(ctx, sess, req.RemoteThreadID); err != nil {
			client.close()
			return SessionHandle{}, err
		}
		sess.acpSessionID = req.RemoteThreadID
	} else {
		id, err := r.newSession(ctx, sess)
		if err != nil {
			client.close()
			return SessionHandle{}, err
		}
		sess.acpSessionID = id
	}

	sess.nativeResume = r.resolveNativeResumeID(sess.acpSessionID, cwd)

	r.mu.Lock()
	// Defend against a concurrent open that won the race.
	if existing := r.sessions[req.SID]; existing != nil && existing.client != nil && !existing.client.isClosed() {
		handle := r.handleFor(existing)
		r.mu.Unlock()
		client.close()
		return handle, nil
	}
	r.sessions[req.SID] = sess
	handle := r.handleFor(sess)
	r.mu.Unlock()
	return handle, nil
}

// Resume is an alias for OpenSession with the persisted ACP session id; it is
// kept separate so callers can express intent and so future load-only behavior
// can diverge.
func (r *ACPRunner) Resume(ctx context.Context, req ResumeRequest) (SessionHandle, error) {
	return r.OpenSession(ctx, OpenSessionRequest{
		SID:            req.SID,
		CWD:            req.CWD,
		RemoteThreadID: req.RemoteThreadID,
		Approvals:      req.Approvals,
	})
}

// PromptSession sends one prompt into a resident session and streams updates.
func (r *ACPRunner) PromptSession(ctx context.Context, req PromptSessionRequest, onUpdate func(update RunnerUpdate)) (RunnerResult, error) {
	r.mu.Lock()
	sess := r.sessions[req.SID]
	r.mu.Unlock()
	if sess == nil || sess.client == nil || sess.client.isClosed() {
		return RunnerResult{}, errSessionNotOpen
	}

	turnCtx, cancel := context.WithTimeout(ctx, acpPromptTimeout)
	defer cancel()

	result := RunnerResult{RemoteThreadID: sess.acpSessionID}
	var text strings.Builder

	// Consume notifications and reverse requests for the duration of the turn.
	stop := make(chan struct{})
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		r.pump(turnCtx, sess, req, onUpdate, &text, &result, stop)
	}()

	params := map[string]any{
		"sessionId": sess.acpSessionID,
		"prompt": []map[string]any{
			{"type": "text", "text": sanitizePromptText(req.Content)},
		},
	}
	raw, err := sess.client.request(turnCtx, "session/prompt", params)
	close(stop)
	<-pumpDone
	// The prompt response can race ahead of session/update notifications that
	// the adapter emitted just before it. Drain any already-queued notifications
	// so the final tool/message updates are not lost.
	r.drainPending(sess, onUpdate, &text, &result)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			// Best-effort cancel notification to the adapter.
			_ = sess.client.notify("session/cancel", map[string]any{"sessionId": sess.acpSessionID})
			return result, ctxErr
		}
		return result, err
	}
	if reason := acpStopReason(raw); reason == "cancelled" {
		return result, context.Canceled
	}
	if text.Len() > 0 {
		result.Content = text.String()
	}
	return result, nil
}

// drainPending consumes notifications and reverse requests that were buffered
// before the prompt response arrived, without blocking on the channels.
func (r *ACPRunner) drainPending(sess *acpSession, onUpdate func(update RunnerUpdate), text *strings.Builder, result *RunnerResult) {
	for {
		select {
		case msg, ok := <-sess.client.notifs:
			if !ok {
				return
			}
			r.handleNotification(msg, onUpdate, text, result)
		default:
			return
		}
	}
}

// pump reads agent->client notifications and reverse requests during a turn.
func (r *ACPRunner) pump(ctx context.Context, sess *acpSession, req PromptSessionRequest, onUpdate func(update RunnerUpdate), text *strings.Builder, result *RunnerResult, stop <-chan struct{}) {
	for {
		select {
		case <-stop:
			return
		case <-ctx.Done():
			return
		case msg, ok := <-sess.client.notifs:
			if !ok {
				return
			}
			r.handleNotification(msg, onUpdate, text, result)
		case msg, ok := <-sess.client.requests:
			if !ok {
				return
			}
			r.handleReverseRequest(ctx, sess, msg, req)
		}
	}
}

func (r *ACPRunner) handleNotification(msg acpMessage, onUpdate func(update RunnerUpdate), text *strings.Builder, result *RunnerResult) {
	if msg.Method != "session/update" {
		return
	}
	var params struct {
		Update json.RawMessage `json:"update"`
	}
	if err := json.Unmarshal(msg.Params, &params); err != nil {
		return
	}
	if update, tool, kind := mapACPUpdate(params.Update); kind != "" {
		switch kind {
		case "message_delta":
			text.WriteString(update)
			onUpdate(RunnerUpdate{Delta: update})
		case "tool":
			if tool != nil {
				onUpdate(RunnerUpdate{Tool: tool})
			}
		}
	}
}

func (r *ACPRunner) handleReverseRequest(ctx context.Context, sess *acpSession, msg acpMessage, req PromptSessionRequest) {
	switch msg.Method {
	case "session/request_permission":
		r.handlePermission(ctx, sess, msg, req)
	default:
		// Unsupported client method (for example fs/* or terminal/* when we did
		// not advertise the capability). Answer with a method-not-found error so
		// the adapter does not block.
		_ = sess.client.respondError(msg.ID, -32601, "method not supported by codex-bridge acp client")
	}
}

func (r *ACPRunner) handlePermission(ctx context.Context, sess *acpSession, msg acpMessage, req PromptSessionRequest) {
	options, allowID, rejectID := acpPermissionOptions(msg.Params)
	if req.Approvals == nil {
		// No browser approval channel: cancel safely.
		_ = sess.client.respond(msg.ID, acpPermissionOutcome("cancelled", ""))
		return
	}
	payload := protocol.ApprovalRequestPayload{
		RequestID: fmt.Sprintf("acp_%d", time.Now().UnixNano()),
		Kind:      "session/request_permission",
		Command:   acpToolCallTitle(msg.Params),
		CWD:       sess.cwd,
		ThreadID:  sess.acpSessionID,
		RunID:     req.RunID,
		PromptID:  req.PromptID,
		Params:    msg.Params,
	}
	res, err := req.Approvals.RequestApproval(ctx, payload)
	if err != nil || res.Decision == "cancel" {
		_ = sess.client.respond(msg.ID, acpPermissionOutcome("cancelled", ""))
		return
	}
	optionID := rejectID
	if res.Decision == "accept" {
		optionID = allowID
	}
	if optionID == "" && len(options) > 0 {
		optionID = options[0]
	}
	_ = sess.client.respond(msg.ID, acpPermissionOutcome("selected", optionID))
}

// CloseSession releases the resident adapter process for a sid.
func (r *ACPRunner) CloseSession(sid string) {
	r.mu.Lock()
	sess := r.sessions[sid]
	delete(r.sessions, sid)
	r.mu.Unlock()
	if sess != nil && sess.client != nil {
		sess.client.close()
	}
}

// Close releases every resident session.
func (r *ACPRunner) Close() {
	r.mu.Lock()
	sessions := make([]*acpSession, 0, len(r.sessions))
	for sid, sess := range r.sessions {
		sessions = append(sessions, sess)
		delete(r.sessions, sid)
	}
	r.mu.Unlock()
	for _, sess := range sessions {
		if sess.client != nil {
			sess.client.close()
		}
	}
}

func (r *ACPRunner) initialize(ctx context.Context, sess *acpSession) error {
	raw, err := sess.client.request(ctx, "initialize", map[string]any{
		"protocolVersion": acpProtocolVersion,
		"clientInfo":      map[string]string{"name": "codex-bridge", "version": "dev"},
		"clientCapabilities": map[string]any{
			"fs":       map[string]any{"readTextFile": false, "writeTextFile": false},
			"terminal": false,
		},
	})
	if err != nil {
		return fmt.Errorf("acp initialize: %w", err)
	}
	sess.initialized = true
	sess.loadSession = acpAdvertisesLoadSession(raw)
	return nil
}

func (r *ACPRunner) newSession(ctx context.Context, sess *acpSession) (string, error) {
	raw, err := sess.client.request(ctx, "session/new", map[string]any{
		"cwd":        sess.cwd,
		"mcpServers": []any{},
	})
	if err != nil {
		return "", fmt.Errorf("acp session/new: %w", err)
	}
	id := acpSessionID(raw)
	if id == "" {
		return "", errors.New("acp session/new did not return a sessionId")
	}
	return id, nil
}

func (r *ACPRunner) loadSession(ctx context.Context, sess *acpSession, sessionID string) error {
	_, err := sess.client.request(ctx, "session/load", map[string]any{
		"sessionId":  sessionID,
		"cwd":        sess.cwd,
		"mcpServers": []any{},
	})
	if err != nil {
		return fmt.Errorf("acp session/load: %w", err)
	}
	return nil
}

func (r *ACPRunner) handleFor(sess *acpSession) SessionHandle {
	handle := SessionHandle{ACPSessionID: sess.acpSessionID, NativeResumeID: sess.nativeResume}
	if sess.nativeResume != "" {
		handle.NativeResumeCommand = r.nativeResumeCommand(sess.nativeResume)
	}
	return handle
}

func (r *ACPRunner) resolveCWD(reqCWD string) string {
	cwd := r.cfg.Bridge.CWD
	if strings.TrimSpace(reqCWD) != "" {
		cwd = reqCWD
	}
	if cwd == "" {
		cwd = "."
	}
	if abs, err := filepath.Abs(expandHome(cwd)); err == nil {
		return abs
	}
	return expandHome(cwd)
}

// adapterCommand returns the configured adapter command and args for the
// selected CLI.
func (r *ACPRunner) adapterCommand() (string, []string, error) {
	switch r.acpCLI() {
	case "codex":
		command := strings.TrimSpace(r.cfg.Bridge.ACP.CodexCommand)
		if command == "" {
			return "", nil, errors.New("bridge.acp.codex_command is not configured")
		}
		return command, append([]string(nil), r.cfg.Bridge.ACP.CodexArgs...), nil
	default: // claude
		command := strings.TrimSpace(r.cfg.Bridge.ACP.ClaudeCommand)
		if command == "" {
			return "", nil, errors.New("bridge.acp.claude_command is not configured")
		}
		return command, append([]string(nil), r.cfg.Bridge.ACP.ClaudeArgs...), nil
	}
}

func (r *ACPRunner) acpCLI() string {
	cli := strings.ToLower(strings.TrimSpace(r.cfg.Bridge.ACP.CLI))
	if cli == "codex" {
		return "codex"
	}
	return "claude"
}

// resolveNativeResumeID returns the underlying CLI's own session id used for a
// local resume command (target B), or "" when it cannot be honestly resolved.
func (r *ACPRunner) resolveNativeResumeID(acpSessionID, cwd string) string {
	if !r.cfg.Bridge.ACP.PreferNativeResume {
		return ""
	}
	switch r.acpCLI() {
	case "codex":
		// Prefer the ACP-reported id if it looks like a usable native id;
		// otherwise fall back to scanning ~/.codex/sessions for the latest
		// rollout under this cwd.
		if id := codexNativeResumeFromACP(acpSessionID); id != "" {
			return id
		}
		return scanCodexSessionID(cwd)
	default: // claude
		// The Claude ACP adapter's sessionId equals the native .jsonl id. We only
		// offer it when the native rollout file actually exists for this cwd so we
		// never fabricate an unresolvable command.
		if claudeSessionFileExists(cwd, acpSessionID) {
			return acpSessionID
		}
		// The rollout file may not be flushed yet right after session/new; still
		// offer the id because Claude reuses the ACP id verbatim.
		return acpSessionID
	}
}

func (r *ACPRunner) nativeResumeCommand(nativeID string) string {
	if nativeID == "" {
		return ""
	}
	switch r.acpCLI() {
	case "codex":
		return "codex resume " + nativeID
	default:
		return "claude --resume " + nativeID
	}
}

// ---- ACP payload parsing helpers ----

func acpAdvertisesLoadSession(raw json.RawMessage) bool {
	var out struct {
		AgentCapabilities struct {
			LoadSession bool `json:"loadSession"`
		} `json:"agentCapabilities"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return false
	}
	return out.AgentCapabilities.LoadSession
}

func acpSessionID(raw json.RawMessage) string {
	var out struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return ""
	}
	return out.SessionID
}

func acpStopReason(raw json.RawMessage) string {
	var out struct {
		StopReason string `json:"stopReason"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return ""
	}
	return strings.ToLower(out.StopReason)
}

// mapACPUpdate converts one SessionUpdate union value into a RunnerUpdate piece.
// It returns (text, tool, kind) where kind is "message_delta" or "tool" (or ""
// for updates we ignore). Tool calls are surfaced as RunnerToolEvent so the
// existing browser tool timeline keeps working.
func mapACPUpdate(raw json.RawMessage) (string, *RunnerToolEvent, string) {
	var head struct {
		SessionUpdate string `json:"sessionUpdate"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return "", nil, ""
	}
	switch head.SessionUpdate {
	case "agent_message_chunk":
		var u struct {
			Content acpContentBlock `json:"content"`
		}
		if err := json.Unmarshal(raw, &u); err != nil {
			return "", nil, ""
		}
		return u.Content.Text, nil, "message_delta"
	case "tool_call", "tool_call_update":
		tool := acpToolEvent(raw)
		if tool == nil {
			return "", nil, ""
		}
		return "", tool, "tool"
	default:
		// agent_thought_chunk, plan, available_commands_update, current_mode_update
		// are not surfaced as chat content in PR-1.
		return "", nil, ""
	}
}

type acpContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func acpToolEvent(raw json.RawMessage) *RunnerToolEvent {
	var u struct {
		ToolCallID string `json:"toolCallId"`
		Title      string `json:"title"`
		Status     string `json:"status"`
		RawInput   json.RawMessage `json:"rawInput"`
		Content    []struct {
			Type    string `json:"type"`
			Content struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil
	}
	if u.ToolCallID == "" && u.Title == "" && u.Status == "" {
		return nil
	}
	tool := &RunnerToolEvent{ID: u.ToolCallID, Command: u.Title, Status: u.Status}
	var out strings.Builder
	for _, c := range u.Content {
		out.WriteString(c.Content.Text)
	}
	tool.Output = out.String()
	return tool
}

func acpToolCallTitle(raw json.RawMessage) string {
	var u struct {
		ToolCall struct {
			Title string `json:"title"`
		} `json:"toolCall"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return ""
	}
	return u.ToolCall.Title
}

// acpPermissionOptions returns the available option ids plus the best
// allow/reject candidates based on each option's kind.
func acpPermissionOptions(raw json.RawMessage) (all []string, allowID, rejectID string) {
	var u struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	if err := json.Unmarshal(raw, &u); err != nil {
		return nil, "", ""
	}
	for _, o := range u.Options {
		all = append(all, o.OptionID)
		switch o.Kind {
		case "allow_once", "allow_always":
			if allowID == "" {
				allowID = o.OptionID
			}
		case "reject_once", "reject_always":
			if rejectID == "" {
				rejectID = o.OptionID
			}
		}
	}
	return all, allowID, rejectID
}

func acpPermissionOutcome(outcome, optionID string) map[string]any {
	body := map[string]any{"outcome": outcome}
	if outcome == "selected" {
		body["optionId"] = optionID
	}
	return map[string]any{"outcome": body}
}

// ---- native resume id resolution ----

func codexNativeResumeFromACP(acpSessionID string) string {
	// Codex ACP adapters may report the native thread id directly as the ACP
	// sessionId. We accept it when it looks like a codex thread/rollout id.
	id := strings.TrimSpace(acpSessionID)
	if id == "" {
		return ""
	}
	if strings.HasPrefix(id, "thr_") || looksLikeUUID(id) {
		return id
	}
	return ""
}

func looksLikeUUID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

func claudeSessionFileExists(cwd, sessionID string) bool {
	dir := claudeProjectDir(cwd)
	if dir == "" || sessionID == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, sessionID+".jsonl"))
	return err == nil
}

// claudeProjectDir maps an absolute cwd to ~/.claude/projects/<encoded-cwd>.
// Claude encodes the path by replacing path separators with dashes.
func claudeProjectDir(cwd string) string {
	home, err := os.UserHomeDir()
	if err != nil || cwd == "" {
		return ""
	}
	encoded := strings.ReplaceAll(cwd, string(filepath.Separator), "-")
	return filepath.Join(home, ".claude", "projects", encoded)
}

// scanCodexSessionID returns the most recent codex rollout id under
// ~/.codex/sessions that matches the cwd, or "" when none is found. This is the
// degradation path used when the adapter does not report a usable native id.
func scanCodexSessionID(cwd string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	root := filepath.Join(home, ".codex", "sessions")
	type candidate struct {
		id      string
		modTime time.Time
	}
	var found []candidate
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		id := codexRolloutID(name)
		if id == "" {
			return nil
		}
		found = append(found, candidate{id: id, modTime: info.ModTime()})
		return nil
	})
	if len(found) == 0 {
		return ""
	}
	sort.Slice(found, func(i, j int) bool {
		return found[i].modTime.After(found[j].modTime)
	})
	return found[0].id
}

// codexRolloutID extracts the trailing UUID from a codex rollout filename like
// rollout-2026-05-31T12-00-00-<uuid>.jsonl.
func codexRolloutID(name string) string {
	base := strings.TrimSuffix(name, ".jsonl")
	if looksLikeUUID(base) {
		return base
	}
	if idx := strings.LastIndex(base, "-"); idx >= 0 && idx+1 < len(base) {
		// Try last 36 chars as a UUID.
		if len(base) >= 36 {
			tail := base[len(base)-36:]
			if looksLikeUUID(tail) {
				return tail
			}
		}
	}
	return ""
}

// Ensure ACPRunner satisfies SessionRunner at compile time.
var _ SessionRunner = (*ACPRunner)(nil)
