package skill_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/skill"
)

func boardSkill(result string, files ...skill.File) skill.Skill {
	m := skill.Manifest{Name: "stop-only", Description: "Stop only retro", Metadata: map[string]string{skill.ResultKey: result}}
	return skill.New(m, "Collect what to stop.", files...)
}

const stopBoard = "version: 1\ncolumns: [{key: stop, name: Stop}]\nphases: [{key: collect, name: Write, visibility: private, allow: [add]}, {key: recap, name: Recap, zoe: summarize}]\n"

func TestBoardSkills(t *testing.T) {
	ok := boardSkill("board", skill.File{Path: "board.yaml", Content: stopBoard})
	require.NoError(t, ok.Validate())
	def, has, err := ok.Board()
	require.NoError(t, err)
	assert.True(t, has)
	assert.Equal(t, "stop", def.Columns[0].Key)
	assert.Equal(t, []string{"team"}, ok.StartsFrom())

	assert.ErrorContains(t, boardSkill("board").Validate(), "needs board.yaml")
	assert.ErrorContains(t, boardSkill("answer", skill.File{Path: "board.yaml", Content: stopBoard}).Validate(), "only read when")
	assert.ErrorContains(t, boardSkill("board", skill.File{Path: "board.yaml", Content: "version: 1\nphases: []\n"}).Validate(), "board:")
	bound := `{"type":"object","properties":{"f":{"$ref":"board#/columns","x-steermi-custom":true}}}`
	assert.ErrorContains(t, boardSkill("answer", skill.File{Path: "inputs.schema.json", Content: bound}).Validate(), "board#/columns needs")
	assert.ErrorContains(t, boardSkill("answer", skill.File{Path: "inputs.schema.json", Content: `{"type":"object","properties":{"a":{"type":"string","pattern":"x"}}}`}).Validate(), "inputs:")
	plain := boardSkill("answer", skill.File{Path: "inputs.schema.json", Content: `{"type":"object","properties":{"tone":{"type":"string","enum":["calm"]}}}`})
	require.NoError(t, plain.Validate(), "any skill may declare inputs")
	assert.True(t, plain.HasInputs())
}
