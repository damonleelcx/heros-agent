//go:build pgproof

// Live-Postgres proof for the Variant Spec store (task 2.1).
//
// Behind the `pgproof` tag, like internal/registry's: `make go` does not compile it, `make pg-proof`
// runs it, and with no reachable database it FAILS rather than skips.
package variantspec

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/lib/pq"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	// Its own schema: `go test` runs packages concurrently, and this proof and internal/registry's
	// both apply 0001 — whose DDL is bare CREATE TABLE — against the same server. See internal/pgtest.
	db, err := pgtest.Open("proof_variantspec")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	testDB = db
	for _, f := range []string{"0001_p0_lineage.up.sql", "0002_p2_registries.up.sql", "0003_p2_variant_spec.up.sql"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", f))
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", f, err)
			os.Exit(1)
		}
		if _, err := db.Exec(string(b)); err != nil {
			fmt.Fprintf(os.Stderr, "apply %s: %v\n", f, err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// seedWorkflow creates the FK targets 0001 requires (config -> workflow, variant -> workflow).
func seedWorkflow(t *testing.T, id string) {
	t.Helper()
	if _, err := testDB.Exec(
		`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		 VALUES ($1, 'local://test', 'abc123', 'go', '1.0.0') ON CONFLICT (workflow_id) DO NOTHING`, id); err != nil {
		t.Fatalf("seed workflow: %v", err)
	}
}

func resolveForTest(t *testing.T, spec *VariantSpec, regs Registries) *Resolved {
	t.Helper()
	r, err := Resolve(context.Background(), spec, testIR(), regs)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return r
}

func TestPG_Put_StoresLineageAndSpecAndRoundTrips(t *testing.T) {
	ctx := context.Background()
	seedWorkflow(t, "wf1")
	s := NewStore(testDB)

	regs := newFakeRegistries()
	regs.models["m1"] = modelEntry("m1", "openai", "gpt-5")
	spec := baseSpec()
	spec.Nodes["n_a"] = NodeOverride{ModelRef: "m1"}
	r := resolveForTest(t, spec, regs)

	if err := s.Put(ctx, r, spec, "v_label", "baseline"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	gotSpec, gotLineage, err := s.Get(ctx, r.ConfigHash, r.SourceRevision)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if gotSpec.SourceRevision != spec.SourceRevision || len(gotSpec.Order) != 2 {
		t.Errorf("spec did not round-trip: %+v", gotSpec)
	}
	if gotSpec.Nodes["n_a"].ModelRef != "m1" {
		t.Errorf("the authored override did not round-trip: %+v", gotSpec.Nodes)
	}
	// L3: the value propagated without drifting. Get() re-hashes the stored lineage internally, but
	// assert it here too so the failure names the property rather than an opaque mismatch.
	rehash, err := gotLineage.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if rehash != r.ConfigHash {
		t.Errorf("stored lineage re-hashes to %s, not the %s it is filed under", Display(rehash), Display(r.ConfigHash))
	}
}

// The property P0's frozen schema silently depends on, and the reason this proof exists.
//
// `config.lineage_json` is JSONB, which RE-SERIALIZES: it reorders keys (length-then-byte, not RFC
// 8785's byte order) and stores numbers as `numeric`. If that round trip altered a single number
// token, the stored lineage would no longer re-hash to its own config_hash — replay-from-lineage
// would be broken, and every result keyed by that hash would be unreproducible. Nothing in the
// schema enforces this; it holds only because confighash refuses to hash a number that is not
// already in shortest form, so no other kind can reach the column.
//
// Asserted over the numbers most likely to break: a pinned zero, a repeating decimal, a large int.
func TestPG_JSONBRoundTripPreservesTheHashedNumbers(t *testing.T) {
	ctx := context.Background()
	seedWorkflow(t, "wf_numbers")

	rc := ResolvedConfig{
		IRVersion: "1.0.0",
		Nodes: []ResolvedNode{{
			NodeID: "n_a", ModelRef: "openai/gpt-5", PromptRef: "inline://x",
			SkillRefs: []string{}, ContextPolicy: "full", ContextParams: map[string]any{},
			ProviderParams: map[string]any{
				"temperature":     0.0,              // a pinned zero must not vanish
				"top_p":           0.7,              // a repeating binary fraction
				"max_tokens":      1024,             //
				"thinking_budget": 4096,             //
				"big":             9007199254740991, // 2^53-1: the edge of exact float64
			},
		}},
		Edges: []ResolvedEdge{},
	}
	want, err := rc.Hash()
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	canon, err := rc.Canonical()
	if err != nil {
		t.Fatalf("Canonical: %v", err)
	}

	if _, err := testDB.ExecContext(ctx,
		`INSERT INTO variant (variant_id, workflow_id, label) VALUES ('v_numbers','wf_numbers','n')
		 ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed variant: %v", err)
	}
	if _, err := testDB.ExecContext(ctx,
		`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		 VALUES ($1,'v_numbers','wf_numbers','1.0.0',$2) ON CONFLICT DO NOTHING`,
		want, string(canon)); err != nil {
		t.Fatalf("insert config: %v", err)
	}

	got, err := readLineage(ctx, testDB, want)
	if err != nil {
		t.Fatalf("readLineage: %v", err)
	}
	if string(got) != string(canon) {
		t.Errorf("JSONB altered the hashed bytes:\n stored back: %s\n original:   %s", got, canon)
	}
	if h := sha256Hex(got); h != want {
		t.Errorf("lineage read back from JSONB hashes to %s, not %s — replay-from-lineage is broken", h, want)
	}
}

// Re-submitting an identical spec is a no-op, not a conflict: everything is content-derived, so P4
// can fan out over variants without first asking whether it has seen one.
func TestPG_Put_IsIdempotent(t *testing.T) {
	ctx := context.Background()
	seedWorkflow(t, "wf_idem")
	s := NewStore(testDB)

	spec := baseSpec()
	spec.WorkflowID = "wf_idem"
	r := resolveForTest(t, spec, newFakeRegistries())

	if err := s.Put(ctx, r, spec, "v_idem", "baseline"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Put(ctx, r, spec, "v_idem", "baseline"); err != nil {
		t.Fatalf("re-Put of an identical spec must be a no-op, got: %v", err)
	}
	var n int
	if err := testDB.QueryRowContext(ctx,
		`SELECT count(*) FROM variant_spec WHERE config_hash = $1`, r.ConfigHash).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("an identical spec stored %d rows, want 1", n)
	}
}

// The reason the key is (config_hash, source_revision) and not config_hash alone: source_revision is
// not in the hash, so the SAME configuration can legitimately target TWO commits. Both must be
// storable, each keeping its own revision — otherwise the transform, which is keyed by the pair,
// would be attributed to the wrong commit.
func TestPG_Put_SameConfigHashAtTwoRevisionsKeepsBothRevisions(t *testing.T) {
	ctx := context.Background()
	seedWorkflow(t, "wf_rev")
	s := NewStore(testDB)

	specA := baseSpec()
	specA.WorkflowID = "wf_rev"
	specA.SourceRevision = "rev_one"
	rA := resolveForTest(t, specA, newFakeRegistries())

	specB := baseSpec()
	specB.WorkflowID = "wf_rev"
	specB.SourceRevision = "rev_two"
	rB := resolveForTest(t, specB, newFakeRegistries())

	if rA.ConfigHash != rB.ConfigHash {
		t.Fatalf("test bug: source_revision must not change config_hash (%s vs %s)",
			Display(rA.ConfigHash), Display(rB.ConfigHash))
	}
	if err := s.Put(ctx, rA, specA, "v_rev", "baseline"); err != nil {
		t.Fatalf("Put rev_one: %v", err)
	}
	if err := s.Put(ctx, rB, specB, "v_rev", "baseline"); err != nil {
		t.Fatalf("Put rev_two: %v", err)
	}

	for _, rev := range []string{"rev_one", "rev_two"} {
		got, _, err := s.Get(ctx, rA.ConfigHash, rev)
		if err != nil {
			t.Fatalf("Get %s: %v", rev, err)
		}
		if got.SourceRevision != rev {
			t.Errorf("spec at %s reports source_revision %q", rev, got.SourceRevision)
		}
	}
}

func TestPG_Get_UnknownSpecIsErrSpecNotFound(t *testing.T) {
	_, _, err := NewStore(testDB).Get(context.Background(), strings.Repeat("0", 64), "nope")
	if err == nil || !strings.Contains(err.Error(), "no spec stored") {
		t.Fatalf("want ErrSpecNotFound, got %v", err)
	}
}

// A stored spec is as immutable as the config_hash identifying it — an "edit" is a different hash and
// therefore a different row. 0003 reuses 0002's guard, so this also proves the two migrations agree.
func TestPG_VariantSpec_IsImmutable(t *testing.T) {
	ctx := context.Background()
	seedWorkflow(t, "wf_immut")
	s := NewStore(testDB)
	spec := baseSpec()
	spec.WorkflowID = "wf_immut"
	spec.SourceRevision = "rev_immut"
	r := resolveForTest(t, spec, newFakeRegistries())
	if err := s.Put(ctx, r, spec, "v_immut", "baseline"); err != nil {
		t.Fatalf("Put: %v", err)
	}

	for _, tc := range []struct{ name, q string }{
		{"update", `UPDATE variant_spec SET source_revision = 'tampered' WHERE config_hash = $1`},
		{"delete", `DELETE FROM variant_spec WHERE config_hash = $1`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := testDB.ExecContext(ctx, tc.q, r.ConfigHash)
			if err == nil {
				t.Fatalf("a published spec was mutated by %s", tc.name)
			}
			var pqErr *pq.Error
			if !asPQ(err, &pqErr) || string(pqErr.Code) != "HR001" {
				t.Errorf("rejected, but not by the immutability guard (HR001): %v", err)
			}
		})
	}
}

// The FK is what keeps a spec from claiming a config_hash that resolves to nothing.
func TestPG_VariantSpec_CannotReferenceAbsentLineage(t *testing.T) {
	_, err := testDB.Exec(
		`INSERT INTO variant_spec (config_hash, source_revision, spec_json) VALUES ($1, 'r', '{}')`,
		strings.Repeat("f", 64))
	if err == nil {
		t.Fatal("a spec referencing a non-existent config was accepted; its config_hash would resolve to nothing")
	}
	var pqErr *pq.Error
	if !asPQ(err, &pqErr) || string(pqErr.Code) != "23503" {
		t.Errorf("rejected, but not by the FK (23503): %v", err)
	}
}

func TestPG_ZZ_DownMigrationRemovesVariantSpecOnly(t *testing.T) {
	ctx := context.Background()
	down, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres", "0003_p2_variant_spec.down.sql"))
	if err != nil {
		t.Fatalf("read down: %v", err)
	}
	if _, err := testDB.ExecContext(ctx, string(down)); err != nil {
		t.Fatalf("apply down: %v", err)
	}
	var exists bool
	if err := testDB.QueryRowContext(ctx, `SELECT to_regclass('variant_spec') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatalf("check: %v", err)
	}
	if exists {
		t.Error("variant_spec survived the down migration")
	}
	// Expand-only: rolling 0003 back must leave 0001's and 0002's objects intact — including the
	// guard function 0002 owns and 0003 merely borrowed.
	for _, obj := range []string{"config", "model_entry"} {
		var ok bool
		if err := testDB.QueryRowContext(ctx, `SELECT to_regclass($1) IS NOT NULL`, obj).Scan(&ok); err != nil {
			t.Fatalf("check %s: %v", obj, err)
		}
		if !ok {
			t.Errorf("the 0003 rollback removed %s, which it does not own", obj)
		}
	}
	var fnExists bool
	if err := testDB.QueryRowContext(ctx,
		`SELECT count(*) > 0 FROM pg_proc WHERE proname = 'registry_reject_mutation'`).Scan(&fnExists); err != nil {
		t.Fatalf("check function: %v", err)
	}
	if !fnExists {
		t.Error("the 0003 rollback dropped registry_reject_mutation(), which 0002 owns and the registries still use")
	}
}

func asPQ(err error, target **pq.Error) bool {
	for err != nil {
		if e, ok := err.(*pq.Error); ok {
			*target = e
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

var _ = json.Marshal
