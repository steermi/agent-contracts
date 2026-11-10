package activity

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSessionCommandsOnlyApplyToSessionRuns(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member", MeetingRole: "facilitator"}
	item := Run{OwnerID: "owner", Runner: Answer, State: Waiting, Blocker: BlockerMeetingInput, MeetingRecapSaved: true}
	for cmd := range sessionCommands {
		require.ErrorIs(t, item.guard(owner, cmd), ErrForbidden, cmd)
	}

	session := item
	session.Subject = Subject{Kind: SubjectSession, ID: "s", ScopeType: "team", ScopeID: "team"}
	require.NoError(t, session.guard(owner, CloseMeeting))
	for _, cmd := range []CommandType{Confirm, Retry, Next, ApplyFixes, TakeLocal, ResumeCloud} {
		require.ErrorIs(t, session.guard(owner, cmd), ErrForbidden, cmd)
	}
}
