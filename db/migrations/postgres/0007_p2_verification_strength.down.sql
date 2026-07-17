-- Rollback for 0007_p2_verification_strength.up.sql (ADR-003 decision 3).
--
-- Drops only what 0007 added: the column and its CHECK. `transform` itself, its immutability triggers,
-- and registry_reject_mutation() all belong to 0004/0002 and are untouched — dropping any of them here
-- would make rolling back 0007 break its dependencies.
--
-- ⚠️ This rollback DESTROYS evidence, and that is not a reason to refuse it — it is a reason to say so.
-- verification_strength is not derivable from anything else in the row: once it is gone, no query can
-- tell a diff a compiler stood behind from one that was only parsed. Rolling 0007 back returns the
-- system to the state ADR-003 identifies as unsafe (every transform looking equally verified), so it
-- is a step BACKWARD to 0004's world, only correct as part of also rolling back the code that reads
-- the column.

BEGIN;

ALTER TABLE transform DROP CONSTRAINT IF EXISTS transform_verification_strength_known;
ALTER TABLE transform DROP COLUMN IF EXISTS verification_strength;

DELETE FROM schema_migrations WHERE id = 7;

COMMIT;
