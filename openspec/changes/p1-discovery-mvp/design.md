# Design — P1 Discovery MVP (Go, static)

Cross-reference: [`../../../docs/prd/P1-discovery-mvp.md`](../../../docs/prd/P1-discovery-mvp.md).

## Context

Discovery is the first producer of the P0 Workflow IR. It turns a Go repository — treated as
**untrusted text** — into a graph of addressable nodes that every downstream subsystem keys on.
The two facts that dominate the design are (1) real codebases **wrap** the SDK, so signature
matching alone under-counts nodes, and (2) node count is only well-defined **per static
definition** because loops/agents make a variable number of calls at runtime. Confirming the
candidate graph by executing it is dynamic tracing (P5); P1 is static-only.

Back-of-envelope (System Designer lens): repos are tens–hundreds of nodes over up to ~200k LOC;
throughput target ≤60 s/repo on one worker. This is a single-worker AST pass, not a distributed
system — the design optimizes for **determinism and faithfulness**, not throughput scaling.

## Key decisions

### D1 — Three co-equal, merged detection sources (not a fallback chain)
Signature registry, user-declared entrypoints, and framework DAG readers all feed **one** node set,
deduplicated by a stable call-site identity. **Alternative rejected:** "registry first, declarations
only for misses." Rejected because a wrapper call site can legitimately be described by both a
declaration and a framework reader; treating declarations as a fallback invites double-counting and
makes the wrapper case second-class. User-declared entrypoints are **mandatory and co-equal** — the
wrapper reality is the norm, not the exception.

### D0 — Language-agnostic core behind a `LanguageFrontend` (rescope)
Discovery is split into a **language-neutral core** (signature-registry *model*, node-ID scheme,
metadata extraction, call-graph builder, IR emitter, run report, all eight invariants) and a
**`LanguageFrontend`** — the only language-specific layer — which parses a file/unit and enumerates
call sites in a normalized shape `{root, selector-chain, enclosing symbol, import map, structural
position}`. The detector consumes that normalized shape and never knows the source language. **Adding
a language = adding a frontend + registry rows + fixtures; the core is untouched.** **Alternative
rejected:** a Go-specific pipeline generalized ad-hoc later — rejected because it would bake `go/ast`
assumptions into the detector/emitter and make every new language a partial rewrite (an L5/L6
evolvability/extensibility violation). This is the seam the multi-language rescope (PRD §15) turns on.

### D2 — `go/ast` for the Go frontend; tree-sitter for every other language
The **Go frontend** uses Go's native `go/ast` + import resolution (accurate symbol resolution). **All
other language frontends** use **tree-sitter** — a pure, language-agnostic parser with a grammar per
language (Python, TS/JS, Java/Kotlin, Rust, …). Tree-sitter is syntactic (no type resolution), so
non-Go detection leans on import-presence + selector-chain + mandatory declared entrypoints, and marks
more fields `unresolved` honestly (the Go frontend's method-call detection already works this way, so
the approach generalizes cleanly). **Alternative rejected:** a single typed analyzer per language
(compiler frontend / LSP) for full fidelity — rejected for P1 as far too costly across N languages;
tree-sitter gives breadth now, and per-language type resolution is a **post-M1 fidelity uplift**, not a
gate. **Alternative rejected:** tree-sitter for *Go too* (drop `go/ast`) — rejected because `go/ast`
gives Go real import/type resolution for free and the Go frontend already ships; tree-sitter-go remains
available if a uniform substrate is later preferred.

> **Rescope note:** the previous revision listed tree-sitter as a *post-M1 non-goal*. On the product
> owner's direction ("any repo of any language"), multi-language moved to a **first-class P1 goal**;
> D0 + D2 are the architecture that makes it affordable — the Go work becomes frontend #1, not throwaway.

### D3 — Honest `unresolved`, never a guess
Statically-unresolvable metadata (inter-procedural prompt assembly, runtime-selected model) is
emitted as `unresolved` and **flagged** as a P5 dynamic-trace candidate, never silently omitted and
never guessed. **Alternative rejected:** best-effort inference of a "probable" value — rejected
because a confident-but-wrong prompt/model would mislead P2 overrides and P4 attribution. An honest
gap is a feature: it is exactly the input P5 dynamic tracing consumes.

### D4 — Per-static-definition counting with `variable_at_runtime`
Node count is reported per static definition. Any node reachable through a loop/agent control
structure is flagged `variable_at_runtime`; **no fixed runtime invocation count is ever emitted**.
**Alternative rejected:** estimating an iteration count statically — rejected as unknowable without
running the code; the honest artifact is the flag, and P5 supplies the real count from traces.

### D5 — Content-addressed, deterministic node IDs
Node IDs derive from a stable tuple (package path + function + call-site identity + content hash),
not from traversal/map order or line numbers alone. Output is sorted. **Consequence:** two runs on
unchanged source produce byte-identical, diffable IR (the M1 exit criterion). **Alternative
rejected:** sequential integer IDs — rejected because they reshuffle on any edit and destroy diffability.

### D6 — No-execution as a structural invariant
The discovery path contains **no** code that spawns a process, runs `go run`/`go build
-buildmode=plugin`, or loads the repo as a plugin. This is enforced structurally *and* asserted by a
test that denies process spawn and proves an `init()` side effect never fires. **Alternative
rejected:** "just don't call the run functions" as a convention — rejected; the invariant is a
security boundary (untrusted source) and must be testable, not aspirational.

### D7 — Framework DAG read declaratively, degrade-to-flag on version drift
For LangGraph/CrewAI, derive nodes/edges from the declarative graph definition, not from inferred
call order. An unrecognized framework version degrades the subgraph to a flagged, best-effort result
rather than crashing or silently mis-inferring topology.

## Data model / interface sketches

**Node (populates P0 IR node fields):**
```
Node {
  id                 string        // content-addressed, stable (D5)
  call_site          { file, line, func, package }
  detected_by        [registry|declared|framework]   // may be multiple after merge
  model              Value|unresolved
  prompt_construction PromptMeta|unresolved
  tools_skills       []ToolRef
  upstream_dataflow  []DataRef
  input_schema       SchemaStub    // typed I/O contract stub, best-effort static
  output_schema      SchemaStub
  variable_at_runtime bool         // D4
  ambiguity_flags    []{ field, reason, p5_candidate:true }   // D3
  framework_source   string?       // D7, when detected_by includes framework
}
Edge { from_node, to_node, kind: data|control, evidence }
```

**`llm-eval.yaml` (user-declared entrypoints, mandatory):**
```yaml
entrypoints:
  - symbol: "internal/llm.Complete"      # package-qualified func/method
    args:
      model:  { name: "modelID" }        # or { index: 1 }
      prompt: { name: "prompt" }
      tools:  { name: "tools" }
```

**Interfaces:**
- `SignatureRegistry` — data table; add an SDK by adding a row (D1).
- `FrameworkReader { Detect(pkg) bool; ReadDAG(pkg) (nodes, edges, err) }` (D7).
- Output: Workflow IR JSON (validated vs `workflow-ir.schema.json`) + `discovery-report.json`.

## Risks

- **Wrapper coverage depends on the user authoring `llm-eval.yaml`.** Mitigation: the run report
  surfaces detection counts by source so an under-declared repo is visible; fixture proves the mechanism.
- **Intra-procedural resolution cost/precision trade-off.** Mitigation: bounded resolution budget per
  call site; beyond it, mark `unresolved` + flag (open question PRD Q3).
- **Framework version drift.** Mitigation: versioned, isolated readers; degrade-to-flag (D7).
- **Determinism regressions.** Mitigation: golden-IR diff test in CI (D5).
