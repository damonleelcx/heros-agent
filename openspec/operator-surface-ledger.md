# Operator Surface Ledger

**Scope: this ledger governs the OPERATOR console only** (`web/admin-console`). A
`no-operator-surface` row is a statement that *no operator surface* exercises the capability — it is
**not** a claim that no surface of any kind exists for it. Several capabilities below with
`no-operator-surface` have rich customer-console surfaces; that is a different question, asked by a
different console, and P26 deliberately did not answer it here (PRD open question 6).

Phase: P26. Fence: [`web/admin-console/scripts/scan-ledger.mjs`](../web/admin-console/scripts/scan-ledger.mjs),
run by `npm run scan:ledger`, by `npm run build`, and by CI.

## Why this file exists

Fourteen phases of operator-console drift happened with **nothing failing**, because the operator
console was nobody's acceptance criterion and there was no fence that noticed a capability with no
operator oversight. This file is that fence's input. Its product is not the rows — it is that a
capability with no row **fails the build**, naming itself.

## The three states, and there is no fourth

| State | Meaning | The row must carry |
|---|---|---|
| `surface` | An operator surface exercises this capability | one or more destinations, each present in `web/admin-console/src/lib/surfaces.ts` |
| `no-operator-surface` | Deliberately none | a reason **and** the deciding phase, as `(P26)` |
| `not-yet-readable` | Wanted, not derivable from an existing store | **the collection that would make it readable**, named after the word `requires` |

`not-yet-readable` is the typed-refusal shape applied to oversight. It converts a vague wish into a
specified task for a later phase, which is why this change needed no new table: where a read is not
derivable, the ledger says so and names what would make it so, instead of a table built to hold
guesses.

A row moves state when the named input lands, and the fence is what forces the move: adding a route
without its `surfaces.ts` entry fails the reverse assertion, and pointing a row at a destination that
does not exist fails the forward one.

## A. Built capabilities — `openspec/specs/`

Every directory in `openspec/specs/` has exactly one row here. A directory with no row fails the
build; a row naming no directory fails it too, so a typo cannot masquerade as coverage.

| capability | state | detail |
|---|---|---|
| authored-change | surface | /crosstenant |
| billing-webhooks | not-yet-readable | requires the P21 webhook receipt store — the recorded provider webhook events and their idempotent dispositions. P21 is specified and not built; nothing renders a zero for it |
| ci-integration | surface | /delivery |
| cli | no-operator-surface | The CLI is customer-installed and runs offline against the customer's own repository; the platform holds no state about an invocation it never observes. Its linked runs are overseen on /billing via link coverage (P26) |
| cli-reference | no-operator-surface | Reference documentation whose accuracy is enforced by the P23 documentation fence at build time; there is no runtime state for an operator to oversee (P26) |
| consent-records | surface | /oversight |
| console-marketing-site | no-operator-surface | A public marketing surface with no per-tenant state; its claims are fenced by social-proof-claims at build time rather than watched by an operator (P26) |
| context-authoring | surface | /axes |
| context-language-coverage | surface | /axes |
| context-policy | surface | /axes |
| delivery-record | surface | /delivery |
| developer-docs | no-operator-surface | Published documentation with no per-tenant state; accuracy is a build fence, not an operator watch (P26) |
| docs-accuracy-fence | no-operator-surface | The capability IS a build fence. Its state is the build's state, and a console rendering of a fence's last result would be a second, staler source of truth for it (P26) |
| forge-delivery | surface | /delivery |
| install-documentation | no-operator-surface | Documents the install channels; the channels themselves, their published artefacts and their trust state are overseen on /releases, so an operator surface here would duplicate that one (P26) |
| language-coverage | surface | /axes |
| legal-documents | surface | /oversight |
| memory-authoring | surface | /axes |
| memory-policy | surface | /axes |
| memory-store | surface | /axes |
| model-selection | surface | /registry, /axes |
| node-wiring | surface | /axes |
| operator-sso-mfa | surface | /oversight |
| payment-collection | not-yet-readable | requires the P21 payment collection records — checkout sessions, their outcomes, and the dunning attempts against a failed collection. Not derivable from the pre-PSP billing ledger, which records quantities and never an attempt to collect |
| prompt-model-authoring | surface | /axes |
| prompt-model-language-coverage | surface | /axes |
| prompt-rewrite | surface | /axes |
| reading-surface | no-operator-surface | The customer console's own reading surface; it holds no cross-tenant state, and an operator reproducing one tenant's view is what impersonation is for (P26) |
| retrieval-tuning | surface | /axes |
| run-linking | surface | /billing |
| social-proof-claims | no-operator-surface | Public claims derived from a checked-in manifest and fenced at build time; a console view of them would be a second source for a claim that already has exactly one (P26) |
| sso-identity | not-yet-readable | requires a per-tenant identity connection read model — which provider a tenant federates to and when its assertion last verified. The customer identity seam records a verified tenant id and keeps no per-tenant connection state to read |
| stripe-billing-provider | not-yet-readable | requires the P21 provider object records — the recorded subscription, price and charge handles per tenant. The operator billing read model predates any payment provider and holds provider references only where the pre-PSP ledger already carried one |
| wiring-safety | surface | /axes |

## B. Capabilities landing in this change — the unarchived changes

Same rule, same fence. When a change archives, its rows move into section A unchanged.

The governed changes are named in
[`scan-ledger.mjs`](../web/admin-console/scripts/scan-ledger.mjs)'s `GOVERNED_CHANGES`, one line per
phase. 🔴 That list replaced a single hard-coded P26 path, which meant the fence covered exactly the
change that created it: **P27's five capabilities were invisible to it** and would have stayed invisible
until P27 archived. A fence scoped to its own author is the drift it was written against, wearing a
uniform. Adding a phase there is a line of diff in a review; forgetting to is what P27 task 10.5 caught.

| capability | state | detail |
|---|---|---|
| account-registry | surface | /tenants |
| axis-node-projection | not-yet-readable | requires a FLEET projection read model — how many of each organization's reported nodes each axis applies to, side by side. The per-tenant read exists (`internal/axisprojection`, computed at request time from the coverage table and one stored structure) and is deliberately not materialised, so a fleet view today is an N+1 loop over `workflow_ir`. 🔴 And the fleet number would be the LEAST trustworthy one on any operator screen: it is an average over organizations that opted in, and every organization that did not is absent rather than zero — an operator reading "62% of customer nodes are covered" would be reading a statistic about who runs `--with-ir`, not about the product |
| hosted-workflow-catalog | no-operator-surface | Deliberately none. The catalog answers "which workflows has THIS organization reported", and an operator page over it would be a cross-tenant listing of customers' workflow identifiers and revision hashes — the same privacy expansion `user-identity` below records as a review failure rather than a modelling improvement. An operator investigating one tenant reaches its records through /tenants (P29) |
| link-coverage-visibility | surface | /billing |
| run-linking | surface | /billing |
| cli | no-operator-surface | Deliberately none. The capability governs how a command behaves in a customer's terminal — non-interactive by default, refusing rather than prompting with no TTY, naming every non-interactive way to supply a missing value. None of that produces a record an operator could read: the CLI runs on machines this platform never sees, and its contract is held by tests in this repository and by the release artifacts under `operator-release-oversight`. A page restating "commands are non-interactive" would be a second, staler copy of a fence, which is the shape `operator-surface-ledger` itself is given for the same reason (P28) |
| deployment-topology | no-operator-surface | Deliberately none. The capability is about ARTEFACTS being self-consistent — a runbook whose steps are performable in the order it states, and a workload whose outbound dependency is reachable from its own network policy (the production overlay configured SMTP on 587 while its egress allowlist opened 443 only, eighty lines apart in one file, each statement individually correct). Both are properties of checked-in documents and manifests, enforced by `scripts/deploy/check-env-parity.sh` and its neighbours. An operator page could only restate what those files say, and the answer that matters is the one that fails the build rather than the one rendered after it shipped (P28) |
| email-delivery | not-yet-readable | requires a DURABLE held-message store. The capability's own requirement is that an undelivered message "including the link a person needs" is written to an **operator-readable record** — and `/readyz` already tells the operator `mail_configured: false` with the detail *"they are held on the operator surface"*. 🔴 There is no such surface. The record is `mailer.OperatorMailer.Undelivered()`: in-process, per-replica, bounded to the most recent 200, and — verified by search — **read by nothing outside its own package**. So the readiness surface currently directs an operator to a place that does not exist. It is `not-yet-readable` rather than a missing page because the collection itself is not one a page could honestly render: under P19's `replicas: 2` two pods hold two disjoint lists and neither is the answer, and a restart empties both. What would make it readable is a durable store for held messages, keyed so one query answers for the deployment rather than for whichever replica served the request |
| password-identity | no-operator-surface | Deliberately none, and it is the second row here that must not later be "upgraded" without a decision — see `user-identity`, whose reasoning this matches and extends. An operator page over this capability would list a customer's people by verified address alongside their password state, lockout counters and reset-link history, which is strictly MORE sensitive than the join migration 0038 already forbids in writing (`admin_principal` has no tenant column and no foreign key into any customer table). The lockout state is the tempting exception because it reads as operational — it is not: it is a count of one named person's failed attempts, and the person who needs it is that person's own organization administrator, on their own console. What an operator legitimately needs is the deployment's identity POSTURE, which is already a value on `/readyz` (`identity_provider.kind`) rather than an inference (P28) |
| platform-ingress | no-operator-surface | Deliberately none, for the reason `platform-edge-reach` gives and from the same history: this capability IS a checked-in manifest and a build fence (`internal/api/ingress_fence_test.go`, now over both substrates). Rendering "which routes are published" as an operator page would put a second, staler copy in front of somebody whose only useful next action is to read and edit the manifest — and the whole history of this capability is a hand-maintained list drifting from the code it claimed to describe. A page IS a hand-maintained list with better typography (P28) |
| sso-identity | no-operator-surface | Deliberately none. This is the CUSTOMER console's tenant-identity seam — `verify(assertion) → { tenantId, userId? }` — and the seam kind is one DEPLOYMENT-WIDE setting, not a per-tenant configuration. There is therefore no collection to enumerate: `not-yet-readable` would be the wrong state, because it names a collection that would make a read possible and there is none to name. The value an operator actually needs is already reported as a value rather than an inference on `/readyz` (`identity_provider.kind`, `issuer`, `reachable`), and `health-signal-surface` is explicit that a console dashboard may not be the health judgement. The OPERATOR's own SSO and MFA are a different capability and are governed under `operator-oversight-health` (P28) |
| linked-subject-index | no-operator-surface | Deliberately none. The index answers "what does THIS organization have", scoped to the authenticated principal, and an operator page over it would be a cross-tenant listing of customers' workflow identifiers and configuration hashes — a privacy expansion nobody asked for, and the same review failure `user-identity` below records. An operator investigating one tenant reaches its records through /tenants (P29) |
| platform-edge-reach | no-operator-surface | This capability is a checked-in manifest and a build fence (`internal/api/ingress_fence_test.go` over two substrates). Rendering "which routes are published" as an operator page would put a second, staler copy in front of somebody whose next action is to read the manifest — and the answer that matters is the one that fails the build. 🔴 Its whole history is a hand-maintained list drifting from the code; a page IS a hand-maintained list with better typography (P29) |
| operator-axis-oversight | surface | /axes |
| operator-delivery-oversight | surface | /delivery |
| operator-metering-honesty | surface | /billing |
| operator-oversight-health | surface | /oversight |
| operator-release-oversight | surface | /releases |
| operator-surface-ledger | no-operator-surface | This capability is a checked-in ledger and a build fence. Rendering it as a page would put a second, staler copy of the fence's answer in front of an operator, and the answer that matters is the one that fails the build (P26) |
| run-ownership | not-yet-readable | requires a fleet ownership read model — per-tenant run counts and the pre-ownership residue side by side. Ownership is now recorded per run and `PreOwnedCount` answers the residue as ONE platform-wide integer on the customer runs endpoint; neither is a collection an operator page can list or page through, and a per-tenant breakdown has no query behind it |
| seat-accounting | not-yet-readable | requires a fleet seat read model — each tenant's current count beside its period peak. `seats.Current` answers one tenant at a time from membership and the peak lives in the metering store under a different key, so a fleet view today is an N+1 loop over two stores. **And the definition is blocking**: design.md D5 leaves *whether a credential-only member occupies a seat* undecided by Product and Sales, and until it is ratified no surface may quote a seat number — an operator page would be the first place a number nobody has agreed on gets read as authoritative |
| self-serve-subscription | not-yet-readable | requires tenant provenance — which organizations created themselves and which were seeded from configuration. The deployment POSTURE is reported (`/readyz` `account_system.self_serve_signup`, a value not an inference), but `tenant` records no origin, so "list the organizations that signed themselves up" has nothing to select. The operator controls that matter for such a tenant — plan override and suspension — already exist on /tenants under `account-registry` |
| user-identity | no-operator-surface | Deliberately none, and it is the one row here that must not later be "upgraded" to a surface without a decision. An operator page listing a customer's people by verified email is a privacy expansion nobody asked for, and it is the exact join migration 0038 forbids in writing: `admin_principal` has no tenant column and no foreign key into any customer table, and "a future migration that connects those two halves is a review failure, not a modelling improvement". The capability's own last requirement says it: a user is not an operator. Membership disputes are the customer's own administration, on their console (P27) |

## C. Operator destinations — the governing capability in `adminrbac`

The reverse direction. Every destination in `surfaces.ts` is named by a row here, and every row here
is a capability `surfaces.ts` actually gates a destination with — so a page added without its
registry entry, or a registry entry nobody can justify, is a build failure rather than a review
finding.

| capability | state | detail |
|---|---|---|
| audit.read | surface | /audit, /oversight |
| billing.correct | surface | /billing |
| billing.read | surface | /billing |
| crosstenant.read | surface | /crosstenant |
| delivery.read | surface | /delivery |
| release.read | surface | /releases |
| axis.read | surface | /axes |
| agent.read | surface | /agent, /agent/spend |
| agent.admin | surface | /agent#publish, /agent/spend#placements |
| entitlement.override | surface | /tenants |
| gdpr.execute | surface | /compliance, /compliance#erasure |
| impersonate.read | surface | /tenants |
| job.cancel | surface | /fleet#jobs |
| job.read | surface | /fleet |
| killswitch.operate | surface | /killswitch, /killswitch#global-kill-switch, /killswitch#per-tenant-kill-switch |
| registry.admin | surface | /registry, /registry#add-model |
| tenant.read | surface | /, /tenants |
| tenant.suspend | surface | /tenants |

## Not-yet-readable, collected

Two kinds of row carry this state, and the difference matters to whoever reads the ledger next.

**Waiting on a subsystem that has not shipped** — implementing the named collection is the whole of
the work:

- **billing-webhooks**, **payment-collection**, **stripe-billing-provider** — all three wait on P21.
  Where the operator console must say something about payments, it states that the subsystem has not
  shipped: it renders no count, no zero, and no empty table implying there is nothing to show.
- **sso-identity** — waits on a per-tenant identity connection read model.

**Waiting on a wave of this change** — the underlying records exist and the operator backend does not
read them yet. These rows move to `surface` as waves 26b–26e land, and the fence is what forces the
move rather than trusting anyone to remember.

And one row that is not a capability but is the same shape, recorded here because a later phase will
look for it: **per-tenant deployed version** is `not-yet-readable` — it requires a deployment
heartbeat carrying the release identifier. The oversight surface renders *unknown* rather than a
version inferred from an API contract version, a feature probe or any other proxy.
