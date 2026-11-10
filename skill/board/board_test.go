package board_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/skill/board"
	"github.com/steermi/agent-contracts/skill/inputs"
)

const ssc = `version: 1
columns_max: 4
columns:
  - {key: start, name: Start, prompt: "What should we start doing?", color: green}
  - {key: stop, name: Stop, color: red}
phases:
  - {key: collect, name: Write, visibility: private, allow: [add, edit_own]}
  - {key: group, name: Group, allow: [group, edit_any], zoe: suggest_groups}
  - {key: vote, name: Vote, allow: [vote], budget: inputs.votes}
  - {key: actions, name: Actions, allow: [act], zoe: suggest_actions}
  - {key: recap, name: Recap, zoe: summarize}
apply: [work_items]
`

func TestParse(t *testing.T) {
	d, err := board.Parse([]byte(ssc))
	require.NoError(t, err)
	assert.Equal(t, 1, d.ColumnsMin)
	assert.Equal(t, 2000, d.Card.MaxLength)
	assert.Equal(t, []string{"collect", "group", "vote", "actions", "recap"}, d.Stages())
	assert.Equal(t, board.Public, d.Phases[1].Visibility)
	p, i, ok := d.Phase("vote")
	require.True(t, ok)
	assert.Equal(t, 2, i)
	assert.True(t, p.Allowed(board.Vote))
	fixed, err := board.Parse([]byte("version: 1\ncolumns: [{key: stop, name: Stop}]\nphases: [{key: vote, name: Vote, allow: [vote], budget: 3}]\n"))
	require.NoError(t, err)
	assert.Equal(t, 3, fixed.Phases[0].Votes)
	raw, err := json.Marshal(d.Phases[2])
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"budget":"inputs.votes"`)
	var back board.Phase
	require.NoError(t, json.Unmarshal(raw, &back))
	assert.Equal(t, board.Budget{Input: "votes"}, back.Budget)
	_, err = board.Parse([]byte("version: 1\ncolumns: [{key: stop, name: Stop}]\nphases: [{key: vote, name: Vote, allow: [vote], budget: lots}]\n"))
	assert.ErrorIs(t, err, board.ErrInvalid, "a budget that is neither a number nor an input")
}

func TestParseRejects(t *testing.T) {
	base := "version: 1\ncolumns: [{key: a, name: A}]\n"
	for name, doc := range map[string]string{
		"unknown key":       base + "phases: [{key: p, name: P}]\nscript: x\n",
		"bad command":       base + "phases: [{key: p, name: P, allow: [delete_any]}]\n",
		"bad hook":          base + "phases: [{key: p, name: P, zoe: write_cards}]\n",
		"private group":     base + "phases: [{key: p, name: P, visibility: private, allow: [add, group]}]\n",
		"private zoe":       base + "phases: [{key: p, name: P, visibility: private, allow: [add], zoe: suggest_cards}]\n",
		"vote no budget":    base + "phases: [{key: p, name: P, allow: [vote]}]\n",
		"vote budget range": base + "phases: [{key: p, name: P, allow: [vote], budget: 50}]\n",
		"act without apply": base + "phases: [{key: p, name: P, allow: [act]}]\n",
		"no phases":         base + "phases: []\n",
		"dup column":        "version: 1\ncolumns: [{key: a, name: A}, {key: a, name: B}]\nphases: [{key: p, name: P}]\n",
		"bad color":         "version: 1\ncolumns: [{key: a, name: A, color: '#fff'}]\nphases: [{key: p, name: P}]\n",
		"markup name":       "version: 1\ncolumns: [{key: a, name: <b>A</b>}]\nphases: [{key: p, name: P}]\n",
		"too many columns":  "version: 1\ncolumns_max: 1\ncolumns: [{key: a, name: A}, {key: b, name: B}]\nphases: [{key: p, name: P}]\n",
		"version":           "version: 2\ncolumns: [{key: a, name: A}]\nphases: [{key: p, name: P}]\n",
	} {
		_, err := board.Parse([]byte(doc))
		assert.ErrorIs(t, err, board.ErrInvalid, name)
	}
}

func schema(t *testing.T, doc string) inputs.Schema {
	s, err := inputs.Parse([]byte(doc))
	require.NoError(t, err)
	return s
}

const votes = `"votes":{"type":"integer","minimum":1,"maximum":10,"default":3}`

func TestCheckInputs(t *testing.T) {
	d, err := board.Parse([]byte(ssc))
	require.NoError(t, err)
	assert.ErrorIs(t, d.CheckInputs(inputs.Schema{}), board.ErrInvalid, "budget input missing")
	assert.NoError(t, d.CheckInputs(schema(t, `{"type":"object","properties":{`+votes+`}}`)))
	assert.ErrorIs(t, d.CheckInputs(schema(t, `{"type":"object","properties":{"votes":{"type":"integer","minimum":1,"maximum":99,"default":3}}}`)), board.ErrInvalid, "budget above the limit")
	assert.ErrorIs(t, d.CheckInputs(schema(t, `{"type":"object","properties":{`+votes+`,"f":{"$ref":"board#/columns","x-steermi-presets":["four_ls","nope"]}}}`)), board.ErrInvalid, "unknown preset")
	narrow, err := board.Parse([]byte("version: 1\ncolumns_max: 3\ncolumns: [{key: a, name: A}]\nphases: [{key: p, name: P}]\n"))
	require.NoError(t, err)
	assert.ErrorIs(t, narrow.CheckInputs(schema(t, `{"type":"object","properties":{"f":{"$ref":"board#/columns","x-steermi-presets":["four_ls"]}}}`)), board.ErrInvalid, "a 4-column preset violates columns_max 3")
}

func resolve(t *testing.T, d board.Def, s inputs.Schema, value string) (board.Def, error) {
	t.Helper()
	v, err := s.Validate([]byte(value))
	if err != nil {
		return board.Def{}, err
	}
	return d.WithInputs(s, v)
}

func TestWithInputs(t *testing.T) {
	d, err := board.Parse([]byte(ssc))
	require.NoError(t, err)
	unbound := schema(t, `{"type":"object","properties":{`+votes+`}}`)
	got, err := resolve(t, d, unbound, `{"votes":5}`)
	require.NoError(t, err)
	assert.Equal(t, 5, got.Phases[2].Votes)
	assert.Equal(t, d.Columns, got.Columns)

	// Unbound: a columns choice is never accepted (Validate rejects it).
	_, err = resolve(t, d, unbound, `{"votes":3,"format":{"preset":"start_stop_continue"}}`)
	assert.ErrorIs(t, err, inputs.ErrInvalid)

	presets := schema(t, `{"type":"object","properties":{`+votes+`,"format":{"$ref":"board#/columns","x-steermi-presets":["start_stop_continue","stop"]}}}`)
	got, err = resolve(t, d, presets, `{"format":{"preset":"stop"}}`)
	require.NoError(t, err)
	assert.Equal(t, board.Presets["stop"], got.Columns)
	_, err = resolve(t, d, presets, `{"format":{"columns":[{"name":"Mine"}]}}`)
	assert.ErrorIs(t, err, inputs.ErrInvalid, "custom when only presets are allowed")

	custom := schema(t, `{"type":"object","properties":{`+votes+`,"format":{"$ref":"board#/columns","x-steermi-custom":true}}}`)
	got, err = resolve(t, d, custom, `{"format":{"columns":[{"name":"Keep doing"},{"name":"Keep doing!"},{"name":"42"}]}}`)
	require.NoError(t, err)
	assert.Equal(t, []string{"keep_doing", "keep_doing_2", "col42"}, []string{got.Columns[0].Key, got.Columns[1].Key, got.Columns[2].Key})
	_, err = resolve(t, d, custom, `{"format":{"columns":[{"name":"1"},{"name":"2"},{"name":"3"},{"name":"4"},{"name":"5"}]}}`)
	assert.ErrorIs(t, err, inputs.ErrInvalid, "custom above columns_max")
	got, err = resolve(t, d, custom, `{"format":{"columns":[{"name":"1"},{"name":"2"},{"name":"3"},{"name":"4"}]}}`)
	require.NoError(t, err, "custom at columns_max")
	assert.Len(t, got.Columns, 4)
	got, err = resolve(t, d, custom, `{"format":{"columns":[{"name":"Only"}]}}`)
	require.NoError(t, err, "custom at columns_min")
	assert.Len(t, got.Columns, 1)
}
