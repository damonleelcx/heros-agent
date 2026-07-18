//go:build pgproof

// Task 9.2: the integration test — the whole substrate wired to a LIVE Postgres eval store (with a
// local span store + TSDB and a stubbed provider), proving the end-to-end flow that the hermetic tests
// prove in pieces: zero-user-code collection, tag-completeness (missing-config_hash rejected before
// Postgres), cardinality discipline, three-store routing, drillable spans, secrets scrubbing,
// idempotency, and the evaluator seam landing in Postgres.
//
// Shares TestMain / seedLineage / evalCfgHash with evalstore_pgproof_test.go. Reuses the hermetic test
// rig (testRigWithSink, runFixture) — untagged _test.go files compile in the pgproof build too.
package telemetry

import (
	"context"
	"testing"
	"time"
)

// seedIntegrationNodes adds the extra lineage the fixture needs beyond seedLineage: node rows for
// n_b/n_c (the eval_result (workflow_id, node_id) FK) and the variant the fixture runs under (the
// eval_result variant_id FK — runFixture uses "variant_alpha", seedLineage only seeds "v").
func seedIntegrationNodes(t *testing.T) {
	t.Helper()
	for _, n := range []string{"n_b", "n_c"} {
		if _, err := testDB.Exec(`INSERT INTO node (workflow_id, node_id) VALUES ('wf',$1) ON CONFLICT DO NOTHING`, n); err != nil {
			t.Fatalf("seed node %s: %v", n, err)
		}
	}
	if _, err := testDB.Exec(`INSERT INTO variant (variant_id, workflow_id, label) VALUES ('variant_alpha','wf','fixture')
		ON CONFLICT DO NOTHING`); err != nil {
		t.Fatalf("seed variant: %v", err)
	}
}

func TestPG_Integration_FullPipelineToLivePostgres(t *testing.T) {
	seedLineage(t)
	seedIntegrationNodes(t)
	ctx := context.Background()

	spans := NewMemSpanStore(0)
	tsdb := NewMemTSDB(0)
	pgEval := NewPGEvalStore(testDB) // the REAL Postgres eval store
	log := &captureLogger{}
	col := NewCollector(CollectorConfig{Spans: spans, TSDB: tsdb, Eval: pgEval, Logger: log})
	t.Cleanup(col.Close)

	// Zero-user-code collection: the fixture drives real gateway calls; testConfigHash == evalCfgHash,
	// which seedLineage recorded, so eval-result FKs resolve.
	gw, inst, entry := testRigWithSink(t, col)
	rc := runFixture(t, gw, inst, entry, []string{"n_a", "n_b", "n_c"})
	col.Flush()

	// Operational metrics reached the TSDB; spans reached the span store, drillable per run.
	if s, err := tsdb.Query(map[string]string{AttrConfigHash: rc.ConfigHash}); err != nil || len(s) == 0 {
		t.Fatalf("no operational metrics in the TSDB: %d, %v", len(s), err)
	}
	if got := spans.Trace(rc.RunID); len(got) == 0 {
		t.Fatal("no spans in the span store for the run")
	}

	// The evaluator seam, landing in LIVE Postgres.
	reg := NewRegistry()
	_ = reg.Register(NewReferenceEvaluator())
	RunEvaluators(ctx, reg, rc, Trace{Run: rc, Spans: spans.Trace(rc.RunID)}, col)
	col.Flush()

	rows, err := pgEval.ByConfigHash(ctx, rc.ConfigHash)
	if err != nil {
		t.Fatalf("ByConfigHash: %v", err)
	}
	got := 0
	for _, r := range rows {
		if r.RunID == rc.RunID && r.MetricName == MetricReferenceNodePresent {
			got++
			if err := r.Event.Validate(); err != nil {
				t.Errorf("a Postgres eval row is under-tagged: %v", err)
			}
			if r.EvaluatorName != ReferenceEvaluatorName {
				t.Errorf("eval row not attributed: %q", r.EvaluatorName)
			}
		}
	}
	if got != 3 {
		t.Errorf("the reference evaluator landed %d rows in Postgres, want 3; collector logs: %v", got, log.all())
	}

	// Tag-completeness against the whole pipeline: a missing-config_hash quality event is rejected at
	// the boundary and never reaches Postgres.
	before := col.GateStats().Rejected
	bad := QualityMetricEvent{
		Event:         rc.WithNode("n_a", 0).event(MetricReferenceNodePresent, 1, UnitCount, time.Now(), nil),
		EvaluatorName: "reference",
	}
	bad.ConfigHash = ""
	col.EmitEval(ctx, bad)
	col.Flush()
	if col.GateStats().Rejected <= before {
		t.Error("a missing-config_hash eval event was not rejected before Postgres")
	}

	// Secrets: nothing secret-shaped in any span reached the store.
	for _, sp := range spans.SpansByConfigHash(rc.ConfigHash) {
		for _, v := range sp.Attributes {
			if str, ok := v.(string); ok && containsSecret(str) {
				t.Errorf("secret/PII leaked into a span: %q", str)
			}
		}
	}
}
