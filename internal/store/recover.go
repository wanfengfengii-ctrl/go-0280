package store

import (
	"context"
	"fmt"

	"silage/internal/sampling"
)

// RecoveryReport summarizes the deterministic outcome of a startup recovery. It
// is derived entirely from persisted records and the injected logical time, so
// two recoveries at the same logical time observe the same result.
type RecoveryReport struct {
	ActiveTasks       int      `json:"active_tasks"`
	Leases            int      `json:"leases"`
	ReleasedLeases    int      `json:"released_leases"`
	PendingRetries    int      `json:"pending_retries"`
	ExpansionPlans    int      `json:"expansion_plans"`
	FinalCredentials  int      `json:"final_credentials"`
	ReleasedResources []string `json:"released_resources,omitempty"`
}

// Recover restores the running state from persisted records. It releases only
// leases that have expired at the supplied logical time AND whose associated
// hole is plugged with no pending retry call; it never releases a lease early
// while a hole is unplugged or a retry is still outstanding. All decisions use
// persisted logical time, never wall-clock races.
func (s *Store) Recover(ctx context.Context, now int64) (RecoveryReport, error) {
	var report RecoveryReport

	tx, err := s.Begin(ctx)
	if err != nil {
		return report, err
	}
	defer tx.Rollback()

	leases, err := tx.ListAllLeases(ctx)
	if err != nil {
		return report, err
	}
	report.Leases = len(leases)

	for _, l := range leases {
		if l.ExpiresAt > now {
			continue
		}
		safe, err := s.leaseReleaseSafe(ctx, tx, l)
		if err != nil {
			return report, err
		}
		if safe {
			if err := tx.DeleteLease(ctx, l.ResourceType, l.ResourceID); err != nil {
				return report, err
			}
			report.ReleasedLeases++
			report.ReleasedResources = append(report.ReleasedResources,
				fmt.Sprintf("%s:%s", l.ResourceType, l.ResourceID))
		}
	}

	if err := s.countReport(ctx, tx, &report); err != nil {
		return report, err
	}

	if err := tx.Commit(); err != nil {
		return report, err
	}
	return report, nil
}

// leaseReleaseSafe decides whether an expired lease may be released. A hole
// lease is only safe when the referenced hole is plugged and no pending retry
// call targets it; a drill or zone lease without a hole is always safe once its
// time has lapsed.
func (s *Store) leaseReleaseSafe(ctx context.Context, tx *Tx, l sampling.ResourceLease) (bool, error) {
	if l.HoleID == "" {
		return true, nil
	}
	plugged, err := s.holePlugged(ctx, tx, l.TaskID, l.HoleID)
	if err != nil {
		return false, err
	}
	if !plugged {
		return false, nil
	}
	pending, err := s.hasPendingRetryForHole(ctx, tx, l.HoleID)
	if err != nil {
		return false, err
	}
	return !pending, nil
}

// holePlugged reports whether the referenced hole has been plugged.
func (s *Store) holePlugged(ctx context.Context, tx *Tx, taskID, holeID string) (bool, error) {
	rows, err := tx.tx.QueryContext(ctx,
		`SELECT data FROM sampling_cells WHERE task_id = ?`, taskID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return false, err
		}
		var c sampling.SamplingCell
		if err := decode(data, &c); err != nil {
			return false, err
		}
		if c.HoleID == holeID {
			return c.Plugged, nil
		}
	}
	return false, rows.Err()
}

// hasPendingRetryForHole reports whether any pending retry call targets a hole.
func (s *Store) hasPendingRetryForHole(ctx context.Context, tx *Tx, holeID string) (bool, error) {
	var n int
	err := tx.tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM instrument_calls WHERE hole_id = ? AND status IN ('pending','retry')`,
		holeID).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// countReport fills the non-lease counts in the report.
func (s *Store) countReport(ctx context.Context, tx *Tx, report *RecoveryReport) error {
	var n int
	if err := tx.tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM tasks WHERE status NOT IN ('opened','feed_isolated','cancelled')`).Scan(&n); err != nil {
		return err
	}
	report.ActiveTasks = n

	if err := tx.tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM instrument_calls WHERE status IN ('pending','retry')`).Scan(&n); err != nil {
		return err
	}
	report.PendingRetries = n

	if err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM expansion_plans`).Scan(&n); err != nil {
		return err
	}
	report.ExpansionPlans = n

	if err := tx.tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM final_credentials`).Scan(&n); err != nil {
		return err
	}
	report.FinalCredentials = n
	return nil
}
