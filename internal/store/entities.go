package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"silage/internal/arbitration"
	"silage/internal/catalog"
	"silage/internal/domain"
	"silage/internal/evidence"
	"silage/internal/sampling"
	"silage/internal/task"
)

// --- Catalog snapshots ---

// SaveCatalogSnapshot persists an immutable, versioned snapshot.
func (t *Tx) SaveCatalogSnapshot(ctx context.Context, s catalog.CatalogSnapshot) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO catalog_snapshots (id, version, digest, data) VALUES (?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET version=excluded.version, digest=excluded.digest, data=excluded.data`,
		s.ID, s.Version, catalog.RuleDigest(s), encode(s))
	return err
}

// CatalogSnapshot loads a snapshot by id.
func (t *Tx) CatalogSnapshot(ctx context.Context, id string) (catalog.CatalogSnapshot, error) {
	var data string
	err := t.tx.QueryRowContext(ctx, `SELECT data FROM catalog_snapshots WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return catalog.CatalogSnapshot{}, fmt.Errorf("catalog snapshot %q not found", id)
	}
	if err != nil {
		return catalog.CatalogSnapshot{}, err
	}
	var s catalog.CatalogSnapshot
	if err := decode(data, &s); err != nil {
		return catalog.CatalogSnapshot{}, err
	}
	return s, nil
}

// ListCatalogSnapshots returns every snapshot ordered by id.
func (t *Tx) ListCatalogSnapshots(ctx context.Context) ([]catalog.CatalogSnapshot, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT data FROM catalog_snapshots ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []catalog.CatalogSnapshot
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var s catalog.CatalogSnapshot
		if err := decode(data, &s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// --- Tasks ---

// SaveTask persists an inspection task aggregate.
func (t *Tx) SaveTask(ctx context.Context, tk task.InspectionTask) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO tasks (id, silo_id, snapshot_id, status, generation, data) VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET silo_id=excluded.silo_id, snapshot_id=excluded.snapshot_id,
		 status=excluded.status, generation=excluded.generation, data=excluded.data`,
		tk.ID, tk.SiloID, tk.SnapshotID, string(tk.Status), int64(tk.Generation), encode(tk))
	return err
}

// Task loads an inspection task by id.
func (t *Tx) Task(ctx context.Context, id string) (task.InspectionTask, error) {
	var data string
	err := t.tx.QueryRowContext(ctx, `SELECT data FROM tasks WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return task.InspectionTask{}, fmt.Errorf("task %q not found", id)
	}
	if err != nil {
		return task.InspectionTask{}, err
	}
	var tk task.InspectionTask
	if err := decode(data, &tk); err != nil {
		return task.InspectionTask{}, err
	}
	return tk, nil
}

// ListTasks returns every task ordered by id.
func (t *Tx) ListTasks(ctx context.Context) ([]task.InspectionTask, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT data FROM tasks ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []task.InspectionTask
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var tk task.InspectionTask
		if err := decode(data, &tk); err != nil {
			return nil, err
		}
		out = append(out, tk)
	}
	return out, rows.Err()
}

// --- Sampling cells ---

// SaveCell persists one sampling cell, keyed by task and coordinate. The task
// id is passed explicitly because SamplingCell is a value type reused across
// task generations.
func (t *Tx) SaveCell(ctx context.Context, taskID string, c sampling.SamplingCell) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO sampling_cells (task_id, zone, layer, depth, data) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(task_id, zone, layer, depth) DO UPDATE SET data=excluded.data`,
		taskID, c.Coordinate.Zone, c.Coordinate.Layer, c.Coordinate.Depth, encode(c))
	return err
}

// ListCells returns a task's sampling cells ordered by zone, layer, depth.
func (t *Tx) ListCells(ctx context.Context, taskID string) ([]sampling.SamplingCell, error) {
	rows, err := t.tx.QueryContext(ctx,
		`SELECT data FROM sampling_cells WHERE task_id = ? ORDER BY zone, layer, depth`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sampling.SamplingCell
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var c sampling.SamplingCell
		if err := decode(data, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// --- Leases ---

// SaveLease persists or replaces a resource lease. The primary key on
// (resource_type, resource_id) enforces that a live resource belongs to exactly
// one task at a time.
func (t *Tx) SaveLease(ctx context.Context, l sampling.ResourceLease) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO leases (resource_type, resource_id, task_id, data) VALUES (?, ?, ?, ?)
		 ON CONFLICT(resource_type, resource_id) DO UPDATE SET task_id=excluded.task_id, data=excluded.data`,
		string(l.ResourceType), l.ResourceID, l.TaskID, encode(l))
	return err
}

// DeleteLease removes a lease by resource type and id.
func (t *Tx) DeleteLease(ctx context.Context, resource sampling.ResourceType, id string) error {
	_, err := t.tx.ExecContext(ctx,
		`DELETE FROM leases WHERE resource_type = ? AND resource_id = ?`,
		string(resource), id)
	return err
}

// ListAllLeases returns every lease in the database, used by recovery.
func (t *Tx) ListAllLeases(ctx context.Context) ([]sampling.ResourceLease, error) {
	rows, err := t.tx.QueryContext(ctx, `SELECT data FROM leases ORDER BY resource_type, resource_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sampling.ResourceLease
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var l sampling.ResourceLease
		if err := decode(data, &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ActiveLease returns the current lease for a resource, if one exists.
func (t *Tx) ActiveLease(ctx context.Context, resource sampling.ResourceType, id string) (sampling.ResourceLease, bool, error) {
	var data string
	err := t.tx.QueryRowContext(ctx,
		`SELECT data FROM leases WHERE resource_type = ? AND resource_id = ?`,
		string(resource), id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return sampling.ResourceLease{}, false, nil
	}
	if err != nil {
		return sampling.ResourceLease{}, false, err
	}
	var l sampling.ResourceLease
	if err := decode(data, &l); err != nil {
		return sampling.ResourceLease{}, false, err
	}
	return l, true, nil
}

// ListLeases returns a task's leases ordered by resource type and id.
func (t *Tx) ListLeases(ctx context.Context, taskID string) ([]sampling.ResourceLease, error) {
	rows, err := t.tx.QueryContext(ctx,
		`SELECT data FROM leases WHERE task_id = ? ORDER BY resource_type, resource_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []sampling.ResourceLease
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var l sampling.ResourceLease
		if err := decode(data, &l); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// --- Seals and transfers ---

// SaveSeal persists a sample seal.
func (t *Tx) SaveSeal(ctx context.Context, s evidence.SampleSeal) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO seals (id, data) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET data=excluded.data`,
		s.ID, encode(s))
	return err
}

// Seal loads a sample seal by id.
func (t *Tx) Seal(ctx context.Context, id string) (evidence.SampleSeal, error) {
	var data string
	err := t.tx.QueryRowContext(ctx, `SELECT data FROM seals WHERE id = ?`, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return evidence.SampleSeal{}, fmt.Errorf("seal %q not found", id)
	}
	if err != nil {
		return evidence.SampleSeal{}, err
	}
	var s evidence.SampleSeal
	if err := decode(data, &s); err != nil {
		return evidence.SampleSeal{}, err
	}
	return s, nil
}

// AppendTransfer appends one custody-chain link, keyed by seal and sequence.
func (t *Tx) AppendTransfer(ctx context.Context, tr evidence.SealTransfer) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO seal_transfers (seal_id, seq, data) VALUES (?, ?, ?)`,
		tr.SealID, tr.Seq, encode(tr))
	return err
}

// ListTransfers returns a seal's custody chain ordered by sequence.
func (t *Tx) ListTransfers(ctx context.Context, sealID string) ([]evidence.SealTransfer, error) {
	rows, err := t.tx.QueryContext(ctx,
		`SELECT data FROM seal_transfers WHERE seal_id = ? ORDER BY seq`, sealID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evidence.SealTransfer
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var tr evidence.SealTransfer
		if err := decode(data, &tr); err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// --- Instrument calls and readings ---

// SaveCall persists an instrument call.
func (t *Tx) SaveCall(ctx context.Context, c evidence.InstrumentCall) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO instrument_calls (id, task_id, hole_id, status, data) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET status=excluded.status, data=excluded.data`,
		c.ID, c.TaskID, c.HoleID, string(c.Status), encode(c))
	return err
}

// ListCalls returns a task's instrument calls ordered by id.
func (t *Tx) ListCalls(ctx context.Context, taskID string) ([]evidence.InstrumentCall, error) {
	rows, err := t.tx.QueryContext(ctx,
		`SELECT data FROM instrument_calls WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evidence.InstrumentCall
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var c evidence.InstrumentCall
		if err := decode(data, &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SaveReading persists an evidence reading.
func (t *Tx) SaveReading(ctx context.Context, r evidence.EvidenceReading) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO evidence_readings (id, task_id, hole_id, metric, generation, seq, data)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.CallID, r.TaskID, r.HoleID, r.Metric, int64(r.Generation), r.Seq, encode(r))
	return err
}

// ListReadings returns a task's readings ordered by generation then sequence.
func (t *Tx) ListReadings(ctx context.Context, taskID string) ([]evidence.EvidenceReading, error) {
	rows, err := t.tx.QueryContext(ctx,
		`SELECT data FROM evidence_readings WHERE task_id = ? ORDER BY generation, seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []evidence.EvidenceReading
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var r evidence.EvidenceReading
		if err := decode(data, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- Expansions ---

// SaveExpansion persists an expansion plan, keyed by task and generation.
func (t *Tx) SaveExpansion(ctx context.Context, p arbitration.ExpansionPlan) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO expansion_plans (task_id, generation, data) VALUES (?, ?, ?)
		 ON CONFLICT(task_id, generation) DO UPDATE SET data=excluded.data`,
		p.TaskID, int64(p.Generation), encode(p))
	return err
}

// ListExpansions returns a task's expansion plans ordered by generation.
func (t *Tx) ListExpansions(ctx context.Context, taskID string) ([]arbitration.ExpansionPlan, error) {
	rows, err := t.tx.QueryContext(ctx,
		`SELECT data FROM expansion_plans WHERE task_id = ? ORDER BY generation`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []arbitration.ExpansionPlan
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var p arbitration.ExpansionPlan
		if err := decode(data, &p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// --- Ventilation windows ---

// SaveWindow persists a ventilation window.
func (t *Tx) SaveWindow(ctx context.Context, w arbitration.VentilationWindow) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO ventilation_windows (id, task_id, data) VALUES (?, ?, ?)`,
		w.ID, w.TaskID, encode(w))
	return err
}

// ListWindows returns every ventilation window for a task.
func (t *Tx) ListWindows(ctx context.Context, taskID string) ([]arbitration.VentilationWindow, error) {
	rows, err := t.tx.QueryContext(ctx,
		`SELECT data FROM ventilation_windows WHERE task_id = ? ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []arbitration.VentilationWindow
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var w arbitration.VentilationWindow
		if err := decode(data, &w); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// --- Reviews ---

// SaveReview persists one reviewer opinion, keyed by task and reviewer.
func (t *Tx) SaveReview(ctx context.Context, taskID string, r arbitration.ReviewOpinion) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO reviews (task_id, reviewer_id, data) VALUES (?, ?, ?)
		 ON CONFLICT(task_id, reviewer_id) DO UPDATE SET data=excluded.data`,
		taskID, r.ReviewerID, encode(r))
	return err
}

// ListReviews returns a task's reviewer opinions.
func (t *Tx) ListReviews(ctx context.Context, taskID string) ([]arbitration.ReviewOpinion, error) {
	rows, err := t.tx.QueryContext(ctx,
		`SELECT data FROM reviews WHERE task_id = ? ORDER BY reviewer_id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []arbitration.ReviewOpinion
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var r arbitration.ReviewOpinion
		if err := decode(data, &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- Final credentials ---

// SaveCredential persists a final credential. The UNIQUE(task_id) constraint is
// the single-writer barrier: a second credential for the same task fails.
func (t *Tx) SaveCredential(ctx context.Context, c arbitration.FinalCredential) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO final_credentials (id, task_id, data) VALUES (?, ?, ?)`,
		c.ID, c.TaskID, encode(c))
	return err
}

// Credential loads a task's final credential, if written.
func (t *Tx) Credential(ctx context.Context, taskID string) (arbitration.FinalCredential, bool, error) {
	var data string
	err := t.tx.QueryRowContext(ctx,
		`SELECT data FROM final_credentials WHERE task_id = ?`, taskID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return arbitration.FinalCredential{}, false, nil
	}
	if err != nil {
		return arbitration.FinalCredential{}, false, err
	}
	var c arbitration.FinalCredential
	if err := decode(data, &c); err != nil {
		return arbitration.FinalCredential{}, false, err
	}
	return c, true, nil
}

// --- Idempotency ---

// SaveIdempotency records a completed command for stable replay.
func (t *Tx) SaveIdempotency(ctx context.Context, r task.IdempotencyRecord) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO idempotency_records (operation_id, request_digest, response_digest, status, error_code)
		 VALUES (?, ?, ?, ?, ?)`,
		string(r.OperationID), r.RequestDigest, r.ResponseDigest, string(r.Status), string(r.ErrorCode))
	return err
}

// Idempotency loads a prior command result by operation id.
func (t *Tx) Idempotency(ctx context.Context, op domain.OperationID) (task.IdempotencyRecord, bool, error) {
	var r task.IdempotencyRecord
	var opid, status, code string
	err := t.tx.QueryRowContext(ctx,
		`SELECT operation_id, request_digest, response_digest, status, error_code
		 FROM idempotency_records WHERE operation_id = ?`, string(op)).
		Scan(&opid, &r.RequestDigest, &r.ResponseDigest, &status, &code)
	if errors.Is(err, sql.ErrNoRows) {
		return task.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return task.IdempotencyRecord{}, false, err
	}
	r.OperationID = domain.OperationID(opid)
	r.Status = task.Status(status)
	r.ErrorCode = domain.StableCode(code)
	return r, true, nil
}

// --- Audit events ---

// AppendAudit appends one audit event for a task.
func (t *Tx) AppendAudit(ctx context.Context, taskID string, e task.AuditEvent) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO audit_events (task_id, seq, data) VALUES (?, ?, ?)`,
		taskID, e.Sequence, encode(e))
	return err
}

// ListAudit returns a task's audit events ordered by sequence.
func (t *Tx) ListAudit(ctx context.Context, taskID string) ([]task.AuditEvent, error) {
	rows, err := t.tx.QueryContext(ctx,
		`SELECT data FROM audit_events WHERE task_id = ? ORDER BY seq`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []task.AuditEvent
	for rows.Next() {
		var data string
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var e task.AuditEvent
		if err := decode(data, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// --- Film seals ---

// SaveFilmSeal records a film-seal verification for a task.
func (t *Tx) SaveFilmSeal(ctx context.Context, taskID, sealID, digest string) error {
	_, err := t.tx.ExecContext(ctx,
		`INSERT INTO film_seals (task_id, seal_id, content_digest) VALUES (?, ?, ?)`,
		taskID, sealID, digest)
	return err
}

// FilmSeal loads a task's film-seal digest by seal id.
func (t *Tx) FilmSeal(ctx context.Context, taskID, sealID string) (string, bool, error) {
	var digest string
	err := t.tx.QueryRowContext(ctx,
		`SELECT content_digest FROM film_seals WHERE task_id = ? AND seal_id = ?`,
		taskID, sealID).Scan(&digest)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return digest, true, nil
}
