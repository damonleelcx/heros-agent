# Design review — P8 operator console, experience layer (§15)

| Field | Value |
|---|---|
| Change | `p8-admin-console` §15 — experience craft above the interface floor |
| Reviewer (Product Designer) | **Claude Opus 4.8**, acting as the console's product designer under `senior-product-designer-workflow` |
| Frontend | **Claude Opus 4.8**, under `senior-frontend-dev-workflow` |
| Date | 2026-07-24 |
| Verdict | **Accepted** — with the two follow-ups recorded in §5 |
| Evidence | Rendered-browser walkthrough in Chrome at 1728×938 and 1552×792, plus `npm test` (35), `npm run scan:tokens`, `npm run baseline` |

> FR37 requires a **recorded design review with its reviewer named** for a new view. This is that
> record. It is written to be read by the next person who changes this console, so it states what was
> decided and *why it may not be undone* — not merely that a review happened.

---

## 1. What was reviewed

The console's whole surface, rebuilt above its floor: one design language (`web/design-system/`), a
closed primitive set, comparison-ready numbers, a reserved hazard palette, a command palette,
URL-addressable views, a live operating picture, motion with a budget, receipts carrying audit
references, and the three-outcome command feedback.

New view: **Overview** (`/`) — the operating picture. Every other view was refactored onto the
primitive set without changing what it can do (§3).

---

## 2. The decisions this review signs off

**The operator accent moved off amber.** The previous identity used `--warn`'s hue for the chrome,
the links, the primary buttons and the chart fill. That is the FR31 violation with the highest cost on
this surface: it spends the signal the kill switch depends on, so by the time an operator reaches a
control that really is dangerous, amber and red have been ordinary for the whole session. The accent
is now a cool teal-cyan, ≥60° from both hazard hues, asserted by a test. **Do not move it back toward
amber, orange or red.**

**The home is the operating picture, not the tenant list.** The console opened on a table of names,
which answers a question nobody arrives with. Both questions an operator does arrive with — *is
anything halted*, *is anything wrong* — took three to four navigations. They now take none.

**Density is a preference and carries no friction.** No reason, no confirmation, no audit entry.
Friction is for the write path to the platform; spending it on "make my rows tighter" is how operators
learn to click through confirmations.

**Receipts link to the audit entry.** "What did I just do, and where is the record?" is asked after the
moment has passed. Verified live: arming the per-tenant kill switch produced entry 5, and the
receipt's link resolved to that entry with the same actor, target and reason.

**Outcome unknown is a first-class rendering.** Verified live by killing the admin API mid-command:
the console rendered *"Outcome unknown — do not retry yet"* with a link to the audit log, distinct
from both the green receipt and the red failure.

---

## 3. Feature-loss check (the review's main job)

Per `senior-frontend-dev-workflow` §1, a redesign may not silently drop what the previous version
could do. Every surface was walked against the previous implementation:

| Surface | Capabilities before | After |
|---|---|---|
| Tenants | search by name/plan, list with status + halt state + config version, links to detail | **all present**, plus halted count, plus ⌘K subject search |
| Tenant detail | plan/status/halt/quota overview, suspend, reactivate, quota, entitlement override, impersonate, denied-with-escalation per capability | **all present**, overview re-expressed as stats + a quota table |
| Billing | tenant search, reconciliation, drift detail, gainshare evidence + exceptions, invoices, credit, refund | **all present** |
| Registry | add model, list, repoint price ref, deprecate | **all present**, plus a search filter |
| Jobs & fleet | queue-state counts, worker count, expired leases, job list, retry, cancel | **all present**, plus a state filter and oldest-lease age |
| Kill switch | global arm/disarm, two-person disarm approver, per-tenant arm, current-state table | **all present**, plus armed counts in the section headers |
| Cross-tenant | five aggregates as tabs, chart, tabular fallback, suppression, degraded | **all present**, plus the value printed beside each bar |
| Audit | chain verdict, entry table | **all present**, plus seq/actor/action/result filters |
| Compliance | erasure with typed target, request table | **all present** |

Nothing was removed. Three things were added that the visual-regression baseline now protects.

---

## 4. What the review explicitly did NOT relax

- Reason required on every destructive action, explicit confirmation checkbox, typed target on the
  irreversible one, global control heavier than per-tenant. Re-verified after the restyle (14.8).
- The command palette navigates and never performs. Verified live: selecting "Arm the global kill
  switch" opened the confirmation with **empty** reason and **unchecked** confirmation.
- Nothing on the danger path is pre-filled, including after a failed attempt.

---

## 5. Follow-ups (accepted with these open)

1. **The visual baseline is structural, not pixel-based.** It catches moved primitives, dropped
   columns, renamed states and vocabulary changes; it cannot catch a two-pixel misalignment or a
   contrast regression inside one token. The acceptance matrix (14.7) is what covers those, and it is
   a human looking at real renderings. This limitation is written at the top of the script so nobody
   mistakes the gate for more than it is.
2. **The dev-mode CSP needed `'unsafe-eval'`** for React Refresh; without it the console served
   correct HTML that never hydrated — every form inert, and invisible to `next build` and `tsc`. The
   production CSP is unchanged and the asymmetry is asserted. Worth revisiting if Next's dev runtime
   stops requiring it.

---

## 6. How to keep this review true

```bash
npm run scan:tokens   # no colour/spacing/type/radius literal outside the token layers
npm test              # 35 assertions incl. hazard reservation, palette-navigates-only, no-optimistic-UI
npm run baseline      # structural visual regression across 11 routes
```

If a change makes one of these red, the answer is to look at the rendering — not to update the
baseline. A baseline updated without looking is a baseline that records the regression; that happened
once during this very review, which is why the tool says so in its own failure message.
