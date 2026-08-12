package patternclassifier

import (
	"sort"
	"strings"

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
	// LLMCallsNote is the sentence that goes with LLMCalls, resolved HERE because the three cases it
	// distinguishes are classification facts and a page that computed them from the count alone gets one
	// of them wrong every time. See llmCallsNote.
	LLMCallsNote string `json:"llm_calls_note"`
	// Topology is why this graph looks the way it does when it has nodes and no edges. Nil when the
	// graph has edges — there is nothing to explain.
	Topology *ViewTopology `json:"topology,omitempty"`

	// Composition is what this workflow is MADE OF: every pattern present, what it covers, and the
	// remainder nothing covered (task 8.1). 🚫 It dispatches nothing — see composition.go.
	Composition Composition `json:"composition"`

	// Agent is the analysis agent's contribution and its current availability (tasks 8.2, 8.6–8.8).
	//
	// 🔴 Nil until a caller attaches it, and nil means THIS DEPLOYMENT RUNS NO AGENT. It is deliberately
	// not computed here: everything on it is a CURRENT fact (a placement an operator can change this
	// afternoon) while the rest of this view is a fact about a stored analysis, and a ViewAgent stored
	// beside the graph would report a placement that was true when discovery ran. See viewagent.go.
	Agent *ViewAgent `json:"agent,omitempty"`
}

// TopologyReason is the closed vocabulary for "why does this graph have no edges".
type TopologyReason string

// ReasonNoTopology: the graph has nodes and zero edges. Everything keyed off topology — the layout, the
// structural detectors, the metric-set dispatch, cost attribution — degrades to its zero case at once,
// so the drawing would be a column of disconnected boxes that LOOKS like a finding and is not one.
const ReasonNoTopology TopologyReason = "no_topology"

// ViewTopology explains an edgeless graph by naming the analysis that produced it.
//
// 🔴 Every fact here comes from the discovery report. Nothing in this package knows that Python's
// frontend is syntactic, and nothing here should: the day a frontend learns to emit edges, its own
// declared AnalysisKind changes and this explanation stops being made about it, with no edit here.
type ViewTopology struct {
	Reason TopologyReason `json:"reason"`
	// Frontends are the frontends that produced this graph, with the analysis kind each declares.
	// Empty means discovery reported none — which is itself the honest answer and is rendered as such,
	// never as "no frontend is syntactic".
	Frontends []ViewFrontend `json:"frontends"`
	// Sentence is the explanation rendered for a reader, assembled from Frontends. Resolved server-side
	// so a test can assert the exact string and so two consoles cannot word it differently.
	Sentence string `json:"sentence"`
}

// ViewFrontend is one contributing frontend as the view reports it.
type ViewFrontend struct {
	Language     string `json:"language"`
	AnalysisKind string `json:"analysis_kind"` // typed | syntactic
	Nodes        int    `json:"nodes"`
	Edges        int    `json:"edges"`
}

// UnclassifiedReason is the closed vocabulary for "why does this region carry no label".
//
// 🔴 Four values, not one. The surface used to say "not yet classified" over all four, and the four have
// four different next actions: push a revision a typed frontend can read; nothing (the rules looked and
// this is not a pattern they name); configure a model; look at what the model was asked. One sentence
// covering four states is a sentence that is wrong three times out of four.
type UnclassifiedReason string

const (
	// ReasonNoTopologyToMatch: the graph has no edges at all, so no structural signature had anything
	// to match against. Takes precedence over every other cause: with no topology, neither layer ever
	// had a chance, and reporting "no signature matched" would blame the detectors for an empty input.
	ReasonNoTopologyToMatch UnclassifiedReason = "no_topology"
	// ReasonNoSignatureMatched: structure existed and no rule detector's signature matched this region.
	ReasonNoSignatureMatched UnclassifiedReason = "no_signature_matched"
	// ReasonModelNotConsulted: the rules found nothing and no model fallback was configured, so the
	// second layer never ran. The region is unlabelled because nothing looked, not because nothing is
	// there — the distinction the old copy erased.
	ReasonModelNotConsulted UnclassifiedReason = "model_not_consulted"
	// ReasonModelAbstained: a model WAS consulted about this exact region and returned nothing usable —
	// an empty answer, or one rejected at the taxonomy gate. An abstention is an output, and it is a
	// stronger statement than the three above.
	ReasonModelAbstained UnclassifiedReason = "model_abstained"
)

// unclassifiedSentences maps each cause to the sentence a reader gets. A map rather than a switch in
// the page: the console renders what it is handed, so a fifth cause cannot be invented in TypeScript.
var unclassifiedSentences = map[UnclassifiedReason]string{
	ReasonNoTopologyToMatch: "This graph carries no edges, so there was no structure for any pattern " +
		"signature to match. The region is unlabelled because nothing could be matched against it — not " +
		"because its nodes implement no pattern.",
	ReasonNoSignatureMatched: "The structural detectors ran against this region and none of their " +
		"signatures matched it. That is a statement about the twenty patterns in the taxonomy, not about " +
		"whether this code does something worth naming.",
	ReasonModelNotConsulted: "No structural signature matched, and no model fallback is configured on " +
		"this deployment, so the second classification layer never ran. Nothing has looked at this region " +
		"beyond the rules.",
	ReasonModelAbstained: "A model was consulted about this region and returned nothing the taxonomy " +
		"accepts. An abstention is an answer: the region was examined and left unlabelled deliberately.",
}

// SentenceFor returns the reader-facing sentence for a cause, or "" for an unknown one.
//
// 🚫 It does NOT fall back to a generic sentence. An unrecognised cause rendering as a plausible
// paragraph is how a fifth state ships looking like one of the four.
func SentenceFor(r UnclassifiedReason) string { return unclassifiedSentences[r] }

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
	// Author is who wrote this edge (P30 D4): frontend | detector | heros | operator. Empty reads as
	// `legacy`. It reaches the view because the customer surface has to draw an inferred edge
	// differently from a measured one (task 8.3), and a page cannot distinguish them from anything else
	// on this struct — by design, since an inferred edge is the same shape as any other.
	Author string `json:"author,omitempty"`
	// Confidence is the author's confidence in [0,1]. Meaningful for an inferred edge; a frontend edge
	// carries none, and absence must not be read as zero confidence.
	Confidence float64 `json:"confidence,omitempty"`
}

// ViewRegion is a subgraph as the UI shows it. Labels may be empty — that is the unclassified state,
// and it is spelled out rather than implied.
type ViewRegion struct {
	SubgraphID string      `json:"subgraph_id"`
	NodeIDs    []string    `json:"node_ids"`
	Labels     []ViewLabel `json:"labels"`
	// Reason is why this region carries no label, from the four-value closed enum. Empty on a labelled
	// region, where there is nothing to explain.
	Reason UnclassifiedReason `json:"reason,omitempty"`
	// ReasonSentence is Reason rendered for a reader, resolved here so the page cannot invent a fifth
	// cause and so the exact string is assertable in a test.
	ReasonSentence string `json:"reason_sentence,omitempty"`
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
	// Author is who wrote this label (P30 D4). 🔴 NOT derivable from Source: P30's agent emits through
	// the rule layer by design (one arbitration path, D3), so a heros label reads `source: rule` and is
	// otherwise identical in shape to a detector's.
	Author discovery.FactAuthor `json:"author,omitempty"`
}

func toViewLabel(l Label) ViewLabel {
	v := ViewLabel{
		Pattern: l.Pattern, Confidence: l.Confidence, Source: l.Source, Candidate: l.Candidate,
		Provenance: l.DetectorID + l.LLMRunRef, Author: l.Author,
	}
	if info, ok := Info(l.Pattern); ok {
		v.Title, v.Group, v.Ordinal = info.Title, info.Group, info.Ordinal
	}
	if ms, ok := MetricSetFor(l.Pattern); ok {
		v.PrimaryMetric, v.Metrics = ms.Primary, ms.Metrics
	}
	return v
}

// BuildGraphView assembles the UI read model from an IR, its classification, and the report of the
// discovery run that produced the IR.
//
// 🔴 The report is a VALUE, not a pointer, and it is a required parameter rather than an option. A
// caller with no report passes the zero value and gets a view that SAYS discovery reported no
// contributing frontend — which is the honest reading. A pointer would have made the same caller pass
// nil, and nil would have had to mean something, and the something it would have quietly meant is
// "nothing about this analysis is worth explaining".
func BuildGraphView(ir *discovery.IR, res Result, rep discovery.DiscoveryReport) GraphView {
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
	gv.LLMCallsNote = llmCallsNote(res, len(ir.Nodes))
	gv.Topology = topologyFor(ir, rep)
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
		gv.Edges = append(gv.Edges, ViewEdge{
			From: e.FromNodeID, To: e.ToNodeID, Kind: e.Kind,
			Author: e.Author, Confidence: e.Confidence,
		})
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
	consulted := map[string]bool{}
	for _, r := range res.LLMRuns {
		consulted[r.SubgraphRef] = true
	}
	for _, d := range res.Diagnostics {
		if d.Stage == StageLLMFallback && d.SubgraphRef != "" {
			// A model error or a rejected answer both mean the model WAS asked about this region. The
			// run record only exists on the success path, so without this the region reads as
			// "not consulted" precisely when the consultation is what went wrong.
			consulted[d.SubgraphRef] = true
		}
	}
	for _, sg := range res.Residue {
		if labelled[sg.SubgraphID] {
			continue
		}
		reason := unclassifiedReason(len(ir.Edges) == 0 && len(ir.Nodes) > 0, res.FallbackConfigured, consulted[sg.SubgraphID])
		gv.Unclassified = append(gv.Unclassified, ViewRegion{
			SubgraphID: sg.SubgraphID, NodeIDs: sg.NodeIDs,
			Reason: reason, ReasonSentence: SentenceFor(reason),
		})
	}

	sort.SliceStable(gv.Regions, func(i, j int) bool { return gv.Regions[i].SubgraphID < gv.Regions[j].SubgraphID })
	sort.SliceStable(gv.Unclassified, func(i, j int) bool {
		return gv.Unclassified[i].SubgraphID < gv.Unclassified[j].SubgraphID
	})
	// LAST, over the assembled view rather than over `res`: everything the composition needs has been
	// resolved once already, and re-deriving ordinals and authors from the raw result would be the
	// second implementation this file's header warns about.
	gv.Composition = buildComposition(gv)
	return gv
}

// unclassifiedReason picks the ONE cause that explains this region, in strict precedence order.
//
// The order is the order in which a chance was lost. No topology means neither layer ever had an input.
// A model that was asked and declined is a stronger statement than one that was never configured, which
// in turn says more than "the rules did not match", because every residue region is by construction a
// region the rules did not match — that fact alone distinguishes nothing.
func unclassifiedReason(noTopology, fallbackConfigured, modelConsulted bool) UnclassifiedReason {
	switch {
	case noTopology:
		return ReasonNoTopologyToMatch
	case modelConsulted:
		return ReasonModelAbstained
	case !fallbackConfigured:
		return ReasonModelNotConsulted
	default:
		return ReasonNoSignatureMatched
	}
}

// llmCallsNote is the sentence beside the fallback-call count.
//
// 🔴 The old copy read "Fully rule-covered — no model was consulted" whenever the count was zero, and
// that is FALSE in the case it most often ran in: a graph where nothing was labelled at all. Zero calls
// with zero labels is not full coverage; it is nothing having happened. Three cases, three sentences.
func llmCallsNote(res Result, nodes int) string {
	if res.LLMCalls > 0 {
		return "A model was consulted on the regions the rules did not cover. Labels it produced are " +
			"marked model-guessed and never override a rule-matched one."
	}
	switch {
	case len(res.Labels) == 0:
		// The count is zero because there was nothing the rules covered AND nothing reached the model.
		return "No model was consulted, and no rule matched either — so nothing on this graph carries " +
			"a label. Zero fallback calls here means nothing looked, not that everything was covered."
	case len(res.Residue) == 0 && nodes > 0:
		return "Fully rule-covered: every region matched a structural signature, so no model was " +
			"consulted and no label on this graph is a guess."
	default:
		return "Partly rule-covered: the regions that matched a signature are labelled, and the rest " +
			"were left unlabelled because no model fallback is configured to look at them."
	}
}

// topologyFor explains an edgeless graph, or returns nil when there is nothing to explain.
func topologyFor(ir *discovery.IR, rep discovery.DiscoveryReport) *ViewTopology {
	if len(ir.Nodes) == 0 || len(ir.Edges) > 0 {
		return nil
	}
	t := &ViewTopology{Reason: ReasonNoTopology, Frontends: []ViewFrontend{}}
	var syntactic, typed []string
	for _, f := range rep.Frontends {
		t.Frontends = append(t.Frontends, ViewFrontend{
			Language: f.Language, AnalysisKind: string(f.AnalysisKind), Nodes: f.Nodes, Edges: f.Edges,
		})
		if f.AnalysisKind == discovery.AnalysisSyntactic {
			syntactic = append(syntactic, f.Language)
		} else {
			typed = append(typed, f.Language)
		}
	}
	sort.Strings(syntactic)
	sort.Strings(typed)

	switch {
	case len(rep.Frontends) == 0:
		t.Sentence = "This graph has nodes and no edges, and the discovery run that produced it recorded " +
			"no contributing frontend — so which analysis ran, and whether it can produce edges at all, is " +
			"not known here. The drawing is withheld rather than shown as disconnected boxes."
	case len(syntactic) > 0 && len(typed) == 0:
		t.Sentence = "This graph has nodes and no edges because the " + joinLanguages(syntactic) +
			" frontend that produced it is syntactic: it finds call sites by matching a parse tree and " +
			"cannot follow a value from one statement to the next. No dependency between these nodes has " +
			"been ruled out — none has been looked for."
	case len(syntactic) > 0:
		t.Sentence = "This graph has nodes and no edges. The " + joinLanguages(typed) + " frontend " +
			"resolves values and found no dependency; the " + joinLanguages(syntactic) + " frontend is " +
			"syntactic and cannot look for one. Part of this graph's topology was measured and part was " +
			"never examined, and the two are not distinguishable in the drawing."
	default:
		t.Sentence = "This graph has nodes and no edges, and the " + joinLanguages(typed) + " frontend " +
			"that produced it does resolve values across statements. Nothing connects these call sites in " +
			"the source at this revision."
	}
	return t
}

// joinLanguages renders a language list as prose: "Python", "Python and Rust", "Python, Rust and Java".
func joinLanguages(langs []string) string {
	switch len(langs) {
	case 0:
		return ""
	case 1:
		return langs[0]
	case 2:
		return langs[0] + " and " + langs[1]
	default:
		return strings.Join(langs[:len(langs)-1], ", ") + " and " + langs[len(langs)-1]
	}
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
