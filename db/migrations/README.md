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
