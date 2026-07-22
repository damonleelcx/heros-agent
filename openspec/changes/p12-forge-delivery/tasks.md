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

- [ ] 1.1 Fix the **pull-request body format**. It becomes a de-facto contract the moment a customer
      builds automation on it (PRD §14 Q3) — decide now whether it carries an explicit version, as
      P11's CLI output does.
- [ ] 1.2 Fix the `delivery` record's schema and key `(config_hash, source_revision, forge_ref)`. This
      key is the join P7's gainshare computation depends on. **One new table**, decided in
      [ADR-005](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md) against two
      alternatives per 🔴 `careful-table-creation` — do not add a second.
- [ ] 1.3 Decide the **merge-observation mechanism** (PRD §14 Q4): webhook, polling, or CI-reported.
      Gainshare timeliness depends on it, and each option has a different failure mode.
- [ ] 1.4 Decide whether `delivery_route` keys on repository or on `(repository, workflow)` (PRD §14
      Q6) — a monorepo may host several workflows with different reviewers.
- [ ] 1.5 Decide **branch naming and stale-branch policy** (PRD §14 Q2). A predictable scheme aids
      idempotency; deletion must never remove something a customer built on.
- [ ] 1.6 Confirm the first-class forge, and whether it must match P11's (PRD §14 Q1).

## 2. Backend — The delivery core (12a)

- [ ] 2.1 Implement `Deliver(proposal, route)` with its preconditions enforced **server-side**:
      gate-passed, entitlement ≥ Team, halt readable and not armed.
- [ ] 2.2 🔴 Enforce that **only a gate-passed change is deliverable**, from **every** entry point. A
      delivery path that could bypass the P5.5 gate would dissolve "verification decides."
- [ ] 2.3 Implement **idempotency** keyed by `(config_hash, source_revision, target)` — re-delivery
      **updates** the existing pull request. Must hold under retries, restarts, and **concurrent**
      attempts; the concurrent case is the one that actually produces duplicates.
- [ ] 2.4 Implement **supersession**: a newer verified proposal closes the older pull request **with the
      reason stated**, so a reviewer is never left with two candidates for one decision.
- [ ] 2.5 Implement the **per-repository open-PR bound**, with reaching it **reported** and the
      undelivered proposal **not discarded**.
- [ ] 2.6 Enforce **never-merge below Autonomous**; under Autonomous merge only a gate-passed change.
- [ ] 2.7 Wire the P6/P8 **halt** to delivery scope, and make an unreadable halt state **fail closed** —
      the kill switch exists for the case where delivery is causing harm.
- [ ] 2.8 Restrict forge writes to **pull requests and their branches** — 🚫 no direct push to a
      protected branch, no tags, no releases, no issues. Scope is easy to add later and impossible to
      take back from an approved installation.
- [ ] 2.9 Isolate failures **per repository**: one customer's forge outage blocks no other, and a failed
      delivery does not lose the proposal.

## 3. Backend + AI Engineer — Pull-request content (12a)

- [ ] 3.1 Render the PR body per §1.1: diff, **verified delta with its confidence interval**, held-out
      status, eval evidence, `config_hash` lineage, and a reference opening the full evidence in the
      console.
- [ ] 3.2 🔴 Render evidence **as computed**. A delta whose interval overlaps the baseline **reads as a
      tie**, never as an improvement — a PR that oversells spends a reviewer's trust once and does not
      get it back.
- [ ] 3.3 Ensure the console reference resolves to a canonical route per P9's rules, so it survives
      being pasted anywhere.
- [ ] 3.4 Test — a tie-valued delta produces a PR that describes a tie; an improvement produces one that
      describes an improvement with its interval.

## 4. Backend — The delivery record (12a)

- [ ] 4.1 Create the **append-only** `delivery` record per §1.2. State changes **append**; 🚫 no
      mutation, no deletion, no interface that expresses either.
- [ ] 4.2 🔴 Leave `transform` **untouched**. Its immutability is what makes `config_hash`
      reproducibility checkable — `TestPG_Immutability_*` must stay green with delivery in use.
- [ ] 4.3 Record the **mode** on every entry, so a later audit can answer which credential path opened a
      given pull request.
- [ ] 4.4 Record a **merge into the target branch** from an **observation** (§1.3) — 🚫 do not infer a
      merge from a pull request closing. A closed-without-merge PR records `closed`, not `merged`.
- [ ] 4.5 Append a **revert** as a further state; the `merged` entry stays. A disputed billed period
      must be answerable from the record.
- [ ] 4.6 Test — history reconstructable in order; no operation mutates or deletes; a closed-without-
      merge PR is never recorded as merged.

## 5. DevOps + Backend — CI-mediated delivery (12a)

- [ ] 5.1 Implement the delivery step running inside [P11](../p11-cli-ci-integration/)'s CI integration
      hook, using the CI environment's **own** ephemeral, repo-scoped forge token.
- [ ] 5.2 🔴 Assert **structurally** that the platform has **no forge-credential store** and no code path
      that reads one in this mode. This is the phase's headline security property and it should be
      provable by absence, not by policy.
- [ ] 5.3 Implement the authenticated fetch by which CI retrieves the verified proposal and its
      evidence, scoped server-side to the caller's tenant and repository.
- [ ] 5.4 Report a **degraded** state when the CI credential is expired or rotated away — 🚫 never a
      silent stop, which reads as "no suggestions this week."
- [ ] 5.5 Test — end to end: verified proposal → CI job → pull request on the customer's repository,
      with no platform-held credential anywhere in the path.

## 6. Product + Frontend — Visibility (12a)

- [ ] 6.1 Implement the **no delivery route** reported state (FR13), rendered as a **condition with a
      next action** — 🚫 not an empty list. Silence here is indistinguishable from "the product found
      nothing," and that confusion lands hardest during evaluation.
- [ ] 6.2 Implement the **degraded / revoked** state the same way.
- [ ] 6.3 Show each delivery's state — **open / merged / closed / superseded** — linked to the proposal
      that produced it, so the loop from proposal to outcome is visible.
- [ ] 6.4 Inherit P9's rules unchanged: token system, English strings, render-as-received, four distinct
      data states, browser-rendered acceptance. 🚫 Do not restate them here.

## 7. QA — 12a acceptance gate

- [ ] 7.1 🔴 **Idempotency under concurrency** — two deliveries for the same
      `(config_hash, source_revision, target)` running concurrently leave **exactly one** pull request.
      A sequential-only test would miss the race that produces duplicates.
- [ ] 7.2 🔴 **Halt fails closed** — make the halt state unreadable and assert delivery does **not**
      occur. If this test cannot be made to fail, the requirement is decoration.
- [ ] 7.3 **Gate integrity** — an unverified change is undeliverable through every entry point.
- [ ] 7.4 **Credential absence** — assert no forge-credential store and no code path reading one in the
      default mode.
- [ ] 7.5 **Volume bound** — a burst cannot exceed the bound; reaching it is reported and the proposal
      is not discarded.
- [ ] 7.6 **Visibility** — route-absence and revocation assert as *renderings*, not just log lines.
- [ ] 7.7 **Merge observation** — a merge records `merged`; a close-without-merge does not; a later
      revert appends a further state.
- [ ] 7.8 **Isolation** — a forge failure for one repository blocks no other and loses no proposal.
- [ ] 7.9 **Entitlement** — delivery below Team refused server-side; auto-merge below Enterprise refused.

---

## 8. Backend + DevOps — Hosted Git App (12b)

- [ ] 8.1 Implement installation and revocation flows: **per-repository** selection, 🚫 never org-wide by
      default.
- [ ] 8.2 Define and document the **least-privilege permission set** — no broader than opening and
      updating pull requests on selected repositories. Broadening it is a **spec change**, not a config
      choice, and the narrowest credible ask is what gets an installation approved.
- [ ] 8.3 Hold the installation token in a secrets manager. 🚫 Never in code, git, a log line, a
      telemetry attribute, a pull-request body, or an artifact; never leaving the platform.
- [ ] 8.4 Make revocation effective **from the customer's side without contacting us**, and report the
      resulting state.
- [ ] 8.5 Test — no credential in any emitted surface on success **and** failure; revocation from the
      forge side stops delivery and is reported.

## 9. QA — 12b parity gate

- [ ] 9.1 🔴 **Content parity** — byte-compare the pull request produced by both modes for one proposal.
      Verified by comparison, not by review; two renderings drift.
- [ ] 9.2 Re-run the full 12a suite against the hosted mode.

## 10. Sales Operations — Claims (12a → 12b)

- [ ] 10.1 Lead with the default posture: **"you grant us nothing — your CI opens the pull request with
      a token it already has."** Repository write access is where source-tooling deals stall, and this
      answers it before it is asked.
- [ ] 10.2 🚫 Present the hosted App **honestly** as real standing write access, contained per-repo,
      least-privilege and revocable — not as equivalent to the default.
- [ ] 10.3 🚫 Do not claim delivery below **Team**, or that the platform merges below **Autonomous**
      (Enterprise). The positioning is *a human merges*; overselling autonomy contradicts the spec and
      the screen at once.
- [ ] 10.4 Frame gainshare on **merges**, not on proposals opened — only merged deltas are billable.

## 11. Documentation

- [ ] 11.1 Fold the two P12 capability specs into `openspec/specs/` when the change deploys.
- [ ] 11.2 Record the §1.1 PR-body format and the §1.3 merge-observation decision as ADRs or referenced
      contracts.
- [ ] 11.3 Update [`p2-config-runtime/tasks.md`](../p2-config-runtime/tasks.md) §3.10 to point at
      ADR-005 — it currently recommends "C now, then B or A behind a new ADR," and that ADR now exists
      and chose neither B nor A.
