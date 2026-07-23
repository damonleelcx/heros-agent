// Package typedcontract implements P5's ordering-coherence validator: it enforces the per-node
// `io_contract` reserved on every IR node since P0 as a structural-subtype check over producer→consumer
// data edges, and it owns the ONE schema-satisfaction predicate the P2 Runtime Executor also enforces
// at runtime (Decision 1 — static/runtime parity).
//
// # Why one predicate, shared
//
// If the static validator used a looser rule than the runtime Executor, a Variant Spec could pass
// validation and then halt at runtime (or vice versa) — the "validator says yes, runtime says no" class
// of bug P5 exists to prevent. The parity chain is:
//
//	the Executor guarantees every node's output VALUE conforms to that node's own output_schema
//	    (validateValue, below, used by internal/executor);
//	Satisfies(producer.output_schema, consumer.input_schema) guarantees any value conforming to the
//	    producer's output_schema also conforms to the consumer's input_schema.
//
// Compose the two and a coherent ordering runs end-to-end with no contract halt — a structural
// guarantee, not a hope. Both halves are defined over the SAME JSON Schema semantics (the same compiler,
// the same hermetic offline loader), which is the concrete meaning of "shared predicate".
//
// # Fail closed
//
// Satisfies is total: every consumer-required field is either present-and-type-compatible or a named
// mismatch. There is no "unknown / allowed anyway" outcome — the load-bearing property is *no
// silently-broken reorder* (Decision 2), and that requires a total verdict.
package typedcontract

import (
	"fmt"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// MismatchReason is why one consumer-required field is not satisfied by the producer.
type MismatchReason string

const (
	// ReasonMissing: the producer's output_schema does not declare a field the consumer requires.
	ReasonMissing MismatchReason = "missing"
	// ReasonTypeIncompatible: the producer declares the field, but its type cannot flow into the
	// consumer's declared type (e.g. producer `string`, consumer `number`, no coercion).
	ReasonTypeIncompatible MismatchReason = "type_incompatible"
)

// FieldMismatch names one consumer-required field the producer fails to satisfy. Named, not counted,
// because the diagnostic must be specific enough for the UI to anchor on the offending edge (FR: "the
// diagnostic identifies the exact producer, consumer, and mismatching field(s)").
type FieldMismatch struct {
	Field        string         `json:"field"`
	Reason       MismatchReason `json:"reason"`
	ProducerType string         `json:"producer_type,omitempty"`
	ConsumerType string         `json:"consumer_type,omitempty"`
}

// SatisfyResult is the total verdict of one producer→consumer schema comparison. OK is true iff there
// are no mismatches; a caller must never treat an empty Mismatches with OK=false, or vice versa — the
// two are kept consistent by construction.
type SatisfyResult struct {
	OK         bool
	Mismatches []FieldMismatch
}

// Satisfies decides whether a producer's `output_schema` structurally satisfies a consumer's
// `input_schema`: every field the consumer *requires* must be present in and type-compatible with the
// producer's output; extra producer fields are permitted (structural subtyping). This is the predicate
// shared with the runtime Executor (Decision 1).
//
// Both schemas are P0 `io_contract` JSON Schemas (a `map[string]any`). A permissive consumer
// (`{"type":"object"}` — no `required`) is satisfied by any producer; a permissive producer provides no
// guaranteed field, so it satisfies only a consumer that requires nothing. This asymmetry is exactly
// why Decision 7's schema refinement *tightens* coherence over time: a permissive schema admits more
// orderings as coherent than a strict one would.
func Satisfies(producerOut, consumerIn map[string]any) SatisfyResult {
	required := requiredFields(consumerIn)
	if len(required) == 0 {
		return SatisfyResult{OK: true}
	}
	prodProps := properties(producerOut)
	consProps := properties(consumerIn)

	var out []FieldMismatch
	for _, field := range required { // requiredFields returns a sorted slice — determinism
		prodSchema, present := prodProps[field]
		if !present {
			out = append(out, FieldMismatch{Field: field, Reason: ReasonMissing})
			continue
		}
		pt := typeOf(prodSchema)
		ct := typeOf(consProps[field]) // consumer may not declare a type for a required field
		if !typeCompatible(pt, ct) {
			out = append(out, FieldMismatch{
				Field:        field,
				Reason:       ReasonTypeIncompatible,
				ProducerType: strings.Join(pt, "|"),
				ConsumerType: strings.Join(ct, "|"),
			})
		}
	}
	return SatisfyResult{OK: len(out) == 0, Mismatches: out}
}

// requiredFields returns the consumer's `required` array as a sorted, de-duplicated string slice.
// Sorted so that Satisfies — and everything built on it — is deterministic (FR: same ordering over the
// same IR yields the same verdict).
func requiredFields(schema map[string]any) []string {
	raw, ok := schema["required"].([]any)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// properties returns the schema's `properties` object as a map of field name → sub-schema. A schema
// with no `properties` yields an empty map (not nil), so callers never nil-check.
func properties(schema map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	raw, ok := schema["properties"].(map[string]any)
	if !ok {
		return out
	}
	for name, v := range raw {
		if sub, ok := v.(map[string]any); ok {
			out[name] = sub
		} else {
			out[name] = map[string]any{} // a `true`/`false` sub-schema: unconstrained for our purposes
		}
	}
	return out
}

// typeOf returns a field sub-schema's declared JSON Schema type(s) as a slice. JSON Schema permits a
// bare string (`"type":"string"`) or an array (`"type":["string","null"]`); both are normalised here.
// A field with no declared type returns an empty slice — "unconstrained", compatible with anything.
func typeOf(schema map[string]any) []string {
	if schema == nil {
		return nil
	}
	switch t := schema["type"].(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		sort.Strings(out)
		return out
	default:
		return nil
	}
}

// typeCompatible reports whether a producer field of the declared producer type(s) can flow into a
// consumer field of the declared consumer type(s). An unconstrained side (empty) is compatible with
// anything. `integer` is treated as a subtype of `number` (JSON Schema's own numeric hierarchy), so an
// integer producer satisfies a number consumer but not the reverse.
func typeCompatible(producer, consumer []string) bool {
	if len(producer) == 0 || len(consumer) == 0 {
		return true
	}
	accepted := map[string]bool{}
	for _, c := range consumer {
		accepted[c] = true
	}
	for _, p := range producer {
		if accepted[p] {
			continue
		}
		if p == "integer" && accepted["number"] {
			continue
		}
		return false
	}
	return true
}

// CompileSchema compiles a P0 `io_contract` JSON Schema (a `map[string]any`) into a validator. This is
// the SAME compilation the runtime Executor uses, extracted here so static validation and runtime
// enforcement share one JSON Schema engine and one hermetic loader — a remote $ref is refused, exactly
// as in the Skill Registry, so a contract check never depends on a third party.
func CompileSchema(schema map[string]any) (*jsonschema.Schema, error) {
	if schema == nil {
		return nil, fmt.Errorf("typedcontract: nil schema")
	}
	// Round-trip through the library's own JSON reader so the input is a plain schema document.
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(mustJSON(schema)))
	if err != nil {
		return nil, fmt.Errorf("typedcontract: io_contract is not valid JSON: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.UseLoader(offlineLoader{})
	const loc = "heros:io-contract"
	if err := c.AddResource(loc, doc); err != nil {
		return nil, fmt.Errorf("typedcontract: add schema resource: %w", err)
	}
	sch, err := c.Compile(loc)
	if err != nil {
		return nil, fmt.Errorf("typedcontract: io_contract is not a valid JSON Schema: %w", err)
	}
	return sch, nil
}

// ValidateValue checks a decoded JSON value against a compiled schema. This is the runtime half of the
// parity chain: the Executor calls it to guarantee a node's output conforms to that node's output
// schema, which — composed with a coherent Satisfies verdict — is what makes a validated ordering run
// with no contract halt.
func ValidateValue(schema *jsonschema.Schema, value any) error {
	return schema.Validate(value)
}

type offlineLoader struct{}

func (offlineLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("remote $ref %q is not allowed in an io_contract", url)
}
