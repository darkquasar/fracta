package registry

import (
	"context"
	"encoding/json"
	"time"

	"github.com/darkquasar/fracta/internal/authz"
)

// RegistryService wraps a Store with authorization checks and audit logging.
// All registry mutations go through RegistryService, not directly through Store.
type RegistryService struct {
	store      Store
	authorizer authz.Authorizer
}

// NewRegistryService creates a RegistryService wrapping the given store and authorizer.
func NewRegistryService(store Store, authorizer authz.Authorizer) *RegistryService {
	return &RegistryService{store: store, authorizer: authorizer}
}

// RegisterServer registers a new MCP server after authorization.
func (s *RegistryService) RegisterServer(ctx context.Context, server Server) error {
	sub, err := s.requireSubject(ctx)
	if err != nil {
		return err
	}
	res := authz.Resource{Type: authz.ResourceServer, Name: server.Name}
	if err := s.authorizer.Authorize(ctx, sub, authz.RegisterServer, res); err != nil {
		return err
	}
	if err := s.store.UpsertServer(ctx, server); err != nil {
		return err
	}
	return s.audit(ctx, sub, "RegisterServer", "server", server.Name, nil)
}

// UpdateServer updates an existing MCP server after authorization.
func (s *RegistryService) UpdateServer(ctx context.Context, server Server) error {
	sub, err := s.requireSubject(ctx)
	if err != nil {
		return err
	}
	res := authz.Resource{Type: authz.ResourceServer, Name: server.Name}
	if err := s.authorizer.Authorize(ctx, sub, authz.UpdateServer, res); err != nil {
		return err
	}
	if err := s.store.UpsertServer(ctx, server); err != nil {
		return err
	}
	return s.audit(ctx, sub, "UpdateServer", "server", server.Name, nil)
}

// DeleteServer removes an MCP server after authorization.
func (s *RegistryService) DeleteServer(ctx context.Context, name string) error {
	sub, err := s.requireSubject(ctx)
	if err != nil {
		return err
	}
	res := authz.Resource{Type: authz.ResourceServer, Name: name}
	if err := s.authorizer.Authorize(ctx, sub, authz.DeleteServer, res); err != nil {
		return err
	}
	if err := s.store.DeleteServer(ctx, name); err != nil {
		return err
	}
	return s.audit(ctx, sub, "DeleteServer", "server", name, nil)
}

// SetToolEnabled enables or disables a tool after authorization.
func (s *RegistryService) SetToolEnabled(ctx context.Context, server, tool string, enabled bool) error {
	sub, err := s.requireSubject(ctx)
	if err != nil {
		return err
	}
	var act authz.Action
	if enabled {
		act = authz.EnableTool
	} else {
		act = authz.DisableTool
	}
	res := authz.Resource{Type: authz.ResourceTool, Name: server + "/" + tool}
	if err := s.authorizer.Authorize(ctx, sub, act, res); err != nil {
		return err
	}
	if err := s.store.SetToolEnabled(ctx, server, tool, enabled); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]any{"enabled": enabled})
	return s.audit(ctx, sub, string(act), "tool", server+"/"+tool, detail)
}

// requireSubject extracts the subject from context or returns UnauthorizedError.
func (s *RegistryService) requireSubject(ctx context.Context) (authz.Subject, error) {
	sub, ok := authz.SubjectFromContext(ctx)
	if !ok {
		return authz.Subject{}, &authz.UnauthorizedError{}
	}
	return sub, nil
}

// audit logs an audit entry for the given action.
func (s *RegistryService) audit(ctx context.Context, sub authz.Subject, action, resourceType, resourceName string, detail json.RawMessage) error {
	return s.store.LogAudit(ctx, AuditEntry{
		Actor:        sub.ID,
		ActorType:    string(sub.Type),
		Action:       action,
		ResourceType: resourceType,
		ResourceName: resourceName,
		Detail:       detail,
		Timestamp:    time.Now(),
	})
}
