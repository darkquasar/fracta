package credentials

import (
	"fmt"

	"github.com/darkquasar/fracta/internal/fractalog"
)

// RehydrateSource attaches pre-materialized data to a named source in the plan,
// upgrading its phase from unavailable -> prepare_now with MaterializedData set.
// This preserves the source's identity for legacy resolver.order handling,
// diagnostics, and assertions.
//
// Strict validation:
//   - sourceName must exist in the plan (error if not found)
//   - source type must be command_output (only command outputs produce stageable artifacts)
//   - source delivery must be file_mount or staged_secret (not env-only sources)
//   - source must currently be unavailable or prepare_now (not runtime_only —
//     those are pod-internal and should never be externally materialized)
//   - duplicate rehydration for the same source overwrites MaterializedData (idempotent)
func RehydrateSource(plan *CredentialPlan, sourceName string, data []byte) error {
	log := fractalog.Component("credentials")

	// Find the source in the plan by name.
	var target *AnnotatedSource
	for i := range plan.AuthOrigins {
		if plan.AuthOrigins[i].Name == sourceName {
			target = &plan.AuthOrigins[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("credentials: rehydrate %q: source not found in plan", sourceName)
	}

	// Validate source type — only command_output sources produce stageable artifacts.
	if target.AuthOrigin.Type != "command_output" {
		return fmt.Errorf("credentials: rehydrate %q: source type %q is not stageable (must be command_output)", sourceName, target.AuthOrigin.Type)
	}

	// Validate delivery — only file_mount and staged_secret are valid for staging.
	switch target.AuthOrigin.Delivery {
	case "file_mount", "staged_secret":
		// OK
	default:
		return fmt.Errorf("credentials: rehydrate %q: delivery %q is not stageable (must be file_mount or staged_secret)", sourceName, target.AuthOrigin.Delivery)
	}

	// Validate phase — runtime_only sources must not be externally materialized.
	switch target.Phase {
	case PhaseUnavailable, PhasePrepareNow:
		// OK — unavailable is the normal case (host-edge source on in-cluster worker),
		// prepare_now allows idempotent re-materialization.
	case PhaseRuntimeOnly:
		return fmt.Errorf("credentials: rehydrate %q: phase %q cannot be externally materialized (pod-internal source)", sourceName, target.Phase)
	default:
		return fmt.Errorf("credentials: rehydrate %q: unexpected phase %q", sourceName, target.Phase)
	}

	// Phase transition: unavailable -> prepare_now (or keep prepare_now).
	previousPhase := target.Phase
	target.Phase = PhasePrepareNow
	target.MaterializedData = data

	log.Info("credentials.rehydrate.success",
		"source_name", sourceName,
		"phase_transition", fmt.Sprintf("%s->%s", previousPhase, PhasePrepareNow),
		"bytes_count", len(data),
	)

	return nil
}
