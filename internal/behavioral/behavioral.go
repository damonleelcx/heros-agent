// Package behavioral implements P5's behavioral pattern confirmation and anti-pattern detection
// (Decision 5). Topology could only GUESS Reflection / Planning / Memory Management / HITL /
// Self-Consistency as capped structural candidates (P3.5); the trace CONFIRMS them.
//
// # Rules first, over trace signatures — the same discipline as P3.5/P4.5
//
// Confirmation is deterministic rules over trace signatures, not an LLM guess:
//
//	iteration count > 1 on a self-edge          → Reflection
//	a planning node's task list consumed downstream → Planning
//	sampling one node N times then voting        → Self-Consistency (Reasoning Techniques)
//	memory read/write against a store between turns → Memory Management
//	a human-approval pause in the trace          → Human-in-the-Loop
//
// A confirmed label carries `source = behavioral` and is written back additively at the same
// `ir_version` MAJOR. A one-shot self-edge (iteration count == 1) is NOT confirmed Reflection — a
// self-edge that fires once is not a loop, and must get no convergence metrics. An LLM-as-classifier
// handles only the ambiguous residue the rules leave, constrained to the fixed taxonomy, and NEVER
// overrides a confident rule/behavioral label.
//
// # Anti-patterns are diagnoses, not fixes
//
// Anti-patterns fall out as typed diagnoses with evidence (never-improving reflection loop; router
// sending everything one way; parallelization with no real independence; plan never followed),
// consumable by P5.5. P5 surfaces them; P5.5's change operators propose a fix and verification decides.
package behavioral

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/reconcile"
)

// Evidence bundles the trace signals the rules read: the reconciliation report, plus optional
// per-invocation quality scores and behavioral markers the interceptor captured. Optional fields let a
// caller confirm what it has evidence for and no more.
type Evidence struct {
	Report reconcile.Report
	// Quality[nodeID][i] is a 0..1 quality score for invocation i of a node — used to confirm that a
	// Reflection loop actually improves (and to flag one that does not).
	Quality map[string][]float64
	// MemoryOps[nodeID] lists observed store operations ("read"/"write") for a Memory Management node.
	MemoryOps map[string][]string
	// HumanPauses[nodeID] is true if the trace recorded a human-approval pause at that node.
	HumanPauses map[string]bool
	// BranchTraffic[routerID][branchID] is how many invocations a router sent to each branch — used to
	// detect a router that sends (nearly) all traffic one way.
	BranchTraffic map[string]map[string]int
	// ParallelOutputs[nodeID][i] is invocation i's output content hash — identical hashes across a
	// parallelization's branches mean the branches are not actually independent.
	ParallelOutputs map[string][]string
}

// AntiPatternKind is the closed set of P5 anti-pattern diagnoses (consumable by P5.5).
type AntiPatternKind string

const (
	ReflectionNoImprove    AntiPatternKind = "reflection_no_improve"
	RouterOneWay           AntiPatternKind = "router_one_way"
	ParallelNoIndependence AntiPatternKind = "parallel_no_independence"
	PlanNotFollowed        AntiPatternKind = "plan_not_followed"
)

// AntiPattern is a typed diagnosis with attached evidence. A DIAGNOSIS, not a fix.
type AntiPattern struct {
	Kind        AntiPatternKind `json:"kind"`
	SubgraphRef string          `json:"subgraph_ref"`
	Evidence    map[string]any  `json:"evidence"`
	Message     string          `json:"message"`
}

// Result is the confirmation output: confirmed behavioral labels, the metric-set each selects (task
// 6.3), and any anti-pattern diagnoses.
type Result struct {
	Confirmed    []patternclassifier.Label
	MetricSets   map[string]patternclassifier.MetricSet
	AntiPatterns []AntiPattern
}

// Confirm applies the behavioral rules to the structural candidates using the trace evidence. It only
// UPGRADES a matching P3.5 candidate — it never invents a label for a pattern nobody flagged
// structurally — and it wires each confirmed pattern to its metric-set.
func Confirm(ir *discovery.IR, candidates []patternclassifier.Label, ev Evidence) Result {
	res := Result{MetricSets: map[string]patternclassifier.MetricSet{}}
	subgraphNodes := subgraphIndex(ir)
	selfEdge, downstream := edgeIndex(ev.Report)
	invocations := invocationCounts(ev.Report)

	// Sort candidates for deterministic output.
	cands := append([]patternclassifier.Label(nil), candidates...)
	sort.Slice(cands, func(i, j int) bool { return patternclassifier.LabelLess(cands[i], cands[j]) })

	for _, cand := range cands {
		nodes := resolveNodes(cand.SubgraphRef, subgraphNodes)
		if confirmed, ok := confirmCandidate(cand, nodes, ev, selfEdge, downstream, invocations); ok {
			res.Confirmed = append(res.Confirmed, confirmed)
			if ms, ok := patternclassifier.MetricSetFor(confirmed.Pattern); ok {
				res.MetricSets[confirmed.SubgraphRef] = ms
			}
		}
	}

	res.AntiPatterns = detectAntiPatterns(cands, subgraphNodes, ev, selfEdge, invocations)
	return res
}

// confirmCandidate applies the per-pattern rule. It returns the confirmed label (source=behavioral) and
// ok=true only when the trace evidence supports the pattern.
func confirmCandidate(cand patternclassifier.Label, nodes []string, ev Evidence,
	selfEdge map[string]bool, downstream map[string][]string, inv map[string]int) (patternclassifier.Label, bool) {

	confirmed := patternclassifier.Label{
		Pattern: cand.Pattern, Source: patternclassifier.SourceBehavioral,
		SubgraphRef: cand.SubgraphRef, TaxonomyVersion: patternclassifier.TaxonomyVersion,
		Confidence: 0.9, Candidate: false,
	}

	switch cand.Pattern {
	case patternclassifier.Reflection:
		// iteration count > 1 on a self-edge.
		for _, n := range nodes {
			if selfEdge[n] && inv[n] > 1 {
				return confirmed, true
			}
		}
		return patternclassifier.Label{}, false

	case patternclassifier.Planning:
		// a planning node emitting a task list consumed downstream: the node ran AND has a downstream
		// consumer that also ran.
		for _, n := range nodes {
			if inv[n] > 0 {
				for _, d := range downstream[n] {
					if inv[d] > 0 {
						return confirmed, true
					}
				}
			}
		}
		return patternclassifier.Label{}, false

	case patternclassifier.ReasoningTechniques:
		// Self-Consistency: sample one node N (>=3) times then a downstream aggregator consumes them.
		for _, n := range nodes {
			if inv[n] >= 3 && len(downstream[n]) > 0 {
				return confirmed, true
			}
		}
		return patternclassifier.Label{}, false

	case patternclassifier.MemoryManagement:
		// memory read/write against a store between turns: both a read and a write observed.
		for _, n := range nodes {
			if hasReadAndWrite(ev.MemoryOps[n]) {
				return confirmed, true
			}
		}
		return patternclassifier.Label{}, false

	case patternclassifier.HumanInTheLoop:
		// a human-approval pause in the trace.
		for _, n := range nodes {
			if ev.HumanPauses[n] {
				return confirmed, true
			}
		}
		return patternclassifier.Label{}, false

	default:
		// Not a behavioral pattern this layer confirms (structural patterns stay as P3.5 produced them).
		return patternclassifier.Label{}, false
	}
}

// detectAntiPatterns runs the four typed detectors over the candidates + evidence.
func detectAntiPatterns(cands []patternclassifier.Label, subgraphNodes map[string][]string,
	ev Evidence, selfEdge map[string]bool, inv map[string]int) []AntiPattern {

	var out []AntiPattern
	for _, cand := range cands {
		nodes := resolveNodes(cand.SubgraphRef, subgraphNodes)
		switch cand.Pattern {
		case patternclassifier.Reflection:
			for _, n := range nodes {
				if selfEdge[n] && inv[n] > 1 {
					if q, ok := ev.Quality[n]; ok && !improves(q) {
						out = append(out, AntiPattern{
							Kind: ReflectionNoImprove, SubgraphRef: cand.SubgraphRef,
							Evidence: map[string]any{"per_iteration_quality": q, "node_id": n},
							Message:  fmt.Sprintf("Reflection loop on %q iterated %d times with no quality gain across iterations", n, inv[n]),
						})
					}
				}
			}
		case patternclassifier.Routing:
			for _, n := range nodes {
				if bt, ok := ev.BranchTraffic[n]; ok {
					if branch, share := dominantBranch(bt); share >= 0.9 {
						out = append(out, AntiPattern{
							Kind: RouterOneWay, SubgraphRef: cand.SubgraphRef,
							Evidence: map[string]any{"branch_traffic": bt, "dominant_branch": branch, "share": share},
							Message:  fmt.Sprintf("Router %q sent %.0f%% of traffic to a single branch %q", n, share*100, branch),
						})
					}
				}
			}
		case patternclassifier.Parallelization:
			for _, n := range nodes {
				if outs, ok := ev.ParallelOutputs[n]; ok && allIdentical(outs) && len(outs) > 1 {
					out = append(out, AntiPattern{
						Kind: ParallelNoIndependence, SubgraphRef: cand.SubgraphRef,
						Evidence: map[string]any{"output_hashes": outs, "node_id": n},
						Message:  fmt.Sprintf("Parallelization on %q produced identical outputs across %d branches — no real independence", n, len(outs)),
					})
				}
			}
		case patternclassifier.Planning:
			// plan never followed: the planning node ran but no downstream consumer did.
			_, downstream := edgeIndex(ev.Report)
			for _, n := range nodes {
				if inv[n] > 0 && !anyObserved(downstream[n], inv) && len(downstream[n]) > 0 {
					out = append(out, AntiPattern{
						Kind: PlanNotFollowed, SubgraphRef: cand.SubgraphRef,
						Evidence: map[string]any{"planning_node": n, "unfollowed_consumers": downstream[n]},
						Message:  fmt.Sprintf("Plan emitted by %q was never followed — no downstream consumer executed", n),
					})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SubgraphRef != out[j].SubgraphRef {
			return out[i].SubgraphRef < out[j].SubgraphRef
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

// ── evidence helpers ──────────────────────────────────────────────────────

func subgraphIndex(ir *discovery.IR) map[string][]string {
	out := map[string][]string{}
	for _, sg := range ir.Subgraphs {
		out[sg.SubgraphID] = append([]string(nil), sg.NodeIDs...)
	}
	return out
}

// resolveNodes maps a subgraphRef to its node ids, tolerating a subgraphRef that is itself a node id
// (the single-node case, common for a self-looping Reflection node).
func resolveNodes(subgraphRef string, subgraphNodes map[string][]string) []string {
	if ns, ok := subgraphNodes[subgraphRef]; ok {
		return ns
	}
	return []string{subgraphRef}
}

func edgeIndex(rep reconcile.Report) (selfEdge map[string]bool, downstream map[string][]string) {
	selfEdge = map[string]bool{}
	downstream = map[string][]string{}
	for _, e := range rep.Edges {
		if e.FromNodeID == e.ToNodeID {
			selfEdge[e.FromNodeID] = true
			continue
		}
		downstream[e.FromNodeID] = append(downstream[e.FromNodeID], e.ToNodeID)
	}
	return selfEdge, downstream
}

func invocationCounts(rep reconcile.Report) map[string]int {
	out := map[string]int{}
	for _, n := range rep.Nodes {
		out[n.NodeID] = n.InvocationCount
	}
	return out
}

func hasReadAndWrite(ops []string) bool {
	var r, w bool
	for _, op := range ops {
		switch op {
		case "read":
			r = true
		case "write":
			w = true
		}
	}
	return r && w
}

// improves reports whether a per-iteration quality series shows ANY gain — the last score exceeds the
// first. A flat or declining series is a never-improving loop.
func improves(q []float64) bool {
	if len(q) < 2 {
		return true // not enough evidence to call it non-improving
	}
	return q[len(q)-1] > q[0]
}

func dominantBranch(bt map[string]int) (string, float64) {
	total := 0
	for _, c := range bt {
		total += c
	}
	if total == 0 {
		return "", 0
	}
	best, bestC := "", -1
	for _, b := range sortedKeys(bt) {
		if bt[b] > bestC {
			best, bestC = b, bt[b]
		}
	}
	return best, float64(bestC) / float64(total)
}

func allIdentical(xs []string) bool {
	for i := 1; i < len(xs); i++ {
		if xs[i] != xs[0] {
			return false
		}
	}
	return len(xs) > 0
}

func anyObserved(nodes []string, inv map[string]int) bool {
	for _, n := range nodes {
		if inv[n] > 0 {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
