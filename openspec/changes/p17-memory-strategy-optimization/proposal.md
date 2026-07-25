## Why

Every optimization axis on this platform is a **Dimension**: a node's model, prompt, skills, or context
can be overridden, resolved, hashed into a `config_hash`, and — for the two call-site-safe axes — realized
as a codemod and scored. The enum is closed and tiny
([`internal/variantspec/spec.go:42`](../../../internal/variantspec/spec.go)): `DimModel`, `DimPrompt`,
`DimSkills`, `DimContext`. **Memory is not one of them.** What an agent carries *across* invocations and
sessions is neither modelable nor optimizable today.

Memory exists in the codebase only as two disconnected fragments. It is a **detectable behavioral
pattern** — `MemoryManagement`, pattern 8 of the frozen taxonomy
([`internal/patternclassifier/taxonomy.go:29`](../../../internal/patternclassifier/taxonomy.go)), with
metrics `memory_hit_rate` / `recall_precision` / `staleness` / `write_amplification`
([`internal/patternclassifier/metricset.go:98`](../../../internal/patternclassifier/metricset.go)) and
failure modes `contradictory_memory` / `stale_read`. And it is a **filesystem directory concept**
(`MemoryDir = "memory"`, [`internal/agentlayout/layout.go:13`](../../../internal/agentlayout/layout.go)).
Neither is optimizable: the classifier can *notice* that a target agent manages memory and *score how well
it does*, but there is no Dimension, no override field, no registry entry, and no operator to *change the
strategy and re-measure*. Diagnosis without an actionable axis is a thermometer with no dial. The old
runtime "memory sweeper" that once touched this area was **removed** in the pivot
([`internal/launch/launch.go:6`](../../../internal/launch/launch.go)), leaving nothing optimizable behind.

This is **greenfield**: P17 introduces memory as a **new first-class Dimension**, following the
repository's canonical eight-step "add an axis" checklist end-to-end, exactly as `DimModel` and `DimPrompt`
did. Two facts make this the honest shape. First, **memory is not context**: memory persists *across*
invocations while context assembly ([P16](../p16-context-strategy-optimization/)) is how a *single* call
builds its message list — and the classifier already encodes the split, since `MemoryManagement`'s
confirmation signal is *"memory read/write against a store between turns"* (`taxonomy.go:108`). Second,
**realizing a memory change at a call site is not yet safe**: it means wiring a store the surrounding code
reads and writes between turns, which is strictly harder than the skills/context rewrites that already
*refuse* with a typed `unsafeRewrite` ([`internal/transform/rewrite.go:388`,`:417`](../../../internal/transform/rewrite.go)).
So P17 ships the modeling and a **first-class refusal**, not a codemod, and claims **no scored memory win**.

## What Changes

- **New capability `memory-store`.** A new content-addressed registry `Kind` `memory`, alongside `model`,
  `prompt`, `skill`, and `context` ([`internal/registry/registry.go:57`](../../../internal/registry/registry.go)),
  backed by a new `memory_entry` table. It holds **versioned memory strategies**, and the platform ships
  exactly five as a **closed, versioned** set: **`none`** (identity — no memory), **`scratchpad`**
  (ephemeral working notes), **`summary-buffer`** (a rolling summary that trades fidelity for tokens),
  **`vector-recall`** (embedding-backed retrieval of prior turns), and **`entity-memory`** (structured key
  facts). Each strategy declares a **`ParamsSchema`**, and a memory entry whose params violate the schema
  is rejected at seal. `none` is the **identity strategy**: a resolved node whose strategy is `none`
  canonicalizes **byte-identically** to a node with no memory field, so no existing `config_hash` changes.
  Strategies are referenced by version_id, never inlined, so a memory change is always resolvable back from
  a hash; and each carries a human title and description distinct from its wire name.
- **New capability `memory-policy`.** A new **`DimMemory`** Dimension joins the closed enum
  ([`spec.go:42`](../../../internal/variantspec/spec.go)); a **`NodeOverride.MemoryRef`** field (additive,
  `omitempty`, participating in `isEmpty`/`Refs`/`Validate`) at [`spec.go:183`](../../../internal/variantspec/spec.go);
  a `resolveNode` block and a `DimMemory` case in `Dimensions()`
  ([`resolve.go:67,154`](../../../internal/variantspec/resolve.go)); an additive `omitempty`
  **`ResolvedNode`** memory field ([`resolved.go:46`](../../../internal/variantspec/resolved.go)) that
  **auto-participates in `config_hash`** because the hash is purely structural; and an additive `omitempty`
  IR field defaulting to `none`, emitted by a discovery frontend ([`emit.go`](../../../internal/discovery/emit.go)).
  Because binding a memory backend at a call site is **not yet safe**, the phase specifies, as a
  first-class requirement, an **interim refusal**: a resolved node carrying a `MemoryRef ≠ none` **SHALL be
  refused at transform with a typed `unsafeRewrite`, never silently dropped**, in **both** the Go AST
  rewriter ([`rewrite.go:54`](../../../internal/transform/rewrite.go)) and the tree-sitter span rewriter
  (`rewrite_span.go:59`) — mirroring `refuseSkills`/`refuseContext`. A spec carrying a `MemoryRef` still
  **resolves** and still produces a stable `config_hash`; only the transform refuses. Finally a new
  operator **`OpMemoryPolicy`** ([`operator.go:34`](../../../internal/proposal/operator.go), catalog row,
  `operatorPrior` + `verifyOrderHint` at [`gain.go:8,26`](../../../internal/proposal/gain.go)) proposes a
  strategy swap against a memory bottleneck; **verification decides**, and while the transform refuses, a
  memory proposal is surfaced as **refused-not-scored** — no win is claimed.
- **Not changed here.** No memory codemod / call-site rewrite (**deferred** to a future memory-runtime
  owner). No live memory runtime or store (the platform wiring was removed at the pivot and is reintroduced
  per phase; `vector-recall`'s embedding store and `entity-memory`'s structured store are strategy
  *descriptions*, not services). No change to the pattern taxonomy, its metrics, or `TaxonomyVersion`
  ([`taxonomy.go:8`](../../../internal/patternclassifier/taxonomy.go)) — memory *consumes* the existing
  `MemoryManagement` metric set and adds none. No within-call context work (that is
  [P16](../p16-context-strategy-optimization/)'s `DimContext`). No second cost model and no bespoke
  scoring path — eval stays axis-agnostic on `config_hash` + `Trace`.

## Impact

- **Affected capabilities:** `memory-store` (new), `memory-policy` (new). Consumed, not modified:
  `variant-spec`/`config-hash` (P2), `registry` (P0/P2), `discovery-engine`/`workflow-ir` (P1),
  `transform-engine` (P2), `pattern-classifier` (P4.5), `proposal-catalog` (P4.5), `eval-harness`/`scoring`
  (P4). Kept disjoint: `context-assembly` (P16).
- **Affected code/systems:** `internal/variantspec` (Dimension, NodeOverride, resolve, ResolvedNode),
  `internal/registry` (new `KindMemory` + `memory.go` + `memory_entry` table), `internal/discovery`
  (IR field + frontend), `internal/transform` (a `refuseMemory` in both engines), `internal/proposal`
  (operator kind, catalog, priors). One new table; everything else is an additive field.
- **Dependencies:** requires **P1** (IR), **P2** (resolution, `config_hash`, transform dispatch),
  **P4** (axis-agnostic harness — ready but not exercised for memory at M20), **P4.5** (taxonomy, catalog,
  priors), **P16** (the context axis memory is kept disjoint from).
- **Unblocks:** a **future memory-runtime phase** that owns only the live store and the call-site codemod
  which lifts the refusal; and **memory-aware diagnosis**, once a memory variant can be scored.
- **Breaking:** none. Every change is additive; a node with no memory strategy serializes and hashes
  byte-identically to a pre-P17 node, and the P0 golden `config_hash` vectors reproduce unchanged.
- **Sequencing:** **20a** (the store + the modeled, refused Dimension) is a complete phase on its own.
  **20b** (the operator + the metric wiring) follows. A **future** phase owns the rewrite that lifts the
  refusal — explicitly out of scope here.
