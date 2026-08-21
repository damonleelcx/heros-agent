# PRD — P35: The Improvement Run, End to End

| | |
|---|---|
| **Phase** | P35 |
| **Program** | [Graph Engineering Harness Agent (GEHA)](P31-P38-graph-engineering-agent-program.md) |
| **OpenSpec change** | [`p35-autonomous-improvement-run`](../../openspec/changes/p35-autonomous-improvement-run/) |
| **Lead roles** | Backend Dev + AI Engineer |
| **Support roles** | System Designer, DevOps, Product Designer, Frontend Dev, QA, Sales Operations |
| **Upstream** | [P31](P31-conversational-console.md) · [P32](P32-repo-intake.md) · [P33](P33-surface-assessment.md) · P5.5 (verification) · P6 (optimizer) · P12 (forge delivery) · P7 (entitlements) · [ADR-001](../adr/ADR-001-source-transformation-apply-model.md) · [ADR-005](../adr/ADR-005-forge-delivery-and-credential-posture.md) |
| **Unblocks** | the program's user-facing promise: one question, one pull request |
| **Status** | Proposed — awaiting sign-off on §14 |

---

## 1. Summary

This is the phase where the conversation does something. A person has asked a question, the agent has
reported findings ([P33](P33-surface-assessment.md)), and P35 carries the rest: propose, wait to be told
yes, apply, re-measure, and — when the change proved itself — commit, push and open the pull request.

Almost every component already exists. `internal/optimizer/loop.go` runs a closed loop of enumerate →
verify → gate → merge. `internal/proposal` produces candidates. `internal/verification` is the held-out
gate that decides. `internal/transform` produces the reviewable diff. `internal/forgedelivery` opens the
pull request, and **both** delivery modes are already coded — `cimediated.go` and `hostedapp.go`.

So P35 is mostly wiring, and it has exactly two pieces of genuinely new design.

**The first is that a question becomes a bounded plan.** The optimizer is a search; a conversation is a
request. "Make my memory strategy better" has to become an ordered set of candidate changes with a budget,
a stopping condition and a scope — and that translation must be visible to the person before it runs,
because a search nobody bounded is a search nobody can predict the cost of.

**The second is ruling R3: for console-driven runs, the hosted Git App becomes the default**, so the agent
itself opens the pull request. [ADR-005](../adr/ADR-005-forge-delivery-and-credential-posture.md) made the
customer's own CI the default precisely to keep a write-scoped forge credential out of the platform, and
its argument is not wrong — it is an argument about a **default on a surface**, and there are now two
surfaces. The CLI keeps CI-mediated delivery. The console gets the App. This PRD states that as an
amendment, not a discovery.

What does not change is the sentence the whole product rests on: **delivery is downstream of verification,
never a path around it.** The conversation adds no shortcut.

---

## 2. Problem & context

### 2.1 The loop exists and nothing can ask it a question

`optimizer.Controller` takes an `Enumerator`, a `Verifier`, a `Repo`, a `ChangeLedger`, a `KillSwitch` and
an `Authority`, and runs until convergence or `max_iter`. Its tests read like the platform's conscience:
`TestLoop_GateFailingHighScorerNotMerged`, `TestLoop_UnverifiedNotMerged`,
`TestLoop_ContractViolationNotMerged`.

None of that is reachable from a sentence. There is no translation from "improve this" to an `Enumerator`
scoped to a person's intent and a budget they agreed to.

### 2.2 The proposal path had three stacked failures, and P30 fixed the plumbing

P30 recorded that `POST /api/v1/workflows/{workflow_id}/proposals/generate` was mounted, **had no button
in the console**, was **not published on the ingress** so a button would have 404'd, and that the surface
**discarded the reason** when there was nothing to propose — `proposalgen` returns a closed `State` with
five distinct answers (`no_linked_runs`, `no_per_node_metrics`, `no_discovered_graph`, …) and the screen
showed none of them.

P35 must not re-create any of those three. In particular the fifth-state discipline is inherited: **"there
is nothing to propose" is five different sentences**, and the conversation says which one.

### 2.3 Delivery: what ADR-005 decided, and what R3 changes

ADR-005 chose CI-mediated delivery as the default because pushing to a customer's repository *"is the
highest-blast-radius action in the system"*. It also built Mode 2 — the hosted Git App — as the opt-in
upgrade, per-repository, least-privilege and customer-revocable.

R3 makes Mode 2 the **console default**. The reasoning is that ADR-005's argument answers "what should
happen when we do not know how the customer works", and on the console we do: they came to us with no CI
integration and no CLI, and telling them to go configure one is telling them the product does not work.

Three of ADR-005's constraints are untouched by this and must stay stated, because they are what make it
safe: delivery is **idempotent** per `(config_hash, source_revision, target)`; it **never merges below
Autonomous**; and a merge is **observed, never inferred** from a pull request closing.

### 2.4 The step the request lists that does not exist

The original request lists *"if everything is good and there are improvements"* as a step before
committing. In this codebase that is the P5.5 verified-delta gate, which already exists and already
decides. P35 adds no second check — a second gate is a second place for the first one to be bypassed.

---

## 3. Goals & non-goals

### Goals

1. **G1** — A question becomes a **bounded plan**: scope, candidate axes, budget, stopping condition —
   shown to the person before anything runs.
2. **G2** — Candidates are generated, applied in an isolated worktree, and scored, reusing P5.5 and P6
   unchanged.
3. **G3** — Only a **verified** change is offered. Diagnosis proposes; verification decides.
4. **G4** — Approval is explicit, in the conversation, per change, and routed through the existing gate.
5. **G5** — On approval the platform commits, pushes and opens the pull request via the hosted Git App on
   console-driven runs; the CLI path keeps CI-mediated delivery.
6. **G6** — Re-measurement after apply is reported with intervals, and a change that fails to reproduce
   its delta is **withdrawn**, not shipped.
7. **G7** — "Nothing to propose" names which of the five states it was.
8. **G8** — Every run is bounded, cancellable, and attributable: budget, kill switch, ledger entry.

### Non-goals (with the phase that owns them)

- **Merging.** Auto-merge is P6's Autonomous level and Enterprise-only. P35 opens pull requests.
- **Assessing** — [P33](P33-surface-assessment.md). **Rendering** — [P31](P31-conversational-console.md).
- **New axes** — [P34](P34-harness-loop-graph-split.md).
- **A second verification gate** — §2.4.
- **Multi-repository or cross-workflow runs.** One workflow, one revision.

---

## 4. Users & personas

| Persona | What they need before saying yes |
|---|---|
| Application engineer | the diff, the verified delta with its interval, and what happens if it is wrong |
| Staff engineer | which axis, which node, and why this change rather than the others considered |
| Engineering manager | what it will cost to run, before it runs |
| Security / platform owner | that the platform's write credential is per-repository, revocable, and used only after a person said yes |

---

## 5. User stories

- **US1** As an engineer I ask "fix what you can prove" and see a plan with a budget **before** anything
  spends, so that I am not surprised by a bill or a wait.
- **US2** As an engineer I see each proposal with its diff and its verified delta, and I approve them
  individually, so that I am not asked to accept a bundle.
- **US3** As an engineer I decline one proposal and the run continues with the others, so that one no is
  not a cancel.
- **US4** As an engineer I watch the re-measurement after apply and see the change withdrawn when it fails
  to reproduce, so that "verified" means verified twice.
- **US5** As an engineer I receive a pull request URL in the conversation, so that the run ends where my
  review starts.
- **US6** As an engineer whose repository yields nothing I am told **which** of the five reasons applies,
  so that I know whether to link runs, push source, or wait.
- **US7** As a platform owner I revoke the App installation and the platform can no longer push, so that
  the write capability is mine to withdraw.
- **US8** As an engineer I cancel mid-run and nothing partial is left on my repository, so that cancelling
  is safe.

---

## 6. Functional requirements

### 6.1 Question → plan (capability `improvement-run`)

**FR1** — A question is translated into a plan carrying: workflow and source revision, the axes in scope,
the maximum number of candidates, the spend budget, and the stopping condition. **FR2** — The plan is
shown before execution and requires acknowledgement when its projected spend exceeds the disclosure
threshold. **FR3** — A question that cannot be translated into a bounded plan is **refused**, not run with
defaults — an unbounded search is not a smaller version of a bounded one.

### 6.2 Generate, apply, score

**FR4** — Candidate generation uses `internal/proposal` unchanged; P35 adds no operator. **FR5** — Each
candidate is applied in an **isolated worktree** and scored by the eval harness unchanged, multi-seed with
intervals. **FR6** — A candidate violating a typed I/O contract is rejected **before** verification and
never surfaced. **FR7** — Where nothing can be proposed, the closed `State` is reported by name — one of
the five — and never as an empty result.

### 6.3 Verification decides

**FR8** — A candidate is surfaced only when the P5.5 gate passes on **held-out** data. **FR9** — A
candidate that scores well but fails a gate is **not** surfaced, however high its composite. **FR10** — The
verified delta travels with the proposal wherever it is rendered, with its confidence interval.

### 6.4 Approval

**FR11** — Approval is per proposal, explicit, and routed through `internal/approval`. **FR12** — Declining
one proposal does not cancel the run. **FR13** — An approval is bound to a specific
`(config_hash, source_revision)`; if either moves, the approval is void and re-requested. **FR14** — No
plan, role, entitlement, flag or request parameter can materialize a configuration the transform refuses.

### 6.5 Re-measurement

**FR15** — After apply, the change is re-measured and reported with intervals. **FR16** — A change that
fails to reproduce its verified delta on re-measurement is **withdrawn** before delivery, and the
withdrawal is reported with both measurements. **FR17** — Re-measurement runs are pinned; a run whose
resolved `config_hash` does not match what was requested **fails** rather than being scored.

### 6.6 Delivery

**FR18** — On console-driven runs delivery uses the hosted Git App by default; CLI-originated runs keep
CI-mediated delivery. **FR19** — Delivery is **downstream of verification** and there is no path around it.
**FR20** — Delivery is **idempotent** per `(config_hash, source_revision, target)`. **FR21** — Delivery
**never merges** below the Autonomous automation level. **FR22** — A merge is **observed**, never inferred
from a pull request closing. **FR23** — The pull request body carries the axis, the node, the verified
delta with its interval, the eval set's decisiveness, and how to revert. **FR24** — Delivery is recorded
append-only; `transform` stays immutable. **FR25** — Revoking the App installation removes the platform's
ability to push, immediately.

### 6.7 Bounds

**FR26** — A run stops at its budget, its candidate cap, or its stopping condition, whichever comes first,
and reports which. **FR27** — The kill switch halts a run at the next safe point. **FR28** — Cancellation
leaves nothing partial on the customer's repository: either a pull request exists or nothing was pushed.
**FR29** — Every run writes a ledger entry: plan, candidates, verdicts, approvals, deliveries.

---

## 7. Non-functional requirements

**7.1 Credential posture.** The App installation is per-repository, least-privilege, customer-revocable,
and **separate from** the P32 read grant. One credential that both reads source and opens pull requests is
the thing neither ADR wants.

**7.2 Statistical honesty.** Every delta carries its interval and the size of the set behind it. A tie is a
tie. Re-measurement is a second observation, not a confirmation ritual — it is allowed to disagree, and
FR16 is what happens when it does.

**7.3 Cost.** Provider spend is capped per run, attributed to the tenant, and reported. Budget exhaustion
is a **reported stopping condition**, not an error.

**7.4 Idempotency and self-healing.** A retried delivery does not open a second pull request. A run
interrupted between apply and delivery resumes or rolls back cleanly; a partially-delivered state is
reconcilable from the append-only record without human intervention.

**7.5 Entitlements.** Plan **and** automation level both gate. Autonomous auto-merge stays Enterprise-only.
The conversation cannot raise either.

---

## 8. System design summary

### 8.1 Shape

```
question
   │
   ▼
 plan  ──(shown; acknowledged above threshold)──▶  bounded Enumerator
   │                                                    │
   │                                    ┌───────────────▼──────────────┐
   │                                    │  proposal → worktree apply   │
   │                                    │  → eval (multi-seed, CIs)    │
   │                                    │  → typed-contract check      │
   │                                    │  → P5.5 held-out gate        │
   │                                    └───────────────┬──────────────┘
   │                                          verified candidates
   ▼                                                    │
conversation ◀── proposal + diff + delta ───────────────┘
   │
   ├─ decline ──▶ run continues
   └─ approve ──▶ internal/approval ──▶ apply ──▶ RE-MEASURE
                                                    │
                                     reproduces? ───┴─── no ──▶ WITHDRAW (report both)
                                          │ yes
                                          ▼
                                   forgedelivery
                                 (hosted App on console runs)
                                          │
                                    commit → push → PR ──▶ result message
```

### 8.2 Decisions

**D1 — A question becomes a plan, and an untranslatable question is refused.** Running with defaults is how
a conversational surface spends someone's money on a search they did not ask for. The plan is the artifact
that makes the run's cost and scope reviewable **before** it exists.

**D2 — Re-measurement can disagree, and disagreement withdraws the change.** The gate ran on held-out data
before apply; re-measurement observes the applied result. Treating the second as a formality would make it
theatre. FR16 gives it teeth, and reports both numbers so the disagreement is visible rather than resolved
silently.

**D3 — Hosted App by default on the console only (R3).** ADR-005's default is amended for one surface, and
the three constraints that make delivery safe — idempotency, never-merge-below-Autonomous, merge-observed —
are restated here rather than assumed, because a surface that changes a default is exactly where the rest
of a policy gets forgotten.

**D4 — Approval is per proposal and bound to a hash.** A bundle approval is one click that means several
things. And an approval that survives a revision change is an approval for a diff nobody saw (FR13).

**D5 — The five "nothing to propose" states are five sentences.** P30's defect, inherited as a requirement.

**D6 — Cancellation is atomic with respect to the customer's repository.** Either a pull request exists or
nothing was pushed. A half-pushed branch is a mess the customer has to clean up, from a run they cancelled.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *reduce the input, never the truth*

The input reduction is enormous: a sentence replaces eleven steps. The truth that must not reduce is **the
plan** (FR1/FR2). A person must be able to see what will be spent and touched before it happens, and the
plan is the only place that is possible — afterwards it is a receipt.

Per-proposal approval (D4) resists the pressure to offer "approve all", which is the single most requested
and most dangerous convenience in this phase.

### 9.2 Senior System Designer — *arbitrate by level; do not open a one-way door*

The door is **the platform holding a write-scoped forge credential by default on a surface**. ADR-005 spent
its whole argument keeping that out, and R3 admits it for the console. What makes it survivable is that
every property ADR-005 required of Mode 2 still holds: per-repository, least-privilege, customer-revocable,
opt-in **per repository** even when it is the surface default. What must not happen is the default
silently widening into "the platform has write access to your account", which is why FR25 (revocation is
immediate and complete) is a requirement rather than an expectation.

Level ordering: this trades level 1 (a write credential exists) against level 3 (the product otherwise does
not work for a console customer). That is genuinely close, and it is resolved by narrowing the grant rather
than by accepting a broad one — the same move ADR-013 makes for reads.

### 9.3 Senior Backend Dev — *a 200 is not evidence of a write*

A delivery that returns 200 has not necessarily produced a pull request, and a pull request existing is not
a delivery record. Acceptance is the live four-step: approve → `SELECT` the approval → `SELECT` the
delivery record → fetch the pull request from the forge and assert it exists at the recorded URL.

Idempotency (FR20) is the requirement most likely to be satisfied incompletely: the second call must return
the **first** delivery, not create a second one and not error. And `transform` is immutable by DB trigger,
so the pull request URL lives on the delivery record — the same structural constraint ADR-005 hit and
solved.

### 9.4 Senior Frontend Dev — *do not bundle what must be decided separately*

Each proposal is its own card with its own approve and decline. There is no "approve all". A declined
proposal stays visible with its decline recorded — a disappeared proposal looks like one that was never
made.

The withdrawal case (FR16) needs a designed state: a change that was approved, applied, and then withdrawn
is a sequence the UI must be able to tell, or it reads as a failure rather than as the system working.

### 9.5 Senior AI Engineer — *an aggregate hides the single-sample defect*

Two obligations.

**Report per-axis, never a mean.** Proposals generated, verified, approved, delivered — broken out by axis.
An operator with a 5% verification pass rate hidden inside a healthy average is an operator that is not
working, and the aggregate is what will be built if nobody asks.

**Re-measurement is a control-variable problem.** A delta that fails to reproduce may mean the change is
bad, the eval set is noisy, the provider moved, or the source moved. FR17's pinning holds the last;
recording the provider model version holds the third; multi-seed intervals hold the second. Without all
three, FR16 withdraws changes for the wrong reason and nobody can tell.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable, least privilege*

Blast radius is a pull request, and its reversal is `git revert` — ADR-001's founding property. That is
what makes the whole phase acceptable, and it holds only while FR21 (never merge below Autonomous) does.

Observable: runs started / bounded-out / cancelled, proposals generated / verified / approved / delivered,
deliveries idempotently-deduplicated, on a readable health endpoint. Not the dashboard — the dashboard
reads historical aggregates and can look fine while the pipeline is broken.

Self-healing (§7.4): a run interrupted between apply and delivery must reconcile from the append-only
record on the next pass, with no human step. That reconciliation is a **necessary path**, not a repair
script — it runs whether or not anything broke.

### 9.7 Senior QA Engineer — *green is worth having only if green can be red*

1. A gate-failing high scorer is **not** delivered. The optimizer already tests this; P35 must test it
   **through the conversation**, because a new caller is a new way to bypass it.
2. An unverified candidate is not delivered.
3. A contract-violating candidate is rejected before verification.
4. Re-measurement disagrees → the change is withdrawn and **both** measurements are reported.
5. Delivery called twice with the same `(config_hash, source_revision, target)` → one pull request, and the
   second call returns the first delivery.
6. Automation level below Autonomous → no merge, ever, even when the forge would allow it.
7. Approval bound to a hash; move the revision → the approval is void and re-requested.
8. Cancel mid-run → nothing partial on the repository.
9. Revoke the App → push fails immediately, not at the next token refresh.
10. Each of the five "nothing to propose" states renders its own sentence.
11. A run that hits its budget reports **which** bound stopped it.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

Sayable: *"Ask in English; when a change proves itself on held-out data, the agent opens a pull request
with the evidence attached. You review and merge."*

Not sayable: that it merges (below Enterprise Autonomous it never does), or that it "fixes your codebase"
(it proposes a diff a human merges).

Two boundaries to state out loud, both of which are strengths when said first. **The platform needs write
access to open the pull request on the console path** — per-repository, revocable, and used only after
someone approved. And **a change can be withdrawn after approval** if re-measurement disagrees, which
sounds like a failure and is the product working.

---

## 10. Dependencies

| Needs | From | Hard? |
|---|---|---|
| findings to propose against | [P33](P33-surface-assessment.md) | hard |
| a conversation to approve in | [P31](P31-conversational-console.md) | hard |
| source | [P32](P32-repo-intake.md) | hard |
| proposal operators, verification gate | P5.5 | hard |
| the loop, kill switch, ledger | P6 | hard |
| both delivery modes | P12 / `forgedelivery` | hard — exist |
| App installation, write-scoped | ADR-005 Mode 2 | hard — exists |
| entitlements by plan **and** automation level | P7 | hard |
| worktree isolation | `internal/worktree` | hard |

---

## 11. Risks & mitigations

| Risk | Severity | Mitigation |
|---|---|---|
| A path to delivery that skips verification | **critical** | FR19; QA fences 1–3 run **through the conversation**, not only through the optimizer. |
| "Approve all" is added by request | **high** | D4/§9.4; per-proposal approval is a requirement, and the pressure for this is predictable. |
| Idempotency incomplete → duplicate pull requests | **high** | FR20, QA fence 5 asserts the second call returns the first delivery. |
| Write credential widens beyond per-repository | **high** | §9.2; FR25 makes revocation immediate and complete. |
| Re-measurement withdraws changes for the wrong reason | med | §9.5 — pinning, provider version recorded, multi-seed intervals. All three, or none work. |
| A cancelled run leaves a branch behind | med | FR28/D6, QA fence 8. |
| Merge inferred from a closed pull request → wrong billing | med | FR22; P7 bills only merged-PR deltas, so this is a revenue-correctness issue too. |
| Per-axis operator failure hidden in an aggregate | med | §9.5 — break out by axis. |

---

## 12. Rollout & test strategy

1. **Plan only.** A question produces a plan and stops. No generation, no spend. Validates that the
   translation and the budget disclosure are right before anything costs money.
2. **Propose, no apply.** Candidates generated, verified, surfaced in the conversation. Approval controls
   render but are disabled.
3. **Apply + re-measure, delivery withheld.** The full loop, ending at a diff. `forgedelivery`'s existing
   `withheld` path is exactly this state.
4. **Delivery on**, internal repositories, hosted App, entitlement-gated.
5. **Customer tenants**, opt-in per repository.

Rollback at every stage is disabling the newest step; every earlier stage remains useful on its own.

---

## 13. Success metrics & acceptance criteria

| # | Criterion | How it is checked |
|---|---|---|
| A1 | A question yields a plan with scope, budget and stopping condition, before spend | conversation acceptance |
| A2 | An untranslatable question is refused, not run with defaults | refusal test |
| A3 | Gate-failing high scorer not delivered, **through the conversation** | fence |
| A4 | Unverified candidate not delivered | fence |
| A5 | Contract-violating candidate rejected before verification | fence |
| A6 | Re-measurement disagreement withdraws and reports both numbers | forced-disagreement test |
| A7 | Delivery idempotent; second call returns the first delivery | double-call test |
| A8 | No merge below Autonomous | entitlement test at every level |
| A9 | Approval void when the revision moves | hash-binding test |
| A10 | Cancel leaves nothing partial on the repository | interrupted-run test |
| A11 | App revocation stops pushes immediately | live revocation test |
| A12 | Five "nothing to propose" states render five sentences | one case each |
| A13 | Live four-step: approve → approval row → delivery record → pull request exists at the recorded URL | live event |
| A14 | Proposals broken out per axis at every stage | assert the breakdown, not the aggregate |

---

## 14. Open questions

| # | Question | Why it is open |
|---|---|---|
| **Q1** | Does the console default to the hosted App, or offer both with the App preselected? | R3 says default. A preselected choice is nearly as good for UX and keeps the customer's decision explicit, which matters for a write credential. **Recommendation: offer both, App preselected, and record the choice per repository.** |
| **Q2** | Who is the commit author — the platform's bot, or the approving person? | The bot is honest about what happened; the person is what most review tooling and CODEOWNERS expect. **Recommendation: bot as author, approving person as `Co-authored-by`, so both facts are on the record.** |
| **Q3** | May a run be scheduled (unattended), and if so how does per-proposal approval work? | Unattended runs are the input to any trend surface, and they compose with P32 §14 Q5's unattended clone. Per-proposal approval is incompatible with unattended delivery below Autonomous — so either scheduled runs stop at proposals, or they need Autonomous. |
| **Q4** | Does a withdrawn change (FR16) count against the tenant's budget, and is it billable? | It consumed provider spend and produced no delivery. P7 bills only merged-PR deltas, so it is not billable — but "not billable" and "not charged for compute" are different claims and only one is currently true. |
| **Q5** | What is the retry policy when the forge is down between commit and pull-request creation? | §7.4 requires reconciliation from the append-only record; the open part is how long it retries before the run reports partial delivery, and what the conversation says in the meantime. |
