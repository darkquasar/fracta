package orchestrator

import (
	"context"
	"fmt"

	"github.com/darkquasar/fracta/internal/mailbox"
)

// SendMessage validates sender/recipient existence, then delegates to the Mailbox.
func (o *Orchestrator) SendMessage(from, to, content string) error {
	ctx := context.Background()
	if from != "chessmaster" {
		sender, err := o.Store.FindAgent(ctx, from)
		if err != nil {
			return fmt.Errorf("looking up sender: %w", err)
		}
		if sender == nil {
			return fmt.Errorf("sender agent %q not found", from)
		}
	}

	if to != "chessmaster" {
		recipient, err := o.Store.FindAgent(ctx, to)
		if err != nil {
			return fmt.Errorf("looking up recipient: %w", err)
		}
		if recipient == nil {
			return fmt.Errorf("recipient agent %q not found", to)
		}
	}

	return o.Mailbox.Send(ctx, from, to, content)
}

// ReadInbox validates agent existence, then delegates to the Mailbox.
func (o *Orchestrator) ReadInbox(task string) ([]mailbox.Message, error) {
	ctx := context.Background()
	if task != "chessmaster" {
		agent, err := o.Store.FindAgent(ctx, task)
		if err != nil {
			return nil, fmt.Errorf("looking up agent: %w", err)
		}
		if agent == nil {
			return nil, fmt.Errorf("agent %q not found", task)
		}
	}

	return o.Mailbox.Read(ctx, task)
}

// UnreadCount delegates to the Mailbox. Advisory only — errors are suppressed.
func (o *Orchestrator) UnreadCount(task string) int {
	count, _ := o.Mailbox.UnreadCount(context.Background(), task)
	return count
}
