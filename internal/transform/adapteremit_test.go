package transform

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

func renameAdapter() variantspec.InsertedAdapter {
	return variantspec.InsertedAdapter{
		AdapterNodeID: "adapter:rename:A->B",
		FromNodeID:    "A", ToNodeID: "B", CatalogKind: "rename",
		Params:   map[string]any{"renames": []map[string]any{{"from": "answer", "to": "response"}}},
		InSchema: map[string]any{"type": "object", "properties": map[string]any{"answer": map[string]any{"type": "string"}}},
		OutSchema: map[string]any{"type": "object", "properties": map[string]any{"response": map[string]any{"type": "string"}},
			"required": []any{"response"}},
	}
}

// TASK 2.2: the adapter is emitted as inspectable, valid Go source.
func TestEmitAdapter_ParsesAsGo(t *testing.T) {
	gf, err := EmitAdapter(renameAdapter(), "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.ParseFile(token.NewFileSet(), gf.Path, gf.Content, parser.ParseComments); err != nil {
		t.Fatalf("emitted adapter must parse as Go: %v\n%s", err, gf.Content)
	}
	src := string(gf.Content)
	if !strings.Contains(src, `out["response"] = v`) || !strings.Contains(src, `delete(out, "answer")`) {
		t.Fatalf("rename adapter must move answer→response:\n%s", src)
	}
	// It carries its own io_contract in the header (explicit, inspectable node).
	if !strings.Contains(src, `io_contract`) || !strings.Contains(src, `"response"`) {
		t.Fatalf("adapter must carry its io_contract:\n%s", src)
	}
}

// TASK 2.4: emission is deterministic — byte-identical on regeneration.
func TestEmitAdapter_Deterministic(t *testing.T) {
	first, err := EmitAdapter(renameAdapter(), "go")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		got, err := EmitAdapter(renameAdapter(), "go")
		if err != nil {
			t.Fatal(err)
		}
		if string(got.Content) != string(first.Content) || got.Path != first.Path {
			t.Fatalf("non-deterministic emission")
		}
	}
}

// An UNSUPPORTED target is refused with a named reason, never emitted as broken syntax.
func TestEmitAdapter_RefusesUnsupportedLanguage(t *testing.T) {
	if _, err := EmitAdapter(renameAdapter(), "ruby"); err == nil {
		t.Fatalf("must refuse an unsupported target")
	}
}

// A Python target emits a valid, inspectable Python adapter that parses and carries its io_contract.
func TestEmitAdapter_Python(t *testing.T) {
	gf, err := EmitAdapter(renameAdapter(), "python")
	if err != nil {
		t.Fatalf("python emission must succeed: %v", err)
	}
	if !strings.HasSuffix(gf.Path, ".py") {
		t.Fatalf("python adapter must be a .py file, got %s", gf.Path)
	}
	src := string(gf.Content)
	if !strings.Contains(src, `out["response"] = inp["answer"]`) || !strings.Contains(src, `del out["answer"]`) {
		t.Fatalf("python rename adapter must move answer→response:\n%s", src)
	}
	if !strings.Contains(src, "io_contract") || !strings.Contains(src, "def adapter_") {
		t.Fatalf("python adapter must carry its io_contract and a def:\n%s", src)
	}
}

// Every catalog kind emits parseable Go (no dead code / unused vars).
func TestEmitAdapter_AllKindsParse(t *testing.T) {
	kinds := []variantspec.InsertedAdapter{
		renameAdapter(),
		{AdapterNodeID: "a:fill", FromNodeID: "A", ToNodeID: "B", CatalogKind: "default_fill",
			Params:   map[string]any{"fills": []map[string]any{{"field": "lang", "value": "en"}}},
			InSchema: map[string]any{"type": "object"}, OutSchema: map[string]any{"type": "object"}},
		{AdapterNodeID: "a:wrap", FromNodeID: "A", ToNodeID: "B", CatalogKind: "wrap",
			Params:   map[string]any{"key": "payload"},
			InSchema: map[string]any{"type": "object"}, OutSchema: map[string]any{"type": "object"}},
		{AdapterNodeID: "a:unwrap", FromNodeID: "A", ToNodeID: "B", CatalogKind: "unwrap",
			Params:   map[string]any{"key": "inner"},
			InSchema: map[string]any{"type": "object"}, OutSchema: map[string]any{"type": "object"}},
		{AdapterNodeID: "a:coerce", FromNodeID: "A", ToNodeID: "B", CatalogKind: "coerce",
			Params:   map[string]any{"coercions": []map[string]any{{"field": "score", "to": "number"}}},
			InSchema: map[string]any{"type": "object"}, OutSchema: map[string]any{"type": "object"}},
	}
	for _, k := range kinds {
		gf, err := EmitAdapter(k, "go")
		if err != nil {
			t.Fatalf("%s: %v", k.CatalogKind, err)
		}
		if _, err := parser.ParseFile(token.NewFileSet(), gf.Path, gf.Content, parser.ParseComments); err != nil {
			t.Fatalf("%s adapter must parse: %v\n%s", k.CatalogKind, err, gf.Content)
		}
	}
}
