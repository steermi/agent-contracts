package skill_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/skill"
)

func TestResultDerivesTheRunner(t *testing.T) {
	cases := []struct {
		result    skill.Result
		kind      activity.Runner
		workspace bool
		inFlows   bool
		subjects  []string
	}{
		{skill.ResultAnswer, activity.Answer, false, true, []string{"organization", "workspace", "team", "project", "work_item"}},
		{skill.ResultCode, activity.Code, false, true, []string{"work_item"}},
		{skill.ResultFindings, activity.Findings, false, true, []string{"work_item"}},
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
}
