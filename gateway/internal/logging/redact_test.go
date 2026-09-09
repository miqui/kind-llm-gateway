package logging

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedact_MasksAPIKeyEmailPhone(t *testing.T) {
	payload := map[string]any{
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": "my key is sk-team-a-0000000000000000, email me at alice@example.com or call 415-555-0132",
			},
		},
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	redacted := Redact(raw, nil)
	s := string(redacted)

	if strings.Contains(s, "sk-team-a-0000000000000000") {
		t.Error("expected API key to be redacted")
	}
	if strings.Contains(s, "alice@example.com") {
		t.Error("expected email to be redacted")
	}
	if strings.Contains(s, "415-555-0132") {
		t.Error("expected phone number to be redacted")
	}
	if !strings.Contains(s, "[REDACTED]") {
		t.Error("expected at least one [REDACTED] marker in output")
	}
}

func TestRedact_InnocuousStringUntouched(t *testing.T) {
	payload := map[string]any{"content": "hello"}
	raw, _ := json.Marshal(payload)

	redacted := Redact(raw, nil)
	if !strings.Contains(string(redacted), "hello") {
		t.Error("expected innocuous short string to survive redaction untouched")
	}
}

func TestRedact_AuthorizationHeader(t *testing.T) {
	headers := map[string][]string{
		"Authorization": {"Bearer sk-team-a-0000000000000000"},
		"Content-Type":  {"application/json"},
	}
	redactedHeaders := RedactHeaders(headers)

	if redactedHeaders["Authorization"][0] != "[REDACTED]" {
		t.Errorf("Authorization = %q, want [REDACTED]", redactedHeaders["Authorization"][0])
	}
	if redactedHeaders["Content-Type"][0] != "application/json" {
		t.Errorf("Content-Type should be left untouched, got %q", redactedHeaders["Content-Type"][0])
	}
}

func TestRedact_ExtraPatterns(t *testing.T) {
	raw := []byte(`{"content":"secret-code-XYZ123"}`)
	redacted := Redact(raw, []string{`secret-code-[A-Z0-9]+`})
	if strings.Contains(string(redacted), "secret-code-XYZ123") {
		t.Error("expected extra pattern match to be redacted")
	}
}

func TestRedact_GenericLongToken(t *testing.T) {
	// A 32+ char opaque token not matching sk-... shape should still be
	// masked as a generic long-token.
	raw := []byte(`{"content":"token=abcdefghij0123456789ABCDEFGHIJ01"}`)
	redacted := Redact(raw, nil)
	if strings.Contains(string(redacted), "abcdefghij0123456789ABCDEFGHIJ01") {
		t.Error("expected 32+ char opaque token to be redacted")
	}
}
