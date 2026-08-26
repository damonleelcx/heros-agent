# P35 — Recorded decisions (System Designer)

The narrative reasoning lives in [`design.md`](design.md); the delivery-posture argument this phase
amends lives in
[ADR-005](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md). **This file is the
contract of record**: the six things that had to be settled before any P35 code shipped, each walked
through the five-step record (problem → decision → why appropriate → alternatives and the level each was
rejected on → effect) and tagged with the governing cost-and-complexity level.

`tasks.md` §1 owns these. The first five answer PRD §14 Q1–Q5, which
[the PRD](../../../docs/prd/P35-autonomous-improvement-run.md) left open rather than guessing. The
sixth is a **contradiction between two shipped rules** that this phase surfaced and had to arbitrate
before either could be coded against.

| # | Question | Answer |
|---|---|---|
| **D-35.1** (§14 Q1) | Console default: hosted App outright, or offered with the App preselected? | **Offered, App preselected, choice recorded per repository.** The mode is the default; the grant stays an act. |
| **D-35.2** (§14 Q2) | Who is the commit author? | **The platform bot, with the approving person as `Co-authored-by`.** Both facts on the record. |
| **D-35.3** (§14 Q3) | Scheduled (unattended) runs? | **They stop at proposals.** They do not deliver, at any automation level. |
| **D-35.4** (§14 Q4) | Is a withdrawn change charged for compute? Is it billable? | **Charged for compute, not billable** — two ledgers, two answers, reported separately. |
| **D-35.5** (§14 Q5) | Forge outage between commit and pull-request creation? | **Bounded in-run retry, then hand to reconciliation.** The run never blocks on a forge. |
| **D-35.6** | FR28 says cancellation leaves no branch; P12 says the platform never deletes a branch. | **Never push before the last safe point.** No deletion capability is added. |

---

## D-35.1 — The console OFFERS both modes with the hosted App preselected, and records the choice per repository (PRD §14 Q1)

**Problem.** Program ruling **R3** amends [ADR-005](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md)'s
CI-mediated default for the console surface, because a console customer arrived with no CI integration
and no CLI, and defaulting to a mode they cannot use is a default that means *this feature is off*. What
R3 does not settle is whether "default" means the platform **chooses** the hosted App, or **offers** it
already selected. The two are one click apart in the UI and very far apart in what the platform can
later claim about consent.

**Decision.** The console renders both modes with the hosted App **preselected**, and records the choice
**per repository** on the delivery route. The App is the default *mode*; installing it stays an act the
customer performs, and the record says which repository they performed it for.

**Why appropriate.** The failure R3 exposes the phase to is named in design D3: *the default quietly
widening into "the platform has write access to your account"*. A preselected choice and a hard default
produce the same first-run experience — the product works — and differ entirely in what exists
afterwards. A hard default leaves a write-scoped credential whose only provenance is a policy decision
taken in a design document; a preselected choice leaves a per-repository row naming the customer, the
repository and the moment. Only the second is answerable to *"why does the platform hold write access
to this repository?"*, and that is a question a security reviewer asks on day one of a procurement.

The cost of the preselection over the hard default is one rendered control and one persisted column.
The cost of the hard default is that the phase's own stated slope has no brake on it.

**Alternatives + decision point.**
- *Hard default to the hosted App, no choice rendered.* Rejected on **L1 security**: the platform's
  write credential would exist because of a product decision rather than a customer's, and there would
  be no artifact recording otherwise. It also makes design D3's "the default is the mode, not the
  scope" a claim with nothing enforcing it.
- *Offer both, neither preselected — make the customer choose before the first run.* Rejected on
  **L3 UX**: it puts a credential-posture decision in front of a person before they have seen a single
  proposal, which is the moment they are least equipped to make it and most likely to abandon. R3's
  whole argument is that the console customer must be able to get to a pull request.
- *Record the choice per TENANT rather than per repository.* Rejected on **L1 security**: a tenant-level
  choice is precisely the widening D3 forbids — it makes the second repository inherit a decision made
  about the first, which is how "per-repository, least-privilege" stops being true without anybody
  changing it.

**Effect.** `forgedelivery.Route.Mode` is already per (repository, workflow); the console writes it and
the improvement run reads it. `ModeApp` on a console run with no installation is **withheld** with
`WithheldNoInstallation`, not silently downgraded to CI-mediated — the customer is told an installation
is required and the verified diff stays available (spec `forge-delivery`, scenario *A console-driven run
with no installation*).

---

## D-35.2 — The commit author is the platform bot; the approving person is `Co-authored-by` (PRD §14 Q2)

**Problem.** A pull request opened by the platform carries a commit, and a commit carries an author. The
bot is honest about what happened. The person is what CODEOWNERS, review tooling, and most `git log`
readers expect. Choosing one loses the other fact.

**Decision.** **Author: the platform bot. `Co-authored-by`: the person who approved**, taken from the
approval row. Both are written; neither substitutes for the other.

**Why appropriate.** These are two different claims and the commit has room for both. *Who wrote this
change* is the bot — a person who did not type it must not appear as its author, because a `git blame`
that names a human for a machine-generated line sends the next reader to ask that human a question they
cannot answer. *Who authorized it* is the person, and that fact has to be on the commit rather than only
in our database, because the commit is the artifact that survives into the customer's repository and
outlives any record we keep.

**🔴 The identity comes from the approval row, never from the request.** `approval.Approve` already
refuses an empty actor for exactly this reason — an audit row that says a proposal was approved and
cannot say by whom is worse than no row, because it is believed. The `Co-authored-by` line is read back
through `approval.ApprovedBy`, so the trailer and the audit trail cannot disagree.

**Alternatives + decision point.**
- *Author: the approving person.* Rejected on **L1 security / attribution**: it attributes machine
  output to a human in the one place attribution is load-bearing, and it would let a repository's
  history show a person authoring a change they only read.
- *Author: the bot, nothing else.* Rejected on **L3 UX**: CODEOWNERS routing and review-assignment
  tooling read authorship, and a repository whose optimization pull requests all trace to one bot with
  no human on them is one nobody feels ownership of.

**Effect.** The pull-request body's evidence block and the commit trailer name the same person, both
sourced from `approval`. A delivery whose approval row carries no approver cannot be authored, which is
already impossible upstream and is now also impossible here.

---

## D-35.3 — A scheduled run stops at proposals (PRD §14 Q3)

**Problem.** Unattended runs are the input to any trend surface and compose with P32's unattended clone.
Per-proposal approval (D4) is incompatible with unattended **delivery** below Autonomous. design.md
states there are two answers and no third: either scheduled runs stop at proposals, or they require
Autonomous.

**Decision.** **They stop at proposals.** A run whose origin is `scheduled` generates, verifies and
records candidates, and **cannot deliver at any automation level** — including Autonomous. Delivery
requires a per-proposal approval, and an approval requires a person in the loop.

**Why appropriate.** The alternative couples two decisions that are independent in the customer's mind
and must stay independent in the product: *may this run without me watching* and *may this merge without
me watching*. Making Autonomous the price of scheduling means a customer who wants a nightly report of
what could be improved has to buy the level that also merges — so the only way to get the safer thing is
to buy the more dangerous one. That is a boundary that pushes in the wrong direction, and it is the kind
that gets quietly widened once a customer complains about the price of it.

Stopping at proposals also keeps the ledger honest. A scheduled run produces the same artifacts an
interactive one does — a plan, candidates, verdicts — and simply has no approval and no delivery beside
them. Nothing about the record has to be read differently.

🚫 **P35 ships no scheduler.** This decision binds the one that arrives, so that the phase which adds it
does not get to re-open the question under delivery pressure.

**Alternatives + decision point.**
- *Scheduled runs require Autonomous and deliver unattended.* Rejected on **L1 security**: it produces
  unattended delivery authorized by an entitlement rather than by a person, and design D4's whole
  argument is that consent is per proposal and hash-bound. An entitlement is not consent.
- *Scheduled runs deliver, with approval batched into a digest the customer approves later.* Rejected on
  **L1 security** and **L3 UX**: it is "approve all" wearing a schedule, which D4 names as the most
  predictable and most dangerous request of this phase.

**Effect.** `RunOrigin` is a closed set — `console`, `cli`, `ci`, `scheduled` — recorded on the plan and
carried into the ledger. Delivery refuses `OriginScheduled` **server-side**, before entitlement is even
consulted, so no plan, role, flag or parameter can reach a forge from a scheduled run.

---

## D-35.4 — A withdrawn change is charged for compute and is not billable (PRD §14 Q4)

**Problem.** FR16 withdraws a change that fails to reproduce its verified delta. It consumed provider
spend and produced no delivery. The PRD notes that *"not billable"* and *"not charged for compute"* are
different claims and only one is currently true — but does not say which.

**Decision.** Two ledgers, two answers, both reported:

| Ledger | Answer | Why |
|---|---|---|
| The run's **spend budget** and the tenant's spend attribution | **Charged.** | The tokens were spent. A budget that did not count them would be a budget that a run of withdrawals could exceed without ever reporting a bound. |
| The **invoice** (P7 gainshare) | **Not billable.** | P7 bills merged-PR deltas. Nothing merged; nothing is billed. This needs no new rule — it is what P7 already does. |

**Why appropriate.** The temptation is to make the two agree, in either direction, because one number is
easier to explain. Both directions are wrong. Not charging the budget would mean a run could withdraw
candidates indefinitely while reporting spend that does not match the provider invoice we receive —
which is the shape where a cost overrun is discovered by finance rather than by the bound that exists to
catch it. Billing the customer would mean charging for a change we ourselves decided not to ship.

**🔴 The withdrawal's spend is reported as its own figure**, not folded into the run total alone. A run
that spent 40% of its budget on candidates it withdrew is telling us something — most likely about the
eval set's noise, per design D2 — and an operator cannot see that if the number is only ever an
aggregate. This is the per-axis discipline (§9.5) applied to money.

**Alternatives + decision point.**
- *Do not charge the budget; treat a withdrawal as if the work had not happened.* Rejected on
  **L2 stability**: the run's spend bound would stop bounding actual spend, and the discrepancy would
  grow exactly in proportion to how noisy the eval set is.
- *Bill it, on the grounds that the analysis was performed.* Rejected on **L3 UX** and on P7's own
  design: gainshare is billed on merged deltas, and a line item for a change the platform withdrew is
  the single most corrosive invoice line this product could print.

**Effect.** `RunLedger` records `spend_usd` and `withdrawn_spend_usd` separately; the health document
publishes both; the invoice reads neither, because it reads merged deliveries.

---

## D-35.5 — A forge outage retries in-run to a stated bound, then hands the delivery to reconciliation (PRD §14 Q5)

**Problem.** §7.4 requires reconciliation from the append-only record. What is open is **how long the
run itself retries** before reporting partial delivery, and **what the conversation says** meanwhile.

**Decision.** Three attempts inside the run, with the whole retry window bounded so the run does not sit
on a forge. On exhaustion the run does **not** fail: it records the delivery as `pending_forge` in the
append-only record and reports it to the conversation as a **stated condition with no action for the
person**, naming the delivery id. The **reconciliation pass** — which runs every cycle regardless
(design D6) — completes it.

The conversation says, in the product's own words: *the change is committed and the pull request has not
been opened yet; the forge did not answer. Nothing is required from you — this completes on its own, and
the result will name the pull request.* 🚫 It does not say *"failed"*, and it does not offer a retry
button: a person clicking retry against an idempotent delivery cannot make it faster and can only make
the record noisier.

**Why appropriate.** The bound exists because the alternative is a conversational turn holding a
connection open against a third party's outage, which converts *their* incident into *our* hung UI. The
hand-off is safe precisely because delivery is idempotent per `(config_hash, source_revision, target)`:
reconciliation recomputes the same delivery id, finds the record, and either finds the pull request or
opens it. There is no state it can double.

Making reconciliation the completion path rather than a special-case retry is design D6's rule applied
here: *a repair path that only runs after failures is a path that is never exercised until it is
needed.* This outage is the ordinary case for that pass, not its exception.

**Alternatives + decision point.**
- *Retry until it succeeds, inside the run.* Rejected on **L2 stability**: a run pinned to a forge's
  availability is a run whose bound is not ours, and the person watching has no way to tell it from a
  hang.
- *Report the delivery as failed and require the person to re-run.* Rejected on **L3 UX**: it hands
  someone work created by a third party's outage, and a re-run costs a fresh verification.
- *Open the pull request first and push the branch after.* Not possible on any forge that requires the
  head ref to exist. Recorded so it is not re-proposed.

**Effect.** `deliveryroute`/`forgedelivery` gain no new state machine — `pending_forge` is a reason on an
existing record, and the reconciliation pass reads the same head it already reads.

---

## D-35.6 — Cancellation is satisfied by NEVER PUSHING, not by deleting (the contradiction)

**Problem.** Two shipped rules point in opposite directions and both are load-bearing:

- **FR28 / design D6 / fence 7.10** — *"Cancellation leaves nothing partial on the customer's
  repository: either a pull request exists or nothing was pushed"*, and the fence asserts **no branch
  was left**.
- **P12 `StaleBranchPolicy`** — *"the platform NEVER deletes a branch … deletion could remove something
  a customer built on — a one-way door the spec forbids"*, enforced by **absence**: `ForgeWriter` has no
  delete method at all.

Read naively, satisfying 7.10 requires a branch deletion, which P12 makes structurally impossible on
purpose. 🔴 This is a genuine contradiction, not a gap, and blending the two — *"delete only branches we
are sure nobody touched"* — would be the worst available answer: it adds a destructive capability to
the one interface whose safety comes from not having one, and gates it on a judgement the platform
cannot make.

**Decision.** **Neither rule moves.** The push is made the **last** step, and the cancellation check
sits immediately before it. A cancelled run therefore never pushed, so there is nothing to delete, and
7.10's assertion is satisfied in its literal form — *no branch was pushed* — rather than by a deletion.
🚫 No delete method is added to `ForgeWriter`. `StaleBranchPolicy.MayDelete` continues to return false.

**Why appropriate.** FR28's sentence is already written in the form that makes this work: *"either a
pull request exists **or nothing was pushed**"*. It does not require deletion; it requires that the
window in which a branch exists without a pull request be closed against cancellation. That window is
exactly `EnsureBranch` → `OpenOrUpdatePR` inside `OpenFromPrepared`, and it is short and does not
contain a cancellation point.

What can still land inside it is a **forge outage** — and that is D-35.5's case, where the correct
answer is to complete the delivery, not to undo it. So the two paths through that window have opposite
resolutions and both are right: *cancelled → we never entered it*; *forge down → we finish it*.

**Alternatives + decision point.**
- *Add `DeleteBranch` to `ForgeWriter`, used only by cancellation.* Rejected on **L1 security**: the
  interface's safety property is that the platform's entire write surface on a customer's repository is
  visible by reading one interface, and a destructive method there is destructive for every future
  caller, not only this one. P12 chose absence over policy precisely because a policy is a thing a
  reviewer has to remember.
- *Relax the fence to "no PULL REQUEST was left".* Rejected on **L2 stability**: a pushed branch with no
  pull request is exactly the mess FR28 names, and weakening the assertion to what the code currently
  does is how a requirement becomes a description.
- *Push to a scratch namespace and let it expire.* Rejected on **L5 evolvability**: it invents a second
  branch namespace with a lifecycle nobody owns, to avoid an ordering change that costs nothing.

**Effect.** `improvementrun` checks cancellation and the kill switch at the **delivery gate**, the last
point before `OpenFromPrepared`. Fence 7.10 asserts the forge received **no `EnsureBranch` call**, which
is a stronger and more directly observable claim than "no branch remains".
