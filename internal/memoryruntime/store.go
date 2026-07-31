// Package memoryruntime is the first of the two artifacts P17's refusal named as missing: a memory
// RUNTIME — a store, a key scheme, and a lifetime — plus the `Recall`/`Record` semantics of the five
// sealed builtin strategies (P18 §1, decisions.md D1/D3/D4/D5).
//
// # What "runtime" means here, and what it deliberately excludes
//
// This runtime backs a module that ships INTO THE CUSTOMER'S REPOSITORY and runs in their process. That
// one fact decides most of what follows, and each consequence is a recorded decision rather than a style
// choice:
//
//	it calls no provider          a generated file that reached one would need a credential there (D3)
//	it reads no clock             a TTL makes recall depend on WHEN it runs, so the axis stops being
//	                              scorable — and an unscorable axis cannot be optimized, which is the
//	                              whole reason memory became a Dimension (D4)
//	it defaults no scope          a defaulted session id merges conversations that must stay separate,
//	                              undetectably, from inside the process (D1)
//	it substitutes nothing        a summary-buffer that quietly truncates IS scratchpad, running under a
//	                              config_hash that says otherwise (D3)
//
// # Why the behaviour lives here and not on the sealed registry entry
//
// `registry.MemoryStrategy` declares WHAT a strategy is — its name, its human labels, the schema its
// params must satisfy. This package decides what it DOES. Keeping them apart means resolving a sealed
// definition does not drag a store into every consumer, and the two are bound by a conformance test
// rather than by an import: `TestEveryBuiltinStrategyImplementsRecallAndRecord` fails if the sealed
// vocabulary and this dispatch ever disagree about which strategies exist.
package memoryruntime

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Message is one turn. Deliberately the same shape as registry.Message — memory and context are
// different axes but carry the same unit, and a second spelling would need a converter nobody keeps
// correct.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Entry is one stored record: a turn, plus the scoping and ordering a recall needs without re-deriving
// anything.
type Entry struct {
	Key Key `json:"key"`
	// Seq is the monotonic position within the key, assigned by the STORE rather than taken from a clock.
	// A wall-clock timestamp makes recall non-deterministic across machines and leaves two writes in the
	// same millisecond unordered; determinism is the one property the whole eval path rests on (D4).
	Seq     int     `json:"seq"`
	Message Message `json:"message"`
}

// Key scopes a set of memory entries — the KEY SCHEME P17's refusal named as missing (D1).
//
// # Why two parts, and why both are required
//
// The two failures the two halves prevent are different in kind, and each is invisible in single-user
// testing:
//
//   - NodeID alone would make every conversation in a workflow share one memory. Two users' facts land in
//     one scratchpad, and one user's entity memory is recalled into another's prompt. That is a DATA-LEAK
//     shape, not a correctness bug, and it appears only under concurrent real traffic.
//   - SessionID alone would make two different nodes share a store, so a summarizer's rolling summary is
//     recalled by a classifier that never wrote it — a node running a memory it did not configure.
//
// 🔴 There is deliberately NO tenant part. A store instance belongs to one deployment of one customer's
// agent, so a tenant field here would be a SECOND isolation boundary beside the real one (the process) —
// and when two mechanisms exist, only one ends up enforced, never the stronger.
type Key struct {
	NodeID string `json:"node_id"`
	// SessionID is which conversation this belongs to. Supplied by the caller; the runtime never invents
	// one, because a defaulted session id silently merges conversations that should be separate.
	SessionID string `json:"session_id"`
}

// Valid reports whether the key names both scopes.
func (k Key) Valid() bool { return k.NodeID != "" && k.SessionID != "" }

// String is the store's index key. The NUL separator is not decoration: without it
// {"a","bc"} and {"ab","c"} would collide onto one scope, which is the cross-conversation merge D1
// exists to prevent, reached by string concatenation instead of by policy.
func (k Key) String() string { return k.NodeID + "\x00" + k.SessionID }

var (
	// ErrInvalidKey is returned for a key missing either scope. Fail closed: a recall against an invalid
	// key must not quietly return everything, and a record against one must not quietly go somewhere
	// shared.
	ErrInvalidKey = errors.New("memoryruntime: a memory key needs both a node and a session")
	// ErrUnknownStrategy is returned for a strategy this runtime does not implement. Loud, never a
	// pass-through: a strategy that silently returned its input unchanged would run `none` under another
	// strategy's config_hash — the failure the whole axis exists to prevent.
	ErrUnknownStrategy = errors.New("memoryruntime: no runtime implementation for this strategy")
	// ErrNoSummarizer / ErrNoEmbedder: a strategy needs a host service it was not given. It REFUSES
	// rather than falling back, because the tempting fallback for a missing summarizer — "drop the oldest
	// turns instead" — is not a degraded summary-buffer, it IS scratchpad (D3).
	ErrNoSummarizer = errors.New("memoryruntime: summary-buffer needs a host summarizer; refusing rather than truncating silently")
	ErrNoEmbedder   = errors.New("memoryruntime: vector-recall needs a host embedder; refusing rather than falling back to recency")
)

// Store is the persistence seam. The contract is deliberately tiny — append, read, expire — because
// every strategy's behaviour is decided by this package. A store that knew about `summary-buffer` would
// be a second place the strategy is defined (D5).
type Store interface {
	Append(k Key, m Message) (Entry, error)
	Entries(k Key) ([]Entry, error)
	// Expire retains the most recent keepLast entries and drops the rest, returning how many it dropped.
	Expire(k Key, keepLast int) (int, error)
}

// MemStore is the in-process Store the generated artifact uses by default and the tests run against.
//
// 🚫 It is not a claim that a process-local store is the right production choice. It is honest for a
// single-process agent, and every surface that mentions it must say so rather than imply durability.
// What matters for every guarantee this package makes is that the SEMANTICS live here rather than in the
// store, so swapping the store changes durability and nothing else.
type MemStore struct {
	byKey map[string][]Entry
	seq   map[string]int
}

func NewMemStore() *MemStore {
	return &MemStore{byKey: map[string][]Entry{}, seq: map[string]int{}}
}

func (s *MemStore) Append(k Key, m Message) (Entry, error) {
	if !k.Valid() {
		return Entry{}, fmt.Errorf("%w: %+v", ErrInvalidKey, k)
	}
	id := k.String()
	s.seq[id]++
	e := Entry{Key: k, Seq: s.seq[id], Message: m}
	s.byKey[id] = append(s.byKey[id], e)
	return e, nil
}

func (s *MemStore) Entries(k Key) ([]Entry, error) {
	if !k.Valid() {
		return nil, fmt.Errorf("%w: %+v", ErrInvalidKey, k)
	}
	src := s.byKey[k.String()]
	// A COPY. Returning the backing slice would let a caller mutate the store by holding it — the kind of
	// aliasing bug that produces a memory which changes when nobody wrote to it.
	out := make([]Entry, len(src))
	copy(out, src)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

// Expire enforces the LIFETIME (D4): count-based, never time-based.
func (s *MemStore) Expire(k Key, keepLast int) (int, error) {
	if !k.Valid() {
		return 0, fmt.Errorf("%w: %+v", ErrInvalidKey, k)
	}
	if keepLast < 0 {
		return 0, fmt.Errorf("memoryruntime: keepLast must not be negative, got %d", keepLast)
	}
	id := k.String()
	cur := s.byKey[id]
	if len(cur) <= keepLast {
		return 0, nil
	}
	dropped := len(cur) - keepLast
	s.byKey[id] = append([]Entry(nil), cur[dropped:]...)
	return dropped, nil
}

// Summarizer is the host seam `summary-buffer` folds older turns through. An INTERFACE the caller
// supplies, never something this package constructs (D3).
type Summarizer interface {
	Summarize(older []Message, maxTokens int) (string, error)
}

// Embedder is the host seam `vector-recall` scores similarity through. Same rule.
type Embedder interface {
	// Score returns a similarity for each candidate against the query. It must be deterministic for a
	// given (query, candidates, ref) triple — this package asserts ordering stability, not a value.
	Score(query string, candidates []string, embeddingRef string) ([]float64, error)
}

// Host bundles the optional services strategies may need. A strategy that needs one it was not given
// REFUSES; it never falls back to a cheaper behaviour, because a fallback runs something other than what
// the config_hash names.
type Host struct {
	Summarizer Summarizer
	Embedder   Embedder
}

// estimateTokens is the same cheap word-count proxy the context policies use. It is a PROXY and is named
// as one: an exact tokenizer is provider-specific, and a bound that is approximately right and identical
// everywhere beats one exact in a single provider and wrong in the rest.
func estimateTokens(ms []Message) int {
	n := 0
	for _, m := range ms {
		n += len(strings.Fields(m.Content))
	}
	return n
}

func messagesOf(entries []Entry) []Message {
	out := make([]Message, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Message)
	}
	return out
}
