# P1 Discovery — Invariants that must never break

> **Task:** P1 `tasks.md` §1.3 — *Write down the invariants that must never break: no execution of
> the repo; no fixed count for a variable node; stable node IDs; every missing node explainable from
> the run report.*
> **Workstream:** ① Understand & Explore (Backend lead).
> **Purpose:** these are the **test assertions and the review checklist** for all of P1. An invariant
> is not a preference — it is a property that, if violated, fails the build. Each entry below states
> the invariant, *why* it is load-bearing, its **decided failure behavior**, and **how it is
> enforced** (a test that can go red). Arbitration when two goals collide follows the core law:
> **安全 > 稳定 > UX > 运维 > 不可演进 > 不可扩展 > 维护 > 实现**
> ([`shared/00-核心法则`](../../../aikeylabs-skills/shared/00-核心法则.md)).

The four named invariants are **I1–I4**. I5–I8 are the supporting invariants without which I1–I4
cannot actually hold (honest `unresolved`, additive-only IR, skip-and-report robustness, least
privilege). All eight are freeze-grade for P1.

---

## I1 — Discovery NEVER executes the target repo *(security invariant — highest priority)*

**Statement.** The discovery path SHALL NOT execute, evaluate, `go run`, `go build` with plugins,
`go test`, import the target as a plugin, spawn a subprocess from target code, or otherwise run any
target-repo code — at any point, for any reason. Analysis is over `go/ast` + text only. Discovered
source is **untrusted**.

**Why load-bearing.** This is the top of the core law (安全). Executing untrusted source is arbitrary
code execution on the discovery worker. No convenience, fidelity gain, or "it would resolve the
prompt if we just ran it" ever justifies crossing it — a higher-tier risk can never be traded for a
lower-tier convenience (L1).

**Failure behavior.** There is no degraded mode. Any code path that would run target code is a defect,
not a fallback. A value that could only be resolved by execution is resolved as `unresolved` (I5),
never by running anything.

**Enforcement (must be able to go red):**
- **Structural:** the discovery package imports nothing that spawns processes or loads plugins
  (`os/exec`, `plugin`, `go/build` invocation of the toolchain) in the analysis path. A grep/CI guard
  asserts their absence in the discovery path.
- **Behavioral test (P1 §7.1):** run discovery over a fixture whose package has an `init()` with an
  observable side effect (writes a sentinel file / increments a counter); assert the side effect
  **never fires**. Run the worker with process-spawn denied (sandbox) and assert discovery still
  completes.
- **Review:** any new import into the discovery path that can execute code blocks the PR.

---

## I2 — A variable node is NEVER given a fixed runtime count *(stability / contract invariant)*

**Statement.** Discovery reports node count **per static definition** (`count ≡ len(nodes)`). A node
whose runtime LLM-call count is not statically fixed (loop, agent, conditional) SHALL set
`invocation_semantics.variable_at_runtime = true` and SHALL NOT be expanded into multiple nodes or
annotated with any inferred/estimated invocation count.

**Why load-bearing.** The static-vs-runtime split is a frozen P0 contract (
[`workflow-ir` spec](../../openspec/changes/p0-foundations/specs/workflow-ir/spec.md)). A guessed
count is a confident lie: it corrupts every downstream consumer that keys on `node_id` (Metrics,
Eval, Attribution) and misleads the user into thinking an agent loop is one call. The true count is
unknowable without executing the repo — which I1 forbids. The honest artifact is the **flag**; P5
dynamic tracing supplies the real count from traces.

**Failure behavior.** Cannot determine the count statically → `variable_at_runtime = true`, `type ∈
{loop, conditional}`, **no** number emitted. Never estimate an iteration count.

**Enforcement:**
- **Loop/agent fixture (P1 §6.4):** a fixture with a known loop asserts the node has
  `variable_at_runtime = true`, `type = "loop"`, and that **no** fixed runtime count appears anywhere
  in the emitted IR.
- **Count test (P0 scenario):** the "20 call sites, one a loop → 20 nodes" property — node count equals
  the number of static call sites; the loop is exactly one node.
- **Review:** any code that multiplies a node by an inferred iteration factor blocks the PR.

---

## I3 — Node IDs are STABLE and deterministic *(evolvability / diffability invariant)*

**Statement.** `node_id` SHALL be derived from a **content-addressed, stable tuple** (package path +
enclosing symbol + call-site identity + a content hash), never from wall-clock, map-iteration order,
sequential counters, or line number alone. Two discovery runs over the **same `commit_sha`** with no
source change SHALL produce **byte-identical** IR after canonicalization (same nodes, same `node_id`s,
same edges, same ordering).

**Why load-bearing.** Diffability is the M1 exit criterion and the whole product premise ("you review a
diff and merge"). Non-deterministic IDs destroy diffing, break lineage keyed on `node_id`, and make
every re-run look like a change. IDs must also survive **benign refactors** (line shifts, reformatting)
so a cosmetic edit doesn't reshuffle the graph — hence line number alone is disqualified, and
`call_site.ast_path` exists to anchor rewrites across formatting changes.

**Failure behavior.** Output is **sorted deterministically** before emission (nodes and edges by a
stable key). No reliance on Go map iteration order anywhere in the emit path.

**Enforcement:**
- **Golden-IR diff test (P1 §6.7 / CI §8.2):** discover a fixture twice; assert byte-identical output.
  Commit the golden IR; CI fails on drift.
- **Determinism guard:** the emitter sorts; a test shuffles input file order / map seeding and asserts
  identical `node_id`s and identical serialization.
- **Refactor-stability check (design §2.3, PRD Q4):** a line-shift-only edit to a fixture must not
  change `node_id`s (the content-addressed tuple excludes absolute line numbers from the identity, or
  hashes structure not position).

---

## I4 — Every missing node is EXPLAINABLE from the run report *(observability invariant)*

**Statement.** Discovery SHALL emit a structured **run report** (`discovery-report.json`, NFR6)
recording: files scanned, per-file parse diagnostics, call sites detected **by source** (registry /
declared / framework), nodes emitted, dedup merges, and every ambiguity/`unresolved` flag with its
reason. It SHALL be possible to explain **why any expected node is absent** — parse-skipped file,
no matching signature and no declaration, deduped into another node, or degraded framework subgraph —
purely from the report, without re-running or reading Discovery's source.

**Why load-bearing.** Wrapper coverage depends on the user authoring `llm-eval.yaml` (§1.2). When a
node is missing, the *only* way the user knows whether it's a bug or an un-declared wrapper is the run
report's detection-by-source counts. A silent drop is the single worst failure mode of a discovery
tool: it looks like success. The report is what turns "a node vanished" from a mystery into a lookup.

**Failure behavior.** A node is **never silently dropped**. Every skip, merge, degrade, and unresolved
field produces a report entry. Absence of evidence in the report for a missing node is itself a defect.

**Enforcement:**
- **Malformed-file fixture (P1 §6.5):** a syntactically broken file is skipped, **other files still
  discovered**, and the report contains a per-file diagnostic for the skipped file.
- **Wrapper fixture (P1 §6.2):** removing the `llm-eval.yaml` declaration makes the wrapper node
  disappear **and** the report's declared-source count drops correspondingly — the disappearance is
  explained, not mysterious.
- **Dedup fixture (P1 §6.6):** a call site hit by both registry and declaration is **one** node, and
  the report records the merge (both sources credited, one node emitted).
- **Report completeness test:** `report.nodes_emitted == len(ir.nodes)`; every `unresolved` field on a
  node has a matching flagged entry in the report.

---

## I5 — `unresolved` is marked, never guessed and never omitted *(faithfulness — supports I2, I4)*

**Statement.** Any metadata field Discovery cannot resolve statically (model bound at
construction/env, inter-procedurally-assembled prompt, opaque serialized body, interface-selected
impl — see §1.2 catalog W3/W4/W7/W8/W9/W12) SHALL be emitted as an explicit **sentinel `unresolved`
value** (the frozen schema has no `unresolved` marker — see [contract confirmation §6 Finding B](01-ir-contract-confirmation.md#6--divergences-between-the-p1-designmd-node-sketch-and-the-frozen-schema))
and SHALL be **flagged in the run report** as a P5 dynamic-trace candidate **with a machine-readable
reason**. It SHALL NEVER be silently omitted and NEVER filled with a best-effort guess.

**Why load-bearing.** A confident-but-wrong prompt/model misleads P2 overrides and P4 attribution far
worse than an honest gap — and the honest gap is exactly the input P5 consumes. This is the same
"diagnosis proposes, verification decides" discipline applied to static analysis: never trust an
unverified inference (design D3, FR3, FR8).

**Enforcement:** the `unresolved` sentinel is a **single documented constant** (ratified in design
§2.x) so downstream consumers can detect it; a test asserts every unresolved field has a report flag
with a reason; a review rule blocks any "probable value" heuristic that fills a field it cannot prove.

---

## I6 — IR emission is additive-only against the frozen P0 schema *(evolvability — supports the contract)*

**Statement.** Discovery SHALL populate only fields defined in the frozen `workflow-ir.schema.json`
(which is `additionalProperties: false` at every level) and SHALL emit IR that validates against it.
Discovery-internal provenance (`detected_by`, `ambiguity_flags`, `framework_source`, dataflow
evidence) does **not** go on the IR node — it goes in the **run report** (see [contract confirmation §6 Finding A](01-ir-contract-confirmation.md#6--divergences-between-the-p1-designmd-node-sketch-and-the-frozen-schema)).
Extending the IR requires an **additive P0 schema change** (optional field, MINOR bump), never an
ad-hoc field slipped in by Discovery.

**Why load-bearing.** The IR is a one-way-door public contract that outlives this code (不可演进 tier).
An undeclared field either fails validation (best case) or silently forks the contract (worst case).
Provenance belongs in the report, which is Discovery's own artifact and free to evolve.

**Enforcement:** the schema-validation CI gate (§8.1) validates every emitted IR against the frozen
schema and fails the build on any violation — including an unknown field. A test asserts Discovery
emits nothing outside the schema.

---

## I7 — Hostile/broken input degrades to skip-and-report, never a crash *(stability — supports I4)*

**Statement.** Malformed, syntactically invalid, or adversarial source (deeply nested expressions,
huge literals, symlink cycles, cyclic imports) SHALL be handled by **skip-and-report** with bounded
recursion and bounded resource use. A parse error degrades to a **per-file diagnostic**; discovery of
every other file continues. Discovery SHALL NOT crash, panic-unwind out of the run, or consume
unbounded CPU/memory on any single input.

**Why load-bearing.** A crash on one hostile file drops **all** nodes from that run — the silent-drop
failure I4 exists to prevent, at whole-run scale. Bounded resource use is also part of the untrusted-
input security posture (a huge-literal DoS is an attack).

**Enforcement:** malformed-file fixture (§6.5); a robustness test with deep nesting / huge literal /
symlink cycle asserts bounded time+memory and a clean per-file diagnostic; expression-walk recursion is
depth-bounded with the over-budget case flagged `unresolved` (I5), not panicked.

---

## I8 — The discovery worker runs least-privilege *(security — supports I1)*

**Statement.** The discovery worker SHALL run with **read-only** filesystem access to the target repo,
**no network egress**, and **no ambient provider credentials**. This is the observable, external proof
of the no-execution invariant (I1): a worker that cannot write, cannot reach the network, and holds no
keys cannot exfiltrate or execute even if a bug tried to.

**Why load-bearing.** Defense in depth for 安全. I1 is enforced in code; I8 makes the boundary hold
even against a code-level mistake, and is externally auditable (the mount/network/creds posture is
inspectable without reading source).

**Enforcement (P1 §7.2 / DevOps):** the CI/worker config mounts the repo read-only, denies egress, and
injects no provider keys; a test asserts a write attempt / network dial / key read fails.

---

## Invariant → enforcement map (the P1 review checklist)

| Invariant | Named in §1.3? | Primary test (P1 tasks) | Core-law tier |
|---|---|---|---|
| **I1** no execution of the repo | ✅ | §7.1 `init()` side-effect never fires; spawn-denied run | 1 安全 |
| **I2** no fixed count for a variable node | ✅ | §6.4 loop fixture; §P0 20→20 count | 2 稳定 |
| **I3** stable, deterministic node IDs | ✅ | §6.7 golden-IR byte-diff; refactor-stability | 5 演进 |
| **I4** every missing node explainable from the report | ✅ | §6.5 malformed; §6.2 wrapper; §6.6 dedup | 4 运维 |
| **I5** honest `unresolved`, never a guess | (supports I2/I4) | unresolved→report-flag test | 稳定/演进 |
| **I6** additive-only IR emission | (supports contract) | §8.1 schema-validation gate | 5 演进 |
| **I7** skip-and-report on hostile input | (supports I4) | §6.5 + robustness fixture | 2 稳定 |
| **I8** least-privilege worker | (supports I1) | §7.2 read-only / no-net / no-creds | 1 安全 |

> **Adversarial self-review (P1 §7.4) hunts exactly four classic violations:** a silently-dropped node
> (I4), a variable node given a fixed count (I2), non-deterministic IDs (I3), and an unhandled parse
> panic (I7) — plus any code path that could execute target code (I1). If none of those can be made to
> go red in the test suite, the invariants are not yet enforced.
