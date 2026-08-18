# Context Policy — Spec (folded from P16)

Product rationale: [`../../../docs/prd/P16-context-strategy-optimization.md`](../../../docs/prd/P16-context-strategy-optimization.md)
§6 (FR1–FR9), §7, §8. Design reasoning:
[`../../changes/archive/2026-07-29-p16-context-strategy-optimization/design.md`](../../changes/archive/2026-07-29-p16-context-strategy-optimization/design.md)
Decisions 1, 2, 3, 6, 7; [`decisions.md`](../../changes/archive/2026-07-29-p16-context-strategy-optimization/decisions.md) D-1, D-2, D-3.

Covers context as a first-class **applicable** axis: the Go engine materializes a resolved context
policy at the call site, every other language keeps a specified and tested interim refusal, context
loss is a scored admissibility gate rather than a post-hoc diagnosis, and new policies are additions
behind the existing `Policy` interface.

> **Why the interim refusal is a requirement, not a gap.** Everything left of the transform already
> worked for context — `DimContext`, the `ContextPolicy` override, resolution into `ResolvedNode`, and
> `config_hash` participation. The only missing piece was the call-site codemod, and it is genuinely
> hard: context assembly is not an argument to swap, it is *how the surrounding code builds the message
> list*, so materializing a policy is a **region** rewrite, per language. P16 built it for Go by
> DELETING the messages a selection policy does not retain — a deletion of what the author already
> wrote, which constructs nothing and needs no SDK knowledge. A language whose rewriter has not landed
> therefore **refuses loudly** rather than silently no-op the override: a silently-dropped override is
> resolved, hashed, and scored as the base configuration under the variant's hash, which is a false
> result, the worst failure an eval platform can produce.

## Implementation evidence

| Requirement | Where it lives | Proof |
|---|---|---|
| Edit or typed refusal, never a silent drop | [`transform/contextmaterialize.go`](../../../internal/transform/contextmaterialize.go) `materializeContext` | `TestContextOverrideNeverSilentlyDropped`, `TestInterimRefusalIsLoudNotSilent` |
| Go call-site materialization | [`transform/contextmaterialize.go`](../../../internal/transform/contextmaterialize.go) + [`registry.SelectionPolicy`](../../../internal/registry/context_policies.go) | `TestGoContextMaterializes`, `TestGoContextMaterializesCompaction` |
| Deterministic materialization | pure iteration over the written elements; no map ordering | `TestGoContextMaterializationDeterministic` |
| Context is code, visible in the diff | [`transform/engine.go`](../../../internal/transform/engine.go); the binding document carries no context | `TestContextChangeAppearsInDiff`, `TestOneLineContextChangeRendersOnce` |
| Interim refusal, per language, tested | [`transform/rewrite.go`](../../../internal/transform/rewrite.go) `refuseContext`, [`rewrite_span.go`](../../../internal/transform/rewrite_span.go) dispatch | `TestSpanEngineContextRefusesNotDrops`, `TestRefusalNamesOwningPhase` |
| New policies behind the `Policy` interface | [`registry/context_policies.go`](../../../internal/registry/context_policies.go) `HierarchicalSummaryPolicy`, `StructuredExtractionPolicy`; `Store.AddPolicy` | `TestHierarchicalSummaryPolicyAddedNoSchemaChange`, `TestNewPoliciesRequiredNoSpecShapeChange` |
| Drop recorded as a scored signal | [`contextassembly/contextassembly.go`](../../../internal/contextassembly/contextassembly.go) → existing `context_drop_ratio` | `TestMaterializedDropRecordedAsSignal`, `TestLosslessPolicyPublishesNoDropEvent` |
| Drop-tolerance admissibility gate | [`proposal/dropgate.go`](../../../internal/proposal/dropgate.go), wired first in `Engine.Propose` | `TestProposalPastDropToleranceInadmissible`, `TestMeasuredDropBeatsTheEstimate` |
| Additive, omit-when-absent tolerance | [`variantspec/spec.go`](../../../internal/variantspec/spec.go), [`resolved.go`](../../../internal/variantspec/resolved.go) | `TestDropToleranceIsAdditiveToHash` |
| Host-calling policy reaches its model host-side only | [`registry/context_policies.go`](../../../internal/registry/context_policies.go) `HostServices` | `TestSummarizerRunsHostSideOnly`, `TestHostCallingPolicyRunsThroughTheSeam` |

🚫 No `Dimension`, registry schema, `ContextSpec` field, or metric name was added for this capability —
guarded structurally by `TestP16AddsNoDimension`, `TestNewPoliciesRequiredNoSpecShapeChange`, and the
no-new-metric assertion inside `TestMaterializedDropRecordedAsSignal`.

⚠️ **The honest boundary.** Go materializes SELECTION policies (`sliding-window`,
`semantic-compaction`) and treats the identity policies as equivalent to the un-rewritten call site.
A policy that assembles at RUN TIME (`summarization`, `hierarchical-summary`, `rag-retrieval`) or that
rewrites message content (`structured-extraction`) is refused at the call site by name and still runs
host-side where it belongs. Every non-Go language keeps the interim refusal. The coverage table
([`ContextMaterializerCoverage`](../../../internal/transform/contextmaterialize.go)) is the single
source of truth for all of it.

## Requirements

### Requirement: A differing context policy SHALL yield an edit or a typed refusal, never a silent drop

For a node whose resolved context policy differs from its discovered context assembly, the transform
SHALL either emit a call-site context-materialization edit or refuse with a typed error. It SHALL NOT
produce a diff that omits the context change while reporting the variant's configuration hash.

#### Scenario: A materializable policy emits an edit

- **WHEN** a node's resolved context policy differs from its discovered assembly and its language has a
  context rewriter
- **THEN** the transform emits a call-site edit that materializes the resolved policy
- **AND** the variant's diff reflects the context change.

#### Scenario: A non-materializable policy refuses rather than dropping

- **WHEN** a node's resolved context policy differs from its discovered assembly and its language has no
  context rewriter yet
- **THEN** the transform returns a typed refusal
- **AND** the override is neither applied as the base configuration nor silently omitted from the diff.

### Requirement: The Go engine SHALL materialize a resolved context policy at the call site

The Go transform engine SHALL materialize a resolved context policy by rewriting the call site's
message-assembly region so the assembled message list is the one the policy defines.

#### Scenario: A windowing policy is materialized on a Go node

- **WHEN** a Go node resolves to a `sliding-window` context policy
- **THEN** the transform rewrites the message-assembly region so the assembled context is the policy's
  windowed message list
- **AND** the change is a real code edit, not an argument swap.

#### Scenario: Materialization is deterministic

- **WHEN** the same configuration hash at the same source revision and seed is materialized twice for an
  LLM-free policy
- **THEN** the two diffs are byte-identical.

### Requirement: A context change SHALL be materialized as code, visible in the diff

A materialized context change SHALL be treated as code under the apply-mode rule: the resolved assembly
SHALL appear in the same diff a reviewer reads. No apply mode SHALL hide a context change behind an
indirection that omits the resolved values from review.

#### Scenario: The resolved assembly appears in the reviewable diff

- **WHEN** a context policy is materialized for a node
- **THEN** the resolved message-assembly change is present in the diff
- **AND** no apply mode renders the change as an opaque handle that hides the assembly.

### Requirement: The interim refusal SHALL be specified and tested per language

A node carrying a context policy not yet call-site-applicable in its language SHALL be refused at
transform with a typed error naming the node, the policy, and the reason. This refusal SHALL be a
tested behavior for that language until its rewriter lands, and SHALL NOT be a silent no-op.

#### Scenario: An unbuilt language refuses loudly

- **WHEN** a context override is applied to a node in a language whose context rewriter has not landed
- **THEN** the transform returns a typed refusal naming the node and the policy
- **AND** a test asserts the refusal occurs and the override is not applied as the base configuration.

#### Scenario: The refusal names the owning phase

- **WHEN** the refusal reason is read
- **THEN** it names the phase that owns the context-materialization rewrite
- **AND** it does not direct the reader to a phase that does not implement it.

### Requirement: A new context policy SHALL be added behind the Policy interface without a schema change

A new context policy SHALL be added by implementing the Policy interface (name, params schema, assemble)
and registering it through the policy-registration seam. Adding a policy SHALL NOT change the registry
schema, the context-spec shape, or the dimension enum.

#### Scenario: A new policy is a new implementation and a row

- **WHEN** a new policy such as `hierarchical-summary` is introduced
- **THEN** it is added as a Policy implementation registered through the policy-registration seam
- **AND** no registry schema, context-spec, or dimension-enum change is required.

#### Scenario: A policy validates its own params at registration

- **WHEN** a context entry is registered naming a policy with params that violate the policy's params
  schema
- **THEN** registration is rejected
- **AND** the rejection happens at registration time, not when a run reaches the node.

### Requirement: Context-loss SHALL be modeled as a scored quality signal

A lossy policy's dropped-token ratio SHALL be recorded per node per run through the existing
context-drop telemetry and made available to scoring and diagnosis. A measured zero drop SHALL be
distinguishable from an unmeasured lossless policy.

#### Scenario: A lossy materialization records its observed drop

- **WHEN** a materialized lossy policy assembles context for a run
- **THEN** its observed drop ratio is recorded through the existing context-drop telemetry
- **AND** it is available to scoring and diagnosis as a quality signal.

#### Scenario: Measured zero is distinct from unmeasured

- **WHEN** a lossless policy assembles context
- **THEN** its lossless nature is recorded distinctly from a lossy policy that measured a zero drop
- **AND** a zero from a lossless policy is not read as "measured no drop".

### Requirement: A proposal exceeding a node's drop tolerance SHALL be inadmissible

Each node MAY carry a drop tolerance. A proposal whose resolved policy would drive that node's drop
ratio past its tolerance SHALL be inadmissible — rejected at proposal admissibility, before transform
and before any evaluation spend. The tolerance SHALL be an additive, omit-when-absent attribute.

#### Scenario: A too-lossy proposal is rejected before eval spend

- **WHEN** a proposal's resolved policy would drive a node's drop ratio past that node's tolerance
- **THEN** the proposal is inadmissible
- **AND** it is rejected before transform and before any evaluation run.

#### Scenario: A node with no tolerance hashes byte-identically to before

- **WHEN** a node declares no drop tolerance
- **THEN** its serialized override and resolved node omit the tolerance field entirely
- **AND** its configuration hash is byte-identical to the same node before the tolerance attribute existed.

### Requirement: A host-calling context policy SHALL reach its model only host-side

A context policy that requires a summarizer or other model SHALL reach it only through the trusted-host
services gateway, executing host-side. The resolved request it issues SHALL be captured as the
determinism handle.

#### Scenario: A summarizer runs on the trusted host

- **WHEN** a `summarization` policy assembles context
- **THEN** the summarizer call is made host-side through the host-services gateway
- **AND** no provider credential is exposed to a sandboxed node.

#### Scenario: The resolved request is the determinism handle

- **WHEN** a host-calling policy assembles context twice under the same policy, params, conversation and
  seed
- **THEN** it issues an identical resolved request both times
- **AND** determinism is asserted at the resolved-request level, not at the provider's output bytes.
