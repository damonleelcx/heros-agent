# P1 Discovery — Confirmation of the frozen P0 IR contract

> **Task:** P1 `tasks.md` §1.1 — *Confirm the frozen P0 IR contract: node fields, edge fields,
> typed I/O contract stubs, and the static-node vs. runtime-invocation distinction Discovery must
> populate.*
> **Workstream:** ① Understand & Explore (Backend lead, AI Eng support).
> **Source of truth (frozen at M0, additive-only):**
> [`schemas/workflow-ir.schema.json`](../../schemas/workflow-ir.schema.json),
> [`schemas/runtime-invocation.schema.json`](../../schemas/runtime-invocation.schema.json),
> [`openspec/changes/p0-foundations/specs/workflow-ir/spec.md`](../../openspec/changes/p0-foundations/specs/workflow-ir/spec.md),
> [`docs/decisions/m0-review-and-freeze.md`](../decisions/m0-review-and-freeze.md).

This is a **confirmation**, not a redesign. Discovery is the first producer of the Workflow IR; the
IR is a public contract that outlives the discovery code. The job here is to read the frozen schema
literally, write down exactly which fields Discovery must populate, and — critically — record where
the P1 `design.md` node sketch **diverges** from the frozen schema so the Design phase (§2) resolves
it deliberately instead of discovering it at emit time.

---

## 1. Document envelope (root object)

`workflow-ir.schema.json` sets `additionalProperties: false` at every level. The root requires
exactly four keys; Discovery populates all four.

| Field | Type / shape | Discovery obligation |
|---|---|---|
| `ir_version` | semver string `^\d+\.\d+\.\d+$` | Emit the IR-schema version Discovery targets (**the contract's version, not the repo's** — see the schema `$comment`). Pin one constant; do not read it from the repo. |
| `workflow` | `{ id, repo{url, commit_sha}, language }` | `id` = stable workflow id; `repo.url` + `repo.commit_sha` (7–64 hex) = the exact commit discovered — this is half of what makes the IR diffable/reproducible; `language` = `"go"` for P1. |
| `nodes` | array of `Node` | One entry per **static** LLM call site (see §2). **Node count ≡ `len(nodes)`** — never expand a loop into N nodes (§4). |
| `edges` | array of `Edge` | Data/control edges between nodes (see §3). |
| `subgraphs` | *(optional)* array of `Subgraph` | **Not populated in P1.** Reserved for the P3.5 Pattern Classifier. Absence is valid. |

**Referential integrity is a CI check, not a JSON-Schema check** (per the root `$comment`): every edge
endpoint and every runtime-invocation `node_id` must reference an existing node. Discovery must emit
graphs that pass that CI check even though the schema alone won't catch a dangling edge.

---

## 2. Node — the fields Discovery must populate

`$defs/Node`, `additionalProperties: false`. **All of the following are required**; a node missing any
is rejected as invalid (P0 spec: *"A node exposes every override dimension the Config Layer will
target"*).

| Required field | Frozen shape | What Discovery puts here | Notes / hazards |
|---|---|---|---|
| `node_id` | string, `minLength 1` | Deterministic, content-addressed id (§1.1 design task §2.3). | Must be stable across runs on the same commit → diffability. Not a sequential integer; not map-iteration-order dependent. |
| `kind` | `const "static_definition"` | Literal `"static_definition"`, always. | A node is *always* a static definition. Runtime instances are a **separate schema** (§5). |
| `call_site` | `{ file, symbol, line_start, line_end, ast_path? }` | `file` (repo-relative), `symbol` (enclosing func/method), `line_start`/`line_end` (≥1), optional `ast_path`. | This is the anchor P2's codemod rewrites. `ast_path` (optional) is where the structured AST path goes — use it for rewrite-robustness. Enclosing `symbol` for a call inside a closure = the closure's parent func. |
| `model` | `{ provider, model_id, params }` — `provider`/`model_id` `minLength 1`, `params` free-form object | The observed model binding. | **No `unresolved` representation exists** (see §6, Finding B). `params` (temperature, max_tokens…) is part of the `config_hash` include set. |
| `prompt` | `{ variables[] }` **plus `oneOf`** `template_ref` \| `inline` | Either an inline prompt string **or** a template reference, **plus** the declared variable slots. | `oneOf` is exclusive — emit exactly one of `template_ref`/`inline`. Large inline prompts SHOULD be content-hashed to the blob store and referenced (storage decision record). |
| `tools_skills` | array of strings (`minLength 1` each) | Bound tool/skill names. | May be empty `[]` (valid). |
| `context_assembly` | `{ policy, description }` — `policy` `minLength 1` | How context is built (RAG policy / message-window strategy). | For a plain call with no retrieval, emit an explicit policy (e.g. `"inline_messages"`), **not** an empty string — `policy` has `minLength 1`. |
| `io_contract` | `{ input_schema, output_schema }` — each an embedded JSON Schema object | Best-effort static I/O shape; **permissive is allowed** — `{"type":"object"}` is valid when the shape can't be inferred. | Mandatory from v1 even though re-arrangement (P5) is the only consumer. Refining later to a stricter schema is additive → **no MAJOR bump**. CI meta-validates that each embedded schema compiles. |
| `invocation_semantics` | `{ type ∈ {single,loop,conditional}, variable_at_runtime: bool }` | `type` = the static control shape; `variable_at_runtime = true` for loop/agent/conditional-count nodes. | This is the load-bearing static-vs-runtime field (§4). Never emit a runtime count. |
| `pattern_labels` | *(optional)* array | **Not populated in P1.** | Reserved for P3.5. Absence is valid at the same MAJOR. |

### 2.1 Typed I/O contract stubs (`io_contract`) — explicit confirmation

The PRD calls these "typed I/O contract stubs." Confirmed shape: `io_contract` is **required on every
node**, containing **`input_schema`** and **`output_schema`**, each an embedded **JSON Schema draft
2020-12** document. P1's obligation is only the *stub*: emit a valid (possibly permissive) schema for
each. `{"type":"object"}` satisfies the contract. Because precision can be refined additively, P1
should emit the **most honest** shape it can cheaply infer and leave the rest permissive rather than
guess a strict-but-wrong schema.

---

## 3. Edge — the fields Discovery must populate

`$defs/Edge`, `additionalProperties: false`, all required:

| Field | Shape | Discovery obligation |
|---|---|---|
| `from_node_id` | string `minLength 1` | Must reference an existing node (CI referential check). |
| `to_node_id` | string `minLength 1` | Must reference an existing node. |
| `kind` | `enum ["data","control"]` | `data` = producer output feeds consumer input; `control` = a router/condition activates the target without passing data. **No other kinds exist** — an edge Discovery can't classify is still one of these two, or is not emitted. |

There is **no `evidence` field** on the frozen edge (the design sketch shows one — see §6, Finding A).

---

## 4. Static-node vs. runtime-invocation distinction (the invariant Discovery must populate)

This is a first-class concept in the frozen contract and the reason two schemas exist.

- A **node is a static definition** (`kind: "static_definition"`). Discovery emits **one node per
  static call site**, full stop.
- **Node count is per static definition:** `count ≡ len(nodes)`. The P0 scenario is explicit — *20
  static call sites, one an agent loop → reported node count is 20*, and the loop node is **not**
  represented as more than one node.
- A definition whose runtime LLM-call count is not statically fixed (loop, conditional agent) sets
  `invocation_semantics.variable_at_runtime = true` and picks `type: "loop"` or `"conditional"`.
  Discovery **never emits a fixed runtime invocation count** — that number is unknowable without
  executing the repo, which P1 must never do.
- **Runtime invocations are a different artifact entirely.** They live in
  [`runtime-invocation.schema.json`](../../schemas/runtime-invocation.schema.json)
  (`{invocation_id, node_id, run_id, invocation_index}`) and are produced by the **Runtime (P2) and
  dynamic tracing (P5)** — **not by Discovery.** P1 emits definitions only; it does not emit, and has
  no field for, invocation records.

**Confirmation:** Discovery populates the *definition* side of this split (`nodes` + the
`invocation_semantics` flag). The *invocation* side is out of P1 scope.

---

## 5. What Discovery does **not** emit (scope fences)

- **Runtime-invocation records** — Runtime/P5, separate schema.
- **`subgraphs` / `pattern_labels`** — P3.5 Pattern Classifier. P1 leaves them absent (valid).
- **A stricter `io_contract`** than static analysis honestly supports — permissive is correct; a
  confident-but-wrong schema is a defect (NFR4).
- **Any field not in the frozen schema** — `additionalProperties: false` rejects it (see §6).

---

## 6. ⚠️ Divergences between the P1 `design.md` node sketch and the frozen schema

These are the load-bearing findings of this confirmation pass. The P1
[`design.md`](../../openspec/changes/p1-discovery-mvp/design.md) "Data model" sketch lists node fields
that **are not in the frozen, `additionalProperties:false` schema**. They cannot be written onto an IR
node without failing schema validation. Each needs a deliberate decision in Design (§2), recorded here
so it is not resolved by accident at emit time.

**Finding A — Discovery-internal provenance fields have no home on the frozen node.**
The sketch shows `detected_by`, `ambiguity_flags`, `framework_source`, `upstream_dataflow`,
`prompt_construction`, and edge `evidence`. **None exist in the frozen schema**, and
`additionalProperties: false` will reject them.
*Resolution options (for §2 to decide):*
1. **Carry them in the discovery run report** (`discovery-report.json`, NFR6), keyed by `node_id`, not
   on the IR node. This keeps the IR contract frozen and is the default recommendation — the run
   report already owns "detections by source" and "ambiguity flags" per NFR6.
2. **Propose an additive P0 schema MINOR** adding these as *optional* node fields (allowed under the
   additive-only freeze policy; would need P0/System-Designer sign-off since it touches the frozen
   contract).
   Recommendation: **(1) for P1** — the run report is the honest home for provenance and ambiguity;
   revisit (2) only if a downstream consumer (P2/P3.5) needs provenance *inside* the IR.

**Finding B — the frozen schema has no first-class `unresolved` representation.**
Design decision D3 ("honest `unresolved`, never a guess") and FR3/FR8 require unresolved metadata to
be *marked, not omitted*. But the frozen node **requires** a fully-populated `model`
(`provider`/`model_id` are `minLength 1`) and **requires** exactly one of `prompt.inline` /
`prompt.template_ref`. There is no `unresolved: true` marker.
*Resolution (for §2 to freeze):* emit an explicit **sentinel** in the IR (e.g.
`model.provider = "unresolved"`, `model.model_id = "unresolved"`; `prompt.inline = ""` with the reason
recorded) **and** make the **discovery run report the authoritative record** of the unresolved field +
its P5-candidate reason. The IR stays schema-valid; the honesty lives in the report and the sentinel.
The sentinel convention must be a single documented constant so downstream consumers can detect it.

**Finding C — `context_assembly.policy` and `tools_skills` cannot be "empty by omission".**
`context_assembly` is required with a non-empty `policy`; `tools_skills` is required (but may be `[]`).
A plain call site still needs an explicit policy string (e.g. `"inline_messages"` / `"none"`), decided
in §2.6 alongside the run-report shape.

> These three findings are the concrete output of "confirm the contract": the frozen schema is
> **narrower** than the design sketch, and the gap is resolved in favor of keeping the IR contract
> frozen and pushing provenance/ambiguity into the run report. Design §2.1–§2.6 must ratify the
> sentinel convention (B), the run-report provenance keys (A), and the default policy strings (C).

---

## 7. Confirmation checklist (what §1.1 certifies)

- [x] Root envelope fields (`ir_version`, `workflow`, `nodes`, `edges`) and their sub-shapes are
  enumerated and assigned to Discovery.
- [x] Every **required** node field is listed with its frozen shape and Discovery's obligation.
- [x] **Edge** fields confirmed: `from_node_id`, `to_node_id`, `kind ∈ {data, control}` — and the
  design-sketch `evidence` field confirmed **absent**.
- [x] **Typed I/O contract stubs** confirmed: `io_contract.{input_schema,output_schema}`, required,
  permissive-allowed, additively refinable.
- [x] **Static-node vs. runtime-invocation** distinction confirmed: node = definition;
  count ≡ `len(nodes)`; `variable_at_runtime` flag; runtime records are a separate schema **not** emitted
  by Discovery.
- [x] Scope fences recorded (§5) and the three contract-vs-sketch divergences flagged for Design (§6).
