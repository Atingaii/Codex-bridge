package store

import (
	"context"
	"testing"
)

func TestAdminConversationContentChecksOwnershipAndMergesTurnDeltas(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	owner, err := st.CreateUser(ctx, "content-owner", "secret-password")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateUser(ctx, "content-other", "secret-password")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgentForUser(ctx, owner.ID, "content-agent", "content-machine", "host", "instance", nil)
	if err != nil {
		t.Fatal(err)
	}
	run, err := st.CreateOrchestrationRun(ctx, CreateOrchestrationRunParams{
		UserID: owner.ID, AgentID: agent.ID, Title: "proof", Prompt: "prove it",
		CWD: "/private/workspace", Mode: "collaboration", MaxTurns: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"first ", "second"} {
		if _, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{
			RunID: run.ID, Kind: "turn.delta", TurnID: "turn-1", Role: "worker", Source: "cli", Content: body,
		}); err != nil {
			t.Fatal(err)
		}
	}
	content, err := st.AdminConversationContent(ctx, owner.ID, "orchestration", run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if content.Prompt != "prove it" || len(content.Items) != 1 || content.Items[0].Content != "first second" {
		t.Fatalf("unexpected administrator content projection: %#v", content)
	}
	if _, err := st.AdminConversationContent(ctx, other.ID, "orchestration", run.ID); err != ErrNotFound {
		t.Fatalf("wrong-owner content error = %v, want ErrNotFound", err)
	}
}
