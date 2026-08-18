# Tasks — P32: Repo Intake

> **Nothing here is implemented.** Documents only, as the whole GEHA program is.

## 1. System Designer — the boundary before the code

- [ ] 1.1 Answer PRD §14 Q2: forge **App installation** vs pasted **access token**, per forge. An App is revocable on both sides; a pasted token is a plaintext secret the customer hands us.
- [ ] 1.2 Answer PRD §14 Q4: the retention window, **as a number**. FR16 defers to "the bundle's rule" and that rule is not written down anywhere in the tree.
- [ ] 1.3 Answer PRD §14 Q3: monorepo — whole repository or sub-path.
- [ ] 1.4 Answer PRD §14 Q1 with DevOps: local mode against self-hosted, or hosted-only with the limit stated in the UI.
- [ ] 1.5 Record that organization-wide scope is refused (ADR-013 option B), so a later phase proposing it is amending an ADR rather than extending this phase.

## 2. Backend Dev — the seam and its second implementation

- [ ] 2.1 Extract `TreeGuard` from `bundle.go`: the traversal and size refusals, as a step both implementations run. **`bundle_test.go` must pass unchanged before and after** — it is the characterization suite for this refactor.
- [ ] 2.2 Run every `bundle_test.go` refusal case against the clone path as well, from a constructed repository fixture.
- [ ] 2.3 `GitSource` implementing `sourceingest.Source`; shallow clone at the requested revision.
- [ ] 2.4 `ConnectionStore` — tenant-scoped, one repository per connection, no field able to express a write scope.
- [ ] 2.5 Forge adapters for GitHub, GitLab, Bitbucket, each producing the narrowest grant that forge supports.
- [ ] 2.6 Refuse at connect any authorization whose resulting grant would cover a repository the customer did not name.
- [ ] 2.7 Revocation: delete the grant, enumerate and delete every derived snapshot, and make a subsequent read return `ErrNoSource`.
- [ ] 2.8 Append-only `CloneRecord` with `actor` distinguishing person-initiated from scheduled/autonomous.
- [ ] 2.9 Four typed failure causes; **no** fallback to an older snapshot on any of them.
- [ ] 2.10 One constructor call in `internal/launch` selects the implementation — no branch threaded through the pipeline.
- [ ] 2.11 Connection routes on the API, added to the P19 ingress as `Exact` paths.
- [ ] 2.12 Central event names — `ingest.connection.created`, `.revoked`, `ingest.clone.succeeded`, `.failed` — in the central enum; error codes `UPPER_SNAKE_CASE`; every WARN/ERROR carries `request_id` / `trace_id`.

## 3. Backend Dev — credentials

- [ ] 3.1 Add a **forge-read** kind to `internal/broker`'s `Secrets` source, which is provider-scoped today.
- [ ] 3.2 Rotation and revocation as lifecycle operations with tests, not runbook steps.
- [ ] 3.3 Assert by fence that no forge credential value can appear in a request body, a config file, a log line or an audit record.
- [ ] 3.4 Keep the read grant structurally separate from any ADR-005 write installation for the same repository.

## 4. Local mode

- [ ] 4.1 Pairing flow between the console and a local agent — **not** a browser file picker (design D5).
- [ ] 4.2 Local assessment reads in place; assert by egress capture that no file content, prompt text or diff is transmitted.
- [ ] 4.3 State in the console which deployments local mode works against, per the Q1 answer, before the flow starts.

## 5. DevOps

- [ ] 5.1 Add forge hosts to the constructed egress allowlist. Cloning is a new egress class and is not implicitly permitted because it is git.
- [ ] 5.2 Retention scheduler that runs whether or not anything else does; expose its last successful run on a readable health endpoint.
- [ ] 5.3 Escalate a connection that has failed N consecutive times, rather than leaving it at WARN forever.
- [ ] 5.4 Clone duration, bytes and failure-cause metrics on the health endpoint — **broken out per forge**.
- [ ] 5.5 Verify the 30,500-file benchmark repository ingests within budget on each mode.

## 6. Frontend Dev

- [ ] 6.1 Connection list per workflow: mode, last successful read, last failure and its cause.
- [ ] 6.2 Consent screen stating what the grant permits, that it is usable when the customer is not present, and how to revoke it — displayed before authorization can complete.
- [ ] 6.3 Revoke control in the hazard palette; the confirmation states that derived trees are deleted.
- [ ] 6.4 Four failure causes render as four messages.
- [ ] 6.5 `not reported` as a rendered state where a workflow has no snapshot; no prompt-to-connect as a precondition.
- [ ] 6.6 No colour / spacing / type / radius literals; `scan:tokens` stays green.

## 7. QA — fences that can go red

- [ ] 7.1 Escaping symlink in a **cloned** repository → refused. This is the fence most likely to be written only for bundles.
- [ ] 7.2 Entry-count / per-file / total-bytes ceilings enforced on the clone path.
- [ ] 7.3 Revoke → `SELECT` returns no grant, the derived tree is **absent from disk**, and a read returns `ErrNoSource`.
- [ ] 7.4 A grant broader than one repository → refused at connect, per forge.
- [ ] 7.5 Rotated credential → cause `credential rejected`, and assert **no** older snapshot was served.
- [ ] 7.6 Retention → an expired snapshot is gone and the job's last success is readable from the health endpoint.
- [ ] 7.7 Ingest metrics broken out per forge; assert the breakdown exists, because the aggregate is what gets built if nobody checks.
- [ ] 7.8 Mode parity: the same tree through all three modes produces an identical IR.
- [ ] 7.9 Local mode egress capture shows no repository content.
- [ ] 7.10 Live-event acceptance: connect → `SELECT` the connection row → clone → assert files on disk at the expected revision → run discovery → assert nodes. A 200 is not evidence of a write.
- [ ] 7.11 Mode 1 regression: every existing bundle behaviour unchanged.

## 8. Sales Operations + Product Designer

- [ ] 8.1 Customer-facing copy: "connect one repository, read-only, revocable — or push a bundle and connect nothing at all." Only claims what shipped.
- [ ] 8.2 State the boundary out loud in the consent screen: a connection is usable when you are not present.
- [ ] 8.3 Never describe the platform as "watching" a repository; it reads a revision when asked.
- [ ] 8.4 Confirm Mode 1 has lost no capability — a feature that works only under a connection is a defect, not a tier.

## 9. Sign-off

- [ ] 9.1 PRD §14 Q1–Q5 answered and folded in.
- [ ] 9.2 ADR-013 reviewed against what was actually built, especially the refusal of organization-wide scope.
- [ ] 9.3 Security review of the `TreeGuard` extraction, since it moves shipped, load-bearing refusals.
