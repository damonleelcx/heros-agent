-- Down-migration for 0018 (P18 harness registry). Drops the one table, its guards, and the marker.
--
-- What a reversal costs, stated rather than hidden: dropping `harness_entry` discards every sealed
-- harness strategy, so any Variant Spec whose `harness_ref` pointed at one stops resolving. That is a
-- LOUD failure (ErrUnresolvedRef at resolve, before any diff, worktree, build, or provider call), not a
-- silent one — resolution fails closed, which is exactly the behaviour that makes this rollback safe.
--
-- 🔴 Rolling this back does NOT change the config_hash of any node that carries no harness strategy. The
-- harness field is additive and omit-when-absent, so a pre-P18 configuration hashes identically before
-- and after this migration in either direction, and the P0 golden vectors reproduce unchanged. Only specs
-- that actually bound a harness strategy are affected, and they fail to resolve rather than resolving to
-- something else — never to `single-shot`, which would run one turn under a hash naming a loop.
--
-- 0002's registry_verify_envelope / registry_reject_mutation functions are NOT dropped here: they are
-- 0002's, still used by the other five registries, and dropping them would break tables this migration
-- never created.

BEGIN;

DROP TRIGGER IF EXISTS harness_entry_no_truncate ON harness_entry;
DROP TRIGGER IF EXISTS harness_entry_immutable ON harness_entry;
DROP TRIGGER IF EXISTS harness_entry_coherent ON harness_entry;

DROP TABLE IF EXISTS harness_entry;

DELETE FROM schema_migrations WHERE id = 18;

COMMIT;
