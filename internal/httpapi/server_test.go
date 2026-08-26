package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"silage/internal/app"
	"silage/internal/domain"
	"silage/internal/store"
)

// newTestServer builds a Server over an in-memory store for handler tests.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(":memory:", domain.NewWallClock())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return NewServer(app.NewService(st, domain.NewWallClock(), nil, nil))
}

func TestHealthEndpoint(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body healthResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "ok" {
		t.Fatalf("status field = %q, want ok", body.Status)
	}
}

func TestStaticWorkbenchServed(t *testing.T) {
	srv := newTestServer(t)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "" {
		t.Fatal("missing content type for index page")
	}
}

func TestCatalogAndTaskLifecycleHTTP(t *testing.T) {
	srv := newTestServer(t)

	// Create a catalog.
	catalogBody := `{"id":"c1","version":1,"plot_id":"p1","harvest_batch_id":"b1","zones":["A"],"layers":{"A":[1,2]},"depths":[0,1],"adjacency":{"A:1":["A:2"]},"open_face":"f1","ventilator":"v1","oxygen_min":180,"h2s_max":5,"scale":1}`
	rec := doJSON(t, srv, http.MethodPost, "/api/catalogs", catalogBody)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create catalog status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// Create a task.
	rec = doJSON(t, srv, http.MethodPost, "/api/tasks", `{"silo_id":"s1"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create task status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var tk struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &tk); err != nil {
		t.Fatalf("decode task: %v", err)
	}
	if tk.ID == "" {
		t.Fatal("task id missing")
	}

	// Lock it.
	lockBody := `{"operation_id":"op-lock","expected_generation":0,"snapshot_id":"c1"}`
	rec = doJSON(t, srv, http.MethodPost, "/api/tasks/"+tk.ID+"/lock", lockBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("lock status = %d, body=%s", rec.Code, rec.Body.String())
	}

	// The snapshot endpoint must return the grid cells.
	rec = doJSON(t, srv, http.MethodGet, "/api/tasks/"+tk.ID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("snapshot status = %d", rec.Code)
	}
	var snap struct {
		Cells []struct {
			BlindCode string `json:"BlindCode"`
		} `json:"cells"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	if len(snap.Cells) != 4 {
		t.Fatalf("got %d cells, want 4", len(snap.Cells))
	}
}

func doJSON(t *testing.T, srv *Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	return rec
}
