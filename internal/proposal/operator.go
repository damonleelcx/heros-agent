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

	// ── P14 skills & tools (14a/14b) ────────────────────────────────────────────────────────────────
	//
	// OpRemoveSkill is its OWN kind rather than a generalization of OpPrune (PRD §14 Q2, settled here).
	// The two look alike only from far away: OpPrune removes a NODE and rewires its neighbours — a graph
	// edit whose admissibility question is "does the wiring still type-check" — while a skill removal is
	// a per-dimension delta on one node whose admissibility question is "was this skill ever exercised".
	// They fire on different drivers (a redundant-node signal vs a tool diagnosis), carry different
	// rationales, and want different verification order. Folding them together would put an `if` inside
	// pruneOp deciding which of two contracts it is honouring, which is the enum-switch this catalog is
	// built to avoid (core rule 6).
	OpRemoveSkill OperatorKind = "remove_skill" // erroring / unexercised skill → unbind it
	// OpToolPrune drops provider-native tools the eval set never exercises — a call-site DELETION of an
	// already-present tool, not a construction. Distinct from OpRemoveSkill for the reason D-14.1 splits
	// tools from skills in the IR: a tool is selected, a skill is bound, and one sentence cannot mean both.
	OpToolPrune OperatorKind = "tool_prune"
	// OpToolMinimize proposes the MINIMAL tool set that preserves task success — the whole-set analogue
	// of a single prune, emitted as one candidate so the harness scores the set rather than each drop.
	OpToolMinimize OperatorKind = "tool_minimize"

	// ── P17 memory (20b) ────────────────────────────────────────────────────────────────────────────
	//
	// OpMemoryPolicy proposes swapping what a node carries ACROSS invocations — a different memory
	// strategy, or the same strategy retuned. It is its own kind and not a mode of OpContextPolicy for
	// the reason decisions.md D2 keeps the two dimensions apart: context assembly is how a SINGLE call
	// builds its message list, memory is what survives between calls, and one operator meaning both would
	// put an `if` inside contextPolicyOp deciding which contract it was honouring.
	//
	// 🚫 It is DORMANT at M20, and that is a contract rather than a stage of implementation. The
	// transform refuses every memory rewrite (decisions.md D4), so a memory proposal resolves and hashes
	// but can never be verified — and what cannot be verified cannot be a win. It is catalogued anyway,
	// exactly as the reserved OpMerge was, because a proposal a user can SEE and reason about is worth
	// more than silence, and because the day the rewriter lands the operator wakes with no change to its
	// contract: its worth was always going to be decided by the harness.
	OpMemoryPolicy OperatorKind = "memory_policy_switch"
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
	// SignalUnusedTools is P14's structural driver: the node offers the model tools the eval set never
	// calls. It is a Signal rather than a taxonomy code for the reason the other two are — an unused
	// tool is not a FAILURE, so the frozen P4.5 failure taxonomy has no honest code for it, and inventing
	// one would put "nothing went wrong, but this costs tokens" in a vocabulary about what went wrong.
	SignalUnusedTools Signal = "unused_tools" // → tool-prune / tool-minimize

	// ── P17 memory bottlenecks ──────────────────────────────────────────────────────────────────────
	//
	// These two are the classifier's OWN failure modes for the MemoryManagement pattern
	// (internal/patternclassifier/metricset.go: FailureModes {"contradictory_memory", "stale_read"}),
	// lifted verbatim rather than renamed. Reusing the exact spellings is the point: the vocabulary that
	// DETECTS a memory problem and the vocabulary that DRIVES a memory proposal are then the same
	// vocabulary, so nothing has to translate between them and no translation can drift.
	//
	// They are Signals rather than taxonomy codes for the reason the three above are: the frozen P4.5
	// taxonomy is about what WENT WRONG in a case, and "the agent recalled something stale" is a property
	// of the memory strategy across turns rather than of any one failing case. Inventing a code for it
	// would put a cross-turn observation into a per-case vocabulary.
	SignalStaleMemory         Signal = "stale_read"           // → memory-policy switch
	SignalContradictoryMemory Signal = "contradictory_memory" // → memory-policy switch
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
	// BaseVariantID is the baseline's own config_hash, when the caller resolved it. The wiring
	// operators (reorder / prune / merge) record it as the candidate's ParentVariantID, which is what
	// "this candidate was derived from that configuration" means — the compare view and rollback both
	// walk that pointer (P15 design Decision 2).
	//
	// It is OPTIONAL and in-memory only: an operator test that never resolves a baseline has no hash to
	// supply, and a candidate then falls back to the baseline's own recorded lineage rather than
	// claiming a parent that does not exist. 🚫 Never hashed — lineage is a property of how a spec was
	// authored, not of the configuration it denotes.
	BaseVariantID string
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
	// Usage is the P14 selection evidence: which of the node's tools/skills the eval set actually
	// exercised, and which of them errored. The skill-removal and tool-pruning operators are GROUNDED on
	// it and emit nothing without it — unbinding a capability on no evidence is exactly the plausible-
	// but-unfounded change the catalog declines elsewhere (the prompt operators' grounded-or-silent rule).
	Usage ToolUsage
}

// ToolUsage is the evidence the P14 selection operators are grounded on.
//
// It is deliberately about NAMES the call site declares, not registry refs: a discovered tool has no
// registry identity at all (decisions.md D-14.2 — "a tool is selected against the discovered set, not
// resolved from a ref"), and a bound skill's name is what the trace records when it is called. The
// operators map a spec's skill_ref back to a name through the Menu, which is the one place refs and
// names are already joined.
type ToolUsage struct {
	// Discovered is the node's discovered tool set, in IR order. Empty means "not recorded", which is
	// not the same as "the node offers no tools" — an operator treats it as no evidence and declines.
	Discovered []string
	// Exercised is the subset the eval set actually called.
	Exercised []string
	// Erroring names the tools/skills whose calls failed during the eval.
	Erroring []string
}

// Recorded reports whether any usage evidence exists. A pass with no recorded usage grounds nothing,
// and the selection operators emit nothing rather than guess which capability is dead.
func (u ToolUsage) Recorded() bool {
	return len(u.Discovered) > 0 || len(u.Exercised) > 0 || len(u.Erroring) > 0
}

func (u ToolUsage) exercised(name string) bool { return containsString(u.Exercised, name) }
func (u ToolUsage) erroring(name string) bool  { return containsString(u.Erroring, name) }

func containsString(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
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

	// ── P13 13c: the second ORIGIN ────────────────────────────────────────────────────────────────
	//
	// Origin is who initiated this change. The zero value reads as OriginOperator, so every catalog
	// operator keeps constructing candidates exactly as it did. 🚫 Never hashed (origin.go).
	Origin Origin
	// Actor is the acting identity for a user-originated candidate. Zero for an operator's.
	Actor Actor
	// ForkedFromProposal is set when a person edited an operator's proposal and submitted the result.
	// Both lineages stay visible — but the outcome belongs to the person, and the originating operator
	// is NOT credited with it, or an operator's win rate degrades into a measure of how often humans
	// fix it. In-memory only, never hashed.
	ForkedFromProposal string
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
