//go:build pgproof

// Live-Postgres proof for P30's two migrations (tasks 2.4, 2.5, 2.7).
//
// 🔴 The whole point of the `pgproof` tag is that these run the REAL migration files. Task 2.4 says
// "real migration in tests — 🚫 no inlined CREATE TABLE", and the reason is the bug family this
// repository has already been bitten by three times: a test that inlines its own DDL proves that the
// TEST's schema works. The customer's schema comes from the migration, and the two drift the moment
// somebody edits one.
//
//	make pg-proof
//	HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/pgmigrate/ -run P30
package pgmigrate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// 🔴 Apply, RE-APPLY, down, up again — on a real database.
//
// The re-apply is the half that catches the common defect. `CREATE TABLE IF NOT EXISTS` makes the DDL
// safe on a second run; the DO blocks that verify DEFINITIONS do not get that for free, and a
// definition check written carelessly fails on exactly the run where nothing is wrong. Every
// deployment's SECOND boot runs this path.
func TestP30MigrationsApplyReapplyAndSurviveADownAndUp(t *testing.T) {
	db := openSchema(t, "p30_migrations")
	ctx := context.Background()

	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("the embedded set does not apply: %v", err)
	}
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("a SECOND apply failed — this is every deployment's second boot: %v", err)
	}
	for _, name := range []string{"0045_p30_ir_fact_provenance", "0046_p30_heros_agent"} {
		applyDownThenUp(t, db, name)
	}
	// And once more through the embedded set, which is what a rolled-back-then-rolled-forward
	// deployment actually does.
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply after a down/up cycle failed: %v", err)
	}
}

// 🔴 TASK 2.5 — THE IDEMPOTENCY FENCE, PROVED BY CONTENTION.
//
// Two goroutines insert the SAME (workflow_id, source_revision, agent_config_hash) at the same time.
// Exactly one row must exist afterwards and exactly one insert must be refused by the constraint.
//
// A sequential double-insert would not test this. It exercises the WRITER's own check — read, see a
// row, skip — and never reaches the index at all, so it passes identically against a schema with no
// unique constraint on it. This repository has paid for that lesson once already; the migration's own
// header says so.
func TestP30ConcurrentDoubleSubmitWritesExactlyOneInference(t *testing.T) {
	db := openSchema(t, "p30_contention")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}

	const (
		wf  = "wf-contended"
		rev = "rev-abc123"
		cfg = "cfg-deadbeef"
	)
	insert := func(id string) error {
		_, err := db.ExecContext(ctx,
			`INSERT INTO heros_inference
			   (inference_id, workflow_id, source_revision, agent_config_hash, tenant_id, placement,
			    edges_json, labels_json, tokens_in, tokens_out, created_at_ms)
			 VALUES ($1,$2,$3,$4,$5,'platform','[]','[]',0,0,$6)`,
			id, wf, rev, cfg, "t1", int64(1_700_000_000_000))
		return err
	}

	// A barrier so both statements are genuinely in flight. Without it the scheduler usually serialises
	// them and the test degrades into the sequential one it exists not to be.
	var start sync.WaitGroup
	start.Add(1)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			start.Wait()
			errs[i] = insert(fmt.Sprintf("inf-%d", i))
		}(i)
	}
	start.Done()
	wg.Wait()

	var refused int
	for _, err := range errs {
		if err != nil {
			refused++
			if !strings.Contains(err.Error(), "uq_heros_inference_key") {
				t.Errorf("an insert was refused by something OTHER than the idempotency fence: %v", err)
			}
		}
	}
	if refused != 1 {
		t.Errorf("%d of 2 concurrent inserts were refused, want exactly 1.\n"+
			"  0 refusals means the UNIQUE (workflow_id, source_revision, agent_config_hash) is not there "+
			"or is not enforced — and D2's guarantee that the same revision always shows the same graph "+
			"is then a claim about a model rather than a property of the store.", refused)
	}

	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM heros_inference WHERE workflow_id=$1 AND source_revision=$2 AND agent_config_hash=$3`,
		wf, rev, cfg).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d rows exist for one inference key, want 1", n)
	}
}

// 🔴 At most ONE active definition, under contention for the same reason.
func TestP30OnlyOneAgentVersionCanBeActive(t *testing.T) {
	db := openSchema(t, "p30_active")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	for _, h := range []string{"cfg-a", "cfg-b"} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO heros_agent_version
			   (config_hash, spec_json, model_ref, credential_ref, rehearsal_state, created_at_ms)
			 VALUES ($1,'{}','m','provider-name','passed',$2)`, h, int64(1)); err != nil {
			t.Fatal(err)
		}
	}
	activate := func(h string) error {
		_, err := db.ExecContext(ctx,
			`UPDATE heros_agent_version SET activated_at_ms=$1 WHERE config_hash=$2`, int64(2), h)
		return err
	}
	if err := activate("cfg-a"); err != nil {
		t.Fatalf("activating the first definition failed: %v", err)
	}
	if err := activate("cfg-b"); err == nil {
		t.Error("a SECOND definition activated. `which definition is serving inference` would then " +
			"depend on which transaction committed last, which is the one question this surface must " +
			"always be able to answer.")
	}
}

// 🔴 An UNREHEARSED definition cannot be activated, in the database as well as in the Go path. The two
// fail independently; a future writer that bypasses the activation transaction still cannot arm an
// agent nothing measured.
func TestP30AnUnrehearsedDefinitionCannotBeActivated(t *testing.T) {
	db := openSchema(t, "p30_gate")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO heros_agent_version
		   (config_hash, spec_json, model_ref, credential_ref, rehearsal_state, activated_at_ms, created_at_ms)
		 VALUES ('cfg-pending','{}','m','provider-name','pending',$1,$2)`, int64(2), int64(1))
	if err == nil {
		t.Error("a `pending` definition was activated. D7's gate is that a published definition is " +
			"INACTIVE until it meets the floor on every fixture individually — an agent armed without " +
			"that has been measured by nothing.")
	}
}

// 🔴 An unpriced spend row cannot carry a cost. `unpriced` rendering as `0` is the most reassuring
// possible lie about a bill (task 6.5), and this is the half of that rule the database holds.
func TestP30AnUnpricedSpendRowCannotCarryACost(t *testing.T) {
	db := openSchema(t, "p30_spend")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO heros_inference
		   (inference_id, workflow_id, source_revision, agent_config_hash, tenant_id, placement,
		    edges_json, labels_json, tokens_in, tokens_out, created_at_ms)
		 VALUES ('inf-1','wf','rev','cfg','t1','platform','[]','[]',0,0,1)`); err != nil {
		t.Fatal(err)
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO heros_spend (tenant_id, inference_id, tokens_in, tokens_out, estimated_cost, priced, created_at_ms)
		 VALUES ('t1','inf-1',10,20,4.2,FALSE,1)`)
	if err == nil {
		t.Error("an unpriced row stored a cost. `priced` exists precisely so a model with no published " +
			"price produces a token count and NO number a reader could take for a bill.")
	}
}

// 🔴 TASK 2.7 — a DOWN migration leaves every previously readable IR readable.
//
// The assertion is on the DOCUMENT, not on the column: per-fact authorship lives inside `view_json`, so
// dropping 0045's derived index must cost a query shape and no information. A down that took the facts
// with it would be the one outcome worse than never having stamped them.
func TestP30DownMigrationLeavesEveryStoredIRReadable(t *testing.T) {
	db := openSchema(t, "p30_down")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}

	const view = `{"workflow_id":"wf","edges":[{"from":"a","to":"b","kind":"data","author":"heros","confidence":0.9}],` +
		`"nodes":[],"regions":[],"unclassified":[]}`
	if _, err := db.ExecContext(ctx,
		`INSERT INTO platform_workflow_graph
		   (tenant_id, workflow_id, source_revision, ir_version, taxonomy_version, discovered_at,
		    llm_calls, view_json, provenance)
		 VALUES ('t1','wf','rev','1.3.0','1.0.0', now(), 0, $1::jsonb, 'heros')`, view); err != nil {
		t.Fatal(err)
	}

	// Roll 0045 back.
	b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", "postgres",
		"0045_p30_ir_fact_provenance.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, string(b)); err != nil {
		t.Fatalf("the down migration failed: %v", err)
	}

	var got string
	if err := db.QueryRowContext(ctx,
		`SELECT view_json::text FROM platform_workflow_graph WHERE tenant_id='t1' AND workflow_id='wf'`).
		Scan(&got); err != nil {
		t.Fatalf("a stored IR became UNREADABLE after the down migration: %v", err)
	}
	for _, want := range []string{`"author": "heros"`, `"author":"heros"`} {
		if strings.Contains(got, want) {
			return
		}
	}
	t.Errorf("the down migration took the per-fact authorship with it. It lives in the document, not in "+
		"the dropped column, and losing the record of an authored fact while KEEPING the fact is the one "+
		"outcome worse than either.\n  got: %s", got)
}

// 🔴 TASK 2.6 — no timestamp literal appears in P30's migrations.
//
// The rule is int64 milliseconds everywhere, and the reason it needs a fence rather than a convention
// is that a `TIMESTAMPTZ DEFAULT now()` reads as harmless: it works, it is correct in the session that
// wrote it, and it becomes a SECOND CLOCK the moment a reader in another time zone compares it against
// a value Go computed. Four tests in this repository have already gone red on the calendar alone.
func TestP30MigrationsCarryNoTimestampLiteral(t *testing.T) {
	dir := filepath.Join("..", "..", "db", "migrations", "postgres")
	// `now()`, `current_timestamp`, `TIMESTAMPTZ`, and an ISO-8601-shaped literal.
	banned := regexp.MustCompile(`(?i)\b(now\(\)|current_timestamp|current_date|timestamptz|timestamp\b)|'\d{4}-\d{2}-\d{2}`)
	for _, name := range []string{
		"0045_p30_ir_fact_provenance.up.sql", "0045_p30_ir_fact_provenance.down.sql",
		"0046_p30_heros_agent.up.sql", "0046_p30_heros_agent.down.sql",
	} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		// Comments carry the ARGUMENT for int64 ms and legitimately name the thing being avoided, so
		// they are stripped before matching. A fence that trips on its own rationale is a fence somebody
		// deletes. (See the standing rule that a fence must not trip on prose.)
		var code strings.Builder
		for _, line := range strings.Split(string(b), "\n") {
			if t := strings.TrimSpace(line); strings.HasPrefix(t, "--") {
				continue
			}
			if i := strings.Index(line, "--"); i >= 0 {
				line = line[:i]
			}
			code.WriteString(line + "\n")
		}
		if m := banned.FindString(code.String()); m != "" {
			t.Errorf("%s contains the time expression %q. P30 stores int64 MILLISECONDS: a value the "+
				"database renders through a session time zone is a second clock, and it disagrees with "+
				"the Go value it is compared against on exactly the machines that are not the developer's.",
				name, m)
		}
	}
}

// 🔴 NO STORAGE FIELD CAN CARRY A PROVIDER KEY (task 10.17's storage half).
//
// AUTO-DISCOVERING: it reads the catalog for every P30 table and matches column NAMES against
// key-shaped patterns. A whitelist would pass a column added tomorrow.
//
// The pattern matches name COMPONENTS rather than substrings. The first version matched substrings and
// flagged `tokens_in`/`tokens_out` — token COUNTS — which is how a fence gets switched off. It was
// caught by running the migration against a real database, not by review.
func TestP30NoStorageColumnCanCarryAKey(t *testing.T) {
	db := openSchema(t, "p30_nokey")
	ctx := context.Background()
	if _, err := Apply(ctx, db); err != nil {
		t.Fatalf("apply: %v", err)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT table_name, column_name FROM information_schema.columns
		  WHERE table_name IN ('heros_agent_version','heros_inference','heros_abstention','heros_spend')
		    AND column_name ~ '(^|_)(api_?key|apikey|secret|password|passwd|bearer|token|credential_value)(_|$)'
		    AND column_name <> 'credential_ref'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var found []string
	for rows.Next() {
		var tbl, col string
		if err := rows.Scan(&tbl, &col); err != nil {
			t.Fatal(err)
		}
		found = append(found, tbl+"."+col)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(found) > 0 {
		t.Errorf("P30 storage has column(s) capable of holding a provider key: %s.\n"+
			"  D5 is that the credential is BOUND, never entered — and the mechanism is the ABSENCE of a "+
			"field, not a rule about what to put in one.", strings.Join(found, ", "))
	}

	// The fence must be able to FAIL. Add a key-shaped column and confirm the query finds it, then
	// drop it — otherwise a broken pattern would report clean forever.
	if _, err := db.ExecContext(ctx, `ALTER TABLE heros_spend ADD COLUMN api_key TEXT`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM information_schema.columns
		  WHERE table_name = 'heros_spend' AND column_name ~ '(^|_)(api_?key|apikey|secret|password|passwd|bearer|token|credential_value)(_|$)'`).
		Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("the key-shaped-column pattern does not match `api_key` — the fence above reports clean " +
			"because it cannot see anything, not because there is nothing to see")
	}
	if _, err := db.ExecContext(ctx, `ALTER TABLE heros_spend DROP COLUMN api_key`); err != nil {
		t.Fatal(err)
	}
}
