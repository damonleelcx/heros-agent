# Design — P9: Web Console

Product rationale: [`../../../docs/prd/P9-web-console.md`](../../../docs/prd/P9-web-console.md).
Companion documents in this change: [`feature-inventory.md`](feature-inventory.md) (the
no-feature-loss checklist that governs the port) and [`ui-ux-plan.md`](ui-ux-plan.md) (the UI/UX
improvement rules R1–R12).

## Context

The web surface today is five hand-written HTML files under
[`internal/api/static/`](../../../internal/api/static/), four served via `go:embed` from
`p2.go`, `monitor.go`, `p35.go`, `p4.go`. They have real virtues worth stating, because the port must
not lose them: **no build step**, **no dependency tree**, **deterministic rendering**, and a
consistently disciplined relationship to the server — every one of them renders a read model the Go
service computed, and none of them derives a statistic. `internal/evalboard/view.go` exists precisely
so the browser cannot fork the scoring rules. That invariant survives P9 unchanged.

What they cannot do is hold a credential. The page routes are public; the `/api/*` routes they call
require `X-API-Key` (`internal/auth/middleware.go`, `IsPublicPath` exempts only `/health`, `/metrics`,
`/`, `/static/`). Under `auth_mode=required` the pages render and every fetch 401s. Every other
problem in this change — no navigation, three forked palettes, accessibility on one page of five,
hand-typed identifiers, a hardcoded `'wf-demo'` default — is a product problem. This one is a security
problem, and it is what forces an architecture decision rather than a refactor.

Constraint carried from the repository's own discipline: **the acceptance bar is a rendered browser,
not a successful build.** That rule shapes the test strategy more than any framework choice below.

## Decision 1 — Next.js App Router + TypeScript, running as a Node BFF

The console is a Next.js application whose **server process is the BFF**. The browser holds an
`HttpOnly`, `SameSite` session cookie it cannot read; the BFF holds the platform API key in process
environment; the credential crosses exactly one boundary (BFF → platform API) and never reaches the
client. Request scope is derived **server-side from the session's tenant** — a client-supplied tenant
identifier can never widen it.

**Alternative rejected: static export (`output: 'export'`) embedded with `go:embed`.** This is
genuinely cheaper. It preserves the single-binary deployment, adds no runtime, no supervisor, no
second image layer, and no new health surface — a real win on **L4 高运维成本**. It was rejected
because a static bundle cannot hold a secret. Closing the browser-auth gap would then have to happen
inside the Go service (a session store, a cookie exchange, a CSRF story, a login surface) — the same
work, in a service whose job is not serving a UI — or not happen at all, which means either running
the console unauthenticated or shipping a platform credential to the browser. Both are **L1 安全**
degradations.

**Arbitration (八级法则).** L1 安全 vs L4 运维. L2 铁律: *the degradation of a higher level cannot be
bought with the convenience of a lower one.* L1 decides it; per L2 we do not fall back to the cheaper
option because of the lower-level convenience. The 运维 cost is **accepted and priced**, not waved
away — Decision 6 is the payment schedule.

**Alternative rejected: an SPA (Vite/React) served statically, with a small standalone Go BFF.** Same
credential property, one less runtime. Rejected on **L7 维护**: it splits the console across two
languages and two deployment stories for a benefit the Node process already provides, and the request
that started this work names Next.js. Recorded so a future reader knows it was considered, not missed.

**Not used:** server-side rendering of platform data, Server Actions against platform state, or any
Next.js server feature that would make the BFF stateful. See Decision 3.

## Decision 2 — One token set, derived from what already ships

All visual values come from a single token set. It is **derived from the existing dark palette**, not
invented: `#0f1419` page surface, `#1a2332` card, `#e7ecf3` text, `#3d8bfd` accent, and the status
triad already used consistently across `p2`/`p25monitor` (`#14352a` ok-green, `#3a2027` fail-red,
`#3d2f14` warn-amber, `#2e2140` halt-purple).

The three forks are reconciled with a **recorded winner per token**, not averaged into a fourth:

| Token | `p2` / `p25monitor` / `p35graph` | `p4board` | Winner | Why |
|---|---|---|---|---|
| `--muted` | `#8b9cb3` | `#8fa3bd` | `#8b9cb3` | 3 of 4 pages; no contrast advantage to the outlier. |
| `--line` | `#2a3545` | `#243247` | `#2a3545` | Same. |
| card radius | `10px` | `8px` | `10px` | Same. |
| chip radius | `4px` (`5px` on `p35graph`) | `999px` | `4px` | Majority, and square chips read as status badges rather than tags. `p35graph`'s `5px` is a drift, not a fork. |
| sizing unit | `rem` | `px` | `rem` | Respects user font-size preference — an accessibility property, so it wins on merit rather than on count. |
| font stack | 3-family | 5-family | 5-family | The outlier is strictly better: `-apple-system` and `"Segoe UI"` improve native rendering, and the mono stack's `SFMono-Regular`/`Menlo` render tabular data better. Winning on merit, not majority. |
| status vocabulary | `--ok`/`--warn`/`--bad`/`--halt` | `--good`/`--warning`/`--serious`/`--critical` | semantic names, one set | Two names for one concept is the fork itself. One set, chosen for legibility, applied everywhere. |
| chart series | — | `--series-1..4` | keep | Only `p4board` charts today; the series palette is real and is promoted into the shared tokens rather than left page-local. |
| `--llm` / `--ctl` / `--none` | `p35graph` only | — | keep as graph-domain tokens | Domain-specific and legitimately needed; they move into the token set instead of staying inline. Note `--llm` is `#c084fc`, the same value as `--halt` — the token set keeps both names because they mean different things. |

**Alternative rejected: adopt a third-party design system.** It would be faster to start and would
supply components, a11y primitives and dark-mode handling. Rejected because customers already look at
this product: re-skinning during a port makes it impossible to tell a lost feature from an intended
visual change, and it violates the standing rule against improvised visual decisions. A component
library may be adopted later *inside* these tokens; the tokens are the contract.

## Decision 3 — The BFF is a pass-through; the browser derives nothing

Two rules, one at each side of the BFF.

**Upstream side:** the BFF returns platform read models **unmodified**. It does not merge two upstream
calls into one response, does not re-rank, does not re-aggregate, does not translate a status, does not
decide what a value means. It authenticates, authorizes by session, forwards, and returns.

**Downstream side:** the browser renders statistics as received. Composite scores, confidence
intervals, tie determinations, ranks, gate outcomes, Pareto dominance, coverage percentages and
pattern confidences are computed in Go and rendered verbatim — no client-side computation, no
rounding before comparison, no client re-sort of a server-ranked field.

**Alternative rejected: a "smart" BFF that shapes responses for the UI** (merging the run record with
the monitor snapshot, pre-formatting numbers, normalizing error shapes). It would simplify components.
Rejected on **L5 不可演进**: it creates a second place where business rules live, and the first thing
it would absorb is the tie rule — at which point two implementations of P4's statistical honesty exist
and can disagree. The failure mode is silent and the diagnosis is expensive. A number the UI needs that
the server does not return is a **read-model change request to the owning phase**, not a client-side
computation.

This also constrains error handling: the BFF **forwards** the upstream failure taxonomy rather than
normalizing it. **503 not-mounted**, **404 not-found**, and **transport failure** are three different
things the user does three different things about, and the current pages already distinguish them
correctly. Collapsing them — including the tempting "map 404 to an empty result" — destroys
information the user needs.

## Decision 4 — SSE proxied, polling fallback preserved

`p25monitor.html` opens an `EventSource` first and falls back to polling **only if no message ever
arrived** — a deliberate design that survives a proxy which buffers or strips SSE. Both paths carry
forward. The BFF proxies the stream with flush semantics intact, closes the client stream when the
upstream closes, and does not batch events; the client falls back to polling on stream failure, and
**stops polling on the run record's status**, never on a node-derived condition.

**Alternative rejected: SSE only.** Simpler, one code path. Rejected on **L2 稳定** — the fallback
exists because customer-network intermediaries break SSE, and removing a shipped resilience behavior
during a port is exactly the feature loss this change is organized to prevent.

**Alternative rejected: WebSockets.** More capable, and would generalize to live boards later.
Rejected as scope: nothing today needs bidirectional transport, and it would add a second connection
model to operate for no current requirement.

## Decision 5 — TypeScript types generated from the Go view structs, with a CI drift gate

The console's data contract is the Go view types (`internal/evalboard.View`, the P3.5 `GraphView`, the
P2.5 `RunMonitor`, and the P2 `transformView`/`runView`/`submitResult`/`specError`). Types are
**generated** from them, checked in, and regenerated in CI with a diff that **fails the build**.

**Alternative rejected: hand-written types maintained by review.** Rejected on **L5/L6** and on
evidence: the failure mode of a drifted hand-written type is not a compile error, it is a blank cell
in production — a field renamed in Go becomes `undefined` in TypeScript and renders as an em-dash that
looks like legitimately absent data. A generated artifact with a gate turns a silent wrong answer into
a red build. This is the same reasoning that makes a checked-in generated artifact plus a drift gate
preferable to a `.gitignore`d build product.

The generator choice is open (see PRD §14 Q3); emitting JSON Schema from the view types and generating
from that composes with the existing [`schemas/`](../../../schemas/) discipline and is the current
preference.

## Decision 6 — One supervised console component; readiness aggregates it

This is the payment schedule for Decision 1's accepted 运维 cost.

The console ships as **one declared, supervised, health-checked component** with a pinned runtime
version and a lockfile-reproducible dependency tree. The platform's readiness signal **aggregates it**:
a healthy Go service in front of an unreachable BFF **does not report ready**, and the degraded
component is **named** on a readable endpoint.

**Alternative rejected: deploy the console independently and leave `/readyz` reporting only the Go
service.** Rejected on the health-signal rule: a readiness endpoint that reports ready while the
surface users actually reach is dead is a **lying health signal**, and a UI dashboard is never itself a
health judgement. The moment a second process exists in the request path, readiness has to cover it or
it is measuring the wrong thing.

Packaging (one container with a supervisor vs. two containers in one unit) is deliberately left to
implementation and should be recorded as an ADR when decided — the *requirements* above are what this
change fixes.

## Decision 7 — Keep the hand-rolled SVG graph renderer

`p35graph.html` implements a deterministic layout with behaviors that are deliberate, not incidental:
**back edges routed under the row** (with a dip below `max(sy,ty)`) so a Reflection loop is visible
rather than hidden behind forward edges; **region rectangles** computed from member node bounding
boxes and styled by label source (rule / llm / unclassified); **data vs. control edges** distinguished
by both dash pattern **and** arrow marker, so the distinction survives greyscale; node-scoped labels
rendered separately from region labels; and a container that **scrolls rather than shrinks**, so a
large graph is never silently compressed into unreadability.

**Alternative rejected: adopt a graph library.** It would supply pan, zoom, selection and auto-layout.
Rejected on **no feature loss**: every behavior above would have to be re-earned in the library's
model, and auto-layout would replace a deterministic layout with a heuristic one — a reproducibility
regression (NFR6). The current renderer's cost is bounded because the layout is a pure function of
`layer`/`order`, which the read model already provides.

**When to reopen:** if pan, zoom, node selection or subgraph focus become specified requirements. The
behaviors listed above are then **non-negotiable inputs** to the evaluation, not things to re-derive.

## Decision 8 — Nothing is deleted; cutover is a separate, gated step

The five existing HTML files and their `go:embed` handlers are **untouched** by this change. The
legacy pages keep serving until the console demonstrates parity item-by-item against
[`feature-inventory.md`](feature-inventory.md); removal is then a discrete task with an owner, removing
each page **together with** its Go handler so no route is left serving a stale asset.

The orphaned `internal/api/static/index.html` is **forward-ported, not deleted**. It has no handler and
its three endpoints do not exist, and it is Chinese-only — but its shape (queue → rationale → diff →
approve/reject) is the human-in-the-loop surface **P5.5** needs. P9 specifies that surface in English;
**it does not ship before the P5.5 API exists**, because a surface with no backing endpoint is how the
current orphan was created in the first place.

**Rationale:** deletion is a one-way door and reduces capability, so it requires explicit sign-off
rather than being folded into a port.

## Interfaces sketch

```
Browser ──HttpOnly session cookie──▶ BFF ──X-API-Key (server-held)──▶ agentd /api/*
                                      │
                                      └── SSE passthrough (flush preserved, no batching)

Session:  { session_id, tenant_id, issued_at, expires_at, revoked_at? }   # server-side; tenant is authoritative
Scope:    every upstream call is scoped by session.tenant_id             # never by a client-supplied id
Errors:   upstream {status, body} forwarded verbatim; transport failure is a distinct fourth outcome
Types:    Go view structs ──generate──▶ checked-in .d.ts ──CI diff gate──▶ build fails on drift
Health:   /readyz aggregates {platform, console}; degraded component named
```

## Risks

| Risk | Mitigation |
|---|---|
| The port silently drops a behavior | `feature-inventory.md` is written **before** any code and executed as a regression suite; the behaviors most at risk (SSE fallback, poll-termination source, virtualization threshold, keyboard wrap-around, chart table fallback) get named cases. |
| The BFF grows business logic | Decision 3 is a spec requirement with scenarios, not a convention; any merging/reinterpreting of read models is a review block. |
| A credential reaches the client bundle | Build-time gate over the shipped bundle plus a test asserting no key material in client artifacts — machine-enforced, because a rule that only turns a light red is the kind that holds. |
| The design system forks a fourth time | Single token set with a recorded winner per token, plus a gate on color/radius/font literals outside the token file. |
| Statistics drift between server and screen | Decision 3 forbids client computation, rounding-before-comparison and client re-sorting of server-ranked fields; the leaderboard test asserts rendered values equal response values. |
| The Node runtime becomes an unowned second deploy unit | Decision 6: declared, supervised, health-checked, readiness-aggregated, pinned, lockfile-reproducible. |
| Two UIs stay live and diverge | Decision 8's cutover is a dated task with an owner, gated on inventory parity — not deferred indefinitely. |
| Accessibility regresses to the current 1-of-5 level | Per-page gate: automated audit **plus** a keyboard-only pass; a page below the `p4board` level does not ship. |
