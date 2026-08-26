package catalog

import (
	"errors"
	"testing"

	"silage/internal/domain"
)

type reg map[string]string

func (r reg) Plot(id string) (Plot, bool) {
	b, ok := r[id]
	return Plot{ID: id, HarvestBatchID: b}, ok
}

func snap() CatalogSnapshot {
	return CatalogSnapshot{
		ID: "c1", PlotID: "p1", HarvestBatchID: "b1",
		ChopLengthRule: ChopLengthRule{MinMM: 5, MaxMM: 20},
		Thresholds:     Thresholds{Scale: 100},
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

func TestValidateLinksBatchMismatch(t *testing.T) {
	s := snap()
	s.HarvestBatchID = "b2" // plot p1 is linked to b1
	err := ValidateLinks(s, reg{"p1": "b1"})
	if err == nil {
		t.Fatal("expected batch mismatch")
	}
	if codeOf(t, err) != domain.CodeBatchMismatch {
		t.Fatalf("got code %s, want %s", codeOf(t, err), domain.CodeBatchMismatch)
	}
}

func TestValidateLinksUnknownPlot(t *testing.T) {
	s := snap()
	err := ValidateLinks(s, reg{})
	if err == nil {
		t.Fatal("expected unknown plot error")
	}
	if codeOf(t, err) != domain.CodeBatchMismatch {
		t.Fatalf("got code %s", codeOf(t, err))
	}
}

func TestValidateLinksChopLengthInvalid(t *testing.T) {
	s := snap()
	s.ChopLengthRule.MinMM = 50
	s.ChopLengthRule.MaxMM = 20
	if err := ValidateLinks(s, nil); err == nil {
		t.Fatal("expected chop-length error")
	} else if codeOf(t, err) != domain.CodeStaleRuleDigest {
		t.Fatalf("got code %s", codeOf(t, err))
	}
}

func TestValidateLinksOK(t *testing.T) {
	if err := ValidateLinks(snap(), reg{"p1": "b1"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRuleDigestStable(t *testing.T) {
	a := snap()
	b := snap()
	if RuleDigest(a) != RuleDigest(b) {
		t.Fatal("identical snapshots must have identical digests")
	}
	b.Thresholds.OxygenMin = 1
	if RuleDigest(a) == RuleDigest(b) {
		t.Fatal("different thresholds must yield different digests")
	}
}
