package memoryruntime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// The strategy dispatch — THE single definition of what a memory strategy does (P18 §1, D5).
//
// # One dispatch, not five methods
//
// Retention and recall live here and nowhere else. `registry.MemoryStrategy` declares what a strategy IS
// (name, labels, params schema); this table decides what it DOES. The generated artifact that ships into
// a customer's repository is emitted FROM this table's parameters, and a conformance test executes both
// and compares — because the copy that drifts is always the generated one, being the one nobody reads
// after it is written.
//
// 🔴 The failure this prevents is stated once and is worth restating: two implementations of "what does
// `scratchpad(max_entries=5)` retain" produce a diff that behaves differently from the strategy the
// `config_hash` names, SCORED AS THAT STRATEGY. That is the same class as a silent drop, with a
// non-empty diff in front of it.
//
// # Recall and Record are one unit, deliberately
//
// They are declared together, dispatched together, and tested together because a strategy is not a read
// or a write — it is both, and either alone behaves as `none` (decisions.md D2). Splitting them into two
// tables would make it possible to add a strategy that implements one, which is precisely the
// half-materialization the transform refuses.

// Params is the typed view of a strategy's sealed params.
//
// One struct for every strategy rather than one per strategy: the registry's `ParamsSchema` already
// rejects a param that does not belong to its strategy at seal time, so by the time params reach here
// the irrelevant fields are absent. A per-strategy type would re-litigate a decision the schema already
// made, in a second place.
type Params struct {
	MaxEntries    int      `json:"max_entries"`
	MaxTokens     int      `json:"max_tokens"`
	KeepLastTurns int      `json:"keep_last_turns"`
	TopK          int      `json:"top_k"`
	EmbeddingRef  string   `json:"embedding_ref"`
	EntityKeys    []string `json:"entity_keys"`
}

// ParseParams decodes sealed params. A malformed body is an error rather than a zero Params: a zero
// `max_entries` would silently mean "retain nothing", which is `none` under another name.
func ParseParams(raw json.RawMessage) (Params, error) {
	var p Params
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return Params{}, fmt.Errorf("memoryruntime: params are not a JSON object: %w", err)
	}
	return p, nil
}

// behaviour is one strategy's read and write. Both fields are required — a nil half is a compile-time
// impossibility here and a loud failure in the conformance test, so a strategy cannot ship with one.
type behaviour struct {
	recall func(p Params, s Store, k Key, incoming []Message, h Host) ([]Message, error)
	record func(p Params, s Store, k Key, turn []Message) error
}

// behaviours is the closed dispatch. A strategy in the sealed vocabulary with no row here fails loudly
// (ErrUnknownStrategy) rather than passing its input through.
var behaviours = map[string]behaviour{
	"none":           {recall: recallNone, record: recordNone},
	"scratchpad":     {recall: recallScratchpad, record: recordScratchpad},
	"summary-buffer": {recall: recallSummaryBuffer, record: recordAppend},
	"vector-recall":  {recall: recallVector, record: recordAppend},
	"entity-memory":  {recall: recallEntity, record: recordEntity},
}

// Strategies lists every strategy this runtime implements, sorted. The conformance test compares it
// against the registry's sealed vocabulary, which is what binds the two without an import.
func Strategies() []string {
	out := make([]string, 0, len(behaviours))
	for name := range behaviours {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Implements reports whether this runtime can execute a strategy.
func Implements(strategy string) bool { _, ok := behaviours[strategy]; return ok }

// Recall returns the message list the node should send: what the store holds, per the strategy, followed
// by the turns the call site wrote.
//
// 🔴 `incoming` is always preserved and always LAST. Memory augments a call; it does not replace it. A
// strategy that dropped or reordered the caller's own turns would be rewriting the request rather than
// remembering, and the node would send something its author never wrote.
func Recall(strategy string, p Params, s Store, k Key, incoming []Message, h Host) ([]Message, error) {
	b, ok := behaviours[strategy]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownStrategy, strategy)
	}
	if !k.Valid() {
		return nil, fmt.Errorf("%w: %+v", ErrInvalidKey, k)
	}
	return b.recall(p, s, k, incoming, h)
}

// Record writes a completed turn to the store. Called with BOTH the request turns and the response, so
// the store holds a conversation rather than half of one.
func Record(strategy string, p Params, s Store, k Key, turn []Message) error {
	b, ok := behaviours[strategy]
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownStrategy, strategy)
	}
	if !k.Valid() {
		return fmt.Errorf("%w: %+v", ErrInvalidKey, k)
	}
	return b.record(p, s, k, turn)
}

// ── none — the identity ──────────────────────────────────────────────────────────────────────────

// recallNone returns the caller's turns untouched. It is a real implementation rather than a missing
// row, so that "this node deliberately carries nothing" is expressible and testable.
func recallNone(_ Params, _ Store, _ Key, incoming []Message, _ Host) ([]Message, error) {
	return append([]Message(nil), incoming...), nil
}

func recordNone(Params, Store, Key, []Message) error { return nil }

// ── scratchpad — recent turns, verbatim, bounded by count ────────────────────────────────────────

func recallScratchpad(p Params, s Store, k Key, incoming []Message, _ Host) ([]Message, error) {
	if p.MaxEntries <= 0 {
		return nil, fmt.Errorf("memoryruntime: scratchpad needs max_entries >= 1, got %d", p.MaxEntries)
	}
	entries, err := s.Entries(k)
	if err != nil {
		return nil, err
	}
	// Bounded HERE as well as at record time. Two enforcement points is not redundancy: a store shared
	// with an older build, or one whose Expire failed, would otherwise recall more than the strategy
	// names — and an unbounded recall is a memory leak in the customer's process (FR6).
	if len(entries) > p.MaxEntries {
		entries = entries[len(entries)-p.MaxEntries:]
	}
	return append(messagesOf(entries), incoming...), nil
}

func recordScratchpad(p Params, s Store, k Key, turn []Message) error {
	if p.MaxEntries <= 0 {
		return fmt.Errorf("memoryruntime: scratchpad needs max_entries >= 1, got %d", p.MaxEntries)
	}
	for _, m := range turn {
		if _, err := s.Append(k, m); err != nil {
			return err
		}
	}
	_, err := s.Expire(k, p.MaxEntries)
	return err
}

// ── summary-buffer — a rolling summary, bounded by tokens ────────────────────────────────────────

func recallSummaryBuffer(p Params, s Store, k Key, incoming []Message, h Host) ([]Message, error) {
	if p.MaxTokens <= 0 {
		return nil, fmt.Errorf("memoryruntime: summary-buffer needs max_tokens >= 1, got %d", p.MaxTokens)
	}
	entries, err := s.Entries(k)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return append([]Message(nil), incoming...), nil
	}

	keep := p.KeepLastTurns
	if keep < 0 {
		keep = 0
	}
	if keep > len(entries) {
		keep = len(entries)
	}
	older := messagesOf(entries[:len(entries)-keep])
	tail := messagesOf(entries[len(entries)-keep:])

	if len(older) == 0 {
		return append(tail, incoming...), nil
	}

	// 🚫 The refusal that keeps the strategy's NAME meaningful. The tempting fallback here is to drop
	// `older` and keep the tail — but that is not a degraded summary-buffer, it IS scratchpad, running
	// under a config_hash that says summary-buffer (D3).
	if h.Summarizer == nil {
		return nil, ErrNoSummarizer
	}
	summary, err := h.Summarizer.Summarize(older, p.MaxTokens)
	if err != nil {
		return nil, fmt.Errorf("memoryruntime: summarizing %d older turn(s): %w", len(older), err)
	}

	out := append([]Message{{Role: "system", Content: summary}}, tail...)
	// Bounded by construction (FR6): trim the retained tail, oldest first, until the recalled prefix fits
	// the budget. The summary itself is never trimmed — a half-sentence summary is worse than a shorter
	// tail, because it silently misrepresents what was remembered rather than carrying less of it.
	for len(out) > 1 && estimateTokens(out) > p.MaxTokens {
		out = append(out[:1], out[2:]...)
	}
	return append(out, incoming...), nil
}

// recordAppend is the shared write for the strategies whose record is "keep the turn". The bound is
// applied at recall for these, because what they retain is a function of tokens or similarity rather
// than of count, and expiring by count here would impose a second, different bound.
func recordAppend(_ Params, s Store, k Key, turn []Message) error {
	for _, m := range turn {
		if _, err := s.Append(k, m); err != nil {
			return err
		}
	}
	return nil
}

// ── vector-recall — similarity, bounded by top-k ─────────────────────────────────────────────────

func recallVector(p Params, s Store, k Key, incoming []Message, h Host) ([]Message, error) {
	if p.TopK <= 0 {
		return nil, fmt.Errorf("memoryruntime: vector-recall needs top_k >= 1, got %d", p.TopK)
	}
	if p.EmbeddingRef == "" {
		// Pinned because recall is only reproducible against ONE embedding: the same top_k over vectors
		// from a different embedding is a different computation that would score differently.
		return nil, fmt.Errorf("memoryruntime: vector-recall needs embedding_ref; recall is only reproducible against a pinned embedding")
	}
	entries, err := s.Entries(k)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return append([]Message(nil), incoming...), nil
	}
	if h.Embedder == nil {
		// 🚫 The fallback here would be "return the most recent top_k instead", which is scratchpad.
		return nil, ErrNoEmbedder
	}

	query := ""
	if len(incoming) > 0 {
		query = incoming[len(incoming)-1].Content
	}
	cands := make([]string, 0, len(entries))
	for _, e := range entries {
		cands = append(cands, e.Message.Content)
	}
	scores, err := h.Embedder.Score(query, cands, p.EmbeddingRef)
	if err != nil {
		return nil, fmt.Errorf("memoryruntime: scoring %d candidate(s): %w", len(cands), err)
	}
	if len(scores) != len(cands) {
		// A scorer that returned a different arity would make the pairing below silently wrong.
		return nil, fmt.Errorf("memoryruntime: embedder returned %d score(s) for %d candidate(s)", len(scores), len(cands))
	}

	idx := make([]int, len(entries))
	for i := range idx {
		idx[i] = i
	}
	// Sort by score DESC, then by Seq ASC. The tiebreak is load-bearing: equal scores are common (an
	// embedder that returns 0 for everything, two identical turns), and without a total order the recall
	// would depend on Go's map/sort instability — which is exactly the non-determinism FR3 forbids.
	sort.SliceStable(idx, func(a, b int) bool {
		ia, ib := idx[a], idx[b]
		if scores[ia] != scores[ib] {
			return scores[ia] > scores[ib]
		}
		return entries[ia].Seq < entries[ib].Seq
	})

	n := p.TopK
	if n > len(idx) {
		n = len(idx)
	}
	picked := idx[:n]
	// Emit in CONVERSATION order, not score order: the model reads a transcript, and a transcript sorted
	// by similarity is not one. Selection is by score; presentation is by sequence.
	sort.Ints(picked)

	out := make([]Message, 0, n+len(incoming))
	for _, i := range picked {
		out = append(out, entries[i].Message)
	}
	return append(out, incoming...), nil
}

// ── entity-memory — declared facts, bounded by the key set ───────────────────────────────────────

// recallEntity emits ONE system message carrying the facts it holds for the declared keys.
//
// The narrowest strategy, and the only one whose loss is legible from the configuration: it carries
// exactly the keys it declares, so what it will and will not remember is readable without running it.
func recallEntity(p Params, s Store, k Key, incoming []Message, _ Host) ([]Message, error) {
	if len(p.EntityKeys) == 0 {
		return nil, fmt.Errorf("memoryruntime: entity-memory needs at least one entity key")
	}
	entries, err := s.Entries(k)
	if err != nil {
		return nil, err
	}
	// Last write wins per key, which is the whole point of an entity memory: a fact that was corrected
	// should be recalled corrected. Iterating in Seq order makes "last" deterministic.
	facts := map[string]string{}
	for _, e := range entries {
		for key, val := range extractFacts(e.Message.Content, p.EntityKeys) {
			facts[key] = val
		}
	}
	if len(facts) == 0 {
		return append([]Message(nil), incoming...), nil
	}

	keys := make([]string, 0, len(facts))
	for key := range facts {
		keys = append(keys, key)
	}
	sort.Strings(keys) // deterministic rendering; map order must never reach the model
	var b strings.Builder
	b.WriteString("Known facts:")
	for _, key := range keys {
		fmt.Fprintf(&b, "\n%s: %s", key, facts[key])
	}
	return append([]Message{{Role: "system", Content: b.String()}}, incoming...), nil
}

// recordEntity stores only turns that carry a declared fact. Storing everything would make the store
// grow with the conversation while recall still only ever reads the declared keys — unbounded growth for
// no recall benefit, which is FR6's failure with a different shape.
func recordEntity(p Params, s Store, k Key, turn []Message) error {
	if len(p.EntityKeys) == 0 {
		return fmt.Errorf("memoryruntime: entity-memory needs at least one entity key")
	}
	for _, m := range turn {
		if len(extractFacts(m.Content, p.EntityKeys)) == 0 {
			continue
		}
		if _, err := s.Append(k, m); err != nil {
			return err
		}
	}
	return nil
}

// extractFacts finds `key: value` or `key = value` for the declared keys, one per line.
//
// 🚫 Deliberately a fixed, boring pattern rather than anything model-driven. Extraction that called a
// model would put a provider call in the runtime (D3) and would make recall non-deterministic (FR3) —
// two of this package's three hard rules, broken for a nicer demo. What it cannot parse it does not
// remember, which is a stated limit rather than a silent guess.
func extractFacts(content string, keys []string) map[string]string {
	if content == "" {
		return nil
	}
	declared := make(map[string]bool, len(keys))
	for _, k := range keys {
		declared[strings.ToLower(strings.TrimSpace(k))] = true
	}
	var out map[string]string
	for _, line := range strings.Split(content, "\n") {
		sep := strings.IndexAny(line, ":=")
		if sep <= 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:sep]))
		val := strings.TrimSpace(line[sep+1:])
		if val == "" || !declared[key] {
			continue
		}
		if out == nil {
			out = map[string]string{}
		}
		out[key] = val
	}
	return out
}
