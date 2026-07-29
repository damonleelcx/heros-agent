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
- **New capability `skill-tool-authoring` (wave 14c).** The axis gains a **second origin**: a user binds,
  unbinds and **reorders** a node's skills, and prunes or restores its tools, directly — instead of waiting
  for `OpAddSkill` / `OpAddRerank` / `OpToolPrune` to fire. It consumes the shared
  [`authored-change`](../p13-prompt-model-optimization/specs/authored-change/spec.md) contract unchanged
  (one spine two origins; `Origin` recorded never hashed; **origin-blind refusals with no override**;
  preflight; `unverified` never a claim and never auto-merged; named conflicts; byte-exact reversal;
  append-only audit; offline CLI parity; no new egress; **the user does not author the evidence**), and
  adds exactly two axis-specific rules. **Selection is fail-closed**: a skill must be a **registry-sealed**
  entry with a **pinned version** and a tool must be a member of the node's **discovered** tool set —
  neither is free text, for the same reason `env` validates against `DeclaredEnv` and `expr` against
  `in_scope`. **The language boundary is stated at preflight**: on a node whose language has no landed
  materializer, skills are **not offered at all**, and a binding submitted through any surface is refused
  with the *same* typed cause the transform raises — read from the same materializer-coverage table, so
  preflight and transform cannot disagree. Authored skill arguments are validated against the **pinned**
  version's compiled input contract, a tool selection over a **dynamically-assembled** set refuses rather
  than guesses, and because skill order is identity-bearing an authored **reorder** is a real, scoreable
  `config_hash` change — never presented as cosmetic.
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

- **New capability `skill-tool-language-coverage` (14d).** The refusal 14a specifies as *interim* is made
  finite. Discovery finds these call sites in all seven registered languages and the IR records them;
  what is missing is stated per cell and then closed. **Binding** is blocked on a **spelling** —
  `toolValueForms` ([`skillbind.go:94`](../../../internal/transform/skillbind.go)) carries `anthropic` and
  `openai` in Go and nothing else — while the *shape* already comes from the sealed schema and is
  language-independent, so the coverage unit is **(language, provider, SDK generation)** and a new language
  is a set of rows, never a second source of truth about what a bound skill means. **Pruning** is blocked
  somewhere else entirely: `spanRewriteTools`
  ([`rewritetools.go:169`](../../../internal/transform/rewritetools.go)) refuses not because deletion is
  hard at the syntactic floor but because *the frontends record no tool split*, so there is nothing to
  prune against — a **frontend** gap wearing a rewriter's clothing. So 14d publishes **two total tables**
  (a language routinely prunes before it binds), teaches **every** frontend to record each tool's
  identifier and declaration location, reaches builder- and request-field-bound SDKs through P13's
  binding-site generalization, and 🚫 explicitly refuses the shortcut of letting a span pruner *infer*
  which unnamed element is which tool — a prune that deletes the wrong element produces a diff that
  parses. It also fixes the refusal this axis gets wrong most often: a call site with unpacked arguments
  or a run-time-assembled tool list is told **that**, and told a materializer would not change it, rather
  than being told to wait for its language. The cross-axis rules come from **P13's `language-coverage`**
  and are referenced, never restated.

## Impact

- **Affected capabilities:** `skill-binding` (new), `tool-selection` (new), `skill-tool-authoring` (new,
  14c), `skill-tool-language-coverage` (new, 14d). Consumed, not modified: `discovery-engine` (P1), `transform`/codemod (P2), `registry`/skills (P3),
  `pattern-classifier` (P3.5), `eval-harness`/`scoring` (P4/P4.5), `proposal-catalog`/`verification`
  (P5.5), **`authored-change` (P13 — referenced, never restated)**, `entitlements` (P7), `web-console`
  (P9), `cli`/`ci` (P11), `forge-delivery` (P12).
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
  tool pruning/minimization) follows and depends on the additive IR change landing first. **14c**
  (`skill-tool-authoring`) follows 14b and depends on **P13's 14c-equivalent wave** — the shared
  `authored-change` contract — landing first; it is independently revertible, returning the axis to a
  single origin with no upstream change. **14d** (`skill-tool-language-coverage`) depends on **P13's 13d**
  — the shared `language-coverage` contract and the binding-site generalization — landing first, and is
  independent of 14c. Within 14d the two halves are independent of each other and land in different
  packages: the spelling rows in `internal/transform`, the tool split in `internal/discovery`. 14d is
  independently revertible, returning the axis to its 14a/14b cells with the refusals it already had.
