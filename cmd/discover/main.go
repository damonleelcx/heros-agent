// Command discover resolves a repository reference and prints what discovery found, without calling a
// model. It answers "did the extraction work?" separately from "was the judgement any good?", which is
// the whole reason those two halves are separate packages.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/heros-foreal/heros/internal/discovery"
	"github.com/heros-foreal/heros/internal/intake"
	"github.com/heros-foreal/heros/internal/intent"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("usage: discover <path-or-github-ref>")
		os.Exit(2)
	}
	cache, _ := os.UserCacheDir()
	r := intake.NewResolver(filepath.Join(cache, "heros", "repos"))
	src, err := r.Resolve(os.Args[1])
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("source   %s\nkind     %s\n\n", src.Describe(), src.Kind)

	corpus, err := discovery.Walk(src.Root, discovery.Limits{})
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	var parts []string
	for l, n := range corpus.LanguageCounts() {
		parts = append(parts, fmt.Sprintf("%s %d", l, n))
	}
	sort.Strings(parts)
	fmt.Printf("read     %d files (%s)", len(corpus.Files), strings.Join(parts, ", "))
	if corpus.Truncated {
		fmt.Printf("  ** TRUNCATED")
	}
	fmt.Println()
	if len(corpus.Skipped) > 0 {
		var sk []string
		for reason, n := range corpus.Skipped {
			sk = append(sk, fmt.Sprintf("%s %d", reason, n))
		}
		sort.Strings(sk)
		fmt.Printf("skipped  %s\n", strings.Join(sk, ", "))
	}

	nodes := discovery.Nodes(corpus)
	fmt.Printf("\n--- call sites (%d) ---\n", len(nodes))
	shown := map[string]bool{}
	n := 0
	for _, nd := range nodes {
		if shown[nd.ID] || n >= 10 {
			continue
		}
		shown[nd.ID] = true
		n++
		fmt.Printf("  %-30s %s\n", trunc(nd.ID, 30), nd.Span.Ref())
	}
	if len(nodes) > n {
		fmt.Printf("  ... and %d more call sites\n", len(nodes)-n)
	}

	fmt.Printf("\n--- axis evidence ---\n")
	for _, axis := range intent.Axes() {
		ev := discovery.ForAxis(corpus, axis)
		if ev.Found {
			files := map[string]bool{}
			for _, s := range ev.Spans {
				files[s.Path] = true
			}
			fmt.Printf("  OK   %-9s %2d spans / %d file(s)   e.g. %s -- %s\n",
				axis, len(ev.Spans), len(files), ev.Spans[0].Ref(), ev.Spans[0].Why)
		} else {
			fmt.Printf("  MISS %-9s %s\n", axis, trunc(ev.Note, 100))
		}
	}
}

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "..."
}
