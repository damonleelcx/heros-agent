# P24 — Design

Product rationale: [`../../../docs/prd/P24-analytics-and-error-monitoring.md`](../../../docs/prd/P24-analytics-and-error-monitoring.md)
§8. This file carries the decisions and the numbers; the PRD carries the narrative.

## Context

Three third-party products are being installed into a system that has, in code and in shipped specs,
committed to running none. The commitments are real and enforced:

| Commitment | Enforced by | Status after P24 |
|---|---|---|
| `default-src 'self'`, per-request nonce, `'strict-dynamic'` | `web/console/src/middleware.ts`, `web/admin-console/src/middleware.ts` | Unchanged on `/app/**`, `/api/**`, every operator route. Widened on the public prefix only. |
| Shipped CSP names no `https://` origin | `web/console/tests/security.test.mjs:286` | Becomes **two** assertions — absolute per non-public prefix, allowlist-bounded on the public prefix. |
| Public surface references no third-party origin | `web/console/tests/public-surface.test.mjs:151` | MODIFIED — allowlisted origins only, per consent category. |
| A visitor is not tracked before they consent to anything | `console-marketing-site` spec scenario | **Preserved verbatim.** Default-denied consent is what preserves it. |
| Shipped client JS under 1,400,000 bytes | `scan-bundle.mjs` × 2 | Unchanged, plus a new third-party per-origin transfer budget. |
| CLI carries no telemetry / analytics / machine id / hostname | `internal/clilink/upgrade_test.go:264` | **Unchanged.** No integration reaches the CLI. |
| Content never crosses the boundary; payload constructed from an allowlist | `internal/runlink/allowlist.go` | **Reused as the pattern** for both new payload types. |
| Secrets / prompts / completions / PII scrubbed at one chokepoint | `internal/telemetry/scrub.go` | **Reused as the second guard.** |

The design problem is therefore not "how do we add a script tag". It is: *which surface may carry
which tool, what may each payload contain, and what stops the exception spreading.*

## Decisions

### D1 — Configured only for the platform's own hosted deployment; absent by default everywhere else

Same binaries, three substrates: our hosted deployment, a customer's Compose/Kubernetes install, and an
air-gapped network. All three integrations are configured only for the first. Absence is the default
and is silent (no warning, no log line, no readiness noise).

The air-gapped assertion is at **package-build** time, not install time: the claim "this package
references no external origin" is produced by the same run that produces the artefact, so it cannot be
a stale README.

*Rejected:* one build with ids from the environment (a stray Helm value turns a customer's install into
a reporting client — silent, blast radius is their network); per-customer opt-in in the installer (asks
the customer to evaluate a decision they cannot, and every one declines); ship everywhere and rely on
the DSN being unset (the code path exists and the SDK is in the bundle; "unset by default" is one merge
from "set by default").

**Numbers:** 3 substrates, 3 integrations, 1 permitted combination set. The air-gapped assertion
expects exactly **0** external origins.

### D2 — Clarity is refused on `/app/**` and on every operator route

Session replay records the screen. The screen under `/app/**` renders prompt text, generated diffs,
node identifiers, model configuration and run output. The operator console's screen renders
cross-tenant aggregates, tenant names, active impersonation state and audit rows. A replay of
`/app/studio` is a legible copy of most of the never-permitted list in
`internal/runlink/allowlist.go`. Maintaining that allowlist while installing a recorder on the same
content is not a trade-off, it is a contradiction — first-priority security cannot be bought with
eighth-priority implementation convenience.

Enforced by three independent mechanisms, because one is a checklist:
1. Unreachable from the tenant and operator layouts.
2. The origin is absent from those prefixes' CSP.
3. A build failure if a replay runtime appears in a client chunk reachable from those routes.

*Rejected:* aggressive masking on tenant pages (our exposure is rendered read-model content, code
panels, diff views and SVG graph labels — a selector denylist over a surface that gains pages every
phase fails silently the first time somebody adds a page); per-tenant opt-in (an administrator cannot
consent for the developers whose prompts appear on the screen, and asking does not change what the
recording contains); "heatmaps only" (same script — configuration is not a boundary); build our own
(moves the risk without reducing it).

### D3 — Tenant analytics is server-emitted with a closed surface enum

No browser tag under `/app/**`, `/api/**` or in the operator console. The BFF emits the event
server-side; the surface is an id from a closed enum, never a path.

The BFF already exists to keep something out of the browser (the platform credential); keeping the
tenant's URL out of a third party's logs is the same argument on the same component. The closed enum is
what makes the guarantee auditable — you can read the complete set of reportable surfaces, which you
can never do for a URL.

*Rejected:* browser tag with URL redaction (denylist over paths that gain segments every phase); no
console analytics at all (the status quo, and the reason six axis phases shipped surfaces with no way
to learn whether they landed); **proxy the tag through our own origin to keep the CSP literally
`'self'`** — rejected on honesty grounds: it would make a third-party flow look first-party, and the
CSP's whole value is that it is a readable statement of where data goes.

### D4 — The CSP splits by route prefix

`/app/**`, `/api/**` and every operator route keep `default-src 'self'` verbatim and gain a per-prefix
assertion. The public prefix names only allowlisted origins. P9 FR35 is amended as an explicit
`## MODIFIED Requirements` delta.

The precedent is already in the tree with the argument attached: `next.config.mjs` splits
`Cache-Control` by prefix because the public surface genuinely contains something different, noting
that a per-page judgement "fails the first time somebody adds a page". Same split, same boundary, same
reason.

*Rejected:* relax the global CSP and widen the two tests (silently removes the guarantee from the
surface it was for); keep it absolute and forgo GA4/Clarity entirely (taken for the tenant prefix;
declined for the public prefix, where the cost is an unmeasurable commercial funnel arriving with P21);
report-only CSP (a policy that does not enforce is documentation).

**Shape of the built header (public prefix, all categories granted):**

```
default-src 'self';
script-src  'self' 'nonce-<per-request>' 'strict-dynamic';
connect-src 'self' <analytics-ingest> <replay-ingest> <error-ingest>;
img-src     'self' data: <analytics-pixel>;
style-src   'self' 'unsafe-inline';
font-src    'self';  object-src 'none';  base-uri 'self';
form-action 'self';  frame-ancestors 'none';
```

Non-public prefixes are byte-identical to today except `connect-src 'self' <error-ingest>`. Script
origins on the public prefix are reached through `'strict-dynamic'` from the nonced loader rather than
being listed, so `script-src` gains **no** host. `'unsafe-inline'` for scripts and `'unsafe-eval'` in
production remain refused on every prefix — an integration that needs either is refused, not
accommodated.

### D5 — Event payloads are constructed from an allowlist, then scrubbed

A default Sentry event is close to a worst case here: message, frames with source context, request URL
/ headers / body, environment, breadcrumbs carrying every fetch URL and console line, IP, hostname. The
most dangerous field is the most innocuous-looking: `fmt.Errorf("failed to resolve prompt %q", p)` is
an ordinary Go error and an exfiltration path.

`BeforeSend` **builds** the outbound event from a named list and discards everything else, then
`telemetry.Scrubber` runs over the result — two guards of different kinds, the same shape as
`server-only` plus `scan-bundle.mjs` on the credential surface.

The asymmetry is already written down in `internal/runlink`'s package comment: a denylist means a new
field is *sent* (silent, discovered by a customer); an allowlist means a new field is *absent*
(visible as a missing feature, discovered here). An error reporter receives every field any engineer
ever attaches to an error, from anywhere, forever — the strongest case for construction in the system.

**The error-event allowlist (initial, ratified):**

| Field | Category | Why it is structure, not content |
|---|---|---|
| `error.type` | classification | The exception class (`*runtime.Error`, `TypeError`) — a type name, not a value |
| `error.code` | classification | A value from the central `error.code` enum. The **only** permitted message-shaped field |
| `frames[].function` | location | Our own symbol name |
| `frames[].package` | location | Our own package path |
| `frames[].file` | location | Our own file path |
| `frames[].line` | location | A line number |
| `frames[].in_app` | location | Whether the frame is platform code; non-platform frames carry no source context |
| `trace_id` | correlation | The identity already on the span, the log and `X-Trace-Id` |
| `release` | provenance | The build the failure occurred in |
| `edition` | provenance | Which deployment shape — a label from a closed set |
| `surface` | provenance | Which surface — an id from the closed enum, never a URL |
| `runtime` | provenance | `go` / `browser` and its version |
| `level` | classification | `error` / `fatal` |

**Never permitted, and deliberately not expressible as a field:** message bodies (except an
`error.code`), request bodies, request/response headers, query strings, breadcrumb / fetch / XHR /
navigation URLs, console output, DOM breadcrumbs and click-target text, local variables, source context
for a non-platform frame, environment values, credentials, prompt / completion / source / diff text,
hostnames, server names, IP addresses, email addresses, tenant names.

**The analytics-event allowlist (initial, ratified):**

| Field | Why |
|---|---|
| `event.name` | From the central enum; an ad-hoc name fails the build |
| `surface_id` | Closed enum — never a URL, which under `/app` carries variant/run/node/tenant ids |
| `plan_name` | Plan **name** only (Free / Team / Business / Enterprise) — never a price, never a value |
| `edition` | Deployment shape, closed set |
| `release` | Build identifier |
| `occurred_at` | Timestamp, second granularity |

No tenant id, no principal id, no run/variant/node id, no path, no query, no referrer beyond
first-party, no free text.

*Rejected:* Sentry's own server-side scrubbing plus `send_default_pii=false` (happens after
transmission and after storage — the wrong side of the boundary, with patterns we guessed); our own
`BeforeSend` denylist (better, still a denylist, must be updated whenever the SDK adds a field); no
error reporting at all (the status quo, and why a production browser exception reaches nobody today).

### D6 — One correlation identity

Every event carries the existing `trace_id`. Sentry mints nothing. Adding an incident system usually
adds an incident identity, after which two systems hold half an incident each with no join key. The
platform already made this choice and stated it: every metric and trace event carries the same
identity, because a claim you cannot join to its evidence is not a claim.

*Rejected:* Sentry's event id as the operator handle (names the report, not the request — joins to
nothing); a new `incident_id` (a third identity to explain the relationship between two).

### D7 — Consent is per category, default-denied, and a refusal is a stored fact

Four categories; all non-essential default to denied; each independently grantable and withdrawable;
the grant carries the policy version it was given against; a refusal is stored as a refusal.

The shipped P9 scenario is "a visitor is not tracked before they consent to **anything**" — categories
are what make that survivable as integrations grow, because a visitor who accepted usage counting has
not accepted being filmed.

**State machine.** Per category: `not-asked → granted | denied`; `granted → denied` (withdrawal);
`denied → granted` (change of mind, user-initiated only). A material policy version resets every
non-essential category to `not-asked`. Nothing transitions on a navigation, a timer, or a
scroll — only on an explicit user action or a material policy version.

Storage: one first-party cookie carrying `{policy_version, per-category decision}`. **Not** the P23
`consent-records` ledger — that ledger is statutory, append-only, keyed to an immutable document hash
and survives identity erasure; a revocable per-visitor preference with no tenant has the opposite
lifecycle in every respect, and putting it there would mean a cookie choice outliving a deletion
request.

The operator console presents **no** banner: it is a staff surface governed by the internal
acceptable-use notice, and its only integration is error reporting, whose payload carries no personal
data by construction. That exception is stated in the notice, not inferred from the absence of a
banner.

*Rejected:* one accept/decline (lets session replay ride in on a page-view count); legitimate-interest
for analytics with consent only for replay (drops to the legal floor from a stronger published
posture — the kind of change a customer discovers rather than reads).

### D8 — Analytics may never become a business number

No analytics figure on a customer-facing surface, in an invoiced quantity, or in a claim. Asserted, not
documented.

The console's read-model rule already forbids the browser recomputing a statistical claim on the
grounds that it would be a second source of truth. GA4 is a *third*, held by a party we do not control,
sampled, ad-blocked and consent-gated — systematically wrong in a direction nobody can quantify. And
metering is explicit: SUM derives from **linked** runs, and the platform never infers or extrapolates.

This is also why P24 needs no idempotent reconciliation point: nothing here is a source of truth, so
there is no "A must be accompanied by B" invariant to reconcile. Worth stating, because the moment an
analytics number backs a business figure that stops being true.

### D9 — The payload fence is extended, or this change weakens it

`scan-bundle.mjs` measures `.next/static` — the JavaScript **our build** produces. A script from a
third-party host is not there. So the ceiling would stop a small 3 D library and not notice three
trackers, and after this change it would mean less than before.

Two additions:
1. A **per-origin transfer budget** for each allowlisted origin, measured in a real browser during the
   acceptance run, failing with the origin and the overage named.
2. The **inverse** of the decorative-runtime scan: an analytics, replay or error-reporting runtime found
   in a client chunk reachable from a tenant or operator route fails the build, naming the runtime and
   the chunk.

**Budgets (transferred bytes, public surface, per origin, gzip on the wire):** analytics tag ≤ 120 KB;
replay tag ≤ 80 KB; browser error SDK ≤ 100 KB. Total third-party ≤ 300 KB. Per-origin rather than
total, so one integration cannot grow into another's headroom without a decision. First-party ceiling
stays 1,400,000 bytes, unchanged.

*Rejected:* trust review to notice a fourth tracker (the script's own comment answers this: a rule with
a demonstrated failure rate needs a machine); budget total weight only (lets one integration absorb
another's headroom silently).

### D10 — Browser events go direct, not through the BFF

A tunnel would keep the browser talking only to its own origin, which superficially serves the
tenant-surface guarantee. It is refused: it makes the BFF — the component holding the platform
credential — accept and forward arbitrary client-supplied error material on a new unauthenticated
ingest path, and it hides the flow behind a `'self'` that we arranged specifically to mislead a reader.
Same dishonesty D3 rejects for GA4.

*Rejected:* tunnel to keep `connect-src 'self'`; tunnel with a server-side payload allowlist (now we
have built a second SDK to avoid naming an origin).

### D11 — One shared artefact, read by both consoles

The origin allowlist, the consent categories, the surface enum and the prefix rules live in **one**
checked-in artefact under `web/design-system/`, which both `middleware.ts` files read. A hard-coded
origin in either middleware fails the build, and a drift check fails if the two consoles' derived
policies disagree about a shared prefix rule.

Both consoles already have their own middleware, their own `next.config.mjs` and their own
`scan-bundle.mjs`, and this change touches all six. A rule copied into two files is a rule that will
differ in two files — this repository has the scar tissue for exactly that failure class.

Asserted in **both** directions: a surface may not load an origin the artefact does not allow, and the
artefact may not allow an origin no surface loads. A stale entry is a permission nobody asked for.

## Interfaces

```go
// Package erroreport is the P24 error-reporting boundary. Like internal/runlink, it never serializes a
// rich error object: it reads named fields off a source and writes them into a fresh event whose
// encoding is the exact bytes on the wire.
type AllowlistField struct {
    Name     string // wire key
    Category string // classification | location | correlation | provenance
    Why      string // the one-line justification a reviewer reads
}

var Allowlist = []AllowlistField{ /* the table in D5 */ }

// State is an integration's honest state. Three values, never a bool.
type State string
const (
    StateAbsent     State = "absent"     // not configured — a decision, and silent
    StateConfigured State = "configured"
    StateDegraded   State = "degraded"   // configured and failing; names the failure class
)

// Reporter is fail-static and non-blocking. Report never returns an error that a caller could
// mistake for a request failure, and never blocks on transmission.
type Reporter interface {
    Report(ctx context.Context, ev Event)  // ctx carries the existing trace_id
    State() (State, string)                // state + failure class when degraded
}
```

```ts
// The shared artefact both consoles read. Origins are data; middleware constructs the header from it.
export type ConsentCategory = "essential" | "product_analytics" | "session_replay" | "error_diagnostics";

export type AllowedOrigin = {
  origin: string;            // exact origin, no wildcard
  integration: string;       // which product needs it
  category: ConsentCategory; // what gates it
  directive: "connect-src" | "img-src";   // never script-src: 'strict-dynamic' reaches it
  budgetBytes: number;       // the acceptance run's per-origin ceiling
};

export type PrefixPolicy = {
  prefix: string;            // "/app", "/api", "/" ...
  thirdPartyOrigins: "none" | "allowlisted";
};
```

## Risks

| Risk | Mitigation |
|---|---|
| The CSP exception spreads from the public prefix to a tenant prefix | Per-prefix assertions that name the requirement they defend; the tenant rule is asserted specifically for the first time; the allowlist is checked in both directions |
| Clarity enabled on a tenant page by a well-meaning change | Three independent mechanisms (layout reachability, prefix CSP, build-time runtime scan) — no single checklist |
| A leaked value reaches an error inbox through a message | Messages dropped unless `error.code`; construction not filtering; `Scrubber` as second guard; a red-demonstrated fence asserting the transmitted bytes against a forbidden-shape fixture |
| An analytics number becomes a business number | Asserted (not documented); the substrate stays the only source for SUM, savings, coverage, scores |
| An id or DSN reaches a customer artefact | Absent by default; air-gapped zero-external-origin assertion at package build; the seven-layer propagation list names the manifest layer |
| Consent theatre once a funnel exists | Equal-weight decline, full function on decline, refusal remembered, withdrawal on every gated page |
| The payload ceiling quietly stops meaning anything | Per-origin browser-measured transfer budget with the overage named |
| A reporting outage degrades the product | Out-of-band, fail-static, `degraded` on readiness, and a p99 regression is a defect |
| Two incident systems that cannot be joined | One identity — the existing `trace_id` |
| Sampling makes frequency unanswerable | Sentry is a defect **inbox**, not a rate source; every frequency question is answered from the substrate, where events are complete |
