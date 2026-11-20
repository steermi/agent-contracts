package activity

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOutputRefNormalize(t *testing.T) {
	sha := strings.Repeat("c", 40)
	for _, tc := range []struct {
		name  string
		in    OutputRef
		want  OutputRef
		field string
	}{
		{"resource only", OutputRef{Kind: RefRepository, ResourceID: "repo", CommitSHA: sha}, OutputRef{Kind: RefRepository, ResourceID: "repo", RepositoryID: "repo", CommitSHA: sha}, ""},
		{"repository only", OutputRef{Kind: RefRepository, RepositoryID: "repo", CommitSHA: " " + strings.ToUpper(sha) + "\n"}, OutputRef{Kind: RefRepository, ResourceID: "repo", RepositoryID: "repo", CommitSHA: sha}, ""},
		{"sha-256", OutputRef{Kind: RefRepository, RepositoryID: "repo", CommitSHA: strings.Repeat("a", 64)}, OutputRef{Kind: RefRepository, ResourceID: "repo", RepositoryID: "repo", CommitSHA: strings.Repeat("a", 64)}, ""},
		{"no repository", OutputRef{Kind: RefRepository, CommitSHA: sha}, OutputRef{}, "repository_id"},
		{"mismatch", OutputRef{Kind: RefRepository, ResourceID: "other", RepositoryID: "repo", CommitSHA: sha}, OutputRef{}, "resource_id"},
		{"short sha", OutputRef{Kind: RefRepository, RepositoryID: "repo", CommitSHA: "abc1234"}, OutputRef{}, "commit_sha"},
		{"docs without id", OutputRef{Kind: RefDocs}, OutputRef{}, "resource_id"},
		{"docs", OutputRef{Kind: RefDocs, ResourceID: "page"}, OutputRef{Kind: RefDocs, ResourceID: "page"}, ""},
		{"unknown kind", OutputRef{Kind: "branch", ResourceID: "x"}, OutputRef{}, "kind"},
		{"no kind", OutputRef{ResourceID: "x"}, OutputRef{}, "kind"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := tc.in
			err := ref.Normalize()
			if tc.field == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.want, ref)
				return
			}
			require.ErrorIs(t, err, ErrValidation)
			var named *OutputRefError
			require.ErrorAs(t, err, &named)
			assert.Equal(t, tc.field, named.Field)
			assert.True(t, strings.HasPrefix(err.Error(), tc.field+" "))
		})
	}
}
