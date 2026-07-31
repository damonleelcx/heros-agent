package memoryruntime

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
)

// P18 §1 — the memory runtime.
//
// Three properties carry the phase, and each has a failure mode that is invisible without a test:
// determinism (a recall that varies is unscorable), boundedness (an unbounded strategy is a memory leak
// in code we generated into a customer's process), and no-substitution (a summary-buffer that quietly
// truncates IS scratchpad, running under another strategy's hash).

func key(node, session string) Key { return Key{NodeID: node, SessionID: session} }

func msgs(pairs ...string) []Message {
	out := make([]Message, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, Message{Role: pairs[i], Content: pairs[i+1]})
	}
	return out
}

// fixedSummarizer is a deterministic stand-in. A real summarizer is a provider call; what this package
// asserts about one is that it is CALLED host-side and that its absence refuses — both observable here.
type fixedSummarizer struct{ calls int }

func (f *fixedSummarizer) Summarize(older []Message, maxTokens int) (string, error) {
	f.calls++
	return fmt.Sprintf("summary of %d turn(s)", len(older)), nil
}

// lengthEmbedder scores by content length — deterministic, and enough to assert ordering and bounds.
type lengthEmbedder struct{ calls int }

func (e *lengthEmbedder) Score(_ string, cands []string, _ string) ([]float64, error) {
	e.calls++
	out := make([]float64, len(cands))
	for i, c := range cands {
		out[i] = float64(len(c))
	}
	return out, nil
}

// TestStoreKeySchemeScopesByNodeAndSession — task 1.1 🔴 (D1). Both halves of the key, and the
// fail-closed behaviour when either is missing.
func TestStoreKeySchemeScopesByNodeAndSession(t *testing.T) {
	s := NewMemStore()
	p := Params{MaxEntries: 10}

	if err := Record("scratchpad", p, s, key("n1", "s1"), msgs("user", "alpha")); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := Record("scratchpad", p, s, key("n1", "s2"), msgs("user", "beta")); err != nil {
		t.Fatalf("record: %v", err)
	}
	if err := Record("scratchpad", p, s, key("n2", "s1"), msgs("user", "gamma")); err != nil {
		t.Fatalf("record: %v", err)
	}

	t.Run("sessions do not see each other", func(t *testing.T) {
		got, err := Recall("scratchpad", p, s, key("n1", "s1"), nil, Host{})
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		if len(got) != 1 || got[0].Content != "alpha" {
			t.Fatalf("recall = %+v, want only s1's turn. A session leak here is a CROSS-USER leak: one "+
				"user's facts recalled into another's prompt, visible only under concurrent traffic", got)
		}
	})

	t.Run("nodes do not see each other", func(t *testing.T) {
		got, err := Recall("scratchpad", p, s, key("n2", "s1"), nil, Host{})
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		if len(got) != 1 || got[0].Content != "gamma" {
			t.Fatalf("recall = %+v, want only n2's turn; a node must not read memory it never wrote", got)
		}
	})

	t.Run("an incomplete key fails closed", func(t *testing.T) {
		for _, k := range []Key{{NodeID: "n1"}, {SessionID: "s1"}, {}} {
			if _, err := Recall("scratchpad", p, s, k, nil, Host{}); !errors.Is(err, ErrInvalidKey) {
				t.Errorf("recall with key %+v = %v, want ErrInvalidKey. A defaulted scope MERGES "+
					"conversations that must stay separate, undetectably from inside the process", k, err)
			}
			if err := Record("scratchpad", p, s, k, msgs("user", "x")); !errors.Is(err, ErrInvalidKey) {
				t.Errorf("record with key %+v = %v, want ErrInvalidKey", k, err)
			}
		}
	})

	t.Run("adjacent keys do not collide", func(t *testing.T) {
		// 🔴 Without a separator, {"a","bc"} and {"ab","c"} would index to one scope — a
		// cross-conversation merge reached by string concatenation instead of by policy.
		if key("a", "bc").String() == key("ab", "c").String() {
			t.Fatal("two different keys produced the same store index")
		}
	})

	t.Run("Entries returns a copy", func(t *testing.T) {
		got, err := s.Entries(key("n1", "s1"))
		if err != nil {
			t.Fatalf("entries: %v", err)
		}
		if len(got) == 0 {
			t.Fatal("no entries")
		}
		got[0].Message.Content = "mutated"
		again, _ := s.Entries(key("n1", "s1"))
		if again[0].Message.Content == "mutated" {
			t.Error("a caller mutated the store by holding the returned slice; a memory that changes when " +
				"nobody wrote to it is the hardest kind of bug to see")
		}
	})
}

// TestEveryBuiltinStrategyImplementsRecallAndRecord — task 1.2. The conformance test that binds the
// sealed vocabulary to this runtime without an import.
func TestEveryBuiltinStrategyImplementsRecallAndRecord(t *testing.T) {
	sealed := registry.BuiltinMemoryStrategies()
	if len(sealed) != registry.MemoryStrategySetSize {
		t.Fatalf("the sealed set has %d strategies, the cardinality assertion says %d — parser drift",
			len(sealed), registry.MemoryStrategySetSize)
	}

	for _, st := range sealed {
		if !Implements(st.Name()) {
			t.Errorf("the registry seals strategy %q and this runtime does not implement it. A sealed "+
				"strategy with no runtime is one a user can select, hash, and materialize — and which then "+
				"does nothing at run time.", st.Name())
		}
	}
	for _, name := range Strategies() {
		found := false
		for _, st := range sealed {
			if st.Name() == name {
				found = true
			}
		}
		if !found {
			t.Errorf("this runtime implements %q, which the registry does not seal; it is unreachable", name)
		}
	}

	// 🔴 Both halves present for every strategy. A strategy with one is exactly the half-materialization
	// the transform refuses (D2), reached from inside instead of from the call site.
	for name, b := range behaviours {
		if b.recall == nil {
			t.Errorf("strategy %q has no recall", name)
		}
		if b.record == nil {
			t.Errorf("strategy %q has no record", name)
		}
	}

	t.Run("an unimplemented strategy fails loudly", func(t *testing.T) {
		s := NewMemStore()
		_, err := Recall("no-such-strategy", Params{}, s, key("n", "s"), msgs("user", "hi"), Host{})
		if !errors.Is(err, ErrUnknownStrategy) {
			t.Fatalf("err = %v, want ErrUnknownStrategy. Passing the input through would run `none` under "+
				"another strategy's config_hash", err)
		}
		if err := Record("no-such-strategy", Params{}, s, key("n", "s"), msgs("user", "hi")); !errors.Is(err, ErrUnknownStrategy) {
			t.Fatalf("record err = %v, want ErrUnknownStrategy", err)
		}
	})
}

// TestRecallDeterministic — task 1.3 🔴 (FR3).
func TestRecallDeterministic(t *testing.T) {
	cases := []struct {
		strategy string
		params   Params
		host     Host
	}{
		{"none", Params{}, Host{}},
		{"scratchpad", Params{MaxEntries: 3}, Host{}},
		{"summary-buffer", Params{MaxTokens: 50, KeepLastTurns: 2}, Host{Summarizer: &fixedSummarizer{}}},
		{"vector-recall", Params{TopK: 2, EmbeddingRef: "e1"}, Host{Embedder: &lengthEmbedder{}}},
		{"entity-memory", Params{EntityKeys: []string{"user_name", "project"}}, Host{}},
	}

	for _, c := range cases {
		t.Run(c.strategy, func(t *testing.T) {
			s := NewMemStore()
			k := key("n1", "s1")
			for _, turn := range [][]Message{
				msgs("user", "user_name: ada"),
				msgs("assistant", "noted"),
				msgs("user", "project: apollo"),
				msgs("assistant", "understood"),
				msgs("user", "user_name: grace"),
			} {
				if err := Record(c.strategy, c.params, s, k, turn); err != nil {
					t.Fatalf("record: %v", err)
				}
			}

			incoming := msgs("user", "who am I?")
			first, err := Recall(c.strategy, c.params, s, k, incoming, c.host)
			if err != nil {
				t.Fatalf("recall: %v", err)
			}
			want, _ := json.Marshal(first)
			for i := 0; i < 20; i++ {
				got, err := Recall(c.strategy, c.params, s, k, incoming, c.host)
				if err != nil {
					t.Fatalf("recall %d: %v", i, err)
				}
				gotJSON, _ := json.Marshal(got)
				if string(gotJSON) != string(want) {
					t.Fatalf("recall %d differed from the first.\n got: %s\nwant: %s\nA recall that varies "+
						"makes the axis unscorable, and an unscorable axis cannot be optimized — which is the "+
						"whole reason memory became a Dimension", i, gotJSON, want)
				}
			}

			// 🔴 The caller's own turns survive, in order, at the end. Memory AUGMENTS a call; a strategy
			// that dropped or reordered them would be rewriting the request rather than remembering it.
			if len(first) < len(incoming) {
				t.Fatalf("recall returned %d message(s), fewer than the %d the caller wrote", len(first), len(incoming))
			}
			tail := first[len(first)-len(incoming):]
			for i := range incoming {
				if tail[i] != incoming[i] {
					t.Errorf("the caller's turn %d was altered: got %+v, want %+v", i, tail[i], incoming[i])
				}
			}
		})
	}
}

// TestStrategiesAreBounded — task 1.4 🔴 (FR6). An unbounded strategy is a memory leak in code the
// platform generated into a customer's process.
func TestStrategiesAreBounded(t *testing.T) {
	const turns = 500

	t.Run("scratchpad never exceeds max_entries", func(t *testing.T) {
		s := NewMemStore()
		k := key("n", "s")
		p := Params{MaxEntries: 5}
		for i := 0; i < turns; i++ {
			if err := Record("scratchpad", p, s, k, msgs("user", fmt.Sprintf("turn %d", i))); err != nil {
				t.Fatalf("record: %v", err)
			}
		}
		got, err := Recall("scratchpad", p, s, k, nil, Host{})
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		if len(got) > p.MaxEntries {
			t.Fatalf("recalled %d turns with max_entries=%d", len(got), p.MaxEntries)
		}
		// And the STORE is bounded too, not just the recall — otherwise the process grows forever while
		// the recall stays small, which is the leak wearing a passing test.
		entries, _ := s.Entries(k)
		if len(entries) > p.MaxEntries {
			t.Fatalf("the store holds %d entries with max_entries=%d; the recall was bounded but the "+
				"process still grows without limit", len(entries), p.MaxEntries)
		}
	})

	t.Run("summary-buffer never exceeds max_tokens", func(t *testing.T) {
		s := NewMemStore()
		k := key("n", "s")
		p := Params{MaxTokens: 12, KeepLastTurns: 50}
		for i := 0; i < turns; i++ {
			if err := Record("summary-buffer", p, s, k, msgs("user", fmt.Sprintf("a fairly wordy turn number %d indeed", i))); err != nil {
				t.Fatalf("record: %v", err)
			}
		}
		got, err := Recall("summary-buffer", p, s, k, nil, Host{Summarizer: &fixedSummarizer{}})
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		if n := estimateTokens(got); n > p.MaxTokens {
			t.Fatalf("recalled ~%d tokens with max_tokens=%d: %+v", n, p.MaxTokens, got)
		}
	})

	t.Run("vector-recall never exceeds top_k", func(t *testing.T) {
		s := NewMemStore()
		k := key("n", "s")
		p := Params{TopK: 4, EmbeddingRef: "e1"}
		for i := 0; i < turns; i++ {
			if err := Record("vector-recall", p, s, k, msgs("user", strings.Repeat("x", i%17+1))); err != nil {
				t.Fatalf("record: %v", err)
			}
		}
		got, err := Recall("vector-recall", p, s, k, nil, Host{Embedder: &lengthEmbedder{}})
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		if len(got) > p.TopK {
			t.Fatalf("recalled %d turns with top_k=%d", len(got), p.TopK)
		}
	})

	t.Run("entity-memory is bounded by its declared keys", func(t *testing.T) {
		s := NewMemStore()
		k := key("n", "s")
		p := Params{EntityKeys: []string{"user_name", "project"}}
		for i := 0; i < turns; i++ {
			// A turn carrying no declared fact must not be stored at all — otherwise the store grows with
			// the conversation while recall still reads only the declared keys.
			if err := Record("entity-memory", p, s, k, msgs("user", fmt.Sprintf("chit chat %d", i))); err != nil {
				t.Fatalf("record: %v", err)
			}
		}
		entries, _ := s.Entries(k)
		if len(entries) != 0 {
			t.Fatalf("entity-memory stored %d turns that carry no declared fact; the store would grow with "+
				"the conversation for no recall benefit", len(entries))
		}
		for i := 0; i < turns; i++ {
			if err := Record("entity-memory", p, s, k, msgs("user", fmt.Sprintf("user_name: name%d", i))); err != nil {
				t.Fatalf("record: %v", err)
			}
		}
		got, err := Recall("entity-memory", p, s, k, nil, Host{})
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("recalled %d messages, want exactly one facts message", len(got))
		}
		// Last write wins — a corrected fact is recalled corrected.
		if !strings.Contains(got[0].Content, fmt.Sprintf("name%d", turns-1)) {
			t.Errorf("the facts message does not carry the latest value: %q", got[0].Content)
		}
	})
}

// TestRuntimeMakesNoProviderCall — task 1.5 🚫 (FR7/D3). Both directions: the runtime calls nothing
// itself, and a strategy missing its host service REFUSES rather than substituting.
func TestRuntimeMakesNoProviderCall(t *testing.T) {
	t.Run("a missing summarizer refuses rather than truncating", func(t *testing.T) {
		s := NewMemStore()
		k := key("n", "s")
		p := Params{MaxTokens: 100, KeepLastTurns: 1}
		for i := 0; i < 5; i++ {
			if err := Record("summary-buffer", p, s, k, msgs("user", fmt.Sprintf("turn %d", i))); err != nil {
				t.Fatalf("record: %v", err)
			}
		}
		_, err := Recall("summary-buffer", p, s, k, nil, Host{}) // no summarizer
		if !errors.Is(err, ErrNoSummarizer) {
			t.Fatalf("err = %v, want ErrNoSummarizer.\nThe tempting fallback is to drop the older turns and "+
				"keep the tail — but that is not a degraded summary-buffer, it IS scratchpad, running under "+
				"a config_hash that says summary-buffer", err)
		}
	})

	t.Run("a missing embedder refuses rather than falling back to recency", func(t *testing.T) {
		s := NewMemStore()
		k := key("n", "s")
		p := Params{TopK: 2, EmbeddingRef: "e1"}
		for i := 0; i < 5; i++ {
			if err := Record("vector-recall", p, s, k, msgs("user", fmt.Sprintf("turn %d", i))); err != nil {
				t.Fatalf("record: %v", err)
			}
		}
		if _, err := Recall("vector-recall", p, s, k, nil, Host{}); !errors.Is(err, ErrNoEmbedder) {
			t.Fatalf("err = %v, want ErrNoEmbedder; returning the most recent top_k instead would be "+
				"scratchpad under vector-recall's hash", err)
		}
	})

	t.Run("the host service is CALLED when supplied", func(t *testing.T) {
		// The mirror: if the summarizer were never invoked, the refusal above would be satisfied by a
		// strategy that ignores summarization entirely.
		s := NewMemStore()
		k := key("n", "s")
		p := Params{MaxTokens: 100, KeepLastTurns: 1}
		for i := 0; i < 5; i++ {
			if err := Record("summary-buffer", p, s, k, msgs("user", fmt.Sprintf("turn %d", i))); err != nil {
				t.Fatalf("record: %v", err)
			}
		}
		sum := &fixedSummarizer{}
		got, err := Recall("summary-buffer", p, s, k, nil, Host{Summarizer: sum})
		if err != nil {
			t.Fatalf("recall: %v", err)
		}
		if sum.calls != 1 {
			t.Errorf("the summarizer was called %d time(s), want 1", sum.calls)
		}
		if len(got) == 0 || !strings.HasPrefix(got[0].Content, "summary of") {
			t.Errorf("the summary did not reach the recalled messages: %+v", got)
		}
	})

	t.Run("the strategies that need no host do not require one", func(t *testing.T) {
		s := NewMemStore()
		k := key("n", "s")
		for _, c := range []struct {
			name string
			p    Params
		}{
			{"none", Params{}},
			{"scratchpad", Params{MaxEntries: 3}},
			{"entity-memory", Params{EntityKeys: []string{"project"}}},
		} {
			if err := Record(c.name, c.p, s, k, msgs("user", "project: apollo")); err != nil {
				t.Fatalf("%s record: %v", c.name, err)
			}
			if _, err := Recall(c.name, c.p, s, k, msgs("user", "hi"), Host{}); err != nil {
				t.Errorf("%s needs a host service it should not: %v", c.name, err)
			}
		}
	})
}

// TestCountBasedLifetime — D4/FR4/FR5. Ordering is a sequence, and expiry is a count.
func TestCountBasedLifetime(t *testing.T) {
	s := NewMemStore()
	k := key("n", "s")
	for i := 0; i < 10; i++ {
		if _, err := s.Append(k, Message{Role: "user", Content: fmt.Sprintf("t%d", i)}); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	entries, err := s.Entries(k)
	if err != nil {
		t.Fatalf("entries: %v", err)
	}
	for i, e := range entries {
		if e.Seq != i+1 {
			t.Fatalf("entry %d has Seq %d, want %d — ordering must be a store-assigned sequence, not a "+
				"clock: two writes in one millisecond are unordered, and clock skew makes \"oldest first\" "+
				"machine-dependent", i, e.Seq, i+1)
		}
	}

	dropped, err := s.Expire(k, 4)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if dropped != 6 {
		t.Errorf("expire dropped %d, want 6", dropped)
	}
	kept, _ := s.Entries(k)
	if len(kept) != 4 || kept[0].Message.Content != "t6" {
		t.Fatalf("expire kept %+v, want the 4 most recent", kept)
	}

	// Expiring below the retention count is a no-op rather than an error.
	if n, err := s.Expire(k, 10); err != nil || n != 0 {
		t.Errorf("expire past the end = (%d, %v), want (0, nil)", n, err)
	}
}

// TestParamsRejectMalformed pins that a malformed params body is an error rather than a zero Params — a
// zero max_entries would silently mean "retain nothing", which is `none` under another name.
func TestParamsRejectMalformed(t *testing.T) {
	if _, err := ParseParams(json.RawMessage(`not json`)); err == nil {
		t.Fatal("malformed params were accepted")
	}
	p, err := ParseParams(json.RawMessage(`{"max_entries":5}`))
	if err != nil || p.MaxEntries != 5 {
		t.Fatalf("ParseParams = (%+v, %v), want max_entries 5", p, err)
	}
	s := NewMemStore()
	if _, err := Recall("scratchpad", Params{}, s, key("n", "s"), nil, Host{}); err == nil {
		t.Fatal("scratchpad accepted max_entries=0; retaining nothing is `none` under another name")
	}
}
