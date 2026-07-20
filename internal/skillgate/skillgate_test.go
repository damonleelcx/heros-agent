package skillgate

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

// compile builds a compiled schema from a JSON document — the same hermetic compile the registry does,
// so a test SkillEntry carries real *jsonschema.Schema values without needing a database.
func compile(t *testing.T, raw string) *jsonschema.Schema {
	t.Helper()
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(raw)))
	if err != nil {
		t.Fatalf("bad schema JSON: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem:s", doc); err != nil {
		t.Fatal(err)
	}
	sch, err := c.Compile("mem:s")
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	return sch
}

// fakeResolver returns a canned entry or a not-found error, deterministically by version_id.
type fakeResolver struct {
	entries map[string]*registry.SkillEntry
}

func (r *fakeResolver) ResolveSkill(_ context.Context, versionID string) (*registry.SkillEntry, error) {
	if e, ok := r.entries[versionID]; ok {
		return e, nil
	}
	return nil, registry.ErrNotFound
}

func newGate(t *testing.T) (*Gate, string) {
	t.Helper()
	entry := &registry.SkillEntry{
		VersionID: "v_search",
		Name:      "search",
		Spec:      registry.SkillSpec{ImplHandle: "repo:tools/search.py"},
		Input:     compile(t, `{"type":"object","properties":{"query":{"type":"string"},"top_k":{"type":"integer","minimum":1}},"required":["query","top_k"],"additionalProperties":false}`),
		Output:    compile(t, `{"type":"object","properties":{"hits":{"type":"array"}},"required":["hits"],"additionalProperties":false}`),
	}
	g := New(&fakeResolver{entries: map[string]*registry.SkillEntry{"v_search": entry}}, nil)
	return g, "v_search"
}

// Task 2.3a / spec "Unavailable tool fails closed": a version that does not resolve fails closed before
// any implementation is considered.
func TestCheckInput_UnresolvableVersionFailsClosed(t *testing.T) {
	g, _ := newGate(t)
	_, err := g.CheckInput(context.Background(), "v_missing", map[string]any{})
	if !errors.Is(err, ErrContract) {
		t.Fatalf("want ErrContract, got %v", err)
	}
	var ce *ContractError
	if !errors.As(err, &ce) || ce.Kind != FailureUnavailable {
		t.Fatalf("want FailureUnavailable, got %v", err)
	}
}

// An unbindable impl handle is an unavailable tool (spec 2.3): fail closed, do not invoke.
func TestCheckInput_UnbindableHandleFailsClosed(t *testing.T) {
	entry := &registry.SkillEntry{
		VersionID: "v_bad", Name: "weird",
		Spec:   registry.SkillSpec{ImplHandle: "ftp://nope"}, // no bindable scheme
		Input:  compile(t, `{"type":"object"}`),
		Output: compile(t, `{"type":"object"}`),
	}
	g := New(&fakeResolver{entries: map[string]*registry.SkillEntry{"v_bad": entry}}, nil)
	_, err := g.CheckInput(context.Background(), "v_bad", map[string]any{})
	var ce *ContractError
	if !errors.As(err, &ce) || ce.Kind != FailureUnavailable {
		t.Fatalf("unbindable handle should fail closed as unavailable, got %v", err)
	}
}

// Task 2.3b / spec "Argument-schema violation rejected before the tool runs": a bad argument object is
// rejected with a typed error naming the skill and the violated field; the impl is never reached.
func TestCheckInput_ArgSchemaViolationNamesField(t *testing.T) {
	g, v := newGate(t)
	// top_k is present but out of range (minimum:1) — a specific field violation.
	_, err := g.CheckInput(context.Background(), v, map[string]any{"query": "hi", "top_k": 0})
	var ce *ContractError
	if !errors.As(err, &ce) {
		t.Fatalf("want *ContractError, got %v", err)
	}
	if ce.Kind != FailureInputSchema {
		t.Errorf("kind = %s, want input_schema", ce.Kind)
	}
	if ce.Skill != "search" {
		t.Errorf("error should name the skill, got %q", ce.Skill)
	}
	if ce.Field != "top_k" {
		t.Errorf("error should name the violated field, got %q", ce.Field)
	}
}

// A valid argument object passes and returns the resolved entry for the caller to invoke + later check.
func TestCheckInput_ValidArgsReturnEntry(t *testing.T) {
	g, v := newGate(t)
	entry, err := g.CheckInput(context.Background(), v, map[string]any{"query": "hi", "top_k": 3})
	if err != nil {
		t.Fatalf("valid args rejected: %v", err)
	}
	if entry == nil || entry.Name != "search" {
		t.Fatalf("expected resolved entry, got %+v", entry)
	}
}

// Task 2.4 / spec "Output-schema violation discarded before propagation": a result violating the output
// schema is rejected with a typed error naming the skill and field.
func TestCheckOutput_ViolationFailsClosed(t *testing.T) {
	g, v := newGate(t)
	entry, err := g.CheckInput(context.Background(), v, map[string]any{"query": "hi", "top_k": 3})
	if err != nil {
		t.Fatal(err)
	}
	// Missing the required `hits` field.
	err = g.CheckOutput(entry, map[string]any{"wrong": 1})
	var ce *ContractError
	if !errors.As(err, &ce) || ce.Kind != FailureOutputSchema {
		t.Fatalf("want output_schema failure, got %v", err)
	}
	if ce.Skill != "search" {
		t.Errorf("error should name the skill, got %q", ce.Skill)
	}
	// A conforming result passes.
	if err := g.CheckOutput(entry, map[string]any{"hits": []any{}}); err != nil {
		t.Errorf("conforming output rejected: %v", err)
	}
}

// Task 2.6 / spec "Validation result is reproducible": the same version_id + args yields the same
// verdict twice, independent of ambient host state (the gate reads none).
func TestValidation_IsDeterministic(t *testing.T) {
	g, v := newGate(t)
	args := map[string]any{"query": "hi", "top_k": 0}
	_, err1 := g.CheckInput(context.Background(), v, args)
	_, err2 := g.CheckInput(context.Background(), v, args)
	if (err1 == nil) != (err2 == nil) {
		t.Fatal("verdict changed between identical validations")
	}
	var c1, c2 *ContractError
	errors.As(err1, &c1)
	errors.As(err2, &c2)
	if c1.Kind != c2.Kind || c1.Field != c2.Field {
		t.Errorf("verdict not reproducible: %v vs %v", err1, err2)
	}
}
