package mcpserver

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/state"
	"github.com/mark3labs/mcp-go/mcp"
)

// --- Mock store and mailbox for agent tool tests ---

type mockStore struct {
	agents []model.AgentEntry
	mb     mailbox.Mailbox
}

func (m *mockStore) Load(_ context.Context) (model.State, error) {
	return model.State{Agents: m.agents}, nil
}
func (m *mockStore) WithLock(_ context.Context, fn func(*model.State) error) error {
	st := model.State{Agents: m.agents}
	return fn(&st)
}
func (m *mockStore) FindAgent(_ context.Context, task string) (*model.AgentEntry, error) {
	for i := range m.agents {
		if m.agents[i].Task == task {
			return &m.agents[i], nil
		}
	}
	return nil, nil
}
func (m *mockStore) RemoveAgent(_ context.Context, _ string) error { return nil }
func (m *mockStore) UpdateAgentStatus(_ context.Context, _ string, _ model.AgentStatus, _ string) error {
	return nil
}
func (m *mockStore) UpdateAgentResult(_ context.Context, _ string, _ model.AgentStatus, _, _ string) error {
	return nil
}
func (m *mockStore) UpdateAgentIntent(_ context.Context, task, intent string) error {
	for i := range m.agents {
		if m.agents[i].Task == task {
			m.agents[i].CurrentIntent = intent
			return nil
		}
	}
	return nil
}
func (m *mockStore) ClaimAgent(_ context.Context, _ string) error                       { return nil }
func (m *mockStore) UpdateAgentStatusIf(_ context.Context, _ string, _ []model.AgentStatus, _ model.AgentStatus, _ string) (bool, error) {
	return true, nil
}
func (m *mockStore) UpdateAgentResultIf(_ context.Context, _ string, _ []model.AgentStatus, _ model.AgentStatus, _, _ string) (bool, error) {
	return true, nil
}
func (m *mockStore) UpdateChessmaster(_ context.Context, _, _ string, _ time.Time) error { return nil }
func (m *mockStore) Mailbox() mailbox.Mailbox                                           { return m.mb }
func (m *mockStore) Close() error                                                       { return nil }

var _ state.Store = (*mockStore)(nil)

type mockMailbox struct {
	messages []mailbox.Message
}

func (m *mockMailbox) Send(_ context.Context, from, to, content string) error {
	m.messages = append(m.messages, mailbox.Message{From: from, To: to, Content: content})
	return nil
}
func (m *mockMailbox) Read(_ context.Context, task string) ([]mailbox.Message, error) {
	var result []mailbox.Message
	for _, msg := range m.messages {
		if msg.To == task {
			result = append(result, msg)
		}
	}
	return result, nil
}
func (m *mockMailbox) UnreadCount(_ context.Context, _ string) (int, error) { return 0, nil }
func (m *mockMailbox) Remove(_ context.Context, _ string) error             { return nil }

var _ mailbox.Mailbox = (*mockMailbox)(nil)

// --- Tests ---

func TestGatewayServer_ToolSurface(t *testing.T) {
	store := &mockStore{}
	mb := &mockMailbox{}

	gs := NewGatewayServer("",
		WithGatewayStore(store),
		WithGatewayMailbox(mb),
	)

	tools := gs.MCPServer().ListTools()

	// Agent tools must be present
	agentTools := []string{"fracta_list", "fracta_peek", "fracta_send", "fracta_inbox", "fracta_set_intent"}
	for _, name := range agentTools {
		if _, ok := tools[name]; !ok {
			t.Errorf("expected agent tool %q to be registered on GatewayServer", name)
		}
	}
}

func TestGatewayServer_NoAdminTools(t *testing.T) {
	store := &mockStore{}
	mb := &mockMailbox{}

	gs := NewGatewayServer("",
		WithGatewayStore(store),
		WithGatewayMailbox(mb),
	)

	tools := gs.MCPServer().ListTools()

	// Admin tools must NOT be present
	adminTools := []string{"fracta_init", "fracta_spawn", "fracta_kill", "fracta_merge", "fracta_say"}
	for _, name := range adminTools {
		if _, ok := tools[name]; ok {
			t.Errorf("admin tool %q should NOT be registered on GatewayServer", name)
		}
	}
}

func TestGatewayServer_WithGraphTools(t *testing.T) {
	store := &mockStore{}
	mb := &mockMailbox{}
	gc := &mockGraphClient{}

	gs := NewGatewayServer("",
		WithGatewayStore(store),
		WithGatewayMailbox(mb),
		WithGatewayGraphClient(gc),
	)

	tools := gs.MCPServer().ListTools()

	graphTools := []string{"graph_query", "graph_update", "graph_schema", "graph_path", "graph_neighbors", "graph_checkpoint"}
	for _, name := range graphTools {
		if _, ok := tools[name]; !ok {
			t.Errorf("expected graph tool %q to be registered on GatewayServer", name)
		}
	}
}

func TestAgentIdentityEnforced_Send(t *testing.T) {
	store := &mockStore{
		agents: []model.AgentEntry{
			{Task: "agent-a", Status: model.StatusRunning},
			{Task: "agent-b", Status: model.StatusRunning},
		},
	}
	mb := &mockMailbox{}

	handler := makeAgentSendHandler(store, mb)

	// Simulate gateway mode: context carries agent identity
	ctx := context.WithValue(context.Background(), agentTaskKey{}, "agent-a")
	reqJSON := `{"method":"tools/call","params":{"name":"fracta_send","arguments":{
		"from": "spoofed-name",
		"to": "agent-b",
		"message": "hello"
	}}}`
	var req mcp.CallToolRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	// Should succeed
	text := result.Content[0].(mcp.TextContent).Text
	if text == "" {
		t.Fatal("expected non-empty result")
	}

	// Verify the from was enforced to "agent-a", not "spoofed-name"
	if len(mb.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mb.messages))
	}
	if mb.messages[0].From != "agent-a" {
		t.Errorf("from = %q, want %q (identity should be enforced)", mb.messages[0].From, "agent-a")
	}
}

func TestAgentIdentityEnforced_Inbox(t *testing.T) {
	store := &mockStore{
		agents: []model.AgentEntry{
			{Task: "agent-x", Status: model.StatusRunning},
		},
	}
	// Pre-load a message to agent-x
	mb := &mockMailbox{
		messages: []mailbox.Message{
			{From: "chessmaster", To: "agent-x", Content: "update ready"},
		},
	}

	handler := makeAgentInboxHandler(store, mb)

	// Simulate gateway mode: context carries agent identity
	ctx := context.WithValue(context.Background(), agentTaskKey{}, "agent-x")
	// Caller tries to read a different agent's inbox
	reqJSON := `{"method":"tools/call","params":{"name":"fracta_inbox","arguments":{
		"name": "someone-else"
	}}}`
	var req mcp.CallToolRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	// Should return agent-x's messages, not someone-else's
	text := result.Content[0].(mcp.TextContent).Text
	if text == "" {
		t.Fatal("expected non-empty result")
	}

	// Verify it returned the message for agent-x (the enforced identity)
	var messages []mailbox.Message
	if err := json.Unmarshal([]byte(text), &messages); err != nil {
		t.Fatalf("unmarshal messages: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(messages))
	}
	if messages[0].Content != "update ready" {
		t.Errorf("content = %q, want %q", messages[0].Content, "update ready")
	}
}

func TestAgentIdentityPreserved_Stdio(t *testing.T) {
	store := &mockStore{
		agents: []model.AgentEntry{
			{Task: "my-agent", Status: model.StatusRunning},
			{Task: "peer", Status: model.StatusRunning},
		},
	}
	mb := &mockMailbox{}

	handler := makeAgentSendHandler(store, mb)

	// Stdio mode: no agent identity in context
	ctx := context.Background()
	reqJSON := `{"method":"tools/call","params":{"name":"fracta_send","arguments":{
		"from": "my-agent",
		"to": "peer",
		"message": "stdio message"
	}}}`
	var req mcp.CallToolRequest
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	result, err := handler(ctx, req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	text := result.Content[0].(mcp.TextContent).Text
	if text == "" {
		t.Fatal("expected non-empty result")
	}

	// In stdio mode, caller-supplied "from" should be preserved
	if len(mb.messages) != 1 {
		t.Fatalf("expected 1 message, got %d", len(mb.messages))
	}
	if mb.messages[0].From != "my-agent" {
		t.Errorf("from = %q, want %q (should preserve caller-supplied from in stdio mode)", mb.messages[0].From, "my-agent")
	}
}
