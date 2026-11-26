package contracts_test

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/flow"
	"github.com/steermi/agent-contracts/locallaunch"
	"github.com/steermi/agent-contracts/skill/builtin"
)

// The compatibility corpus locks the wire shapes, digests and capability
// decisions the Steermi service and the CLI rely on. A diff here is a contract
// change: update the corpus with `go test -run TestCompat -update` only when
// the change is intended, and describe it in CHANGELOG.md.
var update = flag.Bool("update", false, "rewrite testdata/compat golden files")

var scopes = []string{"organization", "workspace", "project", "team"}

func golden(t *testing.T, name string, got any) {
	t.Helper()
	data, err := json.MarshalIndent(got, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')
	path := filepath.Join("testdata", "compat", name+".json")
	if *update {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, data, 0o644))
	}
	want, err := os.ReadFile(path)
	require.NoError(t, err, "missing golden file; run go test -run TestCompat -update")
	assert.JSONEq(t, string(want), string(data), "%s changed: this is a contract change", path)
}

func sorted(s activity.CapabilitySet) []string {
	out := make([]string, 0, len(s))
	for name, ok := range s {
		if ok {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

func TestCompatDefinitions(t *testing.T) {
	type def struct {
		Scopes    []string `json:"scopes"`
		Locations []string `json:"execution_locations"`
	}
	defs := map[string]def{}
	for _, k := range []activity.Runner{activity.Answer, activity.Code, activity.Findings} {
		d, ok := activity.Lookup(k)
		require.True(t, ok)
		defs[string(k)] = def{d.ApplicableScopes, d.ExecutionLocations}
	}
	_, ok := activity.Lookup("deploy")
	assert.False(t, ok, "unknown runners never resolve")
	golden(t, "definitions", map[string]any{"identity_digest": activity.Digest(activity.Identity), "runners": defs})
}

func TestCompatCapabilities(t *testing.T) {
	caps := map[string]map[string][]string{}
	for _, k := range []activity.Runner{"", activity.Answer, activity.Code, activity.Findings} {
		name := string(k)
		if name == "" {
			name = "conversation"
		}
		caps[name] = map[string][]string{}
		for _, scope := range scopes {
			caps[name][scope] = sorted(activity.CapabilitiesFor(k, scope))
		}
	}
	followUp := map[string]map[string][]string{}
	for _, subject := range []activity.SubjectKind{activity.SubjectWorkItem, activity.SubjectSession, activity.SubjectDocPage} {
		followUp[string(subject)] = map[string][]string{}
		for _, scope := range scopes {
			followUp[string(subject)][scope] = sorted(activity.CapabilitiesForFollowUp(subject, scope))
		}
	}
	golden(t, "capabilities", map[string]any{"runners": caps, "meeting": sorted(activity.CapabilitiesForMeeting()),
		"doc_page":  map[string][]string{"answer": sorted(activity.CapabilitiesForDocPage(activity.Answer)), "findings": sorted(activity.CapabilitiesForDocPage(activity.Findings))},
		"follow_up": followUp})
}

func TestCompatCapabilityDenials(t *testing.T) {
	cases := []struct {
		kind  activity.Runner
		scope string
		tool  string
	}{
		{activity.Findings, "project", "write_file"},
		{activity.Findings, "project", "create_pull_request"},
		{activity.Findings, "project", "submit_activity_outputs"},
		{activity.Answer, "project", "write_file"},
		{activity.Answer, "project", "batch_commit"},
		{activity.Code, "team", "write_file"},
		{activity.Code, "project", "submit_review_findings"},
		{activity.Code, "project", "unknown_tool"},
		{"deploy", "project", "read_file"},
	}
	for _, c := range cases {
		assert.False(t, activity.CapabilitiesFor(c.kind, c.scope).Allows(c.tool), "%s/%s must not allow %s", c.kind, c.scope, c.tool)
	}
	assert.False(t, activity.CapabilitiesForMeeting().Allows("update_work_item"), "meetings mutate only through reviewed approval actions")
}

var (
	at   = time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	sha  = "0123456789abcdef0123456789abcdef01234567"
	hash = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	repo = activity.OutputRef{Kind: activity.RefRepository, ResourceID: "mr-1", RepositoryID: "repo-1", CommitSHA: sha}
	docs = activity.OutputRef{Kind: activity.RefDocs, ResourceID: "page-1", AreaID: "area-1", Revision: "3", Hash: hash}
)

func syntheticRun(t *testing.T) activity.Run {
	t.Helper()
	owner := activity.Actor{OrgID: "org-1", UserID: "dev-1", Role: "member"}
	r, err := activity.NewRun("run-1", "org-1", "project-1", "item-1", owner, activity.Request{
		Runner: activity.Code, Location: activity.Local, InputDigest: activity.Digest("ticket@1"),
		Inputs: []activity.OutputRef{docs}, CommandKey: "cmd-1", Origin: "manual",
		Policy: activity.Policy{RequiredChecks: []string{"ci"}},
	}, nil)
	require.NoError(t, err)
	r.CreatedAt, r.UpdatedAt = at, at
	return r
}

func TestCompatWire(t *testing.T) {
	r := syntheticRun(t)
	r.Outputs = []activity.OutputRef{repo}
	r.Checks = []activity.Check{{RepositoryID: "repo-1", CommitSHA: sha, Name: "ci", ProviderRunID: "p-1", Attempt: 1, Status: activity.CheckSuccess, Provenance: activity.ProviderVerified, StartedAt: at}}
	cmd := activity.Command{Type: activity.Submit, CommandKey: "cmd-2", ExpectedVersion: 3, InputDigest: r.InputDigest, Outputs: []activity.OutputRef{repo}, Summary: "Adds retry"}

	var public []string
	for _, c := range []activity.CommandType{
		activity.Continue, activity.Confirm, activity.Retry, activity.Start, activity.Clarify, activity.TakeLocal,
		activity.ResumeCloud, activity.Handoff, activity.Fail, activity.Cancel, activity.Submit, activity.SubmitFindings,
		activity.Decide, activity.Revise, activity.Next, activity.ApplyFixes, activity.Refine, activity.CloseMeeting, activity.RecordMeetingRecap,
		activity.StopMeetingTurn, activity.TurnComplete, activity.Interrupt, activity.RecordCheck, activity.RecordCost,
	} {
		if c.Public() {
			public = append(public, string(c))
		}
	}
	golden(t, "wire", map[string]any{
		"run":             r,
		"command":         cmd,
		"evidence_digest": r.EvidenceDigest(),
		"public_commands": public,
		"digests": map[string]string{
			"string": activity.Digest("ticket@1"),
			"refs":   activity.Digest([]activity.OutputRef{repo, docs}),
			"nil":    activity.Digest(nil),
		},
	})
}

func TestCompatLocalLaunch(t *testing.T) {
	// RFC 7636 appendix B test vector.
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const challenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	assert.Equal(t, challenge, locallaunch.PKCEChallenge(verifier))
	assert.True(t, locallaunch.VerifyPKCE(verifier, challenge))
	for _, bad := range [][2]string{{"", challenge}, {verifier, ""}, {verifier + "x", challenge}, {verifier, challenge[:len(challenge)-1]}} {
		assert.False(t, locallaunch.VerifyPKCE(bad[0], bad[1]), "PKCE must reject %q/%q", bad[0], bad[1])
	}

	ids := map[string]bool{}
	for _, id := range []string{"lx_7Q4XK2M9ab", "lx_7Q4XK2M9a", "LX_7Q4XK2M9ab", "lx_7Q4XK2M9a!", "lx_7Q4XK2M9ab\n", " lx_7Q4XK2M9ab"} {
		ids[id] = locallaunch.IsPublicID(id)
	}
	golden(t, "locallaunch", map[string]any{
		"public_ids":  ids,
		"hash_secret": locallaunch.HashSecret("std_synthetic"),
		"branches": map[string]string{
			"feature":   locallaunch.BranchName("feature", "STE-123", "Add retry to the importer"),
			"bug":       locallaunch.BranchName("Bug", "STE-7", "Crash: nil pointer in `sync`!"),
			"chore":     locallaunch.BranchName("chore", "STE-9", ""),
			"unicode":   locallaunch.BranchName("task", "STE-10", "Zażółć gęślą jaźń"),
			"long":      locallaunch.BranchName("task", "STE-11", "A very long title that keeps going well past the sixty character branch limit"),
			"shell":     locallaunch.BranchName("task", "STE-12", "$(rm -rf ~); `id` && echo"),
			"separator": locallaunch.BranchName("task", "STE-13", "../../etc/passwd"),
		},
	})
}

// TestCompatBoundaries covers the decisions both consumers depend on: stale
// inputs, self-acceptance, provider-verified checks and lease generations.
func TestCompatBoundaries(t *testing.T) {
	owner := activity.Actor{OrgID: "org-1", UserID: "dev-1", Role: "member"}
	reviewer := activity.Actor{OrgID: "org-1", UserID: "rev-1", Role: "member"}
	coordinator := activity.Actor{OrgID: "org-1", UserID: "system", Purpose: activity.PurposeCoordinator}

	r := syntheticRun(t)
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.Confirm, ExpectedVersion: r.Version}))
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.Start, ExpectedVersion: r.Version}))

	t.Run("stale version and digest", func(t *testing.T) {
		c := r
		assert.ErrorIs(t, c.Apply(owner, activity.Command{Type: activity.Handoff, ExpectedVersion: c.Version - 1}), activity.ErrStale)
		assert.ErrorIs(t, c.Apply(owner, activity.Command{Type: activity.Handoff, ExpectedVersion: c.Version, InputDigest: activity.Digest("ticket@2")}), activity.ErrStale)
	})
	t.Run("invalid command", func(t *testing.T) {
		c := r
		assert.ErrorIs(t, c.Apply(owner, activity.Command{Type: "deploy", ExpectedVersion: c.Version}), activity.ErrValidation)
		assert.ErrorIs(t, c.Apply(owner, activity.Command{Type: activity.Retry, ExpectedVersion: c.Version}), activity.ErrValidation, "successor commands never mutate the run")
		assert.ErrorIs(t, c.Apply(owner, activity.Command{Type: activity.Submit, ExpectedVersion: c.Version}), activity.ErrValidation, "submit needs outputs")
	})
	t.Run("only the coordinator interrupts", func(t *testing.T) {
		c := r
		assert.ErrorIs(t, c.Apply(owner, activity.Command{Type: activity.Interrupt, ExpectedVersion: c.Version, Blocker: activity.BlockerLocalDisconnected}), activity.ErrForbidden)
		require.NoError(t, c.Apply(coordinator, activity.Command{Type: activity.Interrupt, ExpectedVersion: c.Version, Blocker: activity.BlockerLocalDisconnected}))
		assert.Equal(t, activity.Waiting, c.State)
	})
	t.Run("take local starts a new generation", func(t *testing.T) {
		c := r
		require.NoError(t, c.Apply(owner, activity.Command{Type: activity.Handoff, ExpectedVersion: c.Version}))
		gen := c.Generation
		c.LaunchID = "launch-1"
		require.NoError(t, c.Apply(owner, activity.Command{Type: activity.TakeLocal, ExpectedVersion: c.Version}))
		assert.Equal(t, gen+1, c.Generation, "an older launch can no longer complete this run")
		assert.Empty(t, c.LaunchID)
	})

	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.Submit, ExpectedVersion: r.Version, Outputs: []activity.OutputRef{repo}}))
	require.Equal(t, activity.Submitted, r.State)

	t.Run("submission is not acceptance", func(t *testing.T) {
		assert.False(t, r.HumanAccepted())
		assert.False(t, r.ChecksPassed(), "agent-reported checks are not provider verified")
	})
	t.Run("authors cannot accept their own work", func(t *testing.T) {
		c := r
		err := c.Apply(owner, activity.Command{Type: activity.Decide, ExpectedVersion: c.Version, Decision: activity.Accept, EvidenceDigest: c.EvidenceDigest()})
		assert.ErrorIs(t, err, activity.ErrForbidden)
		assert.NotContains(t, c.AllowedCommands(owner, activity.HostFacts{}), activity.Decide)
	})
	t.Run("acceptance waits for verified checks", func(t *testing.T) {
		c := r
		err := c.Apply(reviewer, activity.Command{Type: activity.Decide, ExpectedVersion: c.Version, Decision: activity.Accept, EvidenceDigest: c.EvidenceDigest()})
		assert.ErrorIs(t, err, activity.ErrChecksPending)
	})
	t.Run("acceptance binds the evidence digest", func(t *testing.T) {
		c := r
		git := activity.Actor{OrgID: "org-1", UserID: "system", Purpose: activity.PurposeVerifiedGit}
		require.NoError(t, c.Apply(git, activity.Command{Type: activity.RecordCheck, ExpectedVersion: c.Version, Check: &activity.Check{
			RepositoryID: "repo-1", CommitSHA: sha, Name: "ci", ProviderRunID: "p-1", Attempt: 1, Status: activity.CheckSuccess, StartedAt: at,
		}}))
		old := r.EvidenceDigest()
		assert.ErrorIs(t, c.Apply(reviewer, activity.Command{Type: activity.Decide, ExpectedVersion: c.Version, Decision: activity.Accept, EvidenceDigest: old}), activity.ErrStale)
		require.NoError(t, c.Apply(reviewer, activity.Command{Type: activity.Decide, ExpectedVersion: c.Version, Decision: activity.Accept, EvidenceDigest: c.EvidenceDigest()}))
		assert.True(t, c.HumanAccepted())
	})
}

// Built-in skills and the default flow are pinned by digest on every run;
// changing them is a contract change (bump builtin.Version).
func TestCompatSkillsAndFlows(t *testing.T) {
	skills, results := map[string]string{}, map[string]any{}
	for _, id := range builtin.IDs() {
		s, ok := builtin.Get(id)
		require.True(t, ok)
		skills[id] = s.Digest()
		results[id] = map[string]any{"result": s.Result(), "runner": s.Runner(), "starts_from": s.StartsFrom()}
	}
	golden(t, "skills_flows", map[string]any{"builtin_version": builtin.Version, "builtin_digests": skills, "builtin_results": results, "default_flow": flow.Default(), "default_flow_digest": flow.Default().Digest()})
}

// Docs page runs, the default docs page flow and findings that name a target
// are locked like the work item contracts.
func TestCompatDocPage(t *testing.T) {
	owner := activity.Actor{OrgID: "org-1", UserID: "dev-1", Role: "member"}
	subject := activity.Subject{Kind: activity.SubjectDocPage, ID: "page-1", ScopeType: "organization", ScopeID: "org-1"}
	r, err := activity.NewRun("run-1", "org-1", "", "", owner, activity.Request{
		Runner: activity.Findings, Location: activity.Cloud, InputDigest: activity.Digest("page-1@3"), Subject: subject,
		Inputs: []activity.OutputRef{docs}, CommandKey: "cmd-1", Origin: "manual",
	}, nil)
	require.NoError(t, err)
	r.CreatedAt, r.UpdatedAt = at, at
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.Confirm, ExpectedVersion: r.Version}))
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.Start, ExpectedVersion: r.Version}))
	finding := activity.Finding{
		ID: "f-1", Severity: activity.High, Location: "Deploys", Description: "Contradicts the release runbook",
		Target:       &activity.FindingTarget{Type: activity.FindingTargetDocPage, ID: "page-2", Version: "7"},
		SubjectQuote: "Deploys run on Fridays", TargetQuote: "Never deploy on Fridays",
		Suggestion: &activity.FindingSuggestion{Action: activity.SuggestMarkSuperseded, Rationale: "The runbook is newer"},
	}
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.SubmitFindings, ExpectedVersion: r.Version, Summary: "One conflict", Recommendation: activity.ChangesRequested, Findings: []activity.Finding{finding}}))
	r.UpdatedAt = at
	assert.ErrorIs(t, r.Apply(owner, activity.Command{Type: activity.TakeLocal, ExpectedVersion: r.Version}), activity.ErrForbidden)

	// system:doc:1 was the default docs page flow when this corpus was cut;
	// it stays resolvable under its version for runs that pin it.
	doc, ok := flow.BuiltinGraph(flow.DocVersionV1)
	require.True(t, ok)
	triggered := flow.Graph{
		Subject: flow.SubjectDocPage, Start: "draft",
		Nodes: []flow.Node{
			{Key: "draft", Type: flow.NodeAgent, Name: "Draft", Skill: "steermi:plan", PageTriggers: []activity.PageTrigger{{Event: activity.PageEventUpdated, Mode: flow.ModeAsk}}},
			{Key: "check", Type: flow.NodeAgent, Name: "Check", Skill: "steermi:review", PageTriggers: []activity.PageTrigger{{Event: activity.PageEventPublished, Mode: flow.ModeAsk}}},
			{Key: "end", Type: flow.NodeEnd},
		},
		Edges: []flow.Edge{
			{From: "draft", To: "check", On: activity.OutcomeDone, Start: flow.ModeAsk},
			{From: "check", To: "end", On: activity.OutcomeApproved},
		},
	}
	resolvedTriggered, issues := triggered.Resolve(nil)
	require.Empty(t, issues)
	require.NoError(t, resolvedTriggered.Validate(flow.Options{}))
	golden(t, "doc_page", map[string]any{
		"run":                      r,
		"evidence_digest":          r.EvidenceDigest(),
		"capabilities":             sorted(r.Capabilities),
		"default_flow":             doc,
		"default_flow_version":     flow.DocVersionV1,
		"default_flow_digest":      doc.Digest(),
		"work_item_default_digest": flow.DefaultFor("").Digest(),
		"triggered_flow":           triggered,
		"triggered_flow_digest":    triggered.Digest(),
	})
}

// The second default docs page flow (system:doc:2: Docs reconcile → ask →
// Docs fix) and its skills are locked like the first; system:doc:1 keeps its
// own golden in doc_page.json.
func TestCompatDocPageV2(t *testing.T) {
	doc := flow.DefaultFor(flow.SubjectDocPage)
	pinned, ok := flow.BuiltinGraph(flow.DefaultDocVersion)
	require.True(t, ok)
	require.Equal(t, doc, pinned)
	r, issues := doc.Resolve(nil)
	require.Empty(t, issues)
	require.NoError(t, r.Validate(flow.Options{}))
	reconcile, ok := r.Ref(flow.DefaultDocVersion, "reconcile", nil)
	require.True(t, ok)
	fix, ok := r.Ref(flow.DefaultDocVersion, "fix", nil)
	require.True(t, ok)
	skills := map[string]any{}
	for _, id := range []string{"steermi:docs-reconcile", "steermi:docs-fix"} {
		s, ok := builtin.Get(id)
		require.True(t, ok)
		skills[id] = map[string]any{"digest": s.Digest(), "result": s.Result(), "runner": s.Runner(), "starts_from": s.StartsFrom(), "effort": s.Effort(), "sources": s.Sources(), "required": builtin.Required(id)}
	}
	golden(t, "doc_page_v2", map[string]any{
		"default_flow":         doc,
		"default_flow_version": flow.DefaultVersionFor(flow.SubjectDocPage),
		"default_flow_digest":  doc.Digest(),
		"page_rules":           r.PageRules(),
		"reconcile_ref":        reconcile,
		"fix_ref":              fix,
		"skills":               skills,
	})
}

// A docs page findings run whose host dropped findings with unverifiable
// quotes records the count (dropped_findings); runs that dropped nothing
// keep their earlier shape (doc_page.json is unchanged).
func TestCompatDroppedFindings(t *testing.T) {
	owner := activity.Actor{OrgID: "org-1", UserID: "dev-1", Role: "member"}
	subject := activity.Subject{Kind: activity.SubjectDocPage, ID: "page-1", ScopeType: "organization", ScopeID: "org-1"}
	r, err := activity.NewRun("run-1", "org-1", "", "", owner, activity.Request{
		Runner: activity.Findings, Location: activity.Cloud, InputDigest: activity.Digest("page-1@3"), Subject: subject,
		Inputs: []activity.OutputRef{docs}, CommandKey: "cmd-1", Origin: "manual",
	}, nil)
	require.NoError(t, err)
	r.CreatedAt, r.UpdatedAt = at, at
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.Confirm, ExpectedVersion: r.Version}))
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.Start, ExpectedVersion: r.Version}))
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.SubmitFindings, ExpectedVersion: r.Version, Summary: "Quotes not found", Recommendation: activity.Inconclusive, DroppedFindings: 2}))
	r.UpdatedAt = at
	golden(t, "dropped_findings", map[string]any{
		"run":             r,
		"evidence_digest": r.EvidenceDigest(),
	})
}

// A person dismisses one finding of a docs page findings run
// (dismiss_finding): the command's wire shape, the run with the dismissal
// reason and its evidence digest, which equals the digest before dismissal.
func TestCompatDismissFinding(t *testing.T) {
	owner := activity.Actor{OrgID: "org-1", UserID: "dev-1", Role: "member"}
	subject := activity.Subject{Kind: activity.SubjectDocPage, ID: "page-1", ScopeType: "organization", ScopeID: "org-1"}
	r, err := activity.NewRun("run-1", "org-1", "", "", owner, activity.Request{
		Runner: activity.Findings, Location: activity.Cloud, InputDigest: activity.Digest("page-1@3"), Subject: subject,
		Inputs: []activity.OutputRef{docs}, CommandKey: "cmd-1", Origin: "manual",
	}, nil)
	require.NoError(t, err)
	r.CreatedAt, r.UpdatedAt = at, at
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.Confirm, ExpectedVersion: r.Version}))
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.Start, ExpectedVersion: r.Version}))
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.SubmitFindings, ExpectedVersion: r.Version, Summary: "One contradiction",
		Recommendation: activity.ChangesRequested, Findings: []activity.Finding{
			{ID: "f-1", Severity: activity.High, Description: "Retention period differs"},
			{ID: "f-2", Severity: activity.Low, Description: "Duplicate paragraph"},
		}}))
	before := r.EvidenceDigest()
	cmd := activity.Command{Type: activity.DismissFinding, CommandKey: "cmd-5", ExpectedVersion: r.Version, FindingID: "f-2", Reason: "Applied"}
	require.NoError(t, r.Apply(owner, cmd))
	r.UpdatedAt = at
	require.Equal(t, before, r.EvidenceDigest())
	golden(t, "dismiss_finding", map[string]any{
		"command":          cmd,
		"run":              r,
		"evidence_digest":  r.EvidenceDigest(),
		"allowed_commands": r.AllowedCommands(owner, activity.HostFacts{}),
	})
}

// A person answers a decide finding of a docs page findings run
// (resolve_finding): the command's wire shape, the run with the resolution
// (its time pinned) and its evidence digest, which equals the digest before;
// which fix applies each finding; and a linked fix on another page
// (linked_from_id) whose Retry keeps the link.
func TestCompatResolveFinding(t *testing.T) {
	owner := activity.Actor{OrgID: "org-1", UserID: "dev-1", Role: "member"}
	subject := activity.Subject{Kind: activity.SubjectDocPage, ID: "page-1", ScopeType: "organization", ScopeID: "org-1"}
	r, err := activity.NewRun("run-1", "org-1", "", "", owner, activity.Request{
		Runner: activity.Findings, Location: activity.Cloud, InputDigest: activity.Digest("page-1@3"), Subject: subject,
		Inputs: []activity.OutputRef{docs}, CommandKey: "cmd-1", Origin: "manual",
	}, nil)
	require.NoError(t, err)
	r.CreatedAt, r.UpdatedAt = at, at
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.Confirm, ExpectedVersion: r.Version}))
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.Start, ExpectedVersion: r.Version}))
	require.NoError(t, r.Apply(owner, activity.Command{Type: activity.SubmitFindings, ExpectedVersion: r.Version, Summary: "One open question",
		Recommendation: activity.ChangesRequested, Findings: []activity.Finding{{
			ID: "f-1", Severity: activity.High, Description: "Cutover day differs",
			Target:       &activity.FindingTarget{Type: activity.FindingTargetDocPage, ID: "page-2", Version: "4"},
			SubjectQuote: "the day after its billing date", TargetQuote: "on its billing date",
			Suggestion: &activity.FindingSuggestion{Action: activity.SuggestDecide, Rationale: "Both pages are recent."},
		}, {
			ID: "f-2", Severity: activity.Medium, Description: "Refund window is out of date on page-2",
			Target:       &activity.FindingTarget{Type: activity.FindingTargetDocPage, ID: "page-2", Version: "4"},
			SubjectQuote: "within 30 days", TargetQuote: "within 14 days",
			Suggestion: &activity.FindingSuggestion{Action: activity.SuggestUpdateTarget, MarkdownPatch: "within 30 days"},
		}}}))
	before := r.EvidenceDigest()
	cmd := activity.Command{Type: activity.ResolveFinding, CommandKey: "cmd-5", ExpectedVersion: r.Version, FindingID: "f-1", Choice: activity.ResolveTarget, Reason: "Finance confirmed"}
	require.NoError(t, r.Apply(owner, cmd))
	r.UpdatedAt = at
	r.Findings[0].Resolution.At = at
	require.Equal(t, before, r.EvidenceDigest())

	// f-2 asks to change page-2: a linked fix on page-2 applies it
	// (linked_from_id = the check). It fails; its Retry keeps the link.
	page2 := activity.OutputRef{Kind: activity.RefDocs, ResourceID: "page-2", AreaID: "area-1", Revision: "4", Hash: hash}
	fixRequest := activity.Request{
		Runner: activity.Answer, Location: activity.Cloud, InputDigest: activity.Digest("page-2@4"), Inputs: []activity.OutputRef{page2},
		Subject:    activity.Subject{Kind: activity.SubjectDocPage, ID: "page-2", ScopeType: "organization", ScopeID: "org-1"},
		CommandKey: "cmd-6", Origin: "docs", LinkedFromID: r.ID,
	}
	fix, err := activity.NewRun("fix-1", "org-1", "", "", owner, fixRequest, nil)
	require.NoError(t, err)
	for _, c := range []activity.CommandType{activity.Confirm, activity.Start, activity.Fail} {
		require.NoError(t, fix.Apply(owner, activity.Command{Type: c, ExpectedVersion: fix.Version}))
	}
	fix.CreatedAt, fix.UpdatedAt = at, at
	retry, err := fix.Successor(owner, activity.Command{Type: activity.Retry, ExpectedVersion: fix.Version})
	require.NoError(t, err)
	fixRequest.CommandKey, fixRequest.LinkedFromID = "cmd-7", retry.LinkedFromID
	retried, err := activity.NewRun("fix-2", "org-1", "", "", owner, fixRequest, &fix)
	require.NoError(t, err)
	retried.CreatedAt, retried.UpdatedAt = at, at
	golden(t, "resolve_finding", map[string]any{
		"command":          cmd,
		"run":              r,
		"evidence_digest":  r.EvidenceDigest(),
		"open_decisions":   r.OpenDecisions(),
		"allowed_commands": r.AllowedCommands(owner, activity.HostFacts{}),
		// The answer says page-2 is right: the fix on page-1 applies f-1;
		// f-2 goes to page-2's linked fix.
		"findings_for_subject": r.FindingsFor("page-1"),
		"findings_for_page_2":  r.FindingsFor("page-2"),
		"linked_fix_pages":     r.LinkedFixPages(),
		"linked_fix":           fix,
		"linked_fix_retry":     retried,
	})
}

// A run placed by a host's flow assignment rule carries the rule and the
// split arm on its flow ref (rule, arm), and its successors keep them.
// Unattributed refs keep their earlier shape (no rule/arm keys), so stored
// runs and every digest are unchanged.
func TestCompatFlowAttribution(t *testing.T) {
	g := flow.DefaultResolved()
	plan, ok := g.Ref("v1", "plan", nil)
	require.True(t, ok)
	unattributed := plan
	plan.Rule, plan.Arm = "rule-1", "fast"
	build, ok := g.Ref("v1", "build", &activity.Run{Runner: activity.Answer, Flow: &plan})
	require.True(t, ok)
	golden(t, "flow_attribution", map[string]any{
		"unattributed": unattributed,
		"placed":       plan,
		"successor":    build,
	})
}

// Quality loops on evidence: a code step with required checks inside a loop
// pins a failed-check round (hop "repeat", back), every in-loop successor
// carries the round ledger, and checks carry their details. A run placed
// without them (old refs, loops without checks) keeps its shape: no repeat
// hop, no ledger key, and today's repeat behaviour. 0.3.0: the system:1
// ("old") hops are unchanged except the new in_loop marker on hops between
// the loop's steps; Auto stays false (TestSystemV1HopsUnchangedExceptInLoop
// pins the v0.2.0 literal). The checked loop moved from on_failed_check to
// advance.
func TestCompatLoopLedger(t *testing.T) {
	sha := func(n int) string { return fmt.Sprintf("%040x", n) }
	v1, _ := flow.BuiltinGraph(flow.VersionV1)
	old, _ := v1.Resolve(nil)
	oldBuild, ok := old.Ref("v1", "build", nil)
	require.True(t, ok)

	g := flow.Default()
	g.Nodes[2].Requires.Checks = []string{"ci"}
	g.Nodes[1].Loop.StallMatch = activity.StallLocation
	checked, _ := g.Resolve(nil)
	build, ok := checked.Ref("v1", "build", nil)
	require.True(t, ok)
	red := activity.Run{ID: "b1", Runner: activity.Code, State: activity.Submitted, Version: 1, OwnerID: "o", Location: activity.Cloud, Flow: &build,
		Cost: 1.5, Tried: "reorder auth middleware", Policy: activity.Policy{RequiredChecks: []string{"ci"}},
		Outputs: []activity.OutputRef{{Kind: activity.RefRepository, ResourceID: "r", RepositoryID: "r", CommitSHA: sha(1)}},
		Checks: []activity.Check{{RepositoryID: "r", CommitSHA: sha(1), Name: "ci", ProviderRunID: "p1", Attempt: 1, Status: activity.CheckFailed,
			Provenance: activity.ProviderVerified, StartedAt: time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC),
			DetailsURL: "https://ci.example.com/p/1", Summary: "2 jobs failed", FailedJobs: []string{"lint", "unit"}}}}
	round2, ok := checked.RefVia("v1", "build", &red, activity.Next)
	require.True(t, ok)
	golden(t, "loop_ledger", map[string]any{
		"old_build":         oldBuild,
		"checked_build":     build,
		"red_check_run":     red,
		"evidence_digest":   red.EvidenceDigest(),
		"check_fingerprint": activity.CheckFingerprint(red.FailedChecks()),
		"round_2":           round2,
		"location_fingerprint": activity.FingerprintBy([]activity.Finding{{ID: "f", Severity: activity.High, Location: "a.go:12", Description: "x"}},
			activity.StallLocation),
	})
}

// Flow run UX (0.3.0): system:1 keeps its digest and hops for pinned runs
// (every hop asks); system:2 advances its loop by itself and runs Review in
// the cloud; runs carry step_mode, blocker_detail and pinned hop locations;
// set_step_mode, and confirm / next with a location, are public commands.
func TestCompatFlowRunUX(t *testing.T) {
	v1, ok := flow.BuiltinGraph(flow.VersionV1)
	require.True(t, ok)
	// v0.2.0's flow.Default().Digest(): system:1 never changes under its id.
	require.Equal(t, "995702d4f581e42eb3f23680b4b74a14b8b2578182763e952e17354e83aa9430", v1.Digest(), "system:1 never changes under its id")
	refs := func(g flow.Graph, version string) map[string]activity.FlowRef {
		r, issues := g.Resolve(nil)
		require.Empty(t, issues)
		out := map[string]activity.FlowRef{}
		for _, key := range []string{"plan", "build", "review"} {
			ref, ok := r.Ref(version, key, nil)
			require.True(t, ok)
			out[key] = ref
		}
		return out
	}
	owner := activity.Actor{OrgID: "org-1", UserID: "dev-1", Role: "member"}
	build := refs(flow.Default(), flow.DefaultVersion)["build"]
	done := build.Hops[activity.OutcomeDone]
	done.Location = activity.Cloud
	build.Hops[activity.OutcomeDone] = done
	run := activity.Run{ID: "r1", OrgID: "org-1", Runner: activity.Code, State: activity.Submitted, Version: 3, OwnerID: "dev-1",
		Location: activity.Local, StepMode: activity.StepModeAsk, Flow: &build}
	_, auto := run.AutoHop()
	next := activity.Command{Type: activity.Next, CommandKey: "k2", ExpectedVersion: 3, Location: activity.Local, StepMode: activity.StepModeFlow}
	placed, err := run.Successor(owner, activity.Command{Type: activity.Next, ExpectedVersion: 3})
	require.NoError(t, err)
	chosen, err := run.Successor(owner, next)
	require.NoError(t, err)
	blocked := activity.Run{ID: "r2", OrgID: "org-1", Runner: activity.Code, State: activity.Running, Version: 4, OwnerID: "dev-1", Location: activity.Cloud}
	require.NoError(t, blocked.Apply(activity.Actor{OrgID: "org-1", UserID: "dev-1", Purpose: activity.PurposeCoordinator},
		activity.Command{Type: activity.Interrupt, ExpectedVersion: 4, Blocker: activity.BlockerSourceUnavailable, BlockerDetail: activity.BlockerDetailRepositoryUnreachable}))
	blocked.UpdatedAt = at
	golden(t, "flow_run_ux", map[string]any{
		"default_version":       flow.DefaultVersion,
		"v1_version":            flow.VersionV1,
		"v1_digest":             v1.Digest(),
		"v1_refs":               refs(v1, flow.VersionV1),
		"v2_refs":               refs(flow.Default(), flow.DefaultVersion),
		"v2_has_auto_outside":   flow.Default().HasAutoOutsideLoops(),
		"run":                   run,
		"paused_run_auto":       auto,
		"allowed_commands":      run.AllowedCommands(owner, activity.HostFacts{}),
		"successor_placement":   map[string]any{"runner": placed.Runner, "location": placed.Location, "step_mode": placed.StepMode, "chosen": placed.LocationChosen},
		"chosen_placement":      map[string]any{"runner": chosen.Runner, "location": chosen.Location, "step_mode": chosen.StepMode, "chosen": chosen.LocationChosen},
		"can_choose_location":   run.CanChooseLocation(owner),
		"set_step_mode_command": activity.Command{Type: activity.SetStepMode, CommandKey: "k1", ExpectedVersion: 3, StepMode: activity.StepModeFlow},
		"next_command":          next,
		"blocked_run":           blocked,
		"blocker_details": []activity.BlockerDetail{activity.BlockerDetailRepositoryUnreachable,
			activity.BlockerDetailAccessLost, activity.BlockerDetailInputsMoved},
		"paused_reasons": []activity.AdvancePausedReason{activity.PausedOwnerInactive, activity.PausedProjectMoved,
			activity.PausedAutoNotAllowed, activity.PausedNoProviderKey, activity.PausedRouteNotAllowed},
	})
}

// Approval nodes with edges: the graph fields, the hop pinned on a run that
// stops there, the decision commands and the run fields a decision adds.
func TestCompatApproval(t *testing.T) {
	g := flow.Default()
	for i := range g.Nodes {
		if g.Nodes[i].Type == flow.NodeApproval {
			g.Nodes[i].Approver, g.Nodes[i].Note = activity.ApproverTeamManagers, "Check the acceptance criteria."
		}
	}
	g.Edges = append(g.Edges,
		flow.Edge{From: "owner", To: "quality", On: activity.OutcomeChangesRequested},
		flow.Edge{From: "owner", To: "in_review", On: activity.OutcomeApproved})
	r, issues := g.Resolve(nil)
	require.Empty(t, issues)
	require.NoError(t, r.Validate(flow.Options{}))
	ref, ok := r.Ref("v1", "review", nil)
	require.True(t, ok)
	// The host pins where each hop's step runs, decision hops included.
	back := ref.Loop.Exhausted.Decide[activity.OutcomeChangesRequested]
	back.Location = activity.Cloud
	ref.Loop.Exhausted.Decide[activity.OutcomeChangesRequested] = back

	run := activity.Run{ID: "r", Runner: activity.Findings, State: activity.Submitted, Version: 1, OwnerID: "owner", Location: activity.Cloud,
		Recommendation: activity.Inconclusive, Flow: &ref, CreatedAt: time.Unix(0, 0).UTC(), UpdatedAt: time.Unix(0, 0).UTC()}
	lead := activity.Actor{UserID: "lead", Role: "member"}
	manages := activity.HostFacts{TeamManager: true}
	waiting, ok := run.Approval(activity.DecisionFence{})
	require.True(t, ok)
	require.True(t, waiting.Waiting)
	allowed := run.AllowedCommands(lead, manages)
	require.NoError(t, run.Apply(lead, activity.Command{Type: activity.RequestStepChanges, ExpectedVersion: 1, Reason: "Handle the empty list.", Facts: manages}))
	require.Len(t, run.StepDecisions, 1)
	run.StepDecisions[0].At, run.UpdatedAt = time.Unix(0, 0).UTC(), time.Unix(0, 0).UTC()
	next, err := run.Successor(activity.Actor{UserID: "owner", Role: "member", Purpose: activity.PurposeFlow}, activity.Command{Type: activity.Next, ExpectedVersion: run.Version})
	require.NoError(t, err)
	hop, stop, _ := run.FlowNext()
	golden(t, "approval", map[string]any{
		"graph":             g,
		"pinned":            ref,
		"approver_commands": allowed,
		"decided_run":       run,
		"decided_hop":       hop,
		"decided_stop":      stop,
		// The successor's feedback is what its run stores (Run.Feedback).
		"successor_feedback": next.Feedback,
		"public":             map[string]bool{"approve_step": activity.ApproveStep.Public(), "request_step_changes": activity.RequestStepChanges.Public()},
	})
}
