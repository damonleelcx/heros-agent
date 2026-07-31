---
title: Bring your own provider keys
tier: guide
summary: Evaluation calls models on your account, with your keys, from your machine — here is where they go and where they never go.
platform_version: 0.20.0
boundary: Your provider keys stay on the machine that runs the CLI. This page describes how they are read and what is done to keep them out of telemetry; it does not manage, rotate or store keys for you.
order: 5
---

The job on this page: **know exactly where your provider keys live, what reads them, and what can never
see them.**

## The short version

- Keys are read from **your environment**, on **your machine**, at the moment a model is called.
- They are never written into a repository, a log, a trace, a span, or CI output.
- The platform never receives them. There is no field for them.
- The console never receives them either — its own credential boundary is separate and stronger, and the
  build fails if a credential reaches a shipped browser chunk.

## Where they come from

From a secrets manager or your environment, resolved at call time. The rule the platform holds itself to
is stated in its own secrets baseline: **never in a repository, never in logs, never echoed by CI, never
in traces.**

In CI, use your provider's secret mechanism — the same one you use for every other credential. Do not put
a key in `llm-eval.yaml`, and do not put one in `.heros.json`; both are files people commit.

## What sees them, and what cannot

| | Sees your provider key |
|---|---|
| The `heros` binary, at the moment of a model call | **yes** |
| Your model provider | **yes**, that is the point |
| Telemetry, spans, metrics, logs | **no** — a single scrubber strips them before anything is stored |
| The platform | **no** |
| The console, and anything in a browser | **no** |

The telemetry line is the one worth a sentence of mechanism. Every event and every span passes through
**one** scrubber before it reaches any store, and that scrubber strips secrets, API keys, prompt text,
completion text and PII. It runs at a single chokepoint on every event, so "secrets never touch a span or
a log" is enforced in one place rather than trusted to each emitter.

Prompt and completion text are handled the same way and for related reasons: substantial content is
replaced by a content-hash reference, so telemetry carries a pointer and never the words.

## Cost is on your account

Because the calls are yours, the bill is yours, directly from your provider. Nothing is marked up and
nothing is resold — and that also means **nothing on our side can cap your spend**. The controls are
yours:

- `--seeds` and `--cases` on `heros eval` decide how many calls a run makes.
- `--max-cost-per-run` is a gate that fails the run when the measured cost exceeds it.

`--max-cost-per-run` is a **gate**, not a budget: it reports and fails after the fact rather than stopping
mid-run. If a hard ceiling matters, set it at your provider, where a ceiling can actually be enforced.

Cost per call is recorded as `cost_usd`, priced from the provider's published rate for the model actually
used, in `internal/telemetry/pricing.go`. A model with **no pricing entry emits no cost at all** rather
than a zero — a zero would be a claim, and an absent value is a gap you can see. The full definition is
in the [metric reference](/docs/reference/metrics).

## Running with no keys at all

Two of the most useful commands never call a model:

```bash
heros discover --repo . --out ir.json --report discovery.json
heros coverage
```

Discovery is static analysis and `coverage` reads the build's own capabilities. Both are free, both are
offline, and neither needs an account. If you are evaluating whether this tool is worth wiring in, they
are the whole first day.

## Checking what is configured

```bash
heros status --repo .
```

This prints every resolved setting **with its provenance** — which of flag, environment, project file or
default supplied it, and which sources were overridden. It reports whether a platform credential exists
as a **boolean**; it never prints the value of anything.

## If a key leaks anyway

Rotate it at the provider. Nothing in this system can un-leak a key, and nothing here stores one to be
purged — which is the design that makes the answer that short.
