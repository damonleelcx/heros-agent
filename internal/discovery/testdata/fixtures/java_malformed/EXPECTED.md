# java_malformed fixture — a broken file must be reported, never silently half-read

**1 node** (from `Good.java`) + a **PARSE_ERROR diagnostic naming `Bad.java`**.

`summary.files_skipped` is **0** — tree-sitter recovers rather than skipping. See kotlin_malformed's
EXPECTED.md for why the severity honestly differs from the Go `malformed` fixture.
