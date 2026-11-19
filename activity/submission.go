package activity

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// Submissions and evidence: what a submit records on a run (outputs,
// findings, assumptions), dismissing findings, recorded checks and whether
// the required checks passed.

// ChecksPassed requires every policy check to have succeeded, provider
// verified, on every submitted repository revision.
func (r Run) ChecksPassed() bool {
	for _, o := range r.Outputs {
		if o.Kind != RefRepository {
			continue
		}
		for _, name := range r.Policy.RequiredChecks {
			if !slices.ContainsFunc(r.Checks, func(c Check) bool {
				return c.Name == name && c.RepositoryID == o.RepositoryID && c.CommitSHA == o.CommitSHA && c.Provenance == ProviderVerified && c.Status == CheckSuccess
			}) {
				return false
			}
		}
	}
	return true
}

// dismissFinding sets one open finding aside with the person's reason. The
// findings stay on the run; neither the evidence digest nor the outcome
// looks at dismissals, so decisions and followed flow hops keep matching.
func (r *Run) dismissFinding(c Command) error {
	reason := strings.TrimSpace(c.Reason)
	if c.FindingID == "" || reason == "" || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > MaxDismissalReasonRunes {
		return ErrValidation
	}
	i := slices.IndexFunc(r.Findings, func(f Finding) bool { return f.ID == c.FindingID })
	if i < 0 {
		return ErrValidation
	}
	if !r.Findings[i].open() {
		return ErrConflict
	}
	r.Findings[i].DismissalReason = reason
	return nil
}

// open reports whether no person has dismissed the finding.
func (f Finding) open() bool { return f.DismissalReason == "" }

func (r *Run) submit(c Command) error {
	if len(c.Outputs) == 0 {
		return ErrValidation
	}
	for _, o := range c.Outputs {
		if err := o.Validate(); err != nil {
			return err
		}
		if o.Kind == RefContext || r.Runner == Answer && o.Kind == RefRepository {
			return ErrForbidden
		}
	}
	if pinned := RepositoryIDs(r.Inputs); r.Runner == Code && len(pinned) > 0 {
		submitted := RepositoryIDs(c.Outputs)
		if len(submitted) == 0 {
			return ErrValidation
		}
		for _, id := range submitted {
			if !slices.Contains(pinned, id) {
				return ErrForbidden
			}
		}
	}
	assumptions, err := cleanAssumptions(c.Assumptions)
	if err != nil {
		return err
	}
	tried, err := cleanTried(c.Tried)
	if err != nil {
		return err
	}
	r.Tried = tried
	r.Outputs = slices.Clone(c.Outputs)
	r.Summary = strings.TrimSpace(c.Summary)
	r.Assumptions = assumptions
	r.State = Submitted
	return nil
}

// cleanAssumptions trims a submission's assumptions and drops blanks; too
// many or too long ones are invalid rather than silently cut.
func cleanAssumptions(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if !utf8.ValidString(s) || utf8.RuneCountInString(s) > MaxAssumptionRunes {
			return nil, ErrValidation
		}
		out = append(out, s)
	}
	if len(out) > MaxAssumptions {
		return nil, ErrValidation
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// recordCheck keeps the newest provider attempt per repository revision and
// check name; late or replayed deliveries never regress a settled result.
func (r *Run) recordCheck(c *Check) error {
	if c == nil {
		return ErrValidation
	}
	check := *c
	if check.RepositoryID == "" || !fullSHA.MatchString(check.CommitSHA) || check.Name == "" || check.ProviderRunID == "" || check.Attempt < 1 ||
		!slices.Contains([]CheckStatus{CheckPending, CheckSuccess, CheckFailed, CheckCancelled, CheckUnknown}, check.Status) {
		return ErrValidation
	}
	if !slices.ContainsFunc(r.Outputs, func(o OutputRef) bool {
		return o.RepositoryID == check.RepositoryID && o.CommitSHA == check.CommitSHA
	}) {
		return ErrStale
	}
	check.Provenance = ProviderVerified
	CleanCheckDetails(&check)
	for i, old := range r.Checks {
		if old.RepositoryID != check.RepositoryID || old.CommitSHA != check.CommitSHA || old.Name != check.Name {
			continue
		}
		older := check.StartedAt.Before(old.StartedAt) || check.StartedAt.Equal(old.StartedAt) && check.Attempt < old.Attempt
		regress := old.ProviderRunID == check.ProviderRunID && old.Attempt == check.Attempt && check.Status == CheckPending && old.Status != CheckPending && old.Status != CheckUnknown
		if !older && !regress {
			r.Checks[i] = check
		}
		return nil
	}
	r.Checks = append(r.Checks, check)
	return nil
}
