# Discovery Engine — P1 Delta

Cross-reference: [`../../../../docs/prd/P1-discovery-mvp.md`](../../../../docs/prd/P1-discovery-mvp.md).

## ADDED Requirements

### Requirement: The Discovery Engine SHALL be language-agnostic behind a `LanguageFrontend` abstraction

Discovery SHALL separate a **language-neutral core** (signature-registry model, node-ID scheme,
metadata extraction, call-graph builder, IR emission, run report, and the no-execution/determinism/
faithfulness invariants) from a per-language **`LanguageFrontend`** that parses source and enumerates
call sites in a normalized shape. The **Go** frontend SHALL use `go/ast`; **all other languages**
(Python, TypeScript/JavaScript, Java/Kotlin, Rust, …) SHALL use a **tree-sitter** substrate. Adding a
language SHALL require adding a frontend + signature-registry rows + fixtures, with **no change to the
core**. The emitted IR SHALL record `workflow.language`.

#### Scenario: The Go frontend and a tree-sitter frontend feed the same core
- **WHEN** Discovery runs over a Go repo and over a Python repo
- **THEN** both produce IR that validates against `workflow-ir.schema.json`
- **AND** each IR records its `workflow.language`
- **AND** the detection, node-ID, extraction, and emission logic exercised is the same language-neutral
  core for both.

#### Scenario: Adding a language does not change the core
- **WHEN** a new `LanguageFrontend` and its signature-registry rows are added
- **THEN** call sites in that language are detected and emitted as nodes
- **AND** no change is required to the registry model, node-ID scheme, extractor, emitter, or run report.

#### Scenario: A mixed-language repository yields one coherent IR
- **WHEN** a repository contains source in more than one supported language
- **THEN** Discovery selects the appropriate frontend per file
- **AND** emits one Workflow IR whose nodes span the languages, each node's `call_site.file` naming its
  source file.

### Requirement: The Discovery Engine SHALL detect LLM call sites that match a known-SDK signature registry

The signature registry is a data-driven, **language-tagged** table of module/import-qualified SDK
entrypoints with an argument map. The seed registry SHALL cover, **per language**, the major SDKs —
Anthropic Messages, OpenAI Chat Completions, LangChain/LangGraph invoke, and Bedrock Converse in Go;
the `anthropic`/`openai`/`langchain`/`langgraph`/`crewai`/`boto3` families in Python; `@anthropic-ai/sdk`/
`openai`/`langchain.js`/Vercel AI SDK in TypeScript/JavaScript; langchain4j/Spring AI in Java; the
`async-openai`/`anthropic` crates in Rust — and SHALL be extensible by adding a table entry without
changing detector code.

#### Scenario: Direct Anthropic SDK call is detected
- **WHEN** a source file (Go resolved via `go/ast`; other languages via the tree-sitter frontend's
  import + selector resolution) contains a call expression that resolves to the Anthropic Messages
  entrypoint in the signature registry for that language
- **THEN** the engine emits exactly one IR node for that call site
- **AND** the run report's provenance for the node includes `registry`
- **AND** the node's `call_site` records the file, symbol, and line span of the call.

#### Scenario: Registry is extended by data, not code
- **WHEN** a new SDK entrypoint row is added to the signature registry table and Discovery is re-run
- **THEN** call sites resolving to that entrypoint are detected as nodes with no change to detector code.

#### Scenario: A same-named function from an unrelated module is not a false positive
- **WHEN** a call resolves to a function whose name matches a registry entry but whose module/import
  path does not match the registry entry
- **THEN** the engine does NOT emit a node for that call site.

### Requirement: The Discovery Engine SHALL detect LLM call sites declared as user entrypoints in `llm-eval.yaml`

Real codebases wrap the SDK behind in-house functions, so signature matching alone misses nodes.
User-declared entrypoints are a **mandatory, co-equal** detection source: a call site resolving to a
declared entrypoint SHALL produce a node of equal standing to a registry hit, and the declared
argument positions/names SHALL be mapped to node metadata fields.

#### Scenario: SDK hidden behind an in-house wrapper is discovered via declaration
- **WHEN** a repo calls an in-house function `internal/llm.Complete(ctx, modelID, prompt)` that wraps
  the SDK, and `llm-eval.yaml` declares `internal/llm.Complete` as an entrypoint with `model`→`modelID`
  and `prompt`→`prompt`
- **THEN** the engine emits a node for each call to `internal/llm.Complete`
- **AND** the node's `detected_by` includes `declared`
- **AND** the node's `model` and `prompt_construction` are populated from the mapped arguments.

#### Scenario: The declaration is what surfaces the node
- **WHEN** the same wrapper repo is discovered with the `internal/llm.Complete` declaration removed
  from `llm-eval.yaml` and the wrapper matches no registry entry
- **THEN** no node is emitted for those call sites — proving user-declared entrypoints are the
  mechanism that finds wrapped nodes.

#### Scenario: A declaration pointing at a nonexistent symbol is reported, not fatal
- **WHEN** `llm-eval.yaml` declares an entrypoint symbol that does not resolve in the repo
- **THEN** the engine records a diagnostic in the run report for that declaration
- **AND** discovery of all other call sites continues.

### Requirement: The Discovery Engine SHALL extract per-call-site metadata, marking statically-unresolvable fields as unresolved rather than omitting them

For each detected call site the engine SHALL extract the model argument, the messages/prompt
construction, the tools/skills passed, and the upstream data flow feeding the prompt. A field that
cannot be resolved by static (intra-procedural) analysis SHALL be emitted with an explicit
`unresolved` marker, never silently dropped and never replaced by a guessed value.

#### Scenario: Literal and locally-constructed arguments resolve
- **WHEN** a call site passes a string-literal model ID and a locally-assembled message slice
- **THEN** the node's `model` holds the resolved model ID
- **AND** the node's `prompt_construction` reflects the locally-assembled messages.

#### Scenario: A runtime-selected argument is marked unresolved
- **WHEN** a call site's model argument is a variable whose value is chosen at runtime (e.g. selected
  from a map by an input parameter) and cannot be resolved intra-procedurally
- **THEN** the node's `model` field is emitted as `unresolved`
- **AND** the field is not omitted and is not populated with an inferred value.

### Requirement: The Discovery Engine SHALL construct a call graph of nodes and data/control-flow edges

Nodes are LLM-invoking functions/agent steps; edges represent data or control flow, where the output
of one node feeds the input of another. The engine SHALL emit this node/edge set as the Workflow IR.

#### Scenario: Output of one node feeding another produces a data edge
- **WHEN** node A's return value is parsed and passed into the prompt construction of node B
- **THEN** the engine emits a directed edge from A to B with `kind = data`
- **AND** the edge records the evidence (the value passed) that justifies it.

#### Scenario: Detection sources are merged, not double-counted
- **WHEN** a single call site is matched by both the signature registry and a user declaration
- **THEN** the engine emits exactly one node for that call site
- **AND** the node's `detected_by` lists both `registry` and `declared`.

### Requirement: The Discovery Engine SHALL special-case framework DAGs by reading their declarative graph definition

When a recognized framework declares its workflow graph — LangGraph/CrewAI (Python),
LangGraphGo/langchaingo (Go), and equivalents in other languages — the engine SHALL derive nodes and
edges from that declarative definition rather than inferring topology from call order, and SHALL record
the framework as the source on the affected subgraph. Framework readers are per-language implementations
of one `FrameworkReader` interface.

#### Scenario: A declarative graph's nodes and edges come from the declaration
- **WHEN** a repo builds a framework graph declaratively (nodes and edges registered on a graph object —
  e.g. a LangGraph graph in Python or a langgraphgo state graph in Go)
- **THEN** the engine emits nodes and edges matching the declared graph structure
- **AND** the affected subgraph's `framework_source` records the framework
- **AND** the topology is taken from the declaration, not inferred from the order of calls in source.

#### Scenario: Unrecognized framework version degrades to a flag, not a crash
- **WHEN** the framework graph uses a version the reader does not recognize
- **THEN** the engine emits a best-effort subgraph with a diagnostic flag in the run report
- **AND** does not crash and does not silently drop the subgraph.

### Requirement: The Discovery Engine SHALL report node count per static definition and flag loop/agent nodes as variable-at-runtime

"How many nodes make LLM requests" is only well-defined for static definitions; loops and agents make
a variable number of calls at runtime. The engine SHALL count nodes **per static definition** and
SHALL flag any node reachable through a loop or agent control structure as `variable_at_runtime`,
never emitting a fixed runtime invocation count.

#### Scenario: An agent loop is one static node flagged variable-at-runtime
- **WHEN** an LLM call site sits inside a `for` loop / agent control structure that may invoke it an
  unknown number of times at runtime
- **THEN** the engine emits exactly one static node for that call site
- **AND** the node is flagged `variable_at_runtime`
- **AND** no fixed runtime invocation count is emitted for it.

#### Scenario: Node count is reported per static definition
- **WHEN** discovery completes on a repo
- **THEN** the reported node count equals the number of static LLM-call definitions
- **AND** does not attempt to sum runtime invocations.

### Requirement: The Discovery Engine SHALL emit IR that validates against the P0 schema and is deterministic and diffable

Emitted IR SHALL validate against `workflow-ir.schema.json`. Node IDs SHALL be content-addressed from
stable inputs and output SHALL be deterministically ordered, so two runs on unchanged source produce
byte-identical, diffable output.

#### Scenario: Emitted IR passes schema validation in CI
- **WHEN** the CI job runs Discovery on a fixture repo and validates the emitted IR against
  `workflow-ir.schema.json`
- **THEN** validation passes with no schema violations
- **AND** the build fails if any emitted IR violates the schema.

#### Scenario: Re-running on unchanged source is byte-identical
- **WHEN** Discovery is run twice on the same repo state (same content hashes)
- **THEN** the two emitted IR outputs are byte-identical
- **AND** node IDs are stable across the two runs.

### Requirement: The Discovery Engine SHALL flag call sites with unresolved static data flow as P5 dynamic-tracing candidates

Where prompt-construction or upstream data flow cannot be resolved statically, the engine SHALL record
an ambiguity flag identifying the call site, the unresolved field, and the reason, marking it a
candidate for P5 dynamic tracing.

#### Scenario: Inter-procedural prompt assembly is flagged for P5
- **WHEN** a node's prompt is assembled across function boundaries such that static analysis cannot
  determine its full construction
- **THEN** the node carries an `ambiguity_flag` naming the unresolved field and the reason
- **AND** the flag marks the call site as a P5 dynamic-tracing candidate.

### Requirement: The Discovery Engine SHALL never execute the target repository during analysis

Discovery operates over the AST/parse-tree and text only, **in any language**. It SHALL NOT run,
evaluate, interpret, compile-and-run, `go run`/`python`/`node`, build with plugin mode, import as a
plugin, or otherwise execute any target-repo code at any point. Discovered source is untrusted. This is
a security invariant with its own test. (Tree-sitter is a pure parser and does not execute source,
which is part of why it is the multi-language substrate.)

#### Scenario: No target code runs during discovery
- **WHEN** Discovery analyzes a fixture repo (in any supported language) containing module-init /
  top-level code with an observable side effect (e.g. writing a file or opening a network connection)
- **THEN** that side effect never occurs during discovery.

#### Scenario: The discovery path spawns no process
- **WHEN** Discovery runs with process spawning and plugin loading denied by the environment
- **THEN** discovery completes successfully without attempting to spawn a process or load a plugin.

#### Scenario: The worker has no ambient credentials or network egress
- **WHEN** the discovery worker runs
- **THEN** it operates with read-only access to the repo, no network egress, and no ambient provider
  credentials.
