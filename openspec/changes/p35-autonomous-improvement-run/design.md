# Design — P35: The Improvement Run

## Context

Most of this phase is wiring existing components to a new caller, and that is precisely where the risk is.
Every safety property in the platform — the verification gate, the contract check, the entitlement check,
the automation-level check — was written against callers that exist today. A new caller is a new way for
each of them to be bypassed, and none of them will fail loudly if it is simply not called.

So the design's organising question is not "how do we build the run" but **"which existing gate could this
new path go around, and what makes that impossible rather than merely unlikely."**

## D1 — A question becomes a plan; an untranslatable question is refused

**Decision.** Scope, candidate cap, spend budget, stopping condition — produced before execution, shown to
the person, acknowledged above a disclosure threshold. A question that cannot be bounded is refused.

**Why refusal rather than defaults.** Defaults are how a conversational surface spends someone's money on a
search they did not ask for. The failure is silent and it is discovered on an invoice. An unbounded search
is not a larger version of a bounded one — it is a different product with a different risk, and the person
who typed a sentence did not choose it.

**Why the plan is an artifact.** Before the run, a plan is a decision the person can decline. After the
run, the same information is a receipt. Only one of those is useful.

## D2 — Re-measurement is allowed to disagree, and disagreement withdraws the change

**Decision.** After applying, re-measure. A change that fails to reproduce its verified delta is withdrawn
before delivery, and **both** numbers are reported.

**Why.** The P5.5 gate ran on held-out data *before* the change was applied. Re-measurement observes the
applied result. If the second observation can only confirm, it is theatre — and a ritual that cannot fail
teaches everyone downstream to ignore it.

**Why report both.** A withdrawal with one number looks like a bug. With two it is a finding: *this change
looked like +8% and measured +1% ± 4%*, which is information about the eval set as much as about the
change.

**The trap.** A delta can fail to reproduce because the change is bad, the eval set is noisy, the provider
moved, or the source moved. Withdrawing on the first reading alone would withdraw good changes for reasons
nobody can see. Three mechanisms must all be in place or none of them works: pinning holds the source and
config (FR17), the recorded provider model version holds the provider, and multi-seed intervals hold the
noise. This is stated here because it is the kind of dependency that gets discovered after the feature
ships and is blamed on the model.

## D3 — The hosted App is the default for the console surface only

**Decision.** Console-driven runs deliver through the hosted Git App; CLI- and CI-originated runs keep
CI-mediated delivery.

**Why a per-surface default rather than a per-tenant one.** ADR-005's reasoning is about what to do when
the platform does not know how the customer works. On the CLI it genuinely does not — the customer may have
any CI, any token policy, any review process. On the console it does: they have no CI integration with us
and no CLI installed, and the CI-mediated path requires both. Defaulting to a mode the customer cannot use
is a default that means "this feature is off".

**What must not drift.** The default is the *mode*, not the *scope*. The installation stays
per-repository, least-privilege and customer-revocable, and revocation is immediate and complete (not "at
the next token refresh"). The failure this phase is exposed to is the default quietly widening into "the
platform has write access to your account", which is a different product.

**Rejected.** *Hosted App everywhere, retire CI-mediated.* One code path, simplest story, and it makes a
write-scoped forge credential a permanent platform holding for every customer including the ones ADR-005
was written for.

## D4 — Approval is per proposal and bound to a hash

**Decision.** Each proposal carries its own approve and decline. No control approves more than one. An
approval is bound to `(config_hash, source_revision)` and is void if either moves.

**Why per proposal.** A bundle approval is one click that means several things, and the person will read
the first item and accept the rest. The pressure to add "approve all" is the most predictable request in
this phase and it should be refused with the reason written down in advance.

**Why hash-bound.** An approval that survives a revision change is an approval for a diff nobody saw. This
is the same reasoning P10 applies to measurement runs — what ran is reconciled against what was requested —
applied to consent.

## D5 — The five "nothing to propose" states are five sentences

**Decision.** `proposalgen`'s closed `State` is surfaced by name.

**Why.** P30 found the surface discarding it, and the consequence is that a customer with no linked runs
and a customer with no discovered graph see the same empty screen and take the same (wrong) action. The
states already exist; the only work is not throwing them away.

## D6 — Cancellation is atomic with respect to the customer's repository

**Decision.** After a cancel, either a pull request exists or nothing was pushed.

**Why.** A half-pushed branch is a mess the customer has to clean up, from a run they explicitly stopped.
That is the worst possible moment to hand someone work.

**How.** Delivery is the last step and is idempotent per `(config_hash, source_revision, target)`, so the
reconciliation pass can always answer "was this delivered" from the append-only record and either complete
or clean up. That pass is a **necessary path** — it runs every cycle, not only after a failure — because a
repair path that only runs after failures is a path that is never exercised until it is needed.

## D7 — No second gate

**Decision.** The request's step "if everything is good and there are improvements" maps onto the existing
P5.5 verified-delta gate. P35 adds nothing.

**Why.** A second gate is a second place for the first to be bypassed, and two gates with slightly
different conditions eventually disagree — at which point the safe answer is whichever one someone
remembered to call.

## The gate inventory — what this new caller must not bypass

| Gate | Where it lives | How P35 guarantees it is called |
|---|---|---|
| typed I/O contract | `internal/typedcontract` | candidate rejected before verification; fence 3 |
| verified delta, held-out | P5.5 `internal/verification` | delivery is downstream of it, no branch; fences 1–2 |
| entitlement: plan **and** automation level | P7 | evaluated server-side; the conversation cannot raise either |
| transform refusal | `internal/transform` | no plan, role, flag or parameter materialises a refused config |
| human approval | `internal/approval` | the only approval path; fence 7 |
| never merge below Autonomous | `forgedelivery` | fence 6, tested at every level |

**Every one of these fences must be run through the conversation**, not only through the optimizer. The
optimizer's own tests prove the optimizer calls them; they say nothing about a new caller.

## Risks this design accepts

- **The conversation is a new caller for six gates.** Mitigated by testing each through the new path, and
  by the inventory above existing as a checklist rather than as tribal knowledge.
- **Re-measurement's three-mechanism dependency** (D2). If any one is missing, withdrawals become
  arbitrary and the feature will be blamed on model variance.
- **The write credential's scope is a slope.** Per-repository today, and every future convenience request
  pushes toward per-account. FR25's immediate-and-complete revocation is the property that keeps the
  customer in control regardless.
- **Scheduled runs do not compose with per-proposal approval** (PRD §14 Q3). Either they stop at proposals
  or they require Autonomous. There is no third answer, and pretending otherwise would produce unattended
  delivery below the automation level that authorizes it.
