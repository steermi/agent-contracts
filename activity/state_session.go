package activity

import "slices"

// Session workflow runs (meetings, project and organization answer sessions)
// are driven by their participants instead of a single owner. They share the
// run model, but take their own command set: the commands below exist only
// on session runs, and the ticket-run follow-ups (Confirm, Retry, Next,
// ApplyFixes) never apply to them. guard dispatches here so the ticket-run
// rules stay free of session special cases.

// sessionCommands exist only on session workflow runs.
var sessionCommands = map[CommandType]bool{
	RecordMeetingRecap: true,
	CloseMeeting:       true,
	StopMeetingTurn:    true,
	Continue:           true,
	TurnComplete:       true,
}

// ownedBy reports whether a acts as the run's owner. In a session the
// executor, the facilitator and the person whose turn is running own it.
func (r Run) ownedBy(a Actor) bool {
	if r.IsSessionWorkflow() && a.MeetingRole != "" {
		return a.Purpose == PurposeExecutor || a.MeetingRole == "facilitator" || r.Turn != nil && a.UserID == r.Turn.ActorID
	}
	return a.UserID == r.OwnerID || a.Purpose == PurposeExecutor
}

// sessionAllows reports whether a session run takes a ticket-run command.
// Only project-scoped sessions move between cloud and local.
func (r Run) sessionAllows(t CommandType) bool {
	switch t {
	case Confirm, Next, ApplyFixes, Retry:
		return false
	case TakeLocal, ResumeCloud:
		return r.RunSubject().ScopeType == "project"
	}
	return true
}

func (r Run) sessionGuard(a Actor, t CommandType, owner bool) error {
	if !r.IsSessionWorkflow() {
		return ErrForbidden
	}
	switch t {
	case RecordMeetingRecap:
		if a.Purpose != PurposeCoordinator {
			return ErrForbidden
		}
	case CloseMeeting:
		if !a.human() || a.MeetingRole != "facilitator" {
			return ErrForbidden
		}
		if r.State != Waiting || r.Blocker != BlockerMeetingInput || !r.MeetingRecapSaved {
			return ErrConflict
		}
	case StopMeetingTurn:
		if !a.human() || !(owner || a.manager() || r.Turn != nil && a.UserID == r.Turn.ActorID) {
			return ErrForbidden
		}
		if !r.ActiveExecution() {
			return ErrConflict
		}
	case Continue:
		if !a.human() {
			return ErrForbidden
		}
		if r.State != Waiting || r.Blocker != BlockerMeetingInput {
			return ErrConflict
		}
	case TurnComplete:
		if a.Purpose != PurposeExecutor && a.UserID != r.OwnerID {
			return ErrForbidden
		}
		if r.State != Running {
			return ErrConflict
		}
	}
	return nil
}

func (r *Run) applySession(c Command) {
	switch c.Type {
	case RecordMeetingRecap:
		r.MeetingRecapSaved = c.MeetingRecapSaved
	case CloseMeeting:
		r.Generation++
		r.State, r.Blocker = Completed, ""
	case StopMeetingTurn:
		r.Generation++
		r.State, r.Blocker = Waiting, BlockerMeetingInput
	case Continue:
		r.LaunchID = ""
		r.Summary = ""
		if c.InputDigest != "" {
			r.InputDigest = c.InputDigest
			r.Inputs = slices.Clone(c.MeetingInputs)
		}
		if c.Turn != nil {
			snapshot := *c.Turn
			r.Turn = &snapshot
		}
		r.Generation++
		r.State, r.Blocker = Ready, ""
	case TurnComplete:
		r.State, r.Blocker = Waiting, BlockerMeetingInput
	}
}
