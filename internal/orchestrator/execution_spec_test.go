package orchestrator

import (
	"encoding/json"
	"testing"

	"github.com/darkquasar/fracta/internal/queue"
)

// TestMissionPayloadRoundTrip verifies that ExecutionSpec survives
// serialization through MissionPayload and back.
func TestMissionPayloadRoundTrip(t *testing.T) {
	original := ExecutionSpec{
		Identity: SpecIdentity{
			Task:        "my-task",
			Contract:    "do the thing",
			BaseBranch:  "main",
			ObjectiveID: "obj-123",
			MissionID:   42,
		},
		Resolution: SpecResolution{
			RuntimeType:  "claude",
			Model:        "opus-4",
			Mode:         "batch",
			AllowedTools: []string{"Read", "Write", "Bash"},
		},
		Topology: SpecTopology{
			MCPServers:  json.RawMessage(`{"servers":{"gateway":{"url":"http://gw:8080"}}}`),
			Backend:     "kubernetes",
			ConfigPath:  "/etc/fracta/config.yaml",
			GraphAddr:   "localhost:6379",
			StrategyDir: "/strategies",
			GatewayURL:  "http://gateway:9090",
			ConfigHash:  "abc123def456",
		},
		Credentials: &SpecCredentials{
			StagedCredentialRefs: map[string]string{
				"bedrock_token": "ref-abc",
			},
		},
	}

	// Spec -> Payload -> JSON -> Payload -> Spec
	payload := original.ToMissionPayload()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	var decoded MissionPayloadForTest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	// Use the real queue.MissionPayload type for the round-trip.
	var realPayload = original.ToMissionPayload()
	payloadBytes, _ := json.Marshal(realPayload)
	var roundPayload = payloadFromJSON(t, payloadBytes)
	roundTrip := ExecutionSpecFromPayload(roundPayload)

	// Identity
	if roundTrip.Identity.Task != original.Identity.Task {
		t.Errorf("Task: got %q, want %q", roundTrip.Identity.Task, original.Identity.Task)
	}
	if roundTrip.Identity.Contract != original.Identity.Contract {
		t.Errorf("Contract: got %q, want %q", roundTrip.Identity.Contract, original.Identity.Contract)
	}
	if roundTrip.Identity.BaseBranch != original.Identity.BaseBranch {
		t.Errorf("BaseBranch: got %q, want %q", roundTrip.Identity.BaseBranch, original.Identity.BaseBranch)
	}
	if roundTrip.Identity.ObjectiveID != original.Identity.ObjectiveID {
		t.Errorf("ObjectiveID: got %q, want %q", roundTrip.Identity.ObjectiveID, original.Identity.ObjectiveID)
	}
	if roundTrip.Identity.MissionID != original.Identity.MissionID {
		t.Errorf("MissionID: got %d, want %d", roundTrip.Identity.MissionID, original.Identity.MissionID)
	}

	// Resolution
	if roundTrip.Resolution.RuntimeType != original.Resolution.RuntimeType {
		t.Errorf("RuntimeType: got %q, want %q", roundTrip.Resolution.RuntimeType, original.Resolution.RuntimeType)
	}
	if roundTrip.Resolution.Model != original.Resolution.Model {
		t.Errorf("Model: got %q, want %q", roundTrip.Resolution.Model, original.Resolution.Model)
	}
	if roundTrip.Resolution.Mode != original.Resolution.Mode {
		t.Errorf("Mode: got %q, want %q", roundTrip.Resolution.Mode, original.Resolution.Mode)
	}

	// Topology — the critical fields
	if roundTrip.Topology.Backend != original.Topology.Backend {
		t.Errorf("Backend: got %q, want %q", roundTrip.Topology.Backend, original.Topology.Backend)
	}
	if roundTrip.Topology.GatewayURL != original.Topology.GatewayURL {
		t.Errorf("GatewayURL: got %q, want %q", roundTrip.Topology.GatewayURL, original.Topology.GatewayURL)
	}
	if roundTrip.Topology.ConfigPath != original.Topology.ConfigPath {
		t.Errorf("ConfigPath: got %q, want %q", roundTrip.Topology.ConfigPath, original.Topology.ConfigPath)
	}
	if roundTrip.Topology.GraphAddr != original.Topology.GraphAddr {
		t.Errorf("GraphAddr: got %q, want %q", roundTrip.Topology.GraphAddr, original.Topology.GraphAddr)
	}
	if roundTrip.Topology.StrategyDir != original.Topology.StrategyDir {
		t.Errorf("StrategyDir: got %q, want %q", roundTrip.Topology.StrategyDir, original.Topology.StrategyDir)
	}
	if roundTrip.Topology.ConfigHash != original.Topology.ConfigHash {
		t.Errorf("ConfigHash: got %q, want %q", roundTrip.Topology.ConfigHash, original.Topology.ConfigHash)
	}

	// Credentials
	if roundTrip.Credentials == nil {
		t.Fatal("Credentials: nil after round-trip")
	}
	if roundTrip.Credentials.StagedCredentialRefs["bedrock_token"] != "ref-abc" {
		t.Errorf("StagedCredentialRefs: got %v", roundTrip.Credentials.StagedCredentialRefs)
	}
}

// TestChildSpecInheritsTopology verifies that ChildSpec inherits all topology
// fields from the parent, specifically GatewayURL which was previously lost.
func TestChildSpecInheritsTopology(t *testing.T) {
	parent := ExecutionSpec{
		Identity: SpecIdentity{
			Task:       "parent-task",
			BaseBranch: "main",
			MissionID:  1,
		},
		Resolution: SpecResolution{
			RuntimeType: "claude",
			Model:       "opus-4",
		},
		Topology: SpecTopology{
			Backend:     "kubernetes",
			GatewayURL:  "http://gateway:9090",
			ConfigPath:  "/etc/fracta/config.yaml",
			GraphAddr:   "localhost:6379",
			StrategyDir: "/strategies",
			ConfigHash:  "parenthash",
			MCPServers:  json.RawMessage(`{"servers":{}}`),
		},
		Credentials: &SpecCredentials{
			StagedCredentialRefs: map[string]string{"key": "val"},
		},
	}

	child := ChildSpec(parent, "child-task", "child contract", "obj-456")

	// Identity should be overridden.
	if child.Identity.Task != "child-task" {
		t.Errorf("child Task: got %q, want %q", child.Identity.Task, "child-task")
	}
	if child.Identity.Contract != "child contract" {
		t.Errorf("child Contract: got %q, want %q", child.Identity.Contract, "child contract")
	}
	if child.Identity.ObjectiveID != "obj-456" {
		t.Errorf("child ObjectiveID: got %q, want %q", child.Identity.ObjectiveID, "obj-456")
	}
	if child.Identity.BaseBranch != "main" {
		t.Errorf("child BaseBranch: got %q, want %q", child.Identity.BaseBranch, "main")
	}
	if child.Identity.MissionID != 0 {
		t.Errorf("child MissionID: got %d, want 0 (set after enqueue)", child.Identity.MissionID)
	}

	// Resolution should be inherited verbatim.
	if child.Resolution.RuntimeType != parent.Resolution.RuntimeType {
		t.Errorf("child HostType: got %q, want %q", child.Resolution.RuntimeType, parent.Resolution.RuntimeType)
	}
	if child.Resolution.Model != parent.Resolution.Model {
		t.Errorf("child Model: got %q, want %q", child.Resolution.Model, parent.Resolution.Model)
	}

	// Topology — the GatewayURL bug fix.
	if child.Topology.GatewayURL != parent.Topology.GatewayURL {
		t.Errorf("child GatewayURL: got %q, want %q (must inherit from parent)", child.Topology.GatewayURL, parent.Topology.GatewayURL)
	}
	if child.Topology.Backend != parent.Topology.Backend {
		t.Errorf("child Backend: got %q, want %q", child.Topology.Backend, parent.Topology.Backend)
	}
	if child.Topology.ConfigPath != parent.Topology.ConfigPath {
		t.Errorf("child ConfigPath: got %q, want %q", child.Topology.ConfigPath, parent.Topology.ConfigPath)
	}
	if child.Topology.GraphAddr != parent.Topology.GraphAddr {
		t.Errorf("child GraphAddr: got %q, want %q", child.Topology.GraphAddr, parent.Topology.GraphAddr)
	}
	if child.Topology.StrategyDir != parent.Topology.StrategyDir {
		t.Errorf("child StrategyDir: got %q, want %q", child.Topology.StrategyDir, parent.Topology.StrategyDir)
	}
	if child.Topology.ConfigHash != parent.Topology.ConfigHash {
		t.Errorf("child ConfigHash: got %q, want %q", child.Topology.ConfigHash, parent.Topology.ConfigHash)
	}

	// Credentials should be inherited.
	if child.Credentials == nil {
		t.Fatal("child Credentials: nil (should inherit from parent)")
	}
	if child.Credentials.StagedCredentialRefs["key"] != "val" {
		t.Errorf("child StagedCredentialRefs: got %v", child.Credentials.StagedCredentialRefs)
	}
}

// TestMissionPayloadRoundTrip_NilCredentials verifies that a spec without
// credentials survives the round-trip.
func TestMissionPayloadRoundTrip_NilCredentials(t *testing.T) {
	original := ExecutionSpec{
		Identity: SpecIdentity{
			Task: "simple-task",
		},
		Resolution: SpecResolution{
			RuntimeType: "claude",
		},
		Topology: SpecTopology{
			Backend: "local",
		},
	}

	payload := original.ToMissionPayload()
	roundTrip := ExecutionSpecFromPayload(payload)

	if roundTrip.Credentials != nil {
		t.Errorf("Credentials should be nil for spec without staged refs, got %v", roundTrip.Credentials)
	}
	if roundTrip.Identity.Task != "simple-task" {
		t.Errorf("Task: got %q, want %q", roundTrip.Identity.Task, "simple-task")
	}
}

// MissionPayloadForTest mirrors queue.MissionPayload for JSON round-trip testing
// without creating an import cycle.
type MissionPayloadForTest struct {
	Task        string `json:"task"`
	RuntimeType string `json:"host_type"`
}

// payloadFromJSON deserializes a queue.MissionPayload from JSON bytes.
func payloadFromJSON(t *testing.T, data []byte) queue.MissionPayload {
	var p queue.MissionPayload
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal MissionPayload: %v", err)
	}
	return p
}
