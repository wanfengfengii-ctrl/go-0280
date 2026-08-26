package evidence

import (
	"errors"
	"testing"

	"silage/internal/domain"
)

func codeOf(t *testing.T, err error) domain.StableCode {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("expected *domain.Error, got %T (%v)", err, err)
	}
	return de.Code
}

func TestNewFixedPointRejectsNonPositiveScale(t *testing.T) {
	for _, scale := range []int64{0, -1} {
		if _, err := NewFixedPoint(10, scale); err == nil {
			t.Fatalf("scale %d should be rejected", scale)
		} else if codeOf(t, err) != domain.CodeFixedPointOverflow {
			t.Fatalf("scale %d: got code %s", scale, codeOf(t, err))
		}
	}
}

func TestFixedPointAddOverflow(t *testing.T) {
	a := FixedPoint{Value: 1<<63 - 1, Scale: 1}
	if _, err := a.Add(FixedPoint{Value: 1, Scale: 1}); err == nil {
		t.Fatal("expected overflow")
	} else if codeOf(t, err) != domain.CodeFixedPointOverflow {
		t.Fatalf("got code %s", codeOf(t, err))
	}
}

func TestFixedPointScaleMismatch(t *testing.T) {
	a := FixedPoint{Value: 5, Scale: 10}
	if _, err := a.Add(FixedPoint{Value: 5, Scale: 100}); err == nil {
		t.Fatal("expected scale mismatch")
	}
}

func TestFixedPointMulOverflow(t *testing.T) {
	a := FixedPoint{Value: 1 << 62, Scale: 1}
	if _, err := a.Mul(4); err == nil {
		t.Fatal("expected multiplication overflow")
	} else if codeOf(t, err) != domain.CodeFixedPointOverflow {
		t.Fatalf("got code %s", codeOf(t, err))
	}
}

func TestFixedPointDivByZero(t *testing.T) {
	a := FixedPoint{Value: 10, Scale: 1}
	if _, err := a.Div(0); err == nil {
		t.Fatal("expected division by zero")
	} else if codeOf(t, err) != domain.CodeFixedPointOverflow {
		t.Fatalf("got code %s", codeOf(t, err))
	}
}

func TestFixedPointAddMulDivExact(t *testing.T) {
	a := FixedPoint{Value: 100, Scale: 10} // 10.0
	sum, err := a.Add(FixedPoint{Value: 50, Scale: 10})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if sum.Value != 150 {
		t.Fatalf("sum = %d, want 150", sum.Value)
	}
	prod, err := a.Mul(3)
	if err != nil {
		t.Fatalf("mul: %v", err)
	}
	if prod.Value != 300 {
		t.Fatalf("prod = %d, want 300", prod.Value)
	}
	quot, err := a.Div(5)
	if err != nil {
		t.Fatalf("div: %v", err)
	}
	if quot.Value != 20 {
		t.Fatalf("quot = %d, want 20", quot.Value)
	}
}

func TestFixedPointNonNegative(t *testing.T) {
	if !(FixedPoint{Value: 0, Scale: 1}).NonNegative() {
		t.Fatal("0 should be non-negative")
	}
	if (FixedPoint{Value: -1, Scale: 1}).NonNegative() {
		t.Fatal("-1 should be negative")
	}
}
