# P3 Security Review — Sign-off (task 5.4)

Reviewed against [`threat-model.md`](threat-model.md). The review verifies, for every in-scope attack,
that (a) a control denies it and (b) a test proves the control. Sign-off is recorded only with all
adversarial tests green.

## Verification (attack → control → test verdict)

| # | Attack | Control present | Test green | Verdict |
|---|--------|-----------------|-----------|---------|
| 1 | Credential theft | scrubbed env, HOME→scratch, no mounts, broker holds secrets | `TestNoAmbientCredentials` | ✅ PASS |
| 2 | Network exfiltration | default-deny egress + broker allowlist | `TestHTTP_DefaultDeny`, `TestHTTP_EgressAllowlistEnforced` | ✅ PASS |
| 3 | Broker as egress hole | fixed vocabulary + same allowlist + audit | `TestHTTP_EgressAllowlistEnforced` | ✅ PASS |
| 4 | Resource exhaustion | CPU/mem/file/wall-clock/output bounds; terminate isolate only | `TestResourceBounds_CPUSpinContainedAndBlastRadius`, `_OutputCap`, `_WallClock` | ✅ PASS |
| 5 | Cross-run contamination | ephemeral per-node isolate; single-use scratch | `TestLifecycle_EphemeralAndAudited` + blast-radius | ✅ PASS |
| 6 | Filesystem traversal | RO working-set-scoped view; symlinks skipped | `TestFilesystem_WorkingSetReadOnlyAndScoped` | ✅ PASS |
| 7 | Host fallback on failure | fail-closed capability gate | `TestFailClosed_NoHostFallbackWhenIsolationUnavailable` | ✅ PASS |
| 8 | Malformed args/results | contract validated pre-exec + pre-propagation, typed error | `TestCheckInput_*`, `TestCheckOutput_ViolationFailsClosed` | ✅ PASS |
| 9 | Context-policy credential reach | policies host-side only; broker seam | `TestSummarization_CallsHostAndResolvesIdenticalRequest`, `TestBroker_ImplementsHostServices` | ✅ PASS |
| 10 | Silent audit gap | P0-tagged secret-free denial + lifecycle events | `TestSandboxDenialEmittedWithP0Tags`, `TestFlowsThroughRealCollector` | ✅ PASS |

## Findings

1. **Resolved — OS network/FS denial now has a machine-checked container proof in CI.** The Go enforcer
   honestly reports network/FS-namespace isolation as unavailable on a bare host and **fails closed**
   there; the real denial is provided by the container (`NewContainedEnforcer` /
   `deploy/docker-compose.sandbox.yml`) and is now proven by `make sandbox-proof` +
   `make sandbox-proof-redcheck`, wired as the `sandbox` job in `.github/workflows/ci.yml` (mirrors the
   discovery-sandbox proof). Static + dynamic checks assert no egress (incl. metadata endpoint), a
   read-only working-set FS, no ambient creds, and cgroup resource bounds; the red-check proves the
   proof can fail.
2. **Resolved — skill availability is now sandbox-backed.** `nodeexec.SandboxBinder` ties a `repo:` impl
   handle's bindability to `Sandbox.CanIsolate()`, so on a host that cannot isolate a repo tool is
   unavailable and its node fails closed instead of running unsandboxed. `nodeexec.Runner` wires the
   full node-execution path: `CheckInput` → isolate run → `CheckOutput`, fail-closed at each stage.
3. **Info — audit records are metadata-only by construction**, with `redactSecrets` as defense-in-depth
   and the collector scrubber as the final backstop. No secret path into an event was found.
4. **No blocking findings.** No control is missing a test; no test asserts a weaker property than its
   control claims.

## Residual risk (accepted)

- Kernel-level container escape (0-day) — mitigated by dropped privileges + no host mounts + container
  isolate; microVM upgrade tracked for a later phase.
- Timing/cache side channels — out of scope for P3.

## Sign-off

- **Threat model:** complete; every in-scope attack maps to a control and a green test.
- **Fail-closed posture:** verified — no path downgrades untrusted execution to the host.
- **Secrets:** verified never to enter the isolate or an audit event.

**Security-reviewer sign-off: APPROVED for P3 close.** The prior condition — a deploy-time container
proof in CI — is now satisfied: `make sandbox-proof` + `sandbox-proof-redcheck` run as the `sandbox`
job in CI, asserting the OS-level egress/FS/creds/resource posture the Go layer defers to the container.

_Reviewed under the senior-devops-engineer + senior-qa-engineer security discipline; recorded before
P3 close per PRD §13._
