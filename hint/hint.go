// Package hint suggests how much model a run deserves. Steermi names a tier
// and an effort, never a model: cloud runs map the tier onto the
// organization's catalog, local runs onto the developer's agent profile.
// The suggestion is advice; nothing is authorized or blocked by it.
package hint

import (
	"strconv"
	"strings"

	"github.com/steermi/agent-contracts/activity"
)

// Tier is a model size class.
type Tier string

const (
	Fast     Tier = "fast"
	Standard Tier = "standard"
	Deep     Tier = "deep"
)

// Effort is how hard the model should think.
type Effort string

const (
	Low    Effort = "low"
	Medium Effort = "medium"
	High   Effort = "high"
)

// Valid reports whether e is a known effort.
func (e Effort) Valid() bool { return e == Low || e == Medium || e == High }

// Valid reports whether t is a known tier.
func (t Tier) Valid() bool { return t == Fast || t == Standard || t == Deep }

// ParseEffort reads a skill's metadata value ("" or unknown → "").
func ParseEffort(s string) Effort {
	e := Effort(strings.ToLower(strings.TrimSpace(s)))
	if e.Valid() {
		return e
	}
	return ""
}

// Hint is the suggestion carried by a launch bundle.
type Hint struct {
	Tier   Tier   `json:"tier"`
	Effort Effort `json:"effort"`
	// Reason is one human sentence shown next to the suggestion.
	Reason string `json:"reason"`
}

// Input is what the suggestion is derived from.
type Input struct {
	Runner activity.Runner
	// SkillEffort is the skill's `metadata.steermi-effort` ("" = unset).
	SkillEffort Effort
	// LoopIteration is the run's round inside a flow loop (0 = not in one).
	LoopIteration int
	// PriorFailed: the run follows a failed run or a review that asked for
	// changes (retry, apply fixes).
	PriorFailed bool
}

// For returns the suggestion for in. Rules, in order:
//  1. the skill's declared effort sets the base (low → fast, high → deep);
//     unset: findings runs think harder (standard/high), others standard/medium;
//  2. a second or later loop round, or a run after a failure, escalates one
//     tier and sets effort high.
func For(in Input) Hint {
	h := Hint{Tier: Standard, Effort: Medium, Reason: "default for " + runnerName(in.Runner)}
	switch in.SkillEffort {
	case Low:
		h = Hint{Tier: Fast, Effort: Low, Reason: "the skill declares low effort"}
	case High:
		h = Hint{Tier: Deep, Effort: High, Reason: "the skill declares high effort"}
	case Medium:
		h.Reason = "the skill declares medium effort"
	default:
		if in.Runner == activity.Findings {
			h.Effort = High
			h.Reason = "findings skills read carefully"
		}
	}
	escalate := ""
	switch {
	case in.LoopIteration >= 2:
		escalate = "loop round " + strconv.Itoa(in.LoopIteration)
	case in.PriorFailed:
		escalate = "the previous run did not pass"
	}
	if escalate != "" {
		switch h.Tier {
		case Fast:
			h.Tier = Standard
		default:
			h.Tier = Deep
		}
		h.Effort = High
		h.Reason = escalate + "; " + h.Reason
	}
	return h
}

func runnerName(r activity.Runner) string {
	if r == "" {
		return "this run"
	}
	return string(r) + " skills"
}

// FromRun is For applied to a run: its runner, its loop round and whether its
// predecessor (nil = none) failed or was a findings run that asked for changes.
func FromRun(run activity.Run, skillEffort Effort, predecessor *activity.Run) Hint {
	in := Input{Runner: run.Runner, SkillEffort: skillEffort}
	if run.Flow != nil && run.Flow.Loop != nil {
		in.LoopIteration = run.Flow.Loop.Iteration
	}
	if predecessor != nil {
		in.PriorFailed = predecessor.State == activity.Failed || predecessor.Outcome() == activity.OutcomeChangesRequested
	}
	return For(in)
}
