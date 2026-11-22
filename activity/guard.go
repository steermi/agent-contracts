package activity

import "slices"

// guard is the single eligibility rule: who may issue a command, with its
// choices (location, step mode), in which state. Apply, Successor,
// AllowedCommands and CanChooseLocation all ask it; apply adds only payload
// validation.
func (r Run) guard(a Actor, c Command) error {
	if err := r.stateGuard(a, c.Type, c.Facts); err != nil {
		return err
	}
	return r.choiceGuard(a, c)
}

// stateGuard is guard's table of who may issue a command type in which
// state; f are the host's facts about the run (only step decisions read
// them).
func (r Run) stateGuard(a Actor, t CommandType, f HostFacts) error {
	owner := r.ownedBy(a)
	if sessionCommands[t] {
		return r.sessionGuard(a, t, owner)
	}
	if r.IsSessionWorkflow() && !r.sessionAllows(t) {
		return ErrForbidden
	}
	// Docs page runs are cloud only: nothing moves them to, or waits for, a
	// local agent. ResumeCloud stays: it resumes a blocked run in the cloud.
	if r.IsDocPage() && (t == TakeLocal || t == Handoff) {
		return ErrForbidden
	}
	manager := owner || a.manager()
	switch t {
	case Confirm:
		if !manager {
			return ErrForbidden
		}
		if r.Blocker.final() {
			return ErrStale
		}
		if r.State != Suggested && (r.State != Waiting || r.Blocker.Resumable()) {
			return ErrConflict
		}
	case Start, Clarify, Handoff:
		if !owner {
			return ErrForbidden
		}
		if t == Start && r.State != Ready || t != Start && r.State != Running {
			return ErrConflict
		}
	case TakeLocal, ResumeCloud:
		if !r.ownerAsPerson(a) {
			return ErrForbidden
		}
		// A suggestion moves with Confirm's location; moving applies once a
		// run is underway or interrupted.
		if r.State == Suggested || r.State == Submitted || r.State == Completed || r.State == Cancelled {
			return ErrConflict
		}
		if r.Blocker.final() {
			return ErrStale
		}
	case Fail:
		if !owner {
			return ErrForbidden
		}
		if !r.ActiveExecution() {
			return ErrConflict
		}
	case Cancel:
		if !manager {
			return ErrForbidden
		}
		// A current submission awaits a decision; only a stale one is withdrawn.
		if r.Terminal() || r.State == Submitted && r.Blocker == "" {
			return ErrConflict
		}
	case Submit, SubmitFindings:
		if !owner {
			return ErrForbidden
		}
		if r.State != Running || (r.Runner == Findings) != (t == SubmitFindings) {
			return ErrConflict
		}
	case Decide:
		if !a.human() || a.UserID == r.OwnerID || a.UserID == r.CandidateAuthorID || !r.Policy.allowsReviewer(a.UserID) {
			return ErrForbidden
		}
		if r.Blocker != "" {
			return ErrStale
		}
		if r.State != Submitted && r.State != Completed {
			return ErrConflict
		}
	case Revise:
		if !a.human() {
			return ErrForbidden
		}
	case Retry:
		if !r.ownerAsPerson(a) {
			return ErrForbidden
		}
		if r.State != Failed {
			return ErrConflict
		}
	case Refine:
		// A person (owner or manager) refines submitted answer or code work.
		if !manager || a.Purpose != "" {
			return ErrForbidden
		}
		if r.Blocker != "" {
			return ErrStale
		}
		if (r.Runner != Answer && r.Runner != Code) || (r.State != Submitted && r.State != Completed) || len(r.Outputs) == 0 {
			return ErrConflict
		}
	case Next, ApplyFixes:
		if a.Purpose == PurposeFlow {
			return r.flowGuard(a, t)
		}
		if !manager || a.Purpose != "" {
			return ErrForbidden
		}
		if r.Blocker != "" {
			return ErrStale
		}
		if t == Next && r.NextRunner() == "" || t == ApplyFixes && !r.canApplyFixes() {
			return ErrConflict
		}
	case SetStepMode:
		if !manager || !a.human() {
			return ErrForbidden
		}
		if !r.stepModeOpen() {
			return ErrConflict
		}
	case ApproveStep, RequestStepChanges:
		// A person the approval node's approver setting names, on a run
		// that waits there (pendingApproval), for a decision the node has
		// an edge for, unless the host's fence makes it moot.
		if hop := r.flowPath().hop(); !a.human() || hop.decides() && !r.mayDecideStep(a, f, hop.Approver) {
			return ErrForbidden
		}
		if r.Blocker != "" {
			return ErrStale
		}
		at, waits := r.pendingApproval()
		if _, has := at.Decide[stepOutcome(t)]; !waits || !has {
			return ErrConflict
		}
		if err := f.Fence.err(); err != nil {
			return err
		}
	case DismissFinding:
		// The run's owner or a manager sets findings aside, as people.
		if !manager || !a.human() {
			return ErrForbidden
		}
		if r.Blocker != "" {
			return ErrStale
		}
		if r.Runner != Findings || (r.State != Submitted && r.State != Completed) || !slices.ContainsFunc(r.Findings, Finding.open) {
			return ErrConflict
		}
	case Interrupt:
		if a.Purpose != PurposeCoordinator {
			return ErrForbidden
		}
		if r.Terminal() {
			return ErrConflict
		}
	case RecordCheck:
		if a.Purpose != PurposeVerifiedGit {
			return ErrForbidden
		}
	case RecordCost:
		if a.Purpose != PurposeExecutor && a.Purpose != PurposeCoordinator {
			return ErrForbidden
		}
	default:
		return ErrValidation
	}
	return nil
}

// userCommands are the commands a person can act on from the run's controls.
var userCommands = []CommandType{CloseMeeting, StopMeetingTurn, Continue, Confirm, Retry, TakeLocal, ResumeCloud, Next, ApplyFixes, Refine, Submit, SubmitFindings, Decide, DismissFinding, SetStepMode, ApproveStep, RequestStepChanges, Cancel}

// AllowedCommands lists the controls this person may use now, given the
// host's facts about the run (HostFacts; zero when the host has none).
// Machine callers act through their own protocols and get none.
func (r Run) AllowedCommands(a Actor, f HostFacts) []CommandType {
	out := []CommandType{}
	if !a.human() {
		return out
	}
	for _, t := range userCommands {
		if r.guard(a, Command{Type: t, Facts: f}) == nil {
			out = append(out, t)
		}
	}
	return out
}
