package flow_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/flow"
	"github.com/steermi/agent-contracts/skill"
	"github.com/steermi/agent-contracts/skill/builtin"
)

func TestDefaultsPerSubject(t *testing.T) {
	assert.Equal(t, flow.Default(), flow.DefaultFor(""), "an unset subject is a work item")
	assert.Equal(t, flow.Default(), flow.DefaultFor(flow.SubjectWorkItem))
	assert.Equal(t, flow.DefaultVersion, flow.DefaultVersionFor(""))
	assert.Equal(t, "system:3", flow.DefaultVersionFor(flow.SubjectWorkItem))
	assert.Equal(t, "system:doc:2", flow.DefaultVersionFor(flow.SubjectDocPage))
	assert.Empty(t, flow.DefaultVersionFor("repository"))
	assert.Empty(t, flow.DefaultFor("repository").Nodes, "unknown subjects have no default")
	assert.Empty(t, flow.Default().Subject, "the work item default stays byte-identical")

	doc := flow.DefaultFor(flow.SubjectDocPage)
	assert.Equal(t, flow.SubjectDocPage, doc.Subject)
	r, issues := doc.Resolve(nil)
	require.Empty(t, issues)
	require.NoError(t, r.Validate(flow.Options{}))
	assert.False(t, r.HasAutoOutsideLoops())
	assert.Empty(t, r.Rules())
	assert.Equal(t, []flow.PageStepRule{
		{Node: "reconcile", Runner: activity.Findings, Trigger: activity.PageTrigger{Event: activity.PageEventPublished, Mode: flow.ModeAsk}},
	}, r.PageRules(), "publishing a page asks to reconcile it")
	assert.Equal(t, "reconcile", doc.Start)
	step, ok := r.FirstOf(activity.Findings)
	require.True(t, ok)
	assert.Equal(t, "steermi:docs-reconcile", step.Skill)
	fix, ok := r.FirstOf(activity.Answer)
	require.True(t, ok)
	assert.Equal(t, "steermi:docs-fix", fix.Skill)

	ref, ok := r.Ref(flow.DefaultVersionFor(flow.SubjectDocPage), step.Key, nil)
	require.True(t, ok)
	assert.Equal(t, "end", ref.Hops[activity.OutcomeApproved].Stop, "no findings: done")
	toFix := ref.Hops[activity.OutcomeChangesRequested]
	assert.Equal(t, "fix", toFix.Node, "findings: ask to apply them")
	assert.False(t, toFix.Auto)
	fixRef, ok := r.Ref(flow.DefaultVersionFor(flow.SubjectDocPage), "fix", nil)
	require.True(t, ok)
	assert.Equal(t, "end", fixRef.Hops[activity.OutcomeDone].Stop)
}

func TestBuiltinGraphs(t *testing.T) {
	for version, want := range map[string]flow.Graph{
		flow.DefaultVersion:    flow.Default(),
		flow.DefaultDocVersion: flow.DefaultFor(flow.SubjectDocPage),
	} {
		got, ok := flow.BuiltinGraph(version)
		require.True(t, ok, version)
		assert.Equal(t, want, got, version)
		r, issues := got.Resolve(nil)
		require.Empty(t, issues, version)
		require.NoError(t, r.Validate(flow.Options{}), version)
	}
	assert.Equal(t, "system:doc:1", flow.DocVersionV1)
	v1, ok := flow.BuiltinGraph("system:doc:1")
	require.True(t, ok)
	assert.Equal(t, flow.SubjectDocPage, v1.Subject)
	r, issues := v1.Resolve(nil)
	require.Empty(t, issues)
	require.NoError(t, r.Validate(flow.Options{}))
	step, ok := r.FirstOf(activity.Answer)
	require.True(t, ok)
	assert.Equal(t, "steermi:plan", step.Skill, "pinned v1 runs keep their one Plan step")
	assert.Empty(t, r.PageRules())
	for _, bad := range []string{"", "system:4", "system:doc:3", "flow-version-uuid"} {
		_, ok := flow.BuiltinGraph(bad)
		assert.False(t, ok, bad)
	}
	assert.True(t, flow.IsBuiltinVersion("system:doc:1"))
	assert.False(t, flow.IsBuiltinVersion("system:doc:3"))
}

// docGraph is a docs page flow: an answer step, then a findings step.
func docGraph() flow.Graph {
	return flow.Graph{
		Subject: flow.SubjectDocPage,
		Start:   "draft",
		Nodes: []flow.Node{
			{Key: "draft", Type: flow.NodeAgent, Skill: "steermi:plan", PageTriggers: []activity.PageTrigger{{Event: activity.PageEventUpdated, Mode: flow.ModeAsk}}},
			{Key: "check", Type: flow.NodeAgent, Skill: "steermi:review", PageTriggers: []activity.PageTrigger{{Event: activity.PageEventPublished, Mode: flow.ModeAsk}}},
			{Key: "owner", Type: flow.NodeApproval},
			{Key: "end", Type: flow.NodeEnd},
		},
		Edges: []flow.Edge{
			{From: "draft", To: "check", On: activity.OutcomeDone, Start: flow.ModeAuto},
			{From: "check", To: "end", On: activity.OutcomeApproved},
			{From: "check", To: "owner", On: activity.OutcomeChangesRequested},
		},
	}
}

func TestDocPageFlowValidation(t *testing.T) {
	g := docGraph()
	r, issues := g.Resolve(nil)
	require.Empty(t, issues)
	require.NoError(t, r.Validate(flow.Options{}))
	assert.Equal(t, []flow.PageStepRule{
		{Node: "draft", Runner: activity.Answer, Trigger: activity.PageTrigger{Event: activity.PageEventUpdated, Mode: flow.ModeAsk}},
		{Node: "check", Runner: activity.Findings, Trigger: activity.PageTrigger{Event: activity.PageEventPublished, Mode: flow.ModeAsk}},
	}, r.PageRules())

	tests := []struct {
		name  string
		edit  func(*flow.Graph)
		field string
	}{
		{"status node", func(g *flow.Graph) {
			g.Nodes = append(g.Nodes, flow.Node{Key: "published", Type: flow.NodeStatus, Status: &activity.StatusTarget{Category: "done"}})
			g.Edges[1].To = "published"
			g.Edges = append(g.Edges, flow.Edge{From: "published", To: "end", On: activity.OutcomeDone})
		}, "type"},
		{"status trigger", func(g *flow.Graph) {
			node(g, "draft").Triggers = []flow.Trigger{{FromStatus: "todo", ToStatus: "in_progress", ItemType: "*", Mode: flow.ModeAsk}}
		}, "triggers"},
		{"code step", func(g *flow.Graph) { node(g, "draft").Skill = "steermi:build" }, "skill"},
		{"unknown event", func(g *flow.Graph) { node(g, "draft").PageTriggers[0].Event = "deleted" }, "page_triggers"},
		{"bad mode", func(g *flow.Graph) { node(g, "draft").PageTriggers[0].Mode = "always" }, "page_triggers"},
		// A page trigger run has no automation owner: it is always suggested.
		{"auto page trigger", func(g *flow.Graph) { node(g, "check").PageTriggers[0].Mode = flow.ModeAuto }, "page_triggers"},
		{"duplicate event", func(g *flow.Graph) { node(g, "check").PageTriggers[0].Event = activity.PageEventUpdated }, "page_triggers"},
		{"page trigger off an agent", func(g *flow.Graph) {
			node(g, "end").PageTriggers = []activity.PageTrigger{{Event: activity.PageEventUpdated, Mode: flow.ModeAsk}}
		}, "type"},
		{"unknown subject", func(g *flow.Graph) { g.Subject = "repository" }, "subject"},
		// A docs page run has no team or project: only its owner decides.
		{"team managers decide", func(g *flow.Graph) { node(g, "owner").Approver = activity.ApproverTeamManagers }, "approver"},
		{"reviewers decide", func(g *flow.Graph) { node(g, "owner").Approver = activity.ApproverReviewers }, "approver"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := docGraph()
			tc.edit(&g)
			err := resolved(g).Validate(flow.Options{})
			require.ErrorIs(t, err, flow.ErrInvalid)
			var issues flow.Issues
			require.ErrorAs(t, err, &issues)
			fields := []string{}
			for _, i := range issues {
				fields = append(fields, i.Field)
			}
			assert.Contains(t, fields, tc.field)
		})
	}
}

func TestDocPageApprovalDecidedByOwner(t *testing.T) {
	g := docGraph()
	g.Edges = append(g.Edges, flow.Edge{From: "owner", To: "end", On: activity.OutcomeApproved})
	node(&g, "owner").Approver = activity.ApproverOwner
	require.NoError(t, resolved(g).Validate(flow.Options{}), "the run's owner decides at a docs page approval")
}

func TestWorkItemFlowRefusesPageTriggers(t *testing.T) {
	g := flow.Default()
	node(&g, "plan").PageTriggers = []activity.PageTrigger{{Event: activity.PageEventPublished, Mode: flow.ModeAsk}}
	err := resolved(g).Validate(flow.Options{})
	require.ErrorIs(t, err, flow.ErrInvalid)
	assert.Contains(t, err.Error(), "page_triggers")
}

func TestWorkItemSubjectIsLeftEmpty(t *testing.T) {
	g := flow.Default()
	g.Subject = flow.SubjectWorkItem
	err := resolved(g).Validate(flow.Options{})
	require.ErrorIs(t, err, flow.ErrInvalid, "one encoding and digest per flow")
	assert.Contains(t, err.Error(), "subject")
	assert.Equal(t, flow.SubjectWorkItem, g.SubjectKind())
}

func TestInheritedFlowKeepsItsSubject(t *testing.T) {
	work, doc := flow.Default(), docGraph()
	err := resolved(doc).Validate(flow.Options{Parent: &work})
	require.ErrorIs(t, err, flow.ErrInvalid)
	assert.Contains(t, err.Error(), "subject")
	err = resolved(flow.Default()).Validate(flow.Options{Parent: &doc})
	require.ErrorIs(t, err, flow.ErrInvalid)
	assert.Contains(t, err.Error(), "subject")
	same := docGraph()
	assert.NoError(t, resolved(docGraph()).Validate(flow.Options{Parent: &same}))
}

func TestPageTriggersOnlyCompileForDocFlows(t *testing.T) {
	assert.Empty(t, resolved(docGraph()).Rules(), "docs page flows have no board rules")
	assert.Empty(t, flow.DefaultResolved().PageRules())
	assert.NotEmpty(t, flow.DefaultResolved().Rules())
}

func TestNonAgentPageTriggersReportedOnce(t *testing.T) {
	g := docGraph()
	node(&g, "end").PageTriggers = []activity.PageTrigger{{Event: "deleted", Mode: "always"}}
	var issues flow.Issues
	require.ErrorAs(t, resolved(g).Validate(flow.Options{}), &issues)
	assert.Len(t, issues, 1)
	assert.Equal(t, "type", issues[0].Field)
}

func TestPageTriggersReachSteps(t *testing.T) {
	g := docGraph()
	g.Edges = g.Edges[1:] // check is reached only by its page trigger
	require.NoError(t, resolved(g).Validate(flow.Options{}))
	node(&g, "check").PageTriggers = nil
	assert.Error(t, resolved(g).Validate(flow.Options{}), "an unreachable step without a trigger is refused")
}

func TestStoredDocFlowsResolveStoredSkills(t *testing.T) {
	g := docGraph()
	node(&g, "check").Skill = "doc-lint"
	r, issues := g.Resolve(func(string) (skill.Result, bool) { return skill.ResultCode, true })
	require.Len(t, issues, 1, "resolving reports a code skill on a page")
	assert.Equal(t, "check", issues[0].Node)
	assert.Empty(t, r.Runner("check"))
	assert.ErrorIs(t, r.Validate(flow.Options{}), flow.ErrInvalid, "a stored code skill cannot run on a page")
	_, issues = flow.Default().Resolve(func(string) (skill.Result, bool) { return skill.ResultCode, true })
	assert.Empty(t, issues, "work item flows keep code steps")
}

func TestSimulateDocPageFlow(t *testing.T) {
	r := resolved(docGraph())
	happy := simulate(r, nil, "", 1)
	assert.Equal(t, simEnd, happy.EndKind)
	assert.Equal(t, []string{"draft", "check"}, simNodes(happy))
	assert.True(t, happy.Steps[0].Auto)
	assert.Empty(t, happy.Steps[1].Statuses)

	changes := simulate(r, script{"check": {"changes_requested"}}, "", 1)
	assert.Equal(t, simApproval, changes.EndKind)
	assert.Equal(t, "owner", changes.Ends)
}

// Where a person starts a skill (skill.Manifest.StartsFrom) does not limit
// flow steps: a docs page flow's author places Plan on the page explicitly,
// and pinned system:doc:1 runs keep their Plan step. Only the result
// decides (no code skills on a page).
func TestDocPageFlowStepsIgnoreStartsFrom(t *testing.T) {
	plan, ok := builtin.Get(builtin.ID(activity.Answer))
	require.True(t, ok)
	require.NotContains(t, plan.StartsFrom(), skill.SubjectDocPage)
	g := flow.Graph{Subject: flow.SubjectDocPage, Start: "plan", Nodes: []flow.Node{
		{Key: "plan", Type: flow.NodeAgent, Skill: builtin.ID(activity.Answer), PageTriggers: []activity.PageTrigger{{Event: activity.PageEventUpdated, Mode: flow.ModeAsk}}},
		{Key: "end", Type: flow.NodeEnd},
	}, Edges: []flow.Edge{{From: "plan", To: "end", On: activity.OutcomeDone}}}
	r, issues := g.Resolve(nil)
	require.Empty(t, issues)
	assert.NoError(t, r.Validate(flow.Options{}))
}
