// Package locallaunch holds identifiers and secrets shared by the Steermi
// service and the steermi CLI for running Steermi tasks on a developer's own
// machine: launch IDs, device secrets, PKCE and launch branch names.
package locallaunch

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"math/big"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
)

// PublicIDPrefix marks a launch ID that users copy into `steermi run`.
const PublicIDPrefix = "lx_"

const (
	publicIDLen      = 10
	publicIDAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	maxBranchLen     = 60
)

var publicIDPattern = regexp.MustCompile(`^lx_[A-Za-z0-9]{10}$`)

// NewPublicID returns a random launch ID such as "lx_7Q4XK2M9ab". It is not a
// secret: claiming a launch also requires the owner's device credential.
func NewPublicID() (string, error) {
	var b strings.Builder
	b.WriteString(PublicIDPrefix)
	max := big.NewInt(int64(len(publicIDAlphabet)))
	for range publicIDLen {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b.WriteByte(publicIDAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// IsPublicID reports whether s is a well-formed launch ID.
func IsPublicID(s string) bool {
	return publicIDPattern.MatchString(s)
}

// NewSecret returns prefix followed by 32 random bytes, base64url encoded.
// Used for device refresh tokens ("std_") and pairing codes ("stp_").
func NewSecret(prefix string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashSecret returns the SHA-256 hex digest stored instead of a secret.
func HashSecret(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// PKCEChallenge derives the S256 code challenge for a verifier (RFC 7636).
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// VerifyPKCE checks an S256 verifier against the stored challenge in
// constant time.
func VerifyPKCE(verifier, challenge string) bool {
	if verifier == "" || challenge == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(PKCEChallenge(verifier)), []byte(challenge)) == 1
}

// BranchName builds the launch branch, e.g. "feat/STE-123-add-retry".
// Bugs get "fix/", chores "chore/", everything else "feat/".
func BranchName(workItemType, identifier, title string) string {
	prefix := "feat/"
	switch strings.ToLower(workItemType) {
	case "bug":
		prefix = "fix/"
	case "chore":
		prefix = "chore/"
	}
	base := prefix + identifier
	slug := slugify(title)
	if slug == "" {
		return base
	}
	room := maxBranchLen - len(base) - 1
	if room <= 0 {
		return base
	}
	if len(slug) > room {
		slug = strings.TrimRight(slug[:room], "-")
	}
	if slug == "" {
		return base
	}
	return base + "-" + slug
}

func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r):
			continue
		case r == 'ł':
			r = 'l'
		}
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
			continue
		}
		if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
