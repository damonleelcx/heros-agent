## Why

Every subsystem on the critical path (Discovery → Config/Runtime → Metrics → Eval → Analysis →
Autonomous) reads from or writes to two artifacts that do not yet exist: the **Workflow IR** (the
canonical graph Discovery emits and everything else consumes) and the **metric event schema** (the
tag set on every telemetry event that makes slicing, comparison, reproducibility, and attribution
possible). The source plan names the two most-underestimated items in the whole project — the
**metric tagging contract** and the **typed per-node I/O contract** — and both are cheap to design now
and ruinously expensive to retrofit: an event written in P2.5 without a `config_hash` or `seed` can
never be reproduced or attributed, and an IR emitted in P1–P4 without an `io_contract` field forces a
backfill migration of every historical IR the day re-arrangement (P5) ships.

P0 therefore freezes these contracts before any of them is exercised. There is **no upstream
dependency** — this is the root of the critical path (Milestone M0). It delivers contracts, a lineage
scheme, a storage decision grounded in back-of-envelope volume estimates, and a scaffolded green CI —
so that P1–P6 *add* capability against stable schemas instead of re-litigating the foundations.

See `docs/prd/P0-foundations.md` for the full product rationale, the capacity estimation, and the
role-lens design.

## What Changes

- **ADD capability `workflow-ir`** — a versioned JSON schema for the workflow graph: nodes with
  call-site/model/prompt/tools/context-assembly metadata; **static node definitions** distinguished
  from **runtime invocations**; node count reported per static definition (loops/agents flagged
  `variable_at_runtime`, not counted as many nodes); typed edges (`data`/`control`); a reserved
  optional `pattern_labels` field for P3.5.
- **ADD** a first-class, required **typed per-node I/O contract** (`input_schema` + `output_schema`,
  JSON Schema) on every IR node — present from IR v1 even though re-arrangement ships in P5.
- **ADD capability `metric-event-schema`** — a versioned event schema where every event carries the
  full tag set `{variant_id, run_id, node_id, case_id, seed, timestamp, config_hash}`, **all
  non-null**, plus a typed, additively-extensible payload; aligned with OpenTelemetry GenAI semantic
  conventions.
- **ADD capability `storage-and-lineage`** — the `config_hash` canonicalization + lineage scheme
  (what is hashed, what is excluded, how a hash resolves to exact registry versions and content-hashed
  blobs), and the storage decision record: three stores by shape (spans → OTel span store, metrics →
  TSDB, eval results → Postgres) with blobs content-hashed in object storage, plus the DB-enforced
  tagging/lineage invariants.
- **ADD** repo scaffold + CI (build/test/lint green, schema-validation gate), a secrets-management
  baseline, and the OTel GenAI-conventions doc.
- **ADD** validating sample fixtures (a valid IR sample, a valid metric-event sample) and negative
  fixtures (missing-tag / missing-`io_contract`) wired into CI.

No **breaking** changes — this is the first change; it establishes the contracts everything else
extends additively.

## Impact

- **Affected capabilities:** `workflow-ir` (new), `metric-event-schema` (new), `storage-and-lineage`
  (new).
- **Affected code/systems:** repo scaffold + CI; `workflow-ir.schema.json`, `metric-event.schema.json`;
  the config-hash spec and storage decision record; the OTel conventions doc; Postgres schema/constraint
  design (not yet migrated live — modeled here, applied in P2/P2.5).
- **Dependencies:** none upstream (root of critical path). **Unblocks** P1 (Discovery emits the IR),
  P2 (Variant Spec + `config_hash`), P2.5 (emits against the event schema into the three stores), P3.5
  (`pattern_labels`), P4 (schema-driven eval-set gen + output-contract metrics, result persistence),
  P5 (reorder validator reads the I/O contract), P6 (auditable/reversible changes via `config_hash`
  lineage).
