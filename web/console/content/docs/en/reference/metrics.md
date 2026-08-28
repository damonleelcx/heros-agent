---
title: Metric reference
tier: reference
summary: Every metric the platform emits, with its unit, its exact computation, and the code that computes it.
platform_version: 0.22.0
boundary: It states what each number measures and where it is computed. It does not tell you what a good value is — that depends on your workflow, and a benchmark stated here would be a number about somebody else's system.
generated: true
order: 3
---

This page is **generated** from the metric catalogue in `internal/telemetry` on every build.

A metric name is not a definition. "Latency" can mean total wall time, time to first token, or time excluding retries — three different numbers — so every row below states the **computation** and **cites the site that performs it**. If you are comparing one of these figures against your own measurement, the computation column is the one that matters.

## Per provider call

Emitted once for every call to a model provider.

| Metric | Unit | Computation | Computed in |
|---|---|---|---|
| `latency_total_ms` | `ms` | Wall-clock duration of the whole provider call, including every retry attempt. It is not the time of the successful attempt alone. | `internal/telemetry/metrics.go:derive` |
| `latency_ttft_ms` | `ms` | Time from the request being sent to the first streamed token arriving. Emitted only for a streaming call; a non-streaming call has no first token to time. | `internal/telemetry/metrics.go:derive` |
| `throughput_tokens_per_sec` | `tokens/s` | Completion tokens divided by the generation duration. It measures output throughput, not the total call. | `internal/telemetry/metrics.go:derive` |
| `cost_usd` | `usd` | Token counts priced by the provider's published rate for the model actually used. A model with no pricing entry emits no cost rather than a zero — a zero cost is a claim, and an absent one is a gap. | `internal/telemetry/pricing.go, applied in internal/telemetry/metrics.go:derive` |
| `tokens_prompt` | `tokens` | Input tokens the provider reported for this call. | `internal/telemetry/metrics.go:derive` |
| `tokens_completion` | `tokens` | Output tokens the provider reported for this call. | `internal/telemetry/metrics.go:derive` |
| `tokens_thinking` | `tokens` | Reasoning tokens the provider reported, where the provider reports them separately from completion. | `internal/telemetry/metrics.go:derive` |
| `tokens_cache_read` | `tokens` | Prompt tokens served from the provider's cache. | `internal/telemetry/metrics.go:derive` |
| `tokens_cache_write` | `tokens` | Prompt tokens written into the provider's cache. | `internal/telemetry/metrics.go:derive` |
| `context_window_utilization` | `ratio` | Total tokens divided by the model's context window. Emitted only when the window is known; an unknown window emits nothing rather than assuming one. | `internal/telemetry/metrics.go:derive` |
| `reliability_error` | `count` | 1 when the call ended in an error, 0 otherwise. It counts terminal outcomes, not attempts. | `internal/telemetry/metrics.go:derive` |
| `reliability_timeout` | `count` | 1 when the terminal error was a timeout, 0 otherwise. A subset of reliability_error, not a separate failure. | `internal/telemetry/metrics.go:derive` |
| `reliability_retry_count` | `count` | HTTP attempts beyond the first. A call that succeeded first time is 0, not 1. | `internal/telemetry/metrics.go:derive` |
| `reliability_rate_limit_hit` | `count` | 1 when the provider returned a rate-limit response at any point during the call, 0 otherwise. | `internal/telemetry/metrics.go:derive` |
| `context_assembled_tokens` | `tokens` | Tokens in the message list the policy actually assembled for this node invocation. It is what the model was given, not what was available to give. | `internal/telemetry/context_assembly.go:EmitContextAssembly` |
| `context_source_messages` | `count` | How many messages the policy had to select from, before assembly. The denominator the drop ratio is taken against. | `internal/telemetry/context_assembly.go:EmitContextAssembly` |
| `context_drop_ratio` | `ratio` | The fraction of the source context a LOSSY policy discarded. Emitted only for a lossy policy (summarization, semantic-compaction); a lossless policy emits nothing on this axis rather than a zero, because a zero from a summariser and a zero from a window mean opposite things. | `internal/telemetry/context_assembly.go:EmitContextAssembly` |
| `context_retrieved_chunks` | `count` | Passages a retrieval policy added. Emitted only when the count is above zero, and recorded as retrieval rather than as loss — augmentation counted as loss would make the drop-tolerance gate reject retrieval for doing exactly what it is for. | `internal/telemetry/context_assembly.go:EmitContextAssembly` |

## Per node event

Emitted when something happens TO a node that is not a provider call — a sandbox denial, an isolate lifecycle transition, a tool or skill failing closed. These carry a real `node_id`, unlike a run-scoped metric, and none of them needs a call to be in flight.

| Metric | Unit | Computation | Computed in |
|---|---|---|---|
| `sandbox_denial` | `count` | 1 per action the sandbox denied — egress, credential, filesystem, resource, or a brokered call that was refused. The class rides as the `denial_kind` dimension so a consumer can filter without the value becoming a series label. An ALLOWED brokered call is not counted: it is not an anomaly and would inflate the denial rate. | `internal/sandboxaudit/adapter.go` |
| `sandbox_lifecycle` | `count` | 1 per isolate lifecycle transition, with the transition as the `phase` dimension. | `internal/sandboxaudit/adapter.go` |
| `tool_error` | `count` | 1 when a skill or tool call fails closed, with the contract-error class as `reason` and the skill as `skill_name`. It counts failures that were REFUSED, not failures that were retried. | `internal/sandboxaudit/adapter.go:RecordSkillFailure` |

## Per run

Emitted once per run window. These carry a reserved `node_id` sentinel, because concurrency across a run is not attributable to one node.

| Metric | Unit | Computation | Computed in |
|---|---|---|---|
| `concurrency_calls_in_flight` | `calls` | Concurrent provider calls observed during the run window. Run-scoped, so it carries the reserved node_id sentinel rather than a node's. | `internal/telemetry/monitor.go` |
| `throughput_calls_per_sec` | `calls/s` | Provider calls per second over the run window. | `internal/telemetry/monitor.go` |

## Per customer period

Emitted for a customer's billing period rather than for a run. A customer's spend is not attributable to a run, a node or a case, so these carry the reserved tag sentinels and ride the customer and period as dimensions — never as labels, because a series per customer is a cardinality explosion.

| Metric | Unit | Computation | Computed in |
|---|---|---|---|
| `revenue_sum_under_management` | `usd` | A customer's derived spend under management for a period. | `internal/metering/observe.go:SUMRecorded` |
| `revenue_metered_reported` | `count` | A metered quantity as handed to the billing provider, with the meter as the `meter_name` dimension. It is what we REPORTED, which is the half of a reconciliation that is ours. | `internal/metering/observe.go:UsageReported` |
| `revenue_invoice_state` | `count` | 1 per observed invoice or subscription state transition, with the state as the `billing_state` dimension. The value is 1 rather than an encoded state, because a state is not a quantity and averaging one is meaningless. | `internal/metering/observe.go:InvoiceState` |
| `revenue_charge_failed` | `count` | 1 per charge that did not settle, with the kind as the `charge_kind` dimension. It also raises an alert: a failed charge is revenue that silently did not happen. | `internal/metering/observe.go:ChargeFailed` |
| `revenue_gainshare_billed` | `usd` | A gainshare charge's billed quantity, over VERIFIED savings only. An unverified change contributes nothing to it. | `internal/metering/observe.go:GainshareBilled` |
| `revenue_reconciliation_drift` | `count` | 1 per drift finding, with the kind as `drift_kind` and the meter as `meter_name`. It also raises an alert: drift is the meter and the provider disagreeing, which is how month-end becomes a reconstruction. | `internal/metering/observe.go:DriftDetected` |

## What is not measured, and why that is stated

Two absences are deliberate rather than pending. **A model with no pricing entry emits no cost at all**, rather than a zero — a zero cost is a claim, and an absent one is a gap you can see. **A non-streaming call emits no time-to-first-token**, because there is no first token to time. In both cases the metric is missing rather than wrong, which is the only version of the choice that survives being aggregated.

The build checks that every metric named on any documentation page exists with this name, this unit and this computation. It cannot check that the sentence in the computation column still describes the code at the cited site — that is a review responsibility, and the citation is there so it is a cheap one.

