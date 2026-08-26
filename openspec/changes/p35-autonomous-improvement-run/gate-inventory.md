# P35 — Gate inventory (the reviewable checklist)

> **This file is a checklist, not prose** (`tasks.md` 1.6). design.md states *why* the inventory exists;
> this states *what to walk, in what order, and what proves each line*. It is the artifact task 11.3
> hands to somebody who did not implement the phase.
>
> 🔴 **A box is ticked only when its fence exists AND has been proved able to go red.** An unticked box
> is a claim nobody has earned yet, and that is the state this file is supposed to be able to show.

## Why a checklist and not a paragraph

Every safety property in this platform was written against callers that exist today. **A new caller is a
new way for each of them to be bypassed, and none of them fails loudly if it is simply not called.** The
optimizer's own tests prove *the optimizer* calls these gates. They say nothing about the conversation.

A paragraph describing that is read once. A checklist is walked, and each line either has a named test
behind it or is visibly empty. 🔴 `TestGateInventoryIsComplete` in `internal/improvementrun` parses the
table below and fails when a row names a test that does not exist, or when a gate declared in
`improvementrun.Gates()` has no row — so the checklist cannot rot into a description of an older
system.

---

## The six gates the conversational caller must not bypass

Every row is run **through the conversational path**, not only through the optimizer.

| ✔ | Gate | Where it lives | What makes it impossible to skip | Fence |
|---|---|---|---|---|
| [x] | **G1 · typed I/O contract** | `internal/typedcontract` | The candidate is rejected **before** verification is even requested, so a contract violation cannot be "verified" and then filtered. | `TestConversationalRun_ContractViolationRejectedBeforeVerification` |
| [x] | **G2 · verified delta, held-out** | P5.5 `internal/verification` | Delivery reads the verdict from the **oracle**, not from a flag on the proposal, and there is no branch from candidate to delivery that does not pass through it. | `TestConversationalRun_UnverifiedNotDelivered` · `TestConversationalRun_GateFailingHighScorerNotDelivered` |
| [x] | **G3 · entitlement: plan AND automation level** | P7 `internal/entitlement` | Evaluated **server-side** inside `forgedelivery.Prepare`. The conversation carries no field that reaches either argument. | `TestConversationalRun_EntitlementRefusedServerSide` · `TestConversationalRun_ConversationCannotRaiseEntitlement` |
| [x] | **G4 · transform refusal** | `internal/transform` | No plan, role, entitlement, flag or request parameter materialises a configuration the transform refuses — the refusal is a property of the configuration, not of the caller. | `TestConversationalRun_NoOverrideMaterialisesARefusedConfiguration` |
| [x] | **G5 · human approval** | `internal/approval` | The **only** approval path. P35 adds no second one; the run reads `approval.Approve`'s result and nothing else. Bound to `(config_hash, source_revision)`. | `TestConversationalRun_ApprovalIsPerProposalAndRoutedThroughTheShippedGate` · `TestConversationalRun_ApprovalVoidWhenRevisionMoves` |
| [x] | **G6 · never merge below Autonomous** | `internal/forgedelivery` | `AllowMerge` is computed in `Prepare` from the level **and** the auto-merge entitlement; it is the single place the rule is decided, and it is tested at **every** level. | `TestConversationalRun_NeverMergesBelowAutonomous` |

---

## The seven properties this phase does not restate but does re-fence

`specs/forge-delivery/spec.md` deliberately does **not** re-declare these as `ADDED` — they already hold.
What P35 adds is the obligation to prove they hold **for a new caller**.

| ✔ | Property | Fence |
|---|---|---|
| [x] | Delivery is idempotent per `(config_hash, source_revision, target)`; the second call returns the **first** delivery | `TestConversationalRun_DeliveryIsIdempotent` |
| [x] | A merge is **observed**, never inferred from a pull request closing | `TestConversationalRun_MergeIsObservedNotInferred` |
| [x] | The pull-request body carries axis, node, verified delta with interval, eval-set decisiveness, and how to revert | `TestConversationalRun_PullRequestBodyCarriesItsEvidence` |
| [x] | The delivery record is append-only and `transform` stays immutable | `TestThePullRequestURLIsRecordedAndNeverComposed` |
| [x] | An installation is per-repository, least-privilege and customer-revocable | `TestInstallationScopedToSelectedRepositories` |
| [x] | Revocation stops pushes **immediately**, not at the next token refresh | `TestRevocationStopsPushesImmediately` |
| [x] | The read connection and the write installation are separate grants with independent revocations | `TestReadConnectionAndWriteInstallationAreSeparateGrants` |

---

## The five P35-specific fences

| ✔ | Property | Fence |
|---|---|---|
| [x] | Re-measurement disagreement **withdraws** before delivery and reports **both** measurements | `TestConversationalRun_RemeasurementDisagreementWithdraws` |
| [x] | Cancellation pushed **nothing** — the forge received no `EnsureBranch` (D-35.6) | `TestConversationalRun_CancelPushesNothing` |
| [x] | Every closed `proposalgen.State` renders its **own** sentence | `TestEveryNothingToProposeStateHasItsOwnSentence` · `TestEveryNothingToProposeStateRendersThroughTheConversationalPath` |
| [x] | A run that hits a bound reports **which** bound stopped it | `TestARunThatHitsItsBudgetReportsWhichBoundStoppedIt` · `TestConversationalRun_ReportsWhichBoundStoppedIt` |
| [x] | Proposals are broken out **per axis** at every stage — generated, verified, approved, delivered | `TestPerAxisBreakdownExistsAtEveryStage` |

---

## The live four-step (§9.3, A13)

🔴 **A 200 is not evidence of a write.** Acceptance is not any of the above; it is this, run against a
real forge:

1. [x] Approve a proposal through the conversational path.
2. [x] `SELECT` the approval row and assert it names the approving person.
3. [x] `SELECT` the delivery record and assert it carries the pull-request URL.
4. [x] Fetch the pull request **from the forge** and assert it exists at the recorded URL.

Fence: `TestLiveFourStep_ApproveSelectSelectFetch` (`-tags live`), run by `make p35-live-four-step`.

⚠️ **Written and never run against a real forge in this change.** It needs `HEROS_LIVE_FORGE_TOKEN` and
a repository somebody is willing to have a pull request opened on, and it opens one. The four boxes
above are ticked for *the step exists and is asserted*; the run itself is a release-checklist item, not
something a build can do. 🔴 Nobody should read these ticks as "a pull request has been observed".

---

## How to walk this (task 11.3)

For each row, in order:

1. Read the **fence** named in the row. Confirm it drives the **conversational** entry point
   (`improvementrun.Service`), not `optimizer.Controller` directly. A fence that calls the optimizer
   proves the thing that was already proved.
2. Break the gate — comment out the check, or invert its condition — and confirm the fence goes **red**.
   `make p35-fence-redcheck` does this mechanically for every row and is the machine form of this step.
   It currently proves **20** mutations, and it has already earned its place: on its first run it found
   four fences that were not fencing, including one guard whose condition was **unreachable** and would
   have been wrong if it ever fired. Two of the four were passing because a SECOND guard caught the same
   case — defence in depth working, and a drill that could prove neither. The fix in both was to give
   each guard a case only it catches.
3. Confirm the row's ✔ is only set once both of the above hold.

🚫 A row whose fence passes but cannot be made to fail is **not a fence**. That is the whole content of
§9.7: *green is worth having only if green can be red*.
