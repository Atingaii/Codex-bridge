package bridge

import (
	"context"
	"os"
	"path/filepath"
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
