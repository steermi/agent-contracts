package activity

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Notes on a findings run's findings (DismissFinding, ResolveFinding) are
// people's, never evidence, but they change what a fix applies
// (FindingsFor), so one rule covers their lifecycle:
//
//   - notable: which findings each note command may note (any open one for
//     a dismissal, an open decide finding for an answer); stateGuard and
//     findingNote both read it;
//   - notesOpen: people may change notes while the run is merely submitted
//     and nobody decided on it, so a fix and a decision always cover the
//     notes they saw;
//   - locksNotes: the commands that end that (hand-off, a decision, a step
//     decision) are refused while a decide finding still waits for an
//     answer (OpenDecisions), so a question is never locked unanswered;
//   - evidenceFindings: the evidence digest ignores notes, and
//     SubmitFindings clears submitted ones (Finding.submitted).

// notable is the table of which findings a note command may note; nil for
// any other command.
func notable(t CommandType) func(Finding) bool {
	switch t {
	case DismissFinding:
		return Finding.open
	case ResolveFinding:
		return Finding.isDecision
	}
	return nil
}

// notesOpen reports whether people may still change notes on the run's
// findings.
func (r Run) notesOpen() bool {
	return r.Runner == Findings && r.State == Submitted && len(r.Decisions) == 0 && len(r.StepDecisions) == 0
}

// locksNotes reports whether a command of type t ends notesOpen: it hands
// the run off (Next, ApplyFixes, the flow advancer's Next; the host then
// completes it) or decides on it (Decide of any kind, ApproveStep,
// RequestStepChanges).
func locksNotes(t CommandType) bool {
	switch t {
	case Next, ApplyFixes, Decide, ApproveStep, RequestStepChanges:
		return true
	}
	return false
}

// findingNote validates a person's note command on one finding: the named
// finding exists (ErrValidation) and is one the command may note
// (notable, ErrConflict), and its trimmed text (c.Reason) is valid UTF-8
// within MaxFindingNoteRunes. It returns the finding's index and the text.
func (r Run) findingNote(c Command) (int, string, error) {
	text := strings.TrimSpace(c.Reason)
	if c.FindingID == "" || !utf8.ValidString(text) || utf8.RuneCountInString(text) > MaxFindingNoteRunes {
		return 0, "", ErrValidation
	}
	i := slices.IndexFunc(r.Findings, func(f Finding) bool { return f.ID == c.FindingID })
	if i < 0 {
		return 0, "", ErrValidation
	}
	if may := notable(c.Type); may == nil || !may(r.Findings[i]) {
		return 0, "", ErrConflict
	}
	return i, text, nil
}

// dismissFinding sets one open finding aside with the person's reason. The
// findings stay on the run; neither the evidence digest nor the outcome
// looks at dismissals, so decisions and followed flow hops keep matching.
// A dismissed decision drops its answer: a finding is answered or set
// aside, never both.
func (r *Run) dismissFinding(c Command) error {
	i, reason, err := r.findingNote(c)
	if err != nil {
		return err
	}
	if reason == "" {
		return ErrValidation
	}
	r.Findings[i].DismissalReason = reason
	r.Findings[i].Resolution = nil
	return nil
}

// resolveFinding records a person's answer to an open decide finding
// (Finding.isDecision); answering again replaces the earlier answer.
func (r *Run) resolveFinding(a Actor, c Command) error {
	if !c.Choice.Valid() {
		return ErrValidation
	}
	i, text, err := r.findingNote(c)
	if err != nil {
		return err
	}
	if c.Choice == ResolveOther && text == "" {
		return ErrValidation
	}
	r.Findings[i].Resolution = &FindingResolution{Choice: c.Choice, Text: text, By: a.UserID, At: time.Now().UTC()}
	return nil
}

// evidenceFindings is what the evidence digest covers: the findings as
// submitted. A dismissal or an answer is a person's note on the evidence,
// not evidence, so noting never invalidates decisions or re-keys a followed
// flow hop.
func evidenceFindings(in []Finding) []Finding {
	if !slices.ContainsFunc(in, Finding.noted) {
		return in
	}
	out := slices.Clone(in)
	for i := range out {
		out[i] = out[i].withoutNotes()
	}
	return out
}

// noted reports whether a person dismissed or answered the finding.
func (f Finding) noted() bool { return !f.open() || f.Resolution != nil }

// withoutNotes is the finding without people's notes: no dismissal, no
// answer.
func (f Finding) withoutNotes() Finding {
	f.DismissalReason, f.Resolution = "", nil
	return f
}
