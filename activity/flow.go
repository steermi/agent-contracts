package activity

import (
	"slices"
	"strings"
)

// SkillRef pins the skill a run follows: the latest published version when
// the run was created (or the built-in skill of its runner, builtin.ID).
// The digest covers every file of the skill.
type SkillRef struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	VersionID string `json:"version_id"`
	Digest    string `json:"digest"`
	// Name is the pinned version's title (skill.Skill.Title), shown wherever
	// the run is named. Display only; never part of authorization.
	Name string `json:"name,omitempty"`
}

// Outcome classifies a finished run; it names the flow edge that follows.
type Outcome string

const (
	// OutcomeDone: an answer or code run submitted its outputs.
	OutcomeDone Outcome = "done"
	// OutcomeApproved: a findings run found nothing at or above the loop's
	// threshold (or recommended approval).
	OutcomeApproved Outcome = "approved"
	// OutcomeChangesRequested: a findings run or a person requested changes.
	OutcomeChangesRequested Outcome = "changes_requested"
	// OutcomeFailed: the run failed.
	OutcomeFailed Outcome = "failed"
	// OutcomeExhausted: a quality loop stopped (iterations, budget, stall).
	OutcomeExhausted Outcome = "exhausted"
	// OutcomeRepeat: the step must run again (code checks not passed). It
	// is not an edge.
	OutcomeRepeat Outcome = "repeat"
)

// Outcomes lists the edge outcomes in display order.
func Outcomes() []Outcome {
	return []Outcome{OutcomeDone, OutcomeApproved, OutcomeChangesRequested, OutcomeFailed, OutcomeExhausted}
}

// StatusTarget is a work item status a flow sets: a category, optionally
// narrowed to one team status.
type StatusTarget struct {
	Category string `json:"category"`
	StatusID string `json:"status_id,omitempty"`
}

// FlowHop is where an outcome leads, resolved from the pinned flow version
// when the run was created: the status nodes passed (applied on arrival),
// then the next agent step, or a stop.
type FlowHop struct {
	// Node, Name and Runner identify the agent step started; empty when the
	// hop ends. Name is the step's display name (its key when unnamed).
	Node   string `json:"node,omitempty"`
	Name   string `json:"name,omitempty"`
	Runner Runner `json:"runner,omitempty"`
	// Auto: the edge into Node starts it without asking a person.
	Auto bool `json:"auto,omitempty"`
	// Statuses are set when the hop is reached, in order.
	Statuses []StatusTarget `json:"statuses,omitempty"`
	// Stop names the end or approval node the hop ends at.
	Stop string `json:"stop,omitempty"`
	// Approval: the hop ends at a person's decision.
	Approval bool `json:"approval,omitempty"`
	// Back: the hop starts the next iteration of the run's loop.
	Back bool `json:"back,omitempty"`
}

// LoopPolicy bounds a quality loop.
type LoopPolicy struct {
	// MaxIterations: rounds of the loop body (1..6).
	MaxIterations int `json:"max_iterations"`
	// Budget: USD the loop may spend (0 = the run policy's MaxCost).
	Budget float64 `json:"budget,omitempty"`
	// StopBelow: findings below this severity do not start another round.
	StopBelow Severity `json:"stop_below,omitempty"`
	// StallRounds: stop when the same findings repeat this many times.
	StallRounds int `json:"stall_rounds,omitempty"`
}

// LoopState is a run's place in a quality loop.
type LoopState struct {
	Key string `json:"key"`
	LoopPolicy
	// Iteration counts rounds from 1.
	Iteration int `json:"iteration"`
	// StartCost is the chain's cost when the loop was entered.
	StartCost float64 `json:"start_cost,omitempty"`
	// Fingerprints of the findings of previous rounds.
	Fingerprints []string `json:"fingerprints,omitempty"`
	// Revision is the digest of the latest code outputs of the loop.
	Revision string `json:"revision,omitempty"`
	// Exhausted is where the loop exits when it stops.
	Exhausted FlowHop `json:"exhausted"`
}

// FlowRef places a run on a node of a pinned delivery flow version, with the
// hops of every outcome, so successor decisions never read live
// configuration.
type FlowRef struct {
	VersionID string              `json:"version_id"`
	Node      string              `json:"node"`
	Hops      map[Outcome]FlowHop `json:"hops,omitempty"`
	Loop      *LoopState          `json:"loop,omitempty"`
}

// Outcome classifies a submitted, completed or failed run; "" otherwise.
func (r Run) Outcome() Outcome {
	if r.IsSessionWorkflow() {
		return ""
	}
	if r.State == Failed {
		return OutcomeFailed
	}
	if r.State != Submitted && r.State != Completed {
		return ""
	}
	digest := r.EvidenceDigest()
	for i := len(r.Decisions) - 1; i >= 0; i-- {
		if d := r.Decisions[i]; d.EvidenceDigest == digest && d.Kind == RequestChanges {
			return OutcomeChangesRequested
		}
	}
	switch r.Runner {
	case Code:
		if !r.ChecksPassed() {
			return OutcomeRepeat
		}
	case Findings:
		if r.Recommendation != ChangesRequested {
			return OutcomeApproved
		}
		if r.Flow != nil && r.Flow.Loop != nil && r.Flow.Loop.StopBelow != "" && !AnyAtOrAbove(r.Findings, r.Flow.Loop.StopBelow) {
			// Findings below the threshold stay on the run as follow-ups.
			return OutcomeApproved
		}
		return OutcomeChangesRequested
	}
	return OutcomeDone
}

var severityRank = map[Severity]int{Low: 1, Medium: 2, High: 3, Critical: 4}

// ValidSeverity reports whether s is a known severity.
func ValidSeverity(s Severity) bool { return severityRank[s] > 0 }

// AtOrAbove reports whether s is at least min.
func (s Severity) AtOrAbove(min Severity) bool { return severityRank[s] >= severityRank[min] }

// AnyAtOrAbove reports whether an open finding is at least min.
func AnyAtOrAbove(findings []Finding, min Severity) bool {
	return slices.ContainsFunc(findings, func(f Finding) bool { return f.DismissalReason == "" && f.Severity.AtOrAbove(min) })
}

// Fingerprint identifies a set of open findings independently of their
// order and ids, so a loop can tell when a round changed nothing.
func Fingerprint(findings []Finding) string {
	keys := make([]string, 0, len(findings))
	for _, f := range findings {
		if f.DismissalReason != "" {
			continue
		}
		keys = append(keys, string(f.Severity)+"|"+strings.ToLower(strings.TrimSpace(f.Location))+"|"+strings.ToLower(strings.Join(strings.Fields(f.Description), " ")))
	}
	slices.Sort(keys)
	return Digest(keys)
}

// RevisionDigest identifies a run's repository outputs.
func RevisionDigest(outputs []OutputRef) string {
	var refs []string
	for _, o := range outputs {
		if o.Kind == RefRepository {
			refs = append(refs, o.RepositoryID+"@"+o.CommitSHA)
		}
	}
	if len(refs) == 0 {
		return ""
	}
	slices.Sort(refs)
	return Digest(refs)
}

// Stop reports why the loop may not follow hop from r ("" = it may). This is
// the single stop table of quality loops: a hop back to the loop's first step
// stops at the iteration limit, the budget or repeated findings; a code run that
// reproduced the previous round's revision changed nothing, so reviewing it
// again cannot help.
func (l LoopState) Stop(r Run, hop FlowHop) Blocker {
	switch {
	case hop.Back && l.Iteration >= l.MaxIterations:
		return BlockerRepairLimit
	case hop.Back && l.Budget > 0 && r.Cost-l.StartCost >= l.Budget:
		return BlockerBudget
	case hop.Back && r.Runner == Findings && l.StallRounds > 1 && len(r.Findings) > 0 && countOf(l.Fingerprints, Fingerprint(r.Findings))+1 >= l.StallRounds:
		return BlockerLoopStalled
	case r.Runner == Code && hop.Runner == Findings && l.Iteration > 1 && l.Revision != "" && RevisionDigest(r.Outputs) == l.Revision:
		return BlockerLoopStalled
	}
	return ""
}

func countOf(list []string, v string) int {
	n := 0
	for _, s := range list {
		if s == v {
			n++
		}
	}
	return n
}

// FlowNext resolves the hop the run's outcome leads to and, when a quality
// loop stops, the exhausted hop with the reason. ok is false while the run
// has no outcome or its flow has no edge for it.
func (r Run) FlowNext() (hop FlowHop, stop Blocker, ok bool) {
	if r.Flow == nil {
		return FlowHop{}, "", false
	}
	o := r.Outcome()
	switch o {
	case "":
		return FlowHop{}, "", false
	case OutcomeRepeat:
		return FlowHop{Node: r.Flow.Node, Name: r.SkillName(), Runner: r.Runner}, "", true
	}
	hop, ok = r.Flow.Hops[o]
	if !ok {
		return FlowHop{}, "", false
	}
	if l := r.Flow.Loop; l != nil {
		if b := l.Stop(r, hop); b != "" {
			return l.Exhausted, b, true
		}
	}
	return hop, "", true
}

// reworkHop is the hop Apply fixes follows: the changes-requested edge. A
// person may take it past the loop's stops ("One more round"); the policy's
// MaxRepairRounds still bounds it.
func (r Run) reworkHop() (FlowHop, bool) {
	if r.Flow == nil {
		return FlowHop{}, false
	}
	hop, ok := r.Flow.Hops[OutcomeChangesRequested]
	return hop, ok && hop.Runner != ""
}

// NextRunner is the runner a Next command starts, "" when none. The pinned
// delivery flow is the only source of successors: a run off a flow has none.
func (r Run) NextRunner() Runner {
	hop, _, ok := r.FlowNext()
	if !ok {
		return ""
	}
	return hop.Runner
}

// SuccessorNode names the flow node a retry/next/apply_fixes command starts;
// "" when the run has no flow.
func (r Run) SuccessorNode(t CommandType) string {
	if r.Flow == nil {
		return ""
	}
	switch t {
	case Retry, Refine:
		return r.Flow.Node
	case ApplyFixes:
		hop, _ := r.reworkHop()
		return hop.Node
	case Next:
		hop, _, _ := r.FlowNext()
		return hop.Node
	}
	return ""
}

// AutoHop is the hop the run's outcome follows without a person: an edge set
// to auto into an agent step that the run's quality loop did not stop, on a
// cloud run (local runs never auto-chain). It is the single eligibility check
// for automatic advances: hosts record and follow them with it and the
// PurposeFlow guard admits Next only when it holds. Whether the organization
// allows auto edges at all is the host's policy.
func (r Run) AutoHop() (FlowHop, bool) {
	hop, stop, ok := r.FlowNext()
	return hop, ok && stop == "" && hop.Auto && hop.Runner != "" && r.Location == Cloud
}

// flowGuard admits the flow advancer: only Next, only for the run's owner,
// only while AutoHop holds.
func (r Run) flowGuard(a Actor, t CommandType) error {
	if t != Next || a.UserID != r.OwnerID {
		return ErrForbidden
	}
	if r.Blocker != "" {
		return ErrStale
	}
	if _, ok := r.AutoHop(); !ok {
		return ErrConflict
	}
	return nil
}

// SkillName names the run for people: its pinned skill's title. This is the
// only fallback to the runner's label (a run without a named skill); hosts
// and clients show this value and never derive a name themselves.
func (r Run) SkillName() string {
	if r.Skill != nil && r.Skill.Name != "" {
		return r.Skill.Name
	}
	return r.Runner.Label()
}
