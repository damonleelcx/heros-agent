# P1 Discovery — Design: The discovery run report shape

> **Task:** P1 `tasks.md` §2.6. **Phase:** ② Design (Backend lead, System Designer support).
> **Inputs:** [invariant I4 (every missing node explainable)](03-discovery-invariants.md),
> [contract §6 Finding A (provenance lives here, not on the IR node)](01-ir-contract-confirmation.md),
> [failure table (diagnostic codes)](08-failure-behavior.md), NFR6.

## §0 TL;DR

`discovery-report.json` is Discovery's **own** artifact (not the frozen IR) and therefore the home for
everything the `additionalProperties:false` IR node can't hold: **detection provenance**, **ambiguity
flags**, **framework/version metadata**, **dedup merges**, and **per-file/-declaration diagnostics**. Its
one hard requirement (I4): **from this file alone, you can explain why any expected node is absent** —
parse-skipped, undeclared, deduped, or degraded. Because it is Discovery-owned (not a one-way-door public
contract like the IR), it evolves under lighter governance — but it still ships with a
`schemas/discovery-report.schema.json` and a CI validation.

## §1 Why a separate report (not fields on the IR node)

[Contract Finding A](01-ir-contract-confirmation.md) established that `detected_by`, `ambiguity_flags`,
`framework_source`, and dataflow evidence **cannot** go on the frozen node. The run report is their correct
home for three reasons: (1) it keeps the IR contract frozen/minimal (L5 演进 — the IR outlives this code);
(2) provenance is *about* the discovery run, not *part of* the workflow graph — different lifecycle, so
different artifact (职责划分 by ownership); (3) the report can evolve freely as Discovery learns, without
touching a public contract. This is the "配置 > 数据表 > 代码 / single source of truth" instinct: the IR is
the durable graph, the report is the run log — don't conflate them.

## §2 The shape

```jsonc
{
  "report_version": "1.0.0",
  "workflow": {                             // mirror of the IR's identity, for correlation
    "id": "acme-app",
    "repo": { "url": "https://github.com/acme/app", "commit_sha": "a1b2c3d" },
    "language": "go"
  },

  "summary": {                              // the at-a-glance health of the run
    "files_scanned": 812,
    "files_parsed_ok": 810,
    "files_skipped": 2,                     // = len(file_diagnostics with severity=error)
    "packages_scanned": 47,
    "call_sites_detected": 21,              // before dedup
    "nodes_emitted": 20,                    // = len(ir.nodes) — MUST equal it (checked in CI)
    "edges_emitted": 24,
    "dedup_merges": 1,                      // call_sites_detected - nodes_emitted attributable to merges
    "ambiguity_flags": 3,
    "elapsed_ms": 4120                      // throughput signal (NFR3 / CI §8.3)
  },

  "detections_by_source": {                 // ★ the wrapper-coverage proof (I4): how many nodes each source found
    "registry": 14,
    "declared": 5,                          // 0 here => "you have no llm-eval.yaml; wrappers may be missed" (doc 05 §3.2)
    "framework": 1
  },

  "nodes": [                                // per-node PROVENANCE (keyed by the IR node_id) — Finding A's home
    {
      "node_id": "n_9f2a...",
      "detected_by": ["registry"],          // may be multiple after a merge
      "signature_row_id": "anthropic.messages.new",   // which registry row matched (doc 04)
      "unresolved_fields": [],
      "variable_at_runtime": false
    },
    {
      "node_id": "n_4c81...",
      "detected_by": ["declared"],
      "declared_symbol": "…/internal/llm.Complete",
      "unresolved_fields": ["model"],
      "variable_at_runtime": false
    }
  ],

  "ambiguity_flags": [                       // ★ every P-C sentinel, with a machine-readable reason (I5, FR8)
    { "node_id": "n_4c81...", "field": "model",  "reason": "model bound at construction; not at call site",
      "code": "MODEL_CONSTRUCTION_BOUND", "p5_candidate": true },
    { "node_id": "n_71de...", "field": "prompt", "reason": "Bedrock InvokeModel Body is opaque []byte",
      "code": "PROMPT_OPAQUE_BODY", "p5_candidate": true },
    { "node_id": "n_71de...", "field": "prompt", "reason": "resolution budget exceeded (depth>N)",
      "code": "RESOLUTION_BUDGET_EXCEEDED", "p5_candidate": true }
  ],

  "dedup_merges": [                          // ★ why "one node" when two sources hit it (I4, F12)
    { "node_id": "n_2b7f...", "sources": ["registry", "declared"] }
  ],

  "declaration_diagnostics": [               // ★ per llm-eval.yaml entrypoint outcome (I4, F3)
    { "symbol": "…/internal/llm.Complete",        "status": "resolved",   "node_id": "n_4c81..." },
    { "symbol": "…/internal/old.LegacyGenerate",  "status": "not_found",  "code": "DECL_SYMBOL_NOT_FOUND" }
  ],

  "file_diagnostics": [                       // ★ per skipped/partial file (I4, F1/F4/F5)
    { "file": "internal/broken/x.go", "severity": "error", "code": "PARSE_ERROR",
      "message": "expected ';', found '}'", "line": 42 },
    { "file": "vendor/loop",          "severity": "warn",  "code": "SYMLINK_CYCLE_SKIPPED", "message": "skipped" }
  ],

  "framework_subgraphs": [                    // ★ framework provenance + version drift (doc 07, F9/F10)
    { "subgraph_id": "sg_agent0", "framework": "langgraphgo", "version": "0.1.3",
      "recognized": true, "degraded": false, "node_ids": ["n_9f2a...", "n_2b7f..."] }
  ]
}
```

## §3 Key decisions

### 3.1 Decision — the report is the authoritative "why is this node missing?" oracle (I4)
**Problem.** A missing node has four benign causes; the user must distinguish them without reading source.
**Design.** Each cause has a dedicated section: parse-skipped → `file_diagnostics`; undeclared wrapper →
`detections_by_source.declared` + a low count; deduped → `dedup_merges`; framework-degraded →
`framework_subgraphs`; stale declaration → `declaration_diagnostics`. **Alternatives compared.** *A flat
log of messages* — rejected: not queryable ("which nodes were deduped?" becomes a grep), and easy to leave
a gap. **Effect.** "My `Summarize` wrapper isn't in the IR" resolves to one lookup: is it in
`declaration_diagnostics` as `not_found`? is `declared` count 0 (no file)? was it merged? — deterministic,
not detective work.

### 3.2 Decision — `nodes_emitted` MUST equal `len(ir.nodes)`, checked in CI
**Design.** The report's `summary.nodes_emitted` and per-node `nodes[]` are cross-checked against the IR in
CI; a mismatch fails the build. **Why.** It structurally forbids the silent-drop failure — a node in the IR
with no provenance row, or a provenance row with no IR node, is a bug caught mechanically. **Effect.** I4 is
enforced by a test, not by discipline.

### 3.3 Decision — diagnostic `code`s are a closed, documented enum
**Design.** Every diagnostic/flag carries a stable `code` (`PARSE_ERROR`, `MODEL_CONSTRUCTION_BOUND`,
`RESOLUTION_BUDGET_EXCEEDED`, `DECL_SYMBOL_NOT_FOUND`, `SYMLINK_CYCLE_SKIPPED`, `TYPE_RESOLUTION_PARTIAL`,
`PROMPT_OPAQUE_BODY`, …). **Why.** P5 dynamic tracing consumes these programmatically (it triages by
`code`, not by parsing English `message`s); a stable enum is the machine contract. **Alternatives
compared.** *Free-text messages only* — rejected: not machine-triageable, and drifts. **Effect.** P5 can
select "all `p5_candidate` flags with `code` in {opaque, construction-bound}" without NLP; the human
`message` stays for readability.

### 3.4 Decision — ship a `schemas/discovery-report.schema.json` + CI validation, lighter governance than the IR
**Design.** The report gets a JSON schema and a CI validation (mirroring `workflow-ir` validation), added in
implementation §5.2 — but it is **not** frozen/additive-only, because it is Discovery-owned, not a
cross-subsystem contract. **Why appropriate.** Health-signal-must-be-externally-readable (a machine-readable
report is the observable proof of the no-execution/least-privilege posture, [invariants I1/I8](03-discovery-invariants.md));
but it isn't a one-way door, so it evolves as Discovery learns. **Effect.** DevOps CI (§8.1) can validate
the report shape; the report can still grow fields freely between phases.

## §4 Invariant ties

- **I4** — §3.1 + §3.2: every missing node is explainable, and the IR↔report node-set equality is
  CI-enforced.
- **I5** — `ambiguity_flags[]` is the authoritative unresolved record; every P-C sentinel in the IR has a
  matching flag here with a reason + `code`.
- **I1/I8** — the report is the external, machine-readable evidence of the run (files scanned, no execution
  performed, detections by source) — the health signal DevOps least-privilege checks hang off.

## §5 Consumed by

Implementation §5.2 (emit the report + its schema), CI §8.1 (validate report + IR↔report equality), the
wrapper/dedup/malformed fixtures (§6.2/§6.6/§6.5 assert specific report entries), and **P5 dynamic tracing**
(consumes `ambiguity_flags[].code` + `p5_candidate` to build its trace targets — the hand-off the whole
"honest `unresolved`" discipline exists to produce).
