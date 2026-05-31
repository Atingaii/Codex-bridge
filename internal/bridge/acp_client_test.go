package bridge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

// fakeACPAdapterSource is a minimal ACP adapter used to exercise the resident
// session flow end to end without a real CLI. It implements initialize,
// session/new, session/load, and session/prompt, streams an agent message and a
// tool call, raises one session/request_permission, and finishes with end_turn.
const fakeACPAdapterSource = `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
)

func write(v map[string]any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

func main() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for in.Scan() {
		var msg map[string]any
		if err := json.Unmarshal(in.Bytes(), &msg); err != nil {
			continue
		}
		method, _ := msg["method"].(string)
		id, hasID := msg["id"]
		switch method {
		case "initialize":
			write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
				"protocolVersion":   1,
				"agentCapabilities": map[string]any{"loadSession": true},
			}})
		case "session/new":
			write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{
				"sessionId": "11111111-1111-1111-1111-111111111111",
			}})
		case "session/load":
			write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
		case "session/prompt":
			params, _ := msg["params"].(map[string]any)
			sid, _ := params["sessionId"].(string)
			// Stream an agent message chunk.
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "agent_message_chunk",
					"content":       map[string]any{"type": "text", "text": "hello from acp"},
				},
			}})
			// Ask for permission (reverse request).
			write(map[string]any{"jsonrpc": "2.0", "id": 9001, "method": "session/request_permission", "params": map[string]any{
				"sessionId": sid,
				"toolCall":  map[string]any{"title": "run dangerous"},
				"options": []map[string]any{
					{"optionId": "allow", "kind": "allow_once"},
					{"optionId": "deny", "kind": "reject_once"},
				},
			}})
			// Read the permission response from the client.
			if in.Scan() {
				var resp map[string]any
				_ = json.Unmarshal(in.Bytes(), &resp)
			}
			// Stream a tool call update.
			write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{
				"sessionId": sid,
				"update": map[string]any{
					"sessionUpdate": "tool_call",
					"toolCallId":    "tc_1",
					"title":         "run dangerous",
					"status":        "completed",
				},
			}})
			// Complete the turn.
			write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"stopReason": "end_turn"}})
		default:
			if hasID {
				write(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{}})
			}
		}
	}
}
`

func buildFakeACPAdapter(t *testing.T) string {
	t.Helper()
	goBin := goToolPath()
	if goBin == "" {
		t.Skip("go toolchain not found for building fake ACP adapter")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte(fakeACPAdapterSource), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "fake-acp")
	cmd := exec.Command(goBin, "build", "-o", bin, src)
	cmd.Env = append(os.Environ(), "GO111MODULE=off", "CGO_ENABLED=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build fake adapter: %v\n%s", err, out)
	}
	return bin
}

func goToolPath() string {
	for _, candidate := range []string{"/usr/local/go/bin/go", "go"} {
		if p, err := exec.LookPath(candidate); err == nil {
			return p
		}
	}
	return ""
}

type recordingApprovals struct {
	last     protocol.ApprovalRequestPayload
	decision string
}

func (r *recordingApprovals) RequestApproval(ctx context.Context, req protocol.ApprovalRequestPayload) (protocol.ApprovalResponsePayload, error) {
	r.last = req
	return protocol.ApprovalResponsePayload{RequestID: req.RequestID, Decision: r.decision}, nil
}

func TestACPRunnerEndToEndWithFakeAdapter(t *testing.T) {
	bin := buildFakeACPAdapter(t)

	cfg := config.Default()
	cfg.Bridge.Runner = "acp"
	cfg.Bridge.ACP.CLI = "claude"
	cfg.Bridge.ACP.ClaudeCommand = bin
	cfg.Bridge.ACP.ClaudeArgs = nil
	cfg.Bridge.ACP.PreferNativeResume = true
	cfg.Bridge.CWD = t.TempDir()

	r := NewACPRunner(&cfg)
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	approvals := &recordingApprovals{decision: "accept"}
	handle, err := r.OpenSession(ctx, OpenSessionRequest{SID: "s1", CWD: cfg.Bridge.CWD, Approvals: approvals})
	if err != nil {
		t.Fatalf("OpenSession: %v", err)
	}
	if handle.ACPSessionID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("acp session id = %q", handle.ACPSessionID)
	}
	// Claude reuses the ACP id as the native resume id.
	if handle.NativeResumeID != handle.ACPSessionID {
		t.Fatalf("native resume id = %q, want %q", handle.NativeResumeID, handle.ACPSessionID)
	}
	if handle.NativeResumeCommand != "claude --resume "+handle.ACPSessionID {
		t.Fatalf("native resume command = %q", handle.NativeResumeCommand)
	}

	var deltas strings.Builder
	var tools []RunnerToolEvent
	result, err := r.PromptSession(ctx, PromptSessionRequest{SID: "s1", Content: "hi", Approvals: approvals}, func(u RunnerUpdate) {
		deltas.WriteString(u.Delta)
		if u.Tool != nil {
			tools = append(tools, *u.Tool)
		}
	})
	if err != nil {
		t.Fatalf("PromptSession: %v", err)
	}
	if deltas.String() != "hello from acp" {
		t.Fatalf("streamed deltas = %q", deltas.String())
	}
	if result.Content != "hello from acp" {
		t.Fatalf("result content = %q", result.Content)
	}
	if len(tools) != 1 || tools[0].ID != "tc_1" || tools[0].Status != "completed" {
		t.Fatalf("tools = %#v", tools)
	}
	// The reverse permission request must have been routed to the approver.
	if approvals.last.Kind != "session/request_permission" {
		t.Fatalf("approval kind = %q", approvals.last.Kind)
	}
	if approvals.last.Command != "run dangerous" {
		t.Fatalf("approval command = %q", approvals.last.Command)
	}

	// Re-opening the same sid reuses the resident session (same process/id).
	handle2, err := r.OpenSession(ctx, OpenSessionRequest{SID: "s1", CWD: cfg.Bridge.CWD, Approvals: approvals})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if handle2.ACPSessionID != handle.ACPSessionID {
		t.Fatalf("reopen changed session id: %q -> %q", handle.ACPSessionID, handle2.ACPSessionID)
	}
}

func TestACPRunnerPromptSessionWithoutOpen(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Runner = "acp"
	r := NewACPRunner(&cfg)
	defer r.Close()
	_, err := r.PromptSession(context.Background(), PromptSessionRequest{SID: "missing", Content: "x"}, func(RunnerUpdate) {})
	if err == nil {
		t.Fatal("expected error prompting an unopened session")
	}
}
