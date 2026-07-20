# P3 Exit Checklist Verification (task 7.5)

Each acceptance criterion from PRD §13, with the test(s) proving it. All green under
`go test ./internal/{registry,telemetry,skillgate,sandbox,broker,sandboxaudit,p3e2e}/`.

| PRD §13 criterion | Proven by | Status |
|-------------------|-----------|--------|
| Repo tool executes inside the sandbox with **no ambient credentials** (cred-read finds nothing) | `sandbox.TestNoAmbientCredentials`, `sandbox.TestMaliciousRepoToolSet_Contained/reads-*` | ✅ |
| Context policy **swappable per node via config alone** (assembly + `config_hash` change, no code change) | `registry.TestContextEntry_ConfigSwapChangesContentAddressOnly`, `TestTwoNodes_DifferentPoliciesAssembleIndependently` | ✅ |
| **All five policies** implemented behind the P2 interface with validated typed params | `registry.TestBuiltinPolicies_AllFiveSpecNamesPresent`, `TestPolicyFamily_MatchesContextEngineeringDiscipline` | ✅ |
| Context assembly is **deterministic** given policy + params + conversation + seed | `registry.TestSlidingWindow_KeepsTailAndIsDeterministic`, `TestSemanticCompaction_BoundsTokensAndReportsDrop`, `TestSummarization_CallsHostAndResolvesIdenticalRequest` | ✅ |
| Skill args violating the schema **rejected before running**; bad output **discarded before propagation** | `skillgate.TestCheckInput_ArgSchemaViolationNamesField`, `TestCheckOutput_ViolationFailsClosed`, `p3e2e.TestSandboxOutput_GatedByContractBeforePropagation` | ✅ |
| Malformed skill **contract rejected at registration** | `registry.TestRegisterSkill_RejectsBeforeTouchingTheDatabase/invalid_*_schema` | ✅ |
| Repo tool **network egress denied** and the attempt is **recorded** | `broker.TestHTTP_DefaultDeny`, `TestHTTP_EgressAllowlistEnforced`; recorded via `sandboxaudit.TestBrokerDenialEmitted` | ✅ |
| **Resource-exhausting** tool contained; isolate terminated, node fails closed, other runs unaffected | `sandbox.TestResourceBounds_CPUSpinContainedAndBlastRadius`, `_OutputCap`, `_WallClock` | ✅ |
| **Filesystem-scope violation** denied | `sandbox.TestFilesystem_WorkingSetReadOnlyAndScoped`, `TestMaliciousRepoTool_WriteOutsideWorkingSet_FailsClosedWithoutFSScope` | ✅ |
| Isolate creation **fails closed** — no host fallback | `sandbox.TestFailClosed_NoHostFallbackWhenIsolationUnavailable` | ✅ |
| Needed LLM/retrieval call **brokered by the host**; isolate never holds the credential | `broker.TestComplete_HostPerformsCallIsolateHoldsNoCredential`, `p3e2e.TestBrokerBoundary_CredentialNeverInIsolate` | ✅ |
| Every denial + contract rejection emits a **tagged audit event**, no secret values | `sandboxaudit.TestSandboxDenialEmittedWithP0Tags`, `TestFlowsThroughRealCollector`, `broker.TestAudit_SecretFree` | ✅ |
| **Security-reviewer sign-off** against the threat model recorded | [`security-review.md`](security-review.md) — APPROVED | ✅ |

## Deferred to deploy-time CI (tracked, not silently dropped)

- OS-level network/FS-namespace **denial proof** runs at the deployment layer (container proof, mirrors
  `make discovery-sandbox-proof`). The Go layer proves the fail-closed gate that makes the bare-host
  case safe; the container proof must run in CI before the sandbox handles real target repos. This is
  the one condition on the security sign-off.

**P3 exit checklist: GREEN** (Go-provable criteria all pass; the single deploy-time container proof is
tracked as the sign-off condition).
