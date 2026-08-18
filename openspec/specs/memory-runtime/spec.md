# Memory Runtime — Spec (folded from P18)

Product rationale: [`../../../docs/prd/P18-memory-runtime.md`](../../../docs/prd/P18-memory-runtime.md)
§6 (FR1–FR8), §7. Design reasoning: [`../../changes/archive/2026-07-31-p18-memory-runtime/design.md`](../../changes/archive/2026-07-31-p18-memory-runtime/design.md) Decisions 2, 3, 4, 5;
[`../../changes/archive/2026-07-31-p18-memory-runtime/decisions.md`](../../changes/archive/2026-07-31-p18-memory-runtime/decisions.md) D1, D3, D4, D5.

Covers the first of the two artifacts [P17](../../changes/archive/2026-08-01-p17-memory-strategy-optimization)'s refusal named as
missing: a memory **runtime** — a store, a key scheme, a lifetime, and the `Recall`/`Record` semantics of
the five sealed builtin strategies.

> **What "runtime" means here, and what it deliberately excludes.** This runtime backs a module that ships
> **into the customer's repository and runs in their process**. That single fact decides most of what
> follows: it calls no provider, it reads no credential, it imports nothing, and it never reads a clock.
> Summarization and embedding are **injected**, and a strategy needing a service it was not given
> **refuses** rather than substituting a cheaper behaviour — because a `summary-buffer` that quietly
> truncates *is* `scratchpad`, running under a `config_hash` that says otherwise.

## Requirements

### Requirement: A memory entry SHALL be scoped by node and session, both required

A memory entry SHALL be scoped by a key of `node_id` and `session_id`. Both parts SHALL be required: a key
missing either part SHALL produce a typed error, and the runtime SHALL NOT supply a default for either.

#### Scenario: Entries are scoped per call site and per conversation

- **WHEN** two sessions record memory at the same node
- **THEN** a recall in one session returns only that session's entries
- **AND** a recall at a different node returns neither session's entries for the first node.

#### Scenario: An incomplete key fails closed

- **WHEN** a recall or a record is attempted with an empty node id or an empty session id
- **THEN** it returns a typed invalid-key error
- **AND** no entry is read or written under any other scope.

#### Scenario: The runtime never invents a session

- **WHEN** a caller supplies no session id
- **THEN** the operation is refused
- **AND** the entries are not merged into a shared or default scope.

### Requirement: Every builtin strategy SHALL implement recall and record through one dispatch

All five builtin strategies SHALL implement both `Recall` and `Record`, resolved through a single dispatch
over the closed set. A strategy present in the sealed vocabulary without a runtime implementation SHALL
fail loudly rather than behave as a no-op.

#### Scenario: The vocabulary and the runtime agree

- **WHEN** the builtin strategy set is enumerated
- **THEN** every member resolves to a recall implementation and a record implementation.

#### Scenario: A strategy with no implementation fails loudly

- **WHEN** a strategy name outside the implemented set reaches the runtime
- **THEN** it returns a typed error
- **AND** it does not silently return the input unchanged.

### Requirement: Recall SHALL be deterministic

Recall SHALL be a pure function of the store's contents, the strategy, and its params. Identical store
state and identical params SHALL produce byte-identical output, on any machine and on any repetition.

#### Scenario: Repeated recall is identical

- **WHEN** recall runs twice against unchanged store state with the same params
- **THEN** the two outputs are byte-identical.

#### Scenario: Ordering does not depend on a clock

- **WHEN** entries are recorded in sequence
- **THEN** their order is determined by a store-assigned monotonic sequence
- **AND** no wall-clock timestamp participates in ordering or selection.

### Requirement: The lifetime SHALL be count-based

Expiry SHALL retain a caller-specified number of the most recent entries. No wall-clock time-to-live SHALL
affect what a recall returns.

#### Scenario: Expiry keeps the most recent entries

- **WHEN** expiry is applied with a retention count lower than the number of stored entries
- **THEN** exactly the most recent entries up to that count remain
- **AND** the number dropped is reported.

#### Scenario: Elapsed time changes nothing

- **WHEN** recall runs against unchanged store state after an arbitrary interval
- **THEN** its output is unchanged.

### Requirement: Every strategy SHALL be bounded by construction

Each strategy SHALL enforce its own bound: `scratchpad` SHALL NOT retain more than its entry limit,
`summary-buffer` SHALL NOT exceed its token budget, and `vector-recall` SHALL NOT return more than its
top-k. A strategy SHALL NOT be able to grow a store without bound.

#### Scenario: A strategy respects its bound under sustained writes

- **WHEN** more turns are recorded than a strategy's bound permits
- **THEN** recall returns no more than the bound
- **AND** the stored entry count does not grow without limit.

### Requirement: The runtime SHALL make no provider call

The runtime SHALL NOT call any model provider, retriever, or network service. Summarization and embedding
SHALL be supplied as injected host services.

#### Scenario: The runtime issues no outbound call

- **WHEN** any strategy's recall or record runs
- **THEN** no provider, retriever, or network call is issued by the runtime itself.

#### Scenario: A missing host service is refused, not substituted

- **WHEN** a strategy requiring a summarizer or an embedder runs without one supplied
- **THEN** it returns a typed refusal naming the missing service
- **AND** it does not fall back to truncation, recency, or any other behaviour.

### Requirement: A strategy's behaviour SHALL have exactly one definition

The retention and recall semantics of a strategy SHALL be defined in one place, read by the runtime and by
the call-site materializer alike. Generated code SHALL call that definition rather than re-implement it.

#### Scenario: The generated artifact defers to the runtime

- **WHEN** the generated memory module performs a recall
- **THEN** it invokes the shared definition
- **AND** it contains no independent implementation of retention.

#### Scenario: Changing a strategy changes both readers together

- **WHEN** a strategy's retention semantics change
- **THEN** the runtime and the materialized behaviour change together
- **AND** no second implementation can disagree with the first.
