# mixed-language fixture — expected (§10.12)

**3 nodes across 3 frontends in ONE IR** (workflow.language = mixed):
- main.go        — anthropic Messages.New (Go frontend, go/ast)
- svc/triage.py  — anthropic messages.create (Python frontend, tree-sitter)
- svc/web.ts     — openai chat.completions.create (TypeScript frontend, tree-sitter)
