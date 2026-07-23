-- Down-migration for 0012 (P5.5 proposals + verification). Drops the four P5.5 tables and the `split`
-- column it added to eval_result, and removes the schema_migrations marker. Order respects FKs:
-- dependents (rank_entry, verdict, proposal_evidence) before the parent (proposal).
--
-- The eval_result.split column is dropped LAST and separately, mirroring how it was added separately
-- (独立 ALTER). Dropping it loses split attribution on existing verification rows; that is acceptable
-- for a down-migration (a reversal is destructive by definition) and leaves every eval_result row a
-- valid row exactly as a pre-P5.5 database had them.

BEGIN;

DROP INDEX IF EXISTS idx_proposal_diagnosis;
DROP INDEX IF EXISTS idx_verdict_gate_result;
DROP TABLE IF EXISTS rank_entry;
DROP TABLE IF EXISTS verdict;
DROP TABLE IF EXISTS proposal_evidence;
DROP TABLE IF EXISTS proposal;

ALTER TABLE eval_result DROP COLUMN IF EXISTS split;

DELETE FROM schema_migrations WHERE id = 12;

COMMIT;
