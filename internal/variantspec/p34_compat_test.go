package variantspec

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
)

// p34_compat_test.go is P34 §1 — the compatibility fence for the whole phase.
//
// # Why this file exists before any P34 code does
//
// P34 splits one axis into three. ADR-014's whole argument is that the naive version of that split
// changes the `version_id` of every loop-bearing harness entry, which changes the `config_hash` of every
// spec referencing it, which orphans every measurement ever taken on a multi-turn node. Nothing errors
// when that happens. A board simply has less evidence on it than it should, months later, with no change
// nearby to blame.
//
// So the fence is bytes RECORDED BY PRE-P34 CODE, checked in, and compared against. A test that
// recomputes both sides asserts only that Resolve is a function — the same reasoning
// p27_hash_recording_test.go gives, and this file follows its shape deliberately rather than inventing a
// second recording discipline.
//
// # RECORDING (done once, in the pre-P34 tree — do NOT re-record from a tree that has P34 in it)
//
//	GOWORK=off P34_RECORD_PRE=1 go test ./internal/variantspec/ -run TestPreP34
//
// Re-recording destroys the only evidence this file carries.
//
// # The four fences, and what each one can catch that the others cannot
//
//	§1.1  the P0 golden vectors, unchanged           — TestP34_GoldenVectorsUnchanged
//	§1.2  a no-override spec serialises byte-identically to its pre-P34 bytes
//	                                                 — TestPreP34ConfigHashesAreReproducedExactly (row 1)
//	§1.3  a pre-P34 spec on a LOOP-BEARING harness entry resolves and reproduces its config_hash
//	                                                 — same test, rows 2-4
//	§1.5  a spec using no new field still decodes under the PREVIOUS binary's shape
//	                                                 — TestP34_RollbackShapeIsReadableByThePreviousBinary
//
// §1.4 (raising a turn ceiling moves no loop entry's version_id) needs `registry.KindLoop` to exist and
// lives in internal/registry/loop_test.go, beside the thing it fences.

const p34PreFixture = "testdata/p34-pre-confighash.json"

// p34Recorded is one spec's resolution as recorded. Canonical BYTES beside the hash on purpose: a hash
// comparison says a configuration moved and nothing else, while the bytes say which key did it.
type p34Recorded struct {
	Name       string          `json:"name"`
	Why        string          `json:"why"`
	Canonical  string          `json:"canonical_json"`
	ConfigHash string          `json:"config_hash"`
	Raw        json.RawMessage `json:"resolved_config"`
}

// p34RecordingSpecs are the configurations the fixture covers. They are chosen to span exactly what P34
// threatens: a spec that mentions no scaffold at all (§1.2), and three that reference LOOP-BEARING
// harness entries (§1.3) — one per multi-turn strategy shape that a `loop` axis would relocate.
func p34RecordingSpecs(t *testing.T) []p34Recorded {
	t.Helper()
	ctx := context.Background()

	return []p34Recorded{
		p34Resolve(t, ctx, "no-scaffold-at-all",
			"§1.2 — a spec with no harness_ref, no loop_ref and no graph declaration. Its canonical bytes "+
				"are the ones P34 must not move: every additive field the phase adds is omitempty, so a "+
				"configuration that declares none of them must serialise exactly as it did before.",
			func(s *VariantSpec) {}),

		p34Resolve(t, ctx, "legacy-loop-bearing-reflexion",
			"§1.3 — a pre-P34 spec on a loop-bearing harness entry: reflexion carries max_turns, a stop "+
				"condition and a reflection prompt, all of which P34 relocates to the loop axis. This row is "+
				"the ADR-014 orphaning chain made checkable: if the hashed projection of these params moves, "+
				"every measurement taken on this node becomes unreachable from any spec anyone can write.",
			func(s *VariantSpec) {
				s.Nodes["n_a"] = NodeOverride{HarnessRef: "h-reflexion"}
			}),

		p34Resolve(t, ctx, "legacy-loop-bearing-react",
			"§1.3 — the same, on react-loop, whose params carry a retry budget as well. A separate row "+
				"because the params SET differs, and the hash is over the set.",
			func(s *VariantSpec) {
				s.Nodes["n_a"] = NodeOverride{HarnessRef: "h-react"}
			}),

		p34Resolve(t, ctx, "legacy-loop-bearing-group",
			"§1.3 — a loop-bearing harness wrapping an ordered EDGE SET (P18 FR15) rather than one node. "+
				"Included because harness_groups is the precedent P34's graph_groups follows, and a change "+
				"to the group projection would move a hash that no per-node fixture covers.",
			func(s *VariantSpec) {
				s.HarnessGroups = []HarnessGroup{{
					HarnessRef: "h-reflexion",
					Edges:      []Edge{{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "data"}},
				}}
			}),
	}
}

func p34Resolve(t *testing.T, ctx context.Context, name, why string, mutate func(*VariantSpec)) p34Recorded {
	t.Helper()
	spec := p34Spec()
	mutate(spec)
	got, err := Resolve(ctx, spec, p34IR(), p34Registries{t: t})
	if err != nil {
		t.Fatalf("%s: Resolve: %v", name, err)
	}
	canon, err := got.Config.Canonical()
	if err != nil {
		t.Fatalf("%s: Canonical: %v", name, err)
	}
	raw, err := json.Marshal(got.Config)
	if err != nil {
		t.Fatalf("%s: marshal: %v", name, err)
	}
	return p34Recorded{Name: name, Why: why, Canonical: string(canon), ConfigHash: got.ConfigHash, Raw: raw}
}

// p34IR / p34Spec / p34Registries are this file's OWN fixtures rather than resolve_test.go's, for the
// reason p27_hash_recording_test.go builds its own: the recorded bytes only mean something if the inputs
// behind them travel in the same file that was run in the pre-change checkout.
func p34IR() *discovery.IR {
	return &discovery.IR{
		IRVersion: "1.0.0",
		Nodes: []discovery.IRNode{
			{
				NodeID: "n_a", Kind: "static_definition",
				CallSite:        discovery.IRCallSite{File: "agent.go", Symbol: "answer", LineStart: 9, LineEnd: 9},
				Model:           discovery.IRModel{Provider: "anthropic", ModelID: "claude-opus-4-8", Params: map[string]any{}},
				Prompt:          discovery.IRPrompt{Inline: "answer the question", Variables: []string{}},
				ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages"},
			},
			{
				NodeID: "n_b", Kind: "static_definition",
				CallSite:        discovery.IRCallSite{File: "agent.go", Symbol: "review", LineStart: 21, LineEnd: 21},
				Model:           discovery.IRModel{Provider: "anthropic", ModelID: "claude-sonnet-5", Params: map[string]any{}},
				Prompt:          discovery.IRPrompt{Inline: "review the answer", Variables: []string{}},
				ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages"},
			},
		},
		Edges: []discovery.IREdge{},
	}
}

func p34Spec() *VariantSpec {
	return &VariantSpec{
		WorkflowID:     "wf-p34-recording",
		SourceRevision: "1a2b3c4d5e6f70819293a4b5c6d7e8f901234567",
		Order:          []string{"n_a", "n_b"},
		Nodes:          map[string]NodeOverride{},
		Edges:          []Edge{{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "data"}},
	}
}

// p34Registries resolves exactly the two LOOP-BEARING harness entries the recording needs. The entries
// are built from the builtin strategy set rather than hand-spelled, so what the recording resolves is
// what the production seal path would have produced for the same content.
type p34Registries struct{ t *testing.T }

func (p p34Registries) ResolveModel(context.Context, string) (*registry.ModelEntry, error) {
	return nil, registry.ErrNotFound
}
func (p p34Registries) ResolvePrompt(context.Context, string) (*registry.PromptEntry, error) {
	return nil, registry.ErrNotFound
}
func (p p34Registries) ResolveSkill(context.Context, string) (*registry.SkillEntry, error) {
	return nil, registry.ErrNotFound
}
func (p p34Registries) ResolveContextPolicy(context.Context, string) (*registry.ContextEntry, error) {
	return nil, registry.ErrNotFound
}
func (p p34Registries) ResolveMemory(context.Context, string) (*registry.MemoryEntry, error) {
	return nil, registry.ErrNotFound
}

func (p p34Registries) ResolveHarness(_ context.Context, id string) (*registry.HarnessEntry, error) {
	switch id {
	case "h-reflexion":
		return p34Harness(p.t, "h-reflexion", "revise-twice", "reflexion",
			`{"max_turns":4,"stop_condition":"answer-marker","answer_marker":"FINAL","reflection_prompt":"critique your answer and improve it"}`), nil
	case "h-react":
		return p34Harness(p.t, "h-react", "tool-loop", "react-loop",
			`{"max_turns":6,"stop_condition":"no-tool-call","retry_budget":2}`), nil
	}
	return nil, registry.ErrNotFound
}

// ResolveLoop answers for P34's loop registry. This fixture publishes no loop entries, so every
// loop_ref misses — which is the fail-closed answer, not an empty one.
func (p p34Registries) ResolveLoop(context.Context, string) (*registry.LoopEntry, error) {
	return nil, registry.ErrNotFound
}

func p34Harness(t *testing.T, ref, name, strategy, params string) *registry.HarnessEntry {
	t.Helper()
	st := registry.HarnessStrategyNamed(strategy)
	if st == nil {
		t.Fatalf("p34Harness: %q is not a builtin harness strategy", strategy)
	}
	return &registry.HarnessEntry{
		VersionID: ref, Name: name, Strategy: st,
		Spec: registry.HarnessSpec{Strategy: strategy, Params: json.RawMessage(params)},
	}
}

// ── §1.2 + §1.3 — the recording ──────────────────────────────────────────────────────────────────

func TestPreP34ConfigHashesAreReproducedExactly(t *testing.T) {
	got := p34RecordingSpecs(t)

	if os.Getenv("P34_RECORD_PRE") == "1" {
		p34WriteRecording(t, got)
		t.Skip("recorded the pre-P34 config hashes; this mode must only ever run in a pre-P34 checkout")
	}

	want := p34ReadRecording(t)
	if len(got) != len(want) {
		t.Fatalf("the recording covers %d configurations, this tree resolves %d", len(want), len(got))
	}
	for i := range want {
		w, g := want[i], got[i]
		if g.Name != w.Name {
			t.Fatalf("fixture %d: the recording is of %q, this tree built %q — the fixtures diverged, and "+
				"nothing below is a comparison of like with like", i, w.Name, g.Name)
		}
		// Bytes before hash. If both moved, the bytes say WHICH key did it; if only the hash moved, the
		// canonicalizer changed, which is a different and much worse bug.
		if g.Canonical != w.Canonical {
			t.Errorf("%s: the canonical bytes changed.\n  before P34: %s\n  now:        %s",
				w.Name, w.Canonical, g.Canonical)
		}
		if g.ConfigHash != w.ConfigHash {
			t.Errorf("%s: config_hash = %s, was %s before P34. ADR-014's orphaning chain has just happened: "+
				"every measurement filed under the old hash is now unreachable from this configuration.",
				w.Name, g.ConfigHash, w.ConfigHash)
		}
		if g.Canonical == w.Canonical && g.ConfigHash != w.ConfigHash {
			t.Errorf("%s: identical canonical bytes hashed to a different value — the digest itself moved, "+
				"not the configuration", w.Name)
		}
	}
}

// TestTheP34RecordingStillDistinguishesConfigurations keeps the recording honest: rows that all carried
// the same hash would compare equal forever and assert nothing.
func TestTheP34RecordingStillDistinguishesConfigurations(t *testing.T) {
	want := p34ReadRecording(t)
	if len(want) < 4 {
		t.Fatalf("the recording covers %d configurations; it was written to cover four distinct shapes", len(want))
	}
	seen := map[string]string{}
	for _, r := range want {
		if len(r.ConfigHash) != 64 {
			t.Errorf("%s: recorded config_hash %q is not 64 hex chars", r.Name, r.ConfigHash)
		}
		if prev, dup := seen[r.ConfigHash]; dup {
			t.Errorf("%s and %s share a config_hash. These are different configurations; a recording in "+
				"which they collide is not evidence that the hash distinguishes anything.", prev, r.Name)
		}
		seen[r.ConfigHash] = r.Name
	}
	// The loop-bearing rows must actually carry the loop params into the hash, or §1.3 is fencing an
	// absence. `reflection_prompt` is the one that only a loop-bearing entry can put there.
	found := false
	for _, r := range want {
		if strings.Contains(r.Canonical, "reflection_prompt") {
			found = true
		}
	}
	if !found {
		t.Error("no recorded row carries a loop param in its canonical bytes; §1.3 would then be asserting " +
			"that an empty projection stayed empty, which is not the thing at risk")
	}
}

// ── §1.1 — the P0 golden vectors, named as a P34 fence ───────────────────────────────────────────

// TestP34_GoldenVectorsUnchanged is task 1.1, and it is deliberately a THIN wrapper: the golden
// comparison already lives in resolved_golden_test.go and confighash's own suite, and a second
// implementation of it would be a second thing to keep true. What this adds is a NAME — the phase's
// most important fence should be findable by the phase's own vocabulary, so that "is 1.1 still green?"
// is answerable without knowing which file P0 happened to put it in.
func TestP34_GoldenVectorsUnchanged(t *testing.T) {
	g := loadGolden(t)
	rc := decodeGolden(t, g.Base.ResolvedConfig)

	canon, err := rc.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if string(canon) != g.Base.CanonicalJSON {
		t.Fatalf("§1.1 IS RED. The P0 golden vector's canonical bytes moved, which means every stored "+
			"config_hash in the product now denotes something else.\n got: %s\nwant: %s", canon, g.Base.CanonicalJSON)
	}
	got, err := rc.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if got != g.Base.ConfigHash {
		t.Fatalf("§1.1 IS RED. config_hash = %s, want %s. Nothing else in P34 is safe until this is green.",
			got, g.Base.ConfigHash)
	}

	// The other half of 1.1: the frozen bytes must not have acquired P34 vocabulary. A golden that
	// reproduced while carrying a `loop` or a `graph_groups` KEY would mean the fixture was re-frozen
	// rather than the code kept compatible — the standing way a golden stops being evidence.
	//
	// 🔴 It reads KEYS, parsed, not substrings. A substring scan reports a match on a customer's node_id
	// that happens to contain the word "loop" — which this golden's own fixture does — and a fence that
	// fires on someone else's naming is a fence that gets deleted.
	for _, banned := range []string{"loop", "loop_ref", "graph_groups", "concurrent", "merge", "predicate"} {
		if p34JSONHasKey(t, g.Base.CanonicalJSON, banned) {
			t.Errorf("the frozen golden vector carries a %q key. P34 is additive-only: if this appears, the "+
				"golden was re-recorded instead of the compatibility being preserved.", banned)
		}
	}
}

// p34JSONHasKey reports whether any object anywhere in the document declares this key.
func p34JSONHasKey(t *testing.T, doc, key string) bool {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(doc), &v); err != nil {
		t.Fatalf("the golden's canonical_json is not JSON: %v", err)
	}
	var walk func(any) bool
	walk = func(n any) bool {
		switch x := n.(type) {
		case map[string]any:
			for k, child := range x {
				if k == key || walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range x {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(v)
}

// ── §1.5 — rollback ──────────────────────────────────────────────────────────────────────────────

// TestP34_RollbackShapeIsReadableByThePreviousBinary is task 1.5's checkable half.
//
// The claim under test: a spec authored under the NEW binary that uses no new field still resolves
// under the PREVIOUS one. The previous binary is not available to run here, but what it would do is
// entirely determined by its SHAPE — it decodes resolved_config into a struct that has no P34 fields.
// So the fence decodes this tree's canonical bytes into a locally-declared mirror of the pre-P34 shape
// with unknown fields REFUSED. A new always-present key would fail it; a new omitempty key left unset
// would pass, which is exactly the behaviour rollback depends on.
//
// 🔴 The mirror is spelled out here rather than derived from ResolvedConfig. Deriving it would make the
// fence move whenever the thing it fences moves, which is the standing way a compatibility test becomes
// permanently green.
func TestP34_RollbackShapeIsReadableByThePreviousBinary(t *testing.T) {
	type preNode struct {
		NodeID               string                     `json:"node_id"`
		ModelRef             string                     `json:"model_ref"`
		PromptRef            string                     `json:"prompt_ref"`
		SkillRefs            []string                   `json:"skill_refs"`
		ContextPolicy        string                     `json:"context_policy"`
		ContextParams        map[string]any             `json:"context_params"`
		ProviderParams       map[string]any             `json:"provider_params"`
		Bindings             map[string]ResolvedBinding `json:"bindings"`
		ToolSelection        []string                   `json:"tool_selection"`
		ContextDropTolerance *float64                   `json:"context_drop_tolerance"`
		Memory               *ResolvedMemory            `json:"memory"`
		Harness              *ResolvedHarness           `json:"harness"`
	}
	type preEdge struct {
		FromNodeID string `json:"from_node_id"`
		ToNodeID   string `json:"to_node_id"`
		Kind       string `json:"kind"`
	}
	type preGroup struct {
		Harness ResolvedHarness `json:"harness"`
		Edges   []preEdge       `json:"edges"`
	}
	type preConfig struct {
		IRVersion     string     `json:"ir_version"`
		Nodes         []preNode  `json:"nodes"`
		Edges         []preEdge  `json:"edges"`
		HarnessGroups []preGroup `json:"harness_groups"`
	}

	for _, r := range p34RecordingSpecs(t) {
		dec := json.NewDecoder(strings.NewReader(r.Canonical))
		dec.DisallowUnknownFields()
		var pre preConfig
		if err := dec.Decode(&pre); err != nil {
			t.Errorf("%s: a pre-P34 binary could not read this configuration: %v.\n"+
				"Rollback is only safe while a spec that uses no new field produces bytes the previous "+
				"binary accepts — an always-present new key breaks that, an omitempty one does not.\n"+
				"  bytes: %s", r.Name, err, r.Canonical)
		}
	}
}

// ── fixture I/O ──────────────────────────────────────────────────────────────────────────────────

func p34WriteRecording(t *testing.T, rs []p34Recorded) {
	t.Helper()
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	b, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		t.Fatalf("marshal recording: %v", err)
	}
	if err := os.WriteFile(p34PreFixture, append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", p34PreFixture, err)
	}
	t.Logf("wrote %s (%d configurations)", p34PreFixture, len(rs))
}

func p34ReadRecording(t *testing.T) []p34Recorded {
	t.Helper()
	b, err := os.ReadFile(p34PreFixture)
	if err != nil {
		t.Fatalf("read %s: %v (record it in a PRE-P34 checkout with P34_RECORD_PRE=1)", p34PreFixture, err)
	}
	var out []p34Recorded
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("parse %s: %v", p34PreFixture, err)
	}
	return out
}
