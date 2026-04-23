package authz

import "context"

type contextKey struct{}

// WithSubject returns a new context carrying the given Subject.
func WithSubject(ctx context.Context, sub Subject) context.Context {
	return context.WithValue(ctx, contextKey{}, sub)
}

// SubjectFromContext extracts the Subject from the context.
// Returns the subject and true if present, or a zero Subject and false if not.
func SubjectFromContext(ctx context.Context) (Subject, bool) {
	sub, ok := ctx.Value(contextKey{}).(Subject)
	return sub, ok
}
