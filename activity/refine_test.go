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
	require.Contains(t, r.AllowedCommands(owner), Refine)
	require.Equal(t, "plan", r.SuccessorNode(Refine))

	kind, err := r.Successor(owner, Command{Type: Refine, ExpectedVersion: 2, Reason: "RIDE is a cycling game, not motorsport."})
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
	require.ErrorIs(t, r.guard(other, Refine), ErrForbidden)
	require.NoError(t, r.guard(admin, Refine))
	require.ErrorIs(t, r.guard(Actor{UserID: "owner", Role: "member", Purpose: PurposeFlow}, Refine), ErrForbidden)

	running := submittedPlan()
	running.State = Running
	require.ErrorIs(t, running.guard(owner, Refine), ErrConflict)

	review := submittedPlan()
	review.Runner = Findings
	require.ErrorIs(t, review.guard(owner, Refine), ErrConflict)

	noOutput := submittedPlan()
	noOutput.Outputs = nil
	require.ErrorIs(t, noOutput.guard(owner, Refine), ErrConflict)

	stale := submittedPlan()
	stale.Blocker = BlockerInputsChanged
	require.ErrorIs(t, stale.guard(owner, Refine), ErrStale)
}

func TestNewRunKeepsFeedback(t *testing.T) {
	parent := submittedPlan()
	parent.ID, parent.RootID = "p", "p"
	r, err := NewRun("r2", "org", "project", "item", Actor{UserID: "owner"}, Request{
		Runner: Answer, Location: Cloud, InputDigest: "d", Feedback: "  make it about bikes  ",
	}, &parent)
	require.NoError(t, err)
	require.Equal(t, "make it about bikes", r.Feedback)
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
