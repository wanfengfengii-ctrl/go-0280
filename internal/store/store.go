// Package store provides the embedded relational persistence boundary shared by
// every business component: a SQLite-backed store with explicit migrations,
// transaction-scoped repositories for every entity, an append-only audit event
// sink and deterministic startup recovery driven purely by persisted logical
// time rather than wall-clock races.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	_ "modernc.org/sqlite"

	"silage/internal/domain"
)

// Store is the concrete SQLite-backed persistence implementation. It owns the
// connection pool and the injected logical clock used for lease expiry decisions
// during recovery.
type Store struct {
	db    *sql.DB
	clock domain.LogicalClock
}

// Open opens (or creates) the SQLite database at path, applies the explicit
// migration set, and returns a ready Store. The path ":memory:" opens a private
// in-memory database useful for deterministic tests.
func Open(path string, clock domain.LogicalClock) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// A single writer keeps the embedded database deterministic and avoids
	// SQLITE_BUSY under concurrent commands; transactions still serialize writes.
	db.SetMaxOpenConns(1)

	s := &Store{db: db, clock: clock}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// Clock returns the store's injected logical clock.
func (s *Store) Clock() domain.LogicalClock { return s.clock }

// Begin starts a new transaction. All commands execute atomically through it.
func (s *Store) Begin(ctx context.Context) (*Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: begin: %w", err)
	}
	return &Tx{tx: tx}, nil
}

// Tx is a transaction-scoped repository exposing every entity operation. A
// rejected command must roll back, leaving no partial lease, coverage, seal
// flow, expansion range, evidence closure or final credential behind.
type Tx struct {
	tx *sql.Tx
}

// Commit persists the transaction.
func (t *Tx) Commit() error { return t.tx.Commit() }

// Rollback discards the transaction.
func (t *Tx) Rollback() error { return t.tx.Rollback() }

func encode(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		// Domain structs are always JSON-serializable.
		panic("store: encode: " + err.Error())
	}
	return string(b)
}

func decode(s string, v any) error { return json.Unmarshal([]byte(s), v) }
