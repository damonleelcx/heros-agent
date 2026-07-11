# LLM Agentic Workflow Evaluation & Configuration System — Implementation Timeline

> **Project pivot.** This repository is being repurposed from the *Heros OS-level agent*
> into a platform that ingests a codebase, discovers its LLM call graph, exposes every
> call site as a configurable node, and lets users remix models/prompts/skills/context,
> re-order nodes, then **execute, score, diagnose, and optimize** variants.
>
> This document set is the comprehensive implementation timeline. It is written by
> applying six senior-role operating playbooks (see [`roles-and-ownership.md`](roles-and-ownership.md))
> to the [source plan](source-plan.md) so that every
> phase ships with the right questions already answered — architecture quantified,
> contracts designed, evals built before optimization, infra observable, UX validated.

## Document map

| File | What it covers |
|------|----------------|
| [`README.md`](README.md) | This file — system overview, critical path, role matrix, Gantt, milestones, cross-cutting risks |
| [`roles-and-ownership.md`](roles-and-ownership.md) | The six senior workflows, what each owns, and how their internal phases apply here |
| [`phases-platform.md`](phases-platform.md) | **Phases 0 → 3.5** — build the platform: IR/schema, Discovery, Config+Runtime, Metrics, Context/Skills, Pattern Classification |
| [`phases-intelligence.md`](phases-intelligence.md) | **Phases 4 → 6** — measure & optimize: Eval harness, Attribution/Diagnosis, Re-arrangement/Tracing, Proposals/Verification, Autonomous loop |
| [`source-plan.md`](source-plan.md) | The verbatim source specification this timeline implements |

## 1. System overview

Four core subsystems plus three cross-cutting subsystems:

| Subsystem | Responsibility |
|-----------|----------------|
| **Discovery Engine** | Static + dynamic analysis to extract LLM nodes and their DAG into a canonical **Workflow IR** |
| **Configuration Layer** | Per-node overrides (model / prompt / skill / context) resolved from registries via a shim; a **Variant Spec** is a full config |
| **Runtime** | Dynamic executor that runs a Variant Spec against live providers, sandboxed, fully traced |
| **Evaluation Harness** | Runs variants over eval sets, collects traces/metrics, compares with statistical rigor |
| **Metrics & Observability** *(cross-cutting)* | Shared OTel-based telemetry substrate every other subsystem reads from |
| **Analysis & Improvement Engine** *(cross-cutting)* | Attribution → diagnosis → verified proposals → closed-loop optimization |
| **Pattern Classifier** *(cross-cutting)* | Labels each subgraph's agentic pattern; dispatches which metrics / failure modes / operators apply |

```mermaid
graph LR
  Repo[Repo] --> DE[Discovery Engine]
  DE --> IR[Workflow IR]
  IR --> CL[Config Layer]
  Reg[(Registries<br/>model/prompt/skill/context)] --> CL
  CL --> VS[Variant Spec]
  VS --> RT[Runtime<br/>loader+executor+sandbox]
  ED[(Eval data)] --> RT
  RT --> TR[Traces + Metrics]
  IR --> PC[Pattern Classifier]
  PC -.dispatches.-> EH
  TR --> EH[Eval Harness]
  EH --> AIE[Analysis & Improvement Engine]
  AIE -->|verified proposals| VS
  TR --> OBS[(Metrics/Observability<br/>substrate)]
  OBS --> EH
  OBS --> AIE
  EH --> UI[UI: compare / optimize]
  AIE --> UI
```

## 2. Critical path

The single hardest scheduling truth from the plan: **the IR and the event schema (Phase 0)
gate everything**, and the two most commonly *underestimated* items — the **Metrics/tagging
substrate** and the **typed per-node I/O contracts** — must be front-loaded in design even
though they pay off later.

```mermaid
graph LR
  P0[P0 Foundations<br/>IR + event schema] --> P1[P1 Discovery MVP]
  P1 --> P2[P2 Config + Runtime]
  P2 --> P25[P2.5 Metrics / OTel]
  P2 --> P3[P3 Context + Skills + Sandbox]
  P3 --> P35[P3.5 Pattern Classifier<br/>structural]
  P25 --> P4[P4 Eval Harness<br/>+ eval-set gen + scoring]
  P35 --> P4
  P4 --> P45[P4.5 Attribution + Diagnosis]
  P4 --> P5[P5 Typed contracts + Re-arrange<br/>+ Dynamic tracing + behavioral classify]
  P45 --> P55[P5.5 Proposals + Verification gate]
  P5 --> P55
  P55 --> P6[P6 Autonomous optimizer]
  P0 -. design now, pays later .-> P25
  P0 -. design now, pays later .-> P5
```

**Two items to get right at design time in Phase 0, not when they block you:**
1. The **event tagging contract** `{variant_id, run_id, node_id, case_id, seed, timestamp, config_hash}`. Under-tagged metrics you can't slice later is the top failure mode.
2. The **typed per-node I/O contract** (input schema, output schema). It is the precondition for *safe* re-arrangement — the biggest gap in the naïve design.

## 3. Role-ownership matrix

Which of the six senior roles leads (**L**) or supports (**S**) each phase. Full mapping in [`roles-and-ownership.md`](roles-and-ownership.md).

| Phase | System Designer | Backend Dev | AI Engineer | DevOps | Frontend Dev | Product Designer |
|-------|:---:|:---:|:---:|:---:|:---:|:---:|
| **P0** Foundations | **L** | S | S | S | – | S |
| **P1** Discovery MVP | S | **L** | S | S | – | S |
| **P2** Config + Runtime | S | **L** | S | S | S | S |
| **P2.5** Metrics/OTel | S | S | S | **L** | S | – |
| **P3** Context + Skills + Sandbox | S | **L** | S | **L** | – | – |
| **P3.5** Pattern Classifier | S | S | **L** | – | S | – |
| **P4** Eval Harness + gen + scoring | S | S | **L** | S | **L** | S |
| **P4.5** Attribution + Diagnosis | S | S | **L** | S | S | S |
| **P5** Contracts + Re-arrange + Tracing | **L** | **L** | S | S | **L** | **L** |
| **P5.5** Proposals + Verification | S | S | **L** | S | S | S |
| **P6** Autonomous optimizer | S | S | **L** | **L** | S | **L** |

## 4. Timeline (Gantt)

Assumes a team of six seniors (one per role) plus shared review, working in two-week sprints.
Durations are estimates for a first production-quality build; adjacent phases overlap where the
critical path allows. **~40 weeks (~10 months)** end to end; the read-only platform + eval harness
(through P4) is usable at **~week 22**.

```mermaid
gantt
  title Implementation Timeline (weeks)
  dateFormat X
  axisFormat %s
  section Platform
  P0 Foundations (IR + event schema)      :p0, 0, 3w
  P1 Discovery MVP (Go static)            :p1, 3, 4w
  P2 Config + Runtime                     :p2, 6, 5w
  P2.5 Metrics / OTel                     :p25, 9, 4w
  P3 Context + Skills + Sandbox           :p3, 12, 4w
  P3.5 Pattern Classifier (structural)    :p35, 15, 2w
  section Intelligence
  P4 Eval Harness + gen + scoring         :p4, 16, 6w
  P4.5 Attribution + Diagnosis            :p45, 21, 4w
  P5 Contracts + Re-arrange + Tracing     :p5, 24, 6w
  P5.5 Proposals + Verification gate      :p55, 29, 5w
  P6 Autonomous optimizer                 :p6, 33, 7w
```

## 5. Milestones & exit criteria

| Milestone | ~Week | Definition of done |
|-----------|:----:|--------------------|
| **M0 — Foundations frozen** | 3 | Workflow IR JSON schema + metric event schema versioned; config-hash scheme; repo scaffolded; CI green |
| **M1 — Node extraction proven** | 7 | Discovery MVP extracts static LLM nodes from a Go repo via signature + user-declared entrypoints; IR emitted and diffable |
| **M2 — First variant executes** | 11 | Runtime runs a hardcoded graph with per-node model/prompt overrides through the shim, sandboxed |
| **M3 — Everything is measured** | 13 | Every provider-gateway call emits tagged OTel spans + operational metrics into trace/metric stores |
| **M4 — Patterns dispatch metrics** | 17 | Structural classifier labels subgraphs; metric-set selection keys off pattern label |
| **M5 — Variants are comparable** | 22 | Eval harness runs variants over generated + user eval sets, multi-seed, CI-bounded composite scores, leaderboard |
| **M6 — Failures are localized & explained** | 25 | Per-node attribution + rule-based diagnosis + read-only scorecard |
| **M7 — Safe re-arrangement** | 30 | Typed I/O contracts validate proposed orderings; dynamic tracing reconciles static graph; behavioral classification live |
| **M8 — Verified advice** | 34 | Change operators emit proposals; verification gate re-runs on held-out data with statistical significance before surfacing |
| **M9 — Closed loop** | 40 | Autonomous analyze→propose→verify→apply under hard constraints (budget, allowlist, min-improvement, max-iterations), full audit trail + rollback |

## 6. Cross-cutting risks (front-loaded on purpose)

| Risk | Owner | Mitigation | Phase surfaced |
|------|-------|-----------|----------------|
| Under-tagged metrics can't be sliced later | DevOps + System Designer | Freeze the event tagging contract in P0; every emitter conforms | P0 → P2.5 |
| "Drag to reorder" silently breaks workflows | System Designer + Backend | Typed per-node I/O contracts + adapter auto-insertion **before** shipping reorder | P0 (design) → P5 (build) |
| Executing discovered repo code with ambient creds | DevOps + Backend | Sandbox (subprocess/container), no ambient credentials, least privilege | P3 |
| LLM-as-judge / analyst is noisy and biased | AI Engineer | Calibrate against human-labeled subset, report judge agreement, never let one unverified opinion drive an automated change | P4, P4.5, P5.5 |
| Single-run variant comparison ranks noise | AI Engineer + System Designer | Multi-seed runs, confidence intervals, significance gates; ties when CIs overlap | P4 onward |
| Synthetic eval sets inherit generator blind spots / are trivially easy | AI Engineer | Calibrate against real-trace subset, dedupe, track eval-set difficulty/diversity as a metric | P4 |
| "Fixed accuracy, tripled cost" regressions | AI Engineer + DevOps | Regression detection + hard budget/latency gates that separate constraints from weighted preferences | P4.5, P6 |
| Static analysis misses runtime dynamic dispatch | Backend + AI Engineer | Dynamic tracing to validate/repair the static graph (not optional) | P5 |
| Autonomous loop runs away | DevOps + AI Engineer | Hard constraints, min-improvement threshold, max iterations, kill switch, audit trail, rollback | P6 |

## 7. Recommended stack (from the plan, with owners)

- **Discovery** — tree-sitter + language ASTs (Go `go/ast` first) — *Backend*
- **Backend** — Go + Gin — *Backend*
- **Storage** — Postgres (variant specs, registries, eval results) + object store (content-hashed blobs) — *Backend / System Designer*
- **Provider gateway** — LiteLLM-style unified abstraction — *AI Engineer / Backend*
- **Tracing/metrics** — OpenTelemetry (GenAI semantic conventions) → span store (Tempo/Jaeger) + TSDB (Prometheus/ClickHouse) — *DevOps*
- **Execution** — a queue for run fan-out; sandbox via subprocess/container — *DevOps / Backend*
- **UI** — React + a graph library (node editor, leaderboard, dashboards, diff views) — *Frontend / Product Designer*
