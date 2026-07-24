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

The neutral ramp and status palette are shared. **Surface identity is not**: the operator console's
accent lives in its own layer, because the two consoles must be distinguishable at a glance and that
is a safety requirement (FR23).

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
| `StateBlock` | Loading · empty · denied · degraded · unknown. |

> **Adding a primitive is a deliberate, reviewed extension of this document — never a page's side
> effect.** If a view needs a shape that is not here, the change is to this list first.

---

## 8. Numbers are rendered for comparison (FR30)

- **Tabular figures** (`font-variant-numeric: tabular-nums`) on every numeral in a table or stat.
- **Digit-aligned** — numeric columns are right-aligned so magnitude is visible without reading.
- **Unit and scale stated once**, in the column header or the stat's label, never repeated per cell.
- **One quantity, one scale, one precision per view.**

The console performs no arithmetic on platform values: every number it renders came from the server,
so the screen cannot disagree with the system of record.

---

## 9. Enforcement

Documentation alone has a demonstrated failure rate. These rules are machine-checked:

| Guard | What it fails on |
|---|---|
| `npm run scan:tokens` | Any colour, spacing, type-size or radius **literal** outside the token layers. |
| `npm run scan:bundle` | Credential material or a priced literal in the shipped client bundle. |
| `npm test` | Density parity, hazard reservation, tabular numerals, motion budget, palette scope, receipt/audit linkage. |

If a rule here cannot be expressed as a guard, say so in the rule — do not rely on the reader
remembering it.
