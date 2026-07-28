# Design — P14: Skills & Tools Optimization

Product rationale: [`../../../docs/prd/P14-skills-tools-optimization.md`](../../../docs/prd/P14-skills-tools-optimization.md).
One-way-door contracts recorded separately in [`decisions.md`](decisions.md).

## Context

The skill axis is fully built up to the codemod and stops there. Resolution
([`internal/variantspec/resolve.go`](../../../internal/variantspec/resolve.go) skill branch), hashing
(`ResolvedNode.SkillRefs`, [`resolved.go:55`](../../../internal/variantspec/resolved.go)), the sealed
registry contract (`KindSkill`, [`registry/skill.go`](../../../internal/registry/skill.go)), the operator
catalog ([`proposal/catalog.go`](../../../internal/proposal/catalog.go)) — all real. The transform is
where it ends: [`rewrite.go:388`](../../../internal/transform/rewrite.go) `refuseSkills` returns
`ErrUnsafeRewrite`, shared by the tree-sitter engine ([`rewrite_span.go:62`](../../../internal/transform/rewrite_span.go)).

Two facts shape everything below, and they pull in opposite directions:

- **Binding a skill is construction.** It means building an SDK tool value
  (`[]anthropic.ToolParam{{Name, InputSchema}}` and its per-SDK equivalents) from a JSON schema — code
  generation, whose failure mode is *"compiles and then degrades quality invisibly."* This is the hard
  part, and it lands *per language*.
- **Pruning a tool is deletion.** A pruned tool is an already-present element of a static tool array;
  removing it is a byte-safe span edit. This is the easy part, and it lands *now*.

The resolution is not to pretend they are the same maturity. `skill-binding` replaces the refusal where
a materializer exists and **keeps the refusal, as a specified behavior**, where one does not.
`tool-selection` splits the conflated slice so the deletion can be expressed at all, then prunes.

## Decision 1 — Materialize a skill from its sealed schema; verification decides whether it ships

For a supported language, the SDK tool value is **constructed from `SkillEntry`'s compiled input/output
schema** — the contract the skill's version_id already pins — and the resulting diff is **scored by the
eval harness**, never merged on the diagnosis alone.

**Alternative rejected — emit a best-effort tool value and let it ride into the merged change.** Fewer
moving parts, and it makes every language "work" immediately. Rejected on **L1 安全**: `refuseSkills`
already names the failure — a subtly-wrong tool schema *compiles and then degrades quality invisibly*,
the worst outcome for an eval platform. Construction without a verification gate trades L1 correctness
for L8 implementation convenience, which the eight-level law forbids. Deriving the shape from the sealed
schema (not from the value) means the pinned version pins the shape; gating on the score means a wrong
shape is caught by measurement, not by hope.

## Decision 2 — The interim refusal is a first-class, tested behavior, not a temporary gap

A node carrying a `SkillRef` whose language has **no landed materializer** refuses at transform with
`ErrUnsafeRewrite`, naming the node and the `skills` dimension. Nothing is dropped; no partial diff is
emitted for that node's skill dimension.

**Alternative rejected — when a language can't materialize, strip the `SkillRef` and emit the rest of
the diff.** It produces a diff for every spec and never "fails." That is exactly the danger: the node the
author asked to gain a skill ships **without** it, while its `config_hash` still claims the skill and the
eval scores a configuration that never existed. Rejected on **L1/L2**: a silent drop is a change that
looks applied and is not, and the only safe direction at a boundary carrying a behavior change is to fail
**loud**. This mirrors the existing contract — `ErrUnsafeRewrite` is *"the outcome FR5 specifies … the
spec anticipated exactly this case and asked for a loud refusal."* P14 narrows *when* the refusal fires
(per language, as materializers land), it does not weaken *that* it fires.

## Decision 3 — tools≠skills is an additive split; `ToolsSkills` is frozen and retained

The IR gains `Tools` and `Skills` fields, both `omitempty` and nil-when-empty, following the
`DeclaredEnv` pattern ([`emit.go:39-43`](../../../internal/discovery/emit.go)). The conflated
`ToolsSkills []string` ([`emit.go:98`](../../../internal/discovery/emit.go)) is **retained unchanged and
never repurposed**.

**Alternative rejected — rename/repurpose `ToolsSkills`, or fold tools into `DimSkills`.** One field is
tidier than three, and reusing `DimSkills` avoids growing the dimension enum. Rejected on **L5 不可演进
+ L2 稳定**:

| Approach | Failure mode |
|---|---|
| Repurpose `ToolsSkills` | It is part of the frozen IR bytes and keyed rows depend on it; a repurpose **breaks the golden vectors** and **orphans `config_hash`-keyed rows**. |
| Fold tools into `DimSkills` | One dimension carries two contracts; a tool prune **masquerades as a skill change** in the hash — a single-source-of-truth violation. |
| **Additive split (chosen)** | Pre-P14 bytes byte-identical; a consumer pinned below the new IR minor parses both; the two mechanics are separable. |

This is the expand-contract rule the registry and IR already live by: a new optional field must leave
old serializations byte-identical.

## Decision 4 — A new `DimTools` dimension, but no new registry `Kind`

Tool selection is a distinct dimension (`DimTools`) with its own `NodeOverride` field, resolve block, and
additive `ResolvedNode` field. A tool is **selected** against the node's discovered tool set — **not
resolved from a registered ref** — and a selection naming a tool the IR does not record is **rejected
(fail closed)**.

**Alternative rejected — give tools a registry `Kind` like skills.** It would make tools symmetric with
skills and reuse the seal/resolve machinery. Rejected on **L6 不可扩展 + single-source-of-truth**: a tool
is declared at the call site and offered to the model; it is already identified *by that call site*.
Sealing it into the registry invents a version-addressed identity for something that already has one, and
forces every discovered tool through a registration path it does not need. Validating a selection against
the discovered set is the pattern the codebase already proved twice — `env` against `DeclaredEnv`, `expr`
against `in_scope` (decisions.md D-1.2) — and it fails in the safe direction: a selection over a tool the
node does not offer is refused, not applied to nothing.

## Decision 5 — Tool selection joins `config_hash` by omission-when-empty

`ResolvedNode` gains a `tool_selection` field that is `omitempty` and left nil when the node prunes
nothing, so a no-prune node emits **no** `tool_selection` key and hashes byte-identically to a pre-P14
node.

**Alternative rejected — an always-present `[]`/`{}` like the sibling fields.** It is what
`ContextParams` and `ProviderParams` do. Rejected on **L2/L5**: those siblings' emptiness *predates* the
golden vectors and is part of the frozen bytes; a **new** field's *absence* is what must be
byte-compatible, and absence is achieved by omitting the key, not by an empty object. This is decisions.md
**D-1.4** (the `Bindings` field) applied a second time — the rule generalizes: a new optional
hash-participating field is `omitempty` + nil-when-empty, full stop.

## Decision 6 — No new metric; the axis-agnostic harness scores the prune

Tool pruning and minimization are scored by the **existing** harness, and their benefit surfaces as fewer
`eval_tokens_total` and lower `tool_error_rate`.

**Alternative rejected — a bespoke tool-efficiency score.** It would name the saving directly. Rejected
on **L5 不可演进 + L7 维护**: the harness already emits both metrics, and it consumes only
`config_hash` + `Trace`, never a dimension label — so a pruned tool set is scored with **zero eval
change**. A second metric is a second definition of a saving the platform can already see, and a second
place for it to be wrong. The consequence of the axis-agnostic design is precisely that a new axis needs
only to land its effect in `resolved_config` → `config_hash`; scoring is free.

## Decision 7 — Tool and skill changes respect the `toolcontract` typed envelope

A materialized skill or a pruned tool set surfaces tool-call failures only through the `toolcontract`
allowlisted `ErrorCode` set ([`internal/toolcontract/errors.go`](../../../internal/toolcontract/errors.go),
`ErrorCodeWhitelist`; response shapes in [`response.go`](../../../internal/toolcontract/response.go)).

**Alternative rejected — let a materialized skill or pruned tool raise raw provider errors.** Less
wrapping. Rejected on **L1 安全 + L2 稳定**: an out-of-whitelist error code makes `tool_error_rate`
ill-defined — the very metric that proves a prune helped — and a raw provider error string can leak into
a trace the platform renders. The typed envelope *is* the runtime error taxonomy the axis is measured
against; a change to what tools are offered must not change what an error can be.

## Decision 8 — Authoring on this axis is fail-closed selection, and its refusal moves to preflight

A user may bind / unbind / reorder a node's skills and prune / restore its tools, through the shared
[`authored-change`](../p13-prompt-model-optimization/specs/authored-change/spec.md) contract (one spine,
two origins; origin-blind refusals; `unverified` labeling; conflicts, reversal, audit, offline parity).
Two axis-specific rules are added. **Selection is fail-closed**: a skill must be a registry-sealed entry
with a *pinned* version, and a tool must be a member of the node's **discovered** tool set — neither is
free text. **The language boundary is stated at preflight**: on a node whose language has no landed
materializer, skills are not offered at all, and a binding submitted anyway is refused with the same typed
cause the transform raises.

**Alternative rejected — let the user type a skill or tool identifier and resolve it later.** It is the
obvious editor affordance and it removes a dependency on discovery being complete. Rejected on **L1 safety +
L2 stability**, and it is the same argument that makes `env` validate against `DeclaredEnv` and `expr`
validate against `in_scope`. A tool the frontend did not locate at that call site is not a tool the codemod
can delete — the emitted diff would either remove nothing or remove the wrong span, and both are silent.
A skill bound without a pinned version is worse: the SDK tool value is constructed from the version's
sealed schema, so an unpinned binding means *the shape of the constructed value is whatever the registry
happens to hold at apply time*, which is precisely the "compiles and then degrades quality invisibly"
failure `refuseSkills` was written to prevent. Fail-closed selection costs an authoring surface that can
only offer what discovery found; free text costs a class of wrong diffs that no test can enumerate.

**Alternative rejected — surface the language refusal at submit, reusing the transform's refusal.** No new
code path, and the cause text is already correct. Rejected on **L3 user-facing complexity**: a node's language is
known before the user opens the picker. Offering a full skill catalog on a node that provably cannot carry
one, then refusing after the user has chosen and ordered several, is the interaction-simplicity failure in
its purest form — the system withheld a fact it already had. The materializer-coverage table
([`docs/decisions/p14-materializer-coverage.md`](../../../docs/decisions/p14-materializer-coverage.md),
derived from the form table rather than copied) is the single source that answers it, so preflight and the
transform cannot disagree about which languages are supported.

## Decision 9 — Two total coverage tables, and the tool split is a frontend obligation

D-14.4 chose Go as the *first* materializer and left every other language on D-14.3's refusal. That was
right as an interim posture and is wrong as a terminal one, for a reason the refusal text itself makes
visible: discovery finds these call sites in all seven registered languages and the IR records them, so a
missing row describes **our backlog**, not the customer's code. D-14.5 closes it. Three sub-decisions do the
work, and each rejects a shortcut that would have been faster.

**Coverage is two tables, not one, and both are total.** Binding and pruning are blocked by different
things in different packages: binding needs a **spelling** for a (language, provider, SDK generation) cell;
pruning needs the **frontend** to record a tool split. So a language that can prune and cannot bind is the
*normal* case here, not an anomaly — and a single "P14 coverage" answer would have to pick one of the two,
which in practice means the pessimistic one, telling a customer whose real need is a prune that the axis
does not apply to them. Both tables are total over the registered language set (**absence is not a value**,
P13 Decision 13), so every language reads as either a materialization or a named missing artifact.

**The spelling is the only per-language part of binding, and it is evidence, not intent.** The *shape* of a
bound skill already comes from the pinned version's sealed schema, which is language-independent; what
differs per cell is how that provider's SDK **in that language, at that generation** spells a tool list.
That is why adding TypeScript is a set of rows rather than a second source of truth. It is also why a row
is admitted only with a **build gate** proving those bytes compile against the named generation —
*rejected: shipping a spelling that merely reparses.* This axis exists because a wrong tool schema
**compiles**; a row backed by anything less than a build is the failure the whole capability was written
to prevent, wearing a coverage badge.

**The pruner does not infer; the frontend records.** `spanRewriteTools` refuses today not because deletion
is hard at the syntactic floor — it is the easiest edit in the engine — but because a syntactic list
element is an unnamed span and the frontend records no tool split, so there is nothing to prune *against*.
*Rejected: let the span pruner infer which element is which tool* (by position, by text similarity, by
matching the selection's names against element text). Every version of that inference trades **L1
correctness for L8 convenience**, and its failure mode is a prune that deletes the wrong element in a diff
that parses — the failure class with no downstream net. The frontend recording each tool's identifier and
declaration location is more work, in the right package, and it makes the unlocatable case explicit rather
than indistinguishable from the absent one.

**What stays refused after every row lands.** An SDK that carries its tools inside an opaque serialized
body has no tool value to construct or delete in **any** language, and a call site that assembles its tool
set at run time has no declaration to point at. Both are `call-site-cannot-carry-it` under P13's cause
classes, both refuse identically before and after 14d, and neither may be counted as a platform gap — the
distinction that keeps a coverage table from turning into a promise nobody can keep.

**Corollary — a reorder is a real change.** Skill order is identity-bearing, so an authored reorder yields
a new `config_hash` and is a scoreable configuration, not a cosmetic edit. The authoring surface must not
present it as one.

## Interfaces sketch

```
// EXISTS today
rewriters[DimSkills]  = rewriteSkills → refuseSkills(nodeID, o) → ErrUnsafeRewrite   // rewrite.go:57,378
spanRewriters[DimSkills] = refuseSkills                                              // rewrite_span.go:62
IRNode.ToolsSkills []string                                                          // emit.go:98 (conflated, FROZEN)

// P14 skill-binding  (14a)
rewriters[DimSkills]  = materializeSkill | refuseSkills   // per language: construct SDK tool value from SkillEntry schema,
                                                          // else ErrUnsafeRewrite (interim refusal, tested)
verify(candidate)     = harness score gate                // add/remove/rerank are proposals; a regression does not ship
SkillEntry.ValidateInput(args)                            // arg shape checked before execution (FR4)

// P14 tool-selection  (14b, additive)
IRNode.Tools   []IRTool `json:"tools,omitempty"`          // DeclaredEnv omitempty pattern (emit.go:39-43)
IRNode.Skills  []string `json:"skills,omitempty"`
Dimension       DimTools = "tools"                        // closed enum grows by one; no new Kind
NodeOverride.ToolSelection *ToolSelection `json:",omitempty"`   // { keep []string }, validated vs IRNode.Tools (fail closed)
ResolvedNode.ToolSelection *ResolvedToolSelection `json:",omitempty"`   // nil-when-empty → byte-identical pre-P14 (D5)
rewriters[DimTools]   = deletePrunedTool | refuseDynamic  // deletion of a static tool element, else ErrUnsafeRewrite (FR14)
OpToolPrune           // catalog row; scored by existing metrics (D6) → eval_tokens_total ↓ · tool_error_rate ↓

// P14 skill-tool-authoring  (14c — a second ORIGIN on the same spine; see p13 authored-change)
authoring.Draft{ Edits: node → { SkillRefs?, ToolSelection? } }        // parent immutable; token-guarded
preflight(draft) → admissible
                 | refused{ cause, node, field }                       // SAME typed causes as the operator path:
                 //   no-materializer(language) · unknown-or-unpinned-skill · invalid-skill-args(field)
                 //   tool-outside-discovered-set(tool) · dynamic-tool-set(node)
                 | not-yet-measurable{ missing }
offer(node) = { skills: registry-sealed ∧ pinned ∧ materializerCoverage(node.language),
                tools : node.Tools (discovered) }                      // fail closed; NEVER free text
// reorder is identity-bearing ⇒ new config_hash ⇒ a real, scoreable change, not a cosmetic one
```

## Risks

| Risk | Mitigation |
|---|---|
| A materialized tool schema is subtly wrong and degrades quality invisibly | Decision 1 — shape from the **sealed** schema; the diff is **verified**, not trusted; args validated before execution. |
| A language without a materializer silently drops the skill | Decision 2 — `ErrUnsafeRewrite`, no partial diff; a go-red test that a silent drop **fails**. |
| Splitting `ToolsSkills` breaks a pre-P14 `config_hash` or orphans a keyed row | Decision 3 — additive `omitempty` fields only; frozen slice retained; golden vectors reproduce. |
| A tool selection names a tool the node does not offer and applies to nothing | Decision 4 — validate against the **discovered** set, fail closed. |
| Pruning over a dynamically-built tool list produces a bad edit | Refuse a dynamic tool set with `ErrUnsafeRewrite` rather than guess (FR14). |
| A new tool metric forks the definition of a saving | Decision 6 — no new metric; scored by the axis-agnostic harness. |
| A tool/skill change leaks a raw provider error and breaks `tool_error_rate` | Decision 7 — failures only through the `toolcontract` allowlisted codes. |
| A user types a tool or skill name and the codemod deletes the wrong span, or nothing | Decision 8 — fail-closed selection: skills from the sealed registry with a **pinned** version, tools from the node's **discovered** set; free text is not a selection. |
| A user picks skills on a node whose language cannot carry one, and finds out after submitting | Decision 8 — the language boundary is stated at preflight from the **same** materializer-coverage table the transform reads, so the two cannot disagree. |
| An authored reorder is treated as cosmetic | Decision 8 corollary — skill order is identity-bearing; a reorder yields a new `config_hash` and is presented as a real change. |
| An unverified authored prune is quoted as a token saving | The shared `unverified` rule — no token, cost, or error-rate saving is attributed until the harness runs. |
| The capability doc and actual per-language coverage drift | Coverage written down once (NFR7), the `argumentForm` single-source-of-truth discipline. |
| A language is absent from coverage and reads as "not applicable" | Decision 9 — **two total tables** over the registered language set; a generated test fails on a missing cell. |
| A new spelling compiles against the wrong SDK generation | Decision 9 — a row names its generation and is admitted only with a build gate proving those bytes compile against it. |
| A span pruner deletes the wrong element | Decision 9 — the **frontend** records each tool's declaration location; an unlocatable tool is recorded as such and refused, never inferred. |
| Pruning is written off because binding is unavailable | Decision 9 — two tables, neither read as implying the other; a language routinely prunes before it binds. |
| A call site that assembles tools at run time is told to wait for a rewriter | Decision 9 — it is `call-site-cannot-carry-it`, reported ahead of the language question and unchanged by any later row. |
