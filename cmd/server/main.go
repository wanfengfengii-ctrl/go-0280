// Command server is the runnable entry point for the silage inspection service.
// It opens the embedded relational store, performs deterministic startup
// recovery, wires the application service and serves the JSON API plus the
// embedded frontend workbench.
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"time"

	"silage/internal/app"
	"silage/internal/domain"
	"silage/internal/httpapi"
	"silage/internal/store"
)

func main() {
	addr := envOr("ADDR", ":8080")
	dbPath := envOr("DB_PATH", "silage.db")

	clock := domain.NewWallClock()
	st, err := store.Open(dbPath, clock)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	// Deterministic startup recovery: release only safely-expired leases and
	// reload all active tasks, pending retries, expansion generations and the
	// final barrier from persisted logical time.
	report, err := st.Recover(context.Background(), clock.Now())
	if err != nil {
		log.Fatalf("recover: %v", err)
	}
	log.Printf("recovered: %+v", report)

	svc := app.NewService(st, clock, nil, nil)
	srv := httpapi.NewServer(svc)

	s := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("silage inspection service listening on %s (db=%s)", addr, dbPath)
	if err := s.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
