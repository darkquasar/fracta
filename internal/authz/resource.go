package authz

// ResourceType classifies the target of an authorization check.
type ResourceType string

const (
	ResourceServer  ResourceType = "server"
	ResourceTool    ResourceType = "tool"
	ResourceSecret  ResourceType = "secret"
	ResourceCatalog ResourceType = "catalog"
)

// Resource identifies the object being acted upon.
type Resource struct {
	Type ResourceType
	Name string
}
