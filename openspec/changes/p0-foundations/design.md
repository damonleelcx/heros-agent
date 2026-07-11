# Design — P0 Foundations

Technical decisions and trade-offs behind the P0 contracts. Product rationale lives in
`docs/prd/P0-foundations.md`; this file records *why the design is the way it is* and what was rejected.

## Context

P0 has no upstream dependency; it is the root of the critical path (M0). It ships contracts, a lineage
scheme, a storage decision, and scaffolding — not running services. The two decisions with the largest
downstream blast radius are the **metric tagging contract** and the **typed per-node I/O contract**;
both are designed in full here because both are cheap now and ruinous to retrofit.

## Decision 1 — Static definitions vs. runtime invocations are distinct types

**Decision.** The IR models a **static node definition** (a call site in the source) as the node; a
**runtime invocation** is a separate concept referencing a definition's `node_id`. Node count is
reported per definition; a definition whose runtime fan-out is variable (loop / conditional agent) is
flagged `invocation_semantics.variable_at_runtime = true` rather than expanded into many nodes.

**Why.** "How many nodes make LLM requests" is only well-defined for static call sites; an agent loop
makes a *variable* number of requests at runtime. Conflating the two makes node count meaningless and
makes the graph un-diffable across runs. Keeping them separate lets Discovery (P1) report a stable
per-definition count and lets Metrics (P2.5) attribute a variable number of invocations back to one
definition via `node_id`.

**Rejected.** Expanding loops into N runtime nodes in the IR — produces an unbounded, run-dependent
graph that cannot be diffed or configured.

## Decision 2 — Typed I/O contract is a required field from v1

**Decision.** Every node carries `io_contract: { input_schema, output_schema }`, each a JSON Schema
(2020-12). The field is **required** in IR v1, three phases before re-arrangement (P5) uses it.

**Why.** The reorder validator (P5), schema-driven eval-set synthesis (P4), and output-contract
adherence metrics (P4) all read these schemas. If the field is added in P5, every IR emitted by P1–P4
lacks it and needs a backfill migration — the exact "underestimated" cost the source plan warns about.
Making it required now costs Discovery a partial inference it is *allowed to make permissive*.

**Trade-off accepted.** In P1, static analysis often cannot fully infer a node's input/output shape,
so early schemas may be permissive (`{}` / `"type": "object"` / `any`). Precision improves once dynamic
tracing (P5) observes real I/O. The **field's presence** is the contract; its **precision** is
allowed to increase over time — no schema-version change required because it is additive refinement.

**Rejected.** Optional `io_contract` — makes it skippable, and "skippable" means "skipped," so the
data isn't there when P5 needs it.

## Decision 3 — The seven-tag contract, enforced at the DB, not by convention

**Decision.** Every event carries `{variant_id, run_id, node_id, case_id, seed, timestamp,
config_hash}`, all non-null. Enforcement is layered: rejected at the emission boundary *and*
constrained non-null in Postgres.

**Why.** The top failure mode named in the source plan is "under-tagged metrics you can't later slice."
A tag is only trustworthy if it *cannot* be null — a constraint is a test that runs on every write
forever, whereas a code-review comment is a test that runs once. Each tag answers a specific downstream
question: `variant_id`/`config_hash` → "which configuration"; `node_id` → per-node attribution;
`case_id` → per-case / per-failure-cluster slicing; `seed` → multi-seed confidence intervals;
`run_id` → grouping an execution batch; `timestamp` → trend over time.

**Rejected.** Emit-time-only validation — application code eventually forgets; the DB does not.

## Decision 4 — config_hash canonicalization excludes run-time values

**Decision.** `config_hash = SHA-256(canonical_json(resolved_config))`. `resolved_config` includes
`ir_version`, each node's resolved `{model_ref@version, prompt_ref@version, skill_refs[]@version,
context_policy + params}`, the node ordering/graph, and provider inference params. It **excludes**
`run_id`, `seed`, and `timestamp`. Canonicalization = sorted object keys, normalized number
representation, UTF-8, no insignificant whitespace.

**Why.** Reproducibility is *exact-config replay*: the same configuration under different seeds must
share one `config_hash` (so multi-seed runs roll up under one configuration), while any change to a
bound registry version must change the hash (so "B beat A" compares two exact configs). Including a
timestamp or seed would fragment identical configs into many hashes and destroy attribution.

`variant_id` is a stable *logical* label that may map to many `config_hash` values across its edit
history; a `config_hash` is immutable and content-defined. This separation lets the UI track "variant
3 over time" while each concrete run is pinned to an immutable, replayable hash.

**Rejected.** Hashing the raw serialized spec without canonicalization — key ordering / whitespace /
number formatting differences would produce different hashes for identical configs.

## Decision 5 — Three stores by shape + content-hashed blobs

**Decision.** Route by data shape, all keyed by `config_hash`:

| Data | Store | Reason |
|---|---|---|
| Spans | OTel-compatible span store (Tempo/Jaeger) | Per-run drill-down; trace-native; sampled + retention-bounded. |
| Metrics | TSDB (Prometheus/ClickHouse) | ~10⁷ tiny numeric points/run; aggregation & trend queries; columnar/compressed. |
| Eval results | Postgres | Low volume (~2×10⁵ rows/run); rich relational joins; constraints enforce tags. |
| Blobs (prompts/artifacts) | Object store, content-hashed (SHA-256 of bytes) | Large; ~5–10× dedup; reproducibility by reference. |

**Why (numbers).** A representative optimization run = 20 variants × 200 cases × 5 seeds × ~50
invocations/case = **10⁶ invocations** → ~**10⁷ metric events**, ~**3×10⁶ spans**, ~**2×10⁵ eval
rows**, ~**10⁶ blob writes** (dedup → ~0.6–1 GB). The shapes are too different for one store: metrics
are huge-count/tiny-value/aggregation-queried; spans are huge/drill-down/sampled; eval results are
small/relational/joined.

**Cardinality is the load-bearing sub-decision.** Active TSDB series ≈ variant(20) × node(20) ×
metric_name(~15) × seed(5) ≈ **3×10⁴ series/run** — fine. But `case_id` (200) and especially
`run_id`/`invocation_id` (10⁶) must **not** be TSDB series labels or cardinality explodes to 10⁸;
they live as span attributes and Postgres columns. This is precisely *why* metrics ≠ Postgres and
high-cardinality identifiers ≠ TSDB labels.

**Rejected.** One store (Postgres) for everything — 10⁷ metric events/run makes trend queries and
cardinality unmanageable; forcing spans into relational rows loses trace drill-down.

## Decision 6 — Schema versioning = additive / expand-migrate-contract

**Decision.** Both schemas carry an explicit version (semver). Evolution is additive: new fields are
optional and do not bump MAJOR; a breaking change bumps MAJOR. Registry-backed refs are versioned so a
`config_hash` always resolves exact bytes. Schema/DB changes follow expand-migrate-contract (add
nullable/optional → dual-write/backfill → drop old) so older variants keep resolving throughout.

**Why.** "Contracts outlive code": the IR and event schema are public contracts consumed by six
subsystems. Additive-only evolution + a MAJOR-bump rule for breaks is what lets a consumer written at
MAJOR *n* keep validating documents with added optional fields. The M0 freeze + CI sample-validation
gate is the mechanism that keeps this true as later PRs touch the schemas.

## Data-model sketch (Postgres, modeled here / applied P2–P2.5)

```
config           (config_hash PK, ir_version, variant_id FK, created_at, spec_json/lineage_ref)
variant          (variant_id PK, label, created_at)
eval_result      (id PK,
                  config_hash FK NOT NULL, variant_id FK NOT NULL, run_id NOT NULL,
                  node_id NOT NULL, case_id NOT NULL, seed NOT NULL, timestamp NOT NULL,
                  metric_name, value, unit, blob_ref NULL,
                  UNIQUE(config_hash, run_id, node_id, case_id, seed, metric_name))
blob             (content_hash PK, size_bytes, media_type)
```

All seven tag columns `NOT NULL`; FKs from `eval_result` → `config`/`variant`; blobs referenced by
`content_hash`, never inlined.

## Risks

- Tag set later found insufficient → mitigated by additive extensibility (optional dimensions) and an
  up-front cross-check against every P4/P4.5 slice.
- `config_hash` accidentally seed/timestamp-sensitive → golden vectors assert seed-invariance and
  version-sensitivity in CI.
- Cardinality misuse in P2.5 → the storage decision record fixes label-vs-attribute placement now.
- Schema churn after M0 → versioning + freeze gate + additive-only rule.

## Open questions

Tracked in PRD §14 (OQ1–OQ6): concrete span store / TSDB product choice, hash length, JSON Schema
dialect strictness, whether `variant_id` lives in the IR, blob GC, and per-store retention/sampling.
