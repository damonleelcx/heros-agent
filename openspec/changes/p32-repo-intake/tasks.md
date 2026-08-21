# Tasks — P32: Repo Intake

> **Implemented (2026-08-21).** All nine sections. `make ci` green, `make console-test` 680/680,
> `make pg-proof` green for every package this change touches. The live-event acceptance runs against
> real Postgres and real git.
>
> ⚠️ **Two things are carried rather than closed, and both are named in the PRD rather than hidden:**
> a self-hosted local mode (§14 A1 — the console states the limit before the flow starts), and an
> attended-only connection setting (§14 A6 — revocation is the off switch today).
>
> ⚠️ **One deviation from a task's literal wording**, recorded at 2.11: the connection routes are NOT
> published on the public ingress.

## 1. System Designer — the boundary before the code

- [x] 1.1 Answer PRD §14 Q2: forge **App installation** vs pasted **access token**, per forge. An App is revocable on both sides; a pasted token is a plaintext secret the customer hands us.
- [x] 1.2 Answer PRD §14 Q4: the retention window, **as a number**. FR16 defers to "the bundle's rule" and that rule is not written down anywhere in the tree.
- [x] 1.3 Answer PRD §14 Q3: monorepo — whole repository or sub-path.
- [x] 1.4 Answer PRD §14 Q1 with DevOps: local mode against self-hosted, or hosted-only with the limit stated in the UI.
- [x] 1.5 Record that organization-wide scope is refused (ADR-013 option B), so a later phase proposing it is amending an ADR rather than extending this phase.

## 2. Backend Dev — the seam and its second implementation

- [x] 2.1 Extract `TreeGuard` from `bundle.go`: the traversal and size refusals, as a step both implementations run. **`bundle_test.go` must pass unchanged before and after** — it is the characterization suite for this refactor.
- [x] 2.2 Run every `bundle_test.go` refusal case against the clone path as well, from a constructed repository fixture.
- [x] 2.3 `GitSource` implementing `sourceingest.Source`; shallow clone at the requested revision.
- [x] 2.4 `ConnectionStore` — tenant-scoped, one repository per connection, no field able to express a write scope.
- [x] 2.5 Forge adapters for GitHub, GitLab, Bitbucket, each producing the narrowest grant that forge supports.
- [x] 2.6 Refuse at connect any authorization whose resulting grant would cover a repository the customer did not name.
- [x] 2.7 Revocation: delete the grant, enumerate and delete every derived snapshot, and make a subsequent read return `ErrNoSource`.
- [x] 2.8 Append-only `CloneRecord` with `actor` distinguishing person-initiated from scheduled/autonomous.
- [x] 2.9 Four typed failure causes; **no** fallback to an older snapshot on any of them.
- [x] 2.10 One constructor call in `internal/launch` selects the implementation — no branch threaded through the pipeline.
- [x] 2.11 Connection routes on the API, added to the P19 ingress as `Exact` paths. — **routes shipped; ingress publication REFUSED, deviation recorded.** All four are flat so an `Exact` rule is a one-liner if ever needed, but they are classified `ExposureInternal` in `publicroutes.go`: `POST /api/v1/repo-connections` carries the forge credential in its body, no CLI addresses it (the forge redirects to the *console*, which posts inside the cluster), and §7.1 refuses an internet-facing surface for a route nothing calls. Same shape as `/api/v1/device/approve`.
- [x] 2.12 Central event names — `ingest.connection.created`, `.revoked`, `ingest.clone.succeeded`, `.failed` — in the central enum; error codes `UPPER_SNAKE_CASE`; every WARN/ERROR carries `request_id` / `trace_id`.

## 3. Backend Dev — credentials

- [x] 3.1 Add a **forge-read** kind to `internal/broker`'s `Secrets` source, which is provider-scoped today.
- [x] 3.2 Rotation and revocation as lifecycle operations with tests, not runbook steps.
- [x] 3.3 Assert by fence that no forge credential value can appear in a request body, a config file, a log line or an audit record.
- [x] 3.4 Keep the read grant structurally separate from any ADR-005 write installation for the same repository.

## 4. Local mode

- [x] 4.1 Pairing flow between the console and a local agent — **not** a browser file picker (design D5).
- [x] 4.2 Local assessment reads in place; assert by egress capture that no file content, prompt text or diff is transmitted.
- [x] 4.3 State in the console which deployments local mode works against, per the Q1 answer, before the flow starts. — the availability answer is served on `GET /api/v1/local-pairings` and the start handler **refuses to issue a code** on a deployment the bridge cannot reach, so the flow cannot fail at its last step even before the screen ships. The screen itself is §6.5, and `TestLocalModeAvailabilityIsStatedBeforeTheFlow` covers the three answers (pinned / self-hosted / address unknown).

## 5. DevOps

- [x] 5.1 Add forge hosts to the constructed egress allowlist. Cloning is a new egress class and is not implicitly permitted because it is git.
- [x] 5.2 Retention scheduler that runs whether or not anything else does; expose its last successful run on a readable health endpoint.
- [x] 5.3 Escalate a connection that has failed N consecutive times, rather than leaving it at WARN forever.
- [x] 5.4 Clone duration, bytes and failure-cause metrics on the health endpoint — **broken out per forge**.
- [x] 5.5 Verify the 30,500-file benchmark repository ingests within budget on each mode. — `TestTheBenchmarkRepositoryIngestsWithinBudget`: guard 88 ms, archive 565 ms, extract 1777 ms, round trip lossless, entries under the ceiling. ⚠️ The duration is **logged, not asserted** — a wall-clock threshold on CI measures the runner, and the network half is answered from production by the per-forge `DurationMaxMS` on `/readyz`. Mode parity of the extractor is `TestTheThreeModesConvergeOnOneExtractor`.

## 6. Frontend Dev

- [x] 6.1 Connection list per workflow: mode, last successful read, last failure and its cause.
- [x] 6.2 Consent screen stating what the grant permits, that it is usable when the customer is not present, and how to revoke it — displayed before authorization can complete. — a **three**-step flow: name the repository → disclose → authorize. Rendered-browser acceptance caught the two-step version describing a *Bitbucket* grant to somebody who had not chosen a forge; the naming step exists so the disclosure is about the grant actually being created. Enforced at three layers (browser, BFF, platform).
- [x] 6.3 Revoke control in the hazard palette; the confirmation states that derived trees are deleted.
- [x] 6.4 Four failure causes render as four messages.
- [x] 6.5 `not reported` as a rendered state where a workflow has no snapshot; no prompt-to-connect as a precondition.
- [x] 6.6 No colour / spacing / type / radius literals; `scan:tokens` stays green.

## 7. QA — fences that can go red

- [x] 7.1 Escaping symlink in a **cloned** repository → refused. This is the fence most likely to be written only for bundles.
- [x] 7.2 Entry-count / per-file / total-bytes ceilings enforced on the clone path.
- [x] 7.3 Revoke → `SELECT` returns no grant, the derived tree is **absent from disk**, and a read returns `ErrNoSource`.
- [x] 7.4 A grant broader than one repository → refused at connect, per forge.
- [x] 7.5 Rotated credential → cause `credential rejected`, and assert **no** older snapshot was served.
- [x] 7.6 Retention → an expired snapshot is gone and the job's last success is readable from the health endpoint.
- [x] 7.7 Ingest metrics broken out per forge; assert the breakdown exists, because the aggregate is what gets built if nobody checks.
- [x] 7.8 Mode parity: the same tree through all three modes produces an identical IR.
- [x] 7.9 Local mode egress capture shows no repository content.
- [x] 7.10 Live-event acceptance: connect → `SELECT` the connection row → clone → assert files on disk at the expected revision → run discovery → assert nodes. A 200 is not evidence of a write. — run against **real Postgres and real git** in two files (`internal/api/p32_intake_pgproof_test.go`, `internal/sourceingest/p32_acceptance_pgproof_test.go`); split because pointing production code at a test remote would mean adding a seam that lets a caller redirect a clone. It found a real classifier defect: upload-pack's `not our ref` was reported as `network`.
- [x] 7.11 Mode 1 regression: every existing bundle behaviour unchanged.

## 8. Sales Operations + Product Designer

- [x] 8.1 Customer-facing copy: "connect one repository, read-only, revocable — or push a bundle and connect nothing at all." Only claims what shipped.
- [x] 8.2 State the boundary out loud in the consent screen: a connection is usable when you are not present.
- [x] 8.3 Never describe the platform as "watching" a repository; it reads a revision when asked. — a **fence**, not a style note: `TestTheProductNeverDescribesItselfAsWatchingARepository` scans the console copy and the sales doc for subscription verbs next to a repository noun, with a section-scoped exemption so a "what must never be said" table can quote the forbidden sentence.
- [x] 8.4 Confirm Mode 1 has lost no capability — a feature that works only under a connection is a defect, not a tier. — `TestNoFeatureIsGatedOnAConnection`: no package outside the connection's own domain, surface or wiring may name `ErrNoConnection`, `ConnectionStore`, `sourceingest.Connection` or `ModeConnected`. A package that cannot name one cannot gate on one.

## 9. Sign-off

- [x] 9.1 PRD §14 Q1–Q5 answered and folded in.
- [x] 9.2 ADR-013 reviewed against what was actually built, especially the refusal of organization-wide scope.
- [x] 9.3 Security review of the `TreeGuard` extraction, since it moves shipped, load-bearing refusals. — [docs/decisions/p32-treeguard-security-review.md](../../../docs/decisions/p32-treeguard-security-review.md). Verdict: safe to ship. Two messages reworded (neither asserted, both improve the remedy they point at); one new default (`EntryOther` as the zero value) which fails closed; the pax-ordering that a careless version breaks is preserved and pinned.
