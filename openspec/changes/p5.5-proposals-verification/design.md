# Design — P5.5: Proposal operators + Verification gate (advisory/assisted)

Cross-reference: product rationale in [`../../../docs/prd/P5.5-proposals-verification.md`](../../../docs/prd/P5.5-proposals-verification.md).

## Context

P5.5 is where the AI Engineer playbook's sharpest law becomes the deliverable itself: **analysis
without verification is confident guessing**, and LLM-generated analysis is especially prone to it.
P4.5 produces read-only diagnoses (node + dimension + typed cause + failing cases); P5.5 turns each
into a **concrete source-code change** — an AST-level codemod delivered as a **reviewable diff / pull
request** (ADR-001) — and **proves it before it surfaces**. Applying a Variant Spec no longer means a
runtime shim resolving parameters; it means **rewriting the discovered call sites in source** so the
verified code is the code that ships. Four forces shape every decision. First, a recommendation must
be **executed, not asserted** — so every proposal is a Variant Spec whose transformed working copy
the P2 runtime runs and the P4 harness scores. A related, ADR-mandated force: because editing user
code is high blast radius, a proposal's diff must be **AST-level + deterministic**, **build-
preserving** (a diff that fails to compile is rejected before it is surfaced), **behavior-preserving
except for the intended change**, **applied to an isolated worktree/branch**, and **always
reviewable** (nothing reaches the repo except as a diff a human reads). Second, an LLM-driven fix (a
prompt rewrite) will **overfit** to the cases that generated it — so it must be tested on **held-out**
cases
and its gain must clear the **same statistical bar** the leaderboard uses. Third, a fix that helps
its target can **harm elsewhere** ("fixed accuracy, tripled cost"; "fixed cluster A, broke cluster
B") — so a **regression check** with a hard cost/latency budget stands between the proposal and the
user. The phase reuses machinery already built: the P4 harness + `Stats.Compare` primitive are the
verification engine, the P5 typed I/O contract validates candidate diffs, the P3.5/P5 pattern label
gates operators, the P2 queue + idempotency drive verification fan-out, and the **source
transformation engine** (ADR-001) is the codemod that turns a Variant Spec into the reviewable source
diff. The **nothing-unverified-surfaces** guarantee — now joined by **nothing reaches the repo except
as a reviewable, building diff** — is the property the whole phase exists to enforce.

## Decision 1 — A proposal is a Variant Spec *and* the source diff its codemod emits; the engine closes the loop

**Decision.** A proposal is not an opinion about a change — it **is** a candidate Variant Spec,
content-hashed (`config_hash`) like any other, produced by a **change operator**, **compiled to a
concrete source diff by a deterministic AST-level codemod** (ADR-001), applied to an isolated
worktree/branch, and executed there by the existing runtime. Verification is therefore "build the
transformed working copy for `config_hash` X and run the P4 harness on the held-out split"; a verdict
is attributable to an exact proposal × exact source diff × exact eval split.

**Why.** This is the architectural backbone of the improvement engine (source-plan §1: *the engine
must close the loop — every proposed improvement is itself a Variant Spec the runtime can execute, so
recommendations are verified, not asserted*), now made faithful by ADR-001: the runtime executes the
**real, transformed source** rather than a shimmed run, so measured cost/latency/quality reflect the
code that would ship. Making the proposal a first-class Variant Spec *with a reviewable diff* means
P5.5 adds operators + a codemod + a gate on top of P4/P5 rather than a parallel evaluation path — and
it is exactly what lets P6 turn this loop autonomous by adding search + guardrails + PR merge, not a
rewrite.

**Alternative rejected.** Emitting a free-text "suggestion" the user manually translates into a spec —
un-executable, un-verifiable, and the definition of "confident guessing." Also rejected (ADR-001): a
runtime shim resolving parameters without editing source — infeasible for compiled targets (Go) and
it measures a code path that will never ship.

## Decision 2 — Operators are pattern- and contract-gated

**Decision.** Each P4.5 diagnosis maps (via the source-plan catalog) to one or more operators, each
`Operator(diagnosis, ir, registries) → []CandidateVariantSpec`. An operator is emitted only where
**valid**: the candidate must satisfy the **P5 typed I/O contract** (contract-valid diff, adapters
flagged as in P5), and the operator must be **admissible for the node's pattern label** (P3.5/P5).

**Why.** An operator firing where it makes no sense produces a nonsensical recommendation — `add
rerank` on a `Routing` node, `add a critic` on a subgraph that has no Reflection to critique. The
pattern label is the dispatcher (same discipline as P4's pattern-scoped metrics and P4.5's
pattern-scoped failure taxonomy): it keeps the proposal set valid per subgraph. Routing candidate
diffs through the P5 contract validator means a proposed change that would break a downstream node's
typed input is **never emitted**, not caught later.

**The catalog (source-plan §4, made operational):**

| Diagnosis | Operator(s) | Emits |
|---|---|---|
| Reasoning-heavy node on weak model | Upgrade model / enable extended thinking | candidate(s) with stronger `model_ref` / thinking budget on that node |
| Cheap task on expensive model | Downgrade | candidate with a cheaper `model_ref` at equal expected quality |
| Prompt / output-contract violation | Rewrite prompt (grounded) + add format constraint/schema | candidate with a new `prompt_ref` + output schema |
| Context overflow / lost-in-middle | Switch context policy → summarization / sliding window; reorder | candidate with a new `context_policy` or node order |
| RAG relevance low | Tune top-k / swap retriever/embedding / add rerank | candidate(s) with retrieval params / a rerank node |
| Missing / erroring tool | Add skill from registry / fix schema binding | candidate with a `skill_ref` added / a corrected binding |
| Redundant node | Prune / merge | candidate with the node removed / merged |

## Decision 3 — Prompt optimization is grounded and traceable

**Decision.** Prompt-rewrite operators run a **DSPy-style / self-refine** optimizer,
`PromptOptimize(node, failing_cases) → PromptEdit`, over the **specific failing cases** the P4.5
diagnosis attached — not a generic "make it better." The produced edit is **traceable** to the
failing cases that motivated it (the grounding bundle is persisted, content-hashed), and an
ungrounded generic rewrite is rejected.

**Why.** Source-plan §4: *use a DSPy-style or self-refine optimizer that proposes prompt edits
grounded in the failing cases, not generic "make it better."* A generic rewrite is unfalsifiable and
usually inert; grounding in the actual failures is what makes the edit targeted — and traceability is
what lets verification later attribute a held-out gain to a specific, motivated change rather than
noise.

**Trade-off.** Grounding requires the failing-case traces (possible PII) as optimizer input — stored
as content-hashed blobs, never in logs, and executed only in the P3 sandbox.

## Decision 4 — Rank by expected gain / cost-of-change, hard constraints filter

**Decision.** Candidates are ranked by **expected gain / cost of change** and filtered by the user's
**hard constraints** (budget ceiling, latency SLA, provider allowlist). A candidate that would
violate a hard constraint is **constraint-excluded** — not ranked as a recommendation — though it may
be listed separately with the violated constraint named. Pre-verification, "expected gain" is a cheap
estimate (operator-prior × cluster severity — Q2); post-verification, the rank uses the **measured
verdict**.

**Why.** Source-plan §4: *rank proposals by expected gain / cost of change, respecting user-set
constraints.* This mirrors P4's **gates-not-penalties** discipline: a constraint violation
**disqualifies** a candidate from the recommendation surface rather than merely lowering its score,
so a promising-but-inadmissible proposal (e.g. a model upgrade that blows the budget) can never
become the top pick. Ranking by gain *per unit cost-of-change* keeps a marginally-better,
hugely-expensive change from out-ranking a cheap, solid one.

## Decision 5 — Held-out auto-execution avoids overfitting

**Decision.** The verification gate **auto-executes** each candidate through the P4 harness,
multi-seed, on a **held-out split** — the cases the proposal was *not* generated from — whenever such
a split exists. The surfaced delta is the **held-out** delta. When no split exists, the verdict is
flagged **not held-out** (the gain is still subject to the significance + regression gates, but its
generalization is unproven).

**Why.** Source-plan §5: *auto-execute each proposal against the same eval dataset, held-out where
possible to avoid overfitting the recommendation to the cases that generated it.* A prompt tuned on
its generating cases will look excellent on exactly those cases — that is memorization, not
improvement. Testing on held-out cases turns the delta into a generalization estimate. This is a
first-class, tested property: an **overfit proposal** (wins on generating cases, ties on held-out)
must **not** pass.

**Split ownership (Q1).** Proposed: P4.5 tags the cases that generated a diagnosis; P5.5 verifies on
the complement, topping up from the P4 generator if the held-out slice is underpowered for the
significance test. Left open pending the fixture's case counts.

## Decision 6 — The significance gate is the P4 primitive, reused

**Decision.** Verification admits a proposal only when its improvement over the baseline variant is
**statistically significant** — multi-seed, mean + CI, significance test — by calling the **same
`Stats.Compare(candidate, baseline, metric)`** the P4 leaderboard uses. A CI-overlap result is a
**tie** and **does not pass**.

**Why.** "Improvement" must mean one thing everywhere. Re-implementing the stats here would risk a
second, laxer definition of "better" that lets noise through on the recommendation path. Reusing the
primitive means the tie-on-overlapping-CIs rule — the property that stops the platform ranking noise —
governs proposals too. Tested claim: a **noise proposal** (true-zero held-out delta) returns `tie`
and is withheld.

**Targeted vs. composite (Q6).** Proposed: require a significant gain on the **targeted metric** *and*
no **composite-score** regression — the targeted win must not be a net loss under the active weight
profile.

## Decision 7 — The regression check catches "fixed accuracy, tripled cost"

**Decision.** Before a proposal passes, the gate runs a **regression check**: (a) re-score the
**other failure clusters** and confirm none degrades beyond a configured threshold; (b) enforce the
**cost/latency budget as a hard gate** (not a soft penalty). A proposal that fixes its target cluster
but **breaks another**, or that improves quality while **breaching the cost/latency budget**,
**fails** the check.

**Why.** Source-plan §5: *confirm the fix didn't degrade other case clusters or blow the cost/latency
budget (a common failure — fixing accuracy by silently 3×-ing cost).* Passing the target cluster is
necessary but not sufficient. Making cost/latency a **hard gate** (mirroring P4) means "fixed
accuracy, tripled cost" fails **deterministically**, not "scores slightly lower." The verdict reports
**cases fixed *and* cases broken** so the trade is legible even when the net is positive.

**Cluster scope (Q3).** Proposed: re-score all clusters on the affected path, plus a cheap global
cost/latency budget check over the full set.

## Decision 8 — Nothing unverified surfaces; the verdict is the source of truth

**Decision.** A proposal is presented to the user as a recommendation **only** when its verdict's
`gate_result = pass` (held-out where available + significant + regression-clean + constraint-clean).
A proposal that failed any gate — or never ran it — is **withheld**. The recommendation surface reads
only gate-passing verdicts. Human-readable synthesis **narrates** the structured verdict; the verdict
is the source of truth and the summary can never replace or contradict it.

**Why.** This is the phase's load-bearing guarantee and the M8 exit criterion: *nothing unverified
reaches the user.* Source-plan closing line: *analysis without verification is just confident
guessing … every proposal is re-run and measured before it reaches the user.* Making "surface" a
predicate over the verdict (not a UI choice) means the guarantee cannot be bypassed by a rendering
bug or an eager summary.

**Verdict schema:**

```
Verdict = {
  proposal_id, diff_blob_hash,
  metric, delta, ci_low, ci_high, significant BOOL,      // held-out where available
  held_out BOOL,
  cost_delta, latency_delta,
  cases_fixed[], cases_broken[],
  regression_pass BOOL,
  gate_result ∈ { pass, fail_significance, fail_regression, fail_constraint }
}
```

## Decision 9 — Two automation levels; Assisted is gated on verification

**Decision.** P5.5 ships **Advisory** (open a **draft PR** / report the verified diff; the human
applies and merges) and **Assisted** (**one-click open the verified pull request**; the human still
reviews and merges). **Assisted PR-open is offered only when `gate_result = pass`.** Apply
**opens a pull request** carrying the codemod's source diff against the user's repo — it never merges
to the default branch and never mutates the working tree in place; merge (and thus activation) is the
human's explicit step (Q7). Rollback is `git revert`; the audit trail is git history. Advisory is the
default; Assisted is an explicit per-workflow opt-in.

**Why.** Source-plan §7: Advisory = engine reports, human applies; Assisted = one-click apply a
verified proposal. Each level is a distinct **trust contract** (Product). Gating one-click PR-open on
the verdict means the convenience of Assisted never becomes a channel for shipping an unverified
change. Keeping apply to *open a PR the human merges* (not auto-merge) keeps Assisted reversible and
leaves unattended **merge** + the loop to **P6-Autonomous** — which adds the kill switch, audit trail
(git history + change ledger), and rollback (`git revert`) that unattended merge requires.

**Boundary with P6.** P5.5 deliberately stops at human-initiated apply (open a PR; the human merges).
The full unattended analyze → propose → verify → apply loop (open **and merge** a PR), automated
search, and the operational guardrails (kill switch / audit trail = git history + change ledger /
rollback = `git revert` / min-improvement + max-iteration gates) are **P6**.

## Decision 10 — The codemod is deterministic, build-preserving, isolated, and reviewable (ADR-001)

**Decision.** The transform that turns a candidate Variant Spec into a source change is a
**deterministic, AST-level codemod**, not string substitution: the same `config_hash` against the
same source produces a **byte-identical diff**, content-hashed for reproducibility. It changes only
the configured dimension(s) at the targeted call site(s) (**behavior-preserving except for the
intended change**; no incidental edits). It is applied to an **isolated worktree/branch**, never the
user's working tree in place. Before a candidate is surfaced, its diff must **build/compile the
target**; a non-building diff is **rejected pre-surface** (build-preserving), so it never reaches the
ranker or the verification gate. Every surfaced change is a **reviewable diff** delivered as a
patch/PR; rollback is `git revert` and the audit trail is git history.

**Why.** ADR-001 replaces the runtime shim — infeasible for compiled languages (Go, the P1 target)
and measuring a code path that never ships — with source transformation, and makes editing user code
a first-class, testable risk surface. Transform correctness is now the top risk, so determinism,
build-preservation, behavior-preservation, isolation, reviewability, and clean rollback are
**requirements with scenarios**, not aspirations. Making the build gate a **pre-surface** filter
means a broken codemod is caught before a user (or the verification gate) ever sees it, mirroring the
P4 gates-not-penalties discipline.

**Alternative rejected.** String/regex substitution — non-deterministic across formatting, fragile,
and unable to guarantee behavior preservation. Mutating the user's tree in place — no isolation, no
clean rollback.

## Data model sketch

```
proposal(proposal_id PK, diagnosis_id FK->P4.5, operator, base_variant_id,
         candidate_config_hash, source_diff_blob_hash, build_status ENUM('unbuilt','built','build_failed'),
         status ENUM('candidate','build_failed','verifying','verified','gate_failed','constraint_excluded'),
         created_at)
proposal_evidence(proposal_id FK, case_id, role ENUM('generating','held_out'),
                  PRIMARY KEY(proposal_id, case_id))
verdict(proposal_id PK FK, metric, delta, ci_low, ci_high, significant BOOL, held_out BOOL,
        cost_delta, latency_delta, regression_pass BOOL,
        cases_fixed_json, cases_broken_json,
        gate_result ENUM('pass','fail_significance','fail_regression','fail_constraint'))
rank_entry(proposal_id FK, ranking_context, expected_gain, cost_of_change, score,
           constraint_status ENUM('ok','excluded'), violated_constraint,
           PRIMARY KEY(proposal_id, ranking_context))
-- verification runs are ordinary P4 eval_result rows tagged with candidate_config_hash,
-- eval_set_hash, split, seed; P5.5 does NOT re-store traces.
```
Candidate **source diffs** (the codemod output), rendered candidate prompts, and prompt-optimizer
grounding bundles (failing-case traces) live in the object store keyed by content hash; DB rows hold
only the hash. A `build_failed` proposal is retained for diagnostics but is never ranked or surfaced.

## Key interfaces

```
Operator(diagnosis, ir, registries) -> []CandidateVariantSpec   // catalog row; pattern-gated
Codemod(candidate, source) -> SourceDiff                         // deterministic AST transform (ADR-001)
BuildCheck(source_diff, worktree) -> {builds BOOL}               // pre-surface build/compile gate
PromptOptimize(node, failing_cases) -> PromptEdit                // DSPy/self-refine, grounded
ContractValidate(candidate) -> {ok, adapters[]}                  // reuses P5 typed-contract validator
Rank(candidates, constraints) -> []RankedProposal                // gain/cost-of-change; constraint filter
Verify(proposal, eval_set, split) -> Verdict                     // build transformed copy -> held-out auto-exec -> Stats.Compare -> regression
Stats.Compare(candidate, baseline, metric) -> {delta±ci, sig, verdict}   // REUSED from P4, unchanged
Apply(proposal, level) -> PullRequest                            // advisory: draft PR; assisted: verified PR (gate_result = pass); human merges
```

## Risks

- **A bad codemod breaks the build or silently changes behavior** (ADR-001's top new risk) —
  mitigated by the deterministic AST transform, the **pre-surface build gate** (a non-building diff is
  rejected before ranking/verification), behavior-preservation to the targeted call site, and
  isolated worktree application (Decision 10); tested with a candidate whose diff fails to compile.
- **A change mutates the user's tree or is unreviewable** — mitigated by applying every transform to
  an isolated worktree/branch and delivering it only as a reviewable diff/PR; rollback is `git revert`
  (Decision 10).
- **Unverified suggestion reaches the user** — mitigated by making "surface" a predicate over
  `gate_result = pass` (Decision 8); tested with a good-looking-but-gate-failing proposal.
- **Prompt rewrite overfits its generating cases** — mitigated by held-out auto-execution; surfaced
  delta is the held-out delta (Decision 5).
- **"Fixed accuracy, tripled cost" / broke another cluster** — mitigated by the regression check with
  a hard cost/latency budget + other-cluster re-scoring; verdict reports cases broken (Decision 7).
- **Noise read as a win** — mitigated by reusing the P4 significance primitive; CI-overlap = tie
  (Decision 6).
- **Nonsensical operator emitted** — mitigated by pattern + typed-contract gating (Decision 2).
- **Constraint-violating proposal surfaces** — mitigated by hard-constraint filtering; excluded, not
  ranked (Decision 4).
- **Verifying every candidate blows the budget** — mitigated by a per-batch spend cap, queue
  backpressure, cheapest-operator-first ordering, idempotent re-delivery (DevOps, NFR).
- **Assisted apply ships unverified** — mitigated by gating one-click apply on `gate_result = pass`
  (Decision 9).
- **No held-out split → false confidence** — mitigated by flagging the verdict **not held-out**;
  split-minting policy (Q1) still requires the significance gate.
