package task

import "testing"

func TestStatusIsFinal(t *testing.T) {
	final := []Status{StatusOpened, StatusFeedIsolated, StatusCancelled}
	open := []Status{
		StatusPendingLock, StatusFilmCheck, StatusCoring, StatusSealing,
		StatusFermenting, StatusExpanding, StatusVentilating,
		StatusPendingReview, StatusOpenable,
	}
	for _, s := range final {
		if !s.IsFinal() {
			t.Fatalf("%s should be final", s)
		}
	}
	for _, s := range open {
		if s.IsFinal() {
			t.Fatalf("%s should not be final", s)
		}
	}
}
