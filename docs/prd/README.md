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

**What "implemented" claims, and what it does not.** The M16 checklist is green against a **real Stripe
test account** (2026-07-30) — the wire, not an in-process double, on a customer created by that run. Moving
to the real wire found seven defects, five in shipped code, which is the argument for doing it stated as a
number. **Live mode is untaken and inherits none of that account's artefacts**: the key, the webhook endpoint
and its signing secret, the customer handles and the price ids are all per-mode objects, and the live catalog
has to be preflighted in live mode before anything charges. That second checklist is
[P21 §13.1](P21-stripe-payments.md), it is not green, and it is deliberately kept separate — the first
checklist is a claim about the platform, the second is a claim about an account.

**P22 is implemented.** Its two capabilities are folded into the live spec set —
[`sso-identity`](../../openspec/specs/sso-identity/spec.md) (the customer OIDC/SAML seam, tenant mapping,
and the identity security posture) and
[`operator-sso-mfa`](../../openspec/specs/operator-sso-mfa/spec.md) (the P8 operator surface made real) —
and it ships with the [identity copy and commitment boundary](../sales/P22-identity-copy.md): what we
sell, what we explicitly do **not** commit to (SCIM, a per-seat user/audit model, transformed-program
identity), and the questions a security reviewer always asks.

**What "implemented" claims, and what it does not.** Both verifiers are green — against a signing identity
provider **this repository runs**. That is the right way to prove a *refusal*: a fixture can be told to send
a stale assertion, a wrapped signature, or a token signed with the wrong key, and a real provider cannot. It
proves nothing about *acceptance* — whether a real discovery document parses, a real key set loads and
rotates, a real assertion shape verifies. **No real org has been federated with yet.** The read-only probe
([`liveidp_test.go`](../../internal/adminidentity/liveidp_test.go)) has been ready since the module was
written and skips, because nobody has pointed it at one. That checklist is
[P22 §13.1](P22-sso-identity.md) — Okta first — and it is not green. One correction it forces is worth
knowing before a sales conversation: **there is no directory back-channel**, so a user disabled at the
customer's IdP starts no new session but keeps an existing one until it expires, bounded by the console
session TTL.

Two things are worth reading even if you never touch the code. First, **the seam did not move**: the
session store, cookie, revocation, scope derivation and fail-closed middleware are byte-for-byte what
ADR-008 built, fenced by pinned digests plus a rule that no mechanism word may appear above the seam.
Second, **operator MFA is now an invariant rather than a claim** — the OIDC and SAML admin providers
deliberately do not read `amr`, `acr` or `<AuthnContextClassRef>`, so a misconfigured IdP MFA policy
still results in denial on the surface that can halt the fleet.

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
go run ./cmd/proof/payments -repo /tmp/hermes-agent            # the whole period, printed
go run ./cmd/proof/payments -repo /tmp/hermes-agent -serve     # …and serve the console's platform API
```

### Published-Word Surfaces — P23

The console's two **read, not computed** surfaces: a **legal surface** (Terms of Service + Privacy Notice) and a
**developer documentation** surface. They ship together because they are one engineering problem — long-form text
served from the console to readers with **no session**, which must stay true as the system changes and must keep
serving when the platform does not. Their characteristic failure is **drift**, not a crash, and drift is found by
customers, auditors and regulators rather than by tests. So the phase delivers content *and* the machinery that
keeps it honest: content-as-code inside the console's deploy unit (no CMS, no runtime fetch — [ADR-011](../adr/ADR-011-legal-and-docs-content-as-code.md)),
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

**P23 is implemented.** Its nine capabilities are folded into the live spec set —
[`legal-documents`](../../openspec/specs/legal-documents/spec.md),
[`consent-records`](../../openspec/specs/consent-records/spec.md),
[`developer-docs`](../../openspec/specs/developer-docs/spec.md),
[`cli-reference`](../../openspec/specs/cli-reference/spec.md),
[`install-documentation`](../../openspec/specs/install-documentation/spec.md),
[`docs-accuracy-fence`](../../openspec/specs/docs-accuracy-fence/spec.md),
[`reading-surface`](../../openspec/specs/reading-surface/spec.md),
[`social-proof-claims`](../../openspec/specs/social-proof-claims/spec.md) and
[`console-marketing-site`](../../openspec/specs/console-marketing-site/spec.md) — and it ships with four
documents rather than only code: the ratified one-way doors
([`p23-one-way-doors.md`](../decisions/p23-one-way-doors.md)), the
[data inventory](../decisions/p23-data-inventory.md) counsel's Privacy Notice was written from, the
[Terms reconciliation](../sales/P23-terms-reconciliation.md) against P7/P21, and the
[acceptance evidence](../release/p23-evidence.md).

**A numbering correction worth knowing.** The design document calls the content-as-code decision
"ADR-010" and the consent migration "0016". Both numbers were taken between the design being written and
the phase being built — by [`ADR-010-runtime-gradual-rollout`](../adr/ADR-010-runtime-gradual-rollout.md)
and by `0016_p13_authored_change` respectively. They shipped as **ADR-011** and **0019**, and each file
says why rather than being silently renumbered.

**What "implemented" claims, and what it does not.** Twelve fence fixtures were each watched failing and
then passing; the consent record's idempotency is proved against **real Postgres** with twelve concurrent
inserts collapsing to one row; and the install page and quickstart were executed end to end against the
**real published v0.20.0 release** — checksum verified, signature verified against key
`heros-release-2026c`, Gatekeeper met exactly as the page warned, and a discovery graph produced on
`nousresearch/hermes-agent`. What is **not** claimed: that walk was performed by the author, not by an
independent reviewer on a clean machine, and [§12.10's independent half is recorded
open](../release/p23-evidence.md). Counsel has not reviewed the Terms. The consent endpoints have never
run against a vendor-operated deployment, because there is not one.

Three real defects surfaced by generating documentation against artifacts rather than describing them:
the reading pages were `force-static`, so the nonce-based CSP silently killed both client islands on a
green build; the `.deb`/`.rpm` channels claim a signed-manifest coverage `SHA256SUMS` does not give them;
and the `.rpm` install command names an asset the release does not publish. The last two are P20's to
fix — P23 makes the *documentation* honest about them and does not edit the channel contract.

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

### The Customer Is a Row — P27

The commercial front door assumed a customer existed and nothing made one. **P27 — Account System** closes that
chain. A tenant is not a row anywhere in the thirty-seven migrations — it is a key in a map
[`auth.Registry`](../../internal/auth/registry.go) builds from configuration at boot, so onboarding a customer
is a deploy and self-serve sign-up is impossible. There is **no user**:
[`session.ts`](../../web/console/src/lib/session.ts) says the session holds a tenant and not a person "because
the platform cannot currently prove one" — true when ADR-008 was written, made **false by P22**, and never
revisited. Nothing calls `account.Store.Create` outside demos, so P21's checkout path opens with
`accounts.Get → ErrNotFound`. The `run` table has no owner column and there is no `GET /api/v1/runs`, so *"what
did I run last week?"* is not a question the API can be asked. And the isolation `scope.ts` carefully defers to
does not exist — the platform never reads `X-Console-Tenant`, and every console request carries one
process-wide credential.

P27 makes the tenant a durable row (configuration demoted to an **expand-only boot seed**, so every existing
deployment keeps its tenants and its keys), the person a first-class record with membership, invitations and
per-user revocation, and the run **owned** — with scope travelling *inside* the credential and the tenant header
**deleted and fenced** rather than made authoritative, because the cheap fix lets any holder of one credential
name any tenant. It splits `seats` into the two quantities that were collapsed into one word (a **state** that
gates the next invitation, a **period peak** that prices an invoice — today's limit is enforced against a number
nothing ever writes), and makes sign-up create `{tenant, user, owner membership, Free account}` atomically so the
first *Upgrade* finds an account instead of a 404. It adds **no** price, plan, billing dimension or permission
system, and changes nothing above the ADR-008 seam.

| Phase | PRD | OpenSpec change | Lead role(s) |
|-------|-----|-----------------|--------------|
| P27 — Account System *(durable tenant + config-as-seed; users, memberships, invitations; run ownership and credential-carried scope; seats made measurable; self-serve sign-up and first upgrade)* | [P27-account-system.md](P27-account-system.md) | `p27-account-system` | System Designer + Backend |

**P27 is not implemented.** Nothing in its [§13 exit checklist](P27-account-system.md) is checked, and its
[customer-facing copy](../sales/P27-account-copy.md) is written *before* the phase specifically so the wrong
sentences do not enter a deck during implementation — every claim in it carries the tasks that license it, and
one claim is blocked on a question nobody has answered: **whether a CLI-only member occupies a seat.** Until
that is ratified, no seat number may be quoted, because we cannot state what one includes.

### The Bridge Carries Nothing — P29

The free surface is the CLI; the paid surface is the console; and a developer who ran a real workflow,
signed in and linked the run found **fifteen console surfaces with nothing to say about it.** Six distinct
causes wearing one symptom, the worst two being structural: `heros link --with-ir` and `heros push-source`
address paths the deployed Ingress does not publish — and the fence written to catch exactly that skips
every path with a variable segment, which is precisely those two. Meanwhile the axis surfaces render
*total, correct build facts* (`128 apply / 123 refuse`) and the platform has never multiplied them by the
customer's own nodes.

**P29 — Linked Run Fan-out** makes one `heros link` fill every one of those surfaces with the
organization's own data, or say in a named, typed sentence why it cannot — without widening the egress
promise by one byte on the default path. Its centre is a refusal: the platform **never computes** whether
an axis applies to a call site, because that depends on source it must never hold. The customer's machine
computes it with the real engine on the real code and transmits a stable identifier; a node we were not
told about renders **`not reported`**, a fourth state beside applies / refused / not-applicable.

| Phase | PRD | OpenSpec change | Lead role(s) |
|-------|-----|-----------------|--------------|
| P29 — Linked Run Fan-out *(edge reach + its fence; per-node applicability computed client-side; subject enumeration; the coverage × your-nodes projection; hosted workflow catalog; link coverage without a billing account)* | [P29-linked-run-fanout.md](P29-linked-run-fanout.md) | `p29-linked-run-fanout` | Backend + Frontend |

**P29 is not implemented.** Its metric is chosen so it cannot be satisfied by shipping pages: *the number
of console surfaces that render an unexplained empty state after one fully opted-in link* — today fifteen,
at exit zero. A surface that renders a named, typed refusal counts as explained; a blank does not.

### One Frontend Emits Edges — P30

An operator opened a real workflow and reported four problems: the Structure drawing is an unordered
stack, no overall pattern is reported, the eval set's scenarios are nowhere, and there are no proposals.
Three of the four are one defect. **Exactly one discovery frontend has ever emitted an edge** —
`frontend_go.go` appends `g.Edges`; the other five contain no occurrence of the word. `BuildGraph` walks a
`go/ast` tree, so the syntactic frontends have nothing to hand it. Every non-Go repository therefore gets a
node list, and seven of the eight pattern detectors are topology predicates — **0/22 can fire by
construction**. The header then renders `llm_calls == 0` as *"Fully rule-covered"*, asserting complete
coverage over a graph with zero labels.

**P30 — HEROS** introduces the platform's own agent: it produces the graph the static frontends cannot,
and it reports whether each surface's answer rests on evidence. It is configured entirely from the
operator console — prompt, model, credential reference, skills, tools, context, memory, harness — through
**the six-axis vocabulary the product already sells**. HEROS is a Variant Spec resolved against the P2
registries, sealed to a `config_hash`; the platform optimizes agentic workflows for a living and its own
agent is one of them. Its centre is a refusal: **determinism does not come from the model.** An inference
runs once per `(source_revision, config_hash)`, is content-addressed and pinned, and re-running is an
explicit act shown as a diff. Seed and temperature are recorded because they explain a result, not because
they guarantee one.

| Phase | PRD | OpenSpec change | Lead role(s) |
|-------|-----|-----------------|--------------|
| P30 — HEROS, the Platform Agent *(residue-only chain inference; provenance on every IR fact; pinned-by-content determinism; two placements one definition; composition not a workflow label; six axis editors bound to their existing vocabularies — no axis is a text box, and a harness the runner cannot serve is refused at save; the four surface-honesty fixes)* | [P30-heros-platform-agent.md](P30-heros-platform-agent.md) | `p30-heros-platform-agent` | AI Engineer + Backend |

**P30 is not implemented; it is cleared to start.** The two decisions that gated it are closed (PRD §14):
platform-side HEROS spends the **platform's** credential and the platform stores no customer provider key
under **any** placement — so P29's promise carries forward unweakened rather than qualified — and the
default placement is **`disabled`**, so deploying the phase changes nothing for any existing tenant until
an operator acts. Its first workstream, the four surface-honesty fixes, depends on neither decision and
ships alone.

### Graph Engineering Harness Agent (GEHA) — P31–P36

The platform can do eleven things and asks the customer to perform all eleven; the CLI demo is a numbered
list, and the numbering is the product's shape rather than the video's editing. **GEHA collapses the list
into one sentence typed into a conversation** — point at a repository, ask a question, and the platform's
own agent reads the repository's agent-engineering surfaces, reports what it found with the evidence behind
each finding, proposes changes, waits to be told yes, applies them, re-measures, and opens the pull request.

Almost every component already exists; the program's discipline is to wire them rather than fork them. Its
second half is not a convenience: the platform sells the idea that an agentic system is a graph of
configured nodes, and **its own agent is one node** — `NodeID = "heros_analyst"`, which is why HEROS's
wiring axis is not merely unused but *vacuous* (*"there is no second node to order it against"*). An agent
that cannot be a graph cannot do graph engineering. **P36 makes the platform's own agent a graph**, and the
other five phases exist to make that possible.

Six rulings shape the program and are recorded with their costs in
[the program document](P31-P38-graph-engineering-agent-program.md) §3: bundle-push stays the default with
a per-repository read grant as the opt-in upgrade ([ADR-013](../adr/ADR-013-source-acquisition-posture.md));
**loop and graph become axes separate from harness** ([ADR-014](../adr/ADR-014-harness-loop-graph-axis-split.md));
the hosted Git App becomes the delivery default **for console-driven runs only**; and assessment produces
**evidence-backed per-surface findings with no composite score** — because every score in this codebase is
comparative and verified, and an absolute "your repository is 62 out of 100" is a model's judgement in a
metric's typeface. Two more (§3.1) came back from reading the consoles against the first six phases:
the customer console's working surfaces **become editors bound to the reader's own source**, with their
31,628 words of explanation moved to the reading surface rather than deleted; and the operator console
**renders the agent's whole contract** — twenty dimensions, each `authorable`, `observable` or `fixed` —
wiring an editor core that has been written, specified and unused since P30.

| Phase | PRD | OpenSpec change | Lead role(s) |
|-------|-----|-----------------|--------------|
| **Program overview** *(decomposition, the six rulings, the nine axes, sequencing, staged plan)* | [P31-P38-graph-engineering-agent-program.md](P31-P38-graph-engineering-agent-program.md) | — | System Designer |
| P31 — The Conversational Console *(typed message kinds, not prose; streaming over the existing SSE substrate; approval in dialogue routed to the existing gate; the untrusted-source boundary)* | [P31-conversational-console.md](P31-conversational-console.md) | `p31-conversational-console` | Frontend + Product Designer |
| P32 — Repo Intake *(GitSource behind the existing seam; bundle default; local bridge; revocation that cascades to derived trees)* | [P32-repo-intake.md](P32-repo-intake.md) | `p32-repo-intake` | Backend + DevOps |
| P33 — Surface Assessment *(nine axes × four states; `not_measured` names its missing input; decisiveness travels with every score; no composite)* | [P33-surface-assessment.md](P33-surface-assessment.md) | `p33-surface-assessment` | AI Engineer + Product Designer |
| P34 — Harness / Loop / Graph *(three axes, not one; expand-only, the contract half refused on the record; concurrency, conditional edges and merge)* | [P34-harness-loop-graph-split.md](P34-harness-loop-graph-split.md) | `p34-harness-loop-graph-split` | System Designer + AI Engineer |
| P35 — The Improvement Run *(a question becomes a bounded plan; re-measurement may disagree and withdraw; hosted App as the console delivery default)* | [P35-autonomous-improvement-run.md](P35-autonomous-improvement-run.md) | `p35-autonomous-improvement-run` | Backend + AI Engineer |
| P36 — The Agent Is a Graph *(nine axes for the operator; `NodeID` becomes data; the wiring refusal narrows rather than disappears; single-node stays hash-identical)* | [P36-agent-self-configuration.md](P36-agent-self-configuration.md) | `p36-agent-self-configuration` | System Designer + Frontend |
| P37 — Source-Bound Editors *(every axis surface edits a node from the reader's own repository; explanation moves to the reading surface; one editor kit; no fixture where the reader's data belongs)* | [P37-source-bound-editors.md](P37-source-bound-editors.md) | `p37-source-bound-editors` | Frontend + Product Designer |
| P38 — The Agent Contract *(twenty dimensions, three states, none hidden; the `config_hash` boundary fenced in both directions; `axiseditor.go` wired, not rewritten)* | [P38-agent-contract.md](P38-agent-contract.md) | `p38-agent-contract` | System Designer + Frontend |

**None of GEHA is implemented, and this program is authored documents-only** — the same scope the OAX
program (P13–P18) was written under. Every task in every change set is unchecked.

Three phases carry a failure that no test goes red for, and each is stated as the first task of its
phase rather than as a QA item at the end. **P34**: removing the loop fields from `HarnessSpec` would move
every harness `version_id` and orphan every measurement taken on a multi-turn node, so the contract half of
expand-contract is refused *on the record*. **P36**: every pinned inference is keyed by
`(source_revision, agent config_hash)`, so a change to the agent's definition *shape* orphans every pin —
silently re-running every assessment at provider cost, weeks later, while the console shows results
computed by a configuration that no longer exists. **P38** is the same hazard reached from the other
side: a contract field added to the hashed definition orphans every pin, so the contract is specified as
a *view* over things that already have identity, and the boundary is fenced in **both** directions — a
non-hashed change producing a new `config_hash` fails, and a hashed change producing none fails too.

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
