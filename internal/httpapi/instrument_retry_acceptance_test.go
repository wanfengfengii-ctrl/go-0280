package httpapi_test

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
	"silage/internal/domain"
	"silage/internal/evidence"
	"silage/internal/httpapi"
	"silage/internal/store"
)

type retryAcceptanceClock struct{ now int64 }

func (c *retryAcceptanceClock) Now() int64 {
	c.now++
	return c.now
}

type retryAcceptanceAdapter struct {
	calls int
	err   error
}

func (a *retryAcceptanceAdapter) Submit(evidence.InstrumentCall) (int64, int64, error) {
	a.calls++
	if a.err != nil {
		return 0, 0, a.err
	}
	return 701, 100, nil
}

func TestModel_InstrumentRetryPersistsRecoversAndReplays(t *testing.T) {
	cases := []struct {
		name        string
		adapterErr  error
		noAdapter   bool
		wantPending bool
		wantFailure string
	}{
		{name: "rejected", adapterErr: evidence.ErrInstrumentRejected, wantPending: true, wantFailure: "rejected"},
		{name: "disconnected", adapterErr: evidence.ErrInstrumentDisconnected, wantPending: true, wantFailure: "disconnected"},
		{name: "timeout", adapterErr: evidence.ErrInstrumentTimeout, wantPending: true, wantFailure: "timeout"},
		{name: "adapter_not_configured", noAdapter: true, wantPending: true},
		{name: "successful_reading", wantPending: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			clock := &retryAcceptanceClock{}
			dbPath := filepath.Join(t.TempDir(), "instrument-retry.db")
			st, err := store.Open(dbPath, clock)
			if err != nil {
				t.Fatalf("open store: %v", err)
			}

			adapter := &retryAcceptanceAdapter{err: tc.adapterErr}
			var instrument evidence.InstrumentAdapter = adapter
			if tc.noAdapter {
				instrument = nil
			}
			svc := app.NewService(st, clock, instrument, nil)
			task, err := svc.CreateTask(ctx, "silo-retry")
			if err != nil {
				t.Fatalf("create task: %v", err)
			}
			server := httpapi.NewServer(svc)
			post := func(body string) *httptest.ResponseRecorder {
				t.Helper()
				req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+task.ID+"/retries", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				server.Handler().ServeHTTP(rec, req)
				return rec
			}
			getSnapshot := func(handler http.Handler) ([]evidence.InstrumentCall, []evidence.EvidenceReading) {
				t.Helper()
				req := httptest.NewRequest(http.MethodGet, "/api/tasks/"+task.ID, nil)
				rec := httptest.NewRecorder()
				handler.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("snapshot status = %d, body=%s", rec.Code, rec.Body.String())
				}
				var snapshot struct {
					Calls    []evidence.InstrumentCall  `json:"calls"`
					Readings []evidence.EvidenceReading `json:"readings"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &snapshot); err != nil {
					t.Fatalf("decode snapshot: %v", err)
				}
				return snapshot.Calls, snapshot.Readings
			}

			first := post(`{"operation_id":"retry-ph-1","expected_generation":0,"hole_id":"hole-7","metric":"ph"}`)
			replay := post("{\n  \"metric\": \"ph\", \"hole_id\": \"hole-7\", \"expected_generation\": 0, \"operation_id\": \"retry-ph-1\"\n}")
			wantStatus := http.StatusOK
			if tc.wantPending {
				wantStatus = http.StatusConflict
			}
			if first.Code != wantStatus {
				t.Fatalf("first retry status = %d, want %d; body=%s", first.Code, wantStatus, first.Body.String())
			}
			if replay.Code != first.Code || replay.Body.String() != first.Body.String() {
				t.Fatalf("same operation did not replay stably: first=(%d %s), replay=(%d %s)", first.Code, first.Body.String(), replay.Code, replay.Body.String())
			}
			if tc.wantPending {
				var response httpapi.ErrorResponse
				if err := json.Unmarshal(first.Body.Bytes(), &response); err != nil {
					t.Fatalf("decode pending response: %v", err)
				}
				if response.StableCode != domain.CodeInstrumentRetryPending {
					t.Fatalf("stable code = %q, want %q", response.StableCode, domain.CodeInstrumentRetryPending)
				}
			}
			wantAdapterCalls := 1
			if tc.noAdapter {
				wantAdapterCalls = 0
			}
			if adapter.calls != wantAdapterCalls {
				t.Fatalf("adapter calls = %d, want %d after idempotent replay", adapter.calls, wantAdapterCalls)
			}

			calls, readings := getSnapshot(server.Handler())
			if len(calls) != 1 {
				t.Fatalf("snapshot calls = %d, want one persisted call", len(calls))
			}
			if calls[0].TaskID != task.ID || calls[0].HoleID != "hole-7" || calls[0].Metric != "ph" || calls[0].Instrument != "ph_meter" {
				t.Fatalf("persisted call = %+v", calls[0])
			}
			if tc.wantPending {
				if calls[0].Status != evidence.CallRetry || calls[0].Retries != 1 {
					t.Fatalf("pending call status/retries = %s/%d, want retry/1", calls[0].Status, calls[0].Retries)
				}
				if tc.wantFailure != "" && calls[0].FailureClass != tc.wantFailure {
					t.Fatalf("failure class = %q, want %q", calls[0].FailureClass, tc.wantFailure)
				}
				if len(readings) != 0 {
					t.Fatalf("pending call fabricated %d readings", len(readings))
				}
			} else {
				if calls[0].Status != evidence.CallAccepted || calls[0].Retries != 0 {
					t.Fatalf("successful call status/retries = %s/%d, want accepted/0", calls[0].Status, calls[0].Retries)
				}
				if len(readings) != 1 || !readings[0].Valid || readings[0].CallID != calls[0].ID || readings[0].Value != 701 || readings[0].Scale != 100 {
					t.Fatalf("accepted readings = %+v, want one valid adapter reading", readings)
				}
			}

			if err := st.Close(); err != nil {
				t.Fatalf("close store: %v", err)
			}
			reopened, err := store.Open(dbPath, clock)
			if err != nil {
				t.Fatalf("reopen store: %v", err)
			}
			defer reopened.Close()
			report, err := reopened.Recover(ctx, clock.Now())
			if err != nil {
				t.Fatalf("recover: %v", err)
			}
			wantRecoveredPending := 0
			if tc.wantPending {
				wantRecoveredPending = 1
			}
			if report.PendingRetries != wantRecoveredPending {
				t.Fatalf("recovered pending retries = %d, want %d", report.PendingRetries, wantRecoveredPending)
			}
			restarted := httpapi.NewServer(app.NewService(reopened, clock, nil, nil))
			recoveredCalls, recoveredReadings := getSnapshot(restarted.Handler())
			if !reflect.DeepEqual(recoveredCalls, calls) || !reflect.DeepEqual(recoveredReadings, readings) {
				t.Fatalf("restart snapshot changed persisted evidence: before calls/readings=%+v/%+v after=%+v/%+v", calls, readings, recoveredCalls, recoveredReadings)
			}
		})
	}
}
