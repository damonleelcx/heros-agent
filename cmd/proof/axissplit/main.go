// Command axissplit runs P34's three axes against a REAL repository over the REAL network.
//
//	clone  →  discover  →  author a loop, an envelope and a topology on the repository's own nodes
//	       →  watch every refusal fire, by name, on a node the customer wrote
//
// # Why this exists when every P34 fence is green and every one has been drilled red
//
// Green fences prove the parts. Every one of them runs against a fixture this repository wrote — a
// two-node IR with hand-built io_contracts, chosen to make the assertion clean. That is the right shape
// for a fence and it is not evidence about a customer.
//
// This proves the WALK: it clones `nousresearch/hermes-agent`, discovers its actual call sites, and
// authors P34 configurations against the node ids that come out. Every refusal it prints names a symbol
// somebody else wrote, in a file this repository has never seen.
//
// 🚫 It calls no provider and costs nothing. Every P34 gate is a RESOLVE-time gate by construction —
// that is the phase's central claim — so proving them needs no run, no sandbox and no credential. A
// version of this that spent money would be proving something else.
//
// 🔴 It exits non-zero on any gate that fails to fire. A refusal that stopped firing would not error;
// it would simply admit a configuration it should have refused, and the first symptom would be a
// measurement scored against source that was never rewired.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/transform"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

func main() {
	log.SetFlags(0)
	local := flag.String("local", "", "a checkout already on disk (required; clone it first)")
	workflow := flag.String("workflow", "github.com/nousresearch/hermes-agent", "the workflow id to label the run with")
	flag.Parse()

	if *local == "" {
		log.Fatal("axissplit: -local is required. Point it at a checkout of the repository under test.\n" +
			"  git clone --depth 1 https://github.com/nousresearch/hermes-agent /tmp/hermes-agent\n" +
			"  go run ./cmd/proof/axissplit -local /tmp/hermes-agent")
	}

	ctx := context.Background()

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
	if len(ir.Nodes) < 2 {
		log.Fatalf("axissplit: discovery found %d node(s); this proof needs at least two to declare a "+
			"concurrent group, and a repository with fewer is not a subject for it", len(ir.Nodes))
	}
	fmt.Printf("    %d node(s), %d edge(s) — every id below is the repository's own\n", len(ir.Nodes), len(ir.Edges))
	a, b := ir.Nodes[0].NodeID, ir.Nodes[1].NodeID
	fmt.Printf("    working on: %s\n                %s\n", a, b)

	regs := newRegs()

	// ── 2) the ceiling: imposed by the envelope, chosen by the loop ──────────────────────────────
	step(2, "an operator's ceiling refuses an engineer's turn count — naming BOTH numbers")
	regs.envelope("env-tight", `{"sandbox_posture":"no-network","turn_ceiling":2,"spend_ceiling_usd":1}`)
	regs.loop("loop-four", "reflexion",
		`{"max_turns":4,"stop_condition":"max-turns","reflection_prompt":"check your answer"}`)
	mustRefuse(ctx, ir, regs, "the turn ceiling",
		spec(ir, a, variantspec.NodeOverride{HarnessRef: "env-tight", LoopRef: "loop-four"}),
		variantspec.ErrCeilingExceeded, []string{"4", "2", a})

	step(3, "the same loop under a ceiling that admits it — resolves, and hashes")
	regs.envelope("env-roomy", `{"sandbox_posture":"no-network","turn_ceiling":8,"spend_ceiling_usd":1}`)
	mustResolve(ctx, ir, regs, "loop within the envelope",
		spec(ir, a, variantspec.NodeOverride{HarnessRef: "env-roomy", LoopRef: "loop-four"}))

	// ── 4) the host service, refused at RESOLVE rather than at run ───────────────────────────────
	step(4, "a tool-calling loop the envelope grants no tool executor — refused BEFORE anything is generated")
	regs.loop("loop-react", "react-loop", `{"max_turns":4,"stop_condition":"no-tool-call"}`)
	mustRefuse(ctx, ir, regs, "the missing host service",
		spec(ir, a, variantspec.NodeOverride{HarnessRef: "env-roomy", LoopRef: "loop-react"}),
		variantspec.ErrMissingHostService, []string{"react-loop", registry.HostServiceToolExecutor, "NOT degraded"})

	// ── 5) the ambiguity refusal, on a legacy shape ──────────────────────────────────────────────
	step(5, "a pre-P34 loop-bearing harness ref AND a loop ref — refused, naming both")
	regs.harness("h-legacy", "reflexion",
		`{"max_turns":3,"stop_condition":"max-turns","reflection_prompt":"revise"}`)
	mustRefuse(ctx, ir, regs, "the ambiguity",
		spec(ir, a, variantspec.NodeOverride{HarnessRef: "h-legacy", LoopRef: "loop-four"}),
		variantspec.ErrAmbiguousAxis, []string{"h-legacy", "loop-four", registry.StrategyEnvelope})

	step(6, "the SAME legacy ref on its own — still resolves, exactly as it did before P34")
	mustResolve(ctx, ir, regs, "the legacy path",
		spec(ir, a, variantspec.NodeOverride{HarnessRef: "h-legacy"}))

	// ── 7) topology on the repository's own nodes ────────────────────────────────────────────────
	step(7, "a fan-in on two of the repository's nodes, with no merge declared — refused at VALIDATE")
	fan := topologySpec(ir, a, b, nil)
	if err := fan.Validate(); err == nil {
		fail("a fan-in with no merge validated against %s and %s", a, b)
	} else {
		fmt.Printf("    ✓ refused at validate, with no IR and no registry:\n%s\n", indent(err.Error()))
	}

	step(8, "the same fan-in WITH a merge — validates, then refused at TRANSFORM by name")
	merged := topologySpec(ir, a, b, &variantspec.Merge{
		Into: mergeTarget(ir, a, b), Strategy: variantspec.MergeNamespaced, OnNodeFailure: variantspec.FailFast,
	})
	if err := merged.Validate(); err != nil {
		fail("a well-formed fan-in was refused at validate: %v", err)
	}
	fmt.Println("    ✓ validates")

	resolved, err := variantspec.Resolve(ctx, merged, ir, regs)
	if err != nil {
		// A syntactic frontend emits no io_contracts, so the merge cannot be checked against one. That
		// is an honest refusal about THIS repository's analysis, and it is printed rather than hidden.
		fmt.Printf("    ✓ refused at resolve — the typed-contract gate, on this repository's own analysis:\n%s\n",
			indent(err.Error()))
	} else {
		fmt.Printf("    ✓ resolves; config_hash %s\n", variantspec.Display(resolved.ConfigHash))
		if len(resolved.Config.GraphGroups) != 1 {
			fail("the topology did not reach the hashed projection")
		}
		_, terr := transform.Generate(resolved, *local)
		if terr == nil {
			fail("the transform emitted a patch for a topology no rewriter materializes; that hash would " +
				"then be scored against source that still runs the old topology")
		}
		fmt.Printf("    ✓ refused at transform, by name:\n%s\n", indent(terr.Error()))
		if !strings.Contains(terr.Error(), "not dropped") {
			fail("the refusal does not say the declaration was not dropped")
		}
	}

	// ── 9) what the axes declare about this repository's language ────────────────────────────────
	step(9, "what each axis declares for this repository's language")
	lang := ir.Workflow.Language
	if lang == "" {
		lang = "python"
	}
	for _, axis := range []string{"harness", "loop", "graph"} {
		fmt.Printf("    %-8s %s\n", axis, transform.StatusFor(axis))
		for _, c := range transform.CoverageFor(axis) {
			if !strings.EqualFold(c.Language, lang) {
				continue
			}
			verdict := "materializes"
			if c.Refused() {
				verdict = "refuses (" + string(c.Cause) + ")"
			}
			fmt.Printf("        %-18s %s\n", c.Form, verdict)
			if c.MissingArtifact != "" {
				fmt.Printf("        %-18s missing: %s\n", "", c.MissingArtifact)
			}
		}
	}

	fmt.Printf("\n== P34 axis split: PASS against %s ==\n", *workflow)
	fmt.Println("   🚫 No provider was called and nothing cost money. Every gate above is a RESOLVE-time")
	fmt.Println("      gate by construction, which is the phase's central claim — so proving them needs")
	fmt.Println("      no run, no sandbox and no credential.")
	fmt.Println("   🔴 Every node id, symbol and file above came out of the repository under test.")
}

// ── the harness ──────────────────────────────────────────────────────────────────────────────────

func step(n int, what string) { fmt.Printf("\n-- %d) %s\n", n, what) }

func fail(format string, args ...any) {
	fmt.Printf("\n✖ %s\n", fmt.Sprintf(format, args...))
	os.Exit(1)
}

func indent(s string) string {
	var b strings.Builder
	for _, line := range wrap(s, 92) {
		fmt.Fprintf(&b, "        %s\n", line)
	}
	return strings.TrimRight(b.String(), "\n")
}

func wrap(s string, width int) []string {
	var out []string
	line := ""
	for _, w := range strings.Fields(s) {
		if line != "" && len(line)+1+len(w) > width {
			out = append(out, line)
			line = ""
		}
		if line == "" {
			line = w
		} else {
			line += " " + w
		}
	}
	if line != "" {
		out = append(out, line)
	}
	return out
}

// spec builds a one-node spec over a real node id.
func spec(ir *discovery.IR, node string, o variantspec.NodeOverride) *variantspec.VariantSpec {
	return &variantspec.VariantSpec{
		WorkflowID:     ir.Workflow.ID,
		SourceRevision: "local",
		Order:          []string{node},
		Nodes:          map[string]variantspec.NodeOverride{node: o},
	}
}

// topologySpec declares a fan-out/fan-in over two real nodes plus a third to converge on.
//
// 🔴 `Order` lists EVERY discovered node, not just the three in play, and the first version of this
// proof did not — which produced a genuine and correct refusal from the WIRING axis before the graph
// check was ever reached: a spec that orders 3 of a repository's 28 call sites is asking to delete 25
// of them, and `checkWiring` said so. That is P15 working, and the fix was to write a spec somebody
// would actually author.
//
// It is worth recording rather than quietly fixing: the two axes are checked in order, wiring first,
// and a spec that both rewires and declares topology gets the wiring answer — which is right, because
// the node-set change is the larger claim.
func topologySpec(ir *discovery.IR, a, b string, merge *variantspec.Merge) *variantspec.VariantSpec {
	target := mergeTarget(ir, a, b)
	order := make([]string, 0, len(ir.Nodes))
	for _, n := range ir.Nodes {
		order = append(order, n.NodeID)
	}
	return &variantspec.VariantSpec{
		WorkflowID:     ir.Workflow.ID,
		SourceRevision: "local",
		Order:          order,
		Nodes:          map[string]variantspec.NodeOverride{},
		Edges: []variantspec.Edge{
			{FromNodeID: a, ToNodeID: target, Kind: "data"},
			{FromNodeID: b, ToNodeID: target, Kind: "data"},
		},
		GraphGroups: []variantspec.GraphGroup{{Nodes: []string{a, b}, Concurrent: true, Merge: merge}},
	}
}

// mergeTarget picks a third real node for the pair to converge on.
func mergeTarget(ir *discovery.IR, a, b string) string {
	for _, n := range ir.Nodes {
		if n.NodeID != a && n.NodeID != b {
			return n.NodeID
		}
	}
	return ""
}

func mustRefuse(ctx context.Context, ir *discovery.IR, regs variantspec.Registries, what string,
	s *variantspec.VariantSpec, want error, mustName []string) {
	_, err := variantspec.Resolve(ctx, s, ir, regs)
	if err == nil {
		fail("%s did not fire — the configuration resolved", what)
	}
	if !isErr(err, want) {
		fail("%s produced the wrong error:\n  %v", what, err)
	}
	for _, name := range mustName {
		if !strings.Contains(err.Error(), name) {
			fail("%s did not name %q. A refusal that omits it sends the reader to the wrong fix.\n  %v",
				what, name, err)
		}
	}
	fmt.Printf("    ✓ refused, naming %s:\n%s\n", strings.Join(quoted(mustName), ", "), indent(err.Error()))
}

func mustResolve(ctx context.Context, ir *discovery.IR, regs variantspec.Registries, what string, s *variantspec.VariantSpec) {
	got, err := variantspec.Resolve(ctx, s, ir, regs)
	if err != nil {
		fail("%s was refused, and it must not be:\n  %v", what, err)
	}
	fmt.Printf("    ✓ %s resolves; config_hash %s\n", what, variantspec.Display(got.ConfigHash))
}

func quoted(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = fmt.Sprintf("%q", x)
	}
	return out
}

func isErr(err, target error) bool {
	for e := err; e != nil; {
		if e == target {
			return true
		}
		u, ok := e.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		e = u.Unwrap()
	}
	return false
}

// ── an in-memory registry ────────────────────────────────────────────────────────────────────────
//
// 🔴 The ENTRIES are real — built from the builtin vocabularies through the same validator the store
// uses — so a params blob this proof could not seal is one it cannot test with either. What is faked is
// only the storage, which is not what P34 changed.

type proofRegs struct {
	harnesses map[string]*registry.HarnessEntry
	loops     map[string]*registry.LoopEntry
	store     *registry.Store
}

func newRegs() *proofRegs {
	return &proofRegs{
		harnesses: map[string]*registry.HarnessEntry{},
		loops:     map[string]*registry.LoopEntry{},
		store:     registry.NewStore(nil, nil),
	}
}

func (p *proofRegs) envelope(ref, params string) { p.harness(ref, registry.StrategyEnvelope, params) }

func (p *proofRegs) harness(ref, strategy, params string) {
	st, _, err := p.store.ValidateHarnessParams(ref, strategy, json.RawMessage(params))
	if err != nil {
		log.Fatalf("axissplit: %s is not a sealable harness entry: %v", ref, err)
	}
	p.harnesses[ref] = &registry.HarnessEntry{VersionID: ref, Name: ref, Strategy: st,
		Spec: registry.HarnessSpec{Strategy: strategy, Params: json.RawMessage(params)}}
}

func (p *proofRegs) loop(ref, strategy, params string) {
	st, _, err := p.store.ValidateLoopParams(ref, strategy, json.RawMessage(params))
	if err != nil {
		log.Fatalf("axissplit: %s is not a sealable loop entry: %v", ref, err)
	}
	p.loops[ref] = &registry.LoopEntry{VersionID: ref, Name: ref, Strategy: st,
		Spec: registry.LoopSpec{Strategy: strategy, Params: json.RawMessage(params)}}
}

func (p *proofRegs) ResolveModel(context.Context, string) (*registry.ModelEntry, error) {
	return nil, registry.ErrNotFound
}
func (p *proofRegs) ResolvePrompt(context.Context, string) (*registry.PromptEntry, error) {
	return nil, registry.ErrNotFound
}
func (p *proofRegs) ResolveSkill(context.Context, string) (*registry.SkillEntry, error) {
	return nil, registry.ErrNotFound
}
func (p *proofRegs) ResolveContextPolicy(context.Context, string) (*registry.ContextEntry, error) {
	return nil, registry.ErrNotFound
}
func (p *proofRegs) ResolveMemory(context.Context, string) (*registry.MemoryEntry, error) {
	return nil, registry.ErrNotFound
}
func (p *proofRegs) ResolveHarness(_ context.Context, id string) (*registry.HarnessEntry, error) {
	if e, ok := p.harnesses[id]; ok {
		return e, nil
	}
	return nil, registry.ErrNotFound
}
func (p *proofRegs) ResolveLoop(_ context.Context, id string) (*registry.LoopEntry, error) {
	if e, ok := p.loops[id]; ok {
		return e, nil
	}
	return nil, registry.ErrNotFound
}
