package activity

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func docPageSubject(scope, id string) Subject {
	return Subject{Kind: SubjectDocPage, ID: "page-1", ScopeType: scope, ScopeID: id}
}

func TestDocPageSubjectValidate(t *testing.T) {
	for _, scope := range []string{"organization", "workspace", "team", "project"} {
		s := docPageSubject(scope, "scope-1")
		assert.NoError(t, s.Validate(Answer, Cloud), scope)
		assert.NoError(t, s.Validate(Findings, Cloud), scope)
	}
	for _, tc := range []struct {
		name     string
		subject  Subject
		runner   Runner
		location Location
	}{
		{"code runner", docPageSubject("project", "p"), Code, Cloud},
		{"conversation runner", docPageSubject("project", "p"), "", Cloud},
		{"local answer", docPageSubject("project", "p"), Answer, Local},
		{"local findings", docPageSubject("team", "t"), Findings, Local},
		{"unknown scope", docPageSubject("area", "a"), Answer, Cloud},
		{"empty scope id", docPageSubject("organization", ""), Answer, Cloud},
		{"empty page", Subject{SubjectDocPage, "", "project", "p"}, Answer, Cloud},
	} {
		assert.ErrorIs(t, tc.subject.Validate(tc.runner, tc.location), ErrValidation, tc.name)
	}
	assert.Equal(t, "doc_page:page-1", docPageSubject("team", "t").LockKey())
}

func TestDocPageRunIsNotASession(t *testing.T) {
	owner := Actor{OrgID: "org", UserID: "owner", Role: "member"}
	subject := docPageSubject("team", "team-1")
	r, err := NewRun("run", "org", "", "", owner, Request{Runner: Findings, Location: Cloud, InputDigest: "page@3", Subject: subject}, nil)
	require.NoError(t, err)
	assert.Equal(t, subject, r.RunSubject())
	assert.False(t, r.IsSessionWorkflow())
	assert.False(t, r.IsMeeting())
	assert.Empty(t, r.SessionID, "the dispatch worker attaches a session later")
	assert.Empty(t, r.ProjectID)
	assert.Empty(t, r.WorkItemID)
	assert.Equal(t, Suggested, r.State)
	assert.Equal(t, CapabilitiesForDocPage(Findings), r.Capabilities)
	for _, tool := range []string{"create_work_items", "update_work_item", "plan_sprint", "create_sprint_with_items", "propose_cycle_plan", "create_comment", "update_comment", "update_doc_page", "write_file"} {
		assert.False(t, r.Capabilities.Allows(tool), tool)
	}
	assert.True(t, r.Capabilities.Allows("submit_review_findings"))
	assert.Contains(t, r.AllowedCommands(owner, HostFacts{}), Confirm)
	assert.NotContains(t, r.AllowedCommands(owner, HostFacts{}), TakeLocal)

	org, err := NewRun("run", "org", "", "", owner, Request{Runner: Answer, Location: Cloud, InputDigest: "page@3", Subject: docPageSubject("organization", "org")}, nil)
	require.NoError(t, err)
	assert.Equal(t, CapabilitiesForDocPage(Answer), org.Capabilities)
	assert.True(t, org.Capabilities.Allows("update_doc_page"))
	assert.True(t, org.Capabilities.Allows("submit_activity_outputs"))
	for _, tool := range []string{"create_work_items", "update_work_item", "create_comment", "submit_review_findings", "create_doc_page", "search_docs"} {
		assert.False(t, org.Capabilities.Allows(tool), tool)
	}
	assert.Empty(t, CapabilitiesForDocPage(Code))
	assert.Empty(t, CapabilitiesForDocPage(""))
}

func TestDocPageRunRejectsInvalidRequests(t *testing.T) {
	owner := Actor{OrgID: "org", UserID: "owner"}
	for _, tc := range []struct {
		name          string
		project, item string
		q             Request
	}{
		{"code", "", "", Request{Runner: Code, Location: Cloud, InputDigest: "d", Subject: docPageSubject("project", "p")}},
		{"local", "", "", Request{Runner: Answer, Location: Local, InputDigest: "d", Subject: docPageSubject("project", "p")}},
		{"with project", "p", "", Request{Runner: Answer, Location: Cloud, InputDigest: "d", Subject: docPageSubject("project", "p")}},
		{"with item", "", "i", Request{Runner: Answer, Location: Cloud, InputDigest: "d", Subject: docPageSubject("project", "p")}},
	} {
		_, err := NewRun("run", "org", tc.project, tc.item, owner, tc.q, nil)
		assert.ErrorIs(t, err, ErrValidation, tc.name)
	}
}

func runningDocPage(t *testing.T, runner Runner) (Run, Actor) {
	t.Helper()
	owner := Actor{OrgID: "org", UserID: "owner", Role: "member"}
	r, err := NewRun("run", "org", "", "", owner, Request{Runner: runner, Location: Cloud, InputDigest: "page@3", Subject: docPageSubject("project", "p")}, nil)
	require.NoError(t, err)
	require.NoError(t, r.Apply(owner, Command{Type: Confirm, ExpectedVersion: r.Version}))
	require.NoError(t, r.Apply(owner, Command{Type: Start, ExpectedVersion: r.Version}))
	return r, owner
}

func TestDocPageRunStaysInTheCloud(t *testing.T) {
	r, owner := runningDocPage(t, Answer)
	assert.ErrorIs(t, r.Apply(owner, Command{Type: TakeLocal, ExpectedVersion: r.Version}), ErrForbidden)
	assert.ErrorIs(t, r.Apply(owner, Command{Type: Handoff, ExpectedVersion: r.Version}), ErrForbidden, "handoff waits for a local resume")
	coordinator := Actor{OrgID: "org", UserID: "system", Purpose: PurposeCoordinator}
	require.NoError(t, r.Apply(coordinator, Command{Type: Interrupt, ExpectedVersion: r.Version, Blocker: BlockerOrgBudget}))
	assert.NotContains(t, r.AllowedCommands(owner, HostFacts{}), TakeLocal)
	assert.Contains(t, r.AllowedCommands(owner, HostFacts{}), ResumeCloud, "a blocked cloud run resumes in the cloud")
	require.NoError(t, r.Apply(owner, Command{Type: ResumeCloud, ExpectedVersion: r.Version}))
	assert.Equal(t, Cloud, r.Location)
	assert.Equal(t, Ready, r.State)
}

func TestDocPageRunSubmitsAndSpawnsSuccessors(t *testing.T) {
	r, owner := runningDocPage(t, Answer)
	page := OutputRef{Kind: RefDocs, ResourceID: "page-1", AreaID: "area-1", Revision: "4", Hash: strings.Repeat("a", 64)}
	require.NoError(t, r.Apply(owner, Command{Type: Submit, ExpectedVersion: r.Version, Outputs: []OutputRef{page}}))
	assert.Equal(t, Submitted, r.State)
	spawn, err := r.Successor(owner, Command{Type: Refine, ExpectedVersion: r.Version, Reason: "Shorter"})
	runner := spawn.Runner
	require.NoError(t, err)
	assert.Equal(t, Answer, runner)
	next, err := NewRun("run-2", "org", "", "", owner, Request{Runner: Findings, Location: Cloud, InputDigest: "page@4", Subject: r.Subject}, &r)
	require.NoError(t, err)
	assert.Equal(t, r.RootID, next.RootID)
	assert.Equal(t, r.ID, next.PredecessorID)
	assert.Equal(t, []OutputRef{page}, next.Outputs)
}

func TestDocPageSuccessorsStayOnTheirPage(t *testing.T) {
	page, owner := runningDocPage(t, Answer)
	other := page.Subject
	other.ID = "page-2"
	_, err := NewRun("run-2", "org", "", "", owner, Request{Runner: Findings, Location: Cloud, InputDigest: "d", Subject: other}, &page)
	assert.ErrorIs(t, err, ErrValidation, "a successor never moves to another page")

	item, err := NewRun("item-run", "org", "p", "i", owner, Request{Runner: Code, Location: Cloud, InputDigest: "d"}, nil)
	require.NoError(t, err)
	_, err = NewRun("run-3", "org", "", "", owner, Request{Runner: Findings, Location: Cloud, InputDigest: "d", Subject: page.Subject}, &item)
	assert.ErrorIs(t, err, ErrValidation, "a work item run never spawns a page run")
	_, err = NewRun("run-4", "org", "p", "i", owner, Request{Runner: Findings, Location: Cloud, InputDigest: "d"}, &page)
	assert.ErrorIs(t, err, ErrValidation, "a page run never spawns a work item run")
	_, err = NewRun("run-5", "org", "p", "i", owner, Request{Runner: Findings, Location: Cloud, InputDigest: "d"}, &item)
	assert.NoError(t, err, "work item chains are unchanged")
}

func validFinding() Finding {
	return Finding{
		ID: "f1", Severity: High, Description: "The page contradicts the runbook",
		Target:       &FindingTarget{Type: FindingTargetDocPage, ID: "page-2", Version: "7"},
		SubjectQuote: "Deploys run on Fridays", TargetQuote: "Never deploy on Fridays",
		Suggestion: &FindingSuggestion{Action: SuggestMarkSuperseded, Rationale: "The runbook is newer"},
	}
}

func TestSubmitFindingsValidatesTargetsQuotesAndSuggestions(t *testing.T) {
	submit := func(f Finding) error {
		r, owner := runningDocPage(t, Findings)
		return r.Apply(owner, Command{Type: SubmitFindings, ExpectedVersion: r.Version, Summary: "One conflict", Recommendation: ChangesRequested, Findings: []Finding{f}})
	}
	require.NoError(t, submit(validFinding()))
	require.NoError(t, submit(Finding{ID: "f", Severity: Low, Description: "plain"}), "the new fields are optional")
	for _, action := range []SuggestionAction{SuggestLinkRelated, SuggestMarkSuperseded, SuggestUpdateTarget, SuggestUpdateSubject, SuggestMergeIntoTarget, SuggestNone} {
		f := validFinding()
		f.Suggestion.Action = action
		assert.NoError(t, submit(f), action)
	}
	for _, action := range []SuggestionAction{SuggestUpdateRule, SuggestRetireRule} {
		f := validFinding()
		f.Target.Type = FindingTargetContextEntry
		f.Suggestion.Action = action
		assert.NoError(t, submit(f), action)
	}
	cases := map[string]func(*Finding){
		"unknown target type":   func(f *Finding) { f.Target.Type = "repository" },
		"target without id":     func(f *Finding) { f.Target.ID = "" },
		"long subject quote":    func(f *Finding) { f.SubjectQuote = strings.Repeat("ż", MaxFindingQuoteRunes+1) },
		"long target quote":     func(f *Finding) { f.TargetQuote = strings.Repeat("x", MaxFindingQuoteRunes+1) },
		"target quote alone":    func(f *Finding) { f.Target = nil; f.Suggestion = nil },
		"unknown action":        func(f *Finding) { f.Suggestion.Action = "delete_page" },
		"empty action":          func(f *Finding) { f.Suggestion.Action = "" },
		"long patch":            func(f *Finding) { f.Suggestion.MarkdownPatch = strings.Repeat("x", MaxFindingPatchBytes+1) },
		"long rationale":        func(f *Finding) { f.Suggestion.Rationale = strings.Repeat("x", MaxFindingRationaleRunes+1) },
		"target action, no one": func(f *Finding) { f.Target, f.TargetQuote = nil, "" },
		"rule action on a page": func(f *Finding) { f.Suggestion.Action = SuggestRetireRule },
		"long target id":        func(f *Finding) { f.Target.ID = strings.Repeat("x", MaxFindingTargetIDBytes+1) },
		"long target version":   func(f *Finding) { f.Target.Version = strings.Repeat("x", MaxFindingTargetVersionBytes+1) },
	}
	for name, edit := range cases {
		f := validFinding()
		edit(&f)
		assert.ErrorIs(t, submit(f), ErrValidation, name)
	}
}

func TestFindingFieldsKeepDigests(t *testing.T) {
	plain := []Finding{{ID: "f", Severity: Low, Description: "d"}}
	assert.Equal(t, `[{"id":"f","severity":"low","description":"d"}]`, string(mustJSON(t, plain)), "new fields are omitted when empty")
	rich := validFinding()
	assert.Equal(t, Fingerprint([]Finding{{ID: rich.ID, Severity: rich.Severity, Description: rich.Description}}), Fingerprint([]Finding{rich}),
		"loop stall detection keys on severity, location and description only")
}

func TestEvaluatePageTrigger(t *testing.T) {
	for _, tc := range []struct {
		name    string
		trigger PageTrigger
		event   string
		fires   bool
	}{
		{"ask on publish", PageTrigger{PageEventPublished, "ask"}, PageEventPublished, true},
		{"ask on update", PageTrigger{PageEventUpdated, "ask"}, PageEventUpdated, true},
		{"other event", PageTrigger{PageEventPublished, "ask"}, PageEventUpdated, false},
		{"off", PageTrigger{PageEventPublished, "off"}, PageEventPublished, false},
		{"auto is not a page trigger mode", PageTrigger{PageEventPublished, "auto"}, PageEventPublished, false},
		{"unknown mode", PageTrigger{PageEventPublished, "always"}, PageEventPublished, false},
		{"unknown event", PageTrigger{"deleted", "ask"}, "deleted", false},
	} {
		assert.Equal(t, tc.fires, EvaluatePageTrigger(tc.trigger, tc.event), tc.name)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// The host drops findings whose quotes it cannot find in the pinned texts
// and records how many on the run; the count is an internal payload, never
// decoded from a command body, and is omitted when zero.
func TestSubmitFindingsRecordsDroppedFindings(t *testing.T) {
	r, owner := runningDocPage(t, Findings)
	require.NoError(t, r.Apply(owner, Command{Type: SubmitFindings, ExpectedVersion: r.Version, Summary: "All quotes invented", Recommendation: Inconclusive, DroppedFindings: 2}))
	assert.Equal(t, Submitted, r.State, "a run whose every finding was dropped still submits")
	assert.Equal(t, 2, r.DroppedFindings)
	assert.Contains(t, string(mustJSON(t, r)), `"dropped_findings":2`)

	plain, owner := runningDocPage(t, Findings)
	require.NoError(t, plain.Apply(owner, Command{Type: SubmitFindings, ExpectedVersion: plain.Version, Summary: "ok", Recommendation: Approve}))
	assert.NotContains(t, string(mustJSON(t, plain)), "dropped_findings", "omitted when nothing was dropped")

	negative, owner := runningDocPage(t, Findings)
	assert.ErrorIs(t, negative.Apply(owner, Command{Type: SubmitFindings, ExpectedVersion: negative.Version, Summary: "x", Recommendation: Approve, DroppedFindings: -1}), ErrValidation)

	var decoded Command
	require.NoError(t, json.Unmarshal([]byte(`{"command":"findings","dropped_findings":5,"DroppedFindings":5}`), &decoded))
	assert.Zero(t, decoded.DroppedFindings, "clients cannot claim a dropped count")
}
