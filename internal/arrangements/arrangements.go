// Package arrangements enumerates the orderings of a workflow's nodes, validates each through the
// ordering-coherence validator (§1), scores it, and ranks the results: approved arrangements
// (coherent / adapter-augmented) first, rejected ones below, each group ordered by score. It is the
// backing logic for the editor's "explore all orderings" list view.
//
// # Bounded, and it says when it is bounded
//
// The number of orderings is n! — astronomically large for a real graph. Enumeration is therefore
// CAPPED, and the cap is SURFACED, never silent (the "no silent truncation" discipline): the result
// reports the total (or that it is too large to compute), how many were considered, and whether the
// list was truncated. A caller renders "showing N of M" rather than pretending it explored everything.
//
// # The score is a coherence-quality score, not an eval score
//
// Scoring here ranks orderings by how cleanly they satisfy the typed contract — coherent beats
// adapter-augmented beats rejected, and within rejected, fewer broken edges rank higher. It is NOT a P4
// leaderboard score (which requires actually RUNNING each variant over an eval set); it is the cheap,
// pure, deterministic signal available before any run, and it is labelled as such.
package arrangements

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/typedcontract"
)

// DefaultCap bounds how many orderings are enumerated and validated. 5040 = 7!, so any graph of ≤7
// nodes is explored exhaustively; larger graphs are sampled deterministically and the truncation is
// surfaced. Chosen so the whole enumeration stays well under a second.
const DefaultCap = 5040

// Arrangement is one ordering with its verdict and score.
type Arrangement struct {
	Order    []string                  `json:"order"`
	Kind     typedcontract.VerdictKind `json:"kind"`
	Score    float64                   `json:"score"` // coherence-quality score in [0,1]
	Approved bool                      `json:"approved"`
	// AdapterCount is how many adapters an `adapted` arrangement needs (0 for coherent).
	AdapterCount int `json:"adapter_count"`
	// RejectedEdgeCount is how many data edges a `rejected` arrangement breaks (0 when approved).
	RejectedEdgeCount int `json:"rejected_edge_count"`
}

// Ranking is the ordered result plus the honesty metadata about how much of the space was explored.
type Ranking struct {
	Arrangements []Arrangement `json:"arrangements"`
	// Total is the total number of orderings (n!). -1 means "too large to represent" (n > 12).
	Total int64 `json:"total"`
	// Considered is how many orderings were actually validated (≤ Cap and ≤ Total).
	Considered int `json:"considered"`
	// Truncated is true when Considered < Total — the list is a bounded sample, not exhaustive.
	Truncated bool `json:"truncated"`
	Cap       int  `json:"cap"`
	// ApprovedCount / RejectedCount summarise the considered set.
	ApprovedCount int `json:"approved_count"`
	RejectedCount int `json:"rejected_count"`
}

// Summary is the honesty metadata about how much of the ordering space was explored. It is shared by
// the batch Ranking and the streaming API.
type Summary struct {
	Total         int64 `json:"total"`
	Considered    int   `json:"considered"`
	Truncated     bool  `json:"truncated"`
	Cap           int   `json:"cap"`
	ApprovedCount int   `json:"approved_count"`
	RejectedCount int   `json:"rejected_count"`
}

// EnumerateStream validates orderings of `order` (permuted) against the fixed `edges` and calls `emit`
// once per arrangement AS IT IS DISCOVERED — in lexicographic permutation order, before any ranking.
// This is what the live "discovery" view consumes: each arrangement is surfaced the moment it is
// validated, and the client animates it into its ranked position. It returns the Summary once the
// (bounded) space is exhausted.
//
// The edges are the code's inherent data dependencies — they do NOT move when the order is permuted;
// only the execution order changes, which is exactly what re-arrangement does. Deterministic: the
// discovery order is a pure function of the base order.
func EnumerateStream(ir *discovery.IR, order []string, edges []typedcontract.Edge, catalog *typedcontract.Catalog, cap int, emit func(a Arrangement)) Summary {
	if catalog == nil {
		catalog = typedcontract.DefaultCatalog()
	}
	if cap <= 0 {
		cap = DefaultCap
	}
	n := len(order)
	dataEdges := 0
	for _, e := range edges {
		if e.Kind == "data" {
			dataEdges++
		}
	}

	sum := Summary{Total: factorial(n), Cap: cap}
	idx := make([]int, n)
	for i := range idx {
		idx[i] = i
	}
	for {
		perm := make([]string, n)
		for i, j := range idx {
			perm[i] = order[j]
		}
		a := score(ir, perm, edges, catalog, dataEdges)
		if a.Approved {
			sum.ApprovedCount++
		} else {
			sum.RejectedCount++
		}
		sum.Considered++
		if emit != nil {
			emit(a)
		}
		if sum.Considered >= cap {
			break
		}
		if !nextPermutation(idx) {
			break // exhausted all permutations
		}
	}
	sum.Truncated = sum.Total < 0 || int64(sum.Considered) < sum.Total
	return sum
}

// Enumerate validates orderings of `order` (permuted) against the fixed `edges` and returns a ranking:
// approved first, rejected below, each group by score descending. It is the batch form of
// EnumerateStream — it collects every discovered arrangement and sorts it. Deterministic.
func Enumerate(ir *discovery.IR, order []string, edges []typedcontract.Edge, catalog *typedcontract.Catalog, cap int) Ranking {
	var arrs []Arrangement
	sum := EnumerateStream(ir, order, edges, catalog, cap, func(a Arrangement) { arrs = append(arrs, a) })

	SortArrangements(arrs)
	return Ranking{
		Arrangements: arrs, Total: sum.Total, Considered: sum.Considered, Truncated: sum.Truncated,
		Cap: sum.Cap, ApprovedCount: sum.ApprovedCount, RejectedCount: sum.RejectedCount,
	}
}

// SortArrangements ranks in place: approved first, then by score descending; ties broken by the order
// string so the ranking is deterministic. Exported so the streaming client's final reconciliation and
// the batch endpoint use the identical rule.
func SortArrangements(arrs []Arrangement) {
	sort.SliceStable(arrs, func(i, j int) bool {
		ai, aj := arrs[i], arrs[j]
		if ai.Approved != aj.Approved {
			return ai.Approved // approved before rejected
		}
		if ai.Score != aj.Score {
			return ai.Score > aj.Score
		}
		return joinOrder(ai.Order) < joinOrder(aj.Order)
	})
}

// score validates one ordering and turns the verdict into a coherence-quality score.
//
//	coherent            → 1.0                        (approved)
//	adapted(k adapters) → 0.9 − 0.05·k, floor 0.55   (approved; more adapters cost a little)
//	rejected(r edges)   → 0.5·(dataEdges−r)/dataEdges (below every approved; fewer broken → higher)
func score(ir *discovery.IR, order []string, edges []typedcontract.Edge, catalog *typedcontract.Catalog, dataEdges int) Arrangement {
	v := typedcontract.ValidateOrdering(ir, typedcontract.Ordering{Order: order, Edges: edges}, catalog)
	a := Arrangement{Order: order, Kind: v.Kind}
	switch v.Kind {
	case typedcontract.VerdictCoherent:
		a.Approved, a.Score = true, 1.0
	case typedcontract.VerdictAdapted:
		a.Approved = true
		a.AdapterCount = len(v.Adapters)
		a.Score = 0.9 - 0.05*float64(a.AdapterCount)
		if a.Score < 0.55 {
			a.Score = 0.55
		}
	default: // rejected
		a.Approved = false
		a.RejectedEdgeCount = len(v.Diagnostics)
		if dataEdges > 0 {
			a.Score = 0.5 * float64(dataEdges-a.RejectedEdgeCount) / float64(dataEdges)
		}
	}
	return a
}

// nextPermutation advances idx to the next lexicographic permutation in place, returning false when the
// sequence is exhausted (the standard algorithm).
func nextPermutation(a []int) bool {
	n := len(a)
	if n < 2 {
		return false
	}
	i := n - 2
	for i >= 0 && a[i] >= a[i+1] {
		i--
	}
	if i < 0 {
		return false
	}
	j := n - 1
	for a[j] <= a[i] {
		j--
	}
	a[i], a[j] = a[j], a[i]
	for l, r := i+1, n-1; l < r; l, r = l+1, r-1 {
		a[l], a[r] = a[r], a[l]
	}
	return true
}

// factorial returns n! or -1 when it would overflow int64 (n > 20). Used only to report the total; the
// enumeration itself is capped independently.
func factorial(n int) int64 {
	if n < 0 {
		return 0
	}
	if n > 20 {
		return -1
	}
	out := int64(1)
	for i := 2; i <= n; i++ {
		out *= int64(i)
	}
	return out
}

func joinOrder(order []string) string {
	out := ""
	for i, s := range order {
		if i > 0 {
			out += "\x00"
		}
		out += s
	}
	return out
}
