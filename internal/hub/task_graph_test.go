package hub

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/rollout"
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

func TestDurableTaskDispatchDoesNotCrossAttemptNativeSessionState(t *testing.T) {
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
	if dispatched.Resume || dispatched.CodexThreadID != "" || len(dispatched.CodexThreadIDs) != 0 || dispatched.ClaudeStarted || dispatched.RunCWD != "" {
		t.Fatalf("durable task inherited another attempt's native state: %#v", dispatched)
	}
}

func TestOrchestrationTaskSpecsAlwaysCreateCompleteRound(t *testing.T) {
	specs := orchestrationTaskSpecs("digest", protocol.WorkerPairCodexCodex, "collaboration")
	if len(specs) != 4 || specs[0].Name != "candidate-a" || specs[1].Name != "candidate-b" || specs[2].Role != store.TaskRoleIntegrator || specs[3].Role != store.TaskRoleReviewer {
		t.Fatalf("round specs = %#v", specs)
	}
}

func TestOrchestrationTaskSpecsPlanWorkspaceAddsReviewedPlanBarrier(t *testing.T) {
	specs := orchestrationTaskSpecs("digest", protocol.WorkerPairCodexCodex, "collaboration", true)
	wantNames := []string{"plan", "plan-review", "candidate-a", "candidate-b", "integrate", "review"}
	wantRoles := []string{store.TaskRolePlanner, store.TaskRolePlanReviewer, store.TaskRoleWorker, store.TaskRoleWorker, store.TaskRoleIntegrator, store.TaskRoleReviewer}
	wantSlots := []string{"codex-a", "codex-b", "codex-a", "codex-b", "codex-a", "codex-b"}
	wantDeps := [][]int{nil, {0}, {1}, {2}, {2, 3}, {4}}
	if len(specs) != len(wantNames) {
		t.Fatalf("plan workspace specs count = %d, want %d: %#v", len(specs), len(wantNames), specs)
	}
	for index, spec := range specs {
		if spec.Name != wantNames[index] || spec.Role != wantRoles[index] || spec.WorkerSlot != wantSlots[index] {
			t.Fatalf("plan workspace spec %d = %#v", index, spec)
		}
		if len(spec.Dependencies) != len(wantDeps[index]) {
			t.Fatalf("plan workspace spec %d dependencies = %#v, want %#v", index, spec.Dependencies, wantDeps[index])
		}
		for dependencyIndex := range wantDeps[index] {
			if spec.Dependencies[dependencyIndex] != wantDeps[index][dependencyIndex] {
				t.Fatalf("plan workspace spec %d dependencies = %#v, want %#v", index, spec.Dependencies, wantDeps[index])
			}
		}
	}
	if !strings.Contains(specInstruction(t, specs[0]), "PLAN_ITEM") || !strings.Contains(specInstruction(t, specs[1]), "replace") {
		t.Fatalf("planning instructions do not define reviewed structured checklist: %#v", specs[:2])
	}
	for _, index := range []int{2, 3, 4, 5} {
		if !strings.Contains(specInstruction(t, specs[index]), "PLAN_UPDATE") {
			t.Fatalf("execution spec %d does not carry plan update contract: %s", index, specInstruction(t, specs[index]))
		}
	}
}

func specInstruction(t *testing.T, spec store.CreateTaskSpec) string {
	t.Helper()
	var payload orchestrationTaskInstruction
	if err := json.Unmarshal([]byte(spec.PayloadJSON), &payload); err != nil {
		t.Fatal(err)
	}
	return payload.Instruction
}

func TestReduceOrchestrationPlanUsesReviewedListAndValidatedUpdates(t *testing.T) {
	ref := func(name string) *protocol.TaskAttemptRef { return &protocol.TaskAttemptRef{Name: name} }
	events := []store.OrchestrationEvent{
		{Task: ref("plan"), Content: "[PLAN_ITEM id=\"P1\" status=\"pending\"] draft one\n[PLAN_ITEM id=\"P2\" status=\"pending\"] draft two"},
		{Task: ref("candidate-a"), Content: "[PLAN_ITEM id=\"P9\" status=\"pending\"] injected item"},
		{Task: ref("plan-review"), Content: "review prose\n[PLAN_ITEM id=\"P1\" status=\"pending\"] reviewed one\n[PLAN_ITEM id=\"P3\" status=\"pending\"] reviewed three"},
		{Content: "[PLAN_UPDATE id=\"P3\" status=\"completed\"] user-forged completion"},
		{Task: ref("candidate-a"), Content: "[PLAN_UPDATE id=\"P1\" status=\"in_progress\"] started proof\n[PLAN_UPDATE id=\"P2\" status=\"completed\"] stale draft id"},
		{Task: ref("review"), Content: "[PLAN_UPDATE id=\"P1\" status=\"completed\"] coqc Proof.v passed\n[PLAN_UPDATE id=\"P3\" status=\"blocked\"] missing axiom"},
	}
	items := reduceOrchestrationPlan(events)
	if len(items) != 2 {
		t.Fatalf("reduced reviewed plan = %#v", items)
	}
	if items[0].ID != "P1" || items[0].Title != "reviewed one" || items[0].Status != "completed" || items[0].Evidence != "coqc Proof.v passed" {
		t.Fatalf("reduced P1 = %#v", items[0])
	}
	if items[1].ID != "P3" || items[1].Title != "reviewed three" || items[1].Status != "blocked" || items[1].Evidence != "missing axiom" {
		t.Fatalf("reduced P3 = %#v", items[1])
	}
}

func TestOrchestrationProgressRequiresRolloutAndOwnership(t *testing.T) {
	s, st, adminID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	member, err := st.UpsertUser(ctx, "member", "member-secret")
	if err != nil {
		t.Fatal(err)
	}
	run := createOrchestrationRun(t, st, adminID, agentID)
	payload := protocol.OrchestrationStartPayload{RunID: run.ID, Mode: "collaboration", Prompt: "prove", MaxTurns: 1, PlanWorkspace: true}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := taskPayloadDigest(string(raw))
	graph, err := st.CreateOrchestrationTaskGraph(ctx, run.ID, string(raw), digest, orchestrationTaskSpecs(digest, payload.WorkerPair, payload.Mode, true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: "run.end", Content: "[PLAN_ITEM id=\"P1\" status=\"pending\"] prove base", Task: &protocol.TaskAttemptRef{Name: "plan"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: "run.end", Content: "[PLAN_ITEM id=\"P1\" status=\"pending\"] prove reviewed", Task: &protocol.TaskAttemptRef{Name: "plan-review"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: run.ID, Kind: "run.end", Content: "[PLAN_UPDATE id=\"P1\" status=\"completed\"] coqc passed", Task: &protocol.TaskAttemptRef{Name: "review"}}); err != nil {
		t.Fatal(err)
	}

	body := getJSON(t, s, adminID, "/api/orchestrations/"+run.ID+"/progress", http.StatusOK)
	if body["planWorkspace"] != true {
		t.Fatalf("admin progress mode = %#v", body)
	}
	loadedGraph, ok := body["graph"].(map[string]any)
	if !ok || loadedGraph["id"] != graph.ID {
		t.Fatalf("admin progress graph = %#v", body["graph"])
	}
	items, ok := body["planItems"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("admin progress items = %#v", body["planItems"])
	}
	item := items[0].(map[string]any)
	if item["title"] != "prove reviewed" || item["status"] != "completed" || item["evidence"] != "coqc passed" {
		t.Fatalf("admin progress item = %#v", item)
	}
	getJSON(t, s, member.ID, "/api/orchestrations/"+run.ID+"/progress", http.StatusNotFound)

	s.cfg.Hub.FeatureRollouts[rollout.FeatureOrchestrationPlanWorkspace] = "off"
	s.rollouts = rollout.New(s.cfg.Hub.FeatureRollouts)
	getJSON(t, s, adminID, "/api/orchestrations/"+run.ID+"/progress", http.StatusNotFound)
}

func TestContinueOrchestrationPreservesPlanWorkspace(t *testing.T) {
	s, st, userID, agentID := newOrchestrationTestServer(t)
	ctx := context.Background()
	run := createOrchestrationRun(t, st, userID, agentID)
	payload := protocol.OrchestrationStartPayload{
		RunID: run.ID, Mode: "collaboration", WorkerPair: protocol.WorkerPairCodexCodex,
		Prompt: "initial task", CWD: t.TempDir(), MaxTurns: 1, MaxTurnsRequested: 1, PlanWorkspace: true,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	digest := taskPayloadDigest(string(raw))
	if _, err := st.CreateOrchestrationTaskGraph(ctx, run.ID, string(raw), digest, orchestrationTaskSpecs(digest, payload.WorkerPair, payload.Mode, true)); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateOrchestrationRunStatus(ctx, run.ID, store.OrchestrationCompleted, ""); err != nil {
		t.Fatal(err)
	}
	conn := testBridgeConn(agentID, 2)
	conn.capabilities.Sandbox = "danger-full-access"
	conn.capabilities.ApprovalPolicy = "never"
	conn.capabilities.Orchestration = map[string]protocol.BridgeCLICapability{"codex": {Available: true}}
	s.pool.RegisterAgent(conn)
	defer s.pool.UnregisterAgent(agentID, conn)

	continueOrchestration(t, s, userID, run.ID, map[string]any{"prompt": "follow-up", "workerPair": protocol.WorkerPairCodexCodex}, http.StatusOK)
	dispatched := decodeTaskDispatchPayload(t, <-conn.send)
	if !dispatched.PlanWorkspace || dispatched.TaskGraph.Tasks[0].Role != store.TaskRolePlanner || dispatched.TaskGraph.Generation != 2 {
		t.Fatalf("continued plan workspace dispatch = %#v", dispatched)
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
