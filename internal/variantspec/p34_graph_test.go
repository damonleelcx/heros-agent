package variantspec

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// P34 §5 — graph topology. Concurrency over Order, predicate edges through the ADR-004 path, and a
// merge that is required, closed and typed.

// graphIR is a four-node fan-out/fan-in: a → {b, c} → d. Every node carries a typed io_contract,
// because the merge checks are the point of this file and a contract-less fixture would make them
// vacuously pass.
func graphIR() *discovery.IR {
	node := func(id string, in, out map[string]any, inScope []string) discovery.IRNode {
		return discovery.IRNode{
			NodeID: id, Kind: "static_definition",
			CallSite: discovery.IRCallSite{
				File: "flow.py", Symbol: id, LineStart: 1, LineEnd: 1, InScope: inScope,
			},
			Model:           discovery.IRModel{Provider: "anthropic", ModelID: "claude-sonnet-5", Params: map[string]any{}},
			Prompt:          discovery.IRPrompt{Inline: id, Variables: []string{}},
			ContextAssembly: discovery.IRContextAssembly{Policy: "inline_messages"},
			IOContract:      discovery.IRIOContract{InputSchema: in, OutputSchema: out},
		}
	}
	obj := func(props map[string]any, required ...string) map[string]any {
		out := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			req := make([]any, len(required))
			for i, r := range required {
				req[i] = r
			}
			out["required"] = req
		}
		return out
	}
	str := map[string]any{"type": "string"}

	return &discovery.IR{
		IRVersion: "1.0.0",
		Nodes: []discovery.IRNode{
			// `route` is a symbol the IR records as in scope at a's call site — the one a predicate may name.
			node("n_a", obj(map[string]any{}), obj(map[string]any{"topic": str}), []string{"route", "topic"}),
			node("n_b", obj(map[string]any{"topic": str}), obj(map[string]any{"summary": str}), []string{}),
			node("n_c", obj(map[string]any{"topic": str}), obj(map[string]any{"citations": str}), []string{}),
			node("n_d", obj(map[string]any{"summary": str, "citations": str}, "summary", "citations"),
				obj(map[string]any{"answer": str}), []string{}),
		},
		Edges: []discovery.IREdge{},
	}
}

// fanInSpec is the spec shape every test here starts from: a → b, a → c, b → d, c → d.
func fanInSpec(mutate func(s *VariantSpec)) *VariantSpec {
	s := &VariantSpec{
		WorkflowID:     "wf-graph",
		SourceRevision: "rev1",
		Order:          []string{"n_a", "n_b", "n_c", "n_d"},
		Nodes:          map[string]NodeOverride{},
		Edges: []Edge{
			{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "data"},
			{FromNodeID: "n_a", ToNodeID: "n_c", Kind: "data"},
			{FromNodeID: "n_b", ToNodeID: "n_d", Kind: "data"},
			{FromNodeID: "n_c", ToNodeID: "n_d", Kind: "data"},
		},
		GraphGroups: []GraphGroup{{
			Nodes:      []string{"n_b", "n_c"},
			Concurrent: true,
			Merge: &Merge{
				Into:          "n_d",
				Strategy:      MergeAllFields,
				OnNodeFailure: FailFast,
			},
		}},
	}
	if mutate != nil {
		mutate(s)
	}
	return s
}

func resolveGraphSpec(t *testing.T, s *VariantSpec) (*Resolved, error) {
	t.Helper()
	return Resolve(context.Background(), s, graphIR(), newFakeRegistries())
}

// ── 5.1 / 5.2 — concurrency is declared OVER Order ──────────────────────────────────────────────

func TestAWellFormedFanInResolves(t *testing.T) {
	got, err := resolveGraphSpec(t, fanInSpec(nil))
	if err != nil {
		t.Fatalf("a well-formed concurrent fan-in was refused: %v", err)
	}
	if len(got.Config.GraphGroups) != 1 {
		t.Fatalf("the topology did not reach the hashed projection: %+v", got.Config.GraphGroups)
	}
	g := got.Config.GraphGroups[0]
	if !g.Concurrent || g.Merge == nil {
		t.Fatalf("the projected group lost its declaration: %+v", g)
	}
	if g.Merge.OnNodeFailure != string(FailFast) {
		t.Errorf("on_node_failure projected as %q", g.Merge.OnNodeFailure)
	}
	// 🔴 Order still contains every node, in sequence. That is design D4: a replay visits nodes in this
	// sequence even when the live run overlapped them, which is what attribution and run-diffing need.
	if len(got.Config.Nodes) != 4 {
		t.Fatalf("the resolved config has %d nodes; declaring a group must not remove any from the walk",
			len(got.Config.Nodes))
	}
	for i, want := range []string{"n_a", "n_b", "n_c", "n_d"} {
		if got.Config.Nodes[i].NodeID != want {
			t.Fatalf("node %d is %q, want %q — concurrency is declared OVER the ordering, never instead "+
				"of it, so the ordering must be untouched", i, got.Config.Nodes[i].NodeID, want)
		}
	}
}

func TestAGroupNodeOutsideTheOrderingIsRefused(t *testing.T) {
	_, err := resolveGraphSpec(t, fanInSpec(func(s *VariantSpec) {
		s.GraphGroups[0].Nodes = []string{"n_b", "n_z"}
	}))
	if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("got %v, want ErrInvalidSpec", err)
	}
	if !strings.Contains(err.Error(), "n_z") {
		t.Errorf("the refusal does not name the offending node: %v", err)
	}
}

func TestAGroupOfOneIsRefused(t *testing.T) {
	_, err := resolveGraphSpec(t, fanInSpec(func(s *VariantSpec) {
		s.GraphGroups[0].Nodes = []string{"n_b"}
		s.GraphGroups[0].Merge = nil
	}))
	if err == nil {
		t.Fatal("a group with one node was accepted; a topology unit is a statement about how nodes " +
			"relate to EACH OTHER, and one node relates to nothing")
	}
}

// TestNoGraphDeclarationHashesAsBefore is FR17 at the value level; the recorded fixture covers the
// bytes. Without it, `graph_groups` could be emitted as an empty array and every existing hash moves.
func TestNoGraphDeclarationHashesAsBefore(t *testing.T) {
	bare := fanInSpec(func(s *VariantSpec) { s.GraphGroups = nil })
	got, err := resolveGraphSpec(t, bare)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	canon, err := got.Config.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}
	if strings.Contains(string(canon), "graph_groups") {
		t.Fatalf("a spec declaring no topology emitted a graph_groups key:\n%s\n"+
			"Absence must be achieved by omission, never by an empty array — an empty array is different "+
			"bytes and therefore a different config_hash for every spec in existence", canon)
	}
	// And declaring one DOES move the hash, or the equality above would be a property of the projection
	// dropping everything rather than of absence being absence.
	withGroup, err := resolveGraphSpec(t, fanInSpec(nil))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if withGroup.ConfigHash == got.ConfigHash {
		t.Error("declaring two nodes concurrent and merged did not change config_hash; two different " +
			"computations — different cost, different latency, different failure behaviour — share one identity")
	}
}

// ── 5.4 — a fan-in without a merge is refused, never defaulted ──────────────────────────────────

func TestAFanInWithNoMergeIsRefusedAtValidate(t *testing.T) {
	s := fanInSpec(func(s *VariantSpec) { s.GraphGroups[0].Merge = nil })

	// 🔴 At VALIDATE, which is reachable with no IR and no registry at all — so a surface can refuse a
	// draft on a keystroke rather than after a round trip.
	if err := s.Validate(); err == nil {
		t.Fatal("a fan-in with no merge passed Validate; the results of two nodes converge on n_d and " +
			"nothing says how they combine")
	} else if !errors.Is(err, ErrInvalidSpec) {
		t.Fatalf("got %v, want ErrInvalidSpec", err)
	}

	_, err := resolveGraphSpec(t, s)
	if err == nil {
		t.Fatal("a fan-in with no merge resolved")
	}
	msg := err.Error()
	// The refusal must offer the vocabulary, or the author is told no without being told what to write.
	for _, want := range []string{"n_d", string(MergeAllFields), string(MergeNamespaced), string(FailFast), string(CollectPartial)} {
		if !strings.Contains(msg, want) {
			t.Errorf("the refusal does not mention %q; an author told only that something is missing has "+
				"to go and read the source to learn what to type.\n  got: %s", want, msg)
		}
	}
	// 🚫 And nothing was defaulted.
	if strings.Contains(strings.ToLower(msg), "defaulting to") {
		t.Errorf("the refusal describes a default: %s", msg)
	}
}

func TestAMergeWithNoFailureModeIsRefused(t *testing.T) {
	_, err := resolveGraphSpec(t, fanInSpec(func(s *VariantSpec) {
		s.GraphGroups[0].Merge.OnNodeFailure = ""
	}))
	if err == nil {
		t.Fatal("a merge with no on_node_failure was accepted; what happens to the other nodes when one " +
			"fails is a statement about the author's program and has no default")
	}
	if !strings.Contains(err.Error(), string(FailFast)) || !strings.Contains(err.Error(), string(CollectPartial)) {
		t.Errorf("the refusal does not offer the closed set: %v", err)
	}
}

func TestAMergeWithNoFanInIsRefused(t *testing.T) {
	// Two nodes that do NOT converge, but a merge is declared anyway. The merge would never run, so the
	// author believes results are being combined and they are not.
	_, err := resolveGraphSpec(t, fanInSpec(func(s *VariantSpec) {
		s.Edges = []Edge{
			{FromNodeID: "n_a", ToNodeID: "n_b", Kind: "data"},
			{FromNodeID: "n_a", ToNodeID: "n_c", Kind: "data"},
			{FromNodeID: "n_b", ToNodeID: "n_d", Kind: "data"},
		}
	}))
	if err == nil {
		t.Fatal("a merge declared over nodes that converge on nothing was accepted")
	}
}

func TestAMergeIntoTheWrongNodeIsRefused(t *testing.T) {
	_, err := resolveGraphSpec(t, fanInSpec(func(s *VariantSpec) {
		s.GraphGroups[0].Merge.Into = "n_a"
	}))
	if err == nil {
		t.Fatal("a merge naming a node the edges do not converge on was accepted; the merge and the " +
			"wiring describe two different graphs, and the executor walks the wiring")
	}
}

// ── 5.5 / 5.6 — the merge, through the typed-contract gate ──────────────────────────────────────

// TestACollidingAllFieldsMergeIsRefused — precedence here would be the platform deciding which of the
// author's two values is real, and under concurrency the answer would depend on scheduling.
func TestACollidingAllFieldsMergeIsRefused(t *testing.T) {
	ir := graphIR()
	// Make n_c produce `summary` too, so both producers declare it.
	for i := range ir.Nodes {
		if ir.Nodes[i].NodeID == "n_c" {
			ir.Nodes[i].IOContract.OutputSchema = map[string]any{
				"type": "object", "properties": map[string]any{"summary": map[string]any{"type": "string"}},
			}
		}
	}
	_, err := Resolve(context.Background(), fanInSpec(nil), ir, newFakeRegistries())
	if err == nil {
		t.Fatal("two nodes producing the same field merged under all-fields; one of the author's two " +
			"values was silently discarded")
	}
	msg := err.Error()
	if !strings.Contains(msg, "summary") {
		t.Errorf("the refusal does not name the colliding field: %s", msg)
	}
	if !strings.Contains(msg, string(MergeNamespaced)) {
		t.Errorf("the refusal does not offer the strategy that cannot collide: %s", msg)
	}
}

// TestCollectPartialAgainstARequiredFieldIsRefused is decisions.md D-34.3's enforced consequence.
//
// 🔴 This is the check that keeps `collect-partial` from being a promise the type system does not keep.
// Without it, a group that answers with partials would hand n_d an object missing a REQUIRED field, and
// the failure would arrive at run time to whoever was unlucky.
func TestCollectPartialAgainstARequiredFieldIsRefused(t *testing.T) {
	_, err := resolveGraphSpec(t, fanInSpec(func(s *VariantSpec) {
		s.GraphGroups[0].Merge.OnNodeFailure = CollectPartial
	}))
	if err == nil {
		t.Fatal("collect-partial was accepted against a downstream contract that REQUIRES a field only " +
			"one node produces; the merge may deliver fewer inputs than the group has nodes, so this is a " +
			"promise the type system does not keep")
	}
	msg := err.Error()
	if !strings.Contains(msg, string(CollectPartial)) || !strings.Contains(msg, string(FailFast)) {
		t.Errorf("the refusal does not name both the mode chosen and the one that would work: %s", msg)
	}
}

// TestCollectPartialIsAcceptedWhenTheContractAdmitsAbsence keeps the check above from being a blanket
// refusal of the mode. If this went red, `collect-partial` would be inexpressible.
func TestCollectPartialIsAcceptedWhenTheContractAdmitsAbsence(t *testing.T) {
	ir := graphIR()
	for i := range ir.Nodes {
		if ir.Nodes[i].NodeID == "n_d" {
			// Same properties, nothing required.
			delete(ir.Nodes[i].IOContract.InputSchema, "required")
		}
	}
	if _, err := Resolve(context.Background(), fanInSpec(func(s *VariantSpec) {
		s.GraphGroups[0].Merge.OnNodeFailure = CollectPartial
	}), ir, newFakeRegistries()); err != nil {
		t.Fatalf("collect-partial was refused against a contract that admits absence: %v", err)
	}
}

// TestAMergeThatDoesNotSatisfyTheDownstreamContractIsRefusedBeforeAnyCodemod is FR15.
func TestAMergeThatDoesNotSatisfyTheDownstreamContractIsRefused(t *testing.T) {
	ir := graphIR()
	for i := range ir.Nodes {
		if ir.Nodes[i].NodeID == "n_c" {
			// Produces something the downstream node has no use for, and stops producing `citations`.
			ir.Nodes[i].IOContract.OutputSchema = map[string]any{
				"type": "object", "properties": map[string]any{"unrelated": map[string]any{"type": "integer"}},
			}
		}
	}
	_, err := Resolve(context.Background(), fanInSpec(nil), ir, newFakeRegistries())
	if err == nil {
		t.Fatal("a merge whose combined output does not satisfy the downstream input contract resolved; " +
			"a codemod would then be generated for a graph that cannot type-check")
	}
	if !strings.Contains(err.Error(), "citations") {
		t.Errorf("the refusal does not name the field the downstream node is missing: %v", err)
	}
}

// ── 5.3 — the predicate, through the ADR-004 expr path ──────────────────────────────────────────

func TestAPredicateEdgeResolvesWhenTheSymbolIsInScope(t *testing.T) {
	got, err := resolveGraphSpec(t, fanInSpec(func(s *VariantSpec) {
		s.Edges[0].Kind = EdgeKindPredicate
		s.Edges[0].Predicate = "route" // recorded in scope at n_a's call site
	}))
	if err != nil {
		t.Fatalf("a predicate naming an in-scope symbol was refused: %v", err)
	}
	// The predicate is HASHED: two graphs differing only in which condition routes an edge are different
	// computations.
	canon, _ := got.Config.Canonical()
	if !strings.Contains(string(canon), `"predicate":"route"`) {
		t.Errorf("the predicate did not reach the hashed projection:\n%s", canon)
	}
}

// TestAnOutOfScopePredicateIsRefusedNamingTheSymbol is QA fence 9.8.
//
// 🔴 It must go through the ADR-004 path — the same sentinel a prompt slot's `expr` binding produces —
// because "one grammar, one validator" is only true if there is literally one scope check.
func TestAnOutOfScopePredicateIsRefusedNamingTheSymbol(t *testing.T) {
	_, err := resolveGraphSpec(t, fanInSpec(func(s *VariantSpec) {
		s.Edges[0].Kind = EdgeKindPredicate
		s.Edges[0].Predicate = "confidence_score"
	}))
	if !errors.Is(err, ErrBindingOutOfScope) {
		t.Fatalf("got %v, want ErrBindingOutOfScope — the SAME sentinel an out-of-scope `expr` binding "+
			"produces. A predicate-specific error would mean a predicate-specific scope rule, and the "+
			"second implementation of a safety check is always the one that is wrong", err)
	}
	var se *SpecError
	if !errors.As(err, &se) {
		t.Fatalf("the error is not a SpecError: %v", err)
	}
	if se.Ref != "confidence_score" {
		t.Errorf("the refusal names ref %q, want the offending symbol", se.Ref)
	}
	if se.Dim != graphDim {
		t.Errorf("the refusal names dimension %q, want the graph axis", se.Dim)
	}
}

func TestAPredicateEdgeWithNoPredicateIsRefused(t *testing.T) {
	s := fanInSpec(func(s *VariantSpec) { s.Edges[0].Kind = EdgeKindPredicate })
	if err := s.Validate(); err == nil {
		t.Fatal("a predicate edge with no predicate passed Validate; a conditional edge with no " +
			"condition is an unconditional edge that a reader will believe is guarded")
	}
}

func TestAPredicateOnANonPredicateEdgeIsRefused(t *testing.T) {
	s := fanInSpec(func(s *VariantSpec) { s.Edges[0].Predicate = "route" })
	if err := s.Validate(); err == nil {
		t.Fatal("a data edge carrying a predicate passed Validate; the predicate would never be " +
			"evaluated, so the author believes this edge is guarded and it is not")
	}
}

func TestAnUnknownEdgeKindIsRefused(t *testing.T) {
	s := fanInSpec(func(s *VariantSpec) { s.Edges[0].Kind = "maybe" })
	err := s.Validate()
	if err == nil {
		t.Fatal("an edge of unknown kind was accepted")
	}
	for _, want := range EdgeKinds() {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not offer the closed set member %q: %v", want, err)
		}
	}
}

// TestAPredicateAgainstAnOlderIRIsDeferredNotRefused — an IR that predates the in-scope record means
// NOT RECORDED, never "nothing is in scope". Refusing here would reject every predicate against every
// older IR, which is a FALSE refusal wearing fail-closed's clothes.
func TestAPredicateAgainstAnOlderIRIsDeferredNotRefused(t *testing.T) {
	ir := graphIR()
	for i := range ir.Nodes {
		ir.Nodes[i].CallSite.InScope = nil // a pre-P10 IR
	}
	if _, err := Resolve(context.Background(), fanInSpec(func(s *VariantSpec) {
		s.Edges[0].Kind = EdgeKindPredicate
		s.Edges[0].Predicate = "route"
	}), ir, newFakeRegistries()); err != nil {
		t.Fatalf("a predicate was refused against an IR that records no scope at all: %v.\n"+
			"Absent evidence is not evidence of absence — the same gate validateBindings applies", err)
	}
}

// ── 4.4's resolve-time half ─────────────────────────────────────────────────────────────────────

// TestAGroupWiderThanTheEnvelopeLimitIsRefused is the first half of QA fence 9.12. The second half —
// the sandbox capping at execution — lives in internal/sandbox, because it is what holds when this one
// is bypassed.
func TestAGroupWiderThanTheEnvelopeLimitIsRefused(t *testing.T) {
	regs := newFakeRegistries()
	regs.addEnvelope(t, "h-env", "narrow",
		`{"sandbox_posture":"no-network","turn_ceiling":4,"spend_ceiling_usd":1,"concurrency_limit":1}`)
	s := fanInSpec(func(s *VariantSpec) {
		s.Nodes["n_b"] = NodeOverride{HarnessRef: "h-env"}
	})
	_, err := Resolve(context.Background(), s, graphIR(), regs)
	if !errors.Is(err, ErrCeilingExceeded) {
		t.Fatalf("got %v, want ErrCeilingExceeded for a group of 2 against a concurrency_limit of 1", err)
	}
	if !strings.Contains(err.Error(), "sandbox enforces the same number") {
		t.Errorf("the refusal does not say the sandbox enforces it too; a reader who thinks this is the "+
			"only gate will believe bypassing it removes the limit: %v", err)
	}
}

// TestTheTightestEnvelopeWins — a group spans nodes, so "which envelope's limit applies" has no single
// answer. Taking the loosest would let an author widen a group by attaching a permissive envelope to
// any one node in it.
func TestTheTightestEnvelopeWins(t *testing.T) {
	regs := newFakeRegistries()
	regs.addEnvelope(t, "h-wide", "wide",
		`{"sandbox_posture":"no-network","turn_ceiling":4,"spend_ceiling_usd":1,"concurrency_limit":8}`)
	regs.addEnvelope(t, "h-narrow", "narrow",
		`{"sandbox_posture":"no-network","turn_ceiling":4,"spend_ceiling_usd":1,"concurrency_limit":1}`)
	s := fanInSpec(func(s *VariantSpec) {
		s.Nodes["n_b"] = NodeOverride{HarnessRef: "h-wide"}
		s.Nodes["n_c"] = NodeOverride{HarnessRef: "h-narrow"}
	})
	if _, err := Resolve(context.Background(), s, graphIR(), regs); !errors.Is(err, ErrCeilingExceeded) {
		t.Fatalf("got %v; the permissive envelope on n_b was allowed to override the strict one on n_c, "+
			"so an author can widen any group by attaching a loose envelope to one node in it", err)
	}
}
