# UI/UX Improvement Plan — P9 Web Console (R1–R15)

> **Why these are rules, not suggestions.** A visual specification that cannot be checked is not a
> specification, it is a preference. Every rule below is written as **must / must not**, with the
> concrete defect in this repository that motivates it, and a **regression guard** that can turn red.
> The standing lesson from this codebase is that a rule which exists only as a comment gets violated
> repeatedly; a rule wired to a failing check does not.
>
> **Scope.** These govern the P9 console. They are enforced through the requirements in
> [`specs/console-design-system/spec.md`](specs/console-design-system/spec.md),
> [`specs/web-console/spec.md`](specs/web-console/spec.md) and
> [`specs/console-marketing-site/spec.md`](specs/console-marketing-site/spec.md); this file is where
> the *reasoning* lives.
> Behaviors to preserve are in [`feature-inventory.md`](feature-inventory.md).

---

## Ratification (§1.2) — signed off, not assumed

**R1–R15 are ratified by the product owner.** Removing capability is a product decision, so the four
deliberate drops are recorded here with what is given up and what replaces it, rather than being
folded silently into a port.

| # | Dropped | What is given up | What replaces it | Where |
|---|---|---|---|---|
| 1 | `p4board.html`'s hardcoded `'wf-demo'` default | Opening `/p4/board` with no parameter currently renders **something**. | Selection, or an empty state. A confident, fully-populated board for a workflow that is not the user's is strictly worse than an empty state: an empty state tells the truth, a wrong default asserts a falsehood with the full authority of a populated UI. | R8 · P4-0 · FR10 |
| 2 | `index.html`'s Chinese UI strings | Nothing — the page has no Go handler and its three endpoints do not exist. | The same surface in English, against the P5.5 API. | R4 · IDX · FR23 |
| 3 | `index.html`'s unconditional 15 s polling that never stops | A queue that refreshes forever without being asked. | A bounded refresh strategy. An always-on timer against a queue with **no terminal state** is a cost with no owner, and it is the one polling loop in the tree that can never decide to stop. | IDX · R5 |
| 4 | `index.html`'s `alert()` on action failure | A modal that blocks the page and carries no detail. | An in-page error state carrying the failure class, per R5's three-way taxonomy. | IDX · R5 |

**Also ratified:** the **cutover** in [`tasks.md`](tasks.md) §12 — each of `p2.html`,
`p25monitor.html`, `p35graph.html` and `p4board.html` is removed **together with its Go handler and
`go:embed` directive** once its canonical route exists and every inventory item is checked or
explicitly dropped; and `internal/api/static/index.html` is deleted, having no handler and three
endpoints that do not exist. Recorded here because a deletion is a one-way door and 🔴
`pattern-violation-minimal-fix` requires it to be explicit rather than a drive-by.

**Not dropped, and not open for reinterpretation during delivery:** everything else in
[`feature-inventory.md`](feature-inventory.md). A behavior absent from the drop table above and absent
from the console is a **defect**, not a decision.

---

## R1 — One design language. No page-local palettes.

**Mandatory.** Every visual value — color, radius, spacing, type scale, font stack, sizing unit —
resolves to the **single token set**. No route, component, inline style or SVG attribute defines its
own color, radius or font.

**The defect.** The design language has already forked **three ways across four files**:

| | `p2` / `p25monitor` / `p35graph` | `p4board` | `index` (orphan) |
|---|---|---|---|
| `--muted` | `#8b9cb3` | `#8fa3bd` | `#8b9cb3` |
| `--line` | `#2a3545` | `#243247` | literal `#2a3545`, no token |
| card radius | `10px` | `8px` | `10px` |
| chip radius | `4px` (`5px` on `p35graph`) | `999px` | `4px` |
| sizing | `rem` | `px` | `rem` |
| font stack | 3-family | 5-family | 3-family |
| status names | `--ok`/`--warn`/`--bad`/`--halt` | `--good`/`--warning`/`--serious`/`--critical` | `--ok`/`--warn` |

Plus hardcoded literals that bypass tokens entirely: `p35graph.html`'s legend swatches and SVG marker
fills (`#8b9cb3`, `#f0c14b`, `#3d8bfd`, `#c084fc`, `#5a6b80`, `#3ecf8e`) are written into the markup,
and `index.html` never tokenizes its border color at all.

**The resolution** is in [`design.md`](design.md) Decision 2 — a **recorded winner per token**, chosen
on merit where merit is clear (`rem` for respecting user font size; the 5-family stack for native
rendering) and on majority otherwise. Not averaged into a fourth variant.

**Exceptions.** Domain-specific tokens are legitimate and are *promoted into* the token set rather than
left inline — the graph's `--llm` / `--ctl` / `--none`, and the chart `--series-1..4`. `--llm` and
`--halt` share the value `#c084fc` and keep **both names**, because they mean different things and will
not always be the same color.

**Regression guard.** A repo scan fails the build on a color / border-radius / font-family literal
outside the token definition file.

---

## R2 — No feature is lost in the port.

**Mandatory.** Every behavior in [`feature-inventory.md`](feature-inventory.md) is present in the
console, **or** listed there as deliberately dropped with a reason. A behavior must never disappear by
not being mentioned.

**The defect this prevents.** A reference design demonstrates *visual style*; it is never a complete
*functional* spec. Porting against a mockup is how a second chart series, a sort control, a tooltip, a
keyboard path or an empty-state sentence gets deleted — each one individually plausible, none of them
noticed until a user comes back for it.

**The behaviors most likely to be dropped silently**, each an explicit test case: SSE-first with
polling fallback engaging **only if no message ever arrived** (P25-8/9); polling that stops on the
**run record's** status and not on a node-derived condition (P2-24); row virtualization above 60 rows
with its explanatory footer (P4-20); keyboard row navigation **with wrap-around** (P4-18); the Pareto
tooltip bound to `focus` as well as `mousemove` (P4-27); the `<details>` tabular fallback for the
scatter plot (P4-30); back-edge routing under the row so a Reflection loop stays visible (P35-6);
the conditional *nothing was persisted* message shown **on 400 only** (P2-9); and the error path that
hides the graph, label **and** diagnostics cards together (P35-17).

**If a reference design omits something**, the default is **keep the existing behavior**. Removing
capability is a product decision and belongs to the person who owns the product, not to the port.

**Regression guard.** The inventory runs as a test suite; an unchecked item blocks removal of the
corresponding legacy page.

---

## R3 — Every state carries a distinct color **and** a distinct word, and unknown states degrade visibly.

**Mandatory.** A status is never conveyed by color alone. Two conditions with different user remedies
never collapse into one rendering. A status value the console does not model renders with a **defined
fallback style and the raw value visible**.

**The defect.** `p2.html` builds its CSS class by string interpolation — `state-${status}` — against a
stylesheet that knows eight values. A ninth status from the server produces a class no rule matches, so
it renders **unstyled and uncolored while still looking like a rendered state**. The failure is silent
in both directions: the user sees no signal, and no error is logged. `p25monitor.html` has the mirror
defect with the opposite failure mode — an unknown status falls back to the **`running`** style, so an
unmodelled state actively **impersonates a known one**.

**Counter-examples.**

| Bad | Why |
|---|---|
| Unknown status → no class → invisible | Silent information loss; looks like the design intended it. |
| Unknown status → styled as `running` | Worse: it asserts something false. |
| Collapsing "no result" and "could not measure" into one grey chip | Different remedies. A state that is always true carries zero information. |
| Distinguishing pass/fail by green vs. red alone | Fails for colorblind users and in greyscale. |

**What good looks like** already exists here: `p4board.html` distinguishes Pareto frontier membership
by **shape** (diamond vs. circle) as well as color, and `p35graph.html` distinguishes data from control
edges by **dash pattern and arrow marker** as well as hue.

**Regression guard.** A test feeds the console a status value absent from the token set and asserts the
fallback style is applied **and** the raw value is rendered.

---

## R4 — English UI strings; `en-US` formatting through one swap point.

**Mandatory.** All UI strings are English: labels, buttons, tooltips, placeholders, empty states, error
copy, table headers, and null placeholders. All `Intl`-based date / time / number formatting is pinned
to **`en-US`** through a **single** function, never `navigator.language`.

**The defect.** `internal/api/static/index.html` is entirely Chinese (`lang="zh-CN"`) — headings, status
line, buttons (批准 / 拒绝), and error copy. A Chinese source comment also survives inside `p2.html`.

**Why the pinned locale matters even in an English-only product.** `Intl` following the browser locale
means a Chinese-locale browser renders `rtf.format(0,'second')` as "现在" **next to an English label** —
a mixed-language string inside one sentence. The single swap-point function is the only i18n work in
P9; it is the seam a future i18n phase uses.

**Regression guard.** A scan fails the build on non-ASCII characters in user-facing string literals, and
on any `Intl.*` construction that does not go through the locale helper.

---

## R5 — Loading, empty, and error are three renderings. Error classes stay distinguishable.

**Mandatory.** Every view renders **loading**, **empty**, and **error** distinctly. Within error,
**503 subsystem-not-mounted**, **404 not-found**, and **transport failure** remain three outcomes with
three messages. A 404 is never mapped to a business state. A transport failure is never rendered as an
empty result.

**This rule preserves rather than fixes.** The current pages get this right and the port must not
flatten it. `p35graph.html` is the model: its transport error says explicitly *"this is a transport
failure, not an empty classification,"* and its 404 says *"no such workflow (distinct from a workflow
that exists but is unclassified)."* Those parentheticals are the product.

**Counter-examples.**

| Bad | Why |
|---|---|
| `catch { return [] }` | Transport failure becomes indistinguishable from "there is genuinely nothing." The user is told the wrong thing and looks in the wrong place. |
| Mapping any 404 to `{ notConfigured: true }` | Silently invents a business state out of a routing fact. |
| One generic *Something went wrong* | Three different remedies (mount the subsystem / check the id / check the network) collapse into none. |
| Error state that collapses the whole page skeleton | The user loses the controls needed to retry or change subject. |

**Also mandatory:** empty-state copy is **status-dependent** where the current pages make it so — a run
with no nodes reads differently while `running` than when terminal (P2-26, P25-7).

**Regression guard.** Per-view tests for all four states, with the three error classes asserted to
produce three different messages.

---

## R6 — The `p4board` accessibility level is the floor, not the exception.

**Mandatory.** Every interactive element is keyboard-reachable with a visible focus indicator; every
graphical data representation carries a text alternative; every data table uses scoped column headers;
every chart has an accessible tabular fallback; contrast meets WCAG 2.1 AA.

**The defect.** Accessibility exists on **one page of five**. `p4board.html` has `:focus-visible` rings
on every interactive element, `role="img"` with descriptive `aria-label` on each CI bar and each Pareto
mark, keyboard row navigation (Enter/Space to expand, arrows to move, wrap-around), `scope="col"`
headers, a `role="status"` tooltip reachable by **focus** and not only hover, and a `<details>` table
fallback for the scatter plot. `p2.html`, `p25monitor.html` and `p35graph.html` have **none** of it:
no focus styling, no ARIA, no keyboard path.

The floor is not aspirational — it already exists in this repository, and four pages sit below it.

**Highest-value gaps to close first:** the P2 node table and P25 monitor table (no scoped headers, no
keyboard path), and the P3.5 graph (an SVG with **no text alternative at all** — currently unreadable
to a screen reader, and it is the primary comprehension surface for a workflow).

**Regression guard.** Per-page automated audit **plus a keyboard-only pass**, which no automated tool
substitutes for. A page below the floor does not ship.

---

## R7 — Escape by default. Never interpolate a server string into markup.

**Mandatory.** All values are escaped on render.

**The defect.** `p25monitor.html` builds table rows by string concatenation —
`'<td>' + n.node_id + '</td>'` — and the page **has no escaping helper defined at all** (the other
pages each define one; this one was missed). `node_id` is derived from customer source code. The three
pages that do escape are also inconsistent: `p2.html` and `index.html` escape five characters,
`p35graph.html` and `p4board.html` escape four (no `'`).

**Resolution.** React's default escaping covers this, which removes the class of defect rather than
fixing one instance — but only if the port does not reach for `dangerouslySetInnerHTML`.

**Regression guard.** Lint bans `dangerouslySetInnerHTML` outside an explicitly reviewed allowlist; a
test renders a node id containing markup and asserts it appears as text.

---

## R8 — Stop asking for identifiers the system already has. Never invent one.

**Mandatory.** The console lets the user **select** a workflow, run, variant, board or transform from
platform data. No route requires a hand-typed identifier for a subject the platform can enumerate, and
**no route substitutes a hardcoded default subject** when none is supplied.

**The defect.** Four pages, zero navigation, zero links between them, and every entry point a bare query
parameter the user must already know: `?run=` / `?cfg=` / `?rev=` (P2), `?run_id=` (monitor),
`?workflow_id=` (graph), `?workflow=` (board). `p25monitor.html` does not even have an input field — the
only way in is to edit the URL. The error copy is literally instructions for editing a URL:
*"No run_id in the URL. Append ?run_id=…"*.

**The worst instance** is `p4board.html`'s `const workflow = params.get('workflow') || 'wf-demo'`. A user
who opens the board with no parameter is shown a **fully rendered, confident board for a workflow that
is not theirs**. That is strictly worse than an empty state: an empty state tells the truth, a wrong
default asserts a falsehood with the full authority of a populated UI.

**The principle.** Every parameter the user must supply is a mental-load and error-rate increase. If the
system can derive, enumerate or remember it, the user must not be asked. Where a value genuinely cannot
be derived, the empty state carries the **next action**, not a syntax lesson.

**Regression guard.** A test opens every route with no parameters and asserts a **selection or empty
state** — never populated data.

---

## R9 — Deep links survive, as canonical routes.

**Mandatory.** Every current entry point maps to a stable, shareable canonical route that opens exactly
its subject. A link pasted into a PR resolves to the same view for its recipient.

**Why it is a rule.** R8 removes hand-typed parameters as an *entry mechanism*; it must not remove
**shareability**, which is a real and used capability — `p2.html` deliberately supports `?run=` and
`?cfg=`+`?rev=` auto-load precisely so a link can point at evidence. Fixing the input problem by making
views unaddressable would trade one defect for a worse one.

**Regression guard.** A test asserts each legacy entry point resolves to its canonical route and renders
the same subject.

---

## R10 — The browser gets a session. Never a platform key.

**Mandatory.** The platform API key is held server-side only. The browser holds an `HttpOnly`,
`SameSite` session cookie it cannot read. No credential appears in the client bundle, in `localStorage`,
in a URL, in a log line, or in a telemetry attribute.

**The defect.** Page routes are public; `/api/*` routes require `X-API-Key`. Under `auth_mode=required`
every page loads and then 401s on every fetch. The two obvious workarounds are both unacceptable — run
unauthenticated (the read models expose prompts, diffs, costs and provider spend), or put a long-lived
platform credential in the browser (exfiltrable by any XSS, with no per-user revocation).

**Also mandatory:** an unauthenticated route **redirects to sign-in** rather than rendering a shell that
then fails every request. A shell that renders and then 401s is the current behavior, and it teaches the
user that the product is broken rather than that they are signed out.

**Regression guard.** A build-time gate scans the shipped client bundle for key material; tests assert
the redirect, and assert that a revoked session is denied at the **next** request.

---

## R11 — Acceptance is a rendered browser, not a green build.

**Mandatory.** Every user-visible behavior is verified by rendering it in a **real browser** against a
**real API response**, at a fixed viewport, with the network traffic inspected so the assertion is
*the screen agrees with the response*. The error path is walked, not only the happy path.

**Why this is a hard rule.** A successful build, a passing type-check and green unit tests are all
compatible with a page that renders nothing, renders a raw key, or renders the wrong subject. The whole
class of defects this plan addresses — an unstyled unknown status, a missing text alternative, a
hardcoded default workflow, a mixed-language timestamp — is invisible to every one of those gates and
obvious in one second of looking.

**Procedure per view:** navigate → wait for the data request → read the page structure → inspect the
network response → screenshot → assert screen against response. Fixed viewport for reproducible
screenshots; keep image dimensions bounded.

**Regression guard.** Browser-rendered evidence is required in the acceptance record for any change to a
user-visible behavior. A green build is explicitly not acceptance.

---

## R12 — Every read-model field is rendered or explicitly dropped. No silent unread fields.

**Mandatory.** For each field the platform returns, the console either renders it or records it in the
inventory as **deliberately unrendered, with a reason**.

**The defect.** The Go read models already compute and return fields **nothing renders**:

| Field | Source | Why it probably matters |
|---|---|---|
| `spend.budget` | `SpendReport` | The board shows spend and shows *"budget cap reached"* — but never the budget. The reader cannot tell how close they are until they hit it. |
| `DimensionView.uncovered[]` | `CoverageView` | Coverage shows a percentage; this is the list of **what is actually uncovered** — the actionable half. |
| `ComponentView.raw_ci_low` / `raw_ci_high` | `ComponentView` | Per-component intervals. The composite score shows its CI; components show a point estimate, implying more precision than exists. |
| `ComponentView.unit` | `ComponentView` | Raw values render unitless. |
| `judge.percent_agreement` / `floor` | `JudgeView` | κ is shown; the floor it is being judged against is not, so the reader cannot tell how close to disqualification a judge is. |
| `coverage.low_confidence` | `CoverageView` | Surfaced only indirectly through `reasons[]`. |
| `progress.seed_floor` | `progress` | Rows are marked `provisional` "below the seed floor" without ever naming it. |
| `gate_set` | board | Which gate set produced these pass/fail outcomes. |
| `Row.variant_id`, `ParetoPoint.composite`, `spend.eval_run_id` | board | Identity and lineage. |
| `ViewNode.symbol` / `.policy` / `.tools` | `GraphView` | Per-node detail with no surface; `.nodebox.hl` is even styled for a selection that no code applies. |
| `RunMonitor.config_hash` | monitor | Present, never shown, while every other view leads with it. |
| `state === 'complete'` | board | Has no distinct rendering, so "finished" looks like "in progress with nothing left." |

**Why unread fields are not free.** They are one of two things: information the customer needs and is
not getting, or contract surface nobody is maintaining. Both cost something, and the cost is invisible
until someone asks why the number they need is in the API response but not on the screen.

**Process.** Each field gets a **surface-or-drop** decision with the owning phase. Where the console
cannot render it usefully, the decision is filed against the owning phase rather than compensated for
with a client-side computation (which R-series rule R2 of [`design.md`](design.md) Decision 3 forbids).

**Regression guard.** A test compares the read-model type surface against the rendered-or-declared list
and fails on a field that appears in neither.

---

## R13 — One subject per view, on screen before its data. Structure never reflows.

**Mandatory.** Every view has **exactly one subject**, carried by exactly one display-level heading,
and that subject is rendered in the **first paint** — before its data resolves. A skeleton occupies
the shape the content will take, so the arrival of data changes values and never structure.

**The defect this prevents.** All four current pages open the same way: a bare shell, then a generic
`Loading…`, then a jump as content lands. `p4board.html` renders *Loading board…* into an empty page
with no indication of which workflow is loading — so during the one second the reader most wants to
know they are in the right place, the screen tells them nothing. And because the empty state and the
populated state have different shapes, every load ends in a reflow.

**Why it is a rule rather than a nicety.** A view that cannot name its subject before its data arrives
is a view that cannot be shared, cannot be resumed, and cannot be told apart from the same view on a
different subject in a screenshot. The subject is not decoration on the data — it is the one thing the
route already knows for certain.

**Counter-examples.**

| Bad | Why |
|---|---|
| A full-page spinner with no subject | The reader cannot confirm they opened the right thing until it is already loaded. |
| An empty state that is structurally different from the populated state | Every load ends in a layout jump; the eye re-finds everything. |
| Two `<h1>`-level headings on one view | Two subjects means no subject; the page is really two pages. |

**Regression guard.** A test asserts exactly one display-level heading per route, asserts the subject
string is present in the pre-data render, and asserts the skeleton and populated renders produce the
same structural signature.

---

## R14 — 🔴 The confidence treatment is reserved for confident values.

**Mandatory.** Emphasis reserved for a settled result — accent color, elevation above peers, entrance
animation, display-weight type — may **not** be applied to a value the server qualified:
`provisional`, `tie`, `disqualified`, `low-confidence`, an uncalibrated judge, `withheld`,
`candidate`, unverified, or entitlement-gated.

**Why this is the load-bearing craft rule.** Everything else in this document protects information
from being lost. This one protects it from being **overstated**, which on this product is worse. P4
went to considerable trouble to make a tie a tie: overlapping confidence intervals are *not* an
ordering, so `p4board.html` renders a tied rank muted and non-bold, puts disqualified variants in
their own section titled *excluded from the ranked order, not ranked last*, and flags an uncalibrated
judge wherever its metric appears. Every one of those is a **statistical** decision expressed
visually. A styling pass that gives row 1 a glow, or animates the leader in, has silently overturned
it — and no test in the eval harness can see that happen.

This is the direct analogue of the operator console's reserved hazard palette
([`web/design-system/README.md`](../../../web/design-system/README.md) §4): danger is legible because
it is rare, and **confidence is credible because it is earned**. Spend it on a provisional number and
the reader stops being able to tell which figures to trust — which is the entire value of the product.

**Counter-examples.**

| Bad | Why |
|---|---|
| Rank 1 accented while tied with rank 2 | Asserts an ordering the server explicitly declined to assert. |
| A provisional interval animated in like a result | Motion reads as arrival-of-answer; there is no answer yet. |
| A gated capability styled exactly like an available one, with the gate only in a tooltip | Reads as a broken feature rather than an unpurchased one (R-series: FR15). |
| An unverified proposal card that looks like a verified one | The one rendering P5.5 exists to prevent. |

**Regression guard.** A test renders a board whose top row carries `tie`, and asserts the row does not
carry the confidence-treatment class; the same test runs for `provisional`, `disqualified`,
`low-confidence`, `withheld`, `candidate` and gated.

---

## R15 — The public surface may not claim more than the product ships.

**Mandatory.** The public home page renders with **no session, no tenant data and no upstream platform
call**. Every capability claim on it resolves through a **checked-in capability manifest** naming the
owning phase and shipped state; a claim whose capability has not shipped **fails the build**. Plans are
named, never priced. No third-party origin — no external font, script, tracker or image host.

**The defect this prevents.** A marketing surface is the one page in a repository that no test reads
and no engineer re-checks after the first week. It is therefore the page most likely to describe the
roadmap in the present tense, and the drift is not caught internally — it is caught by a customer,
after the sale, as a support ticket or a refund. Making the claims resolve through a manifest turns
"only promise what has shipped" from a habit into a gate.

**Why no upstream call.** Two reasons, and the second is the one that matters. First, an anonymous
visitor must never cause the BFF to use the server-held credential (R10). Second, the page a prospect
first sees must not go down with the platform API — a marketing page that 500s during an incident is
the worst possible sample of the product.

**Counter-examples.**

| Bad | Why |
|---|---|
| "Automatically optimizes your prompts" when auto-merge is gated and PRs are human-merged | Sells an automation level the platform deliberately does not offer by default. |
| A price on the page | A number in git outlives the moment it was true, and it ships (P8 FR28). |
| A hosted font or an analytics tag | Breaks `default-src 'self'`, and sends a prospect's IP to a third party before they consent to anything. |
| A "customer logo" strip or benchmark figure with no source | Fabricated evidence on the surface whose entire job is credibility. |

**Regression guard.** The claim scan fails the build on a page claim absent from the manifest or
marked unshipped; the existing bundle scan already fails on a priced literal; a CSP test asserts no
third-party origin is referenced.

---

## Rollout order

The rules are not equally urgent. Suggested order, by what unblocks the most:

| Order | Rules | Why first |
|---|---|---|
| 1 | **R10** | Security. Nothing else can ship to a customer until the credential boundary exists. |
| 2 | **R1**, **R4** | The token set and the string/locale seam are the substrate every component sits on; retrofitting them later touches every file. |
| 3 | **R2**, **R5**, **R3** | Correctness of the port itself — no feature loss, states stay distinguishable. |
| 4 | **R8**, **R9** | The largest experience win, and the one users will notice first. |
| 5 | **R6**, **R7** | Accessibility floor and escaping — both are cheap in a React port and expensive to retrofit. |
| 6 | **R12** | Needs cross-phase decisions, so it runs alongside rather than blocking. |
| 2½ | **R14** | Sits with R1/R4 rather than after them: the reservation is a property of the token set and the primitives, and retrofitting it means revisiting every view that already shipped an emphasis. |
| 4½ | **R13** | Follows R8/R9, because "one subject per view" is only expressible once the routes know what their subject is. |
| 6 | **R15** | Independent of the credential boundary — the public page has no session — so it can run in parallel from the start. Its gate must exist before the page does, not after. |
| — | **R11** | Not an item — it is the gate every one of the above passes through. |
