# P23 — Terms of Service, reconciled line by line against what the software executes

**Status:** reconciled 2026-07-31 against Terms **v1.0.0** (`/legal/terms`, effective 2026-07-31).
Closes **task 8.8**.

## Why this document exists

A refund term Stripe cannot execute is a promise software will break. So every commercial sentence in
the Terms is checked here against the code that would have to perform it — P7 entitlements, P21 Stripe,
and the delivery record P12 writes.

The rule this applies is the sales-operations one — *only promise what has been delivered* — at the point
where over-promising stops being a support ticket and becomes a legal liability.

**Method:** each row quotes the Terms, names the mechanism, and states the verdict. A row that cannot be
traced to a mechanism is a **defect in the Terms**, not a note for later.

---

## 1 · Plans and entitlements

| Terms §4.1 says | Mechanism | Verdict |
|---|---|---|
| Four plans: Free, Team, Business, Enterprise | `internal/plancfg` — plan set is configuration; `DisplayName` carries the customer-facing name | ✅ matches |
| "included features and limits are stated on the plans page and in the console at the time you subscribe" | `internal/entitlement` resolves features and limits from the live plan snapshot | ✅ matches. The Terms deliberately do **not** enumerate limits, so a plan change is not a Terms change |
| "The `heros` command-line tool and repository discovery are available on every plan, including Free" | `plancfg.FeatureCLI` and `plancfg.FeatureDiscovery` are both documented as available on every plan including Free | ✅ matches |

**Deliberate omission:** the Terms name no prices. Prices are configuration and a price in a legal
document is a price that goes stale in a version nobody wanted to publish. Rates as *percentages* are
named, because those are the commercial basis rather than a number.

---

## 2 · The metered charge

| Terms §4.2 says | Mechanism | Verdict |
|---|---|---|
| Billed "per US dollar of spend under management" | The metered unit is 1 USD of SUM; the meter produces USD and the platform reports it without multiplying | ✅ matches |
| 3% Team / 2% Business / 1% Enterprise | Stripe unit amounts 3¢ / 2¢ / 1¢ per USD of SUM | ✅ matches — the percentage and the unit amount are the same statement |
| "Quantities are reported in whole US dollars, and every rate is a whole number of cents, so nothing is rounded in our favour" | The platform refuses to round a reported quantity; every rate is a whole number of cents so Stripe rounds nothing either | ✅ matches |
| "Your model provider bills you directly… We do not resell model capacity and we take no margin on it" | Evaluation calls providers with the customer's own keys, from the customer's machine. No provider billing path exists on our side | ✅ matches |

---

## 3 · Gainshare — the three conditions

This is the section with the most legal exposure, so each condition is traced to the refusal that
enforces it.

| Terms §4.3 condition | Enforcing refusal | Verdict |
|---|---|---|
| "You have recorded consent to gainshare" | `ErrNoGainshareConsent` — the charge is refused when `account.gainshare_consent` is false | ✅ enforced in code |
| "The saving is in the verified-delta ledger" | `ErrSavingsNotVerified` — refused when the claimed saving is absent from, or not billable in, the ledger | ✅ enforced in code |
| "The change was merged… Merge is an observed fact read back from your forge" | The `delivery` record's `merged` state is an observed fact from the forge, not an inference from a pull request closing | ✅ enforced in the data model |
| "20% of verified, merged savings — one US dollar of verified saving, charged at twenty cents" | Gainshare price is 20¢ per USD of verified, merged saving | ✅ matches |
| "Consent is revocable" | `account.gainshare_consent` is a boolean with a paired `consented_at`, and a database constraint requires the two to move together — so a revocation cannot leave a stale timestamp that reads, in an audit, as still-consented | ✅ enforced by constraint |
| "Every gainshare line carries the evidence that justified it" | Gainshare lines carry their verified-delta references and merge commits | ✅ matches |

**A promise checked and kept:** "A pull request you close without merging is billed nothing." Closing is
not merging in the delivery record, and gainshare joins on the merged state.

---

## 4 · Payment, plan change, cancellation

| Terms §4.4 says | Mechanism | Verdict |
|---|---|---|
| "Payments are processed by Stripe. We do not receive, hold or store your card details" | `account.provider_customer_handle` is an opaque handle; a `CHECK` constraint rejects the PAN family (a 12–19 digit run, with or without separators) | ✅ **enforced by the database**, not by care |
| "Proration is calculated by Stripe under its own schedule; we neither compute nor store it" | `ChangePlan` repoints the subscription at the new price; proration is explicitly Stripe's — the platform neither computes nor stores it | ✅ matches exactly |
| "Your entitlements change at the moment of the plan change… rather than waiting for an invoice" | The entitlement flips at the plan-change event by the same audited path the webhook uses, deliberately not waiting for an invoice | ✅ matches |
| "You cancel through your own billing portal. We do not cancel a subscription on your behalf" | On a move to the free tier the subscription is left alone: cancelling is the customer's own flow, and doing it for them would be the platform making a money decision on their behalf | ✅ matches, and the code comment gives the same reason the Terms do |
| "Access continues to the end of the period you have paid for" | Stripe's subscription period governs | ✅ matches — this is Stripe's behaviour, stated rather than re-implemented |

---

## 5 · Corrections and refunds

| Terms §4.5 says | Mechanism | Verdict |
|---|---|---|
| Corrections are "additive… rather than by rewriting the original record" | `Refund` and `Credit` are additive corrections raised against a prior charge; the original event is not mutated | ✅ matches |
| "what a refund means for your payment instrument is determined by Stripe, including how long it takes to reach you" | The provider decides what "refund" means for the payment instrument | ✅ matches — and this is the sentence that stops the Terms promising a settlement time the platform does not control |
| "Beyond correcting our own billing errors, fees are non-refundable except where required by law" | No automatic refund path exists beyond corrections | ✅ matches — no mechanism is claimed that does not exist |

🔴 **The rewrite that was avoided.** An earlier phrasing would have said refunds are issued "within 5–10
business days". Nothing in this system controls that interval; Stripe and the issuing bank do. A term
stating it would be a promise the software cannot keep, which is precisely what this document is for.

---

## 6 · Availability, certification, sub-processors

| Terms says | Reality | Verdict |
|---|---|---|
| §7: "We do not offer a service-level agreement, and these Terms contain no availability commitment" | There is no availability measurement and no credit mechanism | ✅ **correctly claims nothing** |
| §15: no SOC 2, no ISO certification, no audit attestation | None exist | ✅ correctly claims nothing |
| §15: no sub-processor list | Verified 2026-07-31: there is **no vendor-operated hosted deployment**, so there are no sub-processors to list | ✅ correctly claims nothing |
| §15: no data-residency guarantee | `deploy/` names no region and no vendor account; the operator chooses | ✅ correctly claims nothing |

These four are the rows most commercial pressure will be applied to. Each becomes a commitment when it
is real, in a version recorded as **material** — not by quietly deleting a sentence from §15.

---

## 7 · Findings

**No term in v1.0.0 promises something the software cannot execute.** Two things were caught while
writing it and are recorded so the reasoning survives:

1. **A refund settlement window was almost stated.** Removed: the interval belongs to Stripe and the
   issuing bank. §4.5 now says who decides it instead of what it is.
2. **"Your data will be deleted on cancellation" was almost written.** It would have been false — see
   the [data inventory](../decisions/p23-data-inventory.md) §5: **no job enforces retention** on most
   stores. §8 now says data is retained as described in the Privacy Notice, and the Privacy Notice says
   plainly that most stores have no automatic deletion. The console's own banned-phrase fence already
   refuses "your data will be deleted" anywhere in shipped source, for the same reason.

---

## 8 · What is outstanding

Named rather than implied, so nobody reads this document as a sign-off it is not.

| # | Outstanding | Owner |
|---|---|---|
| O1 | **Counsel review of v1.0.0.** This reconciliation establishes that every commercial sentence matches a mechanism. It does not establish that the document is enforceable, complete for the jurisdiction, or optimal — that is counsel's, and it is the remaining step before this text is relied on in a negotiation. | Business + counsel |
| O2 | **Acceptable use for sandboxed execution and forge delivery** (PRD OQ5) — §3 covers both in general terms. Whether they warrant their own section is counsel's call. | Counsel |
| O3 | **The moment a hosted deployment exists**, §7, §15 and the Privacy Notice's §1 and §5 all become live obligations at once. That is a single, foreseeable trigger, and it is written here so it is met deliberately. | System Design |

---

**Re-run this reconciliation on every Terms version.** A version that changes a commercial sentence
without a row here is a version whose promise nobody checked against the software.
