-- Down for 0032_proposal_refusal. A review and recovery artifact — NOT something the deployed binary
-- can run: internal/pgmigrate embeds only `*.up.sql`.
--
-- ⚠️ This loses every recorded refusal and, with it, the ability to tell a change the transform
-- DECLINED from one that has simply not been compiled. Both read as `unbuilt`, the surface renders the
-- weaker of the two, and the one change the engine deliberately declined to make becomes the one the
-- user never hears about — which is the disappearance BuildRefused was made a status to prevent.

BEGIN;

ALTER TABLE proposal DROP CONSTRAINT IF EXISTS proposal_refusal_dimension_is_explained;
ALTER TABLE proposal DROP CONSTRAINT IF EXISTS proposal_refusal_has_no_diff;
ALTER TABLE proposal DROP COLUMN IF EXISTS refusal_dimension;
ALTER TABLE proposal DROP COLUMN IF EXISTS refusal_reason;

DELETE FROM schema_migrations WHERE id = 32;

COMMIT;
