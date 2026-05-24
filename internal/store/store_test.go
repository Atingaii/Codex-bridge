package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestStoreUserAgentSessionMessageFlow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	user, err := st.UpsertUser(ctx, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AuthenticateUser(ctx, "admin", "bad"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized, got %v", err)
	}
	if _, err := st.AuthenticateUser(ctx, "admin", "secret"); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := st.db.QueryRowContext(ctx, `SELECT password_hash FROM users WHERE username = ?`, "admin").Scan(&hash); err != nil {
		t.Fatal(err)
	}
	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatal(err)
	}
	if cost < passwordHashCost {
		t.Fatalf("bcrypt cost = %d, want at least %d", cost, passwordHashCost)
	}

	agent, err := st.UpsertAgent(ctx, "bridge", "machine-1", "host", "inst-1", []string{"/work", "/work/project"})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Instance != "inst-1" {
		t.Fatalf("agent instance = %q", agent.Instance)
	}
	if len(agent.WorkingDirs) != 2 || agent.WorkingDirs[0] != "/work" || agent.WorkingDirs[1] != "/work/project" {
		t.Fatalf("agent working dirs = %#v", agent.WorkingDirs)
	}
	agent, err = st.UpsertAgent(ctx, "bridge", "machine-1", "host", "inst-2", []string{"/next", "/next", " "})
	if err != nil {
		t.Fatal(err)
	}
	if agent.Instance != "inst-2" {
		t.Fatalf("updated agent instance = %q", agent.Instance)
	}
	if len(agent.WorkingDirs) != 1 || agent.WorkingDirs[0] != "/next" {
		t.Fatalf("updated agent working dirs = %#v", agent.WorkingDirs)
	}
	session, err := st.CreateSession(ctx, user.ID, agent.ID, "chat")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSessionRemoteThread(ctx, session.ID, user.ID, "thread-1"); err != nil {
		t.Fatal(err)
	}
	renamed, err := st.UpdateSessionTitle(ctx, session.ID, user.ID, "  renamed chat  ")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Title != "renamed chat" {
		t.Fatalf("renamed session title = %q", renamed.Title)
	}
	if _, err := st.AddMessage(ctx, session.ID, "user", "hello", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddMessage(ctx, session.ID, "assistant", "world", `{"output_tokens":1}`); err != nil {
		t.Fatal(err)
	}
	messages, err := st.ListMessages(ctx, session.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 2 || messages[0].Role != "user" || messages[1].UsageJSON == "" {
		t.Fatalf("unexpected messages: %#v", messages)
	}

	run, err := st.CreateRun(ctx, session.ID, "prompt-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != RunRunning || run.PromptID != "prompt-1" {
		t.Fatalf("unexpected run: %#v", run)
	}
	if _, err := st.CreateRun(ctx, session.ID, "prompt-1"); err == nil {
		t.Fatal("expected duplicate prompt id to fail")
	} else if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	active, err := st.ActiveRunBySession(ctx, session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if active.ID != run.ID {
		t.Fatalf("active run = %#v, want %#v", active, run)
	}
	if err := st.UpdateRunStatus(ctx, run.ID, RunSucceeded, "", `{"output_tokens":1}`); err != nil {
		t.Fatal(err)
	}
	updated, err := st.RunByID(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != RunSucceeded || updated.FinishedAt == 0 || updated.UsageJSON == "" {
		t.Fatalf("updated run = %#v", updated)
	}
	if _, err := st.ActiveRunBySession(ctx, session.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected no active run, got %v", err)
	}

	run2, err := st.CreateRun(ctx, session.ID, "prompt-2")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := st.MarkUnfinishedRunsFailed(ctx, "restart"); err != nil {
		t.Fatal(err)
	} else if n != 1 {
		t.Fatalf("marked unfinished runs = %d, want 1", n)
	}
	restarted, err := st.RunByID(ctx, run2.ID)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Status != RunFailed || restarted.Error != "restart" || restarted.FinishedAt == 0 {
		t.Fatalf("restart-marked run = %#v", restarted)
	}

	run3, err := st.CreateRun(ctx, session.ID, "prompt-3")
	if err != nil {
		t.Fatal(err)
	}
	if n, err := st.MarkActiveRunsForAgentFailed(ctx, agent.ID, "offline"); err != nil {
		t.Fatal(err)
	} else if n != 1 {
		t.Fatalf("marked agent runs = %d, want 1", n)
	}
	offline, err := st.RunByID(ctx, run3.ID)
	if err != nil {
		t.Fatal(err)
	}
	if offline.Status != RunFailed || offline.Error != "offline" {
		t.Fatalf("offline-marked run = %#v", offline)
	}
	orchestration, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{
		UserID:   user.ID,
		AgentID:  agent.ID,
		Title:    "active orchestration",
		Mode:     "collaboration",
		Prompt:   "prove it",
		MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, orchestration.ID, OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	markedOrchestrations, err := st.MarkActiveOrchestrationRunsForAgentFailed(ctx, agent.ID, "offline orchestration")
	if err != nil {
		t.Fatal(err)
	}
	if len(markedOrchestrations) != 1 || markedOrchestrations[0].ID != orchestration.ID {
		t.Fatalf("marked orchestrations = %#v, want %s", markedOrchestrations, orchestration.ID)
	}
	failedOrchestration, err := st.OrchestrationRunByID(ctx, orchestration.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failedOrchestration.Status != OrchestrationFailed || failedOrchestration.Error != "offline orchestration" || failedOrchestration.FinishedAt == 0 {
		t.Fatalf("offline-marked orchestration = %#v", failedOrchestration)
	}
	orchestrationEvents, err := st.ListOrchestrationEvents(ctx, orchestration.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(orchestrationEvents) != 1 || orchestrationEvents[0].Kind != "run.error" || orchestrationEvents[0].Error != "offline orchestration" {
		t.Fatalf("offline orchestration events = %#v", orchestrationEvents)
	}
	if err := st.DeleteAgent(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AgentByID(ctx, agent.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted agent to be hidden, got %v", err)
	}
	if _, err := st.AgentByMachineID(ctx, "machine-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted machine to be hidden, got %v", err)
	}
	agents, err := st.ListAgents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range agents {
		if item.ID == agent.ID {
			t.Fatalf("deleted agent listed: %#v", agents)
		}
	}
	if _, err := st.SessionByID(ctx, session.ID, user.ID); err != nil {
		t.Fatalf("session history should remain after deleting agent: %v", err)
	}
	if _, err := st.UpsertAgent(ctx, "bridge", "machine-1", "host", "inst-3", nil); err != nil {
		t.Fatalf("re-enroll deleted machine: %v", err)
	}
	if err := st.DeleteSession(ctx, session.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SessionByID(ctx, session.ID, user.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected deleted session to be missing, got %v", err)
	}
	messages, err = st.ListMessages(ctx, session.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 0 {
		t.Fatalf("expected cascade-deleted messages, got %#v", messages)
	}
}

func TestStoreRejectsQuotedEmptyPasswords(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	for _, password := range []string{`""`, `''`, ` "" `} {
		if _, err := st.UpsertUser(ctx, "admin", password); err == nil {
			t.Fatalf("UpsertUser accepted quoted empty password %q", password)
		}
		if _, err := st.CreateUser(ctx, "member", password); err == nil {
			t.Fatalf("CreateUser accepted quoted empty password %q", password)
		}
	}
}

func TestStoreOrchestrationRunEventFlow(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	user, err := st.UpsertUser(ctx, "admin", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgent(ctx, "bridge", "machine-orc", "host", "inst", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{
		UserID:   user.ID,
		AgentID:  agent.ID,
		Title:    "Debate",
		Mode:     "debate",
		Prompt:   "prove a theorem",
		MaxTurns: 2,
		Files:    []OrchestrationFile{{Name: "A.v", MimeType: "text/plain", Size: 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != OrchestrationQueued || run.Mode != "debate" || len(run.Files) != 1 {
		t.Fatalf("unexpected run: %+v", run)
	}
	if _, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "turn.start", Role: "proposer", CLI: "claude"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: run.ID, Kind: "turn.end", Role: "proposer", CLI: "claude", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListOrchestrationEvents(ctx, run.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Seq != 1 || events[1].Seq != 2 {
		t.Fatalf("unexpected events: %+v", events)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	loaded, err := st.OrchestrationRunByID(ctx, run.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != OrchestrationCompleted || loaded.FinishedAt == 0 {
		t.Fatalf("unexpected loaded run: %+v", loaded)
	}
	if err := st.UpdateOrchestrationRunSettings(ctx, run.ID, agent.ID, run.Mode, run.CWD, run.MaxTurns, run.Files); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, OrchestrationRunning, ""); err != nil {
		t.Fatal(err)
	}
	loaded, err = st.OrchestrationRunByID(ctx, run.ID, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != OrchestrationRunning || loaded.FinishedAt != 0 {
		t.Fatalf("resumed run kept terminal state: %+v", loaded)
	}
}

func TestConsumeEnrollToken(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	expires := time.Now().Add(time.Hour)
	if err := st.CreateEnrollToken(ctx, "token-1", &expires); err != nil {
		t.Fatal(err)
	}
	if err := st.ConsumeEnrollToken(ctx, "token-1", "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.ConsumeEnrollToken(ctx, "token-1", "machine-1"); err != nil {
		t.Fatal(err)
	}
	if err := st.ConsumeEnrollToken(ctx, "token-1", "machine-2"); !errors.Is(err, ErrTokenConsumed) {
		t.Fatalf("expected consumed, got %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st
}
