package flow

import (
	"cmp"
	"slices"

	"github.com/steermi/agent-contracts/activity"
)

// Quality loops: finding a step's loop, validating loop policies and placing
// runs on a version with their loop state carried over.

// LoopOf returns the loop whose body holds key.
func (g Graph) LoopOf(key string) (Node, bool) {
	for _, n := range g.Nodes {
		if n.Type == NodeLoop && slices.Contains(n.Body, key) {
			return n, true
		}
	}
	return Node{}, false
}

func validateLoop(n Node, keys map[string]Node, member map[string]string, add adder) {
	if len(n.Body) == 0 || len(n.Body) > MaxLoopBody {
		add(n.Key, "body", "a loop holds 1 to %d steps", MaxLoopBody)
	}
	for _, b := range n.Body {
		m, ok := keys[b]
		switch {
		case !ok:
			add(n.Key, "body", "unknown step %q", b)
		case m.Type != NodeAgent:
			add(n.Key, "body", "only agent steps go inside a loop")
		case member[b] != "":
			add(n.Key, "body", "%q is already inside loop %q", b, member[b])
		}
		member[b] = n.Key
	}
	p := n.Loop
	if p == nil {
		add(n.Key, "loop", "a loop needs its stop conditions")
		return
	}
	if p.MaxIterations < 1 || p.MaxIterations > MaxLoopIterations {
		add(n.Key, "loop", "max iterations is 1 to %d", MaxLoopIterations)
	}
	if p.Budget < 0 {
		add(n.Key, "loop", "the budget cannot be negative")
	}
	if p.StopBelow != "" && !activity.ValidSeverity(p.StopBelow) {
		add(n.Key, "loop", "stop below must be low, medium, high or critical")
	}
	if p.StallRounds != 0 && (p.StallRounds < 2 || p.StallRounds > MaxLoopIterations) {
		add(n.Key, "loop", "stall rounds is 0 (off) or 2 to %d", MaxLoopIterations)
	}
	switch p.StallMatch {
	case "", activity.StallExact, activity.StallLocation:
	default:
		add(n.Key, "loop", "same problem is exact or location")
	}
	if !p.Advance.Valid() {
		add(n.Key, "loop", "advance is ask or auto")
	}
}

// innerLoop is the loop an edge stays inside: both its ends are steps of
// that loop. Such an edge follows the loop's advance (never its own start)
// and is bounded by the loop's stops; this is the one "same loop" rule.
// Checking the edge's own ends is enough: a loop body holds only agent
// steps, an edge into a body step must come from the same loop, and a
// path step → status → own loop is a cycle Validate refuses
// (TestNoStatusBetweenStepsOfOneLoop), so no status node sits between two
// steps of one loop.
func (g Graph) innerLoop(e Edge) (Node, bool) {
	from, ok := g.LoopOf(e.From)
	if !ok || !slices.Contains(from.Body, e.To) {
		return Node{}, false
	}
	return from, true
}

func isEntry(loop Node, key string) bool { return len(loop.Body) > 0 && loop.Body[0] == key }

// Ref places a run on an agent node of the version and pins its hops and
// loop state. from is the run the new one follows (nil for a new chain):
// inside the same loop its state carries over, a back edge starts the next
// iteration and records the round's findings. A follow-up on the same
// version carries the chain's rule attribution (FlowRef.Rule, Arm). Ref
// never counts a failed-check round; hosts placing a successor use RefVia.
func (g Resolved) Ref(versionID, key string, from *activity.Run) (activity.FlowRef, bool) {
	return g.RefVia(versionID, key, from, "")
}

// RefVia is Ref for a successor started by command via (Next, ApplyFixes,
// Retry, Refine; "" when unknown). Inside the same loop the step of from is
// added to the round's ledger (not for Retry: a failed run tried nothing),
// and a Next over from's pinned failed-check hop counts as a round: the
// iteration grows and the failed checks' fingerprint is recorded. Retry and
// Refine restart the same step and never count.
func (g Resolved) RefVia(versionID, key string, from *activity.Run, via activity.CommandType) (activity.FlowRef, bool) {
	n, ok := g.Node(key)
	if !ok || n.Type != NodeAgent {
		return activity.FlowRef{}, false
	}
	ref := activity.FlowRef{VersionID: versionID, Node: key, Hops: map[activity.Outcome]activity.FlowHop{}}
	if from != nil && from.Flow != nil && from.Flow.VersionID == versionID {
		ref.Rule, ref.Arm = from.Flow.Rule, from.Flow.Arm
	}
	for _, o := range g.Outcomes(n) {
		if e, ok := g.Next(key, o); ok {
			ref.Hops[o] = g.Hop(e)
		}
	}
	loop, inLoop := g.LoopOf(key)
	if !inLoop {
		return ref, true
	}
	if loop.Loop != nil && g.Runner(key) == activity.Code && len(n.Requires.Checks) > 0 {
		// A failed required check sends the round back to this step.
		ref.Hops[activity.OutcomeRepeat] = activity.FlowHop{Node: key, Name: cmp.Or(n.Name, n.Key), Runner: activity.Code,
			Back: true, InLoop: true, Auto: loop.Loop.AutoAdvance(), Rule: n.Location}
	}
	if from != nil && from.Flow != nil && from.Flow.Loop != nil && from.Flow.Loop.Key == loop.Key && from.Flow.VersionID == versionID {
		state := *from.Flow.Loop
		state.Fingerprints = slices.Clone(state.Fingerprints)
		if via != activity.Retry {
			state.Ledger = activity.LedgerWith(state.Ledger, state.Iteration, activity.LoopStepOf(*from))
		}
		switch {
		// Back to the loop's first step: from another step of the loop, or
		// (Next only) through the loop node again on a person's decision at
		// an approval node ("changes requested" after the loop stopped).
		// Retry and Refine restart the same step and never count.
		case isEntry(loop, key) && (from.Flow.Node != key || via == activity.Next && hopDecided(from)):
			// One entry per round, so the stall check reads consecutive
			// rounds: "" when the round left nothing comparable.
			state.Iteration++
			fingerprint := ""
			if from.Runner == activity.Findings {
				fingerprint = state.StallFingerprint(from.Findings)
			}
			state.Fingerprints = append(state.Fingerprints, fingerprint)
		case via == activity.Next && from.Flow.Node == key && from.Flow.HasRepeatHop() && from.Outcome() == activity.OutcomeRepeat && from.ChecksFailed():
			state.Iteration++
			state.Fingerprints = append(state.Fingerprints, activity.CheckFingerprint(from.FailedChecks()))
		}
		if from.Runner == activity.Code {
			if rev := activity.RevisionDigest(from.Outputs); rev != "" {
				state.Revision = rev
			}
		}
		ref.Loop = &state
		return ref, true
	}
	// A valid graph gives every loop its policy and an exhausted exit; a run
	// is never placed on a loop without them.
	exit, ok := g.Next(loop.Key, activity.OutcomeExhausted)
	if loop.Loop == nil || !ok {
		return activity.FlowRef{}, false
	}
	state := activity.LoopState{Key: loop.Key, LoopPolicy: *loop.Loop, Iteration: 1, Exhausted: g.Hop(exit)}
	if from != nil {
		state.StartCost = from.Cost
	}
	ref.Loop = &state
	return ref, true
}

// hopDecided reports whether the hop r's FlowNext returns was chosen by a
// person's decision at an approval node (its last flow stage is decided).
func hopDecided(r *activity.Run) bool {
	stages := r.FlowStages()
	return len(stages) > 0 && stages[len(stages)-1].Decision != nil
}
