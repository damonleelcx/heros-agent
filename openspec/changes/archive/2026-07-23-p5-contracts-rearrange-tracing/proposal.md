## Why

After P4 a user can run any Variant Spec over a coverage-measured eval set and rank variants on a
CI-bounded leaderboard — but every Variant Spec so far is either the statically-discovered graph or a
per-node re-configuration of it. Two structural gaps remain, and the source plan flags both as the
ones **most likely to be underestimated**:

- **Re-arrangement is unsafe.** The naïve product ships "drag to reorder," but node B usually depends
  on B's parsing of A's output — arbitrary re-ordering is *not free*. Without enforcing a typed I/O
  contract, dragging a consumer ahead of its producer silently produces a workflow that fails at
  runtime or, worse, runs and emits garbage. The `io_contract` has been a mandatory-but-unused field
  on every IR node since **P0** precisely so this enforcement is cheap now rather than a ruinous
  retrofit.
- **The static graph is unvalidated against reality.** Static analysis produced a *candidate* graph;
  runtime-dynamic dispatch (loops with variable iteration counts, conditional routing, dispatch
  through wrappers) is invisible to it. And **P3.5** could only mark Reflection/Planning/Memory/HITL
  as *structural candidates with capped confidence*, deferring confirmation to P5, because you cannot
  tell a one-shot self-edge from an iterating loop without watching it run.

P5 has **four leads** because safe re-arrangement is simultaneously a **schema** problem (System
Designer), a **runtime** problem (Backend), a **UI** problem (Frontend), and a **UX** problem
(Product), with AI Engineer + DevOps in support. The load-bearing thesis: **no silently-broken
reorder** — an incoherent ordering is *flagged or an adapter is inserted*, never accepted as-is; and
the static IR is *confirmed* against a real run before it is trusted.

Depends on **P0** (`io_contract` per node, the `static_definition`↔runtime-invocation distinction,
the reserved additive `pattern_labels`, `config_hash`/lineage), **P2** (Runtime whose Executor
already passes node I/O through the typed contract and halts on violation — the runtime half of
static/runtime parity; reproducibility + idempotency), **P2.5** (OTel span substrate + GenAI semantic
conventions the interceptor extends), **P3** (sandbox + provider gateway for the instrumented run),
**P3.5** (structural pattern candidates awaiting behavioral confirmation; the pattern→metric-set
mapping), and **P4** (the eval-set generator's seed-from-real-traces interface to enrich, and the
harness that scores re-arranged variants).

## What Changes

- **New capability `typed-contracts`.** Enforces the P0 `io_contract` as an **ordering-coherence
  validator**: for every producer→consumer data edge, `Satisfies(output_schema, input_schema)` —
  the **same** structural-subtype predicate the P2 Executor enforces at runtime, so static validation
  and runtime enforcement never disagree. Each mismatch is classified **adaptable** or **incoherent**,
  and a mismatching edge is **never** admitted as coherent without either an adapter or a rejection.
  Where adaptable, the system **synthesizes an explicit adapter node** from a **typed adapter catalog**
  (field rename, projection, wrap/unwrap, default-fill, declared format coercion), inserts it on the
  edge, and represents it as an inspectable node carrying its own `io_contract` — no adapter that
  silently drops a consumer-required field. **No silently-broken reorder:** an incoherent, un-adaptable
  ordering is **rejected** with a typed diagnostic naming producer, consumer, and mismatching fields,
  and is never persisted as a runnable Variant Spec. The verdict is **pure, deterministic, and total**
  over all edges, and it runs **before any source transformation is generated** (ADR-001) — a rejected
  ordering yields no codemod, diff, or PR. A coherent (possibly adapter-augmented) ordering yields a new
  Variant Spec + new `config_hash` **and** a **deterministic, AST-level source transformation (codemod)**
  that rewrites the affected call sites / node wiring to match the spec, delivered as a **reviewable
  diff/PR**. The transform is **build-preserving** — a codemod that fails to build the target is rejected
  before it is ever proposed, so there is **no silently-broken diff** — applied to an **isolated
  worktree/branch** (never the user's tree in place), revertible by a single `git revert`. Where a
  mismatch is adaptable, adapter auto-insertion is itself a **generated code change** (the adapter node's
  code is inserted and the call sites rewired), never a hidden runtime coercion.
- **New capability `rearrangement`.** An **interactive graph editor** exposes the IR; users
  **add/remove/reorder/swap** nodes to produce a **candidate** new Variant Spec, validated through
  `typed-contracts` **before commit** — never silently committed broken. **The unhappy path is
  first-class:** an invalid reordering is **legible** — the contract mismatch is attached to the
  offending edge (both nodes + the specific fields), the auto-inserted adapter — and the **source change
  it would generate** — is **previewed** when the mismatch is adaptable, and the breakage is
  **explained in plain language** when it is not; an invalid reorder is legible in the UI as a **rejected
  or adapted diff that is never applied** (ADR-001). The
  editor is **fully keyboard-operable**, screen-reader-announces the validation state
  (coherent / adapter-inserted / rejected), and stays **responsive on large IRs** (virtualized canvas,
  incremental per-edge re-validation). A committed edit produces a new Variant Spec with lineage +
  diff for P4 comparison **and** a **reviewable source diff (an AST-level codemod that rewrites node
  wiring)** that must **build** before it is proposed and is applied on an isolated worktree/branch,
  never the user's working tree in place.
- **New capability `dynamic-tracing`.** An **OTel-style interceptor** wraps the signature-registry SDK
  entrypoints and logs **every real LLM call, its inputs, and its call stack**, tagged with the P0 tag
  set — **passive and async** (never alters the run's outputs; a logging failure never fails the run;
  secrets/PII redacted, inputs content-hashed). A **reconciler** matches each observed call to a static
  candidate, marks candidates **confirmed/unconfirmed** and observed calls **matched/runtime-only**,
  surfaces a **runtime-only edge static analysis missed** and adds it additively, and maps one
  **static definition** to its **many runtime invocations** (`invocation_index`), resolving loops and
  conditional dispatch. **Behavioral pattern confirmation** upgrades P3.5 structural candidates to
  **confirmed** labels from trace evidence — iteration count > 1 → Reflection; a consumed planning list
  → Planning; sample-N-then-vote → Self-Consistency; memory R/W between turns → Memory Management; a
  human-approval pause → HITL — each wiring the pattern → metric-set / failure-taxonomy /
  eval-targeting. **Anti-pattern detection** emits typed diagnoses (never-improving reflection loop;
  router sending everything one way; parallelization with no real independence; plan never followed)
  for P5.5. **Eval-set enrichment:** real trace inputs become **seed cases** for the P4 generator, and
  **per-path targeting** covers reconciled runtime-only edges and loop iteration bounds.
- **IR write-back (additive).** Reconciled runtime edges/nodes and confirmed behavioral labels are
  written back to the IR **additively** (same `ir_version` MAJOR); a pre-P5 consumer still parses a
  reconciled, behaviorally-labeled IR. Node `io_contract` schemas MAY be **refined** from observed
  trace shapes additively (tightening coherence without a schema-version break).
- **Deferred:** change operators + proposal generation + held-out verification gate (**P5.5**);
  automated search over orderings / autonomous optimizer (**P6**); LLM-authored, free-form adapter
  *transform code* beyond the fixed, deterministic catalog codemods (out of scope — would need sandboxing
  + P5.5 verification); autonomous **merge** of a generated PR without human approval (**P6**); new
  metric-set *definitions* (P5 wires existing ones).

## Impact

- **Affected capabilities:** `typed-contracts` (new), `rearrangement` (new), `dynamic-tracing` (new).
  Consumes `workflow-ir` + `io_contract` + the `static_definition`↔invocation distinction + reserved
  `pattern_labels` (P0), the Runtime Executor's runtime contract enforcement + reproducibility/
  idempotency (P2), the OTel span substrate + tag set (P2.5), the sandbox + provider gateway (P3), the
  structural pattern candidates + pattern→metric-set mapping (P3.5), and the eval-set generator's seed
  interface + coverage machinery (P4).
- **Affected code/systems:** ordering-coherence validator (`Satisfies` + `ValidateOrdering`, shared
  with the P2 Executor's runtime check), typed adapter catalog + adapter inserter (emitting adapter code
  as a codemod), the **source-transformation engine** that turns a coherent Variant Spec into a
  deterministic AST-level codemod / reviewable diff with a build-preserving gate on an isolated worktree
  (ADR-001), React interactive
  graph editor with invalid-state/adapter-preview/diff-preview UX (keyboard + a11y + virtualized canvas), OTel-style
  interceptor extending the P2.5 substrate, reconciler (confirmed/unconfirmed/runtime-only + static-def
  ↔ invocation mapping), behavioral pattern classifier (rules-first over trace signatures + constrained
  LLM residue) + anti-pattern detectors, eval-set seed-from-traces + per-path targeting wired into the
  P4 generator, and Postgres schema (variant-spec lineage + inserted-adapter list, reconciliation
  reports, behavioral labels, anti-pattern diagnoses) + object store (content-hashed call inputs,
  stacks, adapter defs, reconciliation reports).
- **Dependencies:** requires **P0**, **P2**, **P2.5**, **P3**, **P3.5**, **P4**. Unblocks **P4.5**
  (reconciled static↔runtime mapping → per-invocation attribution; confirmed labels → failure-taxonomy
  scoping), **P5.5** (anti-pattern diagnoses → change operators; the ordering-coherence validator →
  legal-move check on proposed reorders), and **P6** (the validator is the optimizer's legal-move
  generator for search over orderings; trace-seeded eval cases are the living memory).
