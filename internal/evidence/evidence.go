// Package evidence models sample sealing, the append-only seal transfer chain,
// instrument calls and versioned readings. It enforces mass conservation,
// blind-code reveal ordering, and integer fixed-point arithmetic; instrument
// failures become deterministic pending retries rather than fabricated readings.
package evidence

import "silage/internal/domain"

// SampleType classifies a split of the original core mass.
type SampleType string

const (
	SampleTest     SampleType = "test"
	SampleRetained SampleType = "retained"
	SampleLoss     SampleType = "loss"
)

// SampleSeal is a sealed sample whose original core is split exactly once.
type SampleSeal struct {
	ID         string
	BlindCode  string
	SampleType SampleType
	Mass       int64 // integer fixed-point
	LossReason string
	Holder     string
}

// SealTransfer is an append-only link in the custody chain.
type SealTransfer struct {
	SealID    string
	From      string
	To        string
	At        int64
	Operation domain.OperationID
	Seq       int
}

// CallStatus is the lifecycle state of an instrument call.
type CallStatus string

const (
	CallPending  CallStatus = "pending"
	CallAccepted CallStatus = "accepted"
	CallRetry    CallStatus = "retry"
)

// InstrumentCall is one scripted instrument invocation carrying a logical
// sequence number used to order readings deterministically.
type InstrumentCall struct {
	ID           string
	TaskID       string
	Instrument   string
	HoleID       string
	Metric       string
	Generation   domain.Generation
	Seq          int64
	Status       CallStatus
	FailureClass string
	Retries      int
}

// EvidenceReading is one accepted instrument reading, ordered by generation,
// call sequence and instrument sequence.
type EvidenceReading struct {
	CallID       string
	TaskID       string
	HoleID       string
	Metric       string
	Generation   domain.Generation
	Seq          int64
	Value        int64 // integer fixed-point
	Scale        int64
	At           int64
	Valid        bool
	RejectReason string
}

// InstrumentAdapter is the injected boundary to the four instrument families.
// Rejection, disconnection, timeout and format errors must be converted into
// pending retries with a deterministic retry count, never into fake readings.
type InstrumentAdapter interface {
	// Submit invokes the instrument for a metric and returns the raw outcome.
	Submit(call InstrumentCall) (raw int64, scale int64, err error)
}
