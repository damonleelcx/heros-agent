package herosagent

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/providercall"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// graphrun.go is P36 §4: executing a definition that is a GRAPH rather than a single call.
//
// # 🔴 The property everything here exists to protect
//
// A PINNED RESULT MUST NOT DEPEND ON INTERLEAVING (task 4.5). D2's guarantee is that the same three
// inputs always show you the same graph, and it is a property of the STORE — but only if what the store
// receives is a function of the inputs alone. The moment two nodes may overlap, "what the store
// receives" acquires a second input nobody declared: the scheduler.
//
// So the discipline is one sentence: **concurrency changes WHEN a node runs, never WHERE its output
// lands.** Every node's contribution is collected into a slot indexed by the ORDERING, and the merge
// walks the ordering. Nothing is appended in completion order, anywhere, ever — an append is the one
// operation that records the scheduler into the result.
//
// # Why merge is not "the last writer"
//
// `variantspec.MergeStrategy` is a closed set of two, and both members are TOTAL and DETERMINISTIC.
// That was a selection criterion rather than a coincidence: under concurrency, which node is "first" is
// a property of the machine, so a spec whose meaning depends on scheduling cannot be scored, because
// two runs of one configuration would not agree.

// AssessmentBinding is the definition ONE assessment runs under — resolved once, at the start, and
// carried as a VALUE.
//
// # 🔴 This is PRD §14 Q4's answer made structural (decisions.md D-36.4)
//
// An in-flight assessment finishes under the definition it started with. Not because a rule says so but
// because there is no read of "the active definition" anywhere inside a run that could return a
// different answer: the definition is a value the run holds. A run cannot pick up a new definition
// because it never asks again.
//
// The alternative — reading the active definition per node — produces a report with two configurations
// in it and no honest way to label it: half its findings came from one agent and half from another, and
// `config_hash` would be a lie whichever value it took.
type AssessmentBinding struct {
	// ConfigHash is the definition's identity, and it is what the pin is filed under.
	ConfigHash string
	// Definition is the resolved definition. It MAY be empty — see BindHash.
	Definition Definition
}

// BindDefinition binds an assessment to a resolved definition and its hash.
func BindDefinition(configHash string, d Definition) AssessmentBinding {
	return AssessmentBinding{ConfigHash: configHash, Definition: d}
}

// BindHash binds an assessment to a hash with NO definition behind it.
//
// 🔴 This is the CUSTOMER-SIDE runner's binding and nothing else. It receives a
// `runlink.AgentDefinition` — one prompt, one model, by contract — and never a node list, because the
// producing node is operator-side only (decisions.md D-36.2) and a node id has no business on that
// wire.
//
// 🚫 The result carries NO per-node record, and that is the honest value rather than a gap: nobody
// observed which node produced those edges on that machine. NULL means NOT RECORDED. A stamped
// `heros_analyst` would be this platform asserting a provenance it did not witness.
func BindHash(configHash string) AssessmentBinding {
	return AssessmentBinding{ConfigHash: configHash}
}

// Graph reports whether this binding carries a definition the runner must walk as a graph.
func (b AssessmentBinding) Graph() bool { return b.Definition.MultiNode() }

// ── Predicates: a CLOSED set, because nothing in this product evaluates `expr` at run time ───────
//
// # 🔴 Why a closed set rather than an expression evaluator
//
// A customer's predicate is evaluated by the customer's own transformed program — we are not the data
// path, which is exactly what ADR-001 settles. For the platform's own agent WE are the executor, so
// something here has to answer "did this edge hold".
//
// Writing an expression evaluator would be a SECOND grammar. `variantspec` already validates a
// predicate as an `expr` binding against the producing call site's recorded in-scope symbols, and the
// whole force of that is that there is ONE grammar and ONE scope validator. So instead of a second
// grammar, the agent's call sites RECORD A CLOSED IN-SCOPE SET — the observable facts of one HEROS node
// run — and `variantspec`'s existing check refuses anything outside it AT PUBLISH.
//
// The result is a configuration table rather than a parser: every predicate a reader can write is one of
// these, each is a boolean fact about the producing node, and there is no arithmetic to get wrong.

// AgentPredicate is one routable fact about a node's run.
type AgentPredicate struct {
	// Symbol is what an operator writes on the edge. It is what `AgentIR` records as in scope, so
	// anything else is refused at publish by the same check that governs a prompt slot's `expr`.
	Symbol string
	// Meaning is the sentence the console renders beside the choice. A predicate whose meaning an
	// operator has to infer is a route nobody can review.
	Meaning string
	// Holds answers the fact for one node's run.
	Holds func(NodeOutput) bool
}

// AgentPredicates is the closed set, in the order a console renders them.
func AgentPredicates() []AgentPredicate {
	return []AgentPredicate{
		{Symbol: "produced_edges",
			Meaning: "this node returned at least one edge that passed validation and the confidence floor",
			Holds:   func(o NodeOutput) bool { return len(o.Edges) > 0 }},
		{Symbol: "produced_labels",
			Meaning: "this node returned at least one region label that passed validation",
			Holds:   func(o NodeOutput) bool { return len(o.Labels) > 0 }},
		{Symbol: "produced_narrative",
			Meaning: "this node wrote prose. Narrative is ASSESSED rather than measured, so routing on " +
				"it routes on whether the agent had anything to say",
			Holds: func(o NodeOutput) bool { return strings.TrimSpace(o.Narrative) != "" }},
		{Symbol: "abstained",
			Meaning: "this node declined at least one subject. Not knowing is an OUTPUT, so it is " +
				"routable — a critic that runs only when the analyst declined is the obvious use",
			Holds: func(o NodeOutput) bool { return len(o.Abstentions) > 0 }},
		{Symbol: "failed",
			Meaning: "this node's provider call did not complete. 🚫 Routing on it does NOT make the " +
				"failure disappear: the node is still recorded as failed and the assessment still says so",
			Holds: func(o NodeOutput) bool { return o.Failed }},
	}
}

// AgentPredicateSymbols is the closed set as `AgentIR` records it, sorted.
func AgentPredicateSymbols() []string {
	out := make([]string, 0, len(AgentPredicates()))
	for _, p := range AgentPredicates() {
		out = append(out, p.Symbol)
	}
	sort.Strings(out)
	return out
}

// predicateHolds evaluates one predicate against a node's output.
//
// 🔴 An UNKNOWN symbol does not hold, and it is a defect rather than a route. Publish refuses anything
// outside the closed set, so reaching here means a definition was stored under an older vocabulary — and
// the safe reading of "I do not know what this means" is "do not take this edge", because taking it
// would run a node the author did not ask for and spend for it.
func predicateHolds(symbol string, o NodeOutput) (bool, bool) {
	for _, p := range AgentPredicates() {
		if p.Symbol == symbol {
			return p.Holds(o), true
		}
	}
	return false, false
}

// NodeOutput is what ONE node produced, before any merge.
type NodeOutput struct {
	NodeID      string
	Edges       []ProvenancedEdge
	Labels      []patternclassifier.RegionProposal
	Abstentions []Abstention
	Narrative   string
	Usage       providercall.Usage
	Failed      bool
	Cause       string
	Skipped     bool
	SkipReason  string
	LatencyMS   int64
	Calls       int
}

// run is this node's row in the stored per-node record.
func (o NodeOutput) run() NodeRun {
	return NodeRun{
		NodeID: o.NodeID, ProviderCalls: o.Calls,
		TokensIn: o.Usage.InputTokens, TokensOut: o.Usage.OutputTokens,
		LatencyMS: o.LatencyMS,
		Edges:     len(o.Edges), Labels: len(o.Labels), Abstentions: len(o.Abstentions),
		Failed: o.Failed, Cause: o.Cause, Skipped: o.Skipped, SkipReason: o.SkipReason,
	}
}

// mergeOutputs combines the nodes' contributions into one result, IN THE DEFINITION'S ORDERING.
//
// # 🔴 The ordering, not completion order, and not the group's node list
//
// `Order` is the replay sequence. Walking it means the merge is a pure function of (definition,
// per-node outputs) — so two runs that overlapped differently produce byte-identical bytes, which is
// what task 4.5 asks for and what `TestARepeatedPinnedInferenceUnderConcurrencyIsByteIdentical` runs
// repeatedly to catch.
//
// # Why a namespaced merge still yields a flat edge list
//
// `namespaced` and `all-fields` differ in how they handle a COLLISION, and an edge is not a field: two
// nodes proposing the same edge are two votes for one fact, not two values for one key. So the strategy
// governs the LABEL and NARRATIVE composition, and edges are unioned with the higher confidence kept —
// deterministically, by (from, to, kind), never by arrival.
func mergeOutputs(d Definition, outputs map[string]NodeOutput) (
	[]ProvenancedEdge, []patternclassifier.RegionProposal, []Abstention, string, []NodeRun) {

	edges := []ProvenancedEdge{}
	labels := []patternclassifier.RegionProposal{}
	abstentions := []Abstention{}
	runs := []NodeRun{}
	var narratives []string

	// byEdge keeps the highest-confidence proposal for one (from, to, kind). 🔴 Ties are broken by the
	// NODE'S POSITION IN THE ORDERING, never by which arrived first — the tie is exactly where a
	// scheduler would otherwise leak into the result.
	byEdge := map[string]ProvenancedEdge{}
	position := map[string]int{}
	for i, id := range d.Ordering() {
		position[id] = i
	}

	for _, id := range d.Ordering() {
		o, ran := outputs[id]
		if !ran {
			continue
		}
		runs = append(runs, o.run())
		if o.Skipped || o.Failed {
			// 🚫 A skipped or failed node contributes NOTHING and is still RECORDED. An empty
			// contribution silently folded in would read as "this node found nothing", which is a
			// finding about the customer's workflow rather than about our run.
			abstentions = append(abstentions, o.Abstentions...)
			continue
		}
		for _, e := range o.Edges {
			key := e.From + "\x00" + e.To + "\x00" + e.Kind
			prev, seen := byEdge[key]
			switch {
			case !seen:
				byEdge[key] = e
			case e.Confidence > prev.Confidence:
				byEdge[key] = e
			case e.Confidence == prev.Confidence &&
				position[e.ProducedByNode] < position[prev.ProducedByNode]:
				byEdge[key] = e
			}
		}
		labels = append(labels, o.Labels...)
		abstentions = append(abstentions, o.Abstentions...)
		if s := strings.TrimSpace(o.Narrative); s != "" {
			narratives = append(narratives, s)
		}
	}

	// Emit the edges in a DETERMINISTIC order derived from their content, not from the map.
	keys := make([]string, 0, len(byEdge))
	for k := range byEdge {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		edges = append(edges, byEdge[k])
	}
	return edges, labels, abstentions, strings.Join(narratives, "\n\n"), runs
}

// concurrentGroups indexes which nodes may overlap, by node id.
//
// 🔴 It answers "may this node overlap with its group" and NOT "run these in parallel". Whether the
// runner actually overlaps them is an execution choice; whether the RESULT may depend on that is not,
// and the merge above already settles it. So this exists to bound the parallelism, not to define the
// semantics.
func concurrentGroups(d Definition) map[string]int {
	out := map[string]int{}
	for i, g := range d.GraphGroups {
		if !g.Concurrent {
			continue
		}
		for _, n := range g.Nodes {
			out[n] = i
		}
	}
	return out
}

// incomingPredicates indexes the predicate edges INTO each node.
func incomingPredicates(d Definition) map[string][]variantspec.Edge {
	out := map[string][]variantspec.Edge{}
	for _, e := range d.Edges {
		if e.Kind != variantspec.EdgeKindPredicate {
			continue
		}
		out[e.ToNodeID] = append(out[e.ToNodeID], e)
	}
	return out
}

// shouldEnter answers whether a node is entered, given what has run so far.
//
// A node with NO incoming predicate edge is always entered — an unconditional edge is unconditional, and
// the ordering is what sequences it. A node with one or more is entered when ANY of them holds: two
// predicate edges into one node are two routes to it, and requiring all of them would make a second
// route a restriction, which is the opposite of what an author wrote.
func shouldEnter(node string, preds map[string][]variantspec.Edge, outputs map[string]NodeOutput) (bool, string) {
	in := preds[node]
	if len(in) == 0 {
		return true, ""
	}
	var why []string
	for _, e := range in {
		producer, ran := outputs[e.FromNodeID]
		if !ran {
			// The producer was itself skipped, so its facts do not exist. 🚫 An unrun producer's
			// predicate is NOT read as false-because-empty: it is read as "this route was never
			// available", which is what the skip reason says.
			why = append(why, fmt.Sprintf("%s did not run, so %q could not be evaluated",
				e.FromNodeID, e.Predicate))
			continue
		}
		held, known := predicateHolds(e.Predicate, producer)
		if !known {
			why = append(why, fmt.Sprintf("%q is not a predicate this build knows, so the edge from %s "+
				"was not taken — an unknown route is not taken rather than guessed",
				e.Predicate, e.FromNodeID))
			continue
		}
		if held {
			return true, ""
		}
		why = append(why, fmt.Sprintf("%q did not hold after %s", e.Predicate, e.FromNodeID))
	}
	return false, strings.Join(why, "; ")
}

// freshGraph runs a graph WITHOUT reading or writing the store — the re-inference path.
//
// 🔴 It shares `runNode` and `mergeOutputs` with `inferGraph`, so the answer a diff is computed against
// is produced by the same execution the pin was. A second walk here would make the diff a comparison
// between two implementations rather than between two runs.
func (r *Runner) freshGraph(ctx context.Context, in Input, binding AssessmentBinding) (Result, error) {
	d := binding.Definition
	if r.nodeModel == nil {
		err := fmt.Errorf("%w: this definition declares %d nodes and this runner resolves no per-node "+
			"model, so a re-inference would run a configuration the config_hash does not name",
			ErrInvalidDefinition, len(d.Nodes))
		return Result{Code: CodeProviderFailed, Cause: err.Error()}, err
	}
	preds := incomingPredicates(d)
	outputs := map[string]NodeOutput{}
	res := Result{Code: CodeOK}
	var usage providercall.Usage

	for _, id := range d.Ordering() {
		node, ok := d.NodeByID(id)
		if !ok {
			continue
		}
		if enter, why := shouldEnter(id, preds, outputs); !enter {
			outputs[id] = NodeOutput{NodeID: id, Skipped: true, SkipReason: why}
			continue
		}
		out := r.runNode(ctx, in, node)
		outputs[id] = out
		res.ProviderCalls += out.Calls
		usage.InputTokens += out.Usage.InputTokens
		usage.OutputTokens += out.Usage.OutputTokens
		if out.Failed {
			return Result{Code: CodeProviderFailed, ProviderCalls: res.ProviderCalls, Usage: usage,
					Cause: fmt.Sprintf("node %q failed during re-inference: %s", id, out.Cause)},
				fmt.Errorf("herosagent: node %q failed during re-inference: %s", id, out.Cause)
		}
	}
	edges, labels, abstentions, narrative, _ := mergeOutputs(d, outputs)
	res.Edges, res.Labels, res.Abstentions, res.Narrative = edges, labels, abstentions, narrative
	res.Usage = usage
	return res, nil
}
