package app

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"silage/internal/arbitration"
	"silage/internal/domain"
)

func TestModel_VentilationPreservesReportedReadingOrder(t *testing.T) {
	tests := []struct {
		name       string
		readings   []arbitration.GasReading
		constraint string
	}{
		{
			name: "valid out-of-order readings retain caller order in response and query",
			readings: []arbitration.GasReading{
				{At: 110, Oxygen: 190, H2S: 4},
				{At: 100, Oxygen: 200, H2S: 2},
				{At: 105, Oxygen: 195, H2S: 3},
			},
		},
		{name: "empty window is rejected", constraint: "empty_window"},
		{
			name: "chronological gap above maximum is rejected",
			readings: []arbitration.GasReading{
				{At: 120, Oxygen: 200, H2S: 2},
				{At: 100, Oxygen: 200, H2S: 2},
			},
			constraint: "interval_too_large",
		},
		{
			name: "oxygen below minimum is rejected",
			readings: []arbitration.GasReading{
				{At: 105, Oxygen: 179, H2S: 2},
				{At: 100, Oxygen: 200, H2S: 2},
			},
			constraint: "oxygen_below_min",
		},
		{
			name: "hydrogen sulfide above maximum is rejected",
			readings: []arbitration.GasReading{
				{At: 105, Oxygen: 200, H2S: 6},
				{At: 100, Oxygen: 200, H2S: 2},
			},
			constraint: "hydrogen_above_max",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			svc, _ := newTestService(t)
			if err := svc.CreateCatalog(ctx, sampleCatalog()); err != nil {
				t.Fatalf("create catalog: %v", err)
			}
			tk, err := svc.CreateTask(ctx, "silo-ventilation-order")
			if err != nil {
				t.Fatalf("create task: %v", err)
			}
			lockTask(t, svc, tk.ID)
			if _, err := svc.FilmCheck(ctx, tk.ID, FilmCheckRequest{
				OperationID: "order-film", Generation: 1, SealID: "seal", Content: "intact",
			}); err != nil {
				t.Fatalf("film check: %v", err)
			}
			if err := svc.StartCoring(ctx, tk.ID, StartCoringRequest{
				OperationID: "order-start", Generation: 1, DrillID: "drill", ZoneID: "A",
			}); err != nil {
				t.Fatalf("start coring: %v", err)
			}
			cells, err := svc.Cells(ctx, tk.ID)
			if err != nil {
				t.Fatalf("list cells: %v", err)
			}
			for _, cell := range cells {
				if err := svc.RegisterCore(ctx, tk.ID, CoreRequest{
					OperationID: domain.OperationID("order-core-" + cell.HoleID), Generation: 1,
					HoleID: cell.HoleID, CoreMass: 100,
				}); err != nil {
					t.Fatalf("register core %s: %v", cell.HoleID, err)
				}
				if err := svc.SplitSeal(ctx, tk.ID, SplitRequest{
					OperationID: domain.OperationID("order-split-" + cell.HoleID), Generation: 1,
					HoleID: cell.HoleID, Test: 60, Retained: 30, Loss: 10,
				}); err != nil {
					t.Fatalf("split core %s: %v", cell.HoleID, err)
				}
				if err := svc.PlugHole(ctx, tk.ID, PlugRequest{
					OperationID: domain.OperationID("order-plug-" + cell.HoleID), Generation: 1,
					HoleID: cell.HoleID,
				}); err != nil {
					t.Fatalf("plug hole %s: %v", cell.HoleID, err)
				}
			}

			original := append([]arbitration.GasReading(nil), tt.readings...)
			window, err := svc.Ventilate(ctx, tk.ID, VentilateRequest{
				OperationID: "order-ventilate", Generation: 1, Readings: tt.readings,
			})
			if tt.constraint != "" {
				if err == nil {
					t.Fatalf("Ventilate() succeeded, want rejection for %s", tt.constraint)
				}
				var domainErr *domain.Error
				if !errors.As(err, &domainErr) {
					t.Fatalf("Ventilate() error = %T %v, want *domain.Error", err, err)
				}
				for _, reason := range domainErr.Reasons {
					if reason.Constraint == tt.constraint {
						return
					}
				}
				t.Fatalf("Ventilate() reasons = %#v, want constraint %q", domainErr.Reasons, tt.constraint)
			}

			if err != nil {
				t.Fatalf("Ventilate() valid out-of-order readings: %v", err)
			}
			if !reflect.DeepEqual(tt.readings, original) {
				t.Fatalf("caller readings mutated: got %#v, want %#v", tt.readings, original)
			}
			if !reflect.DeepEqual(window.Readings, original) {
				t.Fatalf("response readings reordered: got %#v, want %#v", window.Readings, original)
			}
			snapshot, err := svc.Snapshot(ctx, tk.ID)
			if err != nil {
				t.Fatalf("query snapshot: %v", err)
			}
			if len(snapshot.Windows) != 1 {
				t.Fatalf("persisted windows = %d, want 1", len(snapshot.Windows))
			}
			if !reflect.DeepEqual(snapshot.Windows[0].Readings, original) {
				t.Fatalf("persisted readings reordered: got %#v, want %#v", snapshot.Windows[0].Readings, original)
			}
		})
	}
}
