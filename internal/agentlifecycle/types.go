package agentlifecycle

// LifecycleMeta carries context for non-result-bearing transitions.
type LifecycleMeta struct {
	RuntimeType string
	Backend     string
	MissionID   int64
	ObjectiveID string
	Reason      string
}

// ResultMeta carries context for result-bearing terminal transitions.
type ResultMeta struct {
	LifecycleMeta
	LastOutput  string
	ResumeToken string
}

// CreationMeta carries context for agent creation (direct spawn).
type CreationMeta struct {
	LifecycleMeta
	WorkspacePath string
	BranchName    string
	BaseBranch    string
	Mode          string // "batch" or "stream"
}
