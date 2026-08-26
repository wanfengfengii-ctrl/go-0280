package app

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"

	"silage/internal/arbitration"
	"silage/internal/catalog"
	"silage/internal/domain"
	"silage/internal/store"
)

func TestModel_LockedTaskRetainsImmutableCatalogSnapshot(t *testing.T) {
	tests := []struct {
		name               string
		conflictingVersion int64
	}{
		{name: "same version content cannot replace locked snapshot", conflictingVersion: 1},
		{name: "later version content cannot replace locked snapshot", conflictingVersion: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			dbPath := filepath.Join(t.TempDir(), "silage.db")
			clock := domain.NewWallClock()
			firstStore, err := store.Open(dbPath, clock)
			if err != nil {
				t.Fatalf("open initial store: %v", err)
			}
			t.Cleanup(func() { _ = firstStore.Close() })
			svc := NewService(firstStore, clock, nil, nil)

			lockedCatalog := catalog.CatalogSnapshot{
				ID:             "c1",
				Version:        1,
				PlotID:         "p1",
				HarvestBatchID: "b1",
				Zones:          []catalog.Zone{{ID: "A"}},
				CompactionLayers: []catalog.CompactionLayer{
					{ZoneID: "A", Seq: 1},
					{ZoneID: "A", Seq: 2},
				},
				Depths:    []int{0, 1},
				Adjacency: map[string][]string{"A:1": {"A:2"}, "A:2": {"A:1"}},
				OpenFace:  catalog.OpenFace{ID: "locked-face", Ventilator: "locked-fan"},
				Thresholds: catalog.Thresholds{
					OxygenMin: 180, HydrogenSulfMax: 5, Scale: 1, MaxInterval: 10,
				},
			}
			if err := svc.CreateCatalog(ctx, lockedCatalog); err != nil {
				t.Fatalf("create initial catalog: %v", err)
			}
			uniqueCatalog := lockedCatalog
			uniqueCatalog.ID = "c-unique"
			if err := svc.CreateCatalog(ctx, uniqueCatalog); err != nil {
				t.Fatalf("create catalog with unique id: %v", err)
			}

			tk, err := svc.CreateTask(ctx, "s1")
			if err != nil {
				t.Fatalf("create task: %v", err)
			}
			if _, err := svc.LockTask(ctx, tk.ID, LockTaskRequest{
				OperationID: "op-lock", Generation: 0, SnapshotID: lockedCatalog.ID,
			}); err != nil {
				t.Fatalf("lock task: %v", err)
			}

			conflict := lockedCatalog
			conflict.Version = tt.conflictingVersion
			conflict.Adjacency = map[string][]string{"A:1": {"A:1"}, "A:2": {"A:2"}}
			conflict.OpenFace = catalog.OpenFace{ID: "replacement-face", Ventilator: "replacement-fan"}
			conflict.Thresholds.OxygenMin = 250
			conflict.Thresholds.HydrogenSulfMax = 1
			conflict.Thresholds.MaxInterval = 1
			writeErr := svc.CreateCatalog(ctx, conflict)

			catalogs, err := svc.ListCatalogs(ctx)
			if err != nil {
				t.Fatalf("list catalogs: %v", err)
			}
			lockedDigest := catalog.RuleDigest(lockedCatalog)
			conflictDigest := catalog.RuleDigest(conflict)
			var keptLocked, keptConflict, keptUnique bool
			for _, got := range catalogs {
				digest := catalog.RuleDigest(got)
				keptLocked = keptLocked || got.ID == lockedCatalog.ID && digest == lockedDigest
				keptConflict = keptConflict || digest == conflictDigest
				keptUnique = keptUnique || got.ID == uniqueCatalog.ID
			}
			if !keptLocked {
				t.Fatal("conflicting catalog publication replaced the immutable snapshot referenced by the locked task")
			}
			if writeErr == nil && !keptConflict {
				t.Fatal("accepted conflicting catalog publication was not retained as a distinct immutable version")
			}
			if !keptUnique {
				t.Fatal("catalog with a unique id disappeared after conflicting publication")
			}

			if _, err := svc.FilmCheck(ctx, tk.ID, FilmCheckRequest{
				OperationID: "op-film", Generation: 1, SealID: "seal-1", Content: "intact",
			}); err != nil {
				t.Fatalf("film check: %v", err)
			}
			if err := svc.StartCoring(ctx, tk.ID, StartCoringRequest{
				OperationID: "op-start", Generation: 1, DrillID: "drill-1", ZoneID: "A",
			}); err != nil {
				t.Fatalf("start coring: %v", err)
			}
			cells, err := svc.Cells(ctx, tk.ID)
			if err != nil {
				t.Fatalf("list locked cells: %v", err)
			}
			if len(cells) != 4 {
				t.Fatalf("locked grid has %d cells, want 4", len(cells))
			}
			for i, cell := range cells {
				if err := svc.RegisterCore(ctx, tk.ID, CoreRequest{
					OperationID: domain.OperationID(fmt.Sprintf("op-core-%d", i)), Generation: 1,
					HoleID: cell.HoleID, CoreMass: 100,
				}); err != nil {
					t.Fatalf("register core %d: %v", i, err)
				}
				if err := svc.SplitSeal(ctx, tk.ID, SplitRequest{
					OperationID: domain.OperationID(fmt.Sprintf("op-split-%d", i)), Generation: 1,
					HoleID: cell.HoleID, Test: 60, Retained: 30, Loss: 10,
				}); err != nil {
					t.Fatalf("split core %d: %v", i, err)
				}
				if err := svc.PlugHole(ctx, tk.ID, PlugRequest{
					OperationID: domain.OperationID(fmt.Sprintf("op-plug-%d", i)), Generation: 1,
					HoleID: cell.HoleID,
				}); err != nil {
					t.Fatalf("plug hole %d: %v", i, err)
				}
			}

			if err := firstStore.Close(); err != nil {
				t.Fatalf("close before restart: %v", err)
			}
			secondStore, err := store.Open(dbPath, clock)
			if err != nil {
				t.Fatalf("reopen store: %v", err)
			}
			t.Cleanup(func() { _ = secondStore.Close() })
			svc = NewService(secondStore, clock, nil, nil)

			plan, err := svc.Expand(ctx, tk.ID, ExpandRequest{
				OperationID: "op-expand", Generation: 1,
				Anomalies: []domain.Coordinate{{Zone: "A", Layer: 1, Depth: 99}},
			})
			if err != nil {
				t.Fatalf("expand after restart: %v", err)
			}
			wantExpansion := []domain.Coordinate{
				{Zone: "A", Layer: 2, Depth: 0},
				{Zone: "A", Layer: 2, Depth: 1},
			}
			if !reflect.DeepEqual(plan.Coordinates, wantExpansion) {
				t.Fatalf("expansion coordinates = %#v, want locked adjacency result %#v", plan.Coordinates, wantExpansion)
			}

			window, err := svc.Ventilate(ctx, tk.ID, VentilateRequest{
				OperationID: "op-ventilate", Generation: 2,
				Readings: []arbitration.GasReading{
					{At: 100, Oxygen: 190, H2S: 3},
					{At: 105, Oxygen: 190, H2S: 3},
				},
			})
			if err != nil {
				t.Fatalf("ventilate with locked thresholds after restart: %v", err)
			}
			if window.OpenFace != lockedCatalog.OpenFace.ID || window.Device != lockedCatalog.OpenFace.Ventilator {
				t.Fatalf("ventilation used face/device %q/%q, want locked %q/%q",
					window.OpenFace, window.Device, lockedCatalog.OpenFace.ID, lockedCatalog.OpenFace.Ventilator)
			}
		})
	}
}
