# typescript fixture — golden (tree-sitter frontend #3)

**3 nodes** (workflow.language = typescript):
1. `classify`   — `anthropic.messages.create`, model="claude-sonnet-4-5" (options-object string literal), **single**.
2. `loopAgent`  — `openai.chat.completions.create`, model="gpt-4o", **loop** (inside `for…of`) → variable_at_runtime.
3. `vercel`     — Vercel AI SDK `generateText({...})`, a bare imported package function; prompt="summarize".

TS/JS SDKs pass an options OBJECT rather than keyword args; the frontend lifts object keys whose values are
string literals into the keyword map, which is why `model` resolves here but `messages: [...]` does not.

Prompts assembled from a `messages` array are unresolved + flagged (syntactic floor, no type resolution — 10.5).

The committed golden IR is expected-ir.json. Regenerate with UPDATE_GOLDEN=1.

## Fixture-kind coverage for typescript

| kind | where | notes |
|---|---|---|
| golden | **this fixture** | + expected-ir.json byte-diff in CI |
| loop | **this fixture** (`loopAgent`) | a `for…of` call site → variable_at_runtime; no separate dir needed |
| wrapper | `typescript_wrapper` | declared entrypoint, options-object prompt resolves |
| framework-DAG | `typescript_framework` | LangGraph.js (camelCase builder API) |
| malformed | `typescript_malformed` | tree-sitter recovers → warn-severity PARSE_ERROR |
| dedup | `typescript_dedup` | registry + declared on one call site |
