package hub

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/rollout"
	"github.com/tencent/codex-bridge/internal/serverutil"
	"github.com/tencent/codex-bridge/internal/store"
)

type orchestrationPlanItem struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	Status     string   `json:"status"`
	Kind       string   `json:"kind,omitempty"`
	Branch     string   `json:"branch,omitempty"`
	Difficulty string   `json:"difficulty,omitempty"`
	Priority   int      `json:"priority,omitempty"`
	DependsOn  []string `json:"dependsOn,omitempty"`
	Progress   int      `json:"progress"`
	Rationale  string   `json:"rationale,omitempty"`
	Evidence   string   `json:"evidence,omitempty"`
	Ready      bool     `json:"ready"`
	BlockedBy  []string `json:"blockedBy,omitempty"`
}

type orchestrationPlanProgress struct {
	Goal         string                  `json:"goal,omitempty"`
	Items        []orchestrationPlanItem `json:"items"`
	Total        int                     `json:"total"`
	Completed    int                     `json:"completed"`
	InProgress   int                     `json:"inProgress"`
	Blocked      int                     `json:"blocked"`
	Pending      int                     `json:"pending"`
	Ready        int                     `json:"ready"`
	Percent      int                     `json:"percent"`
	CurrentFocus string                  `json:"currentFocus,omitempty"`
	Labels       map[string]string       `json:"labels,omitempty"`
}

type orchestrationProgressTask struct {
	TaskNumber int                            `json:"taskNumber"`
	PromptSeq  int64                          `json:"promptSeq,omitempty"`
	Prompt     string                         `json:"prompt,omitempty"`
	Status     string                         `json:"status,omitempty"`
	CreatedAt  int64                          `json:"createdAt,omitempty"`
	UpdatedAt  int64                          `json:"updatedAt,omitempty"`
	FinishedAt int64                          `json:"finishedAt,omitempty"`
	Graphs     []store.OrchestrationTaskGraph `json:"graphs"`
	Graph      *store.OrchestrationTaskGraph  `json:"graph,omitempty"`
	PlanItems  []orchestrationPlanItem        `json:"planItems"`
	Plan       orchestrationPlanProgress      `json:"plan"`
}

var (
	orchestrationPlanGoalMarker = regexp.MustCompile(`(?m)^\[PLAN_GOAL\]\s*(.+?)\s*$`)
	orchestrationPlanMarker     = regexp.MustCompile(`^\[(PLAN_ITEM|PLAN_UPDATE)\s+([^\]]+)\]\s*(.*?)\s*$`)
	orchestrationPlanAttribute  = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9_-]*)="([^"]*)"`)
)

var orchestrationPlanLabels = map[string]string{
	"goal":         "总体目标",
	"branch":       "证明分支",
	"difficulty":   "难度",
	"priority":     "优先级",
	"dependencies": "依赖",
	"progress":     "局部进度",
}

func reduceOrchestrationPlan(events []store.OrchestrationEvent) orchestrationPlanProgress {
	items := make([]orchestrationPlanItem, 0, 12)
	indexes := make(map[string]int)
	planReviewStarted := false
	goal := ""
	for _, event := range events {
		name := ""
		if event.Task != nil {
			name = event.Task.Name
		}
		if name == "plan-review" && !planReviewStarted {
			items = items[:0]
			indexes = make(map[string]int)
			goal = ""
			planReviewStarted = true
		}
		if name == "plan" || name == "plan-review" {
			if match := orchestrationPlanGoalMarker.FindStringSubmatch(event.Content); len(match) == 2 {
				goal = strings.TrimSpace(match[1])
			}
		}
		for _, line := range strings.Split(event.Content, "\n") {
			match := orchestrationPlanMarker.FindStringSubmatch(strings.TrimSpace(line))
			if len(match) != 4 {
				continue
			}
			kind, attributes, detail := match[1], parsePlanAttributes(match[2]), strings.TrimSpace(match[3])
			id, status := attributes["id"], attributes["status"]
			if !validPlanID(id) || !validPlanStatus(status) {
				continue
			}
			if kind == "PLAN_ITEM" {
				if name != "plan" && name != "plan-review" {
					continue
				}
				if _, exists := indexes[id]; exists || len(items) >= 12 {
					continue
				}
				title, rationale := splitPlanItemDetail(detail)
				if title == "" {
					continue
				}
				priority := len(items) + 1
				if parsed := boundedPlanNumber(attributes["priority"], 1, 99); parsed > 0 {
					priority = parsed
				}
				item := orchestrationPlanItem{
					ID: id, Title: title, Status: status, Priority: priority,
					Branch: strings.TrimSpace(attributes["branch"]), Rationale: rationale,
					DependsOn: parsePlanDependencies(attributes["depends"]),
				}
				if validPlanKind(attributes["kind"]) {
					item.Kind = attributes["kind"]
				}
				if validPlanDifficulty(attributes["difficulty"]) {
					item.Difficulty = attributes["difficulty"]
				}
				item.Progress = planProgress(status, attributes["progress"], 0)
				indexes[id] = len(items)
				items = append(items, item)
				continue
			}
			index, exists := indexes[id]
			if !exists || (name != "candidate-a" && name != "candidate-b" && name != "integrate" && name != "review") {
				continue
			}
			items[index].Status = status
			items[index].Evidence = detail
			items[index].Progress = planProgress(status, attributes["progress"], items[index].Progress)
		}
	}
	return projectOrchestrationPlan(goal, items)
}

func parsePlanAttributes(raw string) map[string]string {
	attributes := make(map[string]string)
	for _, match := range orchestrationPlanAttribute.FindAllStringSubmatch(raw, -1) {
		attributes[strings.ToLower(match[1])] = strings.TrimSpace(match[2])
	}
	return attributes
}

func validPlanID(id string) bool {
	if len(id) == 0 || len(id) > 32 || (id[0] < 'A' || id[0] > 'Z') && (id[0] < 'a' || id[0] > 'z') {
		return false
	}
	for _, char := range id[1:] {
		if (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func validPlanStatus(status string) bool {
	return status == "pending" || status == "in_progress" || status == "completed" || status == "blocked"
}

func validPlanKind(kind string) bool {
	return kind == "proof" || kind == "implementation" || kind == "verification" || kind == "research"
}

func validPlanDifficulty(difficulty string) bool {
	return difficulty == "easy" || difficulty == "medium" || difficulty == "hard" || difficulty == "critical"
}

func boundedPlanNumber(raw string, minimum, maximum int) int {
	value := -1
	if _, err := fmt.Sscanf(strings.TrimSpace(raw), "%d", &value); err != nil || value < minimum || value > maximum {
		return -1
	}
	return value
}

func planProgress(status, raw string, previous int) int {
	if status == "completed" {
		return 100
	}
	if status == "pending" {
		return 0
	}
	if strings.TrimSpace(raw) != "" {
		if parsed := boundedPlanNumber(raw, 0, 100); parsed >= 0 {
			return parsed
		}
	}
	if previous > 0 {
		return previous
	}
	if status == "in_progress" {
		return 50
	}
	return 0
}

func splitPlanItemDetail(detail string) (string, string) {
	parts := strings.SplitN(strings.TrimSpace(detail), " | ", 2)
	if len(parts) == 1 {
		return parts[0], ""
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
}

func parsePlanDependencies(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool { return r == ',' || r == ' ' })
	dependencies := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		dependencies = append(dependencies, part)
	}
	return dependencies
}

func projectOrchestrationPlan(goal string, items []orchestrationPlanItem) orchestrationPlanProgress {
	knownIDs := make(map[string]struct{}, len(items))
	statuses := make(map[string]string, len(items))
	for _, item := range items {
		knownIDs[item.ID] = struct{}{}
		statuses[item.ID] = item.Status
	}
	progress := orchestrationPlanProgress{Goal: goal, Items: items, Total: len(items), Labels: orchestrationPlanLabels}
	for index := range progress.Items {
		item := &progress.Items[index]
		dependencies := item.DependsOn[:0]
		seen := make(map[string]struct{}, len(item.DependsOn))
		for _, dependency := range item.DependsOn {
			if dependency == item.ID {
				continue
			}
			if _, exists := knownIDs[dependency]; !exists {
				continue
			}
			if _, exists := seen[dependency]; exists {
				continue
			}
			seen[dependency] = struct{}{}
			dependencies = append(dependencies, dependency)
			if statuses[dependency] != "completed" {
				item.BlockedBy = append(item.BlockedBy, dependency)
			}
		}
		item.DependsOn = dependencies
		item.Ready = item.Status == "pending" && len(item.BlockedBy) == 0
		switch item.Status {
		case "completed":
			progress.Completed++
		case "in_progress":
			progress.InProgress++
			if progress.CurrentFocus == "" {
				progress.CurrentFocus = item.ID
			}
		case "blocked":
			progress.Blocked++
		default:
			progress.Pending++
			if item.Ready {
				progress.Ready++
			}
		}
	}
	if progress.CurrentFocus == "" {
		for _, item := range progress.Items {
			if item.Ready {
				progress.CurrentFocus = item.ID
				break
			}
		}
	}
	if progress.Total > 0 {
		progress.Percent = progress.Completed * 100 / progress.Total
	}
	return progress
}

func groupOrchestrationProgressGraphs(graphs []store.OrchestrationTaskGraph, events []store.OrchestrationEvent) []orchestrationProgressTask {
	groups := make([]orchestrationProgressTask, 0)
	var previousPayload protocol.OrchestrationStartPayload
	previousPayloadValid := false
	for _, graph := range graphs {
		var payload protocol.OrchestrationStartPayload
		payloadValid := json.Unmarshal([]byte(graph.PayloadJSON), &payload) == nil
		payload.Prompt = strings.TrimSpace(payload.Prompt)
		continuesPrevious := len(groups) > 0 && sameOrchestrationUserTask(previousPayload, previousPayloadValid, payload, payloadValid)
		if !continuesPrevious {
			groups = append(groups, orchestrationProgressTask{
				TaskNumber: len(groups) + 1, PromptSeq: payload.PromptSeq, Prompt: payload.Prompt,
				CreatedAt: graph.CreatedAt, UpdatedAt: graph.UpdatedAt, Status: graph.Status,
				Graphs: make([]store.OrchestrationTaskGraph, 0, 1), PlanItems: []orchestrationPlanItem{},
				Plan: orchestrationPlanProgress{Items: []orchestrationPlanItem{}},
			})
		}
		group := &groups[len(groups)-1]
		group.Graphs = append(group.Graphs, graph)
		graphCopy := graph
		group.Graph = &graphCopy
		group.Status = graph.Status
		group.UpdatedAt = graph.UpdatedAt
		group.FinishedAt = graph.FinishedAt
		previousPayload = payload
		previousPayloadValid = payloadValid
	}
	graphGroup := make(map[string]int, len(graphs))
	for index := range groups {
		for _, graph := range groups[index].Graphs {
			graphGroup[graph.ID] = index
		}
	}
	taskEvents := make([][]store.OrchestrationEvent, len(groups))
	for _, event := range events {
		if event.Task == nil {
			continue
		}
		if index, exists := graphGroup[event.Task.GraphID]; exists {
			taskEvents[index] = append(taskEvents[index], event)
		} else if event.Task.GraphID == "" && len(groups) > 0 {
			// Older persisted task events predate graph-scoped references. Keep
			// their previous latest-plan behavior without leaking them into all
			// historical tasks.
			taskEvents[len(groups)-1] = append(taskEvents[len(groups)-1], event)
		}
	}
	for index := range groups {
		groups[index].Plan = reduceOrchestrationPlan(taskEvents[index])
		groups[index].PlanItems = groups[index].Plan.Items
	}
	return groups
}

// mergeOrchestrationProgressEvents preserves the bounded live-event query for
// transcript responsiveness while retaining the durable plan marker ledger.
// The same event can appear in both lists, so sequence is the stable identity.
func mergeOrchestrationProgressEvents(live, plan []store.OrchestrationEvent) []store.OrchestrationEvent {
	if len(plan) == 0 {
		return live
	}
	merged := make([]store.OrchestrationEvent, 0, len(live)+len(plan))
	seen := make(map[int64]struct{}, len(live)+len(plan))
	for _, event := range live {
		if _, exists := seen[event.Seq]; exists {
			continue
		}
		seen[event.Seq] = struct{}{}
		merged = append(merged, event)
	}
	for _, event := range plan {
		if _, exists := seen[event.Seq]; exists {
			continue
		}
		seen[event.Seq] = struct{}{}
		merged = append(merged, event)
	}
	slices.SortFunc(merged, func(left, right store.OrchestrationEvent) int {
		return cmp.Compare(left.Seq, right.Seq)
	})
	return merged
}

func sameOrchestrationUserTask(previous protocol.OrchestrationStartPayload, previousValid bool, current protocol.OrchestrationStartPayload, currentValid bool) bool {
	if !previousValid || !currentValid {
		return false
	}
	if previous.PromptSeq > 0 || current.PromptSeq > 0 {
		return previous.PromptSeq > 0 && previous.PromptSeq == current.PromptSeq
	}
	return previous.Prompt != "" && previous.Prompt == current.Prompt
}

func (s *Server) handleOrchestrationProgress(w http.ResponseWriter, r *http.Request, uid string) {
	user, err := s.store.UserByID(r.Context(), uid)
	if err != nil || !s.featureEnabled(rollout.FeatureOrchestrationPlanWorkspace, user) {
		serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration progress not found")
		return
	}
	runID := r.PathValue("runID")
	if _, err := s.store.OrchestrationRunByID(r.Context(), runID, uid); err != nil {
		serverutil.WriteError(w, http.StatusNotFound, "NOT_FOUND", "orchestration run not found")
		return
	}
	graphs, err := s.store.ListTaskGraphsByRun(r.Context(), runID)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration progress")
		return
	}
	if len(graphs) == 0 {
		serverutil.WriteJSON(w, http.StatusOK, map[string]any{"graph": nil, "graphs": graphs, "tasks": []orchestrationProgressTask{}, "planItems": []orchestrationPlanItem{}, "plan": orchestrationPlanProgress{Items: []orchestrationPlanItem{}}})
		return
	}
	graph := graphs[len(graphs)-1]
	var payload protocol.OrchestrationStartPayload
	_ = json.Unmarshal([]byte(graph.PayloadJSON), &payload)
	if !payload.PlanWorkspace {
		serverutil.WriteJSON(w, http.StatusOK, map[string]any{"graph": graph, "graphs": graphs, "tasks": []orchestrationProgressTask{}, "planItems": []orchestrationPlanItem{}, "plan": orchestrationPlanProgress{Items: []orchestrationPlanItem{}}, "planWorkspace": false})
		return
	}
	events, err := s.store.ListOrchestrationEvents(r.Context(), runID, 10000)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration progress events")
		return
	}
	planEvents, err := s.store.ListOrchestrationPlanEvents(r.Context(), runID)
	if err != nil {
		serverutil.WriteError(w, http.StatusInternalServerError, "STORE_ERROR", "failed to load orchestration plan events")
		return
	}
	events = mergeOrchestrationProgressEvents(events, planEvents)
	tasks := groupOrchestrationProgressGraphs(graphs, events)
	latestTask := tasks[len(tasks)-1]
	serverutil.WriteJSON(w, http.StatusOK, map[string]any{
		"graph":         graph,
		"graphs":        graphs,
		"tasks":         tasks,
		"planItems":     latestTask.PlanItems,
		"plan":          latestTask.Plan,
		"planWorkspace": true,
	})
}

type orchestrationTaskInstruction struct {
	Instruction string `json:"instruction"`
}

func taskPayloadDigest(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		_, _ = h.Write([]byte(part))
		_, _ = h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func orchestrationTaskSpecs(baseDigest, workerPair, mode string, planWorkspace ...bool) []store.CreateTaskSpec {
	workerA, workerB := "claude", "codex"
	if protocol.NormalizeOrchestrationWorkerPair(workerPair) == protocol.WorkerPairCodexCodex {
		workerA, workerB = "codex-a", "codex-b"
	}
	workerDuty := "Build one independent candidate in the user's selected project directory. Inspect the actual project, maximize useful implementation progress through viable approaches, run focused checks, and finish with an engineer-to-engineer handoff naming changed files, exact commands and results, repeated blockers, attempted approaches, and the next executable entry point."
	if mode == "debate" {
		workerDuty = "Develop one independent falsifiable solution or counterexample in the user's selected project directory. Test the strongest claim, make bounded changes when justified, and finish with a structured handoff naming changed files, exact commands, and remaining objections."
	}
	instructions := []orchestrationTaskInstruction{
		{Instruction: workerDuty + " You are candidate A; choose the strongest route, pursue it deeply, and do not wait for candidate B."},
		{Instruction: workerDuty + " You are candidate B; start from candidate A's workspace and evidence, improve it or choose a materially different route, and do not repeat its baseline scan."},
		{Instruction: "Act as the sole integrator in the user's selected project directory. Inspect both candidate handoffs and compact evidence, reconcile their actual changes, resolve conflicts, continue implementing remaining gaps, and run integration checks. Finish with an exact handoff for an independent reviewer rather than a summary-only report."},
		{Instruction: "Act as the independent reviewer in the user's selected project directory. Rerun relevant tests, audit the original requirement and risks, and directly fix safe in-scope defects before reporting. Return resolved, blocked, or needs_next with independent command evidence; only the final configured round may address a resolved final conclusion to the user. Formal-proof work must include a successful proof-assistant checker or audit command in this reviewing turn."},
	}
	roles := []string{store.TaskRoleWorker, store.TaskRoleWorker, store.TaskRoleIntegrator, store.TaskRoleReviewer}
	slots := []string{workerA, workerB, workerA, workerB}
	// All nodes write to the user-selected checkout. Candidate B waits for A so
	// durable scheduling never overlaps arbitrary filesystem side effects.
	deps := [][]int{nil, {0}, {0, 1}, {2}}
	names := []string{"candidate-a", "candidate-b", "integrate", "review"}
	if len(planWorkspace) > 0 && planWorkspace[0] {
		planInstruction := "Act as the planning agent. Inspect the user's initial request and selected workspace only as needed to produce the complete task plan before implementation. For formal-verification work, decompose the goal into concrete proof branches and verification obligations, identify dependencies, rate difficulty, and choose the recommended order. Do not implement. Write the goal, titles, and rationale in concise Chinese while preserving theorem, file, and command names. End with one exact [PLAN_GOAL] Chinese overall goal line and 2-12 exact lines [PLAN_ITEM id=\"P1\" status=\"pending\" kind=\"proof\" difficulty=\"hard\" priority=\"1\" depends=\"\"] Chinese task title | Chinese reason and recommended order. Allowed kind values: proof, implementation, verification, research. Allowed difficulty values: easy, medium, hard, critical. Use comma-separated P ids in depends, or an empty value for roots."
		planReviewInstruction := "Act as the independent plan reviewer. Audit the proposed complete plan against the original request, workspace constraints, formal-proof branches, dependencies, difficulty, recommended order, and verification obligations. Do not implement. Write all human-facing goal, titles, and rationale in concise Chinese while preserving theorem, file, and command names. End by restating one corrected [PLAN_GOAL] line and the complete corrected 2-12 item list using the exact enriched [PLAN_ITEM id=\"P1\" status=\"pending\" kind=\"proof\" difficulty=\"hard\" priority=\"1\" depends=\"\"] Chinese title | Chinese rationale form; these replace the planner list."
		updateSuffix := " Track the reviewed whole-task plan while working. When evidence changes an item, append exact lines [PLAN_UPDATE id=\"P1\" status=\"in_progress\" progress=\"40\"] concise Chinese evidence, [PLAN_UPDATE id=\"P1\" status=\"completed\" progress=\"100\"] concise Chinese evidence, or [PLAN_UPDATE id=\"P1\" status=\"blocked\" progress=\"40\"] concise Chinese blocker for ids from the reviewed plan. Progress is an integer from 0 through 100. Preserve command and theorem names verbatim."
		instructions = []orchestrationTaskInstruction{
			{Instruction: planInstruction},
			{Instruction: planReviewInstruction},
			{Instruction: instructions[0].Instruction + updateSuffix},
			{Instruction: instructions[1].Instruction + updateSuffix},
			{Instruction: instructions[2].Instruction + updateSuffix},
			{Instruction: instructions[3].Instruction + updateSuffix},
		}
		roles = []string{store.TaskRolePlanner, store.TaskRolePlanReviewer, store.TaskRoleWorker, store.TaskRoleWorker, store.TaskRoleIntegrator, store.TaskRoleReviewer}
		slots = []string{workerA, workerB, workerA, workerB, workerA, workerB}
		deps = [][]int{nil, {0}, {1}, {2}, {2, 3}, {4}}
		names = []string{"plan", "plan-review", "candidate-a", "candidate-b", "integrate", "review"}
	}
	out := make([]store.CreateTaskSpec, 0, len(instructions))
	for i, instruction := range instructions {
		raw, _ := json.Marshal(instruction)
		out = append(out, store.CreateTaskSpec{
			Name:          names[i],
			Role:          roles[i],
			WorkerSlot:    slots[i],
			PayloadJSON:   string(raw),
			PayloadDigest: taskPayloadDigest(baseDigest, string(raw)),
			Dependencies:  deps[i],
		})
	}
	return out
}

func (s *Server) durableTaskGraphEnabled(agentID string) bool {
	caps, ok := s.pool.AgentCapabilities(agentID)
	return ok && caps != nil && caps.DurableTaskGraph
}

func (s *Server) createAndDispatchTaskGraph(ctx context.Context, run store.OrchestrationRun, payload protocol.OrchestrationStartPayload) error {
	_, err := s.createAndDispatchTaskGraphAfter(ctx, run, payload, "")
	return err
}

func (s *Server) createAndDispatchTaskGraphAfter(ctx context.Context, run store.OrchestrationRun, payload protocol.OrchestrationStartPayload, previousGraphID string) (bool, error) {
	if payload.Round <= 0 {
		payload.Round = 1
	}
	if payload.MaxRounds <= 0 {
		payload.MaxRounds = payload.MaxTurns
	}
	baseJSON, err := json.Marshal(payload)
	if err != nil {
		return false, err
	}
	baseDigest := taskPayloadDigest(string(baseJSON))
	specs := orchestrationTaskSpecs(baseDigest, payload.WorkerPair, payload.Mode, payload.PlanWorkspace)
	var graph store.OrchestrationTaskGraph
	created := true
	if previousGraphID == "" {
		graph, err = s.store.CreateOrchestrationTaskGraph(ctx, run.ID, string(baseJSON), baseDigest, specs)
	} else {
		graph, created, err = s.store.CreateNextOrchestrationTaskGraph(ctx, run.ID, previousGraphID, string(baseJSON), baseDigest, specs)
	}
	if err != nil {
		return false, err
	}
	if !created {
		return false, nil
	}
	if err := s.dispatchReadyTaskGraph(ctx, run, graph); err != nil {
		// A failed queue write is explicitly rolled back to ready. Keep the run
		// durable and let reconnect dispatch it instead of turning transient
		// transport pressure into a user-visible failed orchestration.
		slog.Warn("[hub] initial durable task dispatch deferred", "run_id", run.ID, "error", err)
	}
	return true, nil
}

func (s *Server) dispatchReadyTaskGraph(ctx context.Context, run store.OrchestrationRun, graph store.OrchestrationTaskGraph) error {
	ready, err := s.store.ReadyTasks(ctx, graph.ID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, candidate := range ready {
		task, attempt, claimed, err := s.store.ClaimReadyTask(ctx, candidate.ID, "")
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !claimed {
			continue
		}
		if err := s.dispatchTaskAttempt(ctx, run, graph, task, attempt); err != nil {
			if requeueErr := s.store.RequeueUnsentTask(ctx, task.ID, attempt.ID, task.PayloadDigest, err.Error()); requeueErr != nil {
				slog.Error("[hub] requeue unsent orchestration task failed", "run_id", run.ID, "task_id", task.ID, "error", requeueErr)
			}
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

func (s *Server) dispatchReadyTaskGraphsForAgent(ctx context.Context, agentID string) {
	graphs, err := s.store.ReadyTaskGraphsForAgent(ctx, agentID)
	if err != nil {
		slog.Error("[hub] list reconnect-ready task graphs failed", "agent_id", agentID, "error", err)
		return
	}
	for _, graph := range graphs {
		run, err := s.store.OrchestrationRunByIDAnyUser(ctx, graph.RunID)
		if err != nil {
			slog.Error("[hub] load reconnect-ready orchestration failed", "run_id", graph.RunID, "error", err)
			continue
		}
		if err := s.dispatchReadyTaskGraph(ctx, run, graph); err != nil {
			slog.Warn("[hub] reconnect task dispatch deferred", "run_id", graph.RunID, "error", err)
		}
	}
}

func (s *Server) dispatchTaskAttempt(ctx context.Context, run store.OrchestrationRun, graph store.OrchestrationTaskGraph, task store.OrchestrationTask, attempt store.OrchestrationTaskAttempt) error {
	var payload protocol.OrchestrationStartPayload
	if err := json.Unmarshal([]byte(graph.PayloadJSON), &payload); err != nil {
		return fmt.Errorf("decode durable task payload: %w", err)
	}
	var instruction orchestrationTaskInstruction
	if err := json.Unmarshal([]byte(task.PayloadJSON), &instruction); err != nil {
		return fmt.Errorf("decode task instruction: %w", err)
	}
	evidence, err := s.store.TaskDependencyEvidence(ctx, task.ID)
	if err != nil {
		return err
	}
	payload.Prompt = strings.TrimSpace(payload.Prompt) + "\n\nDurable task node:\n" + instruction.Instruction
	if len(evidence) > 0 {
		raw, _ := json.Marshal(evidence)
		payload.Context = strings.TrimSpace(payload.Context + "\n\nDependency evidence (compact JSON):\n" + string(raw))
	}
	// A durable graph node owns its own native CLI session. In particular, a
	// Claude session id is derived from this attempt's execution key, while the
	// run-level ClaudeStarted flag describes a different node (or an earlier
	// non-graph relay). Carrying it here would make the new id look resumable
	// before Claude has ever created its transcript.
	//
	// Node handoffs are carried through compact dependency evidence and the
	// graph context above. Native ids must never cross an attempt boundary.
	payload.CodexThreadID = ""
	payload.CodexThreadIDs = nil
	payload.ClaudeStarted = false
	payload.RunCWD = ""
	payload.Resume = false
	maxRounds := payload.MaxRounds
	payload.MaxTurns = 1
	payload.TaskGraph = &protocol.TaskGraphPayload{ID: graph.ID, Generation: graph.Generation, Round: payload.Round, MaxRounds: maxRounds, ParallelLimit: graph.ParallelLimit, Tasks: []protocol.TaskPayload{{
		ID: task.ID, AttemptID: attempt.ID, Name: task.Name, Role: task.Role, WorkerSlot: task.WorkerSlot, PayloadDigest: task.PayloadDigest, Dependencies: task.Dependencies,
	}}}
	return s.pool.SendToAgent(run.AgentID, protocol.MustEnvelope(protocol.TypeOrchestrationStart, "", payload))
}

func taskTerminalStatus(kind string) string {
	switch kind {
	case "run.end":
		return store.TaskSucceeded
	case "run.error":
		return store.TaskFailed
	case "run.cancelled":
		return store.TaskCanceled
	default:
		return ""
	}
}

func taskEvidence(payload protocol.OrchestrationEventPayload) map[string]any {
	evidence := map[string]any{"kind": payload.Kind, "content": payload.Content, "error": payload.Error, "data": payload.Data}
	if payload.RunStartData != nil && payload.RunStartData.CWD != "" {
		evidence["cwd"] = payload.RunStartData.CWD
	}
	if payload.RunEndData != nil {
		evidence["runEnd"] = payload.RunEndData
	}
	if payload.RunConclusion != nil {
		evidence["conclusion"] = payload.RunConclusion
	}
	return evidence
}

func nextTaskGraphPayload(graph store.OrchestrationTaskGraph, run store.OrchestrationRun, review protocol.OrchestrationEventPayload) (protocol.OrchestrationStartPayload, bool, error) {
	var payload protocol.OrchestrationStartPayload
	if err := json.Unmarshal([]byte(graph.PayloadJSON), &payload); err != nil {
		return payload, false, fmt.Errorf("decode completed durable task payload: %w", err)
	}
	if payload.MaxRounds <= 0 {
		payload.MaxRounds = payload.MaxTurns
	}
	if payload.Round <= 0 {
		payload.Round = 1
	}
	if payload.Round >= payload.MaxRounds {
		return payload, false, nil
	}
	payload.Round++
	payload.Resume = true
	payload.Files = nil
	payload.CodexThreadID = run.CodexThreadID
	payload.CodexThreadIDs = run.CodexThreadIDs
	payload.ClaudeStarted = run.ClaudeStarted
	payload.RunCWD = run.RunCWD
	reviewContext := trimForContext(review.Content, 8*1024)
	if review.RunConclusion != nil {
		if raw, err := json.Marshal(review.RunConclusion); err == nil {
			reviewContext = strings.TrimSpace(reviewContext + "\nConclusion: " + string(raw))
		}
	}
	payload.Context = trimForContext(strings.TrimSpace(payload.Context+"\n\nPrevious round reviewer handoff:\n"+reviewContext), 64*1024)
	return payload, true, nil
}

func (s *Server) advanceCompletedTaskGraph(ctx context.Context, run store.OrchestrationRun, graph store.OrchestrationTaskGraph, review protocol.OrchestrationEventPayload) (bool, error) {
	next, advance, err := nextTaskGraphPayload(graph, run, review)
	if err != nil || !advance {
		return advance, err
	}
	created, err := s.createAndDispatchTaskGraphAfter(ctx, run, next, graph.ID)
	if err != nil {
		return false, err
	}
	if !created {
		return true, nil
	}
	event, err := s.store.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
		RunID: run.ID, Kind: "turn.delta", Source: "bridge", Severity: "info", Role: "summary",
		Content: fmt.Sprintf("Collaboration round %d/%d completed; starting round %d/%d.", next.Round-1, next.MaxRounds, next.Round, next.MaxRounds),
		Data:    map[string]any{"category": "orchestration-round-advance", "round": next.Round, "maxRounds": next.MaxRounds},
	})
	if err != nil {
		return false, err
	}
	s.pool.BroadcastToOrchestrationBrowsers(run.ID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(event)))
	return true, nil
}

func (s *Server) handleTaskGraphEvent(ctx context.Context, payload protocol.OrchestrationEventPayload) (bool, error) {
	if payload.Task == nil {
		return false, nil
	}
	graph, err := s.store.TaskGraphByRun(ctx, payload.RunID)
	if err != nil {
		return true, err
	}
	if graph.ID != payload.Task.GraphID {
		return true, errors.New("stale orchestration task graph event")
	}
	status := ""
	if payload.Kind == "run.start" {
		status = store.TaskRunning
	} else {
		status = taskTerminalStatus(payload.Kind)
	}
	if status == "" {
		return true, nil
	}
	changed, err := s.store.UpdateTaskAttempt(ctx, payload.Task.TaskID, payload.Task.AttemptID, payload.Task.PayloadDigest, status, taskEvidence(payload), payload.Error)
	if err != nil {
		return true, err
	}
	if !changed {
		return true, errors.New("stale or mismatched orchestration task attempt event")
	}
	run, err := s.store.OrchestrationRunByIDAnyUser(ctx, payload.RunID)
	if err != nil {
		return true, err
	}
	if run.DeleteRequested || run.Status == store.OrchestrationCanceling {
		return true, nil
	}
	if status == store.TaskRunning {
		if run.Status == store.OrchestrationQueued {
			if changed, err := s.store.UpdateOrchestrationRunStatusIfAllowed(ctx, payload.RunID, store.OrchestrationRunning, ""); err != nil {
				return true, err
			} else if !changed {
				return true, nil
			}
		}
	}
	if status == store.TaskSucceeded {
		updated, err := s.store.TaskGraphByRun(ctx, payload.RunID)
		if err != nil {
			return true, err
		}
		if updated.Status == store.TaskGraphCompleted {
			run, err := s.store.OrchestrationRunByIDAnyUser(ctx, payload.RunID)
			if err != nil {
				return true, err
			}
			if orchestrationTerminalStatus(run.Status) {
				return true, nil
			}
			advanced, err := s.advanceCompletedTaskGraph(ctx, run, updated, payload)
			if err != nil {
				return true, err
			}
			if advanced {
				return true, nil
			}
			if err := s.store.UpdateOrchestrationRunStatus(ctx, payload.RunID, store.OrchestrationCompleted, ""); err != nil {
				return true, err
			}
			conclusion := payload.RunConclusion
			if conclusion == nil {
				conclusion = &protocol.RunConclusion{Outcome: "satisfied", Summary: payload.Content}
			}
			terminal, err := s.store.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
				RunID: payload.RunID, Kind: "run.end", Source: "bridge", Role: "summary", Content: payload.Content, Status: store.OrchestrationCompleted, RunConclusion: conclusion,
			})
			if err != nil {
				return true, err
			}
			s.pool.BroadcastToOrchestrationBrowsers(payload.RunID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(terminal)))
			event, err := s.store.AddOrchestrationEvent(ctx, store.OrchestrationEvent{
				RunID: payload.RunID, Kind: "run.conclusion", Source: "bridge", Role: "summary", Content: conclusion.Summary, RunConclusion: conclusion,
			})
			if err != nil {
				return true, err
			}
			s.pool.BroadcastToOrchestrationBrowsers(payload.RunID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(event)))
			return true, nil
		}
		run, err := s.store.OrchestrationRunByIDAnyUser(ctx, payload.RunID)
		if err != nil {
			return true, err
		}
		return true, s.dispatchReadyTaskGraph(ctx, run, updated)
	}
	if status == store.TaskFailed || status == store.TaskCanceled {
		run, err := s.store.OrchestrationRunByIDAnyUser(ctx, payload.RunID)
		if err != nil {
			return true, err
		}
		if orchestrationTerminalStatus(run.Status) {
			return true, nil
		}
		runStatus := store.OrchestrationFailed
		if status == store.TaskCanceled {
			runStatus = store.OrchestrationCanceled
		}
		if err := s.store.UpdateOrchestrationRunStatus(ctx, payload.RunID, runStatus, payload.Error); err != nil {
			return true, err
		}
		kind := "run.error"
		if status == store.TaskCanceled {
			kind = "run.cancelled"
		}
		event, err := s.store.AddOrchestrationEvent(ctx, store.OrchestrationEvent{RunID: payload.RunID, Kind: kind, Source: "bridge", Severity: "error", Content: payload.Content, Status: runStatus, Error: payload.Error, RunConclusion: payload.RunConclusion})
		if err != nil {
			return true, err
		}
		s.pool.BroadcastToOrchestrationBrowsers(payload.RunID, protocol.MustEnvelope(protocol.TypeOrchestrationEvent, "", eventToPayload(event)))
		return true, nil
	}
	return true, nil
}
