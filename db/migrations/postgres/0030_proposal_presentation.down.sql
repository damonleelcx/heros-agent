-- Down for 0030_proposal_presentation. A review and recovery artifact — NOT something the deployed
-- binary can run: internal/pgmigrate embeds only `*.up.sql`.
--
-- ⚠️ This loses the only record of WHICH CALL SITE each proposal changes. The rows survive and the
-- recommendation surface renders an operator name and a hash, asking a reviewer to open a pull request
-- on faith. The node id is decided by the operator that emitted the candidate and is not recoverable
-- from a config hash.

BEGIN;

ALTER TABLE proposal DROP COLUMN IF EXISTS rationale;
ALTER TABLE proposal DROP COLUMN IF EXISTS pattern;
ALTER TABLE proposal DROP COLUMN IF EXISTS node_id;

DELETE FROM schema_migrations WHERE id = 30;

COMMIT;
