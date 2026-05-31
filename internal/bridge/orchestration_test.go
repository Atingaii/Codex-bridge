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

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
)

func cleanOrchestrationTurnContent(content string) string {
	return scrubOrchestrationTurnContent(content)
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

	codexRecords := readJSONLines(t, codexLogPath)
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
			if !sliceContainsArgPrefix(args, "--input-format") || !sliceContainsString(args, "--session-id") || !sliceContainsString(args, "--name") {
				t.Fatalf("claude process args missing stream session/name: %#v", record)
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
	}, "turn_1", "reviewer", "continue", "thread_missing")
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

func TestOrchestrationCodexEmptyTailErrorAfterVisibleOutputContinues(t *testing.T) {
	tmp := t.TempDir()
	codexPath := filepath.Join(tmp, "codex")
	claudePath := filepath.Join(tmp, "claude")
	if err := os.WriteFile(codexPath, []byte(fakeCodexAppServerEmptyErrorScript()), 0o755); err != nil {
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
	if !orchestrationEventsContain(events, "turn.delta", "codex", "empty tail error after visible output") {
		t.Fatalf("missing recoverable warning: %#v", events)
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
			LongCommandObserver: longCommandObserverConfig{
				Enabled:         true,
				CommandPatterns: []string{"python -m slow_build"},
				AppliesTo:       []string{"claude", "codex"},
			},
		})
	}()

	fmt.Fprintln(stdoutWriter, `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tool_build","name":"Bash","input":{"command":"python -m slow_build --workspace demo"}}]}}`)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !strings.Contains(stdin.String(), "Codex Bridge observer note") {
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
	if got := stdin.String(); !strings.Contains(got, "Codex Bridge observer note") || !strings.Contains(got, "python -m slow_build --workspace demo") {
		t.Fatalf("nudge was not written to Claude stream: %s", got)
	}
	events := drainOrchestrationEvents(t, out)
	if !orchestrationEventsContain(events, "turn.delta", "claude", "Bridge sent a long-command observer note") {
		t.Fatal("frontend-visible observer event was not emitted")
	}
	for _, event := range events {
		if event.Kind == "turn.delta" && event.Source == "bridge" && event.BridgeNoteData != nil && event.BridgeNoteData.InjectedText != "" {
			return
		}
	}
	t.Fatalf("observer event did not carry structured injected text: %#v", events)
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
				if !strings.Contains(event.BridgeNoteData.InjectedText, "Codex Bridge observer note") {
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
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("codex-first proof prompt missing %q:\n%s", want, prompt)
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

func stringMapValue(data map[string]any, key string) string {
	value, _ := data[key].(string)
	return value
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
if "--input-format=stream-json" in sys.argv:
    for line in sys.stdin:
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
with open(argv_path, "w", encoding="utf-8") as f:
    json.dump(sys.argv[1:], f)
if len(sys.argv) >= 2 and sys.argv[1] == "app-server":
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
    elif method == "turn/start":
        turn_count += 1
        log({"event": "turn_start", "threadId": params.get("threadId"), "params": params})
        text = "Codex native turn %d\n\nMsg: to=implementer; intent=continue; need=next\nHandoff: status=needs_next; changed=none; verified=codex native; next=continue; risks=none" % turn_count
        if turn_count >= 2:
            text = "Codex native final\n\nMsg: to=user; intent=final; need=none\nHandoff: status=resolved; changed=none; verified=codex native reused; next=none; risks=none"
        emit({"id": msg["id"], "result": {"turn": {"id": "turn_%d" % turn_count, "status": "inProgress"}}})
        emit({"method": "item/agentMessage/delta", "params": {"threadId": params.get("threadId"), "turnId": "turn_%d" % turn_count, "delta": text}})
        emit({"method": "turn/completed", "params": {"threadId": params.get("threadId"), "turn": {"id": "turn_%d" % turn_count, "status": "completed"}}})
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
    text = "Claude native turn %d\n\nMsg: to=reviewer; intent=review; need=continue\nHandoff: status=needs_next; changed=none; verified=claude native; next=review; risks=none" % idx
    if idx >= 2:
        text = "Claude native turn 2\n\nMsg: to=reviewer; intent=final-check; need=finish\nHandoff: status=needs_next; changed=none; verified=claude native reused; next=finish; risks=none"
    print(json.dumps({"type": "assistant", "message": {"content": [{"type": "text", "text": text}]}}, ensure_ascii=False), flush=True)
    print(json.dumps({"type": "result", "result": text}, ensure_ascii=False), flush=True)
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
