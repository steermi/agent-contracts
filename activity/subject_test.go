package activity

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSessionSubjectPlan(t *testing.T) {
	actor := Actor{OrgID: "org", UserID: "owner", Role: "member"}
	subject := Subject{Kind: SubjectSession, ID: "session", ScopeType: "team", ScopeID: "team"}
	q := Request{Runner: Answer, Location: Cloud, InputDigest: "digest", Subject: subject}
	r, err := NewRun("run", "org", "", "", actor, q, nil)
	require.NoError(t, err)
	require.Equal(t, subject, r.Subject)
	require.Equal(t, "session", r.SessionID)
	require.Empty(t, r.WorkItemID)
	require.False(t, r.Capabilities.Allows("update_work_item"))
	require.Equal(t, Waiting, r.State)
	require.Equal(t, BlockerMeetingInput, r.Blocker)
	require.NotContains(t, r.AllowedCommands(actor), TakeLocal)
	require.NotContains(t, r.AllowedCommands(actor), Confirm)
	require.Contains(t, r.AllowedCommands(actor), Continue)
	v := r.Version
	require.NoError(t, r.Apply(actor, Command{Type: Continue, ExpectedVersion: v}))
	require.Equal(t, Ready, r.State)
	require.EqualValues(t, 2, r.Generation)
	require.ErrorIs(t, r.Apply(actor, Command{Type: Continue, ExpectedVersion: v}), ErrStale)
	require.NoError(t, r.Apply(actor, Command{Type: Start, ExpectedVersion: r.Version}))
	require.NoError(t, r.Apply(actor, Command{Type: TurnComplete, ExpectedVersion: r.Version}))
	require.Equal(t, Waiting, r.State)
	require.Equal(t, BlockerMeetingInput, r.Blocker)
	require.Empty(t, r.NextRunner())
}

func TestSessionSubjectRejectsInvalidRuns(t *testing.T) {
	for _, tc := range []struct {
		name     string
		kind     Runner
		location Location
		subject  Subject
	}{
		{"build", Code, Cloud, Subject{SubjectSession, "session", "team", "team"}},
		{"review", Findings, Cloud, Subject{SubjectSession, "session", "team", "team"}},
		{"local", Answer, Local, Subject{SubjectSession, "session", "team", "team"}},
		{"workspace scope", Answer, Cloud, Subject{SubjectSession, "session", "workspace", "workspace"}},
		{"missing scope", Answer, Cloud, Subject{SubjectSession, "session", "team", ""}},
		{"unknown subject", Answer, Cloud, Subject{"other", "session", "team", "team"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRun("run", "org", "", "", Actor{UserID: "owner"}, Request{Runner: tc.kind, Location: tc.location, InputDigest: "d", Subject: tc.subject}, nil)
			require.ErrorIs(t, err, ErrValidation)
		})
	}
}

func TestMeetingContinueAuthority(t *testing.T) {
	r, err := NewRun("run", "org", "", "", Actor{UserID: "owner"}, Request{Runner: Answer, Location: Cloud, InputDigest: "d", Subject: Subject{SubjectSession, "s", "team", "t"}}, nil)
	require.NoError(t, err)
	for _, actor := range []Actor{{UserID: "viewer", Role: "viewer"}, {UserID: "worker", Purpose: PurposeExecutor}, {UserID: "", Role: "member"}} {
		require.ErrorIs(t, r.Apply(actor, Command{Type: Continue, ExpectedVersion: r.Version}), ErrForbidden)
	}
	require.NoError(t, r.Apply(Actor{UserID: "participant", Role: "member"}, Command{Type: Continue, ExpectedVersion: r.Version}))
	require.NotContains(t, r.AllowedCommands(Actor{UserID: "participant", Role: "member"}), Continue)
}

func TestMeetingStopFencesTurnAndAllowsExplicitNextTurn(t *testing.T) {
	r, err := NewRun("run", "org", "", "", Actor{UserID: "owner"}, Request{Subject: Subject{Kind: SubjectSession, ID: "session", ScopeType: "team", ScopeID: "team"}, Runner: Answer, Location: Cloud, InputDigest: "digest"}, nil)
	require.NoError(t, err)
	human := Actor{UserID: "participant", Role: "member"}
	require.NoError(t, r.Apply(human, Command{Type: Continue, ExpectedVersion: r.Version, Turn: &MeetingTurn{ActorID: human.UserID}}))
	require.NoError(t, r.Apply(Actor{UserID: human.UserID, Purpose: PurposeExecutor}, Command{Type: Start, ExpectedVersion: r.Version}))
	generation := r.Generation
	require.NoError(t, r.Apply(human, Command{Type: StopMeetingTurn, ExpectedVersion: r.Version}))
	require.Equal(t, Waiting, r.State)
	require.Greater(t, r.Generation, generation)
	require.NoError(t, r.Apply(human, Command{Type: Continue, ExpectedVersion: r.Version}))
}

func TestMeetingFailedTurnAllowsExplicitRecovery(t *testing.T) {
	for _, command := range []Command{{Type: Fail}, {Type: Interrupt, Blocker: BlockerInputsChanged}, {Type: Interrupt, Blocker: BlockerSourceUnavailable}} {
		t.Run(string(command.Type)+string(command.Blocker), func(t *testing.T) {
			human := Actor{UserID: "owner", Role: "member", MeetingRole: "facilitator"}
			run, err := NewRun("run", "org", "", "", human, Request{Runner: Answer, Location: Cloud, InputDigest: "d", Subject: Subject{SubjectSession, "session", "team", "team"}}, nil)
			require.NoError(t, err)
			require.NoError(t, run.Apply(human, Command{Type: Continue, ExpectedVersion: run.Version}))
			command.ExpectedVersion = run.Version
			actor := human
			if command.Type == Interrupt {
				actor.Purpose = PurposeCoordinator
			}
			require.NoError(t, run.Apply(actor, command))
			require.Equal(t, Waiting, run.State)
			require.Equal(t, BlockerMeetingInput, run.Blocker)
			require.Contains(t, run.AllowedCommands(human), Continue)
		})
	}
}

func TestMeetingExplicitRetryClearsPreviousTurnFailure(t *testing.T) {
	human := Actor{UserID: "owner", Role: "member", MeetingRole: "facilitator"}
	run, err := NewRun("run", "org", "", "", human, Request{Runner: Answer, Location: Cloud, InputDigest: "d", Subject: Subject{SubjectSession, "session", "team", "team"}}, nil)
	require.NoError(t, err)
	require.NoError(t, run.Apply(human, Command{Type: Continue, ExpectedVersion: run.Version}))
	require.NoError(t, run.Apply(human, Command{Type: Fail, ExpectedVersion: run.Version, Reason: "Provider unavailable"}))
	require.Equal(t, "Provider unavailable", run.Summary)
	require.NoError(t, run.Apply(human, Command{Type: Continue, ExpectedVersion: run.Version}))
	require.Empty(t, run.Summary, "a new authorized turn must not retain the previous failure banner")
}

func TestProjectSessionUsesConversationalExecutionAndRequiresSavedRecap(t *testing.T) {
	human := Actor{UserID: "owner", Role: "member", MeetingRole: "facilitator"}
	run, err := NewRun("run", "org", "", "", human, Request{Runner: Answer, Location: Cloud, InputDigest: "sources", Subject: Subject{SubjectSession, "session", "project", "project"}}, nil)
	require.NoError(t, err)
	require.Empty(t, run.WorkItemID)
	require.Empty(t, run.ProjectID)
	require.False(t, run.Capabilities.Allows("get_team_info"))
	require.ErrorIs(t, run.Apply(human, Command{Type: CloseMeeting, ExpectedVersion: run.Version}), ErrConflict)
	require.NoError(t, run.Apply(human, Command{Type: Continue, ExpectedVersion: run.Version, Turn: &MeetingTurn{ActorID: human.UserID}}))
	executor := Actor{UserID: human.UserID, Purpose: PurposeExecutor}
	require.NoError(t, run.Apply(executor, Command{Type: Start, ExpectedVersion: run.Version}))
	require.NoError(t, run.Apply(executor, Command{Type: TurnComplete, ExpectedVersion: run.Version}))
	require.Equal(t, Waiting, run.State)
	require.Empty(t, run.NextRunner())
}
