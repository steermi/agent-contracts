package activity

// A person's choices beside a command: where a run executes (Location on
// Confirm and Next) and whether its flow continues by itself (StepMode),
// plus the cause code an Interrupt records beside its blocker.

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// StepMode is a person's per-run choice over a flow's automatic hops:
// flow follows the pinned hops, ask pauses after every step ("Pause after
// each step") so no hop starts without a person. It is carried to every
// successor of the chain. One representation everywhere: empty and "flow"
// mean flow (a run stores flow as empty, so stored runs without the field
// read flow), "ask" means ask.
type StepMode string

const (
	StepModeFlow StepMode = "flow"
	StepModeAsk  StepMode = "ask"
)

// Valid reports whether m is empty (= flow) or a known mode.
func (m StepMode) Valid() bool { return m == "" || m == StepModeFlow || m == StepModeAsk }

// OrDefault is the effective mode: flow when unset. Hosts show this value.
func (m StepMode) OrDefault() StepMode {
	if m == "" {
		return StepModeFlow
	}
	return m
}

// stored is the one encoding a run keeps: the default (flow) is empty, so
// an explicit "flow" and an omitted mode never differ.
func (m StepMode) stored() StepMode {
	if m == StepModeFlow {
		return ""
	}
	return m
}

// BlockerDetail is a safe cause code beside Blocker. The host sets it on an
// Interrupt (Command.BlockerDetail, never decoded from API bodies); any
// other command that changes the run's blocker clears it.
type BlockerDetail string

const (
	BlockerDetailRepositoryUnreachable BlockerDetail = "repository_unreachable"
	BlockerDetailAccessLost            BlockerDetail = "access_lost"
	BlockerDetailInputsMoved           BlockerDetail = "inputs_moved"
)

// blockerDetails lists the blockers each detail may explain. Interrupt
// refuses any other pairing.
var blockerDetails = map[BlockerDetail][]Blocker{
	BlockerDetailRepositoryUnreachable: {BlockerSourceUnavailable},
	BlockerDetailAccessLost:            {BlockerSourceUnavailable},
	BlockerDetailInputsMoved:           {BlockerInputsChanged},
}

// Valid reports whether d is empty or a known detail.
func (d BlockerDetail) Valid() bool {
	_, ok := blockerDetails[d]
	return d == "" || ok
}

// Explains reports whether d may stand beside blocker b (empty always may).
func (d BlockerDetail) Explains(b Blocker) bool {
	return d == "" || slices.Contains(blockerDetails[d], b)
}

// stepModeOpen is the one rule for choosing a step mode, whether by
// SetStepMode or beside Confirm / Next: the run is placed on a flow that can
// still continue (not terminal, no final blocker and, once submitted, only
// while it has a next step to start).
func (r Run) stepModeOpen() bool {
	return r.Flow != nil && !r.Terminal() && !r.Blocker.final() && (r.State != Submitted || r.NextRunner() != "")
}

// ownerAsPerson reports whether a is the run's owner acting as a person
// (no machine purpose): who may move a run or choose where it runs.
func (r Run) ownerAsPerson(a Actor) bool { return a.UserID == r.OwnerID && a.Purpose == "" }

// choiceGuard admits the optional per-command choices; guard calls it
// after the state table, so every caller gets both. A location rides only
// on a suggestion's Confirm or on Next, from the run's owner as a person,
// at a location the subject allows; a step mode rides on Confirm and Next
// under the same rule as SetStepMode (stateGuard), from a person.
func (r Run) choiceGuard(a Actor, c Command) error {
	if c.Location != "" {
		if c.Type != Confirm && c.Type != Next || !c.Location.Valid() {
			return ErrValidation
		}
		if !r.ownerAsPerson(a) {
			return ErrForbidden
		}
		// A waiting run moves with TakeLocal/ResumeCloud, which fence its
		// execution; only a suggestion has none yet.
		if c.Type == Confirm && r.State != Suggested {
			return ErrConflict
		}
		// Docs page runs are cloud only.
		if r.IsDocPage() && c.Location != Cloud {
			return ErrValidation
		}
	}
	if c.StepMode != "" {
		if c.Type != SetStepMode && c.Type != Confirm && c.Type != Next || !c.StepMode.Valid() {
			return ErrValidation
		}
		if !a.Human() {
			return ErrForbidden
		}
		if !r.stepModeOpen() {
			return ErrConflict
		}
	}
	return nil
}

// CanChooseLocation reports whether actor may pick where r's next start
// runs: a suggestion's Confirm, else Next, carrying a location. It asks the
// guard table, so hosts never restate who may.
func (r Run) CanChooseLocation(a Actor) bool {
	t := Next
	if r.State == Suggested {
		t = Confirm
	}
	return r.guard(a, Command{Type: t, Location: Local}) == nil && r.guard(a, Command{Type: t, Location: Cloud}) == nil
}

// SuccessorRun is the follow-up a spawning command starts: its runner,
// where it executes and the step mode it carries. Location: the person's
// choice on Next (LocationChosen), else the pinned location of the hop it
// follows (Next, ApplyFixes; the flow advancer always takes the pinned
// hop, which AutoHop requires to be cloud), else the run's own location
// (Retry, Refine, hops the host did not resolve). Step mode: the person's
// choice on Next, else the chain's, stored as a run stores it. Feedback is
// the person's change request the follow-up works from (Request.Feedback;
// nil without one): a Refine reason, or the reason of the "changes
// requested" decision at an approval node that chose the hop Next follows,
// whatever the follow-up's runner. Hosts request the successor with all of
// it, and NewRun takes it as given.
type SuccessorRun struct {
	Runner         Runner
	Location       Location
	LocationChosen bool
	StepMode       StepMode
	Feedback       *Feedback
	// LinkedFromID is the follow-up's Request.LinkedFromID: the run's own
	// link for Retry and Refine (the same step again applies the same
	// check's findings), "" for Next and ApplyFixes.
	LinkedFromID string
}

// Feedback is a person's change request a run works from: what they asked
// (Text, ≤ MaxFeedbackRunes), where it comes from (Source) and who asked
// (By, a user id).
type Feedback struct {
	Text   string         `json:"text"`
	Source FeedbackSource `json:"source"`
	By     string         `json:"by,omitempty"`
}

// FeedbackSource is where a run's Feedback comes from: a Refine command or
// a "changes requested" decision at an approval node.
type FeedbackSource string

const (
	FeedbackRefine       FeedbackSource = "refine"
	FeedbackStepDecision FeedbackSource = "step_decision"
)

// Valid reports whether s is a known source.
func (s FeedbackSource) Valid() bool { return s == FeedbackRefine || s == FeedbackStepDecision }

// cleaned is the feedback a run stores: trimmed text, nil without any; a
// source that is not known, or text over MaxFeedbackRunes, is refused.
func (f Feedback) cleaned() (*Feedback, error) {
	f.Text = strings.TrimSpace(f.Text)
	if f.Text == "" {
		return nil, nil
	}
	if !f.Source.Valid() || !utf8.ValidString(f.Text) || utf8.RuneCountInString(f.Text) > MaxFeedbackRunes {
		return nil, ErrValidation
	}
	return &f, nil
}

func (r Run) successorRun(a Actor, c Command, runner Runner, hop FlowHop) SuccessorRun {
	out := SuccessorRun{Runner: runner, Location: r.Location, StepMode: r.StepMode, Feedback: r.successorFeedback(a, c)}
	if c.Type == Retry || c.Type == Refine {
		out.LinkedFromID = r.LinkedFromID
	}
	switch {
	case c.Type == Next && c.Location != "":
		out.Location, out.LocationChosen = c.Location, true
	case hop.Location.Valid():
		out.Location = hop.Location
	}
	if c.Type == Next && c.StepMode != "" {
		out.StepMode = c.StepMode
	}
	out.StepMode = out.StepMode.stored()
	return out
}
