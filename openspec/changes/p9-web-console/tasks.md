# Tasks — P9: Web Console

Two waves. **Wave 9a** = the credential boundary + the app shell + the design system **and its craft
layer** + the **public home page** + parity ports of the four live surfaces. **Wave 9b** = the product
layer (entitlement gating, diagnosis views, the proposal-review surface once P5.5 exists) and the
cutover.

**Standing constraints for every task below.**
No new platform endpoint, table, queue, or statistic — the console renders read models that already
exist. Nothing under [`internal/api/static/`](../../../internal/api/static/) is modified or deleted
before §11. Acceptance for any user-visible behavior is **rendered-browser evidence**, never a green
build. Every behavior in [`feature-inventory.md`](feature-inventory.md) is either present or recorded
there as deliberately dropped with a reason. 🔴 And the craft rules bind the same way the gates do: the
**confidence treatment is reserved for values the server did not qualify** (R14), and nothing on the
**public surface** may claim a capability the platform has not shipped (R15).

---

## 1. Product + Frontend — Inventory and rules first (9a, blocks everything else)

- [x] 1.1 Ratify [`feature-inventory.md`](feature-inventory.md) against the five source files and the Go
      read models (`internal/evalboard/view.go`, `internal/patternclassifier` `GraphView`,
      `internal/telemetry` `RunMonitor`, and the P2 view types in `internal/api/p2.go`). Anything the
      inventory missed is added **before** any component is written.
- [x] 1.2 Ratify [`ui-ux-plan.md`](ui-ux-plan.md) R1–R15 with the product owner, in particular the two
      deliberate drops (`p4board`'s hardcoded `'wf-demo'` default, and the legacy page's Chinese
      strings / unbounded 15 s polling / `alert()` error path). **Removing capability is a product
      decision** — it is signed off here, not assumed.
- [x] 1.3 Produce the **surface-or-drop decision list** for every read-model field currently unrendered
      (R12): `spend.budget`, `DimensionView.uncovered[]`, `ComponentView.raw_ci_low`/`raw_ci_high`/
      `unit`, `judge.percent_agreement`/`floor`, `coverage.low_confidence`, `progress.seed_floor`,
      `gate_set`, `Row.variant_id`, `ParetoPoint.composite`, `spend.eval_run_id`,
      `ViewNode.symbol`/`policy`/`tools`, `RunMonitor.config_hash`, and `state === 'complete'`. Each
      gets an owner phase and a decision.
- [x] 1.4 Define the console's information architecture: the shell, the navigation set, and the
      canonical route for each subject, so every legacy entry point has a target (FR11).

## 2. System Designer + DevOps — Decide the two one-way doors (9a)

- [x] 2.1 Record an **ADR for deploy packaging** (one container under a supervisor vs. two containers in
      one unit), satisfying `design.md` Decision 6's requirements: declared, supervised, health-checked,
      readiness-aggregated, pinned runtime, lockfile-reproducible deps.
- [x] 2.2 Decide the **type-generation toolchain** (`design.md` D5 / PRD §14 Q3). Preference: emit JSON
      Schema from the Go view types and generate TypeScript from that, composing with the existing
      [`schemas/`](../../../schemas/) discipline. Record the decision.
- [x] 2.3 Confirm with P7 the **tenant-identity binding** the session exchange authenticates against
      (PRD §14 Q1). P9 designs against an abstract authenticated tenant principal and must not pre-empt
      the P7 mechanism.

## 3. Backend — Session and credential boundary (9a, the security-critical work)

- [x] 3.1 Implement session issuance against P7 tenant identity: server-side session bound to a tenant,
      bounded lifetime, revocable. Set the browser cookie `HttpOnly` + `SameSite`.
- [x] 3.2 Hold the platform API key in the BFF process environment only. Gate the build so key material
      cannot appear in the shipped client bundle.
- [x] 3.3 Make every console data route **fail closed**: an unauthenticated request redirects to
      sign-in and is never served a shell that then fails its data requests.
- [x] 3.4 Deny an expired or revoked session at the **next** request, with no grace period and **no
      silent retry using the server-held credential**.
- [x] 3.5 Derive request scope **server-side from the session's tenant**. A client-supplied tenant
      identifier must not widen, change, or override it.
- [x] 3.6 Implement the upstream forwarder as a **pass-through**: no merging of responses, no
      re-ranking, no reformatting, no status translation, no business rules.
- [x] 3.7 Preserve the three-way failure taxonomy end to end — **503 not-mounted**, **404 not-found**,
      **transport failure** — carrying the upstream error body where one exists. Never map a 404 to an
      empty successful result.
- [x] 3.8 Give every upstream call an **explicit timeout**; a hung upstream surfaces as a transport
      failure, never as an unbounded loading state.
- [x] 3.9 Proxy the run-monitor **SSE** stream with flush semantics preserved, closing the client stream
      when upstream closes, without batching events.
- [x] 3.10 Emit structured logs and traces correlated with the platform `trace_id`, carrying **no**
      prompt text, diff content, or credential — on the error path as well as the success path.
- [x] 3.11 **Tests (security assertions, not review items):** no key material in the shipped bundle; an
      unauthenticated data route redirects rather than rendering; a revoked session is denied at the next
      request; a client-supplied tenant id cannot widen scope; a 404 is not returned as an empty result;
      a hung upstream produces a transport failure within the timeout.

## 4. Backend + DevOps — Readiness and operability (9a)

- [x] 4.1 Aggregate the console component into the platform readiness signal in
      `internal/api/server.go`: a healthy platform service with an unreachable console **does not report
      ready**, and the degraded component is **named** on a machine-readable endpoint.
- [x] 4.2 Ship the console as one declared, supervised, health-checked component per the §2.1 ADR, with
      a pinned runtime version and a lockfile-reproducible dependency tree.
- [x] 4.3 Source all secrets from the environment or secret store — never the repository, never a log,
      never a trace attribute, never the client bundle.
- [x] 4.4 **Test:** readiness reports not-ready and names the console when the console is unreachable.

## 5. Frontend — Design system substrate (9a, before any page)

- [x] 5.1 Build the **single token set** from `design.md` Decision 2's reconciliation table, including
      the promoted domain tokens (graph `--llm`/`--ctl`/`--none`, chart `--series-1..4`) and both names
      where two concepts currently share a value.
- [x] 5.2 Add the **build gate** that fails on a color / border-radius / font-family literal outside the
      token definition (R1).
- [x] 5.3 Build the status primitive: **distinct color and distinct word**, a **defined fallback for
      unmodelled values that displays the raw value**, and no styling that lets an unknown status
      impersonate a known one (R3).
- [x] 5.4 Build the loading / empty / error primitives as **three distinct renderings**, with the three
      error classes carrying distinct copy and the surrounding controls preserved on error (R5).
- [x] 5.5 Build the `en-US` locale swap point — a single function every formatter resolves through —
      and the scan that fails on non-ASCII user-facing string literals and on any formatter that
      bypasses it (R4).
- [x] 5.6 Establish the accessibility primitives: focus-visible treatment, scoped table headers, the
      chart text-alternative and tabular-fallback pattern, and the focus-reachable tooltip pattern (R6).
      Take `p4board.html` as the reference — it already implements all of these.
- [x] 5.7 Ban raw-markup rendering in lint, with an explicitly reviewed allowlist (R7).
- [x] 5.8 **Test:** an unmodelled status renders the fallback **and** its raw value; a value containing
      markup renders as literal text; a non-English browser locale still produces `en-US` formatting.

### The craft layer (R13, R14 — a floor of lower bounds produces a screen nobody opens)

- [x] 5.9 Promote the **craft tokens** into the shared set: the elevation model, the motion budget, the
      editorial type roles, and the **atmosphere** layer (the page's own depth treatment). Add the build
      gate that fails on a transition/animation duration outside the motion budget.
- [x] 5.10 Build the **page frame primitive** enforcing R13: exactly one display-level heading naming
      the subject, the subject rendered in the **first paint** before data resolves, and a skeleton that
      occupies the shape the populated content will take so arrival changes values, never structure.
- [x] 5.11 🔴 Build the **emphasis primitive** and its reservation (R14): the confidence treatment is
      applied only to a value the server did not qualify, and applying it to `provisional` / `tie` /
      `disqualified` / `low-confidence` / uncalibrated / `withheld` / `candidate` / unverified / gated
      is a **failing test**, not a review note.
- [x] 5.12 Build the **anticipation primitives**: a per-session visited-subject record (console-local,
      never a platform statistic), the keyboard command path reaching every surface and visited subject,
      and the subject-carrying navigation that stops each surface re-asking who the subject is.
- [x] 5.13 **Test:** a tied top row does not carry the confidence treatment; every duration resolves to
      a motion-budget token; `prefers-reduced-motion` renders every state the motion would have carried;
      each route renders exactly one display-level heading and names its subject before data resolves.

### 5b. Hierarchy, theme, payload, agency (R16–R20 — the 2026 review)

The trend review is recorded in
[`../../../web/design-system/trend-ledger.md`](../../../web/design-system/trend-ledger.md): sixteen
trends, four adopted, four adapted-and-inverted, eight rejected, each with its reason and its check.
§5's craft layer fixed *how emphasis is earned*; it never said **which element on a view should be
largest**, and the shipped console answers that wrongly — measured values render at the smallest type
size on the page while the section frames introducing them render larger. Measured in a browser on
2026-07-24: the graph view spends ~900px of scroll and three padded section frames to present three
integers, each set in a `--text-xs` chip.

This block comes **before** §8 deliberately: the four ports land on these primitives, and porting first
would mean restyling four surfaces twice.

- [x] 5b.1 Build the **stat primitive** — value at display scale, label and unit subordinate, unit and
      scale stated **once**, tabular figures — and the **section budget** that stops a frame from
      outweighing its content (FR36). 🚫 A page may not answer "this needs to look denser" by dropping
      a section, collapsing one by default, or hiding one behind a disclosure: density is a rhythm
      change, never an information change.
      → `Stat`/`Stats` in `primitives.tsx`, `--text-stat`/`--text-stat-sm` in the shared tokens, and
      `.section__head` moved to the tighter rhythm. Applied to every **summary block**: the graph's
      node/edge/LLM counts, the scorecard's Overall four, the account period's spend.
      ⚠️ **The requirement was narrowed while implementing it.** As first written FR36 said "every
      block that presents a quantity", which would have forced display scale onto **table cells** and
      destroyed the comparison a table exists for. It now governs **summary blocks** and explicitly
      exempts tables, which stay under FR31. Recorded because a rule that cannot be implemented as
      written gets quietly ignored, and this one nearly was.
- [x] 5b.2 🔴 Hold the stat primitive to the **confidence reservation** (FR29): a value the server
      qualified — `tie`, `provisional`, `disqualified`, `low-confidence`, uncalibrated, `withheld`,
      `candidate`, unverified, gated — renders its qualifier **beside it at display scale**, and being
      large never substitutes for being settled. Size reads as certainty, so this is the same defect as
      5.11 with a larger blast radius.
      → `Stat` resolves through the same `emphasis(flags)` as `Value`; a test asserts it never writes
      the confident class by hand.
- [x] 5b.3 Add the **composition grid** in practice: a view whose content is three figures does not
      render as one full-width card holding three chips. Stats compose on the existing `--measure-*`
      and grid tokens; 🚫 no new width, gutter or breakpoint literal is introduced (R1).
      → `.stats` / `.stats--fill` on `--measure-stat`; the token scan confirms no new literal.
- [x] 5b.4 Add the **form measure**: an input is sized to its content, not to the viewport. The
      sign-in credential field currently spans 1440px for a token of ~40 characters.
      → `--measure-field` on `.field` with a `.field--wide` opt-out, `--measure-form` on `.page--form`.
- [x] 5b.5 Implement the **theme control** — follow system / dark / light, persisted, resolved
      **server-side** so the first paint is already correct with no flash and no post-hydration reflow
      (FR37). Both themes meet WCAG 2.1 AA on every token pair, and no information is carried by a hue
      that exists in only one theme.
      → `lib/theme.ts` + `components/themeControl.tsx` + `POST /api/theme`; `data-theme` is written on
      `<html>` in the root layout and is present in the first byte (`curl | grep data-theme`).
      The shared tokens now define both palettes once and map them in three value-free blocks, and the
      customer identity carries a light variant that reuses the shared light neutrals rather than
      inventing a second ramp.
      🔎 **Found by rendering:** the first implementation redirected to `Referer`, and this console
      sends `Referrer-Policy: no-referrer` — so every theme change silently returned the reader to `/`
      while the endpoint answered a perfect 303. Fixed with an `x-pathname` request header set in
      middleware and carried in the form.
- [x] 5b.6 Add the **payload ceiling** to the build — a stated byte budget on the shipped client
      bundle, failing with the budget and the overage named, and no rendering runtime shipped for
      decoration (FR38). Extends `scripts/scan-bundle.mjs`.
      → 1,400,000-byte ceiling; today's build ships **827,926 bytes**, 572,074 under. The measurement
      is taken from the build manifests rather than the directory, because `next dev` writes 7.6MB of
      its own chunks into the same place; a dev-written tree is **refused** rather than misreported.
- [x] 5b.7 🔴 Assert the two rejected trends stay rejected (FR39): every capability in the command path
      is also reachable by navigation, and no input carrying user intent is pre-filled, inferred or
      auto-submitted. On this product the second is not a style rule — a pre-filled confirmation is a
      confirmation that confirms nothing.
- [x] 5b.8 **Test:** on every data view the rendered type scale of a value exceeds that of its label,
      its section heading and its chrome; a qualified value at display scale carries its qualifier and
      not the confidence treatment; the first paint matches the persisted theme; both themes pass AA;
      an over-budget bundle fails the build; no palette-only capability exists.
      → `tests/craft.test.mjs`, 18 cases. Both payload fences are **proven red** against a sandboxed
      copy of the build tree — copied rather than mutated in place, because the first version corrupted
      chunks a concurrently-running test server was serving and made seven unrelated security tests
      fail intermittently.
- [x] 5b.9 🔴 **No feature lost.** Walk every view before and after §5b and confirm against
      [`feature-inventory.md`](feature-inventory.md) that no control, column, tooltip, keyboard path or
      empty-state sentence was removed for visual reasons
      (`ui-redesign-feature-and-visual-consistency`). A restyle is where capability disappears without
      anyone deciding to remove it.
      → Three views changed. Graph: three chips became three stats; the LLM chip's tone and its
      *fully rule-covered* / *a model was consulted* copy survive as the stat's note. Scorecard: the
      Overall table became stats, and its caption — *what this variant did across the eval set*, which
      states the **scope** of all four figures — was carried over as a caption rather than dropped with
      the `<table>`. Account: the spend figure kept the server-supplied unit, now as the Stat's `unit`
      so it is stated once structurally. Nothing else on any view was touched.

## 6. Frontend + System Designer — Typed contract (9a)

- [x] 6.1 Generate TypeScript types from the Go view structs per the §2.2 decision; check the generated
      artifact in.
- [x] 6.2 Add the **CI drift gate**: regenerate and fail the build on a diff, so a Go read-model change
      cannot reach the browser as `undefined`.
- [x] 6.3 **Test:** renaming a Go view field fails the build rather than producing a blank cell.

## 7. Frontend + Product — App shell, selection, canonical routes (9a)

- [x] 7.1 Build the shell and navigation reaching every surface; no surface reachable only by URL (FR9).
- [x] 7.2 Build **subject selection** from platform data for workflow / run / variant / board /
      transform. Direct identifier entry stays as an accelerator, not the only path (FR10, R8).
- [x] 7.3 Ensure **no route substitutes a default subject**. The eval board opened with no workflow shows
      selection or empty state — the `'wf-demo'` default is not ported (FR10, R8).
- [x] 7.4 Implement the canonical routes from §1.4 so every legacy entry point resolves to exactly its
      subject, and a shared link opens the same subject for its recipient (FR11, R9).
- [x] 7.5 Add cross-surface navigation: graph → run → transform → board without URL editing.
- [x] 7.6 **Test:** every route opened with no parameters renders selection or empty state, never
      populated data; every legacy entry point resolves to its canonical route.

## 7b. Product + Sales Operations + Frontend — The public home page (9a, R15)

The surface a prospect meets **before there is a session**. It is in this shell rather than in a
separate site so the token set, the string rules, the accessibility floor and the CSP apply for free —
and so "only promise what has shipped" can be a **build gate** instead of a review habit.

- [x] 7b.1 Write the **capability manifest**: every claim the public surface may make, with its owning
      phase and its **shipped state**, checked in beside the page. This is written **before** the page.
- [x] 7b.2 Add the **claim gate**: a claim rendered on the public surface that is absent from the
      manifest, or present but not shipped, **fails the build** and names the claim and its phase.
- [x] 7b.3 Build the public home page: what the platform does, the evidence discipline that makes it
      different, the surfaces it delivers, the plan names that unlock them, and the **boundary** stated
      beside the benefit — proposals a human merges, measured versus asserted, what still needs a
      person.
- [x] 7b.4 Guarantee the page renders with **no session, no tenant data and no upstream platform call**
      — demonstrated by serving it with the platform API stopped.
- [x] 7b.5 Keep it **plans-by-name, never priced**, and reference **no third-party origin** — no
      external font, script, tracker, stylesheet or image host, so `default-src 'self'` holds
      unrelaxed.
- [x] 7b.6 Give it the same floor as a data view: single token set, `en-US` strings, keyboard
      reachability with visible focus, WCAG 2.1 AA contrast, text alternatives on graphical content.
- [x] 7b.7 **Test:** an unshipped or unlisted claim fails the build; the page renders with the platform
      stopped; no third-party origin is requested; no priced literal ships.

## 8. Frontend — Port the four live surfaces (9a, parity against the inventory)

Each sub-task below ships only when its inventory section is fully checked.

- [x] 8.1 **Configure / diff / run** (`p2.html` → canonical routes). Inventory items **P2-1 … P2-26**.
      Load-bearing details: the *nothing was persisted* message appears **on HTTP 400 only**;
      `requires_human_review` is **read from the API, never recomputed**; a watch always stops the
      previous watch; only the first load shows a loading state.
- [x] 8.2 **Live run monitor** (`p25monitor.html`). Inventory items **P25-1 … P25-11**. Load-bearing:
      **SSE first**, polling fallback engaging **only if no message ever arrived**; per-row state carried
      by chip **and** row marker; status-dependent empty copy.
- [x] 8.3 **Pattern-classified graph** (`p35graph.html`). Inventory items **P35-1 … P35-19**.
      Load-bearing: deterministic layout from `layer`/`order`; **back edges routed under the row**;
      edge kind carried by **dash and marker**; container **scrolls, never shrinks**; an error hides the
      graph, label **and** diagnostics cards together; the *unlabelled ≠ no pattern* copy survives
      verbatim in meaning. Add the text alternative the SVG currently lacks entirely (R6).
- [x] 8.4 **Eval board** (`p4board.html`). Inventory items **P4-1 … P4-44**. Load-bearing:
      **virtualization above 60 rows** with its explanatory footer; **keyboard row navigation with
      wrap-around**; the Pareto tooltip bound to **focus as well as hover**; the `<details>` tabular
      fallback; frontier membership by **shape**; disqualified variants in their **own section**, not
      ranked last; a tied rank rendered de-emphasized; the residual framing sentence's meaning
      preserved; the budget-cap banner presenting a stop as **correct behavior**.
- [x] 8.5 Enforce **render-as-received** across all four: no client computation of score / CI / tie /
      rank / gate / dominance / coverage, no rounding before comparison, no client re-sort of a
      server-ranked field (FR14).
- [x] 8.6 Surface judge-calibration and weak-eval-set flags **wherever the affected metric appears**.
- [x] 8.7 Implement the §1.3 surface-or-drop decisions that landed on "surface".

> **§8 execution record — 2026-07-24.** All four inventory sections are checked, and the checking is
> **executable**: `web/console/tests/inventory.test.mjs`, 111 cases, one per item, all green. The full
> console suite is **197 / 197** with the build's five scans passing.
>
> 🔎 The suite found a real regression on its first run — the configurator's `fetch` calls were
> unwrapped and `busy` is derived from the outcome, so a transport failure disabled every control on
> the panel permanently, with no message. That is precisely the behaviour **P2-7** enumerates, and it
> had been lost in the port with nothing to notice it. See the execution record in
> [`feature-inventory.md`](feature-inventory.md) for the full accounting of the eight initial
> failures — seven were wrong assertions, one was this defect.

## 9. QA — The acceptance gate (9a, runs alongside §8)

- [x] 9.1 Turn [`feature-inventory.md`](feature-inventory.md) into an executable regression suite — one
      case per item, so "no feature loss" can **fail** rather than be asserted in a PR description.
      → `tests/inventory.test.mjs`, 111 cases, each named by its inventory id so a failure names the
      behaviour rather than the file.
- [x] 9.2 Named cases for the behaviors most likely to be dropped silently: SSE fallback condition,
      poll-termination source, virtualization threshold, keyboard wrap-around, chart tabular fallback,
      the 400-only persistence message, and the graph's combined error hide.
      → each has its own 🔴-marked case: **P25-9** (`sawMessage` — the distinction between a stream
      that never worked and one that ended), **P2-24**, **P4-20** (the 60-row threshold *and* its
      explanatory footer), **P4-18** (wrap-around), **P4-30**, **P2-9** (400 only), **P35-17**.
- [x] 9.3 Per-view state matrix: loading / empty / error / populated, with the three error classes each
      asserted to produce distinct copy.
- [x] 9.4 **Browser-rendered acceptance** for every user-visible behavior: navigate → await the data
      request → read the rendered structure → inspect the network response → screenshot → assert the
      screen agrees with the response. Fixed viewport for reproducibility; bounded image dimensions.
- [x] 9.5 Degradation runs: subsystems unmounted (503), missing subject (404), platform unreachable
      (transport), and **SSE forcibly disabled**.
- [x] 9.6 Accessibility: automated audit **plus a keyboard-only pass** per page. A page below the
      `p4board` level does not ship.
- [x] 9.7 Confirm no page renders a raw identifier as markup and no page displays a non-English string.
- [x] 9.8 **Craft acceptance (R13/R14):** per route, assert one display-level heading naming the
      subject, the subject present before data resolves, and the same structural signature between the
      loading and populated renders.
- [x] 9.9 🔴 **Reservation acceptance (R14):** a case per qualifier — `tie`, `provisional`,
      `disqualified`, `low-confidence`, uncalibrated judge, `withheld`, `candidate`, unverified,
      gated — asserting the value does **not** carry the confidence treatment and its qualifier renders
      beside it rather than only in a tooltip.
- [x] 9.10 **Public-surface acceptance (R15):** the page renders with the platform API stopped; an
      unshipped claim fails the build; no third-party origin is requested; the accessibility floor and
      keyboard-only pass hold.

> **§9 evidence — [`acceptance-record.md`](acceptance-record.md).** 214 / 214 across five consecutive
> runs, plus a browser walk against the real `nousresearch/hermes-agent` checkout: ten routes audited,
> a keyboard-only pass, and contrast computed from the live values in both themes.
>
> 🔴 **The gate found three defects a green build could not**, each now fixed and each now covered:
> the configurator could **disable itself permanently** on a transport failure (the `finally` **P2-7**
> enumerates, lost in the port); a **200 of the wrong shape destroyed the whole page**, which is now a
> fourth failure state at the `load()` boundary rather than a crash; and a **control boundary at
> 1.83 : 1** in the shipped dark palette, below SC 1.4.11's floor.
>
> It also found two defects in the new tests themselves — a probe that corrupted a concurrently-served
> build, and a harness whose 90-slot port space let one test file assert against another's server.
> Both are recorded rather than quietly fixed, because an intermittent test is a defect with a
> reputation cost.

## 10. Product + Sales Operations + Frontend — Entitlement gating (9b)

- [x] 10.1 Map every console capability to the **plan name** (Free / Team / Business / Enterprise) and
      automation level that unlocks it. **No price value in git** — plans by name only.
- [x] 10.2 Render a gated capability as **gated with the unlocking plan named** — never hidden without
      explanation, never as an error (FR15).
- [x] 10.3 Read the same P7 entitlement facts the platform enforces, so the screen and the gate cannot
      disagree.
- [x] 10.4 Design the Free-tier console experience deliberately rather than letting it fall out of a
      denial.
- [x] 10.5 **Test:** a capability shown as available is not refused by the platform on use; a gated
      capability names the unlocking plan; gating on plan **and** automation level names the unmet
      condition.

> **§10 record — 2026-07-24.** 10.1–10.3 were already built on the account view: the capability→plan
> map in `lib/entitlements.ts`, and availability read from the **P7 entitlement rows the platform
> enforces with** rather than from that map.
>
> 🔴 **10.2 was only half true, and the missing half was a defect.** Gating rendered correctly on the
> account view and **nowhere else**. A tenant opening a capability outside their plan got the
> platform's 403 classified as `upstream` — rendered as *"The platform refused this request"*, an
> **error**, which is exactly what FR15 forbids. Nothing is broken when a capability is outside a plan;
> it is a commercial boundary, and a reader shown an error opens a support ticket while a reader shown
> the plan that unlocks it has a different conversation and has been told the truth.
>
> Fixed at the boundary: 403 is now its own `gated` failure class, carrying the platform's own
> `DenialView` — feature label, reason, and the plan name that unlocks it — rendered in the accent
> rather than a hazard hue, saying in as many words that this is *a plan boundary rather than an
> error*, and linking to the account view. 🚫 The plan named on screen is the platform's, never one the
> console looked up: that is what makes the screen and the gate incapable of disagreeing.
>
> **10.4** — the Free-tier experience is deliberate rather than a fallout of denial: graph, run and
> configure carry `feature: null` (ungated on every plan including Free), so a Free tenant has a
> complete, working console rather than a wall of refusals, and the capabilities beyond it are listed
> with what unlocks them instead of hidden.

## 11. Frontend + Product — Diagnosis views and proposal review (9b, blocked upstream)

- [x] 11.1 Port the P4.5 attribution/diagnosis read models into the console once available.
      → P4.5 **is** mounted (`GET /api/p45/variants/{variant_id}/scorecard`), and the console renders it
      at `/app/variants/{id}/scorecard`: overall metrics, per-node attribution, failure clusters, the
      analyst's diagnoses and the ablations that were actually re-run. 🔴 A **diagnosis** and an
      **ablation** are rendered in separate sections with different emphasis, because a hypothesis
      styled like a measurement is how an unverified theory gets acted on.
- [x] 11.2 Build the **proposal-review surface** — queue → rationale → **verified delta** → full diff →
      approve / reject, in English — per inventory items **IDX-1 … IDX-5**. An unverified proposal is
      rendered as unverified and never in a form resembling verified evidence.
- [x] 11.3 ✅ **Unblocked — verified 2026-07-24.** The P5.5 API exists and is mounted:
      `GET /api/p55/workflows/{workflow_id}/surface` and
      `POST /api/p55/workflows/{workflow_id}/proposals/{proposal_id}/open-pr`
      (`internal/api/p55.go:145-146`). The gate this task set has been satisfied rather than waived —
      the surface ships **because** it has a backing endpoint, which is the whole point.

      ⚠️ **One deliberate divergence from the inventory, recorded rather than absorbed.** IDX-2 says
      *approve* and *reject*, written against the orphaned legacy page. The API P5.5 actually shipped
      exposes **open-PR**, gated on a passing verdict and Assisted automation, because P5.5 decided a
      human merges a reviewable pull request and the platform never merges for them. The console
      renders the verb the platform implements. 🚫 Whether an in-console approve/reject *should* exist
      is a P5.5 product question, not a console styling choice, and it is not answered here.

## 11b. Frontend — Host the P10 Prompt & Model Studio (9b, owned by P10)

> ✅ **UNBLOCKED — verified 2026-07-27.** P10 is implemented and mounted: `internal/api/p10.go`
> (`MountP10` — publish + timeline/diff/impact) and `internal/api/p10matrix.go` (`MountP10Matrix` —
> `GET /api/p10/models`, `/api/p10/workflows/{id}/nodes`, `/api/p10/workflows/{id}/bindings`,
> `POST /api/p10/studio/run`, `/api/p10/studio/bind`). The gate this block set is satisfied rather than
> waived — the studio ships **because** it has backing endpoints, which is the whole point. The studio
> surface itself landed under P10 (`web/console/src/app/app/studio/`), and this block is the record that
> it satisfies **P9's** rules: single token set, `en-US` strings, render-as-received, three data states,
> the accessibility floor, and 🔴 11b.3's honesty rule — each now an executable check.

The studio is a console surface, so its routes live in this shell — but its **requirements** belong to
[`../p10-prompt-model-studio/`](../p10-prompt-model-studio/) and are not duplicated here. P9's rules
govern it unchanged: single token set, English strings with pinned `en-US` formatting,
render-as-received, no credential in the browser, three distinct data states, the accessibility floor,
and browser-rendered acceptance.

- [x] 11b.1 Add the studio routes to the shell and navigation (§7.1): prompt browser, version timeline,
      version diff, editor, binding editor, preview + test-run, comparison, per-node selector.
      → `/app/studio` is in the shell rail and the command path (`app/layout.tsx`), and its one page
      composes every listed sub-surface as in-page tabs: the **matrix** (per-node selector · models ·
      test-run · bind), the **prompt library** (browser · version timeline · diff · editor/publish ·
      preview · side-by-side comparison · binding editor), and **bound nodes**
      (`studio/{studio,matrix,boundmode}.tsx`). `studio.test.mjs` asserts the nav entry (task 4.1).
- [x] 11b.2 Ensure the studio inherits the design system rather than introducing a fourth palette —
      an editor and a diff view are exactly where a page-local style set tends to appear (R1).
      → The studio renders through the shared primitives (`PageFrame`, `Section`, `Tabs`, `Card`,
      `Chip`, `Banner`) and no page-local palette. The R1 build gate `scan-tokens.mjs` walks all of
      `src/` — the studio's three component files included (90 files scanned, green) — so a colour /
      radius / type-size / duration literal in the editor or diff view **fails the build**, not review.
- [x] 11b.3 🔴 Enforce P10's honesty rule in this shell: **no score, rank, winner, or confidence
      interval** may render in a studio result, and no promotion path may exist from one. It is a
      failing test here as well as in P10.
      → `studio.test.mjs` sweeps `studio.tsx` and `matrix.tsx` line by line: any of
      *score / winner / confidence interval / rank / best / promote* on a non-negating line is a
      **failing test** (tasks 6.2, M6.1). The only cell distinction is *"in force — unverified"*, which
      the test also pins as "not a proof". The page lede states *no score, no winner, no promotion path*.
- [x] 11b.4 Render prompt bodies as **text, never markup** (R7) — they are customer content and arrive
      from the registry.
      → No `dangerouslySetInnerHTML` on the studio surface — asserted by `studio.test.mjs` for both
      `studio.tsx` and `matrix.tsx`, and by the repo-wide `scan-markup.mjs` build gate (89 files, green).
      🔎 **Found while landing this under P9:** the studio's in-scope-symbol parse did
      `catch { return [] }`, collapsing *"the nodes payload could not be read"* into *"there are no
      symbols in scope"* — the unknown-vs-empty lie `security.test.mjs` forbids across `src/`. Fixed to
      return `null` (scope **unknown**, still degrading to the free-text input), so the guard passes on
      the truth rather than a contortion.
- [x] 11b.5 Extend the entitlement mapping (§10.1) to cover studio capabilities.
      → A `studio` capability row in `lib/entitlements.ts`, listed on the account view like every other.
      🔴 It maps `feature: null` **deliberately**: P10 (`p10.go`, `p10matrix.go`, and the P10 spec)
      enforces **no** entitlement on the studio, so claiming a plan boundary would make the screen and
      the gate disagree — the one failure this file exists to prevent. The commercial boundary is not
      lost, it moves downstream: studio exploration is ungated; *shipping* a verified change is the
      already-gated `open-pr` path (Business, assisted). `studio.test.mjs` asserts the row exists **and**
      that its feature is `null`, not a plan feature — so a future edit that invents an unenforced gate
      turns red. If P10 ever gates the studio, this row maps to that feature, not before.

## 11c. Frontend — Surfaces owned by the distribution phases (9b)

> ✅ **UNBLOCKED — verified 2026-07-27.** Both read models exist and are mounted. P11 exposes a
> **coverage** read model — `LinkIngestSource.Coverage` (`internal/api/p11.go`), joined into the P7
> billing view as `link_coverage` so the SUM figure carries how complete it is (FR17), not a bare
> number. P12 exposes the **delivery-state** read model — `GET /api/p12/deliveries` returning a
> `DeliveriesView` of `DeliveryView` rows plus a `RouteConditionView` (`internal/api/p12.go`,
> `MountP12`). The block set the right gate: 11c.1 was **not** approximated — the fraction is the
> platform's own (`runs_linked` / `runs_reported`), and *unknown* is a distinct third state the console
> never renders as full.

Like §11b, these live in this shell but their **requirements** belong to their own phases and are
not duplicated here.

- [x] 11c.1 **Link coverage** ([P11](../p11-cli-ci-integration/)) — display how much of a customer's
      activity is linked, **wherever a spend figure derived from linked runs is shown**. It is not a
      footnote: a figure reflecting a fraction of activity, shown without saying so, is what a billing
      dispute is made of. Complete coverage and *unknown* coverage must render distinguishably.
      → `components/linkCoverage.tsx` sits **beside** SUM on the account view (`account/page.tsx`), not
      in a footnote, and renders **three** distinct states — `complete` (all reported runs linked),
      `partial` (`N of M`, "unlinked runs contribute nothing and are never estimated"), and `unknown`
      (no run count reported — a dashed bar, never 100%). `link-coverage.test.mjs` renders all three
      against a stub and asserts unknown is never collapsed into complete.
- [x] 11c.2 **Delivery state** ([P12](../p12-forge-delivery/)) — show each delivery as **open /
      merged / closed / superseded**, linked to the proposal that produced it, so the loop from
      proposal to outcome is visible.
      → `app/delivery/page.tsx` renders each `DeliveryView` with the `Status` primitive
      (opened→*open*, merged, superseded, closed, reverted — each a distinct word, not colour alone)
      and a one-click **"Open evidence"** link to `proposal_ref`. `delivery.test.mjs` renders the four
      states against a stub and asserts the proposal link and the merged row's merge commit.
- [x] 11c.3 **No delivery route** and **degraded / revoked** render as **conditions with a next
      action**, 🚫 never as empty lists — an empty list is the rendering that makes an invisible
      failure look normal.
      → `RouteConditionBanner` renders `no_route` / `degraded` / `revoked` as a titled condition with
      the platform's `next_action` and the line *"a reported condition, not an error and not an empty
      result"*; with no configured route the deliveries area shows that condition, not the
      configured-route empty copy. `delivery.test.mjs` asserts each condition is distinct, carries its
      next action, and that a **503** surfaces as *not-mounted* rather than "no deliveries" (the R5
      distinction, two hops).
- [x] 11c.4 Resolve the CLI-emitted run reference to a canonical console route (§7.4), so a URL
      pasted from a terminal into a pull request opens exactly that run.
      → The CLI/linkingest emits `https://heros-agent.space/app/runs/{run_id}`
      (`internal/linkingest/linkingest.go`, pinned `runlink.PlatformBaseURL`), whose path is the
      console's canonical `routes.run(id)`. Pinned from **both** sides so neither can drift silently:
      Go `TestConsoleRoute_IsThePlatformCanonicalRunPath` asserts the default emitted URL; and
      `routes.test.mjs` asserts `routes.run` is exactly `/app/runs/{id}` **and** renders the pasted
      path to prove it opens that run's subject page, never the picker.

## 12. Cutover — remove the legacy pages (9b, gated, owned, dated)

- [x] 12.1 For each of `p2.html`, `p25monitor.html`, `p35graph.html`, `p4board.html`: confirm its
      canonical route exists and every inventory item is checked or explicitly dropped. Record the
      result in the inventory's cutover table.
- [x] 12.2 Remove each HTML file **together with its Go handler and `go:embed` directive**
      (`internal/api/{p2,monitor,p35,p4}.go`), so no route is left serving a stale asset.
- [x] 12.3 Decide the disposition of `internal/api/static/index.html` once its successor ships: it has no
      handler and no backing endpoints, so removal is safe — but it is a **deletion**, so it takes
      explicit sign-off, not a drive-by.
      → **Deleted.** Its successor shipped (§11.2, on the mounted P5.5 API), and the sign-off is the one
      recorded in §1.2 rather than assumed here. Its three endpoints never existed, it had no handler,
      and its UI was Chinese-only; the shape it contributed lives on as **IDX-1…5** in the inventory.
- [x] 12.4 Update the `cmd/*demo` entrypoints and any documentation that references the removed routes.
- [x] 12.5 **Test:** no removed route responds; no documentation links to one.

## 13. Documentation

- [ ] 13.1 Fold the three P9 capability specs into `openspec/specs/` when the change deploys, per
      [`openspec/AGENTS.md`](../../AGENTS.md).
      ⏸️ **Correctly deferred, not skipped.** `openspec/specs/` **does not exist**: no phase in this
      repository has folded yet, because the convention folds a change's delta specs *when the change
      deploys*. Folding P9 alone would create the directory with one phase in it and imply the other
      twelve had been superseded. The trigger is deployment, and it has not happened.
- [x] 13.2 Record the §2.1 packaging ADR and the §2.2 type-generation decision under
      [`docs/adr/`](../../../docs/adr/).
- [x] 13.3 Update the root [`README.md`](../../../README.md) "Getting started" once the console is a
      runnable component, including how to run it without a platform credential in the browser.
- [x] 13.4 Extend [`web/design-system/README.md`](../../../web/design-system/README.md) with the craft
      layer both consoles now share — the elevation and motion meanings, the editorial type roles, and
      🔴 the **confidence reservation** written beside the existing hazard reservation, since the two
      rules are the same rule pointed at different risks.

---

## Run record — the real `nousresearch/hermes-agent`

The whole console was driven in a browser against a real checkout of
[`github.com/NousResearch/hermes-agent`](https://github.com/nousresearch/hermes-agent):
**[`hermes-run.md`](hermes-run.md)**. The screen agreed with the API field for field (40 nodes,
0 edges, 0 LLM calls, 40 regions not yet classified), the hierarchy was **measured** at 36px › 18px ›
12px, five genuinely-unmounted subsystems each carried the platform's own message across two hops, and
the theme survived end to end.

🔴 Four defects were found by looking that no build could see — a theme control that silently returned
the reader to `/`, a control boundary at 1.83 : 1, a configurator that could disable itself
permanently, and a 200 of the wrong shape destroying the whole page. All fixed, all now covered.

---

## Verification record — 2026-07-24

**Machine-checked, green.**

| Gate | Result |
|---|---|
| `web/console` build (token · string · markup · claim scans, then `next build`, then bundle scan) | pass |
| `web/console` `npm test` — security, design-system, routes, public-surface | **68 / 68** |
| `go build ./...`, `go vet ./...` | pass |
| `go test ./internal/api ./cmd/consoletypes` — readiness aggregation, type-contract drift gate | pass |
| `schemas/validate.py` — including the generated `console-view.schema.json` | pass |

**Rendered-browser acceptance (R11), against `cmd/proof/customerconsole` over a real
`github.com/NousResearch/hermes-agent` checkout.** Four properties were verified by looking, and three
of them found defects a green build could not:

1. **The public home page renders for an anonymous visitor** with no session and no upstream call.
2. **A tenant route fails closed** — `/app/…` without a session redirected to sign-in rather than
   rendering a shell that then failed its requests.
3. **The real graph renders**: 40 call sites discovered from the actual repository, `0 edges`,
   `0 LLM calls — fully rule-covered`, 40 regions not yet classified. The screen agreed with the API
   response field for field.
4. **The failure taxonomy survives two hops**: the eval board rendered *"This subsystem is not mounted
   on this deployment"* carrying the platform's own message, visually distinct from not-found.

**Three defects found by rendering, each invisible to the build:**

| Defect | Why no check could see it |
|---|---|
| The session store was a module-level `Map`. Next's dev server compiles route handlers and pages into separate module graphs, so sign-in wrote to a different map from the one pages read — the console signed you in and immediately said your session had ended. | Every test under `next start` passed; the production module graph is shared. Fixed by anchoring the store to `globalThis` (one store per **process**, which is what the design meant). |
| `Response.redirect()` is immutable, so Next could not attach the `Set-Cookie` — the browser got a redirect with no session. | The handler returned a correct-looking 303. Fixed by setting the cookie on a `NextResponse`. |
| The redirect target was built from `request.url`, which Next normalises to `localhost` while the user is on `127.0.0.1`. Different origins, so the console's own `form-action 'self'` policy **refused its own sign-in navigation**. The button did nothing, silently. | Server-side the response was perfect. Fixed by issuing **relative** `Location` headers (`src/lib/redirect.ts`), which is same-origin by construction and removes the open-redirect surface entirely. |

A fourth was found in `cmd/proof/customerconsole` itself: leaving a subsystem unmounted leaves its route
unregistered, so the mux answers **404** and the console truthfully renders *"No such workflow"* for a
workflow that plainly exists. Mounting the routes with a **nil source** produces the honest **503
not-mounted**. That is the R5 distinction collapsing in the one place nobody would have looked.
