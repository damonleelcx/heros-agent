# kotlin_malformed fixture — a broken file must be reported, never silently half-read

**1 node** (from `Good.kt`) + a **PARSE_ERROR diagnostic naming `app/Bad.kt`**.

`Good.kt`'s node is still discovered: one unparsable file must not lose the rest of the repo.

## Why this fixture exists (and why its severity differs from the Go `malformed` fixture)

🔴 **tree-sitter never fails a parse.** Unlike `go/parser`, it is error-tolerant: it recovers and returns a
tree containing ERROR/MISSING nodes. So before `syntaxErrorDiagnostics` existed, a malformed .kt/.py/.ts/.rs/
.java file was **silently PARTIALLY analyzed** — the call sites inside the broken region simply never
appeared and the run report was clean. That is the "looks normal, is wrong" failure shape the report exists
to prevent: "why is this node missing?" must always be answerable (I4).

**`summary.files_skipped` is 0 here, and that is deliberate — not a miscount.** The file was NOT skipped:
tree-sitter recovered it and any nodes outside the broken region ARE emitted. `files_skipped` is derived
from error-severity diagnostics, so this diagnostic is **warn**-severity to avoid claiming a skip that did
not happen. The Go `malformed` fixture's PARSE_ERROR **is** error-severity and DOES increment
`files_skipped`, because go/parser yields no AST and that file genuinely is skipped.

Same diagnostic code, honestly different severity, because the two situations genuinely differ. Averaging
them into one number would make both lie.
