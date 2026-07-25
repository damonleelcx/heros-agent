# P14 — Recorded decisions (System Designer)

Three contracts that must be fixed **before any transform or discovery code ships**, because each is a
one-way door. `design.md` argues the full decision set; this file records only the pre-code, one-way-door
contracts: the tools≠skills IR split (D-14.1), the new `DimTools` dimension without a registry `Kind`
(D-14.2), and the interim-refusal contract (D-14.3).

---

## D-14.1 — tools≠skills is an **additive** IR split; `ToolsSkills` is frozen and never repurposed

**Problem.** The IR carries [`emit.go:98`](../../../internal/discovery/emit.go) `ToolsSkills []string` —
one flat list conflating two different things. A *tool* is a provider-native function the model may call,
*selected* (kept/pruned) from what the model is offered; a *skill* is a registered platform capability
with a sealed contract, *bound* by constructing a value. Tool pruning cannot be expressed while the two
share a slice: "prune this unused tool" would also read as "unbind this platform skill." But `ToolsSkills`
is part of the **frozen IR bytes** — the emitter always normalizes it to `[]` when empty
([`emit.go:223`](../../../internal/discovery/emit.go)), so its emptiness is *part of* the golden — and
`config_hash`-keyed rows depend on the current shape. Splitting is a one-way door: the moment a consumer
treats "the third element of `ToolsSkills`" as a stable identity, un-splitting is a breaking migration.

**Decision.** Split **additively**. Add two new fields to `IRNode`:

```go
// Tools are provider-native functions/tools the model may call. ADDITIVE and omitempty (DeclaredEnv
// pattern, emit.go:39-43): a pre-P14 IR that predates this field serialises byte-identically.
Tools []IRTool `json:"tools,omitempty"`
// Skills are registered platform-capability references. ADDITIVE and omitempty for the same reason.
Skills []string `json:"skills,omitempty"`
```

`ToolsSkills` is **retained unchanged and never repurposed** — it remains the frozen conflated view that
pre-P14 consumers read. The discovery frontend populates `Tools`/`Skills` at extraction (it is the only
component that knows which discovered entry is which); a consumer never re-derives the split.

**Why this is the appropriate design.** The expand-contract rule the registry and IR already live by is:
a new *optional* field must leave old serialisations byte-identical (registry.go, "a spec struct that
gains an optional field must leave old envelopes byte-identical"). `omitempty` + nil-when-empty applied to
`Tools`/`Skills` is exactly that rule. The golden vectors carry no `tools`/`skills` key, so they keep
reproducing; a pre-P14 IR round-trips byte-for-byte and its `config_hash` is untouched.

**Alternatives + decision point.** (a) **Repurpose `ToolsSkills`** (make it tools-only, add a separate
skills field) — changes the frozen bytes for every node that has any tool or skill, **breaks the golden
vectors**, and **orphans `config_hash`-keyed rows**. Rejected on **stability (L2) and evolvability (L5)**:
a frozen contract cannot be re-cut under live keyed data. (b) **Fold tools into `DimSkills`** (no new
fields, reuse the skill list) — overloads one dimension with two contracts and lets a tool prune
masquerade as a skill change in the hash. Rejected on **single-source-of-truth**. The additive split is
the only shape that separates the two mechanics **and** leaves every pre-P14 serialisation byte-identical.

**Effect.** Every IR authored before P14 serialises to exactly the bytes it did before and stays
reproducible. Tools and skills become independently addressable, so tool pruning (deletion) and skill
binding (construction) are expressible without either implying the other.

---

## D-14.2 — Tool selection is a new `DimTools` dimension **with no new registry `Kind`**

**Problem.** Tool pruning must change `config_hash` (a pruned tool set is a different configuration), so
it has to land in `resolved_config`. Following the canonical "add an axis" checklist, that is a new
`Dimension`, a new `NodeOverride` field, a resolve block, and a new `ResolvedNode` field. The open
question is whether a **tool** also needs a registry `Kind` the way a **skill** has `KindSkill` — i.e.
whether a tool is *resolved from a registered, version-addressed ref* or *selected from what the call site
already declares*. This is one-way: once a tool has a sealed identity that customers key against, it
cannot be un-sealed.

**Decision.** Add **`DimTools`** as a distinct dimension (the closed enum `{DimModel, DimPrompt,
DimSkills, DimContext}` grows to five), with a `NodeOverride.ToolSelection` field, a `resolveNode` tool
block, and an additive `ResolvedNode.ToolSelection` field. **Add no new registry `Kind`.** A tool
selection names tools by their **discovered** call-site identifiers and is **validated against the node's
discovered tool set** — a selection naming a tool the IR does not record for that node is **rejected
(fail closed)**.

**Why this is the appropriate design.** A tool is declared at the call site and offered to the model — it
is *already identified by that call site*. Sealing it into the registry would invent a second,
version-addressed identity for something that already has one, and force every discovered tool through a
registration path it does not need (a skill needs `KindSkill` because its *contract* — the input/output
schema — is what a `skill_ref` pins; a tool selection pins nothing but a *subset of what was discovered*).
Validating a selection against the discovered set is the pattern the codebase already proved twice: an
`env` binding validated against `DeclaredEnv` ([`emit.go:39-43`](../../../internal/discovery/emit.go)) and
an `expr` binding validated against `in_scope` (D-1.2). Both fail closed — the safe direction — and tool
selection inherits exactly that: a selection over a tool the node does not offer is refused, never applied
to nothing.

**Alternatives + decision point.** (a) **Give tools a registry `Kind`** — symmetric with skills, reuses
the seal/resolve machinery. Rejected on **extensibility (L6) and single-source-of-truth**: it duplicates
an identity the call site already carries and adds a registration burden with no contract to pin. (b)
**Reuse `DimSkills` for tool selection** — no enum growth. Rejected on **single-source-of-truth**: it
conflates in the *dimension layer* the very thing D-14.1 splits in the *IR layer*. `DimTools` with
discovered-set validation is the shape that keeps tools first-class, reproducible, and fail-closed without
inventing an identity.

**Effect.** Tool selection is a first-class, hashed dimension the optimizer can propose over, while a tool
stays what it is — a discovered call-site declaration — rather than becoming a registry object. A prune
naming a real discovered tool is accepted and changes `config_hash`; a prune naming a phantom tool is
refused at resolve.

---

## D-14.3 — The interim refusal is a **specified, testable** contract, not a temporary gap

**Problem.** Binding a skill is call-site **construction** (build an SDK tool value from a JSON schema),
and that construction lands **per language** — Go first, tree-sitter languages behind it. Between "the
axis is applicable" and "every language's materializer has landed," a node can carry a `SkillRef` the
active engine cannot yet materialize. The same is true for a tool selection over a **dynamically-assembled**
tool set the frontend cannot locate as a static, deletable declaration. The tempting shortcut — drop the
un-applicable override and emit the rest of the diff — is one-way in the worst sense: it ships changes
that look applied and are not, and once that behavior is relied on it is hard to retract.

**Decision.** An un-applicable override is **refused at transform**, loudly, with `ErrUnsafeRewrite`,
naming the node and dimension and the reason. Specifically:

- A node carrying a `SkillRef` whose **language has no landed materializer** → `ErrUnsafeRewrite`
  (`refuseSkills`-shaped), **no** diff for that node's skill dimension.
- A tool selection over a **dynamically-assembled** tool set → `ErrUnsafeRewrite`, no guessed deletion.

Neither is ever a **silent drop**, and no **partial diff** is emitted for the refused dimension. The
refusal is a **first-class requirement with its own scenarios** and a **go-red test**: a test that a
silent drop still passes is itself a failing test.

**Why this is the appropriate design.** This is the repo's honest pattern already: `ErrUnsafeRewrite` is
*"the outcome FR5 specifies … the spec anticipated exactly this case and asked for a loud refusal"*
([`rewrite.go`](../../../internal/transform/rewrite.go)). A silent drop ships a node the author asked to
change **without** the change, while its `config_hash` still claims it and the eval scores a configuration
that never existed — an L1 correctness defect wearing the costume of a completed diff. Fail-loud is the
only direction that cannot mislead a merge. P14 **narrows when** the refusal fires (per language, as
materializers land; per call site, static vs dynamic), it does not weaken **that** it fires.

**Alternatives + decision point.** **Strip the un-applicable override and emit the rest** — every spec
produces a diff and nothing ever "fails." Rejected on **安全 (L1) over 实现 (L8)**: the convenience of a
never-failing codemod is bought with a change that silently omits what it claims. Under the eight-level
law an L1 correctness risk cannot be traded for L8 implementation convenience. The refusal costs a user an
occasional "not yet for this language" message (an L3 UX cost, the one we are allowed to pay) and buys the
guarantee that a merged diff did what its `config_hash` says.

**Effect.** As each language's materializer lands, its refusal is *replaced by real materialization* and
its go-red test flips to a materialization test — a visible, testable progression. Until then, an
un-applicable skill or a dynamic tool set is refused by name, and no diff ever ships a configuration it
did not actually apply.
