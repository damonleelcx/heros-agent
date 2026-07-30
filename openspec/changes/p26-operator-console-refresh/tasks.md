# P26 — Tasks

Ordered by wave. **Wave 26a lands the fence and the three honesty corrections before any new page** — the
pages are this phase's output, the fence is its product, and a phase about operator honesty cannot add
four pages while leaving three known-wrong figures on shipped ones.

Each task is independently verifiable. A completion claim must name an assertion that exists and runs.

## 1. Wave 26a — the fence

- [ ] 1.1 Author `operator-surface-ledger.md` with a row for **every** capability directory in
      `openspec/specs/`, each resolving to `surface: <href>`, `no-operator-surface` (reason + deciding
      phase), or `not-yet-readable` (the collection that would make it readable, named). No fourth state.
- [ ] 1.2 Build fence: a capability in `openspec/specs/` appearing in no ledger row fails the build,
      naming the capability. Demonstrate it red by adding a capability directory with no row.
- [ ] 1.3 Both-directions assertion, forward: every `surface:` row resolves to a destination present in
      `web/admin-console/src/lib/surfaces.ts`. Demonstrate it red with a row pointing at a
      non-existent href.
- [ ] 1.4 Both-directions assertion, reverse: every destination in `surfaces.ts` is named by at least one
      ledger row. Demonstrate it red by adding an unnamed destination.
- [ ] 1.5 Assert that a `not-yet-readable` row **names a collection** — a row with an empty detail fails,
      so the state cannot become a place to park a wish.
- [ ] 1.6 Wire the fence into the operator console's build and the repository's CI, beside the existing
      token, bundle and drift checks.

## 2. Wave 26a — the honesty corrections

- [ ] 2.1 Add link coverage to `internal/adminops/billing.go`'s read model, paired with the figure **in
      the type** (`DerivedFigure.Coverage *float64`) so a figure cannot be rendered without it.
- [ ] 2.2 Render link coverage beside every SUM-derived figure on the operator billing surface, in the
      same view — not behind a link, not in a footnote.
- [ ] 2.3 A figure whose coverage is unknown is **not rendered**; the surface states that coverage is
      unknown instead. Assert it.
- [ ] 2.4 Name the provenance of every gainshare / verified-savings figure where it appears: the P5.5
      verified-delta ledger and nothing else.
- [ ] 2.5 Exclude `unverified` authored changes from every aggregate improvement, savings and quality
      figure, at the query.
- [ ] 2.6 **Prove 2.5 by seeding one.** Create an authored change, leave it unverified, and assert it
      contributes exactly zero to every such aggregate. Asserting the `WHERE` clause exists is asserting
      that we wrote a `WHERE` clause.
- [ ] 2.7 State on the audit surface which merge paths the hash chain covers (P6 autonomous merges, via
      `mergeaudit.go`) and which it does not (P12 customer-CI-mediated deliveries), and link the delivery
      surface for the paths it does cover.
- [ ] 2.8 One named regression test per correction (2.2, 2.6, 2.7), each naming the requirement it
      defends, so a later change that removes it fails with an explanation.
- [ ] 2.9 **Wave gate:** the fence is red-demonstrated four ways (1.2–1.5) and the three corrections have
      named regression tests. No new page exists yet.

## 3. Wave 26b — Delivery (read-only)

- [ ] 3.1 Decide and add the governing capability (proposed `delivery.read`; see PRD open question 3) to
      `adminrbac.Capabilities` with a considered grant per role. The matrix test must pass with it.
- [ ] 3.2 `internal/adminops` delivery read model over `deliveryrecord` + `forgedelivery`, per tenant and
      cross-tenant.
- [ ] 3.3 `MergeState` as three values — `merged` / `closed_unmerged` / `unknown`. Assert a merge is read
      as **observed** and is never derived from a pull request closing.
- [ ] 3.4 Rollout stage from `changedelivery` (ADR-010), with the undeliverable count and its typed causes.
- [ ] 3.5 Admin API read routes behind the granted capability; each writes its audit entry on the **same
      code path** as the read, not from a poller.
- [ ] 3.6 `/delivery` route plus its `surfaces.ts` entry plus its ledger row — three files, or the slot
      silently disappears from the palette while looking fine in the nav.
- [ ] 3.7 Drill-down from every aggregate to the individual records behind it.
- [ ] 3.8 Assert **no** control on this surface opens, closes, retries or merges a delivery.
- [ ] 3.9 Contract test against a real Postgres running the real migration chain. No inline
      `CREATE TABLE` standing in for a production table.

## 4. Wave 26c — Releases & Trust (read-only)

- [ ] 4.1 Decide and add the governing capability (proposed `release.read`; see PRD open question 4).
- [ ] 4.2 Release read model over `internal/distribution`: published versions per channel, artefacts per
      platform.
- [ ] 4.3 Signing-key state: the active key and every retired key with its rotation date and recorded
      reason; identify published artefacts signed with a retired key.
- [ ] 4.4 **Assert no key material on any surface** — identifier and fingerprint only. Assert the surface
      offers no key generation, no export, and no operation whose output is key material. A signing key has
      already leaked once in this project by being emitted into a session transcript.
- [ ] 4.5 `VerifyState` as three values — `verified` / `failed` / `not_yet_verified`.
- [ ] 4.6 `SmokeState` as three values — `passed` / `failed` / **`queued_until_timeout`**. Assert a queued
      run is not rendered as a failure: a retired runner label queues until timeout, and reading that as
      *failed* sends an engineer to debug a build that never ran.
- [ ] 4.7 Show where the publish → verify → smoke sequence **stopped**, not only its final state.
- [ ] 4.8 `/releases` route + `surfaces.ts` entry + ledger row.
- [ ] 4.9 Assert this surface halts, unpublishes and changes nothing (the channel-halt control is PRD open
      question 1, deferred).

## 5. Wave 26d — Axes (read-only)

- [ ] 5.1 Decide and add the governing capability (proposed `axis.read`; see PRD open question 3).
- [ ] 5.2 Per-axis read model: the axis's own declared `EXISTS / PARTIAL / ABSENT` status and fleet
      adoption (tenants and nodes carrying an override).
- [ ] 5.3 Refusal counts keyed by **stable typed cause identifier**, never prose, per axis and per
      language. Keep `not-expressible-at-a-call-site`, `call-site-cannot-carry-it` and
      `no-materializer-for-this-language` distinguishable — they are answered by three different people.
- [ ] 5.4 Rank which artefact would close the most refusals (form row / list splitter / statement resolver
      / registry row / frontend field), so the backlog is ordered by evidence.
- [ ] 5.5 Coverage matrix read from the **one coverage source**. Assert the console computes, caches and
      reformats nothing.
- [ ] 5.6 Parity assertion in **both** directions against the **real engine** (not a fixture): the surface
      offers no cell the engine refuses, and omits no cell the engine materializes.
- [ ] 5.7 An absent row renders as **unknown**, naming what is missing. Assert it is never rendered as
      *not applicable*, which is a claim about the customer's code.
- [ ] 5.8 Assert no coverage gap is presented as a plan boundary — identical on every plan, no tier
      unlocks a cell the engine refuses.
- [ ] 5.9 Assert a refusal count is not rendered with the visual grammar of a ranked result: only P4 ranks,
      and only a P5.5 verified delta is a claim.
- [ ] 5.10 `/axes` route + `surfaces.ts` entry + ledger row, with drill-down to individual refused nodes.

## 6. Wave 26e — Oversight (read-only)

- [ ] 6.1 Show which factor authenticated each operator session, and when. Works against the real verifier
      whether the IdP is the test-mode fixture or a P22 provider; claim no real IdP exists.
- [ ] 6.2 Per-tenant legal-acceptance state: accepted versions and versions **owed** after a material
      publication, each linking to the archived text at its content hash.
- [ ] 6.3 Each observability integration as `absent` / `configured` / `degraded`, read from the platform's
      own readiness surface. Assert it is not read from a third party's dashboard and is never a boolean.
- [ ] 6.4 Per-tenant deployment shape and version **where derivable**; explicit *unknown* where not.
      Assert no version is inferred or estimated.
- [ ] 6.5 Payments: specify the webhook and dunning surface and record it `not-yet-readable` in the ledger
      until P21 lands. Assert an empty state is not rendered as a working page or as a zero.
- [ ] 6.6 `/oversight` route + `surfaces.ts` entry + ledger row.

## 7. Interface floor and acceptance

- [ ] 7.1 All four pages: token set only — `npm run scan:tokens` fails on a colour, spacing, type-size or
      radius literal.
- [ ] 7.2 English strings with `en-US` pinned through the single swap point.
- [ ] 7.3 Keyboard reachability with visible focus; WCAG 2.1 AA in **both** resolved themes.
- [ ] 7.4 Viewport floor: dense subjects grouped with `<Tabs>` rather than laid out below the fold.
- [ ] 7.5 Payload ceiling holds; no charting library arrives for this phase — the existing `chart.tsx`
      primitive is the answer, and the trend ledger's rejections stay rejected.
- [ ] 7.6 Hazard palette untouched: a read surface does not use danger colour to signal volume or novelty.
- [ ] 7.7 Failure classes stay three: subsystem-not-mounted, not-found and transport failure are three
      messages, and an empty aggregate renders as *no records*, never as `0`.
- [ ] 7.8 Capability filtering through the same map the backend enforces: a capability the role does not
      grant is absent from the nav **and** absent from the palette.
- [ ] 7.9 **Browser acceptance**, the gate: all four pages, both themes, at the viewport floor, with a
      role that grants each capability **and** a role that does not — the second is how a page that renders
      for everyone is caught.
- [ ] 7.10 Run the console test suite with **no `next dev` running**: it clobbers `.next`, makes the bundle
      scan refuse to measure, and manufactures a large number of spurious sign-in failures. Known trap in
      this repository.

## 8. Wave 26f — reconciliation and exit

- [ ] 8.1 Every capability in `openspec/specs/` resolved in the ledger; the fence green.
- [ ] 8.2 Assert no existing role gained a capability, and no new table was created.
- [ ] 8.3 Measure the impersonation ratio: sessions whose recorded reason is a routine lookup an aggregate
      now answers, before and after, from the existing impersonation audit records. Record the number
      whatever it is — this is the phase's headline metric and it must be able to say the phase failed.
- [ ] 8.4 Walk the PRD §13 exit checklist end to end and record the evidence per item.
- [ ] 8.5 Confirm every fence in this change has been demonstrated red by a deliberate violation.
- [ ] 8.6 Audit this task list against reality: for each `[x]`, name the assertion and confirm it exists
      and runs. This project has found never-built tasks and lying test runners beneath a fully green
      suite; a pointer is not evidence until it resolves.
- [ ] 8.7 Resolve PRD open questions 1–6, or record them as still open with an owner. In particular, the
      three proposed capabilities (`delivery.read`, `release.read`, `axis.read`) need a decision rather
      than an assumption before waves 26b–26d can start.
