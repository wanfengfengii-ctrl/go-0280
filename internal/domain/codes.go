// Package domain holds the shared, dependency-free primitives used across the
// business packages: the stable error protocol, idempotency keys, task
// generations, three-dimensional sampling coordinates and logical time.
package domain

import "sort"

// StableCode is a stable error code returned by both the domain layer and the
// HTTP JSON API. The set below is the minimum required by the error protocol.
type StableCode string

const (
	CodeBatchMismatch          StableCode = "BATCH_MISMATCH"
	CodeStaleRuleDigest        StableCode = "STALE_RULE_DIGEST"
	CodeDuplicateFilmSeal      StableCode = "DUPLICATE_FILM_SEAL"
	CodeGridGap                StableCode = "GRID_GAP"
	CodeGridOverlap            StableCode = "GRID_OVERLAP"
	CodeLeaseConflict          StableCode = "LEASE_CONFLICT"
	CodeHoleNotPlugged         StableCode = "HOLE_NOT_PLUGGED"
	CodeBlindCodeEarly         StableCode = "BLIND_CODE_EARLY"
	CodeMassNotConserved       StableCode = "MASS_NOT_CONSERVED"
	CodeReadingStale           StableCode = "READING_STALE"
	CodeFixedPointOverflow     StableCode = "FIXED_POINT_OVERFLOW"
	CodeInstrumentRetryPending StableCode = "INSTRUMENT_RETRY_PENDING"
	CodeGenerationConflict     StableCode = "GENERATION_CONFLICT"
	CodeIdempotencyConflict    StableCode = "IDEMPOTENCY_CONFLICT"
	CodeFinalAlreadyWritten    StableCode = "FINAL_ALREADY_WRITTEN"
)

// AllStableCodes lists every stable code in a fixed, deterministic order.
var AllStableCodes = []StableCode{
	CodeBatchMismatch,
	CodeStaleRuleDigest,
	CodeDuplicateFilmSeal,
	CodeGridGap,
	CodeGridOverlap,
	CodeLeaseConflict,
	CodeHoleNotPlugged,
	CodeBlindCodeEarly,
	CodeMassNotConserved,
	CodeReadingStale,
	CodeFixedPointOverflow,
	CodeInstrumentRetryPending,
	CodeGenerationConflict,
	CodeIdempotencyConflict,
	CodeFinalAlreadyWritten,
}

// Reason pinpoints the coordinate or constraint behind a rejection so the
// frontend can locate it against a specific sampling cell.
type Reason struct {
	Zone       string `json:"zone,omitempty"`
	Layer      int    `json:"layer,omitempty"`
	Depth      int    `json:"depth,omitempty"`
	BlindCode  string `json:"blind_code,omitempty"`
	HoleID     string `json:"hole_id,omitempty"`
	Constraint string `json:"constraint"`
}

// Error carries a stable code, a human-readable message and an ordered list
// of reasons. Reasons are always sorted deterministically before serialization.
type Error struct {
	Code    StableCode
	Message string
	Reasons []Reason
}

// Error implements the error interface.
func (e *Error) Error() string { return e.Message }

// SortReasons orders reasons by zone, layer, depth, blind code and hole id.
func SortReasons(rs []Reason) {
	sort.SliceStable(rs, func(i, j int) bool {
		a, b := rs[i], rs[j]
		switch {
		case a.Zone != b.Zone:
			return a.Zone < b.Zone
		case a.Layer != b.Layer:
			return a.Layer < b.Layer
		case a.Depth != b.Depth:
			return a.Depth < b.Depth
		case a.BlindCode != b.BlindCode:
			return a.BlindCode < b.BlindCode
		default:
			return a.HoleID < b.HoleID
		}
	})
}
