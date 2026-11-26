package activity

// Docs page runs: the rules NewRun applies to a run on a docs page (its
// chain and its link to a run on another page).

// validPageChain reports whether a new run on subject (with NewRun's
// project and work item) keeps docs page chains on their page. A docs page
// run has no project or work item; its scope is the page's area. Its
// successors never move to another page, and work item runs never spawn
// page runs (or the reverse).
func validPageChain(project, item string, subject Subject, parent *Run) bool {
	if subject.Kind != SubjectDocPage {
		return parent == nil || !parent.IsDocPage()
	}
	return project == "" && item == "" && (parent == nil || parent.RunSubject() == subject)
}

// validLink reports whether the new run id may carry q.LinkedFromID (a run
// on another page whose findings it applies). Only docs page runs link,
// never to themselves, with an id ≤ MaxRunIDBytes; a successor may only
// keep its parent's link (Retry, Refine: SuccessorRun.LinkedFromID), never
// gain another. No link is always valid.
func validLink(id string, q Request, parent *Run) bool {
	l := q.LinkedFromID
	if l == "" {
		return true
	}
	return q.Subject.Kind == SubjectDocPage && len(l) <= MaxRunIDBytes && l != id && (parent == nil || l == parent.LinkedFromID)
}
