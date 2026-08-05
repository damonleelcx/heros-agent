-- P27 task 13.1: the device authorization a terminal uses to obtain a credential.
--
-- # Why this is a table and not a cache
--
-- The obvious implementation is an in-process map with a TTL — the record lives ten minutes and nobody
-- would miss it after a restart. That is wrong for the same reason the console's session map was wrong,
-- and it is wrong in a way that only appears in the deployment we ship: the CLI POLLS. It requests a code
-- against one replica and polls against whichever one the load balancer picks next, so a per-process map
-- means a login that succeeds or hangs depending on routing. Intermittently. With nothing logged, because
-- "no such device code" is a correct answer from a replica that never saw it.
--
-- # 🔴 Two hashes, no plaintext, and they are not the same kind of secret
--
-- `user_code_hash` is the short code a person retypes off a screen. It is low-entropy by design and it is
-- NOT a bearer token: holding it grants nothing, because approving requires an already-authenticated
-- person and can only select an organization that person is a member of. It is still stored hashed — a
-- database read must not hand somebody the list of codes currently awaiting approval.
--
-- `device_code_hash` is the CLI's polling secret: 32 random bytes, the only thing that can COLLECT the
-- issued credential. Both columns are UNIQUE, so two live authorizations can never share either code and
-- "which one did they approve" has exactly one answer.
--
-- # Single-use lives HERE, not in the caller
--
-- `decided_at` and `collected_at` are the two stamps that make this flow single-use, and both are written
-- by a conditional UPDATE rather than by a read-then-write. A double-clicked Approve, two browser tabs, or
-- a poll retried after a timeout must not produce two credentials — and a check-then-act in Go would let
-- exactly that happen under the replica count the console now runs.
--
-- # The credential reference is deliberately NOT a foreign key
--
-- `credential_id` names the `api_credential` this authorization issued. A constraint here would mean
-- revoking a credential (task 4.5's removal, which deletes nothing but stamps `revoked_at`) has to
-- consider this table — and worse, that a later hard-delete of a credential row could not proceed without
-- rewriting login history. The reference is a pointer for auditing, and its absence of a constraint is the
-- same call `run.tenant_id` makes for the same reason.

BEGIN;

CREATE TABLE IF NOT EXISTS device_authorization (
    device_id        TEXT        PRIMARY KEY CHECK (device_id <> ''),
    user_code_hash   TEXT        NOT NULL CHECK (user_code_hash <> ''),
    device_code_hash TEXT        NOT NULL CHECK (device_code_hash <> ''),
    -- What the CLI reported about the machine. A DISPLAY string, never compared and never used to find a
    -- row: it is shown on the approval screen so a person can tell which terminal they are approving, and
    -- carried onto the credential so a revocation screen names something a human recognises.
    label            TEXT        NOT NULL DEFAULT '',

    status           TEXT        NOT NULL DEFAULT 'pending'
                                 CHECK (status IN ('pending', 'approved', 'denied')),
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at       TIMESTAMPTZ NOT NULL,

    -- Written together, at approval, from the approver's verified principal and their explicit choice.
    approved_by      TEXT        NULL REFERENCES platform_user (user_id),
    tenant_id        TEXT        NULL REFERENCES tenant (tenant_id),
    decided_at       TIMESTAMPTZ,

    credential_id    TEXT        NULL,
    collected_at     TIMESTAMPTZ,

    -- A decision names WHO made it and WHERE, or it is not a decision. Without this, an approved row with
    -- a null tenant would issue a credential scoped to nothing — and the failure would be a 500 on the
    -- poll, long after the approval screen said yes.
    CONSTRAINT device_decision_is_complete
        CHECK (status = 'pending'
               OR (decided_at IS NOT NULL
                   AND (status = 'denied'
                        OR (approved_by IS NOT NULL AND tenant_id IS NOT NULL AND credential_id IS NOT NULL)))),
    -- Nothing is collected that was not approved. The poll's own UPDATE enforces this too; the constraint
    -- is what makes a hand-written repair unable to create the state.
    CONSTRAINT device_collected_only_when_approved
        CHECK (collected_at IS NULL OR status = 'approved')
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_device_user_code   ON device_authorization (user_code_hash);
CREATE UNIQUE INDEX IF NOT EXISTS idx_device_device_code ON device_authorization (device_code_hash);
-- Expired rows are swept by whoever schedules it; this is the index that sweep reads.
CREATE INDEX IF NOT EXISTS idx_device_expiry ON device_authorization (expires_at);

INSERT INTO schema_migrations (id, name) VALUES (40, 'p27_device_authorization')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
