# Project Context — LLM Agentic Workflow Evaluation & Configuration System

## Purpose

A platform that ingests a codebase, discovers its LLM call graph, exposes each call site
("node") as a configurable unit, and lets users remix models / prompts / skills / context
strategies, re-order nodes, then **execute, score, diagnose, and optimize** variants.

Four core subsystems (Discovery Engine, Configuration Layer, Runtime, Evaluation Harness) plus
three cross-cutting subsystems (Metrics & Observability, Analysis & Improvement Engine, Pattern
Classifier). See [`../docs/implementation-timeline/README.md`](../docs/implementation-timeline/README.md)
for the full timeline and [`../docs/implementation-timeline/source-plan.md`](../docs/implementation-timeline/source-plan.md)
for the source specification.

## Tech stack

- **Discovery** — tree-sitter + language ASTs (Go `go/ast` first)
- **Backend** — Go + Gin
- **Storage** — Postgres (variant specs, registries, eval results) + object store (content-hashed blobs)
- **Provider gateway** — LiteLLM-style unified abstraction
- **Telemetry** — OpenTelemetry (GenAI semantic conventions) → span store (Tempo/Jaeger) + TSDB (Prometheus/ClickHouse)
- **Execution** — queue for run fan-out; sandbox via subprocess/container
- **Consoles (P8 + P9)** — **two separate Next.js (App Router) + TypeScript applications, each on its
  own origin with its own BFF**: the **customer** console (P9, single-tenant) and the **operator**
  console (P8, cross-tenant, own admin identity + RBAC). They share the token **system** but not their
  appearance — the operator console carries distinct chrome so the two are never confused. Isolation
  between them is enforced by the **browser's origin boundary**, not by routing inside a shared app.
  The shared system lives in [`web/design-system/`](../web/design-system/README.md) — scale, spacing,
  type, radius, elevation, motion budget, density, neutral ramp, status palette and focus — beside the
  consoles rather than inside either one, so neither can fork it quietly. A surface's **identity** (its
  accent and chrome) lives in that console's own token layer. Colour, spacing, type-size and radius
  **literals are legal only in those two layers**; `npm run scan:tokens` fails the build on any other.
  The hazard palette (`--warn`, `--danger`) is **reserved for hazard** — a destructive control, an armed
  halt, an active impersonation, an alarming state — because danger is only legible while it is rare.
- **Web console (P9)** — Next.js (App Router) + TypeScript, running as a **BFF**: the Node server holds
  the platform credential and issues the browser an `HttpOnly` session, so **no API key ever reaches the
  browser**. Graph rendering is a deterministic hand-rolled SVG layout (no graph library — see
  [`changes/p9-web-console/design.md`](changes/p9-web-console/design.md) D7).
  *Until P9 lands, the shipped UI is five hand-written HTML files under `internal/api/static/` served
  via `go:embed` — no build step and no JS toolchain in the repo.*

## Conventions

- Every metric/trace event is tagged `{variant_id, run_id, node_id, case_id, seed, timestamp, config_hash}`.
- Everything is keyed by `config_hash` for reproducibility; large blobs are content-hashed in object storage.
- Static **nodes** (definitions) are distinguished from runtime **invocations** (execution instances).
- Registries (model/prompt/skill/context) are versioned, git-like, and referenced by ID from Variant Specs.
- Diagnosis proposes; **verification decides** — no unverified LLM opinion drives an automated change.
- Statistical honesty — multi-seed runs, confidence intervals; ties when CIs overlap.

### Web console (P9)

- **Read models are computed server-side; the browser derives nothing.** Scores, CIs, ties, ranks, gate
  outcomes, Pareto dominance and coverage percentages are rendered as received — a client-side
  recomputation would be a second source of truth for a statistical claim.
- **No platform credential in the browser.** The BFF holds the key; the browser holds an `HttpOnly`
  session bound server-side to a tenant. Request scope never comes from a client-supplied tenant id.
- **The BFF is a pass-through, not a brain** — no merging, re-ranking, reformatting or status
  translation of read models.
- **Failure classes stay distinguishable**: 503 subsystem-not-mounted, 404 not-found, and transport
  failure are three outcomes with three messages. A 404 is never mapped to a business state.
- **UI strings are English; `Intl` formatting is pinned to `en-US`** through a single swap point.
- **Acceptance is a rendered browser, not a green build** — a passing build, type-check and unit tests
  are all compatible with a page that renders nothing or the wrong subject.

### Prompt & model configuration (P10)

- **Prompt versions are immutable and content-addressed.** Editing publishes a new version; the prior
  one stays resolvable. No interface expresses mutation — the DB trigger is the last line, not the first.
- **A slot is bound explicitly or not at all.** Bindings (`literal` / `expr` / `env` / `input`) are
  declared and validated at **spec-resolve** time, never inferred. The engine does not guess which value
  belongs in a slot, and an unclaimed call-site value remains a refusal.
- **Apply mode is per node; `inline` is the default.** In `bound` mode the model, its params, the prompt
  version and `literal`/`env` bindings are **data**; wiring, skills, context policy and `expr`/`input`
  bindings are **code**, because they name things in the program's lexical scope
  ([ADR-004](../docs/adr/ADR-004-runtime-config-binding.md)).
- **An indirection never hides a value from review** — the resolved values ship in the same diff, or the
  transformation is rejected.
- **What ran is reconciled against what was requested** — the resolver emits its resolved `config_hash`
  per invocation; a mismatched run **fails** rather than being scored. Measurement runs are **pinned**.
- **Config resolution is fail-static** — last known-good stays in force, degraded is reported; never
  fail-open, never a startup dependency.
- **The studio is not an evaluator** — no score, rank, winner or interval, and no promotion path from an
  exploratory result. Only P4 ranks; only a P5.5 verified delta is a claim.

### Distribution surfaces (P11 CLI/CI, P12 forge delivery)

- **The CLI is offline-first and free on every plan.** Discovery, apply and eval work with no account and
  no network. Provider credentials are read from the customer's environment and **never** transmitted.
- **Egress is an allowlist, constructed — never a denylist, filtered.** A denylist fails *silently* when
  a field is added; an allowlist fails toward omission. Metrics and IR **structure** cross the boundary;
  prompt text, source, diffs, environment values and credentials never do, on any path including
  diagnostics.
- **Metering counts only what it observed.** SUM derives from **linked** runs; the platform never infers
  or extrapolates unlinked spend, and **link coverage** is displayed wherever a derived figure is shown.
- **Our availability never fails a customer's build** — a CI step reports and continues when the platform
  is unreachable, degraded, or slow. A **customer-configured gate** does fail it.
- **The platform holds no forge credential by default.** The customer's CI opens the pull request with
  the ephemeral, repo-scoped token it already holds; a hosted Git App is opt-in, per-repository,
  least-privilege and customer-revocable
  ([ADR-005](../docs/adr/ADR-005-forge-delivery-and-credential-posture.md)).
- **Delivery is downstream of verification, never a path around it**, is idempotent per
  `(config_hash, source_revision, target)`, and **never merges below Autonomous**.
- **Delivery is recorded append-only; `transform` stays immutable.** A transform is produced once; a
  delivery has a lifecycle. A merge is **observed**, never inferred from a pull request closing.

### Optimization axes (P13–P18 — Optimization Axis Expansion)

- **An axis is a `Dimension`, not a feature.** Every optimization axis is a field that resolves into
  `ResolvedNode` and therefore into `config_hash`; the eval harness scores it **without knowing the axis
  exists**. A new axis that needs an eval, scorer or metric change is designed wrong — reduction shows up
  in the existing `eval_tokens_total` / `eval_cost_usd` / `task_success` family, not a bespoke oracle.
- **`config_hash` is append-only-compatible.** A new axis field is additive and `omitempty`; the
  no-override (`none`) case SHALL hash byte-identically to before the field existed, so P0 golden vectors
  keep reproducing (the P10 `bindings` expand-contract precedent).
- **Modeled is not applied — and the gap is stated, never hidden.** Each axis declares its honest
  `EXISTS / PARTIAL / ABSENT` status. Where a call-site codemod is not yet safe, a node carrying that
  axis's override **SHALL be refused at transform with a typed `unsafeRewrite`, never silently dropped**
  (the posture `refuseSkills`/`refuseContext` already ship). A silently-dropped override would let a
  variant's `config_hash` be scored against unchanged source — a false result, so this is L1/L2.
- **Diagnosis proposes; verification decides — on every axis.** An axis operator only *proposes* a Variant
  Spec; a change is surfaced solely when it is verified better or cheaper on held-out data (P5.5). This
  holds identically for a prompt rewrite, a tool prune, a reorder, a context-policy swap, a memory
  strategy, or a heavier harness.

### Commercial model & entitlements

- The billable **value metric** is **LLM spend under management (SUM)**, aggregated from the P2.5 cost metrics — metering is a read over the telemetry substrate, not a parallel counter.
- **Plans-as-config** — plans are referenced by **name** (Free / Team / Business / Enterprise); prices and plan definitions live in configuration, **never in git**.
- **Entitlements gate by plan _and_ automation level** — a feature is unlocked only when both the plan and the automation level allow it (Autonomous auto-merge is Enterprise-only).
- Customers use their **own provider keys** — the platform **never resells tokens**.
- **Only verified savings are billable** — gainshare/verified-savings billing draws exclusively on the P5.5 verified-delta ledger; unverified savings are never billed.

### User-initiated change on an axis (P13 `authored-change`, consumed by P14–P16)

- **One spine, two origins.** A change originated by a **user** is derived, resolved, hashed, gated,
  transformed and (on request) scored by the **same** components that process an operator candidate.
  There is **no** authoring-only resolve, transform, or gate — a second apply path is a second place for
  every safety gate to be wrong, and the gates are the platform.
- **`Origin` is recorded, never hashed.** `config_hash` stays purely structural: a user-authored
  configuration and a byte-identical operator-proposed one hash the same and are the same measurement.
  Origin, actor, and tenant live on the candidate / transform / delivery record.
- **Every refusal that binds an operator binds a user identically, and there is no override.** No plan,
  role, entitlement, flag, or request parameter materializes a configuration the transform refuses. A
  refusal exists because the artifact would be wrong in a way the author cannot see at the moment of
  choosing; a human asking for it does not make the SDK, the slot, or the language match.
- **Refusal moves left.** A draft is **preflighted** before submission and returns exactly one of
  `admissible`, `refused` (named cause + offending node/field), or `not-yet-measurable` (named missing
  input). Preflight publishes nothing, writes no diff, and spends no eval budget. **A gate never refuses
  on ignorance — and never passes on ignorance.**
- **An authored change may apply; it may never claim.** It MAY be applied without a verdict — it is the
  customer's repository — and is stamped **`unverified`**: outside the verified-delta ledger, contributing
  zero to every aggregate improvement/savings/quality figure, and **never auto-merged**. `unverified` is a
  state the ledger filters on, not a badge a refactor can drop.
- **A user may author the change; a user may not author the evidence.** Case selection, held-out splits,
  seeds, and repetition counts stay platform-derived. Classifier labels are inputs to what may be
  authored, never outputs of it.
- **Selection is fail-closed; drafts never mutate their parent.** Authored values are chosen from what the
  platform sealed or discovered, never free text. A stale submission is a **named conflict**, not a lost
  update, and every authored change has a reversal that reproduces the parent `config_hash`
  **byte-identically**.
- **Authoring adds no egress and works offline.** The CLI authors with no account and no network, reaching
  the same verdict with the same typed cause text; prompt text, source, diffs, environment values and
  credentials cross no boundary on any authoring path, including preflight and diagnostics.

### Language coverage on an axis (P13 `language-coverage`, consumed by P14–P16)

- **Coverage is a total function, and absence is not a value.** Every axis publishes an entry for every
  language the discovery frontend registers and every form that axis binds against — the provider and SDK
  generation for a tool value, the registry row for a binding, the policy for a context selection, the
  statement form for a wiring move. A cell the axis cannot apply is **present with a named cause**. An
  absent row renders on every surface as *not applicable*, which is a claim about the customer's code; it
  is the one thing coverage data must never say by accident.
- **A refusal names which of three different things is missing.**
  `not-expressible-at-a-call-site` (the value does not exist until run time),
  `call-site-cannot-carry-it` (unpacked arguments, a run-time-assembled list, no row locator, a binding
  the frontend did not record), and `no-materializer-for-this-language` are answered by the platform's
  designer, the customer's engineer, and the platform's backlog respectively. They are distinguishable by
  a **stable identifier**, not by prose.
- **The most specific true cause wins, and the language question is asked last.** The order is: the
  change → the registry row → the call site's own source → the language. A call site refused for its own
  shape refuses **identically** after that language's materializer lands, which is exactly why naming the
  language would have been useless.
- **Every registered language is a target, and no gate is weakened to reach one.** A gap names the
  artifact that would close it (a form row, a list splitter, a statement resolver, a registry row, a
  frontend field). A coverage row is admitted only on **executable evidence** — the language's reparse
  assertion, plus the build gate wherever the change constructs source — never on a document, and never
  by relaxing a check for one language.
- **One coverage source, read by everything that states coverage.** The transform's refusal, preflight,
  the console, the CLI and every published table read the same source, asserted in **both** directions:
  a surface may not offer a cell the engine refuses, and the engine may not materialize a cell no surface
  offers.
- **The same override means the same thing in every language.** A language's materializer never applies a
  broader, narrower, or different interpretation; a divergence is a defect, not a per-language behavior.
  What differs per language is the *spelling*, never the *meaning* — which is why the shape of a bound
  skill comes from its sealed schema and the retention of a context policy comes from the shared
  selection code.
- **A coverage gap is not a plan boundary.** It is *not yet applied by the platform*, identical on every
  plan; no tier, role, flag, or setting materializes a cell the engine refuses. And a call site the
  platform will never apply does not borrow "not yet" — it is a fact about the source, with no "when".

## OpenSpec workflow

This project uses OpenSpec for spec-driven development. See [`AGENTS.md`](AGENTS.md) for the
format and rules. Each delivery phase (P0 → P30) is tracked as one change, and a change lives in
exactly one of three places:

- `specs/` — the **current truth**: 100 capabilities, folded from every deployed phase. Read this
  to learn what the system does today.
- `changes/` — phases **still open**. A change is here only while it has unfinished tasks.
- `changes/archive/<YYYY-MM-DD>-<change-id>/` — phases **deployed and folded**. Read these for the
  reasoning (`design.md`, `proposal.md`) behind a requirement that now lives in `specs/`.

Both steps happen together: folding without moving, or moving without folding, leaves `specs/` and
`changes/` each telling a different story. **P8 — Admin & Operations
Console** is the platform team's **internal operator** surface (its own admin identity + RBAC);
**P9 — Web Console** is the **customer-facing** dashboard, scoped to one tenant. The two are distinct
surfaces and must never be conflated: nothing in P9 crosses a tenant boundary, and no P8 capability is
reachable from P9.
