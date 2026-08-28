---
title: A metric nobody emits
tier: guide
summary: A deliberately broken fixture that proves the metric fence still refuses an uncatalogued name in prose.
platform_version: 0.20.0
boundary: This is a test fixture and is never published.
---

## Reading the numbers

🔴 This fixture exists because `scan-metric.mjs` was NARROWED: it now removes source paths from a line
before scanning, so that citing `internal/telemetry/context_assembly.go` — which the fence itself demands
— stops being read as a claim about a metric called `context_assembly`.

A narrowing needs a fixture proving what it did NOT narrow. The sentence below names a metric-shaped word
in PROSE, with no path around it, and nothing emits it. The fence must still refuse it.

The `context_hallucinated_ratio` metric is reported per node, computed in
internal/telemetry/context_assembly.go:EmitContextAssembly.
