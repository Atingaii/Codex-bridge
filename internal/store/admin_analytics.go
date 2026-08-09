package store

import (
	"context"
	"database/sql"
)

// AdminUsageSnapshot is a content-free, read-only projection for the Hub's
// administrator dashboard. Raw usage JSON is retained only long enough for the
// Hub to apply the same normalization and pricing rules as other usage views.
type AdminUsageSnapshot struct {
	Users          []AdminUserUsage
	UsageEvents    []AdminUsageEvent
	ActivityEvents []AdminActivityEvent
}

type AdminUserUsage struct {
	UserID            string
	Username          string
	CreatedAt         int64
	LastActiveAt      int64
	AgentIDs          []string
	ChatSessions      int
	OrchestrationRuns int
	RunningRuns       int
}

type AdminUsageEvent struct {
	UserID           string
	OccurredAt       int64
	CLI              string
	Model            string
	InputTokens      int64
	OutputTokens     int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	UsageJSON        string
	Normalized       bool
	Legacy           bool
	CallCount        int
}

type AdminActivityEvent struct {
	UserID     string
	OccurredAt int64
	Kind       string
	Count      int
}

type AdminUserDetailSnapshot struct {
	User          User
	Conversations []AdminConversation
	UsageEvents   []AdminConversationUsageEvent
}

type AdminConversation struct {
	ID            string
	Kind          string
	Title         string
	AgentName     string
	Status        string
	Mode          string
	MaxTurns      int
	ActivityCount int
	CreatedAt     int64
	UpdatedAt     int64
}

type AdminConversationUsageEvent struct {
	ConversationID string
	AdminUsageEvent
}

type AdminConversationContent struct {
	ID     string
	Kind   string
	Title  string
	Prompt string
	Items  []AdminConversationContentItem
}

type AdminConversationContentItem struct {
	Role      string
	Source    string
	Kind      string
	Content   string
	CreatedAt int64
}

func (s *Store) AdminUsageSnapshot(ctx context.Context, cutoff int64, timezoneOffset int) (AdminUsageSnapshot, error) {
	users, err := s.adminUsageUsers(ctx, cutoff)
	if err != nil {
		return AdminUsageSnapshot{}, err
	}
	events, err := s.adminUsageEvents(ctx, cutoff, timezoneOffset)
	if err != nil {
		return AdminUsageSnapshot{}, err
	}
	activity, err := s.adminActivityEvents(ctx, cutoff, timezoneOffset)
	if err != nil {
		return AdminUsageSnapshot{}, err
	}
	return AdminUsageSnapshot{Users: users, UsageEvents: events, ActivityEvents: activity}, nil
}

func (s *Store) AdminUserDetailSnapshot(ctx context.Context, userID string, cutoff int64) (AdminUserDetailSnapshot, error) {
	user, err := s.UserByID(ctx, userID)
	if err != nil {
		return AdminUserDetailSnapshot{}, err
	}
	conversations, err := s.adminUserConversations(ctx, userID, cutoff)
	if err != nil {
		return AdminUserDetailSnapshot{}, err
	}
	events, err := s.adminUserConversationUsage(ctx, userID, cutoff)
	if err != nil {
		return AdminUserDetailSnapshot{}, err
	}
	return AdminUserDetailSnapshot{User: user, Conversations: conversations, UsageEvents: events}, nil
}

// AdminConversationContent returns the narrow, read-only content projection
// exposed by the administrator API. Ownership is checked in the same query
// that loads the conversation, and operational/native metadata is never read.
func (s *Store) AdminConversationContent(ctx context.Context, userID, kind, conversationID string) (AdminConversationContent, error) {
	content := AdminConversationContent{ID: conversationID, Kind: kind}
	switch kind {
	case "chat":
		if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(title,'') FROM sessions WHERE id = ? AND user_id = ?`, conversationID, userID).Scan(&content.Title); err != nil {
			return AdminConversationContent{}, storeError(err)
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT role, content, created_at FROM messages
			WHERE session_id = ? ORDER BY created_at, id`, conversationID)
		if err != nil {
			return AdminConversationContent{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var item AdminConversationContentItem
			if err := rows.Scan(&item.Role, &item.Content, &item.CreatedAt); err != nil {
				return AdminConversationContent{}, err
			}
			item.Kind = "message"
			content.Items = append(content.Items, item)
		}
		return content, rows.Err()
	case "orchestration":
		if err := s.db.QueryRowContext(ctx, `
			SELECT title, prompt FROM orchestration_runs WHERE id = ? AND user_id = ?`, conversationID, userID,
		).Scan(&content.Title, &content.Prompt); err != nil {
			return AdminConversationContent{}, storeError(err)
		}
		rows, err := s.db.QueryContext(ctx, `
			SELECT role, source, kind, GROUP_CONCAT(content, ''), MIN(created_at)
			FROM (
				SELECT id, seq, COALESCE(role,'') AS role, COALESCE(source,'') AS source,
					kind, COALESCE(turn_id,'') AS turn_id, content, created_at
				FROM orchestration_events
				WHERE run_id = ? AND content IS NOT NULL AND content != ''
					AND kind IN ('user.message', 'turn.delta', 'run.error', 'run.conclusion')
				ORDER BY seq
			)
			GROUP BY CASE WHEN kind = 'turn.delta' AND turn_id != ''
				THEN 'turn:' || turn_id || ':' || role || ':' || source ELSE id END
			ORDER BY MIN(seq)`, conversationID)
		if err != nil {
			return AdminConversationContent{}, err
		}
		defer rows.Close()
		for rows.Next() {
			var item AdminConversationContentItem
			if err := rows.Scan(&item.Role, &item.Source, &item.Kind, &item.Content, &item.CreatedAt); err != nil {
				return AdminConversationContent{}, err
			}
			content.Items = append(content.Items, item)
		}
		return content, rows.Err()
	default:
		return AdminConversationContent{}, ErrNotFound
	}
}

func storeError(err error) error {
	if err == sql.ErrNoRows {
		return ErrNotFound
	}
	return err
}

func (s *Store) adminUserConversations(ctx context.Context, userID string, cutoff int64) ([]AdminConversation, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT se.id, 'chat', COALESCE(se.title,''), COALESCE(a.name,''),
			COALESCE((SELECT r.status FROM runs r WHERE r.session_id = se.id ORDER BY r.updated_at DESC LIMIT 1), 'idle'),
			'', 0, (SELECT COUNT(*) FROM messages m WHERE m.session_id = se.id), se.created_at, se.updated_at
		FROM sessions se LEFT JOIN agents a ON a.id = se.agent_id
		WHERE se.user_id = ? AND (? = 0 OR se.updated_at >= ?)
		UNION ALL
		SELECT o.id, 'orchestration', o.title, COALESCE(a.name,''), o.status,
			o.mode, o.max_turns, (SELECT COUNT(*) FROM orchestration_events e WHERE e.run_id = o.id), o.created_at, o.updated_at
		FROM orchestration_runs o LEFT JOIN agents a ON a.id = o.agent_id
		WHERE o.user_id = ? AND (? = 0 OR o.updated_at >= ?)
		ORDER BY 10 DESC, 9 DESC`, userID, cutoff, cutoff, userID, cutoff, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []AdminConversation{}
	for rows.Next() {
		var item AdminConversation
		if err := rows.Scan(&item.ID, &item.Kind, &item.Title, &item.AgentName, &item.Status, &item.Mode, &item.MaxTurns, &item.ActivityCount, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) adminUserConversationUsage(ctx context.Context, userID string, cutoff int64) ([]AdminConversationUsageEvent, error) {
	events := []AdminConversationUsageEvent{}
	chatRows, err := s.db.QueryContext(ctx, `
		SELECT se.id, COALESCE(NULLIF(r.finished_at, 0), r.updated_at), r.usage_json
		FROM runs r JOIN sessions se ON se.id = r.session_id
		WHERE se.user_id = ? AND r.usage_json IS NOT NULL AND r.usage_json != ''
			AND (? = 0 OR COALESCE(NULLIF(r.finished_at, 0), r.updated_at) >= ?)`, userID, cutoff, cutoff)
	if err != nil {
		return nil, err
	}
	for chatRows.Next() {
		event := AdminConversationUsageEvent{AdminUsageEvent: AdminUsageEvent{UserID: userID, CLI: "codex", CallCount: 1}}
		if err := chatRows.Scan(&event.ConversationID, &event.OccurredAt, &event.UsageJSON); err != nil {
			chatRows.Close()
			return nil, err
		}
		events = append(events, event)
	}
	if err := chatRows.Close(); err != nil {
		return nil, err
	}

	ledgerRows, err := s.db.QueryContext(ctx, `
		SELECT o.id, CASE WHEN ue.occurred_at > 0 THEN ue.occurred_at ELSE o.created_at END,
			ue.cli, COALESCE(ue.model,''), ue.input_tokens, ue.output_tokens,
			ue.cache_read_tokens, ue.cache_write_tokens
		FROM orchestration_usage_events ue JOIN orchestration_runs o ON o.id = ue.run_id
		WHERE o.user_id = ? AND (? = 0 OR CASE WHEN ue.occurred_at > 0 THEN ue.occurred_at ELSE o.created_at END >= ?)`, userID, cutoff, cutoff)
	if err != nil {
		return nil, err
	}
	for ledgerRows.Next() {
		event := AdminConversationUsageEvent{AdminUsageEvent: AdminUsageEvent{UserID: userID, Normalized: true, CallCount: 1}}
		if err := ledgerRows.Scan(&event.ConversationID, &event.OccurredAt, &event.CLI, &event.Model, &event.InputTokens, &event.OutputTokens, &event.CacheReadTokens, &event.CacheWriteTokens); err != nil {
			ledgerRows.Close()
			return nil, err
		}
		events = append(events, event)
	}
	if err := ledgerRows.Close(); err != nil {
		return nil, err
	}

	legacyRows, err := s.db.QueryContext(ctx, `
		SELECT o.id, e.created_at, COALESCE(e.cli,''), e.data_json
		FROM orchestration_events e JOIN orchestration_runs o ON o.id = e.run_id
		WHERE o.user_id = ? AND e.kind = 'turn.usage' AND e.data_json IS NOT NULL AND e.data_json != ''
			AND NOT EXISTS (SELECT 1 FROM orchestration_usage_events ue WHERE ue.run_id = e.run_id)
			AND (? = 0 OR e.created_at >= ?)`, userID, cutoff, cutoff)
	if err != nil {
		return nil, err
	}
	for legacyRows.Next() {
		event := AdminConversationUsageEvent{AdminUsageEvent: AdminUsageEvent{UserID: userID, Legacy: true, CallCount: 1}}
		if err := legacyRows.Scan(&event.ConversationID, &event.OccurredAt, &event.CLI, &event.UsageJSON); err != nil {
			legacyRows.Close()
			return nil, err
		}
		events = append(events, event)
	}
	if err := legacyRows.Close(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) adminUsageUsers(ctx context.Context, cutoff int64) ([]AdminUserUsage, error) {
	rows, err := s.db.QueryContext(ctx, `
		WITH
		agent_activity AS (
			SELECT user_id, MAX(last_seen_at) AS last_active_at
			FROM agents WHERE deleted_at IS NULL AND user_id IS NOT NULL GROUP BY user_id
		),
		session_stats AS (
			SELECT user_id, MAX(updated_at) AS last_active_at,
				SUM(CASE WHEN ? = 0 OR created_at >= ? THEN 1 ELSE 0 END) AS conversation_count
			FROM sessions GROUP BY user_id
		),
		orchestration_stats AS (
			SELECT user_id, MAX(updated_at) AS last_active_at,
				SUM(CASE WHEN ? = 0 OR created_at >= ? THEN 1 ELSE 0 END) AS conversation_count,
				SUM(CASE WHEN status IN ('queued','running','canceling') THEN 1 ELSE 0 END) AS running_count
			FROM orchestration_runs GROUP BY user_id
		)
		SELECT u.id, u.username, u.created_at,
			MAX(u.created_at,
				COALESCE(a.last_active_at, 0), COALESCE(se.last_active_at, 0), COALESCE(o.last_active_at, 0)
			) AS last_active_at,
			COALESCE(se.conversation_count, 0), COALESCE(o.conversation_count, 0), COALESCE(o.running_count, 0)
		FROM users u
		LEFT JOIN agent_activity a ON a.user_id = u.id
		LEFT JOIN session_stats se ON se.user_id = u.id
		LEFT JOIN orchestration_stats o ON o.user_id = u.id
		ORDER BY lower(u.username), u.id`, cutoff, cutoff, cutoff, cutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := []AdminUserUsage{}
	index := map[string]int{}
	for rows.Next() {
		var user AdminUserUsage
		if err := rows.Scan(&user.UserID, &user.Username, &user.CreatedAt, &user.LastActiveAt, &user.ChatSessions, &user.OrchestrationRuns, &user.RunningRuns); err != nil {
			return nil, err
		}
		index[user.UserID] = len(users)
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	agents, err := s.db.QueryContext(ctx, `SELECT user_id, id FROM agents WHERE deleted_at IS NULL AND user_id IS NOT NULL ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, err
	}
	defer agents.Close()
	for agents.Next() {
		var userID, agentID string
		if err := agents.Scan(&userID, &agentID); err != nil {
			return nil, err
		}
		if i, ok := index[userID]; ok {
			users[i].AgentIDs = append(users[i].AgentIDs, agentID)
		}
	}
	return users, agents.Err()
}

func (s *Store) adminUsageEvents(ctx context.Context, cutoff int64, timezoneOffset int) ([]AdminUsageEvent, error) {
	offsetSeconds := int64(timezoneOffset) * 60
	events := []AdminUsageEvent{}
	chatRows, err := s.db.QueryContext(ctx, `
		SELECT se.user_id, COALESCE(NULLIF(r.finished_at, 0), r.updated_at), r.usage_json
		FROM runs r JOIN sessions se ON se.id = r.session_id
		WHERE r.usage_json IS NOT NULL AND r.usage_json != ''
			AND (? = 0 OR COALESCE(NULLIF(r.finished_at, 0), r.updated_at) >= ?)`, cutoff, cutoff)
	if err != nil {
		return nil, err
	}
	for chatRows.Next() {
		var event AdminUsageEvent
		if err := chatRows.Scan(&event.UserID, &event.OccurredAt, &event.UsageJSON); err != nil {
			chatRows.Close()
			return nil, err
		}
		event.CLI = "codex"
		event.CallCount = 1
		events = append(events, event)
	}
	if err := chatRows.Close(); err != nil {
		return nil, err
	}

	ledgerRows, err := s.db.QueryContext(ctx, `
		SELECT o.user_id,
			((CASE WHEN ue.occurred_at > 0 THEN ue.occurred_at ELSE o.created_at END - ?) / 86400) * 86400 + ?,
			ue.cli, COALESCE(ue.model,''), SUM(ue.input_tokens), SUM(ue.output_tokens),
			SUM(ue.cache_read_tokens), SUM(ue.cache_write_tokens), COUNT(*)
		FROM orchestration_usage_events ue JOIN orchestration_runs o ON o.id = ue.run_id
		WHERE (? = 0 OR CASE WHEN ue.occurred_at > 0 THEN ue.occurred_at ELSE o.created_at END >= ?)
		GROUP BY o.user_id,
			((CASE WHEN ue.occurred_at > 0 THEN ue.occurred_at ELSE o.created_at END - ?) / 86400),
			ue.cli, COALESCE(ue.model,'')`, offsetSeconds, offsetSeconds, cutoff, cutoff, offsetSeconds)
	if err != nil {
		return nil, err
	}
	for ledgerRows.Next() {
		event := AdminUsageEvent{Normalized: true}
		if err := ledgerRows.Scan(&event.UserID, &event.OccurredAt, &event.CLI, &event.Model, &event.InputTokens, &event.OutputTokens, &event.CacheReadTokens, &event.CacheWriteTokens, &event.CallCount); err != nil {
			ledgerRows.Close()
			return nil, err
		}
		events = append(events, event)
	}
	if err := ledgerRows.Close(); err != nil {
		return nil, err
	}

	legacyRows, err := s.db.QueryContext(ctx, `
		SELECT o.user_id, e.created_at, COALESCE(e.cli,''), e.data_json
		FROM orchestration_events e JOIN orchestration_runs o ON o.id = e.run_id
		WHERE e.kind = 'turn.usage' AND e.data_json IS NOT NULL AND e.data_json != ''
			AND NOT EXISTS (SELECT 1 FROM orchestration_usage_events ue WHERE ue.run_id = e.run_id)
			AND (? = 0 OR e.created_at >= ?)`, cutoff, cutoff)
	if err != nil {
		return nil, err
	}
	for legacyRows.Next() {
		event := AdminUsageEvent{Legacy: true}
		if err := legacyRows.Scan(&event.UserID, &event.OccurredAt, &event.CLI, &event.UsageJSON); err != nil {
			legacyRows.Close()
			return nil, err
		}
		event.CallCount = 1
		events = append(events, event)
	}
	if err := legacyRows.Close(); err != nil {
		return nil, err
	}
	return events, nil
}

func (s *Store) adminActivityEvents(ctx context.Context, cutoff int64, timezoneOffset int) ([]AdminActivityEvent, error) {
	offsetSeconds := int64(timezoneOffset) * 60
	rows, err := s.db.QueryContext(ctx, `
		SELECT se.user_id, ((m.created_at - ?) / 86400) * 86400 + ?, 'chat_message', COUNT(*)
		FROM messages m JOIN sessions se ON se.id = m.session_id
		WHERE ? = 0 OR m.created_at >= ?
		GROUP BY se.user_id, ((m.created_at - ?) / 86400)
		UNION ALL
		SELECT o.user_id, ((o.created_at - ?) / 86400) * 86400 + ?, 'orchestration_run', COUNT(*)
		FROM orchestration_runs o WHERE ? = 0 OR o.created_at >= ?
		GROUP BY o.user_id, ((o.created_at - ?) / 86400)
		UNION ALL
		SELECT o.user_id, ((o.updated_at - ?) / 86400) * 86400 + ?, 'orchestration_activity', COUNT(*)
		FROM orchestration_runs o
		WHERE ? = 0 OR o.updated_at >= ?
		GROUP BY o.user_id, ((o.updated_at - ?) / 86400)`,
		offsetSeconds, offsetSeconds, cutoff, cutoff, offsetSeconds,
		offsetSeconds, offsetSeconds, cutoff, cutoff, offsetSeconds,
		offsetSeconds, offsetSeconds, cutoff, cutoff, offsetSeconds)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []AdminActivityEvent{}
	for rows.Next() {
		var event AdminActivityEvent
		if err := rows.Scan(&event.UserID, &event.OccurredAt, &event.Kind, &event.Count); err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
