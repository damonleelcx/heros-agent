## Why

After P4 a comparison is honest — two Variant Specs run multi-seed over a coverage-measured eval set,
every metric carries a confidence interval, ties are declared when CIs overlap, and a **per-node
contribution** signal is already emitted from the traces. But a workflow owner still only knows
*that* a variant scores 60%, not **which node** is responsible, whether the failures are one category
or ten, or **why** the node fails. Aggregate scores hide locality; failures are treated as one-offs;
causation is asserted rather than isolated; and any LLM-generated explanation is un-calibrated
guessing.

P4.5 closes that gap **read-only**: it localizes an end-to-end failure/cost/latency to individual
nodes and attaches a **named, typed cause** — but it **proposes nothing and changes nothing**. It
delivers an **attribution engine** (per-node contribution + first-divergence, failure clustering,
ablation/counterfactual isolation, cost/latency bottleneck flags) and a **diagnosis engine**
(rules-first deterministic detectors over traces, then an LLM-as-analyst constrained to a fixed
failure taxonomy for the fuzzy residue), surfaced as a **per-run scorecard**. The AI Engineer's law
governs it: **diagnosis proposes; verification decides** — proposals + the verification gate are
P5.5, autonomous apply is P6. LLM-as-analyst is noisy and biased, so it is constrained to a fixed
taxonomy + confidence, calibrated against a human-labeled subset, and its agreement is reported
alongside every diagnosis; no single unverified analyst opinion drives a change.

Depends on P0 (`workflow-ir.schema.json` typed I/O contract + `config_hash`; `metric-event.schema.json`
tag set), P2 (Runtime + run queue + idempotency, reused for ablation re-runs), P2.5 (OTel spans +
operational metrics the attribution engine reads), P3 (sandbox for ablation re-runs), P3.5 (the
structural pattern label that scopes which failure modes to check), and **P4** (eval harness + eval
results + per-node contribution signal + the multi-seed / CI / tie primitive that attribution and
ablation reuse). Behavioral pattern re-classification (Reflection/Planning/HITL from runtime) is P5;
P4.5 consumes the P3.5 structural label as-is.

## What Changes

- **New capability `attribution`.** For a failing variant, **decomposes end-to-end
  failure/cost/latency to individual nodes** from the OTel traces and identifies, per failing case,
  the node whose output **first diverges** from success. **Clusters failing cases** (embed inputs +
  traces) into **named categories** ("fails on multi-hop" vs. "fails when a tool returns empty") with
  sizes and representative cases, so fixes target categories not one-offs. Performs
  **ablation/counterfactual isolation** — hold every other node fixed, swap exactly one node's
  config, re-run through the P4 harness **multi-seed**, report the measured delta **with its CI**;
  a delta whose CI overlaps zero is **inconclusive**, not a bottleneck; ablation variants are
  **ephemeral measurement runs, never applied** to the user's workflow. Flags cost/latency
  **bottlenecks** from the per-node Pareto. Emits a read-only **per-run scorecard** (overall metrics,
  per-node breakdown, top failure clusters).
- **New capability `diagnosis`.** **Rule-based detectors** over traces (deterministic, cheap,
  rules-first) emitting a **typed cause** from the fixed failure taxonomy — context overflow /
  truncation before a failing node; tool schema mismatch / repeated tool errors; retrieval miss
  (low-relevance chunks); prompt-format drift (output contract ignored → downstream parse fail);
  lost-in-the-middle; model-capability mismatch. An **LLM-as-analyst** runs **only on the residue**
  the rules don't explain, **constrained to the fixed failure taxonomy + a confidence score** (an
  off-taxonomy / free-text label is rejected, not recorded). The analyst is **calibrated against a
  human-labeled subset**, its **agreement reported alongside every diagnosis**, and an
  uncalibrated/below-floor analyst is flagged — **no single unverified analyst opinion drives a
  change**. Diagnosis is **pattern-scoped**: only the failure modes a node's P3.5 pattern admits are
  checked (Routing → misroutes; Planning → infeasible/circular; Reflection →
  non-convergence/degradation-on-revision). Every diagnosis attaches the **specific failing cases as
  evidence**, not just a label. **Read-only guarantee:** the engine mutates no Variant Spec, registry,
  config, or workflow and emits **no proposal**.
- **UI.** A read-only **per-run scorecard**: overall metrics, per-node breakdown (contribution,
  first-divergence, bottleneck flags), top failure clusters, and **diagnosis cards that show *why***
  — typed cause, source (rule vs. analyst), confidence + agreement, and the failing cases as evidence
  in one click. Ablation deltas render with their CI + inconclusive/bottleneck verdict; large lists
  virtualized; encodings via the **dataviz** skill; loading / error / empty / **partial** (ablation
  in progress) / **inconclusive** / **uncalibrated** states first-class. Deliberately **no apply /
  change affordance** — the phase is read-only.
- **Effective-topology recovery (beyond framework detection).** "No framework graph" is not "no
  topology": a hand-rolled agent's LLM calls are linked by the code (a **static call graph** — a
  dispatch forwards to the create boundary, a fallback wraps the primary; **data flow** — a response
  is appended to `messages` and becomes the next prompt; a **shared conversation/memory object** every
  call reads and writes) and by the runtime traces (span **parent-child** nesting, a shared
  **thread/conversation id**, **temporal + data** order). The system recovers the effective node graph
  from **whichever linkage signals exist**, ranked by provenance — `framework` (strongest) >
  `inferred_static` (P1: call-graph/data-flow/shared-state) > `inferred_dynamic` (P2.5:
  span/thread/temporal) > flat trace-order (last resort). Each recovered edge is a **provenance-tagged
  hypothesis**, never asserted as certain; ablation upgrades an inferred-edge localization to a
  measured cause. Edge **recovery** is owned upstream (P1 static, P2.5 dynamic); **P4.5 consumes** the
  recovered edges — ordering first-divergence and scoping ablation by the highest-provenance edge set —
  and surfaces each edge's provenance on the scorecard.
- **Framework-agnostic operation.** Attribution and diagnosis derive locality from the **traces**
  (span attributes + execution order) and the recovered topology above, not from an assumed framework
  graph — so the engine works for **any agent that emits run/node/tool spans**, graph-framework or
  **hand-rolled loop**. The Workflow IR is **optional enrichment**: its edges sharpen ordering, its per-node output
  contracts enable contract-violation detection, and its P3.5 pattern labels enable pattern-scoped
  diagnosis — but none is required to produce a scorecard. When enrichment is absent the engine
  **degrades explicitly**: first-divergence from trace order; contract-violation only where a node
  declares a contract (else span-status); and for an **unclassified** node, only the pattern-agnostic
  detectors, with the node surfaced as **"not classified"** and pattern-scoped modes refused, never
  silently misapplied (`no-lazy-defaults`). The single hard prerequisite is **trace acquisition** —
  for a hand-rolled agent the spans come from auto-instrumenting the discovered call sites or a thin
  user-declared node-boundary adapter, a P1/P2.5 concern P4.5 consumes but does not own.
- **Deferred:** change operators + proposal generation (**P5.5**); the verification gate — held-out
  re-run, statistical gate, regression check, verdict (**P5.5**); automation levels (Advisory /
  Assisted) + autonomous apply (**P5.5 / P6**); behavioral pattern re-classification + anti-pattern →
  operator wiring (**P5 / P5.5**); ranked recommendation list + trend view across variants over time
  (**P5.5+**); human-readable narrative synthesis over the structured results (**P5.5**).

## Impact

- **Affected capabilities:** `attribution` (new), `diagnosis` (new). Consumes `workflow-ir` +
  `config_hash`/tag contracts (P0), the Runtime + run queue (P2, reused for ablation), the metrics
  substrate + traces (P2.5), the sandbox (P3), the pattern label (P3.5), and the eval harness + eval
  results + per-node contribution + CI/tie primitive (P4).
- **Affected code/systems:** new attribution engine (per-node contribution / first-divergence,
  failure clustering with embeddings, ablation runner over the P4 harness, cost/latency Pareto
  bottleneck flags), rule-based diagnostic detector suite (deterministic, over traces),
  LLM-as-analyst module (fixed-taxonomy-constrained output + confidence + human-subset calibration +
  agreement reporting), pattern-scoped failure-mode selector (reads the P3.5 label), append-only
  Postgres report schema (attribution / failure_cluster / ablation_result / bottleneck_flag /
  diagnosis / analyst_cal — **no write path to Variant Spec / registry / config**), object store for
  trace excerpts / analyst prompts / embeddings (content-hashed), ablation fan-out on the P4 run
  queue (bounded concurrency, spend cap, P3 sandbox), and a React read-only per-run scorecard UI.
- **Framework-agnostic note:** the hard input is P2.5 **traces**; the IR (edges / contracts) and P3.5
  **pattern labels** are optional enrichment the engine degrades without (FR13/FR14). Trace
  acquisition for hand-rolled (non-framework) agents — auto-instrumenting discovered call sites vs. a
  declared node-boundary adapter — is a **P1/P2.5** concern this phase consumes, not a P4.5 deliverable.
- **Dependencies:** requires **P0**, **P2**, **P2.5** (hard: traces), **P3**, **P4**; **P3.5** pattern
  labels and IR edges/contracts are **optional enrichment**, not hard requirements. Unblocks **P5.5**
  (typed diagnoses → change operators; ablation machinery → verification gate; fixed taxonomy → the
  operator map's key), **P6** (node+dimension attribution points diagnosis-guided search), and the
  **P5** behavioral classifier's anti-pattern detections (plug into the same pattern-scoped
  failure-mode framework).
