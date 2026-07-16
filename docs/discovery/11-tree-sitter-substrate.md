# P1 Discovery — Design & delivery: tree-sitter substrate + resolution floor (§10.4–10.5, PRD Q7)

> **Tasks:** P1 `tasks.md` §10.4 (tree-sitter integration + normalized shape + no-execution) and §10.5
> (the type-free resolution floor / PRD Q7). **Phase:** ② Design → ③ Implement (Backend lead, AI Eng).
> **Builds on:** the [language-neutral seam (§10.A)](README.md), design [D0/D2](../../openspec/changes/p1-discovery-mvp/design.md),
> invariants [I1/I5](03-discovery-invariants.md).

## §0 TL;DR

Every non-Go language is parsed by **tree-sitter** — a pure, type-free parser — behind a
`LanguageAnalyzer` that yields the **normalized call-site shape** the shared core already consumes. The
Go frontend (`go/ast`, with real import resolution) and the tree-sitter frontends produce the *same*
`RawCallSite` shape, so detection, node-ID, merge, emission, and the run report are identical across
languages. Because tree-sitter has **no type resolution**, non-Go detection rests on **import-presence +
selector-chain + declared entrypoints + keyword string literals** — the *resolution floor* — and marks
everything else `unresolved` with a flagged reason (I5), **never guessed**. Python is the first shipped
tree-sitter frontend.

## §1 The substrate (§10.4)

```
LanguageAnalyzer (per language, tree-sitter)          SyntacticFrontend (shared)
  Analyze(file) -> SyntacticUnit{                        Discover(repo):
     Imports, ImportPaths,                                 walk files it Handles (read-only, never run)
     CallSites []RawCallSite{                              analyze each -> unit
        Root, Chain, EnclosingSymbol,                      detectSyntacticUnit(unit):
        LineStart/End, Invocation, KeywordStrings }          matchRegistryRows / matchDeclaredEntries  ← SHARED with Go
  }                                                          Merge (dedup by node_id)                  ← SHARED
                                                             extractSyntacticFloor                     ← the 10.5 floor
```

- **`RawCallSite`** is the language-neutral shape: the call target's `Root` + selector `Chain`, the
  `EnclosingSymbol`, the source span, an invocation hint (loop/conditional from surrounding control
  flow), and any keyword string-literal args. The Go frontend produces the same fields from `go/ast`;
  the two paths converge here.
- **`SyntacticFrontend`** adapts any `LanguageAnalyzer` to `LanguageFrontend`, reusing the exact shared
  matchers (`matchRegistryRows`/`matchDeclaredEntries`), node-ID scheme (doc 06), `Merge`, and the IR
  emitter. Adding a language = writing an analyzer + registry rows + fixtures; **the core is untouched**.
- **No-execution (I1) extends to tree-sitter for free:** tree-sitter is a parser, not an interpreter —
  it never runs source. `TestPythonNoExecution` proves a side-effectful Python top-level statement never
  fires during discovery, the Python analogue of the Go `init()` guard.

### 1.1 Decision — cgo tree-sitter bindings
The Go binding (`github.com/smacker/go-tree-sitter` + per-language grammar packages) links the
tree-sitter C runtime via **cgo**. **Consequence (surfaced honestly):** the discovery build now requires
a C toolchain (`CGO_ENABLED=1`); CI's `ubuntu-latest` has gcc, so the existing jobs are unaffected, but
`CGO_ENABLED=0` / exotic cross-compiles of the discovery packages will not build. **Alternatives
considered:** WASM grammars via wazero (no cgo, but adds a WASM runtime + bundled `.wasm` assets) and a
pure-Go bootstrap parser (no real tree-sitter). cgo was chosen for a real, complete tree-sitter
integration now; WASM remains a drop-in behind the same `LanguageAnalyzer` if the cgo cost becomes a
problem.

## §2 The resolution floor (§10.5 — resolves PRD Q7)

PRD Q7: *without type info, how are module/import + selector resolved to a registry row, and what is the
honest floor before `unresolved`?* The answer, per field:

| Field | Resolvable at the syntactic floor? | Rule |
|---|---|---|
| **Detection** (is this an LLM call?) | ✅ mostly | file imports the SDK module (`ImportPaths`) **and** the call's selector chain suffix-matches a registry row (e.g. `client.messages.create` → `messages.create`). Package-qualified roots resolve via the import map (higher confidence); method calls use import-presence (`BasisSelectorImport`). |
| **`model`** | ✅ when a keyword string literal | `model="claude-sonnet-4-5"` resolves; `model=chosen_var` does **not** → `unresolved` + `MODEL_UNRESOLVED`. Provider comes from the registry row's `provider_hint` (or `unresolved`). |
| **`prompt`** | rarely | a string-literal keyword resolves inline; the common `messages=[…]` list / a variable does **not** → `unresolved` + `PROMPT_UNRESOLVED`. |
| **`tools`** | ✗ at the floor | emitted `[]`; refinement is a future per-language uplift. |
| **`invocation_semantics`** | ✅ | loop/conditional inferred from the surrounding `for`/`while`/`if` in the parse tree — a real, type-free signal (an agent loop is correctly `variable_at_runtime`). |
| **edges** | ✗ at the floor | intra-file data-flow needs types; **honestly omitted**, never guessed. |

**The floor is honest, not lossy-by-accident.** Every unresolved field carries a P5 ambiguity flag with
a reason naming the syntactic limitation — exactly the input P5 dynamic tracing consumes. A confident-
but-wrong value would be worse (I5/NFR4). Per-language **deep type resolution** (LSP / compiler
frontends) is the documented **post-M1 fidelity uplift**, not an M1 gate.

### 2.1 Fidelity is recorded, not hidden
Each node's match basis (`BasisPackageQualified` vs `BasisSelectorImport`) and its `unresolved_fields`
travel in the run report (doc 09), so a consumer can see *how confident* a tree-sitter node is versus a
Go node — the AI-Eng fidelity judgment is auditable, not implicit.

## §3 Shipped frontends (§10.C)

Each frontend is **one `LanguageAnalyzer` + registry rows + a fixture** behind this substrate; the core
is untouched. All are proven end-to-end (real source → schema-valid IR, correct providers/models where
resolvable, loop nodes flagged `variable_at_runtime`, honest `unresolved` elsewhere):

| Frontend | Grammar / parser | SDK registry rows | Notes |
|---|---|---|---|
| **Go** (§3–§7) | `go/ast` (typed) | anthropic-sdk-go, openai-go, sashabaranov, langchaingo, bedrock | frontend #1; real import resolution |
| **Python** (10.6) | tree-sitter `python` | anthropic, openai, langchain(_openai/_anthropic), langgraph, crewai, boto3/bedrock | keyword-string model resolution; **LangGraph framework reader** |
| **TypeScript** (10.7) | tree-sitter `typescript` | @anthropic-ai/sdk, openai, Vercel AI SDK (`generateText`/`streamText`), @langchain/core | object-literal `{model, prompt}` args |
| **JavaScript** (10.7) | tree-sitter `javascript` | same as TS (rows duplicated per language tag) | incl. `require()` imports |
| **Rust** (10.9) | tree-sitter `rust` | async-openai, anthropic crates | `use`-crate presence + method selector; model builder-bound → unresolved |
| **Java** (10.8) | tree-sitter `java` | langchain4j, Spring AI, Bedrock | package import-presence; model builder-bound → unresolved |

The **`syntacticFrameworks`** reader derives LangGraph/CrewAI declarative graphs (`add_node`/`add_edge`/
`add_conditional_edges`/`set_entry_point`, snake- and camelCase) from any syntactic frontend, mapping
routing-map **values** to control edges (labels are not targets).

## §4 Mixed-language & further languages (§10.10)

A repository may mix languages: each frontend handles its own extensions and contributes to **one** IR
(`workflow.language = "mixed"`), proven by the mixed-language test. **Further languages** (Ruby, C#, C++,
Kotlin — grammars already available in the binding) are each just another `LanguageAnalyzer` + rows +
fixture; until one ships, its source is reported via a `LANGUAGE_UNSUPPORTED` diagnostic (never silently
dropped, I4). CI (`discovery-ci`) validates every language fixture's emitted IR against the frozen schema.
