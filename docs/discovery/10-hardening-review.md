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
  loads credentials.

### The deployment half (added; previously deferred to "DevOps §8" and never delivered)

The three tests above prove the *code* half — discovery does not *ask* for write access, sockets, or
credentials. They do not prove the *runtime* denies them, and **the gap is not theoretical**:
`discover` parses untrusted customer source with **tree-sitter's C runtime via cgo**
(`CGO_ENABLED=0` does not link — the Python/Rust/Java/TS/JS frontends all bind it). A memory-safety
bug in that C parser, reached by a hostile input file, is arbitrary code execution that the Go import
guard cannot see: the guard constrains what *our* code asks for, not what someone else's bug does.
Additionally, `noexec_test.go` scans only `internal/discovery/*.go`, so it says nothing about the
`cmd/discover` main package or any dependency.

| Claim | Runtime enforcement | Proof |
|---|---|---|
| Read-only repo mount | `/repo` mounted `:ro` (kernel-enforced), `read_only: true` rootfs | write + delete probes inside the shipped spec must fail; host tree unchanged |
| No network egress | `network_mode: none` — no non-loopback interface exists | interface count == 0; outbound TCP cannot be established |
| No ambient provider creds | `environment: {}`, no `env_file:` — compose forwards nothing | poison `OPENAI_API_KEY`/`ANTHROPIC_API_KEY`/`AWS_*` exported in the invoking shell must be absent inside the worker |

- **Spec:** [`deploy/docker-compose.discovery.yml`](../../deploy/docker-compose.discovery.yml) (+ [`deploy/Dockerfile.discover`](../../deploy/Dockerfile.discover)) — also `cap_drop: [ALL]`, `no-new-privileges`, non-root uid 65532.
- **Proof:** `make discovery-sandbox-proof` asserts each claim **twice** — statically (the field is
  present in the resolved spec) and dynamically (the thing the field forbids actually fails). Static
  alone would pass on a field Docker ignores; dynamic alone would pass if a probe measured nothing.
  The probes run *through the shipped compose file*, so a proof that hand-rolled its own `docker run`
  — which would only prove Docker works — is structurally impossible here.
- **Red-check:** `make discovery-sandbox-proof-redcheck` weakens the spec one guarantee at a time
  (drop `network_mode: none`; flip `/repo` to `:rw`; forward `OPENAI_API_KEY`) and requires the proof
  to go red **and name the specific claim**. A fence that cannot go red is decoration.

**Honest limits — what this does *not* guarantee.** It binds the container, not the binary. Running
`bin/discover` directly on a host (which is what `make discovery-ci` does) gets **none** of these
protections; the compose spec *encourages* the hardened path, and only CI's use of it is *enforced*.
`network_mode: none` cuts egress, not a local kernel exploit; `cap_drop: ALL` is not a sandbox against
a container escape. This bounds blast radius — it is not a claim to contain hostile code, which PRD §3
defers to P3 (the same disclaimer `internal/executor`'s package doc makes about its own sandbox).

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
- Least-privilege at the *deployment* layer is **no longer deferred** — see §7.2 "The deployment
  half". Its limits are stated there: it binds the containerised worker, not a direct `bin/discover`
  invocation on a host.
- There is no least-privilege posture for `agentd`, and there cannot be one: `internal/api` links
  `internal/executor` → `internal/providergateway`, which reads `OPENAI_API_KEY` / `ANTHROPIC_API_KEY`
  from the environment, and it must reach providers over the network. **`agentd` is not the
  least-privilege worker** — `cmd/discover` is the only entrypoint where all three NFR7 claims can be
  simultaneously true (its sole internal dependency is `internal/discovery`).
