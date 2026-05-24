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
		}
		if event.Kind == "command.end" && event.Status == "completed" && event.CLI == "claude" && event.Data["output"] == "created\n" {
			sawEnd = true
		}
	}
	if !sawStart || !sawEnd {
		t.Fatalf("missing tool events: sawStart=%v sawEnd=%v events=%#v", sawStart, sawEnd, events)
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
	if !strings.Contains(prompt, "Do not call PDF-reading tools with empty pages values") {
		t.Fatalf("prompt missing PDF pages guard:\n%s", prompt)
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

func TestComposeFinalVerifierPromptUsesStructuredState(t *testing.T) {
	prompt := composeFinalVerifierPrompt("collaboration", "finish task", "", false, "verifier", "codex", []orchestrationTurn{{
		Role:          "implementer",
		CLI:           "claude",
		Content:       strings.Repeat("raw transcript ", 100),
		HandoffFields: orchestrationHandoffFields{Status: "needs_next", Changed: "main.go", Verified: "none", Next: "run tests", Risks: "tests not run"},
	}})
	for _, want := range []string{"lightweight final verifier", "changed=main.go", "risks=tests not run", "finish task"} {
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

func TestComposeOrchestrationPromptFinalTurnRequiresUserVisibleAnswer(t *testing.T) {
	prompt := composeOrchestrationPrompt("collaboration", "finish it", "", false, "reviewer", "codex", 4, 4, nil)
	for _, want := range []string{"final scheduled turn", "user-visible final answer", "what was verified"} {
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
	for _, want := range []string{"最终结论", "已验证", "isabelle build -D .", "0:00:05 elapsed time"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q:\n%s", want, summary)
		}
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
