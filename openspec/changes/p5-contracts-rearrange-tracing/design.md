# Design — P5: Typed I/O contracts + Re-arrangement + Dynamic tracing + Behavioral classification

Cross-reference: product rationale in [`../../../docs/prd/P5-contracts-rearrange-tracing.md`](../../../docs/prd/P5-contracts-rearrange-tracing.md).

## Context

P5 closes the biggest gap in the naïve design: safe re-arrangement, and validation of the static
graph against a real run. Three forces shape every decision. Re-arrangement is **not free** — node B
usually parses A's output, so an arbitrary reorder can produce a graph that fails at runtime or, worse,
runs and emits garbage; the fix is enforcing the typed `io_contract` P0 reserved on every node. Static
analysis produces a **candidate** graph — loops, conditional routing, and wrapper dispatch are
invisible to it, so it must be confirmed by instrumenting a run. And P3.5 could only mark
Reflection/Planning/Memory/HITL as **capped-confidence structural candidates**, because you cannot
distinguish a one-shot self-edge from an iterating loop without watching it run. The phase reuses
machinery already built: the P0 `io_contract` and static-def↔invocation distinction, the P2 Executor's
*runtime* contract enforcement, the P2.5 OTel substrate, the P3 sandbox, the P3.5 pattern candidates +
metric-set mapping, and the P4 generator's seed interface. Four leads, one thesis: **no
silently-broken reorder**.

## Decision 1 — One schema-satisfaction predicate, shared by validator and runtime

**Decision.** Ordering coherence is decided by a single predicate `Satisfies(output_schema,
input_schema) → {ok | mismatch(fields)}`: structural subtyping — every field the consumer *requires*
must be present in and type-compatible with the producer's output; extra producer fields are
permitted. `ValidateOrdering(ir, ordering, catalog)` applies it to **every** producer→consumer data
edge and returns `{coherent | adapted(adapters) | rejected(diagnostics)}`. This is the **same**
predicate the P2 Executor already uses to pass node I/O through the typed contract and halt on a
violation.

**Why.** If the static validator used a different rule than the runtime Executor, a Variant Spec could
pass validation and then halt at runtime (or vice versa) — the "validator says yes, runtime says no"
class of bug. Defining `Satisfies` once, over the schemas P0 froze, and sharing it between the P5
validator and the P2 Executor makes **static/runtime parity** a structural guarantee, not a hope. The
tested claim: a validator-accepted reorder runs end-to-end with **no** contract halt; a rejected one is
not runnable.

**Alternative rejected.** A separate, looser static "lint" that warns but lets you save — this is
exactly "drag to reorder silently produces broken workflows," the failure P5 exists to prevent.

## Decision 2 — Coherent / adaptable / incoherent is total; there is no escape hatch

**Decision.** Every mismatching edge is classified **adaptable** (bridgeable by a catalog adapter) or
**incoherent** (not bridgeable). Together with **coherent**, this partitions the space with **no
"unknown" bucket**. An incoherent, un-adaptable ordering is **rejected** — a typed diagnostic names the
producer, the consumer, and the specific mismatching fields — and is **never persisted as a runnable
Variant Spec**.

**Why.** The load-bearing correctness property is *no silently-broken reorder*. That requires the
verdict to be **total**: if any edge could fall into an "unclassified/allowed anyway" state, a broken
graph leaks through. Making rejection a fail-closed outcome (like the P2 Loader aborting on a dangling
ref) means the only ways forward are *coherent as-is*, *coherent via an explicit adapter*, or *blocked
with a reason*. Tested with a known-incoherent reorder (consumer precedes its data producer): the
verdict is **rejected**, never coherent.

## Decision 3 — Adapters come from a typed catalog and are validated like any node

**Decision.** Adapters are drawn from a **fixed, versioned catalog** — field rename, projection,
wrap/unwrap, default-fill, declared format coercion. An inserted adapter is an **explicit, inspectable
node** carrying its own `io_contract`; it is validated (its input satisfied by the upstream producer,
its output satisfying the downstream consumer) exactly like a discovered node. No adapter that would
**silently drop a field the consumer requires**, or lose data, is inserted without flagging the loss.

**Why.** An auto-inserted adapter is a real change to data flow — hiding it would reintroduce the
silent-breakage problem one layer down. Making it an explicit node with its own contract means the
system never trusts a transform it hasn't type-checked, and the UI can preview exactly what it inserts.
Restricting to a **typed catalog** (not LLM-authored transform code) keeps adapters analyzable and
avoids executing arbitrary generated code — that would need P3 sandboxing and P5.5 verification and is
explicitly out of scope.

**Trade-off.** A mismatch the catalog can't express is **incoherent** (rejected), not
best-effort-coerced. This is deliberate: better to block a reorder than to silently coerce data in a
way the user didn't sanction. The catalog can grow additively.

## Decision 4 — The interceptor is passive; static analysis produced a candidate, tracing confirms it

**Decision.** Dynamic tracing wraps the signature-registry SDK entrypoints with an **OTel-style
interceptor** that logs **every real LLM call, its inputs, and its call stack**, tagged with the P0 tag
set and correlated to a P2.5 span. The interceptor is **passive and async**: it never alters the traced
workflow's outputs, logging is best-effort so a logging failure never fails the run, and secrets/PII
are redacted (inputs are content-hashed blobs, secrets from the manager only). The **reconciler** then
matches observed calls to static candidates: each candidate is **confirmed** (observed) or
**unconfirmed** (not seen on this run); each observed call is **matched** or **runtime-only**; a
runtime-only edge/node static analysis missed is surfaced and added to the IR **additively**; and one
**static definition** maps to its **many runtime invocations** (`invocation_index`), so a loop is one
definition with n invocations, never n definitions.

**Why.** The premise correction from the source plan: "how many nodes make LLM requests" is only
well-defined for static call sites; agents with loops make a *variable* number of runtime requests. The
IR distinguishes static definitions from runtime invocations (P0) precisely so tracing can confirm the
static graph and resolve dynamic dispatch concretely. The interceptor **must** be passive — if
instrumentation changes the run, the evidence is worthless (assert identical outputs traced vs.
untraced). Additive reconciliation (mark unobserved candidates *unconfirmed*, don't delete them) means
a path this input didn't exercise is not mistaken for a dead node.

**Alternative rejected.** Trusting the static graph alone — it silently omits runtime-only edges
(conditional branches, loop-backs) and can't tell a one-shot self-edge from an iterating loop.

## Decision 5 — Behavioral confirmation is rules-first over trace signatures; anti-patterns are diagnoses

**Decision.** Behavioral pattern confirmation runs **deterministic rules over trace signatures** —
iteration count > 1 on a self-edge → **Reflection**; a planning node's task list consumed downstream →
**Planning**; sampling one node N times then voting → **Self-Consistency (Reasoning Techniques)**;
memory read/write against a store between turns → **Memory Management**; a human-approval pause →
**Human-in-the-Loop** — upgrading a P3.5 structural candidate to a **confirmed** label
(`source = behavioral`) and selecting the pattern's metric-set / failure-taxonomy / eval-targeting. An
LLM-as-classifier handles only the ambiguous residue, constrained to the fixed 20-pattern taxonomy.
**Anti-patterns** fall out as typed **diagnoses** (never-improving reflection loop; router sending
everything one way; parallelization with no real independence; plan never followed) — evidence
attached, consumable by P5.5.

**Why.** Same discipline as the P3.5 structural detectors and the P4.5 diagnosis engine: **rules
first** (fast, deterministic, cheap), LLM for the fuzzy residue, never unverified. Topology could only
*guess* these patterns; the trace *confirms* them — a self-edge that iterates once is **not** Reflection
and must get no convergence metrics. Anti-patterns are **surfaced, not fixed**: P5 emits the diagnosis;
P5.5's change operators propose a fix and **verification decides**. Diagnosis proposes; verification
decides — the platform's spine.

**Trade-off.** Confirmation needs enough runtime evidence (how many iterations/samples/turns, across
how many cases) to be reliable — left as an open question (thresholds), defaulting to rate-across-cases
rather than a single observation.

## Decision 6 — The editor treats the validator's verdict as truth; invalid is a first-class state

**Decision.** The graph editor produces a **candidate** Variant Spec on every add/remove/reorder/swap
and validates it through `typed-contracts` **before commit**. The states — loading / valid /
**adapter-inserted** / **rejected** — are modeled explicitly; the editor renders the validator's verdict
and **never commits a broken graph**. The **rejected** state is attached to the specific offending edge
(both nodes + the mismatching fields), the **adapter-inserted** state previews the adapter and requires
accept/reject, and both are keyboard-operable and screen-reader-announced. Only affected edges are
re-validated per edit, on a virtualized canvas, so large IRs stay responsive.

**Why.** This is the Product thesis — *design the unhappy path first*. The primary artifact is the
invalid reorder, made legible: which two nodes, which fields, what breaks. Modeling invalid as a
first-class state (not a global error toast) is what makes "drag to reorder" safe by construction.
Accessibility and performance are **requirements, not polish** (the Frontend playbook): the whole
interaction must work by keyboard on a hundreds-of-node graph, or it isn't done.

**Trade-off.** Re-validating on every edit costs computation; mitigated by incremental per-edge
re-validation (< 200 ms perceived) rather than whole-graph re-validation.

## Decision 7 — IR write-back and schema refinement are additive

**Decision.** Reconciled runtime edges/nodes, confirmed behavioral labels, and any `io_contract` schema
**refined from observed trace shapes** are written back to the IR **additively** — same `ir_version`
MAJOR, so a pre-P5 consumer still parses the enriched IR (the P0 additive-evolution rule). Refinement
*tightens* a permissive early schema (`{"type":"object"}`) toward the observed shape without a
schema-version break.

**Why.** P0 mandated additive evolution precisely so later phases can enrich the IR without breaking
earlier consumers. Refining permissive schemas from real traces is what closes Decision 1's known gap:
a permissive schema admits more orderings as "coherent" than a strict one would, so tracing feeds the
contract and coherence tightens over time. Additivity keeps this safe.

**Trade-off.** Refinement could *retroactively* make a previously-"coherent" ordering incoherent as the
schema tightens — surfaced (which nodes still carry permissive schemas; which orderings a refinement
would affect), never silent (Q3).

## Data model sketch

```
variant_spec(variant_id PK, config_hash, parent_variant_id FK NULL,   -- lineage from an edit
             ordering_json, created_at)
inserted_adapter(adapter_id PK, variant_id FK, from_node_id, to_node_id,
                 catalog_kind ENUM('rename','projection','wrap','unwrap','default_fill','coerce'),
                 io_contract_hash, params_json)          -- explicit, inspectable, validated node
reconciliation(run_id PK, config_hash, ir_ref, report_blob_hash, created_at)
recon_node(run_id FK, node_id, status ENUM('confirmed','unconfirmed'))
recon_call(run_id FK, observed_call_id, node_id NULL, status ENUM('matched','runtime_only'),
           invocation_index INT, inputs_blob_hash, stack_blob_hash)   -- static-def ↔ invocations
recon_edge(run_id FK, from_node_id, to_node_id, origin ENUM('static','runtime_only'))
behavioral_label(subgraph_ref, pattern, source='behavioral', confidence, evidence_blob_hash,
                 taxonomy_version, PRIMARY KEY(subgraph_ref, pattern))
anti_pattern(id PK, subgraph_ref, kind ENUM('reflection_no_improve','router_one_way',
             'parallel_no_independence','plan_not_followed'), evidence_blob_hash)
-- io_contract schemas live in the IR (P0); P5 reads them and writes refined schemas back additively
```
Logged call inputs, call stacks, adapter definitions, and reconciliation reports live in the object
store keyed by content hash; DB rows hold only the hash and the P0 tag set.

## Key interfaces

```
Satisfies(output_schema, input_schema) -> {ok | mismatch(fields)}   // shared with the P2 Executor
ValidateOrdering(ir, ordering, catalog) -> {coherent | adapted(adapters) | rejected(diagnostics)}
                                                                    // pure, deterministic, TOTAL
Adapter{kind, in_schema, out_schema, apply}                         // fixed catalog; own io_contract
Editor.Commit(edit) -> VariantSpec | InvalidState(mismatch)         // never commits broken
Interceptor.wrap(entrypoint) -> logs {call, inputs_hash, stack, tags}   // passive, async, redacted
Reconcile(ir, trace) -> ReconciliationReport{confirmed[], unconfirmed[], runtime_only[], invocations}
ConfirmBehavioral(ir, trace) -> {labels[], anti_patterns[]}         // rules-first; wires metric-set
SeedFromTraces(trace) -> []EvalCaseSeed                             // feeds the P4 generator
PerPathTargets(reconciled_ir) -> []PathTarget                      // incl. runtime-only edges, loops
```

## Risks

- **Drag-to-reorder ships a broken graph** — mitigated by a total coherence verdict + fail-closed
  rejection that is never persisted as runnable (Decisions 1, 2); tested with a known-incoherent reorder.
- **Validator accepts an ordering the runtime halts on** — mitigated by sharing `Satisfies` with the
  P2 Executor (Decision 1); tested end-to-end (validator-accepted reorder → no contract halt).
- **Adapter silently drops a required field / loses data** — mitigated by typed-catalog adapters that
  carry + validate their own `io_contract` (Decision 3).
- **Interceptor alters the run → invalid evidence** — mitigated by a passive, async, best-effort
  interceptor; assert identical outputs traced vs. untraced (Decision 4).
- **Runtime-only edge missed / loop mistaken for n nodes** — mitigated by additive reconciliation +
  static-def↔invocation mapping (Decision 4).
- **One-shot self-edge mislabeled Reflection** — mitigated by requiring iteration count > 1 from the
  trace (Decision 5).
- **Anti-pattern treated as a verdict / auto-fixed** — mitigated by emitting typed diagnoses for P5.5;
  verification decides (Decision 5).
- **Enriched IR breaks pre-P5 consumers** — mitigated by additive write-back at the same `ir_version`
  MAJOR (Decision 7).
- **Refinement retroactively invalidates a saved ordering** — surfaced (which nodes are permissive;
  what a refinement affects), never silent (Decision 7, Q3).
- **Secrets/PII in trace logs or stacks** — mitigated by sandbox + content-hashed inputs + secrets from
  the manager only (Decision 4, DevOps).
