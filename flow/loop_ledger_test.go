package flow_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/flow"
)

// checkedDefault is the default flow whose Build step requires the "ci" check.
func checkedDefault(advance activity.LoopAdvance) flow.Resolved {
	g := flow.Default()
	node(&g, "build").Requires.Checks = []string{"ci"}
	node(&g, "quality").Loop.Advance = advance
	return resolved(g)
}

func sha(n int) string { return fmt.Sprintf("%040x", n) }

// codeRun is a submitted Build on ref whose "ci" check has status (none when
// empty) with the given failed jobs.
func codeRun(id string, ref *activity.FlowRef, commit int, status activity.CheckStatus, jobs ...string) activity.Run {
	r := activity.Run{ID: id, Runner: activity.Code, State: activity.Submitted, Version: 1, OwnerID: "o", Location: activity.Cloud, Flow: ref,
		Policy:  activity.Policy{RequiredChecks: []string{"ci"}},
		Outputs: []activity.OutputRef{{Kind: activity.RefRepository, ResourceID: "r", RepositoryID: "r", CommitSHA: sha(commit)}}}
	if status != "" {
		r.Checks = []activity.Check{{RepositoryID: "r", CommitSHA: sha(commit), Name: "ci", ProviderRunID: "p", Attempt: 1, Status: status,
			Provenance: activity.ProviderVerified, FailedJobs: jobs, DetailsURL: "https://ci.example.com/p/1"}}
	}
	return r
}

func TestRepeatHopPinnedOnlyForCheckedCodeStepsInLoops(t *testing.T) {
	plain, _ := flow.DefaultResolved().Ref("v", "build", nil)
	assert.False(t, plain.HasRepeatHop(), "no required checks: today's repeat")

	ask, _ := checkedDefault("").Ref("v", "build", nil)
	require.True(t, ask.HasRepeatHop())
	assert.Equal(t, activity.FlowHop{Node: "build", Name: "Build", Runner: activity.Code, Back: true, InLoop: true}, ask.Hops[activity.OutcomeRepeat])

	explicitAsk, _ := checkedDefault(activity.LoopAdvanceAsk).Ref("v", "build", nil)
	assert.False(t, explicitAsk.Hops[activity.OutcomeRepeat].Auto)

	auto, _ := checkedDefault(activity.LoopAdvanceAuto).Ref("v", "build", nil)
	assert.True(t, auto.Hops[activity.OutcomeRepeat].Auto)

	g := flow.Default()
	node(&g, "plan").Skill = "steermi:build"
	node(&g, "plan").Requires.Checks = []string{"ci"}
	outside, _ := resolved(g).Ref("v", "plan", nil)
	assert.False(t, outside.HasRepeatHop(), "code steps outside loops keep today's repeat")
}

func TestFailedCheckRound(t *testing.T) {
	g := checkedDefault(activity.LoopAdvanceAuto)
	owner := activity.Actor{UserID: "o", Role: "member"}
	ref, _ := g.Ref("v", "build", nil)
	repeat := ref.Hops[activity.OutcomeRepeat]
	repeat.Location = activity.Cloud // the host pins each hop's location
	ref.Hops[activity.OutcomeRepeat] = repeat

	pending := codeRun("b1", &ref, 1, activity.CheckPending)
	_, _, ok := pending.FlowNext()
	assert.False(t, ok, "pending checks wait")
	assert.True(t, pending.WaitingForChecks())
	_, err := pending.Successor(owner, activity.Command{Type: activity.Next, ExpectedVersion: 1})
	assert.ErrorIs(t, err, activity.ErrConflict)

	missing := codeRun("b1", &ref, 1, "")
	_, _, ok = missing.FlowNext()
	assert.False(t, ok, "missing checks wait")

	red := codeRun("b1", &ref, 1, activity.CheckFailed, "unit")
	hop, stop, ok := red.FlowNext()
	require.True(t, ok)
	assert.Empty(t, stop)
	assert.True(t, hop.Back)
	auto, ok := red.AutoHop()
	assert.True(t, ok)
	assert.Equal(t, "build", auto.Node)
	spawn, err := red.Successor(owner, activity.Command{Type: activity.Next, ExpectedVersion: 1})
	kind := spawn.Runner
	require.NoError(t, err)
	assert.Equal(t, activity.Code, kind)

	next, ok := g.RefVia("v", red.SuccessorNode(activity.Next), &red, activity.Next)
	require.True(t, ok)
	assert.Equal(t, 2, next.Loop.Iteration, "a failed check is a round")
	assert.Equal(t, []string{activity.CheckFingerprint(red.FailedChecks())}, next.Loop.Fingerprints)
	require.Len(t, next.Loop.Ledger, 1)
	step := next.Loop.Ledger[0].Steps[0]
	assert.Equal(t, 1, next.Loop.Ledger[0].Iteration)
	assert.Equal(t, "b1", step.RunID)
	require.Len(t, step.Red, 1)
	assert.Equal(t, activity.Red{Kind: activity.RedCheck, Ref: "ci", Saw: "unit", URL: "https://ci.example.com/p/1"}, step.Red[0])

	for _, via := range []activity.CommandType{activity.Retry, activity.Refine} {
		same, _ := g.RefVia("v", "build", &red, via)
		assert.Equal(t, 1, same.Loop.Iteration, "%s never counts", via)
		assert.Empty(t, same.Loop.Fingerprints)
	}
	retry, _ := g.RefVia("v", "build", &red, activity.Retry)
	assert.Empty(t, retry.Loop.Ledger, "a retry adds no step")

	// Round 2 fails on the same job: the stall stops the loop.
	again := codeRun("b2", &next, 2, activity.CheckFailed, "unit")
	hop, stop, ok = again.FlowNext()
	require.True(t, ok)
	assert.Equal(t, activity.BlockerLoopStalled, stop)
	assert.True(t, hop.Approval)
	_, ok = again.AutoHop()
	assert.False(t, ok)

	// A GitLab pipeline failing in another job is another problem.
	other := codeRun("b2", &next, 2, activity.CheckFailed, "lint")
	_, stop, _ = other.FlowNext()
	assert.Empty(t, stop)

	// A green re-run on the same commit wins: done, no round.
	green := codeRun("b2", &next, 2, activity.CheckFailed, "unit")
	green.Checks = append(green.Checks[:0], activity.Check{RepositoryID: "r", CommitSHA: sha(2), Name: "ci", ProviderRunID: "p", Attempt: 2,
		Status: activity.CheckSuccess, Provenance: activity.ProviderVerified, StartedAt: time.Now()})
	assert.Equal(t, activity.OutcomeDone, green.Outcome())
	hop, _, _ = green.FlowNext()
	assert.Equal(t, "review", hop.Node)
}

func TestRecordedRerunOutranksFailure(t *testing.T) {
	ref, _ := checkedDefault("").Ref("v", "build", nil)
	r := codeRun("b1", &ref, 1, activity.CheckFailed, "unit")
	r.Checks[0].StartedAt = time.Unix(100, 0)
	require.True(t, r.ChecksFailed())
	git := activity.Actor{UserID: "git", Purpose: activity.PurposeVerifiedGit}
	require.NoError(t, r.Apply(git, activity.Command{Type: activity.RecordCheck, ExpectedVersion: 1, Check: &activity.Check{
		RepositoryID: "r", CommitSHA: sha(1), Name: "ci", ProviderRunID: "p", Attempt: 2, Status: activity.CheckSuccess, StartedAt: time.Unix(200, 0)}}))
	assert.False(t, r.ChecksFailed())
	assert.Equal(t, activity.OutcomeDone, r.Outcome())
}

func TestCheckRoundWhenCodeStepIsNotTheEntry(t *testing.T) {
	g := flow.Default()
	node(&g, "quality").Body = []string{"review", "build"}
	node(&g, "build").Requires.Checks = []string{"ci"}
	*edge(&g, "plan", activity.OutcomeDone) = flow.Edge{From: "plan", To: "quality", On: activity.OutcomeDone}
	*edge(&g, "build", activity.OutcomeDone) = flow.Edge{From: "build", To: "review", On: activity.OutcomeDone}
	*edge(&g, "review", activity.OutcomeChangesRequested) = flow.Edge{From: "review", To: "build", On: activity.OutcomeChangesRequested}
	r := resolved(g)
	ref, _ := r.Ref("v", "build", nil)
	red := codeRun("b1", &ref, 1, activity.CheckFailed, "unit")
	next, _ := r.RefVia("v", "build", &red, activity.Next)
	assert.Equal(t, ref.Loop.Iteration+1, next.Loop.Iteration)
}

func TestRunWithoutPinnedRepeatHopKeepsTodaysRepeat(t *testing.T) {
	ref, _ := flow.DefaultResolved().Ref("v", "build", nil)
	r := codeRun("b1", &ref, 1, activity.CheckPending)
	hop, stop, ok := r.FlowNext()
	require.True(t, ok)
	assert.Empty(t, stop)
	assert.Equal(t, activity.FlowHop{Node: "build", Name: r.SkillName(), Runner: activity.Code}, hop)
	next, _ := flow.DefaultResolved().RefVia("v", "build", &r, activity.Next)
	assert.Equal(t, 1, next.Loop.Iteration)
}

func TestLedgerRecordsEveryInLoopHop(t *testing.T) {
	g := flow.DefaultResolved()
	plan, _ := g.Ref("v", "plan", nil)
	build, _ := g.Ref("v", "build", &activity.Run{ID: "p", Runner: activity.Answer, Flow: &plan})
	assert.Empty(t, build.Loop.Ledger, "entering the loop records nothing")

	b := codeRun("b1", &build, 1, "")
	b.Policy = activity.Policy{}
	b.Tried = "reorder auth middleware"
	b.Cost = 1.9
	review, _ := g.RefVia("v", "review", &b, activity.Next)
	require.Len(t, review.Loop.Ledger, 1)
	assert.Equal(t, "reorder auth middleware", review.Loop.Ledger[0].Steps[0].Tried)
	assert.Equal(t, sha(1), review.Loop.Ledger[0].Steps[0].Commit)

	rv := activity.Run{ID: "r1", Runner: activity.Findings, State: activity.Submitted, Flow: &review, Cost: 2.5, Recommendation: activity.ChangesRequested,
		Findings: []activity.Finding{
			{ID: "f1", Severity: activity.High, Location: "auth/refresh.py:74", Description: "session cookie not cleared", Expected: "cookie cleared", Observed: "cookie kept"},
			{ID: "f2", Severity: activity.Low, Description: "naming"},
		}}
	next, _ := g.RefVia("v", "build", &rv, activity.ApplyFixes)
	assert.Equal(t, 2, next.Loop.Iteration)
	require.Len(t, next.Loop.Ledger, 1)
	steps := next.Loop.Ledger[0].Steps
	require.Len(t, steps, 2)
	assert.Equal(t, []activity.Red{{Kind: activity.RedFinding, Ref: "f1", Severity: "high", Expected: "cookie cleared", Saw: "cookie kept", Location: "auth/refresh.py:74"}}, steps[1].Red,
		"findings below the threshold are not reds")
	assert.Equal(t, []float64{2.5}, activity.RoundCosts(next.Loop.Ledger, 0))

	// The run a person looks at adds its own step.
	b2 := codeRun("b2", &next, 2, "")
	b2.Policy = activity.Policy{}
	view := b2.Ledger()
	require.Len(t, view, 2)
	assert.Equal(t, 2, view[1].Iteration)
	assert.Equal(t, "b2", view[1].Steps[0].RunID)
	assert.Len(t, next.Loop.Ledger, 1, "the pinned ledger is not mutated")
}

func TestStallMatchLocation(t *testing.T) {
	g := flow.Default()
	node(&g, "quality").Loop.StallMatch = activity.StallLocation
	r := resolved(g)
	build, _ := r.Ref("v", "build", nil)
	review, _ := r.RefVia("v", "review", &activity.Run{Runner: activity.Code, Flow: &build}, activity.Next)
	review1 := activity.Run{Runner: activity.Findings, State: activity.Submitted, Flow: &review, Recommendation: activity.ChangesRequested,
		Findings: []activity.Finding{{ID: "1", Severity: activity.High, Location: "a.go:10", Description: "cookie not cleared"}}}
	build2, _ := r.RefVia("v", "build", &review1, activity.Next)
	review2Ref, _ := r.RefVia("v", "review", &activity.Run{Runner: activity.Code, Flow: &build2}, activity.Next)
	reworded := activity.Run{Runner: activity.Findings, State: activity.Submitted, Flow: &review2Ref, Recommendation: activity.ChangesRequested,
		Findings: []activity.Finding{{ID: "9", Severity: activity.High, Location: "A.go:12", Description: "the session cookie stays set"}}}
	_, stop, _ := reworded.FlowNext()
	assert.Equal(t, activity.BlockerLoopStalled, stop, "location ignores wording and line")

	noLoc := func(d string) []activity.Finding {
		return []activity.Finding{{ID: "1", Severity: activity.High, Description: d}}
	}
	assert.NotEqual(t, activity.FingerprintBy(noLoc("a"), activity.StallLocation), activity.FingerprintBy(noLoc("b"), activity.StallLocation),
		"location-less findings compare exactly")
	assert.Equal(t, activity.Fingerprint(noLoc("a")), activity.FingerprintBy(noLoc("a"), ""), "absent = exact = today's digest")
}

func TestLoopSettingsValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*activity.LoopPolicy)
		ok   bool
	}{
		{"exact", func(p *activity.LoopPolicy) { p.StallMatch = activity.StallExact }, true},
		{"location", func(p *activity.LoopPolicy) { p.StallMatch = activity.StallLocation }, true},
		{"bad match", func(p *activity.LoopPolicy) { p.StallMatch = "fuzzy" }, false},
		{"ask", func(p *activity.LoopPolicy) { p.Advance = activity.LoopAdvanceAsk }, true},
		{"auto", func(p *activity.LoopPolicy) { p.Advance = activity.LoopAdvanceAuto }, true},
		{"missing", func(p *activity.LoopPolicy) { p.Advance = "" }, true},
		{"bad mode", func(p *activity.LoopPolicy) { p.Advance = "always" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := flow.Default()
			tc.edit(node(&g, "quality").Loop)
			err := resolved(g).Validate(flow.Options{})
			if tc.ok {
				assert.NoError(t, err)
			} else {
				assert.ErrorIs(t, err, flow.ErrInvalid)
			}
		})
	}
	g := flow.Default()
	require.True(t, node(&g, "quality").Loop.AutoAdvance())
	assert.False(t, g.HasAutoOutsideLoops(), "a loop advancing by itself needs no auto-edge policy")
}

func TestSimulateFailedCheckRounds(t *testing.T) {
	sim := checkedDefault(activity.LoopAdvanceAuto).Simulate(flow.Script{"build": {flow.CheckFailed}}, "", 1)
	require.GreaterOrEqual(t, len(sim.Steps), 3)
	assert.Equal(t, activity.OutcomeRepeat, sim.Steps[1].Outcome)
	assert.True(t, sim.Steps[1].Auto)
	last := sim.Steps[len(sim.Steps)-1]
	assert.Equal(t, activity.BlockerLoopStalled, last.Stop)
	assert.Equal(t, flow.SimApproval, sim.EndKind)
}
