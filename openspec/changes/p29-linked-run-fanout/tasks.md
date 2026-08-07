# P29 — Tasks

Ordered by wave. A wave lands whole: its fences are written first, **observed red**, and only then made
green. A task is done when the artifact it names exists and the check it names fails without it.

Standing rules for this change:

- 🔴 **No task is complete on a green build alone.** The acceptance is a rendered browser page carrying
  this organization's own data, or a `curl` against a deployed cluster — a passing type-check is
  compatible with a page that renders nothing.
- 🔴 **Every fence in this change is verified red before it is verified green**, and how it was broken is
  recorded in the task. A fence nobody has seen fail is a fence nobody has tested.
- 🚫 No surface may state a fact about a node the platform was not sent.

---

## 1. Wave A — the edge (DevOps + Backend). Nothing downstream is observable until this lands.

- [x] 1.1 Delete the `strings.HasSuffix(path, "/") { continue }` exemption in
      `internal/api/ingress_fence_test.go`. Observe the fence go **red** naming
      `/api/v1/workflows/` and `/api/v1/proposals/`. Record the failure output in the task.

      🔴 **The exemption was not what hid them.** Deleting it alone changed nothing, and would have
      looked like the defect was closed. `transportPaths` matched `bin.Y.(*ast.BasicLit)` — a STRING
      LITERAL — and every path that matters is written `c.base + runlink.SomePath`, an identifier. The
      scan found four paths (`whoami`, `device/authorize`, `device/token`, `auth/password/signin`),
      all four of them the inline-written ones, and found none of `run-links`, `/api/v1/workflows/` or
      `/api/v1/proposals/`. So `runlinkStringConsts` was added to resolve `runlink.*` constants, and the
      vacuity guard was raised 3 → 6. Only then did deleting the exemption produce:

      ```
      --- FAIL: TestEveryPathTheCLIAddressesIsPublished
        the CLI addresses /api/v1/proposals/, which has a caller-supplied segment below it.
          An Exact ingress rule cannot match it, and a Prefix rule would publish every route beneath
          it. Publish it Exact, which means giving it a FLAT shape: move the identifier into the
          request payload and address a path with no variable segment.
        the CLI addresses /api/v1/workflows/, which has a caller-supplied segment below it. …
      ```
- [x] 1.2 Add flat routes beside the parameterised ones in `internal/api`: `POST /api/v1/workflow-ir`,
      `PUT /api/v1/workflow-source`, `DELETE /api/v1/workflow-source`,
      `POST /api/v1/proposal-verdicts`. Identifiers move into the payload; a request whose path and
      payload disagree is refused rather than resolved by precedence.

      A **fifth** machine-addressed path was found doing this and it had never been counted:
      `RunDiscovery` built its URL as `<local variable> + "/discover"`, so no scan could see it and no
      manifest published it. It is now `POST /api/v1/workflow-source-discovery`, addressed as
      `c.base + runlink.SourceDiscoveryPath`. — `internal/api/{runlinking,sourcepush,verdictingest}.go`

      `workflow-source` carries its identifiers in `X-Heros-Workflow-Id` / `X-Heros-Source-Revision`
      headers, because its body is an opaque gzip archive with no field to move them into and
      `IsLinkTarget` refuses a query string. Disagreement between path and header is refused, never
      resolved by precedence.
- [x] 1.3 Classify the four flat routes `ExposurePublic` and the four parameterised ones
      `ExposureInternal` in `internal/api/publicroutes.go`. (Five and four respectively, with the
      discovery route above.)
- [x] 1.4 Point `internal/runlink/transport` at the flat paths; update `runlink.PlatformPaths`,
      `WorkflowIRPath`, `VerdictPath` and the source-push URL construction. Assert the transport scan
      finds them (the scan's own minimum-count guard must still hold — raised 3 → 6).

      `PlatformPaths` lost both trailing-slash PREFIX entries: it had been permitting everything under
      `/api/v1/workflows/` and `/api/v1/proposals/`, a much wider pin than its own comment claimed.
- [x] 1.5 Add four `Exact` rules to `deploy/k8s/overlays/prod/ingress.yaml` with the comment block the
      file's convention requires — what each carries, why it is safe to publish, and why it is not a
      prefix. The fence named exactly these four and no others.
- [x] 1.6 Extend the fence with the **prefix-consequence** assertion: for any path a prefix rule would
      be needed to publish, the fence names the other registered routes that rule would publish.
      Verify it red by adding a `Prefix` rule for `/api/v1/workflows/`.

      Red output listed **15** routes by name — `commit`, `eval-board`, `ir`, `orderings`,
      `orderings/stream`, `pattern-graph`, `proposals`, `proposals/compile`, `proposals/generate`,
      `open-pr`, `source/{rev}`, `source/{rev}/discover`, `validate`, `{id}/bindings`, `{id}/nodes` —
      "…and every route added under that prefix from now on, by default, forever."
- [x] 1.7 Extend the fence to assert the parameterised routes are **registered and never published**.
      Verify it red by publishing one. Red by adding an Exact rule for
      `/api/v1/workflows/{workflow_id}/ir`: *"It must never be published: the flat replacement carries
      the same traffic and can be matched Exact."*
- [x] 1.8 Make the CLI distinguish an edge 404 from a platform refusal: a response that did not come
      from a platform handler reports "not reachable at this endpoint" and names one next action.
      Fence: a test serving a bare 404 at the flat path asserts the message.
      — `internal/runlink/transport/edge404{,_test}.go`, wired into all six machine transports.

      Verified red by removing the `edge404` call from `ReportVerdict`: the old message was *"the
      platform has no proposal p for this identity — check the id, and that the token belongs to the
      tenant"*, which is three wrong investigations offered for a deployment gap. The reverse case is
      fenced too — a platform-WRITTEN 404 still reports the platform's own refusal.
- [x] 1.9 Compose-substrate parity: the same four paths on the Compose deployment's reverse proxy, and
      a fence that reads both substrates from one list.

      🔴 **The gap was far larger than four paths.** `deploy/scripts/bootstrap-vm.sh` published exactly
      ONE agentd path (`/billing/webhook`) and sent everything else to the console — so on every box
      our own bootstrap script produces, `heros login`, `heros link`, the device pair and password
      sign-in all answered 404 from Next.js. The Caddyfile now takes its list from
      `PLATFORM_PUBLIC_PATHS`, and `TestBothSubstratesPublishExactlyTheDeclaredPublicRoutes` reads both
      substrates against `api.PublicRoutes()`. Verified red by shrinking the list back to
      `/billing/webhook`: nine named failures.
- [x] 1.10 🔴 **Live proof, not a manifest diff.** Against a deployed cluster: `heros link --with-ir`
      and `heros push-source` return 2xx. Record the status line and the run URL.

      Deployment: `make deploy-up` from this checkout (agentd rebuilt), plus a Caddy front door built
      from the Caddyfile shape `bootstrap-vm.sh` generates — so the EDGE is exercised, not bypassed.
      Driven through the shipped command path (`cmd/heroslocallink`, extended with `-push-source`).

      | request | direct to agentd | through the edge |
      |---|---|---|
      | `POST /api/v1/workflow-ir` | **201** | **201** |
      | `PUT /api/v1/workflow-source` | **202** | **202** |
      | `POST /api/v1/proposal-verdicts` | **404** *(platform: "no such proposal")* | **404** *(platform)* |
      | `POST /api/v1/workflow-source-discovery` | **500** *(platform, see finding)* | — |
      | `POST /api/v1/workflows/{id}/ir` (old shape) | 200 in-cluster | **404 CONSOLE-FALLTHROUGH** ✅ |

      `heros link --with-ir` on the real hermes-agent run: `run_url`
      `https://heros-agent.space/app/runs/run-d88b04b8133b`, `workflow_ir.sent: true`, 27 nodes,
      `graph_url https://heros-agent.space/app/workflows/hermes-agent/graph`.
      `heros push-source`: 8200 files, 137.0 MiB uncompressed (59.9 MiB compressed), stored **202**.

      🔴 The 1.8 message was proved on the real edge, not only in a unit test: removing
      `/api/v1/workflow-ir` from the Caddyfile reproduced the exact production failure, and the CLI
      said *"link: the run linked, but the structure did NOT (/api/v1/workflow-ir is not reachable at
      this endpoint … this is a deployment gap, not a problem with your workflow, your token or your
      id. Next: ask whoever operates … to publish /api/v1/workflow-ir)"*.

      ⚠️ **Finding, reported not fixed** (`scope-fidelity`): platform-side discovery over a pushed
      snapshot returns 500 for **any** repository — `sourceingest: entry pax_global_header has
      unsupported type "g"`. `git archive` writes that entry for every repo with a commit, so
      `POST /api/v1/workflow-source-discovery` cannot succeed today. It is a `sourceingest` defect, it
      predates this change, and the 500 is a *platform* answer — which is itself the evidence the flat
      route is reachable. Recommend a separate fix; it is not in P29's scope and it does not block
      §8.3, whose walk uses local `heros discover`.

## 2. Wave B — the payload (Backend + AI Engineer + Product)

- [x] 2.1 Add `language` and `axis_verdicts` to `runlink.WireIRNode`, and `coverage_version` to
      `WorkflowIRPayload`. Add the corresponding rows to `WorkflowIRAllowlist` with the per-field
      justification the file's convention requires. All three are `omitempty`, which is what makes 2.6
      true rather than approximately true.
- [x] 2.2 Allowlist fence: assert every new field is an identifier, a count or a member of a closed set,
      and that no new field can carry repository-originating free text. Verify it red by adding a
      `string` field with no closed set. — `internal/runlink/optinfields_test.go`, a STRUCTURAL walk of
      the Go types rather than a grep of field names, because the realistic mistake is a field called
      `detail` (the engine has exactly that field) and no name-based check would object.

      Verified red with `Detail string` on `WireAxisVerdict` — caught by all three checks at once:
      *"is a string with no closed set and is not a declared identifier"*, *"not on its allowlist"*, and
      *"whose name says content"*.
- [x] 2.3 Compute per-node axis verdicts in the CLI from `internal/transform`'s real engine against real
      source — not from a table lookup on `(axis, language, form)`. Fence: a fixture whose language and
      form are covered but whose call site refuses for its own shape must transmit `refused` with the
      call-site cause. — new `internal/nodeaxis`, fixture `testdata/pyrepo`.

      The fixture is two `client.chat.completions.create` calls: identical language, provider, SDK
      method, registry row and FORM. `plain` writes its arguments; `unpacked` splats `**opts`. The engine
      answers `applies` and `refused / call-site-cannot-carry-it`. A coverage lookup cannot tell them
      apart and would say `applies` for both.

      🔴 **A fabricated refusal was found and fixed while building this, and it is the reason there is
      now an anti-fabrication fence.** The first skills probe declared `{"type":"object"}` with no
      `properties`, and the engine answered *"skill \"probe\": the sealed input schema declares no
      `properties`"* — `refused / call-site-cannot-carry-it` at a call site that accepts a real skill
      perfectly well. It would have shipped as "your code cannot carry skills" on a paid surface, and it
      was invisible to every other test: the verdict was well-formed, closed-set, coverage-consistent
      and wrong. `TestNoRefusalIsAboutTheProbeItself` now fails when the engine's own sentence names the
      probe.

      🔴 **Fail-safe direction, stated:** only `ErrUnsafeRewrite` with a valid cause becomes a verdict.
      A missing engine, an unindexable tree, an unbuildable probe and a rewriter that produced no edit
      are all ABSENCE, which renders `not-reported`. Wiring is deliberately not probed — its scope is a
      set of edges, so a per-node wiring verdict would invent a grain the engine cannot answer at.
- [x] 2.4 Fence: the per-node verdicts transmitted equal what `heros coverage` reports locally for the
      same repository. A divergence fails.
      — `TestTransmittedVerdictsAgreeWithTheLocalCoverageTable`.

      ⚠️ The relation had to be stated precisely, and getting it wrong once was instructive. The first
      version required every refusal's cause to appear in the table for that (axis, language) — and went
      red on `memory / python`, where the table says APPLIES and both call sites refuse. That is not a
      drift: `call-site-cannot-carry-it` is **by definition invisible to the table**, which answers
      (axis, language, form) and has never read anyone's source. So that class is exempt, and the other
      two — which ARE language-level facts the table owns — must match. An exemption that covered all
      three would have made the fence vacuous.
- [x] 2.5 `--with-ir` with no value discovers in-process from `--repo`; the path form is retained. Fence:
      both forms produce byte-identical payloads for the same repository.

      Go's `flag` cannot express an optional value: a `String` flag makes the bare form an error, and a
      bool-like flag breaks `--with-ir <path>`. Taking the space form away is not an option
      (`UI 改版不得丢失既有功能` applies to a command line too), so the flag is bool-like and
      `joinOptionalValueFlags` re-attaches a following non-flag argument before parsing. Both spellings
      reach one field. The bare form resolves to a NAMED sentinel, not `""`, because `""` already means
      "not given" everywhere in that package and the collapse would turn an opt-in into a silent no-op.
- [x] 2.6 Fence: a link with **no** opt-in produces a payload byte-identical to the pre-change one.
      This is the promise the whole boundary rests on and it is asserted, not assumed.
      — `TestADefaultLinkIsByteIdenticalToThePreChangePayload`: the pre-change key set is spelled out
      (a test that recomputes its expectation from the code under test cannot fail), every field this
      change added is searched for at any depth, and the transmission COUNT is asserted to be exactly
      one.
- [x] 2.7 `--dry-run` renders all three payloads with full fidelity; fence asserts rendered bytes equal
      transmitted bytes for each. — link renders payload + structure
      (`TestDryRunRendersTheExactStructureBytesThatAreTransmitted`, comparing canonicalised values so a
      dropped field cannot hide behind whitespace); `apply --link-receipt --dry-run` renders the receipt
      from the same value the transport marshals and transmits nothing.
- [x] 2.8 Transform receipt: `runlink.TransformReceipt` (config hash, source revision, workflow id,
      per-node outcome + cause, files changed, lines added, lines removed, status, coverage version)
      with its own allowlist and its own contract version. Fence: no field can hold a diff, and the
      receipt builder reads named fields into a fresh struct.
      — `internal/runlink/transformreceipt.go`, `internal/cli/transformreceipt.go`. `diffstat` walks the
      diff to COUNT and returns three integers, holding no line and — deliberately — no PATH.
      A REFUSED transform gets a receipt too (`BuildRefusedTransformReceipt`), because
      `/app/transforms/…` saying "the engine declined this node, and here is the class" is an answer,
      where an empty surface is indistinguishable from never having tried.
- [x] 2.9 `heros apply` gains the named opt-in that transmits a receipt. Absent the flag, nothing is
      transmitted; fence asserts it. — `TestApplyTransmitsNothingWithoutTheNamedOptIn`, which also
      asserts that WITH the flag there is exactly one transmission, to the receipt path, carrying no
      diff marker.

      🔴 The endpoint pin caught a real omission here: the first run refused with *"refusing to transmit:
      https://heros-agent.space/api/v1/transform-receipts is not the pinned link endpoint"* because
      `TransformReceiptPath` was not on `runlink.PlatformPaths`. The guard is not decoration.
- [x] 2.10 Link success output names the surfaces filled and, for each surface not filled, the one
      option that would fill it. Fence: the surface list is derived from the capability registry, not
      hand-written, so a new surface cannot be omitted silently.
      — `internal/cli/linksurfaces.go`. Derivation is enforced against the console's OWN route directory
      (`web/console/src/app/app/*`): a page added there fails
      `TestEveryConsoleSurfaceIsAccountedForInTheLinkReport` until somebody records a decision for it,
      including the decision "this is not a data surface".
- [x] 2.11 Egress fence extension: run the full opt-in path at highest verbosity with diagnostics on and
      assert no prompt text, source line, diff, environment value or credential appears in any
      transmitted byte or error report.
      — `TestNothingFromTheSourceCrossesOnTheFullOptInPath`: every substantial line of the fixture's
      source is searched for verbatim in every transmitted byte, plus eight distinctive fragments of the
      engine's real refusal SENTENCES (`"passes **opts"`, `"the sealed input schema"`, …). The sentences
      are the new risk this change introduces and the reason that half exists.

### 🔴 A performance defect found on the real repository, and why it was not a level-8 concern

The first implementation asked the engine once per (node, axis) through `transform.Generate`, which
indexes the whole tree on every call. Against **nousresearch/hermes-agent** — 27 nodes × 7 axes over 8200
files — `heros link --with-ir` had **not finished after ten minutes** and was killed.

A command that hangs delivers the same empty console this phase exists to fix, by a longer road: that is
a level-2/3 failure, not an implementation cost to be traded away. `transform.ProbeNodeDimensions`
(new, `internal/transform/probe.go`) indexes ONCE and shares `site.rewrite` with `Generate` rather than
reimplementing the question — a second answer to "does this axis apply here" would be a second thing that
can be wrong. **19 seconds** after the fix.

### 🔴 Live proof (§2 acceptance)

Against the redeployed cluster, driven through the shipped command path on the real
`nousresearch/hermes-agent` checkout:

```
link: 25 node(s) carry axis verdicts computed here (11 applies, 114 refused, coverage cov-c19cf0c4).
      A node with no verdict is transmitted as REPORTED-BUT-UNDECIDED and the console shows
      `not reported`, never `not applicable`.
link: structure transmitted — 27 node(s), 0 edge(s) · graph at
      https://heros-agent.space/app/workflows/hermes-agent/graph
```

The run BEFORE redeploying is worth recording too, because it is the contract doing its job: the
previous image answered `400 {"error":"the workflow structure is not valid: json: unknown field
\"coverage_version\""}` — `DisallowUnknownFields` refusing a field it never ratified — and the CLI
reported the run as linked and the structure as NOT, then listed every structure surface as unfilled.
A widened payload cannot be silently accepted by an older platform.

## 3. Wave C — storage (Backend + System Designer)

- [x] 3.1 Migration `0042`: `workflow_ir` gains `coverage_version text NULL`. ~~Dual-dialect~~ (see 3.4),
      idempotent, guarded by definition not by object name, no backfill, no table rewrite.
      The per-node `language` and `axis_verdicts` need **no DDL at all** — they live inside the existing
      `nodes_json`, which the console renders whole and never filters by field, exactly as 0021 designed
      it. Only the payload-level version is a column, and the header says why.
- [x] 3.2 Migration `0043`: `linked_transform`, PK `(tenant_id, config_hash, source_revision)`.
      ~~Dual-dialect~~ (see 3.4), idempotent. The three alternatives to a new table are named and
      rejected in the file's own header, as `careful-table-creation` requires. A DB-level
      `CHECK (files_changed >= 0 AND …)` sits beside the handler's check because the two fail
      independently.
- [x] 3.3 🔴 Run both migrations on a **real Postgres**, then re-run them, then run the down migrations
      and up again. — `internal/pgmigrate/p29_linked_run_fanout_pgproof_test.go`, five tests, green
      against **postgres:16-alpine**:

      ```
      --- PASS: TestP29MigrationsApplyReapplyAndSurviveADownAndUp
      --- PASS: TestP29CoverageVersionIsNullableText
      --- PASS: TestP29ARowWrittenBeforeThisChangeReadsAsNotReported
      --- PASS: TestP29ATransformReceiptUpsertsRatherThanAppends
      --- PASS: TestP29TheDiffstatCheckFiresInTheDatabase
      ```

      The pre-change row is written with the **old column list**, not with `''` through the new store —
      simulating it the other way would only prove the store round-trips an empty string.
- [x] 3.4 Dialect-symmetry lint covers both migrations. If any DDL is moved into a Go hook, it must
      still be covered — a hook is not an exemption.

      ⚠️ **Reported rather than quietly reinterpreted: this repository has ONE dialect for this table
      family, and the asymmetry is deliberate.** `db/migrations/README.md` opens with *two dialects are
      two semantics*; the SQLite store in `internal/db/db.go` is the **dev ledger** (registries, memory,
      API keys), it has never carried `workflow_ir`, and `linkingest` opens Postgres and only Postgres.
      Writing a SQLite copy of these two tables to satisfy the phrase "dual-dialect" would create a
      second schema nothing reads and nothing migrates — the exact drift that README exists to prevent,
      wearing symmetry's clothes.

      What IS enforced is the hazard underneath, which does not need a second dialect to bite:
      `TestNoGoCodeExecutesDDLForP29Objects` asserts that **no Go file under `internal/` executes DDL
      touching P29's objects** (a hook is not an exemption), and
      `TestP29MigrationsAreCompleteAndSelfRecording` asserts the up/down pair, the idempotent ledger
      insert, and the presence of a `RAISE EXCEPTION` definition check — because `IF NOT EXISTS` is a
      NAME guard, satisfied by an object of the right name and any shape at all.
- [x] 3.5 `linkingest.WorkflowIRStore` reads and writes the new fields; absent fields read back as
      absent, never as a default. Fence: a row written before this change reads as `not reported`.
      — `sql.NullString` on the read and `nullIfEmpty` on the write. A plain `string` scan would turn
      NULL into `""`, which is indistinguishable from a client that reported an empty version, and the
      projection would have no way to render `not reported`.
- [x] 3.6 `linkingest.TransformReceiptStore` with upsert on the primary key. Fence: two transmissions
      of the same receipt leave one row — asserted at the SQL level (3.3) and again through the
      HANDLER (3.7), which is the path a retrying CI job actually takes.
- [x] 3.7 Four-layer assertion on the ingest path (request accepted → row present → read model returns it
      → surface renders it), for the structure payload and the receipt. A 2xx is not evidence of a write.
      — `internal/api/p29_ingest_pgproof_test.go`, layers 1–3 against a real Postgres; layer 4 is
      asserted on the deployed cluster in §8.3, because a rendered page is the only honest evidence for
      it. Layer 3 is called out in the file as the one that breaks *silently*: a column added to the
      write and forgotten in the read makes the surface say `not reported` for data the customer
      definitely sent.

      A third test covers the older-client direction end to end (§8.5's ingest half): a payload with no
      `coverage_version` is accepted, stored NULL, and reads back with `language` and `axis_verdicts`
      absent rather than defaulted.

### ⚠️ Three findings raised by this wave, none of them P29's to fix

1. **`internal/api`'s entire live-Postgres suite had not compiled since P28** —
   `accountflows_pgproof_test.go`'s `liveSurface` was missing `ConsoleURL()` and `Mailer()`, both added
   to `AccountSurface` by P28. `make pg-proof` reported `[build failed]` for that package and nobody had
   looked. Found because §3.7 needed to add a test to that package and could not. **Fixed to the minimum
   that revives the suite** (two stub methods carrying the interface's own documented meanings) rather
   than left broken, because leaving it would have made §3.7 unprovable — the suite now passes.
2. **`internal/tenancy`'s pgproof fixture cannot clear `platform_user`** —
   `user_password_user_id_fkey` (0041) blocks the delete, so four subtests fail. **Confirmed pre-existing
   at HEAD** by running it in a clean worktree. Not touched: it is P28's fixture and its own decision.
3. A governance fence in `internal/adminops` requires every post-P26 migration to name its owning phase.
   Both P29 migrations are registered there with their justification — that is the fence working, and it
   is recorded here because a reader tracing "why does a P26 test know about P29" deserves the answer.

## 4. Wave D — enumeration (Backend + Frontend)

- [x] 4.1 `GET /api/v1/workflows` answers from the reported-structure store, scoped to the authenticated
      principal. Remove the process-local `studio.WorkflowCatalog` from the console-facing path.
      — the route is now registered by `MountEnumeration`, not `MountStudioMatrix`. The catalog stays for
      the demo binaries; it is off the console path. It was a process-local map filled only by
      `cmd/demo` and `cmd/proof`, so on every real deployment this route answered an empty list,
      permanently, and the studio's picker had nothing in it for a reason no screen stated.
- [x] 4.2 `GET /api/v1/runs` merges executed and linked runs into one list with an `origin` field, one
      cursor, one ordering. Fence: a run linked in the test appears in the list.
      — `TestTheMergedRunsListLabelsEveryRowByOrigin`.

      A linked run is NOT flattened into a `RunSummary`: that type's `Status` is the executor's terminal
      state and a linked run has none, so filling it would mean inventing a value (`succeeded` claims we
      observed something we did not; `""` renders as a broken row). The two shapes sit side by side and
      `origin` says which — the same discipline `LinkedRunView` follows.

      🔴 A second defect was found writing the fence: the endpoint returned **503 whenever the EXECUTOR
      store was absent**, which is the normal shape of a deployment that only receives links. Hosted
      execution is P25's standing refusal, so "no executor" is the expected configuration — and a
      customer with a hundred linked runs was told "the P2 store is not mounted" and shown nothing.
      Either source is now enough, and a list with no executed half says so.
- [x] 4.3 `GET /api/v1/variants` and `GET /api/v1/transforms`, both derived — **no new table**.
      A variant IS a `config_hash` that has runs; the grouping rule lives in Go rather than in SQL for
      the reason `ForWorkflow` gives — a rule in SQL is a rule nobody can test without a database.
- [x] 4.4 Cross-tenant fence: for each of the four enumerations, a second organization's subjects are
      absent from the list and answer identically-to-nonexistent by id. Verify red by scoping one query
      to a request-supplied identifier. — `TestNoEnumerationLeaksAnotherOrganizationsSubjects`, which
      also asserts this organization's OWN subject IS returned, so the leak check cannot pass vacuously.
      Verified red by scoping `MemWorkflowIRStore.ListWorkflows` to a query parameter.
- [x] 4.5 Three-state responses on every enumeration: empty / read-failed / not-mounted. Fence: a store
      returning an error must not produce an empty list.
      — `TestAReadFailureNeverRendersAsAnEmptyList`, and it asserts more than the status: a read failure
      carries **no items array at all**, so a consumer that reads `items` without checking `state`
      cannot be handed an empty one. Verified red by making each failing store return `(nil, nil)`.
- [x] 4.6 The pre-ownership count is reported on the merged runs list whether or not the list is empty.
      — already true from P27 and preserved through the merge; the note now sits beside a second
      qualifier (`executed_runs_state`) for a deployment that carries no executor at all.
- [x] 4.7 `web/console/src/lib/subjects.ts` demoted to an ordering hint: pickers populate from the
      enumeration; a remembered subject the enumeration does not contain is discarded, not rendered.
      Hand entry is retained. — new `web/console/src/lib/enumeration.ts`; all four picker surfaces
      updated; `SubjectPicker` rewritten.

      The discard is surfaced rather than silent: *"N shortcuts from this session are not shown: the
      platform no longer lists them."* A session's memory is not evidence that a subject exists, and a
      picker that offers a door which does not open is worse than one that offers fewer doors.

      `/app/runs` builds its picker from the SAME fetch its list already made — two requests for one
      list is two chances for them to disagree, and the reader would see a run in the list that the
      picker below did not offer.

      🔴 The fence caught `subjects.ts` still asserting *"the platform exposes no enumeration endpoint
      for any of them"* — a sentence that had just become false. It now describes what the module is.
- [x] 4.8 🚫 Regression fence for `UI 改版不得丢失既有功能`: every control present on the five picker
      surfaces before this change is present after it.
      — `web/console/tests/p29-pickers.test.mjs`, twelve controls named individually (the input, its
      label, the submit button, the GET form, the help text, the `children` slot a two-part subject
      needs, the list, the per-row link and hint, the chevron, the section, the form's accessible
      heading).

      The list is SPELLED OUT rather than derived, deliberately: a derived list would be derived from
      the component as it is now and would therefore agree with any removal. A regression fence has to
      compare against the shape BEFORE the change, which only a written record supplies.

      Console suite: **635/635**. One unrelated fence fired and was honoured — the two new enumeration
      paths had no telemetry template, so their latency and error rates would have logged as
      `/unknown`.

## 5. Wave E — the projection (Backend + AI Engineer + Frontend)

- [x] 5.1 `internal/axisprojection`: pure read joining `transform.AxisCoverage()` with a stored
      structure. No table, no cache, no materialisation — asserted structurally by
      `TestTheProjectionHoldsNoStateBetweenCalls`, because "it is a read" is the kind of property that
      stops being true one convenience at a time.
- [x] 5.2 🔴 The **no-derived-verdict** fence: a static check that no path in the projection produces a
      verdict from platform-held node properties. Verify it red by adding one.
      — `TestTheProjectionDerivesNoVerdict`. Verified red by adding the exact one-line "improvement" it
      exists to catch — `case !told && hasRow[axis+"|"+lang]: cell.State = StateApplies` — which the
      scan named twice, once for `hasRow` and once for `Language`, and which
      `TestACoveredNodeWithNoVerdictRendersNotReported` caught independently by its effect.
- [x] 5.3 Four-state cell (`applies` / `refused` / `not-applicable` / `not-reported`) in the read model,
      with the cause identifier and owner carried as data. The console renders the SENTENCE from its own
      catalogue keyed by the identifier — so a CLI three versions old cannot put stale copy on a paid
      surface, and the engine's `Detail` (which names the customer's own arguments and symbols) has no
      field on the wire at all.
- [x] 5.4 Fence: a node whose language and form are covered and which carries no transmitted verdict
      renders `not-reported`, never `applies`. — `TestACoveredNodeWithNoVerdictRendersNotReported`.
      This is precisely the cell a derived projection would fill.
- [x] 5.5 Fence: `not-applicable` is never produced from an absent input. Verify red by deleting a row
      and asserting the cell does not become `not-applicable`.
      — `TestNotApplicableIsNeverProducedFromAnAbsentInput`. Verified red by deleting the
      `row.Language != ""` guard: a node with no reported language then fell into the `!hasRow` branch
      for **every axis** and every cell read `not-applicable` — a confident claim about the customer's
      code, sourced entirely from our not having been told what language it is in.

      The test also asserts `not-applicable` is REACHABLE (via a language the table genuinely has no row
      for). A state that can never be produced makes its absence elsewhere prove nothing.
- [x] 5.6 Staleness: compare stored `coverage_version` with the running build's; label stale, show both
      versions, exclude stale counts from totals. Fence: pin a stored version the build does not have
      and assert the exclusion arithmetic.
      — `TestAStaleProjectionExcludesItsCellsFromEveryTotal`, which asserts the ARITHMETIC rather than
      the label: the four states plus `stale_excluded` equal the denominator, on every axis, always.

      A companion fence holds the distinction that matters: an **absent** reported version is NOT stale.
      It is not reported — a different fact with a different remedy — and conflating them would tell
      every pre-P29 client their data came from the wrong build.
- [x] 5.7 Denominators on every count; a proportion is never rendered without the counts behind it.
      — `AxisTotals.Nodes` is part of the TYPE rather than something a surface is trusted to add, and
      the panel renders `value / of` for all four states.
- [x] 5.8 `GET /api/v1/workflows/{workflow_id}/axis-projection`, and the delivery-route projection with
      the `undeliverable` count. — `internal/api/axisprojection.go`.

      🔴 The runtime-route rule is exported from `changedelivery` as
      `RuntimeDeliversDespiteSourceRefusal` rather than restated in the projection. `BuildReport` already
      decides it in two places with two different arguments, and a second copy would be a second answer
      to "can this reach my node", disagreeing with `/app/delivery` on the days it matters most.

      🚫 A node the platform was not told about is **not** counted as undeliverable — that would be a
      claim about the customer's code drawn from our own ignorance, and it is exactly the number a
      reader would act on. The denominator printed beside it is `reported_cells`, not `cells`.
- [x] 5.9 Bidirectional coverage-source assertion: the projection offers no cell the engine refuses, and
      the engine materialises no cell the projection has no row for.
      — `TestTheProjectionAndTheEngineAgreeOnTheAxisSet`, plus a per-row check that every node carries
      exactly one cell per axis: a missing cell renders as nothing, which reads as `not applicable` —
      the one claim this data must never make by accident.
- [x] 5.10 Console: add a projection panel to `/app/wiring`, `/app/context`, `/app/memory`,
      `/app/harness`, `/app/coverage`, `/app/authoring` and `/app/delivery`. The existing worked
      examples stay; the panel carries its own heading and its own denominator, and states that it is
      live data. — `web/console/src/components/axisProjection.tsx`, all seven surfaces.

      The axis pages do NOT gain a workflow picker. Their subject is the axis, not a workflow, and
      putting a picker on seven surfaces would make each of them ask a question the reader did not come
      there with. The panel projects the most recently reported workflow, names it, and links to it.

      🔴 The "worked examples stay" fence had to be corrected while writing it: two of the four pages
      carry their vocabulary as DATA (`STRATEGIES`, `HARNESS_BOUNDARY`) rather than through the shared
      refusal card, so a fence that only knew about the card would have "passed" on those two by never
      looking at them.
- [x] 5.11 Frontend three-state discipline: `not-reported`, `refused` and a transport failure are three
      treatments; no 404 is mapped to a business state. — asserted on both sides: the panel has a
      branch per state with copy that distinguishes each, and the fence reads
      `internal/api/axisprojection.go` to confirm an unreported workflow answers **200 carrying
      `not-reported`**. A 404 there would be indistinguishable from a transport failure and would send
      the reader hunting a broken deployment when the truth is that they have not opted in.
- [x] 5.12 Design-token conformance (`npm run scan:tokens`): no colour, spacing, type-size or radius
      literal outside the two permitted layers. The fourth state gets a token, not an improvised colour.
      — `--not-reported` added to all three theme blocks plus the colour mapping.
      `token scan passed: 205 file(s)`.

      It deliberately does NOT alias `--unknown`. They are different facts: `--unknown` is "we could not
      determine this" (an outage) and `--not-reported` is "you did not tell us" (a boundary the customer
      chose). One colour for both would show an egress decision in an outage's colour on the very screen
      where somebody is deciding whether to opt in.

## 6. Wave F — the workflow surfaces (Backend + Frontend)

- [x] 6.1 Studio matrix columns from the reported structure; rows from the model registry; each column
      carries the node's symbol and current model.
      — `GET /api/v1/workflows/{id}/nodes` consults the process-local catalog FIRST (so the demo binaries
      are unchanged, and a platform-loaded workflow is never overridden by a reported one) and falls back
      to the reported structure. The fallback direction is the one that cannot regress anything.
- [x] 6.2 A cell action requiring a provider credential is refused by name, states that the platform
      holds no customer provider credential, names the local command, and does **not** imply that a
      plan would change it.

      The sentence it replaces was *"studio test-run is not available on this deployment"* — the shape of
      answer that produces a support ticket, because it reads as a capability somebody switched off and
      the reader's next move is to ask which plan turns it on. There is no such plan. The response now
      carries `reason_code`, `local_command` and an explicit `plan_would_fix: false`, all fenced.
- [x] 6.3 A hosted binding travels the existing preflight → resolve → gate → transform spine. Fence:
      no second apply path exists; a refused change is refused identically from the console.
      — `TestThereIsNoSecondApplyPath`, an AST scan for `transform.Generate*` calls in `internal/api`.

      ⚠️ It found ONE pre-existing call — `grapheditor.go`'s commit — and it is legitimate: the contract
      verdict runs BEFORE the engine (a `rejected` reorder never reaches it) and the engine's own wiring
      check refuses what it cannot materialise, surfaced as `rejected_transform`. It is allowlisted **by
      name with that justification written down**, the same discipline `publicroutes.go` and the
      post-P26 migration ledger use. A separate assertion holds that `studiomatrix.go` — the file P29
      adds a hosted action to — reaches the engine through nothing of its own, so removing the allowlist
      entry cannot silently remove that check too.
- [x] 6.4 `/app/workflows`, its graph and its board resolve for a reported workflow. Unreported is
      distinct from nonexistent, and both are distinct from a read failure.
      — `TestAnUnreportedWorkflowIsNotReportedRatherThanNotFound`: an unreported workflow answers **200
      carrying `not-reported`** and names the command that would fill it. 404 would read as "no such
      workflow" and send the reader to check an id that is correct.

      A catalog-only deployment keeps its old 404 unchanged — that answer was true before this change and
      is true after it, and widening it would tell an operator their deployment is broken when it is
      doing exactly what it is configured to do.
- [x] 6.5 `/app/transforms/{config_hash}/{source_revision}` resolves from a transmitted receipt and
      renders per-node outcomes and the diffstat — never a diff.
      — `handleGetTransform` falls back to `reportedTransform` both when the executor store is absent and
      when it has no such transform. The response carries `origin: reported` and — said out loud rather
      than left as an empty field — `diff_available: false` with the reason. A transform page with a
      blank diff reads as broken; one that says why the diff is absent reads as a boundary.
- [x] 6.6 `/app/variants/{id}/scorecard` resolves from the enumeration; the existing
      `FailureAttribution: unavailable` statement is preserved verbatim. — `/api/v1/variants` lists the
      configurations this organization has reported runs for, and the picker links each to its
      scorecard. The scorecard itself is untouched: `hostedscorecard` already documents why a linked run
      cannot support attribution ("not to blame" and "not investigated" are opposite findings), and this
      change gives it more subjects rather than a new claim.
- [x] 6.7 Graph regions stay `unclassified`, carried as data; no pattern label is inferred from a symbol
      name. Fence asserts it. — `TestNoPatternLabelIsInferredFromASymbolName`, an AST scan over the
      graph-rendering files for `strings.Contains/HasPrefix/HasSuffix/EqualFold` applied to a node's
      `Symbol`, `NodeID` or `File`.

      The temptation is concrete: `rerank_results` is obviously a reranker and one line of string
      matching would label the graph beautifully — until it is a repository where that function does
      something else, and a pattern label is read as a finding rather than as a guess.

## 7. Wave G — metering honesty (Backend + Sales Operations)

- [x] 7.1 Provision a Free account at the first authenticated act, create-if-absent, never correcting an
      existing one. Fence: an organization on a paying plan is unchanged across a restart and across
      repeated links. — `internal/launch`'s `firstActProvisioner`, called from the run-link ingest,
      `whoami` and the coverage read; `TestProvisioningNeverCorrectsAnExistingAccount`.

      🔴 Three populations fell outside the boot-time seeding and every one is a real customer: an
      organization created by SELF-SERVE SIGN-UP after boot, one created before a plan catalog was
      configured (the seeding returns early with no catalog and never revisits), and any created by a
      path that is not the seed. Each linked a run attributed to a customer the billing read model could
      not find, and `/app/billing` was ABSENT — not empty, absent.

      The provisioner returns on `Get` success WITHOUT reading the plan, so there is no code path that
      could read one and decide to change it. A read FAILURE does not provision either: creating an
      account because the database was unreachable would create a second one for a customer who has one.
- [x] 7.2 `GET /api/v1/link-coverage` — the three-state coverage outside `BillingView`.
      — `internal/api/linkcoverage.go`. `TestCoverageIsReadableWithNoBillingAtAll` mounts it with no
      provisioner, no account system and no plan catalog.
- [x] 7.3 Fence: unknown coverage renders distinctly from complete, and a read failure yields unknown
      rather than zero. Verify red by making the read return `(0, 0, nil)`.
      — `TestUnknownCoverageIsDistinctFromCompleteAndFromZero`, four states asserted plus a
      **serialisation** check: the two that must never be confusable are compared as BYTES, because a
      distinction that does not survive the wire is not a distinction. Verified red by returning
      `(linkingest.LinkCoverage{}, nil)` on error — the exact shape of `return 0, 0, nil` — which
      answered `known:false` with zeros a caller would render as "0 of 0 runs observed".
- [x] 7.4 Every derived spend figure displays its coverage; no extrapolation to unobserved runs.
      — the billing page's coverage panel REUSES the existing `<LinkCoverage>` component rather than
      drawing a second one. It already renders the three states with its colours from the token layer and
      its copy written for this exact distinction; a parallel renderer would be a second source of truth
      for the one number this page most needs to be unambiguous about, and copy is always what drifts.
      What changed is where the data comes from.
- [x] 7.5 `/app/billing` distinguishes no-data, no-account and not-served with three messages.
      — `not-served` was already `NoCollection`; `no-account` was collapsed into a generic failure and
      now has its own `NoAccount` banner saying the runs ARE linked and counted and that nothing the
      reader has done is affected; `no-data` is the existing per-period empty state.
- [x] 7.6 Closed-period refusal stays distinguishable from a duplicate link and from a rejected payload.
      — unchanged and re-verified: `409 + closed_period: true`, `409 + already_linked: true` and `400`
      are three distinct answers on the ingest path, and this change touched none of them. The
      provisioning call was placed AFTER the authentication check and BEFORE the decode, so it cannot
      alter which of the three a request gets.
- [x] 7.7 Sales-operations pass: the customer-facing description of this phase claims only what the
      commit does. Terminology added to the noun dictionary: *reported node*, *not reported*,
      *projection*, *undeliverable*, *link coverage*.
      — `docs/decisions/p27-console-surface-design.md` §1 gains five rows and a new §7.

      🔴 §7 names the wrong sentence explicitly, because it is the most tempting one in this phase and it
      is wrong the same way "create an account" was wrong in P27 — it is what every other product says:

      | 🚫 Never write | ✅ Write |
      |---|---|
      | "the console shows your workflow" | "the console shows the structure you chose to send" |
      | "we analyse your code" | "your CLI computes the verdicts; the platform crosses them with its coverage table" |
      | "N of your nodes are not covered" | "N of your nodes were not reported" |
      | "we found 31 undeliverable changes" | "31 of the nodes you reported are undeliverable by both routes" |

      The rule generalises: every customer-facing sentence about a node names WHOSE FACT IT IS. The
      coverage table is ours; the verdict is theirs, computed on their machine; the multiplication is
      ours and is only as complete as what they sent.

## 8. Wave H — governance and proof (QA + DevOps + all roles)

- [x] 8.1 Ledger rows for the six capabilities in `openspec/operator-surface-ledger.md` §B, and
      `p29-linked-run-fanout` added to `GOVERNED_CHANGES` in
      `web/admin-console/scripts/scan-ledger.mjs`. — `ledger scan passed: 67 row(s), 18 destination(s)`.

      Seven rows, not six: adding the change to `GOVERNED_CHANGES` also brought `run-linking` (modified
      by this change) into the fence's scope, and it named it immediately.

      🔴 `axis-node-projection` is `not-yet-readable`, and the reason is worth quoting because it is not
      the usual "no query behind it": a FLEET projection number would be **the least trustworthy figure
      on any operator screen**. It is an average over the organizations that opted in, and every
      organization that did not is absent rather than zero — an operator reading "62% of customer nodes
      are covered" would be reading a statistic about who runs `--with-ir`, not about the product.

      `hosted-workflow-catalog` and `linked-subject-index` are `no-operator-surface` on the privacy
      grounds the `user-identity` row establishes: a cross-tenant listing of customers' workflow
      identifiers and configuration hashes is an expansion nobody asked for.
- [x] 8.2 ⚠️ **Report, do not silently fix:** `p28-email-password-identity` is not in `GOVERNED_CHANGES`
      either, so its capabilities are ungoverned by the same fence.

      **Confirmed, and it is larger than stated.** TWO P28 change sets are ungoverned:

      | change | capabilities |
      |---|---|
      | `p28-email-password-identity` | cli, email-delivery, password-identity, platform-ingress, sso-identity |
      | `p28-first-owner-reachability` | deployment-topology, email-delivery, password-identity |

      **Eight capability specs** for which nobody has decided whether an operator gets a surface, a
      reasoned absence, or a named missing collection. The fence's own documentation describes exactly
      this state — *"a fence scoped to its own author is the drift it was written against, wearing a
      uniform"* — which it wrote about P27 before P27 was added.

      **Recommendation:** add both to `GOVERNED_CHANGES` and write a row per capability. Several are
      identity/password capabilities where `no-operator-surface` on privacy grounds is likely right; the
      `user-identity` row is the precedent and the reasoning any such row must match.
      **Not done here**, per this task's instruction. Raised as a separate task — and **subsequently
      decided and done** in a follow-up: both changes are now in `GOVERNED_CHANGES` and all six distinct
      capability names carry rows (`ledger scan passed: 73 row(s)`). Recorded here so a reader of this
      finding is not left hunting for its resolution.

      🔴 Writing those rows turned up a defect the fence could not have named on its own:
      `email-delivery` requires that an undelivered message "including the link a person needs" is
      written to an **operator-readable record**, and `/readyz` tells the operator
      `mail_configured: false` with the detail *"they are held on the operator surface"* — while
      `mailer.OperatorMailer.Undelivered()` is in-process, per-replica, bounded to 200, and **read by
      nothing outside its own package**. The readiness surface currently directs an operator to a place
      that does not exist. Its row is `not-yet-readable`, naming the durable store that would fix it.
- [x] 8.3 End-to-end acceptance on a real deployment, in one sitting, recorded: sign in → `heros
      discover` → `heros eval` → `heros link --with-ir` → `heros apply --link-receipt` → open all
      fifteen surfaces → each renders this organization's own data or a named, typed refusal.

      Deployment: `make deploy-up` from this checkout. Repository: the real
      **nousresearch/hermes-agent** at `470cf66b039c`. Driven through the shipped command path.

      ```
      heros discover        28 nodes, 0 edges
      heros link --with-ir  25 of 27 nodes carry verdicts computed here (11 applies, 114 refused,
                            cov-c19cf0c4) · structure transmitted · 10 of 18 surfaces now carry data
      heros apply           --link-receipt — 0 node outcomes and a diffstat (0 files, +0/-0).
        --link-receipt      "The diff itself has no field on this payload and is not sent."
                            receipt accepted · /app/transforms/f0f51f381c…/470cf66b039c…
      ```

      **All fifteen surfaces answered 200** in the browser. The read side, on real data:

      | read | answer |
      |---|---|
      | `/api/v1/workflows` | `ok`, 2 |
      | `/api/v1/variants` | `ok`, 9 |
      | `/api/v1/transforms` | `ok`, 1 |
      | `/api/v1/runs` | 9 rows, every one labelled `origin: linked` |
      | `/api/v1/link-coverage` | `complete`, 9/9 |
      | transform by key | `origin: reported`, `diff_available: false` |
      | matrix columns | 27, first `TrajectoryCompressor._generate_summary_async` |
      | delivery projection | **89 undeliverable / 125 reported**, 216 cells, 91 not reported |

      That last row is the sentence the proposal was written around, realised: not *"128 apply / 123
      refuse"* but *"89 of the 125 cells you reported are undeliverable by both routes, and here they
      are"* — each named by its real symbol and axis, with the 91 unreported cells explicitly **not
      counted either way**.

      🔴 **The browser found a defect the type-check could not.** `/app/runs` answered **500** —
      `Cannot read properties of undefined (reading 'slice')`. The console's `RunSummary` was
      hand-written as if every field were always present; the merged list started returning linked rows
      whose fields live under `linked`; the type-check passed because a hand-written type is a CLAIM
      about a payload, and a claim that has stopped being true type-checks perfectly. Fixed, and fenced
      (`§4.2 the runs list never dereferences a field a linked row does not carry`).

      A layout defect was found the same way: the projection panel was placed as a SIBLING after
      `<Tabs>`, which is `flex-1` and owns the remaining viewport — so its heading overlapped the tab
      strip. It is a tab now, on all seven surfaces.
- [x] 8.4 Negative acceptance: the same walk with **no** opt-ins. Every structure-dependent surface says
      `not reported` and names the option. No surface renders `not applicable`, a zero, or an empty
      table implying there is nothing to show.

      ```
      GET /api/v1/workflows/never-linked/axis-projection
        state       not-reported          (200, not 404 — 404 would read as "no such workflow")
        node_count  0
        fill_with   heros link --with-ir
        detail      "…there is nothing to cross the coverage table with. The platform computes no
                     verdict it was not sent."
      ```
- [x] 8.5 Older-client acceptance: a CLI build from before this change links successfully; its structure
      stores with the new fields absent; every projection cell reads `not-reported`.

      A pre-P29 payload (no `coverage_version`, no `language`, no `axis_verdicts`) POSTed to the live
      deployment:

      ```
      ingest                        HTTP 201
      reported_coverage_version     None      ← absent, NOT this build's version
      stale                         False     ← absent is not stale; they are different facts
      node language                 None      ← absent, never inferred from the .py extension
      verdicts_reported             0
      cell states                   {'not-reported': 8}   ← all eight axes, none `not-applicable`
      ```
- [x] 8.6 Fence-redness ledger: one table listing every fence added by this change, how it was broken,
      and the failure output observed.

      | # | fence | how it was broken | what it said |
      |---|---|---|---|
      | 1.1 | `TestEveryPathTheCLIAddressesIsPublished` | deleted the `HasSuffix(path,"/")` exemption (after fixing the scan, which never saw those paths) | *"the CLI addresses /api/v1/proposals/, which has a caller-supplied segment below it… Publish it Exact, which means giving it a FLAT shape"* |
      | 1.6 | `TestAPrefixRuleWouldPublishItsSiblings` | added a `Prefix` rule for `/api/v1/workflows/` | listed **15 routes by name**, "…and every route added under that prefix from now on, by default, forever" |
      | 1.7 | `TestTheParameterisedRoutesAreRegisteredAndNeverPublished` | published `/api/v1/workflows/{workflow_id}/ir` | *"It must never be published: the flat replacement carries the same traffic and can be matched Exact"* |
      | 1.8 | `TestABare404IsReportedAsNotReachable…` | removed the `edge404` call from `ReportVerdict` | the old message: *"the platform has no proposal p for this identity — check the id, and that the token belongs to the tenant"* |
      | 1.9 | `TestBothSubstratesPublishExactlyTheDeclaredPublicRoutes` | shrank `PLATFORM_PUBLIC_PATHS` back to `/billing/webhook` | **nine** named failures, one per unpublished route |
      | 2.2 | `TestEveryOptInFieldIsAnIdentifierOrAClosedSetMember` | added `Detail string` to `WireAxisVerdict` — the realistic mistake, since the engine has exactly that field | all three checks fired: *"is a string with no closed set"*, *"not on its allowlist"*, *"whose name says content"* |
      | 2.3 | `TestNoRefusalIsAboutTheProbeItself` | the ORIGINAL skills probe (`{"type":"object"}`, no `properties`) | *"the skills probe made the engine refuse node plain by naming the PROBE: skill \"probe\": the sealed input schema declares no `properties`"* |
      | 5.2 | `TestTheProjectionDerivesNoVerdict` | added `case !told && hasRow[axis+lang]: StateApplies` | named it twice — once for `hasRow`, once for `Language` — and §5.4 caught it independently by its effect |
      | 5.5 | `TestNotApplicableIsNeverProducedFromAnAbsentInput` | deleted the `row.Language != ""` guard | *"a node with no reported language produced `not-applicable` for axis model"* — and for all seven others |
      | 4.5 | `TestAReadFailureNeverRendersAsAnEmptyList` | made each failing store return `(nil, nil)` | every endpoint answered 200 with an empty array; the test named each |
      | 4.4 | `TestNoEnumerationLeaksAnotherOrganizationsSubjects` | scoped `ListWorkflows` to a query parameter instead of the principal | the other organization's workflow appeared in the list |
      | 7.3 | `TestUnknownCoverageIsDistinctFromCompleteAndFromZero` | returned `(LinkCoverage{}, nil)` on error — the shape of `return 0, 0, nil` | `known:false` with zeros a caller renders as "0 of 0 runs observed" |
      | 4.7 | `§4.7 subjects.ts no longer claims…` | ran it before editing `subjects.ts` | caught the file still asserting *"the platform exposes no enumeration endpoint for any of them"* — a sentence that had just become false |
      | 5.10 | `§5.10 the worked examples are still there` | ran it as first written | 🔴 **the fence itself was wrong**: it looked only for `AxisRefusal`, and two of the four pages carry their examples as DATA (`STRATEGIES`, `HARNESS_BOUNDARY`), so it would have "passed" on those two by never looking |
      | 8.3 | the browser | ran the real walk | `/app/runs` **500** — `Cannot read properties of undefined (reading 'slice')`, which the type-check could not see |

      Three fences in this table were verified red by a mistake somebody would actually make rather than
      by a synthetic break — 2.3, 5.2 and 5.10 — and two of them (2.3 and 5.10) caught a defect **in the
      thing being fenced** rather than a hypothetical one.
- [x] 8.7 Upgrade rehearsal: confirm the parameterised routes still serve in-cluster, confirm the flat
      routes serve publicly, then confirm the CLI's pre-change path still works.

      Against the deployed stack, with the Caddy edge in front:

      | request | in-cluster (`:14321`) | through the edge (`:18080`) |
      |---|---|---|
      | `POST /api/v1/workflows/{id}/ir` | **201** | **404** (console fall-through) ✅ |
      | `PUT /api/v1/workflows/{id}/source/{rev}` | **202** | not published ✅ |
      | `POST /api/v1/proposals/{id}/verdict` | **404** *(platform: "no such proposal")* | not published ✅ |
      | `POST /api/v1/workflow-ir` | **201** | **201** ✅ |

      That is expand-contract holding in both directions: an older CLI reaching the parameterised routes
      from inside a cluster still works, and none of them is on the internet.

      ⚠️ **Stated rather than glossed:** the rollback half — applying this deployment over a cluster
      running the PREVIOUS release image and then re-applying the previous one — was **not** performed.
      This host runs a single Compose stack built from the checkout, so there is no previous release
      image to roll back to without publishing one. What IS proven is the property the rehearsal exists
      to establish (both shapes serve, only the flat ones publicly) plus migration 0042/0043's own
      down-and-up on a real Postgres (§3.3). A rehearsal against a tagged previous release belongs in the
      release pipeline, and is the one item in this change closed by argument rather than by execution.
- [x] 8.8 Coverage-total regression: `config_hash` for a configuration with no new axis field hashes
      byte-identically to before this change, so P0 golden vectors keep reproducing.
      — `internal/confighash` and `internal/variantspec` golden-vector suites green, unchanged. Nothing
      in this change touches `discovery.IRNode` or `variantspec`: the new fields are on the WIRE type
      (`runlink.WireIRNode`), which config_hash has never read.
