// Package telemetry is P2.5's metrics & observability substrate: the running infrastructure that
// turns P0's frozen contracts (the seven-tag metric-event schema, the config_hash/lineage scheme, the
// three-stores-by-shape decision) into telemetry that actually flows.
//
// # What it does
//
//   - Auto-instruments the P2 provider gateway (through internal/providercall's Observer seam — this
//     package deliberately does NOT import internal/providergateway, which links an HTTP client and the
//     AWS SDK, because doing so put net/http in reach of every package that can reach telemetry,
//     including the CLI's offline surface) and the run/node execution
//     path so every provider call and every node execution emits operational metrics + an OpenTelemetry
//     span with ZERO workflow-author code (Decision 1). The single attach point is the gateway's
//     Observer seam; a node cannot execute through the Runtime without being instrumented.
//   - Emits every metric as a typed event carrying the full seven-tag set {variant_id, run_id, node_id,
//     case_id, seed, timestamp, config_hash} populated from the run context, plus the P0 payload
//     {metric_name, value, unit}. Tags are complete BY CONSTRUCTION (the RunContext carries them) and
//     enforced again at the collector's emission gate (internal/telemetry Collector, Decision 3).
//   - Routes telemetry to three stores by shape — spans -> span store, metrics -> TSDB, eval results ->
//     Postgres — every record keyed by config_hash (Decision 5).
//   - Keeps high-cardinality identifiers out of TSDB series labels (Decision 4), scrubs secrets/PII to
//     content-hash references before any store (Decision 6), emits async/degrade-safe (Decision 7),
//     idempotent under P2's retry model (Decision 8), and stubs the evaluator-plugin seam (Decision 9).
//
// # Why the OTel data model is in-process here, not the OTel SDK
//
// The design deliberately deferred the SDK/backend choice (span store, TSDB) to "mechanism now,
// numbers tuned against real volume" (Decision 3 note, PRD OQ1/OQ6). This package models the OTel
// GenAI semantic conventions faithfully — the attribute keys in attributes.go are the real gen_ai.*
// convention keys, and the span hierarchy is run -> node -> tool-call — behind a small Exporter seam
// (stores.go). That keeps the substrate hermetically testable (`go test`, no Docker) while remaining a
// drop-in for a real OTLP exporter + OTel Collector + Tempo/Prometheus: the wire shape is the
// convention, not a bespoke logging layer. Swapping in the SDK is a Sink/Exporter implementation, not
// a re-plumb of collection, tagging, or storage.
package telemetry
