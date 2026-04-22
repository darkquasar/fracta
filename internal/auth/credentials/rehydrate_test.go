package credentials

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeTestPlan() *CredentialPlan {
	return &CredentialPlan{
		Profile: "bedrock",
		AuthOrigins: []AnnotatedSource{
			{
				Name:  "proxy",
				Phase: PhaseRuntimeOnly,
				AuthOrigin: &CredentialSource{
					Type:  "http_header_token",
					Scope: "agent_runtime",
				},
			},
			{
				Name:  "host_fallback",
				Phase: PhaseUnavailable,
				AuthOrigin: &CredentialSource{
					Type:     "command_output",
					Scope:    "host_edge",
					Delivery: "file_mount",
					Path:     "/var/run/fracta-auth/bedrock-token",
				},
			},
		},
	}
}

func TestRehydrateSource_Success(t *testing.T) {
	plan := makeTestPlan()

	err := RehydrateSource(plan, "host_fallback", []byte("rehydrated-token"))
	require.NoError(t, err)

	// Verify phase transition.
	src := plan.AuthOrigins[1]
	assert.Equal(t, PhasePrepareNow, src.Phase)
	assert.Equal(t, []byte("rehydrated-token"), src.MaterializedData)
}

func TestRehydrateSource_NotFound(t *testing.T) {
	plan := makeTestPlan()

	err := RehydrateSource(plan, "nonexistent", []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "source not found")
}

func TestRehydrateSource_WrongType(t *testing.T) {
	plan := makeTestPlan()

	// proxy is http_header_token, not command_output.
	err := RehydrateSource(plan, "proxy", []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not stageable")
	assert.Contains(t, err.Error(), "http_header_token")
}

func TestRehydrateSource_WrongDelivery(t *testing.T) {
	plan := &CredentialPlan{
		AuthOrigins: []AnnotatedSource{
			{
				Name:  "env_source",
				Phase: PhaseUnavailable,
				AuthOrigin: &CredentialSource{
					Type:     "command_output",
					Scope:    "host_edge",
					Delivery: "env",
				},
			},
		},
	}

	err := RehydrateSource(plan, "env_source", []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not stageable")
	assert.Contains(t, err.Error(), "env")
}

func TestRehydrateSource_RuntimeOnlyRejected(t *testing.T) {
	plan := makeTestPlan()

	// proxy is runtime_only — should be rejected.
	// But proxy is also http_header_token which would fail type check first.
	// Create a command_output source that is runtime_only.
	plan.AuthOrigins = append(plan.AuthOrigins, AnnotatedSource{
		Name:  "pod_cmd",
		Phase: PhaseRuntimeOnly,
		AuthOrigin: &CredentialSource{
			Type:     "command_output",
			Scope:    "agent_runtime",
			Delivery: "file_mount",
		},
	})

	err := RehydrateSource(plan, "pod_cmd", []byte("data"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot be externally materialized")
}

func TestRehydrateSource_Idempotent(t *testing.T) {
	plan := makeTestPlan()

	err := RehydrateSource(plan, "host_fallback", []byte("first"))
	require.NoError(t, err)

	// Second rehydration with different data should overwrite.
	err = RehydrateSource(plan, "host_fallback", []byte("second"))
	require.NoError(t, err)

	src := plan.AuthOrigins[1]
	assert.Equal(t, PhasePrepareNow, src.Phase)
	assert.Equal(t, []byte("second"), src.MaterializedData)
}

func TestRehydrateSource_PrepareNowAllowed(t *testing.T) {
	plan := &CredentialPlan{
		AuthOrigins: []AnnotatedSource{
			{
				Name:  "host_cmd",
				Phase: PhasePrepareNow,
				AuthOrigin: &CredentialSource{
					Type:     "command_output",
					Scope:    "host_edge",
					Delivery: "file_mount",
				},
			},
		},
	}

	err := RehydrateSource(plan, "host_cmd", []byte("data"))
	require.NoError(t, err)
	assert.Equal(t, PhasePrepareNow, plan.AuthOrigins[0].Phase)
	assert.Equal(t, []byte("data"), plan.AuthOrigins[0].MaterializedData)
}

func TestRehydrateSource_StagedSecretDeliveryAllowed(t *testing.T) {
	plan := &CredentialPlan{
		AuthOrigins: []AnnotatedSource{
			{
				Name:  "staged_src",
				Phase: PhaseUnavailable,
				AuthOrigin: &CredentialSource{
					Type:     "command_output",
					Scope:    "host_edge",
					Delivery: "staged_secret",
				},
			},
		},
	}

	err := RehydrateSource(plan, "staged_src", []byte("data"))
	require.NoError(t, err)
	assert.Equal(t, PhasePrepareNow, plan.AuthOrigins[0].Phase)
}
