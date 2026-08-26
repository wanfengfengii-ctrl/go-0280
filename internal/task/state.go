package task

import (
	"fmt"

	"silage/internal/domain"
)

// transitions is the allowed state machine. Every non-terminal state lists the
// states it may advance to; terminal states have no outgoing edge, so any write
// command on a terminal task is rejected before it can change business state.
var transitions = map[Status][]Status{
	StatusPendingLock:   {StatusFilmCheck},
	StatusFilmCheck:     {StatusCoring},
	StatusCoring:        {StatusSealing, StatusFermenting},
	StatusSealing:       {StatusCoring, StatusFermenting},
	StatusFermenting:    {StatusExpanding, StatusVentilating, StatusPendingReview},
	StatusExpanding:     {StatusVentilating, StatusPendingReview},
	StatusVentilating:   {StatusPendingReview},
	StatusPendingReview: {StatusOpenable},
	StatusOpenable:      {StatusOpened, StatusFeedIsolated, StatusCancelled},
}

// CanTransition reports whether the state machine permits moving from one state
// to another.
func CanTransition(from, to Status) bool {
	if from == to {
		return true
	}
	for _, n := range transitions[from] {
		if n == to {
			return true
		}
	}
	return false
}

// Aggregate is the in-memory task aggregate. It encapsulates the state machine,
// generation control and the final single-writer barrier. Persistence is owned
// by the application service which loads, applies and saves this aggregate.
type Aggregate struct {
	Task   InspectionTask
	Events []AuditEvent
	seq    int64
}

// NewAggregate wraps a persisted task and resumes its audit sequence counter.
func NewAggregate(t InspectionTask, events []AuditEvent) *Aggregate {
	a := &Aggregate{Task: t, Events: events}
	for _, e := range events {
		if e.Sequence > a.seq {
			a.seq = e.Sequence
		}
	}
	return a
}

// NextEventSeq returns the next audit sequence number.
func (a *Aggregate) NextEventSeq() int64 { return a.seq + 1 }

func (a *Aggregate) emit(status Status, code domain.StableCode, at int64) {
	a.seq++
	a.Events = append(a.Events, AuditEvent{Sequence: a.seq, Status: status, ErrorCode: code, At: at})
}

// Status returns the current status.
func (a *Aggregate) Status() Status { return a.Task.Status }

// Generation returns the current generation.
func (a *Aggregate) Generation() domain.Generation { return a.Task.Generation }

// GuardWrite rejects any business write against a terminal task.
func (a *Aggregate) GuardWrite() error {
	if a.Task.Status.IsFinal() {
		return &domain.Error{
			Code:    domain.CodeFinalAlreadyWritten,
			Message: "task has reached a terminal state",
			Reasons: []domain.Reason{{Constraint: fmt.Sprintf("status=%s", a.Task.Status)}},
		}
	}
	return nil
}

// CheckGeneration rejects a command whose expected generation does not match the
// current generation, preventing stale commands from mutating newer evidence.
func (a *Aggregate) CheckGeneration(expected domain.Generation) error {
	if expected != a.Task.Generation {
		return &domain.Error{
			Code:    domain.CodeGenerationConflict,
			Message: "task generation has advanced",
			Reasons: []domain.Reason{{Constraint: fmt.Sprintf(
				"expected=%d current=%d", expected, a.Task.Generation)}},
		}
	}
	return nil
}

// Transition advances the task to the target status after validating the state
// machine edge. It returns an error (leaving the aggregate unchanged) when the
// transition is not permitted.
func (a *Aggregate) Transition(to Status, at int64) error {
	if err := a.GuardWrite(); err != nil {
		return err
	}
	if !CanTransition(a.Task.Status, to) {
		return &domain.Error{
			Code:    domain.CodeGenerationConflict,
			Message: fmt.Sprintf("invalid transition %s -> %s", a.Task.Status, to),
			Reasons: []domain.Reason{{Constraint: "state_machine"}},
		}
	}
	a.Task.Status = to
	a.Task.UpdatedAt = at
	a.Task.Version++
	a.emit(to, "", at)
	return nil
}

// BumpGeneration increments the task generation, used when an expansion wave is
// frozen. The generation bump and status change are committed atomically by the
// caller.
func (a *Aggregate) BumpGeneration(at int64) {
	a.Task.Generation++
	a.Task.UpdatedAt = at
	a.Task.Version++
}

// Block records a blocking reason (for example exceeding the retry ceiling) and
// moves the task toward pending review so a human can adjudicate.
func (a *Aggregate) Block(reason string, at int64) error {
	if err := a.GuardWrite(); err != nil {
		return err
	}
	a.Task.BlockReason = reason
	a.Task.UpdatedAt = at
	a.Task.Version++
	a.emit(a.Task.Status, domain.CodeInstrumentRetryPending, at)
	return nil
}
