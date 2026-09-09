package logging

import (
	"testing"
	"time"
)

func newTestStore(t *testing.T, maxRows int) *Store {
	t.Helper()
	s, err := Open(":memory:", maxRows)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sampleRecord(tenantID, model string) Record {
	return Record{
		TS:               time.Now().UTC(),
		TenantID:         tenantID,
		Model:            model,
		Route:            "/v1/chat/completions",
		PromptTokens:     10,
		CompletionTokens: 20,
		StatusCode:       200,
		LatencyMS:        15,
		RequestJSON:      `{"content":"hello"}`,
		ResponseJSON:     `{"content":"world"}`,
	}
}

func TestStore_InsertAndQueryRecent(t *testing.T) {
	s := newTestStore(t, 10000)

	if err := s.Insert(sampleRecord("t1", "model-a")); err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := s.Insert(sampleRecord("t1", "model-b")); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	recs, err := s.QueryRecent(10)
	if err != nil {
		t.Fatalf("QueryRecent: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("len(recs) = %d, want 2", len(recs))
	}
	// Most recent first.
	if recs[0].Model != "model-b" {
		t.Errorf("recs[0].Model = %q, want model-b", recs[0].Model)
	}
	if recs[0].TenantID != "t1" {
		t.Errorf("recs[0].TenantID = %q, want t1", recs[0].TenantID)
	}
}

func TestStore_QueryRecentLimit(t *testing.T) {
	s := newTestStore(t, 10000)
	for i := 0; i < 5; i++ {
		if err := s.Insert(sampleRecord("t1", "model-a")); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	recs, err := s.QueryRecent(2)
	if err != nil {
		t.Fatalf("QueryRecent: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("len(recs) = %d, want 2", len(recs))
	}
}

func TestStore_RetentionSweep(t *testing.T) {
	s := newTestStore(t, 3)

	for i := 0; i < 10; i++ {
		if err := s.Insert(sampleRecord("t1", "model-a")); err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}

	recs, err := s.QueryRecent(100)
	if err != nil {
		t.Fatalf("QueryRecent: %v", err)
	}
	if len(recs) != 3 {
		t.Fatalf("len(recs) = %d, want 3 after retention sweep", len(recs))
	}
}

func TestStore_RedactionRoundTrip(t *testing.T) {
	s := newTestStore(t, 10000)

	rec := sampleRecord("t1", "model-a")
	rec.RequestJSON = string(Redact([]byte(`{"content":"my key is sk-team-a-0000000000000000, email alice@example.com, call 415-555-0132"}`), nil))
	rec.ResponseJSON = string(Redact([]byte(`{"content":"hello"}`), nil))

	if err := s.Insert(rec); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	recs, err := s.QueryRecent(1)
	if err != nil {
		t.Fatalf("QueryRecent: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("len(recs) = %d, want 1", len(recs))
	}
	got := recs[0].RequestJSON
	for _, forbidden := range []string{"sk-team-a-0000000000000000", "alice@example.com", "415-555-0132"} {
		if contains(got, forbidden) {
			t.Errorf("stored request_json still contains %q", forbidden)
		}
	}
	if !contains(recs[0].ResponseJSON, "hello") {
		t.Error("expected innocuous response content to survive")
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}
