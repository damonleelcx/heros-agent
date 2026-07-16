# P1 Discovery — Hardening & adversarial self-review (§7)

> **Task:** P1 `tasks.md` §7 (Backend lead, DevOps support). **Method:** the QA discipline —
> *a fence that cannot go red is decoration*; hunt the failure shape, then prove the guard fires.
> Cross-refs: [invariants I1–I8](03-discovery-invariants.md), [failure table](08-failure-behavior.md).

## §7.1 No-execution assertion (NFR1 / I1)

**Structural guard** — [`noexec_test.go`](../../internal/discovery/noexec_test.go) `TestNoExecutionImports`
parses the package's own non-test source and fails if it imports `os/exec`, `plugin`,
`golang.org/x/tools/go/packages` (shells out to `go list`), or `go/build`. This is why detection is
built on bare `go/parser` rather than `go/packages`.

**Behavioral guard** — `TestNoExecutionInitNeverFires` runs discovery over a repo whose `main.go` has
`func init() { os.WriteFile(sentinel, …) }` and asserts the **sentinel is never created**. Because
discovery only parses text (never compiles or runs the target), the `init()` cannot fire. Verified
red-able: an implementation that shelled out to run the repo would create the sentinel and fail.

## §7.2 Least-privilege worker (NFR7 / I8)

- **No network egress (structural):** the import guard (§7.1) also forbids `net` and `net/http` in the
  analysis path — the code *cannot* reach the network, independent of deployment sandboxing.
- **Read-only (behavioral):** `TestReadOnlyNoRepoMutation` digests the repo tree before and after a
  run and asserts byte-identical — discovery creates/modifies/deletes nothing in the target.
- **No ambient creds:** discovery takes no provider keys and reads none; there is no code path that
  loads credentials. (The deployment-level read-only mount / no-egress / no-creds posture is the
  DevOps §8 concern; these tests prove the code half.)

## §7.3 Robustness on hostile input (NFR5 / I7)

Every hostile input degrades to a per-file diagnostic; the run completes and siblings are still found.

| Attack | Guard | Test |
|---|---|---|
| Deeply-nested expression (120k parens) | `go/parser`'s own nesting limit → `PARSE_ERROR`; the AST walk is additionally depth-bounded (`defaultMaxWalkDepth`, `EXPR_DEPTH_EXCEEDED`) | `TestDeepNestingNoCrash` — no overflow; good sibling still discovered; hostile file flagged |
| Huge string literal (2 MB) | bounded single-pass render/resolve | `TestHugeLiteralBounded` — completes, one node |
| Symlink cycle | `filepath.WalkDir` never follows symlinks; symlinked entries are skip-and-reported | `TestSymlinkCycleSkipped` — terminates, real node found |

## §7.4 Adversarial self-review — the four classic failure shapes

The review hunted exactly the four failures the PRD names. Verdicts:

| Failure hunted | Finding | Guard / evidence |
|---|---|---|
| **Silently-dropped node** | No silent path found. Every skip (parse error, symlink, stale declaration, dedup) writes a diagnostic; `Run` **structurally asserts `report.nodes_emitted == len(ir.nodes)`** and errors otherwise. | malformed fixture; dedup fixture; the equality check in `discover.go` |
| **Variable node given a fixed count** | Impossible by construction — there is **no count field** anywhere in the IR or the model. A loop node is one node with `variable_at_runtime=true`. | loop fixture asserts it; schema has no count field |
| **Non-deterministic IDs** | IDs are `sha256` over a stable tuple; occurrence index is assigned in source-order AST traversal; all output is sorted before emit. | golden-IR byte-diff (`TestGoldenIR`), `TestDeterministicEmission`, `TestSelectorIgnoresReceiverName` |
| **Unhandled parse/processing panic** | **Gap found and fixed.** Per-package processing and each `FrameworkReader` call had no `recover`; a reader panic on drifted input would crash the whole run — contradicting I7 / doc 08 F10. | **Fix:** `recover` guards added in `discover.go` (`safeFramework` + the per-package `defer`), converting a panic into a `FRAMEWORK_READER_ERROR` / `PACKAGE_PANIC` diagnostic. `TestFrameworkReaderPanicRecovered` proves a panicking reader is recovered, the run completes, and non-framework nodes are still emitted. |

**Outcome:** one real defect (missing panic isolation) found and fixed with a proving test; the other
three failure shapes were already structurally prevented and now carry explicit red-able fences.

## Residual limits (honest scope, not defects)

- Method-call detection is import-presence + selector heuristic (no `go/types`) — recorded as
  `BasisSelectorImport` in the report, not hidden.
- Least-privilege at the *deployment* layer (read-only mount, network policy, secret-free env) is the
  DevOps §8 job; §7 proves only that the code cannot violate it.
