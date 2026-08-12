-- P30 task 7.5: where a tenant's analysis RUNS, stored durably.
--
-- # Why this is a table and not a column on something that already exists
--
-- 🔴 Because the ABSENCE OF A ROW is the value. Q2 makes `disabled` the default, and `adminops`
-- distinguishes `defaulted` — nobody has decided — from `explicit`, which includes somebody deciding
-- `disabled`. Those are the same VALUE and different FACTS, and the console renders them differently so
-- an operator can tell how much of the fleet has actually been reviewed.
--
-- A column with a `DEFAULT 'disabled'` on a tenant table cannot express that: every tenant would arrive
-- already carrying an answer nobody gave. The distinction only survives if "no row" is a state, which
-- means the placements live in their own table keyed by tenant.
--
-- # Why there is no foreign key to a tenants table
--
-- The same reason `heros_inference.tenant_id` has none: this deployment's tenants come from more than
-- one place (a config seed, self-serve sign-up, the operator console), and a constraint that resolved
-- against one of them would refuse a placement for a tenant that exists in another. The tenant id here
-- is the same authenticated principal id every other P30 table records, and it is scoped by the
-- capability that writes it (`agent.admin`, Superadmin only), not by a reference.
--
-- # What is recorded besides the value
--
-- `reason` and `set_by` are here because `adminops.SetPlacement` already REQUIRES a reason and audits
-- the change — setting a tenant to `platform` is what makes this platform read that tenant's source
-- under a platform-held credential. The audit log is the record of the ACT; these two columns are the
-- record of the current STATE, so an operator reading the placement can see why it is what it is
-- without correlating against an audit trail by timestamp.
--
-- 🚫 `reason` is operator prose about our own fleet. It is never rendered on a customer surface and
-- never crosses the egress boundary — nothing in `internal/runlink` has a field it could occupy, and
-- `AgentDefinition` carries the placement VALUE alone.
--
-- Dialect: PostgreSQL only, the same answer 0045 and 0046 give for the same reason — the SQLite store
-- in `internal/db/db.go` is the retired agent's dev ledger and holds no part of this domain.

BEGIN;

CREATE TABLE IF NOT EXISTS heros_tenant_placement (
    tenant_id     TEXT PRIMARY KEY,
    -- platform | customer | disabled. `disabled` is a real value here, and it means somebody CHOSE it —
    -- a tenant nobody has considered has no row at all.
    placement     TEXT   NOT NULL,
    reason        TEXT   NOT NULL,
    set_by        TEXT   NOT NULL,
    updated_at_ms BIGINT NOT NULL,

    CONSTRAINT heros_tenant_placement_value
        CHECK (placement IN ('platform', 'customer', 'disabled')),
    -- A placement set with no reason is a placement nobody has to justify, and `platform` is the setting
    -- that starts reading a customer's source. The service requires one; this is the half that holds
    -- when a row is written by something other than the service.
    CONSTRAINT heros_tenant_placement_has_a_reason
        CHECK (length(btrim(reason)) > 0)
);

DO $$
DECLARE
    n INTEGER;
BEGIN
    SELECT count(*) INTO n
      FROM information_schema.columns
     WHERE table_name = 'heros_tenant_placement'
       AND column_name IN ('tenant_id', 'placement', 'reason', 'set_by', 'updated_at_ms');
    IF n <> 5 THEN
        RAISE EXCEPTION 'heros_tenant_placement has % of its 5 columns — `CREATE TABLE IF NOT EXISTS` '
                        'is a NAME guard, so a pre-existing table of another shape satisfies it '
                        'silently', n;
    END IF;

    SELECT count(*) INTO n
      FROM information_schema.check_constraints c
      JOIN information_schema.constraint_table_usage u ON u.constraint_name = c.constraint_name
     WHERE u.table_name = 'heros_tenant_placement'
       AND c.constraint_name = 'heros_tenant_placement_value';
    IF n = 0 THEN
        RAISE EXCEPTION 'heros_tenant_placement accepts any placement string. The three-state '
                        'vocabulary is the whole of task 7.5, and a fourth value written here would '
                        'be read by a gate that has no branch for it';
    END IF;
END $$;

-- 🚫 NO BACKFILL, and this is the row that would be tempting. Inserting `disabled` for every existing
-- tenant would produce exactly the right BEHAVIOUR and destroy the fact the table exists to record:
-- every tenant would read as reviewed-and-switched-off, and the console's "defaulted vs explicit"
-- column — the one that tells an operator how much of the fleet anybody has actually looked at — would
-- report a fleet fully considered on the day of the migration. Same reasoning as 0045's provenance
-- column and 0042's coverage_version.

INSERT INTO schema_migrations (id, name) VALUES (47, 'p30_tenant_placement')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
