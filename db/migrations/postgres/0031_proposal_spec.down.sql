-- Down for 0031_proposal_spec. A review and recovery artifact — NOT something the deployed binary can
-- run: internal/pgmigrate embeds only `*.up.sql`.
--
-- ⚠️ This loses the record of WHAT each proposal changes. The rows survive as descriptions of a change
-- nobody can reconstruct: the codemod has nothing to apply, and re-deriving a spec by re-running the
-- generator would mint a different change under an id a customer may already be verifying.

BEGIN;

ALTER TABLE proposal DROP CONSTRAINT IF EXISTS proposal_spec_blob_hash_is_a_hash;
ALTER TABLE proposal DROP COLUMN IF EXISTS spec_blob_hash;

DELETE FROM schema_migrations WHERE id = 31;

COMMIT;
