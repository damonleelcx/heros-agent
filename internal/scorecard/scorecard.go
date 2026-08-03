// Package scorecard is the P4.5 read-only per-run scorecard (§10): the SOLE output surface of the
// attribution + diagnosis engines. It is built SERVER-SIDE for the same reason evalboard is —
// "which node localizes the failures", "is this ablation inconclusive", "is the analyst below its
// floor" are questions with exactly one correct answer, and a second implementation in JavaScript
// would drift. The browser renders what it is handed and derives nothing.
//
// The screen EXPLAINS; it does not FIX. There is no apply/change affordance anywhere in this view —
// the deliberate absence is the read-only guarantee made visible (task 10.1, 10.5). Every state
// (empty / partial / inconclusive / uncalibrated / error) is an explicit field read from the
// persisted reports, never inferred from a missing key (task 10.6).
package scorecard

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/linkage"
)

// State is the scorecard's aggregate state. It is DATA, computed from the reports, not a guess the
// browser makes from absent fields.
type State string

const (
	// StateEmpty: the variant has no failing cases to localize. The screen says "nothing to diagnose",
	// not a blank scorecard (design 10.1 unhappy path).
	StateEmpty State = "empty"
	// StateError: the scorecard could not be built. It never renders a partial scorecard as whole.
	StateError State = "error"
	// StateReady: the scorecard is built. Region-level flags (Partial / Inconclusive / Uncalibrated)
	// still qualify it — a ready scorecard can carry an in-progress ablation fan-out.
	StateReady State = "ready"
)

// OverallMetrics is the run's headline. Passed in (computed from eval results by the caller) rather
// than recomputed here, so the scorecard cites the SAME numbers the leaderboard does.
type OverallMetrics struct {
	TaskSuccess float64 `json:"task_success"`
	NCases      int     `json:"n_cases"`
	NFailing    int     `json:"n_failing"`
	CostUSD     float64 `json:"cost_usd"`
	LatencyMS   float64 `json:"latency_ms"`
}

// NodeRow is one node's line in the per-node breakdown: its contribution, whether it is a
// first-divergence hotspot, and any bottleneck flags (task 10.2).
type NodeRow struct {
	NodeID               string                  `json:"node_id"`
	FailureShare         float64                 `json:"failure_share"`
	FirstDivergenceCount int                     `json:"first_divergence_count"`
	MeanCostShare        float64                 `json:"mean_cost_share"`
	MeanLatencyShare     float64                 `json:"mean_latency_share"`
	BottleneckDimensions []attribution.Dimension `json:"bottleneck_dimensions,omitempty"`
	// Pattern is the node's P3.5 structural pattern label, "" when the node is unclassified (a
	// hand-rolled agent with no discovered graph). Classified says the same as a boolean so the UI can
	// mark "not classified" without inferring it from an empty string (FR14 / task 12.5).
	Pattern    string `json:"pattern,omitempty"`
	Classified bool   `json:"classified"`
}

// ClusterRow is one top failure cluster with its size and representative case (task 10.2).
type ClusterRow struct {
	Signature            string   `json:"signature"`
	Label                string   `json:"label"`
	Size                 int      `json:"size"`
	RepresentativeCaseID string   `json:"representative_case_id"`
	MemberCaseIDs        []string `json:"member_case_ids"`
}

// DiagnosisCard shows *why* — the typed cause, its source (rule vs analyst), confidence + agreement,
// and the specific failing cases as EVIDENCE reachable in one click. It is never a bare label (task
// 10.3): EvidenceCaseIDs is part of the card, and Description renders the cause in words.
type DiagnosisCard struct {
	Code            diagnosis.TaxonomyCode `json:"code"`
	Description     string                 `json:"description"`
	NodeID          string                 `json:"node_id"`
	Source          diagnosis.Source       `json:"source"`
	Confidence      float64                `json:"confidence"`
	EvidenceCaseIDs []string               `json:"evidence_case_ids"`

	// Analyst-only trust fields, shown on the card so a human can weigh an analyst opinion (task 10.4).
	Agreement      float64 `json:"agreement"`
	NHuman         int     `json:"n_human"`
	Calibrated     bool    `json:"calibrated"`
	AnalystFlagged bool    `json:"analyst_flagged"`
	LowConfidence  bool    `json:"low_confidence"`
	// Classified says whether the diagnosed node carried a P3.5 pattern label. A diagnosis on an
	// unclassified node was produced by a pattern-agnostic rule only, and the card says so (FR14).
	Classified bool `json:"classified"`
}

// AblationCard renders one ablation delta WITH its CI and verdict (task 10.4). The CI is shown, not
// hidden, because a delta without its interval reads as a certainty it is not.
type AblationCard struct {
	NodeID    string              `json:"node_id"`
	Metric    string              `json:"metric"`
	DeltaMean float64             `json:"delta_mean"`
	CILow     float64             `json:"ci_low"`
	CIHigh    float64             `json:"ci_high"`
	NSeeds    int                 `json:"n_seeds"`
	Verdict   attribution.Verdict `json:"verdict"`
}

// EdgeView is one recovered topology edge surfaced on the scorecard, with its provenance and the
// signal it came from — so a reader sees HOW the topology was recovered and can tell an inferred edge
// from a framework-certain one (FR15 / task 13.4/13.5). It is never rendered as certain: provenance
// and confidence ride on every edge.
type EdgeView struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	Provenance string  `json:"provenance"`
	Signal     string  `json:"signal"`
	Confidence float64 `json:"confidence"`
}

// TopologyView is the recovered agent topology the scorecard renders: the edges, whether it actually
// constrained first-divergence ordering, and the strongest provenance present.
type TopologyView struct {
	Edges             []EdgeView `json:"edges"`
	OrderedByTopology bool       `json:"ordered_by_topology"`
	Provenance        string     `json:"provenance,omitempty"`
}

// AnalystStatus is the analyst calibration surfaced on the scorecard, so an uncalibrated analyst is
// visibly flagged (task 10.4).
type AnalystStatus struct {
	Present    bool    `json:"present"`
	Agreement  float64 `json:"agreement"`
	NHuman     int     `json:"n_human"`
	Calibrated bool    `json:"calibrated"`
	Floor      float64 `json:"floor"`
	Flagged    bool    `json:"flagged"`
}

// View is the whole read-only scorecard. ReadOnly is always true and carries no apply affordance —
// the field exists so the client can ASSERT the absence rather than assume it.
type View struct {
	VariantID   string `json:"variant_id"`
	EvalSetHash string `json:"eval_set_hash"`
	ConfigHash  string `json:"config_hash"`
	WorkflowID  string `json:"workflow_id"`

	State   State  `json:"state"`
	Message string `json:"message,omitempty"`

	Overall   OverallMetrics  `json:"overall"`
	Nodes     []NodeRow       `json:"nodes"`
	Clusters  []ClusterRow    `json:"clusters"`
	Diagnoses []DiagnosisCard `json:"diagnoses"`
	Ablations []AblationCard  `json:"ablations"`
	Analyst   AnalystStatus   `json:"analyst"`
	Topology  TopologyView    `json:"topology"`

	// Region flags — first-class states (task 10.6).
	Partial             bool `json:"partial"`              // ablation fan-out in progress
	HasInconclusive     bool `json:"has_inconclusive"`     // ≥1 ablation returned inconclusive
	AnalystUncalibrated bool `json:"analyst_uncalibrated"` // analyst below floor / uncalibrated

	// Classification coverage — how much of the per-node breakdown carries a P3.5 pattern label
	// (FR14 / design Q9). A hand-rolled agent with no discovered graph reports 0 classified, and the
	// scorecard shows the count so a user sees how much the missing labels cost their diagnosis
	// coverage rather than mistaking "not classified" for "nothing wrong".
	ClassifiedNodeCount   int `json:"classified_node_count"`
	UnclassifiedNodeCount int `json:"unclassified_node_count"`

	// FailureAttribution says whether the per-node FAILURE columns mean anything on this card.
	//
	// 🔴 A NodeRow with FailureShare 0 is a claim — "this node caused none of the failures" — and a
	// scorecard that never computed failure attribution would be making it on every row. Attribution
	// needs per-node correctness per case, which is produced where the eval runs and does not cross the
	// egress boundary; a card assembled from linked runs therefore has real COST and LATENCY shares and
	// no failure shares at all. Those are different situations and the reader must be able to tell:
	// "this node is not to blame" and "we did not work out who is to blame" lead to opposite next steps.
	//
	// Same doctrine as evalboard.TieAnalysis: the state is DATA the UI is handed, never something it
	// infers from a column of zeroes.
	FailureAttribution FailureAttribution `json:"failure_attribution"`

	// ReadOnly is always true; the scorecard exposes no apply/change control.
	ReadOnly bool `json:"read_only"`
}

// FailureAttribution reports whether per-node failure attribution was computed for this card.
type FailureAttribution string

const (
	// FailureComputed: the attribution engine ran. FailureShare and FirstDivergenceCount are meaningful,
	// and so are Clusters, Diagnoses and Ablations.
	FailureComputed FailureAttribution = "computed"
	// FailureUnavailable: the card was assembled from linked runs, which carry per-node COST and LATENCY
	// but no per-node correctness. Failure shares are zero because nothing was attributed, not because
	// nothing failed — and the clusters, diagnoses and ablations are absent for the same reason.
	FailureUnavailable FailureAttribution = "unavailable"
)

// Input is everything Build needs — the engine outputs, already computed and (in production) read
// back from the persisted reports.
type Input struct {
	Variant   attribution.Variant
	Overall   OverallMetrics
	Contrib   attribution.PerNodeContribution
	Clusters  []attribution.FailureCluster
	Flags     []attribution.BottleneckFlag
	Diagnoses []diagnosis.Diagnosis
	Ablations []attribution.AblationResult
	Analyst   diagnosis.AnalystCalibration
	// Patterns maps node_id → P3.5 pattern label ("" = unclassified). Supplied by the engine (which
	// holds the IR) so the read model can mark "not classified" without reaching into the IR itself.
	Patterns map[string]string
	// Topology is the recovered edge set the engine reconciled and attribution ordered by. Surfaced so
	// the scorecard can show HOW the agent's nodes are linked and each edge's provenance (FR15).
	Topology linkage.Topology
	// AblationInProgress marks the fan-out as still running — the scorecard renders `partial` rather
	// than implying the ablations are complete.
	AblationInProgress bool
}

// Build assembles the read-only View from the engine outputs. It derives the aggregate state and the
// region flags from the data (never from absent keys), and attaches each node's bottleneck flags and
// each diagnosis's evidence.
func Build(in Input) View {
	v := View{
		VariantID:   in.Variant.VariantID,
		EvalSetHash: in.Variant.EvalSetHash,
		ConfigHash:  in.Variant.ConfigHash,
		WorkflowID:  in.Variant.WorkflowID,
		Overall:     in.Overall,
		// Build runs the attribution engine over real per-node contributions, so this path is
		// unconditionally `computed`. The other value exists for assemblers that cannot
		// (internal/hostedscorecard).
		FailureAttribution: FailureComputed,
		ReadOnly:           true,
	}

	// Bottleneck flags, grouped by node so the per-node row can carry them.
	flagsByNode := map[string][]attribution.Dimension{}
	for _, f := range in.Flags {
		flagsByNode[f.NodeID] = append(flagsByNode[f.NodeID], f.Dimension)
	}
	for id := range flagsByNode {
		sort.Slice(flagsByNode[id], func(i, j int) bool { return flagsByNode[id][i] < flagsByNode[id][j] })
	}

	classifiedOf := func(nodeID string) (string, bool) {
		if in.Patterns == nil {
			return "", false
		}
		p := in.Patterns[nodeID]
		return p, p != ""
	}

	for _, n := range in.Contrib.Nodes {
		pat, classified := classifiedOf(n.NodeID)
		if classified {
			v.ClassifiedNodeCount++
		} else {
			v.UnclassifiedNodeCount++
		}
		v.Nodes = append(v.Nodes, NodeRow{
			NodeID:               n.NodeID,
			FailureShare:         n.FailureShare,
			FirstDivergenceCount: n.FirstDivergenceCount,
			MeanCostShare:        n.MeanCostShare,
			MeanLatencyShare:     n.MeanLatencyShare,
			BottleneckDimensions: flagsByNode[n.NodeID],
			Pattern:              pat,
			Classified:           classified,
		})
	}
	// Per-node breakdown ordered by failure share (desc) — the hotspots first.
	sort.SliceStable(v.Nodes, func(i, j int) bool {
		if v.Nodes[i].FailureShare != v.Nodes[j].FailureShare {
			return v.Nodes[i].FailureShare > v.Nodes[j].FailureShare
		}
		return v.Nodes[i].NodeID < v.Nodes[j].NodeID
	})

	for _, c := range in.Clusters {
		v.Clusters = append(v.Clusters, ClusterRow{
			Signature:            string(c.Signature),
			Label:                c.Label,
			Size:                 c.Size,
			RepresentativeCaseID: c.RepresentativeCaseID,
			MemberCaseIDs:        c.MemberCaseIDs,
		})
	}

	for _, d := range in.Diagnoses {
		_, classified := classifiedOf(d.NodeID)
		v.Diagnoses = append(v.Diagnoses, DiagnosisCard{
			Code:            d.TaxonomyCode,
			Description:     diagnosis.Description(d.TaxonomyCode),
			NodeID:          d.NodeID,
			Source:          d.Source,
			Confidence:      d.Confidence,
			EvidenceCaseIDs: d.EvidenceCaseIDs,
			Agreement:       d.Agreement,
			NHuman:          d.NHuman,
			Calibrated:      d.Calibrated,
			AnalystFlagged:  d.AnalystFlagged,
			LowConfidence:   d.LowConfidence,
			Classified:      classified,
		})
	}
	sort.SliceStable(v.Diagnoses, func(i, j int) bool {
		if v.Diagnoses[i].NodeID != v.Diagnoses[j].NodeID {
			return v.Diagnoses[i].NodeID < v.Diagnoses[j].NodeID
		}
		return v.Diagnoses[i].Code < v.Diagnoses[j].Code
	})

	for _, a := range in.Ablations {
		v.Ablations = append(v.Ablations, AblationCard{
			NodeID: a.NodeID, Metric: a.Metric, DeltaMean: a.DeltaMean,
			CILow: a.CILow, CIHigh: a.CIHigh, NSeeds: a.NSeeds, Verdict: a.Verdict,
		})
		if a.Verdict == attribution.VerdictInconclusive {
			v.HasInconclusive = true
		}
	}
	sort.SliceStable(v.Ablations, func(i, j int) bool { return v.Ablations[i].NodeID < v.Ablations[j].NodeID })

	// Analyst status — present iff calibration was attempted, flagged iff below floor.
	if in.Analyst.NHuman > 0 || in.Analyst.Calibrated || anyAnalystDiagnosis(in.Diagnoses) {
		v.Analyst = AnalystStatus{
			Present:    true,
			Agreement:  in.Analyst.Agreement,
			NHuman:     in.Analyst.NHuman,
			Calibrated: in.Analyst.Calibrated,
			Floor:      in.Analyst.Floor,
			Flagged:    in.Analyst.BelowFloor(),
		}
		v.AnalystUncalibrated = in.Analyst.BelowFloor()
	}

	// Recovered topology — surfaced with per-edge provenance (FR15). Ordered deterministically.
	tv := TopologyView{
		OrderedByTopology: in.Contrib.OrderedByTopology,
		Provenance:        string(in.Contrib.TopologyProvenance),
	}
	for _, e := range in.Topology.Edges {
		tv.Edges = append(tv.Edges, EdgeView{
			From: e.From, To: e.To, Provenance: string(e.Provenance),
			Signal: string(e.Signal), Confidence: e.Confidence,
		})
	}
	sort.SliceStable(tv.Edges, func(i, j int) bool {
		if tv.Edges[i].From != tv.Edges[j].From {
			return tv.Edges[i].From < tv.Edges[j].From
		}
		return tv.Edges[i].To < tv.Edges[j].To
	})
	v.Topology = tv

	v.Partial = in.AblationInProgress

	// Aggregate state.
	switch {
	case in.Overall.NFailing == 0 && len(v.Clusters) == 0 && len(v.Diagnoses) == 0:
		v.State = StateEmpty
		v.Message = "no failing cases to diagnose"
	default:
		v.State = StateReady
	}
	return v
}

// ErrorView builds the error state — a scorecard that could not be computed renders as error, never
// as an empty-but-whole scorecard.
func ErrorView(v attribution.Variant, msg string) View {
	return View{
		VariantID: v.VariantID, EvalSetHash: v.EvalSetHash, ConfigHash: v.ConfigHash,
		WorkflowID: v.WorkflowID, State: StateError, Message: msg, ReadOnly: true,
	}
}

func anyAnalystDiagnosis(diags []diagnosis.Diagnosis) bool {
	for _, d := range diags {
		if d.Source == diagnosis.SourceAnalyst {
			return true
		}
	}
	return false
}
