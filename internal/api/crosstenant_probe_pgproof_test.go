//go:build pgproof

// Live-Postgres proof of P27's isolation claim, over HTTP (task 11.2, run-ownership FR).
//
// # Why this test exists at the API and not only at the store
//
// `internal/executor` already proves the STORE scopes a listing by owner. That is the easy half. The
// claim a customer relies on is about the API: *another organization's run is indistinguishable from a
// run that does not exist* — and between the store and the wire sit the principal resolution, the
// `visibleToPrincipal` decision, and the choice of status code. Each is a place isolation can be
// correct underneath and absent on the wire.
//
// # 🔴 The trap this test is written against
//
// The PRD names it: the cross-tenant refusal "passes vacuously if both probes run as the same tenant".
// It is worse than that — a probe that only asserts `404` passes against a platform with NO isolation at
// all, because a run id that does not exist also returns `404`. Asserting the refusal proves nothing
// unless the SAME request, changed in exactly one way, succeeds.
//
// So every probe below is a PAIR: the owner reads the run and gets 200, the stranger reads the same run
// and gets 404. One request, one field different. If isolation were removed, the first assertion still
// passes and the second fails; if the run were simply missing, the FIRST fails and says so.
package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/heros-foreal/agentd/internal/auth"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/executor"
	"github.com/heros-foreal/agentd/internal/pgmigrate"
	"github.com/heros-foreal/agentd/internal/pgtest"
	_ "github.com/lib/pq"
)

const (
	orgAcme   = "org_acme_probe"
	orgGlobex = "org_globex_probe"
	probeHash = "bbbb111122223333444455556666777788889999aaaabbbbccccddddeeeeffff"
)

// openProbeDB brings the schema up through the EMBEDDED SET, not a hand-listed subset.
//
// The sibling proof in this package hand-lists five migration files. `internal/pgmigrate`'s header names
// that pattern as the reason nothing in CI applied anything past ~0009: a proof against its own subset
// is a proof against a schema no deployment has, and it goes red the first time another phase touches a
// table it writes. This test reads `run.tenant_id`, which is exactly such a column.
func openProbeDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := pgtest.Open("proof_api_crosstenant")
	if err != nil {
		t.Fatalf("postgres: %v", err)
	}
	if _, err := pgmigrate.Apply(context.Background(), db); err != nil {
		t.Fatalf("apply the embedded migration set: %v", err)
	}
	return db
}

// seedProbeRuns writes one run per organization, plus one that predates ownership, through the real
// store — so the rows carry whatever the production write path puts in them.
func seedProbeRuns(t *testing.T, db *sql.DB) *executor.Store {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO workflow (workflow_id, repo_url, commit_sha, language, ir_version)
		  VALUES ('wf_probe','https://example.invalid/r','abc123','go','1.0.0') ON CONFLICT DO NOTHING`, nil},
		{`INSERT INTO variant (variant_id, workflow_id, label)
		  VALUES ('v_probe','wf_probe','probe') ON CONFLICT DO NOTHING`, nil},
		{`INSERT INTO config (config_hash, variant_id, workflow_id, ir_version, lineage_json)
		  VALUES ($1,'v_probe','wf_probe','1.0.0','{}') ON CONFLICT DO NOTHING`, []any{probeHash}},
		{`INSERT INTO variant_spec (config_hash, source_revision, spec_json)
		  VALUES ($1,'rev1','{}') ON CONFLICT DO NOTHING`, []any{probeHash}},
		{`INSERT INTO transform (config_hash, source_revision, build_status, verification_strength)
		  VALUES ($1,'rev1','built','type-checked') ON CONFLICT DO NOTHING`, []any{probeHash}},
	} {
		if _, err := db.ExecContext(ctx, stmt.sql, stmt.args...); err != nil {
			t.Fatalf("seed lineage: %v", err)
		}
	}
	store := executor.NewStore(db)
	if err := store.Start(ctx, "run_acme", probeHash, "rev1", 1, orgAcme); err != nil {
		t.Fatalf("start acme's run: %v", err)
	}
	if err := store.Start(ctx, "run_globex", probeHash, "rev1", 2, orgGlobex); err != nil {
		t.Fatalf("start globex's run: %v", err)
	}
	// A run from before P27. Its owner was never written and is not recoverable (D6).
	if err := store.Start(ctx, "run_legacy", probeHash, "rev1", 3, ""); err != nil {
		t.Fatalf("start the pre-ownership run: %v", err)
	}
	return store
}

func probeServer(t *testing.T, db *sql.DB) *Server {
	t.Helper()
	s := New(nil, config.Config{})
	s.MountConfigRuntime(ConfigRuntimeStores{Runs: executor.NewStore(db)})
	return s
}

// getAs issues a real request carrying a verified principal — the same thing the auth middleware puts in
// the context after resolving a credential.
func getAs(t *testing.T, s *Server, path, tenantID string) (int, map[string]any) {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if tenantID != "" {
		r = r.WithContext(auth.WithPrincipal(r.Context(),
			auth.Principal{TenantID: tenantID, UserID: "usr_probe", Role: "owner", APIKeyID: "cred_probe"}))
	}
	rec := httptest.NewRecorder()
	s.Mux.ServeHTTP(rec, r)
	var body map[string]any
	if rec.Body.Len() > 0 {
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
	}
	return rec.Code, body
}

// TestPG_AnotherOrganizationsRunIsIndistinguishableFromOneThatDoesNotExist is 11.2.
func TestPG_AnotherOrganizationsRunIsIndistinguishableFromOneThatDoesNotExist(t *testing.T) {
	db := openProbeDB(t)
	seedProbeRuns(t, db)
	s := probeServer(t, db)

	// ── The control. Without this, everything below passes on a platform that lost the run. ──────────
	code, body := getAs(t, s, "/api/v1/runs/run_globex", orgGlobex)
	if code != http.StatusOK {
		t.Fatalf("globex cannot read its OWN run (status %d, body %v).\n"+
			"Every refusal asserted below would pass vacuously against this — a run nobody can read is "+
			"refused to strangers for the wrong reason.", code, body)
	}

	// ── The probe. Same request, one field different: who is asking. ─────────────────────────────────
	code, body = getAs(t, s, "/api/v1/runs/run_globex", orgAcme)
	if code != http.StatusNotFound {
		t.Fatalf("acme read globex's run and got %d (body %v). A subject belonging to another "+
			"organization must be indistinguishable from one that does not exist.", code, body)
	}

	// 404 and not 403. A 403 confirms the run EXISTS, which is a cross-tenant disclosure in a status
	// code — the caller learns that this id is real and belongs to somebody.
	code, missing := getAs(t, s, "/api/v1/runs/run_does_not_exist_at_all", orgAcme)
	if code != http.StatusNotFound {
		t.Fatalf("a genuinely absent run answered %d", code)
	}
	if a, b := body["error"], missing["error"]; a != b {
		t.Errorf("the two refusals differ in their body: another organization's run says %v and an "+
			"absent one says %v. The status matching is not enough — a caller reads the body too, and a "+
			"difference there is the same disclosure the status code was careful not to make.", a, b)
	}

	// ── And the listing, which is the other way the same fact leaks. ─────────────────────────────────
	code, listed := getAs(t, s, "/api/v1/runs", orgAcme)
	if code != http.StatusOK {
		t.Fatalf("acme's listing answered %d", code)
	}
	rows, _ := listed["runs"].([]any)
	if len(rows) != 1 {
		t.Fatalf("acme's listing returned %d runs, want exactly its own one: %v", len(rows), listed)
	}
	first, _ := rows[0].(map[string]any)
	if first["run_id"] != "run_acme" {
		t.Errorf("acme's listing contains %v", first["run_id"])
	}

	// The pre-ownership run belongs to nobody, so it appears in NOBODY's listing — and is reported as
	// its own state rather than silently dropped.
	if n, ok := listed["pre_ownership_runs"].(float64); !ok || n < 1 {
		t.Errorf("the listing does not report the pre-ownership residue (%v). A silently partial list "+
			"is the quietest kind of wrong: the customer cannot tell it from having no old runs.", listed["pre_ownership_runs"])
	}
}

// TestPG_TheCrossTenantProbeCanFail is the fence on the fence.
//
// 🔴 It runs the probe the WRONG way — both halves as the same organization — and asserts that version
// would have passed. That is the trap the PRD names by hand, made mechanical: if this ever starts
// failing, the vacuous form has stopped being vacuous, which means the real probe above is no longer
// testing what its name says.
func TestPG_TheCrossTenantProbeCanFail(t *testing.T) {
	db := openProbeDB(t)
	seedProbeRuns(t, db)
	s := probeServer(t, db)

	// The vacuous probe: ask twice as globex, once for a run it owns and once for an id that does not
	// exist. Both answers are "correct", and together they assert nothing about isolation.
	okCode, _ := getAs(t, s, "/api/v1/runs/run_globex", orgGlobex)
	missCode, _ := getAs(t, s, "/api/v1/runs/run_nope", orgGlobex)
	if okCode != http.StatusOK || missCode != http.StatusNotFound {
		t.Fatalf("the vacuous probe did not behave as described (%d, %d); this fence's premise has "+
			"changed and the real probe above needs re-reading", okCode, missCode)
	}
	t.Log("the same-organization probe passes 200/404 and proves nothing — which is why " +
		"TestPG_AnotherOrganizationsRunIsIndistinguishableFromOneThatDoesNotExist changes the ASKER " +
		"rather than the run id")
}

// TestPG_AnUnauthenticatedRequestIsRefusedBeforeOwnershipIsConsulted closes the last door: a caller with
// no principal at all must not fall through to a listing.
//
// The failure mode is specific and has a history — a scope derived from a header meant an absent header
// was an absent scope, and an absent scope read as "all of them". P27 deleted that header; this asserts
// the replacement fails closed rather than open.
func TestPG_AnUnauthenticatedRequestIsRefusedBeforeOwnershipIsConsulted(t *testing.T) {
	db := openProbeDB(t)
	seedProbeRuns(t, db)
	s := probeServer(t, db)

	if code, body := getAs(t, s, "/api/v1/runs", ""); code != http.StatusUnauthorized {
		t.Errorf("an unauthenticated listing answered %d (%v), want 401. A request with no verified "+
			"organization has no scope, and no scope must never widen to every scope.", code, body)
	}
	if code, _ := getAs(t, s, "/api/v1/runs/run_globex", ""); code == http.StatusOK {
		t.Error("an unauthenticated caller read a run detail")
	}
}
