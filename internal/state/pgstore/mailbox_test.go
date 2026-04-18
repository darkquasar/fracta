//go:build postgres

package pgstore_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMailbox_SendAndRead(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	mb := store.Mailbox()

	require.NoError(t, mb.Send(ctx, "alice", "bob", "hello bob"))
	require.NoError(t, mb.Send(ctx, "alice", "bob", "second msg"))

	msgs, err := mb.Read(ctx, "bob")
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	assert.Equal(t, "hello bob", msgs[0].Content)
	assert.Equal(t, "second msg", msgs[1].Content)
	assert.Equal(t, "alice", msgs[0].From)
	assert.Equal(t, "bob", msgs[0].To)
	assert.False(t, msgs[0].Timestamp.IsZero())

	// Second read should return empty — messages consumed.
	msgs, err = mb.Read(ctx, "bob")
	require.NoError(t, err)
	assert.Empty(t, msgs)
}

func TestMailbox_UnreadCount(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	mb := store.Mailbox()

	count, err := mb.UnreadCount(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, 0, count)

	require.NoError(t, mb.Send(ctx, "alice", "bob", "msg1"))
	require.NoError(t, mb.Send(ctx, "alice", "bob", "msg2"))

	count, err = mb.UnreadCount(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, 2, count)

	// Read consumes messages.
	_, err = mb.Read(ctx, "bob")
	require.NoError(t, err)

	count, err = mb.UnreadCount(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestMailbox_Remove(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	mb := store.Mailbox()

	require.NoError(t, mb.Send(ctx, "alice", "bob", "msg"))
	require.NoError(t, mb.Remove(ctx, "bob"))

	count, err := mb.UnreadCount(ctx, "bob")
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestMailbox_IsolatedAgents(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	mb := store.Mailbox()

	require.NoError(t, mb.Send(ctx, "alice", "bob", "for bob"))
	require.NoError(t, mb.Send(ctx, "alice", "carol", "for carol"))

	bobMsgs, err := mb.Read(ctx, "bob")
	require.NoError(t, err)
	assert.Len(t, bobMsgs, 1)
	assert.Equal(t, "for bob", bobMsgs[0].Content)

	carolMsgs, err := mb.Read(ctx, "carol")
	require.NoError(t, err)
	assert.Len(t, carolMsgs, 1)
	assert.Equal(t, "for carol", carolMsgs[0].Content)
}

func TestMailbox_ReadEmptyInbox(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	mb := store.Mailbox()

	msgs, err := mb.Read(ctx, "nobody")
	require.NoError(t, err)
	assert.Empty(t, msgs)
}

func TestMailbox_NewMessageAfterRead(t *testing.T) {
	store := setupStore(t)
	ctx := context.Background()
	mb := store.Mailbox()

	require.NoError(t, mb.Send(ctx, "a", "b", "first"))
	_, err := mb.Read(ctx, "b")
	require.NoError(t, err)

	require.NoError(t, mb.Send(ctx, "a", "b", "second"))
	msgs, err := mb.Read(ctx, "b")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, "second", msgs[0].Content)
}
