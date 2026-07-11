# Project Context — LLM Agentic Workflow Evaluation & Configuration System

## Purpose

A platform that ingests a codebase, discovers its LLM call graph, exposes each call site
("node") as a configurable unit, and lets users remix models / prompts / skills / context
strategies, re-order nodes, then **execute, score, diagnose, and optimize** variants.

Four core subsystems (Discovery Engine, Configuration Layer, Runtime, Evaluation Harness) plus
three cross-cutting subsystems (Metrics & Observability, Analysis & Improvement Engine, Pattern
Classifier). See [`../docs/implementation-timeline/README.md`](../docs/implementation-timeline/README.md)
for the full timeline and [`../docs/implementation-timeline/source-plan.md`](../docs/implementation-timeline/source-plan.md)
for the source specification.

## Tech stack

- **Discovery** — tree-sitter + language ASTs (Go `go/ast` first)
- **Backend** — Go + Gin
- **Storage** — Postgres (variant specs, registries, eval results) + object store (content-hashed blobs)
- **Provider gateway** — LiteLLM-style unified abstraction
- **Telemetry** — OpenTelemetry (GenAI semantic conventions) → span store (Tempo/Jaeger) + TSDB (Prometheus/ClickHouse)
- **Execution** — queue for run fan-out; sandbox via subprocess/container
- **UI** — React + a graph library

## Conventions

- Every metric/trace event is tagged `{variant_id, run_id, node_id, case_id, seed, timestamp, config_hash}`.
- Everything is keyed by `config_hash` for reproducibility; large blobs are content-hashed in object storage.
- Static **nodes** (definitions) are distinguished from runtime **invocations** (execution instances).
- Registries (model/prompt/skill/context) are versioned, git-like, and referenced by ID from Variant Specs.
- Diagnosis proposes; **verification decides** — no unverified LLM opinion drives an automated change.
- Statistical honesty — multi-seed runs, confidence intervals; ties when CIs overlap.

### Commercial model & entitlements

- The billable **value metric** is **LLM spend under management (SUM)**, aggregated from the P2.5 cost metrics — metering is a read over the telemetry substrate, not a parallel counter.
- **Plans-as-config** — plans are referenced by **name** (Free / Team / Business / Enterprise); prices and plan definitions live in configuration, **never in git**.
- **Entitlements gate by plan _and_ automation level** — a feature is unlocked only when both the plan and the automation level allow it (Autonomous auto-merge is Enterprise-only).
- Customers use their **own provider keys** — the platform **never resells tokens**.
- **Only verified savings are billable** — gainshare/verified-savings billing draws exclusively on the P5.5 verified-delta ledger; unverified savings are never billed.

## OpenSpec workflow

This project uses OpenSpec for spec-driven development. See [`AGENTS.md`](AGENTS.md) for the
format and rules. Capabilities live in `specs/`; proposed changes live in `changes/`. Each
delivery phase (P0–P7) is tracked as one change under `changes/`.
