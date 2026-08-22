package pgmigrate

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// P34 §8 — the loop registry's migration, its edition scope, and the rollback claim.
//
// 🔴 None of this needs a live Postgres, and that is deliberate. The live proofs are in
// `apply_pgproof_test.go` behind the `pgproof` tag and they run where a database exists; these are the
// assertions that must hold on EVERY build, including the one a contributor runs before pushing —
// because the properties they check are the ones whose violation is silent.

const (
	p34Up   = "0051_p34_loop_registry.up.sql"
	p34Down = "0051_p34_loop_registry.down.sql"
)

// p34SQL returns the migration's STATEMENTS, with `--` comments stripped.
//
// 🔴 Stripping is load-bearing, not tidying. These migrations carry long comments that quote the very
// things the fences below forbid — `harness_entry` appears three times explaining why it is not
// touched, and `CREATE OR REPLACE TRIGGER` appears explaining why it is not used. A scan over the raw
// bytes reports every one of those as a violation.
//
// That is not a near-miss: a fence that fires on a file's PROSE gets read as broken and then gets
// deleted, taking the real check with it. Both of these fired on their first run for exactly that
// reason, which is why this function exists rather than a narrower regex.
func p34SQL(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		out = append(out, line)
	}
	stripped := strings.Join(out, "\n")
	if strings.TrimSpace(stripped) == "" {
		t.Fatalf("%s is entirely comments; the fences below would pass on an empty file", name)
	}
	return stripped
}

// p34Prose returns the migration's COMMENTS, for the assertions that are about what a reader is told
// rather than about what the database does.
func p34Prose(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// ── 8.1 — a kind and omitempty fields, and NO column on a deployed table ─────────────────────────

// TestP34AddsNoColumnToADeployedTable is the assertion ADR-014's whole argument reduces to, checked
// against the SQL rather than trusted.
//
// The orphaning chain: alter `harness_entry` → a loop-bearing entry's content changes → its
// `version_id` changes → the `config_hash` of every spec referencing it changes → every measurement
// taken on a multi-turn node becomes UNREACHABLE from any spec anyone can construct. A migration that
// touched that table would be that chain arriving through the database instead of through the seal
// path, and nothing would error when it did.
func TestP34AddsNoColumnToADeployedTable(t *testing.T) {
	up := p34SQL(t, p34Up)

	// 🔴 It may CREATE its own table and nothing else. ALTER of any kind, on any table, is the failure.
	if m := regexp.MustCompile(`(?i)\bALTER\s+TABLE\b`).FindString(up); m != "" {
		t.Errorf("%s contains %q. P34 is expand-only: it adds a Kind and `omitempty` fields, and adds no "+
			"column to any table that is already deployed.", p34Up, m)
	}
	for _, deployed := range []string{"harness_entry", "memory_entry", "context_entry", "model_entry",
		"prompt_entry", "skill_entry", "variant_spec", "config"} {
		// `schema_migrations` is the one deployed table it writes to, and it INSERTs its own marker row —
		// which is data, not schema. Everything else must not appear at all.
		if strings.Contains(up, deployed) {
			t.Errorf("%s names the deployed table %q. Altering `harness_entry` in particular is ADR-014's "+
				"orphaning chain arriving through the database: every loop-bearing entry's version_id would "+
				"move, and every measurement filed under the old config_hash would become unreachable.",
				p34Up, deployed)
		}
	}
	if !strings.Contains(up, "CREATE TABLE IF NOT EXISTS loop_entry") {
		t.Errorf("%s does not create loop_entry", p34Up)
	}
}

// TestP34IsIdempotentByConstruction checks the three guards the commit body names — one per statement
// class, because `IF NOT EXISTS` does not exist for every one of them.
//
// 🚫 This does not prove a second run succeeds; only a live database does that
// (`TestASecondApplyIsANoOp`, behind `pgproof`). What it proves is that the CONSTRUCTS are present, on
// every build — because the way this requirement fails is a `CREATE TRIGGER` written without its
// `DROP TRIGGER IF EXISTS`, which looks fine in review and only fails on a re-run nobody does locally.
func TestP34IsIdempotentByConstruction(t *testing.T) {
	up := p34SQL(t, p34Up)

	for _, guard := range []struct{ what, needle string }{
		{"the table", "CREATE TABLE IF NOT EXISTS"},
		{"the migration marker", "ON CONFLICT (id) DO NOTHING"},
	} {
		if !strings.Contains(up, guard.needle) {
			t.Errorf("%s: %s has no idempotency guard (%q)", p34Up, guard.what, guard.needle)
		}
	}

	// Every CREATE TRIGGER must be preceded by a DROP TRIGGER IF EXISTS for the same name.
	//
	// 🔴 Not `CREATE OR REPLACE TRIGGER`, which is PG14+ — this deployment targets 11+. The
	// DROP-then-CREATE pair is re-runnable AND re-points an existing trigger at the current function
	// definition, which a bare `CREATE TRIGGER IF NOT EXISTS` (which does not exist) could not do.
	creates := regexp.MustCompile(`CREATE TRIGGER (\w+)`).FindAllStringSubmatch(up, -1)
	if len(creates) == 0 {
		t.Fatal("no triggers are attached; the content-address and immutability guards are what make a " +
			"registry entry immutable, and a table without them is a registry anyone can rewrite")
	}
	for _, m := range creates {
		name := m[1]
		if !strings.Contains(up, "DROP TRIGGER IF EXISTS "+name) {
			t.Errorf("%s creates trigger %q with no preceding DROP TRIGGER IF EXISTS; a second run fails",
				p34Up, name)
		}
	}
	if strings.Contains(up, "CREATE OR REPLACE TRIGGER") {
		t.Errorf("%s uses CREATE OR REPLACE TRIGGER, which is PG14+; this deployment targets 11+", p34Up)
	}
}

// TestP34AttachesTheKindGuard — the database half of the fail-closed guarantee. Without it, a HARNESS
// envelope could be filed into `loop_entry` by any SQL that bypasses internal/registry, and the two id
// spaces the Kind exists to separate would be crossable by a path nobody audits.
func TestP34AttachesTheKindGuard(t *testing.T) {
	up := p34SQL(t, p34Up)
	if !strings.Contains(up, "registry_verify_envelope('loop')") {
		t.Error("the coherence trigger does not pin the envelope's kind to 'loop'. The Kind being part of " +
			"the content address is what makes a cross-dimension paste fail closed, and this trigger is " +
			"the half of it that holds against SQL which never went through internal/registry.")
	}
	for _, fn := range []string{"registry_verify_envelope", "registry_reject_mutation"} {
		if !strings.Contains(up, fn) {
			t.Errorf("%s does not attach %s; the seventh registry must reuse 0002's guards rather than "+
				"acquire a seventh set that can drift from the other six", p34Up, fn)
		}
	}
}

// ── 8.2 — the migration runs only where the edition deploys the component ────────────────────────

// TestP34RunsOnlyWhereTheRegistriesAreDeployed is task 8.2, and the mechanism is the point.
//
// 🔴 The scope is enforced by a DEPENDENCY, not by a list somebody maintains. This migration attaches
// `registry_verify_envelope` and `registry_reject_mutation`, which are 0002's — so a component that
// never ran 0002 cannot run this one, and the failure is a loud missing-function error rather than a
// silently-created table nobody uses.
//
// A hand-maintained "which editions get 0051" list would be a second source of truth about deployment
// topology, and the copy is always the one that goes stale.
func TestP34RunsOnlyWhereTheRegistriesAreDeployed(t *testing.T) {
	up := p34SQL(t, p34Up)
	if !strings.Contains(up, "registry_verify_envelope") {
		t.Fatal("the migration does not depend on 0002's functions, so nothing structurally prevents it " +
			"from running on a component that deploys no registry")
	}
	// And the file SAYS so, because a dependency that only exists in the SQL is one a reader has to
	// reverse-engineer when deciding whether an edition needs this migration.
	if !strings.Contains(p34Prose(t, p34Up), "EDITION SCOPE") {
		t.Error("the migration does not state its edition scope. The dependency enforces it; the comment " +
			"is what lets somebody planning a deployment know that without reading the DDL.")
	}
}

// ── 8.4 — rollback needs no migration, ASSERTED rather than assumed ──────────────────────────────

// TestRollbackNeedsNoMigration is task 8.4, and the wording of the task is the reason the test exists:
// *"assert this rather than assume it."*
//
// The claim: reverting the BINARY requires nothing from the database. It holds because P34 is additive
// in every direction — a new table, a new Kind, and `omitempty` fields — so a previous binary
// encountering a `loop_entry` it never reads is in exactly the position a pre-P18 binary was in with
// respect to `harness_entry`: it is inert.
//
// What would break it is a NOT NULL column added to something deployed, or a changed constraint on an
// existing table. Both are checked above; this asserts the claim is WRITTEN DOWN in the down-migration,
// where an operator reaching for a rollback will actually be reading.
func TestRollbackNeedsNoMigration(t *testing.T) {
	down := p34SQL(t, p34Down)

	if !strings.Contains(p34Prose(t, p34Down), "ROLLBACK NEEDS NO MIGRATION") {
		t.Error("the down-migration does not state that rolling back the binary needs nothing from the " +
			"database. An operator under pressure reads this file, and 'run the down migration' is the " +
			"more destructive of the two options — it discards every sealed iteration policy.")
	}
	// It must also say what running it DOES cost, because a file that says only "safe" is one somebody
	// runs without reading.
	if !strings.Contains(p34Prose(t, p34Down), "stops resolving") {
		t.Error("the down-migration does not state that specs pinning a loop_ref stop resolving after it " +
			"runs. That is a LOUD failure and therefore a safe one — but only if it is expected.")
	}
	// 🔴 And it must not touch harness_entry, in either direction. A rollback that dropped a column from
	// the legacy table would orphan exactly the measurements ADR-014 refuses to orphan.
	if strings.Contains(down, "harness_entry") {
		t.Error("the down-migration names harness_entry. Legacy loop-bearing entries must be unaffected " +
			"by a loop-registry rollback in either direction — that independence is what makes ADR-014's " +
			"permanent legacy path independent of anything P34 deploys.")
	}
	if m := regexp.MustCompile(`(?i)\bALTER\s+TABLE\b`).FindString(down); m != "" {
		t.Errorf("the down-migration contains %q; nothing was altered on the way up, so nothing may be "+
			"altered on the way down", m)
	}
}

// TestP34DownDropsExactlyWhatUpCreated — a down-migration that missed a trigger would leave an
// orphaned guard pointing at a table that no longer exists, and the next `up` would fail on a name
// collision nobody could explain.
func TestP34DownDropsExactlyWhatUpCreated(t *testing.T) {
	up, down := p34SQL(t, p34Up), p34SQL(t, p34Down)
	for _, m := range regexp.MustCompile(`CREATE TRIGGER (\w+)`).FindAllStringSubmatch(up, -1) {
		if !strings.Contains(down, "DROP TRIGGER IF EXISTS "+m[1]) {
			t.Errorf("the down-migration does not drop trigger %q", m[1])
		}
	}
	if !strings.Contains(down, "DROP TABLE IF EXISTS loop_entry") {
		t.Error("the down-migration does not drop loop_entry")
	}
	if !strings.Contains(down, "DELETE FROM schema_migrations WHERE id = 51") {
		t.Error("the down-migration does not remove its ledger row; the next `up` would then skip itself " +
			"and leave a database with no loop_entry that believes it has one")
	}
}

// TestP34IsLoadedByTheEmbeddedSet — a migration file that is not in the embed is a file that ships and
// never runs. It is the quietest possible failure: the table is simply absent, and the first symptom is
// a resolve error on a customer's spec.
func TestP34IsLoadedByTheEmbeddedSet(t *testing.T) {
	all, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	for _, m := range all {
		if m.ID == 51 {
			if !strings.Contains(m.Name, "p34_loop_registry") {
				t.Fatalf("migration 51 is named %q", m.Name)
			}
			return
		}
	}
	t.Fatal("migration 51 is not in the embedded set; it would ship and never run, and the first symptom " +
		"would be a resolve error on a customer's spec")
}
