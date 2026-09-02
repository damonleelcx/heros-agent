package memory_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/auth"
	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/store"
)

// isolation_test.go is THE fence for P32, and it is the mirror of
// `store.TestATenantCannotReachAnotherTenantsData` — same shape, same reasoning, different package.

// seedGoal returns a goal id owned by a tenant. Postgres needs a real row, because episodes reference
// goals(id); the in-memory store has no goals table and establishes ownership on the first write.
type rootImpl struct {
	name string
	open func(*testing.T) (memory.Root, func(t *testing.T, tenant string) string)
}

func roots() []rootImpl {
	return []rootImpl{
		{"mem", func(*testing.T) (memory.Root, func(*testing.T, string) string) {
			return memory.NewMem(), func(*testing.T, string) string { return uniqueID("g") }
		}},
		{"postgres", func(t *testing.T) (memory.Root, func(*testing.T, string) string) {
			t.Helper()
			dsn := os.Getenv("HEROS_TEST_DATABASE_URL")
			if dsn == "" {
				t.Skip("HEROS_TEST_DATABASE_URL is unset; the Postgres leg did not run")
			}
			db, err := sql.Open("pgx", dsn)
			if err != nil {
				t.Fatalf("open: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			if err := migrations.Apply(context.Background(), db); err != nil {
				t.Fatalf("migrate: %v", err)
			}
			pgRan()
			return memory.NewPG(db), func(t *testing.T, tenant string) string {
				t.Helper()
				// The organization row first: `conversation_turns.tenant` references `tenants(id)`, so a
				// turn for an organization that does not exist is refused. See newPG in
				// conformance_test.go for why this table has the key and its siblings do not.
				if err := auth.NewStore(db).CreateTenant(context.Background(), tenant, "Seeded"); err != nil {
					t.Fatalf("create tenant: %v", err)
				}
				id := goal.ID(uniqueID("g"))
				now := time.Now().UTC()
				g := &goal.Goal{
					ID: id, Tenant: tenant, Intent: intent.Assess, State: goal.Draft,
					Subject: goal.Subject{RepoURL: "git@github.com:acme/bot.git", Revision: "abc"},
					Ceilings: bounds.Ceilings{MaxIterations: 10, MaxTasks: 10, MaxAttemptsPerTask: 2,
						MaxToolCalls: 10, MaxTokens: 1e6, MaxCostCents: 100,
						MaxWallClock: time.Hour, MaxSpawnDepth: 2},
					Criteria:  []goal.Criterion{{Kind: goal.AxesAssessed, Threshold: 1}},
					CreatedAt: now, UpdatedAt: now,
				}
				if err := g.Admit(now); err != nil {
					t.Fatalf("admit: %v", err)
				}
				if err := store.NewPostgres(db).CreateGoal(g); err != nil {
					t.Fatalf("create goal: %v", err)
				}
				return string(id)
			}
		}},
	}
}

// twoTenants returns a fresh pair of organization ids.
//
// ⚠️ Fixed names like "victim-org" do not work here. The Postgres leg shares one database across every
// test and every run, so knowledge and preferences written under a constant tenant ACCUMULATE — and a
// test asserting "the attacker sees none of the victim's claims" then reads the attacker's own leftovers
// from the previous test and reports a cross-tenant leak that did not happen. That is exactly how this
// fence first failed, on Postgres only, while the in-memory leg passed because it starts empty.
//
// The lesson generalises past this file: any assertion that COUNTS rows in a shared database needs a key
// nothing else has used.
func twoTenants() (victim, attacker string) {
	return uniqueID("victim"), uniqueID("attacker")
}

// TestATenantCannotReachAnotherTenantsHistory exercises EVERY method, on both implementations.
//
// # 🔴 Why every method and not the two with callers
//
// Two of these four have no production caller at all today — compression is built and wired to nothing.
// Testing only the two that are used would leave the other two unscoped inside a store called scoped,
// and the unscoped one is the exception somebody later relies on. The goal store's fence made the same
// argument about twelve methods that took a goal id; this is the same shape in a different package.
//
// # 🔴 Why reads and WRITES
//
// `worker.record` discards its error by design — a run must not fail because its narration did. So a
// cross-tenant write would be silent: no error anywhere, one customer's run narrated into another's
// timeline. The write path is the one that has to refuse, and it is the one a fence is most needed for
// precisely because nothing else would notice.
func TestATenantCannotReachAnotherTenantsHistory(t *testing.T) {
	for _, impl := range roots() {
		t.Run(impl.name, func(t *testing.T) {
			root, seed := impl.open(t)
			victim, attacker := twoTenants()

			theirs := seed(t, victim)
			v := root.For(victim)
			for i := 1; i <= 3; i++ {
				if _, err := v.AppendEpisode(obs(theirs, "the victim's private history")); err != nil {
					t.Fatalf("seeding episode %d: %v", i, err)
				}
			}
			if _, err := v.SaveSummary(memory.Summary{
				GoalID: theirs, FromSeq: 1, ToSeq: 2, Content: "a private summary",
				At: time.Now().UTC()}); err != nil {
				t.Fatalf("seeding summary: %v", err)
			}

			a := root.For(attacker)

			// ── reads are INVISIBLE, not forbidden ────────────────────────────────────────────────
			// Returning an error would confirm the goal id is real, turning a guessable identifier into
			// an enumeration of everybody's runs. A cross-tenant read returns what a missing goal returns.
			eps, err := a.Episodes(theirs)
			if err != nil {
				t.Errorf("Episodes across tenants errored (%v); it must be indistinguishable from a "+
					"goal that does not exist", err)
			}
			if len(eps) != 0 {
				t.Errorf("Episodes returned %d of another tenant's episodes", len(eps))
			}
			sums, err := a.Summaries(theirs)
			if err != nil {
				t.Errorf("Summaries across tenants errored (%v)", err)
			}
			if len(sums) != 0 {
				t.Errorf("Summaries returned %d of another tenant's summaries", len(sums))
			}

			// ── writes are REFUSED ────────────────────────────────────────────────────────────────
			if _, err := a.AppendEpisode(obs(theirs, "planted by somebody else")); err == nil {
				t.Error("AppendEpisode wrote into another tenant's history. worker.record discards its " +
					"error, so this would be silent: one customer's run narrated into another's timeline")
			}
			if _, err := a.SaveSummary(memory.Summary{
				GoalID: theirs, FromSeq: 1, ToSeq: 1, Content: "planted", At: time.Now().UTC()}); err == nil {
				t.Error("SaveSummary wrote into another tenant's history")
			}

			// ── and the victim's record is untouched ──────────────────────────────────────────────
			after, err := v.Episodes(theirs)
			if err != nil {
				t.Fatalf("re-reading the victim's episodes: %v", err)
			}
			if len(after) != 3 {
				t.Errorf("the victim now has %d episodes, not the 3 they wrote — a refused write still "+
					"changed their history", len(after))
			}
			for _, e := range after {
				if e.Summary != "the victim's private history" {
					t.Errorf("a planted episode reached the victim's record: %q", e.Summary)
				}
			}
		})
	}
}

// TestTheTenantIsImposedNotTrusted.
//
// 🔴 Knowledge and preferences already carry a tenant field, which means a caller can SET it — and a
// caller who can set it can set it to somebody else. The scoped view overwrites it on the way in, the
// same way `store.CreateGoal` imposes the tenant on a goal, so the field on the object is a value the
// store reports rather than a value the store obeys.
func TestTheTenantIsImposedNotTrusted(t *testing.T) {
	for _, impl := range roots() {
		t.Run(impl.name, func(t *testing.T) {
			root, seed := impl.open(t)
			victim, attacker := twoTenants()
			mine := seed(t, attacker)

			a := root.For(attacker)
			if _, err := a.AppendEpisode(obs(mine, "evidence")); err != nil {
				t.Fatalf("seed: %v", err)
			}

			// A claim that names the victim as its tenant.
			k, err := memory.Promote(victim, "acme/bot", "context.summarisation", "absent",
				[]memory.Episode{{GoalID: mine, Seq: 1, Kind: memory.EpisodeObservation}})
			if err != nil {
				t.Fatalf("promote: %v", err)
			}
			if err := a.PromoteKnowledge(k); err != nil {
				t.Fatalf("promote knowledge: %v", err)
			}
			// A preference that names the victim as its tenant.
			if err := a.SetPreference(memory.Preference{
				Tenant: victim, Key: "model", Value: "flash", AuthoredBy: "damon",
				At: time.Now().UTC()}); err != nil {
				t.Fatalf("set preference: %v", err)
			}

			// Neither reached the victim.
			vk, err := root.For(victim).KnowledgeFor(victim, "acme/bot")
			if err != nil {
				t.Fatalf("knowledge: %v", err)
			}
			if len(vk) != 0 {
				t.Errorf("a claim labelled with the victim's tenant was stored under it: %+v", vk)
			}
			vp, err := root.For(victim).Preferences(victim)
			if err != nil {
				t.Fatalf("preferences: %v", err)
			}
			if len(vp) != 0 {
				t.Errorf("a preference labelled with the victim's tenant was stored under it: %+v", vp)
			}

			// Both landed under the attacker, where they belong.
			ak, _ := a.KnowledgeFor(attacker, "acme/bot")
			if len(ak) != 1 {
				t.Errorf("the claim did not land under the tenant that wrote it: %+v", ak)
			}
			ap, _ := a.Preferences(attacker)
			if len(ap) != 1 {
				t.Errorf("the preference did not land under the tenant that wrote it: %+v", ap)
			}
		})
	}
}

// TestASuppliedTenantIsIgnored.
//
// `KnowledgeFor` and `Preferences` keep a tenant parameter they no longer need, so that call sites did
// not have to churn — the same choice `store.LatestGoal(_ string)` made. This is the fence that keeps
// the parameter honest: passing somebody else's tenant changes nothing.
func TestASuppliedTenantIsIgnored(t *testing.T) {
	for _, impl := range roots() {
		t.Run(impl.name, func(t *testing.T) {
			root, seed := impl.open(t)
			victim, attacker := twoTenants()

			theirs := seed(t, victim)
			v := root.For(victim)
			if _, err := v.AppendEpisode(obs(theirs, "evidence")); err != nil {
				t.Fatalf("seed: %v", err)
			}
			k, err := memory.Promote(victim, "acme/bot", "context.summarisation", "absent",
				[]memory.Episode{{GoalID: theirs, Seq: 1, Kind: memory.EpisodeObservation}})
			if err != nil {
				t.Fatalf("promote: %v", err)
			}
			if err := v.PromoteKnowledge(k); err != nil {
				t.Fatalf("promote knowledge: %v", err)
			}
			if err := v.SetPreference(memory.Preference{
				Tenant: victim, Key: "model", Value: "flash", AuthoredBy: "damon",
				At: time.Now().UTC()}); err != nil {
				t.Fatalf("set preference: %v", err)
			}

			// The attacker asks for the victim's tenant, by name.
			a := root.For(attacker)
			got, err := a.KnowledgeFor(victim, "acme/bot")
			if err != nil {
				t.Fatalf("knowledge: %v", err)
			}
			if len(got) != 0 {
				t.Errorf("naming the victim's tenant returned %d of their claims; the argument is being "+
					"obeyed instead of ignored", len(got))
			}
			prefs, err := a.Preferences(victim)
			if err != nil {
				t.Fatalf("preferences: %v", err)
			}
			if len(prefs) != 0 {
				t.Errorf("naming the victim's tenant returned %d of their preferences", len(prefs))
			}
		})
	}
}

// TestAnEmptyTenantCannotWriteAnything.
//
// 🔴 "" is the value an unset variable has, and a store bound to it must not become a store bound to
// everything. The worker skips recording entirely when the tenant is empty; this is the layer below
// that, so the skip is a convenience rather than the guarantee.
func TestAnEmptyTenantCannotWriteAnything(t *testing.T) {
	for _, impl := range roots() {
		t.Run(impl.name, func(t *testing.T) {
			root, seed := impl.open(t)
			id := seed(t, "somebody")
			nobody := root.For("")

			if _, err := nobody.AppendEpisode(obs(id, "from nowhere")); err == nil {
				t.Error("a store bound to no tenant appended an episode")
			}
			if _, err := nobody.SaveSummary(memory.Summary{
				GoalID: id, FromSeq: 1, ToSeq: 1, Content: "x", At: time.Now().UTC()}); err == nil {
				t.Error("a store bound to no tenant saved a summary")
			}
			if eps, err := nobody.Episodes(id); err == nil && len(eps) != 0 {
				t.Errorf("a store bound to no tenant read %d episodes", len(eps))
			}
		})
	}
}

var _ = errors.Is

// TestATenantCannotReachAnotherTenantsConversation.
//
// # 🔴 Why turns need their own fence rather than inheriting the episode one
//
// Episodes scope through `goals` — `goal_id IN (SELECT id FROM goals WHERE tenant = $n)` — so their
// isolation is a property of that join. Turns have no goal to join to and scope on their own `tenant`
// column instead. That is a SECOND mechanism, and a guarantee proven for one mechanism says nothing
// about the other; the episodes-were-never-scoped bug recorded in store/scoped_pg.go is exactly what
// happens when a package's isolation story is assumed to cover a record it does not.
//
// # What "isolated" means here, precisely
//
// A conversation id is chosen by the client. Two tenants can therefore name the SAME conversation id,
// and the correct behaviour is not an error — it is that they are two different conversations. An
// attacker who guesses a victim's conversation id must get their own empty thread, and anything they
// write must land in it rather than in the victim's.
func TestATenantCannotReachAnotherTenantsConversation(t *testing.T) {
	for _, impl := range roots() {
		t.Run(impl.name, func(t *testing.T) {
			root, seed := impl.open(t)
			victim, attacker := twoTenants()
			conv := uniqueID("c")

			// Both organizations must EXIST before either can speak: turns carry a foreign key to
			// `tenants(id)`. seed creates the org (and, incidentally, a goal this test does not use) on
			// Postgres, and is a no-op on the in-memory store.
			seed(t, victim)
			seed(t, attacker)

			v := root.For(victim)
			for i := 1; i <= 3; i++ {
				if _, err := v.AppendTurn(memory.Turn{
					ConversationID: conv, Role: memory.TurnUser, Body: "the victim's private question",
					At: time.Now().UTC()}); err != nil {
					t.Fatalf("seeding turn %d: %v", i, err)
				}
			}

			a := root.For(attacker)

			// ── reads are INVISIBLE, not forbidden ────────────────────────────────────────────────
			got, err := a.Turns(victim, conv)
			if err != nil {
				t.Errorf("Turns across tenants errored (%v); it must be indistinguishable from a "+
					"conversation that does not exist", err)
			}
			if len(got) != 0 {
				t.Errorf("Turns returned %d of another tenant's turns", len(got))
			}

			// 🔴 The tenant ARGUMENT is ignored in favour of the bound one. A scoped store that honoured
			// it would hand every caller a parameter that reads anybody's transcript.
			if latest, ok, err := a.LatestConversation(victim); err != nil {
				t.Errorf("LatestConversation across tenants errored: %v", err)
			} else if ok && latest == conv {
				t.Error("LatestConversation returned another tenant's thread id: passing somebody " +
					"else's tenant must change nothing")
			}

			// ── writes land in the attacker's OWN thread, never the victim's ──────────────────────
			if _, err := a.AppendTurn(memory.Turn{
				Tenant: victim, ConversationID: conv, Role: memory.TurnAgent, Body: "planted",
				At: time.Now().UTC()}); err != nil {
				t.Fatalf("the attacker's own write was refused: %v", err)
			}

			after, err := v.Turns(victim, conv)
			if err != nil {
				t.Fatalf("re-reading the victim's turns: %v", err)
			}
			if len(after) != 3 {
				t.Fatalf("the victim's conversation now has %d turns, not the 3 they wrote — a turn "+
					"naming their tenant was obeyed rather than overwritten", len(after))
			}
			for _, tn := range after {
				if tn.Body == "planted" {
					t.Error("a planted turn reached the victim's transcript")
				}
				if tn.Tenant != victim {
					t.Errorf("the victim's turn reports tenant %q", tn.Tenant)
				}
			}
		})
	}
}
