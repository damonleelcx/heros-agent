# kotlin fixture — golden (tree-sitter frontend #7)

**2 nodes** (workflow.language = kotlin), both matching registry row `kt.langchain4j.generate`
(import_path `dev.langchain4j`, selector `generate`):

1. `Agent.classify` — `model.generate(text)`, **single**.
2. `Agent.batch`    — `model.generate(item)` inside a `for` → **loop**, `variable_at_runtime=true` (I2:
   no fixed runtime count is ever emitted for a loop).

**Model is `unresolved` + flagged on both.** This is correct, not a gap: langchain4j binds the model at
`OpenAiChatModel.builder()...build()` construction, so the model ID is not present at the call site and
the syntactic floor never guesses it (I5 / 10.5). Same reason the java fixture is unresolved.

**Prompts are unresolved + flagged**: `generate(text)` takes a positional variable, not a string literal.

This fixture is the regression guard for the thing 10.8 claimed but did not have: before frontend #7,
this exact file produced **0 nodes** plus `LANGUAGE_UNSUPPORTED: kotlin source is present but no frontend
for it is registered`. Kotlin now has a frontend AND kotlin-tagged registry rows — both are required,
since rows are language-tagged and `ForLanguage("kotlin")` matched nothing before.

The committed golden IR is expected-ir.json. Regenerate with UPDATE_GOLDEN=1.

## Fixture-kind coverage for kotlin

| kind | where | notes |
|---|---|---|
| golden | **this fixture** | + expected-ir.json byte-diff in CI |
| loop | **this fixture** (`batch`) | `for (item in items)` → variable_at_runtime; no separate dir needed |
| wrapper | `kotlin_wrapper` | declared top-level fn; NAMED-arg prompt resolves at the floor |
| malformed | `kotlin_malformed` | tree-sitter recovers → warn-severity PARSE_ERROR |
| dedup | `kotlin_dedup` | registry + declared on one call site |
| framework-DAG | **N/A — documented** | see below |

## 🔴 framework-DAG: N/A for kotlin, and why

There is **no declarative agent-graph framework registered for kotlin** in `frameworkReadersByLanguage`
(internal/discovery/framework.go), so a framework fixture would assert nothing.

This is an honest N/A for the same reason as java: Kotlin's JVM LLM frameworks (langchain4j, Spring AI)
bind chains imperatively, not as a statically-readable declarative node/edge graph. Kotlin adds no
JVM-specific graph DSL that changes this. If one lands, the fix is a row in `frameworkReadersByLanguage`
plus a `kotlin_framework` fixture — the reader contract is already language-neutral.
