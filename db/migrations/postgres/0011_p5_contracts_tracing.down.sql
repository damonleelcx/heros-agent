-- Down-migration for 0011 (P5 contracts + tracing). Drops the six P5 tables and the lineage column it
-- added, and removes the schema_migrations marker. Order respects FKs: dependents before parents.
--
-- The lineage column is dropped LAST and separately, mirroring how it was added separately (独立 ALTER).
-- Dropping it loses lineage on existing rows; that is acceptable for a down-migration (a reversal is a
-- destructive operation by definition), and it leaves every variant_spec row a valid root, exactly as a
-- pre-P5 database had them.

BEGIN;

DROP TABLE IF EXISTS anti_pattern;
DROP TABLE IF EXISTS behavioral_label;
DROP TABLE IF EXISTS recon_edge;
DROP TABLE IF EXISTS recon_call;
DROP TABLE IF EXISTS recon_node;
DROP TABLE IF EXISTS reconciliation;
DROP TABLE IF EXISTS inserted_adapter;

DROP INDEX IF EXISTS idx_variant_spec_parent;
ALTER TABLE variant_spec DROP COLUMN IF EXISTS parent_config_hash;

DELETE FROM schema_migrations WHERE id = 11;

COMMIT;
