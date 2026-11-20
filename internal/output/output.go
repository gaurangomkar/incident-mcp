// Package output trims tool results so a single call cannot fill a context window.
package output

import "unicode/utf8"

const marker = "\n[output truncated]\n"

// Truncate returns s unchanged when it fits in max bytes.
// Otherwise it cuts on a UTF-8 boundary and appends a marker.
// A non-positive max disables the limit.
func Truncate(s string, max int) (string, bool) {
	if max <= 0 || len(s) <= max {
		return s, false
	}
	budget := max - len(marker)
	if budget < 0 {
		return marker, true
	}
	if budget > len(s) {
		budget = len(s)
	}
	for budget > 0 && !utf8.RuneStart(s[budget]) {
		budget--
	}
	return s[:budget] + marker, true
}
