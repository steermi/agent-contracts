package activity

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

type State string

const (
	Suggested State = "suggested"
	Ready     State = "ready"
	Running   State = "running"
	Submitted State = "submitted"
	Waiting   State = "waiting"
	Completed State = "completed"
	Failed    State = "failed"
	Cancelled State = "cancelled"
)

type Location string

const (
	Local Location = "local"
	Cloud Location = "cloud"
)

func (l Location) Valid() bool { return l == Local || l == Cloud }

// Blocker explains why a run is waiting or was cancelled.
type Blocker string

const (
	BlockerMeetingInput         Blocker = "meeting_input"
	BlockerInputsChanged        Blocker = "inputs_changed"
	BlockerSourceUnavailable    Blocker = "source_unavailable"
	BlockerExecutionBusy        Blocker = "execution_busy"
	BlockerRepairLimit          Blocker = "repair_limit"
	BlockerReassessment         Blocker = "reassessment_required"
	BlockerExecutionFailed      Blocker = "execution_failed"
	BlockerNeedsClarification   Blocker = "needs_clarification"
	BlockerAwaitingResume       Blocker = "awaiting_resume"
	BlockerLocalDisconnected    Blocker = "local_disconnected"
	BlockerExecutionUnavailable Blocker = "execution_unavailable"
	// A cloud run that could not start because the organization's monthly AI
	// spend or message limit is reached. Resumable once the limit is raised.
	BlockerOrgBudget    Blocker = "org_budget"
	BlockerMessageLimit Blocker = "message_limit"
	// Loop stop reasons (FlowNext): the loop's budget is spent, its
	// findings or revision stopped changing, or its review was inconclusive.
	BlockerBudget             Blocker = "budget"
	BlockerLoopStalled        Blocker = "loop_stalled"
	BlockerReviewInconclusive Blocker = "review_inconclusive"
)

// final blockers can never be resumed; the run must be requested again.
func (b Blocker) final() bool { return b == BlockerRepairLimit || b == BlockerInputsChanged }

// Resumable blockers are cleared by take_local/resume_cloud, not by confirm.
func (b Blocker) Resumable() bool {
	switch b {
	case BlockerNeedsClarification, BlockerAwaitingResume, BlockerLocalDisconnected, BlockerExecutionUnavailable, BlockerOrgBudget, BlockerMessageLimit:
		return true
	}
	return false
}

// Purpose identifies a non-human caller. Humans have an empty purpose.
type Purpose string

const (
	PurposeExecutor    Purpose = "activity_executor"
	PurposeCoordinator Purpose = "activity_coordinator"
	PurposeVerifiedGit Purpose = "verified_git"
	// PurposeFlow advances a delivery flow along an edge set to auto, as the
	// run's owner. It may only issue Next when the pinned edge allows it.
	PurposeFlow Purpose = "flow"
)

type DecisionKind string

const (
	Accept         DecisionKind = "accept"
	RequestChanges DecisionKind = "request_changes"
	Reject         DecisionKind = "reject"
)

type Recommendation string

const (
	Approve          Recommendation = "approve"
	ChangesRequested Recommendation = "changes_requested"
	Inconclusive     Recommendation = "inconclusive"
)

type Severity string

const (
	Critical Severity = "critical"
	High     Severity = "high"
	Medium   Severity = "medium"
	Low      Severity = "low"
)

type CheckStatus string

const (
	CheckPending   CheckStatus = "pending"
	CheckSuccess   CheckStatus = "success"
	CheckFailed    CheckStatus = "failed"
	CheckCancelled CheckStatus = "cancelled"
	CheckUnknown   CheckStatus = "unknown"
)

const ProviderVerified = "provider_verified"

type CommandType string

const (
	Continue       CommandType = "continue"
	Confirm        CommandType = "confirm"
	Retry          CommandType = "retry"
	Start          CommandType = "start"
	Clarify        CommandType = "clarify"
	TakeLocal      CommandType = "take_local"
	ResumeCloud    CommandType = "resume_cloud"
	Handoff        CommandType = "handoff"
	Fail           CommandType = "fail"
	Cancel         CommandType = "cancel"
	Submit         CommandType = "submit"
	SubmitFindings CommandType = "findings"
	Decide         CommandType = "decision"
	Revise         CommandType = "revise"
	// Refine asks for another round of the same step with a person's feedback:
	// a successor of the same runner on the same flow node, with the run's
	// outputs pinned as inputs so the work is updated, not redone.
	Refine CommandType = "refine"

	// MaxFeedbackRunes bounds a Refine reason; MaxAssumptions and
	// MaxAssumptionRunes bound a submission's assumptions.
	MaxFeedbackRunes               = 4000
	MaxAssumptions                 = 12
	MaxAssumptionRunes             = 300
	Next               CommandType = "next"
	ApplyFixes         CommandType = "apply_fixes"
	// DismissFinding records why a person set one finding of a submitted
	// findings run aside (Finding.DismissalReason): applied by hand, or not
	// worth acting on. It never changes the evidence digest; like answers
	// it locks once the run hands off or someone decides on it (notesOpen).
	DismissFinding CommandType = "dismiss_finding"
	// ResolveFinding records a person's answer to a decide finding of a
	// submitted findings run (Finding.Resolution: Choice, and Reason as the
	// text). It never changes the evidence digest; answering again replaces
	// the earlier answer until the run hands off or someone decides on it.
	ResolveFinding CommandType = "resolve_finding"
	// SetStepMode changes the run's StepMode while its flow runs (owner or
	// manager; any non-terminal run, so also a submitted run whose next hop
	// has not started yet).
	SetStepMode CommandType = "set_step_mode"
	// ApproveStep and RequestStepChanges are a person's decision at the
	// approval node the run's flow stopped at (Run.Approval): recorded as a
	// StepDecision, it selects the node's hop for that outcome. The reason
	// is required for RequestStepChanges (≤ MaxFeedbackRunes) and becomes
	// the Feedback of the step the hop starts.
	ApproveStep        CommandType = "approve_step"
	RequestStepChanges CommandType = "request_step_changes"
	// MaxFindingNoteRunes bounds a person's note on a finding: a
	// DismissFinding reason or a ResolveFinding text.
	MaxFindingNoteRunes = 600
	// MaxRunIDBytes bounds a run id a request refers to (LinkedFromID).
	MaxRunIDBytes = 128
	// Internal commands; never accepted from an API caller.
	CloseMeeting       CommandType = "close_meeting"
	RecordMeetingRecap CommandType = "meeting_recap"
	StopMeetingTurn    CommandType = "stop_turn"
	TurnComplete       CommandType = "turn_complete"
	Interrupt          CommandType = "interrupt"
	RecordCheck        CommandType = "check"
	RecordCost         CommandType = "cost"
)

// Public reports whether API callers may send the command.
func (t CommandType) Public() bool {
	switch t {
	case CloseMeeting, StopMeetingTurn, Continue, Confirm, Retry, Clarify, TakeLocal, ResumeCloud, Cancel, Submit, SubmitFindings, Decide, Revise, Next, ApplyFixes, Refine, DismissFinding, ResolveFinding, SetStepMode, ApproveStep, RequestStepChanges:
		return true
	}
	return false
}

// spawns reports whether the command starts a successor run (see Successor)
// instead of transitioning this one.
func (t CommandType) spawns() bool { return t == Retry || t == Next || t == ApplyFixes || t == Refine }

var (
	ErrValidation        = errors.New("ACTIVITY_INVALID_COMMAND")
	ErrStale             = errors.New("ACTIVITY_INPUTS_CHANGED")
	ErrForbidden         = errors.New("ACTIVITY_FORBIDDEN")
	ErrConflict          = errors.New("ACTIVITY_COMMAND_CONFLICT")
	ErrChecksPending     = errors.New("ACTIVITY_CHECKS_PENDING")
	ErrNotFound          = errors.New("ACTIVITY_NOT_FOUND")
	ErrExecutionBusy     = errors.New("ACTIVITY_EXECUTION_BUSY")
	ErrLeaseLost         = errors.New("ACTIVITY_LEASE_LOST")
	ErrSourceUnavailable = errors.New("ACTIVITY_SOURCE_UNAVAILABLE")
)

type Policy struct {
	RequiredChecks    []string `json:"required_checks,omitempty"`
	RequireAcceptance bool     `json:"require_acceptance"`
	ReviewerIDs       []string `json:"reviewer_ids,omitempty"`
	Quorum            int      `json:"quorum"`
	MaxRepairRounds   int      `json:"max_repair_rounds"`
	MaxCost           float64  `json:"max_cost"`
}

func (p Policy) Normalized() Policy {
	if p.MaxRepairRounds <= 0 {
		p.MaxRepairRounds = 6
	}
	if p.MaxCost <= 0 {
		p.MaxCost = 20
	}
	return p
}

func (p Policy) Validate() error {
	if p.MaxRepairRounds < 1 || p.MaxRepairRounds > 6 || p.Quorum < 0 || p.Quorum > 10 || p.MaxCost <= 0 {
		return ErrValidation
	}
	return nil
}

func (p Policy) allowsReviewer(userID string) bool {
	return len(p.ReviewerIDs) == 0 || slices.Contains(p.ReviewerIDs, userID)
}

type Check struct {
	RepositoryID  string      `json:"repository_id"`
	CommitSHA     string      `json:"commit_sha"`
	Name          string      `json:"name"`
	ProviderRunID string      `json:"provider_run_id"`
	Attempt       int64       `json:"attempt"`
	Status        CheckStatus `json:"status"`
	Provenance    string      `json:"provenance"`
	StartedAt     time.Time   `json:"started_at"`
	// DetailsURL links the provider's page of the check (absolute https,
	// no userinfo); Summary (≤ MaxCheckSummaryBytes) and FailedJobs (≤
	// MaxFailedJobs names, sorted) describe a result. They are third-party
	// text: recordCheck redacts, strips markdown links and images and
	// truncates them (CleanCheckDetails). Checks without them stay valid.
	DetailsURL string   `json:"details_url,omitempty"`
	Summary    string   `json:"summary,omitempty"`
	FailedJobs []string `json:"failed_jobs,omitempty"`
}
type Decision struct {
	UserID         string       `json:"user_id"`
	Kind           DecisionKind `json:"kind"`
	Reason         string       `json:"reason,omitempty"`
	EvidenceDigest string       `json:"evidence_digest"`
	At             time.Time    `json:"at"`
}

// MeetingTurn captures immutable settings and inputs for one dispatch generation.
type MeetingTurn struct {
	RevealedRounds   []string `json:"revealed_rounds,omitempty"`
	ActorID          string   `json:"actor_id"`
	ModelID          string   `json:"model_id"`
	ReasoningEffort  string   `json:"reasoning_effort"`
	ConfigVersion    int64    `json:"config_version"`
	ArtifactRevision int64    `json:"artifact_revision"`
	LastMessageID    string   `json:"last_message_id"`
}

type Run struct {
	Skill *SkillRef `json:"skill,omitempty"`
	Flow  *FlowRef  `json:"flow,omitempty"`
	// Preserve the team/rule that caused this run even after project transfers.
	ProjectTeamID           string         `json:"project_team_id,omitempty"`
	AutomationRuleID        string         `json:"automation_rule_id,omitempty"`
	AutomationRuleDigest    string         `json:"automation_rule_digest,omitempty"`
	ProjectOwnershipVersion int64          `json:"project_ownership_version,omitempty"`
	MeetingRecapSaved       bool           `json:"meeting_recap_saved,omitempty"`
	Turn                    *MeetingTurn   `json:"turn,omitempty"`
	WorkflowKey             string         `json:"workflow_key,omitempty"`
	WorkflowVersion         int            `json:"workflow_version,omitempty"`
	Subject                 Subject        `json:"subject"`
	ID                      string         `json:"id"`
	OrgID                   string         `json:"-"`
	ProjectID               string         `json:"project_id"`
	WorkItemID              string         `json:"work_item_id"`
	Runner                  Runner         `json:"runner"`
	State                   State          `json:"state"`
	Version                 int64          `json:"version"`
	Generation              int64          `json:"generation"`
	InputDigest             string         `json:"input_digest"`
	Inputs                  []OutputRef    `json:"inputs,omitempty"`
	CandidateAuthorID       string         `json:"candidate_author_id,omitempty"`
	OwnerID                 string         `json:"owner_id"`
	Location                Location       `json:"execution_location"`
	Origin                  string         `json:"origin"`
	RootID                  string         `json:"root_id"`
	PredecessorID           string         `json:"predecessor_id,omitempty"`
	Round                   int            `json:"round"`
	Cost                    float64        `json:"cost"`
	SessionID               string         `json:"session_id,omitempty"`
	LaunchID                string         `json:"launch_id,omitempty"`
	Prompt                  string         `json:"-"`
	OutputContract          string         `json:"output_contract"`
	Capabilities            CapabilitySet  `json:"-"`
	Outputs                 []OutputRef    `json:"outputs,omitempty"`
	Checks                  []Check        `json:"checks,omitempty"`
	Findings                []Finding      `json:"findings,omitempty"`
	Recommendation          Recommendation `json:"recommendation,omitempty"`
	Summary                 string         `json:"summary,omitempty"`
	// DroppedFindings counts the submitted findings the host refused to keep
	// (SubmitFindings): a docs page run's findings whose quotes do not occur
	// in the pinned texts. Omitted when zero.
	DroppedFindings int `json:"dropped_findings,omitempty"`
	// Assumptions the run made where its sources were silent (Submit).
	Assumptions []string `json:"assumptions,omitempty"`
	// Tried is the run's own one-line account of its approach (Submit,
	// SubmitFindings; ≤ MaxLoopTextRunes). A label for people and later loop
	// rounds, never an input to a stop.
	Tried string `json:"tried,omitempty"`
	// LinkedFromID names a run on another subject whose findings this run
	// applies (a docs fix on the page a check of another page found wrong;
	// ≤ MaxRunIDBytes). It is a reference, not a parent: the chains stay on
	// their subjects. Retry and Refine keep it (the same fix again); Next
	// and ApplyFixes successors do not (they follow this run, not the
	// check).
	LinkedFromID string `json:"linked_from_id,omitempty"`
	// Feedback is the person's change request the run works from (nil
	// without one). The text is a person's, not the owner's: hosts give it
	// to the model as data, never as instructions.
	Feedback *Feedback `json:"feedback,omitempty"`
	// SkillInputs is the validated start-form value of the run's skill
	// (skill/inputs), pinned at request time and given to the model as data.
	SkillInputs json.RawMessage `json:"skill_inputs,omitempty"`
	ResumeNote  string          `json:"resume_note,omitempty"`
	Decisions   []Decision      `json:"decisions,omitempty"`
	// StepDecisions are the decisions people made at the approval nodes the
	// run's flow stopped at, in order (ApproveStep, RequestStepChanges).
	StepDecisions []StepDecision `json:"step_decisions,omitempty"`
	Policy        Policy         `json:"policy"`
	Blocker       Blocker        `json:"blocker,omitempty"`
	// StepMode is the person's choice over automatic hops, stored empty for
	// flow and "ask" for ask; set at request, Confirm, Next or SetStepMode
	// and carried to successors (SuccessorRun).
	StepMode StepMode `json:"step_mode,omitempty"`
	// BlockerDetail names the cause behind Blocker when the host knows it
	// (set by Interrupt, cleared with the blocker).
	BlockerDetail BlockerDetail `json:"blocker_detail,omitempty"`
	CreatedAt     time.Time     `json:"created_at"`
	UpdatedAt     time.Time     `json:"updated_at"`
}
type Request struct {
	Skill                *SkillRef `json:"skill,omitempty"`
	Flow                 *FlowRef  `json:"flow,omitempty"`
	AutomationRuleID     string    `json:"-"`
	AutomationRuleDigest string    `json:"-"`
	// ExpectedOwnershipVersion binds server-evaluated automation to its owner.
	ExpectedOwnershipVersion int64       `json:"-"`
	WorkflowKey              string      `json:"workflow_key,omitempty"`
	WorkflowVersion          int         `json:"workflow_version,omitempty"`
	Subject                  Subject     `json:"subject"`
	Runner                   Runner      `json:"runner"`
	Location                 Location    `json:"execution_location"`
	InputDigest              string      `json:"input_digest"`
	Inputs                   []OutputRef `json:"inputs,omitempty"`
	CommandKey               string      `json:"command_key"`
	SourceEventID            string      `json:"source_event_id,omitempty"`
	Origin                   string      `json:"origin"`
	CausationID              string      `json:"causation_id,omitempty"`
	PredecessorID            string      `json:"predecessor_id,omitempty"`
	// LinkedFromID: Run.LinkedFromID of the new run (docs page runs only).
	LinkedFromID string `json:"linked_from_id,omitempty"`
	// Feedback carries a person's change request into the successor
	// (SuccessorRun.Feedback).
	Feedback *Feedback `json:"feedback,omitempty"`
	// SkillInputs is the validated value of the skill's inputs schema. Hosts
	// validate it (skill.Skill.Inputs) and fold it into InputDigest.
	SkillInputs json.RawMessage `json:"skill_inputs,omitempty"`
	// StepMode is the new run's step mode (empty = flow). A successor takes
	// SuccessorRun.StepMode, which carries the chain's.
	StepMode StepMode `json:"step_mode,omitempty"`
	Policy   Policy   `json:"-"`
}

// Command is one change a caller asks of a run (Run.Apply) or a follow-up
// it starts (Run.Successor). Reason is the person's free text for every
// command, whatever the command calls it: a resume note (TakeLocal,
// ResumeCloud), a decision's reason (Decide, ApproveStep,
// RequestStepChanges), feedback (Refine), a dismissal reason
// (DismissFinding) or an answer's text (ResolveFinding); for the runner's
// Clarify and Fail it is the runner's question or failure. Commands never
// add a field of their own for that text.
type Command struct {
	MeetingRecapSaved bool           `json:"-"`
	Turn              *MeetingTurn   `json:"-"`
	MeetingInputs     []OutputRef    `json:"-"`
	Type              CommandType    `json:"command"`
	CommandKey        string         `json:"command_key"`
	ExpectedVersion   int64          `json:"expected_version"`
	InputDigest       string         `json:"input_digest,omitempty"`
	Outputs           []OutputRef    `json:"outputs,omitempty"`
	Findings          []Finding      `json:"findings,omitempty"`
	Recommendation    Recommendation `json:"recommendation,omitempty"`
	Summary           string         `json:"summary,omitempty"`
	Assumptions       []string       `json:"assumptions,omitempty"`
	Tried             string         `json:"tried,omitempty"`
	Decision          DecisionKind   `json:"decision,omitempty"`
	EvidenceDigest    string         `json:"evidence_digest,omitempty"`
	Reason            string         `json:"reason,omitempty"`
	// FindingID names the finding a DismissFinding command sets aside or a
	// ResolveFinding command answers.
	FindingID string `json:"finding_id,omitempty"`
	// Choice is ResolveFinding's answer (required); Reason carries its text
	// (FindingResolution.Text), as for every command (Command).
	Choice ResolutionChoice `json:"choice,omitempty"`
	Check  *Check           `json:"check,omitempty"`
	// StepMode: SetStepMode's new mode (required); optional on Confirm and
	// on Next (the successor's mode, Run.Successor).
	StepMode StepMode `json:"step_mode,omitempty"`
	// Location: optional on Confirm of a suggestion (it runs there) and on
	// Next (the successor runs there, Run.Successor). Owner only.
	Location Location `json:"location,omitempty"`
	// Internal payloads, never decoded from API bodies.
	// Facts are the host's facts about the run for this command
	// (HostFacts); only step decisions read them.
	Facts   HostFacts `json:"-"`
	Blocker Blocker   `json:"-"`
	// BlockerDetail travels with Blocker on Interrupt (Run.BlockerDetail).
	BlockerDetail BlockerDetail `json:"-"`
	Cost          float64       `json:"-"`
	// DroppedFindings is set by the host on SubmitFindings: how many
	// submitted findings it dropped before the command (Run.DroppedFindings).
	DroppedFindings int `json:"-"`
}

func NewRun(id, org, project, item string, a Actor, q Request, parent *Run) (Run, error) {
	d, ok := Lookup(q.Runner)
	if !ok || id == "" || org == "" || a.UserID == "" || q.InputDigest == "" || !q.Location.Valid() || !q.StepMode.Valid() {
		return Run{}, ErrValidation
	}
	subject := q.Subject
	if subject.Kind == "" {
		subject = Subject{SubjectWorkItem, item, "project", project}
	}
	if subject.Validate(q.Runner, q.Location) != nil {
		return Run{}, ErrValidation
	}
	if subject.Kind == SubjectWorkItem && (subject.ID != item || subject.ScopeID != project) {
		return Run{}, ErrValidation
	}
	if subject.Kind == SubjectSession && (project != "" || item != "" || parent != nil) {
		return Run{}, ErrValidation
	}
	if !validPageChain(project, item, subject, parent) || !validLink(id, q, parent) {
		return Run{}, ErrValidation
	}
	p := q.Policy.Normalized()
	now := time.Now().UTC()
	r := Run{
		ID: id, OrgID: org, ProjectID: project, WorkItemID: item, Runner: q.Runner, State: Suggested,
		Version: 1, Generation: 1, InputDigest: q.InputDigest, Inputs: q.Inputs, OwnerID: a.UserID,
		Location: q.Location, Origin: q.Origin, RootID: id, Prompt: Identity,
		OutputContract: string(q.Runner) + "/1", Capabilities: d.Capabilities, Policy: p, CreatedAt: now, UpdatedAt: now,
	}
	r.Subject = subject
	r.StepMode = q.StepMode.stored()
	if q.Feedback != nil {
		f, err := q.Feedback.cleaned()
		if err != nil {
			return Run{}, err
		}
		r.Feedback = f
	}
	if len(q.SkillInputs) > 0 {
		r.SkillInputs = append(json.RawMessage(nil), q.SkillInputs...)
	}
	if q.Skill != nil {
		ref := *q.Skill
		r.Skill = &ref
	}
	if q.Flow != nil {
		ref := *q.Flow
		r.Flow = &ref
	}
	if subject.Kind == SubjectSession {
		r.SessionID = subject.ID
		r.State = Waiting
		r.Blocker = BlockerMeetingInput
		r.Capabilities = CapabilitySet{}
		if r.IsMeeting() {
			r.Capabilities = CapabilitiesForMeeting()
		}
	}
	if subject.Kind == SubjectDocPage {
		r.Capabilities = CapabilitiesForDocPage(q.Runner)
		r.LinkedFromID = q.LinkedFromID
	}
	if q.Runner == Findings {
		r.Outputs = Sources(q.Inputs)
	}
	if parent == nil {
		return r, nil
	}
	r.RootID = parent.RootID
	r.PredecessorID = parent.ID
	r.Generation = parent.Generation
	r.Round = parent.Round
	r.Cost = parent.Cost
	if q.Runner == Findings {
		r.CandidateAuthorID = parent.OwnerID
		r.Outputs = slices.Clone(parent.Outputs)
		r.Checks = slices.Clone(parent.Checks)
	}
	if q.Runner == Code && (parent.Runner == Findings || parent.Runner == Code) {
		r.Round++
	}
	switch {
	case r.Round >= p.MaxRepairRounds || r.Cost >= p.MaxCost:
		r.State, r.Blocker = Waiting, BlockerRepairLimit
	case r.Round >= 3 && q.Location == Cloud && (r.Flow == nil || r.Flow.Loop == nil):
		// Inside a quality loop its MaxIterations bounds the rounds instead.
		r.State, r.Blocker = Waiting, BlockerReassessment
	case a.Purpose == PurposeFlow:
		// An automatic hop starts without a confirmation. Successor's guard
		// (flowGuard: AutoHop) admitted it and placed it on the hop's
		// location; NewRun takes that placement as given.
		r.State = Ready
	}
	return r, nil
}

// AttachExecution records the session and launch serving the current
// generation. Execution resources never change the run's state.
func (r *Run) AttachExecution(session, launch string) error {
	if r.State != Ready && r.State != Running {
		return ErrStale
	}
	if r.SessionID != "" && r.SessionID != session || r.LaunchID != "" && launch != "" && r.LaunchID != launch {
		return ErrConflict
	}
	r.SessionID = session
	if launch != "" {
		r.LaunchID = launch
	}
	return nil
}

func (r Run) ActiveExecution() bool { return r.State == Ready || r.State == Running }

// AwaitsStart reports whether the run waits for a person to start it: a
// suggestion, or one the host held because its subject was busy with
// another run (BlockerExecutionBusy). Hosts tell its owner so.
func (r Run) AwaitsStart() bool {
	return r.State == Suggested || r.State == Waiting && r.Blocker == BlockerExecutionBusy
}

// AcceptsFollowUp reports whether a person may ask follow-up questions in
// the run's session (read only, CapabilitiesForFollowUp): once it stopped
// working (submitted, completed, failed, cancelled). A waiting run resumes
// in its session, so a follow-up there would become part of what it reads;
// unknown states fail closed.
func (r Run) AcceptsFollowUp() bool {
	switch r.State {
	case Submitted, Completed, Failed, Cancelled:
		return true
	}
	return false
}
func (r Run) Terminal() bool {
	return r.State == Cancelled || r.State == Failed || r.State == Completed
}
func (r Run) EvidenceDigest() string {
	return Digest(struct {
		Input          string
		Outputs        []OutputRef
		Checks         []Check
		Findings       []Finding
		Recommendation Recommendation
	}{r.InputDigest, r.Outputs, r.Checks, evidenceFindings(r.Findings), r.Recommendation})
}

func Digest(v any) string {
	b, _ := json.Marshal(v)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
func (r Run) HumanAccepted() bool {
	if r.Blocker != "" || r.State == Cancelled || r.State == Failed {
		return false
	}
	digest := r.EvidenceDigest()
	votes := map[string]bool{}
	for _, d := range r.Decisions {
		if d.EvidenceDigest == digest {
			votes[d.UserID] = d.Kind == Accept
		}
	}
	accepted := 0
	for _, accept := range votes {
		if accept {
			accepted++
		}
	}
	return accepted >= max(r.Policy.Quorum, 1)
}

// Successor validates a command that starts a follow-up run and returns the
// follow-up: runner, location and step mode, decided in one pass. The
// coordinator resolves its inputs and creates it.
func (r Run) Successor(a Actor, c Command) (SuccessorRun, error) {
	if !c.Type.spawns() {
		return SuccessorRun{}, ErrValidation
	}
	if c.ExpectedVersion != r.Version {
		return SuccessorRun{}, ErrStale
	}
	if err := r.guard(a, c); err != nil {
		return SuccessorRun{}, err
	}
	switch c.Type {
	case Retry:
		return r.successorRun(a, c, r.Runner, FlowHop{}), nil
	case Refine:
		if f := strings.TrimSpace(c.Reason); f == "" || utf8.RuneCountInString(f) > MaxFeedbackRunes {
			return SuccessorRun{}, ErrValidation
		}
		return r.successorRun(a, c, r.Runner, FlowHop{}), nil
	case ApplyFixes:
		hop, _ := r.reworkHop()
		return r.successorRun(a, c, hop.Runner, hop), nil
	}
	hop, _, _ := r.FlowNext()
	return r.successorRun(a, c, hop.Runner, hop), nil
}

// Apply validates before changing the snapshot. Persistence and idempotency are
// owned by the coordinator transaction, not by the model or a browser timer.
func (r *Run) Apply(a Actor, c Command) error {
	if c.ExpectedVersion != r.Version || c.InputDigest != "" && c.Type != Revise && !(r.IsSessionWorkflow() && c.Type == Continue) && c.InputDigest != r.InputDigest {
		return ErrStale
	}
	if c.Type.spawns() {
		return ErrValidation
	}
	if err := r.guard(a, c); err != nil {
		return err
	}
	next := *r
	next.Outputs = slices.Clone(r.Outputs)
	next.Checks = slices.Clone(r.Checks)
	next.Decisions = slices.Clone(r.Decisions)
	next.Findings = slices.Clone(r.Findings)
	if err := next.apply(a, c); err != nil {
		return err
	}
	// The detail explains the blocker an Interrupt set; any other command
	// that changes the blocker drops it.
	if c.Type != Interrupt && next.Blocker != r.Blocker {
		next.BlockerDetail = ""
	}
	next.Version++
	next.UpdatedAt = time.Now().UTC()
	*r = next
	return nil
}

// apply validates the payload and mutates the run; guard already admitted the
// actor and state.
func (r *Run) apply(a Actor, c Command) error {
	if sessionCommands[c.Type] {
		r.applySession(c)
		return nil
	}
	switch c.Type {
	case Confirm:
		// Only a suggestion takes a location (choiceGuard): it has no
		// execution yet. A waiting run moves with TakeLocal/ResumeCloud.
		if c.Location != "" {
			r.Location = c.Location
		}
		if c.StepMode != "" {
			r.StepMode = c.StepMode.stored()
		}
		r.State, r.Blocker = Ready, ""
	case SetStepMode:
		if c.StepMode == "" {
			return ErrValidation
		}
		r.StepMode = c.StepMode.stored()
	case Start:
		r.State = Running
	case Clarify:
		if strings.TrimSpace(c.Reason) == "" {
			return ErrValidation
		}
		r.State, r.Blocker, r.Summary = Waiting, BlockerNeedsClarification, c.Reason
	case TakeLocal, ResumeCloud:
		if c.Turn != nil {
			snapshot := *c.Turn
			r.Turn = &snapshot
		}
		if r.Blocker == BlockerNeedsClarification && strings.TrimSpace(c.Reason) == "" {
			return ErrValidation
		}
		r.ResumeNote = strings.TrimSpace(c.Reason)
		r.Generation++
		r.LaunchID = ""
		r.State, r.Blocker = Ready, ""
		r.Location = Local
		if c.Type == ResumeCloud {
			r.Location = Cloud
		}
	case Handoff:
		r.State, r.Blocker = Waiting, BlockerAwaitingResume
	case Interrupt:
		if c.Blocker == "" || !c.BlockerDetail.Explains(c.Blocker) {
			return ErrValidation
		}
		r.State, r.Blocker, r.BlockerDetail = Waiting, c.Blocker, c.BlockerDetail
		if r.IsSessionWorkflow() {
			r.Generation++
			r.State, r.Blocker, r.BlockerDetail = Waiting, BlockerMeetingInput, ""
			r.Summary = string(c.Blocker)
		} else if c.Blocker == BlockerInputsChanged {
			r.State = Cancelled
		}
	case Fail:
		r.State, r.Blocker = Failed, BlockerExecutionFailed
		if r.IsSessionWorkflow() {
			r.Generation++
			r.State, r.Blocker, r.Summary = Waiting, BlockerMeetingInput, c.Reason
		}
	case Cancel:
		r.State = Cancelled
	case Submit:
		return r.submit(c)
	case SubmitFindings:
		if c.Summary == "" || c.DroppedFindings < 0 || !slices.Contains([]Recommendation{Approve, ChangesRequested, Inconclusive}, c.Recommendation) {
			return ErrValidation
		}
		// Notes and stray patches are dropped (Finding.submitted), then
		// what remains is validated.
		findings := slices.Clone(c.Findings)
		seen := make(map[string]bool, len(c.Findings))
		for i, f := range findings {
			f = f.submitted()
			if f.ID == "" || seen[f.ID] || f.Description == "" || !slices.Contains([]Severity{Critical, High, Medium, Low}, f.Severity) || f.validateReference() != nil || r.decidesItself(f) {
				return ErrValidation
			}
			seen[f.ID] = true
			findings[i] = f
		}
		if r.ContradictsApprove(c) {
			return fmt.Errorf("%w: approve contradicts a finding rated %s or above; recommend changes_requested, or lower the severity of findings that do not block", ErrValidation, r.BlockingSeverity())
		}
		r.Findings = findings
		tried, err := cleanTried(c.Tried)
		if err != nil {
			return err
		}
		r.Tried = tried
		r.DroppedFindings = c.DroppedFindings
		r.Summary = c.Summary
		r.Recommendation = c.Recommendation
		r.State = Submitted
	case RecordCheck:
		return r.recordCheck(c.Check)
	case RecordCost:
		if c.Cost < 0 {
			return ErrValidation
		}
		r.Cost += c.Cost
	case Decide:
		if c.EvidenceDigest != r.EvidenceDigest() {
			return ErrStale
		}
		if !slices.Contains([]DecisionKind{Accept, RequestChanges, Reject}, c.Decision) {
			return ErrValidation
		}
		if c.Decision == Accept && !r.ChecksPassed() {
			return ErrChecksPending
		}
		if c.Decision != Accept && strings.TrimSpace(c.Reason) == "" {
			return ErrValidation
		}
		r.Decisions = append(r.Decisions, Decision{a.UserID, c.Decision, c.Reason, c.EvidenceDigest, time.Now().UTC()})
	case DismissFinding:
		return r.dismissFinding(c)
	case ResolveFinding:
		return r.resolveFinding(a, c)
	case ApproveStep, RequestStepChanges:
		return r.decideStep(a, c)
	case Revise:
		if c.InputDigest == "" {
			return ErrValidation
		}
		if c.InputDigest != r.InputDigest {
			r.State, r.Blocker = Cancelled, BlockerInputsChanged
		}
	}
	return nil
}
