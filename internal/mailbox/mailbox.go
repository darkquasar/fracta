package mailbox

import (
	"context"
	"time"
)

// Message represents an inter-agent message.
type Message struct {
	From      string    `json:"from"`
	To        string    `json:"to"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	Read      bool      `json:"read"`
}

// Mailbox is the interface for inter-agent message storage.
type Mailbox interface {
	Send(ctx context.Context, from, to, content string) error
	Read(ctx context.Context, task string) ([]Message, error)
	UnreadCount(ctx context.Context, task string) (int, error)
	Remove(ctx context.Context, task string) error
}
