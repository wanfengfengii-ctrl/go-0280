package arbitration

import (
	"errors"
	"testing"

	"silage/internal/domain"
)

func codeOf(t *testing.T, err error) domain.StableCode {
	t.Helper()
	var de *domain.Error
	if !errors.As(err, &de) {
		t.Fatalf("expected *domain.Error, got %T", err)
	}
	return de.Code
}

func TestComputeExpansionAdjacentAndHarvest(t *testing.T) {
	adjacency := map[string][]string{
		"A:1": {"A:2"},
		"A:2": {"A:1"},
	}
	cells := []Cell{
		{Coordinate: domain.Coordinate{Zone: "A", Layer: 1, Depth: 0}, HarvestBatch: "b1"},
		{Coordinate: domain.Coordinate{Zone: "A", Layer: 1, Depth: 1}, HarvestBatch: "b1"},
		{Coordinate: domain.Coordinate{Zone: "A", Layer: 2, Depth: 0}, HarvestBatch: "b2"},
		{Coordinate: domain.Coordinate{Zone: "A", Layer: 2, Depth: 1}, HarvestBatch: "b2"},
	}
	anomaly := []domain.Coordinate{{Zone: "A", Layer: 1, Depth: 0}}
	got := ComputeExpansion(anomaly, cells, adjacency)

	// Adjacent layer (A:2) plus same harvest batch (b1 -> the other A:1 cell).
	// Deduplicated and sorted: A:1 depth1 (same batch), A:2 depth0, A:2 depth1.
	want := []domain.Coordinate{
		{Zone: "A", Layer: 1, Depth: 1},
		{Zone: "A", Layer: 2, Depth: 0},
		{Zone: "A", Layer: 2, Depth: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d coords %+v, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("coord[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestVentilationValid(t *testing.T) {
	rule := VentilationRule{MaxInterval: 10, OxygenMin: 180, HydrogenMax: 5}
	w := VentilationWindow{
		OpenFace: "f1",
		Readings: []GasReading{
			{At: 100, Oxygen: 200, H2S: 2},
			{At: 105, Oxygen: 195, H2S: 3},
			{At: 110, Oxygen: 190, H2S: 4},
		},
	}
	if err := ValidateVentilation(w, rule); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVentilationOxygenBelowMin(t *testing.T) {
	rule := VentilationRule{MaxInterval: 10, OxygenMin: 180, HydrogenMax: 5}
	w := VentilationWindow{
		Readings: []GasReading{{At: 100, Oxygen: 150, H2S: 2}},
	}
	if err := ValidateVentilation(w, rule); err == nil {
		t.Fatal("expected error")
	} else if codeOf(t, err) != domain.CodeReadingStale {
		t.Fatalf("got code %s", codeOf(t, err))
	}
}

func TestVentilationIntervalTooLarge(t *testing.T) {
	rule := VentilationRule{MaxInterval: 5, OxygenMin: 180, HydrogenMax: 5}
	w := VentilationWindow{
		Readings: []GasReading{
			{At: 100, Oxygen: 200, H2S: 2},
			{At: 120, Oxygen: 200, H2S: 2},
		},
	}
	if err := ValidateVentilation(w, rule); err == nil {
		t.Fatal("expected interval error")
	}
}

func TestValidateReviewsQuorum(t *testing.T) {
	if err := ValidateReviews([]ReviewOpinion{{ReviewerID: "r1", Qualified: true}}); err == nil {
		t.Fatal("expected quorum error for one reviewer")
	}
	if err := ValidateReviews([]ReviewOpinion{
		{ReviewerID: "r1", Qualified: true},
		{ReviewerID: "r1", Qualified: true},
	}); err == nil {
		t.Fatal("expected quorum error for duplicate reviewer")
	}
	if err := ValidateReviews([]ReviewOpinion{
		{ReviewerID: "r1", Qualified: true},
		{ReviewerID: "r2", Qualified: true},
	}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWinnerOf(t *testing.T) {
	for _, c := range []string{"open", "feed_isolate", "cancel"} {
		if _, ok := WinnerOf(c); !ok {
			t.Fatalf("command %q should be valid", c)
		}
	}
	if _, ok := WinnerOf("bogus"); ok {
		t.Fatal("bogus command should be invalid")
	}
}
