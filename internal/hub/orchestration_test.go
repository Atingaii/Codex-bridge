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
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}

	conn := &BridgeConn{
		agentID: agentID,
		wsSender: wsSender{
			send: make(chan protocol.Envelope, 4),
			done: make(chan struct{}),
		},
	}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	body := continueOrchestration(t, s, userID, run.ID, map[string]any{
		"prompt":   "second task",
		"maxTurns": 2,
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
	if !strings.Contains(payload.Context, "first task") || !strings.Contains(payload.Context, "implemented first task") {
		t.Fatalf("payload context = %q", payload.Context)
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
