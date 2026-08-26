package evidence

import (
	"testing"

	"silage/internal/domain"
)

func TestValidateSplitExact(t *testing.T) {
	if err := ValidateSplit(100, Split{Test: 60, Retained: 30, Loss: 10}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSplitOffByOne(t *testing.T) {
	if err := ValidateSplit(100, Split{Test: 60, Retained: 30, Loss: 9}); err == nil {
		t.Fatal("expected off-by-one error")
	}
}

func TestValidateSplitNegative(t *testing.T) {
	if err := ValidateSplit(100, Split{Test: -1, Retained: 30, Loss: 71}); err == nil {
		t.Fatal("expected negative mass error")
	}
}

func TestValidateSplitOverflow(t *testing.T) {
	// Sum of the three parts overflows int64.
	err := ValidateSplit(0, Split{Test: 1 << 62, Retained: 1 << 62, Loss: 1 << 62})
	if err == nil {
		t.Fatal("expected overflow error")
	}
}

func TestChainValidatorAppendOnly(t *testing.T) {
	v := NewChainValidator()
	if err := v.Begin("s1", "op"); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := v.Append(SealTransfer{SealID: "s1", From: "op", To: "lab", Seq: 1}); err != nil {
		t.Fatalf("append: %v", err)
	}
	// A broken holder link is rejected.
	if err := v.Append(SealTransfer{SealID: "s1", From: "wrong", To: "lab2", Seq: 2}); err == nil {
		t.Fatal("expected holder mismatch")
	} else if de, ok := err.(*domain.Error); !ok || de.Code != domain.CodeBlindCodeEarly {
		t.Fatalf("got %v", err)
	}
	// A sequence gap is rejected.
	if err := v.Append(SealTransfer{SealID: "s1", From: "lab", To: "lab2", Seq: 5}); err == nil {
		t.Fatal("expected sequence gap")
	}
}

func TestChainValidatorNotStarted(t *testing.T) {
	v := NewChainValidator()
	if err := v.Append(SealTransfer{SealID: "s1", From: "op", To: "lab", Seq: 1}); err == nil {
		t.Fatal("expected not-started error")
	}
}

func TestClassifyFailures(t *testing.T) {
	if Classify(nil) != "" {
		t.Fatal("nil error should classify empty")
	}
	if Classify(ErrInstrumentRejected) != FailureRejected {
		t.Fatal("rejected")
	}
	if Classify(ErrInstrumentDisconnected) != FailureDisconn {
		t.Fatal("disconnected")
	}
	if Classify(ErrInstrumentTimeout) != FailureTimeout {
		t.Fatal("timeout")
	}
	if Classify(errorsNew("x")) != FailureMalformed {
		t.Fatal("malformed")
	}
}

func errorsNew(s string) error { return &strErr{s} }

type strErr struct{ s string }

func (e *strErr) Error() string { return e.s }
