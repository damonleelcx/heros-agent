# Design brief — Heros consoles

Two applications to design. **29 pages.** Every page below lists what it contains and what it does.

- **Web console** (customer-facing) — 18 pages
- **Admin console** (internal operators) — 11 pages

---

# Part 1 — The brief

## What this product is

Heros finds every place your code calls an AI model, changes one of them at a time as a reviewable
diff, runs it in a sandbox, and scores it. **Its distinguishing feature is that it tells you when it
does not know.** When two results are statistically tied, it says *no winner* instead of picking one.

So the design has an unusually good story to tell: **this is a scientific instrument, not a
dashboard.** Instruments are beautiful because they are precise — an oscilloscope, an observatory
control room, a spectrometer readout. Confidence you can see. Measurement you can trust. That is a far
richer direction than "enterprise SaaS," and it is what the product actually is.

Two moods, one family:

- **Web console** — a customer's instrument. Can be genuinely gorgeous: depth, atmosphere, considered
  motion, expressive data visualisation, a public page that makes someone want in.
- **Admin console** — a control room at 3am. Fewer decorations, more *presence*. Should feel like
  something serious is under your hands.

## Where you have total freedom

Essentially everything visual. To be explicit, because the constraint list below is short but
load-bearing and I do not want it read as "keep it plain":

Layout · composition · grid · every colour value · typography and type pairing · the whole scale ·
depth, shadow, glow, glass, grain, gradient, texture · illustration · iconography · the entire chart
and graph aesthetic · motion and transitions · empty-state art · the public page, completely · loading
choreography · hover and focus expression · the shape of every card, chip, table and control · light
and dark as two designed experiences rather than one inverted.

**The current UI is a competent floor, not a design.** Treat it as a wireframe.

## Three things that must survive

Not style rules — these are what the product *means*. Each has a specific failure it prevents.

### 1. A tie must never look like a win

The scoreboard's top row is frequently a **statistical tie**. The server tells us so, and the design
must not overrule it. Ten flags mean "do not treat this as settled":

`tie` · `provisional` · `disqualified` · `low-confidence` · `weak-labeled` · `uncalibrated` ·
`withheld` · `candidate` · `unverified` · `gated`

A value carrying any of them must not get the treatment a settled result gets — no accent, no glow, no
lift, no entrance animation, no heavier weight. **Make the settled result gorgeous.** Just make sure a
qualified one is visibly quieter, and that its caveat sits *beside* it rather than in a tooltip.

*This is a creative opportunity, not a restriction: you get to design what "earned confidence" looks
like, and it will be one of the best things in the product.*

### 2. Alarm colours mean alarm

Red and amber are reserved for danger, halts, and active impersonation. If they also mean "primary
button," then the button that halts the entire fleet stops standing out. Pick any accent you like —
just not from the alarm family. (The admin console currently uses teal for exactly this reason.)

### 3. Status needs a word, not just a colour

Every state carries a distinct colour **and** a distinct word — for colourblind readers, greyscale,
and screenshots. Style it however you want; keep the word.

**Also:** AA contrast in both themes, keyboard reachable with visible focus, charts need a text
alternative and a data-table fallback. All normal, none of it limits the aesthetic.

---

# Part 2 — Web console (customer)

18 pages. Signed-in tenants inspect their AI workflows and the evidence behind changes to them.

## Public & entry

### 1. `/` — Home (public marketing page)
**The most important page to get right, and the freest.** No sign-in, no data, no constraints beyond
"only claim what has shipped." A prospect's first impression.

| | |
|---|---|
| **Contains** | Hero (headline, subhead, two CTAs) · "the difference" cards · how-it-works steps · claim cards, each pairing a benefit with its honest boundary · plan names (Free / Team / Business / Enterprise, never priced) · footer |
| **Does** | Explains the product · routes to sign-in · states limits beside benefits |
| **Note** | Currently leaves half a 1440px viewport empty. Needs the most design attention of any page. |

### 2. `/signin` — Sign in
| | |
|---|---|
| **Contains** | Single credential field · submit · explanatory note ("the browser never receives an API key") · error state |
| **Does** | Exchanges a credential for a server-held session |

### 3. `/app` — Overview (signed-in home)
| | |
|---|---|
| **Contains** | "Opened in this session" — recently visited subjects · "Start somewhere" — entry points · empty state when nothing visited |
| **Does** | Resumes where you left off; onboards when there is nothing to resume |

## Selection pages (4 — same pattern)

### 4–7. `/app/workflows` · `/app/runs` · `/app/variants` · `/app/transforms`
| | |
|---|---|
| **Contains** | Subject picker: list of known subjects + direct identifier entry as an accelerator · empty state |
| **Does** | Picks a workflow / run / variant / transform. 🔴 Never guesses a default subject — a confidently-populated page for the wrong subject is worse than an empty one. |

## Workflow pages

### 8. `/app/workflows/[id]` — Workflow home
| | |
|---|---|
| **Contains** | "Where to go" — links to graph, board, proposals |
| **Does** | Hub for one workflow |

### 9. `/app/workflows/[id]/graph` — Pattern-classified graph ⭐
**The flagship data view.** A map of every AI call site in the customer's repository.

| | |
|---|---|
| **Contains** | 3 headline stats (nodes / edges / LLM fallback calls) · **the graph itself** — node boxes, edges, region rectangles · legend (5 entries) · text alternative + data-table fallback · pattern cards per region · "unclassified" cards explaining *why* nothing matched · diagnostics list |
| **Does** | Shows workflow structure · shows which regions the classifier could name and which it could not · distinguishes rule-matched from model-guessed labels |
| **Design notes** | Node position is computed and fixed (reproducibility) — but node, edge and region *appearance* is entirely open. Edges must differ by **shape as well as colour** (dash + arrowhead). The container scrolls; a big graph is never squashed. Genuinely fun to design. |

### 10. `/app/workflows/[id]/board` — Eval board ⭐
**The densest and most important screen in the product.** Where variants get compared.

| | |
|---|---|
| **Contains** | Weight-profile selector · banners (all-tie / partial / notes) · **leaderboard** — rank, variant, score ± confidence interval with a visual interval bar, gate result, state flags · expandable per-row breakdown (per-metric contributions, judge agreement, penalties, gate reasons) · **Pareto scatter** — quality vs cost, marker size = latency, shape = frontier membership · legend · data-table fallback · **coverage meters** per dimension · residual table · **spend table** · disqualified variants in their own section |
| **Does** | Ranks variants with honest uncertainty · re-ranks by weight profile at no cost · explains every score · shows the cost/quality frontier · shows what the eval set never exercised · shows what measurement cost |
| **Design notes** | Where "a tie must never look like a win" bites hardest. Also: virtualises above 60 rows, keyboard row navigation with wrap-around, disqualified variants are *excluded* rather than ranked last. **The single highest-value page to redesign.** |

### 11. `/app/workflows/[id]/proposals` — Proposal queue
| | |
|---|---|
| **Contains** | "Recommended" section · **"Withheld" section (visually separate)** · per-proposal card: rationale, measured delta, diff · "what was measured" summary |
| **Does** | Lists changes the platform suggests · 🔴 keeps unverified proposals visibly apart from verified ones |

### 12. `/app/workflows/[id]/proposals/[proposalId]` — Proposal detail
| | |
|---|---|
| **Contains** | "Why this was proposed" (rationale) · "The verified delta" (measurement + interval) · "The change" (full diff) · "Decision" (open a pull request) |
| **Does** | Full review of one proposal → opens a PR a human merges. The platform never merges. |

## Run pages

### 13. `/app/runs/[id]` — Run detail
| | |
|---|---|
| **Contains** | Status + config hash + seed + revision chips · halt banner when halted · per-node table (node, attempt, status, input, output, idempotency key) · per-node error rows · **watch toggle** (live polling) |
| **Does** | Inspects one run node-by-node · watches it live · explains a halt |

### 14. `/app/runs/[id]/live` — Live monitor ⭐
| | |
|---|---|
| **Contains** | Run id + status · **live connection line** (streaming / polling / closed) · per-node metrics table (latency, cost, prompt tokens, completion tokens) · per-row state chip **plus** a row marker · halt banner · status-dependent empty state |
| **Does** | Streams metrics live over SSE, falls back to polling only if the stream never worked |
| **Design notes** | The one genuinely *live* screen. Updates must land in place — no layout shift, no row moving under the pointer. Real opportunity for beautiful, restrained motion. |

### 15. `/app/transforms/[hash]/[revision]` — Generated diff
| | |
|---|---|
| **Contains** | Status + verification-strength + branch chips · **syntax-highlighted diff** · diff hash footer · build log on rejection · "no changes — this is the baseline" state |
| **Does** | Reviews the code change a variant produced before it runs |

### 16. `/app/variants/[id]/scorecard` — Attribution scorecard
| | |
|---|---|
| **Contains** | 4 headline stats (task success, failing cases, cost, latency) · per-node failure attribution table · failure clusters · **"What the analyst believes" — hypotheses** · **"What was actually re-run" — measurements with intervals** · uncalibrated-analyst warning |
| **Does** | Explains *why* a variant scored what it scored · 🔴 keeps hypotheses and measurements visually distinct |

## Action & account

### 17. `/app/configure` — Configure a variant
**The only page where a customer writes anything.**

| | |
|---|---|
| **Contains** | 4 override-dimension chips (model / prompt / skills / context) · large spec editor (monospace) · variant id, label, seed fields · three buttons: *Reset to example*, *Validate only*, *Submit & run* · validation result panel · submission result panel |
| **Does** | Overrides one AI call site · validates without running · submits and runs sandboxed · carries the result into the diff and run views |

### 18. `/app/account` — Plan & spend
| | |
|---|---|
| **Contains** | Plan name + period chips · capability table (what this plan includes, what unlocks the rest) · entitlement table · **spend stat** with server-supplied unit · metered-usage table with allowances |
| **Does** | Shows the plan, what it unlocks, and what the period cost · names the upgrade plan for anything gated |

---

# Part 3 — Admin console (internal)

11 pages. Platform operators run the fleet. Highest blast radius in the product — one action here
crosses tenant boundaries.

**Every page carries:** a distinct dark chrome band · `Acting as <admin>` + role · command palette
(⌘K) · density toggle · theme toggle · impersonation banner when one is active.

### 1. `/signin` — Operator sign-in
| | |
|---|---|
| **Contains** | SSO subject field · MFA factor selector · submit |
| **Does** | SSO + MFA. Separate identity system from customers entirely. |

### 2. `/` — Overview (operating picture) ⭐
**Must be readable across a room, without interaction.**

| | |
|---|---|
| **Contains** | **As-of timestamp** · autonomous-merge state (fleet-wide armed/disarmed + per-tenant halts) · worker-fleet stats (ready / running / dead / expired leases) · tenant stats (on platform / suspended / merges halted) · active impersonations · unresolved anomalies |
| **Does** | Answers "is anything wrong right now" at a glance · every figure states when it was current and announces staleness |
| **Design notes** | The admin console's hero. An armed kill switch must be **unmissable**. |

### 3. `/tenants` — Tenant list
| | |
|---|---|
| **Contains** | Search by tenant or plan · tenant table (tenant, plan, status, autonomous merges, config version) |
| **Does** | Finds a tenant |

### 4. `/tenants/[id]` — Tenant detail
| | |
|---|---|
| **Contains** | State stats · quota-override table · **4 action forms**: suspend / reactivate · set quota override · override plan · start impersonation (read-only) |
| **Does** | Full tenant lifecycle. Suspending halts that tenant's autonomous merges. |

### 5. `/billing` — Billing oversight
| | |
|---|---|
| **Contains** | Tenant chooser · invoices table · reconciliation table · gainshare-evidence table · credit/refund action form |
| **Does** | Reviews invoices and dunning · issues credits and refunds as **additive corrections**, never destructive edits |

### 6. `/registry` — Model registry
| | |
|---|---|
| **Contains** | Model table · "add a model" form · deprecate / repoint-price-reference actions · drawer for detail |
| **Does** | Administers models and their price references. Changes never rewrite already-closed billing periods. |

### 7. `/fleet` — Jobs & fleet
| | |
|---|---|
| **Contains** | Worker-fleet stat row · jobs table · retry / cancel action forms · job drawer |
| **Does** | Views, retries and cancels jobs. Cancel requires a reason and is audited. |

### 8. `/killswitch` — Kill switch ⭐
**The highest-stakes control in the product.**

| | |
|---|---|
| **Contains** | Global state pill (armed / disarmed) · **"Arm the GLOBAL kill switch"** — reason field + confirmation checkbox + *Halt the entire fleet* · **"Arm per-tenant kill switch"** — tenant id + reason + confirm · disarm forms · current-state table |
| **Does** | Halts autonomous merges immediately, fleet-wide or for one tenant, with no deploy |
| **Design notes** | 🔴 The **global** control must be visually distinct from and *higher-friction* than the per-tenant one, so "halt this tenant" can never be mistaken for "halt everything." Reason fields open empty. This is where restraint becomes a feature. |

### 9. `/crosstenant` — Cross-tenant read models
| | |
|---|---|
| **Contains** | 5 tabs (usage / provider spend / revenue & operations / top consumers / anomalies) · aggregate chart · data-table fallback · privacy note |
| **Does** | Fleet-wide aggregates · every view is logged · aggregates over fewer than 3 tenants are suppressed to prevent re-identification |

### 10. `/audit` — Audit log
| | |
|---|---|
| **Contains** | **Chain-verification panel** (intact / broken, and where) · filters · entry table · entry drawer |
| **Does** | Append-only hash-chained record of every admin action and every autonomous merge. Nothing can be edited or deleted, by anyone. |

### 11. `/compliance` — Data deletion
| | |
|---|---|
| **Contains** | Erasure request table · "execute an erasure" form (subject + reason + second confirmation) · completion records |
| **Does** | GDPR data-subject erasure, with a verifiable completion record and an audit chain that stays intact |

---

# Part 4 — Component library

## Shared across both consoles

| Component | Role |
|---|---|
| **Page frame** | Eyebrow + one page title naming the subject + lede. Present before data loads. |
| **Section** | Titled panel. Optional right-side provenance (as-of, version, hash). |
| **Data table** | Column headers, caption, right-aligned numerics |
| **Stat** | Label + big value + optional unit + note. The number is the largest thing in its block. |
| **Chip / Pill** | Small labelled token — status, hash, count, plan |
| **Banner** | View-level caveat that changes what the whole page means |
| **Command palette** | ⌘K — jump to any surface or recently-viewed subject |
| **Chart + fallback** | Any visualisation, with text alternative and a data table |

## Web console only

| Component | Role |
|---|---|
| **Value** | A figure with its confidence treatment and qualifiers (rule 1 lives here) |
| **Status** | 10 statuses + a visible fallback for unknown ones |
| **Diff** | Syntax-coloured unified diff |
| **Subject picker** | Select a workflow / run / variant / transform |
| **Loading / Empty / Failure** | Failure has 6 variants (below) |

## Admin console only

| Component | Role |
|---|---|
| **Action form** | Reason + confirmation + submit. The write path. |
| **Freshness** | As-of time + staleness warning on live figures |
| **Timeline** | Chronological events (audit, lifecycle) |
| **Drawer** | Detail panel over a table row |
| **Num** | Number with unit and scale |
| **Impersonation banner** | Persistent while impersonating, with End always visible |
| **5 state blocks** | Loading · Empty · **Denied** · **Degraded** · Unknown |

## States every page must be able to render

Seven different answers, seven different remedies. **They must be distinguishable before the copy is
read** — the design opportunity here is real, since they currently differ only by border and tint.

| State | Means | Remedy |
|---|---|---|
| **Loading** | On its way | wait |
| **Empty** | Genuinely nothing yet | add something |
| **Not mounted** | Capability not installed here | nothing is wrong |
| **Not found** | Identifier doesn't resolve | check what you pasted |
| **Transport failure** | Couldn't reach the platform | check the network |
| **Unusable response** | Platform answered something we can't read | version mismatch |
| **Gated** | Outside this plan | upgrade — ✋ *not an error* |

Admin adds **Denied** (your role lacks this — names who has it) and **Degraded**.

---

# Part 5 — Delivery checklist

Each page needs designs for:

- **Light and dark** — two designed experiences, not one inverted
- **Narrow and wide** — ~768px and ~1440px+
- **Comfortable and compact** density — spacing changes, information never does
- **Its states** — at minimum populated, loading, empty, and one failure
- **Reduced motion** — every state the motion would have conveyed, conveyed statically

A design that covers only "wide, dark, comfortable, populated" will need redoing for the other cells,
so it is worth planning for them from the start.

**Priority order** — most impact first:

1. **`/` public home** — freest surface, weakest today, first impression
2. **Eval board** — densest screen; nail this and the customer console follows
3. **Admin overview** — must read across a room
4. **The seven states as a family** — the biggest visible win for the least work
5. **Graph** — most visually distinctive thing in the product
6. Everything else

---

### Reference

- Live values today: [`tokens.css`](tokens.css)
- Rules and their reasoning: [`README.md`](README.md)
- 2026 trend review, with what we adopted and rejected: [`trend-ledger.md`](trend-ledger.md)

Running locally: web console `http://127.0.0.1:4320` (credential `dev`) · admin console
`http://127.0.0.1:4310` (SSO subject `sso|superadmin`).
