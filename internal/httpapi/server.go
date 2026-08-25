// Package httpapi exposes the JSON API endpoints and hosts the pre-built
// frontend workbench. It normalizes every error into the stable error protocol
// and serves live backend state for the page to render.
package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
	"sync/atomic"
	"time"

	"silage/internal/app"
)

//go:embed all:dist
var distFS embed.FS

// Server is the HTTP entry point for the API and the embedded workbench.
type Server struct {
	svc     *app.Service
	mux     *http.ServeMux
	started int64
	tasks   atomic.Int64
}

// NewServer builds a Server over the application service and registers every
// route.
func NewServer(svc *app.Service) *Server {
	s := &Server{svc: svc, started: time.Now().UnixMilli()}
	mux := http.NewServeMux()

	// Health and catalog/task collection.
	mux.HandleFunc("GET /api/health", s.handleHealth)
	mux.HandleFunc("GET /api/catalogs", s.handleListCatalogs)
	mux.HandleFunc("POST /api/catalogs", s.handleCreateCatalog)
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("POST /api/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)

	// The write command surface.
	mux.HandleFunc("POST /api/tasks/{id}/lock", s.handleLock)
	mux.HandleFunc("POST /api/tasks/{id}/film-checks", s.handleFilmCheck)
	mux.HandleFunc("POST /api/tasks/{id}/leases", s.handleStartCoring)
	mux.HandleFunc("POST /api/tasks/{id}/cores", s.handleRegisterCore)
	mux.HandleFunc("POST /api/tasks/{id}/seals", s.handleSplitSeal)
	mux.HandleFunc("POST /api/tasks/{id}/readings", s.handleSubmitReading)
	mux.HandleFunc("POST /api/tasks/{id}/retries", s.handleRunInstrument)
	mux.HandleFunc("POST /api/tasks/{id}/expansions", s.handleExpand)
	mux.HandleFunc("POST /api/tasks/{id}/ventilation", s.handleVentilate)
	mux.HandleFunc("POST /api/tasks/{id}/reviews", s.handleReview)
	mux.HandleFunc("POST /api/tasks/{id}/finalize", s.handleFinalize)

	// Static frontend workbench.
	static, err := fs.Sub(distFS, "dist")
	if err == nil {
		mux.Handle("/", http.FileServer(http.FS(static)))
	}
	s.mux = mux
	return s
}

// Handler returns the root handler for wiring into an http.Server.
func (s *Server) Handler() http.Handler { return s.mux }

type healthResponse struct {
	Status   string `json:"status"`
	Service  string `json:"service"`
	UptimeMS int64  `json:"uptime_ms"`
	Tasks    int64  `json:"tasks"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthResponse{
		Status:   "ok",
		Service:  "silage-core-opening-inspection-closure",
		UptimeMS: time.Now().UnixMilli() - s.started,
		Tasks:    s.tasks.Load(),
	})
}
