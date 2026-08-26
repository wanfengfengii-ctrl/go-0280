package store

import (
	"context"
	"fmt"
)

// migrations is the ordered, explicit schema migration set applied at startup.
// Each entry is a versioned statement list; versions are applied in order and
// recorded so a running database is never partially migrated.
var migrations = []struct {
	version    int
	statements []string
}{
	{
		version: 1,
		statements: []string{
			`CREATE TABLE IF NOT EXISTS catalog_snapshots (
				id TEXT PRIMARY KEY,
				version INTEGER NOT NULL,
				digest TEXT NOT NULL,
				data TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS tasks (
				id TEXT PRIMARY KEY,
				silo_id TEXT NOT NULL,
				snapshot_id TEXT NOT NULL,
				status TEXT NOT NULL,
				generation INTEGER NOT NULL,
				data TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS sampling_cells (
				task_id TEXT NOT NULL,
				zone TEXT NOT NULL,
				layer INTEGER NOT NULL,
				depth INTEGER NOT NULL,
				data TEXT NOT NULL,
				PRIMARY KEY (task_id, zone, layer, depth)
			)`,
			`CREATE TABLE IF NOT EXISTS leases (
				resource_type TEXT NOT NULL,
				resource_id TEXT NOT NULL,
				task_id TEXT NOT NULL,
				data TEXT NOT NULL,
				PRIMARY KEY (resource_type, resource_id)
			)`,
			`CREATE TABLE IF NOT EXISTS film_seals (
				task_id TEXT NOT NULL,
				seal_id TEXT NOT NULL,
				content_digest TEXT NOT NULL,
				PRIMARY KEY (task_id, seal_id)
			)`,
			`CREATE TABLE IF NOT EXISTS seals (
				id TEXT PRIMARY KEY,
				data TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS seal_transfers (
				seal_id TEXT NOT NULL,
				seq INTEGER NOT NULL,
				data TEXT NOT NULL,
				PRIMARY KEY (seal_id, seq)
			)`,
			`CREATE TABLE IF NOT EXISTS instrument_calls (
				id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				hole_id TEXT NOT NULL,
				status TEXT NOT NULL,
				data TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS evidence_readings (
				id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				hole_id TEXT NOT NULL,
				metric TEXT NOT NULL,
				generation INTEGER NOT NULL,
				seq INTEGER NOT NULL,
				data TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS expansion_plans (
				task_id TEXT NOT NULL,
				generation INTEGER NOT NULL,
				data TEXT NOT NULL,
				PRIMARY KEY (task_id, generation)
			)`,
			`CREATE TABLE IF NOT EXISTS ventilation_windows (
				id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL,
				data TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS reviews (
				task_id TEXT NOT NULL,
				reviewer_id TEXT NOT NULL,
				data TEXT NOT NULL,
				PRIMARY KEY (task_id, reviewer_id)
			)`,
			`CREATE TABLE IF NOT EXISTS idempotency_records (
				operation_id TEXT PRIMARY KEY,
				request_digest TEXT NOT NULL,
				response_digest TEXT NOT NULL,
				status TEXT NOT NULL,
				error_code TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS audit_events (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				task_id TEXT NOT NULL,
				seq INTEGER NOT NULL,
				data TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS final_credentials (
				id TEXT PRIMARY KEY,
				task_id TEXT NOT NULL UNIQUE,
				data TEXT NOT NULL
			)`,
			`CREATE TABLE IF NOT EXISTS meta (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL
			)`,
		},
	},
}

// migrate applies any pending migrations and records the applied schema version.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	var current int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_version`).Scan(&current)
	if err != nil {
		return fmt.Errorf("store: migrate: read version: %w", err)
	}
	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, stmt := range m.statements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("store: migrate v%d: %w", m.version, err)
			}
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, m.version); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: migrate record v%d: %w", m.version, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
