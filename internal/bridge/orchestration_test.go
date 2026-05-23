package bridge

import (
	"strings"
	"testing"

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

func assertArgPair(t *testing.T, args []string, key, value string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == key && args[i+1] == value {
			return
		}
	}
	t.Fatalf("args missing %s %q: %#v", key, value, args)
}
