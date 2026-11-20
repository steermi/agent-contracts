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
	// is not an edge. Inside a quality loop a code step with required checks
	// pins its repeat hop (FlowRef.Hops[OutcomeRepeat], Back): a failed
	// check sends the round back to the step and counts as a round.
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
	// InLoop: the hop leads to a step of the same quality loop, so the
	// loop's advance decided Auto and its own stops bound it. Hops pinned
	// before this field read false (treated as outside a loop).
	InLoop bool `json:"in_loop,omitempty"`
	// Rule is the target step's location rule as the flow version states it
	// (flow.Node.Location; empty = RuleInherit).
	Rule LocationRule `json:"location_rule,omitempty"`
	// Location is where the hop's step runs, resolved by the host from Rule
	// (and its own defaults) when it places the run and pins the hop. Empty
	// on hops the host did not resolve (stops, older pins): such a hop is
	// never automatic.
	Location Location `json:"location,omitempty"`
}

// LocationRule is a flow step's location setting: inherit (the default when
// empty), or always cloud or local. Hosts resolve inherit to a Location.
type LocationRule string

const (
	RuleInherit LocationRule = "inherit"
	RuleCloud   LocationRule = "cloud"
	RuleLocal   LocationRule = "local"
)

// Valid reports whether l is empty (= inherit) or a known rule.
func (l LocationRule) Valid() bool {
	return l == "" || l == RuleInherit || l == RuleCloud || l == RuleLocal
}

// Fixed returns the location a cloud or local rule names; ok is false for
// inherit, which the host resolves.
func (l LocationRule) Fixed() (Location, bool) {
	switch l {
	case RuleCloud:
		return Cloud, true
	case RuleLocal:
		return Local, true
	}
	return "", false
}

// LoopAdvance is how a quality loop moves between its own steps: auto
// starts the next step (or round) without a person, ask (the default when
// empty) waits for one. Edges outside the loop keep their own start.
type LoopAdvance string

const (
	LoopAdvanceAuto LoopAdvance = "auto"
	LoopAdvanceAsk  LoopAdvance = "ask"
)

// Valid reports whether a is empty (= ask) or a known mode.
func (a LoopAdvance) Valid() bool { return a == "" || a == LoopAdvanceAuto || a == LoopAdvanceAsk }

// LoopPolicy bounds a quality loop.
type LoopPolicy struct {
	// MaxIterations: rounds of the loop body (1..6).
	MaxIterations int `json:"max_iterations"`
	// Budget: USD the loop may spend (0 = the run policy's MaxCost).
	Budget float64 `json:"budget,omitempty"`
	// StopBelow: findings below this severity do not start another round.
	StopBelow Severity `json:"stop_below,omitempty"`
	// StallRounds: stop when the same findings (or the same failed checks)
	// repeat this many times.
	StallRounds int `json:"stall_rounds,omitempty"`
	// StallMatch decides when two rounds found "the same problem": exact
	// (default when empty) compares severity, location and wording;
	// location ignores wording (FingerprintBy).
	StallMatch StallMatch `json:"stall_match,omitempty"`
	// Advance decides every hop between the loop's own steps (build →
	// review, review → build, failed-check rounds): auto starts them without
	// a person, ask (default when empty) waits.
	Advance LoopAdvance `json:"advance,omitempty"`
}

// AutoAdvance reports whether the loop starts its own steps by itself.
func (p LoopPolicy) AutoAdvance() bool { return p.Advance == LoopAdvanceAuto }

// StallMatch is how a loop compares findings across rounds.
type StallMatch string

const (
	StallExact    StallMatch = "exact"
	StallLocation StallMatch = "location"
)

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
	// Ledger is what earlier rounds tried and saw (loop.go), carried to
	// every successor in the loop.
	Ledger []LoopRound `json:"ledger,omitempty"`
}

// FlowRef places a run on a node of a pinned delivery flow version, with the
// hops of every outcome, so successor decisions never read live
// configuration.
type FlowRef struct {
	VersionID string              `json:"version_id"`
	Node      string              `json:"node"`
	Hops      map[Outcome]FlowHop `json:"hops,omitempty"`
	Loop      *LoopState          `json:"loop,omitempty"`
	// Rule and Arm attribute the chain to the host's flow assignment rule
	// that chose this flow when its first run was placed (Rule: the host's
	// rule id; Arm: the chosen arm of a split rule, empty for other kinds
	// of rule). Both are empty when no rule chose the flow. Reporting only:
	// they never decide successors or authorization, and successors on the
	// same version carry them (flow.Resolved.Ref).
	Rule string `json:"rule,omitempty"`
	Arm  string `json:"arm,omitempty"`
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
		// Dismissals never change the outcome (in any state): the hop is
		// decided by the findings as submitted. Dismissals only shape what
		// successors work from (kept findings) and what people see.
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

// AnyAtOrAbove reports whether a finding is at least min. Dismissed findings
// count: a dismissal never changes a run's outcome.
func AnyAtOrAbove(findings []Finding, min Severity) bool {
	return slices.ContainsFunc(findings, func(f Finding) bool { return f.Severity.AtOrAbove(min) })
}

// Fingerprint identifies a set of findings independently of their order,
// ids and dismissals, so a loop can tell when a round changed nothing.
func Fingerprint(findings []Finding) string {
	keys := make([]string, 0, len(findings))
	for _, f := range findings {
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
// (or a failed-check round, which is pinned as Back) stops at the iteration
// limit, the budget or repeated findings or failed checks; a code run that
// reproduced the previous round's revision changed nothing, so reviewing it
// again cannot help.
func (l LoopState) Stop(r Run, hop FlowHop) Blocker {
	switch {
	case hop.Back && l.Iteration >= l.MaxIterations:
		return BlockerRepairLimit
	case hop.Back && l.Budget > 0 && r.Cost-l.StartCost >= l.Budget:
		return BlockerBudget
	case hop.Back && r.Runner == Findings && l.StallRounds > 1 && len(r.Findings) > 0 && countOf(l.Fingerprints, FingerprintBy(r.Findings, l.StallMatch))+1 >= l.StallRounds:
		return BlockerLoopStalled
	case hop.Back && r.Runner == Code && l.StallRounds > 1 && r.ChecksFailed() && countOf(l.Fingerprints, CheckFingerprint(r.FailedChecks()))+1 >= l.StallRounds:
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
		_, has := r.Flow.Hops[OutcomeRepeat]
		if !has {
			return FlowHop{Node: r.Flow.Node, Name: r.SkillName(), Runner: r.Runner}, "", true
		}
		// A failed-check round of a loop: only a failed check starts it;
		// pending or missing checks wait and spend no round.
		if !r.ChecksFailed() {
			return FlowHop{}, "", false
		}
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

// HasRepeatHop reports whether the run pins a failed-check round (a code
// step with required checks inside a quality loop).
func (f *FlowRef) HasRepeatHop() bool {
	if f == nil {
		return false
	}
	_, ok := f.Hops[OutcomeRepeat]
	return ok
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

// AutoHop is the hop the run's outcome follows without a person: an auto hop
// (an auto edge, or a hop inside a loop set to advance automatically) into
// an agent step that the run's quality loop did not stop, pinned to run in
// the cloud, while the run's step mode is not ask. The run's own location
// does not matter: a local Build continues into a cloud Review; a hop pinned
// local (or not resolved) always waits for a developer. It is the single
// eligibility check for automatic advances: hosts record and follow them
// with it and the PurposeFlow guard admits Next only when it holds. Whether
// the organization allows auto edges outside loops is the host's policy.
func (r Run) AutoHop() (FlowHop, bool) {
	hop, stop, ok := r.FlowNext()
	return hop, ok && stop == "" && hop.Auto && hop.Runner != "" && r.StepMode != StepModeAsk && hop.Location == Cloud
}

// AdvancePausedReason is why the host did not start a hop AutoHop admitted
// (the hop stays for a person): the owner left, the project moved to
// another team, the organization does not allow auto edges outside loops,
// or no model can be called in the cloud (no provider key, or the AI policy
// forbids the route).
type AdvancePausedReason string

const (
	PausedOwnerInactive   AdvancePausedReason = "owner_inactive"
	PausedProjectMoved    AdvancePausedReason = "project_moved"
	PausedAutoNotAllowed  AdvancePausedReason = "auto_not_allowed"
	PausedNoProviderKey   AdvancePausedReason = "no_provider_key"
	PausedRouteNotAllowed AdvancePausedReason = "route_not_allowed"
)

// Valid reports whether p is a known reason.
func (p AdvancePausedReason) Valid() bool {
	switch p {
	case PausedOwnerInactive, PausedProjectMoved, PausedAutoNotAllowed, PausedNoProviderKey, PausedRouteNotAllowed:
		return true
	}
	return false
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
