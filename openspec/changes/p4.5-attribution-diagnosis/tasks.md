# Tasks — P4.5: Attribution + rule-based Diagnosis + scorecard (read-only)

## 1. AI Engineer — Per-node contribution & first-divergence
- [ ] 1.1 Implement `Attribute(variant, eval_set) → PerNodeContribution`: decompose end-to-end
      failure/cost/latency to individual nodes from the OTel traces + P4 per-node contribution signal.
- [ ] 1.2 Compute, per failing case, the node whose output **first diverges** from success
      (contract-violation first, then reference-mismatch where a gold reference exists — see design Q1).
- [ ] 1.3 Persist attribution rows keyed `{variant_id, eval_set_hash, config_hash, node_id, case_id}`;
      make per-node / per-case attribution a **query**, not a re-run.
- [ ] 1.4 Test: on a workflow with a fault injected at exactly one node, per-node contribution names
      that node as the first-divergence node on the failing cases.

## 2. AI Engineer — Failure clustering
- [ ] 2.1 Embed failing inputs + traces; cluster into **named categories** (design Q2: embedding
      choice + algorithm).
- [ ] 2.2 Emit `[]FailureCluster` each with a **size**, a **representative case**, and member
      `case_id`s; persist append-only.
- [ ] 2.3 Name each cluster (LLM-labeled — itself calibrated — or rule-derived from the dominant trace
      signature; design Q2).
- [ ] 2.4 Test: a fixture with a **multi-hop** fault cluster and a **tool-returns-empty** fault
      cluster yields two distinct named clusters with correct sizes.

## 3. AI Engineer — Ablation / counterfactual isolation
- [ ] 3.1 Implement `Ablate(variant, node, config') → AblationResult`: hold every **other** node's
      config fixed, swap exactly **one** node's config, re-run through the **P4 harness** multi-seed.
- [ ] 3.2 Report the measured delta **with its CI** (reuse the P4 `Stats.Compare` primitive); a delta
      whose CI **overlaps zero** → verdict `inconclusive`, else `bottleneck`.
- [ ] 3.3 Ablation runs are **ephemeral measurement variants** — enqueue them, never persist them as
      user variants, never apply them to the workflow.
- [ ] 3.4 Seed the ablation candidate set from the per-node contribution ranking (design Q3);
      run in the **P3 sandbox**, no ambient credentials.
- [ ] 3.5 Test: swapping the **faulty** node's config → non-overlapping-zero delta names it the
      bottleneck; swapping a **non-faulty** node → **inconclusive** (CI overlaps zero).

## 4. AI Engineer — Cost/latency bottleneck flags
- [ ] 4.1 Compute the cost/latency **Pareto across nodes** from the P2.5 per-node metrics.
- [ ] 4.2 Emit `[]BottleneckFlag` naming the node(s) that dominate spend or sit on the critical path,
      tagged with the dimension (cost | latency).
- [ ] 4.3 Test: the flag names the cost-dominating node and the latency-critical-path node.

## 5. AI Engineer — Rule-based detectors (rules-first)
- [ ] 5.1 Implement deterministic `Detect(trace) → []TypedCause` detectors, each emitting a **typed
      cause** from the fixed failure taxonomy:
      context overflow/truncation before a failing node; tool schema mismatch / repeated tool errors;
      retrieval miss (low-relevance chunks); prompt-format drift (output contract ignored → downstream
      parse fail); lost-in-the-middle (over-long context degrading a later node); model-capability
      mismatch (cheap model on a reasoning-heavy node).
- [ ] 5.2 Guarantee determinism: the same trace yields the same typed cause every run (no LLM in the
      rule path).
- [ ] 5.3 Detectors run **first** on every failing case; record which cases they explained.
- [ ] 5.4 Test: prompt-format-drift detector names the faulty node's cause on every run of the same
      trace; a context-overflow fixture trips the overflow detector.

## 6. AI Engineer — LLM-as-analyst (constrained + calibrated)
- [ ] 6.1 Invoke `Analyze(trace, rubric) → Diagnosis` **only on the residue** the rules didn't explain
      (assert analyst call count = residue count, not total failing count).
- [ ] 6.2 Constrain output to the **fixed failure taxonomy + a confidence score**; **reject** an
      off-taxonomy / free-text label rather than recording it.
- [ ] 6.3 Accept a **human-labeled calibration subset**; compute analyst **agreement** (κ / %
      agreement) with `n_human`; persist it.
- [ ] 6.4 Report agreement **alongside every diagnosis** the analyst emits; **flag** an
      uncalibrated/below-floor analyst; mark low-confidence diagnoses.
- [ ] 6.5 Enforce **no single unverified analyst opinion drives a change** — there is no apply path in
      P4.5; the diagnosis is a report only (the constraint binds P5.5).
- [ ] 6.6 Rule-vs-analyst conflict resolution: the **deterministic rule wins**; log the analyst
      disagreement for calibration (design Q6).

## 7. AI Engineer — Pattern-scoped failure modes
- [ ] 7.1 Read each node's **P3.5 structural pattern label**; select only the failure modes that
      pattern admits (Routing → misroutes; Planning → infeasible/circular plans; Reflection →
      non-convergence / degradation-on-revision; etc.).
- [ ] 7.2 Refuse to diagnose a node with a failure mode its pattern cannot exhibit.
- [ ] 7.3 Test: the Routing node is checked for misroutes and **not** a RAG failure mode; the
      Reflection node is checked for non-convergence/degradation-on-revision.

## 8. AI Engineer + System Designer — Read-only report data model
- [ ] 8.1 Define **append-only** report tables (`attribution`, `failure_cluster`, `ablation_result`,
      `bottleneck_flag`, `diagnosis`, `analyst_cal`), each keyed `{variant_id, eval_set_hash,
      config_hash}`; store trace excerpts / analyst prompts / embeddings content-hashed in the object
      store (hashes only in the DB).
- [ ] 8.2 **Structural read-only guarantee:** the schema has **no write path / FK-write** into any
      Variant Spec, registry, or node-config store; the engine's DB grant is write-only to the report
      tables, read on traces/eval results.
- [ ] 8.3 Freeze the **diagnosis record schema** (taxonomy code + node + evidence `case_id`s +
      confidence + agreement) as the contract P5.5 operators consume (design Q7).
- [ ] 8.4 Test (**load-bearing**): a full attribution + diagnosis run leaves every Variant Spec /
      registry / config **byte-identical** (same `config_hash`) and creates **zero** proposal records.

## 9. DevOps — Trace access, ablation fan-out, spend caps
- [ ] 9.1 Provide **read-only trace access** to the attribution engine against the P2.5 span store /
      TSDB.
- [ ] 9.2 Stand up **ablation fan-out** on the P4 run queue: bounded concurrency, backpressure,
      idempotent re-delivery (inherit P2 idempotency — no double-charge on redelivery).
- [ ] 9.3 **Meter and cap analyst + ablation spend** per run; enforce rules-first so most cases cost
      nothing; surface spend.
- [ ] 9.4 Ensure ablation re-runs execute **only in the P3 sandbox** with no ambient credentials;
      traces / analyst prompts with possible PII are content-hashed blobs, never inline in logs.

## 10. Frontend + Product — Read-only per-run scorecard
- [ ] 10.1 Product: design the **diagnose journey** (failing variant → scorecard → localized node →
      clusters → typed cause with evidence). Frame the boundary — **this screen explains, it does not
      fix** — so the absent apply button reads as a guarantee. Design the unhappy paths first (no
      failing cases to cluster; analyst fails calibration; ablation inconclusive; node whose pattern
      admits no checked failure mode).
- [ ] 10.2 Frontend: **per-run scorecard** — overall metrics; per-node breakdown (contribution,
      first-divergence, bottleneck flags); top failure clusters with sizes.
- [ ] 10.3 Frontend: **diagnosis cards show *why*** — typed cause, source (rule vs. analyst),
      confidence + agreement, and the **specific failing cases as evidence** reachable in one click
      (never a bare label).
- [ ] 10.4 Frontend: render ablation deltas **with CI** + inconclusive/bottleneck verdict; flag an
      uncalibrated analyst and low-confidence diagnoses visibly.
- [ ] 10.5 Frontend: **no apply / change affordance** anywhere on the scorecard (deliberate absence —
      the phase is read-only).
- [ ] 10.6 First-class states: loading / error / empty / **partial** (ablation fan-out in progress) /
      **inconclusive** / **uncalibrated**; read terminal status from persisted reports (no drifting
      derived state).
- [ ] 10.7 **Accessibility & performance:** virtualize large node/cluster lists; keyboard-operable;
      cost-Pareto / per-node color via the **dataviz** skill for contrast + light/dark consistency.

## 11. Testing & review
- [ ] 11.1 Fixtures: a multi-pattern workflow (Routing → per-branch Tool Use → Reflection) with a
      known fault at **one node** (node 3 drops the output contract → downstream parse fail); a failing
      eval set; a **multi-hop** and a **tool-returns-empty** cluster; a human-labeled diagnosis subset.
- [ ] 11.2 **Read-only tests (load-bearing):** full run → every spec/registry/config byte-identical
      (same `config_hash`), zero proposal records; only ablation ephemeral variants enqueued, none
      persisted as user variants.
- [ ] 11.3 Attribution tests: first-divergence names the faulty node; two distinct named clusters;
      ablation isolates the faulty node (non-overlapping-zero) and returns **inconclusive** on a
      non-faulty swap; bottleneck flags name the dominating node.
- [ ] 11.4 Diagnosis tests: rules fire deterministically; analyst called **only on residue**;
      off-taxonomy analyst output **rejected**; agreement reported per diagnosis + below-floor analyst
      flagged; pattern-scoped (Routing not diagnosed with a RAG mode); evidence attached to every card.
- [ ] 11.5 UI verification: drive the scorecard against a live (stubbed-provider) run; confirm per-node
      breakdown, clusters, bottleneck flags, diagnosis cards with evidence, ablation CI/verdict, and
      all states render; confirm **no apply affordance** exists.
- [ ] 11.6 Confirm the M6 exit checklist (PRD §13) is green.
