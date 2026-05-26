package bridge

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	lines := sanitizeCCBConsoleLines("\x1b[31mOPENAI_API_KEY=sk-test\nAuthorization: Bearer abc.def\nplain\x1b[0m\n", 10)
	got := strings.Join(lines, "\n")
	if strings.Contains(got, "\x1b") || strings.Contains(got, "sk-test") || strings.Contains(got, "abc.def") {
		t.Fatalf("console was not sanitized:\n%s", got)
	}
	if !strings.Contains(got, "OPENAI_API_KEY=[REDACTED]") || !strings.Contains(got, "[REDACTED]") || !strings.Contains(got, "plain") {
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

func TestOrchestrationResolvedMachineOnlyTurnGetsReadableConclusion(t *testing.T) {
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

	var sawReadableConclusion bool
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
			if !strings.Contains(event.Content, "最终结论") {
				t.Fatalf("machine-only turn.end did not get readable conclusion: %#v", event)
			}
			if !strings.Contains(event.Content, "Msg: to=user") || !strings.Contains(event.Content, "Handoff: status=resolved") {
				t.Fatalf("machine contract lines were not preserved: %#v", event)
			}
			sawReadableConclusion = true
		}
	}
	if !sawReadableConclusion {
		t.Fatal("did not see readable turn.end conclusion")
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

func TestPrepareOrchestrationPromptFilesWarnsAgainstEmptyPDFPages(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CWD = t.TempDir()
	prompt, metas, err := PrepareOrchestrationPromptFiles(&cfg, "orc_pdf", "read this", []protocol.AttachmentPayload{{
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
	for _, want := range []string{
		"inspect them with shell commands",
		"do not use Claude's Read tool",
		"Do not send an empty pages field to any file-reading tool",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestFinalVerifierRunsOnlyWhenRiskSignalsExist(t *testing.T) {
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
	if !manager.shouldRunFinalVerifier(resolvedChanged) {
		t.Fatal("resolved file changes should still trigger final verifier")
	}
	changed := []orchestrationTurn{{
		HandoffFields: orchestrationHandoffFields{Status: "needs_next", Changed: "main.go"},
	}}
	if !manager.shouldRunFinalVerifier(changed) {
		t.Fatal("changed files should trigger final verifier")
	}
	exitCode := 1
	failed := []orchestrationTurn{{
		Tools: []RunnerToolEvent{{Command: "go test ./...", Status: "completed", ExitCode: &exitCode}},
	}}
	if !manager.shouldRunFinalVerifier(failed) {
		t.Fatal("failed command should trigger final verifier")
	}
}

func TestRepeatedBlockedHandoffStopsRunAsFailed(t *testing.T) {
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

	var runError protocol.OrchestrationEventPayload
	turnStarts := 0
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
			runError = event
		}
	}
	if runError.Kind != "run.error" || !strings.Contains(runError.Error, "repeated blocker") {
		t.Fatalf("missing repeated-blocker run.error: %#v", runError)
	}
	if turnStarts >= 6 {
		t.Fatalf("run should stop before exhausting all turns, saw %d starts", turnStarts)
	}
}

func TestUnresolvedFinalHandoffFailsRun(t *testing.T) {
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

	var sawRunError bool
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
			t.Fatalf("unresolved final handoff should not complete run: %#v", event)
		}
		if event.Kind == "run.error" {
			sawRunError = true
			if !strings.Contains(event.Error, "主定理 sorry") {
				t.Fatalf("run.error lost unresolved goal: %#v", event)
			}
		}
	}
	if !sawRunError {
		t.Fatal("missing run.error for unresolved final handoff")
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
