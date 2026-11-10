// Package builtin ships Steermi's built-in skills.
//
// Plan, Build and Review are the delivery baseline, one per runner (answer,
// code, findings): a run with no skill runs the built-in of its runner, so
// "no skill" and "Steermi default" are the same thing and every run has
// exactly one prompt path (skill.Skill.Prompt). The
// workspace skills (Refinement, Cycle planning, Retro, Org design, Project
// discovery, Documentation, Research) are the instructions of Steermi's
// typed workspaces.
//
// Built-ins are visible in every organization, read-only and never stored;
// deploys update them. An organization that wants its own copy duplicates one
// into an ordinary, editable skill that is never updated automatically.
package builtin

import (
	"embed"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/skill"
)

// Version identifies the built-in skill content. Bump it with every content
// change and add a Changelog line.
const Version = "0.8.0"

// Prefix marks built-in skill identities ("steermi:plan", the plugin:skill
// form of Claude Code; URL-safe, so built-ins share the skill routes).
const Prefix = "steermi:"

// Changelog lists what changed per version, newest first.
var Changelog = []Change{
	{Version: "0.8.0", Note: "Refinement takes a start form (focus, tickets, candidate pool, queue size) and queues only from the eligible pool Steermi computes; refined items are remembered."},
	{Version: "0.7.0", Note: "Initiative planning: break an initiative into per-team epics with cross-team dependencies; in chats Zoe can read initiatives (get_initiative)."},
	{Version: "0.6.0", Note: "Refinement, Cycle planning and Research use work item links: they check dependencies and duplicates, propose blocks/duplicates links and order cycles by dependency."},
	{Version: "0.5.0", Note: "Retrospective is a board skill: board.yaml (write privately, group, vote, actions, recap) and an inputs schema (cycle, format with presets or custom columns, votes per person)."},
	{Version: "0.4.1", Note: "Retro is titled Retrospective, the name Steermi shows for the workflow."},
	{Version: "0.4.0", Note: "Plan never guesses what the product is, submits its assumptions with its outputs and, when refined with feedback, updates its earlier document as a new revision."},
	{Version: "0.3.0", Note: "Skills declare what they hand back (metadata steermi-result) instead of an access profile. Refinement, Cycle planning, Retro, Org design, Project discovery, Documentation and Research are built-in skills."},
	{Version: "0.2.0", Note: "Plan, Build and Review are skills: the activity prompts moved into SKILL.md with templates."},
}

// Change is one changelog entry.
type Change struct {
	Version string `json:"version"`
	Note    string `json:"note"`
}

//go:embed plan build review refinement sprint-planning retro org-design project-discovery project-documentation work-planning initiative-planning
var files embed.FS

// slugs lists every built-in in display order; delivery skills first.
var slugs = []string{"plan", "build", "review", "refinement", "sprint-planning", "retro", "org-design", "project-discovery", "project-documentation", "work-planning", "initiative-planning"}

// skills holds every built-in, loaded once from the embedded files.
var skills = sync.OnceValue(func() map[string]skill.Skill {
	out := make(map[string]skill.Skill, len(slugs))
	for _, slug := range slugs {
		out[slug] = load(slug)
	}
	return out
})

// IDOf is the identity of the built-in with slug.
func IDOf(slug string) string { return Prefix + slug }

// IsID reports whether id names a built-in skill (known or not).
func IsID(id string) bool { return strings.HasPrefix(id, Prefix) }

// Known reports whether id names a shipped built-in skill.
func Known(id string) bool { return IsID(id) && slices.Contains(slugs, strings.TrimPrefix(id, Prefix)) }

// IDs lists every built-in identity in display order.
func IDs() []string {
	out := make([]string, len(slugs))
	for i, s := range slugs {
		out[i] = IDOf(s)
	}
	return out
}

// Get returns the built-in skill named by id.
func Get(id string) (skill.Skill, bool) {
	if !Known(id) {
		return skill.Skill{}, false
	}
	s := skills()[strings.TrimPrefix(id, Prefix)]
	return skill.Skill{Files: slices.Clone(s.Files)}, true
}

// ForResult returns the built-in that hands back r. Each result has exactly
// one: the skills' own manifests are the only mapping.
func ForResult(r skill.Result) (string, bool) {
	for _, slug := range slugs {
		if skills()[slug].Result() == r {
			return IDOf(slug), true
		}
	}
	return "", false
}

// ID is the identity of the built-in delivery skill of a runner
// ("steermi:build" for code); "" for an unknown runner.
func ID(r activity.Runner) string {
	id, _ := ForResult(skill.Result(r))
	return id
}

// RunnerOf returns the runner of a built-in delivery skill identity.
func RunnerOf(id string) (activity.Runner, bool) {
	s, ok := Get(id)
	if !ok || !s.Result().InFlows() {
		return "", false
	}
	return s.Runner(), true
}

// Required reports whether a built-in can never be turned off: the delivery
// skills every flow falls back to.
func Required(id string) bool {
	_, ok := RunnerOf(id)
	return ok
}

// For returns the built-in delivery skill of a runner.
func For(r activity.Runner) (skill.Skill, bool) { return Get(ID(r)) }

// Runners lists the runners with a built-in delivery skill, in display order.
func Runners() []activity.Runner {
	var out []activity.Runner
	for _, id := range IDs() {
		if r, ok := RunnerOf(id); ok {
			out = append(out, r)
		}
	}
	return out
}

// Ref pins the built-in delivery skill of a runner on a run.
func Ref(r activity.Runner) *activity.SkillRef { return RefOf(ID(r)) }

// RefOf pins any built-in skill.
func RefOf(id string) *activity.SkillRef {
	s, ok := Get(id)
	if !ok {
		return nil
	}
	return &activity.SkillRef{ID: id, Slug: id, VersionID: Version, Digest: s.Digest(), Name: s.Title()}
}

func load(dir string) skill.Skill {
	var out []skill.File
	_ = fs.WalkDir(files, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := files.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, skill.File{Path: strings.TrimPrefix(p, dir+"/"), Content: string(data)})
		return nil
	})
	return skill.Skill{Files: out}.Normalize()
}

// DuplicateName is the default slug of a duplicate ("plan-custom").
func DuplicateName(s skill.Skill) string {
	name := s.Name()
	if name == "" {
		name = "skill"
	}
	return name + "-custom"
}
