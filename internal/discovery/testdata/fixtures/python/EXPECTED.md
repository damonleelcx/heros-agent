# python fixture — expected (tree-sitter frontend)

**2 nodes** (workflow.language = python):
1. classify — anthropic messages.create, model="claude-sonnet-4-5" (keyword string literal), single.
2. agent    — openai chat.completions.create, model="gpt-4o", **loop** (inside `for`) → variable_at_runtime.

Prompts (messages=[...]) are unresolved + flagged (syntactic floor, no type resolution — 10.5).
