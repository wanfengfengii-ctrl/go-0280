package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"silage/internal/app"
	"silage/internal/arbitration"
	"silage/internal/catalog"
	"silage/internal/domain"
)

// --- Shared helpers ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

// writeErr serializes an error into the stable error protocol. A domain error
// carries a stable code and reasons; any other error is mapped to a generic
// internal code.
func writeErr(w http.ResponseWriter, status int, err error) {
	var de *domain.Error
	if !errors.As(err, &de) {
		writeJSON(w, status, ErrorResponse{
			StableCode: domain.StableCode("INTERNAL"),
			Message:    err.Error(),
			Reasons:    []domain.Reason{},
		})
		return
	}
	writeJSON(w, status, NewErrorResponse(de))
}

func httpStatus(err error) int {
	var de *domain.Error
	if !errors.As(err, &de) {
		return http.StatusInternalServerError
	}
	switch de.Code {
	case domain.CodeIdempotencyConflict, domain.CodeGenerationConflict,
		domain.CodeLeaseConflict, domain.CodeFinalAlreadyWritten,
		domain.CodeDuplicateFilmSeal, domain.CodeHoleNotPlugged,
		domain.CodeBlindCodeEarly, domain.CodeMassNotConserved,
		domain.CodeReadingStale, domain.CodeInstrumentRetryPending,
		domain.CodeBatchMismatch, domain.CodeStaleRuleDigest,
		domain.CodeGridGap, domain.CodeGridOverlap, domain.CodeFixedPointOverflow:
		return http.StatusConflict
	default:
		return http.StatusBadRequest
	}
}

// --- Catalog ---

type createCatalogRequest struct {
	ID         string              `json:"id"`
	Version    int64               `json:"version"`
	PlotID     string              `json:"plot_id"`
	BatchID    string              `json:"harvest_batch_id"`
	Zones      []string            `json:"zones"`
	Layers     map[string][]int    `json:"layers"`
	Depths     []int               `json:"depths"`
	Adjacency  map[string][]string `json:"adjacency"`
	OpenFace   string              `json:"open_face"`
	Ventilator string              `json:"ventilator"`
	OxygenMin  int64               `json:"oxygen_min"`
	H2SMax     int64               `json:"h2s_max"`
	Scale      int64               `json:"scale"`
}

func (s *Server) handleCreateCatalog(w http.ResponseWriter, r *http.Request) {
	var req createCatalogRequest
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	snap := catalog.CatalogSnapshot{
		ID:               req.ID,
		Version:          req.Version,
		PlotID:           req.PlotID,
		HarvestBatchID:   req.BatchID,
		Zones:            makeZones(req.Zones),
		CompactionLayers: makeLayers(req.Layers),
		Adjacency:        req.Adjacency,
		OpenFace:         catalog.OpenFace{ID: req.OpenFace, Ventilator: req.Ventilator},
		Thresholds: catalog.Thresholds{
			OxygenMin: req.OxygenMin, HydrogenSulfMax: req.H2SMax, Scale: req.Scale,
		},
		Depths: req.Depths,
	}
	if err := s.svc.CreateCatalog(r.Context(), snap); err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": snap.ID, "version": snap.Version})
}

func (s *Server) handleListCatalogs(w http.ResponseWriter, r *http.Request) {
	snaps, err := s.svc.ListCatalogs(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"catalogs": snaps})
}

func makeZones(ids []string) []catalog.Zone {
	out := make([]catalog.Zone, 0, len(ids))
	for _, id := range ids {
		out = append(out, catalog.Zone{ID: id})
	}
	return out
}

func makeLayers(m map[string][]int) []catalog.CompactionLayer {
	var out []catalog.CompactionLayer
	for zone, seqs := range m {
		for _, seq := range seqs {
			out = append(out, catalog.CompactionLayer{ZoneID: zone, Seq: seq})
		}
	}
	return out
}

// --- Tasks ---

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SiloID string `json:"silo_id"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	tk, err := s.svc.CreateTask(r.Context(), req.SiloID)
	if err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	s.tasks.Add(1)
	writeJSON(w, http.StatusCreated, tk)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.svc.ListTasks(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": tasks})
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	snap, err := s.svc.Snapshot(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

// --- Write commands ---

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		OperationID domain.OperationID `json:"operation_id"`
		Generation  domain.Generation  `json:"expected_generation"`
		SnapshotID  string             `json:"snapshot_id"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	tk, err := s.svc.LockTask(context.Background(), id, app.LockTaskRequest{
		OperationID: req.OperationID, Generation: req.Generation, SnapshotID: req.SnapshotID,
	})
	if err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, tk)
}

func (s *Server) handleFilmCheck(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		OperationID domain.OperationID `json:"operation_id"`
		Generation  domain.Generation  `json:"expected_generation"`
		SealID      string             `json:"seal_id"`
		Content     string             `json:"content"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	tk, err := s.svc.FilmCheck(r.Context(), id, app.FilmCheckRequest{
		OperationID: req.OperationID, Generation: req.Generation, SealID: req.SealID, Content: req.Content,
	})
	if err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, tk)
}

func (s *Server) handleStartCoring(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		OperationID domain.OperationID `json:"operation_id"`
		Generation  domain.Generation  `json:"expected_generation"`
		DrillID     string             `json:"drill_id"`
		ZoneID      string             `json:"zone_id"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	if err := s.svc.StartCoring(r.Context(), id, app.StartCoringRequest{
		OperationID: req.OperationID, Generation: req.Generation, DrillID: req.DrillID, ZoneID: req.ZoneID,
	}); err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "coring_started"})
}

func (s *Server) handleRegisterCore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		OperationID domain.OperationID `json:"operation_id"`
		Generation  domain.Generation  `json:"expected_generation"`
		HoleID      string             `json:"hole_id"`
		CoreMass    int64              `json:"core_mass"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	if err := s.svc.RegisterCore(r.Context(), id, app.CoreRequest{
		OperationID: req.OperationID, Generation: req.Generation, HoleID: req.HoleID, CoreMass: req.CoreMass,
	}); err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "core_registered"})
}

func (s *Server) handleSplitSeal(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		OperationID domain.OperationID `json:"operation_id"`
		Generation  domain.Generation  `json:"expected_generation"`
		HoleID      string             `json:"hole_id"`
		Test        int64              `json:"test"`
		Retained    int64              `json:"retained"`
		Loss        int64              `json:"loss"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	if err := s.svc.SplitSeal(r.Context(), id, app.SplitRequest{
		OperationID: req.OperationID, Generation: req.Generation, HoleID: req.HoleID,
		Test: req.Test, Retained: req.Retained, Loss: req.Loss,
	}); err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "sealed"})
}

func (s *Server) handleSubmitReading(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		OperationID domain.OperationID `json:"operation_id"`
		Generation  domain.Generation  `json:"expected_generation"`
		HoleID      string             `json:"hole_id"`
		Metric      string             `json:"metric"`
		Value       int64              `json:"value"`
		Scale       int64              `json:"scale"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	reading, err := s.svc.SubmitReading(r.Context(), id, app.ReadingRequest{
		OperationID: req.OperationID, Generation: req.Generation, HoleID: req.HoleID,
		Metric: req.Metric, Value: req.Value, Scale: req.Scale,
	})
	if err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, reading)
}

func (s *Server) handleRunInstrument(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		OperationID domain.OperationID `json:"operation_id"`
		Generation  domain.Generation  `json:"expected_generation"`
		HoleID      string             `json:"hole_id"`
		Metric      string             `json:"metric"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	call, err := s.svc.RunInstrument(r.Context(), id, app.ReadingRequest{
		OperationID: req.OperationID, Generation: req.Generation, HoleID: req.HoleID, Metric: req.Metric,
	})
	if err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, call)
}

func (s *Server) handleExpand(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		OperationID domain.OperationID  `json:"operation_id"`
		Generation  domain.Generation   `json:"expected_generation"`
		Anomalies   []domain.Coordinate `json:"anomalies"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	plan, err := s.svc.Expand(r.Context(), id, app.ExpandRequest{
		OperationID: req.OperationID, Generation: req.Generation, Anomalies: req.Anomalies,
	})
	if err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) handleVentilate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		OperationID domain.OperationID       `json:"operation_id"`
		Generation  domain.Generation        `json:"expected_generation"`
		Readings    []arbitration.GasReading `json:"readings"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	win, err := s.svc.Ventilate(r.Context(), id, app.VentilateRequest{
		OperationID: req.OperationID, Generation: req.Generation, Readings: req.Readings,
	})
	if err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, win)
}

func (s *Server) handleReview(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		OperationID domain.OperationID `json:"operation_id"`
		Generation  domain.Generation  `json:"expected_generation"`
		ReviewerID  string             `json:"reviewer_id"`
		Qualified   bool               `json:"qualified"`
		Opinion     string             `json:"opinion"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	tk, err := s.svc.Review(r.Context(), id, app.ReviewRequest{
		OperationID: req.OperationID, Generation: req.Generation,
		ReviewerID: req.ReviewerID, Qualified: req.Qualified, Opinion: req.Opinion,
	})
	if err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, tk)
}

func (s *Server) handleFinalize(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		OperationID domain.OperationID `json:"operation_id"`
		Generation  domain.Generation  `json:"expected_generation"`
		Command     string             `json:"command"`
		Winner      string             `json:"winner"`
	}
	if err := decodeBody(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{StableCode: "BAD_REQUEST", Message: err.Error(), Reasons: []domain.Reason{}})
		return
	}
	cred, err := s.svc.Finalize(r.Context(), id, app.FinalizeRequest{
		OperationID: req.OperationID, Generation: req.Generation, Command: req.Command, Winner: req.Winner,
	})
	if err != nil {
		writeErr(w, httpStatus(err), err)
		return
	}
	writeJSON(w, http.StatusOK, cred)
}
