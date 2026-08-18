# P26 — Operator Console Refresh

Product rationale: [`../../../docs/prd/P26-operator-console-refresh.md`](../../../docs/prd/P26-operator-console-refresh.md)

## Why

The operator console shipped in P8 with nine surfaces and fifteen deny-by-default capabilities. Since
then fourteen phases landed — P10 studio, P11 run linking, P12 forge delivery, P13–P18 six optimization
axes with three cross-axis contracts, P19 deployment, P20 installable packages, P21 payments, P22 SSO,
P23 legal, P24 analytics — and **the operator console has not moved.**

The gap is verifiable in one grep:

```
$ grep -rn "forgedelivery\|deliveryrecord\|changedelivery\|distribution\|runlink" \
        internal/adminops/ internal/api/p8.go
(no matches)
```

The operator backend imports nothing from the delivery, release or run-linking subsystems. Four
consequences, each checked against the tree rather than assumed:

- **Delivery is invisible.** P12 delivery records and the P13 change-delivery rollout ledger have no
  operator surface. `internal/adminops/mergeaudit.go` mirrors P6 *autonomous* merges into the audit chain
   — a different, earlier mechanism — so the audit log's implied coverage of "every merge" now covers one
  of two merge paths.
- **Releases and trust are invisible.** P20 shipped a signing pipeline, five install channels, self-update
  and a platform-trust story, and rotated the signing key mid-flight after a leak. No surface shows which
  key is active, which published artefacts were signed with the retired one, or whether the post-publish
  smoke passed.
- **The axes are invisible.** Six axes resolve into `config_hash`. The console shows no adoption, no
  refusal counts and no coverage — so the platform's own backlog question (*which materializer would
  unblock the most refused nodes?*) has no surface and no data path, and the last several such decisions
  were made without one.
- **SUM-derived figures carry no link coverage.** `openspec/project.md` states the rule once: metering
  counts only what it observed, and link coverage is displayed wherever a derived figure is shown. The
  customer console honours it. The operator billing surface, which predates P11, does not — verified: no
  coverage field exists in `internal/adminops/billing.go` or the operator billing page.

And a fifth consequence that is a behaviour rather than an absence: **impersonation has become the
workaround for the missing views.** An operator who needs a tenant's axis coverage, delivery state or
refusal causes has exactly one tool that can show it — `impersonate.read`, a reason-required,
time-bounded, audited read into customer data, built for reproducing a customer's own view. It is now
answering questions a cross-tenant aggregate should answer. Every such session is a data-protection cost
with no product need behind it.

**Why a refresh alone is not the fix.** Nothing failed when the drift happened. Every one of those
fourteen phases was correct and tested; the operator console was simply nobody's acceptance criterion,
and there is no fence that notices a capability with no operator oversight. This codebase already
recorded that lesson elsewhere, in the frontend scope guard: a manual and agent horizontal scan still
missed the fifth occurrence, which proves the rule must be machine-enforced rather than remembered. So
this change leads with the fence, not the pages. **The pages are its output; the fence is its product.**

## What Changes

**The surface ledger and its fence — lands first, before any page**
- A checked-in **operator-surface ledger** mapping every capability in `openspec/specs/` to either the
  operator surface that exercises it or an explicit `no-operator-surface` decision carrying a reason and
  the deciding phase. **No third state.**
- A **build fence** fails when a capability appears in neither column, naming it. Drift becomes a build
  failure rather than a review finding.
- Asserted in **both directions**: every ledger entry naming a surface resolves to a `surfaces.ts`
  destination, and every `surfaces.ts` destination is named by at least one entry.
- A read that is wanted but not derivable from an existing store is recorded `not-yet-readable` **naming
  the collection that would make it readable** — never filled with an estimate, and never rendered as an
  empty page that looks like a working one.
- `surfaces.ts` remains the single map read by the nav, the palette and the ledger. Every new surface
  requires a capability in `adminrbac.Capabilities`; the deny-by-default matrix test keeps iterating them.

**Honesty corrections to existing surfaces — also before any new page**
- **Link coverage displayed beside every SUM-derived figure**, in the same view. A figure whose coverage
  is unknown is not rendered.
- **`unverified` authored changes provably excluded** from every aggregate improvement, savings and
  quality figure — asserted by seeding one, not by inspecting a `WHERE` clause.
- **Gainshare provenance named** where such a figure appears: it draws exclusively on the P5.5
  verified-delta ledger.
- **The audit surface states which merge paths the chain covers** (P6 autonomous merges) and which it does
  not (P12 customer-CI-mediated deliveries), instead of implying it covers all of them.

**Delivery oversight** *(new, read-only)*
- P12 delivery records per tenant and cross-tenant; the P13 change-delivery rollout stage; undeliverable
  counts with their typed causes.
- A merge is shown as **observed**, never inferred from a pull request closing, and *merged* /
  *closed-unmerged* / *state unknown* are three outcomes.
- **No control** that opens, closes, retries or merges a delivery — delivery is downstream of
  verification, and the platform holds no forge credential by default.

**Release and trust oversight** *(new, read-only)*
- Published releases per install channel with their artefacts per platform.
- The **active signing key** and every retired key with its rotation date and recorded reason, and the
  published artefacts signed with a retired one.
- Artefact verification as three states: *verified* / *failed* / *not yet verified*.
- Post-publish smoke per platform image as three states: *passed* / *failed* / **_queued until timeout_* —
  because a retired runner label queues rather than failing, and reading that as a failure sends an
  engineer to the wrong problem.
- **No key material on any surface**, ever: identifier and fingerprint only, no generation, no export.

**Axis oversight** *(new, read-only)*
- Per-axis `EXISTS / PARTIAL / ABSENT` status and fleet-wide adoption.
- **Refusal counts by stable typed cause** and by language, keeping the three causes distinguishable
  because they are answered by three different people.
- A ranking of **which artefact would close the most refusals** — a form row, a list splitter, a statement
  resolver, a registry row, a frontend field — so the backlog is ordered by evidence.
- The coverage matrix read from the **one coverage source**, never computed, cached or reformatted, with
  parity asserted in both directions.
- **An absent coverage row renders as *unknown*, naming what is missing — never as *not applicable***,
  which is a claim about the customer's code and the one thing coverage data must never say by accident.
- A coverage gap is never presented as a plan boundary.

**Identity, consent and reporting health** *(new, read-only)*
- Which factor authenticated each operator session.
- Which legal document versions each tenant has accepted and which are **owed** after a material
  publication, each linking to the archived text at its content hash.
- Each observability integration as one of the three P24 states — `absent` / `configured` / `degraded` —
  read from the platform's own readiness surface, never from a third party's dashboard.
- Per-tenant deployment shape and version where derivable; an explicit *unknown* where not.

**Explicitly not in this change**
- **No new destructive control.** The one candidate — halting an install channel after a bad release — is
  written up as a costed decision fork with a recommendation, and is not built.
- **No new table, no new collection path, no BFF-side aggregation.** The BFF stays a pass-through.
- **No re-architecture.** The shell, `surfaces.ts`, the RBAC model, the audit chain and the confirmation
  discipline are unchanged. No existing role gains a capability.
- **No axis, resolver, gate, transform, scorer or eval change**; no `config_hash` input change.

## Impact

- **Affected capabilities (new)**: `operator-surface-ledger`, `operator-delivery-oversight`,
  `operator-release-oversight`, `operator-axis-oversight`, `operator-metering-honesty`,
  `operator-oversight-health`.
- **Affected code/systems**:
  - `web/admin-console/src/lib/surfaces.ts` — four nav destinations plus palette destinations.
  - `web/admin-console/src/app/delivery`, `/releases`, `/axes`, `/oversight` — new read-only routes.
  - `web/admin-console/src/app/billing/page.tsx` — link coverage beside every derived figure; gainshare
    provenance named.
  - `web/admin-console/src/app/crosstenant/page.tsx` — `unverified` exclusion stated.
  - `web/admin-console/src/app/audit/page.tsx` — merge-path coverage stated honestly.
  - `internal/adminops/` — new read models beside the existing ones; `billing.go` gains coverage;
    delivery, release and axis read models added.
  - `internal/api/p8.go` — new read routes, each behind a granted capability.
  - `internal/adminrbac/rbac.go` — new capabilities that **partition** rather than widen (see open
    questions 3 and 4).
  - New checked-in ledger plus its build fence and both-directions assertions.
  - `docs/prd/README.md` — the new phase row.
- **Partially available upstream, handled honestly**: **P21** (the payment integration is not built —
  the webhook and dunning surface is specified and recorded `not-yet-readable`); **P22** (the SSO/MFA
  *verifier* is real, the IdP is a test-mode fixture in `internal/adminidentity/fixture.go` — this change
  surfaces the authenticating factor and claims no real IdP).
- **Dependencies**: P8 (the console being refreshed), P10, P11, P12, P13–P18 (in particular the **one
  coverage source**), P19, P20 (`internal/distribution`), P23, P24.
- **Unblocks**: the next axis or language-materializer phase (prioritised from evidence rather than
  taste); any future operator write control, which now arrives on a surface that already shows the state
  it would act on; P21 and P22, each of which gains a surface waiting for it.
- **Numbering**: this change is `p26-…`. There is no `p25-…` because `p25` already denotes P2.5 in this
  repository (`/p25/monitor`, the Gantt id in `docs/implementation-timeline/README.md`,
  `internal/api/monitor.go`), and reusing it would make the token ambiguous exactly where an operator
  greps during an incident.
