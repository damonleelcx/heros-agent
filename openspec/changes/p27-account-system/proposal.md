## Why

The platform can prove **who is calling** and it can **price what they used**. It cannot **remember that they
exist**.

A tenant is not a row anywhere in the thirty-seven Postgres migrations. It is a key in a map that
[`auth.Registry`](../../../internal/auth/registry.go) builds from the configuration file at boot, plus a second
map the console reads from its environment
([`identity.ts`](../../../web/console/src/lib/identity.ts)). Grep the schema for a `tenant` table and every hit
is a `tenant_id` **column** on somebody else's table — `delivery`, `workflow_ir`, `legal_acceptance` — each a
foreign key into nothing. Creating a customer therefore means editing a file and restarting a process. There is
no sign-up, and there cannot be one, because there is nowhere to write the answer.

Five consequences follow, and each is checkable rather than argued.

**There is no user.** [`session.ts`](../../../web/console/src/lib/session.ts) says so and says why: the session
holds a tenant and not a user *"because the platform cannot currently prove one"*.
[P22](../../../docs/prd/P22-sso-identity.md) repeated the deferral in its non-goals — and then **shipped**, so a
verified assertion now yields `sub@issuer`, which is exactly the proof whose absence was the reason. A deferral
whose precondition has since been met is not a decision; it is an unexamined default. What it still costs: a
revoked session cannot say **whose** it was, an audit entry cannot attribute an action to a person, an
invitation has nowhere to land, and the `seats` limit has nothing to count.

**Nothing creates an account.** `account.Store.Create` is called by four demo/proof binaries and by test
fixtures, and by **zero** lines of non-test code under `internal/`. Meanwhile [`billing/collection.go`](../../../internal/billing/collection.go)
opens `StartCheckout`, `ChangePlan` and the payment read model with `s.accounts.Get(customerID)`, and
[`entitlement`](../../../internal/entitlement/entitlement.go) denies with `ReasonNoAccount` when that misses. The
P21 collection path is complete, correct, and reachable only for a customer somebody inserted by hand.

**A run has no owner.** Migration `0005_p2_run.up.sql` keys `run` by `run_id` with no tenant column, and
`internal/api/configruntime.go:62` registers `GET /api/v1/runs/{run_id}` with no collection route beside it.
"What did I run last week?" is not a question the API can be asked.

**And the command line has the same hole, one layer up.** `heros login --token <string>` stores a **tenant**
credential ([`credential.go`](../../../internal/cli/credential.go)) and validates it at `/api/v1/whoami`, which
answers `{"identity": principal.TenantID}` — the organization, never the person. A developer's first experience
of the product is *"paste the string an admin sent you"*, `heros status` cannot say who they are, and — the one
that matters — **removing a member does not revoke their CLI access**. Offboarding is currently: rotate the
organization's key and hand it out again to everybody who stayed.

**And the isolation the console defers to does not exist.**
[`scope.ts`](../../../web/console/src/lib/scope.ts) states — carefully, declining to claim more than it can —
that the platform enforces tenant isolation "against the credential and the `X-Console-Tenant` header the
forwarder attaches". The platform never reads that header: `grep -rn X-Console-Tenant --include=*.go` returns
one hit, a comment in a proof binary. And every console request carries the **same** credential —
`platformFetch` sets `X-API-Key: platformCredential()` from one process-wide environment variable. On a
multi-tenant deployment, every signed-in tenant therefore resolves to one platform principal. The console keeps
its half of the contract exactly; the other half was never built.

Two more facts complete the picture. The `seats` limit is **sold, gated, and enforced against a permanent
zero** — `plancfg.LimitSeats` and `metering.MetricSeats` exist, `entitlement.go:109` gates the dashboard on
them, the plan fixtures price 1 / 5 / 25 / 500 seats, and no code path anywhere writes a `seats` usage record.
And console sessions live in a process-local map, so a rollout signs every customer out and the `replicas: 2`
declared in P19's Kubernetes overlay cannot work — a user signs in against one pod and is signed out by the
next request that lands on the other.

**P27 is the phase that makes the tenant a row, the person real, and the run owned** — so that a customer can
sign up, find their work tomorrow, and pay for the plan that governs it, with no operator in the path.

## What Changes

- **The tenant becomes a durable record; configuration becomes an expand-only seed.** A `tenant` table is the
  system of record for existence, name, status and plan binding. `cfg.TenantCredentials` is applied once at
  boot as **create-if-absent** — never overwriting, never deleting — so every deployment that boots today boots
  tomorrow with the same tenants and the same working keys, while a tenant created at runtime survives the next
  restart. API credentials become durable rows storing a **hash**, with a `revoked_at` honoured at the next
  request; the plaintext is shown once at creation and is never readable again.
- **A person becomes a first-class record.** `user` (keyed by an internal id, uniquely identified by the
  verified `(issuer, subject)` pair — `email` is a display attribute, never the identity, because an address
  can be reassigned and a subject cannot), `membership` (`owner` / `admin` / `member`, one person may belong to
  several organizations) and `invitation`. The console session becomes
  `{id, tenantId, userId?, issuedAt, expiresAt, revokedAt?}` — `userId` present for an interactive sign-in and
  **absent**, never a placeholder, for a machine principal. Removing a membership revokes that person's
  sessions **and** their user-scoped credentials at the next request; tenant-scoped machine credentials are
  untouched and are **listed on the removal surface**, because an offboarding screen that hides what it did not
  revoke is worse than none.
- **The console session store becomes durable and shared**, using the shape the operator side's `admin_session`
  has had since P8. TTL, revocation-with-no-grace, cookie flags, the fail-closed middleware and `scope.ts`'s
  no-tenant-parameter rule are **unchanged** and asserted by regression test — ADR-008 Rule 3 holds.
- **Work gets an owner, and a tenant can list its own.** New runs, variant specs and eval runs record the
  owning tenant at write time — **not** proposals, which migration 0025 already made `tenant_id NOT NULL` when
  P5.5's console work landed, and which therefore have no pre-ownership state at all. (An earlier draft added
  the column to `proposal` too; the schema fence caught it, which is the argument for the fence.) `GET /api/v1/runs` returns this tenant's runs, paged, accepting no tenant
  parameter in any position. A subject belonging to another tenant answers `404`, identical to one that does not
  exist. Rows written before P27 carry `NULL`, meaning **pre-ownership**, and every listing surface distinguishes
  that from "you have no runs" — a phase that silently redefines a customer's history as starting at the upgrade
  reads as data loss. Ownership is **immutable**: there is no transfer interface, because a transfer moves
  billed usage between customers.
- **🔴 Scope comes from the credential, and `X-Console-Tenant` is deleted rather than made to work.** Two fixes
  were available. Trusting the header when presented with the BFF's credential is far cheaper and means any
  holder of that one credential can name any tenant — a request describing its own authority, which is the
  thing ADR-008 Rule 2 exists to forbid. **Rejected at level 1**, and level 8 may not push back. Instead the
  BFF exchanges its session for a **short-lived, tenant-scoped platform token** and forwards that, so the tenant
  is inside the thing the platform verifies and `auth` derives scope exactly as it always has. The header is
  removed **and fenced**, because an inert header is a header somebody later makes load-bearing. The browser
  still holds no credential of any kind.
- **Seats become two quantities with two names.** `seats_current` is a **state** — the count of active
  console-capable memberships, read directly from membership — and it gates the next invitation, refusing with
  the plan's number and the current number **both named**. `seats_billed` is the **period peak**, written as a
  usage observation on every membership change, and it is what an invoice line may cite. Collapsing them is
  precisely why today's limit is decorative: a state accumulated as a flow is a number nobody ever writes. No
  surface may render an unlabelled "seats" figure. Operator quota overrides are unchanged.
- **Sign-up creates everything the paid path needs, atomically.** A verified identity mapping to no tenant, on a
  deployment that permits self-serve, creates `{tenant, user, membership(owner), account(Free)}` in **one
  transaction** — a partial failure leaves none of them, so there is no ownerless tenant and therefore no
  cleanup job to forget. `account.provider_customer_handle` becomes **nullable**, meaning *no billing-provider
  customer yet*, under a database `CHECK` binding the two: a handle may be absent only while the plan charges
  nothing. The existing card-data `CHECK` is unchanged for every non-null handle. The first *Upgrade* mints the
  provider customer, persists the handle and proceeds — idempotently, so a retry does not create a second
  customer. **Self-serve is off by default** and is a declared deployment posture reported on the readiness
  surface: an air-gapped or single-customer install does not gain a registration form by upgrading.
- **The command line joins the account system.** `heros login` with no token performs a **device
  authorization**: the CLI prints a short code, the person approves it in the console behind their existing
  SSO sign-in, picks which organization, and the CLI receives a **user-scoped `api_credential`** — the same row
  type the members page lists and the same row member removal revokes. The CLI never touches a password, an
  assertion or an ID token; it receives only the credential the platform issued. `--token` stays supported and
  stays the **machine** path, because a CI key with no `user_id` is exactly the distinction that lets removal
  revoke a person without breaking a build. `whoami` gains the organization name, the acting user (absent for a
  machine credential) and the credential's kind — **additively**: `identity` keeps its name, meaning and value,
  and both current callers read only that field. Device codes are short-lived, single-use, and approvable only
  by someone holding an active membership in the organization they select.
- **Account closure is honest about what it is.** Closing suspends the tenant and stops accrual; it does **not**
  erase history, and the surface names the existing `gdpr_request` mechanism rather than implying deletion has
  occurred.
- **Non-goals (not built):** no password store or credential recovery; no SCIM or push-based deactivation (the
  IdP-side window remains the published session TTL, unchanged from P22 G11); **no repricing, no new plan, no
  new billing dimension** — P27 makes `seats` measurable and does not change what one costs; three roles and no
  permission system; no cross-tenant sharing, public run links or run transfer; no hard erasure path; **no
  backfill of ownership onto historical runs**, because the information was never written and a guessed owner is
  a confidently wrong one; operator identity stays categorically separate — an operator is not a user and never
  gains a membership.

## Impact

- **Affected capabilities:** `account-registry` (**new** — the durable tenant, hashed credentials, the
  expand-only seed); `user-identity` (**new** — user, membership, invitation, durable sessions naming a person,
  per-user revocation and audit attribution); `run-ownership` (**new** — owner at write time, tenant-scoped
  listing, credential-derived scope, cross-tenant `404`); `seat-accounting` (**new** — the two seat quantities
  and their enforcement); `self-serve-subscription` (**new** — atomic sign-up, the nullable handle and its
  invariant, first-upgrade handle minting, self-serve as declared posture). All five are delta specs under this
  change.
- **Customer-facing wording:** `docs/sales/P27-account-copy.md` — what becomes sayable ("organizations and
  members", "remove a member and their access ends at their next request", "seats included in your plan"), the
  boundary each of those sentences must be spoken with, and the claims P27 does **not** license.
- **Affected code/systems:** `internal/auth` (`Registry` reads a durable store; `Principal` gains an optional
  `UserID`); a new account/tenant/membership store package and its Postgres implementation;
  `internal/account` (nullable handle + the paid-plan invariant); `internal/billing/collection.go` (mint the
  handle on first checkout, idempotently); `internal/entitlement` (seats read from membership, not from the
  usage store); `internal/metering` (a `seats` observation written on membership change); `internal/api`
  (`GET /api/v1/runs`, member/invitation surfaces, the token-exchange endpoint, the device-authorization
  endpoints, an additively-extended `/api/v1/whoami`, `/readyz` posture reporting); `internal/cli`
  (`login` without `--token`, `status` naming the person and the organization) and
  `internal/runlink/transport`;
  the P2 run write path and P4/P5 write paths (owner column); `db/migrations/postgres/**` plus the SQLite
  baseline, **both dialects executed**; `web/console` (`/app/runs`, `/app/settings/members`, invitation
  acceptance, sign-up; the durable session store; **`X-Console-Tenant` removed from `platformApi.ts`** and
  fenced); `web/admin-console` (tenant and member read surfaces registered in the P26 operator-surface ledger);
  P19 deployment manifests and the air-gapped package assertion for the self-serve posture.
- **Dependencies:** **upstream** — [P22](../../../docs/prd/P22-sso-identity.md) (the verified `(issuer,
  subject)` that makes a user provable, and `resolveTenant`'s refusal taxonomy, which this extends rather than
  replaces), [ADR-008](../../../docs/adr/ADR-008-console-tenant-identity-seam.md) (Rule 2 strengthened, Rule 3
  held), [P7](../../../docs/prd/P7-billing-metering.md) (`account`, `plancfg`, `entitlement`, `metering`
  consumed verbatim), [P21](../../../docs/prd/P21-stripe-payments.md) (collection, made reachable),
  [P8](../../../docs/prd/P8-admin-console.md) (`admin_session` as the proven durable-session shape; the audit
  chain attributed into), [P19](../../../docs/prd/P19-deployment-delivery.md) (readiness surface and capability
  ledger), [ADR-004](../../../docs/adr/ADR-004-runtime-config-binding.md) (fail-static binding, which the seed
  follows). **Unblocks** — self-serve revenue; any honest seat-based commercial claim; per-user audit
  attribution, a hole in P8's audit chain since it was built; SCIM, which needs a `user` and a `membership` to
  synchronise into; and P19's `replicas: 2` becoming a supported topology instead of an intermittent bug.
  **Not depended on** — a password store, guaranteed email delivery for invitations (an invitation is
  retrievable in the console by the inviter; email is a convenience), or any change to the transformed
  program's identity ([ADR-002](../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md)).
