## Why

P2 shipped the Configuration Layer, the four registries, the Loader/Executor, and the provider
gateway — but with two deliberate stubs. The `context_policy` field is selectable yet only `full`
history is implemented, and skills run **trusted / built-in** because there is no isolation
boundary. Both must close before anything downstream is trustworthy or safe. Context is a
first-class evaluation lever — the improvement engine (P5.5) will *swap context policies* as a change
operator — and that operator is meaningless until the policies exist and are swappable per node by
config alone. More urgently, the platform ingests arbitrary repos and will **run their tool code**;
executing that untrusted code with the platform's ambient credentials and full host access is the
sharpest security vulnerability in the whole program.

P3 makes both real. It implements **context strategies** as pluggable, named policies with typed
params (`full-history`, `sliding-window`, `summarization`, `rag-retrieval`, `semantic-compaction`),
each swappable per node purely by config and deterministic given that config + seed. It **hardens
the Skill Registry** so every entry carries a JSON-schema contract that the runtime validates —
tool availability and argument shape — **before** the skill runs, failing closed on any mismatch.
And it introduces the **sandbox**: every node whose execution involves repo tool code runs in a
subprocess/container isolate with **no ambient credentials**, default-deny network egress, a
least-privilege read-only filesystem, and enforced resource bounds — with credentialed calls
brokered by the trusted host so the isolate never receives a provider key.

Depends on P2 (source-transformation engine, Variant Spec, `config_hash`, the four registries incl. the Skill Registry
baseline and the `context_policy` field + pluggable interface, the Runtime, and the gateway with
secrets from a manager). Uses P2.5's OTel substrate to emit sandbox/context telemetry.
Product rationale: [`../../../docs/prd/P3-context-skills-sandbox.md`](../../../docs/prd/P3-context-skills-sandbox.md).

## What Changes

- **New capability `context-strategies`.** Five named context policies behind the P2 policy
  interface, each with a typed params schema: `full-history`; `sliding-window {window_size}`;
  `summarization {summarizer_model_ref}`; `rag-retrieval {top_k, retriever_ref}`;
  `semantic-compaction {target_tokens}`. A node's policy is **swappable via config alone** (changing
  it changes `config_hash`, no workflow-code change); context assembly is **deterministic** given
  policy + params + conversation + seed. Bad params **fail closed** at resolution. Policies that need
  a model/retriever reference it by ref and the call is executed by the **trusted host** via the
  gateway — never from inside a sandboxed node. Each policy emits context-assembly telemetry.
- **New capability `skill-registry`** (hardening the P2 Skill Registry baseline). Every entry carries
  a JSON-schema contract for inputs **and** outputs; the contract is validated as well-formed at
  registration. Before a node runs a skill, the runtime validates **tool availability + argument
  shape** against the contract; the tool result is validated against the output schema before it
  propagates. Any mismatch — unavailable tool, arg-schema violation, output-schema violation —
  **fails closed**: the impl is not invoked (or its result discarded) and a typed error names the
  skill and violated field. Validation is pre-execution for inputs, pre-propagation for outputs.
- **New capability `sandbox`.** Each node involving repo tool code runs in a subprocess/container
  **isolate**; discovered tool code runs **only** there. The isolate starts with **no ambient
  credentials** (no provider keys, cloud credential files, secrets-manager access, or inherited env
  secrets); enforces **default-deny network egress** (explicit allowlist only); a **least-privilege
  read-only filesystem** scoped to the node's working set (no host FS, other runs, `.git` creds, or
  secrets store); and **resource bounds** (CPU, memory, wall-clock, PID, output size) whose breach
  terminates the isolate and fails the node closed. Credentialed LLM/retrieval calls are **brokered
  by the trusted host** — the sandbox never receives the credential. Isolation **fails closed**: an
  un-creatable isolate does **not** downgrade to host execution. Every denied action and resource
  breach emits a tagged audit event with no secret values leaked. Ships with an explicit
  **untrusted-code threat model** (credential theft, egress/exfiltration, host escape, resource
  exhaustion, cross-run contamination, filesystem traversal → control → test).
- **Deferred:** context-policy *change operators* (when to swap) → P5.5; behavioral pattern
  confirmation → P5; eval/scoring of which policy wins → P4; large-corpus RAG index build-out → later.

## Impact

- **Affected capabilities:** `context-strategies` (new), `skill-registry` (new, hardens the P2 Skill
  Registry), `sandbox` (new). Consumes P2's `config-layer`, `registries`, `runtime`, and gateway.
- **Affected code/systems:** context-policy plugins + Context Registry params; Skill Registry schema
  columns (`input_schema`/`output_schema`) + registration- and execution-time validators; the
  sandbox runner (subprocess/container isolate), the credentialed **broker** channel, egress/FS/
  resource enforcement, and the tagged denial/audit event stream; Executor/Loader integration so
  repo tool code runs only in-sandbox and contract validation gates every skill call; P2.5 telemetry
  emitters for context-assembly, tool-error, and sandbox-denial events.
- **Dependencies:** requires **P2** (source-transformation engine, Variant Spec, `config_hash`, the four registries + policy
  interface, Runtime, gateway w/ secrets manager) and **P2.5** (soft — telemetry substrate).
  Unblocks **P4** (variants differing only by context policy; safe execution of repo tools), **P4.5**
  (slices on tool-error / context-utilization / sandbox-denial), **P5.5** (context-policy swap +
  skill-add as change operators; the context-engineering disciplines as named operators), and
  **P3.5** (Tool Use / RAG structural detection keys off skill bindings and the `rag-retrieval`
  policy).
