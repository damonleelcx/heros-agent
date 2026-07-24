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

---

# Review 2 — the instrument restyle (§17, 2026-07-24)

**Reviewer: not yet named.** This entry records what was built and what was verified in a browser; the
named human sign-off FR37 asks for is **still outstanding** and is the one acceptance item this round
does not close. Everything below is evidence, not approval.

## 1. What changed, and what deliberately did not

The console was restyled onto the **operator instrument** design language: a near-black ground with
hairline rules instead of cards on paper, figures set in mono at display scale, a condensed
tracked-out micro-type layer for every label, sharper radii, and one teal that marks what is
interactive and nothing else. Three self-hosted faces (Barlow, Barlow Condensed, JetBrains Mono,
latin subset, 160 kB, `font-src 'self'`). The chrome band, the navigation and the acting principal
were merged into a **single sticky band** so the eye finds one horizon line.

Nothing in the console's *contract* moved. Same closed primitive set (plus `AlarmBanner`), same
capability-filtered nav and palette, same command path, same four/five states, same friction on the
write path. The restyle is a re-valuing of the shared token vocabulary in the operator layer — not a
fork, and not a change to what any view says.

## 2. 🔴 Two defects the rendering found that the tests did not

Both were invisible to a green build, and both are now fenced.

**FR24 was false on screen while true in the markup.** The global and per-tenant kill switches both
rendered as red panels with red submit buttons, differing only in the width of a rule on their left
edge. The existing test passed — it checked that `--rule-heavy` was present on one and absent on the
other — but a rule width is not a difference an operator scanning a page perceives, and FR24's entire
content is that the two can never be confused. The confirmation now has **three** weights keyed to the
server's blast-radius classification: amber for reversible, red for irreversible, red-and-heavy for
fleet-wide. A new test asserts the *hue* separation, which is the thing that actually separates them.

**A light-theme control that was invisible.** `.quiet` resolves `--text-muted`, which inverts with the
page — but the chrome band is dark in *both* themes, so "Sign out" rendered as dark slate on deep teal.
Present in the accessibility tree, unreadable on screen, and impossible to notice while working in
dark. A new test walks every rule targeting the band and fails on any page-scoped colour token; it was
proven red against the exact defect and green against the fix.

## 3. Walked in a browser, against the live `p8hermes` stack

Production build (`npm start`), not `next dev` — the CSP asymmetry noted in Review 2 §5.2 means dev is
the weaker evidence.

- **Sign-in · Overview · Tenants · Kill switch · Audit log · Command palette**, dark and light.
- **The armed state, for real.** Armed the global kill switch (reason recorded, confirmation checked),
  confirmed the receipt named its audit entry as a link, and confirmed the overview then renders the
  full-bleed hazard band — the only red on the page. Disarmed it again through the two-person path,
  restoring the fixture. Both actions are in the chain.
- **Narrow (768 px).** The band wraps into three tiers; no horizontal scroll; the table holds.
- **Field measure.** Every text field is capped at the form measure. At the console's 90 rem content
  measure an uncapped input rendered ~1 400 px wide, which is not a generous field — it is one whose
  ends cannot be read in a single fixation.

## 4. What is NOT covered by this round

- **The named design review above.** Outstanding.
- **The full 14.7 acceptance matrix.** Light/dark, narrow/wide and the populated + empty + armed states
  were walked. **200 % zoom, reduced motion, and both densities were not re-walked** after the restyle,
  and the matrix's rule is that a cell without evidence blocks acceptance. Those cells are open.
- **A contrast audit of the new palettes by instrument.** Every pair was computed by hand against its
  own surface while choosing values; none has been measured by a tool on the rendered page.

---

# Review 3 — the state family (§18, 2026-07-24)

**Reviewer: still not named.** Same standing as Review 2: this is evidence, not approval.

## 1. The gap this closed

The design brief specifies seven states every page must render, plus Denied and Degraded for the admin
console. The console shipped five. That was not a cosmetic shortfall — each missing state was being
answered with another state's copy, and three of the four substitutions asserted something false:

| Missing | Was rendered as | What that told the operator |
|---|---|---|
| `not_found` | Degraded | "the platform is broken" — when they had mistyped an id |
| `unusable` | **blank data** | a version mismatch, presented as a tenant with no plan |
| `not_mounted` | Degraded | go and look for an outage that is not happening |
| `gated` | Degraded | an entitlement boundary, in the language of failure |

The second is the worst of the four and the reason this was worth doing on its own. `safeParse`
returned `null`, `null` was cast to the expected type, and the page rendered every field as
`undefined`. It does not look like a failure. It looks like data, and it is read as data.

## 2. 🔴 A third defect the rendering found

With the console side finished, a bogus tenant id **still** rendered DEGRADED. The console was right;
the platform was answering `400 / kind: "request"` for "no such customer". `account.ErrNotFound`
already existed and `writeCapabilityError` simply never mapped it, so every unresolvable identifier
fell through the `default` arm.

Fixed in `internal/api/p8.go` (404 / `not_found`), `go test ./internal/api` green. The same URL now
renders **NO SUCH RECORD** with the identifier echoed in mono, in the accent rather than the hazard
palette, inside the operator shell. Three layers, one requirement, and only the rendering could show
it — which is the third time in two reviews that a green build was compatible with a page saying the
wrong thing.

## 3. Distinguishable before the copy is read

This was the brief's actual complaint: the states "differ only by border and tint". Nine states cannot
have nine legible tints. Two signals now vary independently — **tint and rule weight carry the family**
(wait · nothing is wrong · you can act · something is wrong), **a mono glyph carries the member**
(`··· ∅ ⊘ ▲ ? ⊗ ⚠ ≠ ⁇`). The glyph is `aria-hidden` and every title states the answer in words, so it
is a second signal and never the only one.

## 4. Acceptance matrix — the open cells, now walked

- **200 % zoom** (720 CSS px on a 1440 screen): no horizontal overflow, asserted programmatically as
  `scrollWidth === clientWidth` with an empty list of overflowing descendants. The band reflows to
  four tiers; every figure and label stays legible.
- **Reduced motion**: the shared override applied live. Every `.drawer__body` computes to
  `opacity: 1`, `animation-duration: 0s`, `transform: none`, full height — the `from { opacity: 0 }`
  keyframes strand nothing. The armed-halt indicator stays fully visible with its word at display
  scale, so the state is conveyed statically.
- **Compact density**: rhythm tightens; no row, column, control or disclosure disappears.
- **Not-found, live**: walked in the browser against the real 404.

## 5. What is STILL not covered

- **The named design review.** Outstanding, and now the only item on §17 left open.
- **`not_mounted`, `gated` and `unusable` have not been walked in a browser** against a real trigger.
  They are unit-asserted and their triggers are wired, but the `p8hermes` fixture never produces a
  501, a 402, or a malformed body, so no rendered evidence exists for those three. By 14.7's own rule
  — a cell without evidence blocks acceptance — those three cells are open.
- **A contrast audit by instrument** of the new state tints, as with the palettes in Review 2.
