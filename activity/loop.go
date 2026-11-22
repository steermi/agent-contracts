package activity

import (
	"cmp"
	"encoding/json"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/steermi/agent-contracts/redact"
)

// Loop ledger and check detail limits. The ledger is copied to every
// successor, the prompt and the local claim bundle, so it stays small.
const (
	// MaxLoopTextRunes bounds Run.Tried, Finding.Expected/Observed and the
	// texts of a ledger Red.
	MaxLoopTextRunes = 300
	// MaxLoopRounds is how many rounds a ledger keeps (oldest dropped first;
	// "One more round" can go past the loop's iterations).
	MaxLoopRounds = 6
	// MaxLoopSteps bounds the steps of one round (4 steps + check repeats).
	MaxLoopSteps = 8
	// MaxLoopReds and MaxLoopPassed bound one step's reds and passed checks.
	MaxLoopReds   = 10
	MaxLoopPassed = 10
	// MaxLedgerBytes caps the encoded ledger.
	MaxLedgerBytes = 32 * 1024
	// MaxCheckSummaryBytes, MaxFailedJobs and MaxFailedJobRunes bound check
	// details.
	MaxCheckSummaryBytes = 2 * 1024
	MaxFailedJobs        = 10
	MaxFailedJobRunes    = 100
	maxLocationRunes     = 200
	maxURLBytes          = 2048
)

// Red kinds: why a step was not green.
const (
	RedFinding = "finding"
	RedCheck   = "check"
)

// LoopRound is one round of a quality loop as later rounds see it: one entry
// per step that ran in it (Build, then Review), in order.
type LoopRound struct {
	Iteration int        `json:"iteration"`
	Steps     []LoopStep `json:"steps"`
}

// LoopStep is one finished run of a round (LoopStepOf).
type LoopStep struct {
	RunID  string `json:"run_id"`
	Node   string `json:"node"`
	Name   string `json:"name,omitempty"`
	Runner Runner `json:"runner"`
	// Revision is the digest of a code step's repository outputs; Commit the
	// first output commit, for display.
	Revision string `json:"revision,omitempty"`
	Commit   string `json:"commit,omitempty"`
	// Fingerprint identifies the step's findings (FingerprintBy) or failed
	// checks (CheckFingerprint).
	Fingerprint string `json:"fingerprint,omitempty"`
	Tried       string `json:"tried,omitempty"`
	// CostTotal is the chain's cost when the step ended; hosts derive each
	// round's spend from it.
	CostTotal float64  `json:"cost_total,omitempty"`
	Passed    []string `json:"passed,omitempty"`
	Red       []Red    `json:"red,omitempty"`
}

// Red is one reason a step was not green.
type Red struct {
	Kind     string `json:"kind"`
	Ref      string `json:"ref"`
	Severity string `json:"severity,omitempty"`
	Expected string `json:"expected,omitempty"`
	Saw      string `json:"saw,omitempty"`
	Location string `json:"location,omitempty"`
	URL      string `json:"url,omitempty"`
}

// LoopStepOf is the ledger entry of a finished run: its approach, revision,
// passed required checks and its reds (open blocking findings,
// LoopPolicy.Blocking; failed required checks). It is the only way a step is built.
func LoopStepOf(r Run) LoopStep {
	s := LoopStep{RunID: r.ID, Name: r.SkillName(), Runner: r.Runner, Tried: clip(r.Tried, MaxLoopTextRunes), CostTotal: r.Cost}
	if r.Flow != nil {
		s.Node = r.Flow.Node
	}
	if r.Runner == Code {
		s.Revision = RevisionDigest(r.Outputs)
		for _, o := range r.Outputs {
			if o.Kind == RefRepository {
				s.Commit = o.CommitSHA
				break
			}
		}
	}
	results := r.RequiredCheckResults()
	var failed []Check
	for _, c := range results {
		switch c.Status {
		case CheckSuccess:
			if !slices.Contains(s.Passed, c.Name) && len(s.Passed) < MaxLoopPassed {
				s.Passed = append(s.Passed, c.Name)
			}
		case CheckFailed:
			failed = append(failed, c)
			saw := strings.Join(c.FailedJobs, ", ")
			if saw == "" {
				saw, _, _ = strings.Cut(strings.TrimSpace(c.Summary), "\n")
			}
			s.Red = append(s.Red, Red{Kind: RedCheck, Ref: c.Name, Saw: clip(saw, MaxLoopTextRunes), URL: c.DetailsURL})
		}
	}
	if len(failed) > 0 {
		s.Fingerprint = CheckFingerprint(failed)
	}
	if r.Runner == Findings {
		policy := r.loopPolicy()
		for _, f := range r.Findings {
			if !f.open() || !policy.blocks(f) {
				continue
			}
			s.Red = append(s.Red, Red{Kind: RedFinding, Ref: f.ID, Severity: string(f.Severity),
				Expected: clip(f.Expected, MaxLoopTextRunes), Saw: clip(cmp.Or(f.Observed, f.Description), MaxLoopTextRunes),
				Location: clip(f.Location, maxLocationRunes)})
		}
		s.Fingerprint = policy.StallFingerprint(r.Findings)
	}
	if len(s.Red) > MaxLoopReds {
		s.Red = s.Red[:MaxLoopReds]
	}
	return s
}

// LedgerWith adds a step to the round of iteration (opening it when it is
// new) and applies the ledger's bounds. ledger is not modified.
func LedgerWith(ledger []LoopRound, iteration int, step LoopStep) []LoopRound {
	out := cloneLedger(ledger)
	if n := len(out); n == 0 || out[n-1].Iteration != iteration {
		out = append(out, LoopRound{Iteration: iteration})
	}
	last := &out[len(out)-1]
	last.Steps = append(last.Steps, step)
	if len(last.Steps) > MaxLoopSteps {
		last.Steps = last.Steps[len(last.Steps)-MaxLoopSteps:]
	}
	return boundLedger(out)
}

// Ledger is the run's loop ledger with its own step: what a person looking at
// the run sees (its pinned ledger has no entry for itself yet). Nil off a
// loop; a run without an outcome adds no step.
func (r Run) Ledger() []LoopRound {
	if r.Flow == nil || r.Flow.Loop == nil {
		return nil
	}
	l := r.Flow.Loop
	if r.Outcome() == "" {
		return cloneLedger(l.Ledger)
	}
	return LedgerWith(l.Ledger, l.Iteration, LoopStepOf(r))
}

// RoundCosts is what each round of a ledger spent: the difference between
// the chain cost at its last step and at the previous round's last step
// (start for the first). Hosts send it; clients never compute it.
func RoundCosts(ledger []LoopRound, start float64) []float64 {
	out := make([]float64, len(ledger))
	prev := start
	for i, round := range ledger {
		end := prev
		for _, s := range round.Steps {
			if s.CostTotal > end {
				end = s.CostTotal
			}
		}
		out[i] = max(end-prev, 0)
		prev = end
	}
	return out
}

func cloneLedger(in []LoopRound) []LoopRound {
	if len(in) == 0 {
		return nil
	}
	out := make([]LoopRound, len(in))
	for i, r := range in {
		out[i] = LoopRound{Iteration: r.Iteration, Steps: make([]LoopStep, len(r.Steps))}
		for j, s := range r.Steps {
			s.Passed = slices.Clone(s.Passed)
			s.Red = slices.Clone(s.Red)
			out[i].Steps[j] = s
		}
	}
	return out
}

// boundLedger keeps the last MaxLoopRounds rounds and, while the encoding is
// over MaxLedgerBytes, clears texts of the older rounds, oldest first
// (expected and saw, then tried and locations), then drops older rounds, and
// only then clears the newest round's texts.
func boundLedger(l []LoopRound) []LoopRound {
	if len(l) > MaxLoopRounds {
		l = l[len(l)-MaxLoopRounds:]
	}
	fits := func() bool { b, _ := json.Marshal(l); return len(b) <= MaxLedgerBytes }
	if fits() {
		return l
	}
	passes := []func(*LoopStep){
		func(s *LoopStep) {
			for i := range s.Red {
				s.Red[i].Expected, s.Red[i].Saw = "", ""
			}
		},
		func(s *LoopStep) {
			s.Tried = ""
			for i := range s.Red {
				s.Red[i].Location, s.Red[i].URL = "", ""
			}
		},
	}
	clearRound := func(i int, f func(*LoopStep)) {
		for j := range l[i].Steps {
			f(&l[i].Steps[j])
		}
	}
	for _, f := range passes {
		for i := range len(l) - 1 {
			clearRound(i, f)
			if fits() {
				return l
			}
		}
	}
	for len(l) > 1 && !fits() {
		l = l[1:]
	}
	for _, f := range passes {
		if fits() {
			return l
		}
		clearRound(len(l)-1, f)
	}
	return l
}

// FingerprintBy identifies findings for stall detection. exact is
// Fingerprint; location (and empty) compares severity and normalized
// location and ignores wording, except for findings without a location,
// which compare exactly (otherwise every location-less finding of one
// severity would be the same problem). Loops fingerprint through
// LoopPolicy.StallFingerprint, which first drops the findings that do not
// block.
func FingerprintBy(findings []Finding, match StallMatch) string {
	if match == StallExact {
		return Fingerprint(findings)
	}
	keys := make([]string, 0, len(findings))
	for _, f := range findings {
		loc := normalizeLocation(f.Location)
		if loc == "" {
			keys = append(keys, string(f.Severity)+"||"+strings.ToLower(strings.Join(strings.Fields(f.Description), " ")))
			continue
		}
		keys = append(keys, string(f.Severity)+"|"+loc)
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	return Digest(append([]string{"location"}, keys...))
}

// normalizeLocation lowercases a location and drops line/column suffixes
// ("a/b.go:61:3" → "a/b.go"), so a moved line is still the same place.
func normalizeLocation(loc string) string {
	loc = strings.ToLower(strings.TrimSpace(loc))
	for {
		i := strings.LastIndexByte(loc, ':')
		if i < 0 || i == len(loc)-1 || strings.Trim(loc[i+1:], "0123456789-") != "" {
			break
		}
		loc = loc[:i]
	}
	return strings.TrimSpace(loc)
}

// RequiredCheckResults lists, per repository output and required check name,
// the provider-verified check the run holds (the newest attempt: recordCheck
// keeps one per revision and name). Missing checks are absent.
func (r Run) RequiredCheckResults() []Check {
	var out []Check
	for _, o := range r.Outputs {
		if o.Kind != RefRepository {
			continue
		}
		for _, name := range r.Policy.RequiredChecks {
			var best *Check
			for i, c := range r.Checks {
				if c.Name != name || c.RepositoryID != o.RepositoryID || c.CommitSHA != o.CommitSHA || c.Provenance != ProviderVerified {
					continue
				}
				if best == nil || c.Attempt > best.Attempt || c.Attempt == best.Attempt && c.StartedAt.After(best.StartedAt) {
					best = &r.Checks[i]
				}
			}
			if best != nil {
				out = append(out, *best)
			}
		}
	}
	return out
}

// ChecksFailed reports whether a required check's result failed. A missing,
// pending, cancelled or unknown check is not failed.
func (r Run) ChecksFailed() bool {
	return slices.ContainsFunc(r.RequiredCheckResults(), func(c Check) bool { return c.Status == CheckFailed })
}

// FailedChecks lists the failed required check results.
func (r Run) FailedChecks() []Check {
	return slices.DeleteFunc(r.RequiredCheckResults(), func(c Check) bool { return c.Status != CheckFailed })
}

// WaitingForChecks reports whether a submitted code run waits for its
// required checks: none failed, and not all passed yet.
func (r Run) WaitingForChecks() bool {
	return r.Runner == Code && (r.State == Submitted || r.State == Completed) && len(r.Outputs) > 0 && !r.ChecksPassed() && !r.ChecksFailed()
}

// CheckFingerprint identifies failed checks by name and failed jobs, so two
// red pipelines that failed in different jobs are different problems (GitLab
// reports every pipeline as one check).
func CheckFingerprint(failed []Check) string {
	keys := make([]string, 0, len(failed))
	for _, c := range failed {
		jobs := slices.Clone(c.FailedJobs)
		slices.Sort(jobs)
		keys = append(keys, c.Name+"|"+strings.Join(jobs, ","))
	}
	slices.Sort(keys)
	keys = slices.Compact(keys)
	return Digest(append([]string{"check"}, keys...))
}

var (
	mdImage = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)
	mdLink  = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	rawURL  = regexp.MustCompile(`(?i)\b(?:https?|ftp|data|javascript):[^\s)>\]]+`)
)

// CleanCheckDetails makes a check's third-party details safe to store and
// show: a DetailsURL that is not an absolute https URL without userinfo is
// dropped; Summary and job names are redacted, stripped of markdown images
// and links (and bare URLs) and truncated; jobs are trimmed, deduplicated,
// sorted and bounded.
func CleanCheckDetails(c *Check) {
	c.DetailsURL = cleanDetailsURL(c.DetailsURL)
	c.Summary = truncateBytes(cleanText(c.Summary), MaxCheckSummaryBytes)
	var jobs []string
	for _, j := range c.FailedJobs {
		j = clip(strings.Join(strings.Fields(cleanText(j)), " "), MaxFailedJobRunes)
		if j != "" && !slices.Contains(jobs, j) {
			jobs = append(jobs, j)
		}
	}
	slices.Sort(jobs)
	if len(jobs) > MaxFailedJobs {
		jobs = jobs[:MaxFailedJobs]
	}
	c.FailedJobs = jobs
}

func cleanDetailsURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxURLBytes {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Opaque != "" {
		return ""
	}
	return u.String()
}

func cleanText(s string) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	s = redact.String(s)
	s = mdImage.ReplaceAllString(s, "")
	s = mdLink.ReplaceAllString(s, "$1")
	s = rawURL.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

func cleanTried(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxLoopTextRunes {
		return "", ErrValidation
	}
	return strings.Join(strings.Fields(s), " "), nil
}

// clip truncates s to n runes.
func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return strings.TrimSpace(string([]rune(s)[:n-1])) + "…"
}

// truncateBytes cuts s to at most n bytes on a rune boundary.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
