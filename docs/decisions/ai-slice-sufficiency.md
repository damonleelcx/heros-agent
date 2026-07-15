# AI-Engineer: Downstream-Slice Sufficiency of the P0 Contracts

| Field | Value |
|---|---|
| Phase / Milestone | P0 / M0 |
| Owner | AI Engineer (support to System Designer) |
| Status | Draft — freeze at M0 |
| Tasks | 3.1 (tag-set slice sufficiency), 3.2 (I/O-contract sufficiency), 3.3 (reproducibility + `pattern_labels`) |
| Cross-refs | PRDs `P4-eval-harness.md`, `P4.5-attribution-diagnosis.md`, `P3.5-pattern-classifier.md`; `docs/decisions/config-hash-spec.md`; `docs/decisions/storage-decision-record.md` |

AI-Engineer's P0 job is to confirm the frozen contracts carry **enough signal for every question P4/P4.5
will ask**, because a tag or field missing here is an *un-answerable question later* — and by then it is
a backfill of historical data. Per the senior-AI-engineer discipline (*prove it, print the process data,
let the reader recompute*), every claim below is backed by a runnable proof, not an assertion. One real
gap was found and **closed additively** (§3.3).

## 3.1 — Tag-set slice sufficiency

**Question:** can every P4/P4.5 slice be computed from the seven tags `{variant_id, run_id, node_id,
case_id, seed, timestamp, config_hash}` (+ optional `node_kind`, `invocation_id`)?

**Proof:** [`db/migrations/postgres/prove_slices.py`](../../db/migrations/postgres/prove_slices.py),
run live via `run_pg_proof.sh prove_slices.py` over a synthetic 2×2×2×3 dataset. Every slice returned
the expected groups. Result matrix:

| Required slice (source) | Answered by | Verdict |
|---|---|---|
| **per-variant** (P4 leaderboard) | `variant_id` / `config_hash` (native tags) | ✅ native |
| **per-node attribution** (P4 G3, P4.5 G1) | `node_id` (native tag) | ✅ native |
| **per-case** (P4) | `case_id` (native tag) | ✅ native |
| **per-seed confidence intervals** (P4 G4, FR5) | group across `seed`; `mean`, `stddev`, `n` per `(config_hash, node_id, case_id, metric_name)` | ✅ native |
| **per-run** (batch grouping) | `run_id` (native tag) | ✅ native |
| **per-failure-cluster** (P4.5 G2) | **derived label**, joined on `case_id` | ✅ join, no new tag |
| **per-pattern** (P4 leaderboard "per pattern", P3.5 dispatch) | **derived label**, joined on `node_id` → IR `pattern_labels` | ✅ join, no new tag |
| **per-invocation** (P4.5 ablation drill-down) | optional `invocation_id` dim + runtime-invocation record | ✅ optional dim |

**The load-bearing finding — derived labels need a join key, not a new tag.** `failure_cluster` and
`pattern` are computed *after* a run (embedding+clustering in P4.5; classification in P3.5). They are
**not** properties of a raw metric event, so they are correctly **not** tags. They attach by joining on
an existing tag (`case_id`, `node_id`). The proof builds those two derived tables and shows the slices
resolve. This is why the tag set is *complete* without them.

**Gaps found: none requiring a breaking change.** Two forward-looking notes, both already covered by
the schema's additive extensibility (`metric-event.schema.json` `additionalProperties: true`, FR10):

1. **`eval_set` identity** is not a tag. A run is over one eval set; `run_id → run metadata (P4)` holds
   `eval_set_hash`. If a *metric-level* `eval_set` slice is ever wanted directly on events, it is added
   as an optional dimension — no MAJOR bump. Recorded, not closed (no consumer needs it in P0).
2. **`metric_name` vocabulary consistency** (`cost.output_usd` vs `cost_output`) is a *convention*, not
   a schema gap. It belongs in the DevOps OTel-conventions doc (task 4.3), referenced here so P4's
   per-metric slices are stable.

**Honesty check (the headline P4 question).** The proof's final block groups by `config_hash` to answer
"did B beat A?" with `mean ± stddev` — and shows two variants with *equal means but different variance*,
i.e. exactly why per-seed CIs (not point estimates) are the comparison unit. The tags make that honest.

## 3.2 — Typed I/O-contract sufficiency

**Question:** is `io_contract.{input_schema, output_schema}` (JSON Schema 2020-12) enough to later drive
(A) schema-driven eval-set synthesis and (B) output-contract-adherence metrics?

**Proof:** [`schemas/spike_io_contract.py`](../../schemas/spike_io_contract.py) (pure `jsonschema`, no
PG). Findings:

- **(A) Synthesis.** From a constrained `input_schema` the contract discriminates **valid**, **boundary**
  (at `minLength`/`maxLength`/`minimum`/`maximum`), and **invalid** (missing `required`, out-of-`enum`,
  `additionalProperties`) instances — the exact valid/boundary/invalid partition P4's property/fuzz
  generator needs. Proven: all valid/boundary accepted, all five invalid classes rejected.
- **(B) Adherence.** Validating a node's output against `output_schema` yields a per-output pass/fail;
  the sample shows a **1/3 adherence rate** with two violation classes flagged (out-of-enum, missing
  required). That *is* the P4 output-contract-adherence metric.
- **Permissive-schema allowance (P1), degrades gracefully — not silently wrong.** A permissive
  `{"type":"object"}` accepts arbitrary objects (constraint score 0). The contract's design intent is
  therefore: when the score is 0, **synthesis falls back to LLM-driven** and adherence is reported as
  *"unconstrained"* — **never mistaken for a passing contract**. The proof asserts the permissive schema
  accepts everything and computes a constraint score (constrained=5 vs permissive=0) that P4 can surface
  as an eval-set-quality signal. This matches Decision 2 in `design.md`: the *field's presence* is the
  contract; its *precision* refines additively.

**Verdict:** sufficient. No change to the contract; the permissive allowance is a feature with a defined
degradation, and the constraint-score idea is handed to P4 as the way to keep a weak schema visible.

## 3.3 — Reproducibility + reserving `pattern_labels`

**Reproducibility ("verification decides").** The platform rule is *diagnosis proposes, verification
decides* — which requires every result to be **replayable**. `config_hash + seed` is that unit:
`config_hash` resolves the exact configuration (registry versions + content-hashed blobs — proven in
`internal/confighash` golden tests and the config-hash spec), and `seed` pins *our* inputs. So a P5.5/P6
proposal can be re-run over a held-out slice and its delta *measured*, not asserted. Confirmed: the tag
set carries `config_hash` and `seed` on every event, and the lineage resolves both — no gap. (Caveat,
stated openly per PRD §8.6: this is *exact-config* replay, not bit-identical provider output; multi-seed
CIs absorb provider non-determinism — which is precisely why `seed` is a first-class tag.)

**`pattern_labels` reservation — GAP FOUND AND CLOSED ADDITIVELY.** The `workflow-ir` spec requires
`pattern_labels` on **nodes *and/or* subgraphs** (FR7), and P3.5 explicitly labels **subgraphs**
("emit a *set* of pattern labels, each tied to the specific subgraph — never one label for the whole
workflow"). The initial IR schema reserved `pattern_labels` on **nodes only** — there was no subgraph
construct, so P3.5's per-subgraph dispatch would have had nowhere to write.

*Closure (additive, no MAJOR bump):* added an optional top-level `subgraphs[]` to the IR
(`workflow-ir.schema.json` → `$defs/Subgraph`), each `{subgraph_id, node_ids[], pattern_labels?}`,
reusing the existing `PatternLabels` definition. Optional ⇒ existing v1.0 samples still validate;
[`schemas/samples/workflow-ir.with-subgraphs.valid.json`](../../schemas/samples/workflow-ir.with-subgraphs.valid.json)
exercises subgraph-level labels and is wired into `validate.py`. This is the P3.5 dispatcher's write
target: `pattern → metric-set` selection keys off these labels (RAG subgraph → retrieval metrics; router
→ misroute-rate).

## How to run every AI-Engineer proof

```bash
# I/O-contract sufficiency (needs: pip install jsonschema)
python3 schemas/spike_io_contract.py

# subgraph pattern_labels sample validates (part of the schema gate)
python3 schemas/validate.py

# tag-set slice sufficiency on a live Postgres (needs initdb/pg_ctl/postgres on PATH + psycopg[binary])
PATH="/path/to/pg/bin:$PATH" bash db/migrations/postgres/run_pg_proof.sh prove_slices.py
```

## Outcome

The seven-tag set answers every P4/P4.5 slice (natively or by a join on an existing tag); the typed I/O
contract is sufficient for synthesis and adherence with a graceful permissive fallback; `config_hash +
seed` makes every result replayable; and the `pattern_labels` reservation now covers subgraphs so the
P3.5 dispatcher has a home. One gap found, closed additively — no consumer of the frozen contracts is
left with an un-answerable question.
