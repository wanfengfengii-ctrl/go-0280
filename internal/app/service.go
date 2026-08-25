// Package app is the application service that orchestrates the full silage
// inspection flow across the catalog, task, sampling, evidence and arbitration
// packages. It owns the persistence transaction boundary, idempotency replay,
// generation control and the injected logical clock and instrument adapter.
package app

import (
	"context"
	"fmt"

	"silage/internal/catalog"
	"silage/internal/domain"
	"silage/internal/evidence"
	"silage/internal/store"
	"silage/internal/task"
)

// Service is the single orchestration entry point used by the HTTP API.
type Service struct {
	store   *store.Store
	clock   domain.LogicalClock
	adapter evidence.InstrumentAdapter
	// reg is the plot registry used to verify plot/harvest-batch links at lock
	// time. It is optional; a nil registry skips the relationship check.
	reg catalog.Registry
}

// NewService builds a Service over a store with an injected clock and adapter.
func NewService(st *store.Store, clock domain.LogicalClock, adapter evidence.InstrumentAdapter, reg catalog.Registry) *Service {
	return &Service{store: st, clock: clock, adapter: adapter, reg: reg}
}

// Store exposes the underlying store for callers that need direct access.
func (s *Service) Store() *store.Store { return s.store }

// Clock exposes the injected logical clock.
func (s *Service) Clock() domain.LogicalClock { return s.clock }

// runInTx executes fn inside a transaction, committing on success and rolling
// back on any error.
func (s *Service) runInTx(ctx context.Context, fn func(tx *store.Tx) error) error {
	tx, err := s.store.Begin(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// idempotencyResult is the stable reply recorded for a completed command.
type idempotencyResult struct {
	Status    string
	ErrorCode domain.StableCode
}

// checkIdempotent applies the idempotency rule for a write command: the same
// operation id with the same normalized request returns the prior result; the
// same operation id with different content returns a stable IDEMPOTENCY_CONFLICT.
// It returns (result, found, conflictErr).
func (s *Service) checkIdempotent(ctx context.Context, tx *store.Tx, op domain.OperationID, requestDigest string) (idempotencyResult, bool, error) {
	rec, ok, err := tx.Idempotency(ctx, op)
	if err != nil {
		return idempotencyResult{}, false, err
	}
	if !ok {
		return idempotencyResult{}, false, nil
	}
	if rec.RequestDigest != requestDigest {
		return idempotencyResult{}, false, &domain.Error{
			Code:    domain.CodeIdempotencyConflict,
			Message: "operation id reused with different content",
			Reasons: []domain.Reason{{Constraint: "operation_id=" + string(op)}},
		}
	}
	return idempotencyResult{Status: string(rec.Status), ErrorCode: rec.ErrorCode}, true, nil
}

// recordIdempotent persists the outcome of a completed command for stable replay.
func (s *Service) recordIdempotent(ctx context.Context, tx *store.Tx, op domain.OperationID, requestDigest string, res idempotencyResult) error {
	return tx.SaveIdempotency(ctx, task.IdempotencyRecord{
		OperationID:    op,
		RequestDigest:  requestDigest,
		ResponseDigest: domain.CanonicalDigest(res),
		Status:         task.Status(res.Status),
		ErrorCode:      res.ErrorCode,
	})
}

// newID derives a deterministic identifier from a prefix and a digest input.
func newID(prefix string, parts ...any) string {
	return prefix + domain.CanonicalDigest(fmt.Sprint(parts...))[:16]
}
