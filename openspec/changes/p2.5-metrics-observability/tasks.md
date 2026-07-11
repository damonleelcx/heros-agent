# Tasks — P2.5: Metrics & Observability substrate

## 1. DevOps — Auto-instrumentation at the shim/gateway
- [ ] 1.1 Attach the OpenTelemetry SDK at the P2 provider gateway so every `Gateway.Complete` call
      emits telemetry with **zero user code**; wrap the shim so every node execution emits a span.
- [ ] 1.2 Emit **latency** metrics per call: total, TTFT, tokens-per-sec (per node + end-to-end).
- [ ] 1.3 Emit **cost** metrics: input/output/cache tokens × the model's price; attributable per
      node, per run, cumulative. Pin the price source (see design; likely the model-registry version).
- [ ] 1.4 Emit **token** metrics: prompt/completion/thinking/cache-hit + context-window utilization.
- [ ] 1.5 Emit **reliability** metrics: error rate, timeout rate, retry count, rate-limit hits.
- [ ] 1.6 Emit **throughput/concurrency** metrics for the run (in-flight calls, calls/sec).
- [ ] 1.7 Verify no operational metric requires a workflow author to annotate a call site: a fixture
      workflow with **zero** telemetry code emits the full operational set.

## 2. DevOps + System Designer — Tagged-event emitter & the emission gate
- [ ] 2.1 Populate the full seven-tag set `{variant_id, run_id, node_id, case_id, seed, timestamp,
      config_hash}` from the run context on every metric event, plus the P0 payload
      (`metric_name`, `value`, `unit`).
- [ ] 2.2 Implement the **tag-completeness gate** at the emission boundary: reject any event missing
      any of the seven tags; the event reaches no store. Layer with P0 `NOT NULL` columns.
- [ ] 2.3 Ensure `config_hash` is present on every metric event **and** every span so all telemetry
      is attributable to an exact configuration.
- [ ] 2.4 Idempotent emission: key on P2's `{run_id, node_id, attempt_group}` so a retried
      invocation is measured once — no double-counted cost, no double-written event/span.

## 3. DevOps + System Designer — Cardinality / label discipline (the deep-dive)
- [ ] 3.1 Configure the collector so only low-to-moderate cardinality tags (`variant_id`, `node_id`,
      `seed`, `metric_name`) become TSDB **series labels**.
- [ ] 3.2 Strip high-cardinality identifiers (`case_id`, `run_id`, `invocation_id`, content-hash
      refs) from TSDB labels; retain them as span attributes / Postgres columns / exemplars.
- [ ] 3.3 Assert the budget: a 200-case run keeps active series ≈ 3×10⁴/run (not ~10⁸); `case_id`
      is still queryable as a span attribute / Postgres column.

## 4. DevOps — OTel spans & one instrumentation standard
- [ ] 4.1 Emit one run span, one span per node execution, tool calls as child spans — drillable
      per-run in the span store.
- [ ] 4.2 Emit spans + metrics through the OTel **GenAI semantic conventions** (P0 doc), not a
      bespoke logging layer; carry the seven tags as OTel attributes.
- [ ] 4.3 Configure span **sampling + retention bounds** sized from the §8.3 volumes (mechanism now,
      numbers tuned against real volume).

## 5. DevOps + System Designer — Three-store routing (keyed by config_hash)
- [ ] 5.1 Stand up the OTel Collector pipeline: tag-completeness gate → cardinality/label filter →
      secret/PII scrubber → fan-out to the three stores.
- [ ] 5.2 Route spans → OTel-compatible span store (Tempo/Jaeger); metrics → TSDB
      (Prometheus/ClickHouse); eval results → Postgres. Every record keyed by `config_hash`.
- [ ] 5.3 Expand-only Postgres eval-results tables: `NOT NULL` seven tag columns, FKs to
      variant/node/case (P0 storage-and-lineage), `config_hash` column.
- [ ] 5.4 Verify each query shape hits its store filterable by `config_hash`: trend→TSDB,
      drill-down→span store, comparison→Postgres.

## 6. DevOps — Secrets / PII scrubbing & degrade-safety
- [ ] 6.1 Scrub secrets, API keys, prompt text, completion text, and PII at the collector before any
      store; telemetry carries content-hashed blob references only.
- [ ] 6.2 Least privilege: collector holds write-only store credentials; provider keys stay in the
      manager and never reach a span/label/log.
- [ ] 6.3 Async/non-blocking emission: a telemetry-backend outage degrades telemetry, never fails a
      paid run; add < 5 ms p50 overhead per call off the request path.
- [ ] 6.4 Fault-injection test: kill the collector mid-run → the run still completes.

## 7. AI Engineer + System Designer — Evaluator-plugin interface stub
- [ ] 7.1 Define the `Evaluator` interface: `Evaluate(ctx RunContext, trace Trace) →
      []QualityMetricEvent`, where `RunContext` carries the seven tags so an evaluator cannot emit an
      under-tagged event; `Register(evaluator)` supports built-in + user-defined (Skill-Registry pattern).
- [ ] 7.2 Ship one trivial **built-in reference evaluator** exercising the seam end to end: its
      events carry the seven-tag set and land in the eval-results store.
- [ ] 7.3 Version the interface (stable major) so P4's real evaluators bind without re-plumbing
      collection, tagging, or storage. Real quality evaluators are P4.
- [ ] 7.4 Confirm the tag set supports every P4/P4.5 slice: per-variant, per-node, per-case /
      per-failure-cluster, per-seed. A missing tag here is an un-answerable question later.

## 8. Frontend — Live run-monitoring view
- [ ] 8.1 Stream a run's per-node latency, cost, and token metrics as they arrive.
- [ ] 8.2 First-class **loading / error / empty / streaming / terminal** states; read status from the
      run record (no derived state that drifts); a failed/timed-out node is driven by the reliability
      metric and is visually distinct from a slow-but-healthy node.
- [ ] 8.3 Verify against a live (stubbed-provider) run before calling the view done.

## 9. Testing & review
- [ ] 9.1 Fixture: a run (reusing P2's hardcoded graph) with **no** telemetry code; assert the full
      operational taxonomy is emitted, fully tagged, keyed by `config_hash`.
- [ ] 9.2 Integration tests (real Postgres + object store + local span store/TSDB, stubbed
      providers): zero-user-code collection; tag-completeness (missing-`config_hash` rejected);
      cardinality discipline; three-store routing; drillable spans; secrets/PII scrubbing;
      degrade-safe; idempotency; evaluator seam.
- [ ] 9.3 UI verification: drive the live monitor against a live run; confirm streaming metrics +
      loading/error/empty states + distinct failed/timed-out node.
- [ ] 9.4 Adversarial self-review: under-tagged event, `case_id` as a TSDB label, secret in a span,
      collector down mid-run, retry double-count, un-instrumented node.
- [ ] 9.5 Confirm the M3 exit checklist (PRD §13) is green: a run produces drillable spans +
      queryable operational metrics, all tagged and keyed by `config_hash`.
