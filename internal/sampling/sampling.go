// Package sampling models the three-dimensional sampling grid, core holes and
// the exclusive, time-bounded resource leases. It enforces the outer-to-inner
// depth order and the hole-plugging barrier, and coordinates lease acquisition,
// first-point creation, renewal and release as a single transaction.
package sampling

import "silage/internal/domain"

// SamplingCell is one three-dimensional grid cell validated at lock time. Its
// lifecycle is strictly ordered: a cell is established (Covered), then its
// original core is registered (CoreMass), then split and sealed (Sealed), then
// plugged (Plugged). A cell may not advance to the next stage until the current
// stage completes, and the exposure front may not move past an unplugged hole.
type SamplingCell struct {
	Coordinate   domain.Coordinate `json:"coordinate"`
	HarvestBatch string            `json:"harvest_batch"`
	BlindCode    string            `json:"blind_code"`
	HoleID       string            `json:"hole_id"`
	Order        int               `json:"order"`
	Covered      bool              `json:"covered"`
	CoreMass     int64             `json:"core_mass"`
	Sealed       bool              `json:"sealed"`
	Plugged      bool              `json:"plugged"`
	Generation   domain.Generation `json:"generation"`
}

// CoreHole is the physical coring site associated with a cell.
type CoreHole struct {
	ID         string
	Coordinate domain.Coordinate
	BlindCode  string
	Order      int
	Plugged    bool
	LeaseToken string
}

// ResourceType enumerates the leaseable resource kinds.
type ResourceType string

const (
	ResourceDrill ResourceType = "drill"
	ResourceHole  ResourceType = "hole"
	ResourceZone  ResourceType = "zone"
)

// ResourceLease is an exclusive, time-bounded lease over one resource.
type ResourceLease struct {
	ResourceType  ResourceType
	ResourceID    string
	TaskID        string
	HoleID        string
	Token         string
	AcquiredAt    int64
	ExpiresAt     int64
	Renewals      int
	ReleaseReason string
}

// LeaseCoordinator acquires, renews and releases exclusive leases using
// injected logical time. It must guarantee that acquisition and first-point
// creation commit atomically, and that no partial lease or coverage remains on
// conflict or failure. A concrete, transaction-bound implementation lives in
// the store package.
type LeaseCoordinator interface {
	// Acquire attempts an exclusive lease for the given resource for ttl units
	// of logical time.
	Acquire(resource ResourceType, id string, ttl int64) (ResourceLease, error)
	// Renew extends the lease by ttl units and returns the updated lease.
	Renew(resource ResourceType, id string, ttl int64) (ResourceLease, error)
	// Release returns the lease, recording the reason.
	Release(resource ResourceType, id string, reason string) error
}
