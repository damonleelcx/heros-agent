# The console design language

> **Scope.** This directory is the **shared token system** both consoles draw from — the customer
> console (P9) and the operator console (P8) — plus the documented language layered on top of it.
> It lives here, beside the consoles, rather than inside either one, so neither can fork it quietly
> (P8 PRD §14 Q8; design.md Decision 12).
>
> **Owner.** Jointly owned. A change to `tokens.css` is a change to both surfaces.

---

## 0. Why a language, and not just a floor

The operator console already had a floor: keyboard reachability, four distinct states, WCAG 2.1 AA
contrast, scoped headers, no hardcoded number. Every one of those is a **lower bound**, and a
specification made only of lower bounds produces exactly what it asks for — a compliant surface nobody
chooses to open.

On this platform that is a **security** outcome, not a taste one. P8 exists to retire the ad-hoc
production shell. An operator one page into an incident uses whatever answers fastest; a console
slower to read than a `psql` prompt does not retire the prompt, it gets maintained beside it. So the
properties that make the console *preferred* are specified here with the same seriousness as the
gates (P8 design.md Decision 13).

**The rule that keeps that safe:** *delight on the read path, friction on the write path*
(Decision 14). Everything below applies to reading — finding, comparing, linking, seeing. Nothing
below may shorten the deliberate steps to a destructive effect.

---

## 1. Type — an editorial hierarchy, five roles

| Role | Token | Use |
|---|---|---|
| **Display** | `--text-2xl` / `--text-3xl`, `--tracking-tight`, `--line-tight` | One per page. The page's subject. |
| **Title** | `--text-lg` / `--text-xl`, `--weight-semibold` | A section's subject. |
| **Eyebrow** | `--text-2xs`, `--tracking-eyebrow`, uppercase | A label above a value or a group. Never a sentence. |
| **Body** | `--text-base` / `--text-sm`, `--line-normal` | Prose, table cells, form copy. |
| **Caption** | `--text-xs`, `--text-muted` | Provenance, as-of times, hints. |

**Identifiers are always mono** (`--font-mono`): tenant ids, run ids, price refs, hashes, capability
names. An operator compares them character by character, and a proportional font renders `l` and `1`
identically.

**Prose is capped at `--measure-prose`.** A 200-character line is unreadable at any contrast ratio.

---

## 2. Rhythm and grid

One 4px base scale (`--space-1` … `--space-10`). **There is no value between the steps** — if a gap
looks wrong, the answer is the adjacent step, never `13px`.

- Page content is capped at `--measure-content`, gutter `--gutter`.
- Vertical rhythm between sections is `--stack`; within a section it is `--stack-tight`.
- Both change with density (§5), so a page written in tokens becomes compact for free.

---

## 3. Elevation — three steps and their meaning

| Step | Token | Meaning |
|---|---|---|
| Flat | `--elev-0` | Content in the page's own plane. |
| Raised | `--elev-1` / `--elev-2` | A panel of grouped content: a section, a stat, a table. |
| Overlay | `--elev-3` | Sits **above** the page and takes focus: the command palette, a drawer. |

Depth is a hierarchy signal. A view that uses overlay elevation for something that does not take
focus has told the operator a lie about where their attention belongs.

---

## 4. Colour — and the one rule that matters

This file defines the token **vocabulary**: which names exist, what each is for, and the contract that
every status token carries an ink, a surface and a border meeting AA in both themes. A console's own
layer supplies its **values**.

That split is what "documented extension, never a fork" (15.1) means precisely: a fork would introduce
a second set of *names*, so a primitive would have to know which console it was rendering on. Re-valuing
a shared name does not — `.stat--alarm` is one rule in one stylesheet either way.

The operator console exercises that fully. `web/admin-console/src/app/tokens.operator.css` renders the
whole vocabulary for a near-black instrument ground: the neutral ramp, the status palette, the accent
and chrome, sharper radii, and three type faces. The customer console renders the same names for paper.
Neither can quietly acquire a name the other lacks — `tests/craft.test.mjs` asserts the light and dark
mapping blocks in each layer name the same set.

**Surface identity in particular is never shared**: the two consoles must be distinguishable at a
glance and that is a safety requirement (FR23), asserted per theme as a minimum hue separation rather
than assumed.

> ### 🔴 Anything painted on a console's chrome band takes the band's tokens
>
> The operator band is dark in **both** themes while the page inverts, so a page ink is
> contrast-checked against the wrong background when it lands there. This was a real defect: `.quiet`
> used `--text-muted`, and the light theme rendered "Sign out" as dark slate on deep teal — present in
> the accessibility tree, invisible on screen, and impossible to notice while working in dark. Inside
> the band, colour comes from `--chrome-*`, and a test enforces it.

> ### 🔴 The hazard palette is reserved for hazard (FR31)
>
> `--warn` and `--danger` (and their surfaces/borders) may appear **only** on:
> a destructive control · an armed halt · an active impersonation · an alarming state.
>
> They may **never** be used for emphasis, branding, an accent, or decoration.

This is the requirement most easily lost, and losing it is expensive. Danger is legible because it is
**rare**: on a view with one destructive control, that control must be the only element carrying the
hazard palette. A console whose chrome is amber has spent the colour that its kill switch needs — the
operator's eye stops distinguishing "this halts one tenant" from "this halts the fleet", which is
precisely the confusion FR24's friction is built to prevent.

Status is never conveyed by colour alone: every status carries a **word**, and shape or position
where possible.

---

## 5. Density is an operator's choice, not a designer's

`comfortable` (default) and `compact`, set as `data-density` on the root element from a **server-read
preference**, so the first paint is already correct and nothing reflows after hydration.

> **Compact tightens the rhythm, never the information.** No row, column, control, disclosure or
> caption may exist at one density and not the other. A density that hides a column is a second,
> undocumented information architecture.

---

## 6. Motion budget (FR35)

| Token | Duration | Use |
|---|---|---|
| `--motion-fast` | 120ms | Hover, focus, small state marks. |
| `--motion-base` | 180ms | Overlay entry, disclosure, value-change highlight. |
| `--motion-slow` | 240ms | The longest permitted transition. Nothing exceeds it. |
| `--motion-alarm` | 2000ms | 🔴 **Hazard only.** The slow breath on an armed halt or an active impersonation. |

`--motion-alarm` is the one duration outside the interaction budget, and the distinction is what makes
it legal. Everything above it times a **transition** — something a reader is waiting through, which is
why none of them is long enough to be waited on. `--motion-alarm` times a **state marker**, which is on
no interaction path at all, so its length costs nobody anything. It is deliberately slow: a fast blink
reads as a rendering fault and gets ignored; a two-second breath reads as a thing that is *on*.

It carries the same reservation the hazard palette does (§8b), and for the same reason —
`tests/craft.test.mjs` fails the build if a rule uses this duration without also carrying a hazard
treatment. A decorative pulse would spend the attention the armed kill switch depends on.

Three rules:

1. **Motion means something.** Continuity (where a surface came from), or a state change (which value
   changed, what arrived). Never decoration.
2. **Motion is never on the action path.** No confirmation, navigation or command waits for a
   transition. If a control cannot be operated mid-animation, that is a defect.
3. **`prefers-reduced-motion` loses nothing.** Every duration collapses to `0ms` and every state a
   transition would have communicated is also communicated statically.

---

## 7. The primitive set is closed

Every view composes from these, and only these:

| Primitive | Responsibility |
|---|---|
| `PageFrame` | Page title, lede, and the one content measure. |
| `Section` | A titled panel of related content, with an aside for provenance. |
| `DataTable` | Scoped headers, a caption, numeric columns, an empty body that never collapses. |
| `Stat` | One labelled figure, with its unit and its as-of time. |
| `Timeline` | Ordered events with their time and actor. |
| `Drawer` | Focus-taking overlay for a subject's detail. |
| `ConfirmSheet` | The dangerous-action confirmation. Reason, typed target, scope. |
| `Receipt` | What happened, to whom, why, and the audit entry it wrote. |
| `StateBlock` | The nine answers (below). One shape; a tenth cannot arrive with its own layout. |
| `AlarmBanner` | 🔴 *Operator console only.* A fleet-wide condition stated across the top of the view. |

> **Adding a primitive is a deliberate, reviewed extension of this document — never a page's side
> effect.** If a view needs a shape that is not here, the change is to this list first.

**On `AlarmBanner` (added by the operator restyle).** FR34 requires an armed kill switch to be apparent
*without interaction*, and a `Stat` inside a `Section` is not that: it is legible only once the reader
has decided which panel to read, and an operator one page into an incident has not. The banner is a
distinct shape rather than a loud `Stat` because it answers a different question — not *what is this
figure* but *what does everything below this mean*. It carries four independent cues (hazard tint, heavy
rule, the condition in words at display scale, and the `--motion-alarm` indicator), so removing colour
entirely still leaves the sentence.

### The state family: nine answers, nine remedies

Every view must be able to render all nine. They are separate components, not one with a variant prop,
because the failure they exist to prevent is precisely the collapse of two into one.

| State | Means | Remedy | Mark | Family |
|---|---|---|---|---|
| Loading | On its way | wait | `···` | wait |
| Empty | Genuinely nothing yet | nothing is wrong | `∅` | nothing is wrong |
| Not mounted | Not installed in this deployment | nothing is wrong | `⊘` | nothing is wrong |
| Gated | Outside this plan | ✋ an entitlement, **not** an error | `▲` | nothing is wrong |
| Not found | The identifier does not resolve | check what you pasted | `?` | you can act |
| Denied | Your role does not hold this | ask whoever does (named) | `⊗` | you can act |
| Degraded | The platform could not be reached | check the network, retry | `⚠` | something is wrong |
| Unusable | It answered something we cannot read | a version mismatch; retrying will not help | `≠` | something is wrong |
| Unknown | A command's response was lost | the audit log, never a retry | `⁇` | something is wrong |

🔴 **They must be distinguishable before the copy is read.** Tint alone never achieved that — nine
states cannot have nine legible tints, and an operator mid-incident does not read two sentences to
learn which of nine answers they are looking at. So two signals vary independently: **tint and rule
weight carry the family**, and **a mono glyph carries the member**. The glyph is `aria-hidden` and
every title states the answer in words, so a reader who cannot see it loses nothing.

Only the *something is wrong* family may use the hazard palette. Gated in particular is an
entitlement boundary, and rendering one in red teaches operators the console cries wolf — which costs
exactly the states that are real failures.

### The confirmation has three weights, not two

`ConfirmSheet` renders at a weight derived from the **server's** blast-radius classification, never
from a page's opinion:

| Modifier | When | Treatment |
|---|---|---|
| `action--caution` | reversible, single subject | amber tint, amber rule, amber submit |
| `action--danger` | irreversible (typed target) | red tint, red rule, red submit |
| `action--global` | fleet-wide | red, heavy rule on every edge, raised |

🔴 The amber tier exists because the *rendering* disproved FR24 while the markup satisfied it. With two
tiers, "halt this tenant" and "halt the fleet" both came out as red panels with red submit buttons
differing only in the width of a rule on the left edge — a difference in the stylesheet, not one an
operator scanning a page perceives. Two blast radii sharing one colour is precisely the confusion FR24
names. This is still hazard reservation and not a dilution of it: amber has always meant "needs
attention" and red "alarming", and a reversible single-tenant halt is honestly the first of those.

---

## 8. Numbers are rendered for comparison (FR30)

- **Tabular figures** (`font-variant-numeric: tabular-nums`) on every numeral in a table or stat.
- **Digit-aligned** — numeric columns are right-aligned so magnitude is visible without reading.
- **Unit and scale stated once**, in the column header or the stat's label, never repeated per cell.
- **One quantity, one scale, one precision per view.**

The console performs no arithmetic on platform values: every number it renders came from the server,
so the screen cannot disagree with the system of record.

---

## 8b. 🔴 The two reservations — the same rule pointed at different risks

Both consoles reserve a visual treatment, and it is worth stating them side by side, because they are
one idea:

| Reservation | Owned by | Reserved for | What it fails at |
|---|---|---|---|
| **Hazard palette** (`--warn`, `--danger`) | operator console (P8 FR31) | destructive scope, armed halt, active impersonation, alarming state | Spent on emphasis, a real hazard stops standing out. The named failure is an operator who misses an armed **global** kill switch because three other things on the page are also red. |
| **Confidence treatment** (`.confident`) | customer console (P9 FR29) | a value the server did **not** qualify | Applied to a `tie`, a `provisional` result or a `disqualified` variant, the UI has invented a ranking the server refused to make — in CSS, where no test in the eval harness can see it. |

**Why they are the same rule.** A signal is legible because it is *rare*, and it is credible because it
is *earned*. Danger is meaningful only when most things are not dangerous; confidence is meaningful
only when a settled result looks different from an unsettled one. Both are destroyed the same way —
by a well-meant pass that reaches for the treatment because it is available and the element looks
important.

Both are machine-checked rather than reviewed. Neither is a style preference: on this product, a tie
rendered as a win is a **correctness** defect, and a hazard hue spent on decoration is a **safety** one.

### And the reservation survives scale (R16 / FR36 · FR38)

The stat primitive renders a quantity at display scale. That does **not** exempt it:

- a qualified value at `--text-stat` carries its qualifier **beside it, at that scale** — never shrunk
  to a footnote, never deferred to a tooltip;
- 🔴 **size reads as certainty**, so emphasis on a qualified figure is a *larger* defect at 2.25rem
  than at 0.875rem, not a smaller one;
- a figure rendered large is not thereby licensed to borrow the hazard palette.

---

## 8c. What the 2026 trend review changed, and what it did not

The full adopt / adapt / reject ledger is [`trend-ledger.md`](trend-ledger.md) — sixteen trends, each
with a verdict and a reason. In summary:

**Adopted** — bold typography *pointed at measured values rather than at headings* (R16); an explicit
persisted **theme** rather than a hardcoded dark default (R17); a **payload ceiling** (R18).

**Rejected, and worth naming** — vibrant/dopamine palettes and maximalism, because they destroy the
reservations above; 🔴 **agentic task execution** and 🔴 **forms that pre-fill from history**, because
this platform audits-then-effects and a pre-filled confirmation is a confirmation that confirms
nothing.

**Not changed** — the palettes, the token set's shape, and every existing feature. The work is a
**hierarchy** change: 🚫 no section may be dropped, collapsed by default, or hidden behind a disclosure
because it makes a page look cleaner.

---

## 9. Enforcement

Documentation alone has a demonstrated failure rate. These rules are machine-checked:

| Guard | What it fails on |
|---|---|
| `npm run scan:tokens` | Any colour, spacing, type-size or radius **literal** outside the token layers. |
| `npm run scan:bundle` | Credential material or a priced literal in the shipped client bundle. |
| `npm test` | Density parity, hazard reservation, tabular numerals, motion budget, palette scope, receipt/audit linkage. |
| `npm test` (customer console) | 🔴 The **confidence reservation** — one case per qualifier, asserting a qualified value never carries the settled-result emphasis and renders its qualifier beside it. Plus: the stat scale outranks every frame it sits in; contrast computed for **both** themes; the three theme mapping blocks carry identical token sets; every capability in the command path is also reachable by navigation. |
| `npm run scan:bundle` (payload) | A shipped bundle over its stated byte ceiling, or a decorative 3D/animation runtime. Refuses to measure a tree a dev server has written into, rather than reporting a number that describes the wrong bundle. |

If a rule here cannot be expressed as a guard, say so in the rule — do not rely on the reader
remembering it.
