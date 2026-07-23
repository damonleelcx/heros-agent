// Package diagnosis is the P4.5 diagnosis engine: rules-first deterministic detectors over traces
// that emit a TYPED cause from a fixed failure taxonomy, then an LLM-as-analyst constrained to that
// same taxonomy for the fuzzy residue the rules did not explain.
//
// Three laws govern it, all inherited from P4's judge discipline and stated plainly in the design:
//   - Rules first. A deterministic detector is fast, free, and reproducible; the analyst is slow,
//     metered, and variable. Most diagnoses therefore cost nothing and are the same on every run.
//   - The analyst is constrained + calibrated. Its output is limited to the fixed taxonomy + a
//     confidence, an off-taxonomy label is rejected not recorded, and its agreement against a
//     human-labeled subset is reported alongside every diagnosis it emits.
//   - Read-only. The engine emits reports, never a proposal and never a mutation. There is no apply
//     path here; that constraint binds P5.5.
package diagnosis

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

// TaxonomyVersion is the versioned identity of the fixed failure taxonomy (design Q5/Q7). It is the
// contract P5.5's change operators consume — a code means the same failure, and admits the same
// patterns, for everyone reading this version. Bumping it is the only signal that the taxonomy moved.
const TaxonomyVersion = "heros.p45.taxonomy.v1"

// TaxonomyCode is one slot of the CLOSED failure taxonomy. The vocabulary is closed for the same
// reason the edge-case taxonomy is: a hundred failing cases must resolve into a handful of named
// causes that aggregate and that each map deterministically to a P5.5 operator — not a hundred
// free-text essays. The analyst is constrained to exactly these codes; an off-taxonomy label is
// rejected.
type TaxonomyCode string

const (
	// Broad codes — a failure mode any context-consuming node can exhibit.
	CauseContextOverflow    TaxonomyCode = "context_overflow"          // context truncated before a failing node
	CausePromptFormatDrift  TaxonomyCode = "prompt_format_drift"       // output contract ignored → downstream parse fail
	CauseLostInMiddle       TaxonomyCode = "lost_in_middle"            // over-long context degrading a later node
	CauseModelCapabilityGap TaxonomyCode = "model_capability_mismatch" // cheap model on a reasoning-heavy node

	// Capability-scoped codes.
	CauseToolSchemaMismatch TaxonomyCode = "tool_schema_mismatch" // Tool Use: schema mismatch / repeated tool errors
	CauseRetrievalMiss      TaxonomyCode = "retrieval_miss"       // RAG: low-relevance / empty chunks

	// Control-flow-scoped codes (pattern-scoped failure modes, design/Decision 5).
	CauseMisroute             TaxonomyCode = "misroute"                // Routing: wrong branch chosen
	CauseInfeasiblePlan       TaxonomyCode = "infeasible_plan"         // Planning: an unexecutable plan
	CauseCircularPlan         TaxonomyCode = "circular_plan"           // Planning: a non-terminating plan
	CauseNonConvergence       TaxonomyCode = "non_convergence"         // Reflection: revisions never converge
	CauseDegradationOnRevisit TaxonomyCode = "degradation_on_revision" // Reflection: a revision makes it worse
)

// causeMeta records, per code, a human description and the P3.5 patterns the failure mode is
// ADMISSIBLE on (design Q5: one taxonomy tagged with admissible patterns per code). An empty
// admissible set means the mode is pattern-agnostic — any node can exhibit it (e.g. a context
// overflow is not specific to one pattern). A non-empty set is the dispatcher §7 uses to REFUSE
// diagnosing a node with a mode its pattern cannot exhibit.
type causeMeta struct {
	Description string
	Admissible  []patternclassifier.Pattern
}

var taxonomy = map[TaxonomyCode]causeMeta{
	CauseContextOverflow:    {"context truncated before the failing node", nil},
	CausePromptFormatDrift:  {"a node ignored its output contract, causing a downstream parse failure", nil},
	CauseLostInMiddle:       {"an over-long context degraded a later node", nil},
	CauseModelCapabilityGap: {"a cheap model ran on a reasoning-heavy node", nil},

	CauseToolSchemaMismatch: {"a tool schema mismatch or repeated tool errors", []patternclassifier.Pattern{patternclassifier.ToolUse}},
	CauseRetrievalMiss:      {"retrieval returned low-relevance or empty chunks", []patternclassifier.Pattern{patternclassifier.RetrievalRAG}},

	CauseMisroute:             {"the router chose the wrong branch", []patternclassifier.Pattern{patternclassifier.Routing}},
	CauseInfeasiblePlan:       {"the plan was infeasible", []patternclassifier.Pattern{patternclassifier.Planning}},
	CauseCircularPlan:         {"the plan was circular / non-terminating", []patternclassifier.Pattern{patternclassifier.Planning}},
	CauseNonConvergence:       {"reflection revisions never converged", []patternclassifier.Pattern{patternclassifier.Reflection}},
	CauseDegradationOnRevisit: {"a reflection revision degraded the output", []patternclassifier.Pattern{patternclassifier.Reflection}},
}

// ValidCode reports whether a code is in the fixed taxonomy. An analyst response carrying an unknown
// code is rejected on this predicate — it is the boundary that keeps off-taxonomy labels from being
// recorded (task 6.2).
func ValidCode(c TaxonomyCode) bool {
	_, ok := taxonomy[c]
	return ok
}

// Description returns a code's human description, "" for an unknown code.
func Description(c TaxonomyCode) string { return taxonomy[c].Description }

// AdmissibleOn reports whether a code's failure mode can be exhibited by a node with the given P3.5
// pattern (Decision 5 / §7). A pattern-agnostic code (empty admissible set) is admissible everywhere;
// a pattern-scoped code is admissible only on its listed patterns. An empty pattern (unlabeled node)
// admits only the pattern-agnostic codes — refusing to guess a scoped mode on an unlabeled node.
func AdmissibleOn(c TaxonomyCode, pattern patternclassifier.Pattern) bool {
	meta, ok := taxonomy[c]
	if !ok {
		return false
	}
	if len(meta.Admissible) == 0 {
		return true // pattern-agnostic
	}
	for _, p := range meta.Admissible {
		if p == pattern {
			return true
		}
	}
	return false
}

// Codes returns the taxonomy codes in a stable, sorted order — for rendering the taxonomy and for
// tests that assert the closed set.
func Codes() []TaxonomyCode {
	out := make([]TaxonomyCode, 0, len(taxonomy))
	for c := range taxonomy {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
