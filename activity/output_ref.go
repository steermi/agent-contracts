package activity

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

type RefKind string

const (
	RefRepository RefKind = "repository"
	RefDocs       RefKind = "docs"
	RefWorkItem   RefKind = "work_item"
	RefContext    RefKind = "context"
)

type OutputRef struct {
	Restricted   bool    `json:"restricted,omitempty"`
	Kind         RefKind `json:"kind"`
	ResourceID   string  `json:"resource_id"`
	AreaID       string  `json:"area_id,omitempty"`
	Revision     string  `json:"revision,omitempty"`
	RepositoryID string  `json:"repository_id,omitempty"`
	CommitSHA    string  `json:"commit_sha,omitempty"`
	Hash         string  `json:"hash,omitempty"`
}

var fullSHA = regexp.MustCompile(`^[a-f0-9]{40}([a-f0-9]{24})?$`)

// Validate accepts a pinned ref: what it names (problem) plus the revision
// and hash the host resolved for docs, work item and context refs.
func (o OutputRef) Validate() error {
	if o.problem() != nil {
		return ErrValidation
	}
	if o.Kind != RefRepository && (o.Revision == "" || len(o.Hash) != 64) {
		return ErrValidation
	}
	return nil
}

// Sources returns the refs that are not resolved project context.
func Sources(refs []OutputRef) []OutputRef {
	return slices.DeleteFunc(slices.Clone(refs), func(ref OutputRef) bool { return ref.Kind == RefContext })
}

// RepositoryIDs lists the repositories pinned by refs, in order.
func RepositoryIDs(refs []OutputRef) []string {
	var ids []string
	for _, ref := range refs {
		if ref.Kind == RefRepository && !slices.Contains(ids, ref.RepositoryID) {
			ids = append(ids, ref.RepositoryID)
		}
	}
	return ids
}

// AreaIDs lists the docs areas of pinned docs refs, in order.
func AreaIDs(refs []OutputRef) []string {
	var ids []string
	for _, ref := range refs {
		if ref.Kind == RefDocs && ref.AreaID != "" && !slices.Contains(ids, ref.AreaID) {
			ids = append(ids, ref.AreaID)
		}
	}
	return ids
}

// OutputRefError names the field of a submitted output that cannot be
// pinned and what is wrong with it. It wraps ErrValidation.
type OutputRefError struct {
	Field   string
	Problem string
}

func (e *OutputRefError) Error() string { return e.Field + " " + e.Problem }
func (e *OutputRefError) Unwrap() error { return ErrValidation }

// Normalize completes a submitted output from what it implies and refuses,
// naming the field, what the host cannot resolve: a repository output's
// resource is the repository (either id fills the other) and pins a full
// commit SHA (SHA-1 or SHA-256, lower-cased here, never a short SHA); every
// other output names its resource. Revisions and hashes are the host's to
// resolve. Callers (agents' tools, the CLI, the gateway) normalize before
// submitting so a model can fix its call instead of the run being fenced.
func (o *OutputRef) Normalize() error {
	if o.Kind == RefRepository {
		if o.RepositoryID == "" {
			o.RepositoryID = o.ResourceID
		}
		if o.ResourceID == "" {
			o.ResourceID = o.RepositoryID
		}
		if o.ResourceID != o.RepositoryID {
			return &OutputRefError{"resource_id", "must equal repository_id for a repository output"}
		}
		o.CommitSHA = strings.ToLower(strings.TrimSpace(o.CommitSHA))
	}
	if p := o.problem(); p != nil {
		return p
	}
	return nil
}

// problem is the one check of what a ref names, shared by Normalize and
// Validate: a known kind and its resource; a repository ref also its
// repository and a full commit SHA.
func (o OutputRef) problem() *OutputRefError {
	switch o.Kind {
	case RefRepository:
		switch {
		case o.RepositoryID == "":
			return &OutputRefError{"repository_id", "is required for a repository output"}
		case o.ResourceID == "":
			return &OutputRefError{"resource_id", "is required for a repository output"}
		case !fullSHA.MatchString(o.CommitSHA):
			return &OutputRefError{"commit_sha", fmt.Sprintf("must be the full commit SHA (40 or 64 hex characters, git rev-parse HEAD), got %q", o.CommitSHA)}
		}
	case RefDocs, RefWorkItem, RefContext:
		if o.ResourceID == "" {
			return &OutputRefError{"resource_id", fmt.Sprintf("is required for a %s output", o.Kind)}
		}
	default:
		return &OutputRefError{"kind", fmt.Sprintf("must be repository, docs, work_item or context, got %q", o.Kind)}
	}
	return nil
}
