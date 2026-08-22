package variantspec

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
)

// p27_hash_recording_test.go is P27 task 9.1: config_hash is unchanged by ownership.
//
// An ownership field reaching resolved_config would silently invalidate every cached score, because
// config_hash is the key every stored result is filed under — a run measured last week and the same
// configuration submitted today would stop being the same row. Nothing would error. The board would
// simply have less evidence on it than it should, and the reason would be invisible.
//
// The task is guarded in three ways, because the failure has three shapes:
//
//  1. RECORDED BYTES (TestPreP27ConfigHashesAreReproducedExactly). Four specs resolved through Resolve,
//     with the canonical bytes and the hash recorded by pre-P27 code and checked in. This catches a
//     field that changed, moved, or appeared in a configuration the fixture covers. Recorded exactly as
//     internal/evalboard's board recording is, and for the same reason: a test that recomputes both
//     sides asserts only that Resolve is a function.
//
//  2. THE SHAPE (TestNoOwnershipVocabularyReachesTheHashedShape). The recording can only speak about
//     configurations the fixture builds. An ownership field added as `omitempty` and left unset by
//     every fixture spec would produce identical bytes and slip straight through. So the hashed TYPE is
//     read directly, and the whole ownership vocabulary is banned from it.
//
//  3. INVARIANCE UNDER THE ACTOR. Neither of the above watches a hash computed while a tenant is
//     actually in hand. That one lives where ownership enters the path — internal/submit, in
//     p27_ownership_invariance_test.go.
//
// # RECORDING
//
//	git worktree add /tmp/pre-p27 <pre-P27-commit> --detach
//	cp internal/variantspec/p27_hash_recording_test.go /tmp/pre-p27/internal/variantspec/
//	cd /tmp/pre-p27 && GOWORK=off P27_RECORD_PRE=1 go test ./internal/variantspec/ -run TestPreP27ConfigHashes
//	cp /tmp/pre-p27/internal/variantspec/testdata/p27-pre-confighash.json internal/variantspec/testdata/
//
// Re-recording from THIS tree destroys the only evidence the test carries. Don't.

const preHashFixture = "testdata/p27-pre-confighash.json"

// recordedResolution is one spec's resolution, as recorded. The canonical BYTES are kept beside the
// hash on purpose: a hash comparison says a configuration moved and nothing else, while the bytes say
// which key did it — and a diff of two 64-hex strings is the least useful failure message in the
// codebase.
type recordedResolution struct {
	Name       string          `json:"name"`
	Why        string          `json:"why"`
	Canonical  string          `json:"canonical_json"`
	ConfigHash string          `json:"config_hash"`
	Raw        json.RawMessage `json:"resolved_config"`
}

// recordingSpecs are the four configurations the fixture covers. They are chosen to span the hash's
// structure rather than to be realistic: the discovered defaults, a node with every registry-backed
// dimension overridden, the same overrides under a different node order, and an additive omitempty
// field actually present.
func recordingSpecs(t *testing.T) []recordedResolution {
	t.Helper()
	ctx := context.Background()

	out := []recordedResolution{
		resolveForRecord(t, ctx, "discovered-defaults",
			"a spec that overrides nothing still hashes a COMPLETE configuration — the dimensions nobody "+
				"overrode are pinned by source_revision. This is the shape most stored rows have.",
			func(s *VariantSpec) {}, recRegistries(t)),

		resolveForRecord(t, ctx, "every-dimension-overridden",
			"model, prompt, skills and context all resolved through the registries on one node. Covers "+
				"every projection that turns a version_id into a hashed value.",
			func(s *VariantSpec) {
				s.Nodes["n_a"] = NodeOverride{
					ModelRef:      "m-openai",
					PromptRef:     "p-triage",
					SkillRefs:     []string{"sk-search", "sk-lookup"},
					ContextPolicy: "cx-window",
				}
			}, recRegistries(t)),

		resolveForRecord(t, ctx, "node-order-reversed",
			"the SAME overrides with the node order flipped. Node order is identity-bearing (JCS does not "+
				"sort arrays), so this hash must differ from the one above — which is what makes the pair "+
				"evidence that ordering still counts, rather than two rows that happen to agree.",
			func(s *VariantSpec) {
				s.Order = []string{"n_b", "n_a"}
				s.Nodes["n_a"] = NodeOverride{
					ModelRef:      "m-openai",
					PromptRef:     "p-triage",
					SkillRefs:     []string{"sk-search", "sk-lookup"},
					ContextPolicy: "cx-window",
				}
			}, recRegistries(t)),

		resolveForRecord(t, ctx, "additive-field-present",
			"an authored context_drop_tolerance: one of the omitempty fields later phases added. Included "+
				"so the recording covers a PRESENT additive key and not only absent ones.",
			func(s *VariantSpec) {
				s.Nodes["n_b"] = NodeOverride{ContextDropTolerance: ptrF(0.15)}
			}, recRegistries(t)),
	}
	return out
}

func resolveForRecord(t *testing.T, ctx context.Context, name, why string, mutate func(*VariantSpec), regs Registries) recordedResolution {
	t.Helper()
	spec := recSpec()
	mutate(spec)
	got, err := Resolve(ctx, spec, recIR(), regs)
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
	return recordedResolution{Name: name, Why: why, Canonical: string(canon), ConfigHash: got.ConfigHash, Raw: raw}
}

// recIR / recSpec / recRegistries are this file's own fixtures rather than resolve_test.go's, for the
// reason internal/evalboard's recorder builds its own board: the recorded bytes only mean something if
// the inputs behind them travel in the same file that gets copied into the pre-P27 checkout.
func recIR() *discovery.IR {
	return &discovery.IR{
		IRVersion: "1.0.0",
		Nodes: []discovery.IRNode{
			{
				NodeID: "n_a", Kind: "static_definition",
				CallSite:        discovery.IRCallSite{File: "triage.go", Symbol: "classify", LineStart: 11, LineEnd: 11},
				Model:           discovery.IRModel{Provider: "anthropic", ModelID: "claude-opus-4-8", Params: map[string]any{}},
				Prompt:          discovery.IRPrompt{Inline: "classify this ticket", Variables: []string{}},
				ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages"},
			},
			{
				NodeID: "n_b", Kind: "static_definition",
				CallSite:        discovery.IRCallSite{File: "triage.go", Symbol: "respond", LineStart: 23, LineEnd: 23},
				Model:           discovery.IRModel{Provider: "anthropic", ModelID: "claude-sonnet-5", Params: map[string]any{}},
				Prompt:          discovery.IRPrompt{Inline: "draft a reply", Variables: []string{}},
				ToolsSkills:     []string{"search_kb"},
				ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages"},
			},
		},
		Edges: []discovery.IREdge{},
	}
}

func recSpec() *VariantSpec {
	return &VariantSpec{
		WorkflowID:     "wf-p27-recording",
		SourceRevision: "9f1c2d3e4a5b6c7d8e9f0a1b2c3d4e5f60718293",
		Order:          []string{"n_a", "n_b"},
		Nodes:          map[string]NodeOverride{},
		Edges:          []Edge{{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "data"}},
	}
}

// recStore is a Registries whose only job is to return the entries below. It is not a test double for
// Postgres — Resolve's contract is "fail closed on anything that does not resolve", and what a
// recording needs is control over what resolves.
type recStore struct{ t *testing.T }

func recRegistries(t *testing.T) Registries { return recStore{t: t} }

func (r recStore) ResolveModel(_ context.Context, id string) (*registry.ModelEntry, error) {
	if id != "m-openai" {
		return nil, registry.ErrNotFound
	}
	return &registry.ModelEntry{VersionID: "m-openai", Name: "triage-model",
		Spec: registry.ModelSpec{Provider: "openai", ModelID: "gpt-5",
			Params: registry.ModelParams{Temperature: ptrF(0.2), MaxTokens: ptrI(1024)}}}, nil
}

// ResolvePrompt returns an entry with a PARSED template. Resolve reads the template's slots to validate
// bindings, so an entry without one is not a lighter fixture — it is a nil dereference. Parsed through
// the registry's own parser rather than hand-built, so the slot set the recording resolves against is
// the one the production path would derive from the same body.
func (r recStore) ResolvePrompt(_ context.Context, id string) (*registry.PromptEntry, error) {
	if id != "p-triage" {
		return nil, registry.ErrNotFound
	}
	tpl, err := registry.ParseTemplate("triage the ticket: {{ticket}}")
	if err != nil {
		return nil, err
	}
	return &registry.PromptEntry{VersionID: "p-triage", Name: "triage", Template: tpl}, nil
}

func (r recStore) ResolveSkill(_ context.Context, id string) (*registry.SkillEntry, error) {
	switch id {
	case "sk-search":
		return &registry.SkillEntry{VersionID: "sk-search", Name: "search_kb"}, nil
	case "sk-lookup":
		return &registry.SkillEntry{VersionID: "sk-lookup", Name: "issue_lookup"}, nil
	}
	return nil, registry.ErrNotFound
}

func (r recStore) ResolveContextPolicy(_ context.Context, id string) (*registry.ContextEntry, error) {
	if id != "cx-window" {
		return nil, registry.ErrNotFound
	}
	return &registry.ContextEntry{VersionID: "cx-window", Name: "window",
		Spec: registry.ContextSpec{Policy: "sliding_window", Params: json.RawMessage(`{"turns":8}`)}}, nil
}

func (r recStore) ResolveMemory(_ context.Context, _ string) (*registry.MemoryEntry, error) {
	return nil, registry.ErrNotFound
}

func (r recStore) ResolveHarness(_ context.Context, _ string) (*registry.HarnessEntry, error) {
	return nil, registry.ErrNotFound
}

// ResolveLoop answers for P34's loop registry. This fixture publishes no loop entries, so every
// loop_ref misses — which is the fail-closed answer, not an empty one.
func (r recStore) ResolveLoop(context.Context, string) (*registry.LoopEntry, error) {
	return nil, registry.ErrNotFound
}

// ── 1. the recording ─────────────────────────────────────────────────────────────────────────────────

func TestPreP27ConfigHashesAreReproducedExactly(t *testing.T) {
	got := recordingSpecs(t)

	if os.Getenv("P27_RECORD_PRE") == "1" {
		writeHashRecording(t, got)
		t.Skip("recorded the pre-P27 config hashes; this mode must only ever run in a pre-P27 checkout")
	}

	want := readHashRecording(t)
	if len(got) != len(want) {
		t.Fatalf("the recording covers %d configurations, this tree resolves %d", len(want), len(got))
	}
	for i := range want {
		w, g := want[i], got[i]
		if g.Name != w.Name {
			t.Fatalf("fixture %d: the recording is of %q, this tree built %q — the fixtures diverged, "+
				"and nothing below is a comparison of like with like", i, w.Name, g.Name)
		}
		// Bytes before hash. If both moved, the bytes say WHICH key did it; if only the hash moved, the
		// canonicalizer changed and that is a different and much worse bug.
		if g.Canonical != w.Canonical {
			t.Errorf("%s: the canonical bytes changed.\n  before P27: %s\n  now:        %s",
				w.Name, w.Canonical, g.Canonical)
		}
		if g.ConfigHash != w.ConfigHash {
			t.Errorf("%s: config_hash = %s, was %s before P27. Every result cached under the old hash is "+
				"now unreachable from this configuration.", w.Name, g.ConfigHash, w.ConfigHash)
		}
		if g.Canonical == w.Canonical && g.ConfigHash != w.ConfigHash {
			t.Errorf("%s: identical canonical bytes hashed to a different value — the digest itself moved, "+
				"not the configuration", w.Name)
		}
	}
}

// TestTheHashRecordingStillDistinguishesConfigurations keeps the recording honest. Four rows that all
// carried the SAME hash would compare equal forever and assert nothing about what config_hash denotes.
func TestTheHashRecordingStillDistinguishesConfigurations(t *testing.T) {
	want := readHashRecording(t)
	seen := map[string]string{}
	for _, r := range want {
		if r.ConfigHash == "" || len(r.ConfigHash) != 64 {
			t.Errorf("%s: recorded config_hash %q is not 64 hex chars", r.Name, r.ConfigHash)
		}
		if prev, dup := seen[r.ConfigHash]; dup {
			t.Errorf("%s and %s share a config_hash. These are different configurations; a recording in "+
				"which they collide is not evidence that the hash distinguishes anything.", prev, r.Name)
		}
		seen[r.ConfigHash] = r.Name
	}
	if len(want) < 4 {
		t.Errorf("the recording covers %d configurations; it was written to cover four distinct shapes", len(want))
	}
}

// ── 2. the shape ─────────────────────────────────────────────────────────────────────────────────────

// TestNoOwnershipVocabularyReachesTheHashedShape reads the hashed TYPE rather than any value of it.
//
// 🔴 This is the half of 9.1 the recording cannot do. An ownership field added as `omitempty` and left
// unset by every fixture spec produces byte-identical output, so the recording stays green while the
// hash has acquired the ability to fork one configuration per organization the moment anything sets it.
// The failure would then surface as a board that had lost half its history, months later, with no
// change nearby to blame.
//
// The ban is on the VOCABULARY, not on a list of known fields, because the thing being prevented is a
// field nobody has written yet.
func TestNoOwnershipVocabularyReachesTheHashedShape(t *testing.T) {
	// Every word P27 introduced that names WHO, plus the ones the platform already used for it. A
	// configuration is a description of a computation; none of these belong in one.
	banned := []string{
		"tenant", "owner", "user", "account", "organization", "org_", "member",
		"seat", "invitation", "principal", "actor", "subject", "issuer", "credential", "session",
	}
	for _, f := range hashedJSONFields(reflect.TypeOf(ResolvedConfig{}), map[reflect.Type]bool{}) {
		lower := strings.ToLower(f.tag)
		for _, b := range banned {
			if strings.Contains(lower, b) {
				t.Errorf("%s carries %q, which names WHO rather than WHAT. config_hash denotes a "+
					"configuration; an ownership field in it forks one configuration per organization and "+
					"orphans every result already filed under the old hash.", f.path, f.tag)
			}
		}
	}
}

// TestTheVocabularyFenceCanActuallyFail is the fence on the fence. hashedJSONFields walks a struct
// graph, and a walk that silently returned nothing — a missed pointer indirection, a slice element type
// it did not follow — would make the ban above vacuous and permanently green.
func TestTheVocabularyFenceCanActuallyFail(t *testing.T) {
	fields := hashedJSONFields(reflect.TypeOf(ResolvedConfig{}), map[reflect.Type]bool{})
	if len(fields) < 20 {
		t.Fatalf("the walk found only %d hashed fields; ResolvedConfig's graph is larger than that, so "+
			"the ownership ban is running over a fraction of the shape it claims to cover", len(fields))
	}
	// Fields reached only through a pointer, a slice and a map respectively. If the walk stops at any of
	// those three indirections it stops before most of the hashed shape.
	for _, want := range []string{
		"ResolvedConfig.nodes[].node_id",               // slice of structs
		"ResolvedConfig.nodes[].memory.strategy",       // pointer to a struct
		"ResolvedConfig.nodes[].bindings{}.kind",       // map to a struct
		"ResolvedConfig.harness_groups[].edges[].kind", // slice inside a slice
	} {
		found := false
		for _, f := range fields {
			if f.path == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the walk never reached %s, so anything added there would pass the ownership ban "+
				"unexamined", want)
		}
	}
}

type hashedField struct {
	path string
	tag  string
}

// hashedJSONFields walks the hashed struct graph and returns every JSON key it can emit, with the path
// that reaches it. Only exported fields with a json tag participate — which is exactly the set that
// reaches the canonical bytes.
func hashedJSONFields(t reflect.Type, seen map[reflect.Type]bool) []hashedField {
	return walkHashed(t, t.Name(), seen)
}

func walkHashed(t reflect.Type, path string, seen map[reflect.Type]bool) []hashedField {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	// Cycle guard only — the same type reached by two paths is walked under both, because a banned word
	// has to be reported at the path a reader can find it.
	if seen[t] {
		return nil
	}
	seen[t] = true
	defer delete(seen, t)

	var out []hashedField
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" { // unexported: never marshalled, never hashed
			continue
		}
		tag := strings.Split(f.Tag.Get("json"), ",")[0]
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = f.Name
		}
		here := path + "." + tag
		out = append(out, hashedField{path: here, tag: tag})

		ft := f.Type
		for ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Slice, reflect.Array:
			out = append(out, walkHashed(ft.Elem(), here+"[]", seen)...)
		case reflect.Map:
			out = append(out, walkHashed(ft.Elem(), here+"{}", seen)...)
		case reflect.Struct:
			out = append(out, walkHashed(ft, here, seen)...)
		}
	}
	return out
}

// ── fixture I/O ──────────────────────────────────────────────────────────────────────────────────────

func writeHashRecording(t *testing.T, rs []recordedResolution) {
	t.Helper()
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	b, err := json.MarshalIndent(rs, "", "  ")
	if err != nil {
		t.Fatalf("marshal recording: %v", err)
	}
	if err := os.WriteFile(preHashFixture, append(b, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", preHashFixture, err)
	}
	t.Logf("recorded %s (%d configurations)", preHashFixture, len(rs))
}

func readHashRecording(t *testing.T) []recordedResolution {
	t.Helper()
	raw, err := os.ReadFile(preHashFixture)
	if err != nil {
		t.Fatalf("read the pre-P27 config_hash recording: %v\n"+
			"This fixture is EVIDENCE, not a cache. It is re-recorded from a pre-P27 checkout only — see "+
			"this file's header. Do not regenerate it here.", err)
	}
	var rs []recordedResolution
	if err := json.Unmarshal(raw, &rs); err != nil {
		t.Fatalf("parse the pre-P27 config_hash recording: %v", err)
	}
	return rs
}
