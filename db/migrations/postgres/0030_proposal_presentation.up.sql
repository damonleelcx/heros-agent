-- The three fields a proposal CARD renders that `proposal` has no column for.
--
-- # What is missing, and why it was not noticed
--
-- 0012 stores a proposal's identity, its hashes and its status. It stores no `node_id`, no `pattern`
-- and no `rationale` — and `api.Card`, the shape the recommendation surface renders, has all three:
--
--     NodeID    which call site this change is about
--     Pattern   the node's classifier label, which gates which operators may fire on it
--     Rationale the one-line, evidence-anchored reason ("cost bottleneck → downgrade to …")
--
-- Nothing noticed because no Go code has ever read this table into a Card. `internal/proposal` builds a
-- Presentation from a COMPILED candidate held in memory, in a demo binary, where all three are simply
-- fields on the value — so the schema and the read model were built in different phases against
-- different assumptions, and the gap only appears when something tries to serve one from the other.
-- 0025 found the same class of gap on the same table (no tenant, no workflow) for the same reason.
--
-- 🔴 A card with no node_id is not a degraded card. The whole claim a proposal makes is "change THIS
-- call site"; without it the surface renders an operator name and a hash and asks a reviewer to open a
-- pull request on faith. Storing it as a column rather than deriving it later is the point — it is
-- decided by the operator that emitted the candidate and cannot be recovered from a config hash.
--
-- # Why nullable-with-a-default rather than NOT NULL
--
-- Unlike 0025's scope columns, an absent value here is legible and harmless: an empty pattern is
-- "unclassified", which every operator's admissibility check already reads as a distinct state, and an
-- empty rationale renders as no rationale. Scope was different — a row with no tenant is readable by
-- everyone, so it had to fail loudly. This one does not, and a NOT NULL that forced a backfill would be
-- inventing a node id for a row whose node nobody recorded.
--
-- Dialect: PostgreSQL.

BEGIN;

ALTER TABLE proposal ADD COLUMN IF NOT EXISTS node_id   TEXT NOT NULL DEFAULT '';
ALTER TABLE proposal ADD COLUMN IF NOT EXISTS pattern   TEXT NOT NULL DEFAULT '';
ALTER TABLE proposal ADD COLUMN IF NOT EXISTS rationale TEXT NOT NULL DEFAULT '';

INSERT INTO schema_migrations (id, name) VALUES (30, 'proposal_presentation')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
