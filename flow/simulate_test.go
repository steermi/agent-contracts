package flow_test

import (
	"fmt"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/flow"
)

// simulate drives a resolved flow on scripted outcomes through the same
// Resolved.RefVia and activity.Run.FlowNext as live runs, so tests check the
// runtime's placement and stop table end to end. It follows Next only (no
// approval decisions). Test helper only: no host simulates flows.

// sameAsBefore scripts a step that changed nothing since its previous
// visit: a review requesting the same changes again, a build handing back
// the same revision.
const sameAsBefore = "same"

// checkFailed scripts a code step whose first required check failed.
const checkFailed = "check_failed"

// script lists, per agent node, the outcome of each visit (an edge outcome,
// sameAsBefore or checkFailed); the last entry repeats. Unscripted steps
// succeed (done, or approved for findings steps).
type script map[string][]string

type simStep struct {
	Node      string
	Outcome   activity.Outcome
	Iteration int
	Cost      float64
	Statuses  []activity.StatusTarget
	Stop      activity.Blocker
	Auto      bool
}

const (
	simEnd      = "end"
	simApproval = "approval"
	simOpen     = "open"
	simLimit    = "limit"
)

type simResult struct {
	Steps   []simStep
	Ends    string
	EndKind string
	Cost    float64
}

func simulate(g flow.Resolved, sc script, from string, perRun float64) simResult {
	if from == "" {
		from = g.Start
	}
	out := simResult{}
	hop := g.Hop(flow.Edge{To: from})
	visits := map[string]int{}
	var prev *activity.Run
	revision := 0
	findings := map[string]string{} // node → its latest finding
	for range 40 {
		if hop.Node == "" {
			out.Ends, out.EndKind = hop.Stop, endKind(g, hop)
			return out
		}
		ref, ok := g.RefVia("simulation", hop.Node, prev, activity.Next)
		if !ok {
			out.EndKind = simOpen
			return out
		}
		out.Cost += perRun
		visit := visits[hop.Node]
		visits[hop.Node]++
		scripted := scriptedOutcome(sc[hop.Node], visit, hop.Runner)
		if hop.Runner == activity.Code && scripted != sameAsBefore {
			revision++
		}
		if scripted != sameAsBefore || findings[hop.Node] == "" {
			findings[hop.Node] = fmt.Sprintf("finding of visit %d", visit)
		}
		var checks []string
		if n, ok := g.Node(hop.Node); ok {
			checks = n.Requires.Checks
		}
		r := simulatedRun(hop.Runner, scripted, findings[hop.Node], revision, out.Cost, &ref, checks)
		next, stop, ok := r.FlowNext()
		step := simStep{Node: hop.Node, Outcome: r.Outcome(), Cost: out.Cost, Stop: stop}
		if ref.Loop != nil {
			step.Iteration = ref.Loop.Iteration
		}
		if ok {
			step.Statuses, step.Auto = next.Statuses, next.Auto && next.Node != ""
		}
		out.Steps = append(out.Steps, step)
		if !ok {
			out.EndKind = simOpen
			return out
		}
		prev, hop = &r, next
	}
	out.EndKind = simLimit
	return out
}

func endKind(g flow.Resolved, hop activity.FlowHop) string {
	if hop.Approval {
		return simApproval
	}
	if n, ok := g.Node(hop.Stop); ok && n.Type == flow.NodeEnd {
		return simEnd
	}
	return simOpen
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

// simulatedRun is a finished run with the given outcome: a critical finding
// requests changes, revision numbers the code output.
func simulatedRun(runner activity.Runner, outcome, finding string, revision int, cost float64, ref *activity.FlowRef, checks []string) activity.Run {
	r := activity.Run{ID: "simulated-" + ref.Node, Runner: runner, State: activity.Submitted, Cost: cost, Flow: ref}
	if runner == activity.Code {
		sha := fmt.Sprintf("%040x", revision)
		r.Outputs = []activity.OutputRef{{Kind: activity.RefRepository, ResourceID: "repo", RepositoryID: "repo", CommitSHA: sha}}
		r.Policy.RequiredChecks = checks
		for i, name := range checks {
			status, jobs := activity.CheckSuccess, []string(nil)
			if outcome == checkFailed && i == 0 {
				status, jobs = activity.CheckFailed, []string{"test"}
			}
			r.Checks = append(r.Checks, activity.Check{RepositoryID: "repo", CommitSHA: sha, Name: name, ProviderRunID: "sim", Attempt: 1,
				Status: status, Provenance: activity.ProviderVerified, FailedJobs: jobs})
		}
	}
	switch activity.Outcome(outcome) {
	case activity.OutcomeFailed:
		r.State = activity.Failed
	case activity.OutcomeApproved:
		r.Recommendation = activity.Approve
	case activity.OutcomeChangesRequested, sameAsBefore:
		if runner == activity.Findings {
			r.Recommendation = activity.ChangesRequested
			// Each new finding is somewhere else, so only sameAsBefore
			// repeats a problem under either stall match.
			r.Findings = []activity.Finding{{ID: "f", Severity: activity.Critical, Location: finding, Description: finding}}
		} else if outcome != sameAsBefore {
			r.Decisions = []activity.Decision{{Kind: activity.RequestChanges, EvidenceDigest: r.EvidenceDigest()}}
		}
	}
	return r
}

func simNodes(r simResult) []string {
	out := []string{}
	for _, s := range r.Steps {
		out = append(out, s.Node)
	}
	return out
}
