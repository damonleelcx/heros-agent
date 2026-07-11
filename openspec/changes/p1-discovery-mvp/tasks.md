# Tasks — P1 Discovery MVP (Go, static)

Ordered, independently-verifiable tasks grouped by workstream/role. Backend leads
(explore → design → implement → test → harden → review); AI Eng, DevOps, System Designer support.

## 1. Understand & Explore (Backend lead, AI Eng support)
- [ ] 1.1 Confirm the frozen P0 IR contract: node fields, edge fields, typed I/O contract stubs, and
  the static-node vs. runtime-invocation distinction Discovery must populate.
- [ ] 1.2 Survey real Go LLM repos; catalog concrete call shapes (direct SDK calls, options structs,
  interface-typed clients, in-house `Complete()`/`Generate()` wrappers). Record the wrapper patterns
  that defeat signature matching.
- [ ] 1.3 Write down the invariants that must never break: no execution of the repo; no fixed count for
  a variable node; stable node IDs; every missing node explainable from the run report.

## 2. Design (Backend lead, System Designer + AI Eng support)
- [ ] 2.1 Design the **signature registry** data model (package-qualified entrypoint → argument map)
  and the seed rows: Anthropic Messages, OpenAI Chat Completions, LangChain/LangGraph invoke, Bedrock Converse.
- [ ] 2.2 Design the **`llm-eval.yaml`** schema: declared entrypoints and the mapping from argument
  position/name to IR metadata fields (model, prompt, tools). (Resolves PRD Q1.)
- [ ] 2.3 Design the **node-ID scheme**: content-addressed tuple (package path + func + call-site
  identity + content hash) that is stable across benign line shifts yet unique per call site. (PRD Q4.)
- [ ] 2.4 Design the **`FrameworkReader`** interface and the LangGraph/CrewAI declarative-graph mapping;
  define version-drift behavior (degrade-to-flag, not crash). (PRD Q2.)
- [ ] 2.5 Design **failure behavior** for each fault: unparseable file, bad `llm-eval.yaml` symbol,
  cyclic imports, unresolved model/prompt arg → decided outcome (skip-and-report / mark-unresolved-and-flag).
- [ ] 2.6 Define the **discovery run report** shape (files scanned, detections by source, nodes emitted,
  ambiguity flags, per-file diagnostics).

## 3. Implement — parsing & detection (Backend lead)
- [ ] 3.1 Loader: read-only repo walk treating source as untrusted text; stream per package to bound memory.
- [ ] 3.2 `go/ast` parser with type/import resolution; bounded recursion on expression walking; **no**
  process spawn, `go run`, `go build -buildmode=plugin`, or plugin load anywhere in the path.
- [ ] 3.3 Registry detector: resolve call expressions against the signature registry (FR1).
- [ ] 3.4 Declared-entrypoint detector: load `llm-eval.yaml`, resolve declared entrypoints as co-equal
  call sites, map args to metadata (FR2).
- [ ] 3.5 Merge/dedup detection sources by stable call-site identity so one call site = one node.

## 4. Implement — extraction & graph (Backend lead, AI Eng support)
- [ ] 4.1 Metadata extractor: model arg, messages/prompt construction, tools/skills, upstream data flow;
  intra-procedural resolution where possible, `unresolved` (not omitted) where not (FR3).
- [ ] 4.2 Ambiguity flagging: mark unresolved prompt-construction/data-flow call sites as P5
  dynamic-trace candidates with a reason (FR8). (AI Eng owns the fidelity judgment.)
- [ ] 4.3 Call-graph builder: nodes = LLM-invoking functions/agent steps; edges = data/control flow (FR4).
- [ ] 4.4 Framework readers: read LangGraph/CrewAI declarative DAGs; derive nodes/edges from the
  definition, tag `framework_source` on the subgraph (FR5).
- [ ] 4.5 Static-vs-runtime counting: per-static-definition count; flag loop/agent nodes
  `variable_at_runtime`; never emit a fixed runtime count (FR6).

## 5. Implement — IR emission (Backend lead, System Designer support)
- [ ] 5.1 IR emitter: populate P0 node/edge fields additively; deterministic ordering; content-addressed IDs.
- [ ] 5.2 Emit the discovery run report alongside the IR.
- [ ] 5.3 CLI/service entrypoint: `discover --repo <path> --config llm-eval.yaml --out ir.json`.

## 6. Test (Backend lead)
- [ ] 6.1 Fixture repos with documented expected node counts; assert extracted IR matches exactly.
- [ ] 6.2 **Wrapper fixture**: SDK behind an in-house function — node appears only when the
  `llm-eval.yaml` declaration is present; absent when removed (proves user-declared entrypoints).
- [ ] 6.3 **Framework-DAG fixture**: LangGraph/CrewAI graph — nodes/edges come from the declarative
  definition and carry `framework_source`.
- [ ] 6.4 **Loop/agent fixture**: asserts `variable_at_runtime` and that no fixed runtime count is emitted.
- [ ] 6.5 **Malformed-file fixture**: skip-and-report, other files still discovered, no crash.
- [ ] 6.6 **Multi-source dedup fixture**: a call site hit by both registry and declaration is one node.
- [ ] 6.7 **Golden-IR diff test**: byte-identical output on re-run of unchanged source (determinism).

## 7. Harden & Review (Backend lead, DevOps support)
- [ ] 7.1 **No-execution assertion**: run discovery with process spawn / plugin load denied; a fixture
  with an `init()` side effect must never fire (NFR1).
- [ ] 7.2 Least-privilege worker: read-only repo mount, no network egress, no ambient provider creds (NFR7).
- [ ] 7.3 Robustness: bound recursion/resource use on hostile input (deep nesting, huge literals,
  symlink cycles); degrade to per-file diagnostic (NFR5).
- [ ] 7.4 Adversarial self-review: hunt silently-dropped nodes, variable-node-given-fixed-count,
  non-deterministic IDs, unhandled parse panics.

## 8. CI & Rollout (DevOps lead, System Designer support)
- [ ] 8.1 CI job: run Discovery on every fixture repo and validate emitted IR against
  `workflow-ir.schema.json`; fail the build on any violation.
- [ ] 8.2 CI: golden-IR drift check (determinism) and node-count regression check per fixture.
- [ ] 8.3 Wire the throughput budget (≤60 s for ~200k LOC) as a soft CI signal.

## 9. Milestone M1 exit verification
- [ ] 9.1 Run Discovery on a real Go repo: static nodes extracted, IR emitted, IR diffable.
- [ ] 9.2 Confirm wrapper nodes found via user-declared entrypoints on the real repo.
- [ ] 9.3 Confirm loop/agent nodes flagged variable-at-runtime; count reported per static definition.
- [ ] 9.4 Confirm no-execution invariant held throughout.
