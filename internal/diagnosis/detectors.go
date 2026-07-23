package diagnosis

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/evalharness"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// detectors.go is §5: the deterministic, rules-first detector suite. Each detector reads the trace
// signals (never an LLM) and emits a typed cause from the fixed taxonomy. The same trace yields the
// same causes every run — the property the whole rules-first discipline rests on.
//
// Every detector's output is pattern-scoped through the SAME dispatcher §7 uses: a detector for a
// pattern-scoped code fires on a node only if the node's P3.5 pattern admits that code. That is why
// the rule path and the analyst path cannot diagnose a router for a RAG failure mode.

// Source records whether a cause came from a deterministic rule or the LLM analyst. It rides on every
// cause because the scorecard must show a human where a diagnosis came from — a rule is reproducible,
// an analyst opinion is calibrated-at-best.
type Source string

const (
	SourceRule    Source = "rule"
	SourceAnalyst Source = "analyst"
)

// TypedCause is one diagnosis: a taxonomy code localized to a node on a case, with its source,
// confidence, and the specific failing cases as evidence. A cause is NEVER a bare label — the
// evidence is part of the value, not an afterthought the UI is trusted to attach (task 6.x / Decision
// 6).
type TypedCause struct {
	Code       TaxonomyCode              `json:"code"`
	NodeID     string                    `json:"node_id"`
	CaseID     string                    `json:"case_id"`
	Pattern    patternclassifier.Pattern `json:"pattern"`
	Source     Source                    `json:"source"`
	Confidence float64                   `json:"confidence"`
	// Evidence is the specific failing case ids that triggered this cause. For a rule detector it is
	// the case itself; the aggregation layer widens it across a cluster.
	Evidence []string `json:"evidence"`
}

// ruleDetector is one deterministic rule: a code plus a fire function over the case's signals. The
// fire function returns the node the cause localizes to, or ok=false when the rule does not apply.
type ruleDetector struct {
	code TaxonomyCode
	fire func(ir *discovery.IR, fc attribution.FailingCase, s attribution.Signals) (nodeID string, ok bool)
}

// ruleDetectors is the enumerated suite (task 5.1). It is a fixed list, walked in a stable order, so
// "the detectors that ran" is a value and not a set that drifts.
var ruleDetectors = []ruleDetector{
	{CauseContextOverflow, func(_ *discovery.IR, _ attribution.FailingCase, s attribution.Signals) (string, bool) {
		return s.OverflowNode, s.ContextOverflow
	}},
	{CausePromptFormatDrift, func(ir *discovery.IR, fc attribution.FailingCase, _ attribution.Signals) (string, bool) {
		node, kind := attribution.FirstDivergence(ir, fc)
		return node, node != "" && kind == attribution.DivergenceContract
	}},
	{CauseLostInMiddle, func(_ *discovery.IR, _ attribution.FailingCase, s attribution.Signals) (string, bool) {
		return s.OverflowNode, s.LostInMiddle
	}},
	{CauseModelCapabilityGap, func(_ *discovery.IR, _ attribution.FailingCase, s attribution.Signals) (string, bool) {
		return s.CheapModelNode, s.CheapModelOnReasoningNode
	}},
	{CauseToolSchemaMismatch, func(_ *discovery.IR, _ attribution.FailingCase, s attribution.Signals) (string, bool) {
		return s.ToolErrorNode, s.ToolSchemaMismatch
	}},
	{CauseRetrievalMiss, func(_ *discovery.IR, _ attribution.FailingCase, s attribution.Signals) (string, bool) {
		return s.RetrievalMissNode, s.RetrievalMiss
	}},
}

// Detect runs the deterministic detectors over one failing case and returns the typed causes that
// fired, pattern-scoped and in stable code order (tasks 5.1–5.3). A rule cause carries confidence 1.0
// — it is a proof from the trace, not an estimate. A detector whose code is inadmissible on the
// target node's pattern is REFUSED (§7): it never emits a cause its pattern cannot exhibit.
func Detect(ir *discovery.IR, fc attribution.FailingCase) []TypedCause {
	s := attribution.DeriveSignals(fc)
	var out []TypedCause
	for _, d := range ruleDetectors {
		node, ok := d.fire(ir, fc, s)
		if !ok {
			continue
		}
		pattern, _ := evalharness.NodePattern(ir, node)
		if !AdmissibleOn(d.code, pattern) {
			// The node's pattern cannot exhibit this failure mode — refuse rather than record noise.
			continue
		}
		out = append(out, TypedCause{
			Code:       d.code,
			NodeID:     node,
			CaseID:     fc.Case.CaseID,
			Pattern:    pattern,
			Source:     SourceRule,
			Confidence: 1.0,
			Evidence:   []string{fc.Case.CaseID},
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Code < out[j].Code })
	return out
}

// DetectSet runs the detectors over every failing case and returns all rule causes plus the residue —
// the cases no rule explained. The residue is what the analyst runs on (§6), and returning it here
// (rather than recomputing it in the analyst) is what makes "the analyst is called only on the
// residue" a single, testable fact.
func DetectSet(ir *discovery.IR, cases []attribution.FailingCase) (causes []TypedCause, residue []attribution.FailingCase) {
	explained := map[string]bool{}
	for _, fc := range cases {
		cs := Detect(ir, fc)
		if len(cs) > 0 {
			explained[fc.Case.CaseID] = true
			causes = append(causes, cs...)
		}
	}
	for _, fc := range cases {
		if !explained[fc.Case.CaseID] {
			residue = append(residue, fc)
		}
	}
	return causes, residue
}

// AdmissibleCodes returns the taxonomy codes a node with the given pattern may be diagnosed with
// (§7.1). It is the selection the pattern label drives: a Routing node's list contains misroute but
// not retrieval_miss, a Reflection node's contains non_convergence / degradation_on_revision.
func AdmissibleCodes(pattern patternclassifier.Pattern) []TaxonomyCode {
	var out []TaxonomyCode
	for _, c := range Codes() {
		if AdmissibleOn(c, pattern) {
			out = append(out, c)
		}
	}
	return out
}
