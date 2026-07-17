# M2 Exit Review — P2 Configuration Layer + Runtime

| Field | Value |
|---|---|
| Phase / Milestone | P2 / M2 |
| Tasks | 8.4 (adversarial self-review), 8.5 (M2 exit checklist) |
| Cross-refs | [`docs/prd/P2-config-runtime.md`](../prd/P2-config-runtime.md) §13; [`ADR-001`](../adr/ADR-001-source-transformation-apply-model.md); [`openspec/changes/p2-config-runtime/tasks.md`](../../openspec/changes/p2-config-runtime/tasks.md) |
| Verdict | **Not green.** 12 of 14 exit criteria met; 2 are partial, each for a design reason recorded below. (2b closed 2026-07-17 — see Deviation A.) |

> Every ✅ below names the test that proves it. A criterion with no test behind it is marked ⚠️ or ❌,
> not ✅. The point of an exit checklist is to be failable — one that is green by assertion is a
> ceremony, and a phase that ships on a ceremony is a phase whose next phase discovers the truth.

---

## 1. Adversarial self-review (task 8.4)

The eleven named hazards. Each has **two independent tests** — one at the unit level, one live —
because the P2 bugs that actually happened all lived in the seam between two correct halves.

| # | Hazard | Proved by | |
|---|---|---|---|
| 1 | Unresolved ref triggers a partial run | `TestE2E_DanglingRefFailsClosedWithNoSideEffects` (asserts **zero** rows in variant_spec/transform/run) · `TestResolve_RejectsDanglingRefNamingNodeAndDimension` | ✅ |
| 2 | Registry version mutated in place | `TestPG_Immutability_RawUpdateOfAPublishedVersionIsRejected` · `TestPG_Immutability_TruncateIsRejected` | ✅ |
| 3 | Non-deterministic diff | `TestGenerate_IsDeterministic` (25 regenerations) · `TestE2E_ModelOverride_MinimalDeterministicDiffOnAnIsolatedWorktree` (cold pool + cache) | ✅ |
| 4 | Transform breaks the build | `TestE2E_BuildBreakingTransformIsRejectedBeforeItRuns` (asserts **no run row exists**) · `TestApply_BuildBreakingTransformIsRejected` | ✅ |
| 5 | Diff with incidental edits | `TestGenerate_EverythingOutsideTheCallSiteIsByteIdentical` · `TestGenerate_MultipleEditsDoNotOverReportChangedLines` | ✅ |
| 6 | User working tree mutated in place | `TestApply_UsersWorkingTreeIsByteForByteUnchanged` (tree hash, not `git status`) · `TestGenerate_WritesNothingToTheTree` | ✅ |
| 7 | Secret in logs / diffs / rows | `TestPG_NoSecretReachesTheStores` (sweeps every text column via `information_schema`) · `TestComplete_SecretsNeverAppearInAnyError` | ✅ |
| 8 | Non-idempotent retry | `TestCallProvider_ForcedRetryProducesExactlyOneCharge` · `TestPG_DoubleWriteOfOneNodeExecutionIsCaught` | ✅ |
| 9 | Seed not propagated | `TestCallProvider_ThreadsTheRunsSeedToTheProvider` · `TestRun_SameSeedPropagatesIdenticallyAcrossRuns` | ✅ |
| 10 | Malformed I/O passed downstream | `TestRun_HaltActuallyStopsTheWorkflowBeforeItContinues` (the workflow would touch a marker file a second later; it never does) · `TestE2E_ContractViolationHaltsTheRunAndIsRecordedAsHalted` | ✅ |
| 11 | Rollback leaves residue | `TestE2E_RollbackIsASingleGitRevertWithNoResidue` · `TestRevert_RestoresSourceRevisionByteForByteWithNoResidue` | ✅ |

### Tests that carry their own control

Four assertions would pass for the wrong reason without a paired negative, so each ships with one:

| Claim | Its control |
|---|---|
| "a retry is one charge" | `TestCallProvider_WithoutTheKeyTheSameRetryWouldBeBilledEveryTime` — proves a missing key really would bill 3× |
| "no secret in the stores" | `TestPG_TheSweepActuallyDetectsAPlantedSecret` — proves the sweep is looking where it claims |
| "eviction is LRU" | `TestPrune_AnEvictedVariantIsSimplyRebuilt` — proves eviction costs a rebuild, not a result |
| "the diff is minimal" | `TestE2E_..._MinimalDeterministic...` asserts against **changed lines only**, so a context line is not mistaken for an edit |

### Bugs this review process actually found

Not hypothetical. Each was green under the tests that existed at the time:

| Bug | Why the existing tests missed it | Found by |
|---|---|---|
| plpgsql evaluated `NEW.body_blob_hash` regardless of the `TG_TABLE_NAME` guard — **every** model/skill/context INSERT failed | no unit test applies real DDL | first live-Postgres run |
| Diff over-reported 6 changed lines when 2 changed | every string assertion still passed; the minimality gate checks *edits*, not rendered hunks | reading the output |
| `int(d.Seconds())` truncated sub-second leases to `0` → every item instantly redeliverable | prod uses 5 min; the expiry test passed **vacuously**, proving "a zero lease expires" | the renew test |
| `CommandContext` killed only the direct child; `sleep 60` held the pipes → a 500 ms timeout took 60 s | nothing else spawned a grandchild | the timeout test |
| `FSBlobStore` never catalogued the `blob` row → **any** node with real I/O failed its FK | every executor test recorded nodes with *empty* blob hashes | driving the UI live |
| `go build ./...` dropped a 2.5 MB binary into the worktree → `git status` dirty, residue after revert | no test ran apply→build→revert in sequence | the e2e rollback test |

The pattern is worth stating plainly: **six for six were invisible to unit tests and surfaced only when
real infrastructure ran the real sequence.** That is the argument for `make pg-proof` and
`internal/e2e` existing at all.

---

## 2. M2 exit checklist (task 8.5, PRD §13)

| # | Criterion | Status |
|---|---|---|
| 1 | Hardcoded graph **transformed, built, and run** end to end → per-node I/O + terminal status | ✅ `TestE2E_M2_HardcodedGraphIsTransformedBuiltAndRunEndToEnd` — runs the *built* copy, 3 nodes, blobs resolve, `succeeded` in Postgres |
| 2 | **Model** override → minimal targeted diff, takes effect | ✅ `TestE2E_ModelOverride_...` (1 line changed) · `TestE2E_OverridingANodeThatNeverSetAModelInsertsTheFieldAndBuilds` |
| 2b | **Prompt** override → minimal targeted diff, takes effect | ✅ `TestE2E_PromptOverride_TakesEffectAsAMinimalDeterministicDiffThatBuildsAndRuns` — 1 line changed, byte-identical on regeneration, builds, runs, `succeeded` in Postgres · `TestE2E_PromptOverride_UnrewritableCallSitesAreRefusedBeforeAnythingIsApplied` (4 boundary cases, each pinning its reason) — **Deviation A closed** |
| 3 | Deterministic transform (byte-identical diff) | ✅ `TestGenerate_IsDeterministic` |
| 4 | Build-breaking transform **rejected** as `build-rejected` | ✅ `TestE2E_BuildBreakingTransformIsRejectedBeforeItRuns` |
| 5 | Behavior-preserving; user tree never mutated | ✅ `TestApply_UsersWorkingTreeIsByteForByteUnchanged` |
| 6 | **Provider swap** (Anthropic ↔ OpenAI) by rewriting only `model_ref` | ⚠️ **Partial** — see Deviation B |
| 7 | Same `config_hash` + `seed` replays identically | ✅ `TestE2E_Reproducibility_SamePairBuildsTheSameTree` · `TestReproducibility_*` |
| 8 | Forced retry does not double-charge / double-write | ✅ `TestCallProvider_ForcedRetryProducesExactlyOneCharge` · `TestPG_DoubleWriteOfOneNodeExecutionIsCaught` |
| 9 | Loader **fails closed** on any unresolved `*_ref` | ✅ `TestE2E_DanglingRefFailsClosedWithNoSideEffects` |
| 10 | Executor **halts** on a typed-contract violation | ✅ `TestE2E_ContractViolationHaltsTheRunAndIsRecordedAsHalted` |
| 11 | Registries versioned + immutable; old spec still resolves | ✅ `TestPG_Immutability_EditPublishesNewVersionAndOldStillResolves` · `TestPG_ExpandContract_OldPinnedVersionResolvesAfterANewOneIsPublished` |
| 12 | Secrets never in specs / diffs / rows / logs / run records | ✅ `TestPG_NoSecretReachesTheStores` + gitleaks (CI) |
| 13 | Every applied change is a reviewable diff; reverts cleanly | ⚠️ **Partial** — diff ✅, revert ✅, **PR ❌** — see Deviation C |
| 14 | Bare UI renders loading / error / empty / build-rejected / success | ✅ driven in a real browser; all five states visually distinct, 0 console errors |

**12 ✅ · 2 ⚠️ · 0 ❌**

---

## 3. The deviations, and why

### A · Prompt override — ✅ CLOSED (2026-07-17)

`TestE2E_PromptOverride_TakesEffectAsAMinimalDeterministicDiffThatBuildsAndRuns` ·
`TestE2E_PromptOverride_UnrewritableCallSitesAreRefusedBeforeAnythingIsApplied`

**This deviation rested on a false premise, and the premise was in the fixture, not the engine.**

The original reasoning was: a real prompt is a `Messages` *construction*, so rewriting it means
synthesizing SDK-shaped code, so refuse. The engine now takes a third option the review did not
consider: **descend into the construction the author already wrote and replace only the string
expression inside it.** The message slice, the role helper and the SDK types survive byte-for-byte.
Nothing is synthesized, so ADR-001's top risk is not incurred — a string expression swapped for a
string expression type-checks exactly where the original did, on any SDK, with zero per-SDK
knowledge. That is strictly better than the "per-SDK message-construction rewriter" this section
proposed as the fix, which would have put SDK codegen in the engine.

**The old test passed for a misleading reason, and that is the finding worth recording.** No node in
the fixture set `Messages` *at all*, so `TestE2E_PromptOverride_RefusedSafelyBeforeAnythingIsApplied`
refused with *"the prompt argument is not present"* — it never reached the boundary its own comment
claimed to prove ("this fixture's prompt is a `Messages` construction"). The comment was false. The
SDK stub compounded it: it typed messages as `Message{Role string; Content string}`, two bare string
literals, which would *also* refuse — for a reason no real Go SDK produces, since real SDKs carry the
role in a **function** (`NewUserMessage`), never a string. **A fixture that cannot express the
success case makes the failure case unfalsifiable.**

Now proved, against a real compiler and real Postgres:

| Claim | Evidence |
|---|---|
| takes effect | the **built worktree on disk** carries `NewTextBlock("You are a support router…\n" + ticket)` |
| minimal + targeted | exactly 1 line changed; other nodes' prompts and this node's model untouched; `Touched` names only `prompt` |
| deterministic | same `{config_hash, source_revision}` → byte-identical diff + `DiffHash` |
| builds | `StatusBuilt` from a real `go build` |
| runs | terminal `succeeded` read back **from Postgres**, attributed to the prompt-overridden `config_hash`, 3 nodes with resolvable I/O blobs |

**The refusal is kept and strengthened, not deleted** — it is a real guarantee, not a consolation
prize. Four boundary cases now each pin their *reason* (slotless template feeding a runtime value ·
multi-turn list · no prompt argument · slot matching no operand), so a refusal firing for an
unrelated reason can no longer pass as the boundary holding. That assertion is the direct fix for how
this deviation went unnoticed.

**Red-checked** (围栏必须能红): three mutations, three precise failures. The one that matters —
emitting a slot as the string literal `"ticket"` instead of the call site's `ticket` expression —
**compiles and runs**, silently dropping the runtime value, and is caught. A test that only asserted
"it refused" or "it compiled" would have shipped it.

### B · Provider swap is a gateway concern, not a call-site rewrite

`TestE2E_ProviderSwapAtTheCallSiteIsRefused` · `TestE2E_IdempotencyProviderSwapAndSeedThroughTheRealGateway`

§13 asks that swapping Anthropic → OpenAI be achieved *"by rewriting only its `model_ref`"*. At the
**call site** that is not true and cannot be made true: rewriting a model string does not turn
`client.Messages.New(...)` into an OpenAI call — the client, the params type, and the response shape
all differ. A codemod that tried would emit a diff that compiles and then talks to the wrong provider.

The gateway half **is** met: the same request against three providers returns one normalized shape
(`TestComplete_ProviderSwapIsTransparent`). So the requirement holds wherever the gateway is in the
call path — and that is the open question below.

### C · No PR is opened

Every applied change is a **reviewable diff**, content-hashed, on its own variant branch, revertible
by a single `git revert`, and rendered for review in the UI. What does not exist is the step that
opens a **pull request** against the user's repository. §13 says "reviewable diff/PR"; we have the
diff and the branch, not the PR.

Nothing merges to a default branch without human approval — trivially, since nothing merges at all.

---

## 4. The open question this review could not settle — **RESOLVED 2026-07-17**

> ✅ **Settled by [ADR-002](../adr/ADR-002-provider-gateway-serves-platform-callers.md): the first
> reading wins.** The gateway is for **our** callers (P4's harness, P5.5, P6). The transformed program
> calls its own SDKs. FR12 (`docs/prd/P2-config-runtime.md`) and the runtime spec
> (`openspec/changes/p2-config-runtime/specs/runtime/spec.md`) are amended to match, and
> `internal/transform/rewrite.go` now cites ADR-002 instead of citing FR12 while doing the opposite of
> it.
>
> **Deviation B is therefore not a gap** — criterion 6 is worded from the pre-ADR-001 design and
> should be reworded in PRD §13 to "within-provider model swap".
>
> The decisive argument was not cost but a **dilemma**: if the codemod routes call sites through our
> gateway, the resulting PR either merges with that dependency (we have rewritten the customer's
> production architecture — not "behavior-preserving except for the intended change") or it is
> stripped before merge (we measured code that is not the code that ships — *precisely* the flaw that
> killed the shim model in ADR-001). There is no third branch, which is why no implementation could
> ever have satisfied both requirements. See ADR-002 for the full 八级法则 arbitration.
>
> **Cross-provider swap at a user call site is now an explicit, named non-capability** rather than an
> unstated one: it requires rewriting the SDK call itself (a per-(source-SDK, target-SDK) rewriter),
> and it is out of P2 scope. Sales-operations must not promise it.

*Original text, retained for the record:*

**FR12 and ADR-001 disagree about where the gateway sits, and it is a design call, not an
implementation one.**

- ADR-001: the transformed program runs and calls provider SDKs **directly** — that is what makes the
  measurement faithful.
- FR12: models **"SHALL be invoked through a unified provider gateway."**

Both cannot hold unless the codemod rewrites call sites to route through the gateway — a far larger
transform than a model-argument swap, and one that re-introduces a runtime dependency in the user's
process, which ADR-001 rejected.

The two readings imply different work:

| Reading | Consequence |
|---|---|
| The gateway is for **our** callers (P4's harness, P5.5) | P2 is complete as built. Deviation B is not a gap; the criterion is just worded from the pre-ADR-001 design. |
| The codemod must **route call sites through the gateway** | A whole dimension of transform work remains, and Deviation B is a real gap. |

`executor.CallProvider` is correct either way and is the single derivation point for the idempotency
key. **Recommend resolving this before P4 depends on it.**

---

## 5. Evidence

```
make ci        PASS      gofmt clean · config_hash golden vectors pass · discovery-ci pass
make pg-proof  PASS      6 packages, live Postgres, isolated schemas per package
golangci-lint  0 issues  with and without the pgproof tag
```

| Suite | Tests |
|---|---|
| P2 unit (8 packages) | 215+, 0 skips |
| P2 live-Postgres (6 packages) | 227+, 0 skips |
| End-to-end (`internal/e2e`) | 12 (+4 subtests), real Postgres + git + `go build` + stubbed providers |
| UI | driven in a real browser; 5 states verified visually |

Neither `make go` nor `make pg-proof` contains a skip in any P2 package. The live proofs **fail**
rather than skip when Postgres is unreachable — a suite that skips its way to green is the failure
mode this whole review exists to avoid.
