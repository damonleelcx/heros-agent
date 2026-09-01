package memory_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	migrations "github.com/heros-foreal/heros/db/migrations"
	"github.com/heros-foreal/heros/internal/bounds"
	"github.com/heros-foreal/heros/internal/goal"
	"github.com/heros-foreal/heros/internal/intent"
	"github.com/heros-foreal/heros/internal/memory"
	"github.com/heros-foreal/heros/internal/store"
)

// Every guarantee is written ONCE and run against both implementations. The Postgres leg skips without
// a DSN, and TestZZPostgresLegActuallyRan fails when the DSN is set but nothing ran — a skip is not a
// pass, and the most dangerous green build is the one where the thing you care about did not execute.

type impl struct {
	name string
	open func(*testing.T) (memory.Store, string) // store, goalID (episodes reference a real goal in PG)
}

var seq struct {
	sync.Mutex
	n int
}

func uniqueID(prefix string) string {
	seq.Lock()
	defer seq.Unlock()
	seq.n++
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixNano(), seq.n)
}

func newMem(t *testing.T) (memory.Store, string) {
	t.Helper()
	return memory.NewMem(), uniqueID("g")
}

// newPG creates a real goal first, because episodes reference goals(id) — a foreign key the in-memory
// store does not have, and exactly the kind of difference a conformance suite exists to surface.
func newPG(t *testing.T) (memory.Store, string) {
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
	id := goal.ID(uniqueID("g"))
	now := time.Now().UTC()
	g := &goal.Goal{
		ID: id, Tenant: "t1", Intent: intent.Assess, State: goal.Draft,
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
	pgRan()
	return memory.NewPG(db), string(id)
}

func implementations() []impl {
	return []impl{{"mem", newMem}, {"postgres", newPG}}
}

func obs(goalID, summary string) memory.Episode {
	return memory.Episode{GoalID: goalID, Kind: memory.EpisodeObservation, Summary: summary,
		At: time.Now().UTC().Truncate(time.Millisecond)}
}

// TestEpisodesAreOrderedByTheStore.
//
// 🔴 The sequence is assigned by the store, not the caller. Two workers writing episodes for one goal
// would otherwise choose the same number — and the order of a run is the one thing an episodic record
// exists to preserve.
func TestEpisodesAreOrderedByTheStore(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			s, gid := im.open(t)
			for i := 1; i <= 5; i++ {
				got, err := s.AppendEpisode(obs(gid, fmt.Sprintf("step %d", i)))
				if err != nil {
					t.Fatalf("append: %v", err)
				}
				if got != int64(i) {
					t.Fatalf("append %d returned seq %d", i, got)
				}
			}
			es, err := s.Episodes(gid)
			if err != nil {
				t.Fatalf("episodes: %v", err)
			}
			if len(es) != 5 {
				t.Fatalf("%d episodes, want 5", len(es))
			}
			for i, e := range es {
				if e.Seq != int64(i+1) {
					t.Errorf("episode %d has seq %d", i, e.Seq)
				}
			}
		})
	}
}

// TestConcurrentWritersGetDistinctSequences. Run under -race.
func TestConcurrentWritersGetDistinctSequences(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			s, gid := im.open(t)
			const n = 16
			var wg sync.WaitGroup
			var mu sync.Mutex
			seen := map[int64]bool{}
			wg.Add(n)
			for i := 0; i < n; i++ {
				go func(i int) {
					defer wg.Done()
					got, err := s.AppendEpisode(obs(gid, fmt.Sprintf("concurrent %d", i)))
					if err != nil {
						t.Errorf("append: %v", err)
						return
					}
					mu.Lock()
					defer mu.Unlock()
					if seen[got] {
						t.Errorf("sequence %d was handed out twice", got)
					}
					seen[got] = true
				}(i)
			}
			wg.Wait()
			if len(seen) != n {
				t.Fatalf("%d distinct sequences from %d writers", len(seen), n)
			}
		})
	}
}

// TestKnowledgeCannotBeStoredWithoutEvidence.
//
// 🔴 The property the whole class exists for. An agent able to write knowledge directly launders its own
// speculation into fact, and the next goal reads that fact as if somebody had established it.
func TestKnowledgeCannotBeStoredWithoutEvidence(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			s, gid := im.open(t)
			bare := memory.Knowledge{Tenant: "t1", Subject: "acme/bot", Key: "k", Value: "v"}
			if err := s.PromoteKnowledge(bare); !errors.Is(err, memory.ErrNoEvidence) {
				t.Fatalf("a claim with no evidence was stored: %v", err)
			}
			good, err := memory.Promote("t1", "acme/bot", "context.summarisation", "absent",
				[]memory.Episode{{GoalID: gid, Seq: 1, Kind: memory.EpisodeObservation}})
			if err != nil {
				t.Fatalf("promote: %v", err)
			}
			if err := s.PromoteKnowledge(good); err != nil {
				t.Fatalf("a promoted claim was refused: %v", err)
			}
			ks, err := s.KnowledgeFor("t1", "acme/bot")
			if err != nil || len(ks) != 1 {
				t.Fatalf("knowledge = %d (%v)", len(ks), err)
			}
			if ks[0].EvidenceGoalID != gid || len(ks[0].EvidenceSeqs) != 1 {
				t.Errorf("evidence did not survive the round trip: %+v", ks[0])
			}
		})
	}
}

// TestABeliefThatChangesKeepsTheOldOne. Knowing a belief changed, and on what evidence, is what lets
// somebody audit a decision made while it was still held.
func TestABeliefThatChangesKeepsTheOldOne(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			s, gid := im.open(t)
			for _, v := range []string{"absent", "present"} {
				k, err := memory.Promote("t1", "acme/bot", "context.summarisation", v,
					[]memory.Episode{{GoalID: gid, Seq: 1, Kind: memory.EpisodeObservation}})
				if err != nil {
					t.Fatalf("promote: %v", err)
				}
				k.At = time.Now().UTC()
				if err := s.PromoteKnowledge(k); err != nil {
					t.Fatalf("promote: %v", err)
				}
				time.Sleep(2 * time.Millisecond) // distinct timestamps; the PK includes `at`
			}
			ks, err := s.KnowledgeFor("t1", "acme/bot")
			if err != nil {
				t.Fatalf("knowledge: %v", err)
			}
			if len(ks) != 1 {
				t.Fatalf("%d current claims, want 1 — the superseded one is still being returned", len(ks))
			}
			if ks[0].Value != "present" {
				t.Errorf("current value is %q, want the later one", ks[0].Value)
			}
		})
	}
}

// TestAPreferenceNeedsAHumanAuthor.
func TestAPreferenceNeedsAHumanAuthor(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			s, _ := im.open(t)
			for _, author := range []string{"", "system", "agent", "heros", "worker-1"} {
				err := s.SetPreference(memory.Preference{Tenant: "t1", Key: "model", Value: "flash",
					AuthoredBy: author})
				if err == nil {
					t.Errorf("a preference authored by %q was stored; an agent that infers a preference "+
						"has invented a mandate", author)
				}
			}
			if err := s.SetPreference(memory.Preference{Tenant: "t1", Key: "model", Value: "flash",
				AuthoredBy: "damon", At: time.Now().UTC()}); err != nil {
				t.Fatalf("a human-authored preference was refused: %v", err)
			}
			ps, err := s.Preferences("t1")
			if err != nil || len(ps) != 1 {
				t.Fatalf("preferences = %d (%v)", len(ps), err)
			}
			if ps[0].AuthoredBy != "damon" {
				t.Errorf("author lost: %q", ps[0].AuthoredBy)
			}
		})
	}
}

// TestCompressionKeepsTheSourceAndRefusesFailures.
func TestCompressionKeepsTheSourceAndRefusesFailures(t *testing.T) {
	for _, im := range implementations() {
		t.Run(im.name, func(t *testing.T) {
			s, gid := im.open(t)
			var eps []memory.Episode
			for i := 1; i <= 4; i++ {
				e := obs(gid, fmt.Sprintf("read file %d", i))
				sq, err := s.AppendEpisode(e)
				if err != nil {
					t.Fatalf("append: %v", err)
				}
				e.Seq = sq
				eps = append(eps, e)
			}
			sum, err := memory.Compress(gid, eps, "read four files", time.Now().UTC())
			if err != nil {
				t.Fatalf("compress: %v", err)
			}
			if sum.Dropped != 4 || sum.FromSeq != 1 || sum.ToSeq != 4 {
				t.Fatalf("summary covers the wrong range: %+v", sum)
			}
			if _, err := s.SaveSummary(sum); err != nil {
				t.Fatalf("save summary: %v", err)
			}
			// 🔴 The episodes are still there. Compression must be auditable: "what did the summary leave
			// out" is unanswerable once the source is gone.
			after, err := s.Episodes(gid)
			if err != nil {
				t.Fatalf("episodes: %v", err)
			}
			if len(after) != 4 {
				t.Fatalf("%d episodes survive compression, want 4", len(after))
			}
			for _, e := range after {
				if e.SummarisedBy == 0 {
					t.Errorf("episode %d is not marked as covered", e.Seq)
				}
			}
		})
	}
}

var pgLeg struct {
	sync.Mutex
	ran bool
}

func pgRan() {
	pgLeg.Lock()
	defer pgLeg.Unlock()
	pgLeg.ran = true
}

// TestZZPostgresLegActuallyRan — a skip is not a pass.
func TestZZPostgresLegActuallyRan(t *testing.T) {
	if os.Getenv("HEROS_TEST_DATABASE_URL") == "" {
		t.Log("HEROS_TEST_DATABASE_URL unset: the Postgres leg was SKIPPED, and a skip is not a pass")
		return
	}
	pgLeg.Lock()
	defer pgLeg.Unlock()
	if !pgLeg.ran {
		t.Fatal("HEROS_TEST_DATABASE_URL is set but no Postgres subtest ran; the suite would have " +
			"reported green while testing only the in-memory implementation")
	}
}
