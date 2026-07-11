# Tasks — P5.5: Proposal operators + Verification gate (advisory/assisted)

## 1. AI Engineer — Change-operator catalog
- [ ] 1.1 Define the `Operator(diagnosis, ir, registries) → []CandidateVariantSpec` interface: each
      operator declares the diagnosis type(s) it handles and the pattern label(s) it is admissible for.
- [ ] 1.2 Implement the catalog operators, one per source-plan row:
      **model-upgrade / enable-extended-thinking** (reasoning-heavy node on weak model),
      **model-downgrade** (cheap task on expensive model),
      **prompt-rewrite + format-constraint/schema** (prompt/output-contract violation),
      **context-policy switch (summarization / sliding window) / reorder** (context overflow / lost-in-middle),
      **RAG-tune (top-k / retriever / embedding / rerank)** (RAG relevance low),
      **add-skill / fix-schema-binding** (missing/erroring tool),
      **prune / merge** (redundant node).
- [ ] 1.3 Each operator emits one or more **candidate Variant Specs**, content-hashed (`config_hash`),
      referencing registry entries by ID (never injecting code).
- [ ] 1.4 **Operator gating:** emit an operator only where its candidate satisfies the P5 typed I/O
      contract (call `ContractValidate`; carry any flagged adapters) **and** the operator is admissible
      for the node's pattern label. Refuse to emit an inadmissible or contract-violating candidate.
- [ ] 1.5 Test: `add rerank` is emitted on a `Retrieval (RAG)` node and **not** on a `Routing` node;
      a candidate that would violate the typed I/O contract is **not** emitted.

## 2. AI Engineer — Grounded prompt optimization
- [ ] 2.1 Implement `PromptOptimize(node, failing_cases) → PromptEdit`: a **DSPy-style / self-refine**
      optimizer that proposes prompt edits **grounded in the specific failing cases** attached to the
      diagnosis, plus format-constraint/schema additions where the contract was violated.
- [ ] 2.2 Make the edit **traceable to the generating failing cases** (persist the grounding bundle,
      content-hashed); reject a generic rewrite that is not grounded in the attached cases.
- [ ] 2.3 Store optimizer inputs (failing-case traces, possible PII) and rendered candidate prompts as
      **content-hashed blobs**, never inline in logs.
- [ ] 2.4 Test: a prompt-rewrite candidate's edit is traceable to its attached failing cases; an
      ungrounded generic rewrite is rejected.

## 3. AI Engineer — Ranking under constraints
- [ ] 3.1 Implement `Rank(candidates, constraints) → []RankedProposal`: order by **expected gain /
      cost of change** (pre-verification estimate — see design Q2).
- [ ] 3.2 **Respect hard constraints:** a candidate that would violate the budget ceiling, latency
      SLA, or provider allowlist is **constraint-excluded** — not ranked as a recommendation; it MAY
      be listed separately with the violated constraint named.
- [ ] 3.3 Present each candidate as a **diff against the current Variant Spec** (reuse the P5
      Variant-Spec diff), with the originating **diagnosis** and the **specific failing cases**
      attached as evidence.
- [ ] 3.4 Test: ranking orders by expected gain / cost-of-change; a budget/latency/provider-violating
      candidate is constraint-excluded, not ranked #1.

## 4. AI Engineer — Verification gate (held-out + significance + regression)
- [ ] 4.1 Implement `Verify(proposal, eval_set, split) → Verdict`: **auto-execute** the candidate
      through the **P4 eval harness**, multi-seed, on the **held-out split** (cases the proposal was
      not generated from) when one exists; flag the result **not held-out** when no split exists.
- [ ] 4.2 **Significance gate:** reuse the P4 `Stats.Compare(candidate, baseline, metric)` primitive —
      admit only a **statistically-significant** gain (mean + CI + significance test); a CI-overlap
      **tie** does **not** pass.
- [ ] 4.3 **Regression check:** re-score the **other failure clusters** and confirm none degrades
      beyond a configured threshold; enforce the **cost/latency budget as a hard gate**. A proposal
      that improves its target but breaks another cluster, or that breaches the cost/latency budget,
      **fails** the regression check.
- [ ] 4.4 Emit the **verdict**: proposed change (diff), **measured delta with CI**, **cost/latency
      impact**, **cases fixed**, **cases broken**, `held_out` flag, and `gate_result ∈ {pass,
      fail_significance, fail_regression, fail_constraint}`.
- [ ] 4.5 **Nothing-unverified guarantee:** a proposal whose `gate_result ≠ pass` (or that never ran
      the gate) is **withheld** — the recommendation surface only ever reads gate-passing verdicts.
- [ ] 4.6 Test — **nothing-unverified:** a noise proposal (true-zero held-out delta) does **not**
      surface; an overfit proposal (wins on generating cases, ties on held-out) does **not** surface.
- [ ] 4.7 Test — **held-out:** the surfaced delta for a known-good proposal is the **held-out** delta;
      with no split, the verdict is flagged **not held-out**.
- [ ] 4.8 Test — **regression (cost):** a "fixed accuracy, tripled cost" proposal **fails** the
      regression check and the verdict shows the cost impact.
- [ ] 4.9 Test — **regression (cluster):** a "fixed cluster A, broke cluster B" proposal **fails**;
      the verdict lists **cases broken** alongside **cases fixed**.

## 5. System Designer + DevOps — Verification fan-out, budget/latency gates, data path
- [ ] 5.1 Fan verification out through the **P2 run queue**: bounded concurrency, backpressure,
      idempotent re-delivery (a re-verified `config_hash` does not double-charge). Order
      **cheapest-operator-first** (downgrade / prune before multi-candidate prompt sweeps).
- [ ] 5.2 **Cap verification spend per diagnosis/proposal batch** and surface it — proving proposals
      must not silently blow a budget.
- [ ] 5.3 Make the regression check's **cost/latency budget a hard gate** (mirroring the P4 gate
      discipline), so "fixed accuracy, tripled cost" fails deterministically.
- [ ] 5.4 Reuse the P4 `eval_result` substrate for verification runs (tagged with the candidate
      `config_hash`, `eval_set_hash`, `split`, `seed`); store proposals / evidence / verdicts / rank
      entries, not a second copy of the traces. Make every verdict attributable to an exact
      proposal × exact eval split.
- [ ] 5.5 Run candidate specs **only in the P3 sandbox** with no ambient credentials; keep optimizer
      grounding bundles / candidate prompts as content-hashed blobs.

## 6. Frontend + Product — Ranked recommendations, trend view, Advisory/Assisted UX
- [ ] 6.1 Product: design the **Advisory / Assisted automation-level model** — Advisory is the default
      (report a verified proposal, human applies); Assisted is an explicit **per-workflow opt-in**
      (one-click apply a **verified** proposal). Define how authority is granted and how the verified
      verdict earns trust. Design the unhappy path first: the all-failed **"no verified improvement
      found"** empty state, the regression-caught state (cases fixed *and* broken side by side), and
      the constraint-excluded state.
- [ ] 6.2 Frontend: **ranked recommendation list** — each card = **diagnosis + failing-case evidence +
      proposed diff (P5 diff component) + verified verdict** (delta ± CI, cost/latency impact, cases
      fixed / cases broken). Withheld (gate-failed) proposals are not in the list; if shown at all,
      they are a separate, clearly-labeled "did not pass verification" section.
- [ ] 6.3 Frontend: **trend view** across variants over time — did quality actually rise, or did the
      failure mass move from cluster A to cluster B? Reads structured verdicts/eval history, not a
      hand-written narrative.
- [ ] 6.4 Frontend: **Assisted one-click apply** — enabled **only** when `gate_result = pass`;
      disabled-with-reason otherwise. Apply **materializes** the candidate as a saved named Variant
      Spec (reversible; promotion to "active" is a separate step — see design Q7). An unverified
      proposal never presents an apply control.
- [ ] 6.5 First-class states: loading / **verifying** / **verified** / **gate-failed** / error —
      each visually distinct; **held-out** vs. **not-held-out** labelled; read terminal verdict status
      from persisted results (no derived state that drifts).
- [ ] 6.6 **Accessibility & performance:** virtualize large proposal lists; keyboard-operable cards +
      one-click apply; verdict charts (delta ± CI, cost/latency) via the **dataviz** skill for
      contrast + light/dark consistency.
- [ ] 6.7 Ensure human-readable **synthesis is narration over the structured verdict** — the verdict
      is the source of truth; the summary can never contradict or replace it.

## 7. Testing & review
- [ ] 7.1 Fixtures: a diagnosed multi-pattern workflow (Routing → per-branch Tool Use → Reflection,
      with a RAG node) carrying P4.5 diagnoses across the operator table; an eval set with a
      **train/held-out split**; a **known-good** proposal (real held-out gain), a **noise** proposal
      (true-zero held-out delta), an **overfit** proposal (wins-on-generating, ties-held-out), a
      **cost-regression** proposal (fixed accuracy, tripled cost), and a **cluster-regression**
      proposal (fixed cluster A, broke cluster B).
- [ ] 7.2 Operator tests: each diagnosis emits the catalog operator(s) + a contract-valid candidate;
      prompt rewrite is grounded + traceable; `add rerank` gated to the RAG node; contract-violating
      candidate not emitted.
- [ ] 7.3 Ranking test: order by expected gain / cost-of-change; constraint-violating candidate
      excluded, not #1.
- [ ] 7.4 Verification tests (core): nothing-unverified (noise + overfit withheld); held-out delta
      surfaced (else flagged); significance (CI-overlap tie fails, real gain passes); regression-cost
      (tripled-cost fails); regression-cluster (broke-B fails, cases broken listed); verdict contents
      (diff + delta±CI + cost/latency + cases fixed + cases broken).
- [ ] 7.5 Automation-level tests: Advisory reports without auto-apply; Assisted one-click apply
      offered **only** for a gate-passing proposal and creates a Variant Spec; gate-failed proposal
      offers no apply.
- [ ] 7.6 Trend-view test: across three iterations where cluster-A falls but cluster-B rises, the
      trend view shows the workflow did **not** globally improve (problems moved).
- [ ] 7.7 UI verification: drive ranked list + diff-with-evidence + trend + Advisory/Assisted screens
      against a live (stubbed-provider) verification fan-out; confirm all states, held-out labelling,
      cases-fixed/broken rendering, and that Assisted apply is gated on verification.
- [ ] 7.8 Confirm the M8 exit checklist (PRD §13) is green.
