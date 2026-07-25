# Design — P18: Harness Strategy Optimization

Product rationale: [`../../../docs/prd/P18-harness-strategy-optimization.md`](../../../docs/prd/P18-harness-strategy-optimization.md).
One-way-door contracts: [`decisions.md`](decisions.md). Refusal-pattern precedent: `refuseSkills`
(`../../../internal/transform/rewrite.go` :388) and `refuseContext` (:417).

## Context

The optimizer's leverage is one structural fact: anything that lands in `ResolvedConfig` flows into
`config_hash` (`internal/confighash`, purely structural), and the eval harness scores by
`config_hash`+`Trace` **without reading a Dimension label** (`internal/evalharness/family.go`). A new axis
therefore becomes scorable the moment it participates in identity — no eval change required. Adding an
axis is a mechanical **eight-step checklist** (authoring kit): a `Dimension` const, a `NodeOverride`
field, a `resolveNode`/`Dimensions()` block, a `ResolvedNode` field, a registry `Kind`, an additive IR
field + frontend, the per-dimension rewriter, and an operator + catalog row + prior.

What does not exist is any notion of the *scaffold* around a node: how many turns it runs, in what control
loop, with what stop condition. Every "harness" symbol in the tree is the **eval** harness
(`internal/evalharness`), unrelated. The one trace of an agent scaffold is a comment naming what *target*
codebases contain — `internal/irwriteback/recover.go:11` lists "a ReAct loop, a script of independent LLM
calls" as a shape the platform **discovers**, never one it **models**. The prior runtime harness
(leader/follower/critic, ~1898 LOC) was removed in the migration; only its idea — generator + separate
critic — survives, as one catalog entry.

Two forces shape everything below. **Modelling requires the axis to be first-class** — a closed Dimension,
a versioned Kind, an auto-hashed field — or it forks the identity contract. **Materialization is not yet
safe** — wrapping a call in a bounded loop with a stop condition and a critic is code generation, strictly
more structural than the already-refused `skills`/`context`. The resolution is not a compromise: P18
ships the axis fully modelled and hashed, and **refused at the codemod boundary**, exactly as `skills` and
`context` are today. Modelling and materialization are sequenced, not opposed.

## Decision 1 — Harness is a new closed `Dimension`, added by the eight-step checklist

`DimHarness` joins the closed enum (`internal/variantspec/spec.go:42`); the axis is added through all eight
canonical sites and no shortcut.

**Alternative rejected — a free-form `strategy` string on the node, or a params bag folded into an
existing dimension.** Less code, no new enum value. Rejected on **L6 不可扩展** and **L8 实现**: a closed
enum keeps every error nameable and the set finite, and — more importantly — a first-class field
*auto-participates* in `config_hash` because the hash is purely structural. A stringly-typed sixth channel
would hash inconsistently, and folding harness into `context_params`/`provider_params` would let a
scaffold change masquerade as a param change in the hash, violating single-source-of-truth. The checklist
is the design; deviating from it is the smell.

## Decision 2 — Strategies are a content-addressed registry Kind `harness`

A new Kind `harness` (`internal/registry/registry.go:57`) seals each strategy by content address, exactly
as `model`/`prompt`/`skill`/`context`. A `HarnessRef` in a spec is an immutable `version_id`; inline
strategy definitions are rejected.

**Alternative rejected — hard-code the five strategies as an in-code table.** Simpler, and five is not
many. Rejected on **L5 不可演进**: a registry Kind makes a strategy versioned, referenced by immutable id,
and resolvable back from a `config_hash` months later — the same lineage guarantee the other four Kinds
give and the whole reason the platform is ref-based. An in-code table cannot be pinned: the moment it
changes, a stored `config_hash` no longer resolves the strategy that produced it, and lineage breaks. The
`version_id` is hashed with its Kind, so a `harness` ref pasted into a non-harness dimension fails closed
instead of resolving — the same cross-Kind safety the existing registries rely on.

## Decision 3 — The no-harness case hashes byte-identically; the field is additive `omitempty`

`ResolvedNode.HarnessRef` is additive, `omitempty`, and **nil-when-empty**: a node that declares no
harness emits **no** harness key, so its canonical bytes are identical to a pre-P18 node.

**Alternative rejected — make `harness` always-present (like `skill_refs:[]` / `context_params:{}`).**
Consistent with the sibling fields. Rejected on **L2 稳定** and **L5**: the sibling fields predate the
golden and their emptiness is *part of* the frozen bytes; `harness` is new, so its **absence** is what must
be byte-compatible, and absence is achieved by omission, not by an empty value. Always-present would change
the golden bytes of **every** node in **every** existing config, breaking P0's frozen `config_hash` and
orphaning every keyed row. The `omitempty` + nil-when-empty shape is the *only* one that satisfies "changes
iff a harness changes" **and** "no-harness spec unchanged" simultaneously — precisely the reasoning
`resolved.go`'s D-1.4 applied to bindings, applied again here.

| Approach | Effect on a pre-P18 config's `config_hash` |
|---|---|
| Always-present `harness` value | **Changes** for every node. Every golden vector breaks; every keyed row orphans. |
| Additive `omitempty`, nil-when-empty | **Unchanged** — no key emitted. Golden vectors reproduce; identity preserved. |

## Decision 4 — A `HarnessRef` is refused at transform, never silently dropped

The per-dimension rewriter (`internal/transform/rewrite.go:54` + `rewrite_span.go:59`) returns a typed
`unsafeRewrite` (`edit.go:90`) for any resolved node — or node-group — carrying a `HarnessRef`, naming the
strategy and the reason, on both engines. It mirrors `refuseSkills` (`:388`) exactly.

**Alternative rejected — silently no-op the override until the codemod exists.** Least friction: the axis
"works" end-to-end and just does nothing at the call site for now. Rejected on **L1 安全** and **L2 稳定**,
and it is the single most dangerous option on the table: a silent drop lets the platform **score a variant
as if its scaffold changed when the emitted source did not** — a false result, which is the one thing an
eval platform must never produce. A typed refusal fails **visibly**, before a diff nobody can trust is
generated, and keeps "modelled but not yet applicable" a first-class, testable state rather than an
invisible lie. This is the repo's honest interim pattern (`refuseSkills`/`refuseContext`), and harness —
being strictly more structural than either — belongs squarely inside it. The refusal is the seam a later
phase lands in when the bounded-loop rewrite is proven safe, exactly as P3 lands in `refuseContext`.

## Decision 5 — Harness composes with P15 wiring; it never reorders

A harness may wrap a **single node** or an **ordered edge set** (a node-group / subgraph). The group form
consumes P15's wiring (`VariantSpec.Order`/`Edges`, `spec.go:255-258`); the wiring defines *what the edges
are*, the harness defines *what loop runs over them*. A harness override never reorders nodes.

**Alternative rejected — let a group harness re-derive its own edge set (an inferred subgraph).** More
convenient — name a strategy, let the harness figure out the scope. Rejected on **L5/L7 单一真相**: wiring
is P15's single source of truth for the edges, and a harness that re-derived them would be a second,
divergent definition that could drift from the wiring the executor actually walks. The M21 default is an
**explicit** ordered edge set on the override, auditable in the diff; inferred subgraph scoping waits until
P15's wiring identity is frozen (open question 2). The clean split is a boundary, not a limitation: reorder
is P15's operator (`OpReorder`), loop-over-edges is P18's — neither reaches into the other.

## Decision 6 — A heavier harness is admissible only when it earns its cost, on held-out data

`OpHarnessStrategy` admits a heavier scaffold (more turns) over a lighter one **only** when the measured
`task_success` gain outweighs its added `eval_cost_usd` and `eval_latency_ms`, computed on **held-out**
cases disjoint from any used to shape the proposal.

**Alternative rejected — admit any scaffold that raises `task_success`.** Simpler gate, and quality is the
headline metric. Rejected on **L1 honesty** and strategy: "it scored higher" ignores the turns it burned,
and a ten-turn loop almost always raises `task_success` *somewhere* while multiplying cost and latency.
Admitting a cost blow-up as a win produces a bill the customer never agreed to buy quality with — and the
platform's whole discipline is that a change ships on *verified* value, not a partial one. The **held-out**
framing is the second half: a win measured on the cases the proposal was tuned against is overfitting with
a confidence interval, so the admissibility set is disjoint by construction (no leak). The existing harness
supplies all three metrics unchanged (`metricnames.go`); the gate is arithmetic over them, not a new
measurement.

## Decision 7 — Autonomous turns run in the node's existing sandbox and grant; the surface is observable

Every multi-turn strategy declares a bounded `max_turns` and a stop condition; no strategy expresses an
unbounded loop. The added turns execute within the node's **existing** P3 sandbox and tool grant, reach no
egress destination or tool outside it, and the enlarged turn/tool-call surface is **observable** in the
trace.

**Alternative rejected — give an agent loop a broader grant "so it can do more."** Tempting, because more
autonomy is the point of a heavier scaffold. Rejected on **L1 安全**: more turns of autonomous tool-calling
already enlarge the blast radius (that is the honest cost of the axis), and widening the grant on top would
compound it — an agent loop that can suddenly reach a destination the single shot could not is a new,
unscoped attack surface. The turns stay inside the existing grant; bounded `max_turns` forecloses runaway
autonomy; and the enlarged surface is made **observable** rather than asserted away. The sales-honest word
is *observable*, never "controlled" or "风险可控" — the risk is real and the guarantee is that you can see
it, not that it is gone.

## Interfaces sketch

```
registry Kind "harness"                                   // registry.go:57 — fifth Kind, content-addressed
HarnessSpec = { name, params_schema{ max_turns?:int>0, stop_condition?, critic_model_ref?, retry_budget?:int>=0 } }
  builtins: single-shot | react-loop | plan-execute | reflexion | critic-loop   // critic-loop names a SEPARATE critic model ref

NodeOverride.HarnessRef  string `json:"harness_ref,omitempty"`      // spec.go:183 — sparse, ref-only, inline rejected
ResolvedNode.HarnessRef  string `json:"harness_ref,omitempty"`      // resolved.go:46 — auto-hashed, nil-when-empty
IRNode.DiscoveredHarness string `json:"discovered_harness,omitempty"` // emit.go:92 — additive; default single-shot

resolveNode:  override -> registry entry pinned by version_id ; absent -> discovered default pinned by source_revision
config_hash:  changes IFF harness changes ; no-harness == byte-identical to pre-P18

transform:    HarnessRef present  ->  unsafeRewrite(node|edge-set, DimHarness, "materializing a control loop is
                                       code generation, not an argument swap")   // refuse, NEVER drop

OpHarnessStrategy: propose swap -> P5.5 verify -> admit IFF  Δtask_success  >  cost(Δeval_cost_usd, Δeval_latency_ms)
                                                             on HELD-OUT cases (disjoint from tuning set)
scoring:      unchanged — eval harness scores by config_hash+Trace, no new metric
```

## Risks

| Risk | Mitigation |
|---|---|
| A harness override is silently dropped; a variant is scored as if its scaffold changed | Decision 4 — typed `unsafeRewrite`; a test asserts the override is **present-and-refused**, not absent. If that test cannot be made to fail on a silent drop, the honesty is decoration. |
| The additive field changes an existing config's `config_hash` | Decision 3 — `omitempty` + nil-when-empty; a golden test asserts byte-identical no-harness bytes. |
| The operator ships an expensive scaffold that won on quality alone | Decision 6 — cost/quality admissibility on held-out data; a cost-only win is rejected, and the held-out set is disjoint (no leak). |
| An autonomous loop becomes a new blast-radius surface | Decision 7 — turns run in the node's existing P3 grant; no new egress/tool scope; bounded `max_turns`; surface **observable** in the trace. |
| A group harness drifts from P15's wiring | Decision 5 — the harness composes with, never re-derives, the ordered edge set; it never reorders. |
| Scope creep into a runtime topology engine | Non-goal — the removed harness stays removed; only `critic-loop`'s pattern survives, as data, not resurrected runtime. |
| An unbounded or unresolvable strategy | Params validated at seal; `max_turns`/retry budget bounded; an unresolvable ref fails the resolve closed naming the ref. |
