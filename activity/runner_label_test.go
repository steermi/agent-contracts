package activity

import "testing"

func TestRunnerLabel(t *testing.T) {
	for r, want := range map[Runner]string{Answer: "Answer", Code: "Code", Findings: "Findings", "custom": "custom"} {
		if got := r.Label(); got != want {
			t.Errorf("%q.Label() = %q, want %q", r, got, want)
		}
	}
}

func TestRunSkillName(t *testing.T) {
	r := Run{Runner: Findings}
	if got := r.SkillName(); got != "Findings" {
		t.Errorf("no skill: %q", got)
	}
	r.Skill = &SkillRef{ID: "x", Name: "Security audit"}
	if got := r.SkillName(); got != "Security audit" {
		t.Errorf("pinned skill: %q", got)
	}
}
