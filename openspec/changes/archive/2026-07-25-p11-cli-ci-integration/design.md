# Design — P11: CLI & CI Integration

Product rationale: [`../../../docs/prd/P11-cli-ci-integration.md`](../../../docs/prd/P11-cli-ci-integration.md).
Delivery counterpart: [`../p12-forge-delivery/`](../p12-forge-delivery/) and
[ADR-005](../../../docs/adr/ADR-005-forge-delivery-and-credential-posture.md).

## Context

The libraries this phase exposes already exist and are already correct: `internal/discovery` produces
the IR, `internal/transform` produces the codemod, `internal/evalharness` runs multi-seed evaluations
with confidence intervals and the tie rule. What does not exist is a way for a customer to run any of
it on their own machine beyond discovery, and a way for the results to reach the platform.

Two constraints shape everything below, and they pull against each other:

- **Adoption requires the tool to ask for nothing.** The license was chosen so the ingestion layer
  could spread freely. A tool that demands an account before it will read a repository does not become
  a default.
- **Revenue requires the platform to see something.** SUM derives from P2.5 cost events; a run executed
  entirely on a laptop emits its events into that laptop.

The resolution is not a compromise between them but a boundary: the tool asks for nothing, and
*linking* — an explicit, disclosed, authenticated act — is what makes results legible in the dashboard.
Adoption and metering are then sequential rather than opposed.

## Decision 1 — Offline-first, no account required for the local workflow

`discover`, `apply` and `eval` complete with no platform account and no network access.

**Alternative rejected — require an account to run the CLI.** Every run becomes attributable, SUM
coverage is complete from day one, and the funnel is fully instrumented. Rejected on **L3 UX** and on
strategy: complete metering of a product nobody adopts measures nothing, and the Apache-2.0 positioning
becomes incoherent. A login gate in front of "read my repo" is also the single most effective way to
lose a developer-tool evaluation in its first thirty seconds.

**Secondary benefit worth stating:** an offline guarantee is the simplest possible answer in a security
review. "It works with the network off" ends a class of question that no amount of policy documentation
resolves.

## Decision 2 — Opt-in linking, disclosed before it happens

Transmitting run data requires an explicit command and an authenticated identity, and the CLI can
render the exact payload without sending it.

**Alternative rejected — telemetry on by default with an opt-out.** Best metering coverage short of
requiring an account, and it is what most developer tools do. Rejected on **L1 安全** and trust: this
tool runs over proprietary source with the customer's own provider keys, and the blast radius of a
redaction defect under a default-on model is the company. There is also a strategic asymmetry — a tool
that is *known* not to phone home is adoptable inside organizations that would otherwise ban it, and
that population is exactly the ingestion-standard target.

**The dry-run is load-bearing, not a nicety.** "We only send metrics" is a claim. A command that prints
the bytes is evidence. The person who needs it is a security reviewer who has been lied to before, and
designing that surface for *them* is what gets a deal through review.

## Decision 3 — The payload is constructed from an allowlist, never filtered by a denylist

The linked payload is built field by field from an explicit permitted list. It is never produced by
serializing a rich object and removing sensitive fields.

**Alternative rejected — serialize the existing run object and strip what must not leave.** Far less
code, and it stays automatically in sync as the run object grows. That last property is precisely the
problem: it stays in sync by **sending new fields**. A denylist fails the first time someone adds a
field and does not update the stripper, and the failure is **silent** — the leak is discovered by
someone reading logs later, or by a customer.

The asymmetry is the whole argument, and it is worth stating as a rule rather than a preference:

| Approach | Failure mode when a field is added |
|---|---|
| Denylist (strip) | The field **is sent**. Silent. Discovered externally. |
| Allowlist (construct) | The field **is absent**. Visible as a missing feature. Discovered internally. |

**L1/L5.** One fails toward disclosure, the other toward omission. For a boundary carrying customer
source, only one of those directions is acceptable.

## Decision 4 — Structure crosses; content does not

Permitted: cost / latency / token metrics, IR **structure** (node ids, edges, model refs, pattern
labels), `config_hash` and `source_revision`, eval scores and intervals, run metadata.
Never permitted: prompt text, source code, file contents, generated diffs, environment values, provider
credentials.

**Alternative rejected — send prompt bodies so the dashboard can display them.** It would make the
console richer, and users will ask for it. Rejected on **L1**: prompt bodies are the customer's most
sensitive artifact after their source, and the dashboard's job is *comparison* — which structure and
metrics fully satisfy. Content would buy presentation and cost the security review.

This is also why the [P10](../p10-prompt-model-studio/) studio works the way it does: prompts a
customer *chooses* to author in the console are on the platform because they put them there, which is a
different act from a CLI shipping whatever it found in their repository.

## Decision 5 — Platform unavailability never fails the customer's build

A CI step reports and continues when the platform is unreachable, degraded, or slow, with a bounded
timeout. A customer-configured quality gate does fail the build.

**Alternative rejected — fail closed on link failure so no run goes unmetered.** It protects the meter.
Rejected on **L2 稳定**: the customer's pipeline outranks our metering. Failing someone's build for a
reason unrelated to their code is a stability cost we impose on them to serve our convenience, and the
priority ordering forbids buying a lower-level benefit with a higher-level degradation.

The bounded timeout matters as much as the non-failure: **a slow dependency is an outage with extra
steps**, and a CI step that hangs for ten minutes waiting on us is worse than one that fails fast.

## Decision 6 — Metering counts only linked runs; coverage is reported

SUM derives from linked runs only. The platform does not infer, extrapolate, or estimate unlinked
spend. Link coverage is a first-class read model shown wherever a derived spend figure appears.

**Alternative rejected — estimate unlinked spend from linked samples.** It would produce a
fuller-looking SUM. Rejected on honesty: a bill computed partly from an estimate of what we could not
see is indefensible in a dispute, and *"we inferred the rest"* is not a sentence that belongs in an
invoice. Reporting coverage is the honest alternative, and it converts a hidden weakness into a visible
fact — which is also a better prompt to link more runs than a silently inflated number would be.

Once metering is partial by design, **the completeness of the figure is part of the figure**. Hiding it
would make every spend number quietly unfalsifiable.

## Decision 7 — Linked events enter the existing P2.5 substrate

Linking transports events into P2.5 with the standard tag set. No dedicated ingestion service, no
second cost model, no separate store.

**Alternative rejected — a dedicated ingest service shaped for CLI runs.** Easier to build and tune.
Rejected on **L5 不可演进**: it creates a second definition of what a cost event is, at which point SUM
has two possible values. P7's rule — *"not collected by a second pipeline"* — would be satisfied in
letter and violated in spirit, and the divergence would surface as a billing dispute rather than a test
failure.

## Decision 8 — The CLI runs the P4 harness, not a local approximation

`eval` executes the same harness the platform runs: multi-seed, confidence intervals, tie rule,
disqualifying gates.

**Alternative rejected — a lightweight local scorer for speed.** Faster local loop. Rejected because it
would give a user two numbers for one question with no way to tell which is right, and the cheaper one
would be the one they see first. Statistical honesty is a P4 invariant; a second implementation is a
second place for it to be wrong.

## Decision 9 — Exit codes discriminate; stdout is machine, stderr is human

Distinct documented exit codes for success, configured-gate-failure, operational error, and invalid
configuration. Machine output on stdout in a versioned format; narration on stderr.

**Alternative rejected — one non-zero code and human-readable output.** Simpler. Rejected on **L3/L6**:
three conditions with three different remedies collapsed into one carries zero information, and a CI
step that fails unclearly gets disabled — after which the check protects nothing. Versioning the output
format matters because the moment a customer's pipeline parses it, it is a public contract.

## Interfaces sketch

```
discover  --repo --config --out --report            → IR + discovery report        (offline)
apply     --spec --repo --out                       → reviewable diff, worktree-isolated (offline)
eval      --spec --eval-set --seeds                 → scores + intervals + cost events   (offline, customer keys)
login                                               → authenticated identity        (network)
link      [--dry-run] <run>                         → allowlist payload → P2.5      (network, explicit)
status                                              → effective config + source per value

LinkedPayload = construct(                          // FR11: built, never filtered
    metrics{cost,latency,tokens}, ir_structure{node_ids,edges,model_refs,labels},
    config_hash, source_revision, scores{value,ci_low,ci_high}, run_metadata{ts,seed,tool_version})

exit: 0 ok · 1 configured-gate-failed · 2 operational-error · 3 invalid-config
```

## Risks

| Risk | Mitigation |
|---|---|
| Customer source or prompts leak through the boundary | Decision 3 — allowlist **construction**; a test that adds a field to the source struct and asserts it is **absent** from a linked payload. If that test cannot be made to fail, the guarantee is decoration. |
| A provider credential is transmitted or logged | Never read into the payload at all; assertions cover payloads, logs, check output **and uploaded artifacts** — artifacts persist and are the easy one to forget. |
| Debug mode widens the boundary | Diagnostics obey the same allowlist; a verbose flag cannot add fields. "Verbose sends more" is the classic shape of an accidental leak. |
| Our outage breaks customer pipelines | Decision 5 — report and continue, with a bounded timeout so a hung platform cannot stall a pipeline either. |
| A customer is billed against a figure that reflects a fraction of activity | Decision 6 — no extrapolation, and coverage displayed wherever the figure is. |
| Offline mode quietly depends on the network | Tested with networking **denied**, not by code inspection — a library that resolves DNS on init passes every review. |
| The CLI and platform compute different scores | Decision 8 — one harness, one implementation. |
| A compromised release runs inside every customer's CI | Signed, checksummed, reproducible builds with a documented verification step; this binary is a distribution target with repository access. |
| The output format becomes an unversioned de-facto contract | Versioned explicitly, with a loud mismatch rather than silent divergence. |
| A retried CI step double-counts a meter that becomes an invoice | Idempotent linking keyed by run identity. |
