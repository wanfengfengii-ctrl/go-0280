package arbitration

import (
	"fmt"
	"sort"

	"silage/internal/domain"
)

// Cell couples a sampling coordinate to its harvest batch, which is the input
// expansion uses to propagate an anomaly across a locked grid.
type Cell struct {
	Coordinate   domain.Coordinate
	HarvestBatch string
}

// ComputeExpansion returns the deterministic expansion set for one or more
// anomalous coordinates: the ordered, deduplicated union of the locked
// adjacent-layer coordinates and the same-harvest-batch affected coordinates.
// Adjacency is keyed by "zone:layer" and maps to a list of "zone:layer" keys.
func ComputeExpansion(anomalies []domain.Coordinate, cells []Cell, adjacency map[string][]string) []domain.Coordinate {
	harvestByCoord := make(map[domain.Coordinate]string, len(cells))
	coordsByZoneLayer := make(map[string][]domain.Coordinate, len(cells))
	for _, c := range cells {
		harvestByCoord[c.Coordinate] = c.HarvestBatch
		k := zoneLayerKey(c.Coordinate.Zone, c.Coordinate.Layer)
		coordsByZoneLayer[k] = append(coordsByZoneLayer[k], c.Coordinate)
	}

	// anomalySet excludes the already-inspected anomaly coordinates from the
	// expansion result: the expansion is the set of new cells to inspect.
	anomalySet := make(map[domain.Coordinate]bool, len(anomalies))
	for _, a := range anomalies {
		anomalySet[a] = true
	}

	set := make(map[domain.Coordinate]bool)
	for _, a := range anomalies {
		// Adjacent compaction layers: every cell in the same zone at an
		// adjacent layer, across all coring depths.
		key := zoneLayerKey(a.Zone, a.Layer)
		for _, adj := range adjacency[key] {
			for _, c := range coordsByZoneLayer[adj] {
				if !anomalySet[c] {
					set[c] = true
				}
			}
		}
		// Same harvest batch: every other cell sharing the anomaly's batch.
		hb := harvestByCoord[a]
		for c, b := range harvestByCoord {
			if b == hb && !anomalySet[c] {
				set[c] = true
			}
		}
	}

	out := make([]domain.Coordinate, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}

func zoneLayerKey(zone string, layer int) string {
	return fmt.Sprintf("%s:%d", zone, layer)
}
