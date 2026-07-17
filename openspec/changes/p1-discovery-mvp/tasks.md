# Tasks — P1 Discovery MVP (multi-language, static)

Ordered, independently-verifiable tasks grouped by workstream/role. Backend leads
(explore → design → implement → test → harden → review); AI Eng, DevOps, System Designer support.

> **Rescoped Go-only → multi-language** (PRD §15). §1–§9 below delivered the **language-neutral core +
> the Go frontend** (`internal/discovery`, all green). §10 is the multi-language workstream: extract the
> `LanguageFrontend` interface and add per-language frontends (Python → TS/JS → Java/Kotlin → Rust → …)
> behind it, reusing the core unchanged.

## 1. Understand & Explore (Backend lead, AI Eng support)

- [x] 1.1 Confirm the frozen P0 IR contract: node fields, edge fields, typed I/O contract stubs, and
  the static-node vs. runtime-invocation distinction Discovery must populate.
- [x] 1.2 Survey real Go LLM repos; catalog concrete call shapes (direct SDK calls, options structs,
  interface-typed clients, in-house `Complete()`/`Generate()` wrappers). Record the wrapper patterns
  that defeat signature matching.
- [x] 1.3 Write down the invariants that must never break: no execution of the repo; no fixed count for
  a variable node; stable node IDs; every missing node explainable from the run report.



## 2. Design (Backend lead, System Designer + AI Eng support)

- [x] 2.1 Design the **signature registry** data model (package-qualified entrypoint → argument map)
  and the seed rows: Anthropic Messages, OpenAI Chat Completions, LangChain/LangGraph invoke, Bedrock Converse.
- [x] 2.2 Design the `llm-eval.yaml` schema: declared entrypoints and the mapping from argument
  position/name to IR metadata fields (model, prompt, tools). (Resolves PRD Q1.)
- [x] 2.3 Design the **node-ID scheme**: content-addressed tuple (package path + func + call-site
  identity + content hash) that is stable across benign line shifts yet unique per call site. (PRD Q4.)
- [x] 2.4 Design the `FrameworkReader` interface and the LangGraph/CrewAI declarative-graph mapping;
  define version-drift behavior (degrade-to-flag, not crash). (PRD Q2.)
- [x] 2.5 Design **failure behavior** for each fault: unparseable file, bad `llm-eval.yaml` symbol,
  cyclic imports, unresolved model/prompt arg → decided outcome (skip-and-report / mark-unresolved-and-flag).
- [x] 2.6 Define the **discovery run report** shape (files scanned, detections by source, nodes emitted,
  ambiguity flags, per-file diagnostics).



## 3. Implement — parsing & detection (Backend lead)

- [x] 3.1 Loader: read-only repo walk treating source as untrusted text; stream per package to bound memory.
- [x] 3.2 `go/ast` parser with type/import resolution; bounded recursion on expression walking; **no**
  process spawn, `go run`, `go build -buildmode=plugin`, or plugin load anywhere in the path.
- [x] 3.3 Registry detector: resolve call expressions against the signature registry (FR1).
- [x] 3.4 Declared-entrypoint detector: load `llm-eval.yaml`, resolve declared entrypoints as co-equal
  call sites, map args to metadata (FR2).
- [x] 3.5 Merge/dedup detection sources by stable call-site identity so one call site = one node.



## 4. Implement — extraction & graph (Backend lead, AI Eng support)

- [x] 4.1 Metadata extractor: model arg, messages/prompt construction, tools/skills, upstream data flow;
  intra-procedural resolution where possible, `unresolved` (not omitted) where not (FR3).
- [x] 4.2 Ambiguity flagging: mark unresolved prompt-construction/data-flow call sites as P5
  dynamic-trace candidates with a reason (FR8). (AI Eng owns the fidelity judgment.)
- [x] 4.3 Call-graph builder: nodes = LLM-invoking functions/agent steps; edges = data/control flow (FR4).
- [x] 4.4 Framework readers: read LangGraph/CrewAI declarative DAGs; derive nodes/edges from the
  definition, tag `framework_source` on the subgraph (FR5).
- [x] 4.5 Static-vs-runtime counting: per-static-definition count; flag loop/agent nodes
  `variable_at_runtime`; never emit a fixed runtime count (FR6).



## 5. Implement — IR emission (Backend lead, System Designer support)

- [x] 5.1 IR emitter: populate P0 node/edge fields additively; deterministic ordering; content-addressed IDs.
- [x] 5.2 Emit the discovery run report alongside the IR.
- [x] 5.3 CLI/service entrypoint: `discover --repo <path> --config llm-eval.yaml --out ir.json`.



## 6. Test (Backend lead)

- [x] 6.1 Fixture repos with documented expected node counts; assert extracted IR matches exactly.
- [x] 6.2 **Wrapper fixture**: SDK behind an in-house function — node appears only when the
  `llm-eval.yaml` declaration is present; absent when removed (proves user-declared entrypoints).
- [x] 6.3 **Framework-DAG fixture**: LangGraph/CrewAI graph — nodes/edges come from the declarative
  definition and carry `framework_source`.
- [x] 6.4 **Loop/agent fixture**: asserts `variable_at_runtime` and that no fixed runtime count is emitted.
- [x] 6.5 **Malformed-file fixture**: skip-and-report, other files still discovered, no crash.
- [x] 6.6 **Multi-source dedup fixture**: a call site hit by both registry and declaration is one node.
- [x] 6.7 **Golden-IR diff test**: byte-identical output on re-run of unchanged source (determinism).



## 7. Harden & Review (Backend lead, DevOps support)

- [x] 7.1 **No-execution assertion**: run discovery with process spawn / plugin load denied; a fixture
  with an `init()` side effect must never fire (NFR1).
- [x] 7.2 Least-privilege worker: read-only repo mount, no network egress, no ambient provider creds (NFR7).
  > **Both halves now exist.** *Code half:* `noexec_test.go:17` is a structural guard (fails the build
  > if the analysis path imports `os/exec`/`plugin`/`net`) + `hardening_test.go:43`'s init-sentinel.
  > *Deployment half (was missing — docs/discovery/10 §57 deferred it to "DevOps §8", which turned out
  > to be GitHub Actions only, so nobody delivered it):* `deploy/docker-compose.discovery.yml`
  > (`network_mode: none`, `/repo:ro`, `environment: {}`, `read_only`, `cap_drop: [ALL]`,
  > `no-new-privileges`, non-root) + `deploy/Dockerfile.discover`, verified by
  > `make discovery-sandbox-proof` (20/20 pass) and `make discovery-sandbox-proof-redcheck`, which
  > proves the proof goes red when each claim is broken. CI job: `discovery-sandbox`.
  > **Scope:** binds `cmd/discover` only. `agentd` can never satisfy claims 2–3 — it must reach
  > providers by design. **Honest limit:** this bounds blast radius; it is *not* a claim to contain
  > hostile code. The container defends what the import guard cannot — `discover` parses untrusted
  > source through tree-sitter's C runtime via cgo. `make discovery-ci` still runs the binary on the
  > host with none of this posture.
  - Code half: `internal/discovery/noexec_test.go` (import guard: no `net`/`os/exec`/`plugin`) +
    `hardening_test.go` (`TestReadOnlyNoRepoMutation`, init-sentinel).
  - Runtime half: `deploy/docker-compose.discovery.yml` + `deploy/Dockerfile.discover` enforce all
    three claims on `cmd/discover` (the only entrypoint where they can all hold — `agentd` links
    `providergateway` and needs both creds and egress). Proven by `make discovery-sandbox-proof`
    (static + dynamic per claim); the fence's own red-ability by `make discovery-sandbox-proof-redcheck`.
    Limits stated in `docs/discovery/10-hardening-review.md` §7.2: this binds the containerised
    worker, not a direct `bin/discover` run on a host.
- [x] 7.3 Robustness: bound recursion/resource use on hostile input (deep nesting, huge literals,
  symlink cycles); degrade to per-file diagnostic (NFR5).
- [x] 7.4 Adversarial self-review: hunt silently-dropped nodes, variable-node-given-fixed-count,
  non-deterministic IDs, unhandled parse panics.



## 8. CI & Rollout (DevOps lead, System Designer support)

- [x] 8.1 CI job: run Discovery on every fixture repo and validate emitted IR against
  `workflow-ir.schema.json`; fail the build on any violation.
- [x] 8.2 CI: golden-IR drift check (determinism) and node-count regression check per fixture.
- [x] 8.3 Wire the throughput budget (≤60 s for ~200k LOC) as a soft CI signal.



## 9. Milestone M1 exit verification

- [x] 9.1 Run Discovery on a real repo **per shipped language**: static nodes extracted, IR emitted, IR diffable.
- [x] 9.2 Confirm wrapper nodes found via user-declared entrypoints on the real repo, per language.
- [x] 9.3 Confirm loop/agent nodes flagged variable-at-runtime; count reported per static definition.
- [x] 9.4 Confirm no-execution invariant held throughout, any language.
- [x] 9.5 Confirm a **mixed-language repo** yields one coherent IR spanning frontends.



## 10. Multi-language frontends (Backend lead, AI Eng + Sys Designer support) — the rescope workstream



### 10.A — Extract the language-neutral seam

- [x] 10.1 Extract a `LanguageFrontend` **interface** (`Parse`, `CallSites`) from the current Go
  implementation, so the detector/extractor/emitter consume a normalized, language-neutral call-site
  shape; the Go path becomes `GoFrontend` with **no behavior change** (existing tests stay green).
- [x] 10.2 Make the **signature registry language-tagged** (each row carries `language`); the detector
  selects rows by the active frontend's language. Node-ID `module/pkg path` + `enclosing symbol`
  become per-frontend spellings (PRD Q8).
- [x] 10.3 Add **language auto-detection** (extension/shebang/tree-sitter guess) and mixed-repo handling
  (one IR, per-node `call_site.file`). Resolves PRD Q6.



### 10.B — tree-sitter substrate

- [x] 10.4 Integrate **tree-sitter** with per-language grammars; a `TreeSitterFrontend` base that
  yields the normalized call-site shape (import map + selector-chain + enclosing symbol + structural
  position) **without executing source** (extends the no-execution guard to tree-sitter).
- [x] 10.5 Define the tree-sitter symbol-resolution floor (no types): import-presence + selector +
  declared entrypoints; document the honest `unresolved` boundary per language. Resolves PRD Q7.



### 10.C — Per-language frontends (priority order; each = frontend + registry rows + fixtures)

- [x] 10.6 **Python** frontend + registry rows (`anthropic`, `openai`, `langchain`, `langgraph`,
  `crewai`, `boto3`/Bedrock) + fixtures (wrapper, framework-DAG=LangGraph/CrewAI, loop, malformed,
  dedup, golden).
  > **Now true.** Previously the six-kind fixture list held for **Go only**: Python had no malformed
  > and no dedup fixture, and its wrapper existed only as an in-test fixture
  > (`frontend_python_test.go:97`) rather than a committed dir. All six now exist as committed
  > fixtures. Fixture corpus overall: 13 → 30.
  > The malformed fixtures exposed a real bug across **every** tree-sitter frontend: tree-sitter never
  > fails a parse — it *recovers* — so no frontend surfaced `HasError`, and malformed source was
  > silently half-analyzed under a clean report (失败要显眼 violation). `syntaxErrorDiagnostics` was
  > added to the shared substrate for all frontends (a capability in only one frontend is a bug).
  > Severity is deliberately `warn`, not `error`: the file was not skipped, so `error` would inflate
  > `files_skipped` and claim a skip that never happened.
- [ ] 10.7 **TypeScript/JavaScript** frontend + registry rows (`@anthropic-ai/sdk`, `openai`,
  `langchain`/`langgraph.js`, Vercel AI SDK) + fixtures.
  > **TypeScript done; JavaScript un-ticked.** TS now has all six fixture kinds (golden, wrapper,
  > framework-DAG, loop-in-golden, malformed, dedup). **!!! JavaScript has a registered frontend and
  > 6 registry rows but ZERO fixtures** — so `.js` support is asserted, never demonstrated. Un-ticked
  > rather than reworded: the frontend claims the language, so the fixture gap is a real gap.
- [x] 10.8 **Java/Kotlin** frontend + registry rows (langchain4j, Spring AI, Bedrock) + fixtures.
  > **Kotlin now genuinely ships** (it did not before: `frontend_java.go` returned only `.java`, there
  > were zero kotlin-tagged registry rows, and `registry.yaml:229` carried a `# ---- Java/Kotlin ----`
  > header over java-only rows — the false claim in data form). `frontend_kotlin.go` +
  > 5 kotlin registry rows + 5 fixtures. Java's AST patterns did **not** transfer (Kotlin has no `name`
  > field; `interface`→`class_declaration`; `if`/`when` are expressions), so the frontend was written
  > against the real grammar, not copied.
  > Fixtures: java = golden/wrapper/loop/malformed/dedup; kotlin = golden/wrapper/loop-in-golden/
  > malformed/dedup. **framework-DAG is N/A for both** and documented as such in each `EXPECTED.md`:
  > langchain4j/Spring AI are request/response SDKs and imperative builders, not statically-readable
  > node/edge graphs. An N/A with a written reason is a real answer; a silently absent fixture is not.
- [x] 10.9 **Rust** frontend + registry rows (`async-openai`, `anthropic` crates) + fixtures.
  > Fixtures: golden, wrapper, loop-in-golden, malformed, dedup. **framework-DAG N/A** (same reason as
  > 10.8, documented in `EXPECTED.md`). The `rust_wrapper` fixture caught a real bug on arrival: Rust
  > `use` never populated `Imports`, so Rust free-function wrappers were undetectable despite
  > `matchDeclaredEntries` documenting itself "LANGUAGE-NEUTRAL". Fixed at the source, not by editing
  > the expected count to match.
- [ ] 10.10 **Further languages** on demand behind the same interface (C#, Go already done, Ruby, …).
  > **Un-ticked — nothing was demanded, so nothing shipped.** No C#/Ruby/PHP/Swift/Scala frontend
  > exists; `frontend.go:44` lists those extensions only so they can be *reported as unsupported*
  > (a `.cs` file with an Anthropic call yields 0 nodes and an honest `LANGUAGE_UNSUPPORTED`
  > diagnostic). Building them speculatively is 八级法则 L8 cost for no demand, and 禁止清单 #15
  > ("建了等未来用") forbids it. **The task is correctly worded ("on demand") — it was simply never
  > true.** It becomes tickable the day a language is actually demanded.



### 10.D — Per-language framework readers, tests, CI

- [x] 10.11 **Python framework readers**: LangGraph + CrewAI declarative-graph readers behind **one**
  framework-reader interface (resolves the Go-vs-Python scope conflict, docs/discovery/07 §4).
  > **Reworded — the readers always worked; the claim of interface reuse was false.** They sat behind a
  > *new, parallel* `syntacticFrameworkReader`, leaving the repo with **two** framework-reader
  > interfaces. Per 「暴露冲突，不要折中平均」 the two were not blended into an average interface:
  > the `SyntacticUnit`-keyed contract **won** and the `*Package`-keyed one was **deleted**. Rationale:
  > `*Package` is `go/ast`, so it is structurally incapable of ever serving a tree-sitter frontend
  > (八级法则 L6 不可扩展). The winner absorbed the loser's degrade-to-flag + diagnostics contract
  > (a capability, not a competing design). Language scoping is now a config table
  > (`frameworkReadersByLanguage`), not an if/else chain (禁止清单 #14).
  > **Non-regression proved, not claimed** (import-parser-research-validation: 「没有纯重构例外 —
  > '等价'是声称，不是证明」): all 13 pre-existing fixtures re-run before/after → **26/26 artifacts
  > byte-identical (13 IR + 13 report)**. Diffing the IR alone would have been blind here — framework
  > subgraphs travel in the *report*. The diff harness was itself red-checked.
  > **!!! Known limitation, pinned not buried:** the shared floor drops positional fidelity —
  > `AddNode(nodeName, "notaname")` now yields node `notaname` (a guess) where the deleted Go reader
  > honestly skipped. Pre-existing in Python/TS, unreachable via valid LangGraph APIs, and outside the
  > fixture corpus — so the byte-identical proof would **not** have caught it. Pinned in
  > `TestBuilderFloorDropsPositionalFidelity_KnownLimitation`. The real fix (positional fidelity in
  > `PositionalStrings` across all six analyzers) is a substrate change beyond this task.
- [x] 10.12 **Mixed-language fixture** + golden IR proving one run spans multiple frontends.
- [x] 10.13 CI: run Discovery + schema-validate + golden-drift + node-count regression **per language**.