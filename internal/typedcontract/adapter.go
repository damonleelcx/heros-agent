package typedcontract

import (
	"fmt"
	"sort"
)

// CatalogKind is one entry in the FIXED, versioned adapter catalog (Decision 3). The set is closed:
// a mismatch the catalog cannot express is incoherent (rejected), never best-effort-coerced. Adapters
// are drawn from a typed catalog — not LLM-authored transform code — so they stay analyzable and we
// never execute arbitrary generated code (which would need P3 sandboxing + P5.5 verification, out of
// scope).
type CatalogKind string

const (
	KindRename      CatalogKind = "rename"       // producer field F renamed to the consumer's required name R
	KindProjection  CatalogKind = "projection"   // extract a sub-field the consumer requires from a producer object
	KindWrap        CatalogKind = "wrap"         // producer value wrapped under a key the consumer requires
	KindUnwrap      CatalogKind = "unwrap"       // a single-key producer object unwrapped to its inner value
	KindDefaultFill CatalogKind = "default_fill" // a missing field supplied from a declared default
	KindCoerce      CatalogKind = "coerce"       // a declared, lossless format coercion (e.g. integer→number)
)

// CatalogVersion is bumped when the catalog grows. It is additive-only: a new kind never invalidates a
// previously-inserted adapter, so a stored Variant Spec's adapters stay meaningful.
const CatalogVersion = "1.0.0"

// AdapterMatch is the RESULT of catalog matching: a concrete adapter that bridges a specific mismatch,
// carrying its own `io_contract` (InSchema/OutSchema) so it can be validated and inserted as an
// explicit node. It deliberately does NOT carry a codemod — emission is the source-transformation
// engine's job (§2). This is the matching half: "does a catalog adapter bridge this edge, and if so,
// what is its typed shape".
type AdapterMatch struct {
	Kind CatalogKind `json:"kind"`
	// Params are the kind-specific parameters, e.g. rename: {"renames":[{"from":"answer","to":"response"}]}.
	// A map so §2's emitter is a pure function of it; sorted keys keep it deterministic.
	Params map[string]any `json:"params"`
	// InSchema/OutSchema are the adapter node's own io_contract. InSchema is satisfied by the upstream
	// producer; OutSchema satisfies the downstream consumer. Both are re-validated by ValidateOrdering
	// so the system never trusts an adapter it has not type-checked.
	InSchema  map[string]any `json:"in_schema"`
	OutSchema map[string]any `json:"out_schema"`
}

// Catalog is the fixed adapter catalog. Its only public behaviour in P5-§1 is FindAdapter (matching);
// §2 attaches the deterministic codemod emitter keyed by Kind.
type Catalog struct{}

// DefaultCatalog returns the fixed catalog.
func DefaultCatalog() *Catalog { return &Catalog{} }

// FindAdapter tries each catalog kind, in a fixed order, to bridge producer→consumer. It returns the
// first admissible adapter or (nil,false) if none applies — in which case the edge is incoherent and
// the ordering is rejected (Decision 2's fail-closed outcome, not a silent coercion).
//
// An adapter is *admissible* only if it drops nothing the consumer requires: its OutSchema must fully
// Satisfy the consumer, and its InSchema must be Satisfied by the producer. This is where task 2.3's
// "refuse any adapter that would silently drop a consumer-required field" is enforced structurally —
// a projection/rename that leaves any required field unsatisfied is rejected here, before it can be
// proposed as a code change.
func (c *Catalog) FindAdapter(producerOut, consumerIn map[string]any) (*AdapterMatch, bool) {
	// Fixed order: rename first (the common re-arrangement case), then structural, then value-supplying.
	// Order is deterministic and documented so the verdict is reproducible (FR: identical inserted
	// adapters on every evaluation).
	for _, try := range []func(map[string]any, map[string]any) (*AdapterMatch, bool){
		tryRename,
		tryCoerce,
		tryDefaultFill,
		tryWrap,
		tryUnwrap,
	} {
		if a, ok := try(producerOut, consumerIn); ok {
			// Structurally re-validate the synthesised adapter against BOTH sides. An adapter that does
			// not fully satisfy the consumer (e.g. would drop a required field) is not admissible.
			if !Satisfies(producerOut, a.InSchema).OK {
				continue
			}
			if !Satisfies(a.OutSchema, consumerIn).OK {
				continue
			}
			return a, true
		}
	}
	return nil, false
}

// tryRename bridges a mismatch where every consumer-required field the producer lacks can be supplied
// by renaming a spare producer field of a compatible type. It never drops a field: only spare producer
// fields (ones the consumer does not itself require) are eligible sources, and every missing field must
// be matched or the whole adapter is rejected.
func tryRename(producerOut, consumerIn map[string]any) (*AdapterMatch, bool) {
	prodProps := properties(producerOut)
	consProps := properties(consumerIn)
	required := requiredFields(consumerIn)

	var missing []string
	for _, f := range required {
		if _, ok := prodProps[f]; !ok {
			missing = append(missing, f)
		}
	}
	if len(missing) == 0 {
		return nil, false // nothing to rename — a pure type mismatch is tryCoerce's job
	}

	// Candidate sources: producer fields the consumer does not require (spare), sorted for determinism.
	var spare []string
	for name := range prodProps {
		if !contains(required, name) {
			spare = append(spare, name)
		}
	}
	sort.Strings(spare)

	used := map[string]bool{}
	var renames []map[string]any
	outProps := cloneProps(prodProps)
	for _, target := range missing { // missing is already sorted
		wantType := typeOf(consProps[target])
		src := ""
		for _, cand := range spare {
			if used[cand] {
				continue
			}
			if typeCompatible(typeOf(prodProps[cand]), wantType) {
				src = cand
				break
			}
		}
		if src == "" {
			return nil, false // a required field with no rename source — not a rename adapter
		}
		used[src] = true
		renames = append(renames, map[string]any{"from": src, "to": target})
		// Reflect the rename in the out-schema: drop the source name, add the target with the producer's type.
		delete(outProps, src)
		outProps[target] = prodProps[src]
	}

	return &AdapterMatch{
		Kind:      KindRename,
		Params:    map[string]any{"renames": renames},
		InSchema:  producerOut,
		OutSchema: objectSchema(outProps, required),
	}, true
}

// tryCoerce bridges a pure type mismatch the JSON numeric hierarchy makes lossless: a producer
// `integer` field feeding a consumer `number` field. It supplies no field and drops none — it only
// records a declared coercion so the emitter can widen the value.
func tryCoerce(producerOut, consumerIn map[string]any) (*AdapterMatch, bool) {
	prodProps := properties(producerOut)
	consProps := properties(consumerIn)
	required := requiredFields(consumerIn)

	var coercions []map[string]any
	outProps := cloneProps(prodProps)
	for _, f := range required {
		ps, present := prodProps[f]
		if !present {
			return nil, false // a missing field is not a coercion; leave it to another kind
		}
		pt, ct := typeOf(ps), typeOf(consProps[f])
		if typeCompatible(pt, ct) {
			continue // already compatible
		}
		// Only the declared, lossless integer→number widening is admitted. Anything else (string→number,
		// number→integer) risks data loss and is deliberately NOT a catalog coercion.
		if len(pt) == 1 && pt[0] == "integer" && contains(ct, "number") {
			coercions = append(coercions, map[string]any{"field": f, "from": "integer", "to": "number"})
			outProps[f] = map[string]any{"type": "number"}
			continue
		}
		return nil, false
	}
	if len(coercions) == 0 {
		return nil, false
	}
	return &AdapterMatch{
		Kind:      KindCoerce,
		Params:    map[string]any{"coercions": coercions},
		InSchema:  producerOut,
		OutSchema: objectSchema(outProps, required),
	}, true
}

// tryDefaultFill supplies a consumer-required field the producer lacks, when the consumer's schema
// declares a `default` for it. This never overwrites producer data — it only fills an absent field —
// and it is admitted only when every missing field has a declared default.
func tryDefaultFill(producerOut, consumerIn map[string]any) (*AdapterMatch, bool) {
	prodProps := properties(producerOut)
	consProps := properties(consumerIn)
	required := requiredFields(consumerIn)

	var fills []map[string]any
	outProps := cloneProps(prodProps)
	for _, f := range required {
		if _, ok := prodProps[f]; ok {
			continue
		}
		def, hasDefault := consProps[f]["default"]
		if !hasDefault {
			return nil, false // a missing field with no declared default is not fillable
		}
		fills = append(fills, map[string]any{"field": f, "value": def})
		outProps[f] = consProps[f]
	}
	if len(fills) == 0 {
		return nil, false
	}
	return &AdapterMatch{
		Kind:      KindDefaultFill,
		Params:    map[string]any{"fills": fills},
		InSchema:  producerOut,
		OutSchema: objectSchema(outProps, required),
	}, true
}

// tryWrap bridges the case where the consumer requires exactly one field whose value is the producer's
// whole output object (`{"result": <producer>}`). Admitted only when the producer declares no property
// the consumer directly requires — i.e. the wrapping is the only sensible bridge.
func tryWrap(producerOut, consumerIn map[string]any) (*AdapterMatch, bool) {
	required := requiredFields(consumerIn)
	if len(required) != 1 {
		return nil, false
	}
	key := required[0]
	prodProps := properties(producerOut)
	if _, ok := prodProps[key]; ok {
		return nil, false // producer already has the field — nothing to wrap
	}
	consProps := properties(consumerIn)
	// Only wrap into an object-typed target (the producer output is an object).
	if ct := typeOf(consProps[key]); len(ct) != 0 && !contains(ct, "object") {
		return nil, false
	}
	outProps := map[string]map[string]any{key: {"type": "object"}}
	return &AdapterMatch{
		Kind:      KindWrap,
		Params:    map[string]any{"key": key},
		InSchema:  producerOut,
		OutSchema: objectSchema(outProps, required),
	}, true
}

// tryUnwrap bridges the mirror case: the producer emits a single-key object and the consumer requires
// the fields inside it. Admitted only when a single producer property is itself an object whose
// properties cover every consumer-required field.
func tryUnwrap(producerOut, consumerIn map[string]any) (*AdapterMatch, bool) {
	prodProps := properties(producerOut)
	if len(prodProps) != 1 {
		return nil, false
	}
	var key string
	var inner map[string]any
	for k, v := range prodProps {
		key, inner = k, v
	}
	innerProps := properties(inner)
	if len(innerProps) == 0 {
		return nil, false
	}
	required := requiredFields(consumerIn)
	for _, f := range required {
		if _, ok := innerProps[f]; !ok {
			return nil, false
		}
	}
	return &AdapterMatch{
		Kind:      KindUnwrap,
		Params:    map[string]any{"key": key},
		InSchema:  producerOut,
		OutSchema: objectSchema(innerProps, required),
	}, true
}

// objectSchema builds a JSON-Schema object document from a property map and a required list. The
// required list is filtered to fields the property map actually declares, and sorted, so the synthesised
// schema is deterministic and self-consistent.
func objectSchema(props map[string]map[string]any, required []string) map[string]any {
	p := map[string]any{}
	for name, sub := range props {
		p[name] = sub
	}
	var req []string
	for _, f := range required {
		if _, ok := props[f]; ok {
			req = append(req, f)
		}
	}
	sort.Strings(req)
	out := map[string]any{"type": "object", "properties": p}
	if len(req) > 0 {
		reqAny := make([]any, len(req))
		for i, r := range req {
			reqAny[i] = r
		}
		out["required"] = reqAny
	}
	return out
}

func cloneProps(in map[string]map[string]any) map[string]map[string]any {
	out := make(map[string]map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

// describeMismatch renders a field mismatch for a diagnostic. Kept here so the wording is consistent
// between the validator's rejection and any UI that reuses the catalog vocabulary.
func describeMismatch(m FieldMismatch) string {
	switch m.Reason {
	case ReasonMissing:
		return fmt.Sprintf("field %q is required by the consumer but not produced", m.Field)
	case ReasonTypeIncompatible:
		return fmt.Sprintf("field %q is %s at the producer but the consumer requires %s",
			m.Field, m.ProducerType, m.ConsumerType)
	default:
		return fmt.Sprintf("field %q does not match", m.Field)
	}
}
