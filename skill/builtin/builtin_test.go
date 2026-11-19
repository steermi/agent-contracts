package builtin_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/hint"
	"github.com/steermi/agent-contracts/skill"
	"github.com/steermi/agent-contracts/skill/builtin"
)

func TestEveryRunnerHasAValidBuiltin(t *testing.T) {
	slugs := map[activity.Runner]string{activity.Answer: "plan", activity.Code: "build", activity.Findings: "review"}
	for _, kind := range builtin.Runners() {
		s, ok := builtin.For(kind)
		require.True(t, ok, kind)
		require.NoError(t, s.Validate(), kind)
		assert.Equal(t, kind, s.Runner(), "the built-in's result runs on its runner")
		assert.Equal(t, slugs[kind], s.Name(), "built-ins keep their Plan/Build/Review names")
		assert.Equal(t, "steermi:"+slugs[kind], builtin.ID(kind))
		assert.Empty(t, s.AllowedTools(), "built-ins never narrow the activity's tools")
		assert.NotEmpty(t, s.Prompt())

		ref := builtin.Ref(kind)
		assert.Equal(t, builtin.ID(kind), ref.ID)
		assert.Equal(t, builtin.Version, ref.VersionID)
		assert.Equal(t, s.Digest(), ref.Digest, "the pin covers every file")
	}
}

func TestIDs(t *testing.T) {
	k, ok := builtin.RunnerOf(builtin.ID(activity.Findings))
	require.True(t, ok)
	assert.Equal(t, activity.Findings, k)
	for _, id := range []string{"review", "steermi:", "steermi:findings", "steermi:code", "steermi:deploy", "other:plan"} {
		_, ok := builtin.RunnerOf(id)
		assert.False(t, ok, id)
		_, ok = builtin.Get(id)
		assert.False(t, ok, id)
	}
	_, ok = builtin.For("deploy")
	assert.False(t, ok)
	assert.Nil(t, builtin.Ref("deploy"))
}

func TestChangelogCoversTheVersion(t *testing.T) {
	require.NotEmpty(t, builtin.Changelog)
	assert.Equal(t, builtin.Version, builtin.Changelog[0].Version, "bump Version and add its changelog entry together")
}

func TestDuplicateName(t *testing.T) {
	s, _ := builtin.For(activity.Answer)
	assert.Equal(t, "plan-custom", builtin.DuplicateName(s))
}

func TestEveryBuiltinIsValid(t *testing.T) {
	require.Len(t, builtin.IDs(), 13)
	for _, id := range builtin.IDs() {
		s, ok := builtin.Get(id)
		require.True(t, ok, id)
		require.NoError(t, s.Validate(), id)
		assert.Equal(t, id, builtin.IDOf(s.Name()), "the folder, slug and name agree")
		assert.True(t, s.Result().Valid(), id)
		assert.NotEmpty(t, s.StartsFrom(), id)
		got, ok := builtin.ForResult(s.Result())
		require.True(t, ok, id)
		if s.Result().Workspace() {
			assert.Equal(t, id, got, "one built-in per workspace")
		}
		delivery := slices.Contains([]string{"steermi:plan", "steermi:build", "steermi:review"}, id)
		assert.Equal(t, delivery, builtin.Required(id), "only the runner built-ins are required")
		assert.NotNil(t, builtin.RefOf(id))
	}
	assert.False(t, builtin.Known("steermi:deploy"))
}

func TestRefinementInputsSchema(t *testing.T) {
	s, ok := builtin.Get(builtin.IDOf("refinement"))
	require.True(t, ok)
	require.True(t, s.HasInputs(), "refinement ships a start form")
	schema, err := s.Inputs()
	require.NoError(t, err)

	filled, err := schema.Validate([]byte("{}"))
	require.NoError(t, err)
	var value struct {
		Pool      string `json:"pool"`
		QueueSize int    `json:"queue_size"`
	}
	require.NoError(t, json.Unmarshal(filled, &value))
	assert.Equal(t, "backlog_and_planned", value.Pool)
	assert.Equal(t, 5, value.QueueSize)

	refs := schema.Refs([]byte(`{"items":["0b6fd0a4-6a8e-4d5e-9c3e-2f0e2a1b9c11"]}`))
	require.Len(t, refs, 1)
	assert.Equal(t, "work_item", refs[0].Kind)

	_, err = schema.Validate([]byte(`{"pool":"everything"}`))
	assert.Error(t, err, "pool is a closed enum")
}

func TestRunnersListEachRunnerOnce(t *testing.T) {
	assert.Equal(t, []activity.Runner{activity.Answer, activity.Code, activity.Findings}, builtin.Runners())
	assert.Equal(t, "steermi:plan", builtin.ID(activity.Answer), "the delivery built-in stays first")
	assert.Equal(t, "steermi:review", builtin.ID(activity.Findings))
}

func TestDocsSkills(t *testing.T) {
	reconcile, ok := builtin.Get("steermi:docs-reconcile")
	require.True(t, ok)
	require.NoError(t, reconcile.Validate())
	assert.Equal(t, activity.Findings, reconcile.Runner())
	assert.Equal(t, []string{"doc_page"}, reconcile.StartsFrom())
	assert.Equal(t, hint.Medium, reconcile.Effort())
	assert.Equal(t, []string{skill.SourceRelatedDocs}, reconcile.Sources())
	k, ok := builtin.RunnerOf("steermi:docs-reconcile")
	require.True(t, ok, "docs skills run as flow steps")
	assert.Equal(t, activity.Findings, k)
	for _, word := range []string{"subject_quote", "target_quote", "suggestion.action", "verbatim", "submit_review_findings"} {
		assert.Contains(t, reconcile.Prompt(), word)
	}

	fix, ok := builtin.Get("steermi:docs-fix")
	require.True(t, ok)
	require.NoError(t, fix.Validate())
	assert.Equal(t, activity.Answer, fix.Runner())
	assert.Equal(t, []string{"doc_page"}, fix.StartsFrom())
	assert.Empty(t, fix.Sources())
	for _, word := range []string{"get_doc_page", "update_doc_page", "submit_activity_outputs", "resource_id"} {
		assert.Contains(t, fix.Prompt(), word)
	}
}

// Findings and summaries are read by people: both docs skills require plain
// language there and forbid naming actions, tools or fields.
func TestDocsSkillsWritePlainLanguageForPeople(t *testing.T) {
	for _, id := range []string{"steermi:docs-reconcile", "steermi:docs-fix"} {
		s, ok := builtin.Get(id)
		require.True(t, ok, id)
		prompt := strings.Join(strings.Fields(s.Prompt()), " ")
		assert.Contains(t, prompt, "Plain language for people", id)
		assert.Contains(t, prompt, "Never name actions, tools, commands or field names", id)
	}
	reconcile, _ := builtin.Get("steermi:docs-reconcile")
	prompt := strings.Join(strings.Fields(reconcile.Prompt()), " ")
	for _, phrase := range []string{"`description`", "`suggestion.rationale`", "edit the other page", "mark one page as superseded"} {
		assert.Contains(t, prompt, phrase)
	}
}
