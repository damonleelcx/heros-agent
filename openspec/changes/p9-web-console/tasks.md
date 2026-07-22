# Tasks — P9: Web Console

Two waves. **Wave 9a** = the credential boundary + the app shell + the design system + parity ports of
the four live surfaces. **Wave 9b** = the product layer (entitlement gating, diagnosis views, the
proposal-review surface once P5.5 exists) and the cutover.

**Standing constraints for every task below.**
No new platform endpoint, table, queue, or statistic — the console renders read models that already
exist. Nothing under [`internal/api/static/`](../../../internal/api/static/) is modified or deleted
before §11. Acceptance for any user-visible behavior is **rendered-browser evidence**, never a green
build. Every behavior in [`feature-inventory.md`](feature-inventory.md) is either present or recorded
there as deliberately dropped with a reason.

---

## 1. Product + Frontend — Inventory and rules first (9a, blocks everything else)

- [ ] 1.1 Ratify [`feature-inventory.md`](feature-inventory.md) against the five source files and the Go
      read models (`internal/evalboard/view.go`, `internal/patternclassifier` `GraphView`,
      `internal/telemetry` `RunMonitor`, and the P2 view types in `internal/api/p2.go`). Anything the
      inventory missed is added **before** any component is written.
- [ ] 1.2 Ratify [`ui-ux-plan.md`](ui-ux-plan.md) R1–R12 with the product owner, in particular the two
      deliberate drops (`p4board`'s hardcoded `'wf-demo'` default, and the legacy page's Chinese
      strings / unbounded 15 s polling / `alert()` error path). **Removing capability is a product
      decision** — it is signed off here, not assumed.
- [ ] 1.3 Produce the **surface-or-drop decision list** for every read-model field currently unrendered
      (R12): `spend.budget`, `DimensionView.uncovered[]`, `ComponentView.raw_ci_low`/`raw_ci_high`/
      `unit`, `judge.percent_agreement`/`floor`, `coverage.low_confidence`, `progress.seed_floor`,
      `gate_set`, `Row.variant_id`, `ParetoPoint.composite`, `spend.eval_run_id`,
      `ViewNode.symbol`/`policy`/`tools`, `RunMonitor.config_hash`, and `state === 'complete'`. Each
      gets an owner phase and a decision.
- [ ] 1.4 Define the console's information architecture: the shell, the navigation set, and the
      canonical route for each subject, so every legacy entry point has a target (FR11).

## 2. System Designer + DevOps — Decide the two one-way doors (9a)

- [ ] 2.1 Record an **ADR for deploy packaging** (one container under a supervisor vs. two containers in
      one unit), satisfying `design.md` Decision 6's requirements: declared, supervised, health-checked,
      readiness-aggregated, pinned runtime, lockfile-reproducible deps.
- [ ] 2.2 Decide the **type-generation toolchain** (`design.md` D5 / PRD §14 Q3). Preference: emit JSON
      Schema from the Go view types and generate TypeScript from that, composing with the existing
      [`schemas/`](../../../schemas/) discipline. Record the decision.
- [ ] 2.3 Confirm with P7 the **tenant-identity binding** the session exchange authenticates against
      (PRD §14 Q1). P9 designs against an abstract authenticated tenant principal and must not pre-empt
      the P7 mechanism.

## 3. Backend — Session and credential boundary (9a, the security-critical work)

- [ ] 3.1 Implement session issuance against P7 tenant identity: server-side session bound to a tenant,
      bounded lifetime, revocable. Set the browser cookie `HttpOnly` + `SameSite`.
- [ ] 3.2 Hold the platform API key in the BFF process environment only. Gate the build so key material
      cannot appear in the shipped client bundle.
- [ ] 3.3 Make every console data route **fail closed**: an unauthenticated request redirects to
      sign-in and is never served a shell that then fails its data requests.
- [ ] 3.4 Deny an expired or revoked session at the **next** request, with no grace period and **no
      silent retry using the server-held credential**.
- [ ] 3.5 Derive request scope **server-side from the session's tenant**. A client-supplied tenant
      identifier must not widen, change, or override it.
- [ ] 3.6 Implement the upstream forwarder as a **pass-through**: no merging of responses, no
      re-ranking, no reformatting, no status translation, no business rules.
- [ ] 3.7 Preserve the three-way failure taxonomy end to end — **503 not-mounted**, **404 not-found**,
      **transport failure** — carrying the upstream error body where one exists. Never map a 404 to an
      empty successful result.
- [ ] 3.8 Give every upstream call an **explicit timeout**; a hung upstream surfaces as a transport
      failure, never as an unbounded loading state.
- [ ] 3.9 Proxy the run-monitor **SSE** stream with flush semantics preserved, closing the client stream
      when upstream closes, without batching events.
- [ ] 3.10 Emit structured logs and traces correlated with the platform `trace_id`, carrying **no**
      prompt text, diff content, or credential — on the error path as well as the success path.
- [ ] 3.11 **Tests (security assertions, not review items):** no key material in the shipped bundle; an
      unauthenticated data route redirects rather than rendering; a revoked session is denied at the next
      request; a client-supplied tenant id cannot widen scope; a 404 is not returned as an empty result;
      a hung upstream produces a transport failure within the timeout.

## 4. Backend + DevOps — Readiness and operability (9a)

- [ ] 4.1 Aggregate the console component into the platform readiness signal in
      `internal/api/server.go`: a healthy platform service with an unreachable console **does not report
      ready**, and the degraded component is **named** on a machine-readable endpoint.
- [ ] 4.2 Ship the console as one declared, supervised, health-checked component per the §2.1 ADR, with
      a pinned runtime version and a lockfile-reproducible dependency tree.
- [ ] 4.3 Source all secrets from the environment or secret store — never the repository, never a log,
      never a trace attribute, never the client bundle.
- [ ] 4.4 **Test:** readiness reports not-ready and names the console when the console is unreachable.

## 5. Frontend — Design system substrate (9a, before any page)

- [ ] 5.1 Build the **single token set** from `design.md` Decision 2's reconciliation table, including
      the promoted domain tokens (graph `--llm`/`--ctl`/`--none`, chart `--series-1..4`) and both names
      where two concepts currently share a value.
- [ ] 5.2 Add the **build gate** that fails on a color / border-radius / font-family literal outside the
      token definition (R1).
- [ ] 5.3 Build the status primitive: **distinct color and distinct word**, a **defined fallback for
      unmodelled values that displays the raw value**, and no styling that lets an unknown status
      impersonate a known one (R3).
- [ ] 5.4 Build the loading / empty / error primitives as **three distinct renderings**, with the three
      error classes carrying distinct copy and the surrounding controls preserved on error (R5).
- [ ] 5.5 Build the `en-US` locale swap point — a single function every formatter resolves through —
      and the scan that fails on non-ASCII user-facing string literals and on any formatter that
      bypasses it (R4).
- [ ] 5.6 Establish the accessibility primitives: focus-visible treatment, scoped table headers, the
      chart text-alternative and tabular-fallback pattern, and the focus-reachable tooltip pattern (R6).
      Take `p4board.html` as the reference — it already implements all of these.
- [ ] 5.7 Ban raw-markup rendering in lint, with an explicitly reviewed allowlist (R7).
- [ ] 5.8 **Test:** an unmodelled status renders the fallback **and** its raw value; a value containing
      markup renders as literal text; a non-English browser locale still produces `en-US` formatting.

## 6. Frontend + System Designer — Typed contract (9a)

- [ ] 6.1 Generate TypeScript types from the Go view structs per the §2.2 decision; check the generated
      artifact in.
- [ ] 6.2 Add the **CI drift gate**: regenerate and fail the build on a diff, so a Go read-model change
      cannot reach the browser as `undefined`.
- [ ] 6.3 **Test:** renaming a Go view field fails the build rather than producing a blank cell.

## 7. Frontend + Product — App shell, selection, canonical routes (9a)

- [ ] 7.1 Build the shell and navigation reaching every surface; no surface reachable only by URL (FR9).
- [ ] 7.2 Build **subject selection** from platform data for workflow / run / variant / board /
      transform. Direct identifier entry stays as an accelerator, not the only path (FR10, R8).
- [ ] 7.3 Ensure **no route substitutes a default subject**. The eval board opened with no workflow shows
      selection or empty state — the `'wf-demo'` default is not ported (FR10, R8).
- [ ] 7.4 Implement the canonical routes from §1.4 so every legacy entry point resolves to exactly its
      subject, and a shared link opens the same subject for its recipient (FR11, R9).
- [ ] 7.5 Add cross-surface navigation: graph → run → transform → board without URL editing.
- [ ] 7.6 **Test:** every route opened with no parameters renders selection or empty state, never
      populated data; every legacy entry point resolves to its canonical route.

## 8. Frontend — Port the four live surfaces (9a, parity against the inventory)

Each sub-task below ships only when its inventory section is fully checked.

- [ ] 8.1 **Configure / diff / run** (`p2.html` → canonical routes). Inventory items **P2-1 … P2-26**.
      Load-bearing details: the *nothing was persisted* message appears **on HTTP 400 only**;
      `requires_human_review` is **read from the API, never recomputed**; a watch always stops the
      previous watch; only the first load shows a loading state.
- [ ] 8.2 **Live run monitor** (`p25monitor.html`). Inventory items **P25-1 … P25-11**. Load-bearing:
      **SSE first**, polling fallback engaging **only if no message ever arrived**; per-row state carried
      by chip **and** row marker; status-dependent empty copy.
- [ ] 8.3 **Pattern-classified graph** (`p35graph.html`). Inventory items **P35-1 … P35-19**.
      Load-bearing: deterministic layout from `layer`/`order`; **back edges routed under the row**;
      edge kind carried by **dash and marker**; container **scrolls, never shrinks**; an error hides the
      graph, label **and** diagnostics cards together; the *unlabelled ≠ no pattern* copy survives
      verbatim in meaning. Add the text alternative the SVG currently lacks entirely (R6).
- [ ] 8.4 **Eval board** (`p4board.html`). Inventory items **P4-1 … P4-44**. Load-bearing:
      **virtualization above 60 rows** with its explanatory footer; **keyboard row navigation with
      wrap-around**; the Pareto tooltip bound to **focus as well as hover**; the `<details>` tabular
      fallback; frontier membership by **shape**; disqualified variants in their **own section**, not
      ranked last; a tied rank rendered de-emphasized; the residual framing sentence's meaning
      preserved; the budget-cap banner presenting a stop as **correct behavior**.
- [ ] 8.5 Enforce **render-as-received** across all four: no client computation of score / CI / tie /
      rank / gate / dominance / coverage, no rounding before comparison, no client re-sort of a
      server-ranked field (FR14).
- [ ] 8.6 Surface judge-calibration and weak-eval-set flags **wherever the affected metric appears**.
- [ ] 8.7 Implement the §1.3 surface-or-drop decisions that landed on "surface".

## 9. QA — The acceptance gate (9a, runs alongside §8)

- [ ] 9.1 Turn [`feature-inventory.md`](feature-inventory.md) into an executable regression suite — one
      case per item, so "no feature loss" can **fail** rather than be asserted in a PR description.
- [ ] 9.2 Named cases for the behaviors most likely to be dropped silently: SSE fallback condition,
      poll-termination source, virtualization threshold, keyboard wrap-around, chart tabular fallback,
      the 400-only persistence message, and the graph's combined error hide.
- [ ] 9.3 Per-view state matrix: loading / empty / error / populated, with the three error classes each
      asserted to produce distinct copy.
- [ ] 9.4 **Browser-rendered acceptance** for every user-visible behavior: navigate → await the data
      request → read the rendered structure → inspect the network response → screenshot → assert the
      screen agrees with the response. Fixed viewport for reproducibility; bounded image dimensions.
- [ ] 9.5 Degradation runs: subsystems unmounted (503), missing subject (404), platform unreachable
      (transport), and **SSE forcibly disabled**.
- [ ] 9.6 Accessibility: automated audit **plus a keyboard-only pass** per page. A page below the
      `p4board` level does not ship.
- [ ] 9.7 Confirm no page renders a raw identifier as markup and no page displays a non-English string.

## 10. Product + Sales Operations + Frontend — Entitlement gating (9b)

- [ ] 10.1 Map every console capability to the **plan name** (Free / Team / Business / Enterprise) and
      automation level that unlocks it. **No price value in git** — plans by name only.
- [ ] 10.2 Render a gated capability as **gated with the unlocking plan named** — never hidden without
      explanation, never as an error (FR15).
- [ ] 10.3 Read the same P7 entitlement facts the platform enforces, so the screen and the gate cannot
      disagree.
- [ ] 10.4 Design the Free-tier console experience deliberately rather than letting it fall out of a
      denial.
- [ ] 10.5 **Test:** a capability shown as available is not refused by the platform on use; a gated
      capability names the unlocking plan; gating on plan **and** automation level names the unmet
      condition.

## 11. Frontend + Product — Diagnosis views and proposal review (9b, blocked upstream)

- [ ] 11.1 Port the P4.5 attribution/diagnosis read models into the console once available.
- [ ] 11.2 Build the **proposal-review surface** — queue → rationale → **verified delta** → full diff →
      approve / reject, in English — per inventory items **IDX-1 … IDX-5**. An unverified proposal is
      rendered as unverified and never in a form resembling verified evidence.
- [ ] 11.3 🚧 **Blocked:** this surface does not merge into a shipping build until the **P5.5** proposal
      API exists. A surface with no backing endpoint is exactly how `internal/api/static/index.html`
      became an orphan.

## 11b. Frontend — Host the P10 Prompt & Model Studio (9b, owned by P10)

The studio is a console surface, so its routes live in this shell — but its **requirements** belong to
[`../p10-prompt-model-studio/`](../p10-prompt-model-studio/) and are not duplicated here. P9's rules
govern it unchanged: single token set, English strings with pinned `en-US` formatting,
render-as-received, no credential in the browser, three distinct data states, the accessibility floor,
and browser-rendered acceptance.

- [ ] 11b.1 Add the studio routes to the shell and navigation (§7.1): prompt browser, version timeline,
      version diff, editor, binding editor, preview + test-run, comparison, per-node selector.
- [ ] 11b.2 Ensure the studio inherits the design system rather than introducing a fourth palette —
      an editor and a diff view are exactly where a page-local style set tends to appear (R1).
- [ ] 11b.3 🔴 Enforce P10's honesty rule in this shell: **no score, rank, winner, or confidence
      interval** may render in a studio result, and no promotion path may exist from one. It is a
      failing test here as well as in P10.
- [ ] 11b.4 Render prompt bodies as **text, never markup** (R7) — they are customer content and arrive
      from the registry.
- [ ] 11b.5 Extend the entitlement mapping (§10.1) to cover studio capabilities.

## 11c. Frontend — Surfaces owned by the distribution phases (9b)

Like §11b, these live in this shell but their **requirements** belong to their own phases and are
not duplicated here.

- [ ] 11c.1 **Link coverage** ([P11](../p11-cli-ci-integration/)) — display how much of a customer's
      activity is linked, **wherever a spend figure derived from linked runs is shown**. It is not a
      footnote: a figure reflecting a fraction of activity, shown without saying so, is what a billing
      dispute is made of. Complete coverage and *unknown* coverage must render distinguishably.
- [ ] 11c.2 **Delivery state** ([P12](../p12-forge-delivery/)) — show each delivery as **open /
      merged / closed / superseded**, linked to the proposal that produced it, so the loop from
      proposal to outcome is visible.
- [ ] 11c.3 **No delivery route** and **degraded / revoked** render as **conditions with a next
      action**, 🚫 never as empty lists — an empty list is the rendering that makes an invisible
      failure look normal.
- [ ] 11c.4 Resolve the CLI-emitted run reference to a canonical console route (§7.4), so a URL
      pasted from a terminal into a pull request opens exactly that run.

## 12. Cutover — remove the legacy pages (9b, gated, owned, dated)

- [ ] 12.1 For each of `p2.html`, `p25monitor.html`, `p35graph.html`, `p4board.html`: confirm its
      canonical route exists and every inventory item is checked or explicitly dropped. Record the
      result in the inventory's cutover table.
- [ ] 12.2 Remove each HTML file **together with its Go handler and `go:embed` directive**
      (`internal/api/{p2,monitor,p35,p4}.go`), so no route is left serving a stale asset.
- [ ] 12.3 Decide the disposition of `internal/api/static/index.html` once its successor ships: it has no
      handler and no backing endpoints, so removal is safe — but it is a **deletion**, so it takes
      explicit sign-off, not a drive-by.
- [ ] 12.4 Update the `cmd/*demo` entrypoints and any documentation that references the removed routes.
- [ ] 12.5 **Test:** no removed route responds; no documentation links to one.

## 13. Documentation

- [ ] 13.1 Fold the three P9 capability specs into `openspec/specs/` when the change deploys, per
      [`openspec/AGENTS.md`](../../AGENTS.md).
- [ ] 13.2 Record the §2.1 packaging ADR and the §2.2 type-generation decision under
      [`docs/adr/`](../../../docs/adr/).
- [ ] 13.3 Update the root [`README.md`](../../../README.md) "Getting started" once the console is a
      runnable component, including how to run it without a platform credential in the browser.
