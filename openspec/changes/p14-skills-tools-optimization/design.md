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
| The capability doc and actual per-language coverage drift | Coverage written down once (NFR7), the `argumentForm` single-source-of-truth discipline. |
