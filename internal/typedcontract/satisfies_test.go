package typedcontract

import "testing"

// obj builds a JSON-Schema object document from properties and a required list, mirroring what the IR
// carries. Values are the raw shapes json.Unmarshal would produce (map[string]any, []any).
func obj(props map[string]any, required ...string) map[string]any {
	req := make([]any, len(required))
	for i, r := range required {
		req[i] = r
	}
	out := map[string]any{"type": "object", "properties": props}
	if len(req) > 0 {
		out["required"] = req
	}
	return out
}

func field(t string) map[string]any { return map[string]any{"type": t} }

func TestSatisfies_MissingRequiredField(t *testing.T) {
	producer := obj(map[string]any{"answer": field("string")}) // no `summary`
	consumer := obj(map[string]any{"summary": field("string")}, "summary")
	res := Satisfies(producer, consumer)
	if res.OK {
		t.Fatalf("want mismatch, got ok")
	}
	if len(res.Mismatches) != 1 || res.Mismatches[0].Field != "summary" || res.Mismatches[0].Reason != ReasonMissing {
		t.Fatalf("want a single missing-summary mismatch, got %+v", res.Mismatches)
	}
}

func TestSatisfies_ExtraProducerFieldsAllowed(t *testing.T) {
	producer := obj(map[string]any{"summary": field("string"), "extra": field("number"), "more": field("boolean")})
	consumer := obj(map[string]any{"summary": field("string")}, "summary")
	if res := Satisfies(producer, consumer); !res.OK {
		t.Fatalf("extra producer fields must not break satisfaction, got %+v", res.Mismatches)
	}
}

func TestSatisfies_PermissiveConsumerAcceptsAnything(t *testing.T) {
	producer := obj(map[string]any{"whatever": field("string")})
	consumer := map[string]any{"type": "object"} // no required
	if res := Satisfies(producer, consumer); !res.OK {
		t.Fatalf("a permissive consumer requires nothing and must be satisfied, got %+v", res.Mismatches)
	}
}

func TestSatisfies_PermissiveProducerFailsStrictConsumer(t *testing.T) {
	producer := map[string]any{"type": "object"} // guarantees no field
	consumer := obj(map[string]any{"summary": field("string")}, "summary")
	if res := Satisfies(producer, consumer); res.OK {
		t.Fatalf("a permissive producer guarantees nothing and cannot satisfy a strict consumer")
	}
}

func TestSatisfies_TypeIncompatible(t *testing.T) {
	producer := obj(map[string]any{"n": field("string")})
	consumer := obj(map[string]any{"n": field("number")}, "n")
	res := Satisfies(producer, consumer)
	if res.OK || res.Mismatches[0].Reason != ReasonTypeIncompatible {
		t.Fatalf("string→number must be type_incompatible, got %+v", res)
	}
}

func TestSatisfies_IntegerSatisfiesNumber(t *testing.T) {
	producer := obj(map[string]any{"n": field("integer")})
	consumer := obj(map[string]any{"n": field("number")}, "n")
	if res := Satisfies(producer, consumer); !res.OK {
		t.Fatalf("integer is a subtype of number and must satisfy it, got %+v", res.Mismatches)
	}
	// The reverse must NOT hold: a number producer cannot satisfy an integer consumer.
	if res := Satisfies(obj(map[string]any{"n": field("number")}), obj(map[string]any{"n": field("integer")}, "n")); res.OK {
		t.Fatalf("number must not satisfy integer")
	}
}

func TestSatisfies_Deterministic(t *testing.T) {
	producer := obj(map[string]any{"a": field("string")})
	consumer := obj(map[string]any{"z": field("string"), "a": field("string"), "m": field("string")}, "z", "a", "m")
	first := Satisfies(producer, consumer)
	for i := 0; i < 20; i++ {
		got := Satisfies(producer, consumer)
		if len(got.Mismatches) != len(first.Mismatches) {
			t.Fatalf("non-deterministic mismatch count")
		}
		for j := range got.Mismatches {
			if got.Mismatches[j].Field != first.Mismatches[j].Field {
				t.Fatalf("non-deterministic mismatch order: %v vs %v", got.Mismatches, first.Mismatches)
			}
		}
	}
	// z, a, m required; producer has only a → mismatches for m and z, sorted → [m, z].
	if len(first.Mismatches) != 2 || first.Mismatches[0].Field != "m" || first.Mismatches[1].Field != "z" {
		t.Fatalf("want sorted mismatches [m z], got %+v", first.Mismatches)
	}
}
