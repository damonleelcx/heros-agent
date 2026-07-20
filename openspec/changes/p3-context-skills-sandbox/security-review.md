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

1. **Info — OS network/FS denial is deployment-layer, not Go-layer.** The Go enforcer honestly reports
   network/FS-namespace isolation as unavailable on a bare host and **fails closed** there; the real
   denial is provided by the container/namespace (`NewContainedEnforcer`) and must be proven by a
   container proof at deploy time (mirrors `make discovery-sandbox-proof`). Accepted: this is the
   documented split, and the fail-closed gate makes the bare-host case safe.
2. **Info — audit records are metadata-only by construction**, with `redactSecrets` as defense-in-depth
   and the collector scrubber as the final backstop. No secret path into an event was found.
3. **No blocking findings.** No control is missing a test; no test asserts a weaker property than its
   control claims.

## Residual risk (accepted)

- Kernel-level container escape (0-day) — mitigated by dropped privileges + no host mounts + container
  isolate; microVM upgrade tracked for a later phase.
- Timing/cache side channels — out of scope for P3.

## Sign-off

- **Threat model:** complete; every in-scope attack maps to a control and a green test.
- **Fail-closed posture:** verified — no path downgrades untrusted execution to the host.
- **Secrets:** verified never to enter the isolate or an audit event.

**Security-reviewer sign-off: APPROVED for P3 close**, conditional on the deploy-time container proof
running in CI before the sandbox handles real target repos (tracked as task 7.4's CI wiring).

_Reviewed under the senior-devops-engineer + senior-qa-engineer security discipline; recorded before
P3 close per PRD §13._
