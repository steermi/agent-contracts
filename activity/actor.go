package activity

// Actor is who sends a command: a person, or the service acting for a
// purpose on a run.
type Actor struct {
	OrgID, UserID, Role string
	MeetingRole         string
	Purpose             Purpose
	Runner              Runner
	// SkillSlug is the skill the actor acts for ("steermi:review" or a stored
	// slug); hosts use it to pick the rules that apply. Never authorization.
	SkillSlug string
}

// For returns the actor acting for a run of runner with its pinned skill
// (nil without one). Hosts build every run-scoped actor this way, so the
// rules resolved for a run never depend on which caller assembled the actor.
func (a Actor) For(runner Runner, skill *SkillRef) Actor {
	a.Runner, a.SkillSlug = runner, ""
	if skill != nil {
		a.SkillSlug = skill.Slug
	}
	return a
}

func (a Actor) human() bool   { return a.Purpose == "" && a.UserID != "" && a.Role != "viewer" }
func (a Actor) manager() bool { return a.Role == "owner" || a.Role == "admin" }
