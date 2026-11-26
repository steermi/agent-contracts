package skill_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/skill"
	"github.com/steermi/agent-contracts/skill/builtin"
)

func TestResultDerivesTheRunner(t *testing.T) {
	cases := []struct {
		result    skill.Result
		kind      activity.Runner
		workspace bool
		inFlows   bool
		subjects  []string
	}{
		{skill.ResultAnswer, activity.Answer, false, true, []string{"organization", "workspace", "team", "project", "work_item", "doc_page"}},
		{skill.ResultCode, activity.Code, false, true, []string{"work_item"}},
		{skill.ResultFindings, activity.Findings, false, true, []string{"work_item", "doc_page"}},
		{skill.ResultRefinement, activity.Answer, true, false, []string{"team"}},
		{skill.ResultCyclePlanning, activity.Answer, true, false, []string{"team"}},
		{skill.ResultBoard, activity.Answer, true, false, []string{"team"}},
		{skill.ResultOrgDesign, activity.Answer, true, false, []string{"organization"}},

		{skill.ResultInitiativePlanning, activity.Answer, true, false, []string{"organization"}},
		{skill.ResultProjectDiscovery, activity.Answer, true, false, []string{"project"}},
		{skill.ResultProjectDocumentation, activity.Answer, true, false, []string{"project"}},
		{skill.ResultResearch, activity.Answer, true, false, []string{"project"}},
	}
	for _, c := range cases {
		assert.True(t, c.result.Valid(), c.result)
		assert.Equal(t, c.kind, c.result.Runner(), c.result)
		assert.Equal(t, c.workspace, c.result.Workspace(), c.result)
		assert.Equal(t, c.inFlows, c.result.InFlows(), c.result)
		assert.Equal(t, c.subjects, c.result.Subjects(), c.result)
	}
	assert.Len(t, skill.Results(), len(cases))
	assert.Empty(t, skill.Result("deploy").Runner(), "unknown results never get a runner")
	for _, r := range []skill.Result{skill.ResultAnswer, skill.ResultCode, skill.ResultFindings} {
		assert.Equal(t, string(r), string(r.Runner()), "a delivery result and its runner share one value")
	}
}

func TestStartsFromNarrows(t *testing.T) {
	m := skill.Manifest{Metadata: map[string]string{skill.StartsFromKey: "project, work_item, bogus"}}
	assert.Equal(t, []string{"project", "work_item"}, m.StartsFrom())
	assert.Equal(t, skill.ResultAnswer, m.Result(), "answer is the default")
	m.Metadata[skill.ResultKey] = "code"
	assert.Equal(t, []string{"work_item"}, m.StartsFrom())
	assert.Equal(t, "solo", m.Audience())
	m.Metadata[skill.ResultKey] = "findings"
	m.Metadata[skill.StartsFromKey] = "doc_page"
	assert.Equal(t, []string{skill.SubjectDocPage}, m.StartsFrom(), "a findings skill can be limited to docs pages")
}

// Docs pages are opt-in: without steermi-starts-from a skill starts wherever
// its result allows except docs pages.
func TestStartsFromDocPagesOptIn(t *testing.T) {
	answer := skill.Manifest{}
	assert.NotContains(t, answer.StartsFrom(), skill.SubjectDocPage)
	assert.Contains(t, answer.StartsFrom(), skill.SubjectWorkItem)
	findings := skill.Manifest{Metadata: map[string]string{skill.ResultKey: "findings"}}
	assert.Equal(t, []string{skill.SubjectWorkItem}, findings.StartsFrom())
	optIn := skill.Manifest{Metadata: map[string]string{skill.ResultKey: "findings", skill.StartsFromKey: "doc_page"}}
	assert.Equal(t, []string{skill.SubjectDocPage}, optIn.StartsFrom())
}

// Docs pages are opt-in subjects: a skill without steermi-starts-from
// starts from every subject its result allows except docs pages; Subjects
// keep them so a skill can ask.
func TestStartsFromLeavesOutOptInSubjects(t *testing.T) {
	for _, r := range skill.Results() {
		got := skill.Manifest{Metadata: map[string]string{skill.ResultKey: string(r)}}.StartsFrom()
		assert.NotContains(t, got, skill.SubjectDocPage, r)
		for _, s := range r.Subjects() {
			if s != skill.SubjectDocPage {
				assert.Contains(t, got, s, r)
			}
		}
	}
}

// Built-ins: only the docs skills start from docs pages; Plan and Review
// start where their result does by default.
func TestBuiltinsStartFromDocPagesOnlyWhenTheyAsk(t *testing.T) {
	for _, id := range builtin.IDs() {
		s, ok := builtin.Get(id)
		require.True(t, ok)
		docs := strings.HasPrefix(id, builtin.IDOf("docs-"))
		assert.Equal(t, docs, slices.Contains(s.StartsFrom(), skill.SubjectDocPage), id)
	}
}
