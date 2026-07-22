package evalharness

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// scope.go is task 1.4: the pattern label is the DISPATCHER. The harness reads each node's P3.5
// label and applies only the metrics that label admits, and refuses to compute an inadmissible
// metric on a node. Computing every metric everywhere is both wasteful (judge calls cost money) and
// wrong — relevance@k on a router is a number with no meaning, and a number with no meaning on a
// leaderboard is worse than a blank.

// NodePlan is one node's measurement plan: what it is labeled and which registered evaluators are
// admissible on it. Uncovered is the diagnostic half — the evaluators that were REFUSED and why —
// because "we chose not to measure this here" is a fact a reviewer needs to see, not an absence.
type NodePlan struct {
	Target     Target      `json:"target"`
	Evaluators []string    `json:"evaluators"`
	MetricSet  []string    `json:"metric_set"`
	Primary    string      `json:"primary_metric"`
	Refusals   []Refusal   `json:"refusals,omitempty"`
	Labels     []nodeLabel `json:"labels,omitempty"`
}

type nodeLabel struct {
	Pattern    patternclassifier.Pattern `json:"pattern"`
	Confidence float64                   `json:"confidence"`
	Source     string                    `json:"source"`
	Candidate  bool                      `json:"candidate"`
}

// Refusal records an evaluator that was not run on a target, with the reason. It is persisted with
// the plan so "why is there no relevance@k on node r?" is answerable without re-running anything.
type Refusal struct {
	Evaluator string `json:"evaluator"`
	Metric    string `json:"metric"`
	Reason    string `json:"reason"`
}

// Plan is the whole workflow's measurement plan: the run-scoped evaluators plus one NodePlan per
// node, in stable node order.
type Plan struct {
	WorkflowID string     `json:"workflow_id"`
	Run        NodePlan   `json:"run"`
	Nodes      []NodePlan `json:"nodes"`
}

// NodePattern returns the winning P3.5 pattern label for a node: the highest-confidence label
// carried on the node itself, else the highest-confidence label of a subgraph containing it. A
// CANDIDATE label (behavioral, unconfirmed) loses to a confirmed one at equal confidence, because a
// candidate is a hypothesis and dispatching measurement off a hypothesis is how a node gets scored
// on metrics its region does not actually implement.
func NodePattern(ir *discovery.IR, nodeID string) (patternclassifier.Pattern, []nodeLabel) {
	if ir == nil {
		return "", nil
	}
	var cands []discovery.IRPatternLabel
	for _, n := range ir.Nodes {
		if n.NodeID == nodeID {
			cands = append(cands, n.PatternLabels...)
		}
	}
	for _, sg := range ir.Subgraphs {
		for _, member := range sg.NodeIDs {
			if member == nodeID {
				cands = append(cands, sg.PatternLabels...)
				break
			}
		}
	}
	if len(cands) == 0 {
		return "", nil
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.Candidate != b.Candidate {
			return !a.Candidate // confirmed before candidate
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		return a.Pattern < b.Pattern // total order so the winner is deterministic
	})
	labels := make([]nodeLabel, 0, len(cands))
	for _, c := range cands {
		labels = append(labels, nodeLabel{
			Pattern:    patternclassifier.Pattern(c.Pattern),
			Confidence: c.Confidence,
			Source:     c.Source,
			Candidate:  c.Candidate,
		})
	}
	return patternclassifier.Pattern(cands[0].Pattern), labels
}

// BuildPlan selects, for every node in the IR, exactly the registered evaluators that node's pattern
// label admits — and records every refusal. Run-scoped evaluators (pattern-agnostic) form the run
// plan; a pattern-scoped evaluator is never admissible at run scope.
func BuildPlan(ir *discovery.IR, reg *Registry) Plan {
	p := Plan{Run: NodePlan{Target: RunTarget()}}
	if ir == nil || reg == nil {
		return p
	}
	p.WorkflowID = ir.Workflow.ID
	evs := reg.Evaluators()

	for _, e := range evs {
		tgt := RunTarget()
		if err := Admissible(e, tgt); err != nil {
			p.Run.Refusals = append(p.Run.Refusals, Refusal{Evaluator: e.Name(), Metric: e.Metric(), Reason: err.Error()})
			continue
		}
		p.Run.Evaluators = append(p.Run.Evaluators, e.Name())
	}

	nodes := append([]discovery.IRNode(nil), ir.Nodes...)
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	for _, n := range nodes {
		pat, labels := NodePattern(ir, n.NodeID)
		np := NodePlan{Target: NodeTarget(n.NodeID, pat), Labels: labels}
		if set, ok := patternclassifier.MetricSetFor(pat); ok {
			np.MetricSet = append([]string(nil), set.Metrics...)
			np.Primary = set.Primary
		}
		for _, e := range evs {
			if err := Admissible(e, np.Target); err != nil {
				np.Refusals = append(np.Refusals, Refusal{Evaluator: e.Name(), Metric: e.Metric(), Reason: err.Error()})
				continue
			}
			np.Evaluators = append(np.Evaluators, e.Name())
		}
		p.Nodes = append(p.Nodes, np)
	}
	return p
}

// EvaluatorsFor returns the registered evaluators admissible on tgt, in stable order. It is the
// single query the harness makes per target; there is no path that runs an evaluator without going
// through Admissible.
func (r *Registry) EvaluatorsFor(tgt Target) []Evaluator {
	var out []Evaluator
	for _, e := range r.Evaluators() {
		if Admissible(e, tgt) == nil {
			out = append(out, e)
		}
	}
	return out
}
