package hostedboard

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/heros-foreal/agentd/internal/evalboard"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/runlink"
)

// p27_scope_test.go is P27 task 9.3: tenant scoping is applied to LISTING, and never to a statistic.
//
// # The distinction, and why it is worth a file
//
// "Show this organization its own runs" and "compute this organization's number" sound like the same
// sentence with the scope in a different place. They are not. A scope on the listing SELECTS ROWS and
// then hands them to a statistic that has no idea a tenant exists — so the statistic answers the same
// question it always answered, about a smaller set. A scope reaching INSIDE the statistic changes the
// question: a normalization over "this tenant's runs", a percentile over "this tenant's costs", a
// frontier drawn against "this tenant's best" are all different measurements wearing the old name.
//
// That is the failure mode 9.3 names, and the reason it is worth guarding is that it is SILENT. Nothing
// errors. A board keeps rendering, ranks keep coming out, and the numbers on it stopped meaning what the
// column header says — with no version, no migration and no banner to notice.
//
// So the shape this file asserts is: `ForWorkflow(tenantID, workflowID)` is the ONLY place the tenant is
// consulted, and everything downstream of it is a pure function of the rows it returned.

// scopeRun builds one linked run for a named tenant. Deliberately not hostedboard_test.go's `run`
// helper: these tests turn on the tenant field, which that helper hard-codes.
func scopeRun(tenant, configHash string, quality, cost, latency float64) linkingest.LinkedRun {
	return linkingest.LinkedRun{
		RunID: tenant + "-run-" + configHash, TenantID: tenant, WorkflowID: "wf", ConfigHash: configHash,
		LinkedAt: time.Date(2026, 8, 5, 9, 0, 0, 0, time.UTC),
		Scores: []runlink.Score{
			{Metric: "quality", Value: quality, CILow: quality - 0.04, CIHigh: quality + 0.04},
			{Metric: "cost_usd", Value: cost},
			{Metric: "latency_ms", Value: latency},
		},
		Eval: runlink.EvalSummary{CaseCount: 8, SeedCount: 5, GateOutcome: runlink.GatePass},
	}
}

func storeWith(t *testing.T, runs ...linkingest.LinkedRun) *linkingest.MemStore {
	t.Helper()
	s := linkingest.NewMemStore()
	for _, r := range runs {
		if _, err := s.Record(r); err != nil {
			t.Fatalf("record %s: %v", r.RunID, err)
		}
	}
	return s
}

func jsonOf(t *testing.T, v evalboard.View) string {
	t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal board: %v", err)
	}
	return string(b)
}

// TestScopingSelectsRowsAndThenLeavesTheStatisticAlone is the assertion itself.
//
// The scoped board — assembled by a Source that went through a real tenant-scoped store — must be
// IDENTICAL to the board built from the same rows handed over directly, with no tenant in sight. If the
// two ever diverge, something downstream of the listing consulted the tenant.
func TestScopingSelectsRowsAndThenLeavesTheStatisticAlone(t *testing.T) {
	alphaRuns := []linkingest.LinkedRun{
		scopeRun("org_alpha", "aaaa1111", 0.91, 0.020, 900),
		scopeRun("org_alpha", "aaaa2222", 0.72, 0.004, 350),
	}
	betaRuns := []linkingest.LinkedRun{
		scopeRun("org_beta", "bbbb3333", 0.99, 0.001, 100),
		scopeRun("org_beta", "bbbb4444", 0.10, 0.900, 9000),
	}
	src := NewSource(storeWith(t, append(append([]linkingest.LinkedRun{}, alphaRuns...), betaRuns...)...))

	scoped, ok := src.Board("org_alpha", "wf", "")
	if !ok {
		t.Fatal("org_alpha has linked runs for wf and got no board")
	}
	// The same statistic, over the same rows, with the tenant nowhere in the call.
	unscoped := Build("wf", alphaRuns)
	unscoped.Spend = spendOf(alphaRuns)

	if got, want := jsonOf(t, scoped), jsonOf(t, unscoped); got != want {
		t.Errorf("the tenant-scoped board differs from the same rows measured without a tenant.\n"+
			"Scoping is supposed to select what is listed and then get out of the way; a difference here "+
			"means the tenant reached a computation.\n scoped:   %s\n unscoped: %s", got, want)
	}
}

// TestAnotherOrganizationsRunsDoNotMoveThisOnesNumbers is the same property from the outside, and it is
// the one a customer would actually feel.
//
// 🔴 org_beta's runs are extreme on purpose — near-perfect quality at a tenth of a cent, and a disastrous
// one at ninety cents. Any statistic that normalized, ranked or drew a frontier across the whole store
// would move org_alpha's numbers the moment org_beta linked a run. A board ranks what it ranked.
func TestAnotherOrganizationsRunsDoNotMoveThisOnesNumbers(t *testing.T) {
	alphaRuns := []linkingest.LinkedRun{
		scopeRun("org_alpha", "aaaa1111", 0.91, 0.020, 900),
		scopeRun("org_alpha", "aaaa2222", 0.72, 0.004, 350),
	}
	alone := NewSource(storeWith(t, alphaRuns...))
	crowded := NewSource(storeWith(t, append(append([]linkingest.LinkedRun{}, alphaRuns...),
		scopeRun("org_beta", "bbbb3333", 0.99, 0.0001, 100),
		scopeRun("org_beta", "bbbb4444", 0.10, 0.9000, 9000),
		scopeRun("org_gamma", "cccc5555", 0.55, 0.5000, 5000),
	)...))

	before, ok := alone.Board("org_alpha", "wf", "")
	if !ok {
		t.Fatal("no board for org_alpha in an empty-but-for-alpha store")
	}
	after, ok := crowded.Board("org_alpha", "wf", "")
	if !ok {
		t.Fatal("no board for org_alpha once other organizations had linked runs")
	}
	if got, want := jsonOf(t, after), jsonOf(t, before); got != want {
		t.Errorf("org_alpha's board changed when other organizations linked runs it cannot see.\n"+
			"Nothing org_alpha did moved these numbers, and nothing on the board would say why.\n"+
			" before: %s\n after:  %s", want, got)
	}
}

// TestTheStatisticCannotSeeTheTenantLabel closes the last door the two tests above leave open: they both
// feed the statistic rows that CARRY a tenant id, and agree only because the tenants were partitioned
// the same way in each. This one keeps the rows identical and changes only the label on them.
func TestTheStatisticCannotSeeTheTenantLabel(t *testing.T) {
	base := []linkingest.LinkedRun{
		scopeRun("org_alpha", "aaaa1111", 0.91, 0.020, 900),
		scopeRun("org_alpha", "aaaa2222", 0.72, 0.004, 350),
	}
	relabelled := make([]linkingest.LinkedRun, len(base))
	for i, r := range base {
		r.TenantID = "org_completely_different"
		relabelled[i] = r
	}
	if got, want := jsonOf(t, Build("wf", relabelled)), jsonOf(t, Build("wf", base)); got != want {
		t.Errorf("relabelling the owner of the same runs changed the board. The rows are identical; only "+
			"who owns them differs, and ownership is not a measurement.\n with alpha: %s\n relabelled: %s",
			want, got)
	}
}

// TestNoTenantIdentifierIsRenderedIntoTheBoard is the leak in the other direction: not a tenant that
// changed a number, but a tenant that arrived as one.
//
// The board is a measurement document. An organization id inside it would be a value some client could
// key on, compare, or send onward — and it is exactly the kind of field that gets added because it was
// "already in hand" at assembly time.
func TestNoTenantIdentifierIsRenderedIntoTheBoard(t *testing.T) {
	src := NewSource(storeWith(t,
		scopeRun("org_alpha_distinctive_id", "aaaa1111", 0.91, 0.020, 900),
		scopeRun("org_alpha_distinctive_id", "aaaa2222", 0.72, 0.004, 350),
	))
	view, ok := src.Board("org_alpha_distinctive_id", "wf", "")
	if !ok {
		t.Fatal("no board")
	}
	if doc := jsonOf(t, view); strings.Contains(doc, "org_alpha_distinctive_id") {
		t.Errorf("the rendered board carries the requesting organization's id. It is a measurement "+
			"document; who asked is not part of what was measured.\n%s", doc)
	}
}

// TestTheStatisticTakesNoTenantParameter reads the SIGNATURES, because the tests above can only speak
// about a tenant that changes an answer. A tenant threaded into Build and then not yet used would pass
// every one of them, and would be the last edit before it started being used.
//
// Board is the boundary and is EXPECTED to take one — that is the listing. Everything downstream of it
// must not.
func TestTheStatisticTakesNoTenantParameter(t *testing.T) {
	// The boundary: Board(tenantID, workflowID, profile). The scope belongs here and nowhere deeper.
	boardFn := reflect.TypeOf((*Source)(nil)).Method(0)
	if boardFn.Name != "Board" {
		t.Fatalf("Source's exported surface starts with %q, not Board; this fence is aimed at the wrong "+
			"method", boardFn.Name)
	}
	if n := boardFn.Type.NumIn(); n != 4 { // receiver + tenantID + workflowID + profile
		t.Errorf("Source.Board takes %d arguments; the listing boundary changed shape and this fence's "+
			"premise needs re-reading", n)
	}

	// The statistic: Build(workflowID, runs). Two arguments, and neither of them is a tenant.
	build := reflect.TypeOf(Build)
	if build.NumIn() != 2 {
		t.Fatalf("Build takes %d arguments, want 2 (workflow id, runs). A third is how a tenant gets in.",
			build.NumIn())
	}
	if build.In(0).Kind() != reflect.String || build.In(1) != reflect.TypeOf([]linkingest.LinkedRun(nil)) {
		t.Errorf("Build's signature is (%s, %s); it must stay (string, []linkingest.LinkedRun) so the "+
			"only thing it can measure is the rows the listing already scoped", build.In(0), build.In(1))
	}
}
