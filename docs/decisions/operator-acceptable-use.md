# Operator console — acceptable use, and what it collects

**Audience:** anybody with an operator account on the Heros platform. **Status:** internal. **Owner:**
platform operations.

This document exists because of one absence, and the absence is deliberate: **the operator console
presents no consent banner.** A reader who notices that cannot tell a decision from an oversight, so the
decision is written here (P24 task 4.9, design D7).

---

## 1. Why there is no banner

A consent banner is a control for a **visitor** — somebody who arrived, has no relationship with us, and
is being asked whether they agree to something before it happens. An operator is not that person. They
are an employee acting in that capacity, on a staff surface, under this notice.

Asking them to consent to their employer's error diagnostics would be consent theatre: the answer is not
theirs to give, refusing it would not change what the console does, and a banner on an incident console
is a control an operator learns to dismiss without reading — which is worse than no banner, because it
trains the dismissal reflex on the day a real one appears.

So the exception is stated here instead, and it is **one category wide**.

---

## 2. What the operator console collects, exactly

| | On the operator console |
|---|---|
| **Usage analytics** (Google Analytics) | 🚫 **Refused.** Not gated, not configurable — the analytics category is absent from the operator surface class in `web/design-system/third-party-policy.ts`, so no policy this console serves can name an analytics origin. |
| **Session recording** (Microsoft Clarity) | 🚫 **Refused**, by the same mechanism. |
| **Error diagnostics** (Sentry) | ✅ **On**, by this notice rather than by a banner. |

### Why session recording is refused here specifically

This console's screen renders **cross-tenant aggregates, tenant names, active impersonation state and
audit rows**. A recording of it is a copy of exactly the material the platform's egress allowlist exists
to keep inside a boundary, held by a party we do not control — and no masking configuration changes what
a recording contains. It is refused structurally, not switched off.

### What an error event from this console contains

The same thirteen fields as everywhere else, built one at a time from a named list:

`error.type` · `error.code` · `level` · `frames.{function,package,file,line,in_app}` · `trace_id` ·
`release` · `edition` · `surface` · `runtime`

And **not**: the error's message body (unless it is a value from the central `error.code` enum), the page
address, any request or response header, any query string, any breadcrumb — there is no breadcrumb
collection at all — console output, click-target text, local variables, environment values, credentials,
prompt or diff text, hostnames, IP addresses, email addresses, or tenant names.

The complete, generated list is
[`error-event-allowlist.md`](error-event-allowlist.md), rendered from the code by
`cmd/erroreportdoc` so it cannot drift from what is actually transmitted.

**No operator is identified in an error event.** There is no principal id, no session id, no name and no
address. What is transmitted says *this build, on this surface, failed this way, in this trace* — and
nothing about who was looking at it.

---

## 3. What this means for you, in practice

- **Nothing you do on this console is filmed**, and nothing counts your navigation for a third party.
- **A failure you hit is reported automatically**, with a stack and the trace id — the same trace id the
  span store and the structured log use, so an incident is one string rather than three piles.
- **You do not need to report a console error by hand.** If it produced a stack, it is already in the
  inbox. What is still worth reporting by hand is the failure that produced *no* error: a control that
  did nothing, a page that rendered stale, a number that looked wrong. Those are invisible to this
  pipeline by construction, and they are the expensive ones.

---

## 4. Acceptable use of the console itself

Unchanged by P24 and restated here so this document is the one an operator reads:

- Every privileged command is **permission-gated, reason-required and audited**. Do not share an operator
  session, and do not act on a tenant's behalf without an impersonation record.
- **Read freely; write deliberately.** The console is built so reading is fast and writing is slow, and
  the slowness is the feature.
- Cross-tenant views are themselves audited reads. Opening one to satisfy curiosity is a use this notice
  does not permit.

---

## 5. If the answer above ever changes

Turning any refused integration on for this console would mean editing the operator surface class in
`web/design-system/third-party-policy.ts` — a diff that says what it is doing, in a table with a
justification column. Three tests fail on it, in both consoles, naming the class and the category. That
is the intended cost.
