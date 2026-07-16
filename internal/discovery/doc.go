// Package discovery is the P1 Discovery Engine: it reads a Go repository as untrusted text and
// extracts static LLM call sites into the frozen P0 Workflow IR.
//
// Design records: docs/discovery/ (01 contract confirmation … 09 run report). This package
// implements tasks.md §3 (parsing & detection): loader (§3.1), AST parser (§3.2), signature-registry
// detector (§3.3), declared-entrypoint detector (§3.4), and multi-source merge/dedup (§3.5).
//
// # No-execution invariant (I1 — security boundary, not a preference)
//
// Discovery NEVER executes, compiles, or `go list`s the target repo. Analysis is over go/parser ASTs
// and text only. This package deliberately does NOT import os/exec, plugin, or golang.org/x/tools/go/
// packages (which shells out to `go list`). Because the Go language forbids two imports with the same
// local name in one file without an alias, per-file import resolution (local name -> import path) is
// unambiguous without type-checking — so we get correct SDK disambiguation without a subprocess.
//
// Fidelity note (bounded, honest): without go/types, method-call detection (client.Messages.New) is
// resolved by "file imports the SDK path" + "selector-chain suffix match" rather than by typing the
// receiver. This detection basis is recorded per site (BasisPackageQualified vs BasisSelectorImport)
// so downstream and the run report can see how a node was matched. An unresolved value is marked, never
// guessed (I5).
package discovery
