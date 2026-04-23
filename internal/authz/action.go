package authz

// Action represents an operation that a subject wants to perform.
type Action string

const (
	// v1 enforcement surface (checked via RegistryService).
	RegisterServer Action = "RegisterServer"
	UpdateServer   Action = "UpdateServer"
	DeleteServer   Action = "DeleteServer"
	EnableTool     Action = "EnableTool"
	DisableTool    Action = "DisableTool"

	// Future enforcement (defined for forward compatibility, not wired in v1).
	ViewCatalog      Action = "ViewCatalog"
	SearchTool       Action = "SearchTool"
	ViewStatus       Action = "ViewStatus"
	CallTool         Action = "CallTool"
	RefreshDiscovery Action = "RefreshDiscovery"
	BindSecret       Action = "BindSecret"
)
