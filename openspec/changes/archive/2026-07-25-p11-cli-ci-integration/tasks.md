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

- [x] 1.1 Ratify the **allowlist** field by field. It becomes a public contract and a security-review
      artifact the moment a customer reads it, so it is decided here rather than discovered in code.
      → `internal/runlink/allowlist.go` (single source of truth) + `docs/decisions/p11-contracts.md` §1.
- [x] 1.2 Decide the **machine output format and its version scheme** (PRD §14). It becomes a public
      contract the moment a customer's pipeline parses it. → `internal/cli/output.go` `Envelope`,
      `OutputContractVersion = "p11.cli.v1"`; contracts doc §2.
- [x] 1.3 Decide the **exit-code table** — success / configured-gate-failed / operational-error /
      invalid-config — and document it. Three remedies must not share a code. → `internal/cli/exit.go`;
      contracts doc §3.
- [x] 1.4 Decide **where local cost events live before linking** (PRD §14 Q2). → On disk, already
      allowlist-shaped, at `.heros/runs/<run_id>.json`; contracts doc Q1/Q2. Enables retroactive
      linking + CI retries; `link` is a pure reader.
- [x] 1.5 Decide whether `apply` may ever write to the working tree (PRD §14 Q3). → No — strict
      worktree isolation always (ADR-001); reviewable diff out, never in-place. Contracts doc Q3.
- [x] 1.6 Decide the **authentication mechanism** for `login` (PRD §14 Q4). → Token path at M14
      (`--token`/`$HEROS_PLATFORM_TOKEN`/stdin, stored `0600`); device-code deferred. Contracts doc Q4.
- [x] 1.7 Decide the **link-coverage denominator** (PRD §14 Q5). → CLI reports `runs_reported`, a single
      allowlisted non-negative integer (a count, never the runs). Contracts doc Q5.

## 2. Backend — CLI command set (11a)

- [x] 2.1 Build `discover` on `internal/discovery`, preserving `cmd/discover`'s existing flags and
      outputs so no current invocation breaks. → `internal/cli/discover.go`, `cmd/heros`. `cmd/discover`
      left untouched.
- [x] 2.2 Build `apply` on `internal/transform` + `internal/worktree` — Variant Spec in, **reviewable
      diff** out, applied to an isolated working copy per ADR-001. → `internal/cli/apply.go` (worktree
      isolation, temp-copy fallback; working tree never modified in place).
- [x] 2.3 Build `eval` on `internal/evalharness` — multi-seed, intervals, tie rule, disqualifying gates.
      🚫 No local scorer: intervals come from `evalstats.Aggregate` (the platform's bootstrap); the node
      runtime is the only pluggable seam. → `internal/cli/eval.go`, `reference_runtime.go`.
- [x] 2.4 Build `login`, `link`, `status`, **and `version`** per §1.6, §3, and §1.1. →
      `internal/clilink/clilink.go` (login/link), `internal/cli/status.go` (status + version).
- [x] 2.5 Make **every** command non-interactive and TTY-free; a missing required input exits
      invalid-config **naming the input**, and never prompts. → `cfg.Require` names the input;
      `TestExitCodesDiscriminate`.
- [x] 2.6 Implement config resolution (flags → env → project file → defaults) and make `status` report
      each effective value **and its source**. → `internal/cli/config.go`, `TestConfigProvenance`.
- [x] 2.7 Implement the contract-version declaration and the refuse-on-mismatch behavior, naming the
      required version. → `internal/cli/version.go` `CheckContract`; the link client sends
      `X-Heros-Contract` and refuses on a 426/mismatch.
- [x] 2.8 Split streams: machine output on stdout in the §1.2 format, narration on stderr. →
      `internal/cli/output.go`; `TestStdoutIsMachineOnly`.
- [x] 2.9 **Test — offline**: run discovery, apply and eval with **networking denied**, not by
      inspecting call sites. → `TestLocalWorkflowRunsWithNetworkingDenied` installs a failing dialer and
      runs all three; plus `TestOfflineNoNetworkImports` (structural).

## 3. Backend — The egress boundary (11a, the security-critical work)

- [x] 3.1 Implement the payload as **construction from the allowlist**. → `runlink.BuildPayload` copies
      named fields into a fresh `Payload`; never serializes `RunRecord`.
- [x] 3.2 Implement render-only (`--dry-run`) producing the **exact** payload without transmitting it,
      byte-identical to what a real link sends. → `clilink.Link` dry-run; `TestDryRunIsByteIdenticalToSend`.
- [x] 3.3 Require an explicit command **and** an authenticated identity to transmit; ensure no other
      command, and no first-run or background path, transmits anything. → `TestOnlyLinkTransmits`;
      `link` requires `cli.LoadCredential`.
- [x] 3.4 Make linking **idempotent** keyed by run identity. → `linkingest` records run id; re-link →
      409 already_linked; `TestIdempotentLinking`, `TestP11IngestIdempotent`.
- [x] 3.5 Transport linked events into the **existing P2.5 substrate** with the standard tag set. →
      `linkingest` emits `metricevent.Event` (seven tags) into the `metering` cost-event substrate; no
      second store or cost model.
- [x] 3.6 Attribute a linked run **server-side** to the authenticated identity's tenant. → tenant taken
      from `auth.Principal`; payload has no tenant field; `TestServerSideTenantAttribution`,
      `TestP11IngestAttributesServerSide`.
- [x] 3.7 Ensure diagnostics, error reporting and elevated verbosity obey the **same** allowlist. → no
      verbose path adds fields; `clilink.Link` runs `runlink.AssertAllowlisted` at the boundary before
      transmit; the machine `Error` field is content-free.
- [x] 3.8 Make a link failure **non-fatal** to the local result and distinguishable from a run failure.
      → `TestLinkFailureIsNonFatalToLocalResult` (run record survives; failure is operational, distinct).
- [x] 3.9 Emit a route to the linked run in the console on success. → `run_url` in `LinkData`; narration
      prints it; server builds P9's canonical route.
- [x] 3.10 🔴 **Test — the guarantee that cannot be read from code**. → `LocalNote` canary on `RunRecord`
      + `TestFR11_AddedFieldIsAbsent` (asserts the sensitive source field is absent from the payload).
- [x] 3.11 Test — no provider credential in any payload, log line, or artifact; no ambient transmission.
      → `TestTokenNeverInPayloadBody`, `TestNoCredentialFieldExists`, `TestOnlyLinkTransmits`.

## 4. Backend + AI Engineer — Metering honesty (11a)

- [x] 4.1 Derive SUM from **linked runs only**. → `metering.DeriveSUM` sums observed events only; the
      ingester only lands linked runs; `TestNoExtrapolationPath` (1 of 100 linked → SUM is exactly the
      one, never inflated).
- [x] 4.2 Build **link coverage** as a read model over the substrate. → `linkingest.LinkCoverage` /
      `MemStore.Coverage` (runs_linked / runs_reported, known vs complete).
- [x] 4.3 Ensure a linked run's scores and intervals are recorded as computed. → `LinkedRun.Scores`
      recorded from the payload; `TestScoresRecordedAsComputed`. The CLI already uses `evalstats`, so the
      numbers are the platform's own.
- [x] 4.4 Test — a partially-linked customer's spend figure equals the sum of linked runs exactly. →
      `TestPartialCoverageSpendIsExact` (2 of 4 linked → SUM = exactly 2×cost).

## 5. Frontend — Console surfaces (11a, in the P9 shell)

- [x] 5.1 Render linked runs exactly as platform-executed runs — same read models, no second-class
      treatment. → Linked events land in the SAME P2.5 substrate with the standard tag set, so every
      aggregate the console derives (SUM, board spend, coverage) reads them through the same derivation
      as a hosted run; `linkingest` test "indistinguishable in kind". Content (prompts/traces) is
      absent by design (the boundary), not by second-class treatment.
- [x] 5.2 Display **link coverage** wherever a spend figure derived from linked runs appears. →
      `web/console/src/components/linkCoverage.tsx` rendered beside SUM on the account view
      (`.../account/page.tsx`); verified rendering "3 of 10 reported runs (30%) … never estimated"
      through the real BFF+SSR.
- [x] 5.3 Distinguish **complete** coverage from **unknown** coverage. → three states (complete /
      partial / unknown), each with distinct copy and colour; `tests/link-coverage.test.mjs` asserts all
      three; unknown never renders as full.
- [x] 5.4 Resolve the CLI-emitted run reference to a canonical console route per P9's rules. → the
      ingester emits `https://heros-agent.space/app/runs/<run_id>`, which the existing
      `/app/runs/[runId]` route (`routes.run`) resolves — a URL pasted from a terminal opens that run.

## 6. DevOps — Release and supply chain (11a)

- [x] 6.1 Build a single self-contained binary per target with **no runtime dependency to install**. →
      `scripts/release-cli.sh` builds `cmd/heros` per target (native runner, CGO tree-sitter); `otool`
      confirms it links only OS system libs — no heros dependency to install.
- [x] 6.2 **Sign and checksum** releases, make builds reproducible, and document the verification step.
      → `SHA256SUMS` + ed25519 `SHA256SUMS.sig` (`cmd/herossign`, `internal/release`); published key
      `docs/release/heros-release.pub`; reproducible via `-trimpath -buildvcs=false`
      (`TestReproducibleBuild` asserts byte-identical); documented in `docs/release/cli-verification.md`.
- [x] 6.3 Define and document the **version support window** against the platform contract. →
      `docs/release/cli-verification.md` "Version support window"; `heros version` reports the contract;
      out-of-window refuses (`cli.CheckContract`, ingest 426).
- [x] 6.4 Test — the documented verification step succeeds against a real released artifact. →
      `TestDocumentedVerificationSucceeds` builds a real `heros`, writes+verifies the manifest, signs +
      verifies, and asserts a tampered binary FAILS. Ran `scripts/release-cli.sh` end-to-end: sign +
      `herossign verify` + `shasum -c` all OK.

## 7. QA — 11a acceptance gate

- [x] 7.1 Offline suite with **networking denied** for the full local workflow. →
      `TestLocalWorkflowRunsWithNetworkingDenied` (failing dialer; discover+eval+apply all pass).
- [x] 7.2 Egress suite: payload vs allowlist; the §3.10 added-field test; verbosity/diagnostics; error
      paths. → `runlink/egress_test.go` (`TestFR11_AddedFieldIsAbsent`, `TestOnlyAllowlistedKeysCross`),
      `clilink/link_test.go` (dry-run, failure paths); verbosity has no field-widening path.
- [x] 7.3 Credential suite: none in payloads, logs, or artifacts, on success and failure. →
      `TestTokenNeverInPayloadBody`, `TestNoCredentialFieldExists`, `TestLinkFailureIsNonFatalToLocalResult`.
- [x] 7.4 Exit-code suite: one case per code, asserting discrimination. → `TestExitCodesDiscriminate`
      (0/3) + `TestExitCode_GateFailedIsDistinct` (1 gate vs 0 pass) + link-failure (2 operational).
- [x] 7.5 Determinism: same repo + revision + config → identical IR and `config_hash`. →
      `TestDeterministicIR` (byte-identical IR) + `TestEvalDeterminismAndRecord` (config_hash stable).
- [x] 7.6 Parity: a local `eval` and a hosted run over the same inputs produce the same scores and
      intervals. → one implementation (`evalstats`); `TestParity_RecordedScoresEqualEvalScores` +
      `linkingest.TestScoresRecordedAsComputed` (recorded == computed).
- [x] 7.7 Idempotency: re-linking the same run double-counts nothing. → `TestIdempotentLinking`,
      `TestP11IngestIdempotent`.

---

## 8. DevOps + Backend — CI integration (11b)

- [x] 8.1 Publish a **versioned** action / reusable workflow for GitHub (first-class, §1 Q1); document
      the equivalent for others. → `.github/actions/heros/action.yml` (composite, `@v0`) +
      `.github/workflows/heros-eval.yml` (reusable). GitLab/Bitbucket documented as the same
      forge-agnostic binary invocation (contracts doc Q1).
- [x] 8.2 Post a **check** with the run's outcome; upload the IR and run report as **artifacts**. →
      action.yml `actions/github-script` posts `heros/eval` check; `actions/upload-artifact` uploads
      ir.json/discovery-report.json/eval.json (NOT the log).
- [x] 8.3 🔴 **Never fail the build on platform unavailability**, bounded timeout. →
      `scripts/ci/heros-ci.sh` `run_bounded` + `report_platform`; `TestPlatformUnreachableDoesNotFailBuild`,
      `TestPlatformSlowDoesNotStallOrFail` (2s bound).
- [x] 8.4 **Fail the build when a customer-configured quality gate fails**, naming the gate. →
      eval exit 1 → build fails naming the gate; `TestGateFailureFailsBuildAndNames`.
- [x] 8.5 Consume credentials from the CI secret mechanism; assert none reaches logs, check output, or
      artifacts. → token via env, never echoed; `heros.log` excluded from artifacts;
      `TestCredentialNeverInEmittedSurfaces`.
- [x] 8.6 Make the integration fully usable **without linking**, publishing nothing. →
      no token → link steps skipped; `TestNoLinkingTransmitsNothing`.
- [x] 8.7 Expose the extension point P12's CI-mediated delivery runs through. → `HEROS_DELIVERY_HOOK`
      invoked with the run id, defining nothing about delivery; `TestP12DeliveryHookInvoked`.

## 9. QA — 11b acceptance gate

- [x] 9.1 Run the CI step with the platform unreachable, slow, and erroring; assert **non-failing** with
      the condition reported. → `TestPlatformUnreachableDoesNotFailBuild`, `TestPlatformSlowDoesNotStallOrFail`.
- [x] 9.2 Assert a configured gate failing **does** fail the build and names the gate. →
      `TestGateFailureFailsBuildAndNames`.
- [x] 9.3 Assert a re-run on the same commit double-counts no meter. → idempotency is keyed by run
      identity server-side: `TestP11IngestIdempotent`, `TestIdempotentLinking` (a CI re-run re-links the
      same run_id → 409 already_linked, SUM unchanged).
- [x] 9.4 Assert the no-linking configuration transmits nothing. → `TestNoLinkingTransmitsNothing`.

## 10. Sales Operations — Claims and the security-review path (11b)

- [x] 10.1 Write the capability statement with its verifiable clauses. →
      `docs/sales/P11-cli-ci-integration-claims.md` §10.1 (free / offline / never-transmits, each
      demonstrable with the dry-run).
- [x] 10.2 🚫 Never present SUM as total spend. → §10.2 (SUM = linked runs; coverage visible;
      "we inferred the rest" is banned).
- [x] 10.3 Make the security-review path a **first-class part of the funnel**. → §10.3 (dry-run, written
      allowlist, signed release, endpoint pin, build-safety — offered early and unprompted).
- [x] 10.4 Do not promise 11b capabilities before they ship. → §10.4 (CI is 11b; PRs are P12).

## 11. Documentation

- [x] 11.1 Fold the three P11 capability specs into `openspec/specs/`. → `openspec/specs/{cli,
      run-linking,ci-integration}/spec.md` (ADDED → Requirements, paths corrected).
- [x] 11.2 Record the §1.1 allowlist, §1.2 output format, and §1.3 exit codes as referenceable
      contracts. → `docs/decisions/p11-contracts.md`, each with a single machine source of truth in Go.
- [x] 11.3 Update the root [`README.md`](../../../README.md) "Getting started". → added a "The CLI
      (`heros`)" section (discover/apply/eval/status/version + opt-in link/dry-run, exit codes, the
      endpoint pin), so the CLI's promise is now stated accurately and truthfully.
