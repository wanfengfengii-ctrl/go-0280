package app

import (
	"context"

	"silage/internal/arbitration"
	"silage/internal/domain"
	"silage/internal/store"
	"silage/internal/task"
)

// ExpandRequest carries the anomalous coordinates that trigger an expansion.
type ExpandRequest struct {
	OperationID domain.OperationID
	Generation  domain.Generation
	Anomalies   []domain.Coordinate
}

// Expand computes the deterministic expansion set (the sorted, deduplicated
// union of adjacent-layer and same-harvest-batch coordinates), bumps the task
// generation and freezes the expansion plan in a single transaction. A stale
// generation is rejected so an old expansion cannot overwrite newer evidence.
func (s *Service) Expand(ctx context.Context, taskID string, req ExpandRequest) (arbitration.ExpansionPlan, error) {
	digest := domain.CanonicalDigest(req)
	var out arbitration.ExpansionPlan

	err := s.runInTx(ctx, func(tx *store.Tx) error {
		if res, ok, err := s.checkIdempotent(ctx, tx, req.OperationID, digest); err != nil {
			return err
		} else if ok {
			_ = res
			return nil
		}
		tk, err := tx.Task(ctx, taskID)
		if err != nil {
			return err
		}
		agg := task.NewAggregate(tk, nil)
		if err := agg.GuardWrite(); err != nil {
			return err
		}
		if err := agg.CheckGeneration(req.Generation); err != nil {
			return err
		}

		snap, err := tx.CatalogSnapshot(ctx, tk.SnapshotID)
		if err != nil {
			return err
		}
		cells, err := tx.ListCells(ctx, taskID)
		if err != nil {
			return err
		}
		acells := make([]arbitration.Cell, 0, len(cells))
		for _, c := range cells {
			acells = append(acells, arbitration.Cell{Coordinate: c.Coordinate, HarvestBatch: c.HarvestBatch})
		}
		coords := arbitration.ComputeExpansion(req.Anomalies, acells, snap.Adjacency)

		plan := arbitration.ExpansionPlan{
			TaskID:         taskID,
			Generation:     agg.Generation() + 1,
			SourceEvidence: digest,
			Coordinates:    coords,
			Digest:         domain.CanonicalDigest(coords),
			Closed:         false,
		}
		if err := tx.SaveExpansion(ctx, plan); err != nil {
			return err
		}
		agg.BumpGeneration(s.clock.Now())
		if agg.Status() != task.StatusExpanding {
			_ = agg.Transition(task.StatusExpanding, s.clock.Now())
		}
		if err := tx.SaveTask(ctx, agg.Task); err != nil {
			return err
		}
		if err := s.recordIdempotent(ctx, tx, req.OperationID, digest, idempotencyResult{Status: string(agg.Task.Status)}); err != nil {
			return err
		}
		out = plan
		return nil
	})
	return out, err
}

// VentilateRequest carries an ordered run of gas readings on the opening face.
type VentilateRequest struct {
	OperationID domain.OperationID
	Generation  domain.Generation
	Readings    []arbitration.GasReading
}

// Ventilate validates a continuous ventilation window against the locked
// thresholds. A valid window moves the task into pending review.
func (s *Service) Ventilate(ctx context.Context, taskID string, req VentilateRequest) (arbitration.VentilationWindow, error) {
	digest := domain.CanonicalDigest(req)
	var out arbitration.VentilationWindow

	err := s.runInTx(ctx, func(tx *store.Tx) error {
		if res, ok, err := s.checkIdempotent(ctx, tx, req.OperationID, digest); err != nil {
			return err
		} else if ok {
			_ = res
			return nil
		}
		tk, err := tx.Task(ctx, taskID)
		if err != nil {
			return err
		}
		agg := task.NewAggregate(tk, nil)
		if err := agg.GuardWrite(); err != nil {
			return err
		}
		if err := agg.CheckGeneration(req.Generation); err != nil {
			return err
		}

		snap, err := tx.CatalogSnapshot(ctx, tk.SnapshotID)
		if err != nil {
			return err
		}
		window := arbitration.VentilationWindow{
			ID:         newID("window-", taskID, s.clock.Now()),
			TaskID:     taskID,
			OpenFace:   snap.OpenFace.ID,
			Device:     snap.OpenFace.Ventilator,
			Generation: req.Generation,
			Readings:   req.Readings,
		}
		if len(req.Readings) > 0 {
			window.StartAt = req.Readings[0].At
			window.EndAt = req.Readings[len(req.Readings)-1].At
		}
		rule := arbitration.VentilationRule{
			MaxInterval: snap.Thresholds.MaxInterval,
			OxygenMin:   snap.Thresholds.OxygenMin,
			HydrogenMax: snap.Thresholds.HydrogenSulfMax,
		}
		if err := arbitration.ValidateVentilation(window, rule); err != nil {
			return err
		}
		window.Continuous = true
		if err := tx.SaveWindow(ctx, window); err != nil {
			return err
		}
		if agg.Status() != task.StatusPendingReview {
			if err := agg.Transition(task.StatusVentilating, s.clock.Now()); err != nil {
				return err
			}
			if err := agg.Transition(task.StatusPendingReview, s.clock.Now()); err != nil {
				return err
			}
		}
		if err := tx.SaveTask(ctx, agg.Task); err != nil {
			return err
		}
		if err := s.recordIdempotent(ctx, tx, req.OperationID, digest, idempotencyResult{Status: string(agg.Task.Status)}); err != nil {
			return err
		}
		out = window
		return nil
	})
	return out, err
}

// ReviewRequest submits one qualified reviewer's independent opinion.
type ReviewRequest struct {
	OperationID domain.OperationID
	Generation  domain.Generation
	ReviewerID  string
	Qualified   bool
	Opinion     string
}

// Review records a reviewer opinion. When two distinct, qualified reviewers have
// submitted opinions the task becomes openable.
func (s *Service) Review(ctx context.Context, taskID string, req ReviewRequest) (task.InspectionTask, error) {
	digest := domain.CanonicalDigest(req)
	var out task.InspectionTask
	opinion := arbitration.ReviewOpinion{
		ReviewerID: req.ReviewerID,
		Qualified:  req.Qualified,
		Opinion:    req.Opinion,
		At:         s.clock.Now(),
	}
	if err := s.runInTx(ctx, func(tx *store.Tx) error {
		return tx.SaveReview(ctx, taskID, opinion)
	}); err != nil {
		return out, err
	}

	err := s.runInTx(ctx, func(tx *store.Tx) error {
		if res, ok, err := s.checkIdempotent(ctx, tx, req.OperationID, digest); err != nil {
			return err
		} else if ok {
			tk, _ := tx.Task(ctx, taskID)
			out = tk
			_ = res
			return nil
		}
		tk, err := tx.Task(ctx, taskID)
		if err != nil {
			return err
		}
		agg := task.NewAggregate(tk, nil)
		if err := agg.GuardWrite(); err != nil {
			return err
		}
		if err := agg.CheckGeneration(req.Generation); err != nil {
			return err
		}

		if err := tx.SaveReview(ctx, taskID, opinion); err != nil {
			return err
		}
		opinions, err := tx.ListReviews(ctx, taskID)
		if err != nil {
			return err
		}
		if err := arbitration.ValidateReviews(opinions); err == nil {
			if agg.Status() == task.StatusPendingReview {
				if err := agg.Transition(task.StatusOpenable, s.clock.Now()); err != nil {
					return err
				}
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

// FinalizeRequest carries one of the three competing terminal commands.
type FinalizeRequest struct {
	OperationID domain.OperationID
	Generation  domain.Generation
	Command     string // open | feed_isolate | cancel
	Winner      string
}

// Finalize arbitrates the three competing terminal commands behind a single-
// writer barrier. The database unique constraint on task id guarantees exactly
// one immutable credential wins; a second finalize returns FINAL_ALREADY_WRITTEN.
func (s *Service) Finalize(ctx context.Context, taskID string, req FinalizeRequest) (arbitration.FinalCredential, error) {
	digest := domain.CanonicalDigest(req)
	var out arbitration.FinalCredential

	err := s.runInTx(ctx, func(tx *store.Tx) error {
		if res, ok, err := s.checkIdempotent(ctx, tx, req.OperationID, digest); err != nil {
			return err
		} else if ok {
			if cred, ok2, _ := tx.Credential(ctx, taskID); ok2 {
				out = cred
			}
			_ = res
			return nil
		}
		tk, err := tx.Task(ctx, taskID)
		if err != nil {
			return err
		}
		agg := task.NewAggregate(tk, nil)
		if err := agg.GuardWrite(); err != nil {
			return err
		}
		if err := agg.CheckGeneration(req.Generation); err != nil {
			return err
		}

		opinions, err := tx.ListReviews(ctx, taskID)
		if err != nil {
			return err
		}
		if err := arbitration.ValidateReviews(opinions); err != nil {
			return err
		}
		kind, ok := arbitration.WinnerOf(req.Command)
		if !ok {
			return &domain.Error{Code: domain.CodeGenerationConflict, Message: "unknown terminal command"}
		}

		cred := arbitration.NewCredential(newID("cred-", taskID, req.Command, s.clock.Now()), taskID, kind, req.Winner, s.clock.Now())
		if err := tx.SaveCredential(ctx, cred); err != nil {
			// The unique constraint is the single-writer barrier.
			return &domain.Error{
				Code:    domain.CodeFinalAlreadyWritten,
				Message: "a final credential already exists for this task",
				Reasons: []domain.Reason{{Constraint: "task_id=" + taskID}},
			}
		}

		var terminal task.Status
		switch kind {
		case arbitration.FinalOpen:
			terminal = task.StatusOpened
		case arbitration.FinalFeedIsolate:
			terminal = task.StatusFeedIsolated
		case arbitration.FinalCancel:
			terminal = task.StatusCancelled
		}
		if err := agg.Transition(terminal, s.clock.Now()); err != nil {
			return err
		}
		agg.Task.FinalCredentialID = cred.ID
		if err := tx.SaveTask(ctx, agg.Task); err != nil {
			return err
		}
		if err := s.recordIdempotent(ctx, tx, req.OperationID, digest, idempotencyResult{Status: string(agg.Task.Status)}); err != nil {
			return err
		}
		out = cred
		return nil
	})
	return out, err
}

// GetCredential returns a task's final credential, if written.
func (s *Service) GetCredential(ctx context.Context, taskID string) (arbitration.FinalCredential, bool, error) {
	var out arbitration.FinalCredential
	var ok bool
	err := s.runInTx(ctx, func(tx *store.Tx) error {
		var err error
		out, ok, err = tx.Credential(ctx, taskID)
		return err
	})
	return out, ok, err
}
