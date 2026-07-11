# Design — P2: Configuration Layer + Runtime

Cross-reference: product rationale in [`../../../docs/prd/P2-config-runtime.md`](../../../docs/prd/P2-config-runtime.md).

## Context

P2 turns the inert static IR from P1 into something runnable **without regenerating source**. The
central constraint — *we cannot safely edit arbitrary third-party code* — forces the shim design:
discovered call sites are wrapped so parameters resolve from a config store at runtime. Everything
else follows from two backend realities the platform lives or dies on: **contracts outlive code**
(the Variant Spec and registry shapes are consumed by P2.5/P4/P5.5) and **partial failure is
normal** (provider calls fail mid-graph and get retried). This is also the first phase that spends
real money on provider calls, which is why idempotency and reproducibility are designed in, not
bolted on.

## Decision 1 — Shim resolves at invocation time, not at generation time

**Decision.** The shim reads the Variant Spec + registries and resolves a node's four dimensions
*when the node is about to execute*, not by generating a materialized config ahead of time.

**Why.** Resolution against immutable, content-addressed registry versions is a cheap indexed read
(< 100 ms for ≤ 200 nodes) and keeps a single source of truth. Materializing config early would
create a second artifact that can drift from the spec and would complicate `config_hash` stability.

**Alternative rejected.** Source rewriting / codegen — unsafe on third-party repos and impossible
to reproduce or diff cleanly. Precompiled config bundles — extra artifact, drift risk.

## Decision 2 — Four independent override dimensions with fallback-to-default

Each node has four dimensions — **model**, **prompt**, **skills**, **context** — overridable
*independently*. A Variant Spec entry may set only `model_ref` and leave the other three to the
IR-captured default. This keeps ablation (P4.5: change exactly one dimension of one node) a
first-class operation rather than requiring a full re-specification, and it makes the
configure-a-node UX legible ("this node overrides only its model").

## Decision 3 — Registries are immutable, content-addressed, git-like

**Decision.** Every published registry entry gets an immutable `version_id` derived from its
content hash. Editing an entry produces a *new* version; the old one is never mutated.

**Why.** Reproducibility is meaningless if a `model_ref` or `prompt_ref` can silently change under
an old Variant Spec. Immutable versions make a `config_hash` a durable pointer to an exact
configuration — the property P4's variant comparison and P5.5's proposals depend on. It also gives
git-like diffability for free (two versions, two hashes). Evolution is **expand-contract**: add
fields or versions additively; older specs keep resolving (FR10). This is the "contracts outlive
code" reality applied to data.

**Trade-off.** Storage grows monotonically with edits; acceptable because entries are small and
blobs are content-hashed (dedup identical bodies).

## Decision 4 — `config_hash` is derived from pinned versions + ordering, canonically

`config_hash = H(canonical(Variant Spec))` where the canonical form pins each `*_ref` to its
immutable `version_id`, is invariant to key ordering and whitespace, and includes the node
ordering. Consequences:
- The hash changes **iff** a referenced version or the ordering changes — the reproducibility
  contract.
- Prompt **rendering** is *not* part of the hash. We hash the template `version_id` + binding
  *values*; the rendered string is a derived, content-hashed blob in the object store. (Open
  question Q3 in the PRD — this is the proposed resolution.)

## Decision 5 — Provider gateway is the single seam; normalize shapes

**Decision.** All model calls go through one LiteLLM-style gateway exposing a provider-agnostic
`Complete(ModelEntry, Request, Seed) → Response`. The gateway normalizes request and response
shapes across Anthropic / OpenAI / Bedrock.

**Why.** Three payoffs converge on one boundary: (a) **provider-swap transparency** — changing a
node's provider is a `model_ref` change, no workflow code touched; (b) **one instrumentation
point** — P2.5 attaches OTel at the gateway and captures every call's cost/latency/tokens with
zero application effort; (c) **model tiering later** — the uniform interface lets P6 select a tier
per node without re-plumbing (AI Engineer lens). The gateway is also where **timeouts + bounded
backoff** and **secrets injection** live, so no caller can forget them.

**Trade-off.** A normalized shape is a lowest-common-denominator over provider features; provider-
specific params live inside `ModelEntry.params` and are passed through, so power isn't lost, only
uniformity is imposed at the envelope.

## Decision 6 — Idempotency key + DB uniqueness for exactly-once effects

Partial failure is the norm: a provider returns 500 mid-graph, the run queue redelivers, a node is
retried. Two mechanisms guarantee effects happen once:
- **Provider charge:** each node invocation carries an idempotency key
  `{run_id, node_id, attempt_group}`; the gateway reuses it across retries so a duplicated request
  is de-duplicated and billed once (FR17).
- **Run writes:** a unique constraint `(run_id, node_id, attempt_group)` turns a double-write race
  into a caught conflict — the DB enforces the invariant application code forgets (Backend lens:
  model invariants into the schema).

At-least-once queue dispatch + idempotent execution is preferred over exactly-once delivery
(simpler, and the idempotency machinery is needed anyway). (PRD Q4.)

## Decision 7 — Seed propagation defines reproducibility

Reproducibility is asserted at the level of **resolved configuration + seed propagation**, not
provider output bytes (providers don't guarantee bitwise determinism even at temperature 0). The
`seed` is threaded from the Variant Spec → gateway → every stochastic step. The tested claim: same
`{config_hash, seed}` → byte-identical `ResolvedConfig` and identical seed reaching each provider
call (FR16). This is honest about what we can guarantee (PRD Q2).

## Decision 8 — Executor enforces the typed I/O contract on every edge

The Executor walks the graph in the spec's declared ordering and passes each node's output through
the P0 typed I/O contract before it becomes a downstream input. On a violation it **halts** with a
typed error naming the node and dimension rather than propagating malformed data (FR15). P2 does
not reorder (that's P5) but it enforces the same contract that makes P5's re-arrangement safe —
building the invariant in from the first run rather than retrofitting it.

## Decision 9 — Context policy is an interface now, `full` is the only implementation

The `context_policy` field, its selection, validation, and the pluggable policy **interface** ship
in P2; only the `full` policy is implemented. P3 adds sliding-window / summarization / RAG /
compaction behind the same interface. Getting the interface surface right now (retrieval handles,
summarizer model refs as future params) avoids a breaking change in P3 (PRD Q5).

## Data model sketch

```
variant_spec(config_hash PK, spec_json, node_ordering, created_at)          -- unique config_hash
model_entry(version_id PK, name, provider, model_id, params_json)           -- immutable
prompt_entry(version_id PK, name, template_blob_hash, slots_json)           -- immutable
skill_entry(version_id PK, name, io_schema_json, impl_handle)               -- immutable
context_entry(version_id PK, name, policy_kind, params_json)                -- immutable
run(run_id PK, config_hash FK→variant_spec, seed, status, created_at)
node_execution(run_id FK, node_id, attempt_group, status,
               input_blob_hash, output_blob_hash, idempotency_key,
               UNIQUE(run_id, node_id, attempt_group))
```
Blobs (prompt bodies, rendered prompts, node inputs/outputs) live in the object store keyed by
content hash; DB rows hold only the hash.

## Key interfaces

```
Loader.Resolve(VariantSpec) -> (ResolvedConfig, error)   // fails closed on dangling ref
Gateway.Complete(ModelEntry, Request, Seed) -> Response   // normalized; timeout+backoff; idem key
Executor.Run(ResolvedConfig, Input, Seed) -> Run          // graph walk; contract checks per edge
Register<Kind>(entry) -> version_id                        // content-addressed, immutable
```

## Risks

- **Determinism ceiling** — providers may vary output even at fixed seed; we scope reproducibility
  to config + seed propagation (Decision 7). Mitigation: assert at that level; document the ceiling.
- **Gateway lowest-common-denominator** — mitigated by pass-through `params` (Decision 5).
- **Registry storage growth** — monotonic with edits; mitigated by content-dedup of blobs.
- **Context interface lock-in** — an interface too `full`-specific blocks P3; mitigated by
  co-designing the interface with the P3 policy authors (Decision 9).
