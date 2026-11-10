package flow

import (
	"fmt"

	"github.com/steermi/agent-contracts/activity"
)

// Simulation runs a flow on scripted outcomes through the same Resolved.Ref and
// activity.Run.FlowNext as live runs, so the designer's simulator can never
// disagree with the runtime's stop table. No LLM, no writes.

// SameAsBefore scripts a step that changed nothing since its previous visit:
// a findings run requesting the same changes again, a code run producing the same
// revision.
const SameAsBefore = "same"

// MaxSimulatedSteps bounds a simulation.
const MaxSimulatedSteps = 40

// Script lists, per agent node, the outcome of each visit (an edge outcome or
// SameAsBefore); the last entry repeats. Unscripted steps succeed (done,
// approved for reviews).
type Script map[string][]string

// SimStep is one simulated run.
type SimStep struct {
	Node      string           `json:"node"`
	Outcome   activity.Outcome `json:"outcome"`
	Iteration int              `json:"iteration,omitempty"`
	// Cost is the chain's spend after the run.
	Cost float64 `json:"cost"`
	// Statuses are the status nodes passed after the run.
	Statuses []activity.StatusTarget `json:"statuses,omitempty"`
	// Stop is why a quality loop stopped after the run.
	Stop activity.Blocker `json:"stop,omitempty"`
	// Auto: the next step starts without asking.
	Auto bool `json:"auto,omitempty"`
}

// Simulation end kinds.
const (
	SimEnd      = "end"
	SimApproval = "approval"
	SimOpen     = "open"
	SimLimit    = "limit"
)

// SimResult is a simulated path.
type SimResult struct {
	Steps   []SimStep `json:"steps"`
	Ends    string    `json:"ends,omitempty"`
	EndKind string    `json:"end_kind"`
	Cost    float64   `json:"cost"`
}

// Simulate follows the flow from a node (the start when empty), every run
// costing perRun. Validate the graph first: an invalid graph simulates
// whatever its edges say.
func (g Resolved) Simulate(script Script, from string, perRun float64) SimResult {
	if from == "" {
		from = g.Start
	}
	out := SimResult{Steps: []SimStep{}}
	hop := g.Hop(Edge{To: from})
	visits := map[string]int{}
	var prev *activity.Run
	revision := 0
	findings := map[string]string{} // node → its latest finding
	for range MaxSimulatedSteps {
		if hop.Node == "" {
			out.Ends, out.EndKind = hop.Stop, g.endKind(hop)
			return out
		}
		ref, ok := g.Ref("simulation", hop.Node, prev)
		if !ok {
			out.EndKind = SimOpen
			return out
		}
		out.Cost += perRun
		visit := visits[hop.Node]
		visits[hop.Node]++
		scripted := scriptedOutcome(script[hop.Node], visit, hop.Runner)
		if hop.Runner == activity.Code && scripted != SameAsBefore {
			revision++
		}
		if scripted != SameAsBefore || findings[hop.Node] == "" {
			findings[hop.Node] = fmt.Sprintf("finding of visit %d", visit)
		}
		r := simulatedRun(hop.Runner, scripted, findings[hop.Node], revision, out.Cost, &ref)
		next, stop, ok := r.FlowNext()
		step := SimStep{Node: hop.Node, Outcome: r.Outcome(), Cost: out.Cost, Stop: stop}
		if ref.Loop != nil {
			step.Iteration = ref.Loop.Iteration
		}
		if ok {
			step.Statuses, step.Auto = next.Statuses, next.Auto && next.Node != ""
		}
		out.Steps = append(out.Steps, step)
		if !ok {
			out.EndKind = SimOpen
			return out
		}
		prev, hop = &r, next
	}
	out.EndKind = SimLimit
	return out
}

func (g Resolved) endKind(hop activity.FlowHop) string {
	if hop.Approval {
		return SimApproval
	}
	if n, ok := g.Node(hop.Stop); ok && n.Type == NodeEnd {
		return SimEnd
	}
	return SimOpen
}

func scriptedOutcome(list []string, visit int, runner activity.Runner) string {
	if len(list) == 0 {
		if runner == activity.Findings {
			return string(activity.OutcomeApproved)
		}
		return string(activity.OutcomeDone)
	}
	return list[min(visit, len(list)-1)]
}

// simulatedRun is a finished run with the given outcome: a finding at the
// highest severity requests changes, revision numbers the code output.
func simulatedRun(runner activity.Runner, outcome, finding string, revision int, cost float64, ref *activity.FlowRef) activity.Run {
	r := activity.Run{ID: "simulated-" + ref.Node, Runner: runner, State: activity.Submitted, Cost: cost, Flow: ref}
	if runner == activity.Code {
		r.Outputs = []activity.OutputRef{{Kind: activity.RefRepository, ResourceID: "repo", RepositoryID: "repo", CommitSHA: fmt.Sprintf("%040x", revision)}}
	}
	switch activity.Outcome(outcome) {
	case activity.OutcomeFailed:
		r.State = activity.Failed
	case activity.OutcomeApproved:
		r.Recommendation = activity.Approve
	case activity.OutcomeChangesRequested, SameAsBefore:
		if runner == activity.Findings {
			r.Recommendation = activity.ChangesRequested
			r.Findings = []activity.Finding{{ID: "f", Severity: activity.Critical, Location: ref.Node, Description: finding}}
		} else if outcome != SameAsBefore {
			r.Decisions = []activity.Decision{{Kind: activity.RequestChanges, EvidenceDigest: r.EvidenceDigest()}}
		}
	}
	return r
}
