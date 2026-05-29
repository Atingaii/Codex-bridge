package bridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

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

type recordingApprovalRequester struct {
	request protocol.ApprovalRequestPayload
}

func (r *recordingApprovalRequester) RequestApproval(ctx context.Context, req protocol.ApprovalRequestPayload) (protocol.ApprovalResponsePayload, error) {
	r.request = req
	return protocol.ApprovalResponsePayload{RequestID: req.RequestID, Decision: "accept"}, nil
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
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_app"}}})
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
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_app"}}})
    elif method == "turn/start":
        with open(captured_path, "w", encoding="utf-8") as f:
            json.dump(msg, f, ensure_ascii=False)
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_1", "items": [], "itemsView": "notLoaded", "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"delta": "done"}})
        emit({"method": "turn/completed", "params": {"turn": {"id": "turn_1", "status": "completed"}}})
        sys.exit(0)
`
}
