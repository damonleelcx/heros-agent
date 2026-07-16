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
- [x] 10.7 **TypeScript/JavaScript** frontend + registry rows (`@anthropic-ai/sdk`, `openai`,
  `langchain`/`langgraph.js`, Vercel AI SDK) + fixtures.
- [x] 10.8 **Java/Kotlin** frontend + registry rows (langchain4j, Spring AI, Bedrock) + fixtures.
- [x] 10.9 **Rust** frontend + registry rows (`async-openai`, `anthropic` crates) + fixtures.
- [x] 10.10 **Further languages** on demand behind the same interface (C#, Go already done, Ruby, …).



### 10.D — Per-language framework readers, tests, CI

- [x] 10.11 **Python framework readers**: LangGraph + CrewAI declarative-graph readers behind the
  existing `FrameworkReader` interface (resolves the Go-vs-Python scope conflict, docs/discovery/07 §4).
- [x] 10.12 **Mixed-language fixture** + golden IR proving one run spans multiple frontends.
- [x] 10.13 CI: run Discovery + schema-validate + golden-drift + node-count regression **per language**.