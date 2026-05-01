package proposal

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// SQLiteStore implements ProposalStore backed by SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// NewSQLiteStore creates a SQLiteStore sharing the given database.
func NewSQLiteStore(db *sql.DB) *SQLiteStore {
	return &SQLiteStore{db: db}
}

func scanProposalSQLite(scanner interface{ Scan(...any) error }) (*MissionProposal, error) {
	var p MissionProposal
	var evidence sql.NullString
	var createdAt string
	var reviewedAt sql.NullString
	err := scanner.Scan(
		&p.ID, &p.ObjectiveID, &p.ParentMission, &p.ProposedBy, &p.Task, &p.Contract,
		&p.Priority, &p.DedupeKey, &p.Rationale, &evidence, &p.Status, &createdAt,
		&reviewedAt, &p.RejectionNote,
	)
	if err != nil {
		return nil, err
	}
	if createdAt != "" {
		p.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	}
	if evidence.Valid && evidence.String != "" {
		p.Evidence = json.RawMessage(evidence.String)
	}
	if reviewedAt.Valid && reviewedAt.String != "" {
		t, _ := time.Parse(time.RFC3339Nano, reviewedAt.String)
		p.ReviewedAt = &t
	}
	return &p, nil
}

const sqliteProposalCols = `id, objective_id, parent_mission, proposed_by, task, contract,
	priority, dedupe_key, rationale, evidence, status, created_at, reviewed_at, rejection_note`

func (s *SQLiteStore) Submit(ctx context.Context, p *MissionProposal) error {
	if p.Status == "" {
		p.Status = StatusPending
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}

	var evidence *string
	if p.Evidence != nil {
		s := string(p.Evidence)
		evidence = &s
	}

	res, err := s.db.ExecContext(ctx,
		`INSERT INTO proposals (objective_id, parent_mission, proposed_by, task, contract,
			priority, dedupe_key, rationale, evidence, status, created_at, rejection_note)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		p.ObjectiveID, p.ParentMission, p.ProposedBy, p.Task, p.Contract,
		p.Priority, p.DedupeKey, p.Rationale, evidence, p.Status,
		p.CreatedAt.Format(time.RFC3339Nano), p.RejectionNote)
	if err != nil {
		return fmt.Errorf("proposal sqlitestore: submit: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("proposal sqlitestore: last insert id: %w", err)
	}
	p.ID = id
	return nil
}

func (s *SQLiteStore) PendingProposals(ctx context.Context) ([]*MissionProposal, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+sqliteProposalCols+` FROM proposals WHERE status = 'pending'
		 ORDER BY priority DESC, created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("proposal sqlitestore: pending: %w", err)
	}
	defer rows.Close()

	var result []*MissionProposal
	for rows.Next() {
		p, err := scanProposalSQLite(rows)
		if err != nil {
			return nil, fmt.Errorf("proposal sqlitestore: scan: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) Approve(ctx context.Context, id int64) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE proposals SET status = 'approved', reviewed_at = ? WHERE id = ?`,
		now, id)
	if err != nil {
		return fmt.Errorf("proposal sqlitestore: approve: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) Reject(ctx context.Context, id int64, note string) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE proposals SET status = 'rejected', reviewed_at = ?, rejection_note = ? WHERE id = ?`,
		now, note, id)
	if err != nil {
		return fmt.Errorf("proposal sqlitestore: reject: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) UpdateStatus(ctx context.Context, id int64, status string) error {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE proposals SET status = ?, reviewed_at = ? WHERE id = ?`,
		status, now, id)
	if err != nil {
		return fmt.Errorf("proposal sqlitestore: update status: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLiteStore) PendingForObjective(ctx context.Context, objectiveID string) ([]*MissionProposal, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+sqliteProposalCols+` FROM proposals
		 WHERE status = 'pending' AND objective_id = ?
		 ORDER BY priority DESC, created_at ASC`, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("proposal sqlitestore: pending for objective: %w", err)
	}
	defer rows.Close()

	var result []*MissionProposal
	for rows.Next() {
		p, err := scanProposalSQLite(rows)
		if err != nil {
			return nil, fmt.Errorf("proposal sqlitestore: scan: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *SQLiteStore) RejectAllPending(ctx context.Context, objectiveID string, note string) (int, error) {
	now := time.Now().Format(time.RFC3339Nano)
	res, err := s.db.ExecContext(ctx,
		`UPDATE proposals SET status = 'rejected', reviewed_at = ?, rejection_note = ?
		 WHERE objective_id = ? AND status = 'pending'`,
		now, note, objectiveID)
	if err != nil {
		return 0, fmt.Errorf("proposal sqlitestore: reject all pending: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

var _ ProposalStore = (*SQLiteStore)(nil)
