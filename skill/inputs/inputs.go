// Package inputs is the start-form contract of a skill: a small, closed
// subset of JSON Schema 2020-12 a skill ships as inputs.schema.json. Hosts
// render it as a form, validate the submitted value with Validate and pin the
// result on the run, so the skill receives typed data instead of asking in
// chat.
//
// The subset is data only. There are no remote references, no regular
// expressions (pattern) and no conditional keywords; unknown keywords are
// rejected, depth and field counts are bounded. Values that name Steermi
// entities carry x-steermi-ref and must be re-authorized by the host as the
// caller before a run uses them (Refs lists them).
package inputs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/steermi/agent-contracts/skill/internal/problem"
)

// File is the schema's path inside a skill folder.
const File = "inputs.schema.json"

// Limits keep forms small and validation cheap.
const (
	MaxDepth      = 4
	MaxFields     = 40
	MaxValueBytes = 16 << 10
	MaxEnum       = 50
	MaxTitle      = 120
	MaxDesc       = 1000
	MaxString     = 10000
	MaxArrayItems = 100
)

// BoardColumnsRef binds a property to the board's columns (skill/board): only
// such a property lets a start request choose the board's format.
const BoardColumnsRef = "board#/columns"

// Ref kinds a value may name (x-steermi-ref).
const (
	RefCycle      = "cycle"
	RefProject    = "project"
	RefWorkItem   = "work_item"
	RefRepository = "repository"
	RefMember     = "member"
	RefDocsPage   = "docs_page"
)

// Dynamic defaults a host resolves for ref fields before validation.
const (
	DefaultLatestCompleted = "$latest_completed"
	DefaultActive          = "$active"
	DefaultScope           = "$scope"
)

var (
	// ErrInvalid wraps every schema or value problem (SKILL_INPUT_INVALID).
	ErrInvalid = errors.New("SKILL_INPUT_INVALID")

	refKinds        = []string{RefCycle, RefProject, RefWorkItem, RefRepository, RefMember, RefDocsPage}
	dynamicDefaults = []string{DefaultLatestCompleted, DefaultActive, DefaultScope}
	types           = []string{"object", "string", "number", "integer", "boolean", "array"}
	widgets         = []string{"text", "textarea", "radio", "select", "checkbox", "number", "columns"}
	cycleStatuses   = []string{"planning", "active", "completed"}
	keywords        = []string{"$schema", "$defs", "$ref", "type", "title", "description", "properties", "required", "enum", "default", "minimum", "maximum", "minLength", "maxLength", "maxItems", "items", "additionalProperties", "x-steermi-ref", "x-steermi-widget", "x-steermi-status", "x-steermi-presets", "x-steermi-custom", "x-steermi-enum-titles"}
)

// Node is one parsed schema node with local references resolved.
type Node struct {
	Type        string           `json:"type,omitempty"`
	Title       string           `json:"title,omitempty"`
	Description string           `json:"description,omitempty"`
	Properties  map[string]*Node `json:"properties,omitempty"`
	// Order lists the property names in document order (forms render it).
	Order    []string `json:"order,omitempty"`
	Required []string `json:"required,omitempty"`
	Enum     []any    `json:"enum,omitempty"`
	// EnumTitles labels the enum options for people, one per enum value
	// (x-steermi-enum-titles); the stored value stays the enum value.
	EnumTitles []string `json:"x-steermi-enum-titles,omitempty"`
	Default    any      `json:"default,omitempty"`
	Minimum    *float64 `json:"minimum,omitempty"`
	Maximum    *float64 `json:"maximum,omitempty"`
	MinLength  *int     `json:"minLength,omitempty"`
	MaxLength  *int     `json:"maxLength,omitempty"`
	MaxItems   *int     `json:"maxItems,omitempty"`
	Items      *Node    `json:"items,omitempty"`
	// Ref names a Steermi entity kind (x-steermi-ref); the value is its id.
	Ref string `json:"x-steermi-ref,omitempty"`
	// Statuses narrows cycle refs (x-steermi-status).
	Statuses []string `json:"x-steermi-status,omitempty"`
	Widget   string   `json:"x-steermi-widget,omitempty"`
	// Board marks a property bound to board#/columns.
	Board bool `json:"board,omitempty"`
	// Presets / Custom narrow a board binding (x-steermi-presets / -custom).
	Presets []string `json:"x-steermi-presets,omitempty"`
	Custom  bool     `json:"x-steermi-custom,omitempty"`
}

// Schema is a parsed inputs schema. The zero value accepts only an empty
// object (a skill without inputs).
type Schema struct {
	Root *Node `json:"root,omitempty"`
}

// Binding is the property bound to the board's columns.
type Binding struct {
	Property string   `json:"property"`
	Presets  []string `json:"presets,omitempty"`
	Custom   bool     `json:"custom,omitempty"`
}

// Ref is one entity id named by a validated value.
type Ref struct {
	Path     string   `json:"path"`
	Kind     string   `json:"kind"`
	ID       string   `json:"id"`
	Statuses []string `json:"statuses,omitempty"`
}

func invalid(format string, args ...any) error {
	return problem.New(ErrInvalid, format, args...)
}

// Detail is the human part of an inputs error (without the code), or the
// whole message for any other error.
func Detail(err error) string {
	var p *problem.Error
	if errors.As(err, &p) {
		return p.Detail
	}
	return err.Error()
}

func decodeStrict(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(out); err != nil {
		return invalid("not JSON: %v", err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return invalid("trailing data")
	}
	return nil
}

// Parse reads inputs.schema.json. The root must be an object schema.
func Parse(raw []byte) (Schema, error) {
	var doc map[string]any
	if err := decodeStrict(raw, &doc); err != nil {
		return Schema{}, err
	}
	if doc == nil {
		return Schema{}, invalid("the schema must be an object")
	}
	defs := map[string]any{}
	if d, ok := doc["$defs"]; ok {
		m, ok := d.(map[string]any)
		if !ok {
			return Schema{}, invalid("$defs must be an object")
		}
		defs = m
	}
	p := parser{defs: defs, order: propertyOrder(raw), aliases: map[string]string{}}
	root, err := p.node(doc, "", 0)
	if err != nil {
		return Schema{}, err
	}
	if root.Type != "object" {
		return Schema{}, invalid("the root must be type object")
	}
	if p.fields > MaxFields {
		return Schema{}, invalid("at most %d fields", MaxFields)
	}
	if p.bindings > 1 {
		return Schema{}, invalid("at most one property may bind %s", BoardColumnsRef)
	}
	return Schema{Root: root}, nil
}

type parser struct {
	defs  map[string]any
	order map[string][]string
	// aliases maps a use-site path to the $defs path its properties were
	// written at (propertyOrder records them there).
	aliases  map[string]string
	fields   int
	bindings int
}

// orderAt is the document order of the properties at path, following
// $defs aliases of path or of any parent.
func (p *parser) orderAt(path string) []string {
	if o, ok := p.order[path]; ok {
		return o
	}
	for from, to := range p.aliases {
		if path == from {
			return p.order[to]
		}
		for _, sep := range []string{".", "[]"} {
			if rest, ok := strings.CutPrefix(path, from+sep); ok {
				return p.orderAt(to + sep + rest)
			}
		}
	}
	return nil
}

func (p *parser) node(raw map[string]any, path string, depth int) (*Node, error) {
	if depth > MaxDepth {
		return nil, invalid("%s: nested deeper than %d", label(path), MaxDepth)
	}
	for key := range raw {
		if !slices.Contains(keywords, key) {
			return nil, invalid("%s: keyword %q is not supported", label(path), key)
		}
	}
	if depth > 0 {
		if _, ok := raw["$defs"]; ok {
			return nil, invalid("%s: $defs only at the root", label(path))
		}
	}
	if ref, ok := raw["$ref"]; ok {
		s, _ := ref.(string)
		if s == BoardColumnsRef {
			return p.boardBinding(raw, path)
		}
		name, found := strings.CutPrefix(s, "#/$defs/")
		target, exists := p.defs[name]
		m, isMap := target.(map[string]any)
		if !found || !exists || !isMap || strings.Contains(name, "/") {
			return nil, invalid("%s: only local #/$defs/<name> references (or %s)", label(path), BoardColumnsRef)
		}
		merged := map[string]any{}
		for k, v := range m {
			merged[k] = v
		}
		for k, v := range raw {
			if k != "$ref" {
				merged[k] = v
			}
		}
		if _, nested := merged["$ref"]; nested && depth >= MaxDepth {
			return nil, invalid("%s: references nested too deep", label(path))
		}
		if _, own := raw["properties"]; !own {
			// The properties come from the definition: read their document
			// order where the definition is written.
			p.aliases[path] = "#$defs#" + name
		}
		return p.node(merged, path, depth+1)
	}
	n := &Node{}
	var err error
	if n.Type, err = str(raw, "type"); err != nil || !slices.Contains(types, n.Type) {
		return nil, invalid("%s: type must be one of %s", label(path), strings.Join(types, ", "))
	}
	if n.Title, err = str(raw, "title"); err != nil || utf8.RuneCountInString(n.Title) > MaxTitle {
		return nil, invalid("%s: title is text of at most %d characters", label(path), MaxTitle)
	}
	if n.Description, err = str(raw, "description"); err != nil || utf8.RuneCountInString(n.Description) > MaxDesc {
		return nil, invalid("%s: description is text of at most %d characters", label(path), MaxDesc)
	}
	if n.Widget, err = str(raw, "x-steermi-widget"); err != nil || n.Widget != "" && !slices.Contains(widgets, n.Widget) {
		return nil, invalid("%s: x-steermi-widget must be one of %s", label(path), strings.Join(widgets, ", "))
	}
	if n.Ref, err = str(raw, "x-steermi-ref"); err != nil || n.Ref != "" && (!slices.Contains(refKinds, n.Ref) || n.Type != "string") {
		return nil, invalid("%s: x-steermi-ref must be one of %s on a string", label(path), strings.Join(refKinds, ", "))
	}
	if v, ok := raw["x-steermi-status"]; ok {
		n.Statuses, err = strList(v)
		if err != nil || n.Ref != RefCycle || len(n.Statuses) == 0 {
			return nil, invalid("%s: x-steermi-status lists cycle statuses on a cycle ref", label(path))
		}
		for _, s := range n.Statuses {
			if !slices.Contains(cycleStatuses, s) {
				return nil, invalid("%s: unknown cycle status %q", label(path), s)
			}
		}
	}
	for _, key := range []string{"x-steermi-presets", "x-steermi-custom"} {
		if _, ok := raw[key]; ok {
			return nil, invalid("%s: %s only on a %s property", label(path), key, BoardColumnsRef)
		}
	}
	if n.Minimum, err = num(raw, "minimum"); err != nil {
		return nil, invalid("%s: minimum must be a number", label(path))
	}
	if n.Maximum, err = num(raw, "maximum"); err != nil {
		return nil, invalid("%s: maximum must be a number", label(path))
	}
	if n.MinLength, err = count(raw, "minLength"); err != nil {
		return nil, invalid("%s: minLength must be a non-negative integer", label(path))
	}
	if n.MaxLength, err = count(raw, "maxLength"); err != nil {
		return nil, invalid("%s: maxLength must be a non-negative integer", label(path))
	}
	if n.MaxItems, err = count(raw, "maxItems"); err != nil {
		return nil, invalid("%s: maxItems must be a non-negative integer", label(path))
	}
	if (n.Minimum != nil || n.Maximum != nil) && n.Type != "number" && n.Type != "integer" {
		return nil, invalid("%s: minimum/maximum only on numbers", label(path))
	}
	if n.Minimum != nil && n.Maximum != nil && *n.Minimum > *n.Maximum {
		return nil, invalid("%s: minimum is above maximum", label(path))
	}
	if (n.MinLength != nil || n.MaxLength != nil) && n.Type != "string" {
		return nil, invalid("%s: minLength/maxLength only on strings", label(path))
	}
	if n.MaxItems != nil && (n.Type != "array" || *n.MaxItems > MaxArrayItems) {
		return nil, invalid("%s: maxItems only on arrays, at most %d", label(path), MaxArrayItems)
	}
	if ap, ok := raw["additionalProperties"]; ok {
		if b, isBool := ap.(bool); !isBool || b || n.Type != "object" {
			return nil, invalid("%s: additionalProperties may only be false on objects", label(path))
		}
	}
	switch n.Type {
	case "object":
		if err := p.object(n, raw, path, depth); err != nil {
			return nil, err
		}
	case "array":
		items, ok := raw["items"].(map[string]any)
		if !ok {
			return nil, invalid("%s: arrays need an items schema", label(path))
		}
		if n.Items, err = p.node(items, path+"[]", depth+1); err != nil {
			return nil, err
		}
		if n.Items.Board {
			return nil, invalid("%s: %s cannot be an array item", label(path), BoardColumnsRef)
		}
	default:
		if _, ok := raw["items"]; ok {
			return nil, invalid("%s: items only on arrays", label(path))
		}
		if _, ok := raw["properties"]; ok {
			return nil, invalid("%s: properties only on objects", label(path))
		}
	}
	if e, ok := raw["enum"]; ok {
		list, isList := e.([]any)
		if !isList || len(list) == 0 || len(list) > MaxEnum || n.Type == "object" || n.Type == "array" {
			return nil, invalid("%s: enum is a list of 1-%d scalar values", label(path), MaxEnum)
		}
		for _, v := range list {
			norm, err := n.scalar(v, path)
			if err != nil {
				return nil, invalid("%s: enum value %v does not match the type", label(path), v)
			}
			n.Enum = append(n.Enum, norm)
		}
	}
	if t, ok := raw["x-steermi-enum-titles"]; ok {
		list, isList := t.([]any)
		if !isList || len(n.Enum) == 0 || len(list) != len(n.Enum) {
			return nil, invalid("%s: x-steermi-enum-titles lists one title per enum value", label(path))
		}
		for _, v := range list {
			title, isStr := v.(string)
			if !isStr || strings.TrimSpace(title) == "" || utf8.RuneCountInString(title) > MaxTitle {
				return nil, invalid("%s: x-steermi-enum-titles must be non-empty strings", label(path))
			}
			n.EnumTitles = append(n.EnumTitles, title)
		}
	}
	if d, ok := raw["default"]; ok {
		if s, isStr := d.(string); isStr && n.Ref != "" && strings.HasPrefix(s, "$") {
			if !slices.Contains(dynamicDefaults, s) || s == DefaultLatestCompleted && n.Ref != RefCycle || s == DefaultActive && n.Ref != RefCycle {
				return nil, invalid("%s: unknown dynamic default %q", label(path), s)
			}
			n.Default = s
		} else {
			norm, err := n.value(d, path, true)
			if err != nil {
				return nil, invalid("%s: default does not match the schema", label(path))
			}
			n.Default = norm
		}
	}
	return n, nil
}

func (p *parser) object(n *Node, raw map[string]any, path string, depth int) error {
	props, _ := raw["properties"].(map[string]any)
	if _, ok := raw["properties"]; ok && props == nil {
		return invalid("%s: properties must be an object", label(path))
	}
	n.Properties = map[string]*Node{}
	order := p.orderAt(path)
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	// Document order; names it does not list (none in practice) go last,
	// alphabetically, so the order is total.
	rank := func(name string) int {
		if i := slices.Index(order, name); i >= 0 {
			return i
		}
		return len(order)
	}
	sort.Slice(names, func(i, j int) bool {
		a, b := rank(names[i]), rank(names[j])
		if a != b {
			return a < b
		}
		return names[i] < names[j]
	})
	for _, name := range names {
		if !validName(name) {
			return invalid("%s: property name %q must be 1-64 letters, digits or _", label(path), name)
		}
		child, ok := props[name].(map[string]any)
		if !ok {
			return invalid("%s: property %q must be a schema", label(path), name)
		}
		p.fields++
		if p.fields > MaxFields {
			return invalid("at most %d fields", MaxFields)
		}
		node, err := p.node(child, join(path, name), depth+1)
		if err != nil {
			return err
		}
		if node.Board && path != "" {
			return invalid("%s: %s only on a top-level property", label(join(path, name)), BoardColumnsRef)
		}
		n.Properties[name] = node
		n.Order = append(n.Order, name)
	}
	if r, ok := raw["required"]; ok {
		list, err := strList(r)
		if err != nil {
			return invalid("%s: required lists property names", label(path))
		}
		for _, name := range list {
			if n.Properties[name] == nil {
				return invalid("%s: required property %q is not defined", label(path), name)
			}
		}
		n.Required = list
	}
	return nil
}

// boardBinding parses a property bound to board#/columns. Its value is
// {"preset": key} or {"columns": [{name, prompt?, color?}]}.
func (p *parser) boardBinding(raw map[string]any, path string) (*Node, error) {
	p.bindings++
	n := &Node{Type: "object", Board: true, Widget: "columns"}
	for key, v := range raw {
		var err error
		switch key {
		case "$ref":
		case "title":
			n.Title, err = str(raw, key)
		case "description":
			n.Description, err = str(raw, key)
		case "x-steermi-presets":
			n.Presets, err = strList(v)
			if err == nil && len(n.Presets) == 0 {
				err = errors.New("empty")
			}
		case "x-steermi-custom":
			b, ok := v.(bool)
			if !ok {
				err = errors.New("bool")
			}
			n.Custom = b
		case "default":
			s, ok := v.(map[string]any)
			preset, _ := s["preset"].(string)
			if !ok || len(s) != 1 || preset == "" {
				err = errors.New("default")
			}
			n.Default = map[string]any{"preset": preset}
		default:
			err = fmt.Errorf("keyword %q", key)
		}
		if err != nil {
			return nil, invalid("%s: a %s property takes title, description, default {preset}, x-steermi-presets and x-steermi-custom (%v)", label(path), BoardColumnsRef, err)
		}
	}
	if !n.Custom && len(n.Presets) == 0 {
		return nil, invalid("%s: a %s property needs x-steermi-presets or x-steermi-custom: true", label(path), BoardColumnsRef)
	}
	if d, ok := n.Default.(map[string]any); ok && len(n.Presets) > 0 && !slices.Contains(n.Presets, d["preset"].(string)) {
		return nil, invalid("%s: the default preset is not offered", label(path))
	}
	return n, nil
}

// Binding returns the property bound to the board's columns, if any.
func (s Schema) Binding() (Binding, bool) {
	if s.Root == nil {
		return Binding{}, false
	}
	for _, name := range s.Root.Order {
		if n := s.Root.Properties[name]; n.Board {
			return Binding{Property: name, Presets: slices.Clone(n.Presets), Custom: n.Custom}, true
		}
	}
	return Binding{}, false
}

// Validate checks value against the schema, fills static defaults and
// returns the normalized object. An empty value is an empty object. Dynamic
// defaults ($latest_completed, …) must be resolved by the host first
// (ResolveDefaults); an unresolved one fails as missing.
func (s Schema) Validate(value []byte) (json.RawMessage, error) {
	if len(bytes.TrimSpace(value)) == 0 {
		value = []byte("{}")
	}
	if len(value) > MaxValueBytes {
		return nil, invalid("inputs are larger than %d bytes", MaxValueBytes)
	}
	var v any
	if err := decodeStrict(value, &v); err != nil {
		return nil, err
	}
	root := s.Root
	if root == nil {
		root = &Node{Type: "object", Properties: map[string]*Node{}}
	}
	out, err := root.value(v, "", true)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, invalid("%v", err)
	}
	if len(raw) > MaxValueBytes {
		return nil, invalid("inputs are larger than %d bytes", MaxValueBytes)
	}
	return raw, nil
}

// ResolveDefaults fills every missing top-level ref property that has a
// dynamic default with resolve(kind, token). resolve returns "" when nothing
// matches (the field then stays missing).
func (s Schema) ResolveDefaults(value []byte, resolve func(kind, token string) (string, error)) (json.RawMessage, error) {
	if len(bytes.TrimSpace(value)) == 0 {
		value = []byte("{}")
	}
	var obj map[string]any
	if err := decodeStrict(value, &obj); err != nil || obj == nil {
		return nil, invalid("inputs must be an object")
	}
	if s.Root != nil {
		for _, name := range s.Root.Order {
			n := s.Root.Properties[name]
			token, ok := n.Default.(string)
			if _, present := obj[name]; present || n.Ref == "" || !ok || !strings.HasPrefix(token, "$") {
				continue
			}
			id, err := resolve(n.Ref, token)
			if err != nil {
				return nil, err
			}
			if id != "" {
				obj[name] = id
			}
		}
	}
	return json.Marshal(obj)
}

// Refs lists the entity ids a validated value names, in document order.
func (s Schema) Refs(value []byte) []Ref {
	var v any
	if s.Root == nil || decodeStrict(value, &v) != nil {
		return nil
	}
	var out []Ref
	var walk func(n *Node, v any, path string)
	walk = func(n *Node, v any, path string) {
		switch {
		case n == nil || v == nil:
		case n.Ref != "":
			if id, ok := v.(string); ok && id != "" {
				out = append(out, Ref{Path: path, Kind: n.Ref, ID: id, Statuses: slices.Clone(n.Statuses)})
			}
		case n.Type == "object" && !n.Board:
			m, _ := v.(map[string]any)
			for _, name := range n.Order {
				walk(n.Properties[name], m[name], join(path, name))
			}
		case n.Type == "array":
			list, _ := v.([]any)
			for i, item := range list {
				walk(n.Items, item, fmt.Sprintf("%s[%d]", path, i))
			}
		}
	}
	walk(s.Root, v, "")
	return out
}

// value validates v (decoded with UseNumber) and returns it normalized.
func (n *Node) value(v any, path string, fill bool) (any, error) {
	if n.Board {
		return boardValue(n, v, path)
	}
	switch n.Type {
	case "object":
		m, ok := v.(map[string]any)
		if !ok {
			return nil, invalid("%s: expected an object", label(path))
		}
		for key := range m {
			if n.Properties[key] == nil {
				return nil, invalid("%s: unknown field %q", label(path), key)
			}
		}
		out := map[string]any{}
		for _, name := range n.Order {
			child := n.Properties[name]
			raw, present := m[name]
			if !present && fill && child.Default != nil && !isDynamic(child.Default) {
				// Defaults were normalized by Parse.
				out[name] = child.Default
				continue
			}
			if !present {
				if slices.Contains(n.Required, name) {
					return nil, invalid("%s: required", label(join(path, name)))
				}
				continue
			}
			norm, err := child.value(raw, join(path, name), fill)
			if err != nil {
				return nil, err
			}
			out[name] = norm
		}
		return out, nil
	case "array":
		list, ok := v.([]any)
		if !ok {
			return nil, invalid("%s: expected a list", label(path))
		}
		limit := MaxArrayItems
		if n.MaxItems != nil {
			limit = *n.MaxItems
		}
		if len(list) > limit {
			return nil, invalid("%s: at most %d items", label(path), limit)
		}
		out := make([]any, len(list))
		for i, item := range list {
			norm, err := n.Items.value(item, fmt.Sprintf("%s[%d]", path, i), fill)
			if err != nil {
				return nil, err
			}
			out[i] = norm
		}
		return out, nil
	}
	norm, err := n.scalar(v, path)
	if err != nil {
		return nil, err
	}
	if len(n.Enum) > 0 && !slices.Contains(n.Enum, norm) {
		return nil, invalid("%s: not one of the allowed values", label(path))
	}
	return norm, nil
}

func (n *Node) scalar(v any, path string) (any, error) {
	switch n.Type {
	case "string":
		s, ok := v.(string)
		if !ok || !utf8.ValidString(s) {
			return nil, invalid("%s: expected text", label(path))
		}
		length := utf8.RuneCountInString(s)
		if length > MaxString || n.MaxLength != nil && length > *n.MaxLength || n.MinLength != nil && length < *n.MinLength {
			return nil, invalid("%s: text length out of range", label(path))
		}
		if n.Ref != "" && (s == "" || len(s) > 100 || strings.ContainsAny(s, " \t\n<>\"'")) {
			return nil, invalid("%s: expected an id", label(path))
		}
		return s, nil
	case "boolean":
		b, ok := v.(bool)
		if !ok {
			return nil, invalid("%s: expected true or false", label(path))
		}
		return b, nil
	case "number", "integer":
		// Every caller decodes with UseNumber (decodeStrict).
		x, ok := v.(json.Number)
		if !ok {
			return nil, invalid("%s: expected a number", label(path))
		}
		f, err := x.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, invalid("%s: expected a number", label(path))
		}
		if n.Type == "integer" && f != math.Trunc(f) {
			return nil, invalid("%s: expected an integer", label(path))
		}
		if n.Minimum != nil && f < *n.Minimum || n.Maximum != nil && f > *n.Maximum {
			return nil, invalid("%s: out of range", label(path))
		}
		if n.Type == "integer" {
			return int64(f), nil
		}
		return f, nil
	}
	return nil, invalid("%s: unsupported type", label(path))
}

// The value of a board#/columns property is owned here (skill/board only
// fits a validated choice to its definition): {"preset": key} or
// {"columns": [{name, prompt?, color?}]}.
const (
	MaxColumnName   = 60
	MaxColumnPrompt = 200
	MaxColumns      = 8
)

// ColumnColors is the closed column palette (hosts map it to tokens).
var ColumnColors = []string{"gray", "red", "orange", "amber", "green", "teal", "blue", "violet", "pink"}

// CustomColumn is one column a start request defines.
type CustomColumn struct {
	Name   string `json:"name"`
	Prompt string `json:"prompt,omitempty"`
	Color  string `json:"color,omitempty"`
}

// ColumnChoice is a validated board#/columns value: exactly one of Preset
// or Columns is set.
type ColumnChoice struct {
	Preset  string         `json:"preset,omitempty"`
	Columns []CustomColumn `json:"columns,omitempty"`
}

// ColumnChoice returns the bound property's choice in a value Validate
// returned; ok is false when the schema binds nothing or the value does not
// choose.
func (s Schema) ColumnChoice(value json.RawMessage) (ColumnChoice, bool, error) {
	binding, bound := s.Binding()
	if !bound || len(value) == 0 {
		return ColumnChoice{}, false, nil
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(value, &obj) != nil {
		return ColumnChoice{}, false, invalid("inputs must be an object")
	}
	raw, chosen := obj[binding.Property]
	if !chosen {
		return ColumnChoice{}, false, nil
	}
	var c ColumnChoice
	if json.Unmarshal(raw, &c) != nil || (c.Preset == "") == (len(c.Columns) == 0) {
		return ColumnChoice{}, false, invalid("%s: choose a preset or custom columns", binding.Property)
	}
	return c, true, nil
}

func boardValue(n *Node, v any, path string) (any, error) {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 {
		return nil, invalid("%s: choose a preset or custom columns", label(path))
	}
	if preset, ok := m["preset"]; ok {
		s, isStr := preset.(string)
		if !isStr || s == "" || len(n.Presets) == 0 || !slices.Contains(n.Presets, s) {
			return nil, invalid("%s: preset is not offered", label(path))
		}
		return map[string]any{"preset": s}, nil
	}
	list, ok := m["columns"].([]any)
	if !ok || !n.Custom {
		return nil, invalid("%s: custom columns are not allowed", label(path))
	}
	if len(list) == 0 || len(list) > MaxColumns {
		return nil, invalid("%s: 1-%d columns", label(path), MaxColumns)
	}
	out := make([]any, len(list))
	for i, item := range list {
		col, ok := item.(map[string]any)
		if !ok {
			return nil, invalid("%s[%d]: expected a column", label(path), i)
		}
		clean := map[string]any{}
		for key, raw := range col {
			s, isStr := raw.(string)
			s = strings.TrimSpace(s)
			switch {
			case !isStr || strings.ContainsAny(s, "<>"):
				return nil, invalid("%s[%d].%s: expected plain text", label(path), i, key)
			case key == "name" && (s == "" || utf8.RuneCountInString(s) > MaxColumnName):
				return nil, invalid("%s[%d]: name is 1-%d characters", label(path), i, MaxColumnName)
			case key == "prompt" && utf8.RuneCountInString(s) > MaxColumnPrompt:
				return nil, invalid("%s[%d]: prompt is at most %d characters", label(path), i, MaxColumnPrompt)
			case key == "color" && !slices.Contains(ColumnColors, s):
				return nil, invalid("%s[%d]: unknown color", label(path), i)
			case key != "name" && key != "prompt" && key != "color":
				return nil, invalid("%s[%d]: unknown field %q", label(path), i, key)
			}
			if s != "" {
				clean[key] = s
			}
		}
		if clean["name"] == nil {
			return nil, invalid("%s[%d]: name is required", label(path), i)
		}
		out[i] = clean
	}
	return map[string]any{"columns": out}, nil
}

func isDynamic(v any) bool {
	s, ok := v.(string)
	return ok && slices.Contains(dynamicDefaults, s)
}

func str(raw map[string]any, key string) (string, error) {
	v, ok := raw[key]
	if !ok {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", errors.New("not a string")
	}
	return strings.TrimSpace(s), nil
}

func strList(v any) ([]string, error) {
	list, ok := v.([]any)
	if !ok {
		return nil, errors.New("not a list")
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, ok := item.(string)
		if !ok || s == "" || slices.Contains(out, s) {
			return nil, errors.New("not a list of distinct strings")
		}
		out = append(out, s)
	}
	return out, nil
}

func num(raw map[string]any, key string) (*float64, error) {
	v, ok := raw[key]
	if !ok {
		return nil, nil
	}
	n, ok := v.(json.Number)
	if !ok {
		return nil, errors.New("not a number")
	}
	f, err := n.Float64()
	if err != nil || math.IsInf(f, 0) {
		return nil, errors.New("not a number")
	}
	return &f, nil
}

func count(raw map[string]any, key string) (*int, error) {
	f, err := num(raw, key)
	if err != nil || f == nil {
		return nil, err
	}
	if *f < 0 || *f != math.Trunc(*f) || *f > MaxString {
		return nil, errors.New("not a count")
	}
	i := int(*f)
	return &i, nil
}

func validName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

func label(path string) string {
	if path == "" {
		return "inputs"
	}
	return path
}

// propertyOrder reads the document order of every properties object, keyed
// by path (Go maps lose it; forms keep the author's order).
func propertyOrder(raw []byte) map[string][]string {
	out := map[string][]string{}
	d := json.NewDecoder(bytes.NewReader(raw))
	var walk func(path string, inProps bool) error
	depth := 0
	walk = func(path string, inProps bool) error {
		// Parse rejects schemas deeper than MaxDepth anyway; stop reading
		// order far below that so a hostile document cannot recurse deeply.
		if depth++; depth > 4*MaxDepth+8 {
			return errors.New("too deep")
		}
		defer func() { depth-- }()
		tok, err := d.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			for d.More() {
				keyTok, err := d.Token()
				if err != nil {
					return err
				}
				key, _ := keyTok.(string)
				switch {
				case inProps:
					out[path] = append(out[path], key)
					if err := walk(join(path, key), false); err != nil {
						return err
					}
				case key == "properties":
					if err := walk(path, true); err != nil {
						return err
					}
				case key == "items":
					if err := walk(path+"[]", false); err != nil {
						return err
					}
				default:
					if err := walk(path+"#"+key, false); err != nil {
						return err
					}
				}
			}
			_, err = d.Token()
			return err
		case json.Delim('['):
			for d.More() {
				if err := walk(path+"#", false); err != nil {
					return err
				}
			}
			_, err = d.Token()
			return err
		}
		return nil
	}
	_ = walk("", false)
	return out
}
