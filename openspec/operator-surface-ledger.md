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

## B. Capabilities landing in this change — `openspec/changes/p26-operator-console-refresh/specs/`

Same rule, same fence. When P26 archives, these rows move into section A unchanged.

| capability | state | detail |
|---|---|---|
| operator-axis-oversight | surface | /axes |
| operator-delivery-oversight | surface | /delivery |
| operator-metering-honesty | surface | /billing |
| operator-oversight-health | surface | /oversight |
| operator-release-oversight | surface | /releases |
| operator-surface-ledger | no-operator-surface | This capability is a checked-in ledger and a build fence. Rendering it as a page would put a second, staler copy of the fence's answer in front of an operator, and the answer that matters is the one that fails the build (P26) |

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
