package activity

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loopRef(node string, iteration int) *FlowRef {
	exhausted := FlowHop{Stop: "owner", Approval: true}
	return &FlowRef{VersionID: "v", Node: node, Loop: &LoopState{Key: "quality", Iteration: iteration, Exhausted: exhausted,
		LoopPolicy: LoopPolicy{MaxIterations: 3, StopBelow: High, StallRounds: 2, Budget: 5}}}
}

func TestFlowSuccessorFollowsPinnedHops(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	// An approved Review ends the fixed chain but follows its hop in a flow.
	review := Run{Runner: Findings, State: Submitted, Version: 1, InputDigest: "i", OwnerID: "owner", Recommendation: Approve}
	require.Equal(t, Runner(""), review.NextRunner())
	review.Flow = &FlowRef{VersionID: "v", Node: "review", Hops: map[Outcome]FlowHop{
		OutcomeApproved:         {Node: "notes", Runner: Answer},
		OutcomeChangesRequested: {Node: "build", Runner: Code, Back: true},
	}}
	require.Equal(t, Answer, review.NextRunner())
	require.Equal(t, "notes", review.SuccessorNode(Next))
	kind, err := review.Successor(owner, Command{Type: Next, ExpectedVersion: 1})
	require.NoError(t, err)
	require.Equal(t, Answer, kind)

	review.Recommendation = ChangesRequested
	require.Equal(t, Code, review.NextRunner())
	require.Equal(t, "build", review.SuccessorNode(Next))
	require.Equal(t, "build", review.SuccessorNode(ApplyFixes))
	require.Equal(t, "review", review.SuccessorNode(Retry))

	// A hop that stops ends the flow for Next.
	end := Run{Runner: Answer, State: Submitted, Version: 1, InputDigest: "i", OwnerID: "owner", Flow: &FlowRef{VersionID: "v", Node: "plan", Hops: map[Outcome]FlowHop{OutcomeDone: {Stop: "end"}}}}
	require.Equal(t, Runner(""), end.NextRunner())
	_, err = end.Successor(owner, Command{Type: Next, ExpectedVersion: 1})
	require.ErrorIs(t, err, ErrConflict)

	// Without a flow nothing follows: the flow is the only source of
	// successors.
	plan := Run{Runner: Answer, State: Submitted, Version: 1, InputDigest: "i", OwnerID: "owner"}
	require.Equal(t, Runner(""), plan.NextRunner())
	require.Equal(t, "", plan.SuccessorNode(Next))
	_, err = plan.Successor(owner, Command{Type: Next, ExpectedVersion: 1})
	require.ErrorIs(t, err, ErrConflict)
	offFlow := Run{Runner: Findings, State: Submitted, Version: 1, InputDigest: "i", OwnerID: "owner", Recommendation: ChangesRequested}
	_, err = offFlow.Successor(owner, Command{Type: ApplyFixes, ExpectedVersion: 1})
	require.ErrorIs(t, err, ErrConflict, "apply fixes follows the flow's rework edge")
}

func TestOneMoreRoundPastLoopStop(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	r := Run{Runner: Findings, State: Submitted, Version: 1, InputDigest: "i", OwnerID: "owner", Recommendation: ChangesRequested,
		Findings: []Finding{{ID: "1", Severity: High, Description: "bug"}}, Flow: loopRef("review", 3)}
	r.Flow.Hops = map[Outcome]FlowHop{OutcomeChangesRequested: {Node: "build", Runner: Code, Back: true}}
	_, stop, _ := r.FlowNext()
	require.Equal(t, BlockerRepairLimit, stop, "the loop stops at its iteration limit")
	_, err := r.Successor(owner, Command{Type: Next, ExpectedVersion: 1})
	require.ErrorIs(t, err, ErrConflict, "Next follows the stopped loop to a person")
	kind, err := r.Successor(owner, Command{Type: ApplyFixes, ExpectedVersion: 1})
	require.NoError(t, err, "a person may still ask for one more round")
	require.Equal(t, Code, kind)
}

func TestOutcomeUsesLoopThreshold(t *testing.T) {
	r := Run{Runner: Findings, State: Submitted, Recommendation: ChangesRequested, Findings: []Finding{{ID: "1", Severity: Low, Description: "nit"}}, Flow: loopRef("review", 1)}
	assert.Equal(t, OutcomeApproved, r.Outcome(), "findings below stop_below are follow-ups")
	r.Findings = append(r.Findings, Finding{ID: "2", Severity: High, Description: "bug"})
	assert.Equal(t, OutcomeChangesRequested, r.Outcome())
	r.Findings[1].DismissalReason = "accepted risk"
	assert.Equal(t, OutcomeApproved, r.Outcome(), "dismissed findings do not count")
	r.Flow = nil
	assert.Equal(t, OutcomeChangesRequested, r.Outcome(), "without a loop the recommendation decides")
	r.State = Failed
	assert.Equal(t, OutcomeFailed, r.Outcome())
	b := Run{Runner: Code, State: Submitted}
	assert.Equal(t, OutcomeDone, b.Outcome())
	b.Policy.RequiredChecks = []string{"ci"}
	b.Outputs = []OutputRef{{Kind: RefRepository, ResourceID: "r", RepositoryID: "r", CommitSHA: "0123456789012345678901234567890123456789"}}
	assert.Equal(t, OutcomeRepeat, b.Outcome())
}

func TestLoopStopTable(t *testing.T) {
	high := []Finding{{ID: "1", Severity: High, Location: "a.go:1", Description: "bug"}}
	tests := []struct {
		name string
		edit func(*Run)
		want Blocker
	}{
		{"another round", func(*Run) {}, ""},
		{"max iterations", func(r *Run) { r.Flow.Loop.Iteration = 3 }, BlockerRepairLimit},
		{"budget spent", func(r *Run) { r.Cost = 6; r.Flow.Loop.StartCost = 1 }, BlockerBudget},
		{"budget left", func(r *Run) { r.Cost = 5.5; r.Flow.Loop.StartCost = 1 }, ""},
		{"same findings again", func(r *Run) { r.Flow.Loop.Fingerprints = []string{Fingerprint(high)} }, BlockerLoopStalled},
		{"stall check off", func(r *Run) {
			r.Flow.Loop.Fingerprints = []string{Fingerprint(high)}
			r.Flow.Loop.StallRounds = 0
		}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := Run{Runner: Findings, State: Submitted, OwnerID: "o", Recommendation: ChangesRequested, Findings: high, Flow: loopRef("review", 1)}
			r.Flow.Hops = map[Outcome]FlowHop{OutcomeChangesRequested: {Node: "build", Runner: Code, Back: true}}
			tt.edit(&r)
			hop, stop, ok := r.FlowNext()
			require.True(t, ok)
			assert.Equal(t, tt.want, stop)
			if tt.want != "" {
				assert.Equal(t, r.Flow.Loop.Exhausted, hop)
				assert.Equal(t, Runner(""), r.NextRunner(), "a stopped loop hands to the person")
			} else {
				assert.Equal(t, Code, hop.Runner)
			}
		})
	}
}

func TestBuildWithoutNewRevisionStalls(t *testing.T) {
	out := []OutputRef{{Kind: RefRepository, ResourceID: "r", RepositoryID: "r", CommitSHA: "0123456789012345678901234567890123456789"}}
	b := Run{Runner: Code, State: Submitted, Outputs: out, Flow: loopRef("build", 2)}
	b.Flow.Hops = map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings}}
	b.Flow.Loop.Revision = RevisionDigest(out)
	_, stop, _ := b.FlowNext()
	assert.Equal(t, BlockerLoopStalled, stop)
	b.Flow.Loop.Revision = "other"
	_, stop, _ = b.FlowNext()
	assert.Equal(t, Blocker(""), stop)
}

func TestFingerprintIgnoresOrderAndIDs(t *testing.T) {
	a := []Finding{{ID: "1", Severity: High, Location: "a", Description: "x  y"}, {ID: "2", Severity: Low, Description: "z"}}
	b := []Finding{{ID: "9", Severity: Low, Description: "z"}, {ID: "8", Severity: High, Location: "A", Description: "x y"}}
	assert.Equal(t, Fingerprint(a), Fingerprint(b))
	b[0].Severity = Medium
	assert.NotEqual(t, Fingerprint(a), Fingerprint(b))
}

func TestFlowAdvancerGuard(t *testing.T) {
	flowActor := Actor{OrgID: "o", UserID: "owner", Purpose: PurposeFlow}
	base := func() Run {
		r := Run{Runner: Code, State: Submitted, Version: 1, OwnerID: "owner", Location: Cloud, Flow: loopRef("build", 1)}
		r.Flow.Hops = map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings, Auto: true}}
		return r
	}
	tests := []struct {
		name string
		edit func(*Run, *Actor, *CommandType)
		want error
	}{
		{"auto hop", func(*Run, *Actor, *CommandType) {}, nil},
		{"ask hop", func(r *Run, _ *Actor, _ *CommandType) {
			r.Flow.Hops[OutcomeDone] = FlowHop{Node: "review", Runner: Findings}
		}, ErrConflict},
		{"local run", func(r *Run, _ *Actor, _ *CommandType) { r.Location = Local }, ErrConflict},
		{"other user", func(_ *Run, a *Actor, _ *CommandType) { a.UserID = "x" }, ErrForbidden},
		{"apply fixes", func(_ *Run, _ *Actor, c *CommandType) { *c = ApplyFixes }, ErrForbidden},
		{"blocked", func(r *Run, _ *Actor, _ *CommandType) { r.Blocker = BlockerExecutionBusy }, ErrStale},
		{"not finished", func(r *Run, _ *Actor, _ *CommandType) { r.State = Running }, ErrConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, a, c := base(), flowActor, Next
			tt.edit(&r, &a, &c)
			_, err := r.Successor(a, Command{Type: c, ExpectedVersion: 1})
			if tt.want == nil {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, tt.want)
		})
	}
	// Machine callers never get UI controls.
	assert.Empty(t, base().AllowedCommands(flowActor))
}

func TestAutoHop(t *testing.T) {
	r := Run{Runner: Code, State: Submitted, Location: Cloud, Flow: loopRef("build", 1)}
	r.Flow.Hops = map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings, Auto: true}}
	hop, ok := r.AutoHop()
	require.True(t, ok)
	assert.Equal(t, "review", hop.Node)

	stopped := r
	stopped.Flow = loopRef("build", 1)
	stopped.Flow.Hops = map[Outcome]FlowHop{OutcomeDone: {Node: "build", Runner: Code, Auto: true, Back: true}}
	stopped.Flow.Loop.MaxIterations = 1
	_, ok = stopped.AutoHop()
	assert.False(t, ok, "a hop the loop stopped never starts by itself")

	local := r
	local.Location = Local
	_, ok = local.AutoHop()
	assert.False(t, ok, "local runs never auto-chain")

	toStop := r
	toStop.Flow = loopRef("build", 1)
	toStop.Flow.Hops = map[Outcome]FlowHop{OutcomeDone: {Auto: true, Stop: "end"}}
	_, ok = toStop.AutoHop()
	assert.False(t, ok, "only hops into a step start one")
}

func TestFlowSpawnedRunStartsReady(t *testing.T) {
	parent := Run{ID: "p", RootID: "p", Runner: Code, State: Submitted, OwnerID: "owner", Location: Cloud}
	q := Request{Runner: Findings, Location: Cloud, InputDigest: "x", Flow: loopRef("review", 1)}
	r, err := NewRun("r", "o", "p", "i", Actor{UserID: "owner", Purpose: PurposeFlow}, q, &parent)
	require.NoError(t, err)
	assert.Equal(t, Ready, r.State)
	r, err = NewRun("r", "o", "p", "i", Actor{UserID: "owner", Role: "member"}, q, &parent)
	require.NoError(t, err)
	assert.Equal(t, Suggested, r.State)
}

func TestLoopReplacesReassessment(t *testing.T) {
	parent := Run{ID: "p", RootID: "p", Runner: Findings, State: Submitted, OwnerID: "owner", Location: Cloud, Round: 2}
	owner := Actor{UserID: "owner", Role: "member"}
	r, err := NewRun("r", "o", "p", "i", owner, Request{Runner: Code, Location: Cloud, InputDigest: "x"}, &parent)
	require.NoError(t, err)
	assert.Equal(t, BlockerReassessment, r.Blocker, "outside a loop round 3 needs reassessment")
	r, err = NewRun("r", "o", "p", "i", owner, Request{Runner: Code, Location: Cloud, InputDigest: "x", Flow: loopRef("build", 3)}, &parent)
	require.NoError(t, err)
	assert.Equal(t, Blocker(""), r.Blocker)
}

func TestNewRunCopiesRefs(t *testing.T) {
	actor := Actor{UserID: "u", Role: "member"}
	skill := &SkillRef{ID: "s", Slug: "notes", VersionID: "sv", Digest: "d"}
	flow := &FlowRef{VersionID: "v", Node: "build"}
	r, err := NewRun("r", "o", "p", "i", actor, Request{Runner: Code, Location: Cloud, InputDigest: "x", Skill: skill, Flow: flow}, nil)
	require.NoError(t, err)
	skill.Slug, flow.Node = "changed", "changed"
	require.Equal(t, "notes", r.Skill.Slug)
	require.Equal(t, "build", r.Flow.Node)
	require.Equal(t, Identity, r.Prompt, "the skill carries the activity prompt")
}
