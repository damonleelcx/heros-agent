-- Down for 0049.
--
-- The two tables are dropped and the two columns are removed, in dependency order (the ledger has an
-- FK to the grant, so it goes first even though CASCADE would handle it — an explicit order is what a
-- reviewer can check).
--
-- 🔴 WHAT A ROLLBACK COSTS, stated rather than hidden:
--
--   1. **Every repository connection is gone.** A rolled-back deployment holds no grants, so every
--      connected workflow falls back to `ErrNoSource` and renders `not reported` — which is the
--      designed behaviour for a tenant who has not connected, so nothing breaks. The customer has to
--      re-authorize. The credentials themselves are NOT here to drop: they live in the deployment's
--      secret store and are removed by `ForgeSecrets.Revoke`, which this file cannot reach.
--
--      ⚠️ That is a real gap and it is named: rolling back leaves orphaned credentials in the secret
--      store with no row pointing at them. They authenticate nothing, because every read starts from a
--      grant row that no longer exists — but they are material we said we would delete on revocation,
--      and a rollback is not a revocation. An operator rolling this back should sweep the forge-read
--      namespace of the secret store.
--
--   2. **The read ledger is gone.** The record of when each repository was read, and by whom. It is
--      not re-derivable. That is the cost of the FK cascade the up migration chose, and it is the
--      correct direction for a customer-privacy artifact — but it means a rollback destroys audit
--      evidence, which is worth knowing before running one.
--
--   3. **Cloned snapshots survive as ORPHANS.** Dropping `connection_id` and `expires_at_ms` leaves
--      the `source_bundle` rows and their blobs in place, indistinguishable from pushed bundles.
--      🚫 They are deliberately NOT deleted here. A down migration that deletes customer source is a
--      migration that turns a rollback into data loss, and the safer failure is a tree that is held
--      too long and can be deleted through the ordinary `DELETE /api/v1/workflow-source` route.
--      An operator rolling back should list `source_bundle` rows written since the deploy and decide.
--
--   4. **Every local pairing is gone.** Codes in flight stop working, which reads to a person mid-flow
--      as "my code expired" — the honest message, and the one the console shows for that state. Nothing
--      is lost that a re-pairing does not restore, and no read was ever authorized by a pairing, so a
--      rolled-back deployment loses attribution rather than access.

BEGIN;

DROP INDEX IF EXISTS idx_source_clone_record_connection;
DROP TABLE IF EXISTS source_clone_record;

DROP INDEX IF EXISTS uq_source_local_pairing_code;
DROP INDEX IF EXISTS idx_source_local_pairing_tenant;
DROP TABLE IF EXISTS source_local_pairing;

DROP INDEX IF EXISTS uq_source_connection_workflow;
DROP INDEX IF EXISTS idx_source_connection_tenant;
DROP TABLE IF EXISTS source_connection;

ALTER TABLE source_bundle DROP CONSTRAINT IF EXISTS source_bundle_derived_pair;
DROP INDEX IF EXISTS idx_source_bundle_connection;
DROP INDEX IF EXISTS idx_source_bundle_expiry;
ALTER TABLE source_bundle DROP COLUMN IF EXISTS connection_id;
ALTER TABLE source_bundle DROP COLUMN IF EXISTS expires_at_ms;

DELETE FROM schema_migrations WHERE id = 49;

COMMIT;
