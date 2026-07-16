# Discovery — Exploration & Design Records (P1)

The **① Understand & Explore** and **② Design** deliverables for **P1 Discovery MVP**. Per the R&D
seven-phase process, a research conclusion is the written **entry condition** for design, and a design
must run every decision through *problem → design → why appropriate → alternatives compared → effect*,
arbitrated by the 8-level cost law. These are the P1 analogue of the P0 System-Designer
[`docs/decisions/`](../decisions/README.md) records: written, checkable artifacts the PRD and OpenSpec
specs build on.

## ① Understand & Explore

| Record | Covers | P1 tasks |
|---|---|---|
| [`01-ir-contract-confirmation.md`](01-ir-contract-confirmation.md) | Field-by-field confirmation of the frozen P0 Workflow IR contract Discovery must populate: root envelope, every required node field, edge fields, the typed I/O-contract stubs, and the static-node vs. runtime-invocation distinction. Records **three divergences** where the P1 `design.md` node sketch is wider than the frozen `additionalProperties:false` schema (provenance fields, the missing `unresolved` marker, non-empty `context_assembly.policy`) and resolves them for Design. | 1.1 |
| [`02-go-call-shape-catalog.md`](02-go-call-shape-catalog.md) | The five Go LLM call shapes (Anthropic `anthropic-sdk-go`, OpenAI `openai-go`, `sashabaranov/go-openai`, `langchaingo`, AWS `bedrockruntime`), verified against current APIs; the structural axes (symbol kind, arg passing, model-binding site); and **12 wrapper patterns (W1–W12) that defeat signature matching** — the empirical justification for mandatory `llm-eval.yaml` entrypoints. Ends with the direct implications for design tasks §2.1–§2.2 and the fixtures. | 1.2 |
| [`03-discovery-invariants.md`](03-discovery-invariants.md) | The invariants that must never break, each with *why*, decided failure behavior, and an enforcing test that can go red: **I1** no execution of the repo, **I2** no fixed count for a variable node, **I3** stable node IDs, **I4** every missing node explainable from the run report — plus I5–I8 (honest `unresolved`, additive-only IR, skip-and-report, least privilege). Includes the invariant→enforcement map used as the P1 review checklist. | 1.3 |

## ② Design

| Record | Covers | P1 tasks |
|---|---|---|
| [`04-signature-registry.md`](04-signature-registry.md) | The signature registry as **data, not code**: the `SignatureRow` / `ArgMap` / `ArgLocator` model (import-path-qualified key, symbol-kind + locator taxonomy, value-wrapper unwrap, streaming/opacity), and the **five seed rows** in `registry.yaml` form (Anthropic Messages, OpenAI Chat Completions ×2, LangChain(Go) invoke, Bedrock Converse/InvokeModel). | 2.1 |
| [`05-llm-eval-yaml-schema.md`](05-llm-eval-yaml-schema.md) | The `llm-eval.yaml` schema for user-declared wrappers, reusing the registry's `ArgLocator` forms. **Resolves Q1** (support index/name/field-path/option-constructor, not positional-only) and **Q5** (file optional-but-recommended; absence surfaced in the report, not fatal). Boundary validation: deploy-time fail-loud, run-time fail-open. | 2.2 |
| [`06-node-id-scheme.md`](06-node-id-scheme.md) | The content-addressed `node_id` tuple (module-pkg-path · enclosing-symbol-FQN · selector · per-selector occurrence-index), excluding absolute lines/formatting. **Resolves Q4**. Full stability/uniqueness analysis incl. the documented same-selector-reorder limitation; a prompt edit keeps the id (lineage-correct). | 2.3 |
| [`07-framework-reader.md`](07-framework-reader.md) | The `FrameworkReader` interface + declarative-DAG→IR mapping + version-drift **degrade-to-flag** (**resolves Q2**). Surfaces the **Go-only-vs-Python (LangGraph/CrewAI) scope conflict** and recommends: ship the interface + a Go-native (langchaingo/langgraphgo) reader in P1, defer Python to the multi-language phase. | 2.4 |
| [`08-failure-behavior.md`](08-failure-behavior.md) | Three policies (fail-loud-at-config / skip-and-report / mark-unresolved-and-flag) and a **12-row fault decision table** (F1–F12), each with IR effect, run-report effect, and a red-able test — the concrete enforcement of invariants I4/I5/I7. | 2.5 |
| [`09-run-report-shape.md`](09-run-report-shape.md) | The `discovery-report.json` shape — summary, detections-by-source, per-node provenance (Finding A's home for `detected_by`/`ambiguity_flags`/`framework_source`), ambiguity flags with stable `code`s, dedup merges, file + declaration diagnostics, framework subgraphs. CI-enforced `nodes_emitted == len(ir.nodes)`. Proves **I4** (every missing node explainable). | 2.6 |
| [`10-hardening-review.md`](10-hardening-review.md) | The §7 harden & adversarial-review record: no-execution (structural + behavioral), least-privilege, hostile-input robustness, and the four hunted failure shapes — including the panic-isolation defect found & fixed. | §7 |
| [`11-tree-sitter-substrate.md`](11-tree-sitter-substrate.md) | The multi-language substrate: `LanguageAnalyzer` + `SyntacticFrontend` (tree-sitter, cgo), the normalized `RawCallSite` shape shared with Go, and the **type-free resolution floor** (import-presence + selector + declared + keyword literals) — **resolves PRD Q7**. Python is the first tree-sitter frontend. | 10.4, 10.5 |

**Upstream contract (frozen at M0, additive-only):**
[`schemas/workflow-ir.schema.json`](../../schemas/workflow-ir.schema.json) ·
[`schemas/runtime-invocation.schema.json`](../../schemas/runtime-invocation.schema.json) ·
[`openspec/changes/p0-foundations/specs/workflow-ir/spec.md`](../../openspec/changes/p0-foundations/specs/workflow-ir/spec.md).

**Product rationale:** [`docs/prd/P1-discovery-mvp.md`](../prd/P1-discovery-mvp.md).
**Behavioral spec & plan:** [`openspec/changes/p1-discovery-mvp/`](../../openspec/changes/p1-discovery-mvp/).

## PRD open-question ledger

| # | Question | Status |
|---|---|---|
| Q1 | `llm-eval.yaml` arg mapping — position, name, or both? | **Resolved** ([doc 05](05-llm-eval-yaml-schema.md)): all four locator forms (index / name / field-path / option-constructor). |
| Q2 | Which framework versions; how is drift surfaced? | **Resolved** ([doc 07](07-framework-reader.md)): versioned isolated readers, unknown version → degrade-to-flag. |
| Q3 | How deep does intra-procedural resolution go? | **Framed** ([doc 08 §2.3](08-failure-behavior.md)): a bounded depth+node budget; over-budget → `unresolved`. **Concrete numbers left to implementation §4.1.** |
| Q4 | Node-ID tuple stable across line shifts, unique per site? | **Resolved** ([doc 06](06-node-id-scheme.md)): pkg-path · symbol-FQN · selector · occurrence-index; no absolute lines. |
| Q5 | Is `llm-eval.yaml` required or optional? | **Resolved** ([doc 05 §3.2](05-llm-eval-yaml-schema.md)): mechanism mandatory/co-equal, **file optional-but-recommended**; absence surfaced in the report. |

Also ratified in design and carried into implementation:
- **The `unresolved` sentinel constant** (contract Finding B / I5) — `model.provider="unresolved"`,
  `model.model_id="unresolved"`, `prompt.inline=""` + a report flag; one documented constant ([doc 08 §3](08-failure-behavior.md)).
- **Run-report as the home for provenance** (contract Finding A / I6) — [doc 09](09-run-report-shape.md).

## Rescope: Go-only → multi-language (resolved)

P1 was **rescoped from Go-only to multi-language** on the product owner's direction (PRD §15,
`p1-discovery-mvp` proposal). Impact on this design set:

- **doc 02 (Go call-shape catalog)** is now the catalog for **frontend #1 (Go)**; each additional
  language gets its own call-shape catalog + registry rows (openspec tasks §10.6–10.10).
- **doc 07 §4's open conflict is resolved.** The Go-vs-Python framework-reader question is settled by
  the rescope: **both** ship — the Go framework reader (delivered) and **Python LangGraph/CrewAI readers**
  (openspec task §10.11), behind the one `FrameworkReader` interface.
- **The language-neutral core is unchanged.** Everything in docs 01/03/04(model)/05/06/08/09 is
  language-neutral and reused across frontends; only the parse + call-site-resolution layer is
  per-language (Go via `go/ast`, others via tree-sitter). See PRD §8 and design D0/D2.

## ⚠️ Open decision for the design-review sign-off (硬性规则 2: 集体讨论定稿)

Per the seven-phase process, a design is not final until it clears a **collective review** (not
point-to-point sign-off), with dissent written back into the doc. Open items:

- **Language priority + per-language fidelity floor** — the tree-sitter frontends have no type
  resolution, so non-Go detection is lower-fidelity (more honest `unresolved`). The reviewer should
  ratify the shipped-language order (Go ✅ → Python → TS/JS → Java/Kotlin → Rust → …) and accept the
  per-language `unresolved` floor (PRD Q7) as adequate for M1, with deep type resolution as a post-M1
  uplift.
