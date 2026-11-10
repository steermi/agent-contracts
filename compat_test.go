package contracts_test

import (
	"encoding/json"
	"flag"
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
	golden(t, "capabilities", map[string]any{"runners": caps, "meeting": sorted(activity.CapabilitiesForMeeting())})
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
		assert.NotContains(t, c.AllowedCommands(owner), activity.Decide)
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
