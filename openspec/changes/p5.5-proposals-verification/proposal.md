## Why

After P4.5 the platform localizes a failure to a node + dimension and attaches a named, typed cause
with the specific failing cases as evidence — but it is **read-only**. It says *what* is wrong and
*why*; it does not say *what to change*, and it does not *prove* that a change helps. Diagnoses die
as prose: the user is left to guess the fix, hand-edit a Variant Spec, and re-run it. Any fix the
engine did suggest would be **asserted, not verified** — exactly the "confident guessing" the
architecture is built to avoid, and LLM-generated suggestions are especially prone to it. The
classic traps go uncaught: a change that **fixes accuracy but triples cost**, or fixes one failure
cluster while silently breaking another, or a prompt "improvement" that **overfits to the handful of
failing cases that generated it** and does nothing on unseen inputs.

P5.5 turns diagnoses into **concrete Variant-Spec changes** and **proves each one before it reaches
the user**. The AI Engineer's one law governs the phase: **analysis without verification is
confident guessing** — so the engine closes the loop by making every proposal a Variant Spec the
runtime executes and the P4 harness scores, so recommendations are **verified, not asserted**. Its
load-bearing guarantee is blunt: **nothing unverified reaches the user.** It ships two automation
levels — **Advisory** (report, human applies) and **Assisted** (one-click apply a verified
proposal).

Depends on **P4** (the eval harness — multi-seed, mean + CI, `Stats.Compare` significance + tie
rule, disqualifying gates, `eval_set_hash`; **this is the verification engine**), **P4.5**
(attribution + diagnosis with failing cases as evidence — **the operator input**), **P5** (typed
per-node I/O contract + validator, the Variant-Spec diff view, behavioral pattern labels +
anti-pattern diagnoses that gate operators), **P2** (Runtime + registries + run queue + idempotency),
**P3** (sandbox), and **P2.5** (span/metric substrate). The **Autonomous** loop — the full
analyze → propose → verify → **apply** running unattended under hard constraints with kill switch,
audit trail, and rollback — is deferred to **P6**; P5.5 stops at Assisted (human-initiated, reversible
one-click apply).

## What Changes

- **New capability `proposal-engine`.** Each P4.5 diagnosis maps to one or more **change operators**
  per the catalog — reasoning-heavy-node-on-weak-model → **upgrade model / enable extended thinking**;
  cheap-task-on-expensive-model → **downgrade**; prompt/output-contract violation → **rewrite prompt +
  add format constraints/schema**; context overflow / lost-in-middle → **switch context policy
  (summarization / sliding window) or reorder**; RAG relevance low → **tune top-k / swap
  retriever/embedding / add rerank**; missing/erroring tool → **add skill from registry / fix schema
  binding**; redundant node → **prune / merge**. Each operator emits **candidate Variant Specs**.
  Prompt rewrites use a **DSPy-style / self-refine optimizer grounded in the specific failing cases**,
  not a generic "make it better," and the edit is traceable to those cases. Operators are **gated by
  the node's pattern label and the P5 typed I/O contract** — no `add rerank` on a non-RAG node, no
  contract-violating candidate. Candidates are **ranked by expected gain / cost of change**,
  respecting the user's **hard constraints** (budget ceiling, latency SLA, provider allowlist); a
  constraint-violating candidate is **not surfaced as a recommendation**. Each candidate is presented
  as a **diff against the current Variant Spec** with the diagnosis and the **specific failing cases**
  attached as evidence.
- **New capability `verification`.** The gate **auto-executes** each proposal against the eval
  dataset on a **held-out split** (cases the proposal was *not* generated from) where available, so
  the gain is not overfit to its generating cases (else the result is flagged **not held-out**). A
  **statistical significance gate** (multi-seed, CI, significance vs. baseline — reusing the P4
  `Stats.Compare` primitive) admits only statistically-real gains; a CI-overlap **tie** does not pass.
  A **regression check** confirms the proposal did **not degrade other failure clusters** and did
  **not breach the cost/latency budget** — catching **"fixed accuracy, tripled cost"** and "fixed
  cluster A, broke cluster B"; the cost/latency budget is a **hard gate**, not a soft penalty. Every
  verified proposal carries a **verdict**: the proposed change (diff), the **measured delta with CI**,
  the **cost/latency impact**, the **cases fixed**, and the **cases broken**. **Nothing unverified
  surfaces** — a proposal that fails held-out / significance / regression / a hard constraint is
  **withheld**, never rendered as a recommendation. Verified proposals appear in a **ranked
  recommendation list** (diagnosis + evidence + diff + verified delta) with a **trend view** across
  variants (did the workflow improve or did problems just move?); human-readable synthesis is
  **narration over the structured verdict, never the source of truth**. Two automation levels:
  **Advisory** (report, human applies) and **Assisted** (one-click apply a verified proposal),
  offered **only** for a proposal that passed the gate.
- **UI.** Ranked recommendation list with diff-with-evidence cards (reusing the P5 Variant-Spec diff
  component), a trend view across variants, and the **Advisory/Assisted automation-level UX** — how
  authority is granted (Advisory default; Assisted explicit opt-in) and how one-click apply is
  **gated on verification**; large lists **virtualized**; verdict charts via the **dataviz** skill;
  loading / verifying / verified / **gate-failed** / error states first-class; held-out vs.
  not-held-out and cases-fixed/broken clearly rendered.
- **Deferred:** autonomous apply + the unattended analyze→propose→verify→apply loop + kill
  switch/audit trail/rollback + automated search (grid/Bayesian) + diagnosis-guided search + the
  production-failures-become-eval-cases feedback loop (all **P6**); the attribution/diagnosis engine
  itself (**P4.5**); the eval harness / statistics / composite score / coverage (**P4**).

## Impact

- **Affected capabilities:** `proposal-engine` (new), `verification` (new). Consumes `eval-harness`,
  `eval-set-generation`, `scoring` (P4), the attribution/diagnosis outputs (P4.5), the typed I/O
  contract + diff view + pattern labels (P5), the Runtime + registries (P2), the sandbox (P3), and
  the metrics substrate (P2.5).
- **Affected code/systems:** new change-operator catalog + dispatch (seven operator kinds, pattern-
  and contract-gated), a DSPy-style / self-refine prompt optimizer grounded in failing cases, a
  proposal ranker (expected gain / cost-of-change + hard-constraint filter), the verification gate
  (held-out auto-execution driving the P4 harness, the reused significance primitive, the regression
  check over other clusters + a hard cost/latency budget), a verdict store, Postgres schema
  (proposals, evidence, verdicts, rank entries — reusing the P4 `eval_result` substrate for the
  verification runs), object store (candidate diffs, rendered prompts, optimizer grounding bundles,
  content-hashed), run-queue fan-out + spend cap for verification, and a React ranked-recommendation +
  trend-view + Advisory/Assisted UI.
- **Dependencies:** requires **P4**, **P4.5**, **P5**, **P2**, **P3**, **P2.5**. Unblocks **P6** (the
  autonomous optimizer is this analyze → propose → verify loop made unattended — reusing the operator
  catalog, ranker, and verification gate — plus automated search, hard-constraint gates, kill switch,
  audit trail, and rollback).
