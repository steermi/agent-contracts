// Package redact removes credentials from text sent to or stored by Steermi,
// such as mirrored transcripts and CI check summaries. It is a safety net for
// tokens a tool happened to print, not a guarantee: keep secrets out of
// sessions. The steermi CLI and the Steermi service share this one list.
package redact

import "regexp"

// Marker replaces every redacted value.
const Marker = "[REDACTED]"

var patterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[^"]*?-----END [A-Z ]*PRIVATE KEY-----`), Marker},
	// Credentials embedded in URLs: https://user:token@host
	{regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)[^/\s:@"'\\]+:[^/\s@"'\\]+@`), "${1}" + Marker + "@"},
	{regexp.MustCompile(`(?i)\b(bearer|token)\s+[A-Za-z0-9._~+/=-]{20,}`), "${1} " + Marker},
	// Steermi device refresh tokens and pairing codes.
	{regexp.MustCompile(`\bst[dp]_[A-Za-z0-9_-]{32,}`), Marker},
	{regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{36,}`), Marker},
	{regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{40,}`), Marker},
	{regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`), Marker},
	{regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_-]{20,}`), Marker},
	{regexp.MustCompile(`\bsk-(?:proj-)?[A-Za-z0-9_-]{32,}`), Marker},
	{regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`), Marker},
	{regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`), Marker},
	{regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), Marker},
}

// String returns s with known credential formats replaced by Marker.
func String(s string) string {
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

// Bytes is String for JSON or other encoded text. The patterns never match
// quotes or backslashes, so valid JSON stays valid.
func Bytes(b []byte) []byte {
	for _, p := range patterns {
		b = p.re.ReplaceAll(b, []byte(p.repl))
	}
	return b
}
