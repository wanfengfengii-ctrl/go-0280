package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"silage/internal/app"
	"silage/internal/domain"
	"silage/internal/httpapi"
	"silage/internal/sampling"
	"silage/internal/store"
	"silage/internal/task"
)

func TestModel_TaskDetailReturnsOneCommittedSnapshot(t *testing.T) {
	tests := []struct {
		name       string
		from       task.Status
		to         task.Status
		generation domain.Generation
	}{
		{
			name:       "lock commit cannot mix pending task with committed grid",
			from:       task.StatusPendingLock,
			to:         task.StatusFilmCheck,
			generation: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			dsn := "file:" + filepath.Join(t.TempDir(), "snapshot.db") + "?_pragma=busy_timeout%3d10000"
			readerStore, err := store.Open(dsn, domain.NewWallClock())
			if err != nil {
				t.Fatalf("open reader store: %v", err)
			}
			t.Cleanup(func() { _ = readerStore.Close() })
			writerStore, err := store.Open(dsn, domain.NewWallClock())
			if err != nil {
				t.Fatalf("open writer store: %v", err)
			}
			t.Cleanup(func() { _ = writerStore.Close() })

			// Keeping the aggregate large makes its decode hold the read transaction
			// while the independently opened writer stages the lock result.
			before := task.InspectionTask{
				ID:        "task-snapshot",
				SiloID:    strings.Repeat("s", 16<<20),
				Status:    tt.from,
				CreatedAt: 1,
				UpdatedAt: 1,
				Version:   1,
			}
			seed, err := readerStore.Begin(ctx)
			if err != nil {
				t.Fatalf("begin seed transaction: %v", err)
			}
			if err := seed.SaveTask(ctx, before); err != nil {
				_ = seed.Rollback()
				t.Fatalf("seed task: %v", err)
			}
			if err := seed.Commit(); err != nil {
				t.Fatalf("commit seed task: %v", err)
			}

			after := before
			after.Status = tt.to
			after.SnapshotID = "catalog-1"
			after.Generation = tt.generation
			after.UpdatedAt = 2
			after.Version++
			cell := sampling.SamplingCell{
				Coordinate:   domain.Coordinate{Zone: "A", Layer: 1, Depth: 0},
				HarvestBatch: "batch-1",
				BlindCode:    "blind-1",
				HoleID:       "hole-1",
				Generation:   tt.generation,
			}

			srv := httpapi.NewServer(app.NewService(readerStore, domain.NewWallClock(), nil, nil))
			entered := make(chan struct{})
			response := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				rec := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+before.ID, nil)
				close(entered)
				srv.Handler().ServeHTTP(rec, req)
				response <- rec
			}()
			<-entered

			writeTx, err := writerStore.Begin(ctx)
			if err != nil {
				t.Fatalf("begin lock transaction: %v", err)
			}
			if err := writeTx.SaveCell(ctx, before.ID, cell); err != nil {
				_ = writeTx.Rollback()
				t.Fatalf("stage grid: %v", err)
			}
			if err := writeTx.SaveTask(ctx, after); err != nil {
				_ = writeTx.Rollback()
				t.Fatalf("stage task transition: %v", err)
			}
			if err := writeTx.Commit(); err != nil {
				t.Fatalf("commit lock transaction: %v", err)
			}

			rec := <-response
			if rec.Code != http.StatusOK {
				t.Fatalf("GET status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var got struct {
				Task  task.InspectionTask     `json:"task"`
				Cells []sampling.SamplingCell `json:"cells"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode GET response: %v", err)
			}

			beforeSnapshot := got.Task.Status == tt.from && got.Task.SnapshotID == "" &&
				got.Task.Generation == 0 && len(got.Cells) == 0
			afterSnapshot := got.Task.Status == tt.to && got.Task.SnapshotID == after.SnapshotID &&
				got.Task.Generation == tt.generation && len(got.Cells) == 1 && got.Cells[0] == cell
			if !beforeSnapshot && !afterSnapshot {
				t.Fatalf("mixed committed states: status=%q snapshot_id=%q generation=%d cells=%d",
					got.Task.Status, got.Task.SnapshotID, got.Task.Generation, len(got.Cells))
			}
		})
	}
}
