## Why

The platform cannot expose, configure, run, measure, or optimize anything until a codebase becomes
a graph of addressable **nodes**. P0 froze the Workflow IR and the static-node vs.
runtime-invocation distinction; P1 is the first producer of that IR. It reads a Go repository as
**untrusted text** and extracts static LLM call sites into a valid IR.

Two realities make naïve discovery wrong and shape this change. First, **real codebases wrap the
SDK** — `anthropic.Messages.New(...)` sits behind an in-house `Complete()`/`Generate()` helper — so
a signature registry alone under-counts nodes. **User-declared entrypoints** (`llm-eval.yaml`) are
therefore a mandatory, co-equal detection source, not an add-on. Second, **"how many nodes make LLM
requests" is only well-defined for static definitions**: a loop or agent makes a *variable* number
of calls at runtime, so node count is reported **per static definition** and loop/agent nodes are
flagged variable-at-runtime. Confirming the candidate graph by running it is dynamic tracing —
explicitly deferred to **P5**; P1 is static-only and **never executes the target repo**.

Upstream dependency: P0/M0 (frozen `workflow-ir.schema.json`, typed I/O contract fields, content-hash
conventions, green CI). This change unblocks P2 (Config Layer wraps discovered nodes), P2.5 (Metrics
key on `node_id`), P3.5 (Pattern Classifier reads IR topology), and P4+ (per-node attribution).

## What Changes

- Add a **Discovery Engine** service (Go) that parses a Go repo with `go/ast`, **never executing it**.
- Add a **signature registry** of known SDK entrypoints (Anthropic Messages, OpenAI Chat Completions,
  LangChain/LangGraph invoke, Bedrock Converse) — a data-driven, extensible detection table.
- Add **mandatory user-declared entrypoints** via `llm-eval.yaml`, treated as a co-equal detection
  source so SDK wrappers are discovered. **This is not optional.**
- Add **per-call-site metadata extraction**: model arg, messages/prompt construction, tools/skills
  passed, upstream data flow — marking statically-unresolvable fields `unresolved`, never omitting them.
- Add **call-graph construction**: nodes = LLM-invoking functions/agent steps; edges = data/control flow.
- Add **framework DAG special-casing**: derive nodes/edges from LangGraph/CrewAI declarative graphs
  rather than inferring topology from call order.
- Add **static-vs-runtime node counting**: report per static definition; flag loop/agent nodes as
  `variable_at_runtime`; never emit a fixed runtime invocation count.
- Add **valid, deterministic IR emission** validated against `workflow-ir.schema.json` and diffable.
- Add **ambiguity flags** marking call sites with unresolved static data flow as P5 dynamic-trace candidates.
- Add a **no-execution safety invariant** as a first-class, testable non-functional requirement.
- Add a **CI job** that runs Discovery on fixture repos and validates emitted IR against the schema.

## Impact

- **Affected capabilities:** `discovery-engine` (new).
- **Affected code/systems:** new Go Discovery service (loader, `go/ast` parser, detector, metadata
  extractor, call-graph builder, framework readers, IR emitter); `llm-eval.yaml` config format;
  fixture-repo suite; CI schema-validation + golden-IR job. Consumes the P0 `workflow-ir.schema.json`.
- **Dependencies:** requires P0/M0 (frozen IR schema, typed I/O contract fields, content-hash
  conventions, CI). Unblocks P2, P2.5, P3.5, P4+, and provides the ambiguity flags P5 consumes.
