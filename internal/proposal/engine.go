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
	// Usage is the P14 tool/skill selection evidence for this node (which capabilities the eval set
	// exercised, which errored). Assembled by the caller from the run traces; absent means "no evidence
	// recorded", and the selection operators then emit nothing.
	Usage ToolUsage
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
	// BaseVariantID is the baseline's config_hash, when the caller has resolved it. Wiring candidates
	// record it as their ParentVariantID (P15 design Decision 2). Empty is legitimate: a caller that
	// has not resolved the baseline supplies nothing rather than a made-up id.
	BaseVariantID string
	// Optimizer produces grounded prompt rewrites. Nil disables the prompt-rewrite operator.
	Optimizer PromptOptimizer
	// IR is the discovered IR, used by the contract gate and the wiring operators.
	IR *discovery.IR
	// Gate refuses contract-violating candidates. Nil admits every candidate (used by pure operator
	// tests where wiring never changes); production wires NewTypedContractGate(IR).
	Gate ContractGate
	// DropTolerance is the drop-tolerance admissibility gate (P16 task 5.3). Its zero value is a working
	// gate that judges on the menu's estimates; a caller with eval telemetry populates Observed so it
	// judges on measurements where it has them.
	//
	// It is a SEPARATE field from Gate, not a second implementation of ContractGate, because the two ask
	// different questions of different things: the contract gate asks whether the candidate's GRAPH is
	// coherent, and this one asks whether the candidate's context loss is more than this NODE's job can
	// take. Folding them together would make one refusal reason answer for both.
	DropTolerance DropGate
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
				// 🔴 The drop gate runs FIRST, before the contract gate and long before anything is
				// compiled, transformed, or run (P16 task 5.3 / FR8). Its whole value is being early: a
				// policy that would drop more than the node's job tolerates is not a candidate to MEASURE,
				// it is a candidate to reject, and rejecting it after the multi-seed spend would prove the
				// same thing for the price of an eval run.
				if ok, reason := e.DropTolerance.Admit(c, e.Menu, t.Pattern); !ok {
					em.Refusals = append(em.Refusals, Refusal{Operator: c.Operator, NodeID: c.NodeID,
						Reason: reason})
					continue
				}
				if e.Gate != nil {
					gated, ok, reason := e.Gate.Admit(c)
					if !ok {
						em.Refusals = append(em.Refusals, Refusal{Operator: c.Operator, NodeID: c.NodeID,
							Reason: reason})
						continue
					}
					// The spec that leaves the gate is the one that gets compiled: on an `adapted` verdict it
					// carries the inserted adapters, so the diff a reviewer reads contains the bridge the
					// gate admitted (P15 task 3.2 / decisions.md D-2).
					c = gated
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
		BaseVariantID:   e.BaseVariantID,
		Menu:            e.Menu,
		Bottleneck:      t.Bottleneck,
		PromptOptimizer: e.Optimizer,
		IR:              e.IR,
		BasePromptBody:  t.BasePromptBody,
		Groundings:      t.Groundings,
		RequiredFields:  t.RequiredFields,
		Usage:           t.Usage,
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
