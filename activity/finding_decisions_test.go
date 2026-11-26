package activity

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pageCheck is a submitted docs page check on page-1 whose flow continues
// into a fix (auto, cloud) on changes requested; d1 asks page-1 vs page-2.
func pageCheck() Run {
	r := decideFindingsRun()
	r.ID, r.RootID, r.Location = "check-1", "check-1", Cloud
	r.Subject = Subject{Kind: SubjectDocPage, ID: "page-1", ScopeType: "organization", ScopeID: "org-1"}
	r.Flow = &FlowRef{VersionID: "v", Node: "reconcile", Hops: map[Outcome]FlowHop{
		OutcomeChangesRequested: {Node: "fix", Runner: Answer, Auto: true, Location: Cloud},
	}}
	return r
}

// A run cannot hand off while a person still has to answer a question: an
// unanswered decide finding refuses Next, ApplyFixes, the flow advancer,
// accepting and approving. Answering (or dismissing) the question opens
// hand-off, and after hand-off the answers lock.
func TestOpenDecisionsBlockHandOff(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	reviewer := Actor{UserID: "reviewer", Role: "member"}
	flowActor := Actor{UserID: "owner", Purpose: PurposeFlow}

	r := pageCheck()
	require.Equal(t, 1, r.OpenDecisions())
	for name, try := range map[string]func(Run) error{
		"next": func(r Run) error {
			_, err := r.Successor(owner, Command{Type: Next, ExpectedVersion: r.Version})
			return err
		},
		"apply fixes": func(r Run) error {
			_, err := r.Successor(owner, Command{Type: ApplyFixes, ExpectedVersion: r.Version})
			return err
		},
		"flow hop": func(r Run) error {
			_, err := r.Successor(flowActor, Command{Type: Next, ExpectedVersion: r.Version})
			return err
		},
		"accept": func(r Run) error {
			return r.Apply(reviewer, Command{Type: Decide, ExpectedVersion: r.Version, Decision: Accept, EvidenceDigest: r.EvidenceDigest()})
		},
	} {
		assert.ErrorIs(t, try(r), ErrConflict, name)
	}
	_, auto := r.AutoHop()
	assert.False(t, auto, "the flow waits for the answers")
	assert.NotContains(t, r.AllowedCommands(owner, HostFacts{}), Next)
	assert.NotContains(t, r.AllowedCommands(owner, HostFacts{}), ApplyFixes)

	// Any decision locks the notes, so every decision waits for the
	// answers too: none can strand a question (requesting changes, then
	// accepting, used to leave the run stuck).
	for _, kind := range []DecisionKind{RequestChanges, Reject} {
		decided := pageCheck()
		assert.ErrorIs(t, decided.Apply(reviewer, Command{Type: Decide, ExpectedVersion: decided.Version, Decision: kind, Reason: "Redo", EvidenceDigest: decided.EvidenceDigest()}), ErrConflict, kind)
	}
	assert.NotContains(t, r.AllowedCommands(reviewer, HostFacts{}), Decide)
	changes := pageCheck()
	require.NoError(t, changes.Apply(owner, Command{Type: ResolveFinding, ExpectedVersion: changes.Version, FindingID: "d1", Choice: ResolveTarget}))
	require.NoError(t, changes.Apply(reviewer, Command{Type: Decide, ExpectedVersion: changes.Version, Decision: RequestChanges, Reason: "Redo", EvidenceDigest: changes.EvidenceDigest()}))
	assert.NoError(t, changes.Apply(reviewer, Command{Type: Decide, ExpectedVersion: changes.Version, Decision: Accept, EvidenceDigest: changes.EvidenceDigest()}),
		"answered first: a later vote still counts")

	answered := pageCheck()
	require.NoError(t, answered.Apply(owner, Command{Type: ResolveFinding, ExpectedVersion: answered.Version, FindingID: "d1", Choice: ResolveTarget}))
	_, err := answered.Successor(owner, Command{Type: Next, ExpectedVersion: answered.Version})
	assert.NoError(t, err, "answered: hand-off opens")
	assert.Contains(t, answered.AllowedCommands(owner, HostFacts{}), Next)
	_, auto = answered.AutoHop()
	assert.False(t, auto, "answered: a person starts the hop (TestDecideFindingsWaitForAPersonToStartTheHop)")

	dismissed := pageCheck()
	require.NoError(t, dismissed.Apply(owner, Command{Type: DismissFinding, ExpectedVersion: dismissed.Version, FindingID: "d1", Reason: "Not a conflict"}))
	_, err = dismissed.Successor(owner, Command{Type: ApplyFixes, ExpectedVersion: dismissed.Version})
	assert.NoError(t, err, "dismissed: hand-off opens")

	// The host completes the check once its fix started: the answer is final.
	answered.State = Completed
	assert.ErrorIs(t, answered.Apply(owner, Command{Type: ResolveFinding, ExpectedVersion: answered.Version, FindingID: "d1", Choice: ResolveSubject}), ErrConflict)
}

// A check that asked a person anything never hands off on its own: the
// last answer (or dismissal) opens Next for a person, not the auto edge,
// and the flow advancer is refused. A check without decide findings
// follows the same auto edge. A decision at an approval node stays an
// explicit start (TestOpenDecisionsBlockApproval).
func TestDecideFindingsWaitForAPersonToStartTheHop(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	flowActor := Actor{UserID: "owner", Purpose: PurposeFlow}

	plain := pageCheck()
	plain.Findings = plain.Findings[:2]
	_, auto := plain.AutoHop()
	require.True(t, auto, "no question: the auto edge holds")

	for name, note := range map[string]Command{
		"answered":  {Type: ResolveFinding, FindingID: "d1", Choice: ResolveTarget},
		"dismissed": {Type: DismissFinding, FindingID: "d1", Reason: "Not a conflict"},
	} {
		r := pageCheck()
		note.ExpectedVersion = r.Version
		require.NoError(t, r.Apply(owner, note), name)
		require.Zero(t, r.OpenDecisions(), name)
		_, auto := r.AutoHop()
		assert.False(t, auto, name)
		_, err := r.Successor(flowActor, Command{Type: Next, ExpectedVersion: r.Version})
		assert.ErrorIs(t, err, ErrConflict, "%s: the flow advancer waits", name)
		_, err = r.Successor(owner, Command{Type: Next, ExpectedVersion: r.Version})
		assert.NoError(t, err, "%s: a person starts the hop", name)
	}
}

// An approval node after a check with an open question waits for the
// answer too: approving would hand off.
func TestOpenDecisionsBlockApproval(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	r := decideFindingsRun()
	r.Location = Cloud
	r.Flow = &FlowRef{VersionID: "v", Node: "reconcile", Hops: map[Outcome]FlowHop{OutcomeChangesRequested: {
		Stop: "signoff", Approval: true, Approver: ApproverOwner,
		Decide: map[Outcome]FlowHop{
			OutcomeApproved:         {Node: "fix", Runner: Answer, Location: Cloud},
			OutcomeChangesRequested: {Node: "reconcile", Runner: Findings, Location: Cloud},
		}}}}
	assert.ErrorIs(t, r.Apply(owner, Command{Type: ApproveStep, ExpectedVersion: r.Version}), ErrConflict)
	assert.NotContains(t, r.AllowedCommands(owner, HostFacts{}), ApproveStep)
	require.NoError(t, r.Apply(owner, Command{Type: ResolveFinding, ExpectedVersion: r.Version, FindingID: "d1", Choice: ResolveOther, Reason: "Two days after"}))
	assert.NoError(t, r.Apply(owner, Command{Type: ApproveStep, ExpectedVersion: r.Version}))
}

func TestDecideFindingValidation(t *testing.T) {
	page := &FindingTarget{Type: FindingTargetDocPage, ID: "page-2"}
	decide := func(edit func(*Finding)) Finding {
		f := Finding{ID: "d", Severity: High, Description: "d", Target: page, SubjectQuote: "a", TargetQuote: "b",
			Suggestion: &FindingSuggestion{Action: SuggestDecide}}
		if edit != nil {
			edit(&f)
		}
		return f
	}
	for name, tc := range map[string]struct {
		f     Finding
		valid bool
	}{
		"page target":      {decide(nil), true},
		"rule target":      {decide(func(f *Finding) { f.Target = &FindingTarget{Type: FindingTargetContextEntry, ID: "rule-1"} }), true},
		"brief target":     {decide(func(f *Finding) { f.Target = &FindingTarget{Type: FindingTargetProjectBrief, ID: "p"} }), false},
		"no target":        {decide(func(f *Finding) { f.Target, f.TargetQuote = nil, "" }), false},
		"no subject quote": {decide(func(f *Finding) { f.SubjectQuote = "" }), false},
		"no target quote":  {decide(func(f *Finding) { f.TargetQuote = "" }), false},
	} {
		assert.Equal(t, tc.valid, tc.f.validateReference() == nil, name)
	}
}

// A markdown patch is the replacement for the passage a page edit changes:
// only update_target, update_subject and merge_into_target keep one; any
// other action's patch is dropped on submission (like notes), even an
// oversized one, and the submitted finding is not changed in place.
func TestSubmitFindingsDropsPatchesOffPageEdits(t *testing.T) {
	executor := Actor{UserID: "owner", Role: "member", Purpose: PurposeExecutor}
	for _, action := range allActions {
		for _, patch := range []string{"replacement", strings.Repeat("x", MaxFindingPatchBytes+1)} {
			in := Finding{ID: "f", Severity: Low, Description: "d", SubjectQuote: "a", TargetQuote: "b",
				Target:     &FindingTarget{Type: FindingTargetContextEntry, ID: "rule-1"},
				Suggestion: &FindingSuggestion{Action: action, MarkdownPatch: patch, Rationale: "why"}}
			r := Run{Runner: Findings, State: Running, OwnerID: "owner", Version: 2, InputDigest: "in"}
			err := r.Apply(executor, Command{Type: SubmitFindings, ExpectedVersion: 2, Summary: "s", Recommendation: ChangesRequested, Findings: []Finding{in}})
			assert.Equal(t, patch, in.Suggestion.MarkdownPatch, "%s: the command's finding is not changed", action)
			edits := slices.Contains(patchActions, action)
			if edits && len(patch) > MaxFindingPatchBytes {
				assert.ErrorIs(t, err, ErrValidation, "%s: a kept patch is bounded", action)
				continue
			}
			require.NoError(t, err, action)
			want := ""
			if edits {
				want = patch
			}
			assert.Equal(t, want, r.Findings[0].Suggestion.MarkdownPatch, action)
			assert.Equal(t, "why", r.Findings[0].Suggestion.Rationale, action)
		}
	}
}

// A decision between a docs page and itself means nothing: a check refuses
// a decide finding whose page target is its own page (other target actions
// on it are follow-ups for the page's fix, fixPages).
func TestSubmitFindingsRefusesDecidingAgainstItself(t *testing.T) {
	executor := Actor{UserID: "owner", Role: "member", Purpose: PurposeExecutor}
	submit := func(target string, action SuggestionAction) error {
		r := Run{Runner: Findings, State: Running, OwnerID: "owner", Version: 2, InputDigest: "in",
			Subject: Subject{Kind: SubjectDocPage, ID: "page-1", ScopeType: "organization", ScopeID: "org-1"}}
		return r.Apply(executor, Command{Type: SubmitFindings, ExpectedVersion: 2, Summary: "s", Recommendation: ChangesRequested, Findings: []Finding{{
			ID: "d", Severity: High, Description: "d", SubjectQuote: "a", TargetQuote: "b",
			Target: &FindingTarget{Type: FindingTargetDocPage, ID: target}, Suggestion: &FindingSuggestion{Action: action},
		}}})
	}
	assert.ErrorIs(t, submit("page-1", SuggestDecide), ErrValidation)
	assert.NoError(t, submit("page-2", SuggestDecide))
	assert.NoError(t, submit("page-1", SuggestUpdateTarget))
}

func TestResolutionChoices(t *testing.T) {
	assert.Equal(t, []ResolutionChoice{ResolveSubject, ResolveTarget, ResolveOther}, resolutionChoices)
	for _, c := range resolutionChoices {
		assert.True(t, c.Valid(), c)
	}
	assert.False(t, ResolutionChoice("").Valid())
	assert.False(t, ResolutionChoice("both").Valid())
}

// Which fix applies which kept finding of a docs page check on page-1:
// exactly one page's fix, except an "other" answer on a page target (both
// pages state the wrong fact).
func TestFindingsForSplitsAcrossPages(t *testing.T) {
	answer := func(c ResolutionChoice) *FindingResolution { return &FindingResolution{Choice: c, Text: "fact"} }
	page2 := &FindingTarget{Type: FindingTargetDocPage, ID: "page-2"}
	rule := &FindingTarget{Type: FindingTargetContextEntry, ID: "rule-1"}
	self := &FindingTarget{Type: FindingTargetDocPage, ID: "page-1"}
	for name, tc := range map[string]struct {
		f      Finding
		pages  []string // fixPages: whose fix applies it once kept
		kept   bool
		linked []string // LinkedFixPages
	}{
		"update subject":           {Finding{Suggestion: &FindingSuggestion{Action: SuggestUpdateSubject}, Target: page2}, []string{"page-1"}, true, nil},
		"update target":            {Finding{Suggestion: &FindingSuggestion{Action: SuggestUpdateTarget}, Target: page2}, []string{"page-2"}, true, []string{"page-2"}},
		"merge into target":        {Finding{Suggestion: &FindingSuggestion{Action: SuggestMergeIntoTarget}, Target: page2}, []string{"page-2"}, true, []string{"page-2"}},
		"update target, same page": {Finding{Suggestion: &FindingSuggestion{Action: SuggestUpdateTarget}, Target: self}, []string{"page-1"}, true, nil},
		"update target, rule":      {Finding{Suggestion: &FindingSuggestion{Action: SuggestUpdateTarget}, Target: rule}, []string{"page-1"}, true, nil},
		"answer: subject right":    {Finding{Suggestion: &FindingSuggestion{Action: SuggestDecide}, Target: page2, Resolution: answer(ResolveSubject)}, []string{"page-2"}, true, []string{"page-2"}},
		"answer: target right":     {Finding{Suggestion: &FindingSuggestion{Action: SuggestDecide}, Target: page2, Resolution: answer(ResolveTarget)}, []string{"page-1"}, true, nil},
		"answer: neither":          {Finding{Suggestion: &FindingSuggestion{Action: SuggestDecide}, Target: page2, Resolution: answer(ResolveOther)}, []string{"page-1", "page-2"}, true, []string{"page-2"}},
		"answer on a rule":         {Finding{Suggestion: &FindingSuggestion{Action: SuggestDecide}, Target: rule, Resolution: answer(ResolveSubject)}, []string{"page-1"}, true, nil},
		"unanswered":               {Finding{Suggestion: &FindingSuggestion{Action: SuggestDecide}, Target: page2}, []string{"page-1"}, false, nil},
		"dismissed":                {Finding{Suggestion: &FindingSuggestion{Action: SuggestUpdateTarget}, Target: page2, DismissalReason: "done"}, []string{"page-2"}, false, nil},
		"follow-up":                {Finding{Suggestion: &FindingSuggestion{Action: SuggestLinkRelated}, Target: page2}, []string{"page-1"}, true, nil},
		"no suggestion":            {Finding{}, []string{"page-1"}, true, nil},
	} {
		tc.f.ID, tc.f.Severity, tc.f.Description = "f", High, "d"
		assert.Equal(t, tc.pages, tc.f.fixPages("page-1", true), name)
		assert.Equal(t, []string{"page-1"}, tc.f.fixPages("page-1", false), "%s: no split, the subject's fix", name)
		r := pageCheck()
		r.Findings = []Finding{tc.f}
		assert.Equal(t, tc.kept, len(r.keptFindings()) == 1, name)
		for _, page := range []string{"page-1", "page-2", "rule-1"} {
			want := tc.kept && slices.Contains(tc.pages, page)
			assert.Equal(t, want, len(r.FindingsFor(page)) == 1, "%s: %s", name, page)
		}
		assert.Equal(t, tc.linked, r.LinkedFixPages(), name)
	}
}

func TestLinkedFixPagesOncePerPageInOrder(t *testing.T) {
	r := pageCheck()
	target := func(id string) *FindingTarget { return &FindingTarget{Type: FindingTargetDocPage, ID: id} }
	r.Findings = []Finding{
		{ID: "a", Severity: High, Description: "d", Target: target("page-3"), Suggestion: &FindingSuggestion{Action: SuggestUpdateTarget}},
		{ID: "b", Severity: High, Description: "d", Target: target("page-2"), Suggestion: &FindingSuggestion{Action: SuggestMergeIntoTarget}},
		{ID: "c", Severity: High, Description: "d", Target: target("page-3"), Suggestion: &FindingSuggestion{Action: SuggestUpdateTarget}},
	}
	assert.Equal(t, []string{"page-3", "page-2"}, r.LinkedFixPages())
	assert.Len(t, r.FindingsFor("page-3"), 2)
	assert.Empty(t, r.FindingsFor("page-1"))
	assert.Empty(t, r.FindingsFor(""))
}

// Off docs pages there are no linked fixes: a work item review's repair
// receives every kept finding.
func TestFindingsForWorkItemRuns(t *testing.T) {
	r := submittedFindingsRun()
	r.Subject = Subject{Kind: SubjectWorkItem, ID: "item-1", ScopeType: "project", ScopeID: "p"}
	r.Findings = append(r.Findings, Finding{ID: "t", Severity: High, Description: "d",
		Target: &FindingTarget{Type: FindingTargetDocPage, ID: "page-2"}, Suggestion: &FindingSuggestion{Action: SuggestUpdateTarget}})
	assert.Len(t, r.FindingsFor("item-1"), 3)
	assert.Empty(t, r.FindingsFor("page-2"))
	assert.Nil(t, r.LinkedFixPages())
}

func TestRunWaitsForAPerson(t *testing.T) {
	for _, tc := range []struct {
		state    State
		blocker  Blocker
		start    bool
		followUp bool
	}{
		{Suggested, "", true, false},
		{Waiting, BlockerExecutionBusy, true, false},
		{Waiting, BlockerAwaitingResume, false, false},
		{Ready, "", false, false},
		{Running, "", false, false},
		{Submitted, "", false, true},
		{Completed, "", false, true},
		{Failed, BlockerExecutionFailed, false, true},
		{Cancelled, "", false, true},
		{"unknown", "", false, false},
	} {
		r := Run{State: tc.state, Blocker: tc.blocker}
		assert.Equal(t, tc.start, r.AwaitsStart(), "%s %s", tc.state, tc.blocker)
		assert.Equal(t, tc.followUp, r.AcceptsFollowUp(), "%s %s", tc.state, tc.blocker)
	}
}

func TestActorHuman(t *testing.T) {
	assert.True(t, Actor{UserID: "u", Role: "member"}.Human())
	assert.True(t, Actor{UserID: "u", Role: "owner"}.Human())
	assert.False(t, Actor{UserID: "u", Role: "viewer"}.Human(), "viewers act on nothing")
	assert.False(t, Actor{Role: "member"}.Human())
	for _, p := range []Purpose{PurposeFlow, PurposeExecutor, PurposeCoordinator} {
		assert.False(t, Actor{UserID: "u", Role: "member", Purpose: p}.Human(), p)
	}
}

// A follow-up question only reads, as the person: never more than the
// person's own chat in that scope, never a write, submit, command, skill,
// MCP or web tool.
func TestCapabilitiesForFollowUp(t *testing.T) {
	// Writes, and the Docs map: a follow-up answer lands in the run's shared
	// session, so it never reads digests of pages other readers cannot see.
	writes := []string{"docs_map", "find_doc_claims", "update_doc_page", "create_doc_page", "submit_activity_outputs", "submit_review_findings", "create_comment",
		"update_work_item", "write_file", "batch_commit", "request_approval_action", "read_skill_file", "web_search", "web_fetch", "link_work_items"}
	for _, subject := range []SubjectKind{SubjectDocPage, SubjectWorkItem, SubjectSession} {
		for _, scope := range []string{"organization", "workspace", "team", "project"} {
			caps := CapabilitiesForFollowUp(subject, scope)
			assert.NotEmpty(t, caps, "%s %s", subject, scope)
			person := CapabilitiesFor("", scope)
			for name := range caps {
				assert.True(t, person.Allows(name), "%s %s: %s", subject, scope, name)
				assert.False(t, strings.HasPrefix(name, "mcp"), name)
			}
			for _, name := range writes {
				assert.False(t, caps.Allows(name), "%s %s: %s", subject, scope, name)
			}
		}
	}
	docs := CapabilitiesForFollowUp(SubjectDocPage, "organization")
	assert.Equal(t, CapabilitySet{"get_doc_page": true}, docs, "a docs follow-up reads the run's pages only")
	assert.False(t, docs.Allows("get_work_item"), "a docs follow-up reads docs")
	assert.True(t, CapabilitiesForFollowUp(SubjectWorkItem, "project").Allows("get_work_item"))
	assert.Empty(t, CapabilitiesForFollowUp(SubjectDocPage, "planet"), "unknown scopes fail closed")
}
