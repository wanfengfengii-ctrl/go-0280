// Package arbitration models expansion, ventilation and the final dual-person
// adjudication. It computes deterministic expansion sets, validates continuous
// ventilation windows, and arbitrates the three competing terminal commands
// behind a single-writer barrier that produces one immutable credential.
package arbitration

import "silage/internal/domain"

// ExpansionPlan is one immutable expansion wave for a task generation.
type ExpansionPlan struct {
	TaskID          string
	Generation      domain.Generation
	SourceEvidence  string
	Coordinates     []domain.Coordinate // sorted, deduplicated union
	HarvestAffected []domain.Coordinate
	Digest          string
	Closed          bool
	Version         int64
}

// GasReading is one ordered gas reading within a ventilation window.
type GasReading struct {
	At     int64 `json:"at"`
	Oxygen int64 `json:"oxygen"`
	H2S    int64 `json:"h2s"`
}

// VentilationWindow is a contiguous run of valid gas readings on one open face.
type VentilationWindow struct {
	ID         string
	TaskID     string
	OpenFace   string
	Device     string
	Generation domain.Generation
	StartAt    int64
	EndAt      int64
	Readings   []GasReading
	Continuous bool
}

// ReviewOpinion is one qualified, independent reviewer opinion.
type ReviewOpinion struct {
	ReviewerID string `json:"reviewer_id"`
	Qualified  bool   `json:"qualified"`
	Opinion    string `json:"opinion"`
	At         int64  `json:"at"`
}

// FinalKind enumerates the three competing terminal commands.
type FinalKind string

const (
	FinalOpen        FinalKind = "open"
	FinalFeedIsolate FinalKind = "feed_isolate"
	FinalCancel      FinalKind = "cancel"
)

// FinalCredential is the single immutable outcome of the terminal barrier.
type FinalCredential struct {
	ID          string    `json:"id"`
	TaskID      string    `json:"task_id"`
	Kind        FinalKind `json:"kind"`
	Winner      string    `json:"winner"`
	GeneratedAt int64     `json:"generated_at"`
}
