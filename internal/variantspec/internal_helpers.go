package variantspec

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"

	"github.com/heros-foreal/agentd/internal/confighash"
)

// jsonMarshal marshals with HTML escaping OFF.
//
// This matters more than it looks. encoding/json's default escapes <, >, and & into < etc., but
// RFC 8785 (and therefore config_hash) does not. A prompt name or model id containing an ampersand
// would hash differently here than in any other conformant implementation — a silent, data-dependent
// fork that would only ever show up on the one entry that happened to contain the character.
// internal/confighash strips those escapes back out on its own output for the same reason; not
// emitting them in the first place keeps the two from having to agree twice.
func jsonMarshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encode appends a newline; canonical JSON has no trailing whitespace.
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func bytesReader(b []byte) io.Reader { return bytes.NewReader(b) }

func containsBytes(haystack []byte, needle string) bool {
	return bytes.Contains(haystack, []byte(needle))
}

// decodeParams decodes a context entry's params blob into the free-form map resolved_config carries.
// A non-object is an error rather than a coerced empty map: params that silently vanish would change
// what the policy does while leaving config_hash claiming otherwise.
func decodeParams(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	return out, nil
}

func jsonUnmarshal(b []byte, v any) error { return json.Unmarshal(b, v) }

// canonicalizeBytes re-canonicalizes stored JSON. Thin wrapper so store.go does not import
// confighash directly — resolved.go owns the choice of canonicalizer, and there must be exactly one.
func canonicalizeBytes(raw []byte) ([]byte, error) { return confighash.CanonicalizeBytes(raw) }
