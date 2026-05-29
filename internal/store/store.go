package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tencent/codex-bridge/internal/protocol"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound      = errors.New("not found")
	ErrUnauthorized  = errors.New("unauthorized")
	ErrTokenExpired  = errors.New("enroll token expired")
	ErrTokenConsumed = errors.New("enroll token consumed")
	ErrConflict      = errors.New("conflict")
)

const passwordHashCost = 12

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("sqlite path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir db dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA busy_timeout=5000; PRAGMA cache_size=-2000; PRAGMA foreign_keys=ON;`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlite pragmas: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			username TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS agents (
			id TEXT PRIMARY KEY,
			user_id TEXT,
			name TEXT NOT NULL,
			machine_id TEXT UNIQUE NOT NULL,
			hostname TEXT,
			instance TEXT,
			working_dirs_json TEXT,
			deleted_at INTEGER,
			last_seen_at INTEGER NOT NULL,
			FOREIGN KEY (user_id) REFERENCES users(id)
		);`,
		`ALTER TABLE agents ADD COLUMN user_id TEXT;`,
		`ALTER TABLE agents ADD COLUMN instance TEXT;`,
		`ALTER TABLE agents ADD COLUMN working_dirs_json TEXT;`,
		`ALTER TABLE agents ADD COLUMN deleted_at INTEGER;`,
		`CREATE INDEX IF NOT EXISTS idx_agents_user_seen ON agents(user_id, last_seen_at DESC);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL,
			user_id TEXT NOT NULL,
			title TEXT,
			remote_thread_id TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (agent_id) REFERENCES agents(id),
			FOREIGN KEY (user_id) REFERENCES users(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_user_updated ON sessions(user_id, updated_at DESC);`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			role TEXT NOT NULL CHECK(role IN ('user','assistant','system')),
			content TEXT NOT NULL,
			usage_json TEXT,
			created_at INTEGER NOT NULL,
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_created ON messages(session_id, created_at ASC);`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL,
			prompt_id TEXT NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('queued','running','succeeded','failed','canceled')),
			error TEXT,
			usage_json TEXT,
			started_at INTEGER,
			finished_at INTEGER,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			UNIQUE(session_id, prompt_id),
			FOREIGN KEY (session_id) REFERENCES sessions(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_runs_session_updated ON runs(session_id, updated_at DESC);`,
		`CREATE INDEX IF NOT EXISTS idx_runs_session_status ON runs(session_id, status);`,
		`CREATE TABLE IF NOT EXISTS enroll_tokens (
			token TEXT PRIMARY KEY,
			user_id TEXT,
			label TEXT,
			used_by_machine TEXT,
			expires_at INTEGER,
			created_at INTEGER,
			FOREIGN KEY (user_id) REFERENCES users(id)
		);`,
		`ALTER TABLE enroll_tokens ADD COLUMN user_id TEXT;`,
		`ALTER TABLE enroll_tokens ADD COLUMN label TEXT;`,
		`ALTER TABLE enroll_tokens ADD COLUMN created_at INTEGER;`,
		`CREATE TABLE IF NOT EXISTS orchestration_runs (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			title TEXT NOT NULL,
			mode TEXT NOT NULL CHECK(mode IN ('collaboration','debate')),
			first_cli TEXT CHECK(first_cli IN ('claude','codex')),
			profile TEXT NOT NULL DEFAULT 'default',
			prompt TEXT NOT NULL,
			cwd TEXT,
			run_cwd TEXT DEFAULT '',
			codex_thread_id TEXT DEFAULT '',
			claude_started INTEGER NOT NULL DEFAULT 0,
			max_turns INTEGER NOT NULL,
			status TEXT NOT NULL CHECK(status IN ('queued','running','completed','failed','canceled','canceling')),
			error TEXT,
			files_json TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			finished_at INTEGER,
			FOREIGN KEY (user_id) REFERENCES users(id),
			FOREIGN KEY (agent_id) REFERENCES agents(id)
		);`,
		`ALTER TABLE orchestration_runs ADD COLUMN first_cli TEXT CHECK(first_cli IN ('claude','codex'));`,
		`ALTER TABLE orchestration_runs ADD COLUMN profile TEXT NOT NULL DEFAULT 'default';`,
		`ALTER TABLE orchestration_runs ADD COLUMN run_cwd TEXT DEFAULT '';`,
		`ALTER TABLE orchestration_runs ADD COLUMN codex_thread_id TEXT DEFAULT '';`,
		`ALTER TABLE orchestration_runs ADD COLUMN claude_started INTEGER NOT NULL DEFAULT 0;`,
		`CREATE INDEX IF NOT EXISTS idx_orchestration_runs_user_updated ON orchestration_runs(user_id, updated_at DESC);`,
		`CREATE TABLE IF NOT EXISTS orchestration_events (
			id TEXT PRIMARY KEY,
			run_id TEXT NOT NULL,
			seq INTEGER NOT NULL,
			kind TEXT NOT NULL,
			source TEXT,
			severity TEXT,
			role TEXT,
			cli TEXT,
			turn_id TEXT,
			content TEXT,
			status TEXT,
			error TEXT,
			data_json TEXT,
			created_at INTEGER NOT NULL,
			UNIQUE(run_id, seq),
			FOREIGN KEY (run_id) REFERENCES orchestration_runs(id) ON DELETE CASCADE
		);`,
		`ALTER TABLE orchestration_events ADD COLUMN source TEXT;`,
		`ALTER TABLE orchestration_events ADD COLUMN severity TEXT;`,
		`CREATE INDEX IF NOT EXISTS idx_orchestration_events_run_seq ON orchestration_events(run_id, seq);`,
		`CREATE TABLE IF NOT EXISTS conversation_shares (
			id TEXT PRIMARY KEY,
			user_id TEXT NOT NULL,
			kind TEXT NOT NULL CHECK(kind IN ('chat','orchestration')),
			target_id TEXT NOT NULL,
			title TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			revoked_at INTEGER,
			FOREIGN KEY (user_id) REFERENCES users(id)
		);`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_shares_target ON conversation_shares(user_id, kind, target_id, revoked_at);`,
		`CREATE INDEX IF NOT EXISTS idx_conversation_shares_active ON conversation_shares(id, revoked_at);`,
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, stmt := range stmts {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			if isDuplicateColumn(err) {
				continue
			}
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) MarkUnfinishedRunsFailed(ctx context.Context, reason string) (int64, error) {
	if reason == "" {
		reason = "hub restarted while run was active"
	}
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `
		UPDATE runs
		SET status = ?, error = ?, finished_at = ?, updated_at = ?
		WHERE status IN ('queued','running')
	`, RunFailed, reason, now, now)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) MarkActiveRunsForAgentFailed(ctx context.Context, agentID, reason string) (int64, error) {
	if agentID == "" {
		return 0, errors.New("agent id is required")
	}
	if reason == "" {
		reason = "bridge disconnected while run was active"
	}
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `
		UPDATE runs
		SET status = ?, error = ?, finished_at = ?, updated_at = ?
		WHERE status IN ('queued','running')
			AND session_id IN (SELECT id FROM sessions WHERE agent_id = ?)
	`, RunFailed, reason, now, now, agentID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func (s *Store) MarkActiveOrchestrationRunsForAgentFailed(ctx context.Context, agentID, reason string) ([]OrchestrationRun, error) {
	if agentID == "" {
		return nil, errors.New("agent id is required")
	}
	if reason == "" {
		reason = "bridge disconnected while orchestration run was active"
	}
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.QueryContext(ctx, `
		SELECT id, user_id, agent_id, title, mode, COALESCE(first_cli,''), COALESCE(profile,'default'), prompt, COALESCE(cwd,''),
			COALESCE(run_cwd,''), COALESCE(codex_thread_id,''), COALESCE(claude_started,0), max_turns, status,
			COALESCE(error,''), COALESCE(files_json,'[]'), created_at, updated_at, COALESCE(finished_at,0)
		FROM orchestration_runs
		WHERE agent_id = ? AND status IN (?, ?)
		ORDER BY updated_at ASC, created_at ASC
	`, agentID, OrchestrationQueued, OrchestrationRunning)
	if err != nil {
		return nil, err
	}
	var runs []OrchestrationRun
	for rows.Next() {
		run, err := scanOrchestrationRun(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		runs = append(runs, run)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range runs {
		runs[i].Status = OrchestrationFailed
		runs[i].Error = reason
		runs[i].UpdatedAt = now
		runs[i].FinishedAt = now
		if _, err := tx.ExecContext(ctx, `
			UPDATE orchestration_runs
			SET status = ?, error = ?, finished_at = ?, updated_at = ?
			WHERE id = ? AND status IN (?, ?)
		`, OrchestrationFailed, reason, now, now, runs[i].ID, OrchestrationQueued, OrchestrationRunning); err != nil {
			return nil, err
		}
		eventContent := orchestrationDisconnectRunErrorContent(tx, runs[i].ID, reason)
		event := OrchestrationEvent{
			ID:       NewID("evt"),
			RunID:    runs[i].ID,
			Kind:     "run.error",
			Source:   "bridge",
			Severity: "error",
			Content:  eventContent,
			Status:   OrchestrationFailed,
			Error:    reason,
			RunConclusion: &protocol.RunConclusion{
				Outcome:          "errored",
				Summary:          eventContent,
				UnmetObligations: []string{"Bridge disconnected before the orchestration could finish."},
			},
			CreatedAt: now,
		}
		dataJSON, err := orchestrationEventDataJSON(event)
		if err != nil {
			return nil, err
		}
		row := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM orchestration_events WHERE run_id = ?`, event.RunID)
		if err := row.Scan(&event.Seq); err != nil {
			return nil, err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO orchestration_events
				(id, run_id, seq, kind, source, severity, role, cli, turn_id, content, status, error, data_json, created_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, event.ID, event.RunID, event.Seq, event.Kind, nullString(event.Source), nullString(event.Severity), nullString(event.Role), nullString(event.CLI),
			nullString(event.TurnID), nullString(event.Content), nullString(event.Status), nullString(event.Error),
			nullString(dataJSON), now); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return runs, nil
}

func orchestrationDisconnectRunErrorContent(tx *sql.Tx, runID, reason string) string {
	lines := []string{
		"运行中断：Bridge 连接在任务仍在运行时断开。",
		"原因：" + reason,
		"这表示远端 CLI/Bridge 链路中断，不是证明任务已经通过或失败的验收结论。",
	}
	if summaries := recentOrchestrationProgressSummaries(tx, runID, 4); len(summaries) > 0 {
		lines = append(lines, "", "断开前最近进展：")
		lines = append(lines, summaries...)
	}
	lines = append(lines, "", "后续动作：待该机器重新在线后，使用同一上传样例重新发起或继续该任务；前端应以这条链路诊断作为当前测试结果。")
	return strings.Join(lines, "\n")
}

func recentOrchestrationProgressSummaries(tx *sql.Tx, runID string, limit int) []string {
	if limit <= 0 {
		return nil
	}
	rows, err := tx.Query(`
		SELECT kind, COALESCE(role,''), COALESCE(cli,''), COALESCE(content,''), COALESCE(status,''), COALESCE(error,'')
		FROM orchestration_events
		WHERE run_id = ?
		ORDER BY seq DESC
		LIMIT ?
	`, runID, limit*3)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var kind, role, cli, content, status, errText string
		if err := rows.Scan(&kind, &role, &cli, &content, &status, &errText); err != nil {
			return out
		}
		summary := compactOrchestrationProgressLine(kind, role, cli, content, status, errText)
		if summary == "" {
			continue
		}
		out = append(out, "- "+summary)
		if len(out) >= limit {
			break
		}
	}
	return out
}

func compactOrchestrationProgressLine(kind, role, cli, content, status, errText string) string {
	parts := []string{strings.TrimSpace(kind)}
	actor := strings.TrimSpace(strings.Join(nonEmptyStrings(role, cli), "/"))
	if actor != "" {
		parts = append(parts, actor)
	}
	if status = strings.TrimSpace(status); status != "" {
		parts = append(parts, status)
	}
	if content = strings.TrimSpace(content); content != "" {
		parts = append(parts, trimOneLine(content, 220))
	} else if errText = strings.TrimSpace(errText); errText != "" {
		parts = append(parts, trimOneLine(errText, 220))
	}
	if len(parts) == 1 {
		return ""
	}
	return strings.Join(parts, " | ")
}

func nonEmptyStrings(values ...string) []string {
	var out []string
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func trimOneLine(value string, max int) string {
	value = strings.Join(strings.Fields(value), " ")
	if max <= 0 || len([]rune(value)) <= max {
		return value
	}
	runes := []rune(value)
	if max <= 1 {
		return string(runes[:max])
	}
	return string(runes[:max-1]) + "…"
}

type User struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	CreatedAt int64  `json:"createdAt"`
	IsAdmin   bool   `json:"isAdmin,omitempty"`
}

func (s *Store) UpsertUser(ctx context.Context, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return User{}, errors.New("username and password are required")
	}
	if isQuotedEmptyPassword(password) {
		return User{}, errors.New("password is invalid")
	}
	now := time.Now().Unix()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	if err != nil {
		return User{}, err
	}
	user := User{ID: NewID("usr"), Username: username, CreatedAt: now}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, created_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(username) DO UPDATE SET password_hash = excluded.password_hash
	`, user.ID, username, string(hash), now)
	if err != nil {
		return User{}, err
	}
	return s.UserByUsername(ctx, username)
}

func (s *Store) CreateUser(ctx context.Context, username, password string) (User, error) {
	username = strings.TrimSpace(username)
	if username == "" || password == "" {
		return User{}, errors.New("username and password are required")
	}
	if isQuotedEmptyPassword(password) {
		return User{}, errors.New("password is invalid")
	}
	now := time.Now().Unix()
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordHashCost)
	if err != nil {
		return User{}, err
	}
	user := User{ID: NewID("usr"), Username: username, CreatedAt: now}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, created_at)
		VALUES (?, ?, ?, ?)
	`, user.ID, username, string(hash), now)
	if err != nil {
		if isUniqueConstraint(err) {
			return User{}, ErrConflict
		}
		return User{}, err
	}
	return user, nil
}

func isQuotedEmptyPassword(value string) bool {
	trimmed := strings.TrimSpace(value)
	return trimmed == `""` || trimmed == `''`
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, created_at FROM users WHERE username = ?`, username)
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	return u, nil
}

func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, created_at FROM users WHERE id = ?`, id)
	var u User
	if err := row.Scan(&u.ID, &u.Username, &u.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	return u, nil
}

func (s *Store) AuthenticateUser(ctx context.Context, username, password string) (User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, username, password_hash, created_at FROM users WHERE username = ?`, username)
	var u User
	var hash string
	if err := row.Scan(&u.ID, &u.Username, &hash, &u.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrUnauthorized
		}
		return User{}, err
	}
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return User{}, ErrUnauthorized
	}
	return u, nil
}

type Agent struct {
	ID          string   `json:"id"`
	UserID      string   `json:"userId,omitempty"`
	Name        string   `json:"name"`
	MachineID   string   `json:"machineId"`
	Hostname    string   `json:"hostname"`
	Instance    string   `json:"instance,omitempty"`
	WorkingDirs []string `json:"workingDirs,omitempty"`
	DeletedAt   int64    `json:"deletedAt,omitempty"`
	LastSeenAt  int64    `json:"lastSeenAt"`
	Online      bool     `json:"online"`
}

func (s *Store) UpsertAgent(ctx context.Context, name, machineID, hostname, instance string, workingDirs []string) (Agent, error) {
	return s.UpsertAgentForUser(ctx, "", name, machineID, hostname, instance, workingDirs)
}

func (s *Store) UpsertAgentForUser(ctx context.Context, userID, name, machineID, hostname, instance string, workingDirs []string) (Agent, error) {
	if machineID == "" {
		return Agent{}, errors.New("machine id is required")
	}
	if name == "" {
		name = machineID
	}
	workingDirsJSON, err := json.Marshal(cleanWorkingDirs(workingDirs))
	if err != nil {
		return Agent{}, err
	}
	now := time.Now().Unix()
	id := NewID("agt")
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO agents (id, user_id, name, machine_id, hostname, instance, working_dirs_json, last_seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(machine_id) DO UPDATE SET
			user_id = COALESCE(excluded.user_id, agents.user_id),
			name = excluded.name,
			hostname = excluded.hostname,
			instance = excluded.instance,
			working_dirs_json = excluded.working_dirs_json,
			deleted_at = NULL,
			last_seen_at = excluded.last_seen_at
	`, id, nullString(userID), name, machineID, hostname, instance, string(workingDirsJSON), now)
	if err != nil {
		return Agent{}, err
	}
	return s.AgentByMachineID(ctx, machineID)
}

func (s *Store) AgentByMachineID(ctx context.Context, machineID string) (Agent, error) {
	row := s.db.QueryRowContext(ctx, agentSelectSQL()+` WHERE machine_id = ? AND deleted_at IS NULL`, machineID)
	return scanAgent(row)
}

func (s *Store) AgentByID(ctx context.Context, id string) (Agent, error) {
	row := s.db.QueryRowContext(ctx, agentSelectSQL()+` WHERE id = ? AND deleted_at IS NULL`, id)
	return scanAgent(row)
}

func (s *Store) AgentByIDForUser(ctx context.Context, id, userID string, includeUnowned bool) (Agent, error) {
	if id == "" {
		return Agent{}, ErrNotFound
	}
	if userID == "" {
		return s.AgentByID(ctx, id)
	}
	where := ` WHERE id = ? AND user_id = ? AND deleted_at IS NULL`
	args := []any{id, userID}
	if includeUnowned {
		where = ` WHERE id = ? AND (user_id = ? OR user_id IS NULL OR user_id = '') AND deleted_at IS NULL`
	}
	row := s.db.QueryRowContext(ctx, agentSelectSQL()+where, args...)
	return scanAgent(row)
}

func agentSelectSQL() string {
	return `SELECT id, COALESCE(user_id,''), name, machine_id, COALESCE(hostname,''), COALESCE(instance,''), COALESCE(working_dirs_json,'[]'), COALESCE(deleted_at,0), last_seen_at FROM agents`
}

func scanAgent(row interface{ Scan(dest ...any) error }) (Agent, error) {
	var a Agent
	var workingDirsJSON string
	if err := row.Scan(&a.ID, &a.UserID, &a.Name, &a.MachineID, &a.Hostname, &a.Instance, &workingDirsJSON, &a.DeletedAt, &a.LastSeenAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Agent{}, ErrNotFound
		}
		return Agent{}, err
	}
	if strings.TrimSpace(workingDirsJSON) != "" {
		_ = json.Unmarshal([]byte(workingDirsJSON), &a.WorkingDirs)
		a.WorkingDirs = cleanWorkingDirs(a.WorkingDirs)
	}
	return a, nil
}

func (s *Store) TouchAgent(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE agents SET last_seen_at = ? WHERE id = ? AND deleted_at IS NULL`, time.Now().Unix(), id)
	return err
}

func (s *Store) TouchAgentWorkingDirs(ctx context.Context, id string, workingDirs []string) error {
	workingDirsJSON, err := json.Marshal(cleanWorkingDirs(workingDirs))
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		UPDATE agents
		SET last_seen_at = ?, working_dirs_json = ?
		WHERE id = ? AND deleted_at IS NULL
	`, time.Now().Unix(), string(workingDirsJSON), id)
	return err
}

func (s *Store) ListAgents(ctx context.Context) ([]Agent, error) {
	rows, err := s.db.QueryContext(ctx, agentSelectSQL()+` WHERE deleted_at IS NULL ORDER BY last_seen_at DESC`)
	if err != nil {
		return nil, err
	}
	return scanAgents(rows)
}

func (s *Store) ListAgentsForUser(ctx context.Context, userID string, includeUnowned bool) ([]Agent, error) {
	if userID == "" {
		return s.ListAgents(ctx)
	}
	where := ` WHERE user_id = ? AND deleted_at IS NULL`
	if includeUnowned {
		where = ` WHERE (user_id = ? OR user_id IS NULL OR user_id = '') AND deleted_at IS NULL`
	}
	rows, err := s.db.QueryContext(ctx, agentSelectSQL()+where+` ORDER BY last_seen_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return scanAgents(rows)
}

func (s *Store) DeleteAgent(ctx context.Context, id string) error {
	if id == "" {
		return ErrNotFound
	}
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET deleted_at = ?, last_seen_at = ? WHERE id = ? AND deleted_at IS NULL`, now, now, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RevokeEnrollTokensForMachine(ctx context.Context, machineID string) error {
	if strings.TrimSpace(machineID) == "" {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `UPDATE enroll_tokens SET used_by_machine = ? WHERE used_by_machine = ?`, "deleted:"+machineID, machineID)
	return err
}

func scanAgents(rows *sql.Rows) ([]Agent, error) {
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func cleanWorkingDirs(dirs []string) []string {
	if len(dirs) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(dirs))
	out := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" {
			continue
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		out = append(out, dir)
	}
	return out
}

type Session struct {
	ID             string `json:"id"`
	AgentID        string `json:"agentId"`
	UserID         string `json:"userId"`
	Title          string `json:"title"`
	RemoteThreadID string `json:"remoteThreadId,omitempty"`
	CreatedAt      int64  `json:"createdAt"`
	UpdatedAt      int64  `json:"updatedAt"`
}

func (s *Store) CreateSession(ctx context.Context, userID, agentID, title string) (Session, error) {
	now := time.Now().Unix()
	if title == "" {
		title = "New chat"
	}
	sess := Session{ID: NewID("ses"), AgentID: agentID, UserID: userID, Title: title, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO sessions (id, agent_id, user_id, title, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, sess.ID, agentID, userID, title, now, now)
	if err != nil {
		return Session{}, err
	}
	return sess, nil
}

func (s *Store) SessionByID(ctx context.Context, id, userID string) (Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, agent_id, user_id, COALESCE(title,''), COALESCE(remote_thread_id,''), created_at, updated_at
		FROM sessions
		WHERE id = ? AND user_id = ?
	`, id, userID)
	return scanSession(row)
}

func (s *Store) SessionByIDAnyUser(ctx context.Context, id string) (Session, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, agent_id, user_id, COALESCE(title,''), COALESCE(remote_thread_id,''), created_at, updated_at
		FROM sessions
		WHERE id = ?
	`, id)
	return scanSession(row)
}

func (s *Store) ListSessions(ctx context.Context, userID string, limit int) ([]Session, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, agent_id, user_id, COALESCE(title,''), COALESCE(remote_thread_id,''), created_at, updated_at
		FROM sessions
		WHERE user_id = ?
		ORDER BY updated_at DESC, created_at DESC
		LIMIT ?
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		sess, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sess)
	}
	return out, rows.Err()
}

func (s *Store) UpdateSessionTitle(ctx context.Context, sid, userID, title string) (Session, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return Session{}, errors.New("session title is required")
	}
	if runes := []rune(title); len(runes) > 120 {
		title = string(runes[:120])
	}
	if _, err := s.SessionByID(ctx, sid, userID); err != nil {
		return Session{}, err
	}
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `
		UPDATE sessions
		SET title = ?, updated_at = ?
		WHERE id = ? AND user_id = ?
	`, title, now, sid, userID)
	if err != nil {
		return Session{}, err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return s.SessionByID(ctx, sid, userID)
	}
	return s.SessionByID(ctx, sid, userID)
}

func (s *Store) DeleteSession(ctx context.Context, sid, userID string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE id = ? AND user_id = ?`, sid, userID)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrNotFound
	}
	return nil
}

func scanSession(row interface{ Scan(dest ...any) error }) (Session, error) {
	var sess Session
	if err := row.Scan(&sess.ID, &sess.AgentID, &sess.UserID, &sess.Title, &sess.RemoteThreadID, &sess.CreatedAt, &sess.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}
	return sess, nil
}

func (s *Store) UpdateSessionRemoteThread(ctx context.Context, sid, userID, remoteThreadID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET remote_thread_id = ?, updated_at = ? WHERE id = ? AND user_id = ?`, remoteThreadID, time.Now().Unix(), sid, userID)
	return err
}

func (s *Store) UpdateSessionRemoteThreadByID(ctx context.Context, sid, remoteThreadID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET remote_thread_id = ?, updated_at = ? WHERE id = ?`, remoteThreadID, time.Now().Unix(), sid)
	return err
}

func (s *Store) TouchSession(ctx context.Context, sid string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE sessions SET updated_at = ? WHERE id = ?`, time.Now().Unix(), sid)
	return err
}

type Message struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionId"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	UsageJSON string `json:"usageJson,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}

const (
	ShareKindChat          = "chat"
	ShareKindOrchestration = "orchestration"
)

type ConversationShare struct {
	ID        string `json:"id"`
	UserID    string `json:"userId"`
	Kind      string `json:"kind"`
	TargetID  string `json:"targetId"`
	Title     string `json:"title,omitempty"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	RevokedAt int64  `json:"revokedAt,omitempty"`
}

func (s *Store) CreateOrUpdateConversationShare(ctx context.Context, userID, kind, targetID, title string) (ConversationShare, error) {
	userID = strings.TrimSpace(userID)
	targetID = strings.TrimSpace(targetID)
	kind = strings.TrimSpace(kind)
	if userID == "" || targetID == "" {
		return ConversationShare{}, errors.New("user id and target id are required")
	}
	if kind != ShareKindChat && kind != ShareKindOrchestration {
		return ConversationShare{}, errors.New("share kind must be chat or orchestration")
	}
	title = cleanShareTitle(title)
	now := time.Now().Unix()
	share, err := s.activeConversationShareByTarget(ctx, userID, kind, targetID)
	if err == nil {
		_, err = s.db.ExecContext(ctx, `
			UPDATE conversation_shares
			SET title = ?, updated_at = ?
			WHERE id = ? AND revoked_at IS NULL
		`, nullString(title), now, share.ID)
		if err != nil {
			return ConversationShare{}, err
		}
		share.Title = title
		share.UpdatedAt = now
		return share, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return ConversationShare{}, err
	}
	share = ConversationShare{
		ID:        NewToken("shr"),
		UserID:    userID,
		Kind:      kind,
		TargetID:  targetID,
		Title:     title,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO conversation_shares (id, user_id, kind, target_id, title, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, share.ID, userID, kind, targetID, nullString(title), now, now)
	if err != nil {
		return ConversationShare{}, err
	}
	return share, nil
}

func (s *Store) ActiveConversationShareByID(ctx context.Context, id string) (ConversationShare, error) {
	id = CleanToken(id)
	if id == "" {
		return ConversationShare{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, kind, target_id, COALESCE(title,''), created_at, updated_at, COALESCE(revoked_at,0)
		FROM conversation_shares
		WHERE id = ? AND revoked_at IS NULL
	`, id)
	return scanConversationShare(row)
}

func (s *Store) RevokeConversationShare(ctx context.Context, id, userID string) error {
	id = CleanToken(id)
	userID = strings.TrimSpace(userID)
	if id == "" || userID == "" {
		return ErrNotFound
	}
	now := time.Now().Unix()
	res, err := s.db.ExecContext(ctx, `
		UPDATE conversation_shares
		SET revoked_at = ?, updated_at = ?
		WHERE id = ? AND user_id = ? AND revoked_at IS NULL
	`, now, now, id, userID)
	if err != nil {
		return err
	}
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) activeConversationShareByTarget(ctx context.Context, userID, kind, targetID string) (ConversationShare, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, kind, target_id, COALESCE(title,''), created_at, updated_at, COALESCE(revoked_at,0)
		FROM conversation_shares
		WHERE user_id = ? AND kind = ? AND target_id = ? AND revoked_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`, userID, kind, targetID)
	return scanConversationShare(row)
}

func scanConversationShare(row interface{ Scan(dest ...any) error }) (ConversationShare, error) {
	var share ConversationShare
	if err := row.Scan(&share.ID, &share.UserID, &share.Kind, &share.TargetID, &share.Title, &share.CreatedAt, &share.UpdatedAt, &share.RevokedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ConversationShare{}, ErrNotFound
		}
		return ConversationShare{}, err
	}
	return share, nil
}

func cleanShareTitle(title string) string {
	title = strings.TrimSpace(title)
	if runes := []rune(title); len(runes) > 120 {
		return string(runes[:120])
	}
	return title
}

func (s *Store) AddMessage(ctx context.Context, sessionID, role, content, usageJSON string) (Message, error) {
	now := time.Now().Unix()
	msg := Message{ID: NewID("msg"), SessionID: sessionID, Role: role, Content: content, UsageJSON: usageJSON, CreatedAt: now}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO messages (id, session_id, role, content, usage_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, msg.ID, sessionID, role, content, nullString(usageJSON), now)
	if err != nil {
		return Message{}, err
	}
	_ = s.TouchSession(ctx, sessionID)
	return msg, nil
}

func (s *Store) ListMessages(ctx context.Context, sessionID string, limit int) ([]Message, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, role, content, COALESCE(usage_json,''), created_at
		FROM messages
		WHERE session_id = ?
		ORDER BY created_at ASC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var msg Message
		if err := rows.Scan(&msg.ID, &msg.SessionID, &msg.Role, &msg.Content, &msg.UsageJSON, &msg.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, msg)
	}
	return out, rows.Err()
}

const (
	RunQueued    = "queued"
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunCanceled  = "canceled"
)

type Run struct {
	ID         string `json:"id"`
	SessionID  string `json:"sessionId"`
	PromptID   string `json:"promptId"`
	Status     string `json:"status"`
	Error      string `json:"error,omitempty"`
	UsageJSON  string `json:"usageJson,omitempty"`
	StartedAt  int64  `json:"startedAt,omitempty"`
	FinishedAt int64  `json:"finishedAt,omitempty"`
	CreatedAt  int64  `json:"createdAt"`
	UpdatedAt  int64  `json:"updatedAt"`
}

const (
	OrchestrationQueued    = "queued"
	OrchestrationRunning   = "running"
	OrchestrationCompleted = "completed"
	OrchestrationFailed    = "failed"
	OrchestrationCanceled  = "canceled"
	OrchestrationCanceling = "canceling"
)

type OrchestrationFile struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
	Size     int64  `json:"size"`
}

type OrchestrationRun struct {
	ID            string              `json:"id"`
	UserID        string              `json:"userId"`
	AgentID       string              `json:"agentId"`
	Title         string              `json:"title"`
	Mode          string              `json:"mode"`
	FirstCLI      string              `json:"firstCli,omitempty"`
	Profile       string              `json:"profile"`
	Prompt        string              `json:"prompt"`
	CWD           string              `json:"cwd,omitempty"`
	RunCWD        string              `json:"runCwd,omitempty"`
	CodexThreadID string              `json:"codexThreadId,omitempty"`
	ClaudeStarted bool                `json:"claudeStarted,omitempty"`
	MaxTurns      int                 `json:"maxTurns"`
	Status        string              `json:"status"`
	Error         string              `json:"error,omitempty"`
	Files         []OrchestrationFile `json:"files,omitempty"`
	CreatedAt     int64               `json:"createdAt"`
	UpdatedAt     int64               `json:"updatedAt"`
	FinishedAt    int64               `json:"finishedAt,omitempty"`
}

type OrchestrationEvent struct {
	ID             string                   `json:"id"`
	RunID          string                   `json:"runId"`
	Seq            int64                    `json:"seq"`
	Kind           string                   `json:"kind"`
	Source         string                   `json:"source,omitempty"`
	Severity       string                   `json:"severity,omitempty"`
	Role           string                   `json:"role,omitempty"`
	CLI            string                   `json:"cli,omitempty"`
	TurnID         string                   `json:"turnId,omitempty"`
	Content        string                   `json:"content,omitempty"`
	Status         string                   `json:"status,omitempty"`
	Error          string                   `json:"error,omitempty"`
	CommandData    *protocol.CommandData    `json:"commandData,omitempty"`
	RunStartData   *protocol.RunStartData   `json:"runStartData,omitempty"`
	TurnStartData  *protocol.TurnStartData  `json:"turnStartData,omitempty"`
	RunEndData     *protocol.RunEndData     `json:"runEndData,omitempty"`
	BridgeNoteData *protocol.BridgeNoteData `json:"bridgeNoteData,omitempty"`
	RunConclusion  *protocol.RunConclusion  `json:"runConclusion,omitempty"`
	Data           map[string]any           `json:"data,omitempty"`
	CreatedAt      int64                    `json:"createdAt"`
}

type CreateOrchestrationRunParams struct {
	UserID   string
	AgentID  string
	Title    string
	Mode     string
	FirstCLI string
	Profile  string
	Prompt   string
	CWD      string
	MaxTurns int
	Files    []OrchestrationFile
}

func (s *Store) CreateRun(ctx context.Context, sessionID, promptID string) (Run, error) {
	if sessionID == "" {
		return Run{}, errors.New("session id is required")
	}
	if promptID == "" {
		promptID = NewID("prm")
	}
	now := time.Now().Unix()
	run := Run{ID: NewID("run"), SessionID: sessionID, PromptID: promptID, Status: RunRunning, StartedAt: now, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO runs (id, session_id, prompt_id, status, started_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, run.ID, sessionID, promptID, run.Status, now, now, now)
	if err != nil {
		if isUniqueConstraint(err) {
			return Run{}, ErrConflict
		}
		return Run{}, err
	}
	_ = s.TouchSession(ctx, sessionID)
	return run, nil
}

func (s *Store) RunByPromptID(ctx context.Context, sessionID, promptID string) (Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, prompt_id, status, COALESCE(error,''), COALESCE(usage_json,''), COALESCE(started_at,0), COALESCE(finished_at,0), created_at, updated_at
		FROM runs
		WHERE session_id = ? AND prompt_id = ?
	`, sessionID, promptID)
	return scanRun(row)
}

func (s *Store) ActiveRunBySession(ctx context.Context, sessionID string) (Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, prompt_id, status, COALESCE(error,''), COALESCE(usage_json,''), COALESCE(started_at,0), COALESCE(finished_at,0), created_at, updated_at
		FROM runs
		WHERE session_id = ? AND status IN ('queued','running')
		ORDER BY updated_at DESC
		LIMIT 1
	`, sessionID)
	return scanRun(row)
}

func (s *Store) RunByID(ctx context.Context, id string) (Run, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, session_id, prompt_id, status, COALESCE(error,''), COALESCE(usage_json,''), COALESCE(started_at,0), COALESCE(finished_at,0), created_at, updated_at
		FROM runs
		WHERE id = ?
	`, id)
	return scanRun(row)
}

func (s *Store) ListRuns(ctx context.Context, sessionID string, limit int) ([]Run, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, session_id, prompt_id, status, COALESCE(error,''), COALESCE(usage_json,''), COALESCE(started_at,0), COALESCE(finished_at,0), created_at, updated_at
		FROM runs
		WHERE session_id = ?
		ORDER BY created_at ASC
		LIMIT ?
	`, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) UpdateRunStatus(ctx context.Context, id, status, errText, usageJSON string) error {
	now := time.Now().Unix()
	var finished sql.NullInt64
	if status == RunSucceeded || status == RunFailed || status == RunCanceled {
		finished = sql.NullInt64{Int64: now, Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE runs
		SET status = ?, error = ?, usage_json = ?, finished_at = COALESCE(?, finished_at), updated_at = ?
		WHERE id = ?
	`, status, nullString(errText), nullString(usageJSON), finished, now, id)
	return err
}

func (s *Store) CreateOrchestrationRun(ctx context.Context, params CreateOrchestrationRunParams) (OrchestrationRun, error) {
	if params.UserID == "" || params.AgentID == "" {
		return OrchestrationRun{}, errors.New("user id and agent id are required")
	}
	if params.Mode != "collaboration" && params.Mode != "debate" {
		return OrchestrationRun{}, errors.New("mode must be collaboration or debate")
	}
	params.FirstCLI = normalizeOrchestrationFirstCLI(params.FirstCLI)
	params.Prompt = strings.TrimSpace(params.Prompt)
	if params.Prompt == "" {
		return OrchestrationRun{}, errors.New("prompt is required")
	}
	params.Title = strings.TrimSpace(params.Title)
	if params.Title == "" {
		params.Title = params.Prompt
	}
	if runes := []rune(params.Title); len(runes) > 80 {
		params.Title = string(runes[:80])
	}
	if params.MaxTurns <= 0 {
		params.MaxTurns = 4
	}
	if params.MaxTurns > 12 {
		params.MaxTurns = 12
	}
	params.Profile = normalizeOrchestrationProfile(params.Profile)
	filesJSON, err := json.Marshal(params.Files)
	if err != nil {
		return OrchestrationRun{}, err
	}
	now := time.Now().Unix()
	run := OrchestrationRun{
		ID:        NewID("orc"),
		UserID:    params.UserID,
		AgentID:   params.AgentID,
		Title:     params.Title,
		Mode:      params.Mode,
		FirstCLI:  params.FirstCLI,
		Profile:   params.Profile,
		Prompt:    params.Prompt,
		CWD:       params.CWD,
		MaxTurns:  params.MaxTurns,
		Status:    OrchestrationQueued,
		Files:     params.Files,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO orchestration_runs
			(id, user_id, agent_id, title, mode, first_cli, profile, prompt, cwd, max_turns, status, files_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.ID, run.UserID, run.AgentID, run.Title, run.Mode, run.FirstCLI, run.Profile, run.Prompt, nullString(run.CWD), run.MaxTurns, run.Status, string(filesJSON), now, now)
	if err != nil {
		return OrchestrationRun{}, err
	}
	return run, nil
}

func (s *Store) OrchestrationRunByID(ctx context.Context, id, userID string) (OrchestrationRun, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, agent_id, title, mode, COALESCE(first_cli,''), COALESCE(profile,'default'), prompt, COALESCE(cwd,''),
			COALESCE(run_cwd,''), COALESCE(codex_thread_id,''), COALESCE(claude_started,0), max_turns, status,
			COALESCE(error,''), COALESCE(files_json,'[]'), created_at, updated_at, COALESCE(finished_at,0)
		FROM orchestration_runs
		WHERE id = ? AND user_id = ?
	`, id, userID)
	return scanOrchestrationRun(row)
}

func (s *Store) OrchestrationRunByIDAnyUser(ctx context.Context, id string) (OrchestrationRun, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, user_id, agent_id, title, mode, COALESCE(first_cli,''), COALESCE(profile,'default'), prompt, COALESCE(cwd,''),
			COALESCE(run_cwd,''), COALESCE(codex_thread_id,''), COALESCE(claude_started,0), max_turns, status,
			COALESCE(error,''), COALESCE(files_json,'[]'), created_at, updated_at, COALESCE(finished_at,0)
		FROM orchestration_runs
		WHERE id = ?
	`, id)
	return scanOrchestrationRun(row)
}

func (s *Store) ListOrchestrationRuns(ctx context.Context, userID string, limit int) ([]OrchestrationRun, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, agent_id, title, mode, COALESCE(first_cli,''), COALESCE(profile,'default'), prompt, COALESCE(cwd,''),
			COALESCE(run_cwd,''), COALESCE(codex_thread_id,''), COALESCE(claude_started,0), max_turns, status,
			COALESCE(error,''), COALESCE(files_json,'[]'), created_at, updated_at, COALESCE(finished_at,0)
		FROM orchestration_runs
		WHERE user_id = ?
		ORDER BY updated_at DESC, created_at DESC
		LIMIT ?
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrchestrationRun
	for rows.Next() {
		run, err := scanOrchestrationRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) UpdateOrchestrationRunStatus(ctx context.Context, id, status, errText string) error {
	now := time.Now().Unix()
	var finishedAt any
	if status == OrchestrationCompleted || status == OrchestrationFailed || status == OrchestrationCanceled {
		finishedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE orchestration_runs
		SET status = ?, error = ?, finished_at = COALESCE(?, finished_at), updated_at = ?
		WHERE id = ?
	`, status, nullString(errText), finishedAt, now, id)
	return err
}

func (s *Store) UpdateOrchestrationRunSettings(ctx context.Context, id, agentID, mode, firstCLI, profile, cwd string, maxTurns int, files []OrchestrationFile) error {
	if id == "" || agentID == "" {
		return errors.New("run id and agent id are required")
	}
	if mode != "collaboration" && mode != "debate" {
		return errors.New("mode must be collaboration or debate")
	}
	firstCLI = normalizeOrchestrationFirstCLI(firstCLI)
	if maxTurns <= 0 {
		maxTurns = 4
	}
	if maxTurns > 12 {
		maxTurns = 12
	}
	profile = normalizeOrchestrationProfile(profile)
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `
		UPDATE orchestration_runs
		SET agent_id = ?, mode = ?, first_cli = ?, profile = ?, cwd = ?, max_turns = ?, files_json = ?, finished_at = NULL, updated_at = ?
		WHERE id = ?
	`, agentID, mode, firstCLI, profile, nullString(cwd), maxTurns, string(filesJSON), now, id)
	return err
}

func (s *Store) UpdateOrchestrationRunSession(ctx context.Context, id, codexThreadID string, claudeStarted bool, runCWD string) error {
	if id == "" {
		return errors.New("run id is required")
	}
	_, err := s.db.ExecContext(ctx, `
		UPDATE orchestration_runs
		SET codex_thread_id = CASE WHEN ? <> '' THEN ? ELSE codex_thread_id END,
			claude_started = CASE WHEN ? = 1 THEN 1 ELSE claude_started END,
			run_cwd = CASE WHEN ? <> '' AND run_cwd = '' THEN ? ELSE run_cwd END
		WHERE id = ?
	`, codexThreadID, codexThreadID, boolInt(claudeStarted), runCWD, runCWD, id)
	return err
}

func (s *Store) AddOrchestrationEvent(ctx context.Context, event OrchestrationEvent) (OrchestrationEvent, error) {
	if event.RunID == "" || event.Kind == "" {
		return OrchestrationEvent{}, errors.New("run id and kind are required")
	}
	now := time.Now().Unix()
	if event.ID == "" {
		event.ID = NewID("evt")
	}
	event.CreatedAt = now
	event.Source = normalizeOrchestrationEventSource(event.Source, event.Kind)
	if event.Severity == "" {
		event.Severity = severityFromLegacyStatus(event.Status)
		if event.Severity != "" {
			event.Status = ""
		}
	}
	dataJSON, err := orchestrationEventDataJSON(event)
	if err != nil {
		return OrchestrationEvent{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OrchestrationEvent{}, err
	}
	defer tx.Rollback()
	if event.Seq <= 0 {
		row := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM orchestration_events WHERE run_id = ?`, event.RunID)
		if err := row.Scan(&event.Seq); err != nil {
			return OrchestrationEvent{}, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO orchestration_events
			(id, run_id, seq, kind, source, severity, role, cli, turn_id, content, status, error, data_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.RunID, event.Seq, event.Kind, nullString(event.Source), nullString(event.Severity), nullString(event.Role), nullString(event.CLI),
		nullString(event.TurnID), nullString(event.Content), nullString(event.Status), nullString(event.Error),
		nullString(dataJSON), now); err != nil {
		return OrchestrationEvent{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE orchestration_runs SET updated_at = ? WHERE id = ?`, now, event.RunID); err != nil {
		return OrchestrationEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return OrchestrationEvent{}, err
	}
	return event, nil
}

func (s *Store) ListOrchestrationEvents(ctx context.Context, runID string, limit int) ([]OrchestrationEvent, error) {
	if limit <= 0 {
		limit = 1000
	} else if limit > 10000 {
		limit = 10000
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, run_id, seq, kind, COALESCE(source,''), COALESCE(severity,''), COALESCE(role,''), COALESCE(cli,''), COALESCE(turn_id,''),
			COALESCE(content,''), COALESCE(status,''), COALESCE(error,''), COALESCE(data_json,''), created_at
		FROM (
			SELECT id, run_id, seq, kind, source, severity, role, cli, turn_id, content, status, error, data_json, created_at
			FROM orchestration_events
			WHERE run_id = ?
			ORDER BY seq DESC
			LIMIT ?
		)
		ORDER BY seq ASC
	`, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OrchestrationEvent
	for rows.Next() {
		event, err := scanOrchestrationEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

func scanOrchestrationRun(row interface{ Scan(dest ...any) error }) (OrchestrationRun, error) {
	var run OrchestrationRun
	var filesJSON string
	var claudeStarted int
	if err := row.Scan(&run.ID, &run.UserID, &run.AgentID, &run.Title, &run.Mode, &run.FirstCLI, &run.Profile, &run.Prompt, &run.CWD,
		&run.RunCWD, &run.CodexThreadID, &claudeStarted, &run.MaxTurns, &run.Status, &run.Error, &filesJSON, &run.CreatedAt, &run.UpdatedAt, &run.FinishedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OrchestrationRun{}, ErrNotFound
		}
		return OrchestrationRun{}, err
	}
	if strings.TrimSpace(filesJSON) != "" {
		_ = json.Unmarshal([]byte(filesJSON), &run.Files)
	}
	run.FirstCLI = normalizeOrchestrationFirstCLI(run.FirstCLI)
	run.Profile = normalizeOrchestrationProfile(run.Profile)
	run.ClaudeStarted = claudeStarted != 0
	return run, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func normalizeOrchestrationFirstCLI(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "codex":
		return "codex"
	default:
		return "claude"
	}
}

func normalizeOrchestrationProfile(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "default":
		return "default"
	case "formal-proof":
		return "formal-proof"
	default:
		return "default"
	}
}

func scanOrchestrationEvent(row interface{ Scan(dest ...any) error }) (OrchestrationEvent, error) {
	var event OrchestrationEvent
	var dataJSON string
	if err := row.Scan(&event.ID, &event.RunID, &event.Seq, &event.Kind, &event.Source, &event.Severity, &event.Role, &event.CLI, &event.TurnID,
		&event.Content, &event.Status, &event.Error, &dataJSON, &event.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return OrchestrationEvent{}, ErrNotFound
		}
		return OrchestrationEvent{}, err
	}
	if strings.TrimSpace(dataJSON) != "" {
		_ = json.Unmarshal([]byte(dataJSON), &event.Data)
		var typed struct {
			Extra          map[string]any           `json:"extra,omitempty"`
			CommandData    *protocol.CommandData    `json:"commandData,omitempty"`
			RunStartData   *protocol.RunStartData   `json:"runStartData,omitempty"`
			TurnStartData  *protocol.TurnStartData  `json:"turnStartData,omitempty"`
			RunEndData     *protocol.RunEndData     `json:"runEndData,omitempty"`
			BridgeNoteData *protocol.BridgeNoteData `json:"bridgeNoteData,omitempty"`
			RunConclusion  *protocol.RunConclusion  `json:"runConclusion,omitempty"`
		}
		if err := json.Unmarshal([]byte(dataJSON), &typed); err == nil && (typed.Extra != nil ||
			typed.CommandData != nil || typed.RunStartData != nil || typed.TurnStartData != nil ||
			typed.RunEndData != nil || typed.BridgeNoteData != nil || typed.RunConclusion != nil) {
			event.Data = typed.Extra
			event.CommandData = typed.CommandData
			event.RunStartData = typed.RunStartData
			event.TurnStartData = typed.TurnStartData
			event.RunEndData = typed.RunEndData
			event.BridgeNoteData = typed.BridgeNoteData
			event.RunConclusion = typed.RunConclusion
		}
	}
	event.Source = normalizeOrchestrationEventSource(event.Source, event.Kind)
	if event.Severity == "" {
		event.Severity = severityFromLegacyStatus(event.Status)
		if event.Severity != "" {
			event.Status = ""
		}
	}
	return event, nil
}

func orchestrationEventDataJSON(event OrchestrationEvent) (string, error) {
	if event.CommandData == nil && event.RunStartData == nil && event.TurnStartData == nil &&
		event.RunEndData == nil && event.BridgeNoteData == nil && event.RunConclusion == nil {
		if event.Data == nil {
			return "", nil
		}
		b, err := json.Marshal(event.Data)
		return string(b), err
	}
	typed := struct {
		Extra          map[string]any           `json:"extra,omitempty"`
		CommandData    *protocol.CommandData    `json:"commandData,omitempty"`
		RunStartData   *protocol.RunStartData   `json:"runStartData,omitempty"`
		TurnStartData  *protocol.TurnStartData  `json:"turnStartData,omitempty"`
		RunEndData     *protocol.RunEndData     `json:"runEndData,omitempty"`
		BridgeNoteData *protocol.BridgeNoteData `json:"bridgeNoteData,omitempty"`
		RunConclusion  *protocol.RunConclusion  `json:"runConclusion,omitempty"`
	}{
		Extra:          event.Data,
		CommandData:    event.CommandData,
		RunStartData:   event.RunStartData,
		TurnStartData:  event.TurnStartData,
		RunEndData:     event.RunEndData,
		BridgeNoteData: event.BridgeNoteData,
		RunConclusion:  event.RunConclusion,
	}
	b, err := json.Marshal(typed)
	return string(b), err
}

func normalizeOrchestrationEventSource(source, kind string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "cli", "bridge", "user":
		return strings.ToLower(strings.TrimSpace(source))
	}
	switch kind {
	case "user.message":
		return "user"
	case "run.start", "run.error", "run.canceling", "run.cancelled":
		return "bridge"
	default:
		return "cli"
	}
}

func severityFromLegacyStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "info", "warning", "error":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return ""
	}
}

func scanRun(row interface{ Scan(dest ...any) error }) (Run, error) {
	var run Run
	if err := row.Scan(&run.ID, &run.SessionID, &run.PromptID, &run.Status, &run.Error, &run.UsageJSON, &run.StartedAt, &run.FinishedAt, &run.CreatedAt, &run.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Run{}, ErrNotFound
		}
		return Run{}, err
	}
	return run, nil
}

func nullString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}

func isUniqueConstraint(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "constraint failed") && strings.Contains(strings.ToLower(msg), "unique")
}

func isDuplicateColumn(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate column")
}

type EnrollToken struct {
	Token         string `json:"token"`
	UserID        string `json:"userId,omitempty"`
	Label         string `json:"label,omitempty"`
	UsedByMachine string `json:"usedByMachine,omitempty"`
	ExpiresAt     int64  `json:"expiresAt,omitempty"`
	CreatedAt     int64  `json:"createdAt,omitempty"`
}

func (s *Store) CreateEnrollToken(ctx context.Context, token string, expiresAt *time.Time) error {
	return s.CreateEnrollTokenForUser(ctx, token, "", "", expiresAt)
}

func (s *Store) CreateEnrollTokenForUser(ctx context.Context, token, userID, label string, expiresAt *time.Time) error {
	token = CleanToken(token)
	if token == "" {
		return errors.New("token is required")
	}
	var expires sql.NullInt64
	if expiresAt != nil {
		expires = sql.NullInt64{Int64: expiresAt.Unix(), Valid: true}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO enroll_tokens (token, user_id, label, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, token, nullString(userID), nullString(strings.TrimSpace(label)), expires, time.Now().Unix())
	return err
}

func (s *Store) ConsumeEnrollToken(ctx context.Context, token, machineID string) error {
	_, err := s.ConsumeEnrollTokenInfo(ctx, token, machineID)
	return err
}

func (s *Store) ConsumeEnrollTokenInfo(ctx context.Context, token, machineID string) (EnrollToken, error) {
	token = CleanToken(token)
	if token == "" {
		return EnrollToken{}, ErrUnauthorized
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EnrollToken{}, err
	}
	defer tx.Rollback()

	row := tx.QueryRowContext(ctx, `SELECT COALESCE(user_id,''), COALESCE(label,''), used_by_machine, expires_at, COALESCE(created_at,0) FROM enroll_tokens WHERE token = ?`, token)
	info := EnrollToken{Token: token}
	var used sql.NullString
	var expires sql.NullInt64
	if err := row.Scan(&info.UserID, &info.Label, &used, &expires, &info.CreatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return EnrollToken{}, ErrUnauthorized
		}
		return EnrollToken{}, err
	}
	if expires.Valid && expires.Int64 < time.Now().Unix() {
		return EnrollToken{}, ErrTokenExpired
	}
	if expires.Valid {
		info.ExpiresAt = expires.Int64
	}
	if used.Valid {
		info.UsedByMachine = used.String
	}
	if used.Valid && used.String != "" && used.String != machineID {
		return EnrollToken{}, ErrTokenConsumed
	}
	if !used.Valid || used.String == "" {
		if _, err := tx.ExecContext(ctx, `UPDATE enroll_tokens SET used_by_machine = ? WHERE token = ?`, machineID, token); err != nil {
			return EnrollToken{}, err
		}
		info.UsedByMachine = machineID
	}
	return info, tx.Commit()
}
