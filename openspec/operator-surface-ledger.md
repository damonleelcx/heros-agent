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

> ### 🔴 Fifty-seven rows arrived at once, and forty-six of them are UNCONFIRMED
>
> This section held 43 rows while `openspec/specs/` held 43 capabilities — but that number was an
> accident of incomplete folding, not a statement about the platform. Archiving the 26 deployed
> phases folded their specs and took `openspec/specs/` to 100, so 57 capabilities arrived here in one
> change. **Eleven were already decided** and moved verbatim from section B (P26's six and P29's
> five, whose changes archived in the same commit) — those are as trustworthy as they were before.
>
> **The other forty-six were drafted, not decided.** They were written from evidence — the sibling
> rows' precedent, what `surfaces.ts` says each destination answers, and each capability's own spec —
> but nobody has confirmed them, and a drafted row that reads like a decided one is exactly the
> "reasoned absence and an oversight must not look the same" failure this file exists to prevent.
>
> Twenty-one carry an inline `⚠️ UNCONFIRMED` marker. The remaining twenty-five are `surface` rows
> and **cannot** carry one — the fence parses that state's detail column as a comma-separated
> destination list, so any prose in it fails the build. They are therefore named here instead:
>
> `autonomous-optimizer` · `billing` · `change-delivery` · `context-delivery` · `context-strategies` ·
> `entitlements` · `error-monitoring` · `memory-delivery` · `memory-materialization` ·
> `memory-runtime` · `metering` · `prompt-authoring` · `prompt-model-delivery` ·
> `runtime-config-binding` · `skill-binding` · `skill-tool-authoring` · `skill-tool-delivery` ·
> `skill-tool-language-coverage` · `tool-selection` · `typed-contracts` · `variable-bindings` ·
> `wiring-authoring` · `wiring-delivery` · `wiring-language-coverage` · `wiring-materialization`
>
> **A `surface` claim is the dangerous direction**: the fence checks only that the destination
> *exists*, never that the page truly exercises the capability, so a wrong one manufactures coverage
> that nothing will contradict. Each of the twenty-five was claimed only where a sibling capability
> already resolves to the same destination — the axis capabilities to `/axes`, the delivery
> capabilities to `/delivery`. Confirm them against what those pages actually render, and delete this
> block and the inline markers once that is done.

| capability | state | detail |
|---|---|---|
| admin-console | no-operator-surface | ⚠️ UNCONFIRMED — This capability IS the operator console's own layout contract — the shell never page-scrolls and a page's sections become in-page tabs. A page about the shell would be the shell describing itself, and the contract is held by the console's viewport tests rather than by anything an operator reads (P8) |
| agent-runtime-placement | surface | /agent/spend#placements, /agent/spend |
| analytics-consent | no-operator-surface | ⚠️ UNCONFIRMED — A per-visitor decision, default-denied, whose whole design is about the storage it must NOT use — so there is deliberately no server-side per-person consent collection for an operator to enumerate. Legal acceptance, which IS recorded, is `consent-records` and is already on /oversight (P24) |
| attribution | no-operator-surface | ⚠️ UNCONFIRMED — Read-only per-run reports — contribution decomposition, first divergence, failure clusters — scoped to one workflow of one tenant and rendered on that tenant's own console. An operator page would be a cross-tenant listing of customers' failing nodes, the privacy expansion `linked-subject-index` records as a review failure (P4.5) |
| authored-change | surface | /crosstenant |
| autonomous-optimizer | surface | /fleet |
| axis-node-projection | not-yet-readable | requires a FLEET projection read model — how many of each organization's reported nodes each axis applies to, side by side. The per-tenant read exists (`internal/axisprojection`, computed at request time from the coverage table and one stored structure) and is deliberately not materialised, so a fleet view today is an N+1 loop over `workflow_ir`. 🔴 And the fleet number would be the LEAST trustworthy one on any operator screen: it is an average over organizations that opted in, and every organization that did not is absent rather than zero — an operator reading "62% of customer nodes are covered" would be reading a statistic about who runs `--with-ir`, not about the product |
| billing | surface | /billing |
| billing-webhooks | not-yet-readable | requires the P21 webhook receipt store — the recorded provider webhook events and their idempotent dispositions. P21 is specified and not built; nothing renders a zero for it |
| chain-inference | surface | /agent, /agent/spend |
| change-delivery | surface | /delivery |
| ci-integration | surface | /delivery |
| cli | no-operator-surface | The CLI is customer-installed and runs offline against the customer's own repository; the platform holds no state about an invocation it never observes. Its linked runs are overseen on /billing via link coverage (P26) |
| cli-reference | no-operator-surface | Reference documentation whose accuracy is enforced by the P23 documentation fence at build time; there is no runtime state for an operator to oversee (P26) |
| consent-records | surface | /oversight |
| console-marketing-site | no-operator-surface | A public marketing surface with no per-tenant state; its claims are fenced by social-proof-claims at build time rather than watched by an operator (P26) |
| context-authoring | surface | /axes |
| context-delivery | surface | /delivery |
| context-language-coverage | surface | /axes |
| context-policy | surface | /axes |
| context-strategies | surface | /axes |
| delivery-record | surface | /delivery |
| developer-docs | no-operator-surface | Published documentation with no per-tenant state; accuracy is a build fence, not an operator watch (P26) |
| diagnosis | no-operator-surface | ⚠️ UNCONFIRMED — Per-run, per-cluster explanations bound to one tenant's failing cases, with the evidence attached. The answer belongs to whoever owns the workflow, on their own console; what an operator legitimately needs is whether analysis is running and what it costs, which is /agent (P4.5) |
| docs-accuracy-fence | no-operator-surface | The capability IS a build fence. Its state is the build's state, and a console rendering of a fence's last result would be a second, staler source of truth for it (P26) |
| dynamic-tracing | no-operator-surface | ⚠️ UNCONFIRMED — Confirms ONE tenant's static candidate graph against a real run instrumented on the customer's own machine. The traces are the customer's and the reconciliation is per workflow; the operator's version of the question is how much analysis has run, which /agent answers (P5) |
| entitlements | surface | /tenants |
| error-monitoring | surface | /oversight |
| eval-set-visibility | no-operator-surface | The cases never leave the customer's machine. The platform holds `counts_only` and the capability exists to stop a denominator being reported as if it were a set — so an operator page could render only the number the capability refuses to leave unexplained, and the explanation is on the customer's own eval-set route. What an operator legitimately needs is whether analysis is running and what it costs, which is /agent (P30) |
| forge-delivery | surface | /delivery |
| graph-composition-summary | no-operator-surface | A per-workflow reading of ONE tenant's graph — which patterns are present, what they cover, what is unlabelled — rendered on the customer console's graph view. An operator reproducing one tenant's view is what impersonation is for, which is the reason `reading-surface` already carries (P30) |
| heros-agent-definition | surface | /agent, /agent#publish |
| hosted-workflow-catalog | no-operator-surface | Deliberately none. The catalog answers "which workflows has THIS organization reported", and an operator page over it would be a cross-tenant listing of customers' workflow identifiers and revision hashes — the same privacy expansion `user-identity` below records as a review failure rather than a modelling improvement. An operator investigating one tenant reaches its records through /tenants (P29) |
| inference-provenance | no-operator-surface | Provenance sits on the FACT, and the question it answers — who authored this edge — is asked of one tenant's stored graph on the surface that draws it. The operator's fleet-level version of the question is how much analysis has run and where, which /agent answers as a stored-inference count (`unknown`, never zero, when no store is wired) and /agent/spend answers per tenant. A cross-tenant listing of customers' edges would be the privacy expansion `linked-subject-index` records as a review failure rather than a modelling improvement (P30) |
| install-documentation | no-operator-surface | Documents the install channels; the channels themselves, their published artefacts and their trust state are overseen on /releases, so an operator surface here would duplicate that one (P26) |
| language-coverage | surface | /axes |
| legal-documents | surface | /oversight |
| link-coverage-visibility | surface | /billing |
| linked-subject-index | no-operator-surface | Deliberately none. The index answers "what does THIS organization have", scoped to the authenticated principal, and an operator page over it would be a cross-tenant listing of customers' workflow identifiers and configuration hashes — a privacy expansion nobody asked for, and the same review failure `user-identity` below records. An operator investigating one tenant reaches its records through /tenants (P29) |
| memory-authoring | surface | /axes |
| memory-delivery | surface | /delivery |
| memory-materialization | surface | /axes |
| memory-policy | surface | /axes |
| memory-runtime | surface | /axes |
| memory-store | surface | /axes |
| metering | surface | /billing |
| metric-event-schema | no-operator-surface | ⚠️ UNCONFIRMED — A write-time contract — the seven-tag set every event must carry. Its state is the build's state, and a console rendering of a schema's last validation would be a second, staler source of truth for it, which is the reason `docs-accuracy-fence` already carries (P0) |
| metrics-observability | no-operator-surface | ⚠️ UNCONFIRMED — The OpenTelemetry substrate every other surface is measured against. Operators read the measurements on the surfaces that render them, and the one question about the substrate itself — whether reporting is working — is `operator-oversight-health`, already recorded above as /oversight (P2.5) |
| model-selection | surface | /registry, /axes |
| node-wiring | surface | /axes |
| operator-agent-authoring | surface | /agent#publish, /agent |
| operator-agent-control | surface | /agent, /agent/spend, /agent/spend#placements |
| operator-axis-oversight | surface | /axes |
| operator-delivery-oversight | surface | /delivery |
| operator-metering-honesty | surface | /billing |
| operator-oversight-health | surface | /oversight |
| operator-release-oversight | surface | /releases |
| operator-sso-mfa | surface | /oversight |
| operator-surface-ledger | no-operator-surface | This capability is a checked-in ledger and a build fence. Rendering it as a page would put a second, staler copy of the fence's answer in front of an operator, and the answer that matters is the one that fails the build (P26) |
| pattern-classifier | no-operator-surface | ⚠️ UNCONFIRMED — Labels one tenant's graph with agentic patterns. The fleet reading of the same data — which patterns are present and what is unlabelled — is `graph-composition-summary`, already recorded above with the same conclusion (P3.5) |
| payment-collection | not-yet-readable | requires the P21 payment collection records — checkout sessions, their outcomes, and the dunning attempts against a failed collection. Not derivable from the pre-PSP billing ledger, which records quantities and never an attempt to collect |
| platform-edge-reach | no-operator-surface | This capability is a checked-in manifest and a build fence (`internal/api/ingress_fence_test.go` over two substrates). Rendering "which routes are published" as an operator page would put a second, staler copy in front of somebody whose next action is to read the manifest — and the answer that matters is the one that fails the build. 🔴 Its whole history is a hand-maintained list drifting from the code; a page IS a hand-maintained list with better typography (P29) |
| product-analytics | no-operator-surface | ⚠️ UNCONFIRMED — Interface-usage measurement whose events land in the analytics provider's own console. Rendering them again here would be a second, staler copy of a product somebody else already ships, and the boundaries that keep it from becoming something else are build-time construction rules (P24) |
| prompt-authoring | surface | /axes |
| prompt-model-authoring | surface | /axes |
| prompt-model-delivery | surface | /delivery |
| prompt-model-language-coverage | surface | /axes |
| prompt-rewrite | surface | /axes |
| prompt-studio | no-operator-surface | ⚠️ UNCONFIRMED — A customer-console authoring surface where one organization's prompt is rendered, tried and compared. Its results are labelled exploratory and carry no score or rank by construction, so there is no fleet quantity to aggregate; an operator reaching one tenant's studio is impersonation, which `reading-surface` already records (P10) |
| proposal-engine | no-operator-surface | ⚠️ UNCONFIRMED — Per-workflow proposal emission, gated by pattern and contract. The fleet-level question — why a workflow produced no proposals — is `proposal-generation-reach`, which is already recorded above and reaches the same conclusion (P5.5) |
| proposal-generation-reach | no-operator-surface | The generator's state is per workflow — `no_linked_runs`, `no_per_node_metrics`, `no_discovered_graph`, `no_model_menu`, `no_bottleneck` — and each answer's next action belongs to the organization that owns the workflow, on its own proposals surface. An operator page would list customers' workflow identifiers to report a state only they can act on (P30) |
| reading-surface | no-operator-surface | The customer console's own reading surface; it holds no cross-tenant state, and an operator reproducing one tenant's view is what impersonation is for (P26) |
| rearrangement | no-operator-surface | ⚠️ UNCONFIRMED — The customer's interactive graph editor, and the riskiest interaction in the product. Its safety is the unhappy path being refused at edit time under the typed-contract gate, which is a property of the editing session rather than a record an operator could review afterwards (P5) |
| retrieval-tuning | surface | /axes |
| run-linking | surface | /billing |
| runtime-config-binding | surface | /axes |
| sandbox | not-yet-readable | ⚠️ UNCONFIRMED — requires a sandbox-enforcement read model — per-execution isolation mode, and the denied egress and contract-mismatch refusals aggregated per deployment. This is the project's sharpest security boundary and it is enforced per execution and asserted by the isolation proofs in CI; nothing records an enforcement EVENT an operator could page through, so today the honest answer is the build's, not a page's |
| session-replay | no-operator-surface | ⚠️ UNCONFIRMED — Replays live in the provider's console and the capability restricts them to one surface by design. A rendering here would duplicate that console while widening the set of people who can watch a customer use the product (P24) |
| skill-binding | surface | /axes |
| skill-registry | no-operator-surface | ⚠️ UNCONFIRMED — Entries are contracts on tool code that lives in the CUSTOMER's repository and is validated before execution, failing closed on mismatch. The platform holds no cross-tenant skill catalog to page through, and the enforcement that matters happens at execution rather than in a list (P3) |
| skill-tool-authoring | surface | /axes |
| skill-tool-delivery | surface | /delivery |
| skill-tool-language-coverage | surface | /axes |
| social-proof-claims | no-operator-surface | Public claims derived from a checked-in manifest and fenced at build time; a console view of them would be a second source for a claim that already has exactly one (P26) |
| sso-identity | not-yet-readable | requires a per-tenant identity connection read model — which provider a tenant federates to and when its assertion last verified. The customer identity seam records a verified tenant id and keeps no per-tenant connection state to read |
| storage-and-lineage | no-operator-surface | ⚠️ UNCONFIRMED — The determinism contract behind `config_hash` and the lineage every stored artefact inherits. It is asserted where things are written, not observed afterwards; an operator page could only restate the invariant the writes already enforce (P0) |
| stripe-billing-provider | not-yet-readable | requires the P21 provider object records — the recorded subscription, price and charge handles per tenant. The operator billing read model predates any payment provider and holds provider references only where the pre-PSP ledger already carried one |
| telemetry-deployment-posture | no-operator-surface | ⚠️ UNCONFIRMED — Answers *does this thing phone home?* — a deployment-wide posture, not a per-tenant collection. It is already reported as a VALUE rather than an inference on `/readyz`, which is the shape `sso-identity` and `password-identity` were both given for the same reason (P24) |
| third-party-origin-fence | no-operator-surface | ⚠️ UNCONFIRMED — The capability IS a build fence: it holds every tenant prefix at `default-src 'self'` and fails the build when a policy names a third-party origin. Its state is the build's state, for the reason `docs-accuracy-fence` already carries (P24) |
| tool-selection | surface | /axes |
| typed-contracts | surface | /axes |
| variable-bindings | surface | /axes |
| verification | not-yet-readable | ⚠️ UNCONFIRMED — requires a fleet verification read model — gate outcomes per tenant beside whether each was measured on a held-out split. The verdict exists per proposal and carries its own not-held-out flag, but nothing aggregates it, and a pass rate that silently mixed held-out with generating-case results would be the exact overfit the gate exists to refuse, reported as a fleet statistic |
| web-console | no-operator-surface | ⚠️ UNCONFIRMED — The CUSTOMER console's layout contract, for the reason `reading-surface` already carries: it holds no cross-tenant state, and an operator reproducing one tenant's view is what impersonation is for (P9) |
| wiring-authoring | surface | /axes |
| wiring-delivery | surface | /delivery |
| wiring-language-coverage | surface | /axes |
| wiring-materialization | surface | /axes |
| wiring-safety | surface | /axes |
| workflow-ir | no-operator-surface | ⚠️ UNCONFIRMED — The IR is one tenant's graph. Both fleet-level readings of it are already recorded and already decided: `graph-composition-summary` for what a graph contains and `axis-node-projection` for how much of it each axis covers (P0) |

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
| cli | no-operator-surface | Deliberately none. The capability governs how a command behaves in a customer's terminal — non-interactive by default, refusing rather than prompting with no TTY, naming every non-interactive way to supply a missing value. None of that produces a record an operator could read: the CLI runs on machines this platform never sees, and its contract is held by tests in this repository and by the release artifacts under `operator-release-oversight`. A page restating "commands are non-interactive" would be a second, staler copy of a fence, which is the shape `operator-surface-ledger` itself is given for the same reason (P28) |
| deployment-topology | no-operator-surface | Deliberately none. The capability is about ARTEFACTS being self-consistent — a runbook whose steps are performable in the order it states, and a workload whose outbound dependency is reachable from its own network policy (the production overlay configured SMTP on 587 while its egress allowlist opened 443 only, eighty lines apart in one file, each statement individually correct). Both are properties of checked-in documents and manifests, enforced by `scripts/deploy/check-env-parity.sh` and its neighbours. An operator page could only restate what those files say, and the answer that matters is the one that fails the build rather than the one rendered after it shipped (P28) |
| email-delivery | not-yet-readable | requires a DURABLE held-message store. The capability's own requirement is that an undelivered message "including the link a person needs" is written to an **operator-readable record** — and `/readyz` already tells the operator `mail_configured: false` with the detail *"they are held on the operator surface"*. 🔴 There is no such surface. The record is `mailer.OperatorMailer.Undelivered()`: in-process, per-replica, bounded to the most recent 200, and — verified by search — **read by nothing outside its own package**. So the readiness surface currently directs an operator to a place that does not exist. It is `not-yet-readable` rather than a missing page because the collection itself is not one a page could honestly render: under P19's `replicas: 2` two pods hold two disjoint lists and neither is the answer, and a restart empties both. What would make it readable is a durable store for held messages, keyed so one query answers for the deployment rather than for whichever replica served the request |
| password-identity | no-operator-surface | Deliberately none, and it is the second row here that must not later be "upgraded" without a decision — see `user-identity`, whose reasoning this matches and extends. An operator page over this capability would list a customer's people by verified address alongside their password state, lockout counters and reset-link history, which is strictly MORE sensitive than the join migration 0038 already forbids in writing (`admin_principal` has no tenant column and no foreign key into any customer table). The lockout state is the tempting exception because it reads as operational — it is not: it is a count of one named person's failed attempts, and the person who needs it is that person's own organization administrator, on their own console. What an operator legitimately needs is the deployment's identity POSTURE, which is already a value on `/readyz` (`identity_provider.kind`) rather than an inference (P28) |
| platform-ingress | no-operator-surface | Deliberately none, for the reason `platform-edge-reach` gives and from the same history: this capability IS a checked-in manifest and a build fence (`internal/api/ingress_fence_test.go`, now over both substrates). Rendering "which routes are published" as an operator page would put a second, staler copy in front of somebody whose only useful next action is to read and edit the manifest — and the whole history of this capability is a hand-maintained list drifting from the code it claimed to describe. A page IS a hand-maintained list with better typography (P28) |
| sso-identity | no-operator-surface | Deliberately none. This is the CUSTOMER console's tenant-identity seam — `verify(assertion) → { tenantId, userId? }` — and the seam kind is one DEPLOYMENT-WIDE setting, not a per-tenant configuration. There is therefore no collection to enumerate: `not-yet-readable` would be the wrong state, because it names a collection that would make a read possible and there is none to name. The value an operator actually needs is already reported as a value rather than an inference on `/readyz` (`identity_provider.kind`, `issuer`, `reachable`), and `health-signal-surface` is explicit that a console dashboard may not be the health judgement. The OPERATOR's own SSO and MFA are a different capability and are governed under `operator-oversight-health` (P28) |
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
