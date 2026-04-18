package sqlitestore

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func newTestMailbox(t *testing.T) *SQLiteMailbox {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	store, err := New(dbPath)
	if err != nil {
		t.Fatalf("creating store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store.mailbox
}

func TestSQLiteMailbox_SendAndRead(t *testing.T) {
	mb := newTestMailbox(t)
	ctx := context.Background()

	if err := mb.Send(ctx, "alice", "bob", "hello"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if err := mb.Send(ctx, "alice", "bob", "world"); err != nil {
		t.Fatalf("send: %v", err)
	}

	msgs, err := mb.Read(ctx, "bob")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].From != "alice" || msgs[0].Content != "hello" {
		t.Errorf("unexpected first message: %+v", msgs[0])
	}
	if msgs[1].Content != "world" {
		t.Errorf("unexpected second message: %+v", msgs[1])
	}

	// Reading again should return nothing (cursor advanced)
	msgs, err = mb.Read(ctx, "bob")
	if err != nil {
		t.Fatalf("read again: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages after re-read, got %d", len(msgs))
	}
}

func TestSQLiteMailbox_UnreadCount(t *testing.T) {
	mb := newTestMailbox(t)
	ctx := context.Background()

	count, err := mb.UnreadCount(ctx, "bob")
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 unread, got %d", count)
	}

	mb.Send(ctx, "alice", "bob", "msg1")
	mb.Send(ctx, "alice", "bob", "msg2")
	mb.Send(ctx, "alice", "bob", "msg3")

	count, err = mb.UnreadCount(ctx, "bob")
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if count != 3 {
		t.Fatalf("expected 3 unread, got %d", count)
	}

	// Read advances cursor
	mb.Read(ctx, "bob")

	count, err = mb.UnreadCount(ctx, "bob")
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 unread after read, got %d", count)
	}

	// New message after read
	mb.Send(ctx, "alice", "bob", "msg4")
	count, err = mb.UnreadCount(ctx, "bob")
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 unread after new send, got %d", count)
	}
}

func TestSQLiteMailbox_Remove(t *testing.T) {
	mb := newTestMailbox(t)
	ctx := context.Background()

	mb.Send(ctx, "alice", "bob", "msg1")
	mb.Send(ctx, "alice", "bob", "msg2")
	mb.Read(ctx, "bob") // advance cursor

	mb.Send(ctx, "alice", "bob", "msg3")

	if err := mb.Remove(ctx, "bob"); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// After remove, unread count should be 0
	count, err := mb.UnreadCount(ctx, "bob")
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 unread after remove, got %d", count)
	}

	// Read should return nothing
	msgs, err := mb.Read(ctx, "bob")
	if err != nil {
		t.Fatalf("read after remove: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages after remove, got %d", len(msgs))
	}
}

func TestSQLiteMailbox_IsolatedRecipients(t *testing.T) {
	mb := newTestMailbox(t)
	ctx := context.Background()

	mb.Send(ctx, "alice", "bob", "for bob")
	mb.Send(ctx, "alice", "carol", "for carol")

	bobMsgs, _ := mb.Read(ctx, "bob")
	carolMsgs, _ := mb.Read(ctx, "carol")

	if len(bobMsgs) != 1 || bobMsgs[0].Content != "for bob" {
		t.Errorf("bob got unexpected messages: %+v", bobMsgs)
	}
	if len(carolMsgs) != 1 || carolMsgs[0].Content != "for carol" {
		t.Errorf("carol got unexpected messages: %+v", carolMsgs)
	}
}

func TestSQLiteMailbox_ReadEmptyMailbox(t *testing.T) {
	mb := newTestMailbox(t)
	ctx := context.Background()

	msgs, err := mb.Read(ctx, "nobody")
	if err != nil {
		t.Fatalf("read empty: %v", err)
	}
	if len(msgs) != 0 {
		t.Fatalf("expected 0 messages for empty mailbox, got %d", len(msgs))
	}
}

func TestSQLiteMailbox_ConcurrentReads_DisjointMessages(t *testing.T) {
	mb := newTestMailbox(t)
	ctx := context.Background()

	// Send 10 messages to "shared"
	for i := 0; i < 10; i++ {
		if err := mb.Send(ctx, "alice", "shared", fmt.Sprintf("msg-%d", i)); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	// Two concurrent readers for the same task. With BEGIN IMMEDIATE,
	// they should get disjoint message sets (no duplicates).
	var wg sync.WaitGroup
	results := make([][]string, 2)
	errs := make([]error, 2)

	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(idx int) {
			defer wg.Done()
			msgs, err := mb.Read(ctx, "shared")
			errs[idx] = err
			for _, m := range msgs {
				results[idx] = append(results[idx], m.Content)
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("reader %d error: %v", i, err)
		}
	}

	// The two readers should have gotten all 10 messages between them,
	// with no overlap.
	total := len(results[0]) + len(results[1])
	if total != 10 {
		t.Fatalf("expected 10 total messages across readers, got %d (reader0=%d, reader1=%d)",
			total, len(results[0]), len(results[1]))
	}

	// Check no duplicates
	seen := make(map[string]bool)
	for _, set := range results {
		for _, msg := range set {
			if seen[msg] {
				t.Fatalf("duplicate message %q across readers", msg)
			}
			seen[msg] = true
		}
	}
}

func TestSQLiteMailbox_ConcurrentAccess(t *testing.T) {
	mb := newTestMailbox(t)
	ctx := context.Background()

	const numSenders = 10
	const msgsPerSender = 20

	var wg sync.WaitGroup
	wg.Add(numSenders)
	for i := 0; i < numSenders; i++ {
		go func(sender int) {
			defer wg.Done()
			for j := 0; j < msgsPerSender; j++ {
				if err := mb.Send(ctx, "sender", "target", "msg"); err != nil {
					t.Errorf("concurrent send from sender %d msg %d: %v", sender, j, err)
				}
			}
		}(i)
	}
	wg.Wait()

	// All messages should be readable
	totalExpected := numSenders * msgsPerSender
	count, err := mb.UnreadCount(ctx, "target")
	if err != nil {
		t.Fatalf("unread count: %v", err)
	}
	if count != totalExpected {
		t.Fatalf("expected %d unread, got %d", totalExpected, count)
	}

	msgs, err := mb.Read(ctx, "target")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(msgs) != totalExpected {
		t.Fatalf("expected %d messages, got %d", totalExpected, len(msgs))
	}
}
