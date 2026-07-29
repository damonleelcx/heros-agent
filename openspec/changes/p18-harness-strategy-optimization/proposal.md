## Why

Every optimization axis the platform ships tunes what happens **inside one model call** — model, prompt,
skills, context (`internal/variantspec/spec.go:42-47`). None can change **how many calls a node makes or
in what control loop**. A node that issues a single request could, for the same job, run a reason-and-act
tool loop, plan-then-execute, or generate-and-critique with a second model — and which *scaffold* wraps
the call is frequently the difference between a right answer and a wrong one. The platform cannot express
that choice, cannot resolve it, cannot hash it, and cannot propose it.

The absence is total and honest: **there is no agent-harness, agent-loop, or scaffold concept anywhere in
the IR or the optimizer.** Every "harness" symbol in the tree belongs to the *eval* harness
(`internal/evalharness`), which is unrelated. The only trace of an agent scaffold is a comment describing
what *target* codebases contain — `internal/irwriteback/recover.go:11` names "a ReAct loop, a script of
independent LLM calls" as a shape the platform **discovers**, never one it **models**. The prior runtime
harness (leader/follower/critic topology, ~1898 LOC) was removed in the migration and is **not** carried
forward; only its salvageable idea — a generator paired with a separate critic — survives here as one
catalog entry (`critic-loop`).

The consequence is a gap the optimizer can *diagnose but never fix*. A single-shot node failing a
multi-step case is a diagnosis the platform can already form. What it lacks is a Dimension to express the
scaffold, a registry to version it, a hashed field to make it identity-bearing, and an operator to
propose a swap. This change adds all four by walking the repo's canonical eight-step "add an axis"
checklist — and, because materializing a control loop at a call site is a **structural source rewrite
that is not yet safe**, it ships the axis at the same honesty level as `skills`/`context`: fully
modelled, resolved, and hashed, but **refused at transform** with a typed `unsafeRewrite`
(mirroring `refuseSkills`, `internal/transform/rewrite.go:388`), never silently dropped.

## What Changes

- **New capability `harness-strategy`.** A new versioned registry Kind `harness`, sealed and decoded by
  content address exactly as `model`/`prompt`/`skill`/`context` (`internal/registry/registry.go:57`), so
  a `harness` `version_id` is unique across all registries and a ref pasted into the wrong dimension fails
  closed. The builtin catalog enumerates **five** strategies — **`single-shot`** (one call, today's
  implicit default made explicit), **`react-loop`** (reason+act tool loop), **`plan-execute`** (plan then
  execute steps), **`reflexion`** (self-critique + retry), **`critic-loop`** (generator + a *separate*
  critic model — the salvaged pattern from the removed harness). Each declares a **params schema**:
  `max_turns` (a bounded turn ceiling), a stop condition, an optional critic model ref, and a retry
  budget. Params inapplicable to a strategy are **inexpressible** for it, not silently ignored, and are
  validated at seal (`max_turns` bounded positive, a declared critic ref resolves to a `model` entry, the
  retry budget bounded). A `HarnessRef` is an immutable `version_id` and nothing else — an inlined
  strategy definition is rejected, mirroring the ref-only rule for the existing dimensions.

- **New capability `agent-loop`.** A new closed `Dimension` const `DimHarness`
  (`internal/variantspec/spec.go:42`); an additive `omitempty` `NodeOverride.HarnessRef` with matching
  `isEmpty`/`Refs`/`Validate` (`spec.go:183`); a `DimHarness` block in `resolveNode` and a `Dimensions()`
  entry (`internal/variantspec/resolve.go:67,154`); an **auto-hashed** additive `ResolvedNode.HarnessRef`
  that is `omitempty` and nil-when-empty so a no-harness node emits **no** harness key and hashes
  byte-identically to a pre-P18 node (the D-1.4 expand-contract rule); and an **additive** IR field
  recording a node's discovered default harness (`internal/discovery/emit.go:92` + a discovery frontend),
  defaulting to `single-shot`. Because `config_hash` is purely structural, the axis is **scored by the
  existing axis-agnostic eval harness with no new metric and no scoring change**
  (`internal/evalharness/evaluator.go`). A harness may wrap a **single node** or an **ordered edge set**
  (a node-group / subgraph); the group form **composes with P15's wiring** (`VariantSpec.Order`/`Edges`,
  `spec.go:255-258`) — the wiring defines the edges, the harness defines the loop over them — and a
  harness override never reorders. A node or node-group carrying a `HarnessRef` is **refused at transform**
  with a typed `unsafeRewrite` naming the strategy and the reason, on both engines, **never silently
  dropped**. The proposal catalog gains `OpHarnessStrategy` (`operator.go:34`, `catalog.go:18`,
  `gain.go:8`), routed through P5.5 verification, with an **admissibility gate**: a heavier harness (more
  turns) is admitted only when the measured `task_success` gain outweighs its added `eval_cost_usd` and
  `eval_latency_ms` on **held-out** data. Autonomous turns run within the node's **existing** P3 sandbox
  and tool grant, adding no egress/tool scope; the enlarged turn surface is **observable** in the trace.

- **Not changed here.** No runtime topology engine is resurrected (the removed leader/follower/critic
  runtime stays removed; only `critic-loop`'s pattern survives, as data). The **control-loop codemod is
  not realized** — call-site materialization of a bounded loop is the named work behind the interim
  refusal, deferred to the phase that proves the rewrite safe, exactly as `refuseContext` defers to P3.
  **No node-graph wiring** is defined, inserted, or validated here — that is P15. **No eval metric and no
  scoring change** — the axis rides `task_success`/`eval_cost_usd`/`eval_latency_ms`
  (`internal/evalharness/metricnames.go`) unchanged; a bespoke harness-quality metric, if ever needed, is
  an additive `RegisterMetric` (`registry.go:86`), not this phase.

## Impact

- **Affected capabilities:** `harness-strategy` (new), `agent-loop` (new). Consumed, not modified:
  `workflow-ir` (P0/P1), `config-layer`/`variantspec` (P2), `registry` (P2), `transform` (P2 — the
  refusal path is extended), `eval-harness`/`scoring` (P4 — unchanged), `proposals`/`verification` (P5.5),
  `node-graph-wiring` (P15 — composed with).
- **Affected code/systems:** the eight canonical add-an-axis sites — `internal/variantspec/spec.go`
  (Dimension + NodeOverride), `resolve.go` (resolveNode + Dimensions), `resolved.go` (ResolvedNode field),
  `internal/registry/registry.go` (new Kind + a `harness_entry` table and register/resolve file),
  `internal/discovery/emit.go`+`extract.go` (additive IR field + frontend), `internal/transform/rewrite.go`
  +`rewrite_span.go` (the refusing rewriter), `internal/proposal/operator.go`+`catalog.go`+`gain.go`
  (operator kind, catalog row, prior). One new DB table (`harness_entry`).
- **Dependencies:** requires **P0** (`config_hash` contract + golden vectors), **P1** (IR/`IRNode`), **P2**
  (resolution + transform), **P3** (sandbox/grant the turns run in), **P3.5** (pattern labels),
  **P4** (axis-agnostic harness + metrics), **P5.5** (proposal/verification spine), **P15** (wiring a group
  harness composes with).
- **Unblocks:** the optimizer can propose **scaffold** changes, not only in-call ones; and a later phase
  that proves the control-loop codemod safe inherits a fully modelled, hashed, and refused axis to turn on
  — the refusal is the seam it lands in.
- **Breaking:** none. Every field is additive and `omitempty`; a configuration declaring no harness hashes
  byte-identically to its pre-P18 form and every existing golden vector reproduces.
- **Sequencing:** **18a** (the catalog + the dimension: modelled, resolved, hashed, refused, scored) is a
  complete phase on its own. **18b** (the operator + the cost/quality admissibility gate) follows.

---

## Addendum — the runtime, the rewriter, and the authored change

### Why (addendum)

Two requirements arrived after the axis was modelled, and both are load-bearing rather than cosmetic.

**A user must be able to make an active change to their harness strategy.** As written above, the only way
a scaffold ever changes is `OpHarnessStrategy` proposing one. That makes the axis operator-only, which is
not what the other axes are: model, prompt, skills, context, wiring and memory all bind the cross-axis
[`authored-change`](../../specs/authored-change/spec.md) contract, and harness must too, through the same
override and with **no second apply path**.

**The refusal named two missing artifacts, and this change builds them.** Decision 4 refuses a `HarnessRef`
at transform because *"materializing a control loop is code generation."* That sentence names exactly what
is missing: **a harness runtime** — a bounded loop, a stop condition and a continuation rule — and **the
call-site rewriter** that drives it. Without them the axis can be authored, hashed and compared, but can
never reach a customer's source, and the interim refusal is indefinite rather than interim.

### What Changes (addendum)

- **New capability `harness-runtime`.** `internal/harnessruntime`: one definition of each builtin
  strategy's loop — `Plan(strategy, params, turn, answer) → continue|stop(reason)` — plus a `Run` that is
  **bounded by construction** (no strategy and no param combination expresses an unbounded loop), reaching
  the ceiling terminates and is **recorded**, and the result exposes a per-turn trace that is
  **observable and never hashed**. 🚫 The runtime performs **no provider call and dispatches no tool**: a
  planner, a tool executor and a critic are **injected host services**, and a strategy whose service is
  absent **refuses** rather than substituting a lighter loop.

- **New capability `harness-materialization`.** A dependency-free generated module per language that
  regenerates byte-identically and ships in the **same patch** as the call-site edit, plus the rewriters
  that drive it. The one decision it turns on is **DRIVE AND DECIDE, or refuse**: a cell materializes only
  when the runtime can both re-invoke the call **and** evaluate the stop condition against the response;
  otherwise the call site is refused **whole**, naming the missing half. Consequences, stated rather than
  hidden: `single-shot` is the identity and materializes everywhere; `reflexion` materializes where a
  response's text is readable (Python — a message is a `dict`) and refuses where it is the customer's SDK
  type (Go — `CauseNotAtCallSite`, **no missing artifact**, because there is nothing to build); and
  `react-loop`, `plan-execute` and `critic-loop` refuse at **every** call site, each naming the host
  service a generated module may not supply. Decision 4's refusal is therefore **narrowed per cell, never
  removed** — `harnessCoverage` becomes a read of the materializer table, and the totality canary still
  passes for every uncovered cell.

- **New capability `harness-authoring`.** P18's binding of the `authored-change` spine: select a strategy
  from the closed builtin set, supply schema-valid params, clear it (clearing reproduces the prior
  `config_hash` byte-identically; `single-shot` with no params ≡ cleared). The **per-cell** boundary is
  stated **before** the choice, read from the engine's own coverage source rather than a second sentence,
  and the added cost of a heavier scaffold is stated rather than implied. An authored change is stamped
  `unverified`, claims nothing, and where the cell refuses is surfaced as **refused-not-scored**. A new
  `/app/harness` console surface renders it.

- **Still not changed.** `config_hash` is untouched — this addendum changes what the transform **emits**
  and what a surface **offers**, never what a configuration **is**, so every hash minted under 18a
  reproduces bit-for-bit. No runtime topology engine is resurrected. No eval metric and no scoring change.
  No harness override reorders nodes.

### Impact (addendum)

- **Affected capabilities:** `harness-runtime` (new), `harness-materialization` (new), `harness-authoring`
  (new). `agent-loop`'s refusal requirement is **modified** — narrowed per cell, not removed.
- **Affected code/systems:** `internal/harnessruntime` (new), `internal/transform`
  (`harnessartifact*.go`, `harnessmaterialize*.go`, `coverage.go`), `internal/registry/authoring.go`
  (the validate-without-register path the surface calls), `web/console` (`/app/harness`).
- **Breaking:** none. Additive throughout; the refusal narrows, so nothing that worked stops working.
