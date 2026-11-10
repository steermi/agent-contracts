package flow_test

import (
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/flow"
	"github.com/steermi/agent-contracts/skill"
)

// resolved resolves g; stored skill ids run as findings (Review) skills.
func resolved(g flow.Graph) flow.Resolved {
	r, _ := g.Resolve(func(string) (skill.Result, bool) { return skill.ResultFindings, true })
	return r
}

func TestDefaultIsValid(t *testing.T) {
	g := flow.DefaultResolved()
	require.NoError(t, g.Validate(flow.Options{}))
	assert.False(t, g.HasAuto(), "every default edge asks")
}

func node(g *flow.Graph, key string) *flow.Node {
	for i := range g.Nodes {
		if g.Nodes[i].Key == key {
			return &g.Nodes[i]
		}
	}
	panic(key)
}

func edge(g *flow.Graph, from string, on activity.Outcome) *flow.Edge {
	for i := range g.Edges {
		if g.Edges[i].From == from && g.Edges[i].On == on {
			return &g.Edges[i]
		}
	}
	panic(from)
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*flow.Graph)
		opt   flow.Options
		field string
	}{
		{"empty", func(g *flow.Graph) { g.Nodes = nil }, flow.Options{}, "nodes"},
		{"bad key", func(g *flow.Graph) { node(g, "plan").Key = "Plan"; g.Start = "Plan" }, flow.Options{}, "key"},
		{"duplicate key", func(g *flow.Graph) { node(g, "end").Key = "owner" }, flow.Options{}, "key"},
		{"no skill", func(g *flow.Graph) { node(g, "plan").Skill = "" }, flow.Options{}, "skill"},
		{"unresolved stored skill", func(g *flow.Graph) { node(g, "plan").Skill = "6f1c" }, flow.Options{}, "skill"},
		{"status id at org", func(g *flow.Graph) { node(g, "build").Triggers[0].ToStatusID = "s1" }, flow.Options{}, "triggers"},
		{"status node id at org", func(g *flow.Graph) { node(g, "in_review").Status.StatusID = "s1" }, flow.Options{}, "status"},
		{"duplicate trigger", func(g *flow.Graph) { node(g, "review").Triggers = node(g, "build").Triggers }, flow.Options{}, "triggers"},
		{"bad mode", func(g *flow.Graph) { node(g, "build").Triggers[0].Mode = "always" }, flow.Options{}, "triggers"},
		{"checks on plan", func(g *flow.Graph) { node(g, "plan").Requires.Checks = []string{"ci"} }, flow.Options{}, "requires"},
		{"unknown edge node", func(g *flow.Graph) { edge(g, "plan", activity.OutcomeDone).To = "deploy" }, flow.Options{}, "edges"},
		{"wrong outcome", func(g *flow.Graph) { edge(g, "plan", activity.OutcomeDone).On = activity.OutcomeApproved }, flow.Options{}, "edges"},
		{"unknown start", func(g *flow.Graph) { edge(g, "build", activity.OutcomeDone).Start = "later" }, flow.Options{}, "edges"},
		{"auto into status", func(g *flow.Graph) { edge(g, "review", activity.OutcomeApproved).Start = flow.ModeAuto }, flow.Options{}, "edges"},
		{"enter loop body", func(g *flow.Graph) { edge(g, "plan", activity.OutcomeDone).To = "build" }, flow.Options{}, "edges"},
		{"no exhausted exit", func(g *flow.Graph) { g.Edges = g.Edges[:4] }, flow.Options{}, "edges"},
		{"cycle outside loop", func(g *flow.Graph) {
			g.Edges = append(g.Edges, flow.Edge{From: "plan", To: "plan", On: activity.OutcomeChangesRequested})
		}, flow.Options{}, "edges"},
		{"long cycle outside loop", func(g *flow.Graph) {
			node(g, "quality").Body = []string{"build"}
			*edge(g, "build", activity.OutcomeDone) = flow.Edge{From: "build", To: "build", On: activity.OutcomeChangesRequested}
			*edge(g, "review", activity.OutcomeChangesRequested) = flow.Edge{From: "review", To: "plan", On: activity.OutcomeChangesRequested}
			g.Edges = append(g.Edges, flow.Edge{From: "plan", To: "review", On: activity.OutcomeChangesRequested})
		}, flow.Options{}, "edges"},
		{"loop policy", func(g *flow.Graph) { node(g, "quality").Loop.MaxIterations = 9 }, flow.Options{}, "loop"},
		{"loop severity", func(g *flow.Graph) { node(g, "quality").Loop.StopBelow = "blocker" }, flow.Options{}, "loop"},
		{"start in loop", func(g *flow.Graph) { g.Start = "build" }, flow.Options{}, "start"},
		{"unreachable", func(g *flow.Graph) {
			g.Nodes = append(g.Nodes, flow.Node{Key: "docs", Type: flow.NodeAgent, Skill: "steermi:plan"})
		}, flow.Options{}, "key"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := flow.Default()
			tt.edit(&g)
			r, _ := g.Resolve(nil)
			err := r.Validate(tt.opt)
			require.ErrorIs(t, err, flow.ErrInvalid)
			var issues flow.Issues
			require.True(t, errors.As(err, &issues))
			fields := []string{}
			for _, i := range issues {
				fields = append(fields, i.Field)
			}
			assert.Contains(t, fields, tt.field, issues.Error())
		})
	}
}

func TestOptionalFeaturesValidWhenAllowed(t *testing.T) {
	g := flow.Default()
	node(&g, "build").Triggers[0].ToStatusID = "s1"
	edge(&g, "build", activity.OutcomeDone).Start = flow.ModeAuto
	node(&g, "review").Skill = "11111111-1111-1111-1111-111111111111"
	require.NoError(t, resolved(g).Validate(flow.Options{AllowStatusIDs: true}))
	assert.True(t, g.HasAuto())
	assert.Equal(t, []string{"11111111-1111-1111-1111-111111111111"}, g.SkillIDs())
}

func TestRequiredNodesInherited(t *testing.T) {
	parent := flow.Default()
	node(&parent, "review").Required = true
	child := flow.Default()
	node(&child, "review").Skill = "11111111-1111-1111-1111-111111111111"
	node(&child, "review").Required = true
	require.ErrorIs(t, resolved(child).Validate(flow.Options{Parent: &parent}), flow.ErrInvalid)
	node(&child, "review").Skill = "steermi:review"
	require.NoError(t, resolved(child).Validate(flow.Options{Parent: &parent}))
	node(&child, "review").Required = false
	require.ErrorIs(t, resolved(child).Validate(flow.Options{Parent: &parent}), flow.ErrInvalid)
}

func TestRefResolvesHopsAndLoopState(t *testing.T) {
	g := flow.DefaultResolved()
	plan, ok := g.Ref("v1", "plan", nil)
	require.True(t, ok)
	assert.Nil(t, plan.Loop)
	assert.Equal(t, activity.FlowHop{Node: "build", Name: "Build", Runner: activity.Code}, plan.Hops[activity.OutcomeDone], "entering the loop starts its first step")

	build, ok := g.Ref("v1", "build", &activity.Run{Runner: activity.Answer, Cost: 0.5, Flow: &plan})
	require.True(t, ok)
	require.NotNil(t, build.Loop)
	assert.Equal(t, 1, build.Loop.Iteration)
	assert.Equal(t, 0.5, build.Loop.StartCost)
	assert.Equal(t, activity.FlowHop{Stop: "owner", Approval: true}, build.Loop.Exhausted)
	assert.Equal(t, activity.FlowHop{Node: "review", Name: "Review", Runner: activity.Findings}, build.Hops[activity.OutcomeDone])

	out := []activity.OutputRef{{Kind: activity.RefRepository, RepositoryID: "r", CommitSHA: "0123456789012345678901234567890123456789"}}
	review, _ := g.Ref("v1", "review", &activity.Run{Runner: activity.Code, Outputs: out, Flow: &build})
	assert.Equal(t, 1, review.Loop.Iteration)
	assert.Equal(t, activity.RevisionDigest(out), review.Loop.Revision)
	assert.Equal(t, activity.FlowHop{Node: "build", Name: "Build", Runner: activity.Code, Back: true}, review.Hops[activity.OutcomeChangesRequested])
	assert.Equal(t, activity.FlowHop{Statuses: []activity.StatusTarget{{Category: "in_review"}}, Stop: "end"}, review.Hops[activity.OutcomeApproved])

	findings := []activity.Finding{{ID: "1", Severity: activity.High, Description: "bug"}}
	again, _ := g.Ref("v1", "build", &activity.Run{Runner: activity.Findings, Findings: findings, Flow: &review})
	assert.Equal(t, 2, again.Loop.Iteration, "the back edge starts round 2")
	assert.Equal(t, []string{activity.Fingerprint(findings)}, again.Loop.Fingerprints)
	assert.Empty(t, review.Loop.Fingerprints, "the parent state is not mutated")

	retry, _ := g.Ref("v1", "build", &activity.Run{Runner: activity.Code, Flow: &again})
	assert.Equal(t, 2, retry.Loop.Iteration, "a retry stays in the round")

	_, ok = g.Ref("v1", "quality", nil)
	assert.False(t, ok, "runs sit on agent steps only")

	rules := g.Rules()
	require.Len(t, rules, 2)
	assert.Equal(t, "build", rules[0].Node)
	assert.Equal(t, activity.Code, rules[0].Rule.Runner)

	p := flow.Requirements{Acceptance: true, Checks: []string{"ci"}}.Policy(activity.Policy{RequiredChecks: []string{"ci"}})
	assert.True(t, p.RequireAcceptance)
	assert.Equal(t, []string{"ci"}, p.RequiredChecks)
}

func TestLoopEndToEndThroughContracts(t *testing.T) {
	g := flow.DefaultResolved()
	owner := activity.Actor{UserID: "o", Role: "member"}
	ref, _ := g.Ref("v", "build", nil)
	run := activity.Run{ID: "b1", Runner: activity.Code, State: activity.Submitted, Version: 1, OwnerID: "o", Location: activity.Cloud, Flow: &ref,
		Outputs: []activity.OutputRef{{Kind: activity.RefRepository, ResourceID: "r", RepositoryID: "r", CommitSHA: "0123456789012345678901234567890123456789"}}}
	for round := 1; round <= 3; round++ {
		kind, err := run.Successor(owner, activity.Command{Type: activity.Next, ExpectedVersion: 1})
		require.NoError(t, err)
		require.Equal(t, activity.Findings, kind)
		rref, _ := g.Ref("v", run.SuccessorNode(activity.Next), &run)
		review := activity.Run{ID: "r", Runner: activity.Findings, State: activity.Submitted, Version: 1, OwnerID: "o", Location: activity.Cloud, Flow: &rref,
			Recommendation: activity.ChangesRequested, Findings: []activity.Finding{{ID: "f", Severity: activity.Critical, Description: "round " + string(rune('0'+round))}}}
		hop, stop, _ := review.FlowNext()
		if round == 3 {
			assert.Equal(t, activity.BlockerRepairLimit, stop)
			assert.True(t, hop.Approval)
			return
		}
		require.Equal(t, activity.Blocker(""), stop)
		bref, _ := g.Ref("v", hop.Node, &review)
		run = activity.Run{ID: "b", Runner: activity.Code, State: activity.Submitted, Version: 1, OwnerID: "o", Location: activity.Cloud, Flow: &bref,
			Outputs: []activity.OutputRef{{Kind: activity.RefRepository, ResourceID: "r", RepositoryID: "r", CommitSHA: "1123456789012345678901234567890123456789"[:39] + string(rune('0'+round))}}}
	}
	t.Fatal("the loop never stopped")
}

func TestDecodeIsStrict(t *testing.T) {
	var g flow.Graph
	require.Error(t, json.Unmarshal([]byte(`{"start":"a","nodes":[],"edges":[],"x":1}`), &g))
	require.Error(t, json.Unmarshal([]byte(`{"steps":[{"key":"plan","activity":"plan"}]}`), &g), "v1 step lists are not flows")
	raw, err := json.Marshal(flow.Default())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &g))
	assert.Equal(t, flow.Default(), g)
}

func TestBackHopOnlyStraightIntoTheLoopEntry(t *testing.T) {
	g := flow.DefaultResolved()
	back := g.Hop(*edge(&g.Graph, "review", activity.OutcomeChangesRequested))
	assert.True(t, back.Back)
	assert.False(t, g.Hop(*edge(&g.Graph, "build", activity.OutcomeDone)).Back, "build → review stays in the round")
	assert.False(t, g.Hop(*edge(&g.Graph, "plan", activity.OutcomeDone)).Back, "entering the loop is round 1")
}

func TestSimulateUsesTheRuntimeStopTable(t *testing.T) {
	g := flow.DefaultResolved()
	happy := g.Simulate(nil, "", 1)
	assert.Equal(t, flow.SimEnd, happy.EndKind)
	assert.Equal(t, []string{"plan", "build", "review"}, simNodes(happy))
	assert.Equal(t, []activity.StatusTarget{{Category: "in_review"}}, happy.Steps[2].Statuses)
	assert.Equal(t, 3.0, happy.Cost)

	limit := g.Simulate(flow.Script{"review": {"changes_requested"}}, "", 1)
	assert.Equal(t, flow.SimApproval, limit.EndKind)
	assert.Equal(t, "owner", limit.Ends)
	last := limit.Steps[len(limit.Steps)-1]
	assert.Equal(t, activity.BlockerRepairLimit, last.Stop)
	assert.Equal(t, 3, last.Iteration)

	stalled := g.Simulate(flow.Script{"review": {"changes_requested", flow.SameAsBefore}}, "", 1)
	assert.Equal(t, activity.BlockerLoopStalled, stalled.Steps[len(stalled.Steps)-1].Stop)

	sameBuild := g.Simulate(flow.Script{"review": {"changes_requested"}, "build": {"done", flow.SameAsBefore}}, "", 1)
	assert.Equal(t, activity.BlockerLoopStalled, sameBuild.Steps[len(sameBuild.Steps)-1].Stop, "an unchanged revision stops the loop")
	assert.Equal(t, "build", sameBuild.Steps[len(sameBuild.Steps)-1].Node)

	quality := node(&g.Graph, "quality")
	quality.Loop.Budget = 2
	budget := g.Simulate(flow.Script{"review": {"changes_requested"}}, "quality", 1)
	assert.Equal(t, activity.BlockerBudget, budget.Steps[len(budget.Steps)-1].Stop)

	failed := g.Simulate(flow.Script{"plan": {"failed"}}, "", 1)
	assert.Equal(t, flow.SimOpen, failed.EndKind, "no failed edge")
}

func simNodes(r flow.SimResult) []string {
	out := []string{}
	for _, s := range r.Steps {
		out = append(out, s.Node)
	}
	return out
}

func TestWorstCaseCost(t *testing.T) {
	assert.Equal(t, 60.0, flow.WorstCaseCost(activity.LoopPolicy{MaxIterations: 3}, 20))
	assert.Equal(t, 5.0, flow.WorstCaseCost(activity.LoopPolicy{MaxIterations: 3, Budget: 5}, 20))
}

func TestRefNeverPlacesOnAnIncompleteLoop(t *testing.T) {
	g := flow.DefaultResolved()
	_, ok := g.Ref("v", "build", nil)
	require.True(t, ok)
	for i := range g.Nodes {
		if g.Nodes[i].Key == "quality" {
			g.Nodes[i].Loop = nil
		}
	}
	_, ok = g.Ref("v", "build", nil)
	assert.False(t, ok, "a loop without its policy has no stop table")
	g = flow.DefaultResolved()
	g.Edges = slices.DeleteFunc(g.Edges, func(e flow.Edge) bool { return e.On == activity.OutcomeExhausted })
	_, ok = g.Ref("v", "build", nil)
	assert.False(t, ok, "a loop without an exhausted exit cannot stop")
}

func TestResolve(t *testing.T) {
	g := flow.Default()
	node(&g, "review").Skill = "sec-review"
	node(&g, "plan").Skill = "retro-ours"
	results := func(id string) (skill.Result, bool) {
		switch id {
		case "sec-review":
			return skill.ResultFindings, true
		case "retro-ours":
			return skill.ResultBoard, true
		}
		return "", false
	}
	r, issues := g.Resolve(results)
	require.Len(t, issues, 1, "workspace skills cannot be flow steps")
	assert.Equal(t, "plan", issues[0].Node)
	assert.Equal(t, activity.Findings, r.Runner("review"), "a stored skill runs as its result's kind")
	assert.Empty(t, r.Runner("plan"))
	assert.Error(t, r.Validate(flow.Options{}))
	assert.Equal(t, "sec-review", node(&g, "review").Skill, "resolving never edits the graph")

	node(&g, "plan").Skill = "gone"
	_, issues = g.Resolve(results)
	assert.Len(t, issues, 1, "unknown skills are reported")

	def, issues := flow.Default().Resolve(nil)
	require.Empty(t, issues)
	assert.Equal(t, flow.Default(), def.Graph)
	assert.Equal(t, activity.Code, def.Runner("build"))

	raw, err := json.Marshal(def)
	require.NoError(t, err)
	var back flow.Graph
	require.NoError(t, json.Unmarshal(raw, &back), "a resolution encodes as its graph")
	assert.Equal(t, flow.Default(), back)
	assert.Error(t, json.Unmarshal(raw, &def), "a resolution is never decoded")
}

func TestStoredGraphsCarryNoActivity(t *testing.T) {
	data, err := json.Marshal(flow.Default())
	require.NoError(t, err)
	assert.NotContains(t, string(data), `"activity"`)
	var g flow.Graph
	assert.Error(t, json.Unmarshal([]byte(`{"start":"a","nodes":[{"key":"a","type":"agent","activity":"plan","skill":"steermi:plan"}],"edges":[]}`), &g), "graph v2 is rejected")
}
