// Command patterngraph serves the P3.5 pattern-classified graph view against REAL classifier output
// (task 7.4 / 8.8: "render the composite fixture; confirm label + confidence, rule-vs-llm
// distinction, and the empty state").
//
// It exists because those three things cannot be verified by asserting on markup — a test that greps
// the HTML for "not yet classified" proves the string I wrote is the string I wrote. What has to be
// checked is that a real browser, fed a real classification through the real API, renders a
// rule-sourced label, an llm-sourced label, and an unclassified region as three VISIBLY different
// things, and that the annotation stays legible on a large composite IR.
//
// So this stands the whole path up: IR -> classifier -> graph view -> HTTP -> page. Every label on
// screen came out of internal/patternclassifier, not out of a fixture file someone hand-wrote to
// look right.
//
// Not a shipped service: a demo harness. The "llm" workflow uses a canned model (see cannedModel),
// which is why it lives HERE and not in the package — nothing shipped may answer with canned labels.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"

	"github.com/heros-foreal/agentd/internal/api"
	"github.com/heros-foreal/agentd/internal/config"
	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8479", "listen address")
	flag.Parse()

	src := &memSource{views: map[string]patternclassifier.GraphView{}}
	for _, w := range workflows() {
		view, err := w.classify()
		if err != nil {
			log.Fatalf("%s: %v", w.id, err)
		}
		src.views[w.id] = view
	}

	s := api.New(nil, config.Config{})
	s.MountP35(src)
	fmt.Printf("p3.5 graph:\n")
	for _, w := range workflows() {
		fmt.Printf("  %-22s /app/workflows/%s/graph\n     %s\n", w.id, url.PathEscape(w.id), w.what)
	}
	fmt.Printf("  %-22s /app/workflows/nope/graph\n     404: distinct from an unclassified workflow\n", "no-such-workflow")
	log.Fatal(http.ListenAndServe(*addr, s.Handler))
}

type memSource struct {
	views map[string]patternclassifier.GraphView
}

func (m *memSource) GraphView(id string) (patternclassifier.GraphView, bool) {
	v, ok := m.views[id]
	return v, ok
}

type demo struct {
	id       string
	what     string
	ir       *discovery.IR
	skills   []string
	roles    map[string]patternclassifier.SkillRole
	fallback patternclassifier.FallbackModel
}

func (d demo) classify() (patternclassifier.GraphView, error) {
	opts := patternclassifier.Options{
		Skills:     patternclassifier.NewStaticSkillResolver(d.skills...),
		SkillRoles: d.roles,
		Fallback:   d.fallback,
		FallbackConfig: patternclassifier.FallbackConfig{
			Model: "demo-canned-classifier", Seed: 1, Temperature: 0,
		},
	}
	res, err := patternclassifier.Classify(context.Background(), d.ir, opts)
	if err != nil {
		return patternclassifier.GraphView{}, err
	}
	// Write the labels back to the IR and build the view from the WRITTEN document, so the page shows
	// what a consumer would actually read out of a stored IR — not an in-memory result that never
	// survived a round trip.
	labelled, err := patternclassifier.WriteBack(d.ir, res)
	if err != nil {
		return patternclassifier.GraphView{}, err
	}
	return patternclassifier.BuildGraphView(labelled, res), nil
}

// cannedModel answers with one fixed in-taxonomy label plus one deliberately BOGUS one, so the demo
// exercises the rejection path visibly: the page must show the good label and a diagnostic for the
// dropped one. A demo that only ever showed the happy path would not prove the guard runs.
type cannedModel struct{}

func (cannedModel) ClassifySubgraph(_ context.Context, _ patternclassifier.FallbackRequest) ([]patternclassifier.RawLabel, error) {
	c := func(v float64) *float64 { return &v }
	return []patternclassifier.RawLabel{
		{Pattern: "guardrails_safety", Confidence: c(0.58)},
		{Pattern: "self_healing_swarm", Confidence: c(0.99)}, // out of taxonomy: must be rejected + diagnosed
	}, nil
}

func workflows() []demo {
	return []demo{
		{
			id:     "composite-rule-only",
			what:   "Routing on one subgraph, RAG on another, Tool Use co-existing on nodes. 0 LLM calls.",
			ir:     compositeIR(),
			skills: []string{"embed_query", "cross_encoder_rerank", "issue_lookup"},
			roles: map[string]patternclassifier.SkillRole{
				"embed_query": patternclassifier.SkillRoleEmbedding, "cross_encoder_rerank": patternclassifier.SkillRoleRerank,
			},
		},
		{
			id:       "ambiguous-llm-fallback",
			what:     "No structural signature: the constrained fallback labels it. Rule vs LLM must look different.",
			ir:       ambiguousIR(),
			fallback: cannedModel{},
		},
		{
			id:   "unclassified-empty-state",
			what: "No signature and NO model: must read 'not yet classified', never a blank or a default.",
			ir:   ambiguousIR(),
		},
		{
			id:     "large-composite",
			what:   "A big multi-region IR — the legibility check (task 7.4).",
			ir:     largeCompositeIR(),
			skills: []string{"embed_query", "cross_encoder_rerank", "issue_lookup", "search_kb", "ticket_write"},
			roles: map[string]patternclassifier.SkillRole{
				"embed_query": patternclassifier.SkillRoleEmbedding, "cross_encoder_rerank": patternclassifier.SkillRoleRerank,
			},
		},
	}
}

// --- IR construction ------------------------------------------------------------------------

func node(id string, mut ...func(*discovery.IRNode)) discovery.IRNode {
	n := discovery.IRNode{
		NodeID: id, Kind: "static_definition",
		CallSite:            discovery.IRCallSite{File: "app/pipeline.py", Symbol: id, LineStart: 1, LineEnd: 2},
		Model:               discovery.IRModel{Provider: "openai", ModelID: "gpt-4o-mini", Params: map[string]any{"temperature": 0}},
		Prompt:              discovery.IRPrompt{Inline: "do the thing", Variables: []string{}},
		ToolsSkills:         []string{},
		ContextAssembly:     discovery.IRContextAssembly{Policy: "single_message", Description: "one user message"},
		IOContract:          discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}},
		InvocationSemantics: discovery.IRInvocationSem{Type: "single", VariableAtRuntime: false},
	}
	for _, m := range mut {
		m(&n)
	}
	return n
}

func prompt(s string) func(*discovery.IRNode) {
	return func(n *discovery.IRNode) { n.Prompt.Inline = s }
}
func tools(ts ...string) func(*discovery.IRNode) {
	return func(n *discovery.IRNode) { n.ToolsSkills = ts }
}
func policy(p string) func(*discovery.IRNode) {
	return func(n *discovery.IRNode) { n.ContextAssembly = discovery.IRContextAssembly{Policy: p, Description: p} }
}
func conditional() func(*discovery.IRNode) {
	return func(n *discovery.IRNode) { n.InvocationSemantics = discovery.IRInvocationSem{Type: "conditional"} }
}
func loop() func(*discovery.IRNode) {
	return func(n *discovery.IRNode) {
		n.InvocationSemantics = discovery.IRInvocationSem{Type: "loop", VariableAtRuntime: true}
	}
}
func model(id string) func(*discovery.IRNode) {
	return func(n *discovery.IRNode) { n.Model.ModelID = id }
}

func data(a, b string) discovery.IREdge {
	return discovery.IREdge{FromNodeID: a, ToNodeID: b, Kind: "data"}
}
func control(a, b string) discovery.IREdge {
	return discovery.IREdge{FromNodeID: a, ToNodeID: b, Kind: "control"}
}

func ir(id string, nodes []discovery.IRNode, edges []discovery.IREdge) *discovery.IR {
	return &discovery.IR{
		IRVersion: discovery.IRVersion,
		Workflow: discovery.IRWorkflow{
			ID: id, Repo: discovery.IRRepo{URL: "local://demo", CommitSHA: "9f1c2ab"}, Language: "python",
		},
		Nodes: nodes, Edges: edges,
	}
}

func compositeIR() *discovery.IR {
	return ir("composite-rule-only",
		[]discovery.IRNode{
			node("n_router", prompt("classify the ticket")),
			node("n_billing", prompt("handle billing"), conditional()),
			node("n_tech", prompt("handle tech support"), conditional(), tools("issue_lookup")),
			node("n_embed", tools("embed_query")),
			node("n_retrieve", policy("rag-retrieval")),
			node("n_rerank", tools("cross_encoder_rerank")),
			node("n_answer", prompt("answer using the passages")),
		},
		[]discovery.IREdge{
			control("n_router", "n_billing"), control("n_router", "n_tech"),
			data("n_embed", "n_retrieve"), data("n_retrieve", "n_rerank"), data("n_rerank", "n_answer"),
		})
}

func ambiguousIR() *discovery.IR {
	return ir("ambiguous",
		[]discovery.IRNode{
			node("n_guard", prompt("check the request against policy")),
			node("n_solo", prompt("answer"), conditional()),
		},
		[]discovery.IREdge{control("n_guard", "n_solo")})
}

// largeCompositeIR is the legibility case (task 7.4): five regions, sixteen nodes, every structural
// pattern present at once — a router, a parallel fan-out/merge, a prompt chain, a retrieval pipeline,
// a reflection loop, a multi-agent dispatch, and a model-tier branch.
func largeCompositeIR() *discovery.IR {
	shared := policy("full-history")
	same := prompt("answer the question")
	return ir("large-composite",
		[]discovery.IRNode{
			node("n_router", prompt("classify the ticket")),
			node("n_billing", prompt("handle billing"), conditional(), tools("search_kb")),
			node("n_tech", prompt("handle tech support"), conditional(), tools("issue_lookup")),

			node("n_split", prompt("split the work")),
			node("n_left", prompt("draft section A")),
			node("n_right", prompt("draft section B")),
			node("n_merge", prompt("merge the drafts")),

			node("n_extract", prompt("extract entities")),
			node("n_summarize", prompt("summarize")),
			node("n_draft", prompt("draft the reply")),

			node("n_embed", tools("embed_query")),
			node("n_retrieve", policy("rag-retrieval")),
			node("n_rerank", tools("cross_encoder_rerank")),
			node("n_answer", prompt("answer using the passages")),

			node("n_generate", prompt("write"), loop()),
			node("n_critique", prompt("critique"), loop()),

			node("n_manager", prompt("assign the work"), shared),
			node("n_researcher", prompt("research the topic"), shared),
			node("n_writer", prompt("write the report"), shared),

			node("n_triage", prompt("estimate complexity")),
			node("n_cheap", same, model("claude-haiku-4-5"), conditional()),
			node("n_strong", same, model("claude-opus-4-8"), conditional()),
		},
		[]discovery.IREdge{
			control("n_router", "n_billing"), control("n_router", "n_tech"),
			data("n_split", "n_left"), data("n_split", "n_right"), data("n_left", "n_merge"), data("n_right", "n_merge"),
			data("n_extract", "n_summarize"), data("n_summarize", "n_draft"),
			data("n_embed", "n_retrieve"), data("n_retrieve", "n_rerank"), data("n_rerank", "n_answer"),
			data("n_generate", "n_critique"), data("n_critique", "n_generate"),
			control("n_manager", "n_researcher"), control("n_manager", "n_writer"),
			control("n_triage", "n_cheap"), control("n_triage", "n_strong"),
		})
}
