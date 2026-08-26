package domain

// OperationID is the caller-supplied idempotency key required on every write
// command. The same key with the same normalized body returns the original
// result; the same key with different content yields a stable conflict code.
type OperationID string

// Generation identifies one immutable wave of a task's evidence. Older
// generations may be audited but must never overwrite or close newer evidence.
type Generation int64

// Coordinate is the three-dimensional sampling cell location, formed from the
// silo zone, compaction layer and coring depth.
type Coordinate struct {
	Zone  string `json:"zone"`
	Layer int    `json:"layer"`
	Depth int    `json:"depth"`
}

// Less defines the deterministic total ordering used for grids, expansion
// sets and any sorted output: zone ascending, then layer, then depth.
func (c Coordinate) Less(o Coordinate) bool {
	switch {
	case c.Zone != o.Zone:
		return c.Zone < o.Zone
	case c.Layer != o.Layer:
		return c.Layer < o.Layer
	default:
		return c.Depth < o.Depth
	}
}
