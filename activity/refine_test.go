package activity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func submittedPlan() Run {
	return Run{Runner: Answer, State: Submitted, Version: 2, InputDigest: "i", OwnerID: "owner",
		Outputs: []OutputRef{{Kind: RefDocs, ResourceID: "page", Revision: "1", Hash: strings.Repeat("a", 64)}},
		Flow:    &FlowRef{VersionID: "v", Node: "plan"}}
}

func TestRefineSpawnsSameStepWithFeedback(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	r := submittedPlan()
	require.Contains(t, r.AllowedCommands(owner, HostFacts{}), Refine)
	require.Equal(t, "plan", r.SuccessorNode(Refine))

	spawn, err := r.Successor(owner, Command{Type: Refine, ExpectedVersion: 2, Reason: "RIDE is a cycling game, not motorsport."})
	kind := spawn.Runner
	require.NoError(t, err)
	require.Equal(t, Answer, kind)

	// Feedback is required and bounded.
	_, err = r.Successor(owner, Command{Type: Refine, ExpectedVersion: 2, Reason: "  "})
	require.ErrorIs(t, err, ErrValidation)
	_, err = r.Successor(owner, Command{Type: Refine, ExpectedVersion: 2, Reason: strings.Repeat("a", MaxFeedbackRunes+1)})
	require.ErrorIs(t, err, ErrValidation)
	// Spawning commands never apply to the run itself.
	require.ErrorIs(t, r.Apply(owner, Command{Type: Refine, ExpectedVersion: 2, Reason: "x"}), ErrValidation)
}

func TestRefineGuard(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	other := Actor{UserID: "other", Role: "member"}
	admin := Actor{UserID: "admin", Role: "admin"}
	r := submittedPlan()
	require.ErrorIs(t, r.stateGuard(other, Refine, HostFacts{}), ErrForbidden)
	require.NoError(t, r.stateGuard(admin, Refine, HostFacts{}))
	require.ErrorIs(t, r.stateGuard(Actor{UserID: "owner", Role: "member", Purpose: PurposeFlow}, Refine, HostFacts{}), ErrForbidden)

	running := submittedPlan()
	running.State = Running
	require.ErrorIs(t, running.stateGuard(owner, Refine, HostFacts{}), ErrConflict)

	review := submittedPlan()
	review.Runner = Findings
	require.ErrorIs(t, review.stateGuard(owner, Refine, HostFacts{}), ErrConflict)

	noOutput := submittedPlan()
	noOutput.Outputs = nil
	require.ErrorIs(t, noOutput.stateGuard(owner, Refine, HostFacts{}), ErrConflict)

	stale := submittedPlan()
	stale.Blocker = BlockerInputsChanged
	require.ErrorIs(t, stale.stateGuard(owner, Refine, HostFacts{}), ErrStale)
}

func TestNewRunKeepsFeedback(t *testing.T) {
	parent := submittedPlan()
	parent.ID, parent.RootID = "p", "p"
	r, err := NewRun("r2", "org", "project", "item", Actor{UserID: "owner"}, Request{
		Runner: Answer, Location: Cloud, InputDigest: "d", Feedback: &Feedback{Text: "  make it about bikes  ", Source: FeedbackRefine},
	}, &parent)
	require.NoError(t, err)
	require.Equal(t, "make it about bikes", r.Feedback.Text)
	require.Equal(t, "p", r.PredecessorID)
}

func TestSubmitAssumptions(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member", Purpose: PurposeExecutor}
	running := func() Run {
		return Run{Runner: Answer, State: Running, Version: 1, InputDigest: "i", OwnerID: "owner"}
	}
	out := []OutputRef{{Kind: RefDocs, ResourceID: "page", Revision: "1", Hash: strings.Repeat("a", 64)}}

	r := running()
	require.NoError(t, r.Apply(owner, Command{Type: Submit, ExpectedVersion: 1, InputDigest: "i", Outputs: out,
		Assumptions: []string{" RIDE is a racing game ", "", "No repository was readable"}}))
	require.Equal(t, []string{"RIDE is a racing game", "No repository was readable"}, r.Assumptions)

	tooMany := make([]string, MaxAssumptions+1)
	for i := range tooMany {
		tooMany[i] = "a"
	}
	r = running()
	require.ErrorIs(t, r.Apply(owner, Command{Type: Submit, ExpectedVersion: 1, InputDigest: "i", Outputs: out, Assumptions: tooMany}), ErrValidation)
	r = running()
	require.ErrorIs(t, r.Apply(owner, Command{Type: Submit, ExpectedVersion: 1, InputDigest: "i", Outputs: out, Assumptions: []string{strings.Repeat("a", MaxAssumptionRunes+1)}}), ErrValidation)
}
