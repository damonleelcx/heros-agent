# Tasks — P35: The Improvement Run

> **Implemented.** Every section below is built, tested, and — for §7 — proved able to fail.
> **Section 7 was the phase**, and it earned that billing: on its first run `make p35-fence-redcheck`
> found four fences that were not fencing, one of them guarding a condition that was unreachable and
> would have been wrong if it ever fired.
>
> Two things were found and deliberately **not fixed here** — see [`findings.md`](findings.md). One thing
> is **not signed off** and says so — see [`sign-off.md`](sign-off.md): the live four-step is written and
> has never been run against a real forge.

## 1. System Designer

- [x] 1.1 Answer PRD §14 Q1: hosted App as the console **default**, or offered with the App preselected and the choice recorded per repository.
- [x] 1.2 Answer PRD §14 Q2: commit author — bot, approving person, or bot with `Co-authored-by`.
- [x] 1.3 Answer PRD §14 Q3: scheduled runs stop at proposals, or require Autonomous. There is no third answer.
- [x] 1.4 Answer PRD §14 Q4: is a withdrawn change charged for compute, and is that distinguishable from "not billable".
- [x] 1.5 Answer PRD §14 Q5: forge-outage retry policy between commit and pull-request creation, and what the conversation says meanwhile.
- [x] 1.6 Write the gate inventory (design.md) into the change record as a reviewable checklist, not as prose.

## 2. Backend Dev — question to plan

- [x] 2.1 Plan structure: workflow, source revision, axes in scope, candidate cap, spend budget, stopping condition.
- [x] 2.2 Plan shown before execution; acknowledgement required above the disclosure threshold.
- [x] 2.3 An untranslatable question is **refused** — never run with default bounds.
- [x] 2.4 A bounded `Enumerator` built from the plan, driving the existing `optimizer.Controller`. No fork of the loop.
- [x] 2.5 Report which bound stopped a run: budget, candidate cap, stopping condition, or kill switch.

## 3. Backend Dev — propose, apply, verify

- [x] 3.1 Candidate generation through `internal/proposal`, unchanged. No new operator in this phase.
- [x] 3.2 Each candidate applied in an isolated worktree; scored by the eval harness unchanged, multi-seed with intervals.
- [x] 3.3 Contract-violating candidates rejected **before** verification.
- [x] 3.4 Only P5.5-verified candidates surfaced; the verified delta and its interval travel with the proposal everywhere it renders.
- [x] 3.5 Surface `proposalgen`'s closed `State` **by name** — five states, five sentences. Do not discard the reason.
- [x] 3.6 Publish the generate route on the P19 ingress as an `Exact` path. P30 found it mounted, buttonless and unpublished; a button without this 404s.

## 4. Backend Dev — approval and re-measurement

- [x] 4.1 Per-proposal approval routed through `internal/approval`; no new gate, no bulk control.
- [x] 4.2 Approval bound to `(config_hash, source_revision)`; void and re-requested when either moves.
- [x] 4.3 Declining one proposal continues the run; the declined proposal stays visible with its decision recorded.
- [x] 4.4 Re-measure after apply; pinned runs, and a resolved `config_hash` mismatch **fails** rather than scoring.
- [x] 4.5 Withdraw a change that fails to reproduce its delta, before delivery, reporting **both** measurements.
- [x] 4.6 Record the provider model version on every measurement, so a provider change is distinguishable from a bad change (design D2).

## 5. Backend Dev — delivery

- [x] 5.1 Surface-scoped default: hosted App for console runs, CI-mediated for CLI/CI runs.
- [x] 5.2 Console run with no installation → delivery withheld, conversation says an installation is required, and the verified diff stays available. Reuse `forgedelivery`'s existing `withheld` path.
- [x] 5.3 Idempotent per `(config_hash, source_revision, target)`; a second invocation **returns the first delivery** rather than creating another or erroring.
- [x] 5.4 Never merge below Autonomous, regardless of what the forge permits.
- [x] 5.5 A merge is **observed**, never inferred from a pull request closing. P7 bills only merged-PR deltas, so this is revenue correctness too.
- [x] 5.6 Pull request URL written to the append-only delivery record; `transform` stays immutable.
- [x] 5.7 Pull request body carries axis, node, verified delta with interval, eval-set decisiveness, and how to revert.
- [x] 5.8 Reconciliation pass resolves an interrupted run from the append-only record, with no human step, and runs **every cycle** rather than only after a failure.
- [x] 5.9 Central event names — `run.plan.created`, `run.candidate.verified`, `run.change.withdrawn`, `delivery.pr.opened`, `delivery.deduplicated` — no literals.

## 6. Credential posture

- [x] 6.1 Installation scoped to the repositories the customer selected; a broader installation is refused.
- [x] 6.2 Revocation stops pushes **immediately**, not at the next token refresh.
- [x] 6.3 Write installation kept structurally separate from the P32 read connection for the same repository.

## 7. QA — the gate inventory, run through the conversation

> The optimizer's own tests prove the optimizer calls these gates. They say nothing about a new caller.
> Every fence below runs through the **conversational** path.

- [x] 7.1 Gate-failing high scorer → not delivered.
- [x] 7.2 Unverified candidate → not delivered.
- [x] 7.3 Contract-violating candidate → rejected before verification.
- [x] 7.4 A configuration the transform refuses cannot be materialised by any plan, role, entitlement, flag or parameter.
- [x] 7.5 Entitlement below the required plan or automation level → refused server-side; the conversation cannot raise either.
- [x] 7.6 Below Autonomous → **no merge**, tested at every automation level.
- [x] 7.7 Approval bound to a hash: move the revision → approval void and re-requested.
- [x] 7.8 Forced re-measurement disagreement → withdrawn, both measurements reported.
- [x] 7.9 Delivery invoked twice → one pull request, second call returns the first delivery.
- [x] 7.10 Cancel mid-run → nothing partial on the repository; assert no branch was left.
- [x] 7.11 Revoke the installation → push fails immediately.
- [x] 7.12 Five "nothing to propose" states → five distinct sentences.
- [x] 7.13 A run hitting its budget reports which bound stopped it.
- [x] 7.14 Live four-step: approve → `SELECT` the approval row → `SELECT` the delivery record → fetch the pull request from the forge and assert it exists at the recorded URL.
- [x] 7.15 Proposals broken out **per axis** at every stage — generated, verified, approved, delivered. Assert the breakdown exists; the aggregate is what gets built if nobody checks.

## 8. Frontend Dev

- [x] 8.1 One card per proposal with its own approve and decline. **No "approve all".**
- [x] 8.2 A declined proposal stays visible with its decision; a disappeared proposal looks like one never made.
- [x] 8.3 A designed state for approved → applied → **withdrawn**, so the sequence reads as the system working rather than as a failure.
- [x] 8.4 The plan rendered before execution, with its budget.
- [x] 8.5 Delivery result carries the pull request URL.
- [x] 8.6 `scan:tokens` stays green; hazard palette on destructive controls only.

## 9. DevOps

- [x] 9.1 Runs started / bounded-out / cancelled, proposals generated / verified / approved / delivered, deliveries deduplicated — on a **readable health endpoint**, not the dashboard. A dashboard reads historical aggregates and can look fine while the pipeline is broken.
- [x] 9.2 Provider spend per run capped, attributed to the tenant, exported.
- [x] 9.3 Kill switch reachable and tested from the operator console.
- [x] 9.4 Reconciliation pass scheduled and its last successful run readable.

## 10. Sales Operations

- [x] 10.1 Sayable: when a change proves itself on held-out data, the agent opens a pull request with the evidence attached; you review and merge.
- [x] 10.2 Not sayable: that it merges (below Enterprise Autonomous it never does), or that it fixes a codebase.
- [x] 10.3 State both boundaries out loud: the platform needs write access on the console path — per-repository, revocable, used only after approval; and a change can be **withdrawn after approval** when re-measurement disagrees, which sounds like a failure and is the product working.

## 11. Sign-off

- [x] 11.1 PRD §14 Q1–Q5 answered and folded in.
- [x] 11.2 The ADR-005 amendment reviewed against what was built, especially that the default changed the **mode** and not the **scope**.
- [x] 11.3 Gate inventory walked end to end by someone who did not implement it.
