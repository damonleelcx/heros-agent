package proposal

import (
	"fmt"

	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// The DROP-TOLERANCE admissibility gate (P16 task 5.3, design.md Decision 3, FR8/FR13)
// ────────────────────────────────────────────────────────────────────────────────────
//
// A proposal whose resolved context policy would drive a node's drop ratio past what that node's job
// tolerates is INADMISSIBLE — refused here, at emission, before any transform is generated and before
// any eval budget is spent.
//
// # Why a gate and not "let scoring find out"
//
// Scoring *would* eventually punish a `summarization` variant that dropped the answer: the multi-seed
// run would show a `task_success` regression. That is the alternative design.md Decision 3 rejects, on
// **L4 运维 + L8 实现** — it catches the problem only after spending the eval budget to prove an
// obviously-lossy change bad. The gate reaches the same verdict earlier and cheaper, and it costs no
// correctness, because `DropRatio` is a number the policy already computes at assembly.
//
// # Why a MEASUREMENT beats an ESTIMATE, and why both exist
//
// `Observed` carries what a policy actually dropped at a node in a previous run — the number
// `context_drop_ratio` published (internal/contextassembly). When it is present, it decides: a
// measurement of this node under this policy is strictly better evidence than a table.
//
// `ContextChoice.ExpectedDrop` is the pre-verification estimate for a policy nothing has run yet, the
// same kind of platform metadata `ModelChoice.CostPerRun` already is. It is not pretending to be a
// measurement — it is what lets the gate say something about a candidate on its first appearance
// instead of admitting everything unmeasured.
//
// 🔴 An unmeasured, unestimated policy is ADMITTED, deliberately. The gate refuses on evidence, never
// on ignorance: rejecting a candidate because nobody has measured it yet would make "we have no data"
// mean "no", which quietly freezes the board on whatever was measured first. Verification decides what
// the gate has no evidence about — that is the platform's whole shape.

// DropKey identifies one measured observation: what a policy dropped at a node.
type DropKey struct {
	NodeID string
	Policy string
}

// DropGate is the drop-tolerance admissibility predicate. Its zero value is a working gate that judges
// on estimates alone; a caller with eval telemetry supplies Observed and the gate judges on measurements
// where it has them.
type DropGate struct {
	// Observed is the measured drop ratio per (node, policy), from `context_drop_ratio`. A policy present
	// here is judged on this number, not on the menu's estimate.
	Observed map[DropKey]float64
}

// DefaultDropTolerance is the pattern-derived default for a node that declares none (PRD §14 Q1).
//
// Whether a drop is acceptable is a property of the node's JOB, which is exactly what the pattern label
// names — so the default is per-pattern rather than one global number. The values are deliberately
// coarse and the reasoning, not the digits, is the contract:
//
//   - a Retrieval (RAG) node ASSEMBLES from a corpus and routinely reshapes what it carries, so a
//     larger drop is normal there and a tight default would reject the axis's own best operator;
//   - a Memory-management node exists to compact, so compaction is its job, not its failure;
//   - a Reflection / Planning / Reasoning node reasons OVER the conversation, and a drop takes away the
//     material it reasons about — these are the nodes where a lossy policy quietly removes the answer;
//   - everything else sits in the middle.
//
// 🚫 The default is NOT frozen into `config_hash`. It is read here, at gate time, precisely so that
// refining these numbers later does not move the hash of every spec that declared nothing
// (decisions.md D-2). An author who needs a specific tolerance writes it on the node, and the explicit
// value always wins.
func DefaultDropTolerance(p patternclassifier.Pattern) float64 {
	switch p {
	case patternclassifier.RetrievalRAG:
		return 0.60
	case patternclassifier.MemoryManagement:
		return 0.75
	case patternclassifier.Reflection, patternclassifier.Planning,
		patternclassifier.ReasoningTechniques, patternclassifier.GuardrailsSafety:
		return 0.20
	default:
		return 0.40
	}
}

// ToleranceFor returns the tolerance the gate judges a node by, and whether it was authored.
//
// An authored tolerance always wins over the pattern default — including an authored 0, which means
// "this node tolerates no drop at all" and is a legitimate, deliberate thing to say. Treating a
// declared 0 as "unset" would silently replace the strictest possible statement with a permissive
// default, which is the pointer field's whole reason for existing.
func ToleranceFor(o variantspec.NodeOverride, p patternclassifier.Pattern) (tolerance float64, authored bool) {
	if o.ContextDropTolerance != nil {
		return *o.ContextDropTolerance, true
	}
	return DefaultDropTolerance(p), false
}

// Admit reports whether a candidate clears the node's drop tolerance.
//
// It judges only candidates that CHANGE the context policy. A model swap, a prompt rewrite, or a tool
// prune does not touch what the node assembles, so gating them on a context number would refuse
// changes for a property they cannot affect.
func (g DropGate) Admit(c Candidate, menu Menu, pattern patternclassifier.Pattern) (bool, string) {
	if c.Spec == nil || c.NodeID == "" {
		return true, ""
	}
	o := c.Spec.Nodes[c.NodeID]
	if o.ContextPolicy == "" {
		return true, "" // not a context change: nothing about assembly moved
	}
	choice, ok := menu.contextByRef(o.ContextPolicy)
	if !ok {
		// The candidate pins a context entry the menu does not describe. The gate has no evidence about
		// it and says so by admitting: verification decides what the gate cannot.
		return true, ""
	}

	drop, source, known := g.dropFor(c.NodeID, choice)
	if !known {
		return true, "" // no measurement, no estimate — admitted on the no-refusing-on-ignorance rule
	}
	tolerance, authored := ToleranceFor(o, pattern)
	if drop <= tolerance {
		return true, ""
	}

	origin := "the node's declared tolerance"
	if !authored {
		origin = fmt.Sprintf("the default tolerance for a %s node", pattern)
	}
	return false, fmt.Sprintf(
		"context policy %q would drop %.0f%% of this node's context (%s), past %s of %.0f%%; the proposal is "+
			"inadmissible and is rejected before any transform is generated and before any eval spend",
		choice.Policy, drop*100, source, origin, tolerance*100)
}

// dropFor returns the drop this candidate's policy would drive at this node, where the number came
// from, and whether there is one at all.
func (g DropGate) dropFor(nodeID string, choice ContextChoice) (drop float64, source string, known bool) {
	if v, ok := g.Observed[DropKey{NodeID: nodeID, Policy: choice.Policy}]; ok {
		return v, "measured on a previous run", true
	}
	if choice.Lossy {
		return choice.ExpectedDrop, "estimated for this policy", true
	}
	// A LOSSLESS policy's 0.0 is a property of the policy, not an observation — and it can never exceed
	// a tolerance, so it needs no estimate. Reporting it as "known 0" would be the measured-vs-unmeasured
	// conflation this codebase keeps separate everywhere else.
	return 0, "", false
}

// contextByRef returns the menu's description of a context entry by ref.
func (m Menu) contextByRef(ref string) (ContextChoice, bool) {
	for _, c := range m.ContextPolicies {
		if c.Ref == ref {
			return c, true
		}
	}
	return ContextChoice{}, false
}
