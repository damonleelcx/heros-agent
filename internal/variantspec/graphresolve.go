package variantspec

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/typedcontract"
)

// THE RESOLVE-TIME HALF OF THE GRAPH AXIS (P34 tasks 5.3, 5.5, 5.6, 5.7)
// ──────────────────────────────────────────────────────────────────────
//
// `validateGraph` in graph.go runs everything checkable without the IR. This file runs the two checks
// that need it, and both run BEFORE any codemod is generated (FR15), which is what makes the gate a
// gate rather than a diagnosis:
//
//	5.3  a PREDICATE names a symbol in the producing call site's lexical scope
//	5.5  a MERGE satisfies the downstream node's typed input contract
//
// 🔴 Both go through paths that already exist. The predicate uses the SAME in-scope check a prompt
// slot's `expr` binding uses; the merge uses `internal/typedcontract`, UNCHANGED. Neither invents a
// second validator, because the second implementation of a safety check is always the one that is
// wrong — written by somebody who believed the problem was simpler.

// resolveGraph validates the spec's topology against the IR and returns the hashed projection.
//
// It runs after every node has resolved, so a group naming a node that failed to resolve never reaches
// here — and the projection it returns is in the spec's DECLARED order, because that order is the
// replay sequence and sorting it would claim two different replay sequences are one configuration.
func resolveGraph(_ context.Context, spec *VariantSpec, ir *discovery.IR, catalog *typedcontract.Catalog) ([]ResolvedGraphGroup, []InsertedAdapter, error) {
	if err := validatePredicates(spec, ir); err != nil {
		return nil, nil, err
	}
	if len(spec.GraphGroups) == 0 {
		return nil, nil, nil
	}
	if catalog == nil {
		catalog = typedcontract.DefaultCatalog()
	}
	schemas := map[string]discovery.IRIOContract{}
	for i := range ir.Nodes {
		schemas[ir.Nodes[i].NodeID] = ir.Nodes[i].IOContract
	}

	var out []ResolvedGraphGroup
	var adapters []InsertedAdapter
	for i, g := range spec.GraphGroups {
		rg := ResolvedGraphGroup{
			Nodes:      append([]string(nil), g.Nodes...),
			Concurrent: g.Concurrent,
		}
		if g.Merge != nil {
			ins, err := checkMerge(i, g, *g.Merge, spec, schemas, catalog)
			if err != nil {
				return nil, nil, err
			}
			adapters = append(adapters, ins...)
			rg.Merge = &ResolvedMerge{
				Into:          g.Merge.Into,
				Strategy:      string(g.Merge.Strategy),
				OnNodeFailure: string(g.Merge.OnNodeFailure),
			}
		}
		out = append(out, rg)
	}
	return out, adapters, nil
}

// validatePredicates is task 5.3 / FR13 / design D5.
//
// 🔴 It calls `HasInScope` — the SAME method `validateBindings` calls for a `BindExpr` binding — and
// reports through the SAME sentinel (`ErrBindingOutOfScope`) naming the SAME thing (the symbol). That
// is what "one grammar, one validator" means concretely: there is no predicate-specific scope rule to
// get wrong, and if `expr` proves too permissive it is narrowed in one place.
//
// The scope checked is the PRODUCING node's, because that is where the decision is made: the edge is
// taken after the producer runs, on a value the producer's call site can see. Checking the consumer's
// scope would ask about a frame that has not been entered.
//
// 🚫 It is gated on the IR having RECORDED an in-scope set, exactly as the binding satisfaction rule
// is. An IR that predates the record means NOT RECORDED, never "nothing is in scope" — treating the
// two alike would refuse every predicate against every older IR, which is a false refusal rather than
// the fail-closed one it would look like.
func validatePredicates(spec *VariantSpec, ir *discovery.IR) error {
	if ir == nil {
		return nil
	}
	byID := map[string]*discovery.IRNode{}
	for i := range ir.Nodes {
		byID[ir.Nodes[i].NodeID] = &ir.Nodes[i]
	}
	for i, e := range spec.Edges {
		if e.Kind != EdgeKindPredicate {
			continue
		}
		from, ok := byID[e.FromNodeID]
		if !ok {
			return specErr(e.FromNodeID, graphDim, ErrUnknownNode,
				"edges[%d] declares a predicate on an edge out of a node the IR does not contain", i)
		}
		if !from.CallSite.InScopeRecorded() {
			// Deferred, not accepted: nothing here claims the predicate is valid. It is the same gate
			// validateBindings applies, and a later resolve against a P34-aware IR will answer it.
			continue
		}
		if !from.CallSite.HasInScope(e.Predicate) {
			return &SpecError{NodeID: e.FromNodeID, Dim: graphDim, Ref: e.Predicate,
				Err: ErrBindingOutOfScope,
				Detail: fmt.Sprintf("edges[%d] routes %s -> %s on the predicate %q, which the IR does not "+
					"record as in scope at %s's call site. A predicate is an `expr` binding and is validated "+
					"by the same rule (ADR-004): it names a value the PRODUCING call site can already see, "+
					"and it is never inferred. The scope checked is the producer's because the edge is taken "+
					"after it runs.",
					i, e.FromNodeID, e.ToNodeID, e.Predicate, e.FromNodeID)}
		}
	}
	return nil
}

// checkMerge is tasks 5.5 and 5.6: the merge is validated against the DOWNSTREAM node's typed input
// contract, through `internal/typedcontract` unchanged, before any codemod exists.
//
// Three checks, in the order a reader would ask them:
//
//  1. COLLISION — under `all-fields`, two producers declaring the same output property. Refused rather
//     than resolved by precedence: precedence is the platform deciding which of the author's two values
//     is real, and under concurrency the answer would additionally depend on scheduling.
//  2. FAILURE COMPATIBILITY — under `collect-partial`, the downstream contract may not REQUIRE a field
//     that only one producer supplies. This is decisions.md D-34.3's enforced consequence: without it,
//     `collect-partial` is a promise the type system does not keep.
//  3. SATISFACTION — the merged output must satisfy the downstream input, or be bridgeable by a catalog
//     adapter, which is then an EXPLICIT node in the spec (task 5.7).
func checkMerge(i int, g GraphGroup, m Merge, spec *VariantSpec, schemas map[string]discovery.IRIOContract, catalog *typedcontract.Catalog) ([]InsertedAdapter, error) {
	producers := g.MembersConvergingOn(spec.Edges, m.Into)
	down, ok := schemas[m.Into]
	if !ok {
		return nil, specErr(m.Into, graphDim, ErrUnknownNode,
			"%s merges into %q, which the IR does not contain", describeGroup(i, g), m.Into)
	}

	merged, owner, collisions := mergedOutput(producers, schemas, m.Strategy)
	if len(collisions) > 0 {
		return nil, specErr(m.Into, graphDim, ErrInvalidSpec,
			"%s merges with strategy %q and %s produce the same field(s) %s. This is REFUSED rather than "+
				"resolved by precedence: choosing one of your two values is a semantic decision about YOUR "+
				"program, and under concurrency which producer 'won' would additionally depend on how the "+
				"machine scheduled them — so two runs of one configuration would not agree. Use %q, which "+
				"nests each node's output under its own node id and cannot collide.",
			describeGroup(i, g), strings.Join(collidingProducers(collisions), " and "),
			quoteAll(sortedKeysOf(collisions)), m.Strategy, MergeNamespaced)
	}

	// (2) decisions.md D-34.3's enforced consequence.
	if m.OnNodeFailure == CollectPartial {
		if missing := requiredFieldsOwnedByOneProducer(down.InputSchema, owner); len(missing) > 0 {
			return nil, specErr(m.Into, graphDim, ErrInvalidSpec,
				"%s declares on_node_failure %q, so the merge may deliver fewer inputs than the group has "+
					"nodes — but %q REQUIRES the field(s) %s, which only one node produces. That is a promise "+
					"the type system does not keep, discovered at run time by whoever is unlucky. Either "+
					"declare %q, or make those fields optional in %q's input contract.",
				describeGroup(i, g), CollectPartial, m.Into, quoteAll(missing), FailFast, m.Into)
		}
	}

	// (3) The typed-contract gate, UNCHANGED (FR15).
	if res := typedcontract.Satisfies(merged, down.InputSchema); res.OK {
		return nil, nil
	}
	// Task 5.7: an adaptable mismatch becomes an EXPLICIT adapter node in the spec, never a hidden
	// runtime coercion. It is recorded exactly as a re-arrangement's adapter is (P5 Decision 3), so the
	// compare view and the transform engine both see what was bridged — and the transform materialises
	// it as generated source beside the diff.
	if a, ok := catalog.FindAdapter(merged, down.InputSchema); ok {
		from := mergeAdapterSource(producers)
		return []InsertedAdapter{{
			AdapterNodeID: adapterNodeID(from, m.Into, string(a.Kind)),
			FromNodeID:    from,
			ToNodeID:      m.Into,
			CatalogKind:   string(a.Kind),
			Params:        a.Params,
			InSchema:      a.InSchema,
			OutSchema:     a.OutSchema,
		}}, nil
	}
	return nil, specErr(m.Into, graphDim, ErrUnsafeRewrite,
		"%s merges into %q with strategy %q and the combined output does not satisfy %q's input "+
			"contract: %s. No catalog adapter bridges it, so this is refused BEFORE any codemod is "+
			"generated rather than emitting a diff nobody can trust.",
		describeGroup(i, g), m.Into, m.Strategy, m.Into, describeMismatches(typedcontract.Satisfies(merged, down.InputSchema)))
}

// mergedOutput computes what the downstream node would receive, plus which producer owns each field
// and any collisions.
//
// 🔴 `namespaced` cannot collide BY CONSTRUCTION rather than by check — every producer's output goes
// under its own node id — which is why it is the answer offered in the collision refusal.
func mergedOutput(producers []string, schemas map[string]discovery.IRIOContract, strategy MergeStrategy) (merged map[string]any, owner map[string]string, collisions map[string][]string) {
	props := map[string]any{}
	owner = map[string]string{}
	collisions = map[string][]string{}

	for _, p := range producers {
		out := properties(schemas[p].OutputSchema)
		if strategy == MergeNamespaced {
			props[p] = schemas[p].OutputSchema
			owner[p] = p
			continue
		}
		for name, schema := range out {
			if prev, dup := owner[name]; dup {
				collisions[name] = append(collisions[name], prev, p)
				continue
			}
			owner[name] = p
			props[name] = schema
		}
	}
	return map[string]any{"type": "object", "properties": props}, owner, collisions
}

// requiredFieldsOwnedByOneProducer returns the downstream contract's REQUIRED fields that exactly one
// producer supplies, sorted. Those are the ones `collect-partial` can fail to deliver.
func requiredFieldsOwnedByOneProducer(inputSchema map[string]any, owner map[string]string) []string {
	var out []string
	for _, f := range requiredOf(inputSchema) {
		if _, produced := owner[f]; produced {
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// mergeAdapterSource names the edge an adapter is inserted on when the SOURCE is a whole fan-in. The
// producers, joined — an id rather than a sentence, because it becomes part of a node id that must be
// deterministic and reproducible.
func mergeAdapterSource(producers []string) string { return strings.Join(producers, "+") }

// properties reads a JSON-Schema object's `properties` map, or an empty one.
func properties(schema map[string]any) map[string]any {
	p, _ := schema["properties"].(map[string]any)
	if p == nil {
		return map[string]any{}
	}
	return p
}

// requiredOf reads a JSON-Schema object's `required` list.
func requiredOf(schema map[string]any) []string {
	raw, _ := schema["required"].([]any)
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func collidingProducers(collisions map[string][]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, ps := range collisions {
		for _, p := range ps {
			if !seen[p] {
				seen[p] = true
				out = append(out, p)
			}
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeysOf(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func quoteAll(xs []string) string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = fmt.Sprintf("%q", x)
	}
	return strings.Join(out, ", ")
}

func describeMismatches(res typedcontract.SatisfyResult) string {
	out := make([]string, 0, len(res.Mismatches))
	for _, m := range res.Mismatches {
		out = append(out, fmt.Sprintf("%s (%s)", m.Field, m.Reason))
	}
	if len(out) == 0 {
		return "the contracts do not match"
	}
	return strings.Join(out, "; ")
}
