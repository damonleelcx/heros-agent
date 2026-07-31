# P14 — Recorded decisions (System Designer)

Five contracts that must be fixed **before any transform or discovery code ships**, because each is a
one-way door. `design.md` argues the full decision set; this file records only the pre-code, one-way-door
contracts: the tools≠skills IR split (D-14.1), the new `DimTools` dimension without a registry `Kind`
(D-14.2), the interim-refusal contract (D-14.3), which language gets the first skill materializer
(D-14.4), and the shape of the coverage claim that carries the rest of the languages (D-14.5).

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

---

## D-14.4 — **Go** gets the first skill materializer; every other language keeps D-14.3's refusal

**Problem.** D-14.3 fixes *that* an un-applicable skill refuses; it does not say *which* language stops
refusing first. That ordering is a one-way door in a quieter way than the others: the first materializer
sets the shape every later one is judged against — how the sealed schema becomes a tool value, where the
per-language coverage is written down, and what a refusal for the *remaining* languages has to say now
that "no language can do this" is no longer true. Choosing a language whose evidence is weakest would
bake a guess into that shape.

**Decision.** **Go is the first supported language.** Its materializer lives in `internal/transform`
alongside `rewriteSkills`, and it is gated on a **declared per-provider tool-value form** — an anthropic
form and an openai form, spelled once as data. Every other language (the tree-sitter engines) keeps
`refuseSkills` unchanged, and a Go call site whose provider has **no declared form** keeps refusing too:
support is per (language, provider), not per language. The per-language / per-provider coverage is
recorded in **one** place — the form table's doc comment — for the same reason `argumentForm` is
(NFR7, task 9.4): a refusal a user reads and a capability a doc claims must not drift.

**Why this is the appropriate design.** Go is where the evidence is strongest and the blast radius
smallest. `go/parser` + `go/ast` give the engine a *typed, non-recovering* parse: a file that yields a
call site is a file that parsed (see `reparseGo`), so an insertion point is a real position rather than
one invented inside source the parser was guessing at. The tree-sitter engines are explicitly the
opposite (`reparseSyntactic`), and `rewrite_span.go`'s header already argues at length why a type-free
parse cannot tell a role string from a prompt — the same blindness that makes a *prompt* rewrite unsafe
there makes a *tool-value construction* unsafe there, and more so, because construction has no original
expression to check the result against. `refuseSkills` is already a Go rewriter, so Go is also where the
refusal is replaced rather than newly written. Finally, Go is where the build gate bites hardest: a
wrong construction is a compile error the existing `BuildChecker` catches before a reviewer sees it,
whereas in Python a wrong construction *parses* and reaches verification only as a quality regression.

**Alternatives + decision point.** (a) **Python first** (the largest population of agent call sites) —
rejected on **安全 (L1)**: the language with the most call sites is also the one with the least evidence
per call site and no compile gate, so the first materializer would be the one whose mistakes are the
hardest to see. (b) **All languages at once behind a shared JSON-schema→tool-value writer** — rejected
on **single-source-of-truth and 实现 (L8)**: the SDK tool value's *shape* is per language AND per SDK, so
a "shared" writer would be a switch pretending to be an abstraction, and it would land five untested
spellings for the price of one tested one. (c) **Provider-agnostic Go materializer** (construct from the
schema without knowing the SDK) — rejected for the reason `refuseSkills` gives today: there is no
provider-agnostic spelling of a tool value, and inventing one produces code that compiles against
nothing.

**Effect.** 14a lands one materializer, for Go, for the providers whose tool-value form is declared. Every
other (language, provider) pair keeps refusing **by name** — "no materializer for `<language>` yet" /
"no declared tool-value form for provider `<p>`" — so the boundary is legible from the refusal itself,
and each later materializer flips exactly one go-red refusal test into a materialization test.

> **Scope note (D-14.5).** "Go is first" is an **ordering** decision, not a terminal state. D-14.5 defines
> the shape of the claim that carries the remaining languages and the obligations that make each of them a
> row rather than an invention. D-14.4's refusal text, its per-(language, provider) granularity, and its
> go-red tests are unchanged by it.

---

## D-14.5 — Coverage is **two total tables**; the tool split is a **frontend** obligation, never an inference

**Problem.** D-14.4 leaves the axis in a state that is honest per refusal and dishonest in aggregate.
`MaterializerCoverage()` reports only the cells that work, so a language with no materializer has **no row
at all** — and an absent row renders on every surface as *not applicable*, which is a statement about the
customer's code. It is not: discovery finds these call sites in all seven registered languages and the IR
records them. Worse, the axis has *two* mechanics with *two different* blockers, and the single word
"Go-only" hides which one a given customer has hit. Both of those are one-way doors in the same quiet way
D-14.4 was: the first shape published is the shape every later cell is judged against, and a consumer that
learns to read "absent means unsupported" cannot be un-taught cheaply.

**Decision.** Three parts, all pre-code.

1. **Two coverage tables, both total.** Skill **binding** coverage is keyed by
   **(language, provider, SDK generation)**; tool **pruning** coverage is keyed by
   **(language, frontend tool split)**. Each carries an entry for **every** registered language, and an
   entry is either a materialization or a **named missing artifact**. Neither table may be read as implying
   the other, and neither may express a gap by omission. This is P13's `language-coverage` contract applied
   to this axis; the totality check is generated from `discovery.DefaultFrontends`, so adding a frontend
   with no entry fails.
2. **A spelling row is admitted only on a build.** A `(language, provider, SDK generation)` row is a claim
   that *these bytes compile against that SDK generation*, and the only thing that can prove it is a build
   gate. A row that merely reparses is not a row.
3. **The frontend records the tool split; the pruner never infers it.** Every language frontend records,
   per tool, the call-site identifier and the **location of its declaration**, and records *unlocatable*
   explicitly where it cannot. A span pruner may not derive which unnamed element is which tool by
   position, text similarity, or name matching against the selection.

**Why this is the appropriate design.** Part 1 is an **L3 / L6** argument. A single answer for two mechanics
would have to be resolved toward the pessimistic one, so a customer whose real need is a *prune* — which is
the cheap, byte-safe, no-construction half — would be told the axis does not apply. And absence-as-a-value
is the specific failure that makes a backlog item read as a product boundary; making the tables total costs
rows and buys the platform the ability to say *which of three things* is missing (P13 FR43), two of which
are not ours to fix. Part 2 is **L1**, and it is this axis's founding argument restated: `refuseSkills`
exists because a subtly-wrong tool schema *compiles and then degrades quality invisibly*. A coverage row
backed by a reparse rather than a build reintroduces exactly that, with a badge on it. Part 3 is **L1 over
L8**: inference is cheaper than teaching six frontends to record a location, and its failure mode is a
prune that deletes the wrong element in a diff that parses — the failure class ADR-001 names as having no
downstream net. Recording the location is more work in the *right* package; inferring it is less work in
the *wrong* one.

**Alternatives.** (a) **One coverage table for the axis** — rejected under part 1's argument; the mechanics'
blockers live in different packages and move independently. (b) **Reach the other languages by having the
span pruner match selection names against element text** — rejected under part 3; it is an inference
presented as a lookup. (c) **Admit spelling rows on reparse alone, and let verification catch the bad
ones** — rejected under part 2: verification catches *quality* regressions, and a wrong tool schema's first
symptom is a model that stops calling the tool, which is a slow, expensive way to learn what a build would
have said in a second. (d) **Declare the remaining languages structurally unsupported and stop** — rejected
as false: the only structural cases are an SDK whose tools live in an opaque body and a call site that
assembles its tools at run time, and those refuse under `call-site-cannot-carry-it` in **every** language,
including Go.

**Effect.** 14d turns each remaining language into a bounded set of artifacts — a spelling row per
(provider, SDK generation), a frontend tool split, and where the SDK binds before the call, a binding site
(P13 FR52) — each landing as one go-red refusal test flipped into a materialization test, exactly as
D-14.4 intended. What does **not** change: the sealed schema stays the sole source of a bound skill's
shape in every language; a materialized binding is still a proposal the harness decides; and the two
structural refusals above stay refused after every row has landed.
