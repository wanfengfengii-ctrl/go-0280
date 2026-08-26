package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"silage/internal/app"
	"silage/internal/catalog"
	"silage/internal/domain"
	"silage/internal/store"
	"silage/internal/task"
)

func TestModel_IdempotentLockReplayReturnsPersistedOriginalResponse(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "replay.db")
	clock := domain.NewWallClock()
	st, err := store.Open(dbPath, clock)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	svc := app.NewService(st, clock, nil, nil)
	if err := svc.CreateCatalog(ctx, catalog.CatalogSnapshot{
		ID:             "catalog-1",
		Version:        1,
		PlotID:         "plot-1",
		HarvestBatchID: "batch-1",
		Zones:          []catalog.Zone{{ID: "zone-1"}},
		CompactionLayers: []catalog.CompactionLayer{
			{ZoneID: "zone-1", Seq: 1},
		},
		Thresholds: catalog.Thresholds{Scale: 1},
		Depths:     []int{0},
	}); err != nil {
		t.Fatalf("create catalog: %v", err)
	}
	tk, err := svc.CreateTask(ctx, "silo-1")
	if err != nil {
		t.Fatalf("create task: %v", err)
	}

	handler := NewServer(svc).Handler()
	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	lockPath := "/api/tasks/" + tk.ID + "/lock"
	lockBody := `{"operation_id":"lock-timeout-1","expected_generation":0,"snapshot_id":"catalog-1"}`
	first := post(lockPath, lockBody)
	if first.Code != http.StatusOK {
		t.Fatalf("first lock status = %d, body = %s", first.Code, first.Body.String())
	}
	var original task.InspectionTask
	if err := json.Unmarshal(first.Body.Bytes(), &original); err != nil {
		t.Fatalf("decode first lock response: %v", err)
	}

	if _, err := svc.FilmCheck(ctx, tk.ID, app.FilmCheckRequest{
		OperationID: "film-1", Generation: 1, SealID: "seal-1", Content: "verified",
	}); err != nil {
		t.Fatalf("complete film check: %v", err)
	}
	if err := svc.StartCoring(ctx, tk.ID, app.StartCoringRequest{
		OperationID: "start-core-1", Generation: 1, DrillID: "drill-1", ZoneID: "zone-1",
	}); err != nil {
		t.Fatalf("start coring: %v", err)
	}
	cells, err := svc.Cells(ctx, tk.ID)
	if err != nil || len(cells) != 1 {
		t.Fatalf("load locked sampling cell: len=%d err=%v", len(cells), err)
	}
	if err := svc.RegisterCore(ctx, tk.ID, app.CoreRequest{
		OperationID: "core-1", Generation: 1, HoleID: cells[0].HoleID, CoreMass: 100,
	}); err != nil {
		t.Fatalf("register core: %v", err)
	}

	cases := []struct {
		name         string
		restart      bool
		body         string
		wantConflict bool
	}{
		{name: "after_later_task_progress", body: lockBody},
		{name: "after_service_restart", restart: true, body: lockBody},
		{name: "different_content_still_conflicts", body: `{"operation_id":"lock-timeout-1","expected_generation":1,"snapshot_id":"catalog-1"}`, wantConflict: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.restart {
				if err := st.Close(); err != nil {
					t.Fatalf("close store for restart: %v", err)
				}
				clock = domain.NewWallClock()
				st, err = store.Open(dbPath, clock)
				if err != nil {
					t.Fatalf("reopen store: %v", err)
				}
				svc = app.NewService(st, clock, nil, nil)
				handler = NewServer(svc).Handler()
			}

			replay := post(lockPath, tc.body)
			if tc.wantConflict {
				if replay.Code != http.StatusConflict {
					t.Fatalf("conflict status = %d, body = %s", replay.Code, replay.Body.String())
				}
				var got ErrorResponse
				if err := json.Unmarshal(replay.Body.Bytes(), &got); err != nil {
					t.Fatalf("decode conflict response: %v", err)
				}
				if got.StableCode != domain.CodeIdempotencyConflict {
					t.Fatalf("stable code = %q, want %q", got.StableCode, domain.CodeIdempotencyConflict)
				}
				return
			}

			if replay.Code != http.StatusOK {
				t.Fatalf("replay status = %d, body = %s", replay.Code, replay.Body.String())
			}
			var got task.InspectionTask
			if err := json.Unmarshal(replay.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode replay response: %v", err)
			}
			if !reflect.DeepEqual(got, original) {
				t.Fatalf("replayed lock response = %+v, want original persisted response %+v", got, original)
			}
		})
	}
}
