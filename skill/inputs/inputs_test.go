package inputs_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/skill/inputs"
)

const retro = `{
  "type": "object",
  "required": ["cycle"],
  "properties": {
    "cycle": {"type": "string", "title": "Cycle", "x-steermi-ref": "cycle", "x-steermi-status": ["completed"], "default": "$latest_completed"},
    "votes": {"type": "integer", "title": "Votes per person", "minimum": 1, "maximum": 10, "default": 3},
    "tone":  {"type": "string", "enum": ["calm", "direct"]},
    "format": {"$ref": "board#/columns", "x-steermi-presets": ["start_stop_continue"], "x-steermi-custom": true}
  }
}`

func TestParseKeepsOrderAndBinding(t *testing.T) {
	s, err := inputs.Parse([]byte(retro))
	require.NoError(t, err)
	assert.Equal(t, []string{"cycle", "votes", "tone", "format"}, s.Root.Order)
	b, ok := s.Binding()
	require.True(t, ok)
	assert.Equal(t, inputs.Binding{Property: "format", Presets: []string{"start_stop_continue"}, Custom: true}, b)
}

func TestParseRejectsUnsafeOrUnknownSchemas(t *testing.T) {
	for name, doc := range map[string]string{
		"pattern":         `{"type":"object","properties":{"a":{"type":"string","pattern":"(a+)+$"}}}`,
		"remote ref":      `{"type":"object","properties":{"a":{"$ref":"https://example.com/x.json"}}}`,
		"unknown keyword": `{"type":"object","oneOf":[]}`,
		"root not object": `{"type":"string"}`,
		"bad ref kind":    `{"type":"object","properties":{"a":{"type":"string","x-steermi-ref":"secret"}}}`,
		"ref on number":   `{"type":"object","properties":{"a":{"type":"number","x-steermi-ref":"cycle"}}}`,
		"too deep":        `{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"type":"object","properties":{"c":{"type":"object","properties":{"d":{"type":"object","properties":{"e":{"type":"string"}}}}}}}}}}}`,
		"two bindings":    `{"type":"object","properties":{"a":{"$ref":"board#/columns","x-steermi-custom":true},"b":{"$ref":"board#/columns","x-steermi-custom":true}}}`,
		"empty binding":   `{"type":"object","properties":{"a":{"$ref":"board#/columns"}}}`,
		"nested binding":  `{"type":"object","properties":{"a":{"type":"object","properties":{"b":{"$ref":"board#/columns","x-steermi-custom":true}}}}}`,
		"bad default":     `{"type":"object","properties":{"a":{"type":"integer","minimum":1,"default":0}}}`,
		"bad dynamic":     `{"type":"object","properties":{"a":{"type":"string","x-steermi-ref":"project","default":"$latest_completed"}}}`,
		"required undef":  `{"type":"object","required":["x"]}`,
		"additional true": `{"type":"object","additionalProperties":true}`,
	} {
		_, err := inputs.Parse([]byte(doc))
		assert.ErrorIs(t, err, inputs.ErrInvalid, name)
	}
	many := make([]string, 41)
	for i := range many {
		many[i] = `"f` + strings.Repeat("x", i) + `":{"type":"string"}`
	}
	_, err := inputs.Parse([]byte(`{"type":"object","properties":{` + strings.Join(many, ",") + `}}`))
	assert.ErrorIs(t, err, inputs.ErrInvalid, "at most 40 fields")
}

func TestLocalDefsKeepDocumentOrder(t *testing.T) {
	s, err := inputs.Parse([]byte(`{"type":"object","$defs":{"o":{"type":"object","properties":{"zeta":{"type":"string"},"alpha":{"type":"string"},"mid":{"type":"object","properties":{"z":{"type":"string"},"a":{"type":"string"}}}}}},"properties":{"b":{"type":"string"},"a":{"$ref":"#/$defs/o"}}}`))
	require.NoError(t, err)
	assert.Equal(t, []string{"b", "a"}, s.Root.Order)
	assert.Equal(t, []string{"zeta", "alpha", "mid"}, s.Root.Properties["a"].Order)
	assert.Equal(t, []string{"z", "a"}, s.Root.Properties["a"].Properties["mid"].Order)
}

func TestColumnChoiceRejectsUnknownColor(t *testing.T) {
	s, err := inputs.Parse([]byte(retro))
	require.NoError(t, err)
	_, err = s.Validate([]byte(`{"cycle":"c","format":{"columns":[{"name":"Stop","color":"#fff"}]}}`))
	assert.ErrorIs(t, err, inputs.ErrInvalid)
	v, err := s.Validate([]byte(`{"cycle":"c","format":{"columns":[{"name":"Stop","color":"red"}]}}`))
	require.NoError(t, err)
	c, ok, err := s.ColumnChoice(v)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, inputs.ColumnChoice{Columns: []inputs.CustomColumn{{Name: "Stop", Color: "red"}}}, c)
}

func TestLocalDefs(t *testing.T) {
	s, err := inputs.Parse([]byte(`{"type":"object","$defs":{"n":{"type":"integer","minimum":0}},"properties":{"a":{"$ref":"#/$defs/n","title":"A"}}}`))
	require.NoError(t, err)
	assert.Equal(t, "A", s.Root.Properties["a"].Title)
	_, err = s.Validate([]byte(`{"a":-1}`))
	assert.ErrorIs(t, err, inputs.ErrInvalid)
}

func TestValidateFillsDefaultsAndChecksValues(t *testing.T) {
	s, err := inputs.Parse([]byte(retro))
	require.NoError(t, err)
	out, err := s.Validate([]byte(`{"cycle":"c-1"}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"cycle":"c-1","votes":3}`, string(out))

	_, err = s.Validate([]byte(`{}`))
	assert.ErrorIs(t, err, inputs.ErrInvalid, "an unresolved dynamic default is missing")
	for name, value := range map[string]string{
		"unknown field":   `{"cycle":"c","x":1}`,
		"range":           `{"cycle":"c","votes":11}`,
		"integer":         `{"cycle":"c","votes":2.5}`,
		"enum":            `{"cycle":"c","tone":"loud"}`,
		"id with space":   `{"cycle":"a b"}`,
		"preset offered":  `{"cycle":"c","format":{"preset":"four_ls"}}`,
		"column markup":   `{"cycle":"c","format":{"columns":[{"name":"<b>x</b>"}]}}`,
		"column no name":  `{"cycle":"c","format":{"columns":[{"prompt":"x"}]}}`,
		"too many cols":   `{"cycle":"c","format":{"columns":[{"name":"1"},{"name":"2"},{"name":"3"},{"name":"4"},{"name":"5"},{"name":"6"},{"name":"7"},{"name":"8"},{"name":"9"}]}}`,
		"preset and cols": `{"cycle":"c","format":{"preset":"start_stop_continue","columns":[]}}`,
	} {
		_, err := s.Validate([]byte(value))
		assert.ErrorIs(t, err, inputs.ErrInvalid, name)
	}
	_, err = s.Validate([]byte(`{"cycle":"` + strings.Repeat("x", inputs.MaxValueBytes) + `"}`))
	assert.ErrorIs(t, err, inputs.ErrInvalid, "size limit")

	out, err = s.Validate([]byte(`{"cycle":"c","format":{"columns":[{"name":" Stop ","color":"red"}]}}`))
	require.NoError(t, err)
	assert.JSONEq(t, `{"cycle":"c","votes":3,"format":{"columns":[{"name":"Stop","color":"red"}]}}`, string(out))
}

func TestZeroSchemaAcceptsOnlyEmpty(t *testing.T) {
	out, err := inputs.Schema{}.Validate(nil)
	require.NoError(t, err)
	assert.JSONEq(t, `{}`, string(out))
	_, err = inputs.Schema{}.Validate([]byte(`{"a":1}`))
	assert.ErrorIs(t, err, inputs.ErrInvalid)
}

func TestResolveDefaultsAndRefs(t *testing.T) {
	s, err := inputs.Parse([]byte(retro))
	require.NoError(t, err)
	raw, err := s.ResolveDefaults(nil, func(kind, token string) (string, error) {
		assert.Equal(t, "cycle", kind)
		assert.Equal(t, inputs.DefaultLatestCompleted, token)
		return "c-9", nil
	})
	require.NoError(t, err)
	out, err := s.Validate(raw)
	require.NoError(t, err)
	assert.Equal(t, []inputs.Ref{{Path: "cycle", Kind: "cycle", ID: "c-9", Statuses: []string{"completed"}}}, s.Refs(out))
}

func TestHostileNestingIsBounded(t *testing.T) {
	deep := strings.Repeat(`{"type":"object","properties":{"a":`, 2000) + `{"type":"string"}` + strings.Repeat(`}}`, 2000)
	_, err := inputs.Parse([]byte(deep))
	assert.ErrorIs(t, err, inputs.ErrInvalid)
}

// x-steermi-enum-titles labels enum options for people; it must mirror the
// enum one to one and never appears without one.
func TestEnumTitles(t *testing.T) {
	s, err := inputs.Parse([]byte(`{"type":"object","properties":{"pool":{"type":"string","enum":["a","b"],"x-steermi-enum-titles":["Alpha","Beta"]}}}`))
	require.NoError(t, err)
	require.Equal(t, []string{"Alpha", "Beta"}, s.Root.Properties["pool"].EnumTitles)
	for name, raw := range map[string]string{
		"length mismatch": `{"type":"object","properties":{"pool":{"type":"string","enum":["a","b"],"x-steermi-enum-titles":["Alpha"]}}}`,
		"without enum":    `{"type":"object","properties":{"pool":{"type":"string","x-steermi-enum-titles":["Alpha"]}}}`,
		"not strings":     `{"type":"object","properties":{"pool":{"type":"string","enum":["a"],"x-steermi-enum-titles":[1]}}}`,
	} {
		_, err := inputs.Parse([]byte(raw))
		require.Error(t, err, name)
	}
}
