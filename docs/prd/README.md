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
