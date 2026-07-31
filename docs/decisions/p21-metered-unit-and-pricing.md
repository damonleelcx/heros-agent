# P21 / Q7 — the metered unit, and the prices denominated in it

- **Status:** Decided and configured (2026-07-30)
- **Decides:** PRD [`P21-stripe-payments.md`](../prd/P21-stripe-payments.md) open question **Q7**
- **Related:** [`p21-m16-exit-checklist.md`](p21-m16-exit-checklist.md) · [`p21-billing-webhook-ingress.md`](p21-billing-webhook-ingress.md) · [`p7-packaging-and-paywall.md`](p7-packaging-and-paywall.md)
- **Language note:** written in English per the standing rule for this repository, using the
  decision-exposition structure the Chinese tech-design-doc convention requires (problem → design →
  why it fits → alternatives considered → resulting effect).

---

## 0. The decision, in one paragraph

**The metered unit is one US dollar of spend under management.** The SUM meter already produces its
figure in USD, the platform reports it without multiplying anything, and Stripe records whole-unit
quantities — so the only unit that can be reported integrally is the meter's own, at whole-dollar
granularity. Metered prices are therefore denominated **per USD of SUM**: 3¢ on Team, 2¢ on Business,
1¢ on Enterprise, which is a 3% / 2% / 1% management fee that declines with volume. Gainshare is
denominated **per USD of verified, merged saving** at 20¢, which is a 20% share. Every rate is a whole
number of cents, so Stripe rounds nothing either.

---

## 1. The original requirement

Three verification steps — the metered charge, the invoice read-back, and reconciliation — had nothing
to run against, because the Stripe account carried four products with a single licensed monthly price
each and **no metered price and no gainshare price at all**.

Creating them needed one answer that no one could give from the code: *what unit is the metered price
denominated in?* The platform reports a whole-unit quantity and refuses to round one, so the unit has
to be something the SUM meter produces integrally.

---

## 2. The decision

| Price | Unit of one quantity | Rate | Stripe `unit_amount` | Stripe price id |
|---|---|---|---|---|
| Team metered | 1 USD of spend under management | 3% of SUM | 3¢ | `price_1TytR8L7abkWCmrIIVcnDXx8` |
| Business metered | 1 USD of spend under management | 2% of SUM | 2¢ | `price_1TytR9L7abkWCmrIDHzz1Vu1` |
| Enterprise metered | 1 USD of spend under management | 1% of SUM | 1¢ | `price_1TytRAL7abkWCmrIolh3yyG1` |
| Enterprise gainshare | 1 USD of verified, merged saving | 20% of savings | 20¢ | `price_1TytRAL7abkWCmrI8VnlQpea` |

All four are **`type=one_time`** prices. The reason is in §5.

The reconciliation ledger is one Stripe **billing meter**, `heros_sum`
(`mtr_test_61V8HKNFrppcXrIBO41L7abkWCmrI184`), aggregation `sum` over payload `value`, customers mapped
by `stripe_customer_id`.

Worked example, from the run that verified it: the hermes period's SUM is **432**, so the metered line
is 432 × 3¢ = **$12.96**, and Stripe's invoice total for that period is **1296**. The platform sent the
number 432 and nothing else.

---

## 3. Why this unit

**What problem it solves.** `billing.stripeQuantity` refuses a fractional quantity rather than rounding
it, because rounding silently changes what a customer is billed and every individual number still looks
plausible. That refusal is only livable if some unit exists in which the reported figure is a whole
number.

**Why it is designed this way.** `metering.SUMResult` carries `Quantity float64` and `Unit: "USD"` — the
customer's own provider spend, computed from the customer's own price book. The platform hands that
figure to Stripe unaltered. So the unit of the price must be the unit of the figure: one USD of spend
under management, and the quantity is how many of those dollars.

**Why that fits.** It makes the platform's non-negotiable rule and Stripe's data model the same
statement. The platform never computes money; it reports a count. Stripe holds the rate and does the
multiplication. Nothing in between has to agree about arithmetic.

**Alternatives considered, and why each was rejected.**

- **Denominate in cents, or micro-dollars.** This is what most usage platforms do, and it would let
  sub-dollar spend be billed exactly. It requires the platform to multiply the meter's figure by 100 or
  by 1,000,000 before reporting. That is the platform scaling a quantity to reach a price, which P7
  Decision 10 refuses outright — and the refusal is load-bearing, because a scale factor is exactly the
  kind of constant that gets changed once, in one of two places, by someone who is confident.
- **Denominate per $1,000 of SUM**, the shape a lot of cost-management pricing takes. Same problem
  mirrored: the platform would have to divide, and a division that does not come out even reintroduces
  the fraction it was meant to remove.
- **Round the SUM to whole dollars in the meter.** Moves the rounding upstream without removing it. The
  platform would then be deciding what a customer is billed, which is the thing every other part of
  this design is arranged to prevent.
- **Let Stripe bill the metered subscription item directly** and drop the platform's metered charge.
  Genuinely tempting — Stripe would own the whole computation. Rejected because the platform's ledger
  would then hold no record of what was charged, `TypeMeteredCharge` would settle against nothing, and
  the invoice line would lose the `platform_basis` metadata that makes `Invoice.Validate` able to say
  why each line exists.

**The resulting effect.** A period whose SUM is a whole number of dollars bills exactly. A period whose
SUM is not is **refused, loudly**, with a message naming the remedy — and that refusal is demonstrated
on purpose in `cmd/proof/payments`, because a constraint nobody has seen fire is a constraint nobody
believes.

**What this costs, stated plainly.** Sub-dollar precision is gone. A customer whose SUM for a period is
$187.43 cannot be billed on $187.43 under this decision. That is a real limitation, not a rounding
detail, and the honest options if it ever matters are (a) Finance denominates a finer unit and someone
revisits P7 Decision 10 with open eyes, or (b) the meter itself is redefined to emit an integral unit at
source. Neither is a change an engineer should make quietly, which is the point of writing it down.

---

## 4. Why these rates

**What problem it solves.** The unit decision fixes *what* is counted. It says nothing about what a unit
should cost, and the account cannot be configured without an answer.

**Why it is designed this way.** A management fee on spend under management, declining with volume —
3% / 2% / 1% across Team / Business / Enterprise — plus a 20% share of savings the platform has verified
and that were actually merged.

**Why that fits.** Three properties, in order of how much they matter:

1. **Every rate is a whole number of cents.** Stripe's `unit_amount` is an integer number of cents, so a
   rate like 2.5% would need `unit_amount_decimal` and would reintroduce fractional money on Stripe's
   side — after all the work spent removing it from the platform's. Whole cents mean neither side rounds.
2. **The fee declines as spend grows**, which is the ordinary shape for this kind of product and the one
   a customer does not have to have explained to them.
3. **Gainshare at 20%** is the conventional savings-share rate, and it applies only on Enterprise —
   the only plan carrying `auto_merge`, which is the capability that produces merged savings in the
   first place.

**Alternatives considered.** A flat rate across plans (rejected: gives the largest customers the worst
deal, and the plan ladder then has nothing to say about volume). A higher gainshare with no management
fee (rejected: revenue would depend entirely on the optimizer finding merged wins, so a quiet quarter
bills nothing while the platform still runs).

**The resulting effect.** At each plan's SUM band the metered component is $9.00/month on Team and
$400/month on Business; Enterprise is unbanded at 1%. On the hermes run, the 73 USD of verified, merged
saving would bill $14.60 of gainshare — while the 208 USD of verified but **unmerged** saving bills
nothing, which remains the invariant P5.5 and P7 FR3/FR12 exist to protect.

🔴 These are the rates configured in a **test-mode sandbox**. They are a working default that satisfies
the constraints above, not a commercial commitment. Finance owns the number; this document owns the
unit, and the unit is the part that is expensive to change later.

---

## 5. Why the metered price is `one_time` and the meter is separate

This is the part the real account settled, and it is not what the code assumed.

**What problem it solves.** The platform does two different things with metered usage: it **charges**
for it (`RaiseCharge` → a Stripe invoice item) and it **reports** it (`ReportUsage` → Stripe's own
record of what was used, so the two can be reconciled). Before this change, one `price_refs["metered"]`
entry served both.

**What the wire says.** They cannot be the same object:

- An invoice item accepts **only** `type=one_time` — *"The price specified is set to `type=recurring`
  but this field only accepts prices with `type=one_time`."*
- A metered price **must** be meter-backed — *"Starting with Stripe version `2025-03-31.basil`, metered
  prices must be backed by meters."* — and a meter-backed price is recurring, so an invoice item
  refuses it.

**Why the split is designed this way.** The **one-time price** is the charging instrument: the platform
sends a quantity, Stripe applies the rate, and the resulting invoice line carries the platform's
`platform_kind` / `platform_period` / `platform_basis` metadata. The **meter** is the reconciliation
instrument: an independent Stripe-side record of what was used, carrying no price at all.

**Why that fits.** It makes reconciliation mean something. Because the meter is not the thing that
billed, comparing the platform's usage record against the meter compares **two separately authored
numbers**. Had the meter been the billing instrument, a reconciliation would have been comparing the
platform's figure against a figure derived from the platform's figure, and would have agreed with itself
by construction.

**Alternatives considered.** Attaching the meter-backed price to the subscription as a second billing
item was tried and rejected: Stripe would then invoice the metered usage *and* the platform's invoice
item would invoice it again — the same SUM billed twice. The four meter-backed recurring prices created
while establishing this are archived in the account rather than deleted, so the trail of what was tried
is still visible.

**The resulting effect.** `price_refs["metered"]` and `price_refs["gainshare"]` are one-time prices;
`heros_sum` is a meter with no price attached; and the preflight now refuses a price of the wrong shape
at configuration time (§6) instead of at the period's first charge.

---

## 6. Key invariants

1. The platform reports a **quantity**, never an amount — on the meter event, on the invoice item, and
   on the credit note line.
2. A **fractional** quantity is refused, never rounded.
3. A period **older than Stripe's 35-day meter-event window** is refused, never restamped to `now`.
   Restamping would attribute old usage to the current month and nothing downstream would look wrong.
4. Every configured price reference must **resolve AND be the right shape** for the charge kind it is
   configured under, checked before anything charges.
5. The meter is a **ledger, not a biller**. Nothing is attached to it that could invoice.
6. A correction credits the **quantity Stripe holds**, read back from Stripe's own line — not the
   quantity the platform believes it charged.

---

## 7. What the account is configured with now

```
account   acct_1Ty5ZeL7abkWCmrI  "Heros Agent sandbox"  US / USD  TEST MODE

products  Free / Team / Business / Enterprise           (pre-existing)
prices    4 licensed monthly subscription prices        (pre-existing: $0 / $9.99 / $19.99 / $29.99)
          4 one-time prices, this decision              (3¢ / 2¢ / 1¢ / 20¢ per unit)
          4 meter-backed recurring prices               ARCHIVED — see §5
meters    heros_sum        active     sum over payload.value, customer by stripe_customer_id
          heros_gainshare  INACTIVE   created, then deactivated: the platform reports only SUM as
                                      usage, and a meter nothing writes to is configuration that lies
```

---

## 8. Design boundaries — what this does not decide

- **It does not set live-mode prices.** The rollout flag is still at its zero value, which is test.
- **It does not decide who owns the rate going forward.** Finance does; this fixes the unit.
- **It does not add a second metered meter.** `meterNameFor` still states, as a fact rather than a
  lookup, that the platform reports exactly one.
- **It does not change how SUM is derived.** The cost-event substrate and its dedup are untouched.

---

## 9. Risks

| Risk | Why it is acceptable now | What would change that |
|---|---|---|
| Sub-dollar SUM cannot be billed | Refused loudly, never silently rounded; no customer is mis-billed | A customer whose monthly SUM is genuinely under a few dollars — then §3's stated options apply |
| A period reported more than 35 days late is refused | Reporting happens during the period in normal operation | A backfill or a migration; the refusal is the signal to plan it, not to work around it |
| A re-derived SUM cannot overwrite Stripe's recorded figure | The reconciler surfaces the divergence as drift | A need to *repair* rather than surface — which would be a P7 Decision 7 change, not a code change |
| The rates are a working default, not a commercial decision | Test mode; nothing has been charged for real | The live-mode cutover (V2), which is gated on Finance sign-off anyway |

---

## 10. When this document must be updated

- Finance changes a rate, or adds a plan with a metered component.
- A second metered meter is introduced (then `meterNameFor` becomes a real lookup).
- The pinned Stripe API version moves and either constraint in §5 changes.
- Anyone proposes making the platform multiply or divide the reported quantity — at which point §3's
  rejected alternatives are the conversation, not a fresh one.
