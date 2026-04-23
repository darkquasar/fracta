package authz

import "context"

// Authorizer checks whether a subject is allowed to perform an action on a resource.
type Authorizer interface {
	Authorize(ctx context.Context, sub Subject, act Action, res Resource) error
}

// DefaultAuthorizer uses the static role-permission matrix from rules.go.
type DefaultAuthorizer struct{}

// Authorize checks if any of the subject's roles permits the action.
// Returns nil on success, *ForbiddenError if denied.
func (a *DefaultAuthorizer) Authorize(_ context.Context, sub Subject, act Action, res Resource) error {
	for _, role := range sub.Roles {
		if roleAllows(role, act) {
			return nil
		}
	}
	return &ForbiddenError{
		Subject:  sub,
		Action:   act,
		Resource: res,
		Reason:   "no role grants this action",
	}
}
