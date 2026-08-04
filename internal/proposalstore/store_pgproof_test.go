//go:build pgproof

// Live-Postgres proof for the proposal + verdict store.
//
// The guarantees here are constraints and a JOIN, and a Go map has neither. Two in particular cannot be
// checked against a fake:
//
//   - 0012 constrains the blob-hash columns to `NULL OR 64-hex`, so an inline payload where a content
//     hash belongs is refused by the database. A map would accept it.
//
//   - PutVerdict's ownership check is a WHERE clause on a real row. Its whole job is to stop an
//     authenticated tenant reporting a PASSING verdict on somebody else's proposal, which would promote
//     an unmeasured change into their recommendations. Asserting that against a fake asserts nothing.
//
//     make pg-proof
//     HEROS_TEST_POSTGRES_URL=… go test -tags pgproof ./internal/proposalstore/
package proposalstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/heros-foreal/agentd/internal/evalstats"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
	"github.com/heros-foreal/agentd/internal/verification"
)

func store(t *testing.T, schema string) (*PGStore, *sql.DB) {
	t.Helper()
	db, err := pgtest.Open(schema)
	if err != nil {
		t.Fatalf("live Postgres required: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
	s, err := NewPGStore(db)
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	return s, db
}

const hash64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func rec(tenant, workflow, id string) Record {
	return Record{
		ProposalID: id, TenantID: tenant, WorkflowID: workflow,
		DiagnosisID: "diag-1", Operator: "model_swap", BaseVariantID: "v-base",
		NodeID: "n_router", Pattern: "Routing", Rationale: "cost bottleneck → cheaper model",
		CandidateConfigHash: hash64, SourceRevision: "abc123",
		Evidence: []Evidence{
			{CaseID: "case-1", Role: "generating"},
			{CaseID: "case-2", Role: "held_out"},
		},
	}
}

func TestPutAndReadBackWithEvidence(t *testing.T) {
	s, _ := store(t, "proposalstore_put")
	ctx := context.Background()

	if err := s.Put(ctx, rec("t1", "wf1", "p1")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.ForWorkflow(ctx, "t1", "wf1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d proposals, want 1", len(got))
	}
	if got[0].Status != StatusCandidate {
		t.Errorf("status = %q, want candidate — a fresh proposal is unmeasured", got[0].Status)
	}
	if len(got[0].Evidence) != 2 {
		t.Fatalf("evidence = %+v, want 2 cases", got[0].Evidence)
	}
	// 🔴 An unverified proposal must read as NIL verdict, not a zero one: a zero GateResult would look
	// like a specific failure, and "not yet measured" is a different state from "measured and rejected".
	if got[0].Verdict != nil {
		t.Errorf("a proposal with no verdict came back with one: %+v", got[0].Verdict)
	}
}

// TestAnUnverifiedProposalIsStillReturned. The LEFT JOIN matters: on a platform that proposes and lets
// CI verify, most proposals are unverified at any moment, and an inner join would silently show none.
func TestAnUnverifiedProposalIsStillReturned(t *testing.T) {
	s, _ := store(t, "proposalstore_unverified")
	ctx := context.Background()
	for _, id := range []string{"p1", "p2"} {
		if err := s.Put(ctx, rec("t1", "wf1", id)); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	got, err := s.ForWorkflow(ctx, "t1", "wf1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d, want both unverified proposals", len(got))
	}
}

func TestVerdictPromotesTheProposal(t *testing.T) {
	s, _ := store(t, "proposalstore_verdict")
	ctx := context.Background()
	if err := s.Put(ctx, rec("t1", "wf1", "p1")); err != nil {
		t.Fatalf("put: %v", err)
	}

	v := verification.Verdict{
		ProposalID: "p1", Metric: "quality",
		Delta:       evalstats.Interval{Mean: 0.08, Low: 0.02, High: 0.14},
		Significant: true, HeldOut: true, RegressionPass: true,
		GateResult: verification.GatePass,
	}
	// SetCases, not `CasesFixed:` in the literal. Assigning the list alone leaves the count at zero, and
	// 0029's constraint refuses the row — which is the constraint doing its job on a fixture that would
	// otherwise have stored a change reported as fixing nothing.
	v.SetCases([]string{"case-1"}, nil)
	if err := s.PutVerdict(ctx, "t1", v); err != nil {
		t.Fatalf("put verdict: %v", err)
	}

	got, err := s.ForWorkflow(ctx, "t1", "wf1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got[0].Verdict == nil {
		t.Fatal("the verdict was not read back")
	}
	if got[0].Verdict.Delta.Low != 0.02 || got[0].Verdict.Delta.High != 0.14 {
		t.Errorf("interval = [%v, %v], want [0.02, 0.14]", got[0].Verdict.Delta.Low, got[0].Verdict.Delta.High)
	}
	// The status follows the verdict in the same transaction: a verdict beside a proposal still reading
	// `candidate` would leave the surface classifying it by a stale field.
	if got[0].Status != StatusVerified {
		t.Errorf("status = %q, want verified after a passing verdict", got[0].Status)
	}
}

func TestAFailingVerdictDoesNotPromote(t *testing.T) {
	s, _ := store(t, "proposalstore_failverdict")
	ctx := context.Background()
	if err := s.Put(ctx, rec("t1", "wf1", "p1")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := s.PutVerdict(ctx, "t1", verification.Verdict{
		ProposalID: "p1", Metric: "quality", GateResult: verification.GateFailSig,
	}); err != nil {
		t.Fatalf("put verdict: %v", err)
	}
	got, _ := s.ForWorkflow(ctx, "t1", "wf1")
	if got[0].Status != StatusGateFailed {
		t.Errorf("status = %q, want gate_failed", got[0].Status)
	}
}

// TestAVerdictCannotBeReportedForAnotherTenantsProposal is the one that decides whether this ingest is
// safe to expose. The caller is authenticated; the proposal id in the body is not.
func TestAVerdictCannotBeReportedForAnotherTenantsProposal(t *testing.T) {
	s, db := store(t, "proposalstore_crosstenant")
	ctx := context.Background()
	if err := s.Put(ctx, rec("tenant-a", "wf1", "p1")); err != nil {
		t.Fatalf("put: %v", err)
	}

	err := s.PutVerdict(ctx, "tenant-b", verification.Verdict{
		ProposalID: "p1", Metric: "quality", GateResult: verification.GatePass,
	})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound — tenant-b reported a PASSING verdict on tenant-a's "+
			"proposal, which would promote an unmeasured change into their recommendations", err)
	}

	// And nothing was written: no verdict row, and the proposal is untouched.
	var verdicts int
	if err := db.QueryRow(`SELECT count(*) FROM verdict WHERE proposal_id = 'p1'`).Scan(&verdicts); err != nil {
		t.Fatalf("count verdicts: %v", err)
	}
	if verdicts != 0 {
		t.Fatalf("%d verdict row(s) written by the wrong tenant", verdicts)
	}
	got, _ := s.ForWorkflow(ctx, "tenant-a", "wf1")
	if got[0].Status != StatusCandidate {
		t.Fatalf("tenant-a's proposal moved to %q on tenant-b's report", got[0].Status)
	}
}

func TestProposalsAreScopedToTheirTenant(t *testing.T) {
	s, _ := store(t, "proposalstore_scope")
	ctx := context.Background()
	if err := s.Put(ctx, rec("tenant-a", "wf1", "p1")); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.ForWorkflow(ctx, "tenant-b", "wf1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("tenant-b sees %d of tenant-a's proposals", len(got))
	}
}

// TestAnUnscopedProposalIsRefused: migration 0025's CHECK would catch it, but a sentence beats a
// constraint-violation string, and a row with no tenant is a proposal nobody owns.
func TestAnUnscopedProposalIsRefused(t *testing.T) {
	s, _ := store(t, "proposalstore_unscoped")
	ctx := context.Background()
	r := rec("", "wf1", "p1")
	if err := s.Put(ctx, r); !errors.Is(err, ErrUnscoped) {
		t.Fatalf("err = %v, want ErrUnscoped", err)
	}
}

// TestAnInlinePayloadWhereAHashBelongsIsRefused. 0012 constrains the blob-hash columns to
// `NULL OR ^[0-9a-f]{64}$`, so the database is what stops a diff being pasted into the row.
func TestAnInlinePayloadWhereAHashBelongsIsRefused(t *testing.T) {
	s, _ := store(t, "proposalstore_inline")
	ctx := context.Background()
	r := rec("t1", "wf1", "p1")
	r.SourceDiffBlobHash = "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new"
	err := s.Put(ctx, r)
	if err == nil {
		t.Fatal("a diff was stored in a column that may only hold a content hash")
	}
	if !strings.Contains(err.Error(), "put p1") {
		t.Errorf("err = %v, want it to name the proposal", err)
	}
}

// A verdict REPORTED by a customer's CI: counts, no case ids, no reason.
//
// 🔴 This is the shape every hosted deployment stores, and it is the one a fake would get wrong. The
// counts live in their own columns (migration 0029) precisely because `cases_fixed_json` is `[]` here —
// a store that derived the count from the array would record a change that fixed four cases as having
// fixed none, in the console and in the body of the pull request P12 opens.
func TestAReportedVerdictKeepsItsCountsWithoutIds(t *testing.T) {
	s, _ := store(t, "proposalstore_reported")
	ctx := t.Context()

	if err := s.Put(ctx, rec("tenant-r", "wf-1", "prop-r")); err != nil {
		t.Fatalf("put: %v", err)
	}

	reported := verification.Verdict{
		ProposalID: "prop-r", Metric: "quality",
		Delta:       evalstats.Interval{Mean: 0.06, Low: 0.02, High: 0.10},
		Significant: true, HeldOut: true, RegressionPass: true,
		GateResult: verification.GatePass,
		// What the ingest builds: counts, and deliberately nothing else about the cases.
		CasesFixedCount: 4, CasesBrokenCount: 0,
	}
	if err := s.PutVerdict(ctx, "tenant-r", reported); err != nil {
		t.Fatalf("put verdict: %v", err)
	}

	got, err := s.ForWorkflow(ctx, "tenant-r", "wf-1")
	if err != nil || len(got) != 1 || got[0].Verdict == nil {
		t.Fatalf("read back: %+v %v", got, err)
	}
	v := *got[0].Verdict
	if v.CasesFixedCount != 4 {
		t.Errorf("cases_fixed_count = %d, want 4 — a reported verdict's only record of how many is the "+
			"count column, and this is the number the console and the PR body print", v.CasesFixedCount)
	}
	if v.CasesBrokenCount != 0 || len(v.CasesFixed) != 0 {
		t.Errorf("unexpected case detail survived: %+v", v)
	}
	if !v.Passed() {
		t.Errorf("gate result did not survive: %q", v.GateResult)
	}
}

// The constraint 0029 adds: a verdict may carry fewer ids than it counts (that IS the reported shape),
// but never more — a count that understates its own evidence is corrupt, not conservative.
func TestAVerdictMayNotListMoreIdsThanItCounts(t *testing.T) {
	s, db := store(t, "proposalstore_countfence")
	ctx := t.Context()

	if err := s.Put(ctx, rec("tenant-c", "wf-1", "prop-c")); err != nil {
		t.Fatalf("put: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO verdict (proposal_id, metric, delta, ci_low, ci_high, significant, held_out,
		    cost_delta, latency_delta, regression_pass, cases_fixed_json, cases_broken_json, gate_result,
		    cases_fixed_count, cases_broken_count)
		 VALUES ('prop-c','quality',0.05,0.01,0.09,true,true,0,0,true,
		         '["c1","c2","c3"]'::jsonb, '[]'::jsonb, 'pass', 1, 0)`); err == nil {
		t.Error("the table accepted three case ids under a count of one — the count is what every " +
			"reader prints, so a row like this understates the change it describes")
	}

	// ...and the reported shape (counts, no ids) is explicitly allowed.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO verdict (proposal_id, metric, delta, ci_low, ci_high, significant, held_out,
		    cost_delta, latency_delta, regression_pass, cases_fixed_json, cases_broken_json, gate_result,
		    cases_fixed_count, cases_broken_count)
		 VALUES ('prop-c','quality',0.05,0.01,0.09,true,true,0,0,true,
		         '[]'::jsonb, '[]'::jsonb, 'pass', 4, 0)`); err != nil {
		t.Errorf("the table refused a REPORTED verdict (counts, no ids), which is the shape every "+
			"hosted deployment stores: %v", err)
	}
}

// The two P12 reads, against real rows.
//
// VerdictFor is what Deliverer.Prepare consults for EVERY proposal it considers, and it is the reason a
// proposal carrying a forged or stale verdict cannot open a pull request: the gate is re-read from the
// authority. Both properties below need real SQL — a fake would agree with whatever it was told.
func TestTheGateReadIsScopedAndKeyedByTheChange(t *testing.T) {
	s, _ := store(t, "proposalstore_gateread")
	ctx := t.Context()

	mine := rec("tenant-g1", "wf-1", "prop-mine")
	if err := s.Put(ctx, mine); err != nil {
		t.Fatalf("put: %v", err)
	}
	theirs := rec("tenant-g2", "wf-1", "prop-theirs")
	if err := s.Put(ctx, theirs); err != nil {
		t.Fatalf("put: %v", err)
	}
	v := verification.Verdict{
		ProposalID: "prop-theirs", Metric: "quality",
		Delta:       evalstats.Interval{Mean: 0.09, Low: 0.03, High: 0.15},
		Significant: true, HeldOut: true, RegressionPass: true,
		GateResult: verification.GatePass, CasesFixedCount: 2,
	}
	if err := s.PutVerdict(ctx, "tenant-g2", v); err != nil {
		t.Fatalf("put verdict: %v", err)
	}

	// tenant-g1 shares the config hash and revision (rec() uses the same fixture) but has no verdict.
	got, ok, err := s.VerdictFor(ctx, "tenant-g1", mine.CandidateConfigHash, mine.SourceRevision)
	if err != nil {
		t.Fatalf("verdict for: %v", err)
	}
	if ok || got.Passed() {
		t.Fatalf("tenant-g1 read tenant-g2's PASSING verdict for the same change (%+v) — the gate would "+
			"open a pull request into their repository on somebody else's measurement", got)
	}

	// ...and the owner does see it.
	got, ok, err = s.VerdictFor(ctx, "tenant-g2", theirs.CandidateConfigHash, theirs.SourceRevision)
	if err != nil || !ok || !got.Passed() {
		t.Fatalf("the owning tenant could not read its own verdict: %+v ok=%v err=%v", got, ok, err)
	}

	// A change nobody measured is ok=false, not a zero verdict — "not measured" and "measured and
	// rejected" are different sentences downstream.
	if _, ok, err := s.VerdictFor(ctx, "tenant-g2", hash64, "some-other-rev"); err != nil || ok {
		t.Errorf("an unmeasured change reported ok=%v (err %v)", ok, err)
	}
}

// PendingVerified reads `gate_result = 'pass'` from the VERDICT, not `status` on the proposal. The two
// are written in one transaction and cannot disagree today; only one of them is the measurement.
func TestPendingVerifiedReadsTheVerdictNotTheProjection(t *testing.T) {
	s, db := store(t, "proposalstore_pendingverified")
	ctx := t.Context()

	for _, id := range []string{"p-pass", "p-fail", "p-unmeasured"} {
		if err := s.Put(ctx, rec("tenant-p", "wf-1", id)); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	pass := verification.Verdict{
		ProposalID: "p-pass", Metric: "quality",
		Delta:       evalstats.Interval{Mean: 0.07, Low: 0.02, High: 0.12},
		Significant: true, HeldOut: true, RegressionPass: true,
		GateResult: verification.GatePass, CasesFixedCount: 3,
	}
	if err := s.PutVerdict(ctx, "tenant-p", pass); err != nil {
		t.Fatalf("put verdict: %v", err)
	}
	fail := pass
	fail.ProposalID, fail.GateResult, fail.Significant = "p-fail", verification.GateFailSig, false
	if err := s.PutVerdict(ctx, "tenant-p", fail); err != nil {
		t.Fatalf("put verdict: %v", err)
	}

	got, err := s.PendingVerified(ctx, "tenant-p")
	if err != nil {
		t.Fatalf("pending verified: %v", err)
	}
	if len(got) != 1 || got[0].ProposalID != "p-pass" {
		t.Fatalf("pending = %+v, want only p-pass", ids(got))
	}

	// 🔴 Now make the PROJECTION lie: set the failing proposal's status to `verified` behind the store's
	// back, exactly as a stale or hand-patched row would be. The read must not follow it.
	if _, err := db.ExecContext(ctx,
		`UPDATE proposal SET status = 'verified' WHERE proposal_id = 'p-fail'`); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = s.PendingVerified(ctx, "tenant-p")
	if err != nil {
		t.Fatalf("pending verified: %v", err)
	}
	if len(got) != 1 || got[0].ProposalID != "p-pass" {
		t.Errorf("pending = %+v — a proposal whose STATUS says verified and whose VERDICT says otherwise "+
			"became deliverable; delivery must read the measurement", ids(got))
	}

	// And the presentation columns survive the read, since the delivery title is built from them.
	if got[0].NodeID == "" {
		t.Error("the node id did not survive PendingVerified; the pull request title is built from it")
	}
}

func ids(in []Scored) []string {
	out := make([]string, 0, len(in))
	for _, s := range in {
		out = append(out, s.ProposalID)
	}
	return out
}

// The three states `build_status` alone cannot tell apart, against real constraints.
//
// 0012's CHECK admits unbuilt | built | build_failed, and the transform's REFUSAL is a fourth thing.
// Recording it as `unbuilt` makes a declined change indistinguishable from one nobody has compiled —
// so 0032's reason column is the marker, and the constraints below are what keep the pair coherent.
func TestARefusalIsDistinguishableFromAnUncompiledProposal(t *testing.T) {
	s, db := store(t, "proposalstore_refusal")
	ctx := t.Context()

	uncompiled := rec("tenant-r", "wf-1", "p-uncompiled")
	refused := rec("tenant-r", "wf-1", "p-refused")
	refused.RefusalReason = "the tool selection names a tool this node does not offer"
	refused.RefusalDimension = "tools"
	for _, r := range []Record{uncompiled, refused} {
		if err := s.Put(ctx, r); err != nil {
			t.Fatalf("put %s: %v", r.ProposalID, err)
		}
	}

	got, err := s.ForWorkflow(ctx, "tenant-r", "wf-1")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	byID := map[string]Scored{}
	for _, sc := range got {
		byID[sc.ProposalID] = sc
	}
	if byID["p-uncompiled"].RefusalReason != "" {
		t.Errorf("an uncompiled proposal came back marked refused: %q", byID["p-uncompiled"].RefusalReason)
	}
	if byID["p-refused"].RefusalReason == "" || byID["p-refused"].RefusalDimension != "tools" {
		t.Errorf("the refusal did not survive: %+v", byID["p-refused"])
	}
	// Both read `unbuilt`, which is exactly why the reason has to be the marker.
	if byID["p-uncompiled"].BuildStatus != byID["p-refused"].BuildStatus {
		t.Errorf("the two states differ in build_status (%q vs %q) — if that ever becomes true, the "+
			"reason column is no longer load-bearing and this test's premise is stale",
			byID["p-uncompiled"].BuildStatus, byID["p-refused"].BuildStatus)
	}

	// 🔴 A refusal that shipped a diff is the "looks complete" failure D-14.3 refuses: the surface would
	// render a change beside a sentence saying we declined to make it. The database refuses the pair.
	if _, err := db.ExecContext(ctx,
		`UPDATE proposal SET source_diff_blob_hash = $1 WHERE proposal_id = 'p-refused'`,
		hash64); err == nil {
		t.Error("the table accepted a refused proposal carrying a diff")
	}
	// ...and a dimension with no reason names where without saying what.
	if _, err := db.ExecContext(ctx,
		`UPDATE proposal SET refusal_dimension = 'skills' WHERE proposal_id = 'p-uncompiled'`); err == nil {
		t.Error("the table accepted a refusal dimension with no reason")
	}
}

// The candidate spec round-trips, and the column refuses anything that is not a content hash — the
// same rule the diff and grounding columns carry, so an inline spec cannot land in the row.
func TestTheSpecHashRoundTripsAndRefusesInlineContent(t *testing.T) {
	s, db := store(t, "proposalstore_spec")
	ctx := t.Context()

	r := rec("tenant-s", "wf-1", "p1")
	r.SpecBlobHash = hash64
	if err := s.Put(ctx, r); err != nil {
		t.Fatalf("put: %v", err)
	}
	got, err := s.ForWorkflow(ctx, "tenant-s", "wf-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("read: %+v %v", got, err)
	}
	if got[0].SpecBlobHash != hash64 {
		t.Errorf("spec hash = %q", got[0].SpecBlobHash)
	}

	if _, err := db.ExecContext(ctx,
		`UPDATE proposal SET spec_blob_hash = '{"nodes":{}}' WHERE proposal_id = 'p1'`); err == nil {
		t.Error("the table accepted an inline spec where a content hash belongs — the same constraint " +
			"0012 puts on the diff and grounding columns")
	}
}
