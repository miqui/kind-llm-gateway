// Package logging implements the redacting request/response logger for
// llmgw: sensitive-field redaction applied before any persistence, and a
// bounded-retention SQLite store for logged requests.
package logging

import "regexp"

// Compiled-in redaction patterns, applied to raw JSON text before it is
// persisted. Order matters only in that overlapping matches are each
// replaced independently; regexp.MustCompile panics at init time on a bad
// pattern, which is desired (fail fast on a packaging bug).
var (
	// apiKeyPattern matches OpenAI-style secret keys, e.g. sk-..., and
	// similarly shaped provider keys (prefix + 16+ alnum/dash/underscore
	// chars).
	apiKeyPattern = regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{16,}\b`)

	// genericLongTokenPattern matches any standalone run of 32+ alphanumeric
	// characters, which is the shape of most bearer tokens, API keys, and
	// session identifiers regardless of provider-specific prefix.
	genericLongTokenPattern = regexp.MustCompile(`\b[A-Za-z0-9_-]{32,}\b`)

	// emailPattern matches RFC-5322-ish email addresses (practical subset).
	emailPattern = regexp.MustCompile(`\b[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}\b`)

	// usPhonePattern matches common US phone number formats:
	// 415-555-0132, (415) 555-0132, 415.555.0132, 4155550132.
	usPhonePattern = regexp.MustCompile(`\b(?:\(\d{3}\)\s?|\d{3}[-.\s])\d{3}[-.\s]?\d{4}\b`)
)

const redactedMarker = "[REDACTED]"

// Redact masks sensitive substrings in raw (typically a JSON-encoded
// request or response body) before it is persisted. extraPatterns are
// additional regex strings (e.g. from config.RedactExtraPatterns /
// REDACT_EXTRA_PATTERNS) appended to the compiled-in rule set. Invalid
// extra patterns are skipped rather than causing a panic, since they may
// originate from operator-supplied config at runtime.
func Redact(raw []byte, extraPatterns []string) []byte {
	out := raw
	out = apiKeyPattern.ReplaceAll(out, []byte(redactedMarker))
	out = genericLongTokenPattern.ReplaceAll(out, []byte(redactedMarker))
	out = emailPattern.ReplaceAll(out, []byte(redactedMarker))
	out = usPhonePattern.ReplaceAll(out, []byte(redactedMarker))

	for _, p := range extraPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			continue
		}
		out = re.ReplaceAll(out, []byte(redactedMarker))
	}

	return out
}

// RedactHeaders returns a copy of headers with the Authorization value(s)
// replaced by the redaction marker. All other headers pass through
// unmodified.
func RedactHeaders(headers map[string][]string) map[string][]string {
	out := make(map[string][]string, len(headers))
	for k, v := range headers {
		if isAuthorizationHeader(k) {
			redacted := make([]string, len(v))
			for i := range v {
				redacted[i] = redactedMarker
			}
			out[k] = redacted
			continue
		}
		out[k] = v
	}
	return out
}

func isAuthorizationHeader(key string) bool {
	if len(key) != len("Authorization") {
		return false
	}
	for i := 0; i < len(key); i++ {
		a, b := key[i], "Authorization"[i]
		if a == b {
			continue
		}
		// case-insensitive compare for ASCII letters
		if toLowerByte(a) != toLowerByte(b) {
			return false
		}
	}
	return true
}

func toLowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}
