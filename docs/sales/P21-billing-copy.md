# P21 Billing & Dunning — the customer-facing copy, and why it is worded this way

- **Status:** Accepted (2026-07-30)
- **Audience:** anyone who writes a billing message a customer reads — the console, an email, a support
  reply, a docs page.
- **Rule:** the honest version is the one that survives contact with the invoice. A billing message a
  customer can check against their own records is worth more than a reassuring one they cannot.

## 1. The four rules, before any copy

1. **Plans are named, never priced, in anything the platform ships.** Free / Team / Business /
   Enterprise. The number lives in Stripe and is administered by Finance. A price written into a screen,
   a doc, or a support macro outlives the moment it was true and ships anyway — and the build fails on
   one (`plancfg` payment-UI fence, `scan-bundle.mjs`).
2. **Dunning and refund behavior described to a customer matches Stripe's actual behavior.** The console
   renders the *mirrored* provider state; it has no branch that computes a payment status. So the copy
   must not describe a retry schedule, a grace length, or a refund window the platform does not control.
3. **The phrase "risk is controlled" (风险可控) appears nowhere.** The honest form is *risk is
   observable*. What this system can actually promise is that every charge is idempotent, every
   correction is additive and audited, and every figure traces to the record that justified it. Claiming
   control over a payment processor's outcomes is a promise made with someone else's system.
   **This one is a build gate**, not a review note: `web/console/scripts/scan-claims.mjs` fails the
   build if the phrase appears in any shipped source file, along with four other over-claims. Adding to
   that list is how a new banned phrase becomes real.
4. **No internal mechanism name reaches a customer.** *(Review responsibility — this one is not a fixed
   string, and a scan that tried to enumerate every internal name would be a scan somebody disables.
   Use the checklist in §8.)* No profile name, bundle name, script name, ledger
   type, or code path. A customer reads *"update your card to restore auto-merge"*, never
   *"TypePlanChange"* or *"the FlushPending drain"*. An internal identifier in a billing message is a
   support ticket nobody can answer and a hint nobody needed.

## 2. Past due / payment failed

**Both halves are required: a named reason, and a restore path.** A dunning banner with no next step is
an alarm — it tells someone something is wrong and leaves them to guess, which is how a recoverable card
decline becomes a churned account.

> **A payment did not go through**
>
> The payment provider could not take payment on the card on file.
>
> **To restore it:** add or update the payment method below. The provider retries on its own schedule,
> and a working card ends the retries.
>
> *invoice `payment_failed` · subscription `past_due` — the payment provider's own words, mirrored here
> rather than recomputed.*
>
> Your plan has not been changed. Access follows the provider's retry schedule, and if it ends without a
> payment the plan moves to the free tier at the period boundary — an audited change that deletes
> nothing and reverses when you pay.

Why each part is there:

- **The provider's own status words**, shown verbatim. The platform does not translate them; a state the
  provider owns and the platform renames is a state two systems eventually disagree about, and the one
  on screen is the one that is wrong.
- **"retries on its own schedule"** — not "we will retry in 3 days". The schedule is Stripe's and the
  platform must not quote a number it does not set.
- **"Your plan has not been changed"** — because it has not. Saying otherwise during the grace window
  would be describing a degradation that has not happened.
- **"deletes nothing and reverses when you pay"** — the single most reassuring true thing available, and
  it is true structurally rather than as a policy.

🚫 Do not write: *"Your account has been suspended."* (it has not) · *"We will retry in 72 hours."* (not
our schedule) · *"Your data will be deleted."* (nothing is deleted, ever) · *"Risk is controlled."*

## 3. Downgrade — and the one distinction that must not be blurred

There are **two different downgrades**, they happen at different moments, and using one sentence for
both would misdescribe whichever one the reader is actually in.

### 3a. A downgrade the customer chooses

> Your plan changes to **Team** now. What you can do changes immediately; the amount is prorated by the
> payment provider on its own schedule. Nothing you have already used or been billed for is removed.

The entitlement flips at the plan-change event — that is the product decision (P7 Q4), and it is the one
a customer expects when they click a button and then try to use the thing. **Proration is Stripe's**, so
the copy never states an amount or a credit; Stripe's invoice does.

### 3b. A downgrade dunning causes

> Your plan moves to **Free at the end of the current billing period** unless a payment succeeds before
> then. This is an audited change: nothing is deleted, and paying restores your plan.

This one **does** take effect at the period boundary, because the boundary is where Stripe's dunning
schedule ends. That is the sentence the task list means by *"takes effect at period end"*, and applying
it to 3a would tell a customer who just downgraded that nothing has changed yet, which is false.

🚫 Do not write: *"Downgrades take effect at the end of the period"* as a blanket statement. It is true
of 3b and false of 3a.

## 4. Gainshare — the most contestable line on the invoice

> **Verified savings on merged optimizations.** This line bills only for optimizations that were
> measured against a held-out set **and merged into your repository**. Each one links to the verified
> delta and the merge commit behind it.

Three things must be present, and the third is the one people forget:

1. **"verified and merged"** — both words. A verified saving that was never merged bills nothing, and a
   merge with no verification is not a saving.
2. **Evidence links** on the line itself: the verified delta, and the merge commit that shipped it. A
   gainshare line without them is a defect, not a rendering choice — the whole claim is that the
   platform bills only for savings it can show you.
3. **What was NOT billed.** The console shows the savings that were considered and contributed zero,
   each with its reason and its size. This is the trust claim, quantified: on a real repository the
   *largest* verified saving is often the un-merged one, and showing that it billed nothing is worth
   more than any sentence about integrity.

🚫 Do not write: *"We share in the savings we generate."* (too broad — only verified, merged ones) ·
*"Estimated savings of X."* (an estimate bills nothing and must not appear on an invoice) · any
gainshare figure without its evidence beside it.

## 5. Billing temporarily unavailable

> **Billing is temporarily unavailable.** This is a provider outage, not a problem with your account.
> Nothing has been charged twice, and no usage has been lost — the platform records what you use and
> reports it when the provider is reachable again. Your product is unaffected.

Both claims are structural and therefore safe to make: usage is recorded before it is reported, and the
report carries a derived idempotency key, so the outage window bills exactly once on recovery.

🚫 Do not render this as an empty invoice. *"We could not reach billing"* and *"you owe nothing"* are
different facts, and showing the second for the first is a lie the page tells confidently.

## 6. No payment method yet

> No payment method on file. Nothing is owed until you subscribe to a paid plan. When you do, the card
> is entered on the payment provider's own page — this platform receives a reference to it and never the
> card itself.

The last clause is worth saying because it is *provably* true rather than a policy: the collection
surface routes the card browser→Stripe, and the account model refuses to store anything PAN-shaped. In a
security review this is the answer, not a reassurance.

## 7. Checkout return

> **Your payment method was submitted.** The payment provider has your card. Your subscription becomes
> active when the provider confirms it — usually within a few seconds.

🚫 Do not write *"You're subscribed!"* on return from checkout. The page shows what the provider has
told the platform, so it is briefly **behind** rather than briefly **wrong** — and a customer who reads
"subscribed" and then sees the old plan has been told two things, one of which was false.

## 8. Review checklist

- [ ] No amount, rate, percentage or price band anywhere in the message.
- [ ] Every status word attributed to the payment provider is the provider's own word.
- [ ] Every unhappy state has a named reason **and** a next action.
- [ ] The downgrade sentence matches which downgrade it is (§3a vs §3b).
- [ ] Every gainshare figure is beside its evidence, and "verified **and merged**" appears.
- [ ] Nothing claims a schedule, a window, or a retry count the platform does not control.
- [ ] No internal type, file, script, profile or bundle name appears.
- [ ] The phrase "risk is controlled" appears nowhere.
