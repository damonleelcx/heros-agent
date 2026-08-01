# PRD — P26: Operator Console Refresh (Reconciling P8 with Everything Since)

| | |
|---|---|
| **Status** | Proposed (docs-only; no code in this phase) |
| **Created** | 2026-07-30 |
| **Updated** | 2026-07-30 |
| **OpenSpec change** | [`p26-operator-console-refresh`](../../openspec/changes/p26-operator-console-refresh/) |
| **Lead role(s)** | Frontend + Backend, with System Designer on the drift fence and DevOps on release oversight |
| **Upstream** | P8 (the console being refreshed), P10–P12, P13–P18 (OAX), P19, P20, P21, P22, P23, P24 |
| **Numbering note** | There is no `P25`. The token `p25` already denotes **P2.5 — Metrics & Observability** in this repository (`/p25/monitor`, the Gantt id in `implementation-timeline/README.md`, `internal/api/monitor.go`). Reusing it would make the token ambiguous exactly where an operator greps during an incident. |

> **Language note.** English only, in source, comments, UI strings and documents. The Chinese
> technical-design conventions this document follows — section order, the five-step exposition of each
> decision, the four-element *design key points*, user narration before any diagram — are applied
> translated.

---

## 1. Summary

The operator console shipped in P8 with nine surfaces: Overview, Tenants, Billing, Model Registry, Jobs
& Fleet, Kill Switch, Cross-Tenant, Audit Log, Compliance. Fifteen capabilities in
[`internal/adminrbac/rbac.go`](../../internal/adminrbac/rbac.go), deny-by-default, each mapped to a
destination in the single [`surfaces.ts`](../../web/admin-console/src/lib/surfaces.ts) registry that both
the navigation and the command palette read.

**Since then the platform has roughly doubled and the operator console has not moved.** Fourteen phases
landed — P10 studio, P11 CLI/CI and run linking, P12 forge delivery, P13–P18 six optimization axes with
three cross-axis contracts, P19 deployment, P20 installable packages, P21 payments, P22 SSO, P23 legal,
P24 analytics — and the operator's surface for all of it is nine pages designed before any of it existed.

The gap is not a wish list. It is verifiable, and the verification is one grep:

```
$ grep -rn "forgedelivery\|deliveryrecord\|changedelivery\|distribution\|runlink" \
        internal/adminops/ internal/api/p8.go
(no matches)
```

The operator backend imports **nothing** from the delivery, release, or run-linking subsystems. So:

- **The operator cannot see a delivery.** P12 delivery records and the P13 change-delivery rollout ledger
  have no operator surface. `mergeaudit.go` mirrors P6 *autonomous* merges into the audit chain — a
  different, earlier mechanism — so the audit log's claim to cover "every merge" now covers one of two
  merge paths.
- **The operator cannot see a release.** P20 shipped a signing pipeline, five install channels, a
  self-update path and a platform-trust story — and rotated the signing key mid-flight after a leak.
  There is no surface showing which key is active, which releases are published, or whether the
  post-publish smoke passed.
- **The operator cannot see an axis.** Six axes now resolve into `config_hash`. The console shows none of
  them: no adoption, no refusal counts, no coverage. The platform's own backlog question — *which
  materializer would unblock the most refused nodes across the fleet* — has no surface and no data path.
- **The operator sees SUM-derived figures with no link coverage beside them.** `openspec/project.md`
  states the rule once, plainly: metering counts only what it observed, and **link coverage is displayed
  wherever a derived figure is shown.** The customer console honours it (`link-coverage.test.mjs`). The
  operator billing surface, which predates P11, does not. Verified: no coverage field exists anywhere in
  `internal/adminops/billing.go` or the operator billing page.

And there is a fifth consequence that is worse than any missing page, because it is a *behaviour* rather
than an absence:

**Impersonation has become the answer to a missing operator view.** An operator who needs to know a
tenant's axis coverage, delivery state or refusal reasons has exactly one tool that can show it —
`impersonate.read` — a reason-required, time-bounded, fully-audited read into a customer's own console.
That control was built for the case where an operator must see what the customer sees to reproduce a
report. It is now being used to answer questions a cross-tenant aggregate should answer, which means the
platform's most privileged read is being spent on routine lookups. **Every impersonation that existed
only to read something an aggregate should have shown is a data-protection cost with no product need
behind it, and reducing that count is this phase's sharpest measure of success.**

So this phase does two things, and the second matters more than the first. It closes the surface gap for
the six subsystems above. And it installs **the mechanism that stops the gap reopening**: a checked-in
operator-surface ledger, mapping every shipped capability to the operator surface that exercises it or to
a recorded, reasoned decision that none is needed — with a build fence that fails when a capability
exists in `openspec/specs/` and appears in neither column. Fourteen phases of drift happened because
nothing failed when it happened. A refresh that closes nine gaps and leaves that property intact buys
eighteen months.

---

## 2. Problem & context

### 2.1 What is on the console today, verified

Nine navigation destinations and eleven palette-only action destinations, from `surfaces.ts`. The admin
API (`internal/api/p8.go`) serves 31 routes across session/identity, tenants, plans and billing,
registry, jobs and fleet, kill switch, impersonation, cross-tenant aggregates, audit, and GDPR. RBAC has
15 capabilities, deny-by-default, with a matrix test that iterates `Capabilities` so a capability added
without a considered grant is a failing test.

That architecture is sound and this phase does not touch it. `surfaces.ts` remains the one map; the
palette remains destinations-not-commands; every write keeps its reason field and its confirmation; the
backend keeps classifying blast radius and the console keeps rendering that classification rather than
forming its own opinion.

### 2.2 What landed since, and what an operator can see of it

| Phase | What shipped | Operator surface today | Consequence |
|---|---|---|---|
| **P10** | Prompt registry: immutable, content-addressed versions; bindings | Model Registry covers **models only** | An operator cannot resolve which prompt version a run used |
| **P11** | Run linking, the constructed egress allowlist, link coverage, metering as a read over telemetry | None | SUM-derived figures shown with no coverage; the rule is stated in `project.md` and unhonoured here |
| **P12** | Forge delivery, delivery records, merge **observed** not inferred | None | The most consequential thing the platform does to a customer's repository is invisible to oversight |
| **P13–P18** | Six axes; `authored-change`, `language-coverage`, `change-delivery` contracts | None | No adoption, no refusal, no coverage; the backlog cannot be prioritised from evidence |
| **P19** | Compose / Kubernetes / air-gapped deployment, the console's own deploy unit | `/fleet` shows the **job** fleet | No view of deployments or their versions |
| **P20** | Release pipeline, 5 install channels, self-update, platform trust, signing-key rotation after a leak | None | No surface for the active signing key, published releases, or post-publish smoke |
| **P21** | Stripe checkout, idempotent webhooks, dunning | Billing predates a real PSP | Webhook failures and dunning state are invisible |
| **P22** | Operator SSO + MFA replacing the ADR-008 seam | Verifier is real; the **IdP is a fixture** (`adminidentity/fixture.go`) | Operator identity is not yet a real IdP, and the console cannot show which factor authenticated a session |
| **P23** | Legal documents, consent records, material versioning | None | Cannot answer "which tenants owe re-acceptance of the current Terms" |
| **P24** | GA4 / Clarity / Sentry, three readiness states | None (new) | Reporting health has no operator surface |

### 2.3 Why the drift happened, and why a refresh alone will not fix it

Nothing failed. Every one of those fourteen phases was correct, tested, and shipped with its own
customer-facing surface where it needed one. The operator console was simply nobody's acceptance
criterion. There is no fence that notices a capability without operator oversight, so the only mechanism
was somebody remembering — and this codebase already learned that lesson somewhere else, in the comment
on the frontend scope guard: *manual and agent horizontal scanning still missed the fifth occurrence,
which proves it must be machine-enforced and cannot rely on human memory.*

That is why §6 leads with the ledger and the fence rather than with pages. The pages are this phase's
output. **The fence is its product.**

### 2.4 Why now

Three forcing functions.

**P20 put the platform's software on strangers' machines.** After a signing-key leak and rotation, "which
key is active, and which published artefacts were signed with the retired one" is an incident-response
question with no surface behind it.

**P21 puts money through a third party.** A payment provider's webhook is an asynchronous write from
outside the trust boundary. When one fails, the operator needs to see which, for whom, and whether the
retry landed. A billing surface designed before any PSP existed cannot answer that.

**The axis programme has run out of evidence.** Six axes shipped. Deciding the seventh — or which
language materializer to build — requires knowing which refusals dominate across the fleet. Nobody can
see it, so the next such decision will be made on taste, as the last several were.

---

## 3. Goals & non-goals

### Goals

- **G1 — Make operator-surface drift a build failure.** A checked-in ledger mapping every shipped
  capability to an operator surface or a recorded no-surface-needed decision, with a fence that fails on
  a capability appearing in neither column.
- **G2 — Give delivery oversight a surface.** P12 delivery records and the P13 change-delivery rollout
  state, cross-tenant and per-tenant, with the undeliverable count and its typed causes.
- **G3 — Give release and trust a surface.** Published releases per channel, the active signing key and
  every retired one with its rotation date, artefact verification state, and the post-publish smoke
  result per platform.
- **G4 — Give the axes a surface.** Fleet-wide axis adoption, refusal counts by typed cause, and the
  language-coverage matrix — read from the one coverage source every other surface reads.
- **G5 — Make derived figures honest on the operator surface.** Link coverage displayed beside every
  SUM-derived figure, and `unverified` authored changes provably excluded from every aggregate
  improvement, savings and quality number.
- **G6 — Give identity, consent and reporting health a surface.** Which factor authenticated a session;
  which tenants owe re-acceptance of which document version; the three P24 readiness states.
- **G7 — Reduce impersonation to its actual purpose.** Every routine lookup that today requires
  impersonation gets a cross-tenant read instead, and the ratio is measured.
- **G8 — Add no new destructive control by default.** A refresh is a read-surface phase. The one
  candidate write (halting a release channel) is presented as a decision fork in §14, not slipped in.

### Non-goals (explicitly deferred, with the phase that owns them)

- **Re-architecting the console.** `surfaces.ts` stays the one map; the shell, palette, RBAC model,
  audit chain and confirmation discipline are unchanged. *(P8 owns them and they are working.)*
- **Building the P22 real IdP.** This phase surfaces which factor authenticated a session. Replacing the
  test-mode IdP fixture with a real OIDC/SAML provider is P22's own work.
- **Building the P21 payment integration.** This phase surfaces webhook and dunning state. The
  integration is P21.
- **New customer-facing surfaces.** Nothing in P9's console changes. Cross-tenant reads land on the
  operator console only.
- **New aggregates that need new collection.** Every read here derives from an existing store. A gap
  requiring new collection is recorded in the ledger as *not yet readable* with the collection named —
  not filled with an estimate.
- **Impersonation improvements.** Reducing its *use* is a goal; changing the control is not.
- **Any change to an axis, resolver, gate, transform, scorer or eval.** No `Dimension` changes, no
  `config_hash` input changes.

---

## 4. Users & personas

| Persona | What they need | What they must not get |
|---|---|---|
| **Support operator** (`tenant.read`, `job.read`, `impersonate.read`) | To answer "what happened to this customer's delivery" **without** impersonating | A cross-tenant read that is not logged; a page that shows one tenant's data while claiming to show another's |
| **Billing operator** (`billing.*`, `entitlement.override`) | Link coverage beside every derived figure; which webhook failed for whom; dunning state | A SUM figure with no coverage; a gainshare number drawing on unverified deltas |
| **Release engineer** (new capability) | Which releases are published on which channel, which signing key is active, which artefacts verify, which smoke passed | A trust surface that shows only the happy path; the ability to unpublish without a confirmation and a reason |
| **Platform engineer / axis owner** | Which refusal causes dominate the fleet, and which materializer would unblock the most nodes | A coverage cell the engine would refuse; a fleet number that includes `unverified` authored changes |
| **Superadmin** (`role.grant`, `gdpr.execute`) | Which factor authenticated each operator session; which tenants owe re-acceptance | A privileged read that is convenient enough to become routine |
| **Data-protection reviewer** (ours or a customer's) | Evidence that a routine lookup no longer requires reading customer data | A console where impersonation is the only way to answer a support ticket |

---

## 5. User stories / jobs-to-be-done

**Support operator**
- As a support operator with a ticket saying "our PR never opened", I want to open one page, find the
  tenant's delivery records, and see the typed cause — *not* start an impersonation session to look at
  their console.
- As a support operator, I want to know a node was **refused** rather than failed, and which of the three
  refusal causes applies, because that determines whether I answer the customer, file a backlog item, or
  tell them their call site cannot carry it.

**Billing operator**
- As a billing operator about to issue a credit, I want link coverage beside the SUM figure, because a
  derived figure with unknown coverage is a number I should not act on.
- As a billing operator, I want to see which Stripe webhook failed, for which tenant, and whether the
  idempotent retry landed — before the customer notices.

**Release engineer**
- As a release engineer during an incident, I want to see which signing key is active, which are retired
  with their rotation dates, and which published artefacts were signed with a retired one.
- As a release engineer, I want the post-publish smoke result per platform, because a green publish with
  a failed smoke is the state that reaches a stranger's laptop.
- As a release engineer, I want to know a runner **queued until timeout** rather than failed, because a
  retired runner label is a different problem from a broken build.

**Axis owner**
- As an axis owner planning the next phase, I want fleet-wide refusal counts by typed cause, so I can say
  "a Kotlin selection materializer unblocks 1,200 nodes and a Java one unblocks 40" instead of guessing.
- As an axis owner, I want the coverage matrix read from the **same** source the transform's refusal, the
  preflight, the CLI and the customer console read, so no two surfaces disagree about coverage.

**Superadmin**
- As a superadmin reviewing operator activity, I want each session to show which factor authenticated it.
- As a superadmin, I want to see which tenants owe re-acceptance of the current Terms version, and to
  reach the archived text of what each accepted.

**Platform operator, generally**
- As an operator, I want the readiness surface to tell me whether error reporting is `absent`,
  `configured` or `degraded`, because during an incident the third-party dashboard is the least available
  part of the system.

---

## 6. Functional requirements

Numbering continues from P8's FR41.

### The surface ledger and its fence (capability `operator-surface-ledger`)

- **FR42.** A checked-in **operator-surface ledger** SHALL map every capability in `openspec/specs/` to
  either the operator surface that exercises it or an explicit `no-operator-surface` decision carrying a
  reason and the phase that decided it. There SHALL be no third state.
- **FR43.** A build fence SHALL fail when a capability exists in `openspec/specs/` and appears in neither
  column, naming the capability. Drift SHALL be a build failure, not a review finding.
- **FR44.** The ledger SHALL be asserted in **both** directions: every ledger entry naming a surface
  resolves to a destination in `surfaces.ts`, and every destination in `surfaces.ts` is named by at least
  one ledger entry. A surface nobody can justify and a justification pointing at no surface are both
  defects.
- **FR45.** Where a read is wanted but not yet derivable from an existing store, the ledger entry SHALL
  record `not-yet-readable` and **name the collection that would make it readable**. It SHALL NOT be
  filled with an estimate, an extrapolation, or a surface that renders an empty state as though it were
  a zero.
- **FR46.** A new operator surface SHALL require a capability in `adminrbac.Capabilities`. No surface
  SHALL be reachable without a granted capability, and the existing deny-by-default matrix test SHALL
  continue to iterate every capability.
- **FR47.** `surfaces.ts` SHALL remain the single map read by the navigation, the command palette and the
  ledger. A destination SHALL NOT be reachable from one and absent from another.

### Delivery oversight (capability `operator-delivery-oversight`)

- **FR48.** The console SHALL show **delivery records** — the P12 forge-delivery lifecycle — per tenant
  and as a cross-tenant aggregate: pull requests opened, their state, merges **observed**, and deliveries
  that are undeliverable with their typed cause.
- **FR49.** A merge SHALL be shown as **observed**, never inferred from a pull request closing, and the
  surface SHALL distinguish *merged*, *closed unmerged* and *state unknown* as three outcomes.
- **FR50.** The console SHALL show the **change-delivery rollout** state (ADR-010): which rollout stage a
  change is in, and the undeliverable count with its causes.
- **FR51.** The audit log's coverage SHALL be stated honestly. It mirrors P6 autonomous merges; it does
  not mirror P12 customer-CI-mediated deliveries. The surface SHALL name which merge paths the chain
  covers rather than implying it covers all of them, and the delivery surface SHALL link to the chain for
  the paths it does cover.
- **FR52.** Delivery reads SHALL be read-only. This phase SHALL add no control that opens, closes,
  retries or merges a delivery — delivery is downstream of verification and the platform holds no forge
  credential by default.

### Release and trust oversight (capability `operator-release-oversight`)

- **FR53.** The console SHALL show **published releases per install channel**, with version, publication
  date, and the artefacts published per platform.
- **FR54.** The console SHALL show the **active signing key** and every retired key with its rotation
  date and the reason recorded for the rotation, and SHALL identify published artefacts signed with a
  retired key.
- **FR55.** The console SHALL show **artefact verification state** per artefact — checksum and signature
  — and SHALL distinguish *verified*, *failed verification* and *not yet verified* as three outcomes.
- **FR56.** The console SHALL show the **post-publish smoke result per platform image**, and SHALL
  distinguish *passed*, *failed* and **_queued until timeout_** — because a retired runner label queues
  rather than failing, and reading that as a failure sends an engineer to the wrong problem.
- **FR57.** No key material SHALL appear on any surface. A key SHALL be identified by its identifier and
  fingerprint only. The surface SHALL NOT offer key generation, export, or any operation whose output is
  key material.
- **FR58.** Release reads SHALL be read-only in this phase. Any control that halts or unpublishes a
  channel is deferred to the decision in §14.

### Axis oversight (capability `operator-axis-oversight`)

- **FR59.** The console SHALL show, per optimization axis, its declared `EXISTS / PARTIAL / ABSENT`
  status and its fleet-wide adoption — how many tenants and nodes carry an override on that axis.
- **FR60.** The console SHALL show **refusal counts by typed cause**, per axis and per language, using
  the stable refusal identifiers rather than prose. The three causes —
  `not-expressible-at-a-call-site`, `call-site-cannot-carry-it`, `no-materializer-for-this-language` —
  SHALL remain distinguishable, because they are answered by three different people.
- **FR61.** The console SHALL rank **which artefact would close the most refusals** — a form row, a list
  splitter, a statement resolver, a registry row, a frontend field — so the backlog is ordered by
  evidence.
- **FR62.** The coverage matrix SHALL be read from the **one coverage source** that the transform's
  refusal, preflight, the CLI and the customer console read. The console SHALL NOT compute, cache or
  reformat coverage. Both directions SHALL hold: this surface SHALL NOT offer a cell the engine refuses,
  and SHALL NOT omit a cell the engine materializes.
- **FR63.** An absent coverage row SHALL NOT render as *not applicable*. *Not applicable* is a claim
  about the customer's code and is the one thing coverage data must never say by accident; an absent row
  SHALL render as *unknown* and SHALL name what is missing.
- **FR64.** A coverage gap SHALL NOT be presented as a plan boundary. It is *not yet applied by the
  platform*, identical on every plan, and the surface SHALL NOT imply a tier unlocks it.

### Metering honesty (capability `operator-metering-honesty`)

- **FR65.** **Link coverage SHALL be displayed beside every SUM-derived figure** on the operator console,
  in the same view, not behind a link. The rule is stated once in `project.md`; the operator surface
  currently predates it.
- **FR66.** The console SHALL NOT infer or extrapolate unlinked spend, and SHALL NOT render a derived
  figure whose coverage is unknown. Unknown coverage SHALL render as unknown.
- **FR67.** Every aggregate improvement, savings or quality figure SHALL exclude `unverified`
  authored changes, and the surface SHALL state that it does. The exclusion SHALL be asserted, not
  documented — `unverified` is a state the ledger filters on, not a badge a refactor can drop.
- **FR68.** A gainshare or verified-savings figure SHALL draw exclusively on the P5.5 verified-delta
  ledger, and the surface SHALL name that provenance where the figure appears.
- **FR69.** Prices SHALL remain references, never values. Plans SHALL be named. The existing bundle price
  fence SHALL continue to fail the build on a priced literal, and this phase SHALL add none.

### Identity, consent and reporting health (capability `operator-oversight-health`)

- **FR70.** An operator session SHALL show **which factor authenticated it** and when, so a superadmin
  reviewing activity is not inferring authentication strength.
- **FR71.** The console SHALL show, per tenant, which legal document versions are accepted and which are
  **owed** after a material publication, each linking to the archived text carrying the accepted content
  hash.
- **FR72.** The console SHALL show each observability integration's state as one of `absent`,
  `configured`, `degraded` — the three P24 states — read from the readiness surface, never as a boolean
  and never from a third party's dashboard.
- **FR73.** The console SHALL show, per tenant, which **deployment shape and version** it is running,
  where that is known from an existing signal, and SHALL render *unknown* where it is not. It SHALL NOT
  guess a version.

### Interface floor (unchanged from P8, restated because this phase adds surfaces)

- **FR74.** Every new surface SHALL meet the existing floor: the single token set with no colour,
  spacing, type-size or radius literal; English strings with `en-US` pinned through the one swap point;
  keyboard reachability with visible focus; WCAG 2.1 AA in both resolved themes; the viewport floor; and
  the payload ceiling.
- **FR75.** Failure classes SHALL stay distinguishable: subsystem-not-mounted, not-found and transport
  failure are three outcomes with three messages. A 404 SHALL NOT be mapped to a business state, and an
  empty state SHALL NOT be rendered as a zero.
- **FR76.** Read models SHALL be computed server-side and rendered as received. The console SHALL NOT
  derive, re-rank or recompute a statistical claim, a coverage percentage or a rollout outcome.
- **FR77.** The hazard palette SHALL remain reserved for hazard. This phase adds read surfaces; a read
  surface SHALL NOT use hazard colour to signal volume or novelty.

---

## 7. Non-functional requirements

- **NFR1 — Every cross-tenant read is logged.** Each new aggregate is a cross-tenant read and inherits
  the existing rule: it is recorded in the audit chain with actor, capability and scope. Adding
  convenience SHALL NOT add an unlogged read.
- **NFR2 — Least privilege, unchanged.** New capabilities partition rather than widen. No existing role
  gains a capability it did not have because a new surface made it convenient.
- **NFR3 — No new store.** Every read derives from an existing store. Creating a table is a one-way door
  and this phase does not need one; where a read is not derivable, FR45 records it rather than building
  for it.
- **NFR4 — No aggregate hides a single-sample defect.** Where a fleet number is shown, the surface offers
  the drill-down to the individual records behind it. An aggregate without a path to its samples is how a
  single-tenant defect stays invisible.
- **NFR5 — Read-only by default.** This phase adds no destructive control. Every existing write keeps its
  classification, its reason field and its confirmation.
- **NFR6 — Payload ceiling holds.** New surfaces ship under the existing 1,400,000-byte ceiling; the
  build fails and names the overage otherwise.
- **NFR7 — Fail-static reads.** A subsystem that is not mounted renders as not mounted. The console does
  not blank, does not retry into a spinner forever, and does not render a partial aggregate as a complete
  one.
- **NFR8 — Acceptance is a rendered browser.** A green build, a passing type-check and green unit tests
  are all compatible with a page that renders nothing or the wrong subject.
- **NFR9 — Every fence can go red.** The ledger fence, the both-directions assertions, the coverage
  parity check and the `unverified`-exclusion assertion each have a demonstrated failing case.

---

## 8. System design summary

### 8.1 Shape

```
   ┌───────────────────────── THE LEDGER (this phase's product) ─────────────────────────┐
   │  openspec/specs/<capability>/  ──▶  operator-surface-ledger.md                       │
   │                                      ├─ surface: /delivery      ──▶ surfaces.ts      │
   │                                      ├─ surface: /releases      ──▶ surfaces.ts      │
   │                                      ├─ no-operator-surface: <reason> + <phase>      │
   │                                      └─ not-yet-readable: <collection that would>    │
   │  FENCE: a capability in neither column ⇒ BUILD FAILS, naming the capability          │
   │  BOTH DIRECTIONS: every named surface exists; every surface is named                 │
   └────────────────────────────────────────────────────────────────────────────────────┘

   OPERATOR CONSOLE (own origin, own BFF, own identity + RBAC)
   ├─ existing ──── Overview · Tenants · Billing · Registry · Jobs & Fleet
   │                Kill Switch · Cross-Tenant · Audit · Compliance
   └─ new (read-only) ─────────────────────────────────────────────────────────────────
      ├─ Delivery      ◀── deliveryrecord + forgedelivery + changedelivery (rollout)
      ├─ Releases      ◀── distribution (channels, trust, manifests, smoke)
      ├─ Axes          ◀── the ONE coverage source + axis adoption + typed refusals
      └─ Oversight     ◀── readiness states · consent/legal owed · session factor

   HONESTY CORRECTIONS applied to EXISTING surfaces
      Billing   + link coverage beside every SUM-derived figure          (project.md rule)
      Billing   + gainshare provenance named (P5.5 verified-delta ledger only)
      Cross-Tenant + `unverified` authored changes provably excluded
      Audit     + states which merge paths the chain covers (P6, not P12)

   ┌─ THE BEHAVIOURAL GOAL ───────────────────────────────────────────────────────────┐
   │ Before: routine lookup ⇒ impersonate.read ⇒ a logged read of customer data       │
   │ After:  routine lookup ⇒ a cross-tenant aggregate ⇒ no customer-data read        │
   │ Measured as: impersonations-per-support-ticket, before and after                  │
   └──────────────────────────────────────────────────────────────────────────────────┘
```

### 8.2 Decisions

Each decision runs: **the problem → the design → why it fits here → the alternatives and why not → the
effect.**

---

#### D1 — The ledger and its fence come first; the pages come second

**The problem.** Fourteen phases of operator-console drift happened without anything failing. A refresh
that closes today's nine gaps and changes nothing about *why* they opened will be followed by a P40
refresh closing the next nine.

**The design.** A checked-in ledger mapping every capability in `openspec/specs/` to an operator surface
or an explicit reasoned `no-operator-surface` decision, with a build fence that fails on a capability in
neither column and both-directions assertions between the ledger and `surfaces.ts`. It lands **before**
any new page.

**Why it fits here.** This repository has the precedent and the scar. P9 derives every public capability
claim from a checked-in manifest so that a claim outrunning a capability fails the build. P23 does the
same for documentation accuracy. And the frontend scope guard records the lesson explicitly: a manual
horizontal scan missed the fifth occurrence, which proves the rule must be machine-enforced. "Remember to
consider the operator console" is exactly the class of rule that must become a red build.

**Alternatives, and why not.**
- *A review checklist in the PR template.* This is what we effectively had. Fourteen phases is the
  measured failure rate.
- *A dashboard showing coverage without failing anything.* A number nobody is blocked by is a number
  nobody reads.
- *Require an operator surface for every capability.* Rejected as wrong, not merely expensive: plenty of
  capabilities genuinely need no operator surface. Forcing one produces pages that exist to satisfy a
  fence, which is worse than a gap because it looks like coverage.
- *Fence on `docs/prd/` instead of `openspec/specs/`.* Rejected: specs are the current truth of what is
  built; PRDs include what is proposed. Fencing on proposals would fail the build for unbuilt things.

**The effect.** Adding a capability without deciding its operator story fails the build with the
capability named. A deliberate "no surface needed" is one line with a reason, so the cheap path is
honest rather than silent.

---

#### D2 — Every new surface is read-only, and the one candidate write is a decision fork

**The problem.** A gap analysis generates write controls: retry a delivery, unpublish a bad release, halt
a channel. Each is plausible and each expands the platform's most dangerous surface during a phase whose
purpose is visibility.

**The design.** Every surface added here is read-only. The one control with a real argument — halting an
install channel so a bad release stops reaching strangers — is written up as a decision in §14 with both
paths costed, and it is not built in this phase.

**Why it fits here.** The console's existing discipline is audit-then-effect with a recorded reason and a
second confirmation, and its trend ledger refuses, without qualification, anything that performs a
privileged action on a user's behalf. A refresh phase is where a new destructive control is least likely
to get the scrutiny it deserves, because it arrives inside a page whose purpose is a table. And when the
correct answer is expensive, the rule is to present both paths and let the decision be made explicitly —
not to absorb it.

**Alternatives, and why not.**
- *Add the channel halt now, behind a typed-target confirmation.* Genuinely defensible after the P20 key
  rotation. Deferred rather than refused, because a control that stops software reaching customers
  deserves its own design conversation, not a paragraph in a refresh.
- *Add "retry a delivery".* Rejected on principle, not cost: delivery is downstream of verification and
  is never a path around it, and the platform holds no forge credential by default. An operator retry
  would need one.

**The effect.** The blast radius of this phase is bounded by construction: it can render a wrong number,
which is a bug, and it cannot take an action, which would be an incident.

---

#### D3 — Read the one coverage source; never compute coverage

**The problem.** A coverage matrix is a table of a few thousand cells, and a console rendering it is one
`filter` away from becoming a second opinion about coverage.

**The design.** The console reads the same coverage source the transform's refusal, the preflight, the
CLI and the customer console read, and renders it as received. Both directions asserted: it may not offer
a cell the engine refuses, and may not omit a cell the engine materializes.

**Why it fits here.** The `language-coverage` contract states it directly: one coverage source, read by
everything that states coverage, asserted in both directions. And the console's read-model rule is
already that scores, intervals, ties, ranks, gate outcomes and coverage percentages are computed
server-side and rendered as received, because a client-side recomputation would be a second source of
truth for a statistical claim.

**Alternatives, and why not.**
- *Compute a fleet roll-up in the BFF.* The BFF is a pass-through, not a brain — no merging, re-ranking,
  reformatting or status translation. A roll-up is all four.
- *Cache the matrix for latency.* A cached refusal that the engine has since stopped refusing is a
  surface offering a cell the engine refuses, which is the exact failure the both-directions assertion
  exists to catch.

**The effect.** An operator and a customer looking at the same node's coverage see the same answer, and a
disagreement is a test failure rather than a support conversation.

---

#### D4 — *Not applicable* is never rendered from an absent row

**The problem.** The natural rendering of a missing coverage row is a blank cell or a dash, and a reader
reads a dash as *not applicable*. But *not applicable* is a claim about the customer's code — "your call
site cannot carry this" — while the truth may be "we have not built the materializer".

**The design.** An absent row renders as **unknown**, naming what is missing. *Not applicable* is
rendered only from a present row whose named cause says so.

**Why it fits here.** The contract names this specific hazard: an absent row rendering as *not
applicable* is a claim about the customer's code, and it is the one thing coverage data must never say by
accident. It is also the more dangerous direction for the platform — it converts our backlog into the
customer's problem, invisibly, and the customer has no way to discover the substitution.

**Alternatives, and why not.**
- *Render a dash and explain in a tooltip.* A tooltip is not read; the dash is.
- *Suppress rows with no data.* Now the reader has no way to know a row exists, which is worse: the
  absence is invisible instead of merely ambiguous.

**The effect.** A cell says one of: applies, refused with a named cause, or unknown with a named missing
input. Three states, three renderings, and no honest reading of the table blames the customer for our
backlog.

---

#### D5 — Correct the existing surfaces' honesty before adding new ones

**The problem.** The operator billing surface shows SUM-derived figures with no link coverage beside
them, against a rule stated in `project.md`. Cross-tenant aggregates do not demonstrably exclude
`unverified` authored changes. The audit log implies coverage of "every merge" while mirroring one of two
merge paths. Each is a wrong number on a shipped page, and a wrong number is worse than a missing one —
**the wrong number gets acted on.**

**The design.** These three corrections land in the first wave, before any new page.

**Why it fits here.** The bundle scanner already makes this argument in its own comment, about a
different measurement: it refuses to report a number it cannot measure honestly, because a scan that
reports a wrong number is worse than one that reports none, since the wrong number gets acted on. A
billing operator issuing a credit against a SUM figure with 31% link coverage is that sentence with money
attached.

**Alternatives, and why not.**
- *Ship new surfaces first; corrections in a follow-up.* Rejected. A phase whose stated purpose is
  operator honesty cannot leave three known dishonest figures on shipped pages while adding four pages.
- *Add a footnote naming the caveat.* A footnote beside a figure is read as reassurance, not as a caveat.

**The effect.** A billing operator sees `SUM $12,400 · link coverage 31%` and does not issue the credit.
That is the whole point: coverage beside the figure changes a decision.

---

#### D6 — Measure the phase by impersonation displaced, not by pages shipped

**The problem.** A refresh phase's natural success metric is "the surfaces exist", which is satisfied by
nine pages nobody opens.

**The design.** The headline metric is the change in **impersonations that existed only to read something
a cross-tenant aggregate should have shown**, measured before and after from the existing impersonation
audit records and their recorded reasons.

**Why it fits here.** Impersonation is the platform's most privileged read: reason-required,
time-bounded, read-scoped, fully audited — all of which are the marks of a control designed to be rare.
It is currently rare-by-policy and routine-by-necessity, because it is the only tool that can answer
several ordinary questions. Reducing that is a genuine data-protection improvement that the platform can
evidence to a customer, and the measurement already exists in the audit chain.

**Alternatives, and why not.**
- *Count new surfaces.* Satisfied by pages nobody opens.
- *Survey operators.* A preference, not a measurement, and this repository's rule is that a preference is
  not evidence.

**The effect.** The phase can fail visibly. If impersonation volume does not move, the surfaces answered
the wrong questions, and that is worth knowing.

---

#### D7 — No new table; where a read is not derivable, say so

**The problem.** Several wanted reads — per-tenant deployed version, fleet-wide axis adoption — may not
be derivable from what is stored today. The tempting fix is a table.

**The design.** No new store. Every read derives from an existing one. Where it does not, the ledger
records `not-yet-readable` and **names the collection** that would make it readable.

**Why it fits here.** Creating a table is a one-way door and this project requires at least two
non-table alternatives before one is created, and refuses "build it now for future use". More
importantly, the alternative to a table here is not a gap — it is an **honest gap with a named cause**,
which is the same shape as a typed refusal and is directly actionable by the next phase.

**Alternatives, and why not.**
- *Add a per-tenant deployment-state table.* Rejected: it would be populated by a signal that does not
  exist, so the table would be a place to store guesses.
- *Estimate the version from the last-seen API contract version.* Rejected: an inferred version rendered
  as a version is a wrong number that gets acted on during an incident.

**The effect.** A ledger row reads `deployed-version: not-yet-readable — requires a deployment heartbeat
carrying the release identifier`. The next phase has a specified task instead of a vague wish.

---

### 8.3 Design key points

**What original need does this answer?**
From §1: fourteen phases landed and the operator's surface for all of them is nine pages designed before
any of it existed. Concretely — delivery is invisible, releases and signing-key state are invisible, axis
refusals are invisible, SUM figures carry no coverage, and **the platform's most privileged read has
become the workaround for the missing ones.**

**Why designed this way**
- The **fence precedes the pages**, because the pages are this phase's output and the fence is its
  product. Nine gaps closed with the drift mechanism intact buys eighteen months; a fence buys the
  property.
- **Honesty corrections precede new surfaces**, because a wrong number on a shipped page outranks a
  missing page: the wrong number gets acted on.
- **Read-only by default**, because a refresh is where a new destructive control gets the least scrutiny.
- **Read the one source, compute nothing**, because a console that computes coverage becomes a second
  opinion about coverage.
- **Absence renders as unknown, never as not-applicable**, because the wrong rendering converts our
  backlog into a claim about the customer's code, invisibly.

**Key business decisions**
- Who may see a fleet-wide aggregate: a granted capability, always logged, no exceptions for convenience.
- What replaces impersonation: routine lookups become cross-tenant reads; impersonation returns to
  reproducing a customer's own view.
- What we will not claim: a coverage gap is not a plan boundary, a SUM figure with unknown coverage is
  not shown, and a gainshare figure names the verified-delta ledger it drew on.
- Who decides the channel halt: not this phase. It is written up as a fork.

**Key technical decisions**
- Ledger keyed on `openspec/specs/` (built truth), not `docs/prd/` (includes proposals).
- Ledger asserted in both directions against `surfaces.ts`, which stays the single map.
- `not-yet-readable` as a first-class ledger state naming the missing collection — the typed-refusal
  shape applied to oversight.
- No new table; no new collection path; no BFF-side aggregation (the BFF is a pass-through).
- New capabilities partition existing roles rather than widening them; the deny-by-default matrix test
  keeps iterating every capability.
- Three-state renderings throughout — merged / closed-unmerged / unknown; verified / failed /
  not-yet-verified; passed / failed / queued-until-timeout; absent / configured / degraded — because
  collapsing any of them to two hides the state an operator most needs during an incident.

---

## 9. Design by role lens

### 9.1 Senior Product Designer — *the left nav holds task domains; an action is not a destination*

- **Four new nav entries, not fourteen.** Delivery, Releases, Axes, Oversight. The nav holds task
  domains; everything else is a destination inside one, reachable by name from the palette. A nav that
  grows one entry per phase stops being navigable.
- **Every addition is a destination, never a command.** The palette's existing contract holds: selecting
  an entry opens the surface or the confirmation, and performs nothing.
- **The information architecture is operator-shaped, not phase-shaped.** An operator with a ticket does
  not think "P12". They think "this customer's PR never opened". So *Delivery* holds delivery records,
  rollout state and the audit link together, even though three phases produced them.
- **Three states get three renderings, everywhere.** Merged / closed-unmerged / unknown. Verified /
  failed / not-yet-verified. Passed / failed / queued-until-timeout. Absent / configured / degraded.
  Collapsing any pair is the failure this console already forbids for 404-versus-503, and here it would
  hide the state that matters most.
- **The term dictionary gains entries before the pages do.** *Delivery record*, *observed merge*,
  *undeliverable*, *rollout stage*, *install channel*, *signing key*, *refusal cause*, *link coverage*,
  *unverified*. Interface text, the ER entities and the code names stay three separable layers, and the
  operator console must not invent a fifth synonym for something the customer console already names.
- **Drill-down from every aggregate.** A fleet number with no path to its samples is how a
  single-tenant defect stays invisible.
- **No functionality is lost.** A UI change may not drop an existing capability, and this phase adds
  surfaces beside the nine rather than reorganising them.

### 9.2 Senior System Designer — *arbitrate by priority; do not open a one-way door*

- **The arbitration.** Adding a write control would be priority 1 and 2 (security, stability) traded for
  priority 8 (a slightly better page), so every new surface is read-only. Correcting the SUM-coverage
  omission is priority 2 versus priority 8, so it lands in wave one. Building the fence before the pages
  is priority 5 (evolvability) versus priority 8, and the fence wins.
- **Escalation, not silent downgrade.** The channel-halt control is the one place the correct answer may
  be expensive; both paths are costed in §14 and the decision is handed over rather than absorbed.
- **No new surface area.** No new table (D7), no new collection path, no new aggregation layer. New
  capabilities *partition*; they do not widen an existing role.
- **The fence is the evolvability decision.** The one-way door this phase closes is not a schema — it is
  the property that operator oversight cannot silently fall behind. That property is worth more than any
  of the four pages, which is why it ships first.
- **Where a write must reconcile with a read.** The delivery surface reads an append-only record whose
  merge is *observed*, and the phase adds no write, so there is no new "A must be accompanied by B"
  invariant to reconcile. Where one already exists — the audit chain's fail-closed relationship to the
  P6 change ledger — this phase reads it and does not decorate it.
- **Control plane and data plane stay separate.** The operator console is a control-plane surface on its
  own origin with its own identity. Nothing added here reaches into a customer request path, and no
  customer-facing surface gains a cross-tenant read.

### 9.3 Senior Backend Dev — *contracts outlive code; a 200 is not evidence*

- **Read models are computed server-side and rendered as received.** New aggregates are Go read models
  in `internal/adminops`, alongside the existing ones. The BFF stays a pass-through — no merging, no
  re-ranking, no reformatting, no status translation.
- **The coverage source is read, never duplicated.** No second query that reimplements a refusal, and no
  cached copy. Both-directions parity between what the engine refuses and what the surface offers is
  asserted, not assumed.
- **Three-state types, not booleans.** Merge state, verification state, smoke state and integration
  state are each a named enum in Go. A `bool` here is how *queued until timeout* becomes *failed*.
- **`unverified` exclusion is enforced at the query, and asserted.** An aggregate that filters in the
  read model can be broken by a refactor; the assertion is a test that seeds an `unverified` authored
  change and proves it contributes zero to every aggregate improvement, savings and quality figure.
- **No new table.** Every read derives from an existing store. A read that cannot is recorded in the
  ledger rather than given a table to live in.
- **Central enums for refusal causes and metric names.** The refusal identifiers are stable identifiers
  by contract, not prose, and the console renders the identifier's label from one place.
- **Two hundred is not evidence.** Each new endpoint's test asserts on the read model's contents against
  a real store, not on the status code. A green fetch is compatible with an empty aggregate rendered as
  a zero.
- **Every cross-tenant read writes its audit entry on the same code path as the read**, not from a
  poller, so a crash cannot leave a read unlogged.

### 9.4 Senior Frontend Dev — *match the codebase; no improvised styling; three states stay three*

- **Three files per new page, or the slot vanishes silently.** The route, the `surfaces.ts` entry with
  its capability, and the ledger row. This console's own precedent is that a page added without its
  registry entry is unreachable from the palette while looking fine in the nav; here it would also fail
  the ledger fence, which is the improvement.
- **No improvised styling.** Every visual value resolves to the shared token set; `scan:tokens` fails on
  a literal. The hazard palette stays reserved for hazard — a read surface does not use danger colour to
  signal volume.
- **Capability filtering through the same map the backend enforces.** A capability the role does not
  grant is absent from the nav and absent from the palette; it is never offered and then refused.
- **`<Tabs>` for dense subject grouping, viewport-first.** The console's own precedent: a dense surface
  laid out below the fold fails the viewport floor.
- **Failure classes stay distinguishable.** Subsystem-not-mounted, not-found and transport failure are
  three messages. An empty aggregate renders as *no records*, never as `0`.
- **Payload ceiling.** Four new pages under the existing 1,400,000-byte ceiling; the build names the
  overage otherwise. No charting library arrives for this — the existing `chart.tsx` primitive is the
  answer, and the trend ledger's rejections stay rejected.
- **`next dev` must not be running during the test run.** It clobbers `.next` and makes the bundle scan
  refuse to measure — a known trap in this repository, and worth stating in the task list rather than
  rediscovering.
- **Acceptance is a rendered browser.** Four new pages, both themes, at the viewport floor, with a role
  that grants the capability and a role that does not.

### 9.5 Senior AI Engineer — *aggregates hide single-sample defects; a preference is not evidence*

- **This phase's whole purpose is to make an evidence-based axis decision possible.** FR61 — ranking
  which artefact would close the most refusals — is the deliverable that changes how the next axis phase
  is chosen. Six axes were prioritised without it.
- **Every aggregate offers its samples.** A refusal count of 1,200 that cannot be drilled into is how a
  single tenant's pathological repository looks like a fleet-wide pattern. NFR4 is here for that reason,
  and it is the same discipline as refusing to read a single-sample failure off an aggregate metric.
- **The console ranks nothing statistical.** Only P4 ranks; only a P5.5 verified delta is a claim. A
  refusal count is a **count**, not a score, and the surface must not present it with the visual grammar
  of a ranked result.
- **`unverified` stays out of every improvement number.** A user may author a change; a user may not
  author the evidence. An operator aggregate that quietly included authored-but-unverified changes would
  be the platform citing customer opinion as measurement.
- **No new model, no classifier, no inference.** Nothing here proposes, predicts or scores. Where data is
  missing, the surface says so — it does not estimate.

### 9.6 Senior DevOps Engineer — *blast radius, reversible, observable, least privilege*

- **Blast radius is bounded by construction.** Read-only surfaces can render a wrong number; they cannot
  take an action. That is the deliberate ceiling on this phase's risk.
- **Release oversight is the operationally load-bearing addition.** After a signing-key rotation
  following a leak, "which key is active, which are retired and when, and which published artefacts were
  signed with a retired one" is an incident question. FR54 exists because that incident happened.
- **Assert the transition sequence, not the end state.** Publish → verify → smoke is a sequence, and the
  surface must show where it stopped. A release that publishes green and smokes red is precisely the state
  that reaches a stranger's laptop, and a surface showing only the final state hides it.
- **Queued-until-timeout is its own state.** A retired runner label queues until timeout rather than
  failing. Rendering that as *failed* sends an engineer to debug a build that never ran. This is a
  measured lesson from P20, not a hypothetical.
- **No key material on any surface, ever.** Identifier and fingerprint only; no generation, no export.
  A signing key has already leaked once in this project's history by being emitted into a session
  transcript, and the console must not be a second path.
- **Health is externally readable and the dashboard is not the judge.** The three P24 integration states
  come from the platform's own readiness surface. "It looks fine in the third party's UI" is not a health
  signal.
- **Least privilege.** New capabilities partition; no role widens because a new page made it convenient.
  Every cross-tenant read stays logged.
- **Reversibility.** Removing a surface removes a read. No migration, no data to unwind. The rollback for
  this phase is a deploy.

### 9.7 Senior QA Engineer — *green is worth having only if green is credible*

- **The ledger fence is demonstrated red first.** Add a capability directory with no ledger row: the
  build must fail naming it. Point a ledger row at a non-existent surface: fail. Leave a `surfaces.ts`
  destination unnamed by any row: fail. A fence never observed failing has not been checked.
- **Both-directions assertions, everywhere they are claimed.** Ledger↔`surfaces.ts`. Coverage offered↔
  coverage materialized. A one-directional check on a bidirectional claim is a fence with a hole in the
  shape of the direction nobody tested.
- **The `unverified` exclusion is tested by seeding one.** Create an authored change, leave it
  unverified, and assert it contributes zero to every aggregate improvement, savings and quality figure.
  Asserting the filter exists in the query is asserting that we wrote a `WHERE` clause.
- **Coverage parity is tested against the engine, not a fixture.** A fixture asserting the surface shows
  what the fixture says proves nothing about what the engine refuses. Drive the real coverage source.
- **Live four-layer assertion where a record is read.** Delivery, release and audit reads are asserted
  against a real Postgres running the real migration chain — no inline `CREATE TABLE` standing in for a
  production table, because the fixture itself can be wrong.
- **Browser acceptance is the gate.** Four new pages, both themes, viewport floor, one role that grants
  each capability and one that does not — the second is how you catch a page that renders for everyone.
- **Regression pointers for the three honesty corrections.** Each of link coverage beside SUM, `unverified`
  exclusion, and the audit log's stated merge-path coverage gets a named test that fails if a later change
  removes it.
- **Audit the completion claims.** For every `[x]` in the task list, name the assertion and confirm it
  exists and runs. This project has found never-built tasks and lying test runners under a fully green
  suite; a pointer is not evidence until it resolves.

### 9.8 Senior Sales Operations — *only promise what shipped; state the boundary out loud*

- **This phase is internal, and that is the story.** It ships no customer feature. What it produces for a
  customer conversation is an **oversight posture**, which is exactly what a security review asks about.
- **The strongest claim it creates.** "Answering a routine support question about your account no longer
  requires anyone reading your data. When it does, it is reason-required, time-bounded, read-scoped and
  audited — and here is the record." That is checkable and it is new.
- **Do not oversell the axis surface.** Fleet refusal counts inform our roadmap. They are not a promise
  that any specific language materializer is coming, and a coverage gap is *not yet applied by the
  platform* — identical on every plan, never a tier boundary. Saying "upgrade and it works" would be
  false in a way the customer discovers.
- **Honest numbers only.** A SUM figure now carries link coverage; quote both or neither. A savings figure
  draws on the verified-delta ledger; say so. An unverified authored change is excluded from every
  improvement number; do not quote it as adoption.
- **Do not leak internals.** Refusal-cause distributions, fleet composition, per-tenant volumes and
  release-pipeline topology stay internal. What a customer may hear is *their own* state.
- **FAQ entries this creates.** *Does your staff read my data to support me?* — Only when they must
  reproduce your view, with a recorded reason, time-bounded and audited; routine lookups no longer touch
  it. *Can you tell me which version I am running?* — Where a signal exists, yes; where it does not, the
  console says unknown rather than guessing. *Who can see my delivery history?* — Operators with the
  granted capability, and every cross-tenant view is logged.

---

## 10. Dependencies

**Upstream (must exist; all do)**
- **P8** — the console, its shell, `surfaces.ts`, the RBAC matrix, the audit chain, the confirmation
  discipline, `internal/adminops`.
- **P10** — the prompt registry (content-addressed, immutable versions).
- **P11** — run linking, link coverage, metering as a read over telemetry.
- **P12** — `forgedelivery`, `deliveryrecord`; merge observed, not inferred.
- **P13–P18** — the six axes, and `authored-change` / `language-coverage` / `change-delivery`; in
  particular the **one coverage source** this phase reads and must not duplicate.
- **P19** — the deployment substrates and the console's own deploy unit.
- **P20** — `internal/distribution`: channels, manifests, trust, the release pipeline, the smoke job.
- **P23** — legal documents, consent records, material versioning.
- **P24** — the three readiness states this phase renders.

**Partially available, and handled honestly**
- **P21** — the payment integration is not built. This phase specifies the webhook and dunning surface
  and records it in the ledger as `not-yet-readable` until P21 lands; it does not render an empty page as
  a working one.
- **P22** — the SSO/MFA **verifier** is real; the IdP is a test-mode fixture
  (`internal/adminidentity/fixture.go`). This phase surfaces which factor authenticated a session, which
  works against either, and does not claim a real IdP exists.

**Downstream (this unblocks)**
- The next axis or language-materializer phase, which can be prioritised from FR61's ranking instead of
  from taste.
- Any future operator write control, which now arrives on a surface that already shows the state it would
  act on.
- P21 and P22, each of which gains an operator surface waiting for it rather than needing one built.

---

## 11. Risks & mitigations

| Risk | Why it is real here | Mitigation |
|---|---|---|
| **The refresh closes today's gaps and drift resumes** | It is precisely what happened after P8, and nothing failed | The ledger and its fence ship in wave one, before any page; both directions asserted; a capability in neither column fails the build |
| **The ledger becomes a rubber stamp** — every capability marked `no-operator-surface: not needed` | The cheap path is always available | The decision requires a reason and the deciding phase, and it is reviewed; a fence cannot force judgement, but it can force the judgement to be recorded and attributable |
| **A wrong number gets acted on** | A billing operator issuing a credit against a SUM figure with 31% coverage | Coverage beside every derived figure; unknown coverage renders as unknown; a figure with unknown coverage is not rendered at all |
| **The console becomes a second opinion about coverage** | A `filter` in a component is one line away | Read the one source; both-directions parity asserted against the real engine; the BFF stays a pass-through |
| **An absent coverage row reads as *not applicable*** | It is the natural rendering of missing data | Absent renders as *unknown* with the missing input named; *not applicable* only from a present row whose cause says so |
| **A write control slips in** | Four new pages full of tables invite a button | Read-only by construction; the one candidate is a §14 decision fork with both paths costed |
| **A new surface widens a role** | Convenience arguments are persuasive during a refresh | New capabilities partition; the deny-by-default matrix test iterates every capability; no existing role gains one |
| **Key material reaches a surface** | It has leaked once in this project, via a session transcript | Identifier and fingerprint only; no generation, no export, asserted |
| **An aggregate hides a single-tenant defect** | Fleet numbers are the point of this phase | Every aggregate offers its drill-down; the surface does not present a count with the grammar of a rank |
| **P21/P22 surfaces render empty pages as working ones** | Both are partially built | `not-yet-readable` in the ledger with the missing input named; an empty state is never rendered as a zero |
| **Impersonation volume does not move** | The surfaces may answer the wrong questions | It is the headline metric (D6), measured before and after from the existing audit records — so the phase can fail visibly |

---

## 12. Rollout & test strategy

**Wave 26a — the fence and the honesty corrections. No new pages.**
- The ledger, populated for every existing capability; the build fence; both-directions assertions.
- Link coverage beside every SUM-derived figure on the operator billing surface.
- `unverified` authored changes provably excluded from every aggregate, proven by seeding one.
- The audit surface states which merge paths the chain covers (P6 autonomous merges) and which it does
  not (P12 customer-CI-mediated deliveries).
- **Gate:** every fence demonstrated red; the three corrections have named regression tests.

**Wave 26b — Delivery.** Delivery records per tenant and cross-tenant; observed merges as three states;
change-delivery rollout stage; undeliverable counts with typed causes; the audit link for the paths the
chain covers. Read-only.

**Wave 26c — Releases & Trust.** Published releases per channel; the active signing key and every retired
key with rotation date and reason; artefact verification as three states; post-publish smoke per platform
as three states including *queued until timeout*. No key material. Read-only.

**Wave 26d — Axes.** Per-axis status and fleet adoption; refusal counts by stable typed cause and by
language; the ranking of which artefact would close the most refusals; the coverage matrix read from the
one source with both-directions parity asserted, and absent rows rendered as *unknown*.

**Wave 26e — Oversight.** Session authentication factor; consent and legal-acceptance state per tenant
with links to archived text; the three P24 integration states; per-tenant deployment shape and version
where derivable, *unknown* where not.

**Wave 26f — Ledger reconciliation and exit.** Every capability resolved to a surface, a reasoned
`no-operator-surface`, or `not-yet-readable` with its missing collection named. The impersonation-ratio
measurement taken and compared against the pre-phase baseline.

**Test layers**
- *Unit* — ledger parsing and the fence; three-state enums; capability filtering.
- *Contract* — each new read model against a **real Postgres** running the real migration chain; no
  inline `CREATE TABLE` standing in for a production table.
- *Parity* — coverage offered ↔ coverage materialized, driven against the real engine, both directions.
- *Build gates* — ledger fence; `surfaces.ts` ↔ ledger both directions; tokens; payload ceiling; price
  literal.
- *Browser acceptance (the gate)* — four new pages, both themes, viewport floor, with a granting role
  and a non-granting role.
- *Audit trail* — each new cross-tenant read produces its audit entry on the same code path as the read.
- *Regression pointers* — one named test per honesty correction, each naming the requirement it defends.

**Pre-flight note.** `next dev` must not be running when the console test suite executes: it clobbers
`.next`, makes the bundle scan refuse to measure, and manufactures a large number of spurious sign-in
failures. Known trap in this repository; stated here so it is not rediscovered.

---

## 13. Success metrics & acceptance criteria

**Exit checklist** — walked end to end at exit; evidence per item.

- [x] Every capability in `openspec/specs/` resolves in the ledger to a surface, a reasoned
      `no-operator-surface`, or `not-yet-readable` with its missing collection named. No capability is
      unresolved. — `npm run scan:ledger`: *56 row(s), 18 destination(s), every capability resolved and
      both directions asserted.*
- [x] The ledger fence has been demonstrated red three ways: an unresolved capability, a row pointing at
      a non-existent surface, and a `surfaces.ts` destination no row names. — **Four** ways, in
      `tests/surface-ledger.test.mjs`, each committing the real violation and requiring exit 1 that names
      the thing: an unresolved capability, a row naming a missing destination, an unjustified
      destination, and a `not-yet-readable` row with no collection named. Plus a fourth state, an
      unattributable absence, a reasonless absence, and a ledger with no scope statement.
- [x] Every SUM-derived figure on the operator console displays link coverage in the same view; a figure
      with unknown coverage is not rendered. — `TestLinkCoverageIsDisplayedBesideEverySUMDerivedFigure`,
      `TestAFigureWithUnknownCoverageIsWithheld`, and the console fence *🔴 2.2*, itself demonstrated red.
      Browser-verified at 30% coverage and at unknown coverage.
- [x] A seeded `unverified` authored change contributes zero to every aggregate improvement, savings and
      quality figure, proven by test. — `TestASeededUnverifiedAuthoredChangeContributesExactlyZero`,
      which reads the figures rather than the query, and includes the control half.
- [x] The audit surface states which merge paths the chain covers and which it does not. —
      `TestTheAuditSurfaceStatesWhichMergePathsTheChainCovers`; browser-verified.
- [x] Delivery: records per tenant and cross-tenant; merged / closed-unmerged / unknown rendered as three
      states; rollout stage shown; undeliverable count with typed causes. —
      `TestAClosedPullRequestIsNeverReadAsAMerge`, `TestTheThreeMergeStatesStayThree`,
      `TestUndeliverableCausesStayTypedAndSeparate`; browser-verified (5 deliveries: 2 / 2 / 1).
- [x] Releases: channels and published versions; active and retired signing keys with rotation dates and
      reasons; artefacts signed with a retired key identified; verification as three states; smoke as
      three states including *queued until timeout*. — `TestArtefactsSignedWithARetiredKeyAreIdentifiable`,
      `TestArtefactVerificationHasThreeStates`, `TestAQueuedSmokeRunIsNotRenderedAsAFailure`;
      browser-verified.
- [x] No key material appears on any surface, asserted. —
      `TestNoKeyMaterialReachesTheReleaseSurface`, which scans the SERIALISED read model for any live
      public key, any 32-character prefix of one, any 40+ character hex run, and any PEM/OpenSSH marker.
- [x] Axes: per-axis status and adoption; refusal counts by stable typed cause and language; the
      closing-artefact ranking; the coverage matrix read from the one source. —
      `TestTheAxisSurfaceRendersTheAxisStatusAsDeclared`, `TestTheThreeCausesStayDistinguishable`,
      `TestTheRankingIsCountsAndNamesOnlyClosableArtefacts`; browser-verified.
- [x] Coverage parity asserted in both directions against the real engine; an absent row renders as
      *unknown* naming what is missing, never as *not applicable*. —
      `TestTheAxisSurfaceIsAtParityWithTheRealEngine` over `transform.AxisCoverage()`, and
      `TestNoCellRendersAsNotApplicable`. The parity fence is itself demonstrated red by
      `TestTheParityFenceGoesRedOnADeliberateViolation`, in both directions.
- [x] No coverage gap is presented as a plan boundary anywhere on the console. —
      `TestACoverageGapIsNotPresentedAsAPlanBoundary` and the console's *🔴 5.8*.
- [x] Oversight: session authentication factor; consent and legal state per tenant with archived-text
      links; the three P24 integration states; deployment version or an explicit *unknown*. —
      `TestAnOperatorSessionShowsTheFactorThatAuthenticatedIt`,
      `TestAnObservabilityIntegrationHasThreeStatesAndNamesItsFailure`,
      `TestAnUnknownDeployedVersionIsStatedAndNamesTheMissingCollection`; browser-verified.
- [x] Every new cross-tenant read writes its audit entry on the same code path as the read. —
      `TestEveryCrossTenantDeliveryReadIsAuditedOnTheReadPath`; the release, axis and oversight reads use
      the same in-service write, and `TestTheAxisReadIsNotCached` proves two reads produce two entries.
- [x] No new destructive control shipped. No existing role gained a capability. —
      `TestTheDeliverySurfaceIsReadOnly`, `TestTheReleaseSurfaceOffersNoOperationThatProducesKeyMaterial`,
      `TestTheAxisSurfaceIsReadOnlyAndDrillsDown`, `TestTheOversightSurfaceIsReadOnly` (each enumerates
      its service's methods by reflection), and `TestNoExistingRoleWidened` against a hand-written record
      of the pre-P26 holder sets.
      ⚠️ **One deliberate exception, argued rather than hidden:** Support gained the NEW `delivery.read`.
      No pre-existing capability changed hands. See §14 Q3.
- [x] No new table created. — `TestNoNewTableWasCreated`: no migration above `0019` exists.
- [x] Four new pages pass the interface floor: tokens, English/`en-US`, keyboard with visible focus, AA in
      both themes, viewport floor, payload ceiling. — `tests/p26-floor.test.mjs`; token scan green;
      bundle scan *833055 shipped bytes, 566945 under the ceiling*; contrast measured in-browser across
      2275 text elements over four pages × two themes with **0 failures**.
- [x] Browser acceptance recorded for all four pages, in both themes, with a granting and a non-granting
      role. — Recorded in `openspec/changes/p26-operator-console-refresh/tasks.md` §7.9. It caught a real
      defect: a `nil` Go slice marshalling to `null` crashed `/oversight`.
- [x] Every `[x]` in the task list names an assertion that exists and runs. — Audited mechanically at
      exit: **32 cited Go assertions, 32 exist, 32 pass, 0 skipped, 0 phantom**; 9 cited console
      assertions, all present.

**Metrics that would tell us this phase worked**
- **Headline:** impersonation sessions whose recorded reason is a routine lookup an aggregate now answers
  fall measurably against the pre-phase baseline, computed from the existing audit records.
- Median operator time to answer "what happened to this tenant's delivery" drops from *impersonate and
  look* to one page.
- The next axis or materializer decision cites FR61's ranking.
- At least one release or signing-key question is answered from the console during a real incident.
- Zero capabilities unresolved in the ledger at exit, and the fence stays green through the next phase to
  land after this one — the actual test of whether the drift property was fixed.

**Metrics that would tell us it went wrong**
- Impersonation volume unchanged: the surfaces answered the wrong questions.
- A ledger where most capabilities are `no-operator-surface: not needed`: the fence became a rubber stamp.
- A derived figure rendered without its coverage.
- A coverage cell shown that the engine refuses, or omitted that it materializes.

---

## 14. Open questions — RESOLVED AT EXIT

All six were decided during implementation. Each records what was decided, by what argument, and — where
it matters — what was deliberately NOT done.

1. **Should the console be able to halt an install channel?**
   **RESOLVED: Path B — the halt stays in the pipeline for this phase.** The recommendation stood
   unchanged after building the release surface: a control that stops software reaching customers
   deserves its own design conversation, not a paragraph in a refresh, and a refresh is where a new
   destructive control gets the least scrutiny because it arrives inside a page whose purpose is a
   table. The consequence is accepted and stated on the surface itself: `/releases` shows a problem it
   cannot act on. `TestTheReleaseSurfaceOffersNoOperationThatProducesKeyMaterial` and the console's
   *🔴 4.9* keep it that way. **Path A remains open as its own change.**

2. **Is per-tenant deployed version derivable today?**
   **RESOLVED: no.** No signal carries a customer deployment's release identifier. `/oversight` renders
   *unknown* per tenant and names the missing collection — `a deployment heartbeat carrying the release
   identifier` — and the ledger carries the row. Nothing is inferred from an API contract version, a
   feature probe or any other proxy: `TestAnUnknownDeployedVersionIsStatedAndNamesTheMissingCollection`
   fails if a row reports a version and an unknown at the same time.

3. **Does the axis surface need a new capability, or does `crosstenant.read` cover it?**
   **RESOLVED: a separate `axis.read`**, granted to Platform-SRE and Superadmin. `crosstenant.read`
   grants the money aggregates in the same breath, and the question *which materializer would unblock
   the most refused nodes* needs none of them. The same argument produced `delivery.read`.
   🔴 **`delivery.read` is granted to Support, and that is the one place an existing role gained
   anything.** It is a NEW capability, not a widening of an old one — no pre-existing capability changed
   hands, which is what `TestNoExistingRoleWidened` enforces — and it is the grant that makes D6's
   headline metric possible: Support is the role that was opening impersonation sessions to answer
   delivery questions. `TestSupportHoldsOnlyReadAndReadImpersonation` was edited deliberately and
   carries the reasoning, so the change is a visible edit rather than a quiet one.

4. **Does release oversight need a new capability?**
   **RESOLVED: yes — `release.read`,** granted to Platform-SRE and Superadmin only. A release engineer
   is arguably a fifth operator persona and this console has no role for one; until it does, Platform-SRE
   is the nearest holder. NOT granted to Support or Billing-Ops: signing-key state is not something a
   support queue needs, and the alternative — widening `registry.admin` to cover keys — would have
   widened a role that already administers the model registry.

5. **How far back should the delivery surface reach?**
   **RESOLVED: reuse the Audit Log's existing answer** rather than re-decide it. The delivery read model
   reads the record's own per-tenant listing and the fleet view iterates tenants; the surface filters by
   merge outcome through the URL, so a narrowed view is a link. No window and no pagination were
   invented for this phase. 🔴 **This is the item most likely to need revisiting**: delivery records are
   append-only and will grow without bound, and the fleet read currently walks every tenant. It is
   recorded here rather than closed silently.

6. **Should the ledger also cover the customer console?**
   **RESOLVED: no, and the ledger says so in its own first paragraph.** Its scope statement is asserted
   by the fence — a ledger with no stated scope fails — precisely so a `no-operator-surface` row cannot
   be misread as a claim that no surface of any kind exists. The shape anticipates the extension: the
   fence already reads three independent sources against three sections, and a customer-console section
   would be a fourth of the same form.
