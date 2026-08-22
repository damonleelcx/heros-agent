-- Down-migration for 0051 (P34 loop registry). Drops the one table, its guards, and the marker.
--
-- 🔴 ROLLBACK NEEDS NO MIGRATION, and this file exists so that claim can be CHECKED rather than assumed
-- (task 8.4). P34 is additive: it adds a Kind, a table, and `omitempty` fields. Nothing is removed and no
-- deployed table gains a column, so reverting the BINARY requires nothing from the database at all — a
-- `loop_entry` that the previous binary never reads is inert, exactly as `harness_entry` was inert before
-- P18's code was enabled.
--
-- Running this file is therefore optional and is the more destructive of the two rollbacks. What it costs,
-- stated rather than hidden: dropping `loop_entry` discards every sealed iteration policy, so any Variant
-- Spec whose `loop_ref` pointed at one stops resolving. That is a LOUD failure (ErrUnresolvedRef at
-- resolve, before any diff, worktree, build, or provider call), not a silent one — resolution fails
-- closed, which is exactly the behaviour that makes even this rollback safe.
--
-- 🔴 It does NOT change the config_hash of any node that carries no loop. `loop_ref` and the hashed
-- `loop` projection are additive and omit-when-absent, so a pre-P34 configuration hashes identically
-- before and after this migration in either direction, and the P0 golden vectors reproduce unchanged.
--
-- 🔴 It does NOT touch `harness_entry`, and could not: this migration never created it. Legacy
-- loop-bearing harness entries are unaffected by a loop-registry rollback in either direction, which is
-- the property that makes ADR-014's permanent legacy path independent of anything P34 deploys.
--
-- 0002's registry_verify_envelope / registry_reject_mutation functions are NOT dropped here: they are
-- 0002's, still used by the other six registries, and dropping them would break tables this migration
-- never created.

BEGIN;

DROP TRIGGER IF EXISTS loop_entry_no_truncate ON loop_entry;
DROP TRIGGER IF EXISTS loop_entry_immutable ON loop_entry;
DROP TRIGGER IF EXISTS loop_entry_coherent ON loop_entry;

DROP TABLE IF EXISTS loop_entry;

DELETE FROM schema_migrations WHERE id = 51;

COMMIT;
