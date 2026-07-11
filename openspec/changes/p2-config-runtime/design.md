# Design — P2: Configuration Layer + Runtime

Cross-reference: product rationale in [`../../../docs/prd/P2-config-runtime.md`](../../../docs/prd/P2-config-runtime.md).
Supersedes the runtime-shim mechanism per [ADR-001](../../../docs/adr/ADR-001-source-transformation-apply-model.md).

## Context

P2 turns the inert static IR from P1 into something runnable. The original plan proposed a runtime
shim that wrapped discovered call sites so their parameters resolved from a config store at
invocation time, on the premise that "we cannot safely edit arbitrary third-party code." ADR-001
rejects that premise and reverses the mechanism: **the system applies a configuration by
transforming the user's source code and delivering the change as a reviewable diff/PR.** Everything
else follows from two backend realities the platform lives or dies on: **contracts outlive code**
(the Variant Spec and registry shapes are consumed by P2.5/P4/P5.5) and **partial failure is
normal** (transforms fail to build, provider calls fail mid-graph and get retried). This is also
the first phase that spends real money on provider calls, which is why idempotency and
reproducibility are designed in, not bolted on.

## Decision 1 — Realize a Variant Spec as a deterministic AST source transformation, not a runtime shim

**Decision.** The Variant Spec remains the canonical desired-state config. "Applying" it means the
Transform Engine **generates a deterministic, AST-level codemod** that rewrites the discovered call
sites — the model argument, the prompt construction, the tools/skills passed, the context assembly,
or the node wiring — so the hardcoded parameters at each call site match the Variant Spec's values.
The transform is applied to an isolated working copy and delivered as a reviewable diff/PR; the
Runtime executes the *transformed* copy.

**Why.** Two fatal flaws sank the runtime shim (ADR-001 §Context):
1. **Compiled-language infeasibility.** You cannot intercept `anthropic.messages.create` in a
   compiled Go binary — there is no monkey-patch seam, and Go is the P1 discovery target. Source
   rewriting works on any language the Discovery Engine can parse.
2. **Faithful measurement.** A shimmed run does not exercise the code that ships, so measured
   cost/latency/quality can diverge from production. Running the transformed source is the real
   thing.

PR-native delivery also fits the mental model developers already trust (Dependabot / Renovate /
coding agents): the applied output is a **verified optimization pull request**, not zero-touch
"magic." Git history is the audit trail and `git revert` is rollback — for free.

**Why AST-level, not string substitution.** A codemod must be **deterministic** (same `config_hash`
+ same source → byte-identical diff, so `config_hash` stays a durable pointer to an exact change)
and **behavior-preserving except for the intended dimension**. String/regex substitution cannot
guarantee either: it can match inside comments/strings, break on formatting, and produce
non-canonical output. Operating on the parsed syntax tree, anchored to the P0 call-site metadata,
lets the transform target exactly the argument being overridden and re-print canonically.

**Alternative rejected — runtime shim / adapter (the superseded plan).** Wrap each call site so
parameters resolve from a config store at runtime. Rejected: infeasible on compiled languages;
measures a proxy for production rather than production; adds a permanent runtime dependency in the
user's process. **Precompiled config bundles** were also rejected — an extra artifact that drifts
from the spec.

**Trade-off (the new top risk).** Transform correctness replaces runtime-interception fragility as
the top risk: a bad codemod can break a build or subtly change behavior. This is bounded by
Decisions 10–11 (build-preserving + behavior-preserving gates) and the requirement that nothing is
surfaced to the user until it builds and passes the eval/regression gate.

## Decision 2 — Four independent override dimensions, applied as targeted call-site edits

Each node has four dimensions — **model**, **prompt**, **skills**, **context** — overridable
*independently*. A Variant Spec entry may set only `model_ref` and leave the other three unchanged;
the generated diff then edits **only** the model argument at that one call site. This keeps ablation
(P4.5: change exactly one dimension of one node) a first-class operation, and it keeps the diff
minimal and reviewable — a reviewer sees exactly one dimension change at exactly one call site, with
no incidental edits.

## Decision 3 — Registries are immutable, content-addressed, git-like

**Decision.** Every published registry entry gets an immutable `version_id` derived from its
content hash. Editing an entry produces a *new* version; the old one is never mutated.

**Why.** Reproducibility is meaningless if a `model_ref` or `prompt_ref` can silently change under
an old Variant Spec — and here it must also mean a **byte-identical generated diff**. Immutable
versions make a `config_hash` a durable pointer to an exact configuration, hence an exact transform.
It also gives git-like diffability for free. Evolution is **expand-contract**: add fields or versions
additively; older specs keep resolving (FR10).

**Trade-off.** Storage grows monotonically with edits; acceptable because entries are small and
blobs are content-hashed (dedup identical bodies).

## Decision 4 — `config_hash` is derived from pinned versions + ordering, canonically; it pins the diff

`config_hash = H(canonical(Variant Spec))` where the canonical form pins each `*_ref` to its
immutable `version_id`, is invariant to key ordering and whitespace, and includes the node ordering.
Consequences:
- The hash changes **iff** a referenced version or the ordering changes — the reproducibility
  contract.
- **The generated diff is a pure function of `(config_hash, source_revision)`.** Same inputs →
  byte-identical patch. The patch itself is content-hashed and stored so the review artifact is
  itself reproducible and diffable.
- Prompt **rendering** is *not* part of the hash. We hash the template `version_id` + binding
  *values*; the rendered string is a derived, content-hashed blob. (PRD Q3.)

## Decision 5 — Provider gateway is the single seam; normalize shapes (unaffected by ADR-001)

**Decision.** All model calls made by the transformed, running code go through one LiteLLM-style
gateway exposing a provider-agnostic `Complete(ModelEntry, Request, Seed) → Response`. The gateway
normalizes request and response shapes across Anthropic / OpenAI / Bedrock.

**Why.** Three payoffs converge on one boundary: (a) **provider-swap transparency** — changing a
node's provider is a `model_ref` change, so the codemod rewrites the model argument and no other
workflow logic; (b) **one instrumentation point** — P2.5 attaches OTel at the gateway and captures
every call's cost/latency/tokens with zero application effort; (c) **model tiering later** — the
uniform interface lets P6 select a tier per node without re-plumbing. The gateway is also where
**timeouts + bounded backoff** and **secrets injection** live. ADR-001 leaves this seam unchanged:
only the *config-application mechanism* moved from runtime shim to source transformation.

**Trade-off.** A normalized shape is a lowest-common-denominator over provider features;
provider-specific params live inside `ModelEntry.params` and are passed through.

## Decision 6 — Idempotency key + DB uniqueness for exactly-once effects

Partial failure is the norm: a provider returns 500 mid-graph, the run queue redelivers, a node is
retried. Two mechanisms guarantee effects happen once:
- **Provider charge:** each node invocation carries an idempotency key
  `{run_id, node_id, attempt_group}`; the gateway reuses it across retries so a duplicated request
  is billed once (FR17).
- **Run writes:** a unique constraint `(run_id, node_id, attempt_group)` turns a double-write race
  into a caught conflict.

At-least-once queue dispatch + idempotent execution is preferred over exactly-once delivery. (PRD Q4.)

## Decision 7 — Reproducibility spans transform, build, and seed propagation

Reproducibility is asserted at three levels, none of which is provider output bytes (providers
don't guarantee bitwise determinism even at temperature 0):
1. **Transform:** same `{config_hash, source_revision}` → **byte-identical diff** (Decision 4).
2. **Build:** the transformed worktree builds deterministically to the same artifact (given a pinned
   toolchain), cached by `config_hash` (Decision 9).
3. **Run:** the `seed` is threaded from the Variant Spec → gateway → every stochastic step; same
   `{config_hash, seed}` → identical seed reaching each provider call (FR16).

This is honest about what we can guarantee (PRD Q2).

## Decision 8 — Executor runs the transformed working copy in a sandbox

**Decision.** The Executor does **not** walk the graph "through a shim." It builds the transformed
worktree, then **runs that built artifact in a sandbox**, walking the node graph in the spec's
declared ordering. Each node's output passes through the P0 typed I/O contract before it becomes a
downstream input; on a violation it **halts** with a typed error naming the node and dimension
(FR15). Because the code under measurement is the code that would ship, cost/latency/quality are
faithful. P2 does not reorder (that's P5) but enforces the same contract that makes P5's
re-arrangement safe.

## Decision 9 — Isolated worktree application + per-`config_hash` build cache

**Decision.** Transforms are applied to an **isolated git worktree/branch**, never the user's
working tree in place. A pooled set of worktrees is checked out at the target `source_revision`; the
Transform Engine applies the codemod, commits it on a variant branch, and runs the build. Build
outputs are cached keyed by `config_hash` (which pins source_revision + spec), so re-running the
same variant skips regeneration and rebuild.

**Why.** Isolation is a hard requirement (ADR-001 req 4): editing user code is high blast radius, so
the user's tree must never be mutated. A worktree gives a cheap, disposable checkout that shares the
object store with the origin repo. Keying the build cache on `config_hash` makes P4's fan-out
(hundreds of variants over the same base) affordable — identical variants are generated and built
once. Rollback of any surfaced change is a single `git revert` on the variant branch/PR.

**Trade-off.** The Runtime must manage working copies and builds per variant (worktree pool + build
cache eviction), which is heavier than a runtime shim — but far more accurate, and the pooling +
`config_hash` cache bound the cost. A build-cache miss adds a full transform+build to first run of a
variant; a hit is a lookup.

## Decision 10 — Build-preserving gate: a transform that breaks the build is rejected before it is proposed

**Decision.** After applying the codemod to the worktree, the Transform Engine runs the target's
build/compile. If it fails, the transform is **rejected** — it never becomes a proposed diff, never
runs, and surfaces a typed error naming the node/dimension whose rewrite failed to build. Only a
transform that builds green can proceed to run or be surfaced for review.

**Why.** Editing user code that doesn't compile is the worst failure mode. Making "it builds" a
precondition of *proposing* (not just running) means no reviewer ever sees a broken diff and the
build+eval gate is what bounds the transform-correctness risk (ADR-001 req 2, Consequences).

## Decision 11 — Behavior-preserving-except-intended-change + always-reviewable diff

**Decision.** The generated diff changes **only** the configured dimension(s) at the targeted call
site(s) — no reformatting of untouched code, no incidental edits, no changes to other call sites.
Every applied change reaches the user's repository **only** as a diff/PR a human can read; nothing
merges to the default branch without passing the verification gates (build + eval + regression) and
— below the Autonomous automation level (P6) — without human approval.

**Why.** Minimal, targeted diffs are what make review tractable and trust possible (ADR-001 reqs 3,
5). A codemod that re-prints the whole file or touches unrelated call sites is unreviewable even if
correct. The AST approach re-prints only the rewritten subtree; a diff that touches anything outside
the targeted call sites is itself a rejectable defect.

## Data model sketch

```
variant_spec(config_hash PK, spec_json, node_ordering, source_revision, created_at)  -- unique config_hash
model_entry(version_id PK, name, provider, model_id, params_json)           -- immutable
prompt_entry(version_id PK, name, template_blob_hash, slots_json)           -- immutable
skill_entry(version_id PK, name, io_schema_json, impl_handle)               -- immutable
context_entry(version_id PK, name, policy_kind, params_json)                -- immutable
transform(config_hash FK→variant_spec, source_revision, diff_blob_hash,     -- generated codemod
          build_status, worktree_ref, UNIQUE(config_hash, source_revision)) -- byte-identical per key
run(run_id PK, config_hash FK→variant_spec, seed, status, created_at)
node_execution(run_id FK, node_id, attempt_group, status,
               input_blob_hash, output_blob_hash, idempotency_key,
               UNIQUE(run_id, node_id, attempt_group))
```
Blobs (prompt bodies, rendered prompts, node inputs/outputs, **generated diffs/patches**) live in
the object store keyed by content hash; DB rows hold only the hash. Isolated worktrees + build
artifacts live in a pooled build cache keyed by `config_hash`.

## Key interfaces

```
Loader.Resolve(VariantSpec) -> (ResolvedConfig, error)          // fails closed on dangling ref
Transform.Generate(ResolvedConfig, SourceRevision) -> Diff       // deterministic AST codemod; byte-identical per (config_hash, rev)
Transform.Apply(Diff, WorktreeRef) -> AppliedWorktree            // isolated worktree; never user's tree in place
Transform.Build(AppliedWorktree) -> BuildResult                  // reject if build fails (build-preserving gate)
Gateway.Complete(ModelEntry, Request, Seed) -> Response          // normalized; timeout+backoff; idem key (unaffected by ADR-001)
Executor.Run(AppliedWorktree, Input, Seed) -> Run                // runs the built, transformed copy in a sandbox; contract checks per edge
Register<Kind>(entry) -> version_id                              // content-addressed, immutable
Rollback(config_hash) -> git revert                             // clean single-revert of the applied change
```

## Risks

- **Transform correctness (new top risk)** — a bad codemod breaks a build or subtly changes
  behavior. Mitigation: AST-level determinism (Decision 1), build-preserving rejection (Decision
  10), behavior-preserving minimal diffs (Decision 11), and the build+eval+regression gate before
  any proposal is surfaced.
- **Review burden** — every change is a diff the user must read. Mitigation: minimal targeted diffs,
  evidence-attached (measured cost/latency/quality), ranking + batching.
- **Worktree/build-cache cost** — managing per-variant working copies and builds is heavier than a
  shim. Mitigation: worktree pool + `config_hash`-keyed build cache so fan-out reuses builds
  (Decision 9).
- **Determinism ceiling** — providers may vary output even at fixed seed; we scope reproducibility
  to diff + build + seed propagation (Decision 7). Mitigation: assert at those levels; document the
  ceiling.
- **Gateway lowest-common-denominator** — mitigated by pass-through `params` (Decision 5).
- **Registry storage growth** — monotonic with edits; mitigated by content-dedup of blobs.
- **Context interface lock-in** — an interface too `full`-specific blocks P3; mitigated by
  co-designing the interface with the P3 policy authors.
