package patternclassifier

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// GraphView is the read model the graph UI renders. It is built HERE, not in the browser, for the
// same reason the metric-set table is a single source of truth: "which labels apply to this region",
// "is this region classified at all", and "what does this label dispatch" are classification
// questions, and a second implementation of them in JavaScript would drift from this one.
type GraphView struct {
	WorkflowID      string `json:"workflow_id"`
	IRVersion       string `json:"ir_version"`
	TaxonomyVersion string `json:"taxonomy_version"`

	Nodes []ViewNode `json:"nodes"`
	Edges []ViewEdge `json:"edges"`
	// Regions are the labelled subgraphs.
	Regions []ViewRegion `json:"regions"`
	// Unclassified are regions no layer could label. They are a FIRST-CLASS part of the view, not an
	// absence: a region the classifier could not name must read as "not yet classified", and the only
	// way to guarantee that is for the unclassified regions to be data the UI is handed, rather than
	// something it infers from a missing key.
	Unclassified []ViewRegion `json:"unclassified"`
	Diagnostics  []Diagnostic `json:"diagnostics,omitempty"`
	// LLMCalls is surfaced so an operator can see, on the page, whether a model was consulted at all.
	LLMCalls int `json:"llm_calls"`
}

// ViewNode is one node positioned for rendering. Layer/Order come from a deterministic layout so the
// same IR always draws the same picture — a graph that reshuffles between reloads is unreadable.
type ViewNode struct {
	NodeID string `json:"node_id"`
	Symbol string `json:"symbol"`
	Model  string `json:"model"`
	Policy string `json:"policy"`
	Tools  int    `json:"tools"`
	Layer  int    `json:"layer"`
	Order  int    `json:"order"`
	// Labels are node-scoped labels (capabilities co-existing on this node).
	Labels []ViewLabel `json:"labels"`
	// RegionIDs are the subgraphs this node belongs to, so hovering a region can highlight members.
	RegionIDs []string `json:"region_ids"`
}

type ViewEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // data | control
}

// ViewRegion is a subgraph as the UI shows it. Labels may be empty — that is the unclassified state,
// and it is spelled out rather than implied.
type ViewRegion struct {
	SubgraphID string      `json:"subgraph_id"`
	NodeIDs    []string    `json:"node_ids"`
	Labels     []ViewLabel `json:"labels"`
}

// ViewLabel is a label with everything the UI needs to render it honestly, resolved server-side.
type ViewLabel struct {
	Pattern Pattern `json:"pattern"`
	// Ordinal is the canonical pattern number (1–20), carried so the page can show the number people
	// actually say ("Pattern 13") instead of leaving the reader to look it up.
	Ordinal    int     `json:"ordinal"`
	Title      string  `json:"title"`
	Group      Group   `json:"group"`
	Confidence float64 `json:"confidence"`
	Source     Source  `json:"source"`
	// Candidate marks a behavioral pattern structure could not confirm. The UI must say so: a
	// candidate rendered like a confirmed label is a false claim about what the system knows.
	Candidate bool `json:"candidate"`
	// Provenance is the detector id or llm run ref — which exact thing produced this label.
	Provenance string `json:"provenance"`
	// PrimaryMetric is what this label DISPATCHES. Showing it is what makes the annotation a
	// dispatcher on screen rather than decoration.
	PrimaryMetric string   `json:"primary_metric"`
	Metrics       []string `json:"metrics"`
}

func toViewLabel(l Label) ViewLabel {
	v := ViewLabel{
		Pattern: l.Pattern, Confidence: l.Confidence, Source: l.Source, Candidate: l.Candidate,
		Provenance: l.DetectorID + l.LLMRunRef,
	}
	if info, ok := Info(l.Pattern); ok {
		v.Title, v.Group, v.Ordinal = info.Title, info.Group, info.Ordinal
	}
	if ms, ok := MetricSetFor(l.Pattern); ok {
		v.PrimaryMetric, v.Metrics = ms.Primary, ms.Metrics
	}
	return v
}

// BuildGraphView assembles the UI read model from an IR and its classification.
func BuildGraphView(ir *discovery.IR, res Result) GraphView {
	// Every collection starts EMPTY, never nil. A nil slice serialises as JSON `null`, and a consumer
	// handed `"unclassified": null` has to treat an absence as a state — which is the exact failure
	// this view exists to prevent, and which cost a blank page the first time it happened. Empty
	// means "none of these"; null means "the producer had nothing to say", and they are not the same.
	gv := GraphView{
		WorkflowID: ir.Workflow.ID, IRVersion: ir.IRVersion,
		TaxonomyVersion: TaxonomyVersion, Diagnostics: res.Diagnostics, LLMCalls: res.LLMCalls,
		Nodes:        []ViewNode{},
		Edges:        []ViewEdge{},
		Regions:      []ViewRegion{},
		Unclassified: []ViewRegion{},
	}
	g := newGraph(ir)
	layers := layerize(g)

	memberOf := map[string][]string{}
	for _, sg := range res.Subgraphs {
		for _, id := range sg.NodeIDs {
			memberOf[id] = append(memberOf[id], sg.SubgraphID)
		}
	}

	// Rows are assigned in per-region BANDS, not globally down each layer.
	//
	// The obvious layout — walk the nodes and give each the next free row in its layer — was wrong on
	// screen in a way no assertion caught: two regions' nodes interleave down the rows, so their
	// bounding boxes overlap and each box visibly encloses the other's nodes. A router's branch drawn
	// inside the RAG box is not a cosmetic problem; it says something false about which region a node
	// belongs to. Giving each region a disjoint band of rows makes the boxes disjoint by construction.
	rowOf := map[string]int{}
	band := 0
	place := func(ids []string) {
		perLayer := map[int]int{}
		used := 0
		for _, id := range ids {
			if _, done := rowOf[id]; done || g.nodes[id] == nil {
				continue // a node in two nested regions belongs to the first band that claimed it
			}
			rowOf[id] = band + perLayer[layers[id]]
			perLayer[layers[id]]++
			if perLayer[layers[id]] > used {
				used = perLayer[layers[id]]
			}
		}
		band += used
	}
	for _, sg := range res.Subgraphs { // res.Subgraphs is already sorted: bands are deterministic
		place(sg.NodeIDs)
	}
	// Everything not in a region: laid out per connected component, so unrelated islands do not stack
	// into one column either.
	inRegion := map[string]bool{}
	for id := range rowOf {
		inRegion[id] = true
	}
	for _, comp := range g.residueComponents(inRegion) {
		place(comp)
	}

	for _, id := range g.order { // sorted: layout is deterministic
		n := g.nodes[id]
		gv.Nodes = append(gv.Nodes, ViewNode{
			NodeID: id, Symbol: n.CallSite.Symbol, Model: modelKey(n), Policy: n.ContextAssembly.Policy,
			Tools: len(n.ToolsSkills), Layer: layers[id], Order: rowOf[id],
			Labels: viewLabelsFor(res, id), RegionIDs: memberOf[id],
		})
	}
	for _, e := range ir.Edges {
		if g.nodes[e.FromNodeID] == nil || g.nodes[e.ToNodeID] == nil {
			continue
		}
		gv.Edges = append(gv.Edges, ViewEdge{From: e.FromNodeID, To: e.ToNodeID, Kind: e.Kind})
	}

	for _, sg := range res.Subgraphs {
		gv.Regions = append(gv.Regions, ViewRegion{
			SubgraphID: sg.SubgraphID, NodeIDs: sg.NodeIDs, Labels: viewLabelsFor(res, sg.SubgraphID),
		})
	}
	// Residue that nothing labelled. A region that WAS labelled by the fallback is no longer residue,
	// so it is filtered out here rather than appearing in both lists.
	labelled := map[string]bool{}
	for _, l := range res.Labels {
		labelled[l.SubgraphRef] = true
	}
	for _, sg := range res.Residue {
		if labelled[sg.SubgraphID] {
			continue
		}
		gv.Unclassified = append(gv.Unclassified, ViewRegion{SubgraphID: sg.SubgraphID, NodeIDs: sg.NodeIDs})
	}

	sort.SliceStable(gv.Regions, func(i, j int) bool { return gv.Regions[i].SubgraphID < gv.Regions[j].SubgraphID })
	sort.SliceStable(gv.Unclassified, func(i, j int) bool {
		return gv.Unclassified[i].SubgraphID < gv.Unclassified[j].SubgraphID
	})
	return gv
}

func viewLabelsFor(res Result, ref string) []ViewLabel {
	out := []ViewLabel{} // empty, never nil — see BuildGraphView
	for _, l := range res.LabelsFor(ref) {
		out = append(out, toViewLabel(l))
	}
	return out
}

// layerize assigns each node a depth: longest path from any source, over data and control edges.
//
// Back edges are ignored when computing depth (a Reflection loop has no "deeper" node), so a cyclic
// graph still lays out instead of failing to terminate. The result depends only on the graph, never
// on iteration order, which is what keeps the drawing stable across reloads.
func layerize(g *graph) map[string]int {
	layer := map[string]int{}
	state := map[string]int{} // 0 unvisited, 1 in-progress, 2 done
	var depth func(id string) int
	depth = func(id string) int {
		switch state[id] {
		case 2:
			return layer[id]
		case 1:
			return 0 // a back edge: do not follow it, and do not let it define depth
		}
		state[id] = 1
		best := 0
		for _, up := range g.anyIn[id] { // already sorted
			if d := depth(up) + 1; d > best {
				best = d
			}
		}
		state[id] = 2
		layer[id] = best
		return best
	}
	for _, id := range g.order {
		depth(id)
	}
	// Normalise so the shallowest node sits at layer 0. A cycle has no true source, so its members
	// come out offset (a two-node loop lands on layers 1 and 2), which would draw the whole component
	// with an empty leading column. Shifting is purely cosmetic and does not change relative depth.
	min := 0
	first := true
	for _, d := range layer {
		if first || d < min {
			min, first = d, false
		}
	}
	if min != 0 {
		for id := range layer {
			layer[id] -= min
		}
	}
	return layer
}
