package evidence

import "silage/internal/domain"

// FixedPoint performs integer fixed-point arithmetic with explicit overflow and
// division-by-zero checks. All masses and physicochemical values are non-negative
// integer fixed-point values scaled by a rule-declared factor.
type FixedPoint struct {
	Value int64
	Scale int64
}

// NewFixedPoint builds a fixed-point value after validating the scale is positive.
func NewFixedPoint(value, scale int64) (FixedPoint, error) {
	if scale <= 0 {
		return FixedPoint{}, &domain.Error{
			Code:    domain.CodeFixedPointOverflow,
			Message: "fixed-point scale must be positive",
		}
	}
	return FixedPoint{Value: value, Scale: scale}, nil
}

// Add sums two values that share a scale, rejecting overflow.
func (f FixedPoint) Add(o FixedPoint) (FixedPoint, error) {
	if f.Scale != o.Scale {
		return FixedPoint{}, &domain.Error{
			Code:    domain.CodeFixedPointOverflow,
			Message: "fixed-point scale mismatch",
		}
	}
	return f.scaledAdd(o.Value)
}

func (f FixedPoint) scaledAdd(v int64) (FixedPoint, error) {
	if (v > 0 && f.Value > int64Max()-v) || (v < 0 && f.Value < int64Min()-v) {
		return FixedPoint{}, &domain.Error{
			Code:    domain.CodeFixedPointOverflow,
			Message: "fixed-point addition overflow",
		}
	}
	return FixedPoint{Value: f.Value + v, Scale: f.Scale}, nil
}

// Mul multiplies by an integer factor, rejecting overflow. Multiplication is
// used for ratio and concentration computations and must be checked.
func (f FixedPoint) Mul(factor int64) (FixedPoint, error) {
	if factor == 0 {
		return FixedPoint{Value: 0, Scale: f.Scale}, nil
	}
	r := f.Value * factor
	if r/factor != f.Value {
		return FixedPoint{}, &domain.Error{
			Code:    domain.CodeFixedPointOverflow,
			Message: "fixed-point multiplication overflow",
		}
	}
	return FixedPoint{Value: r, Scale: f.Scale}, nil
}

// Div divides by an integer divisor, rejecting a zero divisor.
func (f FixedPoint) Div(divisor int64) (FixedPoint, error) {
	if divisor == 0 {
		return FixedPoint{}, &domain.Error{
			Code:    domain.CodeFixedPointOverflow,
			Message: "fixed-point division by zero",
		}
	}
	return FixedPoint{Value: f.Value / divisor, Scale: f.Scale}, nil
}

// NonNegative reports whether the value is non-negative.
func (f FixedPoint) NonNegative() bool { return f.Value >= 0 }

func int64Max() int64 { return 1<<63 - 1 }
func int64Min() int64 { return -1 << 63 }

// RoundMode selects the deterministic rounding used when scaling a ratio.
type RoundMode int

const (
	// RoundDown truncates toward zero.
	RoundDown RoundMode = iota
	// RoundUp rounds away from zero on any remainder.
	RoundUp
	// RoundHalfUp rounds to the nearest, halves away from zero.
	RoundHalfUp
)

// Ratio computes (numerator * scale) / denominator using integer fixed-point
// arithmetic. It validates a positive denominator, checks multiplication for
// overflow, and applies the requested deterministic rounding so repeated calls
// never drift via floating-point error. A zero denominator returns
// FIXED_POINT_OVERFLOW.
func Ratio(numerator, denominator, scale int64, mode RoundMode) (int64, error) {
	if denominator == 0 {
		return 0, &domain.Error{
			Code:    domain.CodeFixedPointOverflow,
			Message: "ratio denominator is zero",
		}
	}
	if scale <= 0 {
		return 0, &domain.Error{
			Code:    domain.CodeFixedPointOverflow,
			Message: "ratio scale must be positive",
		}
	}
	scaled := numerator * scale
	if scale != 0 && scaled/scale != numerator {
		return 0, &domain.Error{
			Code:    domain.CodeFixedPointOverflow,
			Message: "ratio multiplication overflow",
		}
	}
	q := scaled / denominator
	r := scaled % denominator
	if r != 0 {
		switch mode {
		case RoundUp:
			if q >= 0 {
				q++
			} else {
				q--
			}
		case RoundHalfUp:
			if r*2 >= denominator {
				if q >= 0 {
					q++
				} else {
					q--
				}
			}
		}
	}
	return q, nil
}

// CompareThreshold evaluates an integer fixed-point reading against a threshold
// using the requested comparison. It returns true when the comparison holds.
func CompareThreshold(value, threshold int64, op string) bool {
	switch op {
	case ">=":
		return value >= threshold
	case "<=":
		return value <= threshold
	case ">":
		return value > threshold
	case "<":
		return value < threshold
	default:
		return value == threshold
	}
}
