# Tasks — P15: Workflow / Node-Wiring Optimization

Three waves. **Wave 15a** = the node-wiring operators — implement `OpMerge`, broaden reorder toward free
rewiring, confirm prune — every candidate derived, gated, and hashed. **Wave 15b** = wiring-safety as a
first-class requirement set — reject-at-compile, adapter reconciliation in the diff, admissibility,
deterministic identity, and the interim refusal for un-materializable wiring. **Wave 15c** = the
call-site rewriter's first slice — a transposition of two adjacent, independent sibling statements,
materialized as a *permutation of the file's lines*; everything else keeps 15b's refusal.

The first round shipped **docs** (the PRD + this OpenSpec change); this round shipped the **code** they
specified, and every task now carries a `→ file (Test: Name)` pointer to the implementation and the test
that proves it. 🔴 marks a security/must-fail gate; 🚫 marks a banned action; → marks the evidence
pointer.

⚠️ **What is delivered and what is not.** The wiring axis is now produced (merge / free reorder /
parallelize / prune), gated (`GateReorder`, one gate, adapter-augmenting), hashed (`Order`/`Edges` were
already identity-bearing), scored by the existing harness, and surfaced only after verification. Source
materialization of a rearrangement is still **ABSENT**: a wiring-differing spec is **refused at
transform** naming the axis (§4), which changes the P5 editor's commit outcome for a genuine reorder
from `committed` (with a diff that rewired nothing) to `rejected_transform`. That is the point of the
round — a refusal is honest; a no-op would have let a wiring `config_hash` be scored against unchanged
source.

**Standing constraints.** The wiring axis lives entirely in `VariantSpec.Order`/`Edges`/`InsertedAdapter`
— **no** new `Dimension`, registry `Kind`, `NodeOverride` field, or DB table. `Order`/`Edges` are
**identity-bearing**, so a wiring change is a new `config_hash` with **no eval-side change** (the harness
consumes `config_hash` + `Trace`). Every candidate is **derived** with `ParentVariantID`; the parent is
never mutated. Every candidate is validated by the **one** gate (`GateReorder`) before any transform.
Wiring is **not** yet materialized as source — an un-materializable wiring spec is **refused at transform**,
never silently dropped or no-op'd. A candidate is **surfaced only after P5.5 verification** on held-out data.

---

## 1. System Designer — Fix the one-way-door contracts before any operator ships (15a)

- [x] 1.1 Record the **`OpMerge` semantics** — adjacent-pair only, survivor subsumes, absorbed node
      dropped from `Order`, edges mechanically rewired through the survivor — as a one-way door, because
      a stored proposal row will name `OpMerge` and every future reader depends on its meaning.
      → [`decisions.md`](decisions.md) D-1; PRD §8.3 D1.
- [x] 1.2 Record the **adapter-insertion posture** — explicit `InsertedAdapter` node, its `io_contract`
      carried, materialized as generated source in the same diff, never a runtime shim — as a one-way
      door. → [`decisions.md`](decisions.md) D-2; PRD §8.3 D4.
- [x] 1.3 State the **EXISTS / PARTIAL / ABSENT** ledger so the honest boundary is on the record (ordering
      EXISTS; free rewiring PARTIAL; `OpMerge` RESERVED; source materialization ABSENT). → PRD §8.2.
- [x] 1.4 🚫 Do **not** add a `Dimension` const, registry `Kind`, `NodeOverride` field, or DB table — the
      axis is `Order`/`Edges`, already hashed. → guard: `TestNoNewDimensionForWiring` (asserts the
      `Dimension` enum still has exactly the content values, [`spec.go:42-57`](../../../internal/variantspec/spec.go),
      plus the registry `Kind` set, `NodeOverride`'s fields, and every migration's `CREATE TABLE`)
      → [`p15_wiring_test.go`](../../../internal/variantspec/p15_wiring_test.go).

## 2. Backend — The merge operator (15a)

- [x] 2.1 Specify `mergeOp`: `Kind()=OpMerge`, `HandlesSignal()=SignalRedundantNode`, `Propose` derives a
      candidate that drops the absorbed node from `Order` and rewires its edges through the survivor.
      → `internal/proposal/catalog.go` `mergeOp` (Test: `TestMergeProducesFusedSpec`).
- [x] 2.2 Implement `mergeOp.Propose` on the `Reorder`/derive helpers so the candidate carries
      `ParentVariantID` and leaves the parent spec byte-identical. → [`catalog.go` `mergeNodes`/`parentVariantID`](../../../internal/proposal/catalog.go)
      (Test: `TestMergeDerivesWithLineageParentUnchanged`). Lineage is real rather than inherited: the
      baseline's own `config_hash` arrives as the optional in-memory `OperatorInput.BaseVariantID`
      (never hashed), and reorder/prune now record it too.
- [x] 2.3 Register `mergeOp{}` in `DefaultCatalog()` — one row in the dispatch table, never a switch edit.
      → [`catalog.go:17-44`](../../../internal/proposal/catalog.go) (Test: `TestDefaultCatalogIncludesMerge`).
- [x] 2.4 Confirm the `OpMerge` gain prior is live now that the operator exists (it already sits in
      `gain.go`). → [`gain.go:20,46`](../../../internal/proposal/gain.go) (Test: `TestMergeHasPrior` —
      prior, verify-order hint, and a non-zero `ExpectedGain` on a real emitted candidate).
- [x] 2.5 A merge candidate's `config_hash` differs from its parent (Order/Edges are identity-bearing);
      a merge that resolved to the same configuration hashes identically. → (Test: `TestMergeChangesConfigHash`,
      plus `TestMergeIsAdjacentPairOnly` for D-1's adjacency door)
      → [`p15_wiring_test.go`](../../../internal/proposal/p15_wiring_test.go).

## 3. Backend — Free edge rewiring (15a)

- [x] 3.1 Specify **free reorder**: reorder data-independent nodes and mark parallelizable ones, beyond
      the single lost-in-middle swap [`catalog.go:193-198`](../../../internal/proposal/catalog.go).
      → `internal/proposal/catalog.go` `reorderOp` (Test: `TestFreeReorderIndependentNodes`).
- [x] 3.2 Implement bounded independent-node reordering; every candidate routes through `GateReorder`.
      → [`catalog.go` `reorderOp.Propose`/`independentAdjacentPairs`](../../../internal/proposal/catalog.go)
      (swap + parallelize, `freeReorderBudget` with the truncation stated in the rationale — no silent cap)
      and [`gate.go` `TypedContractGate.Admit`](../../../internal/proposal/gate.go), which now delegates to
      the ONE gate and returns the ADAPTER-AUGMENTED spec instead of discarding the verdict's adapters
      (Test: `TestReorderCandidatesAreGated`, `TestFreeReorderIndependentNodes`, `TestGatedAdaptedCandidateCarriesAdapter`).
- [x] 3.3 Confirm **prune** rewires neighbours and drops the dead node (already implemented — assert the
      shape holds under P15). → [`catalog.go` `pruneNode`](../../../internal/proposal/catalog.go)
      (Test: `TestPruneRewiresNeighbours`, plus `TestPruneAndMergeDifferOnFanIn` — on a straight chain the
      two operators denote the SAME graph and tie; the fan-in is where they genuinely differ).
- [x] 3.4 Every wiring candidate is deterministic — same base + signal → identical candidate spec and
      `config_hash`. → (Test: `TestWiringProposalsAreDeterministic`).

## 4. Backend — The interim refusal for un-materializable wiring (15a)

- [x] 4.1 Specify the **interim refusal**: a resolved spec whose `Order`/`Edges` differ from the discovered
      wiring is refused at transform with an `unsafeRewrite`-class error naming the wiring axis — the
      analogue of `refuseSkills`/`refuseContext`. → PRD §6 FR8; spec `node-wiring`.
- [x] 4.2 🔴 Implement the refusal in the transform engine; a wiring-differing spec returns the typed
      refusal, **never** a silent no-op that would let a wiring `config_hash` be scored against unchanged
      source. → [`rewrite.go` `refuseWiring`/`checkWiring`](../../../internal/transform/rewrite.go)
      (analogue of `refuseSkills`/`refuseContext`), gated at the head of both
      [`Generate`](../../../internal/transform/engine.go) and
      [`GenerateTransform`](../../../internal/transform/generatetransform.go). The evidence it compares
      against is the new, unhashed [`Resolved.DiscoveredWiring`](../../../internal/variantspec/resolve.go)
      (`WiringOf(ir)`), populated by `Resolve` and by the P5 commit handler
      (Test: `TestWiringRefusedNotNoop`, `TestWiringUnchangedIsNotRefused`,
      `TestAdapterInsertionIsNotAWiringRefusal` — an adapter hop is collapsed before comparing, because
      an adapter IS materialized in the same diff).
- [x] 4.3 The refusal is **observable** in the transform result and names the axis, and **no diff** is
      emitted for the refused spec. → (Test: `TestWiringRefusalIsObservableNoDiff`, plus the user-visible
      face at the API: `TestP5Commit_ReorderRefusedAtTransform` — a coherent reorder now commits as
      `rejected_transform` naming the wiring axis, where before P15 it returned `committed` with a diff
      that never rewired anything).

### ⚠️ Correction, earned by CI (2026-07-28)

The first version of §4's gate compared the spec's `Order` against the IR's **node-emission order** —
the order Discovery happened to walk files in — and treated any difference as a rearrangement. That
refused **twelve pre-existing e2e specs** that override only a model or a prompt, because their author
listed the nodes in the workflow's logical sequence rather than in emission order. The Python e2e was
the same mistake on edges: the spec declares the data edges the coherence gate needs, and the IR had
recorded none.

The rule now matches what the source actually states:

| Case | Outcome |
|---|---|
| the spec's **node set** differs (merge / prune) | **REFUSED** — the dropped call is still in the tree |
| a **source-stated pair** is inverted (two calls the source runs as consecutive sibling statements) | **MATERIALIZED** if it is one adjacent transposition, else **REFUSED** |
| an ordering between calls the source does **not** order, or a declared edge | **nothing to do** — a declaration with no source counterpart, refusing it would break every spec ever authored while preventing no false measurement |

`sourceOrderedPairs` reuses the SAME admissibility the materializer requires, so "which pairs does the
source order" and "which pairs can be exchanged" can never drift apart. The honest boundary is recorded
in PRD §8.2 as a **NOT MODELLED** row rather than left to be discovered.

🔴 The local suite was green when this shipped because the failing tests are behind the `pgproof` build
tag and were never compiled by `go test ./...`. `make pgproof` (Docker) is the command that would have
caught it, and it is what verified the fix.

## 5. Backend + System Designer — Wiring-safety: the coherence gate as a requirement (15b)

- [x] 5.1 Specify that a candidate ordering is validated by `GateReorder` → `ValidateOrdering` **before**
      any codemod is generated. → spec `wiring-safety`; [`rearrange.go:52`](../../../internal/variantspec/rearrange.go).
- [x] 5.2 🔴 Assert **reject-at-compile**: an ordering that consumes a field before it is produced yields
      **no runnable spec** (`GateReorder` returns `(nil, verdict)`) and **no diff, codemod, or PR**. The
      gate must **go red**. → extends `TestGateReorder_RejectedYieldsNoRunnableSpec`
      ([`rearrange_test.go:69`](../../../internal/variantspec/rearrange_test.go)) to merge/prune candidates
      (Test: `TestIncoherentWiringRejectedAtCompile` — prune AND merge, each first shown to be produced
      ungated so the assertion is not vacuous, then refused, recorded, and shown to yield no runnable spec)
      → [`internal/proposal/p15_wiring_test.go`](../../../internal/proposal/p15_wiring_test.go).
- [x] 5.3 Assert an **`adapted`** verdict records the adapter as an explicit `InsertedAdapter` node and
      rewires edges producer→adapter→consumer. → [`rearrange.go:66-89`](../../../internal/variantspec/rearrange.go)
      (Test: `TestAdaptedVerdictRecordsAdapter` — io_contract carried, edges rewired, adapter ordered
      between producer and consumer, parent untouched; cf. `TestGateReorder_AdaptedRecordsAdapter`).
- [x] 5.4 🔴 Assert an adapter is admissible **only if it drops nothing the consumer requires**; a
      non-satisfying adapter is refused and the ordering rejected with it. →
      [`adapter.go:73-82`](../../../internal/typedcontract/adapter.go) (Test: `TestAdapterDropsNothingRequired`,
      with `TestSatisfyingAdapterIsAdmitted` as the positive control and `TestAdapterCatalogOrderIsFixed`
      for the fixed match order) → [`internal/typedcontract/p15_wiring_test.go`](../../../internal/typedcontract/p15_wiring_test.go).
- [x] 5.5 Assert an inserted adapter appears as **generated source in the same reviewable diff** — no
      coercion exists outside the diff. → (Test: `TestAdapterIsInReviewableDiff` — the file, the `--- /dev/null`
      hunk, the declared contract inside the source, the `Touched` attribution, and a byte-identical
      regeneration) → [`internal/transform/p15_wiring_test.go`](../../../internal/transform/p15_wiring_test.go).
- [x] 5.6 Assert adapter **identity is deterministic** — same reorder → same adapter ids and `config_hash`.
      → [`rearrange.go:91-93`](../../../internal/variantspec/rearrange.go) (Test: `TestAdapterIdentityDeterministic`).

## 6. AI Engineer — Verification-gated surfacing (15a→15b)

- [x] 6.1 Specify that a produced wiring candidate is **surfaced as a recommended change only after P5.5
      verification** shows it better or cheaper on held-out data; a produced candidate is exploratory
      until then. → PRD §6 FR7; spec `node-wiring`.
- [x] 6.2 A wiring-changed `config_hash` is scored by the **existing** harness — no metric added, no
      Dimension-label branch. → 🚫 no new metric in `internal/evalharness`
      (Test: `TestWiringScoredByExistingHarness` — the standard family is still exactly six, carries no
      wiring-shaped name, and a merge candidate validates + hashes through the ordinary path resolving
      no new ref).
- [x] 6.3 A merge that reads redundant but scores worse on held-out data is **not** surfaced as a
      recommendation. → (Test: `TestUnverifiedMergeNotSurfaced` — cheaper AND faster AND worse is
      withheld with a reason; `TestVerifiedMergeIsSurfaced` is the positive control;
      `TestUnrunWiringCandidateNotSurfaced` covers produced-but-never-verified)
      → [`internal/verification/p15_wiring_test.go`](../../../internal/verification/p15_wiring_test.go).

## 7. QA — Acceptance gate

- [x] 7.1 Merge-shape suite: absorbed node dropped from `Order`, edges rewired through survivor, parent
      unchanged, `config_hash` differs. → (Test: `TestMergeProducesFusedSpec`, `TestMergeChangesConfigHash`).
- [x] 7.2 🔴 Safety suite: incoherent ordering → no runnable spec, no diff; the gate **goes red**. →
      (Test: `TestIncoherentWiringRejectedAtCompile` — prune AND merge, each first shown to be produced
      ungated so the assertion is not vacuous, then refused, recorded, and shown to yield no runnable spec)
      → [`internal/proposal/p15_wiring_test.go`](../../../internal/proposal/p15_wiring_test.go).
- [x] 7.3 Adapter suite: `adapted` records the adapter in the spec **and** the diff; a non-satisfying
      adapter is refused. → (Test: `TestAdaptedVerdictRecordsAdapter`, `TestAdapterDropsNothingRequired`,
      `TestAdapterIsInReviewableDiff`).
- [x] 7.4 Determinism suite: same base + signal → identical candidate spec, adapter ids, and `config_hash`.
      → (Test: `TestWiringProposalsAreDeterministic`, `TestAdapterIdentityDeterministic`).
- [x] 7.5 🔴 Interim-refusal suite: a wiring-differing resolved spec is refused at transform naming the
      axis, never a silent no-op; no diff emitted. → (Test: `TestWiringRefusedNotNoop`,
      `TestWiringRefusalIsObservableNoDiff`).
- [x] 7.6 Eval-agnostic suite: a wiring-changed `config_hash` scores through the existing harness with no
      P15 eval change. → (Test: `TestWiringScoredByExistingHarness`).

## 8. Documentation

- [x] 8.1 Author the P15 PRD (14 sections). → [`../../../docs/prd/P15-workflow-wiring-optimization.md`](../../../docs/prd/P15-workflow-wiring-optimization.md).
- [x] 8.2 Author this OpenSpec change: `proposal.md`, `design.md`, `tasks.md`, `decisions.md`, and the two
      capability spec deltas (`node-wiring`, `wiring-safety`).
- [x] 8.3 On delivery, fold the two P15 capability specs into `openspec/specs/`. →
      [`openspec/specs/node-wiring/spec.md`](../../specs/node-wiring/spec.md),
      [`openspec/specs/wiring-safety/spec.md`](../../specs/wiring-safety/spec.md) — each carrying an
      implementation-evidence table (requirement → code → test) and, for `node-wiring`, the ⚠️ honest
      boundary that source materialization is still ABSENT and every wiring-differing spec is refused.

## 9. Frontend — the declined-change surface (added during delivery)

Not in the original task list, and added for a reason §4 created: the wiring refusal is now the NORMAL
outcome of the axis for the whole interim period, and the console rendered it as an anonymous
"submission failed" — a message that invites a retry that will never be accepted and hides the sentence
that says what to do instead. A refusal the user cannot tell apart from a breakage is a refusal that
teaches them the tool is broken.

- [x] 9.1 Give a dimension-named `400` its own outcome on the submit path, separate from a transport or
      server failure. → [`configurator.tsx`](../../../web/console/src/app/app/configure/configurator.tsx)
      (Test: `P15-1` in [`tests/inventory.test.mjs`](../../../web/console/tests/inventory.test.mjs)).
- [x] 9.2 Render it as a **declined-change card** naming the axis and the node, stating that nothing was
      persisted and that re-submitting will be declined again, with the platform's own sentence kept
      **verbatim** behind a disclosure. → [`axisRefusal.tsx`](../../../web/console/src/components/axisRefusal.tsx)
      (Test: `P15-2`, `P15-3`, `P15-4`).
- [x] 9.3 An axis the console cannot annotate still renders — the platform's sentence carries it — rather
      than being swallowed. → `AXIS_NOTE` lookup returns undefined and the note is omitted (Test: `P15-4`).
- [x] 9.4 Browser-verified against a rendered page, not only asserted: `/preview/p15` renders the SAME
      component the submit path renders, one linkable fixture per shape (reorder / merge / rewired edge /
      un-annotated axis). → [`preview/p15/page.tsx`](../../../web/console/src/app/preview/p15/page.tsx).
      Checked in Chrome at 1280×720 dark and 375×812 light: no console error, no horizontal overflow,
      the verbatim disclosure opens, and the un-annotated axis correctly shows no console note.
      `npm run build` + `npm test` green (249/249).

## 10. Delivery run — the axis against a real repository

- [x] 10.1 Run the shipped code paths against **github.com/nousresearch/hermes-agent** (the same target as
      P5/P13/P14). → [`cmd/p15hermes`](../../../cmd/p15hermes/main.go); `go run ./cmd/p15hermes -repo /tmp/hermes-agent`.

Re-run on a **fresh `git clone --depth 1`** of the upstream repository at `fa7b0fcf5d6e` (26 discovered
nodes) and on an older checkout at `528e3350374b` (40 nodes). Same behaviour on both; the node count
differs because discovery is a function of `source_revision`, which is the point of the pair
`{config_hash, source_revision}`. The table below is the 40-node run; the 26-node run differs only in the
hashes and the bound (`4 of 25 independent adjacent pairs`).

What the run produced at commit `528e3350374b` (40 discovered nodes, Python, **0 discovered edges**):

| Claim | Result on the real IR |
|---|---|
| No new modeling | `Dimension` enum still `[model prompt skills context tools]` |
| Real identity | baseline `config_hash = 06c6d2171dd0c69c…`, and **every** wiring candidate hashes differently |
| Operators fire | 1 merge, 1 prune, 5 reorders on real node ids; parent recorded on each; baseline still 40 nodes after 6 proposals |
| No silent cap | the rationale states `(bounded: 4 of 39 independent adjacent pairs proposed this pass)` |
| 🔴 Reject-at-compile | a declared data edge with the consumer ordered first → `verdict=rejected`, **no runnable spec** |
| 🔴 Refuse-at-transform | all 6 candidates `REFUSED  dim=wiring`, **no patch**; the unchanged baseline is accepted |
| Determinism | two passes → byte-identical specs and identical config hashes |
| Verification decides | worse-but-cheaper merge → `fail_significance`, not recommended; better-and-cheaper → `pass` |

**The control — what this repository does NOT decline.** A run whose every line says REFUSED is
indistinguishable, to a reader, from a platform that does not work, so `cmd/p15hermes` ends by trying a
**model** override at every discovered call site. At `fa7b0fcf5d6e`: **4 of 26 produced a real diff**,
22 refused — 19 because the call site assembles its arguments at runtime (`**kwargs`, so adding `model=`
could raise `TypeError` at run time while the file still parses), 3 because the site is a Bedrock SDK
call and the override selected OpenAI. The first accepted diff is a one-line value replacement in
`optional-skills/research/darwinian-evolver/scripts/parrot_openrouter.py:47`. So "declined" is a
statement about the **wiring axis and its missing call-site rewriter**, not about the platform: the same
engine, tree and commit lands a content change and refuses a rearrangement.

Two boundaries the run **states rather than works around**:

- the syntactic Python frontend records **no edges**, so every adjacent pair is data-independent and there
  is no control edge to drop — the §5 rejection therefore uses a data edge the SPEC declares between two
  REAL nodes, which is how a re-arrangement authors one;
- **no node declares a field-level `io_contract`** (0 of 40 — the frontend honestly states `{"type":"object"}`),
  so the `adapted` verdict is unreachable here and is reported as not-applicable rather than simulated. It
  is covered by `TestAdaptedVerdictRecordsAdapter` / `TestAdapterIsInReviewableDiff`.

On this repository a merge and a prune of the same node produce the **same** graph and therefore the same
`config_hash` — an edgeless chain makes the two hypotheses indistinguishable in the wiring space, which is
a tie rather than a defect (`TestPruneAndMergeDifferOnFanIn` shows where they diverge).

---

# Wave 15c — the call-site wiring rewriter (reorder-only slice)

15a/15b shipped an axis whose every candidate was declined, and said so honestly. 15c makes ONE shape
applicable. It is deliberately the smallest shape with a checkable invariant: a **transposition of two
adjacent sibling statements**, whose output is the input's lines reordered — same count, same multiset.
🚫 It does **not** materialize a merge, a prune, an edge change, a non-adjacent move, or a language
without a statement materializer; each of those keeps the typed refusal from §4 and says which condition
failed.

## 11. System Designer — the one-way doors before any line moves (15c)

- [x] 11.1 Record **D-3**: the admitted operation is a transposition of two ADJACENT sibling statements
      and nothing else — a stored "wiring materialized" transform must mean one fixed thing forever.
      → [`decisions.md`](decisions.md) D-3; PRD §8.3 D7.
- [x] 11.2 Record **D-4**: the permutation invariant is a NEW edit class, never a loosening of
      `gateMinimal`'s no-line-count-change rule — relaxing the shared rule would remove the check from
      every rewriter, forever, to serve one caller. → [`decisions.md`](decisions.md) D-4; PRD §8.3 D8.
- [x] 11.3 Update the **EXISTS / PARTIAL / ABSENT** ledger: reorder materialization becomes PARTIAL
      (swap-only, Go + Python); merge/prune materialization stays ABSENT; other languages stay ABSENT.
      → PRD §8.2.
- [x] 11.4 Author the capability spec delta `wiring-materialization` (7 requirements, FR15–FR21).
      → [`specs/wiring-materialization/spec.md`](specs/wiring-materialization/spec.md).

## 12. Backend — the swap edit class and its permutation gate (15c)

- [x] 12.1 Add a **block-swap edit kind** carrying two whole-line ranges, applied through the existing
      right-to-left splice with no change to value-rewrite behaviour. → `internal/transform/edit.go`
      (Test: `TestBlockSwapAppliesAsPermutation`).
- [x] 12.2 🔴 Extend the minimality gate for the swap class ONLY: line count unchanged, **line multiset
      unchanged**, and every changed line inside one of the two swapped blocks. The existing
      no-newline / no-multi-line rule stays untouched for value rewrites.
      → [`engine.go`](../../../internal/transform/engine.go) `gateSwapPermutation`/`lineMultisetDiff`,
      dispatched from `gateMinimal` (Test: `TestSwapGateRejectsNonPermutation` — a line edited while being
      moved, a line dropped, a line outside the blocks disturbed — and `TestValueRewriteGateUnchanged`,
      which proves the old rule still fires and that a swap-flagged value rewrite cannot smuggle through)
      → [`wiringswap_test.go`](../../../internal/transform/wiringswap_test.go).
- [x] 12.3 The swap is **self-inverse**: applying the same transposition to its own output returns the
      original bytes. → (Test: `TestSwapIsSelfInverse`).

## 13. Backend — the Go statement materializer (15c)

- [x] 13.1 Resolve each node's **enclosing statement** from `go/ast` and require the two to be
      consecutive siblings in one block, whole-line, same indentation.
      → [`wiringswap_go.go`](../../../internal/transform/wiringswap_go.go) `resolveGoStatement` (innermost
      block, direct-child statement, whole-line span) (Test: `TestGoSwapSiblingStatements`).
- [x] 13.2 🔴 Prove **independence**: collect names bound and names read per statement; refuse when
      either reads what the other binds, naming the shared identifier.
      → `goBindsAndReads` — LHS binds, everything else reads; `_` and selector FIELDS excluded so two
      unrelated statements do not look dependent (Test: `TestGoSwapRefusesDataDependency` — both directions
      plus the same-target case).
- [x] 13.3 🔴 Refuse **control flow** (`return`/`break`/`continue`/`goto`/`defer`/`go`) and any pair with
      a comment or other code between them. → (Test: `TestGoSwapRefusesControlFlowAndComments`).
- [x] 13.4 The emitted Go file is **gofmt-stable** — a swap of two well-formatted statements needs no
      reformatting. → (Test: `TestGoSwapOutputIsGofmtStable`).

## 14. Backend — the Python statement materializer (15c)

- [x] 14.1 Resolve each node's statement as its **whole-line span** and require identical indentation,
      consecutive placement, and a simple (non-compound) statement.
      → [`wiringswap_python.go`](../../../internal/transform/wiringswap_python.go) — indentation is block
      membership, a logical line ends where its brackets balance (Test: `TestPythonSwapSiblingStatements`,
      including a multi-line call that moves as one unit).
- [x] 14.2 🔴 Prove **independence** conservatively from bound/read names; an unparseable or
      unanalysable statement is REFUSED, never assumed independent.
      → `pyBindsAndReads` refuses tuple/starred/chained targets, walrus bindings and backslash
      continuations outright; an attribute write both binds AND reads its base
      (Test: `TestPythonSwapRefusesDataDependency`, `TestPythonSwapRefusesUnanalysable`).
- [x] 14.3 🔴 Refuse control flow (`return`/`raise`/`yield`/`break`/`continue`) and interleaved comments.
      → (Test: `TestPythonSwapRefusesControlFlowAndComments`).

## 15. Backend — materialize-or-refuse routing (15c)

- [x] 15.1 `checkWiring` becomes **materialize-or-refuse**: exactly one adjacent transposition with equal
      edges is attempted; every other difference keeps §4's refusal unchanged.
      → [`rewrite.go` `checkWiring`](../../../internal/transform/rewrite.go) now returns `(*swapPlan, error)`
      and [`wiringswap.go` `planWiringSwap`](../../../internal/transform/wiringswap.go) admits ONE shape —
      a three-cycle is two moves wearing one name and is refused (Test: `TestOnlyAdjacentTranspositionAttempted`).
- [x] 15.2 Both entrypoints route through it — `Generate` (P2 submit) and `GenerateTransform` (P5 commit)
      — so a materialized reorder is available wherever a transform is generated.
      → `planSwapEdits` in [`wiringswap.go`](../../../internal/transform/wiringswap.go), emitted from
      `Generate` so there is ONE emission path; a file carrying both a value rewrite and a swap is refused,
      because each edit would otherwise be checked by the other's gate
      (Test: `TestMaterializedReorderOnBothEntrypoints` — same diff hash from both entrypoints).
- [x] 15.3 🔴 A language with no materializer refuses **by name**, never falls back to a textual move.
      → (Test: `TestUnsupportedLanguageRefusesByName`).
- [x] 15.4 Determinism: the same {spec, revision, tree} yields a byte-identical diff.
      → (Test: `TestMaterializedSwapIsDeterministic`).

## 16. QA — acceptance gate for the rewriter (15c)

- [x] 16.1 🔴 Must-fail suite: dependent statements, control flow, interleaved comment, different files,
      different nesting, two swaps at once — each refused, each naming its condition, none producing a diff.
      → (Test: `TestSwapRefusalMatrix` — 8 rows, each asserting the RIGHT refusal, the wiring axis on the
      error, and that no edits were produced; the declines ARE the specification of this rewriter).
- [x] 16.2 🔴 The permutation invariant holds on every produced swap, and a hand-broken swap is rejected.
      → (Test: `TestSwapGateRejectsNonPermutation`).
- [x] 16.3 The swapped file still **parses**, and the patch carries the swap in `Touched` so the build
      gate can attribute a failure. → (Test: `TestSwappedFileParsesAndIsAttributed`).
- [x] 16.4 Merge and prune are still refused after 15c — the slice widened by exactly one shape.
      → (Test: `TestMergeAndPruneStillRefused`).

## 17. Frontend — the console stops saying "always declined" (15c)

- [x] 17.1 Update `/app/wiring`: wiring is no longer wholly declined — add the **accepted** state showing
      a real swap diff beside the declined ones, and correct the axis copy.
      → [`wiring/page.tsx`](../../../web/console/src/app/app/wiring/page.tsx) — a new **An applied
      reorder** tab, placed FIRST among the outcomes (a reader who meets four refusals in a row learns the
      wrong thing), and the axis banner rewritten from "declined at the transform today" to
      applied-vs-declined-by-name (Test: `P15-9`).
- [x] 17.2 The accepted state shows the DIFF a reviewer reads, not a description of one.
      → `AxisApplied` in [`axisRefusal.tsx`](../../../web/console/src/components/axisRefusal.tsx) renders the
      engine's REAL emitted diff through the existing `<Diff>` component and states the permutation
      invariant on the card (Test: `P15-10`, `P15-11`). `Banner`'s icon is now tone-aware — a warning
      triangle over "this change was applied" is a small lie told in an eyeblink.
- [x] 17.3 Browser-verified in Chrome at desktop and mobile, both themes, no console error: six tabs, the
      applied diff rendered with its −2/+2 counts, the diff scrolling inside its own box at 375px and the
      page body never scrolling horizontally. `npm run build` + `npm test` green (256/256).

## 18. Delivery — run the rewriter against the real repository (15c)

- [x] 18.1 Extend `cmd/p15hermes` to survey every adjacent pair for swappability and report the outcome
      per pair, then run it against a fresh `nousresearch/hermes-agent` clone.
      → [`cmd/p15hermes`](../../../cmd/p15hermes/main.go) `swapSurvey`.

At `fa7b0fcf5d6e` (26 nodes, fresh `git clone --depth 1`): **25 adjacent pairs surveyed, 0 materialized,
25 refused** — and every refusal names its condition:

| Condition | Pairs |
|---|---|
| the two calls are in **different files** | 21 |
| one of them is a **`return` statement**, whose position is part of its meaning | 3 |
| the two statements sit at **different nesting** (8 vs 12 columns) | 1 |

That is a fact about hermes-agent's code — its LLM calls live in ten files and are mostly `return`s
inside branches — not a gap in the rewriter: the same engine materializes the swap on a tree whose calls
ARE siblings (`TestMaterializedReorderOnBothEntrypoints`, and the diff the console's wiring surface
renders).

🔴 **The survey found a real defect and it is fixed.** The first run reported four pairs as *"does not
close its brackets before the end of the file"*, which was false: a docstring containing an unmatched
`(` — ordinary prose — shifted the Python line scanner's bracket count for the rest of the file. The
failure was **fail-closed** (those pairs were refused, never mis-swapped), which is the design working;
but a refusal with a wrong reason sends a user hunting a defect in their own code. `pyScan` now carries
string state across lines, and `TestPythonScannerSurvivesDocstrings` keeps it.

