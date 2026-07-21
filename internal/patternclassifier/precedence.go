package patternclassifier

import (
	"fmt"
	"sort"
)

// ownershipRank orders the taxonomy groups for the ONE case where two region claims genuinely
// contest the same nodes. Control-flow ranks first because a control-flow pattern describes the
// SHAPE of the region — what activates what — and that is the claim a metric-set is dispatched on;
// the other groups describe what happens inside a shape.
func ownershipRank(p Pattern) int {
	info, ok := Info(p)
	if !ok {
		return 99
	}
	switch info.Group {
	case GroupControlFlow:
		return 0
	case GroupCoordination:
		return 1
	case GroupCapability:
		return 2
	case GroupGovernance:
		return 3
	}
	return 99
}

// resolvedRegion is a proposal that survived arbitration, carrying the identity the partitioner
// assigned it.
type resolvedRegion struct {
	RegionProposal
	SubgraphID string
	NodeIDs    []string // normalized
}

// resolve turns raw detector proposals into the final set of labelled regions, applying the overlap
// contract. It is the SINGLE place precedence lives; detectors never look at each other.
//
// The contract, in the order the cases are checked:
//
//  1. NODE-SCOPED proposals never contest. A capability on a node (Tool Use) co-exists with whatever
//     region owns that node — a tool-bound node inside a Routing branch yields BOTH labels, Routing
//     on the subgraph and Tool Use on the node. That is the resolution of PRD Q3, and it is why a
//     node-scoped label's subgraph_ref is the node_id itself.
//
//  2. IDENTICAL node sets co-exist. Two signatures matching exactly the same region are two true
//     statements about it (they share one subgraph_id, since ids are content-addressed), not a
//     contest. Suppressing one here would silently delete a legitimate composite.
//
//  3. NESTING co-exists, as two separate subgraphs. A smaller region strictly inside a larger one is
//     a real composition ("a router inside a parallel branch"); each keeps its own subgraph_ref, so
//     each dispatches its own metric-set.
//
//  4. PARTIAL overlap is the only true contest — two regions sharing some nodes with neither
//     containing the other. Here the region is genuinely ambiguous, so one owner is chosen by a
//     total, deterministic order: lower ownershipRank, then more nodes ("maximal region matching a
//     single structural signature"), then lexically smaller subgraph_id. The loser is DROPPED WITH A
//     DIAGNOSTIC — never silently, because a dropped label is a metric-set that will not be computed.
func resolve(proposals []RegionProposal, diags *diagSink) []resolvedRegion {
	var nodeScoped, regionScoped []resolvedRegion
	for _, p := range proposals {
		ids := normalizeNodeIDs(p.NodeIDs)
		if len(ids) == 0 {
			diags.add(Diagnostic{Stage: StagePartition, RawPattern: string(p.Pattern),
				Reason: fmt.Sprintf("detector %s proposed an empty region", p.DetectorID)})
			continue
		}
		r := resolvedRegion{RegionProposal: p, NodeIDs: ids}
		if p.Scope == ScopeNode {
			// A node-scoped label names the node itself: readable, already stable, and it makes the
			// write-back target unambiguous (the node's own pattern_labels).
			r.SubgraphID = ids[0]
			nodeScoped = append(nodeScoped, r)
			continue
		}
		r.SubgraphID = SubgraphIDFor(ids)
		regionScoped = append(regionScoped, r)
	}

	// Deterministic arbitration order: strongest claim first, so the winner of a partial overlap does
	// not depend on the order detectors happened to run in.
	sort.SliceStable(regionScoped, func(i, j int) bool {
		a, b := regionScoped[i], regionScoped[j]
		if ra, rb := ownershipRank(a.Pattern), ownershipRank(b.Pattern); ra != rb {
			return ra < rb
		}
		if len(a.NodeIDs) != len(b.NodeIDs) {
			return len(a.NodeIDs) > len(b.NodeIDs)
		}
		if a.SubgraphID != b.SubgraphID {
			return a.SubgraphID < b.SubgraphID
		}
		return a.Pattern < b.Pattern
	})

	var kept []resolvedRegion
	for _, cand := range regionScoped {
		conflict := -1
		for i, k := range kept {
			if rel := overlap(k.NodeIDs, cand.NodeIDs); rel == overlapPartial {
				conflict = i
				break
			}
		}
		if conflict >= 0 {
			w := kept[conflict]
			diags.add(Diagnostic{
				Stage: StagePartition, SubgraphRef: cand.SubgraphID, RawPattern: string(cand.Pattern),
				Source: SourceRule,
				Reason: fmt.Sprintf("region partially overlaps subgraph %s (%s, detector %s) with neither containing the other; "+
					"the higher-precedence region owns the nodes and this proposal (detector %s) was dropped",
					w.SubgraphID, w.Pattern, w.DetectorID, cand.DetectorID),
			})
			continue
		}
		kept = append(kept, cand)
	}

	out := append(kept, nodeScoped...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].SubgraphID != out[j].SubgraphID {
			return out[i].SubgraphID < out[j].SubgraphID
		}
		return out[i].Pattern < out[j].Pattern
	})
	return out
}

type overlapRel int

const (
	overlapNone overlapRel = iota
	overlapEqual
	overlapContains // a ⊃ b or b ⊃ a
	overlapPartial
)

// overlap classifies the set relation between two normalized (sorted, unique) node-id sets.
func overlap(a, b []string) overlapRel {
	set := make(map[string]bool, len(a))
	for _, x := range a {
		set[x] = true
	}
	shared := 0
	for _, y := range b {
		if set[y] {
			shared++
		}
	}
	switch {
	case shared == 0:
		return overlapNone
	case shared == len(a) && shared == len(b):
		return overlapEqual
	case shared == len(a) || shared == len(b):
		return overlapContains
	default:
		return overlapPartial
	}
}
