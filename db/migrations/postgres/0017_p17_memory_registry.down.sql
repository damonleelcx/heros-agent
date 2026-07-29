-- Down-migration for 0017 (P17 memory registry). Drops the one table, its guards, and the marker.
--
-- What a reversal costs, stated rather than hidden: dropping `memory_entry` discards every sealed memory
-- strategy, so any Variant Spec whose `memory_ref` pointed at one stops resolving. That is a LOUD failure
-- (ErrUnresolvedRef at resolve, before any diff, worktree, build, or provider call), not a silent one —
-- resolution fails closed, which is exactly the behaviour that makes this rollback safe to perform.
--
-- 🔴 Rolling this back does NOT change the config_hash of any node that carries no memory strategy. The
-- memory field is additive and omit-when-absent, so a pre-P17 configuration hashes identically before and
-- after this migration in either direction, and the P0 golden vectors reproduce unchanged. Only specs
-- that actually bound a memory strategy are affected, and they fail to resolve rather than resolving to
-- something else.
--
-- 0002's registry_verify_envelope / registry_reject_mutation functions are NOT dropped here: they are
-- 0002's, still used by the other four registries, and dropping them would break tables this migration
-- never created.

BEGIN;

DROP TRIGGER IF EXISTS memory_entry_no_truncate ON memory_entry;
DROP TRIGGER IF EXISTS memory_entry_immutable ON memory_entry;
DROP TRIGGER IF EXISTS memory_entry_coherent ON memory_entry;

DROP TABLE IF EXISTS memory_entry;

DELETE FROM schema_migrations WHERE id = 17;

COMMIT;
