// Package hostedscorecard assembles the P4.5 scorecard from a LINKED run.
//
// # What a linked run can and cannot support
//
// The scorecard answers "where did this variant's cost, latency and FAILURES come from". Migration 0023
// brought per-node cost and latency across the boundary, so the first two are now real, attributed
// numbers rather than an aggregate the console would have to split by guessing.
//
// The third is not, and this package's whole job is to be honest about that. Failure attribution needs
// per-node CORRECTNESS per case — which node first diverged, on which case — and that is eval data. It
// does not cross, it is not on the allowlist, and there is no field it could occupy. Everything built on
// top of it is equally absent: failure clusters, typed diagnoses, ablations.
//
// 🔴 So the card reports `FailureAttribution: unavailable`, and every downstream list stays empty with
// that one field explaining why. The alternative — shipping NodeRows with FailureShare 0 — would say
// "no node caused any failures" on a variant that may be failing badly, which is worse than showing
// nothing at all. "Not to blame" and "not investigated" are opposite findings.
//
// A tenant who wants real failure attribution runs the eval where the eval data lives; that is what
// `heros eval` and the local scorecard already do. This is the hosted VIEW of a run they linked.
package hostedscorecard

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/scorecard"
)

// RunSource is the read this package needs.
type RunSource interface {
	ForWorkflow(tenantID, workflowID string) ([]linkingest.LinkedRun, error)
	LinkedRunIDs(tenantID string) ([]string, error)
	Get(tenantID, runID string) (linkingest.LinkedRun, bool, error)
}

// Build assembles a scorecard from one linked run.
func Build(lr linkingest.LinkedRun) scorecard.View {
	v := scorecard.View{
		// The variant IS the configuration, exactly as on the board.
		VariantID:  lr.ConfigHash,
		ConfigHash: lr.ConfigHash,
		WorkflowID: lr.WorkflowID,
		State:      scorecard.StateReady,
		ReadOnly:   true,
		// The load-bearing field. See the package header.
		FailureAttribution: scorecard.FailureUnavailable,
		Message: "Cost and latency are attributed per node from the linked run. Failure attribution, " +
			"failure clusters, diagnoses and ablations are NOT shown: they are computed from per-node " +
			"correctness, which is eval data and stays on the machine that ran the eval.",
	}

	quality, hasQuality := scoreOf(lr.Scores, "quality")
	cost, _ := scoreOf(lr.Scores, "cost_usd")
	latency, _ := scoreOf(lr.Scores, "latency_ms")

	v.Overall = scorecard.OverallMetrics{
		NCases:    lr.Eval.CaseCount,
		CostUSD:   cost.Value,
		LatencyMS: latency.Value,
	}
	if hasQuality {
		v.Overall.TaskSuccess = quality.Value
	}
	// NFailing is deliberately left at zero and NOT derived as round((1-quality)*NCases). That
	// arithmetic looks reasonable and is an invention: quality here is the mean FRACTION OF NODES
	// answering correctly per run, not a per-case pass/fail, so the product is not a count of anything.
	// A fabricated failure count on a card whose whole subject is failure would be the worst possible
	// place to guess.

	v.Nodes = nodeRows(lr.PerNode)
	// Every node is unclassified on this card: pattern labels come from the discovered IR, which this
	// assembler does not read. Counted rather than left blank, so a reader sees what the missing labels
	// cost their diagnosis coverage instead of mistaking "not classified" for "nothing wrong".
	v.UnclassifiedNodeCount = len(v.Nodes)
	v.ClassifiedNodeCount = 0

	if len(v.Nodes) == 0 {
		// No per-node breakdown crossed: an older run, or a workflow with no nodes. Empty rather than
		// ready — a scorecard with no rows and a "ready" badge reads as "we looked and found nothing".
		v.State = scorecard.StateEmpty
		v.Message = "This run was linked without per-node metrics, so there is nothing to attribute. " +
			"Re-run `heros eval` and link it again to get per-node cost and latency."
	}
	return v
}

// nodeRows turns per-node metrics into SHARES of the run's total.
//
// Shares rather than absolute values because the card's question is "which node dominates", and a share
// answers it without the reader dividing. The denominator is the sum of the per-node values, not the
// run's reported aggregate: the aggregate is a per-run MEAN and the per-node figures are sums over the
// same run, so dividing one by the other would produce shares that do not add to 1 and nobody could
// explain.
func nodeRows(perNode map[string]runlink.NodeMetric) []scorecard.NodeRow {
	if len(perNode) == 0 {
		return nil
	}
	var totalCost, totalLatency float64
	for _, m := range perNode {
		totalCost += m.CostUSD
		totalLatency += m.LatencyMS
	}

	rows := make([]scorecard.NodeRow, 0, len(perNode))
	for id, m := range perNode {
		rows = append(rows, scorecard.NodeRow{
			NodeID:           id,
			MeanCostShare:    share(m.CostUSD, totalCost),
			MeanLatencyShare: share(m.LatencyMS, totalLatency),
			// FailureShare and FirstDivergenceCount stay zero. FailureAttribution on the view is what
			// tells the reader that zero means "not computed" here.
			Classified: false,
		})
	}
	// Most expensive first — the card's first question is "what should I look at" — with the node id
	// breaking ties so the order does not shuffle between reloads.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].MeanCostShare != rows[j].MeanCostShare {
			return rows[i].MeanCostShare > rows[j].MeanCostShare
		}
		return rows[i].NodeID < rows[j].NodeID
	})
	return rows
}

// share divides safely: a zero denominator yields a zero share rather than NaN, which would serialize as
// `null` and render as an empty cell that looks like missing data.
func share(v, total float64) float64 {
	if total == 0 {
		return 0
	}
	return v / total
}

func scoreOf(scores []runlink.Score, metric string) (runlink.Score, bool) {
	for _, s := range scores {
		if s.Metric == metric {
			return s, true
		}
	}
	return runlink.Score{}, false
}

// Source serves scorecards from a linked-run store.
type Source struct{ runs RunSource }

// NewSource returns a scorecard source over a linked-run store.
func NewSource(runs RunSource) *Source { return &Source{runs: runs} }

// Scorecard returns the card for a variant id, which on this platform is a CONFIG HASH.
//
// Resolving a config hash to a run requires a scan of the tenant's linked runs, because the store is
// keyed by run id. That is honest but O(n) per request; it is bounded by one tenant's link history and
// is the read a board row's "open scorecard" link performs. An index on (tenant, config_hash) is the fix
// if it ever matters, and it is a migration rather than a change here.
//
// ok=false means this tenant has no linked run for that configuration. A read failure also returns
// false — the api.ScorecardSource signature has nowhere to put an error, a limitation inherited rather
// than introduced, stated rather than hidden.
func (s *Source) Scorecard(tenantID, variantID string) (scorecard.View, bool) {
	ids, err := s.runs.LinkedRunIDs(tenantID)
	if err != nil {
		return scorecard.View{}, false
	}
	var newest linkingest.LinkedRun
	var found bool
	for _, id := range ids {
		lr, ok, err := s.runs.Get(tenantID, id)
		if err != nil || !ok || lr.ConfigHash != variantID {
			continue
		}
		// Newest wins, matching the board: the two must agree about which run represents a
		// configuration, or clicking a board row would open a scorecard for a different measurement.
		if !found || lr.LinkedAt.After(newest.LinkedAt) {
			newest, found = lr, true
		}
	}
	if !found {
		return scorecard.View{}, false
	}
	return Build(newest), true
}
