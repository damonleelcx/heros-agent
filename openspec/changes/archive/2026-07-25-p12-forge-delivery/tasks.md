# Tasks — P12: Forge Delivery

Two waves. **Wave 12a** = CI-mediated delivery — the default mode, in which the platform holds **no
forge credential**. A complete phase on its own. **Wave 12b** = the hosted Git App, sequenced second
**deliberately** because it is the mode that carries standing write access, so it stays separable and
independently cuttable.

**Standing constraints.** Nothing unverified is delivered — the P5.5 gate is upstream and is not a
thing delivery can route around. `transform` is **not modified**; its immutability trigger and tests
stay green. The platform **never merges** below Autonomous. Delivery writes **only** pull requests and
their branches. Both modes produce **identical** pull-request content.

---

## 1. System Designer — Contracts and one-way doors first (12a)

- [x] 1.1 Fix the **pull-request body format**. It becomes a de-facto contract the moment a customer
      builds automation on it (PRD §14 Q3) — decide now whether it carries an explicit version, as
      P11's CLI output does. → carries `PRBodyContractVersion = "pr-body/v1"` marker;
      [p12-contracts.md §1](../../../docs/decisions/p12-contracts.md).
- [x] 1.2 Fix the `delivery` record's schema and key `(config_hash, source_revision, forge_ref)`. This
      key is the join P7's gainshare computation depends on. **One new table**, decided in
      [ADR-005](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md) against two
      alternatives per 🔴 `careful-table-creation` — do not add a second. →
      `0015_p12_delivery.up.sql`, one table, validated on live PG (partial-unique + append-only fire).
- [x] 1.3 Decide the **merge-observation mechanism** (PRD §14 Q4): webhook, polling, or CI-reported.
      Gainshare timeliness depends on it, and each option has a different failure mode. → CI-reported
      primary + App webhook secondary; never inferred from close; [p12-contracts.md §3].
- [x] 1.4 Decide whether `delivery_route` keys on repository or on `(repository, workflow)` (PRD §14
      Q6) — a monorepo may host several workflows with different reviewers. → keys on
      `(repository, workflow)`, workflow optional; `Target.Key()`; [p12-contracts.md §4].
- [x] 1.5 Decide **branch naming and stale-branch policy** (PRD §14 Q2). A predictable scheme aids
      idempotency; deletion must never remove something a customer built on. → deterministic
      `heros/opt/<hash>-<rev>`; never auto-delete; `BranchName`/`StaleBranchPolicy`; [p12-contracts.md §5].
- [x] 1.6 Confirm the first-class forge, and whether it must match P11's (PRD §14 Q1). → GitHub,
      matches P11; `ForgeKind`; [p12-contracts.md §6].

## 2. Backend — The delivery core (12a)

- [x] 2.1 Implement `Deliver(proposal, route)` with its preconditions enforced **server-side**:
      gate-passed, entitlement ≥ Team, halt readable and not armed. → `Deliverer.Deliver`; all five
      preconditions in order; tested.
- [x] 2.2 🔴 Enforce that **only a gate-passed change is deliverable**, from **every** entry point. A
      delivery path that could bypass the P5.5 gate would dissolve "verification decides." → the single
      `Deliver` funnel asks the authoritative `GateOracle`, ignores any caller "verified" claim;
      `TestDeliver_GateNotPassed`, `TestDeliver_NoVerdict`.
- [x] 2.3 Implement **idempotency** keyed by `(config_hash, source_revision, target)` — re-delivery
      **updates** the existing pull request. Must hold under retries, restarts, and **concurrent**
      attempts; the concurrent case is the one that actually produces duplicates. → deterministic
      `DeliveryID` + DB partial-unique on `state='opened'` + forge head-branch idempotency;
      `TestDeliver_Idempotent_Retry` and `_Concurrent`.
- [x] 2.4 Implement **supersession**: a newer verified proposal closes the older pull request **with the
      reason stated**, so a reviewer is never left with two candidates for one decision. →
      `Deliverer.supersede`; `TestDeliver_Supersession`.
- [x] 2.5 Implement the **per-repository open-PR bound**, with reaching it **reported** and the
      undelivered proposal **not discarded**. → `ErrBoundReached` (reported condition), nothing recorded
      or discarded; `TestDeliver_Bound`.
- [x] 2.6 Enforce **never-merge below Autonomous**; under Autonomous merge only a gate-passed change. →
      merge branch entered only at `LevelAutonomous`; `TestDeliver_NeverMergeBelowAutonomous`,
      `_AutonomousMerges`.
- [x] 2.7 Wire the P6/P8 **halt** to delivery scope, and make an unreadable halt state **fail closed** —
      the kill switch exists for the case where delivery is causing harm. → `HaltReader` (same
      fail-closed contract as `adminops.HaltsMerge`), `HaltReaderFunc` adapter;
      `TestDeliver_HaltArmed`, `_HaltUnreadable_FailsClosed`.
- [x] 2.8 Restrict forge writes to **pull requests and their branches** — 🚫 no direct push to a
      protected branch, no tags, no releases, no issues. Scope is easy to add later and impossible to
      take back from an approved installation. → `ForgeWriter` exposes only PR/branch ops (restriction
      by absence); `TestDeliver_WriteScope`.
- [x] 2.9 Isolate failures **per repository**: one customer's forge outage blocks no other, and a failed
      delivery does not lose the proposal. → `DeliverBatch` per-job isolation; `ErrForgeUnavailable`
      retains the proposal; `TestDeliverBatch_Isolation`.

## 3. Backend + AI Engineer — Pull-request content (12a)

- [x] 3.1 Render the PR body per §1.1: diff, **verified delta with its confidence interval**, held-out
      status, eval evidence, `config_hash` lineage, and a reference opening the full evidence in the
      console. → `RenderPRBody` (six fixed ordered sections); `TestRenderPRBody_CarriesEvidence`.
- [x] 3.2 🔴 Render evidence **as computed**. A delta whose interval overlaps the baseline **reads as a
      tie**, never as an improvement — a PR that oversells spends a reviewer's trust once and does not
      get it back. → `DeltaReadsAsTie` (low bound ≤ baseline); `TestRenderPRBody_TieReadsAsTie`,
      `_ImprovementReadsAsImprovement`, `TestDeltaReadsAsTie_Boundary`.
- [x] 3.3 Ensure the console reference resolves to a canonical route per P9's rules, so it survives
      being pasted anywhere. → `ConsoleEvidenceRef` (absolute + canonical, mirrors `routes.transform`);
      `TestConsoleEvidenceRef_Canonical`.
- [x] 3.4 Test — a tie-valued delta produces a PR that describes a tie; an improvement produces one that
      describes an improvement with its interval. → both covered above; plus `_Deterministic` and
      `_NoCredential`.

## 4. Backend — The delivery record (12a)

- [x] 4.1 Create the **append-only** `delivery` record per §1.2. State changes **append**; 🚫 no
      mutation, no deletion, no interface that expresses either. → `0015_p12_delivery` trigger +
      `Recorder` interface (Append + reads only, no mutate/delete method); `MemStore`/`PGStore`;
      `TestDeliveryIsAppendOnly` (live PG rejects UPDATE/DELETE/TRUNCATE).
- [x] 4.2 🔴 Leave `transform` **untouched**. Its immutability is what makes `config_hash`
      reproducibility checkable — `TestPG_Immutability_*` must stay green with delivery in use. → 0015
      touches no other table; `TestTransformImmutabilityStillHolds` proves the trigger survives and
      still rejects (HR001) after delivery is installed.
- [x] 4.3 Record the **mode** on every entry, so a later audit can answer which credential path opened a
      given pull request. → `mode` column (`ci`|`app`); `TestRecord_ModeRecorded`.
- [x] 4.4 Record a **merge into the target branch** from an **observation** (§1.3) — 🚫 do not infer a
      merge from a pull request closing. A closed-without-merge PR records `closed`, not `merged`. →
      `MergeObserver` (distinct `ObserveMerge`/`ObserveClose`, no inference) + schema
      `delivery_merge_is_observed`/`delivery_only_merged_has_commit`; `TestCloseIsNotAMerge`,
      `TestObserve_CloseIsNotMerge`, `_MergeFromObservation`.
- [x] 4.5 Append a **revert** as a further state; the `merged` entry stays. A disputed billed period
      must be answerable from the record. → `MergeObserver.ObserveRevert`; `TestObserve_RevertKeepsMerged`.
- [x] 4.6 Test — history reconstructable in order; no operation mutates or deletes; a closed-without-
      merge PR is never recorded as merged. → `TestRecord_AppendOnly_HistoryReconstructs`,
      `_NothingOverwritten`, plus the live-PG suite above.

## 5. DevOps + Backend — CI-mediated delivery (12a)

- [x] 5.1 Implement the delivery step running inside [P11](../p11-cli-ci-integration/)'s CI integration
      hook, using the CI environment's **own** ephemeral, repo-scoped forge token. → `CIStep`
      (fetch → open with the CI runner's writer → report); runs through P11's exposed hook, which owns
      no delivery behavior. `TestCIMediated_EndToEnd`.
- [x] 5.2 🔴 Assert **structurally** that the platform has **no forge-credential store** and no code path
      that reads one in this mode. This is the phase's headline security property and it should be
      provable by absence, not by policy. → `TestDeliverer_HoldsNoCredential`,
      `TestPrepared_CarriesNoCredential`, `TestPlatformCore_ReadsNoForgeCredential`,
      `TestCIWriter_HoldsNoPlatformCredential`.
- [x] 5.3 Implement the authenticated fetch by which CI retrieves the verified proposal and its
      evidence, scoped server-side to the caller's tenant and repository. → `Fetcher.Pending` served by
      `Deliverer.Prepare` (all preconditions server-side; tenant from the authenticated principal, not
      the request); `TestCIMediated_UnverifiedNotServed`.
- [x] 5.4 Report a **degraded** state when the CI credential is expired or rotated away — 🚫 never a
      silent stop, which reads as "no suggestions this week." → `ErrCICredentialExpired` →
      `CIReport.CredentialDegraded`; `TestCIMediated_DegradedCredentialReported`.
- [x] 5.5 Test — end to end: verified proposal → CI job → pull request on the customer's repository,
      with no platform-held credential anywhere in the path. → `TestCIMediated_EndToEnd`.

## 6. Product + Frontend — Visibility (12a)

- [x] 6.1 Implement the **no delivery route** reported state (FR13), rendered as a **condition with a
      next action** — 🚫 not an empty list. Silence here is indistinguishable from "the product found
      nothing," and that confusion lands hardest during evaluation. → `RouteConditionFor` +
      `RouteConditionBanner`; **verified in Chrome** (no-route scenario renders a banner with detail +
      "Next:" action, not an empty list).
- [x] 6.2 Implement the **degraded / revoked** state the same way. → `RouteRegistry.Capability`
      (degraded/revoked); **verified in Chrome** (degraded scenario renders the condition + next action).
- [x] 6.3 Show each delivery's state — **open / merged / closed / superseded** — linked to the proposal
      that produced it, so the loop from proposal to outcome is visible. → `/app/delivery` table with
      `Status` chips + `proposal_ref` "Open evidence" link; **verified in Chrome** (4 states rendered).
- [x] 6.4 Inherit P9's rules unchanged: token system, English strings, render-as-received, four distinct
      data states, browser-rendered acceptance. 🚫 Do not restate them here. → page composes existing
      primitives (`PageFrame`/`Failure`/`Status`/`Banner`), tokens only (scan:tokens/strings/markup/claims
      all pass), `Status` renders unknown states as-received; browser-rendered acceptance done above.

## 7. QA — 12a acceptance gate

- [x] 7.1 🔴 **Idempotency under concurrency** — two deliveries for the same
      `(config_hash, source_revision, target)` running concurrently leave **exactly one** pull request.
      A sequential-only test would miss the race that produces duplicates. → `TestAccept_IdempotencyUnderConcurrency`
      (25 rounds × 8 racers, passes under `-race`).
- [x] 7.2 🔴 **Halt fails closed** — make the halt state unreadable and assert delivery does **not**
      occur. If this test cannot be made to fail, the requirement is decoration. → `TestAccept_HaltFailsClosed`
      (armed + unreadable both withhold).
- [x] 7.3 **Gate integrity** — an unverified change is undeliverable through every entry point. →
      `TestAccept_GateIntegrityEveryEntryPoint` (App `Deliver` + CI `Prepare`).
- [x] 7.4 **Credential absence** — assert no forge-credential store and no code path reading one in the
      default mode. → `credential_absence_test.go` (runs in the same acceptance binary).
- [x] 7.5 **Volume bound** — a burst cannot exceed the bound; reaching it is reported and the proposal
      is not discarded. → `TestAccept_VolumeBound` (reported + no partial record).
- [x] 7.6 **Visibility** — route-absence and revocation assert as *renderings*, not just log lines. →
      `TestAccept_VisibilityIsRendered` + `TestP12Deliveries_RendersConditionAndStates` (served JSON) +
      Chrome verification in §6.
- [x] 7.7 **Merge observation** — a merge records `merged`; a close-without-merge does not; a later
      revert appends a further state. → `TestAccept_MergeObservation`.
- [x] 7.8 **Isolation** — a forge failure for one repository blocks no other and loses no proposal. →
      `TestAccept_Isolation`.
- [x] 7.9 **Entitlement** — delivery below Team refused server-side; auto-merge below Enterprise refused.
      → `TestAccept_Entitlement`.

---

## 8. Backend + DevOps — Hosted Git App (12b)

- [x] 8.1 Implement installation and revocation flows: **per-repository** selection, 🚫 never org-wide by
      default. → `Installation` (no org-wide flag; ≥1 repo required) + `InstallationStore.Install/Revoke`;
      `TestApp_InstallationIsPerRepository`.
- [x] 8.2 Define and document the **least-privilege permission set** — no broader than opening and
      updating pull requests on selected repositories. Broadening it is a **spec change**, not a config
      choice, and the narrowest credible ask is what gets an installation approved. →
      `LeastPrivilegePermissions()` + `WithinLeastPrivilege()`; documented in
      [p12-contracts.md §6b](../../../docs/decisions/p12-contracts.md); `TestApp_LeastPrivilege`.
- [x] 8.3 Hold the installation token in a secrets manager. 🚫 Never in code, git, a log line, a
      telemetry attribute, a pull-request body, or an artifact; never leaving the platform. →
      `SecretsManager.UseToken` closure (no get-token method); `TestApp_CredentialNeverInEmittedSurfaces`.
- [x] 8.4 Make revocation effective **from the customer's side without contacting us**, and report the
      resulting state. → `InstallationStore.Revoke` + `Capability`→`revoked`;
      `TestApp_RevocationStopsDeliveryAndIsReported`.
- [x] 8.5 Test — no credential in any emitted surface on success **and** failure; revocation from the
      forge side stops delivery and is reported. → `TestApp_CredentialNeverInEmittedSurfaces` (success +
      failure paths) and `TestApp_RevocationStopsDeliveryAndIsReported`.

## 9. QA — 12b parity gate

- [x] 9.1 🔴 **Content parity** — byte-compare the pull request produced by both modes for one proposal.
      Verified by comparison, not by review; two renderings drift. →
      `TestParity_PullRequestContentIsByteIdentical` (CI `OpenFromPrepared` vs App `Deliver`, bodies
      captured from the forge and byte-compared).
- [x] 9.2 Re-run the full 12a suite against the hosted mode. → `TestParity_HostedModeReRun`
      (gate integrity, idempotency-under-concurrency, autonomous merge — all through `AppForgeWriter`).

## 10. Sales Operations — Claims (12a → 12b)

- [x] 10.1 Lead with the default posture: **"you grant us nothing — your CI opens the pull request with
      a token it already has."** Repository write access is where source-tooling deals stall, and this
      answers it before it is asked. → [P12 sales claims §10.1](../../../docs/sales/P12-forge-delivery-claims.md).
- [x] 10.2 🚫 Present the hosted App **honestly** as real standing write access, contained per-repo,
      least-privilege and revocable — not as equivalent to the default. → sales claims §10.2.
- [x] 10.3 🚫 Do not claim delivery below **Team**, or that the platform merges below **Autonomous**
      (Enterprise). The positioning is *a human merges*; overselling autonomy contradicts the spec and
      the screen at once. → sales claims §10.3.
- [x] 10.4 Frame gainshare on **merges**, not on proposals opened — only merged deltas are billable. →
      sales claims §10.4.

## 11. Documentation

- [x] 11.1 Fold the two P12 capability specs into `openspec/specs/` when the change deploys. →
      `openspec/specs/forge-delivery/spec.md` (15 requirements) and `openspec/specs/delivery-record/spec.md`
      (7 requirements), with doc-relative paths adjusted.
- [x] 11.2 Record the §1.1 PR-body format and the §1.3 merge-observation decision as ADRs or referenced
      contracts. → [`docs/decisions/p12-contracts.md`](../../../docs/decisions/p12-contracts.md) §1
      (PR-body format + version) and §3 (merge-observation mechanism).
- [x] 11.3 Update [`p2-config-runtime/tasks.md`](../p2-config-runtime/tasks.md) §3.10 to point at
      ADR-005 — it currently recommends "C now, then B or A behind a new ADR," and that ADR now exists
      and chose neither B nor A. → §3.10 carries the ADR-005 resolution note and the stale recommendation
      is now struck through as superseded.
