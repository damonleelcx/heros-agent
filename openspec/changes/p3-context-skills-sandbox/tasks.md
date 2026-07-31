# Tasks — P3: Context strategies + Skill registry + Sandbox

## 1. Backend — Context policy plugins (`context-strategies`)
- [x] 1.1 Define each policy's typed **params schema**: `sliding-window {window_size}`,
      `summarization {summarizer_model_ref}`, `rag-retrieval {top_k, retriever_ref}`,
      `semantic-compaction {target_tokens}`; `full-history` has no params.
- [x] 1.2 Implement `full-history` and `sliding-window` (deterministic, host-side, no LLM) behind the
      P2 `Policy.Assemble(conversation, params, seed)` interface.
- [x] 1.3 Implement `summarization` — calls the `summarizer_model_ref` via the **provider gateway on
      the trusted host** (never from a sandbox); seed threaded for reproducibility.
- [x] 1.4 Implement `rag-retrieval` — resolves `retriever_ref`, retrieves top-k on the host; optional
      rerank carries the seed.
- [x] 1.5 Implement `semantic-compaction` to a `target_tokens` bound; emit the compaction/drop ratio.
- [x] 1.6 **Param validation fails closed** at resolution: reject out-of-range/malformed params
      (e.g. negative `window_size`, `top_k` ≤ 0) before the node executes.
- [x] 1.7 **Per-node swap via config alone:** changing `context_policy`/params changes `config_hash`
      with no workflow-code change; assert the swap end to end.
- [x] 1.8 **Determinism:** same policy + params + conversation + seed → byte-identical assembly
      (LLM-free policies) / identical resolved request (LLM policies).
- [x] 1.9 Emit context-assembly telemetry (assembled tokens, source-message count, drop/compaction
      ratio, retrieved-chunk count) tagged with the P0 tag set.

## 2. Backend — Skill Registry hardening (`skill-registry`)
- [x] 2.1 Add first-class `input_schema` and `output_schema` (JSON Schema) columns to Skill Registry
      entries; keep versioning/immutability from P2. *(P2 baseline: `SkillSpec.{Input,Output}Schema`
      hashed into the immutable `version_id`.)*
- [x] 2.2 **Registration-time validation:** reject an entry whose input/output schema is not
      well-formed JSON Schema. *(P2 baseline: `RegisterSkill` → `compileSchema`, tested.)*
- [x] 2.3 **Pre-execution input validation:** before invoking a skill, validate (a) tool availability
      (version resolves + impl handle bindable in-sandbox) and (b) argument object conforms to the
      input schema. *(`skillgate.Gate.CheckInput`; `Binder` is the in-sandbox bindability seam.)*
- [x] 2.4 **Pre-propagation output validation:** validate the tool result against the output schema
      before it feeds a downstream node. *(`skillgate.Gate.CheckOutput`.)*
- [x] 2.5 **Fail closed** on any mismatch: unavailable tool / arg-schema violation → impl not
      invoked; output-schema violation → result discarded; typed error names skill + violated field.
      *(`skillgate.ContractError{Skill, Field, Kind}` wraps `ErrContract`.)*
- [x] 2.6 Ensure validation is deterministic given `version_id` + args and does not read ambient host
      state. *(Gate reads only the content-addressed row + args; default binder inspects the handle only.)*

## 3. DevOps — Sandbox isolate (`sandbox`)
- [x] 3.1 Implement the isolate runner (subprocess with OS sandboxing and/or container); repo tool
      code executes **only** inside the isolate, separate from the host process.
      *(`sandbox.Sandbox.Run` → `SubprocessEnforcer`; tool runs as an isolated subprocess.)*
- [x] 3.2 **No ambient credentials:** start the isolate with a scrubbed environment, no credential
      mounts, no secrets-manager access; block the cloud metadata endpoint.
      *(`scrubbedEnv`; HOME/TMPDIR → scratch; metadata endpoint unreachable via the network-deny layer.
      Tested: `TestNoAmbientCredentials`.)*
- [x] 3.3 **Least-privilege network:** default-deny egress; explicit allowlist only (default empty);
      block + record any un-allowlisted outbound connection.
      *(`EgressPolicy` default-deny; direct egress denied by the contained enforcer's network layer;
      the broker records every un-allowlisted attempt — `TestHTTP_EgressAllowlistEnforced`.)*
- [x] 3.4 **Least-privilege filesystem:** read-only, minimal view scoped to the node's declared
      working set; no host FS, other-run data, `.git` credentials, or secrets store.
      *(`stageWorkingSet` copies the working set read-only; nothing else is handed over.
      Tested: `TestFilesystem_WorkingSetReadOnlyAndScoped`.)*
- [x] 3.5 **Resource bounds:** CPU, memory, wall-clock, process/PID count, output size; breach
      terminates the isolate and fails the node closed with a typed resource error; other runs
      unaffected. *(ulimit shim + wall-clock + output cap; `ErrResourceBreach`. Tested:
      `TestResourceBounds_*` incl. blast-radius.)*
- [x] 3.6 **Fail-closed isolation:** if an isolate cannot be created with the required restrictions,
      the node does **not** run the tool on the host as a fallback.
      *(Capability gate → `ErrIsolateUnavailable`. Tested: `TestFailClosed_NoHostFallback…`.)*
- [x] 3.7 Ephemeral lifecycle: destroy the isolate after each node; no state persists between nodes
      except via the typed I/O contract through the host. Warm pool to amortize container start.
      *(`Destroy` removes scratch; `warmPool`. Tested: `TestLifecycle_EphemeralAndAudited`.)*

## 4. DevOps + Backend — Credentialed broker
- [x] 4.1 Implement the host-side **broker** channel: a sandboxed tool requests an LLM/retrieval/
      allowlisted call; the host performs it via the gateway (with real secrets) and returns only the
      result. The isolate never holds the credential.
      *(`broker.Broker.Complete/Retrieve`; `TestComplete_HostPerformsCallIsolateHoldsNoCredential`.)*
- [x] 4.2 The broker applies the **same** validation + least-privilege rules; a tool cannot use it to
      reach a non-allowlisted host or bypass egress deny.
      *(`Broker.HTTP` applies `EgressPolicy`; `TestHTTP_EgressAllowlistEnforced`, `TestHTTP_DefaultDeny`.)*
- [x] 4.3 Audit every brokered call; scrub secret values from the audit record.
      *(`AuditRecord` is metadata-only + `redactSecrets`; `TestAudit_SecretFree`.)*

## 5. DevOps — Threat model, audit events, security review
- [x] 5.1 Write the **untrusted-code threat model**: credential theft, network exfiltration, host/
      container escape, resource exhaustion, cross-run contamination, filesystem traversal — each
      mapped to the control that denies it and the test that proves it. *(`threat-model.md`.)*
- [x] 5.2 Emit a tagged **audit event** for every denied action (egress block, credential-read
      attempt, resource breach, FS-scope violation) and isolate lifecycle transition; no secret
      values in the event. *(`sandboxaudit.Adapter`; `sandbox.Event`/`broker.AuditRecord` are
      secret-free; `TestSandboxDenialEmittedWithP0Tags`, `TestLifecycleEmitted`.)*
- [x] 5.3 Wire the sandbox/context telemetry into the P2.5 substrate (tool-error rate, context-window
      utilization, sandbox-denial rate). *(`sandboxaudit.Adapter` → `telemetry.Collector`;
      `MetricSandboxDenial`/`MetricToolError`; context metrics via `telemetry.EmitContextAssembly`;
      `TestFlowsThroughRealCollector`.)*
- [x] 5.4 **Security-reviewer sign-off** against the threat model (attack → control → test mapping)
      recorded before P3 closes. *(`security-review.md` — APPROVED.)*

## 6. AI Engineer (support) — Context-policy semantics
- [x] 6.1 Confirm the policy family + params match the context-engineering discipline (full / sliding
      window / summarization / RAG just-in-time retrieval / semantic compaction).
      *(`context-semantics.md`; `TestPolicyFamily_MatchesContextEngineeringDiscipline`.)*
- [x] 6.2 Ensure the params surface reserves room for P5.5 operators (just-in-time retrieval,
      sub-agent context isolation) so no breaking change is needed later. *(Additive: new operator =
      new policy via `AddPolicy`; per-policy schema; extensible `AssembledContext`. See doc.)*
- [x] 6.3 Require lossy policies (summarization, compaction) to emit drop/compaction ratios so a
      "compaction dropped the answer" is later diagnosable in P4. *(`Lossy`+`DropRatio`; drop-ratio
      event lossy-only; `TestPolicyFamily…`, `TestEmitContextAssembly_*`.)*

## 7. Testing & review
- [x] 7.1 Fixtures: a long conversation exercising every policy boundary; a **malicious repo-tool**
      set (reads env/cred files, attempts egress, fork/memory bomb, writes outside working set,
      returns output violating its schema). *(`registry.TestLongConversation_EveryPolicyBoundary`;
      `sandbox.TestMaliciousRepoToolSet_Contained` + `…_WriteOutsideWorkingSet…`;
      `sandboxe2e.TestSandboxOutput_GatedByContractBeforePropagation`.)*
- [x] 7.2 Context tests: per-node config swap changes assembly + `config_hash` only; determinism per
      policy; param validation fails closed. *(`registry` context_policies_test: `TestContextEntry_…`,
      determinism tests per policy, `TestParams_FailClosed`.)*
- [x] 7.3 Skill-contract tests: malformed contract rejected at registration; arg-schema violation →
      not invoked; output-schema violation → discarded; missing tool → fail closed.
      *(`registry.TestRegisterSkill_Rejects…`; `skillgate.TestCheckInput_*`, `TestCheckOutput_*`.)*
- [x] 7.4 Sandbox adversarial tests (CI): **no ambient creds**; **egress denied** (+ denial event);
      **resource bounds** contained (isolate terminated, node fails closed, second run unaffected);
      **FS scope** violation denied; **fail-closed isolate** (no host fallback); **broker boundary**.
      *(`sandbox.*`, `broker.*`, `sandboxaudit.*`, `sandboxe2e.TestBrokerBoundary_*`. OS-level egress/FS
      denial proof runs at deploy-time via the container proof, per the sign-off condition.)*
- [x] 7.5 Confirm the P3 exit checklist (PRD §13) is green, including security-reviewer sign-off.
      *(`exit-checklist.md` — GREEN; sign-off in `security-review.md`.)*
