## Why

[P17](../p17-memory-strategy-optimization/) made memory a first-class Dimension and then refused to apply
it, naming exactly what was missing:

> *a memory runtime (a store, a lifetime, and a key scheme) plus the call-site rewriter that reads and
> writes it*

That refusal was the honest outcome for M20, and it is a poor place to stay. The `p17hermes` run states
the position without decoration: **186 (node × strategy) combinations, 186 typed refusals, 0 diffs.** A
memory strategy can be authored, hashed, compared, and handed to a colleague — and it reaches nothing.

The cost is not cosmetic. An axis that cannot be applied cannot be **verified**, and an axis that cannot be
verified cannot be **optimized** — which is the entire reason memory became a Dimension. `OpMemoryPolicy`
is catalogued and dormant for precisely this reason: diagnosis proposes, verification decides, and
verification has nothing to run.

Two things make the phase tractable now. [P16](../p16-context-strategy-optimization/) already solved the
shape — a dimension that is not an argument anywhere — by materializing what is a *transformation of an
expression the author already wrote* and refusing what would be a *construction*; memory's recall half is
exactly that shape. And P10's bound mode already emits a generated module plus a data document in the
**same patch** as a call-site edit, so the artifact needs no new delivery mechanism.

What is genuinely new is the **write** half. Recording a turn is not an expression replacement — it is a
statement that must run *after* the call. That is the edit class this phase adds, and it is why coverage
is narrower than the recall half alone would suggest.

## What Changes

- **New capability `memory-runtime`.** `internal/memoryruntime` ships the three artifacts P17's refusal
  named: a **`Store`** seam, a **key scheme** (`Key{NodeID, SessionID}`, both required — node-only leaks
  across conversations, session-only leaks across nodes), and a **count-based lifetime** (never a
  wall-clock TTL: a TTL makes recall depend on *when* it runs, so the same configuration returns different
  memory on a slow machine, and an unscorable axis cannot be optimized). All five builtin strategies get
  `Recall`/`Record` through **one dispatch**, deterministic and **bounded by construction** — an unbounded
  strategy is a memory leak in the customer's process. 🚫 The runtime **calls nothing**: summarization and
  embedding are injected host services, and a strategy needing one it was not given returns a **typed
  refusal** rather than falling back to a cheaper behaviour, because a `summary-buffer` that quietly
  truncates *is* `scratchpad` running under another strategy's hash.

- **New capability `memory-materialization`.** A generated, **dependency-free**, byte-identically
  regenerating memory module ships in the **same patch** as the call-site edit, carrying strategy and
  params **as data** so retuning is a document change (ADR-004's data/structure line). Two edit classes
  land: **recall** — replacing the written `messages=` argument with a call into the module, which is an
  expression replacement of something the author already wrote — and **record** — a statement following
  the call, gated on the call being a simple assignment at statement level. Python and Go first; every
  other language keeps its typed refusal and its own row.

- **🔴 The decision the phase turns on: BOTH HALVES OR REFUSE.** A cell materializes **only** when it can
  emit recall *and* record. A call site admitting one half is **refused whole**, naming which half is
  missing. Half a memory is not a weaker memory — recall-only reads from a store nothing fills and
  record-only fills a store nothing reads, so **both behave as `none`** while the `config_hash` claims
  `summary-buffer`. That is P17's *"scored a configuration that never ran"* failure one layer down, and it
  is **harder to see**: a diff genuinely was emitted, the build passes, and a reviewer sees real memory
  code.

- **The refusal is NARROWED, never removed.** `memoryCoverage` becomes a **per-cell** read of the
  materializer table: a covered (language, strategy, call-shape) cell reports `materializes`, and every
  other cell keeps its typed `unsafeRewrite` and its own cause. 🔴 The P17 **totality canary must still
  pass for every uncovered cell** — gaining a capability must not cost the guarantee that an
  unmaterializable override is refused rather than dropped. The refusal ladder keeps P16's ordering: the
  strategy, then the call shape (`**kwargs` — permanent and actionable), then the missing half, and only
  last the language.

- **`OpMemoryPolicy` wakes, partially.** On a covered cell a memory proposal now compiles to a real diff
  and becomes verifiable — the harness can finally answer whether `summary-buffer` beats `scratchpad` on a
  node. Everywhere else it stays **refused-not-scored**, unchanged. The honesty contract survives the
  capability.

- **The surfaces stop saying "no language has one".** The console boundary becomes per-cell, still read
  from the engine's coverage table. 🔴 P17's copy stating the limit is *language-independent* must
  **change**, because it is no longer true — a surface that kept it would be lying in the opposite
  direction, over-refusing instead of over-claiming. Both are the same defect.

- **Not changed here.** 🔴 **`config_hash` is untouched.** This phase changes what the transform *emits*,
  never what a configuration *is*: the hashed projection stays strategy + params, and the session id, the
  store, the lifetime bound, the artifact path, and the materialization status are all outside it. Every
  P17 hash reproduces bit-for-bit, and `none` still hashes as absent — which is what makes P17's promise
  (*it materializes unchanged once the rewriter lands*) literally true rather than aspirational. Also
  unchanged: no durable store (in-process, stated where a deployment sees it, never implied durable), no
  new metric (the classifier's existing `MemoryManagement` set becomes *measurable*, not larger), no
  languages beyond Python and Go, and no context work.

## Impact

- **Affected capabilities:** `memory-runtime` (new), `memory-materialization` (new). Modified:
  `memory-policy` (P17 — its refusal narrows per cell; its no-silent-drop guarantee is unchanged),
  `memory-authoring` (P17 — the boundary becomes per-cell). Consumed, not modified: `context-policy` (P16
  — the region-rewrite precedent and the list splitters), `variant-spec`/`config-hash` (P2),
  `proposal-catalog` (P4.5), `eval-harness` (P4).
- **Affected code/systems:** `internal/memoryruntime` (new), `internal/transform`
  (`memoryartifact.go`, `memorymaterialize.go`, `memorymaterialize_span.go`; `memoryrefuse.go` narrows;
  `coverage.go` becomes per-cell), `internal/registry` (the strategy interface gains its behaviour seam),
  `internal/proposal` (the operator wakes on covered cells), `internal/authoring` + `internal/api` +
  `web/console` (per-cell boundary). No new table, no migration, no hashed field.
- **Dependencies:** **P17** (the modeled axis and its frozen hash contract), **P16** (region rewrite, list
  splitters), **P10** (bound-mode artifact emission), **P2**, **P1**, **P4/P4.5**.
- **Unblocks:** scored memory optimization — the first phase in which a memory variant can enter the
  verified-delta ledger; and a durable-store phase, which becomes a delivery question rather than a design
  one because the semantics are store-agnostic by construction.
- **Breaking:** none. No hash moves, no schema changes, no migration. A P17 variant materializes without
  being re-authored.
- **Sequencing:** **18a** the runtime + the artifact (ships nothing to a call site, complete on its own).
  **18b** the Python/Go materializers + the per-cell coverage narrowing. **18c** the operator waking and
  the surfaces, then a real-repository run reporting what moved **and what did not**, with counts rather
  than claims.
