package hub

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
	"github.com/tencent/codex-bridge/internal/store"
)

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

func orchestrationTaskSpecs(baseDigest, workerPair, mode string) []store.CreateTaskSpec {
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
	out := make([]store.CreateTaskSpec, 0, len(instructions))
	for i, instruction := range instructions {
		raw, _ := json.Marshal(instruction)
		out = append(out, store.CreateTaskSpec{
			Name:          []string{"candidate-a", "candidate-b", "integrate", "review"}[i],
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
	specs := orchestrationTaskSpecs(baseDigest, payload.WorkerPair, payload.Mode)
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
	payload.CodexThreadID = run.CodexThreadID
	payload.CodexThreadIDs = run.CodexThreadIDs
	payload.ClaudeStarted = run.ClaudeStarted
	payload.RunCWD = run.RunCWD
	payload.Resume = payload.Resume || run.CodexThreadID != "" || len(run.CodexThreadIDs) > 0 || run.ClaudeStarted
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
	if status == store.TaskRunning {
		run, err := s.store.OrchestrationRunByIDAnyUser(ctx, payload.RunID)
		if err != nil {
			return true, err
		}
		if run.Status == store.OrchestrationQueued {
			if err := s.store.UpdateOrchestrationRunStatus(ctx, payload.RunID, store.OrchestrationRunning, ""); err != nil {
				return true, err
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
