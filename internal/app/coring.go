package app

import (
	"context"

	"silage/internal/domain"
	"silage/internal/evidence"
	"silage/internal/sampling"
	"silage/internal/store"
	"silage/internal/task"
)

// FilmCheckRequest submits one film-seal verification.
type FilmCheckRequest struct {
	OperationID domain.OperationID
	Generation  domain.Generation
	SealID      string
	Content     string
}

// FilmCheck verifies a film seal. Submitting the same seal twice with identical
// content is idempotent; the same seal with different content is rejected as
// DUPLICATE_FILM_SEAL. The first successful verification moves the task from
// film-check into coring.
func (s *Service) FilmCheck(ctx context.Context, taskID string, req FilmCheckRequest) (task.InspectionTask, error) {
	digest := domain.CanonicalDigest(map[string]any{"seal": req.SealID, "content": req.Content})
	var out task.InspectionTask

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
		if err := agg.CheckGeneration(req.Generation); err != nil {
			return err
		}
		if agg.Status() != task.StatusFilmCheck {
			return &domain.Error{Code: domain.CodeGenerationConflict, Message: "task is not in film-check"}
		}

		prior, exists, err := tx.FilmSeal(ctx, taskID, req.SealID)
		if err != nil {
			return err
		}
		if exists {
			if prior != digest {
				return &domain.Error{
					Code:    domain.CodeDuplicateFilmSeal,
					Message: "film seal already verified with different content",
					Reasons: []domain.Reason{{Constraint: "seal_id=" + req.SealID}},
				}
			}
			out = agg.Task
			return nil
		}

		if err := tx.SaveFilmSeal(ctx, taskID, req.SealID, digest); err != nil {
			return err
		}
		if err := agg.Transition(task.StatusCoring, s.clock.Now()); err != nil {
			return err
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

// StartCoringRequest carries the resources leased to open the first hole.
type StartCoringRequest struct {
	OperationID domain.OperationID
	Generation  domain.Generation
	DrillID     string
	ZoneID      string
}

// StartCoring atomically acquires the drill, the exclusive zone (if configured)
// and the first hole lease, then establishes the first sampling point. A lease
// conflict or a wrong generation rolls the whole command back, leaving no
// partial lease or coverage.
func (s *Service) StartCoring(ctx context.Context, taskID string, req StartCoringRequest) error {
	digest := domain.CanonicalDigest(req)
	return s.runInTx(ctx, func(tx *store.Tx) error {
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
		if err := agg.CheckGeneration(req.Generation); err != nil {
			return err
		}
		if agg.Status() != task.StatusCoring {
			return &domain.Error{Code: domain.CodeGenerationConflict, Message: "task is not in coring"}
		}

		cells, err := tx.ListCells(ctx, taskID)
		if err != nil {
			return err
		}
		if len(cells) == 0 {
			return &domain.Error{Code: domain.CodeGridGap, Message: "no sampling cells"}
		}

		coord := store.NewLeaseCoordinator(tx, taskID, s.clock)
		// Acquire the drill, the exclusive zone and the first hole in order.
		if _, err := coord.Acquire(sampling.ResourceDrill, req.DrillID, defaultTTL); err != nil {
			return err
		}
		if _, err := coord.Acquire(sampling.ResourceZone, req.ZoneID, defaultTTL); err != nil {
			return err
		}
		first := cells[0]
		if _, err := coord.Acquire(sampling.ResourceHole, first.HoleID, defaultTTL); err != nil {
			return err
		}

		// Establish the first sampling point.
		first.Covered = true
		if err := tx.SaveCell(ctx, taskID, first); err != nil {
			return err
		}
		agg.Task.ExposureFront = first.Coordinate
		if err := tx.SaveTask(ctx, agg.Task); err != nil {
			return err
		}
		return s.recordIdempotent(ctx, tx, req.OperationID, digest, idempotencyResult{Status: string(agg.Task.Status)})
	})
}

// CoreRequest registers the original core mass for a hole.
type CoreRequest struct {
	OperationID domain.OperationID
	Generation  domain.Generation
	HoleID      string
	CoreMass    int64
}

// RegisterCore registers the original core mass of a hole. The hole must already
// be established and must not yet be sealed.
func (s *Service) RegisterCore(ctx context.Context, taskID string, req CoreRequest) error {
	digest := domain.CanonicalDigest(req)
	return s.runInTx(ctx, func(tx *store.Tx) error {
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
		cells, err := tx.ListCells(ctx, taskID)
		if err != nil {
			return err
		}
		cell, err := findCell(cells, req.HoleID)
		if err != nil {
			return err
		}
		if !cell.Covered {
			return &domain.Error{Code: domain.CodeHoleNotPlugged, Message: "hole is not yet established"}
		}
		if cell.CoreMass != 0 || cell.Sealed {
			return &domain.Error{Code: domain.CodeMassNotConserved, Message: "core already registered"}
		}
		if req.CoreMass < 0 {
			return &domain.Error{Code: domain.CodeMassNotConserved, Message: "core mass must be non-negative"}
		}
		cell.CoreMass = req.CoreMass
		if err := tx.SaveCell(ctx, taskID, *cell); err != nil {
			return err
		}
		return s.recordIdempotent(ctx, tx, req.OperationID, digest, idempotencyResult{Status: string(agg.Task.Status)})
	})
}

// SplitRequest carries the three-way split of an original core.
type SplitRequest struct {
	OperationID domain.OperationID
	Generation  domain.Generation
	HoleID      string
	Test        int64
	Retained    int64
	Loss        int64
}

// SplitSeal splits an original core into test, retained and loss partitions,
// enforcing exact mass conservation, then records the sealed samples and the
// append-only custody chain.
func (s *Service) SplitSeal(ctx context.Context, taskID string, req SplitRequest) error {
	digest := domain.CanonicalDigest(req)
	return s.runInTx(ctx, func(tx *store.Tx) error {
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
		cells, err := tx.ListCells(ctx, taskID)
		if err != nil {
			return err
		}
		cell, err := findCell(cells, req.HoleID)
		if err != nil {
			return err
		}
		if cell.CoreMass == 0 {
			return &domain.Error{Code: domain.CodeHoleNotPlugged, Message: "core not yet registered"}
		}
		if cell.Sealed {
			return &domain.Error{Code: domain.CodeMassNotConserved, Message: "core already sealed"}
		}
		split := evidence.Split{Test: req.Test, Retained: req.Retained, Loss: req.Loss}
		if err := evidence.ValidateSplit(cell.CoreMass, split); err != nil {
			return err
		}
		if err := s.persistSeals(ctx, tx, taskID, cell, split); err != nil {
			return err
		}
		cell.Sealed = true
		if err := tx.SaveCell(ctx, taskID, *cell); err != nil {
			return err
		}
		return s.recordIdempotent(ctx, tx, req.OperationID, digest, idempotencyResult{Status: string(agg.Task.Status)})
	})
}

// persistSeals writes the three sealed samples and the custody-chain links.
func (s *Service) persistSeals(ctx context.Context, tx *store.Tx, taskID string, cell *sampling.SamplingCell, split evidence.Split) error {
	now := s.clock.Now()
	parts := []struct {
		typ  evidence.SampleType
		mass int64
		loss string
	}{
		{evidence.SampleTest, split.Test, ""},
		{evidence.SampleRetained, split.Retained, ""},
		{evidence.SampleLoss, split.Loss, "allowed_loss"},
	}
	for i, p := range parts {
		seal := evidence.SampleSeal{
			ID:         newID("seal-", taskID, cell.HoleID, i),
			BlindCode:  cell.BlindCode,
			SampleType: p.typ,
			Mass:       p.mass,
			LossReason: p.loss,
			Holder:     "operator",
		}
		if err := tx.SaveSeal(ctx, seal); err != nil {
			return err
		}
		if err := tx.AppendTransfer(ctx, evidence.SealTransfer{
			SealID:    seal.ID,
			From:      "operator",
			To:        "lab",
			At:        now,
			Operation: domain.OperationID(""),
			Seq:       1,
		}); err != nil {
			return err
		}
	}
	return nil
}

// PlugRequest plugs a hole after its core has been split and sealed.
type PlugRequest struct {
	OperationID domain.OperationID
	Generation  domain.Generation
	HoleID      string
}

// PlugHole plugs a hole and advances the exposure front to the next cell. A hole
// may only be plugged after it has been established, cored and sealed.
func (s *Service) PlugHole(ctx context.Context, taskID string, req PlugRequest) error {
	digest := domain.CanonicalDigest(req)
	return s.runInTx(ctx, func(tx *store.Tx) error {
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
		cells, err := tx.ListCells(ctx, taskID)
		if err != nil {
			return err
		}
		cell, err := findCell(cells, req.HoleID)
		if err != nil {
			return err
		}
		if !cell.Sealed {
			return &domain.Error{Code: domain.CodeHoleNotPlugged, Message: "hole is not yet sealed"}
		}
		cell.Plugged = true
		if err := tx.SaveCell(ctx, taskID, *cell); err != nil {
			return err
		}
		// Advance the exposure front to the next unplugged cell and establish
		// its sampling point, if any. The front may never move past an unplugged
		// hole: the next cell is only established after this one is plugged.
		allPlugged := true
		for i := range cells {
			if cells[i].HoleID == cell.HoleID {
				cells[i].Plugged = true
			}
			if !cells[i].Plugged {
				allPlugged = false
				if !cells[i].Covered {
					cells[i].Covered = true
					if err := tx.SaveCell(ctx, taskID, cells[i]); err != nil {
						return err
					}
				}
				agg.Task.ExposureFront = cells[i].Coordinate
				break
			}
		}
		if allPlugged {
			// All holes are plugged: move into fermentation detection.
			if agg.Status() == task.StatusCoring || agg.Status() == task.StatusSealing {
				if err := agg.Transition(task.StatusFermenting, s.clock.Now()); err != nil {
					return err
				}
			}
		}
		if err := tx.SaveTask(ctx, agg.Task); err != nil {
			return err
		}
		return s.recordIdempotent(ctx, tx, req.OperationID, digest, idempotencyResult{Status: string(agg.Task.Status)})
	})
}

// findCell locates a cell by hole id.
func findCell(cells []sampling.SamplingCell, holeID string) (*sampling.SamplingCell, error) {
	for i := range cells {
		if cells[i].HoleID == holeID {
			return &cells[i], nil
		}
	}
	return nil, &domain.Error{Code: domain.CodeHoleNotPlugged, Message: "hole not found", Reasons: []domain.Reason{{HoleID: holeID, Constraint: "hole_not_found"}}}
}

// defaultTTL is the default lease duration in logical-time units.
const defaultTTL = 1000
