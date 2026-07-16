# P1 Discovery — Design: `FrameworkReader` interface & version-drift (resolves PRD Q2)

> **Task:** P1 `tasks.md` §2.4. **Phase:** ② Design (Backend lead, System Designer support).
> **Inputs:** [call-shape catalog §1.4 (langchaingo interface)](02-go-call-shape-catalog.md),
> [contract §1 (`subgraphs` reserved)](01-ir-contract-confirmation.md), FR5, D7.

## §0 TL;DR

A `FrameworkReader` derives nodes/edges from a framework's **declarative graph definition** instead of
inferring topology from call order (FR5, D7). Readers are **versioned and isolated**: an unrecognized
version **degrades to a flagged, best-effort subgraph**, never a crash and never silent mis-inference.
**⚠️ Scope conflict surfaced for user decision:** the PRD names **LangGraph/CrewAI**, but those are
**Python** frameworks and **P1 is Go-only** — see §4. The interface below is defined generically; the
recommendation is that P1 ships the interface + a **Go-native** reader (langchaingo/langgraphgo) and defers
the Python frameworks to the post-M1 language-agnostic (tree-sitter) path.

## §1 The problem

For agent frameworks, the runtime topology (which step feeds which) is declared in a **graph builder**, not
implied by the order of source statements. Example (langgraphgo-style):
```go
g := graph.NewStateGraph()
g.AddNode("classify", classifyFn)
g.AddNode("route",    routeFn)
g.AddNode("answer",   answerFn)
g.AddEdge("classify", "route")
g.AddConditionalEdges("route", pick, map[string]string{"faq": "answer", "esc": "escalate"})
g.SetEntryPoint("classify")
```
Inferring edges from call order here would be wrong — the wiring is data in `AddEdge`/`AddConditionalEdges`.
FR5 says: **read the declaration.** This produces reliable data/control edges (`AddEdge` → `data`,
`AddConditionalEdges` → `control`) that the Pattern Classifier (P3.5) can trust.

## §2 The interface

```go
// A FrameworkReader is one versioned, isolated plugin per framework. It NEVER executes target code
// (I1) — it reads the graph-builder calls statically, like the rest of Discovery.
type FrameworkReader interface {
    // Name identifies the framework for the run report ("langchaingo", "langgraphgo").
    Name() string

    // Detect reports whether this package uses the framework, and which version it looks like.
    // recognized=false => Discovery still calls ReadDAG but marks the subgraph degraded (§3.2).
    Detect(pkg *packages.Package) (version string, present bool, recognized bool)

    // ReadDAG derives nodes and edges from the declarative graph. It returns diagnostics (never panics)
    // and, on partial understanding, as much of the graph as it could read plus a degraded flag.
    ReadDAG(pkg *packages.Package) (FrameworkGraph, []Diagnostic, error)
}

type FrameworkGraph struct {
    FrameworkSource string          // -> recorded on the subgraph in the run report (Finding A: not on the IR node)
    Version         string
    Recognized      bool            // false => degrade-to-flag (§3.2)
    Nodes           []NodeIdentity  // reuse the node-ID scheme (doc 06) so framework nodes dedup with detected nodes
    Edges           []FrameworkEdge // {From, To, Kind: data|control}
}
```

### 2.1 Decision — a registry of readers, resolved by `Detect`, isolated per reader
**Design.** Discovery holds `[]FrameworkReader`; for each package it asks each reader `Detect`. A reader is
a self-contained unit; a panic inside one is recovered and turned into a diagnostic (I7) so one broken
reader can't kill the run. **Alternatives compared.** *One monolithic framework function with a `switch`* —
rejected: couples all frameworks, and a bug in the CrewAI branch breaks LangGraph handling (L2 稳定 + L6
扩展). **Effect.** Adding a framework = adding a reader; a reader failure degrades exactly one subgraph.

## §3 Mapping & version-drift

### 3.1 Declarative-graph → IR mapping
| Framework construct | IR effect |
|---|---|
| `AddNode(name, fn)` where `fn` (transitively) contains an LLM call | one node (deduped by node-ID with the call-site the extractor finds inside `fn`) |
| `AddNode(name, fn)` with no LLM call | **not** an LLM node — recorded as framework structure only |
| `AddEdge(a, b)` | edge `a→b`, `kind: data` |
| `AddConditionalEdges(a, pick, {…})` | edges `a→each target`, `kind: control` |
| `SetEntryPoint(n)` | subgraph entry annotation (report) |
| the whole builder | one `Subgraph` (reserved field, [contract §1](01-ir-contract-confirmation.md)) tagged `framework_source` in the report |

### 3.2 Decision (Q2) — unrecognized version degrades to a flagged subgraph, never crash/mis-infer
**Problem.** Q2: which versions does the reader target, and how is drift surfaced? **Design.** `Detect`
returns `recognized`. On `recognized=false` (a version the reader hasn't been validated against): read what
is structurally unambiguous (`AddNode`/`AddEdge` are stable across versions), mark the subgraph
`recognized=false` + `degraded`, flag its nodes as review-worthy in the report, and **do not** fall back to
call-order inference for the parts it couldn't read. **Alternatives compared.** (a) *Hard-fail on unknown
version* — rejected: a minor framework bump would break discovery of the whole repo (L2 稳定). (b) *Silently
infer topology from call order when the version is unknown* — rejected outright: silent mis-inference is
worse than an honest gap (D3/I5) and would feed the Pattern Classifier wrong topology. **Effect.** A
framework upgrade degrades to "here's the part we're sure of, flagged as partial," never a crash and never
a confident-wrong graph. Target versions are recorded per reader and advisory in P1.

## §4 ✅ RESOLVED by the multi-language rescope: LangGraph + CrewAI ship for Python

> **Update (§10.11):** the rescope (PRD §15) made P1 multi-language, so the conflict below is resolved
> in favor of shipping **both**. The Go framework reader (`FrameworkReader`, `*Package`-based) and the
> tree-sitter framework readers (`syntacticFrameworkReader`, `SyntacticUnit`-based — `langGraphReader`
> + `crewAIReader`) are the two per-substrate implementations of the same concept. Python LangGraph
> (`add_node`/`add_edge`/`add_conditional_edges`) and CrewAI (`Agent(role=…)` + `Crew`/`kickoff`) both
> ship, tested, behind the reader interface. The original framing is kept below for the record.

### ⚠️ Original scope conflict (now resolved): LangGraph/CrewAI are Python; P1 was Go-only

Per the skill's *"expose conflicts, don't average them"* rule, this is stated plainly rather than papered
over:

- The PRD/FR5 name **LangGraph** and **CrewAI**. **LangGraph** and **CrewAI** are **Python** libraries.
- **P1's mandate is Go static analysis via `go/ast`** (G1, D2); non-Go languages are an explicit non-goal
  deferred to the post-M1 tree-sitter path.
- Therefore a P1 Go reader **cannot** read a Python LangGraph/CrewAI graph — there is no Go source to parse.

**Recommendation (for the design-review sign-off to ratify):**
1. **Ship the `FrameworkReader` interface in P1** — it is the correct abstraction and carries no Go/Python
   assumption.
2. **Ship one Go-native reader in P1**: target the Go framework that actually has a declarative graph —
   **`langchaingo`** agents/chains, and **`langgraphgo`** (the community Go port of the LangGraph builder)
   if present. This satisfies FR5's *intent* ("read declarative DAGs, don't infer from call order") on the
   language P1 owns, and gives the framework-DAG fixture (§6.3) a real Go target.
3. **Defer Python LangGraph/CrewAI proper to the multi-language phase** (tree-sitter), tracked as its own
   item — the interface is ready for it.

This keeps P1 honest (it does what Go allows) without silently redefining the PRD. If the reviewer instead
wants Python support *in P1*, that is a scope change to the "Go-only, static" mandate and must be decided
explicitly — it is not something to smuggle in under FR5.

## §5 Invariant ties

- **I1 (no execution):** readers parse the builder calls statically; they never run `fn` or the graph.
- **I6 (additive IR):** framework nodes/edges populate only frozen fields; `framework_source`, `version`,
  `degraded` live on the **subgraph record in the run report** (Finding A).
- **I2 (variable count):** a framework loop/agent node still sets `variable_at_runtime=true`, one node.
- **I7 (no crash):** a reader panic is recovered → diagnostic; the rest of discovery continues.

## §6 Consumed by

Implementation §4.4 (framework readers), the subgraph tagging in the run report ([doc 09](09-run-report-shape.md)),
and fixture §6.3. The scope-conflict recommendation (§4) is an **open decision for the design-review
sign-off** and is listed as such in the [README open questions](README.md).
