package activity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func submittedFindingsRun() Run {
	return Run{
		Runner: Findings, State: Submitted, OwnerID: "owner", Version: 3, InputDigest: "in",
		Recommendation: ChangesRequested,
		Findings: []Finding{
			{ID: "f1", Severity: High, Description: "Contradicts the runbook"},
			{ID: "f2", Severity: Low, Description: "Stale link"},
		},
	}
}

func TestDismissFindingSetsTheReason(t *testing.T) {
	r := submittedFindingsRun()
	digest := r.EvidenceDigest()
	owner := Actor{UserID: "owner", Role: "member"}
	require.NoError(t, r.Apply(owner, Command{Type: DismissFinding, ExpectedVersion: 3, FindingID: "f1", Reason: "  Applied  "}))
	assert.Equal(t, "Applied", r.Findings[0].DismissalReason)
	assert.Empty(t, r.Findings[1].DismissalReason)
	assert.Equal(t, Submitted, r.State, "a dismissal never moves the run")
	assert.EqualValues(t, 4, r.Version)
	assert.Equal(t, digest, r.EvidenceDigest(), "dismissals are not evidence")
	assert.Equal(t, ChangesRequested, r.Recommendation)
}

func TestDismissFindingRoles(t *testing.T) {
	cmd := Command{Type: DismissFinding, ExpectedVersion: 3, FindingID: "f1", Reason: "Dismissed"}
	for _, tc := range []struct {
		name  string
		actor Actor
		err   error
	}{
		{"owner", Actor{UserID: "owner", Role: "member"}, nil},
		{"admin", Actor{UserID: "admin", Role: "admin"}, nil},
		{"org owner", Actor{UserID: "boss", Role: "owner"}, nil},
		{"other member", Actor{UserID: "member", Role: "member"}, ErrForbidden},
		{"owner as viewer", Actor{UserID: "owner", Role: "viewer"}, ErrForbidden},
		{"machine", Actor{UserID: "owner", Role: "member", Purpose: PurposeExecutor}, ErrForbidden},
		{"flow", Actor{UserID: "owner", Role: "member", Purpose: PurposeFlow}, ErrForbidden},
	} {
		r := submittedFindingsRun()
		err := r.Apply(tc.actor, cmd)
		if tc.err == nil {
			assert.NoError(t, err, tc.name)
			assert.Contains(t, submittedFindingsRun().AllowedCommands(tc.actor, HostFacts{}), DismissFinding, tc.name)
		} else {
			assert.ErrorIs(t, err, tc.err, tc.name)
			assert.NotContains(t, submittedFindingsRun().AllowedCommands(tc.actor, HostFacts{}), DismissFinding, tc.name)
		}
	}
}

func TestDismissFindingStates(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	cmd := Command{Type: DismissFinding, ExpectedVersion: 3, FindingID: "f1", Reason: "Dismissed"}

	completed := submittedFindingsRun()
	completed.State = Completed
	require.NoError(t, completed.Apply(owner, cmd))

	for name, mutate := range map[string]func(*Run){
		"running":     func(r *Run) { r.State = Running },
		"cancelled":   func(r *Run) { r.State = Cancelled },
		"answer run":  func(r *Run) { r.Runner = Answer },
		"no findings": func(r *Run) { r.Findings = nil },
		"all dismissed": func(r *Run) {
			for i := range r.Findings {
				r.Findings[i].DismissalReason = "x"
			}
		},
	} {
		r := submittedFindingsRun()
		mutate(&r)
		assert.ErrorIs(t, r.Apply(owner, cmd), ErrConflict, name)
		assert.NotContains(t, r.AllowedCommands(owner, HostFacts{}), DismissFinding, name)
	}

	stale := submittedFindingsRun()
	stale.Blocker = BlockerInputsChanged
	assert.ErrorIs(t, stale.Apply(owner, cmd), ErrStale)

	wrongVersion := submittedFindingsRun()
	assert.ErrorIs(t, wrongVersion.Apply(owner, Command{Type: DismissFinding, ExpectedVersion: 2, FindingID: "f1", Reason: "x"}), ErrStale)
}

func TestDismissFindingPayload(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	for name, c := range map[string]Command{
		"no finding":      {FindingID: "", Reason: "x"},
		"unknown":         {FindingID: "nope", Reason: "x"},
		"no reason":       {FindingID: "f1", Reason: "   "},
		"reason too long": {FindingID: "f1", Reason: strings.Repeat("é", MaxDismissalReasonRunes+1)},
	} {
		r := submittedFindingsRun()
		c.Type, c.ExpectedVersion = DismissFinding, 3
		assert.ErrorIs(t, r.Apply(owner, c), ErrValidation, name)
		assert.EqualValues(t, 3, r.Version, name)
	}
	r := submittedFindingsRun()
	require.NoError(t, r.Apply(owner, Command{Type: DismissFinding, ExpectedVersion: 3, FindingID: "f1", Reason: strings.Repeat("é", MaxDismissalReasonRunes)}))
	assert.ErrorIs(t, r.Apply(owner, Command{Type: DismissFinding, ExpectedVersion: 4, FindingID: "f1", Reason: "again"}), ErrConflict, "already dismissed")
	assert.True(t, DismissFinding.Public())
	assert.False(t, DismissFinding.spawns())
}

// A dismissal never changes the outcome, in any state: the hop that was (or
// will be) followed is the one the submitted findings decided. Fingerprints
// ignore dismissals too.
func TestDismissFindingNeverChangesTheOutcome(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	for _, state := range []State{Submitted, Completed} {
		r := submittedFindingsRun()
		r.State = state
		r.Flow = &FlowRef{VersionID: "v", Node: "review", Loop: &LoopState{LoopPolicy: LoopPolicy{MaxIterations: 3, StopBelow: Medium}},
			Hops: map[Outcome]FlowHop{OutcomeChangesRequested: {Node: "build", Runner: Code, Back: true}, OutcomeApproved: {Node: "end"}}}
		require.Equal(t, OutcomeChangesRequested, r.Outcome())
		digest, fingerprint := r.EvidenceDigest(), Fingerprint(r.Findings)
		require.NoError(t, r.Apply(owner, Command{Type: DismissFinding, ExpectedVersion: r.Version, FindingID: "f1", Reason: "Not a contradiction"}))
		assert.Equal(t, OutcomeChangesRequested, r.Outcome(), state)
		assert.Equal(t, digest, r.EvidenceDigest(), state)
		assert.Equal(t, fingerprint, Fingerprint(r.Findings), state)
	}
}

// Only people dismiss: a submitted dismissal reason is cleared (older
// runners keep working), and duplicate finding ids are refused.
func TestSubmitFindingsClearsDismissalsAndRefusesDuplicateIDs(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	running := func() Run {
		return Run{Runner: Findings, State: Running, OwnerID: "owner", Version: 2, InputDigest: "in"}
	}
	r := running()
	require.NoError(t, r.Apply(owner, Command{Type: SubmitFindings, ExpectedVersion: 2, Summary: "s", Recommendation: ChangesRequested,
		Findings: []Finding{{ID: "f1", Severity: High, Description: "d", DismissalReason: "pre-dismissed"}}}))
	assert.Empty(t, r.Findings[0].DismissalReason)
	assert.Equal(t, 1, openFindingCount(r))

	dup := running()
	assert.ErrorIs(t, dup.Apply(owner, Command{Type: SubmitFindings, ExpectedVersion: 2, Summary: "s", Recommendation: ChangesRequested,
		Findings: []Finding{{ID: "f1", Severity: High, Description: "a"}, {ID: "f1", Severity: Low, Description: "b"}}}), ErrValidation)
}

func openFindingCount(r Run) int {
	n := 0
	for _, f := range r.Findings {
		if f.open() {
			n++
		}
	}
	return n
}

func TestDismissFindingKeepsDecisions(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	reviewer := Actor{UserID: "reviewer", Role: "member"}
	r := submittedFindingsRun()
	require.NoError(t, r.Apply(reviewer, Command{Type: Decide, ExpectedVersion: 3, Decision: Accept, EvidenceDigest: r.EvidenceDigest()}))
	require.True(t, r.HumanAccepted())
	require.NoError(t, r.Apply(owner, Command{Type: DismissFinding, ExpectedVersion: r.Version, FindingID: "f2", Reason: "Applied"}))
	assert.True(t, r.HumanAccepted(), "a dismissal does not invalidate decisions")
}
