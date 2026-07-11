## Why

After P1, Discovery emits a static **Workflow IR** — nodes, edges, per-node call-site metadata —
but the IR is inert. To evaluate a workflow we must be able to *override* a node (its model,
prompt, tools, context strategy) and *execute* it, and we cannot safely rewrite arbitrary
third-party source to do so. Nothing downstream can proceed until a workflow actually runs under
our control: the metrics substrate (P2.5) has no run to instrument, and the eval harness (P4) has
no variant to score.

P2 introduces a **Configuration Layer** — a shim that wraps each discovered call site so its
parameters resolve from a config store at runtime instead of from hardcoded literals — plus the
**Runtime** that resolves, binds, and executes a **Variant Spec** against live providers. Four
versioned registries (model / prompt / skill / context) hold reusable definitions referenced by
ID. This is the first phase that runs real provider calls, so idempotency and reproducibility
(keyed by `config_hash` + `seed`) are first-class, tested requirements: a retry must not
double-charge, and the same `config_hash` + seed must replay reproducibly.

Depends on P0 (`workflow-ir.schema.json` with the typed per-node I/O contract field and the
`config_hash` scheme; `metric-event.schema.json`) and P1 (a valid static IR). The input graph is
hardcoded/hand-authored for this phase; building Variant Specs from arbitrary discovered repos at
scale is out of scope.

## What Changes

- **New capability `config-layer`.** A shim resolves each node's four override dimensions (model,
  prompt, skills, context) from a Variant Spec + registries at invocation time; each dimension is
  independently overridable, and an absent override falls back to the IR-captured default. Defines
  the **Variant Spec** structure (`{node_id → {model_ref, prompt_ref, skill_refs[],
  context_policy}}` + node ordering) and the stable `config_hash` derived from it. Fails closed on
  a Variant Spec referencing a missing node, unresolved `*_ref`, or unregistered context policy.
- **New capability `registries`.** Four registries (model / prompt / skill / context) that are
  versioned git-like, immutable per published version, content-addressed, and referenced by ID.
  Prompt entries are templates with named variable slots that render deterministically. Skill
  entries carry a **JSON-schema contract** (inputs + outputs) validated before binding. Model
  entries capture provider + id + inference params (incl. seed, thinking budget) as a versioned
  unit. Evolution is additive/expand-contract so older Variant Specs keep resolving.
- **New capability `runtime`.** A **Loader** resolves every `*_ref` at invocation time and fails
  closed on dangling references. A **provider gateway** (LiteLLM-style) makes provider swaps
  transparent (only `model_ref` changes), normalizes request/response shapes, applies per-call
  timeouts + bounded backoff, and sources secrets from a manager — never from the spec, code, or
  logs. An **Executor** walks the node graph in the declared ordering through the shim, passing
  node I/O through the typed contract and halting on violations. Runs are **reproducible**
  (`config_hash` + seed → identical resolution + seed propagation) and **idempotent** (retry does
  not double-charge or double-write).
- **Storage.** Variant Specs + registries in Postgres (unique `config_hash`, FKs run → variant/
  node, unique `(run_id, node_id, attempt_group)`); prompt/artifact/IO blobs content-hashed in the
  object store.
- **Minimal UI.** A bare run/inspect view (submit spec → watch run → per-node I/O) with
  loading / error / empty states first-class.
- Context **policy implementations** beyond `full` are **deferred to P3** (the config field,
  selection, and pluggable interface land here; only `full` is implemented). Sandboxed tool
  execution is **P3**; the OTel metrics substrate is **P2.5**; re-arrangement UI is **P5**.

## Impact

- **Affected capabilities:** `config-layer` (new), `registries` (new), `runtime` (new). Consumes
  the `workflow-ir` and `config-hash`/tagging contracts from P0.
- **Affected code/systems:** new Config Layer shim, four registry services + stores, Runtime
  loader/executor, provider gateway, Postgres schema (variant specs, registries, run/node
  records), object store (content-hashed blobs), run queue (seed), a minimal React run/inspect UI,
  secrets manager integration.
- **Dependencies:** requires **P0** (frozen IR + metric-event schemas, `config_hash` scheme) and
  **P1** (valid static IR). Unblocks **P2.5** (instruments the shim/gateway), **P3** (context
  policies + sandbox plug into the policy interface and shim), **P4** (eval harness submits Variant
  Specs), **P5.5** (proposals are Variant Specs the runtime executes).
