package activity

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestProjectFoundationCanCloseSavedPlanAndTakeLocal(t *testing.T) {
	actor := Actor{OrgID: "org", UserID: "owner", Role: "member", MeetingRole: "facilitator"}
	r := Run{ID: "run", OrgID: "org", OwnerID: "owner", Runner: Answer, Location: Cloud, State: Waiting, Blocker: BlockerMeetingInput, Version: 1, Generation: 1, Subject: Subject{Kind: SubjectSession, ID: "session", ScopeType: "project", ScopeID: "project"}, SessionID: "session"}
	require.NoError(t, r.Apply(actor, Command{Type: TakeLocal, ExpectedVersion: 1}))
	require.Equal(t, Local, r.Location)
	require.Equal(t, Ready, r.State)
	r.State = Waiting
	r.Blocker = BlockerMeetingInput
	require.NoError(t, r.Apply(Actor{OrgID: "org", UserID: "owner", Purpose: PurposeCoordinator}, Command{Type: RecordMeetingRecap, ExpectedVersion: r.Version, MeetingRecapSaved: true}))
	require.NoError(t, r.Apply(actor, Command{Type: CloseMeeting, ExpectedVersion: r.Version}))
	require.Equal(t, Completed, r.State)
}
func TestTeamMeetingsCannotTakeLocal(t *testing.T) {
	r := Run{OrgID: "org", OwnerID: "owner", Runner: Answer, Location: Cloud, State: Waiting, Blocker: BlockerMeetingInput, Version: 1, Subject: Subject{Kind: SubjectSession, ID: "s", ScopeType: "team", ScopeID: "team"}}
	require.Error(t, r.Apply(Actor{OrgID: "org", UserID: "owner", Role: "member", MeetingRole: "facilitator"}, Command{Type: TakeLocal, ExpectedVersion: 1}))
}
