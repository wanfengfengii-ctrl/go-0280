package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"silage/internal/app"
	"silage/internal/arbitration"
	"silage/internal/domain"
	"silage/internal/task"
)

func TestModel_ReviewCommandIsAtomic(t *testing.T) {
	type request struct {
		OperationID domain.OperationID `json:"operation_id"`
		Generation  domain.Generation  `json:"expected_generation"`
		ReviewerID  string             `json:"reviewer_id"`
		Qualified   bool               `json:"qualified"`
		Opinion     string             `json:"opinion"`
	}
	type step struct {
		request       request
		wantHTTP      int
		wantCode      domain.StableCode
		wantStatus    task.Status
		wantReviews   int
		wantR1Opinion string
	}
	tests := []struct {
		name           string
		initialStatus  task.Status
		initialReviews []arbitration.ReviewOpinion
		steps          []step
	}{
		{
			name:          "stale review leaves no state and does not count toward quorum",
			initialStatus: task.StatusPendingReview,
			steps: []step{
				{
					request:     request{OperationID: "review-attempt", Generation: 1, ReviewerID: "r1", Qualified: true, Opinion: "stale"},
					wantHTTP:    http.StatusConflict,
					wantCode:    domain.CodeGenerationConflict,
					wantStatus:  task.StatusPendingReview,
					wantReviews: 0,
				},
				{
					request:       request{OperationID: "review-attempt", Generation: 2, ReviewerID: "r1", Qualified: true, Opinion: "approve"},
					wantHTTP:      http.StatusOK,
					wantStatus:    task.StatusPendingReview,
					wantReviews:   1,
					wantR1Opinion: "approve",
				},
				{
					request:       request{OperationID: "review-r1-update", Generation: 2, ReviewerID: "r1", Qualified: true, Opinion: "approve-updated"},
					wantHTTP:      http.StatusOK,
					wantStatus:    task.StatusPendingReview,
					wantReviews:   1,
					wantR1Opinion: "approve-updated",
				},
				{
					request:       request{OperationID: "review-r2", Generation: 2, ReviewerID: "r2", Qualified: true, Opinion: "approve"},
					wantHTTP:      http.StatusOK,
					wantStatus:    task.StatusOpenable,
					wantReviews:   2,
					wantR1Opinion: "approve-updated",
				},
			},
		},
		{
			name:          "terminal rejection cannot overwrite an existing review",
			initialStatus: task.StatusOpened,
			initialReviews: []arbitration.ReviewOpinion{
				{ReviewerID: "r1", Qualified: true, Opinion: "original", At: 10},
			},
			steps: []step{
				{
					request:       request{OperationID: "review-after-final", Generation: 2, ReviewerID: "r1", Qualified: false, Opinion: "overwritten"},
					wantHTTP:      http.StatusConflict,
					wantCode:      domain.CodeFinalAlreadyWritten,
					wantStatus:    task.StatusOpened,
					wantReviews:   1,
					wantR1Opinion: "original",
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)
			ctx := context.Background()
			tx, err := srv.svc.Store().Begin(ctx)
			if err != nil {
				t.Fatalf("begin seed transaction: %v", err)
			}
			seed := task.InspectionTask{
				ID: "review-task", SiloID: "s1", SnapshotID: "catalog-1",
				Status: tc.initialStatus, Generation: 2, CreatedAt: 1, UpdatedAt: 2,
			}
			if err := tx.SaveTask(ctx, seed); err != nil {
				_ = tx.Rollback()
				t.Fatalf("seed task: %v", err)
			}
			for _, review := range tc.initialReviews {
				if err := tx.SaveReview(ctx, seed.ID, review); err != nil {
					_ = tx.Rollback()
					t.Fatalf("seed review: %v", err)
				}
			}
			if err := tx.Commit(); err != nil {
				t.Fatalf("commit seed transaction: %v", err)
			}

			for i, step := range tc.steps {
				body, err := json.Marshal(step.request)
				if err != nil {
					t.Fatalf("step %d marshal request: %v", i, err)
				}
				rec := doJSON(t, srv, http.MethodPost, "/api/tasks/"+seed.ID+"/reviews", string(body))
				if rec.Code != step.wantHTTP {
					t.Fatalf("step %d HTTP status = %d, want %d; body=%s", i, rec.Code, step.wantHTTP, rec.Body.String())
				}
				if step.wantCode != "" {
					var gotErr ErrorResponse
					if err := json.Unmarshal(rec.Body.Bytes(), &gotErr); err != nil {
						t.Fatalf("step %d decode error: %v", i, err)
					}
					if gotErr.StableCode != step.wantCode {
						t.Fatalf("step %d stable_code = %q, want %q", i, gotErr.StableCode, step.wantCode)
					}
				}

				rec = doJSON(t, srv, http.MethodGet, "/api/tasks/"+seed.ID, "")
				if rec.Code != http.StatusOK {
					t.Fatalf("step %d refresh status = %d; body=%s", i, rec.Code, rec.Body.String())
				}
				var snap app.TaskSnapshot
				if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
					t.Fatalf("step %d decode snapshot: %v", i, err)
				}
				if snap.Task.Generation != 2 || snap.Task.Status != step.wantStatus {
					t.Fatalf("step %d task = generation %d status %s, want generation 2 status %s", i, snap.Task.Generation, snap.Task.Status, step.wantStatus)
				}
				if len(snap.Reviews) != step.wantReviews {
					t.Fatalf("step %d reviews = %#v, want count %d", i, snap.Reviews, step.wantReviews)
				}
				if step.wantR1Opinion != "" {
					if len(snap.Reviews) == 0 || snap.Reviews[0].ReviewerID != "r1" || snap.Reviews[0].Opinion != step.wantR1Opinion {
						t.Fatalf("step %d r1 review = %#v, want opinion %q", i, snap.Reviews, step.wantR1Opinion)
					}
				}
			}
		})
	}
}
