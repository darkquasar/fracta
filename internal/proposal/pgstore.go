package proposal

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresStore implements ProposalStore backed by PostgreSQL.
type PostgresStore struct {
	pool *pgxpool.Pool
}

// NewPostgresStore creates a PostgresStore sharing the given pool.
func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

const proposalCols = `id, objective_id, parent_mission, proposed_by, task, contract,
	priority, dedupe_key, rationale, evidence, status, created_at, reviewed_at, rejection_note`

func scanProposal(row pgx.Row) (*MissionProposal, error) {
	var p MissionProposal
	var evidence []byte
	err := row.Scan(
		&p.ID, &p.ObjectiveID, &p.ParentMission, &p.ProposedBy, &p.Task, &p.Contract,
		&p.Priority, &p.DedupeKey, &p.Rationale, &evidence, &p.Status, &p.CreatedAt,
		&p.ReviewedAt, &p.RejectionNote,
	)
	if err != nil {
		return nil, err
	}
	if evidence != nil {
		p.Evidence = json.RawMessage(evidence)
	}
	return &p, nil
}

func (s *PostgresStore) Submit(ctx context.Context, p *MissionProposal) error {
	if p.Status == "" {
		p.Status = StatusPending
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO proposals (objective_id, parent_mission, proposed_by, task, contract,
			priority, dedupe_key, rationale, evidence, status, created_at, rejection_note)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)
		 RETURNING id`,
		p.ObjectiveID, p.ParentMission, p.ProposedBy, p.Task, p.Contract,
		p.Priority, p.DedupeKey, p.Rationale, p.Evidence, p.Status, p.CreatedAt,
		p.RejectionNote).Scan(&p.ID)
	if err != nil {
		return fmt.Errorf("proposal pgstore: submit: %w", err)
	}
	return nil
}

func (s *PostgresStore) PendingProposals(ctx context.Context) ([]*MissionProposal, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+proposalCols+` FROM proposals WHERE status = 'pending'
		 ORDER BY priority DESC, created_at ASC`)
	if err != nil {
		return nil, fmt.Errorf("proposal pgstore: pending: %w", err)
	}
	defer rows.Close()

	var result []*MissionProposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, fmt.Errorf("proposal pgstore: scan: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *PostgresStore) Approve(ctx context.Context, id int64) error {
	now := time.Now()
	tag, err := s.pool.Exec(ctx,
		`UPDATE proposals SET status = 'approved', reviewed_at = $1 WHERE id = $2`,
		now, id)
	if err != nil {
		return fmt.Errorf("proposal pgstore: approve: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) Reject(ctx context.Context, id int64, note string) error {
	now := time.Now()
	tag, err := s.pool.Exec(ctx,
		`UPDATE proposals SET status = 'rejected', reviewed_at = $1, rejection_note = $2 WHERE id = $3`,
		now, note, id)
	if err != nil {
		return fmt.Errorf("proposal pgstore: reject: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) UpdateStatus(ctx context.Context, id int64, status string) error {
	now := time.Now()
	tag, err := s.pool.Exec(ctx,
		`UPDATE proposals SET status = $1, reviewed_at = $2 WHERE id = $3`,
		status, now, id)
	if err != nil {
		return fmt.Errorf("proposal pgstore: update status: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PostgresStore) PendingForObjective(ctx context.Context, objectiveID string) ([]*MissionProposal, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+proposalCols+` FROM proposals
		 WHERE status = 'pending' AND objective_id = $1
		 ORDER BY priority DESC, created_at ASC`, objectiveID)
	if err != nil {
		return nil, fmt.Errorf("proposal pgstore: pending for objective: %w", err)
	}
	defer rows.Close()

	var result []*MissionProposal
	for rows.Next() {
		p, err := scanProposal(rows)
		if err != nil {
			return nil, fmt.Errorf("proposal pgstore: scan: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (s *PostgresStore) RejectAllPending(ctx context.Context, objectiveID string, note string) (int, error) {
	now := time.Now()
	tag, err := s.pool.Exec(ctx,
		`UPDATE proposals SET status = 'rejected', reviewed_at = $1, rejection_note = $2
		 WHERE objective_id = $3 AND status = 'pending'`,
		now, note, objectiveID)
	if err != nil {
		return 0, fmt.Errorf("proposal pgstore: reject all pending: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

var _ ProposalStore = (*PostgresStore)(nil)
