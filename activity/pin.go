package activity

import "slices"

// Pinning: what a run reads (its inputs and their digest), set when it is
// bound to a candidate or re-pinned before confirmation.

// BindCandidate points a findings run at a submitted code run's revision before it is
// confirmed. The caller resolves digest and inputs for the candidate outputs;
// a malformed ref or an empty digest is refused (ErrValidation).
func (r *Run) BindCandidate(candidate Run, digest string, inputs []OutputRef) error {
	if err := r.pin(digest, inputs); err != nil {
		return err
	}
	r.Outputs = slices.Clone(candidate.Outputs)
	r.Checks = slices.Clone(candidate.Checks)
	r.CandidateAuthorID = candidate.OwnerID
	r.PredecessorID = candidate.ID
	return nil
}

// RepinSources replaces the pinned inputs of a suggested docs page run with
// no predecessor before it is confirmed (a docs check re-reads its related
// pages as the person who confirms it). A findings run's outputs follow its
// inputs. Another run, state or a chain step is ErrConflict; a malformed
// ref, an empty digest or inputs that no longer pin the subject page are
// ErrValidation.
func (r *Run) RepinSources(digest string, inputs []OutputRef) error {
	if !r.IsDocPage() || r.State != Suggested || r.PredecessorID != "" {
		return ErrConflict
	}
	page := r.RunSubject().ID
	if !slices.ContainsFunc(inputs, func(o OutputRef) bool { return o.Kind == RefDocs && o.ResourceID == page }) {
		return ErrValidation
	}
	if err := r.pin(digest, inputs); err != nil {
		return err
	}
	if r.Runner == Findings {
		r.Outputs = Sources(r.Inputs)
	}
	return nil
}

// pin replaces the run's input digest and pinned refs, each a resolved ref
// (OutputRef.Validate).
func (r *Run) pin(digest string, inputs []OutputRef) error {
	if digest == "" {
		return ErrValidation
	}
	for _, o := range inputs {
		if err := o.Validate(); err != nil {
			return err
		}
	}
	r.InputDigest = digest
	r.Inputs = slices.Clone(inputs)
	return nil
}
