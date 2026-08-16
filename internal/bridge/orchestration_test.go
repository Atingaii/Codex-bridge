package bridge

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func cleanOrchestrationTurnContent(content string) string {
	return scrubOrchestrationTurnContent(content)
}

func containsTestString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestOrchestrationClaudeStreamInputArgsKeepSessionAndOmitPromptArg(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	args := manager.claudeArgsWithStreamInput(protocol.OrchestrationStartPayload{CWD: "/repo"}, "11111111-1111-5111-8111-111111111111", false)
	for _, want := range []string{"--input-format=stream-json", "--output-format=stream-json", "--verbose", "--session-id", "11111111-1111-5111-8111-111111111111"} {
		if !containsArg(args, want) {
			t.Fatalf("stream claude args missing %q: %#v", want, args)
		}
	}
	if containsArg(args, "task") {
		t.Fatalf("stream claude args should not append prompt as argv: %#v", args)
	}
}

func TestOrchestrationClaudeArgsUseConfiguredModel(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.ClaudeModel = "deepseek-v4-flash"
	manager := NewOrchestrationManager(&cfg)

	for _, args := range map[string][]string{
		"print":  manager.claudeArgsWithSession(protocol.OrchestrationStartPayload{}, "task", "", false),
		"stream": manager.claudeArgsWithStreamInput(protocol.OrchestrationStartPayload{}, "", false),
	} {
		assertArgPair(t, args, "--model", "deepseek-v4-flash")
	}
	if got := claudeBridgeModel(&cfg); got != "deepseek-v4-flash" {
		t.Fatalf("usage model = %q, want provider model", got)
	}
}

func TestOrchestrationClaudeAutoExecuteUsesBypassPermissions(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)

	printArgs := manager.claudeArgsWithSession(protocol.OrchestrationStartPayload{CWD: "/repo"}, "task", "11111111-1111-5111-8111-111111111111", false)
	streamArgs := manager.claudeArgsWithStreamInput(protocol.OrchestrationStartPayload{CWD: "/repo"}, "11111111-1111-5111-8111-111111111111", false)
	for name, args := range map[string][]string{"print": printArgs, "stream": streamArgs} {
		assertArgPair(t, args, "--permission-mode", "bypassPermissions")
		if containsArg(args, "acceptEdits") {
			t.Fatalf("%s claude args should not downgrade auto-execute to acceptEdits: %#v", name, args)
		}
	}
	if manager.shouldBridgeClaudeApproval() {
		t.Fatalf("auto-execute mode should not attach browser approval MCP")
	}
}

func TestOrchestrationClaudeReviewRequiredKeepsBrowserApproval(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"
	manager := NewOrchestrationManager(&cfg)

	args := manager.claudeArgsWithStreamInput(protocol.OrchestrationStartPayload{CWD: "/repo"}, "11111111-1111-5111-8111-111111111111", false)
	if containsArg(args, "--permission-mode") {
		t.Fatalf("review-required base args should leave permission mode to approval MCP helper: %#v", args)
	}
	if !manager.shouldBridgeClaudeApproval() {
		t.Fatalf("review-required mode should attach browser approval MCP")
	}
	args = manager.withClaudeStreamApprovalArgs(args, "/tmp/codex-bridge-mcp.json")
	assertArgPair(t, args, "--permission-mode", "default")
	assertArgPair(t, args, "--mcp-config", "/tmp/codex-bridge-mcp.json")
}

func TestAppendCommandEnvInheritsEnvironmentBeforeAppending(t *testing.T) {
	t.Setenv("CODEX_BRIDGE_ENV_TEST", "parent")
	cmd := exec.Command("true")

	appendCommandEnv(cmd, "IS_SANDBOX=1", "CODEX_BRIDGE_ENV_TEST=child")

	if !containsArg(cmd.Env, "PATH="+os.Getenv("PATH")) {
		t.Fatalf("command env did not inherit PATH: %#v", cmd.Env)
	}
	if !containsArg(cmd.Env, "CODEX_BRIDGE_ENV_TEST=parent") {
		t.Fatalf("command env did not inherit parent variable: %#v", cmd.Env)
	}
	if !containsArg(cmd.Env, "CODEX_BRIDGE_ENV_TEST=child") {
		t.Fatalf("command env did not append override variable: %#v", cmd.Env)
	}
	if !containsArg(cmd.Env, "IS_SANDBOX=1") {
		t.Fatalf("command env did not append sandbox marker: %#v", cmd.Env)
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

func TestOrchestrationClaudeStreamApprovalArgsAppendWithoutPrompt(t *testing.T) {
	args := NewOrchestrationManager(&config.Config{}).withClaudeStreamApprovalArgs(
		[]string{"--print", "--input-format=stream-json", "--output-format=stream-json", "--name", "Bridge"},
		"/tmp/codex-bridge-mcp.json",
	)
	assertArgPair(t, args, "--name", "Bridge")
	assertArgPair(t, args, "--mcp-config", "/tmp/codex-bridge-mcp.json")
	if args[len(args)-1] != "mcp__codex_bridge__browser_approval" {
		t.Fatalf("stream approval args should append after --name value: %#v", args)
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
	if !orchestrationEventsContain(events, "turn.start", "claude", "Starting Claude") {
		t.Fatalf("Claude turn start was not frontend-visible: %#v", events)
	}
	for _, event := range events {
		if event.Kind == "turn.start" && (strings.Contains(event.Content, task) || strings.Contains(event.Content, "Prompt sent to")) {
			t.Fatalf("turn.start leaked internal relay prompt: %#v", event)
		}
	}
	if !orchestrationEventsContain(events, "command.start", "claude", "mkdir -p coq-relay") ||
		!orchestrationEventsContain(events, "command.end", "codex", "go test ./...") {
		t.Fatalf("command events were not frontend-visible: %#v", events)
	}
	if !orchestrationEventsContain(events, "turn.start", "codex", "Starting Codex") {
		t.Fatalf("Codex turn start was not frontend-visible: %#v", events)
	}
	if !orchestrationEventsContain(events, "run.conclusion", "", "Codex final: verified relay result") {
		t.Fatalf("run.conclusion did not relay final structured conclusion: %#v", events)
	}
	if !orchestrationEventsContain(events, "run.end", "", "Codex final: verified relay result") {
		t.Fatalf("run.end did not relay final Codex content: %#v", events)
	}
	for _, event := range events {
		if event.Kind == "turn.start" && (event.TurnStartData == nil || event.TurnStartData.StartedAt <= 0) {
			t.Fatalf("turn.start missing timing: %#v", event)
		}
		if event.Kind == "turn.end" && (event.TurnEndData == nil || event.TurnEndData.StartedAt <= 0 || event.TurnEndData.CompletedAt < event.TurnEndData.StartedAt || event.TurnEndData.DurationMs < 0) {
			t.Fatalf("turn.end missing valid timing: %#v", event)
		}
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
	if !strings.Contains(string(codexPrompt), "state: status=needs_next") ||
		!strings.Contains(string(codexPrompt), "changed=coq-relay/Model.v, coq-relay/Termination.v") ||
		!strings.Contains(string(codexPrompt), "mkdir -p coq-relay") {
		t.Fatalf("Codex stdin missing compact Claude handoff context:\n%s", codexPrompt)
	}
	if strings.Contains(string(codexPrompt), "Claude result: wrote Model.v") {
		t.Fatalf("Codex stdin repeated structured Claude prose instead of compact state:\n%s", codexPrompt)
	}
	claudeArgv, err := os.ReadFile(claudeArgvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claudeArgv), "--session-id") || !strings.Contains(string(claudeArgv), "--input-format=stream-json") {
		t.Fatalf("Claude was not started with stable session id: %s", claudeArgv)
	}
	codexArgv, err := os.ReadFile(codexArgvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codexArgv), "app-server") || !strings.Contains(string(codexArgv), "--listen") {
		t.Fatalf("Codex initial args did not use app-server: %s", codexArgv)
	}
}

func TestOrchestrationResumeRestoresCLIStateAndLockedCWD(t *testing.T) {
	tmp := t.TempDir()
	runCWD := filepath.Join(tmp, "locked")
	if err := os.MkdirAll(runCWD, 0o755); err != nil {
		t.Fatal(err)
	}
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	claudeArgvPath := filepath.Join(tmp, "claude_argv.json")
	codexArgvPath := filepath.Join(tmp, "codex_argv.json")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeRelayScript(filepath.Join(tmp, "claude_prompt.txt"), claudeArgvPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexRelayScript(filepath.Join(tmp, "codex_prompt.txt"), codexArgvPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = filepath.Join(tmp, "ignored")
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	claudeSessionID := stableOrchestrationSessionID("orc_resume", "claude")
	transcriptPath := claudeSessionFilePath(runCWD, claudeSessionID)
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcriptPath, []byte(`{"type":"user","sessionId":"`+claudeSessionID+`","entrypoint":"cli"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:         "orc_resume",
		Mode:          "collaboration",
		Prompt:        "continue",
		Resume:        true,
		MaxTurns:      2,
		RunCWD:        runCWD,
		CodexThreadID: "thread_saved",
		ClaudeStarted: true,
	})

	events := drainOrchestrationEvents(t, out)
	var runStart protocol.OrchestrationEventPayload
	var codexEnd protocol.OrchestrationEventPayload
	for _, event := range events {
		if event.Kind == "run.start" {
			runStart = event
		}
		if event.Kind == "turn.end" && event.CLI == "codex" {
			codexEnd = event
		}
	}
	if got := stringMapValue(runStart.Data, "cwd"); got != runCWD {
		t.Fatalf("run.start cwd = %q, want %q", got, runCWD)
	}
	if got := stringMapValue(codexEnd.Data, "codexThreadId"); got == "" {
		t.Fatalf("codex turn.end missing thread id: %#v", codexEnd)
	}

	claudeArgv, err := os.ReadFile(claudeArgvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claudeArgv), "--resume") || strings.Contains(string(claudeArgv), "--session-id") || !strings.Contains(string(claudeArgv), runCWD) {
		t.Fatalf("Claude resume args incorrect: %s", claudeArgv)
	}
	codexArgv, err := os.ReadFile(codexArgvPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(codexArgv), "app-server") {
		t.Fatalf("Codex resume should use app-server args: %s", codexArgv)
	}
}

func TestOrchestrationReusesNativeInteractiveSessionsAcrossSameCLITurns(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	claudeLogPath := filepath.Join(tmp, "claude_log.jsonl")
	codexLogPath := filepath.Join(tmp, "codex_log.jsonl")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeInteractiveRelayScript(claudeLogPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexInteractiveRelayScript(codexLogPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 256)
	manager.AttachOut(out)
	defer manager.CloseAll()

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:    "orc_native_reuse",
		Mode:     "collaboration",
		Prompt:   "finish native session reuse",
		MaxTurns: 4,
		CWD:      tmp,
	})

	events := drainOrchestrationEvents(t, out)
	var codexThreadIDs []string
	var claudeSessionIDs []string
	var codexStartModes []string
	var claudeStartModes []string
	for _, event := range events {
		if event.Kind == "turn.start" {
			switch event.CLI {
			case "codex":
				codexStartModes = append(codexStartModes, stringMapValue(event.Data, "resumeMode"))
			case "claude":
				claudeStartModes = append(claudeStartModes, stringMapValue(event.Data, "resumeMode"))
			}
		}
		if event.Kind != "turn.end" {
			continue
		}
		switch event.CLI {
		case "codex":
			codexThreadIDs = append(codexThreadIDs, stringMapValue(event.Data, "codexThreadId"))
		case "claude":
			claudeSessionIDs = append(claudeSessionIDs, stringMapValue(event.Data, "sessionId"))
		}
	}
	if len(codexThreadIDs) != 2 || codexThreadIDs[0] != "thr_native" || codexThreadIDs[1] != "thr_native" {
		t.Fatalf("codex thread ids = %#v", codexThreadIDs)
	}
	wantClaudeSessionID := stableOrchestrationSessionID("orc_native_reuse", "claude")
	if len(claudeSessionIDs) != 2 || claudeSessionIDs[0] != wantClaudeSessionID || claudeSessionIDs[1] != wantClaudeSessionID {
		t.Fatalf("claude session ids = %#v, want %q", claudeSessionIDs, wantClaudeSessionID)
	}
	if got := strings.Join(codexStartModes, ","); got != "codex-interactive-thread,codex-interactive-resume" {
		t.Fatalf("codex turn.start modes = %q", got)
	}
	if got := strings.Join(claudeStartModes, ","); got != "claude-interactive-session,claude-interactive-session" {
		t.Fatalf("claude turn.start modes = %q", got)
	}

	codexRecords := waitForJSONLineEventCount(t, codexLogPath, "thread_unsubscribe", 2)
	var codexStarts, codexTurns, codexNames, codexResumes, codexUnsubscribes int
	var codexPrompts []string
	for _, record := range codexRecords {
		switch record["event"] {
		case "process_start":
			codexStarts++
		case "thread_start":
			codexStartsForThread, _ := record["threadId"].(string)
			if codexStartsForThread != "thr_native" {
				t.Fatalf("codex thread_start id = %#v", record)
			}
		case "thread_name":
			codexNames++
			if got, _ := record["name"].(string); got != nativeSessionDisplayName("orc_native_reuse", "codex") {
				t.Fatalf("codex native name = %q", got)
			}
		case "thread_resume":
			codexResumes++
			if got, _ := record["threadId"].(string); got != "thr_native" {
				t.Fatalf("codex thread_resume id = %#v", record)
			}
		case "thread_unsubscribe":
			codexUnsubscribes++
			if got, _ := record["threadId"].(string); got != "thr_native" {
				t.Fatalf("codex thread_unsubscribe id = %#v", record)
			}
		case "turn_start":
			codexTurns++
			codexPrompts = append(codexPrompts, stringFromNestedText(record["params"]))
			if got, _ := record["threadId"].(string); got != "thr_native" {
				t.Fatalf("codex turn thread = %#v", record)
			}
		}
	}
	if codexStarts != 1 || codexTurns != 2 || codexNames != 1 || codexResumes != 1 || codexUnsubscribes != 2 {
		t.Fatalf("codex log starts=%d turns=%d names=%d resumes=%d unsubscribes=%d records=%#v", codexStarts, codexTurns, codexNames, codexResumes, codexUnsubscribes, codexRecords)
	}
	if len(codexPrompts) != 2 || !strings.Contains(codexPrompts[1], "same native codex conversation") {
		t.Fatalf("second codex prompt missing same-native notice: %#v", codexPrompts)
	}

	claudeRecords := readJSONLines(t, claudeLogPath)
	var claudeStarts, claudeMessages int
	var claudePrompts []string
	for _, record := range claudeRecords {
		switch record["event"] {
		case "process_start":
			claudeStarts++
			args, _ := record["argv"].([]any)
			if !sliceContainsArgPrefix(args, "--input-format") || !sliceContainsString(args, "--session-id") {
				t.Fatalf("claude process args missing stream input or session: %#v", record)
			}
		case "user_message":
			claudeMessages++
			if got, _ := record["sessionId"].(string); got != wantClaudeSessionID {
				t.Fatalf("claude message session = %#v", record)
			}
			if prompt, _ := record["prompt"].(string); prompt != "" {
				claudePrompts = append(claudePrompts, prompt)
			}
		}
	}
	if claudeStarts != 1 || claudeMessages != 2 {
		t.Fatalf("claude log starts=%d messages=%d records=%#v", claudeStarts, claudeMessages, claudeRecords)
	}
	if len(claudePrompts) != 2 || !strings.Contains(claudePrompts[1], "same native claude conversation") {
		t.Fatalf("second claude prompt missing same-native notice: %#v", claudePrompts)
	}
}

func TestCodexCodexOrchestrationUsesIndependentCodexSlots(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	codexLogPath := filepath.Join(tmp, "codex_log.jsonl")
	if err := os.WriteFile(codexPath, []byte(fakeCodexInteractiveRelayScriptWithPIDThreads(codexLogPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 256)
	manager.AttachOut(out)
	defer manager.CloseAll()

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:      "orc_codex_codex",
		Mode:       "collaboration",
		WorkerPair: protocol.WorkerPairCodexCodex,
		Prompt:     "finish codex codex worker pair",
		MaxTurns:   4,
		CWD:        tmp,
	})

	events := drainOrchestrationEvents(t, out)
	var runStart protocol.OrchestrationEventPayload
	var runEnd protocol.OrchestrationEventPayload
	var turnSlots []string
	turnThreadIDs := map[string]string{}
	for _, event := range events {
		switch event.Kind {
		case "run.start":
			runStart = event
		case "turn.start":
			if event.TurnStartData != nil {
				turnSlots = append(turnSlots, event.TurnStartData.WorkerSlot)
			}
		case "turn.end":
			if event.CLI == "codex" {
				turnThreadIDs[stringMapValue(event.Data, "workerSlot")] = stringMapValue(event.Data, "codexThreadId")
			}
		case "run.end":
			runEnd = event
		}
	}
	if runStart.RunStartData == nil || runStart.RunStartData.WorkerPair != protocol.WorkerPairCodexCodex || runStart.RunStartData.FirstCLI != "codex" {
		t.Fatalf("run.start worker pair/first cli = %#v", runStart)
	}
	if got := strings.Join(turnSlots, ","); got != "codex-a,codex-b,codex-a,codex-b" {
		t.Fatalf("turn worker slots = %q", got)
	}
	threadA := turnThreadIDs[orchestrationCodexSlotA]
	threadB := turnThreadIDs[orchestrationCodexSlotB]
	if threadA == "" || threadB == "" || threadA == threadB {
		t.Fatalf("codex slot thread ids = %#v", turnThreadIDs)
	}
	if runEnd.RunEndData == nil || runEnd.RunEndData.CodexThreadIDs[orchestrationCodexSlotA] != threadA || runEnd.RunEndData.CodexThreadIDs[orchestrationCodexSlotB] != threadB {
		t.Fatalf("run.end codex thread map = %#v, want %s/%s", runEnd.RunEndData, threadA, threadB)
	}
	if len(runEnd.RunEndData.NativeResume) < 2 {
		t.Fatalf("run.end native resume missing codex slots: %#v", runEnd.RunEndData.NativeResume)
	}

	records := waitForJSONLineEventCount(t, codexLogPath, "thread_resume", 2)
	var starts, turns, resumes int
	threadNames := map[string]string{}
	threadTurns := map[string]int{}
	for _, record := range records {
		switch record["event"] {
		case "thread_start":
			starts++
		case "thread_name":
			threadID, _ := record["threadId"].(string)
			name, _ := record["name"].(string)
			threadNames[threadID] = name
		case "thread_resume":
			resumes++
		case "turn_start":
			turns++
			threadID, _ := record["threadId"].(string)
			threadTurns[threadID]++
		}
	}
	if starts != 2 || turns != 4 || resumes != 2 {
		t.Fatalf("codex-codex starts=%d turns=%d resumes=%d records=%#v", starts, turns, resumes, records)
	}
	if threadNames[threadA] != nativeSessionDisplayName("orc_codex_codex", orchestrationCodexSlotA) || threadNames[threadB] != nativeSessionDisplayName("orc_codex_codex", orchestrationCodexSlotB) {
		t.Fatalf("codex thread names = %#v", threadNames)
	}
	if threadTurns[threadA] != 2 || threadTurns[threadB] != 2 {
		t.Fatalf("codex turns by thread = %#v", threadTurns)
	}
}

func TestOrchestrationNativeContextCompactionAfterTurn(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	claudeLogPath := filepath.Join(tmp, "claude_log.jsonl")
	codexLogPath := filepath.Join(tmp, "codex_log.jsonl")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeInteractiveRelayScript(claudeLogPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexInteractiveRelayScript(codexLogPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 256)
	manager.AttachOut(out)
	defer manager.CloseAll()

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:                   "orc_native_compact",
		Mode:                    "collaboration",
		Prompt:                  "finish native context maintenance",
		MaxTurns:                2,
		CWD:                     tmp,
		NativeContextCompaction: protocol.NativeContextCompactionAfterTurn,
	})

	events := drainOrchestrationEvents(t, out)
	var compactionNotes int
	turnEndIndex := map[string]int{}
	firstCompactionIndex := map[string]int{}
	var finalContent string
	for index, event := range events {
		if event.Kind == "run.start" && event.RunStartData != nil && event.RunStartData.NativeContextCompaction != protocol.NativeContextCompactionAfterTurn {
			t.Fatalf("run.start native compaction = %q", event.RunStartData.NativeContextCompaction)
		}
		if event.Kind == "turn.end" && event.TurnID != "" {
			turnEndIndex[event.TurnID] = index
		}
		if event.BridgeNoteData != nil && event.BridgeNoteData.Category == "native-context-compaction" {
			compactionNotes++
			if event.BridgeNoteData.Command != nativeContextCompactionCommand {
				t.Fatalf("compaction note command = %#v", event.BridgeNoteData)
			}
			if _, ok := firstCompactionIndex[event.TurnID]; !ok {
				firstCompactionIndex[event.TurnID] = index
			}
		}
		if event.Kind == "run.end" {
			finalContent = event.Content
		}
	}
	if compactionNotes != 2 {
		t.Fatalf("native compaction notes = %d, want 2 visible middle-turn notes events=%#v", compactionNotes, events)
	}
	for turnID, compactionIndex := range firstCompactionIndex {
		turnIndex, ok := turnEndIndex[turnID]
		if !ok || turnIndex > compactionIndex {
			t.Fatalf("native compaction should be visible only after business turn.end for %s: turnEndIndex=%d compactionIndex=%d events=%#v", turnID, turnIndex, compactionIndex, events)
		}
	}
	if strings.Contains(finalContent, "compacted") || strings.Contains(finalContent, nativeContextCompactionCommand) {
		t.Fatalf("maintenance output leaked into final content: %q", finalContent)
	}

	codexRecords := waitForJSONLineEvent(t, codexLogPath, "thread_compact")
	var codexBusinessTurns, codexMaintenanceTurns int
	for _, record := range codexRecords {
		switch record["event"] {
		case "turn_start":
			codexBusinessTurns++
		case "thread_compact":
			codexMaintenanceTurns++
		}
	}
	if codexBusinessTurns != 1 || codexMaintenanceTurns != 1 {
		t.Fatalf("codex business=%d maintenance=%d records=%#v", codexBusinessTurns, codexMaintenanceTurns, codexRecords)
	}

	claudeRecords := readJSONLines(t, claudeLogPath)
	var claudeBusinessTurns, claudeMaintenanceTurns int
	for _, record := range claudeRecords {
		if record["event"] != "user_message" {
			continue
		}
		prompt, _ := record["prompt"].(string)
		if prompt == nativeContextCompactionCommand {
			claudeMaintenanceTurns++
		} else {
			claudeBusinessTurns++
		}
	}
	if claudeBusinessTurns != 1 || claudeMaintenanceTurns != 0 {
		t.Fatalf("claude business=%d maintenance=%d records=%#v", claudeBusinessTurns, claudeMaintenanceTurns, claudeRecords)
	}
}

func TestOrchestrationNativeContextCompactionFailureIsWarningOnly(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	claudeLogPath := filepath.Join(tmp, "claude_log.jsonl")
	codexLogPath := filepath.Join(tmp, "codex_log.jsonl")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeInteractiveRelayScript(claudeLogPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexInteractiveRelayScriptWithCompactFailure(codexLogPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 256)
	manager.AttachOut(out)
	defer manager.CloseAll()

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:                   "orc_native_compact_warn",
		Mode:                    "collaboration",
		FirstCLI:                "codex",
		Prompt:                  "finish despite maintenance warning",
		MaxTurns:                2,
		CWD:                     tmp,
		NativeContextCompaction: protocol.NativeContextCompactionAfterTurn,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "run.end", "", "Claude native") {
		t.Fatalf("run did not complete after compaction failure: %#v", events)
	}
	var warnings int
	for _, event := range events {
		if event.BridgeNoteData != nil && event.BridgeNoteData.Category == "native-context-compaction" && event.Severity == "warning" {
			warnings++
			if !strings.Contains(event.Error, "compact failed") {
				t.Fatalf("warning did not expose compaction error: %#v", event)
			}
		}
	}
	if warnings != 1 {
		t.Fatalf("compaction warning count = %d events=%#v", warnings, events)
	}
}

func TestOrchestrationFinalTurnEndAndRunEndDoNotWaitForVisibleCompaction(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	codexPath := filepath.Join(tmp, "codex")
	codexLogPath := filepath.Join(tmp, "codex_log.jsonl")
	if err := os.WriteFile(codexPath, []byte(fakeCodexInteractiveRelayScriptWithHangingCompact(codexLogPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)
	defer manager.CloseAll()

	done := make(chan struct{})
	go func() {
		manager.run(context.Background(), protocol.OrchestrationStartPayload{
			RunID:                   "orc_final_compact_hang",
			Mode:                    "collaboration",
			FirstCLI:                "codex",
			Prompt:                  "finish without tail clipping",
			MaxTurns:                1,
			CWD:                     tmp,
			NativeContextCompaction: protocol.NativeContextCompactionAfterTurn,
		})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("manager.run waited for final native maintenance before returning")
	}

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.end", "codex", "Codex final before hanging compact") {
		t.Fatalf("final turn.end should be emitted before final native maintenance can hang: %#v", events)
	}
	if !orchestrationEventsContain(events, "run.end", "", "Codex final before hanging compact") {
		t.Fatalf("run.end should be emitted before final native maintenance can hang: %#v", events)
	}
	for _, event := range events {
		if event.BridgeNoteData != nil && event.BridgeNoteData.Category == "native-context-compaction" {
			t.Fatalf("final native maintenance should not append visible compaction notes after run.end: %#v", events)
		}
	}

	codexRecords := waitForJSONLineEvent(t, codexLogPath, "thread_compact")
	var codexBusinessTurns, codexMaintenanceTurns int
	for _, record := range codexRecords {
		switch record["event"] {
		case "turn_start":
			codexBusinessTurns++
		case "thread_compact":
			codexMaintenanceTurns++
		}
	}
	if codexBusinessTurns != 1 || codexMaintenanceTurns != 1 {
		t.Fatalf("codex business=%d maintenance=%d records=%#v", codexBusinessTurns, codexMaintenanceTurns, codexRecords)
	}
}

func TestOrchestrationFailedCommandTailContinuesAfterRetryBudget(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(claudePath, []byte(fakeClaudePrintScript("Claude continued after interrupted Codex turn")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerFailedCommandScript(false)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)
	defer manager.CloseAll()

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:                   "orc_failed_tail",
		Mode:                    "collaboration",
		FirstCLI:                "codex",
		Prompt:                  "continue failed proof",
		MaxTurns:                2,
		CWD:                     tmp,
		NativeContextCompaction: protocol.NativeContextCompactionAfterTurn,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "codex", "继续编译。") {
		t.Fatalf("missing visible Codex progress before failure: %#v", events)
	}
	if !orchestrationEventsContain(events, "command.end", "codex", "Unable to unify") {
		t.Fatalf("missing failed command event: %#v", events)
	}
	if !orchestrationEventsContain(events, "turn.delta", "codex", "Bridge is preserving this turn's command events and moving to the next turn") {
		t.Fatalf("codex retry budget exhaustion was not visible: %#v", events)
	}
	if !orchestrationEventsContain(events, "turn.start", "claude", "Starting Claude") {
		t.Fatalf("orchestration did not enter the next turn after retry budget: %#v", events)
	}
	if !orchestrationEventsContain(events, "run.end", "", "Claude continued after interrupted Codex turn") {
		t.Fatalf("orchestration did not complete after next turn: %#v", events)
	}
	for _, event := range events {
		if event.BridgeNoteData != nil && event.BridgeNoteData.Category == "native-context-compaction" {
			t.Fatalf("interrupted business turn should not trigger native compaction: %#v", event)
		}
		if event.Kind == "run.error" {
			t.Fatalf("retry exhaustion should not fail the run: %#v", event)
		}
		if event.Kind == "turn.end" && event.CLI == "codex" && event.Severity != "warning" {
			t.Fatalf("exhausted Codex turn should be marked warning: %#v", event)
		}
	}
}

func TestOrchestrationProgressOnlyTurnRequiresFinalConclusion(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	codexProgress := "我会继续补 balanced-count 不变式，下一步先拆旧前缀和最后一个新 call。"
	claudeFinal := "最终结论：已接续处理缺少收尾的 Codex 轮次。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=orchestration continuation; next=none; risks=none"
	if err := os.WriteFile(codexPath, []byte(fakeCodexExecScript(codexProgress)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, []byte(fakeClaudePrintScript(claudeFinal)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)
	defer manager.CloseAll()

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:                   "orc_progress_only_tail",
		Mode:                    "collaboration",
		FirstCLI:                "codex",
		Prompt:                  "continue proof",
		MaxTurns:                2,
		CWD:                     tmp,
		NativeContextCompaction: protocol.NativeContextCompactionAfterTurn,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "codex", "Bridge is continuing this same turn (1/3)") {
		t.Fatalf("missing continuation retry notice for progress-only output: %#v", events)
	}
	if !orchestrationEventsContain(events, "turn.delta", "codex", "final conclusion or handoff summary after 3 continuation attempts") {
		t.Fatalf("missing retry exhaustion notice for progress-only output: %#v", events)
	}
	if !orchestrationEventsContain(events, "turn.start", "claude", "Starting Claude") {
		t.Fatalf("orchestration did not move to the next turn after retry exhaustion: %#v", events)
	}
	if !orchestrationEventsContain(events, "run.end", "", "已接续处理缺少收尾的 Codex 轮次") {
		t.Fatalf("orchestration did not complete after next turn: %#v", events)
	}
	for _, event := range events {
		if event.Kind == "turn.end" && event.CLI == "codex" && event.Status == "success" {
			t.Fatalf("progress-only Codex turn should not be marked success: %#v", event)
		}
		if event.BridgeNoteData != nil && event.BridgeNoteData.Category == "native-context-compaction" {
			t.Fatalf("progress-only Codex turn should not trigger native compaction: %#v", event)
		}
	}
}

// TestOrchestrationInlineMarkerMentionDoesNotEndTurn is the end-to-end regression
// guard for the anchored-marker fix. The Codex output mentions "交接摘要" inline as
// part of normal reasoning ("…比交接摘要里的行号更短…") without ever writing a real
// 交接总结/最终结论 section. Before the fix, the naive full-text substring match
// treated that mention as a finished handoff, so the turn was marked success and
// immediately /compact-ed mid-work. With anchored detection the turn must instead be
// continued, and must NOT trigger native context compaction.
func TestOrchestrationInlineMarkerMentionDoesNotEndTurn(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	t.Setenv("HOME", home)
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	codexInlineMention := "我检查了文件。sed 后段没有输出，说明文件比交接摘要里的行号更短。随后立即跑 coqc。"
	claudeFinal := "最终结论：已接续处理缺少收尾的 Codex 轮次。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=orchestration continuation; next=none; risks=none"
	if err := os.WriteFile(codexPath, []byte(fakeCodexExecScript(codexInlineMention)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(claudePath, []byte(fakeClaudePrintScript(claudeFinal)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)
	defer manager.CloseAll()

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:                   "orc_inline_marker_mention",
		Mode:                    "collaboration",
		FirstCLI:                "codex",
		Prompt:                  "continue proof",
		MaxTurns:                2,
		CWD:                     tmp,
		NativeContextCompaction: protocol.NativeContextCompactionAfterTurn,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "codex", "Bridge is continuing this same turn (1/3)") {
		t.Fatalf("inline marker mention should be continued, not treated as a finished handoff: %#v", events)
	}
	if !orchestrationEventsContain(events, "turn.start", "claude", "Starting Claude") {
		t.Fatalf("orchestration did not move to the next turn after exhausting continuations: %#v", events)
	}
	if !orchestrationEventsContain(events, "run.end", "", "已接续处理缺少收尾的 Codex 轮次") {
		t.Fatalf("orchestration did not complete after next turn: %#v", events)
	}
	for _, event := range events {
		if event.Kind == "turn.end" && event.CLI == "codex" && event.Status == "success" {
			t.Fatalf("inline marker mention must not mark the Codex turn success: %#v", event)
		}
		if event.BridgeNoteData != nil && event.BridgeNoteData.Category == "native-context-compaction" {
			t.Fatalf("inline marker mention must not trigger native compaction: %#v", event)
		}
	}
}

func TestOrchestrationInterruptedTurnContinuationCanCompleteSameTurn(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeInterruptedThenFinalScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexExecScript("Codex should not run")), 0o755); err != nil {
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
		RunID:    "orc_retry_success",
		Mode:     "collaboration",
		Prompt:   "continue same turn",
		MaxTurns: 1,
		CWD:      tmp,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "claude", "Bridge is continuing this same turn (1/3)") {
		t.Fatalf("missing continuation retry notice: %#v", events)
	}
	if !orchestrationEventsContain(events, "turn.end", "claude", "Claude completed after continuation") {
		t.Fatalf("turn did not complete from the continuation attempt: %#v", events)
	}
	if !orchestrationEventsContain(events, "run.end", "", "Claude completed after continuation") {
		t.Fatalf("run did not complete after continuation: %#v", events)
	}
	for _, event := range events {
		if event.Kind == "run.error" {
			t.Fatalf("continuation success should not fail run: %#v", event)
		}
		if event.Kind == "turn.end" && event.CLI == "claude" && event.Status != "success" {
			t.Fatalf("completed continuation should be successful: %#v", event)
		}
	}
}

func TestClaudeNativeResumeMetadataUpdatesProjectVisibility(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	cwd := filepath.Join(tmp, "work")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	sessionID := "11111111-2222-5333-8444-555555555555"
	transcriptPath := claudeSessionFilePath(cwd, sessionID)
	if err := os.MkdirAll(filepath.Dir(transcriptPath), 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := strings.Join([]string{
		`{"type":"custom-title","customTitle":"Bridge visible resume","sessionId":"` + sessionID + `"}`,
		`{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":[{"type":"text","text":"create a visible bridge session"}]},"uuid":"user-1","timestamp":"2026-06-02T00:00:00.000Z","permissionMode":"acceptEdits","userType":"external","entrypoint":"sdk-cli","cwd":"` + cwd + `","sessionId":"` + sessionID + `","version":"2.1.159","gitBranch":"HEAD"}`,
		`{"parentUuid":"user-1","isSidechain":false,"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]},"uuid":"assistant-1","timestamp":"2026-06-02T00:00:01.000Z","userType":"external","entrypoint":"sdk-cli","cwd":"` + cwd + `","sessionId":"` + sessionID + `","version":"2.1.159","gitBranch":"HEAD"}`,
		`{"type":"ai-title","aiTitle":"AI title should not override custom title","sessionId":"` + sessionID + `"}`,
		"",
	}, "\n")
	if err := os.WriteFile(transcriptPath, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	otherCWD := filepath.Join(tmp, "other")
	seed := map[string]any{
		"projects": map[string]any{
			otherCWD: map[string]any{"lastSessionId": "keep-me"},
		},
	}
	seedData, err := json.Marshal(seed)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), seedData, 0o600); err != nil {
		t.Fatal(err)
	}

	manager := NewOrchestrationManager(&config.Config{})
	claude := &orchestrationClaudeSession{sessionID: sessionID}
	info := manager.registerClaudeNativeResume(&orchestrationNativeSession{cwd: cwd}, claude, orchestrationClaudeDefaultSlot, "orc_resume", cwd)
	if info == nil {
		t.Fatal("expected claude native resume info")
	}
	if !info.Visible || info.TranscriptPath != transcriptPath || info.Command != "claude --resume "+sessionID {
		t.Fatalf("unexpected resume info: %#v", info)
	}
	if !strings.Contains(info.VisibilityReason, "/resume picker") {
		t.Fatalf("visibility reason does not mention picker materialization: %q", info.VisibilityReason)
	}
	raw, err := os.ReadFile(filepath.Join(home, ".claude.json"))
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	projects := root["projects"].(map[string]any)
	project := projects[cwd].(map[string]any)
	if project["lastSessionId"] != sessionID || project["lastGracefulShutdown"] != true {
		t.Fatalf("project metadata not updated: %#v", project)
	}
	other := projects[otherCWD].(map[string]any)
	if other["lastSessionId"] != "keep-me" {
		t.Fatalf("unrelated project changed: %#v", other)
	}
	hintPath := filepath.Join(home, ".claude", "sessions", sessionID+".json")
	hintRaw, err := os.ReadFile(hintPath)
	if err != nil {
		t.Fatal(err)
	}
	var hint map[string]any
	if err := json.Unmarshal(hintRaw, &hint); err != nil {
		t.Fatal(err)
	}
	if hint["nativeResumeCommand"] != info.Command || hint["nativeResumeAvailable"] != true {
		t.Fatalf("compat session hint = %#v", hint)
	}
	materialized, err := os.ReadFile(transcriptPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(materialized), `"entrypoint":"sdk-cli"`) || !strings.Contains(string(materialized), `"entrypoint":"cli"`) {
		t.Fatalf("transcript was not materialized for picker visibility:\n%s", string(materialized))
	}
	historyRaw, err := os.ReadFile(filepath.Join(home, ".claude", "history.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(historyRaw), `"sessionId":"`+sessionID+`"`) || !strings.Contains(string(historyRaw), "Bridge visible resume") {
		t.Fatalf("history index missing visible session: %s", string(historyRaw))
	}
}

func TestIsolatedCodexSessionIsVisibleWithoutCopyingConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	threadID := "019fffff-1111-7222-8333-444444444444"
	privateHome := filepath.Join(home, ".codex-bridge", "orchestration-profiles", "run", "codex-a", "codex")
	source := filepath.Join(privateHome, "sessions", "2026", "08", "15", "rollout-2026-08-15T00-00-00-"+threadID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := []byte(`{"type":"session_meta","payload":{"id":"` + threadID + `","cwd":"/work","model_provider":"custom"}}` + "\n")
	if err := os.WriteFile(source, transcript, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateHome, "config.toml"), []byte("secret-config"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(privateHome, "auth.json"), []byte("secret-auth"), 0o600); err != nil {
		t.Fatal(err)
	}

	info := codexNativeResumeInfoForSlot("codex-a", threadID, "/work", orchestrationWorkerRuntime{env: []string{"CODEX_HOME=" + privateHome}})
	if info == nil || !strings.Contains(info.Command, "CODEX_HOME=") || !strings.Contains(info.VisibilityReason, "ordinary Codex /resume picker") {
		t.Fatalf("isolated Codex resume info = %#v", info)
	}
	target := filepath.Join(home, ".codex", "sessions", "2026", "08", "15", filepath.Base(source))
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, transcript) {
		t.Fatalf("ordinary picker transcript = %q, %v", got, err)
	}
	for _, path := range []string{filepath.Join(home, ".codex", "config.toml"), filepath.Join(home, ".codex", "auth.json")} {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("isolated configuration leaked to %q: %v", path, err)
		}
	}
}

func TestIsolatedClaudeSessionIsVisibleWithoutCopyingConfiguration(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "work")
	t.Setenv("HOME", home)
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	sessionID := "11111111-2222-5333-8444-666666666666"
	configDir := filepath.Join(home, ".codex-bridge", "orchestration-profiles", "run", "claude-a", "claude")
	source := claudeSessionFilePathForConfig(cwd, sessionID, configDir)
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	transcript := `{"type":"user","entrypoint":"sdk-cli","cwd":"` + cwd + `","sessionId":"` + sessionID + `","message":{"role":"user","content":"isolated session"}}` + "\n"
	if err := os.WriteFile(source, []byte(transcript), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "settings.json"), []byte(`{"env":{"ANTHROPIC_AUTH_TOKEN":"secret"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	native := &orchestrationNativeSession{cwd: cwd, profileRuntime: map[string]orchestrationWorkerRuntime{
		"claude-a": {env: []string{"CLAUDE_CONFIG_DIR=" + configDir}},
	}}
	manager := NewOrchestrationManager(&config.Config{})
	info := manager.registerClaudeNativeResume(native, &orchestrationClaudeSession{sessionID: sessionID}, "claude-a", "orc_isolated", cwd)
	if info == nil || !info.Visible || !strings.Contains(info.Command, "CLAUDE_CONFIG_DIR=") {
		t.Fatalf("isolated Claude resume info = %#v", info)
	}
	target := claudeSessionFilePath(cwd, sessionID)
	materialized, err := os.ReadFile(target)
	if err != nil || !strings.Contains(string(materialized), `"entrypoint":"cli"`) {
		t.Fatalf("ordinary Claude picker transcript = %q, %v", materialized, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude", "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("isolated Claude settings leaked into the ordinary config: %v", err)
	}
}

func TestClaudeTranscriptTitlePrefersNativeTitleOverFirstUserMessage(t *testing.T) {
	transcript := strings.Join([]string{
		`{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Return exactly this phrase for the smoke run"}]}}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`,
		`{"type":"ai-title","aiTitle":"Return specified bridge phrase"}`,
		"",
	}, "\n")

	if got := claudeTranscriptTitle([]byte(transcript)); got != "Return specified bridge phrase" {
		t.Fatalf("claudeTranscriptTitle() = %q", got)
	}
}

func TestOrchestrationCodexResumeMissingThreadRetriesFresh(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	argvPath := filepath.Join(tmp, "codex_argv.jsonl")
	if err := os.WriteFile(codexPath, []byte(fakeCodexResumeMissThenFreshScript(argvPath)), 0o755); err != nil {
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

	content, _, threadID, resumeMode, err := manager.runCodexWithThread(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_codex_retry",
		CWD:   tmp,
	}, "turn_1", "reviewer", orchestrationCodexDefaultSlot, "continue", "thread_missing")
	if err != nil {
		t.Fatal(err)
	}
	if content != "fresh result" || threadID != "thread_fresh" || resumeMode != "codex-fresh-after-resume-miss" {
		t.Fatalf("codex retry result content=%q thread=%q mode=%q", content, threadID, resumeMode)
	}
	raw, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "resume") || !strings.Contains(lines[0], "thread_missing") || strings.Contains(lines[1], "resume") {
		t.Fatalf("codex retry argv log unexpected:\n%s", raw)
	}
	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "codex", "fresh Codex thread") {
		t.Fatalf("missing codex resume warning event: %#v", events)
	}
}

func TestOrchestrationClaudeResumeMissingSessionRetriesSessionID(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	argvPath := filepath.Join(tmp, "claude_argv.jsonl")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeResumeMissThenSessionScript(argvPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)

	content, _, resumeMode, err := manager.runClaudeWithSession(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_claude_retry",
		CWD:   tmp,
	}, "turn_1", "implementer", "continue", "11111111-1111-5111-8111-111111111111", true)
	if err != nil {
		t.Fatal(err)
	}
	if content != "claude fresh session result" || resumeMode != "claude-new-after-resume-miss" {
		t.Fatalf("claude retry result content=%q mode=%q", content, resumeMode)
	}
	raw, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "--resume") || !strings.Contains(lines[1], "--session-id") {
		t.Fatalf("claude retry argv log unexpected:\n%s", raw)
	}
	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "claude", "retry once") {
		t.Fatalf("missing claude resume warning event: %#v", events)
	}
}

func TestOrchestrationClaudeInteractiveSkipsResumeWhenTranscriptIsMissing(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	argvPath := filepath.Join(tmp, "claude_argv.jsonl")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeInteractiveRelayScript(argvPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)
	state := &orchestrationSessionState{
		ClaudeSessionID:      "11111111-1111-5111-8111-111111111111",
		ClaudeSessionStarted: true,
		NativeSession:        manager.nativeSession("orc_missing_transcript", tmp),
	}

	content, _, resumeMode, err := manager.runClaudeInteractive(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_missing_transcript",
		CWD:   tmp,
	}, "turn_1", "implementer", "continue", state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "Claude native turn 1") || resumeMode != "claude-interactive-session" || state.ClaudeSessionStarted {
		t.Fatalf("unexpected fallback result content=%q mode=%q started=%v", content, resumeMode, state.ClaudeSessionStarted)
	}
	raw, err := os.ReadFile(argvPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "--resume") || !strings.Contains(string(raw), "--session-id") {
		t.Fatalf("missing-transcript fallback should start a session, argv=%s", raw)
	}
	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "claude", "transcript is unavailable") {
		t.Fatalf("missing transcript warning event: %#v", events)
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

func TestModelCapacityErrorRecognitionIsConservative(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "selected model capacity", err: errors.New("Selected model is at capacity. Please try a different model."), want: true},
		{name: "generic model capacity", err: errors.New("model is at capacity"), want: true},
		{name: "invalid model", err: errors.New("selected model does not exist"), want: false},
		{name: "canceled", err: context.Canceled, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isModelCapacityError(test.err); got != test.want {
				t.Fatalf("isModelCapacityError(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestRecoverableCLITransportErrorRecognitionIsConservative(t *testing.T) {
	for _, test := range []struct {
		name string
		cli  string
		err  error
		want bool
	}{
		{name: "codex reconnecting", cli: "codex", err: errors.New("Reconnecting... 1/5"), want: true},
		{name: "codex stream closed", cli: "codex", err: errors.New("stream closed before response.completed"), want: true},
		{name: "claude reconnect text", cli: "claude", err: errors.New("Reconnecting... 1/5"), want: false},
		{name: "codex command failure", cli: "codex", err: errors.New("coqc exited with status 1"), want: false},
		{name: "canceled", cli: "codex", err: context.Canceled, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isRecoverableCLITransportError(test.cli, test.err); got != test.want {
				t.Fatalf("isRecoverableCLITransportError(%q, %v) = %v, want %v", test.cli, test.err, got, test.want)
			}
		})
	}
}

func TestCodexThreadActiveWriterErrorRecognitionIsConservative(t *testing.T) {
	for _, test := range []struct {
		name string
		cli  string
		err  error
		want bool
	}{
		{name: "codex active writer", cli: "codex", err: errors.New("thread 019fe187 already has an active writer"), want: true},
		{name: "claude same text", cli: "claude", err: errors.New("thread 019fe187 already has an active writer"), want: false},
		{name: "generic thread error", cli: "codex", err: errors.New("thread 019fe187 was not found"), want: false},
		{name: "canceled", cli: "codex", err: context.Canceled, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isCodexThreadActiveWriterError(test.cli, test.err); got != test.want {
				t.Fatalf("isCodexThreadActiveWriterError(%q, %v) = %v, want %v", test.cli, test.err, got, test.want)
			}
		})
	}
}

func TestOrchestrationCodexActiveWriterRetriesOriginalPromptOnSameThread(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	statePath := filepath.Join(tmp, "active-writer-state.json")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerActiveWriterScript(statePath, 1)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	manager.codexThreadBusyRetryWaits = []time.Duration{0}
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:         "orc_active_writer_recovery",
		Mode:          "collaboration",
		FirstCLI:      "codex",
		Prompt:        "continue the existing proof",
		MaxTurns:      1,
		CWD:           tmp,
		Resume:        true,
		CodexThreadID: "thr_active_writer",
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "codex", "上一回合仍在收尾") ||
		!orchestrationEventsContain(events, "turn.delta", "codex", "重新提交当前消息") ||
		!orchestrationEventsContain(events, "run.end", "", "active writer recovery completed") {
		t.Fatalf("active-writer recovery was not visible or successful: %#v", events)
	}
	for _, event := range events {
		if event.Kind == "run.error" {
			t.Fatalf("active-writer recovery ended the run: %#v", event)
		}
	}
	state := readActiveWriterFakeState(t, statePath)
	if state.Processes != 2 || state.TurnStarts != 2 || len(state.Prompts) != 2 || state.Prompts[0] != state.Prompts[1] {
		t.Fatalf("active-writer retry changed process or prompt: %#v", state)
	}
}

func TestOrchestrationCodexActiveWriterRetryExhaustionPreservesReason(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	statePath := filepath.Join(tmp, "active-writer-state.json")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerActiveWriterScript(statePath, 99)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	manager.codexThreadBusyRetryWaits = []time.Duration{0}
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_active_writer_exhausted", Mode: "collaboration", FirstCLI: "codex", Prompt: "continue", MaxTurns: 1, CWD: tmp,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "codex", "当前消息尚未提交") ||
		!orchestrationEventsContain(events, "run.error", "codex", "already has an active writer") {
		t.Fatalf("active-writer exhaustion did not preserve reason: %#v", events)
	}
	state := readActiveWriterFakeState(t, statePath)
	if state.Processes != 2 || state.TurnStarts != 2 || len(state.Prompts) != 2 || state.Prompts[0] != state.Prompts[1] {
		t.Fatalf("active-writer exhaustion attempts = %#v", state)
	}
}

func TestDurableTaskTerminalReleasesCodexWriterBeforeDelivery(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	statePath := filepath.Join(tmp, "durable-task-writer-state.json")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerActiveWriterScript(statePath, 0)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)
	defer manager.CloseAll()

	payload := protocol.OrchestrationStartPayload{
		RunID: "orc_durable_writer_release", Mode: "collaboration", WorkerPair: protocol.WorkerPairCodexCodex,
		Prompt: "review one durable task", MaxTurns: 1, MaxTurnsRequested: 1, CWD: tmp,
		TaskGraph: &protocol.TaskGraphPayload{ID: "otg_writer", Generation: 1, Round: 1, MaxRounds: 1, Tasks: []protocol.TaskPayload{{
			ID: "otk_writer", AttemptID: "ota_writer", Role: store.TaskRoleReviewer, WorkerSlot: orchestrationCodexSlotA, PayloadDigest: "digest",
		}}},
	}
	manager.Start(payload)
	var terminal protocol.OrchestrationEventPayload
	ok := false
	deadline := time.After(5 * time.Second)
	for !ok {
		select {
		case env := <-out:
			if env.Type != protocol.TypeOrchestrationEvent {
				continue
			}
			event, err := protocol.Decode[protocol.OrchestrationEventPayload](env)
			if err != nil {
				t.Fatal(err)
			}
			if event.Kind == "run.error" {
				terminal, ok = event, true
			}
		case <-deadline:
			t.Fatal("timed out waiting for durable reviewer failure")
		}
	}
	if !ok || terminal.Task == nil || terminal.Task.AttemptID != "ota_writer" {
		t.Fatalf("durable terminal event = %#v", terminal)
	}
	manager.mu.Lock()
	handle := manager.runs[orchestrationExecutionKey(payload)]
	manager.mu.Unlock()
	if handle != nil {
		<-handle.done
	}
	executionKey := orchestrationExecutionKey(payload)
	manager.mu.Lock()
	_, sessionExists := manager.sessions[executionKey]
	manager.mu.Unlock()
	if sessionExists {
		t.Fatal("durable task terminal event was delivered before native session cleanup")
	}
}

func TestDurableClaudeAttemptsCreateTheirOwnSessions(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	logPath := filepath.Join(tmp, "claude-log.jsonl")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeInteractiveRelayScript(logPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	defer manager.CloseAll()

	runAttempt := func(attemptID string) {
		t.Helper()
		out := make(chan protocol.Envelope, 64)
		manager.AttachOut(out)
		manager.run(context.Background(), protocol.OrchestrationStartPayload{
			// Start rewrites graph attempts to this execution key before calling
			// run. Mirror that boundary directly so the test covers session identity.
			RunID: "orc_durable_claude:" + attemptID, Mode: "collaboration", FirstCLI: "claude", Prompt: "complete node " + attemptID,
			MaxTurns: 1, CWD: tmp,
			TaskGraph: &protocol.TaskGraphPayload{ID: "otg_claude", Generation: 1, Round: 1, MaxRounds: 1, Tasks: []protocol.TaskPayload{{
				ID: "otk_" + attemptID, AttemptID: attemptID, Role: store.TaskRoleWorker, WorkerSlot: "claude", PayloadDigest: "digest_" + attemptID,
			}}},
		})
	}
	runAttempt("ota_first")
	runAttempt("ota_second")

	records := waitForJSONLineEventCount(t, logPath, "process_start", 2)
	var starts []map[string]any
	for _, record := range records {
		if record["event"] == "process_start" {
			starts = append(starts, record)
		}
	}
	if len(starts) != 2 {
		t.Fatalf("Claude process records = %#v", records)
	}
	firstArgs, _ := starts[0]["argv"].([]any)
	secondArgs, _ := starts[1]["argv"].([]any)
	for _, args := range [][]any{firstArgs, secondArgs} {
		if sliceContainsString(args, "--resume") || !sliceContainsString(args, "--session-id") {
			t.Fatalf("durable Claude attempt did not create its own session: %#v", args)
		}
	}
	firstID, _ := starts[0]["sessionId"].(string)
	secondID, _ := starts[1]["sessionId"].(string)
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("durable Claude session ids = %q, %q", firstID, secondID)
	}
}

func TestDurableClaudeClaudeTaskInitializesEmptySlotSessionIDs(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	logPath := filepath.Join(tmp, "claude-claude-log.jsonl")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeInteractiveRelayScript(logPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	defer manager.CloseAll()
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)

	// Durable graph dispatch deliberately clears native session IDs before the
	// first worker starts. Claude+Claude must recreate both slot IDs from that
	// empty payload without crashing the Bridge.
	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_durable_claude_pair:ota_first", Mode: "collaboration", WorkerPair: protocol.WorkerPairClaudeClaude,
		Prompt: "complete the first Claude pair node", MaxTurns: 1, CWD: tmp,
		TaskGraph: &protocol.TaskGraphPayload{ID: "otg_claude_pair", Generation: 1, Round: 1, MaxRounds: 1, Tasks: []protocol.TaskPayload{{
			ID: "otk_claude_pair", AttemptID: "ota_first", Role: store.TaskRolePlanner, WorkerSlot: orchestrationClaudeSlotA, PayloadDigest: "digest",
		}}},
	})

	events := drainOrchestrationEvents(t, out)
	var runStart protocol.OrchestrationEventPayload
	for _, event := range events {
		if event.Kind == "run.start" {
			runStart = event
			break
		}
	}
	if runStart.RunStartData == nil || runStart.RunStartData.WorkerPair != protocol.WorkerPairClaudeClaude {
		t.Fatalf("durable Claude+Claude run did not start: %#v", events)
	}
}

func TestOrchestrationCodexActiveWriterRetryWaitHonorsCancellation(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	statePath := filepath.Join(tmp, "active-writer-state.json")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerActiveWriterScript(statePath, 99)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	manager.codexThreadBusyRetryWaits = []time.Duration{time.Hour}
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.run(ctx, protocol.OrchestrationStartPayload{
			RunID: "orc_active_writer_cancel", Mode: "collaboration", FirstCLI: "codex", Prompt: "continue", MaxTurns: 1, CWD: tmp,
		})
	}()

	if !waitForOrchestrationEvent(t, out, "turn.delta", "codex", "将在 60 分钟后重新提交") {
		t.Fatal("active-writer retry wait was not emitted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("active-writer retry wait did not stop after cancellation")
	}
	events := drainOrchestrationEvents(t, out)
	if orchestrationEventsContain(events, "turn.delta", "codex", "正在向同一 Codex 原生会话重新提交") {
		t.Fatalf("active-writer retry started after cancellation: %#v", events)
	}
	state := readActiveWriterFakeState(t, statePath)
	if state.TurnStarts != 1 {
		t.Fatalf("active-writer retry submitted after cancellation: %#v", state)
	}
}

func TestOrchestrationCodexTransportFailureResumesSameThread(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	statePath := filepath.Join(tmp, "transport-attempts")
	promptPath := filepath.Join(tmp, "recovery-prompt")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerTransportRecoveryScript(statePath, promptPath)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "workspace-write"
	cfg.Bridge.ApprovalPolicy = "untrusted"
	manager := NewOrchestrationManager(&cfg)
	manager.cliTransportRetryWaits = []time.Duration{0}
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:    "orc_transport_recovery",
		Mode:     "collaboration",
		FirstCLI: "codex",
		Prompt:   "finish without repeating side effects",
		MaxTurns: 1,
		CWD:      tmp,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "codex", "原生连接正在恢复（1/5）") ||
		!orchestrationEventsContain(events, "turn.delta", "codex", "流连接中断，将在 0 秒后") ||
		!orchestrationEventsContain(events, "turn.delta", "codex", "正在从同一 Codex 会话恢复当前回合") ||
		!orchestrationEventsContain(events, "run.end", "", "transport recovery completed") {
		t.Fatalf("transport recovery was not fully visible or successful: %#v", events)
	}
	for _, event := range events {
		if event.Kind == "run.error" {
			t.Fatalf("recoverable transport failure ended the run: %#v", event)
		}
		if event.Kind == "turn.end" && event.Error != "" {
			t.Fatalf("successful transport recovery retained stale error: %#v", event)
		}
	}
	prompt, err := os.ReadFile(promptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(prompt), "do not replay the original request") || strings.Contains(string(prompt), "finish without repeating side effects") {
		t.Fatalf("unsafe transport recovery prompt = %q", prompt)
	}
	if attempts, err := os.ReadFile(statePath); err != nil || string(attempts) != "2" {
		t.Fatalf("app-server process attempts = %q, err = %v", attempts, err)
	}
}

func TestOrchestrationCodexTransportRetryExhaustionPreservesReason(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	statePath := filepath.Join(tmp, "transport-attempts")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerFailureScript(statePath, "Reconnecting... 5/5 (stream disconnected before completion: upstream closed)")), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	manager.cliTransportRetryWaits = []time.Duration{0}
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_transport_exhausted", Mode: "collaboration", FirstCLI: "codex", Prompt: "finish", MaxTurns: 1, CWD: tmp,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "codex", "已完成 1 次退避重试") ||
		!orchestrationEventsContain(events, "run.error", "codex", "stream disconnected before completion") {
		t.Fatalf("transport exhaustion did not retain its concrete reason: %#v", events)
	}
	if attempts, err := os.ReadFile(statePath); err != nil || string(attempts) != "2" {
		t.Fatalf("transport exhaustion process attempts = %q, err = %v", attempts, err)
	}
}

func TestOrchestrationCodexTransportRetryWaitHonorsCancellation(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	statePath := filepath.Join(tmp, "transport-attempts")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerFailureScript(statePath, "Reconnecting... 1/5 (stream closed before response.completed)")), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	manager.cliTransportRetryWaits = []time.Duration{time.Hour}
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		manager.run(ctx, protocol.OrchestrationStartPayload{
			RunID: "orc_transport_cancel", Mode: "collaboration", FirstCLI: "codex", Prompt: "finish", MaxTurns: 1, CWD: tmp,
		})
	}()

	if !waitForOrchestrationEvent(t, out, "turn.delta", "codex", "流连接中断，将在 60 分钟后") {
		t.Fatal("transport retry wait was not emitted")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("transport retry wait did not stop after cancellation")
	}
	events := drainOrchestrationEvents(t, out)
	if orchestrationEventsContain(events, "turn.delta", "codex", "正在从同一 Codex 会话恢复当前回合") {
		t.Fatalf("transport retry started after cancellation: %#v", events)
	}
	if attempts, err := os.ReadFile(statePath); err != nil || string(attempts) != "1" {
		t.Fatalf("canceled transport process attempts = %q, err = %v", attempts, err)
	}
}

func TestOrchestrationCodexPermanentFailureDoesNotUseTransportRetry(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	statePath := filepath.Join(tmp, "transport-attempts")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerFailureScript(statePath, "coqc exited with status 1: proof obligation remains")), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	manager.cliTransportRetryWaits = []time.Duration{0, 0, 0}
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_permanent_error", Mode: "collaboration", FirstCLI: "codex", Prompt: "finish proof", MaxTurns: 1, CWD: tmp,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "run.error", "codex", "coqc exited with status 1") {
		t.Fatalf("permanent error was not reported: %#v", events)
	}
	if orchestrationEventsContain(events, "turn.delta", "codex", "流连接中断") {
		t.Fatalf("permanent error incorrectly entered transport recovery: %#v", events)
	}
	if attempts, err := os.ReadFile(statePath); err != nil || string(attempts) != "1" {
		t.Fatalf("permanent failure process attempts = %q, err = %v", attempts, err)
	}
}

func TestRelayCLIModelCapacityRetriesSameTurn(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeCapacityThenFinalScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	manager.modelCapacityRetryWaits = []time.Duration{0}
	out := make(chan protocol.Envelope, 32)
	manager.AttachOut(out)

	content, _, err := manager.runRelayCLIWithCapacityRetries(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_capacity_retry", CWD: tmp,
	}, "orc_capacity_retry-01", "implementer", "claude", "claude", "finish the task", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "capacity retry completed") {
		t.Fatalf("retry content = %q", content)
	}
	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "claude", "模型当前容量已满") ||
		!orchestrationEventsContain(events, "turn.delta", "claude", "正在重试 Claude") {
		t.Fatalf("capacity retry state was not visible: %#v", events)
	}
}

func TestRelayCLIModelCapacityRetryExhaustionPreservesProviderError(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeErrorScript("Selected model is at capacity. Please try a different model.")), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	manager.modelCapacityRetryWaits = []time.Duration{0, 0}
	out := make(chan protocol.Envelope, 32)
	manager.AttachOut(out)

	_, _, err := manager.runRelayCLIWithCapacityRetries(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_capacity_exhausted", CWD: tmp,
	}, "orc_capacity_exhausted-01", "implementer", "claude", "claude", "finish the task", nil)
	if err == nil || !strings.Contains(err.Error(), "at capacity") {
		t.Fatalf("retry exhaustion error = %v", err)
	}
	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "claude", "已完成 2 次退避重试") ||
		!orchestrationEventsContain(events, "turn.delta", "claude", "Selected model is at capacity") {
		t.Fatalf("retry exhaustion was not visible with provider error: %#v", events)
	}
}

func TestRelayCLIModelCapacityRetryWaitHonorsCancellation(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeErrorScript("Selected model is at capacity. Please try a different model.")), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	manager.modelCapacityRetryWaits = []time.Duration{time.Hour}
	out := make(chan protocol.Envelope, 32)
	manager.AttachOut(out)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, _, err := manager.runRelayCLIWithCapacityRetries(ctx, protocol.OrchestrationStartPayload{
			RunID: "orc_capacity_cancel", CWD: tmp,
		}, "orc_capacity_cancel-01", "implementer", "claude", "claude", "finish the task", nil)
		done <- err
	}()
	if !waitForOrchestrationEvent(t, out, "turn.delta", "claude", "模型当前容量已满") {
		t.Fatal("capacity retry wait was not emitted")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled wait error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("capacity retry wait did not stop after cancellation")
	}
	events := drainOrchestrationEvents(t, out)
	if orchestrationEventsContain(events, "turn.delta", "claude", "正在重试 Claude") {
		t.Fatalf("retry started after cancellation: %#v", events)
	}
}

func TestOrchestrationModelCapacityExhaustionEndsWithProviderReason(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeErrorScript("Selected model is at capacity. Please try a different model.")), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	manager.modelCapacityRetryWaits = []time.Duration{0}
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_capacity_terminal", Mode: "collaboration", Prompt: "finish", MaxTurns: 1, CWD: tmp,
	})
	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "claude", "已完成 1 次退避重试") ||
		!orchestrationEventsContain(events, "run.error", "claude", "Selected model is at capacity") {
		t.Fatalf("capacity exhaustion did not remain a visible terminal error: %#v", events)
	}
	if orchestrationEventsContain(events, "turn.delta", "claude", "continuing this same turn") {
		t.Fatalf("capacity exhaustion incorrectly started missing-final continuation: %#v", events)
	}
}

func TestOrchestrationCodexTailDisconnectAfterFinalContentCompletes(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	if err := os.WriteFile(codexPath, []byte(`#!/usr/bin/env python3
import json
import sys

text = "最终结果已经输出。\n\nMsg: to=user; intent=final; need=none\nHandoff: status=needs_next; changed=none; verified=none; next=none; risks=仍有证明义务"
if sys.argv[1] == "app-server":
    for line in sys.stdin:
        msg = json.loads(line)
        method = msg.get("method")
        params = msg.get("params") or {}
        if method == "initialize":
            print(json.dumps({"id":msg["id"],"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"linux"}}), flush=True)
        elif method == "thread/start":
            print(json.dumps({"id":msg["id"],"result":{"thread":{"id":"thr_tail"}}}), flush=True)
        elif method == "thread/name/set":
            print(json.dumps({"id":msg["id"],"result":{}}), flush=True)
        elif method == "turn/start":
            print(json.dumps({"id":msg["id"],"result":{"turn":{"id":"turn_tail","status":"inProgress"}}}), flush=True)
            print(json.dumps({"method":"item/agentMessage/delta","params":{"threadId":"thr_tail","turnId":"turn_tail","delta":text}}), flush=True)
            break
    raise SystemExit(0)
if len(sys.argv) < 2 or sys.argv[1] != "exec":
    sys.exit(1)
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

func TestOrchestrationCodexEmptyTailErrorAfterVisibleOutputCompletesSilently(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	claudePath := filepath.Join(tmp, "claude")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerEmptyErrorWithFinalConclusionScript()), 0o755); err != nil {
		t.Fatal(err)
	}
	claudeText := "Claude continued after Codex visible output\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=claude continued; next=none; risks=none"
	if err := os.WriteFile(claudePath, []byte(fakeClaudePrintScript(claudeText)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 128)
	manager.AttachOut(out)

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID:    "orc_empty_tail_error",
		Mode:     "collaboration",
		FirstCLI: "codex",
		Prompt:   "prove and continue",
		MaxTurns: 2,
		CWD:      tmp,
	})

	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "codex", "rewrite Habs direction was wrong") {
		t.Fatalf("missing visible codex output: %#v", events)
	}
	if !orchestrationEventsContain(events, "turn.end", "codex", "rewrite Habs direction was wrong") {
		t.Fatalf("codex turn did not complete with visible output: %#v", events)
	}
	if !orchestrationEventsContain(events, "turn.start", "claude", "Starting Claude") {
		t.Fatalf("orchestration did not continue to next turn: %#v", events)
	}
	if !orchestrationEventsContain(events, "run.end", "", "Claude continued after Codex visible output") {
		t.Fatalf("run did not complete after recoverable codex error: %#v", events)
	}
	for _, event := range events {
		if orchestrationEventContainsText(event, "empty tail error after visible output") {
			t.Fatalf("empty app-server tail error should not be surfaced: %#v", event)
		}
		if event.Kind == "run.error" {
			t.Fatalf("recoverable codex tail error should not fail run: %#v", event)
		}
		if event.Kind == "turn.end" && event.CLI == "codex" && event.Status == "error" {
			t.Fatalf("recoverable codex tail error should not mark turn failed: %#v", event)
		}
	}
}

func TestLongCommandObserverWritesToSameClaudeStreamAndEmitsBridgeNote(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	stdoutReader, stdoutWriter := io.Pipe()
	stdin := &syncWriteCloser{}

	done := make(chan struct{})
	var content string
	var tools []RunnerToolEvent
	var scanErr error
	go func() {
		defer close(done)
		content, tools, scanErr = manager.scanClaudeJSONLWithOptions(stdoutReader, "orc_nudge", "orc_nudge-01", "implementer", claudeScanOptions{
			Input:      stdin,
			CanNudge:   true,
			NudgeAfter: 10 * time.Millisecond,
			LongCommandObserver: longCommandObserverConfig{
				Enabled:         true,
				CommandPatterns: []string{"python -m slow_build"},
				AppliesTo:       []string{"claude", "codex"},
			},
		})
	}()

	fmt.Fprintln(stdoutWriter, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_build","name":"Bash","input":{"command":"python -m slow_build --workspace demo"}}]}}`)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !strings.Contains(stdin.String(), "ProofBridge observer note") {
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
	if got := stdin.String(); !strings.Contains(got, "ProofBridge observer note") || !strings.Contains(got, "python -m slow_build --workspace demo") {
		t.Fatalf("nudge was not written to Claude stream: %s", got)
	}
	event, ok := waitForOrchestrationEventPayload(t, out, "turn.delta", "claude", "Bridge sent a long-command observer note")
	if !ok {
		t.Fatal("frontend-visible observer event was not emitted")
	}
	if event.Source == "bridge" && event.BridgeNoteData != nil && event.BridgeNoteData.InjectedText != "" {
		return
	}
	t.Fatalf("observer event did not carry structured injected text: %#v", event)
}

func TestClaudeStreamFailedToolWithoutFollowupFails(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	input := strings.NewReader(strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"继续编译。"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_build","name":"Bash","input":{"command":"coqc -R . LinLattice HWQ_U/L0Proof.v"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_build","is_error":true,"content":"Error: Unable to unify proof state.\n"}]}}`,
		`{"type":"result","result":"继续编译。"}`,
		"",
	}, "\n"))

	content, tools, err := manager.scanClaudeJSONLWithOptions(input, "orc_claude_failed_tail", "orc_claude_failed_tail-01", "implementer", claudeScanOptions{})
	if err == nil {
		t.Fatal("expected failed tool without follow-up to fail the scanner")
	}
	for _, want := range []string{"coqc -R . LinLattice HWQ_U/L0Proof.v", "Unable to unify", "without a follow-up response"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
	if content != "继续编译。" {
		t.Fatalf("content = %q", content)
	}
	if len(tools) != 2 || !runnerToolEventFailed(tools[1]) {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestClaudeStreamFailedToolWithFollowupCompletes(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	input := strings.NewReader(strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"text","text":"继续编译。"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_build","name":"Bash","input":{"command":"coqc -R . LinLattice HWQ_U/L0Proof.v"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tool_build","is_error":true,"content":"Error: Unable to unify proof state.\n"}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"我会根据这个错误继续修正。"}]}}`,
		`{"type":"result","result":"继续编译。我会根据这个错误继续修正。"}`,
		"",
	}, "\n"))

	content, tools, err := manager.scanClaudeJSONLWithOptions(input, "orc_claude_handled_tail", "orc_claude_handled_tail-01", "implementer", claudeScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(content, "我会根据这个错误继续修正。") {
		t.Fatalf("content = %q", content)
	}
	if len(tools) != 2 || !runnerToolEventFailed(tools[1]) {
		t.Fatalf("tools = %#v", tools)
	}
}

func TestLongCommandObserverEmitsCodexBridgeNoteWithoutSideChannel(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.LongCommandObserver.Enabled = true
	cfg.Bridge.LongCommandObserver.After.Duration = 10 * time.Millisecond
	cfg.Bridge.LongCommandObserver.CommandPatterns = []string{"python -m slow_build"}
	cfg.Bridge.LongCommandObserver.AppliesTo = []string{"codex"}
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	stdoutReader, stdoutWriter := io.Pipe()

	done := make(chan struct{})
	var result codexScanResult
	var scanErr error
	go func() {
		defer close(done)
		result, scanErr = manager.scanCodexJSONLResult(stdoutReader, "orc_codex_observer", "orc_codex_observer-01", "reviewer")
	}()
	fmt.Fprintln(stdoutWriter, `{"type":"item.started","item":{"id":"cmd_1","type":"command_execution","command":"python -m slow_build --workspace demo","status":"running"}}`)

	deadline := time.Now().Add(time.Second)
	var events []protocol.OrchestrationEventPayload
	for time.Now().Before(deadline) {
		events = append(events, drainOrchestrationEvents(t, out)...)
		for _, event := range events {
			if event.Kind == "turn.delta" && event.Source == "bridge" && event.BridgeNoteData != nil && event.BridgeNoteData.Category == "long-command-observer-visible-note" {
				if !strings.Contains(event.BridgeNoteData.InjectedText, "ProofBridge observer note") {
					t.Fatalf("observer note missing sentinel: %#v", event)
				}
				if !strings.Contains(event.BridgeNoteData.Command, "python -m slow_build --workspace demo") {
					t.Fatalf("observer note missing command: %#v", event)
				}
				_ = stdoutWriter.Close()
				<-done
				if scanErr != nil {
					t.Fatal(scanErr)
				}
				if len(result.Tools) != 1 {
					t.Fatalf("tools = %#v", result.Tools)
				}
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = stdoutWriter.Close()
	<-done
	t.Fatalf("Codex observer bridge note was not emitted: %#v", events)
}

func TestCodexJSONLFailedCommandWithoutFollowupFails(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	input := strings.NewReader(strings.Join([]string{
		`{"type":"item.agent_message.delta","delta":"继续编译。"}`,
		`{"type":"item.started","item":{"id":"cmd_1","type":"command_execution","command":"coqc -R . LinLattice HWQ_U/L0Proof.v","status":"running"}}`,
		`{"type":"item.completed","item":{"id":"cmd_1","type":"command_execution","command":"coqc -R . LinLattice HWQ_U/L0Proof.v","status":"failed","exit_code":1,"aggregated_output":"Error: Unable to unify proof state.\n"}}`,
		"",
	}, "\n"))

	result, err := manager.scanCodexJSONLResult(input, "orc_codex_failed_tail", "orc_codex_failed_tail-01", "reviewer")
	if err == nil {
		t.Fatal("expected failed command without follow-up to fail the scanner")
	}
	for _, want := range []string{"coqc -R . LinLattice HWQ_U/L0Proof.v", "Unable to unify", "exit code 1", "without a follow-up response"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
	if result.Content != "继续编译。" {
		t.Fatalf("content = %q", result.Content)
	}
	if len(result.Tools) != 2 || !runnerToolEventFailed(result.Tools[1]) {
		t.Fatalf("tools = %#v", result.Tools)
	}
}

func TestCodexJSONLFailedCommandWithFollowupCompletes(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	input := strings.NewReader(strings.Join([]string{
		`{"type":"item.agent_message.delta","delta":"继续编译。"}`,
		`{"type":"item.started","item":{"id":"cmd_1","type":"command_execution","command":"coqc -R . LinLattice HWQ_U/L0Proof.v","status":"running"}}`,
		`{"type":"item.completed","item":{"id":"cmd_1","type":"command_execution","command":"coqc -R . LinLattice HWQ_U/L0Proof.v","status":"failed","exit_code":1,"aggregated_output":"Error: Unable to unify proof state.\n"}}`,
		`{"type":"item.agent_message.delta","delta":"我会根据这个错误继续修正。"}`,
		"",
	}, "\n"))

	result, err := manager.scanCodexJSONLResult(input, "orc_codex_handled_tail", "orc_codex_handled_tail-01", "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Content, "我会根据这个错误继续修正。") {
		t.Fatalf("content = %q", result.Content)
	}
	if len(result.Tools) != 2 || !runnerToolEventFailed(result.Tools[1]) {
		t.Fatalf("tools = %#v", result.Tools)
	}
}

func TestClaudeStreamInputClosesAfterIdleWindowWithoutInterruptingProcess(t *testing.T) {
	manager := NewOrchestrationManager(&config.Config{})
	out := make(chan protocol.Envelope, 16)
	manager.AttachOut(out)
	stdoutReader, stdoutWriter := io.Pipe()
	stdin := &syncWriteCloser{}

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
	for time.Now().Before(deadline) && !stdin.isClosed() {
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Fprintln(stdoutWriter, `{"type":"result","result":"开始处理"}`)
	stdoutWriter.Close()
	<-done

	if scanErr != nil {
		t.Fatal(scanErr)
	}
	if !stdin.isClosed() {
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

func TestOrchestrationCancelStopsActiveClaudeInteractiveSession(t *testing.T) {
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "claude.pid")
	claudePath := filepath.Join(tmp, "claude")
	if err := os.WriteFile(claudePath, []byte(fakeBlockingClaudeStreamScript(marker)), 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CWD = tmp
	cfg.Bridge.Sandbox = "danger-full-access"
	cfg.Bridge.ApprovalPolicy = "never"
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 32)
	manager.AttachOut(out)

	done := make(chan struct{}, 1)
	go func() {
		manager.Start(protocol.OrchestrationStartPayload{
			RunID:    "orc_cancel_claude",
			Mode:     "collaboration",
			FirstCLI: "claude",
			Prompt:   "start blocking claude",
			MaxTurns: 1,
			CWD:      tmp,
		})
		done <- struct{}{}
	}()
	<-done

	if !waitForOrchestrationEvent(t, out, "turn.start", "claude", "Starting Claude") {
		t.Fatal("Claude turn did not start")
	}
	pid := waitForPIDFile(t, marker)

	cancelDone := make(chan struct{})
	go func() {
		manager.Cancel("orc_cancel_claude")
		close(cancelDone)
	}()
	select {
	case <-cancelDone:
	case <-time.After(time.Second):
		t.Fatal("Cancel blocked while Claude turn was active")
	}

	waitForProcessExit(t, pid)
	if !waitForOrchestrationEvent(t, out, "run.cancelled", "", "") {
		t.Fatal("run.cancelled was not emitted after Claude cancellation")
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

func TestComposeRelayPromptUsesCodexFirstProofStrategy(t *testing.T) {
	prompt := composeRelayPromptWithFirstCLI(
		"debate",
		"codex",
		"formal-proof",
		"把 Model.thy Termination.thy ROOT 做成 Coq 项目，补全 termination modify_lin 的证明，不能用占位符。",
		"",
		false,
		"critic",
		"codex",
		1,
		4,
		nil,
	)
	for _, want := range []string{
		"Initial orchestration strategy for this formal-proof task",
		"Use proposer/critic flow",
		"Because Codex starts first",
		"verifier/planner first",
		"Stop blind proof search after three failed strategies",
		"First CLI: codex",
		"交接总结",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("codex-first proof prompt missing %q:\n%s", want, prompt)
		}
	}
}

func TestRelayModeRoleContractsAndFinalSynthesis(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		role     string
		turn     int
		maxTurns int
		wants    []string
	}{
		{name: "collaboration implementer", mode: "collaboration", role: "implementer", turn: 1, maxTurns: 4, wants: []string{"Collaboration contract (cycle 1)", "Implementer duty", "root cause", "focused validation"}},
		{name: "collaboration reviewer first", mode: "collaboration", role: "reviewer", turn: 1, maxTurns: 4, wants: []string{"Reviewer duty", "independently inspect", "No prior implementation exists", "baseline"}},
		{name: "collaboration later cycle", mode: "collaboration", role: "implementer", turn: 3, maxTurns: 4, wants: []string{"cycle 2", "resolve the prior review evidence"}},
		{name: "debate proposer", mode: "debate", role: "proposer", turn: 1, maxTurns: 4, wants: []string{"Debate contract (cycle 1)", "Proposer duty", "falsifiable claim", "assumptions"}},
		{name: "debate critic first", mode: "debate", role: "critic", turn: 1, maxTurns: 4, wants: []string{"Critic duty", "counterexamples", "No prior proposal exists", "acceptance criteria"}},
		{name: "debate later cycle", mode: "debate", role: "critic", turn: 4, maxTurns: 6, wants: []string{"cycle 2", "strongest unresolved counterargument"}},
		{name: "unknown mode fallback", mode: "other", role: "implementer", turn: 1, maxTurns: 2, wants: []string{"Collaboration contract", "Implementer duty"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prompt := composeRelayPromptWithFirstCLI(test.mode, "claude", "default", "fix the task", "", false, test.role, "claude", test.turn, test.maxTurns, nil)
			for _, want := range test.wants {
				if !strings.Contains(prompt, want) {
					t.Fatalf("prompt missing %q:\n%s", want, prompt)
				}
			}
		})
	}

	finalPrompt := composeRelayPromptWithFirstCLI("debate", "claude", "default", "decide", "", false, "critic", "codex", 4, 4, nil)
	for _, want := range []string{"last scheduled turn", "最终结论", "compares claims with actual command evidence", "Do not hand work to another CLI"} {
		if !strings.Contains(finalPrompt, want) {
			t.Fatalf("final prompt missing %q:\n%s", want, finalPrompt)
		}
	}
	if strings.Contains(finalPrompt, "single most useful next step for the following CLI") {
		t.Fatalf("final prompt still requests a nonexistent next-worker handoff:\n%s", finalPrompt)
	}
}

func TestFormalProofModeContractsUseProofStateAndReproducibleEvidence(t *testing.T) {
	tests := []struct {
		name  string
		mode  string
		role  string
		turn  int
		wants []string
	}{
		{name: "collaboration author", mode: "collaboration", role: "implementer", turn: 1, wants: []string{"Formal-proof collaboration contract (cycle 1)", "Proof author duty", "initial proof state", "unchanged", "dependency audit", "obligation ledger"}},
		{name: "collaboration auditor", mode: "collaboration", role: "reviewer", turn: 2, wants: []string{"Proof auditor duty", "exact statement", "remaining goals", "sorry/admit/Admitted", "compile-only"}},
		{name: "collaboration later", mode: "collaboration", role: "implementer", turn: 3, wants: []string{"cycle 2", "unresolved goal", "instead of restarting proof search"}},
		{name: "debate proposer", mode: "debate", role: "proposer", turn: 1, wants: []string{"Formal-proof debate contract (cycle 1)", "Proof proposer duty", "falsifiable claim", "before/after proof state", "retract"}},
		{name: "debate critic", mode: "debate", role: "critic", turn: 2, wants: []string{"Proof critic duty", "quantifiers", "counterexample", "hidden axioms/oracles", "failed checker"}},
		{name: "debate later", mode: "debate", role: "critic", turn: 4, wants: []string{"cycle 2", "strongest surviving objection", "new proof-state or checker evidence"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prompt := composeRelayPromptWithFirstCLI(test.mode, "claude", "formal-proof", "prove theorem target in Lean", "", false, test.role, "claude", test.turn, 4, nil)
			for _, want := range test.wants {
				if !strings.Contains(prompt, want) {
					t.Fatalf("formal-proof prompt missing %q:\n%s", want, prompt)
				}
			}
		})
	}

	finalPrompt := composeRelayPromptWithFirstCLI("debate", "claude", "formal-proof", "prove theorem target in Lean", "", false, "critic", "codex", 4, 4, nil)
	for _, want := range []string{"final formal-proof turn", "adversarial proof adjudication", "unchanged target statement", "dependency/trust audit", "remaining goals is not a completed proof", "Do not hand work"} {
		if !strings.Contains(finalPrompt, want) {
			t.Fatalf("formal-proof final prompt missing %q:\n%s", want, finalPrompt)
		}
	}
}

func TestRelayTurnPlanKeepsRoleOrderIndependentOfFirstCLI(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		workerPair string
		firstCLI   string
		turn       int
		wantRole   string
		wantCLI    string
		wantSlot   string
	}{
		{name: "collaboration claude first", mode: "collaboration", firstCLI: "claude", turn: 1, wantRole: "implementer", wantCLI: "claude", wantSlot: "claude"},
		{name: "collaboration codex first", mode: "collaboration", firstCLI: "codex", turn: 1, wantRole: "implementer", wantCLI: "codex", wantSlot: orchestrationCodexDefaultSlot},
		{name: "collaboration codex second", mode: "collaboration", firstCLI: "codex", turn: 2, wantRole: "reviewer", wantCLI: "claude", wantSlot: "claude"},
		{name: "debate claude first", mode: "debate", firstCLI: "claude", turn: 1, wantRole: "proposer", wantCLI: "claude", wantSlot: "claude"},
		{name: "debate codex first", mode: "debate", firstCLI: "codex", turn: 1, wantRole: "proposer", wantCLI: "codex", wantSlot: orchestrationCodexDefaultSlot},
		{name: "debate codex second", mode: "debate", firstCLI: "codex", turn: 2, wantRole: "critic", wantCLI: "claude", wantSlot: "claude"},
		{name: "codex pair collaboration first", mode: "collaboration", workerPair: protocol.WorkerPairCodexCodex, firstCLI: "claude", turn: 1, wantRole: "implementer", wantCLI: "codex", wantSlot: orchestrationCodexSlotA},
		{name: "codex pair collaboration second", mode: "collaboration", workerPair: protocol.WorkerPairCodexCodex, firstCLI: "claude", turn: 2, wantRole: "reviewer", wantCLI: "codex", wantSlot: orchestrationCodexSlotB},
		{name: "codex pair debate first", mode: "debate", workerPair: protocol.WorkerPairCodexCodex, firstCLI: "claude", turn: 1, wantRole: "proposer", wantCLI: "codex", wantSlot: orchestrationCodexSlotA},
		{name: "codex pair debate second", mode: "debate", workerPair: protocol.WorkerPairCodexCodex, firstCLI: "claude", turn: 2, wantRole: "critic", wantCLI: "codex", wantSlot: orchestrationCodexSlotB},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := relayTurnPlan(test.workerPair, test.mode, test.firstCLI, test.turn)
			if got.Role != test.wantRole || got.CLI != test.wantCLI || got.WorkerSlot != test.wantSlot {
				t.Fatalf("turn plan = %#v, want role=%q cli=%q slot=%q", got, test.wantRole, test.wantCLI, test.wantSlot)
			}
		})
	}
}

func TestFormalProofInitialStrategyDoesNotRequireFourTurns(t *testing.T) {
	prompt := composeRelayPromptWithFirstCLI(
		"collaboration",
		"codex",
		"formal-proof",
		"用 Coq 证明列表反转定理。",
		"",
		false,
		"implementer",
		"codex",
		1,
		2,
		nil,
	)
	if !strings.Contains(prompt, "Initial orchestration strategy for this formal-proof task") {
		t.Fatalf("two-turn formal proof prompt omitted initial strategy:\n%s", prompt)
	}
}

func TestRelayHistoryBudgetKeepsNewestUTF8Evidence(t *testing.T) {
	history := make([]orchestrationTurn, 0, 12)
	for index := 0; index < 12; index++ {
		marker := fmt.Sprintf("证据-%02d", index)
		history = append(history, orchestrationTurn{
			Role:    "reviewer",
			CLI:     "codex",
			Content: marker + strings.Repeat("复杂证明上下文", 500),
		})
	}
	prompt := composeRelayPromptWithFirstCLI("collaboration", "claude", "default", "继续", "", false, "reviewer", "codex", 12, 12, history)
	if len(prompt) > relayHistoryPromptBudget+5000 {
		t.Fatalf("bounded prompt grew to %d bytes", len(prompt))
	}
	if !strings.Contains(prompt, "证据-11") {
		t.Fatalf("newest evidence was not retained:\n%s", prompt)
	}
	if strings.Contains(prompt, "证据-00") {
		t.Fatal("oldest oversized evidence should have been dropped")
	}
	if !utf8.ValidString(prompt) {
		t.Fatal("history truncation produced invalid UTF-8")
	}
}

func TestReturningWorkerReceivesOnlyLatestCrossWorkerHandoff(t *testing.T) {
	exit := 0
	history := []orchestrationTurn{
		{Role: "implementer", CLI: "codex", WorkerSlot: orchestrationCodexDefaultSlot, Content: "codex-old-private-result"},
		{
			Role: "reviewer", CLI: "claude", WorkerSlot: "claude",
			Content: "claude-current-review", Handoff: "请修复边界条件并复测。",
			Tools: []RunnerToolEvent{{Command: "go test ./internal/bridge", Status: "completed", ExitCode: &exit, Output: "ok"}},
		},
		{Role: "implementer", CLI: "codex", WorkerSlot: orchestrationCodexDefaultSlot, Content: "codex-newer-private-result"},
	}
	prompt := composeRelayPromptWithWorkerSlot("collaboration", "codex", "default", "继续修复", "", false, "implementer", "codex", orchestrationCodexDefaultSlot, 4, 4, history)
	for _, want := range []string{"请修复边界条件并复测", "go test ./internal/bridge", "exit=0"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("returning worker prompt missing %q:\n%s", want, prompt)
		}
	}
	for _, redundant := range []string{"codex-old-private-result", "codex-newer-private-result"} {
		if strings.Contains(prompt, redundant) {
			t.Fatalf("returning worker received redundant own result %q:\n%s", redundant, prompt)
		}
	}
}

func TestFirstWorkerEntryRetainsNecessaryRelayHistory(t *testing.T) {
	history := []orchestrationTurn{
		{Role: "implementer", CLI: "claude", WorkerSlot: "claude", Content: "baseline-evidence"},
		{Role: "reviewer", CLI: "codex", WorkerSlot: orchestrationCodexSlotA, Content: "latest-review-evidence"},
	}
	prompt := composeRelayPromptWithWorkerSlot("collaboration", "claude", "default", "继续", "", false, "reviewer", "codex", orchestrationCodexSlotB, 3, 4, history)
	for _, want := range []string{"baseline-evidence", "latest-review-evidence"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("first worker entry omitted %q:\n%s", want, prompt)
		}
	}
}

func TestDurableTaskPromptUsesOuterRoundAndActiveRole(t *testing.T) {
	tests := []struct {
		name    string
		scope   durableTaskPromptScope
		want    []string
		notWant []string
	}{
		{
			name:  "candidate in intermediate round",
			scope: durableTaskPromptScope{Name: "candidate-a", Role: store.TaskRoleWorker, Round: 2, MaxRounds: 4},
			want: []string{
				"Candidate A duty", "collaboration round 2 of 4", "Maximize useful progress",
				"materially same obstacle recurs", "later durable node or collaboration round",
			},
			notWant: []string{"You are the first CLI", "final formal-proof turn", "Relay turn: 1 of 1", "three failed strategies"},
		},
		{
			name:    "integrator keeps implementing",
			scope:   durableTaskPromptScope{Name: "integrate", Role: store.TaskRoleIntegrator, Round: 3, MaxRounds: 4},
			want:    []string{"Integrator duty", "continue implementing", "not a summary-only coordinator"},
			notWant: []string{"final configured round's independent review"},
		},
		{
			name:    "intermediate reviewer hands to peer",
			scope:   durableTaskPromptScope{Name: "review", Role: store.TaskRoleReviewer, Round: 3, MaxRounds: 4},
			want:    []string{"Independent Reviewer duty", "directly fix safe in-scope defects", "to=<peer>"},
			notWant: []string{"final formal-proof turn", "to=<user>"},
		},
		{
			name:    "only final reviewer synthesizes",
			scope:   durableTaskPromptScope{Name: "review", Role: store.TaskRoleReviewer, Round: 4, MaxRounds: 4},
			want:    []string{"final formal-proof turn", "to=<user>", "intent=<final"},
			notWant: []string{"You are the first CLI", "Relay turn: 1 of 1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prompt := composeRelayPromptWithTaskScope("collaboration", "codex", "formal-proof", "prove theorem Main", "", false, test.scope.Role, "codex", orchestrationCodexSlotA, 1, 1, nil, &test.scope)
			for _, want := range test.want {
				if !strings.Contains(prompt, want) {
					t.Fatalf("task prompt missing %q:\n%s", want, prompt)
				}
			}
			for _, notWant := range test.notWant {
				if strings.Contains(prompt, notWant) {
					t.Fatalf("task prompt unexpectedly contains %q:\n%s", notWant, prompt)
				}
			}
		})
	}
}

func TestRelayCommandSummariesCoalesceLifecycleAndKeepRecentAudit(t *testing.T) {
	exit := 0
	tools := []RunnerToolEvent{
		{ID: "build", Command: "coqc Main.v", Status: "running"},
		{ID: "build", Command: "coqc Main.v", Status: "completed", ExitCode: &exit, Output: "ok"},
	}
	for index := 0; index < 7; index++ {
		tools = append(tools, RunnerToolEvent{ID: fmt.Sprintf("probe-%d", index), Command: fmt.Sprintf("probe %d", index), Status: "completed", ExitCode: &exit})
	}
	tools = append(tools, RunnerToolEvent{ID: "audit", Command: "coqtop -quiet < Assumptions.v", Status: "completed", ExitCode: &exit, Output: "Closed under the global context"})
	summaries := relayCommandSummaries(tools, 6)
	joined := strings.Join(summaries, "\n")
	if strings.Contains(joined, "running") {
		t.Fatalf("stale lifecycle state leaked into summaries:\n%s", joined)
	}
	if strings.Count(joined, "coqc Main.v") > 1 {
		t.Fatalf("command lifecycle was not coalesced:\n%s", joined)
	}
	for _, want := range []string{"coqtop -quiet", "Closed under the global context", "exit=0"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("recent audit evidence missing %q:\n%s", want, joined)
		}
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

func TestPrepareOrchestrationPromptFilesWritesArchiveUploads(t *testing.T) {
	cfg := config.Default()
	runCWD := t.TempDir()
	raw := []byte("PK\x03\x04archive fixture")

	prompt, metas, err := PrepareOrchestrationPromptFiles(&cfg, runCWD, "orc_archive", "inspect archive", []protocol.AttachmentPayload{{
		Name:     "project bundle.zip",
		MimeType: "application/zip",
		Size:     int64(len(raw)),
		Data:     base64.StdEncoding.EncodeToString(raw),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(metas) != 1 || metas[0].Name != "project bundle.zip" || metas[0].MimeType != "application/zip" || metas[0].Size != int64(len(raw)) {
		t.Fatalf("metas = %#v", metas)
	}
	wantPath := filepath.Join(runCWD, ".codex-bridge", "orchestrations", "orc_archive", "01-project-bundle.zip")
	got, err := os.ReadFile(wantPath)
	if err != nil {
		t.Fatalf("archive upload not written: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatalf("archive bytes = %q, want %q", got, raw)
	}
	if !strings.Contains(prompt, wantPath) {
		t.Fatalf("prompt missing archive path %q:\n%s", wantPath, prompt)
	}
}

func TestFormalProofWorkspaceCreatesOnlyProjectAndNotes(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	payload := protocol.OrchestrationStartPayload{
		RunID:     "orc_text",
		Mode:      "collaboration",
		Prompt:    "从 0 开始创建 Lean4 项目，证明 reverse reverse。",
		MaxTurns:  4,
		Profile:   "formal-proof",
		PromptSeq: 1,
	}

	result, err := prepareFormalProofHarness(&cfg, payload, tmp)
	if err != nil {
		t.Fatal(err)
	}
	wantRunDir := tmp
	if result.RunDir != wantRunDir {
		t.Fatalf("run dir = %q, want %q", result.RunDir, wantRunDir)
	}
	if result.ProjectDir != tmp {
		t.Fatalf("project dir = %q, want selected cwd %q", result.ProjectDir, tmp)
	}
	if wantNotes := filepath.Join(tmp, ".codex-bridge", "proof-notes", "orc_text.md"); result.NotesPath != wantNotes {
		t.Fatalf("notes path = %q, want %q", result.NotesPath, wantNotes)
	}
	notes, err := os.ReadFile(result.NotesPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"原始任务", "从 0 开始创建 Lean4 项目", "目标与未解决义务", "验证证据", "关键决策", "lean4"} {
		if !strings.Contains(string(notes), want) {
			t.Fatalf("notes missing %q:\n%s", want, notes)
		}
	}
	for _, forbidden := range []string{"check.sh", "状态.yaml", "证明决策/"} {
		if strings.Contains(result.Prompt, forbidden) {
			t.Fatalf("prompt still requires removed artifact %q:\n%s", forbidden, result.Prompt)
		}
	}
	if !strings.Contains(result.Prompt, "Formal-proof workspace") || !strings.Contains(result.Prompt, "proof-notes/orc_text.md") || !strings.Contains(result.Prompt, "Work directly in the selected project directory") {
		t.Fatalf("prompt missing lightweight workspace instructions:\n%s", result.Prompt)
	}
}

func TestFormalProofWorkspaceMaterializesUploadedProjectFiles(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	payload := protocol.OrchestrationStartPayload{
		RunID:   "orc_upload",
		Prompt:  "补全 termination modify_lin。",
		Profile: "formal-proof",
		Files: []protocol.AttachmentPayload{
			{Name: "Model.thy", MimeType: "application/octet-stream", Size: int64(len("theory Model\n")), Data: base64.StdEncoding.EncodeToString([]byte("theory Model\n"))},
			{Name: "Termination.thy", MimeType: "application/octet-stream", Size: int64(len("theory Termination\n")), Data: base64.StdEncoding.EncodeToString([]byte("theory Termination\n"))},
			{Name: "ROOT", MimeType: "application/octet-stream", Size: int64(len("session Demo\n")), Data: base64.StdEncoding.EncodeToString([]byte("session Demo\n"))},
		},
	}

	result, err := prepareFormalProofHarness(&cfg, payload, tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"Model.thy", "Termination.thy", "ROOT"} {
		if _, err := os.Stat(filepath.Join(result.ProjectDir, rel)); err != nil {
			t.Fatalf("uploaded file %s not materialized under project: %v", rel, err)
		}
	}
	if result.Assistant != "isabelle" {
		t.Fatalf("assistant = %q, want isabelle", result.Assistant)
	}
	notes, err := os.ReadFile(result.NotesPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"isabelle", "Model.thy", "Termination.thy", "ROOT"} {
		if !strings.Contains(string(notes), want) {
			t.Fatalf("notes missing %q:\n%s", want, notes)
		}
	}
}

func TestFormalProofWorkspaceCoqConversionTargetOverridesIsabelleInputs(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	result, err := prepareFormalProofHarness(&cfg, protocol.OrchestrationStartPayload{
		RunID:   "orc_isabelle_to_coq",
		Prompt:  "把上传的 Isabelle 工程转换成新的 Coq 证明项目，并用 coqc 验证。",
		Profile: "formal-proof",
		Files: []protocol.AttachmentPayload{
			{Name: "Model.thy", Size: int64(len("theory Model\n")), Data: base64.StdEncoding.EncodeToString([]byte("theory Model\n"))},
			{Name: "ROOT", Size: int64(len("session Demo\n")), Data: base64.StdEncoding.EncodeToString([]byte("session Demo\n"))},
		},
	}, tmp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Assistant != "coq" {
		t.Fatalf("assistant = %q, want target Coq despite Isabelle source files", result.Assistant)
	}
	notes, err := os.ReadFile(result.NotesPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(notes), "coq") {
		t.Fatalf("notes did not persist target proof assistant:\n%s", notes)
	}
}

func TestFormalProofWorkspaceExtractsZipAndRejectsTraversal(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	raw := zipFixture(t, map[string]string{
		"Proof.lean":     "theorem t : True := by trivial\n",
		"lakefile.lean":  "import Lake\n",
		"nested/Note.md": "notes\n",
	})
	payload := protocol.OrchestrationStartPayload{
		RunID:   "orc_zip",
		Prompt:  "检查 Lean4 项目。",
		Profile: "formal-proof",
		Files: []protocol.AttachmentPayload{{
			Name: "lean-project.zip", MimeType: "application/zip", Size: int64(len(raw)), Data: base64.StdEncoding.EncodeToString(raw),
		}},
	}

	result, err := prepareFormalProofHarness(&cfg, payload, tmp)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"Proof.lean", "lakefile.lean", filepath.Join("nested", "Note.md")} {
		if _, err := os.Stat(filepath.Join(result.ProjectDir, rel)); err != nil {
			t.Fatalf("zip entry %s not extracted: %v", rel, err)
		}
	}
	if result.Assistant != "lean4" {
		t.Fatalf("assistant = %q, want lean4", result.Assistant)
	}

	badRaw := zipFixture(t, map[string]string{"../escape.v": "Axiom bad : True.\n"})
	_, err = prepareFormalProofHarness(&cfg, protocol.OrchestrationStartPayload{
		RunID: "orc_bad_zip", Prompt: "bad", Profile: "formal-proof",
		Files: []protocol.AttachmentPayload{{
			Name: "bad.zip", MimeType: "application/zip", Size: int64(len(badRaw)), Data: base64.StdEncoding.EncodeToString(badRaw),
		}},
	}, tmp)
	if err == nil || !strings.Contains(err.Error(), "unsafe archive path") {
		t.Fatalf("expected unsafe archive path error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(tmp, ".codex-bridge", "proof-notes", "escape.v")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive traversal wrote outside project, stat err=%v", err)
	}
}

func TestFormalProofWorkspaceResumeReusesRunCWDAndAppendsNotes(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	initial, err := prepareFormalProofHarness(&cfg, protocol.OrchestrationStartPayload{
		RunID: "orc_resume", Prompt: "初始 Coq 任务。", Profile: "formal-proof", PromptSeq: 1,
		Files: []protocol.AttachmentPayload{{
			Name: "Main.v", MimeType: "application/octet-stream",
			Size: int64(len("Theorem t : True.\nProof. exact I. Qed.\n")),
			Data: base64.StdEncoding.EncodeToString([]byte("Theorem t : True.\nProof. exact I. Qed.\n")),
		}},
	}, tmp)
	if err != nil {
		t.Fatal(err)
	}
	resumed, err := prepareFormalProofHarness(&cfg, protocol.OrchestrationStartPayload{
		RunID: "orc_resume", Prompt: "继续去掉剩余 Admitted。", Profile: "formal-proof",
		Resume: true, RunCWD: initial.RunDir, PromptSeq: 2,
	}, initial.RunDir)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.RunDir != initial.RunDir {
		t.Fatalf("resumed run dir = %q, want existing %q", resumed.RunDir, initial.RunDir)
	}
	notes, err := os.ReadFile(initial.NotesPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"初始 Coq 任务", "请求 2", "继续去掉剩余 Admitted"} {
		if !strings.Contains(string(notes), want) {
			t.Fatalf("follow-up notes missing %q:\n%s", want, notes)
		}
	}
	if !strings.Contains(resumed.Prompt, "same project directory") {
		t.Fatalf("resumed prompt missing same-run instruction:\n%s", resumed.Prompt)
	}
}

func TestFormalProofFollowupsStayBoundedAndPreserveWorkerEvidence(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.Default()
	initial, err := prepareFormalProofHarness(&cfg, protocol.OrchestrationStartPayload{
		RunID: "orc_bounded_notes", Prompt: "初始 Lean 任务。", Profile: "formal-proof", PromptSeq: 1,
	}, tmp)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(initial.NotesPath)
	if err != nil {
		t.Fatal(err)
	}
	workerEvidence := "- worker 保留的验证证据：lake build exit=0。"
	updated := strings.Replace(string(raw), "## 验证证据\n", "## 验证证据\n\n"+workerEvidence+"\n", 1)
	if err := os.WriteFile(initial.NotesPath, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < 20; index++ {
		_, err := prepareFormalProofHarness(&cfg, protocol.OrchestrationStartPayload{
			RunID: "orc_bounded_notes", Prompt: fmt.Sprintf("followup-%02d %s", index, strings.Repeat("证明上下文", 800)),
			Profile: "formal-proof", Resume: true, RunCWD: initial.RunDir, PromptSeq: int64(index + 2),
		}, initial.RunDir)
		if err != nil {
			t.Fatal(err)
		}
	}
	finalNotes, err := os.ReadFile(initial.NotesPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(finalNotes)
	for _, want := range []string{workerEvidence, "- 后续请求总数：20", "- 已压缩较早请求：12", "请求 21", "followup-19"} {
		if !strings.Contains(content, want) {
			t.Fatalf("bounded notes missing %q:\n%s", want, content)
		}
	}
	for _, dropped := range []string{"followup-00", "followup-11"} {
		if strings.Contains(content, dropped) {
			t.Fatalf("old follow-up %q was not compacted", dropped)
		}
	}
	if strings.Count(content, formalProofFollowupStart) != 1 || strings.Count(content, formalProofFollowupEnd) != 1 {
		t.Fatalf("follow-up marker block count is invalid:\n%s", content)
	}
	if len(finalNotes) > 24*1024 {
		t.Fatalf("proof notes grew beyond bounded follow-up window: %d bytes", len(finalNotes))
	}
}

func TestFormalProofFollowupAdoptsLegacyUnmarkedEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), formalProofNotesFileName)
	legacy := "# Formal Proof Notes\n\n## 验证证据\n\n- legacy worker evidence\n\n## 后续请求\n\n- 暂无。\n\n### 请求 2\n\n```text\n旧请求\n```\n"
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appendFormalProofFollowup(protocol.OrchestrationStartPayload{Prompt: "新请求", PromptSeq: 3}, formalProofHarnessResult{NotesPath: path}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	content := string(raw)
	for _, want := range []string{"legacy worker evidence", "- 后续请求总数：2", "请求 2", "旧请求", "请求 3", "新请求"} {
		if !strings.Contains(content, want) {
			t.Fatalf("migrated legacy notes missing %q:\n%s", want, content)
		}
	}
	if strings.Count(content, formalProofFollowupStart) != 1 || strings.Count(content, "### 请求 2") != 1 {
		t.Fatalf("legacy follow-up was duplicated:\n%s", content)
	}
}

func TestFormalProofRunUsesLightweightWorkspaceWithoutConsumingTurns(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	claudePromptPath := filepath.Join(tmp, "claude_prompt.txt")
	codexPromptPath := filepath.Join(tmp, "codex_prompt.txt")
	if err := os.WriteFile(claudePath, []byte(fakeClaudeRelayScript(claudePromptPath, filepath.Join(tmp, "claude_argv.json"))), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexRelayScript(codexPromptPath, filepath.Join(tmp, "codex_argv.json"))), 0o755); err != nil {
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
		RunID: "orc_workspace_run", Mode: "collaboration",
		Prompt: "补全 Coq theorem，不允许 Admitted。", Profile: "formal-proof", MaxTurns: 2, CWD: tmp,
	})

	events := drainOrchestrationEvents(t, out)
	var runStart protocol.OrchestrationEventPayload
	var turnStarts int
	var sawBootstrap, sawSync bool
	for _, event := range events {
		switch event.Kind {
		case "run.start":
			runStart = event
		case "turn.start":
			turnStarts++
		case "turn.delta":
			if event.BridgeNoteData != nil && event.BridgeNoteData.Category == "formal-proof-harness-bootstrap" {
				sawBootstrap = true
			}
			if event.BridgeNoteData != nil && event.BridgeNoteData.Category == "formal-proof-harness-sync" {
				sawSync = true
			}
		}
	}
	wantRunDir := tmp
	if runStart.RunStartData == nil || runStart.RunStartData.CWD != wantRunDir {
		t.Fatalf("run.start cwd = %#v, want %q", runStart.RunStartData, wantRunDir)
	}
	if turnStarts != 2 {
		t.Fatalf("bootstrap should not consume scheduled turns, saw %d turn.start events", turnStarts)
	}
	if !sawBootstrap {
		t.Fatalf("bootstrap note not emitted: %#v", events)
	}
	if sawSync {
		t.Fatalf("removed harness sync note was emitted: %#v", events)
	}
	claudePrompt, err := os.ReadFile(claudePromptPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(claudePrompt), "Formal-proof workspace") || !strings.Contains(string(claudePrompt), "proof-notes/orc_workspace_run.md") || !strings.Contains(string(claudePrompt), "Work directly in the selected project directory") {
		t.Fatalf("first CLI prompt missing lightweight workspace context:\n%s", claudePrompt)
	}
	if strings.Contains(string(claudePrompt), "check.sh") {
		t.Fatalf("first CLI prompt still requires generated checker:\n%s", claudePrompt)
	}
}

func TestDefaultProfileStillUsesLegacyUploadMaterialization(t *testing.T) {
	cfg := config.Default()
	cfg.Bridge.CWD = filepath.Join(t.TempDir(), "configured")
	runCWD := filepath.Join(t.TempDir(), "plain-run")
	prompt, _, err := PrepareOrchestrationPromptFiles(&cfg, runCWD, "orc_plain", "read this", []protocol.AttachmentPayload{{
		Name:     "Main.v",
		MimeType: "application/octet-stream",
		Size:     int64(len("Theorem t : True.")),
		Data:     base64.StdEncoding.EncodeToString([]byte("Theorem t : True.")),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, filepath.Join(runCWD, ".codex-bridge", "orchestrations", "orc_plain", "01-Main.v")) {
		t.Fatalf("default profile upload prompt changed unexpectedly:\n%s", prompt)
	}
	if strings.Contains(prompt, "Formal-proof harness workspace") {
		t.Fatalf("default profile prompt should not include harness:\n%s", prompt)
	}
}

func zipFixture(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
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

func TestCleanOrchestrationTurnContentTrimsRepeatedProgressBeforeConclusion(t *testing.T) {
	content := strings.Join([]string{
		"我会只核对已报告的框架文件和验证命令结果。",
		"我先只核对已报告变更的 ROOT 和 Termination.thy。",
		"我会只核对最终产物和验证记录，不做新的大范围证明工作。",
		"结论：上述内容不是完整、正确的终止性证明，只能算是一个可编译的证明框架。",
	}, "\n")
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

func TestExtractHandoffSummaryFindsTrailingSection(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "chinese label",
			content: "已经改完 main.go。\n\n交接总结：已修复登录回归并通过 go test ./...，无遗留阻塞，下一步请补充 e2e 用例。",
			want:    "已修复登录回归并通过 go test ./...，无遗留阻塞，下一步请补充 e2e 用例。",
		},
		{
			name:    "english label",
			content: "patched the parser\n\nHandoff summary: rebuilt and ran go vet; nothing blocked; next, add a regression test.",
			want:    "rebuilt and ran go vet; nothing blocked; next, add a regression test.",
		},
		{
			name:    "conclusion fallback",
			content: "构建过程……\n\n最终结论：定理已证明，go build 通过。",
			want:    "定理已证明，go build 通过。",
		},
		{
			name:    "markdown conclusion heading",
			content: "前面是执行过程。\n\n## 最终结论\n定理已证明，go build 通过。",
			want:    "定理已证明，go build 通过。",
		},
		{
			name:    "machine handoff label",
			content: "Msg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=parser; verified=go test ./...; next=none; risks=none",
			want:    "status=resolved; changed=parser; verified=go test ./...; next=none; risks=none",
		},
		{
			name:    "none",
			content: "just some prose without any summary marker.",
			want:    "",
		},
		{
			name:    "inline marker mention",
			content: "我检查了文件。sed 后段没有输出，说明文件比交接摘要里的行号更短。随后立即跑 coqc。",
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractHandoffSummary(tc.content); got != tc.want {
				t.Fatalf("extractHandoffSummary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRunConclusionUsesExplicitFinalHandoffStatus(t *testing.T) {
	tests := []struct {
		name        string
		content     string
		wantOutcome string
		wantsUnmet  []string
	}{
		{
			name:        "resolved",
			content:     "最终结论：验证通过。\nHandoff: status=resolved; changed=main.go; verified=go test ./...; next=none; risks=none",
			wantOutcome: "satisfied",
		},
		{
			name:        "needs next",
			content:     "最终结论：仍需继续。\nHandoff: status=needs_next; changed=none; verified=coqc Main.v; next=prove termination; risks=unjustified fuel bound",
			wantOutcome: "blocked",
			wantsUnmet:  []string{"prove termination", "unjustified fuel bound"},
		},
		{
			name:        "blocked",
			content:     "最终结论：环境阻塞。\nHandoff: status=blocked; changed=none; verified=none; next=install Isabelle; risks=compiler unavailable",
			wantOutcome: "blocked",
			wantsUnmet:  []string{"install Isabelle", "compiler unavailable"},
		},
		{
			name:        "inline text is not parsed",
			content:     "最终结论：正文提到 Handoff: status=blocked 但没有独立机器交接行。",
			wantOutcome: "satisfied",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			history := []orchestrationTurn{{Content: test.content}}
			conclusion := runConclusionForStatus(store.OrchestrationCompleted, test.content, history)
			if conclusion.Outcome != test.wantOutcome {
				t.Fatalf("outcome = %q, want %q", conclusion.Outcome, test.wantOutcome)
			}
			for _, want := range test.wantsUnmet {
				if !containsTestString(conclusion.UnmetObligations, want) {
					t.Fatalf("unmet obligations %#v missing %q", conclusion.UnmetObligations, want)
				}
			}
		})
	}
}

func TestFailedFinalReviewerWithRemainingWorkConcludesBlocked(t *testing.T) {
	history := []orchestrationTurn{{Content: "最终结论：轮次耗尽。\nHandoff: status=needs_next; changed=Main.v; verified=coqc Main.v; next=replace Admitted; risks=theorem incomplete"}}
	conclusion := runConclusionForStatus(store.OrchestrationFailed, "configured collaboration rounds exhausted", history)
	if conclusion.Outcome != "blocked" {
		t.Fatalf("outcome = %q, want blocked", conclusion.Outcome)
	}
	for _, want := range []string{"replace Admitted", "theorem incomplete"} {
		if !containsTestString(conclusion.UnmetObligations, want) {
			t.Fatalf("unmet obligations %#v missing %q", conclusion.UnmetObligations, want)
		}
	}
}

func TestParseOrchestrationRelayPacketRequiresAnchoredMessageAndHandoff(t *testing.T) {
	content := "已完成修复。\n\nMsg: to=reviewer; intent=review; need=verify parser\nHandoff: status=needs_next; changed=parser.go; verified=go test ./...; next=review edge; risks=none"
	packet := parseOrchestrationRelayPacket(content)
	if !packet.Structured || packet.To != "reviewer" || packet.Intent != "review" || packet.Need != "verify parser" {
		t.Fatalf("message packet = %#v", packet)
	}
	if packet.Status != "needs_next" || packet.Changed != "parser.go" || packet.Verified != "go test ./..." || packet.Next != "review edge" || packet.Risks != "none" {
		t.Fatalf("handoff packet = %#v", packet)
	}

	for _, malformed := range []string{
		"正文提到 Msg: to=reviewer，但不是独立行。\nHandoff: status=needs_next; next=review",
		"Msg: to=reviewer; intent=review; need=verify",
		"Msg: to=reviewer; intent=review\nHandoff: changed=parser.go; next=review",
	} {
		if got := parseOrchestrationRelayPacket(malformed); got.Structured {
			t.Fatalf("malformed packet was accepted: %#v", got)
		}
	}
}

func TestFormatRelayPriorTurnUsesCompactStructuredPacket(t *testing.T) {
	exit := 0
	record := newOrchestrationTurnRecord(
		"turn-1",
		"implementer",
		"claude",
		"large implementation explanation that is already in the native Claude session\n\nMsg: to=reviewer; intent=review; need=check edge case\nHandoff: status=needs_next; changed=parser.go; verified=go test ./...; next=review parser; risks=none",
		[]RunnerToolEvent{{Command: "go test ./...", Status: "completed", ExitCode: &exit}},
	)
	out := formatRelayPriorTurn(record)
	for _, want := range []string{"message: to=reviewer", "intent=review", "need=check edge case", "state: status=needs_next", "changed=parser.go", "commands:", "go test ./..."} {
		if !strings.Contains(out, want) {
			t.Fatalf("compact relay missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "large implementation explanation") || strings.Contains(out, "result:") {
		t.Fatalf("structured relay repeated prior prose:\n%s", out)
	}
}

func TestRelayConvergenceRequiresReviewingRoleAndEvidence(t *testing.T) {
	exit := 0
	failed := 1
	needsReview := newOrchestrationTurnRecordWithSlot(
		"turn-1", "implementer", "claude", "claude",
		"交接总结：实现已完成。\nMsg: to=reviewer; intent=review; need=verify\nHandoff: status=resolved; changed=main.go; verified=go test ./...; next=none; risks=none", nil,
	)
	accepted := newOrchestrationTurnRecordWithSlot(
		"turn-2", "reviewer", "codex", orchestrationCodexDefaultSlot,
		"最终结论：复查通过。\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=main.go; verified=go test ./...; next=none; risks=none",
		[]RunnerToolEvent{{Command: "go test ./...", Status: "completed", ExitCode: &exit}},
	)
	if relayCanConverge("collaboration", "default", []orchestrationTurn{needsReview}) {
		t.Fatal("implementer self-certification converged")
	}
	if !relayCanConverge("collaboration", "default", []orchestrationTurn{needsReview, accepted}) {
		t.Fatal("evidenced reviewer acceptance did not converge")
	}

	rejected := accepted
	rejected.Relay.Risks = "edge case untested"
	if relayCanConverge("collaboration", "default", []orchestrationTurn{needsReview, rejected}) {
		t.Fatal("review with remaining risk converged")
	}
	rejected = accepted
	rejected.Tools = []RunnerToolEvent{{Command: "go test ./...", Status: "failed", ExitCode: &failed}}
	rejected.Relay.Verified = "none"
	if relayCanConverge("collaboration", "default", []orchestrationTurn{needsReview, rejected}) {
		t.Fatal("review without successful evidence converged")
	}

	critic := accepted
	critic.Role = "critic"
	if !relayCanConverge("debate", "default", []orchestrationTurn{needsReview, critic}) {
		t.Fatal("evidenced critic acceptance did not converge")
	}
}

func TestRelayRunStopsAfterReviewerConvergence(t *testing.T) {
	tmp := t.TempDir()
	claudePath := filepath.Join(tmp, "claude")
	codexPath := filepath.Join(tmp, "codex")
	implementer := "交接总结：实现已完成，请复查。\nMsg: to=reviewer; intent=review; need=verify fix\nHandoff: status=needs_next; changed=main.go; verified=none; next=review fix; risks=none"
	reviewer := "最终结论：独立复查通过。\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=main.go; verified=go test ./...; next=none; risks=none"
	if err := os.WriteFile(claudePath, []byte(fakeClaudePrintScript(implementer)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(codexPath, []byte(fakeCodexExecScriptWithSuccessfulCommand(reviewer)), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Bridge.ClaudePath = claudePath
	cfg.Bridge.CodexPath = codexPath
	cfg.Bridge.CWD = tmp
	manager := NewOrchestrationManager(&cfg)
	out := make(chan protocol.Envelope, 64)
	manager.AttachOut(out)
	defer manager.CloseAll()

	manager.run(context.Background(), protocol.OrchestrationStartPayload{
		RunID: "orc_converge", Mode: "collaboration", Prompt: "fix regression", MaxTurns: 4, CWD: tmp,
	})
	events := drainOrchestrationEvents(t, out)
	turnStarts := 0
	var runEnd protocol.OrchestrationEventPayload
	for _, event := range events {
		if event.Kind == "turn.start" {
			turnStarts++
		}
		if event.Kind == "run.end" {
			runEnd = event
		}
	}
	if turnStarts != 2 {
		t.Fatalf("turn starts = %d, want 2 after reviewer convergence; events=%#v", turnStarts, events)
	}
	if runEnd.RunConclusion == nil || runEnd.RunConclusion.Outcome != "satisfied" || !strings.Contains(runEnd.Content, "独立复查通过") {
		t.Fatalf("run.end = %#v", runEnd)
	}
	if runEnd.RunEndData == nil || runEnd.RunEndData.TerminalReason != "verified-early" || runEnd.RunEndData.VerifierVerdict == nil || runEnd.RunEndData.VerifierVerdict.Status != verifierVerdictPass {
		t.Fatalf("run end did not retain verified early verdict: %#v", runEnd.RunEndData)
	}
	foundVerdict := false
	for _, event := range events {
		if event.Kind == "verifier.verdict" {
			foundVerdict = true
			break
		}
	}
	if !foundVerdict {
		t.Fatalf("expected visible verifier verdict: %#v", events)
	}
}

func TestWorkerProfileRuntimesAreSlotIsolatedAndRetainOnlyResumeMetadata(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	manager := NewOrchestrationManager(&cfg)
	cliConfig, err := newCLIConfigManager(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetCLIConfigManager(cliConfig)
	payload := protocol.OrchestrationStartPayload{RunID: "orc/profile-isolation", WorkerPair: protocol.WorkerPairCodexCodex, WorkerProfiles: map[string]protocol.WorkerProfileBinding{
		"codex-a": {PresetID: "preset-a", CLI: "codex", BaseURL: "https://a.example/v1", Model: "model-a", ReasoningEffort: "low", ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "low", Secret: encryptCLIConfigSecretForTest(t, cliConfig.private.PublicKey(), []byte("key-a"))},
		"codex-b": {PresetID: "preset-b", CLI: "codex", BaseURL: "https://b.example/v1", Model: "model-b", ReasoningEffort: "high", ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "high", Secret: encryptCLIConfigSecretForTest(t, cliConfig.private.PublicKey(), []byte("key-b"))},
	}}
	session := manager.nativeSession(payload.RunID, home)
	first, err := manager.workerRuntime(payload, "codex-a", "codex", session)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.workerRuntime(payload, "codex-b", "codex", session)
	if err != nil {
		t.Fatal(err)
	}
	if first.dir == second.dir || first.model != "model-a" || second.model != "model-b" || !containsTestString(first.env, "CODEX_HOME="+filepath.Join(first.dir, "codex")) || !containsTestString(second.env, "CODEX_HOME="+filepath.Join(second.dir, "codex")) {
		t.Fatalf("isolated runtimes = first=%#v second=%#v", first, second)
	}
	firstConfig, err := os.ReadFile(filepath.Join(first.dir, "codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	secondConfig, err := os.ReadFile(filepath.Join(second.dir, "codex", "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstConfig), "model-a") || strings.Contains(string(firstConfig), "model-b") || !strings.Contains(string(secondConfig), "model-b") || strings.Contains(string(secondConfig), "model-a") {
		t.Fatalf("runtime configurations crossed slots: a=%q b=%q", firstConfig, secondConfig)
	}
	if !strings.Contains(string(firstConfig), `model_reasoning_effort = "low"`) || !strings.Contains(string(secondConfig), `model_reasoning_effort = "high"`) {
		t.Fatalf("runtime reasoning effort crossed or missing: a=%q b=%q", firstConfig, secondConfig)
	}
	firstAuth, err := os.ReadFile(filepath.Join(first.dir, "codex", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	secondAuth, err := os.ReadFile(filepath.Join(second.dir, "codex", "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(firstConfig), "model_catalog_json") || strings.Contains(string(secondConfig), "model_catalog_json") {
		t.Fatalf("isolated Codex config must not reference a generated model catalog: a=%q b=%q", firstConfig, secondConfig)
	}
	for _, catalogPath := range []string{filepath.Join(first.dir, "codex", "model-catalog.json"), filepath.Join(second.dir, "codex", "model-catalog.json")} {
		if _, err := os.Stat(catalogPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Bridge generated a model catalog at %q: %v", catalogPath, err)
		}
	}
	if !strings.Contains(string(firstAuth), "key-a") || strings.Contains(string(firstAuth), "key-b") || !strings.Contains(string(secondAuth), "key-b") || strings.Contains(string(secondAuth), "key-a") {
		t.Fatalf("runtime credentials crossed slots: a=%q b=%q", firstAuth, secondAuth)
	}
	for _, path := range []string{
		filepath.Join(first.dir, "codex", "config.toml"),
		filepath.Join(first.dir, "codex", "auth.json"),
		filepath.Join(second.dir, "codex", "config.toml"),
		filepath.Join(second.dir, "codex", "auth.json"),
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("runtime file %q mode = %#o, want 0600", path, got)
		}
	}
	manager.closeNativeSession(payload.RunID)
	for _, authPath := range []string{filepath.Join(first.dir, "codex", "auth.json"), filepath.Join(second.dir, "codex", "auth.json")} {
		if _, err := os.Stat(authPath); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("runtime credential %q remained after close: %v", authPath, err)
		}
	}
	for _, configPath := range []string{filepath.Join(first.dir, "codex", "config.toml"), filepath.Join(second.dir, "codex", "config.toml")} {
		if _, err := os.Stat(configPath); err != nil {
			t.Fatalf("resume runtime metadata %q was removed: %v", configPath, err)
		}
	}
}

func TestClaudeWorkerProfileRuntimesAreSlotIsolated(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	manager := NewOrchestrationManager(&cfg)
	cliConfig, err := newCLIConfigManager(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetCLIConfigManager(cliConfig)
	payload := protocol.OrchestrationStartPayload{RunID: "orc/claude-profile-isolation", WorkerPair: protocol.WorkerPairClaudeClaude, WorkerProfiles: map[string]protocol.WorkerProfileBinding{
		"claude-a": {PresetID: "preset-a", CLI: "claude", BaseURL: "https://a.example/v1", Model: "claude-sonnet-5", ClaudeContextWindow: 1_000_000, Secret: encryptCLIConfigSecretForTest(t, cliConfig.private.PublicKey(), []byte("key-a"))},
		"claude-b": {PresetID: "preset-b", CLI: "claude", BaseURL: "https://b.example/v1", Model: "deepseek-v4-flash", ClaudeContextWindow: 128_000, ClaudeDisableUnknownModelWindowEnforcement: true, Secret: encryptCLIConfigSecretForTest(t, cliConfig.private.PublicKey(), []byte("key-b"))},
	}}
	session := manager.nativeSession(payload.RunID, home)
	first, err := manager.workerRuntime(payload, "claude-a", "claude", session)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.workerRuntime(payload, "claude-b", "claude", session)
	if err != nil {
		t.Fatal(err)
	}
	if first.dir == second.dir || first.model != "claude-sonnet-5" || second.model != "deepseek-v4-flash" || !containsTestString(first.env, "CLAUDE_CONFIG_DIR="+filepath.Join(first.dir, "claude")) || !containsTestString(second.env, "CLAUDE_CONFIG_DIR="+filepath.Join(second.dir, "claude")) {
		t.Fatalf("isolated Claude runtimes = first=%#v second=%#v", first, second)
	}
	firstConfig, err := os.ReadFile(filepath.Join(first.dir, "claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	secondConfig, err := os.ReadFile(filepath.Join(second.dir, "claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(firstConfig), "claude-sonnet-5") || strings.Contains(string(firstConfig), "deepseek-v4-flash") || !strings.Contains(string(secondConfig), "deepseek-v4-flash") || strings.Contains(string(secondConfig), "claude-sonnet-5") {
		t.Fatalf("Claude runtime settings crossed slots: a=%q b=%q", firstConfig, secondConfig)
	}
	var firstSettings, secondSettings map[string]any
	if err := json.Unmarshal(firstConfig, &firstSettings); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(secondConfig, &secondSettings); err != nil {
		t.Fatal(err)
	}
	firstEnv := firstSettings["env"].(map[string]any)
	secondEnv := secondSettings["env"].(map[string]any)
	if firstEnv["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "1000000" {
		t.Fatalf("Claude A context profile = %#v", firstEnv)
	}
	if _, exists := firstEnv["CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT"]; exists {
		t.Fatalf("recognized Claude model must not disable window enforcement: %#v", firstEnv)
	}
	if secondEnv["CLAUDE_CODE_MAX_CONTEXT_TOKENS"] != "128000" || secondEnv["CLAUDE_CODE_DISABLE_UNKNOWN_MODEL_WINDOW_ENFORCEMENT"] != "1" {
		t.Fatalf("Claude B context profile = %#v", secondEnv)
	}
	manager.closeNativeSession(payload.RunID)
}

func TestClaudeWorkerProfileUnknownModelUsesNativeContextDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	cfg := config.Default()
	manager := NewOrchestrationManager(&cfg)
	cliConfig, err := newCLIConfigManager(&cfg)
	if err != nil {
		t.Fatal(err)
	}
	manager.SetCLIConfigManager(cliConfig)
	payload := protocol.OrchestrationStartPayload{RunID: "orc/claude-unreviewed-context", WorkerProfiles: map[string]protocol.WorkerProfileBinding{
		"claude": {PresetID: "preset-unreviewed", CLI: "claude", BaseURL: "https://provider.example/v1", Model: "provider-invented-alias", Secret: encryptCLIConfigSecretForTest(t, cliConfig.private.PublicKey(), []byte("key"))},
	}}
	runtime, err := manager.workerRuntime(payload, "claude", "claude", manager.nativeSession(payload.RunID, home))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(runtime.dir, "claude", "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	env := settings["env"].(map[string]any)
	for _, key := range claudeContextEnvironmentKeys {
		if _, exists := env[key]; exists {
			t.Fatalf("unreviewed runtime model must not set %s: %#v", key, env)
		}
	}
	manager.closeNativeSession(payload.RunID)
}

func TestRecordedVerifierFactsCaptureEveryMissingRequirement(t *testing.T) {
	exit := 0
	base := orchestrationTurn{
		Role: "reviewer", CLI: "codex", WorkerSlot: "codex", Tools: []RunnerToolEvent{{Status: "completed", ExitCode: &exit}},
		Relay: orchestrationRelayPacket{Structured: true, Status: "resolved", To: "user", Intent: "final", Next: "none", Risks: "none"},
	}
	for name, mutate := range map[string]func(*orchestrationTurn, *[]orchestrationTurn){
		"handoff":      func(turn *orchestrationTurn, _ *[]orchestrationTurn) { turn.Relay.Next = "run another check" },
		"evidence":     func(turn *orchestrationTurn, _ *[]orchestrationTurn) { turn.Tools = nil },
		"independence": func(turn *orchestrationTurn, history *[]orchestrationTurn) { *history = []orchestrationTurn{*turn} },
	} {
		t.Run(name, func(t *testing.T) {
			turn := base
			history := []orchestrationTurn{{Role: "implementer", CLI: "claude", WorkerSlot: "claude"}, turn}
			mutate(&turn, &history)
			if len(history) > 1 {
				history[len(history)-1] = turn
			}
			facts := collectOrchestrationVerifierFacts("collaboration", "default", false, history)
			if facts.Status != verifierVerdictContinue || len(facts.Checkers) != 3 {
				t.Fatalf("%s missing fact was not recorded: %#v", name, facts)
			}
		})
	}
	facts := collectOrchestrationVerifierFacts("collaboration", "default", false, []orchestrationTurn{{Role: "implementer", CLI: "claude", WorkerSlot: "claude"}, base})
	if facts.Status != verifierVerdictPass || len(facts.Checkers) != 3 {
		t.Fatalf("complete recorded facts = %#v", facts)
	}
}

func TestAgentVerifierResponseValidation(t *testing.T) {
	valid := `before {"status":"pass","reason":"complete","checks":[{"name":"handoff","status":"pass","reason":"resolved"},{"name":"evidence","status":"pass","reason":"commands passed"},{"name":"independence","status":"pass","reason":"reviewed"}]} after`
	response, err := parseAgentVerifierResponse(valid)
	if err != nil || response.Status != verifierVerdictPass {
		t.Fatalf("valid verifier response = %#v, %v", response, err)
	}
	downgraded := `{"status":"pass","reason":"complete","checks":[{"name":"handoff","status":"pass","reason":"resolved"},{"name":"evidence","status":"continue","reason":"missing command"},{"name":"independence","status":"pass","reason":"reviewed"}]}`
	response, err = parseAgentVerifierResponse(downgraded)
	if err != nil || response.Status != verifierVerdictContinue {
		t.Fatalf("mixed verifier response was not downgraded: %#v, %v", response, err)
	}
	if _, err := parseAgentVerifierResponse(`{"status":"pass","reason":"complete","checks":[]}`); err == nil {
		t.Fatal("incomplete verifier response was accepted")
	}
	if _, err := parseAgentVerifierResponse(`{"status":"pass","reason":"stale","checks":[{"name":"handoff","status":"pass","reason":"resolved"},{"name":"evidence","status":"pass","reason":"passed"},{"name":"independence","status":"pass","reason":"reviewed"}],"broken":} {"status":"pass"}`); err == nil {
		t.Fatal("fields from invalid JSON leaked into a later partial object")
	}
}

func TestAgentVerifierQuorumRequiresTwoAgentsButRecordsLocalFacts(t *testing.T) {
	passChecks := []protocol.VerifierCheck{
		{Name: "handoff", Status: verifierVerdictPass, Reason: "resolved"},
		{Name: "evidence", Status: verifierVerdictPass, Reason: "commands passed"},
		{Name: "independence", Status: verifierVerdictPass, Reason: "reviewed"},
	}
	local := verifierQuorum("local pass", passChecks)
	passingAgent := func(name string) agentVerifierResult {
		return agentVerifierResult{Agent: name, Slot: name, CLI: "codex", Model: "model", Status: verifierVerdictPass, Reason: "pass", Checks: append([]protocol.VerifierCheck(nil), passChecks...)}
	}
	if verdict := aggregateAgentVerifierQuorum(local, []agentVerifierResult{passingAgent("agent-1"), passingAgent("agent-2")}); verdict.Status != verifierVerdictPass {
		t.Fatalf("unanimous quorum = %#v", verdict)
	}
	disagreement := passingAgent("agent-2")
	disagreement.Status = verifierVerdictContinue
	disagreement.Checks[1] = verifierCheckContinue("evidence", "insufficient")
	if verdict := aggregateAgentVerifierQuorum(local, []agentVerifierResult{passingAgent("agent-1"), disagreement}); verdict.Status != verifierVerdictContinue {
		t.Fatalf("disagreement incorrectly passed: %#v", verdict)
	}
	if verdict := aggregateAgentVerifierQuorum(local, []agentVerifierResult{passingAgent("agent-1")}); verdict.Status != verifierVerdictContinue {
		t.Fatalf("single verifier incorrectly passed: %#v", verdict)
	}
	missingRecordedFact := local
	missingRecordedFact.Status = verifierVerdictContinue
	missingRecordedFact.Checkers[1] = verifierCheckContinue("evidence", "missing recorded evidence")
	if verdict := aggregateAgentVerifierQuorum(missingRecordedFact, []agentVerifierResult{passingAgent("agent-1"), passingAgent("agent-2")}); verdict.Status != verifierVerdictPass {
		t.Fatalf("recorded facts incorrectly blocked unanimous agents: %#v", verdict)
	} else if len(verdict.Checkers) < 3 || verdict.Checkers[len(verdict.Checkers)-3].Name != "recorded/handoff" {
		t.Fatalf("recorded facts were not retained: %#v", verdict)
	}
}

func TestVerifierAssignmentsUseBothWorkerSlots(t *testing.T) {
	for pair, want := range map[string][]string{
		protocol.WorkerPairClaudeCodex:  {"claude", "codex"},
		protocol.WorkerPairCodexCodex:   {orchestrationCodexSlotA, orchestrationCodexSlotB},
		protocol.WorkerPairClaudeClaude: {orchestrationClaudeSlotA, orchestrationClaudeSlotB},
	} {
		assignments := verifierAssignments("collaboration", pair, "claude")
		if len(assignments) != 2 || assignments[0].WorkerSlot != want[0] || assignments[1].WorkerSlot != want[1] {
			t.Fatalf("%s assignments = %#v, want slots %#v", pair, assignments, want)
		}
	}
}

func TestFormalProofConvergenceRequiresReviewingCheckerCommand(t *testing.T) {
	exit := 0
	first := newOrchestrationTurnRecordWithSlot(
		"turn-1", "implementer", "claude", "claude",
		"交接总结：证明已提交。\nMsg: to=reviewer; intent=review; need=audit proof\nHandoff: status=needs_next; changed=Main.v; verified=coqc Main.v; next=audit; risks=none", nil,
	)
	reviewer := newOrchestrationTurnRecordWithSlot(
		"turn-2", "reviewer", "codex", orchestrationCodexDefaultSlot,
		"最终结论：证明通过。\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=Main.v; verified=coqc Main.v; next=none; risks=none",
		[]RunnerToolEvent{{Command: "go test ./...", Status: "completed", ExitCode: &exit}},
	)
	if relayCanConverge("collaboration", "formal-proof", []orchestrationTurn{first, reviewer}) {
		t.Fatal("formal proof converged from non-checker command")
	}
	reviewer.Tools = []RunnerToolEvent{{Command: "coqc Main.v", Status: "completed", ExitCode: &exit}}
	if !relayCanConverge("collaboration", "formal-proof", []orchestrationTurn{first, reviewer}) {
		t.Fatal("formal proof did not converge from successful reviewing checker command")
	}
}

func TestDurableReviewerRequiresResolvedEvidence(t *testing.T) {
	exit := 0
	reviewer := newOrchestrationTurnRecordWithSlot(
		"review", store.TaskRoleReviewer, "codex", orchestrationCodexDefaultSlot,
		"review complete", []RunnerToolEvent{{Command: "go test ./...", Status: "completed", ExitCode: &exit}},
	)
	if durableReviewerCanComplete("default", []orchestrationTurn{reviewer}) {
		t.Fatal("unstructured reviewer response completed durable graph")
	}
	reviewer = newOrchestrationTurnRecordWithSlot(
		"review", store.TaskRoleReviewer, "codex", orchestrationCodexDefaultSlot,
		"最终结论：通过。\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=Main.go; verified=go test ./...; next=none; risks=none",
		[]RunnerToolEvent{{Command: "go test ./...", Status: "completed", ExitCode: &exit}},
	)
	if !durableReviewerCanComplete("default", []orchestrationTurn{reviewer}) {
		t.Fatal("resolved reviewer with successful evidence did not complete")
	}
	blocked := newOrchestrationTurnRecordWithSlot(
		"review", store.TaskRoleReviewer, "codex", orchestrationCodexDefaultSlot,
		"最终结论：还需继续。\nMsg: to=user; intent=continue; need=finish\nHandoff: status=blocked; changed=Main.go; verified=go test ./...; next=finish proof; risks=admitted theorem",
		[]RunnerToolEvent{{Command: "go test ./...", Status: "completed", ExitCode: &exit}},
	)
	if !durableReviewerCanAdvance("default", []orchestrationTurn{blocked}) {
		t.Fatal("valid blocked reviewer handoff did not advance an intermediate round")
	}
	if durableReviewerCanComplete("default", []orchestrationTurn{blocked}) {
		t.Fatal("blocked reviewer handoff completed final round")
	}
	if durableReviewerCanComplete("formal-proof", []orchestrationTurn{reviewer}) {
		t.Fatal("non-proof command completed formal reviewer")
	}
	reviewer.Tools = []RunnerToolEvent{{Command: "coqc Main.v", Status: "completed", ExitCode: &exit}}
	if !durableReviewerCanComplete("formal-proof", []orchestrationTurn{reviewer}) {
		t.Fatal("successful proof checker did not complete formal reviewer")
	}
}

func TestOrchestrationTurnFinalConclusionRequiresAnchoredMarker(t *testing.T) {
	progressOnly := "我检查了文件。sed 后段没有输出，说明文件比交接摘要里的行号更短。随后立即跑 coqc。"
	record := newOrchestrationTurnRecord("turn_inline", "reviewer", "codex", progressOnly, nil)
	if record.Handoff != "" {
		t.Fatalf("inline marker mention produced handoff %q", record.Handoff)
	}
	if orchestrationTurnHasFinalConclusion(record) {
		t.Fatalf("inline marker mention should not be treated as final conclusion")
	}

	for _, content := range []string{
		"完成了修复。\n\n交接总结：已修复并通过 go test ./...，下一步无需处理。",
		"完成了修复。\n\n## 最终结论\n已修复并通过 go test ./...。",
		"结论：仍有阻塞，需要继续处理。",
		"Msg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=go test ./...; next=none; risks=none",
		"Final conclusion\nThe issue is resolved and tests pass.",
	} {
		t.Run(content, func(t *testing.T) {
			record := newOrchestrationTurnRecord("turn_final", "reviewer", "codex", content, nil)
			if !orchestrationTurnHasFinalConclusion(record) {
				t.Fatalf("anchored marker was not treated as final conclusion: %#v", record)
			}
		})
	}
}

func TestFormatRelayPriorTurnForwardsSummaryAndEvidence(t *testing.T) {
	exit := 0
	withCommands := orchestrationTurn{
		Role:    "implementer",
		CLI:     "claude",
		Content: "改完了 main.go 并跑了测试。\n\n交接总结：已修复并通过测试，下一步请评审。",
		Handoff: "已修复并通过测试，下一步请评审。",
		Tools:   []RunnerToolEvent{{Command: "go test ./...", Status: "completed", ExitCode: &exit, Output: "ok"}},
	}
	out := formatRelayPriorTurn(withCommands)
	if !strings.Contains(out, "handoff: 已修复并通过测试") {
		t.Fatalf("expected handoff lead, got:\n%s", out)
	}
	if !strings.Contains(out, "commands:") || !strings.Contains(out, "go test ./...") {
		t.Fatalf("expected command evidence kept, got:\n%s", out)
	}
	if !strings.Contains(out, "result:") {
		t.Fatalf("expected raw result kept when commands ran, got:\n%s", out)
	}
	if strings.Contains(out, "result:") && strings.Contains(out[strings.Index(out, "result:"):], "交接总结") {
		t.Fatalf("summary tail should be stripped from result, got:\n%s", out)
	}

	debateNoCommands := orchestrationTurn{
		Role:    "proposer",
		CLI:     "claude",
		Content: "我认为应该用方案 A。\n\n交接总结：建议采用方案 A，理由是更简单。",
		Handoff: "建议采用方案 A，理由是更简单。",
	}
	out = formatRelayPriorTurn(debateNoCommands)
	if !strings.Contains(out, "handoff: 建议采用方案 A") {
		t.Fatalf("expected handoff lead, got:\n%s", out)
	}
	if strings.Contains(out, "result:") {
		t.Fatalf("summary should stand alone (no result) when no commands ran, got:\n%s", out)
	}

	noSummaryWithCommands := orchestrationTurn{
		Role:    "reviewer",
		CLI:     "codex",
		Content: "looks fine to me.",
		Tools:   []RunnerToolEvent{{Command: "go build ./...", Status: "completed", ExitCode: &exit}},
	}
	out = formatRelayPriorTurn(noSummaryWithCommands)
	if strings.Contains(out, "handoff:") {
		t.Fatalf("expected no handoff lead without a summary, got:\n%s", out)
	}
	if !strings.Contains(out, "result:") || !strings.Contains(out, "commands:") {
		t.Fatalf("expected result and commands without a summary, got:\n%s", out)
	}

	noSummaryNoCommands := orchestrationTurn{
		Role:    "proposer",
		CLI:     "claude",
		Content: "just talking, no summary.",
	}
	out = formatRelayPriorTurn(noSummaryNoCommands)
	if strings.Contains(out, "handoff:") || strings.Contains(out, "commands:") {
		t.Fatalf("expected only a result block, got:\n%s", out)
	}
	if !strings.Contains(out, "result:") {
		t.Fatalf("expected result block as fallback, got:\n%s", out)
	}

	// Content that is entirely a conclusion (e.g. after scrubbing a preamble)
	// should not be repeated as a result alongside the handoff lead.
	conclusionLed := orchestrationTurn{
		Role:    "reviewer",
		CLI:     "codex",
		Content: "最终结论：定理已证明，go build 通过。",
		Handoff: "定理已证明，go build 通过。",
		Tools:   []RunnerToolEvent{{Command: "go build ./...", Status: "completed", ExitCode: &exit}},
	}
	out = formatRelayPriorTurn(conclusionLed)
	if !strings.Contains(out, "handoff: 定理已证明") || !strings.Contains(out, "commands:") {
		t.Fatalf("expected handoff lead and commands, got:\n%s", out)
	}
	if strings.Contains(out, "result:") {
		t.Fatalf("conclusion-only content should not be duplicated as result, got:\n%s", out)
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

func TestClaudeApprovalSocketAutoApprovesProofCommand(t *testing.T) {
	root := t.TempDir()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "coqc"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.WriteFile(filepath.Join(root, "Main.v"), []byte("Check nat.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	client, server := net.Pipe()
	defer client.Close()
	go handleClaudeApprovalSocketConn(context.Background(), server, nil)
	if err := json.NewEncoder(client).Encode(claudeApprovalSocketRequest{
		RequestID: "apr_auto", Command: "coqc Main.v", CWD: root,
	}); err != nil {
		t.Fatal(err)
	}
	var res claudeApprovalSocketResponse
	if err := json.NewDecoder(client).Decode(&res); err != nil {
		t.Fatal(err)
	}
	if res.RequestID != "apr_auto" || res.Decision != "accept" || res.Error != "" {
		t.Fatalf("auto approval = %#v", res)
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

func stringMapValue(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
}

func waitForOrchestrationEvent(t *testing.T, out <-chan protocol.Envelope, kind, cli, content string) bool {
	_, ok := waitForOrchestrationEventPayload(t, out, kind, cli, content)
	return ok
}

func waitForOrchestrationEventPayload(t *testing.T, out <-chan protocol.Envelope, kind, cli, content string) (protocol.OrchestrationEventPayload, bool) {
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
				return event, true
			}
		case <-deadline:
			return protocol.OrchestrationEventPayload{}, false
		}
	}
}

// syncWriteCloser is a thread-safe stand-in for the Claude stream-input pipe.
// The scan goroutine writes nudge notes and closes it while the test goroutine
// polls its contents, so the buffer and closed flag must be mutex-guarded.
type syncWriteCloser struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
}

func (w *syncWriteCloser) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(p)
}

func (w *syncWriteCloser) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.closed = true
	return nil
}

func (w *syncWriteCloser) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *syncWriteCloser) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
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

func readJSONLines(t *testing.T, path string) []map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("decode %s line %q: %v", path, line, err)
		}
		out = append(out, record)
	}
	return out
}

func waitForJSONLineEvent(t *testing.T, path, eventName string) []map[string]any {
	t.Helper()
	return waitForJSONLineEventCount(t, path, eventName, 1)
}

func waitForJSONLineEventCount(t *testing.T, path, eventName string, want int) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	var last []map[string]any
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			last = readJSONLines(t, path)
			var got int
			for _, record := range last {
				if record["event"] == eventName {
					got++
				}
			}
			if got >= want {
				return last
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %q event(s) in %s records=%#v", want, eventName, path, last)
	return nil
}

func stringFromNestedText(value any) string {
	params, _ := value.(map[string]any)
	input, _ := params["input"].([]any)
	if len(input) == 0 {
		return ""
	}
	first, _ := input[0].(map[string]any)
	text, _ := first["text"].(string)
	return text
}

func sliceContainsString(values []any, want string) bool {
	for _, value := range values {
		if got, _ := value.(string); got == want {
			return true
		}
	}
	return false
}

func sliceContainsArgPrefix(values []any, want string) bool {
	for _, value := range values {
		got, _ := value.(string)
		if got == want || strings.HasPrefix(got, want+"=") {
			return true
		}
	}
	return false
}

func fakeClaudePrintScript(text string) string {
	raw, _ := json.Marshal(text)
	return `#!/usr/bin/env python3
import json
import sys

text = ` + string(raw) + `
verdict = {"status":"pass","reason":"independent evidence is complete","checks":[{"name":"handoff","status":"pass","reason":"resolved final handoff"},{"name":"evidence","status":"pass","reason":"successful command recorded"},{"name":"independence","status":"pass","reason":"independent reviewer present"}]}
if any("PROOFBRIDGE_AGENT_VERIFIER_V1" in arg for arg in sys.argv):
    rendered = json.dumps(verdict, separators=(",", ":"))
    print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":rendered}]}}), flush=True)
    print(json.dumps({"type":"result","result":rendered}), flush=True)
    raise SystemExit(0)
if "--input-format=stream-json" in sys.argv:
    for line in sys.stdin:
        print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":text}]}}), flush=True)
        print(json.dumps({"type":"result","result":text}), flush=True)
    raise SystemExit(0)
print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":text}]}}), flush=True)
print(json.dumps({"type":"result","result":text}), flush=True)
`
}

func fakeCodexAppServerEmptyErrorWithFinalConclusionScript() string {
	text := strings.Join([]string{
		"rewrite Habs direction was wrong",
		"",
		"最终结论：已完成可见修正，尾部 app-server 空错误不应覆盖这个结论。",
		"",
		"Msg: to=reviewer; intent=review; need=continue",
		"Handoff: status=needs_next; changed=none; verified=codex visible output; next=continue; risks=none",
	}, "\n")
	raw, _ := json.Marshal(text)
	return `#!/usr/bin/env python3
import json
import sys

text = ` + string(raw) + `

def emit(obj):
    print(json.dumps(obj, ensure_ascii=False, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    params = msg.get("params") or {}
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": "thr_empty_error_final"}}})
    elif method == "thread/resume":
        emit({"id": msg["id"], "result": {"thread": {"id": params.get("threadId") or "thr_empty_error_final"}}})
    elif method == "thread/name/set":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {"status": "unsubscribed"}})
    elif method == "turn/start":
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_1", "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": params.get("threadId") or "thr_empty_error_final", "turnId": "turn_1", "delta": text}})
        emit({"method": "error", "params": {"message": ""}})
        sys.exit(0)
`
}

func fakeCodexAppServerTransportRecoveryScript(statePath, promptPath string) string {
	stateRaw, _ := json.Marshal(statePath)
	promptRaw, _ := json.Marshal(promptPath)
	return `#!/usr/bin/env python3
import json
import pathlib
import sys

state_path = pathlib.Path(` + string(stateRaw) + `)
prompt_path = pathlib.Path(` + string(promptRaw) + `)
attempt = int(state_path.read_text(encoding="utf-8")) if state_path.exists() else 0
state_path.write_text(str(attempt + 1), encoding="utf-8")

def emit(obj):
    print(json.dumps(obj, ensure_ascii=False, separators=(",", ":")), flush=True)

thread_id = "thr_transport_recovery"
for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    params = msg.get("params") or {}
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake"}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": thread_id}}})
    elif method == "thread/resume":
        if params.get("threadId") != thread_id:
            emit({"id": msg["id"], "error": {"message": "wrong recovery thread"}})
            sys.exit(2)
        emit({"id": msg["id"], "result": {"thread": {"id": thread_id}}})
    elif method == "thread/name/set":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {}})
    elif method == "turn/start":
        prompt = "\n".join(item.get("text", "") for item in params.get("input", []))
        turn_id = "turn_%d" % (attempt + 1)
        emit({"id": msg["id"], "result": {"turn": {"id": turn_id, "status": "inProgress"}}})
        if attempt == 0:
            emit({"method": "item/agentMessage/delta", "params": {"threadId": thread_id, "turnId": turn_id, "delta": "partial work before disconnect"}})
            emit({"method": "error", "params": {"threadId": thread_id, "turnId": turn_id, "message": "Reconnecting... 1/5"}})
            sys.exit(1)
        prompt_path.write_text(prompt, encoding="utf-8")
        text = "最终结论：transport recovery completed\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=same thread resumed; next=none; risks=none"
        emit({"method": "item/agentMessage/delta", "params": {"threadId": thread_id, "turnId": turn_id, "delta": text}})
        emit({"method": "turn/completed", "params": {"threadId": thread_id, "turn": {"id": turn_id, "status": "completed"}}})
`
}

type activeWriterFakeState struct {
	Processes  int      `json:"processes"`
	TurnStarts int      `json:"turnStarts"`
	Prompts    []string `json:"prompts"`
}

func readActiveWriterFakeState(t *testing.T, path string) activeWriterFakeState {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var state activeWriterFakeState
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatal(err)
	}
	return state
}

func fakeCodexAppServerActiveWriterScript(statePath string, rejectCount int) string {
	stateRaw, _ := json.Marshal(statePath)
	return `#!/usr/bin/env python3
import json
import pathlib
import sys

state_path = pathlib.Path(` + string(stateRaw) + `)
reject_count = ` + strconv.Itoa(rejectCount) + `
state = json.loads(state_path.read_text(encoding="utf-8")) if state_path.exists() else {"processes": 0, "turnStarts": 0, "prompts": []}
state["processes"] += 1
state_path.write_text(json.dumps(state, ensure_ascii=False), encoding="utf-8")
thread_id = "thr_active_writer"

def emit(obj):
    print(json.dumps(obj, ensure_ascii=False, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    params = msg.get("params") or {}
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake"}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": thread_id}}})
    elif method == "thread/resume":
        if params.get("threadId") != thread_id:
            emit({"id": msg["id"], "error": {"message": "wrong recovery thread"}})
        else:
            emit({"id": msg["id"], "result": {"thread": {"id": thread_id}}})
    elif method == "thread/name/set" or method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {}})
    elif method == "turn/start":
        prompt = "\n".join(item.get("text", "") for item in params.get("input", []))
        state = json.loads(state_path.read_text(encoding="utf-8"))
        state["turnStarts"] += 1
        state["prompts"].append(prompt)
        state_path.write_text(json.dumps(state, ensure_ascii=False), encoding="utf-8")
        if state["turnStarts"] <= reject_count:
            emit({"id": msg["id"], "error": {"message": "thread " + thread_id + " already has an active writer"}})
            continue
        turn_id = "turn_active_writer"
        emit({"id": msg["id"], "result": {"turn": {"id": turn_id, "status": "inProgress"}}})
        text = "最终结论：active writer recovery completed\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=same prompt; next=none; risks=none"
        emit({"method": "item/agentMessage/delta", "params": {"threadId": thread_id, "turnId": turn_id, "delta": text}})
        emit({"method": "turn/completed", "params": {"threadId": thread_id, "turn": {"id": turn_id, "status": "completed"}}})
`
}

func fakeCodexAppServerFailureScript(statePath, message string) string {
	stateRaw, _ := json.Marshal(statePath)
	messageRaw, _ := json.Marshal(message)
	return `#!/usr/bin/env python3
import json
import pathlib
import sys

state_path = pathlib.Path(` + string(stateRaw) + `)
attempt = int(state_path.read_text(encoding="utf-8")) if state_path.exists() else 0
state_path.write_text(str(attempt + 1), encoding="utf-8")
message = ` + string(messageRaw) + `
thread_id = "thr_failure"

def emit(obj):
    print(json.dumps(obj, separators=(",", ":")), flush=True)

for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    params = msg.get("params") or {}
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake"}})
    elif method == "thread/start":
        emit({"id": msg["id"], "result": {"thread": {"id": thread_id}}})
    elif method == "thread/resume":
        if params.get("threadId") != thread_id:
            emit({"id": msg["id"], "error": {"message": "wrong recovery thread"}})
            sys.exit(2)
        emit({"id": msg["id"], "result": {"thread": {"id": thread_id}}})
    elif method == "thread/name/set":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/unsubscribe":
        emit({"id": msg["id"], "result": {}})
    elif method == "turn/start":
        turn_id = "turn_%d" % (attempt + 1)
        emit({"id": msg["id"], "result": {"turn": {"id": turn_id, "status": "inProgress"}}})
        emit({"method": "error", "params": {"threadId": thread_id, "turnId": turn_id, "message": message}})
        sys.exit(1)
`
}

func fakeClaudeInterruptedThenFinalScript() string {
	return `#!/usr/bin/env python3
import json
import pathlib
import sys

state = pathlib.Path(sys.argv[0]).with_suffix(".state")
count = int(state.read_text(encoding="utf-8")) if state.exists() else 0
state.write_text(str(count + 1), encoding="utf-8")
if count == 0:
    text = "Claude started then lost final text."
    for line in sys.stdin:
        json.loads(line)
        print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":text}]}}), flush=True)
        raise SystemExit(0)
    print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":text}]}}), flush=True)
    raise SystemExit(0)
text = "最终结论：Claude completed after continuation\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=continuation; next=none; risks=none"
for line in sys.stdin:
    json.loads(line)
    print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":text}]}}), flush=True)
    print(json.dumps({"type":"result","result":text}), flush=True)
    raise SystemExit(0)
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

func fakeClaudeCapacityThenFinalScript() string {
	return `#!/usr/bin/env python3
import json
import pathlib
import sys

state = pathlib.Path(sys.argv[0]).with_suffix(".state")
count = int(state.read_text(encoding="utf-8")) if state.exists() else 0
state.write_text(str(count + 1), encoding="utf-8")
if count == 0:
    print("Selected model is at capacity. Please try a different model.", file=sys.stderr, flush=True)
    raise SystemExit(1)
text = "最终结论：capacity retry completed\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=retry; next=none; risks=none"
if "--input-format=stream-json" in sys.argv:
    for line in sys.stdin:
        json.loads(line)
        print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":text}]}}), flush=True)
        print(json.dumps({"type":"result","result":text}), flush=True)
        raise SystemExit(0)
print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":text}]}}), flush=True)
print(json.dumps({"type":"result","result":text}), flush=True)
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
if any("PROOFBRIDGE_AGENT_VERIFIER_V1" in arg for arg in sys.argv):
    verdict = {"status":"pass","reason":"independent evidence is complete","checks":[{"name":"handoff","status":"pass","reason":"resolved final handoff"},{"name":"evidence","status":"pass","reason":"successful command recorded"},{"name":"independence","status":"pass","reason":"independent reviewer present"}]}
    rendered = json.dumps(verdict, separators=(",", ":"))
    print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":rendered}]}}), flush=True)
    print(json.dumps({"type":"result","result":rendered}), flush=True)
    raise SystemExit(0)
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
			if err == nil && pid > 0 {
				return pid
			}
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
if len(sys.argv) >= 2 and sys.argv[1] == "app-server":
    turn_count = 0
    for line in sys.stdin:
        msg = json.loads(line)
        method = msg.get("method")
        params = msg.get("params") or {}
        if method == "initialize":
            print(json.dumps({"id":msg["id"],"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"linux"}}), flush=True)
        elif method == "thread/start":
            print(json.dumps({"id":msg["id"],"result":{"thread":{"id":"thread_fake"}}}), flush=True)
        elif method == "thread/resume":
            print(json.dumps({"id":msg["id"],"result":{"thread":{"id":params.get("threadId") or "thread_fake"}}}), flush=True)
        elif method == "thread/name/set":
            print(json.dumps({"id":msg["id"],"result":{}}), flush=True)
        elif method == "thread/unsubscribe":
            print(json.dumps({"id":msg["id"],"result":{"status":"unsubscribed"}}), flush=True)
        elif method == "turn/start":
            turn_count += 1
            thread_id = params.get("threadId") or "thread_fake"
            turn_id = "turn_%d" % turn_count
            print(json.dumps({"id":msg["id"],"result":{"turn":{"id":turn_id,"status":"inProgress"}}}), flush=True)
            print(json.dumps({"method":"item/agentMessage/delta","params":{"threadId":thread_id,"turnId":turn_id,"delta":text}}), flush=True)
            print(json.dumps({"method":"turn/completed","params":{"threadId":thread_id,"turn":{"id":turn_id,"status":"completed"}}}), flush=True)
            if turn_count >= 3:
                break
    raise SystemExit(0)
if len(sys.argv) < 2 or sys.argv[1] != "exec":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)
prompt = sys.stdin.read()
if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:
    verdict = {"status":"pass","reason":"independent evidence is complete","checks":[{"name":"handoff","status":"pass","reason":"resolved final handoff"},{"name":"evidence","status":"pass","reason":"successful command recorded"},{"name":"independence","status":"pass","reason":"independent reviewer present"}]}
    print(json.dumps({"type":"item.agent_message.delta","delta":json.dumps(verdict, separators=(",", ":"))}), flush=True)
    raise SystemExit(0)
print(json.dumps({"type":"item.agent_message.delta","delta":text}), flush=True)
`
}

func fakeCodexExecScriptWithSuccessfulCommand(text string) string {
	raw, _ := json.Marshal(text)
	return `#!/usr/bin/env python3
import json
import sys

text = ` + string(raw) + `
if len(sys.argv) >= 2 and sys.argv[1] == "app-server":
    for line in sys.stdin:
        msg = json.loads(line)
        method = msg.get("method")
        params = msg.get("params") or {}
        if method == "initialize":
            print(json.dumps({"id":msg["id"],"result":{"userAgent":"fake"}}), flush=True)
        elif method == "thread/start":
            print(json.dumps({"id":msg["id"],"result":{"thread":{"id":"thread_verified"}}}), flush=True)
        elif method == "thread/resume":
            print(json.dumps({"id":msg["id"],"result":{"thread":{"id":params.get("threadId") or "thread_verified"}}}), flush=True)
        elif method == "thread/name/set" or method == "thread/unsubscribe":
            print(json.dumps({"id":msg["id"],"result":{}}), flush=True)
        elif method == "turn/start":
            thread_id = params.get("threadId") or "thread_verified"
            turn_id = "turn_verified"
            print(json.dumps({"id":msg["id"],"result":{"turn":{"id":turn_id,"status":"inProgress"}}}), flush=True)
            print(json.dumps({"method":"item/started","params":{"threadId":thread_id,"turnId":turn_id,"item":{"id":"cmd_verified","type":"commandExecution","command":"go test ./..."}}}), flush=True)
            print(json.dumps({"method":"item/completed","params":{"threadId":thread_id,"turnId":turn_id,"item":{"id":"cmd_verified","type":"commandExecution","command":"go test ./...","exitCode":0,"status":"completed"}}}), flush=True)
            print(json.dumps({"method":"item/agentMessage/delta","params":{"threadId":thread_id,"turnId":turn_id,"delta":text}}), flush=True)
            print(json.dumps({"method":"turn/completed","params":{"threadId":thread_id,"turn":{"id":turn_id,"status":"completed"}}}), flush=True)
    raise SystemExit(0)
prompt = sys.stdin.read()
if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:
    verdict = {"status":"pass","reason":"independent evidence is complete","checks":[{"name":"handoff","status":"pass","reason":"resolved final handoff"},{"name":"evidence","status":"pass","reason":"successful command recorded"},{"name":"independence","status":"pass","reason":"independent reviewer present"}]}
    print(json.dumps({"type":"item.agent_message.delta","delta":json.dumps(verdict, separators=(",", ":"))}), flush=True)
    raise SystemExit(0)
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
if len(sys.argv) >= 2 and sys.argv[1] == "app-server":
    with open(argv_path, "w", encoding="utf-8") as f:
        json.dump(sys.argv[1:], f)
    for line in sys.stdin:
        msg = json.loads(line)
        method = msg.get("method")
        params = msg.get("params") or {}
        if method == "initialize":
            print(json.dumps({"id":msg["id"],"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"linux"}}), flush=True)
        elif method == "thread/start":
            print(json.dumps({"id":msg["id"],"result":{"thread":{"id":"thread_relay_1"}}}), flush=True)
        elif method == "thread/resume":
            print(json.dumps({"id":msg["id"],"result":{"thread":{"id":params.get("threadId") or "thread_relay_1"}}}), flush=True)
        elif method == "thread/name/set":
            print(json.dumps({"id":msg["id"],"result":{}}), flush=True)
        elif method == "thread/unsubscribe":
            print(json.dumps({"id":msg["id"],"result":{"status":"unsubscribed"}}), flush=True)
        elif method == "turn/start":
            prompt = (params.get("input") or [{}])[0].get("text", "")
            with open(prompt_path, "w", encoding="utf-8") as f:
                f.write(prompt)
            print(json.dumps({"id":msg["id"],"result":{"turn":{"id":"turn_1","status":"inProgress"}}}), flush=True)
            print(json.dumps({"method":"item/started","params":{"item":{"id":"cmd_test","type":"commandExecution","command":"go test ./...","status":"running"}}}), flush=True)
            print(json.dumps({"method":"item/completed","params":{"item":{"id":"cmd_test","type":"commandExecution","command":"go test ./...","status":"completed","exitCode":0,"aggregatedOutput":"ok ./...\n"}}}), flush=True)
            print(json.dumps({"method":"item/agentMessage/delta","params":{"threadId":"thread_relay_1","turnId":"turn_1","delta":text}}), flush=True)
            print(json.dumps({"method":"turn/completed","params":{"threadId":"thread_relay_1","turn":{"id":"turn_1","status":"completed"}}}), flush=True)
            break
    raise SystemExit(0)
if len(sys.argv) < 2 or sys.argv[1] != "exec":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)
prompt = sys.stdin.read()
if "PROOFBRIDGE_AGENT_VERIFIER_V1" in prompt:
    verdict = {"status":"pass","reason":"independent evidence is complete","checks":[{"name":"handoff","status":"pass","reason":"resolved final handoff"},{"name":"evidence","status":"pass","reason":"successful command recorded"},{"name":"independence","status":"pass","reason":"independent reviewer present"}]}
    print(json.dumps({"type":"item.agent_message.delta","delta":json.dumps(verdict, separators=(",", ":"))}), flush=True)
    raise SystemExit(0)
with open(prompt_path, "w", encoding="utf-8") as f:
    f.write(prompt)
print(json.dumps({"type":"thread.started","thread_id":"thread_relay_1"}), flush=True)
print(json.dumps({"type":"item.started","item":{"id":"cmd_test","type":"command_execution","command":"go test ./...","status":"running"}}), flush=True)
print(json.dumps({"type":"item.completed","item":{"id":"cmd_test","type":"command_execution","command":"go test ./...","status":"completed","exit_code":0,"aggregated_output":"ok ./...\n"}}), flush=True)
print(json.dumps({"type":"item.agent_message.delta","delta":text}), flush=True)
`
}

func fakeCodexResumeMissThenFreshScript(argvPath string) string {
	argvPathRaw, _ := json.Marshal(argvPath)
	return `#!/usr/bin/env python3
import json
import sys

argv_path = ` + string(argvPathRaw) + `
with open(argv_path, "a", encoding="utf-8") as f:
    f.write(json.dumps(sys.argv[1:]) + "\n")
if len(sys.argv) >= 3 and sys.argv[1:3] == ["exec", "resume"]:
    print("session thread not found: rollout missing", file=sys.stderr, flush=True)
    sys.exit(1)
print(json.dumps({"type":"thread.started","thread_id":"thread_fresh"}), flush=True)
print(json.dumps({"type":"item.agent_message.delta","delta":"fresh result"}), flush=True)
`
}

func fakeCodexInteractiveRelayScript(logPath string) string {
	logPathRaw, _ := json.Marshal(logPath)
	return `#!/usr/bin/env python3
import json
import sys

log_path = ` + string(logPathRaw) + `
thread_id = "thr_native"
turn_count = 0

def log(obj):
    with open(log_path, "a", encoding="utf-8") as f:
        f.write(json.dumps(obj, ensure_ascii=False, separators=(",", ":")) + "\n")

def emit(obj):
    print(json.dumps(obj, ensure_ascii=False, separators=(",", ":")), flush=True)

if len(sys.argv) < 2 or sys.argv[1] != "app-server":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)

log({"event": "process_start", "argv": sys.argv[1:]})
for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    params = msg.get("params") or {}
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "thread/start":
        log({"event": "thread_start", "threadId": thread_id})
        emit({"id": msg["id"], "result": {"thread": {"id": thread_id}}})
    elif method == "thread/resume":
        log({"event": "thread_resume", "threadId": params.get("threadId")})
        emit({"id": msg["id"], "result": {"thread": {"id": params.get("threadId") or thread_id}}})
    elif method == "thread/name/set":
        log({"event": "thread_name", "threadId": params.get("threadId"), "name": params.get("name")})
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/unsubscribe":
        log({"event": "thread_unsubscribe", "threadId": params.get("threadId")})
        emit({"id": msg["id"], "result": {"status": "unsubscribed"}})
    elif method == "thread/compact/start":
        log({"event": "thread_compact", "threadId": params.get("threadId")})
        emit({"id": msg["id"], "result": {}})
        emit({"method": "thread/compacted", "params": {"threadId": params.get("threadId"), "turnId": "compact_1"}})
    elif method == "turn/start":
        turn_count += 1
        log({"event": "turn_start", "threadId": params.get("threadId"), "params": params})
        prompt = (params.get("input") or [{}])[0].get("text", "")
        text = "Codex native turn %d\n\nMsg: to=implementer; intent=continue; need=next\nHandoff: status=needs_next; changed=none; verified=codex native; next=continue; risks=none" % turn_count
        if turn_count >= 2:
            text = "Codex native final\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=codex native reused; next=none; risks=none"
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_%d" % turn_count, "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": params.get("threadId"), "turnId": "turn_%d" % turn_count, "delta": text}})
        emit({"method": "turn/completed", "params": {"threadId": params.get("threadId"), "turn": {"id": "turn_%d" % turn_count, "status": "completed"}}})
`
}

func fakeCodexInteractiveRelayScriptWithPIDThreads(logPath string) string {
	logPathRaw, _ := json.Marshal(logPath)
	return `#!/usr/bin/env python3
import json
import os
import sys

log_path = ` + string(logPathRaw) + `
thread_id = "thr_native_%d" % os.getpid()
turn_count = 0

def log(obj):
    with open(log_path, "a", encoding="utf-8") as f:
        f.write(json.dumps(obj, ensure_ascii=False, separators=(",", ":")) + "\n")

def emit(obj):
    print(json.dumps(obj, ensure_ascii=False, separators=(",", ":")), flush=True)

if len(sys.argv) < 2 or sys.argv[1] != "app-server":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)

log({"event": "process_start", "argv": sys.argv[1:], "threadId": thread_id})
for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    params = msg.get("params") or {}
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "thread/start":
        log({"event": "thread_start", "threadId": thread_id})
        emit({"id": msg["id"], "result": {"thread": {"id": thread_id}}})
    elif method == "thread/resume":
        resume_id = params.get("threadId") or thread_id
        log({"event": "thread_resume", "threadId": resume_id, "processThreadId": thread_id})
        emit({"id": msg["id"], "result": {"thread": {"id": resume_id}}})
    elif method == "thread/name/set":
        log({"event": "thread_name", "threadId": params.get("threadId"), "name": params.get("name")})
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/unsubscribe":
        log({"event": "thread_unsubscribe", "threadId": params.get("threadId")})
        emit({"id": msg["id"], "result": {"status": "unsubscribed"}})
    elif method == "turn/start":
        turn_count += 1
        current_thread = params.get("threadId")
        log({"event": "turn_start", "threadId": current_thread, "params": params})
        text = "Codex %s turn %d\n\nMsg: to=peer; intent=continue; need=next\nHandoff: status=needs_next; changed=none; verified=codex slot; next=continue; risks=none" % (current_thread, turn_count)
        if turn_count >= 2:
            text = "Codex %s final\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=codex slot reused; next=none; risks=none" % current_thread
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_%d" % turn_count, "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": current_thread, "turnId": "turn_%d" % turn_count, "delta": text}})
        emit({"method": "turn/completed", "params": {"threadId": current_thread, "turn": {"id": "turn_%d" % turn_count, "status": "completed"}}})
`
}

func fakeCodexInteractiveRelayScriptWithCompactFailure(logPath string) string {
	logPathRaw, _ := json.Marshal(logPath)
	return `#!/usr/bin/env python3
import json
import sys

log_path = ` + string(logPathRaw) + `
thread_id = "thr_native"
turn_count = 0

def log(obj):
    with open(log_path, "a", encoding="utf-8") as f:
        f.write(json.dumps(obj, ensure_ascii=False, separators=(",", ":")) + "\n")

def emit(obj):
    print(json.dumps(obj, ensure_ascii=False, separators=(",", ":")), flush=True)

if len(sys.argv) < 2 or sys.argv[1] != "app-server":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)

log({"event": "process_start", "argv": sys.argv[1:]})
for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    params = msg.get("params") or {}
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "thread/start":
        log({"event": "thread_start", "threadId": thread_id})
        emit({"id": msg["id"], "result": {"thread": {"id": thread_id}}})
    elif method == "thread/resume":
        log({"event": "thread_resume", "threadId": params.get("threadId")})
        emit({"id": msg["id"], "result": {"thread": {"id": params.get("threadId") or thread_id}}})
    elif method == "thread/name/set":
        log({"event": "thread_name", "threadId": params.get("threadId"), "name": params.get("name")})
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/unsubscribe":
        log({"event": "thread_unsubscribe", "threadId": params.get("threadId")})
        emit({"id": msg["id"], "result": {"status": "unsubscribed"}})
    elif method == "thread/compact/start":
        log({"event": "thread_compact", "threadId": params.get("threadId")})
        emit({"id": msg["id"], "error": {"code": -32000, "message": "compact failed"}})
    elif method == "turn/start":
        turn_count += 1
        log({"event": "turn_start", "threadId": params.get("threadId"), "params": params})
        text = "Codex native turn %d\n\nMsg: to=implementer; intent=continue; need=next\nHandoff: status=needs_next; changed=none; verified=codex native; next=continue; risks=none" % turn_count
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_%d" % turn_count, "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": params.get("threadId"), "turnId": "turn_%d" % turn_count, "delta": text}})
        emit({"method": "turn/completed", "params": {"threadId": params.get("threadId"), "turn": {"id": "turn_%d" % turn_count, "status": "completed"}}})
	`
}

func fakeCodexInteractiveRelayScriptWithHangingCompact(logPath string) string {
	logPathRaw, _ := json.Marshal(logPath)
	return `#!/usr/bin/env python3
import json
import sys
import time

log_path = ` + string(logPathRaw) + `
thread_id = "thr_native"

def log(obj):
    with open(log_path, "a", encoding="utf-8") as f:
        f.write(json.dumps(obj, ensure_ascii=False, separators=(",", ":")) + "\n")

def emit(obj):
    print(json.dumps(obj, ensure_ascii=False, separators=(",", ":")), flush=True)

if len(sys.argv) < 2 or sys.argv[1] != "app-server":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)

log({"event": "process_start", "argv": sys.argv[1:]})
for line in sys.stdin:
    msg = json.loads(line)
    method = msg.get("method")
    params = msg.get("params") or {}
    if method == "initialize":
        emit({"id": msg["id"], "result": {"userAgent": "fake", "codexHome": "/tmp", "platformFamily": "unix", "platformOs": "linux"}})
    elif method == "thread/start":
        log({"event": "thread_start", "threadId": thread_id})
        emit({"id": msg["id"], "result": {"thread": {"id": thread_id}}})
    elif method == "thread/name/set":
        emit({"id": msg["id"], "result": {}})
    elif method == "thread/unsubscribe":
        log({"event": "thread_unsubscribe", "threadId": params.get("threadId")})
        emit({"id": msg["id"], "result": {"status": "unsubscribed"}})
    elif method == "thread/compact/start":
        log({"event": "thread_compact", "threadId": params.get("threadId")})
        emit({"id": msg["id"], "result": {}})
        time.sleep(0.35)
        emit({"method": "thread/compacted", "params": {"threadId": params.get("threadId"), "turnId": "compact_1"}})
    elif method == "turn/start":
        log({"event": "turn_start", "threadId": params.get("threadId"), "params": params})
        text = "Codex final before hanging compact\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=final event first; next=none; risks=none"
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_1", "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": params.get("threadId"), "turnId": "turn_1", "delta": text}})
        emit({"method": "turn/completed", "params": {"threadId": params.get("threadId"), "turn": {"id": "turn_1", "status": "completed"}}})
`
}

func fakeClaudeInteractiveRelayScript(logPath string) string {
	logPathRaw, _ := json.Marshal(logPath)
	return `#!/usr/bin/env python3
import json
import sys

log_path = ` + string(logPathRaw) + `

def log(obj):
    with open(log_path, "a", encoding="utf-8") as f:
        f.write(json.dumps(obj, ensure_ascii=False, separators=(",", ":")) + "\n")

def prompt_text(payload):
    return payload.get("message", {}).get("content", [{}])[0].get("text", "")

session_id = ""
for flag in ("--session-id", "--resume"):
    if flag in sys.argv:
        idx = sys.argv.index(flag)
        if idx + 1 < len(sys.argv):
            session_id = sys.argv[idx + 1]
            break

log({"event": "process_start", "argv": sys.argv[1:], "sessionId": session_id})
for idx, line in enumerate(sys.stdin, start=1):
    payload = json.loads(line)
    prompt = prompt_text(payload)
    log({"event": "user_message", "index": idx, "sessionId": session_id, "prompt": prompt})
    text = "Claude compacted native context."
    if prompt != "/compact":
        text = "Claude native turn %d\n\nMsg: to=reviewer; intent=review; need=continue\nHandoff: status=needs_next; changed=none; verified=claude native; next=review; risks=none" % idx
        if idx >= 2:
            text = "Claude native turn 2\n\nMsg: to=reviewer; intent=final-check; need=finish\nHandoff: status=needs_next; changed=none; verified=claude native reused; next=finish; risks=none"
    print(json.dumps({"type": "assistant", "message": {"content": [{"type": "text", "text": text}]}}, ensure_ascii=False), flush=True)
    print(json.dumps({"type": "result", "result": text}, ensure_ascii=False), flush=True)
`
}

func fakeBlockingClaudeStreamScript(markerPath string) string {
	markerPathRaw, _ := json.Marshal(markerPath)
	return `#!/usr/bin/env python3
import json
import os
import signal
import sys
import time

marker_path = ` + string(markerPathRaw) + `
with open(marker_path, "w", encoding="utf-8") as f:
    f.write(str(os.getpid()))
    f.flush()

signal.signal(signal.SIGTERM, lambda signum, frame: sys.exit(0))
for line in sys.stdin:
    json.loads(line)
    print(json.dumps({"type": "assistant", "message": {"content": [{"type": "text", "text": "started"}]}}), flush=True)
    while True:
        time.sleep(1)
`
}

func fakeClaudeResumeMissThenSessionScript(argvPath string) string {
	argvPathRaw, _ := json.Marshal(argvPath)
	return `#!/usr/bin/env python3
import json
import sys

argv_path = ` + string(argvPathRaw) + `
with open(argv_path, "a", encoding="utf-8") as f:
    f.write(json.dumps(sys.argv[1:]) + "\n")
if "--resume" in sys.argv:
    print("session not found", file=sys.stderr, flush=True)
    sys.exit(1)
text = "claude fresh session result"
print(json.dumps({"type":"assistant","message":{"content":[{"type":"text","text":text}]}}), flush=True)
print(json.dumps({"type":"result","result":text}), flush=True)
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
def run_turn(prompt, appserver=False, msg_id=None, thread_id="thread_coq"):
    os.makedirs("coq-proj", exist_ok=True)
    for name in ["Model.v", "Termination.v", "Makefile"]:
        with open(os.path.join("coq-proj", name), "w", encoding="utf-8") as f:
            f.write("(* generated smoke proof file *)\n")
    use_remediation = "final-assessment remediation" in prompt or "Assessment failure to fix" in prompt
    if appserver:
        print(json.dumps({"id":msg_id,"result":{"turn":{"id":"turn_coq","status":"inProgress"}}}), flush=True)
        if use_remediation:
            with open(os.path.join("coq-proj", "AssumptionsCheck.v"), "w", encoding="utf-8") as f:
                f.write("Print Assumptions modify_lin_termination.\n")
            print(json.dumps({"method":"item/started","params":{"item":{"id":"assumptions","type":"commandExecution","command":"coqtop -quiet -Q coq-proj LinLattice < coq-proj/AssumptionsCheck.v","status":"running"}}}), flush=True)
            print(json.dumps({"method":"item/completed","params":{"item":{"id":"assumptions","type":"commandExecution","command":"coqtop -quiet -Q coq-proj LinLattice < coq-proj/AssumptionsCheck.v","status":"completed","exitCode":0,"aggregatedOutput":"Print Assumptions modify_lin_termination.\nClosed under the global context\n"}}}), flush=True)
            print(json.dumps({"method":"item/agentMessage/delta","params":{"threadId":thread_id,"turnId":"turn_coq","delta":remediation}}), flush=True)
        else:
            print(json.dumps({"method":"item/started","params":{"item":{"id":"write","type":"commandExecution","command":"mkdir -p coq-proj && write Model.v Termination.v Makefile","status":"running"}}}), flush=True)
            print(json.dumps({"method":"item/completed","params":{"item":{"id":"write","type":"commandExecution","command":"mkdir -p coq-proj && write Model.v Termination.v Makefile","status":"completed","exitCode":0,"aggregatedOutput":"created coq-proj\n"}}}), flush=True)
            print(json.dumps({"method":"item/started","params":{"item":{"id":"build","type":"commandExecution","command":"make -C coq-proj","status":"running"}}}), flush=True)
            print(json.dumps({"method":"item/completed","params":{"item":{"id":"build","type":"commandExecution","command":"make -C coq-proj","status":"completed","exitCode":0,"aggregatedOutput":"COQC Model.v\nCOQC Termination.v\n"}}}), flush=True)
            print(json.dumps({"method":"item/started","params":{"item":{"id":"scan","type":"commandExecution","command":"rg -n \"Axiom|Parameter|Conjecture|Admitted|admit|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check\" coq-proj","status":"running"}}}), flush=True)
            print(json.dumps({"method":"item/completed","params":{"item":{"id":"scan","type":"commandExecution","command":"rg -n \"Axiom|Parameter|Conjecture|Admitted|admit|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check\" coq-proj","status":"completed","exitCode":0,"aggregatedOutput":""}}}), flush=True)
            print(json.dumps({"method":"item/agentMessage/delta","params":{"threadId":thread_id,"turnId":"turn_coq","delta":text}}), flush=True)
        print(json.dumps({"method":"turn/completed","params":{"threadId":thread_id,"turn":{"id":"turn_coq","status":"completed"}}}), flush=True)
        return
    if use_remediation:
        with open(os.path.join("coq-proj", "AssumptionsCheck.v"), "w", encoding="utf-8") as f:
            f.write("Print Assumptions modify_lin_termination.\n")
        print(json.dumps({"type":"item.started","item":{"id":"assumptions","type":"command_execution","command":"coqtop -quiet -Q coq-proj LinLattice < coq-proj/AssumptionsCheck.v","status":"running"}}), flush=True)
        print(json.dumps({"type":"item.completed","item":{"id":"assumptions","type":"command_execution","command":"coqtop -quiet -Q coq-proj LinLattice < coq-proj/AssumptionsCheck.v","status":"completed","exit_code":0,"aggregated_output":"Print Assumptions modify_lin_termination.\nClosed under the global context\n"}}), flush=True)
        print(json.dumps({"type":"item.agent_message.delta","delta":remediation}), flush=True)
        return
    print(json.dumps({"type":"item.started","item":{"id":"write","type":"command_execution","command":"mkdir -p coq-proj && write Model.v Termination.v Makefile","status":"running"}}), flush=True)
    print(json.dumps({"type":"item.completed","item":{"id":"write","type":"command_execution","command":"mkdir -p coq-proj && write Model.v Termination.v Makefile","status":"completed","exit_code":0,"aggregated_output":"created coq-proj\n"}}), flush=True)
    print(json.dumps({"type":"item.started","item":{"id":"build","type":"command_execution","command":"make -C coq-proj","status":"running"}}), flush=True)
    print(json.dumps({"type":"item.completed","item":{"id":"build","type":"command_execution","command":"make -C coq-proj","status":"completed","exit_code":0,"aggregated_output":"COQC Model.v\nCOQC Termination.v\n"}}), flush=True)
    print(json.dumps({"type":"item.started","item":{"id":"scan","type":"command_execution","command":"rg -n \"Axiom|Parameter|Conjecture|Admitted|admit|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check\" coq-proj","status":"running"}}), flush=True)
    print(json.dumps({"type":"item.completed","item":{"id":"scan","type":"command_execution","command":"rg -n \"Axiom|Parameter|Conjecture|Admitted|admit|Abort|sorry|TODO|placeholder|quick_and_dirty|Guard Checking|bypass_check\" coq-proj","status":"completed","exit_code":0,"aggregated_output":""}}), flush=True)
    print(json.dumps({"type":"item.agent_message.delta","delta":text}), flush=True)

if len(sys.argv) >= 2 and sys.argv[1] == "app-server":
    thread_id = "thread_coq"
    for line in sys.stdin:
        msg = json.loads(line)
        method = msg.get("method")
        params = msg.get("params") or {}
        if method == "initialize":
            print(json.dumps({"id":msg["id"],"result":{"userAgent":"fake","codexHome":"/tmp","platformFamily":"unix","platformOs":"linux"}}), flush=True)
        elif method == "thread/start":
            print(json.dumps({"id":msg["id"],"result":{"thread":{"id":thread_id}}}), flush=True)
        elif method == "thread/resume":
            thread_id = params.get("threadId") or thread_id
            print(json.dumps({"id":msg["id"],"result":{"thread":{"id":thread_id}}}), flush=True)
        elif method == "thread/name/set":
            print(json.dumps({"id":msg["id"],"result":{}}), flush=True)
        elif method == "turn/start":
            prompt = (params.get("input") or [{}])[0].get("text", "")
            run_turn(prompt, appserver=True, msg_id=msg["id"], thread_id=thread_id)
            break
    raise SystemExit(0)
if len(sys.argv) < 2 or sys.argv[1] != "exec":
    print("unexpected command: " + " ".join(sys.argv[1:]), file=sys.stderr)
    sys.exit(1)
prompt = sys.stdin.read()
run_turn(prompt)
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
