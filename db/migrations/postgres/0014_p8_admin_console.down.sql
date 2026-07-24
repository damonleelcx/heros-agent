-- Down-migration for 0014 (P8 admin & operations console). Drops the eight P8 tables, the write-once
-- triggers, and the schema_migrations marker.
--
-- Note what a reversal costs here, stated rather than hidden: dropping `audit_entry` discards the
-- append-only tamper-evident record of every admin action and every autonomous merge — the one thing
-- P8 promises never to lose in NORMAL operation. That is acceptable for a down-migration (a reversal
-- is destructive by definition) and is exactly why there is NO in-band mutate/delete path: the only way
-- to lose the chain is to roll the schema back, which is a deliberate operator act, not an app path.
--
-- Order respects the foreign keys: dependents (admin_role_grant, admin_session, impersonation_session)
-- before the parent (admin_principal). The reused stores this migration never created — P2.5, P4/P6,
-- P6, P7 — are untouched, because P8 owns only the operator layer.

BEGIN;

DROP TRIGGER IF EXISTS trg_audit_entry_append_only ON audit_entry;
DROP TRIGGER IF EXISTS trg_admin_role_grant_append_only ON admin_role_grant;
DROP FUNCTION IF EXISTS p8_refuse_mutation();

DROP INDEX IF EXISTS idx_admin_session_admin;
DROP INDEX IF EXISTS idx_admin_role_grant_admin;

DROP TABLE IF EXISTS gdpr_request;
DROP TABLE IF EXISTS kill_switch_state;
DROP TABLE IF EXISTS impersonation_session;
DROP TABLE IF EXISTS audit_entry;
DROP TABLE IF EXISTS permission;
DROP TABLE IF EXISTS admin_session;
DROP TABLE IF EXISTS admin_role_grant;
DROP TABLE IF EXISTS admin_principal;

DELETE FROM schema_migrations WHERE id = 14;

COMMIT;
