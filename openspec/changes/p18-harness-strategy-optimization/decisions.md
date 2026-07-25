# P18 — Recorded decisions (System Designer)

Six contracts that must be fixed **before any code ships**, because each is a one-way door: a new registry
**Kind** (`harness`), a new **Dimension** (`DimHarness`), a new **DB table** (`harness_entry`), the
**interim-refusal** contract (refuse-never-drop), the **harness-vs-wiring** boundary, and the **cost/quality
admissibility** rule. Each is walked through the five-step record (problem → decision → why appropriate →
alternatives + the level it was rejected on → effect) and tagged with the governing 八级法则 level. A
standing **L1 安全** note on autonomous-turn blast radius closes the file.

The narrative reasoning and the rejected-alternative prose live in [`design.md`](design.md); this file is
the contract of record.

---

## D-1 — A new registry **Kind `harness`** (one-way door: a fifth content-addressed registry)

**Problem.** A strategy must be referenceable from a spec by an immutable id and resolvable back from a
`config_hash` months later, exactly like a model or prompt. The registry Kind set is closed and hashed
into every `version_id` (`internal/registry/registry.go:57` — `KindModel/KindPrompt/KindSkill/KindContext`);
adding a Kind is not reversible once ids are minted under it.

**Decision.** Add `KindHarness` and a register/resolve file (`internal/registry/harness.go`), sealing each
strategy by content address exactly as the four existing Kinds. A `HarnessRef` is a `harness` `version_id`
and nothing else.

**Why appropriate.** Content addressing is the platform's lineage guarantee: a stored `config_hash` must
resolve the exact strategy bytes that produced it. The Kind is hashed into the id, so a `harness` ref used
in a non-harness dimension fails **closed** rather than colliding — the same cross-Kind safety every
existing registry relies on. This is the checklist's step 5 applied verbatim; no new mechanism is invented.

**Alternatives + decision point.** Hard-code the five strategies as an in-code table (no new Kind). Rejected
on **L5 不可演进**: an in-code table cannot be pinned — the moment it changes, a stored `config_hash` no
longer resolves the strategy that produced it, and lineage breaks. Evolvability of a versioned reference
cannot be traded for the convenience of skipping a Kind.

**Effect.** A strategy is versioned, immutable, and referenced by id; a `config_hash` authored today
resolves the same strategy years later; a cross-Kind ref fails closed.

---

## D-2 — A new **Dimension `DimHarness`** (one-way door: the closed axis enum)

**Problem.** The scaffold must be a sparse, overridable, identity-bearing axis. The `Dimension` enum is
closed and tiny (`internal/variantspec/spec.go:42` — model/prompt/skills/context); every error names one,
and the transform, resolver, and operator all iterate it. Adding a member is a contract every consumer
depends on thereafter.

**Decision.** Add `DimHarness` to the closed enum, plus the additive `NodeOverride.HarnessRef`
(`spec.go:183`), the `resolveNode`/`Dimensions()` block (`resolve.go:67,154`), and the auto-hashed
`ResolvedNode.HarnessRef` (`resolved.go:46`) — the checklist's steps 1–4.

**Why appropriate.** Because `config_hash` is purely structural, a first-class resolved field
*auto-participates* in identity with no hashing change — the axis is scorable the moment it lands. A closed
enum keeps the set finite and every error nameable. This is the only shape that makes harness a peer of the
existing axes rather than a bolt-on.

**Alternatives + decision point.** A free-form `strategy` string on the node, or a params bag folded into
`context_params`/`provider_params`. Rejected on **L6 不可扩展 + L8 实现**: a stringly-typed channel hashes
inconsistently and invites a divergent sixth path, and folding into a frozen field lets a scaffold change
masquerade as a param change in the hash (禁止分裂 single-source-of-truth). An L6 extensibility regression
cannot be bought with the L8 convenience of not adding an enum value.

**Effect.** Two variants differing only in scaffold are distinct configurations the platform can compare,
diagnose, and rank; the axis is added by the checklist, not around it.

---

## D-3 — A new **DB table `harness_entry`**, and no-harness hashes byte-identically (one-way door: schema + frozen identity)

**Problem.** The `harness` Kind needs a table (`harness_entry`), joining `model_entry`/`prompt_entry`/
`skill_entry`/`context_entry` (`registry.go:63-70`). Simultaneously, the resolved harness field must join
`config_hash` **without** changing the hash of any configuration that declares no harness — `config_hash`
is P0's frozen contract with golden vectors the live producer must reproduce bit-for-bit (`resolved.go`),
and every existing row is keyed by it. A new stored table and a hash change are both irreversible once
customers depend on them.

**Decision.** Add the `harness_entry` table (content-addressed rows, like its four siblings), and make
`ResolvedNode.HarnessRef` **additive, `omitempty`, and nil-when-empty**: a node that binds no harness emits
**no** `harness_ref` key, so its canonical bytes are identical to a pre-P18 node.

```go
// HarnessRef is the resolved strategy version_id for this node's scaffold. ADDITIVE and omitempty with a
// nil-when-empty value: a node with no harness override emits NO harness_ref key, so it serialises
// byte-identically to a pre-P18 node and the frozen golden vectors keep reproducing. Present only when a
// harness is overridden or a non-single-shot default was discovered; config_hash changes iff it changes.
HarnessRef string `json:"harness_ref,omitempty"`
```

**Why appropriate.** This is the expand-contract rule the registry and IR already live by — a new *optional*
field must leave old serialisations byte-identical — applied exactly as `resolved.go`'s D-1.4 applied it to
bindings. The sibling maps (`skill_refs:[]`, `context_params:{}`) are always-present because their emptiness
predates and is *part of* the frozen golden bytes; `harness_ref` is new, so its **absence** is what must be
byte-compatible, achieved by omission. It is the only shape satisfying "changes iff a harness changes" and
"no-harness spec unchanged" at once.

**Alternatives + decision point.** (a) Make `harness_ref` always-present (like its siblings) — changes the
golden bytes for **every** node in **every** existing config, breaking P0's contract and orphaning every
keyed row; rejected outright on **L2 稳定 / L5 不可演进** (reproducibility of a frozen contract). (b) Reuse
an existing table with a `kind` column instead of a new `harness_entry` — overloads a frozen registry table
and blurs cross-Kind uniqueness; rejected on single-source-of-truth. The chosen shape loses nothing at L2/L5.

**Effect.** Every config authored before P18 hashes to exactly the byte it did before and stays
reproducible; a config that adds/removes/changes a harness gets a new `config_hash`; the new table is a
peer of the four registries. Backward compatibility is a **test** (tasks 3.5, 4.2), not a hope.

---

## D-4 — The **interim-refusal** contract: a `HarnessRef` at transform is refused, never dropped (one-way door: the honesty seam)

**Problem.** Materializing a control loop at a call site — wrapping a single call in a bounded turn loop
with a stop condition and a critic — is code generation, strictly more structural than the already-refused
`skills`/`context`. It is not yet safe to emit. But an override that resolves and hashes must not be allowed
to *silently do nothing*, or the platform would score a variant as if its scaffold changed when the source
did not.

**Decision.** The per-dimension rewriter refuses any resolved node — or node-group — carrying a `HarnessRef`
with a typed `unsafeRewrite` (`internal/transform/edit.go:90`) naming the strategy and the reason, on both
the Go and tree-sitter engines, mirroring `refuseSkills` (`internal/transform/rewrite.go:388`). The override
is **refused, never dropped**: it remains present in the resolved config, and the transform fails visibly
rather than emitting an incorrect loop.

**Why appropriate.** This is the repo's established honest interim — `skills` and `context` are modelled,
resolved, and hashed but refused at the codemod until their materialization is safe. Harness, being more
structural than either, belongs squarely inside it. A typed refusal keeps "modelled but not yet applicable"
a first-class, **testable** state (a test asserts present-and-refused, and would fail on a silent drop), and
it is the seam a later phase lands in when the bounded-loop rewrite is proven safe — exactly as P3 lands in
`refuseContext`.

**Alternatives + decision point.** Silently no-op the override until the codemod exists (least friction; the
axis "works" end-to-end and does nothing at the call site). Rejected on **L1 安全 + L2 稳定**, and it is the
most dangerous option on the table: a silent drop produces a **false eval result** — the one output an eval
platform must never emit. Convenience at L8 cannot buy an L1 correctness regression.

**Effect.** A harness override either fails visibly at transform or (later) is realized correctly; it can
never be scored as a change that did not happen. The refusal can *go red* in a test, which is the whole of
the guarantee.

---

## D-5 — The **harness-vs-wiring** boundary: harness composes with P15, never reorders (one-way door: axis ownership)

**Problem.** A harness change can span a **node-group** (a subgraph). P15 owns node-graph wiring
(`VariantSpec.Order`/`Edges`, `spec.go:255-258`). Without a boundary, a group harness could re-derive its
own edge set and drift from the wiring the executor actually walks — two divergent definitions of the same
edges.

**Decision.** A harness wraps a **single node** or an **explicit ordered edge set**. The group form
**consumes** P15's wiring — the wiring defines *what the edges are*, the harness defines *what loop runs over
them* — and a harness override **never reorders** nodes. The M21 default is an explicit, diff-auditable edge
set on the override; inferred subgraph scoping waits until P15's wiring identity is frozen.

**Why appropriate.** Single-source-of-truth: wiring is P15's to define, loop-over-edges is P18's; neither
reaches into the other. `OpReorder` (`internal/proposal/operator.go`) stays P15/P5's operator;
`OpHarnessStrategy` never emits an ordering change. The boundary is clean because the two axes are
genuinely orthogonal — the edges and the loop over them are different facts.

**Alternatives + decision point.** Let a group harness infer its own subgraph. Rejected on **L5/L7 单一真相**:
an inferred edge set is a second definition that can drift from the wiring, surfacing as an execution
mismatch rather than a test failure. The reversible choice (explicit now, inferred later if P15 freezes its
identity) is the L5-correct call.

**Effect.** A group harness names an auditable ordered edge set and composes with wiring; a harness change
is never confusable with a reorder; the two axes evolve independently.

---

## D-6 — The **cost/quality admissibility** rule: a heavier harness earns its cost, on held-out data (one-way door: what "a scaffold win" means)

**Problem.** A heavier scaffold (more turns) almost always raises `task_success` *somewhere* while
multiplying `eval_cost_usd` and `eval_latency_ms`. If the operator admits any scaffold that raises quality,
it will ship expensive loops that won on quality alone — a bill the customer never agreed to buy quality
with. Once the operator's admissibility semantics are public, they cannot be quietly tightened.

**Decision.** `OpHarnessStrategy` admits a heavier harness over a lighter one **only** when the measured
`task_success` gain outweighs its added `eval_cost_usd` and `eval_latency_ms`, computed on **held-out**
cases disjoint from any used to shape the proposal. A swap that raises cost/latency without a commensurate
`task_success` gain is rejected by the gate.

**Why appropriate.** The platform's discipline is that a change ships on *verified* value, not a partial one
— **diagnosis proposes, verification decides**. Making the trade-off explicit converts a hidden cost into a
gate that can reject; the held-out framing stops a win that is really overfit to its tuning set. All three
metrics come from the existing harness unchanged (`metricnames.go`); the gate is arithmetic over them, not a
new measurement, so no eval change is incurred.

**Alternatives + decision point.** Admit any scaffold that raises `task_success` (simpler gate). Rejected on
**L1 honesty + strategy**: "it scored higher" ignores the turns it burned, and admitting a cost blow-up as a
win is indefensible in a billing conversation. An L1 honesty regression cannot be bought with a simpler
operator.

**Effect.** A heavier scaffold ships only when it is worth its cost on data it was not tuned against; a
cost-only "win" is rejected; the trade-off is a gate that can *go red* (tasks 6.3, 6.4).

---

## L1 安全 note — autonomous turns enlarge the blast radius; the guarantee is *observable*, not *controlled*

A harness that adds autonomous tool-calling turns (`react-loop`, `plan-execute`, `reflexion`, `critic-loop`)
**raises the sandbox / blast-radius surface** relative to `single-shot`: more turns of autonomous
tool-calling are, honestly, more opportunities to act. This is the real cost of the axis and it is stated,
not hidden. Two contracts contain it (Decision 7 in `design.md`, tasks 7.1–7.2):

1. **Bounded autonomy.** Every multi-turn strategy declares a bounded `max_turns` and a stop condition;
   **no strategy can express an unbounded loop**, and a run reaching the ceiling terminates and is recorded.
2. **No grant widening.** The added turns run within the node's **existing** P3 sandbox and tool grant; they
   reach no egress destination or tool outside it, and the enlarged turn/tool-call surface is **observable**
   in the trace.

The sales-honest framing (per the sales-ops discipline) is that the risk is **风险可观测 (observable)**,
never **风险可控 (controlled)** — the added surface is real, and the guarantee is that you can *see* it in
the trace and that it is *bounded*, not that it is gone.
