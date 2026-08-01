# P26 — Tasks

Ordered by wave. **Wave 26a lands the fence and the three honesty corrections before any new page** — the
pages are this phase's output, the fence is its product, and a phase about operator honesty cannot add
four pages while leaving three known-wrong figures on shipped ones.

Each task is independently verifiable. A completion claim must name an assertion that exists and runs.

## 1. Wave 26a — the fence

- [x] 1.1 Author `operator-surface-ledger.md` with a row for **every** capability directory in
      `openspec/specs/`, each resolving to `surface: <href>`, `no-operator-surface` (reason + deciding
      phase), or `not-yet-readable` (the collection that would make it readable, named). No fourth state.
      (`openspec/operator-surface-ledger.md`; 34 section-A rows + 6 section-B rows for this change's own
      capabilities + 13 section-C rows for the operator destinations. Test: *the fence is GREEN on the
      current tree*, `web/admin-console/tests/surface-ledger.test.mjs`.)
- [x] 1.2 Build fence: a capability in `openspec/specs/` appearing in no ledger row fails the build,
      naming the capability. Demonstrate it red by adding a capability directory with no row.
      (`web/admin-console/scripts/scan-ledger.mjs`. Test: *🔴 1.2 — a capability with no ledger row fails
      the build, naming the capability* — creates a real directory under `openspec/specs/` and requires
      exit 1 naming it.)
- [x] 1.3 Both-directions assertion, forward: every `surface:` row resolves to a destination present in
      `web/admin-console/src/lib/surfaces.ts`. Demonstrate it red with a row pointing at a
      non-existent href. (Test: *🔴 1.3 — a row naming a destination absent from surfaces.ts fails,
      naming the row and the destination*.)
- [x] 1.4 Both-directions assertion, reverse: every destination in `surfaces.ts` is named by at least one
      ledger row. Demonstrate it red by adding an unnamed destination. (Test: *🔴 1.4 — a destination in
      surfaces.ts named by no ledger row fails, naming the destination* — writes a real entry into
      `surfaces.ts` and restores it byte-for-byte.)
- [x] 1.5 Assert that a `not-yet-readable` row **names a collection** — a row with an empty detail fails,
      so the state cannot become a place to park a wish. (Tests: *🔴 1.5 — a not-yet-readable row with an
      empty detail fails…* and its positive twin *…that names a collection passes, so the state stays
      usable* — a fence that rejected every row would also "pass" the red demonstration.)
- [x] 1.6 Wire the fence into the operator console's build and the repository's CI, beside the existing
      token, bundle and drift checks. (`package.json` → `scan:ledger`, and `build` = tokens → ledger →
      next build → bundle; `Makefile` → `operator-ledger` / `operator-console-test`; `.github/workflows/ci.yml`
      → the `operator-console` job, with the ledger fence as its own named step. Tests: *the fence runs in
      the console's build and is exposed as its own script*, *the fence runs in the repository's CI*.)

## 2. Wave 26a — the honesty corrections

- [x] 2.1 Add link coverage to `internal/adminops/billing.go`'s read model, paired with the figure **in
      the type** (`DerivedFigure.Coverage *float64`) so a figure cannot be rendered without it.
      (`internal/adminops/figures.go` — `DerivedFigure`, `NewDerivedFigure` as the only constructor,
      `Renderable()`; `BillingService` now takes a `LinkCoverageSource` and `BillingOversight` carries
      `LinkCoverage`, `MeteredSUM`, `GainshareSavings`.)
- [x] 2.2 Render link coverage beside every SUM-derived figure on the operator billing surface, in the
      same view — not behind a link, not in a footnote. (`components/derived.tsx`, rendered in the
      *What these figures count* section above the tables. Tests: Go
      *TestLinkCoverageIsDisplayedBesideEverySUMDerivedFigure*; console *🔴 2.2 — a SUM-derived figure
      reaches the screen ONLY through the component that carries its coverage* and *…the billing surface
      states its coverage…*. Both console assertions demonstrated red by rendering `metered_sum.value`
      directly. Browser-verified at 30% coverage on `tenant-boreal`.)
- [x] 2.3 A figure whose coverage is unknown is **not rendered**; the surface states that coverage is
      unknown instead. Assert it. (Test: Go *TestAFigureWithUnknownCoverageIsWithheld*; console *🔴 2.3 —
      the derived component withholds the figure…*. Browser-verified on `tenant-dune`, which has no
      reported run count: both figures render as *Not shown — link coverage is unknown*.)
- [x] 2.4 Name the provenance of every gainshare / verified-savings figure where it appears: the P5.5
      verified-delta ledger and nothing else. (`adminops.SourceVerifiedDeltaLedger`, rendered by
      `<Derived>`. Test: *TestAGainshareFigureNamesTheVerifiedDeltaLedger*, which also asserts the
      metered figure does NOT claim that provenance.)
- [x] 2.5 Exclude `unverified` authored changes from every aggregate improvement, savings and quality
      figure, at the query. (`AggregateAuthoredImprovement` in `crosstenant.go`, filtered through
      `authoring.CountableAggregate` — the platform's one filter — rather than a second copy of the rule.)
- [x] 2.6 **Prove 2.5 by seeding one.** Create an authored change, leave it unverified, and assert it
      contributes exactly zero to every such aggregate. Asserting the `WHERE` clause exists is asserting
      that we wrote a `WHERE` clause. (Test: *TestASeededUnverifiedAuthoredChangeContributesExactlyZero*
      — reads the figures before and after, iterates `adminops.ImprovementFigures` so a fourth figure
      added without the filter is caught, and includes the control half: a VERIFIED change DOES move the
      figure, so a filter that excluded everything would not pass.)
- [x] 2.7 State on the audit surface which merge paths the hash chain covers (P6 autonomous merges, via
      `mergeaudit.go`) and which it does not (P12 customer-CI-mediated deliveries), and link the delivery
      surface for the paths it does cover. (`adminops.MergeCoverage()`, rendered as its own section; the
      page's lede corrected from "every merge" to "every AUTONOMOUS merge". Tests: Go
      *TestTheAuditSurfaceStatesWhichMergePathsTheChainCovers*; console *🔴 2.7 — …*. Browser-verified.)
- [x] 2.8 One named regression test per correction (2.2, 2.6, 2.7), each naming the requirement it
      defends, so a later change that removes it fails with an explanation. (`internal/adminops/honesty_test.go`
      and `web/admin-console/tests/honesty.test.mjs`; every failure message names the requirement and
      says what breaks if it is removed.)
- [x] 2.9 **Wave gate:** the fence is red-demonstrated four ways (1.2–1.5) and the three corrections have
      named regression tests. No new page exists yet. (`surfaces.ts` unchanged in this wave; the four new
      destinations are `not-yet-readable` rows in the ledger, and the fence is green on that.)

**Wave 26a also fixed two defects it uncovered, both recorded rather than folded in silently:**

- 🔴 **FR23 was red before this change.** The operator and customer light accents were 17° apart against
  a fence requiring >25°, and the dark accents 8° apart — the light assertion failed first, so the dark
  violation had never been seen at all. The operator console's accent family moved from teal-cyan to
  **violet, with a slate-lilac secondary** carrying the chrome band's ink and the accent border: 87–90°
  from the customer console in both themes, and 105–142° from both hazard hues, with WCAG AA re-checked
  on every pair. The operator console moved rather than the customer one because FR23 is the operator
  console's safety property. (An intermediate azure was tried and rejected on review; violet clears the
  fence by a much larger margin than the ~33° azure did, which is the margin a hue this load-bearing
  should carry.)
- 🔴 **The billing surface's credit/refund control was unreachable.** Its condition tested `line.type`
  against two `ChargeKind` values (`"subscription"`, `"metered"`) that no `type` ever takes, so the
  drawer never appeared against an ordinary charge. Now reads `line.kind`. Browser-verified.

## 3. Wave 26b — Delivery (read-only)

- [x] 3.1 Decide and add the governing capability (proposed `delivery.read`; see PRD open question 3) to
      `adminrbac.Capabilities` with a considered grant per role. The matrix test must pass with it.
      **DECIDED: a separate `delivery.read`** (design D8's recommendation), granted to Support,
      Billing-Ops, Platform-SRE and Superadmin. Support's grant is the point: it is what lets a delivery
      question be answered without an impersonation session, and folding the view into `crosstenant.read`
      would have handed Support fleet-wide spend and usage to answer a question about a pull request.
      `TestSupportHoldsOnlyReadAndReadImpersonation` was updated deliberately and carries the reasoning;
      `TestEveryCapabilityHasARolloutWave` forced a considered wave (8b) rather than a silent default.
- [x] 3.2 `internal/adminops` delivery read model over `deliveryrecord` + `forgedelivery`, per tenant and
      cross-tenant. (`internal/adminops/delivery.go` — `Tenant()` and `Fleet()` are two reads with two
      audit entries, not one read with a filter.)
- [x] 3.3 `MergeState` as three values — `merged` / `closed_unmerged` / `unknown`. Assert a merge is read
      as **observed** and is never derived from a pull request closing. (Tests:
      *TestAClosedPullRequestIsNeverReadAsAMerge* — seeds all three and asserts each; and
      *TestTheThreeMergeStatesStayThree*, which asserts totality over every P12 lifecycle state so a new
      one cannot fall through to a zero value that renders as an empty cell. Console: *🔴 3.3 — the
      delivery surface renders all THREE merge outcomes, and `unknown` as itself*.)
- [x] 3.4 Rollout stage from `changedelivery` (ADR-010), with the undeliverable count and its typed causes.
      (Test: *TestUndeliverableCausesStayTypedAndSeparate* — each cause is a stable identifier from
      `changedelivery.Causes()`, never prose; each names its owner; a permanent cause carries NO missing
      artifact; and the causes are in EVALUATION order, not sorted by volume. Browser-verified: 7 cells
      `not-runtime-resolvable` (boundary, no artifact) and 3 `no-rollout-binding` naming the field.)
- [x] 3.5 Admin API read routes behind the granted capability; each writes its audit entry on the **same
      code path** as the read, not from a poller. (`GET /admin/api/delivery`, `/{tenant}`, `/{tenant}/{id}`.
      Test: *TestEveryCrossTenantDeliveryReadIsAuditedOnTheReadPath* — asserts the entry, its actor, its
      scope, that a per-tenant read is audited against the TENANT, and that the chain stays intact.)
- [x] 3.6 `/delivery` route plus its `surfaces.ts` entry plus its ledger row — three files, or the slot
      silently disappears from the palette while looking fine in the nav. (All three; the ledger's reverse
      assertion is what makes the third file non-optional — omitting it fails `npm run scan:ledger`.
      Four section-A/B rows moved from `not-yet-readable` to `surface: /delivery`.)
- [x] 3.7 Drill-down from every aggregate to the individual records behind it. (Every `DeliveryCount`
      carries its own `drill_down` query. Test: *TestEveryDeliveryAggregateOffersItsDrillDown*, plus the
      console's *🔴 3.7*. Browser-verified: the four fleet counts are links.)
- [x] 3.8 Assert **no** control on this surface opens, closes, retries or merges a delivery. (Test:
      *TestTheDeliverySurfaceIsReadOnly* — enumerates `DeliveryService`'s exported methods by reflection,
      so a `Retry` added later fails HERE rather than in review. Console: *🔴 3.8 — no control on the
      delivery surface acts* asserts no server action, no `ActionForm`, and that the page's only form is
      a GET.)
- [x] 3.9 Contract test against a real Postgres running the real migration chain. No inline
      `CREATE TABLE` standing in for a production table. (`internal/adminops/delivery_pgproof_test.go`,
      driving `deliveryrecord.PGStore` over the real `delivery` table. The pgproof chain was extended to
      `0001 → 0014 → 0002 → 0003 → 0004 → 0015`. **Executed against a real PostgreSQL 16.14** — the three
      merge outcomes survive the round trip, and the whole `-tags pgproof ./internal/adminops/` suite is
      green on the extended chain.)

## 4. Wave 26c — Releases & Trust (read-only)

- [x] 4.1 Decide and add the governing capability (proposed `release.read`; see PRD open question 4).
      **DECIDED: a separate `release.read`**, granted to Platform-SRE and Superadmin only. A release
      engineer is a fifth persona this console has no role for; until it does, Platform-SRE is the
      nearest holder. NOT granted to Support or Billing-Ops — signing-key state is not something a
      support queue needs, and the alternative (widening `registry.admin` to cover keys) would have
      widened a role that already administers the model registry.
- [x] 4.2 Release read model over `internal/distribution`: published versions per channel, artefacts per
      platform. (`internal/adminops/release.go`. The channel contract and the target matrix come from
      `internal/distribution` unchanged; the per-release publish record arrives through a `ReleaseSource`
      the deployment wires — no new table, per D7. Test: *TestAReleaseDeploymentWithNoRecordSaysSo* —
      a deployment with no source reports it plainly instead of rendering an empty page as a working one,
      while still showing the compiled-in channel contract, which is true regardless.)
- [x] 4.3 Signing-key state: the active key and every retired key with its rotation date and recorded
      reason; identify published artefacts signed with a retired key. (`release.RetiredKeys()` — a NEW
      machine-readable rotation record for the two keys P20 withdrew, whose reasons previously existed
      only as source comments. Test: *TestArtefactsSignedWithARetiredKeyAreIdentifiable*, which also
      asserts a retired key is **not** in the verify set: serving the rotation history must never widen
      the set of keys a running binary accepts. Browser-verified.)
- [x] 4.4 **Assert no key material on any surface** — identifier and fingerprint only. Assert the surface
      offers no key generation, no export, and no operation whose output is key material. A signing key has
      already leaked once in this project by being emitted into a session transcript.
      (Test: *TestNoKeyMaterialReachesTheReleaseSurface* — it marshals the whole read model and scans the
      BYTES: no live public key, no 32-char prefix of one, no 40+ character hex run anywhere, no PEM or
      OpenSSH marker. A field added later called `public_key` passes a field-by-field review and fails
      here. It also asserts the surface still identifies every key, so "no key material" cannot be
      satisfied by showing no keys. Plus *TestTheReleaseSurfaceOffersNoOperationThatProducesKeyMaterial*,
      which enumerates the service's methods by reflection, and the console's *🔴 4.4*, which asserts the
      page has no form at all and that `SigningKeyRow` carries no field that could hold material.
      🔴 The retired keys' fingerprints are the PUBLIC-KEY PREFIXES the rotation record preserved, not
      SHA-256 fingerprints, and the type says so: retiring a key deletes the material a fingerprint
      would be computed from, and computing a plausible-looking hash would be inventing evidence.)
- [x] 4.5 `VerifyState` as three values — `verified` / `failed` / `not_yet_verified`. (Test:
      *TestArtefactVerificationHasThreeStates*, which also asserts a not-yet-verified artefact carries no
      smoke result — the sequence cannot have reached smoke. Console: *🔴 4.5*, asserting unchecked is
      painted NEUTRAL rather than with the hazard palette.)
- [x] 4.6 `SmokeState` as three values — `passed` / `failed` / **`queued_until_timeout`**. Assert a queued
      run is not rendered as a failure: a retired runner label queues until timeout, and reading that as
      *failed* sends an engineer to debug a build that never ran. (Test:
      *TestAQueuedSmokeRunIsNotRenderedAsAFailure* — asserts the queued row is not counted as a failure
      AND that its reason says the job *never started* and tells the reader it is *not a failed build*.
      Console *🔴 4.6* asserts the queued tone is neutral. Browser-verified against v0.20.0, whose
      `darwin/arm64` smoke queued until timeout.)
- [x] 4.7 Show where the publish → verify → smoke sequence **stopped**, not only its final state.
      (`SequenceRow.StoppedAt` + `Completed`. Test: *TestTheSurfaceShowsWhereTheSequenceStopped* — asserts
      a green publish with a red smoke is stated as *not successfully delivered*, and that a not-yet-run
      step is distinguishable from a failed one. Browser-verified: three releases, three stopping points.)
- [x] 4.8 `/releases` route + `surfaces.ts` entry + ledger row. (All three; the ledger's reverse assertion
      makes the third non-optional.)
- [x] 4.9 Assert this surface halts, unpublishes and changes nothing (the channel-halt control is PRD open
      question 1, deferred). (Tests: *TestTheReleaseSurfaceOffersNoOperationThatProducesKeyMaterial* on the
      platform side; console *🔴 4.9*, which greps the page for halt/unpublish/re-sign/republish/rerun/retry
      vocabulary. **PRD open question 1 resolved as Path B** — the halt stays in the pipeline for this
      phase, per design D2 and the PRD's own recommendation: a control that stops software reaching
      customers deserves its own design conversation, not a paragraph in a refresh.)

## 5. Wave 26d — Axes (read-only)

- [x] 5.1 Decide and add the governing capability (proposed `axis.read`; see PRD open question 3).
      **DECIDED: a separate `axis.read`**, granted to Platform-SRE and Superadmin. It lets an axis owner
      see refusals WITHOUT seeing usage or spend — the partition `crosstenant.read` could not express,
      because that capability grants the money aggregates in the same breath and the question *which
      materializer would unblock the most refused nodes* needs none of them.
- [x] 5.2 Per-axis read model: the axis's own declared `EXISTS / PARTIAL / ABSENT` status and fleet
      adoption (tenants and nodes carrying an override). (`transform.StatusFor` — the status is declared
      **beside the coverage table it derives from**, not in the console, so a doc, a CLI listing and this
      page cannot disagree. Tests: *TestTheAxisSurfaceRendersTheAxisStatusAsDeclared*, and
      *TestAdoptionWithNoSourceIsUnknownRatherThanZero* — with no adoption source the counts are `nil`
      and render as *unknown*, because "no tenant uses this axis" and "we did not measure adoption" are
      opposite claims that look identical as a zero.)
- [x] 5.3 Refusal counts keyed by **stable typed cause identifier**, never prose, per axis and per
      language. Keep `not-expressible-at-a-call-site`, `call-site-cannot-carry-it` and
      `no-materializer-for-this-language` distinguishable — they are answered by three different people.
      (Test: *TestTheThreeCausesStayDistinguishable* — asserts every counted cause is one of the engine's
      identifiers, that the legend resolves to **three distinct owners**, and that every axis with
      refusals also breaks them down per language. Browser-verified: e.g. `skills` shows
      `call-site-cannot-carry-it: 7` and `no-materializer-for-this-language: 6` as separate figures.)
- [x] 5.4 Rank which artefact would close the most refusals (form row / list splitter / statement resolver
      / registry row / frontend field), so the backlog is ordered by evidence. (Test:
      *TestTheRankingIsCountsAndNamesOnlyClosableArtefacts* — only cells carrying a NAMED artefact are
      counted, so a permanent boundary is never put on a backlog that cannot close it. Browser-verified:
      the memory materializers for java/javascript/kotlin/rust/typescript lead at 4 refusals each.)
- [x] 5.5 Coverage matrix read from the **one coverage source**. Assert the console computes, caches and
      reformats nothing. (`transform.AxisCoverage()`, passed through unchanged. Tests:
      *TestTheAxisReadIsNotCached* — two reads produce two audit entries, so no copy is held; console
      *🔴 5.5*, which asserts the page performs no `reduce` or `sort` on a coverage answer.)
- [x] 5.6 Parity assertion in **both** directions against the **real engine** (not a fixture): the surface
      offers no cell the engine refuses, and omits no cell the engine materializes. (Test:
      *TestTheAxisSurfaceIsAtParityWithTheRealEngine* — set equality over (axis, language, form) against
      `transform.AxisCoverage()`, asserting the state, the **verbatim stable cause** and the missing
      artefact on every cell, in both directions, and refusing to pass on an empty engine result.)
- [x] 5.7 An absent row renders as **unknown**, naming what is missing. Assert it is never rendered as
      *not applicable*, which is a claim about the customer's code. (Test:
      *TestNoCellRendersAsNotApplicable* — the strongest form available: the value does not exist in
      `CellState`, so the read model cannot produce one by accident, and the serialised model is also
      scanned for the phrase in case a later change smuggles it in as prose. Console *🔴 5.7* does the
      same for the page, word-bounded so it does not fire on `/admin/api/axes`.)
- [x] 5.8 Assert no coverage gap is presented as a plan boundary — identical on every plan, no tier
      unlocks a cell the engine refuses. (Tests: *TestACoverageGapIsNotPresentedAsAPlanBoundary* — scans
      the read model for plan/tier/entitlement vocabulary and asserts a permanent boundary is never
      described as *not yet*; console *🔴 5.8* targets the OFFER rather than the word, since the page
      states the denial in prose.)
- [x] 5.9 Assert a refusal count is not rendered with the visual grammar of a ranked result: only P4 ranks,
      and only a P5.5 verified delta is a claim. (`is_ranking: false` on the wire. Test:
      *TestTheRankingIsCountsAndNamesOnlyClosableArtefacts*; console *🔴 5.9* asserts no chart, no bar,
      no score read off the model, and no meter/progressbar semantics.)
- [x] 5.10 `/axes` route + `surfaces.ts` entry + ledger row, with drill-down to individual refused nodes.
      (All three; 14 section-A/B ledger rows moved to `surface: /axes`. Drill-down:
      `GET /admin/api/axes/{axis}/refused`, asserted by *TestTheAxisSurfaceIsReadOnlyAndDrillsDown*.)

## 6. Wave 26e — Oversight (read-only)

- [x] 6.1 Show which factor authenticated each operator session, and when. Works against the real verifier
      whether the IdP is the test-mode fixture or a P22 provider; claim no real IdP exists.
      (`SessionStore.Sessions()` — a new read-only listing that carries the factor NAME and no token,
      because the bearer token is returned once and never stored. Test:
      *TestAnOperatorSessionShowsTheFactorThatAuthenticatedIt* — runs against sessions the REAL
      `adminidentity.Authenticator` issued after a real assertion and a real MFA check, asserts the
      surface says the issuer is the FIXTURE and the VERIFIER is real, and asserts no bearer token
      reached the read model. Browser-verified: `adm-superadmin · webauthn · MULTI-FACTOR`.)
- [x] 6.2 Per-tenant legal-acceptance state: accepted versions and versions **owed** after a material
      publication, each linking to the archived text at its content hash. (`legal.Service.Read` supplies
      both halves; `Pending` applies the manifest's OWN materiality declaration, so a non-material
      publication creates no obligation and nothing here infers one. The link is to the ARCHIVED text at
      the accepted hash — not the current text, which is a different document and is what a dispute is
      about. Console *🔴 6.2* also asserts an absent legal record does not render as an empty acceptance
      table, which would read as "nobody owes anything".)
- [x] 6.3 Each observability integration as `absent` / `configured` / `degraded`, read from the platform's
      own readiness surface. Assert it is not read from a third party's dashboard and is never a boolean.
      (Tests: *TestAnObservabilityIntegrationHasThreeStatesAndNamesItsFailure* — all three states must be
      reachable, a degraded row must name its failure class, an absent row must not, and every row's
      source is scanned for third-party-dashboard vocabulary; and
      *TestNoReadinessSourceIsNotReportedAsAbsent* — with no readiness surface wired the surface reports
      `integrations_known: false` and invents NO rows, because "nothing is configured" and "we did not
      ask" are different answers.)
- [x] 6.4 Per-tenant deployment shape and version **where derivable**; explicit *unknown* where not.
      Assert no version is inferred or estimated. (Test:
      *TestAnUnknownDeployedVersionIsStatedAndNamesTheMissingCollection* — every unknown row must name
      `a deployment heartbeat carrying the release identifier`, and a row may not report a version and
      an unknown at the same time. Console *🔴 6.4* scans the page for inference vocabulary.)
- [x] 6.5 Payments: specify the webhook and dunning surface and record it `not-yet-readable` in the ledger
      until P21 lands. Assert an empty state is not rendered as a working page or as a zero.
      (`adminops.PaymentsNotYetReadable()`; the ledger's three P21 rows already carried it. Test:
      *TestPaymentsAreStatedAsNotYetReadable* asserts the gap names the webhook AND dunning collections
      and that the statement explicitly refuses a zero. Console *🔴 6.5* asserts the panel renders no
      `<DataTable>` and no `<Num>` at all — there is nothing to count. Browser-verified.)
- [x] 6.6 `/oversight` route + `surfaces.ts` entry + ledger row. (All three. Gated on `audit.read` rather
      than a fourth new capability: operator sessions, consent state and reporting health are the record
      of who did what, which `audit.read` already governs — a new capability would have partitioned
      nothing.)

**Wave 26e also fixed a defect the browser found that no unit test would have:**

- 🔴 **A `nil` Go slice marshals to JSON `null`, and the page crashed reading `.length` off it.** Every
  read model in this phase now emits EMPTY lists rather than nil. The distinction those read models
  actually need — "no records" versus "we did not ask" — is carried by the `…Known` booleans beside
  them, not by a null-versus-empty subtlety a consumer has to know about. Caught on first browser
  open of `/oversight`, which is precisely what task 7.9 exists for.

## 7. Interface floor and acceptance

- [x] 7.1 All four pages: token set only — `npm run scan:tokens` fails on a colour, spacing, type-size or
      radius literal. (`token scan passed: 54 file(s)`. Test *7.1* also asserts the new pages compose from
      the closed primitive set and carry no inline style object, which is how a colour literal would get
      past the scan in the first place.)
- [x] 7.2 English strings with `en-US` pinned through the single swap point. (Test *7.2*, which iterates
      the page list rather than naming four files, so a fifth page picks the rule up automatically. The
      existing floor test's no-CJK and single-`Intl` assertions cover the new files too.)
- [x] 7.3 Keyboard reachability with visible focus; WCAG 2.1 AA in **both** resolved themes.
      **Measured in the browser, per page, per theme** — every text element's computed foreground
      against its resolved background, at the WCAG size/weight thresholds, with hidden tab panels
      unhidden so the figures cover every panel rather than the active one:

      | page | elements checked | dark | light |
      |---|---|---|---|
      | /delivery | 275 | 0 failures | 0 failures |
      | /releases | 173 | 0 failures | 0 failures |
      | /axes | 1712 | 0 failures | 0 failures |
      | /oversight | 127 | 0 failures | 0 failures |

      🔴 **These figures are a RE-MEASUREMENT, and the first set was withdrawn.** The original auditor
      tested for a transparent background with a regex that also matched an opaque colour whose last
      channel was `0`, so it sometimes walked past the real background and compared an ink against the
      wrong surface. It surfaced as five impossible "failures" on the dark theme — a light violet
      reported at 2.25:1 against a near-black ground. The tool was fixed and **all eight combinations
      were measured again from scratch**; the earlier numbers are not reported, because a number
      produced by an instrument later found to be broken is not evidence, whichever way it came out.
      Also measured live: 0 unlabelled inputs, 0 positive tabindex, no horizontal page scroll, and a
      focused tab reports `:focus-visible` with a `solid 3px` outline. A real `ArrowRight` on the
      tablist moves focus, `aria-selected` and the rendered panel together.
- [x] 7.4 Viewport floor: dense subjects grouped with `<Tabs>` rather than laid out below the fold.
      (All four pages; test *7.4* requires at least three panels each. Verified at 1280×800.)
- [x] 7.5 Payload ceiling holds; no charting library arrives for this phase — the existing `chart.tsx`
      primitive is the answer, and the trend ledger's rejections stay rejected. (`bundle scan passed: 38
      client chunk(s), 833055 shipped bytes, 566945 under the 1400000-byte ceiling`. Test *7.5* asserts
      the runtime dependency set is still exactly `next`, `react`, `react-dom`.)
- [x] 7.6 Hazard palette untouched: a read surface does not use danger colour to signal volume or novelty.
      (Test *7.6* enumerates every hazard use on the four pages against a DECLARED allow-list: only a
      failed verification, a failed smoke, a degraded integration and an owed re-acceptance. A closed
      pull request, an unchecked artefact, an unconfigured integration and a refused coverage cell are
      all neutral — they are the commonest rows on their pages, and hazard stays legible by staying rare.)
- [x] 7.7 Failure classes stay three: subsystem-not-mounted, not-found and transport failure are three
      messages, and an empty aggregate renders as *no records*, never as `0`. (Test *7.7*.)
- [x] 7.8 Capability filtering through the same map the backend enforces: a capability the role does not
      grant is absent from the nav **and** absent from the palette. (Test *7.8* asserts each page gates on
      the SAME capability its registry entry names. **Verified live** as Support: the nav showed
      `/`, `/tenants`, `/fleet`, `/delivery` and nothing else, and the command palette offered the same
      four plus tenant subjects — `/releases`, `/axes` and `/oversight` were absent from both.)
- [x] 7.9 **Browser acceptance**, the gate: all four pages, both themes, at the viewport floor, with a
      role that grants each capability **and** a role that does not — the second is how a page that renders
      for everyone is caught. (Done at 1280×800 in Chrome. Grant-all role: all four pages render real
      data from the wired stack. **Denied role (Support):** `/releases` renders *Your role does not grant
      this* naming `release.read` and its holders — "held by Platform-SRE or Superadmin" — rather than an
      empty page or a bare 403. 🔴 This gate caught a real defect no unit test would have: a `nil` Go
      slice marshals to JSON `null`, and `/oversight` crashed reading `.length` off it. Fixed in every
      read model.)
- [x] 7.10 Run the console test suite with **no `next dev` running**: it clobbers `.next`, makes the bundle
      scan refuse to measure, and manufactures a large number of spurious sign-in failures. Known trap in
      this repository. (Every `next dev` was stopped first — including two left running by earlier
      sessions on ports 4310 and 4318 — then `npm run build` and `npm test` were run: **103 passed, 0
      failed, 2 skipped** (the two live-server tests, which skip without `ADMIN_CONSOLE_URL`).)

## 8. Wave 26f — reconciliation and exit

- [x] 8.1 Every capability in `openspec/specs/` resolved in the ledger; the fence green.
      (`ledger scan passed: 56 row(s), 18 destination(s), every capability resolved and both directions
      asserted.` All 34 spec capabilities, all 6 of this change's own, all 16 operator destinations.)
- [x] 8.2 Assert no existing role gained a capability, and no new table was created. (Tests:
      *TestNoExistingRoleWidened*, against a **hand-written** record of the pre-P26 holder sets — deriving
      it from the current code would compare `Capabilities` to itself and pass on any widening; and
      *TestNoNewTableWasCreated*, which fails on any migration above `0019`.
      🔴 **One deliberate exception, recorded rather than buried:** Support gained the NEW `delivery.read`.
      No PRE-EXISTING capability changed hands, which is what the assertion enforces. The grant is the
      whole argument for the capability — Support is the role that was opening impersonation sessions to
      answer delivery questions — and `TestSupportHoldsOnlyReadAndReadImpersonation` was edited
      deliberately and carries the reasoning, so it is a visible edit rather than a quiet one.)
- [x] 8.3 Measure the impersonation ratio: sessions whose recorded reason is a routine lookup an aggregate
      now answers, before and after, from the existing impersonation audit records. Record the number
      whatever it is — this is the phase's headline metric and it must be able to say the phase failed.
      (`internal/adminops/displacement.go` — reads the EXISTING audit chain, no new table and no second
      collection path. Test: *TestTheHeadlineMetricCanReportFailure* drives all four verdicts through the
      real classifier over real audit records: `DISPLACED`, `UNMOVED`, **`WORSE`**, and `NO BASELINE`.
      A metric that can only report success is not a metric.
      🔴 **The number itself cannot be reported yet, and saying so is part of the task.** The
      before/after comparison needs a real operator corpus over a real time window; this repository's
      audit chain holds fixture sessions only. What is delivered is the instrument, its verdict
      vocabulary, and the honesty properties that make the eventual number readable: sessions are
      deduplicated by impersonation id (the command path writes its entry write-ahead, so counting audit
      ROWS would have doubled the figure **in the direction that flatters the phase**), the unclassified
      remainder is always reported beside the ratio, and the displaceable-subject list is asserted to
      stay short so the metric cannot inflate itself by claiming credit for lookups these surfaces do
      not answer.)
- [x] 8.4 Walk the PRD §13 exit checklist end to end and record the evidence per item. (Done in
      `docs/prd/P26-operator-console-refresh.md` §13 — every item now carries the named assertion or the
      measured figure behind it.)
- [x] 8.5 Confirm every fence in this change has been demonstrated red by a deliberate violation.
      Four fences, each demonstrated by committing the real violation and requiring the failure:
      | fence | red demonstration |
      |---|---|
      | the operator-surface ledger | `tests/surface-ledger.test.mjs` — 8 violations: an unresolved capability, a row naming a missing destination, an unjustified destination, a `not-yet-readable` row with no collection, a fourth state, an unattributable absence, a reasonless absence, a ledger with no scope statement |
      | the derived-figure pairing | *🔴 8.5 — the derived-figure fence goes RED…* rewrites the billing page to render `metered_sum.value` bare and requires the catch |
      | coverage parity, both directions | *TestTheParityFenceGoesRedOnADeliberateViolation* drops a cell the engine answers and offers one it does not |
      | the token scan | the pre-existing *the token scan goes RED on an injected literal*, which the new pages are inside the scan path of |
- [x] 8.6 Audit this task list against reality: for each `[x]`, name the assertion and confirm it exists
      and runs. This project has found never-built tasks and lying test runners beneath a fully green
      suite; a pointer is not evidence until it resolves. (Audited **mechanically**, not by reading: every
      `*TestXxx*` cited above was extracted from this file and run by name with `-count=1`. Result:
      **32 cited, 32 exist, 32 PASS, 0 skipped, 0 phantom** — a skip would have been reported as a
      non-pass, which is the failure mode an env-gated test produces. The 9 cited console assertions were
      matched against the test sources the same way. The one cited assertion NOT in that count is the
      Postgres contract test, which is `pgproof`-tagged; it was run separately against a real
      PostgreSQL 16.14 and passed.)
- [x] 8.7 Resolve PRD open questions 1–6, or record them as still open with an owner. In particular, the
      three proposed capabilities (`delivery.read`, `release.read`, `axis.read`) need a decision rather
      than an assumption before waves 26b–26d can start. (All six resolved in
      `docs/prd/P26-operator-console-refresh.md` §14, each with the argument and what was deliberately
      not done. Q1 → Path B, the channel halt stays in the pipeline and remains open as its own change.
      Q2 → not derivable; `/oversight` renders *unknown* and names the heartbeat. Q3/Q4 → three separate
      capabilities, with the Support grant argued explicitly. Q5 → reuse the Audit Log's answer, and
      🔴 flagged as the item most likely to need revisiting, since delivery records grow without bound
      and the fleet read walks every tenant. Q6 → no, and the ledger states that boundary in its own
      first paragraph, asserted by the fence.)


## 9. The run against a real repository

Every phase in this repository proves itself against `github.com/nousresearch/hermes-agent`. P26 ships
no engine — its deliverable is four read-only surfaces and a fence — so the run that matters is:
**point the read models at a real repository's real discovered nodes and a real delivery record, and
check that what an operator would read is true of that repository.**

`cmd/proof/operatorsurfaces` (`make operator-hermes`) does that, and **exits non-zero on any honesty
violation**: a refusal with no cause, a permanent boundary that names an artefact, an observed-merge
claim nobody observed, an inferred deployment version, a key fingerprint long enough to be a blob.

**Run at revision `98105f31f46d`, 26 discovered nodes, over the real checkout:**

```
languages    python (3660 files), typescript (1902 files), javascript (14 files), rust (9 files)

AXES         73 coverage cells apply in this repository's languages, 69 refused
             not-expressible-at-a-call-site      28   whose move: nobody
             call-site-cannot-carry-it           24   whose move: the customer's engineer
             no-materializer-for-this-language   17   whose move: the platform
             what would close the most, FOR THIS REPOSITORY:
               4  a javascript memory module and its call-site rewriter
               4  a rust memory module and its call-site rewriter
               4  a typescript memory module and its call-site rewriter
             parity: 142 cells offered, 142 answered by the engine — equal ✓

DELIVERY     opened at 98105f31f46d → main, credential path `ci`
             merge: UNKNOWN — nobody has observed one, and the surface says so
             audit chain covers 1 merge path, does NOT cover 1 — stated on the surface
             undeliverable: 7 boundary cells (nothing would close them), 3 naming a binding-document field

RELEASES     no publish record wired → the surface says so rather than rendering an empty page
             8 channels, 5 installable today
             heros-release-2026c active (aed4d169c707574d); 2026b and 2026a retired, with dates and reasons

OVERSIGHT    identity: TEST-MODE fixture — the verifier is real, the issuer is not a production IdP
             session adm-superadmin, factor webauthn, multi-factor
             reporting: not read — reported as "we did not ask", not as "nothing is configured"
             deployment version: unknown (not inferred), requires a deployment heartbeat

PASS — every surface's claim about this repository checks out.
```

**What the run establishes that no unit test could.** The axis ranking is a statement about
hermes-agent rather than about the engine: it counts only cells in languages this repository actually
contains, so *a TypeScript memory module would close 4 refusals here* is a fact about a repository
somebody works in. The delivery row is the phase's load-bearing distinction observed on real data — a
pull request opened against a real revision, whose merge nobody has observed, rendering as **unknown**
rather than as the most likely outcome.

**What it does not claim.** Only the *release* and *readiness* sources are unwired in this process, and
both are reported as unread rather than rendered as empty or as zero — which is itself one of the
things the run asserts.
