# python fixture — expected (tree-sitter frontend)

**2 nodes** (workflow.language = python):
1. classify — anthropic messages.create, model="claude-sonnet-4-5" (keyword string literal), single.
2. agent    — openai chat.completions.create, model="gpt-4o", **loop** (inside `for`) → variable_at_runtime.

Prompts (messages=[...]) are unresolved + flagged (syntactic floor, no type resolution — 10.5).

## Fixture-kind coverage for python

| kind | where | notes |
|---|---|---|
| golden | **this fixture** | + expected-ir.json byte-diff in CI |
| loop | **this fixture** (`agent`) | `for it in items` → variable_at_runtime; no separate dir needed |
| wrapper | `python_wrapper` | was previously an IN-TEST fixture only (frontend_python_test.go), so the CLI path never exercised it |
| framework-DAG | `python_framework` (LangGraph) + `python_crewai` (CrewAI) | the only language with two framework readers |
| malformed | `python_malformed` | tree-sitter recovers → warn-severity PARSE_ERROR |
| dedup | `python_dedup` | registry + declared on one call site |
