package redact

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tokens are assembled at run time so no credential-shaped literal sits in
// the repository.
func fake(prefix string, n int) string { return prefix + strings.Repeat("a1B2", n/4+1)[:n] }

func TestStringRedactsKnownFormats(t *testing.T) {
	key := "-----BEGIN OPENSSH " + "PRIVATE KEY-----\nAAAA\nBBBB\n-----END OPENSSH PRIVATE KEY-----"
	cases := map[string]string{
		"github":       "export GH_TOKEN=" + fake("gh"+"p_", 36),
		"github pat":   fake("github"+"_pat_", 60),
		"gitlab":       "git push https://oauth2:" + fake("glpat"+"-", 20) + "@gitlab.com/a/b.git",
		"anthropic":    "ANTHROPIC_API_KEY=" + fake("sk-"+"ant-", 40),
		"openai":       fake("sk-"+"proj-", 48),
		"aws":          "AKIA" + strings.Repeat("A", 16),
		"slack":        fake("xox"+"b-", 24),
		"jwt":          fake("ey"+"J", 20) + "." + fake("ey"+"J", 20) + "." + fake("", 20),
		"bearer":       "curl -H 'Authorization: Bearer " + fake("", 40) + "'",
		"steermi":      "refresh " + fake("st"+"d_", 43),
		"url password": "https://deploy:hunter2@example.com/repo.git",
		"private key":  key,
	}
	for name, in := range cases {
		out := String(in)
		assert.Contains(t, out, Marker, name)
		assert.NotContains(t, out, "a1B2a1B2a1B2", name)
	}
	assert.NotContains(t, String(key), "AAAA")
	assert.Equal(t, "https://[REDACTED]@example.com/repo.git", String("https://deploy:hunter2@example.com/repo.git"))
}

func TestStringKeepsOrdinaryText(t *testing.T) {
	for _, s := range []string{
		"go test ./... -count=1",
		"https://example.com/path?q=1",
		"the token expired, run steermi login",
		"sk-short",
		"commit 0123456789abcdef0123456789abcdef01234567",
		"feat/STE-123-add-retry",
	} {
		assert.Equal(t, s, String(s))
	}
}

func TestBytesKeepsJSONValid(t *testing.T) {
	in, err := json.Marshal(map[string]any{
		"command": "curl -H \"Authorization: Bearer " + fake("", 40) + "\" https://u:p@x.io",
		"key":     "-----BEGIN RSA " + "PRIVATE KEY-----\nabc\n-----END RSA PRIVATE KEY-----",
		"next":    "kept",
	})
	require.NoError(t, err)
	out := Bytes(in)
	var v map[string]string
	require.NoError(t, json.Unmarshal(out, &v), string(out))
	assert.Equal(t, "kept", v["next"])
	assert.Equal(t, Marker, v["key"])
	assert.NotContains(t, v["command"], "a1B2a1B2")
}
