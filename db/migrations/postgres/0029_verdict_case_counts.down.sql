-- Down for 0029_verdict_case_counts. A review and recovery artifact — NOT something the deployed binary
-- can run: internal/pgmigrate embeds only `*.up.sql`.
--
-- ⚠️ This LOSES DATA that cannot be recomputed. A verdict reported by a customer's CI has counts and no
-- ids; dropping the counts leaves `cases_fixed_json = '[]'` as the only record, and every reader then
-- reports that the change fixed nothing. The rows survive and quietly understate themselves.

BEGIN;

ALTER TABLE verdict DROP CONSTRAINT IF EXISTS verdict_counts_cover_their_ids;
ALTER TABLE verdict DROP CONSTRAINT IF EXISTS verdict_case_counts_are_counts;
ALTER TABLE verdict DROP COLUMN IF EXISTS cases_broken_count;
ALTER TABLE verdict DROP COLUMN IF EXISTS cases_fixed_count;

DELETE FROM schema_migrations WHERE id = 29;

COMMIT;
