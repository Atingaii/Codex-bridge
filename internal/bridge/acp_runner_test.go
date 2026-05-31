package bridge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
)

func TestMapACPUpdateAgentMessageChunk(t *testing.T) {
	raw := json.RawMessage(`{"sessionUpdate":"agent_message_chunk","content":{"type":"text","text":"hello "}}`)
	text, tool, kind := mapACPUpdate(raw)
	if kind != "message_delta" {
		t.Fatalf("kind = %q, want message_delta", kind)
	}
	if text != "hello " {
		t.Fatalf("text = %q", text)
	}
	if tool != nil {
		t.Fatalf("tool = %#v, want nil", tool)
	}
}

func TestMapACPUpdateToolCall(t *testing.T) {
	raw := json.RawMessage(`{"sessionUpdate":"tool_call","toolCallId":"tc_1","title":"ls -la","status":"in_progress","content":[{"type":"content","content":{"type":"text","text":"one\ntwo\n"}}]}`)
	text, tool, kind := mapACPUpdate(raw)
	if kind != "tool" {
		t.Fatalf("kind = %q, want tool", kind)
	}
	if text != "" {
		t.Fatalf("text = %q, want empty", text)
	}
	if tool == nil {
		t.Fatal("tool is nil")
	}
	if tool.ID != "tc_1" || tool.Command != "ls -la" || tool.Status != "in_progress" {
		t.Fatalf("tool = %#v", tool)
	}
	if tool.Output != "one\ntwo\n" {
		t.Fatalf("tool output = %q", tool.Output)
	}
}

func TestMapACPUpdateIgnoredKinds(t *testing.T) {
	for _, raw := range []string{
		`{"sessionUpdate":"agent_thought_chunk","content":{"type":"text","text":"thinking"}}`,
		`{"sessionUpdate":"plan","entries":[]}`,
		`{"sessionUpdate":"current_mode_update","currentModeId":"code"}`,
		`{"sessionUpdate":"available_commands_update","availableCommands":[]}`,
	} {
		_, _, kind := mapACPUpdate(json.RawMessage(raw))
		if kind != "" {
			t.Fatalf("kind = %q for %s, want empty", kind, raw)
		}
	}
}

func TestACPSessionIDAndStopReason(t *testing.T) {
	if got := acpSessionID(json.RawMessage(`{"sessionId":"sess_abc"}`)); got != "sess_abc" {
		t.Fatalf("sessionId = %q", got)
	}
	if got := acpStopReason(json.RawMessage(`{"stopReason":"Cancelled"}`)); got != "cancelled" {
		t.Fatalf("stopReason = %q", got)
	}
	if got := acpStopReason(json.RawMessage(`{"stopReason":"end_turn"}`)); got != "end_turn" {
		t.Fatalf("stopReason = %q", got)
	}
}

func TestACPAdvertisesLoadSession(t *testing.T) {
	yes := json.RawMessage(`{"protocolVersion":1,"agentCapabilities":{"loadSession":true}}`)
	no := json.RawMessage(`{"protocolVersion":1,"agentCapabilities":{"loadSession":false}}`)
	if !acpAdvertisesLoadSession(yes) {
		t.Fatal("expected loadSession true")
	}
	if acpAdvertisesLoadSession(no) {
		t.Fatal("expected loadSession false")
	}
}

func TestACPPermissionOptionsAndOutcome(t *testing.T) {
	raw := json.RawMessage(`{"options":[{"optionId":"allow","kind":"allow_once"},{"optionId":"deny","kind":"reject_once"}],"toolCall":{"title":"rm -rf /tmp/x"}}`)
	all, allowID, rejectID := acpPermissionOptions(raw)
	if len(all) != 2 || allowID != "allow" || rejectID != "deny" {
		t.Fatalf("options=%v allow=%q reject=%q", all, allowID, rejectID)
	}
	if title := acpToolCallTitle(raw); title != "rm -rf /tmp/x" {
		t.Fatalf("title = %q", title)
	}
	selected := acpPermissionOutcome("selected", "allow")
	outcome, _ := selected["outcome"].(map[string]any)
	if outcome["outcome"] != "selected" || outcome["optionId"] != "allow" {
		t.Fatalf("selected outcome = %#v", selected)
	}
	cancelled := acpPermissionOutcome("cancelled", "")
	co, _ := cancelled["outcome"].(map[string]any)
	if co["outcome"] != "cancelled" {
		t.Fatalf("cancelled outcome = %#v", cancelled)
	}
	if _, hasOption := co["optionId"]; hasOption {
		t.Fatalf("cancelled outcome should not carry optionId: %#v", cancelled)
	}
}

func TestNativeResumeCommandPerCLI(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Runner = "acp"
	cfg.Bridge.ACP.CLI = "claude"
	r := NewACPRunner(&cfg)
	if cmd := r.nativeResumeCommand("uuid-1"); cmd != "claude --resume uuid-1" {
		t.Fatalf("claude command = %q", cmd)
	}
	cfg.Bridge.ACP.CLI = "codex"
	r2 := NewACPRunner(&cfg)
	if cmd := r2.nativeResumeCommand("thr_9"); cmd != "codex resume thr_9" {
		t.Fatalf("codex command = %q", cmd)
	}
	if cmd := r2.nativeResumeCommand(""); cmd != "" {
		t.Fatalf("empty id should yield empty command, got %q", cmd)
	}
}

func TestResolveNativeResumeClaudeUsesACPID(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Runner = "acp"
	cfg.Bridge.ACP.CLI = "claude"
	cfg.Bridge.ACP.PreferNativeResume = true
	r := NewACPRunner(&cfg)
	// Claude reuses the ACP id verbatim as its native .jsonl id.
	if got := r.resolveNativeResumeID("123e4567-e89b-12d3-a456-426614174000", "/tmp/work"); got != "123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("claude native id = %q", got)
	}
}

func TestResolveNativeResumeDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Runner = "acp"
	cfg.Bridge.ACP.CLI = "claude"
	cfg.Bridge.ACP.PreferNativeResume = false
	r := NewACPRunner(&cfg)
	if got := r.resolveNativeResumeID("uuid", "/tmp/work"); got != "" {
		t.Fatalf("disabled prefer_native_resume must yield empty id, got %q", got)
	}
}

func TestResolveNativeResumeCodexFromACPID(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Runner = "acp"
	cfg.Bridge.ACP.CLI = "codex"
	cfg.Bridge.ACP.PreferNativeResume = true
	r := NewACPRunner(&cfg)
	if got := r.resolveNativeResumeID("thr_abc123", "/tmp/missing-cwd-xyz"); got != "thr_abc123" {
		t.Fatalf("codex thr id = %q", got)
	}
	uuid := "123e4567-e89b-12d3-a456-426614174000"
	if got := r.resolveNativeResumeID(uuid, "/tmp/missing-cwd-xyz"); got != uuid {
		t.Fatalf("codex uuid id = %q", got)
	}
}

func TestLooksLikeUUID(t *testing.T) {
	if !looksLikeUUID("123e4567-e89b-12d3-a456-426614174000") {
		t.Fatal("valid uuid rejected")
	}
	for _, bad := range []string{"", "thr_1", "not-a-uuid", "123e4567e89b12d3a456426614174000"} {
		if looksLikeUUID(bad) {
			t.Fatalf("invalid uuid accepted: %q", bad)
		}
	}
}

func TestCodexRolloutID(t *testing.T) {
	uuid := "123e4567-e89b-12d3-a456-426614174000"
	if got := codexRolloutID("rollout-2026-05-31T12-00-00-" + uuid + ".jsonl"); got != uuid {
		t.Fatalf("rollout id = %q", got)
	}
	if got := codexRolloutID(uuid + ".jsonl"); got != uuid {
		t.Fatalf("bare uuid rollout id = %q", got)
	}
	if got := codexRolloutID("not-a-session.jsonl"); got != "" {
		t.Fatalf("non-uuid rollout should yield empty, got %q", got)
	}
}

func TestScanCodexSessionIDPicksMostRecent(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	sessions := filepath.Join(tmp, ".codex", "sessions", "2026", "05", "31")
	if err := os.MkdirAll(sessions, 0o700); err != nil {
		t.Fatal(err)
	}
	older := "11111111-1111-1111-1111-111111111111"
	newer := "22222222-2222-2222-2222-222222222222"
	if err := os.WriteFile(filepath.Join(sessions, "rollout-2026-05-31T10-00-00-"+older+".jsonl"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	newerPath := filepath.Join(sessions, "rollout-2026-05-31T11-00-00-"+newer+".jsonl")
	if err := os.WriteFile(newerPath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Force the newer file to have a later mod time.
	if err := os.Chtimes(filepath.Join(sessions, "rollout-2026-05-31T10-00-00-"+older+".jsonl"), zeroTime(), zeroTime()); err != nil {
		t.Fatal(err)
	}
	if got := scanCodexSessionID("/tmp/anything"); got != newer {
		t.Fatalf("scan picked %q, want %q", got, newer)
	}
}

func TestClaudeProjectDirEncoding(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	dir := claudeProjectDir("/home/me/work")
	want := filepath.Join(tmp, ".claude", "projects", "-home-me-work")
	if dir != want {
		t.Fatalf("project dir = %q, want %q", dir, want)
	}
}

func TestACPAdapterCommandSelection(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Runner = "acp"
	cfg.Bridge.ACP.CLI = "claude"
	r := NewACPRunner(&cfg)
	cmd, args, err := r.adapterCommand()
	if err != nil {
		t.Fatal(err)
	}
	if cmd != "npx" || len(args) != 2 || args[0] != "-y" {
		t.Fatalf("claude adapter cmd=%q args=%v", cmd, args)
	}
	cfg.Bridge.ACP.CLI = "codex"
	r2 := NewACPRunner(&cfg)
	cmd2, _, err := r2.adapterCommand()
	if err != nil || cmd2 != "codex-acp" {
		t.Fatalf("codex adapter cmd=%q err=%v", cmd2, err)
	}
	// Missing command should error.
	cfg.Bridge.ACP.CodexCommand = ""
	r3 := NewACPRunner(&cfg)
	if _, _, err := r3.adapterCommand(); err == nil {
		t.Fatal("expected error for empty codex_command")
	}
}

func TestACPRunnerImplementsSessionRunner(t *testing.T) {
	cfg := config.Default()
	var runner Runner = NewACPRunner(&cfg)
	if _, ok := runner.(SessionRunner); !ok {
		t.Fatal("ACPRunner must implement SessionRunner")
	}
	// Existing runners must NOT implement SessionRunner so the fallback path is used.
	if _, ok := Runner(EchoRunner{}).(SessionRunner); ok {
		t.Fatal("EchoRunner must not implement SessionRunner")
	}
	if _, ok := Runner(NewCodexExecRunner(&cfg)).(SessionRunner); ok {
		t.Fatal("CodexExecRunner must not implement SessionRunner")
	}
	if _, ok := Runner(NewCodexAppServerRunner(&cfg)).(SessionRunner); ok {
		t.Fatal("CodexAppServerRunner must not implement SessionRunner")
	}
}

func zeroTime() time.Time {
	return time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
}
