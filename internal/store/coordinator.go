package store

import (
	"context"
	"fmt"

	"silage/internal/domain"
	"silage/internal/sampling"
	"silage/internal/task"
)

// LeaseCoordinator is a transaction-bound implementation of the lease port. It
// is constructed per command so every acquisition, renewal and release shares
// the same transaction as the surrounding business write, guaranteeing atomic
// lease-and-first-point creation and leaving no partial lease on failure.
type LeaseCoordinator struct {
	tx     *Tx
	clock  domain.LogicalClock
	taskID string
	seq    int64
}

// NewLeaseCoordinator binds a coordinator to a transaction, task and clock.
func NewLeaseCoordinator(tx *Tx, taskID string, clock domain.LogicalClock) *LeaseCoordinator {
	return &LeaseCoordinator{tx: tx, clock: clock, taskID: taskID}
}

// Acquire attempts an exclusive lease. A still-live lease held by any task is a
// conflict; the database unique key on (resource_type, resource_id) is the final
// guard against two tasks racing to the same resource.
func (c *LeaseCoordinator) Acquire(resource sampling.ResourceType, id string, ttl int64) (sampling.ResourceLease, error) {
	now := c.clock.Now()
	existing, ok, err := c.tx.ActiveLease(context.Background(), resource, id)
	if err != nil {
		return sampling.ResourceLease{}, err
	}
	if ok && existing.ExpiresAt > now {
		return sampling.ResourceLease{}, &domain.Error{
			Code:    domain.CodeLeaseConflict,
			Message: fmt.Sprintf("resource %s:%s is already leased", resource, id),
			Reasons: []domain.Reason{{Constraint: fmt.Sprintf("%s:%s", resource, id)}},
		}
	}
	c.seq++
	holeID := ""
	if resource == sampling.ResourceHole {
		holeID = id
	}
	l := sampling.ResourceLease{
		ResourceType: resource,
		ResourceID:   id,
		TaskID:       c.taskID,
		HoleID:       holeID,
		Token:        fmt.Sprintf("%s:%s:%s:%d", c.taskID, resource, id, c.seq),
		AcquiredAt:   now,
		ExpiresAt:    now + ttl,
	}
	if err := c.tx.SaveLease(context.Background(), l); err != nil {
		return sampling.ResourceLease{}, err
	}
	return l, nil
}

// Renew extends a lease and returns the updated lease.
func (c *LeaseCoordinator) Renew(resource sampling.ResourceType, id string, ttl int64) (sampling.ResourceLease, error) {
	now := c.clock.Now()
	existing, ok, err := c.tx.ActiveLease(context.Background(), resource, id)
	if err != nil {
		return sampling.ResourceLease{}, err
	}
	if !ok {
		return sampling.ResourceLease{}, &domain.Error{
			Code:    domain.CodeLeaseConflict,
			Message: "lease not held",
			Reasons: []domain.Reason{{Constraint: fmt.Sprintf("%s:%s", resource, id)}},
		}
	}
	existing.ExpiresAt = now + ttl
	existing.Renewals++
	if err := c.tx.SaveLease(context.Background(), existing); err != nil {
		return sampling.ResourceLease{}, err
	}
	return existing, nil
}

// Release returns a lease, recording the reason.
func (c *LeaseCoordinator) Release(resource sampling.ResourceType, id string, reason string) error {
	return c.tx.DeleteLease(context.Background(), resource, id)
}

// EventSink is the transaction-bound, append-only audit stream. Rejection facts
// are recorded as independent audit events so they survive even when the
// surrounding command is rejected.
type EventSink struct {
	tx     *Tx
	taskID string
}

// NewEventSink binds an event sink to a transaction and task.
func NewEventSink(tx *Tx, taskID string) *EventSink {
	return &EventSink{tx: tx, taskID: taskID}
}

// Emit appends one audit event.
func (e *EventSink) Emit(ctx context.Context, ev task.AuditEvent) error {
	return e.tx.AppendAudit(ctx, e.taskID, ev)
}
