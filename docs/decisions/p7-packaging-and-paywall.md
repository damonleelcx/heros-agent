# P7 — Packaging, the paywall-as-invitation, and gainshare consent

Product design for the P7 commercial surface. Companion to
[`openspec/changes/p7-billing-metering/design.md`](../../openspec/changes/p7-billing-metering/design.md)
(engineering) and [`docs/prd/P7-billing-metering.md`](../prd/P7-billing-metering.md) (rationale).

> **No dollar amounts, percentages, or price bands appear in this document, in any design asset, or in
> the client.** Plans are referred to by **name** (Free / Team / Business / Enterprise). Every limit and
> every amount is configuration, resolved at runtime and rendered from the API.

---

## 1. Glossary — the words the product uses

One name per concept, used identically in the UI, the API, the docs, and support conversations. A
concept with two names is a concept customers and support will disagree about.

| Term | Means | Never called |
|---|---|---|
| **Spend under management (SUM)** | The customer's own LLM provider spend that the platform observes and optimizes, per billing period. Derived from the P2.5 cost events. | "our usage", "your bill", "tokens" |
| **Plan** | The named packaging tier: Free / Team / Business / Enterprise. | "tier", "license", "SKU" |
| **Entitlement** | What the active plan **and** the automation level together permit. | "permission", "feature flag" |
| **Automation level** | Advisory / Assisted / Autonomous — the contract for how much the platform may do on its own. | "mode", "aggressiveness" |
| **Verified saving** | A SUM reduction that passed the P5.5 held-out gate **and** was merged. | "savings" unqualified, "estimated savings" |
| **Estimated saving** | A projection that has **not** been verified or merged. Shown; never billed. | "savings", "potential savings" (in the same breath as a charge) |
| **Gainshare** | Billing a share of **verified** savings. | "success fee", "commission" |
| **Over-limit** | An action past the plan's metered allowance for the period. | "blocked", "quota exceeded" |
| **Past due / payment failed** | The **provider's** dunning state, mirrored. | "suspended", "account locked" |

**The load-bearing distinction is _verified_ vs _estimated_.** The product shows both and bills only the
first. Wherever a saving appears, its status appears with it — as text, never as a colour.

---

## 2. The packaging boundary

| Surface | Free | Team | Business | Enterprise |
|---|---|---|---|---|
| CLI + repository discovery | ✓ | ✓ | ✓ | ✓ |
| Assisted verified pull requests | — | ✓ | ✓ | ✓ |
| Web dashboard (seats / retention / SUM band per plan) | — | ✓ | ✓ | ✓ |
| **Autonomous auto-merge** | — | — | — | ✓ |

**Which plan lists which surface is configuration**, published from the config store; the table above is
the intended shape, not a compiled constant. What *is* fixed in code is the rule that both axes are
checked and that a denial is explicit.

### Why the boundary sits here

- **Free must be genuinely useful, not a demo.** CLI + discovery is the whole "what is my agent
  actually doing" story. A Free tier that cannot answer that teaches nothing and converts nobody.
- **The first paid step buys _shipping_, not _seeing_.** Team is where the platform starts opening
  pull requests — the point where it does work rather than reporting it.
- **Auto-merge is Enterprise-only because of what it is, not what it costs.** It edits and merges the
  customer's code. Restricting it to the tier that contracted for it is the same discipline as P6's own
  prerequisite gate, applied commercially.

---

## 3. The paywall as an invitation

**Every denial names three things: what was gated, why, and the plan that lifts it.** A denial that
names fewer is a dead end, and a dead end in a metered product reads as a bug.

```
┌──────────────────────────────────────────────────────────────────┐
│  ⃠  Assisted pull requests are not included in the Free plan     │
│                                                                  │
│     Upgrade to Team to open verified optimization pull requests. │
│                                              [ See plans ]       │
└──────────────────────────────────────────────────────────────────┘
```

Rules that hold for every denial state:

1. **Name the cheapest plan that lifts it, not the top one.** Pointing a Free customer at Enterprise
   when Team would do is a paywall; naming what they actually hit is an invitation.
2. **Never silently drop, never silently degrade, never silently allow.** These are the two failure
   modes that erode trust in metered software: a silently-allowed over-limit action is a surprise
   invoice, a silently-dropped one is mysterious breakage. Both are worse than a clear "no".
3. **An over-limit denial shows the meter.** "The Team plan allows spend under management up to
   *[limit]* for this period; you are at *[observed]*." The numbers come from the API. Showing "denied"
   without the magnitude gives the customer nothing to act on.
4. **A level mismatch offers no upgrade plan.** If the action was requested at a weaker automation level
   than the surface requires, no plan fixes it — saying "upgrade to Enterprise" would be actively
   misleading. Say what the level requires instead.
5. **The denial is reachable by keyboard and readable by a screen reader.** It is an
   `aria-live="polite"` region with a focusable primary action; status is carried by **text**, never by
   colour alone.

---

## 4. Gainshare consent — an informed, recorded, revocable contract

Gainshare is the sharpest edge in the product: the platform bills a share of savings **it claims to
have produced**. The consent flow's job is to make that claim inspectable *before* the customer agrees,
not after the first invoice.

### What the consent screen must state, in the customer's words

- **The tradeoff, plainly:** "a share of the savings we **verify and merge**."
- **What is billed:** only savings that passed the held-out verification gate **and** shipped as a
  merged pull request.
- **What is never billed:** estimated savings, unverified savings, and verified savings that were never
  merged. Stated explicitly, because the customer's reasonable fear is exactly this.
- **The method:** the fixed baseline + hold-out methodology — which cases were held out, which
  generating cases were excluded, how many seeds — reachable from the screen, not buried in a PDF.
- **How it is evidenced:** every gainshare line on every invoice links to the verified delta and the
  merged pull request behind it.

### Revocable, and visibly so

Consent is a state on the account with a timestamp, and revoking it is a control in the same place as
granting it — not a support ticket. Revocation stops future gainshare charges immediately; it does not
retroactively void past ones (those already trace to merged, verified work), and the screen says so.

### Acceptance criteria

| # | Criterion |
|---|---|
| C1 | Consent cannot be given without the tradeoff sentence and the never-billed list being on screen. |
| C2 | The baseline + hold-out method is reachable in one interaction from the consent screen. |
| C3 | Consent records **who** and **when**; revocation clears the timestamp so an audit cannot read a revoked account as consented. |
| C4 | Revocation is available wherever consent is, at the same visual weight. |
| C5 | With consent absent or revoked, a gainshare charge is refused by the server, not merely hidden by the client. |
| C6 | Consent and revocation are keyboard-reachable and announced. |

---

## 5. The billing surface

One page, four questions, in this order — because it is the order a customer asks them:

1. **What am I spending?** SUM under management for the period, with the trend across periods.
2. **What am I on, and what does it entitle?** The plan by **name**, and the entitlement list with each
   surface marked included / not included — text, not colour.
3. **What am I being charged, and why?** The invoice broken into **subscription / metered / verified
   gainshare**, every line traceable to the usage that justified it.
4. **What did you actually save me?** Verified savings, each linked to its verified delta and merged
   pull request. Estimated savings appear in their own clearly-labelled group, marked *not billed*.

### First-class states

Not edge cases — each is a designed state with its own copy:

| State | What the customer sees |
|---|---|
| **Loading** | Skeleton, never a spinner over stale numbers. A stale figure that looks live is worse than no figure. |
| **Empty** | "No usage recorded for this period yet" — distinct from zero spend, which is a real measurement. |
| **Over-limit** | The denial banner from §3, with the meter and the upgrade path. |
| **Payment failed / past due** | The **provider's** state, mirrored, with what happens next and where to fix it. Never the platform's own invented status. |
| **Drift detected** | Reconciliation found a disagreement between the meter and the provider. Surfaced, not hidden — the customer finds out from us, not from their statement. |
| **No verified savings** | "Nothing verified and merged this period" — explicitly not "0 saved", which reads as a failure rather than an absence. |

### Non-negotiables for the client

- **No dollar figure, percentage, or limit is hardcoded.** Every amount and every allowance is rendered
  from the API/config. A grep for a currency literal in the client is a defect.
- **Amounts the platform does not hold are not invented.** The platform stores provider *handles*, not
  amounts; where only a handle exists, the UI shows the handle and links out, rather than rendering a
  number it cannot justify.
- **Status is text.** Every chip carries a word; colour is redundant reinforcement.

---

## 6. Charts

Two, and only two, because the page answers four questions and only two of them are shaped like a trend:

| Chart | Form | Why |
|---|---|---|
| SUM under management, by period | Bar, one series | Magnitude over a small number of discrete periods. One series needs no legend — the title names it. |
| Baseline vs optimized SUM | Grouped bar, two series | The savings story *is* the comparison. Two series, direct-labelled, with a legend. |

Palette: the house categorical steps already validated for this codebase's chart surfaces —
`#3987e5` / `#008300` on dark (`#1a2332`), `#2a78d6` / `#1a7f37` on light (`#ffffff`). Both pass the
lightness band, chroma floor, CVD separation, normal-vision floor, and contrast checks. The light-mode
pair's tritan separation sits in the 6–8 floor band, so the two series carry **secondary encoding** —
a legend, direct labels, and a surface gap between bars — rather than relying on hue.

**One axis.** Never two y-scales: SUM and savings share a unit and a scale, and anything that does not
gets its own chart.

---

## 7. Open questions carried forward

- **Realization timing for gainshare** (PRD Q1/Q2): billing on realized per-period SUM reduction, capped
  by the verified delta, is the engineering decision; the *copy* for "why is this month's gainshare
  smaller than the verified delta" is not yet written.
- **Seat management UI** is out of P7's scope; the seat *meter* and its over-limit denial are in.
