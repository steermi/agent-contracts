// Package board is the shared-surface contract of a skill that hands back a
// board (metadata steermi-result: board). A skill ships board.yaml next to
// SKILL.md: columns of cards and ordered phases, each phase enabling a closed
// set of commands (write privately, group, vote, act, …). Hosts render it
// with generic components; nothing in the definition is executable.
//
// Columns are fixed unless the skill's inputs schema binds a property to
// board#/columns (skill/inputs): only then may a start request choose a preset
// or, when the author allows it, custom columns. WithInputs resolves the
// effective definition once at start; the host pins it on the session.
package board

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/steermi/agent-contracts/skill/inputs"
	"github.com/steermi/agent-contracts/skill/internal/problem"
)

// File is the definition's path inside a skill folder.
const File = "board.yaml"

// Version is the only definition version.
const Version = 1

// Limits.
const (
	DefaultColumnsMin = 1
	DefaultColumnsMax = inputs.MaxColumns
	MaxPhases         = 8
	DefaultCardLength = 2000
	MaxCardLength     = 5000
	MaxVoteBudget     = 20
	maxKey            = 40
)

// Visibility of cards written in a phase.
const (
	Public  = "public"
	Private = "private"
)

// Command is something people may do on the board in a phase.
type Command string

const (
	Add       Command = "add"
	EditOwn   Command = "edit_own"
	EditAny   Command = "edit_any"
	DeleteOwn Command = "delete_own"
	Group     Command = "group"
	Vote      Command = "vote"
	Act       Command = "act"
	LinkItem  Command = "link_item"
)

// Hook is what Zoe may propose in a phase. Every hook is a proposal people
// keep or dismiss; Zoe never writes to the board.
type Hook string

const (
	SuggestCards   Hook = "suggest_cards"
	SuggestGroups  Hook = "suggest_groups"
	SuggestActions Hook = "suggest_actions"
	Summarize      Hook = "summarize"
)

// Apply targets a board may create through the reviewed apply step.
const (
	ApplyWorkItems = "work_items"
	ApplyDocsPage  = "docs_page"
)

var (
	// ErrInvalid wraps every definition problem (SKILL_BOARD_INVALID).
	ErrInvalid = errors.New("SKILL_BOARD_INVALID")

	commands = []Command{Add, EditOwn, EditAny, DeleteOwn, Group, Vote, Act, LinkItem}
	hooks    = []Hook{SuggestCards, SuggestGroups, SuggestActions, Summarize}
	applies  = []string{ApplyWorkItems, ApplyDocsPage}
	// Colors is the closed column palette (owned by skill/inputs, which
	// validates columns chosen at start).
	Colors = inputs.ColumnColors
)

// validKey reports whether s is a lowercase key (a-z, 0-9, _ not first).
func validKey(s string) bool {
	if s == "" || len(s) > maxKey {
		return false
	}
	for i, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '_' && i > 0) {
			return false
		}
	}
	return true
}

// Column is one board column.
type Column struct {
	Key    string `json:"key" yaml:"key"`
	Name   string `json:"name" yaml:"name"`
	Prompt string `json:"prompt,omitempty" yaml:"prompt,omitempty"`
	Color  string `json:"color,omitempty" yaml:"color,omitempty"`
}

// Card configures cards.
type Card struct {
	MaxLength int `json:"max_length" yaml:"max_length,omitempty"`
}

// Phase is one step of the board.
type Phase struct {
	Key        string    `json:"key" yaml:"key"`
	Name       string    `json:"name" yaml:"name"`
	Visibility string    `json:"visibility" yaml:"visibility,omitempty"`
	Allow      []Command `json:"allow" yaml:"allow,omitempty"`
	// Budget is the declared vote budget (a number or inputs.<property>);
	// Votes is the resolved count (set by Parse for a number, by
	// WithInputs for an input).
	Budget Budget `json:"budget,omitzero" yaml:"budget,omitempty"`
	Votes  int    `json:"votes,omitempty" yaml:"-"`
	Zoe    Hook   `json:"zoe,omitempty" yaml:"zoe,omitempty"`
}

// Budget is a vote budget declaration: a fixed count or the name of an
// integer input. It reads and writes as a YAML/JSON scalar ("3" or
// "inputs.votes").
type Budget struct {
	N     int
	Input string
}

const inputPrefix = "inputs."

// IsZero reports an undeclared budget.
func (b Budget) IsZero() bool { return b.N == 0 && b.Input == "" }

func (b Budget) String() string {
	switch {
	case b.Input != "":
		return inputPrefix + b.Input
	case b.N != 0:
		return strconv.Itoa(b.N)
	}
	return ""
}

func (b *Budget) parse(s string) error {
	s = strings.TrimSpace(s)
	if name, ok := strings.CutPrefix(s, inputPrefix); ok {
		if name == "" {
			return errors.New("empty input name")
		}
		*b = Budget{Input: name}
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("budget %q is a number or inputs.<property>", s)
	}
	*b = Budget{N: n}
	return nil
}

// UnmarshalYAML reads a scalar budget.
func (b *Budget) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return errors.New("budget is a number or inputs.<property>")
	}
	return b.parse(n.Value)
}

// MarshalYAML writes the scalar form.
func (b Budget) MarshalYAML() (any, error) { return b.String(), nil }

// MarshalJSON writes the scalar form as a string.
func (b Budget) MarshalJSON() ([]byte, error) { return json.Marshal(b.String()) }

// UnmarshalJSON reads the string form (a number is accepted too).
func (b *Budget) UnmarshalJSON(raw []byte) error {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		var n int
		if json.Unmarshal(raw, &n) != nil {
			return err
		}
		s = strconv.Itoa(n)
	}
	if s == "" {
		*b = Budget{}
		return nil
	}
	return b.parse(s)
}

// Def is a parsed board.yaml.
type Def struct {
	Version    int      `json:"version" yaml:"version"`
	ColumnsMin int      `json:"columns_min" yaml:"columns_min,omitempty"`
	ColumnsMax int      `json:"columns_max" yaml:"columns_max,omitempty"`
	Columns    []Column `json:"columns" yaml:"columns"`
	Card       Card     `json:"card" yaml:"card,omitempty"`
	Phases     []Phase  `json:"phases" yaml:"phases"`
	Apply      []string `json:"apply,omitempty" yaml:"apply,omitempty"`
}

func invalid(format string, args ...any) error {
	return problem.New(ErrInvalid, format, args...)
}

// Parse reads and validates board.yaml. Unknown keys are rejected.
func Parse(raw []byte) (Def, error) {
	var d Def
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return Def{}, invalid("%s", strings.TrimPrefix(err.Error(), "yaml: "))
	}
	if d.ColumnsMin == 0 {
		d.ColumnsMin = DefaultColumnsMin
	}
	if d.ColumnsMax == 0 {
		d.ColumnsMax = DefaultColumnsMax
	}
	if d.Card.MaxLength == 0 {
		d.Card.MaxLength = DefaultCardLength
	}
	for i := range d.Phases {
		if d.Phases[i].Visibility == "" {
			d.Phases[i].Visibility = Public
		}
		if d.Phases[i].Allow == nil {
			d.Phases[i].Allow = []Command{}
		}
		d.Phases[i].Votes = d.Phases[i].Budget.N
	}
	return d, d.Validate()
}

// Validate checks the definition. Budgets bound to inputs are checked
// against the schema by CheckInputs.
func (d Def) Validate() error {
	if d.Version != Version {
		return invalid("version must be %d", Version)
	}
	if d.ColumnsMin < 1 || d.ColumnsMax > DefaultColumnsMax || d.ColumnsMin > d.ColumnsMax {
		return invalid("columns_min/columns_max must be within 1-%d", DefaultColumnsMax)
	}
	if err := d.validColumns(d.Columns); err != nil {
		return err
	}
	if d.Card.MaxLength < 1 || d.Card.MaxLength > MaxCardLength {
		return invalid("card.max_length must be 1-%d", MaxCardLength)
	}
	if len(d.Phases) == 0 || len(d.Phases) > MaxPhases {
		return invalid("1-%d phases", MaxPhases)
	}
	seen := map[string]bool{}
	for _, p := range d.Phases {
		at := "phase " + strconv.Quote(p.Key)
		if !validKey(p.Key) || seen[p.Key] {
			return invalid("%s: keys are distinct lowercase words (a-z, 0-9, _)", at)
		}
		seen[p.Key] = true
		if !plainText(p.Name, 40) {
			return invalid("%s: name is 1-40 characters of plain text", at)
		}
		if p.Visibility != Public && p.Visibility != Private {
			return invalid("%s: visibility is public or private", at)
		}
		for i, c := range p.Allow {
			if !slices.Contains(commands, c) || slices.Contains(p.Allow[:i], c) {
				return invalid("%s: unknown or repeated command %q", at, c)
			}
		}
		if p.Visibility == Private && (!slices.Contains(p.Allow, Add) || slices.ContainsFunc(p.Allow, func(c Command) bool {
			return c != Add && c != EditOwn && c != DeleteOwn
		})) {
			return invalid("%s: a private phase only allows add, edit_own and delete_own", at)
		}
		if p.Visibility == Private && p.Zoe != "" {
			return invalid("%s: Zoe cannot see a private phase", at)
		}
		if slices.Contains(p.Allow, Vote) {
			if p.Budget.IsZero() {
				return invalid("%s: vote needs a budget", at)
			}
			if p.Budget.Input == "" && (p.Budget.N < 1 || p.Budget.N > MaxVoteBudget) {
				return invalid("%s: budget is 1-%d or inputs.<property>", at, MaxVoteBudget)
			}
		} else if !p.Budget.IsZero() {
			return invalid("%s: budget only with vote", at)
		}
		if slices.Contains(p.Allow, Vote) && len(p.Allow) > 1 {
			return invalid("%s: a vote phase allows only vote", at)
		}
		if p.Zoe != "" && !slices.Contains(hooks, p.Zoe) {
			return invalid("%s: zoe must be one of %s", at, joinHooks())
		}
	}
	for i, a := range d.Apply {
		if !slices.Contains(applies, a) || slices.Contains(d.Apply[:i], a) {
			return invalid("apply: unknown or repeated target %q", a)
		}
	}
	if d.Allows(Act) && !slices.Contains(d.Apply, ApplyWorkItems) {
		return invalid("a phase allows act: apply must list %s", ApplyWorkItems)
	}
	return nil
}

func (d Def) validColumns(cols []Column) error {
	if len(cols) < d.ColumnsMin || len(cols) > d.ColumnsMax {
		return invalid("%d-%d columns", d.ColumnsMin, d.ColumnsMax)
	}
	seen := map[string]bool{}
	for _, c := range cols {
		at := "column " + strconv.Quote(c.Key)
		if !validKey(c.Key) || seen[c.Key] {
			return invalid("%s: keys are distinct lowercase words (a-z, 0-9, _)", at)
		}
		seen[c.Key] = true
		if !plainText(c.Name, inputs.MaxColumnName) {
			return invalid("%s: name is 1-%d characters of plain text", at, inputs.MaxColumnName)
		}
		if c.Prompt != "" && !plainText(c.Prompt, inputs.MaxColumnPrompt) {
			return invalid("%s: prompt is at most %d characters of plain text", at, inputs.MaxColumnPrompt)
		}
		if c.Color != "" && !slices.Contains(Colors, c.Color) {
			return invalid("%s: color must be one of %s", at, strings.Join(Colors, ", "))
		}
	}
	return nil
}

// Allows reports whether any phase allows c.
func (d Def) Allows(c Command) bool {
	return slices.ContainsFunc(d.Phases, func(p Phase) bool { return slices.Contains(p.Allow, c) })
}

// Phase returns the phase with key.
func (d Def) Phase(key string) (Phase, int, bool) {
	i := slices.IndexFunc(d.Phases, func(p Phase) bool { return p.Key == key })
	if i < 0 {
		return Phase{}, -1, false
	}
	return d.Phases[i], i, true
}

// Stages lists the phase keys in order.
func (d Def) Stages() []string {
	out := make([]string, len(d.Phases))
	for i, p := range d.Phases {
		out[i] = p.Key
	}
	return out
}

// Allowed reports whether phase allows c.
func (p Phase) Allowed(c Command) bool { return slices.Contains(p.Allow, c) }

// CheckInputs validates the definition against the skill's inputs schema:
// budgets bound to inputs name integer properties, and a columns binding only
// offers presets whose columns fit the board's limits.
func (d Def) CheckInputs(schema inputs.Schema) error {
	for _, p := range d.Phases {
		name := p.Budget.Input
		if name == "" {
			continue
		}
		n := (*inputs.Node)(nil)
		if schema.Root != nil {
			n = schema.Root.Properties[name]
		}
		if n == nil || n.Type != "integer" || n.Minimum == nil || n.Maximum == nil || *n.Minimum < 1 || *n.Maximum > MaxVoteBudget {
			return invalid("phase %q: budget %s must name an integer input with minimum ≥ 1 and maximum ≤ %d", p.Key, p.Budget, MaxVoteBudget)
		}
		if !slices.Contains(schema.Root.Required, name) && n.Default == nil {
			return invalid("phase %q: budget input %q needs a default or must be required", p.Key, name)
		}
	}
	binding, ok := schema.Binding()
	if !ok {
		return nil
	}
	for _, key := range binding.Presets {
		cols, known := Presets[key]
		if !known {
			return invalid("preset %q is unknown", key)
		}
		if err := d.validColumns(cols); err != nil {
			return invalid("preset %q does not fit this board: %v", key, err)
		}
	}
	return nil
}

// WithInputs resolves the effective definition for a value the schema's
// Validate returned: bound budgets become numbers and, only when the schema
// binds board#/columns and the value chooses, the preset or custom columns
// replace the default columns (keys generated from names). The choice's
// shape is validated by skill/inputs; here it only has to fit this board.
func (d Def) WithInputs(schema inputs.Schema, value json.RawMessage) (Def, error) {
	unfit := func(format string, args ...any) error {
		return problem.New(inputs.ErrInvalid, format, args...)
	}
	var obj map[string]json.RawMessage
	if len(value) > 0 && json.Unmarshal(value, &obj) != nil {
		return Def{}, unfit("inputs must be an object")
	}
	out := d
	out.Columns = slices.Clone(d.Columns)
	out.Phases = slices.Clone(d.Phases)
	for i, p := range out.Phases {
		name := p.Budget.Input
		if name == "" {
			continue
		}
		var n int
		if err := json.Unmarshal(obj[name], &n); err != nil || n < 1 || n > MaxVoteBudget {
			return Def{}, unfit("%s: 1-%d votes", name, MaxVoteBudget)
		}
		out.Phases[i].Votes = n
	}
	choice, chosen, err := schema.ColumnChoice(value)
	if err != nil || !chosen {
		return out, err
	}
	binding, _ := schema.Binding()
	if choice.Preset != "" {
		cols, known := Presets[choice.Preset]
		if !known || !slices.Contains(binding.Presets, choice.Preset) {
			return Def{}, unfit("preset %q is not offered", choice.Preset)
		}
		out.Columns = slices.Clone(cols)
	} else {
		if !binding.Custom {
			return Def{}, unfit("custom columns are not allowed")
		}
		out.Columns = make([]Column, len(choice.Columns))
		used := map[string]bool{}
		for i, c := range choice.Columns {
			color := c.Color
			if color == "" {
				color = Colors[(i+1)%len(Colors)]
			}
			out.Columns[i] = Column{Key: uniqueKey(c.Name, used), Name: c.Name, Prompt: c.Prompt, Color: color}
		}
	}
	if err := out.validColumns(out.Columns); err != nil {
		return Def{}, unfit("%s", inputs.Detail(err))
	}
	return out, nil
}

// uniqueKey slugs a column name into a key unique among used.
func uniqueKey(name string, used map[string]bool) string {
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.IsSpace(r) || r == '-' || r == '_' || r == '/':
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_')
			}
		}
	}
	base := strings.Trim(b.String(), "_")
	if len(base) > maxKey-4 {
		base = strings.Trim(base[:maxKey-4], "_")
	}
	if base == "" || base[0] >= '0' && base[0] <= '9' {
		base = "col" + base
	}
	key := base
	for i := 2; used[key]; i++ {
		key = base + "_" + strconv.Itoa(i)
	}
	used[key] = true
	return key
}

// Presets are the column sets a binding may offer by key.
var Presets = map[string][]Column{
	"went_well_improve_questions": {
		{Key: "went_well", Name: "Went well", Prompt: "What went well this cycle?", Color: "green"},
		{Key: "improve", Name: "Improve", Prompt: "What should we improve?", Color: "amber"},
		{Key: "questions", Name: "Questions", Prompt: "What is still unclear?", Color: "blue"},
	},
	"start_stop_continue": {
		{Key: "start", Name: "Start", Prompt: "What should we start doing?", Color: "green"},
		{Key: "stop", Name: "Stop", Prompt: "What should we stop?", Color: "red"},
		{Key: "continue", Name: "Continue", Prompt: "What works and should stay?", Color: "blue"},
	},
	"four_ls": {
		{Key: "liked", Name: "Liked", Prompt: "What did you like?", Color: "green"},
		{Key: "learned", Name: "Learned", Prompt: "What did you learn?", Color: "blue"},
		{Key: "lacked", Name: "Lacked", Prompt: "What was missing?", Color: "amber"},
		{Key: "longed_for", Name: "Longed for", Prompt: "What do you wish we had?", Color: "violet"},
	},
	"mad_sad_glad": {
		{Key: "mad", Name: "Mad", Prompt: "What frustrated you?", Color: "red"},
		{Key: "sad", Name: "Sad", Prompt: "What disappointed you?", Color: "blue"},
		{Key: "glad", Name: "Glad", Prompt: "What made you happy?", Color: "green"},
	},
	"stop": {
		{Key: "stop", Name: "Stop", Prompt: "What should we stop doing?", Color: "red"},
	},
}

// PresetNames lists preset keys in display order.
var PresetNames = []string{"went_well_improve_questions", "start_stop_continue", "four_ls", "mad_sad_glad", "stop"}

func plainText(s string, limit int) bool {
	s = strings.TrimSpace(s)
	return s != "" && utf8.RuneCountInString(s) <= limit && !strings.ContainsAny(s, "<>")
}

func joinHooks() string {
	out := make([]string, len(hooks))
	for i, h := range hooks {
		out[i] = string(h)
	}
	return strings.Join(out, ", ")
}
