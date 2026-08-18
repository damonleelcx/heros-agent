# Tasks — P2.5: Metrics & Observability substrate

## 1. DevOps — Auto-instrumentation at the provider gateway
- [x] 1.1 Attach the OpenTelemetry SDK at the P2 provider gateway so every `Gateway.Complete` call
      emits telemetry with **zero user code**; wrap the executor so every node execution emits a span.
      — `internal/telemetry.Instrument` implements `providergateway.Observer` (attached via one
      `WithObserver(inst)` line); `RunTracer` brackets the run/node path emitting run + node spans.
- [x] 1.2 Emit **latency** metrics per call: total, TTFT, tokens-per-sec (per node + end-to-end).
      — `MetricSet` in `metrics.go` (latency_total_ms, latency_ttft_ms, throughput_tokens_per_sec).
- [x] 1.3 Emit **cost** metrics: input/output/cache tokens × the model's price; attributable per
      node, per run, cumulative. Pin the price source (see design; likely the model-registry version).
      — `PriceBook` (versioned, keyed by provider/model_id; cost carries `pricebook_version`).
- [x] 1.4 Emit **token** metrics: prompt/completion/thinking/cache-hit + context-window utilization.
- [x] 1.5 Emit **reliability** metrics: error rate, timeout rate, retry count, rate-limit hits.
      — gateway now surfaces `RateLimited` + accurate `Attempts` on failure paths too.
- [x] 1.6 Emit **throughput/concurrency** metrics for the run (in-flight calls, calls/sec).
      — `RunTracer.NodeStarted/NodeFinished/EndRun` emit run-scoped concurrency/throughput.
- [x] 1.7 Verify no operational metric requires a workflow author to annotate a call site: a fixture
      workflow with **zero** telemetry code emits the full operational set.
      — `TestSection1_ZeroUserCodeEmitsFullOperationalSet` drives real `executor.CallProvider`.

## 2. DevOps + System Designer — Tagged-event emitter & the emission gate
- [x] 2.1 Populate the full seven-tag set `{variant_id, run_id, node_id, case_id, seed, timestamp,
      config_hash}` from the run context on every metric event, plus the P0 payload
      (`metric_name`, `value`, `unit`). — `RunContext.event()` is the single stamping point.
- [x] 2.2 Implement the **tag-completeness gate** at the emission boundary: reject any event missing
      any of the seven tags; the event reaches no store. Layer with P0 `NOT NULL` columns.
      — `Gate.EmitMetric` runs `metricevent.Validate`; rejected events are dropped + logged, never
      forwarded; Postgres `NOT NULL` is the second layer (§5).
- [x] 2.3 Ensure `config_hash` is present on every metric event **and** every span so all telemetry
      is attributable to an exact configuration. — `Gate.EmitSpan`/`spanAttributable` reject a span
      without a valid `config_hash`.
- [x] 2.4 Idempotent emission: key on P2's `{run_id, node_id, attempt_group}` so a retried
      invocation is measured once — no double-counted cost, no double-written event/span.
      — metrics dedup on `invocation_id|metric_name`; spans dedup on deterministic `span_id`.

## 3. DevOps + System Designer — Cardinality / label discipline (the deep-dive)
- [x] 3.1 Configure the collector so only low-to-moderate cardinality tags (`variant_id`, `node_id`,
      `seed`, `metric_name`) become TSDB **series labels**. — `SeriesLabelTags` allowlist (+ `config_hash`,
      schema-blessed low-card-per-run, required for trend filtering); `SeriesLabels`/`SeriesKey`.
- [x] 3.2 Strip high-cardinality identifiers (`case_id`, `run_id`, `invocation_id`, content-hash
      refs) from TSDB labels; retain them as span attributes / Postgres columns / exemplars.
      — `SeriesLabels` drops them entirely; `Exemplars` retains them for drill-down.
- [x] 3.3 Assert the budget: a 200-case run keeps active series ≈ 3×10⁴/run (not ~10⁸); `case_id`
      is still queryable as a span attribute / Postgres column.
      — `TestSection3_SeriesBudgetIsUnaffectedByCaseCount` (30000 series exactly; case not a label).

## 4. DevOps — OTel spans & one instrumentation standard
- [x] 4.1 Emit one run span, one span per node execution, tool calls as child spans — drillable
      per-run in the span store. — `RunTracer` (run + tool spans) + `Instrument.nodeSpan`;
      `TestSection4_DrillableHierarchy` proves run→node→tool linkage via deterministic parent ids.
- [x] 4.2 Emit spans + metrics through the OTel **GenAI semantic conventions** (P0 doc), not a
      bespoke logging layer; carry the seven tags as OTel attributes. — `attributes.go` uses the real
      `gen_ai.*` keys; span name `chat <model>`; `TestSection4_SpansUseGenAIConventions`.
- [x] 4.3 Configure span **sampling + retention bounds** sized from the §8.3 volumes (mechanism now,
      numbers tuned against real volume). — `SpanSampler` (per-trace head + always-keep-errors) +
      `RetentionPolicy`/`DefaultRetention` sized from §8.3 (spans shortest, eval longest).

## 5. DevOps + System Designer — Three-store routing (keyed by config_hash)
- [x] 5.1 Stand up the OTel Collector pipeline: tag-completeness gate → cardinality/label filter →
      secret/PII scrubber → fan-out to the three stores. — `Collector` (async worker):
      `gate.admitMetric/admitSpan` → `SeriesLabels` projection → `Scrubber` → `SpanStore`/`TSDB`/`EvalStore`.
- [x] 5.2 Route spans → OTel-compatible span store (Tempo/Jaeger); metrics → TSDB
      (Prometheus/ClickHouse); eval results → Postgres. Every record keyed by `config_hash`.
      — `stores.go` interfaces + in-memory impls; `TestSection5_ThreeStoreRouting`.
- [x] 5.3 Expand-only Postgres eval-results tables: `NOT NULL` seven tag columns, FKs to
      variant/node/case (P0 storage-and-lineage), `config_hash` column. — 0001 froze the seven tags +
      FKs; `0008_p25_eval_results` adds `evaluator_name` + blob refs + widened natural key (expand-only);
      `PGEvalStore` proved live (`*_pgproof_test.go`, added to `make pg-proof`).
- [x] 5.4 Verify each query shape hits its store filterable by `config_hash`: trend→TSDB,
      drill-down→span store, comparison→Postgres. — `TestSection5_QueryShapesHitTheirStore`
      (+ TSDB refuses a high-cardinality matcher loudly).

## 6. DevOps — Secrets / PII scrubbing & degrade-safety
- [x] 6.1 Scrub secrets, API keys, prompt text, completion text, and PII at the collector before any
      store; telemetry carries content-hashed blob references only. — `Scrubber` (secret/PII patterns +
      long-text→blob-ref); runs in `Collector.process` before any store;
      `TestSection6_ScrubberRemovesSecretsAndPII` + end-to-end no-leak test.
- [x] 6.2 Least privilege: collector holds write-only store credentials; provider keys stay in the
      manager and never reach a span/label/log. — `CollectorConfig` takes stores only, no `Secrets`;
      gateway pre-scrubs `CallInfo.Err`; `TestSection6_RunLeavesNoProviderKeyInAnyStore`.
- [x] 6.3 Async/non-blocking emission: a telemetry-backend outage degrades telemetry, never fails a
      paid run; add < 5 ms p50 overhead per call off the request path. — async worker + non-blocking
      enqueue (drop-on-full); `TestSection6_EmissionIsNonBlockingUnderSlowBackend` (p50 < 5 ms).
- [x] 6.4 Fault-injection test: kill the collector mid-run → the run still completes.
      — `TestSection6_CollectorDeathMidRunDoesNotFailTheRun` + `...PanickingStoresDoNotFailTheRun`.

## 7. AI Engineer + System Designer — Evaluator-plugin interface stub
- [x] 7.1 Define the `Evaluator` interface: `Evaluate(ctx RunContext, trace Trace) →
      []QualityMetricEvent`, where `RunContext` carries the seven tags so an evaluator cannot emit an
      under-tagged event; `Register(evaluator)` supports built-in + user-defined (Skill-Registry pattern).
      — `Evaluator`/`Registry`/`RunContext.Quality` (tags by construction); `TestSection7_RegistryBuiltInAndUserDefined`,
      `...EvaluatorCannotEmitUnderTaggedEvent`.
- [x] 7.2 Ship one trivial **built-in reference evaluator** exercising the seam end to end: its
      events carry the seven-tag set and land in the eval-results store. — `ReferenceEvaluator` +
      `RunEvaluators`; `TestSection7_ReferenceEvaluatorProvesTheSeam`.
- [x] 7.3 Version the interface (stable major) so P4's real evaluators bind without re-plumbing
      collection, tagging, or storage. Real quality evaluators are P4. — `EvaluatorInterfaceVersion`.
- [x] 7.4 Confirm the tag set supports every P4/P4.5 slice: per-variant, per-node, per-case /
      per-failure-cluster, per-seed. — `TestSection7_TagSetSupportsAllSlices`.

## 8. Frontend — Live run-monitoring view
- [x] 8.1 Stream a run's per-node latency, cost, and token metrics as they arrive. — `telemetry.Monitor`
      reads per-run node metrics from the span store; `api` serves an SSE stream + JSON snapshot;
      `static/p25monitor.html` renders live. Verified: SSE delivered nodes 0→6 as they arrived.
- [x] 8.2 First-class **loading / error / empty / streaming / terminal** states; read status from the
      run record (no derived state that drifts); a failed/timed-out node is driven by the reliability
      metric and is visually distinct from a slow-but-healthy node. — status read verbatim from
      `RunStatusSource`; `nodeState` drives ok/failed/timed_out from the span reliability attrs; browser-
      verified all five states + distinct failed(red)/timed_out(amber)/ok(green) rendering.
- [x] 8.3 Verify against a live (stubbed-provider) run before calling the view done.
      — `cmd/demo/runmonitor` (stub provider, unique run_id/cycle); verified in Chrome (screenshots).

## 9. Testing & review
- [x] 9.1 Fixture: a run (reusing P2's hardcoded graph) with **no** telemetry code; assert the full
      operational taxonomy is emitted, fully tagged, keyed by `config_hash`.
      — `TestSection1_ZeroUserCodeEmitsFullOperationalSet` (+ folded into `TestM3ExitChecklist`).
- [x] 9.2 Integration tests (real Postgres + object store + local span store/TSDB, stubbed
      providers): zero-user-code collection; tag-completeness (missing-`config_hash` rejected);
      cardinality discipline; three-store routing; drillable spans; secrets/PII scrubbing;
      degrade-safe; idempotency; evaluator seam. — `TestPG_Integration_FullPipelineToLivePostgres`
      (live Postgres eval store) + the hermetic `TestSection2..7` suite; added to `make pg-proof`.
- [x] 9.3 UI verification: drive the live monitor against a live run; confirm streaming metrics +
      loading/error/empty states + distinct failed/timed-out node. — verified in Chrome against
      `cmd/demo/runmonitor` (SSE streamed nodes 0→6; error/terminal/state screenshots captured).
- [x] 9.4 Adversarial self-review: under-tagged event, `case_id` as a TSDB label, secret in a span,
      collector down mid-run, retry double-count, un-instrumented node. — `adversarial_test.go`
      (`TestAdversarial_*`, six checks).
- [x] 9.5 Confirm the M3 exit checklist (PRD §13) is green: a run produces drillable spans +
      queryable operational metrics, all tagged and keyed by `config_hash`.
      — `TestM3ExitChecklist` (all 10 checklist items as named subtests).
