package app

import (
	"context"
	"testing"

	"silage/internal/arbitration"
	"silage/internal/catalog"
	"silage/internal/domain"
	"silage/internal/store"
	"silage/internal/task"
)

// manualClock is a deterministic logical clock for tests.
type manualClock struct{ t int64 }

func (c *manualClock) Now() int64 { c.t++; return c.t }

// testRegistry is a map-backed plot registry.
type testRegistry map[string]string

func (r testRegistry) Plot(id string) (catalog.Plot, bool) {
	b, ok := r[id]
	return catalog.Plot{ID: id, HarvestBatchID: b}, ok
}

func newTestService(t *testing.T) (*Service, *manualClock) {
	t.Helper()
	st, err := store.Open(":memory:", &manualClock{})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clock := &manualClock{}
	svc := NewService(st, clock, nil, testRegistry{"p1": "b1"})
	return svc, clock
}

func sampleCatalog() catalog.CatalogSnapshot {
	return catalog.CatalogSnapshot{
		ID:             "c1",
		Version:        1,
		PlotID:         "p1",
		HarvestBatchID: "b1",
		ChopLengthRule: catalog.ChopLengthRule{ID: "cl", Digest: "d", MinMM: 5, MaxMM: 20},
		Zones:          []catalog.Zone{{ID: "A"}},
		CompactionLayers: []catalog.CompactionLayer{
			{ZoneID: "A", Seq: 1},
			{ZoneID: "A", Seq: 2},
		},
		Adjacency: map[string][]string{"A:1": {"A:2"}, "A:2": {"A:1"}},
		OpenFace:  catalog.OpenFace{ID: "f1", Ventilator: "v1"},
		Thresholds: catalog.Thresholds{
			OxygenMin: 180, HydrogenSulfMax: 5, Scale: 1,
			ButyricAcidMax: 10, AmmoniaNMax: 10, MycotoxinMax: 10, TempRiseMax: 10,
			MaxRetries: 3, MaxInterval: 10,
		},
		Depths: []int{0, 1},
	}
}

func lockTask(t *testing.T, svc *Service, taskID string) {
	t.Helper()
	ctx := context.Background()
	_, err := svc.LockTask(ctx, taskID, LockTaskRequest{
		OperationID: "op-lock", Generation: 0, SnapshotID: "c1",
	})
	if err != nil {
		t.Fatalf("lock: %v", err)
	}
}

func TestFullLifecycle(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)

	if err := svc.CreateCatalog(ctx, sampleCatalog()); err != nil {
		t.Fatalf("create catalog: %v", err)
	}
	tk, err := svc.CreateTask(ctx, "s1")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	lockTask(t, svc, tk.ID)

	if _, err := svc.FilmCheck(ctx, tk.ID, FilmCheckRequest{
		OperationID: "op-film", Generation: 1, SealID: "seal-1", Content: "abc",
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
		t.Fatalf("cells: %v", err)
	}
	if len(cells) != 4 {
		t.Fatalf("got %d cells, want 4", len(cells))
	}

	// Core, split and plug every hole in order.
	for i, c := range cells {
		if err := svc.RegisterCore(ctx, tk.ID, CoreRequest{
			OperationID: domain.OperationID("op-core-" + c.HoleID), Generation: 1, HoleID: c.HoleID, CoreMass: 100,
		}); err != nil {
			t.Fatalf("register core %d: %v", i, err)
		}
		if err := svc.SplitSeal(ctx, tk.ID, SplitRequest{
			OperationID: domain.OperationID("op-split-" + c.HoleID), Generation: 1,
			HoleID: c.HoleID, Test: 60, Retained: 30, Loss: 10,
		}); err != nil {
			t.Fatalf("split seal %d: %v", i, err)
		}
		if err := svc.PlugHole(ctx, tk.ID, PlugRequest{
			OperationID: domain.OperationID("op-plug-" + c.HoleID), Generation: 1, HoleID: c.HoleID,
		}); err != nil {
			t.Fatalf("plug hole %d: %v", i, err)
		}
	}

	got, _ := svc.GetTask(ctx, tk.ID)
	if got.Status != task.StatusFermenting {
		t.Fatalf("status = %s, want fermenting", got.Status)
	}

	// Expand on an anomaly.
	plan, err := svc.Expand(ctx, tk.ID, ExpandRequest{
		OperationID: "op-expand", Generation: 1,
		Anomalies: []domain.Coordinate{{Zone: "A", Layer: 1, Depth: 0}},
	})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if plan.Generation != 2 {
		t.Fatalf("expansion generation = %d, want 2", plan.Generation)
	}

	// Ventilate.
	if _, err := svc.Ventilate(ctx, tk.ID, VentilateRequest{
		OperationID: "op-vent", Generation: 2,
		Readings: []arbitration.GasReading{
			{At: 100, Oxygen: 200, H2S: 2},
			{At: 105, Oxygen: 195, H2S: 3},
		},
	}); err != nil {
		t.Fatalf("ventilate: %v", err)
	}

	// Two reviews.
	if _, err := svc.Review(ctx, tk.ID, ReviewRequest{
		OperationID: "op-review-1", Generation: 2, ReviewerID: "r1", Qualified: true, Opinion: "open",
	}); err != nil {
		t.Fatalf("review 1: %v", err)
	}
	if _, err := svc.Review(ctx, tk.ID, ReviewRequest{
		OperationID: "op-review-2", Generation: 2, ReviewerID: "r2", Qualified: true, Opinion: "open",
	}); err != nil {
		t.Fatalf("review 2: %v", err)
	}

	got, _ = svc.GetTask(ctx, tk.ID)
	if got.Status != task.StatusOpenable {
		t.Fatalf("status = %s, want openable", got.Status)
	}

	// Finalize.
	cred, err := svc.Finalize(ctx, tk.ID, FinalizeRequest{
		OperationID: "op-final", Generation: 2, Command: "open", Winner: "r1",
	})
	if err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if cred.Kind != arbitration.FinalOpen {
		t.Fatalf("kind = %s, want open", cred.Kind)
	}
	got, _ = svc.GetTask(ctx, tk.ID)
	if !got.Status.IsFinal() {
		t.Fatalf("status = %s, want final", got.Status)
	}

	// A write after finalization must be rejected.
	if _, err := svc.SubmitReading(ctx, tk.ID, ReadingRequest{
		OperationID: "op-after", Generation: 2, HoleID: cells[0].HoleID, Metric: "ph", Value: 7, Scale: 1,
	}); err == nil {
		t.Fatal("expected write after finalization to be rejected")
	}
}

func TestMassConservationRejected(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	if err := svc.CreateCatalog(ctx, sampleCatalog()); err != nil {
		t.Fatalf("catalog: %v", err)
	}
	tk, _ := svc.CreateTask(ctx, "s1")
	lockTask(t, svc, tk.ID)
	_, _ = svc.FilmCheck(ctx, tk.ID, FilmCheckRequest{OperationID: "op-film", Generation: 1, SealID: "s", Content: "x"})
	_ = svc.StartCoring(ctx, tk.ID, StartCoringRequest{OperationID: "op-start", Generation: 1, DrillID: "d", ZoneID: "A"})
	cells, _ := svc.Cells(ctx, tk.ID)
	hole := cells[0].HoleID
	_ = svc.RegisterCore(ctx, tk.ID, CoreRequest{OperationID: "op-core", Generation: 1, HoleID: hole, CoreMass: 100})

	err := svc.SplitSeal(ctx, tk.ID, SplitRequest{
		OperationID: "op-split", Generation: 1, HoleID: hole, Test: 60, Retained: 30, Loss: 5,
	})
	if err == nil {
		t.Fatal("expected mass conservation error")
	}
	if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeMassNotConserved {
		t.Fatalf("got %v, want MASS_NOT_CONSERVED", err)
	}
}

func TestIdempotentLockReplay(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestService(t)
	if err := svc.CreateCatalog(ctx, sampleCatalog()); err != nil {
		t.Fatalf("catalog: %v", err)
	}
	tk, _ := svc.CreateTask(ctx, "s1")

	req := LockTaskRequest{OperationID: "op-lock", Generation: 0, SnapshotID: "c1"}
	if _, err := svc.LockTask(ctx, tk.ID, req); err != nil {
		t.Fatalf("lock: %v", err)
	}
	// Replay the same lock: must succeed idempotently.
	if _, err := svc.LockTask(ctx, tk.ID, req); err != nil {
		t.Fatalf("lock replay: %v", err)
	}
	// Same operation id with different content: conflict.
	if _, err := svc.LockTask(ctx, tk.ID, LockTaskRequest{OperationID: "op-lock", Generation: 1, SnapshotID: "c1"}); err == nil {
		t.Fatal("expected idempotency conflict")
	}
}
