# P1 Discovery — Design: Signature registry data model & seed rows

> **Task:** P1 `tasks.md` §2.1. **Phase:** ② Design (Backend lead, System Designer + AI Eng support).
> **Method:** system-designer discipline — every decision runs *problem → design → why appropriate →
> alternatives compared → effect*, arbitrated by the 8-level cost law (安全 > 稳定 > UX > 运维 > 演进 >
> 扩展 > 维护 > 实现). **Inputs:** [call-shape catalog §1–§4](02-go-call-shape-catalog.md),
> [contract confirmation](01-ir-contract-confirmation.md), [invariants I5/I6](03-discovery-invariants.md).

## §0 TL;DR

The signature registry is **data, not code** (config > table > code): a versioned table of rows, each
mapping an **import-path-qualified SDK entrypoint** to an **argument map** that says where the frozen IR
fields (`model`, `prompt`, `tools_skills`) live in that call. Adding an SDK is adding a row, never
editing detector code (FR1, D1). Five seed rows cover Anthropic Messages, OpenAI Chat Completions
(official + the community `sashabaranov` client), LangChain(Go) invoke, and Bedrock Converse/InvokeModel.

## §1 The problem this solves

The catalog (§2) proved a flat `pkg.Func(arg0, arg1)` signature table **mis-maps every real Go LLM
call**: model/prompt/tools arrive as **options-struct fields**, **functional options**, or **request
objects**, on **nested-service methods** and **interfaces**, sometimes wrapped in `aws.String`/`F(...)`.
A registry that only stores "package + function name" cannot say *where the model is*. So the registry's
real job is not "match a name" — it is **"resolve a call to the IR node fields."**

## §2 Data model

### 2.1 Decision — registry is a loaded data table, embedded as a default

**Design.** Rows live in a data file (`registry.yaml`, embedded via `go:embed` as the built-in default,
overridable by an operator-supplied file). Detector code is generic over rows.
**Why appropriate.** The whole point of FR1 ("extensible without code change") is level-6 extensibility.
**Alternatives compared.** (a) *Hardcoded `switch` on package/func in Go* — rejected: every new SDK edits
core detector code (L6 扩展 violation; the catalog's "three parallel hand-maintained provider tables" is
named the top architecture debt in the designpattern library — do not add a fourth). (b) *Registry in
Postgres* — rejected: a one-way-door table for static, git-reviewable data; config-over-table says keep
it a file (single source of truth, diffable, no migration). **Effect.** Adding an SDK = a reviewed PR
adding YAML rows; no detector change, no schema migration.

### 2.2 The Row shape

```go
// Registry is generic over these rows; nothing about a specific SDK lives in code.
type SignatureRow struct {
    ID          string        // stable row id, e.g. "anthropic.messages.new" (referenced by the run report)
    ImportPath  string        // KEY ON THIS, never the local package name — two "openai" packages collide (catalog §1.3)
    SymbolKind  SymbolKind    // how the entrypoint is reached (see 2.3)
    Selector    string        // the call selector chain: "Messages.New", "Chat.Completions.New",
                              //   "CreateChatCompletion", "GenerateContent", "Converse"
    Streaming   bool          // true for the *Stream/*Streaming variant — a SEPARATE row, same node semantics
    ProviderHint string       // static provider for model.provider when the SDK fixes it ("anthropic","openai",
                              //   "bedrock"); "" => provider is unresolved at the call site (e.g. langchaingo)
    ArgMap      ArgMap        // where model/prompt/tools live (see 2.4)
    Opacity     []string      // IR fields that are inherently unresolvable for this entrypoint
                              //   (e.g. ["prompt","model.params"] for Bedrock InvokeModel's []byte Body)
    VersionRange string       // optional semver range the row is known-good for (advisory in P1)
}

type SymbolKind int
const (
    PackageFunc         SymbolKind = iota // llms.GenerateFromSinglePrompt(...)
    ClientMethod                          // client.CreateChatCompletion(...) / client.Converse(...)
    NestedServiceMethod                   // client.Messages.New / client.Chat.Completions.New
    InterfaceMethod                       // llms.Model.GenerateContent  (concrete impl chosen at runtime)
)
```

`SymbolKind` exists because resolution differs: a `NestedServiceMethod` must be matched by resolving the
receiver chain via type info (the bare method name `New` is generic and everywhere); an `InterfaceMethod`
matches the interface method set and accepts that the concrete provider may be `unresolved` (catalog W3).

### 2.3 Decision — key on import path + resolved selector, via `go/types`, not on source text

**Problem.** `openai.CreateChatCompletion` is ambiguous — `github.com/openai/openai-go` and
`github.com/sashabaranov/go-openai` both use local package name `openai`. **Design.** Match using
`go/types` object resolution: resolve the call's selected object to its **defining package's import
path** and compare to `ImportPath`; the local alias is irrelevant. **Alternatives compared.**
*String/regex match on `openai.Create…`* — rejected: collides across the two libraries (would double- or
mis-count), and breaks under import aliases (`oa "github.com/…/go-openai"`). **Effect.** A repo importing
both clients is disambiguated correctly; aliased imports still match.

### 2.4 The argument map — where the IR fields live

This is the core of the row. Each IR field target maps to an **arg locator**; the catalog (§2, §4) proved
four locator forms are required, so one flat "positional index" is insufficient.

```go
type ArgMap struct {
    Model  ArgLocator   // -> node.model
    Prompt ArgLocator   // -> node.prompt (messages/prompt construction)
    Tools  ArgLocator   // -> node.tools_skills
    // (context_assembly is derived by the extractor, not the registry — §4.1 implementation)
}

type ArgLocator struct {
    Form   LocatorForm  // Positional | ParamName | FieldPath | OptionCtor | ConstructionBound | Opaque
    Index  int          // Positional: 0-based arg index
    Name   string       // ParamName: parameter name
    Path   string       // FieldPath: "req.Model" / "params.Messages" (struct-field path on an options-struct arg)
    Option string       // OptionCtor: functional-option constructor name, e.g. "WithModel"
    Unwrap []string     // value-wrappers to see through before reading the literal:
                        //   ["aws.String"], ["openai.F"], ["anthropic.F"], ["param.NewOpt"]
}

type LocatorForm int
const (
    Positional        LocatorForm = iota // f(ctx, prompt) -> prompt at index 1
    ParamName                            // by parameter name
    FieldPath                            // struct-field path on an options/request struct (the common case)
    OptionCtor                           // variadic functional option: WithModel(x)
    ConstructionBound                    // model bound on the receiver at construction (catalog W4) -> resolver
                                         //   walks to the constructor; if intra-procedural budget exceeded => Opaque
    Opaque                               // inherently unresolvable (Bedrock InvokeModel Body) => sentinel + flag (I5)
)
```

**Decision — the registry names *where*, the extractor decides *resolvable vs `unresolved`*.** The row
says "model is `FieldPath req.Model`"; the metadata extractor (§4.1) then does bounded intra-procedural
resolution of that location and, if it can't reach a literal/const, emits the `unresolved` sentinel and a
report flag (I5). The registry never guesses a value. **Effect.** Provenance ("we knew where to look")
and honesty ("we couldn't resolve it") stay separable — exactly the split the invariants require.

## §3 Seed rows (the five the catalog verified)

Written in the intended `registry.yaml` shape. `field:` = `FieldPath`, `option:` = `OptionCtor`,
`index:` = `Positional`.

```yaml
version: "1.0.0"
rows:
  # 1. Anthropic — anthropic-sdk-go (official). Options struct; v1 drops the F() wrapper, older code keeps it.
  - id: anthropic.messages.new
    import_path: github.com/anthropics/anthropic-sdk-go
    symbol_kind: nested_service_method
    selector: Messages.New
    provider_hint: anthropic
    arg_map:
      model:  { field: "params.Model",    unwrap: ["anthropic.F"] }
      prompt: { field: "params.Messages",  unwrap: ["anthropic.F"] }   # + params.System merged by extractor
      tools:  { field: "params.Tools",     unwrap: ["anthropic.F"] }
  - id: anthropic.messages.newstreaming
    import_path: github.com/anthropics/anthropic-sdk-go
    symbol_kind: nested_service_method
    selector: Messages.NewStreaming
    streaming: true
    provider_hint: anthropic
    arg_map: { model: {field: "params.Model"}, prompt: {field: "params.Messages"}, tools: {field: "params.Tools"} }

  # 2. OpenAI — openai-go (official). Doubly-nested service.
  - id: openai.chat.completions.new
    import_path: github.com/openai/openai-go
    symbol_kind: nested_service_method
    selector: Chat.Completions.New
    provider_hint: openai
    arg_map:
      model:  { field: "params.Model" }
      prompt: { field: "params.Messages" }
      tools:  { field: "params.Tools" }
  - id: openai.chat.completions.newstreaming
    import_path: github.com/openai/openai-go
    symbol_kind: nested_service_method
    selector: Chat.Completions.NewStreaming
    streaming: true
    provider_hint: openai
    arg_map: { model: {field: "params.Model"}, prompt: {field: "params.Messages"}, tools: {field: "params.Tools"} }

  # 3. OpenAI — sashabaranov/go-openai (community; most common). SAME local package name "openai" as row 2 —
  #    disambiguated purely by import_path (§2.3).
  - id: sashabaranov.createchatcompletion
    import_path: github.com/sashabaranov/go-openai
    symbol_kind: client_method
    selector: CreateChatCompletion
    provider_hint: openai
    arg_map:
      model:  { field: "req.Model" }
      prompt: { field: "req.Messages" }
      tools:  { field: "req.Tools" }
  - id: sashabaranov.createchatcompletionstream
    import_path: github.com/sashabaranov/go-openai
    symbol_kind: client_method
    selector: CreateChatCompletionStream
    streaming: true
    provider_hint: openai
    arg_map: { model: {field: "req.Model"}, prompt: {field: "req.Messages"}, tools: {field: "req.Tools"} }

  # 4. LangChain(Go) — interface-typed. Provider UNKNOWN at the call site; model often at construction/env
  #    (catalog §1.4) => provider_hint empty, model locator OptionCtor with ConstructionBound fallback.
  - id: langchaingo.model.generatecontent
    import_path: github.com/tmc/langchaingo/llms
    symbol_kind: interface_method
    selector: Model.GenerateContent
    provider_hint: ""                     # resolved from the concrete impl if the resolver can reach it, else unresolved
    arg_map:
      model:  { option: "WithModel" }     # else ConstructionBound (openai.New(openai.WithModel(...))) else unresolved
      prompt: { index: 1 }                # messages []MessageContent
      tools:  { option: "WithTools" }
  - id: langchaingo.generatefromsingleprompt
    import_path: github.com/tmc/langchaingo/llms
    symbol_kind: package_func
    selector: GenerateFromSinglePrompt
    provider_hint: ""
    arg_map:
      model:  { option: "WithModel" }
      prompt: { index: 2 }                # (ctx, llm, prompt, ...opts)
      tools:  { option: "WithTools" }

  # 5. AWS Bedrock — bedrockruntime. Converse is analyzable; InvokeModel Body is an opaque []byte JSON blob.
  - id: bedrock.converse
    import_path: github.com/aws/aws-sdk-go-v2/service/bedrockruntime
    symbol_kind: client_method
    selector: Converse
    provider_hint: bedrock
    arg_map:
      model:  { field: "input.ModelId", unwrap: ["aws.String"] }
      prompt: { field: "input.Messages" }
      tools:  { field: "input.ToolConfig" }
  - id: bedrock.conversestream
    import_path: github.com/aws/aws-sdk-go-v2/service/bedrockruntime
    symbol_kind: client_method
    selector: ConverseStream
    streaming: true
    provider_hint: bedrock
    arg_map: { model: {field: "input.ModelId", unwrap: ["aws.String"]}, prompt: {field: "input.Messages"}, tools: {field: "input.ToolConfig"} }
  - id: bedrock.invokemodel
    import_path: github.com/aws/aws-sdk-go-v2/service/bedrockruntime
    symbol_kind: client_method
    selector: InvokeModel
    provider_hint: bedrock
    opacity: ["prompt", "model.params"]   # Body []byte is opaque => extractor emits sentinel + P5 flag (I5)
    arg_map:
      model:  { field: "input.ModelId", unwrap: ["aws.String"] }   # ModelId is still readable even when Body isn't
      prompt: { form: opaque }
```

## §4 Invariant & scope ties

- **I6 (additive-only IR):** the registry populates only frozen node fields; `provider_hint`, `Opacity`,
  row `id` are Discovery-internal and travel in the **run report** (§2.6), never on the IR node.
- **I5 (honest `unresolved`):** `Opacity` and `ConstructionBound`-over-budget both route to the sentinel +
  flag path, never a guess.
- **Streaming = separate rows, same node semantics:** a streaming call is still one node; the row split is
  only about matching two different symbols.
- **Version drift (advisory in P1):** `VersionRange` is recorded but not enforced in P1; SDK API drift
  that moves a field is handled by editing the row (data), and surfaced when extraction starts marking a
  previously-resolved field `unresolved` (visible in the report's detections-by-source deltas).

## §5 Consumed by

Implementation §3.3 (registry detector), §4.1 (extractor reads `ArgMap`/`Opacity`), and the multi-source
merge §3.5 (rows dedup against declared entrypoints by call-site identity → the node-ID, [doc 06](06-node-id-scheme.md)).
The `llm-eval.yaml` arg-map ([doc 05](05-llm-eval-yaml-schema.md)) deliberately reuses the **same
`ArgLocator` forms** so declared entrypoints and registry rows are one code path.
