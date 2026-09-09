package logging

import (
	"database/sql"
	"time"

	_ "modernc.org/sqlite"
)

// Record is a single logged request/response entry.
type Record struct {
	ID               int64
	TS               time.Time
	TenantID         string
	Model            string
	Route            string
	PromptTokens     int
	CompletionTokens int
	StatusCode       int
	LatencyMS        int64
	RequestJSON      string
	ResponseJSON     string
}

// Store is a SQLite-backed, bounded-retention log of gateway requests.
// Callers MUST redact request/response payloads (see Redact) BEFORE
// constructing a Record — the store persists whatever it is given.
type Store struct {
	db      *sql.DB
	maxRows int
}

const defaultMaxRows = 10000

// Open opens (creating if necessary) the SQLite database at path — use
// ":memory:" for an in-memory database in tests — and ensures the requests
// table exists. maxRows bounds retention: on every insert, rows beyond the
// most recent maxRows are swept (deleted, oldest first). A maxRows <= 0
// falls back to defaultMaxRows.
func Open(path string, maxRows int) (*Store, error) {
	if maxRows <= 0 {
		maxRows = defaultMaxRows
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// modernc.org/sqlite is not safe for concurrent writers across multiple
	// connections; keep a single connection, matching internal/tenants.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS requests (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			ts INTEGER NOT NULL,
			tenant_id TEXT NOT NULL,
			model TEXT NOT NULL,
			route TEXT NOT NULL,
			prompt_tokens INTEGER NOT NULL,
			completion_tokens INTEGER NOT NULL,
			status_code INTEGER NOT NULL,
			latency_ms INTEGER NOT NULL,
			request_json TEXT NOT NULL,
			response_json TEXT NOT NULL
		)
	`); err != nil {
		db.Close()
		return nil, err
	}

	return &Store{db: db, maxRows: maxRows}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Insert persists a Record and then sweeps retention, deleting the oldest
// rows beyond the configured maxRows. request_json/response_json on rec
// MUST already be redacted by the caller — Insert never redacts.
func (s *Store) Insert(rec Record) error {
	_, err := s.db.Exec(`
		INSERT INTO requests (
			ts, tenant_id, model, route, prompt_tokens, completion_tokens,
			status_code, latency_ms, request_json, response_json
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		rec.TS.UnixNano(), rec.TenantID, rec.Model, rec.Route,
		rec.PromptTokens, rec.CompletionTokens, rec.StatusCode, rec.LatencyMS,
		rec.RequestJSON, rec.ResponseJSON,
	)
	if err != nil {
		return err
	}

	return s.sweepRetention()
}

// sweepRetention deletes the oldest rows beyond s.maxRows, keeping the most
// recent s.maxRows rows by id.
func (s *Store) sweepRetention() error {
	_, err := s.db.Exec(`
		DELETE FROM requests
		WHERE id NOT IN (
			SELECT id FROM requests ORDER BY id DESC LIMIT ?
		)
	`, s.maxRows)
	return err
}

// QueryRecent returns up to limit most-recently-inserted records, newest
// first.
func (s *Store) QueryRecent(limit int) ([]Record, error) {
	rows, err := s.db.Query(`
		SELECT id, ts, tenant_id, model, route, prompt_tokens, completion_tokens,
		       status_code, latency_ms, request_json, response_json
		FROM requests
		ORDER BY id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Record
	for rows.Next() {
		var rec Record
		var tsNano int64
		if err := rows.Scan(
			&rec.ID, &tsNano, &rec.TenantID, &rec.Model, &rec.Route,
			&rec.PromptTokens, &rec.CompletionTokens, &rec.StatusCode, &rec.LatencyMS,
			&rec.RequestJSON, &rec.ResponseJSON,
		); err != nil {
			return nil, err
		}
		rec.TS = time.Unix(0, tsNano).UTC()
		out = append(out, rec)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
