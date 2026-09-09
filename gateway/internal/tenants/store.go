// Package tenants provides the tenant registry backed by SQLite.
package tenants

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a tenant lookup finds nothing.
var ErrNotFound = errors.New("tenant not found")

// Store is a SQLite-backed tenant registry.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the SQLite database at path and
// ensures the tenants table exists.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// SQLite via modernc.org driver is not safe for concurrent writers
	// across multiple connections; keep it simple for our workload.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tenants (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			api_key TEXT NOT NULL UNIQUE,
			rate_limit_rps REAL NOT NULL,
			daily_token_quota INTEGER NOT NULL,
			allowed_models TEXT NOT NULL,
			workload_class TEXT NOT NULL,
			cost_tier TEXT NOT NULL
		)
	`); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db}, nil
}

// Close closes the underlying database handle.
func (s *Store) Close() error {
	return s.db.Close()
}

func scanTenant(row interface {
	Scan(dest ...any) error
}) (Tenant, error) {
	var t Tenant
	var models string
	if err := row.Scan(&t.ID, &t.Name, &t.APIKey, &t.RateLimitRPS, &t.DailyTokenQuota, &models, &t.WorkloadClass, &t.CostTier); err != nil {
		return Tenant{}, err
	}
	t.AllowedModels = splitModels(models)
	return t, nil
}

func splitModels(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ",")
}

func joinModels(models []string) string {
	return strings.Join(models, ",")
}

// GetByAPIKey looks up a tenant by its API key.
func (s *Store) GetByAPIKey(key string) (Tenant, error) {
	row := s.db.QueryRow(`SELECT id, name, api_key, rate_limit_rps, daily_token_quota, allowed_models, workload_class, cost_tier FROM tenants WHERE api_key = ?`, key)
	t, err := scanTenant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, ErrNotFound
	}
	return t, err
}

// GetByID looks up a tenant by its ID.
func (s *Store) GetByID(id string) (Tenant, error) {
	row := s.db.QueryRow(`SELECT id, name, api_key, rate_limit_rps, daily_token_quota, allowed_models, workload_class, cost_tier FROM tenants WHERE id = ?`, id)
	t, err := scanTenant(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Tenant{}, ErrNotFound
	}
	return t, err
}

// Upsert inserts or updates a tenant record.
func (s *Store) Upsert(t Tenant) error {
	_, err := s.db.Exec(`
		INSERT INTO tenants (id, name, api_key, rate_limit_rps, daily_token_quota, allowed_models, workload_class, cost_tier)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name,
			api_key=excluded.api_key,
			rate_limit_rps=excluded.rate_limit_rps,
			daily_token_quota=excluded.daily_token_quota,
			allowed_models=excluded.allowed_models,
			workload_class=excluded.workload_class,
			cost_tier=excluded.cost_tier
	`, t.ID, t.Name, t.APIKey, t.RateLimitRPS, t.DailyTokenQuota, joinModels(t.AllowedModels), t.WorkloadClass, t.CostTier)
	return err
}

// Delete removes a tenant by ID.
func (s *Store) Delete(id string) error {
	_, err := s.db.Exec(`DELETE FROM tenants WHERE id = ?`, id)
	return err
}

// bootstrapTenant mirrors the JSON shape used in the bootstrap ConfigMap.
type bootstrapTenant struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	APIKey          string   `json:"api_key"`
	RateLimitRPS    float64  `json:"rate_limit_rps"`
	DailyTokenQuota int64    `json:"daily_token_quota"`
	AllowedModels   []string `json:"allowed_models"`
	WorkloadClass   string   `json:"workload_class"`
	CostTier        string   `json:"cost_tier"`
}

// BootstrapFromJSON inserts tenants from JSON data if they do not already
// exist (insert-if-absent semantics), so the admin API remains the runtime
// source of truth for tenants that already exist. Returns the number of
// tenants actually inserted.
func (s *Store) BootstrapFromJSON(data []byte) (int, error) {
	var list []bootstrapTenant
	if err := json.Unmarshal(data, &list); err != nil {
		return 0, err
	}

	inserted := 0
	for _, bt := range list {
		if _, err := s.GetByID(bt.ID); err == nil {
			continue // already present, do not overwrite
		} else if !errors.Is(err, ErrNotFound) {
			return inserted, err
		}

		t := Tenant{
			ID:              bt.ID,
			Name:            bt.Name,
			APIKey:          bt.APIKey,
			RateLimitRPS:    bt.RateLimitRPS,
			DailyTokenQuota: bt.DailyTokenQuota,
			AllowedModels:   bt.AllowedModels,
			WorkloadClass:   bt.WorkloadClass,
			CostTier:        bt.CostTier,
		}
		if err := s.Upsert(t); err != nil {
			return inserted, err
		}
		inserted++
	}
	return inserted, nil
}
