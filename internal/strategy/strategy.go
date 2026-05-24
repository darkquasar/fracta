package strategy

// RunOptions carries per-invocation context that varies between runs.
// Used for shared-sidecar scenarios where multiple agents use one runner.
type RunOptions struct {
	GatewayURL string // MCP gateway base URL for mid-execution tool calls
	AgentTask  string // Agent task name for tool visibility scope
}

// Runner is the interface for executing strategies. It abstracts the
// underlying implementation (Sidecar, SidecarPool, or test doubles).
type Runner interface {
	List(tags ...string) ([]StrategyInfo, error)
	Describe(name string) (*StrategyInfo, error)
	Run(name string, params map[string]any, manifest StagingManifest, opts *RunOptions) (*RunResult, error)
	Create(name, code, metadata string, force bool) error
	CreateWithContract(name, code, contractYAML string, force bool) error
	StagingDir() string
	StrategyDir() string
	Close() error
}

// Compile-time assertion: *Sidecar satisfies Runner.
var _ Runner = (*Sidecar)(nil)
