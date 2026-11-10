// Package skill defines Zoe skills: versioned instructions for one kind of
// task. A skill declares what it hands back (its Result); the engine derives
// the runner and capability allowlist from that, so a skill never widens what
// a run may do. Its allowed tools are a subset of that allowlist, and the
// rules of the scope a skill runs in always take precedence over its
// instructions.
//
// A skill is a folder of text files with SKILL.md as its entry point, exactly
// like an Agent Skills skill on a developer's machine:
//
//	release-notes/
//	  SKILL.md          front matter + instructions
//	  template.md       any other text file, read on demand
//	  examples/good.md
//
// SKILL.md front matter is Agent Skills compatible. Steermi reads name,
// description, allowed-tools and the steermi-* metadata keys; every other key
// (license, compatibility, …) is preserved verbatim.
package skill

import (
	"bytes"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/steermi/agent-contracts/hint"

	"gopkg.in/yaml.v3"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/skill/board"
	"github.com/steermi/agent-contracts/skill/inputs"
)

// EntryPoint is the file every skill starts from.
const EntryPoint = "SKILL.md"

// Limits keep skills small enough to load into every run's prompt.
const (
	MaxNameLength        = 64
	MaxDescriptionLength = 1024
	// MaxEntryBytes bounds SKILL.md.
	MaxEntryBytes = 32 << 10
	// MaxFiles bounds the reference files besides SKILL.md.
	MaxFiles          = 10
	MaxFileBytes      = 64 << 10
	MaxTotalFileBytes = 256 << 10
	MaxTools          = 64
	maxFilePathLength = 100
)

var (
	// ErrInvalid wraps every validation failure.
	ErrInvalid = errors.New("SKILL_INVALID")

	nameRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	filePathRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_./-]*$`)
	fileExts   = []string{".md", ".txt", ".json", ".yaml", ".yml", ".csv"}
	// essential tools stay available to every skill of the runner, whatever
	// its tool list says: without them the run cannot finish or ask.
	essential = []string{"ask_questions", "manage_todo_list", "read_skill_file", "report_instruction_conflict", "submit_activity_outputs", "submit_review_findings"}
)

// Manifest is the SKILL.md front matter.
type Manifest struct {
	// Name is the slug, unique per scope (Agent Skills `name`).
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
	// AllowedTools, when set, narrows the activity's tools to this list
	// (Agent Skills `allowed-tools`).
	AllowedTools []string          `json:"allowed_tools,omitempty" yaml:"allowed-tools,omitempty"`
	Metadata     map[string]string `json:"metadata,omitempty" yaml:"metadata,omitempty"`
	// Extra holds every other front matter key, kept verbatim.
	Extra map[string]any `json:"-" yaml:",inline"`
}

// File is one file of a skill folder.
type File struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// Skill is one immutable skill version: its files, SKILL.md first.
type Skill struct {
	Files []File `json:"files"`
}

// Issue is one validation problem, addressed to a field for editors.
type Issue struct {
	Field   string `json:"field"`
	Message string `json:"message"`
	// Line is the SKILL.md line of a front matter error (1-based, 0 = none).
	Line int `json:"line,omitempty"`
}

// Issues is a validation error listing every problem found.
type Issues []Issue

func (e Issues) Error() string {
	parts := make([]string, len(e))
	for i, issue := range e {
		parts[i] = issue.Field + ": " + issue.Message
	}
	return ErrInvalid.Error() + ": " + strings.Join(parts, "; ")
}

func (e Issues) Unwrap() error { return ErrInvalid }

// RunnerTools is every tool a skill of the runner may list: the union of the
// runner's capabilities over the scopes it applies to.
func RunnerTools(runner activity.Runner) []string {
	set := map[string]bool{}
	for _, scope := range []string{"organization", "workspace", "team", "project"} {
		for tool, ok := range activity.CapabilitiesFor(runner, scope) {
			if ok {
				set[tool] = true
			}
		}
	}
	out := make([]string, 0, len(set))
	for tool := range set {
		out = append(out, tool)
	}
	slices.Sort(out)
	return out
}

// New builds a skill from a manifest, instructions and reference files.
func New(m Manifest, instructions string, files ...File) Skill {
	return Skill{Files: append([]File{{EntryPoint, Render(m, instructions)}}, files...)}.Normalize()
}

// Render writes SKILL.md for a manifest and instructions.
func Render(m Manifest, instructions string) string {
	front, _ := yaml.Marshal(m)
	return "---\n" + string(front) + "---\n\n" + strings.TrimSpace(instructions) + "\n"
}

// Normalize cleans paths and line endings and orders files: SKILL.md first,
// then by path.
func (s Skill) Normalize() Skill {
	files := make([]File, 0, len(s.Files))
	for _, f := range s.Files {
		f.Path = strings.TrimPrefix(strings.TrimSpace(f.Path), "./")
		f.Content = normalizeText(f.Content)
		files = append(files, f)
	}
	slices.SortStableFunc(files, func(a, b File) int {
		switch {
		case a.Path == EntryPoint && b.Path != EntryPoint:
			return -1
		case b.Path == EntryPoint && a.Path != EntryPoint:
			return 1
		}
		return strings.Compare(a.Path, b.Path)
	})
	return Skill{Files: files}
}

func normalizeText(s string) string {
	s = strings.TrimPrefix(s, "\uFEFF")
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// Entry returns SKILL.md ("" when missing).
func (s Skill) Entry() string {
	if f, ok := s.lookup(EntryPoint); ok {
		return f.Content
	}
	return ""
}

// References lists the files besides SKILL.md.
func (s Skill) References() []File {
	return slices.DeleteFunc(slices.Clone(s.Files), func(f File) bool { return f.Path == EntryPoint })
}

// Manifest parses SKILL.md into its front matter and instructions.
func (s Skill) Manifest() (Manifest, string, error) {
	return ParseEntry(s.Entry())
}

// splitFrontMatter returns the front matter and the text after its closing
// fence.
func splitFrontMatter(doc string) (front, rest string, err error) {
	if !strings.HasPrefix(doc, "---\n") {
		return "", "", Issues{{Field: "document", Message: "SKILL.md must start with --- front matter", Line: 1}}
	}
	body := doc[4:]
	if strings.HasPrefix(body, "---") {
		return "", body[3:], nil
	}
	end := strings.Index(body, "\n---")
	if end < 0 {
		return "", "", Issues{{Field: "document", Message: "front matter is not closed with ---", Line: 1}}
	}
	return body[:end+1], body[end+4:], nil
}

// ParseEntry parses a SKILL.md document. Unknown keys go to Manifest.Extra.
func ParseEntry(doc string) (Manifest, string, error) {
	front, rest, err := splitFrontMatter(normalizeText(doc))
	if err != nil {
		return Manifest{}, "", err
	}
	var m Manifest
	if err := yaml.Unmarshal([]byte(front), &m); err != nil {
		return Manifest{}, "", Issues{{Field: "document", Message: "front matter: " + yamlMessage(err), Line: yamlLine(err) + 1}}
	}
	if i := strings.IndexByte(rest, '\n'); i >= 0 {
		rest = rest[i+1:]
	} else {
		rest = ""
	}
	m.Name = strings.TrimSpace(m.Name)
	m.Description = strings.TrimSpace(m.Description)
	return m, strings.TrimSpace(rest), nil
}

var yamlLineRe = regexp.MustCompile(`line (\d+)`)

func yamlLine(err error) int {
	if m := yamlLineRe.FindStringSubmatch(err.Error()); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n
	}
	return 0
}

func yamlMessage(err error) string { return strings.TrimPrefix(err.Error(), "yaml: ") }

// Name is the manifest name ("" when SKILL.md does not parse).
func (s Skill) Name() string {
	m, _, _ := s.Manifest()
	return m.Name
}

// Title is the skill's display name: the first "# " heading of its
// instructions, else its manifest name. People see titles, never runners.
func (s Skill) Title() string {
	for _, line := range strings.Split(s.Instructions(), "\n") {
		if t, ok := strings.CutPrefix(strings.TrimSpace(line), "# "); ok && strings.TrimSpace(t) != "" {
			return strings.TrimSpace(t)
		}
	}
	return s.Name()
}

// Description is the manifest description.
func (s Skill) Description() string {
	m, _, _ := s.Manifest()
	return m.Description
}

// Runner is the engine runner derived from the skill's result.
func (s Skill) Runner() activity.Runner {
	m, _, _ := s.Manifest()
	return m.Runner()
}

// Runner is the engine runner derived from the manifest's result ("" for an
// unknown result).
func (m Manifest) Runner() activity.Runner { return m.Result().Runner() }

// Effort is the skill's declared `metadata.steermi-effort` ("" = unset or
// invalid), the base of the model suggestion (package hint).
func (s Skill) Effort() hint.Effort {
	m, _, _ := s.Manifest()
	return hint.ParseEffort(m.Metadata[EffortKey])
}

// AllowedTools is the manifest tool list.
func (s Skill) AllowedTools() []string {
	m, _, _ := s.Manifest()
	return m.AllowedTools
}

// Instructions is the SKILL.md body.
func (s Skill) Instructions() string {
	_, body, _ := s.Manifest()
	return body
}

// Validate reports every structural problem. These checks are the
// deterministic skill/rule conflict guard: a skill can never ask for tools or
// results its runner does not allow.
func (s Skill) Validate() error {
	var out Issues
	add := func(field, format string, args ...any) {
		out = append(out, Issue{Field: field, Message: fmt.Sprintf(format, args...)})
	}
	entry, ok := s.lookup(EntryPoint)
	if !ok {
		return Issues{{Field: "files", Message: "a skill needs a SKILL.md"}}
	}
	if len(entry.Content) > MaxEntryBytes || !utf8.ValidString(entry.Content) {
		add("files", "SKILL.md must be UTF-8 text of at most %d bytes", MaxEntryBytes)
	}
	m, body, err := ParseEntry(entry.Content)
	if err != nil {
		var issues Issues
		if errors.As(err, &issues) {
			return append(out, issues...)
		}
		return err
	}
	if !ValidName(m.Name) {
		add("name", "use 1-%d lowercase letters, digits and single hyphens", MaxNameLength)
	}
	if m.Description == "" || utf8.RuneCountInString(m.Description) > MaxDescriptionLength {
		add("description", "required, at most %d characters", MaxDescriptionLength)
	}
	result := m.Result()
	runner := result.Runner()
	if !result.Valid() {
		add("result", "metadata.%s must be one of %s", ResultKey, joinResults())
	}
	if raw := strings.TrimSpace(m.Metadata[StartsFromKey]); raw != "" && result.Valid() {
		for _, subject := range splitList(raw) {
			if !slices.Contains(result.Subjects(), subject) {
				add("starts-from", "%q cannot start a skill that hands back %s", subject, result)
			}
		}
	}
	if raw := strings.TrimSpace(m.Metadata[EffortKey]); raw != "" && hint.ParseEffort(raw) == "" {
		add("effort", "metadata.%s must be low, medium or high", EffortKey)
	}
	if result.Workspace() && len(m.AllowedTools) > 0 {
		add("allowed-tools", "workspace skills use the workspace's own tools")
	}
	if body == "" {
		add("instructions", "SKILL.md needs instructions after the front matter")
	}
	if len(m.AllowedTools) > MaxTools {
		add("allowed-tools", "at most %d tools", MaxTools)
	}
	if runnerTools := RunnerTools(runner); len(runnerTools) > 0 && !result.Workspace() {
		for _, tool := range m.AllowedTools {
			if !slices.Contains(runnerTools, tool) {
				add("allowed-tools", "%q is not available to %s skills", tool, runner)
			}
		}
	}
	refs := s.References()
	if len(refs) > MaxFiles {
		add("files", "at most %d files besides SKILL.md", MaxFiles)
	}
	total, seen := len(entry.Content), map[string]bool{EntryPoint: true}
	for _, f := range s.Files {
		if f.Path == EntryPoint {
			if f.Content != entry.Content {
				add("files", "duplicate path %q", f.Path)
			}
			continue
		}
		if msg := ValidPath(f.Path); msg != "" {
			add("files", "%s", msg)
		} else if seen[f.Path] {
			add("files", "duplicate path %q", f.Path)
		} else if len(f.Content) > MaxFileBytes || !utf8.ValidString(f.Content) {
			add("files", "%q must be UTF-8 text of at most %d bytes", f.Path, MaxFileBytes)
		}
		seen[f.Path] = true
		total += len(f.Content)
	}
	if total > MaxTotalFileBytes {
		add("files", "at most %d bytes in total", MaxTotalFileBytes)
	}
	schema, schemaErr := s.Inputs()
	if schemaErr != nil {
		add("inputs", "%s: %s", inputs.File, inputs.Detail(schemaErr))
	}
	_, bound := schema.Binding()
	def, hasBoard, boardErr := s.Board()
	switch {
	case boardErr != nil:
		add("board", "%s: %s", board.File, inputs.Detail(boardErr))
	case result == ResultBoard && !hasBoard:
		add("board", "a skill that hands back a board needs %s", board.File)
	case result != ResultBoard && hasBoard:
		add("board", "%s is only read when metadata.%s is board", board.File, ResultKey)
	case hasBoard && schemaErr == nil:
		if err := def.CheckInputs(schema); err != nil {
			add("board", "%s", inputs.Detail(err))
		}
	}
	if bound && result != ResultBoard {
		add("inputs", "%s needs metadata.%s: board", inputs.BoardColumnsRef, ResultKey)
	}
	if len(out) > 0 {
		return out
	}
	return nil
}

func joinResults() string {
	parts := make([]string, 0, len(Results()))
	for _, r := range Results() {
		parts = append(parts, string(r))
	}
	return strings.Join(parts, ", ")
}

// ValidName reports whether name is a valid skill slug.
func ValidName(name string) bool {
	return nameRe.MatchString(name) && !strings.HasSuffix(name, "-") && !strings.Contains(name, "--")
}

// ValidPath explains why p is not a valid reference file path ("" = valid).
func ValidPath(p string) string {
	clean := path.Clean(p)
	switch {
	case len(p) > maxFilePathLength || !filePathRe.MatchString(p) || clean != p || strings.HasPrefix(clean, "..") || strings.Contains(p, "/../") || strings.Contains(p, "//"):
		return fmt.Sprintf("invalid path %q", p)
	case strings.EqualFold(p, EntryPoint):
		return "SKILL.md is spelled in capitals"
	case !slices.Contains(fileExts, strings.ToLower(path.Ext(p))):
		return fmt.Sprintf("%q: only text files (%s)", p, strings.Join(fileExts, ", "))
	}
	return ""
}

// Inputs parses the skill's start-form schema (inputs.schema.json). A skill
// without one has the zero Schema, which accepts only an empty object.
func (s Skill) Inputs() (inputs.Schema, error) {
	f, ok := s.lookup(inputs.File)
	if !ok {
		return inputs.Schema{}, nil
	}
	return inputs.Parse([]byte(f.Content))
}

// HasInputs reports whether the skill ships a start-form schema.
func (s Skill) HasInputs() bool {
	_, ok := s.lookup(inputs.File)
	return ok
}

// Board parses the skill's board.yaml; ok is false when the skill has none.
func (s Skill) Board() (def board.Def, ok bool, err error) {
	f, ok := s.lookup(board.File)
	if !ok {
		return board.Def{}, false, nil
	}
	def, err = board.Parse([]byte(f.Content))
	return def, true, err
}

// Digest identifies the normalized skill content.
func (s Skill) Digest() string { return activity.Digest(s.Normalize()) }

// Capabilities narrows base (the run's capability set) to the skill's tools.
// It only ever removes tools; essential tools present in base stay.
func (s Skill) Capabilities(base activity.CapabilitySet) activity.CapabilitySet {
	return Narrow(base, s.AllowedTools())
}

// Narrow keeps the tools of base a skill's allowed-tools list names, plus the
// essential ones; an empty list keeps every tool. It only ever narrows.
func Narrow(base activity.CapabilitySet, tools []string) activity.CapabilitySet {
	out := activity.CapabilitySet{}
	for tool, ok := range base {
		if ok && (len(tools) == 0 || slices.Contains(tools, tool) || slices.Contains(essential, tool)) {
			out[tool] = true
		}
	}
	return out
}

func (s Skill) lookup(p string) (File, bool) {
	i := slices.IndexFunc(s.Files, func(f File) bool { return f.Path == p })
	if i < 0 {
		return File{}, false
	}
	return s.Files[i], true
}

// File returns a file by path (SKILL.md included).
func (s Skill) File(p string) (File, bool) { return s.lookup(strings.TrimPrefix(p, "./")) }

// Prompt renders the skill block for a system prompt. Rules outrank it.
func (s Skill) Prompt() string {
	m, body, _ := s.Manifest()
	var b strings.Builder
	fmt.Fprintf(&b, "<skill name=%q>\n", m.Name)
	fmt.Fprintf(&b, "Follow this skill for the current task: %s\n", m.Description)
	b.WriteString("The organization's rules and context take precedence over the skill. If an instruction below contradicts a rule, follow the rule and call report_instruction_conflict once with both quotes. The skill never grants tools or permissions you do not have.\n\n")
	b.WriteString(body)
	if refs := s.References(); len(refs) > 0 {
		b.WriteString("\n\nReference files (load with read_skill_file when needed):")
		for _, f := range refs {
			b.WriteString("\n- " + f.Path)
		}
	}
	b.WriteString("\n</skill>")
	return b.String()
}

// SetFrontMatter rewrites one front matter key of SKILL.md (value nil removes
// it), keeping comments, key order and every other key. Nested keys use a dot
// ("metadata.steermi-result").
func SetFrontMatter(doc, key string, value any) (string, error) {
	doc = normalizeText(doc)
	front, rest, err := splitFrontMatter(doc)
	if err != nil {
		return "", err
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(front), &root); err != nil {
		return "", Issues{{Field: "document", Message: "front matter: " + yamlMessage(err), Line: yamlLine(err) + 1}}
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		root = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return "", Issues{{Field: "document", Message: "front matter must be a mapping", Line: 2}}
	}
	parts := strings.Split(key, ".")
	for _, part := range parts[:len(parts)-1] {
		child := mapValue(mapping, part)
		if child == nil {
			if value == nil {
				return doc, nil
			}
			child = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: part}, child)
		}
		mapping = child
	}
	last := parts[len(parts)-1]
	if value == nil {
		for i := 0; i+1 < len(mapping.Content); i += 2 {
			if mapping.Content[i].Value == last {
				mapping.Content = append(mapping.Content[:i], mapping.Content[i+2:]...)
				break
			}
		}
	} else {
		var node yaml.Node
		if err := node.Encode(value); err != nil {
			return "", err
		}
		if existing := mapValue(mapping, last); existing != nil {
			node.HeadComment, node.LineComment, node.FootComment = existing.HeadComment, existing.LineComment, existing.FootComment
			*existing = node
		} else {
			mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: last}, &node)
		}
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(&root); err != nil {
		return "", err
	}
	_ = enc.Close()
	return "---\n" + buf.String() + "---" + rest, nil
}

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// WithName returns a copy whose SKILL.md names the skill name.
func (s Skill) WithName(name string) (Skill, error) {
	out := s.Normalize()
	i := slices.IndexFunc(out.Files, func(f File) bool { return f.Path == EntryPoint })
	if i < 0 {
		return s, Issues{{Field: "files", Message: "a skill needs a SKILL.md"}}
	}
	doc, err := SetFrontMatter(out.Files[i].Content, "name", name)
	if err != nil {
		return s, err
	}
	out.Files = slices.Clone(out.Files)
	out.Files[i].Content = doc
	return out, nil
}
