package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/tencent/codex-bridge/internal/protocol"
)

type OrchestrationUsageSync struct {
	RunID      string `json:"runId"`
	CLI        string `json:"cli"`
	WorkerSlot string `json:"workerSlot,omitempty"`
	SessionID  string `json:"sessionId"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	EventCount int    `json:"eventCount"`
	ScannedAt  int64  `json:"scannedAt"`
}

func (s *Store) ListAllOrchestrationRuns(ctx context.Context, userID string) ([]OrchestrationRun, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, agent_id, title, mode, COALESCE(worker_pair,'claude-codex'), COALESCE(first_cli,''), COALESCE(profile,'default'), prompt, COALESCE(cwd,''),
			COALESCE(run_cwd,''), COALESCE(codex_thread_id,''), COALESCE(codex_thread_ids_json,'{}'), COALESCE(claude_started,0),
			COALESCE(native_context_compaction,'off'), max_turns, status, COALESCE(error,''), COALESCE(files_json,'[]'), created_at, updated_at, COALESCE(finished_at,0), COALESCE(delete_requested,0)
		FROM orchestration_runs
		WHERE user_id = ? AND delete_requested = 0
		ORDER BY updated_at DESC, created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []OrchestrationRun
	for rows.Next() {
		run, err := scanOrchestrationRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *Store) ReplaceOrchestrationUsage(ctx context.Context, result protocol.OrchestrationUsageSyncResult) error {
	if strings.TrimSpace(result.RunID) == "" {
		return errors.New("run id is required")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := replaceUsageSessions(ctx, tx, result); err != nil {
		return err
	}
	return tx.Commit()
}

func replaceUsageSessions(ctx context.Context, tx *sql.Tx, result protocol.OrchestrationUsageSyncResult) error {
	for _, session := range result.Sessions {
		if session.Status == "complete" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM orchestration_usage_events WHERE run_id = ? AND cli = ? AND worker_slot = ? AND session_id = ?`, result.RunID, session.CLI, session.WorkerSlot, session.SessionID); err != nil {
				return err
			}
			for _, event := range result.Events {
				if event.CLI != session.CLI || event.WorkerSlot != session.WorkerSlot || event.SessionID != session.SessionID {
					continue
				}
				if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO orchestration_usage_events
					(run_id, cli, worker_slot, session_id, event_id, occurred_at, provider, model, input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, reasoning_tokens)
					VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, result.RunID, event.CLI, event.WorkerSlot, event.SessionID, event.EventID, event.OccurredAt, nullString(event.Provider), nullString(event.Model), event.InputTokens, event.CacheReadTokens, event.CacheWriteTokens, event.OutputTokens, event.ReasoningTokens); err != nil {
					return err
				}
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO orchestration_usage_syncs
			(run_id, cli, worker_slot, session_id, status, error, event_count, scanned_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(run_id, cli, worker_slot, session_id) DO UPDATE SET status=excluded.status, error=excluded.error, event_count=excluded.event_count, scanned_at=excluded.scanned_at`, result.RunID, session.CLI, session.WorkerSlot, session.SessionID, session.Status, nullString(session.Error), session.EventCount, result.ScannedAt); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) ListOrchestrationUsageEvents(ctx context.Context, runID string) ([]protocol.OrchestrationUsageEvent, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT event_id, cli, worker_slot, session_id, occurred_at, COALESCE(provider,''), COALESCE(model,''), input_tokens, cache_read_tokens, cache_write_tokens, output_tokens, reasoning_tokens FROM orchestration_usage_events WHERE run_id = ? ORDER BY occurred_at, cli, worker_slot, event_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []protocol.OrchestrationUsageEvent
	for rows.Next() {
		var event protocol.OrchestrationUsageEvent
		if err := rows.Scan(&event.EventID, &event.CLI, &event.WorkerSlot, &event.SessionID, &event.OccurredAt, &event.Provider, &event.Model, &event.InputTokens, &event.CacheReadTokens, &event.CacheWriteTokens, &event.OutputTokens, &event.ReasoningTokens); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (s *Store) ListOrchestrationUsageSyncs(ctx context.Context, runID string) ([]OrchestrationUsageSync, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT run_id, cli, worker_slot, session_id, status, COALESCE(error,''), event_count, scanned_at FROM orchestration_usage_syncs WHERE run_id = ? ORDER BY cli, worker_slot, session_id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var syncs []OrchestrationUsageSync
	for rows.Next() {
		var item OrchestrationUsageSync
		if err := rows.Scan(&item.RunID, &item.CLI, &item.WorkerSlot, &item.SessionID, &item.Status, &item.Error, &item.EventCount, &item.ScannedAt); err != nil {
			return nil, err
		}
		syncs = append(syncs, item)
	}
	return syncs, rows.Err()
}

// ListTerminalOrchestrationRunsByAgent is used when a Bridge reconnects so
// completed runs from before a Hub restart can be backfilled without a user
// opening each statistics page.
func (s *Store) ListTerminalOrchestrationRunsByAgent(ctx context.Context, agentID string, limit int) ([]OrchestrationRun, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, agent_id, title, mode, COALESCE(worker_pair,'claude-codex'), COALESCE(first_cli,''), COALESCE(profile,'default'), prompt, COALESCE(cwd,''),
			COALESCE(run_cwd,''), COALESCE(codex_thread_id,''), COALESCE(codex_thread_ids_json,'{}'), COALESCE(claude_started,0),
			COALESCE(native_context_compaction,'off'), max_turns, status, COALESCE(error,''), COALESCE(files_json,'[]'), created_at, updated_at, COALESCE(finished_at,0), COALESCE(delete_requested,0)
		FROM orchestration_runs
		WHERE agent_id = ? AND status IN ('completed','failed','canceled') AND delete_requested = 0
		ORDER BY updated_at DESC LIMIT ?`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var runs []OrchestrationRun
	for rows.Next() {
		run, err := scanOrchestrationRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}
