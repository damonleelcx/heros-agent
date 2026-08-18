# Context Strategies — Spec Delta (P3)

Product rationale: [`../../../../docs/prd/P3-context-skills-sandbox.md`](../../../../docs/prd/P3-context-skills-sandbox.md) §6 (FR1–FR6).

Pluggable, named context policies behind the P2 policy interface, swappable per node via config and
deterministic given config + seed.

## ADDED Requirements

### Requirement: The system SHALL provide five named context policies each with a typed params schema

The system SHALL implement `full-history`, `sliding-window`, `summarization`, `rag-retrieval`, and
`semantic-compaction` behind the P2 pluggable policy interface. Each policy SHALL declare a typed
params schema: `sliding-window {window_size}`, `summarization {summarizer_model_ref}`,
`rag-retrieval {top_k, retriever_ref}`, `semantic-compaction {target_tokens}`; `full-history` takes
no params.

#### Scenario: All five policies are selectable by name
- **WHEN** a node's `context_policy` names any one of the five policies with params conforming to its
  params schema
- **THEN** the named policy is instantiated and assembles the node's context
- **AND** the policy's declared params (window size / summarizer model / top-k / target tokens) take
  effect

#### Scenario: Out-of-range params fail closed at resolution
- **WHEN** a node selects `sliding-window` with `window_size` ≤ 0, or `rag-retrieval` with
  `top_k` ≤ 0, or otherwise violates a policy's params schema
- **THEN** resolution fails closed with a typed error naming the policy and the invalid param
- **AND** the node does not execute with an unvalidated context policy

### Requirement: A node's context policy SHALL be swappable per node via config alone

Changing a node's context policy or its params SHALL require no workflow-code change and SHALL be
expressed solely through the node's `context_policy` field in the Variant Spec. The change SHALL
produce a different `config_hash`.

#### Scenario: Swapping the policy changes assembly and config_hash only
- **WHEN** a node's `context_policy` is changed from `full-history` to `sliding-window {window_size:
  10}` and nothing else in the workflow changes
- **THEN** the node's assembled context reflects the sliding window
- **AND** the Variant Spec's `config_hash` changes
- **AND** no workflow (target-repo) code is modified

#### Scenario: Two nodes in one workflow use different policies
- **WHEN** node A is configured with `rag-retrieval {top_k: 5}` and node B with `summarization`
- **THEN** each node assembles its context under its own policy independently, selected by config

### Requirement: Context assembly SHALL be deterministic given policy, params, conversation, and seed

Given identical policy + params + input conversation + seed, context assembly SHALL be
deterministic. For policies with no LLM step (`full-history`, `sliding-window`, and deterministic
`semantic-compaction`), the assembled context SHALL be byte-identical across runs. For policies with
an LLM step (`summarization`, reranked `rag-retrieval`), the *resolved request* sent to the provider
SHALL be identical under the fixed seed (the P2 reproducibility contract).

#### Scenario: LLM-free policy is byte-identical
- **WHEN** `sliding-window {window_size: 8}` assembles the same conversation twice with the same seed
- **THEN** the two assembled contexts are byte-identical

#### Scenario: LLM-using policy produces an identical resolved request
- **WHEN** `summarization` assembles the same conversation twice with the same
  `summarizer_model_ref` and the same seed
- **THEN** the request resolved to the provider (summarizer model, params, seed, input) is identical
  across both runs

### Requirement: A context policy needing a model or retriever SHALL execute that call on the trusted host, never from a sandboxed node

A policy that requires a model (`summarizer_model_ref`) or retriever (`retriever_ref`) SHALL
reference it by registry ref, and the model/retrieval call SHALL be executed by the trusted host via
the provider gateway. The call SHALL NOT be issued from inside a sandboxed node isolate.

#### Scenario: Summarizer call runs host-side through the gateway
- **WHEN** a `summarization` policy needs to call its summarizer model
- **THEN** the call is made by the trusted host through the P2 provider gateway
- **AND** no provider credential is exposed to any sandbox isolate to make the call

### Requirement: Each context policy SHALL emit context-assembly telemetry tagged with the P0 tag set

Each policy SHALL emit telemetry describing its assembly: assembled token count, source-message
count, and where applicable the drop/compaction ratio and retrieved-chunk count. Events SHALL carry
the P0 tag set (`variant_id, run_id, node_id, case_id, seed, timestamp, config_hash`).

#### Scenario: Lossy policy reports what it dropped
- **WHEN** `semantic-compaction {target_tokens: 2000}` compacts a conversation above the target
- **THEN** a telemetry event reports the assembled token count and the compaction/drop ratio
- **AND** the event carries the full P0 tag set so P4 can slice by policy
