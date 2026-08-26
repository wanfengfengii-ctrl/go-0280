package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// CanonicalDigest returns a deterministic SHA-256 hex digest of the JSON
// encoding of v. Object keys are marshalled in sorted order so structurally
// equal values always produce byte-identical digests regardless of map
// iteration order. It is used for idempotency request/response digests and for
// the immutable rule summaries frozen at task lock time.
func CanonicalDigest(v any) string {
	b, err := marshalCanonical(v)
	if err != nil {
		// Values handed to CanonicalDigest are always JSON-serializable domain
		// structs; a failure here is a programming error, not a runtime input.
		panic("domain: canonical digest marshal: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// marshalCanonical encodes v to JSON with deterministic object key ordering.
func marshalCanonical(v any) ([]byte, error) {
	return marshalValue(v)
}

func marshalValue(v any) ([]byte, error) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		buf := []byte{'{'}
		for i, k := range keys {
			if i > 0 {
				buf = append(buf, ',')
			}
			kb, _ := json.Marshal(k)
			buf = append(buf, kb...)
			buf = append(buf, ':')
			vb, err := marshalValue(t[k])
			if err != nil {
				return nil, err
			}
			buf = append(buf, vb...)
		}
		return append(buf, '}'), nil
	default:
		return json.Marshal(v)
	}
}
