package domain

import "testing"

func TestAllStableCodesComplete(t *testing.T) {
	if len(AllStableCodes) != 15 {
		t.Fatalf("got %d stable codes, want 15", len(AllStableCodes))
	}
	seen := map[StableCode]bool{}
	for _, c := range AllStableCodes {
		if c == "" {
			t.Fatal("empty stable code")
		}
		if seen[c] {
			t.Fatalf("duplicate stable code %q", c)
		}
		seen[c] = true
	}
}
