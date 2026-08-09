package hub

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

func createHubTaskGraph(t *testing.T, st *store.Store, run store.OrchestrationRun) store.OrchestrationTaskGraph {
	t.Helper()
	payload := protocol.OrchestrationStartPayload{
		RunID: run.ID, Mode: "collaboration", WorkerPair: protocol.WorkerPairCodexCodex,
		Prompt: "verify the implementation", CWD: t.TempDir(), MaxTurns: 1, MaxTurnsRequested: 1, Round: 1, MaxRounds: 1,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := taskPayloadDigest(string(raw))
	graph, err := st.CreateOrchestrationTaskGraph(context.Background(), run.ID, string(raw), digest, orchestrationTaskSpecs(digest, payload.WorkerPair, payload.Mode))
	if err != nil {
		t.Fatal(err)
	}
	return graph
}

func testBridgeConn(agentID string, queue int) *BridgeConn {
	return &BridgeConn{
		agentID:      agentID,
		capabilities: &protocol.BridgeCapabilities{DurableTaskGraph: true},
		wsSender:     wsSender{send: make(chan protocol.Envelope, queue), done: make(chan struct{})},
	}
}

func decodeTaskDispatchPayload(t *testing.T, env protocol.Envelope) protocol.OrchestrationStartPayload {
	t.Helper()
	payload, err := protocol.Decode[protocol.OrchestrationStartPayload](env)
	if err != nil {
		t.Fatal(err)
	}
	if payload.TaskGraph == nil || len(payload.TaskGraph.Tasks) != 1 {
		t.Fatalf("task dispatch payload = %#v", payload)
	}
	return payload
}

func decodeTaskDispatch(t *testing.T, env protocol.Envelope) protocol.TaskAttemptRef {
	payload := decodeTaskDispatchPayload(t, env)
	task := payload.TaskGraph.Tasks[0]
	return protocol.TaskAttemptRef{
		GraphID: payload.TaskGraph.ID, TaskID: task.ID, AttemptID: task.AttemptID,
		Role: task.Role, WorkerSlot: task.WorkerSlot, PayloadDigest: task.PayloadDigest,
	}
}

func TestDurableTaskDispatchRetainsSelectedCWD(t *testing.T) {
	s, st, userID, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, userID, agentID)
	selectedCWD := t.TempDir()
	payload := protocol.OrchestrationStartPayload{RunID: run.ID, Mode: "collaboration", WorkerPair: protocol.WorkerPairCodexCodex, Prompt: "verify", CWD: selectedCWD, MaxTurns: 1}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := taskPayloadDigest(string(raw))
	graph, err := st.CreateOrchestrationTaskGraph(context.Background(), run.ID, string(raw), digest, orchestrationTaskSpecs(digest, payload.WorkerPair, payload.Mode))
	if err != nil {
		t.Fatal(err)
	}
	conn := testBridgeConn(agentID, 2)
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)
	if err := s.dispatchReadyTaskGraph(context.Background(), run, graph); err != nil {
		t.Fatal(err)
	}
	dispatched := decodeTaskDispatchPayload(t, <-conn.send)
	if dispatched.CWD != selectedCWD || dispatched.RunCWD != "" {
		t.Fatalf("task cwd was rewritten: cwd=%q runCwd=%q, want cwd=%q", dispatched.CWD, dispatched.RunCWD, selectedCWD)
	}
}

func TestDurableTaskDispatchRefreshesNativeSessionState(t *testing.T) {
	s, st, userID, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, userID, agentID)
	graph := createHubTaskGraph(t, st, run)
	if err := st.UpdateOrchestrationRunSessionState(context.Background(), run.ID, "thread-a", map[string]string{"codex-a": "thread-a"}, true, "/private/run-cwd"); err != nil {
		t.Fatal(err)
	}
	run, err := st.OrchestrationRunByID(context.Background(), run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	conn := testBridgeConn(agentID, 2)
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)
	if err := s.dispatchReadyTaskGraph(context.Background(), run, graph); err != nil {
		t.Fatal(err)
	}
	dispatched := decodeTaskDispatchPayload(t, <-conn.send)
	if !dispatched.Resume || dispatched.CodexThreadID != "thread-a" || dispatched.CodexThreadIDs["codex-a"] != "thread-a" || !dispatched.ClaudeStarted || dispatched.RunCWD != "/private/run-cwd" {
		t.Fatalf("native session state was stale: %#v", dispatched)
	}
}

func TestOrchestrationTaskSpecsAlwaysCreateCompleteRound(t *testing.T) {
	specs := orchestrationTaskSpecs("digest", protocol.WorkerPairCodexCodex, "collaboration")
	if len(specs) != 4 || specs[0].Name != "candidate-a" || specs[1].Name != "candidate-b" || specs[2].Role != store.TaskRoleIntegrator || specs[3].Role != store.TaskRoleReviewer {
		t.Fatalf("round specs = %#v", specs)
	}
}

func sendTaskLifecycle(s *Server, runID string, ref protocol.TaskAttemptRef, conclusion *protocol.RunConclusion) {
	ctx := context.Background()
	s.handleOrchestrationEvent(ctx, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID: runID, Kind: "run.start", Task: &ref,
		RunStartData: &protocol.RunStartData{CWD: "/private/task-workspace"},
	}))
	s.handleOrchestrationEvent(ctx, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID: runID, Kind: "run.end", Content: "node complete", Task: &ref, RunConclusion: conclusion,
	}))
}

func TestDurableTaskGraphReviewerIsOnlyPublicCompletionBarrier(t *testing.T) {
	s, st, userID, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, userID, agentID)
	graph := createHubTaskGraph(t, st, run)
	conn := testBridgeConn(agentID, 8)
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	workerRefs := make([]protocol.TaskAttemptRef, 0, 2)
	task, attempt, claimed, err := st.ClaimReadyTask(context.Background(), graph.Tasks[0].ID, "")
	if err != nil || !claimed {
		t.Fatalf("claim worker A: claimed=%v err=%v", claimed, err)
	}
	workerRefs = append(workerRefs, protocol.TaskAttemptRef{GraphID: graph.ID, TaskID: task.ID, AttemptID: attempt.ID, Role: task.Role, WorkerSlot: task.WorkerSlot, PayloadDigest: task.PayloadDigest})
	sendTaskLifecycle(s, run.ID, workerRefs[0], nil)
	workerRefs = append(workerRefs, decodeTaskDispatch(t, <-conn.send))
	if workerRefs[1].Role != store.TaskRoleWorker || workerRefs[1].TaskID == workerRefs[0].TaskID {
		t.Fatalf("candidate B was not dispatched after A: %#v", workerRefs)
	}
	sendTaskLifecycle(s, run.ID, workerRefs[1], nil)
	loaded, err := st.OrchestrationRunByID(context.Background(), run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.OrchestrationRunning {
		t.Fatalf("workers completed public run with status %q", loaded.Status)
	}

	integrator := decodeTaskDispatch(t, <-conn.send)
	sendTaskLifecycle(s, run.ID, integrator, nil)
	reviewer := decodeTaskDispatch(t, <-conn.send)
	conclusion := &protocol.RunConclusion{Outcome: "satisfied", Summary: "independent review passed"}
	sendTaskLifecycle(s, run.ID, reviewer, conclusion)
	// A duplicate terminal delivery is idempotent at the graph-level barrier.
	s.handleOrchestrationEvent(context.Background(), protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", protocol.OrchestrationEventPayload{
		RunID: run.ID, Kind: "run.end", Content: "duplicate", Task: &reviewer, RunConclusion: conclusion,
	}))

	loaded, err = st.OrchestrationRunByID(context.Background(), run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.OrchestrationCompleted {
		t.Fatalf("reviewer did not complete public run: %#v", loaded)
	}
	events, err := st.ListOrchestrationEvents(context.Background(), run.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	var publicEnd, publicConclusion int
	for _, event := range events {
		if event.Task != nil {
			if event.Status != "" {
				t.Fatalf("task event changed public status: %#v", event)
			}
			continue
		}
		if event.Kind == "run.end" {
			publicEnd++
		}
		if event.Kind == "run.conclusion" {
			publicConclusion++
		}
	}
	if publicEnd != 1 || publicConclusion != 1 {
		t.Fatalf("public terminal events: run.end=%d conclusion=%d events=%#v", publicEnd, publicConclusion, events)
	}
}

func TestDurableTaskGraphRunsEveryConfiguredRound(t *testing.T) {
	s, st, userID, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, userID, agentID)
	payload := protocol.OrchestrationStartPayload{
		RunID: run.ID, Mode: "collaboration", WorkerPair: protocol.WorkerPairCodexCodex,
		Prompt: "finish across two rounds", CWD: t.TempDir(), MaxTurns: 2, MaxTurnsRequested: 2,
	}
	conn := testBridgeConn(agentID, 16)
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)
	if err := s.createAndDispatchTaskGraph(context.Background(), run, payload); err != nil {
		t.Fatal(err)
	}

	completeRound := func(wantRound int, conclusion *protocol.RunConclusion) {
		t.Helper()
		for position := 0; position < 4; position++ {
			dispatched := decodeTaskDispatchPayload(t, <-conn.send)
			if dispatched.MaxTurns != 1 || dispatched.MaxTurnsRequested != 2 {
				t.Fatalf("round %d node limits = %d requested=%d", wantRound, dispatched.MaxTurns, dispatched.MaxTurnsRequested)
			}
			if dispatched.TaskGraph.Round != wantRound || dispatched.TaskGraph.MaxRounds != 2 {
				t.Fatalf("round metadata = %#v, want %d/2", dispatched.TaskGraph, wantRound)
			}
			task := dispatched.TaskGraph.Tasks[0]
			ref := protocol.TaskAttemptRef{GraphID: dispatched.TaskGraph.ID, TaskID: task.ID, AttemptID: task.AttemptID, Role: task.Role, WorkerSlot: task.WorkerSlot, PayloadDigest: task.PayloadDigest}
			var nodeConclusion *protocol.RunConclusion
			if position == 3 {
				nodeConclusion = conclusion
			}
			sendTaskLifecycle(s, run.ID, ref, nodeConclusion)
		}
	}

	completeRound(1, &protocol.RunConclusion{Outcome: "blocked", Summary: "more work remains", UnmetObligations: []string{"finish implementation"}})
	loaded, err := st.OrchestrationRunByID(context.Background(), run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.OrchestrationRunning {
		t.Fatalf("intermediate review ended run: %#v", loaded)
	}
	graph, err := st.TaskGraphByRun(context.Background(), run.ID)
	if err != nil || graph.Generation != 2 || graph.Status != store.TaskGraphRunning {
		t.Fatalf("next graph = %#v err=%v", graph, err)
	}

	completeRound(2, &protocol.RunConclusion{Outcome: "satisfied", Summary: "final review passed"})
	loaded, err = st.OrchestrationRunByID(context.Background(), run.ID, userID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != store.OrchestrationCompleted {
		t.Fatalf("final round did not complete run: %#v", loaded)
	}
}

func TestReconnectDispatchesOnlyReadyDurableTasks(t *testing.T) {
	s, st, userID, agentID := newOrchestrationTestServer(t)
	run := createOrchestrationRun(t, st, userID, agentID)
	createHubTaskGraph(t, st, run)
	conn := testBridgeConn(agentID, 4)
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	s.dispatchReadyTaskGraphsForAgent(context.Background(), agentID)
	first := decodeTaskDispatch(t, <-conn.send)
	if first.Role != store.TaskRoleWorker {
		t.Fatalf("reconnect dispatch = %#v", first)
	}
	select {
	case extra := <-conn.send:
		t.Fatalf("unexpected reconnect dispatch: %#v", extra)
	default:
	}
}
