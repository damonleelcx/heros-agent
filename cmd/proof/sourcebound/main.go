// Command sourcebound runs P37's source-bound editors against a REAL repository's own call sites.
//
//	clone → discover → build the IR the platform would hold → read each axis's CURRENT VALUE per node
//	      → read the live per-node context coverage → resolve the subject the shell would resolve
//
// # Why this exists when every P37 fence is green and every one has been drilled red
//
// Green fences prove the parts. Every one of them runs against a fixture this repository wrote — two
// nodes, chosen values, a stub platform whose answers were written to make the assertion clean. That is
// the right shape for a fence and it is not evidence about a customer.
//
// This proves the WALK, and the walk is the whole claim of the phase. P37's premise is one sentence:
//
//	a page that explains what the platform WOULD do to a hypothetical node, while the reader's real node
//	is one query away, has stopped being cautious and become stale.
//
// Every node id printed below is a symbol somebody at Nous Research wrote, in a file this repository has
// never seen — so every `observed` value is a real reading of real source, and every `not_measured` names
// a real gap rather than a convenient one.
//
// # 🔴 What it is looking for, which is NOT "everything resolved"
//
// The interesting result is the SHAPE of the answer on a repository nobody here wrote:
//
//   - the axes with a wire field resolve, and their values are the repository's own;
//   - the four axes with NO wire field report `not_measured` with a NAMED missing input, on every node,
//     which is the honest answer and not a gap this run should try to close;
//   - `discovery`'s `unresolved` sentinel, wherever it appears, is reported as absence rather than as a
//     model called "unresolved".
//
// A run where everything resolved would mean the sentinel check never fired, which is a weaker result
// rather than a stronger one. The exit code reflects that: this fails when a value was INVENTED, not
// when one was absent.
//
// 🚫 It calls no provider and costs nothing. Every read P37 adds is a resolve-time read by construction
// — the platform holds the IR and answers from it — so proving it needs no run, no sandbox and no
// credential. A version that spent money would be proving something else.
//
//	git clone --depth 1 https://github.com/nousresearch/hermes-agent /tmp/hermes-agent
//	go run ./cmd/proof/sourcebound -local /tmp/hermes-agent
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/heros-foreal/agentd/internal/assessment"
	"github.com/heros-foreal/agentd/internal/cli"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/linkingest"
	"github.com/heros-foreal/agentd/internal/nodeaxis"
	"github.com/heros-foreal/agentd/internal/nodeaxisvalue"
	"github.com/heros-foreal/agentd/internal/transform"
)

func main() {
	log.SetFlags(0)
	local := flag.String("local", "", "a checkout already on disk (required; clone it first)")
	workflow := flag.String("workflow", "github.com/nousresearch/hermes-agent", "the workflow id to label the run with")
	show := flag.Int("show", 6, "how many of the repository's nodes to print in full")
	flag.Parse()

	if *local == "" {
		log.Fatal("sourcebound: -local is required. Point it at a checkout of the repository under test.\n" +
			"  git clone --depth 1 https://github.com/nousresearch/hermes-agent /tmp/hermes-agent\n" +
			"  go run ./cmd/proof/sourcebound -local /tmp/hermes-agent")
	}

	// ── 1) discovery over the real tree ──────────────────────────────────────────────────────────
	step(1, "discover the repository's own call sites")
	reg, err := discovery.DefaultRegistry()
	if err != nil {
		log.Fatalf("discovery registry: %v", err)
	}
	res, err := discovery.Run(discovery.Options{Repo: *local, Registry: reg, WorkflowID: *workflow, CommitSHA: "local"})
	if err != nil {
		log.Fatalf("discovery: %v", err)
	}
	ir := &res.IR
	if len(ir.Nodes) == 0 {
		fail("discovery found no call sites. P37 binds a surface to a node; a repository with none is " +
			"not a subject for it, and the surfaces would correctly render not_connected.")
	}
	// 🔴 The language is read from the ENGINE's report below, not guessed from the file's extension here.
	// `nodeaxis` says why: a guessed language makes a guessed verdict look computed.
	fmt.Printf("    %d node(s), %d edge(s)\n", len(ir.Nodes), len(ir.Edges))

	// ── 2) the engine's own per-node verdicts, computed HERE ─────────────────────────────────────
	//
	// 🔴 On the customer's machine, which is the only place the source is. The platform is forbidden
	// from deriving one: it knows a node's language and could compute the (axis, language) cell, and it
	// would then claim `applies` for exactly the call sites that refuse for their own shape.
	step(2, "run the transform engine over those call sites, here, where the source is")
	verdicts := nodeaxis.Compute(ir, *local)
	fmt.Printf("    %d node(s) carry verdicts, computed against coverage table %s\n",
		len(verdicts.Nodes), verdicts.CoverageVersion)

	// ── 3) the IR the platform would hold ────────────────────────────────────────────────────────
	step(3, "build the structure the platform would hold, exactly as `heros link --with-ir` sends it")
	payload := cli.BuildWorkflowIRWithVerdicts(ir, verdicts)
	stored := linkingest.WorkflowIR{
		TenantID: "proof", WorkflowID: payload.WorkflowID, SourceRevision: payload.SourceRevision,
		IRVersion: payload.IRVersion, ReceivedAt: time.Unix(0, 0).UTC(),
		CoverageVersion: payload.CoverageVersion, Nodes: payload.Nodes, Edges: payload.Edges,
	}
	fmt.Printf("    %d node(s) crossed the boundary; no prompt text, no source, no keys\n", len(stored.Nodes))

	// ── 4) P37's read: what each of THEIR nodes does today, per axis ─────────────────────────────
	step(4, "read each node's CURRENT value on each axis — the read every editor binds to")
	report := nodeaxisvalue.Build(stored)

	observed := map[assessment.Axis]int{}
	notMeasured := map[assessment.Axis]int{}
	byInput := map[assessment.MissingInput]int{}
	var invented []string

	for _, node := range report.Nodes {
		for _, v := range node.Values {
			switch v.State {
			case assessment.StateObserved:
				observed[v.Axis]++
				// 🔴 The check this proof exists for. `discovery` writes `unresolved` into BOTH model
				// fields for a detect-only declaration — a LITERAL that a naive `!= ""` reads as a model
				// called "unresolved" and prints as this node's model.
				if strings.Contains(v.Current, discovery.UnresolvedSentinel) {
					invented = append(invented, fmt.Sprintf("%s/%s rendered %q as a value", node.NodeID, v.Axis, v.Current))
				}
				if strings.TrimSpace(v.Current) == "" {
					invented = append(invented, fmt.Sprintf("%s/%s is `observed` with nothing to show", node.NodeID, v.Axis))
				}
			case assessment.StateNotMeasured:
				notMeasured[v.Axis]++
				byInput[v.MissingInput]++
				if v.MissingInput == "" || strings.TrimSpace(v.Because) == "" {
					invented = append(invented, fmt.Sprintf("%s/%s is not_measured and names nothing", node.NodeID, v.Axis))
				}
			default:
				invented = append(invented, fmt.Sprintf("%s/%s produced %q; only observed and not_measured are legal here",
					node.NodeID, v.Axis, v.State))
			}
		}
	}

	fmt.Printf("\n    what a reader would see, per axis, across all %d of their nodes:\n", len(report.Nodes))
	for _, axis := range assessment.Axes() {
		fmt.Printf("      %-9s  %3d observed · %3d not measured\n", axis, observed[axis], notMeasured[axis])
	}

	fmt.Printf("\n    and where the absences come from — the NAMED missing inputs:\n")
	inputs := make([]assessment.MissingInput, 0, len(byInput))
	for k := range byInput {
		inputs = append(inputs, k)
	}
	sort.Slice(inputs, func(i, j int) bool { return byInput[inputs[i]] > byInput[inputs[j]] })
	for _, in := range inputs {
		fmt.Printf("      %-26s %4d\n", in, byInput[in])
	}

	// ── 5) the surface, node by node ─────────────────────────────────────────────────────────────
	step(5, "what the editors would render, on their nodes, in their words")
	shown := *show
	if shown > len(report.Nodes) {
		shown = len(report.Nodes)
	}
	for _, node := range report.Nodes[:shown] {
		fmt.Printf("\n    %s", node.NodeID)
		if node.Symbol != "" {
			fmt.Printf("  (%s)", node.Symbol)
		}
		fmt.Printf("\n      %s · %s\n", nonEmpty(node.File, "no file reported"), nonEmpty(node.Language, "no language reported"))
		for _, v := range node.Values {
			if v.State == assessment.StateObserved {
				fmt.Printf("      %-9s observed      %s%s\n", v.Axis, v.Current, paren(v.Detail))
				continue
			}
			fmt.Printf("      %-9s not measured  missing: %s\n", v.Axis, v.MissingInput)
		}
	}
	if len(report.Nodes) > shown {
		fmt.Printf("\n    … and %d more. Every one of them is a call site somebody else wrote.\n", len(report.Nodes)-shown)
	}

	// ── 6) FR17's live coverage, per the repository's OWN languages ──────────────────────────────
	step(6, "read the live per-node context coverage — what replaced the transcribed table")
	seen := map[string]bool{}
	for _, n := range stored.Nodes {
		if n.Language == "" || seen[n.Language] {
			continue
		}
		seen[n.Language] = true
		rows := nodeaxisvalue.ContextCoverageForLanguage(n.Language)
		if len(rows) == 0 {
			fmt.Printf("    %-10s no context rewriter has landed — the surface says so and names the covered set %v\n",
				n.Language, nodeaxisvalue.CoveredLanguages())
			continue
		}
		modes := map[string]int{}
		for _, r := range rows {
			modes[r.Mode]++
		}
		fmt.Printf("    %-10s %d policy row(s): %s\n", n.Language, len(rows), tally(modes))
	}
	if len(seen) == 0 {
		fmt.Println("    no node reported a language, so no coverage is claimed for any of them — which is " +
			"the honest answer rather than a default to Go")
	}

	// ── 7) the subject the shell would resolve ───────────────────────────────────────────────────
	step(7, "resolve the subject the shell would resolve, over their node list")
	switch {
	case len(report.Nodes) == 1:
		fmt.Printf("    one candidate → resolved WITHOUT asking, and still named on screen: %s\n", report.Nodes[0].NodeID)
	default:
		fmt.Printf("    %d candidates and none chosen → the shell asks ONCE, in the shell, and the answer\n"+
			"    applies to all seven axis surfaces. First three offered: %s\n",
			len(report.Nodes), strings.Join(firstIDs(report, 3), ", "))
	}

	// ── the verdict ──────────────────────────────────────────────────────────────────────────────
	fmt.Println()
	if len(invented) > 0 {
		for _, bad := range invented {
			fmt.Printf("  ✖ %s\n", bad)
		}
		fail("%d value(s) were SUPPLIED rather than read. That is the failure P37 exists to prevent: a "+
			"reader would author a change from a baseline that never existed.", len(invented))
	}

	total := 0
	for _, n := range report.Nodes {
		total += len(n.Values)
	}
	fmt.Printf("✔ %d node(s) × %d axes = %d readings, on a repository nobody here wrote.\n",
		len(report.Nodes), len(assessment.Axes()), total)
	fmt.Println("  Every one was READ or reported absent WITH A NAMED INPUT. None was supplied.")
	fmt.Printf("  Coverage table: %s · engine languages with a context rewriter: %v\n",
		transform.CoverageTableVersion(), nodeaxisvalue.CoveredLanguages())
}

func step(n int, what string) { fmt.Printf("\n-- %d) %s\n", n, what) }

func fail(format string, args ...any) {
	fmt.Printf("\n✖ %s\n", fmt.Sprintf(format, args...))
	os.Exit(1)
}

func nonEmpty(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

func paren(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return "  (" + s + ")"
}

func tally(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return counts[keys[i]] > counts[keys[j]] })
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], nonEmpty(k, "unreported")))
	}
	return strings.Join(parts, ", ")
}

func firstIDs(r nodeaxisvalue.Report, n int) []string {
	if n > len(r.Nodes) {
		n = len(r.Nodes)
	}
	out := make([]string, 0, n)
	for _, node := range r.Nodes[:n] {
		out = append(out, node.NodeID)
	}
	return out
}
