# Backend Invariants, Constraints & Migration Strategy (P0)

| Field | Value |
|---|---|
| Phase / Milestone | P0 / M0 |
| Owner | Backend (support to System Designer) |
| Status | Draft — freeze at M0 |
| Tasks | 2.1 (relational model), 2.2 (emission boundary), 2.3 (expand-migrate-contract), 2.4 (golden vectors) |
| Cross-refs | `docs/decisions/storage-decision-record.md` §6; `docs/decisions/config-hash-spec.md`; `openspec/changes/p0-foundations/specs/storage-and-lineage/spec.md`; `openspec/changes/p0-foundations/specs/metric-event-schema/spec.md` |

Backend's job in P0 is to **model the invariants into the schema, not the code** — so the tagging and
lineage contracts hold on every write, forever, even when application code forgets. Nothing here runs
as a live service in P0; the Postgres schema is *modeled and migration-authored here, applied in
P2/P2.5*. Written in the senior-backend lens: *fail loud, no silent fallback; the DB enforces what app
code forgets; contracts outlive code; two dialects are two semantics.*

---

## 2.1 — Relational model: eval-results & lineage (Postgres)

**Deliverable:** [`db/migrations/postgres/0001_p0_lineage.up.sql`](../../db/migrations/postgres/0001_p0_lineage.up.sql)
(+ `.down.sql`). It structurally enforces three invariants:

| Invariant | Mechanism in the DDL |
|---|---|
| **Seven tags non-null (NFR3, FR9)** | `eval_result` declares `config_hash, variant_id, run_id, node_id, case_id, seed, ts` all `NOT NULL`. An under-tagged row cannot be inserted. |
| **`config_hash` uniqueness where a row is a configuration (FR17)** | The `config` table's PRIMARY KEY **is** `config_hash` — a second insert of the same immutable configuration is rejected. |
| **FKs eval_result → variant / node / case** | `variant_id → variant`, `case_id → eval_case`, composite `(workflow_id, node_id) → node`, plus `config_hash → config` and `blob_ref → blob`. A dangling reference is rejected. |

**Design notes / decisions taken:**

- **Node identity is `(workflow_id, node_id)`**, not `node_id` alone — a `node_id` is unique only within
  one workflow (it is derived from a call site). `eval_result` therefore carries `workflow_id` as a
  **structural helper** (also `NOT NULL`) so the composite FK is well-defined. `workflow_id` is *not*
  one of the seven tags; it is derivable from `config_hash` but denormalized onto the fact row to make
  the FK and the per-node read path (`idx_eval_result_node`) direct.
- **Idempotent re-runs.** `UNIQUE (config_hash, run_id, node_id, case_id, seed, metric_name)` is the
  natural key: a replay neither double-writes a row nor loses attribution — groundwork for P2's
  idempotent executor.
- **Blobs referenced, never inlined (FR15).** `blob(content_hash PK)` catalogs content-addressed
  objects; `eval_result.blob_ref` is a nullable FK. Bytes live in object storage.
- **`config.lineage_json` (JSONB)** stores the exact `resolved_config` that was hashed, so a run
  replays from lineage alone (NFR2) without re-deriving it.
- **CHECK constraints** on `config_hash`/`content_hash` (`^[0-9a-f]{64}$`) and `seed >= 0` catch
  malformed identifiers at the DB even if the boundary is bypassed.

**Two dialects are two semantics.** The dev ledger (`internal/db`) is **SQLite**; per the storage
decision, **eval results live in Postgres**. This DDL is Postgres-only (`TIMESTAMPTZ`, `JSONB`,
`GENERATED ALWAYS AS IDENTITY`, regex `~`) and is deliberately **not** loaded by the SQLite `Open()`
path. When P2.5 wires it, the schema↔migration↔code coherence rule applies: baseline DDL, migration,
and a real-fixture ingest test land together.

## 2.2 — Emission-boundary rejection rule (defense in depth)

**Deliverable:** [`internal/metricevent`](../../internal/metricevent/metricevent.go) (+ tests).

The DB constraints are the *last* line. The **emission boundary is the first**: an event missing any of
the seven tags (or the typed payload) is **rejected before it is persisted or exported to any store** —
it is never written. Two entry points:

- `Event.Validate()` — typed producers. `Seed *int64` / `Value *float64` are **pointers** so a
  legitimate `0` is distinguishable from an absent field (the classic "0 tokens vs no tag" trap).
- `ValidateMap(map[string]any)` — generic / cross-language producers; rejects absent, `null`, or
  empty-string tags.

**Rules enforced (all fail loud — every problem reported at once, nothing defaulted):**

1. Seven tags present and non-null; strings non-empty (whitespace-only = missing).
2. Payload `metric_name`, `value`, `unit` present (a value without a unit is incomplete — FR10).
3. Format: `config_hash` is 64-char lowercase hex; `timestamp` parses as RFC 3339. These pass a naive
   `NOT NULL` but would still be un-sliceable / un-reproducible, so the boundary catches them earlier
   with a better error than a downstream constraint could.

The tests assert each single missing tag is rejected and named, that `seed = 0` is accepted, and that
the **schema fixtures agree with the boundary** — the schema-valid sample passes the boundary and the
schema-invalid (missing `run_id`) sample is rejected by the boundary too. Layers agree; neither is the
sole gate.

## 2.3 — Expand-migrate-contract: evolving the IR & registry schemas

**Deliverable:** procedure below + a worked-example proof,
[`schemas/test_schema_evolution.py`](../../schemas/test_schema_evolution.py).

Contracts outlive code: the IR and metric-event schemas are consumed by six subsystems. They evolve
**additively** under semver (NFR1). The standing procedure for any schema/registry change:

| Step | Action | Compatibility held |
|---|---|---|
| **Expand** | Add the new field as **optional** (nullable in SQL / not in `required` in JSON Schema). Bump **MINOR**. | Old documents still validate against the new schema (they simply lack the new field). |
| **Migrate** | Dual-write / backfill: producers start emitting the new field; backfill historical rows if needed. | Both shapes coexist; readers tolerate either. |
| **Contract** | Only after all consumers moved: drop/rename the old field. Bump **MAJOR**. | The one breaking step, gated behind the M0-style freeze/review. |

A rename is `add-new (expand) → populate-both (migrate) → drop-old (contract)`, so older variants keep
resolving throughout — never an in-place rename.

**What the contract does and does not promise (the subtle part):**

- **Backward compatibility (guaranteed):** a document authored at MINOR *m* validates against any
  schema MINOR *m′ ≥ m* at the same MAJOR. This is the "older samples still validate" guarantee. The
  proof asserts a v1.0 IR sample validates against a v1.1 schema (after adding an optional
  `retry_policy` node field).
- **Forward compatibility is a PARSE contract, not a strict-validate one.** The published schemas are
  `additionalProperties: false` (strict authoring at a pinned version), so a v1.1 document does **not**
  strict-validate against the v1.0 schema. Therefore a consumer pinned to MAJOR *n* must **parse
  leniently** (ignore unknown fields), not strict-validate a newer document. The proof asserts this
  rejection explicitly so the design choice is deliberate, not accidental — a consumer that strict-
  validates newer docs is the bug, not the schema.

Running the proof:

```bash
python3 schemas/test_schema_evolution.py   # exit 0
```

## 2.4 — Golden `config_hash` vectors, wired as tests

**Deliverables:**
- Fixture: [`schemas/samples/config-hash.golden.json`](../../schemas/samples/config-hash.golden.json).
- Go reference impl + regression test: [`internal/confighash`](../../internal/confighash/confighash.go)
  — the live producer is the P2 Config Layer, which MUST reproduce these vectors bit-for-bit.
- Python cross-check: [`schemas/test_config_hash.py`](../../schemas/test_config_hash.py).

Both implementations assert the four properties from `config-hash-spec.md` §7:

| Property | Assertion |
|---|---|
| Determinism | `SumBytes(base.resolved_config) == base.config_hash` (`5427bc41fdb3…`). |
| Canonicalization | key-order-independent: re-marshaling and re-hashing yields the same hash. |
| Registry-version sensitivity | repoint `nodes[0].prompt_ref @3 → @4` ⇒ `variant_b.config_hash` (`c13b5afda2b2…`, different). |
| Seed-invariance | `run_id`/`seed`/`timestamp` are absent from `resolved_config`; asserting their absence *is* the invariant. |

The Go impl reproduces the **Python-generated canonical bytes and SHA-256 exactly**, which is the
cross-language guarantee P2 needs. The Go canonicalizer implements an RFC 8785 subset sufficient for
the `resolved_config` value domain and **fails loud** (`ErrNonCanonicalNumber`) on an exotic number
token rather than silently mis-hashing it — full RFC 8785 numeric normalization is a flagged P2
hardening item, not a hidden gap.

## How to run every Backend check

```bash
# Go (needs Go on PATH; see repo README)
go test ./internal/metricevent/ ./internal/confighash/

# schema evolution + config_hash cross-check (needs: pip install jsonschema)
python3 schemas/test_schema_evolution.py
python3 schemas/test_config_hash.py
```

The Postgres DDL (2.1) is modeled here and validated live when P2.5 stands up Postgres; P0 does not run
a Postgres instance.
