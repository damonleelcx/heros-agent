// Package proposal is the P5.5 proposal engine. It turns a P4.5 diagnosis (a read-only node +
// typed-cause + failing-cases record) into a CONCRETE, executable candidate: a candidate Variant Spec
// (the canonical desired-state config, content-hashed like any other) AND the reviewable source diff a
// deterministic AST-level codemod produces at the discovered call site(s) (ADR-001).
//
// The engine is deliberately three separable steps, mirroring the P5 pipeline the codebase already
// ships:
//
//  1. an OPERATOR emits candidate Variant Specs from a diagnosis — pure, pattern-gated (this file +
//     catalog.go);
//  2. a COMPILER resolves each candidate to a config_hash, runs the codemod (internal/transform) to a
//     byte-deterministic source diff, and gates it on the build (internal/worktree, or an injectable
//     BuildChecker) — compile.go;
//  3. a GATE refuses any candidate that violates the P5 typed I/O contract or is inadmissible for the
//     node's pattern label — gate.go.
//
// Nothing here surfaces a recommendation. Ranking (rank.go) and the verification gate
// (internal/verification) stand between a compiled candidate and the user; a candidate whose diff
// fails to build never reaches either.
package proposal

import (
	"github.com/heros-foreal/agentd/internal/attribution"
	"github.com/heros-foreal/agentd/internal/diagnosis"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// OperatorKind names a catalog operator. It is the wire value stored on a proposal row, so it is a
// stable string, not an ordinal.
type OperatorKind string

const (
	OpModelUpgrade     OperatorKind = "model_upgrade"            // reasoning-heavy node on a weak model → stronger model
	OpEnableThinking   OperatorKind = "enable_extended_thinking" // same, via a thinking-budget model
	OpModelDowngrade   OperatorKind = "model_downgrade"          // cheap task on an expensive model → cheaper model
	OpPromptRewrite    OperatorKind = "prompt_rewrite"           // prompt/output-contract violation → grounded rewrite + schema
	OpContextPolicy    OperatorKind = "context_policy_switch"    // context overflow / lost-in-middle → summarization / sliding window
	OpReorder          OperatorKind = "reorder"                  // lost-in-middle → reorder the nodes
	OpRAGTune          OperatorKind = "rag_tune"                 // RAG relevance low → tune top-k / swap retriever/embedding
	OpAddRerank        OperatorKind = "add_rerank"               // RAG relevance low → add a rerank skill
	OpAddSkill         OperatorKind = "add_skill"                // missing/erroring tool → add a skill from the registry
	OpFixSchemaBinding OperatorKind = "fix_schema_binding"       // erroring tool → correct the schema binding
	OpPrune            OperatorKind = "prune"                    // redundant node → remove it
	OpMerge            OperatorKind = "merge"                    // redundant node → merge it into a neighbour

	// ── P13 deeper prompt operators (13a) — each a distinct grounded catalog row, all publishing a new
	// immutable prompt version through the P10 registry. They are OPERATORS, not new taxonomy codes or
	// dimensions: they handle the existing CausePromptFormatDrift code and land only in PromptRef.
	OpInstructionHarden OperatorKind = "instruction_harden" // under-specified prompt → explicit constraints
	OpFewShotCurate     OperatorKind = "few_shot_curate"    // dead/duplicate exemplars → curated few-shot
	OpPromptCompress    OperatorKind = "prompt_compress"    // token bloat → compressed prompt (quality-gated)
	OpRedundancyRemove  OperatorKind = "redundancy_remove"  // redundant instructions → de-duplicated prompt

	// ── P13 model-parameter tuning (13b) — temperature/max-tokens via ProviderParams, bound-mode only.
	OpParamTune OperatorKind = "param_tune" // tune provider params (bound apply mode)
)

// Signal is a structural driver that is NOT expressible as a P4.5 taxonomy code but still maps to a
// catalog operator (design Decision 2's table has two such rows). "Cheap task on expensive model"
// comes from a cost bottleneck flag; "redundant node" from a near-zero-contribution node. Keeping
// these as an explicit input keeps the frozen diagnosis taxonomy intact rather than inventing codes
// for it.
type Signal string

const (
	SignalNone           Signal = ""
	SignalCostBottleneck Signal = "cost_bottleneck" // → model-downgrade
	SignalRedundantNode  Signal = "redundant_node"  // → prune / merge
)

// OperatorInput is what every operator reads. It is assembled by the engine from a diagnosis (or a
// structural Signal), the node's pattern label, the current baseline spec, and the registry Menu of
// candidate refs the operator may select from.
//
// It is pure data: an operator that only sets a dimension override needs neither the IR nor the
// registries themselves, which is what keeps operator emission unit-testable without a checked-out
// tree. Contract validation (which does need the IR) is a separate gate.
type OperatorInput struct {
	// Diagnosis is the originating P4.5 record. For a Signal-driven operator (downgrade / prune) its
	// TaxonomyCode may be empty; NodeID and EvidenceCaseIDs still carry.
	Diagnosis diagnosis.Diagnosis
	// Signal is the structural driver, when the operator is not diagnosis-taxonomy-driven.
	Signal Signal
	// Pattern is the node's P3.5/P5 pattern label, resolved by the engine via evalharness.NodePattern.
	// It is the dispatcher for admissibility (design Decision 2).
	Pattern patternclassifier.Pattern
	// Base is the current Variant Spec the candidate is derived from (the baseline). Never mutated.
	Base *variantspec.VariantSpec
	// Menu is the set of registry refs the operator may select from (models, skills, context policies).
	Menu Menu
	// Bottleneck is the optional cost/latency attribution flag backing a Signal-driven operator.
	Bottleneck *attribution.BottleneckFlag
	// PromptOptimizer produces a grounded prompt edit for the prompt-rewrite operator. Nil for the
	// other operators.
	PromptOptimizer PromptOptimizer
	// IR is carried for the operators that need to reason about wiring (reorder / prune / merge). It is
	// read-only.
	IR *discovery.IR
	// BasePromptBody is the rendered current prompt at the node, for the prompt-rewrite optimizer.
	BasePromptBody string
	// Groundings are the failing-case groundings the prompt-rewrite optimizer grounds on (§2).
	Groundings []FailingCaseGrounding
	// RequiredFields is the output contract the prompt failures violated, pinned by the rewrite's
	// format constraint. Empty when no schema was violated.
	RequiredFields []string
}

// NodeID returns the node the input targets. For a Signal-driven operator the diagnosis may be a thin
// record, but its NodeID field is always populated by the engine.
func (in OperatorInput) NodeID() string { return in.Diagnosis.NodeID }

// Candidate is one emitted candidate Variant Spec, before compilation. The engine attaches the
// config_hash, source diff, and build status in a second step (Compiled).
type Candidate struct {
	Operator OperatorKind
	// DiagID ties the candidate back to the originating diagnosis (may be empty for a Signal-driven op).
	DiagID string
	// NodeID is the node the candidate changes (empty for a whole-graph reorder).
	NodeID string
	// Pattern is the node's pattern label, carried for the gate and the UI.
	Pattern patternclassifier.Pattern
	// Dimensions names which node dimension(s) this candidate changes ("model", "prompt", …). It is the
	// human-legible summary of the Variant-Spec diff.
	Dimensions []string
	// Spec is the derived candidate Variant Spec (baseline + this one change). Never shares backing
	// storage with Base.
	Spec *variantspec.VariantSpec
	// Rationale is a one-line, evidence-anchored reason ("misroute on 3 cases → stronger router model").
	Rationale string
	// EvidenceCaseIDs are the specific failing cases attached as evidence (from the diagnosis).
	EvidenceCaseIDs []string
	// Grounding is the prompt-optimizer grounding bundle, present only for a prompt-rewrite candidate.
	// Its presence is what makes a rewrite traceable to its generating cases (§2).
	Grounding *GroundingBundle
	// NewPromptBody is the rewritten prompt a prompt-rewrite candidate proposes. The compiler registers
	// it into the prompt registry to mint the version_id the candidate's NodeOverride.PromptRef names.
	// Empty for every non-prompt operator.
	NewPromptBody string
	// ExpectedGain is a cheap pre-verification estimate (operator-prior × severity), used only to order
	// verification (design Q2). The measured verdict replaces it post-verification.
	ExpectedGain float64
	// GuardrailRequired marks a candidate whose admissibility depends on the held-out downgrade guardrail
	// (P13 task 3.4): a cheaper model is admissible ONLY when its task-success CI overlaps the incumbent's
	// on held-out cases. Verification consults RequiresHeldOutGuardrail / EvaluateDowngradeGuardrail for
	// such a candidate before admitting it. In-memory only — never hashed (design Decision 8).
	GuardrailRequired bool
}

// Operator is one catalog change-operator. It declares the diagnosis taxonomy codes and structural
// signals it handles, the pattern labels it is admissible for, and how it derives candidate specs.
//
// Registration in the catalog is a dispatch table, not an if/else ladder: adding an operator is adding
// a row, never editing a switch (core rule 6 — no hardcoded enum switch).
type Operator interface {
	Kind() OperatorKind
	// Handles is the set of P4.5 taxonomy codes this operator fires on. Empty for a Signal-driven op.
	Handles() []diagnosis.TaxonomyCode
	// HandlesSignal is the structural signal this operator fires on, or SignalNone.
	HandlesSignal() Signal
	// AdmissiblePatterns is the closed set of pattern labels this operator may fire on. Empty means
	// pattern-agnostic (e.g. prompt-rewrite applies to any prompting node).
	AdmissiblePatterns() []patternclassifier.Pattern
	// Propose derives zero or more candidate specs from the input. It MUST NOT mutate in.Base. It
	// returns no candidate (not an error) when it has nothing admissible to offer — e.g. the menu holds
	// no stronger model than the node already runs.
	Propose(in OperatorInput) ([]Candidate, error)
}

// admissibleForPattern reports whether op may fire on p. An empty AdmissiblePatterns() set is
// pattern-agnostic and admits every pattern.
func admissibleForPattern(op Operator, p patternclassifier.Pattern) bool {
	pats := op.AdmissiblePatterns()
	if len(pats) == 0 {
		return true
	}
	for _, a := range pats {
		if a == p {
			return true
		}
	}
	return false
}
