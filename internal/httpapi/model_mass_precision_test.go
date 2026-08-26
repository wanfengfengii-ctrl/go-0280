package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"silage/internal/domain"
)

func TestModel_HTTPMassFieldsPreserveInt64Precision(t *testing.T) {
	const largeMass int64 = 9007199254740993
	tests := []struct {
		name     string
		test     int64
		retained int64
		loss     int64
	}{
		{name: "test partition", test: largeMass},
		{name: "retained partition", retained: largeMass},
		{name: "loss partition", loss: largeMass},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t)

			rec := doJSON(t, srv, http.MethodPost, "/api/catalogs", `{"id":"c1","version":1,"plot_id":"p1","harvest_batch_id":"b1","zones":["A"],"layers":{"A":[1]},"depths":[0],"adjacency":{},"open_face":"f1","ventilator":"v1","oxygen_min":180,"h2s_max":5,"scale":1}`)
			if rec.Code != http.StatusCreated {
				t.Fatalf("create catalog status = %d, body=%s", rec.Code, rec.Body.String())
			}

			rec = doJSON(t, srv, http.MethodPost, "/api/tasks", `{"silo_id":"s1"}`)
			if rec.Code != http.StatusCreated {
				t.Fatalf("create task status = %d, body=%s", rec.Code, rec.Body.String())
			}
			var created struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
				t.Fatalf("decode created task: %v", err)
			}

			steps := []struct {
				path string
				body string
			}{
				{path: "/api/tasks/" + created.ID + "/lock", body: `{"operation_id":"op-lock","expected_generation":0,"snapshot_id":"c1"}`},
				{path: "/api/tasks/" + created.ID + "/film-checks", body: `{"operation_id":"op-film","expected_generation":1,"seal_id":"film-1","content":"intact"}`},
				{path: "/api/tasks/" + created.ID + "/leases", body: `{"operation_id":"op-start","expected_generation":1,"drill_id":"drill-1","zone_id":"A"}`},
			}
			for _, step := range steps {
				rec = doJSON(t, srv, http.MethodPost, step.path, step.body)
				if rec.Code != http.StatusOK {
					t.Fatalf("POST %s status = %d, body=%s", step.path, rec.Code, rec.Body.String())
				}
			}

			rec = doJSON(t, srv, http.MethodGet, "/api/tasks/"+created.ID, "")
			var before struct {
				Cells []struct {
					HoleID string `json:"hole_id"`
				} `json:"cells"`
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("initial snapshot status = %d, body=%s", rec.Code, rec.Body.String())
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &before); err != nil {
				t.Fatalf("decode initial snapshot: %v", err)
			}
			if len(before.Cells) != 1 {
				t.Fatalf("initial snapshot has %d cells, want 1", len(before.Cells))
			}
			holeID := before.Cells[0].HoleID

			coreBody := fmt.Sprintf(`{"operation_id":"op-core","expected_generation":1,"hole_id":%q,"core_mass":%d}`, holeID, largeMass)
			rec = doJSON(t, srv, http.MethodPost, "/api/tasks/"+created.ID+"/cores", coreBody)
			if rec.Code != http.StatusOK {
				t.Fatalf("register core status = %d, body=%s", rec.Code, rec.Body.String())
			}

			rec = doJSON(t, srv, http.MethodGet, "/api/tasks/"+created.ID, "")
			var after struct {
				Cells []struct {
					CoreMass int64 `json:"core_mass"`
				} `json:"cells"`
			}
			if rec.Code != http.StatusOK {
				t.Fatalf("core snapshot status = %d, body=%s", rec.Code, rec.Body.String())
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &after); err != nil {
				t.Fatalf("decode core snapshot: %v", err)
			}
			if got := after.Cells[0].CoreMass; got != largeMass {
				t.Errorf("snapshot core_mass = %d, want exact submitted value %d", got, largeMass)
			}

			sealBody := fmt.Sprintf(`{"operation_id":"op-seal","expected_generation":1,"hole_id":%q,"test":%d,"retained":%d,"loss":%d}`, holeID, tc.test, tc.retained, tc.loss)
			rec = doJSON(t, srv, http.MethodPost, "/api/tasks/"+created.ID+"/seals", sealBody)
			if rec.Code != http.StatusOK {
				t.Fatalf("split seal status = %d, body=%s", rec.Code, rec.Body.String())
			}

			tx, err := srv.svc.Store().Begin(context.Background())
			if err != nil {
				t.Fatalf("begin seal read: %v", err)
			}
			defer tx.Rollback()
			for i, want := range []int64{tc.test, tc.retained, tc.loss} {
				sealID := "seal-" + domain.CanonicalDigest(fmt.Sprint(created.ID, holeID, i))[:16]
				seal, err := tx.Seal(context.Background(), sealID)
				if err != nil {
					t.Fatalf("load persisted seal %d: %v", i, err)
				}
				if seal.Mass != want {
					t.Errorf("persisted seal %d mass = %d, want exact submitted value %d", i, seal.Mass, want)
				}
			}
		})
	}
}
