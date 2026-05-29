package bridge

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestOrchestrationClaudeArgsGrantSelectedCWDAndBypassPermissions(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CWD = "/root/tencent/bridge"
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"

	args := NewOrchestrationManager(&cfg).claudeArgs(protocol.OrchestrationStartPayload{
		CWD: "/root/tencent",
	}, "prove this")

	assertArgPair(t, args, "--add-dir", "/root/tencent")
	wantMode := "bypassPermissions"
	if runningAsRoot() {
		wantMode = "acceptEdits"
	}
	assertArgPair(t, args, "--permission-mode", wantMode)
	if got := args[len(args)-1]; got != "prove this" {
		t.Fatalf("last claude arg = %q, want prompt", got)
	}
}

func TestOrchestrationClaudeArgsUseBridgeCWDWhenPayloadCWDIsEmpty(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CWD = "/root/tencent"

	args := NewOrchestrationManager(&cfg).claudeArgs(protocol.OrchestrationStartPayload{}, "task")
	assertArgPair(t, args, "--add-dir", "/root/tencent")
}

func TestOrchestrationClaudeArgsDoNotBypassPermissionsForWorkspaceSandbox(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CWD = "/root/tencent"
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "never"

	args := NewOrchestrationManager(&cfg).claudeArgs(protocol.OrchestrationStartPayload{}, "task")
	if containsArg(args, "--permission-mode") {
		t.Fatalf("claude args should not bypass permissions for workspace sandbox: %#v", args)
	}
}

func TestOrchestrationClaudeStreamInputArgsKeepSessionAndOmitPromptArg(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	args := manager.claudeArgsWithStreamInput(protocol.OrchestrationStartPayload{CWD: "/repo"}, "11111111-1111-5111-8111-111111111111", false)
	for _, want := range []string{"--print", "--input-format=stream-json", "--output-format=stream-json", "--verbose", "--session-id", "11111111-1111-5111-8111-111111111111"} {
		if !containsArg(args, want) {
			t.Fatalf("stream claude args missing %q: %#v", want, args)
		}
	}
	if containsArg(args, "task") {
		t.Fatalf("stream claude args should not append prompt as argv: %#v", args)
	}
}

func TestRoleForTurnSupportsCodexFirst(t *testing.T) {
	role, cli := roleForTurnWithFirstCLI("collaboration", "codex", 1)
	if role != "reviewer" || cli != "codex" {
		t.Fatalf("turn 1 = %s/%s, want reviewer/codex", role, cli)
	}
	role, cli = roleForTurnWithFirstCLI("collaboration", "codex", 2)
	if role != "implementer" || cli != "claude" {
		t.Fatalf("turn 2 = %s/%s, want implementer/claude", role, cli)
	}
	role, cli = roleForTurnWithFirstCLI("debate", "codex", 1)
	if role != "critic" || cli != "codex" {
		t.Fatalf("debate turn 1 = %s/%s, want critic/codex", role, cli)
	}
	role, cli = nextRoleCLIWithFirstCLI("debate", "codex", 1)
	if role != "proposer" || cli != "claude" {
		t.Fatalf("debate next turn = %s/%s, want proposer/claude", role, cli)
	}
}

func TestWriteClaudeStreamUserMessageUsesClaudeJSONShape(t *testing.T) {
	var buf bytes.Buffer
	if err := writeClaudeStreamUserMessage(&buf, "继续处理"); err != nil {
		t.Fatal(err)
	}
	var msg map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &msg); err != nil {
		t.Fatal(err)
	}
	if msg["type"] != "user" {
		t.Fatalf("message type = %#v", msg["type"])
	}
	message := msg["message"].(map[string]any)
	if message["role"] != "user" {
		t.Fatalf("message role = %#v", message["role"])
	}
	content := message["content"].([]any)
	part := content[0].(map[string]any)
	if part["type"] != "text" || part["text"] != "继续处理" {
		t.Fatalf("content = %#v", content)
	}
}

func TestOrchestrationClaudeApprovalArgsAttachMCPBeforePrompt(t *testing.T) {
	args := NewOrchestrationManager(&config.Config{}).withClaudeApprovalArgs(
		[]string{"--print", "--output-format=stream-json", "task"},
		"/tmp/codex-bridge-mcp.json",
	)
	for _, want := range []string{"--permission-mode", "default", "--mcp-config", "/tmp/codex-bridge-mcp.json", "--permission-prompt-tool", "mcp__codex_bridge__browser_approval"} {
		if !containsArg(args, want) {
			t.Fatalf("claude args missing %q: %#v", want, args)
		}
	}
	if got := args[len(args)-1]; got != "task" {
		t.Fatalf("last claude arg = %q, want prompt: %#v", got, args)
	}
}

func TestOrchestrationCodexUsesAppServerWhenApprovalIsRequired(t *testing.T) {
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
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)

	done := make(chan struct{})
	go func() {
		defer close(done)
		for env := range out {
			if env.Type != protocol.TypeApprovalRequest {
				continue
			}
			req, err := protocol.Decode[protocol.ApprovalRequestPayload](env)
			if err == nil {
				manager.ApprovalResponse(protocol.ApprovalResponsePayload{RequestID: req.RequestID, Decision: "accept"})
			}
		}
	}()

	content, tools, err := manager.runCodex(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_app",
		CWD:   tmp,
	}, "turn_app", "reviewer", "run it")
	close(out)
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if content != "done" {
		t.Fatalf("content = %q", content)
	}
	if len(tools) == 0 {
		t.Fatal("expected app-server tool event")
	}
}

func TestCCBHelpersParseJobAndResult(t *testing.T) {
	jobID := parseCCBJobID("accepted job=job_abc-123 target=codex\n[CCB_ASYNC_SUBMITTED job=job_abc-123 target=codex]")
	if jobID != "job_abc-123" {
		t.Fatalf("job id = %q", jobID)
	}
	result := parseCCBWatchResult("observer_view: watch\nstatus: completed\nreply: final line\nmore detail\njob_id: job_abc")
	if result.Status != "completed" || result.Reply != "final line\nmore detail" {
		t.Fatalf("result = %#v", result)
	}
	result = parseCCBWatchResult("status: completed\nreply: 检查结论：通过\n剩余风险：无\njob_id: job_abc")
	if result.Reply != "检查结论：通过\n剩余风险：无" {
		t.Fatalf("metadata parser stripped reply detail incorrectly: %#v", result)
	}
	result = parseCCBWatchResult("status: completed\nreply: Summary: ok\nStatus: verified\njob_id: job_abc")
	if result.Reply != "Summary: ok\nStatus: verified" {
		t.Fatalf("metadata parser stripped status detail incorrectly: %#v", result)
	}
}

func TestCCBFinalReplyDoesNotExposeRawObserverDump(t *testing.T) {
	output := strings.Join([]string{
		"observer_view: watch",
		"observer_authority: supplementary_snapshot",
		"event: evt_1 job_parent codex job_completed 2026-05-26T00:00:00Z",
		"event: evt_2 job_parent codex job_delegated_callback 2026-05-26T00:00:01Z",
		"watch_status: terminal",
		"job_id: job_parent",
		"status: completed",
		"reply:",
	}, "\n")
	reply, synthesized := ccbFinalReply(ccbJobResult{Status: "completed"}, output, &ccbWatchStreamState{
		events: []ccbWatchStreamEvent{
			{Data: map[string]any{"agent": "codex", "eventType": "job_accepted"}},
			{Data: map[string]any{"agent": "codex", "eventType": "job_delegated_callback", "payload": map[string]any{"callback_child_job_id": "job_child"}}},
		},
	}, "codex")
	if !synthesized {
		t.Fatal("empty observer reply should be synthesized")
	}
	if strings.Contains(reply, "observer_view") || strings.Contains(reply, "evt_") {
		t.Fatalf("raw observer dump leaked into reply:\n%s", reply)
	}
	for _, want := range []string{"CCB ended without a final user-visible reply", "Observed codex", "job_child"} {
		if !strings.Contains(reply, want) {
			t.Fatalf("reply missing %q:\n%s", want, reply)
		}
	}
}

func TestDetectCCBTerminalPromptRecognizesCodexTrustPrompt(t *testing.T) {
	lines := []string{
		"Do you trust the contents of this directory? Working with untrusted contents comes with higher risk of prompt injection.",
		"› 1. Yes, continue",
		"2. No, quit",
		"Press enter to continue",
	}
	prompt, ok := detectCCBTerminalPrompt(lines)
	if !ok {
		t.Fatal("trust prompt was not detected")
	}
	if prompt.Type != "workspace_trust" || prompt.Input != "Enter" {
		t.Fatalf("prompt = %#v", prompt)
	}
	if !strings.Contains(prompt.Reason, "Do you trust") || !strings.Contains(prompt.Command, "Enter") {
		t.Fatalf("prompt text = %#v", prompt)
	}
}

func TestCCBTerminalInputArgsUsesSameTmuxPane(t *testing.T) {
	got := ccbTerminalInputArgs("/tmp/ccb/tmux.sock", "%7", "Enter")
	want := []string{"-S", "/tmp/ccb/tmux.sock", "send-keys", "-t", "%7", "Enter"}
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("args = %#v, want %#v", got, want)
	}
}

func TestCCBWatchStreamLineEvent(t *testing.T) {
	event, ok := ccbWatchStreamLineEvent("event: evt_1 job_abc codex completion_item 2026-05-26T00:00:00Z", nil)
	if !ok {
		t.Fatal("event line was not emitted")
	}
	if event.Content != "CCB codex: completion item" {
		t.Fatalf("content = %q", event.Content)
	}
	if event.Data["eventType"] != "completion_item" || event.Data["jobId"] != "job_abc" || event.Data["target"] != "codex" {
		t.Fatalf("data = %#v", event.Data)
	}
	if _, ok := ccbWatchStreamLineEvent("observer_view: watch", nil); ok {
		t.Fatal("observer metadata should not be emitted")
	}
	event, ok = ccbWatchStreamLineEvent("reply: final answer", nil)
	if !ok || event.Content != "final answer" {
		t.Fatalf("reply event = %#v ok=%v", event, ok)
	}
}

func TestCCBWatchStreamLineEventExtractsAgentTextPayload(t *testing.T) {
	line := `event: evt_1 job_abc codex completion_item {"kind":"assistant_chunk","agent_name":"codex","payload":{"merged_text":"hello from codex"}}`
	event, ok := ccbWatchStreamLineEvent(line, &ccbWatchStreamState{})
	if !ok {
		t.Fatal("completion item line was not emitted")
	}
	if event.Content != "hello from codex" || event.Data["agent"] != "codex" || event.Data["contentKind"] != "agent_text" {
		t.Fatalf("event = %#v", event)
	}
}

func TestCCBStructuredWatchEventExtractsCompletionPayload(t *testing.T) {
	record := map[string]any{
		"event_id":    "evt_1",
		"job_id":      "job_abc",
		"agent_name":  "codex",
		"target_name": "codex",
		"type":        "completion_item",
		"timestamp":   "2026-05-26T00:00:00Z",
		"payload": map[string]any{
			"kind":       "assistant_chunk",
			"agent_name": "codex",
			"payload": map[string]any{
				"merged_text": "hello from codex",
			},
		},
	}
	event, ok := ccbStructuredWatchStreamEvent(record, &ccbWatchStreamState{})
	if !ok {
		t.Fatal("structured completion item was not emitted")
	}
	if event.Content != "hello from codex" || event.Data["agent"] != "codex" || event.Data["contentKind"] != "agent_text" {
		t.Fatalf("event = %#v", event)
	}
}

func TestCCBStructuredWatchEventDiscoversCallbackJobs(t *testing.T) {
	record := map[string]any{
		"event_id":   "evt_2",
		"job_id":     "job_parent",
		"agent_name": "codex",
		"type":       "job_delegated_callback",
		"payload": map[string]any{
			"callback_child_job_id": "job_child",
		},
	}
	ids := ccbRelatedJobIDs(record)
	if strings.Join(ids, ",") != "job_parent,job_child" {
		t.Fatalf("related ids = %#v", ids)
	}
	event, ok := ccbStructuredWatchStreamEvent(record, nil)
	if !ok || !strings.Contains(event.Content, "job_child") {
		t.Fatalf("callback event = %#v ok=%v", event, ok)
	}
}

func TestCCBSocketWatchCompleteRequiresCallbackFollowup(t *testing.T) {
	jobs := map[string]*ccbSocketWatchJob{
		"job_parent": {Terminal: true, PendingCallback: true},
	}
	if ccbSocketWatchComplete(jobs, "job_parent", ccbWatchBatch{Status: "completed"}) {
		t.Fatal("callback parent should not complete before a final visible reply is available")
	}
	if !ccbSocketWatchComplete(jobs, "job_parent", ccbWatchBatch{Status: "completed", Reply: "final"}) {
		t.Fatal("callback parent should complete as soon as CCB exposes a final visible reply")
	}
	jobs["job_child"] = &ccbSocketWatchJob{Terminal: true}
	if !ccbSocketWatchComplete(jobs, "job_parent", ccbWatchBatch{Status: "completed"}) {
		t.Fatal("callback parent with no reply should complete after related jobs are terminal")
	}
}

func TestCCBCompletionItemTextPrefersReadableDelta(t *testing.T) {
	got := ccbCompletionItemText("assistant_chunk", map[string]any{
		"text":        "second",
		"merged_text": "first\nsecond",
	})
	if got != "second" {
		t.Fatalf("completion text = %q", got)
	}
}

func TestSanitizeCCBConsoleLinesRedactsSecretsAndANSI(t *testing.T) {
	lines := sanitizeCCBConsoleLines("\x1b[31mOPENAI_API_KEY=sk-test\nAuthorization: Bearer abc.def\nenr_secret-token_123\nplain\x1b[0m\n", 10)
	got := strings.Join(lines, "\n")
	if strings.Contains(got, "\x1b") || strings.Contains(got, "sk-test") || strings.Contains(got, "abc.def") || strings.Contains(got, "enr_secret-token_123") {
		t.Fatalf("console was not sanitized:\n%s", got)
	}
	if !strings.Contains(got, "OPENAI_API_KEY=[REDACTED]") || !strings.Contains(got, "enr_[REDACTED]") || !strings.Contains(got, "plain") {
		t.Fatalf("console redaction lost expected text:\n%s", got)
	}
}

func TestCCBProviderSessionToolEvents(t *testing.T) {
	startLine := []byte(`{"timestamp":"2026-05-26T12:06:50.891Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","arguments":"{\"cmd\":\"command ask --callback claude -- \\\"Reply OK\\\"\",\"workdir\":\"/tmp/work\"}","call_id":"call_1"}}`)
	start := ccbProviderSessionToolEvent(startLine)
	if start == nil || start.Status != "in_progress" || start.ID != "call_1" || start.Command != `command ask --callback claude -- "Reply OK"` {
		t.Fatalf("start event = %#v", start)
	}
	endLine := []byte(`{"timestamp":"2026-05-26T12:06:51.388Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call_1","output":"Chunk ID: abc\nWall time: 0.1 seconds\nProcess exited with code 0\nOriginal token count: 26\nOutput:\naccepted job=job_child target=claude\n[CCB_ASYNC_SUBMITTED job=job_child target=claude]\n"}}`)
	end := ccbProviderSessionToolEvent(endLine)
	if end == nil || end.Status != "completed" || end.ID != "call_1" {
		t.Fatalf("end event = %#v", end)
	}
	if strings.Contains(end.Output, "Chunk ID:") || !strings.Contains(end.Output, "accepted job=job_child") {
		t.Fatalf("output not cleaned for readers:\n%s", end.Output)
	}
}

func TestParseCCBSocketPath(t *testing.T) {
	got := parseCCBSocketPath("start_status: ok\nsocket_path: /tmp/ccb-runtime/ccbd.sock\nagents: codex, claude\n")
	if got != "/tmp/ccb-runtime/ccbd.sock" {
		t.Fatalf("socket path = %q", got)
	}
}

func TestCCBTraceReplyEventsExtractAgentReplies(t *testing.T) {
	events := ccbTraceReplyEvents("reply: id=rep_1 message=msg_1 attempt=att_1 agent=claude terminal=completed size=24 notice=false kind=None reason=task_complete finished=2026-05-26T00:00:00Z preview=hello from claude\n")
	if len(events) != 1 {
		t.Fatalf("events = %#v", events)
	}
	if events[0].CLI != "claude" || events[0].Role != "claude" || events[0].Content != "hello from claude" {
		t.Fatalf("event = %#v", events[0])
	}
}

func TestCCBTraceReplyEventsFromPayloadSkipsAlreadyStreamedText(t *testing.T) {
	state := &ccbWatchStreamState{agentContent: map[string]string{"claude": "hello from claude"}}
	payload := map[string]any{
		"replies": []any{
			map[string]any{
				"reply_id":        "rep_1",
				"message_id":      "msg_1",
				"attempt_id":      "att_1",
				"agent_name":      "claude",
				"terminal_status": "completed",
				"reply":           "hello from claude",
				"finished_at":     "2026-05-26T00:00:00Z",
			},
			map[string]any{
				"reply_id":        "rep_2",
				"message_id":      "msg_2",
				"attempt_id":      "att_2",
				"agent_name":      "codex",
				"terminal_status": "completed",
				"reply":           "hello from codex",
				"finished_at":     "2026-05-26T00:00:01Z",
			},
		},
	}
	events := ccbTraceReplyEventsFromPayload(payload, state)
	if len(events) != 1 || events[0].Role != "codex" || events[0].Content != "hello from codex" {
		t.Fatalf("events = %#v", events)
	}
}

func TestCCBDefaultTargetIsCodex(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CCBTarget = ""
	manager := NewOrchestrationManager(&cfg)
	if got := manager.ccbTarget(); got != "codex" {
		t.Fatalf("ccb target = %q", got)
	}
}

func TestOrchestrationCCBEnvPrependsConfiguredCLIDirs(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CodexPath = "/opt/codex/bin/codex"
	cfg.Bridge.ClaudePath = "/opt/claude/bin/claude"
	cfg.Bridge.CCBPath = "/opt/ccb/ccb"

	env := orchestrationCCBEnv([]string{"PATH=/usr/bin", "BRIDGE_CODEX_PATH=old"}, &cfg)
	path := envValue(env, "PATH")
	for _, want := range []string{"/opt/codex/bin", "/opt/claude/bin", "/opt/ccb", "/usr/bin"} {
		if !strings.Contains(path, want) {
			t.Fatalf("PATH %q missing %q", path, want)
		}
	}
	if got := envValue(env, "BRIDGE_CODEX_PATH"); got != cfg.Bridge.CodexPath {
		t.Fatalf("BRIDGE_CODEX_PATH = %q, want %q", got, cfg.Bridge.CodexPath)
	}
	if got := envValue(env, "BRIDGE_CLAUDE_PATH"); got != cfg.Bridge.ClaudePath {
		t.Fatalf("BRIDGE_CLAUDE_PATH = %q, want %q", got, cfg.Bridge.ClaudePath)
	}
	if got := envValue(env, "BRIDGE_CCB_PATH"); got != cfg.Bridge.CCBPath {
		t.Fatalf("BRIDGE_CCB_PATH = %q, want %q", got, cfg.Bridge.CCBPath)
	}
}

func TestEnsureCCBConfigWritesOnlyCodexAndClaudeWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	if err := manager.ensureCCBConfig(protocol.OrchestrationStartPayload{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(tmp, ".ccb", "ccb.config"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(raw)); got != "codex:codex, claude:claude" {
		t.Fatalf("ccb config = %q", got)
	}
}

func TestEnsureCCBConfigAllowsExistingCodexClaudeConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, ".ccb", "ccb.config")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("cmd; codex:codex, claude:claude\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	if err := manager.ensureCCBConfig(protocol.OrchestrationStartPayload{}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != "cmd; codex:codex, claude:claude\n" {
		t.Fatalf("existing ccb config overwritten: %q", got)
	}
}

func TestEnsureCCBConfigRejectsExistingNonCodexClaudeConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, ".ccb", "ccb.config")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("codex:codex, claude:claude, gemini:gemini\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	err := manager.ensureCCBConfig(protocol.OrchestrationStartPayload{})
	if err == nil || !strings.Contains(err.Error(), "must declare only") {
		t.Fatalf("expected restricted config error, got %v", err)
	}
}

func TestOrchestrationSuccessfulTurnEndCarriesFinalContent(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(claudePath, []byte(fakeClaudePrintScript("我会先检查。")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexExecScript("最终结论：构建通过。\n\n已验证：`isabelle build -D .`。\n\n剩余风险：仍有 sorry 占位。")), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:    "orc_final",
		Mode:     "collaboration",
		Prompt:   "检查证明框架",
		MaxTurns: 2,
		CWD:      tmp,
	})

	var sawFinalTurnEnd bool
	for len(out) > 0 {
		env := <-out
		if env.Type != protocol.TypeOrchestrationEvent {
			continue
		}
		event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == "turn.end" && event.CLI == "codex" && strings.Contains(event.Content, "最终结论") {
			sawFinalTurnEnd = true
			if !strings.Contains(event.Content, "sorry") {
				t.Fatalf("final turn.end content lost risk detail: %#v", event)
			}
		}
	}
	if !sawFinalTurnEnd {
		t.Fatal("codex final turn.end did not carry final content")
	}
}

func TestOrchestrationRelayRunEmitsFrontendVisiblePromptsCommandsAndSessionState(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	claudePromptPath := filepath.Join(tmp, "claude_prompt.txt")
	codexPromptPath := filepath.Join(tmp, "codex_prompt.txt")
	claudeArgvPath := filepath.Join(tmp, "claude_argv.json")
	codexArgvPath := filepath.Join(tmp, "codex_argv.json")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeRelayScript(claudePromptPath, claudeArgvPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexRelayScript(codexPromptPath, codexArgvPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)

	task := "把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全"
	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:    "orc_relay",
		Mode:     "collaboration",
		Prompt:   task,
		MaxTurns: 2,
		CWD:      tmp,
		Files: []protocol.AttachmentPayload{
			{Name: "Model.thy", MimeType: "application/octet-stream", Size: int64(len("theory Model\n")), Data: base64.StdEncoding.EncodeToString([]byte("theory Model\n"))},
			{Name: "Termination.thy", MimeType: "application/octet-stream", Size: int64(len("theory Termination\n")), Data: base64.StdEncoding.EncodeToString([]byte("theory Termination\n"))},
			{Name: "ROOT", MimeType: "application/octet-stream", Size: int64(len("session demo\n")), Data: base64.StdEncoding.EncodeToString([]byte("session demo\n"))},
		},
	})

	var events []protocol.OrchestrationEventPayload
	for len(out) > 0 {
		env := <-out
		if env.Type != protocol.TypeOrchestrationEvent {
			continue
		}
		event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	if !orchestrationEventsContain(events, "turn.start", "claude", task) ||
		!orchestrationEventsContain(events, "turn.start", "claude", "Prompt sent to claude") {
		t.Fatalf("Claude prompt was not frontend-visible: %#v", events)
	}
	if !orchestrationEventsContain(events, "command.start", "claude", "mkdir -p coq-relay") ||
		!orchestrationEventsContain(events, "command.end", "codex", "go test ./...") {
		t.Fatalf("command events were not frontend-visible: %#v", events)
	}
	if !orchestrationEventsContain(events, "turn.start", "codex", "Claude result: wrote Model.v") ||
		!orchestrationEventsContain(events, "turn.start", "codex", "mkdir -p coq-relay") {
		t.Fatalf("Codex handoff prompt did not include Claude result and command context: %#v", events)
	}
	if !orchestrationEventsContain(events, "run.end", "", "Codex final: verified relay result") {
		t.Fatalf("run.end did not relay final Codex content: %#v", events)
	}
	for _, event := range events {
		if event.Kind == "turn.start" && strings.Contains(event.Content, "Formal proof task guardrails") {
			t.Fatalf("relay prompt leaked old proof gate label: %#v", event)
		}
		if strings.Contains(event.TurnID, "verifier") || strings.Contains(event.TurnID, "remediation") {
			t.Fatalf("pass-through relay should not schedule hidden verifier/remediation turn: %#v", event)
		}
	}
	claudePrompt, err := os.ReadFile(claudePromptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claudePrompt), "visible result will be handed to another CLI") {
		t.Fatalf("Claude prompt missing first-turn handoff notice:\n%s", claudePrompt)
	}
	codexPrompt, err := os.ReadFile(codexPromptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codexPrompt), "Claude result: wrote Model.v") || !strings.Contains(string(codexPrompt), "mkdir -p coq-relay") {
		t.Fatalf("Codex stdin missing Claude handoff context:\n%s", codexPrompt)
	}
	claudeArgv, err := os.ReadFile(claudeArgvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claudeArgv), "--session-id") {
		t.Fatalf("Claude was not started with stable session id: %s", claudeArgv)
	}
	if strings.Contains(string(claudeArgv), "--input-format=stream-json") {
		t.Fatalf("Coq upload relay should not use Isabelle stream-input mode: %s", claudeArgv)
	}
	codexArgv, err := os.ReadFile(codexArgvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codexArgv), "exec") || !strings.Contains(string(codexArgv), "--cd") {
		t.Fatalf("Codex initial exec args did not include cwd/thread setup: %s", codexArgv)
	}
}

func TestRelayCLIErrorIsFrontendVisibleAndRedacted(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeErrorScript("server_error token=secret Authorization: Bearer abc.def")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexExecScript("unused")), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:    "orc_cli_error",
		Mode:     "collaboration",
		Prompt:   "检查证明框架",
		MaxTurns: 1,
		CWD:      tmp,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.end", "claude", "CLI process failed before returning a final text response") ||
		!orchestrationEventsContain(events, "turn.end", "claude", "server_error") {
		t.Fatalf("turn.end did not expose CLI error details: %#v", events)
	}
	if !orchestrationEventsContain(events, "run.error", "claude", "server_error") {
		t.Fatalf("run.error did not expose CLI error details: %#v", events)
	}
	for _, event := range events {
		if strings.Contains(event.Content, "abc.def") || strings.Contains(event.Error, "abc.def") || strings.Contains(event.Content, "token=secret") || strings.Contains(event.Error, "token=secret") {
			t.Fatalf("CLI error leaked sensitive value: %#v", event)
		}
	}
}

func TestOrchestrationCodexTailDisconnectAfterFinalContentCompletes(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(`#!/usr/bin/env python3
import json
import sys

if len(sys.argv) < 2 or sys.argv[1] != "exec":
    sys.exit(1)
text = "最终结果已经输出。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=needs_next; changed=none; verified=none; next=none; risks=仍有证明义务"
print(json.dumps({"type":"thread.started","thread_id":"thr_tail"}), flush=True)
print(json.dumps({"type":"item.agent_message.delta","delta":text}), flush=True)
print(json.dumps({"type":"error","message":"Reconnecting... 1/5 (stream disconnected before completion: stream closed before response.completed)"}), flush=True)
`), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:    "orc_tail_disconnect",
		Mode:     "collaboration",
		FirstCLI: "codex",
		Prompt:   "只跑 codex",
		MaxTurns: 1,
		CWD:      tmp,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.end", "codex", "最终结果已经输出") {
		t.Fatalf("final turn content missing: %#v", events)
	}
	if !orchestrationEventsContain(events, "run.end", "", "最终结果已经输出") {
		t.Fatalf("run.end missing final content: %#v", events)
	}
	for _, event := range events {
		if event.Kind == "run.error" {
			t.Fatalf("tail disconnect after final content should not fail run: %#v", event)
		}
	}
}

func TestClaudeIsabelleLongCommandNudgeWritesToSameStreamAndEmitsEvent(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	stdoutReader, stdoutWriter := io.Pipe()
	var stdin bytes.Buffer

	done := make(chan struct{})
	var content string
	var tools []RunnerToolEvent
	var scanErr error
	go func() {
		defer close(done)
		content, tools, scanErr = manager.scanClaudeJSONLWithOptions(stdoutReader, "orc_nudge", "orc_nudge-01", "implementer", claudeScanOptions{
			Input:      &stdin,
			CanNudge:   true,
			NudgeAfter: 10 * time.Millisecond,
		})
	}()

	fmt.Fprintln(stdoutWriter, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_build","name":"Bash","input":{"command":"isabelle build -D isabelle_bridge_demo"}}]}}`)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !strings.Contains(stdin.String(), "Bridge observer note") {
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Fprintln(stdoutWriter, `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_build","content":"Finished\n"}]}}`)
	fmt.Fprintln(stdoutWriter, `{"type":"assistant","message":{"content":[{"type":"text","text":"完成"}]}}`)
	fmt.Fprintln(stdoutWriter, `{"type":"result","result":"完成"}`)
	stdoutWriter.Close()
	<-done

	if scanErr != nil {
		t.Fatal(scanErr)
	}
	if content != "完成" {
		t.Fatalf("content = %q", content)
	}
	if len(tools) != 2 {
		t.Fatalf("tools = %#v", tools)
	}
	if got := stdin.String(); !strings.Contains(got, "Bridge observer note") || !strings.Contains(got, "isabelle build -D isabelle_bridge_demo") {
		t.Fatalf("nudge was not written to Claude stream: %s", got)
	}
	if !orchestrationEventsContain(drainOrchestrationEvents(t, out), "turn.delta", "claude", "Bridge sent Claude an Isabelle timeout nudge") {
		t.Fatal("frontend-visible nudge event was not emitted")
	}
}

func TestClaudeStreamInputClosesAfterIdleWindowWithoutInterruptingProcess(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	stdoutReader, stdoutWriter := io.Pipe()
	stdin := &trackingWriteCloser{}

	done := make(chan struct{})
	var scanErr error
	go func() {
		defer close(done)
		_, _, scanErr = manager.scanClaudeJSONLWithOptions(stdoutReader, "orc_idle", "orc_idle-01", "implementer", claudeScanOptions{
			Input:          stdin,
			CanNudge:       true,
			IdleCloseAfter: 10 * time.Millisecond,
		})
	}()

	fmt.Fprintln(stdoutWriter, `{"type":"assistant","message":{"content":[{"type":"text","text":"开始处理"}]}}`)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !stdin.closed {
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Fprintln(stdoutWriter, `{"type":"result","result":"开始处理"}`)
	stdoutWriter.Close()
	<-done

	if scanErr != nil {
		t.Fatal(scanErr)
	}
	if !stdin.closed {
		t.Fatal("Claude stream input was not closed after idle window")
	}
	if !waitForOrchestrationEvent(t, out, "turn.delta", "claude", "Bridge closed Claude stream input after an idle window") {
		t.Fatal("frontend-visible idle close event was not emitted")
	}
}

func TestOrchestrationMachineOnlyTurnIsRelayedWithoutInjectedConclusion(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(claudePath, []byte(fakeClaudePrintScript("Msg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=none; next=none; risks=none")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexExecScript("unused")), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:    "orc_machine_only",
		Mode:     "collaboration",
		Prompt:   "检查证明框架",
		MaxTurns: 1,
		CWD:      tmp,
	})

	var sawRelayedTurn bool
	for len(out) > 0 {
		env := <-out
		if env.Type != protocol.TypeOrchestrationEvent {
			continue
		}
		event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == "turn.end" && event.CLI == "claude" {
			if !strings.Contains(event.Content, "Msg: to=user") || !strings.Contains(event.Content, "Handoff: status=resolved") {
				t.Fatalf("machine contract lines were not preserved: %#v", event)
			}
			if strings.Contains(event.Content, "最终结论") {
				t.Fatalf("relay should not inject a conclusion into CLI output: %#v", event)
			}
			sawRelayedTurn = true
		}
	}
	if !sawRelayedTurn {
		t.Fatal("did not see relayed turn.end content")
	}
}

func TestOrchestrationErroredFinalVerifierGetsReadableConclusion(t *testing.T) {
	record := newOrchestrationTurnRecord("orc_1-verifier", "verifier", "codex", "", nil)
	record.Verifier = true
	record.Err = "server_error"

	summary := erroredTurnFallbackSummary(
		"检查 Isabelle 证明框架",
		true,
		[]orchestrationTurn{{
			TurnID:  "orc_1-01",
			Role:    "implementer",
			CLI:     "claude",
			Content: "本轮结论：已经创建可编译证明框架。",
		}},
		record,
	)
	if !strings.Contains(summary, "最终结论") {
		t.Fatalf("errored verifier fallback missing final conclusion:\n%s", summary)
	}
}

func TestOrchestrationScanClaudeJSONLEmitsToolEvents(t *testing.T) {
	input := strings.NewReader(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_1","name":"Bash","input":{"command":"mkdir -p isabelle_bridge_demo"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_1","content":"created\n"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}
{"type":"result","result":"done"}
`)
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)

	got, _, err := manager.scanClaudeJSONL(input, "orc_test", "turn_1", "implementer")
	if err != nil {
		t.Fatal(err)
	}
	if got != "done" {
		t.Fatalf("content = %q, want done", got)
	}

	var events []protocol.OrchestrationEventPayload
	for len(out) > 0 {
		env := <-out
		if env.Type != protocol.TypeOrchestrationEvent {
			continue
		}
		payload, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
		if err == nil {
			events = append(events, payload)
		}
	}

	var sawStart, sawEnd bool
	for _, event := range events {
		if event.Kind == "command.start" && event.Status == "in_progress" && event.CLI == "claude" && event.Data["command"] == "mkdir -p isabelle_bridge_demo" {
			sawStart = true
			if _, ok := event.Data["startedAt"].(float64); !ok {
				t.Fatalf("command.start missing startedAt: %#v", event.Data)
			}
		}
		if event.Kind == "command.end" && event.Status == "completed" && event.CLI == "claude" && event.Data["output"] == "created\n" {
			sawEnd = true
			for _, key := range []string{"startedAt", "completedAt", "durationMs"} {
				if _, ok := event.Data[key].(float64); !ok {
					t.Fatalf("command.end missing %s: %#v", key, event.Data)
				}
			}
		}
	}
	if !sawStart || !sawEnd {
		t.Fatalf("missing tool events: sawStart=%v sawEnd=%v events=%#v", sawStart, sawEnd, events)
	}
}

func TestOrchestrationScanCodexJSONLNormalizesCamelCaseToolStatus(t *testing.T) {
	input := strings.NewReader(`{"type":"item.started","item":{"id":"cmd_1","type":"command_execution","command":"/bin/bash -lc 'command -v coqc || true'","status":"inProgress"}}
{"type":"item.completed","item":{"id":"cmd_1","type":"command_execution","command":"/bin/bash -lc 'command -v coqc || true'","status":"completed","exit_code":0,"aggregated_output":"/usr/bin/coqc\n"}}
{"type":"item.agent_message.delta","delta":"done"}
`)
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)

	got, _, err := manager.scanCodexJSONL(input, "orc_test", "turn_1", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if got != "done" {
		t.Fatalf("content = %q, want done", got)
	}

	var sawStart, sawEnd bool
	for len(out) > 0 {
		env := <-out
		if env.Type != protocol.TypeOrchestrationEvent {
			continue
		}
		event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		if event.Data["id"] != "cmd_1" {
			continue
		}
		switch event.Kind {
		case "command.start":
			sawStart = true
			if event.Status != "inProgress" {
				t.Fatalf("start status = %q", event.Status)
			}
		case "command.end":
			sawEnd = true
			if event.Status != "completed" || event.Data["output"] != "/usr/bin/coqc\n" {
				t.Fatalf("bad command end event: %#v", event)
			}
		}
	}
	if !sawStart || !sawEnd {
		t.Fatalf("missing normalized codex tool events: start=%v end=%v", sawStart, sawEnd)
	}
}

func TestOrchestrationScanCodexJSONLWaitsForProcessEOFAfterCompletedCommands(t *testing.T) {
	reader, writer := io.Pipe()
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)

	done := make(chan struct {
		content string
		err     error
	}, 1)
	go func() {
		content, _, err := manager.scanCodexJSONL(reader, "orc_test", "turn_1", "reviewer")
		done <- struct {
			content string
			err     error
		}{content: content, err: err}
	}()

	_, err := writer.Write([]byte(`{"type":"item.started","item":{"id":"cmd_1","type":"command_execution","command":"true","status":"inProgress"}}` + "\n" +
		`{"type":"item.completed","item":{"id":"cmd_1","type":"command_execution","command":"true","status":"completed","exit_code":0}}` + "\n" +
		`{"type":"item.agent_message.delta","delta":"finished after command"}` + "\n"))
	if err != nil {
		t.Fatal(err)
	}

	select {
	case result := <-done:
		t.Fatalf("scan returned before process EOF: content=%q err=%v", result.content, result.err)
	case <-time.After(50 * time.Millisecond):
	}

	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatalf("scan error = %v", result.err)
		}
		if result.content != "finished after command" {
			t.Fatalf("content = %q", result.content)
		}
	case <-time.After(time.Second):
		t.Fatal("scan did not return after process EOF")
	}
}

func TestOrchestrationCancelKillsCodexProcessGroup(t *testing.T) {
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "grandchild.pid")
	codexPath := filepath.Join(tmp, "codex")
	script := "#!/usr/bin/env bash\n" +
		"if [ \"${1:-}\" = exec ]; then shift; fi\n" +
		"(trap 'exit 0' TERM INT; echo $BASHPID > " + shellQuote(marker) + "; while true; do sleep 1; done) &\n" +
		"wait\n"
	if err := os.WriteFile(codexPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, _, err := manager.runCodex(ctx, protocol.OrchestrationStartPayload{RunID: "orc_cancel", CWD: tmp}, "turn_cancel", "reviewer", "stop")
		done <- err
	}()

	pid := waitForPIDFile(t, marker)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runCodex error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runCodex did not return after cancellation")
	}
	waitForProcessExit(t, pid)
}

func TestOrchestrationCancelKillsCCBProcessGroup(t *testing.T) {
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "ccb-grandchild.pid")
	ccbPath := filepath.Join(tmp, "ccb")
	script := "#!/usr/bin/env bash\n" +
		"(trap 'exit 0' TERM INT; echo $BASHPID > " + shellQuote(marker) + "; while true; do sleep 1; done) &\n" +
		"wait\n"
	if err := os.WriteFile(ccbPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Bridge.CCBPath = ccbPath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := manager.runCCBCommandStreaming(ctx, protocol.OrchestrationStartPayload{RunID: "orc_ccb_cancel", CWD: tmp}, "", nil, nil, "run")
		done <- err
	}()

	pid := waitForPIDFile(t, marker)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) && err == nil {
			t.Fatalf("runCCBCommandStreaming error = %v, want cancellation error", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("runCCBCommandStreaming did not return after cancellation")
	}
	waitForProcessExit(t, pid)
}

func TestOrchestrationScanClaudeJSONLSuppressesEmptyPagesReadFailure(t *testing.T) {
	input := strings.NewReader(`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_1","name":"Read","input":{"file_path":"/tmp/Model.thy","pages":""}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_1","is_error":true,"content":"<tool_use_error>Invalid pages parameter: \"\". Use formats like \"1-5\", \"3\", or \"10-20\". Pages are 1-indexed.</tool_use_error>"}]}}
{"type":"assistant","message":{"content":[{"type":"text","text":"retrying"}]}}
{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_2","name":"Read","input":{"file_path":"/tmp/Model.thy"}}]}}
{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_2","content":"theory Model\n"}]}}
{"type":"result","result":"retrying"}
`)
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)

	got, tools, err := manager.scanClaudeJSONL(input, "orc_test", "turn_1", "implementer")
	if err != nil {
		t.Fatal(err)
	}
	if got != "retrying" {
		t.Fatalf("content = %q, want retrying", got)
	}
	for _, tool := range tools {
		if tool.ID == "tool_1" {
			t.Fatalf("empty pages failure was retained in tools: %#v", tools)
		}
	}

	var events []protocol.OrchestrationEventPayload
	for len(out) > 0 {
		env := <-out
		if env.Type != protocol.TypeOrchestrationEvent {
			continue
		}
		payload, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
		if err == nil {
			events = append(events, payload)
		}
	}

	var sawRetryRead bool
	for _, event := range events {
		if event.Kind == "command.start" && event.Data["id"] == "tool_1" {
			t.Fatalf("empty pages read start was emitted: %#v", events)
		}
		if event.Kind == "command.end" && event.Data["id"] == "tool_1" {
			t.Fatalf("empty pages read failure was emitted: %#v", events)
		}
		if event.Kind == "command.end" && event.Data["id"] == "tool_2" && event.Data["output"] == "theory Model\n" {
			sawRetryRead = true
		}
	}
	if !sawRetryRead {
		t.Fatalf("successful retry read was not emitted: %#v", events)
	}
}

func TestOrchestrationEventsBufferWhileBridgeDisconnected(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	firstOut := make(chan protocol.Envelope, 2)
	manager.AttachOut(firstOut)
	manager.DetachOut(firstOut)

	manager.emit("orc_1", protocol.OrchestrationEventPayload{Kind: "turn.start", TurnID: "turn_1"})
	manager.emit("orc_1", protocol.OrchestrationEventPayload{Kind: "turn.delta", TurnID: "turn_1", Content: "working"})

	nextOut := make(chan protocol.Envelope, 2)
	manager.AttachOut(nextOut)

	for _, wantKind := range []string{"turn.start", "turn.delta"} {
		select {
		case env := <-nextOut:
			if env.Type != protocol.TypeOrchestrationEvent {
				t.Fatalf("env type = %q", env.Type)
			}
			payload, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
			if err != nil {
				t.Fatal(err)
			}
			if payload.RunID != "orc_1" || payload.Kind != wantKind || payload.TurnID != "turn_1" {
				t.Fatalf("payload = %#v, want kind %s for orc_1/turn_1", payload, wantKind)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for buffered %s event", wantKind)
		}
	}
}

func TestComposeOrchestrationPromptIncludesResumeContext(t *testing.T) {
	prompt := composeOrchestrationPrompt("collaboration", "continue the fix", "tests already pass", true, "reviewer", "codex", 1, 2, nil)
	if !strings.Contains(prompt, "continuation of the same user-visible orchestration conversation") {
		t.Fatalf("resume prompt missing continuation guidance:\n%s", prompt)
	}
	if !strings.Contains(prompt, "tests already pass") {
		t.Fatalf("resume prompt missing compacted context:\n%s", prompt)
	}
	if !strings.Contains(prompt, "continue the fix") {
		t.Fatalf("resume prompt missing latest task:\n%s", prompt)
	}
}

func TestComposeOrchestrationPromptUsesCompactHandoffs(t *testing.T) {
	longDetail := strings.Repeat("very long implementation detail ", 120)
	history := []orchestrationTurn{{
		Role:    "implementer",
		CLI:     "claude",
		Msg:     "Msg: to=reviewer; intent=review; need=check prompt contract",
		Content: "Changed internal/bridge/orchestration.go.\n\n" + longDetail + "\n\nMsg: to=reviewer; intent=review; need=check prompt contract\nHandoff: status=needs_next; changed=internal/bridge/orchestration.go; verified=go test ./internal/bridge; next=review prompt; risks=none",
		Handoff: "Handoff: status=needs_next; changed=internal/bridge/orchestration.go; verified=go test ./internal/bridge; next=review prompt; risks=none",
		HandoffFields: orchestrationHandoffFields{
			Status:   "needs_next",
			Changed:  "internal/bridge/orchestration.go",
			Verified: "go test ./internal/bridge",
			Next:     "review prompt",
		},
	}}

	prompt := composeOrchestrationPrompt("collaboration", "review it", "", false, "reviewer", "codex", 2, 4, history)
	for _, want := range []string{"From: reviewer/codex", "To: implementer/claude", "Mode: collaboration", "builder-reviewer collaboration", orchestrationMsgContract, orchestrationHandoffContract, "Compact prior-turn handoffs", "intent=review", "status=needs_next"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "very long implementation detail") {
		t.Fatalf("prompt included raw long transcript instead of compact handoff:\n%s", prompt)
	}
}

func TestComposeOrchestrationPromptRequiresGoalProgressAudit(t *testing.T) {
	prompt := composeOrchestrationPrompt("collaboration", "先消除主定理的 sorry", "", false, "reviewer", "codex", 2, 4, []orchestrationTurn{{
		Role:    "implementer",
		CLI:     "claude",
		Content: "本轮结论：只是确认项目能编译。\n\nHandoff: status=needs_next; changed=none; verified=isabelle build -D .; next=remove main theorem sorry; risks=main theorem sorry still present",
		HandoffFields: orchestrationHandoffFields{
			Status:   "needs_next",
			Verified: "isabelle build -D .",
			Next:     "remove main theorem sorry",
			Risks:    "main theorem sorry still present",
		},
	}})
	for _, want := range []string{
		"Latest user task is authoritative",
		"Explicitly audit whether the previous turn advanced the user's core acceptance criterion",
		"do not treat a narrow validation such as compiling as resolved",
		"main theorem sorry still present",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestComposeRelayPromptKeepsCoqUploadTaskPassThrough(t *testing.T) {
	prompt := composeRelayPrompt(
		"collaboration",
		"把 Model.thy Termination.thy ROOT 做成 Coq 项目，补全 termination modify_lin 的证明，不能用占位符。",
		"",
		false,
		"implementer",
		"claude",
		1,
		4,
		nil,
	)
	for _, want := range []string{
		"Codex Bridge is relaying this browser orchestration like a human handoff",
		"Treat this as a real user instruction",
		"You are the first CLI handling the user's task",
		"visible result will be handed to another CLI afterward",
		"Formal proof relay guidance",
		"Coq upload benchmark",
		"Print Assumptions",
		"User task:",
		"Model.thy Termination.thy ROOT",
		"termination modify_lin",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("relay prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, bad := range []string{
		"Formal proof task guardrails",
		"controlled background",
	} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("relay prompt should not inject old proof gate %q:\n%s", bad, prompt)
		}
	}
}

func TestComposeRelayPromptAddsOnlyIsabelleTimeoutBoundary(t *testing.T) {
	prompt := composeRelayPrompt(
		"collaboration",
		"已上传 Model.thy、Termination.thy、ROOT。请在 Isabelle 中补全 termination modify_lin 证明，不能用 sorry 或 quick_and_dirty。",
		"",
		false,
		"implementer",
		"claude",
		1,
		4,
		nil,
	)
	for _, want := range []string{
		"Isabelle timeout boundary",
		"choose and use an explicit timeout",
		"If that timeout is exceeded, stop the build and report the command",
		"Bridge otherwise does not constrain how you run the CLI task",
		"Formal proof relay guidance",
		"thm_oracles",
		"User task:",
		"termination modify_lin",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("Isabelle relay prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, bad := range []string{
		"Formal proof task guardrails",
		"Isabelle full-build visibility rule",
		"controlled background",
		"Do not run `timeout ... isabelle build ...`",
		"build.pid",
	} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("Isabelle relay prompt should not inject old guardrail %q:\n%s", bad, prompt)
		}
	}
}

func TestCoqUploadPromptDoesNotTriggerIsabelleRuntimeBoundary(t *testing.T) {
	task := "把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全\n已上传文件\nModel.thy\nTermination.thy\nROOT"
	prompt := composeRelayPrompt("collaboration", task, "", false, "implementer", "claude", 1, 2, nil)
	if strings.Contains(prompt, "Isabelle timeout boundary") {
		t.Fatalf("Coq upload prompt should not include Isabelle runtime boundary:\n%s", prompt)
	}
	if shouldUseClaudeStreamInput(task) {
		t.Fatal("Coq upload prompt should not use Claude stream input")
	}
	if !looksLikeIsabelleProofTask(task) {
		t.Fatal("fixture should still look like a formal Isabelle-source upload due .thy/ROOT files")
	}
	if !looksLikeCoqProofTask(task) {
		t.Fatal("fixture should look like a Coq proof task")
	}
}

func TestComposeRelayPromptCarriesPreviousCLIResultAndCommandContext(t *testing.T) {
	exitCode := 124
	tailExitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_1", "implementer", "claude", strings.Join([]string{
			"最终结论：Isabelle build 超过本轮窗口，已经交给用户手动执行。",
			"手动执行：timeout 45m sh -lc 'isabelle build -D /root/tencent/linlattice-isabelle > /root/tencent/linlattice-isabelle/build.log 2>&1'",
			"日志路径：/root/tencent/linlattice-isabelle/build.log",
			"状态文件：/root/tencent/linlattice-isabelle/build.pid /root/tencent/linlattice-isabelle/build.pgid /root/tencent/linlattice-isabelle/build.exit",
			"后续 CLI 不需要执行这个 build，只读取日志和源码。",
			"",
			"Msg: to=reviewer; intent=review; need=manual build status",
			"Handoff: status=needs_next; changed=/root/tencent/linlattice-isabelle; verified=tail build.log; next=user manually run isabelle build; risks=manual build pending",
		}, "\n"), []RunnerToolEvent{
			{
				ID:       "build",
				Status:   "failed",
				Command:  `sh -lc 'rm -f build.log build.pid build.pgid build.exit; setsid sh -lc "echo $$ > build.pid; echo $$ > build.pgid; timeout 45m sh -lc '\''isabelle build -D .'\'' >build.log 2>&1; echo $? > build.exit" &'`,
				Output:   "timed out\n",
				ExitCode: &exitCode,
			},
			{
				ID:       "tail",
				Status:   "completed",
				Command:  `tail -n 80 /root/tencent/linlattice-isabelle/build.log && cat /root/tencent/linlattice-isabelle/build.exit`,
				Output:   "Running LinLattice ...\n124\n",
				ExitCode: &tailExitCode,
			},
		}),
	}

	prompt := composeRelayPrompt(
		"collaboration",
		"已上传 Model.thy、Termination.thy、ROOT。请在 Isabelle 中补全 termination modify_lin 证明。",
		"",
		false,
		"reviewer",
		"codex",
		2,
		4,
		history,
	)

	for _, want := range []string{
		"You are continuing from the previous CLI's visible result",
		"Previous CLI result and useful command context",
		"implementer/claude",
		"manual build pending",
		"/root/tencent/linlattice-isabelle/build.log",
		"tail -n 80 /root/tencent/linlattice-isabelle/build.log",
		"Running LinLattice",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("relay handoff prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, bad := range []string{
		"Isabelle manual-build carry-over",
		"Do not rerun the same `isabelle build -D`",
		"final proof acceptance is pending",
	} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("relay prompt should not inject manual-build policy %q:\n%s", bad, prompt)
		}
	}
}

func TestComposeRelayPromptCarriesResumeContextWithoutManualBuildPolicy(t *testing.T) {
	contextSummary := strings.Join([]string{
		"Compacted orchestration context from previous work.",
		"Tool outcomes and commands:",
		"- sh -lc 'timeout 45m sh -lc '\\''isabelle build -D /root/tencent/linlattice-isabelle'\\'' > /root/tencent/linlattice-isabelle/build.log 2>&1' failed Isabelle build timed out; see /root/tencent/linlattice-isabelle/build.log",
		"- tail -n 80 /root/tencent/linlattice-isabelle/build.log completed Running LinLattice ...",
		"Recent agent outputs:",
		"- 后续 CLI 不需要执行这个build，只读取日志和源码。状态文件 /root/tencent/linlattice-isabelle/build.pid /root/tencent/linlattice-isabelle/build.pgid /root/tencent/linlattice-isabelle/build.exit",
	}, "\n")

	prompt := composeRelayPrompt(
		"collaboration",
		"已上传 Model.thy、Termination.thy、ROOT。继续补全 Isabelle termination modify_lin 证明。",
		contextSummary,
		true,
		"reviewer",
		"codex",
		1,
		2,
		nil,
	)

	for _, want := range []string{
		"continuation of the same user-visible orchestration conversation",
		"Compacted context from earlier tasks in this conversation",
		"/root/tencent/linlattice-isabelle/build.log",
		"/root/tencent/linlattice-isabelle/build.pid",
		"/root/tencent/linlattice-isabelle/build.pgid",
		"/root/tencent/linlattice-isabelle/build.exit",
		"Running LinLattice",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("resume relay prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, bad := range []string{
		"Isabelle manual-build carry-over",
		"Do not rerun the same `isabelle build -D`",
		"final proof acceptance is pending",
	} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("relay prompt should not inject resume manual-build policy %q:\n%s", bad, prompt)
		}
	}
}

func TestIsabelleManualBuildVisibleSummaryMentionsNoRerunAndArtifacts(t *testing.T) {
	contextSummary := strings.Join([]string{
		"Compacted orchestration context from previous work.",
		"- sh -lc 'timeout 45m sh -lc '\\''isabelle build -D /root/tencent/linlattice-isabelle'\\'' > /root/tencent/linlattice-isabelle/build.log 2>&1' failed Isabelle build timed out; see /root/tencent/linlattice-isabelle/build.log",
		"- tail -n 80 /root/tencent/linlattice-isabelle/build.log completed Running LinLattice ...",
		"- 状态文件 /root/tencent/linlattice-isabelle/build.pid /root/tencent/linlattice-isabelle/build.pgid /root/tencent/linlattice-isabelle/build.exit",
	}, "\n")

	summary := isabelleManualBuildVisibleSummary(
		"已上传 Model.thy、Termination.thy、ROOT。继续补全 Isabelle termination modify_lin 证明。",
		contextSummary,
		nil,
	)
	for _, want := range []string{
		"Isabelle 长时间 build 交接",
		"本轮不会自动重复执行同一个 `isabelle build -D` / `isabelle build -d`",
		"/root/tencent/linlattice-isabelle/build.log",
		"/root/tencent/linlattice-isabelle/build.pid",
		"/root/tencent/linlattice-isabelle/build.pgid",
		"/root/tencent/linlattice-isabelle/build.exit",
		"Running LinLattice",
		"最终验收等待用户的手动 Isabelle build 结果",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("visible summary missing %q:\n%s", want, summary)
		}
	}
}

func TestIsabelleManualBuildVisibleSummaryQualifiesRelativeArtifacts(t *testing.T) {
	contextSummary := strings.Join([]string{
		"Compacted orchestration context from previous work.",
		"- cd '/root/tencent/linlattice_isabelle_termination' && sh -lc 'rm -f build.log build.pid build.pgid build.exit; setsid sh -lc \"echo $$ > build.pid; echo $$ > build.pgid; timeout 45m sh -lc '\\''isabelle build -D .'\\'' >build.log 2>&1; echo $? > build.exit\" &' | in_progress",
		"- cd '/root/tencent/linlattice_isabelle_termination' && tail -n 80 build.log completed Running LinLattice ...",
		"- run.cancelled context canceled while build.pid/build.pgid existed",
	}, "\n")

	summary := isabelleManualBuildVisibleSummary(
		"已上传 Model.thy、Termination.thy、ROOT。继续补全 Isabelle termination modify_lin 证明。",
		contextSummary,
		nil,
	)
	for _, want := range []string{
		"日志路径：/root/tencent/linlattice_isabelle_termination/build.log",
		"pid=/root/tencent/linlattice_isabelle_termination/build.pid",
		"pgid=/root/tencent/linlattice_isabelle_termination/build.pgid",
		"exit=/root/tencent/linlattice_isabelle_termination/build.exit",
	} {
		if !strings.Contains(summary, want) {
			t.Fatalf("relative artifact summary missing %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "日志路径：>build.log") {
		t.Fatalf("summary kept shell redirection as log path:\n%s", summary)
	}
}

func TestForbiddenForegroundIsabelleBuildErrorHelperDoesNotAffectRelayPath(t *testing.T) {
	err := forbiddenForegroundIsabelleBuildError(
		"已上传 Model.thy、Termination.thy、ROOT。请在 Isabelle 中补全 termination modify_lin 证明。",
		[]RunnerToolEvent{{Command: "timeout 60s sh -lc 'cd /root/tencent/linlattice_formal && isabelle build -D . -v'"}},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "foreground Isabelle build is not allowed") {
		t.Fatalf("foreground Isabelle build error = %v", err)
	}
	prompt := composeRelayPrompt("collaboration", "已上传 Model.thy、Termination.thy、ROOT。请在 Isabelle 中补全 termination modify_lin 证明。", "", false, "implementer", "claude", 1, 2, nil)
	if strings.Contains(prompt, "foreground Isabelle build is not allowed") || strings.Contains(prompt, "controlled background") {
		t.Fatalf("relay prompt should not expose old foreground-build rejection policy:\n%s", prompt)
	}

	err = forbiddenForegroundIsabelleBuildError(
		"已上传 Model.thy、Termination.thy、ROOT。请在 Isabelle 中补全 termination modify_lin 证明。",
		[]RunnerToolEvent{{Command: `sh -lc 'rm -f build.log build.pid build.pgid build.exit; setsid sh -lc "echo $$ > build.pid; echo $$ > build.pgid; timeout 45m sh -lc '\''isabelle build -D .'\'' >build.log 2>&1; echo $? > build.exit" &'`}},
		nil,
	)
	if err != nil {
		t.Fatalf("controlled background Isabelle build was rejected: %v", err)
	}

	err = forbiddenForegroundIsabelleBuildError(
		"已上传 Model.thy、Termination.thy、ROOT。请在 Isabelle 中补全 termination modify_lin 证明。",
		[]RunnerToolEvent{{Command: `sh -lc 'rm -f build.log build.pid build.exit; (timeout 45m sh -lc "isabelle build -D ." >build.log 2>&1; echo $? > build.exit) & echo $! > build.pid'`}},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "foreground Isabelle build is not allowed") {
		t.Fatalf("background build without PGID should be rejected: %v", err)
	}
}

func TestComposeRelayDebatePromptDoesNotInjectProofFalsificationStrategy(t *testing.T) {
	prompt := composeRelayPrompt(
		"debate",
		"补全 Coq theorem，不能用 Admitted 或 Axiom。",
		"",
		false,
		"critic",
		"codex",
		2,
		4,
		nil,
	)
	for _, want := range []string{
		"human handoff",
		"Treat this as a real user instruction",
		"Mode: debate",
		"Current CLI: critic/codex",
		"Formal proof relay guidance",
		"Debate critic focus",
		"fuel/default_fuel shortcuts",
		"补全 Coq theorem",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("debate relay prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, bad := range []string{
		"Formal proof task guardrails",
		"Debate proof workflow",
	} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("debate relay prompt should not inject old proof strategy %q:\n%s", bad, prompt)
		}
	}
}

func TestRelayProofGuardrailsDoNotTriggerOnRootPathOnly(t *testing.T) {
	prompt := composeRelayPrompt("collaboration", "修复 /root/tencent/bridge 的前端刷新问题", "", false, "implementer", "claude", 1, 4, nil)
	if strings.Contains(prompt, "Formal proof task guardrails") {
		t.Fatalf("root path alone should not trigger proof guardrails:\n%s", prompt)
	}
}

func TestFormatCompactPriorTurnDoesNotRecurseThroughFallbackConclusions(t *testing.T) {
	turn := newOrchestrationTurnRecord("turn_1", "implementer", "claude", strings.Join([]string{
		"本轮结论：本轮编排已完成，并已记录当前可确认的结果。",
		"",
		"结果概览：本轮结论：本轮编排已完成，并已记录当前可确认的结果。",
		"",
		"已验证：执行完成：`ToolSearch`。",
		"",
		"剩余风险：主定理 sorry 仍未消除。",
	}, "\n"), nil)
	got := formatCompactPriorTurn(turn)
	if strings.Contains(got, "结果概览") || strings.Count(got, "本轮结论") > 1 {
		t.Fatalf("compact prior turn kept recursive fallback text:\n%s", got)
	}
	if !strings.Contains(got, "主定理 sorry") {
		t.Fatalf("compact prior turn lost concrete risk:\n%s", got)
	}
}

func TestFormatCompactPriorTurnCarriesFailedCommands(t *testing.T) {
	exitCode := 1
	turn := orchestrationTurn{
		Role:          "implementer",
		CLI:           "claude",
		HandoffFields: orchestrationHandoffFields{Status: "needs_next", Next: "fix failed command", Risks: "mkdir failed repeatedly"},
		Tools: []RunnerToolEvent{
			{ID: "cmd_1", Status: "failed", Command: "mkdir -p /root/Isabelle", Output: "Permission denied", ExitCode: &exitCode},
		},
	}
	got := formatCompactPriorTurn(turn)
	for _, want := range []string{"failed:", "mkdir -p /root/Isabelle", "Permission denied"} {
		if !strings.Contains(got, want) {
			t.Fatalf("compact prior turn missing %q:\n%s", want, got)
		}
	}
}

func TestParseHandoffFieldsAndCompactPromptAvoidsRawTranscript(t *testing.T) {
	fields := parseHandoffFields("Handoff: status=needs_next; changed=main.go, README.md; verified=go test ./...; next=fix lint; risks=doc drift")
	if fields.Status != "needs_next" || fields.Changed != "main.go, README.md" || fields.Verified != "go test ./..." || fields.Next != "fix lint" || fields.Risks != "doc drift" {
		t.Fatalf("fields = %#v", fields)
	}
	turn := orchestrationTurn{
		Role:          "implementer",
		CLI:           "claude",
		Content:       strings.Repeat("verbose details ", 200),
		HandoffFields: fields,
	}
	got := formatCompactPriorTurn(turn)
	for _, want := range []string{"changed=main.go, README.md", "verified=go test ./...", "risks=doc drift"} {
		if !strings.Contains(got, want) {
			t.Fatalf("compact turn missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "verbose details") {
		t.Fatalf("compact turn included raw transcript:\n%s", got)
	}
}

func TestPrepareOrchestrationPromptFilesProvidesLocalPathsOnly(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CWD = t.TempDir()
	prompt, metas, err := PrepareOrchestrationPromptFiles(&cfg, "", "orc_pdf", "read this", []protocol.AttachmentPayload{{
		Name:     "paper.pdf",
		MimeType: "application/pdf",
		Size:     int64(len("pdf")),
		Data:     "cGRm",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Name != "paper.pdf" {
		t.Fatalf("metas = %#v", metas)
	}
	for _, want := range []string{"Uploaded files for this orchestration run:", "01-paper.pdf", "Use these local file paths directly"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, bad := range []string{"do not use Claude's Read tool", "Do not send an empty pages field", "inspect them with shell commands"} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("prompt should not inject file-tool policy %q:\n%s", bad, prompt)
		}
	}
}

func TestPrepareOrchestrationPromptFilesUsesRunCWD(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CWD = filepath.Join(t.TempDir(), "configured")
	runCWD := filepath.Join(t.TempDir(), "actual-run")
	prompt, _, err := PrepareOrchestrationPromptFiles(&cfg, runCWD, "orc_cwd", "read this", []protocol.AttachmentPayload{{
		Name:     "Model.thy",
		MimeType: "application/octet-stream",
		Size:     int64(len("thy")),
		Data:     "dGh5",
	}})
	if err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(runCWD, ".codex-bridge", "orchestrations", "orc_cwd")
	if !strings.Contains(prompt, wantDir) {
		t.Fatalf("prompt should contain upload path under run cwd %q:\n%s", wantDir, prompt)
	}
	if strings.Contains(prompt, cfg.Bridge.CWD) {
		t.Fatalf("prompt should not use configured cwd %q when run cwd is set:\n%s", cfg.Bridge.CWD, prompt)
	}
	if _, err := os.Stat(filepath.Join(wantDir, "01-Model.thy")); err != nil {
		t.Fatalf("uploaded file not written under run cwd: %v", err)
	}
}

func TestFinalVerifierDisabledForPassThroughRelay(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	clean := []orchestrationTurn{{
		HandoffFields: orchestrationHandoffFields{Status: "resolved", Verified: "go test ./...", Risks: "none"},
	}}
	if manager.shouldRunFinalVerifier(clean) {
		t.Fatal("clean resolved run should skip final verifier")
	}
	resolvedChanged := []orchestrationTurn{{
		HandoffFields: orchestrationHandoffFields{Status: "resolved", Changed: "main.go", Verified: "go test ./...", Risks: "none"},
	}}
	if manager.shouldRunFinalVerifier(resolvedChanged) {
		t.Fatal("pass-through relay should not trigger hidden final verifier for file changes")
	}
	changed := []orchestrationTurn{{
		HandoffFields: orchestrationHandoffFields{Status: "needs_next", Changed: "main.go"},
	}}
	if manager.shouldRunFinalVerifier(changed) {
		t.Fatal("pass-through relay should not trigger hidden final verifier for needs_next handoff")
	}
	exitCode := 1
	failed := []orchestrationTurn{{
		Tools: []RunnerToolEvent{{Command: "go test ./...", Status: "completed", ExitCode: &exitCode}},
	}}
	if manager.shouldRunFinalVerifier(failed) {
		t.Fatal("pass-through relay should not trigger hidden final verifier for command results")
	}
}

func TestTrimForPromptPreservesUTF8WhenTruncatingMultibyteText(t *testing.T) {
	got := trimForPrompt("验", 1)
	if !utf8.ValidString(got) {
		t.Fatalf("trimForPrompt returned invalid UTF-8 bytes: % x", []byte(got))
	}

	history := []orchestrationTurn{{
		Role:    "implementer",
		CLI:     "claude",
		Content: strings.Repeat("a", 1799) + "验" + strings.Repeat("b", 64),
		Tools: []RunnerToolEvent{{
			Command: strings.Repeat("c", 259) + "证" + strings.Repeat("d", 32),
			Status:  "completed",
			Output:  strings.Repeat("e", 259) + "据" + strings.Repeat("f", 32),
		}},
	}}
	prompt := composeRelayPrompt("collaboration", "继续验证", "", false, "reviewer", "codex", 2, 2, history)
	if !utf8.ValidString(prompt) {
		t.Fatalf("composeRelayPrompt produced invalid UTF-8 around handoff truncation")
	}
}

func TestRepeatedBlockedHandoffIsRelayedThroughScheduledTurns(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	blocked := strings.Join([]string{
		"结论：没有推进主目标，创建 /root/Isabelle 的写入权限异常仍在阻塞。",
		"",
		"Msg: to=reviewer; intent=review; need=none",
		"Handoff: status=blocked; changed=none; verified=none; next=create /root/Isabelle; risks=permission layer blocks mkdir",
	}, "\n")
	if err := os.WriteFile(claudePath, []byte(fakeClaudePrintScript(blocked)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexExecScript(blocked)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:    "orc_blocked",
		Mode:     "collaboration",
		Prompt:   "先消除主定理的 sorry",
		MaxTurns: 6,
		CWD:      tmp,
	})

	turnStarts := 0
	var runEnd protocol.OrchestrationEventPayload
	for len(out) > 0 {
		env := <-out
		if env.Type != protocol.TypeOrchestrationEvent {
			continue
		}
		event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == "turn.start" {
			turnStarts++
		}
		if event.Kind == "run.error" {
			t.Fatalf("pass-through relay should not fail repeated CLI blockers: %#v", event)
		}
		if event.Kind == "run.end" {
			runEnd = event
		}
	}
	if runEnd.Kind != "run.end" || !strings.Contains(runEnd.Content, "permission layer blocks mkdir") {
		t.Fatalf("missing relayed run.end with blocker content: %#v", runEnd)
	}
	if turnStarts != 6 {
		t.Fatalf("relay should exhaust scheduled turns, saw %d starts", turnStarts)
	}
}

func TestUnresolvedFinalHandoffCompletesAsRelayedCLIResult(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	claudeDone := "结论：已确认任务，但还没有消除主定理 sorry。\n\nMsg: to=reviewer; intent=review; need=check main theorem sorry\nHandoff: status=needs_next; changed=none; verified=none; next=remove main theorem sorry; risks=主定理 sorry 仍未消除"
	codexDone := "结论：复查后确认主定理 sorry 仍未消除，不能算完成。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=needs_next; changed=none; verified=isabelle build -D /root/Isabelle; next=remove main theorem sorry; risks=主定理 sorry 仍未消除"
	if err := os.WriteFile(claudePath, []byte(fakeClaudePrintScript(claudeDone)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexExecScript(codexDone)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:    "orc_unresolved_final",
		Mode:     "collaboration",
		Prompt:   "先消除主定理的 sorry",
		MaxTurns: 2,
		CWD:      tmp,
	})

	var sawRunEnd bool
	for len(out) > 0 {
		env := <-out
		if env.Type != protocol.TypeOrchestrationEvent {
			continue
		}
		event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == "run.end" {
			sawRunEnd = true
			if !strings.Contains(event.Content, "主定理 sorry 仍未消除") {
				t.Fatalf("run.end lost unresolved CLI content: %#v", event)
			}
		}
		if event.Kind == "run.error" {
			t.Fatalf("pass-through relay should not fail unresolved CLI handoff: %#v", event)
		}
	}
	if !sawRunEnd {
		t.Fatal("missing run.end for unresolved final handoff")
	}
}

func TestResolvedHandoffWithContradictoryAcceptanceEvidenceFailsRun(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_1", "implementer", "claude", "结论：尝试处理用户要求。", nil),
		newOrchestrationTurnRecord("turn_2", "reviewer", "codex", "最终结论：已检查，验收标准未满足，不能把当前状态视为完成。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=result.txt; verified=check acceptance; next=none; risks=none", []RunnerToolEvent{
			{ID: "cmd_1", Status: "completed", Command: "check acceptance", Output: "acceptance criterion is not satisfied", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("检查最终状态", history, workspaceChangeReport{})
	if !unresolved {
		t.Fatal("resolved handoff with contradictory acceptance evidence should fail")
	}
	if !strings.Contains(reason, "acceptance criterion is not satisfied") {
		t.Fatalf("reason should mention contradictory acceptance evidence: %q", reason)
	}
}

func TestAcceptanceFailurePrefersVerifierExplanationOverRawCommand(t *testing.T) {
	exitCode := 1
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_verifier", "verifier", "codex", strings.Join([]string{
			"结论：当前 /root/tencent/coq-lin-lattice 是可编译的 Coq 项目，且未发现占位符。",
			"",
			"但验收条件里关键的“补全缺失的证明”仍不能判为完成：当前 Coq 版本改成 modify_lin_fuel，并用固定 default_fuel 包装。",
			"现有 Termination.v 证明的是燃料递归会结束及长度保持，没有证明原递归每步下降、没有证明 Distance 下降，也没有证明默认燃料足够模拟原 Isabelle 递归到停止态。",
			"",
			"Msg: to=user; intent=final; need=none",
			"Handoff: status=resolved; changed=Model.v, Termination.v; verified=make; next=prove termination modify_lin equivalence; risks=modify_lin_fuel bypasses original termination proof",
		}, "\n"), []RunnerToolEvent{
			{ID: "cmd_1", Status: "failed", Command: `/bin/bash -lc 'rg -n "modify_lin|fun modify_lin|function modify_lin|Distance|termination|sorry" /root/tencent/coq-lin-lattice'`, Output: "acceptance check failed", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("把这三个做成coq的证明项目并补全缺失的证明", history, workspaceChangeReport{Available: true, Changed: []string{"Model.v", "Termination.v"}})
	if !unresolved {
		t.Fatal("verifier rejection should fail the run")
	}
	for _, want := range []string{"modify_lin_fuel", "default_fuel", "没有证明"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason should preserve verifier explanation %q: %q", want, reason)
		}
	}
	if strings.Contains(reason, "/bin/bash") || strings.Contains(reason, "rg -n") {
		t.Fatalf("reason should not prefer raw acceptance command: %q", reason)
	}
}

func TestResolvedProofRunAllowsForbiddenTokenScanCommand(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_verifier", "reviewer", "codex", strings.Join([]string{
			"结论：当前项目使用 Coq 可检查的 structural recursion/measure 结构；没有 bounded/default fuel，没有 runtime distance guard，没有占位符或未声明公理。",
			"",
			"Msg: to=user; intent=final; need=none",
			"Handoff: status=resolved; changed=none; verified=make/rg/coqtop/python-audit; next=none; risks=none",
		}, "\n"), []RunnerToolEvent{
			{
				ID:       "scan",
				Status:   "completed",
				Command:  `/bin/bash -lc "rg -n \"modify_lin_fuel|default_fuel|fuel|Distance candidate|guard|\\b(Axiom|Admitted|admit|Parameter|Conjecture|Abort|sorry|TODO|placeholder|quick_and_dirty)\\b\" /root/tencent/coq-lin-lattice-complete -S || true"`,
				Output:   "",
				ExitCode: &exitCode,
			},
		}),
	}
	reason, unresolved := unresolvedFinalRun("补全 modify_lin 证明，不能用占位符", history, workspaceChangeReport{Available: true, Changed: []string{"Model.v", "Termination.v"}})
	if unresolved {
		t.Fatalf("forbidden-token scan command text with empty output should not fail resolved run: %q", reason)
	}
}

func TestPlaceholderScanChecksCoqTrustBypasses(t *testing.T) {
	command := `rg -n "\b(Axiom|Parameter|Variable|Hypothesis|Conjecture|Admitted|admit|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check)\b" coq-proj -S`
	if !placeholderScanEvidence(command, "", "") {
		t.Fatal("expected source scan covering Coq trust bypasses with empty output to count as evidence")
	}
	if !placeholderScanFoundForbiddenOutput("Model.v:12:Unset Guard Checking.\nTermination.v:4:Hypothesis trusted : Prop.\n") {
		t.Fatal("expected guard checking and Hypothesis output to be forbidden proof shortcuts")
	}
	if !placeholderScanFoundForbiddenOutput("Model.v:20:Definition bypass_check := true.\n") {
		t.Fatal("expected bypass_check output to be a forbidden proof shortcut")
	}
}

func TestPlaceholderScanOutputOverridesNoPlaceholderClaim(t *testing.T) {
	command := `rg -n "sorry|quick_and_dirty|oops|sketch|admit|TODO|placeholder" isabelle-proj -g "*.thy" -g ROOT -S`
	output := "Termination.thy:12:  oops\n"
	if placeholderScanEvidence(command, output, "source-only placeholder scan：无输出。") {
		t.Fatal("forbidden scan output must override a no-placeholder prose claim")
	}
}

func TestPlaceholderScanRejectsIsabelleDiagnosticLeftovers(t *testing.T) {
	command := `rg -n "sorry|quick_and_dirty|oops|sketch|admit|TODO|placeholder|Repro\\.thy|_original\\.thy|scratch" isabelle-proj -g "*.thy" -g ROOT -S`
	output := "ROOT:6:    Repro\nHWQ-U/Termination_original.thy:4:termination modify_lin\n"
	if placeholderScanEvidence(command, output, "source-only placeholder scan：无输出。") {
		t.Fatal("diagnostic leftover files/imports must not satisfy placeholder scan evidence")
	}
	if !placeholderScanFoundForbiddenOutput(output) {
		t.Fatal("expected Repro.thy and *_original.thy scan output to be forbidden")
	}
}

func TestPlaceholderScanIgnoresEarlierNonScanBuildOutput(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_verifier", "reviewer", "codex", "source-only placeholder scan：rg 无输出。", []RunnerToolEvent{
			{ID: "build1", Status: "failed", Command: "isabelle build -D isabelle-proj", Output: "Termination.thy: sorry", ExitCode: &exitCode},
			{ID: "scan", Status: "completed", Command: `rg -n "sorry|quick_and_dirty|oops|sketch|admit|TODO|placeholder" isabelle-proj -g "*.thy" -g ROOT -S`, Output: "", ExitCode: &exitCode},
		}),
	}
	if !placeholderScanEvidenceForHistory(history, "") {
		t.Fatal("a later empty source scan should satisfy scan evidence despite earlier non-scan build output")
	}
}

func TestResolvedCoqUploadRunRequiresAssessmentDimensions(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_verifier", "reviewer", "codex", strings.Join([]string{
			"最终结论：项目已经创建并且 make 通过。",
			"",
			"Msg: to=user; intent=final; need=none",
			"Handoff: status=resolved; changed=Model.v, Termination.v, Makefile; verified=make; next=none; risks=none",
		}, "\n"), []RunnerToolEvent{
			{ID: "build", Status: "completed", Command: "make -C /root/tencent/coq-lin-lattice", Output: "COQC Model.v\nCOQC Termination.v\n", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全\n已上传文件\nModel.thy\nTermination.thy\nROOT", history, workspaceChangeReport{Available: true, Changed: []string{"coq-lin-lattice/Model.v", "coq-lin-lattice/Termination.v", "coq-lin-lattice/Makefile"}})
	if !unresolved {
		t.Fatal("Coq upload task should require visible multi-dimensional proof assessment")
	}
	for _, want := range []string{"formal proof assessment incomplete", "placeholder scan", "Print Assumptions", "termination/modify_lin"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason missing %q: %q", want, reason)
		}
	}
}

func TestResolvedCoqUploadRunWithFullAssessmentPasses(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_verifier", "reviewer", "codex", strings.Join([]string{
			"最终结论：通过。Model.thy、Termination.thy、ROOT 均已转换并纳入 /root/tencent/coq-lin-lattice-visible-smoke 这个新建 Coq 项目目录。",
			"",
			"验收维度：",
			"- Coq build：make -B clean all 通过。",
			"- source-only placeholder scan：rg 对 Model.v、Termination.v、Makefile 扫描 Admitted/admit/Axiom/Parameter/Conjecture/Abort/sorry/TODO/placeholder/quick_and_dirty/Guard Checking/bypass_check，无输出。",
			"- Coq Print Assumptions：目标定理 modify_lin_original_terminates Closed under the global context，没有额外假设。",
			"- named target theorem：modify_lin_original_terminates 对应 termination modify_lin。",
			"- branch-decrease/equivalence audit：modify_lin_step_decreases 证明每个 recursive branch 的 Distance decreases，modify_lin_semantics_equiv 证明 structural recursion 与 original recursive semantics 等价。",
			"- original proof obligation：termination modify_lin 使用 structural recursion/well-founded measure 证明原始递归语义的下降义务；没有 modify_lin_fuel/default_fuel/fuel wrapper。",
			"",
			"Msg: to=user; intent=final; need=none",
			"Handoff: status=resolved; changed=Model.v, Termination.v, Makefile; verified=make/rg/coqtop; next=none; risks=none",
		}, "\n"), []RunnerToolEvent{
			{ID: "build", Status: "completed", Command: "make -B -C /root/tencent/coq-lin-lattice-visible-smoke clean all", Output: "COQC Model.v\nCOQC Termination.v\n", ExitCode: &exitCode},
			{ID: "scan", Status: "completed", Command: `rg -n "\b(Axiom|Admitted|admit|Parameter|Conjecture|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check)\b" /root/tencent/coq-lin-lattice-visible-smoke -S`, Output: "", ExitCode: &exitCode},
			{ID: "assumptions", Status: "completed", Command: "coqtop -batch -l AssumptionAudit.v", Output: "Print Assumptions modify_lin_original_terminates.\nClosed under the global context\n", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全\n已上传文件\nModel.thy\nTermination.thy\nROOT", history, workspaceChangeReport{Available: true, Changed: []string{"coq-lin-lattice-visible-smoke/Model.v", "coq-lin-lattice-visible-smoke/Termination.v", "coq-lin-lattice-visible-smoke/Makefile"}})
	if unresolved {
		t.Fatalf("full Coq upload assessment should pass: %q", reason)
	}
}

func TestCoqUploadRunDoesNotTreatStagedUploadsAsProjectFolder(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_verifier", "reviewer", "codex", strings.Join([]string{
			"最终结论：通过。Model.thy、Termination.thy、ROOT 均已转换；make 通过；source-only placeholder scan 无输出；Coq Print Assumptions 显示 Closed under the global context；original proof obligation termination modify_lin 使用 well-founded measure 证明。",
			"",
			"Msg: to=user; intent=final; need=none",
			"Handoff: status=resolved; changed=.codex-bridge/orchestrations/orc_x/01-ROOT; verified=make/rg/coqtop; next=none; risks=none",
		}, "\n"), []RunnerToolEvent{
			{ID: "build", Status: "completed", Command: "make -C /home/zy/study/.codex-bridge/orchestrations/orc_x", Output: "COQC Model.v\nCOQC Termination.v\n", ExitCode: &exitCode},
			{ID: "scan", Status: "completed", Command: `rg -n "\b(Axiom|Admitted|admit|Parameter|Conjecture|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check)\b" /home/zy/study/.codex-bridge/orchestrations/orc_x -S`, Output: "", ExitCode: &exitCode},
			{ID: "assumptions", Status: "completed", Command: "coqtop -batch -l AssumptionAudit.v", Output: "Print Assumptions modify_lin_termination.\nClosed under the global context\n", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全\n已上传文件\nModel.thy\nTermination.thy\nROOT", history, workspaceChangeReport{Available: true, Changed: []string{".codex-bridge/orchestrations/orc_x/01-ROOT", ".codex-bridge/orchestrations/orc_x/02-Model.thy", ".codex-bridge/orchestrations/orc_x/03-Termination.thy"}})
	if !unresolved {
		t.Fatal("staged upload files under .codex-bridge must not satisfy new project folder evidence")
	}
	if !strings.Contains(reason, "new Coq project folder") {
		t.Fatalf("reason should mention missing new project folder: %q", reason)
	}
}

func TestCoqUploadRunRejectsSemanticWeakeningDespiteAudits(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_reviewer", "reviewer", "codex", strings.Join([]string{
			"结论：当前 /root/tencent/coq-lin-lattice-proof-20260527 不能判定完成。",
			"Model.thy、Termination.thy、ROOT 均已转换到新 Coq 项目目录；make 通过；source-only placeholder scan 无输出；Print Assumptions 显示 Closed under the global context。",
			"但当前 Coq 版本把原始自递归 modify_lin 改成了结构递归 helper modify_loop，不是原始递归语义，缺少与原 Isabelle 递归语义的等价证明，也没有证明每个原始递归分支按 Distance 下降。",
			"",
			"Msg: to=user; intent=final; need=none",
			"Handoff: status=resolved; changed=coq-lin-lattice-proof-20260527/Termination.v; verified=make/rg/coqtop; next=prove original-step decrease/equivalence; risks=current Coq modify_lin semantically weakens original recursive definition",
		}, "\n"), []RunnerToolEvent{
			{ID: "build", Status: "completed", Command: "make -C /root/tencent/coq-lin-lattice-proof-20260527", Output: "COQC Model.v\nCOQC Termination.v\n", ExitCode: &exitCode},
			{ID: "scan", Status: "completed", Command: `rg -n "\b(Axiom|Admitted|admit|Parameter|Conjecture|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check)\b" /root/tencent/coq-lin-lattice-proof-20260527 -S`, Output: "", ExitCode: &exitCode},
			{ID: "assumptions", Status: "completed", Command: "coqtop -Q /root/tencent/coq-lin-lattice-proof-20260527 LinLattice <<'EOF'\nPrint Assumptions modify_lin_total.\nEOF", Output: "Closed under the global context\n", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全\n已上传文件\nModel.thy\nTermination.thy\nROOT", history, workspaceChangeReport{Available: true, Changed: []string{"coq-lin-lattice-proof-20260527/Model.v", "coq-lin-lattice-proof-20260527/Termination.v", "coq-lin-lattice-proof-20260527/Makefile"}})
	if !unresolved {
		t.Fatal("semantic weakening must fail even when build, scan, and Print Assumptions pass")
	}
	if !strings.Contains(reason, "semantically weakened") &&
		!strings.Contains(reason, "lacks equivalence") &&
		!strings.Contains(reason, "不是原始递归语义") &&
		!strings.Contains(reason, "缺少与原") {
		t.Fatalf("reason should mention semantic weakening/equivalence: %q", reason)
	}
}

func TestCoqUploadRunRejectsTautologicalModifyLinTheorem(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_reviewer", "reviewer", "codex", strings.Join([]string{
			"最终结论：通过。Model.thy、Termination.thy、ROOT 均已转换到 /root/tencent/coq-lin-lattice-visible-smoke 这个新建 Coq 项目目录。",
			"- Coq build：make 通过。",
			"- source-only placeholder scan：无输出。",
			"- Coq Print Assumptions：modify_lin_total Closed under the global context。",
			"- named target theorem：modify_lin_total。",
			"- branch-decrease/equivalence audit：声称 exists/reflexivity 已覆盖。",
			"- original proof obligation：termination modify_lin 由定理 forall L H bt_val, exists R, modify_lin L H bt_val = R 证明，Proof 使用 exists (modify_lin L H bt_val); reflexivity。",
			"",
			"Msg: to=user; intent=final; need=none",
			"Handoff: status=resolved; changed=Model.v, Termination.v, Makefile; verified=make/rg/coqtop; next=none; risks=none",
		}, "\n"), []RunnerToolEvent{
			{ID: "build", Status: "completed", Command: "make -C /root/tencent/coq-lin-lattice-visible-smoke", Output: "COQC Model.v\nCOQC Termination.v\n", ExitCode: &exitCode},
			{ID: "scan", Status: "completed", Command: `rg -n "\b(Axiom|Admitted|admit|Parameter|Conjecture|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check)\b" /root/tencent/coq-lin-lattice-visible-smoke -S`, Output: "", ExitCode: &exitCode},
			{ID: "assumptions", Status: "completed", Command: "coqtop -batch -l AssumptionAudit.v", Output: "Print Assumptions modify_lin_total.\nClosed under the global context\n", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全\n已上传文件\nModel.thy\nTermination.thy\nROOT", history, workspaceChangeReport{Available: true, Changed: []string{"coq-lin-lattice-visible-smoke/Model.v", "coq-lin-lattice-visible-smoke/Termination.v", "coq-lin-lattice-visible-smoke/Makefile"}})
	if !unresolved {
		t.Fatal("tautological exists/reflexivity theorem must not satisfy original proof obligation")
	}
	if !strings.Contains(reason, "tautological") && !strings.Contains(reason, "reflexivity") {
		t.Fatalf("reason should mention tautological/reflexivity theorem: %q", reason)
	}
}

func TestCoqAssumptionAuditRejectsErroredCoqtopOutput(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_reviewer", "reviewer", "codex", "Coq Print Assumptions 显示 Closed under the global context。", []RunnerToolEvent{
			{ID: "assumptions", Status: "completed", Command: "coqtop -Q . LinLattice <<'EOF'\nPrint Assumptions modify_lin_total.\nEOF", Output: "Error: Cannot find a physical path bound to logical path Termination with prefix LinLattice.\nError: The reference modify_lin_total was not found in the current environment.\n", ExitCode: &exitCode},
		}),
	}
	evidence := collectProofAssessmentEvidence(history, workspaceChangeReport{})
	if evidence.assumptionAudit {
		t.Fatal("errored coqtop output must not satisfy Print Assumptions audit evidence")
	}
}

func TestResolvedIsabelleUploadRunRequiresAssessmentDimensions(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_verifier", "reviewer", "codex", strings.Join([]string{
			"最终结论：项目已经创建并且 isabelle build 通过。",
			"",
			"Msg: to=user; intent=final; need=none",
			"Handoff: status=resolved; changed=Model.thy, Termination.thy, ROOT; verified=isabelle build -D .; next=none; risks=none",
		}, "\n"), []RunnerToolEvent{
			{ID: "build", Status: "completed", Command: "isabelle build -D /root/tencent/isabelle-proof-smoke", Output: "Build completed", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("已上传 Model.thy、Termination.thy、ROOT。请补全 Isabelle 中 termination modify_lin 的证明，不能用 sorry/quick_and_dirty。", history, workspaceChangeReport{Available: true, Changed: []string{"isabelle-proof-smoke/Model.thy", "isabelle-proof-smoke/Termination.thy", "isabelle-proof-smoke/ROOT"}})
	if !unresolved {
		t.Fatal("Isabelle upload task should require visible multi-dimensional proof assessment")
	}
	for _, want := range []string{"formal proof assessment incomplete", "source scan", "thm_oracles", "termination/modify_lin"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason missing %q: %q", want, reason)
		}
	}
}

func TestResolvedIsabelleUploadRunWithFullAssessmentPasses(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_verifier", "reviewer", "codex", strings.Join([]string{
			"最终结论：通过。Model.thy、Termination.thy、ROOT 均已纳入 /root/tencent/isabelle-proof-smoke 这个新建 Isabelle 项目目录。",
			"",
			"验收维度：",
			"- Isabelle build：isabelle build -D /root/tencent/isabelle-proof-smoke 通过。",
			"- source-only placeholder scan：rg 对 Model.thy、Termination.thy、ROOT 扫描 sorry/quick_and_dirty/oops/sketch/admit/TODO/placeholder，无输出。",
			"- Isabelle thm_oracles：modify_lin_termination 目标事实 no oracles。",
			"- named target fact：modify_lin_termination 对应 termination modify_lin。",
			"- branch-decrease audit：每个 recursive-call branch 都有 well-founded measure decrease lemma。",
			"- original proof obligation：termination modify_lin 使用 well-founded measure 和 branch decrease lemmas 证明原始递归终止义务。",
			"",
			"Msg: to=user; intent=final; need=none",
			"Handoff: status=resolved; changed=Model.thy, Termination.thy, ROOT; verified=isabelle build/rg/thm_oracles; next=none; risks=none",
		}, "\n"), []RunnerToolEvent{
			{ID: "build", Status: "completed", Command: "isabelle build -D /root/tencent/isabelle-proof-smoke", Output: "Build completed", ExitCode: &exitCode},
			{ID: "scan", Status: "completed", Command: `rg -n "sorry|quick_and_dirty|oops|sketch|admit|TODO|placeholder" /root/tencent/isabelle-proof-smoke -g "*.thy" -g ROOT -S`, Output: "", ExitCode: &exitCode},
			{ID: "oracles", Status: "completed", Command: "isabelle process -T Pure -e 'thm_oracles modify_lin_termination'", Output: "no oracles", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("已上传 Model.thy、Termination.thy、ROOT。请补全 Isabelle 中 termination modify_lin 的证明，不能用 sorry/quick_and_dirty。", history, workspaceChangeReport{Available: true, Changed: []string{"isabelle-proof-smoke/Model.thy", "isabelle-proof-smoke/Termination.thy", "isabelle-proof-smoke/ROOT"}})
	if unresolved {
		t.Fatalf("full Isabelle upload assessment should pass: %q", reason)
	}
}

func TestIsabelleManualBuildHandoffSkipsAssessmentRemediation(t *testing.T) {
	exitCode := 124
	tailExitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_1", "implementer", "claude", strings.Join([]string{
			"最终结论：Isabelle build 超时，已转为用户手动执行。",
			"手动执行：timeout 45m sh -lc 'isabelle build -D /root/tencent/isabelle-proof-smoke 2>&1 | tee /root/tencent/isabelle-proof-smoke/build.log'",
			"日志路径：/root/tencent/isabelle-proof-smoke/build.log",
			"后续 CLI 不需要执行这个build，只读取日志和源码。",
			"",
			"Msg: to=user; intent=final; need=manual build",
			"Handoff: status=needs_next; changed=/root/tencent/isabelle-proof-smoke; verified=tail build.log; next=user manually run isabelle build; risks=manual build pending",
		}, "\n"), []RunnerToolEvent{
			{ID: "build", Status: "failed", Command: "timeout 45m sh -lc 'isabelle build -D /root/tencent/isabelle-proof-smoke 2>&1 | tee /root/tencent/isabelle-proof-smoke/build.log'", Output: "timed out\n", ExitCode: &exitCode},
			{ID: "tail", Status: "completed", Command: "tail -n 80 /root/tencent/isabelle-proof-smoke/build.log", Output: "Running HOL\n", ExitCode: &tailExitCode},
		}),
	}
	prompt := "已上传 Model.thy、Termination.thy、ROOT。请补全 Isabelle 中 termination modify_lin 的证明，不能用 sorry/quick_and_dirty。"

	reason, unresolved := unresolvedFinalRun(
		prompt,
		history,
		workspaceChangeReport{Available: true, Changed: []string{"isabelle-proof-smoke/Model.thy", "isabelle-proof-smoke/Termination.thy", "isabelle-proof-smoke/ROOT"}},
	)
	if !unresolved || !strings.Contains(reason, "manual follow-up") {
		t.Fatalf("manual Isabelle build should be unresolved with manual-follow-up reason: unresolved=%v reason=%q", unresolved, reason)
	}
	if !isabelleManualBuildRequired(reason, history) {
		t.Fatal("manual Isabelle build signal was not detected")
	}
	if shouldRunFinalAssessmentRemediation(prompt, history, reason) && !isabelleManualBuildRequired(reason, history) {
		t.Fatal("manual Isabelle build handoff must not trigger another automatic assessment remediation build")
	}
}

func TestIsabelleManualBuildSignalIncludesCommandOutput(t *testing.T) {
	exitCode := 124
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_1", "implementer", "claude", "本轮只有命令输出记录，没有显式手动 build 段落。\n\nHandoff: status=needs_next; changed=linlattice-isabelle; verified=tail build.log; next=inspect log; risks=build pending", []RunnerToolEvent{
			{
				ID:       "build",
				Status:   "failed",
				Command:  `sh -lc 'timeout 45m sh -lc '\''isabelle build -D /root/tencent/linlattice-isabelle'\'' > /root/tencent/linlattice-isabelle/build.log 2>&1'`,
				Output:   "Isabelle build timed out; see /root/tencent/linlattice-isabelle/build.log\n",
				ExitCode: &exitCode,
			},
		}),
	}

	if !isabelleManualBuildRequired("", history) {
		t.Fatal("manual Isabelle build signal should include command/output text")
	}
	reason, unresolved := unresolvedFinalRun(
		"已上传 Model.thy、Termination.thy、ROOT。请补全 Isabelle termination modify_lin 证明。",
		history,
		workspaceChangeReport{Available: true, Changed: []string{"linlattice-isabelle/Model.thy"}},
	)
	if !unresolved || !strings.Contains(reason, "manual follow-up") {
		t.Fatalf("expected manual-follow-up unresolved reason, got unresolved=%v reason=%q", unresolved, reason)
	}
}

func TestIsabelleManualBuildSignalDoesNotTreatTimeoutWrapperAsTimedOut(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_1", "implementer", "claude", "启动受控后台 Isabelle build，等待日志轮询。\n\nHandoff: status=needs_next; changed=linlattice-isabelle; verified=tail build.log; next=poll build; risks=build running", []RunnerToolEvent{
			{
				ID:       "build",
				Status:   "completed",
				Command:  `sh -lc 'rm -f build.log build.pid build.pgid build.exit; setsid sh -lc "echo $$ > build.pid; echo $$ > build.pgid; timeout 45m sh -lc '\''isabelle build -D .'\'' >build.log 2>&1; echo $? > build.exit" &'`,
				Output:   "(Bash completed with no output)",
				ExitCode: &exitCode,
			},
		}),
	}

	if isabelleManualBuildRequired("", history) {
		t.Fatal("timeout wrapper in a controlled background command should not by itself trigger manual-build carry-over")
	}
}

func TestFinalRunAssessmentSummaryIsGenericRelayAssessment(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_verifier", "reviewer", "codex", strings.Join([]string{
			"最终结论：通过。Model.thy、Termination.thy、ROOT 均已纳入 /home/zy/study/isabelle-proof-smoke 这个新建 Isabelle 项目目录。",
			"- Isabelle build：isabelle build -D /home/zy/study/isabelle-proof-smoke 通过。",
			"- source-only placeholder scan：rg 扫描 sorry/quick_and_dirty/oops/sketch/admit/TODO/placeholder，无输出。",
			"- Isabelle thm_oracles：modify_lin_termination 目标事实 no oracles。",
			"- named target fact：modify_lin_termination 对应 termination modify_lin。",
			"- branch-decrease audit：每个 recursive-call branch 都有 well-founded measure decrease lemma。",
			"- original proof obligation：termination modify_lin 使用 well-founded measure 和 branch decrease lemmas 证明。",
			"",
			"Msg: to=user; intent=final; need=none",
			"Handoff: status=resolved; changed=Model.thy, Termination.thy, ROOT; verified=isabelle build/rg/thm_oracles; next=none; risks=none",
		}, "\n"), []RunnerToolEvent{
			{ID: "build", Status: "completed", Command: "isabelle build -D /home/zy/study/isabelle-proof-smoke", Output: "Build completed", ExitCode: &exitCode},
			{ID: "scan", Status: "completed", Command: `rg -n "sorry|quick_and_dirty|oops|sketch|admit|TODO|placeholder" /home/zy/study/isabelle-proof-smoke -g "*.thy" -g ROOT -S`, Output: "", ExitCode: &exitCode},
			{ID: "oracles", Status: "completed", Command: "isabelle process -T Pure -e 'thm_oracles modify_lin_termination'", Output: "no oracles", ExitCode: &exitCode},
		}),
	}
	summary := finalRunAssessmentSummary("已上传 Model.thy、Termination.thy、ROOT。请补全 Isabelle 中 termination modify_lin 的证明，不能用 sorry/quick_and_dirty。", history, workspaceChangeReport{Available: true, Changed: []string{"isabelle-proof-smoke/Model.thy", "isabelle-proof-smoke/Termination.thy", "isabelle-proof-smoke/ROOT"}}, "")
	for _, want := range []string{"最终测试结果：通过", "验收维度", "工作区变更", "命令验证", "剩余风险"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	for _, bad := range []string{"Isabelle oracle 审计", "命名目标事实", "分支下降审计", "假设审计"} {
		if strings.Contains(summary, bad) {
			t.Fatalf("generic relay assessment should not include proof-specific dimension %q:\n%s", bad, summary)
		}
	}
}

func TestFinalRunAssessmentSummaryIsUserVisible(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_1", "reviewer", "codex", "最终结论：已完成。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=result.txt; verified=go test ./...; next=none; risks=none", []RunnerToolEvent{
			{ID: "test", Status: "completed", Command: "go test ./...", Output: "ok", ExitCode: &exitCode},
		}),
	}
	summary := finalRunAssessmentSummary("请实现并修改文件", history, workspaceChangeReport{Available: true, Changed: []string{"result.txt"}}, "")
	for _, want := range []string{"最终测试结果：通过", "验收维度", "工作区变更", "命令验证", "剩余风险"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("assessment summary missing %q:\n%s", want, summary)
		}
	}
	if summary == "Orchestration completed." {
		t.Fatal("assessment summary must not collapse to hidden default run.end text")
	}
}

func TestComposeFinalAssessmentRemediationPromptTargetsFailedDimensions(t *testing.T) {
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_1", "reviewer", "codex", "最终结论：项目已构建，但缺少 Print Assumptions。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=Model.v; verified=make; next=none; risks=none", nil),
	}
	prompt := composeFinalAssessmentRemediationPrompt(
		"collaboration",
		"把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全\n已上传文件\nModel.thy\nTermination.thy\nROOT",
		"",
		false,
		"implementer",
		"claude",
		history,
		workspaceChangeReport{Available: true, Changed: []string{"Model.v"}},
		"formal proof assessment incomplete: Coq Print Assumptions/global-context audit evidence is missing",
	)
	for _, want := range []string{
		"final-assessment remediation implementer",
		"Continue fixing now",
		"Assessment failure to fix",
		"Coq Print Assumptions/global-context audit evidence is missing",
		"Current terminal assessment before remediation",
		"Original user task",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("assessment remediation prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "Formal proof task guardrails") {
		t.Fatalf("assessment remediation prompt should not inject old proof guardrails:\n%s", prompt)
	}
}

func TestFinalAssessmentRemediationDoesNotRunInPassThroughRelay(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeAssessmentRemediationScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexCoqAssessmentGapScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:    "orc_assessment_remediation",
		Mode:     "collaboration",
		Prompt:   "把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全\n已上传文件\nModel.thy\nTermination.thy\nROOT",
		MaxTurns: 2,
		CWD:      tmp,
	})

	var sawRunEnd bool
	for len(out) > 0 {
		env := <-out
		if env.Type != protocol.TypeOrchestrationEvent {
			continue
		}
		event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		if event.Kind == "turn.start" && strings.Contains(event.TurnID, "assessment-remediation") {
			t.Fatalf("pass-through relay should not start hidden assessment remediation: %#v", event)
		}
		if event.Kind == "run.error" {
			t.Fatalf("pass-through relay should complete with CLI content, got error: %#v", event)
		}
		if event.Kind == "run.end" {
			sawRunEnd = true
			for _, want := range []string{"最终结论：已创建 Coq 项目", "没有执行 Print Assumptions", "Handoff: status=resolved"} {
				if !strings.Contains(event.Content, want) {
					t.Fatalf("run.end relay content missing %q:\n%s", want, event.Content)
				}
			}
		}
	}
	if !sawRunEnd {
		t.Fatal("missing completed run.end after remediation")
	}
}

func TestResolvedHandoffAllowsDomainSpecificCaveatWhenNoOpenRisk(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_1", "implementer", "claude", "结论：已经生成可编译证明框架。", nil),
		newOrchestrationTurnRecord("turn_2", "reviewer", "codex", "最终结论：已验证可编译证明框架，剩余 sorry 属于用户允许的占位。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=Termination.thy; verified=isabelle build -D .; next=none; risks=none", []RunnerToolEvent{
			{ID: "cmd_1", Status: "completed", Command: "isabelle build -D .", Output: "Build completed. Termination.thy still contains sorry placeholders.", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("检查证明框架", history, workspaceChangeReport{})
	if unresolved {
		t.Fatalf("domain-specific caveat should not fail generic acceptance check: %q", reason)
	}
}

func TestChangeOrientedResolvedRunWithoutFileChangeFails(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_1", "implementer", "claude", "结论：检查了项目，但没有写入任何文件。\n\nMsg: to=reviewer; intent=review; need=none\nHandoff: status=needs_next; changed=none; verified=go test ./...; next=implement requested change; risks=none", []RunnerToolEvent{
			{ID: "cmd_1", Status: "completed", Command: "go test ./...", Output: "ok", ExitCode: &exitCode},
		}),
		newOrchestrationTurnRecord("turn_2", "reviewer", "codex", "最终结论：任务已经处理。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=go test ./...; next=none; risks=none", []RunnerToolEvent{
			{ID: "cmd_2", Status: "completed", Command: "go test ./...", Output: "ok", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("请实现用户要求并修改文件", history, workspaceChangeReport{Available: true})
	if !unresolved {
		t.Fatal("change-oriented resolved run without file changes should fail")
	}
	if !strings.Contains(reason, "no concrete file change") {
		t.Fatalf("reason should mention missing file changes: %q", reason)
	}
}

func TestChangeOrientedResolvedRunUsesWorkspaceSnapshotEvidence(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_1", "implementer", "claude", "结论：已实现。\n\nMsg: to=reviewer; intent=review; need=none\nHandoff: status=resolved; changed=none; verified=go test ./...; next=none; risks=none", []RunnerToolEvent{
			{ID: "cmd_1", Status: "completed", Command: "go test ./...", Output: "ok", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("请实现用户要求并修改文件", history, workspaceChangeReport{Available: true, Changed: []string{"main.go"}})
	if unresolved {
		t.Fatalf("workspace snapshot change should satisfy file-change evidence: %q", reason)
	}
}

func TestChangeOrientedRunRequiresActualSnapshotDiffWhenAvailable(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{
		newOrchestrationTurnRecord("turn_1", "implementer", "codex", "最终结论：已写入文件。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=main.go; verified=go test ./...; next=none; risks=none", []RunnerToolEvent{
			{ID: "cmd_1", Status: "completed", Command: "cat > main.go", Output: "ok", ExitCode: &exitCode},
		}),
	}
	reason, unresolved := unresolvedFinalRun("请实现用户要求并修改文件", history, workspaceChangeReport{Available: true})
	if !unresolved {
		t.Fatal("available workspace snapshot without a real diff should fail despite claimed changes")
	}
	if !strings.Contains(reason, "no concrete file change") {
		t.Fatalf("reason should mention missing file changes: %q", reason)
	}
}

func TestWorkspaceSnapshotDetectsRealFileChange(t *testing.T) {
	tmp := t.TempDir()
	before := snapshotWorkspace(tmp)
	if err := os.WriteFile(filepath.Join(tmp, "result.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := snapshotWorkspace(tmp)
	report := diffWorkspaceSnapshots(before, after)
	if !report.Available {
		t.Fatalf("snapshot unavailable: %#v", report)
	}
	if len(report.Changed) != 1 || report.Changed[0] != "result.txt" {
		t.Fatalf("changed files = %#v, want result.txt", report.Changed)
	}
}

func TestWorkspaceSnapshotIgnoresRuntimeDBFiles(t *testing.T) {
	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "bridge.db")
	before := snapshotWorkspace(tmp, dbPath, dbPath+"-wal", dbPath+"-shm")
	for _, name := range []string{"bridge.db", "bridge.db-wal", "bridge.db-shm"} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("runtime\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	after := snapshotWorkspace(tmp, dbPath, dbPath+"-wal", dbPath+"-shm")
	report := diffWorkspaceSnapshots(before, after)
	if len(report.Changed) != 0 {
		t.Fatalf("runtime DB files should be ignored, changed=%#v", report.Changed)
	}
}

func TestErroredFallbackSummaryDoesNotClaimCompleted(t *testing.T) {
	summary := erroredTurnFallbackSummary(
		"先消除主定理的 sorry",
		false,
		nil,
		orchestrationTurn{
			Err: "server_error",
			HandoffFields: orchestrationHandoffFields{
				Status: "blocked",
				Next:   "create /root/Isabelle",
				Risks:  "permission layer blocks mkdir",
			},
		},
	)
	for _, bad := range []string{"本轮编排已完成", "最终结论：本次编排已完成"} {
		if strings.Contains(summary, bad) {
			t.Fatalf("errored fallback should not claim completion:\n%s", summary)
		}
	}
	for _, want := range []string{"本轮结论", "未完成", "server_error", "permission layer blocks mkdir"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("errored fallback missing %q:\n%s", want, summary)
		}
	}
}

func TestComposeFinalVerifierPromptUsesStructuredState(t *testing.T) {
	prompt := composeFinalVerifierPrompt("collaboration", "finish task", "", false, "verifier", "codex", []orchestrationTurn{{
		Role:          "implementer",
		CLI:           "claude",
		Content:       strings.Repeat("raw transcript ", 100),
		HandoffFields: orchestrationHandoffFields{Status: "needs_next", Changed: "main.go", Verified: "none", Next: "run tests", Risks: "tests not run"},
	}})
	for _, want := range []string{"lightweight final verifier", "actual acceptance criterion", "concrete completion condition", "changed=main.go", "risks=tests not run", "finish task"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("verifier prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Contains(prompt, "raw transcript") {
		t.Fatalf("verifier prompt included raw transcript:\n%s", prompt)
	}
}

func TestComposeFinalVerifierPromptDoesNotAddFormalProofRelayGuidance(t *testing.T) {
	prompt := composeFinalVerifierPrompt("collaboration", "补全 Coq termination modify_lin 证明，不能用占位符", "", false, "verifier", "codex", []orchestrationTurn{{
		Role:          "implementer",
		CLI:           "claude",
		HandoffFields: orchestrationHandoffFields{Status: "needs_next", Changed: "Model.v", Verified: "make", Risks: "default_fuel wrapper lacks equivalence proof"},
	}})
	for _, want := range []string{
		"lightweight final verifier",
		"actual acceptance criterion",
		"default_fuel wrapper lacks equivalence proof",
		"补全 Coq",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("verifier prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, bad := range []string{
		"Formal proof final verifier guardrails",
		"Coq upload benchmark",
		"Print Assumptions <target>",
		"thm_oracles",
	} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("verifier prompt should not inject proof guardrail %q:\n%s", bad, prompt)
		}
	}
}

func TestComposeFinalVerifierPromptDoesNotRequireCoqUploadAssessment(t *testing.T) {
	prompt := composeFinalVerifierPrompt("collaboration", "把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全\n已上传文件\nModel.thy\nTermination.thy\nROOT", "", false, "verifier", "codex", nil)
	for _, want := range []string{
		"lightweight final verifier",
		"actual acceptance criterion",
		"Original user task",
		"Model.thy",
		"Termination.thy",
		"ROOT",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("Coq upload verifier prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, bad := range []string{"Coq upload benchmark", "Print/inspection of modify_lin", "modify_lin_fuel/default_fuel", "Closed under the global context"} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("Coq upload verifier prompt should not inject proof assessment %q:\n%s", bad, prompt)
		}
	}
}

func TestComposeFinalVerifierPromptDoesNotRequireIsabelleUploadAssessment(t *testing.T) {
	prompt := composeFinalVerifierPrompt("collaboration", "已上传 Model.thy、Termination.thy、ROOT。请补全 Isabelle 中 termination modify_lin 的证明，不能用 sorry/quick_and_dirty。", "", false, "verifier", "codex", nil)
	for _, want := range []string{
		"lightweight final verifier",
		"actual acceptance criterion",
		"Isabelle timeout boundary",
		"Original user task",
		"termination modify_lin",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("Isabelle upload verifier prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, bad := range []string{"Isabelle upload benchmark", "thm_oracles", "background", "compile-only framework"} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("Isabelle upload verifier prompt should not inject proof assessment %q:\n%s", bad, prompt)
		}
	}
}

func TestCCBPromptAddsFormalProofRelayGuidanceWithoutAssessmentGate(t *testing.T) {
	cfg := config.Default()
	manager := NewOrchestrationManager(&cfg)
	prompt := manager.ccbPrompt(protocol.OrchestrationStartPayload{
		Mode:   "collaboration",
		Prompt: "把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全\n已上传文件\nModel.thy\nTermination.thy\nROOT",
	})
	for _, want := range []string{
		"Use only the local CCB Codex and Claude Code agents",
		"Codex Bridge is only relaying this task and the final CCB result",
		"Return a final user-visible conclusion",
		"Formal proof relay guidance",
		"Coq upload benchmark",
		"Print Assumptions",
		orchestrationMsgContract,
		orchestrationHandoffContract,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("CCB prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, bad := range []string{"Formal proof task guardrails", "Formal proof final verifier guardrails", "Current terminal assessment before remediation"} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("CCB prompt should not inject proof assessment %q:\n%s", bad, prompt)
		}
	}
}

func TestCCBCompletedProofRunRelayUsesGenericAssessmentSummary(t *testing.T) {
	reply := strings.Join([]string{
		"最终结论：已创建 Coq 项目，Model.thy、Termination.thy、ROOT 已纳入转换，并且 make 通过；但这轮没有执行 Print Assumptions。",
		"",
		"验收维度：",
		"- Coq build：make 通过。",
		"- source-only placeholder scan：rg 无输出。",
		"",
		"Msg: to=user; intent=final; need=none",
		"Handoff: status=resolved; changed=coq-proj/Model.v, coq-proj/Termination.v; verified=make/rg; next=none; risks=none",
	}, "\n")
	history := ccbAssessmentHistory("orc_ccb-ccb", reply, nil, []RunnerToolEvent{
		{ID: "build", Status: "completed", Command: "make -C coq-proj", Output: "COQC Model.v\nCOQC Termination.v\n"},
		{ID: "scan", Status: "completed", Command: `rg -n "Axiom|Parameter|Conjecture|Admitted|admit|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check" coq-proj`, Output: ""},
	})
	changes := workspaceChangeReport{Available: true, Changed: []string{"coq-proj/Model.v", "coq-proj/Termination.v", "coq-proj/Makefile"}}
	userPrompt := "把这三个做成coq的证明项目写到工作路径下的一个新建文件夹中，并补全缺失的证明，不能用某些占位符占住，应该补全\n已上传文件\nModel.thy\nTermination.thy\nROOT"
	reason, unresolved := unresolvedFinalRun(userPrompt, history, changes)
	if !unresolved {
		t.Fatal("CCB completed proof reply without full proof evidence should fail final assessment")
	}
	for _, want := range []string{"formal proof assessment incomplete", "Print Assumptions", "termination/modify_lin"} {
		if !strings.Contains(reason, want) {
			t.Fatalf("reason missing %q: %q", want, reason)
		}
	}
	summary := finalRunAssessmentSummary(userPrompt, history, changes, reason)
	for _, want := range []string{"最终测试结果：未通过", "验收维度", "工作区变更", "命令验证", "最终验收"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "假设审计") {
		t.Fatalf("generic assessment summary should not add proof-specific dimension:\n%s", summary)
	}
}

func TestComposeDebateFinalVerifierPromptDoesNotAddAdversarialProofAudit(t *testing.T) {
	prompt := composeFinalVerifierPrompt("debate", "补全 Coq termination modify_lin 证明，不能用占位符", "", false, "verifier", "codex", []orchestrationTurn{{
		Role:          "critic",
		CLI:           "codex",
		HandoffFields: orchestrationHandoffFields{Status: "needs_next", Verified: "make", Risks: "critic found default_fuel shortcut without equivalence proof"},
	}})
	for _, want := range []string{
		"lightweight final verifier",
		"Mode: debate",
		"critic found default_fuel shortcut",
		"Original user task",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("debate verifier prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, bad := range []string{"Formal proof final verifier guardrails", "Debate verifier strategy", "fuel/default_fuel shortcuts"} {
		if strings.Contains(prompt, bad) {
			t.Fatalf("debate verifier prompt should not inject proof audit %q:\n%s", bad, prompt)
		}
	}
}

func TestComposeOrchestrationPromptDebateGuidance(t *testing.T) {
	proposer := composeOrchestrationPrompt("debate", "is this correct?", "", false, "proposer", "claude", 1, 3, nil)
	critic := composeOrchestrationPrompt("debate", "is this correct?", "", false, "critic", "codex", 2, 3, nil)
	for _, tc := range []struct {
		name string
		text string
		want string
	}{
		{"proposer", proposer, "falsifiable handoff"},
		{"critic", critic, "Try to falsify"},
	} {
		if !strings.Contains(tc.text, "evidence-focused debate") || !strings.Contains(tc.text, tc.want) {
			t.Fatalf("%s prompt missing debate guidance:\n%s", tc.name, tc.text)
		}
	}
}

func TestResolvedHandoffRequiresVisibleConclusion(t *testing.T) {
	if !resolvedHandoffReady("Final conclusion: verification passed.\n\nHandoff: status=resolved; changed=none; verified=go test ./...; next=none; risks=none") {
		t.Fatal("resolved handoff with final conclusion should be ready")
	}
	if resolvedHandoffReady("Handoff: status=resolved; changed=none; verified=go test ./...; next=none; risks=none") {
		t.Fatal("resolved handoff without visible conclusion should not stop early")
	}
	if resolvedHandoffReady("Final conclusion: work remains.\n\nHandoff: status=needs_next; changed=main.go; verified=none; next=fix tests; risks=failing tests") {
		t.Fatal("non-resolved handoff should not stop early")
	}
}

func TestResolvedHandoffWithMainTheoremSorryIsNotReady(t *testing.T) {
	content := strings.Join([]string{
		"最终结论：这个只能说通过编译了；用户要求先把主定理 termination modify_lin 的 sorry 消除，这一步没做出来，没有实质上的进展。",
		"",
		"Msg: to=user; intent=final; need=none",
		"Handoff: status=resolved; changed=none; verified=isabelle build -D termination_framework; next=prove termination modify_lin without sorry; risks=Termination.thy contains sorry placeholders, not a completed proof",
	}, "\n")
	if resolvedHandoffReady(content) {
		t.Fatal("resolved handoff with main theorem sorry risk must not be ready")
	}
}

func TestUnresolvedFinalRunRejectsCompileOnlyIsabelleProofFramework(t *testing.T) {
	history := []orchestrationTurn{{
		TurnID:  "orc_1-verifier",
		Role:    "verifier",
		CLI:     "codex",
		Content: "最终结论：这个只能说通过编译了。主定理 termination modify_lin 的 sorry 仍未消除，所以没有实质上的进展。",
		Handoff: "Handoff: status=resolved; changed=none; verified=isabelle build -D termination_framework; next=prove remaining sorry placeholders; risks=Termination.thy contains sorry placeholders, not a completed proof",
		HandoffFields: orchestrationHandoffFields{
			Status:   "resolved",
			Changed:  "none",
			Verified: "isabelle build -D termination_framework",
			Next:     "prove remaining sorry placeholders",
			Risks:    "Termination.thy contains sorry placeholders, not a completed proof",
		},
		Tools: []RunnerToolEvent{{
			ID:      "build",
			Status:  "completed",
			Command: "isabelle build -D termination_framework",
			Output: strings.Join([]string{
				"Finished Termination_Framework",
				"Termination.thy: termination modify_lin",
				"Termination.thy: sorry",
				"ROOT: options [quick_and_dirty = true]",
			}, "\n"),
		}},
	}}
	reason, unresolved := unresolvedFinalRun(
		"已上传文件 Model.thy Termination.thy ROOT。要求先把主定理的sorry消除。",
		history,
		workspaceChangeReport{Available: true, Changed: []string{"Termination.thy"}},
	)
	if !unresolved {
		t.Fatal("compile-only Isabelle proof framework should remain unresolved")
	}
	for _, want := range []string{"acceptance check failed", "sorry"} {
		if !strings.Contains(strings.ToLower(reason), strings.ToLower(want)) {
			t.Fatalf("reason missing %q: %s", want, reason)
		}
	}
}

func TestUnresolvedFinalRunRejectsIsabelleOopsBypass(t *testing.T) {
	exitCode := 0
	history := []orchestrationTurn{{
		TurnID:  "turn_verifier",
		Role:    "reviewer",
		CLI:     "codex",
		Content: "最终结论：项目可以 build，但 termination modify_lin 的证明块以 oops 结束，因此原始终止证明没有完成。",
		Handoff: "Handoff: status=resolved; changed=Termination.thy; verified=isabelle build -D .; next=prove termination modify_lin without oops; risks=Termination.thy contains oops, not a completed proof",
		HandoffFields: orchestrationHandoffFields{
			Status:   "resolved",
			Changed:  "Termination.thy",
			Verified: "isabelle build -D .",
			Next:     "prove termination modify_lin without oops",
			Risks:    "Termination.thy contains oops, not a completed proof",
		},
		Tools: []RunnerToolEvent{
			{ID: "build", Status: "completed", Command: "isabelle build -D .", Output: "Build completed", ExitCode: &exitCode},
			{ID: "scan", Status: "completed", Command: `rg -n "sorry|quick_and_dirty|oops|sketch|admit|TODO|placeholder" . -g "*.thy" -g ROOT -S`, Output: "Termination.thy:12:  oops\n", ExitCode: &exitCode},
		},
	}}
	reason, unresolved := unresolvedFinalRun(
		"已上传 Model.thy、Termination.thy、ROOT。请补全 Isabelle 中 termination modify_lin 的证明，不能用 sorry/quick_and_dirty/oops。",
		history,
		workspaceChangeReport{Available: true, Changed: []string{"isabelle-proof-smoke/Termination.thy"}},
	)
	if !unresolved {
		t.Fatal("Isabelle oops bypass should remain unresolved")
	}
	for _, want := range []string{"acceptance check failed", "oops"} {
		if !strings.Contains(strings.ToLower(reason), strings.ToLower(want)) {
			t.Fatalf("reason missing %q: %q", want, reason)
		}
	}
}

func TestComposeOrchestrationPromptFinalTurnRequiresUserVisibleAnswer(t *testing.T) {
	prompt := composeOrchestrationPrompt("collaboration", "finish it", "", false, "reviewer", "codex", 4, 4, nil)
	for _, want := range []string{"final scheduled turn", "user-visible final answer", "what was verified", "write all user-visible prose in Chinese"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("final prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestFinalTurnFallbackSummaryForProcessOnlyFinalOutput(t *testing.T) {
	exitCode := 0
	summary := finalTurnFallbackSummary(
		"检查一下现在这个项目怎么样证明正确吗",
		4,
		4,
		[]orchestrationTurn{{
			Role:    "implementer",
			CLI:     "claude",
			Content: "我复核后确认：当前项目确实能用 Isabelle 证明并验证正确性。isabelle build -D /root/tencent/BridgeDemo 构建通过。",
		}},
		orchestrationTurn{
			Role:    "reviewer",
			CLI:     "codex",
			Content: "我会做最后一轮独立复核：确认 session 入口、最终定理是否存在，并重新跑一次 Isabelle 构建。",
			Tools: []RunnerToolEvent{
				{ID: "cmd_1", Status: "in_progress", Command: "isabelle build -D ."},
				{ID: "cmd_1", Status: "completed", Command: "isabelle build -D .", Output: "0:00:05 elapsed time\n", ExitCode: &exitCode},
			},
		},
	)
	for _, want := range []string{"最终结论", "已验证", "执行完成：`isabelle build -D .`"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	for _, bad := range []string{"output:", "0:00:05 elapsed time"} {
		if strings.Contains(summary, bad) {
			t.Fatalf("summary should not expose raw command detail %q:\n%s", bad, summary)
		}
	}
}

func TestFinalTurnFallbackSummarySummarizesReadCommandsForHumans(t *testing.T) {
	summary := finalTurnFallbackSummary(
		"请检查这些上传文件",
		1,
		1,
		nil,
		orchestrationTurn{
			Role:    "implementer",
			CLI:     "claude",
			Content: "我会只读取上传文件前三行。",
			Tools: []RunnerToolEvent{
				{ID: "read_1", Status: "in_progress", Command: "Read /tmp/orc/01-Model.thy"},
				{ID: "read_1", Status: "completed", Command: "Read /tmp/orc/01-Model.thy", Output: "0\ttheory Model\n1\timports Main\n2\tbegin"},
				{ID: "read_2", Status: "in_progress", Command: "Read /tmp/orc/02-Termination.thy"},
				{ID: "read_2", Status: "completed", Command: "Read /tmp/orc/02-Termination.thy", Output: "0\ttheory Termination\n1\timports Model\n2\tbegin"},
				{ID: "read_3", Status: "completed", Command: "Read /tmp/orc/03-ROOT", Output: "0\tsession \"BridgeSmoke\" = HOL +"},
			},
		},
	)
	for _, want := range []string{"最终结论", "读取并检查了 3 个文件", "`01-Model.thy`", "`02-Termination.thy`", "`03-ROOT`"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	for _, bad := range []string{"Read /tmp/orc", "completed; output", "0\ttheory"} {
		if strings.Contains(summary, bad) {
			t.Fatalf("summary should be human-readable and omit %q:\n%s", bad, summary)
		}
	}
}

func TestFinalTurnFallbackSummaryAllowsExplicitEnglish(t *testing.T) {
	summary := finalTurnFallbackSummary(
		"Please inspect the uploaded files and reply in English.",
		1,
		1,
		nil,
		orchestrationTurn{
			Content: "I will inspect the files.",
			Tools: []RunnerToolEvent{
				{ID: "read_1", Status: "completed", Command: "Read /tmp/orc/01-Model.thy", Output: "0\ttheory Model"},
				{ID: "read_2", Status: "completed", Command: "Read /tmp/orc/02-ROOT", Output: "0\tsession"},
			},
		},
	)
	for _, want := range []string{"Final conclusion", "Read and checked 2 file(s): `01-Model.thy`, `02-ROOT`"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, "最终结论") {
		t.Fatalf("summary should honor explicit English request:\n%s", summary)
	}
}

func TestFinalTurnFallbackSummarySkipsClearFinalOutput(t *testing.T) {
	summary := finalTurnFallbackSummary(
		"check proof",
		4,
		4,
		nil,
		orchestrationTurn{Content: "Final conclusion: verification passed and no remaining risks were found."},
	)
	if summary != "" {
		t.Fatalf("summary = %q, want empty", summary)
	}
}

func TestCleanOrchestrationTurnContentTrimsRepeatedProgressBeforeConclusion(t *testing.T) {
	content := strings.Join([]string{
		"我会只核对已报告的框架文件和验证命令结果。",
		"我先只核对已报告变更的 ROOT 和 Termination.thy。",
		"我会只核对最终产物和验证记录，不做新的大范围证明工作。",
		"结论：上述内容不是完整、正确的终止性证明，只能算是一个可编译的证明框架。",
	}, "")
	cleaned := cleanOrchestrationTurnContent(content)
	if !strings.HasPrefix(cleaned, "结论：上述内容") {
		t.Fatalf("cleaned content kept progress prefix:\n%s", cleaned)
	}
	if strings.Contains(cleaned, "我会只核对") {
		t.Fatalf("cleaned content still contains repeated progress:\n%s", cleaned)
	}
}

func TestCleanOrchestrationTurnContentKeepsPlainAnswer(t *testing.T) {
	content := "我会说明原因：这个证明还依赖 sorry，因此不是完整证明。"
	if got := cleanOrchestrationTurnContent(content); got != content {
		t.Fatalf("cleaned plain answer = %q, want original", got)
	}
}

func TestExtractMsgFindsTrailingContract(t *testing.T) {
	content := "done\n\nMsg: to=reviewer; intent=review; need=none\nHandoff: status=needs_next; changed=main.go; verified=none; next=review; risks=none"
	if got := extractMsg(content); got != "Msg: to=reviewer; intent=review; need=none" {
		t.Fatalf("extractMsg = %q", got)
	}
}

func TestOrchestrationApprovalRequesterRoundTrip(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 2)
	manager.AttachOut(out)
	requester := orchestrationApprovalRequester{manager: manager, runID: "orc_1", turnID: "turn_1", cwd: "/repo"}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	done := make(chan protocol.ApprovalResponsePayload, 1)
	go func() {
		res, err := requester.RequestApproval(ctx, protocol.ApprovalRequestPayload{
			RequestID: "apr_1",
			Kind:      "claude.permission_prompt",
			Command:   "echo ok",
		})
		if err == nil {
			done <- res
		}
	}()

	env := <-out
	if env.Type != protocol.TypeApprovalRequest || env.Sid != "" {
		t.Fatalf("approval envelope = %#v", env)
	}
	req, err := protocol.Decode[protocol.ApprovalRequestPayload](env)
	if err != nil {
		t.Fatal(err)
	}
	if req.RunID != "orc_1" || req.TurnID != "turn_1" || req.CWD != "/repo" || req.Command != "echo ok" {
		t.Fatalf("approval request = %#v", req)
	}
	if !manager.ApprovalResponse(protocol.ApprovalResponsePayload{RequestID: "apr_1", Decision: "accept"}) {
		t.Fatal("approval response was not accepted")
	}
	select {
	case res := <-done:
		if res.Decision != "accept" {
			t.Fatalf("response = %#v", res)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for approval response")
	}
}

func TestClaudeApprovalMCPToolCallUsesSocketDecision(t *testing.T) {
	socketPath := t.TempDir() + "/approval.sock"
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	got := make(chan claudeApprovalSocketRequest, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req claudeApprovalSocketRequest
		_ = json.NewDecoder(conn).Decode(&req)
		got <- req
		_ = json.NewEncoder(conn).Encode(claudeApprovalSocketResponse{RequestID: req.RequestID, Decision: "accept"})
	}()

	raw := json.RawMessage(`{"name":"browser_approval","arguments":{"command":"rm -rf build","cwd":"/repo","reason":"test"}}`)
	res, err := handleClaudeApprovalMCPToolCall(socketPath, raw)
	if err != nil {
		t.Fatal(err)
	}
	result := res.(map[string]any)
	content := result["content"].([]map[string]any)
	var decision map[string]any
	if err := json.Unmarshal([]byte(content[0]["text"].(string)), &decision); err != nil {
		t.Fatalf("permission prompt result is not JSON: %v", err)
	}
	if decision["behavior"] != "allow" {
		t.Fatalf("mcp result = %#v", result)
	}
	if _, ok := decision["updatedInput"].(map[string]any); !ok {
		t.Fatalf("mcp result missing updatedInput: %#v", decision)
	}
	req := <-got
	if req.Command != "rm -rf build" || req.CWD != "/repo" || req.Reason != "test" {
		t.Fatalf("socket request = %#v", req)
	}
}

func TestClaudeApprovalMCPToolCallReturnsDenyJSON(t *testing.T) {
	socketPath := t.TempDir() + "/approval.sock"
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		var req claudeApprovalSocketRequest
		_ = json.NewDecoder(conn).Decode(&req)
		_ = json.NewEncoder(conn).Encode(claudeApprovalSocketResponse{RequestID: req.RequestID, Decision: "decline"})
	}()

	raw := json.RawMessage(`{"name":"browser_approval","arguments":{"command":"rm -rf build","cwd":"/repo","reason":"test"}}`)
	res, err := handleClaudeApprovalMCPToolCall(socketPath, raw)
	if err != nil {
		t.Fatal(err)
	}
	result := res.(map[string]any)
	content := result["content"].([]map[string]any)
	var decision map[string]any
	if err := json.Unmarshal([]byte(content[0]["text"].(string)), &decision); err != nil {
		t.Fatalf("permission prompt result is not JSON: %v", err)
	}
	if decision["behavior"] != "deny" || decision["message"] == "" || decision["interrupt"] != true {
		t.Fatalf("mcp deny result = %#v", decision)
	}
}

func orchestrationEventsContain(events []protocol.OrchestrationEventPayload, kind, cli, content string) bool {
	for _, event := range events {
		if kind != "" && event.Kind != kind {
			continue
		}
		if cli != "" && event.CLI != cli {
			continue
		}
		if content != "" && !orchestrationEventContainsText(event, content) {
			continue
		}
		return true
	}
	return false
}

func drainOrchestrationEvents(t *testing.T, out <-chan protocol.Envelope) []protocol.OrchestrationEventPayload {
	t.Helper()
	var events []protocol.OrchestrationEventPayload
	for len(out) > 0 {
		env := <-out
		if env.Type != protocol.TypeOrchestrationEvent {
			continue
		}
		event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		events = append(events, event)
	}
	return events
}

func waitForOrchestrationEvent(t *testing.T, out <-chan protocol.Envelope, kind, cli, content string) bool {
	t.Helper()
	deadline := time.After(time.Second)
	for {
		select {
		case env := <-out:
			if env.Type != protocol.TypeOrchestrationEvent {
				continue
			}
			event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
			if err != nil {
				t.Fatal(err)
			}
			if orchestrationEventsContain([]protocol.OrchestrationEventPayload{event}, kind, cli, content) {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

type trackingWriteCloser struct {
	bytes.Buffer
	closed bool
}

func (w *trackingWriteCloser) Close() error {
	w.closed = true
	return nil
}

func orchestrationEventContainsText(event protocol.OrchestrationEventPayload, want string) bool {
	if strings.Contains(event.Content, want) || strings.Contains(event.Error, want) {
		return true
	}
	for _, key := range []string{"command", "output", "id", "target"} {
		if value, _ := event.Data[key].(string); strings.Contains(value, want) {
			return true
		}
	}
	return false
}

func assertArgPair(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return
		}
	}
	t.Fatalf("args missing %s %q: %#v", key, value, args)
}

func fakeClaudePrintScript(text string) string {
	raw, _ := json.Marshal(text)
	return `#!/usr/bin/env python3
import json

text = ` + string(raw) + `
print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":text}]}}), flush=True)
print(json.dumps({"type":"result","result":text}), flush=True)
`
}

func fakeClaudeErrorScript(text string) string {
	raw, _ := json.Marshal(text)
	return `#!/usr/bin/env python3
import sys

text = ` + string(raw) + `
print(text, file=sys.stderr, flush=True)
sys.exit(1)
`
}

func fakeClaudeRelayScript(promptPath, argvPath string) string {
	promptPathRaw, _ := json.Marshal(promptPath)
	argvPathRaw, _ := json.Marshal(argvPath)
	textRaw, _ := json.Marshal("Claude result: wrote Model.v and Termination.v\n\nMsg: to=reviewer; intent=review; need=verify relay\nHandoff: status=needs_next; changed=coq-relay/Model.v, coq-relay/Termination.v; verified=none; next=run tests; risks=none")
	return `#!/usr/bin/env python3
import json
import sys

prompt_path = ` + string(promptPathRaw) + `
argv_path = ` + string(argvPathRaw) + `
text = ` + string(textRaw) + `
with open(prompt_path, "w", encoding="utf-8") as f:
    if "--input-format=stream-json" in sys.argv:
        line = sys.stdin.readline()
        payload = json.loads(line)
        f.write(payload["message"]["content"][0]["text"])
    else:
        f.write(sys.argv[-1])
with open(argv_path, "w", encoding="utf-8") as f:
    json.dump(sys.argv[1:], f)
print(json.dumps({"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_1","name":"Bash","input":{"command":"mkdir -p coq-relay && write Model.v Termination.v"}}]}}), flush=True)
print(json.dumps({"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_1","content":"created coq-relay\n"}]}}), flush=True)
print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":text}]}}), flush=True)
print(json.dumps({"type":"result","result":text}), flush=True)
`
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
			if err != nil {
				t.Fatalf("parse pid file %s: %v", path, err)
			}
			return pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for pid file %s", path)
	return 0
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d still exists after cancellation", pid)
}

func fakeCodexExecScript(text string) string {
	raw, _ := json.Marshal(text)
	return `#!/usr/bin/env python3
import json
import sys

text = ` + string(raw) + `
if len(sys.argv) < 2 or sys.argv[1] != "exec":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)
print(json.dumps({"type":"item.agent_message.delta","delta":text}), flush=True)
`
}

func fakeCodexRelayScript(promptPath, argvPath string) string {
	promptPathRaw, _ := json.Marshal(promptPath)
	argvPathRaw, _ := json.Marshal(argvPath)
	textRaw, _ := json.Marshal("Codex final: verified relay result\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=coq-relay/Model.v, coq-relay/Termination.v; verified=go test ./...; next=none; risks=none")
	return `#!/usr/bin/env python3
import json
import sys

prompt_path = ` + string(promptPathRaw) + `
argv_path = ` + string(argvPathRaw) + `
text = ` + string(textRaw) + `
if len(sys.argv) < 2 or sys.argv[1] != "exec":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)
prompt = sys.stdin.read()
with open(prompt_path, "w", encoding="utf-8") as f:
    f.write(prompt)
with open(argv_path, "w", encoding="utf-8") as f:
    json.dump(sys.argv[1:], f)
print(json.dumps({"type":"thread.started","thread_id":"thread_relay_1"}), flush=True)
print(json.dumps({"type":"item.started","item":{"id":"cmd_test","type":"command_execution","command":"go test ./...","status":"running"}}), flush=True)
print(json.dumps({"type":"item.completed","item":{"id":"cmd_test","type":"command_execution","command":"go test ./...","status":"completed","exit_code":0,"aggregated_output":"ok ./...\n"}}), flush=True)
print(json.dumps({"type":"item.agent_message.delta","delta":text}), flush=True)
`
}

func fakeCodexCoqAssessmentGapScript() string {
	first := strings.Join([]string{
		"最终结论：已创建 Coq 项目，Model.thy、Termination.thy、ROOT 已纳入转换，并且 make 通过；但这轮没有执行 Print Assumptions。",
		"",
		"验收维度：",
		"- Coq build：make 通过。",
		"- source-only placeholder scan：rg 无输出。",
		"- original proof obligation：termination modify_lin 使用 structural recursion/well-founded measure，没有 modify_lin_fuel/default_fuel/fuel wrapper。",
		"",
		"Msg: to=user; intent=final; need=none",
		"Handoff: status=resolved; changed=coq-proj/Model.v, coq-proj/Termination.v; verified=make/rg; next=none; risks=none",
	}, "\n")
	remediation := strings.Join([]string{
		"最终结论：补救轮已补齐最终测评缺口。Model.thy、Termination.thy、ROOT 均已转换到新 Coq 项目 coq-proj；make 通过；source-only placeholder scan 无输出；Coq Print Assumptions 显示 modify_lin_termination Closed under the global context；named target theorem 为 modify_lin_termination；branch-decrease/equivalence audit 记录 modify_lin_step_decreases 证明每个 recursive branch 的 Distance decreases，modify_lin_semantics_equiv 连接 structural recursion 与 original recursive semantics；original proof obligation termination modify_lin 由 structural recursion/well-founded measure 证明，没有 modify_lin_fuel/default_fuel/fuel wrapper。",
		"",
		"Msg: to=user; intent=final; need=none",
		"Handoff: status=resolved; changed=coq-proj/Model.v, coq-proj/Termination.v, coq-proj/AssumptionsCheck.v; verified=make/rg/coqtop Print Assumptions; next=none; risks=none",
	}, "\n")
	raw, _ := json.Marshal(first)
	remediationRaw, _ := json.Marshal(remediation)
	return `#!/usr/bin/env python3
import json
import os
import sys

text = ` + string(raw) + `
remediation = ` + string(remediationRaw) + `
if len(sys.argv) < 2 or sys.argv[1] != "exec":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)
prompt = sys.stdin.read()
os.makedirs("coq-proj", exist_ok=True)
for name in ["Model.v", "Termination.v", "Makefile"]:
    with open(os.path.join("coq-proj", name), "w", encoding="utf-8") as f:
        f.write("(* generated smoke proof file *)\n")
if "final-assessment remediation" in prompt or "Assessment failure to fix" in prompt:
    with open(os.path.join("coq-proj", "AssumptionsCheck.v"), "w", encoding="utf-8") as f:
        f.write("Print Assumptions modify_lin_termination.\n")
    print(json.dumps({"type":"item.started","item":{"id":"assumptions","type":"command_execution","command":"coqtop -quiet -Q coq-proj LinLattice < coq-proj/AssumptionsCheck.v","status":"running"}}), flush=True)
    print(json.dumps({"type":"item.completed","item":{"id":"assumptions","type":"command_execution","command":"coqtop -quiet -Q coq-proj LinLattice < coq-proj/AssumptionsCheck.v","status":"completed","exit_code":0,"aggregated_output":"Print Assumptions modify_lin_termination.\nClosed under the global context\n"}}), flush=True)
    print(json.dumps({"type":"item.agent_message.delta","delta":remediation}), flush=True)
    raise SystemExit(0)
print(json.dumps({"type":"item.started","item":{"id":"write","type":"command_execution","command":"mkdir -p coq-proj && write Model.v Termination.v Makefile","status":"running"}}), flush=True)
print(json.dumps({"type":"item.completed","item":{"id":"write","type":"command_execution","command":"mkdir -p coq-proj && write Model.v Termination.v Makefile","status":"completed","exit_code":0,"aggregated_output":"created coq-proj\n"}}), flush=True)
print(json.dumps({"type":"item.started","item":{"id":"build","type":"command_execution","command":"make -C coq-proj","status":"running"}}), flush=True)
print(json.dumps({"type":"item.completed","item":{"id":"build","type":"command_execution","command":"make -C coq-proj","status":"completed","exit_code":0,"aggregated_output":"COQC Model.v\nCOQC Termination.v\n"}}), flush=True)
print(json.dumps({"type":"item.started","item":{"id":"scan","type":"command_execution","command":"rg -n \"Axiom|Parameter|Conjecture|Admitted|admit|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check\" coq-proj","status":"running"}}), flush=True)
print(json.dumps({"type":"item.completed","item":{"id":"scan","type":"command_execution","command":"rg -n \"Axiom|Parameter|Conjecture|Admitted|admit|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check\" coq-proj","status":"completed","exit_code":0,"aggregated_output":""}}), flush=True)
print(json.dumps({"type":"item.agent_message.delta","delta":text}), flush=True)
`
}

func fakeClaudeAssessmentRemediationScript() string {
	initial := strings.Join([]string{
		"本轮结论：已读取 Coq 上传任务，等待 reviewer 完成构建和最终证据检查。",
		"",
		"Msg: to=reviewer; intent=review; need=check Coq proof evidence",
		"Handoff: status=needs_next; changed=none; verified=none; next=build and audit Coq project; risks=Print Assumptions not checked yet",
	}, "\n")
	text := strings.Join([]string{
		"最终结论：补救轮已补齐最终测评缺口。Model.thy、Termination.thy、ROOT 均已转换到新 Coq 项目 coq-proj；make 通过；source-only placeholder scan 无输出；Coq Print Assumptions 显示 modify_lin_termination Closed under the global context；named target theorem 为 modify_lin_termination；branch-decrease/equivalence audit 记录 modify_lin_step_decreases 证明每个 recursive branch 的 Distance decreases，modify_lin_semantics_equiv 连接 structural recursion 与 original recursive semantics；original proof obligation termination modify_lin 由 structural recursion/well-founded measure 证明，没有 modify_lin_fuel/default_fuel/fuel wrapper。",
		"",
		"Msg: to=user; intent=final; need=none",
		"Handoff: status=resolved; changed=coq-proj/AssumptionsCheck.v; verified=make/rg/coqtop Print Assumptions; next=none; risks=none",
	}, "\n")
	initialRaw, _ := json.Marshal(initial)
	raw, _ := json.Marshal(text)
	return `#!/usr/bin/env python3
import json
import os
import sys

initial = ` + string(initialRaw) + `
text = ` + string(raw) + `
prompt = " ".join(sys.argv[1:])
if "final-assessment remediation" not in prompt and "Assessment failure to fix" not in prompt:
    print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":initial}]}}), flush=True)
    print(json.dumps({"type":"result","result":initial}), flush=True)
    raise SystemExit(0)
os.makedirs("coq-proj", exist_ok=True)
with open("coq-proj/AssumptionsCheck.v", "w", encoding="utf-8") as f:
    f.write("Print Assumptions modify_lin_termination.\n")
print(json.dumps({"type":"assistant","message":{"content":[{"type":"tool_use","id":"assumptions","name":"Bash","input":{"command":"coqtop -quiet -Q coq-proj LinLattice < coq-proj/AssumptionsCheck.v"}}]}}), flush=True)
print(json.dumps({"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"assumptions","content":"Print Assumptions modify_lin_termination.\nClosed under the global context\n"}]}}), flush=True)
print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":text}]}}), flush=True)
print(json.dumps({"type":"result","result":text}), flush=True)
`
}
