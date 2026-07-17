# rust_wrapper fixture — user-declared entrypoint (FR2)

**1 node WITH llm-eval.yaml, 0 nodes WITHOUT it.** `myco_llm::complete` matches no registry row, so the
in-house wrapper is invisible until the user declares it — proving the declared-entrypoint mechanism
reaches Rust.

**prompt is unresolved + flagged.** Rust has no keyword/named arguments at all, so `complete("…")` is a
positional string literal, and `extractSyntacticFloor` resolves `LocParamName` locators only. Declaring
`{ index: 0 }` would not help either — the floor never resolves positional locators, because tying an
argument position to a parameter name requires signature resolution that tree-sitter frontends do not do.

This is the honest floor for Rust, documented rather than papered over: the node IS discovered (that is
what the declaration buys), its prompt is not.
