package strategy

// Runner is the interface for executing strategies. It abstracts the
// underlying implementation (Sidecar, SidecarPool, or test doubles).
type Runner interface {
	List(tags ...string) ([]StrategyInfo, error)
	Describe(name string) (*StrategyInfo, error)
	Run(name string, params map[string]any, manifest StagingManifest) (*RunResult, error)
	Create(name, code, metadata string, force bool) error
	CreateWithContract(name, code, contractYAML string, force bool) error
	StagingDir() string
	StrategyDir() string
	Close() error
}

// Compile-time assertion: *Sidecar satisfies Runner.
var _ Runner = (*Sidecar)(nil)
