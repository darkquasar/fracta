package authz

// SubjectType identifies the category of the actor making a request.
type SubjectType string

const (
	SubjectAdmin    SubjectType = "admin"
	SubjectOperator SubjectType = "operator"
	SubjectViewer   SubjectType = "viewer"
	SubjectAgent    SubjectType = "agent"
	SubjectService  SubjectType = "service"
)

// Subject represents an authenticated identity making a request.
type Subject struct {
	Type  SubjectType
	ID    string
	Roles []string
}

// HasRole returns true if the subject has the given role.
func (s Subject) HasRole(role string) bool {
	for _, r := range s.Roles {
		if r == role {
			return true
		}
	}
	return false
}
