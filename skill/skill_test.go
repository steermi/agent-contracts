package skill_test

import (
	"archive/zip"
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/steermi/agent-contracts/activity"
	"github.com/steermi/agent-contracts/hint"
	"github.com/steermi/agent-contracts/skill"
	"github.com/steermi/agent-contracts/skill/builtin"
)

const entry = `---
name: release-notes
# kept on round trips
description: Draft release notes
license: Apache-2.0
allowed-tools: [list_pull_requests, create_doc_page]
metadata:
  steermi-result: answer
---

# Release notes

Collect merged MRs.
`

func valid() skill.Skill {
	return skill.Skill{Files: []skill.File{{Path: "SKILL.md", Content: entry}, {Path: "template.md", Content: "t"}}}
}

func setEntry(s *skill.Skill, doc string) { s.Files[0].Content = doc }

func TestValidate(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*skill.Skill)
		field string
	}{
		{"valid", func(*skill.Skill) {}, ""},
		{"no SKILL.md", func(s *skill.Skill) { s.Files = s.Files[1:] }, "files"},
		{"bad name", func(s *skill.Skill) { setEntry(s, strings.Replace(entry, "release-notes", "Release Notes", 1)) }, "name"},
		{"double hyphen", func(s *skill.Skill) { setEntry(s, strings.Replace(entry, "release-notes", "a--b", 1)) }, "name"},
		{"no description", func(s *skill.Skill) { setEntry(s, strings.Replace(entry, "Draft release notes", "", 1)) }, "description"},
		{"unknown result", func(s *skill.Skill) {
			setEntry(s, strings.Replace(entry, "steermi-result: answer", "steermi-result: deploy", 1))
		}, "result"},
		{"subject the result cannot start from", func(s *skill.Skill) {
			setEntry(s, strings.Replace(entry, "steermi-result: answer", "steermi-result: code\n  steermi-starts-from: team", 1))
		}, "starts-from"},
		{"workspace skill narrowing tools", func(s *skill.Skill) {
			setEntry(s, strings.Replace(entry, "steermi-result: answer", "steermi-result: refinement", 1))
		}, "allowed-tools"},
		{"empty instructions", func(s *skill.Skill) { setEntry(s, entry[:strings.Index(entry, "# Release")]) }, "instructions"},
		{"tool outside kind", func(s *skill.Skill) { setEntry(s, strings.Replace(entry, "create_doc_page", "batch_commit", 1)) }, "allowed-tools"},
		{"bad yaml", func(s *skill.Skill) { setEntry(s, strings.Replace(entry, "license: Apache-2.0", "license: [", 1)) }, "document"},
		{"path traversal", func(s *skill.Skill) { s.Files[1].Path = "../x.md" }, "files"},
		{"binary ext", func(s *skill.Skill) { s.Files[1].Path = "run.sh" }, "files"},
		{"lowercase entry name", func(s *skill.Skill) { s.Files[1].Path = "skill.md" }, "files"},
		{"file too big", func(s *skill.Skill) { s.Files[1].Content = strings.Repeat("x", skill.MaxFileBytes+1) }, "files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := valid()
			tt.edit(&s)
			err := s.Validate()
			if tt.field == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, skill.ErrInvalid)
			var issues skill.Issues
			require.True(t, errors.As(err, &issues))
			assert.Equal(t, tt.field, issues[0].Field)
		})
	}
}

func TestManifestKeepsUnknownKeys(t *testing.T) {
	m, body, err := valid().Manifest()
	require.NoError(t, err)
	assert.Equal(t, "release-notes", m.Name)
	assert.Equal(t, activity.Answer, m.Runner())
	assert.Equal(t, "Apache-2.0", m.Extra["license"])
	assert.True(t, strings.HasPrefix(body, "# Release notes"))
	// No metadata defaults to plan.
	m.Metadata = nil
	assert.Equal(t, activity.Answer, m.Runner())
}

func TestSetFrontMatterPreservesTheRest(t *testing.T) {
	doc, err := skill.SetFrontMatter(entry, "description", "New text")
	require.NoError(t, err)
	assert.Contains(t, doc, "# kept on round trips")
	assert.Contains(t, doc, "license: Apache-2.0")
	assert.Contains(t, doc, "Collect merged MRs.")
	m, _, err := skill.ParseEntry(doc)
	require.NoError(t, err)
	assert.Equal(t, "New text", m.Description)
	assert.Less(t, strings.Index(doc, "name:"), strings.Index(doc, "license:"), "key order kept")

	doc, err = skill.SetFrontMatter(doc, "metadata.steermi-result", "findings")
	require.NoError(t, err)
	m, _, _ = skill.ParseEntry(doc)
	assert.Equal(t, activity.Findings, m.Runner())
	doc, err = skill.SetFrontMatter(doc, "allowed-tools", nil)
	require.NoError(t, err)
	assert.NotContains(t, doc, "allowed-tools")
}

func TestCapabilitiesOnlyNarrow(t *testing.T) {
	base := activity.CapabilitiesFor(activity.Answer, "project")
	got := valid().Capabilities(base)
	assert.True(t, got.Allows("list_pull_requests"))
	assert.True(t, got.Allows("submit_activity_outputs"), "essential tools stay")
	assert.False(t, got.Allows("create_work_items"))
	// No tool list keeps the base set.
	s := valid()
	doc, _ := skill.SetFrontMatter(entry, "allowed-tools", nil)
	setEntry(&s, doc)
	assert.Equal(t, len(base), len(s.Capabilities(base)))
	// A tool the base set lacks is never added.
	assert.False(t, valid().Capabilities(activity.CapabilitySet{}).Allows("list_pull_requests"))
}

func TestDigestIgnoresFileOrder(t *testing.T) {
	a := valid()
	b := skill.Skill{Files: []skill.File{a.Files[1], a.Files[0]}}
	assert.Equal(t, a.Digest(), b.Digest())
	b.Files[0].Content = "other"
	assert.NotEqual(t, a.Digest(), b.Digest())
}

func TestPromptListsReferences(t *testing.T) {
	p := valid().Prompt()
	assert.Contains(t, p, "take precedence over the skill")
	assert.Contains(t, p, "- template.md")
	assert.NotContains(t, p, "name: release-notes", "front matter is not prompt text")
	f, ok := valid().File("template.md")
	assert.True(t, ok)
	assert.Equal(t, "t", f.Content)
}

func TestParseBundleStripsTopFolder(t *testing.T) {
	s, err := skill.ParseBundle([]skill.File{
		{Path: "release-notes/SKILL.md", Content: entry},
		{Path: "release-notes/examples/good.md", Content: "g"},
		{Path: "release-notes/.DS_Store", Content: "x"},
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"SKILL.md", "examples/good.md"}, paths(s))
	// A lone markdown file is a SKILL.md.
	s, err = skill.ParseBundle([]skill.File{{Path: "notes.md", Content: entry}})
	require.NoError(t, err)
	assert.Equal(t, []string{"SKILL.md"}, paths(s))
	_, err = skill.ParseBundle([]skill.File{{Path: "a.md", Content: "x"}, {Path: "b.md", Content: "y"}})
	require.ErrorIs(t, err, skill.ErrInvalid)
}

func paths(s skill.Skill) []string {
	out := []string{}
	for _, f := range s.Files {
		out = append(out, f.Path)
	}
	return out
}

func zipOf(t *testing.T, entries map[string]string, symlink string) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range entries {
		h := &zip.FileHeader{Name: name, Method: zip.Deflate}
		if name == symlink {
			h.SetMode(0o777 | 1<<27) // os.ModeSymlink
		}
		w, err := zw.CreateHeader(h)
		require.NoError(t, err)
		_, _ = w.Write([]byte(content))
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestZipRoundTripAndLimits(t *testing.T) {
	data, err := valid().Zip()
	require.NoError(t, err)
	s, err := skill.ParseZip(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
	assert.Equal(t, valid().Digest(), s.Digest())

	tests := map[string][]byte{
		"traversal": zipOf(t, map[string]string{"SKILL.md": entry, "../evil.md": "x"}, ""),
		"absolute":  zipOf(t, map[string]string{"SKILL.md": entry, "/etc/x.md": "x"}, ""),
		"symlink":   zipOf(t, map[string]string{"SKILL.md": entry, "link.md": "/etc/passwd"}, "link.md"),
		"binary":    zipOf(t, map[string]string{"SKILL.md": entry, "a.txt": "a\x00b"}, ""),
		"ext":       zipOf(t, map[string]string{"SKILL.md": entry, "run.sh": "x"}, ""),
		"too big":   zipOf(t, map[string]string{"SKILL.md": entry, "a.md": strings.Repeat("x", skill.MaxFileBytes+1)}, ""),
		"not zip":   []byte("plain"),
	}
	for name, data := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := skill.ParseZip(bytes.NewReader(data), int64(len(data)))
			require.ErrorIs(t, err, skill.ErrInvalid)
		})
	}
	// macOS metadata is ignored.
	data = zipOf(t, map[string]string{"x/SKILL.md": entry, "__MACOSX/x/._SKILL.md": "junk"}, "")
	_, err = skill.ParseZip(bytes.NewReader(data), int64(len(data)))
	require.NoError(t, err)
}

func TestWithName(t *testing.T) {
	s, err := valid().WithName("release-notes-custom")
	require.NoError(t, err)
	assert.Equal(t, "release-notes-custom", s.Name())
	assert.Contains(t, s.Entry(), "license: Apache-2.0")
}

func TestTitle(t *testing.T) {
	s, err := skill.ParseBundle([]skill.File{{Path: "SKILL.md", Content: "---\nname: audit\ndescription: Audits\n---\n\n# Security audit\n\nCheck things."}})
	require.NoError(t, err)
	assert.Equal(t, "Security audit", s.Title())
	s, err = skill.ParseBundle([]skill.File{{Path: "SKILL.md", Content: "---\nname: audit\ndescription: Audits\n---\n\nNo heading."}})
	require.NoError(t, err)
	assert.Equal(t, "audit", s.Title(), "falls back to the name")
}

func TestBuiltinsAreValid(t *testing.T) {
	for _, k := range builtin.Runners() {
		s, ok := builtin.For(k)
		require.True(t, ok)
		require.NoError(t, s.Validate(), k)
		assert.Equal(t, k, s.Runner())
		assert.Equal(t, map[activity.Runner]string{activity.Answer: "plan", activity.Code: "build", activity.Findings: "review"}[k], s.Name())
		assert.NotEmpty(t, s.References(), "templates ship with the built-in")
		ref := builtin.Ref(k)
		assert.Equal(t, "steermi:"+s.Name(), ref.ID)
		assert.Equal(t, map[activity.Runner]string{activity.Answer: "Plan", activity.Code: "Build", activity.Findings: "Review"}[k], ref.Name)
		assert.Equal(t, s.Digest(), ref.Digest)
		got, ok := builtin.Get(ref.ID)
		require.True(t, ok)
		assert.Equal(t, s.Digest(), got.Digest())
	}
	_, ok := builtin.Get("steermi:deploy")
	assert.False(t, ok)
	s, _ := builtin.For(activity.Answer)
	assert.Equal(t, "plan-custom", builtin.DuplicateName(s))
}

func TestSkillEffort(t *testing.T) {
	doc := "---\nname: quick\ndescription: Quick plan\nmetadata:\n  steermi-result: answer\n  steermi-effort: low\n---\n# Quick\nDo it.\n"
	s := skill.Skill{Files: []skill.File{{Path: "SKILL.md", Content: doc}}}
	require.NoError(t, s.Validate())
	assert.Equal(t, hint.Low, s.Effort())
	bad := skill.Skill{Files: []skill.File{{Path: "SKILL.md", Content: strings.Replace(doc, "low", "max", 1)}}}
	require.Error(t, bad.Validate())
}
