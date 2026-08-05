# Tasks — P27: Account System

Ordered by workstream. The phase makes the tenant a durable row, the person a first-class record, and the run
owned — and changes **nothing above the ADR-008 seam**. Each task is independently verifiable.

Every PR carries a **deployment-shape impact matrix** (hosted / self-hosted / air-gapped) with every "not
affected" row explaining *why* — the seed, the session store and the self-serve posture each behave
differently across the three, and a row that cannot explain itself has not been thought about.

Nothing below is done.

## 1. System Designer — ratify the one-way doors before any table exists

- [x] 1.1 Ratify **D1** (tenant / user / membership as three tables, not one `principal` with a discriminator),
      **D2** (scope travels inside the credential; `X-Console-Tenant` deleted and fenced) and **D3**
      (`provider_customer_handle` may be absent under a plan-cost `CHECK` — **amended by 10.1**: absence is
      the empty string, not NULL) in `design.md`, each with the alternative
      that was rejected and the level of the priority law that decided it. These are one-way doors: a published
      credential contract, a schema shape every later table joins to, and a database invariant two services can
      violate.
- [x] 1.2 Ratify **D4** (configuration is a seed; the database is the truth) and **D6** (pre-P27 runs carry
      NULL and are never guessed at), with the failure each avoids stated as a scenario rather than a
      principle.
- [x] 1.3 Ratify **D5** (`seats_current` state vs `seats_billed` period peak) and record the **undecided**
      boundary — whether a CLI-only member is a seat — as a blocking question for any surface or conversation
      that quotes a seat number. It is decided by Product + Sales, not here, and it is not decided by shipping.
- [x] 1.4 Write the noun boundary: **tenant** (an organization we bill), **account** (a billable customer),
      **user** (a person), **principal** (an authenticated caller), **member** (a person in an organization),
      **seat** (a paid-for membership slot). Four of these are already used loosely in the codebase; the
      document states which word may appear where, and the review rule is that none of them substitutes for
      another.
- [x] 1.5 Confirm the **control-plane / data-plane split**: membership, seats and account state are
      control-plane and must not enter the run write path, which reads a resolved principal and nothing else.
      State it as the constraint on §5.2's design rather than as an aspiration.

> **Section 1 evidence.** All six doors ratified in [`design.md`](design.md) § *Ratification record* with the
> rejected alternative and the deciding priority level for each; D5 carries the **blocking** seat-definition
> question. The noun boundary is `design.md` § *Noun boundary*, a seven-row three-layer table (interface /
> entity / code) with the two rules that get violated stated explicitly. The control/data-plane constraint is
> `design.md` § *Control plane / data plane*, stated as what the run write path may read (one field off the
> resolved principal) and what it may not (membership, seats, account, plan). One correction was made rather
> than left standing: PRD NFR6's SQLite/Postgres dual-dialect axis **does not exist in this repository**, and
> is restated as `MemStore` vs `PGStore` parity — see `design.md` § *A correction to the PRD*.

## 2. Backend — schema, inert first

- [x] 2.1 Migration: `tenant`, `user`, `membership`, `invitation`, `api_credential`, `console_session`. Same
      DDL landing in the Postgres chain and the store's Go model **in one commit**, with the commit body
      declaring all three landing points (migration + Go model + real-schema proof) the `schema-code-coherence`
      rule requires.
- [x] 2.2 Migration: `tenant_id TEXT NULL` on `run`, `variant_spec` and `eval_run` — **not** `proposal`, which
      0025 already made `tenant_id NOT NULL` — each with a **partial** index on that table's own ordering
      column. Nullable-first, no default rewrite. (`CREATE INDEX CONCURRENTLY` is unavailable to this runner;
      see the evidence note below for why the partial index is the better answer rather than the fallback.)
- [x] 2.3 Migration: `account.provider_customer_handle` becomes optional; add the `CHECK` binding an absent
      handle to a plan that charges nothing; leave the card-data `CHECK` untouched and prove it still refuses a
      Luhn-valid 12–19 digit run. **Amended by 10.1**: absence is the EMPTY STRING and the column keeps its
      `NOT NULL` — 0013's `<> ''` check is what 0038 drops — because the prior image scans this column into a
      Go `string` and a NULL made every `account.List()` fail after a rollback.
- [x] 2.4 Every migration is **idempotent** (a second run succeeds and changes no row) and none wraps the whole
      script in a transaction. Prove idempotency by table checksum before and after a second apply.
- [x] 2.5 Run the whole new schema against a **live Postgres** through the embedded migration set, exactly as a
      booting deployment does — not authored, *executed*. An unrun migration is cover for the failures already
      in it. (The SQLite/Postgres axis the PRD originally named does not exist in this repository; see
      `design.md` § *A correction to the PRD*. The axis that does exist is `MemStore` vs `PGStore`, covered by
      3.1 and 11.6.)

> **Section 2 evidence.** `db/migrations/postgres/0038_p27_account_system.up.sql` (+ `.down.sql`), proven by
> `internal/pgmigrate/p27_account_system_pgproof_test.go` against a real Postgres via
> `bash db/migrations/postgres/run_pg_docker.sh go test -tags pgproof ./internal/pgmigrate/` — **green**.
>
> Two things the fence found that reading had not:
>
> 1. **`proposal` already had `tenant_id NOT NULL`** from migration 0025. The first draft added it again — a
>    no-op that read as work, with a duplicate index beside it. `proposal` is now excluded and the reason is in
>    the migration header. The same is true of `delivery`, `workflow_ir`, `legal_acceptance`, `run_link`,
>    `authored_change`, `source_bundle` and `platform_workflow_graph`; what was missing was never
>    tenant-awareness on every table, it was the tenant ROW those columns name.
> 2. ⚠️ **`account.NewHandle`'s doc comment overclaims.** It says "a legitimate all-digit provider id is not
>    rejected by accident" because Go requires digits AND a Luhn checksum — but 0013's CHECK has no Luhn step
>    and refuses every 12–19 digit run. The database is stricter and the database is the last line. 0013 is
>    **not** narrowed (tightening a CHECK on a deployed table is a one-way door with no P27 reason), and the
>    proof pins the stricter, actual behaviour so nobody meets the difference through a customer's failed
>    insert.
>
> Two constraints of this repository shaped the DDL and are recorded rather than worked around:
> `CREATE INDEX CONCURRENTLY` cannot run — every migration file is executed as one batch inside its own
> transaction — so the ownership indexes are **partial** (`WHERE tenant_id IS NOT NULL`), which indexes zero
> rows at creation and is also the correct index because no query ever looks for a NULL owner. And the
> constraint guard is copied from 0028's **schema-scoped** repair, not 0024's database-wide original, which
> would have reached straight through `internal/pgtest`'s schema-per-package isolation.

## 3. Backend — the durable registry and the seed

- [x] 3.1 Store package for tenant / user / membership / invitation / credential, with a `MemStore` for
      hermetic tests and a `PGStore` for the durable path, interchangeable through one interface — the shape
      `internal/account` already uses. Every read method returns an error: a durable store must have somewhere
      to report a failed read, and returning an empty list on an outage is how a webhook silently matches
      nothing.
- [x] 3.2 `auth.Registry` reads the durable store. Credentials verified against a stored hash in constant time.
      `Principal` gains `UserID` (empty for a machine credential, never a placeholder).
- [x] 3.3 **No positive-result cache**, and the reason recorded: an accept cached for 60s IS a cached
      non-revocation, so NFR1's two halves could not both hold across replicas. Decided at level 1. The
      regression test drives 25 successful lookups *before* revoking, so a caching implementation fails it.
- [x] 3.4 Boot-time seed from `cfg.TenantCredentials`: create-if-absent for both tenant and credential, never
      update, never delete. A partial seed **refuses to serve** rather than starting with some customers
      missing.
- [x] 3.5 Report the seed outcome (`created` / `already present`) and the self-serve posture on the readiness
      surface. A configured value that is only visible in a log is a value nobody checks before an incident.
- [x] 3.6 Suspension: a suspended tenant is refused at authentication, distinguishable in our own logs and
      indistinguishable on the wire from an unknown credential.

## 4. Backend — sign-up, membership, invitations

- [x] 4.1 `POST /api/v1/organizations` — creates `{tenant, user, membership(owner), account(Free, handle NULL)}`
      in **one transaction**. Verified claims come from the BFF's server side; the organization name is the only
      client-supplied field.
- [x] 4.2 Self-serve posture: declared configuration, **off by default**. When off, an unmapped verified
      identity is refused exactly as P22 refuses it today (`not_provisioned`) with no new code path — asserted
      by a test that the refusal is byte-identical.
- [x] 4.3 Membership create / change-role / remove, with the **last-owner** refusal as a named `DomainError`
      code, not a generic denial.
- [x] 4.4 Invitations: create (role decided by the inviter), accept (matching the **verified** address from the
      assertion, never a request field), single-use via `accepted_at`, expiry enforced. A mismatch or expiry is
      a refusal **and** a security event.
- [x] 4.5 Member removal in one transaction: membership → `removed`, sessions revoked, user-scoped credentials
      revoked, seats observation written, audit entry attributed to the **acting** user.
- [x] 4.6 Removal **preview** returns the organization-scoped machine credentials removal will *not* revoke, by
      label. This is the task that keeps the offboarding claim honest and it is not optional.
- [x] 4.7 Credential surfaces: create (plaintext returned exactly once), list (never the secret), revoke.
      Machine credentials and user-scoped credentials are visibly distinct in the response, because the
      difference decides what removal covers.

> **Section 4 evidence.** `internal/signup` (the one place identity and billing are composed),
> `tenancy.CreateOrganization` + `account.CreateWithin` (the shared transaction), and
> `internal/api/accounts.go` (thirteen routes, none of which takes an organization parameter).
>
> **Green:** `go test ./internal/signup/ ./internal/api/ ./internal/account/` and
> `run_pg_docker.sh go test -tags pgproof ./internal/signup/` — including
> `TestAFailedAccountWriteRollsBackTheOrganization`, which is the assertion the in-memory store cannot
> make: three identity rows written, the account write fails, and all four are taken back rather than
> three of them.
>
> **Two design points worth reading.** The billing account joins the identity transaction through a
> **hook** (`func(tenancy.Execer) error`) rather than through an import, so neither bounded context
> depends on the other — an import in that direction means an identity migration cannot run without a
> billing outage. And the **free plan is derived, never named**: the lowest-ranked plan carrying no price
> reference, so a deployment that calls its entry tier `starter` works and no second place holds the
> answer. A catalog with no free plan is a **refusal**, not a fallback to the cheapest paid one — falling
> back would demand a payment method from somebody who has not seen the product.
>
> **A boundary this section did not cross.** Task 4.5 asks for an audit entry attributed to the acting
> user. `audit_entry`'s actor column is `actor_admin_id` — an OPERATOR principal — and P8 FR1 makes the
> two identity domains categorically disjoint. Writing a customer's user id there would be the first join
> between the two halves that 0038's header says must never exist. So attribution is at the point of
> action (the response carries `removed_by`, the structured log carries actor, subject and both revocation
> counts) and a durable customer-facing audit trail is a **named follow-up** rather than a table this
> phase quietly borrowed.

## 5. Backend — ownership, scope, and the header

- [x] 5.1 Write the owning tenant at creation for run, variant spec, eval run and proposal, derived from the
      verified principal. Writes land **before** any read surface exists, so the first page a customer sees is
      not empty.
- [x] 5.2 `GET /api/v1/runs` — this tenant's runs, newest first, cursor-paged, index-backed. **No tenant
      parameter in any position**, and no unbounded result set.
- [x] 5.3 `POST /api/v1/token-exchange` — the BFF presents its own credential and a tenant; the platform issues
      a short-lived token bound to that tenant. The platform still resolves scope through `auth` and nothing
      else.
- [x] 5.4 Cross-tenant reads answer `404`, identical in body and timing class to a non-existent subject.
- [x] 5.5 No transfer interface. Assert its absence — ownership immutability is a property that erodes by
      somebody adding a convenient endpoint, not by a bug.
- [x] 5.6 `DomainError` codes for every new refusal: seat limit, last owner, invitation expired, invitation
      identity mismatch, self-serve disabled, suspended tenant, paid plan without a billing handle. Seven
      codes, not seven strings.

## 6. Backend — seats and billing

- [x] 6.1 `seats_current` derived from membership, read directly. A unit test fails if the usage store is
      consulted for it — that consultation is exactly what made the existing limit decorative.
- [x] 6.2 Entitlement refusal for seats names the plan allowance **and** the current count. Operator quota
      overrides continue to replace the plan allowance, unchanged from P7.
- [x] 6.3 `seats_billed`: write a `seats` usage observation on every membership activation and removal; the
      invoice line cites the period **peak**. Name the idempotent reconciliation point where the peak is
      derived — "the events are ordered" is not a reconciliation point.
- [x] 6.4 `StartCheckout` mints and persists the provider customer handle when it is NULL, under a key derived
      from the tenant so a retried checkout returns the same customer. Assert the retry produces one customer,
      against the real provider test account.
- [x] 6.5 A downgrade below the current seat count is refused, naming both numbers, with removal as the stated
      remedy — the same refusal shape as the invitation limit, deliberately.
- [x] 6.6 Account closure suspends the tenant, stops accrual, erases nothing, and the response names
      `gdpr_request` as the erasure mechanism.

> **Section 6 evidence.** `internal/seats` (the two quantities and the timeline replay),
> `entitlement.SeatCounter` + `stateMetrics`, `billing.Service.WithSeatCounter` +
> `ErrSeatsExceedPlan`, `StartCheckout` persisting the minted handle, and
> `POST /api/v1/organization/close`.
>
> **Green:** `make ci: PASS`, `make pg-proof` all 15 packages, and the unit suite.
>
> **The category error, undone.** `LimitSeats` was enforced against a `seats` usage record nothing ever
> wrote — so the comparison was against zero, forever, and passed. The reason nobody wrote it is that
> **a seat count is a STATE and it was modelled as a FLOW**: there was nothing to accumulate, because
> membership already held the answer. `stateMetrics` now routes the seat meter to a counter that reads
> membership, and a **table** rather than a branch, so correcting a meter's category is a row.
>
> **The honest half.** With no counter wired the limit is **skipped and said to be skipped**, never
> compared against a zero that passes and looks enforced.
> `TestAnUnmeasurableSeatLimitIsSkippedRatherThanTreatedAsZero` asserts the reason is populated,
> because "allowed because we could not measure" and "allowed because there was room" are the two
> answers P7 conflated.
>
> **An existing test was rewritten because it proved the wrong thing.**
> `TestCheckLimitDeniesBeforeTheAllowanceIsConsumed` used to seed a `seats` usage record and pass. No
> code path in this platform has ever written one, so it was asserting against a value that is zero on
> every real deployment. It now drives the count through the counter, and
> `TestTheSeatGateNeverReadsTheUsageStore` plants a `999` record and fails if the gate reads it.
>
> **`seats_billed` is DERIVED, not accumulated.** `seats.PeakOf` replays `joined_at` / `removed_at` —
> rows removal never deletes, because removal is a state change — so re-running produces the same number
> and there is no accumulator to lose. That is the named idempotent reconciliation point. Departures sort
> before arrivals at the same instant, so a same-second replacement is not a third seat; the ⚠️ that
> follows from it (a zero-duration membership contributes nothing) is documented, and it is what a
> frozen-clock fixture produces — which is why the API test advances its clock rather than asserting
> against the fixture.
>
> **A defect found by 6.4.** `StartCheckout` already called `EnsureCustomer` and **threw the answer
> away**. Invisible while every account was hand-created with a handle in it; with Free accounts that
> start with none, it means the platform never learns which provider customer is the customer's, and
> `SetPlan(…, charges: true)` then fails the invariant the database now holds. The handle is persisted
> **before** the session is created, so a failure between the two leaves an account that knows its
> provider customer rather than an orphan at the provider.

## 7. Frontend — four surfaces and one deletion

- [x] 7.1 The durable session store behind the existing session module. TTL, revocation-with-no-grace, cookie
      flags, fail-closed middleware and `scope.ts`'s no-tenant-parameter rule are **byte-for-byte unchanged**,
      asserted by a pinned regression suite rather than by review.
- [x] 7.2 `Session` gains `userId?`. Absent — never `""` — for a machine principal.
- [x] 7.3 **Delete `X-Console-Tenant` from `platformApi.ts`** and route every upstream call through the
      short-lived scoped token. A BFF that cannot obtain one fails closed with no upstream call.
- [x] 7.4 `/app/runs`: three distinct states — *no runs yet*, *runs that predate ownership*, *the platform did
      not answer* — with three messages and three next actions. Collapsing any pair is the failure mode.
- [x] 7.5 `/app/settings/members`: the state → copy mapping table for invited, expired, active, last-owner,
      over-seat-limit and removed, as a **fixed constant table**, not runtime string assembly.
- [x] 7.6 Removal confirmation renders the preview from 4.6, including the machine credentials removal does not
      revoke. A confirmation dialog that hides this is worse than none.
- [x] 7.7 Invitation acceptance page: pre-fills the organization and address, states plainly that signing in is
      what joins, and renders the identity-mismatch refusal as its own state.
- [x] 7.8 Organization creation page, reachable only when the deployment declares self-serve on.
- [x] 7.9 No seat number renders without its label. No seat count is derived in the browser.
- [x] 7.10 Browser-verify all four surfaces. A passing build and a green type-check are both compatible with a
      page that renders nothing.

## 8. Product Designer — the words, before the screens

- [x] 8.1 Noun dictionary for the customer-facing surface: **organization** (never "tenant"), **member**,
      **invitation**, **seat**, **plan**, **API key** — with the interface name, the entity name and the code
      name kept separate.
- [x] 8.2 The state → copy mapping table for members (six states) and for the runs list (three states),
      centralised in one section so a translation pass replaces a column rather than hunting components.
- [x] 8.3 Control-visibility matrix: which controls each of `owner` / `admin` / `member` sees, per surface,
      with the copy variant for each. Without it, an implementer decides by feel whether to hide or disable.
- [x] 8.4 Refusal copy for all seven `DomainError` codes. Every refusal that involves a limit names both
      numbers; a limit the user cannot check is a limit they distrust.
- [x] 8.5 The onboarding path: what the first person sees between completing SSO and having a working
      organization, in the fewest decisions that still produce a usable name.
- [x] 8.6 Design review against the checklist: no raw ids where a name belongs, no unlabelled seat figure, no
      empty state serving three meanings, no acceptance criterion that cannot be judged true or false.

## 9. AI Engineer — prove the measurement did not move

- [x] 9.1 Assert `config_hash` is unchanged by ownership: a variant spec resolved before and after P27
      produces the same hash. An ownership field leaking into the hash would silently invalidate every cached
      score. Guarded three ways, because the failure has three shapes:
      **recorded bytes** — four specs resolved through `Resolve` in a worktree at the pre-P27 commit, canonical
      JSON and hash checked in as `internal/variantspec/testdata/p27-pre-confighash.json`;
      **the hashed shape** — a ban on the ownership vocabulary in `ResolvedConfig`'s type graph, because an
      `omitempty` field no fixture sets produces byte-identical output (observed: adding
      `tenant_id,omitempty` left the recording GREEN and only this fence caught it);
      **live invariance** — the same spec submitted under two organizations against real Postgres derives one
      `config_hash` and one `run_id`.
- [x] 9.2 Assert scoring, confidence intervals, tie determination and Pareto dominance are unchanged against a
      **recorded pre-P27 board**, not against a re-computation. `internal/evalboard/testdata/p27-pre-board.json`
      was written by pre-P27 code and is only ever read here. Watched red on four independent perturbations
      (confidence level → intervals; the overlap test → ties; dropping cost, then latency, from the dominance
      rule → the frontier). The first fixture exercised none of it — its "tie" did not tie and its frontier
      dominated on quality and cost alone — which is why the board is a recording that gets LOOKED at.
- [x] 9.3 Assert tenant scoping is applied to *listing*, never to a statistic: a board ranks what it ranked. A
      scope applied to a statistic is a different statistic, and it would change silently. `ForWorkflow` is the
      only place the tenant is read; the scoped board is byte-identical to the same rows measured with no
      tenant in the call, relabelling every run's owner changes nothing, and two other organizations linking
      extreme runs does not move this one's numbers.

## 10. DevOps — deploy it without breaking anyone

- [x] 10.1 Wire the migration order into the deploy path and prove **rollback is re-apply**: deploy the prior
      image against the new schema and serve traffic. Migration order was already wired (`internal/launch`
      applies the schema before the account system composes and before traffic is served). The proof is
      [`deploy/scripts/prove-rollback-is-reapply.sh`](../../../deploy/scripts/prove-rollback-is-reapply.sh):
      a throwaway Postgres, this tree writes the row shapes only the new code produces, and a worktree at
      the prior ref reads them back **through the prior tree's own store types** — compiled against the
      prior package, never transcribed into a test here.
      **Its first run FAILED, and that is the finding.** The prior image BOOTS fine (`pgmigrate` ignores
      ledger rows it does not recognise) and then cannot read a free account: D3 had made
      `provider_customer_handle` nullable and the prior `scanAccount` reads it into a Go `string`. Because
      `List()` scans every row, ONE free account broke it for every caller — `adminops`
      tenant/delivery/crosstenant, `adminlaunch`, the billing webhook — for every customer. And the window
      was zero, not narrow: `ensureSeededAccounts` writes handle-less accounts at BOOT, so the unreadable
      rows arrived with the upgrade itself.
      **Fixed by amending D3** (damon, 2026-08-05): absence is now the EMPTY STRING. The column keeps its
      `NOT NULL`, 0013's `<> ''` check is dropped by name-by-shape, and the invariant becomes
      `provider_customer_handle <> '' OR plan_charges = FALSE`. `nullHandle` is gone and the read path scans
      a plain `string` — the same type the prior image uses, which is the point. No provider customer is
      minted for a free user, so D3's level-1 reasoning is untouched; the sentinel objection dissolved when
      D3 itself introduced `plan_charges` to carry the meaning. The database now refuses a NULL outright, so
      the prior reader's plain scan is safe by construction rather than by convention. Proof re-run: **PASS**.
      Ratification record updated in [`design.md`](design.md) § *D3 — amended by task 10.1*, with the rule it
      leaves behind: *an additive schema change is one the prior reader can still SCAN*, which is stricter
      than "every DDL statement is an ADD".
- [x] 10.2 Readiness reports the session store's backend, the seed outcome and the self-serve posture as
      **values**, not as the absence of an error. Seed and posture were already on `/readyz` under
      `account_system` (task 3.5). The session store's backend was **not**: `account_system.store` names the
      identity store the PLATFORM opened, and the console picks its own with `CONSOLE_SESSION_STORE` — so a
      Postgres identity store in front of a per-process console map is legal, reachable and silent.
      `describeSessionStore()` existed with a comment saying it was "for the console's own health surface"
      and nothing called it; it is now on `/api/health` as `{kind, durable}`, reporting the consequence
      rather than the setting. Two tests, one console each, that disagree by construction.
- [x] 10.3 P19's `replicas: 2` becomes claimable only after 7.1. Either the two ship together or the replica
      count comes down first — the manifest currently declares a topology the code cannot serve. The count
      is 2 and the manifest declares `CONSOLE_SESSION_STORE=platform` — but **the fence backing that claim
      had stopped working.** It asked "does `session.ts` keep a `Map` on `globalThis`", and 7.1 moved the
      map into `sessionStore.ts` behind a switch, so the fence found nothing, concluded the store was
      durable, and would have certified `replicas: 2` on a manifest that never selected it. It now reads the
      real determinant — the code offers a platform implementation AND this Deployment asks for it — and was
      watched red three ways: env deleted, env set to `memory`, and replicas back to 1.
- [x] 10.4 The air-gapped package asserts self-serve **off** at package-build time, beside the existing
      zero-external-origin assertion. [`check-self-serve-off.sh`](../../../deploy/scripts/check-self-serve-off.sh),
      invoked from `package-airgapped.sh` as step 2c with `die` on failure. It refuses two things, and the
      second is why it is a script: a package that turns self-serve on, **and one that never mentions it**.
      Unset already means off, so silence is not dangerous — it is undiffable, and an operator can see `"0"`
      become `"1"` but cannot see an absent line start being absent for a different reason. The airgapped
      overlay now declares `HEROS_SELF_SERVE_SIGNUP: "0"`. Self-test covers four cases including *prose
      naming the variable is not a declaration*; Go-side fences assert the gate is connected, the manifests
      pass, and the packager still calls it.
- [x] 10.5 Register the new operator-visible surfaces in the P26 operator-surface ledger, or record a reasoned
      absence. A phase that predates the fence does not get to be the phase that makes it vacuous.
      🔴 The fence was scoped to its own author: `CHANGE_SPECS_DIR` was a single hard-coded P26 path, so
      P27's five capabilities were invisible to it and would have stayed invisible until archive. It is now
      `GOVERNED_CHANGES`, a named list — **not** derived, because the obvious derivation ("no
      `openspec/specs/<name>`") reports 87 capabilities across 30 changes and would demand 80 rows for work
      that shipped phases ago, which is how a fence gets switched off. P27 added **no** operator surfaces:
      `account-registry` → `/tenants` (where an organization is listed, its plan overridden and its status
      suspended — and P27 is what makes suspension bite at authentication); `user-identity` → reasoned
      absence, because an operator page listing a customer's people by verified email is the exact join
      migration 0038 forbids in writing; `run-ownership`, `seat-accounting`, `self-serve-subscription` →
      `not-yet-readable`, each naming the collection that would make it readable (and seat-accounting also
      naming D5's **blocking** definition question).
- [x] 10.6 The backfill for `tenant_id` is conditional, resumable and interruptible, with progress readable
      from outside the process. **There is no backfill, and that is the answer.** The strongest form of all
      three properties is a migration that performs no rewrite: 0038 is three `ADD COLUMN … NULL`, three
      partial indexes covering zero rows at creation, and not one `UPDATE` — decided by D6, because a
      pre-P27 owner was never written and inferring one from a neighbouring table produces a confident wrong
      owner on billed usage. The residue is readable before a backfill exists (`PreOwnedCount`). Fenced two
      ways in `internal/pgmigrate`: the SQL carries no ownership rewrite, and — live — a run seeded through
      migration 37 still has a NULL owner after the rest apply, which catches a rewrite a source scan cannot
      see. Watched red by adding the backfill it forbids.

## 11. QA — fences that have been watched failing

- [x] 11.1 Four fences, each with a checked-in broken fixture that has been **observed red**: reintroducing
      `X-Console-Tenant`; a run created without an owner; a seat count read from the usage store; a credential
      written unhashed. `internal/api/p27_fence_fixtures_test.go` + `testdata/p27-fences/`. Every fence runs
      against BOTH corpora — red on its fixture, green on the tree — because a source scan on a clean tree and
      a source scan with a typo in its pattern produce the identical signal. Fixtures are `*.go.fixture` so
      no tool walks them and no exclusion is needed to keep them out of the real-tree scan. Two corrections
      the drill forced: the seat rule was first FILE-scoped and flagged `internal/entitlement` — the file that
      HOLDS the correction — plus `billingview` and three `cmd/proof` views for legitimately citing the period
      peak; it is line-scoped now, and carries a second POSITIVE assertion because the other way that defect
      returns adds no code at all (delete `metering.MetricSeats: true` from `stateMetrics` and `measure` falls
      back through `observed`). `TestTheTenantHeaderIsGoneAndStaysGone` now delegates to the shared detector
      rather than carrying a second copy of the rule.
- [x] 11.2 Cross-tenant probe: tenant A's token requesting tenant B's run observes `404`. The test must be
      **able to fail** — a first version that runs both probes as the same tenant passes vacuously, and that is
      the trap this test exists to avoid. `internal/api/crosstenant_probe_pgproof_test.go`, over HTTP against
      live Postgres. Every probe is a PAIR: the owner reads the run and gets 200, a stranger reads **the same
      run** and gets 404 — one request, one field different. A 404-only assertion passes against a platform
      with no isolation at all, because an absent run also answers 404. Also asserts the two refusals are
      identical in BODY as well as status (a differing body is the same disclosure the status code was careful
      not to make) and that an unauthenticated listing is 401, never a widened scope.
      `TestPG_TheCrossTenantProbeCanFail` runs the vacuous form and asserts it would have passed. Watched red
      by making `visibleToPrincipal` return true.
- [x] 11.3 Four-layer live assertions on sign-up, invitation acceptance and member removal. A 2xx is not
      evidence a row exists; re-read through the downstream consumption path after every write.
      `internal/api/accountflows_pgproof_test.go`, four layers per flow: **transport** (status + body),
      **store** (the row, in Postgres), **consumption** (the endpoint the next page load calls), **consequence**
      (the seat count moves; the removed member's credential stops authenticating; the new member can read the
      organization). ⚠️ Layer 1 surfaced a gap between the spec and the route — see the finding below.
- [x] 11.4 **Upgrade axis, separate from fresh install**: a database that already holds config tenants, runs
      and an account upgrades with nothing lost and every key still working. A clean-database test proves
      nothing about this. `internal/launch/upgrade_pgproof_test.go` builds the BEFORE state — migrations
      through 37, then a billable account with its provider handle, a finished run with its whole lineage, and
      two tenants that exist only as credentials in a config file — then upgrades and asserts *nothing lost*
      (the account unchanged, `List()` still works, the run still there), *every key still works* (both
      configured credentials, through the DURABLE registry they now resolve against), and *the gap is named*
      (the old run has no owner, is in nobody's listing, and is counted). Watched red by defaulting
      `plan_charges` FALSE — which declares every existing paying customer free. A second test covers the
      03:00 rolling restart: a self-serve organization and a member invited into a *configured* organization
      both survive a reboot, because configuration is a seed (D4).
- [x] 11.5 Warm-cache revocation test (see 3.3), and a suspended-tenant test asserting refusal at
      authentication rather than per feature. Both already stood: `TestRevocationIsEffectiveOnTheVeryNextRequest`
      warms 25 lookups before revoking, and `TestASuspendedOrganizationIsRefusedAtAuthentication` goes through
      the registry — so the refusal is at authentication, for a personal AND a machine credential, and
      reactivation restores access.
- [x] 11.6 **Store parity executed, not asserted**: the same behavioural suite runs against `MemStore` and
      `PGStore`, and the durable half runs against a live Postgres under the `pgproof` tag. One `storeSuite`,
      two callers (`memstore_test.go`, `store_pg_pgproof_test.go`), plus the four assertions only the durable
      half can make — concurrent last-owner demotion, an invitation accepted exactly once under concurrency,
      removal atomic across three tables, and the hash index unique across the whole table.
- [ ] 11.7 Real commercial walk on the hosted deployment: sign up → invite → exceed the seat limit and read
      both numbers in the refusal → upgrade through the real provider test account → list runs → remove the
      second person → observe their next request refused. This is the run that decides whether the phase is
      done.
      **BLOCKED, and the blocker is stated rather than worked around.** `heros-agent.space` is live and
      healthy, and it runs a **pre-P27 build**: `/signup` and `/join/<id>` answer 404, and `/api/health`
      reports no `session_store`. The capability the walk exercises is not deployed there, so the walk cannot
      be run — and running it against a local stack would be a different claim wearing this one's name.
      Unblocking it is a deploy of this branch to a live customer-facing host, which is a release decision,
      not a test step. Everything the walk covers is proved one layer down against live Postgres (11.2–11.4);
      what stays unproved is the composition on a real host with real Stripe test-mode collection.
- [x] 11.8 Record what is **not** covered: the IdP-side deactivation window (unchanged from P22, still bounded
      by the session TTL), and retention (unenforced, out of scope, PRD Open Question 4).
      [`design.md` § 9.1](design.md) — a *coverage* boundary kept separate from § 9's *design* boundary,
      because an unbuilt thing is visibly absent while an untested one looks exactly like a tested one. Seven
      entries: the deactivation window, revocation proved per-process rather than across replicas, retention
      enforced against nothing, the un-run commercial walk, sign-up's identity boundary, the limits of a
      source scan, and browser verification covering four surfaces rather than the console.

> **Section 11 finding — sign-up takes the identity from the request body.**
> `specs/self-serve-subscription/spec.md` says *"the identity, issuer and subject are taken from the verified
> assertion server-side"* and *"the organization name is the only client-supplied field"*.
> `handleCreateOrganization` checks that SOME principal is present and then reads `issuer`, `subject` and
> `email` out of the JSON body — so an authenticated caller can create an organization owned by a federated
> subject it names itself. **The shipped product is correct**: the console BFF fills those fields from its own
> session (`web/console/src/app/api/console/organization/signup/route.ts` — "the browser sends one field: a
> name"). The PLATFORM boundary is looser than the spec sentence, and it is not a one-line fix — a person who
> belongs to no organization has no membership and no console session row (`console_session.tenant_id` is NOT
> NULL), so there is no token for the platform to read an identity from. Two honest resolutions: narrow the
> route to a principal the platform recognises as the BFF, or amend the scenario to name which server holds
> the assertion. Pinned by `TestSignUpTakesTheIdentityFromTheRequestBody`, which goes red if either happens.

> **Section 11 finding — three of P27's live proofs were never run, and two other packages' are red.**
> `make pg-proof` did not list `internal/api`, `internal/tenancy` or `internal/signup`, so P27's own
> Postgres proofs sat outside the project's gate. It cost something immediately: the D3 amendment (10.1)
> inverted a `sql.NullString`/`.Valid` assertion in `internal/signup` — an empty handle reported AS a
> handle — and nothing went red. All three are in the target now. The enumeration also found four more
> ungated packages, two of them **already failing**: `internal/reportstore` on its own fixture (a
> `workflow` seed with no `repo_url`, which a later migration made NOT NULL) and `internal/deliveryrecord`
> only inside the batch. Neither is P27's and neither is fixed here — adding a red package to the gate
> breaks it for everybody. `internal/deploy/pgproof_gate_test.go` now fails if a `pgproof`-tagged package
> is neither in the target nor in a named exception list carrying its reason, and a second test fails if
> an exception goes stale.
>
> **And a race the addition exposed in 0038 itself.** With three more packages in the batch, four
> unrelated proofs began failing with `could not open relation with OID nnn` while applying 0038.
> `pg_get_constraintdef(c.oid)` is a catalog FUNCTION and the planner may evaluate it before the
> `n.nspname = current_schema()` filter — so the namespace-join form ran it over every schema in the
> shared database, and another package's concurrent `DROP SCHEMA` killed it. Both guards are bound by
> `conrelid = 'account'::regclass` now, which narrows the rows before the function runs.
> `TestCatalogGuardsAreScopedToTheCurrentSchema` accepted only the join spelling and rejected the correct
> one; it now accepts both, and carries a **second** rule that a catalog function REQUIRES the regclass
> bound — the join form is not sufficient there. Verified by two consecutive green `make pg-proof` runs.

## 12. Sales Operations — the claim boundary

- [x] 12.1 Write `docs/sales/P27-account-copy.md`: what becomes sayable, and the boundary each sentence must
      carry. Five rules, an eight-row licensing table keyed to the tasks that license each claim, the seven
      questions a buyer asks with the honest answer to each, the three a reviewer asks, and the routing for a
      question asked twice. Its status line now names the two boundaries that survive the phase: **no seat
      number anywhere a customer buys from**, and the end-to-end commercial walk **has not been run on the
      hosted deployment** (11.7) — so nobody may describe the flow from experience yet.
- [x] 12.2 *"Remove a member and their access ends at their next request"* — sayable only after 4.5 and 3.3,
      and only when stated **together with** what it does not cover: organization-scoped machine credentials.
      Both halves in one table cell, marked "always paired with", and again in § 4 as the answer to *"when
      exactly does removal take effect?"*: their sessions and personal keys stop with no grace window;
      organization keys they created are listed by name on the confirmation screen and are **not** revoked.
      Saying only the first half is how an offboarding checklist gets signed while a CI key is still deploying.
- [x] 12.3 *"Seats included in your plan"* — sayable only after 6.1–6.3, and **not at all** until Open
      Question 3 (is a CLI-only member a seat?) is decided. Quoting a number the system does not measure is
      the discipline violation, not an optimism. 6.1/6.2/8.4 are closed and the row is still marked 🔴 **NOT
      YET SAYABLE** — the mechanism ships and the sentence does not. Enforced rather than trusted: a seat
      number on the public surface **fails the build**, while a measured count with its label inside the
      product stays legal, because banning both would have deleted the honest half.
- [x] 12.4 State plainly that P27 changes **no price and no plan**. A phase that makes seats countable is not a
      phase that repriced them. Rule 4, with the consequence attached: a pricing change is a separate decision
      with a separate owner and does not ride in on this one.
- [x] 12.5 Keep the P22 offboarding claim intact and unmerged with this one: platform-side revocation is
      immediate; IdP-side deactivation is still bounded by the session TTL, and P27 does not close that window.
      § 3 answers *"if I disable someone in Okta, are they out immediately?"* as **two different things, and we
      say both** — no new session, and an existing one ends when it expires — with *(unchanged from P22; P27
      does not close this window and does not claim to)* attached so the two claims cannot be merged by
      whoever quotes one of them.
- [x] 12.6 Banned phrases enforced at build time, in the same mechanism P21's billing copy already uses: no
      "fully deleted", no "instantly revoked everywhere", no seat claim without its definition.
      `web/console/scripts/scan-claims.mjs` — eight new rules, drilled one probe at a time in
      `tests/public-surface.test.mjs` so none is a rule nobody has watched fire. Two shapes, because one was
      not enough: flat phrases banned **everywhere**, and patterns banned on the **public surface only** (the
      seat number and any retention period), since the same digits are honest inside the product and a claim
      on a page somebody buys from.
      🔴 Narrowed once during the writing: a bare `"directory sync"` flagged `src/content/identity.ts`, whose
      sign-in copy states the boundary CORRECTLY — *"no directory sync, no per-seat user model"*. Only
      affirmative claims are banned; the negation that names an absence is the sentence we want written. That
      is the same correction the seat fence needed in 11.1 and the same one this list's third-party-origin
      entries needed in P24 — a fence that reports the honest sentence as the violation is one somebody
      switches off.

## 13. Backend + CLI — the command line joins the account system

Added after the phase was drafted, on damon's instruction: *"the cli login should use this same account system
as well."* It is **last by dependency, not by importance** — it needs credentials (§3), users and memberships
(§4) and scoped resolution (§5) to exist first. It is also what makes §4.5's offboarding claim true: while a
CLI credential names no person, "removing a member ends their access" is false in a terminal.

- [x] 13.1 Device-authorization endpoints: request a code, poll for the result, approve. Codes are
      short-lived, single-use, and bound to nothing the client supplies. Approval requires an **active
      membership** in the organization being selected. Migration 0040 + `internal/tenancy/deviceauth.go` +
      `internal/api/deviceauth.go`. Two DIFFERENT secrets: a short user code a person retypes (~39 bits,
      and it grants nothing — approving needs an authenticated person and it cannot collect anything) and a
      32-byte device code that is the only thing which can collect. Both stored hashed. Single-use lives at
      the STORE, as one conditional `UPDATE … WHERE status='pending'`, never a read-then-write — two tabs
      and a double-clicked Approve both reach it and exactly one wins. A TABLE and not a per-process map
      because the CLI **polls**: request against one replica, poll against whichever the balancer picks, so
      a map is a login that succeeds or hangs by routing.
- [x] 13.2 `heros login` with no `--token` runs the device flow: print a short code and a URL, poll with a
      bounded interval and a bounded total wait, store the issued credential. The CLI never sees a password,
      an assertion or an ID token. Both bounds are the CLI's own as well as the server's — a server
      answering `interval: 0, expires_in: 86400` would otherwise produce a terminal spinning for a day.
      Asserted through the real command and the endpoint pin: it POLLS (4 requests for 2 pending rounds),
      and it prints the user code and the URL while printing **neither** secret.
- [x] 13.3 The issued credential is a **user-scoped** `api_credential` labelled with the device the CLI
      reported, so it is revoked by 4.5 and listed by 4.6 as personal rather than machine. The label is a
      display string — never compared, never used to find a record — so a revocation screen names something
      a human recognises instead of an opaque id.
- [x] 13.4 `--token` unchanged, and still the machine path. A machine credential has no user reference, is
      reported as machine, and survives member removal. Asserted by a test that fails if `--token` so much
      as touches a device route. A platform that says nothing about the kind (a pre-P27 one) still works:
      a token supplied on a command line is the machine path by construction, so that is the default.
- [x] 13.5 `whoami` extended **additively**: `identity` keeps its name, meaning and value; organization
      name, acting user (absent for a machine credential) and credential kind are added. Assert both
      existing callers still work — the CLI's `Validate` and the console's platform-token seam. `Validate`
      is untouched and is driven against a response carrying everything P27 added; `WhoAmI` is a SECOND
      reader beside it rather than a change to it. The way this requirement dies is not a rename — it is
      somebody returning `{organization: {...}}` because it is tidier, and nothing inside the platform
      noticing that two callers outside it read a flat `identity`.
- [x] 13.6 `heros status` names the person and the organization, and still prints no secret. The stored
      credential gained three `omitempty` fields, so a credential file written by an older binary still
      loads — an upgrade must not log anybody out. A machine credential prints no person: inventing one
      ("service account", "unknown") would put a name on actions nobody took.
- [x] 13.7 Denial, expiry and an unknown code are **one** message to the CLI. Distinguishing them helps only
      somebody guessing codes. One error value from the store, one status and one body from the API — the
      three refusals are compared byte for byte in a live test — and the CLI refuses to infer a difference
      from a status code. The message names none of the four causes and says what to do instead.
- [x] 13.8 A live proof: log in by device flow, run a command, remove the member, and observe the **next**
      CLI request refused. `internal/api/deviceauth_pgproof_test.go`, against real Postgres and over HTTP.
      This is the assertion the whole section exists for: before device authorization a person's terminal
      held an ORGANIZATION key, which no removal touches — so § 4.5's *"remove a member and their access
      ends"* was **false in a shell** and nothing in the product said so. Also proved: a non-member cannot
      approve into another organization, a **removed** member cannot approve into the one they just left,
      and a machine credential cannot approve at all.

> **Section 13 evidence.** Store contract in `internal/tenancy`, executed against `MemStore` and `PGStore`
> through the one shared suite (11.6's mechanism, three new subtests). Console approval surface at
> `/app/device` behind the session gate, with its BFF route carrying the code and the organization and
> **no identity** — the approver comes from the scoped token, exactly as `join` does. The path is compiled
> into two artifacts (the platform prints it, the console serves it) and `routes.test.mjs` asserts they
> match, because the CLI prints that URL before any browser is open and a rename on either side sends
> somebody to a 404 while they are signing in.
>
> **Two fences caught this work and both were right.** `TestEveryMountFunctionIsCalledByTheDeployedPath`
> rejected an exported `MountDeviceAuth` that `internal/launch` never calls — correctly: an exported
> `Mount*` announces an independently-mountable capability, and device auth is not one (its approval needs
> an active membership, so mounting it without the account system yields three routes that can only
> refuse). It is unexported and part of `MountAccounts` now, which says that in the type system rather
> than in a comment. `TestNoNewTableWasCreated` rejected migration 0040 until it was registered with its
> owning phase, which is the row that makes the P26 "creates no table" claim checkable.
>
> ⚠️ **Stated, not solved — two of them.** The issued plaintext is held in memory on the process that
> minted it, keyed by device id, until the poll collects it. It is deliberately **not** a column: a
> working credential at rest in a table is the one thing `Credential` was shaped to make impossible. The
> cost is real and is the refusal path — an approval on replica A with a poll routed to replica B cannot
> complete, and the CLI is told to start over. And neither unauthenticated route is rate-limited; the
> platform has no request-rate mechanism at this layer to hook into, the rows are small and expire in ten
> minutes, but a caller can mint them.
>
> ⚠️ `/api/v1/whoami` is registered by `MountRunLinking`, not by `MountAccounts` — the identity endpoint
> is mounted with an unrelated capability. Harmless deployed (`internal/launch` calls `MountRunLinking`
> unconditionally, with a nil source when there is no database) and found the way these things are found:
> a test that mounted only the account system got a 404 from `whoami`.
