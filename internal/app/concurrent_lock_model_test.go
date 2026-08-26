package app_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"silage/internal/app"
	"silage/internal/catalog"
	"silage/internal/domain"
	"silage/internal/store"
	"silage/internal/task"
)

type lockStepRegistry struct {
	armed   atomic.Bool
	arrived atomic.Int32
	ready   chan struct{}
	once    sync.Once
}

func (r *lockStepRegistry) Plot(id string) (catalog.Plot, bool) {
	if r.armed.Load() {
		if r.arrived.Add(1) == 2 {
			r.once.Do(func() { close(r.ready) })
		}
		select {
		case <-r.ready:
		case <-time.After(250 * time.Millisecond):
		}
	}
	return catalog.Plot{ID: id, HarvestBatchID: "batch-1"}, true
}

func TestModel_ConcurrentLockCommands(t *testing.T) {
	tests := []struct {
		name       string
		zoneCount  int
		layerCount int
		depthCount int
	}{
		{name: "unrelated large grids commit without SQLite write-lock failures", zoneCount: 5, layerCount: 20, depthCount: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			clock := domain.NewWallClock()
			st, err := store.Open(filepath.Join(t.TempDir(), "shared.sqlite"), clock)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })

			registry := &lockStepRegistry{ready: make(chan struct{})}
			svc := app.NewService(st, clock, nil, registry)
			snap := catalog.CatalogSnapshot{
				ID:             "catalog-1",
				Version:        1,
				PlotID:         "plot-1",
				HarvestBatchID: "batch-1",
				ChopLengthRule: catalog.ChopLengthRule{ID: "chop-1", Digest: "digest-1", MinMM: 5, MaxMM: 20},
				OpenFace:       catalog.OpenFace{ID: "face-1", Ventilator: "fan-1"},
				Thresholds:     catalog.Thresholds{Scale: 1},
			}
			for z := 0; z < tt.zoneCount; z++ {
				zoneID := fmt.Sprintf("zone-%02d", z)
				snap.Zones = append(snap.Zones, catalog.Zone{ID: zoneID})
				for layer := 1; layer <= tt.layerCount; layer++ {
					snap.CompactionLayers = append(snap.CompactionLayers, catalog.CompactionLayer{ZoneID: zoneID, Seq: layer})
				}
			}
			for depth := 0; depth < tt.depthCount; depth++ {
				snap.Depths = append(snap.Depths, depth)
			}
			if err := svc.CreateCatalog(ctx, snap); err != nil {
				t.Fatalf("create catalog: %v", err)
			}

			tasks := make([]task.InspectionTask, 2)
			for i := range tasks {
				tasks[i], err = svc.CreateTask(ctx, fmt.Sprintf("silo-%d", i+1))
				if err != nil {
					t.Fatalf("create task %d: %v", i+1, err)
				}
			}

			requests := []app.LockTaskRequest{
				{OperationID: "lock-task-1", Generation: 0, SnapshotID: snap.ID},
				{OperationID: "lock-task-2", Generation: 0, SnapshotID: snap.ID},
			}
			registry.armed.Store(true)
			start := make(chan struct{})
			errs := make([]error, len(tasks))
			var wg sync.WaitGroup
			wg.Add(len(tasks))
			for i := range tasks {
				go func(i int) {
					defer wg.Done()
					<-start
					_, errs[i] = svc.LockTask(ctx, tasks[i].ID, requests[i])
				}(i)
			}
			close(start)
			wg.Wait()

			wantCells := tt.zoneCount * tt.layerCount * tt.depthCount
			for i, commandErr := range errs {
				if commandErr != nil {
					t.Errorf("concurrent lock for task %d failed: %v", i+1, commandErr)
					continue
				}
				view, err := svc.Snapshot(ctx, tasks[i].ID)
				if err != nil {
					t.Errorf("snapshot task %d: %v", i+1, err)
					continue
				}
				if view.Task.Status != task.StatusFilmCheck || view.Task.Generation != 1 || view.Task.SnapshotID != snap.ID {
					t.Errorf("task %d not atomically locked: status=%q generation=%d snapshot=%q", i+1, view.Task.Status, view.Task.Generation, view.Task.SnapshotID)
				}
				if len(view.Cells) != wantCells {
					t.Errorf("task %d grid has %d cells, want %d", i+1, len(view.Cells), wantCells)
				}
				if _, err := svc.LockTask(ctx, tasks[i].ID, requests[i]); err != nil {
					t.Errorf("task %d idempotent replay failed: %v", i+1, err)
				}
			}
		})
	}
}
