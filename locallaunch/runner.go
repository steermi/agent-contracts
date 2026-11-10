package locallaunch

import (
	"regexp"
	"strings"

	"github.com/steermi/agent-contracts/hint"
)

// Agent identifies a coding agent the CLI can run a launch with.
type Agent string

const (
	ClaudeCode Agent = "claude-code"
	Codex      Agent = "codex"
	Copilot    Agent = "copilot"
	Opencode   Agent = "opencode"
)

// Agents lists every known agent, Claude Code first.
var Agents = []Agent{ClaudeCode, Codex, Copilot, Opencode}

// Valid reports whether a is a known agent.
func (a Agent) Valid() bool {
	for _, k := range Agents {
		if a == k {
			return true
		}
	}
	return false
}

// Title is the agent's display name.
func (a Agent) Title() string {
	switch a {
	case ClaudeCode:
		return "Claude Code"
	case Codex:
		return "Codex CLI"
	case Copilot:
		return "GitHub Copilot CLI"
	case Opencode:
		return "opencode"
	}
	return string(a)
}

// Runner is what a launch ran with, as the CLI reports it on finish. It is
// self-reported display data, never used for authorization.
type Runner struct {
	Agent        Agent       `json:"agent"`
	AgentVersion string      `json:"agent_version,omitempty"`
	Model        string      `json:"model,omitempty"`
	Effort       hint.Effort `json:"effort,omitempty"`
	// SelfReported is always true once sanitized; readers label it so.
	SelfReported bool `json:"self_reported,omitempty"`
}

var runnerToken = regexp.MustCompile(`^[A-Za-z0-9._:/@+-]{1,120}$`)

// Sanitize returns the runner with malformed fields dropped, or false when
// the agent is unknown (the report is then ignored, never rejected).
func (r Runner) Sanitize() (Runner, bool) {
	if !r.Agent.Valid() {
		return Runner{}, false
	}
	token := func(s string) string {
		if s = strings.TrimSpace(s); runnerToken.MatchString(s) {
			return s
		}
		return ""
	}
	return Runner{
		Agent:        r.Agent,
		AgentVersion: token(r.AgentVersion),
		Model:        token(r.Model),
		Effort:       hint.ParseEffort(string(r.Effort)),
		SelfReported: true,
	}, true
}
