// Package redact scrubs secret-looking substrings from tool output.
// It is a best-effort filter, not a guarantee that a secret cannot appear.
package redact

import "regexp"

// Redactor applies a fixed set of patterns. A disabled or nil redactor
// returns the input unchanged. The value is safe for concurrent use.
type Redactor struct {
	enabled bool
}

// New returns a redactor that is active only when enabled is true.
func New(enabled bool) *Redactor {
	return &Redactor{enabled: enabled}
}

// Apply returns s with secret-looking substrings replaced.
func (r *Redactor) Apply(s string) string {
	if r == nil || !r.enabled || s == "" {
		return s
	}
	s = pemRE.ReplaceAllString(s, "[REDACTED PRIVATE KEY]")
	s = bearerRE.ReplaceAllString(s, "Bearer [REDACTED]")
	s = jwtRE.ReplaceAllString(s, "[REDACTED]")
	s = githubRE.ReplaceAllString(s, "[REDACTED]")
	s = slackRE.ReplaceAllString(s, "[REDACTED]")
	s = awsRE.ReplaceAllString(s, "[REDACTED]")
	s = userinfoRE.ReplaceAllString(s, "${1}[REDACTED]@")
	s = assignmentRE.ReplaceAllString(s, "${1}[REDACTED]")
	return s
}

var (
	pemRE = regexp.MustCompile(`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?-----END [A-Z0-9 ]*PRIVATE KEY-----`)
	// Bearer tokens are replaced before raw JWTs so the prefix is kept.
	bearerRE   = regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{8,}`)
	jwtRE      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)
	githubRE   = regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{20,}\b|\bgithub_pat_[A-Za-z0-9_]{20,}\b`)
	slackRE    = regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}\b`)
	awsRE      = regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`)
	userinfoRE = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://)([^/\s:@]+):([^/\s@]+)@`)
	// Keys are matched only at a boundary so "mytoken" is left alone.
	// Unquoted values shorter than 6 characters are left alone.
	assignmentRE = regexp.MustCompile(`(?i)((?:^|[^A-Za-z0-9_])["']?(?:password|passwd|secret|client_secret|credentials?|api[_-]?key|access[_-]?key|private[_-]?key|token)["']?\s*[=:]\s*)(?:"[^"]*"|'[^']*'|[^\s,}\]"']{6,})`)
)
