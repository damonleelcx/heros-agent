package proposal

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Target is one thing to propose against: a P4.5 diagnosis (or a structural Signal), the node's
// pattern label, and the per-node data the prompt-rewrite optimizer needs. The caller (the scorecard /
// engine wiring) assembles Targets from the read-only P4.5 outputs.
type Target struct {
	Diagnosis  diagnosis.Diagnosis
	Signal     Signal
	Pattern    patternclassifier.Pattern
	Bottleneck *attribution.BottleneckFlag
	// Prompt-rewrite inputs (used only by the prompt-rewrite operator).
	BasePromptBody string
	Groundings     []FailingCaseGrounding
	RequiredFields []string
}

// Refusal records a candidate the engine declined to surface, and why — either an inadmissible
// operator/pattern pairing or a typed-contract violation. Refusals are kept for diagnostics (the
// design's "record the contract violation rather than surfacing a broken Variant Spec"), never ranked.
type Refusal struct {
	Operator OperatorKind `json:"operator"`
	NodeID   string       `json:"node_id"`
	Reason   string       `json:"reason"`
}

// Emission is the result of a proposal pass: the admissible candidates and the recorded refusals.
type Emission struct {
	Candidates []Candidate
	Refusals   []Refusal
}

// Engine emits candidate Variant Specs from targets, gated by pattern label and the typed I/O
// contract. It holds no state beyond its configuration; Propose is a pure function of (targets,
// config).
type Engine struct {
	// Catalog is the operator dispatch table. Defaults to DefaultCatalog() when nil.
	Catalog []Operator
	// Menu is the registry of candidate refs operators may select from.
	Menu Menu
	// Base is the baseline Variant Spec candidates are derived from.
	Base *variantspec.VariantSpec
	// Optimizer produces grounded prompt rewrites. Nil disables the prompt-rewrite operator.
	Optimizer PromptOptimizer
	// IR is the discovered IR, used by the contract gate and the wiring operators.
	IR *discovery.IR
	// Gate refuses contract-violating candidates. Nil admits every candidate (used by pure operator
	// tests where wiring never changes); production wires NewTypedContractGate(IR).
	Gate ContractGate
}

// Propose runs the catalog over every target and returns the admissible candidates plus recorded
// refusals. Determinism: operators fire in catalog order, targets in input order, and every derived
// spec is built from sorted iteration — so the emission is a pure function of the inputs.
func (e Engine) Propose(targets []Target) Emission {
	catalog := e.Catalog
	if catalog == nil {
		catalog = DefaultCatalog()
	}
	var em Emission
	for _, t := range targets {
		for _, op := range catalog {
			if !fires(op, t) {
				continue
			}
			if !admissibleForPattern(op, t.Pattern) {
				// The load-bearing pattern gate (§1.4/§1.5): an operator inadmissible for the node's
				// pattern (e.g. add-rerank on a Routing node) emits nothing and is recorded, never
				// surfaced.
				em.Refusals = append(em.Refusals, Refusal{Operator: op.Kind(), NodeID: t.Diagnosis.NodeID,
					Reason: "inadmissible for pattern " + string(t.Pattern)})
				continue
			}
			in := e.inputFor(op, t)
			cands, err := op.Propose(in)
			if err != nil {
				em.Refusals = append(em.Refusals, Refusal{Operator: op.Kind(), NodeID: t.Diagnosis.NodeID,
					Reason: err.Error()})
				continue
			}
			for _, c := range cands {
				if e.Gate != nil {
					ok, reason, _ := e.Gate.Admit(c)
					if !ok {
						em.Refusals = append(em.Refusals, Refusal{Operator: c.Operator, NodeID: c.NodeID,
							Reason: reason})
						continue
					}
				}
				em.Candidates = append(em.Candidates, c)
			}
		}
	}
	return em
}

// fires reports whether op is triggered by target t — by matching taxonomy code (diagnosis-driven) or
// by matching signal (structural). Diagnosis-driven firing also requires the code be admissible on the
// node's pattern per the frozen P4.5 taxonomy, so an operator never fires on a code the taxonomy says
// cannot occur on that pattern.
func fires(op Operator, t Target) bool {
	if op.HandlesSignal() != SignalNone && op.HandlesSignal() == t.Signal {
		return true
	}
	if t.Signal != SignalNone {
		return false
	}
	for _, code := range op.Handles() {
		if code == t.Diagnosis.TaxonomyCode && diagnosis.AdmissibleOn(code, t.Pattern) {
			return true
		}
	}
	return false
}

func (e Engine) inputFor(op Operator, t Target) OperatorInput {
	d := t.Diagnosis
	return OperatorInput{
		Diagnosis:       d,
		Signal:          t.Signal,
		Pattern:         t.Pattern,
		Base:            e.Base,
		Menu:            e.Menu,
		Bottleneck:      t.Bottleneck,
		PromptOptimizer: e.Optimizer,
		IR:              e.IR,
		BasePromptBody:  t.BasePromptBody,
		Groundings:      t.Groundings,
		RequiredFields:  t.RequiredFields,
	}
}

// SortCandidates orders candidates deterministically by (node, operator, first dimension) — a stable
// presentation order independent of emission order, used before compilation and ranking.
func SortCandidates(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].NodeID != cs[j].NodeID {
			return cs[i].NodeID < cs[j].NodeID
		}
		if cs[i].Operator != cs[j].Operator {
			return cs[i].Operator < cs[j].Operator
		}
		return firstDim(cs[i]) < firstDim(cs[j])
	})
}

func firstDim(c Candidate) string {
	if len(c.Dimensions) == 0 {
		return ""
	}
	return c.Dimensions[0]
}
