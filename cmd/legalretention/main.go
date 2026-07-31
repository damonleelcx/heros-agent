// Command legalretention runs the consent-record retention job (P23 task 9.7).
//
// # 🔴 Why it defaults to a dry run
//
// A deletion job whose first production run is also its first run ever is a defect waiting for a quiet
// weekend. So the destructive mode is opt-in: `-apply` is a flag an operator types deliberately, and
// every other invocation reports exactly what a live run would remove without removing it.
//
// # 🔴 Why an unset window deletes nothing
//
// The retention period is a LEGAL answer — seven years, per the escalation recorded in
// docs/decisions/p23-one-way-doors.md §1.4 — not an engineering default. A job that invented one would
// delete the wrong things confidently. With no `-window`, this refuses and says so.
//
//	go run ./cmd/legalretention -db "$DATABASE_URL"                 # dry run, refuses: no window
//	go run ./cmd/legalretention -db "$DATABASE_URL" -window 61320h  # dry run, 7 years, reports the cutoff
//	go run ./cmd/legalretention -db "$DATABASE_URL" -window 61320h -apply
//
// # What it does not do
//
// It does not erase a subject. That is a different operation with different semantics — erasure
// TOMBSTONES and keeps the evidentiary row, retention removes the row entirely — and giving them one
// entry point would invite running the wrong one.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/heros-foreal/agentd/internal/legal"
	_ "github.com/lib/pq"
)

func main() {
	var (
		dsn    = flag.String("db", os.Getenv("DATABASE_URL"), "PostgreSQL connection string")
		window = flag.Duration("window", 0, "retention window, e.g. 61320h for 7 years. Unset deletes nothing.")
		apply  = flag.Bool("apply", false, "actually delete. Without it this is a dry run.")
	)
	flag.Parse()

	if *dsn == "" {
		fmt.Fprintln(os.Stderr, "legalretention: -db (or DATABASE_URL) is required")
		os.Exit(2)
	}

	db, err := sql.Open("postgres", *dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, "legalretention:", err)
		os.Exit(2)
	}
	defer func() { _ = db.Close() }()

	svc := legal.NewService(
		legal.NewPGStore(db),
		// The retention job does not read the manifest: it removes rows by age and needs no opinion about
		// which version is current. An empty source rather than a live one keeps this command runnable on
		// a host that cannot reach the console.
		legal.StaticManifestSource{},
		time.Now,
		func() string { return "" },
	)

	res, err := svc.RetentionJob(context.Background(), *window, !*apply)
	if err != nil {
		fmt.Fprintln(os.Stderr, "legalretention:", err)
		os.Exit(1)
	}

	switch {
	case res.Refused != "":
		// A refusal is an outcome, not an error: exit 0, say what was not done and why.
		fmt.Printf("legalretention: REFUSED — %s\n", res.Refused)
	case res.DryRun:
		fmt.Printf("legalretention: DRY RUN — would remove acceptances accepted before %s. "+
			"Nothing was deleted. Re-run with -apply to act.\n", res.Cutoff.Format(time.RFC3339))
	default:
		fmt.Printf("legalretention: removed %d acceptance(s) accepted before %s.\n",
			res.Removed, res.Cutoff.Format(time.RFC3339))
	}
}
