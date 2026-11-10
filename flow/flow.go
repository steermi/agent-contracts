// Package flow defines delivery flows: small graphs of agent steps (each run
// by a skill), quality loops, status changes, human handoffs and ends. A flow
// decides which step follows a finished run, which board status changes
// start a step and where a loop stops. It never runs anything itself: every
// run still goes through the activity coordinator and activity.Run.Apply,
// and runs pin the flow version (with its resolved hops) they were created
// from.
//
// Layout is never stored: editors lay the graph out automatically.
package flow

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/skill"
	"github.com/steermi/agent-contracts/skill/builtin"
)

// Limits keep flows readable as a diagram.
const (
	MaxNodes           = 16
	MaxTriggersPerNode = 8
	MaxRequiredChecks  = 10
	MaxLoopBody        = 4
	MaxLoopIterations  = 6
	DefaultIterations  = 3
	DefaultStallRounds = 2
	maxNameLength      = 80
	maxCheckLength     = 100
)

// Trigger modes, as for board rules.
const (
	ModeOff  = "off"
	ModeAsk  = "ask"
	ModeAuto = "auto"
)

// NodeType is the kind of a flow node.
type NodeType string

const (
	// NodeAgent runs one activity with a skill.
	NodeAgent NodeType = "agent"
	// NodeLoop is a container: its body repeats until approved or stopped.
	NodeLoop NodeType = "loop"
	// NodeApproval hands the work to a person; the flow stops there.
	NodeApproval NodeType = "approval"
	// NodeStatus sets the work item status when reached.
	NodeStatus NodeType = "status"
	// NodeEnd ends the flow.
	NodeEnd NodeType = "end"
)

var (
	// ErrInvalid wraps every validation failure.
	ErrInvalid = errors.New("FLOW_INVALID")

	keyRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
	// Categories are the six work item status categories.
	Categories = []string{"backlog", "todo", "in_progress", "in_review", "done", "cancelled"}
	itemTypes  = []string{"*", "story", "task", "bug", "epic"}
	modes      = []string{ModeOff, ModeAsk, ModeAuto}
	nodeTypes  = []NodeType{NodeAgent, NodeLoop, NodeApproval, NodeStatus, NodeEnd}
)

// Trigger starts a step when a person moves a work item. ToStatusID narrows
// the category to one team status and is allowed only in team and project
// flows (statuses belong to teams).
type Trigger struct {
	FromStatus string `json:"from_status"`
	ToStatus   string `json:"to_status"`
	ToStatusID string `json:"to_status_id,omitempty"`
	ItemType   string `json:"item_type"`
	Mode       string `json:"mode"`
}

// Requirements are gates on a step's run, mapped onto the run policy:
// human acceptance and named provider-verified checks.
type Requirements struct {
	Acceptance bool     `json:"acceptance,omitempty"`
	Checks     []string `json:"checks,omitempty"`
}

// Node is one flow node; which fields apply depends on Type.
type Node struct {
	Key  string   `json:"key"`
	Type NodeType `json:"type"`
	Name string   `json:"name,omitempty"`
	// Agent: the skill the step runs ("steermi:<slug>" built-in or a skill
	// id). The runner is derived from the skill's result (Resolved.Kind),
	// never stored (graph v3).
	Skill    string       `json:"skill,omitempty"`
	Requires Requirements `json:"requires,omitempty"`
	Triggers []Trigger    `json:"triggers,omitempty"`
	// Loop: the body's node keys (first = entry) and its stop conditions.
	Body []string             `json:"body,omitempty"`
	Loop *activity.LoopPolicy `json:"loop,omitempty"`
	// Status: the status set on arrival.
	Status *activity.StatusTarget `json:"status,omitempty"`
	// Required nodes cannot be removed or changed by narrower scopes.
	Required bool `json:"required,omitempty"`
}

// Edge follows an outcome of From. Start applies to edges into agent steps
// and loops: ask (default) waits for a person, auto starts the step.
type Edge struct {
	From  string           `json:"from"`
	To    string           `json:"to"`
	On    activity.Outcome `json:"on"`
	Start string           `json:"start,omitempty"`
}

// Auto reports whether the edge starts its step without asking.
func (e Edge) Auto() bool { return e.Start == ModeAuto }

// Graph is a flow version as stored and edited: agent steps name only
// their skill. Resolve it to place runs, validate or simulate.
type Graph struct {
	Start string `json:"start"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Resolved is a graph whose agent steps carry the runner derived from their
// skill's result. Only Graph.Resolve makes one, so every method that needs
// a step's runner (Validate, Hop, Ref, Rules, FirstOf, Simulate) lives here
// and cannot run on an unresolved graph. It encodes as its Graph.
type Resolved struct {
	Graph
	runners map[string]activity.Runner
}

// Runner is the runner of an agent step ("" when its skill did not resolve).
func (r Resolved) Runner(key string) activity.Runner { return r.runners[key] }

// UnmarshalJSON refuses to decode a resolution: decode a Graph and Resolve
// it with the scope's skills.
func (r *Resolved) UnmarshalJSON([]byte) error {
	return errors.New("flow: decode a Graph and call Resolve")
}

// UnmarshalJSON rejects unknown fields, so a misspelt key never silently
// drops part of a flow.
func (g *Graph) UnmarshalJSON(data []byte) error {
	type plain Graph
	var p plain
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return err
	}
	*g = Graph(p)
	return nil
}

// Issue is a validation problem addressed to a node (empty for the graph).
type Issue struct {
	Node    string `json:"node,omitempty"`
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Issues lists every validation problem.
type Issues []Issue

func (e Issues) Error() string {
	parts := make([]string, len(e))
	for i, issue := range e {
		prefix := issue.Field
		if issue.Node != "" {
			prefix = issue.Node + "." + issue.Field
		}
		parts[i] = prefix + ": " + issue.Message
	}
	return ErrInvalid.Error() + ": " + strings.Join(parts, "; ")
}

func (e Issues) Unwrap() error { return ErrInvalid }

// DefaultVersion identifies the built-in default flow on runs that pin it.
const DefaultVersion = "system:1"

// Default is the built-in flow every organization inherits until a scope
// saves its own: Plan → [quality loop: Build ⇄ Review, max 3 rounds, stop
// below high] → ⚑ In review → End, the loop handing to the owner when it
// stops. Every edge asks; board triggers are the former default rules.
func Default() Graph {
	return Graph{
		Start: "plan",
		Nodes: []Node{
			{Key: "plan", Type: NodeAgent, Name: "Plan", Skill: builtin.ID(activity.Answer)},
			{Key: "quality", Type: NodeLoop, Name: "Quality loop", Body: []string{"build", "review"},
				Loop: &activity.LoopPolicy{MaxIterations: DefaultIterations, StopBelow: activity.High, StallRounds: DefaultStallRounds}},
			{Key: "build", Type: NodeAgent, Name: "Build", Skill: builtin.ID(activity.Code),
				Triggers: []Trigger{{FromStatus: "todo", ToStatus: "in_progress", ItemType: "*", Mode: ModeAsk}}},
			{Key: "review", Type: NodeAgent, Name: "Review", Skill: builtin.ID(activity.Findings),
				Triggers: []Trigger{{FromStatus: "in_progress", ToStatus: "in_review", ItemType: "*", Mode: ModeAsk}}},
			{Key: "in_review", Type: NodeStatus, Name: "In review", Status: &activity.StatusTarget{Category: "in_review"}},
			{Key: "owner", Type: NodeApproval, Name: "Owner decides"},
			{Key: "end", Type: NodeEnd, Name: "End"},
		},
		Edges: []Edge{
			{From: "plan", To: "quality", On: activity.OutcomeDone, Start: ModeAsk},
			{From: "build", To: "review", On: activity.OutcomeDone, Start: ModeAsk},
			{From: "review", To: "build", On: activity.OutcomeChangesRequested, Start: ModeAsk},
			{From: "review", To: "in_review", On: activity.OutcomeApproved},
			{From: "quality", To: "owner", On: activity.OutcomeExhausted},
			{From: "in_review", To: "end", On: activity.OutcomeDone},
		},
	}
}

// DefaultResolved is Default resolved (built-in skills only).
func DefaultResolved() Resolved {
	r, _ := Default().Resolve(nil)
	return r
}

// Options control scope-dependent validation. Whether an organization may
// use auto edges at all (its switch and plan) is the host's policy, checked
// with HasAuto.
type Options struct {
	// AllowStatusIDs permits team statuses (team and project flows).
	AllowStatusIDs bool
	// Parent is the inherited flow; its required nodes must be preserved.
	Parent *Graph
}

// Node returns a node by key.
func (g Graph) Node(key string) (Node, bool) {
	i := slices.IndexFunc(g.Nodes, func(n Node) bool { return n.Key == key })
	if i < 0 {
		return Node{}, false
	}
	return g.Nodes[i], true
}

// LoopOf returns the loop whose body holds key.
func (g Graph) LoopOf(key string) (Node, bool) {
	for _, n := range g.Nodes {
		if n.Type == NodeLoop && slices.Contains(n.Body, key) {
			return n, true
		}
	}
	return Node{}, false
}

// Next returns the edge an outcome of a node follows.
func (g Graph) Next(from string, on activity.Outcome) (Edge, bool) {
	i := slices.IndexFunc(g.Edges, func(e Edge) bool { return e.From == from && e.On == on })
	if i < 0 {
		return Edge{}, false
	}
	return g.Edges[i], true
}

// Outcomes lists the outcomes a node may have an edge for.
func (r Resolved) Outcomes(n Node) []activity.Outcome {
	switch n.Type {
	case NodeAgent:
		if r.Runner(n.Key) == activity.Findings {
			return []activity.Outcome{activity.OutcomeApproved, activity.OutcomeChangesRequested, activity.OutcomeFailed}
		}
		return []activity.Outcome{activity.OutcomeDone, activity.OutcomeChangesRequested, activity.OutcomeFailed}
	case NodeLoop:
		return []activity.Outcome{activity.OutcomeExhausted}
	case NodeStatus:
		return []activity.Outcome{activity.OutcomeDone}
	}
	return nil
}

// FirstOf returns the first agent step of a runner, for manual requests
// that name a runner but no step.
func (g Resolved) FirstOf(runner activity.Runner) (Node, bool) {
	i := slices.IndexFunc(g.Nodes, func(n Node) bool { return n.Type == NodeAgent && g.Runner(n.Key) == runner })
	if i < 0 {
		return Node{}, false
	}
	return g.Nodes[i], true
}

type adder func(node, field, format string, args ...any)

// Validate reports every structural problem.
func (g Resolved) Validate(opt Options) error {
	var out Issues
	add := func(node, field, format string, args ...any) {
		out = append(out, Issue{node, field, fmt.Sprintf(format, args...)})
	}
	if len(g.Nodes) == 0 || len(g.Nodes) > MaxNodes {
		add("", "nodes", "a flow has 1 to %d nodes", MaxNodes)
		return out
	}
	keys := map[string]Node{}
	for _, n := range g.Nodes {
		if !keyRe.MatchString(n.Key) {
			add(n.Key, "key", "use a lowercase letter then letters, digits, - or _ (max 40)")
		}
		if _, dup := keys[n.Key]; dup {
			add(n.Key, "key", "duplicate node key")
		}
		keys[n.Key] = n
	}
	member := map[string]string{} // body node → loop key
	triggers := map[string]string{}
	agents := 0
	for _, n := range g.Nodes {
		if utf8.RuneCountInString(n.Name) > maxNameLength {
			add(n.Key, "name", "at most %d characters", maxNameLength)
		}
		if !slices.Contains(nodeTypes, n.Type) {
			add(n.Key, "type", "must be agent, loop, approval, status or end")
			continue
		}
		if n.Type != NodeAgent && (n.Skill != "" || len(n.Triggers) > 0 || len(n.Requires.Checks) > 0 || n.Requires.Acceptance) {
			add(n.Key, "type", "only agent steps have a skill, triggers or requirements")
		}
		if n.Type != NodeLoop && (len(n.Body) > 0 || n.Loop != nil) {
			add(n.Key, "body", "only loops have a body")
		}
		if n.Type != NodeStatus && n.Status != nil {
			add(n.Key, "status", "only status nodes set a status")
		}
		switch n.Type {
		case NodeAgent:
			agents++
			validateAgent(n, g.Runner(n.Key), add)
		case NodeLoop:
			validateLoop(n, keys, member, add)
		case NodeStatus:
			switch {
			case n.Status == nil || !slices.Contains(Categories, n.Status.Category):
				add(n.Key, "status", "choose one of the status categories")
			case n.Status.StatusID != "" && !opt.AllowStatusIDs:
				add(n.Key, "status", "team statuses can only be used in team or project flows")
			}
		}
		if len(n.Triggers) > MaxTriggersPerNode {
			add(n.Key, "triggers", "at most %d triggers", MaxTriggersPerNode)
		}
		for _, t := range n.Triggers {
			switch {
			case t.FromStatus != "*" && !slices.Contains(Categories, t.FromStatus):
				add(n.Key, "triggers", "unknown from status %q", t.FromStatus)
			case !slices.Contains(Categories, t.ToStatus):
				add(n.Key, "triggers", "unknown to status %q", t.ToStatus)
			case !slices.Contains(itemTypes, t.ItemType):
				add(n.Key, "triggers", "unknown item type %q", t.ItemType)
			case !slices.Contains(modes, t.Mode):
				add(n.Key, "triggers", "mode must be off, ask or auto")
			case t.ToStatusID != "" && !opt.AllowStatusIDs:
				add(n.Key, "triggers", "team statuses can only be used in team or project flows")
			case t.FromStatus == t.ToStatus && t.ToStatusID == "":
				add(n.Key, "triggers", "from and to status are the same")
			}
			id := t.FromStatus + ">" + t.ToStatus + ">" + t.ToStatusID + ">" + t.ItemType
			if other, dup := triggers[id]; dup {
				add(n.Key, "triggers", "the same status change already starts %q", other)
			}
			triggers[id] = n.Key
		}
	}
	if agents == 0 {
		add("", "nodes", "a flow needs at least one agent step")
	}
	g.validateEdges(keys, member, opt, add)
	if start, ok := keys[g.Start]; !ok {
		add("", "start", "the start node %q does not exist", g.Start)
	} else if member[start.Key] != "" {
		add("", "start", "start at the loop, not inside it")
	} else {
		g.validateReach(keys, member, add)
	}
	if opt.Parent != nil {
		for _, p := range opt.Parent.Nodes {
			if !p.Required {
				continue
			}
			n, ok := keys[p.Key]
			switch {
			case !ok:
				add(p.Key, "required", "the inherited flow requires node %q", p.Key)
			case n.Type != p.Type || n.Skill != p.Skill:
				add(p.Key, "required", "a required node keeps its type and skill")
			case !n.Required:
				add(p.Key, "required", "a required node stays required")
			}
		}
	}
	if len(out) > 0 {
		return out
	}
	return nil
}

func validateAgent(n Node, runner activity.Runner, add adder) {
	if n.Skill == "" {
		add(n.Key, "skill", "an agent step runs a skill")
		return
	}
	if _, ok := activity.Lookup(runner); !ok {
		add(n.Key, "skill", "the skill is not resolved; call Resolve first")
		return
	}
	if len(n.Requires.Checks) > MaxRequiredChecks {
		add(n.Key, "requires", "at most %d checks", MaxRequiredChecks)
	}
	for _, c := range n.Requires.Checks {
		if strings.TrimSpace(c) == "" || len(c) > maxCheckLength {
			add(n.Key, "requires", "check names are 1-%d characters", maxCheckLength)
		}
	}
	if runner != activity.Code && len(n.Requires.Checks) > 0 {
		add(n.Key, "requires", "only build steps produce checked revisions")
	}
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
	if p.StallRounds < 0 || p.StallRounds > MaxLoopIterations {
		add(n.Key, "loop", "stall rounds is 0 to %d", MaxLoopIterations)
	}
}

func (g Resolved) validateEdges(keys map[string]Node, member map[string]string, opt Options, add adder) {
	seen := map[string]bool{}
	for _, e := range g.Edges {
		from, okFrom := keys[e.From]
		to, okTo := keys[e.To]
		if !okFrom || !okTo {
			add(e.From, "edges", "edge %s → %s names an unknown node", e.From, e.To)
			continue
		}
		if !slices.Contains(g.Outcomes(from), e.On) {
			add(e.From, "edges", "%s has no %q outcome", g.nodeLabel(from), e.On)
		}
		id := e.From + ">" + string(e.On)
		if seen[id] {
			add(e.From, "edges", "%q already has a %s edge", e.From, e.On)
		}
		seen[id] = true
		switch e.Start {
		case "", ModeAsk:
		case ModeAuto:
			if to.Type != NodeAgent && to.Type != NodeLoop {
				add(e.From, "edges", "only edges into agent steps or loops start automatically")
			}
		default:
			add(e.From, "edges", "start must be ask or auto")
		}
		fromLoop, toLoop := member[e.From], member[e.To]
		switch {
		case e.From == e.To && !(toLoop != "" && isEntry(keys[toLoop], e.To)):
			add(e.From, "edges", "a step cannot follow itself outside a loop")
		case toLoop != "" && fromLoop != toLoop:
			add(e.From, "edges", "enter loop %q through the loop, not its step %q", toLoop, e.To)
		case fromLoop != "" && e.To == fromLoop:
			add(e.From, "edges", "a step cannot point at its own loop")
		}
	}
	for _, n := range g.Nodes {
		if n.Type != NodeLoop {
			continue
		}
		if _, ok := g.Next(n.Key, activity.OutcomeExhausted); !ok {
			add(n.Key, "edges", "a loop needs an exhausted exit")
		}
		if len(n.Body) > 0 && !slices.ContainsFunc(g.Edges, func(e Edge) bool { return e.To == n.Body[0] && member[e.From] == n.Key }) {
			add(n.Key, "edges", "a loop needs an edge back to its first step")
		}
	}
	// Cycles are allowed only inside loops: collapse every loop into one
	// node, drop its internal edges and require the rest to be acyclic.
	rep := func(k string) string {
		if l := member[k]; l != "" {
			return l
		}
		return k
	}
	adj := map[string][]string{}
	for _, e := range g.Edges {
		a, b := rep(e.From), rep(e.To)
		if a != b {
			adj[a] = append(adj[a], b)
		}
	}
	state := map[string]int{}
	cyclic := false
	var visit func(string)
	visit = func(k string) {
		state[k] = 1
		for _, next := range adj[k] {
			switch state[next] {
			case 0:
				visit(next)
			case 1:
				cyclic = true
			}
		}
		state[k] = 2
	}
	for _, n := range g.Nodes {
		if state[rep(n.Key)] == 0 {
			visit(rep(n.Key))
		}
	}
	if cyclic {
		add("", "edges", "steps can only repeat inside a quality loop")
	}
}

func isEntry(loop Node, key string) bool { return len(loop.Body) > 0 && loop.Body[0] == key }

func (g Resolved) nodeLabel(n Node) string {
	if n.Type == NodeAgent {
		return string(g.Runner(n.Key)) + " step " + n.Key
	}
	return string(n.Type) + " " + n.Key
}

// validateReach requires every node to be reachable from the start or a
// trigger.
func (g Graph) validateReach(keys map[string]Node, member map[string]string, add adder) {
	reach := map[string]bool{}
	var walk func(string)
	walk = func(k string) {
		n, ok := keys[k]
		if !ok || reach[k] {
			return
		}
		reach[k] = true
		if l := member[k]; l != "" {
			walk(l)
		}
		for _, b := range n.Body {
			walk(b)
		}
		for _, e := range g.Edges {
			if e.From == k {
				walk(e.To)
			}
		}
	}
	walk(g.Start)
	for _, n := range g.Nodes {
		if len(n.Triggers) > 0 {
			walk(n.Key)
		}
	}
	for _, n := range g.Nodes {
		if !reach[n.Key] {
			add(n.Key, "key", "node is not reachable from the start or a trigger")
		}
	}
}

// Hop resolves where an edge leads: through status nodes (collected), into a
// loop's first step, to an agent step or a stop. An edge that goes straight
// (no status node between) from a step of a loop to that loop's first step
// starts the loop's next iteration (Back).
func (g Resolved) Hop(e Edge) activity.FlowHop {
	hop := activity.FlowHop{Auto: e.Auto()}
	fromLoop, inLoop := g.LoopOf(e.From)
	for range MaxNodes + 1 {
		n, ok := g.Node(e.To)
		if !ok {
			return hop
		}
		switch n.Type {
		case NodeStatus:
			if n.Status != nil {
				hop.Statuses = append(hop.Statuses, *n.Status)
			}
			next, ok := g.Next(n.Key, activity.OutcomeDone)
			if !ok {
				hop.Stop = n.Key
				return hop
			}
			e, hop.Auto = next, next.Auto()
			continue
		case NodeEnd:
			hop.Stop = n.Key
			return hop
		case NodeApproval:
			hop.Stop, hop.Approval = n.Key, true
			return hop
		case NodeLoop:
			if len(n.Body) == 0 {
				hop.Stop = n.Key
				return hop
			}
			n, _ = g.Node(n.Body[0])
		}
		hop.Node, hop.Name, hop.Runner = n.Key, cmp.Or(n.Name, n.Key), g.Runner(n.Key)
		hop.Back = inLoop && len(hop.Statuses) == 0 && isEntry(fromLoop, n.Key)
		return hop
	}
	return hop
}

// Ref places a run on an agent node of the version and pins its hops and
// loop state. from is the run the new one follows (nil for a new chain):
// inside the same loop its state carries over, a back edge starts the next
// iteration and records the round's findings.
func (g Resolved) Ref(versionID, key string, from *activity.Run) (activity.FlowRef, bool) {
	n, ok := g.Node(key)
	if !ok || n.Type != NodeAgent {
		return activity.FlowRef{}, false
	}
	ref := activity.FlowRef{VersionID: versionID, Node: key, Hops: map[activity.Outcome]activity.FlowHop{}}
	for _, o := range g.Outcomes(n) {
		if e, ok := g.Next(key, o); ok {
			ref.Hops[o] = g.Hop(e)
		}
	}
	loop, inLoop := g.LoopOf(key)
	if !inLoop {
		return ref, true
	}
	if from != nil && from.Flow != nil && from.Flow.Loop != nil && from.Flow.Loop.Key == loop.Key && from.Flow.VersionID == versionID {
		state := *from.Flow.Loop
		state.Fingerprints = slices.Clone(state.Fingerprints)
		if isEntry(loop, key) && from.Flow.Node != key {
			state.Iteration++
			if from.Runner == activity.Findings {
				state.Fingerprints = append(state.Fingerprints, activity.Fingerprint(from.Findings))
			}
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

// StepRule is a board rule compiled from a step trigger.
type StepRule struct {
	Node string
	Rule activity.Rule
}

// Rules compiles every trigger into board rules for activity.EvaluateRule, in
// node order. There is no other source of board automation.
func (g Resolved) Rules() []StepRule {
	var out []StepRule
	for _, n := range g.Nodes {
		if n.Type != NodeAgent {
			continue
		}
		for _, t := range n.Triggers {
			out = append(out, StepRule{n.Key, activity.Rule{FromStatus: t.FromStatus, ToStatus: t.ToStatus, ToStatusID: t.ToStatusID, ItemType: t.ItemType, Runner: g.Runner(n.Key), Mode: t.Mode}})
		}
	}
	return out
}

// Policy adds the step's requirements to the scope's run policy. It only
// ever tightens the policy.
func (r Requirements) Policy(p activity.Policy) activity.Policy {
	if r.Acceptance {
		p.RequireAcceptance = true
	}
	for _, c := range r.Checks {
		if !slices.Contains(p.RequiredChecks, c) {
			p.RequiredChecks = append(p.RequiredChecks, c)
		}
	}
	return p
}

// ResultOf looks up the result of a stored skill usable at the flow's scope.
type ResultOf func(skillID string) (skill.Result, bool)

// Resolve derives every agent step's runner from its skill's result
// (built-ins resolve here; stored skills through results). Steps whose
// skill is unknown, unusable or a workspace skill are reported and left
// without a runner, so Validate rejects them too. g is not modified.
func (g Graph) Resolve(results ResultOf) (Resolved, Issues) {
	out := Resolved{Graph: g, runners: map[string]activity.Runner{}}
	var issues Issues
	for _, n := range g.Nodes {
		if n.Type != NodeAgent || n.Skill == "" {
			continue // Validate reports a missing skill
		}
		var r skill.Result
		var ok bool
		switch {
		case builtin.IsID(n.Skill):
			var s skill.Skill
			if s, ok = builtin.Get(n.Skill); ok {
				r = s.Result()
			}
		case results != nil:
			r, ok = results(n.Skill)
		}
		switch {
		case !ok:
			issues = append(issues, Issue{n.Key, "skill", "the skill is not published or not available here"})
		case !r.InFlows():
			issues = append(issues, Issue{n.Key, "skill", "workspace skills run as sessions and cannot be flow steps yet"})
		default:
			out.runners[n.Key] = r.Runner()
		}
	}
	return out, issues
}

// Digest identifies a graph.
func (g Graph) Digest() string { return activity.Digest(g) }

// SkillIDs lists the distinct stored skills a graph uses (built-ins
// excluded).
func (g Graph) SkillIDs() []string {
	var out []string
	for _, n := range g.Nodes {
		if n.Skill != "" && !builtin.IsID(n.Skill) && !slices.Contains(out, n.Skill) {
			out = append(out, n.Skill)
		}
	}
	return out
}

// HasAuto reports whether any edge starts its step automatically.
func (g Graph) HasAuto() bool { return slices.ContainsFunc(g.Edges, Edge.Auto) }

// WorstCaseCost is the most a loop can spend: its budget, or max iterations
// times the per-run budget.
func WorstCaseCost(l activity.LoopPolicy, perRun float64) float64 {
	worst := float64(max(l.MaxIterations, 1)) * perRun
	if l.Budget > 0 && l.Budget < worst {
		return l.Budget
	}
	return worst
}
