package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/darkquasar/fracta/internal/mailbox"
	"github.com/darkquasar/fracta/internal/model"
	"github.com/darkquasar/fracta/internal/fractalog"
	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/proposal"
	"github.com/darkquasar/fracta/internal/queue"
	"github.com/darkquasar/fracta/internal/state"
)

// DefaultInterval is the default polling interval for the admission controller.
const DefaultInterval = 2 * time.Second

// AdmissionController evaluates pending mission proposals against policy,
// materializes approved proposals into queued missions, and manages
// objective lifecycle transitions.
type AdmissionController struct {
	proposalStore  proposal.ProposalStore
	objectiveStore objective.ObjectiveStore
	missionReader  MissionReader
	queue          queue.MissionQueue
	store          state.Store
	mailbox        mailbox.Mailbox
	policy         *AdmissionPolicy
	interval       time.Duration
	logger         *slog.Logger
}

// Option configures an AdmissionController.
type Option func(*AdmissionController)

// WithInterval sets the polling interval.
func WithInterval(d time.Duration) Option {
	return func(ac *AdmissionController) {
		ac.interval = d
	}
}

// WithPolicy sets a custom admission policy.
func WithPolicy(p *AdmissionPolicy) Option {
	return func(ac *AdmissionController) {
		ac.policy = p
	}
}

// New creates an AdmissionController with the given dependencies.
func New(
	proposalStore proposal.ProposalStore,
	objectiveStore objective.ObjectiveStore,
	missionReader MissionReader,
	q queue.MissionQueue,
	store state.Store,
	mb mailbox.Mailbox,
	opts ...Option,
) *AdmissionController {
	ac := &AdmissionController{
		proposalStore:  proposalStore,
		objectiveStore: objectiveStore,
		missionReader:  missionReader,
		queue:          q,
		store:          store,
		mailbox:        mb,
		policy:         DefaultPolicy(),
		interval:       DefaultInterval,
		logger:         fractalog.Component("admission"),
	}
	for _, opt := range opts {
		opt(ac)
	}
	return ac
}

// Run starts the admission controller loop. It blocks until ctx is cancelled.
func (ac *AdmissionController) Run(ctx context.Context) {
	ac.logger.Info("admission controller started", "interval", ac.interval)
	for {
		select {
		case <-ctx.Done():
			ac.logger.Info("admission controller stopped")
			return
		case <-time.After(ac.interval):
			ac.tick(ctx)
		}
	}
}

// tick processes one cycle: evaluate pending proposals, then check convergence.
func (ac *AdmissionController) tick(ctx context.Context) {
	ac.evaluateProposals(ctx)
	ac.checkConvergence(ctx)
}

// evaluateProposals fetches all pending proposals and evaluates each through the policy.
func (ac *AdmissionController) evaluateProposals(ctx context.Context) {
	proposals, err := ac.proposalStore.PendingProposals(ctx)
	if err != nil {
		ac.logger.Error("failed to fetch pending proposals", "error", err)
		return
	}

	for _, p := range proposals {
		ac.evaluateOne(ctx, p)
	}
}

// evaluateOne runs the policy pipeline on a single proposal.
func (ac *AdmissionController) evaluateOne(ctx context.Context, p *proposal.MissionProposal) {
	obj, err := ac.objectiveStore.Get(ctx, p.ObjectiveID)
	if err != nil {
		ac.logger.Error("failed to get objective for proposal",
			"proposal_id", p.ID,
			"objective_id", p.ObjectiveID,
			"error", err,
		)
		ac.reject(ctx, p, "objective not found")
		return
	}

	parent, err := ac.missionReader.GetMission(ctx, p.ParentMission)
	if err != nil {
		ac.logger.Error("failed to get parent mission for proposal",
			"proposal_id", p.ID,
			"parent_mission", p.ParentMission,
			"error", err,
		)
		ac.reject(ctx, p, "parent mission not found")
		return
	}

	decision := ac.policy.Evaluate(ctx, p, obj, parent, ac.missionReader)
	ac.logger.Info("proposal evaluated",
		"proposal_id", p.ID,
		"objective_id", p.ObjectiveID,
		"dedupe_key", p.DedupeKey,
		"decision", decision.Action,
		"reason", decision.Reason,
	)

	switch decision.Action {
	case ActionApprove:
		ac.materialize(ctx, p, obj, parent)
	case ActionReject:
		ac.reject(ctx, p, decision.Reason)
	case ActionFreeze:
		ac.freezeObjective(ctx, obj, p, decision.Reason)
	}
}

// materialize turns an approved proposal into a queued mission.
func (ac *AdmissionController) materialize(ctx context.Context, p *proposal.MissionProposal, obj *objective.Objective, parent *queue.Mission) {
	// 1. Approve the proposal.
	if err := ac.proposalStore.Approve(ctx, p.ID); err != nil {
		ac.logger.Error("failed to approve proposal", "proposal_id", p.ID, "error", err)
		return
	}

	// 2. Generate task name.
	taskName := fmt.Sprintf("obj-%s-d%d-%d", obj.ID, parent.Depth+1, p.ID)
	if len(taskName) > 64 {
		taskName = taskName[:64]
	}

	// 3. Inherit parent payload config (struct copy inherits ALL fields
	// including GatewayURL, fixing the prior bug where manual field-by-field
	// copy omitted it).
	var parentPayload queue.MissionPayload
	if err := json.Unmarshal(parent.Payload, &parentPayload); err != nil {
		ac.logger.Error("failed to unmarshal parent payload",
			"proposal_id", p.ID,
			"parent_mission", parent.ID,
			"error", err,
		)
		return
	}

	childPayload := childMissionPayload(parentPayload, p.Task, p.Contract, obj.ID)

	payloadBytes, err := json.Marshal(childPayload)
	if err != nil {
		ac.logger.Error("failed to marshal child payload", "proposal_id", p.ID, "error", err)
		return
	}

	// 4. Create Mission with DAG fields.
	depth := parent.Depth + 1
	mission := &queue.Mission{
		AgentTask:   taskName,
		Payload:     payloadBytes,
		Priority:    p.Priority,
		ObjectiveID: obj.ID,
		ParentID:    &parent.ID,
		Depth:       depth,
		DedupeKey:   p.DedupeKey,
		ProposedBy:  p.ProposedBy,
	}

	agent := &model.AgentEntry{
		Task:        taskName,
		RuntimeType: parentPayload.RuntimeType,
		Status:      model.StatusQueued,
		Mode:        "queued",
		BaseBranch:  parentPayload.BaseBranch,
	}

	if err := ac.queue.Enqueue(ctx, mission, agent); err != nil {
		ac.logger.Error("failed to enqueue materialized mission",
			"proposal_id", p.ID,
			"task", taskName,
			"error", err,
		)
		return
	}

	// 5. Set mission_id on the payload (now that we have the ID from enqueue).
	childPayload.MissionID = mission.ID
	payloadBytes, _ = json.Marshal(childPayload)
	// Note: mission.ID is set by Enqueue. The payload in the queue already has
	// the old bytes, but the worker reads from the mission row which has the ID.

	// 6. Increment objective counters.
	if err := ac.objectiveStore.IncrementMissionCount(ctx, obj.ID); err != nil {
		ac.logger.Error("failed to increment mission count",
			"objective_id", obj.ID,
			"error", err,
		)
	}

	// 7. Notify proposing agent via mailbox.
	ac.notify(ctx, p.ProposedBy, fmt.Sprintf(
		"Your proposal (id=%d, dedupe_key=%s) has been approved and materialized as mission %q (depth=%d).",
		p.ID, p.DedupeKey, taskName, depth,
	))

	ac.logger.Info("proposal materialized",
		"proposal_id", p.ID,
		"task", taskName,
		"objective_id", obj.ID,
		"depth", depth,
		"parent_mission", parent.ID,
	)
}

// reject marks a proposal as rejected and notifies the proposer.
func (ac *AdmissionController) reject(ctx context.Context, p *proposal.MissionProposal, reason string) {
	if err := ac.proposalStore.Reject(ctx, p.ID, reason); err != nil {
		ac.logger.Error("failed to reject proposal", "proposal_id", p.ID, "error", err)
		return
	}
	ac.notify(ctx, p.ProposedBy, fmt.Sprintf(
		"Your proposal (id=%d, dedupe_key=%s) was rejected: %s",
		p.ID, p.DedupeKey, reason,
	))
}

// freezeObjective transitions an objective to frozen, rejects pending proposals,
// and notifies the chessmaster.
func (ac *AdmissionController) freezeObjective(ctx context.Context, obj *objective.Objective, triggerProposal *proposal.MissionProposal, reason string) {
	obj.Status = objective.StatusFrozen
	if err := ac.objectiveStore.Update(ctx, obj); err != nil {
		ac.logger.Error("failed to freeze objective", "objective_id", obj.ID, "error", err)
		return
	}

	rejected, err := ac.proposalStore.RejectAllPending(ctx, obj.ID, "objective frozen: "+reason)
	if err != nil {
		ac.logger.Error("failed to reject pending proposals on freeze",
			"objective_id", obj.ID,
			"error", err,
		)
	}

	ac.logger.Warn("objective frozen",
		"objective_id", obj.ID,
		"reason", reason,
		"proposals_rejected", rejected,
		"trigger_proposal", triggerProposal.ID,
	)

	// Notify chessmaster.
	ac.notify(ctx, "chessmaster", fmt.Sprintf(
		"Objective %q has been frozen: %s. %d pending proposals rejected. Use fracta_unfreeze_objective to resume.",
		obj.ID, reason, rejected,
	))
}

// checkConvergence checks open objectives for auto-transition conditions.
func (ac *AdmissionController) checkConvergence(ctx context.Context) {
	objectives, err := ac.objectiveStore.ListByStatus(ctx, objective.StatusOpen)
	if err != nil {
		ac.logger.Error("failed to list open objectives", "error", err)
		return
	}

	for _, obj := range objectives {
		ac.checkObjectiveConvergence(ctx, obj)
	}
}

// checkObjectiveConvergence checks a single objective for auto-transition.
func (ac *AdmissionController) checkObjectiveConvergence(ctx context.Context, obj *objective.Objective) {
	// Budget exhaustion: mission count hit cap.
	if obj.MissionCount >= obj.MaxMissions {
		ac.transitionObjective(ctx, obj, objective.StatusBudgetExhausted,
			fmt.Sprintf("mission count %d reached cap %d", obj.MissionCount, obj.MaxMissions))
		return
	}

	// Runtime timeout.
	if obj.MaxRuntime > 0 && time.Since(obj.CreatedAt) >= obj.MaxRuntime {
		ac.transitionObjective(ctx, obj, objective.StatusTimedOut,
			fmt.Sprintf("runtime %s exceeded max %s", time.Since(obj.CreatedAt).Round(time.Second), obj.MaxRuntime))
		return
	}

	// Exhaustion: all missions terminal + no pending proposals.
	allTerminal, err := ac.missionReader.AllMissionsTerminal(ctx, obj.ID)
	if err != nil {
		ac.logger.Error("failed to check terminal missions", "objective_id", obj.ID, "error", err)
		return
	}

	if allTerminal && obj.MissionCount > 0 {
		pending, err := ac.proposalStore.PendingForObjective(ctx, obj.ID)
		if err != nil {
			ac.logger.Error("failed to check pending proposals", "objective_id", obj.ID, "error", err)
			return
		}
		if len(pending) == 0 {
			ac.transitionObjective(ctx, obj, objective.StatusExhausted,
				"all missions terminal with no pending proposals")
		}
	}
}

// transitionObjective moves an objective to a new status and logs/notifies.
func (ac *AdmissionController) transitionObjective(ctx context.Context, obj *objective.Objective, newStatus objective.ObjectiveStatus, reason string) {
	if !objective.CanTransition(obj.Status, newStatus) {
		ac.logger.Warn("invalid objective transition",
			"objective_id", obj.ID,
			"from", obj.Status,
			"to", newStatus,
		)
		return
	}

	obj.Status = newStatus
	if err := ac.objectiveStore.Update(ctx, obj); err != nil {
		ac.logger.Error("failed to transition objective",
			"objective_id", obj.ID,
			"to", newStatus,
			"error", err,
		)
		return
	}

	// Reject any remaining pending proposals.
	if newStatus.Terminal() {
		rejected, _ := ac.proposalStore.RejectAllPending(ctx, obj.ID, "objective "+string(newStatus))
		if rejected > 0 {
			ac.logger.Info("rejected pending proposals on objective close",
				"objective_id", obj.ID,
				"status", newStatus,
				"proposals_rejected", rejected,
			)
		}
	}

	ac.logger.Info("objective transitioned",
		"objective_id", obj.ID,
		"status", newStatus,
		"reason", reason,
	)

	ac.notify(ctx, "chessmaster", fmt.Sprintf(
		"Objective %q transitioned to %s: %s",
		obj.ID, newStatus, reason,
	))
}

// childMissionPayload creates a child payload from a parent by struct copy,
// overriding only the identity fields. The struct copy ensures all topology
// fields (including GatewayURL) are inherited without manual field enumeration.
func childMissionPayload(parent queue.MissionPayload, task, contract, objectiveID string) queue.MissionPayload {
	child := parent // struct copy — inherits all fields
	child.Task = task
	child.Contract = contract
	child.ObjectiveID = objectiveID
	child.MissionID = 0 // set after Enqueue
	return child
}

// notify sends a mailbox message. Logs on failure but does not propagate errors.
func (ac *AdmissionController) notify(ctx context.Context, to, message string) {
	if ac.mailbox == nil {
		return
	}
	if err := ac.mailbox.Send(ctx, "admission-controller", to, message); err != nil {
		ac.logger.Warn("mailbox notification failed", "to", to, "error", err)
	}
}
