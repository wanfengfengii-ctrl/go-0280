package sampling

import (
	"errors"
	"testing"

	"silage/internal/domain"
)

func spec() GridSpec {
	return GridSpec{
		Zones:  []string{"A", "B"},
		Layers: map[string][]int{"A": {1, 2}, "B": {1}},
		Depths: []int{0, 1},
	}
}

func codeOf(t *testing.T, err error) domain.StableCode {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("expected *domain.Error, got %T", err)
	}
	return de.Code
}

func TestGridGenerateCompleteOrdered(t *testing.T) {
	cells := spec().Generate()
	want := []domain.Coordinate{
		{Zone: "A", Layer: 1, Depth: 0},
		{Zone: "A", Layer: 1, Depth: 1},
		{Zone: "A", Layer: 2, Depth: 0},
		{Zone: "A", Layer: 2, Depth: 1},
		{Zone: "B", Layer: 1, Depth: 0},
		{Zone: "B", Layer: 1, Depth: 1},
	}
	if len(cells) != len(want) {
		t.Fatalf("got %d cells, want %d", len(cells), len(want))
	}
	for i := range want {
		if cells[i] != want[i] {
			t.Fatalf("cell[%d] = %+v, want %+v", i, cells[i], want[i])
		}
	}
}

func TestGridValidateRejectsOutOfBounds(t *testing.T) {
	cells := spec().Generate()
	cells[0] = domain.Coordinate{Zone: "X", Layer: 1, Depth: 0}
	err := spec().Validate(cells)
	if err == nil {
		t.Fatal("expected out-of-bounds error")
	}
	if codeOf(t, err) != domain.CodeGridOverlap {
		t.Fatalf("got code %s, want %s", codeOf(t, err), domain.CodeGridOverlap)
	}
}

func TestGridValidateRejectsDuplicate(t *testing.T) {
	cells := spec().Generate()
	cells = append(cells, cells[0])
	err := spec().Validate(cells)
	if err == nil {
		t.Fatal("expected duplicate error")
	}
	if codeOf(t, err) != domain.CodeGridOverlap {
		t.Fatalf("got code %s, want %s", codeOf(t, err), domain.CodeGridOverlap)
	}
}

func TestGridValidateRejectsGap(t *testing.T) {
	cells := spec().Generate()
	cells = cells[1:] // drop the first cell
	err := spec().Validate(cells)
	if err == nil {
		t.Fatal("expected gap error")
	}
	if codeOf(t, err) != domain.CodeGridGap {
		t.Fatalf("got code %s, want %s", codeOf(t, err), domain.CodeGridGap)
	}
}

func TestValidateDepthIntervalsOverlap(t *testing.T) {
	intervals := []DepthInterval{
		{Zone: "A", Layer: 1, From: 0, To: 5},
		{Zone: "A", Layer: 1, From: 4, To: 9},
	}
	if err := ValidateDepthIntervals(intervals); err == nil {
		t.Fatal("expected overlap error")
	} else if codeOf(t, err) != domain.CodeGridOverlap {
		t.Fatalf("got code %s", codeOf(t, err))
	}
}

func TestValidateDepthIntervalsDisjoint(t *testing.T) {
	intervals := []DepthInterval{
		{Zone: "A", Layer: 1, From: 0, To: 3},
		{Zone: "A", Layer: 1, From: 4, To: 7},
		{Zone: "A", Layer: 2, From: 0, To: 3},
	}
	if err := ValidateDepthIntervals(intervals); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
