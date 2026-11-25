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
	// MaxNoteLength bounds an approval node's note (characters).
	MaxNoteLength  = 500
	maxCheckLength = 100
)

// Trigger modes, as for board rules.
const (
	ModeOff  = "off"
	ModeAsk  = "ask"
	ModeAuto = "auto"
)

// PageTriggerModes are the modes of a docs page trigger: a page trigger run
// has no automation owner, so it is always left suggested (no auto).
var PageTriggerModes = []string{ModeOff, ModeAsk}

// NodeType is the kind of a flow node.
type NodeType string

const (
	// NodeAgent runs one activity with a skill.
	NodeAgent NodeType = "agent"
	// NodeLoop is a container: its body repeats until approved or stopped.
	NodeLoop NodeType = "loop"
	// NodeApproval hands the work to a person. Without outgoing edges the
	// flow stops there; with an approved and/or changes_requested edge the
	// node's approver decides which one the flow follows.
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

// Subjects a flow runs on (Graph.Subject); the values are skill subjects.
const (
	SubjectWorkItem = skill.SubjectWorkItem
	SubjectDocPage  = skill.SubjectDocPage
)

var pageEvents = []string{activity.PageEventPublished, activity.PageEventUpdated}

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
	// Note (approval nodes): what the person the work is handed to should
	// know or check, at most MaxNoteLength characters, stored trimmed.
	// Shown with the hand-off (activity.FlowHop.Note); it decides nothing.
	Note string `json:"note,omitempty"`
	// Approver (approval nodes): who decides when the node has edges.
	// owner (default when empty), team_managers or reviewers.
	Approver activity.Approver `json:"approver,omitempty"`
	// Agent: the skill the step runs ("steermi:<slug>" built-in or a skill
	// id). The runner is derived from the skill's result (Resolved.Kind),
	// never stored (graph v3).
	Skill    string       `json:"skill,omitempty"`
	Requires Requirements `json:"requires,omitempty"`
	// Location (agent steps): where the step runs. inherit (default when
	// empty) = where the chain last ran this step, else where its
	// predecessor ran; cloud or local fix it. Hosts resolve it into
	// activity.FlowHop.Location when they place a run.
	Location activity.LocationRule `json:"location,omitempty"`
	Triggers []Trigger             `json:"triggers,omitempty"`
	// PageTriggers start the step on docs page events (docs page flows only).
	PageTriggers []activity.PageTrigger `json:"page_triggers,omitempty"`
	// Loop: the body's node keys (first = entry) and its stop conditions.
	Body []string             `json:"body,omitempty"`
	Loop *activity.LoopPolicy `json:"loop,omitempty"`
	// Status: the status set on arrival.
	Status *activity.StatusTarget `json:"status,omitempty"`
	// Required nodes cannot be removed or changed by narrower scopes.
	Required bool `json:"required,omitempty"`
}

// Edge follows an outcome of From. Start applies to edges into agent steps
// and loops: ask (default) waits for a person, auto starts the step. Edges
// between two steps of the same loop have no start: the loop's
// LoopPolicy.Advance decides them.
type Edge struct {
	From  string           `json:"from"`
	To    string           `json:"to"`
	On    activity.Outcome `json:"on"`
	Start string           `json:"start,omitempty"`
}

// Auto reports whether the edge starts its step without asking.
func (e Edge) Auto() bool { return e.Start == ModeAuto }

// Graph is a flow version as stored and edited: agent steps name only
// their skill. Resolve it to place runs or validate it.
//
// Subject is what the flow's runs work on: work_item (the default when
// empty, so stored work item flows keep their digest) or doc_page.
type Graph struct {
	Subject string `json:"subject,omitempty"`
	Start   string `json:"start"`
	Nodes   []Node `json:"nodes"`
	Edges   []Edge `json:"edges"`
}

// Resolved is a graph whose agent steps carry the runner derived from their
// skill's result. Only Graph.Resolve makes one, so every method that needs
// a step's runner (Validate, Hop, Ref, Rules, FirstOf) lives here
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
	*g = Graph(p).normalized()
	return nil
}

// normalized is the one place a graph's defaults get one spelling: an
// explicit inherit location, ask advance or owner approver becomes empty,
// and notes are trimmed. Every
// graph passes it, decoded (UnmarshalJSON) or built in Go (Resolve,
// Digest), so a flow written either way has one digest and its hops pin
// one rule ("" = inherit, never "inherit").
func (g Graph) normalized() Graph {
	if !slices.ContainsFunc(g.Nodes, func(n Node) bool {
		return n.Location == activity.RuleInherit || n.Loop != nil && n.Loop.Advance == activity.LoopAdvanceAsk ||
			n.Note != strings.TrimSpace(n.Note) || n.Approver == activity.ApproverOwner
	}) {
		return g
	}
	g.Nodes = slices.Clone(g.Nodes)
	for i, n := range g.Nodes {
		g.Nodes[i].Note = strings.TrimSpace(n.Note)
		if n.Approver == activity.ApproverOwner {
			g.Nodes[i].Approver = ""
		}
		if n.Location == activity.RuleInherit {
			g.Nodes[i].Location = ""
		}
		if n.Loop != nil && n.Loop.Advance == activity.LoopAdvanceAsk {
			p := *n.Loop
			p.Advance = ""
			g.Nodes[i].Loop = &p
		}
	}
	return g
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

// SubjectKind is the flow's subject, work_item when unset.
func (g Graph) SubjectKind() string { return cmp.Or(g.Subject, SubjectWorkItem) }

// Options control scope-dependent validation. Whether an organization may
// use auto edges outside loops (its switch and plan) is the host's policy,
// checked with HasAutoOutsideLoops.
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

// Next returns the edge an outcome of a node follows.
func (g Graph) Next(from string, on activity.Outcome) (Edge, bool) {
	i := slices.IndexFunc(g.Edges, func(e Edge) bool { return e.From == from && e.On == on })
	if i < 0 {
		return Edge{}, false
	}
	return g.Edges[i], true
}

// Outcomes lists the outcomes a node may have an edge for. A code step
// reaches "changes requested" only through a person's decision on its
// revision, so it has that outcome only while it requires human acceptance
// (Requires.Acceptance); Validate refuses the edge otherwise.
func (r Resolved) Outcomes(n Node) []activity.Outcome {
	switch n.Type {
	case NodeAgent:
		switch r.Runner(n.Key) {
		case activity.Findings:
			return []activity.Outcome{activity.OutcomeApproved, activity.OutcomeChangesRequested, activity.OutcomeFailed}
		case activity.Code:
			if !n.Requires.Acceptance {
				return []activity.Outcome{activity.OutcomeDone, activity.OutcomeFailed}
			}
		}
		return []activity.Outcome{activity.OutcomeDone, activity.OutcomeChangesRequested, activity.OutcomeFailed}
	case NodeLoop:
		return []activity.Outcome{activity.OutcomeExhausted}
	case NodeApproval:
		// A person's decision; a node with neither edge is a plain hand-off.
		return []activity.Outcome{activity.OutcomeApproved, activity.OutcomeChangesRequested}
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
	doc := g.Subject == SubjectDocPage
	switch {
	case g.Subject == SubjectWorkItem:
		// One encoding (and digest) per flow: work items are the empty subject.
		add("", "subject", "leave the subject empty for work item flows")
	case g.Subject != "" && !doc:
		add("", "subject", "a flow runs on work items or docs pages")
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
	pageTriggers := map[string]string{}
	agents := 0
	for _, n := range g.Nodes {
		if utf8.RuneCountInString(n.Name) > maxNameLength {
			add(n.Key, "name", "at most %d characters", maxNameLength)
		}
		if !slices.Contains(nodeTypes, n.Type) {
			add(n.Key, "type", "must be agent, loop, approval, status or end")
			continue
		}
		if n.Type != NodeAgent && (n.Skill != "" || len(n.Triggers) > 0 || len(n.PageTriggers) > 0 || len(n.Requires.Checks) > 0 || n.Requires.Acceptance) {
			add(n.Key, "type", "only agent steps have a skill, triggers or requirements")
		}
		if n.Type != NodeLoop && (len(n.Body) > 0 || n.Loop != nil) {
			add(n.Key, "body", "only loops have a body")
		}
		if n.Type != NodeStatus && n.Status != nil {
			add(n.Key, "status", "only status nodes set a status")
		}
		switch {
		case n.Note != "" && n.Type != NodeApproval:
			add(n.Key, "note", "only hand-offs to a person have a note")
		case !utf8.ValidString(n.Note) || utf8.RuneCountInString(n.Note) > MaxNoteLength:
			add(n.Key, "note", "at most %d characters", MaxNoteLength)
		}
		switch {
		case n.Approver != "" && n.Type != NodeApproval:
			add(n.Key, "approver", "only hand-offs to a person have an approver")
		case !n.Approver.Valid():
			add(n.Key, "approver", "approver is owner, team_managers or reviewers")
		case doc && n.Approver.OrDefault() != activity.ApproverOwner:
			// A docs page run has no team or project: nobody but the run's
			// owner (and organization owners and admins) could decide.
			add(n.Key, "approver", "docs page approvals are decided by the run's owner")
		}
		switch {
		case !n.Location.Valid():
			add(n.Key, "location", "location is inherit, cloud or local")
		case n.Location != "" && n.Type != NodeAgent:
			add(n.Key, "location", "only agent steps have a location")
		case doc && n.Location == activity.RuleLocal:
			add(n.Key, "location", "docs page steps run in the cloud")
		}
		switch n.Type {
		case NodeAgent:
			agents++
			validateAgent(n, g.Runner(n.Key), add)
			if doc && g.Runner(n.Key) == activity.Code {
				add(n.Key, "skill", "code skills cannot run on a docs page")
			}
		case NodeLoop:
			validateLoop(n, keys, member, add)
		case NodeStatus:
			if doc {
				add(n.Key, "type", "docs page flows have no status changes")
			}
			switch {
			case n.Status == nil || !slices.Contains(Categories, n.Status.Category):
				add(n.Key, "status", "choose one of the status categories")
			case n.Status.StatusID != "" && !opt.AllowStatusIDs:
				add(n.Key, "status", "team statuses can only be used in team or project flows")
			}
		}
		if n.Type == NodeAgent {
			g.validatePageTriggers(n, doc, pageTriggers, add)
		}
		if doc && len(n.Triggers) > 0 {
			add(n.Key, "triggers", "docs page flows start from page events, not status changes")
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
		if opt.Parent.SubjectKind() != g.SubjectKind() {
			add("", "subject", "the inherited flow runs on %s", opt.Parent.SubjectKind())
		}
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

func (g Resolved) validatePageTriggers(n Node, doc bool, seen map[string]string, add adder) {
	if len(n.PageTriggers) == 0 {
		return
	}
	if !doc {
		add(n.Key, "page_triggers", "only docs page flows start from page events")
		return
	}
	if len(n.PageTriggers) > MaxTriggersPerNode {
		add(n.Key, "page_triggers", "at most %d triggers", MaxTriggersPerNode)
	}
	for _, t := range n.PageTriggers {
		switch {
		case !slices.Contains(pageEvents, t.Event):
			add(n.Key, "page_triggers", "unknown page event %q", t.Event)
		case !slices.Contains(PageTriggerModes, t.Mode):
			add(n.Key, "page_triggers", "page triggers are off or ask")
		}
		if other, dup := seen[t.Event]; dup {
			add(n.Key, "page_triggers", "the same page event already starts %q", other)
		}
		seen[t.Event] = n.Key
	}
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
		if loop, inner := g.innerLoop(e); inner && e.Start != "" {
			add(e.From, "edges", "steps inside loop %q follow the loop's advance setting, not the edge's start", loop.Key)
		}
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
	// Work repeats only inside a loop or on a person's decision: collapse
	// every loop into one node, drop its internal edges and the edges that
	// leave an approval node (a person decides each time they are taken)
	// and require the rest to be acyclic.
	rep := func(k string) string {
		if l := member[k]; l != "" {
			return l
		}
		return k
	}
	adj := map[string][]string{}
	// decisions holds the paths that run no step: from an approval or
	// status node to the next approval or status node. A cycle there is a
	// decision that leads only back to a decision.
	decisions := map[string][]string{}
	passive := func(k string) bool { return keys[k].Type == NodeApproval || keys[k].Type == NodeStatus }
	for _, e := range g.Edges {
		a, b := rep(e.From), rep(e.To)
		if a != b && keys[e.From].Type != NodeApproval {
			adj[a] = append(adj[a], b)
		}
		if passive(e.From) && passive(e.To) {
			decisions[e.From] = append(decisions[e.From], e.To)
		}
	}
	nodes := make([]string, 0, len(g.Nodes))
	for _, n := range g.Nodes {
		nodes = append(nodes, rep(n.Key))
	}
	if cyclic(nodes, adj) {
		add("", "edges", "steps can only repeat inside a loop or after a person's decision")
	}
	if cyclic(nodes, decisions) {
		add("", "edges", "a hand-off's decision must lead to a step or an end, not back to a hand-off")
	}
}

// cyclic reports whether the directed graph adj has a cycle reachable from
// nodes.
func cyclic(nodes []string, adj map[string][]string) bool {
	state := map[string]int{}
	found := false
	var visit func(string)
	visit = func(k string) {
		state[k] = 1
		for _, next := range adj[k] {
			switch state[next] {
			case 0:
				visit(next)
			case 1:
				found = true
			}
		}
		state[k] = 2
	}
	for _, k := range nodes {
		if state[k] == 0 {
			visit(k)
		}
	}
	return found
}

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
		if len(n.Triggers) > 0 || len(n.PageTriggers) > 0 {
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
// starts the loop's next iteration (Back). An edge between two steps of the
// same loop (InLoop) is automatic exactly when the loop advances by itself; its own
// start is ignored (pinned versions may still carry one). The hop carries
// the target step's location rule; the host fills its Location. A hop that
// ends at an approval node carries the node's name, note and approver and,
// in Decide, the hop of each decision the node has an edge for.
func (g Resolved) Hop(e Edge) activity.FlowHop { return g.hop(e, nil) }

// hop resolves an edge; passed lists the status and approval nodes the
// path went through to get here, and the path stops at a node it passed
// before, so a graph that was never validated cannot cycle (validation
// refuses such cycles).
func (g Resolved) hop(e Edge, passed []string) activity.FlowHop {
	hop := activity.FlowHop{Auto: e.Auto()}
	loop, inner := g.innerLoop(e)
	if inner {
		hop.Auto, hop.InLoop = loop.Loop != nil && loop.Loop.AutoAdvance(), true
	}
	for {
		n, ok := g.Node(e.To)
		if !ok {
			return hop
		}
		again := slices.Contains(passed, n.Key)
		switch n.Type {
		case NodeStatus:
			if again {
				hop.Stop = n.Key
				return hop
			}
			passed = append(slices.Clip(passed), n.Key)
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
			hop.Stop, hop.Approval, hop.Note = n.Key, true, n.Note
			hop.StopName, hop.Approver = cmp.Or(n.Name, n.Key), n.Approver
			if again {
				return hop
			}
			passed = append(slices.Clip(passed), n.Key)
			for _, o := range g.Outcomes(n) {
				if next, ok := g.Next(n.Key, o); ok {
					if hop.Decide == nil {
						hop.Decide = map[activity.Outcome]activity.FlowHop{}
					}
					hop.Decide[o] = g.hop(next, passed)
				}
			}
			return hop
		case NodeLoop:
			if len(n.Body) == 0 {
				hop.Stop = n.Key
				return hop
			}
			n, _ = g.Node(n.Body[0])
		}
		hop.Node, hop.Name, hop.Runner, hop.Rule = n.Key, cmp.Or(n.Name, n.Key), g.Runner(n.Key), n.Location
		hop.Back = inner && isEntry(loop, n.Key)
		return hop
	}
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
	if g.SubjectKind() != SubjectWorkItem {
		return out
	}
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

// PageStepRule is a docs page trigger of a step, with the step's runner, for
// activity.EvaluatePageTrigger.
type PageStepRule struct {
	Node    string
	Runner  activity.Runner
	Trigger activity.PageTrigger
}

// PageRules lists every page trigger of a docs page flow, in node order.
func (g Resolved) PageRules() []PageStepRule {
	var out []PageStepRule
	if g.Subject != SubjectDocPage {
		return out
	}
	for _, n := range g.Nodes {
		if n.Type != NodeAgent {
			continue
		}
		for _, t := range n.PageTriggers {
			out = append(out, PageStepRule{n.Key, g.Runner(n.Key), t})
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
// skill is unknown, unusable, a workspace skill or (in docs page flows) a
// code skill are reported and left
// without a runner, so Validate rejects them too. g is not modified.
func (g Graph) Resolve(results ResultOf) (Resolved, Issues) {
	g = g.normalized()
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
		case g.Subject == SubjectDocPage && r.Runner() == activity.Code:
			issues = append(issues, Issue{n.Key, "skill", "code skills cannot run on a docs page"})
		default:
			out.runners[n.Key] = r.Runner()
		}
	}
	return out, issues
}

// Digest identifies a graph; both spellings of a default hash alike.
func (g Graph) Digest() string { return activity.Digest(g.normalized()) }

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

// HasAutoOutsideLoops reports whether an edge outside every loop (or into a
// loop) starts its step automatically: the only automation the host's
// auto-edge policy (organization switch and plan) gates. Hops inside a loop
// follow its Advance and are bounded by the loop's own stops.
func (g Graph) HasAutoOutsideLoops() bool {
	return slices.ContainsFunc(g.Edges, func(e Edge) bool {
		_, inner := g.innerLoop(e)
		return e.Auto() && !inner
	})
}
