package activity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decideFindingsRun() Run {
	r := submittedFindingsRun()
	r.Findings = append(r.Findings, Finding{
		ID: "d1", Severity: High, Description: "Cutover day differs",
		Target:       &FindingTarget{Type: FindingTargetDocPage, ID: "page-2", Version: "4"},
		SubjectQuote: "the day after its billing date", TargetQuote: "on its billing date",
		Suggestion: &FindingSuggestion{Action: SuggestDecide, Rationale: "Both pages are recent; their owners differ."},
	})
	return r
}

func TestResolveFindingRecordsTheAnswer(t *testing.T) {
	r := decideFindingsRun()
	digest, fingerprint := r.EvidenceDigest(), Fingerprint(r.Findings)
	require.Equal(t, 1, r.OpenDecisions())
	owner := Actor{UserID: "owner", Role: "member"}
	require.NoError(t, r.Apply(owner, Command{Type: ResolveFinding, ExpectedVersion: 3, FindingID: "d1", Choice: ResolveTarget, Reason: "  Finance confirmed  "}))
	res := r.Findings[2].Resolution
	require.NotNil(t, res)
	assert.Equal(t, ResolveTarget, res.Choice)
	assert.Equal(t, "Finance confirmed", res.Text)
	assert.Equal(t, "owner", res.By)
	assert.False(t, res.At.IsZero())
	assert.Equal(t, 0, r.OpenDecisions())
	assert.Equal(t, Submitted, r.State, "an answer never moves the run")
	assert.Equal(t, digest, r.EvidenceDigest(), "answers are not evidence")
	assert.Equal(t, fingerprint, Fingerprint(r.Findings))

	// Answering again replaces the earlier answer.
	require.NoError(t, r.Apply(owner, Command{Type: ResolveFinding, ExpectedVersion: 4, FindingID: "d1", Choice: ResolveOther, Reason: "Two days after"}))
	assert.Equal(t, ResolveOther, r.Findings[2].Resolution.Choice)
	assert.Equal(t, "Two days after", r.Findings[2].Resolution.Text)
}

func TestResolveFindingPayload(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	for name, tc := range map[string]struct {
		cmd Command
		err error
	}{
		"no finding":       {Command{Choice: ResolveSubject}, ErrValidation},
		"unknown finding":  {Command{FindingID: "nope", Choice: ResolveSubject}, ErrValidation},
		"no choice":        {Command{FindingID: "d1"}, ErrValidation},
		"bad choice":       {Command{FindingID: "d1", Choice: "both"}, ErrValidation},
		"other needs text": {Command{FindingID: "d1", Choice: ResolveOther, Reason: "  "}, ErrValidation},
		"text too long":    {Command{FindingID: "d1", Choice: ResolveSubject, Reason: strings.Repeat("é", MaxFindingNoteRunes+1)}, ErrValidation},
		"not a decision":   {Command{FindingID: "f1", Choice: ResolveSubject}, ErrConflict},
	} {
		r := decideFindingsRun()
		tc.cmd.Type, tc.cmd.ExpectedVersion = ResolveFinding, 3
		assert.ErrorIs(t, r.Apply(owner, tc.cmd), tc.err, name)
		assert.EqualValues(t, 3, r.Version, name)
	}

	dismissed := decideFindingsRun()
	require.NoError(t, dismissed.Apply(owner, Command{Type: DismissFinding, ExpectedVersion: 3, FindingID: "d1", Reason: "Not a conflict"}))
	assert.ErrorIs(t, dismissed.Apply(owner, Command{Type: ResolveFinding, ExpectedVersion: 4, FindingID: "d1", Choice: ResolveSubject}), ErrConflict)
	assert.Equal(t, 0, dismissed.OpenDecisions(), "a dismissed decision is not open")

	// A finding is answered or set aside, never both: dismissing an
	// answered decision drops its answer.
	answered := decideFindingsRun()
	require.NoError(t, answered.Apply(owner, Command{Type: ResolveFinding, ExpectedVersion: 3, FindingID: "d1", Choice: ResolveSubject}))
	require.NoError(t, answered.Apply(owner, Command{Type: DismissFinding, ExpectedVersion: 4, FindingID: "d1", Reason: "Not a conflict"}))
	assert.Nil(t, answered.Findings[2].Resolution)
	assert.Equal(t, "Not a conflict", answered.Findings[2].DismissalReason)

	assert.True(t, ResolveFinding.Public())
	assert.False(t, ResolveFinding.spawns())
}

func TestResolveFindingRoles(t *testing.T) {
	cmd := Command{Type: ResolveFinding, ExpectedVersion: 3, FindingID: "d1", Choice: ResolveSubject}
	for _, tc := range []struct {
		name  string
		actor Actor
		err   error
	}{
		{"owner", Actor{UserID: "owner", Role: "member"}, nil},
		{"admin", Actor{UserID: "admin", Role: "admin"}, nil},
		{"other member", Actor{UserID: "member", Role: "member"}, ErrForbidden},
		{"machine", Actor{UserID: "owner", Role: "member", Purpose: PurposeExecutor}, ErrForbidden},
	} {
		r := decideFindingsRun()
		err := r.Apply(tc.actor, cmd)
		if tc.err == nil {
			assert.NoError(t, err, tc.name)
			assert.Contains(t, decideFindingsRun().AllowedCommands(tc.actor, HostFacts{}), ResolveFinding, tc.name)
		} else {
			assert.ErrorIs(t, err, tc.err, tc.name)
			assert.NotContains(t, decideFindingsRun().AllowedCommands(tc.actor, HostFacts{}), ResolveFinding, tc.name)
		}
	}
}

func TestSubmitFindingsClearsResolutionsAndAcceptsDecide(t *testing.T) {
	r := Run{Runner: Findings, State: Running, OwnerID: "owner", Version: 2, InputDigest: "in"}
	f := decideFindingsRun().Findings[2]
	f.Resolution = &FindingResolution{Choice: ResolveSubject, By: "runner"}
	require.NoError(t, r.Apply(Actor{UserID: "owner", Role: "member", Purpose: PurposeExecutor}, Command{
		Type: SubmitFindings, ExpectedVersion: 2, Summary: "One open question", Recommendation: ChangesRequested, Findings: []Finding{f},
	}))
	assert.Nil(t, r.Findings[0].Resolution, "only people answer")
	assert.Equal(t, 1, r.OpenDecisions())

	noTarget := f
	noTarget.Target, noTarget.TargetQuote = nil, ""
	r2 := Run{Runner: Findings, State: Running, OwnerID: "owner", Version: 2, InputDigest: "in"}
	assert.ErrorIs(t, r2.Apply(Actor{UserID: "owner", Role: "member", Purpose: PurposeExecutor}, Command{
		Type: SubmitFindings, ExpectedVersion: 2, Summary: "x", Recommendation: ChangesRequested, Findings: []Finding{noTarget},
	}), ErrValidation, "a decision needs the other side")
}

func TestLinkedFromIDOnlyOnDocPages(t *testing.T) {
	owner := Actor{OrgID: "org-1", UserID: "dev-1", Role: "member"}
	page := Subject{Kind: SubjectDocPage, ID: "page-2", ScopeType: "organization", ScopeID: "org-1"}
	q := Request{Runner: Answer, Location: Cloud, InputDigest: "in", Subject: page, CommandKey: "k", Origin: "docs", LinkedFromID: "run-1"}
	r, err := NewRun("run-2", "org-1", "", "", owner, q, nil)
	require.NoError(t, err)
	assert.Equal(t, "run-1", r.LinkedFromID)
	assert.Empty(t, r.PredecessorID, "a link is not a parent")

	for name, tc := range map[string]struct {
		id      string
		mutate  func(*Request)
		project string
		item    string
	}{
		"itself":   {id: "run-1"},
		"too long": {id: "run-4", mutate: func(q *Request) { q.LinkedFromID = strings.Repeat("x", MaxRunIDBytes+1) }},
		"work item runs": {id: "run-3", mutate: func(q *Request) {
			q.Subject = Subject{Kind: SubjectWorkItem, ID: "item", ScopeType: "project", ScopeID: "proj"}
		}, project: "proj", item: "item"},
	} {
		bad := q
		if tc.mutate != nil {
			tc.mutate(&bad)
		}
		_, err := NewRun(tc.id, "org-1", tc.project, tc.item, owner, bad, nil)
		assert.ErrorIs(t, err, ErrValidation, name)
	}
	q.LinkedFromID = strings.Repeat("x", MaxRunIDBytes)
	_, err = NewRun("run-5", "org-1", "", "", owner, q, nil)
	assert.NoError(t, err, "at the bound")
}

// Retry and Refine run the same fix again, so they keep the link (and its
// findings); Next and ApplyFixes follow the fix itself and drop it. A
// successor never gains a link its parent does not have.
func TestLinkedFromIDOnSuccessors(t *testing.T) {
	owner := Actor{OrgID: "org-1", UserID: "dev-1", Role: "member"}
	page := Subject{Kind: SubjectDocPage, ID: "page-2", ScopeType: "organization", ScopeID: "org-1"}
	fix := Run{ID: "fix-1", RootID: "fix-1", Runner: Answer, State: Failed, OwnerID: "dev-1", Version: 4, InputDigest: "in",
		Subject: page, Location: Cloud, LinkedFromID: "check-1", Outputs: []OutputRef{{Kind: RefDocs, ResourceID: "page-2"}}}

	retry, err := fix.Successor(owner, Command{Type: Retry, ExpectedVersion: 4})
	require.NoError(t, err)
	assert.Equal(t, "check-1", retry.LinkedFromID)

	fix.State = Submitted
	refine, err := fix.Successor(owner, Command{Type: Refine, ExpectedVersion: 4, Reason: "Shorter"})
	require.NoError(t, err)
	assert.Equal(t, "check-1", refine.LinkedFromID)

	q := Request{Runner: Answer, Location: Cloud, InputDigest: "in", Subject: page, CommandKey: "k", Origin: "docs", LinkedFromID: retry.LinkedFromID}
	again, err := NewRun("fix-2", "org-1", "", "", owner, q, &fix)
	require.NoError(t, err)
	assert.Equal(t, "check-1", again.LinkedFromID)
	assert.Equal(t, "fix-1", again.PredecessorID)

	q.LinkedFromID = "check-9"
	_, err = NewRun("fix-3", "org-1", "", "", owner, q, &fix)
	assert.ErrorIs(t, err, ErrValidation, "a successor keeps its parent's link or none")

	q.LinkedFromID = ""
	next, err := NewRun("fix-4", "org-1", "", "", owner, q, &fix)
	require.NoError(t, err, "Next/ApplyFixes successors drop the link")
	assert.Empty(t, next.LinkedFromID)
}

func resolvedRef(id, rev string) OutputRef {
	return OutputRef{Kind: RefDocs, ResourceID: id, Revision: rev, Hash: strings.Repeat("a", 64)}
}

func TestRepinSources(t *testing.T) {
	owner := Actor{OrgID: "org-1", UserID: "dev-1", Role: "member"}
	page := Subject{Kind: SubjectDocPage, ID: "page-1", ScopeType: "organization", ScopeID: "org-1"}
	subject, related := resolvedRef("page-1", "3"), resolvedRef("page-2", "4")
	suggested := func() Run {
		r, err := NewRun("run-1", "org-1", "", "", owner, Request{Runner: Findings, Location: Cloud, InputDigest: "old", Subject: page,
			Inputs: []OutputRef{subject}, CommandKey: "k", Origin: "docs"}, nil)
		require.NoError(t, err)
		return r
	}
	r := suggested()
	require.NoError(t, r.RepinSources("new", []OutputRef{subject, related}))
	assert.Equal(t, "new", r.InputDigest)
	assert.Equal(t, []OutputRef{subject, related}, r.Inputs)
	assert.Equal(t, []OutputRef{subject, related}, r.Outputs, "findings outputs follow the inputs")

	for name, tc := range map[string]struct {
		mutate func(*Run)
		digest string
		inputs []OutputRef
		err    error
	}{
		"empty digest":       {digest: "", inputs: []OutputRef{subject}, err: ErrValidation},
		"subject not pinned": {digest: "x", inputs: []OutputRef{related}, err: ErrValidation},
		"unresolved ref":     {digest: "x", inputs: []OutputRef{subject, {Kind: RefDocs, ResourceID: "page-2"}}, err: ErrValidation},
		"confirmed":          {mutate: func(r *Run) { r.State = Ready }, digest: "x", inputs: []OutputRef{subject}, err: ErrConflict},
		"chain step":         {mutate: func(r *Run) { r.PredecessorID = "run-0" }, digest: "x", inputs: []OutputRef{subject}, err: ErrConflict},
		"work item run":      {mutate: func(r *Run) { r.Subject = Subject{Kind: SubjectWorkItem, ID: "page-1"} }, digest: "x", inputs: []OutputRef{subject}, err: ErrConflict},
	} {
		r := suggested()
		if tc.mutate != nil {
			tc.mutate(&r)
		}
		assert.ErrorIs(t, r.RepinSources(tc.digest, tc.inputs), tc.err, name)
		assert.Equal(t, "old", r.InputDigest, name)
	}
}

func TestBindCandidateValidatesTheRefs(t *testing.T) {
	sha := strings.Repeat("b", 40)
	build := Run{ID: "build-1", OwnerID: "dev-1", Outputs: []OutputRef{{Kind: RefRepository, ResourceID: "mr-1", RepositoryID: "repo", CommitSHA: sha}}}
	review := Run{ID: "review-1", Runner: Findings, State: Suggested, InputDigest: "old"}
	assert.ErrorIs(t, review.BindCandidate(build, "", nil), ErrValidation)
	assert.ErrorIs(t, review.BindCandidate(build, "d", []OutputRef{{Kind: RefWorkItem, ResourceID: "item"}}), ErrValidation)
	assert.Equal(t, "old", review.InputDigest)
	require.NoError(t, review.BindCandidate(build, "d", build.Outputs))
	assert.Equal(t, "build-1", review.PredecessorID)
	assert.Equal(t, "dev-1", review.CandidateAuthorID)
	assert.Equal(t, build.Outputs, review.Outputs)
}

// People's notes on findings (dismissals and answers) change what a fix
// applies, so both lock together once the run handed off (completed) or
// anyone decided on it: a fix and an approval cover the notes they saw.
func TestFindingNotesLockAfterHandOffOrDecision(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	for _, cmd := range []Command{
		{Type: ResolveFinding, ExpectedVersion: 3, FindingID: "d1", Choice: ResolveSubject},
		{Type: DismissFinding, ExpectedVersion: 3, FindingID: "f1", Reason: "Applied by hand"},
	} {
		fresh := decideFindingsRun()
		assert.Contains(t, fresh.AllowedCommands(owner, HostFacts{}), cmd.Type, "submitted, undecided: open")
		for name, mutate := range map[string]func(*Run){
			"completed":     func(r *Run) { r.State = Completed },
			"decided":       func(r *Run) { r.Decisions = []Decision{{UserID: "boss", Kind: RequestChanges}} },
			"step decision": func(r *Run) { r.StepDecisions = []StepDecision{{Node: "approve"}} },
		} {
			r := decideFindingsRun()
			mutate(&r)
			assert.ErrorIs(t, r.Apply(owner, cmd), ErrConflict, "%s %s", cmd.Type, name)
			assert.NotContains(t, r.AllowedCommands(owner, HostFacts{}), cmd.Type, "%s %s", cmd.Type, name)
		}
	}
}
