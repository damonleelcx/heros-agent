# typescript_malformed fixture — a broken file must be reported, never silently half-read

**1 node** (from `good.ts`, model resolves to "claude-sonnet-4-5") + a **PARSE_ERROR diagnostic naming
`src/bad.ts`**.

`summary.files_skipped` is **0** — tree-sitter recovers rather than skipping. See kotlin_malformed's
EXPECTED.md for why the severity honestly differs from the Go `malformed` fixture.
