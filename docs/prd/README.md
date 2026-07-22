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
