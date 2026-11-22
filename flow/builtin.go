package flow

import (
	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/skill/builtin"
)

// Built-in flows: the defaults every organization inherits, current and
// earlier, by the version id runs pin. Built-in graphs never change under a
// version id.

// DefaultVersion identifies the built-in default flow (Default) on runs that
// pin it.
const DefaultVersion = "system:3"

// VersionV1 identifies the first built-in work item flow (every edge asks)
// and VersionV2 the second (the loop advances by itself; a failed Review
// has no exit). Neither is the default; both stay resolvable for runs that
// pin them.
const (
	VersionV1 = "system:1"
	VersionV2 = "system:2"
)

// DefaultDocVersion identifies the built-in default docs page flow
// (defaultDocPageV2).
const DefaultDocVersion = "system:doc:2"

// DocVersionV1 identifies the first built-in docs page flow (one Plan step).
// It is no longer a default but stays resolvable for runs that pin it.
const DocVersionV1 = "system:doc:1"

// BuiltinGraph returns the built-in flow a run pins by version: the work
// item default and every docs page default, current and earlier. Built-in
// graphs never change under a version id.
func BuiltinGraph(version string) (Graph, bool) {
	switch version {
	case DefaultVersion:
		return Default(), true
	case VersionV1:
		return defaultV1(), true
	case VersionV2:
		return defaultV2(), true
	case DocVersionV1:
		return defaultDocPage(), true
	case DefaultDocVersion:
		return defaultDocPageV2(), true
	}
	return Graph{}, false
}

// IsBuiltinVersion reports whether version names a built-in flow.
func IsBuiltinVersion(version string) bool {
	_, ok := BuiltinGraph(version)
	return ok
}

// DefaultFor is the built-in default flow of a subject ("" = work_item);
// unknown subjects get an empty graph, which never validates.
func DefaultFor(subject string) Graph {
	switch subject {
	case "", SubjectWorkItem:
		return Default()
	case SubjectDocPage:
		return defaultDocPageV2()
	}
	return Graph{}
}

// DefaultVersionFor identifies DefaultFor(subject) on runs that pin it ("" for
// unknown subjects).
func DefaultVersionFor(subject string) string {
	switch subject {
	case "", SubjectWorkItem:
		return DefaultVersion
	case SubjectDocPage:
		return DefaultDocVersion
	}
	return ""
}

// defaultDocPage (system:doc:1) is one Plan step on the page, then End: no
// triggers, no status changes. Kept unchanged for runs that pin it.
func defaultDocPage() Graph {
	return Graph{
		Subject: SubjectDocPage,
		Start:   "plan",
		Nodes: []Node{
			{Key: "plan", Type: NodeAgent, Name: "Plan", Skill: builtin.ID(activity.Answer)},
			{Key: "end", Type: NodeEnd, Name: "End"},
		},
		Edges: []Edge{{From: "plan", To: "end", On: activity.OutcomeDone}},
	}
}

// defaultDocPageV2 (system:doc:2) reconciles the page with its related
// pages and, when findings ask for changes, offers to apply them: Docs
// reconcile (asked when the page is published) → changes requested (ask) →
// Docs fix → End; no findings → End. No status changes.
func defaultDocPageV2() Graph {
	return Graph{
		Subject: SubjectDocPage,
		Start:   "reconcile",
		Nodes: []Node{
			{Key: "reconcile", Type: NodeAgent, Name: "Reconcile", Skill: builtin.IDOf("docs-reconcile"),
				PageTriggers: []activity.PageTrigger{{Event: activity.PageEventPublished, Mode: ModeAsk}}},
			{Key: "fix", Type: NodeAgent, Name: "Fix", Skill: builtin.IDOf("docs-fix")},
			{Key: "end", Type: NodeEnd, Name: "End"},
		},
		Edges: []Edge{
			{From: "reconcile", To: "fix", On: activity.OutcomeChangesRequested, Start: ModeAsk},
			{From: "reconcile", To: "end", On: activity.OutcomeApproved},
			{From: "fix", To: "end", On: activity.OutcomeDone},
		},
	}
}

// Default (system:3) is the built-in flow every organization inherits until
// a scope saves its own: Plan → [loop "Build and review": Build ⇄ Review,
// max 3 rounds, stop below high, advancing by itself, stopping when two
// rounds in a row leave the same blocking findings] → ⚑ In review → End.
// The loop hands the work to a person ("Needs a decision") when it stops,
// and so does a Review that fails, so a broken review ends somewhere
// visible. Plan → loop asks; Review always runs in the cloud, Build where
// the chain runs it. Board triggers are the former default rules.
func Default() Graph {
	return Graph{
		Start: "plan",
		Nodes: []Node{
			{Key: "plan", Type: NodeAgent, Name: "Plan", Skill: builtin.ID(activity.Answer)},
			{Key: "quality", Type: NodeLoop, Name: "Build and review", Body: []string{"build", "review"},
				Loop: &activity.LoopPolicy{MaxIterations: DefaultIterations, StopBelow: activity.High, StallRounds: DefaultStallRounds,
					StallMatch: activity.StallLocation, Advance: activity.LoopAdvanceAuto}},
			{Key: "build", Type: NodeAgent, Name: "Build", Skill: builtin.ID(activity.Code),
				Triggers: []Trigger{{FromStatus: "todo", ToStatus: "in_progress", ItemType: "*", Mode: ModeAsk}}},
			{Key: "review", Type: NodeAgent, Name: "Review", Skill: builtin.ID(activity.Findings), Location: activity.RuleCloud,
				Triggers: []Trigger{{FromStatus: "in_progress", ToStatus: "in_review", ItemType: "*", Mode: ModeAsk}}},
			{Key: "in_review", Type: NodeStatus, Name: "In review", Status: &activity.StatusTarget{Category: "in_review"}},
			{Key: "owner", Type: NodeApproval, Name: "Needs a decision"},
			{Key: "end", Type: NodeEnd, Name: "End"},
		},
		Edges: []Edge{
			{From: "plan", To: "quality", On: activity.OutcomeDone, Start: ModeAsk},
			// Inside the loop: no start, the loop's advance decides.
			{From: "build", To: "review", On: activity.OutcomeDone},
			{From: "review", To: "build", On: activity.OutcomeChangesRequested},
			{From: "review", To: "in_review", On: activity.OutcomeApproved},
			{From: "review", To: "owner", On: activity.OutcomeFailed},
			{From: "quality", To: "owner", On: activity.OutcomeExhausted},
			{From: "in_review", To: "end", On: activity.OutcomeDone},
		},
	}
}

// defaultV2 (system:2) is the second default: the graph of Default before
// Review got its failed exit, with the earlier node names and no stall
// match. Kept unchanged for runs that pin it.
func defaultV2() Graph {
	return Graph{
		Start: "plan",
		Nodes: []Node{
			{Key: "plan", Type: NodeAgent, Name: "Plan", Skill: builtin.ID(activity.Answer)},
			{Key: "quality", Type: NodeLoop, Name: "Quality loop", Body: []string{"build", "review"},
				Loop: &activity.LoopPolicy{MaxIterations: DefaultIterations, StopBelow: activity.High, StallRounds: DefaultStallRounds, Advance: activity.LoopAdvanceAuto}},
			{Key: "build", Type: NodeAgent, Name: "Build", Skill: builtin.ID(activity.Code),
				Triggers: []Trigger{{FromStatus: "todo", ToStatus: "in_progress", ItemType: "*", Mode: ModeAsk}}},
			{Key: "review", Type: NodeAgent, Name: "Review", Skill: builtin.ID(activity.Findings), Location: activity.RuleCloud,
				Triggers: []Trigger{{FromStatus: "in_progress", ToStatus: "in_review", ItemType: "*", Mode: ModeAsk}}},
			{Key: "in_review", Type: NodeStatus, Name: "In review", Status: &activity.StatusTarget{Category: "in_review"}},
			{Key: "owner", Type: NodeApproval, Name: "Owner decides"},
			{Key: "end", Type: NodeEnd, Name: "End"},
		},
		Edges: []Edge{
			{From: "plan", To: "quality", On: activity.OutcomeDone, Start: ModeAsk},
			// Inside the loop: no start, the loop's advance decides.
			{From: "build", To: "review", On: activity.OutcomeDone},
			{From: "review", To: "build", On: activity.OutcomeChangesRequested},
			{From: "review", To: "in_review", On: activity.OutcomeApproved},
			{From: "quality", To: "owner", On: activity.OutcomeExhausted},
			{From: "in_review", To: "end", On: activity.OutcomeDone},
		},
	}
}

// defaultV1 (system:1) is the first default: the graph of Default with
// every edge asking (inner loop edges carry start: ask, which Hop ignores; the
// loop has no advance, so they ask). Kept unchanged for runs that pin it.
func defaultV1() Graph {
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
