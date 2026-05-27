package admission

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/proposal"
	"github.com/darkquasar/fracta/internal/queue"
)

// --- Mock stores ---

type mockObjectiveStore struct {
	mu         sync.Mutex
	objectives map[string]*objective.Objective
}

func newMockObjectiveStore() *mockObjectiveStore {
	return &mockObjectiveStore{objectives: make(map[string]*objective.Objective)}
}

func (s *mockObjectiveStore) Create(_ context.Context, o *objective.Objective) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if o.Status == "" {
		o.Status = objective.StatusOpen
	}
	o.ApplyDefaults()
	now := time.Now()
	if o.CreatedAt.IsZero() {
		o.CreatedAt = now
	}
	if o.UpdatedAt.IsZero() {
		o.UpdatedAt = now
	}
	s.objectives[o.ID] = o
	return nil
}

func (s *mockObjectiveStore) Get(_ context.Context, id string) (*objective.Objective, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objectives[id]
	if !ok {
		return nil, objective.ErrNotFound
	}
	cp := *o
	return &cp, nil
}

func (s *mockObjectiveStore) Update(_ context.Context, o *objective.Objective) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.objectives[o.ID]; !ok {
		return objective.ErrNotFound
	}
	o.UpdatedAt = time.Now()
	s.objectives[o.ID] = o
	return nil
}

func (s *mockObjectiveStore) IncrementMissionCount(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objectives[id]
	if !ok {
		return objective.ErrNotFound
	}
	o.MissionCount++
	return nil
}

func (s *mockObjectiveStore) IncrementFindingCount(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.objectives[id]
	if !ok {
		return objective.ErrNotFound
	}
	o.FindingCount++
	return nil
}

func (s *mockObjectiveStore) ListByStatus(_ context.Context, status objective.ObjectiveStatus) ([]*objective.Objective, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []*objective.Objective
	for _, o := range s.objectives {
		if o.Status == status {
			cp := *o
			result = append(result, &cp)
		}
	}
	return result, nil
}

type mockProposalStore struct {
	mu        sync.Mutex
	proposals []*proposal.MissionProposal
	nextID    int64
}

func newMockProposalStore() *mockProposalStore {
	return &mockProposalStore{}
}

func (s *mockProposalStore) Submit(_ context.Context, p *proposal.MissionProposal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	p.ID = s.nextID
	if p.Status == "" {
		p.Status = proposal.StatusPending
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now()
	}
	cp := *p
	s.proposals = append(s.proposals, &cp)
	return nil
}

func (s *mockProposalStore) PendingProposals(_ context.Context) ([]*proposal.MissionProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []*proposal.MissionProposal
	for _, p := range s.proposals {
		if p.Status == proposal.StatusPending {
			cp := *p
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (s *mockProposalStore) Approve(_ context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.proposals {
		if p.ID == id {
			p.Status = proposal.StatusApproved
			now := time.Now()
			p.ReviewedAt = &now
			return nil
		}
	}
	return proposal.ErrNotFound
}

func (s *mockProposalStore) Reject(_ context.Context, id int64, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.proposals {
		if p.ID == id {
			p.Status = proposal.StatusRejected
			p.RejectionNote = note
			now := time.Now()
			p.ReviewedAt = &now
			return nil
		}
	}
	return proposal.ErrNotFound
}

func (s *mockProposalStore) UpdateStatus(_ context.Context, id int64, status string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.proposals {
		if p.ID == id {
			p.Status = status
			return nil
		}
	}
	return proposal.ErrNotFound
}

func (s *mockProposalStore) PendingForObjective(_ context.Context, objectiveID string) ([]*proposal.MissionProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var result []*proposal.MissionProposal
	for _, p := range s.proposals {
		if p.Status == proposal.StatusPending && p.ObjectiveID == objectiveID {
			cp := *p
			result = append(result, &cp)
		}
	}
	return result, nil
}

func (s *mockProposalStore) RejectAllPending(_ context.Context, objectiveID string, note string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	count := 0
	for _, p := range s.proposals {
		if p.Status == proposal.StatusPending && p.ObjectiveID == objectiveID {
			p.Status = proposal.StatusRejected
			p.RejectionNote = note
			count++
		}
	}
	return count, nil
}

func (s *mockProposalStore) getByID(id int64) *proposal.MissionProposal {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, p := range s.proposals {
		if p.ID == id {
			cp := *p
			return &cp
		}
	}
	return nil
}

type mockMissionReader struct {
	mu       sync.Mutex
	missions map[int64]*queue.Mission
}

func newMockMissionReader() *mockMissionReader {
	return &mockMissionReader{missions: make(map[int64]*queue.Mission)}
}

func (r *mockMissionReader) add(m *queue.Mission) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.missions[m.ID] = m
}

func (r *mockMissionReader) GetMission(_ context.Context, id int64) (*queue.Mission, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	m, ok := r.missions[id]
	if !ok {
		return nil, queue.ErrNotFound
	}
	cp := *m
	return &cp, nil
}

func (r *mockMissionReader) CountActiveChildren(_ context.Context, parentID int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, m := range r.missions {
		if m.ParentID != nil && *m.ParentID == parentID &&
			(m.Status == queue.StatusPending || m.Status == queue.StatusClaimed) {
			count++
		}
	}
	return count, nil
}

func (r *mockMissionReader) AllMissionsTerminal(_ context.Context, objectiveID string) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.missions {
		if m.ObjectiveID == objectiveID &&
			(m.Status == queue.StatusPending || m.Status == queue.StatusClaimed) {
			return false, nil
		}
	}
	return true, nil
}

type mockQueue struct {
	mu       sync.Mutex
	missions []*queue.Mission
	nextID   int64
}

func newMockQueue() *mockQueue {
	return &mockQueue{}
}

func (q *mockQueue) Enqueue(_ context.Context, m *queue.Mission, _ *model.AgentEntry) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.nextID++
	m.ID = q.nextID
	m.Status = queue.StatusPending
	m.CreatedAt = time.Now()
	cp := *m
	q.missions = append(q.missions, &cp)
	return nil
}

func (q *mockQueue) Dequeue(_ context.Context) (*queue.Mission, error) { return nil, nil }
func (q *mockQueue) Ack(_ context.Context, _ int64) error              { return nil }
func (q *mockQueue) Fail(_ context.Context, _ int64, _ string) error   { return nil }
func (q *mockQueue) Len(_ context.Context) (int, error)                { return 0, nil }
func (q *mockQueue) Status(_ context.Context, _ int64) (string, error) { return "", nil }
func (q *mockQueue) Cancel(_ context.Context, _ int64) error           { return nil }
func (q *mockQueue) Close() error                                      { return nil }

func (q *mockQueue) enqueuedCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.missions)
}

type mockMailbox struct {
	mu       sync.Mutex
	messages []mailbox.Message
}

func newMockMailbox() *mockMailbox { return &mockMailbox{} }

func (m *mockMailbox) Send(_ context.Context, from, to, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.messages = append(m.messages, mailbox.Message{From: from, To: to, Content: content, Timestamp: time.Now()})
	return nil
}

func (m *mockMailbox) Read(_ context.Context, _ string) ([]mailbox.Message, error) {
	return nil, nil
}

func (m *mockMailbox) UnreadCount(_ context.Context, _ string) (int, error) { return 0, nil }
func (m *mockMailbox) Remove(_ context.Context, _ string) error             { return nil }

func (m *mockMailbox) messagesTo(to string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []string
	for _, msg := range m.messages {
		if msg.To == to {
			result = append(result, msg.Content)
		}
	}
	return result
}

type testHarness struct {
	objStore  *mockObjectiveStore
	propStore *mockProposalStore
	reader    *mockMissionReader
	queue     *mockQueue
	mailbox   *mockMailbox
	ac        *AdmissionController
}

func setup(t *testing.T) *testHarness {
	t.Helper()
	objStore := newMockObjectiveStore()
	propStore := newMockProposalStore()
	reader := newMockMissionReader()
	q := newMockQueue()
	mb := newMockMailbox()

	ac := New(propStore, objStore, reader, q, nil, mb)
	return &testHarness{
		objStore:  objStore,
		propStore: propStore,
		reader:    reader,
		queue:     q,
		mailbox:   mb,
		ac:        ac,
	}
}

// --- Policy tests ---

func TestPolicy_RejectClosedObjective(t *testing.T) {
	h := setup(t)
	ctx := context.Background()

	obj := &objective.Objective{ID: "obj-1", Status: objective.StatusExhausted}
	parent := &queue.Mission{ID: 1, Depth: 0}
	prop := &proposal.MissionProposal{ObjectiveID: "obj-1", ParentMission: 1}

	d := h.ac.policy.Evaluate(ctx, prop, obj, parent, h.reader)
	if d.Action != ActionReject {
		t.Errorf("expected reject, got %s", d.Action)
	}
}

func TestPolicy_RejectMissionCountExceeded(t *testing.T) {
	ctx := context.Background()
	reader := newMockMissionReader()
	policy := DefaultPolicy()

	obj := &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen,
		MaxMissions: 5, MissionCount: 5,
		MaxDepth: 10, MaxBranching: 10,
		CreatedAt: time.Now(),
	}
	parent := &queue.Mission{ID: 1, Depth: 0}
	prop := &proposal.MissionProposal{ObjectiveID: "obj-1", ParentMission: 1}

	d := policy.Evaluate(ctx, prop, obj, parent, reader)
	if d.Action != ActionReject {
		t.Errorf("expected reject for mission count, got %s: %s", d.Action, d.Reason)
	}
}

func TestPolicy_RejectDepthExceeded(t *testing.T) {
	ctx := context.Background()
	reader := newMockMissionReader()
	policy := DefaultPolicy()

	obj := &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen,
		MaxMissions: 100, MissionCount: 1,
		MaxDepth: 3, MaxBranching: 10,
		CreatedAt: time.Now(),
	}
	parent := &queue.Mission{ID: 1, Depth: 3} // child would be depth 4 > max 3
	prop := &proposal.MissionProposal{ObjectiveID: "obj-1", ParentMission: 1}

	d := policy.Evaluate(ctx, prop, obj, parent, reader)
	if d.Action != ActionReject {
		t.Errorf("expected reject for depth, got %s: %s", d.Action, d.Reason)
	}
}

func TestPolicy_RejectBranchingExceeded(t *testing.T) {
	ctx := context.Background()
	reader := newMockMissionReader()
	policy := DefaultPolicy()

	parentID := int64(1)
	// Add 5 active children to parent.
	for i := 0; i < 5; i++ {
		reader.add(&queue.Mission{
			ID: int64(100 + i), ParentID: &parentID,
			Status: queue.StatusPending, ObjectiveID: "obj-1",
		})
	}

	obj := &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen,
		MaxMissions: 100, MissionCount: 5,
		MaxDepth: 10, MaxBranching: 5,
		CreatedAt: time.Now(),
	}
	parent := &queue.Mission{ID: 1, Depth: 0}
	prop := &proposal.MissionProposal{ObjectiveID: "obj-1", ParentMission: 1}

	d := policy.Evaluate(ctx, prop, obj, parent, reader)
	if d.Action != ActionReject {
		t.Errorf("expected reject for branching, got %s: %s", d.Action, d.Reason)
	}
}

func TestPolicy_RejectRuntimeExceeded(t *testing.T) {
	ctx := context.Background()
	reader := newMockMissionReader()
	policy := DefaultPolicy()
	policy.Now = func() time.Time { return time.Now().Add(5 * time.Hour) }

	obj := &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen,
		MaxMissions: 100, MissionCount: 1,
		MaxDepth: 10, MaxBranching: 10,
		MaxRuntime: 4 * time.Hour,
		CreatedAt:  time.Now(),
	}
	parent := &queue.Mission{ID: 1, Depth: 0}
	prop := &proposal.MissionProposal{ObjectiveID: "obj-1", ParentMission: 1}

	d := policy.Evaluate(ctx, prop, obj, parent, reader)
	if d.Action != ActionReject {
		t.Errorf("expected reject for runtime, got %s: %s", d.Action, d.Reason)
	}
}

func TestPolicy_RejectEvidenceRequired(t *testing.T) {
	ctx := context.Background()
	reader := newMockMissionReader()
	policy := DefaultPolicy()
	policy.EvidenceThreshold = 3

	obj := &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen,
		MaxMissions: 100, MissionCount: 3, FindingCount: 10,
		MaxDepth: 10, MaxBranching: 10,
		CreatedAt: time.Now(),
	}
	parent := &queue.Mission{ID: 1, Depth: 0}
	prop := &proposal.MissionProposal{
		ObjectiveID: "obj-1", ParentMission: 1,
		Evidence: nil, // no evidence
	}

	d := policy.Evaluate(ctx, prop, obj, parent, reader)
	if d.Action != ActionReject {
		t.Errorf("expected reject for missing evidence, got %s: %s", d.Action, d.Reason)
	}

	// With evidence, should pass.
	prop.Evidence = json.RawMessage(`{"alert":"A-1"}`)
	d = policy.Evaluate(ctx, prop, obj, parent, reader)
	if d.Action != ActionApprove {
		t.Errorf("expected approve with evidence, got %s: %s", d.Action, d.Reason)
	}
}

func TestPolicy_CircuitBreaker(t *testing.T) {
	ctx := context.Background()
	reader := newMockMissionReader()
	policy := DefaultPolicy()
	policy.CircuitBreakerRatio = 3

	obj := &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen,
		MaxMissions: 100, MissionCount: 6, FindingCount: 1,
		MaxDepth: 10, MaxBranching: 10,
		CreatedAt: time.Now(),
	}
	parent := &queue.Mission{ID: 1, Depth: 0}
	prop := &proposal.MissionProposal{
		ObjectiveID: "obj-1", ParentMission: 1,
		Evidence: json.RawMessage(`{"e":1}`),
	}

	// 6 missions, 1 finding, ratio 3:1 → threshold 3. 6 >= 3 → freeze.
	d := policy.Evaluate(ctx, prop, obj, parent, reader)
	if d.Action != ActionFreeze {
		t.Errorf("expected freeze, got %s: %s", d.Action, d.Reason)
	}
}

func TestPolicy_CircuitBreakerNoFindings(t *testing.T) {
	ctx := context.Background()
	reader := newMockMissionReader()
	policy := DefaultPolicy()
	policy.CircuitBreakerRatio = 3

	obj := &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen,
		MaxMissions: 100, MissionCount: 3, FindingCount: 0,
		MaxDepth: 10, MaxBranching: 10,
		CreatedAt: time.Now(),
	}
	parent := &queue.Mission{ID: 1, Depth: 0}
	prop := &proposal.MissionProposal{
		ObjectiveID: "obj-1", ParentMission: 1,
		Evidence: json.RawMessage(`{"e":1}`),
	}

	// 3 missions, 0 findings, threshold=3 (minimum). 3 >= 3 → freeze.
	d := policy.Evaluate(ctx, prop, obj, parent, reader)
	if d.Action != ActionFreeze {
		t.Errorf("expected freeze, got %s: %s", d.Action, d.Reason)
	}
}

func TestPolicy_ApproveHappyPath(t *testing.T) {
	ctx := context.Background()
	reader := newMockMissionReader()
	policy := DefaultPolicy()

	obj := &objective.Objective{
		ID: "obj-1", Status: objective.StatusOpen,
		MaxMissions: 100, MissionCount: 1, FindingCount: 1,
		MaxDepth: 5, MaxBranching: 5,
		CreatedAt: time.Now(),
	}
	parent := &queue.Mission{ID: 1, Depth: 0}
	prop := &proposal.MissionProposal{
		ObjectiveID: "obj-1", ParentMission: 1,
	}

	d := policy.Evaluate(ctx, prop, obj, parent, reader)
	if d.Action != ActionApprove {
		t.Errorf("expected approve, got %s: %s", d.Action, d.Reason)
	}
}

// --- Materialization tests ---

func TestMaterialization(t *testing.T) {
	h := setup(t)
	ctx := context.Background()

	// Create objective.
	obj := &objective.Objective{ID: "obj-mat", Description: "test materialization"}
	h.objStore.Create(ctx, obj)

	// Create parent mission in reader.
	parentPayload := queue.MissionPayload{
		Task:        "root task",
		Model:       "claude-sonnet",
		RuntimeType: "claude",
		Backend:     "memory",
	}
	payloadBytes, _ := json.Marshal(parentPayload)
	parentMission := &queue.Mission{
		ID: 1, Depth: 0, ObjectiveID: "obj-mat",
		Payload: payloadBytes, Status: queue.StatusClaimed,
	}
	h.reader.add(parentMission)

	// Submit proposal.
	prop := &proposal.MissionProposal{
		ObjectiveID:   "obj-mat",
		ParentMission: 1,
		ProposedBy:    "agent-root",
		Task:          "investigate host",
		Contract:      "check C2",
		DedupeKey:     "investigate:host=srv-01",
		Rationale:     "suspicious beacon",
		Priority:      5,
	}
	h.propStore.Submit(ctx, prop)

	// Run one tick.
	h.ac.tick(ctx)

	// Verify proposal approved.
	got := h.propStore.getByID(prop.ID)
	if got.Status != proposal.StatusApproved {
		t.Errorf("proposal status = %q, want approved", got.Status)
	}

	// Verify mission enqueued.
	if h.queue.enqueuedCount() != 1 {
		t.Fatalf("expected 1 enqueued mission, got %d", h.queue.enqueuedCount())
	}

	h.queue.mu.Lock()
	enqueued := h.queue.missions[0]
	h.queue.mu.Unlock()

	if enqueued.ObjectiveID != "obj-mat" {
		t.Errorf("enqueued ObjectiveID = %q, want obj-mat", enqueued.ObjectiveID)
	}
	if enqueued.Depth != 1 {
		t.Errorf("enqueued Depth = %d, want 1", enqueued.Depth)
	}
	if enqueued.ParentID == nil || *enqueued.ParentID != 1 {
		t.Error("enqueued ParentID should be 1")
	}
	if enqueued.DedupeKey != "investigate:host=srv-01" {
		t.Errorf("enqueued DedupeKey = %q", enqueued.DedupeKey)
	}

	// Verify child payload inherits parent config.
	var childPayload queue.MissionPayload
	json.Unmarshal(enqueued.Payload, &childPayload)
	if childPayload.Model != "claude-sonnet" {
		t.Errorf("child model = %q, want claude-sonnet", childPayload.Model)
	}
	if childPayload.RuntimeType != "claude" {
		t.Errorf("child host_type = %q, want claude", childPayload.RuntimeType)
	}
	if childPayload.Task != "investigate host" {
		t.Errorf("child task = %q, want investigate host", childPayload.Task)
	}

	// Verify objective mission count incremented.
	updated, _ := h.objStore.Get(ctx, "obj-mat")
	if updated.MissionCount != 1 {
		t.Errorf("MissionCount = %d, want 1", updated.MissionCount)
	}

	// Verify proposer notified.
	msgs := h.mailbox.messagesTo("agent-root")
	if len(msgs) == 0 {
		t.Error("proposer should have been notified")
	}
}

// --- Auto-transition tests ---

func TestAutoTransition_Exhausted(t *testing.T) {
	h := setup(t)
	ctx := context.Background()

	obj := &objective.Objective{ID: "obj-exh", Description: "test exhaustion"}
	h.objStore.Create(ctx, obj)

	// Increment mission count (simulating that a mission was once active).
	h.objStore.IncrementMissionCount(ctx, "obj-exh")

	// All missions terminal (the reader returns true since there are no non-terminal missions).
	// No pending proposals.

	h.ac.checkConvergence(ctx)

	updated, _ := h.objStore.Get(ctx, "obj-exh")
	if updated.Status != objective.StatusExhausted {
		t.Errorf("status = %s, want exhausted", updated.Status)
	}
}

func TestAutoTransition_NotExhausted_PendingProposals(t *testing.T) {
	h := setup(t)
	ctx := context.Background()

	obj := &objective.Objective{ID: "obj-noex", Description: "not exhausted"}
	h.objStore.Create(ctx, obj)
	h.objStore.IncrementMissionCount(ctx, "obj-noex")

	// Add a pending proposal — should prevent exhaustion.
	h.propStore.Submit(ctx, &proposal.MissionProposal{
		ObjectiveID: "obj-noex", ParentMission: 1, DedupeKey: "test",
		Rationale: "test",
	})

	h.ac.checkConvergence(ctx)

	updated, _ := h.objStore.Get(ctx, "obj-noex")
	if updated.Status != objective.StatusOpen {
		t.Errorf("status = %s, want open (pending proposals exist)", updated.Status)
	}
}

func TestAutoTransition_NotExhausted_ActiveMissions(t *testing.T) {
	h := setup(t)
	ctx := context.Background()

	obj := &objective.Objective{ID: "obj-active", Description: "has active missions"}
	h.objStore.Create(ctx, obj)
	h.objStore.IncrementMissionCount(ctx, "obj-active")

	// Add an active (pending) mission.
	h.reader.add(&queue.Mission{
		ID: 10, ObjectiveID: "obj-active", Status: queue.StatusPending,
	})

	h.ac.checkConvergence(ctx)

	updated, _ := h.objStore.Get(ctx, "obj-active")
	if updated.Status != objective.StatusOpen {
		t.Errorf("status = %s, want open (active missions exist)", updated.Status)
	}
}

func TestAutoTransition_BudgetExhausted(t *testing.T) {
	h := setup(t)
	ctx := context.Background()

	obj := &objective.Objective{
		ID: "obj-budget", Description: "budget test",
		MaxMissions: 5,
	}
	h.objStore.Create(ctx, obj)

	// Set mission count at the cap.
	for i := 0; i < 5; i++ {
		h.objStore.IncrementMissionCount(ctx, "obj-budget")
	}

	h.ac.checkConvergence(ctx)

	updated, _ := h.objStore.Get(ctx, "obj-budget")
	if updated.Status != objective.StatusBudgetExhausted {
		t.Errorf("status = %s, want budget_exhausted", updated.Status)
	}
}

func TestAutoTransition_TimedOut(t *testing.T) {
	h := setup(t)
	ctx := context.Background()

	obj := &objective.Objective{
		ID: "obj-timeout", Description: "timeout test",
		MaxRuntime: 1 * time.Hour,
		CreatedAt:  time.Now().Add(-2 * time.Hour), // created 2 hours ago
	}
	h.objStore.Create(ctx, obj)

	h.ac.checkConvergence(ctx)

	updated, _ := h.objStore.Get(ctx, "obj-timeout")
	if updated.Status != objective.StatusTimedOut {
		t.Errorf("status = %s, want timed_out", updated.Status)
	}
}

// --- Circuit breaker test ---

func TestCircuitBreaker_FreezesObjective(t *testing.T) {
	h := setup(t)
	ctx := context.Background()

	obj := &objective.Objective{
		ID: "obj-cb", Description: "circuit breaker test",
	}
	h.objStore.Create(ctx, obj)

	// 6 missions, 0 findings → circuit breaker with default 3:1 ratio.
	for i := 0; i < 6; i++ {
		h.objStore.IncrementMissionCount(ctx, "obj-cb")
	}

	// Create parent mission.
	parentPayload, _ := json.Marshal(queue.MissionPayload{Task: "root"})
	h.reader.add(&queue.Mission{
		ID: 1, Depth: 0, ObjectiveID: "obj-cb",
		Payload: parentPayload, Status: queue.StatusClaimed,
	})

	// Submit proposal that triggers the circuit breaker.
	h.propStore.Submit(ctx, &proposal.MissionProposal{
		ObjectiveID:   "obj-cb",
		ParentMission: 1,
		ProposedBy:    "agent-1",
		DedupeKey:     "test:cb",
		Rationale:     "test",
		Evidence:      json.RawMessage(`{"e":1}`),
	})

	h.ac.tick(ctx)

	// Verify objective frozen.
	updated, _ := h.objStore.Get(ctx, "obj-cb")
	if updated.Status != objective.StatusFrozen {
		t.Errorf("status = %s, want frozen", updated.Status)
	}

	// Verify chessmaster notified.
	msgs := h.mailbox.messagesTo("chessmaster")
	if len(msgs) == 0 {
		t.Error("chessmaster should have been notified of freeze")
	}
}

// --- Rejection test ---

func TestRejectNotifies(t *testing.T) {
	h := setup(t)
	ctx := context.Background()

	// Create an exhausted objective.
	obj := &objective.Objective{
		ID: "obj-rej", Status: objective.StatusExhausted,
		Description: "done",
	}
	h.objStore.mu.Lock()
	h.objStore.objectives["obj-rej"] = obj
	h.objStore.mu.Unlock()

	// Parent mission.
	h.reader.add(&queue.Mission{ID: 1, Depth: 0, ObjectiveID: "obj-rej"})

	// Submit proposal that should be rejected (objective not open).
	h.propStore.Submit(ctx, &proposal.MissionProposal{
		ObjectiveID:   "obj-rej",
		ParentMission: 1,
		ProposedBy:    "agent-x",
		DedupeKey:     "test:rej",
		Rationale:     "test",
	})

	h.ac.tick(ctx)

	// Verify proposal rejected.
	pending, _ := h.propStore.PendingProposals(ctx)
	if len(pending) != 0 {
		t.Errorf("expected 0 pending after rejection, got %d", len(pending))
	}

	// Verify proposer notified.
	msgs := h.mailbox.messagesTo("agent-x")
	if len(msgs) == 0 {
		t.Error("proposer should have been notified of rejection")
	}
}

// --- GatewayURL inheritance regression test ---

func TestMaterialization_ChildInheritsGatewayURL(t *testing.T) {
	h := setup(t)
	ctx := context.Background()

	obj := &objective.Objective{ID: "obj-gw", Description: "gateway URL test"}
	h.objStore.Create(ctx, obj)

	// Create parent mission WITH GatewayURL set.
	parentPayload := queue.MissionPayload{
		Task:        "root task",
		Model:       "claude-sonnet",
		RuntimeType: "claude",
		Backend:     "kubernetes",
		GatewayURL:  "http://gateway.fracta.svc:8080",
		ConfigHash:  "abc123",
	}
	payloadBytes, _ := json.Marshal(parentPayload)
	parentMission := &queue.Mission{
		ID: 1, Depth: 0, ObjectiveID: "obj-gw",
		Payload: payloadBytes, Status: queue.StatusClaimed,
	}
	h.reader.add(parentMission)

	// Submit proposal.
	prop := &proposal.MissionProposal{
		ObjectiveID:   "obj-gw",
		ParentMission: 1,
		ProposedBy:    "agent-root",
		Task:          "child task",
		Contract:      "investigate",
		DedupeKey:     "test:gw",
		Rationale:     "test gateway inheritance",
	}
	h.propStore.Submit(ctx, prop)

	h.ac.tick(ctx)

	// Verify mission enqueued.
	if h.queue.enqueuedCount() != 1 {
		t.Fatalf("expected 1 enqueued mission, got %d", h.queue.enqueuedCount())
	}

	h.queue.mu.Lock()
	enqueued := h.queue.missions[0]
	h.queue.mu.Unlock()

	var childPayload queue.MissionPayload
	if err := json.Unmarshal(enqueued.Payload, &childPayload); err != nil {
		t.Fatalf("unmarshal child payload: %v", err)
	}

	// This is the regression test: GatewayURL MUST be inherited from parent.
	// Before the ChildSpec fix, GatewayURL was omitted in child payload construction.
	if childPayload.GatewayURL != "http://gateway.fracta.svc:8080" {
		t.Errorf("child GatewayURL = %q, want %q — GatewayURL must be inherited from parent",
			childPayload.GatewayURL, "http://gateway.fracta.svc:8080")
	}

	// Also verify other topology fields are inherited.
	if childPayload.Backend != "kubernetes" {
		t.Errorf("child Backend = %q, want %q", childPayload.Backend, "kubernetes")
	}
	if childPayload.ConfigHash != "abc123" {
		t.Errorf("child ConfigHash = %q, want %q", childPayload.ConfigHash, "abc123")
	}

	// Verify child identity fields are set correctly.
	if childPayload.Task != "child task" {
		t.Errorf("child Task = %q, want %q", childPayload.Task, "child task")
	}
	if childPayload.ObjectiveID != "obj-gw" {
		t.Errorf("child ObjectiveID = %q, want %q", childPayload.ObjectiveID, "obj-gw")
	}
}
