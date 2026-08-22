// Package attribution is the P4.5 attribution engine: it turns "the workflow scores 60%" into
// "node 3 is where 34 of the 40 failing cases first diverge, and it dominates 71% of the spend."
//
// It is a READ-ONLY consumer of P4 (eval results + the per-node contribution signal), P2.5 (OTel
// traces), and P3.5 (pattern labels). It localizes an end-to-end failure/cost/latency to individual
// nodes (per-node contribution + first-divergence), clusters failing cases into named categories,
// isolates a single node's causal contribution by ablation with a confidence interval, and flags
// cost/latency bottlenecks. Every output is a report; nothing here mutates a Variant Spec, a
// registry, or a node config, and no interface accepts or returns such a mutation. That guarantee is
// structural (Decision 1): the package has no write path into any config store.
package attribution

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/linkage"
)

// Variant identifies the measured workflow variant an attribution report is about. It carries the
// exact keys every report row is stamped with — a report that cannot say which {variant, eval set,
// config} it describes is a report a reader cannot trust.
type Variant struct {
	VariantID   string `json:"variant_id"`
	ConfigHash  string `json:"config_hash"`
	EvalSetHash string `json:"eval_set_hash"`
	WorkflowID  string `json:"workflow_id"`
}

// FailingCase is one failing eval case with the telemetry attribution reads: the case itself and the
// trace of the variant's run on it. SuccessTrace is an OPTIONAL reference trace of a passing run of
// the same case (a gold reference or a known-good variant) used for reference-mismatch divergence
// where a node exposes no contract to violate.
type FailingCase struct {
	Case         evalharness.Case
	Trace        evalharness.Trace
	SuccessTrace *evalharness.Trace
}

// DivergenceKind names HOW a node's output was found to first diverge from success. It is recorded
// (not just the node) because "the node's own contract said its output was wrong" and "the node's
// output merely differed from a reference we happen to have" are different strengths of evidence, and
// collapsing them would let a weak reference-mismatch read as a hard contract violation.
type DivergenceKind string

const (
	// DivergenceNone means no divergence was localizable — no node violated its contract and no
	// reference trace was available to compare against. It is a first-class value, not an empty
	// string standing in for "we did not look".
	DivergenceNone DivergenceKind = "none"
	// DivergenceContract: the node's output violates its own declared output contract (its span
	// errored/failed, or its output payload fails the node's output schema). This is checked FIRST
	// (design Q1) because a node whose contract is violated is a proven-wrong output regardless of
	// whether any reference exists — it is the node whose output first diverges even when a LATER
	// node is the one that fails to parse it.
	DivergenceContract DivergenceKind = "contract_violation"
	// DivergenceReference: the node's output differs from the success trace's output for the same
	// node. Only used where a gold/success reference exists and no contract violation was found.
	DivergenceReference DivergenceKind = "reference_mismatch"
)

// CaseAttribution is one failing case's localization: which node its output first diverged at, and
// by which kind of evidence.
type CaseAttribution struct {
	CaseID              string         `json:"case_id"`
	FirstDivergenceNode string         `json:"first_divergence_node"`
	DivergenceKind      DivergenceKind `json:"divergence_kind"`
}

// AttributionRow is one persisted attribution fact, keyed exactly as the design's data model
// requires: {variant_id, eval_set_hash, config_hash, node_id, case_id}. Per-node / per-case
// attribution is therefore a QUERY over these rows, never a re-run — the whole point of persisting
// them (task 1.3). Rows are append-only; nothing here writes back into a config store.
type AttributionRow struct {
	VariantID   string `json:"variant_id"`
	EvalSetHash string `json:"eval_set_hash"`
	ConfigHash  string `json:"config_hash"`
	NodeID      string `json:"node_id"`
	CaseID      string `json:"case_id"`

	// ContribSuccess is 1 when this node is the first-divergence node for this case, 0 otherwise —
	// the node's share of THIS case's failure. Only the first-divergence node carries the 1, for the
	// same reason P4's SuccessImpact does: crediting every downstream node with the failure would
	// multiply one root cause into a cluster.
	ContribSuccess float64 `json:"contrib_success"`
	// ContribCost / ContribLatency are the node's share of this case's end-to-end cost / latency, in
	// [0,1], from the P4 per-node contribution signal.
	ContribCost    float64 `json:"contrib_cost"`
	ContribLatency float64 `json:"contrib_latency"`
	// FirstDivergence flags the row whose node first diverges on this case.
	FirstDivergence bool `json:"first_divergence"`
}

// NodeAttribution is one node's aggregate across the failing cases: how often it is the
// first-divergence node, and its mean cost/latency share. This is the per-node breakdown the
// scorecard renders; it is derived from the rows, so it can never disagree with them.
type NodeAttribution struct {
	NodeID string `json:"node_id"`
	// FirstDivergenceCount is how many failing cases first diverge at this node.
	FirstDivergenceCount int `json:"first_divergence_count"`
	// FailureShare is FirstDivergenceCount / (failing cases), the fraction of failures this node is
	// responsible for localizing. A share, not a count, so it is comparable across runs of different
	// size.
	FailureShare float64 `json:"failure_share"`
	// MeanCostShare / MeanLatencyShare are this node's mean share of cost / latency over the failing
	// cases it appears in.
	MeanCostShare    float64 `json:"mean_cost_share"`
	MeanLatencyShare float64 `json:"mean_latency_share"`
	// NCases is how many of the failing cases this node executed in.
	NCases int `json:"n_cases"`
}

// PerNodeContribution is the whole attribution result: the raw rows (the persisted, queryable
// facts), the per-node aggregate breakdown, and the per-case first-divergence localization.
type PerNodeContribution struct {
	Variant Variant           `json:"variant"`
	Rows    []AttributionRow  `json:"rows"`
	Nodes   []NodeAttribution `json:"nodes"`
	Cases   []CaseAttribution `json:"cases"`
	// NFailing is how many failing cases were attributed.
	NFailing int `json:"n_failing"`
	// OrderedByTopology is true when the recovered topology (framework or inferred edges) constrained
	// first-divergence ordering rather than raw span start-time — the honest signal that the
	// localization is a claim about the agent's actual order, not wall-clock coincidence (FR13/FR15).
	OrderedByTopology bool `json:"ordered_by_topology"`
	// TopologyProvenance is the strongest edge provenance that constrained ordering ("" when ordering
	// fell back to flat trace-order). Surfaced so a reader knows how the topology was recovered.
	TopologyProvenance linkage.Provenance `json:"topology_provenance,omitempty"`
	// OverlappingSpans is true when any two node spans in any attributed case overlapped in wall-clock
	// time — the shape P34 concurrency makes ordinary (task 6.4).
	//
	// 🔴 It is reported rather than merely handled, because it changes what a localization MEANS. When
	// spans overlap, "which node diverged first" is not a reading of the trace's timing: the timing is
	// partly the scheduler's choice. A consumer that renders a first-divergence node without knowing
	// this is presenting a scheduling artifact as a finding.
	OverlappingSpans bool `json:"overlapping_spans,omitempty"`
	// OrderedByDeclaration is true when the spec's DECLARED order (P34 design D4) constrained the walk.
	// It is the answer to the question OverlappingSpans raises: overlapping spans ordered by the
	// declaration are replay-consistent; overlapping spans ordered by start time are not.
	//
	// 🚫 The pair is deliberately two booleans rather than one enum. `OverlappingSpans && !
	// OrderedByDeclaration` is a real and reachable state — a caller that has no spec to hand — and it
	// is precisely the state a reader must be able to see, so it must be expressible.
	OrderedByDeclaration bool `json:"ordered_by_declaration,omitempty"`
}

// Attribute decomposes end-to-end failure / cost / latency to individual nodes for a set of failing
// cases, and names the first-divergence node per case (task 1.1, 1.2). The IR supplies each node's
// output contract, which is what lets the contract-violation check name the node whose OUTPUT first
// diverges even when a downstream node is the one that fails to parse it.
//
// The result carries append-only rows keyed {variant, eval_set, config, node, case} so that per-node
// and per-case attribution is a query, not a re-run (task 1.3).
//
// Ordering: first-divergence walks nodes in the agent's EFFECTIVE order — the framework/existing IR
// edges when present — falling back to raw span start-time. To consume RECOVERED (inferred) edges too,
// use AttributeWithTopology.
func Attribute(ir *discovery.IR, v Variant, cases []FailingCase) PerNodeContribution {
	return AttributeWithTopology(ir, v, cases, linkage.Topology{Edges: linkage.FromIR(ir)})
}

// AttributeWithOrder is Attribute with the spec's DECLARED node order supplied (P34 task 6.4).
//
// 🔴 This is the entry point a caller holding a Variant Spec should use, and after P34 that is most of
// them. When a run overlapped two nodes' spans, start time stops being evidence of sequence — the
// scheduler chose it — so first-divergence walks the DECLARED order instead. Design D4 keeps `Order` as
// a linear sequence containing every node precisely so a replay has one, and using it here makes the
// localization a reader sees the one a replay would produce.
//
// 🚫 The declaration does not override a recovered TOPOLOGY. Topology is a claim about data dependency
// and outranks both: a consumer genuinely runs after its producer, however the two were declared. The
// declaration only replaces the start-time fallback, which is the part concurrency invalidated.
func AttributeWithOrder(ir *discovery.IR, v Variant, cases []FailingCase, topo linkage.Topology, declaredOrder []string) PerNodeContribution {
	return attribute(ir, v, cases, topo, declaredOrder)
}

// AttributeWithTopology is Attribute with an explicit recovered topology (task 13.3): first-divergence
// orders by, and a downstream ablation scopes "hold every upstream node fixed" by, the highest-
// provenance edge set in `topo`, falling back to raw span start-time only when no edge links a case's
// nodes. The topology is a reconciled set (framework + inferred_static + inferred_dynamic) built by the
// caller (see internal/linkage); an inferred edge is a hypothesis, never rendered as framework-certain.
func AttributeWithTopology(ir *discovery.IR, v Variant, cases []FailingCase, topo linkage.Topology) PerNodeContribution {
	return attribute(ir, v, cases, topo, nil)
}

// attribute is the one implementation. `declaredOrder` is the spec's node order, or nil.
func attribute(ir *discovery.IR, v Variant, cases []FailingCase, topo linkage.Topology, declaredOrder []string) PerNodeContribution {
	out := PerNodeContribution{Variant: v, NFailing: len(cases)}

	type nodeAgg struct {
		firstDiv        int
		costSum, latSum float64
		nCases          int
	}
	byNode := map[string]*nodeAgg{}
	nodeOrder := []string{}
	touch := func(id string) *nodeAgg {
		a, ok := byNode[id]
		if !ok {
			a = &nodeAgg{}
			byNode[id] = a
			nodeOrder = append(nodeOrder, id)
		}
		return a
	}

	// Sort cases so the row order (and every derived aggregate) is deterministic regardless of the
	// caller's slice order — the same failing set must produce byte-identical rows every run.
	sorted := append([]FailingCase(nil), cases...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Case.CaseID < sorted[j].Case.CaseID })

	for _, fc := range sorted {
		// 🔴 The base walk. Under overlapping spans the start-time order is partly the scheduler's
		// choice, so the DECLARED order replaces it when the caller supplied one — see
		// executionOrderDeclared, and p34_overlap_holdout_test.go, which measured the failure this
		// prevents. The declaration is used unconditionally rather than only on overlap: a spec's order
		// is the replay sequence, and switching ordering rules mid-run would make a localization depend
		// on whether this particular execution happened to interleave.
		startOrder := executionOrderDeclared(fc.Trace, declaredOrder)
		if spansOverlap(fc.Trace) {
			out.OverlappingSpans = true
		}
		if len(declaredOrder) > 0 {
			out.OrderedByDeclaration = true
		}
		order := startOrder
		if o, constrained := topo.Order(startOrder, startOrder); constrained {
			order = o
			out.OrderedByTopology = true
			if p := topo.HighestProvenance(); p.Rank() > out.TopologyProvenance.Rank() {
				out.TopologyProvenance = p
			}
		}
		divNode, kind := firstDivergenceOrdered(ir, fc, order)
		out.Cases = append(out.Cases, CaseAttribution{
			CaseID:              fc.Case.CaseID,
			FirstDivergenceNode: divNode,
			DivergenceKind:      kind,
		})

		// Per-node cost/latency shares come from the P4 contribution signal, unchanged — attribution
		// consumes it, it does not recompute a second, drifting version.
		contribs := evalharness.Contributions(fc.Trace)
		byNodeContrib := map[string]evalharness.NodeContribution{}
		for _, nc := range contribs {
			byNodeContrib[nc.NodeID] = nc
		}

		for _, nc := range contribs {
			agg := touch(nc.NodeID)
			agg.nCases++
			agg.costSum += nc.CostShare
			agg.latSum += nc.LatencyShare

			row := AttributionRow{
				VariantID:      v.VariantID,
				EvalSetHash:    v.EvalSetHash,
				ConfigHash:     v.ConfigHash,
				NodeID:         nc.NodeID,
				CaseID:         fc.Case.CaseID,
				ContribCost:    nc.CostShare,
				ContribLatency: nc.LatencyShare,
			}
			if nc.NodeID == divNode {
				row.ContribSuccess = 1
				row.FirstDivergence = true
			}
			out.Rows = append(out.Rows, row)
		}

		// The first-divergence node may not appear in the contribution list (e.g. a node with no
		// cost/latency spans). Still record its success contribution so the localization is not lost.
		if divNode != "" {
			if _, ok := byNodeContrib[divNode]; !ok {
				agg := touch(divNode)
				agg.nCases++
				out.Rows = append(out.Rows, AttributionRow{
					VariantID: v.VariantID, EvalSetHash: v.EvalSetHash, ConfigHash: v.ConfigHash,
					NodeID: divNode, CaseID: fc.Case.CaseID,
					ContribSuccess: 1, FirstDivergence: true,
				})
			}
			touch(divNode).firstDiv++
		}
	}

	sort.Strings(nodeOrder)
	for _, id := range nodeOrder {
		a := byNode[id]
		na := NodeAttribution{NodeID: id, FirstDivergenceCount: a.firstDiv, NCases: a.nCases}
		if len(sorted) > 0 {
			na.FailureShare = float64(a.firstDiv) / float64(len(sorted))
		}
		if a.nCases > 0 {
			na.MeanCostShare = a.costSum / float64(a.nCases)
			na.MeanLatencyShare = a.latSum / float64(a.nCases)
		}
		out.Nodes = append(out.Nodes, na)
	}
	// Sort rows by (case, node) for a stable, auditable order.
	sort.Slice(out.Rows, func(i, j int) bool {
		if out.Rows[i].CaseID != out.Rows[j].CaseID {
			return out.Rows[i].CaseID < out.Rows[j].CaseID
		}
		return out.Rows[i].NodeID < out.Rows[j].NodeID
	})
	return out
}

// FirstDivergence is the exported first-divergence localizer, so the diagnosis engine's
// prompt-format-drift detector can name the node whose output first violated its contract without
// re-deriving the walk. It returns the node and the kind of divergence (design Q1). It walks nodes in
// raw span start-time order; the topology-aware ordering used by the scorecard lives in Attribute.
func FirstDivergence(ir *discovery.IR, fc FailingCase) (string, DivergenceKind) {
	return firstDivergenceOrdered(ir, fc, executionOrder(fc.Trace))
}

// firstDivergenceOrdered names the node whose output FIRST diverges from success on a failing case,
// walking nodes in the given order (the recovered-topology order in Attribute, or raw start-time).
// Contract-violation is checked first across all nodes in order; only if no node violated its contract
// does it fall back to reference-mismatch against the success trace (design Q1). Returning the
// divergence kind alongside the node keeps the two strengths of evidence distinct.
func firstDivergenceOrdered(ir *discovery.IR, fc FailingCase, order []string) (string, DivergenceKind) {
	// 1. Contract-violation first — the node whose own output is proven wrong.
	for _, id := range order {
		if contractViolated(ir, fc.Trace, id) {
			return id, DivergenceContract
		}
	}

	// 2. Reference-mismatch — only where a success trace exists to compare against.
	if fc.SuccessTrace != nil {
		for _, id := range order {
			got, ok := fc.Trace.NodeOutputs[id]
			if !ok {
				continue
			}
			want, ok := fc.SuccessTrace.NodeOutputs[id]
			if !ok {
				continue
			}
			if !jsonEqual(got, want) {
				return id, DivergenceReference
			}
		}
	}

	return "", DivergenceNone
}
