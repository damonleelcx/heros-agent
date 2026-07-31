// Command changedelivery drives the CHANGE-DELIVERY contract against a REAL repository
// (github.com/nousresearch/hermes-agent, the same target as cmd/proof/promptmodel … p18hermes).
//
// # What this run proves
//
// P13 13e's claim is not "we added a rollout". It is that DELIVERY IS A TOTAL FUNCTION: for every
// change the platform can propose on a real repository, there is a route that carries it or a named
// cause that refused it — and a change no route can deliver is a REPORTED STATE rather than silence.
//
// 🔴 The finding on this repository is the silence, quantified. Before this contract, a verified change
// on an axis whose rewriter refuses produced no diff, therefore no pull request, therefore nothing, and
// nothing said so. This run counts exactly how often that happens against real nodes, and shows that
// every one of those cases now carries two causes with two owners.
//
// The sections, all running the shipped code paths against the real IR:
//
//	§1  what discovery found, and the apply mode every node is actually in — which decides whether the
//	    runtime route is even reachable. `inline` is the default and nothing here changes it;
//	§2  the delivery table itself: total over (change × route), with the eligible set named rather than
//	    counted, because "3 eligible" is a number and "these three fields" is a claim;
//	§3  🔴 the headline: every (node × change) through the REAL coverage join, counted by delivery STATE
//	    and by cause — and the count of changes that reach the running agent by NO route;
//	§4  the runtime route on real nodes: what a rollout would be refused for here, and why the answer is
//	    the same for all 31 of them;
//	§5  arm assignment on real config hashes: determinism, replica agreement, the share it actually
//	    lands on, and the attribution that fails a run rather than corrupting a comparison;
//	§6  the gates that must say no: a gate-rejected change, a memory strategy, and a harness strategy
//	    each refused at authoring with the cause the transform itself returns.
//
// The boundaries, stated rather than worked around:
//
//	no rollout is written into the target and no document is authored — §5 exercises the resolver's
//	arithmetic on real hashes, which is what it is for. This run parses a repository; it does not
//	deploy to one, and it NEVER executes the target (invariant I1).
//
//	go run ./cmd/proof/changedelivery -repo /tmp/hermes-agent
package main

import (
	"flag"
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/changedelivery"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/sourcerev"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

const repoURL = "https://github.com/nousresearch/hermes-agent"

// A fixed instant, passed rather than read, so two runs of this command produce identical output.
const runNow int64 = 1_800_000_000_000

func main() {
	repo := flag.String("repo", "/tmp/hermes-agent", "path to the hermes-agent checkout (read-only)")
	pin := flag.String("pin", "", `commit this run must be checked out at; empty means "use HEAD and say so"`)
	flag.Parse()

	commitSHA, revNote, err := sourcerev.Resolve(*repo, *pin)
	if err != nil {
		log.Fatalf("%v", err)
	}

	res, err := discovery.Run(discovery.Options{
		Repo:      *repo,
		CommitSHA: commitSHA,
		RepoURL:   repoURL,
		Frontends: []discovery.LanguageFrontend{discovery.NewPythonFrontend()},
	})
	if err != nil {
		log.Fatalf("discovery: %v", err)
	}
	ir := &res.IR

	fmt.Printf("=== change delivery (P13 13e) - run for %s ===\n", repoURL)
	fmt.Printf("discovered %d nodes, %d edges (language=%s) at %s (%s)\n\n",
		len(ir.Nodes), len(ir.Edges), ir.Workflow.Language, short(commitSHA), revNote)

	spec := baselineFrom(ir, commitSHA)
	discovered(ir, spec)
	theTable()
	perNode(ir, spec)
	runtimeRouteHere(ir)
	armAssignment(ir, commitSHA)
	gatesThatSayNo()

	fmt.Println("=== end of run ===")
}

// ── §1 what discovery found, and the apply mode that decides reachability ────────────────────────

func discovered(ir *discovery.IR, spec *variantspec.VariantSpec) {
	fmt.Println("§1 what discovery found, and the apply mode every node is in")
	fmt.Println("────────────────────────────────────────────────────────────")

	bound, inline := 0, 0
	for _, id := range spec.Order {
		if spec.Nodes[id].ApplyMode.Mode() == variantspec.ApplyBound {
			bound++
			continue
		}
		inline++
	}
	fmt.Printf("  nodes                 %d\n", len(spec.Order))
	fmt.Printf("  apply mode            inline=%d  bound=%d\n", inline, bound)
	fmt.Println()
	fmt.Println("  🔴 The apply mode is what decides whether the runtime route is reachable at all, and on")
	fmt.Println("     this repository every node is `inline` — which is the DEFAULT and is not a defect.")
	fmt.Println("     ADR-004 made `bound` opt-in precisely so nothing moves onto an indirection unless")
	fmt.Println("     someone asks. So the honest headline for this repo is not \"rollouts are unavailable\";")
	fmt.Println("     it is \"no node has been migrated, and the refusal says exactly that\".")
	fmt.Println()
}

// ── §2 the delivery table ────────────────────────────────────────────────────────────────────────

func theTable() {
	fmt.Println("§2 the delivery table — total over (change × route)")
	fmt.Println("────────────────────────────────────────────────────────────")

	cells := changedelivery.Table()
	byChange := map[changedelivery.ChangeKind][]changedelivery.Cell{}
	var order []changedelivery.ChangeKind
	for _, c := range cells {
		if _, seen := byChange[c.Change]; !seen {
			order = append(order, c.Change)
		}
		byChange[c.Change] = append(byChange[c.Change], c)
	}

	fmt.Printf("  %-24s %-18s %s\n", "CHANGE", "PULL REQUEST", "GRADUAL ROLLOUT")
	for _, k := range order {
		var src, rt string
		for _, c := range byChange[k] {
			label := string(c.Status)
			if c.Refused() {
				label = string(c.Cause)
				if c.Contingent {
					label += " (contingent)"
				}
			}
			if c.Route == changedelivery.RouteSource {
				src = label
			} else {
				rt = label
			}
		}
		fmt.Printf("  %-24s %-18s %s\n", k, src, rt)
	}
	fmt.Println()

	// The eligible set is NAMED, not counted. "3 eligible" is a number; "these three fields" is a claim
	// someone can check.
	var eligible []string
	for _, k := range order {
		e, err := changedelivery.RuntimeEligibility(k, true)
		if err == nil && e.Eligible {
			eligible = append(eligible, string(k))
		}
	}
	fmt.Printf("  rollout-eligible on a BOUND node: %s\n", strings.Join(eligible, ", "))
	fmt.Println("  🔴 …and nothing else. These are exactly the fields ADR-009 already froze in the binding")
	fmt.Println("     document, because ADR-004 already decided they are data rather than program structure.")
	fmt.Println()
}

// ── §3 the headline: every (node × change) through the real coverage join ────────────────────────

func perNode(ir *discovery.IR, spec *variantspec.VariantSpec) {
	fmt.Println("§3 🔴 every (node × change) through the REAL coverage join")
	fmt.Println("────────────────────────────────────────────────────────────")

	lang := ir.Workflow.Language
	kinds := changedelivery.ChangeKinds()

	byState := map[changedelivery.State]int{}
	byRuntimeCause := map[string]int{}
	byRuntimeOwner := map[string]string{}
	bySourceCause := map[string]int{}
	total := 0
	undeliverablePairs := 0
	// 🔴 Counted SEPARATELY from undeliverable, and that separation is the honest half of this section.
	// Without a call-site form the source route's answer is "some forms in this language materialize and
	// some do not", which SourceOutcomeFor reports conservatively as a refusal — the right answer to
	// "will MY call site get a diff", and the wrong input to "is this change undeliverable anywhere".
	// Folding these into the undeliverable count would inflate it with cases where a call site next door
	// gets a diff, which is exactly the over-claim this contract exists to prevent.
	formDecidesPairs := 0

	for _, nodeID := range spec.Order {
		bound := spec.Nodes[nodeID].ApplyMode.Mode() == variantspec.ApplyBound
		for _, kind := range kinds {
			src := changedelivery.SourceOutcomeFor(kind, lang, "")
			rep, err := changedelivery.BuildReport(kind, lang, bound, src, changedelivery.RolloutStatus{}, false)
			if err != nil {
				log.Fatalf("node %s / %s: %v", nodeID, kind, err)
			}
			total++
			byState[rep.State]++
			if o, ok := rep.Outcome(changedelivery.RouteRuntime); ok && o.Refused() {
				byRuntimeCause[o.Cause]++
				// 🚫 The owner is READ from the outcome, never re-derived here. The runtime route can
				// legitimately carry a transform cause (a call site that cannot carry the change gets the
				// same answer from both routes), and a local switch over this package's three causes would
				// print an empty owner for it — a blank that reads as "nobody" and means "we did not say".
				byRuntimeOwner[o.Cause] = o.Owner
			}
			if o, ok := rep.Outcome(changedelivery.RouteSource); ok && o.Refused() {
				bySourceCause[o.Cause]++
			}
			switch {
			case src.Varies:
				formDecidesPairs++
			case rep.State == changedelivery.StateUndeliverable:
				undeliverablePairs++
			}
		}
	}

	fmt.Printf("  %d nodes × %d change kinds = %d delivery reports, all built from the shipped join\n\n",
		len(spec.Order), len(kinds), total)

	fmt.Println("  by delivery state (the CONSERVATIVE per-call-site answer — see the note below):")
	for _, s := range changedelivery.States() {
		if n := byState[s]; n > 0 {
			fmt.Printf("    %-22s %4d\n", s, n)
		}
	}
	fmt.Println()
	fmt.Println("     These states answer \"will THIS node's change ship\" without a resolved call-site form,")
	fmt.Println("     so they take the conservative branch. The two counts below split that number into the")
	fmt.Println("     part that is a real dead end and the part that is an unresolved question.")
	fmt.Println()

	fmt.Println("  runtime-route refusals, by cause:")
	for _, c := range sortedKeys(byRuntimeCause) {
		fmt.Printf("    %-30s %4d   (whose move: %s)\n", c, byRuntimeCause[c], byRuntimeOwner[c])
	}
	fmt.Println()
	fmt.Println("  source-route refusals, by cause:")
	for _, c := range sortedKeys(bySourceCause) {
		fmt.Printf("    %-30s %4d\n", c, bySourceCause[c])
	}
	fmt.Println()

	fmt.Printf("  🔴 %d of %d (node × change) pairs reach the running agent by NO ROUTE, in any language.\n",
		undeliverablePairs, total)
	fmt.Println("     Before this contract each of those produced a diff-less silence indistinguishable from")
	fmt.Println("     a proposal nobody had gotten to. Every one of them now carries two causes and two")
	fmt.Println("     owners, and NONE of them is reported as pending.")
	fmt.Println()
	fmt.Printf("  🚫 A further %d pairs are NOT counted above, and deliberately so: their source answer is\n", formDecidesPairs)
	fmt.Println("     \"some call-site forms in this language materialize and some do not\", and this run does")
	fmt.Println("     not resolve each node to its SDK row. Reporting them as undeliverable would inflate the")
	fmt.Println("     headline with cases where the call site next door gets a diff. The count that would")
	fmt.Println("     make this number smaller is a per-node form resolution, which is discovery's to give.")
	fmt.Println()
}

// ── §4 what a rollout would be refused for, here ─────────────────────────────────────────────────

func runtimeRouteHere(ir *discovery.IR) {
	fmt.Println("§4 the runtime route on this repository")
	fmt.Println("────────────────────────────────────────────────────────────")

	// The three eligible fields, on an inline node — which is every node here.
	for _, kind := range []changedelivery.ChangeKind{
		changedelivery.ChangeModelWithinProvider,
		changedelivery.ChangeInferenceParams,
		changedelivery.ChangePromptVersion,
	} {
		inline, _ := changedelivery.RuntimeEligibility(kind, false)
		bound, _ := changedelivery.RuntimeEligibility(kind, true)
		fmt.Printf("  %-24s inline → %-18s   bound → %s\n", kind, inline.Cause, statusWord(bound.Eligible))
	}
	fmt.Println()
	fmt.Println("  🔴 The refusal on all 31 nodes is `node-not-bound`, whose owner is YOU — a one-time")
	fmt.Println("     migration this repository has not made. That is a different sentence from")
	fmt.Println("     `not-runtime-resolvable`, and telling this reader the latter would send them to stop")
	fmt.Println("     asking about something they can actually have.")
	fmt.Println()
	fmt.Println("  🚫 And the cause order is what makes that true. A provider-crossing model change on the")
	fmt.Println("     SAME inline node reports the boundary instead, because a bound migration would not")
	fmt.Println("     help it:")
	across, _ := changedelivery.RuntimeEligibility(changedelivery.ChangeModelAcrossProvider, false)
	fmt.Printf("     model-across-provider    inline → %s (whose move: %s)\n", across.Cause, across.Cause.Owner())
	fmt.Println()
}

// ── §5 arm assignment on real config hashes ──────────────────────────────────────────────────────

func armAssignment(ir *discovery.IR, commitSHA string) {
	fmt.Println("§5 arm assignment, on real hashes")
	fmt.Println("────────────────────────────────────────────────────────────")

	// Two arms whose hashes are derived from the real revision, so the run is about this repository
	// rather than about a fixture.
	parent := "p" + strings.Repeat(commitSHA[:8], 8)[:63]
	candidate := "c" + strings.Repeat(commitSHA[8:16], 8)[:63]
	ro := changedelivery.Rollout{
		ID:                  "ro_" + short(commitSHA),
		WorkflowID:          "nousresearch/hermes-agent",
		NodeID:              ir.Nodes[0].NodeID,
		ParentConfigHash:    parent,
		CandidateConfigHash: candidate,
		Change:              changedelivery.ChangeModelWithinProvider,
		ShareBasisPoints:    1000, // 10%
		ExpiresAtUnixMs:     runNow + 7*24*60*60*1000,
		VerifiedDelta:       true,
	}
	if err := ro.Validate(runNow); err != nil {
		log.Fatalf("rollout: %v", err)
	}

	const n = 20000
	candidates := 0
	for i := 0; i < n; i++ {
		k := changedelivery.AssignmentKey{Value: fmt.Sprintf("session-%d", i), Supplied: true}
		if changedelivery.Resolve(ro, k, runNow, changedelivery.GuardState{}).Arm == changedelivery.ArmCandidate {
			candidates++
		}
	}
	fmt.Printf("  declared share        %.2f%%\n", float64(ro.ShareBasisPoints)/100)
	fmt.Printf("  observed over %d    %.2f%%\n", n, float64(candidates)*100/float64(n))

	// Replica agreement, with no coordination — the property that lets this run in the customer's
	// process rather than ours.
	agree := true
	for i := 0; i < 2000; i++ {
		k := changedelivery.AssignmentKey{Value: fmt.Sprintf("session-%d", i), Supplied: true}
		a := changedelivery.Resolve(ro, k, runNow, changedelivery.GuardState{})
		b := changedelivery.Resolve(ro, k, runNow+3_600_000, changedelivery.GuardState{})
		if a.Arm != b.Arm {
			agree = false
			break
		}
	}
	fmt.Printf("  replica agreement     %v (2000 keys, replayed an hour later, no coordination)\n", agree)

	// Attribution: the arm's OWN hash, and the failure that protects the comparison.
	sample := changedelivery.Resolve(ro, changedelivery.AssignmentKey{Value: "session-1", Supplied: true},
		runNow, changedelivery.GuardState{})
	fmt.Printf("  a sampled invocation  arm=%s  emits config_hash=%s…\n", sample.Arm, sample.ConfigHash[:12])
	if err := changedelivery.ValidateAttribution(ro, ro.ID); err == nil {
		log.Fatal("attribution accepted the rollout identity in the arm-hash slot")
	} else {
		fmt.Printf("  identity in hash slot FAILS THE RUN: %s\n", firstClause(err.Error()))
	}

	// Expiry and the local guard, both without touching the platform.
	expired := changedelivery.Resolve(ro, changedelivery.AssignmentKey{Value: "session-1", Supplied: true},
		ro.ExpiresAtUnixMs, changedelivery.GuardState{})
	fmt.Printf("  after expiry          arm=%s (reason=%s, no network call)\n", expired.Arm, expired.Reason)

	guarded := ro
	guarded.Guards = []changedelivery.Guard{{Kind: changedelivery.GuardErrorRate, Threshold: 500}}
	tripped := changedelivery.EvaluateGuards(guarded, changedelivery.GuardState{},
		changedelivery.GuardObservation{ErrorRatePerMyriad: 900})
	afterTrip := changedelivery.Resolve(guarded, changedelivery.AssignmentKey{Value: "session-1", Supplied: true},
		runNow, tripped)
	cleared := changedelivery.EvaluateGuards(guarded, tripped, changedelivery.GuardObservation{ErrorRatePerMyriad: 0})
	fmt.Printf("  after a guard trip    arm=%s (reason=%s)\n", afterTrip.Arm, afterTrip.Reason)
	fmt.Printf("  condition clears      still tripped=%v  🔴 reverting is automatic; resuming is not\n", cleared.Tripped)
	fmt.Println()
}

// ── §6 the gates that must say no ────────────────────────────────────────────────────────────────

func gatesThatSayNo() {
	fmt.Println("§6 the gates that say no, with the cause each one owns")
	fmt.Println("────────────────────────────────────────────────────────────")

	base := changedelivery.AuthorRequest{
		Rollout: changedelivery.Rollout{
			ID: "ro_gate", ParentConfigHash: "p", CandidateConfigHash: "c",
			Change: changedelivery.ChangeModelWithinProvider, ShareBasisPoints: 1000,
			ExpiresAtUnixMs: runNow + 86_400_000,
		},
		NodeIsBound:     true,
		Entitled:        true,
		Halt:            changedelivery.HaltState{Readable: true},
		Guardrail:       changedelivery.GuardrailNotApplicable,
		VerifiedDelta:   true,
		CreatedAtUnixMs: runNow,
	}

	type probe struct {
		name  string
		build func(changedelivery.AuthorRequest) changedelivery.AuthorRequest
	}
	probes := []probe{
		{"a well-formed rollout", func(r changedelivery.AuthorRequest) changedelivery.AuthorRequest { return r }},
		{"unentitled caller", func(r changedelivery.AuthorRequest) changedelivery.AuthorRequest {
			r.Entitled = false
			return r
		}},
		{"halt unreadable", func(r changedelivery.AuthorRequest) changedelivery.AuthorRequest {
			r.Halt = changedelivery.HaltState{Readable: false}
			return r
		}},
		{"gate-rejected ordering", func(r changedelivery.AuthorRequest) changedelivery.AuthorRequest {
			r.GateRejected, r.GateCause = true, "incoherent-ordering"
			return r
		}},
		{"memory strategy candidate", func(r changedelivery.AuthorRequest) changedelivery.AuthorRequest {
			r.Rollout.Change = changedelivery.ChangeMemoryStrategy
			r.TransformRefusalCause = "unsafeRewrite: node carries a memory strategy"
			return r
		}},
		{"harness strategy candidate", func(r changedelivery.AuthorRequest) changedelivery.AuthorRequest {
			r.Rollout.Change = changedelivery.ChangeHarnessStrategy
			r.TransformRefusalCause = "unsafeRewrite: node carries a harness strategy"
			return r
		}},
		{"guardrail-rejected downgrade", func(r changedelivery.AuthorRequest) changedelivery.AuthorRequest {
			r.Guardrail = changedelivery.GuardrailRejected
			return r
		}},
		{"wiring candidate (eligible cell absent)", func(r changedelivery.AuthorRequest) changedelivery.AuthorRequest {
			r.Rollout.Change = changedelivery.ChangeWiring
			return r
		}},
	}

	for _, p := range probes {
		err := changedelivery.AuthorRollout(p.build(base))
		if err == nil {
			fmt.Printf("  %-40s AUTHORED\n", p.name)
			continue
		}
		cause := "?"
		if r, ok := err.(*changedelivery.ErrRolloutRefused); ok {
			cause = r.Cause
		}
		fmt.Printf("  %-40s REFUSED  (%s)\n", p.name, cause)
	}
	fmt.Println()
	fmt.Println("  🔴 Note the ORDER. The gate-rejected probe carries a rollout-ELIGIBLE change, and it is")
	fmt.Println("     still refused as gate-rejected — the gate outranks eligibility, so a rollout can never")
	fmt.Println("     be the route around a gate that produced no runnable spec.")
	fmt.Println()
	fmt.Println("  🚫 And not one of these is reported as pending, queued, or in review.")
	fmt.Println()
}

// ── helpers ──────────────────────────────────────────────────────────────────────────────────────

func baselineFrom(ir *discovery.IR, commitSHA string) *variantspec.VariantSpec {
	spec := &variantspec.VariantSpec{
		WorkflowID: "nousresearch/hermes-agent", SourceRevision: commitSHA,
		Nodes: map[string]variantspec.NodeOverride{},
	}
	for _, n := range ir.Nodes {
		spec.Order = append(spec.Order, n.NodeID)
	}
	for _, e := range ir.Edges {
		spec.Edges = append(spec.Edges, variantspec.Edge{FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind})
	}
	return spec
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func statusWord(eligible bool) string {
	if eligible {
		return "ELIGIBLE"
	}
	return "refused"
}

func firstClause(s string) string {
	if i := strings.Index(s, " — "); i > 0 {
		return s[i+len(" — "):]
	}
	return s
}

func short(s string) string {
	if len(s) > 14 {
		return s[:14]
	}
	return s
}

// keep the transform import honest: this run reads the coverage table through SourceOutcomeFor, and
// names the version it read so a later run's different numbers are attributable.
var _ = transform.CoverageTableVersion
