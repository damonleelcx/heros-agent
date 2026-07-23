package typedcontract

import (
	"bytes"
	"encoding/json"
	"sort"
)

// MarshalCompact renders a schema map as compact, key-sorted JSON — deterministic bytes suitable for a
// generated-source comment or a content hash. json.Marshal already sorts map keys, so this is stable.
func MarshalCompact(v map[string]any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "{}"
	}
	return string(bytes.TrimSpace(buf.Bytes()))
}

// SortedKeys returns a map's keys sorted — a small shared helper for deterministic iteration.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// mustJSON marshals a schema map to a string. The input is always a decoded JSON object (a
// `map[string]any` read from the IR), so marshalling it back cannot fail on a type json cannot
// represent; the error is swallowed deliberately and would only ever surface a programmer error.
func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
