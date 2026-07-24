# Trend ledger — what the 2026 web-design trends do and do not buy this product

> **Source.** [Figma, *Web Design Trends for 2026*](https://www.figma.com/resource-library/web-design-trends/),
> read 2026-07-24, cross-checked against the 2026 enterprise-dashboard literature where the two
> disagree (they disagree a lot, and the disagreement is the most useful thing in this document).
>
> **Why this file exists.** "The UI is garbage" is a real report and a useless specification. A trend
> list is the same problem from the other end: sixteen named aesthetics, most of them designed for a
> surface that sells something to a stranger in eight seconds, none of them scoped to a console an
> operator reads for four hours during an incident. Adopting them wholesale would make this product
> worse in a way that photographs well. So each trend gets a **verdict**, a **reason**, and — when
> adopted — the **concrete requirement** it turns into and the **check** that keeps it true.
>
> 🔴 **The governing constraint is unchanged and outranks everything below.** P8 Decision 14 —
> *delight on the read path, friction on the write path*. A trend that speeds up reading is a candidate.
> A trend that adds polish to a destructive command is rejected on sight, no matter how current it is.

---

## The one-paragraph conclusion

The trends worth taking are the ones about **hierarchy and legibility**: bold typography, dark mode,
disciplined motion, and the sustainability trend's real content (lean payloads, keyboard reach, screen
reader support). The trends to refuse are the ones about **decoration and novelty**: vibrant/dopamine
palettes, maximalism, neo-brutalism, collage, retrofuturism, neumorphism, experimental navigation,
gamification, 3D. They are refused for one reason repeated eleven times — **this product's entire
value proposition is that it tells you when it does not know**, and every one of those trends works by
making a surface feel more confident than its content warrants. A console that renders a tie with
dopamine-palette confidence has overturned P4's tie logic in CSS, where no test in the eval harness can
see it.

The defects this repository actually has are **not** a shortage of trend adoption. They are, measured
in the browser on 2026-07-24 against the real `nousresearch/hermes-agent` checkout:

| Measured defect | Evidence |
|---|---|
| **The numbers are the smallest text on the page.** `40 nodes` / `0 edges` render at `--text-xs` inside grey chips, while the section heading that says nothing renders at `--text-lg`. | Graph view, first screenful |
| **Section chrome costs more than section content.** Every `.section` spends a padded header + rule + padded body — ~100px of frame — to present three chips. | Graph view: 900px of scroll to convey three integers |
| **Every page is one column of full-width cards.** At 1600px the content column is one 1280px card holding three chips; the composition grid exists in the tokens and is used by nothing. | Overview, graph, sign-in |
| **A single credential field is 1440px wide.** No form measure exists, so an input for a 40-character token spans the viewport. | Sign-in |
| **Two paragraphs collide in the empty state** — no space between the state body and its follow-on note. | Overview, "Nothing opened yet" |

Every one of those is a **hierarchy** failure, which is exactly what the bold-typography and
information-density trends address — applied to data rather than to a hero. That is the work.

---

## Verdicts

### Adopted

Requirement numbering: the shared **R-number** is the rule; each console's PRD carries it under its own
FR sequence, because the two sequences were already independent before this ledger existed.

| # | Trend | What we take | Requirement it becomes | Check |
|---|---|---|---|---|
| 4 | **Bold typography** | Oversized, editorial type as the primary carrier of hierarchy — **pointed at measured values, not at headings**. A stat's number outranks its label; a section's frame never outranks its content. | **R16** — P9 `FR36` · P8 `FR38`. The stat primitive: value at display scale, label at caption scale, unit stated once. A view's largest type is a number the user came for. | both consoles' craft tests assert no section frame outranks the values it holds |
| 5 | **Dark mode** | The trend's actual content is **a toggle**, not a dark default. Both consoles are dark-only today and force `color-scheme: dark` while the shared tokens switch on `prefers-color-scheme` — a latent mismatch for a light-OS user. | **R17** — P9 `FR37` · P8 `FR39`. An explicit, persisted theme control (system / dark / light), server-read so the first paint is already correct. | design-system tests — every surface token pair meets AA in **both** resolved themes |
| 6 | **Motion design** | Micro-interaction and state-change continuity, inside the existing motion budget. Nothing new; the budget already exists and is already enforced. | Unchanged — the budget (`--motion-fast/base/slow`) and 🔴 **no motion between intent and command** stand as written. | existing duration-token scan |
| 13 | **Sustainable web design** | The half of it that is not marketing: lean payload, no third-party origin, keyboard reach, screen-reader support, high-contrast. This console already holds all of it — the trend adds a **payload budget** we did not have. | **R18** — P9 `FR38` · P8 `FR40`. A shipped-bundle weight budget, failing the build when exceeded. | `scripts/scan-bundle.mjs` gains a byte ceiling |

### Adapted — taken, but inverted from the article's intent

| # | Trend | The article's version | Ours, and why |
|---|---|---|---|
| 2 | **Experimental navigation** | Radial menus, hidden drawers, nonlinear journeys, for differentiation. | 🚫 Rejected as *navigation*. ✅ Taken as **velocity**: the command palette already shipped in P8 and is the one legitimate "non-traditional nav" here — because it is **additive**. The article's failure mode ("risk of confusing users; must maintain discoverability") is avoided precisely by never removing the conventional path. **R19**: no capability is reachable *only* by palette. |
| 14 | **AI chatbots** | A proactive agent that executes multi-step tasks for the user. | 🚫 Rejected on the write path, without qualification. This platform's whole discipline is audit-then-effect with a recorded reason and a second confirmation; an agent that "handles multi-step tasks" is a machine for producing unattributable privileged actions. ✅ Taken only as **anticipation**, which is not conversational: the console already records visited subjects and offers them. **R20**: nothing may perform a privileged command on the user's behalf. |
| 16 | **Progressive lead nurturing** | Forms that learn across interactions and pre-fill. | 🔴 **Rejected outright on the write path — this is the single most dangerous item in the article for this product.** P8 15.10 exists specifically to require that reason and typed-target fields open **empty, never pre-filled from context or history**. A pre-filled confirmation is a confirmation that confirms nothing. ✅ Taken only on the **read** path: filters and time windows may persist, because restoring a view asserts nothing. |
| 7 | **Gamification** | Points, streaks, badges, leaderboards. | 🚫 Rejected as engagement mechanics. ⚠️ Worth naming because P4 **ships a leaderboard**, and the trend is a live temptation to make it feel like a game. A leaderboard here is a ranking with confidence intervals in which **the top row is frequently a tie**. Any treatment that rewards rank 1 — a crown, a highlight, a celebratory transition — is a 🔴 **failing test** under R14's confidence reservation, not a design choice. |

### Rejected

| # | Trend | Why it is wrong here |
|---|---|---|
| 1 | 3D / immersive | Nothing in this product is a spatial object. Cost is real (WebGL payload), benefit is zero, and it violates the payload budget adopted above. |
| 3 | Vibrant / dopamine palettes | The hazard palette is **reserved** (P8 FR31): danger is legible because it is rare. Saturating the surface destroys the reservation, and the reservation is a safety property — it is what makes an armed global kill switch visible. The article names the pitfall itself: "accessibility concerns with extreme contrast." |
| 8 | Neumorphism | Soft low-contrast shadows against a near-background fill. Fails AA by construction, and its raised/inset affordance is exactly the ambiguity a destructive control must not have. |
| 9 | Retrofuturism | "Novelty factor may not age well" — the article's own caveat. An operator mid-incident is not an audience for novelty. |
| 10 | Maximalism | Directly opposed to the density finding: 2026 enterprise practice is **prioritization first, explanation second, raw depth on request**. Maximalism is the 2020 more-widgets instinct with better art direction. |
| 11 | Collage | No coherent mapping to tabular evidence. |
| 12 | Neo-brutalism / anti-design | "Risks appearing unprofessional" — for a surface whose output is billing evidence and merge decisions, deliberate roughness reads as an unmaintained system. |
| 15 | Voice interfaces | Privileged commands spoken aloud in a shared space, with a recognition layer between intent and effect. Fails Decision 14 at the first hop. |

---

## What the adopted set changes, concretely

Four new rules. They are numbered into the existing sequences rather than kept in a separate list,
because a rule in its own document is a rule nobody runs.

- **R16 / FR38 — Numbers outrank their frames.** A view's visual hierarchy is ordered by *what the
  reader came for*. The measured value is the largest element in its block; its label, unit and
  provenance are subordinate. Section chrome is never the largest thing in a section.
  *Corollary, and the reason this is not merely typographic:* a stat has to state its **unit and scale
  once**, carry **tabular figures**, and 🔴 **wear its qualifier beside it** — the confidence
  reservation (R14) governs the stat primitive exactly as it governs a table row. Making a number big
  makes it more believable; a big number that is provisional is a bigger lie.

- **R17 / FR39 — Theme is chosen, not assumed.** System / dark / light, persisted per operator, read
  server-side so the first paint is correct and nothing reflows after hydration. Both themes meet
  WCAG 2.1 AA on every token pair, and **no information is carried by a hue that only exists in one
  theme**.

- **R18 / FR40 — The payload has a ceiling.** The shipped client bundle has a stated byte budget and
  the build fails above it. This is the sustainability trend's only enforceable content, and it also
  protects the credential scan's premise: a bundle nobody can audit by eye is audited by size.

- **R19 / R20 — Acceleration never becomes the only path, and nothing acts on the user's behalf.**
  Every capability reachable by palette is reachable by navigation; no surface performs a privileged
  command without the operator's own confirmed, reasoned, unprefilled input.
  *(P9 `FR39` · P8 `FR41`.)*

---

## What was deliberately *not* changed, and why

Recorded because a redesign that silently drops a constraint is indistinguishable from one that
forgot it (`ui-redesign-feature-and-visual-consistency`).

- **The palette stays.** Both consoles keep their shipped hues. The customer palette was reconciled
  from four live pages by recorded winner (P9 Decision 2); the operator accent exists to make the two
  consoles distinguishable at a glance (P8 FR23), which is a safety requirement. A trend-driven
  re-skin would discard both arguments for a fashion with a stated shelf life.
- **The token set is not forked.** Everything above lands in `tokens.css` and the two identity layers.
  A "2026 refresh" living in one route's stylesheet is the fourth fork this system exists to prevent.
- **No feature is removed for visual reasons.** Density is a *rhythm* change, never an information
  change (FR29), and the same rule now governs every layout change made under this ledger: 🚫 a
  section may not be dropped, collapsed by default, or moved behind a disclosure because it "makes the
  page cleaner."
