# P1 Discovery — Go LLM call-shape & wrapper-pattern catalog

> **Task:** P1 `tasks.md` §1.2 — *Survey real Go LLM repos; catalog concrete call shapes (direct SDK
> calls, options structs, interface-typed clients, in-house `Complete()`/`Generate()` wrappers).
> Record the wrapper patterns that defeat signature matching.*
> **Workstream:** ① Understand & Explore (Backend lead, **AI Eng owns the fidelity judgment**).
> **Feeds:** the signature-registry design (§2.1), the `llm-eval.yaml` arg-map design (§2.2), the
> ambiguity-flag / `unresolved` rules (§4.1–§4.2), and the wrapper/dedup test fixtures (§6.2, §6.6).

This is the empirical grounding for the whole reason **user-declared entrypoints are mandatory**: in
real Go code the SDK is almost never called at the leaf. The catalog below is confirmed against the
**current** public APIs of the five libraries that cover the large majority of Go LLM code, then
distilled into the wrapper patterns the detector must expect.

> **Method note (honesty):** call shapes are verified against each library's current documented API
> (see Sources). This is a *shape* catalog, not a census of specific proprietary repos — line-level
> "here is repo X at line N" evidence is intentionally omitted rather than fabricated. The wrapper
> patterns in §3 are the generalizable, decision-driving output.

---

## 1. The five call shapes that cover most Go LLM code

Each entry gives the **leaf SDK call** (what a signature registry could match) and the **detection
hazard** it creates. The recurring structural fact: **Go LLM SDKs pass model/prompt/tools as fields
of one options struct or as functional options — almost never as separate positional arguments.** A
registry keyed only on `pkg.Func(posArg0, posArg1)` will mis-map every one of these.

### 1.1 Anthropic — `anthropic-sdk-go` (official)
```go
client := anthropic.NewClient(option.WithAPIKey(key))
msg, err := client.Messages.New(ctx, anthropic.MessageNewParams{
    Model:     anthropic.ModelClaudeOpus4_6,          // <- field, not positional
    MaxTokens: 1024,
    Messages:  []anthropic.MessageParam{ anthropic.NewUserMessage(anthropic.NewTextBlock(q)) },
    System:    []anthropic.TextBlockParam{ ... },
    Tools:     []anthropic.ToolUnionParam{ ... },
})
// streaming: client.Messages.NewStreaming(ctx, params)
```
- **Leaf symbol:** method `New` (and `NewStreaming`) on the `Messages` service value hung off
  `*anthropic.Client`. The registry entry is a **method on a nested service**, not a package function.
- **Args:** a single **options struct** `MessageNewParams`; model/messages/tools/system are **struct
  fields**. (Current v1 drops the old `anthropic.F(...)` field-wrapper — older code in the wild still
  uses `anthropic.F(...)`, so both spellings appear.)
- **Hazard:** arg map must address **struct field names** (`Model`, `Messages`, `Tools`), not indices.

### 1.2 OpenAI — `openai-go` (official)
```go
completion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
    Model:    openai.ChatModelGPT4o,                    // <- field
    Messages: []openai.ChatCompletionMessageParamUnion{ openai.UserMessage(q) },
    Tools:    []openai.ChatCompletionToolUnionParam{ ... },
    Seed:     openai.Int(0),
})
// streaming: client.Chat.Completions.NewStreaming(ctx, params)
```
- **Leaf symbol:** method `New`/`NewStreaming` on the **doubly-nested** `Chat.Completions` service.
- **Args:** options struct `ChatCompletionNewParams`; same field-addressed shape as Anthropic.
- **Hazard:** the nested-service receiver path (`client.Chat.Completions.New`) must be resolvable to
  the registry entry, not just the bare method name `New` (which is generic and appears everywhere).

### 1.3 OpenAI — `sashabaranov/go-openai` (community; the most common in the wild)
```go
resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
    Model:    openai.GPT4o,                              // <- field
    Messages: []openai.ChatCompletionMessage{ {Role: openai.ChatMessageRoleUser, Content: q} },
    Tools:    []openai.Tool{ ... },
})
// streaming: client.CreateChatCompletionStream(ctx, req)
```
- **Leaf symbol:** method `CreateChatCompletion` (and `CreateChatCompletionStream`) on `*openai.Client`.
- **Args:** a **request object** `ChatCompletionRequest` — options struct by another name.
- **Hazard:** two libraries share the package name `openai` (this one vs. the official). The registry
  must key on the **import path** (`github.com/sashabaranov/go-openai` vs `github.com/openai/openai-go`),
  never the local package identifier — they collide.

### 1.4 Framework — `langchaingo` (interface-typed client)
```go
llm, err := openai.New(openai.WithModel("gpt-4o"))      // model bound at CONSTRUCTION
// ...
resp, err := llm.GenerateContent(ctx, messages, llms.WithModel("gpt-4o"), llms.WithTemperature(0.2))
// or the one-shot helper:
out, err := llms.GenerateFromSinglePrompt(ctx, llm, prompt, llms.WithModel("gpt-4o"))
```
- **Leaf symbol:** method `GenerateContent` on the **interface** `llms.Model` (also legacy `Call`).
  The concrete provider (`openai`, `anthropic`, `ollama`, `bedrock`, …) sits behind the interface.
- **Args:** `messages []llms.MessageContent` + **variadic functional options** (`llms.CallOption`):
  `WithModel`, `WithTemperature`, `WithTools`, …
- **Hazards (multiple, severe):**
  - **Interface indirection** — the static type at the call site is `llms.Model`; the concrete impl is
    chosen by construction/DI. Signature matching resolves the interface method, but not necessarily
    the provider.
  - **Model bound at construction** — `openai.New(openai.WithModel(...))` sets the model on the client;
    the call site may pass **no** model at all. Worse, langchaingo `WithModel` docs: *"If not set, the
    model is read from an environment variable."* → the model can be **absent from source entirely**.
    This is a first-class `unresolved` case (report Finding B in §1.1 doc): the model binding is not at
    the call site and may not be in the repo.
  - **Functional options** — args are option *functions*, not named fields; the arg-map scheme must
    support an **option-constructor mapping** (`llms.WithModel(x)` → `model = x`), not just index/name.

### 1.5 AWS Bedrock — `aws-sdk-go-v2/service/bedrockruntime`
```go
out, err := client.Converse(ctx, &bedrockruntime.ConverseInput{
    ModelId:  aws.String("anthropic.claude-3-5-sonnet-20241022-v2:0"),  // <- field, pointer-wrapped
    Messages: []types.Message{ {Role: "user", Content: []types.ContentBlock{ &types.ContentBlockMemberText{Value: prompt} }} },
})
// also: client.ConverseStream(ctx, in)
// and the opaque one: client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
//     ModelId: aws.String(id), Body: rawJSONBytes,   // <- prompt & params buried in a []byte JSON blob
// })
```
- **Leaf symbol:** methods `Converse` / `ConverseStream` / `InvokeModel` on `*bedrockruntime.Client`.
- **Args:** an `*Input` **pointer-to-options-struct**; `ModelId` is `*string` (`aws.String(...)`).
- **Hazards:**
  - **Pointer + `aws.String` wrapping** — the model arg is `aws.String(x)`, so the resolver must see
    through the `aws.String` call to the underlying literal/variable.
  - **`InvokeModel` is opaque** — model params and the prompt live inside `Body []byte`, a
    provider-specific JSON blob built elsewhere. Statically this is **unresolvable** → mark
    `unresolved` + flag as a P5 dynamic-trace candidate. `Converse` is far more analyzable than
    `InvokeModel`; the registry should record that distinction.

---

## 2. The structural axes (why one flat signature table is not enough)

Distilling §1, every call shape varies along these axes — the registry data model (§2.1) must encode
all of them:

| Axis | Values seen | Consequence for detection |
|---|---|---|
| **Symbol kind** | package func · method on client · method on **nested service** (`Chat.Completions.New`) · **interface** method | Registry key = import-path-qualified symbol *with receiver path*, resolved via type info — not a bare name. |
| **Arg passing** | options **struct** fields · **pointer**-to-struct · **variadic functional options** · positional | Arg-map must address *field names*, *option constructors*, and indices — three modes, not one. |
| **Model binding site** | call-site field · **construction** (`New(WithModel(...))`) · **env var** (absent from source) | Model is often **not at the call site**; resolver must trace the receiver's construction, and accept that env-bound models are `unresolved`. |
| **Prompt assembly** | inline literal · `fmt.Sprintf` · `strings.Builder` · `text/template` · struct-literal message slice built conditionally · JSON `[]byte` body | Only literals/local const chains resolve statically; the rest → `unresolved` + flag. |
| **Package-name collisions** | two `openai` packages; many `anthropic`/`bedrock` forks | Key on **import path**, never local identifier. |
| **Sync vs. stream** | `New` vs `NewStreaming`; `Create*` vs `Create*Stream`; `Converse` vs `ConverseStream` | Each streaming variant is its **own registry row** (same node semantics, different symbol). |

---

## 3. Wrapper patterns that defeat signature matching (the core deliverable)

These are the patterns that make a signature registry **under-count nodes**, and therefore the
concrete justification for mandatory `llm-eval.yaml` user-declared entrypoints (D1). Each is written
as *what the detector sees* → *why signature matching fails* → *what closes the gap*.

| # | Wrapper pattern | Example leaf | Why signature matching misses it | What closes the gap |
|---|---|---|---|---|
| **W1** | **In-house method wrapper** | `svc.Summarize(ctx, doc)` → internally calls `client.Messages.New` | The call site contains **no SDK symbol at all**; the SDK call is one or more frames down. | `llm-eval.yaml` declares `internal/llm.(*Service).Summarize` as an entrypoint (FR2). |
| **W2** | **Free-function wrapper** | `llm.Complete(ctx, prompt)` / `llm.Generate(ctx, req)` | Same as W1 but package-level; the canonical `Complete()`/`Generate()` helper the PRD names. | Declared entrypoint; arg-map maps `prompt`/`req.Model` to node fields. |
| **W3** | **Interface indirection** | `var m LLMClient; m.Complete(...)` where `LLMClient` is an **in-house** interface | Static type is the interface; the concrete impl (which calls the SDK) is chosen by DI/runtime. | Declare the **interface method** as the entrypoint; the node is the interface call site. Concrete-impl resolution beyond that is `unresolved` → P5. |
| **W4** | **Model bound at construction** | `c := NewLLM(WithModel("gpt-4o")); c.Ask(prompt)` | The model is captured in the receiver at construction; the call site passes **no model**. | Arg-map points `model` at the **constructor** option, or (if unresolvable intra-procedurally) mark `model` `unresolved` + flag. |
| **W5** | **Functional-option args** | `Complete(ctx, prompt, WithModel(m), WithTemp(t))` | Args are variadic option *funcs*, not named/positional fields the arg-map can index. | Arg-map supports an **option-constructor → field** mapping (also needed for langchaingo, §1.4). |
| **W6** | **Options/request struct** | `Do(ctx, Req{Model:…, Messages:…})` | Model/messages are fields of **one struct arg**; positional index 0 is "the whole request." | Arg-map addresses **struct field paths** (`req.Model`), the default shape for all five SDKs. |
| **W7** | **Prompt built inter-procedurally** | `p := buildPrompt(x); c.Complete(ctx, p)` | Prompt value is assembled in another function / via `template`/`Builder`; not a literal at the call site. | Intra-procedural resolution up to a bounded budget (design Q3); beyond it → `prompt` `unresolved` + flag (FR8). |
| **W8** | **Conditionally-built message slice** | `msgs := base; if x { msgs = append(msgs, extra) }` | The message list is not statically fixed; different branches produce different prompts. | Emit what is statically known; flag the branch-dependent part `unresolved` (P5 dynamic-trace candidate). |
| **W9** | **Generic gateway / one call, many logical nodes** | `gw.Do(ctx, req)` used everywhere; `req.Model` chosen by a router `switch` | **One physical call site** backs **many** logical model bindings selected at runtime. | Static analysis emits **one node** for the call site + `unresolved` model + flag; P5 dynamic tracing splits the logical bindings. Do **not** guess-expand into N nodes. |
| **W10** | **Closure / goroutine dispatch** | call inside a closure passed to `errgroup.Go(func(){ c.Complete(...) })` | The enclosing `symbol` for `call_site` is the closure, not the visible outer function. | Resolve `call_site.symbol` to the closure's **named parent** func so the node is addressable and stable. |
| **W11** | **`aws.String` / pointer wrapping** | `ModelId: aws.String(id)` | The model arg is wrapped in a helper call, not a bare literal. | Resolver sees through known value-wrappers (`aws.String`, `openai.F`, `anthropic.F`, `param.Opt`) to the inner value. |
| **W12** | **Opaque serialized body** | Bedrock `InvokeModel{Body: json.Marshal(payload)}` | Model params + prompt are inside a `[]byte` JSON blob built elsewhere. | Mark `prompt`/`params` `unresolved` + flag; record that `InvokeModel` is inherently opaque vs `Converse`. |

**Bottom line for §2 (Design):** the signature registry alone provably under-counts on W1–W3 and
mis-maps on W4–W6, W11. The three co-equal detection sources (registry, declared entrypoints, framework
readers) plus a resolver that sees through value-wrappers and does bounded intra-procedural resolution
are the minimum to cover this catalog. Everything past the bounded budget is **honest `unresolved` +
P5 flag**, never a guess.

---

## 4. Direct implications for the P1 design tasks

- **§2.1 Signature registry** must key on **import-path-qualified symbol with receiver path**, encode
  **struct-field / functional-option / positional** arg-maps, list **sync + streaming** rows per SDK,
  and carry a per-row note for opaque cases (`InvokeModel`). Seed rows: `anthropic-sdk-go`
  `Messages.New`/`NewStreaming`; `openai-go` `Chat.Completions.New`/`NewStreaming`;
  `sashabaranov/go-openai` `CreateChatCompletion`/`Stream`; `langchaingo` `llms.Model.GenerateContent`
  (+ `GenerateFromSinglePrompt`); `bedrockruntime` `Converse`/`ConverseStream`/`InvokeModel`.
- **§2.2 `llm-eval.yaml` arg-map** must express **field-path**, **positional-index**, **param-name**,
  and **option-constructor** mappings (W4–W6) — the single-mode "positional index" sketch in the PRD
  is insufficient; that resolves PRD Q1 toward a richer arg-map.
- **§4.1 metadata extractor** needs a **value-wrapper unwrap** step (W11) and a **bounded
  intra-procedural resolver** (W7) with the budget from PRD Q3.
- **§4.2 ambiguity flags** own W3, W7, W8, W9, W12 — all the `unresolved` producers — and must attach a
  machine-readable reason so P5 knows *why* it is a dynamic-trace candidate.
- **Fixtures:** W1/W2 → the wrapper fixture (§6.2); W9 → a multi-binding-one-call-site case; W4/W6 →
  arg-map coverage; framework interface (langchaingo, §1.4) → the framework-DAG fixture (§6.3).

---

## Sources

- [anthropics/anthropic-sdk-go — README & message API](https://github.com/anthropics/anthropic-sdk-go/blob/main/README.md)
- [tmc/langchaingo — `llms` package (`GenerateContent`, `WithModel`)](https://pkg.go.dev/github.com/tmc/langchaingo/llms)
- [aws/aws-sdk-go-v2 — `bedrockruntime` (`Converse`, `InvokeModel`)](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/bedrockruntime)
- [sashabaranov/go-openai — `CreateChatCompletion` / `ChatCompletionRequest`](https://pkg.go.dev/github.com/sashabaranov/go-openai)
- [openai/openai-go — `Chat.Completions.New` / `ChatCompletionNewParams`](https://github.com/openai/openai-go/blob/main/examples/chat-completion-tool-calling/main.go)
