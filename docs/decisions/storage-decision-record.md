# Storage Decision Record (P0)

| Field | Value |
|---|---|
| Phase / Milestone | P0 / M0 |
| Owner | System Designer (lead); Backend, DevOps support |
| Status | Draft — freeze at M0 |
| Tasks | 1.1 (assumptions), 1.2 (estimate + cardinality budget), 1.7 (three stores + trade-offs) |
| Cross-refs | `docs/prd/P0-foundations.md` §8; `docs/decisions/config-hash-spec.md`; `docs/decisions/architecture-and-lineage.md`; `openspec/changes/archive/2026-07-15-p0-foundations/specs/storage-and-lineage/spec.md` |

> **System-designer stance: numbers before boxes.** This record picks the stores *from the volume
> estimate*, not by reflex. Nothing here is a running service — P0 ships the decision; P2/P2.5 wire it.
> Every choice is arbitrated with the cost-and-complexity priority ladder (safety > stability > UX >
> ops-cost > evolvability > extensibility > maintenance > implementation), and each rejected option is
> stated.

---

## 1. Explicit assumptions & scope boundaries (task 1.1)

These bound the estimate. If any moves by an order of magnitude, revisit §5.

| # | Assumption | Value |
|---|---|---|
| A1 | Repos onboarded | ~5/day early → **~50/day** design target |
| A2 | Static LLM nodes per repo | 10–50, **median ~20** |
| A3 | Representative **optimization run** | **N = 20** variants × **K = 200** eval cases × **S = 5** seeds |
| A4 | Runtime invocations per case | agents loop ~2–3× static count → **~50 invocations/case** |
| A5 | Metrics emitted per invocation | ~10 (latency total/TTFT/TPS; cost in/out/cache; tokens prompt/completion/thinking; retries) |
| A6 | Optimization runs/day (all users, target scale) | **~10** |
| A7 | **Reproducibility definition** | exact-config replay via `config_hash + seed` — *our* inputs pinned, **not** bit-identical provider output (providers are non-deterministic). See config-hash-spec §6. |

**Scope boundaries.** P0 defines *schema + storage shape + cardinality rule* only. Out of scope here
(owning phase in parens): live instrumentation and standing up the stores as services (P2.5); the
concrete product pick for span store / TSDB (P2.5, OQ1); retention/sampling tuning (P2.5, OQ6); blob
GC (deferred, OQ5); the Postgres migration itself (P2/P2.5 — modeled in §6).

## 2. Back-of-envelope estimate (task 1.2)

**Per optimization run** (A3 × A4 × A5):

| Quantity | Derivation | Result |
|---|---|---|
| Runtime invocations | 20 × 200 × 5 × 50 | **1.0 × 10⁶** |
| Metric events | invocations × ~10 metrics | **1.0 × 10⁷** |
| Spans | 1/invocation + ~2× tool-call children + 1 run span | **~3 × 10⁶** |
| Eval-result rows | one per (variant, case, seed) scored: 20 × 200 × 5 | **2.0 × 10⁵** |
| Blob writes (pre-dedup) | prompt + completion per invocation | **1.0 × 10⁶** |

**Sizing → the store each shape implies:**

| Data | Per-run raw | Store & why |
|---|---|---|
| Metrics | 10⁷ events × ~2 B compressed ≈ **~20 MB** | **TSDB** — huge counts of tiny numeric points, aggregation/trend queries, columnar + compressed. |
| Spans | 3×10⁶ × ~1–2 KB ≈ **3–6 GB** (sampled, retention-bounded) | **OTel span store** (Tempo/Jaeger) — per-run drill-down, trace-native. |
| Eval results | 2×10⁵ × ~1 KB ≈ **~200 MB** | **Postgres** — low volume, rich relational joins across variant/node/case, constraints enforce tags. |
| Blobs | 10⁶ × ~6 KB ≈ 6 GB → **content-hash dedup ~5–10× → ~0.6–1 GB** | **Object store**, content-addressed; identical prompts/templates collapse to one object. |

**Per day** at A6 = ~10 runs: ~**10⁸** metric events/day (TSDB comfortable), ~**3×10⁷** spans/day
(sampled), ~**2×10⁶** eval rows/day (Postgres comfortable), ~**10 GB** blobs/day (object store).

## 3. Cardinality budget (task 1.2) — the load-bearing decision

This is *why metrics ≠ Postgres and why high-cardinality ids ≠ TSDB labels*. It fixes, per tag,
whether it may be a **TSDB series label** or must live only as a **span attribute / Postgres column**.

| Tag / dimension | Approx. cardinality per run | TSDB series label? | Home if not a label |
|---|---|---|---|
| `variant_id` | ~20 | ✅ yes | — |
| `node_id` | ~20 | ✅ yes | — |
| `seed` | ~5 | ✅ yes | — |
| `metric_name` | ~15 | ✅ yes | — |
| `config_hash` | ~20/run (≈ variants) | ✅ yes (bounded per run) | — |
| `case_id` | ~200 | ❌ **no** (200× series blow-up) | span attribute + Postgres column |
| `run_id` | ~10⁶ across time | ❌ **no** | span attribute + Postgres column |
| `invocation_id` | ~10⁶/run | ❌ **no** | span attribute / exemplar only |
| blob `content_hash` | ~10⁶ | ❌ **no** | Postgres/span reference |

**Series budget with only the low-cardinality labels:**

```
active series ≈ variant(20) × node(20) × metric_name(15) × seed(5) ≈ 3 × 10⁴ series/run
```

— comfortably within TSDB limits. If `case_id` were a label it would multiply by 200 → **6 × 10⁶**;
if `run_id`/`invocation_id` were labels it would explode to **~10⁸** and make the TSDB unusable. The
schemas encode this: `metric-event.schema.json` documents each tag's cardinality class, and
`runtime-invocation.schema.json` marks `invocation_id`/`run_id` as never-a-label.

## 4. The three stores + content-hashed blobs (task 1.7)

Route by **data shape**, every record keyed by `config_hash`:

| Shape | Store | Query pattern it serves |
|---|---|---|
| Metrics — huge count, tiny numeric, aggregated | **TSDB** (Prometheus / ClickHouse — product TBD, OQ1) | trend over time, aggregate over variants/nodes/seeds |
| Spans — huge, drill-down, sampled | **OTel-compatible span store** (Tempo / Jaeger — OQ1) | per-run trace drill-down |
| Eval results — low volume, relational | **Postgres** | variant-vs-variant comparison joins; **enforces the tag contract** |
| Blobs — large, dedup-able | **Object store**, content-addressed (SHA-256 of bytes) | reproducibility by reference; ~5–10× dedup |

All four are keyed/joined by `config_hash`, so a metric trend, a trace drill-down, and a comparison
table are three questions about the *same* configurations.

## 5. Trade-offs rejected (task 1.7)

Stated explicitly per the system-designer rule (*a diagram without "why not Z" is not a design*).

| Option | Verdict | Reasoning on the priority ladder |
|---|---|---|
| **One store (Postgres) for everything** | Rejected | 10⁷ metric events/run makes trend queries and cardinality unmanageable, and spans-as-rows lose trace drill-down. Forcing three query shapes into one engine degrades **stability + UX** (levels 2–3) to save **operational surface** (level 4) — an inversion. The three-store operational cost is the accepted price. |
| **Metrics in a TSDB with `case_id`/`run_id` as labels** | Rejected | Cardinality → 10⁶–10⁸ series; the TSDB falls over. A **stability** risk (level 2) traded for query convenience. High-cardinality ids are span attributes / PG columns instead. |
| **Inline blobs in events/rows** | Rejected | No dedup (6 GB → stays 6 GB/run) and reproducibility-by-value bloats every store. Content-hash indirection costs one lookup + a future GC story (OQ5) but buys ~5–10× dedup and reproducibility-by-reference. The **evolvability/cost** win (levels 5,7) beats the minor **implementation** simplicity of inlining (level 8). |
| **Pick the concrete TSDB / span-store product now** | Deferred (OQ1) | P0 freezes the *shape* (OTel-compatible). Committing a product is a near one-way door with no P0 information to decide it well; the OTel-compatibility constraint keeps the door open for P2.5. |
| **Skip Postgres constraints, validate tags in app code** | Rejected | App code forgets; a constraint is a test that runs on every write forever. Enforcing non-null tags + FKs in the DB defends **stability** of the whole downstream analysis (§6). |

## 6. Relational model (modeled here, applied P2–P2.5)

Postgres structurally enforces the tagging/lineage invariants — the DB enforces what app code forgets.

```
config       (config_hash PK, ir_version, variant_id FK -> variant, created_at, lineage_ref)
variant      (variant_id PK, label, created_at)
blob         (content_hash PK, size_bytes, media_type)
eval_result  (id PK,
              config_hash  FK -> config   NOT NULL,
              variant_id   FK -> variant  NOT NULL,
              run_id   NOT NULL, node_id NOT NULL, case_id NOT NULL,
              seed     NOT NULL, timestamp NOT NULL,
              metric_name, value, unit,
              blob_ref FK -> blob NULL,
              UNIQUE (config_hash, run_id, node_id, case_id, seed, metric_name))
```

- **All seven tag columns `NOT NULL`** (NFR3) — an untagged row cannot be written.
- **FKs** `eval_result → config / variant`, and `→ blob` for artifacts (never inlined).
- **Uniqueness** on the natural key makes re-runs idempotent (no double-write, no lost attribution) —
  groundwork for P2's idempotent executor.

## 7. Effect (what this unlocks)

- **P2.5** emits against `metric-event.schema.json` into these three stores with the cardinality rule
  already fixed — no retrofit of under-tagged metrics.
- **P4–P6** slice by every tag and reproduce any run from `config_hash + seed`.
- **DevOps (P0 §4 tasks)** builds the schema-validation CI gate and OTel conventions doc against these
  frozen shapes; secrets baseline forbids prompts/PII/secrets in span attributes (NFR6).
