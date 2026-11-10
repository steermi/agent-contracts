package activity

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestActivityDeliveryIndependentAndRepair(t *testing.T) {
	actor := Actor{UserID: "developer", Role: "member"}
	rework := &FlowRef{VersionID: "v", Node: "build", Hops: map[Outcome]FlowHop{OutcomeChangesRequested: {Node: "build", Runner: Code}}}
	r, err := NewRun("run", "org", "project", "item", actor, Request{Runner: Code, Location: "local", InputDigest: "input", CommandKey: "click", Flow: rework}, nil)
	require.NoError(t, err)
	require.Equal(t, Suggested, r.State)
	require.NoError(t, r.Apply(actor, Command{Type: "confirm", ExpectedVersion: 1}))
	require.Equal(t, Ready, r.State)
	require.NoError(t, r.Apply(actor, Command{Type: "start", ExpectedVersion: 2}))
	require.NoError(t, r.Apply(actor, Command{Type: "submit", ExpectedVersion: 3, Outputs: []OutputRef{{Kind: "repository", ResourceID: "repo", RepositoryID: "repo", CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}))
	require.Equal(t, Submitted, r.State)
	require.False(t, r.HumanAccepted())
	require.ErrorIs(t, r.Apply(actor, Command{Type: "decision", ExpectedVersion: 4, Decision: "accept", EvidenceDigest: r.EvidenceDigest()}), ErrForbidden)
	reviewer := Actor{UserID: "reviewer", Role: "member"}
	require.NoError(t, r.Apply(reviewer, Command{Type: "decision", ExpectedVersion: 4, Decision: "request_changes", EvidenceDigest: r.EvidenceDigest(), Reason: "Missing test"}))
	require.Equal(t, Code, r.NextRunner())
}

func TestActivityStaleAndUntrustedEvidence(t *testing.T) {
	r := Run{ID: "r", Runner: Findings, State: Running, Version: 3, InputDigest: "new", OwnerID: "author"}
	require.ErrorIs(t, r.Apply(Actor{UserID: "reviewer"}, Command{Type: "submit", ExpectedVersion: 2, InputDigest: "old"}), ErrStale)
	require.ErrorIs(t, r.Apply(Actor{UserID: "reviewer"}, Command{Type: "check", ExpectedVersion: 3, Check: &Check{Provenance: "provider_verified"}}), ErrForbidden)
	require.Equal(t, int64(3), r.Version)
}

func TestActivityReviewCannotBecomeHumanAcceptance(t *testing.T) {
	r := Run{Runner: Findings, State: Running, Version: 1, InputDigest: "i", OwnerID: "reviewer"}
	a := Actor{UserID: "reviewer", Role: "member"}
	require.NoError(t, r.Apply(a, Command{Type: "findings", ExpectedVersion: 1, Recommendation: "approve", Summary: "No issues found"}))
	require.Equal(t, Submitted, r.State)
	require.False(t, r.HumanAccepted())
	require.Equal(t, Runner(""), r.NextRunner())
}

func TestActivityRequiredChecksAndQuorumBoundRevision(t *testing.T) {
	r := Run{Runner: Code, State: Submitted, Version: 1, InputDigest: "inputs", OwnerID: "builder", Outputs: []OutputRef{{Kind: "repository", ResourceID: "repo", RepositoryID: "repo", CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}, Policy: Policy{RequiredChecks: []string{"test"}, Quorum: 2, ReviewerIDs: []string{"alice", "bob"}}}
	a := Actor{UserID: "alice", Role: "member"}
	require.ErrorIs(t, r.Apply(a, Command{Type: "decision", ExpectedVersion: 1, Decision: "accept", EvidenceDigest: r.EvidenceDigest()}), ErrChecksPending)
	require.NoError(t, r.Apply(Actor{UserID: "git", Purpose: "verified_git"}, Command{Type: "check", ExpectedVersion: 1, Check: &Check{RepositoryID: "repo", CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "test", ProviderRunID: "123", Attempt: 1, Status: "success"}}))
	digest := r.EvidenceDigest()
	require.NoError(t, r.Apply(a, Command{Type: "decision", ExpectedVersion: 2, Decision: "accept", EvidenceDigest: digest}))
	require.False(t, r.HumanAccepted())
	require.NoError(t, r.Apply(Actor{UserID: "bob", Role: "member"}, Command{Type: "decision", ExpectedVersion: 3, Decision: "accept", EvidenceDigest: digest}))
	require.True(t, r.HumanAccepted())
	require.NoError(t, r.Apply(a, Command{Type: "revise", ExpectedVersion: 4, InputDigest: "changed"}))
	require.False(t, r.HumanAccepted())
	require.ErrorIs(t, r.Apply(a, Command{Type: "decision", ExpectedVersion: 5, Decision: "accept", EvidenceDigest: digest}), ErrStale)
}

func TestActivityRuleConsentAndCausation(t *testing.T) {
	rule := Rule{FromStatus: "*", ToStatus: "review", ItemType: "*", Runner: Findings, Mode: "auto"}
	for _, tt := range []struct {
		location      Location
		owner, origin string
		state         State
		match         bool
	}{
		{"local", "u", "user", Suggested, true}, {"cloud", "", "user", Waiting, true}, {"cloud", "u", "user", Ready, true}, {"cloud", "u", "activity", Suggested, false},
	} {
		state, match := EvaluateRule(rule, Transition{FromStatus: "in_progress", ToStatus: "in_review", ItemType: "story", Origin: tt.origin}, tt.location, tt.owner)
		require.Equal(t, tt.state, state)
		require.Equal(t, tt.match, match)
	}
}

func TestRevisionInvalidatesAcceptanceWithoutRewritingPins(t *testing.T) {
	r := Run{ID: "run", OrgID: "org", OwnerID: "author", Runner: Code, State: Submitted, Version: 1, Generation: 4, InputDigest: "old"}
	r.Decisions = []Decision{{UserID: "reviewer", Kind: "accept", EvidenceDigest: r.EvidenceDigest()}}
	require.True(t, r.HumanAccepted())
	require.NoError(t, r.Apply(Actor{UserID: "editor", Role: "member"}, Command{Type: "revise", ExpectedVersion: 1, InputDigest: "new"}))
	require.Equal(t, "old", r.InputDigest)
	require.EqualValues(t, 4, r.Generation)
	require.False(t, r.HumanAccepted())
}

func TestIndependentCodeReviewRequiresCurrentChecks(t *testing.T) {
	r, err := NewRun("r", "org", "project", "item", Actor{UserID: "reviewer"}, Request{Runner: Findings, Location: "local", InputDigest: "i", Policy: Policy{RequiredChecks: []string{"test"}}, Inputs: []OutputRef{{Kind: "repository", ResourceID: "repo", RepositoryID: "repo", CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}, nil)
	require.NoError(t, err)
	require.False(t, r.ChecksPassed())
}
func TestFailedCheckRepairCountsAgainstRepairBound(t *testing.T) {
	parent := Run{ID: "p", RootID: "root", Runner: Code, Round: 1, Generation: 1}
	r, err := NewRun("r", "org", "project", "item", Actor{UserID: "builder"}, Request{Runner: Code, Location: "local", InputDigest: "i", Policy: Policy{MaxRepairRounds: 2}}, &parent)
	require.NoError(t, err)
	require.Equal(t, BlockerRepairLimit, r.Blocker)
}

func TestBuildCannotSubstituteWorkItemForPinnedRepository(t *testing.T) {
	r := Run{Runner: Code, State: Running, Version: 1, OwnerID: "owner", Inputs: []OutputRef{{Kind: "repository", RepositoryID: "repo", ResourceID: "repo", CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}}
	require.Error(t, r.Apply(Actor{UserID: "owner"}, Command{Type: "submit", ExpectedVersion: 1, Outputs: []OutputRef{{Kind: "work_item", ResourceID: "item", Revision: "1", Hash: Digest("x")}}}))
}
func TestLatePendingCheckCannotRegressSameProviderAttempt(t *testing.T) {
	check := Check{RepositoryID: "repo", CommitSHA: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Name: "test", ProviderRunID: "ci", Attempt: 1, Status: "success", StartedAt: time.Now().UTC()}
	r := Run{Runner: Code, State: Submitted, Version: 1, Outputs: []OutputRef{{Kind: "repository", RepositoryID: "repo", ResourceID: "repo", CommitSHA: check.CommitSHA}}, Checks: []Check{check}}
	check.Status = "pending"
	require.NoError(t, r.Apply(Actor{Purpose: "verified_git"}, Command{Type: "check", ExpectedVersion: 1, Check: &check}))
	require.Equal(t, CheckSuccess, r.Checks[0].Status)
}

func TestClarificationNeedsHumanAnswerAndAdvancesGeneration(t *testing.T) {
	r := Run{Runner: Answer, State: Running, OwnerID: "owner", Version: 1, Generation: 1, Location: "cloud"}
	require.NoError(t, r.Apply(Actor{UserID: "owner", Purpose: "activity_executor"}, Command{Type: "clarify", ExpectedVersion: 1, Reason: "Which audience?"}))
	require.Equal(t, Waiting, r.State)
	require.Equal(t, BlockerNeedsClarification, r.Blocker)
	require.Error(t, r.Apply(Actor{UserID: "owner"}, Command{Type: "resume_cloud", ExpectedVersion: r.Version}))
	require.NoError(t, r.Apply(Actor{UserID: "owner"}, Command{Type: "resume_cloud", ExpectedVersion: r.Version, Reason: "Developers"}))
	require.EqualValues(t, 2, r.Generation)
	require.Equal(t, Ready, r.State)
}

func TestAllowedCommandsFollowTheGuards(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	reviewer := Actor{UserID: "reviewer", Role: "member"}
	admin := Actor{UserID: "admin", Role: "admin"}
	viewer := Actor{UserID: "viewer", Role: "viewer"}
	machine := Actor{UserID: "owner", Purpose: PurposeExecutor}
	suggested := Run{Runner: Code, State: Suggested, OwnerID: "owner", Version: 1}
	require.Equal(t, []CommandType{Confirm, Cancel}, suggested.AllowedCommands(owner))
	require.Equal(t, []CommandType{Confirm, Cancel}, suggested.AllowedCommands(admin))
	require.Empty(t, suggested.AllowedCommands(reviewer))
	require.Empty(t, suggested.AllowedCommands(viewer))
	require.Empty(t, suggested.AllowedCommands(machine))

	submitted := Run{Runner: Code, State: Submitted, OwnerID: "owner", Version: 1,
		Flow: &FlowRef{VersionID: "v", Node: "build", Hops: map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings}}}}
	require.Equal(t, []CommandType{Next}, submitted.AllowedCommands(owner), "authors never decide on their own work")
	require.Equal(t, []CommandType{Decide}, submitted.AllowedCommands(reviewer))
	submitted.Blocker = BlockerInputsChanged
	require.Empty(t, submitted.AllowedCommands(reviewer), "stale evidence cannot be decided")

	failed := Run{Runner: Answer, State: Failed, OwnerID: "owner", Version: 1}
	require.Equal(t, []CommandType{Retry, TakeLocal, ResumeCloud}, failed.AllowedCommands(owner))
	for _, cmd := range failed.AllowedCommands(owner) {
		if !cmd.spawns() {
			r := failed
			require.NoError(t, r.Apply(owner, Command{Type: cmd, ExpectedVersion: 1}), cmd)
		}
	}
}

func TestInternalCommandsGoThroughTheStateMachine(t *testing.T) {
	coordinator := Actor{UserID: "owner", Purpose: PurposeCoordinator}
	r := Run{Runner: Code, State: Running, OwnerID: "owner", Version: 1}
	require.ErrorIs(t, r.Apply(Actor{UserID: "owner"}, Command{Type: Interrupt, ExpectedVersion: 1, Blocker: BlockerExecutionBusy}), ErrForbidden)
	require.NoError(t, r.Apply(coordinator, Command{Type: RecordCost, ExpectedVersion: 1, Cost: 1.5}))
	require.InDelta(t, 1.5, r.Cost, 1e-9)
	require.NoError(t, r.Apply(coordinator, Command{Type: Interrupt, ExpectedVersion: 2, Blocker: BlockerInputsChanged}))
	require.Equal(t, Cancelled, r.State)
	require.ErrorIs(t, r.Apply(coordinator, Command{Type: Interrupt, ExpectedVersion: 3, Blocker: BlockerExecutionBusy}), ErrConflict, "terminal runs stay terminal")
	require.ErrorIs(t, r.Apply(coordinator, Command{Type: Next, ExpectedVersion: 3}), ErrValidation, "successors are created, never applied")
}

func TestSuccessorKinds(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	review := Run{Runner: Findings, State: Submitted, OwnerID: "owner", Version: 2, Recommendation: ChangesRequested,
		Flow: &FlowRef{VersionID: "v", Node: "review", Hops: map[Outcome]FlowHop{OutcomeChangesRequested: {Node: "build", Runner: Code}}}}
	kind, err := review.Successor(owner, Command{Type: ApplyFixes, ExpectedVersion: 2})
	require.NoError(t, err)
	require.Equal(t, Code, kind)
	_, err = review.Successor(owner, Command{Type: ApplyFixes, ExpectedVersion: 1})
	require.ErrorIs(t, err, ErrStale)
	_, err = review.Successor(Actor{UserID: "other", Role: "member"}, Command{Type: Next, ExpectedVersion: 2})
	require.ErrorIs(t, err, ErrForbidden)
}

func TestActivityRuleTargetsTeamStatus(t *testing.T) {
	qa := Rule{FromStatus: "*", ToStatus: "in_review", ToStatusID: "qa", ItemType: "*", Runner: Findings, Mode: "ask"}
	category := Rule{FromStatus: "*", ToStatus: "in_review", ItemType: "*", Runner: Findings, Mode: "ask"}
	for _, tt := range []struct {
		name  string
		rule  Rule
		t     Transition
		match bool
	}{
		{"status rule matches its status", qa, Transition{FromStatus: "in_progress", ToStatus: "in_review", FromStatusID: "dev", ToStatusID: "qa"}, true},
		{"status rule matches a same-category move", qa, Transition{FromStatus: "in_review", ToStatus: "in_review", FromStatusID: "code-review", ToStatusID: "qa"}, true},
		{"status rule ignores other statuses of the category", qa, Transition{FromStatus: "in_progress", ToStatus: "in_review", FromStatusID: "dev", ToStatusID: "code-review"}, false},
		{"status rule ignores an unchanged status", qa, Transition{FromStatus: "in_review", ToStatus: "in_review", FromStatusID: "qa", ToStatusID: "qa"}, false},
		{"category rule still matches by category", category, Transition{FromStatus: "in_progress", ToStatus: "in_review", FromStatusID: "dev", ToStatusID: "qa"}, true},
		{"category rule ignores same-category moves", category, Transition{FromStatus: "in_review", ToStatus: "in_review", FromStatusID: "code-review", ToStatusID: "qa"}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.t.ItemType, tt.t.Origin = "story", "user"
			_, match := EvaluateRule(tt.rule, tt.t, Local, "")
			require.Equal(t, tt.match, match)
		})
	}
}

func TestActivityOrgLimitBlockersResumeInCloud(t *testing.T) {
	for _, b := range []Blocker{BlockerOrgBudget, BlockerMessageLimit} {
		owner := Actor{UserID: "developer", Role: "member"}
		r, err := NewRun("run", "org", "project", "item", owner, Request{Runner: Findings, Location: "cloud", InputDigest: "input", CommandKey: "click"}, nil)
		require.NoError(t, err)
		require.NoError(t, r.Apply(owner, Command{Type: Confirm, ExpectedVersion: r.Version}))
		require.NoError(t, r.Apply(Actor{Purpose: PurposeCoordinator}, Command{Type: Interrupt, Blocker: b, ExpectedVersion: r.Version}))
		require.Equal(t, Waiting, r.State)
		require.True(t, b.Resumable())
		require.Contains(t, r.AllowedCommands(owner), ResumeCloud)
		require.NoError(t, r.Apply(owner, Command{Type: ResumeCloud, ExpectedVersion: r.Version}))
		require.Equal(t, Ready, r.State)
		require.Empty(t, r.Blocker)
	}
}

func TestActorForBindsRunnerAndSkillSlug(t *testing.T) {
	base := Actor{OrgID: "o", UserID: "u", Role: "member", Runner: Code, SkillSlug: "old"}
	if got := base.For(Findings, &SkillRef{Slug: "steermi:review"}); got.Runner != Findings || got.SkillSlug != "steermi:review" || got.UserID != "u" {
		t.Fatalf("unexpected actor %+v", got)
	}
	if got := base.For(Answer, nil); got.Runner != Answer || got.SkillSlug != "" {
		t.Fatalf("nil skill must clear the slug: %+v", got)
	}
}
