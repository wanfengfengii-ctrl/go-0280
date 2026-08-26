package catalog

import (
	"fmt"

	"silage/internal/domain"
)

// Plot binds a planting plot to exactly one harvest batch. Task locking rejects
// a snapshot whose plot is linked to a different harvest batch than the one the
// operator supplied.
type Plot struct {
	ID             string
	HarvestBatchID string
}

// Registry is a read-only view of the planting plots used to verify the
// plot/harvest-batch relationship at lock time.
type Registry interface {
	// Plot returns the plot with the given id, or false if unknown.
	Plot(id string) (Plot, bool)
}

// ValidateLinks checks the cross-entity relationships captured in a snapshot:
// the referenced plot must exist, be linked to the stated harvest batch, and
// the chop-length rule must be internally consistent. Any failure returns a
// domain error so the whole lock command is rejected atomically.
func ValidateLinks(s CatalogSnapshot, reg Registry) error {
	if reg != nil {
		p, ok := reg.Plot(s.PlotID)
		if !ok {
			return &domain.Error{
				Code:    domain.CodeBatchMismatch,
				Message: "plot not found in catalog",
				Reasons: []domain.Reason{{Constraint: "plot_id=" + s.PlotID}},
			}
		}
		if p.HarvestBatchID != s.HarvestBatchID {
			return &domain.Error{
				Code:    domain.CodeBatchMismatch,
				Message: "plot is not linked to the stated harvest batch",
				Reasons: []domain.Reason{{
					Constraint: fmt.Sprintf("plot=%s batch=%s want=%s",
						s.PlotID, s.HarvestBatchID, p.HarvestBatchID),
				}},
			}
		}
	}
	if s.ChopLengthRule.MinMM > s.ChopLengthRule.MaxMM {
		return &domain.Error{
			Code:    domain.CodeStaleRuleDigest,
			Message: "chop-length rule min exceeds max",
			Reasons: []domain.Reason{{Constraint: "chop_length_rule"}},
		}
	}
	if s.Thresholds.Scale <= 0 {
		return &domain.Error{
			Code:    domain.CodeStaleRuleDigest,
			Message: "threshold scale must be positive",
			Reasons: []domain.Reason{{Constraint: "threshold_scale"}},
		}
	}
	return nil
}

// RuleDigest returns the deterministic digest of the rule-bearing fields of a
// snapshot: geometry, chop-length rule, inoculant summary, thresholds and the
// fermentation rule. The digest identifies a specific immutable rule version so
// a stale catalog version can be detected before a task is locked.
func RuleDigest(s CatalogSnapshot) string {
	return domain.CanonicalDigest(map[string]any{
		"geometry":     s.Geometry,
		"chop_length":  s.ChopLengthRule,
		"inoculant":    s.Inoculant,
		"open_face":    s.OpenFace,
		"thresholds":   s.Thresholds,
		"fermentation": s.Fermentation,
		"instruments":  s.Instruments,
		"zones":        s.Zones,
		"layers":       s.CompactionLayers,
		"adjacency":    s.Adjacency,
	})
}
