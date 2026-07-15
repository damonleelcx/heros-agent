// Package confighash computes config_hash: the immutable, content-defined identifier that makes every
// result reproducible and attributable (P0 task 2.4; spec: docs/decisions/config-hash-spec.md).
//
//	config_hash = SHA-256( canonical_json( resolved_config ) )
//
// This is the Go REFERENCE IMPLEMENTATION and the home of the golden-vector regression test. The
// live producer is the P2 Config Layer; it MUST reproduce these vectors bit-for-bit.
//
// Canonicalization scope. Canonicalize implements a subset of RFC 8785 (JSON Canonicalization Scheme)
// sufficient for the resolved_config value domain: recursively sorted object keys, no insignificant
// whitespace, UTF-8, HTML escaping OFF, and NUMBERS PRESERVED VERBATIM via json.Number (resolved_config
// numbers are machine-emitted already in shortest form: 0, 0.2, 256, 1024, ...). Full RFC 8785 numeric
// normalization (e.g. 1.0 -> "1", 1e2 -> "100") is a P2 hardening item and is called out, not hidden:
// see ErrNonCanonicalNumber. This keeps the P0 contract honest rather than silently mis-hashing an
// exotic number.
package confighash

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// ErrNonCanonicalNumber is returned when a number's source token is not already in the canonical
// shortest form this subset preserves (e.g. "1.0", "1e2", "+1"). Fail loud instead of hashing an
// ambiguous representation. The P2 implementation replaces this subset with full RFC 8785.
var ErrNonCanonicalNumber = errors.New("confighash: non-canonical number token; needs full RFC 8785 normalization")

// CanonicalizeBytes parses raw JSON and returns its canonical serialization.
func CanonicalizeBytes(raw []byte) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("confighash: decode: %w", err)
	}
	var out bytes.Buffer
	if err := writeCanonical(&out, v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// Canonicalize returns the canonical serialization of an already-decoded value. The value should have
// been decoded with json.Number for numbers (use CanonicalizeBytes if starting from raw JSON).
func Canonicalize(v any) ([]byte, error) {
	var out bytes.Buffer
	if err := writeCanonical(&out, v); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// SumBytes returns the config_hash (lowercase hex, full 64 chars) of raw resolved_config JSON.
func SumBytes(raw []byte) (string, error) {
	c, err := CanonicalizeBytes(raw)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c)
	return hex.EncodeToString(sum[:]), nil
}

// Sum returns the config_hash of an already-decoded resolved_config value.
func Sum(v any) (string, error) {
	c, err := Canonicalize(v)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c)
	return hex.EncodeToString(sum[:]), nil
}

// Display returns the 12-char display prefix (UI only; never stored — spec §2 / OQ2).
func Display(hash string) string {
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func writeCanonical(buf *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		buf.WriteString("null")
	case bool:
		if t {
			buf.WriteString("true")
		} else {
			buf.WriteString("false")
		}
	case string:
		writeJSONString(buf, t)
	case json.Number:
		if !isCanonicalNumber(t.String()) {
			return fmt.Errorf("%w: %q", ErrNonCanonicalNumber, t.String())
		}
		buf.WriteString(t.String())
	case float64:
		// Only reachable if a caller decoded without UseNumber; keep it correct for integers/short
		// decimals via Go's shortest-round-trip formatting, which matches ECMAScript for this domain.
		b, _ := json.Marshal(t)
		buf.Write(b)
	case []any:
		buf.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				buf.WriteByte(',')
			}
			if err := writeCanonical(buf, e); err != nil {
				return err
			}
		}
		buf.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys) // byte-wise sort; equals RFC 8785 UTF-16 order for the ASCII key domain
		buf.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				buf.WriteByte(',')
			}
			writeJSONString(buf, k)
			buf.WriteByte(':')
			if err := writeCanonical(buf, t[k]); err != nil {
				return err
			}
		}
		buf.WriteByte('}')
	default:
		return fmt.Errorf("confighash: unsupported type %T", v)
	}
	return nil
}

// writeJSONString writes a minimally-escaped JSON string with HTML escaping OFF.
func writeJSONString(buf *bytes.Buffer, s string) {
	b, _ := json.Marshal(s) // handles required escapes and UTF-8 correctly
	// json.Marshal HTML-escapes <, >, & as < etc.; undo for canonical (RFC 8785) output.
	out := string(b)
	out = strings.ReplaceAll(out, `<`, "<")
	out = strings.ReplaceAll(out, `>`, ">")
	out = strings.ReplaceAll(out, `&`, "&")
	buf.WriteString(out)
}

// isCanonicalNumber reports whether tok is already in the shortest round-tripping form this subset
// preserves: an optional leading '-', integer part with no superfluous leading zero, an optional
// fractional part, and NO exponent / '+' / trailing zeros ambiguity.
func isCanonicalNumber(tok string) bool {
	if tok == "" {
		return false
	}
	s := tok
	if s[0] == '-' {
		s = s[1:]
	}
	if s == "" {
		return false
	}
	// no exponent form accepted in this subset
	if strings.ContainsAny(s, "eE+") {
		return false
	}
	intPart := s
	fracPart := ""
	if dot := strings.IndexByte(s, '.'); dot >= 0 {
		intPart = s[:dot]
		fracPart = s[dot+1:]
		if fracPart == "" { // "1." not allowed
			return false
		}
		if fracPart[len(fracPart)-1] == '0' { // trailing zero, e.g. "0.20"
			return false
		}
	}
	if intPart == "" {
		return false
	}
	// no leading zero unless the int part is exactly "0"
	if len(intPart) > 1 && intPart[0] == '0' {
		return false
	}
	for _, c := range intPart + fracPart {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
