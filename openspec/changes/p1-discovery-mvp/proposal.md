## Why

The platform cannot expose, configure, run, measure, or optimize anything until a codebase becomes
a graph of addressable **nodes**. P0 froze the Workflow IR (which is **language-neutral** — its own
sample is Python) and the static-node vs. runtime-invocation distinction; P1 is the first producer
of that IR. It reads a repository **in any language** as **untrusted text** and extracts static LLM
call sites into a valid IR.

P1 is **language-agnostic by design**: a language-neutral core (registry model, node-ID scheme,
metadata extraction, IR emission, run report, invariants) sits behind pluggable **`LanguageFrontend`s**
— **Go** via `go/ast`, and **all other languages** (Python, TypeScript/JavaScript, Java/Kotlin, Rust,
…) via a **tree-sitter** substrate. Adding a language is *adding a frontend + registry rows + fixtures*,
never a core rewrite.

Two realities make naïve discovery wrong and shape this change, **in every language**. First, **real
codebases wrap the SDK** — the SDK leaf call sits behind an in-house `Complete()`/`complete()`/
`generateSummary()` helper — so a signature registry alone under-counts nodes. **User-declared
entrypoints** (`llm-eval.yaml`, language-neutral) are therefore a mandatory, co-equal detection source,
not an add-on. Second, **"how many nodes make LLM requests" is only well-defined for static
definitions**: a loop or agent makes a *variable* number of calls at runtime, so node count is
reported **per static definition** and loop/agent nodes are flagged variable-at-runtime. Confirming
the candidate graph by running it is dynamic tracing — explicitly deferred to **P5**; P1 is
static-only and **never executes the target repo, in any language** (tree-sitter is a pure parser).

Upstream dependency: P0/M0 (frozen `workflow-ir.schema.json`, typed I/O contract fields, content-hash
conventions, green CI). This change unblocks P2 (Config Layer wraps discovered nodes), P2.5 (Metrics
key on `node_id`), P3.5 (Pattern Classifier reads IR topology), and P4+ (per-node attribution).

## What Changes

- Add a **Discovery Engine** service (implemented in Go) that parses a repo **in any language**,
  **never executing it**, via a **`LanguageFrontend` abstraction**: **Go** through `go/ast`, and
  **Python, TypeScript/JavaScript, Java/Kotlin, Rust, …** through a **tree-sitter** substrate. The
  language-neutral core is shared across all frontends.
- Add a **per-language signature registry** of known SDK entrypoints — Anthropic Messages, OpenAI Chat
  Completions, LangChain/LangGraph invoke, Bedrock Converse (Go), the `anthropic`/`openai`/`langchain`/
  `langgraph`/`crewai`/`boto3` families (Python), `@anthropic-ai/sdk`/`openai`/`langchain.js`/Vercel AI
  SDK (TS/JS), langchain4j/Spring AI (Java), `async-openai`/`anthropic` crates (Rust) — a data-driven,
  language-tagged, extensible detection table.
- Add **mandatory user-declared entrypoints** via `llm-eval.yaml`, treated as a co-equal detection
  source so SDK wrappers are discovered. **This is not optional.**
- Add **per-call-site metadata extraction**: model arg, messages/prompt construction, tools/skills
  passed, upstream data flow — marking statically-unresolvable fields `unresolved`, never omitting them.
- Add **call-graph construction**: nodes = LLM-invoking functions/agent steps; edges = data/control flow.
- Add **framework DAG special-casing** (per language, one interface): derive nodes/edges from
  declarative graphs — LangGraph/CrewAI (Python), LangGraphGo/langchaingo (Go), equivalents elsewhere —
  rather than inferring topology from call order.
- Add **static-vs-runtime node counting**: report per static definition; flag loop/agent nodes as
  `variable_at_runtime`; never emit a fixed runtime invocation count.
- Add **valid, deterministic IR emission** validated against `workflow-ir.schema.json` and diffable.
- Add **ambiguity flags** marking call sites with unresolved static data flow as P5 dynamic-trace candidates.
- Add a **no-execution safety invariant** as a first-class, testable non-functional requirement (any
  language; tree-sitter parses without executing).
- Add a **CI job** that runs Discovery on fixture repos **per language** and validates emitted IR
  against the schema, plus a golden-IR drift check and a mixed-language fixture.

## Impact

- **Affected capabilities:** `discovery-engine` (new).
- **Affected code/systems:** new Discovery service (implemented in Go) with a **`LanguageFrontend`
  abstraction** — a `go/ast` frontend and a **tree-sitter** frontend per additional language (Python,
  TS/JS, Java/Kotlin, Rust, …) — plus the shared core (detector, metadata extractor, call-graph
  builder, framework readers, IR emitter, run report); the language-neutral `llm-eval.yaml` config
  format; per-language fixture-repo suites + a mixed-language fixture; CI schema-validation + golden-IR
  job per language. Consumes the P0 `workflow-ir.schema.json`.
- **Dependencies:** requires P0/M0 (frozen IR schema, typed I/O contract fields, content-hash
  conventions, CI). Adds a **tree-sitter** dependency (and per-language grammars) for non-Go frontends.
  Unblocks P2, P2.5, P3.5, P4+, and provides the ambiguity flags P5 consumes. **Note:** P2's
  source-transformation codemod becomes per-language downstream of this.
- **Scope note:** this change was **rescoped from Go-only to multi-language** (see PRD §15); the Go
  frontend is the first of several, and the shared core is unchanged per language.
