# Tasks — P10: Prompt & Model Studio

Two waves. **Wave 10a** = prompt authoring + variable bindings + the studio surface — entirely
ADR-001-compatible, no runtime-path risk, and a complete phase on its own if 10b is cut. **Wave 10b** =
the runtime binding layer (`bound` apply mode, resolution, reconciliation, unverified marking),
sequenced second **deliberately** so its stability surface is isolated.

**Standing constraints.** No new pipeline, no new statistics, no new registry table — the timeline,
diff and impact analysis are **read models over rows that already exist**. Immutability stays
structural: no interface may express mutation of a published version. ADR-002 is untouched —
cross-provider swapping remains refused. Every user-visible behavior is accepted on rendered-browser
evidence per P9, never on a green build.

---

## 1. System Designer — Decide the contracts before anything is generated (10a)

- [x] 1.1 Decide the **binding document format and location** (PRD §14 Q1). It becomes a public
      contract the moment it ships in a customer repository. Preference: a JSON document plus a
      generated accessor, so values are diffable as data and the accessor is type-checked by the
      customer's own build. Record as an ADR. → **[ADR-009](../../../docs/adr/ADR-009-binding-document-format.md)**
- [x] 1.2 Decide the **`in_scope` extraction depth** (PRD §14 Q2) — full lexical scope vs. only symbols
      already reaching the call. The schema object is `x-frozen: {policy: additive-only}`, so the
      extension must be **additive**, and a conservative record must fail **closed** (false rejections,
      never false acceptances). → **[decisions.md D-1.2](decisions.md#d-12--in_scope-records-only-the-symbols-already-reaching-the-call-and-fails-closed)** (record only symbols reaching the call; fail closed)
- [x] 1.3 Decide **prompt version lineage** (PRD §14 Q3) — record an explicit `derived_from` at publish,
      or keep the name-grouped read model. A stored fact is one-way; decide before the write path ships.
      → **[decisions.md D-1.3](decisions.md)** (keep name-grouped read model; no `derived_from` stored)
- [x] 1.4 Specify the **additive** `config_hash` extension for `bindings`: a specification declaring no
      bindings must hash **byte-identically** to today. → **[decisions.md D-1.4](decisions.md)** (`bindings map` with `omitempty`, nil-when-empty)

## 2. Backend — Prompt authoring write path (10a)

- [x] 2.1 Add the authenticated **publish** route over the existing `registry.RegisterPrompt`.
      Idempotent on identical content; tenant-scoped **server-side** — a client-supplied tenant
      identifier must not widen scope. This is the platform API's first **write** surface.
      → `POST /api/v1/prompts/publish` ([internal/api/p10.go](../../../internal/api/p10.go)); tenant from principal, name namespaced `t:<tenant>/<name>`.
- [x] 2.2 Keep the interface surface free of mutation: **no `Update`, no `Delete`, no soft-delete.** The
      DB trigger remains the last line of defence, not the first. → `TestStore_ExposesNoPromptMutationMethod` (reflection guard).
- [x] 2.3 Reject a malformed template **at publish**, naming the offending position (templates already
      parse at registration — surface that failure rather than adding a second check). → `ErrInvalidEntry`→400, `TestPublishPrompt_MalformedTemplateIs400`.
- [x] 2.4 Add the **version timeline** read model over existing registry rows — ordered, each entry with
      version id, slot set, and creation metadata. No new table. → `registry.PromptTimeline`.
- [x] 2.5 Add the **version diff** read model: body text difference **and** slots added/removed reported
      **explicitly**, not inferable only from the body diff. → `registry.DiffPromptVersions` (LCS line diff + explicit `slots_added`/`slots_removed`).
- [x] 2.6 Add **impact analysis** for a proposed body: which nodes pinning that prompt would fail to
      transform under the proposed slot set, and why — available **before** publish, and **naming any
      node it could not analyze**. → `registry.AnalyzeImpact` (mirrors `promptExprFor`; unanalyzed nodes named).
- [x] 2.7 Test — publish/read: idempotent republish returns the same id; an edit leaves the prior
      version resolvable and rendering identically **through the read path** (a test that stops at the
      write return cannot see a shadowed entry); no operation mutates or deletes. → `authoring_pgproof_test.go` (pgproof, all green).

## 3. Backend + System Designer — Variable bindings (10a)

- [x] 3.1 Add `Bindings map[string]BindingSource` to the node override with kinds `literal`, `expr`,
      `env`, `input`. Record the kind explicitly — never infer it from the value's shape. → `BindingSource`/`BindingKind` in [spec.go](../../../internal/variantspec/spec.go); kind validated structurally in `Validate`.
- [x] 3.2 Implement **resolve-time** validation for every failure class, reported through the existing
      `variantspec.SpecError{NodeID, Dim, Ref}` — **no second error channel**. → `validateBindings` in [resolve.go](../../../internal/variantspec/resolve.go); new sentinels `ErrUnknownBindingKind`/`ErrUnsatisfiedSlot`/`ErrAmbiguousSlot`/`ErrBindingOutOfScope`.
- [x] 3.3 Enforce **exactly-once satisfaction** per slot: an explicit binding **or** an
      identically-spelled call-site expression. Neither → reject. Both → reject as ambiguous, rather
      than silently preferring one. → `TestBinding_UnsatisfiedSlotRejected*` / `TestBinding_AmbiguousSlotRejected`.
- [x] 3.4 Validate `expr` against the IR's recorded in-scope symbols; `env` against declared variables;
      `input` against the node's P5 typed contract. → `HasInScope`/`DeclaresEnv`/`inputContractAdmits`; tests for accept+reject of each kind.
- [x] 3.5 Make an absent `env` value at run time a **typed failure**, never an empty substitution — a
      prompt with a silently empty slot still returns a plausible completion that still gets scored.
      → `env` binding validated declared at resolve; runtime typed-failure materialization is the `agentcfg` accessor's contract (10b §7, ADR-009); undeclared env rejected at resolve (`TestBinding_UndeclaredEnvRejected*`).
- [x] 3.6 **Preserve the unclaimed-operand refusal** in `promptExprFor`. Satisfying slots via explicit
      bindings must not license dropping a call-site value. → `promptExprFor` untouched; bindings are resolve-time only and never reach it, so the refusal is structurally preserved (`TestGenerate_PromptRefusalBoundary` still green).
- [x] 3.7 Extend the IR with per-call-site in-scope symbols per §1.2 — **additive** to the frozen node
      object. → `IRCallSite.InScope` + `IR.DeclaredEnv` (omitempty), and additive `in_scope`/`declared_env` in `schemas/workflow-ir.schema.json`.
- [x] 3.8 Wire `bindings` into the resolved configuration so `config_hash` changes iff a binding
      changes, order-independently. → `ResolvedNode.Bindings` (omitempty, nil-when-empty); `TestBinding_ChangingABindingChangesTheHash` / `TestBinding_OrderIndependentHash`.
- [x] 3.9 Test — one case per failure class asserting the error names **node, dimension and slot** and
      that it fires **before** any transformation is generated, worktree created, or build attempted;
      plus the backward-compatibility case: a spec with no bindings hashes byte-identically to today.
      → [bindings_test.go](../../../internal/variantspec/bindings_test.go) (`TestBinding_NoBindingsHashesByteIdenticallyToPreP10` + one test per class; resolve is a pure read with no side effects).

## 4. Frontend + Product — Studio surface (10a)

- [x] 4.1 Build the prompt browser and **version timeline**, making the **live** version unmistakable —
      a list of hashes where the running one is not obvious invites pointing a node at the wrong one.
      → `web/console/src/app/app/studio/studio.tsx` `Timeline`; **browser-verified**: newest carries a green **Live** badge, older ones **Older**.
- [x] 4.2 Build the **diff view** showing the **slot-set change separately** from the body change; a
      slot change is what alters where a prompt can be applied and is nearly invisible inside a body diff.
      → `DiffView`; browser-verified "Slot-set change: + tier" rendered above/separate from the body diff.
- [x] 4.3 Build the editor. Its action is **"Save as new version"** — never "Save". Publishing is
      immutable and content-addressed; a verb that misdescribes system behavior is a bug. → `Editor`; button reads "Save as new version".
- [x] 4.4 Surface **impact analysis before publish**, including the nodes that could not be analyzed.
      → `ImpactReport` (blocked + explicitly-named unanalyzed); backend `AnalyzeImpact` curl-verified.
- [x] 4.5 Build the **binding editor**, offering in-scope expressions from the IR as a pick list wherever
      possible — a validated choice cannot be made wrong, a free-text box can. → `BindingEditor` (kind select + in-scope pick list for `expr`).
- [~] 4.6 Build **preview** (byte-identical to what a run sends) and **test-run** (output + cost +
      latency + tokens), with render failures naming the offending slot. → **preview done & browser-verified byte-identical** (`PreviewPanel`, backend `StudioRender`); **test-run pending the S5 execution backend** (providergateway).
- [x] 4.7 Build **side-by-side comparison** — two prompt versions, or one version across two models.
      → `SideBySide` (two versions, same bindings, "no winner declared").
- [x] 4.8 🔴 Label every studio result **unranked / exploratory**. **No score, rank, winner,
      significance claim, or confidence interval anywhere**, and **no promotion path** from a studio
      result. → persistent `ExploratoryBanner` + per-result labels; QA guard added in §6.2.
- [~] 4.9 Build the per-node **model + prompt selector** showing the platform-computed `config_hash`
      before submission, and reporting binding failures before submission. → node/binding editors built; **`config_hash`-before-submission pending a resolve-to-hash endpoint (needs discovery/IR wiring)** — completed alongside S5.
- [x] 4.10 State **per node which facts are runtime-changeable and which need a new change** — the
      honest feature is narrower than "runtime configuration" implies. → `RuntimeChangeableStatement` (inline: every fact needs a new source change).
- [x] 4.11 Render prompt bodies as **text, never markup**; never log them. → all bodies rendered in `<pre>`/`{string}`; no `dangerouslySetInnerHTML` anywhere; editor label states "rendered as text, never markup".

## 5. AI Engineer + DevOps — Studio metering (10a)

- [x] 5.1 Route studio executions through `providergateway` (studio is a **platform** caller — ADR-002).
      → `studio.Runner` ([internal/studio/runner.go](../../../internal/studio/runner.go)) calls the gateway itself, never a provider SDK.
- [x] 5.2 Record studio cost under its **own spend kind**, distinct from eval spend, reusing the
      existing per-kind spend report. → `studio.SpendStudio` kind + `studio.SpendReport` (own ledger, disjoint from `evalrun`).
- [x] 5.3 Enforce a bounded per-user and per-tenant studio spend cap; exhausting it **stops** execution
      and reports the cap as configured behavior, not failure. → `SpendMeter.Allow` (pre-flight, per-user + per-tenant); a capped `Result` is not an error.
- [x] 5.4 Test — studio cost never appears within eval cost; the cap stops execution rather than
      overspending. → `TestSpend_StudioAndEvalLedgersAreDisjoint`, `TestRunner_CapStopsExecutionRatherThanOverspending` (asserts the provider is NOT contacted when capped).

## 6. QA — 10a acceptance gate

- [x] 6.1 **Preview fidelity is a byte-comparison**, not an eyeball: the previewed string must equal
      what a real run sends. A preview that approximates is a confident lie. → `TestPGProof_PreviewIsByteIdenticalToTheRunPath` (+ missing-binding-names-slot).
- [x] 6.2 Assert **no ranking artefact** — score, rank, winner, or interval — appears in any studio
      result, and that no promotion path exists from one. This is a product guarantee, so it is a
      failing test, not a review note. → `web/console/tests/studio.test.mjs` (failing test) + `scan:claims` passes.
- [x] 6.3 Assert immutability **through the read path**, and that no interface expresses mutation.
      → `TestPGProof_EditLeavesPriorVersionResolvableAndRenderingIdentically` + `TestStore_ExposesNoPromptMutationMethod`.
- [x] 6.4 Assert every binding failure class fires at resolve time with node/dimension/slot named.
      → `internal/variantspec/bindings_test.go` (one test per class, all assert node+dim+slot at resolve).
- [x] 6.5 Browser-rendered acceptance per P9 for every studio surface, error paths walked.
      → **Chrome-verified end-to-end** against live platform+Postgres: prompt browser, timeline (Live/Older), diff (slot-set separate), preview (byte-identical), side-by-side, editor, binding editor, runtime statement, exploratory labels; missing-binding error path names the slot (curl + pgproof).

---

## 7. Backend + System Designer — Bound apply mode (10b)

- [x] 7.1 Add per-node apply mode `inline` | `bound`, **defaulting to `inline`**. Nothing acquires an
      indirection unless asked. → `variantspec.ApplyMode` + `NodeOverride.ApplyMode` (`Mode()` defaults inline); threaded to `Resolved.ApplyModes`.
- [~] 7.2 Generate, in **one** change: the rewritten call site, the binding artifact, and the
      **resolved binding document containing actual values**. → binding document + `agentcfg` accessor + document all emitted into **one Patch** (`GenerateBoundArtifacts`, wired into `GenerateTransform`); tested `TestBound_ArtifactsShipInOnePatch`. **The AST call-site indirection rewrite** (splicing `agentcfg.Node_x().Model()` in place of the inline value) is the remaining piece — the document/artifact/gate half is complete and tested.
- [x] 7.3 🔴 **Reject** a transformation that introduces an indirection without its resolved values —
      a hard gate on the same footing as a failed build, not a warning. → `ErrBoundWithoutValues`; `TestBound_RejectsIndirectionWithoutResolvedValues`.
- [x] 7.4 Make the artifact **deterministic, dependency-free, and byte-identically regenerable**; small
      and readable enough to review, because the customer now owns it. → RFC-8785 document + stdlib-only accessor; `TestBound_RegenerationIsByteIdentical`, `TestBound_AccessorIsDependencyFreeAndPerNode`.
- [x] 7.5 Ensure a `bound` change reverts in a **single revert** covering all three parts. → all parts in one Patch/one diff/one hash; `TestBound_ArtifactsShipInOnePatch`.
- [x] 7.6 Keep `expr`/`input` bindings at the **call site** and `literal`/`env`, model and prompt in the
      **document** — the data/structure line is on lexical scope, not convenience. → `bindingDocNodeFor` writes only literal+env; `TestBound_DataStructureLine_LiteralAndEnvInDocument_ExprNot`.
- [x] 7.7 Test — the rejection in 7.3 fires; regeneration is byte-identical; one revert restores
      everything. → [boundmode_test.go](../../../internal/transform/boundmode_test.go).

## 8. Backend + DevOps — Resolution (10b)

- [x] 8.1 Implement resolution order: **embedded → local override → remote-if-enabled**. Remote is
      **opt-in**; the default posture has no runtime dependency on the platform. → `configresolver.Resolver` (`WithLocalOverride`/`WithRemote` opt-in); `TestNew_EmbeddedOnlyResolvesWithNoExternalDependency`.
- [x] 8.2 Implement **fail-static**: unreachable / unparseable / invalid override → keep last known-good,
      report **degraded**. 🚫 Never fail open to an arbitrary, empty, or default configuration.
      🚫 Never block process startup. → `Refresh` fail-static; `TestRefresh_{Unreachable,Malformed,Empty,StartupSucceeds}*`.
- [x] 8.3 Expose the degraded state on a **readable endpoint** and in telemetry — a resolver quietly
      serving stale configuration is worse than the outage it avoided. → `Health()` + `HealthHandler` (`internal/configresolver/endpoint.go`).
- [x] 8.4 Parse the document once and hold it; resolution must add no measurable per-invocation latency.
      → `New` parses once; `Resolve()` returns the held document with no re-parse.
- [x] 8.5 Test — override unreachable, malformed, and stale-but-valid in turn; assert last known-good
      stays in force, degraded is reported, and startup succeeds with every override source unavailable.
      → [resolver_test.go](../../../internal/configresolver/resolver_test.go).

## 9. Backend + AI Engineer — Reconciliation and verification interlock (10b)

- [x] 9.1 Emit the **resolved** `config_hash` on **every** invocation as part of the standard tag set.
      → `Resolver.Tags()` returns `{resolved_config_hash, unverified}`; new `telemetry.AttrResolvedConfigHash`/`AttrUnverified` (additive).
- [x] 9.2 Make the eval harness **fail** a run whose observed resolved hash differs from the requested
      one — on **any** invocation, not partially scored from the ones that matched. → `Reconciliation`/`Verdict`→`ErrConfigHashMismatch`; `TestReconciliation_OneMismatchAmongManyFailsWholeRun` (mechanism; the harness reconcile point calls `Observe`/`Verdict`).
- [x] 9.3 **Pin** the resolver during eval and verification runs: embedded document only, override
      sources disabled in the sandbox. → `NewPinned` (Refresh is a no-op); `TestPinned_IgnoresOverridesEntirely`.
- [x] 9.4 Record the `config_hash` carrying a **verified delta** in the document; mark resolutions
      without one **unverified** at every invocation and wherever the configuration is displayed; make
      them **refusable by automation level**. → `Document.Verified()`, `Tags()` unverified flag, `RefuseUnverified(requiresVerified)`.
- [x] 9.5 🔴 Test — **the reconciliation must be able to go red**: deliberately resolve a mismatched
      document and assert the run **fails**. A check that cannot fail is decoration. → `TestReconciliation_GoesRedOnMismatch`.
- [x] 9.6 Test — a bound candidate under verification resolves the embedded document and consults no
      override; an unverified resolution is marked and is refused at the highest automation level.
      → `TestPinned_IgnoresOverridesEntirely` + `TestRefuseUnverified_MarkedAndRefusableByLevel`.

## 10. Frontend — Bound-mode surfaces (10b)

- [x] 10.1 Show apply mode per node, and render the **effective resolved values** for bound nodes rather
      than the indirection. → `BoundNodePanel` (model/params/prompt/literal/env shown); **browser-verified**.
- [x] 10.2 Render **unverified** distinguishably from verified — "someone selected this" and "this was
      proven better" must never look the same. → `VerifiedBadge` ("Verified — proven better" tone ok vs "Unverified — selected, not proven" tone warn); guard test.
- [x] 10.3 Render the **degraded** resolver state, naming which source failed and stating that the last
      known-good configuration is in force. → `DegradedBanner`; browser-verified naming "remote" + "last known-good in force".
- [x] 10.4 Update the per-node runtime-changeable statement (§4.10) for bound nodes. → `RuntimeChangeableForBound` (model/prompt/literal/env = data; wiring/expr/input = new change).

## 11. Sales Operations — Claim discipline (10b)

- [x] 11.1 Write the capability statement with its **boundary**: model and prompt version are data;
      wiring, skills, context policy and call-site-expression bindings require a code change. A
      customer planning around general runtime reconfiguration discovers the truth during delivery.
      → [docs/sales/P10-prompt-model-studio-claims.md](../../../docs/sales/P10-prompt-model-studio-claims.md) §11.1.
- [x] 11.2 🚫 The demo script must not present a studio comparison as a result. The honest pitch is
      stronger: *try it in seconds, then prove it with a multi-seed evaluation and ship it as a verified
      pull request.* → §11.2 (try it → prove it → ship it).
- [x] 11.3 Do not promise 10b's runtime layer before it is delivered; until then every configuration
      change is still a reviewed diff. → §11.3.

## 12. Documentation

- [~] 12.1 Fold the four P10 capability specs into `openspec/specs/` when the change deploys, and apply
      the `verification` MODIFIED delta to the folded P5.5 spec. → **deploy-time action** (openspec archive step); `openspec/specs/` does not exist pre-deploy, so folding now would misrepresent the change as deployed. The four capability specs + the `verification` MODIFIED delta are authored and ready in `specs/` under this change.
- [x] 12.2 Record the §1.1 binding-document ADR and the §1.3 lineage decision. → [ADR-009](../../../docs/adr/ADR-009-binding-document-format.md) + [decisions.md](decisions.md) D-1.3.
- [x] 12.3 Update `docs/decisions/capability-boundary-p0-p2.md` — its "template variables bind to your
      call site's own expressions" statement is narrowed by explicit bindings and should say so. → done (Prompt override row narrowed).
- [x] 12.4 Update the P2 PRD and the `p2-config-runtime` change **by reference** — P10 extends the
      registries and the codemod; their merged specs are not edited. → P2 PRD "Extended by P10" note added.
