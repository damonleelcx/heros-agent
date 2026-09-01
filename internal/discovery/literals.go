package discovery

import "strings"

// literals.go answers one question as fast as it can be answered: which of the ~70 hint words appear in
// this line?
//
// # 🔴 Why this is hand-written rather than a regex
//
// It was a regex — an alternation of every hint, all plain literals, which looked like exactly the case
// a regex engine should be good at. A CPU profile said otherwise: 3.2 seconds inside `regexp.(*machine)`
// plus 1.9 seconds of GC pressure from its allocations, on a 1.1M-line repository. Go's regexp is a
// general automaton and pays general-automaton costs even when every branch is a constant string.
//
// The specialised structure is a two-byte prefix index. Scan the line once; at each position take the
// next two bytes, look up the handful of hints that start with them, and confirm with a prefix compare.
// No automaton, no allocation, one pass.
//
// # 🚫 What this deliberately is not
//
// It is not Aho-Corasick. The full algorithm's advantage is bounded work on adversarial inputs with long
// shared prefixes; source code is not adversarial, the hint set is small and its two-byte prefixes are
// well spread, so the extra failure-link machinery would cost more to maintain than it saves. If the
// hint vocabulary ever grows enough that a profile disagrees, that is the moment to reach for it.

// literalIndex finds which known literals occur in a string.
type literalIndex struct {
	// byPrefix maps a two-byte prefix to the ids of hints beginning with it. Two bytes rather than one
	// because a single byte leaves large buckets — eleven hints begin with `m` — and the whole point is
	// that the bucket is usually empty.
	byPrefix map[uint16][]int
	hints    []string
}

func newLiteralIndex(hints []string) *literalIndex {
	li := &literalIndex{byPrefix: make(map[uint16][]int, len(hints)*2), hints: hints}
	for id, h := range hints {
		if len(h) < 2 {
			// A one-byte hint would need its own scan path, and every hint in the table is a word. Refuse
			// rather than quietly skipping it, which would silently disable that pattern's gate.
			panic("discovery: hint " + h + " is shorter than two bytes")
		}
		k := prefixKey(h[0], h[1])
		li.byPrefix[k] = append(li.byPrefix[k], id)
	}
	return li
}

func prefixKey(a, b byte) uint16 { return uint16(a)<<8 | uint16(b) }

// scan returns the set of hints present in an already-lowercased line.
//
// The empty result is the common case and the reason this is fast: a line containing no signal word
// costs one pass with a map lookup per position, and almost every lookup misses.
func (li *literalIndex) scan(lower string) bits {
	var found bits
	if len(lower) < 2 {
		return found
	}
	for i := 0; i+1 < len(lower); i++ {
		ids, ok := li.byPrefix[prefixKey(lower[i], lower[i+1])]
		if !ok {
			continue
		}
		rest := lower[i:]
		for _, id := range ids {
			if strings.HasPrefix(rest, li.hints[id]) {
				found.set(id)
			}
		}
	}
	return found
}
