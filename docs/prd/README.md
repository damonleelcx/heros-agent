# Product Requirements Documents (PRDs)

One PRD per delivery phase of the **LLM Agentic Workflow Evaluation & Configuration System**.
Each PRD is written through the eight senior-role lenses (see
[`../implementation-timeline/roles-and-ownership.md`](../implementation-timeline/roles-and-ownership.md))
and pairs with an OpenSpec change of the same name under [`../../openspec/changes/`](../../openspec/changes/).

| Phase | PRD | OpenSpec change | Lead role(s) |
|-------|-----|-----------------|--------------|
| P0 — Foundations (IR + event schema) | [P0-foundations.md](P0-foundations.md) | `p0-foundations` | System Designer |
| P1 — Discovery MVP | [P1-discovery-mvp.md](P1-discovery-mvp.md) | `p1-discovery-mvp` | Backend |
| P2 — Config + Runtime | [P2-config-runtime.md](P2-config-runtime.md) | `p2-config-runtime` | Backend |
| P2.5 — Metrics / OTel | [P2.5-metrics-observability.md](P2.5-metrics-observability.md) | `p2.5-metrics-observability` | DevOps |
| P3 — Context + Skills + Sandbox | [P3-context-skills-sandbox.md](P3-context-skills-sandbox.md) | `p3-context-skills-sandbox` | Backend + DevOps |
| P3.5 — Pattern Classifier | [P3.5-pattern-classifier.md](P3.5-pattern-classifier.md) | `p3.5-pattern-classifier` | AI Engineer |
| P4 — Eval Harness + gen + scoring | [P4-eval-harness.md](P4-eval-harness.md) | `p4-eval-harness` | AI Engineer + Frontend |
| P4.5 — Attribution + Diagnosis | [P4.5-attribution-diagnosis.md](P4.5-attribution-diagnosis.md) | `p4.5-attribution-diagnosis` | AI Engineer |
| P5 — Contracts + Re-arrange + Tracing | [P5-contracts-rearrange-tracing.md](P5-contracts-rearrange-tracing.md) | `p5-contracts-rearrange-tracing` | System Designer + Backend + Frontend + Product |
| P5.5 — Proposals + Verification | [P5.5-proposals-verification.md](P5.5-proposals-verification.md) | `p5.5-proposals-verification` | AI Engineer |
| P6 — Autonomous optimizer | [P6-autonomous-optimizer.md](P6-autonomous-optimizer.md) | `p6-autonomous-optimizer` | AI Engineer + DevOps + Product |
| P7 — Billing, Metering & Entitlements | [P7-billing-metering.md](P7-billing-metering.md) | `p7-billing-metering` | Backend + DevOps |
| P8 — Admin & Operations Console *(internal operator surface)* | [P8-admin-console.md](P8-admin-console.md) | `p8-admin-console` | Backend + Frontend + DevOps |
| P9 — Web Console *(customer-facing dashboard)* | [P9-web-console.md](P9-web-console.md) | `p9-web-console` | Frontend + Product Designer |
| P10 — Prompt & Model Studio *(authoring, bindings, runtime config)* | [P10-prompt-model-studio.md](P10-prompt-model-studio.md) | `p10-prompt-model-studio` | Backend + Product Designer |
| P11 — CLI & CI Integration *(the free surface + the metering path)* | [P11-cli-ci-integration.md](P11-cli-ci-integration.md) | `p11-cli-ci-integration` | Backend + DevOps |
| P12 — Forge Delivery *(the pull request + the gainshare input)* | [P12-forge-delivery.md](P12-forge-delivery.md) | `p12-forge-delivery` | Backend + DevOps |

### Optimization Axis Expansion (OAX) — P13–P18

A follow-on program that widens the set of **optimization axes** the platform can evaluate and improve.
Every axis is a `Dimension` on the Variant Spec that resolves into `config_hash` and is scored by the
**axis-agnostic** eval harness (so none of these phases changes eval/scoring). P13–P16 deepen or make
*applicable* axes that are already modeled; P17–P18 add two **greenfield** axes. Each phase specifies its
honest `EXISTS / PARTIAL / ABSENT` status and, where a call-site codemod is not yet safe, a first-class
**interim refusal** (the same posture skills/context ship with today).

Two **cross-axis contracts** are defined once in P13 and referenced — never restated — by P14/P15/P16:
**`authored-change`** (a user may originate a change on any axis, through the same spine; see
[`docs/decisions/authored-change-data-model-and-flows.md`](../decisions/authored-change-data-model-and-flows.md))
and **`language-coverage`** (coverage is a total function over every registered language, and a refusal
names which of three different things is missing; see
[`docs/decisions/language-coverage-and-refusal-contract.md`](../decisions/language-coverage-and-refusal-contract.md)).

| Phase | PRD | OpenSpec change | Lead role(s) |
|-------|-----|-----------------|--------------|
| P13 — Prompt & Model Optimization *(deepen operators; downgrade guardrail)* | [P13-prompt-model-optimization.md](P13-prompt-model-optimization.md) | `p13-prompt-model-optimization` | AI Engineer + System Designer |
| P14 — Skills & Tools Optimization *(unblock skill binding; split tools≠skills)* | [P14-skills-tools-optimization.md](P14-skills-tools-optimization.md) | `p14-skills-tools-optimization` | Backend + System Designer |
| P15 — Workflow / Node-Wiring Optimization *(merge/reorder under typed-contract gate)* | [P15-workflow-wiring-optimization.md](P15-workflow-wiring-optimization.md) | `p15-workflow-wiring-optimization` | System Designer + Backend |
| P16 — Context Strategy Optimization *(unblock context codemod; retrieval tuning)* | [P16-context-strategy-optimization.md](P16-context-strategy-optimization.md) | `p16-context-strategy-optimization` | AI Engineer + Backend |
| P17 — Memory Strategy Optimization *(greenfield axis: modeled + refused)* | [P17-memory-strategy-optimization.md](P17-memory-strategy-optimization.md) | `p17-memory-strategy-optimization` | System Designer + AI Engineer |
| P18 — Harness Strategy Optimization *(greenfield axis: agent-loop scaffolds)* | [P18-harness-strategy-optimization.md](P18-harness-strategy-optimization.md) | `p18-harness-strategy-optimization` | System Designer + AI Engineer |

### Delivery & Operations — P19 *(cross-cutting)*

A cross-cutting phase that makes the specified system **deployable**. It adds no product feature and no
statistic; it is downstream of P0–P18 and **composes** them into one deployment unit expressed on two
substrates — **Docker Compose** (single-host / open-core) and **Kubernetes (Kustomize base + overlays)** — plus
the operator console's missing deploy unit (on its own origin), the internal-LLM-access posture (secret store,
egress-confined, never in a customer path), and the air-gapped / private-deploy delivery (self-contained
package, declarative-idempotent apply, rollback by re-apply). Written through all eight role lenses; the DevOps
first principle — *deliver "anyone who receives it can run it"* — is the through-line.

| Phase | PRD | OpenSpec change | Lead role(s) |
|-------|-----|-----------------|--------------|
| P19 — Deployment & Delivery *(whole-platform Docker + K8s; consoles; internal LLM access; air-gapped)* | [P19-deployment-delivery.md](P19-deployment-delivery.md) | `p19-deployment` | DevOps + System Designer |

### Self-Serve Distribution — P20 *(cross-cutting)*

The complement to P19: where P19 delivers the platform to a **server/cluster** (the *fleet* delivery), P20
delivers the free `heros` **CLI to an individual machine** (the *self-serve* delivery) as **installable
packages released on GitHub**. It adds no product feature and no statistic; it wraps the P11 binary + supply-
chain floor in a distribution — a tag-triggered GitHub-Release pipeline (no human in the upload path),
native install channels (curl\|sh / PowerShell / Homebrew / Scoop-winget / deb-rpm / container image) that
**verify the signature before the binary is on `PATH`**, an OS-trust posture (Gatekeeper / SmartScreen),
zero-config first-run onboarding, and a safe self-update. The consoles are server-deployed (P19), not
end-user-installed; the product is explicitly *"not a desktop app."*

| Phase | PRD | OpenSpec change | Lead role(s) |
|-------|-----|-----------------|--------------|
| P20 — Installable Packages & Self-Serve Distribution *(GitHub-Release pipeline; native installers; Gatekeeper/SmartScreen; onboarding; self-update)* | [P20-installable-packages.md](P20-installable-packages.md) | `p20-installable-packages` | DevOps + Product Designer |

### Identity & Payments — P21–P22

The commercial front door. **P21 — Stripe Payments** makes the P7 billing abstraction real: it implements the
existing `billing.Provider` interface against Stripe (subscriptions, metered usage, invoices, credits/refunds),
adds the customer payment-method collection P7 left abstract (Stripe Checkout / Payment Element — card data
never touches the platform), and syncs subscription lifecycle to entitlements through **idempotent,
signature-verified, persist-then-ack** webhooks. **P22 — SSO & Identity** supplies the single-sign-on mechanism
[ADR-008](../adr/ADR-008-console-tenant-identity-seam.md) reserved — OIDC (Auth Code + PKCE) primary and SAML 2.0
for the enterprise, plus the operator console's real SSO+MFA — **changing the `verify(assertion) → { tenantId }`
seam and nothing above it** (ADR-008 Rule 3). P22 is the identity floor that both P21's self-serve checkout and
the operator console (its own `admin.*` origin) stand on.

| Phase | PRD | OpenSpec change | Lead role(s) |
|-------|-----|-----------------|--------------|
| P21 — Stripe Payments *(real Stripe behind the P7 Provider interface; checkout; idempotent webhooks; entitlement sync)* | [P21-stripe-payments.md](P21-stripe-payments.md) | `p21-payments` | Sales Operations + Backend |
| P22 — SSO & Identity *(customer OIDC/SAML behind the ADR-008 seam; operator SSO+MFA made real)* | [P22-sso-identity.md](P22-sso-identity.md) | `p22-sso` | System Designer + Backend |

**P21 is implemented.** Its three capabilities are folded into the live spec set —
[`stripe-billing-provider`](../../openspec/specs/stripe-billing-provider/spec.md),
[`payment-collection`](../../openspec/specs/payment-collection/spec.md),
[`billing-webhooks`](../../openspec/specs/billing-webhooks/spec.md) — and it ships with two operational
documents rather than only code: the
[billing-webhook ingress runbook](../decisions/p21-billing-webhook-ingress.md) (the one
inbound-from-internet path, its secret wiring, and what to do when it misbehaves) and the
[customer-facing billing copy](../sales/P21-billing-copy.md) (what a billing message may and may not
say, with the banned phrases enforced at build time).

What it depends on, and what depends on it:

| Relationship | Phase | Why it matters to P21 |
|---|---|---|
| **Implements** | [P7 — Billing & Metering](P7-billing-metering.md) | P21 fills P7's `Provider` box and changes nothing above it: the interface, the append-only ledger, the derived idempotency keys, the correction path and the entitlement gate are P7's and are consumed verbatim. |
| **Reads only** | [P5.5 — Proposals & Verification](P5.5-proposals-verification.md) | Gainshare bills from the verified-delta ledger for **merged** PRs and nothing else. P21 does not loosen this; the invariant is re-asserted against the real provider. |
| **Renders in** | [P9 — Web Console](P9-web-console.md) | The billing page and its BFF. The console holds no Stripe key and no price; the card goes browser→Stripe. |
| **Deployed by** | [P19 — Deployment & Delivery](P19-deployment-delivery.md) | The webhook endpoint is the single documented inbound path in P19's otherwise egress-only network model. |
| **Constrained by** | [ADR-002](../adr/ADR-002-provider-gateway-serves-platform-callers.md) | The platform is never in a customer's production request path. Billing is internal commerce: if the billing path is down, a customer's transformed program is unaffected. |

Run it against a real repository:

```bash
git clone https://github.com/nousresearch/hermes-agent /tmp/hermes-agent
go run ./cmd/p21hermes -repo /tmp/hermes-agent            # the whole period, printed
go run ./cmd/p21hermes -repo /tmp/hermes-agent -serve     # …and serve the console's platform API
```

### Published-Word Surfaces — P23

The console's two **read, not computed** surfaces: a **legal surface** (Terms of Service + Privacy Notice) and a
**developer documentation** surface. They ship together because they are one engineering problem — long-form text
served from the console to readers with **no session**, which must stay true as the system changes and must keep
serving when the platform does not. Their characteristic failure is **drift**, not a crash, and drift is found by
customers, auditors and regulators rather than by tests. So the phase delivers content *and* the machinery that
keeps it honest: content-as-code inside the console's deploy unit (no CMS, no runtime fetch — [ADR-010](../adr/)),
a document identity of `(kind, version, content_hash)` that a **consent record** points at instead of a URL, a
commitment gate that never walls the console, generated reference (absent tiers marked absent, never hand-written),
and eight build-time fences — each with a fixture proving it can fail — extending the `scan-claims` rule from the
marketing page into the documentation tree. The developer tier includes the two pages every developer hits first:
**installing the CLI from a GitHub Release** (verification is a step of the install, not an appendix; only published
channels may be described) and the **complete CLI command reference**, exit-code contract included. The home page
gains a **GitHub repository link**, with any star count taken as a **build-time measurement stamped with its date** —
never a third-party badge, because the console's `default-src 'self'` CSP refuses one and the page's no-third-party
posture is worth more than a number.

| Phase | PRD | OpenSpec change | Lead role(s) |
|-------|-----|-----------------|--------------|
| P23 — Legal Surface & Developer Documentation *(Terms + Privacy Notice as versioned artifacts; append-only consent records; three-tier generated docs; accuracy fences)* | [P23-legal-and-developer-docs.md](P23-legal-and-developer-docs.md) | `p23-legal-and-docs` | Product Designer + Frontend + Sales Operations |

### Seeing the System — P24, P26

Two phases that add no product feature and instead close the platform's two remaining blind spots: *we cannot
see the people using this*, and *the operator cannot see most of what we built*.

**P24 — Product Analytics & Error Monitoring** installs Google Analytics 4, Microsoft Clarity and Sentry. It is
unusual for this program: every other phase added a capability the posture permitted, and **this one modifies a
posture currently enforced by tests** — the `default-src 'self'` CSP whose own comment says an analytics tag
"does not render, it is REFUSED", two live assertions that the shipped CSP names no `https://` origin, P9 FR35,
and the P23 note above that a third-party badge is refused because the no-third-party posture is worth more than
a number. So the amendment is **narrow, per route prefix, and announced**: the tenant and operator prefixes keep
`default-src 'self'` and *gain* a per-prefix assertion; the public prefix becomes bounded by a checked-in origin
allowlist. **Clarity is refused outright on `/app/**` and the operator console** — a session recorder pointed at
prompt text, diffs and cross-tenant aggregates would export the exact content class the P11 egress allowlist was
built to keep in. GA4 never gets a browser tag on a tenant page; console usage is emitted server-side with a
**closed surface enum**, because a URL under `/app` carries variant, run, node and tenant ids. Sentry events are
**constructed from an allowlist** (the `internal/runlink` pattern) rather than scrubbed, and the message body is
dropped unless it is a central `error.code`. All three are **absent by default everywhere except our own hosted
deployment**, and the air-gapped package asserts zero external origins at package-build time. The phase also
leaves the guard *stronger*: a per-origin browser-measured transfer budget, because `scan-bundle.mjs` measures
only `.next/static` and would have stopped a 3D library while not noticing three trackers.

**P26 — Operator Console Refresh** reconciles the P8 operator console with the fourteen phases that landed after
it. The gap is one grep — `internal/adminops/` and `internal/api/p8.go` import nothing from `forgedelivery`,
`deliveryrecord`, `changedelivery`, `distribution` or `runlink` — so an operator cannot see a delivery, a release,
a signing key, an axis refusal, or the link coverage that qualifies a SUM figure. The behavioural consequence is
worse than any missing page: **impersonation has become the workaround**, so the platform's most privileged read
is answering routine lookups. Four read-only surfaces close the gaps; three honesty corrections land *first*
(coverage beside every derived figure, `unverified` authored changes provably excluded, the audit chain's real
merge-path coverage stated). But the phase's **product is a fence** — a checked-in operator-surface ledger with a
build failure for any capability that resolves to neither a surface nor a reasoned absence, because fourteen
phases of drift happened with nothing failing, and a refresh that closes nine gaps and leaves that property
intact buys eighteen months rather than fixing anything. Success is measured in **impersonations displaced**, not
pages shipped, so the phase can fail visibly.

> **There is no P25.** The token `p25` already denotes **P2.5 — Metrics & Observability** in this repository
> (`/p25/monitor`, the Gantt id in [`../implementation-timeline/README.md`](../implementation-timeline/README.md),
> `internal/api/monitor.go`). Reusing it for a new phase would make `p25` ambiguous in exactly the places an
> operator greps during an incident, so the operator-console phase is numbered **P26**.

| Phase | PRD | OpenSpec change | Lead role(s) |
|-------|-----|-----------------|--------------|
| P24 — Product Analytics & Error Monitoring *(GA4 + Clarity on the public prefix only; Sentry allowlist-constructed; consent by category; the origin fence extended)* | [P24-analytics-and-error-monitoring.md](P24-analytics-and-error-monitoring.md) | `p24-analytics-error-monitoring` | Frontend + DevOps |
| P26 — Operator Console Refresh *(the surface ledger + its build fence; delivery, release, axis and oversight surfaces; three honesty corrections)* | [P26-operator-console-refresh.md](P26-operator-console-refresh.md) | `p26-operator-console-refresh` | Frontend + Backend |

## PRD template

Every phase PRD follows this structure:

```markdown
# PRD — <Phase>: <Title>

| Field | Value |
|---|---|
| Phase / Milestone | <Pn> / <Mn> |
| Target window | ~Weeks a–b |
| Lead role(s) | <L> |
| Supporting role(s) | <S…> |
| Status | Draft |
| OpenSpec change | `<change-id>` |

## 1. Summary
<2–4 sentences: what this phase delivers and why it matters on the critical path.>

## 2. Problem & context
<What breaks / is impossible without this phase. Upstream state it assumes.>

## 3. Goals & non-goals
### Goals   ### Non-goals (explicitly deferred, with the phase that owns them)

## 4. Users & personas
<Who consumes this capability — end users and/or downstream subsystems.>

## 5. User stories / jobs-to-be-done
<As a <persona>, I want <capability>, so that <outcome>. Grouped by persona.>

## 6. Functional requirements
<Numbered FRs. These map 1:1 to OpenSpec requirements.>

## 7. Non-functional requirements
<Scale, latency, reliability, security, reproducibility, cost — quantified.>

## 8. System design summary
<Architecture, data model, key interfaces/APIs. Mermaid where useful. (System Designer lens.)>

## 9. Design by role lens
<What each Lead/Support role contributes, applying that role's workflow discipline.
 Only include the roles marked L or S for this phase in the ownership matrix.>

## 10. Dependencies
<Upstream phases required; what this phase unblocks.>

## 11. Risks & mitigations
<Table: risk · owner · mitigation.>

## 12. Rollout & test strategy
<How it ships safely and how correctness is proven. (DevOps + relevant role lens.)>

## 13. Success metrics & acceptance criteria
<Measurable exit criteria — the checklist that closes the milestone.>

## 14. Open questions
```

The role-lens section (9) is where the eight workflows do their work: the System Designer quantifies
and picks storage, the Backend Dev designs contracts/failure behavior, the AI Engineer enforces
evals-before-optimization and verification, DevOps enforces observability/least-privilege/blast-
radius, Frontend owns the interface and its states, Product anchors to the outcome and designs
the unhappy path, QA defines the acceptance gate that can actually fail, and Sales Operations keeps
what is sold aligned with what the system does.

A PRD includes only the roles marked **L** or **S** for its phase in the
[ownership matrix](../implementation-timeline/roles-and-ownership.md) — §9 is a working section, not a
roll call.
