package flow_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/steermi/agent-contracts/flow"
)

func TestScriptValid(t *testing.T) {
	assert.True(t, flow.Script{"build": {"done", flow.CheckFailed, flow.SameAsBefore}, "review": {"approved", "changes_requested", "failed"}}.Valid())
	assert.False(t, flow.Script{"build": {"exhausted"}}.Valid(), "exhausted is a loop exit, never a run outcome")
	assert.False(t, flow.Script{"build": {"repeat"}}.Valid())
	long := make([]string, flow.MaxScriptedVisits+1)
	for i := range long {
		long[i] = "done"
	}
	assert.False(t, flow.Script{"build": long}.Valid())
	many := flow.Script{}
	for i := range flow.MaxNodes + 1 {
		many[string(rune('a'+i))] = []string{"done"}
	}
	assert.False(t, many.Valid())
}
