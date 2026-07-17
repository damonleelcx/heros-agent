# java fixture — golden (tree-sitter frontend #6)

**1 node** (workflow.language = java): `Svc.run` — `model.generate("summarize this")`, matching registry row
`java.langchain4j.generate` (import_path `dev.langchain4j`, selector `generate`), **single**.

**Model is `unresolved` + flagged.** Correct, not a gap: langchain4j binds the model at
`OpenAiChatModel.builder()…build()` construction, so it is not present at the call site and the floor never
guesses it (I5).

**Prompt is unresolved + flagged**: `generate("…")` is POSITIONAL, and the syntactic floor resolves only
`LocParamName` (keyword/named) locators. Java has no named arguments at the call site, so no declaration can
resolve a Java prompt at the floor — unlike Kotlin, which does have them (see kotlin_wrapper). That is a real
language difference, documented rather than papered over.

The committed golden IR is expected-ir.json. Regenerate with UPDATE_GOLDEN=1.

## Fixture-kind coverage for java

| kind | where | notes |
|---|---|---|
| golden | **this fixture** | + expected-ir.json byte-diff in CI |
| loop | `java_loop` | enhanced-for → variable_at_runtime (I2) |
| wrapper | `java_wrapper` | declared METHOD entrypoint (Java has no top-level functions) |
| malformed | `java_malformed` | tree-sitter recovers → warn-severity PARSE_ERROR |
| dedup | `java_dedup` | registry + declared on one call site |
| framework-DAG | **N/A — documented** | see below |

## 🔴 framework-DAG: N/A for java, and why

There is **no declarative agent-graph framework registered for java** in `frameworkReadersByLanguage`
(internal/discovery/framework.go), so a framework fixture would assert nothing.

This is an honest N/A. The JVM LLM frameworks this registry knows — langchain4j and Spring AI — bind chains
imperatively (builders and `AiServices` proxies), not as a declarative node/edge graph that can be read
statically the way LangGraph's `add_node`/`add_edge` can. langchain4j's newer agentic APIs are worth
re-checking later; if a readable declarative graph lands, the fix is a row in `frameworkReadersByLanguage`
plus a `java_framework` fixture — the reader contract is already language-neutral.
