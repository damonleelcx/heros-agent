# Tasks — P2: Configuration Layer + Runtime

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

## 2. Backend — Configuration Layer / shim
- [ ] 2.1 Define the **Variant Spec** type: `{node_id → {model_ref, prompt_ref, skill_refs[],
      context_policy}}` + node ordering/graph; persist in Postgres, unique on `config_hash`.
- [ ] 2.2 Implement the shim: resolve each node's four dimensions from the Variant Spec at
      invocation time; per-dimension independent override; absent override → IR default.
- [ ] 2.3 Implement stable `config_hash`: canonical serialization (key-order- and
      whitespace-invariant); changes iff a referenced version or the ordering changes.
- [ ] 2.4 Validation: reject a spec referencing a missing node, an unresolved `*_ref`, or an
      unregistered `context_policy` — before any execution.

## 3. Backend + System Designer — Runtime loader & executor
- [ ] 3.1 Implement `Loader.Resolve(VariantSpec) → ResolvedConfig`: resolve every `*_ref` against
      registries at invocation time; **fail closed** (abort, no partial run) on any dangling ref.
- [ ] 3.2 Render prompts from templates + bindings; bind skills from the registry; validate skill
      availability + arg shape against the JSON-schema contract before binding.
- [ ] 3.3 Instantiate the selected context policy (`full` in P2) via the pluggable interface.
- [ ] 3.4 Implement `Executor.Run`: walk the node graph in declared ordering through the shim;
      pass each node's output through the typed I/O contract before it feeds a downstream node.
- [ ] 3.5 Halt the run with a typed error naming node + dimension on any typed-contract violation;
      do not pass malformed data downstream.
- [ ] 3.6 Persist `run` (`run_id`, FK → variant) and `node_execution` rows (status,
      input/output blob hashes, idempotency key); terminal status = succeeded / failed / halted.

## 4. Backend + System Designer + AI — Provider gateway
- [ ] 4.1 Implement a LiteLLM-style gateway with a normalized request/response shape across
      providers (Anthropic, OpenAI, Bedrock at minimum).
- [ ] 4.2 **Provider-swap transparency:** swapping a node's provider requires changing only its
      `model_ref`; no workflow code changes; response normalized.
- [ ] 4.3 Ensure the `ModelEntry` + gateway interface does not preclude later **model tiering**
      (cost/complexity-aware selection per node in P6).
- [ ] 4.4 Per-call **timeout** (default 60 s, per-model override) + bounded exponential backoff
      with jitter on transient failures.
- [ ] 4.5 Source provider credentials from a **secrets manager**; inject at call time; scrub from
      logs, traces, error messages, and run records.

## 5. Backend — Idempotency & reproducibility
- [ ] 5.1 Derive an idempotency key per node invocation (`{run_id, node_id, attempt_group}`);
      gateway de-dupes so a retried call is not billed twice.
- [ ] 5.2 Unique constraint `(run_id, node_id, attempt_group)` so a double-write race is a caught
      conflict, not a duplicate row.
- [ ] 5.3 Thread `seed` from the Variant Spec → gateway → every stochastic step; assert identical
      seed propagation for the same `config_hash` + seed.
- [ ] 5.4 Reproducibility test: run the same `{config_hash, seed}` twice → byte-identical resolved
      config and identical seed reaching each provider call.

## 6. DevOps — Secrets, queue, operability
- [ ] 6.1 Wire the secrets manager; verify no secret appears in code, DB rows, logs, or run
      records (log-scrub test).
- [ ] 6.2 Seed the **run queue** for run dispatch (at-least-once dispatch + idempotent execution).
- [ ] 6.3 Structure run/node records so P2.5 OTel instrumentation attaches at the shim/gateway with
      zero application change.

## 7. Frontend + Product — Bare run/inspect UI
- [ ] 7.1 Product: the configure-a-node journey — four independent override dimensions, each → a
      versioned registry entry; "no override" is a legible default. Design the unhappy path
      (unresolved ref, contract halt) first.
- [ ] 7.2 Frontend: submit a Variant Spec → watch a run → view per-node I/O.
- [ ] 7.3 First-class **loading / error / empty / success** states; read terminal status from the
      run record (no derived state that drifts).
- [ ] 7.4 Surface *which node* and *which dimension* failed on a fail-closed resolution or a
      contract halt.

## 8. Testing & review
- [ ] 8.1 Fixture: hardcoded 3–5 node graph + known-good Variant Spec + a model-override variant +
      a prompt-override variant.
- [ ] 8.2 Integration tests (real Postgres + object store, stubbed providers): reproducibility,
      idempotency (one charge / one row under forced retry), provider swap, fail-closed on dangling
      ref, contract halt.
- [ ] 8.3 UI verification: drive the bare UI against a live (stubbed-provider) run; confirm
      loading → per-node I/O → terminal + error + empty states.
- [ ] 8.4 Adversarial self-review: unresolved ref, mutated version, secret in logs, non-idempotent
      retry, seed not propagated, malformed I/O passed downstream.
- [ ] 8.5 Confirm M2 exit checklist (PRD §13) is green.
