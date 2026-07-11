# Tasks — P2: Configuration Layer + Runtime

Applies the source-transformation apply model per
[ADR-001](../../../docs/adr/ADR-001-source-transformation-apply-model.md).

## 1. Backend — Registries (model / prompt / skill / context)
- [ ] 1.1 Design Postgres schema for the four registries: `(name, version_id)` unique per entry,
      `version_id` content-addressed, published rows immutable. Expand-only migration.
- [ ] 1.2 Implement `RegisterModel` — provider + model ID + inference params (temperature,
      max_tokens, thinking budget, seed) as a versioned unit; return `version_id`.
- [ ] 1.3 Implement `RegisterPrompt` — template with named variable slots; store body as a
      content-hashed blob; deterministic renderer given identical bindings.
- [ ] 1.4 Implement `RegisterSkill` — name → JSON-schema input/output contract + impl handle;
      validate the schema itself at registration.
- [ ] 1.5 Implement `RegisterContextPolicy` — named policy + params; register the `full` policy;
      leave the interface open for P3 policies.
- [ ] 1.6 Enforce immutability: reject any mutation of a published `version_id`; a change produces
      a new version.
- [ ] 1.7 Verify additive/expand-contract evolution: a Variant Spec pinning an older version still
      resolves after a new version is published.

## 2. Backend — Configuration Layer / Variant Spec + transform generation
- [ ] 2.1 Define the **Variant Spec** type (canonical desired-state config): `{node_id →
      {model_ref, prompt_ref, skill_refs[], context_policy}}` + node ordering/graph +
      `source_revision`; persist in Postgres, unique on `config_hash`.
- [ ] 2.2 Implement the **Transform Engine**: from a resolved Variant Spec, generate a
      **deterministic AST-level codemod** that rewrites each node's four dimensions at its
      discovered call site (model arg, prompt construction, tools/skills, context assembly) to the
      spec's values; per-dimension independent — a dimension left at default is not edited.
- [ ] 2.3 Determinism: same `config_hash` + same `source_revision` → **byte-identical diff**;
      content-hash the generated patch and store it in the object store.
- [ ] 2.4 Behavior-preserving-except-intended: the diff touches only the targeted call site(s) and
      only the configured dimension(s); reject a transform that produces incidental edits or
      reformats untouched code.
- [ ] 2.5 Implement stable `config_hash`: canonical serialization (key-order- and
      whitespace-invariant); changes iff a referenced version or the ordering changes.
- [ ] 2.6 Validation: reject a spec referencing a missing node, an unresolved `*_ref`, an
      unregistered `context_policy`, or a call site the transform cannot rewrite safely — before
      any transform is applied or run.

## 3. Backend + System Designer — Runtime loader, transform application & executor
- [ ] 3.1 Implement `Loader.Resolve(VariantSpec) → ResolvedConfig`: resolve every `*_ref` against
      registries; **fail closed** (abort, no diff generated, no run) on any dangling ref.
- [ ] 3.2 Render prompts from templates + bindings; bind skills from the registry; validate skill
      availability + arg shape against the JSON-schema contract before the transform binds them at
      the call site.
- [ ] 3.3 Instantiate the selected context policy (`full` in P2) via the pluggable interface; the
      transform rewrites the call site's context assembly accordingly.
- [ ] 3.4 **Isolated application:** check out an isolated git worktree/branch at `source_revision`
      from a pool; apply the codemod and commit on a variant branch; never mutate the user's
      working tree in place.
- [ ] 3.5 **Build-preserving gate:** run the target's build on the transformed worktree; **reject**
      a transform that fails to build before it is ever proposed or run; surface a typed error
      naming the node/dimension whose rewrite failed to build.
- [ ] 3.6 Per-`config_hash` **build cache**: cache the built, transformed artifact keyed by
      `config_hash`; a cache hit skips regeneration + rebuild (supports P4 fan-out).
- [ ] 3.7 Implement `Executor.Run`: **run the built, transformed working copy in a sandbox**,
      walking the node graph in declared ordering; pass each node's output through the typed I/O
      contract before it feeds a downstream node.
- [ ] 3.8 Halt the run with a typed error naming node + dimension on any typed-contract violation;
      do not pass malformed data downstream.
- [ ] 3.9 Persist `transform` (diff blob hash, build status, worktree ref) and `run`/`node_execution`
      rows (status, input/output blob hashes, idempotency key); terminal status =
      succeeded / failed / halted / build-rejected.
- [ ] 3.10 **Always-reviewable diff + clean rollback:** surface every applied change as a
      reviewable diff/PR; nothing merges to the default branch without the build+eval+regression
      gate and (below Autonomous) human approval; implement rollback as a single `git revert`.

## 4. Backend + System Designer + AI — Provider gateway (unaffected by ADR-001)
- [ ] 4.1 Implement a LiteLLM-style gateway with a normalized request/response shape across
      providers (Anthropic, OpenAI, Bedrock at minimum).
- [ ] 4.2 **Provider-swap transparency:** swapping a node's provider requires the codemod to rewrite
      only its `model_ref` at the call site; no other workflow logic changes; response normalized.
- [ ] 4.3 Ensure the `ModelEntry` + gateway interface does not preclude later **model tiering**
      (cost/complexity-aware selection per node in P6).
- [ ] 4.4 Per-call **timeout** (default 60 s, per-model override) + bounded exponential backoff
      with jitter on transient failures.
- [ ] 4.5 Source provider credentials from a **secrets manager**; inject at call time; scrub from
      logs, traces, error messages, generated diffs, and run records.

## 5. Backend — Idempotency & reproducibility
- [ ] 5.1 Derive an idempotency key per node invocation (`{run_id, node_id, attempt_group}`);
      gateway de-dupes so a retried call is not billed twice.
- [ ] 5.2 Unique constraint `(run_id, node_id, attempt_group)` so a double-write race is a caught
      conflict, not a duplicate row.
- [ ] 5.3 Thread `seed` from the Variant Spec → gateway → every stochastic step; assert identical
      seed propagation for the same `config_hash` + seed.
- [ ] 5.4 Reproducibility test: run the same `{config_hash, source_revision, seed}` twice →
      byte-identical generated diff, deterministic build, and identical seed reaching each provider
      call.

## 6. DevOps — Secrets, worktree pool, queue, operability
- [ ] 6.1 Wire the secrets manager; verify no secret appears in code, DB rows, logs, generated
      diffs, or run records (log-scrub test).
- [ ] 6.2 Stand up the **worktree pool + build-cache** infrastructure (checkout, apply, build,
      evict) with least-privilege access to a read-only clone of the target repo.
- [ ] 6.3 Seed the **run queue** for run dispatch (at-least-once dispatch + idempotent execution).
- [ ] 6.4 Structure run/node/transform records so P2.5 OTel instrumentation attaches at the gateway
      and the transform/build/run path with zero application change.

## 7. Frontend + Product — Bare run/review/inspect UI
- [ ] 7.1 Product: the configure-a-node journey — four independent override dimensions, each → a
      versioned registry entry; "no override" is a legible default. The applied change is a
      **reviewable diff/PR**, not a silent runtime effect. Design the unhappy path (unresolved ref,
      build-rejected transform, contract halt) first.
- [ ] 7.2 Frontend: submit a Variant Spec → **review the generated diff** → watch the transformed
      copy run → view per-node I/O.
- [ ] 7.3 First-class **loading / error / empty / success** states; read terminal status from the
      run/transform records (no derived state that drifts); a build-rejected transform is a
      distinct, legible state.
- [ ] 7.4 Surface *which node* and *which dimension* failed on a fail-closed resolution, a
      build-rejected transform, or a contract halt.

## 8. Testing & review
- [ ] 8.1 Fixture: hardcoded 3–5 node graph (buildable target repo at a pinned `source_revision`) +
      known-good Variant Spec + a model-override variant + a prompt-override variant.
- [ ] 8.2 Integration tests (real Postgres + object store + git worktrees, stubbed providers):
      deterministic diff, build-preserving rejection (a spec that would break the build is rejected
      before running), behavior-preserving minimal diff, isolated application (user tree untouched),
      reproducibility, idempotency (one charge / one row under forced retry), provider swap,
      fail-closed on dangling ref, contract halt, clean `git revert` rollback.
- [ ] 8.3 UI verification: drive the bare UI against a live (stubbed-provider) run; confirm submit →
      diff review → per-node I/O → terminal + error + empty + build-rejected states.
- [ ] 8.4 Adversarial self-review: unresolved ref, mutated version, non-deterministic diff, transform
      that breaks the build, diff with incidental edits, user working tree mutated in place, secret
      in logs/diffs, non-idempotent retry, seed not propagated, malformed I/O passed downstream,
      rollback that leaves residue.
- [ ] 8.5 Confirm M2 exit checklist (PRD §13) is green.
