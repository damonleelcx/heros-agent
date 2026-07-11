## Why

After P2, a hardcoded workflow *runs* through the shim and provider gateway — but the run is
opaque. The Runtime persists bare per-node status for its inspect UI; it does not measure what a
run cost, how long each node took, how many tokens it burned, or how often a provider failed. Every
measurement-driven subsystem downstream is blocked on this: the eval harness (P4) has no
operational metrics to compare variants, the improvement engine (P4.5) has no spans to decompose
per-node contribution from, and the autonomous optimizer (P6) has no live cost metric to gate a
budget on. "If it isn't observable, it isn't done" — so the telemetry substrate lands *right after*
the first Runtime, not late.

P2.5 turns P0's frozen contracts into running infrastructure. Collection is **auto-instrumented at
the shim/gateway layer** so operational metrics require zero user code: every provider call is
measured for latency, cost, tokens, and reliability, and every node execution emits an OpenTelemetry
span. Each metric is a **typed event carrying the full seven-tag set** `{variant_id, run_id,
node_id, case_id, seed, timestamp, config_hash}`, routed by shape to the three P0 stores — spans →
span store, metrics → TSDB, eval results → Postgres — all keyed by `config_hash`. The dominant
failure mode is under-tagged metrics you can't later slice, so tagging is enforced at emission, and
the cardinality budget (which tags may be TSDB series labels) is enforced at the collector. The
phase also ships the **evaluator-plugin interface stub** (built-in + user-defined) so P4 slots in
without re-plumbing collection, tagging, or storage.

Depends on P0 (`metric-event.schema.json`, `config_hash`/lineage scheme, storage decision record,
OTel GenAI conventions doc, secrets baseline) and P2 (the shim + gateway as the single
instrumentation seam, the run queue, and the run/`node_execution` records eval results FK to).
Quality/eval metrics, the statistical layer, trend/regression/leaderboard, dashboards, and budget
gates are explicitly out of scope (P4/P4.5+); operational metrics only here.

## What Changes

- **New capability `metrics-observability`.** A shared telemetry substrate consumed by the eval
  harness, improvement engine, and autonomous loop — designed once.
- **Auto-instrumentation at the shim/gateway.** Every provider call and node execution emits
  operational metrics and spans with **zero user code**; no operational metric requires a workflow
  author to annotate a call site. This is a first-class requirement, not a convenience.
- **Operational metric taxonomy.** Latency (total, TTFT, tokens-per-sec), cost (input/output/cache
  tokens × price), tokens (prompt/completion/thinking/cache-hit, context-window utilization),
  reliability (error/timeout/retry/rate-limit), and throughput/concurrency — per node and
  end-to-end. ~15 operational `metric_name`s; quality/agent/safety metrics are P4/P5.
- **Mandatory full-tag emission.** Every metric event carries the full seven-tag set from P0,
  populated from the run context at emission; an event missing any tag is **rejected at the emission
  boundary** and reaches no store, layered with P0's `NOT NULL` columns.
- **Drillable OTel spans.** One run span, one span per node execution, tool calls as child spans,
  emitted through one OTel GenAI-conventions standard (no bespoke logging), tags as OTel attributes.
- **Three-store routing keyed by `config_hash`.** Spans → OTel span store (Tempo/Jaeger); metrics →
  TSDB (Prometheus/ClickHouse); eval results → Postgres — every record keyed by `config_hash`; each
  query shape (trend / drill-down / comparison) served from the store built for it.
- **Cardinality / label discipline (NFR).** Only low-to-moderate cardinality tags (`variant_id`,
  `node_id`, `seed`, `metric_name`) may be TSDB series labels; `case_id`, `run_id`, `invocation_id`,
  and content-hash refs must **not** be labels (they would push series from ~3×10⁴ to ~10⁸/run) —
  they live as span attributes / Postgres columns / exemplars. Enforced at the collector.
- **Secrets never in traces.** No provider secret, API key, prompt text, completion text, or PII in
  any span attribute, metric label, or log; telemetry references content-hashed blobs only.
- **Evaluator-plugin interface stub.** A stable contract for a scoring function that consumes a
  run's trace and emits quality-metric events under the same seven-tag contract — built-in +
  user-defined, mirroring the Skill Registry pattern — exercised by one trivial built-in reference
  evaluator so P4 slots in without re-plumbing. Real evaluators are P4.
- **Degrade-safe, idempotent emission.** Async/non-blocking so a telemetry outage never fails a paid
  run; idempotent under P2's `{run_id, node_id, attempt_group}` retry model so a retry is measured,
  not double-counted.
- **Minimal live run-monitoring view.** Streams a run's per-node latency/cost/tokens with loading /
  error / empty states first-class and a failed/timed-out node visually distinct from a healthy one.

## Impact

- **Affected capabilities:** `metrics-observability` (new). Consumes the `metric-event-schema`,
  `storage-and-lineage`, and `config_hash` contracts from P0 and the `runtime`/`config-layer`
  gateway seam from P2.
- **Affected code/systems:** OTel instrumentation attached at the P2 shim/gateway; an OTel Collector
  (tag-completeness gate + cardinality/label filter + secret/PII scrubber); a span store
  (Tempo/Jaeger); a TSDB (Prometheus/ClickHouse); Postgres eval-results tables (expand-only, `NOT
  NULL` tags + FKs to variant/node/case); the evaluator-plugin interface + one built-in reference
  evaluator; a minimal React live run-monitoring view; retention/sampling configuration.
- **Dependencies:** requires **P0** (metric-event schema, `config_hash`/lineage, storage decision,
  OTel conventions doc, secrets baseline) and **P2** (shim/gateway instrumentation seam, run queue,
  run/node records). Unblocks **P4** (evaluators register through the stub; operational metrics drive
  comparison), **P4.5** (per-node attribution from spans), and **P6** (budget gates read live
  `config_hash`-keyed cost/latency/error metrics).
