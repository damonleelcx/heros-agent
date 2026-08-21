//go:build pgproof

// P33 §2.3 and §7.10 — the ASSESSMENT store against real Postgres, and the live four-step.
//
// # 🔴 A 200 is not evidence of a write, and neither is a green unit test
//
// §9.3 states the acceptance exactly: *"run → `SELECT` the findings → assert nine axes present →
// assert each carries a resolvable evidence reference"*, and names the failure it exists to catch:
// *"adding a column to the finding record without the baseline and the migration means `INSERT OR
// IGNORE` swallows rows while the endpoint keeps reporting success."*
//
// This file runs the real migration chain, writes through the real store, and reads back with SQL that
// this package's own code did not produce. Every layer below has, in this repository, been the one
// that was broken while the layers above it were green:
//
//  1. the runner returned nine findings — says nothing about storage;
//  2. rows present in Postgres — the INSERT landed with the values sent. This is the layer that breaks
//     when a CHECK refuses and a code path swallows it;
//  3. the store reads them back — the SELECT column list matches what was written. Breaks SILENTLY:
//     the row is there, a field comes back empty, and the console renders an absence for data that was
//     definitely written;
//  4. the CONSTRAINTS refuse what the type refuses — the triplication of the four conditional
//     requirements is only worth its cost if the third copy actually fires.
//
// Run with: make pg-proof (or PGPROOF_URL=… go test -tags pgproof ./internal/assessment).

package assessment

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"log/slog"
)

func liveDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := pgtest.Open("pgtest_assessment")
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	return db
}

// liveSubject is the SAME fixture the unit tests use, run through the SAME discovery. A synthetic IR
// here would test the store against a tree no customer has (task 7.11).
func liveSubject(t *testing.T) Subject {
	t.Helper()
	return subjectFor(t, "python")
}

// TestTheLiveFourStep is §7.10, executed end to end.
func TestTheLiveFourStep(t *testing.T) {
	db := liveDB(t)
	store, err := NewPGStore(db)
	if err != nil {
		t.Fatalf("NewPGStore: %v", err)
	}
	tick := int64(1_700_000_000_000)
	r, err := NewRunner(store, allResolve{}, nil, func() int64 { tick += 7; return tick },
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}

	// ── 1 · run ──────────────────────────────────────────────────────────────────────────────────
	a, err := r.Run(context.Background(), Config{
		AssessmentID: "as-live-1", TenantID: "tn-live", SourceRevision: "rev-live",
		AgentConfigHash: "cfg-live", SpendCapUSD: 1.00,
	}, liveSubject(t))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// ── 2 · SELECT the findings, with SQL this package did not write ─────────────────────────────
	rows, err := db.Query(`SELECT axis, state, origin, missing_input, refusal_cause, evidence_surface,
	                              evidence_locator, claim
	                         FROM assessment_finding WHERE assessment_id = $1 ORDER BY axis`, a.AssessmentID)
	if err != nil {
		t.Fatalf("SELECT: %v", err)
	}
	defer func() { _ = rows.Close() }()

	seen := map[string]bool{}
	for rows.Next() {
		var axis, state, origin, surface, locator, claim string
		var missing, refusal sql.NullString
		if err := rows.Scan(&axis, &state, &origin, &missing, &refusal, &surface, &locator, &claim); err != nil {
			t.Fatalf("scan: %v", err)
		}
		seen[axis] = true

		// ── 4 · every evidence reference resolves into an existing surface ───────────────────────
		if surface == "" || locator == "" {
			t.Fatalf("%s stored an evidence reference with an empty %s", axis,
				map[bool]string{true: "surface", false: "locator"}[surface == ""])
		}
		if !Surface(surface).Valid() {
			t.Fatalf("%s stored evidence surface %q, which is not one this build serves", axis, surface)
		}
		// The claim is what a reader reads, and a blank one is a row that renders as an empty answer.
		if strings.TrimSpace(claim) == "" {
			t.Fatalf("%s stored a blank claim", axis)
		}
		if state == string(StateNotMeasured) && !missing.Valid {
			t.Fatalf("%s is not_measured in the DATABASE and names no missing input — the whole product "+
				"of this phase is that absence names what is missing", axis)
		}
		if state == string(StateRefused) && !refusal.Valid {
			t.Fatalf("%s is refused in the database and names none of the three causes", axis)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}

	// ── 3 · nine axes present ────────────────────────────────────────────────────────────────────
	if len(seen) != len(Axes()) {
		t.Fatalf("the database holds %d findings for %s, want %d. This is the layer that breaks when a "+
			"column mismatch swallows rows while the endpoint keeps returning success",
			len(seen), a.AssessmentID, len(Axes()))
	}
	for _, axis := range Axes() {
		if !seen[string(axis)] {
			t.Fatalf("%s is absent from the database", axis)
		}
	}

	// ── And back through the store, which is the layer that breaks silently ──────────────────────
	back, ok, err := store.Get(context.Background(), "tn-live", a.AssessmentID)
	if err != nil || !ok {
		t.Fatalf("reading back: ok=%v err=%v", ok, err)
	}
	if err := back.Validate(); err != nil {
		t.Fatalf("the report read back does not validate: %v", err)
	}
	for i, f := range back.Ordered() {
		orig := a.Ordered()[i]
		if f != orig {
			t.Fatalf("finding %d changed across the round trip:\n  stored %+v\n  read   %+v", i, orig, f)
		}
	}
}

// TestTheDatabaseRefusesWhatTheTypeRefuses is the whole justification for triplicating the four
// conditional requirements. The Go type guards Go callers; the schema guards a payload; THIS guards a
// row written by a migration, a fixture, or a service in another language.
//
// 🔴 Each case asserts the CONSTRAINT NAME in the error. A test that accepted any rejection would pass
// when the insert fails for an unrelated reason — a NOT NULL, a foreign key, a typo — and would then
// keep passing after the constraint it was written for had been dropped.
func TestTheDatabaseRefusesWhatTheTypeRefuses(t *testing.T) {
	db := liveDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO assessment (assessment_id, tenant_id, workflow_id, source_revision,
		   agent_config_hash, started_at_ms, completed_at_ms, spend_usd, spend_cap_usd)
		 VALUES ('as-c', 'tn-c', 'wf-c', 'rev-c', 'cfg-c', 1, 2, 0, 1)`); err != nil {
		t.Fatalf("seeding the run row: %v", err)
	}

	for _, tc := range []struct{ name, sql, constraint string }{
		{
			"not_measured with no missing input",
			`INSERT INTO assessment_finding (assessment_id, axis, state, origin, claim, evidence_surface, evidence_locator)
			 VALUES ('as-c', 'model', 'not_measured', 'structural', 'a claim', 'graph', 'wf-c')`,
			"assessment_finding_not_measured_names_its_gap",
		},
		{
			"refused with a missing input",
			`INSERT INTO assessment_finding (assessment_id, axis, state, origin, claim, evidence_surface, evidence_locator, refusal_cause, missing_input)
			 VALUES ('as-c', 'graph', 'refused', 'structural', 'a claim', 'graph', 'wf-c', 'frontend', 'frontend_emits_no_edges')`,
			"assessment_finding_not_measured_names_its_gap",
		},
		{
			"inferred with only half its attribution",
			`INSERT INTO assessment_finding (assessment_id, axis, state, origin, claim, evidence_surface, evidence_locator, inference_address)
			 VALUES ('as-c', 'memory', 'observed', 'inferred', 'a claim', 'graph', 'wf-c', 'sha256:1')`,
			"assessment_finding_inferred_is_attributed",
		},
		{
			"measured with no decisiveness",
			`INSERT INTO assessment_finding (assessment_id, axis, state, origin, claim, evidence_surface, evidence_locator)
			 VALUES ('as-c', 'prompt', 'measured', 'structural', 'a claim', 'graph', 'wf-c')`,
			"assessment_finding_measured_carries_decisiveness",
		},
		{
			"a measurement a model produced",
			`INSERT INTO assessment_finding (assessment_id, axis, state, origin, claim, evidence_surface, evidence_locator, eval_set_json, provider_model_version, inference_address)
			 VALUES ('as-c', 'prompt', 'measured', 'inferred', 'a claim', 'graph', 'wf-c', '{}', 'm', 'a')`,
			"assessment_finding_no_inferred_measurement",
		},
		{
			"a blank claim",
			`INSERT INTO assessment_finding (assessment_id, axis, state, origin, claim, evidence_surface, evidence_locator)
			 VALUES ('as-c', 'tools', 'observed', 'structural', '   ', 'graph', 'wf-c')`,
			"assessment_finding_claim_is_not_blank",
		},
		{
			"a missing input outside the closed set",
			`INSERT INTO assessment_finding (assessment_id, axis, state, origin, claim, evidence_surface, evidence_locator, missing_input)
			 VALUES ('as-c', 'tools', 'not_measured', 'structural', 'a claim', 'graph', 'wf-c', 'the sandbox was sad')`,
			"assessment_finding_missing_input_known",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.ExecContext(ctx, tc.sql)
			if err == nil {
				t.Fatalf("the database ACCEPTED it. The type refuses this and the schema refuses this; " +
					"a row written by anything that is not a Go caller would reach a reader.")
			}
			if !strings.Contains(err.Error(), tc.constraint) {
				t.Fatalf("rejected for the wrong reason — want %s, got: %v", tc.constraint, err)
			}
		})
	}
}

// TestTheAxisIsHalfThePrimaryKey is FR1 enforced by the database: nine, one per axis, none duplicated.
func TestTheAxisIsHalfThePrimaryKey(t *testing.T) {
	db := liveDB(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO assessment (assessment_id, tenant_id, workflow_id, source_revision,
		   agent_config_hash, started_at_ms, completed_at_ms, spend_usd, spend_cap_usd)
		 VALUES ('as-d', 'tn-d', 'wf-d', 'rev-d', 'cfg-d', 1, 2, 0, 1)`); err != nil {
		t.Fatalf("seeding: %v", err)
	}
	one := `INSERT INTO assessment_finding (assessment_id, axis, state, origin, claim, evidence_surface, evidence_locator)
	        VALUES ('as-d', 'model', 'observed', 'structural', 'a claim', 'graph', 'wf-d')`
	if _, err := db.ExecContext(ctx, one); err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if _, err := db.ExecContext(ctx, one); err == nil {
		t.Fatal("a second finding for the same axis was accepted, so a report can hold nine findings " +
			"covering eight axes — which passes a count and fails a reader")
	}
}

// liveSource is a SourceReader over a fixed fixture, so the "unchanged revision" of task 7.5 is an
// input rather than an assumption about a clock.
type liveSource struct {
	t        *testing.T
	revision string
	reads    int
}

func (l *liveSource) Analyse(context.Context, string, string) (string, *discovery.IR, discovery.DiscoveryReport, error) {
	l.reads++
	s := subjectFor(l.t, "python")
	return l.revision, s.IR, s.Report, nil
}

// countingLiveInference counts provider calls so "no provider call on the second run" is asserted
// rather than inferred from timing.
type countingLiveInference struct{ calls int }

func (c *countingLiveInference) Infer(_ context.Context, axis Axis, s Subject) (Finding, float64, error) {
	c.calls++
	f, err := Inferred(axis, "inferred a strategy for "+string(axis), s.Evidence(),
		"test/live-1", "sha256:"+string(axis))
	return f, 0, err
}

// TestTheRealServicePinsAgainstPostgres is task 7.5 and acceptance A7 through the SHIPPED path.
//
// 🔴 `service_test.go` proves the RULE through a stand-in store and says so. This proves the SERVICE:
// the same `*Service`, the same `*PGStore`, the same `FindPin` query, against a real database. The two
// are not redundant — the unit test would keep passing if `FindPin`'s SQL selected the wrong columns,
// and this one would keep passing if the rule were implemented twice with one copy wrong.
func TestTheRealServicePinsAgainstPostgres(t *testing.T) {
	db := liveDB(t)
	store, err := NewPGStore(db)
	if err != nil {
		t.Fatalf("NewPGStore: %v", err)
	}
	tick := int64(1_700_000_000_000)
	inf := &countingLiveInference{}
	r, err := NewRunner(store, allResolve{}, inf, func() int64 { tick += 7; return tick },
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	ids := 0
	svc, err := NewService(ServiceConfig{
		Runner: r, Store: store, Source: &liveSource{t: t, revision: "rev-pin"},
		NewID:       func() string { ids++; return fmt.Sprintf("as-pin-%d", ids) },
		ConfigHash:  func(context.Context) (string, error) { return "cfg-pin", nil },
		SpendCapUSD: 1.00,
		Log:         slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	first, err := svc.Run(context.Background(), "tn-pin", "wf-pin")
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	callsAfterFirst := inf.calls
	if callsAfterFirst == 0 {
		t.Fatal("the FIRST run made no provider call, so the second making none proves nothing")
	}

	second, err := svc.Run(context.Background(), "tn-pin", "wf-pin")
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	if inf.calls != callsAfterFirst {
		t.Fatalf("the second run made %d further provider call(s) — the pin did not answer",
			inf.calls-callsAfterFirst)
	}
	if second.AssessmentID != first.AssessmentID {
		t.Fatalf("the second run produced a NEW assessment (%s vs %s), so the pin missed and a second "+
			"row was written", second.AssessmentID, first.AssessmentID)
	}
	a, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("the two reports are not byte-identical:\n first: %s\nsecond: %s", a, b)
	}

	// And exactly one row exists — the layer that would break if `Put` were not idempotent on the run.
	var rows int
	if err := db.QueryRow(`SELECT COUNT(*) FROM assessment WHERE tenant_id = 'tn-pin'`).Scan(&rows); err != nil {
		t.Fatalf("counting: %v", err)
	}
	if rows != 1 {
		t.Fatalf("the database holds %d assessments for tn-pin, want 1", rows)
	}
}

// TestSpendIsExportedPerTenant is task 6.4 against the real table: the attribution has to survive the
// GROUP BY, not just the in-memory counter.
func TestSpendIsExportedPerTenant(t *testing.T) {
	db := liveDB(t)
	ctx := context.Background()
	for _, row := range []struct {
		id, tenant string
		spend      float64
	}{
		{"as-s1", "tn-a", 0.42},
		{"as-s2", "tn-a", 0.08},
		{"as-s3", "tn-b", 0.11},
	} {
		if _, err := db.ExecContext(ctx,
			`INSERT INTO assessment (assessment_id, tenant_id, workflow_id, source_revision,
			   agent_config_hash, started_at_ms, completed_at_ms, spend_usd, spend_cap_usd)
			 VALUES ($1, $2, 'wf-s', 'rev-s', 'cfg-s', 10, 20, $3, 1)`,
			row.id, row.tenant, row.spend); err != nil {
			t.Fatalf("seeding %s: %v", row.id, err)
		}
	}
	store, err := NewPGStore(db)
	if err != nil {
		t.Fatalf("NewPGStore: %v", err)
	}
	got, err := store.ExportSpend(ctx, 0)
	if err != nil {
		t.Fatalf("ExportSpend: %v", err)
	}
	byTenant := map[string]SpendExport{}
	for _, e := range got {
		byTenant[e.TenantID] = e
	}
	if a := byTenant["tn-a"]; a.SpendUSD < 0.499 || a.SpendUSD > 0.501 || a.Assessments != 2 {
		t.Fatalf("tn-a exported as $%.4f over %d assessments, want $0.50 over 2", a.SpendUSD, a.Assessments)
	}
	if b := byTenant["tn-b"]; b.SpendUSD < 0.109 || b.SpendUSD > 0.111 || b.Assessments != 1 {
		t.Fatalf("tn-b exported as $%.4f over %d assessments, want $0.11 over 1", b.SpendUSD, b.Assessments)
	}
}

// TestTheHealthBreakdownIsPerAxisAndPerState is DevOps task 6.1's query, proved against the real
// index rather than asserted about.
func TestTheHealthBreakdownIsPerAxisAndPerState(t *testing.T) {
	db := liveDB(t)
	store, err := NewPGStore(db)
	if err != nil {
		t.Fatalf("NewPGStore: %v", err)
	}
	tick := int64(1_700_000_000_000)
	r, err := NewRunner(store, allResolve{}, nil, func() int64 { tick += 7; return tick },
		slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("NewRunner: %v", err)
	}
	// An assessment of a repository with NO call sites: every axis but `loop` comes back
	// `not_measured`. This is DevOps task 6.2's alarm condition, produced rather than simulated.
	empty := Subject{WorkflowID: "wf-empty", IR: &discovery.IR{}}
	if _, err := r.Run(context.Background(), Config{
		AssessmentID: "as-h1", TenantID: "tn-h", SourceRevision: "rev-h",
		AgentConfigHash: "cfg-h", SpendCapUSD: 1.00,
	}, empty); err != nil {
		t.Fatalf("Run: %v", err)
	}

	cells, err := store.AxisStateBreakdown(context.Background(), 0)
	if err != nil {
		t.Fatalf("AxisStateBreakdown: %v", err)
	}
	byAxis := map[Axis]bool{}
	for _, c := range cells {
		byAxis[c.Axis] = true
		if c.Count == 0 {
			t.Fatalf("%s/%s reports a zero count, which a GROUP BY cannot produce — the query is wrong",
				c.Axis, c.State)
		}
	}
	if len(byAxis) != len(Axes()) {
		t.Fatalf("the breakdown covers %d axes, want %d — an aggregate that omits an axis hides exactly "+
			"the axis that is broken", len(byAxis), len(Axes()))
	}

	total, allNM, err := store.AllNotMeasuredRate(context.Background(), 0)
	if err != nil {
		t.Fatalf("AllNotMeasuredRate: %v", err)
	}
	if total != 1 {
		t.Fatalf("counted %d assessments, want 1", total)
	}
	// `loop` is refused, not not_measured, so this assessment is NOT nine-not-measured. That is the
	// correct answer and it is the one worth asserting: the alarm must not fire on a report that
	// contains a legitimate refusal.
	if allNM != 0 {
		t.Fatalf("an assessment with eight not_measured findings and one refusal counted as "+
			"all-not-measured (%d) — the alarm would fire on every assessment until P34 lands", allNM)
	}
}
