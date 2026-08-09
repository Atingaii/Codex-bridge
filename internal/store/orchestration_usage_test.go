package store

import (
	"context"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
)

func TestReplaceOrchestrationUsageIsIdempotent(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	user, err := st.UpsertUser(ctx, "usage-owner", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, user.ID, "usage-bridge", "usage-machine", "host", "instance", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{UserID: user.ID, AgentID: agent.ID, Mode: "collaboration", Prompt: "test", MaxTurns: 2})
	if err != nil {
		t.Fatal(err)
	}
	result := protocol.OrchestrationUsageSyncResult{RunID: run.ID, ScannedAt: 10, Sessions: []protocol.OrchestrationUsageSessionResult{{CLI: "codex", WorkerSlot: "codex-a", SessionID: "session-12345678", Status: "complete", EventCount: 1}}, Events: []protocol.OrchestrationUsageEvent{{EventID: "event-1", CLI: "codex", WorkerSlot: "codex-a", SessionID: "session-12345678", InputTokens: 20, CacheReadTokens: 80, OutputTokens: 5}}}
	if err := st.ReplaceOrchestrationUsage(ctx, result); err != nil {
		t.Fatal(err)
	}
	if err := st.ReplaceOrchestrationUsage(ctx, result); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListOrchestrationUsageEvents(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].InputTokens != 20 {
		t.Fatalf("events = %+v", events)
	}
}

func TestListAllOrchestrationRunsIsUserScoped(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	first, err := st.UpsertUser(ctx, "usage-first", "secret")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.UpsertUser(ctx, "usage-second", "secret")
	if err != nil {
		t.Fatal(err)
	}
	firstAgent, err := st.UpsertAgentForUser(ctx, first.ID, "first-bridge", "first-machine", "first-host", "instance", nil)
	if err != nil {
		t.Fatal(err)
	}
	secondAgent, err := st.UpsertAgentForUser(ctx, second.ID, "second-bridge", "second-machine", "second-host", "instance", nil)
	if err != nil {
		t.Fatal(err)
	}
	firstRun, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{UserID: first.ID, AgentID: firstAgent.ID, Title: "first task", Mode: "collaboration", Prompt: "test", MaxTurns: 2})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{UserID: second.ID, AgentID: secondAgent.ID, Title: "second task", Mode: "collaboration", Prompt: "test", MaxTurns: 2}); err != nil {
		t.Fatal(err)
	}

	runs, err := st.ListAllOrchestrationRuns(ctx, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ID != firstRun.ID {
		t.Fatalf("first user's runs = %#v", runs)
	}
}
