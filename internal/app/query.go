package app

import (
	"context"

	"silage/internal/arbitration"
	"silage/internal/evidence"
	"silage/internal/sampling"
	"silage/internal/store"
	"silage/internal/task"
)

// TaskSnapshot is the complete, read-only view of a task rendered by the
// frontend workbench: the state trajectory inputs, the sampling grid, leases,
// seals, instrument retries, expansion set, ventilation windows and reviews.
type TaskSnapshot struct {
	Task       task.InspectionTask             `json:"task"`
	Cells      []sampling.SamplingCell         `json:"cells"`
	Leases     []sampling.ResourceLease        `json:"leases"`
	Readings   []evidence.EvidenceReading      `json:"readings"`
	Calls      []evidence.InstrumentCall       `json:"calls"`
	Expansions []arbitration.ExpansionPlan     `json:"expansions"`
	Windows    []arbitration.VentilationWindow `json:"windows"`
	Reviews    []arbitration.ReviewOpinion     `json:"reviews"`
	Credential *arbitration.FinalCredential    `json:"credential,omitempty"`
}

// Snapshot assembles the full read view of a task.
func (s *Service) Snapshot(ctx context.Context, taskID string) (TaskSnapshot, error) {
	var out TaskSnapshot
	tk, err := s.GetTask(ctx, taskID)
	if err != nil {
		return out, err
	}
	out.Task = tk
	err = s.runInTx(ctx, func(tx *store.Tx) error {
		var err error
		if out.Cells, err = tx.ListCells(ctx, taskID); err != nil {
			return err
		}
		if out.Leases, err = tx.ListLeases(ctx, taskID); err != nil {
			return err
		}
		if out.Readings, err = tx.ListReadings(ctx, taskID); err != nil {
			return err
		}
		if out.Calls, err = tx.ListCalls(ctx, taskID); err != nil {
			return err
		}
		if out.Expansions, err = tx.ListExpansions(ctx, taskID); err != nil {
			return err
		}
		if out.Windows, err = tx.ListWindows(ctx, taskID); err != nil {
			return err
		}
		if out.Reviews, err = tx.ListReviews(ctx, taskID); err != nil {
			return err
		}
		if cred, ok, err := tx.Credential(ctx, taskID); err != nil {
			return err
		} else if ok {
			out.Credential = &cred
		}
		return nil
	})
	return out, err
}

// Cells returns a task's sampling cells.
func (s *Service) Cells(ctx context.Context, taskID string) ([]sampling.SamplingCell, error) {
	var out []sampling.SamplingCell
	err := s.runInTx(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.ListCells(ctx, taskID)
		return err
	})
	return out, err
}

// Readings returns a task's evidence readings.
func (s *Service) Readings(ctx context.Context, taskID string) ([]evidence.EvidenceReading, error) {
	var out []evidence.EvidenceReading
	err := s.runInTx(ctx, func(tx *store.Tx) error {
		var err error
		out, err = tx.ListReadings(ctx, taskID)
		return err
	})
	return out, err
}
