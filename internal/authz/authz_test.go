package authz_test

import (
	"context"
	"testing"

	"github.com/darkquasar/fracta/internal/authz"
)

// allActions enumerates every defined action for matrix coverage.
var allActions = []authz.Action{
	authz.ViewCatalog,
	authz.SearchTool,
	authz.ViewStatus,
	authz.CallTool,
	authz.RefreshDiscovery,
	authz.EnableTool,
	authz.DisableTool,
	authz.RegisterServer,
	authz.UpdateServer,
	authz.DeleteServer,
	authz.BindSecret,
}

// expectedMatrix is the spec Section 5.2 role matrix. true = allowed.
var expectedMatrix = map[string]map[authz.Action]bool{
	"admin": {
		authz.ViewCatalog: true, authz.SearchTool: true, authz.ViewStatus: true,
		authz.CallTool: true, authz.RefreshDiscovery: true,
		authz.EnableTool: true, authz.DisableTool: true,
		authz.RegisterServer: true, authz.UpdateServer: true, authz.DeleteServer: true,
		authz.BindSecret: true,
	},
	"operator": {
		authz.ViewCatalog: true, authz.SearchTool: true, authz.ViewStatus: true,
		authz.RefreshDiscovery: true, authz.EnableTool: true, authz.DisableTool: true,
	},
	"viewer": {
		authz.ViewCatalog: true, authz.SearchTool: true, authz.ViewStatus: true,
	},
	"agent": {
		authz.ViewCatalog: true, authz.SearchTool: true, authz.CallTool: true,
	},
}

func TestDefaultAuthorizer_RoleMatrix(t *testing.T) {
	az := &authz.DefaultAuthorizer{}
	ctx := context.Background()
	res := authz.Resource{Type: authz.ResourceServer, Name: "test-server"}

	for _, role := range []string{"admin", "operator", "viewer", "agent"} {
		sub := authz.Subject{Type: authz.SubjectAdmin, ID: "test-user", Roles: []string{role}}
		allowed := expectedMatrix[role]

		for _, action := range allActions {
			expectAllow := allowed[action]
			err := az.Authorize(ctx, sub, action, res)

			if expectAllow && err != nil {
				t.Errorf("role=%s action=%s: expected allow, got error: %v", role, action, err)
			}
			if !expectAllow && err == nil {
				t.Errorf("role=%s action=%s: expected deny, got allow", role, action)
			}
			if !expectAllow && err != nil {
				if _, ok := err.(*authz.ForbiddenError); !ok {
					t.Errorf("role=%s action=%s: expected *ForbiddenError, got %T", role, action, err)
				}
			}
		}
	}
}

func TestDefaultAuthorizer_NoRoles(t *testing.T) {
	az := &authz.DefaultAuthorizer{}
	sub := authz.Subject{Type: authz.SubjectAdmin, ID: "nobody", Roles: nil}
	res := authz.Resource{Type: authz.ResourceServer, Name: "s"}

	for _, action := range allActions {
		err := az.Authorize(context.Background(), sub, action, res)
		if err == nil {
			t.Errorf("action=%s: expected deny for subject with no roles", action)
		}
	}
}

func TestDefaultAuthorizer_UnknownRole(t *testing.T) {
	az := &authz.DefaultAuthorizer{}
	sub := authz.Subject{Type: authz.SubjectAdmin, ID: "x", Roles: []string{"unknown"}}
	res := authz.Resource{Type: authz.ResourceServer, Name: "s"}

	err := az.Authorize(context.Background(), sub, authz.RegisterServer, res)
	if err == nil {
		t.Error("expected deny for unknown role")
	}
}

func TestDefaultAuthorizer_MultipleRoles(t *testing.T) {
	az := &authz.DefaultAuthorizer{}
	// viewer + operator should get operator's permissions
	sub := authz.Subject{Type: authz.SubjectOperator, ID: "multi", Roles: []string{"viewer", "operator"}}
	res := authz.Resource{Type: authz.ResourceServer, Name: "s"}

	// EnableTool is allowed for operator but not viewer
	if err := az.Authorize(context.Background(), sub, authz.EnableTool, res); err != nil {
		t.Errorf("expected allow for operator role in multi-role subject: %v", err)
	}
	// RegisterServer is denied for both viewer and operator
	if err := az.Authorize(context.Background(), sub, authz.RegisterServer, res); err == nil {
		t.Error("expected deny for RegisterServer with viewer+operator roles")
	}
}

func TestSubject_HasRole(t *testing.T) {
	sub := authz.Subject{Roles: []string{"admin", "operator"}}
	if !sub.HasRole("admin") {
		t.Error("expected HasRole(admin) = true")
	}
	if !sub.HasRole("operator") {
		t.Error("expected HasRole(operator) = true")
	}
	if sub.HasRole("viewer") {
		t.Error("expected HasRole(viewer) = false")
	}
}

func TestContextRoundTrip(t *testing.T) {
	original := authz.Subject{Type: authz.SubjectAdmin, ID: "ctx-user", Roles: []string{"admin"}}
	ctx := authz.WithSubject(context.Background(), original)

	got, ok := authz.SubjectFromContext(ctx)
	if !ok {
		t.Fatal("expected subject in context")
	}
	if got.ID != original.ID || got.Type != original.Type {
		t.Errorf("subject mismatch: got %+v, want %+v", got, original)
	}
}

func TestSubjectFromContext_Missing(t *testing.T) {
	_, ok := authz.SubjectFromContext(context.Background())
	if ok {
		t.Error("expected no subject in empty context")
	}
}

func TestForbiddenError_Message(t *testing.T) {
	err := &authz.ForbiddenError{
		Subject:  authz.Subject{ID: "alice"},
		Action:   authz.RegisterServer,
		Resource: authz.Resource{Type: authz.ResourceServer, Name: "my-server"},
		Reason:   "no role grants this action",
	}
	msg := err.Error()
	if msg != "forbidden: alice RegisterServer on server/my-server: no role grants this action" {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestForbiddenError_NoReason(t *testing.T) {
	err := &authz.ForbiddenError{
		Subject:  authz.Subject{ID: "bob"},
		Action:   authz.DeleteServer,
		Resource: authz.Resource{Type: authz.ResourceServer, Name: "x"},
	}
	msg := err.Error()
	if msg != "forbidden: bob DeleteServer on server/x" {
		t.Errorf("unexpected error message: %s", msg)
	}
}

func TestUnauthorizedError_Message(t *testing.T) {
	err := &authz.UnauthorizedError{}
	if err.Error() != "unauthorized: no subject in context" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}
