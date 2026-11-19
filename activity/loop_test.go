package activity

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCleanCheckDetails(t *testing.T) {
	token := "glpat-" + strings.Repeat("a1B2", 6)
	c := Check{
		DetailsURL: "https://ci.example.com/p/1",
		Summary:    "Tests failed ![x](https://evil.example/p.png?d=1) see [log](https://ci.example.com/log) token " + token + " at http://leak.example/x",
		FailedJobs: []string{"unit", "  lint  ", "unit", strings.Repeat("j", 150)},
	}
	CleanCheckDetails(&c)
	assert.Equal(t, "https://ci.example.com/p/1", c.DetailsURL)
	assert.NotContains(t, c.Summary, "evil")
	assert.NotContains(t, c.Summary, token)
	assert.NotContains(t, c.Summary, "http")
	assert.Contains(t, c.Summary, "see log")
	require.Len(t, c.FailedJobs, 3)
	assert.Contains(t, c.FailedJobs, "lint")
	assert.Contains(t, c.FailedJobs, "unit")
	for _, j := range c.FailedJobs {
		assert.LessOrEqual(t, len([]rune(j)), MaxFailedJobRunes)
	}

	for _, bad := range []string{"http://ci.example.com", "https://user:pw@ci.example.com", "javascript:alert(1)", "/relative", "https://"} {
		c := Check{DetailsURL: bad}
		CleanCheckDetails(&c)
		assert.Empty(t, c.DetailsURL, bad)
	}

	long := Check{Summary: strings.Repeat("é", 3000)}
	CleanCheckDetails(&long)
	assert.LessOrEqual(t, len(long.Summary), MaxCheckSummaryBytes)

	many := Check{}
	for i := range 15 {
		many.FailedJobs = append(many.FailedJobs, string(rune('a'+i)))
	}
	CleanCheckDetails(&many)
	assert.Len(t, many.FailedJobs, MaxFailedJobs)
}

func TestLedgerBounds(t *testing.T) {
	var ledger []LoopRound
	for i := 1; i <= 9; i++ {
		ledger = LedgerWith(ledger, i, LoopStep{RunID: "r", Tried: "x"})
	}
	require.Len(t, ledger, MaxLoopRounds)
	assert.Equal(t, 4, ledger[0].Iteration, "oldest rounds dropped first")

	for range 12 {
		ledger = LedgerWith(ledger, 9, LoopStep{RunID: "s"})
	}
	assert.Len(t, ledger[len(ledger)-1].Steps, MaxLoopSteps)

	big := strings.Repeat("w", MaxLoopTextRunes)
	reds := make([]Red, MaxLoopReds)
	for i := range reds {
		reds[i] = Red{Kind: RedFinding, Ref: "f", Expected: big, Saw: big, Location: big}
	}
	var heavy []LoopRound
	for i := 1; i <= MaxLoopRounds; i++ {
		for range 3 {
			heavy = LedgerWith(heavy, i, LoopStep{RunID: "r", Tried: big, Red: reds})
		}
	}
	b, _ := json.Marshal(heavy)
	assert.LessOrEqual(t, len(b), MaxLedgerBytes)
	newest := heavy[len(heavy)-1]
	assert.Equal(t, MaxLoopRounds, newest.Iteration)
	assert.Equal(t, big, newest.Steps[0].Red[0].Saw, "the newest round keeps its texts")
	if len(heavy) > 1 {
		assert.Empty(t, heavy[0].Steps[0].Red[0].Saw, "older rounds lose texts first")
	}
}

func TestTriedAndFindingTexts(t *testing.T) {
	owner := Actor{UserID: "o"}
	r := Run{ID: "a", Runner: Answer, State: Running, Version: 1, OwnerID: "o"}
	out := []OutputRef{{Kind: RefDocs, ResourceID: "d", Revision: "1", Hash: strings.Repeat("a", 64)}}
	require.ErrorIs(t, r.Apply(owner, Command{Type: Submit, ExpectedVersion: 1, Outputs: out, Tried: strings.Repeat("x", MaxLoopTextRunes+1)}), ErrValidation)
	require.NoError(t, r.Apply(owner, Command{Type: Submit, ExpectedVersion: 1, Outputs: out, Tried: "  split   the   handler "}))
	assert.Equal(t, "split the handler", r.Tried)

	f := Run{ID: "f", Runner: Findings, State: Running, Version: 1, OwnerID: "o"}
	bad := Finding{ID: "1", Severity: High, Description: "d", Expected: strings.Repeat("e", MaxLoopTextRunes+1)}
	require.ErrorIs(t, f.Apply(owner, Command{Type: SubmitFindings, ExpectedVersion: 1, Summary: "s", Recommendation: ChangesRequested, Findings: []Finding{bad}}), ErrValidation)
	good := Finding{ID: "1", Severity: High, Description: "d", Expected: "302 → /login", Observed: "302 → /dashboard"}
	require.NoError(t, f.Apply(owner, Command{Type: SubmitFindings, ExpectedVersion: 1, Summary: "s", Recommendation: ChangesRequested, Findings: []Finding{good}, Tried: "ran the suite"}))
	assert.Equal(t, "302 → /dashboard", f.Findings[0].Observed)
	assert.Equal(t, "ran the suite", f.Tried)
}

func TestRecordCheckCleansDetails(t *testing.T) {
	sha := strings.Repeat("a", 40)
	r := Run{ID: "b", Runner: Code, State: Submitted, Version: 1, Outputs: []OutputRef{{Kind: RefRepository, ResourceID: "r", RepositoryID: "r", CommitSHA: sha}}}
	git := Actor{UserID: "g", Purpose: PurposeVerifiedGit}
	require.NoError(t, r.Apply(git, Command{Type: RecordCheck, ExpectedVersion: 1, Check: &Check{RepositoryID: "r", CommitSHA: sha, Name: "ci", ProviderRunID: "1", Attempt: 1,
		Status: CheckFailed, DetailsURL: "http://insecure.example", Summary: "![i](https://x.example/i.png)boom", FailedJobs: []string{"b", "a"}}}))
	assert.Empty(t, r.Checks[0].DetailsURL)
	assert.Equal(t, "boom", r.Checks[0].Summary)
	assert.Equal(t, []string{"a", "b"}, r.Checks[0].FailedJobs)
}

func TestLoopFieldsRoundTrip(t *testing.T) {
	in := Run{ID: "r", Tried: "t", Flow: &FlowRef{Node: "build", Loop: &LoopState{Key: "q", LoopPolicy: LoopPolicy{MaxIterations: 3, StallMatch: StallLocation, OnFailedCheck: FailedCheckAuto},
		Ledger: []LoopRound{{Iteration: 1, Steps: []LoopStep{{RunID: "a", Runner: Code, Passed: []string{"lint"}, Red: []Red{{Kind: RedCheck, Ref: "ci", URL: "https://x"}}}}}}}},
		Checks: []Check{{Name: "ci", DetailsURL: "https://x", Summary: "s", FailedJobs: []string{"j"}}}}
	b, err := json.Marshal(in)
	require.NoError(t, err)
	var out Run
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in.Flow.Loop, out.Flow.Loop)
	assert.Equal(t, in.Checks, out.Checks)
	assert.Equal(t, "t", out.Tried)

	old := Run{ID: "r", Flow: &FlowRef{Node: "build", Loop: &LoopState{Key: "q", LoopPolicy: LoopPolicy{MaxIterations: 3}}}}
	b, _ = json.Marshal(old)
	assert.NotContains(t, string(b), "ledger")
	assert.NotContains(t, string(b), "stall_match")
	assert.NotContains(t, string(b), "on_failed_check")
	assert.NotContains(t, string(b), "tried")
}
