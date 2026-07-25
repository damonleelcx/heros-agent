## Why

The skill axis is the one the diagnosis catalog leans on hardest and the one axis the codemod could not
apply. It is **modeled end to end**: `DimSkills` is a closed-enum dimension
([`internal/variantspec/spec.go:45`](../../../internal/variantspec/spec.go)), `NodeOverride.SkillRefs`
carries the override ([`spec.go:186`](../../../internal/variantspec/spec.go)), the registry seals a
version-addressed skill contract (`KindSkill`, [`internal/registry/skill.go`](../../../internal/registry/skill.go)),
four operators generate skill variants (`OpAddSkill` / `OpAddRerank` / `OpFixSchemaBinding` + the
`ragTune` skill branch, [`internal/proposal/catalog.go`](../../../internal/proposal/catalog.go)), and the
resolved value participates in `config_hash` (`ResolvedNode.SkillRefs`,
[`resolved.go:55`](../../../internal/variantspec/resolved.go)). **The one thing it cannot do is apply.**
The call-site codemod *refuses*: [`internal/transform/rewrite.go:388`](../../../internal/transform/rewrite.go)
`refuseSkills` returns `ErrUnsafeRewrite`, and the tree-sitter engine shares that refusal
([`rewrite_span.go:62`](../../../internal/transform/rewrite_span.go)). A skill change resolves, hashes,
and scores — and produces no diff. Every `add_skill` / `add_rerank` candidate is proposed, ranked by its
prior ([`gain.go`](../../../internal/proposal/gain.go) — `OpAddSkill: 0.35`), and then cannot be realized.

The refusal is *correct as a default*: `refuseSkills` explains that binding a skill means constructing
SDK tool values *"whose shape differs per SDK and per SDK version … a subtly-wrong tool schema is the
kind of change that compiles and then degrades quality invisibly — the worst possible failure for an
eval platform."* It is *wrong as a permanent state*: the axis the catalog most relies on cannot close
the loop.

There is also a modeling defect that must be settled before tools can be optimized at all. The IR
**conflates tools and skills into one flat slice**,
[`internal/discovery/emit.go:98`](../../../internal/discovery/emit.go) `ToolsSkills []string`. A *tool*
(a provider-native function the model may call) is *selected* — kept or pruned — from what the model is
offered; a *skill* (a registered platform capability with a sealed contract) is *bound* by constructing
a value. The two have opposite apply mechanics, and a single list cannot express "prune this unused
tool" without the sentence also reading as "unbind this platform skill." The conflation is a one-way
door the moment a consumer depends on the shape.

## What Changes

- **New capability `skill-binding`.** Replace the refusal with **real call-site skill materialization**:
  for each **supported** language, `apply` constructs the SDK tool value for a bound skill from the
  skill's **sealed input/output schema** (`KindSkill`, `SkillEntry`), so the pinned version_id pins the
  shape. The **interim refusal remains a first-class, specified, testable behavior**: a node carrying a
  `SkillRef` whose language has no landed materializer **SHALL be refused at transform** with
  `ErrUnsafeRewrite`, naming the node and dimension, **never silently dropped** and never emitting a
  partial diff. Skill operators — **add, remove, rerank** — are **verification-gated**: each produces a
  candidate scored by the eval harness, and a materialized skill that regresses the score does not ship
  (*diagnosis proposes, verification decides*). A materialized skill's arguments are validated against
  its **compiled input contract** (`SkillEntry.ValidateInput`) before the node executes. Skill order is
  identity-bearing, so add/remove/**rerank** each change `config_hash`, while a no-skill node hashes
  byte-identically to pre-P14. Failures surface only through the `toolcontract` typed envelope's
  **allowlisted error codes**, so `tool_error_rate` stays well-defined for a bound node.
- **New capability `tool-selection`.** **Split tools from skills in the IR** as an **additive,
  append-only** change — new `Tools` / `Skills` fields, `omitempty` and nil-when-empty, following the
  `DeclaredEnv` pattern ([`emit.go:39-43`](../../../internal/discovery/emit.go)) — leaving pre-P14 IR
  bytes byte-identical and **retaining the frozen `ToolsSkills` slice, never repurposed**. The
  **discovery frontend populates the split** at extraction: a *tool* is a provider-native function/tool
  the model may call; a *skill* is a registered platform capability. A **new `DimTools` dimension**
  (with a `NodeOverride.ToolSelection` field, a `resolveNode` block, and an additive `ResolvedNode`
  field) makes tool selection first-class — **but no new registry `Kind`**: a tool is *selected* against
  the node's **discovered** tool set, validated **fail-closed** exactly as `env` is validated against
  `DeclaredEnv` and `expr` against `in_scope`. **Tool pruning** drops a tool a node offers but the eval
  set never exercises, expressed as a **call-site deletion** of an already-present tool (not a
  construction), cutting declared-tool tokens and tool-error surface; **tool-set minimization** emits the
  minimal set preserving `task_success`. A tool selection joins `config_hash` **additively**
  (nil-when-empty), so pruning changes it and a no-prune node hashes byte-identically to pre-P14. A tool
  selection over a **dynamically-assembled** tool set the frontend cannot locate is **refused** with
  `ErrUnsafeRewrite`, not guessed — the same refuse-until-safe discipline.
- **Eval is unchanged.** The harness consumes only `config_hash` + `Trace`, never a dimension label
  ([`internal/evalharness`](../../../internal/evalharness/)), so a materialized skill and a pruned tool
  set are **scored with zero eval change**. Tool pruning's win surfaces as fewer `eval_tokens_total` and
  lower `tool_error_rate` ([`metricnames.go:27-28`](../../../internal/evalharness/metricnames.go)). **No
  new metric is introduced.**
- **Not changed here.** No sandboxed execution of untrusted repository tool code (that is
  [P3](../p3-context-skills-sandbox/); P14 materializes **trusted/built-in** skills the registry seals).
  No cross-provider tool translation (a provider swap at a user call site stays refused — `rewrite.go:81`,
  ADR-002). No console skill-authoring UI (that is [P10](../p10-prompt-model-studio/)). No new registry
  `Kind` for tools. No new diagnosis codes for tool bloat, and no second cost/efficiency metric.

## Impact

- **Affected capabilities:** `skill-binding` (new), `tool-selection` (new). Consumed, not modified:
  `discovery-engine` (P1), `transform`/codemod (P2), `registry`/skills (P3), `pattern-classifier`
  (P3.5), `eval-harness`/`scoring` (P4/P4.5), `proposal-catalog`/`verification` (P5.5).
- **Affected code/systems:** `internal/transform` (replace `refuseSkills` with per-language
  materialization; add a tool rewriter that deletes/refuses); `internal/discovery/emit.go` +
  `extract.go` (additive split fields + the classifying frontend); `internal/variantspec`
  (`DimTools`, `NodeOverride.ToolSelection`, resolve block, additive `ResolvedNode` field);
  `internal/proposal` (an `OpToolPrune` row + priors; a remove-skill operator). No new store, no new
  registry `Kind`.
- **Dependencies:** requires **P1** (IR + frontend), **P2** (codemod + `ErrUnsafeRewrite` contract),
  **P3** (skills + `KindSkill`), **P3.5** (`ToolUse`/`RetrievalRAG` gating), **P4/P4.5** (axis-agnostic
  harness + diagnosis codes), **P5.5** (catalog + verification), **P10** (additive-hash `omitempty`
  pattern + fail-closed validation pattern).
- **Unblocks:** the optimizer's **skill axis produces diffs** (`add_skill`/`add_rerank`/`fix_schema_binding`/
  `rag_tune` stop ending at a proposal); **tool-level cost/latency optimization** (pruning, minimization);
  a cleaner substrate for future per-tool policy, addable over this split without a second migration.
- **Breaking:** **none.** The IR split is additive (`ToolsSkills` retained, pre-P14 bytes and
  `config_hash`es unchanged). `DimTools` grows a closed enum by one without touching the existing four.
  The interim refusal preserves today's behavior for every language until its materializer lands.
- **Sequencing:** **14a** (skill-binding materialization + the interim-refusal contract) is complete on
  its own and touches only `internal/transform` + the operator set. **14b** (the tools≠skills split +
  tool pruning/minimization) follows and depends on the additive IR change landing first.
