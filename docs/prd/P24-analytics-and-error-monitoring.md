# PRD — P24: Product Analytics & Error Monitoring

| | |
|---|---|
| **Status** | Proposed (docs-only; no code in this phase) |
| **Created** | 2026-07-30 |
| **Updated** | 2026-07-30 |
| **OpenSpec change** | [`p24-analytics-error-monitoring`](../../openspec/changes/archive/2026-08-01-p24-analytics-error-monitoring/) |
| **Lead role(s)** | Frontend + DevOps, with Product Designer on consent and System Designer on the egress posture |
| **Upstream** | P2.5 (telemetry substrate), P9 (customer console + public surface), P8 (operator console), P11 (egress allowlist), P19 (deployment substrates), P23 (legal surface + consent records) |
| **Numbering note** | There is no `P25`. The token `p25` already denotes **P2.5 — Metrics & Observability** in this repository (`/p25/monitor`, the Gantt id in `implementation-timeline/README.md`, and `internal/api/monitor.go`). Reusing it for a new phase would make `p25` ambiguous in exactly the places an operator greps during an incident. The operator-console phase that follows this one is **P26**. |

> **Language note.** This project is English-only in source, comments, UI strings and its own documents.
> The Chinese technical-design conventions this document is written against (section order, the
> five-step exposition of every decision, the four-element *design key points*, user-interaction
> narration before any diagram) are applied here **translated**, not transplanted.

---

## 1. Summary

This phase installs three third-party observability products — **Google Analytics 4**, **Microsoft
Clarity**, and **Sentry** — across the platform's own hosted deployment, and it does so under a
posture the platform already committed to in writing and then enforced with tests: *the console
requests no third-party origin, and a visitor is not tracked before they consent to anything.*

Those two commitments are not aspirations in this repository. They are a `default-src 'self'`
Content-Security-Policy set per request in
[`web/console/src/middleware.ts`](../../web/console/src/middleware.ts), whose own comment says an
analytics tag "does not render, it is REFUSED"; two live assertions that the shipped CSP contains no
`https://` origin at all ([`security.test.mjs:286`](../../web/console/tests/security.test.mjs),
[`public-surface.test.mjs:156`](../../web/console/tests/public-surface.test.mjs)); a shipped P9
requirement (FR35); and a shipped OpenSpec scenario. **A phase that installs analytics is therefore a
phase that modifies published requirements, and the honest way to do that is to say so on the first
page rather than to quietly widen a regex in a test.**

So this PRD is mostly about *where each of the three tools may run, and what each may carry*. The
short version:

| Surface | GA4 | Clarity | Sentry |
|---|---|---|---|
| Public surface (`/`, `/install`, `/signin`) — no session, no tenant data | ✅ consent-gated | ✅ consent-gated, masked by default | ✅ consent-gated |
| Customer console tenant surfaces (`/app/**`) | ⚠️ **server-side events only** — no browser tag, no third-party origin | 🚫 **Refused** | ✅ frames-and-codes only |
| Operator console (every route) | 🚫 Refused | 🚫 **Refused** | ✅ frames-and-codes only |
| Go services (`agentd`, admin API, both BFF server sides) | n/a | n/a | ✅ allowlist-constructed |
| `heros` CLI on a customer's machine | 🚫 Refused | 🚫 Refused | 🚫 **Refused** |
| Customer self-hosted / air-gapped deployment | 🚫 Absent | 🚫 Absent | 🚫 Absent |

The two refusals that carry this phase are worth stating plainly, because everything else follows
from them:

**Clarity is a session recorder, and the screen it would record is the customer's own source.** What
is on the screen under `/app/**` is prompt text, generated diffs, node identifiers and model
configuration; what is on the operator console's screen is cross-tenant aggregates, tenant names,
active impersonations and audit rows. Replaying those to a third party exports precisely the content
classes [`internal/runlink/allowlist.go`](../../internal/runlink/allowlist.go) was constructed to
keep in. That is a first-priority security cost bought with an eighth-priority convenience, so it is
refused without qualification — no plan, role, flag or request parameter turns it on.

**An error report is a message, and a message is where a leaked value ends up.** `failed to resolve
prompt "…"` is a Go error string and also an exfiltration path. So Sentry events are **constructed
field-by-field from an allowlist**, exactly as a linked run payload is, rather than serialized and
then scrubbed. What crosses: exception type, our own stack frames, the central `error.code`, the
`trace_id` that already exists, the release, the surface. What never crosses, on any path including
diagnostics: error message bodies, request bodies, breadcrumb URLs, prompt text, source, diffs,
environment values, credentials, hostnames, IP addresses.

---

## 2. Problem & context

### 2.1 What we cannot currently answer

The platform has an excellent record of **what its own machinery did** and almost none of **what a
human in front of it experienced**.

P2.5 gave us OpenTelemetry spans, a metric taxonomy, a cardinality budget and a scrubbing chokepoint.
Every run, node, invocation, eval and cost event is tagged and reproducible. If you ask "did the
resolver emit a mismatched `config_hash` on tenant X's run at 04:12", the substrate answers.

Ask any of these and the substrate is silent:

- A prospect landed on the public home page from a conference link. Did they read the boundary
  statement, or bounce at the fold? We have never known, because the page requests nothing.
- Eleven surfaces now live under `/app` — `configure`, `studio`, `coverage`, `wiring`, `context`,
  `memory`, `harness`, `delivery`, `authoring`, `transforms`, `runs`. Which do customers open twice
  and never return to? P13–P18 each shipped a surface; none shipped a way to learn whether it landed.
- The console's acceptance rule is a rendered browser, and it has caught real defects (a theme
  control that returned the reader to `/` because `Referrer-Policy: no-referrer` made `Referer`
  unavailable — every server-side check passed). Those were found by a person clicking. **A
  client-side exception in production today reaches nobody.** There is no browser error pipeline at
  all: no `window.onerror`, no unhandled-rejection handler, no report of a chunk that failed to load.
- A Go panic in `agentd` writes a structured log line to whatever collects stdout. Correlating three
  reports of "the runs page is blank" with one nil-map dereference is manual work across three
  systems, and the operator's entry point — the audit log and the readiness endpoint — does not
  mention it.

### 2.2 Why now

Three things converged.

**P20 shipped installable packages, so the audience is no longer known by name.** Until v0.20.0 the
people running this software were people we had spoken to. `curl | sh` on five OS images changes
that: the install page moved out from behind the login (P20 D3), and the first-run onboarding flow is
now walked by strangers whose failures we hear about only if they bother to tell us.

**P21 and P22 put a commercial funnel on the public surface.** A checkout that nobody can measure is
a checkout nobody can fix. When Stripe lands, "how many people reached checkout and stopped" becomes
a question with money attached.

**The error budget is being spent by the frontend, and we are not watching it.** The two consoles are
now 20-odd routes of React across two Next.js applications with a shared design system, a per-request
nonce CSP, and a hard payload ceiling. Every one of those is a mechanism that fails *invisibly* in a
browser: a CSP that refuses a script produces a page that renders and does nothing, `next build`
green.

### 2.3 The constraint that makes this phase interesting

Every other phase in this program added a capability to a system whose posture allowed it. **This one
proposes to relax a posture that is currently enforced by tests.** The relevant commitments, verified
in the tree today:

| Commitment | Where it lives | Verified |
|---|---|---|
| `default-src 'self'`, per-request nonce, no third-party origin | `web/console/src/middleware.ts`, `web/admin-console/src/middleware.ts` | Both consoles |
| The shipped CSP names no `https://` origin | `web/console/tests/security.test.mjs:286` | Live assertion |
| The public surface references no third-party origin | `web/console/tests/public-surface.test.mjs:151` | Live assertion |
| A visitor is not tracked before they consent to anything | `openspec/changes/p9-web-console/specs/console-marketing-site/spec.md` | Shipped scenario |
| Shipped client JS is under a 1,400,000-byte ceiling | `scan-bundle.mjs` (both consoles) | Build gate |
| The CLI's upgrade path carries no telemetry, analytics, machine id or hostname | `internal/clilink/upgrade_test.go:264` | Live assertion |
| Content never crosses the P11 boundary; the payload is constructed from an allowlist | `internal/runlink/allowlist.go` | Package-level guarantee |
| Secrets, prompts, completions and PII are scrubbed at one chokepoint | `internal/telemetry/scrub.go` | Every event and span |

Nothing in this phase weakens the last three. The first four are amended **narrowly, by route prefix,
and out loud** — and the fifth is *strengthened*, because as written it would not notice this phase at
all (§8.2 D9).

---

## 3. Goals & non-goals

### Goals

- **G1 — Know what happens on the public surface.** Page views, section reach, the install-page
  funnel, the sign-up funnel, and (when P21 lands) the checkout funnel, on a surface that holds no
  session and reads no tenant data.
- **G2 — See the public surface through a visitor's eyes.** Clarity heatmaps and session replay on
  the public surface only, with masking on by default rather than configured on.
- **G3 — Learn which console surfaces are used, without shipping a tracker to a tenant page.**
  Surface-level usage for both consoles, emitted **server-side** from the BFF as a first-party event,
  relayed onward by the server. The browser on a tenant page talks to its own origin and nothing else.
- **G4 — See a browser exception.** Unhandled errors, unhandled rejections, chunk-load failures and
  hydration failures reach an inbox that is watched, on all three web surfaces, carrying enough to fix
  the defect and nothing more.
- **G5 — See a server exception with its trace.** Go panics and handled-but-unexpected errors in
  `agentd`, the admin API and both BFF server halves, carrying the **same** `trace_id` the span store
  and the `X-Trace-Id` response header already use, so one incident is one identity across three
  systems.
- **G6 — Make absence a first-class, configured state.** A deployment with no analytics and no error
  reporting is a supported, tested configuration — not a degraded one — and it is the **default**
  everywhere except our own hosted deployment.
- **G7 — Ask permission, and remember a refusal.** Consent is per category, default-off, withdrawable,
  and a refusal is stored as a refusal rather than as an absence that re-prompts forever.
- **G8 — Leave the guards stronger than we found them.** The third-party-origin rule stops being
  "no origins" and becomes "exactly these origins, on exactly these route prefixes, under a transfer
  budget" — a narrower, checkable statement rather than a relaxed one.

### Non-goals (explicitly deferred, with the phase that owns them)

- **Analytics as a source of business truth.** SUM, verified savings, coverage percentages, scores and
  invoice quantities derive from the telemetry substrate and the P5.5 verified-delta ledger. A number
  originating in GA4 SHALL NOT appear on a customer-facing surface, in an invoice, or in a claim.
  *(Owned: P7 metering, P5.5 verification — and this phase's job is to not become a second source.)*
- **Replacing P2.5.** Sentry is not a metrics store and GA4 is not a span store. No dashboard, alert
  or SLO moves off the substrate. *(P2.5 remains the substrate.)*
- **Analytics or crash reporting in the CLI.** The CLI is offline-first, free on every plan, and
  works with no account and no network. *(P11's posture; unchanged, and asserted by an existing test.)*
- **Any of the three in a customer deployment.** Compose, Kubernetes and air-gapped installs ship with
  all three absent, and the air-gapped package is asserted to request no external origin. *(P19.)*
- **Self-hosted Sentry / Matomo / an in-house replay tool.** A real option (§8.2 D2 alternatives), out
  of scope here; if a customer later demands on-prem error aggregation, that is its own phase.
- **A/B testing, feature flags, or experiment assignment on the public surface.** GA4 can do it; we
  are not doing it. Experiment machinery on a page that makes capability claims collides with the P23
  accuracy fence.
- **Marketing attribution beyond first-party referrer.** No advertising pixels, no conversion tags,
  no cross-site identifiers, no remarketing audiences. *(Not deferred — declined.)*

---

## 4. Users & personas

| Persona | What they need from this phase | What they must never get |
|---|---|---|
| **Prospect** (public surface, no session) | A page that loads fast and states its boundary honestly; a banner that asks once, plainly, and honours a refusal | Tracking before consent; a cross-site identifier; a page that breaks when they decline |
| **Customer administrator** (`/app/**`) | Their tenant's own screen contents never leaving the platform; a browser bug fixed before they report it twice | A session recording of their prompts; a tracker on a page showing their source diffs |
| **Platform operator** (P8 console) | A production browser exception with a `trace_id` that joins the span store and the audit log; a readiness surface that says whether reporting is up | A recorded replay of a cross-tenant view; a Sentry issue whose payload is itself a data-protection incident |
| **Product designer / PM** | Which surfaces are used, which funnel step loses people, which public section is read | A usage number presented to a customer as a platform metric |
| **DevOps engineer** | Reporting that is fail-static, that is never a startup dependency, whose absence is a state and not an error | A third-party outage that degrades a customer request path |
| **Private-deploy / air-gapped operator** | An install that requests nothing external, provably | A beacon inside their network; a DSN they did not configure |
| **Data-protection reviewer** (customer's, or ours) | A named sub-processor list, a category-level consent record, a payload allowlist they can read | A denylist they are asked to trust |

---

## 5. User stories / jobs-to-be-done

**Prospect**
- As a first-time visitor, I want the page to work fully whether I accept or decline, so declining is
  not punished.
- As a visitor who declined, I want to not be asked again on every page, and I want a way to change my
  mind that is not hidden.

**Product designer**
- As a designer, I want to know whether readers reach the boundary statement on the public page,
  because if they do not, the honesty of that statement is decorative.
- As a designer, I want to know that `/app/memory` was opened 40 times and returned to 3 times, so the
  next axis phase argues from usage rather than from taste.

**Customer administrator**
- As an administrator, I want to be told which third parties process anything about my organisation
  and what categories they receive, in the legal surface, versioned.
- As an administrator, I want an assurance I can verify, not one I am asked to accept: I open dev
  tools on `/app/variants`, and every request goes to the console's own origin.

**Platform operator**
- As an operator paged for "the console is blank", I want the browser exception in an inbox with a
  `trace_id`, so I can pull the server span for the same request instead of guessing.
- As an operator, I want the readiness endpoint to distinguish *reporting absent* from *reporting
  configured but unreachable*, because one is a decision and the other is a fault.
- As an operator, I want a third-party outage to be invisible to customers.

**Air-gapped operator**
- As an operator installing into a network with no public egress, I want the package to contain no
  external origin at all, and I want that asserted by the package's own verification, not promised in
  a README.

**Engineer fixing a browser bug**
- As an engineer, I want the frame, the release and the surface — and I accept losing the error
  message string, because I know where a leaked prompt would have ended up.

---

## 6. Functional requirements

### Deployment posture (capability `telemetry-deployment-posture`)

- **FR1.** Each of the three integrations SHALL be **absent unless configured**, and absence SHALL be
  a supported, tested configuration rather than a degraded one. A build with no GA4 measurement id, no
  Clarity project id and no Sentry DSN SHALL emit no third-party request, load no third-party script,
  and log no warning about the absence.
- **FR2.** The default for every deployment substrate other than the platform's own hosted deployment
  SHALL be **absent**. The Compose, Kubernetes and air-gapped artefacts SHALL NOT carry a
  measurement id, project id or DSN, and SHALL NOT accept one from a discovered default.
- **FR3.** The air-gapped package SHALL be asserted to reference **no external origin** — no script
  host, no ingest host, no font, no image host — as part of its own verification, and the assertion
  SHALL fail the package build rather than being checked at install time.
- **FR4.** Configuration state SHALL be readable on the existing readiness surface as one of three
  named states per integration: `absent`, `configured`, `degraded` — never a boolean. `degraded` SHALL
  name the integration and the observed failure class.
- **FR5.** No integration SHALL be a startup dependency. A service SHALL start, serve and pass
  readiness with an unreachable or misconfigured integration, reporting `degraded`.

### Consent (capability `analytics-consent`)

- **FR6.** Consent SHALL be recorded **per category**, from a closed set: `essential`,
  `product_analytics`, `session_replay`, `error_diagnostics`. A single accept-all control MAY exist
  as a convenience but SHALL NOT be the only granularity offered.
- **FR7.** The default for every non-essential category SHALL be **denied**. No script SHALL load, no
  beacon SHALL fire and no non-essential cookie or storage entry SHALL be written before an explicit
  grant for that category.
- **FR8.** A refusal SHALL be **stored as a refusal**, distinguishable from "not yet asked", and SHALL
  NOT be re-prompted on subsequent navigations or sessions until the consent policy version changes.
- **FR9.** Withdrawal SHALL be reachable from every page carrying a consent-gated integration, SHALL
  take effect on the next navigation without a sign-out, and SHALL stop the corresponding collection.
- **FR10.** Declining every category SHALL leave every function of the surface intact. No content,
  control or route SHALL be conditioned on a grant.
- **FR11.** The consent record SHALL carry the **policy version** it was given against. A material
  change to the privacy or sub-processor document SHALL invalidate prior grants for the affected
  categories and re-ask; a non-material change SHALL ask nobody.
- **FR12.** Analytics consent SHALL NOT be written into the P23 `consent-records` ledger. That ledger
  holds statutory acceptances of immutable documents, survives identity erasure, and is append-only; a
  per-visitor, revocable, often pre-session preference has the opposite lifecycle and SHALL be stored
  separately.
- **FR13.** The operator console SHALL NOT present a consent banner. It is a staff surface governed by
  the internal acceptable-use notice; its only integration is error reporting, whose payload carries
  no personal data by construction (FR21). This exception SHALL be stated in the internal notice, not
  inferred from the absence of a banner.

### Product analytics (capability `product-analytics`)

- **FR14.** GA4 SHALL be loaded as a browser tag **only** on the public surface — routes that require
  no session, read no tenant data and make no upstream platform call — and only after a
  `product_analytics` grant.
- **FR15.** No browser tag SHALL be loaded under `/app/**`, under `/api/**`, or anywhere in the
  operator console. Console usage analytics SHALL be emitted **server-side** by the BFF and relayed
  onward from the server, so the browser on a tenant page contacts only its own origin.
- **FR16.** A server-emitted analytics event's payload SHALL be **constructed from an allowlist**, in
  the same manner and with the same review artefact shape as
  [`internal/runlink/allowlist.go`](../../internal/runlink/allowlist.go). A field added to an internal
  representation SHALL be absent from a transmitted event by default.
- **FR17.** A surface SHALL be identified by an id drawn from a **closed enum**, never by its URL
  path. A URL under `/app` carries variant, run, node and tenant identifiers; a path is therefore not
  a permissible event field.
- **FR18.** Event names SHALL come from a central enum, alongside `event.name` in the existing logging
  conventions. An ad-hoc event name SHALL fail the build.
- **FR19.** No analytics figure SHALL be rendered on a customer-facing surface, used to derive an
  invoice quantity, or presented as a platform metric. Analytics measures **interface usage** and
  nothing else, and the boundary SHALL be asserted, not documented.
- **FR20.** No cross-site identifier, advertising identifier, remarketing audience or conversion pixel
  SHALL be configured. IP anonymisation SHALL be on; ad-personalisation signals SHALL be off.

### Session replay (capability `session-replay`)

- **FR21.** Clarity SHALL be loaded **only** on the public surface, and only after a `session_replay`
  grant. It SHALL NOT be loaded under `/app/**`, and SHALL NOT be loaded anywhere in the operator
  console. This refusal SHALL NOT be reachable by any plan, role, entitlement, feature flag,
  environment variable or request parameter.
- **FR22.** On the surface where it does run, masking SHALL be **on by default** — all text input
  masked, all form fields masked — and any unmasking SHALL be an explicit, per-element opt-in with a
  recorded reason.
- **FR23.** The refusal SHALL be enforced structurally, not by page-level judgement: the script is
  unreachable from the tenant and operator layouts, and the CSP for those prefixes does not name its
  origin. A rule enforced page by page fails the first time somebody adds a page.
- **FR24.** A build in which a replay runtime appears in any client chunk reachable from a tenant or
  operator route SHALL fail, naming the chunk.

### Error monitoring (capability `error-monitoring`)

- **FR25.** Sentry SHALL be integrated in the Go services (`agentd`, the admin API, and both BFF
  server halves) and in the browser on all three web surfaces. It SHALL NOT be integrated into the
  `heros` CLI on a customer's machine.
- **FR26.** A transmitted event SHALL be **constructed from an allowlist**, not serialized-then-
  scrubbed. The permitted field set SHALL be a named, checked-in, reviewable list with a
  one-line justification per field, and the transmitted key set SHALL be asserted to be a subset of it.
- **FR27.** The following SHALL NOT appear in a transmitted event on any path, including diagnostics
  and local development: error message bodies except values drawn from the central `error.code` enum;
  request bodies; request or response headers; query strings; breadcrumb, fetch, XHR or navigation
  URLs; console output; DOM breadcrumbs and click-target text; local variables; source context lines
  for any frame not belonging to platform code; environment variable values; provider credentials;
  prompt, completion, source or diff text; hostnames; server names; IP addresses; email addresses;
  tenant names.
- **FR28.** Every transmitted event SHALL carry the **existing** `trace_id` — the one already on the
  span, in the structured log, and in the `X-Trace-Id` response header of a `SYS_INTERNAL` 500 — and
  SHALL NOT mint a second correlation identity.
- **FR29.** The existing `telemetry.Scrubber` chokepoint SHALL run over the constructed event as the
  last stage before transmission, giving two independent guards of different kinds (construct, then
  scrub) in the same shape as `server-only` plus `scan-bundle.mjs` on the credential surface.
- **FR30.** Sampling SHALL be explicit and stated: a sample rate, a per-issue rate limit, and a
  transmit budget. Performance tracing and profiling SHALL be **off** — a profile carries function
  names and timings from a customer's run path, and the substrate already owns latency.
- **FR31.** Reporting SHALL be fail-static and non-blocking: a transmit failure SHALL never fail,
  delay, retry into an unbounded queue, or panic a request. It SHALL be reported as `degraded` on the
  readiness surface and logged once per interval, not once per event.
- **FR32.** A browser event SHALL be sent from the browser directly to the reporting origin named in
  that surface's CSP, and SHALL NOT be tunnelled through the BFF. A tunnel would make the BFF a
  carrier of arbitrary client-supplied error material and would hide the flow from a reader of the CSP.
- **FR33.** Source maps for platform code MAY be uploaded for the platform's own hosted deployment so
  frames are readable, and SHALL NOT be included in any customer-installable package or served from a
  customer-facing origin.

### The third-party-origin fence (capability `third-party-origin-fence`)

- **FR34.** The Content-Security-Policy SHALL be split **by route prefix**, following the precedent
  already set for `Cache-Control` in both `next.config.mjs` files. `/app/**`, `/api/**` and every
  operator-console route SHALL retain `default-src 'self'` with no third-party origin. The public
  prefix SHALL name **only** origins present on a checked-in allowlist.
- **FR35.** *(Modifies P9 FR35.)* The public surface's "no third-party origin" rule SHALL become "no
  third-party origin other than those on the analytics origin allowlist, each loaded only under its
  consent category". The tenant and operator prefixes' rule SHALL remain absolute, and SHALL gain a
  per-prefix assertion it did not previously have.
- **FR36.** The origin allowlist SHALL be a single checked-in artefact naming each origin, the
  integration that needs it, the consent category that gates it, and the CSP directive it appears
  under. The middleware SHALL construct the header from that artefact; a hard-coded origin in
  middleware SHALL fail the build.
- **FR37.** The payload ceiling SHALL be extended to cover third-party weight. Today
  `scan-bundle.mjs` measures only `.next/static`, so it would stop a 3D library and would not notice
  three trackers — after this phase the ceiling would mean **less** than before it. A **per-origin
  transfer budget**, measured in a real browser during the acceptance run, SHALL fail acceptance when
  exceeded, naming the origin and the overage.
- **FR38.** The existing decorative-runtime scan SHALL gain the inverse rule: an analytics, replay or
  error-reporting runtime appearing in a **client chunk reachable from a tenant or operator route**
  SHALL fail the build, naming the runtime and the chunk.
- **FR39.** Both directions SHALL be asserted: a surface SHALL NOT load an origin the fence does not
  allow, and the fence SHALL NOT allow an origin no surface loads. A stale allowlist entry is a
  permission nobody asked for.

### Legal and disclosure (extends P23)

- **FR40.** Each of the three processors SHALL appear in a versioned **sub-processor document** on the
  legal surface, naming the processor, the categories it receives, the surfaces it runs on, and the
  jurisdiction. Publishing a version declared material SHALL invalidate the affected consent grants
  (FR11).
- **FR41.** Any public claim about tracking SHALL be re-derived from what ships. The existing claims
  fence (`scan:claims`) SHALL fail the build on a claim the shipped configuration contradicts — the
  same mechanism that already stops a capability claim outrunning a capability.

---

## 7. Non-functional requirements

- **NFR1 — Customer request paths are unaffected.** No integration SHALL add latency to a customer
  request path. Every transmit is out-of-band; a p99 regression on any served route attributable to
  this phase is a defect.
- **NFR2 — Public surface stays fast and resilient.** The public surface must keep serving when the
  platform API is down (P9 NFR13). Consent-gated scripts SHALL load after first paint, SHALL NOT block
  rendering, and their failure SHALL be invisible.
- **NFR3 — Payload discipline.** Shipped first-party JS stays under the existing 1,400,000-byte
  ceiling. Third-party transfer per origin stays under its stated budget (FR37).
- **NFR4 — The CSP stays strict.** Nonce-based, per request, `'strict-dynamic'`, no `'unsafe-inline'`
  for scripts, no `'unsafe-eval'` in production, on every prefix including the public one. An
  integration that requires `'unsafe-inline'` script execution SHALL be refused rather than
  accommodated.
- **NFR5 — Data minimisation is structural.** Every payload is constructed from an allowlist. A new
  field in an internal representation is absent from the wire by default, and that direction is the
  requirement, not an implementation preference.
- **NFR6 — Absence is silent.** An unconfigured integration produces no console warning, no log line
  per request, no readiness noise. Absence is a decision, and a decision does not warn.
- **NFR7 — Degradation is loud once, not loud always.** A `degraded` integration is named on the
  readiness surface and logged at most once per interval.
- **NFR8 — Reproducibility is untouched.** No `config_hash` input changes. No metric, score, interval,
  rank or coverage figure derives from any of the three.
- **NFR9 — Every fence can go red.** Each assertion in §6 has a test that fails when the guarantee is
  broken, demonstrated by a deliberate violation in a fixture. A fence never demonstrated red is a
  fence nobody has checked.

---

## 8. System design summary

### 8.1 Shape

```
                        ┌─────────────────────────── PUBLIC PREFIX ────────────────────────────┐
  Prospect ─── GET / ──▶│ CSP: default-src 'self'; script-src 'self' nonce strict-dynamic       │
                        │      + allowlisted analytics origins (per granted category)           │
                        │  consent = denied  → nothing loads, nothing is written                │
                        │  consent = granted → GA4 tag, Clarity tag, Sentry browser             │
                        └──────────────────────────────────────────────────────────────────────┘

                        ┌────────────────────── TENANT PREFIX  /app/**  ───────────────────────┐
  Customer ─── GET ─────▶│ CSP: default-src 'self'   (UNCHANGED — no third-party origin)        │
                        │  browser → own origin only.  No GA4 tag. No Clarity. Ever.           │
                        │  Sentry browser: frames + error.code + trace_id + release + surface   │
                        │                                                                      │
                        │  BFF (server) ── surface_viewed{surface_id from closed enum} ────┐    │
                        └─────────────────────────────────────────────────────────────────│────┘
                                                                                          ▼
                        ┌──── OPERATOR CONSOLE (every route) ────┐        ┌──────────────────────┐
  Operator ────────────▶│ CSP: default-src 'self'  (UNCHANGED)   │        │ server-side relay    │
                        │ No GA4. No Clarity. Sentry only.       │        │ allowlist-constructed│
                        └────────────────────────────────────────┘        └──────────┬───────────┘
                                                                                     │
   agentd / admin API / BFF server halves                                            │
        │  panic or unexpected error                                                 │
        ▼                                                                            ▼
   ┌──────────────────────────────────────────────┐                         ┌──────────────────┐
   │ construct event from ALLOWLIST               │                         │ GA4 (server-side)│
   │   error.type, our frames, error.code,        │                         └──────────────────┘
   │   trace_id, release, edition, surface        │
   ├──────────────────────────────────────────────┤       ┌──────────────────────────────────┐
   │ telemetry.Scrubber  (second, different guard) │──────▶│ Sentry                           │
   └──────────────────────────────────────────────┘       └──────────────────────────────────┘
        │
        └──▶ existing P2.5 substrate: span (trace_id), metric event, structured log  [unchanged]

   ┌─────────────────────────────────────────────────────────────────────────────────────────┐
   │ CUSTOMER DEPLOYMENT (compose / k8s / air-gapped):  all three ABSENT.                    │
   │ No id, no DSN, no script, no beacon. Asserted at package build, not at install.          │
   └─────────────────────────────────────────────────────────────────────────────────────────┘
```

### 8.2 Decisions

Each decision is stated in the order the design conventions require: **the problem → the design →
why it fits here → the alternatives and why not → the effect.**

---

#### D1 — Whose deployment gets a beacon: ours only

**The problem.** The same binaries and the same two Next.js applications run in three very different
places: our hosted deployment at `heros-agent.space`, a customer's own Compose or Kubernetes install,
and an air-gapped network we will never see. "Install Sentry" has to mean something different in each,
and if it does not, we ship a beacon into a customer's private network.

**The design.** All three integrations are configured **only** for the platform's own hosted
deployment. Everywhere else they are absent, absence is the default, and the air-gapped package is
asserted at build time to reference no external origin.

**Why it fits here.** This product is sold into private deployments where the customer's DevOps team
takes over. In that setting a technical choice *is* a product property, not an implementation detail —
a customer who discovers an outbound beacon they did not configure has learned something about us that
no feature recovers from. And the phase that ships an id into the wrong artefact is unrecoverable in
the other direction too: you cannot un-send a beacon.

**Alternatives, and why not.**
- *One build, ids supplied by environment.* Simplest, and it means a stray environment variable in a
  customer's Helm values turns their install into a reporting client. Rejected: the failure is silent
  and the blast radius is a customer's network.
- *Opt-in per customer, with a checkbox in the installer.* Rejected for this phase: it makes the
  customer responsible for a decision they cannot evaluate, and every one of them would decline. If
  on-prem error aggregation is genuinely wanted later, the right answer is a self-hosted collector,
  which is its own phase.
- *Ship it everywhere and rely on the DSN being unset.* Rejected: the code path exists, the SDK is in
  the bundle, and "unset by default" is one merge away from "set by default".

**The effect.** An air-gapped operator running the package verifier sees zero external origins, and
the assertion is part of the package build, so the claim is produced by the same run that produces the
artefact. A customer reading their own egress logs finds nothing from us.

---

#### D2 — Clarity is refused on every surface that renders customer or cross-tenant content

**The problem.** Session replay is the highest-value UX instrument available and also the highest-risk
one: it records the screen. The screen under `/app/**` renders prompt text, generated diffs, node
identifiers, model configuration and run output. The operator console's screen renders cross-tenant
aggregates, tenant names, active impersonation state and audit rows.

**The design.** Clarity runs on the public surface only. On `/app/**` and on every operator route it
is refused — structurally, by the layout it is unreachable from and by a CSP prefix that does not name
its origin — and no plan, role, entitlement, flag, variable or parameter turns it on.

**Why it fits here.** The platform has already decided, in code, what may cross a boundary and what
may not. `internal/runlink/allowlist.go` names the never-permitted set: prompt text, source code, file
contents, generated diffs, environment values, credentials. A replay of `/app/studio` is a
lossy-but-legible copy of most of that list. Installing a recorder there while maintaining an
allowlist against the same content is not a trade-off, it is a contradiction — and under the arbitration
order (security first, single-implementation convenience last) a first-priority loss cannot be bought
with an eighth-priority gain.

**Alternatives, and why not.**
- *Clarity with aggressive masking on tenant pages.* Masking covers inputs and text nodes by
  configuration. Our exposure is not primarily inputs — it is rendered read-model content, code
  panels, diff views and SVG graph labels. A denylist of selectors over a surface that gains pages
  every phase fails silently the first time someone adds a page. Rejected on the same grounds the
  P11 boundary rejects denylists.
- *Replay on tenant pages behind an explicit per-tenant opt-in.* Superficially attractive; rejected
  because a tenant administrator cannot meaningfully consent on behalf of the developers whose prompts
  and source appear on the screen, and because "a customer asked for it" does not change what the
  recording contains.
- *Heatmaps only, replay off.* Clarity's heatmap and its recorder are the same script. Configuration
  is not a boundary.
- *Build our own first-party replay.* Same recording, our storage. Rejected for this phase: it moves
  the risk without reducing it, and it is a large build.

**The effect.** A customer administrator opens dev tools on `/app/variants` and sees every request go
to the console's own origin. An operator viewing a cross-tenant aggregate is not creating a recording
of it. And a future engineer who adds `/app/newthing` inherits the refusal for free, because it lives
in the prefix and the layout, not in a checklist.

---

#### D3 — GA4 on tenant surfaces is server-emitted, never a browser tag

**The problem.** We genuinely need to know which console surfaces get used — eleven shipped in
P13–P18 alone, and the next axis phase should argue from usage. But a browser tag on `/app/**` sends
the page URL and the visitor's address to a third party, and a URL under `/app` contains variant, run
and node identifiers.

**The design.** No browser tag under `/app/**`, `/api/**`, or in the operator console. The BFF emits a
first-party event server-side, with a payload constructed from an allowlist, where the surface is
identified by an id from a closed enum rather than by its path. The server relays onward.

**Why it fits here.** The BFF already exists precisely to keep something out of the browser — the
platform credential. Extending it to keep the tenant's URL out of a third party's logs is the same
argument on the same component. And the closed enum is what makes the guarantee auditable: you can
read the list of surface ids and know that is the complete set of things that can be reported, which
you can never know about a URL.

**Alternatives, and why not.**
- *Browser tag with URL redaction.* A redaction rule over paths that gain segments every phase — a
  denylist again, failing toward disclosure.
- *No console analytics at all.* Defensible, and the status quo. Rejected because the cost is real:
  six axis phases shipped surfaces with no way to learn whether they landed, and the seventh would
  repeat it.
- *Proxy the browser tag through our own origin so the CSP stays literally `'self'`.* Rejected on
  honesty grounds. It would make a third-party data flow *look* first-party, and a customer reading
  `default-src 'self'` would be misled by a header we deliberately arranged to mislead them. The CSP's
  value is that it is a readable statement of where data goes.

**The effect.** The browser on a tenant page contacts one origin. A product designer gets
`surface_viewed{surface_id:"app.memory"}` counts. Nobody can point at a URL in a third party's console
and read a customer's run id out of it.

---

#### D4 — The CSP splits by prefix; the change is announced, not smuggled

**The problem.** The commitment "no third-party origin" is currently enforced by two live assertions
that the shipped CSP contains no `https://` at all. Installing anything third-party makes those
assertions fail. There is a version of this phase that edits two regexes and moves on.

**The design.** Split the CSP by route prefix. `/app/**`, `/api/**` and every operator route keep
`default-src 'self'` verbatim and **gain** a per-prefix assertion. The public prefix names only
origins from a checked-in allowlist. P9 FR35 is amended as a `## MODIFIED Requirements` delta with
its reasoning attached.

**Why it fits here.** The precedent is already in the tree and already argued: `next.config.mjs` splits
`Cache-Control` by prefix because the public surface genuinely contains something different from a
tenant page, and it notes that a per-page judgement "fails the first time somebody adds a page". This
is the same split, on the same boundary, for the same reason.

**Alternatives, and why not.**
- *Relax the global CSP and widen the two tests.* Rejected: it silently removes the guarantee from the
  tenant surface, which is the surface the guarantee was for.
- *Keep the CSP absolute and give up on GA4/Clarity entirely.* An honest option and it stays on the
  table for the tenant prefix, where we take it. On the public prefix the cost — a commercial funnel
  nobody can measure, right as P21 lands — is not worth paying.
- *Report-only CSP on the public surface.* Rejected: a policy that reports and does not enforce is
  documentation.

**The effect.** After this phase the tenant surface's guarantee is *stronger* than before, because it
is asserted specifically rather than incidentally. And the public surface's exposure is a list you can
read, in one file, with a consent category beside each entry.

---

#### D5 — Sentry events are constructed from an allowlist, not scrubbed

**The problem.** A Sentry event, by default, is close to a worst case for this platform: exception
message, stack frames with source context, request URL, headers and body, environment, breadcrumbs
containing every fetch URL and console line, user IP, hostname. And the most dangerous field is the
most innocuous-looking one — the message. `failed to resolve prompt "…"` is an ordinary Go error and
also an exfiltration path.

**The design.** A `BeforeSend` hook that **builds** the outbound event from a named allowlist and
discards the rest: `error.type`, platform-owned stack frames, `error.code` from the central enum,
`trace_id`, `release`, `edition`, `surface`. The `telemetry.Scrubber` then runs over the constructed
event as an independent second guard. Message bodies are dropped unless the value is an `error.code`.

**Why it fits here.** The asymmetry is already written down in this repository, in the package comment
of `internal/runlink`: a denylist means a new field is *sent* — silent, discovered externally, by a
customer. An allowlist means a new field is *absent* — visible as a missing feature, discovered here.
An error reporter receives every field any engineer ever attaches to an error, from anywhere in the
codebase, forever. It is the single strongest case for construction over filtering in the system.

**Alternatives, and why not.**
- *Sentry's own data-scrubbing plus `send_default_pii=false`.* Server-side scrubbing happens *after*
  transmission and *after* storage, and the patterns are shapes we guessed. Both are the wrong side of
  the boundary.
- *Our own `BeforeSend` denylist over Sentry's event.* Better, still a denylist, and it must be
  updated every time the SDK adds a field.
- *No error reporting; rely on structured logs.* The status quo, and it is why a production browser
  exception currently reaches nobody. Logs answer questions you knew to ask; an error inbox surfaces
  the ones you did not.

**The effect.** A Sentry issue reads: `panic: assignment to entry in nil map` at
`internal/evalboard/board.go:212`, `error.code=SYS_INTERNAL`, `trace_id=…`, `release=v0.24.0`,
`surface=api.p4`. An operator pastes the `trace_id` into the span store and has the request. Nobody
can find a customer's prompt in it, because there is no field it could have arrived in.

---

#### D6 — One correlation identity, reused

**The problem.** Adding an incident system usually adds an incident identity, and then two systems
hold half an incident each with no join key.

**The design.** Every Sentry event carries the `trace_id` that already exists on the span, in the
structured log, and in the `X-Trace-Id` header of a `SYS_INTERNAL` 500. Sentry mints nothing.

**Why it fits here.** The platform already made this choice once and stated it: every metric and trace
event is tagged with the same seven-field identity, and the reason is that a claim you cannot join to
its evidence is not a claim. An error report is evidence about a request; the request already has a
name.

**Alternatives, and why not.**
- *Sentry's own event id as the operator's handle.* Rejected: it names the report, not the request, so
  it joins to nothing.
- *A new `incident_id` spanning both.* A third identity to explain the relationship between two.

**The effect.** A customer quotes the `X-Trace-Id` from a 500. The operator finds the span, the log
line, the audit row and the Sentry issue with one string.

---

#### D7 — Consent is per category, default-off, and a refusal is a stored fact

**The problem.** A single accept-all banner lets session replay ride in on the back of a page-view
count, and a refusal stored as an absence re-prompts on every navigation forever.

**The design.** Four named categories (`essential`, `product_analytics`, `session_replay`,
`error_diagnostics`), all non-essential defaulting to denied, each independently grantable and
withdrawable, with the grant carrying the policy version it was given against. A refusal is stored as
a refusal.

**Why it fits here.** The shipped P9 scenario is "a visitor is not tracked before they consent to
**anything**", and the categories are what make that survivable as the integrations grow: a visitor
who accepts usage counting has not accepted being filmed. The interaction rule this project already
enforces — reduce what the user must supply, and never re-ask for something the system already knows —
is what makes "remember the no" a requirement rather than a nicety.

**Alternatives, and why not.**
- *One accept/decline.* Simpler banner, and it conflates a page-view count with a screen recording.
- *Legitimate-interest for analytics, consent only for replay.* Available in some jurisdictions;
  rejected because the posture we have published is stronger than the law's floor, and quietly
  dropping to the floor is the kind of change a customer discovers rather than reads.
- *Store analytics consent in the P23 consent ledger.* Rejected: that ledger is statutory,
  append-only, survives identity erasure and is keyed to an immutable document hash. A revocable
  per-visitor preference with no tenant has the opposite lifecycle in every respect, and putting it
  there would mean a cookie choice outliving a deletion request.

**The effect.** Declining costs nothing and is remembered. Accepting usage counting does not enable
recording. A material privacy-document change re-asks; a typo fix asks nobody.

---

#### D8 — Analytics may never become a business number

**The problem.** Once GA4 exists, someone will want to put a number from it on a slide, then on a
customer-facing surface, then next to an invoice.

**The design.** Analytics measures interface usage only. No analytics figure is rendered on a
customer-facing surface, used to derive an invoiced quantity, or presented as a platform metric — and
the boundary is asserted rather than documented.

**Why it fits here.** The console's read-model rule already forbids the browser recomputing a
statistical claim, on the grounds that it would be a second source of truth. GA4 is a *third*, held by
a party we do not control, sampled, ad-blocked, and consent-gated — so its numbers are systematically
wrong in a direction nobody can quantify. And the metering rule is explicit: SUM derives from
**linked** runs, and the platform never infers or extrapolates.

**Alternatives, and why not.**
- *Allow it for internal reporting only, by convention.* A convention with money on the other side of
  it. Every "internal only" number eventually appears on a slide.

**The effect.** "Active tenants" on the operator console keeps coming from the substrate. A funnel
number lives in GA4 and informs a design decision, which is what it is good for.

---

#### D9 — The payload fence is extended, or this phase weakens it

**The problem.** `scan-bundle.mjs` enforces a 1,400,000-byte ceiling and names decorative runtimes it
refuses. It measures `.next/static` — the JavaScript **our build** produces. A script loaded from
`googletagmanager.com` is not in `.next/static`. So the ceiling would stop a small 3D library and
would not notice three trackers, and after this phase it would mean less than it did before.

**The design.** Two additions. A **per-origin transfer budget** for allowlisted third-party origins,
measured in a real browser during the acceptance run, failing acceptance with the origin and the
overage named. And the **inverse** of the decorative-runtime scan: an analytics, replay or
error-reporting runtime found in a client chunk reachable from a tenant or operator route fails the
build.

**Why it fits here.** The script's own comment states its purpose — the rejected trends stay rejected
because "the build says no and names the overage" — and that purpose is defeated by a class of payload
it cannot see. This is also the second half of the phase's obligation: a phase that relaxes a guard
owes the codebase a stronger one, or it is just a relaxation.

**Alternatives, and why not.**
- *Trust code review to notice a fourth tracker.* The script's own comment answers this: a rule with a
  demonstrated failure rate is a rule that needs a machine.
- *Budget total third-party weight rather than per origin.* Rejected: a total lets one integration grow
  into another's headroom without anyone deciding.

**The effect.** Adding a fourth tracker is a build failure with a number attached, and adding
`@sentry/nextjs` to a tenant chunk is a build failure that names the chunk.

---

#### D10 — The browser reports directly, not through the BFF

**The problem.** A tunnel through the BFF would keep the browser talking only to its own origin, which
looks like it serves the tenant-surface guarantee.

**The design.** Browser events go directly to the reporting origin named in that surface's CSP.

**Why it fits here.** A tunnel makes the BFF accept arbitrary client-supplied error material and
forward it, which is a new unauthenticated ingest path on the component that holds the platform
credential. And it hides the flow: the CSP would say `'self'` while data leaves for a third party,
which is the same dishonesty D3 rejects for GA4. Under the arbitration order, an accurate security
surface outranks a tidier header.

**Alternatives, and why not.**
- *Tunnel to keep `connect-src 'self'`.* Rejected above.
- *Tunnel with a strict server-side allowlist over the forwarded payload.* Now the BFF must validate
  the shape of every event and we have built a second Sentry SDK to avoid naming an origin.

**The effect.** A reader of the CSP on any prefix can enumerate every party that receives anything
from that page. That property is what makes the whole posture checkable.

---

### 8.3 Design key points

**What original need does this answer?**
Two, from §2.1. *We cannot see the people using this* — a commercial funnel about to carry money, six
axis phases whose surfaces landed unmeasured, and a public install page now walked by strangers. And
*we cannot see it break in a browser* — a production client-side exception currently reaches nobody,
on surfaces whose specific failure mode is to render correctly and do nothing.

**Why designed this way**
- The boundary is drawn by **surface**, not by tool, because what varies is the content on the screen,
  not the vendor. That is why Clarity is fine on one prefix and refused on another, and why one refusal
  covers every page a future phase adds to that prefix.
- Payloads are **constructed**, not filtered, because the failure directions are not symmetric: a
  denylist's miss is silent and is discovered by a customer.
- The change to a published commitment is a **narrowing plus an announcement**, not a widened regex,
  because the guarantee's value was never the header text — it was that someone could rely on it.
- Absence is a **state**, not a fault, because most deployments of this software will never have any of
  this configured and must not be treated as broken.

**Key business decisions**
- Who is allowed to be watched: prospects on a public page, yes, with permission. Customers working in
  their own repository, no. Our own operators, error reports only.
- Who owns the refusal: nobody can grant Clarity on a tenant page — not a plan, not an operator, not
  the customer themselves. It is not an entitlement.
- What a measurement may be used for: a design decision, never an invoice and never a customer-facing
  claim.
- What we must publish: a versioned sub-processor list, and a material change to it re-asks for
  consent.

**Key technical decisions**
- CSP split by route prefix rather than one global policy — the split already exists for
  `Cache-Control` and for the same reason.
- Tenant analytics emitted **server-side from the BFF** with a closed surface enum, rather than a
  browser tag with URL redaction.
- Sentry `BeforeSend` **constructs**; `telemetry.Scrubber` then runs as an independent second guard of
  a different kind.
- `trace_id` reused, no new correlation identity.
- Browser reports go direct to a named origin; no BFF tunnel.
- Performance tracing and profiling off — function names and timings from a customer's run path are
  content, and the substrate already owns latency.
- The fence is extended with a per-origin browser-measured transfer budget plus an inverse
  runtime-in-tenant-chunk scan, so the guard is stronger after the phase than before it.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *interaction simplicity first; the requested scope is the scope*

The consent banner is the only new thing a user is asked to do, so it gets the scrutiny.

- **Ask once, plainly, and honour the answer.** Four categories in plain English — what is collected,
  by whom, and what it is for. No dark pattern: decline is the same visual weight as accept, and the
  page is fully functional either way (FR10). A surface that punishes declining has not asked, it has
  coerced.
- **Remember the no.** A refusal stored as "not yet asked" re-prompts forever. That is the same defect
  class as re-asking for an identifier the system already holds, which this project forbids.
- **Withdrawal is reachable, not buried.** From every page that carries a gated integration, effective
  on the next navigation, no sign-out.
- **Nothing is inferred on the user's behalf.** No pre-checked category, no "we assume you agree",
  no auto-submitted banner. The console does not act for the user.
- **Naming.** Interface text says what a reader can check: "Usage analytics", "Session recording",
  "Error diagnostics" — not "Improve your experience". The term dictionary gets three entries so the
  banner, the privacy document and the operator console use one vocabulary.
- **Scope fidelity.** This phase installs measurement. It does not redesign the public page, does not
  add an experiment framework, and does not touch the tenant surfaces' information architecture.
  Adjacent temptations are named in the non-goals and left there.

### 9.2 Senior System Designer — *arbitrate by priority; do not open a one-way door*

- **The arbitration.** Every hard call here resolves at priority 1 (security) or 3 (user complexity),
  and none is bought with priority 8 (implementation cost). Clarity on tenant surfaces is 1-vs-8:
  refused. Allowlist construction over filtering is 1-vs-8: construct, even though it is more code.
  Server-side tenant analytics is 1-vs-8: more work, taken. The one place cost lost to a *lower*
  priority would be the BFF tunnel — cheaper to reason about, dishonest — and it was refused on
  priority 1.
- **Escalation, not silent downgrade.** Where the correct design is expensive, both paths are on the
  page with their costs (D2, D3, D5, D9 alternatives), and the choice is stated rather than absorbed.
- **New surface area, minimised.** No new table. Consent is a first-party cookie plus the existing
  preference mechanism; analytics events are transmitted, not stored by us; Sentry stores nothing on
  our side. The one-way doors here are *published commitments*, not schemas, which is why FR35 is
  amended as an explicit delta with reasoning rather than edited.
- **Extensibility without a core change.** Origins, consent categories, allowlisted event fields and
  surface ids are all **table-driven** artefacts. Adding a fourth integration edits data and its
  budget; it does not edit middleware. Enumerating origins in an `if` chain in middleware is exactly
  the hard-coded-enum failure this project rejects.
- **Where a write must reconcile with a read.** There is no invariant of the form "A must be
  accompanied by B" in this phase, because analytics is deliberately not a source of truth (D8). That
  is the design property that keeps this phase out of the reconciliation discipline, and it is worth
  stating: the moment an analytics number backs a business figure, this phase would need an idempotent
  reconciliation point, and it does not have one.
- **Direction of observability.** The existing pattern holds: enriching a *response* or a *stored
  record* is permitted; adding fields to an outbound *request* toward an upstream is the forbidden
  direction. Every payload here is constructed at the boundary, which is the permitted direction.

### 9.3 Senior Backend Dev — *contracts outlive code; a 200 is not evidence*

- **The allowlist is a package, with a review artefact.** Mirror `internal/runlink`: named fields,
  categories, a one-line justification each, and the doc rendered from the list rather than maintained
  beside it. The transmitted key set is asserted to be a subset — in both directions (a permitted field
  nothing populates is a permission nobody asked for).
- **The chokepoint stays the chokepoint.** `telemetry.Scrubber` runs over the constructed event. Two
  guards of different kinds, so a mistake in one is caught by the other — the same shape as
  `server-only` plus the bundle scan.
- **Error codes come from the central enum.** `error.code` and `event.name` are enum values, not string
  literals at call sites, which is what makes "the message is dropped unless it is a code" implementable
  rather than aspirational.
- **A silent fallback gets a WARN.** If the reporter cannot transmit and falls back to local logging,
  that is a WARN once per interval with the failure class named — never per event, never silent.
- **No cross-layer error is swallowed.** The transmit path may not `_ = err`. It reports `degraded`
  and logs; it does not fail the request, and it does not pretend it succeeded.
- **Two hundred is not evidence.** A test asserting the SDK's send returned success proves nothing
  about what left the process. The fence asserts the **transmitted bytes**: build a synthetic event
  carrying an `sk-…` key, an `AKIA…` id, an email, a 2 KB prompt, a unified diff, a `/app/variants/{id}`
  URL and a hostname, and assert none of them appears on the wire and the key set is within the
  allowlist.
- **Sampling and rate limits are explicit numbers** with a stated basis, not SDK defaults inherited by
  omission.

### 9.4 Senior Frontend Dev — *match the codebase; the smallest correct change; never invent a style*

- **No new page, no new nav slot.** The consent banner is a component within the existing layout and
  uses existing tokens. No colour, spacing, type-size or radius literal — `scan:tokens` fails the build
  on one, and the banner is not an exception to the design system.
- **The nonce is not optional.** Every consent-gated script is injected with the per-request nonce from
  `x-nonce`. A script without it does not run, which is the CSP working. Nothing here relaxes to
  `'unsafe-inline'`; an integration that requires inline execution is refused (NFR4).
- **The mirror trap.** Both consoles have their own `middleware.ts` and their own `next.config.mjs`,
  and this phase touches both. The origin allowlist and the prefix rules live in **one** shared artefact
  that both read, with a drift check — because a rule copied into two files is a rule that will differ
  in two files.
- **Three states stay three states.** Consent is `granted` / `denied` / `not-asked`; reporting is
  `absent` / `configured` / `degraded`. Neither collapses to a boolean anywhere in the UI, for the same
  reason a 404, a 503 and a transport failure are three messages in this console.
- **After first paint, always.** Gated scripts load after first paint and never block rendering. The
  public surface must keep serving when the platform API is down; a third-party script must not be able
  to take that away.
- **Acceptance is a rendered browser.** A green build is compatible with a banner that renders and does
  nothing, and with a CSP that refuses our own tag. The evidence for this phase is: decline and see zero
  third-party requests; accept and see exactly the allowlisted origins; navigate `/app` and see only
  the own origin; and read the CSP header on each prefix.
- **English strings, `en-US` pinned** through the single swap point, including every consent string.

### 9.5 Senior AI Engineer — *no mock in production; aggregate numbers hide single-sample defects*

- **Nothing here touches an eval, a score, an interval, a rank or a `config_hash`.** That is the
  requirement (NFR8), and it is checkable: the P0 golden vectors must reproduce byte-identically after
  this phase, because no field is added to any hashed structure.
- **Sampling is where this phase could quietly lie.** A sampled error stream makes "how often does this
  happen" unanswerable, and an aggregate issue count hides the single-tenant, single-node defect that
  the substrate would have shown. So: Sentry is a **defect inbox, not a rate source**. Any frequency
  question is answered from the substrate, where the events are complete. This is the same discipline
  as refusing to read a single-sample failure off an aggregate metric.
- **No LLM-derived grouping is trusted.** Sentry's own issue grouping is a heuristic. It may organise
  an inbox; it may not be cited as evidence that two defects are the same defect.
- **No mock reaches production.** A stub reporter is a test double. The configured-versus-absent state
  is explicit and readable on the readiness surface precisely so "we thought reporting was on" cannot
  be a discovery made during an incident.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable, least privilege*

- **Blast radius.** Every path is out-of-band. The failure mode of all three integrations is "no data
  arrives", never "a customer request fails". NFR1 makes a p99 regression a defect.
- **Health is externally readable, and the dashboard is not the judge.** Each integration's state
  appears on the readiness endpoint (`absent` / `configured` / `degraded`) as words. "It looks fine in
  the Sentry UI" is not a health signal — a third-party console is the least available part of this
  system during an incident.
- **Assert the transition, not the end state.** The acceptance evidence is the sequence: no consent →
  no requests; grant → exactly the allowlisted origins; withdraw → collection stops on next navigation;
  DSN removed → `absent` and silent; DSN pointed at a black hole → `degraded`, one log line per
  interval, requests unaffected.
- **Least privilege on the credentials.** A GA4 measurement id and a Clarity project id are public by
  nature. The Sentry **DSN** is a write-only ingest key and lives in the secret store, not in a
  manifest. The Sentry **auth token** used to upload source maps is CI-only, scoped to release
  creation, never present at runtime, and never in an image.
- **Systemic propagation — the seven layers.** A change this shape has to land in every layer or it
  becomes a version skew: the shared origin/consent artefact; both `middleware.ts`; both
  `next.config.mjs`; both `scan-bundle.mjs`; the Go service initialisation; the Compose, Kubernetes and
  air-gapped manifests (as **absent**); the release pipeline's source-map step; and the legal
  documents. The air-gapped assertion (FR3) is the backstop that catches a miss in the manifest layer.
- **Reversibility.** Removing an id or a DSN removes the integration, with no migration and no data
  model to unwind. That is a deliberate property: the rollback for this phase is a configuration
  change.
- **Stage verification has no manual step.** The origin assertions, the transfer budget and the
  air-gapped check run in the pipeline. A failure stops the stage and is recorded; nobody clicks
  through it.

### 9.7 Senior QA Engineer — *a green test is worth having only if green is credible*

- **Every fence is demonstrated red.** For each assertion in §6, a deliberate violation in a fixture
  must fail it: a hard-coded origin in middleware; a replay runtime imported into a tenant chunk; an
  event field outside the allowlist; a secret-shaped value in a message; a script injected without the
  nonce; a fourth origin added without a budget. A fence never seen red is a fence nobody has checked.
- **Assert the wire, not the call.** The load-bearing test serialises what would be transmitted and
  asserts on those bytes. Asserting that `CaptureException` was called is asserting that we called a
  library.
- **Allowlists are asserted in both directions.** A whitelist fence that only checks "nothing extra
  got out" does not notice a permitted field that nothing populates, or a permitted origin nothing
  loads. Both are stale permissions.
- **No `env`-gated tripwire.** A test that silently skips without a DSN is false confidence. The
  transmitted-payload tests run against a local capture endpoint, always, with no environment
  precondition.
- **Downstream consumption, not the function's return.** The browser evidence is a real rendered page
  with its network panel read — the same rule that caught the theme-control defect. Decline, accept,
  withdraw, and navigate a tenant route: four observations, each read from the browser.
- **Four-layer live assertion where anything is written.** Consent state is written and read back
  through the same path a subsequent page load uses; the assertion is on the read, not on the write's
  return value.
- **The regression pointers are real.** Each of the four amended commitments (§2.3) gets a named test
  that would fail if a future phase quietly re-widened it, and the test names the requirement so the
  next reader knows what they are breaking. And the completion claim for each task in this phase must
  point at an assertion that exists and runs — a checkbox with a test name beside it is not evidence
  unless the test is real.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

- **What we may now say, and what we may not.** After this phase "we run no third-party code on the
  page" is **false on the public surface** and **true on every tenant surface**. Any external
  statement must carry the distinction, and the existing claims fence is the mechanism: a claim the
  shipped configuration contradicts fails the build.
- **The strongest honest claim is the narrow one.** "Your prompts, source and diffs never reach a
  third party; the pages where you work run no analytics or session recording, and the boundary is
  enforced by a policy you can read in your own browser." That is checkable in thirty seconds by a
  prospect with dev tools, which is what makes it worth more than a broad assurance.
- **The private-deploy answer is the sharpest one.** In a customer's own deployment all three are
  absent, the package is asserted to reference no external origin, and the assertion runs in the build
  that produces the package. For a security-review conversation that is a stronger artefact than a
  policy statement.
- **Sub-processors, named.** Three processors, their categories, their surfaces and their
  jurisdictions, versioned on the legal surface. A material change re-asks for consent. Do not say
  "industry-standard analytics" — name them.
- **The internal boundary.** Do not describe sampling rates, allowlist internals, ingest topology or
  incident volumes to a customer. Say what is collected and where it goes; that is the customer's
  question.
- **FAQ entries that will actually be asked.** *Do you record my screen?* — Not on any page where you
  work; only on the public marketing pages, with permission, with inputs masked. *Can I turn it off?* —
  It is off until you turn it on, and you can withdraw at any time. *Does your on-prem install phone
  home?* — No, and here is the assertion in the package build. *Can I get error reports from my own
  install?* — Not today; that is a self-hosted collector and it is not built.

---

## 10. Dependencies

**Upstream (must exist; all do)**
- **P2.5** — the span/metric/log substrate, `trace_id`, the `Scrubber` chokepoint, the readiness
  surface.
- **P9** — the customer console, its BFF, its public surface, its per-request nonce CSP, its bundle
  fence, and the requirements this phase amends.
- **P8** — the operator console and its own middleware and bundle fence.
- **P11** — `internal/runlink`: the constructed-allowlist pattern and the never-permitted content list
  this phase reuses verbatim.
- **P19** — the Compose / Kubernetes / air-gapped substrates that must ship with everything absent.
- **P23** — the legal surface, the document manifest with `material` versioning, and the claims fence.

**Downstream (this unblocks)**
- **P21 (Stripe)** — a checkout funnel that can be measured on the public surface.
- **P26 (operator console refresh)** — a browser-error and reporting-health surface for operators, and
  the readiness states this phase defines.
- Any future UX work on the public surface, which currently argues from taste.

**Explicitly not depended on**
- No dependency on P13–P18. No axis, `Dimension`, resolver, gate or transform changes.
- No dependency on P22. Operator error reporting carries no identity beyond a surface label.

---

## 11. Risks & mitigations

| Risk | Why it is real here | Mitigation |
|---|---|---|
| **The CSP relaxation spreads.** The public prefix gets an exception; six months later a tenant page needs "just one" origin. | This is how every such boundary has historically been lost. | Per-prefix assertions that name the requirement they defend (FR34, FR39); the tenant rule is asserted specifically for the first time, and the allowlist is checked in both directions. |
| **Clarity is enabled on a tenant page by a well-meaning change.** | The script is a one-line addition and the layout is shared. | Structural refusal: unreachable from the tenant/operator layouts, origin absent from those prefixes' CSP, and a build failure if the runtime appears in a reachable chunk (FR23, FR24, FR38). Three independent mechanisms. |
| **A leaked value reaches Sentry through an error message.** | Every engineer who writes `fmt.Errorf("... %q", value)` is a potential source, forever. | Messages are dropped unless they are `error.code` values; the payload is constructed, not filtered; the `Scrubber` runs as a second guard; a red-demonstrated fence asserts the transmitted bytes (FR26–FR29). |
| **An analytics number becomes a business number.** | It is the single most common way an analytics install goes wrong. | FR19 asserts it rather than documenting it; the substrate remains the only source for SUM, savings, coverage and scores. |
| **An id or DSN reaches a customer artefact.** | Same build, three substrates. | Defaults are absent (FR2); the air-gapped package asserts no external origin at build time (FR3); the seven-layer propagation checklist names the manifest layer explicitly. |
| **Consent theatre.** A banner that technically asks and practically coerces. | Commercial pressure, once a funnel exists. | Decline is equal weight, full function on decline (FR10), refusal remembered (FR8), withdrawal reachable from every gated page (FR9). |
| **The payload ceiling silently stops meaning anything.** | Third-party bytes are invisible to the existing scanner. | FR37 measures them in a real browser, per origin, with the overage named. |
| **Reporting outage degrades the product.** | A synchronous transmit on a request path is an easy mistake. | Fail-static, out-of-band, `degraded` on readiness, NFR1 makes a latency regression a defect. |
| **Two incident systems that cannot be joined.** | The default outcome of adding an error tracker. | One identity: the existing `trace_id`, no new correlation id (FR28). |

---

## 12. Rollout & test strategy

**Wave 24a — the fence, before anything is installed.** The shared origin/consent artefact; the CSP
prefix split with the **tenant and operator prefixes asserted absolute** and the public allowlist
*empty*; the extended bundle scan and the browser transfer budget with **no origins permitted**; the
air-gapped no-external-origin assertion. At the end of 24a nothing is installed and every guard is in
place and demonstrated red. Installing a tool before its fence exists is how the fence ends up shaped
around the tool.

**Wave 24b — Sentry, server side.** Go SDK in `agentd` and the admin API, allowlist-constructed,
`Scrubber` chained, `trace_id` carried, three readiness states, the transmitted-payload fence with its
forbidden-shape fixture. Verified by a deliberate panic in a staging service: the issue appears, the
`trace_id` joins the span, and the payload contains none of the forbidden shapes.

**Wave 24c — Sentry, browser.** Both consoles and both BFF server halves. Verified by a deliberate
throw on a tenant route: the issue arrives with frames and no breadcrumb URLs, and the tenant CSP is
unchanged except for the reporting origin under `connect-src`.

**Wave 24d — consent.** Four categories, default-denied, refusal stored, withdrawal reachable, policy
version carried. Verified in a real browser: decline → zero third-party requests and no non-essential
storage; navigate three pages → not re-prompted; withdraw → collection stops on next navigation.

**Wave 24e — GA4 and Clarity on the public surface.** The two origins added to the allowlist with
budgets; the tags loaded nonced, after first paint, per category. Verified in a real browser: accept →
exactly the allowlisted origins and nothing else; the public page still renders with the platform API
stopped.

**Wave 24f — server-side console analytics.** BFF-emitted `surface_viewed` with the closed surface
enum and its allowlist; asserted that no browser request under `/app` leaves the own origin.

**Wave 24g — legal.** Sub-processor document published as a material version; consent grants
invalidated and re-asked; the claims fence updated so no shipped claim contradicts the configuration.

**Test layers**
- *Unit* — allowlist construction (both directions); consent state machine including `not-asked` vs
  `denied`; CSP header construction per prefix from the artefact.
- *Contract* — transmitted payload key set within the allowlist; forbidden-shape fixture produces no
  match on the wire; readiness reports the three states.
- *Build gates* — no hard-coded origin in middleware; no analytics/replay runtime in a
  tenant-reachable chunk; first-party ceiling; air-gapped no-external-origin; claims fence.
- *Browser acceptance (the real gate)* — the four observations in §9.4, on both consoles, in both
  themes, at the viewport floor.
- *Regression pointers* — one named test per amended commitment in §2.3, each naming the requirement
  it defends.

---

## 13. Success metrics & acceptance criteria

**Exit checklist**

- [ ] With no ids and no DSN configured: zero third-party requests, zero warnings, readiness reports
      `absent` for all three, on both consoles and every service.
- [ ] `/app/**` and every operator route: CSP contains `default-src 'self'` and names no third-party
      origin except the reporting origin under `connect-src`; asserted per prefix.
- [ ] A rendered browser on a tenant route with dev tools open: every request targets the console's
      own origin.
- [ ] Public surface, consent declined: zero third-party requests, no non-essential cookie or storage
      entry, full page function, no re-prompt across three navigations.
- [ ] Public surface, consent granted per category: exactly the allowlisted origins for the granted
      categories, each within its transfer budget.
- [ ] Withdrawal stops collection on the next navigation with no sign-out.
- [ ] A deliberate server panic and a deliberate browser throw each produce an issue carrying
      `trace_id`, `release`, `surface`, `error.code` and frames — and the transmitted bytes contain
      none of the forbidden-shape fixture's values.
- [ ] The transmitted key set is a subset of the allowlist, and every allowlist entry is populated by
      something (both directions).
- [ ] A Clarity or GA4 runtime introduced into a tenant-reachable chunk fails the build, naming the
      chunk.
- [ ] A hard-coded origin in either `middleware.ts` fails the build.
- [ ] The air-gapped package build asserts zero external origins.
- [ ] A DSN pointed at an unreachable host: readiness `degraded`, one log line per interval, no
      request-path latency change, no failed request.
- [ ] P0 golden `config_hash` vectors reproduce byte-identically.
- [ ] Every fence in §6 has been demonstrated red by a deliberate violation.
- [ ] The sub-processor document is published, versioned, and named on the legal surface; the claims
      fence passes.

**Metrics that would tell us this phase worked**
- A browser exception in production is seen by an engineer **before** a customer reports it — at least
  once, on purpose, as the proof.
- Median time from "the console is blank" to a `trace_id` drops from *manual across three systems* to
  one string.
- The next surface-level product decision cites a usage number instead of a preference.
- Consent decline rate is *known* — whatever it is. Not knowing was the problem.

**Metrics that would tell us it went wrong**
- Any customer-facing figure traceable to GA4.
- Any third-party request observed under `/app`.
- A CSP exception added to a prefix other than the public one.
- A forbidden shape found in a stored Sentry event.

---

## 14. Open questions

1. **Does the public surface's consent banner need a jurisdiction split?** The design asks everyone,
   everywhere, which is stricter than several jurisdictions require and simpler than the alternative.
   Confirm we are content to be stricter than the floor rather than geo-branching.
2. **Sentry organisation topology.** One project per surface (`agentd`, `admin-api`, `console`,
   `admin-console`) or one project with a `surface` tag? Per-project gives independent rate limits and
   alert routing; one project keeps a cross-surface incident in one place. Leaning per-project for the
   two Go services and one shared for the two browsers.
3. **Retention.** What is the shortest Sentry retention that still lets us fix an intermittent defect —
   30 days, or 90? Shorter is better for a payload we have gone to this much trouble to minimise.
4. **Do operator-console browser errors need a distinct project?** An operator-surface exception is
   internal, higher-signal and lower-volume; mixing it with customer-surface noise may bury it.
5. **The release/source-map step in the P20 pipeline.** Source maps are uploaded for our hosted
   deployment only and must not enter an installable package. Confirm the exact pipeline stage, and
   that the packaging job asserts their absence rather than relying on the build order.
6. **Is `error_diagnostics` genuinely consent-gated on the public surface?** Its payload carries no
   personal data by construction, so an argument exists for treating it as essential. Included as a
   category here because "we decided our error reporter does not need your permission" is a sentence we
   would rather not have to defend.
