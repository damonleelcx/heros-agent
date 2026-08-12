-- P30 tasks 9.2 and 9.5: the token ceilings a cap check reads BEFORE a provider call, and the mark a
-- stored inference carries when analysis is switched off for its tenant.
--
-- # Why a separate table from heros_spend
--
-- `heros_spend` is the METER — one row per inference, written after a run. A CAP is a decision an
-- operator made about a tenant, and it exists before any inference does. Putting a ceiling on the meter
-- would mean a tenant with no spend has no cap row, so the check would read "no cap" for exactly the
-- tenant nobody has spent anything on yet — which is the tenant a first runaway analysis lands on.
--
-- # Why the fleet cap lives here too, under a sentinel
--
-- 🔴 `tenant_id = ''` IS the fleet cap. A separate one-row table would be the same data with a second
-- shape, and the check has to read both on every call — so one query with a two-key lookup beats two
-- queries against two schemas that can disagree about what "unset" means.
--
-- The empty string is safe as a sentinel because a tenant id is a platform-issued identifier and is
-- never empty: `heros_tenant_placement` and `heros_inference` both carry one from an authenticated
-- principal. The CHECK below makes that structural rather than assumed.
--
-- # 🔴 The dangerous state this table cannot remove
--
-- NO ROW MEANS NO CAP, and no cap means unbounded. That is a real and dangerous state and it is the
-- DEFAULT — deliberately, because the alternative is worse in a specific way: a default ceiling picked
-- here would be a number nobody chose, applied to every tenant, and the first time it bit somebody it
-- would look like a product limit rather than a placeholder. `adminops.AgentSpendView.FleetCap` already
-- renders zero as "none is set" rather than as an empty cell, and `/readyz` reports it.
--
-- What makes it survivable is that Q2 makes `disabled` the default placement, so a deployment with no
-- caps also has no tenant that analyses anything. The two defaults are safe together and neither is
-- safe alone — which is worth stating, because enabling a tenant without setting a cap is exactly the
-- one-step change that separates them.
--
-- Dialect: PostgreSQL only, the same answer 0045–0047 give for the same reason.

BEGIN;

CREATE TABLE IF NOT EXISTS heros_cap (
    -- The tenant this ceiling applies to, or '' for the FLEET-WIDE ceiling.
    tenant_id     TEXT   NOT NULL,
    -- Tokens is the ceiling over the window the checker reads. A cap of 0 is not "unlimited" — it is
    -- refused below, because "unlimited" spelled as the zero value is the version of unlimited that
    -- nobody chose. An operator removing a cap DELETES the row.
    max_tokens    BIGINT NOT NULL,
    reason        TEXT   NOT NULL,
    set_by        TEXT   NOT NULL,
    updated_at_ms BIGINT NOT NULL,

    PRIMARY KEY (tenant_id),

    -- 🔴 A zero or negative ceiling is refused at the schema. A row reading `max_tokens = 0` would be
    -- ambiguous between "spend nothing" and "no limit", and the checker would have to pick — so the
    -- state cannot be written. Removing a cap is a DELETE, which is unambiguous.
    CONSTRAINT heros_cap_positive CHECK (max_tokens > 0),
    CONSTRAINT heros_cap_has_a_reason CHECK (length(btrim(reason)) > 0)
);

DO $$
DECLARE
    n INTEGER;
BEGIN
    SELECT count(*) INTO n
      FROM information_schema.columns
     WHERE table_name = 'heros_cap'
       AND column_name IN ('tenant_id', 'max_tokens', 'reason', 'set_by', 'updated_at_ms');
    IF n <> 5 THEN
        RAISE EXCEPTION 'heros_cap has % of its 5 columns — `CREATE TABLE IF NOT EXISTS` is a NAME '
                        'guard, so a pre-existing table of another shape satisfies it silently', n;
    END IF;

    SELECT count(*) INTO n
      FROM information_schema.check_constraints c
      JOIN information_schema.constraint_table_usage u ON u.constraint_name = c.constraint_name
     WHERE u.table_name = 'heros_cap' AND c.constraint_name = 'heros_cap_positive';
    IF n = 0 THEN
        RAISE EXCEPTION 'heros_cap accepts a zero ceiling. `0` is ambiguous between `spend nothing` and '
                        '`no limit`, and a checker reading it has to guess — removing a cap must be a '
                        'DELETE so that it cannot be';
    END IF;
END $$;

-- The cap check reads spend for a tenant over a WINDOW, so the meter needs an index that serves
-- "this tenant, since T". 0046 created `idx_heros_spend_tenant_time` for exactly this read; it is
-- named here rather than re-created, because the check depends on it and a reader of this migration
-- should not have to discover that from a query plan.

-- ── Task 9.5 · a stored inference can be marked STALE ────────────────────────────────────────────
--
-- Q5's assumed answer is RETAINED AND MARKED STALE, and the assumption is implemented rather than
-- deferred — because deferring it means the first tenant who disables analysis gets whatever the code
-- happens to do, which is delete-by-omission if nothing handles it.
--
-- 🔴 NULLABLE, and NULL means NOT STALE. There is no back-fill and no default: every row already in
-- this table was written while analysis was running for its tenant, so `NULL` is the honest value and
-- a default of anything else would mark facts nobody examined.
--
-- The reason is a closed vocabulary (`analysis_disabled`, `definition_retired`), CHECKed here so a
-- third value cannot arrive from a writer that has no branch for it — the same discipline
-- `heros_inference_placement` follows one table up.
ALTER TABLE heros_inference ADD COLUMN IF NOT EXISTS stale_reason TEXT;
ALTER TABLE heros_inference ADD COLUMN IF NOT EXISTS stale_at_ms BIGINT;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.check_constraints c
          JOIN information_schema.constraint_table_usage u ON u.constraint_name = c.constraint_name
         WHERE u.table_name = 'heros_inference' AND c.constraint_name = 'heros_inference_stale_reason'
    ) THEN
        ALTER TABLE heros_inference ADD CONSTRAINT heros_inference_stale_reason
            CHECK (stale_reason IS NULL OR stale_reason IN ('analysis_disabled', 'definition_retired'));
    END IF;

    -- A reason and a timestamp travel together. A row marked stale with no timestamp cannot answer
    -- "since when", which is the question asked when a customer notices their graph stopped moving.
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.check_constraints c
          JOIN information_schema.constraint_table_usage u ON u.constraint_name = c.constraint_name
         WHERE u.table_name = 'heros_inference' AND c.constraint_name = 'heros_inference_stale_pair'
    ) THEN
        ALTER TABLE heros_inference ADD CONSTRAINT heros_inference_stale_pair
            CHECK ((stale_reason IS NULL) = (stale_at_ms IS NULL));
    END IF;
END $$;

INSERT INTO schema_migrations (id, name) VALUES (48, 'p30_agent_caps_and_stale')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
