package bridge

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestCodexAppServerRunnerApprovalRoundTrip(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"

	approvals := &recordingApprovalRequester{}
	var deltas []string
	result, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{
		Content:   "run it",
		RunID:     "run_1",
		PromptID:  "prm_1",
		Approvals: approvals,
	}, func(update RunnerUpdate) {
		if update.Delta != "" {
			deltas = append(deltas, update.Delta)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RemoteThreadID != "thr_app" || result.Content != "done" {
		t.Fatalf("result = %#v", result)
	}
	if strings.Join(deltas, "") != "done" {
		t.Fatalf("deltas = %#v", deltas)
	}
	if approvals.request.RequestID != "99" || approvals.request.Command != "echo ok" || approvals.request.RunID != "run_1" || approvals.request.PromptID != "prm_1" {
		t.Fatalf("approval request = %#v", approvals.request)
	}
}

func TestApprovalResponseForUsesSessionScopedAcceptance(t *testing.T) {
	tests := []struct {
		method string
		want   any
	}{
		{"item/commandExecution/requestApproval", map[string]any{"decision": "acceptForSession"}},
		{"item/permissions/requestApproval", map[string]any{"permissions": map[string]any{}, "scope": "session"}},
		{"execCommandApproval", map[string]any{"decision": "approved_for_session"}},
		{"applyPatchApproval", map[string]any{"decision": "approved_for_session"}},
	}
	for _, tc := range tests {
		if got := approvalResponseFor(tc.method, "accept"); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("approvalResponseFor(%q) = %#v, want %#v", tc.method, got, tc.want)
		}
	}
}

func TestAutomaticApprovalResponseIsRequestScoped(t *testing.T) {
	tests := []struct {
		method string
		want   any
	}{
		{"item/commandExecution/requestApproval", map[string]any{"decision": "accept"}},
		{"execCommandApproval", map[string]any{"decision": "approved"}},
	}
	for _, tc := range tests {
		if got := automaticApprovalResponseFor(tc.method); !reflect.DeepEqual(got, tc.want) {
			t.Fatalf("automaticApprovalResponseFor(%q) = %#v, want %#v", tc.method, got, tc.want)
		}
	}
}

func TestCodexAppServerRunnerSanitizesPromptText(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	capturedPath := filepath.Join(tmp, "turn-start.json")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerCapturePromptScript(capturedPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	_, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{
		Content: "before " + string([]byte{0xff}) + " after",
	}, func(update RunnerUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(capturedPath)
	if err != nil {
		t.Fatal(err)
	}
	var captured map[string]any
	if err := json.Unmarshal(raw, &captured); err != nil {
		t.Fatal(err)
	}
	params, _ := captured["params"].(map[string]any)
	input, _ := params["input"].([]any)
	first, _ := input[0].(map[string]any)
	text, _ := first["text"].(string)
	if strings.Contains(text, string([]byte{0xff})) || !strings.Contains(text, "\uFFFD") || !strings.Contains(text, "before") || !strings.Contains(text, "after") {
		t.Fatalf("captured prompt was not sanitized: %q", text)
	}
}

func TestCodexAppServerRunnerEmptyErrorAfterVisibleOutputCompletes(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerEmptyErrorScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	var deltas []string
	result, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{
		Content: "reason about proof",
	}, func(update RunnerUpdate) {
		if update.Delta != "" {
			deltas = append(deltas, update.Delta)
		}
	})
	if err != nil {
		t.Fatalf("empty app-server error after visible output should complete: %v", err)
	}
	if result.Content != "rewrite Habs direction was wrong" {
		t.Fatalf("result content = %q", result.Content)
	}
	if strings.Join(deltas, "") != "rewrite Habs direction was wrong" {
		t.Fatalf("deltas = %#v", deltas)
	}
}

func TestCodexAppServerRunnerIgnoresUnscopedEmptyErrorBeforeCompletion(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerUnscopedErrorThenCompletionScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	result, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{Content: "keep going"}, func(RunnerUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "final after protocol noise" {
		t.Fatalf("result content = %q", result.Content)
	}
}

func TestCodexAppServerRunnerBoundsUnscopedEmptyErrorWait(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerUnscopedEmptyErrorOnlyScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	started := time.Now()
	_, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{Content: "do not hang"}, func(RunnerUpdate) {})
	if err == nil || !strings.Contains(err.Error(), "returned an empty error") {
		t.Fatalf("unscoped empty error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("unscoped empty error took %s, want a bounded grace period", elapsed)
	}
}

func TestCodexAppServerRunnerRecoversContentFromCompletedTurn(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerTerminalContentScript(false)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	var contentUpdates []string
	result, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{Content: "finish"}, func(update RunnerUpdate) {
		if update.Content != "" {
			contentUpdates = append(contentUpdates, update.Content)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "terminal-only answer" || strings.Join(contentUpdates, "") != "terminal-only answer" {
		t.Fatalf("result = %#v, content updates = %#v", result, contentUpdates)
	}
}

func TestCodexAppServerRunnerRejectsEmptyCompletedTurn(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerTerminalContentScript(true)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	_, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{Content: "finish"}, func(RunnerUpdate) {})
	if err == nil || !strings.Contains(err.Error(), "without an assistant response") {
		t.Fatalf("empty completed turn error = %v", err)
	}
}

func TestCodexAppServerRunnerRetriesPreparationOnce(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	attemptPath := filepath.Join(tmp, "attempts")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerPreparationRetryScript(attemptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	result, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{Content: "retry safely"}, func(RunnerUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "recovered" {
		t.Fatalf("result content = %q", result.Content)
	}
	attempts, err := os.ReadFile(attemptPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(attempts) != "2" {
		t.Fatalf("process attempts = %q, want 2", attempts)
	}
}

func TestCodexAppServerRunnerIncludesPreparationDiagnostics(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerPreparationFailureScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	_, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{Content: "diagnose"}, func(RunnerUpdate) {})
	if err == nil || !strings.Contains(err.Error(), "initialization rejected") || !strings.Contains(err.Error(), "stderr preparation detail") {
		t.Fatalf("preparation error = %v", err)
	}
}

func TestCodexAppServerRunnerDoesNotRetryTurnStart(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	turnPath := filepath.Join(tmp, "turn-starts")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerTurnStartFailureScript(turnPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	_, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{Content: "do not replay"}, func(RunnerUpdate) {})
	if err == nil || !strings.Contains(err.Error(), "turn rejected") {
		t.Fatalf("turn/start error = %v", err)
	}
	starts, readErr := os.ReadFile(turnPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(starts) != "1" {
		t.Fatalf("turn/start calls = %q, want 1", starts)
	}
}

func TestCodexAppServerRunnerIgnoresStaleTurnCompleted(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerStaleTurnScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	var deltas []string
	result, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{
		Content: "do the actual work",
	}, func(update RunnerUpdate) {
		if update.Delta != "" {
			deltas = append(deltas, update.Delta)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "real final" {
		t.Fatalf("result content = %q", result.Content)
	}
	if strings.Join(deltas, "") != "real final" {
		t.Fatalf("deltas = %#v", deltas)
	}
}

func TestCodexAppServerRunnerIgnoresStaleTurnStartedBeforeResponse(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerStaleTurnStartedScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	result, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{Content: "current turn"}, func(RunnerUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "current answer" {
		t.Fatalf("result content = %q, want current answer", result.Content)
	}
}

func TestCodexAppServerRunnerRejectsStaleApprovalBeforeResponse(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerStaleApprovalScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	approvals := &collectingApprovalRequester{}

	result, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{
		Content:   "current approval",
		Approvals: approvals,
	}, func(RunnerUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "approved current turn" {
		t.Fatalf("result content = %q", result.Content)
	}
	requests := approvals.snapshot()
	if len(requests) != 1 || requests[0].TurnID != "turn_current" || requests[0].Command != "echo current" {
		t.Fatalf("approval requests = %#v", requests)
	}
}

func TestCodexAppServerRunnerFailedCommandWithoutFollowupFails(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerFailedCommandScript(false)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	var deltas []string
	var tools []RunnerToolEvent
	result, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{
		Content: "continue proof",
	}, func(update RunnerUpdate) {
		if update.Delta != "" {
			deltas = append(deltas, update.Delta)
		}
		if update.Tool != nil {
			tools = append(tools, *update.Tool)
		}
	})
	if err == nil {
		t.Fatal("expected failed command without follow-up to fail the runner")
	}
	for _, want := range []string{"coqc -R . LinLattice HWQ_U/L0Proof.v", "Unable to unify", "exit code 1", "without a follow-up response"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
	if result.Content != "继续编译。" {
		t.Fatalf("visible content = %q", result.Content)
	}
	if strings.Join(deltas, "") != "继续编译。" {
		t.Fatalf("deltas = %#v", deltas)
	}
	if len(tools) != 2 || !runnerToolEventFailed(tools[1]) {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestCodexAppServerRunnerFailedCommandWithFollowupCompletes(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerFailedCommandScript(true)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp

	result, err := NewCodexAppServerRunner(&cfg).Prompt(context.Background(), RunnerRequest{
		Content: "continue proof",
	}, func(update RunnerUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "我会根据这个错误继续修正。") {
		t.Fatalf("result content = %q", result.Content)
	}
}

func TestCodexAppServerThreadStartIsPersisted(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CWD = "/work/tree"
	params := NewCodexAppServerRunner(&cfg).threadStartParams()
	if got, ok := params["ephemeral"].(bool); !ok || got {
		t.Fatalf("threadStartParams ephemeral = %#v, want false for native resume visibility", params["ephemeral"])
	}
	if got, _ := params["threadSource"].(string); got != "user" {
		t.Fatalf("threadStartParams threadSource = %q, want user", got)
	}
}

func TestAppServerClientCloseInterruptsBlockedWrite(t *testing.T) {
	stdin := newBlockingWriteCloser()
	client := &appServerClient{
		stdin:       stdin,
		pending:     make(map[int64]chan appServerResponse),
		events:      make(chan appServerMessage),
		stop:        make(chan struct{}),
		diagnostics: &appServerDiagnosticTail{limit: appServerDiagnosticTailBytes},
	}
	requestDone := make(chan error, 1)
	go func() {
		_, err := client.request(context.Background(), "initialize", map[string]any{})
		requestDone <- err
	}()
	select {
	case <-stdin.started:
	case <-time.After(time.Second):
		t.Fatal("request did not begin writing")
	}

	closeDone := make(chan struct{})
	go func() {
		client.closeWithTimeout(0)
		close(closeDone)
	}()
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		stdin.forceClose()
		<-closeDone
		t.Fatal("client close blocked behind stdin write")
	}
	select {
	case err := <-requestDone:
		if err == nil {
			t.Fatal("blocked request completed without an error")
		}
	case <-time.After(time.Second):
		t.Fatal("blocked request was not interrupted")
	}
}

func TestAppServerClientCloseStopsBlockedEventDelivery(t *testing.T) {
	stdin := newBlockingWriteCloser()
	client := &appServerClient{
		stdin:       stdin,
		pending:     make(map[int64]chan appServerResponse),
		events:      make(chan appServerMessage),
		stop:        make(chan struct{}),
		diagnostics: &appServerDiagnosticTail{limit: appServerDiagnosticTailBytes},
	}
	readDone := make(chan struct{})
	go func() {
		client.read(strings.NewReader("{\"method\":\"first\"}\n"))
		close(readDone)
	}()
	time.Sleep(20 * time.Millisecond)
	client.closeWithTimeout(0)
	select {
	case <-readDone:
	case <-time.After(time.Second):
		select {
		case <-client.events:
		default:
		}
		<-readDone
		t.Fatal("stdout reader remained blocked on event delivery")
	}
}

func TestAppServerTurnScopeReadyPathDoesNotAllocateTimer(t *testing.T) {
	scope := newAppServerTurnScope("thread_1")
	scope.setTurnID("turn_1")
	allocations := testing.AllocsPerRun(1000, func() {
		scope.waitForTurnID(context.Background())
	})
	if allocations >= 1 {
		t.Fatalf("ready turn scope allocations = %f, want less than one", allocations)
	}
}

func TestAppServerDiagnosticTailReusesBoundedStorage(t *testing.T) {
	tail := &appServerDiagnosticTail{limit: 64}
	payload := []byte(strings.Repeat("x", 1024*1024) + "tail-marker")
	if _, err := tail.Write(payload); err != nil {
		t.Fatal(err)
	}
	allocations := testing.AllocsPerRun(100, func() {
		_, _ = tail.Write(payload)
	})
	if allocations >= 1 {
		t.Fatalf("diagnostic tail allocations = %f, want bounded storage reuse", allocations)
	}
	if got := tail.String(); !strings.HasSuffix(got, "tail-marker") || len(got) > tail.limit {
		t.Fatalf("diagnostic tail = %q", got)
	}
}

type recordingApprovalRequester struct {
	request protocol.ApprovalRequestPayload
}

func (r *recordingApprovalRequester) RequestApproval(ctx context.Context, req protocol.ApprovalRequestPayload) (protocol.ApprovalResponsePayload, error) {
	r.request = req
	return protocol.ApprovalResponsePayload{RequestID: req.RequestID, Decision: "accept"}, nil
}

type collectingApprovalRequester struct {
	mu       sync.Mutex
	requests []protocol.ApprovalRequestPayload
}

func (r *collectingApprovalRequester) RequestApproval(_ context.Context, req protocol.ApprovalRequestPayload) (protocol.ApprovalResponsePayload, error) {
	r.mu.Lock()
	r.requests = append(r.requests, req)
	r.mu.Unlock()
	return protocol.ApprovalResponsePayload{RequestID: req.RequestID, Decision: "accept"}, nil
}

func (r *collectingApprovalRequester) snapshot() []protocol.ApprovalRequestPayload {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]protocol.ApprovalRequestPayload(nil), r.requests...)
}

type blockingWriteCloser struct {
	started     chan struct{}
	released    chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func newBlockingWriteCloser() *blockingWriteCloser {
	return &blockingWriteCloser{started: make(chan struct{}), released: make(chan struct{})}
}

func (w *blockingWriteCloser) Write([]byte) (int, error) {
	w.startedOnce.Do(func() { close(w.started) })
	<-w.released
	return 0, io.ErrClosedPipe
}

func (w *blockingWriteCloser) Close() error {
	w.forceClose()
	return nil
}

func (w *blockingWriteCloser) forceClose() {
	w.releaseOnce.Do(func() { close(w.released) })
}

func fakeCodexAppServerScript() string {
	return `#!/usr/bin/env python3
import json
import sys

if len(sys.argv) < 2 or sys.argv[1] != "app-server":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "thread/start":
        if msg.get("params", {}).get("ephemeral") is not False:
            emit({"id": msg["id"], "error": {"code": -32600, "message": "missing persisted thread flag"}})
            sys.exit(1)
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_app"}}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {"status": "unsubscribed"}})
    elif method == "turn/start":
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_1", "items": [], "itemsView": "notLoaded", "status": "inProgress", "error": None, "startedAt": None, "completedAt": None, "durationMs": None}}})
        emit({"jsonrpc": "2.0", "id": 99, "method": "item/commandExecution/requestApproval", "params": {"threadId": "thr_app", "turnId": "turn_1", "itemId": "cmd_1", "command": "echo ok", "cwd": "/tmp", "reason": "test"}})
    elif msg.get("id") == 99:
        emit({"method": "item/started", "params": {"item": {"id": "cmd_1", "type": "commandExecution", "command": "echo ok", "status": "running"}}})
        emit({"method": "item/completed", "params": {"item": {"id": "cmd_1", "type": "commandExecution", "command": "echo ok", "status": "completed", "exitCode": 0, "aggregatedOutput": "ok\n"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_app", "turnId": "turn_1", "itemId": "msg_1", "delta": "done"}})
        emit({"method": "turn/completed", "params": {"threadId": "thr_app", "turn": {"id": "turn_1", "items": [], "itemsView": "notLoaded", "status": "completed", "error": None, "startedAt": 1, "completedAt": 2, "durationMs": 1}}})
        sys.exit(0)
`
}

func fakeCodexAppServerCapturePromptScript(capturedPath string) string {
	capturedPathRaw, _ := json.Marshal(capturedPath)
	return `#!/usr/bin/env python3
import json
import sys

captured_path = ` + string(capturedPathRaw) + `

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "thread/start":
        if msg.get("params", {}).get("ephemeral") is not False:
            emit({"id": msg["id"], "error": {"code": -32600, "message": "missing persisted thread flag"}})
            sys.exit(1)
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_app"}}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {"status": "unsubscribed"}})
    elif method == "turn/start":
        with open(captured_path, "w", encoding="utf-8") as f:
            json.dump(msg, f, ensure_ascii=False)
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_1", "items": [], "itemsView": "notLoaded", "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_app", "turnId": "turn_1", "delta": "done"}})
        emit({"method": "turn/completed", "params": {"threadId": "thr_app", "turn": {"id": "turn_1", "status": "completed"}}})
        sys.exit(0)
`
}

func fakeCodexAppServerEmptyErrorScript() string {
	return `#!/usr/bin/env python3
import json
import sys

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_empty_error"}}})
    elif method == "thread/name/set":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {"status": "unsubscribed"}})
    elif method == "turn/start":
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_1", "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_empty_error", "turnId": "turn_1", "delta": "rewrite Habs direction was wrong"}})
        emit({"method": "error", "params": {"message": ""}})
        sys.exit(0)
`
}

func fakeCodexAppServerUnscopedErrorThenCompletionScript() string {
	return `#!/usr/bin/env python3
import json
import sys

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_noise"}}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {}})
    elif method == "turn/start":
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_noise", "status": "inProgress"}}})
        emit({"method": "error", "params": {"message": ""}})
        import time
        time.sleep(1)
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_noise", "turnId": "turn_noise", "delta": "final after protocol noise"}})
        emit({"method": "turn/completed", "params": {"threadId": "thr_noise", "turn": {"id": "turn_noise", "status": "completed"}}})
`
}

func fakeCodexAppServerUnscopedEmptyErrorOnlyScript() string {
	return `#!/usr/bin/env python3
import json
import sys

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_empty_only"}}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {}})
    elif method == "turn/start":
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_empty_only", "status": "inProgress"}}})
        emit({"method": "error", "params": {"message": ""}})
`
}

func fakeCodexAppServerTerminalContentScript(empty bool) string {
	items := `[{"id":"message_1","type":"agentMessage","text":"terminal-only answer"}]`
	if empty {
		items = `[]`
	}
	return `#!/usr/bin/env python3
import json
import sys

items = ` + items + `

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_terminal"}}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {}})
    elif method == "turn/start":
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_terminal", "status": "inProgress"}}})
        emit({"method": "turn/completed", "params": {"threadId": "thr_terminal", "turn": {"id": "turn_terminal", "status": "completed", "items": items}}})
`
}

func fakeCodexAppServerPreparationRetryScript(attemptPath string) string {
	attemptPathJSON, _ := json.Marshal(attemptPath)
	return `#!/usr/bin/env python3
import json
import os
import sys

attempt_path = ` + string(attemptPathJSON) + `
attempt = int(open(attempt_path).read()) + 1 if os.path.exists(attempt_path) else 1
with open(attempt_path, "w") as handle:
    handle.write(str(attempt))

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        if attempt == 1:
            print("temporary initialization failure", file=sys.stderr, flush=True)
            emit({"id": msg["id"], "error": {"code": -32000, "message": "temporarily unavailable"}})
            sys.exit(1)
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_retry"}}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {}})
    elif method == "turn/start":
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_retry", "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_retry", "turnId": "turn_retry", "delta": "recovered"}})
        emit({"method": "turn/completed", "params": {"threadId": "thr_retry", "turn": {"id": "turn_retry", "status": "completed"}}})
`
}

func fakeCodexAppServerPreparationFailureScript() string {
	return `#!/usr/bin/env python3
import json
import sys

for line in sys.stdin:
    msg = json.loads(line)
    if msg.get("method") == "initialize":
        print("stderr preparation detail", file=sys.stderr, flush=True)
        print(json.dumps({"id": msg["id"], "error": {"code": -32000, "message": "initialization rejected"}}), flush=True)
        sys.exit(1)
`
}

func fakeCodexAppServerTurnStartFailureScript(turnPath string) string {
	turnPathJSON, _ := json.Marshal(turnPath)
	return `#!/usr/bin/env python3
import json
import os
import sys

turn_path = ` + string(turnPathJSON) + `

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_no_replay"}}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {}})
    elif method == "turn/start":
        starts = int(open(turn_path).read()) + 1 if os.path.exists(turn_path) else 1
        with open(turn_path, "w") as handle:
            handle.write(str(starts))
        emit({"id": msg["id"], "error": {"code": -32001, "message": "turn rejected"}})
`
}

func fakeCodexAppServerStaleTurnScript() string {
	return `#!/usr/bin/env python3
import json
import sys

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_app"}}})
    elif method == "thread/resume":
        params = msg.get("params") or {}
        emit({"id": msg["id"], "result": {"thread": {"id": params.get("threadId") or "thr_app"}}})
    elif method == "thread/name/set":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {"status": "unsubscribed"}})
    elif method == "turn/start":
        emit({"method": "turn/completed", "params": {"threadId": "thr_app", "turn": {"id": "turn_stale", "status": "completed"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_app", "turnId": "turn_stale", "delta": "stale"}})
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_real", "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_app", "turnId": "turn_real", "delta": "real final"}})
        emit({"method": "turn/completed", "params": {"threadId": "thr_app", "turn": {"id": "turn_real", "status": "completed"}}})
        sys.exit(0)
`
}

func fakeCodexAppServerStaleTurnStartedScript() string {
	return `#!/usr/bin/env python3
import json
import sys

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_scope"}}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {}})
    elif method == "turn/start":
        emit({"method": "turn/started", "params": {"threadId": "thr_scope", "turn": {"id": "turn_stale", "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_scope", "turnId": "turn_stale", "delta": "stale answer"}})
        emit({"method": "turn/completed", "params": {"threadId": "thr_scope", "turn": {"id": "turn_stale", "status": "completed"}}})
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_current", "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_scope", "turnId": "turn_current", "delta": "current answer"}})
        emit({"method": "turn/completed", "params": {"threadId": "thr_scope", "turn": {"id": "turn_current", "status": "completed"}}})
`
}

func fakeCodexAppServerStaleApprovalScript() string {
	return `#!/usr/bin/env python3
import json
import sys

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_approval_scope"}}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {}})
    elif method == "turn/start":
        emit({"method": "turn/started", "params": {"threadId": "thr_approval_scope", "turn": {"id": "turn_stale"}}})
        emit({"jsonrpc": "2.0", "id": 98, "method": "item/commandExecution/requestApproval", "params": {"threadId": "thr_approval_scope", "turnId": "turn_stale", "itemId": "cmd_stale", "command": "echo stale"}})
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_current", "status": "inProgress"}}})
        emit({"jsonrpc": "2.0", "id": 99, "method": "item/commandExecution/requestApproval", "params": {"threadId": "thr_approval_scope", "turnId": "turn_current", "itemId": "cmd_current", "command": "echo current"}})
    elif msg.get("id") == 99:
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_approval_scope", "turnId": "turn_current", "delta": "approved current turn"}})
        emit({"method": "turn/completed", "params": {"threadId": "thr_approval_scope", "turn": {"id": "turn_current", "status": "completed"}}})
`
}

func fakeCodexAppServerFailedCommandScript(withFollowup bool) string {
	followup := ""
	if withFollowup {
		followup = `        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_app", "turnId": "turn_1", "itemId": "msg_2", "delta": "我会根据这个错误继续修正。"}})
`
	}
	return `#!/usr/bin/env python3
import json
import sys

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_app"}}})
    elif method == "thread/resume":
        params = msg.get("params") or {}
        emit({"id": msg["id"], "result": {"thread": {"id": params.get("threadId") or "thr_app"}}})
    elif method == "thread/name/set":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {"status": "unsubscribed"}})
    elif method == "turn/start":
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_1", "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": "thr_app", "turnId": "turn_1", "itemId": "msg_1", "delta": "继续编译。"}})
        emit({"method": "item/started", "params": {"threadId": "thr_app", "turnId": "turn_1", "item": {"id": "cmd_1", "turnId": "turn_1", "type": "commandExecution", "command": "coqc -R . LinLattice HWQ_U/L0Proof.v", "status": "running"}}})
        emit({"method": "item/completed", "params": {"threadId": "thr_app", "turnId": "turn_1", "item": {"id": "cmd_1", "turnId": "turn_1", "type": "commandExecution", "command": "coqc -R . LinLattice HWQ_U/L0Proof.v", "status": "failed", "exitCode": 1, "aggregatedOutput": "File ./HWQ_U/L0Proof.v, line 24, characters 2-18:\nError: Unable to unify proof state.\n"}}})
` + followup + `        emit({"method": "turn/completed", "params": {"threadId": "thr_app", "turn": {"id": "turn_1", "status": "completed"}}})
        sys.exit(0)
`
}
