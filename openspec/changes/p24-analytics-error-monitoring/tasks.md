# P24 — Tasks

Ordered by wave. **Wave 24a lands every fence before any tool is installed** — installing a tool first
is how the fence ends up shaped around the tool. Each task is independently verifiable, and a
completion claim must point at an assertion that exists and runs.

## 1. Wave 24a — the fence, with nothing installed

- [ ] 1.1 Add the shared policy artefact under `web/design-system/` — consent categories, the origin
      allowlist (**empty** at this point), per-prefix third-party rules, the closed surface enum, and
      the per-origin transfer budgets. Data only; no origin yet.
- [ ] 1.2 Rewrite both `middleware.ts` files to **construct** the CSP from the artefact instead of a
      literal. Byte-identical output to today, asserted by a test that compares the built header for
      every prefix against the current shipped header.
- [ ] 1.3 Add a build gate that fails on a hard-coded `https://` origin in either `middleware.ts` or
      either `next.config.mjs`. Demonstrate it red.
- [ ] 1.4 Add **per-prefix** CSP assertions: `/app/**`, `/api/**` and every operator route contain
      `default-src 'self'` and no third-party origin; the public prefix contains only origins from the
      artefact. Replaces the two global `doesNotMatch(/https?:\/\//)` assertions with narrower ones that
      name the requirement they defend.
- [ ] 1.5 Extend both `scan-bundle.mjs` with the inverse runtime scan: an analytics, replay or
      error-reporting runtime in a client chunk reachable from a tenant or operator route fails the
      build, naming the runtime and the chunk. Demonstrate it red with a fixture import.
- [ ] 1.6 Add the per-origin transfer budget to `web/console/scripts/accept.mjs`: measure third-party
      transferred bytes per origin in a real browser and fail with the origin and the overage named.
      With an empty allowlist the expected measurement is **0 bytes from 0 origins**.
- [ ] 1.7 Add the drift check between the two consoles' derived policies for shared prefixes, in the
      shape of the existing web-drift checks.
- [ ] 1.8 Add the air-gapped assertion to the P20/P19 package build: the `airgapped` artefact
      references **zero** external origins — no script host, no ingest host, no font, no image host.
      Fails the package build, not the install. Demonstrate it red.
- [ ] 1.9 Record the decision in `web/design-system/trend-ledger.md`: what was accepted (analytics and
      replay on the public prefix, error reporting everywhere), what was refused (replay on tenant and
      operator surfaces, any browser tag on a tenant page, any tunnel through the BFF), and the budgets.
- [ ] 1.10 **Wave gate:** every fence in 1.3–1.8 has been demonstrated red, and `npm run build` plus
      `npm test` are green on both consoles with the allowlist empty.

## 2. Wave 24b — Sentry, server side

- [ ] 2.1 New Go package for the error-event boundary, modelled on `internal/runlink`: the
      `AllowlistField` table from design D5 with a one-line justification per field, and an event
      constructed field-by-field. Never serialize an error object.
- [ ] 2.2 Render the allowlist into a review doc under `docs/decisions/` **from the table**, so the doc
      cannot drift from the code.
- [ ] 2.3 Chain `telemetry.Scrubber` over the constructed event as the last stage before transmit.
- [ ] 2.4 Add `error.code` to the transmitted event from the central enum, and **drop the message body**
      for anything else. Assert that a message-shaped value that is not an enum value does not reach the
      wire.
- [ ] 2.5 Carry the existing `trace_id` from the request context; mint no new correlation identity.
      Assert the value equals the span's and the `X-Trace-Id` header's for the same request.
- [ ] 2.6 Implement the three-state `Reporter` (`absent` / `configured` / `degraded`) with the failure
      class named on `degraded`; wire it into the existing readiness surface.
- [ ] 2.7 Make it fail-static: out-of-band transmit, no unbounded retry queue, no panic, no request
      failure, no per-event log line — one WARN per interval with the failure class.
- [ ] 2.8 Turn performance tracing and profiling **off** explicitly, not by omission. Assert no
      transaction or profile payload is constructed.
- [ ] 2.9 Set explicit sample rate, per-issue rate limit and transmit budget as named constants with a
      stated basis.
- [ ] 2.10 Initialise in `cmd/agentd` and the admin API. With no DSN: no transmit, no warning,
      readiness `absent`.
- [ ] 2.11 **The load-bearing fence.** A forbidden-shape fixture — an `sk-…` key, an `AKIA…` id, an
      email, a 2 KB prompt, a unified diff, a `/app/variants/{id}` URL, a hostname, a tenant name —
      attached to an error every way an engineer could attach it (message, wrapped error, context value,
      struct field). Assert the **transmitted bytes** contain none of them, and that the transmitted key
      set is a subset of the allowlist. Runs against a local capture endpoint with **no** environment
      precondition.
- [ ] 2.12 Assert the allowlist in the other direction too: every entry is populated by something.
- [ ] 2.13 Live verification: a deliberate panic in a staging service produces an issue carrying
      `trace_id`, `release`, `surface`, `error.code` and frames; the `trace_id` resolves the span in the
      span store; the stored payload contains no forbidden shape.

## 3. Wave 24c — Sentry, browser

- [ ] 3.1 Browser reporting on the customer console, the operator console and the public surface,
      injected with the per-request nonce from `x-nonce`. Assert a script without the nonce does not run.
- [ ] 3.2 Disable breadcrumbs wholesale — fetch/XHR, navigation, console, DOM and click-target text —
      rather than filtering them. Assert no breadcrumb array reaches the wire.
- [ ] 3.3 Construct the browser event from the same allowlist; drop `event.message` unless it is an
      `error.code`; carry `surface` from the closed enum, never `location.href`.
- [ ] 3.4 Add the reporting origin to `connect-src` for every prefix via the artefact — the only
      third-party origin permitted on a tenant or operator prefix. Assert `script-src` gains **no** host
      on any prefix.
- [ ] 3.5 Handle unhandled errors, unhandled rejections, chunk-load failures and hydration failures.
- [ ] 3.6 Source-map upload for the platform's own hosted deployment only, in the P20 release pipeline,
      with a CI-only release-scoped auth token. Assert maps are **absent** from every installable package
      and never served from a customer-facing origin.
- [ ] 3.7 Browser verification: a deliberate throw on a tenant route produces an issue with frames and
      no breadcrumb URLs; the tenant CSP is unchanged except for the reporting origin under
      `connect-src`; dev tools show no other third-party request.

## 4. Wave 24d — consent

- [ ] 4.1 Consent state machine: four categories, `not-asked | granted | denied` each, non-essential
      default **denied**. Transitions only on an explicit user action or a material policy version —
      never on a navigation, a timer or a scroll.
- [ ] 4.2 First-party cookie carrying `{policy_version, per-category decision}`. **Not** the P23
      `consent-records` ledger; add a comment stating why, so the next reader does not "fix" it.
- [ ] 4.3 Banner component inside the existing layout, existing tokens only. `scan:tokens` must pass;
      decline carries the same visual weight as accept.
- [ ] 4.4 English strings through the single `en-US` swap point; three term-dictionary entries ("Usage
      analytics", "Session recording", "Error diagnostics") so the banner, the privacy document and the
      operator console share one vocabulary.
- [ ] 4.5 Store a refusal **as a refusal**. Assert no re-prompt across three navigations and a new
      session.
- [ ] 4.6 Withdrawal reachable from every page carrying a gated integration; effective on the next
      navigation with no sign-out. Assert collection stops.
- [ ] 4.7 Full function on decline: assert no content, control or route is conditioned on a grant.
- [ ] 4.8 Material policy version resets non-essential categories to `not-asked` and re-asks; a
      non-material version asks nobody.
- [ ] 4.9 No banner on the operator console. Add the corresponding paragraph to the internal
      acceptable-use notice, so the exception is stated rather than inferred.
- [ ] 4.10 Browser verification: decline → zero third-party requests and no non-essential cookie or
      storage entry; accept per category → exactly that category's origins.

## 5. Wave 24e — GA4 and Clarity on the public surface

- [ ] 5.1 Add the analytics and replay origins to the artefact with their consent categories, CSP
      directives and byte budgets.
- [ ] 5.2 Load each tag nonced, after first paint, only under its granted category, never blocking
      render. Assert the public surface still renders with the platform API stopped.
- [ ] 5.3 Configure GA4: IP anonymisation on, ad-personalisation signals off, no cross-site identifier,
      no advertising identifier, no remarketing audience, no conversion pixel. Assert the configuration
      rather than documenting it.
- [ ] 5.4 Configure Clarity with masking **on by default** — all text inputs and form fields masked.
      Any unmasking is a per-element opt-in with a recorded reason.
- [ ] 5.5 Assert the replay origin is absent from the tenant and operator prefixes' CSP, and that the
      replay script is unreachable from those layouts.
- [ ] 5.6 Public-surface funnel events: page view, section reach, install-page steps, sign-up steps —
      event names from the central enum. An ad-hoc name fails the build.
- [ ] 5.7 Verify each origin against its transfer budget in the acceptance run.

## 6. Wave 24f — server-side console analytics

- [ ] 6.1 Analytics-event allowlist (design D5, second table) with the same construct-not-serialize
      discipline and the same both-directions assertion.
- [ ] 6.2 `surface_viewed` emitted **server-side** from both BFF halves, `surface_id` from the closed
      enum. Assert a path, query string or free-text field cannot be carried.
- [ ] 6.3 Server-side relay to the analytics backend from the server process only.
- [ ] 6.4 Assert **no** browser request under `/app/**` or in the operator console leaves the own origin
      except the reporting origin under `connect-src`.
- [ ] 6.5 Assert the boundary in D8: no analytics figure is rendered on a customer-facing surface, used
      to derive an invoiced quantity, or presented as a platform metric.
- [ ] 6.6 Assert P0 golden `config_hash` vectors reproduce byte-identically — nothing in this phase
      enters a hashed structure.

## 7. Wave 24g — legal, disclosure and deployment defaults

- [ ] 7.1 Add a `sub-processors` document kind to the P23 legal manifest: processor, categories
      received, surfaces, jurisdiction. Publish version 1 as **material**.
- [ ] 7.2 Wire the material publication to consent invalidation (task 4.8) and verify the re-ask.
- [ ] 7.3 Update the privacy document and re-run `scan:claims`; fix any shipped claim the configuration
      now contradicts. Assert the fence fails on a stale "we run no third-party code" claim.
- [ ] 7.4 Compose, Kustomize base and every overlay: no measurement id, no project id, no DSN, and no
      discovered default. Assert absent.
- [ ] 7.5 Confirm the seven propagation layers all landed: shared artefact → both `middleware.ts` →
      both `next.config.mjs` → both `scan-bundle.mjs` → Go initialisation → deployment manifests →
      release pipeline → legal documents.
- [ ] 7.6 Sales-facing FAQ entries: *Do you record my screen? Can I turn it off? Does your on-prem
      install phone home? Can I get error reports from my own install?* — each answered with what
      shipped, and the last answered "no, and that is a self-hosted collector we have not built".

## 8. Exit

- [ ] 8.1 Walk the PRD §13 exit checklist end to end and record the evidence per item.
- [ ] 8.2 Confirm every fence in the change has been demonstrated red at least once by a deliberate
      violation, and that each of the four amended commitments in PRD §2.3 has a named regression test
      that fails if a future phase re-widens it.
- [ ] 8.3 Audit this task list against reality: for each `[x]`, name the assertion and confirm it exists
      and runs. A pointer is not evidence until it resolves.
