package attribution

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/evalharness"
)

// bottleneck.go is §4: flag the node(s) that dominate spend or sit on the latency critical path, from
// the per-node cost/latency Pareto. It reuses the P2.5/P4 per-node contribution decomposition
// (evalharness.Contributions) — the same absolute cost/latency figures the leaderboard already reads
// — so a bottleneck flag can never disagree with the spend the board reports.

// Dimension tags a bottleneck flag with what the node dominates.
type Dimension string

const (
	DimCost    Dimension = "cost"
	DimLatency Dimension = "latency"
)

// BottleneckFlag names a node that dominates one dimension, with its dominance share. Dominance is
// the node's share of the run's total in that dimension, carried so the flag states HOW dominant, not
// just that it is — a 0.51 majority and a 0.97 monopoly are different facts.
type BottleneckFlag struct {
	VariantID   string    `json:"variant_id"`
	EvalSetHash string    `json:"eval_set_hash"`
	NodeID      string    `json:"node_id"`
	Dimension   Dimension `json:"dimension"`
	Dominance   float64   `json:"dominance"`
}

// BottleneckConfig controls flagging. Coverage is the cumulative share the flagged Pareto prefix must
// reach — the smallest set of nodes that together account for `Coverage` of the total is flagged, so
// "one node holds 97%" flags one node and "two nodes hold 40% each" flags both. Default 0.5 (the
// majority): a node or small set that owns the majority of a dimension is the bottleneck.
type BottleneckConfig struct {
	Coverage float64
}

func (c BottleneckConfig) coverage() float64 {
	if c.Coverage <= 0 || c.Coverage > 1 {
		return 0.5
	}
	return c.Coverage
}

// Bottleneck computes the cost and latency Pareto across nodes for a run's cases and flags the
// dominating node(s) per dimension (tasks 4.1, 4.2). The cases may be the failing set or the whole
// run — cost/latency dominance is a property of the run, not of failure.
func Bottleneck(v Variant, cases []FailingCase, cfg BottleneckConfig) []BottleneckFlag {
	costByNode := map[string]float64{}
	latByNode := map[string]float64{}
	for _, fc := range cases {
		for _, nc := range evalharness.Contributions(fc.Trace) {
			costByNode[nc.NodeID] += nc.CostUSD
			latByNode[nc.NodeID] += nc.LatencyMS
		}
	}
	var flags []BottleneckFlag
	flags = append(flags, paretoFlags(v, DimCost, costByNode, cfg.coverage())...)
	flags = append(flags, paretoFlags(v, DimLatency, latByNode, cfg.coverage())...)
	// Stable order: dimension then dominance (desc) then node id.
	sort.Slice(flags, func(i, j int) bool {
		if flags[i].Dimension != flags[j].Dimension {
			return flags[i].Dimension < flags[j].Dimension
		}
		if flags[i].Dominance != flags[j].Dominance {
			return flags[i].Dominance > flags[j].Dominance
		}
		return flags[i].NodeID < flags[j].NodeID
	})
	return flags
}

// paretoFlags returns the smallest prefix of nodes (by share, desc) whose cumulative share reaches
// the coverage target — the Pareto set that dominates the dimension.
func paretoFlags(v Variant, dim Dimension, totals map[string]float64, coverage float64) []BottleneckFlag {
	var total float64
	for _, t := range totals {
		total += t
	}
	if total <= 0 {
		return nil
	}
	type ns struct {
		node  string
		share float64
	}
	nss := make([]ns, 0, len(totals))
	for node, t := range totals {
		nss = append(nss, ns{node, t / total})
	}
	sort.Slice(nss, func(i, j int) bool {
		if nss[i].share != nss[j].share {
			return nss[i].share > nss[j].share
		}
		return nss[i].node < nss[j].node
	})
	var out []BottleneckFlag
	var cum float64
	for _, n := range nss {
		out = append(out, BottleneckFlag{
			VariantID: v.VariantID, EvalSetHash: v.EvalSetHash,
			NodeID: n.node, Dimension: dim, Dominance: n.share,
		})
		cum += n.share
		if cum >= coverage {
			break
		}
	}
	return out
}
