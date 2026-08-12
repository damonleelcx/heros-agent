// Command agentrollout answers whether this fleet's CURRENT SHAPE permits the next rollout stage
// (P30 task 9.4).
//
// # 🚫 "No stage verified by hand"
//
// The task's own words, and what they rule out is the thing every rollout actually does: somebody looks
// at a dashboard, decides it seems fine, and advances. That is a habit rather than a gate, and it fails
// in one specific way — the stage that gets waved through is the one advanced under time pressure,
// which is the same stage where the evidence is thinnest.
//
// So this reads the evidence itself: it counts the placement table, reads the active definition's
// rehearsal state, and checks whether a fleet ceiling exists. Then it either reports that the step is
// permitted or names the ONE precondition that failed.
//
// # 🔴 It changes nothing
//
// Not a safety rail on an otherwise-automatic process — there is no automatic process. An operator
// still sets each placement deliberately with a reason, because automating enablement would put "read a
// customer's source under a platform credential" behind a scheduler, which is exactly the posture Q2
// chose the default to avoid. This says whether the fleet's shape is a legal next step, and stops.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"

	_ "github.com/lib/pq"

	"github.com/heros-foreal/agentd/internal/herosagent"
)

func main() {
	want := flag.String("want", "", "the stage to test a move to: internal | partner | opt_in | default_on. "+
		"Empty reports the current stage and stops.")
	internal := flag.String("internal-tenants", os.Getenv("HEROS_INTERNAL_TENANTS"),
		"comma-separated tenant ids this deployment considers its own. The one input here that is "+
			"DECLARED rather than read: the platform cannot infer which of its customers is itself.")
	flag.Parse()

	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		fatal("DATABASE_URL is unset, so there is no placement table to count. This command reads the " +
			"fleet's actual shape and has nothing to fall back on — a rollout decision made against an " +
			"assumed shape is the hand-verification this exists to replace.")
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		fatal("opening the platform database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()
	placements, err := herosagent.NewPGPlacementStore(db)
	if err != nil {
		fatal("placement store: %v", err)
	}
	versions, err := herosagent.NewPGVersionStore(db)
	if err != nil {
		fatal("version store: %v", err)
	}
	caps, err := herosagent.NewPGCapStore(db)
	if err != nil {
		fatal("cap store: %v", err)
	}

	reader := herosagent.NewRolloutReader(placements, versions, caps, splitTenants(*internal), totalTenants(db))
	evidence, err := reader.Evidence(ctx)
	if err != nil {
		fatal("reading the fleet's shape: %v", err)
	}

	stage := herosagent.CurrentStage(evidence)
	fmt.Printf("current stage      %s\n", stage)
	fmt.Printf("enabled tenants    %d of %d\n", len(evidence.EnabledTenants), evidence.TotalTenants)
	fmt.Printf("active definition  %s (%s)\n", display(evidence.ActiveConfigHash), evidence.RehearsalState)
	fmt.Printf("fleet ceiling      %s\n", capWord(evidence.FleetCapSet))

	if strings.TrimSpace(*want) == "" {
		return
	}
	if err := herosagent.Advance(evidence, herosagent.Stage(*want)); err != nil {
		fmt.Printf("\n🔴 %s is NOT permitted from %s:\n   %v\n", *want, stage, err)
		// 🔴 A non-zero exit, so a pipeline that asks this question cannot proceed past a `no` by
		// ignoring stdout. The refusal names the ONE precondition that failed, which is the thing an
		// operator acts on.
		os.Exit(1)
	}
	fmt.Printf("\n✓ %s is permitted from %s.\n"+
		"  This command changed NOTHING. An operator still sets each placement deliberately, with a\n"+
		"  reason — enabling analysis for a customer is a decision, not a step in a pipeline.\n",
		*want, stage)
}

// totalTenants counts the deployment's tenants for the default-on threshold.
//
// It reads `account`, which is the table that answers "how many customers does this deployment have".
// A count from the placement table would be circular — it would only ever see tenants somebody had
// already made a placement decision about, so the last rung would be reachable the moment every
// considered tenant was enabled, regardless of how many were never considered.
func totalTenants(db *sql.DB) func(context.Context) (int, error) {
	return func(ctx context.Context) (int, error) {
		var n int
		err := db.QueryRowContext(ctx, `SELECT count(*) FROM account`).Scan(&n)
		if err != nil {
			// A deployment without the billing schema still has a rollout; it just cannot answer the
			// default-on threshold, and zero makes `CurrentStage` fall through to `opt_in` rather than
			// claiming the fleet is mostly enabled.
			return 0, nil
		}
		return n, nil
	}
}

func splitTenants(s string) []string {
	out := []string{}
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func display(hash string) string {
	if hash == "" {
		return "none"
	}
	if len(hash) <= 12 {
		return hash
	}
	return hash[:12]
}

func capWord(set bool) string {
	if set {
		return "set"
	}
	return "🔴 NONE — analysis is unbounded"
}

func fatal(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "agentrollout: "+format+"\n", args...)
	os.Exit(2)
}
