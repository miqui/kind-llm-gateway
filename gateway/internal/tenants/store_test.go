package tenants

import (
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestUpsertAndGet(t *testing.T) {
	st := newTestStore(t)
	tn := Tenant{
		ID:              "team-a",
		Name:            "Team A",
		APIKey:          "sk-team-a-0000000000000000",
		RateLimitRPS:    2,
		DailyTokenQuota: 100000,
		AllowedModels:   []string{"qwen2.5-1.5b"},
		WorkloadClass:   "team",
		CostTier:        "standard",
	}
	if err := st.Upsert(tn); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := st.GetByAPIKey("sk-team-a-0000000000000000")
	if err != nil {
		t.Fatalf("GetByAPIKey: %v", err)
	}
	if got.ID != "team-a" || got.DailyTokenQuota != 100000 || len(got.AllowedModels) != 1 || got.AllowedModels[0] != "qwen2.5-1.5b" {
		t.Fatalf("unexpected tenant: %+v", got)
	}

	got2, err := st.GetByID("team-a")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got2.Name != "Team A" {
		t.Fatalf("unexpected tenant name: %+v", got2)
	}
}

func TestGetByAPIKeyNotFound(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.GetByAPIKey("nope"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestUpsertUpdatesExisting(t *testing.T) {
	st := newTestStore(t)
	tn := Tenant{ID: "team-b", Name: "Team B", APIKey: "key-1", RateLimitRPS: 1, DailyTokenQuota: 10}
	if err := st.Upsert(tn); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	tn.DailyTokenQuota = 999
	if err := st.Upsert(tn); err != nil {
		t.Fatalf("Upsert update: %v", err)
	}
	got, err := st.GetByID("team-b")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.DailyTokenQuota != 999 {
		t.Fatalf("expected updated quota 999, got %d", got.DailyTokenQuota)
	}
}

func TestDelete(t *testing.T) {
	st := newTestStore(t)
	tn := Tenant{ID: "team-c", Name: "Team C", APIKey: "key-c", RateLimitRPS: 1, DailyTokenQuota: 10}
	if err := st.Upsert(tn); err != nil {
		t.Fatalf("Upsert: %v", err)
	}
	if err := st.Delete("team-c"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := st.GetByID("team-c"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestBootstrapFromJSONIdempotent(t *testing.T) {
	st := newTestStore(t)
	data := []byte(`[
		{"id":"team-a","name":"Team A","api_key":"sk-team-a-0000000000000000","rate_limit_rps":2,"daily_token_quota":100000,"allowed_models":["qwen2.5-1.5b"],"workload_class":"team","cost_tier":"standard"},
		{"id":"team-b","name":"Team B","api_key":"sk-team-b-0000000000000000","rate_limit_rps":1,"daily_token_quota":10000,"allowed_models":["qwen2.5-1.5b"],"workload_class":"team","cost_tier":"economy"}
	]`)

	n, err := st.BootstrapFromJSON(data)
	if err != nil {
		t.Fatalf("BootstrapFromJSON: %v", err)
	}
	if n != 2 {
		t.Fatalf("expected 2 inserted, got %d", n)
	}

	// Simulate manual admin change, then re-run bootstrap: should NOT overwrite.
	tn, err := st.GetByID("team-a")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	tn.DailyTokenQuota = 42
	if err := st.Upsert(tn); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	n2, err := st.BootstrapFromJSON(data)
	if err != nil {
		t.Fatalf("BootstrapFromJSON second run: %v", err)
	}
	if n2 != 0 {
		t.Fatalf("expected 0 inserted on second bootstrap, got %d", n2)
	}

	got, err := st.GetByID("team-a")
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.DailyTokenQuota != 42 {
		t.Fatalf("bootstrap should not overwrite existing tenant, got quota=%d", got.DailyTokenQuota)
	}
}

