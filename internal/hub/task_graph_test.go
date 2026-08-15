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
	plan := reduceOrchestrationPlan(events)
	if len(plan.Items) != 2 {
		t.Fatalf("reduced reviewed plan = %#v", plan)
	}
	if plan.Items[0].ID != "P1" || plan.Items[0].Title != "reviewed one" || plan.Items[0].Status != "completed" || plan.Items[0].Progress != 100 || plan.Items[0].Evidence != "coqc Proof.v passed" {
		t.Fatalf("reduced P1 = %#v", plan.Items[0])
	}
	if plan.Items[1].ID != "P3" || plan.Items[1].Title != "reviewed three" || plan.Items[1].Status != "blocked" || plan.Items[1].Evidence != "missing axiom" {
		t.Fatalf("reduced P3 = %#v", plan.Items[1])
	}
}

func TestReduceOrchestrationPlanProjectsEnrichedMetadataAndFiltersDependencies(t *testing.T) {
	ref := func(name string) *protocol.TaskAttemptRef { return &protocol.TaskAttemptRef{Name: name} }
	events := []store.OrchestrationEvent{
		{Task: ref("plan"), Content: "[PLAN_GOAL] 证明锁协议满足互斥性\n" +
			"[PLAN_ITEM priority=\"2\" id=\"P2\" branch=\"归纳步\" depends=\"P1,P2,P9,P1\" status=\"pending\" difficulty=\"hard\" kind=\"proof\"] 证明归纳保持 | 依赖基础定义\n" +
			"[PLAN_ITEM id=\"P1\" status=\"completed\" kind=\"research\" difficulty=\"easy\" priority=\"1\" depends=\"\"] 建立状态模型 | 固化语义"},
		{Task: ref("candidate-a"), Content: "[PLAN_UPDATE progress=\"40\" status=\"in_progress\" id=\"P2\"] 已完成关键引理\n" +
			"[PLAN_UPDATE id=\"P9\" status=\"completed\"] unknown\n" +
			"[PLAN_UPDATE id=\"P2\" status=\"invalid\"] forged"},
	}
	plan := reduceOrchestrationPlan(events)
	if plan.Goal != "证明锁协议满足互斥性" || plan.Total != 2 || plan.Completed != 1 || plan.InProgress != 1 || plan.Percent != 50 || plan.CurrentFocus != "P2" {
		t.Fatalf("enriched plan summary = %#v", plan)
	}
	if plan.Labels["branch"] != "证明分支" || plan.Labels["progress"] != "局部进度" {
		t.Fatalf("localized labels = %#v", plan.Labels)
	}
	item := plan.Items[0]
	if item.ID != "P2" || item.Branch != "归纳步" || item.Kind != "proof" || item.Difficulty != "hard" || item.Priority != 2 || item.Progress != 40 || item.Evidence != "已完成关键引理" {
		t.Fatalf("enriched item = %#v", item)
	}
	if len(item.DependsOn) != 1 || item.DependsOn[0] != "P1" || len(item.BlockedBy) != 0 {
		t.Fatalf("filtered dependencies = %#v blocked=%#v", item.DependsOn, item.BlockedBy)
	}
}

func TestReduceOrchestrationPlanReviewReplacesGoalAndList(t *testing.T) {
	ref := func(name string) *protocol.TaskAttemptRef { return &protocol.TaskAttemptRef{Name: name} }
	events := []store.OrchestrationEvent{
		{Task: ref("plan"), Content: "[PLAN_GOAL] 草稿目标\n[PLAN_ITEM id=\"P1\" status=\"pending\"] 草稿"},
		{Task: ref("plan-review"), Content: "[PLAN_GOAL] 审核目标\n[PLAN_ITEM id=\"P2\" status=\"pending\" difficulty=\"bogus\" priority=\"999\" depends=\"\"] 审核项"},
	}
	plan := reduceOrchestrationPlan(events)
	if plan.Goal != "审核目标" || len(plan.Items) != 1 || plan.Items[0].ID != "P2" {
		t.Fatalf("review did not replace plan = %#v", plan)
	}
	if plan.Items[0].Difficulty != "" || plan.Items[0].Priority != 1 || !plan.Items[0].Ready || plan.Ready != 1 {
		t.Fatalf("invalid metadata was not normalized = %#v", plan.Items[0])
	}
}

func TestReduceOrchestrationPlanReviewBoundaryDoesNotMixDraftItems(t *testing.T) {
	ref := func(name string) *protocol.TaskAttemptRef { return &protocol.TaskAttemptRef{Name: name} }
	events := []store.OrchestrationEvent{
		{Task: ref("plan"), Content: "[PLAN_GOAL] 草稿目标\n[PLAN_ITEM id=\"P1\" status=\"pending\"] 草稿项"},
		{Task: ref("plan-review"), Content: "审核输出格式无效，不能作为结构化计划。"},
	}
	plan := reduceOrchestrationPlan(events)
	if plan.Goal != "" || len(plan.Items) != 0 {
		t.Fatalf("review boundary mixed draft and reviewed state = %#v", plan)
	}
}

func TestMergeOrchestrationProgressEventsRetainsHistoricalPlanMarkers(t *testing.T) {
	ref := func(graphID, name string) *protocol.TaskAttemptRef {
		return &protocol.TaskAttemptRef{GraphID: graphID, Name: name}
	}
	live := []store.OrchestrationEvent{
		{Seq: 10001, Kind: "turn.delta", Content: "latest output"},
		{Seq: 10002, Kind: "turn.end", Content: "latest completion"},
	}
	plan := []store.OrchestrationEvent{
		{Seq: 5, Kind: "run.end", Task: ref("otg_current", "plan-review"), Content: "[PLAN_GOAL] 完成证明\n[PLAN_ITEM id=\"P1\" status=\"pending\"] 验证引理"},
		live[1],
	}
	merged := mergeOrchestrationProgressEvents(live, plan)
	if len(merged) != 3 || merged[0].Seq != 5 || merged[1].Seq != 10001 || merged[2].Seq != 10002 {
		t.Fatalf("merged events = %#v", merged)
	}
	progress := reduceOrchestrationPlan(merged)
	if progress.Goal != "完成证明" || progress.Total != 1 || progress.Items[0].Title != "验证引理" {
		t.Fatalf("historical plan was lost: %#v", progress)
	}
}

func TestGroupOrchestrationProgressGraphsKeepsRoundsUnderTheirPrompt(t *testing.T) {
	payloadJSON := func(prompt string, promptSeq int64) string {
		raw, err := json.Marshal(protocol.OrchestrationStartPayload{Prompt: prompt, PromptSeq: promptSeq})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	graphs := []store.OrchestrationTaskGraph{
		{ID: "otg_1", Generation: 1, Status: store.TaskGraphCompleted, PayloadJSON: payloadJSON("初始证明任务", 0), CreatedAt: 100, UpdatedAt: 120, FinishedAt: 120},
		{ID: "otg_2", Generation: 2, Status: store.TaskGraphCompleted, PayloadJSON: payloadJSON("初始证明任务", 0), CreatedAt: 121, UpdatedAt: 150, FinishedAt: 150},
		{ID: "otg_3", Generation: 3, Status: store.TaskGraphRunning, PayloadJSON: payloadJSON("补充证明边界", 9), CreatedAt: 200, UpdatedAt: 220},
	}

	groups := groupOrchestrationProgressGraphs(graphs, nil)
	if len(groups) != 2 {
		t.Fatalf("task groups = %#v", groups)
	}
	first := groups[0]
	if first.TaskNumber != 1 || first.PromptSeq != 0 || first.Prompt != "初始证明任务" || first.CreatedAt != 100 || first.UpdatedAt != 150 || first.FinishedAt != 150 || first.Status != store.TaskGraphCompleted || len(first.Graphs) != 2 || first.Graph == nil || first.Graph.ID != "otg_2" {
		t.Fatalf("initial task group = %#v", first)
	}
	second := groups[1]
	if second.TaskNumber != 2 || second.PromptSeq != 9 || second.Prompt != "补充证明边界" || second.CreatedAt != 200 || second.UpdatedAt != 220 || second.FinishedAt != 0 || second.Status != store.TaskGraphRunning || len(second.Graphs) != 1 || second.Graph == nil || second.Graph.ID != "otg_3" {
		t.Fatalf("follow-up task group = %#v", second)
	}
}

func TestGroupOrchestrationProgressGraphsSeparatesLegacyPromptChangesAndInvalidPayloads(t *testing.T) {
	payload := func(prompt string) string {
		raw, err := json.Marshal(protocol.OrchestrationStartPayload{Prompt: prompt})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	graphs := []store.OrchestrationTaskGraph{
		{ID: "otg_1", PayloadJSON: payload("first"), CreatedAt: 100},
		{ID: "otg_2", PayloadJSON: payload("first"), CreatedAt: 110},
		{ID: "otg_3", PayloadJSON: payload("followup"), CreatedAt: 120},
		{ID: "otg_4", PayloadJSON: "{", CreatedAt: 130},
		{ID: "otg_5", PayloadJSON: "{", CreatedAt: 140},
	}

	groups := groupOrchestrationProgressGraphs(graphs, nil)
	if len(groups) != 4 || len(groups[0].Graphs) != 2 || groups[1].Prompt != "followup" || groups[2].Graph.ID != "otg_4" || groups[3].Graph.ID != "otg_5" {
		t.Fatalf("legacy task groups = %#v", groups)
	}
}

func TestGroupOrchestrationProgressGraphsProjectsPlanPerTask(t *testing.T) {
	payload := func(prompt string, promptSeq int64) string {
		raw, err := json.Marshal(protocol.OrchestrationStartPayload{Prompt: prompt, PromptSeq: promptSeq})
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	graphs := []store.OrchestrationTaskGraph{
		{ID: "otg_first", Generation: 1, Status: store.TaskGraphCompleted, PayloadJSON: payload("first", 1)},
		{ID: "otg_second", Generation: 2, Status: store.TaskGraphCompleted, PayloadJSON: payload("second", 2)},
	}
	ref := func(graphID, name string) *protocol.TaskAttemptRef {
		return &protocol.TaskAttemptRef{GraphID: graphID, Name: name}
	}
	events := []store.OrchestrationEvent{
		{Task: ref("otg_first", "plan-review"), Content: "[PLAN_ITEM id=\"P1\" status=\"pending\"] first item"},
		{Task: ref("otg_first", "review"), Content: "[PLAN_UPDATE id=\"P1\" status=\"completed\"] first evidence"},
		{Task: ref("otg_second", "plan-review"), Content: "[PLAN_ITEM id=\"P2\" status=\"pending\"] second item"},
	}

	tasks := groupOrchestrationProgressGraphs(graphs, events)
	if len(tasks) != 2 || len(tasks[0].PlanItems) != 1 || len(tasks[1].PlanItems) != 1 {
		t.Fatalf("task plans = %#v", tasks)
	}
	if tasks[0].PlanItems[0].ID != "P1" || tasks[0].PlanItems[0].Status != "completed" || tasks[1].PlanItems[0].ID != "P2" || tasks[1].PlanItems[0].Status != "pending" {
		t.Fatalf("task plan isolation = %#v", tasks)
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
	graphs, ok := body["graphs"].([]any)
	if !ok || len(graphs) != 1 || graphs[0].(map[string]any)["id"] != graph.ID {
		t.Fatalf("admin progress graphs = %#v", body["graphs"])
	}
	tasks, ok := body["tasks"].([]any)
	if !ok || len(tasks) != 1 {
		t.Fatalf("admin progress tasks = %#v", body["tasks"])
	}
	task := tasks[0].(map[string]any)
	if task["taskNumber"] != float64(1) || task["prompt"] != "prove" || len(task["graphs"].([]any)) != 1 {
		t.Fatalf("admin progress task = %#v", task)
	}
	items, ok := body["planItems"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("admin progress items = %#v", body["planItems"])
	}
	item := items[0].(map[string]any)
	if item["title"] != "prove reviewed" || item["status"] != "completed" || item["evidence"] != "coqc passed" {
		t.Fatalf("admin progress item = %#v", item)
	}
	plan, ok := body["plan"].(map[string]any)
	if !ok || plan["total"] != float64(1) || plan["completed"] != float64(1) || plan["percent"] != float64(100) {
		t.Fatalf("admin progress summary = %#v", body["plan"])
	}
	labels, ok := plan["labels"].(map[string]any)
	if !ok || labels["goal"] != "总体目标" || labels["progress"] != "局部进度" {
		t.Fatalf("admin progress labels = %#v", plan["labels"])
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
