package bridge

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/tencent/codex-bridge/internal/config"
)

func TestCodexExecScanJSONL(t *testing.T) {
	input := strings.NewReader(`{"type":"thread.started","thread_id":"thr_1"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"hello"}}
{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":2}}
	`)
	var updates []RunnerUpdate
	got, err := (&CodexExecRunner{}).scanJSONL(input, "", func(update RunnerUpdate) {
		updates = append(updates, update)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.RemoteThreadID != "thr_1" {
		t.Fatalf("thread id = %q", got.RemoteThreadID)
	}
	if got.Content != "hello" {
		t.Fatalf("content = %q", got.Content)
	}
	if len(updates) != 1 || updates[0].Content != "hello" {
		t.Fatalf("updates = %#v", updates)
	}
	if string(got.Usage) != `{"input_tokens":1,"output_tokens":2}` {
		t.Fatalf("usage = %s", got.Usage)
	}
}

func TestSanitizePromptTextReplacesInvalidUTF8(t *testing.T) {
	got := sanitizePromptText("ok" + string([]byte{0xff}) + "done")
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizePromptText returned invalid UTF-8 bytes: % x", []byte(got))
	}
	if !strings.Contains(got, "ok") || !strings.Contains(got, "done") {
		t.Fatalf("sanitizePromptText dropped surrounding content: %q", got)
	}
}

func TestCodexExecScanJSONLStreamingDeltas(t *testing.T) {
	input := strings.NewReader(`{"type":"thread.started","thread":{"id":"thr_nested"}}
{"type":"item.agent_message.delta","params":{"delta":"hel"}}
{"type":"response.output_text.delta","delta":"lo"}
{"type":"turn.completed","usage":{"input_tokens":3,"output_tokens":4}}
	`)
	var updates []RunnerUpdate
	got, err := (&CodexExecRunner{}).scanJSONL(input, "", func(update RunnerUpdate) {
		updates = append(updates, update)
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.RemoteThreadID != "thr_nested" {
		t.Fatalf("thread id = %q", got.RemoteThreadID)
	}
	if got.Content != "hello" {
		t.Fatalf("content = %q", got.Content)
	}
	var streamed strings.Builder
	for _, update := range updates {
		streamed.WriteString(update.Delta)
	}
	if streamed.String() != "hello" {
		t.Fatalf("updates = %#v", updates)
	}
	if string(got.Usage) != `{"input_tokens":3,"output_tokens":4}` {
		t.Fatalf("usage = %s", got.Usage)
	}
}

func TestCodexExecScanJSONLUsesFinalAgentMessage(t *testing.T) {
	input := strings.NewReader(`{"type":"thread.started","thread_id":"thr_final"}
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"我先列一下当前工作目录。"}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","aggregated_output":"bridge\nVibeHub\n","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"当前目录下有：\n\n- bridge/\n- VibeHub/"}}
{"type":"turn.completed","usage":{"input_tokens":5,"output_tokens":6}}
	`)
	var updates []RunnerUpdate
	got, err := (&CodexExecRunner{}).scanJSONL(input, "", func(update RunnerUpdate) {
		updates = append(updates, update)
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "当前目录下有：\n\n- bridge/\n- VibeHub/"
	if got.Content != want {
		t.Fatalf("content = %q, want %q", got.Content, want)
	}
	var contentUpdates []RunnerUpdate
	for _, update := range updates {
		if update.Content != "" {
			contentUpdates = append(contentUpdates, update)
		}
	}
	if len(contentUpdates) != 2 || contentUpdates[0].Content != "我先列一下当前工作目录。" || contentUpdates[1].Content != want {
		t.Fatalf("updates = %#v", updates)
	}
}

func TestCodexExecScanJSONLCommandExecutionVariants(t *testing.T) {
	input := strings.NewReader(`{"type":"item.started","item":{"id":"cmd_1","type":"command_execution","command":["ls","-la"],"status":"running"}}
{"type":"item.updated","item":{"id":"cmd_1","type":"command_execution","stdout":["one\n","two\n"],"status":"running"}}
{"type":"item.completed","item":{"id":"cmd_1","type":"command_execution","cmd":"ls -la","stderr":"warn\n","exit_code":1,"status":"failed"}}
`)
	var tools []RunnerToolEvent
	_, err := (&CodexExecRunner{}).scanJSONL(input, "", func(update RunnerUpdate) {
		if update.Tool != nil {
			tools = append(tools, *update.Tool)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(tools) != 3 {
		t.Fatalf("tools = %#v", tools)
	}
	if tools[0].Command != "ls -la" || tools[1].Output != "one\ntwo\n" || tools[2].Output != "warn\n" || tools[2].ExitCode == nil || *tools[2].ExitCode != 1 {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestCodexExecScanJSONLAgentMessageContentParts(t *testing.T) {
	input := strings.NewReader(`{"type":"item.completed","item":{"type":"agent_message","content":[{"type":"output_text","text":"hello"},{"type":"output_text","text":" world"}]}}
`)
	got, err := (&CodexExecRunner{}).scanJSONL(input, "", func(update RunnerUpdate) {
		if update.Content != "hello world" {
			t.Fatalf("update = %#v", update)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello world" {
		t.Fatalf("content = %q", got.Content)
	}
}

func TestCodexExecScanJSONLErrorEvent(t *testing.T) {
	input := strings.NewReader(`{"type":"thread.started","thread_id":"thr_error"}
{"type":"error","message":"rate limited"}
`)
	got, err := (&CodexExecRunner{}).scanJSONL(input, "", func(update RunnerUpdate) {})
	if err == nil || !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("err = %v", err)
	}
	if got.RemoteThreadID != "thr_error" {
		t.Fatalf("thread id = %q", got.RemoteThreadID)
	}
}

func TestCodexExecScanJSONLIgnoresRecoverableTailErrorAfterContent(t *testing.T) {
	input := strings.NewReader(`{"type":"thread.started","thread_id":"thr_tail"}
{"type":"item.agent_message.delta","delta":"最终可见结果"}
{"type":"error","message":"Reconnecting... 1/5 (stream disconnected before completion: stream closed before response.completed)"}
`)
	got, err := (&CodexExecRunner{}).scanJSONL(input, "", func(update RunnerUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "最终可见结果" {
		t.Fatalf("content = %q", got.Content)
	}
	if got.RemoteThreadID != "thr_tail" {
		t.Fatalf("thread id = %q", got.RemoteThreadID)
	}
}

func TestCodexExecScanJSONLReportsTailErrorWithoutContent(t *testing.T) {
	input := strings.NewReader(`{"type":"thread.started","thread_id":"thr_tail"}
{"type":"error","message":"Reconnecting... 1/5 (stream disconnected before completion: stream closed before response.completed)"}
`)
	_, err := (&CodexExecRunner{}).scanJSONL(input, "", func(update RunnerUpdate) {})
	if err == nil || !strings.Contains(err.Error(), "stream disconnected") {
		t.Fatalf("err = %v", err)
	}
}

func TestCodexExecScanJSONLLongLine(t *testing.T) {
	long := strings.Repeat("x", 5*1024*1024)
	input := strings.NewReader(`{"type":"item.completed","item":{"type":"agent_message","text":"` + long + `"}}
`)
	got, err := (&CodexExecRunner{}).scanJSONL(input, "", func(update RunnerUpdate) {})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != long {
		t.Fatalf("content length = %d, want %d", len(got.Content), len(long))
	}
}

func TestCodexExecScanJSONLLineLimit(t *testing.T) {
	input := strings.NewReader(strings.Repeat("x", 33*1024*1024))
	_, err := (&CodexExecRunner{}).scanJSONL(input, "", func(update RunnerUpdate) {})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
}

func TestEchoRunner(t *testing.T) {
	got, err := EchoRunner{}.Prompt(t.Context(), RunnerRequest{Content: "ping", RemoteThreadID: "thr"}, func(update RunnerUpdate) {
		if update.Delta != "echo: ping" {
			t.Fatalf("update = %#v", update)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "echo: ping" || got.RemoteThreadID != "thr" {
		t.Fatalf("result = %#v", got)
	}
}

func TestCodexExecArgsUseResumeSupportedFlags(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Runner = "codex"
	cfg.Bridge.CWD = "/tmp/work"
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"

	args := NewCodexExecRunner(&cfg).args(RunnerRequest{RemoteThreadID: "thr_123"})
	want := []string{
		"exec",
		"resume",
		"--json",
		"--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox",
		"thr_123",
		"-",
	}
	if !reflect.DeepEqual(args, want) {
		t.Fatalf("resume args = %#v, want %#v", args, want)
	}
	for _, disallowed := range []string{"--color", "--sandbox", "--cd"} {
		if containsArg(args, disallowed) {
			t.Fatalf("resume args include unsupported %s: %#v", disallowed, args)
		}
	}
}

func TestCodexExecResumeArgsKeepConservativePolicy(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Runner = "codex"
	cfg.Bridge.CWD = "/tmp/work"
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"

	args := NewCodexExecRunner(&cfg).args(RunnerRequest{RemoteThreadID: "thr_123"})
	for _, want := range []string{"exec", "resume", "--json", "--skip-git-repo-check", "-c", `sandbox_mode="workspace-write"`, "-c", `approval_policy="untrusted"`, "thr_123", "-"} {
		if !containsArg(args, want) {
			t.Fatalf("resume args missing %q: %#v", want, args)
		}
	}
	for _, disallowed := range []string{"--dangerously-bypass-approvals-and-sandbox", "--sandbox", "--cd"} {
		if containsArg(args, disallowed) {
			t.Fatalf("resume args include %s: %#v", disallowed, args)
		}
	}
}

func TestCodexExecArgsUseBypassForNewSession(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Runner = "codex"
	cfg.Bridge.CWD = "/tmp/work"
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"

	args := NewCodexExecRunner(&cfg).args(RunnerRequest{})
	for _, want := range []string{"exec", "--json", "--color", "never", "--skip-git-repo-check", "--cd", "/tmp/work", "--dangerously-bypass-approvals-and-sandbox", "-"} {
		if !containsArg(args, want) {
			t.Fatalf("new session args missing %q: %#v", want, args)
		}
	}
	if containsArg(args, "--sandbox") {
		t.Fatalf("new session args should use bypass flag instead of --sandbox: %#v", args)
	}
}

func containsArg(args []string, want string) bool {
	for _, arg := range args {
		if arg == want {
			return true
		}
	}
	return false
}
