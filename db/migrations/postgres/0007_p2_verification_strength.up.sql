-- P2 Runtime — what the gate PROVED. ADR-003 decision 3.
-- Spec: docs/adr/ADR-003-multi-language-apply-and-verification-strength.md; ADR-001 requirement 2.
--
-- Dialect: PostgreSQL 11+. Expand-only: ADDS one column to 0004's `transform`. Depends on 0004.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- Why this is a column and not a log line
-- ─────────────────────────────────────────────────────────────────────────────
-- ADR-001 requirement 2 — "a transform that fails to compile/build the target is rejected before it is
-- ever proposed" — carried the entire safety argument for automatically editing a customer's source.
-- It was true because the one gate was `go build`, a FULL TYPE CHECK: a rewrite that passes a string
-- where an enum belongs cannot reach the customer.
--
-- ADR-003 made the apply path language-neutral, and the gate stopped meaning one thing. `py_compile`
-- proves a Python file PARSES; it does not prove the call is well-formed. `model="x"` -> `modle="x"`
-- parses perfectly and fails at runtime. `node --check` is syntax only — JavaScript has no type system
-- to check against. So the guarantee became a property of the target language and repository, varying
-- by an order of magnitude, while the word `built` in build_status stayed exactly the same.
--
-- That is why this is a COLUMN. It is not derivable after the fact: nothing in the diff, the commit,
-- or the build log says whether a compiler stood behind it, and by the time a human reviews the change
-- the worktree and its toolchain are gone. A reviewer extending trust earned by the Go path to a
-- Python diff that never earned it is the exact failure ADR-003 rejected at L1/L2 — and a log line
-- cannot stop it, because the review UI does not read logs (health-signal-surface: a health signal
-- that lives only in a log is one nobody reads).
--
-- 🚫 A `syntax-checked` diff must never be presentable as though it were `type-checked` (decision 3).
-- This column is what makes that structural rather than a matter of discipline.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- Why no UPDATE is needed, and why that is worth stating
-- ─────────────────────────────────────────────────────────────────────────────
-- 0004 makes `transform` IMMUTABLE by trigger (registry_reject_mutation, from 0002). A column that
-- had to be filled in later would be unwritable, so this only works if the strength is known at INSERT
-- time. It is: verification PRECEDES the record. internal/worktree.Applier.Apply verifies, and only
-- then constructs the Applied that Store.Put inserts — there is no window in which a row exists
-- without its strength. The immutability trigger is NOT relaxed here and must not be.
--
-- The same reasoning 0004 gives for build_status applies verbatim: the transform is a pure function of
-- (config_hash, source_revision), so a "changed" strength is a different pair. A toolchain change that
-- silently flipped a stored verdict is precisely what a pinned toolchain (PRD §7) exists to prevent.
--
-- ─────────────────────────────────────────────────────────────────────────────
-- Why NOT NULL, why the default exists for one statement, and why it is then DROPPED
-- ─────────────────────────────────────────────────────────────────────────────
-- This is the subtle part, and getting it wrong recreates the very bug the ADR is about.
--
-- A permanent `DEFAULT 'type-checked'` would be the most dangerous line in this repository: any code
-- path that forgot to state what it proved would silently claim the STRONGEST guarantee, and the
-- claim would be invisible — a green row, a confident UI, and no compiler anywhere behind it. So new
-- rows get no default at all. An INSERT that does not name a strength FAILS on NOT NULL. Stating what
-- you proved is mandatory, and forgetting is loud.
--
-- Existing rows still need a value, and they cannot be UPDATEd (the trigger). PostgreSQL 11+ resolves
-- this exactly: ADD COLUMN ... NOT NULL DEFAULT stores the value as a table-level "missing value"
-- without rewriting rows and WITHOUT firing row triggers, so the backfill needs no UPDATE and does not
-- touch immutability. DROP DEFAULT afterwards leaves the already-filled rows alone and removes the
-- default for every future INSERT. The default therefore exists for exactly one statement.
--
-- 'type-checked' is the correct value for those rows, as a matter of fact rather than convenience:
-- every row that can exist under 0004 was written by the Go-only apply path, whose sole gate was
-- worktree.GoBuilder running `go build ./...` — a full type check. This is not an assumption about
-- history; it is the only implementation the code has ever had. (Had that not been provable, the
-- honest column would have been nullable with NULL meaning "unknown", read as requiring human review.)

BEGIN;

-- The strength the gate PROVED (ADR-003 decision 2). The CHECK keeps the vocabulary closed for the
-- same reason 0004 closes build_status: a typo must not invent a third claim that the automation gate
-- in internal/worktree.Strength.AllowsAutonomousApply has never heard of. Note what that function does
-- with an unknown value — it refuses. The vocabulary is closed here so it never has to.
ALTER TABLE transform
    ADD COLUMN IF NOT EXISTS verification_strength TEXT NOT NULL DEFAULT 'type-checked';

-- The default has done its one job (the pre-ADR-003 rows above). From here on, a row must SAY what it
-- proved.
ALTER TABLE transform ALTER COLUMN verification_strength DROP DEFAULT;

ALTER TABLE transform DROP CONSTRAINT IF EXISTS transform_verification_strength_known;
ALTER TABLE transform ADD CONSTRAINT transform_verification_strength_known
    CHECK (verification_strength IN ('type-checked', 'syntax-checked'));

COMMENT ON COLUMN transform.verification_strength IS
    'What the build gate PROVED (ADR-003): type-checked = a type checker proved the rewritten program '
    'well-typed; syntax-checked = only parse validity was proved, a type error would NOT have been '
    'caught. Autonomous auto-apply requires type-checked; syntax-checked is always human-reviewed. '
    'Orthogonal to build_status, which says only whether the gate passed.';

INSERT INTO schema_migrations (id, name) VALUES (7, 'p2_verification_strength')
    ON CONFLICT (id) DO NOTHING;

COMMIT;
