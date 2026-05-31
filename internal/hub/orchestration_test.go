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
		"prompt":   "second task",
		"maxTurns": 20,
		"profile":  "formal-proof",
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
	if payload.Profile != "formal-proof" || payload.MaxTurns != 12 || payload.MaxTurnsRequested != 20 {
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
