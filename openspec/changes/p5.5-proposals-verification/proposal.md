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

P5.5 turns diagnoses into **concrete source-code changes** and **proves each one before it reaches
the user**. Per **ADR-001**, applying a configuration means **transforming the user's source code**
(a deterministic, AST-level codemod) and delivering the change as a **reviewable diff / pull
request** — not resolving parameters through a runtime shim. A proposal is therefore both a candidate
**Variant Spec** (the canonical desired-state config) **and** the **concrete source diff** its
codemod produces, executed as the code that would actually ship. The AI Engineer's one law governs
the phase: **analysis without verification is confident guessing** — so the engine closes the loop by
making every proposal a Variant Spec whose transformed working copy the runtime executes and the P4
harness scores, so recommendations are **verified, not asserted**. Its load-bearing guarantee is
blunt: **nothing unverified reaches the user** — and, because editing user code is high blast radius,
**nothing reaches the user's repository except as a reviewable diff a human can read**. It ships two
automation levels — **Advisory** (open a draft PR / report the diff, human applies) and **Assisted**
(one-click open the verified pull request).

Depends on **P4** (the eval harness — multi-seed, mean + CI, `Stats.Compare` significance + tie
rule, disqualifying gates, `eval_set_hash`; **this is the verification engine**), **P4.5**
(attribution + diagnosis with failing cases as evidence — **the operator input**), **P5** (typed
per-node I/O contract + validator, the Variant-Spec diff view, behavioral pattern labels +
anti-pattern diagnoses that gate operators), **P2** (Runtime + registries + run queue + idempotency),
**P3** (sandbox), and **P2.5** (span/metric substrate). It also depends on the **source
transformation engine** (ADR-001): the deterministic AST codemod that rewrites the discovered call
sites, applied to an isolated worktree/branch, whose diff must build before it is surfaced. The
**Autonomous** loop — the full analyze → propose → verify → **apply** (open and **merge** a PR)
running unattended under hard constraints with kill switch, audit trail (git history + change
ledger), and rollback (`git revert`) — is deferred to **P6**; P5.5 stops at Assisted (human-initiated,
reversible one-click PR open — the human still merges).

## What Changes

- **New capability `proposal-engine`.** Each P4.5 diagnosis maps to one or more **change operators**
  per the catalog — reasoning-heavy-node-on-weak-model → **upgrade model / enable extended thinking**;
  cheap-task-on-expensive-model → **downgrade**; prompt/output-contract violation → **rewrite prompt +
  add format constraints/schema**; context overflow / lost-in-middle → **switch context policy
  (summarization / sliding window) or reorder**; RAG relevance low → **tune top-k / swap
  retriever/embedding / add rerank**; missing/erroring tool → **add skill from registry / fix schema
  binding**; redundant node → **prune / merge**. Each operator emits **candidate Variant Specs AND the
  concrete source diff** its **deterministic, AST-level codemod** produces at the discovered call
  sites (rewriting the model argument, prompt construction, tools/skills, context assembly, or node
  wiring to the spec's values). The transform is **deterministic** (same `config_hash` + same source →
  byte-identical diff), **behavior-preserving except for the intended change** (only the configured
  dimension at the targeted call site changes), and applied to an **isolated worktree/branch**, never
  the user's tree in place. A candidate whose diff **fails to build/compile the target is rejected
  before it is ever surfaced** (build-preserving).
  Prompt rewrites use a **DSPy-style / self-refine optimizer grounded in the specific failing cases**,
  not a generic "make it better," and the edit is traceable to those cases. Operators are **gated by
  the node's pattern label and the P5 typed I/O contract** — no `add rerank` on a non-RAG node, no
  contract-violating candidate. Candidates are **ranked by expected gain / cost of change**,
  respecting the user's **hard constraints** (budget ceiling, latency SLA, provider allowlist); a
  constraint-violating candidate is **not surfaced as a recommendation**. Each candidate is presented
  as a **reviewable source diff** (paired with the Variant-Spec diff) with the diagnosis and the
  **specific failing cases** attached as evidence.
- **New capability `verification`.** The gate **auto-executes the transformed working copy** (the
  codemod applied on an isolated worktree/branch — the code that would actually ship, not a shimmed
  run) against the eval dataset on a **held-out split** (cases the proposal was *not* generated from)
  where available, so the gain is not overfit to its generating cases (else the result is flagged
  **not held-out**). A candidate whose diff does not build is rejected earlier and never reaches the
  gate. A
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
  **Advisory** (open a **draft PR** / report the diff for the human to apply) and **Assisted**
  (**one-click open the verified pull request** — the human still reviews and merges), offered
  **only** for a proposal that passed the gate. In both levels the change reaches the repository only
  as a **reviewable diff/PR** a human reads; nothing merges without the verification gates and human
  approval.
- **UI.** Ranked recommendation list with diff-with-evidence cards showing the **reviewable source
  diff** (reusing the P5 Variant-Spec diff component alongside a source-code diff view), a trend view
  across variants, and the **Advisory/Assisted automation-level UX** — how authority is granted
  (Advisory default; Assisted explicit opt-in) and how one-click **open-PR** is **gated on
  verification**; large lists **virtualized**; verdict charts via the **dataviz** skill;
  loading / verifying / verified / **gate-failed** / error states first-class; held-out vs.
  not-held-out and cases-fixed/broken clearly rendered.
- **Deferred:** autonomous apply (open **and merge** a PR unattended) + the unattended
  analyze→propose→verify→apply loop + kill switch/audit trail (git history + change ledger)/rollback
  (`git revert`) + automated search (grid/Bayesian) + diagnosis-guided search + the
  production-failures-become-eval-cases feedback loop (all **P6**); the attribution/diagnosis engine
  itself (**P4.5**); the eval harness / statistics / composite score / coverage (**P4**).

## Impact

- **Affected capabilities:** `proposal-engine` (new), `verification` (new). Consumes `eval-harness`,
  `eval-set-generation`, `scoring` (P4), the attribution/diagnosis outputs (P4.5), the typed I/O
  contract + diff view + pattern labels (P5), the Runtime + registries (P2), the sandbox (P3), and
  the metrics substrate (P2.5).
- **Affected code/systems:** new change-operator catalog + dispatch (seven operator kinds, pattern-
  and contract-gated), each backed by a **deterministic AST-level codemod** that emits a concrete
  source diff at the discovered call sites; a **worktree/branch application layer** (isolated working
  copies, never the user's tree in place) with a **build/compile gate** that rejects a non-building
  diff before it is surfaced; a DSPy-style / self-refine prompt optimizer grounded in failing cases, a
  proposal ranker (expected gain / cost-of-change + hard-constraint filter), the verification gate
  (held-out auto-execution of the **transformed working copy** through the P4 harness, the reused
  significance primitive, the regression check over other clusters + a hard cost/latency budget), a
  verdict store, Postgres schema (proposals, evidence, verdicts, rank entries — reusing the P4
  `eval_result` substrate for the verification runs), object store (candidate **source diffs**,
  rendered prompts, optimizer grounding bundles, content-hashed), a **PR-open integration** (draft PR
  for Advisory; one-click verified PR for Assisted), run-queue fan-out + spend cap for verification,
  and a React ranked-recommendation + source-diff + trend-view + Advisory/Assisted UI.
- **Dependencies:** requires **P4**, **P4.5**, **P5**, **P2**, **P3**, **P2.5**. Unblocks **P6** (the
  autonomous optimizer is this analyze → propose → verify loop made unattended — reusing the operator
  catalog, codemod engine, ranker, and verification gate — plus automated search, hard-constraint
  gates, autonomous PR **merge**, kill switch, audit trail (git history + change ledger), and rollback
  (`git revert`)).
