# Tasks — P4.5: Attribution + rule-based Diagnosis + scorecard (read-only)

## 1. AI Engineer — Per-node contribution & first-divergence
- [x] 1.1 Implement `Attribute(variant, eval_set) → PerNodeContribution`: decompose end-to-end
      failure/cost/latency to individual nodes from the OTel traces + P4 per-node contribution signal.
- [x] 1.2 Compute, per failing case, the node whose output **first diverges** from success
      (contract-violation first, then reference-mismatch where a gold reference exists — see design Q1).
- [x] 1.3 Persist attribution rows keyed `{variant_id, eval_set_hash, config_hash, node_id, case_id}`;
      make per-node / per-case attribution a **query**, not a re-run.
- [x] 1.4 Test: on a workflow with a fault injected at exactly one node, per-node contribution names
      that node as the first-divergence node on the failing cases.

## 2. AI Engineer — Failure clustering
- [x] 2.1 Embed failing inputs + traces; cluster into **named categories** (design Q2: embedding
      choice + algorithm).
- [x] 2.2 Emit `[]FailureCluster` each with a **size**, a **representative case**, and member
      `case_id`s; persist append-only.
- [x] 2.3 Name each cluster (LLM-labeled — itself calibrated — or rule-derived from the dominant trace
      signature; design Q2).
- [x] 2.4 Test: a fixture with a **multi-hop** fault cluster and a **tool-returns-empty** fault
      cluster yields two distinct named clusters with correct sizes.

## 3. AI Engineer — Ablation / counterfactual isolation
- [x] 3.1 Implement `Ablate(variant, node, config') → AblationResult`: hold every **other** node's
      config fixed, swap exactly **one** node's config, re-run through the **P4 harness** multi-seed.
- [x] 3.2 Report the measured delta **with its CI** (reuse the P4 `Stats.Compare` primitive); a delta
      whose CI **overlaps zero** → verdict `inconclusive`, else `bottleneck`.
- [x] 3.3 Ablation runs are **ephemeral measurement variants** — enqueue them, never persist them as
      user variants, never apply them to the workflow.
- [x] 3.4 Seed the ablation candidate set from the per-node contribution ranking (design Q3);
      run in the **P3 sandbox**, no ambient credentials. (ranking here; sandboxed runner in §9)
- [x] 3.5 Test: swapping the **faulty** node's config → non-overlapping-zero delta names it the
      bottleneck; swapping a **non-faulty** node → **inconclusive** (CI overlaps zero).

## 4. AI Engineer — Cost/latency bottleneck flags
- [x] 4.1 Compute the cost/latency **Pareto across nodes** from the P2.5 per-node metrics.
- [x] 4.2 Emit `[]BottleneckFlag` naming the node(s) that dominate spend or sit on the critical path,
      tagged with the dimension (cost | latency).
- [x] 4.3 Test: the flag names the cost-dominating node and the latency-critical-path node.

## 5. AI Engineer — Rule-based detectors (rules-first)
- [x] 5.1 Implement deterministic `Detect(trace) → []TypedCause` detectors, each emitting a **typed
      cause** from the fixed failure taxonomy:
      context overflow/truncation before a failing node; tool schema mismatch / repeated tool errors;
      retrieval miss (low-relevance chunks); prompt-format drift (output contract ignored → downstream
      parse fail); lost-in-the-middle (over-long context degrading a later node); model-capability
      mismatch (cheap model on a reasoning-heavy node).
- [x] 5.2 Guarantee determinism: the same trace yields the same typed cause every run (no LLM in the
      rule path).
- [x] 5.3 Detectors run **first** on every failing case; record which cases they explained.
- [x] 5.4 Test: prompt-format-drift detector names the faulty node's cause on every run of the same
      trace; a context-overflow fixture trips the overflow detector.

## 6. AI Engineer — LLM-as-analyst (constrained + calibrated)
- [x] 6.1 Invoke `Analyze(trace, rubric) → Diagnosis` **only on the residue** the rules didn't explain
      (assert analyst call count = residue count, not total failing count).
- [x] 6.2 Constrain output to the **fixed failure taxonomy + a confidence score**; **reject** an
      off-taxonomy / free-text label rather than recording it.
- [x] 6.3 Accept a **human-labeled calibration subset**; compute analyst **agreement** (κ / %
      agreement) with `n_human`; persist it.
- [x] 6.4 Report agreement **alongside every diagnosis** the analyst emits; **flag** an
      uncalibrated/below-floor analyst; mark low-confidence diagnoses.
- [x] 6.5 Enforce **no single unverified analyst opinion drives a change** — there is no apply path in
      P4.5; the diagnosis is a report only (the constraint binds P5.5).
- [x] 6.6 Rule-vs-analyst conflict resolution: the **deterministic rule wins**; log the analyst
      disagreement for calibration (design Q6).

## 7. AI Engineer — Pattern-scoped failure modes
- [x] 7.1 Read each node's **P3.5 structural pattern label**; select only the failure modes that
      pattern admits (Routing → misroutes; Planning → infeasible/circular plans; Reflection →
      non-convergence / degradation-on-revision; etc.).
- [x] 7.2 Refuse to diagnose a node with a failure mode its pattern cannot exhibit.
- [x] 7.3 Test: the Routing node is checked for misroutes and **not** a RAG failure mode; the
      Reflection node is checked for non-convergence/degradation-on-revision.

## 8. AI Engineer + System Designer — Read-only report data model
- [x] 8.1 Define **append-only** report tables (`attribution`, `failure_cluster`, `ablation_result`,
      `bottleneck_flag`, `diagnosis`, `analyst_cal`), each keyed `{variant_id, eval_set_hash,
      config_hash}`; store trace excerpts / analyst prompts / embeddings content-hashed in the object
      store (hashes only in the DB).
- [x] 8.2 **Structural read-only guarantee:** the schema has **no write path / FK-write** into any
      Variant Spec, registry, or node-config store; the engine's DB grant is write-only to the report
      tables, read on traces/eval results.
- [x] 8.3 Freeze the **diagnosis record schema** (taxonomy code + node + evidence `case_id`s +
      confidence + agreement) as the contract P5.5 operators consume (design Q7).
- [x] 8.4 Test (**load-bearing**): a full attribution + diagnosis run leaves every Variant Spec /
      registry / config **byte-identical** (same `config_hash`) and creates **zero** proposal records.

## 9. DevOps — Trace access, ablation fan-out, spend caps
- [x] 9.1 Provide **read-only trace access** to the attribution engine against the P2.5 span store /
      TSDB.
- [x] 9.2 Stand up **ablation fan-out** on the P4 run queue: bounded concurrency, backpressure,
      idempotent re-delivery (inherit P2 idempotency — no double-charge on redelivery).
- [x] 9.3 **Meter and cap analyst + ablation spend** per run; enforce rules-first so most cases cost
      nothing; surface spend.
- [x] 9.4 Ensure ablation re-runs execute **only in the P3 sandbox** with no ambient credentials;
      traces / analyst prompts with possible PII are content-hashed blobs, never inline in logs.

## 10. Frontend + Product — Read-only per-run scorecard
- [x] 10.1 Product: design the **diagnose journey** (failing variant → scorecard → localized node →
      clusters → typed cause with evidence). Frame the boundary — **this screen explains, it does not
      fix** — so the absent apply button reads as a guarantee. Design the unhappy paths first (no
      failing cases to cluster; analyst fails calibration; ablation inconclusive; node whose pattern
      admits no checked failure mode).
- [x] 10.2 Frontend: **per-run scorecard** — overall metrics; per-node breakdown (contribution,
      first-divergence, bottleneck flags); top failure clusters with sizes.
- [x] 10.3 Frontend: **diagnosis cards show *why*** — typed cause, source (rule vs. analyst),
      confidence + agreement, and the **specific failing cases as evidence** reachable in one click
      (never a bare label).
- [x] 10.4 Frontend: render ablation deltas **with CI** + inconclusive/bottleneck verdict; flag an
      uncalibrated analyst and low-confidence diagnoses visibly.
- [x] 10.5 Frontend: **no apply / change affordance** anywhere on the scorecard (deliberate absence —
      the phase is read-only).
- [x] 10.6 First-class states: loading / error / empty / **partial** (ablation fan-out in progress) /
      **inconclusive** / **uncalibrated**; read terminal status from persisted reports (no drifting
      derived state).
- [x] 10.7 **Accessibility & performance:** virtualize large node/cluster lists; keyboard-operable;
      cost-Pareto / per-node color via the **dataviz** skill for contrast + light/dark consistency.

## 11. Testing & review
- [x] 11.1 Fixtures: a multi-pattern workflow (Routing → per-branch Tool Use → Reflection) with a
      known fault at **one node** (node 3 drops the output contract → downstream parse fail); a failing
      eval set; a **multi-hop** and a **tool-returns-empty** cluster; a human-labeled diagnosis subset.
- [x] 11.2 **Read-only tests (load-bearing):** full run → every spec/registry/config byte-identical
      (same `config_hash`), zero proposal records; only ablation ephemeral variants enqueued, none
      persisted as user variants.
- [x] 11.3 Attribution tests: first-divergence names the faulty node; two distinct named clusters;
      ablation isolates the faulty node (non-overlapping-zero) and returns **inconclusive** on a
      non-faulty swap; bottleneck flags name the dominating node.
- [x] 11.4 Diagnosis tests: rules fire deterministically; analyst called **only on residue**;
      off-taxonomy analyst output **rejected**; agreement reported per diagnosis + below-floor analyst
      flagged; pattern-scoped (Routing not diagnosed with a RAG mode); evidence attached to every card.
- [x] 11.5 UI verification: drive the scorecard against a live (stubbed-provider) run; confirm per-node
      breakdown, clusters, bottleneck flags, diagnosis cards with evidence, ablation CI/verdict, and
      all states render; confirm **no apply affordance** exists.
- [x] 11.6 Confirm the M6 exit checklist (PRD §13) is green.

## 12. Framework-agnostic operation (FR13 / FR14 / G11)
- [x] 12.1 AI Engineer: derive locality (per-node contribution, first-divergence order, clustering,
      bottleneck flags) from **traces** (span start-time execution order), not IR edges — so the engine
      needs no discovered graph. *(Already implemented: `executionOrder` reads span start-times;
      `Attribute`/`Cluster`/`Bottleneck` take an optional IR.)*
- [x] 12.2 AI Engineer: contract-violation degrades when a node declares no output contract (falls back
      to span-error/failed; first-divergence to reference-mismatch or span failure); report no
      violation it cannot check. *(Already implemented: `contractViolated` returns false when
      `nodeOutputSchema` is nil.)*
- [x] 12.3 AI Engineer: an **unclassified** node runs **only pattern-agnostic** detectors and **refuses**
      pattern-scoped modes. *(Already implemented + verified: `AdmissibleOn(code, "")` admits only the
      pattern-agnostic codes; `Detect`/`Analyze` refuse inadmissible codes.)*
- [x] 12.4 AI Engineer: log a **WARN** on any silent fall-back to a default pattern (currently the
      engine simply omits the scoped checks; add the explicit WARN per `logging-conventions`).
- [x] 12.5 Frontend + Product: surface **"not classified · pattern-agnostic checks only"** on the
      per-node breakdown and diagnosis cards; show a classified-vs-unclassified node count so a user
      sees how much missing P3.5 labels cost their coverage (design Q9). *(Requires a `pattern` /
      `classified` field on `scorecard.NodeRow` + `DiagnosisCard`, currently absent.)*
- [x] 12.6 QA: **hand-rolled fixture** — a trace set whose IR has **no edges, no contracts, no pattern
      labels**; assert per-node breakdown + first-divergence (from trace order) + clusters + bottleneck
      flags are produced, pattern-agnostic detectors fire, pattern-scoped modes are refused, and each
      unclassified node reads "not classified". *(A minimal proof already passes; promote it into the
      committed suite and add the "enrichment sharpens, never gates" comparison.)*
- [x] 12.7 DevOps / cross-phase: **trace acquisition** for hand-rolled agents is now owned upstream —
      P1 §non-goals hands it to P2.5; **P2.5 FR17a/FR17b** define the two paths + default + min span shape.
      (Original wording:) decide **trace acquisition** for
      hand-rolled agents — auto-instrument discovered call sites vs. a declared node-boundary adapter;
      fix the minimum span shape P4.5 requires (design Q8). Surfaced as a user decision, owned upstream.

## 13. Effective-topology recovery from linkage signals (G12 / FR15, design Decision 8)
Recover a hand-rolled agent's node edges from signals **beyond** framework detection, tag each with a
provenance + confidence, and consume the highest-provenance edge set. Evidence: on a real hand-rolled
repo, framework detection found 0 edges yet the 40 LLM calls are linked by a dispatch→create call
graph, `messages`-append data flow, and a shared `_session_messages` conversation object.
- [x] 13.1 **Static recovery:** infer `inferred_static` edges between discovered call sites from (a) the
      **call graph** (fn with call A calls fn with call B; wrapper → primitive) and (c) **shared-state**
      (two calls read/write the same conversation/memory object), tagged `provenance=inferred_static` +
      confidence. **BUILT:** `internal/linkage/pyextract.go` — a real tree-sitter extractor recovering
      the signals from actual Python (`TestExtractPython_*`, proven on real hermes source: 6 real
      call-graph edges from `auxiliary_client.py`); persisted into the IR via `ToIREdges` (IR 1.2.0).
      *(b) full inter-procedural data-flow is the staged fidelity uplift, design Q11.)*
- [x] 13.2 **P2.5 (upstream, dynamic recovery):** emit `inferred_dynamic` edges from span parent-child
      nesting + shared conversation/thread id + temporal-and-data order. *(Owned by P2.5.)*
- [x] 13.3 **P4.5 (consumption):** order first-divergence and scope ablation's upstream-hold by the
      **highest-provenance** recovered edge set present in the IR; fall back to span start-time order
      only when no edge links the calls. *(Currently `executionOrder` uses start-time only; make it
      edge-aware when edges exist.)*
- [x] 13.4 **P4.5 (honesty):** carry each consumed edge's provenance + confidence through to the
      scorecard; render an `inferred_*` edge distinctly from a `framework` edge; never present an
      inferred edge as certain (FR15).
- [x] 13.5 **Frontend:** surface the recovered topology + edge provenance on the scorecard (an inferred
      chain shown as inferred), and the first-divergence path along it.
- [x] 13.6 **QA:** fixture with a recovered `inferred_static` chain A→B→C whose edge order differs from
      raw start-time → first-divergence orders by the edge DAG, provenance surfaced; edges removed →
      falls back to start-time and still localizes; an inferred edge is a hypothesis ablation upgrades.
- [x] 13.7 **AI/System (design):** fix the linkage-signal precedence + confidence and the static/dynamic
      reconciliation rule (design Q10); stage the minimum-viable edge set (shared-state + span
      parent-child) before the full data-flow graph (design Q11).
