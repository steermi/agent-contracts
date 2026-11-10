package locallaunch

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPublicID(t *testing.T) {
	seen := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id, err := NewPublicID()
		require.NoError(t, err)
		assert.True(t, IsPublicID(id), id)
		assert.False(t, seen[id], "duplicate public id %s", id)
		seen[id] = true
	}
}

func TestIsPublicID(t *testing.T) {
	assert.True(t, IsPublicID("lx_7Q4XK2M9ab"))
	for _, bad := range []string{
		"", "lx_", "lx_short", "lx_7Q4XK2M9abc", "LX_7Q4XK2M9ab", "lx_7Q4XK2M9a!",
		"lx_../../etc", "lx_7Q4XK2M9a b", "zz_7Q4XK2M9ab", "lx_7Q4XK2M9ab\n",
	} {
		assert.False(t, IsPublicID(bad), "%q must be rejected", bad)
	}
}

func TestNewSecretAndHash(t *testing.T) {
	a, err := NewSecret("std_")
	require.NoError(t, err)
	b, err := NewSecret("std_")
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(a, "std_"))
	assert.NotEqual(t, a, b)
	assert.GreaterOrEqual(t, len(a), 4+43)
	assert.Len(t, HashSecret(a), 64)
	assert.Equal(t, HashSecret(a), HashSecret(a))
	assert.NotEqual(t, HashSecret(a), HashSecret(b))
}

func TestVerifyPKCE(t *testing.T) {
	// RFC 7636 appendix B test vector.
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	challenge := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	assert.True(t, VerifyPKCE(verifier, challenge))
	assert.Equal(t, challenge, PKCEChallenge(verifier))
	assert.False(t, VerifyPKCE(verifier+"x", challenge))
	assert.False(t, VerifyPKCE("", challenge))
	assert.False(t, VerifyPKCE(verifier, ""))
}

func TestBranchName(t *testing.T) {
	tests := []struct {
		typ, key, title, want string
	}{
		{"story", "STE-123", "Add retry to webhook sender", "feat/STE-123-add-retry-to-webhook-sender"},
		{"bug", "STE-9", "Crash on login!", "fix/STE-9-crash-on-login"},
		{"task", "ATL-22", "Zażółć gęślą jaźń / cache", "feat/ATL-22-zazolc-gesla-jazn-cache"},
		{"chore", "STE-1", "   ", "chore/STE-1"},
		{"story", "STE-77", strings.Repeat("very long title ", 20), ""},
	}
	for _, tt := range tests {
		got := BranchName(tt.typ, tt.key, tt.title)
		if tt.want != "" {
			assert.Equal(t, tt.want, got)
		}
		assert.LessOrEqual(t, len(got), 60, got)
		assert.False(t, strings.HasSuffix(got, "-"), got)
		assert.True(t, strings.Contains(got, tt.key), got)
	}
}
