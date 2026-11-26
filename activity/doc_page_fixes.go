package activity

import "slices"

// Docs page fixes: which kept findings of a run the fix on each page
// applies.

// keptFindings are the findings a fix after this run receives: those no
// person dismissed, decide findings only once answered (an unanswered
// question stays open on the run and is never applied). Answered ones keep
// their Resolution. Which fix applies which kept finding: fixPages.
func (r Run) keptFindings() []Finding {
	out := []Finding{}
	for _, f := range r.Findings {
		if f.open() && !f.awaitsAnswer() {
			out = append(out, f)
		}
	}
	return out
}

// fixPages lists the pages whose fix applies a kept finding of a run on
// subject; split is whether the run splits its findings across pages (a
// docs page check, Run.splitsFixes). Without a split, the subject's fix
// applies every finding. A docs page check runs on its subject page S; a
// finding may compare S with another page T (Target, doc_page). A fix runs
// on one page, so the findings are split:
//
//   - update_subject: S.
//   - update_target, merge_into_target with a page target T ≠ S: T.
//   - decide, answered: the side the answer says is wrong. subject (S is
//     right) → T; target (T is right) → S; other (neither) → both S and T,
//     the one finding two fixes apply, since both pages state the wrong
//     fact. A decision on an AI rule (context_entry) never changes another
//     page: S.
//   - everything else (no suggestion, none, link_related, mark_superseded,
//     rule actions, target actions on a rule, the brief or S itself): no
//     other page changes; S's fix receives it to report as a follow-up.
func (f Finding) fixPages(subject string, split bool) []string {
	t, s := f.Target, f.Suggestion
	if !split || t == nil || t.Type != FindingTargetDocPage || t.ID == "" || t.ID == subject || s == nil {
		return []string{subject}
	}
	switch s.Action {
	case SuggestUpdateTarget, SuggestMergeIntoTarget:
		return []string{t.ID}
	case SuggestDecide:
		switch f.Resolution.choice() {
		case ResolveSubject:
			return []string{t.ID}
		case ResolveOther:
			return []string{subject, t.ID}
		}
	}
	return []string{subject}
}

// choice is the answer's choice, "" when unanswered.
func (r *FindingResolution) choice() ResolutionChoice {
	if r == nil {
		return ""
	}
	return r.Choice
}

// FindingsFor lists, for a docs page check only, the kept findings
// (keptFindings) the fix on page applies, in finding order: page is the
// run's own page (its successor fix) or another page a finding asks to
// change (a linked fix, LinkedFixPages); the split is fixPages. Any other
// run has no linked fixes: its subject gets every kept finding and other
// ids none.
func (r Run) FindingsFor(page string) []Finding {
	out := []Finding{}
	if page == "" {
		return out
	}
	subject, split := r.RunSubject().ID, r.splitsFixes()
	for _, f := range r.keptFindings() {
		if slices.Contains(f.fixPages(subject, split), page) {
			out = append(out, f)
		}
	}
	return out
}

// splitsFixes reports whether the run's kept findings are split across
// pages (a docs page findings run); any other run's fix gets them all.
func (r Run) splitsFixes() bool { return r.IsDocPage() && r.Runner == Findings }

// LinkedFixPages lists, for a docs page check only, the other pages its
// kept findings ask to change, in finding order and once each: each gets
// its own fix run (Request.LinkedFromID = this run) applying
// FindingsFor(page). None for any other run.
func (r Run) LinkedFixPages() []string {
	if !r.splitsFixes() {
		return nil
	}
	subject := r.RunSubject().ID
	var pages []string
	for _, f := range r.keptFindings() {
		for _, p := range f.fixPages(subject, true) {
			if p != subject && !slices.Contains(pages, p) {
				pages = append(pages, p)
			}
		}
	}
	return pages
}
