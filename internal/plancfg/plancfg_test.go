package plancfg

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// catalogV1 is the fixture catalog: four NAMED plans with limits and OPAQUE price references. It holds
// no dollar amount — a price is a provider handle here, which is why a fixture catalog can exist at all.
// It is written to a temp dir at test time, never committed (see gitfence_test.go).
const catalogV1 = `{
  "version": "cfg-2026-07-23-a",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,
     "features":["cli","discovery"],
     "limits":{"sum_band":100,"seats":1,"retention_days":7,"eval_compute":10},
     "price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":1000,"seats":5,"retention_days":30,"eval_compute":100},
     "price_refs":{"subscription":"price_ref_team_sub","metered":"price_ref_team_metered"}},
    {"plan_id":"business","display_name":"Business","rank":2,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":10000,"seats":25,"retention_days":90,"eval_compute":1000},
     "price_refs":{"subscription":"price_ref_biz_sub","metered":"price_ref_biz_metered"}},
    {"plan_id":"enterprise","display_name":"Enterprise","rank":3,
     "features":["cli","discovery","assisted_pr","dashboard","auto_merge"],
     "limits":{"seats":500,"retention_days":365},
     "price_refs":{"subscription":"price_ref_ent_sub","metered":"price_ref_ent_metered","gainshare":"price_ref_ent_gainshare"}}
  ]
}`

// catalogV2 is the SAME catalog with exactly two edits: Team's seat limit is raised and its metered
// price reference is repointed. Nothing else moves — so a behaviour change after publishing it can only
// have come from configuration.
const catalogV2 = `{
  "version": "cfg-2026-07-23-b",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,
     "features":["cli","discovery"],
     "limits":{"sum_band":100,"seats":1,"retention_days":7,"eval_compute":10},
     "price_refs":{}},
    {"plan_id":"team","display_name":"Team","rank":1,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":1000,"seats":50,"retention_days":30,"eval_compute":100},
     "price_refs":{"subscription":"price_ref_team_sub","metered":"price_ref_team_metered_v2"}},
    {"plan_id":"business","display_name":"Business","rank":2,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":10000,"seats":25,"retention_days":90,"eval_compute":1000},
     "price_refs":{"subscription":"price_ref_biz_sub","metered":"price_ref_biz_metered"}},
    {"plan_id":"enterprise","display_name":"Enterprise","rank":3,
     "features":["cli","discovery","assisted_pr","dashboard","auto_merge"],
     "limits":{"seats":500,"retention_days":365},
     "price_refs":{"subscription":"price_ref_ent_sub","metered":"price_ref_ent_metered","gainshare":"price_ref_ent_gainshare"}}
  ]
}`

// catalogV3 adds a BRAND-NEW named plan. The entitlements spec requires that introducing a plan is a
// config publish, not a code change or a migration.
const catalogV3 = `{
  "version": "cfg-2026-07-23-c",
  "plans": [
    {"plan_id":"free","display_name":"Free","rank":0,
     "features":["cli","discovery"],
     "limits":{"sum_band":100,"seats":1,"retention_days":7,"eval_compute":10},
     "price_refs":{}},
    {"plan_id":"starter","display_name":"Starter","rank":1,
     "features":["cli","discovery","assisted_pr"],
     "limits":{"sum_band":300,"seats":2,"retention_days":14,"eval_compute":30},
     "price_refs":{"subscription":"price_ref_starter_sub"}},
    {"plan_id":"team","display_name":"Team","rank":2,
     "features":["cli","discovery","assisted_pr","dashboard"],
     "limits":{"sum_band":1000,"seats":5,"retention_days":30,"eval_compute":100},
     "price_refs":{"subscription":"price_ref_team_sub","metered":"price_ref_team_metered"}},
    {"plan_id":"enterprise","display_name":"Enterprise","rank":3,
     "features":["cli","discovery","assisted_pr","dashboard","auto_merge"],
     "limits":{"seats":500,"retention_days":365},
     "price_refs":{"subscription":"price_ref_ent_sub","metered":"price_ref_ent_metered","gainshare":"price_ref_ent_gainshare"}}
  ]
}`

// newFileResolver stands up a resolver over a file config store in a temp dir — the deployed shape,
// not a mock: the same FileSource the process uses reads the same JSON the config store publishes.
func newFileResolver(t *testing.T, initial string) (*Resolver, string, *MemAudit) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "plans.json")
	if err := os.WriteFile(path, []byte(initial), 0o600); err != nil {
		t.Fatalf("write fixture catalog: %v", err)
	}
	audit := NewMemAudit()
	r := NewResolver(NewFileSource(path), audit)
	r.SetClock(func() time.Time { return time.Unix(1_700_000_000, 0).UTC() })
	if _, err := r.Reload("fixture"); err != nil {
		t.Fatalf("initial publish: %v", err)
	}
	return r, path, audit
}

// publish rewrites the config store and reloads — the whole "publish a plan change" operation. Note
// what it does NOT do: rebuild, restart, migrate, or restart the resolver. Same process, same objects.
func publish(t *testing.T, r *Resolver, path, catalog string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(catalog), 0o600); err != nil {
		t.Fatalf("publish catalog: %v", err)
	}
	if _, err := r.Reload("finance"); err != nil {
		t.Fatalf("reload after publish: %v", err)
	}
}

// TestResolvePlanReadsTheConfigStore is task 1.2's base case: limits, features and price references all
// come from the store, and an unknown plan is an explicit error rather than a silent default.
func TestResolvePlanReadsTheConfigStore(t *testing.T) {
	r, _, _ := newFileResolver(t, catalogV1)

	team, err := r.ResolvePlan("team")
	if err != nil {
		t.Fatalf("resolve team: %v", err)
	}
	if team.DisplayName != "Team" {
		t.Errorf("display name = %q, want Team", team.DisplayName)
	}
	if !team.Entitles(FeatureAssistedPR) || !team.Entitles(FeatureDashboard) {
		t.Errorf("team should entitle assisted PR + dashboard, got %v", team.Features)
	}
	if team.Entitles(FeatureAutoMerge) {
		t.Error("team must NOT entitle auto-merge")
	}
	if got, ok := team.Limit(LimitSeats); !ok || got != 5 {
		t.Errorf("team seats = %v (set=%v), want 5", got, ok)
	}
	if team.PriceRefs["metered"] != "price_ref_team_metered" {
		t.Errorf("metered price ref = %q", team.PriceRefs["metered"])
	}
	if team.Version != "cfg-2026-07-23-a" {
		t.Errorf("resolved plan must carry the catalog version, got %q", team.Version)
	}

	// An unset limit is UNLIMITED, not zero. Enterprise sets no SUM band on purpose.
	ent, err := r.ResolvePlan("enterprise")
	if err != nil {
		t.Fatalf("resolve enterprise: %v", err)
	}
	if _, ok := ent.Limit(LimitSUMBand); ok {
		t.Error("enterprise sets no sum_band in the fixture; Limit must report it as unset")
	}

	if _, err := r.ResolvePlan("platinum"); !errors.Is(err, ErrUnknownPlan) {
		t.Errorf("unknown plan must be an explicit error, got %v", err)
	}
}

// TestPlanChangeTakesEffectWithNoCodeDeploy is task 1.4 / FR6, the load-bearing config test.
//
// It repoints a fixture plan's LIMIT and its PRICE REFERENCE in the config store and asserts the new
// values are live — in the same process, against the same resolver object, with no restart, no
// rebuild, and no migration. The "zero code change" half is structural: the test body between the two
// assertions is a file write and a reload.
func TestPlanChangeTakesEffectWithNoCodeDeploy(t *testing.T) {
	r, path, audit := newFileResolver(t, catalogV1)

	before, err := r.ResolvePlan("team")
	if err != nil {
		t.Fatalf("resolve before: %v", err)
	}
	if seats, _ := before.Limit(LimitSeats); seats != 5 {
		t.Fatalf("precondition: team seats = %v, want 5", seats)
	}
	if before.PriceRefs["metered"] != "price_ref_team_metered" {
		t.Fatalf("precondition: metered ref = %q", before.PriceRefs["metered"])
	}
	versionBefore := r.Version()

	publish(t, r, path, catalogV2) // <- the ONLY thing that happens between the two assertions

	after, err := r.ResolvePlan("team")
	if err != nil {
		t.Fatalf("resolve after: %v", err)
	}
	if seats, _ := after.Limit(LimitSeats); seats != 50 {
		t.Errorf("after publish, team seats = %v, want 50 — the new limit did not take effect", seats)
	}
	if after.PriceRefs["metered"] != "price_ref_team_metered_v2" {
		t.Errorf("after publish, metered ref = %q, want the repointed reference", after.PriceRefs["metered"])
	}
	if r.Version() == versionBefore {
		t.Errorf("config version did not advance: still %q", versionBefore)
	}

	// Task 1.3: the publish emitted a plan_change audit event naming what moved — and NOT what it moved
	// to (an audit row carrying a price is a price in a log).
	evs := audit.Events()
	if len(evs) != 2 { // initial publish + this one
		t.Fatalf("audit events = %d, want 2 (initial publish + change)", len(evs))
	}
	last := evs[1]
	if last.FromVersion != versionBefore || last.ToVersion != r.Version() {
		t.Errorf("audit event versions = %q -> %q, want %q -> %q", last.FromVersion, last.ToVersion, versionBefore, r.Version())
	}
	if len(last.Changed) != 1 || last.Changed[0] != "team" {
		t.Errorf("audit event changed = %v, want [team]", last.Changed)
	}
	if last.Actor != "finance" {
		t.Errorf("audit actor = %q, want finance", last.Actor)
	}
}

// TestNewPlanIsIntroducedByConfiguration is the entitlements spec's "a new plan is introduced by
// configuration" scenario: a plan that did not exist becomes resolvable and enforceable with no code
// change and no migration.
func TestNewPlanIsIntroducedByConfiguration(t *testing.T) {
	r, path, audit := newFileResolver(t, catalogV1)

	if _, err := r.ResolvePlan("starter"); !errors.Is(err, ErrUnknownPlan) {
		t.Fatalf("precondition: starter must not exist yet, got %v", err)
	}

	publish(t, r, path, catalogV3)

	starter, err := r.ResolvePlan("starter")
	if err != nil {
		t.Fatalf("the new plan must resolve after a config publish: %v", err)
	}
	if !starter.Entitles(FeatureAssistedPR) || starter.Entitles(FeatureDashboard) {
		t.Errorf("starter entitlements came out wrong: %v", starter.Features)
	}

	last := audit.Events()[len(audit.Events())-1]
	if len(last.Added) != 1 || last.Added[0] != "starter" {
		t.Errorf("audit added = %v, want [starter]", last.Added)
	}
	if len(last.Removed) != 1 || last.Removed[0] != "business" {
		t.Errorf("audit removed = %v, want [business]", last.Removed)
	}

	// Plans come back in RANK order, which is what the upgrade-path search and the packaging UI walk.
	var ids []string
	for _, p := range r.Plans() {
		ids = append(ids, p.PlanID)
	}
	want := []string{"free", "starter", "team", "enterprise"}
	if len(ids) != len(want) {
		t.Fatalf("plans = %v, want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("plans = %v, want %v", ids, want)
		}
	}
}

// TestPublishIsRefusedWhenTheAuditSinkIsDown is the write-ahead-audit discipline: a packaging change
// that cannot be explained afterwards must not take effect. The previous catalog stays live.
func TestPublishIsRefusedWhenTheAuditSinkIsDown(t *testing.T) {
	r, path, audit := newFileResolver(t, catalogV1)
	audit.SetDown(true)

	if err := os.WriteFile(path, []byte(catalogV2), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := r.Reload("finance"); err == nil {
		t.Fatal("publish must be REFUSED when the plan_change audit cannot be written")
	}

	team, err := r.ResolvePlan("team")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if seats, _ := team.Limit(LimitSeats); seats != 5 {
		t.Errorf("the refused publish took effect anyway: seats = %v, want the previous 5", seats)
	}
}

// TestResolverFailsClosedBeforeAnyPublish: no configuration means every resolve fails loudly. A default
// plan here would silently grant or deny entitlements nobody configured.
func TestResolverFailsClosedBeforeAnyPublish(t *testing.T) {
	r := NewResolver(NewMemSource(), NewMemAudit())
	if _, err := r.ResolvePlan("team"); !errors.Is(err, ErrNoConfig) {
		t.Errorf("want ErrNoConfig before any publish, got %v", err)
	}
}

// TestCatalogValidationRejectsGarbage: the config store is the source of truth, so a malformed catalog
// must be rejected at load rather than half-applied.
func TestCatalogValidationRejectsGarbage(t *testing.T) {
	cases := map[string]string{
		"unknown feature":  `{"version":"v","plans":[{"plan_id":"x","display_name":"X","features":["teleport"]}]}`,
		"unknown limit":    `{"version":"v","plans":[{"plan_id":"x","display_name":"X","limits":{"bandwidth":1}}]}`,
		"negative limit":   `{"version":"v","plans":[{"plan_id":"x","display_name":"X","limits":{"seats":-1}}]}`,
		"no display name":  `{"version":"v","plans":[{"plan_id":"x"}]}`,
		"empty price ref":  `{"version":"v","plans":[{"plan_id":"x","display_name":"X","price_refs":{"subscription":""}}]}`,
		"duplicate plan":   `{"version":"v","plans":[{"plan_id":"x","display_name":"X"},{"plan_id":"X","display_name":"X"}]}`,
		"no plan id":       `{"version":"v","plans":[{"display_name":"X"}]}`,
		"unversioned":      `{"plans":[{"plan_id":"x","display_name":"X"}]}`,
		"malformed json":   `{`,
		"not even a plan ": `[]`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "plans.json")
			if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
				t.Fatalf("write: %v", err)
			}
			r := NewResolver(NewFileSource(path), NewMemAudit())
			if _, err := r.Reload("t"); err == nil {
				t.Fatalf("catalog %q must be rejected, was accepted", name)
			}
		})
	}
}

// TestMemSourceAndFileSourceAgree: the hermetic store and the deployed store parse one fixture the same
// way, so a test that passes against MemSource is evidence about the file path too.
func TestMemSourceAndFileSourceAgree(t *testing.T) {
	mem := NewMemSource()
	if err := mem.PublishJSON([]byte(catalogV1)); err != nil {
		t.Fatalf("publish json: %v", err)
	}
	memRes := NewResolver(mem, NewMemAudit())
	if _, err := memRes.Reload("t"); err != nil {
		t.Fatalf("mem reload: %v", err)
	}
	fileRes, _, _ := newFileResolver(t, catalogV1)

	for _, id := range []string{"free", "team", "business", "enterprise"} {
		a, err := memRes.ResolvePlan(id)
		if err != nil {
			t.Fatalf("mem resolve %s: %v", id, err)
		}
		b, err := fileRes.ResolvePlan(id)
		if err != nil {
			t.Fatalf("file resolve %s: %v", id, err)
		}
		if !samePlan(a, b) {
			t.Errorf("plan %s differs between the in-memory and file config stores", id)
		}
	}
}
