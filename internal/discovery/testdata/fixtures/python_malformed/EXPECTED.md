# python_malformed fixture — a broken file must be reported, never silently half-read

**1 node** (from `good.py`, model resolves to "claude-sonnet-4-5") + a **PARSE_ERROR diagnostic naming
`app/bad.py`**.

`summary.files_skipped` is **0**: tree-sitter is error-tolerant and RECOVERS rather than failing, so the
file was not skipped. The diagnostic is warn-severity for exactly that reason. See kotlin_malformed's
EXPECTED.md for the full rationale on why this differs from the Go `malformed` fixture (error-severity,
files_skipped >= 1) — go/parser yields no AST, tree-sitter yields a partial one.

Without the `syntaxErrorDiagnostics` check this fixture would emit 1 node and a **completely clean report**,
which is the silent-failure shape the report must never have.
