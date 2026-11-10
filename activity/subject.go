package activity

// Subject identifies the resource whose commands share a mutation lock.
// Session subjects host cloud answer workflows; they never impersonate work items.
type SubjectKind string

const (
	SubjectWorkItem SubjectKind = "work_item"
	SubjectSession  SubjectKind = "session"
)

type Subject struct {
	Kind      SubjectKind `json:"kind"`
	ID        string      `json:"id"`
	ScopeType string      `json:"scope_type"`
	ScopeID   string      `json:"scope_id"`
}

func (s Subject) Validate(runner Runner, location Location) error {
	if s.ID == "" || s.ScopeID == "" {
		return ErrValidation
	}
	switch s.Kind {
	case SubjectWorkItem:
		if s.ScopeType != "project" || !AppliesTo(runner, s.ScopeType) {
			return ErrValidation
		}
	case SubjectSession:
		if (s.ScopeType != "team" && s.ScopeType != "project" && s.ScopeType != "organization") || runner != Answer || (location != Cloud && !(s.ScopeType == "project" && location == Local)) {
			return ErrValidation
		}
	default:
		return ErrValidation
	}
	return nil
}
func (s Subject) LockKey() string { return string(s.Kind) + ":" + s.ID }

// RunSubject resolves snapshots created before typed subjects were introduced.
func (r Run) RunSubject() Subject {
	if r.Subject.Kind != "" {
		return r.Subject
	}
	return Subject{SubjectWorkItem, r.WorkItemID, "project", r.ProjectID}
}
func (r Run) IsSessionWorkflow() bool { return r.RunSubject().Kind == SubjectSession }
func (r Run) IsMeeting() bool         { return r.IsSessionWorkflow() && r.RunSubject().ScopeType == "team" }
