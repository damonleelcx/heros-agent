//go:build pgproof

// P30 task 10.8 — the unique index, proved against a REAL Postgres under contention.
//
// # 🔴 "A unique index is invisible to a test that never contends"
//
// The task's own words. Every in-memory store in this package is idempotent on the three-part key
// because its `Put` checks a map before writing, and that check passes every sequential test ever
// written against it. It also passes on a database with NO unique index at all — the second writer
// simply looks, sees the first row, and declines. What the index exists for is the interleaving the
// map never produces: two writers that both read "absent" before either writes.
//
// So this runs concurrent writers against a real Postgres with the real migration applied, and asserts
// on the ROW COUNT. It is a build-tagged test because it needs a database; `make pgproof` runs it, and
// `db/migrations/postgres/run_pg_docker.sh` boots an ephemeral one so it is runnable anywhere rather
// than only on a maintainer's machine.
package herosagent

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
)

func agentDB(t *testing.T, schema string) *sql.DB {
	t.Helper()
	db, err := pgtest.Open(schema)
	if err != nil {
		t.Skipf("no test Postgres: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

// 🔴 CONCURRENT DOUBLE-SUBMIT WRITES ONE ROW.
//
// Eight writers, one key, all released together. Without `uq_heros_inference_key` several would land
// and the store would hold a workflow whose revision resolves to more than one graph — which is D2's
// guarantee failing in the one way a sequential test cannot reach.
func TestConcurrentDoubleSubmitWritesExactlyOneRow(t *testing.T) {
	db := agentDB(t, "p30_inference_race")
	store, err := NewPGInferenceStore(db)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	const writers = 8
	// A barrier, so the writers are actually simultaneous. Starting eight goroutines in a loop and
	// hoping they overlap is how a race test passes by finishing in order.
	release := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, writers)

	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-release
			// 🔴 Each writer supplies its OWN inference id, which is what a real race produces: two
			// runners that both computed an answer, each with an id derived from its own attempt. If they
			// shared one id the primary key would do the work and the UNIQUE index would never be tested.
			errs[i] = store.Put(ctx, Stored{
				InferenceID: defaultInferenceID("wf-race", "rev-1", "cfg-race") + "-" + string(rune('a'+i)),
				TenantID:    "t-race", WorkflowID: "wf-race", SourceRevision: "rev-1",
				AgentConfigHash: "cfg-race", Placement: PlacementPlatform,
				Edges:       []ProvenancedEdge{{From: "a", To: "b", Kind: "data", Confidence: 0.9}},
				Labels:      []patternclassifier.RegionProposal{},
				Abstentions: []Abstention{{Subject: "b→c", Reason: AbstainBelowFloor}},
				CreatedAtMS: 1_000 + int64(i),
			})
		}(i)
	}
	close(release)
	wg.Wait()

	// 🔴 NO WRITER FAILS. The conflict path resolves rather than erroring: "two runners that raced
	// produced answers for one key; the row that won is the answer, and failing the loser would turn a
	// benign race into an analysis failure a customer sees."
	for i, err := range errs {
		if err != nil {
			t.Errorf("writer %d failed: %v", i, err)
		}
	}

	var rows int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM heros_inference WHERE workflow_id = $1 AND source_revision = $2
		   AND agent_config_hash = $3`, "wf-race", "rev-1", "cfg-race").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("%d rows for one three-part key. D2's promise that a revision resolves to ONE graph is "+
			"a property of this index, and an in-memory store's map check passes with no index at all.", rows)
	}

	// And the abstentions belong to the row that WON — not to a losing writer's inference id, which
	// would either violate the foreign key or attribute one runner's refusals to another's run.
	var winner string
	if err := db.QueryRowContext(ctx,
		`SELECT inference_id FROM heros_inference WHERE workflow_id = $1`, "wf-race").Scan(&winner); err != nil {
		t.Fatal(err)
	}
	var orphans int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM heros_abstention WHERE inference_id <> $1`, winner).Scan(&orphans); err != nil {
		t.Fatal(err)
	}
	if orphans != 0 {
		t.Errorf("%d abstention(s) are attributed to an inference that does not exist — a losing "+
			"writer's refusals were recorded against its own id", orphans)
	}

	// The read finds it. Layer 3: the store's SELECT column list matches what was written, which is the
	// layer that breaks silently when a column is added to the write and forgotten in the read.
	got, ok, err := store.Get(ctx, "wf-race", "rev-1", "cfg-race")
	if err != nil || !ok {
		t.Fatalf("the surviving row does not read back (ok=%v err=%v)", ok, err)
	}
	if len(got.Edges) != 1 || len(got.Abstentions) != 1 {
		t.Errorf("the row reads back as %d edge(s) and %d abstention(s), want 1 and 1", len(got.Edges), len(got.Abstentions))
	}
}

// The cap and placement tables round-trip through their real schema, including the two CHECKs that
// make a zero ceiling and a reason-less placement unwritable.
func TestTheCapAndPlacementSchemasRefuseWhatTheirStoresRefuse(t *testing.T) {
	db := agentDB(t, "p30_caps_schema")
	ctx := context.Background()

	caps, err := NewPGCapStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := caps.Set(ctx, Cap{TenantID: FleetTenantID, MaxTokens: 500, Reason: "a test", SetBy: "test", UpdatedAtMS: 1}); err != nil {
		t.Fatal(err)
	}
	got, ok, err := caps.Get(ctx, FleetTenantID)
	if err != nil || !ok || got.MaxTokens != 500 {
		t.Fatalf("the fleet cap did not round-trip: %+v ok=%v err=%v", got, ok, err)
	}
	// 🔴 Removing a cap is a DELETE, and the read then reports NO CAP rather than a ceiling of zero.
	if err := caps.Delete(ctx, FleetTenantID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := caps.Get(ctx, FleetTenantID); ok {
		t.Error("a deleted cap still reads as present")
	}
	// The schema refuses a zero even if a caller reached past the store's own check.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO heros_cap (tenant_id, max_tokens, reason, set_by, updated_at_ms)
		 VALUES ('t1', 0, 'r', 'test', 1)`); err == nil {
		t.Error("the schema accepted a ceiling of zero, which is ambiguous between `spend nothing` and " +
			"`no limit` — a checker reading it would have to guess")
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO heros_tenant_placement (tenant_id, placement, reason, set_by, updated_at_ms)
		 VALUES ('t1', 'platform', '   ', 'test', 1)`); err == nil {
		t.Error("the schema accepted a placement with a blank reason — `platform` is what makes this " +
			"platform read a tenant's source under a platform-held credential")
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO heros_tenant_placement (tenant_id, placement, reason, set_by, updated_at_ms)
		 VALUES ('t2', 'somewhere-else', 'r', 'test', 1)`); err == nil {
		t.Error("the schema accepted a fourth placement value, which the gate has no branch for")
	}
}

// 🔴 Task 9.5 against the real schema: disabling MARKS and the rows survive, and the reason/timestamp
// CHECK makes a mark that cannot answer "since when" unwritable.
func TestTheStaleMarkSurvivesTheRealSchema(t *testing.T) {
	db := agentDB(t, "p30_stale_schema")
	ctx := context.Background()
	inferences, err := NewPGInferenceStore(db)
	if err != nil {
		t.Fatal(err)
	}
	meter, err := NewPGSpendStore(db)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"i1", "i2"} {
		if err := inferences.Put(ctx, Stored{
			InferenceID: id, TenantID: "t-stale", WorkflowID: "wf-" + id, SourceRevision: "rev",
			AgentConfigHash: "cfg", Placement: PlacementPlatform,
			Edges:  []ProvenancedEdge{{From: "a", To: "b", Kind: "data", Confidence: 0.9}},
			Labels: []patternclassifier.RegionProposal{}, CreatedAtMS: 10,
		}); err != nil {
			t.Fatal(err)
		}
	}

	n, err := meter.MarkStale(ctx, "t-stale", StaleDisabled, 99)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("marked %d rows, want 2", n)
	}
	var rows int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM heros_inference WHERE tenant_id = 't-stale'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 2 {
		t.Fatalf("%d rows survive after disabling, want 2 — disabling must MARK and not delete", rows)
	}

	// A reason with no timestamp is unwritable, so a stale row can always answer "since when".
	if _, err := db.ExecContext(ctx,
		`UPDATE heros_inference SET stale_reason = 'analysis_disabled', stale_at_ms = NULL
		  WHERE inference_id = 'i1'`); err == nil {
		t.Error("the schema accepted a stale mark with no timestamp")
	}
	if _, err := db.ExecContext(ctx,
		`UPDATE heros_inference SET stale_reason = 'because', stale_at_ms = 1 WHERE inference_id = 'i1'`); err == nil {
		t.Error("the schema accepted a free-text stale reason")
	}
}
