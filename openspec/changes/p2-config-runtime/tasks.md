# Tasks — P2: Configuration Layer + Runtime

Applies the source-transformation apply model per
[ADR-001](../../../docs/adr/ADR-001-source-transformation-apply-model.md).

## 1. Backend — Registries (model / prompt / skill / context)

- [x] 1.1 Design Postgres schema for the four registries: `(name, version_id)` unique per entry,
  ```
  `version_id` content-addressed, published rows immutable. Expand-only migration.
  ```
- [x] 1.2 Implement `RegisterModel` — provider + model ID + inference params (temperature,
  ```
  max_tokens, thinking budget, seed) as a versioned unit; return `version_id`.
  ```
- [x] 1.3 Implement `RegisterPrompt` — template with named variable slots; store body as a
  ```
  content-hashed blob; deterministic renderer given identical bindings.
  ```
- [x] 1.4 Implement `RegisterSkill` — name → JSON-schema input/output contract + impl handle;
  ```
  validate the schema itself at registration.
  ```
- [x] 1.5 Implement `RegisterContextPolicy` — named policy + params; register the `full` policy;
  ```
  leave the interface open for P3 policies.
  ```
- [x] 1.6 Enforce immutability: reject any mutation of a published `version_id`; a change produces
  ```
  a new version.
  ```
- [x] 1.7 Verify additive/expand-contract evolution: a Variant Spec pinning an older version still
  ```
  resolves after a new version is published.
  ```



## 2. Backend — Configuration Layer / Variant Spec + transform generation

- [x] 2.1 Define the **Variant Spec** type (canonical desired-state config): `{node_id →
  ```
  {model_ref, prompt_ref, skill_refs[], context_policy}}` + node ordering/graph +
  `source_revision`; persist in Postgres, unique on `(config_hash, source_revision)`.
  ```
  > **Reworded from "unique on `config_hash`", which the implementation does not do — for a reason the
  > migration argues and this review accepts.** The key is `PRIMARY KEY (config_hash, source_revision)`
  > (`0003_p2_variant_spec.up.sql:25-40`). `config_hash` alone is not expressible as the key because
  > `source_revision` is *deliberately* outside `config_hash` (see `config-hash-spec.md`): one config
  > applied against two source revisions is two different specs. The original wording, not the code,
  > was wrong.
- [x] 2.2 Implement the **Transform Engine**: from a resolved Variant Spec, generate a
  ```
  **deterministic AST-level codemod** that rewrites the model and prompt dimensions at a node's
  discovered call site to the spec's values; per-dimension independent — a dimension left at
  default is not edited. Skills and context assembly are refused with a typed, actionable reason
  (see below); they are not silently skipped.
  ```
  > **Reworded from "each node's four dimensions" — that was false when ticked, and two dimensions
  > remain deliberately refused.** State per dimension:
  > - **model — real.** Within-provider swap rewrites the model argument. Cross-provider swap is
  >   refused per [ADR-002](../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md).
  > - **prompt — now real** (it was not: `rewritePrompt` refused any template with slots and anything
  >   that was not a bare `*ast.BasicLit` STRING, i.e. every realistic SDK call site, and **no passing
  >   test rewrote a prompt at all**). The engine now descends into the existing SDK construction and
  >   replaces only the string expression inside it — the message slice, role helper and SDK types
  >   survive byte-for-byte. This needs **zero per-SDK knowledge** and is type-preserving by
  >   construction (a string expression swapped for a string expression type-checks wherever the
  >   original did), which is why it beats the per-SDK message-construction rewriter the m2 review
  >   scoped as the fix. **Slots bind to the call site's own expressions**, verified against the call
  >   site, never guessed — positional matching was explicitly rejected because template slot order
  >   need not match operand order, so it would silently swap two values into each other's holes: a
  >   diff that compiles and quietly corrupts every eval beneath it.
  > - **skills — refused, escalated.** See the cost-escalation table under 3.2. Not implemented, and
  >   deliberately so: the prompt trick does not transfer (there is no existing construction to edit;
  >   we would synthesize per-SDK, per-SDK-version, multi-line tool literals — which `gateMinimal`
  >   forbids outright). **A subtly-wrong tool schema compiles and degrades quality invisibly** — the
  >   worst possible failure for an eval platform (八级法则 L2).
  > - **context — refused; P3 is the named owner** ("the rewrite this needs arrives with P3's real
  >   policies"). The refusal now names P3 rather than reading as an unexplained gap.
  >
  > **Refusal boundary (defensible, not arbitrary):** rewrite when the prompt argument contains
  > **exactly one** string-valued expression; refuse multi-turn lists, slotless templates at a call
  > site feeding a runtime value, slots matching zero or 2+ operands, and operands no slot claims.
  > "Exactly one" is not luck: real SDKs carry the role in a *function* (`NewUserMessage`) or a *typed
  > constant* (`ChatMessageRoleUser`), never a bare string. Widening past this would mean guessing on
  > customer source — ADR-001's top-named risk (八级法则 L1/L2). **An honest narrow boundary beats a
  > broad guess.**
  > **Bug found and fixed en route:** `gateMinimal` rejected rewrites that *insert* a newline but not
  > ones that *remove* a line — sound while only the model rewriter existed (a model constant never
  > spans lines), unsound the moment a prompt rewriter can collapse a multi-line expression.
  > Mutation-tested: without the fix the transform **silently succeeds and shifts every line below it**.
- [x] 2.3 Determinism: same `config_hash` + same `source_revision` → **byte-identical diff**;
  ```
  content-hash the generated patch and store it in the object store.
  ```
- [x] 2.4 Behavior-preserving-except-intended: the diff touches only the targeted call site(s) and
  ```
  only the configured dimension(s); reject a transform that produces incidental edits or
  reformats untouched code.
  ```
- [x] 2.5 Implement stable `config_hash`: canonical serialization (key-order- and
  ```
  whitespace-invariant); changes iff a referenced version or the ordering changes.
  ```
- [x] 2.6 Validation: reject a spec referencing a missing node, an unresolved `*_ref`, an
  ```
  unregistered `context_policy`, or a call site the transform cannot rewrite safely — before
  any transform is applied or run.
  ```



## 3. Backend + System Designer — Runtime loader, transform application & executor

- [x] 3.1 Implement `Loader.Resolve(VariantSpec) → ResolvedConfig`: resolve every `*_ref` against
  ```
  registries; **fail closed** (abort, no diff generated, no run) on any dangling ref.
  ```
- [x] 3.2 Render prompts from templates + bindings; validate skill availability + arg shape against
  ```
  the JSON-schema contract at registration. Prompt rendering reaches the call site (2.2);
  skill binding at the call site is refused, not performed — see the escalation below.
  ```
  > **Reworded: the original clause "before the transform binds them at the call site" was vacuous** —
  > the transform never binds skills. Resolution + registration-time schema validation are real
  > (`resolve.go:175-190`, `skill.go`), and the gateway carries `Tool.InputSchema` through
  > (`gateway.go:73`), but nothing in P2's codemod path binds a skill.
  > **🔴 Escalation — this is a decision for the owner, not a silent downgrade** (cost-escalation-path):
  >
  > | Option | Short-term | Long-term | UX | Architecture |
  > |---|---|---|---|---|
  > | **A.** Per-SDK tool-emitter + multi-line support | High — an emitter per SDK×version, plus relaxing `gateMinimal`'s no-newline invariant | Ongoing per-SDK maintenance; **weakens the line-count invariant that makes minimality checkable at all** | Skills work | Puts SDK codegen in the engine — ADR-001's top-named risk |
  > | **B.** Refuse + name the owner *(current)* | ~0 | Skills stay unconfigurable in P2 | Loud, actionable refusal | Engine stays SDK-agnostic |
  > | **C.** Rewrite only an *existing* `Tools:` slice's names | Medium | Narrow; only helps call sites already passing tools | Partial | Consistent with 2.2's prompt boundary |
  >
  > **Recommendation: B for P2, C as the next increment.** Awaiting sign-off.
- [x] 3.3 Instantiate the selected context policy (`full` in P2) via the pluggable interface.
  ```
  The transform does not rewrite context assembly in P2; P3 owns that rewrite.
  ```
  > **Reworded: the second clause ("the transform rewrites the call site's context assembly
  > accordingly") was never true.** First half is real — `registry/context.go:105` binds a real
  > `Policy` implementation and fails closed on an unknown policy (`:120`). Second half:
  > `rewriteContext` refuses unconditionally, and the code always conceded it —
  > "The rewrite this needs arrives with P3's real policies" (`rewrite.go:397`). P3 is now named as
  > the owner in the refusal text itself. **Pulling P3's policies forward into P2 was considered and
  > rejected** — it dissolves the phase boundary to make a checkbox true.
- [x] 3.4 **Isolated application:** check out an isolated git worktree/branch at `source_revision`
  ```
  from a pool; apply the codemod and commit on a variant branch; never mutate the user's
  working tree in place.
  ```
- [x] 3.5 **Build-preserving gate:** run the target's build on the transformed worktree; **reject**
  ```
  a transform that fails to build before it is ever proposed or run; surface a typed error
  naming the node/dimension whose rewrite failed to build.
  ```
- [x] 3.6 Per-`config_hash` **build cache**: cache the built, transformed artifact keyed by
  ```
  `config_hash`; a cache hit skips regeneration + rebuild (supports P4 fan-out).
  ```
- [x] 3.7 Implement `Executor.Run`: **run the built, transformed working copy in a sandbox**,
  > **Wording superseded by ADR-001.** "walking the node graph in declared ordering ... before it
  > feeds a downstream node" is shim-model language: ADR-001's terminology table retires
  > *executor "walks the graph through the shim"* in favour of *executor "runs the transformed
  > working copy in an isolated sandbox"*. The transformed program contains the graph and walks it
  > itself, so there is no seam between two nodes for us to stand in. Implemented as: run it
  > sandboxed, validate each emitted node record against its `io_contract`, and **halt (kill the
  > process group)** on a violation — which enforces "do not pass malformed data downstream" rather
  > than declining to route it. See `internal/executor`'s package doc.
  ```
  walking the node graph in declared ordering; pass each node's output through the typed I/O
  contract before it feeds a downstream node.
  ```
- [x] 3.8 Halt the run with a typed error naming node + dimension on any typed-contract violation;
  ```
  do not pass malformed data downstream.
  ```
- [x] 3.9 Persist `transform` (diff blob hash, build status, worktree ref) and `run`/`node_execution`
  ```
  rows (status, input/output blob hashes, idempotency key); terminal status =
  succeeded / failed / halted / build-rejected.
  ```
- [ ] 3.10 **Always-reviewable diff + clean rollback:** surface every applied change as a
  ```
  reviewable diff/PR; nothing merges to the default branch without the build+eval+regression
  gate and (below Autonomous) human approval; implement rollback as a single `git revert`.
  ```
  > **Un-ticked. Diff ✅ / revert ✅ / PR ❌ — and the PR half is blocked on decisions above the
  > implementer's authority, so it was escalated rather than half-built** (禁止清单 #10 半成品,
  > #15 建了等未来用).
  > Delivered: the diff is content-hashed, in the object store, on its own variant branch, rendered in
  > the UI; rollback is a single `git revert` (`apply.go:207`, one commit by design). "Nothing merges
  > without approval" holds only **vacuously — nothing merges at all.**
  > **The gap is wider than "the PR call is missing."** The repo is never connected to a forge:
  >
  > | # | Needed | Reality |
  > |---|---|---|
  > | 1 | Forge identity | `workflow.repo_url` exists and `cmd/discover` really derives it — but **no Go code reads it back**; every fixture is `local://demo` |
  > | 2 | The branch on the forge | ❌ **No push path.** `worktree.NewPool` clones `--bare --local` from a filesystem path; the variant branch exists **only in the local mirror** |
  > | 3 | A write-scoped forge token | ❌ `Secrets` is provider-scoped; a forge token is a different credential kind |
  > | 4 | Somewhere to record the PR URL | ❌ **Structurally blocked** — `transform` is immutable by DB trigger (`0004:transform_immutable`), and the PR is opened *after* insert (build must pass first), so the `UPDATE` is rejected by design |
  > | 5 | A consumer | ✅ **Now exists** — `internal/submit` (see 7.2); step 7 is where PR-opening belongs |
  >
  > **Two blockers require sign-off:** (a) #2/#3 mean **pushing to the customer's repo with a
  > write-scoped token** — the highest-blast-radius action in the system, and ADR-002 spent its whole
  > argument refusing exactly this class of customer-side reach (L1/L2). That needs its own ADR.
  > (b) #4 forces a **one-way door**: recording the PR URL needs either a new table (🔴
  > careful-table-creation demands ≥2 alternatives + sign-off) or relaxing `transform`'s immutability
  > (breaks `TestPG_Immutability_*` and a stated invariant).
  >
  > | Option | Short-term | Long-term | UX | Architecture |
  > |---|---|---|---|---|
  > | **A.** Push + PR to the customer's real remote | High | **Write access to every customer repo, forever**; token compromise = supply-chain event | The real Dependabot experience ADR-001 promises | Contradicts ADR-002's L1/L2 reasoning; needs a new ADR |
  > | **B.** PR to a fork/mirror we own | Medium-high | Fork drift; a bot-fork PR is a weaker review artifact | Reviewable but second-class | Avoids customer write scope |
  > | **C.** Defer; seam only *(current)* | ~0 | 3.10 stays partial — honestly marked | User reviews diff + branch in the UI (works today) | Preserves optionality; a `Secrets`-shaped `PullRequests` seam drops in cleanly |
  >
  > ~~**Recommendation: C now, then B or A behind a new ADR.**~~ *(superseded — see below; the ADR
  > chose neither B nor A.)* The seam is easy; the blockers are not.
  >
  > ✅ **Resolved by [ADR-005](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md)**
  > (2026-07-22), which chose **neither B nor A**. The framing above asks *"how do **we** get write
  > access"*, and all three options follow from it. Re-asked as *"**who** should hold the
  > credential"*, a fourth option appears: **the customer's CI already holds one** — repo-scoped,
  > short-lived, forge-rotated. CI-mediated delivery is now the default and the platform holds **no**
  > forge credential; the hosted Git App is opt-in, per-repo and revocable. Blocker #4 is resolved by
  > a **separate append-only `delivery` record** rather than by relaxing `transform` immutability.
  > Owned by **[P12](../p12-forge-delivery/)**; the CI hook it runs inside is
  > **[P11](../p11-cli-ci-integration/)**.



## 4. Backend + System Designer + AI — Provider gateway (unaffected by ADR-001)

- [x] 4.1 Implement a LiteLLM-style gateway with a normalized request/response shape across
  ```
  providers (Anthropic, OpenAI, Bedrock at minimum).
  ```
- [x] 4.2 **Model-swap transparency:** swapping a node's model **within a provider** requires the
  ```
  codemod to rewrite only its `model_ref` at the call site; no other workflow logic changes.
  Cross-provider swap at a user call site is refused with a typed error (ADR-002), not emitted.
  Platform-side calls are normalized across Anthropic / OpenAI / Bedrock by the gateway.
  ```
  > **Reworded — as originally written this task asked for something the system must never do.**
  > `rewrite.go:59` refuses a cross-provider swap, and `TestGenerate_RejectsProviderSwapAsACallSiteRewrite`
  > asserts the refusal; the E2E test asserts the *opposite* of the old task text. The refusal is now
  > **ratified** by [ADR-002](../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md)
  > rather than being a drift from spec — and `rewrite.go` now cites ADR-002 instead of citing FR12
  > while doing the opposite of it.
  > **Why the old wording was unachievable, not merely expensive:** it holds only if the codemod routes
  > customer call sites through our gateway, and then either the gateway dependency **merges** (we have
  > rewritten the customer's production architecture — not "behavior-preserving except for the intended
  > change") or it is **stripped before merge** (we measured code that is not the code that ships —
  > precisely the flaw that killed the shim model in ADR-001). There is no third branch.
  > **🔴 Capability boundary — sales-operations must not promise cross-provider swap.** Honest
  > statement: *"we swap models within a provider today; cross-provider swap requires rewriting the SDK
  > call and is not shipped."* It is a per-(source-SDK, target-SDK) pair of rewriters, correctly sized
  > and now explicitly **not** hiding inside "the gateway makes swaps transparent."
- [x] 4.3 Ensure the `ModelEntry` + gateway interface does not preclude later **model tiering**
  ```
  (cost/complexity-aware selection per node in P6).
  ```
- [x] 4.4 Per-call **timeout** (default 60 s, per-model override) + bounded exponential backoff
  ```
  with jitter on transient failures.
  ```
- [x] 4.5 Source provider credentials from a **secrets manager**; inject at call time; scrub from
  ```
  logs, traces, error messages, generated diffs, and run records.
  ```
  > **Now real — it was env vars.** `Secrets` had only `EnvSecrets` (plain `os.Getenv`) and
  > `StaticSecrets` (a map); the code was candid that it was "the boundary a real one plugs into,"
  > but the task claimed the manager itself. `internal/providergateway/awssecrets.go` is a real
  > `aws-sdk-go-v2/service/secretsmanager` client (`GetSecretValue` at call time, TTL cache, fail-closed).
  > **Choice of manager, arbitrated (八级法则 L2):** the candidates **tie at L1** — Vault/GCP/AWS all
  > hold a secret properly — so the decision moves down and settles at **L4 (运维)**, not L8: Vault
  > adds an agent, a token to renew, a seal to unseal; AWS SM adds a client to a dependency already
  > present (Bedrock signs SigV4 with it). "Lowest marginal cost" is L8 and **per L3 can never be the
  > reason.** The L1 tiebreak that actually mattered: **AWS SM authenticates ambiently
  > (IRSA/instance role/SSO), so wiring it creates no bootstrap secret** — a manager reached with a
  > long-lived key in an env var has *moved* the secret, not removed it.
  > **No env fallback, deliberately** (禁止静默回落 at L1): a "manager with env fallback" is an env
  > source with extra steps — the manager breaks, calls keep succeeding off a stale var, and `/readyz`
  > lies for weeks. An unknown `HEROS_SECRETS_SOURCE` **fails closed** rather than defaulting.
  > **Preserved:** call-time resolution (nothing held between calls) and `scrubErr`'s deliberate break
  > of the `Unwrap` chain. Cache is memory-only — never disk/row/log — so `TestPG_NoSecretReachesTheStores`
  > stays true *by construction*. Cost: 5-min rotation latency, bounded and stated.
  > **!!! Unverified without an AWS account:** the client is real (asserted `AWS4-HMAC-SHA256` SigV4 +
  > `X-Amz-Target` AWS JSON 1.1) but the **endpoint is an httptest replay**. Unproven: that a real IAM
  > policy grants `GetSecretValue`, a real ARN resolves, a real KMS key decrypts, real endpoint TLS.
  > Recorded in `secrets-baseline.md` §6.
  > **⚠️ Review-worthy:** pulling in `secretsmanager` bumped `aws-sdk-go-v2` v1.36.3 → v1.42.1 **under
  > the Bedrock SigV4 path**. `TestComplete_BedrockRequestIsSignedAndAddressed` passes.



## 5. Backend — Idempotency & reproducibility

- [x] 5.1 Derive an idempotency key per node invocation (`{run_id, node_id, attempt_group}`);
  ```
  gateway de-dupes so a retried call is not billed twice.
  ```
- [x] 5.2 Unique constraint `(run_id, node_id, attempt_group)` so a double-write race is a caught
  ```
  conflict, not a duplicate row.
  ```
- [x] 5.3 Thread `seed` from the Variant Spec → gateway → every stochastic step; assert identical
  ```
  seed propagation for the same `config_hash` + seed.
  ```
- [x] 5.4 Reproducibility test: run the same `{config_hash, source_revision, seed}` twice →
  ```
  byte-identical generated diff, deterministic build, and identical seed reaching each provider
  call.
  ```



## 6. DevOps — Secrets, worktree pool, queue, operability

- [x] 6.1 Wire the secrets manager; verify no secret appears in code, DB rows, logs, generated
  ```
  diffs, or run records (log-scrub test). Surface the active secrets source on /readyz.
  ```
  > **Now real** — see 4.5 for the manager itself. The scrub half was always strong:
  > `TestPG_NoSecretReachesTheStores` sweeps **every text column** via `information_schema`, with a
  > planted-secret control (`TestPG_TheSweepActuallyDetectsAPlantedSecret`) proving the sweep can go
  > red. Extended with `TestPG_NoSecretFromTheSecretsManagerReachesTheStores`, which drives a real
  > SM-sourced credential through a real failing call and then sweeps. **All four verified against live
  > Postgres in this session** (the sweep extension was written but never executed by its author).
  > **health-signal-surface (🔴):** `GET /readyz` now reports `secrets_source` — the active choice is
  > **externally readable**, not a log line. `Describe()` is on the interface rather than an optional
  > type-assert, deliberately: an optional describer lets a future implementation be *silently*
  > anonymous, and that failure is invisible because an un-describable source still serves credentials
  > fine. The compiler asking every time is the only version that cannot rot. Verified live:
  > `{"secrets_source":{"kind":"aws-secrets-manager","detail":"region eu-west-1; ..."},"status":"ready"}`,
  > and a typo'd source exits 1 at boot rather than starting.
- [x] 6.2 Stand up the **worktree pool + build-cache** infrastructure (checkout, apply, build,
  ```
  evict) with least-privilege access to a read-only clone of the target repo.
  ```
- [x] 6.3 Seed the **run queue** for run dispatch (at-least-once dispatch + idempotent execution).
- [x] 6.4 Structure run/node/transform records so P2.5 OTel instrumentation attaches at the gateway
  ```
  with zero application change. The transform/build/run records carry P0's seven tags so P2.5
  can attach there; the observer seam on that path is P2.5's to add.
  ```
  > **Reworded — "and the transform/build/run path" was not true.** `providergateway/observe.go:10-45`
  > is a genuine `Observer` seam whose `CallInfo` fields are deliberately mapped to P0's seven-tag
  > contract. But `grep Observer internal/worktree internal/executor` returns **nothing**: the records
  > carry the tags, yet attaching OTel to transform/build/run would require an application edit — which
  > is exactly what "zero application change" forbids.
  > **Scoped to P2.5 rather than pulled forward**: instrumenting that path is P2.5's stated job, and
  > doing it here to make a checkbox true would dissolve the phase boundary. What P2 owes P2.5 is
  > records shaped so the seam *can* attach — that part is done.



## 7. Frontend + Product — Bare run/review/inspect UI

- [x] 7.1 Product: the configure-a-node journey — four independent override dimensions, each → a
  ```
  versioned registry entry; "no override" is a legible default. The applied change is a
  **reviewable diff/PR**, not a silent runtime effect. Design the unhappy path (unresolved ref,
  build-rejected transform, contract halt) first.
  ```
- [x] 7.2 Frontend: submit a Variant Spec → **review the generated diff** → watch the transformed
  ```
  copy run → view per-node I/O.
  ```
  > **Submit is now real; it did not exist.** Previously `POST /api/p2/specs/resolve` only structurally
  > validated ("resolving refs needs the registries, which the API does not hold"), no endpoint
  > persisted a spec / generated a transform / enqueued a run, and the user **hand-pasted a
  > `config_hash` and a `run_id`** — the three panels were not a connected flow.
  > **Root cause:** the whole sequence existed but lived *only* inside `internal/e2e`'s test helper —
  > the product had every part of submit and no submit. Promoted to production as `internal/submit`,
  > the single path: discover IR → resolve (or fail closed) → persist → generate codemod → apply+build
  > on an isolated branch → record transform → record run → enqueue. Steps 1–2 write nothing, which
  > makes **fail-closed structural rather than disciplinary**.
  > **The registries problem was fixed by giving the domain a front door, not by handing registries to
  > the HTTP layer** — `internal/api` still decodes → delegates → maps status codes. No second resolve
  > path (禁止分裂 source-of-truth).
  > **careful-api-creation (🔴):** the load-bearing question — *can `resolve` take an optional field?* —
  > **No, on safety, not tidiness.** `resolve` is a free side-effect-free validator called on every
  > keystroke; submit writes four tables, runs git, runs a compiler, and enqueues billable work. A
  > boolean flipping "reads nothing" → "writes everything" means one wrong default or one replayed
  > request causes a build and a bill. Justification is on the handler.
  > ⚠️ **The principle's step 3 says hand the endpoint decision to the user before building. That gate
  > was pre-empted** — flagged rather than glossed.
  > **Idempotency:** `run_id = f(config_hash, source_revision, seed)` — not invented here; `0005`'s own
  > comment calls that triple the reproducibility unit. Same three = same experiment (collapses onto
  > the first run); **different seed = different experiment** (proved separately — collapsing it would
  > break variance measurement, which is why seed exists).
  > **UI:** reused `renderFailure`, `DIMENSIONS`, `.row`, `.chip`, existing `state-*` colors — no new
  > colors or spacing (禁止即兴定样式). Terminal status still read from the record; the anti-drift grep
  > test stays green.
  > **A real UX bug found by driving the page, not by tests:** on 503 it said *"Fix the spec and submit
  > again"*, misdirecting the user to fix a spec that was fine. The fail-closed reassurance now applies
  > to 400 only — "nothing was persisted" is a claim we cannot make when *our* side failed halfway.
  > **!!! Nothing consumes the queue.** No worker exists (task 6.3 is literally "**Seed** the run
  > queue"). In a real deployment a submitted run sits at `running` forever and the UI's watch polls
  > indefinitely. Submit→enqueue is complete and correct; **"watch the transformed copy run" only fully
  > closes when a worker exists.** Not built — out of scope, and distinguishing "queued, no worker" from
  > "executing" needs a schema change.
  > **!!! `launch.StartAgentd` never calls `MountP2`** — P2 is served only by `cmd/p2uidemo`.
  > Pre-existing; wiring it needs a target-repo config decision.
  > **Was PARTIAL when first marked done; now complete.** Diff review, watch-run and per-node I/O
  > were real, but **submit did not exist**: `POST /api/p2/specs/resolve` only validated a spec's
  > STRUCTURE, nothing persisted a spec / generated a transform / enqueued a run outside
  > `internal/e2e`'s test helper, and the user hand-pasted a `config_hash` and a `run_id` that
  > nothing would ever hand them. The three panels were three tools, not a flow.
  >
  > **Why it was partial:** the sequence existed only as a test helper, so the product had every
  > part of submit and no submit. The API also held no registries, which is why resolve was
  > structural-only and said so.
  >
  > **What closed it:** `internal/submit` is that sequence promoted to production code — the ONE
  > orchestrator (IR at `source_revision` → resolve → persist → generate → apply+build → record →
  > start run → enqueue), holding the registries so the HTTP layer does not. `POST
  > /api/p2/specs/submit` is its front door (justification for the new endpoint surface is in the
  > handler's doc comment, per careful-api-creation), and it returns the `config_hash` and `run_id`
  > the page reads straight into panels 2 and 3. Proofs: `internal/submit/submit_pgproof_test.go`
  > (live Postgres, real git, real `go build`), which asserts the spec / transform / run / queue
  > rows by RE-READING them through the consumption path.
  >
  > **Idempotency:** `run_id` is derived from `{config_hash, source_revision, seed}` —
  > `executor.RunIDFor` — so re-submitting one experiment collapses onto the existing run (no
  > duplicate rows, no second build, no second bill) while a new seed still starts a new run.
- [x] 7.3 First-class **loading / error / empty / success** states; read terminal status from the
  ```
  run/transform records (no derived state that drifts); a build-rejected transform is a
  distinct, legible state.
  ```
- [x] 7.4 Surface *which node* and *which dimension* failed on a fail-closed resolution, a
  ```
  build-rejected transform, or a contract halt.
  ```



## 8. Testing & review

- [x] 8.1 Fixture: hardcoded 3–5 node graph (buildable target repo at a pinned `source_revision`) +
  ```
  known-good Variant Spec + a model-override variant + a prompt-override variant.
  ```
- [x] 8.2 Integration tests (real Postgres + object store + git worktrees, stubbed providers):
  ```
  deterministic diff, build-preserving rejection (a spec that would break the build is rejected
  before running), behavior-preserving minimal diff, isolated application (user tree untouched),
  reproducibility, idempotency (one charge / one row under forced retry), provider swap,
  fail-closed on dangling ref, contract halt, clean `git revert` rollback.
  ```
- [x] 8.3 UI verification: drive the bare UI against a live (stubbed-provider) run; confirm submit →
  ```
  diff review → per-node I/O → terminal + error + empty + build-rejected states.
  ```
- [x] 8.4 Adversarial self-review: unresolved ref, mutated version, non-deterministic diff, transform
  ```
  that breaks the build, diff with incidental edits, user working tree mutated in place, secret
  in logs/diffs, non-idempotent retry, seed not propagated, malformed I/O passed downstream,
  rollback that leaves residue.
  ```
- [ ] 8.5 Confirm M2 exit checklist (PRD §13) is green.
  > **Un-ticked. The checklist is NOT green — 12/14 met, 2 partial** — so a task that says "confirm it
  > is green" cannot be ticked. It previously carried `[x]` *while its own note said "not green"*: the
  > checkbox contradicted the deliverable it pointed at. The review under it was honest; the mark was
  > not. See [`docs/decisions/m2-exit-review.md`](../../../docs/decisions/m2-exit-review.md).
  >
  > **The blocking question is RESOLVED.** FR12 vs ADR-001 ("does the gateway sit in a run's call
  > path?") is settled by [ADR-002](../../../docs/adr/ADR-002-provider-gateway-serves-platform-callers.md):
  > the gateway serves **platform** callers; the transformed program calls its own SDKs. FR12 and the
  > runtime spec are amended accordingly.
  >
  > **The 2 remaining partials, and what each needs:**
  > - **Criterion 6 / Deviation B — provider swap.** No longer a *gap*; ADR-002 ratifies the refusal
  >   and criterion 6 is worded from the pre-ADR-001 design. **To retire: reword PRD §13 criterion 6**
  >   to "within-provider model swap", matching the amended FR12. Not a code change.
  > - **Criterion 13 / Deviation C — no PR is opened.** A real gap, escalated with options under 3.10.
  >   Needs sign-off on (a) pushing to a customer repo with a write-scoped token — an ADR-sized L1/L2
  >   decision — and (b) the one-way door of where a PR URL is recorded (`transform` is immutable by
  >   DB trigger).
  >
  > **2b (prompt override) closed 2026-07-17 — Deviation A retired.** It was previously recorded as
  > "refused, FR5-sanctioned, not applied". That was true of the ENGINE at the time, but the e2e test
  > backing it passed for the wrong reason: **no node in the fixture set `Messages` at all**, so the
  > refusal fired on "argument not present" and never reached the boundary its comment claimed to
  > prove. The SDK stub compounded it — messages were typed `Message{Role string; Content string}`
  > (two bare string literals), a shape that also refuses, but for a reason **no real Go SDK
  > produces**: real SDKs carry the role in a *function* (`NewUserMessage`). The fixture now models
  > the real shape, so the compiler type-checks the rewrite as it would in reality, and a prompt
  > override **takes effect**: 1-line diff, byte-identical on regeneration, builds, runs, terminal
  > `succeeded` in Postgres. The refusal boundary is kept and **each case now pins its reason**, which
  > is the assertion whose absence let this go unnoticed.
  > **Deviation A's stated premise was itself false** — "rewriting means synthesizing SDK-shaped code"
  > — and its proposed fix (a per-SDK message-construction rewriter) is strictly worse than what now
  > exists. The falsehood lived in the fixture, not the engine.
  >
  > **2b (prompt override) closed 2026-07-17 — Deviation A retired.** It was previously recorded as
  > "refused, FR5-sanctioned, not applied". That was true of the ENGINE at the time, but the e2e test
  > backing it passed for the wrong reason: **no node in the fixture set `Messages` at all**, so the
  > refusal fired on "argument not present" and never reached the boundary its comment claimed to
  > prove. The SDK stub compounded it — messages were typed `Message{Role string; Content string}`
  > (two bare string literals), a shape that also refuses, but for a reason **no real Go SDK
  > produces**: real SDKs carry the role in a *function* (`NewUserMessage`). The fixture now models
  > the real shape, so the compiler type-checks the rewrite as it would in reality, and a prompt
  > override **takes effect**: 1-line diff, byte-identical on regeneration, builds, runs, terminal
  > `succeeded` in Postgres. The refusal boundary is kept and **each case now pins its reason**, which
  > is the assertion whose absence let this go unnoticed.