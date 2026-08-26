package evidence

import (
	"fmt"
	"sort"

	"silage/internal/domain"
)

// FailureClass classifies an instrument fault. Rejections, disconnections,
// timeouts and format errors are all converted into pending retries; none may
// fabricate a reading, close a detection step, advance coverage or release a
// resource early.
type FailureClass string

const (
	FailureRejected  FailureClass = "rejected"
	FailureDisconn   FailureClass = "disconnected"
	FailureTimeout   FailureClass = "timeout"
	FailureMalformed FailureClass = "malformed"
)

// Classify maps an adapter error to a deterministic failure class. A nil error
// means the call succeeded and produced a valid reading.
func Classify(err error) FailureClass {
	switch {
	case err == nil:
		return ""
	case err == ErrInstrumentRejected:
		return FailureRejected
	case err == ErrInstrumentDisconnected:
		return FailureDisconn
	case err == ErrInstrumentTimeout:
		return FailureTimeout
	default:
		return FailureMalformed
	}
}

// Sentinels returned by an InstrumentAdapter to script the four failure modes.
var (
	ErrInstrumentRejected     = fmt.Errorf("instrument rejected the call")
	ErrInstrumentDisconnected = fmt.Errorf("instrument disconnected")
	ErrInstrumentTimeout      = fmt.Errorf("instrument call timed out")
)

// ReadingNewer reports whether candidate b supersedes existing a. Newer means a
// strictly higher generation; on an equal generation, a higher call sequence;
// on an equal call sequence, a higher instrument sequence. Stale or reordered
// readings therefore never overwrite a newer valid reading.
func ReadingNewer(a, b EvidenceReading) bool {
	if b.Generation != a.Generation {
		return b.Generation > a.Generation
	}
	if b.Seq != a.Seq {
		return b.Seq > a.Seq
	}
	return b.Seq > a.Seq
}

// OrderReadings sorts readings deterministically by generation, call sequence
// and instrument sequence so output is stable regardless of arrival order.
func OrderReadings(rs []EvidenceReading) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		if a.Generation != b.Generation {
			return a.Generation < b.Generation
		}
		return a.Seq < b.Seq
	})
}

// ValidateReading enforces the integer fixed-point contract of an accepted
// reading: the scale must be positive and the value must fit the declared
// scale. A malformed reading yields FIXED_POINT_OVERFLOW.
func ValidateReading(r EvidenceReading) error {
	if r.Scale <= 0 {
		return &domain.Error{
			Code:    domain.CodeFixedPointOverflow,
			Message: "reading scale must be positive",
			Reasons: []domain.Reason{{HoleID: r.HoleID, Constraint: "scale"}},
		}
	}
	return nil
}
