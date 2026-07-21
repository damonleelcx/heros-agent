package patternclassifier

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
)

func viewOf(t *testing.T, f fixture, opts Options) GraphView {
	t.Helper()
	res, err := Classify(context.Background(), f.ir, opts)
	if err != nil {
		t.Fatal(err)
	}
	labelled, err := WriteBack(f.ir, res)
	if err != nil {
		t.Fatal(err)
	}
	return BuildGraphView(labelled, res)
}

// Task 7.1: each subgraph's label(s) and confidence reach the view, tied to their region.
func TestGraphViewCarriesPerRegionLabelsAndConfidence(t *testing.T) {
	f := fxComposite()
	gv := viewOf(t, f, f.opts())
	if len(gv.Regions) != 2 {
		t.Fatalf("want two labelled regions, got %d: %+v", len(gv.Regions), gv.Regions)
	}
	seen := map[Pattern]float64{}
	for _, r := range gv.Regions {
		if len(r.Labels) == 0 {
			t.Errorf("region %s is in Regions but carries no label", r.SubgraphID)
		}
		for _, l := range r.Labels {
			seen[l.Pattern] = l.Confidence
			if l.Title == "" || l.Group == "" {
				t.Errorf("%q: the view must resolve the display name and group server-side", l.Pattern)
			}
			// The dispatch is what the label is FOR; the view must carry it or the page becomes
			// decoration.
			if l.PrimaryMetric == "" {
				t.Errorf("%q: the view does not carry the dispatched metric-set", l.Pattern)
			}
			if l.Provenance == "" {
				t.Errorf("%q: the view does not say what produced the label", l.Pattern)
			}
		}
	}
	if seen[Routing] != ConfidenceTopologyDetermined || seen[RetrievalRAG] != ConfidenceTopologyDetermined {
		t.Errorf("confidences did not reach the view: %+v", seen)
	}
	// The node-scoped capability is on its NODE, so the page can show it inside the routing branch.
	for _, n := range gv.Nodes {
		if n.NodeID == "n_tech" && (len(n.Labels) != 1 || n.Labels[0].Pattern != ToolUse) {
			t.Errorf("n_tech should carry the Tool Use label in the view, got %+v", n.Labels)
		}
	}
}

// Task 7.2: a rule label and an llm label are DIFFERENT DATA, so the page can render them
// differently. If the view flattened source away, no amount of CSS could distinguish them.
func TestGraphViewDistinguishesRuleFromLLM(t *testing.T) {
	f := fxAmbiguous()
	m := &stubModel{reply: []RawLabel{{Pattern: string(GuardrailsSafety), Confidence: conf(0.58)}}}
	gv := viewOf(t, f, fallbackOpts(f, m))
	if len(gv.Regions) != 1 || len(gv.Regions[0].Labels) != 1 {
		t.Fatalf("want one llm-labelled region, got %+v", gv.Regions)
	}
	l := gv.Regions[0].Labels[0]
	if l.Source != SourceLLM {
		t.Fatalf("source = %q, want llm", l.Source)
	}
	if l.Confidence != 0.58 {
		t.Errorf("confidence = %v, want 0.58 (it is the headline for an llm label)", l.Confidence)
	}
	if gv.LLMCalls != 1 {
		t.Errorf("the view must surface that a model was consulted: %d", gv.LLMCalls)
	}
	// And a rule-labelled workflow reports zero, so an operator can see it on the page.
	rf := fxComposite()
	if got := viewOf(t, rf, rf.opts()).LLMCalls; got != 0 {
		t.Errorf("rule-only classification should report 0 llm calls, got %d", got)
	}
}

// Task 7.3: an unclassified region is DATA in the view, not a missing key. The page cannot render
// "not yet classified" honestly if it has to infer the state from an absence.
func TestGraphViewMakesUnclassifiedARegionNotAnAbsence(t *testing.T) {
	f := fxAmbiguous()
	gv := viewOf(t, f, f.opts())
	if len(gv.Regions) != 0 {
		t.Fatalf("nothing should be labelled: %+v", gv.Regions)
	}
	if len(gv.Unclassified) != 1 {
		t.Fatalf("the unlabelled region must appear as unclassified, got %+v", gv.Unclassified)
	}
	if len(gv.Unclassified[0].NodeIDs) != 2 {
		t.Errorf("the unclassified region must name its members: %+v", gv.Unclassified[0])
	}
	if len(gv.Unclassified[0].Labels) != 0 {
		t.Error("an unclassified region must carry NO label — not a default one")
	}
	// Once the fallback labels it, it stops being residue — it must not appear in both lists.
	m := &stubModel{reply: []RawLabel{{Pattern: string(GuardrailsSafety), Confidence: conf(0.5)}}}
	gv2 := viewOf(t, f, fallbackOpts(f, m))
	if len(gv2.Unclassified) != 0 {
		t.Errorf("a labelled region must not still be listed as unclassified: %+v", gv2.Unclassified)
	}
}

// A behavioral candidate must reach the view MARKED, so the page can say the loop is unconfirmed.
func TestGraphViewMarksCandidates(t *testing.T) {
	f := fxReflection()
	gv := viewOf(t, f, f.opts())
	if len(gv.Regions) != 1 || len(gv.Regions[0].Labels) != 1 {
		t.Fatalf("got %+v", gv.Regions)
	}
	l := gv.Regions[0].Labels[0]
	if !l.Candidate || l.Confidence > BehavioralCandidateCap {
		t.Errorf("Reflection must reach the view as a capped candidate: %+v", l)
	}
}

// Task 7.4: the layout is deterministic. A graph that reshuffles between reloads is unreadable,
// which on a large composite IR is the difference between legible and useless.
func TestGraphLayoutIsDeterministicAndLayered(t *testing.T) {
	f := fxComposite()
	first, _ := json.Marshal(viewOf(t, f, f.opts()))
	for i := 0; i < 10; i++ {
		got, _ := json.Marshal(viewOf(t, f, f.opts()))
		if string(got) != string(first) {
			t.Fatalf("the view is not stable across builds (run %d)", i)
		}
	}
	gv := viewOf(t, f, f.opts())
	layer := map[string]int{}
	for _, n := range gv.Nodes {
		layer[n.NodeID] = n.Layer
	}
	// The retrieval chain must lay out left to right in pipeline order.
	if !(layer["n_embed"] < layer["n_retrieve"] && layer["n_retrieve"] < layer["n_rerank"] && layer["n_rerank"] < layer["n_answer"]) {
		t.Errorf("the retrieval pipeline is not laid out in order: %+v", layer)
	}
	// Nodes on one layer get distinct rows, so they cannot draw on top of each other.
	rows := map[[2]int]int{}
	for _, n := range gv.Nodes {
		rows[[2]int{n.Layer, n.Order}]++
	}
	for k, c := range rows {
		if c > 1 {
			t.Errorf("%d nodes share layer/order %v — they would overlap on screen", c, k)
		}
	}
}

// A cyclic graph must still lay out. A Reflection loop has no "deeper" node, and a layout that
// followed the back edge would not terminate.
func TestGraphLayoutHandlesCycles(t *testing.T) {
	f := fxReflection()
	gv := viewOf(t, f, f.opts())
	if len(gv.Nodes) != 2 {
		t.Fatalf("got %d nodes", len(gv.Nodes))
	}
	for _, n := range gv.Nodes {
		if n.Layer < 0 || n.Layer > 1 {
			t.Errorf("node %s got an implausible layer %d from a cycle", n.NodeID, n.Layer)
		}
	}
}

// A big multi-region IR keeps every region separately annotated — the legibility precondition.
func TestGraphViewOnLargeCompositeKeepsRegionsSeparate(t *testing.T) {
	nodes := []discovery.IRNode{}
	edges := []discovery.IREdge{}
	// Five independent routers: five regions, none merging into one.
	for _, p := range []string{"a", "b", "c", "d", "e"} {
		nodes = append(nodes,
			node("n_router_"+p, withPrompt("classify "+p)),
			node("n_x_"+p, withPrompt("x "+p), withSemantics("conditional", false)),
			node("n_y_"+p, withPrompt("y "+p), withSemantics("conditional", false)),
		)
		edges = append(edges, controlEdge("n_router_"+p, "n_x_"+p), controlEdge("n_router_"+p, "n_y_"+p))
	}
	f := fixture{name: "large", ir: buildIR(nodes, edges)}
	gv := viewOf(t, f, f.opts())
	if len(gv.Regions) != 5 {
		t.Fatalf("want 5 separately-annotated regions, got %d", len(gv.Regions))
	}
	for _, r := range gv.Regions {
		if len(r.Labels) != 1 || r.Labels[0].Pattern != Routing {
			t.Errorf("region %s lost its annotation: %+v", r.SubgraphID, r.Labels)
		}
		if len(r.NodeIDs) != 3 {
			t.Errorf("region %s has %d members, want 3 — regions must not merge", r.SubgraphID, len(r.NodeIDs))
		}
	}
	// Every node knows which region owns it, so the page can highlight members.
	for _, n := range gv.Nodes {
		if len(n.RegionIDs) != 1 {
			t.Errorf("node %s belongs to %d regions, want 1", n.NodeID, len(n.RegionIDs))
		}
	}
}

// REGRESSION (found in a browser, not by a test): a nil slice serialises as JSON `null`, and the page
// iterated `view.unclassified` — so a fully-classified workflow rendered a blank graph and an empty
// label list. Every collection in the view must marshal as [] so "none of these" is never
// indistinguishable from "the producer said nothing".
func TestGraphViewCollectionsMarshalAsEmptyArraysNotNull(t *testing.T) {
	for _, f := range []fixture{fxComposite(), fxAmbiguous(), fxToolUse()} {
		gv := viewOf(t, f, f.opts())
		blob, err := json.Marshal(gv)
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(blob, &raw); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"nodes", "edges", "regions", "unclassified"} {
			if string(raw[key]) == "null" {
				t.Errorf("%s: %q marshalled as null; it must be [] so the UI can tell empty from absent", f.name, key)
			}
		}
		// And per-region / per-node label lists, for the same reason.
		for _, r := range gv.Regions {
			if r.Labels == nil {
				t.Errorf("%s: region %s has nil labels", f.name, r.SubgraphID)
			}
		}
		for _, n := range gv.Nodes {
			if n.Labels == nil {
				t.Errorf("%s: node %s has nil labels", f.name, n.NodeID)
			}
		}
	}
}

// REGRESSION (found in a browser, not by a test): rows were assigned globally down each layer, so two
// regions' nodes interleaved and their bounding boxes overlapped — the page drew a router's branch
// visibly INSIDE the RAG region box, and the two region captions landed on top of each other. That is
// not cosmetic: it states something false about which region a node belongs to.
//
// Regions must occupy DISJOINT row bands, which makes their bounding boxes disjoint by construction.
func TestRegionsOccupyDisjointRowBands(t *testing.T) {
	f := fxComposite()
	gv := viewOf(t, f, f.opts())
	row := map[string]int{}
	for _, n := range gv.Nodes {
		row[n.NodeID] = n.Order
	}
	type span struct{ lo, hi int }
	spans := map[string]span{}
	for _, r := range gv.Regions {
		s := span{lo: 1 << 30, hi: -1}
		for _, id := range r.NodeIDs {
			if row[id] < s.lo {
				s.lo = row[id]
			}
			if row[id] > s.hi {
				s.hi = row[id]
			}
		}
		spans[r.SubgraphID] = s
	}
	if len(spans) < 2 {
		t.Fatalf("need at least two regions to check for overlap, got %d", len(spans))
	}
	for a, sa := range spans {
		for b, sb := range spans {
			if a >= b {
				continue
			}
			if sa.lo <= sb.hi && sb.lo <= sa.hi {
				t.Errorf("regions %s (rows %d..%d) and %s (rows %d..%d) share rows; their boxes would overlap on screen",
					a, sa.lo, sa.hi, b, sb.lo, sb.hi)
			}
		}
	}
	// And no two nodes may land on the same cell, or they would draw on top of each other.
	cell := map[[2]int]string{}
	for _, n := range gv.Nodes {
		k := [2]int{n.Layer, n.Order}
		if prev, dup := cell[k]; dup {
			t.Errorf("nodes %s and %s both sit at layer %d row %d", prev, n.NodeID, n.Layer, n.Order)
		}
		cell[k] = n.NodeID
	}
}
