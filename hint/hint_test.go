package hint_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/hint"
)

func TestFor(t *testing.T) {
	cases := []struct {
		name string
		in   hint.Input
		tier hint.Tier
		eff  hint.Effort
	}{
		{"build default", hint.Input{Runner: activity.Code}, hint.Standard, hint.Medium},
		{"review default", hint.Input{Runner: activity.Findings}, hint.Standard, hint.High},
		{"low skill", hint.Input{Runner: activity.Answer, SkillEffort: hint.Low}, hint.Fast, hint.Low},
		{"high skill", hint.Input{Runner: activity.Code, SkillEffort: hint.High}, hint.Deep, hint.High},
		{"loop round 2", hint.Input{Runner: activity.Code, LoopIteration: 2}, hint.Deep, hint.High},
		{"loop round 1 stays", hint.Input{Runner: activity.Code, LoopIteration: 1}, hint.Standard, hint.Medium},
		{"low skill after failure", hint.Input{Runner: activity.Code, SkillEffort: hint.Low, PriorFailed: true}, hint.Standard, hint.High},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := hint.For(c.in)
			assert.Equal(t, c.tier, h.Tier)
			assert.Equal(t, c.eff, h.Effort)
			assert.NotEmpty(t, h.Reason)
		})
	}
	assert.Contains(t, hint.For(hint.Input{Runner: activity.Code, LoopIteration: 12}).Reason, "loop round 12")
}

func TestParseEffort(t *testing.T) {
	assert.Equal(t, hint.High, hint.ParseEffort(" HIGH "))
	assert.Equal(t, hint.Effort(""), hint.ParseEffort("max"))
}

func TestFromRun(t *testing.T) {
	run := activity.Run{Runner: activity.Code, Flow: &activity.FlowRef{Loop: &activity.LoopState{Iteration: 2}}}
	assert.Equal(t, hint.Deep, hint.FromRun(run, "", nil).Tier)
	failed := activity.Run{State: activity.Failed}
	assert.True(t, hint.FromRun(activity.Run{Runner: activity.Code}, hint.Low, &failed).Effort == hint.High)
	assert.Equal(t, hint.Fast, hint.FromRun(activity.Run{Runner: activity.Answer}, hint.Low, nil).Tier)
}
