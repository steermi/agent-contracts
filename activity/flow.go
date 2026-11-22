package activity

import (
	"cmp"
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
	// OutcomeFailed: the run failed, or a findings run outside a loop was
	// inconclusive (it assessed nothing a flow can act on).
	OutcomeFailed Outcome = "failed"
	// OutcomeExhausted: a quality loop stopped (iterations, budget, stall),
	// or a findings run inside a loop was inconclusive: the loop leaves
	// through its exhausted exit (LoopState.Stop names the reason).
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
	// Note is the approval node's note for the person the work is handed
	// to (flow.Node.Note). Display only.
	Note string `json:"note,omitempty"`
	// StopName is the display name of the approval node the hop ends at.
	StopName string `json:"stop_name,omitempty"`
	// Approver is who decides at the approval node (empty = owner) and
	// Decide where each decision leads (approved, changes_requested), pinned
	// like every other hop. An approval hop without Decide is a plain
	// hand-off: nobody decides there, the flow stops.
	Approver Approver            `json:"approver,omitempty"`
	Decide   map[Outcome]FlowHop `json:"decide,omitempty"`
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
	// Budget: USD the loop may spend. 0 = no loop limit; hosts still apply
	// their own cumulative limit (the run policy's MaxCost).
	Budget float64 `json:"budget,omitempty"`
	// StopBelow: findings below this severity do not block (another round,
	// stall detection, an approve); empty = High (Blocking).
	StopBelow Severity `json:"stop_below,omitempty"`
	// StallRounds: stop when this many consecutive rounds, the current one
	// included, end with the same blocking findings. 0 = off; 2..6. 1 is
	// invalid in a flow and acts as off on a pinned run. It governs findings
	// only: the same failed checks two rounds in a row, and a code step that
	// hands back the previous round's revision, always stop the loop
	// (LoopState.Stop).
	StallRounds int `json:"stall_rounds,omitempty"`
	// StallMatch decides when two rounds found "the same problem":
	// location (default when empty) compares severity and normalized
	// location and ignores wording; exact also compares wording
	// (FingerprintBy).
	StallMatch StallMatch `json:"stall_match,omitempty"`
	// Advance decides every hop between the loop's own steps (build →
	// review, review → build, failed-check rounds): auto starts them without
	// a person, ask (default when empty) waits.
	Advance LoopAdvance `json:"advance,omitempty"`
}

// Blocking is the severity from which a finding blocks the work: the
// loop's StopBelow, High when it has none. The zero policy (a run outside a
// loop) blocks from High too.
func (p LoopPolicy) Blocking() Severity { return cmp.Or(p.StopBelow, High) }

// blocks is the one "does this finding block" rule: its severity is at or
// above Blocking. It decides a loop review's approved outcome, what stall
// detection and the loop ledger compare, and which approve is refused.
// Dismissals do not matter here; callers drop them where they must.
func (p LoopPolicy) blocks(f Finding) bool { return f.Severity.AtOrAbove(p.Blocking()) }

// StallFingerprint identifies what a finished findings round left open, for
// stall detection: its blocking findings that no person dismissed, compared
// by StallMatch. "" when there are none, which never stalls. Lower findings
// and dismissed ones are noise that must not hide (or fake) a repeating
// problem.
func (p LoopPolicy) StallFingerprint(findings []Finding) string {
	return p.fingerprint(openFindings(findings))
}

// fingerprint compares the blocking findings by StallMatch, dismissed or
// not; "" when none blocks.
func (p LoopPolicy) fingerprint(findings []Finding) string {
	kept := slices.DeleteFunc(slices.Clone(findings), func(f Finding) bool { return !p.blocks(f) })
	if len(kept) == 0 {
		return ""
	}
	return FingerprintBy(kept, p.StallMatch)
}

// openFindings drops the findings a person dismissed.
func openFindings(findings []Finding) []Finding {
	return slices.DeleteFunc(slices.Clone(findings), func(f Finding) bool { return !f.open() })
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
	// Fingerprints has one entry per finished round, in order: what the
	// round left open (LoopPolicy.StallFingerprint, or CheckFingerprint for
	// a failed-check round), "" when nothing comparable.
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
	if len(r.Decisions) > 0 {
		digest := r.EvidenceDigest()
		for i := len(r.Decisions) - 1; i >= 0; i-- {
			if d := r.Decisions[i]; d.EvidenceDigest == digest && d.Kind == RequestChanges {
				return OutcomeChangesRequested
			}
		}
	}
	switch r.Runner {
	case Code:
		if !r.ChecksPassed() {
			return OutcomeRepeat
		}
	case Findings:
		switch r.Recommendation {
		case Approve:
			return OutcomeApproved
		case Inconclusive:
			// Nothing was assessed, so nothing is approved: a loop hands
			// the work to a person, a step outside a loop failed.
			if r.Flow != nil && r.Flow.Loop != nil {
				return OutcomeExhausted
			}
			return OutcomeFailed
		}
		// Dismissals never change the outcome (in any state): the hop is
		// decided by the findings as submitted. Dismissals only shape what
		// successors work from (kept findings) and what people see.
		if r.Flow != nil && r.Flow.Loop != nil && !slices.ContainsFunc(r.Findings, r.Flow.Loop.blocks) {
			// Findings that do not block stay on the run as follow-ups.
			return OutcomeApproved
		}
		return OutcomeChangesRequested
	}
	return OutcomeDone
}

// loopPolicy is the policy of the run's loop; the zero policy outside one.
func (r Run) loopPolicy() LoopPolicy {
	if r.Flow == nil || r.Flow.Loop == nil {
		return LoopPolicy{}
	}
	return r.Flow.Loop.LoopPolicy
}

// BlockingSeverity is the severity from which a finding contradicts an
// approval (LoopPolicy.Blocking of the run's loop: its threshold, High
// without one or outside a loop). Submitting approve with such a finding is
// refused; hosts may check first to tell the model why.
func (r Run) BlockingSeverity() Severity { return r.loopPolicy().Blocking() }

// ContradictsApprove reports whether c is a findings submission that
// recommends approve while carrying a finding at or above the run's
// blocking severity (BlockingSeverity): Apply refuses it, and runners may
// ask first to tell the model which rule it broke.
func (r Run) ContradictsApprove(c Command) bool {
	return c.Type == SubmitFindings && c.Recommendation == Approve && slices.ContainsFunc(c.Findings, r.loopPolicy().blocks)
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

// Fingerprint identifies a set of findings by severity, location and
// wording, independently of their order, ids and dismissals.
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
// limit, the budget or when the last StallRounds rounds (this one included)
// ended with the same blocking findings. Two stops need no setting and
// cannot be turned off, because another round would repeat the last one: a
// failed-check round that fails the same checks as the round right before
// it (alwaysStallRounds), and a code run that reproduced the previous
// round's revision, so reviewing it again cannot help. An inconclusive
// review in a loop has no hop of its own (Outcome exhausted): FlowNext
// sends it to the exhausted hop without asking Stop.
func (l LoopState) Stop(r Run, hop FlowHop) Blocker {
	switch {
	case hop.Back && l.Iteration >= l.MaxIterations:
		return BlockerRepairLimit
	case hop.Back && l.Budget > 0 && r.Cost-l.StartCost >= l.Budget:
		return BlockerBudget
	// The run's own findings count as submitted: a dismissal never changes
	// the hop of the run it is made on. Earlier rounds were recorded without
	// the findings people dismissed before the next round started.
	case hop.Back && r.Runner == Findings && l.repeats(l.fingerprint(r.Findings), l.StallRounds):
		return BlockerLoopStalled
	case hop.Back && r.Runner == Code && r.ChecksFailed() && l.repeats(CheckFingerprint(r.FailedChecks()), alwaysStallRounds):
		return BlockerLoopStalled
	case r.Runner == Code && hop.Runner == Findings && l.Iteration > 1 && l.Revision != "" && RevisionDigest(r.Outputs) == l.Revision:
		return BlockerLoopStalled
	}
	return ""
}

// alwaysStallRounds is the window of the failed-check stop every loop has:
// this round and the one right before it.
const alwaysStallRounds = 2

// repeats reports whether the current round's fingerprint closes a stall
// over rounds consecutive rounds (this one included): the rounds-1 rounds
// right before it left the same fingerprint. A round in between with
// another result (or none) starts the count again.
func (l LoopState) repeats(fingerprint string, rounds int) bool {
	before := rounds - 1
	if fingerprint == "" || before < 1 || len(l.Fingerprints) < before {
		return false
	}
	for _, f := range l.Fingerprints[len(l.Fingerprints)-before:] {
		if f != fingerprint {
			return false
		}
	}
	return true
}

// FlowNext resolves the hop the run's outcome leads to and, when a quality
// loop stops, the exhausted hop with the reason. A hop that ends at an
// approval node continues through the decisions people made there
// (StepDecisions on the run's current evidence): the result is then the hop
// the latest decision chose, with no stop. ok is false while the run has no
// outcome or its flow has no edge for it.
func (r Run) FlowNext() (hop FlowHop, stop Blocker, ok bool) {
	p := r.flowPath()
	return p.hop(), p.stop, p.ok()
}

// flowBase is the hop the run's own outcome leads to, before any step
// decision: the pinned hop of the outcome, or the loop's exhausted hop with
// the stop reason. An inconclusive review in a loop (OutcomeExhausted)
// leaves through the exhausted hop and needs no edge of its own.
func (r Run) flowBase() (hop FlowHop, stop Blocker, ok bool) {
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
	l := r.Flow.Loop
	if o == OutcomeExhausted && l != nil {
		return l.Exhausted, BlockerReviewInconclusive, true
	}
	hop, ok = r.Flow.Hops[o]
	if !ok {
		return FlowHop{}, "", false
	}
	if l != nil {
		if b := l.Stop(r, hop); b != "" {
			return l.Exhausted, b, true
		}
	}
	return hop, "", true
}

// MapHops returns a copy of the ref with f applied to every hop it pins:
// each outcome's hop, the loop's exhausted hop and, nested under any of
// them, the hop of each decision at an approval node (f sees a hop before
// its decision hops). Hosts resolve pinned hops (their Location) with it and
// never walk the hop tree themselves. f receives hops it may change freely;
// f's result's Decide map is replaced by the mapped copy.
func (f FlowRef) MapHops(fn func(FlowHop) FlowHop) FlowRef {
	var visit func(FlowHop) FlowHop
	visit = func(h FlowHop) FlowHop {
		h = fn(h)
		if len(h.Decide) > 0 {
			decide := make(map[Outcome]FlowHop, len(h.Decide))
			for o, next := range h.Decide {
				decide[o] = visit(next)
			}
			h.Decide = decide
		}
		return h
	}
	if f.Hops != nil {
		hops := make(map[Outcome]FlowHop, len(f.Hops))
		for o, h := range f.Hops {
			hops[o] = visit(h)
		}
		f.Hops = hops
	}
	if f.Loop != nil {
		loop := *f.Loop
		loop.Exhausted = visit(loop.Exhausted)
		f.Loop = &loop
	}
	return f
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

// canApplyFixes reports whether "one more round" may follow the
// changes-requested edge of a submitted review. Once the flow stands at (or
// passed) an approval node that decides, its decision is the only way on:
// one more round there is RequestStepChanges, recorded with its reason.
func (r Run) canApplyFixes() bool {
	_, rework := r.reworkHop()
	return rework && r.Runner == Findings && r.State == Submitted && !r.flowPath().reachedApproval()
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
	p := r.flowPath()
	hop := p.hop()
	// A person's decision at an approval node is an explicit start: the
	// hop it chose needs no auto edge.
	start := hop.Auto || p.decided() != nil
	return hop, p.ok() && p.stop == "" && start && hop.Runner != "" && r.StepMode != StepModeAsk && hop.Location == Cloud
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
