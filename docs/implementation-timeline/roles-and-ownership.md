# Roles & Ownership — Applying the Six Senior Workflows

This timeline is not a generic project plan. Each phase is decomposed through the lens of six
senior-role operating playbooks (installed as Claude skills). Each playbook carries its own
internal discipline; this document states what each role **owns** on this project and which of
its internal phases apply where.

The organizing principle across all six is the same one the AI Engineer playbook states most
sharply: **you cannot improve what you cannot measure** — so the Metrics substrate and the eval
harness are treated as load-bearing infrastructure, not reporting.

---

## 1. Senior System Designer — *numbers before boxes*

**Owns:** the Workflow IR schema, the metric-event schema, the data model + storage choices, the
config-hash/lineage scheme, capacity estimation, and the typed I/O contract design that makes
re-arrangement safe.

Its internal workflow maps directly onto the platform's design spine:

| Playbook phase | Where it applies here |
|---|---|
| 1. Clarify requirements (functional vs non-functional) | P0 — separate "discover nodes" (functional) from scale/latency/consistency/cost (non-functional) |
| 2. Estimate scale (back-of-envelope) | P0 — repos/day, nodes/repo, runs/variant × seeds, traces/run, metric cardinality → sizes Postgres vs TSDB vs span store |
| 3. Define the API / interface contract | P0 & P2 — the IR schema **is** the contract every subsystem satisfies; also the typed per-node I/O contract (P5) |
| 4. Data model + pick storage | P0 — three stores by shape: spans (OTel), metrics (TSDB), eval results (Postgres); blobs content-hashed in object store |
| 5. High-level architecture | P0 — the request/run path end to end (see README §1) |
| 6. Deep-dive bottlenecks | P2.5/P5 — metric cardinality & the reorder-validation engine are the two hardest parts |
| 7. Scale, reliability, failure story | P6 — how the autonomous loop degrades safely; queue semantics for run fan-out |
| 8. State trade-offs explicitly | Every phase — e.g. static-vs-dynamic discovery, strong-vs-eventual on eval results |

**Highest-leverage decision it makes:** the event tagging contract and the typed I/O contract,
both in P0. Both are cheap to design early and ruinously expensive to retrofit.

**On billing (P7):** designs the **metering data model** — how per-call cost metrics roll up into
**LLM spend under management (SUM)**, the billable value metric — as a derivation over the same
P2.5 telemetry substrate (no parallel counter to drift out of sync), plus the entitlement/plan
schema that gates features by plan and automation level.

**On the operator console (P8):** designs the **privileged read-model** the internal Admin &
Operations Console is built on — the cross-tenant projection over P2.5 (metrics/cost), P4/P6
(jobs/fleet), P6 (autonomous audit + kill switch) and P7 (tenants/billing/entitlements) that
adds **no new pipeline**, only a read + command layer. Owns the **admin RBAC + append-only audit
data model** (who may do what to which tenant, and the immutable record of every operator action,
including impersonation). The console is the platform's highest-blast-radius surface, so the
command side is modeled explicitly around reversibility and least privilege — distinct from the
per-tenant customer Web dashboard.

---

## 2. Senior Backend Dev — *explore → design → implement → test → harden → review*

**Owns:** the Discovery Engine (AST parsing, signature registry, user-declared entrypoints),
the Configuration Layer source-transformation engine (codemod), the registries
(model/prompt/skill/context), the Runtime loader+executor, and all persistence code.

Its seven phases apply per service. The playbook's four backend realities — *shared persistent
state, concurrency, partial failure, contracts outlive code* — are exactly this system's hazards:

- **Contracts outlive code** → the IR schema and Variant Spec are public contracts; design them
  additively (P0, P2). Registry entries are versioned git-like so variants stay reproducible.
- **Idempotency** → a re-run of the same `config_hash` + seed must be reproducible and must not
  double-charge providers or double-write metrics (P2, P2.5).
- **Model invariants into the schema** → uniqueness on `config_hash`, FKs from eval results to
  variant/node/case, non-null tags — the DB enforces the tagging contract application code will
  forget (P0, P4).
- **Migrations as rollouts** (expand-migrate-contract) → registries and IR schema evolve while
  older variants still resolve (all phases).
- **Harden** → sandbox is not optional (P3); parameterize provider calls; never log prompts with
  secrets/PII; timeouts+backoff on every provider call.

**On billing (P7):** co-leads the **Billing, Metering & Entitlements** subsystem with DevOps.
Owns the metering aggregation service, the entitlement-enforcement layer (gating by plan +
automation level), and subscription/verified-savings billing integration. The same idempotency
and invariant discipline applies: a re-processed usage window must not double-bill, and metered
SUM is a read over the P2.5 substrate — the platform never resells tokens, customers use their
own provider keys.

**On the operator console (P8):** **lead** (with Frontend + DevOps). Builds the admin API behind
the console — the tenant / plan / entitlement administration services, billing-operations
endpoints, and the privileged command handlers over the fleet (jobs/queue) and the global safety
controls (incl. the autonomous-optimizer kill switch). Enforces **RBAC + least privilege** on
every operator action and writes an **append-only audit** record for each, impersonation included.
The same backend realities bite harder here: every command is cross-tenant, so authorization is
checked server-side per action, invariants (which admin role may touch which tenant) are modeled
into the schema, and destructive operations are reversible or explicitly flagged as not.

---

## 3. Senior AI Engineer — *evals before optimization*

**Owns:** the Evaluation Harness, eval-set generation, LLM-as-judge/graders, the Pattern
Classifier's LLM fallback, the Analysis & Improvement Engine (attribution, diagnosis, proposals,
verification), and the autonomous optimizer's objective logic.

Its ten-phase workflow is the backbone of the **Intelligence** half (P4–P6). The core rule —
*build the eval harness before you optimize* — is why **P4 precedes P4.5/P5.5/P6** on the
critical path. Direct mappings:

| Playbook phase | Applies at |
|---|---|
| 3. Build the eval harness FIRST | **P4** — nothing downstream is trustworthy without it |
| 4. Prompt **and context** engineering | P5.5 change operators (prompt rewrite, context-policy swap) |
| 6. Agency + the four disciplines | Discovery/classification of agentic patterns; verifying agent nodes |
| 7. Observability | consumes the P2.5 substrate |
| 8. Test & harden (adversarial, injection) | eval-set edge cases; sandbox threat model (with DevOps) |
| 10. Ship safely; failures → new eval cases | P6 feedback loop: production failures re-enter the eval set |

The **four agentic disciplines** (context / harness / loop / memory engineering) are the
vocabulary the Pattern Classifier and Improvement Engine use to reason about discovered agents:
a "non-converging reflection loop" is a *loop-engineering* defect; "context overflow before a
failing node" is a *context-engineering* defect. Each maps to a change operator.

**Non-negotiable it enforces everywhere:** diagnosis proposes, **verification decides**. No
single unverified LLM opinion drives an automated change; judges are calibrated against
human-labeled subsets and their agreement is reported alongside the metric.

**On billing (P7, wave 7b):** owns the **verified-savings computation** that feeds gainshare
billing. Only savings proven through the P5.5 verified-delta ledger are billable — the same
"verification decides" rule that governs proposals also governs what the customer can be charged
for; unverified savings are never billed.

**On the operator console (P8, wave 8b):** **supports** — owns the **autonomous-fleet oversight**
view. Defines what operators need to see and control across every tenant's autonomous loops: the
cross-tenant audit trail, per-tenant and global constraint state, and the semantics of the
**global kill switch** (what it halts, how in-flight applies are drained, how it degrades safely).
The same non-negotiable carries over — the console lets an operator *stop* or *constrain* the
autonomous loop fleet-wide, never silently *approve* an unverified change on a customer's behalf.

---

## 4. Senior DevOps Engineer — *blast radius, reversible, observable, least-privilege*

**Owns:** the OTel instrumentation + trace/metric storage, the sandbox/isolation layer, the run
queue, budget/alert gates, CI/CD, and the autonomous loop's operational guardrails.

Its prime directives map onto the project's sharpest risks:

- **If it isn't observable, it isn't done** → the reason Metrics (P2.5) lands *right after* the
  first Runtime, not late. The substrate is designed once, in P0, alongside the System Designer.
- **Least privilege / secrets never touch repo·logs·terminal** → the sandbox runs discovered
  repo code and tools as untrusted, with no ambient credentials (P3); provider keys via a
  secrets manager, never in traces.
- **Blast radius before implementation** + **reversible or say it isn't** → the autonomous
  optimizer (P6) gets hard constraints, a kill switch, an audit trail, and rollback before it is
  allowed to apply anything.
- **Automate the second time** → run fan-out, seed sweeps, and regression checks become pipeline
  steps, not manual runs.

Its five-step task loop (Frame → Plan → Execute → Verify → Close the loop) governs each infra
change, and its routing table (cicd / iac / containers / observability / incident / security)
is the reference set for the platform's own deployment.

**On billing (P7):** co-leads **Billing, Metering & Entitlements** with Backend. Owns the
metering pipeline's operational reliability (accurate, auditable usage counters — money depends
on them), the delivery surfaces' deploy/ops (CLI/CI, Git App/bot, hosted dashboard), and the
least-privilege posture for provider keys, which stay the customer's and never transit the
platform as resellable tokens.

**On the operator console (P8):** co-**lead** (with Backend + Frontend). Owns the console's
security posture end to end — **SSO+MFA** on the admin identity, the RBAC enforcement points, and
the deploy/ops of the highest-blast-radius surface in the platform. Its prime directives apply at
full strength: *blast radius before implementation* (fleet-wide commands and the global kill
switch get guardrails, confirmation, and rollback before they ship), *reversible or say it isn't*,
and *if it isn't observable it isn't done* — the cross-tenant observability views are read off the
same P2.5 substrate, and every operator action lands in the append-only audit log.

---

## 5. Senior Frontend Dev — *match the codebase, smallest correct change, a11y & perf are requirements*

**Owns:** the graph/node editor UI, the per-node config editor, the leaderboard, the dashboards
and trend/Pareto views, and the Variant-Spec diff view.

Applies from P2 (a minimal run/inspect view) and becomes a lead in **P4** (leaderboard,
scorecards) and **P5** (the interactive graph editor where users add/remove/reorder/swap nodes).
Its phase discipline lands as:

- **State & data** — model loading/error/empty/success as first-class for every long-running
  run, eval, and optimization job; avoid derived state that drifts from the run's true status.
- **Accessibility as a gate** — keyboard-operable graph editing, labeled controls, contrast on
  score/heatmap encodings (coordinate with the dataviz skill for chart color).
- **Performance** — the graph and leaderboard render potentially large IRs and many variants;
  virtualize lists, memoize deliberately, keep the node canvas responsive.
- **Verify before done** — actually run the UI against a live variant before declaring a view
  complete.

**On billing (P7):** owns the **billing / usage UI** in the hosted dashboard — the SUM-under-
management meter, plan/entitlement state, seat & retention limits, and the paywall/upgrade
surfaces. Loading/error/empty/success states apply to usage and invoice views as much as to
runs; a stale or wrong usage number is a money bug, not a cosmetic one.

**On the operator console (P8):** co-**lead** (with Backend + DevOps). Builds the **internal
back-office UI** — tenant/plan/entitlement admin, billing operations, the fleet/jobs views,
cross-tenant observability, and the global safety controls. It is a **separate app from the
customer Web dashboard**, behind admin identity + RBAC, and its states matter more, not less: a
dangerous cross-tenant action (impersonate, change entitlements, hit the kill switch) must render
its blast radius, require deliberate confirmation, and never fire from an ambiguous or stale view.
Accessibility and performance stay gates even though the audience is internal operators.

---

## 6. Senior Product Designer — *anchor to the outcome, match effort to certainty*

**Owns:** the end-to-end UX — the automation-level model (Advisory / Assisted / Autonomous), the
user journeys (import repo → inspect graph → configure → run → compare → diagnose → apply), the
IA for registries and leaderboards, and the engineering handoff specs.

Runs ahead of Frontend in each UI-bearing phase. Its nine phases apply most heavily at the two
UX inflection points:

- **P5 re-arrangement** — the riskiest interaction in the product. The designer's job is to make
  an *invalid* reordering legible: surface the typed-contract mismatch, offer the auto-inserted
  adapter, and never let "drag to reorder" silently produce a broken workflow. Design the unhappy
  path first.
- **P6 automation levels** — a governance UX. Each level (Advisory → Assisted → Autonomous) is a
  different trust contract; the designer defines how a user grants authority, sees the audit
  trail, sets constraints, and hits the kill switch.

Its quality bars — *design the unhappy path, content is the interface, name the tradeoff* — apply
to every screen: diagnosis cards must show *why* (the failing cases as evidence), not just *what*.

**On billing (P7):** owns the **packaging / paywall UX** — the surfaces→entitlements mapping made
legible (what each named plan unlocks, how automation level gates Autonomous auto-merge), the
upgrade path, and the **gainshare-consent** flow (7b) where a customer explicitly agrees that only
**verified** savings are billable. Plans are referenced by name; prices are configuration, never
hard-coded into a screen.

**On the operator console (P8):** **supports** — owns the **back-office UX and the
dangerous-action patterns**. The console's users are internal operators, but the same discipline
applies: *design the unhappy path first*. Defines the consistent pattern for high-blast-radius
actions (impersonate-with-audit, edit a tenant's entitlements, pull the global kill switch) —
what confirmation, scoping, and after-the-fact audit visibility each requires — and *names the
tradeoff* on every one, so an operator always sees the blast radius before acting. Keeps the
operator console legibly distinct from the customer Web dashboard so the two are never conflated.

---

## Ownership at a glance

| Subsystem | Lead role(s) | Supporting |
|---|---|---|
| Workflow IR + schemas | System Designer | Backend, AI |
| Discovery Engine | Backend | AI, System Designer |
| Config Layer + registries | Backend | System Designer, AI |
| Runtime (loader/executor/sandbox) | Backend + DevOps | System Designer |
| Metrics & Observability substrate | DevOps | System Designer, AI |
| Pattern Classifier | AI Engineer | System Designer, Frontend |
| Eval Harness + eval-set gen + scoring | AI Engineer | System Designer, Frontend |
| Analysis & Improvement Engine | AI Engineer | System Designer, DevOps |
| Autonomous optimizer | AI Engineer + DevOps | Product, System Designer |
| Graph/config UI, leaderboard, dashboards | Frontend | Product Designer |
| UX flows, automation levels, handoff | Product Designer | Frontend, AI |
| Billing, Metering & Entitlements (P7) | Backend + DevOps | System Designer (metering data model), AI (verified savings), Frontend (billing UI), Product (packaging/paywall) |
| Admin & Operations Console (P8, internal) | Backend + Frontend + DevOps | System Designer (read-model + RBAC + audit data model), Product (back-office UX / dangerous-action patterns), AI (autonomous-fleet oversight) |
