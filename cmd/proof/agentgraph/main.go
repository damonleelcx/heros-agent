// Command agentgraph runs P36 — the platform's own agent as a GRAPH — against a REAL repository.
//
//	discover  →  author a multi-node HEROS definition  →  publish it through every gate
//	          →  WALK it over the repository's own call sites  →  read the per-node attribution back
//
// # Why this exists when every P36 fence is green and every one has been drilled red
//
// Green fences prove the parts. Every one runs against a definition this repository wrote, over a
// hand-built IR chosen to make the assertion clean. That is the right shape for a fence and it is not
// evidence about a customer.
//
// This proves the WALK. It discovers `nousresearch/hermes-agent`'s actual call sites and runs a
// three-node agent over them, so every node id in the per-node attribution below is a symbol somebody
// else wrote, in a file this repository has never seen. It is the first fan-out, conditional edge and
// merge the graph axis has ever been exercised against outside a fixture — which is exactly what
// design D1 said routing the agent through the customer's validator would buy.
//
// # 🚫 What is NOT real here, said plainly
//
// The MODEL. This proof calls no provider and costs nothing: the agent's nodes answer from a
// deterministic local stub, and every claim below is about the SHAPE of the run — which node was
// entered, which was routed around, what each contributed, and whether two runs agree byte for byte.
// Nothing here is evidence about a model's quality, and a version of this that spent money would be
// proving something else.
//
// Everything else is real: the repository, its discovered IR, the publish gates, the shared
// topology validator, the runner, the store and the pin.
//
// 🔴 It exits non-zero on any gate that fails to fire. A refusal that stopped firing would not error —
// it would admit a configuration it should have refused, and the first symptom would be a bill.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/providercall"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

func main() {
	log.SetFlags(0)
	local := flag.String("local", "", "a checkout already on disk (required; clone it first)")
	workflow := flag.String("workflow", "github.com/nousresearch/hermes-agent", "the workflow id to label the run with")
	flag.Parse()

	if *local == "" {
		log.Fatal("agentgraph: -local is required. Point it at a checkout of the repository under test.\n" +
			"  git clone --depth 1 https://github.com/nousresearch/hermes-agent /tmp/hermes-agent\n" +
			"  go run ./cmd/proof/agentgraph -local /tmp/hermes-agent")
	}
	ctx := context.Background()

	// ── 1) the repository's own call sites ───────────────────────────────────────────────────────
	step(1, "discover the repository this agent will be asked about")
	reg, err := discovery.DefaultRegistry()
	if err != nil {
		log.Fatalf("discovery registry: %v", err)
	}
	res, err := discovery.Run(discovery.Options{
		Repo: *local, Registry: reg, WorkflowID: *workflow, CommitSHA: "local"})
	if err != nil {
		log.Fatalf("discovery: %v", err)
	}
	ir := &res.IR
	if len(ir.Nodes) < 3 {
		fail("discovery found %d node(s); this proof needs at least three so the agent has a residue to "+
			"be asked about", len(ir.Nodes))
	}
	residue := herosagent.SelectResidue(ir, res.Report, nil)
	fmt.Printf("    %d call site(s), %d edge(s) — every id below is the repository's own\n",
		len(ir.Nodes), len(ir.Edges))
	fmt.Printf("    residue: %d pair(s) no frontend established — this is what the agent is asked about\n",
		len(residue.Pairs))
	if residue.Empty() {
		fail("the residue is empty, so the agent would be asked nothing and this proof would assert " +
			"that a run which never happened produced no attribution")
	}
	for i, p := range residue.Pairs {
		if i >= 3 {
			break
		}
		fmt.Printf("        %s  →  %s\n", p.From, p.To)
	}

	// ── 2) the single-node definition still hashes as it always did ──────────────────────────────
	step(2, "the default shape keeps its identity — the fence the whole phase was sized by")
	single := herosagent.SingleNode(herosagent.Node{
		PromptRef: "prompt-v1", ModelRef: "claude-opus-5", CredentialRef: "anthropic",
		ContextRef: "ctx-v1", HarnessRef: "harness-single-shot-v1",
	})
	wire, err := json.Marshal(single)
	if err != nil {
		log.Fatal(err)
	}
	singleHash, err := single.ConfigHash()
	if err != nil {
		log.Fatal(err)
	}
	const preP36Wire = `{"prompt_ref":"prompt-v1","model_ref":"claude-opus-5",` +
		`"credential_ref":"anthropic","context_ref":"ctx-v1","harness_ref":"harness-single-shot-v1"}`
	const preP36Hash = "0db6c67956dcc1bafe0fe1bf40db01acb13b93c854f341d4f9bd729c97cd1e34"
	if string(wire) != preP36Wire {
		fail("the single-node wire bytes moved.\n  was: %s\n  now: %s", preP36Wire, wire)
	}
	if singleHash != preP36Hash {
		fail("the single-node config_hash moved from %s to %s — every pin filed under it is orphaned",
			preP36Hash, singleHash)
	}
	fmt.Printf("    ✓ byte-identical to its pre-P36 form; config_hash %s\n", singleHash[:12])
	fmt.Println("    ✓ so no pinned inference is migrated and no stored definition is rewritten")

	// ── 3) a three-node agent, authored over the repository's own residue ────────────────────────
	step(3, "author a THREE-NODE agent: a triage, an analyst, and a critic behind a conditional edge")
	graph := herosagent.Definition{
		Nodes: []herosagent.Node{
			node("heros_triage", "prompt/triage@1", "claude-haiku-4-5", "anthropic"),
			node("heros_analyst", "prompt/residue@3", "claude-opus-5", "anthropic"),
			// 🔴 A DIFFERENT PROVIDER on the critic. Per-node credentials are the main reason to want a
			// graph — a cheap model triaging for an expensive one is usually two vendors — and a proof
			// where every node named one provider would render that column as decoration.
			node("heros_critic", "prompt/critic@2", "claude-sonnet-5", "openai"),
		},
		Order: []string{"heros_triage", "heros_analyst", "heros_critic"},
		Edges: []variantspec.Edge{
			{FromNodeID: "heros_triage", ToNodeID: "heros_analyst", Kind: "data"},
			// The critic runs only when the analyst DECLINED something. Not knowing is an output, so it
			// is routable — and this is the obvious use of a predicate.
			{FromNodeID: "heros_analyst", ToNodeID: "heros_critic",
				Kind: variantspec.EdgeKindPredicate, Predicate: "abstained"},
		},
		GraphGroups: []variantspec.GraphGroup{{
			Nodes: []string{"heros_triage", "heros_analyst"}, Concurrent: true,
		}},
	}
	graphHash, err := graph.ConfigHash()
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("    3 nodes, 2 providers, 1 concurrent group, 1 conditional edge — config_hash %s\n",
		graphHash[:12])

	// ── 4) every publish gate, in order ──────────────────────────────────────────────────────────
	pub, versions := publisher()

	step(4, "a fan-in with no merge — refused by the SAME validator a customer's spec goes through")
	noMerge := clone(graph)
	noMerge.Edges = append(noMerge.Edges,
		variantspec.Edge{FromNodeID: "heros_triage", ToNodeID: "heros_critic", Kind: "data"},
		variantspec.Edge{FromNodeID: "heros_analyst", ToNodeID: "heros_critic", Kind: "data"})
	noMerge.GraphGroups = []variantspec.GraphGroup{{
		Nodes: []string{"heros_triage", "heros_analyst"}, Concurrent: true}}
	agentErr := mustRefusePublish(ctx, pub, noMerge, "the fan-in with no merge",
		[]string{"no merge is declared", "first-result-wins", "all-fields", "namespaced"})

	// 🔴 THE SAME DECLARATION, THROUGH THE CUSTOMER'S PATH, MUST PRODUCE THE SAME SENTENCE.
	//
	// This is the assertion design D1 is about, made against a real repository rather than a fixture.
	// "Both refuse" is what two independent validators do too; the same words are not a coincidence.
	spec := noMerge.Spec()
	_, _, customerErr := variantspec.ValidateTopology(ctx, &spec, herosagent.AgentIR(noMerge), nil)
	if customerErr == nil {
		fail("the shared validator accepted a fan-in with no merge, so the refusal above came from " +
			"somewhere else")
	}
	if !strings.Contains(agentErr.Error(), customerErr.Error()) {
		fail("the agent's refusal is NOT the customer's.\n  agent:    %v\n  customer: %v",
			agentErr, customerErr)
	}
	fmt.Println("    ✓ the agent's refusal CONTAINS the customer's, verbatim — one validator, not two")

	step(5, "a predicate naming something no node reports — refused by the EXPRESSION path")
	badPred := clone(graph)
	badPred.Edges = []variantspec.Edge{
		{FromNodeID: "heros_triage", ToNodeID: "heros_analyst", Kind: "data"},
		{FromNodeID: "heros_analyst", ToNodeID: "heros_critic",
			Kind: variantspec.EdgeKindPredicate, Predicate: "analyst_had_a_hunch"},
	}
	mustRefusePublish(ctx, pub, badPred, "the predicate out of scope",
		[]string{"analyst_had_a_hunch", "in scope"})

	step(6, "a loop whose host service this deployment does not supply — refused at PUBLISH, not at run")
	withLoop := clone(graph)
	withLoop.Nodes[2].LoopRef = "loop/react@1"
	loopPub := pub.WithAxisRegistry(axisRegistry{})
	err = publishErr(ctx, loopPub, withLoop)
	if !errors.Is(err, herosagent.ErrHostServiceMissing) {
		fail("a react-loop published on a deployment with no tool executor: %v", err)
	}
	for _, want := range []string{"react-loop", "reflexion"} {
		if !strings.Contains(err.Error(), want) {
			fail("the loop refusal does not name %q:\n  %v", want, err)
		}
	}
	fmt.Printf("    ✓ refused at publish, by the operator who chose it:\n%s\n", indent(err.Error()))

	step(7, "the well-formed three-node agent — publishes, PENDING, serving nothing")
	pubRes, err := pub.Publish(ctx, graph)
	if err != nil {
		fail("the well-formed agent was refused: %v", err)
	}
	if !pubRes.Created {
		fail("publishing created no version")
	}
	if err := pub.Activate(ctx, pubRes.ConfigHash); !errors.Is(err, herosagent.ErrRehearsalNotPassed) {
		fail("a graph was activated without a rehearsal: %v", err)
	}
	fmt.Printf("    ✓ published %s and REFUSED activation — nothing this deployment publishes serves\n",
		pubRes.ConfigHash[:12])
	fmt.Println("      until it has been measured, and a graph is where somebody wants an exception")
	if err := versions.SetRehearsal(ctx, pubRes.ConfigHash, herosagent.RehearsalPassed, `{"passed":true}`); err != nil {
		log.Fatal(err)
	}
	if err := pub.Activate(ctx, pubRes.ConfigHash); err != nil {
		fail("a rehearsed graph was still refused: %v", err)
	}
	fmt.Println("    ✓ activated once measured")

	// ── 8) THE WALK — the agent runs over the repository's own call sites ────────────────────────
	step(8, "WALK the three-node agent over the repository's own residue")
	store := herosagent.NewMemInferenceStore()
	health := herosagent.NewNodeHealthRegistry(0)
	models := map[string]herosagent.Model{
		// The triage answers nothing and declines nothing.
		"heros_triage": &stub{},
		// The analyst proposes the first residue pair AND abstains on another — which is what makes
		// the conditional edge to the critic hold.
		"heros_analyst": &stub{propose: 1, abstain: true},
		"heros_critic":  &stub{propose: 1, offset: 1},
	}
	runner, err := herosagent.NewRunner(models["heros_triage"], store, herosagent.DefaultConfidenceFloor,
		func() int64 { return 0 },
		herosagent.WithNodeModels(func(n herosagent.Node) (herosagent.Model, error) {
			m, ok := models[n.NodeID]
			if !ok {
				return nil, fmt.Errorf("no model for node %q", n.NodeID)
			}
			return m, nil
		}),
		herosagent.WithNodeHealth(health.Observe))
	if err != nil {
		log.Fatal(err)
	}
	in := herosagent.Input{
		TenantID: "proof", WorkflowID: ir.Workflow.ID, SourceRevision: "local",
		RuleIR: ir, Residue: residue,
		Budget: herosagent.Budget{MaxTokens: 500_000, MaxWall: 60_000_000_000},
	}
	binding := herosagent.BindDefinition(pubRes.ConfigHash, graph)
	out, err := runner.Infer(ctx, in, binding, herosagent.PlacementPlatform)
	if err != nil {
		fail("the walk failed: %v", err)
	}
	fmt.Printf("    ✓ %d provider call(s) across 3 declared nodes, %d edge(s) produced\n",
		out.ProviderCalls, len(out.Edges))

	// ── 9) per-node attribution, on ids nobody here wrote ────────────────────────────────────────
	step(9, "read the per-node attribution back — every id is the repository's, every node is ours")
	if len(out.Nodes) != 3 {
		fail("the run recorded %d node(s) for a three-node definition", len(out.Nodes))
	}
	var entered, skipped int
	for _, n := range out.Nodes {
		switch {
		case n.Skipped:
			skipped++
			fmt.Printf("        %-14s SKIPPED — %s\n", n.NodeID, n.SkipReason)
		default:
			entered++
			fmt.Printf("        %-14s %d call, %d token(s), %d edge(s), %d abstention(s)\n",
				n.NodeID, n.ProviderCalls, n.TokensIn+n.TokensOut, n.Edges, n.Abstentions)
		}
	}
	if entered != 3 {
		fail("the conditional edge routed around a node it should have entered: the analyst abstained, "+
			"so `abstained` holds and the critic runs. %d entered, %d skipped", entered, skipped)
	}
	for _, e := range out.Edges {
		if e.ProducedByNode == "" {
			fail("edge %s→%s names no producing node — a finding an operator cannot resolve to a node",
				e.From, e.To)
		}
		if !nodeExists(ir, e.From) || !nodeExists(ir, e.To) {
			fail("edge %s→%s names something the repository's IR does not contain", e.From, e.To)
		}
		fmt.Printf("    ✓ %s → %s  produced by %s (both ids are the repository's own)\n",
			e.From, e.To, e.ProducedByNode)
	}

	// ── 10) the conditional edge takes the other branch ──────────────────────────────────────────
	step(10, "the SAME agent when the analyst declines nothing — the critic is skipped, by name")
	models["heros_analyst"] = &stub{propose: 1}
	store2 := herosagent.NewMemInferenceStore()
	runner2, err := herosagent.NewRunner(models["heros_triage"], store2,
		herosagent.DefaultConfidenceFloor, func() int64 { return 0 },
		herosagent.WithNodeModels(func(n herosagent.Node) (herosagent.Model, error) {
			return models[n.NodeID], nil
		}))
	if err != nil {
		log.Fatal(err)
	}
	out2, err := runner2.Infer(ctx, in, binding, herosagent.PlacementPlatform)
	if err != nil {
		fail("the second walk failed: %v", err)
	}
	var criticSkipped bool
	for _, n := range out2.Nodes {
		if n.NodeID == "heros_critic" && n.Skipped {
			criticSkipped = true
			fmt.Printf("    ✓ heros_critic SKIPPED: %s\n", n.SkipReason)
		}
	}
	if !criticSkipped {
		fail("the critic ran even though the analyst declined nothing — the predicate is not being read")
	}
	if out2.ProviderCalls != 2 {
		fail("the skipped branch still made %d call(s); a routed-around node must cost nothing",
			out2.ProviderCalls)
	}
	fmt.Println("    ✓ and it cost nothing: 2 calls, not 3")

	// ── 11) determinism under concurrency ────────────────────────────────────────────────────────
	step(11, "run the SAME pinned inference repeatedly — byte-identical, or the pin is a lie")
	var first string
	for i := 0; i < 20; i++ {
		s := herosagent.NewMemInferenceStore()
		r, rerr := herosagent.NewRunner(models["heros_triage"], s, herosagent.DefaultConfidenceFloor,
			func() int64 { return 0 },
			herosagent.WithNodeModels(func(n herosagent.Node) (herosagent.Model, error) {
				return models[n.NodeID], nil
			}))
		if rerr != nil {
			log.Fatal(rerr)
		}
		got, gerr := r.Infer(ctx, in, binding, herosagent.PlacementPlatform)
		if gerr != nil {
			fail("run %d failed: %v", i, gerr)
		}
		b, merr := json.Marshal(got.Edges)
		if merr != nil {
			log.Fatal(merr)
		}
		if i == 0 {
			first = string(b)
			continue
		}
		if string(b) != first {
			fail("run %d differs from run 0 — a pinned result that depends on interleaving means two "+
				"runs of ONE configuration do not agree\n  %s\n  %s", i, first, b)
		}
	}
	fmt.Println("    ✓ 20 runs, byte-identical — concurrency changes WHEN a node runs, never WHERE its")
	fmt.Println("      output lands")

	// ── 12) the pin, and rollback as one act ─────────────────────────────────────────────────────
	step(12, "the pin names its producing configuration; rollback is one act")
	stored, ok, err := store.Get(ctx, in.WorkflowID, in.SourceRevision, pubRes.ConfigHash)
	if err != nil || !ok {
		fail("the walk stored nothing under its own key: ok=%v err=%v", ok, err)
	}
	if stored.AgentConfigHash != pubRes.ConfigHash {
		fail("the pin names %q and was produced by %q", stored.AgentConfigHash, pubRes.ConfigHash)
	}
	fmt.Printf("    ✓ pinned under (%s, local, %s) with %d node record(s)\n",
		shorten(in.WorkflowID), pubRes.ConfigHash[:12], len(stored.Nodes))

	// Publish and activate a single-node definition, then roll BACK to the graph — one call, one hash.
	if _, err := pub.Publish(ctx, single); err != nil {
		fail("publishing the single-node definition: %v", err)
	}
	if err := versions.SetRehearsal(ctx, singleHash, herosagent.RehearsalPassed, `{"passed":true}`); err != nil {
		log.Fatal(err)
	}
	if err := pub.Activate(ctx, singleHash); err != nil {
		fail("activating the single-node definition: %v", err)
	}
	if err := pub.Rollback(ctx, pubRes.ConfigHash); err != nil {
		fail("rolling back to the graph: %v", err)
	}
	active, _, err := versions.Active(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if active.ConfigHash != pubRes.ConfigHash {
		fail("after rollback %s is serving, want %s", active.ConfigHash, pubRes.ConfigHash)
	}
	all, err := versions.List(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if len(all) != 2 {
		fail("the store holds %d versions after a rollback between two — a rollback that creates a "+
			"version is a re-authoring wearing a different name", len(all))
	}
	fmt.Println("    ✓ rolled back with a hash and nothing else; no version created, no shape re-authored")

	// ── 13) the pin survives, unread and un-re-run ───────────────────────────────────────────────
	step(13, "the definition changed twice and the pin did not move")
	after, ok, err := store.Get(ctx, in.WorkflowID, in.SourceRevision, pubRes.ConfigHash)
	if err != nil || !ok {
		fail("the pin is gone after two activations and a rollback: ok=%v err=%v", ok, err)
	}
	if len(after.Edges) != len(stored.Edges) {
		fail("the pin changed under two activations")
	}
	fmt.Println("    ✓ a configuration change is a PINNING EVENT, not a re-inference")

	// ── 14) per-node health, on the repository's run ─────────────────────────────────────────────
	step(14, "the per-node health endpoint, from the run above")
	doc := health.Document()
	for _, n := range doc.Nodes {
		fmt.Printf("        %-14s %d inference(s), %d skip(s), %d failure(s), mean %d ms, rate %.2f\n",
			n.NodeID, n.Inferences, n.Skips, n.Failures, n.LatencyMeanMS, n.FailureRate)
	}
	if len(doc.Nodes) != 3 {
		fail("the health document carries %d node(s)", len(doc.Nodes))
	}

	fmt.Printf("\n== P36 the agent is a graph: PASS against %s ==\n", *workflow)
	fmt.Println("   🔴 Every call-site id above came out of the repository under test. The agent's three")
	fmt.Println("      nodes walked them, one was routed around by a predicate that read what another")
	fmt.Println("      node reported, and every edge produced names the node that produced it.")
	fmt.Println("   🔴 The fan-in refusal is the CUSTOMER's sentence, from the customer's function.")
	fmt.Println("   🚫 No provider was called. The models are a deterministic local stub, and every claim")
	fmt.Println("      above is about the SHAPE of the run — not about a model's quality.")
	fmt.Println("   🚫 The agent proposed no change to itself, and cannot: proposalgen refuses a pass")
	fmt.Println("      whose subject is this platform's own agent before it reads any store.")
}

// ── the agent under test ─────────────────────────────────────────────────────────────────────────

func node(id, prompt, model, credential string) herosagent.Node {
	return herosagent.Node{
		NodeID: id, PromptRef: prompt, ModelRef: model, CredentialRef: credential,
		ContextRef: "ctx-v1", HarnessRef: "harness-envelope-v1",
	}
}

func clone(d herosagent.Definition) herosagent.Definition {
	out := d
	out.Nodes = append([]herosagent.Node(nil), d.Nodes...)
	out.Order = append([]string(nil), d.Order...)
	out.Edges = append([]variantspec.Edge(nil), d.Edges...)
	out.GraphGroups = append([]variantspec.GraphGroup(nil), d.GraphGroups...)
	return out
}

// stub answers deterministically from the residue it is given.
//
// 🚫 It reaches no provider. Every claim this proof makes is about the SHAPE of the run — which node
// was entered, which was routed around, what each contributed — and a stub is the honest way to hold
// the model constant while the topology varies. A version that called a real model would be measuring
// the model.
type stub struct {
	// propose is how many residue pairs to return as edges.
	propose int
	// offset is where in the residue to start, so two nodes propose different edges.
	offset int
	// abstain makes this node DECLINE a subject, which is what a downstream `abstained` predicate reads.
	abstain bool
}

func (s *stub) Infer(_ context.Context, in herosagent.Input) (herosagent.RawResult, providercall.Usage, error) {
	out := herosagent.RawResult{}
	conf := 0.95
	for i := s.offset; i < len(in.Residue.Pairs) && i < s.offset+s.propose; i++ {
		p := in.Residue.Pairs[i]
		out.Edges = append(out.Edges, herosagent.RawEdge{
			From: p.From, To: p.To, Kind: "data", Confidence: &conf})
	}
	if s.abstain && len(in.Residue.Pairs) > 0 {
		// 🔴 An abstention produced the way the runner records one: a proposal BELOW the floor. A stub
		// that returned an Abstention directly would be filling in the field this proof reads, which
		// asserts nothing about the validator that is supposed to produce it.
		low := 0.10
		last := in.Residue.Pairs[len(in.Residue.Pairs)-1]
		out.Edges = append(out.Edges, herosagent.RawEdge{
			From: last.From, To: last.To, Kind: "data", Confidence: &low})
	}
	return out, providercall.Usage{InputTokens: 100, OutputTokens: 20}, nil
}

// ── the deployment under test ────────────────────────────────────────────────────────────────────

func publisher() (*herosagent.Publisher, *herosagent.MemVersionStore) {
	store := herosagent.NewMemVersionStore()
	// 🔴 A MONOTONIC, NON-ZERO clock, and the reason is a real finding this proof turned up.
	//
	// The first version of this passed `func() int64 { return 0 }` — a constant, which is the ordinary
	// way to make a proof deterministic. Rollback then appeared to do nothing: the version was
	// activated and `Active()` reported nothing serving.
	//
	// `Version.Active()` is `ActivatedAtMS != 0`, so an activation stamped at epoch 0 records itself as
	// NOT ACTIVE. That is a latent inconsistency in the version store rather than in P36 — Postgres
	// selects the active row `WHERE activated_at_ms IS NOT NULL`, which a 0 satisfies, so the two
	// halves disagree about the same row — and it cannot fire in production, where the clock is
	// `time.Now().UnixMilli()`. It is reported rather than worked around silently, and this comment is
	// the record of why the clock here is not a constant.
	//
	// Monotonic rather than merely non-zero, so two activations are distinguishable in the store.
	tick := int64(1_700_000_000_000)
	p, err := herosagent.NewPublisher(catalogue{}, secrets{}, store, herosagent.RunnerHosts{},
		func() int64 { tick++; return tick })
	if err != nil {
		log.Fatal(err)
	}
	return p, store
}

type catalogue struct{}

func (catalogue) Models(context.Context) ([]herosagent.RegisteredModel, error) {
	return []herosagent.RegisteredModel{
		{ModelID: "claude-opus-5", Provider: "anthropic"},
		{ModelID: "claude-haiku-4-5", Provider: "anthropic"},
		{ModelID: "claude-sonnet-5", Provider: "openai"},
	}, nil
}

type secrets struct{}

func (secrets) Credential(_ context.Context, provider string) (providergateway.Credential, error) {
	switch provider {
	case "anthropic", "openai":
		// 🚫 NOT a key. This proof never calls a provider; the value exists so the publish gate can
		// prove the REFERENCE resolves, which is all that gate has ever done.
		return providergateway.Credential{APIKey: "proof-resolves-only"}, nil
	}
	return providergateway.Credential{}, fmt.Errorf("no secret for %q", provider)
}

func (secrets) Describe() providergateway.SourceInfo {
	return providergateway.SourceInfo{Kind: "proof", Detail: "resolves two provider names and nothing else"}
}

// axisRegistry resolves the one loop this proof binds: `react-loop`, which needs a tool executor this
// deployment does not supply.
type axisRegistry struct{}

func (axisRegistry) ResolveLoop(_ context.Context, id string) (*registry.LoopEntry, error) {
	if id == "loop/react@1" {
		return &registry.LoopEntry{VersionID: id,
			Spec: registry.LoopSpec{Strategy: "react-loop", Params: json.RawMessage(`{"max_turns":4}`)}}, nil
	}
	return nil, fmt.Errorf("no loop entry %q", id)
}

func (axisRegistry) ResolveHarness(_ context.Context, id string) (*registry.HarnessEntry, error) {
	return &registry.HarnessEntry{VersionID: id, Spec: registry.HarnessSpec{
		Strategy: registry.StrategyEnvelope,
		Params:   json.RawMessage(`{"sandbox_posture":"no-network","turn_ceiling":8,"spend_ceiling_usd":1}`),
	}}, nil
}

// ── the harness ──────────────────────────────────────────────────────────────────────────────────

func step(n int, what string) { fmt.Printf("\n-- %d) %s\n", n, what) }

func fail(format string, args ...any) {
	fmt.Printf("\n✗ %s\n", fmt.Sprintf(format, args...))
	os.Exit(1)
}

func publishErr(ctx context.Context, p *herosagent.Publisher, d herosagent.Definition) error {
	_, err := p.Publish(ctx, d)
	return err
}

func mustRefusePublish(ctx context.Context, p *herosagent.Publisher, d herosagent.Definition,
	what string, mustName []string) error {
	err := publishErr(ctx, p, d)
	if err == nil {
		fail("%s did not fire — the definition published", what)
	}
	for _, name := range mustName {
		if !strings.Contains(err.Error(), name) {
			fail("%s did not name %q. A refusal that omits it sends the reader to the wrong fix.\n  %v",
				what, name, err)
		}
	}
	fmt.Printf("    ✓ refused at publish, naming %s:\n%s\n",
		strings.Join(quoted(mustName), ", "), indent(err.Error()))
	return err
}

func quoted(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = fmt.Sprintf("%q", x)
	}
	return out
}

func nodeExists(ir *discovery.IR, id string) bool {
	for _, n := range ir.Nodes {
		if n.NodeID == id {
			return true
		}
	}
	return false
}

func shorten(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}

func indent(s string) string {
	var out []string
	for _, line := range wrap(s, 96) {
		out = append(out, "        "+line)
	}
	return strings.Join(out, "\n")
}

func wrap(s string, width int) []string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return nil
	}
	sort.SliceStable(words, func(i, j int) bool { return false }) // stable: preserve order
	var lines []string
	line := words[0]
	for _, w := range words[1:] {
		if len(line)+1+len(w) > width {
			lines = append(lines, line)
			line = w
			continue
		}
		line += " " + w
	}
	return append(lines, line)
}
