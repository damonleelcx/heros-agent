# Schemas (P0 frozen contracts)

Versioned JSON Schemas (draft 2020-12) every phase reads from and writes to. Frozen at M0; evolve
additively (see `docs/prd/P0-foundations.md` NFR1). Design rationale:
`docs/decisions/` and `openspec/changes/p0-foundations/`.

| File | Contract | Tasks |
|---|---|---|
| `workflow-ir.schema.json` | Workflow IR: graph of static node definitions + typed edges; required `io_contract`; reserved `pattern_labels` on nodes **and optional `subgraphs`** (for P3.5, task 3.3); `variable_at_runtime`. | 1.3, 1.4, 3.3 |
| `runtime-invocation.schema.json` | A runtime execution instance referencing a definition by `node_id` (distinct from a node). | 1.3 |
| `metric-event.schema.json` | The seven-tag event contract + typed payload + optional extensible dimensions. | 1.5 |

## Samples & validation

`samples/` holds valid fixtures (must validate) and negative fixtures (must be rejected), plus the
`config-hash.golden.json` vectors. `validate.py` proves the M0 gate (PRD NFR8):

```bash
pip install jsonschema      # draft 2020-12 capable, >= 4.18
python3 schemas/validate.py # exits non-zero if any sample misbehaves
```

This script is the seed of the DevOps CI schema-validation job (P0 task 4.2).

Other checks in this dir:

- `test_config_hash.py` — config_hash golden vectors (task 1.6/2.4).
- `test_schema_evolution.py` — expand-migrate-contract proof (task 2.3).
- `spike_io_contract.py` — typed I/O-contract sufficiency for synthesis + adherence (task 3.2).

All are pure `jsonschema`/Python (no server). The live Postgres proofs live under
`db/migrations/postgres/` (`prove_constraints.py`, `prove_slices.py`).
