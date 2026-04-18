package sqlitestore

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"

	"github.com/darkquasar/fracta/internal/fractalog"
	"time"

	"github.com/darkquasar/fracta/internal/mailbox"
)

// SQLiteMailbox implements mailbox.Mailbox using SQLite tables.
type SQLiteMailbox struct {
	db     *sql.DB
	logger *slog.Logger
}

// newSQLiteMailbox creates a SQLiteMailbox backed by the given database.
// The database must already have the messages and cursors tables (created
// by SQLiteStore schema migration).
func newSQLiteMailbox(db *sql.DB) *SQLiteMailbox {
	return &SQLiteMailbox{db: db, logger: fractalog.Component("mailbox")}
}

func (m *SQLiteMailbox) Send(ctx context.Context, from, to, content string) error {
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO messages (from_task, to_task, content, timestamp) VALUES (?, ?, ?, ?)`,
		from, to, content, time.Now().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("sqlitemailbox: send: %w", err)
	}
	return nil
}

// Read returns unread messages for a task. Uses BEGIN IMMEDIATE to serialize
// concurrent readers — prevents two readers from both seeing the same cursor
// value and returning duplicate messages.
func (m *SQLiteMailbox) Read(ctx context.Context, task string) ([]mailbox.Message, error) {
	// Use a dedicated connection so BEGIN IMMEDIATE / COMMIT are on the same conn.
	conn, err := m.db.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("sqlitemailbox: read: conn: %w", err)
	}
	defer conn.Close()

	// BEGIN IMMEDIATE acquires a RESERVED lock immediately, serializing
	// concurrent Read() calls. A deferred BEGIN would allow both readers
	// to read the cursor before either writes the update.
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, fmt.Errorf("sqlitemailbox: read: begin immediate: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			conn.ExecContext(ctx, "ROLLBACK")
		}
	}()

	// Get cursor (last read message ID).
	var lastID int64
	err = conn.QueryRowContext(ctx, `SELECT last_id FROM cursors WHERE task = ?`, task).Scan(&lastID)
	if err != nil && err != sql.ErrNoRows {
		return nil, fmt.Errorf("sqlitemailbox: read cursor: %w", err)
	}

	rows, err := conn.QueryContext(ctx,
		`SELECT id, from_task, to_task, content, timestamp FROM messages WHERE to_task = ? AND id > ? ORDER BY id`,
		task, lastID,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlitemailbox: read messages: %w", err)
	}
	defer rows.Close()

	var messages []mailbox.Message
	var maxID int64
	for rows.Next() {
		var id int64
		var msg mailbox.Message
		var ts string
		if err := rows.Scan(&id, &msg.From, &msg.To, &msg.Content, &ts); err != nil {
			return nil, fmt.Errorf("sqlitemailbox: scan message: %w", err)
		}
		msg.Timestamp, _ = time.Parse(time.RFC3339Nano, ts)
		messages = append(messages, msg)
		if id > maxID {
			maxID = id
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitemailbox: iterate messages: %w", err)
	}

	// Advance cursor and delete consumed messages atomically with the read.
	// Inbox semantics: read = consumed. Old messages are not kept.
	if maxID > lastID {
		_, err = conn.ExecContext(ctx,
			`INSERT INTO cursors (task, last_id) VALUES (?, ?) ON CONFLICT(task) DO UPDATE SET last_id = excluded.last_id`,
			task, maxID,
		)
		if err != nil {
			return nil, fmt.Errorf("sqlitemailbox: advance cursor: %w", err)
		}
		// Delete consumed messages to prevent unbounded growth.
		_, err = conn.ExecContext(ctx,
			`DELETE FROM messages WHERE to_task = ? AND id <= ?`,
			task, maxID,
		)
		if err != nil {
			return nil, fmt.Errorf("sqlitemailbox: delete consumed: %w", err)
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, fmt.Errorf("sqlitemailbox: read: commit: %w", err)
	}
	committed = true
	return messages, nil
}

func (m *SQLiteMailbox) UnreadCount(ctx context.Context, task string) (int, error) {
	var lastID int64
	if err := m.db.QueryRowContext(ctx, `SELECT last_id FROM cursors WHERE task = ?`, task).Scan(&lastID); err != nil && err != sql.ErrNoRows {
		return 0, fmt.Errorf("sqlitemailbox: unread count cursor: %w", err)
	}

	var count int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM messages WHERE to_task = ? AND id > ?`,
		task, lastID,
	).Scan(&count); err != nil {
		return 0, fmt.Errorf("sqlitemailbox: unread count: %w", err)
	}

	return count, nil
}

func (m *SQLiteMailbox) Remove(ctx context.Context, task string) error {
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitemailbox: remove: begin: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM messages WHERE to_task = ?`, task); err != nil {
		return fmt.Errorf("sqlitemailbox: remove messages: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cursors WHERE task = ?`, task); err != nil {
		return fmt.Errorf("sqlitemailbox: remove cursor: %w", err)
	}

	return tx.Commit()
}

// Compile-time interface check.
var _ mailbox.Mailbox = (*SQLiteMailbox)(nil)
