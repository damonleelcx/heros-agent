# OpenTelemetry GenAI Semantic-Conventions (P0)

| Field | Value |
|---|---|
| Phase / Milestone | P0 / M0 |
| Owner | DevOps (support to System Designer / AI) |
| Status | Draft — freeze at M0 |
| Tasks | 4.3 (this doc) |
| Cross-refs | `schemas/metric-event.schema.json`; `docs/decisions/storage-decision-record.md` (cardinality budget); `docs/decisions/secrets-baseline.md`; PRD §6 (FR11), §7 (NFR6) |

The single instrumentation standard the **whole team emits against**. Every span and metric the platform
produces maps onto the [OpenTelemetry GenAI semantic conventions](https://opentelemetry.io/docs/specs/semconv/gen-ai/)
so we emit against one convention, not a bespoke logging layer (FR11). *"If it isn't observable, it isn't
done"* — which is why this is fixed in P0, three phases before instrumentation lands in P2.5.

No live telemetry is emitted in P0; this doc is the contract P2.5 wires and CI later enforces.

---

## 1. The mapping: metric-event field → OTel attribute

Every event carries the seven tags + typed payload (`schemas/metric-event.schema.json`). Each maps to a
stable OTel attribute key. Platform-specific keys are namespaced `heros.*`; standard GenAI keys use the
upstream `gen_ai.*` names so our data is portable to any OTel backend.

| metric-event field | OTel attribute key | On span? | TSDB series label? | Notes |
|---|---|---|---|---|
| `variant_id` | `heros.variant.id` | ✅ | ✅ low-card | which variant |
| `run_id` | `heros.run.id` | ✅ | ❌ **high-card** | batch grouping; span attr / exemplar only |
| `node_id` | `heros.node.id` | ✅ | ✅ low-card | per-node attribution; maps to IR `node_id` |
| `case_id` | `heros.case.id` | ✅ | ❌ **high-card** | span attr / Postgres column, never a label |
| `seed` | `heros.seed` | ✅ | ✅ low-card | pins our inputs (not provider sampling) |
| `timestamp` | span start / `Timestamp` | native | — | RFC 3339; excluded from `config_hash` |
| `config_hash` | `heros.config.hash` | ✅ | ✅ low-card/run | which exact replayable configuration |
| `metric_name` | metric instrument name | native | ✅ (the metric) | e.g. `heros.llm.latency` |
| `value` | metric data point value | native | — | the measurement |
| `unit` | instrument unit (UCUM) | native | — | `ms`, `1` (ratio), `{token}`, … |
| `node_kind` (opt) | `heros.node.kind` | ✅ | ✅ low-card | optional dimension |
| `invocation_id` (opt) | `heros.invocation.id` | ✅ | ❌ **high-card** | span attr / exemplar only |

### Standard GenAI attributes we also set (upstream `gen_ai.*`)

On each LLM-call span, in addition to the tags above:

| OTel GenAI attribute | Source | Example |
|---|---|---|
| `gen_ai.system` | node `model.provider` | `openai`, `anthropic` |
| `gen_ai.request.model` | node `model.model_id` | `gpt-4o-mini` |
| `gen_ai.request.temperature` / `.max_tokens` / `.top_p` | node `model.params` | `0.2` |
| `gen_ai.usage.input_tokens` / `.output_tokens` | provider response | `1240` |
| `gen_ai.operation.name` | call kind | `chat`, `embeddings` |

### Metric instruments (map to the TSDB)

| Instrument | Type | Unit | Labels (low-card only) |
|---|---|---|---|
| `heros.llm.latency` | histogram | `ms` | variant_id, node_id, seed, config_hash |
| `heros.llm.tokens` | histogram | `{token}` | + `token.type` = input/output/thinking |
| `heros.llm.cost` | histogram | `usd` | + `cost.type` = input/output/cache |
| `heros.llm.retries` | counter | `1` | variant_id, node_id, seed |

`case_id`, `run_id`, `invocation_id` are attached to **spans** and **exemplars**, never as metric
labels — this is the cardinality budget from the storage decision record (active series ≈ 3×10⁴/run).
Putting `case_id` (200×) or `run_id`/`invocation_id` (10⁶×) on a metric would blow the TSDB up; the
convention forbids it.

## 2. 🚫 The hard rule: no prompts / PII / secrets in span attributes (NFR6)

**Prompt text, completion text, tool arguments, retrieved documents, user data, and any provider
credential MUST NEVER be written to a span attribute, metric label, or log line.** This is a security
requirement, not a style preference (arbitrated at level 1 — safety — on the cost/complexity ladder).

Why: spans and metrics fan out to observability backends with broad read access and long retention;
a prompt containing customer PII or an API key in a span attribute is a durable, widely-replicated
leak. The upstream GenAI conventions make prompt/response capture **opt-in and content-separated** for
exactly this reason.

**Where the content goes instead:** large prompt/completion blobs are **content-hashed (SHA-256) into
object storage** and referenced by hash (storage decision FR15). A span carries the **blob reference
hash**, never the bytes:

| Instead of… | Emit… |
|---|---|
| `gen_ai.prompt = "<full prompt with user email>"` | `heros.prompt.blob_hash = "<sha256>"` |
| `gen_ai.completion = "<full model output>"` | `heros.completion.blob_hash = "<sha256>"` |
| tool call arguments inline | `heros.tool.args_hash = "<sha256>"` |
| provider API key anywhere | — (never; see `secrets-baseline.md`) |

Redaction is **emit-side** (before the exporter), so unredacted content never leaves the process. A
span/metric exporter middleware SHALL drop any attribute whose key is not on the allow-list of known
`heros.*` / `gen_ai.*` keys — deny-by-default, so a new field can't accidentally carry content.

## 3. Conventions checklist (what P2.5 instrumentation must satisfy)

- [ ] Every span/metric carries the seven tags under the keys in §1, all non-null.
- [ ] High-cardinality ids (`case_id`, `run_id`, `invocation_id`) are span attributes / exemplars, never
      metric labels.
- [ ] Metric instrument names/units follow the §1 table (UCUM units).
- [ ] No attribute/label/log carries prompt, completion, tool args, retrieved content, PII, or secrets.
- [ ] Large content is content-hashed to object storage; spans carry the hash reference only.
- [ ] Exporter middleware is deny-by-default on attribute keys (allow-list of `heros.*` / `gen_ai.*`).
- [ ] The seven tags round-trip to `metric-event.schema.json` (the boundary guard, `internal/metricevent`,
      and this doc agree).

## 4. Enforcement

- **Schema:** `metric-event.schema.json` + `internal/metricevent` reject under-tagged events at emit time.
- **This doc:** the attribute-key and no-content rules the P2.5 exporter middleware implements.
- **CI:** the `secret-scan` job (gitleaks) blocks committed secrets; a future P2.5 test asserts the
  exporter middleware drops off-allow-list attributes (the redaction proof, wired when instrumentation
  exists).

## 5. Open questions (deferred, non-blocking)

- **OQ1 (storage):** concrete span store / TSDB product (Tempo vs Jaeger; Prometheus vs ClickHouse) —
  the OTel-compat constraint here keeps the choice open to P2.5.
- **Opt-in content capture:** a debugging mode may capture prompt/response *to object storage under
  access control* (never to spans); the trust/consent model for that is a P2.5/Product decision.
