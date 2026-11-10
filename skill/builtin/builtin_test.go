package builtin_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/activity"
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
	require.Len(t, builtin.IDs(), 11)
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
		assert.Equal(t, builtin.Required(id), !s.Result().Workspace(), "delivery built-ins are required")
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
