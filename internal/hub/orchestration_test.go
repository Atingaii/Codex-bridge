package hub

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tencent/codex-bridge/internal/config"
	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func TestCancelOrchestrationStatusTransitions(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()

	running := createOrchestrationRun(t, st, userID, agentID)
	if err := st.UpdateOrchestrationRunStatus(ctx, running.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}

	body := cancelOrchestration(t, s, userID, running.ID, http.StatusOK)
	if body["status"] != store.OrchestrationCanceling {
		t.Fatalf("cancel status = %#v", body["status"])
	}
	loaded, err := st.OrchestrationRunByID(ctx, running.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.OrchestrationCanceling {
		t.Fatalf("run status = %q", loaded.Status)
	}
	events, err := st.ListOrchestrationEvents(ctx, running.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "run.canceling" {
		t.Fatalf("cancel events = %#v", events)
	}

	cancelOrchestration(t, s, userID, running.ID, http.StatusOK)
	events, err = st.ListOrchestrationEvents(ctx, running.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 {
		t.Fatalf("duplicate canceling event appended: %#v", events)
	}
}

func TestCancelingOrchestrationTimesOutToCanceled(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCanceling, ""); err != nil {
		t.Fatal(err)
	}

	s.scheduleOrchestrationCancelTimeout(run.ID, 0)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		loaded, err := st.OrchestrationRunByID(ctx, run.ID, userID)
		if err != nil {
			t.Fatal(err)
		}
		if loaded.Status == store.OrchestrationCanceled {
			events, err := st.ListOrchestrationEvents(ctx, run.ID, 10)
			if err != nil {
				t.Fatal(err)
			}
			if len(events) != 1 || events[0].Kind != "run.cancelled" || events[0].Status != store.OrchestrationCanceled {
				t.Fatalf("cancel timeout event = %#v", events)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("canceling orchestration did not time out to canceled")
}

func TestCancelCompletedOrchestrationIsNoop(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
		RunID:  run.ID,
		Kind:   "run.end",
		Status: store.OrchestrationCompleted,
	}); err != nil {
		t.Fatal(err)
	}

	body := cancelOrchestration(t, s, userID, run.ID, http.StatusOK)
	if body["status"] != store.OrchestrationCompleted {
		t.Fatalf("cancel status = %#v", body["status"])
	}
	loaded, err := st.OrchestrationRunByID(ctx, run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.OrchestrationCompleted {
		t.Fatalf("completed run changed to %q", loaded.Status)
	}
	events, err := st.ListOrchestrationEvents(ctx, run.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != "run.end" {
		t.Fatalf("unexpected events after canceling completed run: %#v", events)
	}
}

func TestCommandStatusDoesNotUpdateOrchestrationRunStatus(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}

	s.handleOrchestrationEvent(ctx, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID:  run.ID,
		Kind:   "command.end",
		CLI:    "claude",
		Status: "completed",
		Data:   map[string]any{"command": "isabelle build -D ."},
	}))
	loaded, err := st.OrchestrationRunByID(ctx, run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.OrchestrationRunning {
		t.Fatalf("command.end changed run status to %q", loaded.Status)
	}

	s.handleOrchestrationEvent(ctx, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID:  run.ID,
		Kind:   "run.end",
		Status: "completed",
	}))
	loaded, err = st.OrchestrationRunByID(ctx, run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.OrchestrationCompleted {
		t.Fatalf("run.end did not complete run, got %q", loaded.Status)
	}
}

func TestLateTerminalEventDoesNotReviveCanceledOrchestration(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCanceled, "canceled"); err != nil {
		t.Fatal(err)
	}

	s.handleOrchestrationEvent(ctx, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID:   run.ID,
		Kind:    "run.end",
		Content: "late completed output",
		Status:  "completed",
	}))

	loaded, err := st.OrchestrationRunByID(ctx, run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.OrchestrationCanceled {
		t.Fatalf("late run.end revived canceled run to %q", loaded.Status)
	}
	events, err := st.ListOrchestrationEvents(ctx, run.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("late terminal event should be ignored, got %#v", events)
	}
}

func TestOrchestrationEventsPersistRunSessionState(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)

	s.handleOrchestrationEvent(ctx, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID:        run.ID,
		Kind:         "run.start",
		Source:       "bridge",
		RunStartData: &protocol.RunStartData{CWD: "/abs/work"},
	}))
	s.handleOrchestrationEvent(ctx, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID:      run.ID,
		Kind:       "turn.end",
		CLI:        "codex",
		RunEndData: &protocol.RunEndData{CodexThreadID: "thread_saved"},
	}))
	s.handleOrchestrationEvent(ctx, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID: run.ID,
		Kind:  "turn.end",
		CLI:   "claude",
	}))

	loaded, err := st.OrchestrationRunByID(ctx, run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunCWD != "/abs/work" || loaded.CodexThreadID != "thread_saved" || !loaded.ClaudeStarted {
		t.Fatalf("run session state = %+v", loaded)
	}
}

func TestCompletedOrchestrationStreamIsRejected(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	token, _, err := s.signer.Sign(userID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/ws/orchestrations?runId="+run.ID, nil)
	req.AddCookie(&http.Cookie{Name: accessCookieName, Value: token})
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("completed orchestration stream status = %d, want %d, body = %s", rr.Code, http.StatusConflict, rr.Body.String())
	}
}

func TestCompactOrchestrationContextIncludesTurnEndConclusion(t *testing.T) {
	t.Parallel()

	run := store.OrchestrationRun{
		ID:     "orc_context",
		Mode:   "collaboration",
		Status: store.OrchestrationCompleted,
		CWD:    "/repo",
	}
	contextSummary := compactOrchestrationContext(run, []store.OrchestrationEvent{
		{
			RunID:     run.ID,
			Kind:      "command.end",
			CLI:       "codex",
			Status:    "completed",
			Data:      map[string]any{"command": "go test ./...", "output": "ok"},
			CreatedAt: 10,
		},
		{
			RunID:     run.ID,
			Kind:      "turn.end",
			Role:      "reviewer",
			CLI:       "codex",
			Content:   "最终结论：构建通过。\n\n已验证：`go test ./...`。\n\n剩余风险：无。",
			Status:    "success",
			CreatedAt: 11,
		},
	})

	if !strings.Contains(contextSummary, "Recent agent outputs") || !strings.Contains(contextSummary, "最终结论：构建通过") {
		t.Fatalf("context summary missing turn.end conclusion:\n%s", contextSummary)
	}
	if !strings.Contains(contextSummary, "Tool outcomes and commands") || !strings.Contains(contextSummary, "go test ./...") {
		t.Fatalf("context summary missing command context:\n%s", contextSummary)
	}
}

func TestEmptyPagesReadFailureEventsAreSuppressed(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}

	s.handleOrchestrationEvent(ctx, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID:  run.ID,
		Kind:   "command.end",
		Status: "failed",
		Data: map[string]any{
			"id":     "read_1",
			"status": "failed",
			"output": `<tool_use_error>Invalid pages parameter: "". Use formats like "1-5", "3", or "10-20". Pages are 1-indexed.</tool_use_error>`,
		},
	}))
	events, err := st.ListOrchestrationEvents(ctx, run.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("empty pages read failure was persisted: %#v", events)
	}
	loaded, err := st.OrchestrationRunByID(ctx, run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.OrchestrationRunning {
		t.Fatalf("suppressed event changed run status to %q", loaded.Status)
	}
}

func TestCompactOrchestrationContextCarriesPriorState(t *testing.T) {
	run := store.OrchestrationRun{ID: "orc_test", Mode: "collaboration", CWD: "/repo", Status: store.OrchestrationCompleted}
	events := []store.OrchestrationEvent{
		{Kind: "user.message", Content: "first task"},
		{Kind: "turn.delta", Role: "implementer", CLI: "claude", TurnID: "t1", Content: "edited main.go"},
		{Kind: "command.end", CLI: "codex", TurnID: "t2", Status: "completed", Data: map[string]any{
			"command": "go test ./...",
			"status":  "completed",
			"output":  "ok",
		}},
		{Kind: "run.error", Error: "still needs README update"},
	}

	got := compactOrchestrationContext(run, events)
	for _, want := range []string{"first task", "edited main.go", "go test ./...", "still needs README update", "/repo"} {
		if !strings.Contains(got, want) {
			t.Fatalf("context missing %q:\n%s", want, got)
		}
	}
}

func TestCompactOrchestrationContextMergesTokenDeltas(t *testing.T) {
	run := store.OrchestrationRun{ID: "orc_test", Mode: "collaboration", CWD: "/repo", Status: store.OrchestrationCompleted}
	events := []store.OrchestrationEvent{
		{RunID: run.ID, Kind: "turn.delta", Role: "reviewer", CLI: "codex", TurnID: "t1", Content: "H"},
		{RunID: run.ID, Kind: "turn.delta", Role: "reviewer", CLI: "codex", TurnID: "t1", Content: "andoff"},
		{RunID: run.ID, Kind: "turn.delta", Role: "reviewer", CLI: "codex", TurnID: "t1", Content: ": status=resolved"},
	}

	got := compactOrchestrationContext(run, events)
	if !strings.Contains(got, "Handoff: status=resolved") {
		t.Fatalf("context did not merge token deltas:\n%s", got)
	}
	if strings.Count(got, "reviewer via codex via t1") != 1 {
		t.Fatalf("context should include one merged turn note:\n%s", got)
	}
}

func TestContinueOrchestrationSendsCompactedContext(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
		RunID:   run.ID,
		Kind:    "user.message",
		Role:    "user",
		Content: "first task",
		Status:  store.OrchestrationQueued,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
		RunID:   run.ID,
		Kind:    "turn.delta",
		Role:    "implementer",
		CLI:     "claude",
		Content: "implemented first task",
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunSession(ctx, run.ID, "thread_resume", true, "/abs/resume"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}

	conn := &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{
			Sandbox:        "danger-full-access",
			ApprovalPolicy: "never",
			Orchestration: map[string]protocol.BridgeCLICapability{
				"claude": {Available: true},
				"codex":  {Available: true},
			},
		},
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 4),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := continueOrchestration(t, s, userID, run.ID, map[string]any{
		"prompt":                  "second task",
		"maxTurns":                20,
		"profile":                 "formal-proof",
		"nativeContextCompaction": "after-turn",
	}, http.StatusOK)
	loaded := body["run"].(map[string]any)
	if loaded["id"] != run.ID || loaded["status"] != store.OrchestrationRunning {
		t.Fatalf("continue body = %#v", loaded)
	}
	var env protocol.Envelope
	select {
	case env = <-conn.send:
	default:
		t.Fatal("no orchestration start sent to agent")
	}
	payload, err := protocol.Decode[protocol.OrchestrationStartPayload](env)
	if err != nil {
		t.Fatal(err)
	}
	if !payload.Resume || payload.Prompt != "second task" || payload.RunID != run.ID {
		t.Fatalf("payload = %#v", payload)
	}
	if payload.CodexThreadID != "thread_resume" || !payload.ClaudeStarted || payload.RunCWD != "/abs/resume" {
		t.Fatalf("payload session state = %#v", payload)
	}
	if payload.Profile != "formal-proof" || payload.NativeContextCompaction != "after-turn" || payload.MaxTurns != 12 || payload.MaxTurnsRequested != 20 {
		t.Fatalf("payload profile/maxTurns = %#v", payload)
	}
	if !strings.Contains(payload.Context, "first task") || !strings.Contains(payload.Context, "implemented first task") {
		t.Fatalf("payload context = %q", payload.Context)
	}
}

func TestContinueOrchestrationRejectsAgentSwitch(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	other, err := st.UpsertAgent(ctx, "other", "machine-2", "host-2", "instance-2", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	body := continueOrchestration(t, s, userID, run.ID, map[string]any{
		"agentId":  other.ID,
		"prompt":   "second task",
		"maxTurns": 2,
	}, http.StatusBadRequest)
	if body["code"] != "BAD_AGENT" || !strings.Contains(body["message"].(string), "same CLI endpoint") {
		t.Fatalf("agent switch error body = %#v", body)
	}
}

func TestCreateOrchestrationRejectsReviewRequiredWithoutCodexApprovalCapability(t *testing.T) {
	t.Parallel()

	s, _, userID, agentID := newOrchestrationTestServer(t)
	conn := &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{
			Sandbox:        "workspace-write",
			ApprovalPolicy: "untrusted",
			Orchestration: map[string]protocol.BridgeCLICapability{
				"claude": {Available: true, BrowserApproval: true},
				"codex":  {Available: true, BrowserApproval: false},
			},
		},
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 2),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := createOrchestrationHTTP(t, s, userID, map[string]any{
		"agentId":  agentID,
		"prompt":   "needs approval",
		"maxTurns": 2,
	}, http.StatusConflict)
	if body["code"] != "ORCHESTRATION_CAPABILITY_UNAVAILABLE" || !strings.Contains(body["message"].(string), "Codex") {
		t.Fatalf("capability error body = %#v", body)
	}
}

func TestCreateOrchestrationAllowsAutoExecuteWithoutBrowserApprovalCapability(t *testing.T) {
	t.Parallel()

	s, _, userID, agentID := newOrchestrationTestServer(t)
	conn := &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{
			Sandbox:        "danger-full-access",
			ApprovalPolicy: "never",
			Orchestration: map[string]protocol.BridgeCLICapability{
				"claude": {Available: true},
				"codex":  {Available: true},
			},
		},
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 2),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := createOrchestrationHTTP(t, s, userID, map[string]any{
		"agentId":  agentID,
		"prompt":   "trusted run",
		"maxTurns": 2,
	}, http.StatusCreated)
	run := body["run"].(map[string]any)
	if run["status"] != store.OrchestrationRunning {
		t.Fatalf("run body = %#v", run)
	}
}

func TestCreateOrchestrationRejectsEndpointMissingDirectCLIs(t *testing.T) {
	t.Parallel()

	s, _, userID, agentID := newOrchestrationTestServer(t)
	conn := &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{
			Runner:         "codex",
			Sandbox:        "workspace-write",
			ApprovalPolicy: "untrusted",
			Orchestration:  map[string]protocol.BridgeCLICapability{},
		},
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 2),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := createOrchestrationHTTP(t, s, userID, map[string]any{
		"agentId":  agentID,
		"prompt":   "run orchestration",
		"maxTurns": 2,
	}, http.StatusConflict)
	if body["code"] != "ORCHESTRATION_CAPABILITY_UNAVAILABLE" || !strings.Contains(body["message"].(string), "Claude") || !strings.Contains(body["message"].(string), "Codex") {
		t.Fatalf("capability error body = %#v", body)
	}
}

func TestCreateOrchestrationPersistsUserMessageFileMetadata(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	conn := &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{
			Sandbox:        "danger-full-access",
			ApprovalPolicy: "never",
			Orchestration: map[string]protocol.BridgeCLICapability{
				"claude": {Available: true},
				"codex":  {Available: true},
			},
		},
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 2),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := createOrchestrationHTTP(t, s, userID, map[string]any{
		"agentId":  agentID,
		"prompt":   "prove termination",
		"maxTurns": 2,
		"files": []map[string]any{{
			"name":     "Termination.thy",
			"mimeType": "text/plain",
			"size":     18,
			"data":     "dGhlb3J5IFRlcm1pbmF0aW9u",
		}},
	}, http.StatusCreated)
	run := body["run"].(map[string]any)
	events, err := st.ListOrchestrationEvents(context.Background(), run["id"].(string), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) == 0 || events[0].Kind != "user.message" {
		t.Fatalf("events = %#v", events)
	}
	rawFiles, ok := events[0].Data["files"].([]any)
	if !ok || len(rawFiles) != 1 {
		t.Fatalf("user message files = %#v", events[0].Data)
	}
	file := rawFiles[0].(map[string]any)
	if file["name"] != "Termination.thy" || file["mimeType"] != "text/plain" || file["data"] != nil {
		t.Fatalf("file metadata = %#v", file)
	}
	if file["size"] != float64(18) {
		t.Fatalf("file size = %#v", file["size"])
	}
}

func TestCreateOrchestrationForwardsFirstCLI(t *testing.T) {
	t.Parallel()

	s, _, userID, agentID := newOrchestrationTestServer(t)
	conn := &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{
			Sandbox:        "danger-full-access",
			ApprovalPolicy: "never",
			Orchestration: map[string]protocol.BridgeCLICapability{
				"claude": {Available: true},
				"codex":  {Available: true},
			},
		},
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 2),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := createOrchestrationHTTP(t, s, userID, map[string]any{
		"agentId":  agentID,
		"prompt":   "run visible proof smoke",
		"firstCli": "codex",
		"maxTurns": 2,
	}, http.StatusCreated)
	run := body["run"].(map[string]any)
	if run["firstCli"] != "codex" {
		t.Fatalf("run firstCli = %#v", run["firstCli"])
	}

	env := <-conn.send
	payload, err := protocol.Decode[protocol.OrchestrationStartPayload](env)
	if err != nil {
		t.Fatal(err)
	}
	if payload.FirstCLI != "codex" {
		t.Fatalf("payload first cli = %q", payload.FirstCLI)
	}
}

func TestCreateCodexCodexOrchestrationRequiresOnlyCodexCapability(t *testing.T) {
	t.Parallel()

	s, _, userID, agentID := newOrchestrationTestServer(t)
	conn := &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{
			Orchestration: map[string]protocol.BridgeCLICapability{
				"codex": {Available: true, BrowserApproval: true},
			},
		},
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 2),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := createOrchestrationHTTP(t, s, userID, map[string]any{
		"agentId":    agentID,
		"prompt":     "run codex codex smoke",
		"workerPair": "codex-codex",
		"firstCli":   "claude",
		"maxTurns":   2,
	}, http.StatusCreated)
	run := body["run"].(map[string]any)
	if run["workerPair"] != "codex-codex" || run["firstCli"] != "codex" {
		t.Fatalf("run worker pair/firstCli = %#v", run)
	}

	env := <-conn.send
	payload, err := protocol.Decode[protocol.OrchestrationStartPayload](env)
	if err != nil {
		t.Fatal(err)
	}
	if payload.WorkerPair != protocol.WorkerPairCodexCodex || payload.FirstCLI != "codex" {
		t.Fatalf("payload worker pair/first cli = %#v", payload)
	}

	createOrchestrationHTTP(t, s, userID, map[string]any{
		"agentId":    agentID,
		"prompt":     "run claude codex smoke",
		"workerPair": "claude-codex",
		"maxTurns":   2,
	}, http.StatusConflict)
}

func TestContinueOrchestrationDefaultsFirstCLIFromRun(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run, err := st.CreateOrchestrationRun(ctx, store.CreateOrchestrationRunParams{
		UserID:   userID,
		AgentID:  agentID,
		Title:    "termination",
		Mode:     "collaboration",
		FirstCLI: "codex",
		Prompt:   "prove termination",
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}

	conn := &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{
			Sandbox:        "danger-full-access",
			ApprovalPolicy: "never",
			Orchestration: map[string]protocol.BridgeCLICapability{
				"claude": {Available: true},
				"codex":  {Available: true},
			},
		},
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 2),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := continueOrchestration(t, s, userID, run.ID, map[string]any{
		"prompt":   "continue proof smoke",
		"maxTurns": 2,
	}, http.StatusOK)
	loaded := body["run"].(map[string]any)
	if loaded["firstCli"] != "codex" {
		t.Fatalf("continued run firstCli = %#v", loaded["firstCli"])
	}
	env := <-conn.send
	payload, err := protocol.Decode[protocol.OrchestrationStartPayload](env)
	if err != nil {
		t.Fatal(err)
	}
	if payload.FirstCLI != "codex" {
		t.Fatalf("continued payload first cli = %q", payload.FirstCLI)
	}
}

func TestContinueCodexCodexRestoresWorkerPairAndThreadIDs(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run, err := st.CreateOrchestrationRun(ctx, store.CreateOrchestrationRunParams{
		UserID:     userID,
		AgentID:    agentID,
		Title:      "codex pair",
		Mode:       "collaboration",
		WorkerPair: protocol.WorkerPairCodexCodex,
		FirstCLI:   "claude",
		Prompt:     "prove with two codex workers",
		MaxTurns:   4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunSessionState(ctx, run.ID, "", map[string]string{"codex-a": "thread_a", "codex-b": "thread_b"}, false, "/abs/codex-pair"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}

	conn := &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{
			Orchestration: map[string]protocol.BridgeCLICapability{
				"codex": {Available: true, BrowserApproval: true},
			},
		},
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 2),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := continueOrchestration(t, s, userID, run.ID, map[string]any{
		"prompt":   "continue codex pair",
		"maxTurns": 4,
	}, http.StatusOK)
	loaded := body["run"].(map[string]any)
	if loaded["workerPair"] != "codex-codex" || loaded["firstCli"] != "codex" {
		t.Fatalf("continued run worker pair/firstCli = %#v", loaded)
	}

	env := <-conn.send
	payload, err := protocol.Decode[protocol.OrchestrationStartPayload](env)
	if err != nil {
		t.Fatal(err)
	}
	if payload.WorkerPair != protocol.WorkerPairCodexCodex || payload.FirstCLI != "codex" || !payload.Resume {
		t.Fatalf("continued payload basics = %#v", payload)
	}
	if payload.CodexThreadID != "thread_a" || payload.CodexThreadIDs["codex-a"] != "thread_a" || payload.CodexThreadIDs["codex-b"] != "thread_b" || payload.RunCWD != "/abs/codex-pair" {
		t.Fatalf("continued payload session state = %#v", payload)
	}
}

func TestContinueOrchestrationPreservesExistingRunFiles(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run, err := st.CreateOrchestrationRun(ctx, store.CreateOrchestrationRunParams{
		UserID:   userID,
		AgentID:  agentID,
		Title:    "termination",
		Mode:     "collaboration",
		Prompt:   "prove termination",
		MaxTurns: 2,
		Files: []store.OrchestrationFile{
			{Name: "Model.thy", MimeType: "text/plain", Size: 11},
			{Name: "Termination.thy", MimeType: "text/plain", Size: 23},
			{Name: "ROOT", MimeType: "text/plain", Size: 4},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}

	conn := &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{
			Sandbox:        "danger-full-access",
			ApprovalPolicy: "never",
			Orchestration: map[string]protocol.BridgeCLICapability{
				"claude": {Available: true},
				"codex":  {Available: true},
			},
		},
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 4),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := continueOrchestration(t, s, userID, run.ID, map[string]any{
		"prompt":   "summarize the remaining sorry placeholders",
		"maxTurns": 2,
	}, http.StatusOK)
	loaded := body["run"].(map[string]any)
	rawFiles, ok := loaded["files"].([]any)
	if !ok || len(rawFiles) != 3 {
		t.Fatalf("continued run files = %#v", loaded["files"])
	}
	for _, want := range []string{"Model.thy", "Termination.thy", "ROOT"} {
		var found bool
		for _, raw := range rawFiles {
			file := raw.(map[string]any)
			if file["name"] == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("continued run files missing %q: %#v", want, rawFiles)
		}
	}
	persisted, err := st.OrchestrationRunByID(ctx, run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.Files) != 3 {
		t.Fatalf("persisted files = %#v", persisted.Files)
	}
}

func TestCreateOrchestrationRejectsUnavailableCLIInAutoExecute(t *testing.T) {
	t.Parallel()

	s, _, userID, agentID := newOrchestrationTestServer(t)
	conn := &BridgeConn{
		agentID: agentID,
		capabilities: &protocol.BridgeCapabilities{
			Sandbox:        "danger-full-access",
			ApprovalPolicy: "never",
			Orchestration: map[string]protocol.BridgeCLICapability{
				"claude": {Available: false},
				"codex":  {Available: true},
			},
		},
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 2),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := createOrchestrationHTTP(t, s, userID, map[string]any{
		"agentId":  agentID,
		"prompt":   "trusted run",
		"maxTurns": 2,
	}, http.StatusConflict)
	if body["code"] != "ORCHESTRATION_CAPABILITY_UNAVAILABLE" || !strings.Contains(body["message"].(string), "Claude") {
		t.Fatalf("capability error body = %#v", body)
	}
}

func TestBridgeApprovalRequestRoutesToOrchestrationBrowsers(t *testing.T) {
	t.Parallel()

	s, _, _, _ := newOrchestrationTestServer(t)
	conn := &BrowserConn{
		sid: "orc_route",
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 2),
			done: make(chan struct{}),
		},
	}
	s.pool.AddOrchestrationBrowser("orc_route", conn)
	defer s.pool.RemoveOrchestrationBrowser("orc_route", conn)

	req := protocol.ApprovalRequestPayload{
		RequestID: "apr_orc",
		RunID:     "orc_route",
		TurnID:    "turn_1",
		Command:   "rm -rf build",
	}
	s.handleBridgeEnvelope(context.Background(), "agent_1", protocol.MustEnvelope(protocol.TypeApprovalRequest, "", req))

	select {
	case env := <-conn.send:
		if env.Type != protocol.TypeApprovalRequest || env.Sid != "" {
			t.Fatalf("routed envelope = %#v", env)
		}
		got, err := protocol.Decode[protocol.ApprovalRequestPayload](env)
		if err != nil {
			t.Fatal(err)
		}
		if got.RequestID != "apr_orc" || got.RunID != "orc_route" || got.Command != "rm -rf build" {
			t.Fatalf("routed approval = %#v", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for orchestration approval request")
	}
}

func TestValidApprovalDecisionNormalizesExpectedValues(t *testing.T) {
	for _, decision := range []string{"accept", "decline", "cancel"} {
		if !validApprovalDecision(decision) {
			t.Fatalf("decision %q should be valid", decision)
		}
	}
	if validApprovalDecision("approve") {
		t.Fatal("unexpected approval decision accepted")
	}
}

func TestBridgeHeartbeatRefreshesWorkingDirs(t *testing.T) {
	t.Parallel()

	s, st, _, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	s.handleBridgeEnvelope(ctx, agentID, protocol.MustEnvelope(protocol.TypeHeartbeat, "", protocol.HeartbeatPayload{
		TS:          time.Now().Unix(),
		WorkingDirs: []string{"/root/tencent", "/root/tencent/bridge", "/root/tencent/bridge", " "},
	}))
	agent, err := st.AgentByID(ctx, agentID)
	if err != nil {
		t.Fatal(err)
	}
	if len(agent.WorkingDirs) != 2 || agent.WorkingDirs[0] != "/root/tencent" || agent.WorkingDirs[1] != "/root/tencent/bridge" {
		t.Fatalf("working dirs were not refreshed from heartbeat: %#v", agent.WorkingDirs)
	}
}

func TestListOrchestrationsFiltersByAgentAndLimit(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	other, err := st.UpsertAgent(ctx, "other", "machine-list-other", "host", "instance-other", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []store.CreateOrchestrationRunParams{
		{UserID: userID, AgentID: agentID, Title: "A1", Mode: "collaboration", Prompt: "agent one first", MaxTurns: 2},
		{UserID: userID, AgentID: agentID, Title: "A2", Mode: "collaboration", Prompt: "agent one second", MaxTurns: 2},
		{UserID: userID, AgentID: other.ID, Title: "B1", Mode: "collaboration", Prompt: "agent two only", MaxTurns: 2},
	} {
		if _, err := st.CreateOrchestrationRun(ctx, params); err != nil {
			t.Fatal(err)
		}
	}

	body := getJSON(t, s, userID, "/api/orchestrations?agentId="+agentID+"&limit=1", http.StatusOK)
	rawRuns, ok := body["runs"].([]any)
	if !ok || len(rawRuns) != 1 {
		t.Fatalf("runs body = %#v", body)
	}
	run := rawRuns[0].(map[string]any)
	if run["agentId"] != agentID {
		t.Fatalf("filtered run has wrong agent: %#v", run)
	}
}

func TestOrchestrationEventsSeqWindowHTTP(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	for i := 0; i < 5; i++ {
		if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: "turn.delta", Content: "event"}); err != nil {
			t.Fatal(err)
		}
	}

	body := getJSON(t, s, userID, "/api/orchestrations/"+run.ID+"/events?afterSeq=3&limit=10", http.StatusOK)
	rawEvents, ok := body["events"].([]any)
	if !ok || len(rawEvents) != 2 {
		t.Fatalf("events body = %#v", body)
	}
	if rawEvents[0].(map[string]any)["seq"] != float64(4) || rawEvents[1].(map[string]any)["seq"] != float64(5) {
		t.Fatalf("unexpected event seqs = %#v", rawEvents)
	}

	body = getJSON(t, s, userID, "/api/orchestrations/"+run.ID+"/events?beforeSeq=4&limit=2", http.StatusOK)
	rawEvents, ok = body["events"].([]any)
	if !ok || len(rawEvents) != 2 {
		t.Fatalf("before events body = %#v", body)
	}
	if rawEvents[0].(map[string]any)["seq"] != float64(2) || rawEvents[1].(map[string]any)["seq"] != float64(3) {
		t.Fatalf("unexpected before event seqs = %#v", rawEvents)
	}

	errorBody := getJSON(t, s, userID, "/api/orchestrations/"+run.ID+"/events?afterSeq=bad", http.StatusBadRequest)
	if errorBody["code"] != "BAD_QUERY" {
		t.Fatalf("bad query body = %#v", errorBody)
	}
	errorBody = getJSON(t, s, userID, "/api/orchestrations/"+run.ID+"/events?afterSeq=3&beforeSeq=4", http.StatusBadRequest)
	if errorBody["code"] != "BAD_QUERY" {
		t.Fatalf("mixed query body = %#v", errorBody)
	}
}

func TestRecoverInterruptedRunsAtBoot(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}

	s.recoverInterruptedRuns(ctx)

	recovered, err := st.OrchestrationRunByID(ctx, run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Status != store.OrchestrationFailed || recovered.FinishedAt == 0 {
		t.Fatalf("boot recovery left run = %#v, want failed", recovered)
	}
}

func TestContinueOrchestrationRejectsConcurrentClaims(t *testing.T) {
	t.Parallel()

	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	// Simulate the loser of two concurrent follow-up prompts: the winner has
	// already claimed the run back to running by the time the loser reaches
	// the atomic claim.
	claimed, err := st.ClaimOrchestrationRunForContinue(ctx, run.ID)
	if err != nil || !claimed {
		t.Fatalf("setup claim = %v, %v", claimed, err)
	}

	payload := map[string]any{"prompt": "follow-up"}
	body := authedJSON(t, s, userID, http.MethodPost, "/api/orchestrations/"+run.ID+"/prompts", payload, http.StatusConflict)
	if body["code"] != "RUN_ACTIVE" {
		t.Fatalf("continue while active body = %#v", body)
	}
}

func newOrchestrationTestServer(t *testing.T) (*Server, *store.Store, string, string) {
	t.Helper()

	cfg := config.Default()
	cfg.Hub.DBPath = t.TempDir() + "/bridge.db"
	cfg.Auth.JWTSecret = "hub-orchestration-test-secret-32-bytes"
	cfg.Auth.AccessTokenTTL.Duration = time.Hour

	st, err := store.Open(cfg.Hub.DBPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	user, err := st.UpsertUser(context.Background(), "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgent(context.Background(), "agent", "machine", "host", "instance", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	return NewServer(&cfg, st, BuildInfo{Version: "test", BuildTime: "test"}), st, user.ID, agent.ID
}

func createOrchestrationRun(t *testing.T, st *store.Store, userID, agentID string) store.OrchestrationRun {
	t.Helper()

	run, err := st.CreateOrchestrationRun(context.Background(), store.CreateOrchestrationRunParams{
		UserID:   userID,
		AgentID:  agentID,
		Title:    "test",
		Mode:     "collaboration",
		Prompt:   "prove a lemma",
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func getJSON(t *testing.T, s *Server, userID, path string, wantStatus int) map[string]any {
	t.Helper()

	token, _, err := s.signer.Sign(userID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: accessCookieName, Value: token})
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("GET %s status = %d, want %d, body = %s", path, rr.Code, wantStatus, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode GET body: %v: %s", err, rr.Body.String())
	}
	return body
}

func cancelOrchestration(t *testing.T, s *Server, userID, runID string, wantStatus int) map[string]any {
	t.Helper()

	token, _, err := s.signer.Sign(userID)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orchestrations/"+runID+"/cancel", nil)
	req.AddCookie(&http.Cookie{Name: accessCookieName, Value: token})
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("cancel HTTP status = %d, want %d, body = %s", rr.Code, wantStatus, rr.Body.String())
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode cancel body: %v: %s", err, rr.Body.String())
	}
	return body
}

func continueOrchestration(t *testing.T, s *Server, userID, runID string, payload map[string]any, wantStatus int) map[string]any {
	t.Helper()

	token, _, err := s.signer.Sign(userID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orchestrations/"+runID+"/prompts", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: accessCookieName, Value: token})
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("continue HTTP status = %d, want %d, body = %s", rr.Code, wantStatus, rr.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode continue body: %v: %s", err, rr.Body.String())
	}
	return decoded
}

func createOrchestrationHTTP(t *testing.T, s *Server, userID string, payload map[string]any, wantStatus int) map[string]any {
	t.Helper()

	token, _, err := s.signer.Sign(userID)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/orchestrations", bytes.NewReader(body))
	req.AddCookie(&http.Cookie{Name: accessCookieName, Value: token})
	rr := httptest.NewRecorder()
	s.httpSrv.Handler.ServeHTTP(rr, req)
	if rr.Code != wantStatus {
		t.Fatalf("create HTTP status = %d, want %d, body = %s", rr.Code, wantStatus, rr.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode create body: %v: %s", err, rr.Body.String())
	}
	return decoded
}
