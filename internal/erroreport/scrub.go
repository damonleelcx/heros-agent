package erroreport

import (
	"sort"
	"strconv"
	"strings"

	"github.com/heros-foreal/agentd/internal/metricevent"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// Scrub runs the platform's EXISTING scrubbing chokepoint over a constructed event, as an independent
// second guard (design D5, task 2.3).
//
// # Why a second guard at all, when the first one is construction
//
// The two guards are of DIFFERENT KINDS, which is the only arrangement worth having. Construction
// answers "which fields exist"; scrubbing answers "does a permitted field contain something it should
// not". A bug in the first — a call site that puts a formatted string into `Surface`, a frame whose
// file path was not trimmed on some platform — is invisible to itself and visible to the second. The
// same pairing is already used on the credential surface, where `server-only` and `scan-bundle.mjs`
// catch each other's misses.
//
// # Why `telemetry.Scrubber` and not a scrubber of our own
//
// Because a second implementation of "what does a secret look like" is a second truth source, and the
// day they disagree neither is authoritative. `internal/telemetry/scrub.go` already holds the pattern
// list, already runs on every span and metric event, and is already the place a new pattern is added.
// Reaching it through `ScrubEvent`'s `Dimensions` map is what lets this boundary inherit that list for
// free — including patterns added for reasons that have nothing to do with error reporting.
//
// # Why the event is FLATTENED first
//
// `scrubValue` inspects string values and passes everything else through untouched, which means a
// string nested inside `frames[]` would never be looked at. Flattening to dotted keys puts every string
// in the event in front of the chokepoint. A guard that silently skips half its input is worse than no
// guard, because the half it skips is the half nobody checks.
func Scrub(sc telemetry.Scrubber, wire map[string]any) map[string]any {
	if sc == nil {
		return wire
	}
	flat := map[string]any{}
	flatten("", wire, flat)
	scrubbed := sc.ScrubEvent(metricevent.Event{Dimensions: flat}).Dimensions
	return unflatten(scrubbed).(map[string]any)
}

func flatten(prefix string, v any, out map[string]any) {
	switch t := v.(type) {
	case map[string]any:
		// An EMPTY collection is carried across as itself. Flattening it to nothing would delete the key,
		// and a key that vanishes when its value is empty is how "every allowlist entry is populated by
		// something" quietly becomes "every allowlist entry that happened to be non-empty".
		if len(t) == 0 {
			out[prefix] = t
			return
		}
		for k, child := range t {
			flatten(join(prefix, k), child, out)
		}
	case []any:
		if len(t) == 0 {
			out[prefix] = t
			return
		}
		for i, child := range t {
			flatten(join(prefix, strconv.Itoa(i)), child, out)
		}
	default:
		out[prefix] = v
	}
}

func join(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "\x00" + key
}

// unflatten rebuilds the nested shape. Numeric path segments rebuild a slice, everything else a map.
func unflatten(flat map[string]any) any {
	root := map[string]any{}
	keys := make([]string, 0, len(flat))
	for k := range flat {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts := strings.Split(k, "\x00")
		cur := root
		for i, part := range parts {
			if i == len(parts)-1 {
				cur[part] = flat[k]
				continue
			}
			next, ok := cur[part].(map[string]any)
			if !ok {
				next = map[string]any{}
				cur[part] = next
			}
			cur = next
		}
	}
	return listify(root)
}

// listify turns a map whose keys are exactly 0..n-1 back into a slice, so `frames` comes out of the
// chokepoint the shape it went in.
func listify(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for k, child := range m {
		m[k] = listify(child)
	}
	if len(m) == 0 {
		return m
	}
	out := make([]any, len(m))
	for k := range m {
		idx, err := strconv.Atoi(k)
		if err != nil || idx < 0 || idx >= len(m) {
			return m
		}
		out[idx] = m[k]
	}
	return out
}
