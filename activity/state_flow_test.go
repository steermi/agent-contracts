package activity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loopRef(node string, iteration int) *FlowRef {
	exhausted := FlowHop{Stop: "owner", StopName: "Needs a decision", Approval: true}
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
	spawn, err := review.Successor(owner, Command{Type: Next, ExpectedVersion: 1})
	kind := spawn.Runner
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
	spawn, err := r.Successor(owner, Command{Type: ApplyFixes, ExpectedVersion: 1})
	kind := spawn.Runner
	require.NoError(t, err, "a person may still ask for one more round")
	require.Equal(t, Code, kind)
}

func TestOutcomeUsesLoopThreshold(t *testing.T) {
	r := Run{Runner: Findings, State: Submitted, Recommendation: ChangesRequested, Findings: []Finding{{ID: "1", Severity: Low, Description: "nit"}}, Flow: loopRef("review", 1)}
	assert.Equal(t, OutcomeApproved, r.Outcome(), "findings below stop_below are follow-ups")
	r.Findings = append(r.Findings, Finding{ID: "2", Severity: High, Description: "bug"})
	assert.Equal(t, OutcomeChangesRequested, r.Outcome())
	r.Findings[1].DismissalReason = "accepted risk"
	assert.Equal(t, OutcomeChangesRequested, r.Outcome(), "a dismissal never changes the outcome")
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
	policy := LoopPolicy{StopBelow: High}
	same := policy.StallFingerprint(high)
	other := policy.StallFingerprint([]Finding{{ID: "1", Severity: High, Location: "b.go", Description: "bug"}})
	tests := []struct {
		name string
		edit func(*Run)
		want Blocker
	}{
		{"another round", func(*Run) {}, ""},
		{"max iterations", func(r *Run) { r.Flow.Loop.Iteration = 3 }, BlockerRepairLimit},
		{"budget spent", func(r *Run) { r.Cost = 6; r.Flow.Loop.StartCost = 1 }, BlockerBudget},
		{"budget left", func(r *Run) { r.Cost = 5.5; r.Flow.Loop.StartCost = 1 }, ""},
		{"same findings again", func(r *Run) { r.Flow.Loop.Fingerprints = []string{same} }, BlockerLoopStalled},
		{"stall check off", func(r *Run) {
			r.Flow.Loop.Fingerprints = []string{same}
			r.Flow.Loop.StallRounds = 0
		}, ""},
		{"a pinned stall of 1 acts as off", func(r *Run) {
			r.Flow.Loop.Fingerprints = []string{same}
			r.Flow.Loop.StallRounds = 1
		}, ""},
		{"low findings are noise", func(r *Run) {
			r.Flow.Loop.Fingerprints = []string{same}
			r.Findings = append(r.Findings, Finding{ID: "2", Severity: Low, Location: "b.go", Description: "nit"})
		}, BlockerLoopStalled},
		{"reworded on another line is the same place", func(r *Run) {
			r.Flow.Loop.Fingerprints = []string{same}
			r.Findings = []Finding{{ID: "9", Severity: High, Location: "A.go:40", Description: "still broken"}}
		}, BlockerLoopStalled},
		{"exact compares wording", func(r *Run) {
			r.Flow.Loop.StallMatch = StallExact
			r.Flow.Loop.Fingerprints = []string{r.Flow.Loop.StallFingerprint(high)}
			r.Findings = []Finding{{ID: "9", Severity: High, Location: "a.go:1", Description: "still broken"}}
		}, ""},
		{"A, B, A is not a stall", func(r *Run) { r.Flow.Loop.Fingerprints = []string{same, other} }, ""},
		{"a round without blocking findings breaks the run", func(r *Run) { r.Flow.Loop.Fingerprints = []string{same, ""} }, ""},
		{"three rounds: two in a row are not enough", func(r *Run) {
			r.Flow.Loop.StallRounds = 3
			r.Flow.Loop.Fingerprints = []string{same}
		}, ""},
		{"three rounds: older repeat does not count", func(r *Run) {
			r.Flow.Loop.StallRounds, r.Flow.Loop.MaxIterations, r.Flow.Loop.Iteration = 3, 6, 4
			r.Flow.Loop.Fingerprints = []string{same, other, same}
		}, ""},
		{"three rounds in a row", func(r *Run) {
			r.Flow.Loop.StallRounds, r.Flow.Loop.MaxIterations, r.Flow.Loop.Iteration = 3, 6, 4
			r.Flow.Loop.Fingerprints = []string{other, same, same}
		}, BlockerLoopStalled},
		{"a dismissal on the run does not change its hop", func(r *Run) {
			r.Flow.Loop.Fingerprints = []string{same}
			r.Findings = []Finding{{ID: "1", Severity: High, Location: "a.go:1", Description: "bug", DismissalReason: "accepted"}}
		}, BlockerLoopStalled},
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
		r.Flow.Hops = map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings, Auto: true, Location: Cloud}}
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
		{"local run, cloud hop", func(r *Run, _ *Actor, _ *CommandType) { r.Location = Local }, nil},
		{"local hop", func(r *Run, _ *Actor, _ *CommandType) {
			r.Flow.Hops[OutcomeDone] = FlowHop{Node: "review", Runner: Findings, Auto: true, Location: Local}
		}, ErrConflict},
		{"unresolved hop", func(r *Run, _ *Actor, _ *CommandType) {
			r.Flow.Hops[OutcomeDone] = FlowHop{Node: "review", Runner: Findings, Auto: true}
		}, ErrConflict},
		{"paused after each step", func(r *Run, _ *Actor, _ *CommandType) { r.StepMode = StepModeAsk }, ErrConflict},
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
	assert.Empty(t, base().AllowedCommands(flowActor, HostFacts{}))
}

func TestAutoHop(t *testing.T) {
	r := Run{Runner: Code, State: Submitted, Location: Cloud, Flow: loopRef("build", 1)}
	r.Flow.Hops = map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings, Auto: true, Location: Cloud}}
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
	assert.True(t, ok, "a local run continues into a cloud step")

	localHop := r
	localHop.Flow = loopRef("build", 1)
	localHop.Flow.Hops = map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings, Auto: true, Location: Local}}
	_, ok = localHop.AutoHop()
	assert.False(t, ok, "a developer starts a local step")

	paused := r
	paused.StepMode = StepModeAsk
	_, ok = paused.AutoHop()
	assert.False(t, ok, "pause after each step")

	toStop := r
	toStop.Flow = loopRef("build", 1)
	toStop.Flow.Hops = map[Outcome]FlowHop{OutcomeDone: {Auto: true, Stop: "end"}}
	_, ok = toStop.AutoHop()
	assert.False(t, ok, "only hops into a step start one")
}

func TestFlowSpawnedRunStartsReady(t *testing.T) {
	// A local Build whose auto hop is pinned to the cloud.
	parent := Run{ID: "p", RootID: "p", Runner: Code, State: Submitted, OwnerID: "owner", Location: Local, Flow: loopRef("build", 1)}
	parent.Flow.Hops = map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings, Auto: true, Location: Cloud}}
	flowActor := Actor{UserID: "owner", Purpose: PurposeFlow}
	q := Request{Runner: Findings, Location: Cloud, InputDigest: "x", Flow: loopRef("review", 1)}
	r, err := NewRun("r", "o", "p", "i", flowActor, q, &parent)
	require.NoError(t, err)
	assert.Equal(t, Ready, r.State)
	r, err = NewRun("r", "o", "p", "i", Actor{UserID: "owner", Role: "member"}, q, &parent)
	require.NoError(t, err)
	assert.Equal(t, Suggested, r.State)

	// Successor decides the automatic hop once (flowGuard: AutoHop) and
	// places it where the hop is pinned; NewRun takes that placement.
	placed, err := parent.Successor(flowActor, Command{Type: Next})
	require.NoError(t, err)
	assert.Equal(t, SuccessorRun{Runner: Findings, Location: Cloud}, placed, "the flow starts the hop where it is pinned")
	ask := parent
	ask.Flow = loopRef("build", 1)
	ask.Flow.Hops = map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings, Location: Cloud}}
	_, err = ask.Successor(flowActor, Command{Type: Next})
	require.ErrorIs(t, err, ErrConflict, "the flow starts only automatic hops")
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

func TestStallFingerprintKeepsOnlyOpenBlockingFindings(t *testing.T) {
	p := LoopPolicy{StopBelow: High}
	high := Finding{ID: "1", Severity: High, Location: "a.go:1", Description: "bug"}
	low := Finding{ID: "2", Severity: Low, Location: "b.go", Description: "nit"}
	dismissed := Finding{ID: "3", Severity: Critical, Location: "c.go", Description: "leak", DismissalReason: "accepted risk"}
	assert.Equal(t, p.StallFingerprint([]Finding{high}), p.StallFingerprint([]Finding{low, high, dismissed}))
	assert.Empty(t, p.StallFingerprint([]Finding{low, dismissed}), "nothing blocking left open")
	assert.Empty(t, p.StallFingerprint(nil))
	assert.NotEqual(t, p.StallFingerprint([]Finding{high}), LoopPolicy{StopBelow: High, StallMatch: StallExact}.StallFingerprint([]Finding{high}))
	assert.Equal(t, FingerprintBy([]Finding{high}, StallLocation), FingerprintBy([]Finding{high}, ""), "absent = location")
	assert.Equal(t, Fingerprint([]Finding{high}), FingerprintBy([]Finding{high}, StallExact))
	assert.Empty(t, LoopPolicy{}.StallFingerprint([]Finding{low}), "without a threshold findings block from high")
	assert.Equal(t, p.StallFingerprint([]Finding{high}), LoopPolicy{}.StallFingerprint([]Finding{low, high}))
}

func TestInconclusiveReview(t *testing.T) {
	owner := Actor{UserID: "o", Role: "member"}
	// Inside a loop: the loop's exhausted exit, with its own reason, whatever
	// edges the step has.
	in := Run{Runner: Findings, State: Submitted, Version: 1, OwnerID: "o", Recommendation: Inconclusive, Flow: loopRef("review", 1)}
	in.Flow.Hops = map[Outcome]FlowHop{
		OutcomeChangesRequested: {Node: "build", Runner: Code, Back: true},
		OutcomeApproved:         {Stop: "end"},
		OutcomeFailed:           {Stop: "broken", Approval: true},
	}
	assert.Equal(t, OutcomeExhausted, in.Outcome())
	hop, stop, ok := in.FlowNext()
	require.True(t, ok)
	assert.Equal(t, BlockerReviewInconclusive, stop)
	assert.Equal(t, in.Flow.Loop.Exhausted, hop)
	_, auto := in.AutoHop()
	assert.False(t, auto)
	_, err := in.Successor(owner, Command{Type: Next, ExpectedVersion: 1})
	assert.ErrorIs(t, err, ErrConflict, "nothing follows by itself")
	in.Flow.Hops = nil
	_, stop, ok = in.FlowNext()
	require.True(t, ok, "the exit needs no edge of the step")
	assert.Equal(t, BlockerReviewInconclusive, stop)
	in.State = Completed
	assert.Equal(t, OutcomeExhausted, in.Outcome())

	// A person's request for changes on the current evidence wins inside a
	// loop too: the changes-requested hop, no inconclusive stop.
	decided := Run{Runner: Findings, State: Submitted, Version: 1, OwnerID: "o", Recommendation: Inconclusive, Flow: loopRef("review", 1)}
	decided.Flow.Hops = map[Outcome]FlowHop{OutcomeChangesRequested: {Node: "build", Runner: Code, Back: true}}
	decided.Decisions = []Decision{{UserID: "rev", Kind: RequestChanges, EvidenceDigest: decided.EvidenceDigest()}}
	assert.Equal(t, OutcomeChangesRequested, decided.Outcome())
	hop, stop, ok = decided.FlowNext()
	require.True(t, ok)
	assert.Empty(t, stop)
	assert.Equal(t, "build", hop.Node)

	// Outside a loop: failed, following the failed edge when the flow has one.
	out := Run{Runner: Findings, State: Submitted, Version: 1, OwnerID: "o", Recommendation: Inconclusive}
	assert.Equal(t, OutcomeFailed, out.Outcome())
	out.Flow = &FlowRef{VersionID: "v", Node: "check", Hops: map[Outcome]FlowHop{OutcomeApproved: {Stop: "end"}}}
	_, _, ok = out.FlowNext()
	assert.False(t, ok, "no failed edge: the flow stops at the run")
	out.Flow.Hops[OutcomeFailed] = FlowHop{Stop: "person", Approval: true, Note: "look at it"}
	hop, stop, ok = out.FlowNext()
	require.True(t, ok)
	assert.Empty(t, stop)
	assert.Equal(t, "look at it", hop.Note)

	// A person's request for changes still wins.
	out.Decisions = []Decision{{Kind: RequestChanges, EvidenceDigest: out.EvidenceDigest()}}
	assert.Equal(t, OutcomeChangesRequested, out.Outcome())

	approved := Run{Runner: Findings, State: Submitted, Recommendation: Approve}
	assert.Equal(t, OutcomeApproved, approved.Outcome())
}

func TestApproveContradictingABlockingFindingIsRefused(t *testing.T) {
	owner := Actor{UserID: "o", Role: "member"}
	submit := func(flow *FlowRef, rec Recommendation, severity Severity) error {
		r := Run{Runner: Findings, State: Running, Version: 1, OwnerID: "o", Flow: flow}
		return r.Apply(owner, Command{Type: SubmitFindings, ExpectedVersion: 1, Summary: "s", Recommendation: rec,
			Findings: []Finding{{ID: "1", Severity: severity, Description: "d"}}})
	}
	medium := loopRef("review", 1)
	medium.Loop.StopBelow = Medium
	open := loopRef("review", 1)
	open.Loop.StopBelow = ""
	tests := []struct {
		name     string
		flow     *FlowRef
		rec      Recommendation
		severity Severity
		refused  bool
	}{
		{"loop, at the threshold", loopRef("review", 1), Approve, High, true},
		{"loop, above the threshold", loopRef("review", 1), Approve, Critical, true},
		{"loop, below the threshold", loopRef("review", 1), Approve, Medium, false},
		{"loop with a lower threshold", medium, Approve, Medium, true},
		{"loop without a threshold blocks from high", open, Approve, Medium, false},
		{"loop without a threshold, high", open, Approve, High, true},
		{"outside a loop, high", nil, Approve, High, true},
		{"outside a loop, critical", &FlowRef{VersionID: "v", Node: "check"}, Approve, Critical, true},
		{"outside a loop, medium", nil, Approve, Medium, false},
		{"changes requested may carry anything", loopRef("review", 1), ChangesRequested, Critical, false},
		{"inconclusive may carry anything", nil, Inconclusive, Critical, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := submit(tt.flow, tt.rec, tt.severity)
			if !tt.refused {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, ErrValidation)
			assert.Contains(t, err.Error(), "approve contradicts a finding rated")
		})
	}
}

// approvalRun is a submitted answer run whose hop ended at an approval node
// with both decisions: approved moves the ticket on, changes requested goes
// back to the step.
func approvalRun(approver Approver) Run {
	return Run{ID: "p1", Runner: Answer, State: Submitted, Version: 1, OwnerID: "owner", Location: Cloud,
		Flow: &FlowRef{VersionID: "v", Node: "plan", Hops: map[Outcome]FlowHop{OutcomeDone: {
			Stop: "signoff", StopName: "Product sign-off", Approval: true, Note: "Check the criteria.", Approver: approver,
			Decide: map[Outcome]FlowHop{
				OutcomeApproved:         {Statuses: []StatusTarget{{Category: "done"}}, Stop: "end"},
				OutcomeChangesRequested: {Node: "plan", Name: "Plan", Runner: Answer, Location: Cloud},
			}}}}}
}

func TestStepApprovalGuardPerApprover(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	admin := Actor{UserID: "boss", Role: "admin"}
	lead := Actor{UserID: "lead", Role: "member"}
	dev := Actor{UserID: "dev", Role: "member"}
	member := Actor{UserID: "other", Role: "member"}
	viewer := Actor{UserID: "viewer", Role: "viewer"}
	machine := Actor{UserID: "owner", Role: "member", Purpose: PurposeFlow}
	manages := HostFacts{TeamManager: true}
	writes := HostFacts{ProjectWriter: true}
	listed := func(r *Run) { r.Policy.ReviewerIDs = []string{"other", "owner"} }
	tests := []struct {
		name     string
		approver Approver
		edit     func(*Run)
		actor    Actor
		facts    HostFacts
		want     error
	}{
		{"owner: the owner", "", nil, owner, HostFacts{}, nil},
		{"owner spelled out", ApproverOwner, nil, owner, HostFacts{}, nil},
		{"owner: a team manager is not the owner", ApproverOwner, nil, lead, manages, ErrForbidden},
		{"owner: an admin always may", ApproverOwner, nil, admin, HostFacts{}, nil},
		{"team managers: a manager", ApproverTeamManagers, nil, lead, manages, nil},
		{"team managers: never without the host's fact", ApproverTeamManagers, nil, lead, HostFacts{}, ErrForbidden},
		{"team managers: the owner alone is not enough", ApproverTeamManagers, nil, owner, HostFacts{}, ErrForbidden},
		{"team managers: an admin", ApproverTeamManagers, nil, admin, HostFacts{}, nil},
		{"reviewers: a project writer", ApproverReviewers, nil, dev, writes, nil},
		{"reviewers: not the run's owner", ApproverReviewers, nil, owner, writes, ErrForbidden},
		{"reviewers: not without write access", ApproverReviewers, nil, member, HostFacts{}, ErrForbidden},
		{"reviewers: listed", ApproverReviewers, listed, member, HostFacts{}, nil},
		{"reviewers: the listed owner", ApproverReviewers, listed, owner, HostFacts{}, nil},
		{"reviewers: a writer who is not listed", ApproverReviewers, listed, dev, writes, ErrForbidden},
		{"viewers never", ApproverTeamManagers, nil, viewer, HostFacts{TeamManager: true, ProjectWriter: true}, ErrForbidden},
		{"machines never", ApproverOwner, nil, machine, HostFacts{}, ErrForbidden},
		{"a superseded run waits for nothing", "", nil, owner, HostFacts{Fence: DecisionFence{Superseded: true}}, ErrConflict},
		{"a moved subject waits for nothing", "", nil, owner, HostFacts{Fence: DecisionFence{Moved: true}}, ErrConflict},
		{"an unknown fence refuses", "", nil, owner, HostFacts{Fence: DecisionFence{Unknown: true}}, ErrSourceUnavailable},
		{"a non-approver is forbidden whatever the fence", ApproverTeamManagers, nil, lead, HostFacts{Fence: DecisionFence{Moved: true}}, ErrForbidden},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := approvalRun(tt.approver)
			if tt.edit != nil {
				tt.edit(&r)
			}
			err := r.Apply(tt.actor, Command{Type: ApproveStep, ExpectedVersion: 1, Facts: tt.facts})
			allowed := r.AllowedCommands(tt.actor, tt.facts)
			if tt.want == nil {
				require.NoError(t, err)
			} else {
				require.ErrorIs(t, err, tt.want)
				assert.NotContains(t, allowed, ApproveStep)
				assert.NotContains(t, allowed, RequestStepChanges)
			}
		})
	}
	r := approvalRun("")
	assert.Contains(t, r.AllowedCommands(owner, HostFacts{}), ApproveStep)
	assert.Contains(t, r.AllowedCommands(owner, HostFacts{}), RequestStepChanges)
	assert.True(t, ApproveStep.Public())
	assert.True(t, RequestStepChanges.Public())
	assert.False(t, ApproveStep.spawns())
}

// A run waits for a decision only in the states the decision guard admits.
func TestApprovalWaitingFollowsTheGuard(t *testing.T) {
	r := approvalRun("")
	require.True(t, waitsForApproval(r))
	completed := r
	completed.State = Completed
	assert.True(t, waitsForApproval(completed))
	for _, s := range []State{Running, Cancelled} {
		other := r
		other.State = s
		assert.False(t, waitsForApproval(other), s)
	}
	// A fenced run waits for nothing: nobody may decide on it.
	blocked := r
	blocked.Blocker = BlockerSourceUnavailable
	assert.False(t, waitsForApproval(blocked))
	_, ok := blocked.Approval(DecisionFence{})
	assert.False(t, ok)
	assert.NotContains(t, blocked.AllowedCommands(Actor{UserID: "owner", Role: "member"}, HostFacts{}), ApproveStep)
}

// A loop without stop_below blocks from high, like every other place that
// asks whether a finding blocks (one rule, LoopPolicy.Blocking).
func TestLoopWithoutThresholdBlocksFromHigh(t *testing.T) {
	flow := loopRef("review", 1)
	flow.Loop.StopBelow = ""
	medium := Finding{ID: "1", Severity: Medium, Location: "a.go", Description: "nit"}
	high := Finding{ID: "2", Severity: High, Location: "b.go", Description: "bug"}
	r := Run{Runner: Findings, State: Submitted, Recommendation: ChangesRequested, Findings: []Finding{medium}, Flow: flow}
	assert.Equal(t, OutcomeApproved, r.Outcome(), "medium findings are follow-ups")
	assert.Empty(t, LoopStepOf(r).Red)
	assert.Empty(t, LoopStepOf(r).Fingerprint)
	assert.Equal(t, High, r.BlockingSeverity())
	r.Findings = append(r.Findings, high)
	assert.Equal(t, OutcomeChangesRequested, r.Outcome())
	require.Len(t, LoopStepOf(r).Red, 1)
	assert.Equal(t, "2", LoopStepOf(r).Red[0].Ref)
	assert.Equal(t, flow.Loop.StallFingerprint([]Finding{high}), LoopStepOf(r).Fingerprint)
}

// MapHops visits every pinned hop: each outcome's, the loop's exit and the
// decision hops nested under either, at any depth.
func TestMapHopsVisitsEveryPinnedHop(t *testing.T) {
	ref := FlowRef{VersionID: "v", Node: "review",
		Hops: map[Outcome]FlowHop{
			OutcomeChangesRequested: {Node: "build"},
			OutcomeApproved: {Stop: "a1", Approval: true, Decide: map[Outcome]FlowHop{
				OutcomeApproved: {Stop: "a2", Approval: true, Decide: map[Outcome]FlowHop{OutcomeChangesRequested: {Node: "deep"}}},
			}},
		},
		Loop: &LoopState{Key: "quality", Exhausted: FlowHop{Stop: "owner", Approval: true, Decide: map[Outcome]FlowHop{OutcomeChangesRequested: {Node: "again"}}}}}
	var seen []string
	out := ref.MapHops(func(h FlowHop) FlowHop {
		seen = append(seen, h.Node+h.Stop)
		h.Location = Cloud
		return h
	})
	assert.ElementsMatch(t, []string{"build", "a1", "a2", "deep", "owner", "again"}, seen)
	assert.Equal(t, Cloud, out.Hops[OutcomeApproved].Decide[OutcomeApproved].Decide[OutcomeChangesRequested].Location)
	assert.Equal(t, Cloud, out.Loop.Exhausted.Decide[OutcomeChangesRequested].Location)
	assert.Equal(t, Cloud, out.Hops[OutcomeChangesRequested].Location)
	// The original ref is untouched.
	assert.Empty(t, ref.Hops[OutcomeApproved].Decide[OutcomeApproved].Decide[OutcomeChangesRequested].Location)
	assert.Empty(t, ref.Loop.Exhausted.Location)
	assert.Empty(t, ref.Loop.Exhausted.Decide[OutcomeChangesRequested].Location)
	assert.Empty(t, FlowRef{}.MapHops(func(h FlowHop) FlowHop { t.Fatal("no hops"); return h }).Hops)
}

func TestStepApprovalDecision(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	flowActor := Actor{UserID: "owner", Role: "member", Purpose: PurposeFlow}

	r := approvalRun("")
	approval, ok := r.Approval(DecisionFence{})
	require.True(t, ok)
	assert.Equal(t, ApprovalStop{Node: "signoff", Name: "Product sign-off", Note: "Check the criteria.", Approver: ApproverOwner,
		Outcomes: []Outcome{OutcomeApproved, OutcomeChangesRequested}, Waiting: true}, approval)
	fenced, ok := r.Approval(DecisionFence{Superseded: true})
	require.True(t, ok, "a fenced run still shows its approval node")
	assert.False(t, fenced.Waiting)
	assert.True(t, waitsForApproval(r))
	assert.False(t, hopDecided(r))
	assert.Equal(t, Runner(""), r.NextRunner(), "nothing starts before the decision")
	_, auto := r.AutoHop()
	assert.False(t, auto)
	digest := r.EvidenceDigest()

	// Changes requested: a reason is required; it becomes the feedback of
	// the step the decision starts, which starts as an explicit human start.
	require.ErrorIs(t, r.Apply(owner, Command{Type: RequestStepChanges, ExpectedVersion: 1}), ErrValidation)
	require.ErrorIs(t, r.Apply(owner, Command{Type: RequestStepChanges, ExpectedVersion: 1, Reason: strings.Repeat("x", MaxFeedbackRunes+1)}), ErrValidation)
	require.NoError(t, r.Apply(owner, Command{Type: RequestStepChanges, ExpectedVersion: 1, Reason: "  Cover the empty state.  "}))
	assert.Equal(t, int64(2), r.Version)
	assert.Equal(t, digest, r.EvidenceDigest(), "a decision is not evidence of the work")
	require.Len(t, r.StepDecisions, 1)
	d := r.StepDecisions[0]
	assert.Equal(t, "signoff", d.Node)
	assert.Equal(t, OutcomeChangesRequested, d.Outcome)
	assert.Equal(t, "owner", d.UserID)
	assert.Equal(t, "Cover the empty state.", d.Reason)
	assert.False(t, d.At.IsZero())
	assert.False(t, waitsForApproval(r))
	assert.True(t, hopDecided(r))
	approval, _ = r.Approval(DecisionFence{})
	require.NotNil(t, approval.Decision)
	assert.Equal(t, OutcomeChangesRequested, approval.Decision.Outcome)
	hop, stop, ok := r.FlowNext()
	require.True(t, ok)
	assert.Empty(t, stop)
	assert.Equal(t, "plan", hop.Node)
	hop, auto = r.AutoHop()
	assert.True(t, auto, "the decision starts the step: no auto edge needed")
	assert.False(t, hop.Auto)
	next, err := r.Successor(flowActor, Command{Type: Next, ExpectedVersion: 2})
	require.NoError(t, err)
	assert.Equal(t, SuccessorRun{Runner: Answer, Location: Cloud, Feedback: &Feedback{Text: "Cover the empty state.", Source: FeedbackStepDecision, By: "owner"}}, next,
		"the feedback names its source and who asked, whoever starts the step")
	stages := r.FlowStages()
	require.Len(t, stages, 2)
	assert.Equal(t, "signoff", stages[0].Hop.Stop)
	assert.Nil(t, stages[0].Decision)
	assert.Equal(t, "plan", stages[1].Hop.Node)
	assert.Equal(t, OutcomeChangesRequested, stages[1].Decision.Outcome)
	assert.Equal(t, "plan", r.SuccessorNode(Next))

	// One decision per stop.
	require.ErrorIs(t, r.Apply(owner, Command{Type: ApproveStep, ExpectedVersion: 2}), ErrConflict)
	require.ErrorIs(t, r.Apply(owner, Command{Type: RequestStepChanges, ExpectedVersion: 2, Reason: "again"}), ErrConflict)
	assert.NotContains(t, r.AllowedCommands(owner, HostFacts{}), ApproveStep)

	// Pause after each step and a hop pinned local still wait for a person.
	paused := r
	paused.StepMode = StepModeAsk
	_, auto = paused.AutoHop()
	assert.False(t, auto)
	local := approvalRun("")
	back := local.Flow.Hops[OutcomeDone].Decide[OutcomeChangesRequested]
	back.Location = Local
	local.Flow.Hops[OutcomeDone].Decide[OutcomeChangesRequested] = back
	require.NoError(t, local.Apply(owner, Command{Type: RequestStepChanges, ExpectedVersion: 1, Reason: "r"}))
	_, auto = local.AutoHop()
	assert.False(t, auto)
	assert.Equal(t, Answer, local.NextRunner(), "the owner starts it by hand")

	// Approved: the hop moves the ticket on and starts nothing.
	a := approvalRun("")
	require.NoError(t, a.Apply(owner, Command{Type: ApproveStep, ExpectedVersion: 1, Reason: "ok"}))
	hop, _, ok = a.FlowNext()
	require.True(t, ok)
	assert.Equal(t, []StatusTarget{{Category: "done"}}, hop.Statuses)
	assert.Equal(t, "end", hop.Stop)
	assert.Equal(t, Runner(""), a.NextRunner())
	assert.Nil(t, a.successorFeedback(owner, Command{Type: Next}), "an approval carries no change request")
}

// Once a run's flow reaches an approval node that decides, its decision is
// the only way on: apply_fixes is refused for everyone while it waits and
// after the decision, so nobody reworks past the approver without a recorded
// decision. A plain hand-off keeps "one more round".
func TestApplyFixesNeverPassesADecidingApproval(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	admin := Actor{UserID: "boss", Role: "admin"}
	lead := Actor{UserID: "lead", Role: "member"}
	waiting := func(approver Approver) Run {
		r := Run{Runner: Findings, State: Submitted, Version: 1, OwnerID: "owner", Recommendation: ChangesRequested,
			Findings: []Finding{{ID: "1", Severity: High, Description: "bug"}}, Flow: loopRef("review", 3)}
		r.Flow.Hops = map[Outcome]FlowHop{OutcomeChangesRequested: {Node: "build", Runner: Code, Back: true}}
		r.Flow.Loop.Exhausted = FlowHop{Stop: "signoff", Approval: true, Approver: approver,
			Decide: map[Outcome]FlowHop{OutcomeChangesRequested: {Node: "build", Runner: Code}}}
		return r
	}
	fix := Command{Type: ApplyFixes, ExpectedVersion: 1}
	for _, approver := range []Approver{"", ApproverTeamManagers} {
		r := waiting(approver)
		require.True(t, waitsForApproval(r))
		for _, a := range []Actor{owner, admin} {
			_, err := r.Successor(a, fix)
			require.ErrorIs(t, err, ErrConflict, "%s/%s", approver, a.UserID)
			assert.NotContains(t, r.AllowedCommands(a, HostFacts{}), ApplyFixes)
		}
	}

	// After the decision Next follows the decided hop; apply_fixes is not
	// offered beside it.
	decided := waiting(ApproverTeamManagers)
	require.NoError(t, decided.Apply(lead, Command{Type: RequestStepChanges, ExpectedVersion: 1, Reason: "fix the edge case", Facts: HostFacts{TeamManager: true}}))
	allowed := decided.AllowedCommands(owner, HostFacts{})
	assert.Contains(t, allowed, Next)
	assert.NotContains(t, allowed, ApplyFixes)
	_, err := decided.Successor(owner, Command{Type: ApplyFixes, ExpectedVersion: 2})
	require.ErrorIs(t, err, ErrConflict)
	next, err := decided.Successor(owner, Command{Type: Next, ExpectedVersion: 2})
	require.NoError(t, err)
	require.NotNil(t, next.Feedback)
	assert.Equal(t, FeedbackStepDecision, next.Feedback.Source)

	// A plain hand-off keeps "one more round".
	plain := waiting("")
	plain.Flow.Loop.Exhausted = FlowHop{Stop: "owner", Approval: true}
	_, err = plain.Successor(owner, fix)
	require.NoError(t, err)
	assert.Contains(t, plain.AllowedCommands(owner, HostFacts{}), ApplyFixes)
}

func TestRefineFeedbackSource(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	r := Run{Runner: Answer, State: Submitted, Version: 1, OwnerID: "owner", Location: Cloud, Outputs: []OutputRef{{Kind: RefDocs, ResourceID: "d", Revision: "1"}}}
	next, err := r.Successor(owner, Command{Type: Refine, ExpectedVersion: 1, Reason: " Shorter. "})
	require.NoError(t, err)
	assert.Equal(t, &Feedback{Text: "Shorter.", Source: FeedbackRefine, By: "owner"}, next.Feedback)

	q := Request{Runner: Answer, Location: Cloud, InputDigest: "d", Feedback: next.Feedback}
	child, err := NewRun("c", "org", "p", "i", owner, q, nil)
	require.NoError(t, err)
	assert.Equal(t, &Feedback{Text: "Shorter.", Source: FeedbackRefine, By: "owner"}, child.Feedback, "refine is stored as refine")
	q.Feedback = &Feedback{Text: " Again. ", Source: FeedbackStepDecision, By: "lead"}
	child, err = NewRun("c", "org", "p", "i", owner, q, nil)
	require.NoError(t, err)
	assert.Equal(t, &Feedback{Text: "Again.", Source: FeedbackStepDecision, By: "lead"}, child.Feedback)
	for _, bad := range []FeedbackSource{"", "rumour"} {
		q.Feedback = &Feedback{Text: "x", Source: bad}
		_, err = NewRun("c", "org", "p", "i", owner, q, nil)
		require.ErrorIs(t, err, ErrValidation, bad)
	}
	q.Feedback = &Feedback{Text: "  ", Source: FeedbackRefine, By: "lead"}
	child, err = NewRun("c", "org", "p", "i", owner, q, nil)
	require.NoError(t, err)
	assert.Nil(t, child.Feedback, "no text, no feedback")
}

// waitsForApproval and hopDecided read Approval and FlowStages the way
// hosts do.
func waitsForApproval(r Run) bool {
	stop, ok := r.Approval(DecisionFence{})
	return ok && stop.Waiting
}

func hopDecided(r Run) bool {
	stages := r.FlowStages()
	return len(stages) > 0 && stages[len(stages)-1].Decision != nil
}

func TestStepApprovalRefusals(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	tests := []struct {
		name string
		edit func(*Run)
		cmd  CommandType
		want error
	}{
		{"no edge for the decision", func(r *Run) {
			hop := r.Flow.Hops[OutcomeDone]
			delete(hop.Decide, OutcomeChangesRequested)
		}, RequestStepChanges, ErrConflict},
		{"a plain hand-off decides nothing", func(r *Run) {
			hop := r.Flow.Hops[OutcomeDone]
			hop.Decide = nil
			r.Flow.Hops[OutcomeDone] = hop
		}, ApproveStep, ErrConflict},
		{"not at an approval", func(r *Run) { r.Flow.Hops[OutcomeDone] = FlowHop{Stop: "end"} }, ApproveStep, ErrConflict},
		{"still running", func(r *Run) { r.State = Running }, ApproveStep, ErrConflict},
		{"off a flow", func(r *Run) { r.Flow = nil }, ApproveStep, ErrConflict},
		{"a fenced run", func(r *Run) { r.Blocker = BlockerSourceUnavailable }, ApproveStep, ErrStale},
		{"a stale version", func(r *Run) { r.Version = 5 }, ApproveStep, ErrStale},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := approvalRun("")
			tt.edit(&r)
			require.ErrorIs(t, r.Apply(owner, Command{Type: tt.cmd, ExpectedVersion: 1, Reason: "r"}), tt.want)
			assert.Empty(t, r.StepDecisions)
		})
	}

	// A decision made on other evidence no longer applies: the run waits
	// again.
	r := approvalRun("")
	require.NoError(t, r.Apply(owner, Command{Type: ApproveStep, ExpectedVersion: 1}))
	r.Outputs = []OutputRef{{Kind: RefDocs, ResourceID: "d", Revision: "2"}}
	assert.True(t, waitsForApproval(r))
	assert.False(t, hopDecided(r))

	// Two approvals in a row are decided one after the other.
	chain := approvalRun("")
	hop := chain.Flow.Hops[OutcomeDone]
	hop.Decide[OutcomeApproved] = FlowHop{Stop: "legal", StopName: "Legal", Approval: true, Decide: map[Outcome]FlowHop{OutcomeApproved: {Stop: "end"}}}
	require.NoError(t, chain.Apply(owner, Command{Type: ApproveStep, ExpectedVersion: 1}))
	second, ok := chain.Approval(DecisionFence{})
	require.True(t, ok)
	assert.Equal(t, "legal", second.Node)
	assert.Nil(t, second.Decision)
	require.NoError(t, chain.Apply(owner, Command{Type: ApproveStep, ExpectedVersion: 2}))
	assert.False(t, waitsForApproval(chain))
	last, _, _ := chain.FlowNext()
	assert.Equal(t, "end", last.Stop)
}
