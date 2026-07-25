package variantspec

import (
	"context"
	"errors"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
)

// P10 variable-bindings tests (task 3.9): one case per failure class, each asserting the error names
// node/dimension/slot and that it fires at RESOLVE — before any transformation, worktree, or build —
// plus the backward-compatibility case (a spec with no bindings hashes byte-identically to today).

// promptEntry builds a resolvable prompt version with the given slots for a test.
func promptEntry(t *testing.T, id, body string) *registry.PromptEntry {
	t.Helper()
	tmpl, err := registry.ParseTemplate(body)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}
	return &registry.PromptEntry{VersionID: id, Name: "p", Template: tmpl,
		Spec: registry.PromptSpec{Slots: tmpl.Slots()}}
}

// irWithScope returns an IR whose single node records an in-scope symbol set and a declared env set —
// a P10-aware discovery, which engages resolve-time binding enforcement.
func irWithScope(inScope, declaredEnv []string, inputProps []string) *discovery.IR {
	props := map[string]any{}
	for _, p := range inputProps {
		props[p] = map[string]any{"type": "string"}
	}
	return &discovery.IR{
		IRVersion:   "1.0.0",
		DeclaredEnv: declaredEnv,
		Nodes: []discovery.IRNode{{
			NodeID: "n", Kind: "static_definition",
			CallSite:        discovery.IRCallSite{File: "p.go", Symbol: "f", InScope: inScope},
			Model:           discovery.IRModel{Provider: "anthropic", ModelID: "m", Params: map[string]any{}},
			Prompt:          discovery.IRPrompt{Inline: "", Variables: []string{}},
			ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages"},
			IOContract:      discovery.IRIOContract{InputSchema: map[string]any{"properties": props}},
		}},
		Edges: []discovery.IREdge{},
	}
}

func specWith(promptID string, bindings map[string]BindingSource) *VariantSpec {
	return &VariantSpec{
		WorkflowID: "wf", SourceRevision: "rev",
		Order: []string{"n"},
		Nodes: map[string]NodeOverride{"n": {PromptRef: promptID, Bindings: bindings}},
		Edges: []Edge{},
	}
}

// assertSpecError asserts err is a *SpecError with the given sentinel, node, dim and ref, and that the
// registries were NOT consulted past the point of failure — proving resolution aborted before any
// downstream work. (calls > 0 is fine; what matters is the typed rejection.)
func assertSpecError(t *testing.T, err error, sentinel error, node string, dim Dimension, ref string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a resolve-time rejection, got nil")
	}
	var se *SpecError
	if !errors.As(err, &se) {
		t.Fatalf("error is not a *SpecError: %v", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("sentinel = %v, want %v", se.Err, sentinel)
	}
	if se.NodeID != node {
		t.Errorf("node = %q, want %q", se.NodeID, node)
	}
	if se.Dim != dim {
		t.Errorf("dimension = %q, want %q", se.Dim, dim)
	}
	if ref != "" && se.Ref != ref {
		t.Errorf("ref = %q, want %q", se.Ref, ref)
	}
}

func TestBinding_UnknownKindRejectedNamingSlot(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Hi {{name}}")
	spec := specWith("p1", map[string]BindingSource{"name": {Kind: "made_up", Value: "x"}})
	_, err := Resolve(context.Background(), spec, irWithScope([]string{}, nil, nil), regs)
	assertSpecError(t, err, ErrUnknownBindingKind, "n", DimPrompt, "name")
}

func TestBinding_UnsatisfiedSlotRejectedWithSlotNamed(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Hi {{name}} and {{extra}}")
	// name is bound; extra is neither bound nor in scope -> unsatisfied.
	spec := specWith("p1", map[string]BindingSource{"name": {Kind: BindLiteral, Value: "Ada"}})
	_, err := Resolve(context.Background(), spec, irWithScope([]string{}, nil, nil), regs)
	assertSpecError(t, err, ErrUnsatisfiedSlot, "n", DimPrompt, "extra")
}

func TestBinding_AmbiguousSlotRejected(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Hi {{ticket}}")
	// ticket is BOTH explicitly bound AND an identically-spelled in-scope call-site expression.
	spec := specWith("p1", map[string]BindingSource{"ticket": {Kind: BindLiteral, Value: "T-1"}})
	_, err := Resolve(context.Background(), spec, irWithScope([]string{"ticket"}, nil, nil), regs)
	assertSpecError(t, err, ErrAmbiguousSlot, "n", DimPrompt, "ticket")
}

func TestBinding_ExprOutOfScopeRejectedNamingExpr(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Hi {{name}}")
	spec := specWith("p1", map[string]BindingSource{"name": {Kind: BindExpr, Value: "not_in_scope"}})
	_, err := Resolve(context.Background(), spec, irWithScope([]string{"something_else"}, nil, nil), regs)
	assertSpecError(t, err, ErrBindingOutOfScope, "n", DimPrompt, "not_in_scope")
}

func TestBinding_ExprInScopeAccepted(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Hi {{name}}")
	spec := specWith("p1", map[string]BindingSource{"name": {Kind: BindExpr, Value: "user"}})
	got, err := Resolve(context.Background(), spec, irWithScope([]string{"user"}, nil, nil), regs)
	if err != nil {
		t.Fatalf("an in-scope expr must be accepted: %v", err)
	}
	if got.Config.Nodes[0].Bindings["name"].Value != "user" {
		t.Fatalf("resolved binding not recorded: %+v", got.Config.Nodes[0].Bindings)
	}
}

func TestBinding_UndeclaredEnvRejectedNamingVariable(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Region {{region}}")
	spec := specWith("p1", map[string]BindingSource{"region": {Kind: BindEnv, Value: "AWS_REGION"}})
	_, err := Resolve(context.Background(), spec, irWithScope([]string{}, []string{"OTHER_VAR"}, nil), regs)
	assertSpecError(t, err, ErrBindingOutOfScope, "n", DimPrompt, "AWS_REGION")
}

func TestBinding_DeclaredEnvAccepted(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Region {{region}}")
	spec := specWith("p1", map[string]BindingSource{"region": {Kind: BindEnv, Value: "AWS_REGION"}})
	_, err := Resolve(context.Background(), spec, irWithScope([]string{}, []string{"AWS_REGION"}, nil), regs)
	if err != nil {
		t.Fatalf("a declared env var must be accepted: %v", err)
	}
}

func TestBinding_InputContractViolationRejected(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Ticket {{ticket}}")
	// The input contract admits "customer" but not "ticket".
	spec := specWith("p1", map[string]BindingSource{"ticket": {Kind: BindInput, Value: "ticket"}})
	_, err := Resolve(context.Background(), spec, irWithScope([]string{}, nil, []string{"customer"}), regs)
	assertSpecError(t, err, ErrBindingOutOfScope, "n", DimPrompt, "ticket")
}

func TestBinding_InputContractSatisfiedAccepted(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Ticket {{ticket}}")
	spec := specWith("p1", map[string]BindingSource{"ticket": {Kind: BindInput, Value: "ticket"}})
	_, err := Resolve(context.Background(), spec, irWithScope([]string{}, nil, []string{"ticket"}), regs)
	if err != nil {
		t.Fatalf("a contract-admitted input must be accepted: %v", err)
	}
}

// ── config_hash (task 3.8/3.9) ─────────────────────────────────────────────────────────────────────

func TestBinding_NoBindingsHashesByteIdenticallyToPreP10(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Static prompt with no slots")
	// A spec with a prompt override but NO bindings must produce a resolved node with NO bindings key.
	spec := specWith("p1", nil)
	got, err := Resolve(context.Background(), spec, irWithScope(nil, nil, nil), regs)
	if err != nil {
		t.Fatal(err)
	}
	canon, err := got.Config.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	// The canonical bytes must not contain a "bindings" key when no binding was declared — that is what
	// keeps every pre-P10 config_hash reproducible.
	if bytesContain(canon, `"bindings"`) {
		t.Fatalf("a spec with no bindings emitted a bindings key:\n%s", canon)
	}
}

func TestBinding_ChangingABindingChangesTheHash(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Hi {{name}}")

	h := func(val string) string {
		spec := specWith("p1", map[string]BindingSource{"name": {Kind: BindLiteral, Value: val}})
		got, err := Resolve(context.Background(), spec, irWithScope([]string{}, nil, nil), regs)
		if err != nil {
			t.Fatal(err)
		}
		return got.ConfigHash
	}
	if h("Ada") == h("Grace") {
		t.Fatal("changing a binding value must change config_hash")
	}
}

func TestBinding_OrderIndependentHash(t *testing.T) {
	regs := newFakeRegistries()
	regs.prompts["p1"] = promptEntry(t, "p1", "Hi {{a}} {{b}}")

	// Two specs with the same bindings; Go map iteration order is randomised, but JCS sorts object keys,
	// so the hash must not depend on insertion order. Resolve many times to shake out any order leak.
	spec := specWith("p1", map[string]BindingSource{
		"a": {Kind: BindLiteral, Value: "1"},
		"b": {Kind: BindLiteral, Value: "2"},
	})
	first, err := Resolve(context.Background(), spec, irWithScope([]string{}, nil, nil), regs)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		got, err := Resolve(context.Background(), spec, irWithScope([]string{}, nil, nil), regs)
		if err != nil {
			t.Fatal(err)
		}
		if got.ConfigHash != first.ConfigHash {
			t.Fatal("config_hash depends on binding serialization order")
		}
	}
}

func bytesContain(b []byte, sub string) bool {
	s := string(b)
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
