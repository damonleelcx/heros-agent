package launch

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// workflowgraph.go is the adapter P19 said it would not invent — and it is only writable now because
// there is finally something to adapt.
//
// The pattern graph was registered-but-unmounted with the reason "no persistent adapter exists outside a
// demo binary". That was true, and it was the smaller half of the truth: there was also no DATA. The
// platform was never sent a workflow's shape, so an adapter would have had a durable store, a mounted
// route, and nothing to put in either. `heros link --with-ir` is what changed, and this is the join.
//
// # What this view is, and what it deliberately is not
//
// It is drawn from the OPT-IN structure and nothing else, so it shows exactly what the customer chose to
// send: symbols, models, context policies, tool counts, edges. It carries NO pattern labels and NO
// regions, and that is not an oversight to fix later by inference — the classifier reads prompts and
// tool names, neither of which crosses the boundary. A label guessed from a symbol name would be the
// platform inventing a claim about a customer's workflow, which is the one thing this codebase spends
// most of its comments refusing to do. Every region is therefore `unclassified`, which the console
// already renders as "not yet classified" rather than as a blank.
type workflowGraphSource struct {
	store linkingest.WorkflowIRStore
}

// GraphView builds the classified-graph view for one tenant's workflow.
//
// ok=false means the tenant has sent no structure for that workflow. A read failure is NOT flattened
// into that: it also returns false, because this interface has nowhere to put an error — and that is a
// real limitation, so it is stated here rather than hidden. The console will render "no such workflow"
// on a database outage. Widening `api.PatternSource` to return an error is the fix; it is a contract
// change and does not belong in the same commit as the feature.
func (w workflowGraphSource) GraphView(tenantID, workflowID string) (patternclassifier.GraphView, bool) {
	ir, ok, err := w.store.Latest(tenantID, workflowID)
	if err != nil || !ok {
		return patternclassifier.GraphView{}, false
	}

	// Deterministic order, so the same structure always draws the same picture. The console's own copy
	// promises "this drawing is the same one you saw yesterday", and a map iteration would make that
	// sentence false on every reload.
	sorted := make([]int, 0, len(ir.Nodes))
	for i := range ir.Nodes {
		sorted = append(sorted, i)
	}
	sort.Slice(sorted, func(a, b int) bool {
		x, y := ir.Nodes[sorted[a]], ir.Nodes[sorted[b]]
		if x.File != y.File {
			return x.File < y.File
		}
		if x.LineStart != y.LineStart {
			return x.LineStart < y.LineStart
		}
		return x.NodeID < y.NodeID
	})

	view := patternclassifier.GraphView{
		WorkflowID:      workflowID,
		IRVersion:       ir.IRVersion,
		TaxonomyVersion: patternclassifier.TaxonomyVersion,
		Nodes:           make([]patternclassifier.ViewNode, 0, len(ir.Nodes)),
		Edges:           make([]patternclassifier.ViewEdge, 0, len(ir.Edges)),
		// LLMCalls is 0 and true: no classifier ran, because the classifier's inputs never crossed the
		// boundary. The console renders this as "fully rule-covered — no model was consulted", which
		// would overstate it, so the unclassified regions below carry the real story.
		LLMCalls: 0,
	}
	for order, i := range sorted {
		n := ir.Nodes[i]
		model := n.ModelID
		if n.Provider != "" {
			model = n.Provider + "/" + n.ModelID
		}
		view.Nodes = append(view.Nodes, patternclassifier.ViewNode{
			NodeID: n.NodeID, Symbol: n.Symbol, Model: model, Policy: n.ContextPolicy,
			Tools: n.ToolCount, Layer: 0, Order: order,
			Labels: []patternclassifier.ViewLabel{}, RegionIDs: []string{},
		})
	}
	for _, e := range ir.Edges {
		view.Edges = append(view.Edges, patternclassifier.ViewEdge{From: e.From, To: e.To, Kind: e.Kind})
	}

	// Every node is an unclassified region of one. Stated as DATA the console is handed, never inferred
	// from a missing key — that distinction is the whole reason `Unclassified` is a field.
	for _, n := range view.Nodes {
		view.Unclassified = append(view.Unclassified, patternclassifier.ViewRegion{
			SubgraphID: "sg_" + n.NodeID, NodeIDs: []string{n.NodeID},
		})
	}
	return view, true
}

// newWorkflowGraphSource returns the pattern-graph adapter over a structure store.
func newWorkflowGraphSource(store linkingest.WorkflowIRStore) api.PatternSource {
	return workflowGraphSource{store: store}
}
