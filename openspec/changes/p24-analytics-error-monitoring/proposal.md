# P24 — Product Analytics & Error Monitoring

Product rationale: [`../../../docs/prd/P24-analytics-and-error-monitoring.md`](../../../docs/prd/P24-analytics-and-error-monitoring.md)

## Why

The platform records what its own machinery did in exhaustive, reproducible detail and records almost
nothing about the people in front of it. P2.5 answers "did the resolver emit a mismatched
`config_hash` on tenant X's run at 04:12". Nothing answers "did anyone read the boundary statement on
the public page", "which of the eleven `/app` surfaces shipped by P13–P18 are actually used", or — the
one that costs us most — "did a client-side exception fire in production". There is no browser error
pipeline at all: no `window.onerror`, no unhandled-rejection handler, no report of a chunk that failed
to load. On surfaces whose specific failure mode is *renders correctly, does nothing* (a CSP that
refuses a script, a hydration failure, a nonce that did not arrive), that gap is the expensive one.

Three things make now the moment. **P20 shipped installable packages**, so the install page moved out
from behind the login and first-run onboarding is walked by strangers whose failures we hear about only
if they choose to tell us. **P21 puts a checkout on the public surface**, and a funnel nobody can
measure is a funnel nobody can fix. And **the error budget is now being spent by two Next.js
applications** across ~20 routes, each carrying a per-request nonce CSP, a payload ceiling and a shared
design system — mechanisms that fail invisibly in a browser with `next build` green.

This change is unusual for this program: every other phase added a capability the platform's posture
permitted. **This one modifies a posture that is currently enforced by tests.** `default-src 'self'`
is set per request in both consoles' `middleware.ts` — whose own comment says an analytics tag "does
not render, it is REFUSED" — and two live assertions require the shipped CSP to contain no `https://`
origin at all (`web/console/tests/security.test.mjs:286`,
`web/console/tests/public-surface.test.mjs:156`). P9 FR35 and a shipped `console-marketing-site`
scenario say the same thing in the specs. The honest way to install analytics is therefore to amend
those requirements **narrowly, by route prefix, out loud** — not to widen a regex — and to leave the
guard stronger than we found it.

## What Changes

**Deployment posture**
- All three integrations are **absent unless configured**, and absence is a supported, tested,
  **silent** configuration rather than a degraded one.
- **Absent is the default on every substrate except the platform's own hosted deployment.** Compose,
  Kubernetes and air-gapped artefacts carry no measurement id, project id or DSN, and the air-gapped
  package asserts **zero external origins** at package-build time.
- Each integration reports one of three named states on the existing readiness surface — `absent`,
  `configured`, `degraded` — never a boolean. No integration is a startup dependency.

**Consent**
- Four named categories: `essential`, `product_analytics`, `session_replay`, `error_diagnostics`. All
  non-essential default to **denied**; nothing loads and no non-essential storage is written before a
  grant.
- A **refusal is stored as a refusal**, distinguishable from "not yet asked", and is not re-prompted
  until the consent policy version changes. Withdrawal is reachable from every gated page and takes
  effect on the next navigation with no sign-out. Declining leaves every function intact.
- Analytics consent is **not** written into the P23 `consent-records` ledger — that ledger is
  statutory, append-only and survives identity erasure, which is the opposite lifecycle to a revocable
  per-visitor preference.

**Product analytics (GA4)**
- Browser tag on the **public surface only**, gated on `product_analytics`.
- **No browser tag under `/app/**`, `/api/**`, or anywhere in the operator console.** Console usage is
  emitted **server-side from the BFF**, payload constructed from an allowlist, with the surface
  identified by an id from a **closed enum** — never by its URL, because a URL under `/app` carries
  variant, run, node and tenant identifiers.
- **No analytics figure may become a business number** — not on a customer-facing surface, not in an
  invoice, not in a claim. Asserted, not documented.
- No cross-site identifier, advertising identifier, remarketing audience or conversion pixel.

**Session replay (Clarity)** — **breaking against a shipped requirement, and a hard refusal elsewhere**
- Public surface only, gated on `session_replay`, with masking **on by default**.
- **Refused on `/app/**` and on every operator route**, structurally (unreachable from those layouts,
  origin absent from those prefixes' CSP, build failure if the runtime appears in a reachable chunk).
  The refusal is reachable by no plan, role, entitlement, flag, variable or parameter.

**Error monitoring (Sentry)**
- Go SDK in `agentd`, the admin API and both BFF server halves; browser SDK on all three web surfaces.
  **Not** in the `heros` CLI.
- Events are **constructed from a named allowlist**, not serialized-then-scrubbed — the
  `internal/runlink` pattern, for the same asymmetry reason. `telemetry.Scrubber` then runs as an
  independent second guard.
- **Error message bodies are dropped** unless the value is drawn from the central `error.code` enum;
  so are request bodies, headers, query strings, breadcrumb/fetch/navigation URLs, console output, DOM
  breadcrumbs, local variables, non-platform source context, environment values, credentials, prompt /
  completion / source / diff text, hostnames, server names, IP addresses, emails and tenant names.
- Every event carries the **existing** `trace_id`; no second correlation identity is minted.
- Performance tracing and profiling are **off**. Fail-static, out-of-band, never a startup dependency,
  never a request-path failure. Browser events go **direct** to the named origin — no BFF tunnel.

**The third-party-origin fence** — the guard gets stronger
- The CSP splits **by route prefix**, following the precedent already set for `Cache-Control` in both
  `next.config.mjs` files. **`/app/**`, `/api/**` and every operator route keep `default-src 'self'`
  and gain a per-prefix assertion they did not have.** The public prefix names only origins from a
  checked-in allowlist.
- **P9 FR35 is MODIFIED**, narrowly and explicitly: the public surface's absolute no-third-party-origin
  rule becomes "none other than the allowlisted analytics origins, each loaded only under its consent
  category". The tenant and operator rule stays absolute.
- The origin allowlist is a **single checked-in artefact** (origin, integration, consent category, CSP
  directive); a hard-coded origin in middleware fails the build. Asserted in **both** directions — a
  permitted origin nothing loads is a permission nobody asked for.
- **The payload ceiling is extended.** `scan-bundle.mjs` measures only `.next/static`, so today it
  would stop a 3 D library and not notice three trackers — after this change the ceiling would mean
  *less* than before. A **per-origin transfer budget**, measured in a real browser during acceptance,
  fails with the origin and overage named; and the decorative-runtime scan gains its inverse — an
  analytics, replay or error-reporting runtime in a tenant- or operator-reachable client chunk fails
  the build.

**Legal (extends P23)**
- A versioned **sub-processor document** names each processor, the categories it receives, the surfaces
  it runs on and its jurisdiction. A material version invalidates the affected consent grants. The
  existing claims fence fails the build on a tracking claim the shipped configuration contradicts.

## Impact

- **Affected capabilities (new)**: `telemetry-deployment-posture`, `analytics-consent`,
  `product-analytics`, `session-replay`, `error-monitoring`, `third-party-origin-fence`.
- **Affected capabilities (modified)**: `console-marketing-site` (P9 FR35 — the no-third-party-origin
  requirement and the not-tracked-before-consent scenario), `legal-documents` (P23 — a sub-processor
  document kind).
- **Affected code/systems**:
  - `web/console/src/middleware.ts`, `web/admin-console/src/middleware.ts` — CSP built from the shared
    artefact, split by prefix.
  - `web/console/next.config.mjs`, `web/admin-console/next.config.mjs` — header prefixes.
  - `web/console/scripts/scan-bundle.mjs`, `web/admin-console/scripts/scan-bundle.mjs` — the inverse
    runtime scan.
  - `web/console/scripts/accept.mjs` — the per-origin browser transfer budget.
  - `web/design-system/` — one shared origin/consent artefact both consoles read, plus a drift check;
    a trend-ledger entry recording what was accepted and what was refused.
  - New Go package for the error-event allowlist and construction, chaining `internal/telemetry`'s
    `Scrubber`; initialisation in `cmd/agentd`, the admin API, and both BFF server halves.
  - `internal/api` readiness surface — three states per integration.
  - `deploy/` Compose, Kustomize base and overlays — absent everywhere, asserted for `airgapped`.
  - `internal/distribution` / the P20 release pipeline — source-map upload for the hosted deployment
    only, asserted absent from every installable package.
  - `docs/prd/README.md` — the new phase row.
- **Explicitly untouched**: no `Dimension`, resolver, gate, transform, scorer or eval change; no new
  table; no `config_hash` input. P0 golden vectors must reproduce byte-identically.
- **Dependencies**: P2.5 (substrate, `trace_id`, `Scrubber`, readiness), P9 (console, BFF, public
  surface, CSP, bundle fence), P8 (operator console), P11 (`internal/runlink` allowlist pattern and its
  never-permitted content list), P19 (three substrates), P23 (legal manifest, `material` versioning,
  claims fence).
- **Unblocks**: P21 (a measurable checkout funnel), P26 (an operator surface for reporting health and
  browser errors), and any evidence-based UX work on the public surface.
- **Numbering**: this change is `p24-…`; the operator-console phase that follows is **P26**. There is
  no `p25-…` because `p25` already denotes P2.5 in this repository (`/p25/monitor`, the Gantt id,
  `internal/api/monitor.go`), and reusing it would make the token ambiguous in the places an operator
  greps during an incident.
