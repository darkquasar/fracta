package authz

import "fmt"

// ForbiddenError is returned when a subject lacks permission for an action.
type ForbiddenError struct {
	Subject Subject
	Action  Action
	Resource Resource
	Reason  string
}

func (e *ForbiddenError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("forbidden: %s %s on %s/%s: %s",
			e.Subject.ID, e.Action, e.Resource.Type, e.Resource.Name, e.Reason)
	}
	return fmt.Sprintf("forbidden: %s %s on %s/%s",
		e.Subject.ID, e.Action, e.Resource.Type, e.Resource.Name)
}

// UnauthorizedError is returned when no subject is present in the context.
type UnauthorizedError struct{}

func (e *UnauthorizedError) Error() string {
	return "unauthorized: no subject in context"
}
