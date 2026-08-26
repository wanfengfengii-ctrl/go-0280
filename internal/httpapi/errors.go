package httpapi

import "silage/internal/domain"

// ErrorResponse is the stable JSON error protocol returned by every endpoint.
// It always carries a stable_code, a message, the current task_generation and
// an ordered list of reasons pinpointing the offending coordinates.
type ErrorResponse struct {
	StableCode     domain.StableCode `json:"stable_code"`
	Message        string            `json:"message"`
	TaskGeneration *int64            `json:"task_generation,omitempty"`
	Reasons        []domain.Reason   `json:"reasons"`
}

// NewErrorResponse builds an ErrorResponse from a domain error, sorting reasons.
func NewErrorResponse(err *domain.Error) ErrorResponse {
	domain.SortReasons(err.Reasons)
	return ErrorResponse{
		StableCode: err.Code,
		Message:    err.Message,
		Reasons:    err.Reasons,
	}
}
