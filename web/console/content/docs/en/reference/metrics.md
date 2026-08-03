---
title: Metric reference
tier: reference
summary: Every metric the platform emits, with its unit, its exact computation, and the code that computes it.
platform_version: 0.21.0
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

## Per run

Emitted once per run window. These carry a reserved `node_id` sentinel, because concurrency across a run is not attributable to one node.

| Metric | Unit | Computation | Computed in |
|---|---|---|---|
| `concurrency_calls_in_flight` | `calls` | Concurrent provider calls observed during the run window. Run-scoped, so it carries the reserved node_id sentinel rather than a node's. | `internal/telemetry/monitor.go` |
| `throughput_calls_per_sec` | `calls/s` | Provider calls per second over the run window. | `internal/telemetry/monitor.go` |

## What is not measured, and why that is stated

Two absences are deliberate rather than pending. **A model with no pricing entry emits no cost at all**, rather than a zero — a zero cost is a claim, and an absent one is a gap you can see. **A non-streaming call emits no time-to-first-token**, because there is no first token to time. In both cases the metric is missing rather than wrong, which is the only version of the choice that survives being aggregated.

The build checks that every metric named on any documentation page exists with this name, this unit and this computation. It cannot check that the sentence in the computation column still describes the code at the cited site — that is a review responsibility, and the citation is there so it is a cheap one.

