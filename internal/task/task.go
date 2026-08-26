// Package task models the inspection task aggregate: the full state machine,
// generation control, idempotent operation records, the exposure front and the
// final single-writer barrier.
package task

import "silage/internal/domain"

// Status is one state of the inspection task state machine.
type Status string

const (
	StatusPendingLock   Status = "pending_lock"
	StatusFilmCheck     Status = "film_check"
	StatusCoring        Status = "coring"
	StatusSealing       Status = "sealing"
	StatusFermenting    Status = "fermenting"
	StatusExpanding     Status = "expanding"
	StatusVentilating   Status = "ventilating"
	StatusPendingReview Status = "pending_review"
	StatusOpenable      Status = "openable"
	StatusOpened        Status = "opened"
	StatusFeedIsolated  Status = "feed_isolated"
	StatusCancelled     Status = "cancelled"
)

// FinalStates are the terminal states after which every business write is
// rejected.
var FinalStates = map[Status]bool{
	StatusOpened:       true,
	StatusFeedIsolated: true,
	StatusCancelled:    true,
}

// IsFinal reports whether the status is terminal.
func (s Status) IsFinal() bool { return FinalStates[s] }

// InspectionTask is the root aggregate persisted and recovered at startup.
type InspectionTask struct {
	ID                string            `json:"id"`
	SiloID            string            `json:"silo_id"`
	SnapshotID        string            `json:"snapshot_id"`
	Status            Status            `json:"status"`
	Generation        domain.Generation `json:"generation"`
	ExposureFront     domain.Coordinate `json:"exposure_front"`
	CreatedAt         int64             `json:"created_at"`
	UpdatedAt         int64             `json:"updated_at"`
	BlockReason       string            `json:"block_reason,omitempty"`
	Version           int64             `json:"version"`
	FinalCredentialID string            `json:"final_credential_id,omitempty"`
}

// IdempotencyRecord captures a completed command for stable replay.
type IdempotencyRecord struct {
	OperationID    domain.OperationID
	RequestDigest  string
	ResponseDigest string
	Status         Status
	ErrorCode      domain.StableCode
}

// AuditEvent is an append-only record of a state change or a rejection fact.
type AuditEvent struct {
	Sequence  int64
	Status    Status
	ErrorCode domain.StableCode
	At        int64
}
