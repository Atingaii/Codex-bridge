package store

import (
	"context"
	"sync"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
)

func createTestTaskGraph(t *testing.T, st *Store) OrchestrationTaskGraph {
	t.Helper()
	ctx := context.Background()
	user, err := st.UpsertUser(ctx, "graph-user", "secret")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := st.UpsertAgent(ctx, "graph-agent", "graph-machine", "host", "instance", nil)
	if err != nil {
		t.Fatal(err)
	}
	run := createTestOrchestrationRun(t, st, user.ID, agent.ID, "graph")
	specs := []CreateTaskSpec{
		{Name: "a", Role: TaskRoleWorker, PayloadJSON: `{}`, PayloadDigest: "a"},
		{Name: "b", Role: TaskRoleWorker, PayloadJSON: `{}`, PayloadDigest: "b"},
		{Name: "integrate", Role: TaskRoleIntegrator, PayloadJSON: `{}`, PayloadDigest: "i", Dependencies: []int{0, 1}},
		{Name: "review", Role: TaskRoleReviewer, PayloadJSON: `{}`, PayloadDigest: "r", Dependencies: []int{2}},
	}
	graph, err := st.CreateOrchestrationTaskGraph(ctx, run.ID, `{}`, "base", specs)
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func TestClaimReadyTaskIsAtomic(t *testing.T) {
	st := openTestStore(t)
	graph := createTestTaskGraph(t, st)
	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, claimed, err := st.ClaimReadyTask(context.Background(), graph.Tasks[0].ID, "")
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			results <- claimed
		}()
	}
	wg.Wait()
	close(results)
	claimed := 0
	for result := range results {
		if result {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("successful claims = %d, want 1", claimed)
	}
}

func TestTaskGraphRequiresReviewerAndPreservesEvidence(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	graph := createTestTaskGraph(t, st)
	for i := 0; i < 2; i++ {
		task, attempt, claimed, err := st.ClaimReadyTask(ctx, graph.Tasks[i].ID, "")
		if err != nil || !claimed {
			t.Fatalf("claim worker %d: claimed=%v err=%v", i, claimed, err)
		}
		if ok, err := st.UpdateTaskAttempt(ctx, task.ID, attempt.ID, task.PayloadDigest, TaskRunning, map[string]any{"cwd": "worker"}, ""); err != nil || !ok {
			t.Fatalf("start worker %d: ok=%v err=%v", i, ok, err)
		}
		if ok, err := st.UpdateTaskAttempt(ctx, task.ID, attempt.ID, task.PayloadDigest, TaskSucceeded, map[string]any{"content": "done"}, ""); err != nil || !ok {
			t.Fatalf("finish worker %d: ok=%v err=%v", i, ok, err)
		}
	}
	updated, err := st.TaskGraphByRun(ctx, graph.RunID)
	if err != nil || updated.Tasks[2].Status != TaskReady || updated.Status != TaskGraphRunning {
		t.Fatalf("after workers: graph=%#v err=%v", updated, err)
	}
	evidence, err := st.TaskDependencyEvidence(ctx, updated.Tasks[2].ID)
	if err != nil || len(evidence) != 2 {
		t.Fatalf("dependency evidence=%#v err=%v", evidence, err)
	}
	for _, item := range evidence {
		if item["cwd"] != "worker" || item["content"] != "done" {
			t.Fatalf("merged evidence = %#v", item)
		}
	}
	for _, index := range []int{2, 3} {
		updated, _ = st.TaskGraphByRun(ctx, graph.RunID)
		task, attempt, claimed, err := st.ClaimReadyTask(ctx, updated.Tasks[index].ID, "")
		if err != nil || !claimed {
			t.Fatalf("claim task %d: claimed=%v err=%v", index, claimed, err)
		}
		if ok, err := st.UpdateTaskAttempt(ctx, task.ID, attempt.ID, task.PayloadDigest, TaskSucceeded, map[string]any{"cwd": "integrated"}, ""); err != nil || !ok {
			t.Fatalf("finish task %d: ok=%v err=%v", index, ok, err)
		}
		updated, _ = st.TaskGraphByRun(ctx, graph.RunID)
		if index == 2 && updated.Status == TaskGraphCompleted {
			t.Fatal("integrator completed graph before reviewer")
		}
	}
	if updated.Status != TaskGraphCompleted {
		t.Fatalf("final graph status = %q", updated.Status)
	}
}

func TestCreateNextTaskGraphIsIdempotentForPreviousGeneration(t *testing.T) {
	st := openTestStore(t)
	first := createTestTaskGraph(t, st)
	specs := []CreateTaskSpec{{Name: "review", Role: TaskRoleReviewer, PayloadJSON: `{}`, PayloadDigest: "next"}}
	next, created, err := st.CreateNextOrchestrationTaskGraph(context.Background(), first.RunID, first.ID, `{}`, "next-base", specs)
	if err != nil || !created || next.Generation != 2 {
		t.Fatalf("first successor = %#v created=%v err=%v", next, created, err)
	}
	duplicate, created, err := st.CreateNextOrchestrationTaskGraph(context.Background(), first.RunID, first.ID, `{}`, "duplicate-base", specs)
	if err != nil || created || duplicate.ID != "" {
		t.Fatalf("duplicate successor = %#v created=%v err=%v", duplicate, created, err)
	}
}

func TestRecoverTaskGraphsMarksAmbiguousAttemptUnknown(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	graph := createTestTaskGraph(t, st)
	if _, _, claimed, err := st.ClaimReadyTask(ctx, graph.Tasks[0].ID, ""); err != nil || !claimed {
		t.Fatalf("claim: %v %v", claimed, err)
	}
	count, err := st.RecoverTaskGraphs(ctx)
	if err != nil || count != 1 {
		t.Fatalf("recover count=%d err=%v", count, err)
	}
	updated, err := st.TaskGraphByRun(ctx, graph.RunID)
	if err != nil || updated.Tasks[0].Status != TaskUnknown || updated.Status != TaskGraphUnknown {
		t.Fatalf("recovered graph=%#v err=%v", updated, err)
	}
	if updated.Tasks[2].Status != TaskBlocked || updated.Tasks[3].Status != TaskBlocked {
		t.Fatalf("unknown dependency did not recursively block descendants: %#v", updated.Tasks)
	}
}

func TestOrchestrationEventTaskAttemptRoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	graph := createTestTaskGraph(t, st)
	ref := &protocol.TaskAttemptRef{
		GraphID: graph.ID, TaskID: graph.Tasks[0].ID, AttemptID: "attempt-roundtrip",
		Role: TaskRoleWorker, WorkerSlot: "codex-a", PayloadDigest: graph.Tasks[0].PayloadDigest,
	}
	if _, err := st.AddOrchestrationEvent(ctx, OrchestrationEvent{RunID: graph.RunID, Kind: "run.start", Task: ref}); err != nil {
		t.Fatal(err)
	}
	events, err := st.ListOrchestrationEvents(ctx, graph.RunID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Task == nil || *events[0].Task != *ref {
		t.Fatalf("task attempt ref round trip = %#v", events)
	}
}

func TestCancelingRunClosesDurableTaskGraph(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	graph := createTestTaskGraph(t, st)
	if _, _, claimed, err := st.ClaimReadyTask(ctx, graph.Tasks[0].ID, ""); err != nil || !claimed {
		t.Fatalf("claim active task: claimed=%v err=%v", claimed, err)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, graph.RunID, OrchestrationCanceling, ""); err != nil {
		t.Fatal(err)
	}
	if _, changed, err := st.CancelOrchestrationRunIfStillCanceling(ctx, graph.RunID, "cancel timeout"); err != nil || !changed {
		t.Fatalf("cancel durable graph: changed=%v err=%v", changed, err)
	}
	updated, err := st.TaskGraphByRun(ctx, graph.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != TaskGraphCanceled {
		t.Fatalf("canceled graph status = %q", updated.Status)
	}
	for _, task := range updated.Tasks {
		if task.Status != TaskCanceled && task.Status != TaskSucceeded && task.Status != TaskFailed {
			t.Fatalf("unfinished task remained after cancel: %#v", task)
		}
	}
}
