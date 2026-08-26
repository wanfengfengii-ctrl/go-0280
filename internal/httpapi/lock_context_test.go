package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"silage/internal/app"
	"silage/internal/catalog"
	"silage/internal/domain"
	"silage/internal/store"
	"silage/internal/task"
)

func TestModel_LockRequestContextLifecycle(t *testing.T) {
	tests := []struct {
		name             string
		cancelRequest    bool
		replays          int
		wantStatus       task.Status
		wantGeneration   domain.Generation
		wantCells        int
		wantIdempotency  bool
		wantHTTPStatusOK bool
	}{
		{
			name:            "canceled request rolls back lock",
			cancelRequest:   true,
			replays:         1,
			wantStatus:      task.StatusPendingLock,
			wantGeneration:  0,
			wantCells:       0,
			wantIdempotency: false,
		},
		{
			name:             "live request commits complete grid and replays idempotently",
			replays:          2,
			wantStatus:       task.StatusFilmCheck,
			wantGeneration:   1,
			wantCells:        24,
			wantIdempotency:  true,
			wantHTTPStatusOK: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clock := domain.NewWallClock()
			st, err := store.Open(":memory:", clock)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}
			t.Cleanup(func() { _ = st.Close() })

			svc := app.NewService(st, clock, nil, nil)
			layers := make([]catalog.CompactionLayer, 0, 8)
			for _, zone := range []string{"north", "south"} {
				for layer := 1; layer <= 4; layer++ {
					layers = append(layers, catalog.CompactionLayer{ZoneID: zone, Seq: layer})
				}
			}
			if err := svc.CreateCatalog(context.Background(), catalog.CatalogSnapshot{
				ID:               "catalog-lock-context",
				Version:          1,
				PlotID:           "plot-1",
				HarvestBatchID:   "batch-1",
				Zones:            []catalog.Zone{{ID: "north"}, {ID: "south"}},
				CompactionLayers: layers,
				Thresholds:       catalog.Thresholds{Scale: 1},
				Depths:           []int{10, 20, 30},
			}); err != nil {
				t.Fatalf("create catalog: %v", err)
			}
			tk, err := svc.CreateTask(context.Background(), "silo-1")
			if err != nil {
				t.Fatalf("create task: %v", err)
			}

			srv := NewServer(svc)
			for replay := 0; replay < tt.replays; replay++ {
				req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+tk.ID+"/lock", strings.NewReader(
					`{"operation_id":"op-lock-context","expected_generation":0,"snapshot_id":"catalog-lock-context"}`,
				))
				req.Header.Set("Content-Type", "application/json")
				if tt.cancelRequest {
					ctx, cancel := context.WithCancel(req.Context())
					cancel()
					req = req.WithContext(ctx)
				}
				rec := httptest.NewRecorder()
				srv.Handler().ServeHTTP(rec, req)
				if gotOK := rec.Code == http.StatusOK; gotOK != tt.wantHTTPStatusOK {
					t.Errorf("lock HTTP status = %d, want success=%t; body=%s", rec.Code, tt.wantHTTPStatusOK, rec.Body.String())
				}
			}

			snap, err := svc.Snapshot(context.Background(), tk.ID)
			if err != nil {
				t.Fatalf("query task after lock: %v", err)
			}
			if snap.Task.Status != tt.wantStatus {
				t.Errorf("task status = %q, want %q", snap.Task.Status, tt.wantStatus)
			}
			if snap.Task.Generation != tt.wantGeneration {
				t.Errorf("task generation = %d, want %d", snap.Task.Generation, tt.wantGeneration)
			}
			if len(snap.Cells) != tt.wantCells {
				t.Errorf("sampling cells = %d, want %d", len(snap.Cells), tt.wantCells)
			}

			tx, err := st.Begin(context.Background())
			if err != nil {
				t.Fatalf("begin idempotency query: %v", err)
			}
			t.Cleanup(func() { _ = tx.Rollback() })
			_, found, err := tx.Idempotency(context.Background(), domain.OperationID("op-lock-context"))
			if err != nil {
				t.Fatalf("query idempotency record: %v", err)
			}
			if found != tt.wantIdempotency {
				t.Errorf("idempotency record found = %t, want %t (request canceled=%t)", found, tt.wantIdempotency, tt.cancelRequest)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatalf("close idempotency query: %v", err)
			}
		})
	}
}
