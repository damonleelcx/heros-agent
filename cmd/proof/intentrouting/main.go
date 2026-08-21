// Command intentrouting runs the intent router against the held-out labelled set and prints the
// fourteen rows (P31 tasks 3.4, 3.5, 3.7).
//
// # Why this exists as a BINARY as well as a test
//
// Task 3.5: a routing change goes through a SPIKE with the holdout before it lands, and again on any
// later change — with no pure-refactor exemption. A test tells you pass or fail; a spike has to show its
// working, because the person changing the router needs to see WHICH questions moved and in which
// direction. The two share one implementation (`conversation.Evaluate`), so the numbers a spike prints
// and the numbers CI enforces cannot drift.
//
//	make intent-holdout                 # print the table
//	go run ./cmd/proof/intentrouting -holdout <path>
//
// 🚫 There is no `-accuracy` flag and no summary line. See conversation/holdout.go for why.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/heros-foreal/agentd/internal/conversation"
)

func main() {
	path := flag.String("holdout", "internal/conversation/testdata/holdout.json", "the labelled set")
	strict := flag.Bool("strict", false, "exit non-zero if any intent's recall is below the floor")
	flag.Parse()

	h, err := conversation.LoadHoldout(*path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "intentrouting: %v\n", err)
		os.Exit(2)
	}
	report := conversation.Evaluate(conversation.NewRouter(), h)
	fmt.Printf("intent routing · %d held-out questions · abstain threshold %.2f\n\n",
		len(h.Questions), conversation.AbstainThreshold)
	fmt.Print(report.Table())

	if !*strict {
		return
	}
	failed := false
	for _, row := range report.Rows {
		if row.Recall() >= 0 && row.Recall() < conversation.MinIntentRecall {
			fmt.Fprintf(os.Stderr, "\n%s recall %.1f%% is below the floor of %.0f%%\n",
				row.Intent, row.Recall()*100, conversation.MinIntentRecall*100)
			failed = true
		}
		if row.Labelled == 0 {
			fmt.Fprintf(os.Stderr, "\n%s has NO held-out question; it is unmeasured, not healthy\n", row.Intent)
			failed = true
		}
	}
	if p := report.AbstentionPrecision(); p >= 0 && p < conversation.MinAbstentionPrecision {
		fmt.Fprintf(os.Stderr, "\nabstention precision %.1f%% is below the floor of %.0f%%\n",
			p*100, conversation.MinAbstentionPrecision*100)
		failed = true
	}
	if failed {
		os.Exit(1)
	}
}
