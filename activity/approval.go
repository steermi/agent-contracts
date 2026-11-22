package activity

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// Approval nodes that decide: a hop that ends at an approval node with
// decisions (FlowHop.Decide) waits for a person's ApproveStep or
// RequestStepChanges, recorded as a StepDecision, and the flow continues on
// the hop that decision chose. Everything about where a run's flow stands
// after its decisions is derived here, from one walk (flowPath).

// Approver is who may decide at an approval node: the run's owner (the
// default when empty), a manager of the team that owns the run's project, or
// a reviewer (the run policy's reviewers; without any, a project writer
// other than the run's owner). Organization owners and admins always may.
type Approver string

const (
	ApproverOwner        Approver = "owner"
	ApproverTeamManagers Approver = "team_managers"
	ApproverReviewers    Approver = "reviewers"
)

// Valid reports whether p is empty (= owner) or a known approver.
func (p Approver) Valid() bool {
	return p == "" || p == ApproverOwner || p == ApproverTeamManagers || p == ApproverReviewers
}

// OrDefault is the effective approver: owner when unset.
func (p Approver) OrDefault() Approver { return cmp.Or(p, ApproverOwner) }

// StepDecision is a person's decision at an approval node the run's flow
// stopped at: evidence on the run. It binds to the evidence it was made on
// (like Decision), so a decision never follows a run whose evidence, and
// with it possibly its outcome, changed afterwards.
type StepDecision struct {
	// Node is the approval node's key; Outcome approved or changes_requested.
	Node    string  `json:"node"`
	Outcome Outcome `json:"outcome"`
	UserID  string  `json:"user_id"`
	// Reason is required for changes_requested: it reaches the step the
	// decision leads to as its Feedback.
	Reason         string    `json:"reason,omitempty"`
	EvidenceDigest string    `json:"evidence_digest"`
	At             time.Time `json:"at"`
}

// decides reports whether people decide at the hop's stop: an approval node
// with at least one decision edge. One without is a plain hand-off.
func (h FlowHop) decides() bool { return h.Approval && len(h.Decide) > 0 }

// FlowStage is one stretch of the run's flow path: the hop its outcome led
// to, then the hop each step decision chose. Decision is nil for the first.
type FlowStage struct {
	Hop      FlowHop
	Decision *StepDecision
}

// flowPath is where the run's flow stands: the hop its outcome leads to
// (stage 0, with the loop's stop when it stopped), then one stage per step
// decision that applies (made on the current evidence at the approval node
// the path stands at, for a decision that node has an edge for). Empty
// while the run has no hop.
type flowPath struct {
	stages []FlowStage
	stop   Blocker
}

func (r Run) flowPath() flowPath {
	hop, stop, ok := r.flowBase()
	if !ok {
		return flowPath{}
	}
	p := flowPath{stages: []FlowStage{{Hop: hop}}, stop: stop}
	if len(r.StepDecisions) == 0 {
		return p
	}
	digest := r.EvidenceDigest()
	for i := range r.StepDecisions {
		d := r.StepDecisions[i]
		next, has := hop.Decide[d.Outcome]
		if !hop.decides() || d.EvidenceDigest != digest || d.Node != hop.Stop || !has {
			continue
		}
		hop = next
		p.stages = append(p.stages, FlowStage{Hop: hop, Decision: &d})
	}
	// A decision clears the loop's stop: the person chose the hop.
	if len(p.stages) > 1 {
		p.stop = ""
	}
	return p
}

func (p flowPath) ok() bool { return len(p.stages) > 0 }

// hop is where the path leads now: the last stage's hop.
func (p flowPath) hop() FlowHop {
	if !p.ok() {
		return FlowHop{}
	}
	return p.stages[len(p.stages)-1].Hop
}

// decided is the decision that chose the current hop, nil when none did.
func (p flowPath) decided() *StepDecision {
	if !p.ok() {
		return nil
	}
	return p.stages[len(p.stages)-1].Decision
}

// reachedApproval reports whether the path stands at, or passed, an
// approval node that decides: from there only its decision leads on.
func (p flowPath) reachedApproval() bool { return p.hop().decides() || p.decided() != nil }

// pendingApproval is the approval hop the run waits at for a decision: a
// submitted or completed run without a blocker whose flow stands at an
// approval node that decides. This is the one "awaits approval" rule:
// Approval and the decision guard both ask it.
func (r Run) pendingApproval() (FlowHop, bool) {
	if r.State != Submitted && r.State != Completed || r.Blocker != "" {
		return FlowHop{}, false
	}
	hop := r.flowPath().hop()
	return hop, hop.decides()
}

// FlowStages lists the hops the run's flow has taken so far, in order: the
// hop of its outcome and, after each decision at an approval node, the hop
// that decision chose (the last is FlowNext's; its Decision is non-nil
// exactly when a person's decision chose the hop FlowNext returns). Each
// stage's statuses are reached once, when its hop is; hosts that apply
// statuses from an outbox key each stage separately so a decision never
// skips the statuses on the way to the approval node. Empty while the run
// has no hop.
func (r Run) FlowStages() []FlowStage { return r.flowPath().stages }

// HostFacts are what the host knows, at the time of one call, about one run
// and the person acting on it that the run itself cannot hold. Only step
// decisions (ApproveStep, RequestStepChanges) read them. Hosts resolve them
// per run and pass them with the call (Command.Facts for Apply and
// Successor, AllowedCommands' facts); the zero value is "nothing known in
// the person's favour, nothing fences", so hosts that never resolve them
// admit only the run's owner, listed reviewers and organization
// owners/admins.
type HostFacts struct {
	// TeamManager: the person manages the team that owns the run's
	// project (the host's "manages this team" rule). ProjectWriter: they
	// may write to the run's project.
	TeamManager   bool
	ProjectWriter bool
	// Fence is what makes a decision at the run's approval node moot.
	Fence DecisionFence
}

// DecisionFence is what the host knows makes a decision at a run's approval
// node one that could never take effect. Any field fences: the run does not
// wait (ApprovalStop.Waiting) and step decisions are refused.
type DecisionFence struct {
	// Superseded: an open run follows the run, so the hop a decision chose
	// would never be followed.
	Superseded bool
	// Moved: the run's subject moved from under it since it started (a
	// work item's project to another team); its path is no longer followed.
	Moved bool
	// Unknown: the host could not read Superseded or Moved. Hosts that
	// cannot tell either way and must not offer a decision set it.
	Unknown bool
}

// Fences reports whether a decision is refused.
func (f DecisionFence) Fences() bool { return f.Superseded || f.Moved || f.Unknown }

// err is the refusal of a decision the fence refuses (nil when none).
func (f DecisionFence) err() error {
	switch {
	case f.Superseded:
		return fmt.Errorf("%w: a later run already continues this work", ErrConflict)
	case f.Moved:
		return fmt.Errorf("%w: the run's subject moved since this run started", ErrConflict)
	case f.Unknown:
		return ErrSourceUnavailable
	}
	return nil
}

// ApprovalStop is an approval node the run's flow reached that people decide
// at: who decides, which decisions have somewhere to lead and, once made,
// the decision.
type ApprovalStop struct {
	Node     string
	Name     string
	Note     string
	Approver Approver
	// Outcomes lists the decisions the node has an edge for, in Outcomes
	// order (approved, changes_requested).
	Outcomes []Outcome
	// Waiting: the run waits here for a decision now (submitted or
	// completed, no blocker, nobody decided yet, and the fence does not
	// fence). Exactly then the node's approvers may send ApproveStep /
	// RequestStepChanges.
	Waiting bool
	// Decision is the decision made here; nil while undecided.
	Decision *StepDecision
}

// Approval is the approval node the run waits at undecided (Waiting unless
// the host's fence fences, HostFacts.Fence), else the one its latest
// decision was made at; ok is false when neither applies (a blocked run, a
// hand-off node without edges: a plain stop, not an approval).
func (r Run) Approval(fence DecisionFence) (ApprovalStop, bool) {
	if at, ok := r.pendingApproval(); ok {
		return approvalStop(at, !fence.Fences(), nil), true
	}
	p := r.flowPath()
	d := p.decided()
	if d == nil {
		return ApprovalStop{}, false
	}
	decision := *d
	return approvalStop(p.stages[len(p.stages)-2].Hop, false, &decision), true
}

func approvalStop(at FlowHop, waiting bool, d *StepDecision) ApprovalStop {
	out := ApprovalStop{Node: at.Stop, Name: at.StopName, Note: at.Note, Approver: at.Approver.OrDefault(), Waiting: waiting, Decision: d}
	for _, o := range Outcomes() {
		if _, has := at.Decide[o]; has {
			out.Outcomes = append(out.Outcomes, o)
		}
	}
	return out
}

// IsStepDecision reports whether t is a person's decision at an approval
// node (ApproveStep, RequestStepChanges): the commands that read
// HostFacts.
func (t CommandType) IsStepDecision() bool { return t == ApproveStep || t == RequestStepChanges }

// stepOutcome is the decision a step approval command records.
func stepOutcome(t CommandType) Outcome {
	if t == ApproveStep {
		return OutcomeApproved
	}
	return OutcomeChangesRequested
}

// mayDecideStep is who may decide at an approval node (Approver).
// Organization owners and admins always may.
func (r Run) mayDecideStep(a Actor, f HostFacts, approver Approver) bool {
	if a.manager() {
		return true
	}
	switch approver.OrDefault() {
	case ApproverOwner:
		return a.UserID == r.OwnerID
	case ApproverTeamManagers:
		return f.TeamManager
	case ApproverReviewers:
		if len(r.Policy.ReviewerIDs) > 0 {
			return slices.Contains(r.Policy.ReviewerIDs, a.UserID)
		}
		return f.ProjectWriter && a.UserID != r.OwnerID
	}
	return false
}

// decideStep records the decision; the guard admitted actor and state.
func (r *Run) decideStep(a Actor, c Command) error {
	reason := strings.TrimSpace(c.Reason)
	if !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > MaxFeedbackRunes || c.Type == RequestStepChanges && reason == "" {
		return ErrValidation
	}
	at, _ := r.pendingApproval()
	r.StepDecisions = append(slices.Clone(r.StepDecisions), StepDecision{Node: at.Stop, Outcome: stepOutcome(c.Type),
		UserID: a.UserID, Reason: reason, EvidenceDigest: r.EvidenceDigest(), At: time.Now().UTC()})
	return nil
}

// successorFeedback is what a follow-up started by c works from (nil
// without anything): a Refine reason (the person refining), or the reason
// of the "changes requested" decision that chose the hop a Next follows
// (the person who decided, whoever starts the step).
func (r Run) successorFeedback(a Actor, c Command) *Feedback {
	switch c.Type {
	case Refine:
		if text := strings.TrimSpace(c.Reason); text != "" {
			return &Feedback{Text: text, Source: FeedbackRefine, By: a.UserID}
		}
	case Next:
		if d := r.flowPath().decided(); d != nil && d.Outcome == OutcomeChangesRequested && d.Reason != "" {
			return &Feedback{Text: d.Reason, Source: FeedbackStepDecision, By: d.UserID}
		}
	}
	return nil
}
