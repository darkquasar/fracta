package model

import (
	"fmt"
	"regexp"
	"time"
)

const (
	FractaDir   = ".fracta"
	WorktreeDir = ".worktrees"
	LogsDir     = "logs"
)

// AgentStatus represents the lifecycle state of an agent.
type AgentStatus string

const (
	StatusPending   AgentStatus = "Pending"
	StatusQueued    AgentStatus = "Queued"
	StatusRunning   AgentStatus = "Running"
	StatusStopped   AgentStatus = "Stopped"
	StatusCompleted AgentStatus = "Completed"
	StatusFailed    AgentStatus = "Failed"
	StatusIdle      AgentStatus = "Idle"
)

var taskNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,63}$`)

// ValidTierNames lists the recognised model tier names.
var ValidTierNames = []string{"heavy", "medium", "light"}

// ResolveModelFromTier looks up a tier name in the tiers map.
// Returns the model ID and true if found, or ("", false) otherwise.
func ResolveModelFromTier(tier string, tiers map[string]string) (string, bool) {
	if tiers == nil {
		return "", false
	}
	m, ok := tiers[tier]
	return m, ok
}

type AgentEntry struct {
	Task          string      `json:"task"`
	RuntimeType   string      `json:"host_type,omitempty"` // JSON/DB tag kept as host_type for backward compat
	ResumeToken   string      `json:"resume_token"`
	WorkspacePath string      `json:"workspace_path"`
	BranchName    string      `json:"branch_name"`
	BaseBranch    string      `json:"base_branch,omitempty"`
	Status        AgentStatus `json:"status"`
	LastOutput    string      `json:"last_output"`
	StartTime     time.Time   `json:"start_time"`
	Mode          string      `json:"mode,omitempty"`
	CurrentIntent string      `json:"current_intent,omitempty"`
	MissionID     int64       `json:"mission_id,omitempty"`
	ObjectiveID   string      `json:"objective_id,omitempty"`
}

type State struct {
	Agents      []AgentEntry      `json:"agents"`
	Chessmaster ChessmasterStatus `json:"chessmaster,omitempty"`
}

type ChessmasterStatus struct {
	Status     string    `json:"status,omitempty"`
	LastAction string    `json:"last_action,omitempty"`
	UpdatedAt  time.Time `json:"updated_at,omitempty"`
}

func ValidateTaskName(name string) error {
	if !taskNameRe.MatchString(name) {
		return fmt.Errorf("invalid task name %q: must match %s", name, taskNameRe.String())
	}
	return nil
}
