# P3 Untrusted-Code Threat Model (task 5.1)

Scope: P3 runs **tool code discovered in an arbitrary target repo**. That code is untrusted — it may be
adversarial or merely careless. This document enumerates the attacks in scope, the control that denies
each, and the test that proves the control holds. It is the artifact the security-reviewer signs off
against ([`security-review.md`](security-review.md)).

Governing rules: *secrets never touch the untrusted surface; least privilege; fail closed; blast radius
before implementation.* Every control below is default-restrictive and every failure is fail-closed —
an un-enforceable restriction denies execution, never downgrades to the host.

## Trust boundary

```
   TRUSTED HOST                          │        UNTRUSTED ISOLATE (per node)
   ───────────                           │        ────────────────────────────
   provider gateway (holds secrets)      │        repo tool code (subprocess)
   context policies (host-side)          │          - scrubbed env (no creds)
   skill contract gate                   │          - default-deny egress
   broker  ◀──── narrow, audited ───────▶│          - RO working-set FS
   audit → telemetry (P0-tagged)         │          - hard resource bounds
```

Nothing crosses the boundary except the typed I/O contract and the narrow broker vocabulary. The
credential never enters the isolate.

## Attack → Control → Test

| # | Attack | In scope | Control (this change) | Proven by (test) |
|---|--------|----------|-----------------------|------------------|
| 1 | **Credential theft** — read provider keys/cloud creds from env, credential files, or the metadata endpoint | Yes | Scrubbed environment (`scrubbedEnv`): no provider keys, no inherited secrets; `HOME`/`TMPDIR` point into the ephemeral scratch so `~/.aws` resolves to nothing; metadata endpoint unreachable via the network-deny layer; secrets stay host-side behind the broker | `sandbox.TestNoAmbientCredentials` |
| 2 | **Network exfiltration** — POST stolen data to an attacker host | Yes | Default-deny egress (`EgressPolicy`, empty allowlist); direct egress denied by the contained enforcer's network namespace; the broker refuses any non-allowlisted host and records the attempt | `broker.TestHTTP_DefaultDeny`, `broker.TestHTTP_EgressAllowlistEnforced` |
| 3 | **Broker as egress hole** — use the sanctioned broker to reach a forbidden host | Yes | Broker exposes a fixed vocabulary (complete/retrieve/allowlisted-HTTP) and applies the SAME allowlist; a non-allowlisted target is denied + audited | `broker.TestHTTP_EgressAllowlistEnforced` |
| 4 | **Resource exhaustion** — fork bomb, mem bomb, infinite loop, output flood | Yes | Per-isolate bounds: CPU (ulimit `-t`), memory (`-v` where supported), file-size (`-f`), wall-clock deadline, captured-output cap; breach terminates only that isolate and fails the node closed with `ErrResourceBreach`; PID/uid containment via the namespace enforcer | `sandbox.TestResourceBounds_CPUSpinContainedAndBlastRadius`, `…_OutputCap`, `…_WallClock` |
| 5 | **Cross-run contamination** — leave state for, or read state from, another run | Yes | Ephemeral per-node isolate; scratch is created fresh and destroyed on teardown (even on panic); the warm pool never hands the same scratch to two isolates; no shared state except the typed I/O contract on the host | `sandbox.TestLifecycle_EphemeralAndAudited`, blast-radius half of `…_CPUSpinContainedAndBlastRadius` |
| 6 | **Filesystem traversal** — read host FS, other runs' data, `.git` creds, secrets store | Yes | Read-only, working-set-scoped view (`stageWorkingSet` copies only declared paths, read-only, symlinks skipped); nothing else handed over; OS FS-namespace scope from the contained enforcer | `sandbox.TestFilesystem_WorkingSetReadOnlyAndScoped` |
| 7 | **Host fallback on sandbox failure** — trip isolate creation so the tool runs on the host | Yes | Fail-closed capability gate: a required restriction that cannot be enforced → `ErrIsolateUnavailable`, tool never runs on the host | `sandbox.TestFailClosed_NoHostFallbackWhenIsolationUnavailable` |
| 8 | **Malformed tool args / results** — smuggle bad input into an impl, or a schema-violating result into a downstream node | Yes | Skill contract validated pre-execution (availability + input) and pre-propagation (output); any mismatch fails closed with a typed error naming skill + field | `skillgate.TestCheckInput_*`, `skillgate.TestCheckOutput_ViolationFailsClosed` |
| 9 | **Context-policy injection** — run a policy's model/retrieval call from inside the isolate to reach a credential | Yes | Context policies run host-side only; the credentialed call goes through the gateway/broker on the host; no policy executes in a sandbox | `registry.TestSummarization_CallsHostAndResolvesIdenticalRequest`, `broker.TestBroker_ImplementsHostServices` |
| 10 | **Silent audit gap** — an action denied but not recorded | Yes | Every denial + lifecycle transition emits a P0-tagged, secret-free metric event through the P2.5 collector | `sandboxaudit.TestSandboxDenialEmittedWithP0Tags`, `…TestFlowsThroughRealCollector` |

## Out of scope for P3 (documented, not silently dropped)

- **Kernel 0-day / container escape via a kernel bug.** Mitigated by dropped privileges, no host mounts,
  and a container/microVM isolate at the deployment layer; residual risk accepted, tracked for a
  microVM upgrade. Proven at the deployment layer by the container proof (same pattern as
  `make discovery-sandbox-proof`), not by Go unit tests.
- **Side channels** (timing, cache). Not addressed in P3.
- **Supply-chain compromise of the platform's own dependencies.** Covered by the repo's `gitleaks` +
  dependency policy, not this change.

## Platform-enforcement note

The Go layer proves the fail-closed gate, env-scrub, resource bounds, audit, and lifecycle hermetically
and cross-platform. The OS-level **network deny** and **filesystem scope** are delivered by the
deployment's container/namespace (advertised via `sandbox.NewContainedEnforcer`,
`deploy/docker-compose.sandbox.yml`) and **proven by `make sandbox-proof` + `make sandbox-proof-redcheck`**
— the `sandbox` job in `.github/workflows/ci.yml`, mirroring how the Discovery worker's least-privilege
runtime is enforced by `deploy/docker-compose.discovery.yml` and proven by `make discovery-sandbox-proof`.
On any host that cannot establish those restrictions, `NewOSEnforcer` reports them unavailable and every
untrusted-repo node fails closed — never a silent downgrade. The repo-tool node path itself is
`internal/nodeexec.Runner` (CheckInput → isolate → CheckOutput), with availability tied to
`Sandbox.CanIsolate()` via `nodeexec.SandboxBinder`.
