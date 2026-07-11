## Why

After P1, Discovery emits a static **Workflow IR** — nodes, edges, per-node call-site metadata —
but the IR is inert. To evaluate a workflow we must be able to *override* a node (its model,
prompt, tools, context strategy) and *execute* it. The original plan wrapped each call site in a
runtime shim that resolved parameters from a config store; ADR-001 supersedes that. A runtime shim
is infeasible for compiled languages (there is no monkey-patch seam in a Go binary — and Go is the
P1 discovery target) and it measures the wrong thing, because a shimmed run does not exercise the
code that will actually ship. Nothing downstream can proceed until a workflow actually runs under
our control: the metrics substrate (P2.5) has no run to instrument, and the eval harness (P4) has
no variant to score.

P2 introduces a **Configuration Layer** that realizes a **Variant Spec** by generating a
**deterministic AST-level source transformation (codemod)** that rewrites the discovered call sites
— the model argument, the prompt construction, the tools/skills passed, the context assembly — to
match the spec. The transform is applied to an **isolated working copy** (git worktree/branch),
built, and delivered as a **reviewable diff/PR**; it is never applied to the user's working tree in
place. The **Runtime** then executes the *transformed working copy* in a sandbox against live
providers, so every measurement reflects the code that would actually ship. Four versioned
registries (model / prompt / skill / context) hold reusable definitions referenced by ID. This is
the first phase that runs real provider calls, so idempotency and reproducibility (keyed by
`config_hash` + `seed`) are first-class, tested requirements: a retry must not double-charge, and
the same `config_hash` + seed must replay reproducibly.

Depends on P0 (`workflow-ir.schema.json` with the typed per-node I/O contract field and the
`config_hash` scheme; `metric-event.schema.json`) and P1 (a valid static IR). The input graph is
hardcoded/hand-authored for this phase; building Variant Specs from arbitrary discovered repos at
scale is out of scope.

## What Changes

- **New capability `config-layer`.** The system realizes a Variant Spec by **generating a
  deterministic AST transformation** that rewrites each node's four override dimensions (model,
  prompt, skills, context) at its discovered call site — rewriting the hardcoded parameters to the
  Variant Spec's values. Each dimension is independently overridable, and an absent override leaves
  the discovered call site unchanged for that dimension. Defines the **Variant Spec** structure
  (`{node_id → {model_ref, prompt_ref, skill_refs[], context_policy}}` + node ordering) as the
  canonical desired-state config and the stable `config_hash` derived from it. Transforms are
  **deterministic** (same `config_hash` + same source → byte-identical diff), **build-preserving**
  (a transform that fails to build the target is rejected before it is proposed),
  **behavior-preserving except for the intended change**, applied to an **isolated worktree**, and
  **always delivered as a reviewable diff**. Fails closed on a Variant Spec referencing a missing
  node, unresolved `*_ref`, unregistered context policy, or a call site the transform cannot
  rewrite safely.
- **New capability `registries`.** Four registries (model / prompt / skill / context) that are
  versioned git-like, immutable per published version, content-addressed, and referenced by ID.
  Prompt entries are templates with named variable slots that render deterministically. Skill
  entries carry a **JSON-schema contract** (inputs + outputs) validated before the transform binds
  them at the call site. Model entries capture provider + id + inference params (incl. seed,
  thinking budget) as a versioned unit. Evolution is additive/expand-contract so older Variant
  Specs keep resolving.
- **New capability `runtime`.** A **Loader** resolves every `*_ref` and fails closed on dangling
  references before any transform is generated. A **Transform Engine** generates the AST codemod,
  applies it to an isolated git worktree, and runs the build; a transform that does not build is
  rejected and never surfaced. A **provider gateway** (LiteLLM-style, unaffected by this change)
  makes provider swaps transparent (only `model_ref` changes), normalizes request/response shapes,
  applies per-call timeouts + bounded backoff, and sources secrets from a manager — never from the
  spec, code, or logs. An **Executor** builds and **runs the transformed working copy in a
  sandbox**, walking the node graph in the declared ordering, passing node I/O through the typed
  contract and halting on violations. Runs are **reproducible** (`config_hash` + seed → identical
  diff, build, and seed propagation) and **idempotent** (retry does not double-charge or
  double-write). Rollback of any applied change is a clean **`git revert`**.
- **Storage.** Variant Specs + registries in Postgres (unique `config_hash`, FKs run → variant/
  node, unique `(run_id, node_id, attempt_group)`); prompt/artifact/IO blobs and generated
  **diffs/patches** content-hashed in the object store; isolated worktrees + build artifacts held
  in a pooled, per-`config_hash` build cache.
- **Minimal UI.** A bare run/inspect view (submit spec → review the generated diff → watch the
  transformed copy run → per-node I/O) with loading / error / empty states first-class.
- Context **policy implementations** beyond `full` are **deferred to P3** (the config field,
  selection, and pluggable interface land here; only `full` is implemented). Sandboxed tool
  execution is **P3**; the OTel metrics substrate is **P2.5**; re-arrangement UI is **P5**;
  autonomous PR merge without human approval is **P6**.

## Impact

- **Affected capabilities:** `config-layer` (new), `registries` (new), `runtime` (new). Consumes
  the `workflow-ir` and `config-hash`/tagging contracts from P0.
- **Affected code/systems:** new Config Layer transform engine (AST codemod generator + worktree
  applier), four registry services + stores, Runtime loader/executor, provider gateway, Postgres
  schema (variant specs, registries, run/node records), object store (content-hashed blobs +
  diffs), worktree pool + build cache, run queue (seed), a minimal React run/review/inspect UI,
  secrets manager integration.
- **Dependencies:** requires **P0** (frozen IR + metric-event schemas, `config_hash` scheme,
  per-node call-site anchors the codemod targets) and **P1** (valid static IR). Unblocks **P2.5**
  (instruments the gateway + build/run path), **P3** (context policies + sandbox plug into the
  policy interface and the transform), **P4** (eval harness submits Variant Specs and scores the
  transformed runs), **P5.5** (proposals are Variant Specs the runtime transforms, builds, and
  runs; surfaced as PRs).
