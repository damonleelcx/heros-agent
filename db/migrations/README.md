# Migrations

SQL migrations, organized by dialect. **Two dialects are two semantics** (backend rule) — a migration
lives under the dialect it targets and is loaded only by that dialect's path.

| Dir | Dialect | Loaded by | Holds |
|---|---|---|---|
| `postgres/` | PostgreSQL | P2.5 (not yet wired) | Eval-results & lineage schema — the P0 storage decision routes eval results to Postgres. |

The **dev ledger** (API-key auth, registries, memory) runs on **SQLite** and is migrated inline in
[`internal/db/db.go`](../../internal/db/db.go) (`migrate()` / `ensure*` functions) — that is a separate
store from the Postgres eval-results database and is intentionally not duplicated here.

## Postgres

- `0001_p0_lineage.up.sql` / `.down.sql` — P0 task 2.1. Modeled and frozen at M0; **applied live in
  P2/P2.5** when Postgres is stood up. Enforces the seven-tag non-null contract, `config_hash`
  uniqueness, and FKs (eval_result → variant / node / case / config / blob).
- `0002_p2_registries.up.sql` / `.down.sql` — P2 task 1.1. The four registries (model / prompt /
  skill / context). Expand-only: adds tables, alters nothing `0001` created, and depends on it
  (`prompt_entry.body_blob_hash` → `blob.content_hash`). Enforces that a `version_id` **is** the
  SHA-256 of the entry's content (CHECK), that a published row can never be updated or deleted
  (trigger, `HR001`), and that the denormalized `name`/`body_blob_hash` columns cannot drift from
  the hashed envelope (trigger, `HR002`).

- `0003_p2_variant_spec.up.sql` / `.down.sql` — P2 task 2.1. The authored Variant Spec (the per-node
  delta of registry version_ids + ordering + `source_revision`), keyed by
  `(config_hash, source_revision)` and FK'd to `config`. The RESOLVED side stays in `0001`'s
  `config.lineage_json`; this table deliberately does not duplicate it.
- `0004_p2_transform.up.sql` / `.down.sql` — P2 task 3.9. The generated transform: `diff_blob_hash`
  (content-addressed in the object store, never inlined), `build_status` (`built` /
  `build-rejected`), worktree/branch/commit, and the compiler's reason plus its node/dimension
  attribution on a rejection. Unique on `(config_hash, source_revision)` — the pair the diff is a
  pure function of.
- `0007_p2_verification_strength.up.sql` / `.down.sql` — [ADR-003](../../docs/adr/ADR-003-multi-language-apply-and-verification-strength.md)
  decision 3. Adds `transform.verification_strength` (`type-checked` / `syntax-checked`): what the
  build gate actually **proved**, as opposed to `0004`'s `build_status`, which says only whether it
  passed. The two are orthogonal and both are needed — once the apply path stopped being Go-only,
  `built` covered everything from "a compiler type-checked this" to "`node --check` confirmed it
  parses", and nothing in the diff, the row, or the UI revealed which. Autonomous auto-apply requires
  `type-checked`; `syntax-checked` is always human-reviewed.

  Expand-only, and it adds **one column to an IMMUTABLE table** — so it is worth reading before
  copying. `transform` rejects every `UPDATE` by trigger (`0004`), so the usual "add nullable column,
  backfill with `UPDATE`, set `NOT NULL`" recipe cannot run here and must NOT be made to run by
  disabling the trigger. Instead it uses PostgreSQL 11+'s `ADD COLUMN … NOT NULL DEFAULT` fast path,
  which fills existing rows via a table-level missing-value **without firing row triggers**, and then
  `DROP DEFAULT` in the next statement. The default therefore exists for exactly one statement: old
  rows get a value, and a new row that does not SAY what it proved fails on `NOT NULL` instead of
  silently inheriting the strongest claim in the vocabulary. That last point is the whole design —
  see the header comment in the file.

Rationale and the invariants each constraint encodes:
[`docs/decisions/backend-invariants-and-migrations.md`](../../docs/decisions/backend-invariants-and-migrations.md).

Naming: `NNNN_<slug>.up.sql` / `NNNN_<slug>.down.sql`, applied in ascending `NNNN`; each `up` records
itself in `schema_migrations` idempotently.

### Proving the constraints fire (live)

`postgres/run_pg_proof.sh` boots an **ephemeral** Postgres, applies `0001` up, deliberately attempts
each violation, asserts the DB rejects it (NOT NULL / PK / FK / CHECK / idempotency), then applies
`down` and asserts the tables are gone — tearing the cluster down on exit. No sudo, no system install.

```bash
# needs initdb/pg_ctl/postgres on PATH and: pip install "psycopg[binary]"
PATH="/path/to/postgres/bin:$PATH" bash db/migrations/postgres/run_pg_proof.sh
```

The proof logic lives in `postgres/prove_constraints.py`. This is verification tooling, not part of the
shipped service; the migration itself is applied by P2.5.

### Proving the registry guards fire (live) — `0002`

`0002`'s guards are proved by a **Go** test rather than a `prove_*.py` script, because the invariant
they enforce spans both lines of defense: `internal/registry` offers no mutation API, and the DDL
rejects a mutation attempted around it. Testing both from one place keeps that pairing honest — a
Python script could prove only the DDL half, and the two halves would drift.

```bash
make pg-proof     # boots an ephemeral Postgres in Docker; needs no local Postgres install
```

It covers `0002`, `0003`, `0004` and `0007` (`internal/registry`, `internal/variantspec`,
`internal/worktree`).
Each package runs in its OWN Postgres schema (see `internal/pgtest`): `go test` runs packages
concurrently, and `0001`'s DDL is bare `CREATE TABLE`, so sharing one schema makes them race.

The tests are behind the `pgproof` build tag (e.g. `internal/registry/store_pgproof_test.go`), so `make go`
does not compile them and CI runs them in the `db-proof` job against its Postgres service. The tag —
rather than an env-var skip — is the gate on purpose: with no reachable database the suite **fails**,
so there is no configuration in which it reports green without proving anything.

`postgres/run_pg_docker.sh` is the Docker-based sibling of `run_pg_proof.sh` and takes any command:

```bash
bash db/migrations/postgres/run_pg_docker.sh psql -c '\dt'
```
