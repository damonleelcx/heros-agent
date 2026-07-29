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

---

# Addendum — the harness runtime, the call-site rewriter, and the authored change

Decisions 1–7 above described an axis that is **modelled, hashed and refused**. Two requirements arrived
after them: a user must be able to make an **active change** to their harness strategy, and the axis must
have a **runtime and a call-site rewriter** so that change can reach source. The decisions below add
those. Contracts of record: [`decisions.md`](decisions.md) D-8 … D-13.

## Context (what changes, and what deliberately does not)

The refusal of Decision 4 named exactly two missing artifacts: *a harness runtime (a bounded loop, a stop
condition, and a continuation rule) plus the call-site rewriter that drives it.* This addendum builds both
and **narrows the refusal per cell** — never wholesale. `config_hash` is untouched: what changes is what
the transform *emits* and what a surface *offers*, never what a configuration *is*.

## Decision 8 — The resolved field is a projection `{strategy, params}`, not the `version_id`

Tasks 3.4 and the Decision 3 sketch both wrote `ResolvedNode.HarnessRef string` — a registry id in the
hashed projection.

**Alternative rejected — keep the `version_id` as written.** It is what the task says, and it is one
fewer type. Rejected on **L5 evolvability** and single-source-of-truth: `resolved.go` freezes the rule
that `config_hash` denotes a *configuration*, not a set of registry rows, so two entries spelling one
strategy with one params set must share a hash. A `version_id` in the projection forks a configuration
per entry, permanently, and a hash is not revisable once rows key on it. The field becomes
`Harness *ResolvedHarness{Strategy, Params}`, the shape `ResolvedMemory` already proved. See
[`decisions.md`](decisions.md) D-8.

## Decision 9 — DRIVE AND DECIDE, or refuse

A loop is two capabilities: **driving** the call again, and **deciding** whether to. A materialization
emits both or refuses the call site whole, naming the missing half.

**Alternative rejected — emit the drive half alone, with `max_turns` as the universal stop.** It covers
every strategy in every language immediately, and it compiles. Rejected on **L1 safety**: a fixed-N loop
under `reflexion`'s name is `single-shot` run N times and priced N times, reported under a `config_hash`
that claims a self-correcting scaffold. That is the harness form of *"a memory that recalls from a store
nothing fills"* — the failure the sibling phase's D2 exists to forbid — and the fact that it is cheap to
build is an L8 argument, which never outranks L1.

**What each half needs, and therefore where each is available:**

| Half | Needs | Python | Go |
|---|---|---|---|
| **Drive** — re-invoke the call | a re-evaluable call expression, and a place to put the result | yes | yes (generic over the SDK's type, no import) |
| **Decide** — evaluate the stop condition | read the response's **text** | yes — a message is a `dict` | **no** — a message is the customer's SDK type |

So Go materializes exactly the identity (`single-shot`) and refuses the rest with `CauseNotAtCallSite` —
the same permanent asymmetry the memory materializer carries, for the same reason, and it carries **no
missing artifact** because there is nothing to build.

## Decision 10 — The generated runtime makes no provider call and dispatches no tool

`react-loop` wants the customer's tool executor; `plan-execute` wants a planner turn and a step executor;
`critic-loop` wants a call to a separate critic model. The artifact performs none of them: they are
**host services**, injected by a caller that has one, and a strategy whose service is absent **refuses**
rather than degrading.

**Alternative rejected — have the artifact reuse the client already at the call site for the critic
turn.** The client is right there; it would work today. Rejected on **L1 safety**: a generated file that
calls a provider puts a **credential in the customer's process** and spends it on turns the author never
wrote — a new egress surface created by a codemod, which is precisely what the standing L1 note forbids
compounding. Rejected again on **L2 stability**: the critic is a different model entry, so its failure
fails a call the author cannot see or retry.

The consequence is stated rather than hidden: **at a call site there is no injection point**, so three of
the five strategies refuse there by name. `single-shot` and `reflexion` need no host service — reflexion's
"self-critique" is a *second turn of the same call*, with the prior answer and a declared reflection
instruction appended — so those two are what Python materializes.

## Decision 11 — The refusal is narrowed per cell; `single-shot` is the identity

`harnessCoverage` stops being uniform and becomes a **read of the materializer table**, per
`(language, strategy, call-shape)`. Covered cells materialize; every other cell still returns a typed
`unsafeRewrite` with its own cause, and Decision 4's totality canary still passes for them.

**Alternative rejected — one flat verdict per axis.** One sentence, one surface, no table. Rejected on
**L3 user complexity** and L1 honesty: after a rewriter lands, a flat "refused" tells a covered user to
wait for work already done, and a flat "supported" tells an uncovered user it works. Both are the same
defect — a coverage claim that does not match the engine — and only a per-cell read is wrong in neither
direction.

## Decision 12 — The authored change rides the existing override, with no second apply path

A user selects a strategy from the closed builtin set, supplies schema-valid params, and may clear it;
the change is expressed **solely** through `NodeOverride.HarnessRef`, resolves through the same resolver,
transforms through the same rewriter, and passes the same admissibility gate an `OpHarnessStrategy`
proposal passes.

**Alternative rejected — an authoring-only apply path that skips the admissibility gate.** The user chose
it, so why gate it. Rejected on **L1 honesty** and **L5 evolvability**: the gate exists because the cost
of a heavier scaffold is invisible until it runs, and a second apply path is a second definition of what
"shipped" means — two truths about one configuration, which cannot be un-forked once surfaces depend on
both. Origin is **recorded and never hashed**: a user-authored configuration and an operator-proposed one
that spell the same strategy are byte-identical downstream.

## Interfaces sketch (addendum)

```
internal/harnessruntime
  Decision   = { Continue bool, Reason StopReason }        // StopReason: ceiling | satisfied | strategy-terminal
  Plan(strategy, params, turn, answerText) Decision        // ONE definition of a strategy's loop behaviour
  Run(cfg, invoke) (Result, error)                         // bounded by construction: turn <= max_turns, always
  Result     = { Answer, Turns int, Stop StopReason, Trace []TurnRecord }   // observable, never hashed
  HostService: ToolInvoker | Planner | Critic              // NOT bound at a call site -> the strategy refuses

generated artifact (per language, dependency-free, byte-identically regenerated)
  agentharness.run(node_id, invoke, messages, params)      // params READ AS DATA from the binding document

python call site
  resp = client.messages.create(model=M, messages=msgs)
    ->  resp = agentharness.run("node", lambda _m: client.messages.create(model=M, messages=_m), msgs)
        # ONE edit, one line, no newline introduced; the written message argument becomes the loop's parameter

go call site
  single-shot only (identity: nothing emitted). Multi-turn refuses: CauseNotAtCallSite, no missing artifact.

coverage      harnessCoverage(language) reads the materializer table, per (language, strategy) cell
config_hash   UNCHANGED — this addendum changes what is EMITTED, never what a configuration IS
```

## Risks (addendum)

| Risk | Mitigation |
|---|---|
| A half-materialized loop ships (drive without decide) | Decision 9 — both halves computed **before** any edit is emitted; a test asserts the refusal names the missing half. |
| A generated file reaches a provider or dispatches a tool | Decision 10 — host services are injected, never called from the artifact; a test asserts the emitted module imports nothing outside the standard library. |
| A coverage sentence drifts from the engine after the rewriter lands | Decision 11 — coverage is a read of the dispatch table, not a second table. |
| An authored change is presented as a win | Decision 12 — `unverified` by construction; refused cells surface as **refused-not-scored**; no metric is attributed. |
| The runtime's trace leaks into identity | [`decisions.md`](decisions.md) D-13 — the trace is a property of a run; every 18a hash reproduces bit-for-bit. |
