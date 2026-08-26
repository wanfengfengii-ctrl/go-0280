package evidence

import (
	"fmt"

	"silage/internal/domain"
)

// Split is one partition of an original core's mass into a test sample, a
// retained sample and an allowed loss. All quantities are non-negative integer
// fixed-point values sharing the snapshot's declared scale.
type Split struct {
	Test     int64
	Retained int64
	Loss     int64
}

// ValidateSplit enforces mass conservation: the three partitions must be
// non-negative and their sum must exactly equal the original core mass. A
// negative value, an off-by-one minimum unit, or an overflowed sum all yield a
// MASS_NOT_CONSERVED error so the coverage state never advances.
func ValidateSplit(coreMass int64, s Split) error {
	if coreMass < 0 || s.Test < 0 || s.Retained < 0 || s.Loss < 0 {
		return &domain.Error{
			Code:    domain.CodeMassNotConserved,
			Message: "core mass and split parts must be non-negative",
			Reasons: []domain.Reason{{Constraint: "negative_mass"}},
		}
	}
	sum, err := safeAdd3(s.Test, s.Retained, s.Loss)
	if err != nil {
		return &domain.Error{
			Code:    domain.CodeMassNotConserved,
			Message: "split mass sum overflow",
			Reasons: []domain.Reason{{Constraint: "mass_overflow"}},
		}
	}
	if sum != coreMass {
		return &domain.Error{
			Code:    domain.CodeMassNotConserved,
			Message: "core mass is not conserved by the split",
			Reasons: []domain.Reason{{Constraint: fmt.Sprintf(
				"core=%d test=%d retained=%d loss=%d sum=%d",
				coreMass, s.Test, s.Retained, s.Loss, sum)}},
		}
	}
	return nil
}

func safeAdd3(a, b, c int64) (int64, error) {
	s, err := safeAdd(a, b)
	if err != nil {
		return 0, err
	}
	return safeAdd(s, c)
}

func safeAdd(a, b int64) (int64, error) {
	if (b > 0 && a > int64Max()-b) || (b < 0 && a < int64Min()-b) {
		return 0, fmt.Errorf("overflow")
	}
	return a + b, nil
}

// ChainValidator enforces the append-only custody chain: a transfer may only
// append a complete hand-off record; it can never split, rewrite or skip a
// holder. The chain is valid when transfers for a seal are ordered by sequence
// and each transfer's "from" equals the previous transfer's "to".
type ChainValidator struct {
	// holders tracks the current holder per seal id.
	holders map[string]string
	// seq tracks the last applied sequence per seal id.
	seq map[string]int
	// started tracks the originating holder per seal id.
	started map[string]string
}

// NewChainValidator builds an empty validator.
func NewChainValidator() *ChainValidator {
	return &ChainValidator{
		holders: map[string]string{},
		seq:     map[string]int{},
		started: map[string]string{},
	}
}

// Begin records the originating holder of a seal before any transfer exists.
func (c *ChainValidator) Begin(sealID, holder string) error {
	if _, ok := c.started[sealID]; ok {
		return &domain.Error{
			Code:    domain.CodeBlindCodeEarly,
			Message: "seal chain already started",
			Reasons: []domain.Reason{{BlindCode: sealID, Constraint: "chain_already_started"}},
		}
	}
	c.started[sealID] = holder
	c.holders[sealID] = holder
	c.seq[sealID] = 0
	return nil
}

// Append applies one transfer, enforcing strict sequence ordering and holder
// continuity. It returns a BLIND_CODE_EARLY error on any broken link.
func (c *ChainValidator) Append(t SealTransfer) error {
	cur, ok := c.holders[t.SealID]
	if !ok {
		return &domain.Error{
			Code:    domain.CodeBlindCodeEarly,
			Message: "seal chain has not been started",
			Reasons: []domain.Reason{{BlindCode: t.SealID, Constraint: "chain_not_started"}},
		}
	}
	if t.Seq != c.seq[t.SealID]+1 {
		return &domain.Error{
			Code:    domain.CodeBlindCodeEarly,
			Message: "seal transfer sequence out of order",
			Reasons: []domain.Reason{{BlindCode: t.SealID, Constraint: "sequence_gap"}},
		}
	}
	if t.From != cur {
		return &domain.Error{
			Code:    domain.CodeBlindCodeEarly,
			Message: "seal transfer breaks the custody chain",
			Reasons: []domain.Reason{{BlindCode: t.SealID, Constraint: "holder_mismatch"}},
		}
	}
	c.holders[t.SealID] = t.To
	c.seq[t.SealID] = t.Seq
	return nil
}
