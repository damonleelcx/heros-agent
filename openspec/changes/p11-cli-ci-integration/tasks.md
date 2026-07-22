# Tasks — P11: CLI & CI Integration

Two waves. **Wave 11a** = the CLI and the egress boundary — a complete phase on its own, with no CI
surface and no forge involvement. **Wave 11b** = the CI integration, which is also what
[P12](../p12-forge-delivery/)'s CI-mediated delivery runs inside.

**Standing constraints.** The CLI is **free on every plan** (P7 entitlements). The local workflow works
with **no account and no network**. **Provider credentials never leave the customer environment.** The
linked payload is **constructed from an allowlist, never filtered**. No second telemetry pipeline —
linked events enter the existing P2.5 substrate. The CLI runs the **P4 harness**, not a local
approximation, so the CLI and the platform cannot produce two answers to one question.

---

## 1. Product + System Designer — Fix the contracts before any command ships (11a)

- [ ] 1.1 Ratify the **allowlist** field by field. It becomes a public contract and a security-review
      artifact the moment a customer reads it, so it is decided here rather than discovered in code.
- [ ] 1.2 Decide the **machine output format and its version scheme** (PRD §14). It becomes a public
      contract the moment a customer's pipeline parses it.
- [ ] 1.3 Decide the **exit-code table** — success / configured-gate-failed / operational-error /
      invalid-config — and document it. Three remedies must not share a code.
- [ ] 1.4 Decide **where local cost events live before linking** (PRD §14 Q2): a file the link command
      reads later, or in-memory only. A file makes retroactive linking and CI retries possible; it also
      means cost data sits on disk in the customer's environment, which customers will ask about.
- [ ] 1.5 Decide whether `apply` may ever write to the working tree (PRD §14 Q3). ADR-001 requires
      worktree isolation; strict is safer and developers will ask for the other.
- [ ] 1.6 Decide the **authentication mechanism** for `login` (PRD §14 Q4). CI likely needs a token path
      regardless, so the question is whether device-code also ships at M14.
- [ ] 1.7 Decide the **link-coverage denominator** (PRD §14 Q5) — it requires the CLI to report a run
      count separately from run data, which is itself an egress decision needing the same scrutiny.

## 2. Backend — CLI command set (11a)

- [ ] 2.1 Build `discover` on `internal/discovery`, preserving `cmd/discover`'s existing flags and
      outputs so no current invocation breaks.
- [ ] 2.2 Build `apply` on `internal/transform` + `internal/worktree` — Variant Spec in, **reviewable
      diff** out, applied to an isolated working copy per ADR-001.
- [ ] 2.3 Build `eval` on `internal/evalharness` — multi-seed, intervals, tie rule, disqualifying gates.
      🚫 Do **not** implement a local scorer: two numbers for one question is worse than a slow one.
- [ ] 2.4 Build `login`, `link`, and `status` per §1.6, §3, and §1.1.
- [ ] 2.5 Make **every** command non-interactive and TTY-free; a missing required input exits
      invalid-config **naming the input**, and never prompts.
- [ ] 2.6 Implement config resolution (flags → env → project file → defaults) and make `status` report
      each effective value **and its source** — a config resolving from four places that explains
      nothing is a support-ticket generator.
- [ ] 2.7 Implement the contract-version declaration and the refuse-on-mismatch behavior, naming the
      required version.
- [ ] 2.8 Split streams: machine output on stdout in the §1.2 format, narration on stderr.
- [ ] 2.9 **Test — offline**: run discovery, apply and eval with **networking denied**, not by
      inspecting call sites. A library that resolves DNS on init passes every code review.

## 3. Backend — The egress boundary (11a, the security-critical work)

- [ ] 3.1 Implement the payload as **construction from the allowlist**. 🚫 Do not serialize a run object
      and strip fields — a denylist fails **silently** the first time someone adds a field.
- [ ] 3.2 Implement render-only (`--dry-run`) producing the **exact** payload without transmitting it,
      byte-identical to what a real link sends.
- [ ] 3.3 Require an explicit command **and** an authenticated identity to transmit; ensure no other
      command, and no first-run or background path, transmits anything.
- [ ] 3.4 Make linking **idempotent** keyed by run identity — a retried CI step must not double-count a
      meter that becomes an invoice.
- [ ] 3.5 Transport linked events into the **existing P2.5 substrate** with the standard tag set. 🚫 No
      second ingestion service, no second cost model.
- [ ] 3.6 Attribute a linked run **server-side** to the authenticated identity's tenant; a
      client-supplied tenant identifier must not widen scope.
- [ ] 3.7 Ensure diagnostics, error reporting and elevated verbosity obey the **same** allowlist —
      "verbose sends more" is the classic shape of an accidental leak.
- [ ] 3.8 Make a link failure **non-fatal** to the local result and distinguishable from a run failure.
- [ ] 3.9 Emit a route to the linked run in the console on success.
- [ ] 3.10 🔴 **Test — the guarantee that cannot be read from code**: add a sensitive-looking field to
      the internal run representation, leave the allowlist unchanged, and assert it is **absent** from a
      transmitted payload. If this test cannot be made to fail, the guarantee is decoration.
- [ ] 3.11 Test — no provider credential in any payload, log line, or artifact, on success **and**
      failure; no ambient transmission on first run.

## 4. Backend + AI Engineer — Metering honesty (11a)

- [ ] 4.1 Derive SUM from **linked runs only**. 🚫 Implement no inference, extrapolation, or estimation
      of unlinked spend — *"we inferred the rest"* is not a sentence that belongs in an invoice.
- [ ] 4.2 Build **link coverage** as a read model over the substrate.
- [ ] 4.3 Ensure a linked run's scores and intervals are recorded as computed, so a local eval and a
      hosted run over the same inputs agree.
- [ ] 4.4 Test — a partially-linked customer's spend figure equals the sum of linked runs exactly, and
      no code path adds an estimate.

## 5. Frontend — Console surfaces (11a, in the P9 shell)

- [ ] 5.1 Render linked runs exactly as platform-executed runs — same read models, no second-class
      treatment. A user who links should get the product, not a preview.
- [ ] 5.2 Display **link coverage** wherever a spend figure derived from linked runs appears. It is not
      a footnote: a figure reflecting a fraction of activity, shown without saying so, is what a billing
      dispute is made of.
- [ ] 5.3 Distinguish **complete** coverage from **unknown** coverage — collapsing them loses the
      distinction that matters.
- [ ] 5.4 Resolve the CLI-emitted run reference to a canonical console route per P9's rules, so a URL
      pasted from a terminal into a PR opens exactly that run.

## 6. DevOps — Release and supply chain (11a)

- [ ] 6.1 Build a single self-contained binary per target with **no runtime dependency to install**.
- [ ] 6.2 **Sign and checksum** releases, make builds reproducible, and document the verification step.
      This binary runs inside customer CI with repository access — it is a distribution target, and a
      compromised release is a compromise of every customer's build.
- [ ] 6.3 Define and document the **version support window** against the platform contract.
- [ ] 6.4 Test — the documented verification step succeeds against a real released artifact.

## 7. QA — 11a acceptance gate

- [ ] 7.1 Offline suite with **networking denied** for the full local workflow.
- [ ] 7.2 Egress suite: payload vs allowlist; the §3.10 added-field test; verbosity and diagnostics
      covered; error paths covered.
- [ ] 7.3 Credential suite: none in payloads, logs, or artifacts, on success and failure.
- [ ] 7.4 Exit-code suite: one case per code, asserting discrimination rather than non-zero-ness.
- [ ] 7.5 Determinism: same repo + revision + config → identical IR and `config_hash`.
- [ ] 7.6 Parity: a local `eval` and a hosted run over the same inputs produce the same scores and
      intervals.
- [ ] 7.7 Idempotency: re-linking the same run double-counts nothing.

---

## 8. DevOps + Backend — CI integration (11b)

- [ ] 8.1 Publish a **versioned** action / reusable workflow for the first-class forge (§1 decides
      which), and document the equivalent invocation for the others. 🚫 Not a README snippet — a snippet
      copied into two hundred pipelines cannot be fixed.
- [ ] 8.2 Post a **check** with the run's outcome; upload the IR and run report as **artifacts**.
- [ ] 8.3 🔴 **Never fail the build on platform unavailability** — unreachable, degraded, and slow each
      report and continue, with a **bounded timeout** so a hung platform cannot stall the pipeline
      either. A slow dependency is an outage with extra steps.
- [ ] 8.4 **Fail the build when a customer-configured quality gate fails**, naming the gate. A check
      that never fails is decoration.
- [ ] 8.5 Consume credentials from the CI secret mechanism; assert none reaches logs, check output, or
      **artifacts** — artifacts persist beyond the job and are the easy one to forget.
- [ ] 8.6 Make the integration fully usable **without linking**, publishing nothing.
- [ ] 8.7 Expose the extension point [P12](../p12-forge-delivery/)'s CI-mediated delivery runs through,
      **without** defining PR content, delivery idempotency, or the delivery record here.

## 9. QA — 11b acceptance gate

- [ ] 9.1 Run the CI step with the platform unreachable, slow, and erroring; assert **non-failing** with
      the condition reported in each case.
- [ ] 9.2 Assert a configured gate failing **does** fail the build and names the gate.
- [ ] 9.3 Assert a re-run on the same commit double-counts no meter.
- [ ] 9.4 Assert the no-linking configuration transmits nothing.

## 10. Sales Operations — Claims and the security-review path (11b)

- [ ] 10.1 Write the capability statement with its verifiable clauses: **free on every plan**, **works
      offline with no account**, **never transmits source, prompts, or provider keys**. Each is a
      commitment and each is demonstrable with the dry-run — unusually strong things to be able to say.
- [ ] 10.2 🚫 Never present SUM as total spend. It reflects **linked** runs, and coverage is visible.
- [ ] 10.3 Make the security-review path a **first-class part of the funnel**: offer the dry-run, the
      signed release, and the written allowlist early and unprompted. This product asks to run inside
      CI with repository access; the review is where it lives or dies.
- [ ] 10.4 Do not promise 11b capabilities before they ship.

## 11. Documentation

- [ ] 11.1 Fold the three P11 capability specs into `openspec/specs/` when the change deploys.
- [ ] 11.2 Record the §1.1 allowlist, §1.2 output format, and §1.3 exit codes as referenceable
      contracts, not just prose.
- [ ] 11.3 Update the root [`README.md`](../../../README.md) "Getting started" — its promise that the
      CLI runs discovery/codemod/eval becomes true at 11a and should be stated accurately before then.
