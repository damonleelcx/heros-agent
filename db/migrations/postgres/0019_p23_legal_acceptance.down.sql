-- Down-migration for 0019 (P23 consent records). Drops only what 0019 created.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- 🔴 WHAT A REVERSAL COSTS, STATED RATHER THAN HIDDEN
-- ─────────────────────────────────────────────────────────────────────────────
-- Dropping `legal_acceptance` DISCARDS EVERY RECORD OF WHAT A CUSTOMER ACCEPTED AND WHEN. That is not a
-- feature regression; it is the destruction of evidence. The first question in a billing dispute, a
-- security review and a data-protection audit is "what exactly did this customer accept, and when" —
-- and after this migration runs, the honest answer is that nobody can say.
--
-- Unlike most rollbacks in this chain, this one does not fail loudly afterwards. The consent gate simply
-- finds no acceptance and asks again, which looks like normal behaviour. **The loss is silent**, which is
-- precisely why it is written here in capitals rather than left to be discovered.
--
-- Run this only when reverting a deployment where the table was never populated — and if you are not
-- certain it was never populated, take a dump first. There is no other copy: the record exists here or
-- it does not exist.
--
-- The consent DOCUMENTS are unaffected. They ship in the console image (ADR-011) and every version stays
-- served at its permanent address, so the text survives this rollback even though the record of who
-- accepted it does not.

BEGIN;

-- The triggers refuse DELETE and TRUNCATE, but DROP TABLE is DDL and is not intercepted by them. That
-- asymmetry is deliberate: the guards protect the data from application-level mistakes, and a schema
-- migration is a decision taken by an operator who has read this file.
DROP TRIGGER IF EXISTS legal_acceptance_no_truncate ON legal_acceptance;
DROP TRIGGER IF EXISTS legal_acceptance_no_rewrite ON legal_acceptance;

DROP TABLE IF EXISTS legal_acceptance;

DROP FUNCTION IF EXISTS legal_acceptance_append_only();

DELETE FROM schema_migrations WHERE id = 19;

COMMIT;
