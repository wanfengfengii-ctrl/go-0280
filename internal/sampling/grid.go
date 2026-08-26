package sampling

import (
	"fmt"
	"sort"

	"silage/internal/domain"
)

// GridSpec describes the complete three-dimensional sampling grid required by a
// locked task: the ordered zones, the compaction-layer sequences within each
// zone, and the ordered coring depths that must be sampled per layer.
type GridSpec struct {
	Zones  []string
	Layers map[string][]int // zone -> ascending layer sequence
	Depths []int            // ascending coring depths, shallow-first
}

// Generate returns every required cell in deterministic order (zone, layer,
// depth ascending). The result is the canonical grid a valid lock freezes.
func (g GridSpec) Generate() []domain.Coordinate {
	var cells []domain.Coordinate
	zones := append([]string(nil), g.Zones...)
	sort.Strings(zones)
	for _, z := range zones {
		layers := append([]int(nil), g.Layers[z]...)
		sort.Ints(layers)
		for _, l := range layers {
			depths := append([]int(nil), g.Depths...)
			sort.Ints(depths)
			for _, d := range depths {
				cells = append(cells, domain.Coordinate{Zone: z, Layer: l, Depth: d})
			}
		}
	}
	return cells
}

// Validate checks a supplied set of cells against the spec. It rejects cells
// outside the boundary, missing cells (gaps), and duplicate coordinates, always
// returning a domain error whose reasons are deterministically sorted.
func (g GridSpec) Validate(cells []domain.Coordinate) error {
	seen := make(map[domain.Coordinate]bool, len(cells))
	var outOfBounds, dupes []domain.Coordinate

	valid := make(map[domain.Coordinate]bool)
	for _, c := range g.Generate() {
		valid[c] = true
	}

	for _, c := range cells {
		if !valid[c] {
			outOfBounds = append(outOfBounds, c)
			continue
		}
		if seen[c] {
			dupes = append(dupes, c)
			continue
		}
		seen[c] = true
	}

	if len(outOfBounds) > 0 || len(dupes) > 0 {
		reasons := make([]domain.Reason, 0, len(outOfBounds)+len(dupes))
		for _, c := range outOfBounds {
			reasons = append(reasons, reasonFor(c, "out_of_bounds"))
		}
		for _, c := range dupes {
			reasons = append(reasons, reasonFor(c, "duplicate_coordinate"))
		}
		return &domain.Error{
			Code:    domain.CodeGridOverlap,
			Message: "sampling grid contains out-of-bound or duplicate coordinates",
			Reasons: reasons,
		}
	}

	var gaps []domain.Coordinate
	for _, c := range g.Generate() {
		if !seen[c] {
			gaps = append(gaps, c)
		}
	}
	if len(gaps) > 0 {
		reasons := make([]domain.Reason, 0, len(gaps))
		for _, c := range gaps {
			reasons = append(reasons, reasonFor(c, "missing_cell"))
		}
		return &domain.Error{
			Code:    domain.CodeGridGap,
			Message: "sampling grid is missing required cells",
			Reasons: reasons,
		}
	}
	return nil
}

// DepthInterval is one contiguous coring-depth range claimed by a cell. Two
// intervals within the same zone and layer must not overlap.
type DepthInterval struct {
	Zone  string
	Layer int
	From  int // inclusive
	To    int // inclusive
}

// Overlaps reports whether two depth intervals intersect.
func (d DepthInterval) Overlaps(o DepthInterval) bool {
	return d.Zone == o.Zone && d.Layer == o.Layer && d.From <= o.To && o.From <= d.To
}

// ValidateDepthIntervals rejects any pair of overlapping depth intervals within
// the same zone and layer, returning a GRID_OVERLAP error with sorted reasons.
func ValidateDepthIntervals(intervals []DepthInterval) error {
	var overlaps []domain.Reason
	for i := 0; i < len(intervals); i++ {
		for j := i + 1; j < len(intervals); j++ {
			if intervals[i].Overlaps(intervals[j]) {
				overlaps = append(overlaps, domain.Reason{
					Zone:  intervals[i].Zone,
					Layer: intervals[i].Layer,
					Constraint: fmt.Sprintf("depth_interval_overlap [%d,%d] vs [%d,%d]",
						intervals[i].From, intervals[i].To, intervals[j].From, intervals[j].To),
				})
			}
		}
	}
	if len(overlaps) > 0 {
		return &domain.Error{
			Code:    domain.CodeGridOverlap,
			Message: "sampling depth intervals overlap",
			Reasons: overlaps,
		}
	}
	return nil
}

func reasonFor(c domain.Coordinate, constraint string) domain.Reason {
	return domain.Reason{
		Zone:       c.Zone,
		Layer:      c.Layer,
		Depth:      c.Depth,
		Constraint: constraint,
	}
}
