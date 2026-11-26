package activity

import (
	"slices"
	"time"
	"unicode/utf8"
)

// FindingTargetType is what a finding compares its subject against.
type FindingTargetType string

const (
	FindingTargetDocPage      FindingTargetType = "doc_page"
	FindingTargetContextEntry FindingTargetType = "context_entry"
	FindingTargetProjectBrief FindingTargetType = "project_brief"
)

// FindingTarget names the other side of a finding (a page that contradicts
// the subject, an AI rule, the project brief) at the version the run read.
type FindingTarget struct {
	Type    FindingTargetType `json:"type"`
	ID      string            `json:"id"`
	Version string            `json:"version,omitempty"`
}

// SuggestionAction is what a finding proposes a person do. A suggestion is
// never applied by the run: people apply it with their own credentials.
type SuggestionAction string

const (
	SuggestLinkRelated     SuggestionAction = "link_related"
	SuggestMarkSuperseded  SuggestionAction = "mark_superseded"
	SuggestUpdateTarget    SuggestionAction = "update_target"
	SuggestUpdateSubject   SuggestionAction = "update_subject"
	SuggestMergeIntoTarget SuggestionAction = "merge_into_target"
	SuggestUpdateRule      SuggestionAction = "update_rule"
	SuggestRetireRule      SuggestionAction = "retire_rule"
	// SuggestDecide asks a person which side of a contradiction is right
	// when the sources do not settle it (no superseding page, both recent).
	// The person answers with ResolveFinding; the answer decides which page
	// a fix step edits.
	SuggestDecide SuggestionAction = "decide"
	SuggestNone   SuggestionAction = "none"
)

// ResolutionChoice is a person's answer to a decide finding.
type ResolutionChoice string

const (
	// ResolveSubject: the subject (the page the run is on) is right; the
	// target is the one to change.
	ResolveSubject ResolutionChoice = "subject"
	// ResolveTarget: the target is right; the subject is the one to change.
	ResolveTarget ResolutionChoice = "target"
	// ResolveOther: neither side; Text states the correct fact and both may
	// need a change.
	ResolveOther ResolutionChoice = "other"
)

// resolutionChoices are the valid answers in display order.
var resolutionChoices = []ResolutionChoice{ResolveSubject, ResolveTarget, ResolveOther}

// Valid reports whether c is a known answer.
func (c ResolutionChoice) Valid() bool { return slices.Contains(resolutionChoices, c) }

// FindingResolution records a person's answer to a decide finding. Like a
// dismissal it is a note on the evidence, never evidence: it does not change
// the evidence digest.
type FindingResolution struct {
	Choice ResolutionChoice `json:"choice"`
	// Text is the correct fact for ResolveOther, an optional note otherwise
	// (≤ MaxFindingNoteRunes).
	Text string    `json:"text,omitempty"`
	By   string    `json:"by"`
	At   time.Time `json:"at"`
}

type Finding struct {
	ID              string   `json:"id"`
	Severity        Severity `json:"severity"`
	Location        string   `json:"location,omitempty"`
	Description     string   `json:"description"`
	DismissalReason string   `json:"dismissal_reason,omitempty"`
	// Resolution is a person's answer to a decide finding (ResolveFinding).
	Resolution *FindingResolution `json:"resolution,omitempty"`
	// Target names what the finding compares the subject against (another
	// page, an AI rule, the project brief); the quotes cite both sides and
	// Suggestion proposes what a person may do about it (finding.go).
	Target       *FindingTarget     `json:"target,omitempty"`
	SubjectQuote string             `json:"subject_quote,omitempty"`
	TargetQuote  string             `json:"target_quote,omitempty"`
	Suggestion   *FindingSuggestion `json:"suggestion,omitempty"`
	// Expected and Observed state what should happen and what the reviewer
	// saw instead (≤ MaxLoopTextRunes each); later loop rounds read them
	// from the loop ledger.
	Expected string `json:"expected,omitempty"`
	Observed string `json:"observed,omitempty"`
}

// FindingSuggestion proposes a fix: an action, an optional markdown patch for
// the page it changes and why.
type FindingSuggestion struct {
	Action        SuggestionAction `json:"action"`
	MarkdownPatch string           `json:"markdown_patch,omitempty"`
	Rationale     string           `json:"rationale,omitempty"`
}

// Finding reference limits.
const (
	MaxFindingQuoteRunes         = 400
	MaxFindingPatchBytes         = 8 * 1024
	MaxFindingRationaleRunes     = 600
	MaxFindingTargetIDBytes      = 128
	MaxFindingTargetVersionBytes = 128
)

// What each suggestion action needs, as data: validateReference and
// Finding.submitted read these tables and nothing else.
var (
	findingTargetTypes = []FindingTargetType{FindingTargetDocPage, FindingTargetContextEntry, FindingTargetProjectBrief}
	// targetActions act on the finding's target, so they need one.
	targetActions = []SuggestionAction{SuggestLinkRelated, SuggestMarkSuperseded, SuggestUpdateTarget, SuggestMergeIntoTarget, SuggestUpdateRule, SuggestRetireRule, SuggestDecide}
	// actionTargets limits the target types of actions that only make sense
	// on some: rule actions change an AI rule (a context entry); a decision
	// weighs this page against another page or an AI rule.
	actionTargets = map[SuggestionAction][]FindingTargetType{
		SuggestUpdateRule: {FindingTargetContextEntry},
		SuggestRetireRule: {FindingTargetContextEntry},
		SuggestDecide:     {FindingTargetDocPage, FindingTargetContextEntry},
	}
	// patchActions may carry a markdown patch: the replacement text for the
	// quoted passage of the page they change. Any other action's patch is
	// dropped on submission (Finding.submitted).
	patchActions = []SuggestionAction{SuggestUpdateTarget, SuggestUpdateSubject, SuggestMergeIntoTarget}
	// quotedActions cite both sides: a person deciding between them reads
	// the subject's and the target's text.
	quotedActions = []SuggestionAction{SuggestDecide}
	allActions    = append(slices.Clone(targetActions), SuggestUpdateSubject, SuggestNone)
)

// submitted is the finding as a run records it from SubmitFindings: no
// person's notes (only people dismiss or answer, notes are cleared, not
// refused, so older runners keep working) and no markdown patch on an
// action that edits no page (patchActions; dropped, not refused, like the
// notes). The suggestion is copied, never changed in place.
func (f Finding) submitted() Finding {
	f = f.withoutNotes()
	if s := f.Suggestion; s != nil && s.MarkdownPatch != "" && !slices.Contains(patchActions, s.Action) {
		c := *s
		c.MarkdownPatch = ""
		f.Suggestion = &c
	}
	return f
}

// validateReference checks the optional target, quotes and suggestion of a
// submitted finding (after submitted). Findings without them stay valid;
// it refuses only shapes whose meaning breaks: an unknown action or
// target, oversized or invalid text, a target action without its target
// (a decision without both quotes).
func (f Finding) validateReference() error {
	if f.Target != nil && (!slices.Contains(findingTargetTypes, f.Target.Type) || f.Target.ID == "" ||
		len(f.Target.ID) > MaxFindingTargetIDBytes || len(f.Target.Version) > MaxFindingTargetVersionBytes) {
		return ErrValidation
	}
	if f.TargetQuote != "" && f.Target == nil {
		return ErrValidation
	}
	for _, q := range []string{f.SubjectQuote, f.TargetQuote} {
		if !utf8.ValidString(q) || utf8.RuneCountInString(q) > MaxFindingQuoteRunes {
			return ErrValidation
		}
	}
	for _, v := range []string{f.Expected, f.Observed} {
		if !utf8.ValidString(v) || utf8.RuneCountInString(v) > MaxLoopTextRunes {
			return ErrValidation
		}
	}
	s := f.Suggestion
	if s == nil {
		return nil
	}
	types, limited := actionTargets[s.Action]
	switch {
	case !slices.Contains(allActions, s.Action),
		len(s.MarkdownPatch) > MaxFindingPatchBytes || !utf8.ValidString(s.MarkdownPatch),
		utf8.RuneCountInString(s.Rationale) > MaxFindingRationaleRunes || !utf8.ValidString(s.Rationale),
		slices.Contains(targetActions, s.Action) && f.Target == nil,
		limited && !slices.Contains(types, f.Target.Type),
		slices.Contains(quotedActions, s.Action) && (f.SubjectQuote == "" || f.TargetQuote == ""):
		return ErrValidation
	}
	return nil
}

// decidesItself reports whether f asks a person to decide between a docs
// page run's own page and itself (a decide finding whose doc_page target is
// the run's subject): nothing contradicts, so SubmitFindings refuses it.
func (r Run) decidesItself(f Finding) bool {
	t := f.Target
	return r.IsDocPage() && f.asksDecision() && t != nil && t.Type == FindingTargetDocPage && t.ID == r.RunSubject().ID
}

// open reports whether no person has dismissed the finding.
func (f Finding) open() bool { return f.DismissalReason == "" }

// asksDecision reports whether the finding was submitted as a question for
// a person (action decide), whatever people noted on it since. It reads
// only evidence (the finding as submitted).
func (f Finding) asksDecision() bool {
	return f.Suggestion != nil && f.Suggestion.Action == SuggestDecide
}

// isDecision reports whether the finding is an open question for a person:
// an undismissed decide finding, answered or not (an answer may be changed
// while the run's notes are open, Run.notesOpen). The one predicate for
// "is this a decision"; awaitsAnswer and resolveFinding build on it.
func (f Finding) isDecision() bool { return f.open() && f.asksDecision() }

// awaitsAnswer reports whether the finding asks a person to decide and
// nobody has answered or dismissed it yet.
func (f Finding) awaitsAnswer() bool { return f.isDecision() && f.Resolution == nil }

// OpenDecisions counts the decide findings still waiting for a person.
// While any waits the run may not hand off or be decided on (locksNotes):
// no Next, ApplyFixes, automatic hop (AutoHop), Decide or step decision.
func (r Run) OpenDecisions() int {
	n := 0
	for _, f := range r.Findings {
		if f.awaitsAnswer() {
			n++
		}
	}
	return n
}

// asksDecisions reports whether the run's findings asked a person to
// decide anything (asksDecision; answered, dismissed or not). Such a run
// never hands off on its own: a person starts the hop (AutoHop).
func (r Run) asksDecisions() bool { return slices.ContainsFunc(r.Findings, Finding.asksDecision) }
