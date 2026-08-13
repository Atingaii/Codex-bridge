package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	TaskPending     = "pending"
	TaskReady       = "ready"
	TaskDispatching = "dispatching"
	TaskRunning     = "running"
	TaskUnknown     = "unknown"
	TaskSucceeded   = "succeeded"
	TaskFailed      = "failed"
	TaskBlocked     = "blocked"
	TaskCanceled    = "canceled"

	TaskRolePlanner      = "planner"
	TaskRolePlanReviewer = "plan-reviewer"
	TaskRoleWorker       = "worker"
	TaskRoleIntegrator   = "integrator"
	TaskRoleReviewer     = "reviewer"

	TaskGraphRunning   = "running"
	TaskGraphCompleted = "completed"
	TaskGraphFailed    = "failed"
	TaskGraphBlocked   = "blocked"
	TaskGraphUnknown   = "unknown"
	TaskGraphCanceled  = "canceled"
)

type OrchestrationTaskGraph struct {
	ID            string              `json:"id"`
	RunID         string              `json:"runId"`
	Generation    int                 `json:"generation"`
	Status        string              `json:"status"`
	ParallelLimit int                 `json:"parallelLimit"`
	PayloadJSON   string              `json:"-"`
	PayloadDigest string              `json:"payloadDigest"`
	Tasks         []OrchestrationTask `json:"tasks,omitempty"`
	CreatedAt     int64               `json:"createdAt"`
	UpdatedAt     int64               `json:"updatedAt"`
	FinishedAt    int64               `json:"finishedAt,omitempty"`
}

type OrchestrationTask struct {
	ID               string   `json:"id"`
	GraphID          string   `json:"graphId"`
	Name             string   `json:"name"`
	Role             string   `json:"role"`
	WorkerSlot       string   `json:"workerSlot,omitempty"`
	Status           string   `json:"status"`
	Position         int      `json:"position"`
	PayloadJSON      string   `json:"-"`
	PayloadDigest    string   `json:"payloadDigest"`
	CurrentAttemptID string   `json:"currentAttemptId,omitempty"`
	Dependencies     []string `json:"dependencies,omitempty"`
	Error            string   `json:"error,omitempty"`
	CreatedAt        int64    `json:"createdAt"`
	UpdatedAt        int64    `json:"updatedAt"`
	StartedAt        int64    `json:"startedAt,omitempty"`
	FinishedAt       int64    `json:"finishedAt,omitempty"`
}

type OrchestrationTaskAttempt struct {
	ID               string         `json:"id"`
	TaskID           string         `json:"taskId"`
	AttemptNo        int            `json:"attemptNo"`
	RetryOfAttemptID string         `json:"retryOfAttemptId,omitempty"`
	PayloadDigest    string         `json:"payloadDigest"`
	Status           string         `json:"status"`
	Evidence         map[string]any `json:"evidence,omitempty"`
	Error            string         `json:"error,omitempty"`
	CreatedAt        int64          `json:"createdAt"`
	DispatchedAt     int64          `json:"dispatchedAt"`
	AcknowledgedAt   int64          `json:"acknowledgedAt,omitempty"`
	FinishedAt       int64          `json:"finishedAt,omitempty"`
}

type CreateTaskSpec struct {
	Name          string
	Role          string
	WorkerSlot    string
	PayloadDigest string
	PayloadJSON   string
	Dependencies  []int
}

// CreateOrchestrationTaskGraph persists the fixed topology and its initial
// attempts atomically. Ready worker attempts are dispatching because the graph
// is returned only for inclusion in the immediately following Bridge frame.
func (s *Store) CreateOrchestrationTaskGraph(ctx context.Context, runID, payloadJSON, payloadDigest string, specs []CreateTaskSpec) (OrchestrationTaskGraph, error) {
	graph, _, err := s.createOrchestrationTaskGraph(ctx, runID, "", payloadJSON, payloadDigest, specs)
	return graph, err
}

// CreateNextOrchestrationTaskGraph creates exactly one successor for the
// expected latest graph. Duplicate terminal deliveries become a no-op.
func (s *Store) CreateNextOrchestrationTaskGraph(ctx context.Context, runID, previousGraphID, payloadJSON, payloadDigest string, specs []CreateTaskSpec) (OrchestrationTaskGraph, bool, error) {
	if strings.TrimSpace(previousGraphID) == "" {
		return OrchestrationTaskGraph{}, false, errors.New("previous graph id is required")
	}
	return s.createOrchestrationTaskGraph(ctx, runID, previousGraphID, payloadJSON, payloadDigest, specs)
}

func (s *Store) createOrchestrationTaskGraph(ctx context.Context, runID, previousGraphID, payloadJSON, payloadDigest string, specs []CreateTaskSpec) (OrchestrationTaskGraph, bool, error) {
	if strings.TrimSpace(runID) == "" || strings.TrimSpace(payloadJSON) == "" || strings.TrimSpace(payloadDigest) == "" || len(specs) == 0 {
		return OrchestrationTaskGraph{}, false, errors.New("run id, payload, and task specs are required")
	}
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OrchestrationTaskGraph{}, false, err
	}
	defer tx.Rollback()
	var latestID string
	var latestGeneration int
	err = tx.QueryRowContext(ctx, `SELECT id, generation FROM orchestration_task_graphs WHERE run_id = ? ORDER BY generation DESC LIMIT 1`, runID).Scan(&latestID, &latestGeneration)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return OrchestrationTaskGraph{}, false, err
	}
	if previousGraphID != "" && latestID != previousGraphID {
		return OrchestrationTaskGraph{}, false, nil
	}
	generation := latestGeneration + 1
	graph := OrchestrationTaskGraph{ID: NewID("otg"), RunID: runID, Generation: generation, Status: TaskGraphRunning, ParallelLimit: 2, PayloadJSON: payloadJSON, PayloadDigest: payloadDigest, CreatedAt: now, UpdatedAt: now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO orchestration_task_graphs (id, run_id, generation, status, parallel_limit, payload_json, payload_digest, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, graph.ID, runID, generation, graph.Status, graph.ParallelLimit, graph.PayloadJSON, graph.PayloadDigest, now, now); err != nil {
		return OrchestrationTaskGraph{}, false, err
	}
	graph.Tasks = make([]OrchestrationTask, len(specs))
	for i, spec := range specs {
		if !validTaskRole(spec.Role) || strings.TrimSpace(spec.PayloadDigest) == "" || strings.TrimSpace(spec.PayloadJSON) == "" {
			return OrchestrationTaskGraph{}, false, fmt.Errorf("invalid task spec at position %d", i)
		}
		status := TaskPending
		if len(spec.Dependencies) == 0 {
			status = TaskReady
		}
		task := OrchestrationTask{ID: NewID("otk"), GraphID: graph.ID, Name: strings.TrimSpace(spec.Name), Role: spec.Role, WorkerSlot: spec.WorkerSlot, Status: status, Position: i, PayloadJSON: spec.PayloadJSON, PayloadDigest: spec.PayloadDigest, CreatedAt: now, UpdatedAt: now}
		if task.Name == "" {
			task.Name = fmt.Sprintf("task-%d", i+1)
		}
		graph.Tasks[i] = task
		if _, err := tx.ExecContext(ctx, `INSERT INTO orchestration_tasks (id, graph_id, name, role, worker_slot, status, position, payload_json, payload_digest, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, task.ID, graph.ID, task.Name, task.Role, nullString(task.WorkerSlot), task.Status, task.Position, task.PayloadJSON, task.PayloadDigest, now, now); err != nil {
			return OrchestrationTaskGraph{}, false, err
		}
	}
	for i, spec := range specs {
		for _, dependency := range spec.Dependencies {
			if dependency < 0 || dependency >= i {
				return OrchestrationTaskGraph{}, false, fmt.Errorf("invalid dependency %d for task %d", dependency, i)
			}
			graph.Tasks[i].Dependencies = append(graph.Tasks[i].Dependencies, graph.Tasks[dependency].ID)
			if _, err := tx.ExecContext(ctx, `INSERT INTO orchestration_task_dependencies (task_id, depends_on_task_id) VALUES (?, ?)`, graph.Tasks[i].ID, graph.Tasks[dependency].ID); err != nil {
				return OrchestrationTaskGraph{}, false, err
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return OrchestrationTaskGraph{}, false, err
	}
	return graph, true, nil
}

// ClaimReadyTask is the only ready -> dispatching transition. It checks the
// graph's worker concurrency limit and creates immutable attempt lineage in the
// same SQLite transaction.
func (s *Store) ClaimReadyTask(ctx context.Context, taskID, retryOfAttemptID string) (OrchestrationTask, OrchestrationTaskAttempt, bool, error) {
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, err
	}
	defer tx.Rollback()
	var task OrchestrationTask
	var parallelLimit int
	err = tx.QueryRowContext(ctx, `SELECT t.id, t.graph_id, t.name, t.role, COALESCE(t.worker_slot,''), t.status, t.position, t.payload_json, t.payload_digest, COALESCE(t.current_attempt_id,''), COALESCE(t.error,''), t.created_at, t.updated_at, COALESCE(t.started_at,0), COALESCE(t.finished_at,0), g.parallel_limit FROM orchestration_tasks t JOIN orchestration_task_graphs g ON g.id = t.graph_id JOIN orchestration_runs r ON r.id = g.run_id WHERE t.id = ? AND g.status = 'running' AND r.status IN ('queued','running') AND r.delete_requested = 0`, taskID).Scan(&task.ID, &task.GraphID, &task.Name, &task.Role, &task.WorkerSlot, &task.Status, &task.Position, &task.PayloadJSON, &task.PayloadDigest, &task.CurrentAttemptID, &task.Error, &task.CreatedAt, &task.UpdatedAt, &task.StartedAt, &task.FinishedAt, &parallelLimit)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, nil
		}
		return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, err
	}
	if task.Status != TaskReady {
		return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, nil
	}
	if task.Role == TaskRoleWorker {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM orchestration_tasks WHERE graph_id = ? AND role = 'worker' AND status IN ('dispatching','running')`, task.GraphID).Scan(&active); err != nil {
			return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, err
		}
		if active >= parallelLimit {
			return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, nil
		}
	} else {
		var active int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM orchestration_tasks WHERE graph_id = ? AND status IN ('dispatching','running')`, task.GraphID).Scan(&active); err != nil {
			return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, err
		}
		if active != 0 {
			return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, nil
		}
	}
	var attemptNo int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(attempt_no), 0) + 1 FROM orchestration_task_attempts WHERE task_id = ?`, task.ID).Scan(&attemptNo); err != nil {
		return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, err
	}
	if retryOfAttemptID != "" {
		var retryTaskID string
		if err := tx.QueryRowContext(ctx, `SELECT task_id FROM orchestration_task_attempts WHERE id = ?`, retryOfAttemptID).Scan(&retryTaskID); err != nil || retryTaskID != task.ID {
			return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, errors.New("retry attempt does not belong to task")
		}
	}
	attempt := OrchestrationTaskAttempt{ID: NewID("ota"), TaskID: task.ID, AttemptNo: attemptNo, RetryOfAttemptID: retryOfAttemptID, PayloadDigest: task.PayloadDigest, Status: TaskDispatching, CreatedAt: now, DispatchedAt: now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO orchestration_task_attempts (id, task_id, attempt_no, retry_of_attempt_id, payload_digest, status, created_at, dispatched_at) VALUES (?, ?, ?, ?, ?, 'dispatching', ?, ?)`, attempt.ID, task.ID, attemptNo, nullString(retryOfAttemptID), task.PayloadDigest, now, now); err != nil {
		return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE orchestration_tasks SET status = 'dispatching', current_attempt_id = ?, error = NULL, updated_at = ? WHERE id = ? AND status = 'ready'`, attempt.ID, now, task.ID)
	if err != nil {
		return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, nil
	}
	task.Status = TaskDispatching
	task.CurrentAttemptID = attempt.ID
	task.UpdatedAt = now
	if err := tx.Commit(); err != nil {
		return OrchestrationTask{}, OrchestrationTaskAttempt{}, false, err
	}
	return task, attempt, true, nil
}

func validTaskRole(role string) bool {
	return role == TaskRolePlanner || role == TaskRolePlanReviewer || role == TaskRoleWorker || role == TaskRoleIntegrator || role == TaskRoleReviewer
}

func (s *Store) TaskGraphByRun(ctx context.Context, runID string) (OrchestrationTaskGraph, error) {
	var graph OrchestrationTaskGraph
	if err := s.db.QueryRowContext(ctx, `SELECT id, run_id, generation, status, parallel_limit, payload_json, payload_digest, created_at, updated_at, COALESCE(finished_at,0) FROM orchestration_task_graphs WHERE run_id = ? ORDER BY generation DESC LIMIT 1`, runID).Scan(&graph.ID, &graph.RunID, &graph.Generation, &graph.Status, &graph.ParallelLimit, &graph.PayloadJSON, &graph.PayloadDigest, &graph.CreatedAt, &graph.UpdatedAt, &graph.FinishedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OrchestrationTaskGraph{}, ErrNotFound
		}
		return OrchestrationTaskGraph{}, err
	}
	tasks, err := s.listTasks(ctx, graph.ID)
	if err != nil {
		return OrchestrationTaskGraph{}, err
	}
	graph.Tasks = tasks
	return graph, nil
}

func (s *Store) listTasks(ctx context.Context, graphID string) ([]OrchestrationTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, graph_id, name, role, COALESCE(worker_slot,''), status, position, payload_json, payload_digest, COALESCE(current_attempt_id,''), COALESCE(error,''), created_at, updated_at, COALESCE(started_at,0), COALESCE(finished_at,0) FROM orchestration_tasks WHERE graph_id = ? ORDER BY position`, graphID)
	if err != nil {
		return nil, err
	}
	var tasks []OrchestrationTask
	for rows.Next() {
		var task OrchestrationTask
		if err := rows.Scan(&task.ID, &task.GraphID, &task.Name, &task.Role, &task.WorkerSlot, &task.Status, &task.Position, &task.PayloadJSON, &task.PayloadDigest, &task.CurrentAttemptID, &task.Error, &task.CreatedAt, &task.UpdatedAt, &task.StartedAt, &task.FinishedAt); err != nil {
			rows.Close()
			return nil, err
		}
		tasks = append(tasks, task)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	// Hub SQLite intentionally uses one open connection. Read all task rows and
	// release that connection before querying dependencies, otherwise the nested
	// query waits forever for the connection held by rows.
	for i := range tasks {
		deps, err := s.taskDependencies(ctx, tasks[i].ID)
		if err != nil {
			return nil, err
		}
		tasks[i].Dependencies = deps
	}
	return tasks, nil
}

func (s *Store) taskDependencies(ctx context.Context, taskID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT depends_on_task_id FROM orchestration_task_dependencies WHERE task_id = ? ORDER BY depends_on_task_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// UpdateTaskAttempt applies an idempotent state transition only when task,
// attempt, and payload digest all match the currently claimed attempt.
func (s *Store) UpdateTaskAttempt(ctx context.Context, taskID, attemptID, digest, status string, evidence map[string]any, errText string) (bool, error) {
	if status != TaskRunning && status != TaskSucceeded && status != TaskFailed && status != TaskCanceled {
		return false, errors.New("invalid task attempt transition")
	}
	now := time.Now().Unix()
	evidenceJSON, err := json.Marshal(evidence)
	if err != nil {
		return false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var currentStatus, currentEvidenceJSON string
	if err := tx.QueryRowContext(ctx, `SELECT status, COALESCE(evidence_json,'{}') FROM orchestration_task_attempts WHERE id = ? AND task_id = ? AND payload_digest = ?`, attemptID, taskID, digest).Scan(&currentStatus, &currentEvidenceJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if currentStatus == status {
		return true, tx.Commit()
	}
	if taskAttemptTerminal(currentStatus) || (status == TaskRunning && currentStatus != TaskPending && currentStatus != TaskDispatching && currentStatus != TaskUnknown) {
		return false, nil
	}
	var mergedEvidence map[string]any
	if err := json.Unmarshal([]byte(currentEvidenceJSON), &mergedEvidence); err != nil || mergedEvidence == nil {
		mergedEvidence = map[string]any{}
	}
	for key, value := range evidence {
		mergedEvidence[key] = value
	}
	evidenceJSON, err = json.Marshal(mergedEvidence)
	if err != nil {
		return false, err
	}
	var finished sql.NullInt64
	if taskAttemptTerminal(status) {
		finished = sql.NullInt64{Int64: now, Valid: true}
	}
	res, err := tx.ExecContext(ctx, `UPDATE orchestration_task_attempts SET status = ?, evidence_json = ?, error = ?, dispatched_at = CASE WHEN ? = 'running' THEN CASE WHEN dispatched_at = 0 THEN ? ELSE dispatched_at END ELSE dispatched_at END, acknowledged_at = CASE WHEN ? = 'running' THEN COALESCE(acknowledged_at, ?) ELSE acknowledged_at END, finished_at = COALESCE(?, finished_at) WHERE id = ? AND status = ?`, status, nullString(string(evidenceJSON)), nullString(errText), status, now, status, now, finished, attemptID, currentStatus)
	if err != nil {
		return false, err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return false, nil
	}
	started := sql.NullInt64{}
	if status == TaskRunning {
		started = sql.NullInt64{Int64: now, Valid: true}
	}
	_, err = tx.ExecContext(ctx, `UPDATE orchestration_tasks SET status = ?, error = ?, started_at = COALESCE(?, started_at), finished_at = COALESCE(?, finished_at), updated_at = ? WHERE id = ? AND current_attempt_id = ?`, status, nullString(errText), started, finished, now, taskID, attemptID)
	if err != nil {
		return false, err
	}
	if taskAttemptTerminal(status) {
		if err := propagateTaskGraphTx(ctx, tx, taskID, status, now); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

func taskAttemptTerminal(status string) bool {
	return status == TaskSucceeded || status == TaskFailed || status == TaskCanceled
}

func propagateTaskGraphTx(ctx context.Context, tx *sql.Tx, taskID, terminal string, now int64) error {
	var graphID string
	if err := tx.QueryRowContext(ctx, `SELECT graph_id FROM orchestration_tasks WHERE id = ?`, taskID).Scan(&graphID); err != nil {
		return err
	}
	if terminal != TaskSucceeded {
		reason := "dependency did not succeed"
		if terminal == TaskUnknown {
			reason = "dependency state is unknown"
		}
		if _, err := tx.ExecContext(ctx, `
			WITH RECURSIVE descendants(id) AS (
				SELECT task_id FROM orchestration_task_dependencies WHERE depends_on_task_id = ?
				UNION
				SELECT d.task_id FROM orchestration_task_dependencies d JOIN descendants p ON d.depends_on_task_id = p.id
			)
			UPDATE orchestration_tasks
			SET status = 'blocked', error = ?, updated_at = ?, finished_at = ?
			WHERE graph_id = ? AND status IN ('pending','ready') AND id IN (SELECT id FROM descendants)
		`, taskID, reason, now, now, graphID); err != nil {
			return err
		}
	}
	if terminal == TaskSucceeded {
		if _, err := tx.ExecContext(ctx, `
			UPDATE orchestration_tasks
			SET status = 'ready', updated_at = ?
			WHERE graph_id = ? AND status = 'pending'
				AND NOT EXISTS (
					SELECT 1 FROM orchestration_task_dependencies d
					JOIN orchestration_tasks parent ON parent.id = d.depends_on_task_id
					WHERE d.task_id = orchestration_tasks.id AND parent.status != 'succeeded'
				)
		`, now, graphID); err != nil {
			return err
		}
	}
	return refreshGraphStatusTx(ctx, tx, graphID, now)
}

func (s *Store) ReadyTasks(ctx context.Context, graphID string) ([]OrchestrationTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, graph_id, name, role, COALESCE(worker_slot,''), status, position, payload_json, payload_digest, COALESCE(current_attempt_id,''), COALESCE(error,''), created_at, updated_at, COALESCE(started_at,0), COALESCE(finished_at,0) FROM orchestration_tasks WHERE graph_id = ? AND status = 'ready' ORDER BY position`, graphID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tasks []OrchestrationTask
	for rows.Next() {
		var task OrchestrationTask
		if err := rows.Scan(&task.ID, &task.GraphID, &task.Name, &task.Role, &task.WorkerSlot, &task.Status, &task.Position, &task.PayloadJSON, &task.PayloadDigest, &task.CurrentAttemptID, &task.Error, &task.CreatedAt, &task.UpdatedAt, &task.StartedAt, &task.FinishedAt); err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

func (s *Store) TaskDependencyEvidence(ctx context.Context, taskID string) (map[string]map[string]any, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT parent.id, COALESCE(a.evidence_json,'{}') FROM orchestration_task_dependencies d JOIN orchestration_tasks parent ON parent.id = d.depends_on_task_id LEFT JOIN orchestration_task_attempts a ON a.id = parent.current_attempt_id WHERE d.task_id = ? ORDER BY parent.position`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]map[string]any{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			return nil, err
		}
		var evidence map[string]any
		if err := json.Unmarshal([]byte(raw), &evidence); err != nil {
			evidence = map[string]any{}
		}
		out[id] = evidence
	}
	return out, rows.Err()
}

func (s *Store) MarkTaskDispatchFailed(ctx context.Context, taskID, attemptID, digest, reason string) error {
	_, err := s.UpdateTaskAttempt(ctx, taskID, attemptID, digest, TaskFailed, nil, reason)
	return err
}

func (s *Store) RequeueUnsentTask(ctx context.Context, taskID, attemptID, digest, reason string) error {
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE orchestration_task_attempts SET status = 'failed', error = ?, finished_at = ? WHERE id = ? AND task_id = ? AND payload_digest = ? AND status = 'dispatching'`, nullString(reason), now, attemptID, taskID, digest)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return ErrConflict
	}
	if _, err := tx.ExecContext(ctx, `UPDATE orchestration_tasks SET status = 'ready', current_attempt_id = NULL, error = ?, updated_at = ? WHERE id = ? AND current_attempt_id = ? AND status = 'dispatching'`, nullString(reason), now, taskID, attemptID); err != nil {
		return err
	}
	return tx.Commit()
}

func refreshGraphStatusTx(ctx context.Context, tx *sql.Tx, graphID string, now int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT role, status FROM orchestration_tasks WHERE graph_id = ?`, graphID)
	if err != nil {
		return err
	}
	defer rows.Close()
	status := TaskGraphRunning
	reviewerSucceeded := false
	allTerminal := true
	for rows.Next() {
		var role, taskStatus string
		if err := rows.Scan(&role, &taskStatus); err != nil {
			return err
		}
		if role == TaskRoleReviewer && taskStatus == TaskSucceeded {
			reviewerSucceeded = true
		}
		if taskStatus == TaskUnknown {
			status = TaskGraphUnknown
		}
		if taskStatus == TaskFailed && status != TaskGraphUnknown {
			status = TaskGraphFailed
		}
		if taskStatus == TaskCanceled && status == TaskGraphRunning {
			status = TaskGraphCanceled
		}
		if taskStatus == TaskBlocked && status == TaskGraphRunning {
			status = TaskGraphBlocked
		}
		if taskStatus == TaskPending || taskStatus == TaskReady || taskStatus == TaskDispatching || taskStatus == TaskRunning {
			allTerminal = false
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if reviewerSucceeded {
		status = TaskGraphCompleted
		allTerminal = true
	}
	var finished sql.NullInt64
	if allTerminal && status != TaskGraphRunning {
		finished = sql.NullInt64{Int64: now, Valid: true}
	}
	_, err = tx.ExecContext(ctx, `UPDATE orchestration_task_graphs SET status = ?, updated_at = ?, finished_at = COALESCE(?, finished_at) WHERE id = ?`, status, now, finished, graphID)
	return err
}

// RecoverTaskGraphs marks delivery-ambiguous work unknown and blocks its
// dependants. It deliberately does not create a retry attempt.
func (s *Store) RecoverTaskGraphs(ctx context.Context) (int64, error) {
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE orchestration_task_attempts SET status = 'unknown', error = 'hub restarted before terminal evidence was recorded' WHERE status IN ('dispatching','running')`)
	if err != nil {
		return 0, err
	}
	count, _ := res.RowsAffected()
	if _, err := tx.ExecContext(ctx, `UPDATE orchestration_tasks SET status = 'unknown', error = 'hub restarted before terminal evidence was recorded', updated_at = ? WHERE status IN ('dispatching','running')`, now); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `
		WITH RECURSIVE descendants(id) AS (
			SELECT d.task_id FROM orchestration_task_dependencies d JOIN orchestration_tasks p ON p.id = d.depends_on_task_id WHERE p.status = 'unknown'
			UNION
			SELECT d.task_id FROM orchestration_task_dependencies d JOIN descendants p ON d.depends_on_task_id = p.id
		)
		UPDATE orchestration_tasks SET status = 'blocked', error = 'dependency state is unknown', updated_at = ?, finished_at = ?
		WHERE status IN ('pending','ready') AND id IN (SELECT id FROM descendants)
	`, now, now); err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE orchestration_task_graphs SET status = 'unknown', updated_at = ? WHERE status = 'running' AND id IN (SELECT graph_id FROM orchestration_tasks WHERE status = 'unknown')`, now); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Store) HasActiveTaskGraph(ctx context.Context, runID string) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orchestration_task_graphs WHERE run_id = ? AND status = 'running'`, runID).Scan(&count)
	return count > 0, err
}

func (s *Store) ReadyTaskGraphsForAgent(ctx context.Context, agentID string) ([]OrchestrationTaskGraph, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.run_id, g.generation, g.status, g.parallel_limit, g.payload_json, g.payload_digest, g.created_at, g.updated_at, COALESCE(g.finished_at,0)
		FROM orchestration_task_graphs g
		JOIN orchestration_runs r ON r.id = g.run_id
		WHERE r.agent_id = ? AND r.status IN ('queued','running') AND g.status = 'running'
			AND EXISTS (SELECT 1 FROM orchestration_tasks t WHERE t.graph_id = g.id AND t.status = 'ready')
		ORDER BY g.created_at, g.generation
	`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var graphs []OrchestrationTaskGraph
	for rows.Next() {
		var graph OrchestrationTaskGraph
		if err := rows.Scan(&graph.ID, &graph.RunID, &graph.Generation, &graph.Status, &graph.ParallelLimit, &graph.PayloadJSON, &graph.PayloadDigest, &graph.CreatedAt, &graph.UpdatedAt, &graph.FinishedAt); err != nil {
			return nil, err
		}
		graphs = append(graphs, graph)
	}
	return graphs, rows.Err()
}
