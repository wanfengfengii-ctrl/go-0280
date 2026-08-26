package arbitration

import (
	"silage/internal/domain"
)

// ReviewSet is the collection of independent reviewer opinions submitted before
// a terminal command may compete for the single-writer barrier.
type ReviewSet struct {
	Opinions []ReviewOpinion
}

// ValidateReviews enforces the dual-person barrier: at least two different,
// qualified reviewers must have each submitted an independent opinion. Fewer
// than two, or two opinions from the same reviewer, do not satisfy the rule.
func ValidateReviews(opinions []ReviewOpinion) error {
	reviewers := map[string]bool{}
	qualified := 0
	for _, o := range opinions {
		if !o.Qualified {
			continue
		}
		if o.ReviewerID == "" {
			continue
		}
		if !reviewers[o.ReviewerID] {
			reviewers[o.ReviewerID] = true
			qualified++
		}
	}
	if qualified < 2 {
		return &domain.Error{
			Code:    domain.CodeReadingStale,
			Message: "final adjudication requires two distinct qualified reviewers",
			Reasons: []domain.Reason{{Constraint: "reviewer_quorum"}},
		}
	}
	return nil
}

// WinnerOf resolves the terminal kind from a command string. It returns false
// when the command is not one of the three competing terminal commands.
func WinnerOf(command string) (FinalKind, bool) {
	switch FinalKind(command) {
	case FinalOpen, FinalFeedIsolate, FinalCancel:
		return FinalKind(command), true
	default:
		return "", false
	}
}

// NewCredential builds the single immutable outcome of a won terminal barrier.
// The caller is responsible for enforcing uniqueness through the database
// constraint and version check so exactly one credential exists per task.
func NewCredential(id, taskID string, kind FinalKind, winner string, at int64) FinalCredential {
	return FinalCredential{
		ID:          id,
		TaskID:      taskID,
		Kind:        kind,
		Winner:      winner,
		GeneratedAt: at,
	}
}
