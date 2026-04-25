package host

// NoopHost is a host adapter that does nothing. Used by agent-mode servers
// that only need read-only orchestrator operations (list, peek, inbox, send)
// and never spawn or execute agents.
type NoopHost struct{}

var _ Host = NoopHost{}

func (NoopHost) WriteWorkspace(string, []string, WorkspaceConfig) error {
	return ErrStreamNotSupported
}

func (NoopHost) Bootstrap(string, string, string) BootstrapResult {
	return BootstrapResult{}
}

func (NoopHost) BuildBatchCommand(string, string, string) CommandSpec {
	return CommandSpec{}
}

func (NoopHost) ParseBatchOutput([]byte, error) (Result, error) {
	return Result{}, ErrStreamNotSupported
}

func (NoopHost) StartStream(string, string, string) (StreamSession, error) {
	return nil, ErrStreamNotSupported
}

func (NoopHost) Capabilities() Capabilities {
	return Capabilities{
		StructuredEvents: false,
	}
}
