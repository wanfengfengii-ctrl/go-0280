package domain

import "testing"

func TestCoordinateLessTotalOrdering(t *testing.T) {
	cases := []struct {
		name string
		a, b Coordinate
		want bool
	}{
		{"zone asc", Coordinate{Zone: "A", Layer: 2, Depth: 1}, Coordinate{Zone: "B", Layer: 0, Depth: 0}, true},
		{"same zone layer asc", Coordinate{Zone: "A", Layer: 1, Depth: 9}, Coordinate{Zone: "A", Layer: 2, Depth: 0}, true},
		{"same zone layer depth asc", Coordinate{Zone: "A", Layer: 1, Depth: 1}, Coordinate{Zone: "A", Layer: 1, Depth: 2}, true},
		{"equal not less", Coordinate{Zone: "A", Layer: 1, Depth: 1}, Coordinate{Zone: "A", Layer: 1, Depth: 1}, false},
		{"greater not less", Coordinate{Zone: "B", Layer: 0, Depth: 0}, Coordinate{Zone: "A", Layer: 9, Depth: 9}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.a.Less(tc.b); got != tc.want {
				t.Fatalf("Less(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestSortReasonsDeterministic(t *testing.T) {
	rs := []Reason{
		{Zone: "B", Layer: 0, Depth: 0},
		{Zone: "A", Layer: 2, Depth: 0},
		{Zone: "A", Layer: 1, Depth: 5},
		{Zone: "A", Layer: 1, Depth: 2},
	}
	SortReasons(rs)
	want := []Reason{
		{Zone: "A", Layer: 1, Depth: 2},
		{Zone: "A", Layer: 1, Depth: 5},
		{Zone: "A", Layer: 2, Depth: 0},
		{Zone: "B", Layer: 0, Depth: 0},
	}
	for i := range want {
		if rs[i] != want[i] {
			t.Fatalf("reason[%d] = %+v, want %+v", i, rs[i], want[i])
		}
	}
}
