## Why

The platform's prompt and model primitives are well built and unreachable. The prompt registry is
content-addressed and structurally immutable — `version_id = sha256(canonical_json(envelope))`, no
`Update*` API exists, and the Postgres migration adds a `BEFORE UPDATE OR DELETE` trigger plus a
`CHECK` that the id equals the hash. `{{name}}` templating renders deterministically and fails loudly
on **missing *and* unknown** bindings. Per-node `model_ref` / `prompt_ref` selection is in the Variant
Spec. None of this is being re-specified.

What is missing is everything a human needs to use it.

**A prompt cannot be written by a person.** `RegisterPrompt(ctx, name, body)` is called by nothing
outside two test files. `internal/api/p2.go` exposes run, transform, spec-resolve and spec-submit —
there is **no registry endpoint of any kind**, no CLI subcommand, and no UI. The product's story is
"remix your prompts," and the only way to author one is to write Go. There is also no history: versions
are content-addressed with no parent link, so "the history of prompt X" is inferred from entries
sharing a name, and nothing can answer *what changed between these two versions* — the first question
anyone asks before adopting one.

**A slot can only bind to a call-site expression spelled identically.**
`internal/transform/rewrite.go:277` requires every slot to match exactly one call-site operand by
identical source text and refuses otherwise. The refusals are correct — an unclaimed operand is a
runtime value the rewrite would silently drop, and guessing which value belongs in which slot is the
plausible-but-wrong behavior this codebase declines elsewhere. But the consequence is a trap: a user
edits a prompt, adds `{{customer_tier}}`, publishes it, points a node at it, and learns **at transform
time** that it cannot be applied anywhere. There is no way to bind a slot to a constant, an environment
variable, or a typed input — only to a variable a colleague already happened to name at the call site.

**Every configuration change costs a pull request.** Because the codemod writes values *inline*,
moving a node to a different model, or to a newer version of the same prompt, requires a fresh codemod,
build and review — the same cost as a structural change. That is proportionate for wiring and
disproportionate for data, and it turns "try a model, try a prompt" into a build loop.

[ADR-004](../../../docs/adr/ADR-004-runtime-config-binding.md) resolves the last one by reframing it:
the question was never *when* a value resolves, but **which facts are data and which are program
structure**. A model id and a prompt body are values. An `expr` binding names a variable in the user's
lexical scope and cannot move into a data file without reflection Go does not offer. So the codemod
gains an opt-in second shape — write an indirection plus a resolved binding document, both in the same
diff — and what becomes runtime-changeable is exactly the data, no more.

ADR-004 **amends ADR-001 without superseding it**: transforms stay AST-level, deterministic,
build-preserving, behavior-preserving, worktree-isolated, reviewable and revertible. It does narrow one
normative clause — `p5.5/specs/verification/spec.md`'s *"no runtime shim substitutes parameters at
execution time"* — which is why this change carries a `MODIFIED` delta rather than a quiet addition.

## What Changes

- **New capability `prompt-authoring`.** An authenticated **write path** that publishes a prompt
  version, reusing the existing content-addressed semantics; publishing identical content returns the
  existing `version_id` rather than duplicating. **No interface expresses mutation or deletion** — an
  edit produces a new version and the prior one stays resolvable by every spec pinning it, so the
  existing DB trigger remains the last line of defence rather than the first. A malformed template is
  rejected **at publish**, naming the offending position. The platform returns a **version timeline**
  per prompt name and a **diff between any two versions** that reports the **slot-set change separately
  from the body text**, because a slot change is what alters where a prompt can be applied and is
  nearly invisible inside a body diff. For a proposed edit it returns an **impact analysis**: which
  nodes pinning that prompt would fail to transform under the new slot set, and why — and it **names
  what it could not analyze**, because silence is not a clean bill of health.
- **New capability `variable-bindings`.** `NodeOverride` gains a `bindings` map from slot name to a
  source of kind **`literal`**, **`expr`**, **`env`**, or **`input`**. Every slot of a node's pinned
  prompt must be satisfied by exactly one of an explicit binding or today's exact-source-text match;
  an unsatisfied slot is a **resolve-time** rejection naming node, dimension and slot, reported through
  the existing `variantspec.SpecError` channel. An `expr` is validated against the **in-scope symbols
  the IR records for that call site**; an `env` names a declared variable and an absent value at run
  time is a **typed failure, never an empty string** substituted into a prompt; an `input` must satisfy
  the node's P5 typed contract. **All validation happens at spec-resolve, before any transformation is
  generated** — nothing is discovered at codemod time that could have been caught earlier. The
  **unclaimed-operand refusal is preserved**. `bindings` join the resolved configuration so
  `config_hash` changes iff a binding changes — **additively**, so a spec with no `bindings` hashes
  exactly as it does today.
- **New capability `runtime-config-binding`.** Apply mode becomes selectable **per node** as `inline`
  or `bound`, defaulting to **`inline`** — nothing acquires an indirection unless asked. In `bound`
  mode the transformation emits, **in one diff**, the rewritten call site, a generated binding
  artifact, and the **resolved binding document containing the actual values**; a transformation that
  introduces an indirection **without** those values is **rejected**, on the same footing as one that
  fails to build, and the pull request renders the **effective resolved values** rather than the
  pointer. At run time the resolver reads **embedded → local override → remote-if-enabled**, and is
  **fail-static**: an unreachable, unparseable or invalid override leaves the last known-good document
  in force and reports **degraded** — never failing open to an arbitrary configuration and never
  blocking process startup. The resolver emits the **`config_hash` of the document it actually
  resolved on every invocation**, and the eval harness **fails a run** whose observed hash differs from
  the requested one rather than scoring it. Eval and verification runs are **pinned** — override
  sources disabled. The document records the `config_hash` carrying a **verified delta**; resolving to
  a configuration without one is permitted but **marked unverified at every invocation** and refusable
  by automation level. The artifact is dependency-free, deterministic and byte-identically
  regenerable, and a `bound` change reverts in a **single `git revert`**.
- **New capability `prompt-studio`.** Console surfaces for **preview** (render a version with sample
  bindings, showing the exact string that would be sent, byte-identical to what a run sends) and
  **test-run** (execute against a selected model version, recording cost, latency and tokens), plus
  **side-by-side comparison** of two prompt versions or one version across two models. Every studio
  result is labelled **unranked / exploratory**: it shows **no score, rank, winner or confidence
  interval**, and offers **no path to promote a configuration**. Only a P4 multi-seed CI-bounded run
  produces a rankable result and only a P5.5 verified delta is a claim — a two-sample comparison
  presented as a finding is the amateur loop the platform exists to replace. Studio traffic is metered
  under its **own spend kind** so exploration never contaminates eval cost metrics. The console lets a
  user select a model version with its inference params and a prompt version per node, showing the
  resulting `config_hash` before submission, and states **per node which facts are runtime-changeable
  and which require a new diff** — because the honest version of this feature is narrower than
  "runtime configuration" implies.
- **`MODIFIED` — `verification`.** The P5.5 requirement *"The gate SHALL auto-execute the transformed
  working copy on a held-out split where available"* is restated verbatim with its scenario clause
  narrowed: what is forbidden is a shim **we** inject around the user's call, absent from their
  repository and their build. A **generated, in-repo, reviewed, built** binding that ships in the
  artifact is not that, and verification reads it with the resolver **pinned**, so the clause's intent
  — measure the code that would ship — is preserved exactly.
- **Not changed here.** No new pipeline, no new statistics, no new registry table (timeline and diff
  are **read models over existing rows**). ADR-002 is untouched: the transformed program still calls
  its own SDKs, and cross-provider swapping is still refused. `internal/registry`,
  `internal/transform`, `internal/variantspec` and `internal/api` are extended by later
  implementation, not by this change.

## Impact

- **Affected capabilities:** `prompt-authoring` (new), `variable-bindings` (new),
  `runtime-config-binding` (new), `prompt-studio` (new), `verification` (**MODIFIED**). Extended by
  reference, not edited: `registries`, `config-layer`, `runtime` (P2); consumed:
  `metrics-observability` (P2.5), `eval-harness`/`scoring` (P4), `typed-contracts` (P5),
  `web-console`/`console-design-system` (P9).
- **Affected code/systems:** `internal/registry` (read models for timeline/diff/impact — no mutation
  API), `internal/variantspec` (the `bindings` map and its resolve-time validation),
  `internal/transform` (the `bound` apply mode and the generated artifact), `internal/api` (the first
  **write** surface on the platform API), `schemas/workflow-ir.schema.json` (an **additive** `in_scope`
  extension to an `x-frozen: additive-only` object), the P0 `config_hash` shape (**additive**), and the
  P9 console.
- **Dependencies:** requires **P0** (IR + `config_hash`), **P2** (registries, Variant Spec, codemod,
  worktree/build), **P2.5** (telemetry for the per-invocation hash and the studio spend kind), **P4**
  (the harness performing the reconciliation, and the boundary the studio must not cross), **P5**
  (typed contracts for `input` bindings), **P5.5** (the verified-delta record), **P9** (the console).
- **Unblocks:** the configuration loop closes in the browser; **P6** gains a cheaper apply path for the
  dimensions that are data; skill and context-policy authoring later reuse this write-path and
  impact-analysis shape.
- **Breaking:** none. `bindings` is additive and a spec omitting it hashes identically to today;
  `inline` remains the default apply mode. The `MODIFIED` verification delta **narrows a clause without
  weakening its intent** — verification still measures the built, shipped artifact, with the resolver
  pinned.
- **Sequencing:** **10a** (authoring + bindings + studio) carries no runtime-path risk and is a
  complete phase on its own. **10b** (the runtime binding layer) is sequenced second deliberately so
  its stability surface is isolated and independently cuttable.
