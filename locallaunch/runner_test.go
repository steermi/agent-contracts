package locallaunch_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/steermi/agent-contracts/hint"
	"github.com/steermi/agent-contracts/locallaunch"
)

func TestRunnerSanitize(t *testing.T) {
	r, ok := locallaunch.Runner{Agent: locallaunch.Codex, AgentVersion: " 0.9.1 ", Model: "bad model\n", Effort: "HIGH"}.Sanitize()
	assert.True(t, ok)
	assert.Equal(t, locallaunch.Runner{Agent: locallaunch.Codex, AgentVersion: "0.9.1", Effort: hint.High, SelfReported: true}, r)

	_, ok = locallaunch.Runner{Agent: "aider"}.Sanitize()
	assert.False(t, ok)
	assert.Equal(t, "Codex CLI", locallaunch.Codex.Title())
}
