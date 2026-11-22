package activity

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetStepMode(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	admin := Actor{UserID: "admin", Role: "admin"}
	other := Actor{UserID: "other", Role: "member"}
	machine := Actor{UserID: "owner", Purpose: PurposeFlow}
	withNext := func() *FlowRef {
		return &FlowRef{VersionID: "v", Node: "build", Hops: map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings}}}
	}
	toEnd := &FlowRef{VersionID: "v", Node: "review", Hops: map[Outcome]FlowHop{OutcomeApproved: {Stop: "end"}}}
	for _, tc := range []struct {
		name  string
		run   func(*Run)
		actor Actor
		mode  StepMode
		want  error
	}{
		{"owner pauses a running run", nil, owner, StepModeAsk, nil},
		{"manager pauses", nil, admin, StepModeAsk, nil},
		{"suggestion", func(r *Run) { r.State = Suggested }, owner, StepModeAsk, nil},
		{"submitted run before its hop", func(r *Run) { r.State = Submitted }, owner, StepModeFlow, nil},
		{"other member", nil, other, StepModeAsk, ErrForbidden},
		{"machine", nil, machine, StepModeAsk, ErrForbidden},
		{"terminal", func(r *Run) { r.State = Completed }, owner, StepModeAsk, ErrConflict},
		{"off a flow", func(r *Run) { r.Flow = nil }, owner, StepModeAsk, ErrConflict},
		{"final blocker", func(r *Run) { r.State, r.Blocker = Waiting, BlockerRepairLimit }, owner, StepModeAsk, ErrConflict},
		{"submitted, flow ends", func(r *Run) {
			r.Runner, r.State, r.Recommendation, r.Flow = Findings, Submitted, Approve, toEnd
		}, owner, StepModeAsk, ErrConflict},
		{"missing mode", nil, owner, "", ErrValidation},
		{"bad mode", nil, owner, "sometimes", ErrValidation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := Run{Runner: Code, State: Running, OwnerID: "owner", Version: 1, Flow: withNext()}
			if tc.run != nil {
				tc.run(&r)
			}
			state := r.State
			if tc.want == ErrConflict {
				assert.NotContains(t, r.AllowedCommands(owner, HostFacts{}), SetStepMode, "never offered when refused")
			}
			err := r.Apply(tc.actor, Command{Type: SetStepMode, ExpectedVersion: 1, StepMode: tc.mode})
			if tc.want != nil {
				require.ErrorIs(t, err, tc.want)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.mode.stored(), r.StepMode, "the default is stored empty")
			assert.Equal(t, state, r.State, "only the mode changes")
		})
	}
	assert.True(t, SetStepMode.Public())
	r := Run{Runner: Code, State: Running, OwnerID: "owner", Version: 1, Flow: withNext()}
	require.ErrorIs(t, r.Apply(owner, Command{Type: Cancel, ExpectedVersion: 1, StepMode: StepModeAsk}), ErrValidation, "only confirm, next and set_step_mode take a mode")
	assert.Equal(t, StepModeFlow, StepMode("").OrDefault())
	assert.Equal(t, StepModeAsk, StepModeAsk.OrDefault())
}

func TestStepModeCarriedToSuccessors(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	_, err := NewRun("r", "o", "p", "i", owner, Request{Runner: Code, Location: Cloud, InputDigest: "x", StepMode: "bad"}, nil)
	require.ErrorIs(t, err, ErrValidation)
	first, err := NewRun("r", "o", "p", "i", owner, Request{Runner: Code, Location: Cloud, InputDigest: "x", StepMode: StepModeAsk}, nil)
	require.NoError(t, err)
	assert.Equal(t, StepModeAsk, first.StepMode)
	next, err := NewRun("n", "o", "p", "i", owner, Request{Runner: Findings, Location: Cloud, InputDigest: "x", StepMode: StepModeFlow}, &first)
	require.NoError(t, err)
	assert.Empty(t, next.StepMode, "the request decides (successors pass SuccessorRun.StepMode); flow is stored empty")
	assert.Equal(t, StepModeFlow, next.StepMode.OrDefault())
	next, err = NewRun("n", "o", "p", "i", owner, Request{Runner: Findings, Location: Cloud, InputDigest: "x"}, &first)
	require.NoError(t, err)
	assert.Empty(t, next.StepMode, "empty is flow, never the parent's: one representation")

	// The chain's mode reaches the successor through Successor.
	first.State, first.Version = Submitted, 1
	first.Flow = &FlowRef{VersionID: "v", Node: "build", Hops: map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings}}}
	placed, err := first.Successor(owner, Command{Type: Next, ExpectedVersion: 1})
	require.NoError(t, err)
	next, err = NewRun("n", "o", "p", "i", owner, Request{Runner: placed.Runner, Location: placed.Location, InputDigest: "x", StepMode: placed.StepMode}, &first)
	require.NoError(t, err)
	assert.Equal(t, StepModeAsk, next.StepMode)

	// A JSON run without the field reads flow.
	var stored Run
	require.NoError(t, json.Unmarshal([]byte(`{"id":"r","state":"running"}`), &stored))
	assert.Equal(t, StepModeFlow, stored.StepMode.OrDefault())
}

func TestConfirmWithLocation(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	admin := Actor{UserID: "admin", Role: "admin"}
	flowRef := &FlowRef{VersionID: "v", Node: "build", Hops: map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings}}}
	r := Run{Runner: Code, State: Suggested, OwnerID: "owner", Version: 1, Generation: 1, Location: Cloud, Flow: flowRef}
	offFlow := Run{Runner: Code, State: Suggested, OwnerID: "owner", Version: 1, Location: Cloud}
	require.ErrorIs(t, offFlow.Apply(owner, Command{Type: Confirm, ExpectedVersion: 1, StepMode: StepModeAsk}), ErrConflict, "a step mode needs a flow, as set_step_mode does")
	require.ErrorIs(t, r.Apply(admin, Command{Type: Confirm, ExpectedVersion: 1, Location: Local}), ErrForbidden, "only the owner picks where")
	require.ErrorIs(t, r.Apply(owner, Command{Type: Confirm, ExpectedVersion: 1, Location: "moon"}), ErrValidation)
	require.NoError(t, r.Apply(owner, Command{Type: Confirm, ExpectedVersion: 1, Location: Local, StepMode: StepModeAsk}))
	assert.Equal(t, Local, r.Location)
	assert.Equal(t, Ready, r.State)
	assert.Equal(t, StepModeAsk, r.StepMode)
	assert.EqualValues(t, 1, r.Generation, "a suggestion has no execution to fence")

	waiting := Run{Runner: Code, State: Waiting, Blocker: BlockerReassessment, OwnerID: "owner", Version: 1, Generation: 1, Location: Cloud, LaunchID: "l"}
	require.ErrorIs(t, waiting.Apply(owner, Command{Type: Confirm, ExpectedVersion: 1, Location: Local}), ErrConflict, "a waiting run moves with take_local")
	require.NoError(t, waiting.Apply(owner, Command{Type: Confirm, ExpectedVersion: 1}))
	assert.Equal(t, Cloud, waiting.Location)
	assert.EqualValues(t, 1, waiting.Generation)

	page := Run{Runner: Answer, State: Suggested, OwnerID: "owner", Version: 1, Location: Cloud, Subject: Subject{Kind: SubjectDocPage, ID: "pg"}}
	require.ErrorIs(t, page.Apply(owner, Command{Type: Confirm, ExpectedVersion: 1, Location: Local}), ErrValidation, "docs page runs are cloud only")

	cancel := Run{Runner: Code, State: Running, OwnerID: "owner", Version: 1}
	require.ErrorIs(t, cancel.Apply(owner, Command{Type: Cancel, ExpectedVersion: 1, Location: Local}), ErrValidation, "only confirm and next take a location")
}

func TestSuccessorRunPlacement(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	admin := Actor{UserID: "admin", Role: "admin"}
	r := Run{Runner: Code, State: Submitted, OwnerID: "owner", Version: 1, Location: Local,
		Flow: &FlowRef{VersionID: "v", Node: "build", Hops: map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings, Location: Cloud}}}}
	next := func(r Run, c Command) SuccessorRun {
		c.Type, c.ExpectedVersion = Next, r.Version
		s, err := r.Successor(owner, c)
		require.NoError(t, err)
		return s
	}
	assert.Equal(t, SuccessorRun{Runner: Findings, Location: Cloud}, next(r, Command{}), "the pinned hop location; flow stored empty")
	_, err := r.Successor(admin, Command{Type: Next, ExpectedVersion: 1, Location: Local})
	require.ErrorIs(t, err, ErrForbidden)
	_, err = r.Successor(owner, Command{Type: Next, ExpectedVersion: 1, Location: "moon"})
	require.ErrorIs(t, err, ErrValidation)
	assert.Equal(t, SuccessorRun{Runner: Findings, Location: Local, LocationChosen: true},
		next(r, Command{Location: Local}), "the person's choice wins")
	_, err = r.Successor(owner, Command{Type: Retry, ExpectedVersion: 1, Location: Cloud})
	require.Error(t, err)

	unresolved := r
	unresolved.Flow = &FlowRef{VersionID: "v", Node: "build", Hops: map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings}}}
	assert.Equal(t, Local, next(unresolved, Command{}).Location, "unresolved hops keep the run's location")

	r.StepMode = StepModeAsk
	assert.Equal(t, StepModeAsk, next(r, Command{}).StepMode, "the chain's mode")
	assert.Empty(t, next(r, Command{StepMode: StepModeFlow}).StepMode, "the person's choice on next, stored as a run stores it")
	machine := Actor{UserID: "admin", Role: "admin", Purpose: PurposeExecutor}
	_, err = r.Successor(machine, Command{Type: Next, ExpectedVersion: 1, StepMode: StepModeFlow})
	require.Error(t, err, "machines never choose a mode")

	failed := Run{Runner: Code, State: Failed, OwnerID: "owner", Version: 1, Location: Local, StepMode: StepModeAsk}
	s, err := failed.Successor(owner, Command{Type: Retry, ExpectedVersion: 1})
	require.NoError(t, err)
	assert.Equal(t, SuccessorRun{Runner: Code, Location: Local, StepMode: StepModeAsk}, s)
	_, err = failed.Successor(owner, Command{Type: Retry, ExpectedVersion: 1, StepMode: StepModeFlow})
	require.ErrorIs(t, err, ErrValidation, "only next takes a mode")
}

func TestCanChooseLocation(t *testing.T) {
	owner := Actor{UserID: "owner", Role: "member"}
	admin := Actor{UserID: "admin", Role: "admin"}
	flowRef := &FlowRef{VersionID: "v", Node: "build", Hops: map[Outcome]FlowHop{OutcomeDone: {Node: "review", Runner: Findings, Location: Cloud}}}
	suggested := Run{Runner: Code, State: Suggested, OwnerID: "owner", Version: 1, Location: Cloud}
	assert.True(t, suggested.CanChooseLocation(owner))
	assert.False(t, suggested.CanChooseLocation(admin), "owner only")
	assert.False(t, suggested.CanChooseLocation(Actor{UserID: "owner", Purpose: PurposeFlow}), "people only")
	submitted := Run{Runner: Code, State: Submitted, OwnerID: "owner", Version: 1, Location: Local, Flow: flowRef}
	assert.True(t, submitted.CanChooseLocation(owner))
	running := submitted
	running.State = Running
	assert.False(t, running.CanChooseLocation(owner), "nothing to start")
	page := suggested
	page.Subject = Subject{Kind: SubjectDocPage, ID: "pg"}
	assert.False(t, page.CanChooseLocation(owner), "docs page runs are cloud only")
}

func TestBlockerDetail(t *testing.T) {
	coord := Actor{UserID: "owner", Purpose: PurposeCoordinator}
	owner := Actor{UserID: "owner", Role: "member"}
	r := Run{Runner: Code, State: Running, OwnerID: "owner", Version: 1, Generation: 1, Location: Cloud}
	require.ErrorIs(t, r.Apply(coord, Command{Type: Interrupt, ExpectedVersion: 1, Blocker: BlockerSourceUnavailable, BlockerDetail: "nope"}), ErrValidation)
	require.ErrorIs(t, r.Apply(coord, Command{Type: Interrupt, ExpectedVersion: 1, Blocker: BlockerExecutionUnavailable, BlockerDetail: BlockerDetailRepositoryUnreachable}), ErrValidation, "a detail explains only its blockers")
	require.ErrorIs(t, r.Apply(coord, Command{Type: Interrupt, ExpectedVersion: 1, Blocker: BlockerSourceUnavailable, BlockerDetail: "budget"}), ErrValidation, "unknown details explain nothing")
	require.NoError(t, r.Apply(coord, Command{Type: Interrupt, ExpectedVersion: 1, Blocker: BlockerSourceUnavailable, BlockerDetail: BlockerDetailRepositoryUnreachable}))
	assert.Equal(t, BlockerDetailRepositoryUnreachable, r.BlockerDetail)
	require.NoError(t, r.Apply(coord, Command{Type: Interrupt, ExpectedVersion: 2, Blocker: BlockerLocalDisconnected}))
	assert.Empty(t, r.BlockerDetail, "a new interrupt without a detail clears it")
	require.NoError(t, r.Apply(coord, Command{Type: Interrupt, ExpectedVersion: 3, Blocker: BlockerSourceUnavailable, BlockerDetail: BlockerDetailAccessLost}))
	require.NoError(t, r.Apply(owner, Command{Type: ResumeCloud, ExpectedVersion: 4}))
	assert.Empty(t, r.Blocker)
	assert.Empty(t, r.BlockerDetail, "cleared with the blocker")
}
