package app

import (
	"context"

	"silage/internal/catalog"
	"silage/internal/domain"
	"silage/internal/sampling"
	"silage/internal/store"
	"silage/internal/task"
)

// CreateCatalog validates and persists a versioned catalog snapshot.
func (s *Service) CreateCatalog(ctx context.Context, snap catalog.CatalogSnapshot) error {
	if snap.ID == "" {
		return &domain.Error{Code: domain.CodeStaleRuleDigest, Message: "catalog id is required"}
	}
	if err := catalog.ValidateLinks(snap, s.reg); err != nil {
		return err
	}
	return s.runInTx(ctx, func(tx *store.Tx) error {
		if snap.Version == 0 {
			snap.Version = 1
		}
		return tx.SaveCatalogSnapshot(ctx, snap)
	})
}

// ListCatalogs returns all catalog snapshots.
func (s *Service) ListCatalogs(ctx context.Context) ([]catalog.CatalogSnapshot, error) {
	var out []catalog.CatalogSnapshot
	err := s.runInTx(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.ListCatalogSnapshots(ctx)
		return err
	})
	return out, err
}

// CreateTask creates a new inspection task in the pending-lock state.
func (s *Service) CreateTask(ctx context.Context, siloID string) (task.InspectionTask, error) {
	id := newID("task-", siloID, s.clock.Now())
	tk := task.InspectionTask{
		ID:        id,
		SiloID:    siloID,
		Status:    task.StatusPendingLock,
		CreatedAt: s.clock.Now(),
		UpdatedAt: s.clock.Now(),
		Version:   1,
	}
	err := s.runInTx(ctx, func(tx *store.Tx) error {
		return tx.SaveTask(ctx, tk)
	})
	return tk, err
}

// LockTaskRequest carries the inputs to freeze a task against a catalog snapshot.
type LockTaskRequest struct {
	OperationID domain.OperationID
	Generation  domain.Generation
	SnapshotID  string
}

// LockTask freezes the catalog snapshot, plot/harvest-batch link, chop-length
// rule, inoculant summary, zones, layers, film seal, opening face, three-
// dimensional sampling grid, blind codes, hole ids, ventilation devices, all
// thresholds and the initial generation in one transaction. Any stale rule
// digest, plot/batch mismatch, grid gap, grid overlap or duplicate coordinate
// rejects the whole lock, leaving the task pending and writing no partial grid.
func (s *Service) LockTask(ctx context.Context, taskID string, req LockTaskRequest) (task.InspectionTask, error) {
	digest := domain.CanonicalDigest(req)
	var out task.InspectionTask

	err := s.runInTx(ctx, func(tx *store.Tx) error {
		// Idempotency check first so replays return the frozen result.
		if res, ok, err := s.checkIdempotent(ctx, tx, req.OperationID, digest); err != nil {
			return err
		} else if ok {
			if tk, err := tx.Task(ctx, taskID); err == nil {
				out = tk
			}
			_ = res
			return nil
		}

		tk, err := tx.Task(ctx, taskID)
		if err != nil {
			return err
		}
		agg := task.NewAggregate(tk, nil)
		if agg.Status() != task.StatusPendingLock {
			return &domain.Error{Code: domain.CodeGenerationConflict, Message: "task is not pending lock"}
		}

		snap, err := tx.CatalogSnapshot(ctx, req.SnapshotID)
		if err != nil {
			return err
		}
		if err := catalog.ValidateLinks(snap, s.reg); err != nil {
			return err
		}

		// Build and validate the three-dimensional grid.
		plan := snap.SamplingPlan()
		grid := sampling.GridSpec{Zones: plan.Zones, Layers: plan.Layers, Depths: plan.Depths}
		coords := grid.Generate()
		if len(coords) == 0 {
			return &domain.Error{Code: domain.CodeGridGap, Message: "sampling grid is empty"}
		}
		if err := grid.Validate(coords); err != nil {
			return err
		}

		// Freeze the snapshot and grid.
		agg.Task.SnapshotID = snap.ID
		agg.Task.Generation = 1
		if err := agg.Transition(task.StatusFilmCheck, s.clock.Now()); err != nil {
			return err
		}

		for i, c := range coords {
			cell := sampling.SamplingCell{
				Coordinate:   c,
				HarvestBatch: snap.HarvestBatchID,
				BlindCode:    newID("bc-", taskID, c.Zone, c.Layer, c.Depth),
				HoleID:       newID("hole-", taskID, c.Zone, c.Layer, c.Depth),
				Order:        i,
				Generation:   1,
			}
			if err := tx.SaveCell(ctx, taskID, cell); err != nil {
				return err
			}
		}

		if err := tx.SaveTask(ctx, agg.Task); err != nil {
			return err
		}
		if err := s.recordIdempotent(ctx, tx, req.OperationID, digest, idempotencyResult{Status: string(agg.Task.Status)}); err != nil {
			return err
		}
		out = agg.Task
		return nil
	})
	return out, err
}

// GetTask loads a task by id.
func (s *Service) GetTask(ctx context.Context, taskID string) (task.InspectionTask, error) {
	var out task.InspectionTask
	err := s.runInTx(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.Task(ctx, taskID)
		return err
	})
	return out, err
}

// ListTasks returns all tasks.
func (s *Service) ListTasks(ctx context.Context) ([]task.InspectionTask, error) {
	var out []task.InspectionTask
	err := s.runInTx(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.ListTasks(ctx)
		return err
	})
	return out, err
}
