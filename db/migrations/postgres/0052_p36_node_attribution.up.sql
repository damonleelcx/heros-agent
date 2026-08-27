-- P36 "The Agent Is a Graph" — per-node attribution on a stored inference. Tasks 3.8, 3.9, 6.4, 8.1.
-- Spec: openspec/changes/p36-agent-self-configuration/specs/heros-agent-definition/spec.md
--       ("An inference SHALL record the node that produced it");
-- PRD docs/prd/P36-agent-self-configuration.md; decisions.md D-36.0, D-36.2.
--
-- Dialect: PostgreSQL only. See 0046's header — the SQLite store is the dev ledger and holds no part of
-- this domain; a copy there would be a second schema nothing reads.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- What this migration does NOT do, and why that is the whole of task 3.9
-- ─────────────────────────────────────────────────────────────────────────────
-- It does NOT touch `heros_agent_version.spec_json`, in either direction, ever.
--
-- P36 changes the SHAPE of a definition, and the obvious migration is to rewrite every stored
-- `spec_json` into the new nested form. That rewrite would change every definition's `config_hash`,
-- which would orphan every pinned inference filed under the old one — the ADR-014 chain, arriving
-- through the database instead of through the seal path.
--
-- It is unnecessary as well as dangerous. decisions.md D-36.0 records the finding: a single-node
-- definition marshals to the pre-P36 bytes exactly, and `Definition.UnmarshalJSON` reads BOTH documents,
-- discriminating on the PRESENCE of `nodes` rather than on a version field — because the rows that need
-- reading were written before anybody could have set one. So an existing row is read back
-- byte-identically by the new binary with no migration at all, which is what
-- `TestAPreP36StoredDefinitionDecodesAndKeepsItsHash` asserts.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- What it DOES do
-- ─────────────────────────────────────────────────────────────────────────────
-- ONE nullable column on `heros_inference`: `nodes_json`, the per-node record of what each node of the
-- producing definition did — its provider calls, tokens, latency, contribution counts, and whether it
-- failed or was skipped by a predicate.
--
-- 🔴 A COLUMN AND NOT A TABLE. `careful-table-creation`: a new table is a one-way door and this grain
-- is already covered — `nodes_json` sits beside `edges_json` and `labels_json`, which are the same
-- decision made twice before for the same reason. The alternative considered and rejected was a
-- `heros_node_inference` table; it buys per-node SQL aggregation that `jsonb_array_elements` already
-- gives, at the cost of a second row lifecycle to keep consistent with its parent.
--
-- 🔴 It is NOT the source of the health endpoint's per-node numbers (task 8.1). Those are in-process
-- counters, read live. Querying this column per request would be exactly the "real-time query against
-- the events table" a CQRS split exists to prevent — and a health endpoint that goes slow when the
-- database does is a health endpoint that reports the wrong thing at the worst moment.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- NULLABLE, and that is a decision rather than a default
-- ─────────────────────────────────────────────────────────────────────────────
-- Every row written before this column existed has no per-node record, and it never will: nobody
-- observed which node produced those edges, because there was one node and nothing recorded it.
--
-- NULL says "not recorded". A backfilled `[{"node_id":"heros_analyst"}]` would say "recorded, and it
-- was the analyst" — inventing a measurement (zero calls, zero tokens, zero latency) that nobody took.
-- That is the same substitution `unpriced` must not render as `0`, one table over, and the reader who
-- would be misled is an operator asking why a node's latency is zero.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- 🔴 NO COLUMN ADDED HERE CAN HOLD A PROVIDER KEY
-- ─────────────────────────────────────────────────────────────────────────────
-- `nodes_json` holds node IDs and counters. There is no key field in `herosagent.NodeRun`, and
-- `TestNoTypeInThisPackageHasAFieldThatCouldCarryAKey` walks it by reflection rather than reading a
-- whitelist — so a key-shaped field added to it later is caught by the fence and not by review.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- IDEMPOTENCY GUARD (the commit body must name it)
-- ─────────────────────────────────────────────────────────────────────────────
-- Two, one per statement class:
--   1. ALTER TABLE ... ADD COLUMN IF NOT EXISTS   — the column. PG 9.6+.
--   2. INSERT ... ON CONFLICT (id) DO NOTHING     — the schema_migrations marker.
-- A second run of this file returns success and changes nothing. Asserted by
-- `TestP36MigrationIsRepeatable` (static) and by the pgproof apply-twice run where a database exists.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- EDITION SCOPE
-- ─────────────────────────────────────────────────────────────────────────────
-- It runs wherever 0046 ran and nowhere else: it ALTERs a table 0046 created, so a component that never
-- created `heros_inference` cannot run this, and the dependency is what enforces the scope rather than
-- a list somebody maintains.

BEGIN;

-- Per-node attribution for one inference (task 3.8).
--
-- The array is `herosagent.NodeRun` in the definition's ORDERING, not sorted: the ordering is the replay
-- sequence, and re-sorting it here would claim two different replay sequences are one record.
ALTER TABLE heros_inference
    ADD COLUMN IF NOT EXISTS nodes_json JSONB;

COMMENT ON COLUMN heros_inference.nodes_json IS
    'Per-node record of what each node of the producing definition did: provider calls, tokens, '
    'latency, contribution counts, failure and skip. NULL means NOT RECORDED (a row written before '
    'per-node attribution existed) and is deliberately not backfilled — a synthesised entry would '
    'report a measurement nobody took. P36 task 3.8.';

INSERT INTO schema_migrations (id, name) VALUES (52, 'p36_node_attribution')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
