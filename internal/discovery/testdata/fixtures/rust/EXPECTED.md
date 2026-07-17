# rust fixture — golden (tree-sitter frontend #5)

**2 nodes** (workflow.language = rust), both matching registry row `rs.async_openai.create`
(crate `async_openai`, selector `chat.create`):
1. `ask` — `client.chat().create(req).await`, **single**.
2. `ask` — `client.chat().create(req2).await` inside `for _ in 0..3` → **loop**, variable_at_runtime=true.

Both are in the same enclosing symbol, so node identity separates them by occurrence index.

**Model is `unresolved` + flagged on both.** Correct, not a gap: async-openai binds the model inside a
request STRUCT (`req`) built elsewhere, so it is not present at the call site and the floor never guesses it
(I5). Rust also has no keyword arguments at all, so nothing at a Rust call site is keyword-resolvable.

The committed golden IR is expected-ir.json. Regenerate with UPDATE_GOLDEN=1.

## Fixture-kind coverage for rust

| kind | where | notes |
|---|---|---|
| golden | **this fixture** | + expected-ir.json byte-diff in CI |
| loop | **this fixture** (`for _ in 0..3`) | variable_at_runtime; no separate dir needed |
| wrapper | `rust_wrapper` | declared free fn; prompt unresolved (Rust has no named args) |
| malformed | `rust_malformed` | tree-sitter recovers → warn-severity PARSE_ERROR |
| dedup | `rust_dedup` | registry + declared on one call site |
| framework-DAG | **N/A — documented** | see below |

## 🔴 framework-DAG: N/A for rust, and why

There is **no declarative agent-graph framework registered for rust** in `frameworkReadersByLanguage`
(internal/discovery/framework.go), so there is nothing to read and a framework fixture would assert nothing.

This is an honest N/A, not an oversight: the Rust LLM ecosystem (async-openai, anthropic crates) is
request/response SDKs, not declarative graph builders — there is no Rust LangGraph whose `add_node`/`add_edge`
topology could be read statically. If one appears, the fix is a row in `frameworkReadersByLanguage` plus a
`rust_framework` fixture; the reader contract is already language-neutral and needs no change.
