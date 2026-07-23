package typedcontract

import (
	"encoding/json"
	"testing"
)

// TASK 1.2 — the parity chain, asserted directly on the shared predicate.
//
// The property: if Satisfies(producerOut, consumerIn) is ok, then any VALUE that conforms to the
// producer's output_schema (which the runtime Executor guarantees for every node's output) also
// conforms to the consumer's input_schema. So a statically-coherent edge cannot halt at runtime.
func TestParity_CoherentEdgeAcceptsProducerValuesAtRuntime(t *testing.T) {
	producerOut := obj(map[string]any{"summary": field("string"), "extra": field("number")})
	consumerIn := obj(map[string]any{"summary": field("string")}, "summary")

	if !Satisfies(producerOut, consumerIn).OK {
		t.Fatalf("precondition: the edge must be statically coherent")
	}

	// A value the Executor would accept as producer output (conforms to producerOut).
	consumerSchema, err := CompileSchema(consumerIn)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal([]byte(`{"summary":"done","extra":3}`), &value); err != nil {
		t.Fatal(err)
	}
	// The runtime consumer-side check must accept it — no contract halt.
	if err := ValidateValue(consumerSchema, value); err != nil {
		t.Fatalf("a statically-coherent edge halted at runtime: %v", err)
	}
}

// The contrapositive: a value that a rejected edge's producer emits is refused by the consumer schema
// at runtime — so rejecting statically and halting at runtime agree.
func TestParity_IncoherentEdgeHaltsAtRuntime(t *testing.T) {
	consumerIn := obj(map[string]any{"summary": field("string")}, "summary")
	if Satisfies(obj(map[string]any{"answer": field("string")}), consumerIn).OK {
		t.Fatalf("precondition: the edge must be statically incoherent")
	}
	consumerSchema, err := CompileSchema(consumerIn)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	_ = json.Unmarshal([]byte(`{"answer":"done"}`), &value) // producer emits `answer`, not `summary`
	if err := ValidateValue(consumerSchema, value); err == nil {
		t.Fatalf("the runtime consumer check must refuse a value missing the required field")
	}
}

// CompileSchema refuses a remote $ref, exactly as the Executor does — the hermetic guarantee is shared.
func TestCompileSchema_RefusesRemoteRef(t *testing.T) {
	_, err := CompileSchema(map[string]any{"$ref": "https://evil.example/schema.json"})
	if err == nil {
		t.Fatalf("a remote $ref must be refused")
	}
}
