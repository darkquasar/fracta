package pgstore

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresMailbox implements mailbox.Mailbox backed by PostgreSQL.
type PostgresMailbox struct {
	pool *pgxpool.Pool
}

// Send inserts a message into the messages table.
func (m *PostgresMailbox) Send(ctx context.Context, from, to, content string) error {
	_, err := m.pool.Exec(ctx,
		"INSERT INTO messages (from_task, to_task, content, timestamp) VALUES ($1, $2, $3, $4)",
		from, to, content, time.Now())
	if err != nil {
		return fmt.Errorf("pgmailbox: send: %w", err)
	}
	return nil
}

// Read returns unread messages for a task and consumes them.
// Uses SELECT FOR UPDATE on the cursor row for per-agent serialization.
func (m *PostgresMailbox) Read(ctx context.Context, task string) ([]mailbox.Message, error) {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("pgmailbox: read: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	// Lock the cursor row to serialize concurrent readers for this task.
	var lastID int64
	err = tx.QueryRow(ctx,
		"SELECT last_id FROM cursors WHERE task = $1 FOR UPDATE", task).Scan(&lastID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, fmt.Errorf("pgmailbox: read cursor: %w", err)
	}

	rows, err := tx.Query(ctx,
		"SELECT id, from_task, to_task, content, timestamp FROM messages WHERE to_task = $1 AND id > $2 ORDER BY id",
		task, lastID)
	if err != nil {
		return nil, fmt.Errorf("pgmailbox: read messages: %w", err)
	}
	defer rows.Close()

	var messages []mailbox.Message
	var maxID int64
	for rows.Next() {
		var id int64
		var msg mailbox.Message
		if err := rows.Scan(&id, &msg.From, &msg.To, &msg.Content, &msg.Timestamp); err != nil {
			return nil, fmt.Errorf("pgmailbox: scan message: %w", err)
		}
		messages = append(messages, msg)
		if id > maxID {
			maxID = id
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgmailbox: iterate messages: %w", err)
	}

	// Advance cursor and delete consumed messages atomically.
	if maxID > lastID {
		_, err = tx.Exec(ctx,
			`INSERT INTO cursors (task, last_id) VALUES ($1, $2)
			 ON CONFLICT (task) DO UPDATE SET last_id = $2`,
			task, maxID)
		if err != nil {
			return nil, fmt.Errorf("pgmailbox: advance cursor: %w", err)
		}

		_, err = tx.Exec(ctx,
			"DELETE FROM messages WHERE to_task = $1 AND id <= $2",
			task, maxID)
		if err != nil {
			return nil, fmt.Errorf("pgmailbox: delete consumed: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("pgmailbox: read: commit: %w", err)
	}
	return messages, nil
}

// UnreadCount returns the number of unread messages for a task.
func (m *PostgresMailbox) UnreadCount(ctx context.Context, task string) (int, error) {
	var lastID int64
	err := m.pool.QueryRow(ctx,
		"SELECT last_id FROM cursors WHERE task = $1", task).Scan(&lastID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("pgmailbox: unread count cursor: %w", err)
	}

	var count int
	if err := m.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM messages WHERE to_task = $1 AND id > $2",
		task, lastID).Scan(&count); err != nil {
		return 0, fmt.Errorf("pgmailbox: unread count: %w", err)
	}
	return count, nil
}

// Remove deletes all messages and cursor for a task.
func (m *PostgresMailbox) Remove(ctx context.Context, task string) error {
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("pgmailbox: remove: begin: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, "DELETE FROM messages WHERE to_task = $1", task); err != nil {
		return fmt.Errorf("pgmailbox: remove messages: %w", err)
	}
	if _, err := tx.Exec(ctx, "DELETE FROM cursors WHERE task = $1", task); err != nil {
		return fmt.Errorf("pgmailbox: remove cursor: %w", err)
	}

	return tx.Commit(ctx)
}

// Compile-time interface check.
var _ mailbox.Mailbox = (*PostgresMailbox)(nil)
