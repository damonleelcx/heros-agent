# Dynamic Tracing — P5 Delta

Cross-reference: [`../../../../docs/prd/P5-contracts-rearrange-tracing.md`](../../../../docs/prd/P5-contracts-rearrange-tracing.md) §6 (FR15–FR22).

Static analysis produced a *candidate* graph; dynamic tracing confirms it by instrumenting a real run,
reconciles the observed calls against the static candidates, confirms behavioral patterns topology
could only guess, detects anti-patterns, and enriches the P4 eval-set generator.

## ADDED Requirements

### Requirement: An OTel-style interceptor SHALL log every real LLM call with its inputs and its stack

The interceptor SHALL wrap the SDK entrypoints in the signature registry and log **every real LLM
call**, its **inputs**, and its **call stack**, each event correlated to a P2.5 span and tagged with the
P0 tag set `{variant_id, run_id, node_id, case_id, seed, timestamp, config_hash}`.

#### Scenario: Every LLM call in an instrumented run is captured
- **WHEN** a workflow run is instrumented and makes several real LLM calls
- **THEN** each call is logged with its inputs and its call stack
- **AND** each logged event carries the full P0 tag set and correlates to a span.

### Requirement: The interceptor SHALL be passive and SHALL redact secrets and PII

The interceptor SHALL NOT alter the traced workflow's behavior or outputs, logging SHALL be
best-effort/async so a logging failure never fails the run, and secrets/PII SHALL be redacted — inputs
stored as content-hashed blobs, secrets sourced from the manager and never present in trace logs,
stacks, or the reconciliation report. The instrumented run SHALL execute in the P3 sandbox with no
ambient credentials.

#### Scenario: Instrumentation does not change the run's outputs
- **WHEN** the same `{config_hash, seed}` workflow is run once traced and once untraced
- **THEN** the two runs produce identical outputs
- **AND** a failure in the logging path does not fail the traced run.

#### Scenario: No secret or PII appears in trace artifacts
- **WHEN** an instrumented run completes and its logs, stacks, and reconciliation report are inspected
- **THEN** no provider secret appears in any of them
- **AND** logged call inputs are stored as content-hashed blobs, not inline.

### Requirement: The reconciler SHALL reconcile observed calls against static candidates and surface runtime-only edges

The reconciler SHALL match each observed call to a **static candidate** node, marking each candidate
**confirmed** (observed) or **unconfirmed** (not observed on the traced run) and each observed call
**matched** or **runtime-only**. A **runtime-only edge or node that static analysis missed** (a
conditional branch, a loop-back, dispatch through an unresolved wrapper) SHALL be surfaced and
reconciled into the IR **additively** (same `ir_version` MAJOR), marked as observed-at-runtime.

#### Scenario: Dynamic tracing reveals a runtime-only edge static analysis missed
- **WHEN** a conditional router activates a branch at runtime that static analysis did not resolve into
  an edge
- **THEN** the reconciler flags the observed branch as a **runtime-only edge**
- **AND** it adds the edge to the IR additively, marked observed-at-runtime
- **AND** the static candidates that were not exercised on this run are marked **unconfirmed**, not
  deleted.

#### Scenario: Reconciliation is reproducible
- **WHEN** the same `{config_hash, seed}` traced run is reconciled twice
- **THEN** the confirmed/unconfirmed/runtime-only verdicts are identical
- **AND** the reconciliation report is content-addressed and attributable to the exact run.

### Requirement: The reconciler SHALL distinguish a static node definition from its runtime invocations

The reconciler SHALL map one **static node definition** to its **many runtime invocations**, each
carrying an `invocation_index` (0..n−1) as P0 specifies, so runtime-dynamic dispatch (loops, conditional
routing) is resolved concretely — a loop is one definition with n invocations, never n definitions.

#### Scenario: A looping agent is one definition and many invocations
- **WHEN** a self-looping agent node executes its LLM call 7 times in one traced run
- **THEN** the reconciler records **one** static definition and **7** runtime invocations with
  `invocation_index` 0..6
- **AND** the node count remains one definition, not seven.

### Requirement: Behavioral tracing SHALL confirm patterns from runtime evidence and wire the pattern to its metric-set

From trace evidence the classifier SHALL upgrade a P3.5 structural candidate to a **confirmed** label
(`source = behavioral`, written back additively): iteration count > 1 on a self-edge → **Reflection**; a
planning node emitting a task list consumed downstream → **Planning**; sampling one node N times then
voting → **Self-Consistency (Reasoning Techniques)**; memory read/write against a store between turns →
**Memory Management**; a human-approval pause in the trace → **Human-in-the-Loop**. A confirmed label
SHALL select the pattern's metric-set / failure-taxonomy / eval-targeting (reusing the P3.5 mapping). A
structural candidate whose runtime evidence is absent SHALL NOT be confirmed.

#### Scenario: Behavioral tracing confirms Reflection via iteration count greater than one
- **WHEN** a node's output loops back to a generate node and the trace shows the self-edge iterated **3**
  times
- **THEN** the classifier confirms **Reflection** for that subgraph with `source = behavioral`
- **AND** the confirmed label selects the Reflection metric-set (iteration-count, convergence,
  quality-gain-per-revision).

#### Scenario: A one-shot self-edge is not confirmed as Reflection
- **WHEN** a node has a self-edge but the trace shows it executed exactly **once**
- **THEN** the classifier does **not** confirm Reflection from that run
- **AND** the Reflection metric-set is not selected for that subgraph.

### Requirement: Behavioral tracing SHALL emit anti-pattern diagnoses from runtime evidence

The classifier SHALL emit typed **anti-pattern** diagnoses from trace evidence — a Reflection loop whose
quality does not improve across iterations; a Router that sends (nearly) all traffic to one branch;
Parallelization whose branches are not actually independent; a Plan that is never followed — each as a
structured diagnosis (pattern, subgraph, evidence) consumable by P5.5. An anti-pattern SHALL be a
**diagnosis, not an applied fix**.

#### Scenario: A reflection loop that never improves is flagged as an anti-pattern
- **WHEN** the trace shows a Reflection loop iterating multiple times with no quality gain across
  iterations
- **THEN** the classifier emits a typed anti-pattern diagnosis for that subgraph with the per-iteration
  quality evidence attached
- **AND** the diagnosis is surfaced for P5.5, not auto-fixed in P5.

### Requirement: Dynamic tracing SHALL enrich the P4 eval-set generator with trace seeds and per-path targeting

The system SHALL make the observed trace inputs available to the P4 eval-set generator as **seed
cases**, and SHALL enable **per-path targeting** — generating cases that force each reconciled path,
including runtime-only edges and loop min/typical/max iteration counts.

#### Scenario: Real trace inputs seed the generator and a per-path target forces a runtime-only edge
- **WHEN** an instrumented run has been reconciled and its inputs mined
- **THEN** the observed trace inputs appear as seed cases in the P4 generator
- **AND** a per-path target generates a case that forces the reconciled runtime-only edge.
