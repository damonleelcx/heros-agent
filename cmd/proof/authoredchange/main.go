// Command authoredchange surveys what USER-AUTHORED changes the platform can actually make against a
// real repository, node by node and axis by axis (P13 13c / P14 14c / P15 15d / P16 16c).
//
// # Why a survey and not a demo
//
// A demo picks the node that works. This walks EVERY discovered node on EVERY axis and reports the
// verdict for each, including the refusals — because the number that matters for this feature is not
// "can it apply a change" but "on how many of a real repository's call sites, and where it cannot, does
// it say why".
//
// It drives the SAME probe the authoring preflight uses (`authoringwire.Materializer`), which runs the
// real codemod and discards what it produces. So a verdict here is the verdict a user would get.
//
// 🔴 What this deliberately does NOT do: resolve registry refs. Offline there is no registry, so a
// `--model some/ref` would refuse as an unresolved ref before ever reaching the codemod — a real
// refusal, but not the one this survey is about. Instead it builds resolved configurations directly,
// which is how the transform's own tests drive it, so the question asked is "would the codemod emit
// this?" rather than "does this ref exist?".
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/authoring"
	"github.com/heros-foreal/agentd/internal/authoringwire"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

func main() {
	repo := flag.String("repo", "", "path to the checked-out repository (required)")
	irPath := flag.String("ir", "", "path to a discovery IR JSON (required)")
	commit := flag.String("commit", "", "source revision")
	// 🔴 A bound, and it is REPORTED rather than silent. Each probe re-indexes the whole tree, so a
	// 3,000-file repository × 41 nodes × 4 axes is minutes of walking. Sampling is legitimate; sampling
	// quietly is not — a survey that covered 8 nodes and printed a number that reads like 41 would be
	// exactly the "no silent caps" failure.
	limit := flag.Int("limit", 8, "nodes to probe per axis (0 = all); the sample size is always printed")
	flag.Parse()

	if *repo == "" || *irPath == "" {
		fmt.Fprintln(os.Stderr, "usage: authoredchange --repo PATH --ir IR.json [--commit SHA]")
		os.Exit(2)
	}

	raw, err := os.ReadFile(*irPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read IR: %v\n", err)
		os.Exit(1)
	}
	var ir discovery.IR
	if err := json.Unmarshal(raw, &ir); err != nil {
		fmt.Fprintf(os.Stderr, "parse IR: %v\n", err)
		os.Exit(1)
	}

	lang := ir.Workflow.Language
	probe := authoringwire.Materializer{Root: *repo}
	cov := authoringwire.Coverage{}
	mat := authoringwire.WiringMaterializability{}

	fmt.Printf("repository : %s\n", *repo)
	fmt.Printf("revision   : %s\n", firstNonEmpty(*commit, ir.Workflow.Repo.CommitSHA))
	fmt.Printf("language   : %s\n", lang)
	sample := ir.Nodes
	if *limit > 0 && *limit < len(sample) {
		sample = sample[:*limit]
	}
	fmt.Printf("nodes      : %d discovered\n", len(ir.Nodes))
	fmt.Printf("probed     : %d per axis%s\n\n", len(sample), sampledNote(len(sample), len(ir.Nodes)))

	// Each axis, surveyed over every node. The override for each is the smallest one that exercises that
	// axis's rewriter — the point is which axis emits, not which value it emits.
	axes := []struct {
		name     string
		override func() variantspec.ResolvedOverride
	}{
		{"model", func() variantspec.ResolvedOverride {
			return variantspec.ResolvedOverride{Model: &registry.ModelEntry{
				VersionID: strings.Repeat("a", 64), Name: "survey",
				Spec: registry.ModelSpec{Provider: "openai", ModelID: "gpt-4o-mini"}}}
		}},
		{"prompt", func() variantspec.ResolvedOverride {
			// A parsed template with no slots: the rewriter needs the TEMPLATE, not a raw body, because
			// what it writes at the call site is the rendered text.
			tpl, err := registry.ParseTemplate("You are a careful assistant.")
			if err != nil {
				panic("survey template: " + err.Error())
			}
			return variantspec.ResolvedOverride{Prompt: &registry.PromptEntry{
				VersionID: strings.Repeat("b", 64), Name: "survey", Template: tpl}}
		}},
		{"skills", func() variantspec.ResolvedOverride {
			return variantspec.ResolvedOverride{Skills: []*registry.SkillEntry{
				{VersionID: strings.Repeat("c", 64), Name: "rerank"}}}
		}},
		{"context", func() variantspec.ResolvedOverride {
			// 🔴 Spec.Policy, not just Name: the rewriter dispatches on the POLICY, and an entry with an
			// empty one refused with `context policy ""` — a refusal about this survey's fixture rather
			// than about hermes. A survey that reports its own defect as the repository's is worse than
			// no survey.
			return variantspec.ResolvedOverride{Context: &registry.ContextEntry{
				VersionID: strings.Repeat("d", 64), Name: "sliding-window",
				Spec:   registry.ContextSpec{Policy: "sliding-window", Params: []byte(`{"window_size":8}`)},
				Policy: registry.SlidingWindowPolicy{}}}
		}},
	}

	type tally struct {
		applied int
		refused map[string]int
	}
	results := map[string]*tally{}

	for _, ax := range axes {
		t := &tally{refused: map[string]int{}}
		results[ax.name] = t
		for _, n := range sample {
			resolved := &variantspec.Resolved{
				ConfigHash: strings.Repeat("e", 64), SourceRevision: firstNonEmpty(*commit, ir.Workflow.Repo.CommitSHA),
				Language:  lang,
				Overrides: map[string]variantspec.ResolvedOverride{n.NodeID: ax.override()},
			}
			ref, err := probe.Probe(context.Background(), resolved)
			switch {
			case err != nil:
				t.refused["probe error: "+truncate(err.Error())]++
			case ref.Cause == "":
				t.applied++
			default:
				t.refused[reasonKey(ref.Cause)]++
			}
		}
	}

	fmt.Println("PER-AXIS OUTCOME — what an authored change on each axis does to this repository")
	fmt.Println(strings.Repeat("─", 96))
	for _, ax := range axes {
		t := results[ax.name]
		fmt.Printf("\n%s: %d/%d probed nodes would APPLY\n", strings.ToUpper(ax.name), t.applied, len(sample))
		for _, r := range sortedKeys(t.refused) {
			fmt.Printf("    %3d refused — %s\n", t.refused[r], r)
		}
	}

	// The two boundaries a user meets before they choose anything.
	fmt.Printf("\n\nBOUNDARIES STATED BEFORE THE USER CHOOSES\n")
	fmt.Println(strings.Repeat("─", 96))
	sel := authoring.NodeSelection{NodeID: "<any>", Language: lang}
	offer := authoring.OfferSkills(sel, cov)
	if offer.Refused.Cause != "" {
		fmt.Printf("skills  : NOT OFFERED — %s\n", offer.Refused.Cause)
	} else {
		fmt.Printf("skills  : offered (%s has a materializer)\n", lang)
	}
	for _, shape := range authoring.WiringShapes() {
		ok, reason := mat.CanMaterialize(shape, lang)
		if ok {
			fmt.Printf("wiring  : %-28s APPLICABLE\n", shape)
		} else {
			fmt.Printf("wiring  : %-28s refused — %s\n", shape, truncate(reason))
		}
	}

	fmt.Printf("\n\nWHAT A USER MAY CLAIM\n")
	fmt.Println(strings.Repeat("─", 96))
	fmt.Println("Every change above, if applied, is recorded UNVERIFIED: outside the verified-delta ledger,")
	fmt.Println("contributing nothing to any improvement or savings figure, and never auto-merged. A number")
	fmt.Println("attaches to it only after a multi-seed evaluation has run.")
}

// reasonKey collapses a refusal to its distinguishing clause, so the tally groups by CAUSE rather than
// by node. A hundred refusals for one reason is one finding; a hundred lines is a wall.
//
// 🔴 An earlier version split on the FIRST ": " and assumed what followed began with `node "`. The real
// message is `transform: cannot rewrite this call site safely: node "x", dimension "y": <reason>`, so
// the first split landed on "transform" and the prefix check failed — every node produced its own key
// and the tally grouped nothing. The anchor is now the `dimension "` marker the engine actually writes.
func reasonKey(cause string) string {
	if i := strings.Index(cause, `dimension "`); i >= 0 {
		rest := cause[i:]
		if j := strings.Index(rest, `": `); j >= 0 {
			return truncate(rest[j+3:])
		}
	}
	// No dimension marker: fall back to the whole sentence rather than guessing at a shorter one.
	return truncate(cause)
}

func truncate(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 150 {
		return s[:147] + "…"
	}
	return s
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// sampledNote says plainly when the survey did not cover everything.
func sampledNote(probed, total int) string {
	if probed >= total {
		return " (every discovered node)"
	}
	return fmt.Sprintf(" — A SAMPLE; %d of %d nodes were not probed (pass --limit 0 for all)",
		total-probed, total)
}
