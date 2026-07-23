package typedcontract

import "testing"

// A field-rename mismatch (answer→response) is bridged by a rename adapter carrying its own contract.
func TestFindAdapter_Rename(t *testing.T) {
	producer := obj(map[string]any{"answer": field("string")})
	consumer := obj(map[string]any{"response": field("string")}, "response")
	a, ok := DefaultCatalog().FindAdapter(producer, consumer)
	if !ok || a.Kind != KindRename {
		t.Fatalf("want a rename adapter, got %+v ok=%v", a, ok)
	}
	// The adapter is validated on both sides: producer satisfies its input, its output satisfies consumer.
	if !Satisfies(producer, a.InSchema).OK {
		t.Fatalf("adapter input must be satisfied by producer")
	}
	if !Satisfies(a.OutSchema, consumer).OK {
		t.Fatalf("adapter output must satisfy consumer")
	}
	renames := a.Params["renames"].([]map[string]any)
	if renames[0]["from"] != "answer" || renames[0]["to"] != "response" {
		t.Fatalf("rename params wrong: %+v", renames)
	}
}

// TASK 2.3 / spec: an adapter that would satisfy the consumer only by dropping another required field
// is refused — the edge is treated as incoherent, not silently coerced.
func TestFindAdapter_RefusesToDropRequiredField(t *testing.T) {
	// Producer emits one spare field. Consumer requires TWO fields. A rename can supply at most one; the
	// second required field has no source, so no admissible adapter exists.
	producer := obj(map[string]any{"answer": field("string")})
	consumer := obj(map[string]any{"response": field("string"), "citations": field("array")}, "response", "citations")
	if a, ok := DefaultCatalog().FindAdapter(producer, consumer); ok {
		t.Fatalf("must refuse: no adapter can supply both required fields without dropping one, got %+v", a)
	}
}

func TestFindAdapter_DefaultFill(t *testing.T) {
	producer := obj(map[string]any{"answer": field("string")}, "answer")
	// Consumer requires `answer` (present) plus `lang` which declares a default.
	consumer := obj(map[string]any{
		"answer": field("string"),
		"lang":   map[string]any{"type": "string", "default": "en"},
	}, "answer", "lang")
	a, ok := DefaultCatalog().FindAdapter(producer, consumer)
	if !ok || a.Kind != KindDefaultFill {
		t.Fatalf("want default_fill, got %+v ok=%v", a, ok)
	}
	if !Satisfies(a.OutSchema, consumer).OK {
		t.Fatalf("filled output must satisfy consumer")
	}
}

// Wrap: consumer requires a single object-typed field whose value is the producer's whole output.
func TestFindAdapter_Wrap(t *testing.T) {
	producer := obj(map[string]any{"a": field("string"), "b": field("number")})
	consumer := obj(map[string]any{"payload": field("object")}, "payload")
	a, ok := DefaultCatalog().FindAdapter(producer, consumer)
	if !ok || a.Kind != KindWrap {
		t.Fatalf("want wrap, got %+v ok=%v", a, ok)
	}
	if a.Params["key"] != "payload" {
		t.Fatalf("wrap key wrong: %+v", a.Params)
	}
	if !Satisfies(a.OutSchema, consumer).OK {
		t.Fatalf("wrapped output must satisfy consumer")
	}
}
