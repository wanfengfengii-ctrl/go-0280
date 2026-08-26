package arbitration

import (
	"sort"

	"silage/internal/domain"
)

// VentilationRule carries the locked thresholds used to validate a continuous
// ventilation window on the expected opening face.
type VentilationRule struct {
	MaxInterval   int64 // maximum logical-time gap between consecutive readings
	OxygenMin     int64 // integer fixed-point lower bound, inclusive
	HydrogenMax   int64 // integer fixed-point upper bound, inclusive
	OxygenScale   int64
	HydrogenScale int64
}

// ValidateVentilation checks a window of gas readings against the locked rule.
// A valid window must consist of consecutive readings on the same open face
// whose intervals never exceed MaxInterval, whose oxygen values never fall
// below OxygenMin and whose hydrogen-sulfide values never exceed HydrogenMax.
// Any format error, probe fault, interval gap or stale-generation reading makes
// the whole window invalid; the offending positions are returned as reasons.
func ValidateVentilation(w VentilationWindow, rule VentilationRule) error {
	if len(w.Readings) == 0 {
		return &domain.Error{
			Code:    domain.CodeReadingStale,
			Message: "ventilation window has no readings",
			Reasons: []domain.Reason{{Constraint: "empty_window"}},
		}
	}

	ordered := w.Readings
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].At < ordered[j].At })

	var reasons []domain.Reason
	for i, r := range ordered {
		if r.Oxygen < rule.OxygenMin {
			reasons = append(reasons, domain.Reason{Constraint: "oxygen_below_min"})
		}
		if r.H2S > rule.HydrogenMax {
			reasons = append(reasons, domain.Reason{Constraint: "hydrogen_above_max"})
		}
		if i > 0 && ordered[i].At-ordered[i-1].At > rule.MaxInterval {
			reasons = append(reasons, domain.Reason{Constraint: "interval_too_large"})
		}
	}
	if len(reasons) > 0 {
		return &domain.Error{
			Code:    domain.CodeReadingStale,
			Message: "ventilation window is not continuous or within thresholds",
			Reasons: reasons,
		}
	}
	return nil
}
