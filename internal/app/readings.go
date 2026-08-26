package app

import (
	"context"

	"silage/internal/domain"
	"silage/internal/evidence"
	"silage/internal/store"
	"silage/internal/task"
)

// ReadingRequest submits one instrument reading for a hole and metric.
type ReadingRequest struct {
	OperationID domain.OperationID
	Generation  domain.Generation
	HoleID      string
	Metric      string
	Value       int64
	Scale       int64
}

// SubmitReading records a client-submitted instrument reading. A stale or
// out-of-order reading (one that is not newer than the current evidence for the
// same hole and metric) is recorded as an audit fact and rejected with
// READING_STALE, never overwriting newer valid evidence.
func (s *Service) SubmitReading(ctx context.Context, taskID string, req ReadingRequest) (evidence.EvidenceReading, error) {
	digest := domain.CanonicalDigest(req)
	var out evidence.EvidenceReading

	err := s.runInTx(ctx, func(tx *store.Tx) error {
		if res, ok, err := s.checkIdempotent(ctx, tx, req.OperationID, digest); err != nil {
			return err
		} else if ok {
			_ = res
			return nil
		}
		tk, err := tx.Task(ctx, taskID)
		if err != nil {
			return err
		}
		agg := task.NewAggregate(tk, nil)
		if err := agg.GuardWrite(); err != nil {
			return err
		}
		if err := agg.CheckGeneration(req.Generation); err != nil {
			return err
		}

		seq := s.nextSeq(ctx, tx, taskID)
		reading := evidence.EvidenceReading{
			CallID:     newID("reading-", taskID, req.HoleID, req.Metric, seq),
			TaskID:     taskID,
			HoleID:     req.HoleID,
			Metric:     req.Metric,
			Generation: req.Generation,
			Seq:        seq,
			Value:      req.Value,
			Scale:      req.Scale,
			At:         s.clock.Now(),
			Valid:      true,
		}
		if err := evidence.ValidateReading(reading); err != nil {
			return err
		}

		// Staleness: an older reading for the same hole and metric cannot
		// replace a newer valid one.
		if s.isStale(ctx, tx, reading) {
			reading.Valid = false
			reading.RejectReason = "stale_reading"
			if err := tx.SaveReading(ctx, reading); err != nil {
				return err
			}
			_ = tx.AppendAudit(ctx, taskID, task.AuditEvent{
				Sequence:  agg.NextEventSeq(),
				Status:    agg.Status(),
				ErrorCode: domain.CodeReadingStale,
				At:        s.clock.Now(),
			})
			return &domain.Error{
				Code:    domain.CodeReadingStale,
				Message: "reading is stale and was not accepted",
				Reasons: []domain.Reason{{HoleID: req.HoleID, Constraint: "metric=" + req.Metric}},
			}
		}

		if err := tx.SaveReading(ctx, reading); err != nil {
			return err
		}
		out = reading
		return s.recordIdempotent(ctx, tx, req.OperationID, digest, idempotencyResult{Status: string(agg.Task.Status)})
	})
	return out, err
}

// isStale reports whether the reading is older than the current valid evidence
// for the same hole and metric.
func (s *Service) isStale(ctx context.Context, tx *store.Tx, r evidence.EvidenceReading) bool {
	readings, err := tx.ListReadings(ctx, r.TaskID)
	if err != nil {
		return false
	}
	for _, existing := range readings {
		if existing.HoleID == r.HoleID && existing.Metric == r.Metric && existing.Valid {
			if existing.Generation > r.Generation {
				return true
			}
			if existing.Generation == r.Generation && existing.Seq >= r.Seq {
				return true
			}
		}
	}
	return false
}

// nextSeq returns the next logical call sequence for a task.
func (s *Service) nextSeq(ctx context.Context, tx *store.Tx, taskID string) int64 {
	readings, err := tx.ListReadings(ctx, taskID)
	if err != nil {
		return 1
	}
	var max int64
	for _, r := range readings {
		if r.Seq > max {
			max = r.Seq
		}
	}
	return max + 1
}

// RunInstrument invokes an instrument through the injected adapter, converting
// rejection, disconnection, timeout and format errors into deterministic pending
// retries rather than fabricated readings. It returns the recorded call.
func (s *Service) RunInstrument(ctx context.Context, taskID string, req ReadingRequest) (evidence.InstrumentCall, error) {
	call := evidence.InstrumentCall{
		ID:         newID("call-", taskID, req.HoleID, req.Metric, s.clock.Now()),
		TaskID:     taskID,
		Instrument: instrumentFor(req.Metric),
		HoleID:     req.HoleID,
		Metric:     req.Metric,
		Generation: req.Generation,
		Seq:        s.clock.Now(),
		Status:     evidence.CallPending,
	}

	err := s.runInTx(ctx, func(tx *store.Tx) error {
		tk, err := tx.Task(ctx, taskID)
		if err != nil {
			return err
		}
		agg := task.NewAggregate(tk, nil)
		if err := agg.GuardWrite(); err != nil {
			return err
		}
		if err := agg.CheckGeneration(req.Generation); err != nil {
			return err
		}

		if s.adapter == nil {
			return &domain.Error{Code: domain.CodeInstrumentRetryPending, Message: "no instrument adapter configured"}
		}
		raw, scale, err := s.adapter.Submit(call)
		if err != nil {
			call.FailureClass = string(evidence.Classify(err))
			call.Status = evidence.CallRetry
			call.Retries++
			if err := tx.SaveCall(ctx, call); err != nil {
				return err
			}
			return &domain.Error{
				Code:    domain.CodeInstrumentRetryPending,
				Message: "instrument call failed and is pending retry",
				Reasons: []domain.Reason{{HoleID: req.HoleID, Constraint: "failure=" + call.FailureClass}},
			}
		}
		call.Status = evidence.CallAccepted
		if err := tx.SaveCall(ctx, call); err != nil {
			return err
		}
		reading := evidence.EvidenceReading{
			CallID:     call.ID,
			TaskID:     taskID,
			HoleID:     req.HoleID,
			Metric:     req.Metric,
			Generation: req.Generation,
			Seq:        call.Seq,
			Value:      raw,
			Scale:      scale,
			At:         s.clock.Now(),
			Valid:      true,
		}
		return tx.SaveReading(ctx, reading)
	})
	return call, err
}

// instrumentFor maps a metric to its instrument family.
func instrumentFor(metric string) string {
	switch metric {
	case "ph":
		return "ph_meter"
	case "dry_matter", "lactic_acid":
		return "nir"
	case "butyric_acid", "ammonia_n", "mycotoxin":
		return "chromatograph"
	case "oxygen", "hydrogen_sulfide", "temp_rise":
		return "gas_probe"
	default:
		return "unknown"
	}
}
