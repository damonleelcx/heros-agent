# Design — P3: Context strategies + Skill registry + Sandbox

Cross-reference: product rationale in [`../../../docs/prd/P3-context-skills-sandbox.md`](../../../docs/prd/P3-context-skills-sandbox.md).

## Context

P3 closes the two deliberate P2 stubs — context policies beyond `full`, and un-isolated skill
execution — and in doing so crosses the platform's sharpest security boundary: it *runs tool code
discovered in an arbitrary target repo*. That single fact drives most of the design. Two roles
co-lead: Backend owns the context-policy plugins and the Skill Registry's contract-before-execution
discipline; DevOps owns the sandbox and its least-privilege containment. The AI Engineer advises on
context-policy semantics so the interface admits P5.5's change operators. The governing rule from
the DevOps playbook — *secrets never touch repo·logs·terminal, least privilege, blast radius before
implementation* — and the Backend rule — *contracts outlive code; fail closed; model invariants into
the boundary* — are the two axes everything below sits on.

## Decision 1 — Untrusted by default: repo tool code runs ONLY in the sandbox

**Decision.** Any node whose execution involves tool code from the target repo runs inside an
isolate (subprocess with OS-level sandboxing and/or container). There is no configuration flag that
runs discovered tool code on the host.

**Why.** The repo is untrusted input. A tool can be adversarial or merely careless; either way it
must be *structurally* unable to reach host secrets, other runs, or the network. Making the sandbox
the only path — rather than an opt-in — removes the "just this once on the host" failure mode.

**Alternative rejected.** In-process execution with monkey-patched guards — a single missed hook is
a full compromise; not a real boundary. A static analysis "is this tool safe?" gate — undecidable in
general and fails open on anything novel.

## Decision 2 — No ambient credentials; credentialed calls go through a host broker

**Decision.** The isolate starts with a scrubbed environment and no credential mounts, no
secrets-manager access, and no cloud-metadata reachability. When a sandboxed tool legitimately needs
a model or retrieval call, it requests it over a narrow **broker** channel; the *trusted host*
performs the call via the P2 provider gateway (which holds the real secrets) and returns only the
result.

**Why.** "No ambient credentials" (FR13) is the highest-value security requirement and it must
survive tools that legitimately need an LLM call. The broker is the seam that keeps the property
true: the credential never crosses into the isolate, yet the tool can still function. This is the
DevOps *least-privilege / secrets never touch the untrusted surface* directive made concrete.

**Trade-off / risk.** The broker could become an egress bypass. Mitigation: it applies the same
allowlist + validation as direct egress, exposes only a fixed call vocabulary (LLM / retrieval /
allowlisted HTTP), and audits every call. A tool cannot use the broker to reach a non-allowlisted
host (PRD Q3).

## Decision 3 — Default-deny egress, read-only scoped filesystem, hard resource bounds

Three containment controls, all default-restrictive:
- **Network:** deny-all egress with an explicit allowlist (default empty). An un-allowlisted outbound
  attempt is blocked and recorded as a denial. The metadata endpoint is unreachable (closes the
  cloud-credential-theft path).
- **Filesystem:** read-only, minimal view scoped to the node's declared working set. No host FS, no
  other run's data, no repo `.git` credentials, no secrets store. Writes are confined to an ephemeral
  scratch area destroyed with the isolate.
- **Resources:** CPU, memory, wall-clock, process/PID count, and captured-output size are bounded
  (defaults e.g. 1 vCPU / 512 MB / 60 s / 128 PIDs / 8 MB — configurable, never unbounded). Breach
  terminates *only* that isolate and fails the node closed with a typed resource error.

**Why.** These are the *blast-radius* controls: one bad tool cannot exfiltrate, cannot read the
host, cannot starve other runs. Isolates are ephemeral and per-node so nothing persists between
nodes except through the typed I/O contract via the host — the *reversible* directive.

## Decision 4 — Isolation fails closed; no host fallback

**Decision.** If an isolate cannot be created with all required restrictions (no creds, deny egress,
scoped FS, bounds), the node **does not execute** the untrusted code — there is no downgrade to
host execution.

**Why.** A fallback-to-host on sandbox failure would convert an operational error into a full
security bypass exactly when the environment is already degraded. Fail-closed is the only safe
default (FR19). This mirrors the Backend fail-closed discipline applied to the security boundary.

## Decision 5 — Skill contract validated BEFORE execution and BEFORE propagation

**Decision.** Skill Registry entries carry `input_schema` and `output_schema` (JSON Schema). Two
gates: (1) at **registration**, both schemas are checked to be well-formed JSON Schema (a malformed
contract is rejected); (2) at **execution**, the runtime validates tool availability + argument
conformance *before* invoking the impl, and the result's conformance *before* it propagates
downstream. Any mismatch fails closed with a typed error naming the skill and the violated field.

**Why.** A contract validated *after* the call has already fired is theatre — the side effect is
already spent, and malformed args may already have run untrusted code. Validating before invoke is
the Backend *model-invariants-into-the-boundary* discipline: the runtime, not each skill author,
guarantees no skill ever runs on malformed args. Output validation before propagation stops a
schema-violating result from silently corrupting a downstream node (which P5 re-arrangement depends
on being safe).

**Interaction with the sandbox.** Input validation happens on the host *before* the isolate is even
handed the arguments; output validation happens on the host *after* the isolate returns, before the
result crosses to the next node. So the untrusted code only ever sees already-validated inputs, and
never gets to emit an unvalidated output into the graph.

## Decision 6 — Context policies are host-side plugins with typed params; deterministic given seed

**Decision.** Each of the five policies (`full-history`, `sliding-window`, `summarization`,
`rag-retrieval`, `semantic-compaction`) implements the P2 pluggable interface
`Policy.Assemble(conversation, params, seed) → AssembledContext`, runs **host-side** (never in a
sandbox), and declares a typed params schema. Params are validated at resolution and fail closed on
violation. Assembly is deterministic given policy + params + conversation + seed: byte-identical for
LLM-free policies (`full-history`, `sliding-window`, deterministic compaction), and an identical
*resolved request* under fixed seed for policies with an LLM step (`summarization`, reranked
`rag-retrieval`) — extending P2's reproducibility ceiling (config + seed, not provider output bytes).

**Why host-side.** A policy that summarizes or retrieves needs a model/retriever call; keeping
policies on the trusted host means those calls go straight through the gateway with real secrets and
never require the sandbox to hold a credential. Context assembly is a trusted operation over the
node's own conversation, not untrusted repo code, so it does not belong in the isolate.

**Swappability.** A node's policy is selected by the `context_policy` field alone; changing it (or
its params) changes `config_hash` and requires no workflow-code change (FR3). This is what makes
"swap context policy" a clean P5.5 change operator.

## Decision 7 — Params surface reserves room for P5.5 operators (AI Engineer lens)

The context-engineering discipline names more operators than P3 implements: just-in-time retrieval,
sub-agent context isolation. The params surface is designed additively so these land as P5.5
operators *behind the same interface* without a breaking change. Lossy policies (summarization,
compaction) must emit drop/compaction ratios and retrieved-chunk counts so that a "compaction
dropped the answer" defect is measurable in P4 — a context-engineering defect mapped to a change
operator, per the roles doc.

## Threat model (summary; full artifact in task 5.1)

| Attack | In scope | Control (this change) | Proven by |
|---|---|---|---|
| Credential theft (env, files, metadata endpoint) | Yes | No ambient creds; scrubbed env; no mounts; metadata blocked; broker holds secrets host-side | cred-read-finds-nothing test |
| Network exfiltration | Yes | Default-deny egress, empty allowlist; broker can't reach non-allowlisted hosts | egress-denied test + denial event |
| Host / container escape | Yes | Dropped privileges, no host mounts, container/microVM isolate | escape-attempt test; security review |
| Resource exhaustion (fork/mem bomb, infinite loop) | Yes | CPU/mem/PID/wall-clock/output bounds; terminate isolate only | fork-bomb-contained + blast-radius test |
| Cross-run contamination | Yes | Ephemeral per-node isolate; no shared state except via typed I/O on host | isolate-teardown test |
| Filesystem traversal (host FS, secrets, other runs) | Yes | Read-only scoped working set; no host FS | FS-scope-violation-denied test |
| Malformed tool args / results | Yes | Skill contract validated pre-exec / pre-propagation, fail closed | arg-/output-schema tests |

## Data model / interface sketch

```
skill_entry(version_id PK, name, input_schema_json, output_schema_json, impl_handle)  -- immutable
context_entry(version_id PK, name, policy_kind, params_json)                          -- immutable

Policy.Assemble(conversation, params, seed) -> AssembledContext        // host-side, deterministic
Sandbox.Run(node, workingSet, resourceBounds, egressAllowlist) -> Result
   // creates isolate: scrubbed env, deny egress, RO scoped FS, bounds; fails closed
Broker.Complete(request) / Broker.Retrieve(query, top_k)               // host-side, credentialed
Validator.CheckInput(skill_version_id, args)  -> ok | typed-error      // pre-execution
Validator.CheckOutput(skill_version_id, result) -> ok | typed-error    // pre-propagation
```

## Risks

- **Sandbox escape** → dropped-privilege container/microVM isolate, no host mounts, security-reviewer
  sign-off against the threat model; escape-attempt test in CI.
- **Broker as egress hole** → narrow fixed vocabulary, same allowlist + validation, audited (Q3).
- **Isolate cold-start latency for tiny tool calls** → warm container pool; bounded subprocess path;
  measured vs. < 500 ms p50 target (Q1).
- **Context interface lock-in** → additive params surface co-designed with the AI Engineer to admit
  P5.5 operators (Decision 7).
- **Lossy compaction hides failures** → mandatory drop/compaction-ratio telemetry so P4 can measure.
- **Determinism ceiling on LLM-using policies** → scoped to resolved request + seed (P2 Q2), not
  provider output bytes; documented.
