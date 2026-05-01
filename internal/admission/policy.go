package admission

import (
	"context"
	"fmt"
	"time"

	"github.com/darkquasar/fracta/internal/objective"
	"github.com/darkquasar/fracta/internal/proposal"
	"github.com/darkquasar/fracta/internal/queue"
)

// Action is the outcome of a policy evaluation.
type Action string

const (
	ActionApprove Action = "approve"
	ActionReject  Action = "reject"
	ActionFreeze  Action = "freeze"
)

// Decision is the result of evaluating a proposal through the admission policy.
type Decision struct {
	Action Action
	Reason string
}

// AdmissionPolicy encapsulates the configurable parameters for the 8-step
// admission pipeline.
type AdmissionPolicy struct {
	// EvidenceThreshold: after this many missions, proposals must include evidence.
	EvidenceThreshold int

	// CircuitBreakerRatio: mission_count / finding_count ratio that triggers freeze.
	// E.g., 3 means freeze when missions > 3 * findings.
	CircuitBreakerRatio int

	// Now returns the current time. Override in tests.
	Now func() time.Time
}

// DefaultPolicy returns an AdmissionPolicy with spec defaults.
func DefaultPolicy() *AdmissionPolicy {
	return &AdmissionPolicy{
		EvidenceThreshold:   3,
		CircuitBreakerRatio: 3,
		Now:                 time.Now,
	}
}

// Evaluate runs the 8-step policy pipeline on a proposal.
//
// Steps:
//  1. Objective status — reject if not open
//  2. Dedupe — soft check (DB unique index is authority)
//  3. Mission count — objective.mission_count < objective.max_missions
//  4. Depth limit — parent.depth + 1 <= objective.max_depth
//  5. Branching factor — active children of parent < objective.max_branching
//  6. Runtime headroom — time since objective creation < max_runtime
//  7. Evidence threshold — after N missions, require non-empty evidence
//  8. Circuit breaker — mission_count > K * finding_count → freeze
func (p *AdmissionPolicy) Evaluate(
	ctx context.Context,
	prop *proposal.MissionProposal,
	obj *objective.Objective,
	parent *queue.Mission,
	reader MissionReader,
) Decision {
	// Step 1: Objective status.
	if obj.Status != objective.StatusOpen {
		return Decision{ActionReject, fmt.Sprintf("objective status is %s, not open", obj.Status)}
	}

	// Step 2: Dedupe — the database unique partial index on
	// (objective_id, dedupe_key) WHERE status IN ('pending', 'claimed')
	// is the authority. Enqueue will fail on duplicates.

	// Step 3: Mission count.
	if obj.MissionCount >= obj.MaxMissions {
		return Decision{ActionReject, fmt.Sprintf("mission count %d reached cap %d", obj.MissionCount, obj.MaxMissions)}
	}

	// Step 4: Depth limit.
	childDepth := parent.Depth + 1
	if childDepth > obj.MaxDepth {
		return Decision{ActionReject, fmt.Sprintf("depth %d exceeds max %d", childDepth, obj.MaxDepth)}
	}

	// Step 5: Branching factor.
	activeChildren, err := reader.CountActiveChildren(ctx, parent.ID)
	if err != nil {
		return Decision{ActionReject, fmt.Sprintf("failed to count active children: %v", err)}
	}
	if activeChildren >= obj.MaxBranching {
		return Decision{ActionReject, fmt.Sprintf("parent has %d active children, max branching is %d", activeChildren, obj.MaxBranching)}
	}

	// Step 6: Runtime headroom.
	now := p.Now()
	if obj.MaxRuntime > 0 {
		elapsed := now.Sub(obj.CreatedAt)
		if elapsed >= obj.MaxRuntime {
			return Decision{ActionReject, fmt.Sprintf("objective runtime %s exceeded max %s", elapsed.Round(time.Second), obj.MaxRuntime)}
		}
	}

	// Step 7: Evidence threshold.
	if p.EvidenceThreshold > 0 && obj.MissionCount >= p.EvidenceThreshold {
		if len(prop.Evidence) == 0 || string(prop.Evidence) == "null" {
			return Decision{ActionReject, fmt.Sprintf(
				"evidence required after %d missions (objective has %d)",
				p.EvidenceThreshold, obj.MissionCount,
			)}
		}
	}

	// Step 8: Circuit breaker.
	if p.CircuitBreakerRatio > 0 && obj.MissionCount > 0 {
		threshold := p.CircuitBreakerRatio * obj.FindingCount
		if threshold == 0 {
			// No findings yet: allow up to CircuitBreakerRatio missions before requiring findings.
			threshold = p.CircuitBreakerRatio
		}
		if obj.MissionCount >= threshold {
			return Decision{ActionFreeze, fmt.Sprintf(
				"circuit breaker: %d missions with only %d findings (ratio limit %d:1)",
				obj.MissionCount, obj.FindingCount, p.CircuitBreakerRatio,
			)}
		}
	}

	return Decision{ActionApprove, "all checks passed"}
}
