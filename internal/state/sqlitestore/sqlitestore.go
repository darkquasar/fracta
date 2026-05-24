package sqlitestore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/darkquasar/fracta/internal/events"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/state"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS agents (
	task           TEXT PRIMARY KEY,
	host_type      TEXT NOT NULL DEFAULT '',
	resume_token   TEXT NOT NULL DEFAULT '',
	workspace_path  TEXT NOT NULL,
	branch_name    TEXT NOT NULL,
	base_branch    TEXT NOT NULL DEFAULT '',
	status         TEXT NOT NULL DEFAULT '',
	last_output    TEXT NOT NULL DEFAULT '',
	start_time     TEXT NOT NULL DEFAULT '',
	mode           TEXT NOT NULL DEFAULT '',
	current_intent TEXT NOT NULL DEFAULT '',
	mission_id     INTEGER NOT NULL DEFAULT 0,
	objective_id   TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS chessmaster (
	id          INTEGER PRIMARY KEY CHECK (id = 1),
	status      TEXT NOT NULL DEFAULT '',
	last_action TEXT NOT NULL DEFAULT '',
	updated_at  TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS messages (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	from_task TEXT NOT NULL,
	to_task   TEXT NOT NULL,
	content   TEXT NOT NULL,
	timestamp TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_messages_to_id ON messages (to_task, id);

CREATE TABLE IF NOT EXISTS cursors (
	task    TEXT PRIMARY KEY,
	last_id INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS objectives (
	id               TEXT PRIMARY KEY,
	description      TEXT NOT NULL DEFAULT '',
	status           TEXT NOT NULL DEFAULT 'open',
	created_at       TEXT NOT NULL DEFAULT '',
	updated_at       TEXT NOT NULL DEFAULT '',
	created_by       TEXT NOT NULL DEFAULT '',
	max_missions     INTEGER NOT NULL DEFAULT 100,
	max_depth        INTEGER NOT NULL DEFAULT 5,
	max_runtime      INTEGER NOT NULL DEFAULT 14400000000000,
	max_branching    INTEGER NOT NULL DEFAULT 5,
	max_tokens       INTEGER NOT NULL DEFAULT 0,
	max_graph_writes INTEGER NOT NULL DEFAULT 0,
	mission_count    INTEGER NOT NULL DEFAULT 0,
	finding_count    INTEGER NOT NULL DEFAULT 0,
	tokens_used      INTEGER NOT NULL DEFAULT 0,
	graph_writes     INTEGER NOT NULL DEFAULT 0,
	outcome          TEXT NOT NULL DEFAULT '',
	outcome_data     TEXT
);

CREATE TABLE IF NOT EXISTS proposals (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	objective_id    TEXT NOT NULL,
	parent_mission  INTEGER NOT NULL,
	proposed_by     TEXT NOT NULL DEFAULT '',
	task            TEXT NOT NULL DEFAULT '',
	contract        TEXT NOT NULL DEFAULT '',
	priority        INTEGER NOT NULL DEFAULT 0,
	dedupe_key      TEXT NOT NULL DEFAULT '',
	rationale       TEXT NOT NULL DEFAULT '',
	evidence        TEXT,
	status          TEXT NOT NULL DEFAULT 'pending',
	created_at      TEXT NOT NULL DEFAULT '',
	reviewed_at     TEXT,
	rejection_note  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_proposals_pending
	ON proposals (status, priority DESC, created_at)
	WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_proposals_objective
	ON proposals (objective_id);

CREATE TABLE IF NOT EXISTS agent_events (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	task      TEXT NOT NULL,
	event     TEXT NOT NULL,
	detail    TEXT,
	timestamp TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_agent_events_task ON agent_events (task, timestamp DESC);
`

// SQLiteStore implements state.Store backed by a SQLite database.
type SQLiteStore struct {
	db      *sql.DB
	mailbox *SQLiteMailbox
}

// New opens (or creates) a SQLite database at the given path and ensures
// the schema exists. The returned SQLiteStore satisfies state.Store.
func New(dbPath string) (*SQLiteStore, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0755); err != nil {
		return nil, fmt.Errorf("sqlitestore: creating directory: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: open: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlitestore: migrate: %w", err)
	}
	// Migrate old schema: drop 'id' column if it exists, rename columns, add new columns.
	if err := migrateOldSchema(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlitestore: migration: %w", err)
	}
	// Add mission_id column if missing (pre-spec-13 schema).
	if err := migrateMissionID(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlitestore: mission_id migration: %w", err)
	}
	// Add objective_id column to agents if missing (spec-16a).
	if err := migrateObjectiveID(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlitestore: objective_id migration: %w", err)
	}
	// Add DAG columns to missions if missing (spec-16a).
	if err := migrateMissionsDAG(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlitestore: missions DAG migration: %w", err)
	}
	// Add structured event columns to agent_events (spec-28).
	if err := migrateAgentEventsSpec28(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlitestore: agent_events spec-28 migration: %w", err)
	}
	s := &SQLiteStore{db: db}
	s.mailbox = newSQLiteMailbox(db)
	return s, nil
}

// migrateOldSchema handles migration from old schema variants:
// - Old schema had 'id' column + 'worktree_path' column
// - Intermediate schema had task as PK but 'worktree_path' column
// - Pre-spec-11 schema had 'session_id' instead of 'resume_token' and no 'host_type'
// Returns nil if already on current schema.
func migrateOldSchema(db *sql.DB) error {
	var hasID, hasWorktreePath, hasSessionID, hasResumeToken int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name='id'`).Scan(&hasID)
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name='worktree_path'`).Scan(&hasWorktreePath)
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name='session_id'`).Scan(&hasSessionID)
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name='resume_token'`).Scan(&hasResumeToken)

	needsMigration := hasID > 0 || hasWorktreePath > 0 || (hasSessionID > 0 && hasResumeToken == 0)
	if !needsMigration {
		return nil // already on current schema
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`ALTER TABLE agents RENAME TO agents_old`); err != nil {
		return fmt.Errorf("rename old table: %w", err)
	}
	if _, err := tx.Exec(`CREATE TABLE agents (
		task            TEXT PRIMARY KEY,
		host_type       TEXT NOT NULL DEFAULT '',
		resume_token    TEXT NOT NULL DEFAULT '',
		workspace_path  TEXT NOT NULL,
		branch_name     TEXT NOT NULL,
		base_branch     TEXT NOT NULL DEFAULT '',
		status          TEXT NOT NULL DEFAULT '',
		last_output     TEXT NOT NULL DEFAULT '',
		start_time      TEXT NOT NULL DEFAULT '',
		mode            TEXT NOT NULL DEFAULT '',
		current_intent  TEXT NOT NULL DEFAULT '',
		mission_id      INTEGER NOT NULL DEFAULT 0,
		objective_id    TEXT NOT NULL DEFAULT ''
	)`); err != nil {
		return fmt.Errorf("create new table: %w", err)
	}

	// Determine old column names for migration.
	oldPathCol := "workspace_path"
	if hasWorktreePath > 0 {
		oldPathCol = "worktree_path"
	}
	oldTokenCol := "resume_token"
	if hasSessionID > 0 && hasResumeToken == 0 {
		oldTokenCol = "session_id"
	}

	copySQL := fmt.Sprintf(`INSERT INTO agents (task, host_type, resume_token, workspace_path, branch_name, base_branch,
		status, last_output, start_time, mode, current_intent)
		SELECT task, '', %s, %s, branch_name, base_branch,
		status, last_output, start_time, mode, current_intent
		FROM agents_old`, oldTokenCol, oldPathCol)
	if _, err := tx.Exec(copySQL); err != nil {
		return fmt.Errorf("copy data: %w", err)
	}
	if _, err := tx.Exec(`DROP TABLE agents_old`); err != nil {
		return fmt.Errorf("drop old table: %w", err)
	}

	return tx.Commit()
}

// DB returns the underlying *sql.DB for constructing stores that share
// the connection (e.g., ObjectiveStore, ProposalStore).
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// Mailbox returns the mailbox implementation sharing this store's database.
func (s *SQLiteStore) Mailbox() mailbox.Mailbox {
	return s.mailbox
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

const agentColumns = `task, host_type, resume_token, workspace_path, branch_name, base_branch,
	status, last_output, start_time, mode, current_intent, mission_id, objective_id`

func scanAgent(scanner interface{ Scan(...any) error }) (model.AgentEntry, error) {
	var a model.AgentEntry
	var startTime string
	err := scanner.Scan(&a.Task, &a.RuntimeType, &a.ResumeToken, &a.WorkspacePath, &a.BranchName, &a.BaseBranch,
		&a.Status, &a.LastOutput, &startTime, &a.Mode, &a.CurrentIntent, &a.MissionID, &a.ObjectiveID)
	if err != nil {
		return a, err
	}
	if startTime != "" {
		a.StartTime, _ = time.Parse(time.RFC3339, startTime)
	}
	return a, nil
}

func (s *SQLiteStore) Load(ctx context.Context) (model.State, error) {
	var st model.State

	rows, err := s.db.QueryContext(ctx, `SELECT `+agentColumns+` FROM agents`)
	if err != nil {
		return st, fmt.Errorf("sqlitestore: load agents: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return st, fmt.Errorf("sqlitestore: scan agent: %w", err)
		}
		st.Agents = append(st.Agents, a)
	}
	if err := rows.Err(); err != nil {
		return st, fmt.Errorf("sqlitestore: iterate agents: %w", err)
	}

	loadChessmaster(ctx, s.db, &st)
	return st, nil
}

func (s *SQLiteStore) WithLock(ctx context.Context, fn func(*model.State) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitestore: begin tx: %w", err)
	}
	defer tx.Rollback()

	// Force an EXCLUSIVE lock via a dummy write.
	if _, err := tx.ExecContext(ctx, `UPDATE chessmaster SET id = 1 WHERE 0`); err != nil {
		if _, err2 := tx.ExecContext(ctx, `INSERT OR IGNORE INTO chessmaster (id, status, last_action, updated_at) VALUES (1, '', '', '')`); err2 != nil {
			return fmt.Errorf("sqlitestore: lock: %w", err2)
		}
	}

	st, err := s.loadTx(ctx, tx)
	if err != nil {
		return err
	}

	if err := fn(&st); err != nil {
		return err
	}

	if err := s.saveTx(ctx, tx, st); err != nil {
		return err
	}

	return tx.Commit()
}

func (s *SQLiteStore) FindAgent(ctx context.Context, task string) (*model.AgentEntry, error) {
	a, err := scanAgent(s.db.QueryRowContext(ctx, `SELECT `+agentColumns+` FROM agents WHERE task = ?`, task))
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: find agent: %w", err)
	}
	return &a, nil
}

func (s *SQLiteStore) loadTx(ctx context.Context, tx *sql.Tx) (model.State, error) {
	var st model.State

	rows, err := tx.QueryContext(ctx, `SELECT `+agentColumns+` FROM agents`)
	if err != nil {
		return st, fmt.Errorf("sqlitestore: load agents: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return st, fmt.Errorf("sqlitestore: scan agent: %w", err)
		}
		st.Agents = append(st.Agents, a)
	}
	if err := rows.Err(); err != nil {
		return st, fmt.Errorf("sqlitestore: iterate agents: %w", err)
	}

	loadChessmasterTx(ctx, tx, &st)
	return st, nil
}

func (s *SQLiteStore) saveTx(ctx context.Context, tx *sql.Tx, st model.State) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM agents`); err != nil {
		return fmt.Errorf("sqlitestore: clear agents: %w", err)
	}

	for _, a := range st.Agents {
		if err := insertAgentTx(ctx, tx, &a); err != nil {
			return err
		}
	}

	updatedAt := ""
	if !st.Chessmaster.UpdatedAt.IsZero() {
		updatedAt = st.Chessmaster.UpdatedAt.Format(time.RFC3339)
	}
	if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO chessmaster (id, status, last_action, updated_at)
		VALUES (1, ?, ?, ?)`, st.Chessmaster.Status, st.Chessmaster.LastAction, updatedAt); err != nil {
		return fmt.Errorf("sqlitestore: save chessmaster: %w", err)
	}

	return nil
}

func insertAgentTx(ctx context.Context, tx *sql.Tx, a *model.AgentEntry) error {
	startTime := ""
	if !a.StartTime.IsZero() {
		startTime = a.StartTime.Format(time.RFC3339)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO agents (`+agentColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.Task, a.RuntimeType, a.ResumeToken, a.WorkspacePath, a.BranchName, a.BaseBranch,
		a.Status, a.LastOutput, startTime, a.Mode, a.CurrentIntent, a.MissionID, a.ObjectiveID)
	if err != nil {
		return fmt.Errorf("sqlitestore: insert agent %s: %w", a.Task, err)
	}
	return nil
}

// RemoveAgent deletes a single agent by task name.
func (s *SQLiteStore) RemoveAgent(ctx context.Context, task string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM agents WHERE task = ?`, task)
	if err != nil {
		return fmt.Errorf("sqlitestore: remove agent %s: %w", task, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlitestore: agent %s not found", task)
	}
	return nil
}

// UpdateAgentStatus updates status and last_output for a single agent.
func (s *SQLiteStore) UpdateAgentStatus(ctx context.Context, task string, status model.AgentStatus, lastOutput string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET status = ?, last_output = ? WHERE task = ?`,
		string(status), lastOutput, task)
	if err != nil {
		return fmt.Errorf("sqlitestore: update agent status %s: %w", task, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlitestore: agent %s not found", task)
	}
	return nil
}

// UpdateAgentResult updates status, last_output, and resume_token for a single agent.
// If resumeToken is non-empty, it also updates resume_token; otherwise the
// existing resume_token is preserved. This prevents error paths from
// accidentally erasing a valid token needed for resume.
func (s *SQLiteStore) UpdateAgentResult(ctx context.Context, task string, status model.AgentStatus, lastOutput, resumeToken string) error {
	var res sql.Result
	var err error
	if resumeToken != "" {
		res, err = s.db.ExecContext(ctx, `UPDATE agents SET status = ?, last_output = ?, resume_token = ? WHERE task = ?`,
			string(status), lastOutput, resumeToken, task)
	} else {
		res, err = s.db.ExecContext(ctx, `UPDATE agents SET status = ?, last_output = ? WHERE task = ?`,
			string(status), lastOutput, task)
	}
	if err != nil {
		return fmt.Errorf("sqlitestore: update agent result %s: %w", task, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlitestore: agent %s not found", task)
	}
	return nil
}

// UpdateAgentStatusIf conditionally updates status and last_output.
// The update is applied only if the agent's current status is in expected.
func (s *SQLiteStore) UpdateAgentStatusIf(ctx context.Context, task string, expected []model.AgentStatus, newStatus model.AgentStatus, lastOutput string) (bool, error) {
	if len(expected) == 0 {
		return false, nil
	}
	args := []any{string(newStatus), lastOutput, task}
	placeholders := ""
	for i, st := range expected {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "?"
		args = append(args, string(st))
	}
	query := fmt.Sprintf(`UPDATE agents SET status = ?, last_output = ? WHERE task = ? AND status IN (%s)`, placeholders)
	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("sqlitestore: update agent status if %s: %w", task, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UpdateAgentResultIf conditionally updates status, last_output, and resume_token.
// The update is applied only if the agent's current status is in expected.
func (s *SQLiteStore) UpdateAgentResultIf(ctx context.Context, task string, expected []model.AgentStatus, status model.AgentStatus, lastOutput, resumeToken string) (bool, error) {
	if len(expected) == 0 {
		return false, nil
	}

	var query string
	var args []any
	placeholders := ""
	for i, st := range expected {
		if i > 0 {
			placeholders += ", "
		}
		placeholders += "?"
		args = append(args, string(st)) // will be reordered below
	}

	if resumeToken != "" {
		args = []any{string(status), lastOutput, resumeToken, task}
		for _, st := range expected {
			args = append(args, string(st))
		}
		query = fmt.Sprintf(`UPDATE agents SET status = ?, last_output = ?, resume_token = ? WHERE task = ? AND status IN (%s)`, placeholders)
	} else {
		args = []any{string(status), lastOutput, task}
		for _, st := range expected {
			args = append(args, string(st))
		}
		query = fmt.Sprintf(`UPDATE agents SET status = ?, last_output = ? WHERE task = ? AND status IN (%s)`, placeholders)
	}

	res, err := s.db.ExecContext(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("sqlitestore: update agent result if %s: %w", task, err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// UpdateAgentIntent updates only the current_intent field for a single agent.
func (s *SQLiteStore) UpdateAgentIntent(ctx context.Context, task, intent string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE agents SET current_intent = ? WHERE task = ?`, intent, task)
	if err != nil {
		return fmt.Errorf("sqlitestore: update agent intent %s: %w", task, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("sqlitestore: agent %s not found", task)
	}
	return nil
}

func loadChessmaster(ctx context.Context, db *sql.DB, st *model.State) {
	var status, lastAction, updatedAt string
	err := db.QueryRowContext(ctx, `SELECT status, last_action, updated_at FROM chessmaster WHERE id = 1`).
		Scan(&status, &lastAction, &updatedAt)
	if err == nil {
		st.Chessmaster.Status = status
		st.Chessmaster.LastAction = lastAction
		if updatedAt != "" {
			st.Chessmaster.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		}
	}
}

func loadChessmasterTx(ctx context.Context, tx *sql.Tx, st *model.State) {
	var status, lastAction, updatedAt string
	err := tx.QueryRowContext(ctx, `SELECT status, last_action, updated_at FROM chessmaster WHERE id = 1`).
		Scan(&status, &lastAction, &updatedAt)
	if err == nil {
		st.Chessmaster.Status = status
		st.Chessmaster.LastAction = lastAction
		if updatedAt != "" {
			st.Chessmaster.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		}
	}
}

// UpdateChessmaster updates the chessmaster singleton without touching agents.
func (s *SQLiteStore) UpdateChessmaster(ctx context.Context, status, lastAction string, updatedAt time.Time) error {
	ts := ""
	if !updatedAt.IsZero() {
		ts = updatedAt.Format(time.RFC3339)
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO chessmaster (id, status, last_action, updated_at)
		VALUES (1, ?, ?, ?)`, status, lastAction, ts)
	if err != nil {
		return fmt.Errorf("sqlitestore: update chessmaster: %w", err)
	}
	return nil
}

// migrateMissionID adds the mission_id column if it doesn't exist (pre-spec-13 schema).
func migrateMissionID(db *sql.DB) error {
	var hasMissionID int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name='mission_id'`).Scan(&hasMissionID)
	if hasMissionID > 0 {
		return nil
	}
	_, err := db.Exec(`ALTER TABLE agents ADD COLUMN mission_id INTEGER NOT NULL DEFAULT 0`)
	return err
}

// migrateObjectiveID adds the objective_id column if it doesn't exist (spec-16a).
func migrateObjectiveID(db *sql.DB) error {
	var hasCol int
	db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('agents') WHERE name='objective_id'`).Scan(&hasCol)
	if hasCol > 0 {
		return nil
	}
	_, err := db.Exec(`ALTER TABLE agents ADD COLUMN objective_id TEXT NOT NULL DEFAULT ''`)
	return err
}

// ClaimAgent atomically transitions an agent from Queued to Running
// and resets StartTime to now.
func (s *SQLiteStore) ClaimAgent(ctx context.Context, task string) error {
	now := time.Now().Format(time.RFC3339)
	res, err := s.db.ExecContext(ctx,
		`UPDATE agents SET status = ?, start_time = ? WHERE task = ? AND status = ?`,
		string(model.StatusRunning), now, task, string(model.StatusQueued))
	if err != nil {
		return fmt.Errorf("sqlitestore: claim agent %s: %w", task, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return state.ErrAgentNotClaimable
	}
	return nil
}

// migrateMissionsDAG adds the DAG columns to the missions table if they don't exist (spec-16a).
// SQLite missions table is only used if a MemoryQueue-based setup stores missions; this
// migration is defensive for schema consistency.
func migrateMissionsDAG(db *sql.DB) error {
	cols := []struct {
		name string
		ddl  string
	}{
		{"objective_id", "ALTER TABLE missions ADD COLUMN objective_id TEXT"},
		{"parent_id", "ALTER TABLE missions ADD COLUMN parent_id INTEGER"},
		{"depth", "ALTER TABLE missions ADD COLUMN depth INTEGER NOT NULL DEFAULT 0"},
		{"dedupe_key", "ALTER TABLE missions ADD COLUMN dedupe_key TEXT NOT NULL DEFAULT ''"},
		{"proposed_by", "ALTER TABLE missions ADD COLUMN proposed_by TEXT NOT NULL DEFAULT ''"},
	}
	for _, c := range cols {
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('missions') WHERE name=?`, c.name).Scan(&count)
		if count > 0 {
			continue
		}
		// Table might not exist (no missions table in SQLite-only mode) — skip silently.
		if _, err := db.Exec(c.ddl); err != nil {
			return nil // missions table absent or column already exists
		}
	}
	return nil
}

// migrateAgentEventsSpec28 adds the structured event columns introduced by spec-28.
// All new columns are nullable or have safe defaults so existing readers continue to work.
func migrateAgentEventsSpec28(db *sql.DB) error {
	cols := []struct {
		name string
		ddl  string
	}{
		{"event_id", "ALTER TABLE agent_events ADD COLUMN event_id TEXT"},
		{"component", "ALTER TABLE agent_events ADD COLUMN component TEXT"},
		{"category", "ALTER TABLE agent_events ADD COLUMN category TEXT"},
		{"resource", "ALTER TABLE agent_events ADD COLUMN resource TEXT"},
		{"action", "ALTER TABLE agent_events ADD COLUMN action TEXT"},
		{"outcome", "ALTER TABLE agent_events ADD COLUMN outcome TEXT"},
		{"severity", "ALTER TABLE agent_events ADD COLUMN severity TEXT NOT NULL DEFAULT 'info'"},
		{"mission_id", "ALTER TABLE agent_events ADD COLUMN mission_id INTEGER NOT NULL DEFAULT 0"},
		{"objective_id", "ALTER TABLE agent_events ADD COLUMN objective_id TEXT NOT NULL DEFAULT ''"},
		{"attrs_json", "ALTER TABLE agent_events ADD COLUMN attrs_json TEXT"},
	}
	for _, c := range cols {
		var count int
		db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('agent_events') WHERE name=?`, c.name).Scan(&count)
		if count > 0 {
			continue
		}
		if _, err := db.Exec(c.ddl); err != nil {
			return fmt.Errorf("add column %s: %w", c.name, err)
		}
	}
	return nil
}

// Deprecated: EmitEvent is replaced by events.StoreSink + InsertEvent.
// Retained for backward compatibility with existing tests.
func (s *SQLiteStore) EmitEvent(ctx context.Context, task, event, detail string) error {
	log := fractalog.Component("events")
	log.Info("agent event", "task", task, "event", event, "detail", detail)

	ts := time.Now().Format(time.RFC3339)
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_events (task, event, detail, timestamp) VALUES (?, ?, ?, ?)`,
		task, event, detail, ts)
	if err != nil {
		return fmt.Errorf("sqlitestore: emit event %s/%s: %w", task, event, err)
	}
	return nil
}

// InsertEvent writes a structured event to the agent_events table.
// Satisfies events.EventInserter for StoreSink.
func (s *SQLiteStore) InsertEvent(ctx context.Context, p events.InsertEventParams) error {
	eventTime := p.Time
	if eventTime.IsZero() {
		eventTime = time.Now()
	}
	ts := eventTime.Format(time.RFC3339)
	var attrsJSON any
	if p.AttrsJSON != "" {
		attrsJSON = p.AttrsJSON
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO agent_events
			(event_id, task, event, component, category, resource, action, outcome,
			 severity, mission_id, objective_id, detail, attrs_json, timestamp)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.EventID, p.Task, p.Event, p.Component, p.Category, p.Resource,
		p.Action, p.Outcome, p.Severity, p.MissionID, p.ObjectiveID,
		p.Detail, attrsJSON, ts)
	if err != nil {
		return fmt.Errorf("sqlitestore: insert event: %w", err)
	}
	return nil
}

// RecentEvents returns the most recent events for a task, ordered newest-first.
func (s *SQLiteStore) RecentEvents(ctx context.Context, task string, limit int) ([]events.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, event_id, task, component, category, resource, action, outcome,
		        severity, mission_id, objective_id, detail, attrs_json, timestamp
		 FROM agent_events
		 WHERE task = ?
		 ORDER BY id DESC
		 LIMIT ?`, task, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: recent events: %w", err)
	}
	defer rows.Close()

	var result []events.Event
	for rows.Next() {
		e, err := scanSQLiteEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// EventsSince returns events for a task after the event with the given UUID,
// ordered oldest-first (ascending), up to limit rows.
// Resolves the UUID to a DB row ID via subquery.
func (s *SQLiteStore) EventsSince(ctx context.Context, task string, sinceEventID string, limit int) ([]events.Event, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, event_id, task, component, category, resource, action, outcome,
		        severity, mission_id, objective_id, detail, attrs_json, timestamp
		 FROM agent_events
		 WHERE task = ? AND id > (SELECT id FROM agent_events WHERE event_id = ? LIMIT 1)
		 ORDER BY id ASC
		 LIMIT ?`, task, sinceEventID, limit)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: events since: %w", err)
	}
	defer rows.Close()

	var result []events.Event
	for rows.Next() {
		e, err := scanSQLiteEvent(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, e)
	}
	return result, rows.Err()
}

// scanSQLiteEvent reconstructs an events.Event from a SQLite database row.
func scanSQLiteEvent(rows *sql.Rows) (events.Event, error) {
	var (
		dbID        int64
		eventID     sql.NullString
		task        string
		component   sql.NullString
		category    sql.NullString
		resource    sql.NullString
		action      sql.NullString
		outcome     sql.NullString
		severity    string
		missionID   int64
		objectiveID string
		detail      sql.NullString
		attrsJSON   sql.NullString
		timestamp   string
	)

	err := rows.Scan(&dbID, &eventID, &task, &component, &category, &resource,
		&action, &outcome, &severity, &missionID, &objectiveID, &detail,
		&attrsJSON, &timestamp)
	if err != nil {
		return events.Event{}, fmt.Errorf("sqlitestore: scan event: %w", err)
	}

	e := events.Event{
		Task:        task,
		Severity:    severity,
		MissionID:   missionID,
		ObjectiveID: objectiveID,
	}

	// Parse timestamp (stored as RFC3339 string in SQLite).
	if t, err := time.Parse(time.RFC3339, timestamp); err == nil {
		e.Time = t
	}

	if eventID.Valid {
		e.ID = eventID.String
	}
	if component.Valid {
		e.Component = component.String
	}
	if category.Valid {
		e.Category = category.String
	}
	if resource.Valid {
		e.Resource = resource.String
	}
	if action.Valid {
		e.Action = action.String
	}
	if outcome.Valid {
		e.Outcome = outcome.String
	}
	if detail.Valid {
		e.Detail = detail.String
	}
	if attrsJSON.Valid && attrsJSON.String != "" {
		var attrs map[string]string
		if err := json.Unmarshal([]byte(attrsJSON.String), &attrs); err == nil {
			e.Attrs = attrs
		}
	}

	return e, nil
}

// Verify interface compliance at compile time.
var _ state.Store = (*SQLiteStore)(nil)
var _ events.EventInserter = (*SQLiteStore)(nil)
var _ state.EventReader = (*SQLiteStore)(nil)
