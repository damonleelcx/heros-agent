package patternclassifier

import "github.com/heros-foreal/agentd/internal/discovery"

// Test IR builders. Fixtures are built through these rather than hand-written JSON so that every
// fixture node carries the fields the FROZEN P0 schema requires — a fixture that does not map to the
// real schema proves nothing about the real classifier (QA: fixtures must mirror the real schema).

type nodeOpt func(*discovery.IRNode)

func withTools(tools ...string) nodeOpt {
	return func(n *discovery.IRNode) { n.ToolsSkills = tools }
}

func withPolicy(policy, desc string) nodeOpt {
	return func(n *discovery.IRNode) {
		n.ContextAssembly = discovery.IRContextAssembly{Policy: policy, Description: desc}
	}
}

func withModel(provider, modelID string) nodeOpt {
	return func(n *discovery.IRNode) { n.Model.Provider, n.Model.ModelID = provider, modelID }
}

func withSemantics(kind string, variable bool) nodeOpt {
	return func(n *discovery.IRNode) {
		n.InvocationSemantics = discovery.IRInvocationSem{Type: kind, VariableAtRuntime: variable}
	}
}

// No withParams / withSymbol option exists, deliberately: no detector reads `model.params` or
// `call_site.symbol` as a signal, so a fixture that varied them would be testing nothing. The set of
// builders here is meant to track the set of facts the detectors actually read.

func withPrompt(text string) nodeOpt {
	return func(n *discovery.IRNode) { n.Prompt.Inline = text }
}

// node builds a schema-complete static node definition with sane defaults.
func node(id string, opts ...nodeOpt) discovery.IRNode {
	n := discovery.IRNode{
		NodeID: id,
		Kind:   "static_definition",
		CallSite: discovery.IRCallSite{
			File: "app/pipeline.py", Symbol: id, LineStart: 1, LineEnd: 2,
		},
		Model:               discovery.IRModel{Provider: "openai", ModelID: "gpt-4o-mini", Params: map[string]any{"temperature": 0}},
		Prompt:              discovery.IRPrompt{Inline: "do the thing", Variables: []string{}},
		ToolsSkills:         []string{},
		ContextAssembly:     discovery.IRContextAssembly{Policy: "single_message", Description: "one user message"},
		IOContract:          discovery.IRIOContract{InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}},
		InvocationSemantics: discovery.IRInvocationSem{Type: "single", VariableAtRuntime: false},
	}
	for _, o := range opts {
		o(&n)
	}
	return n
}

// The fixtures simulate DISCOVERY OUTPUT, so their edges carry the author discovery.BuildIR stamps.
// A fixture that left it empty would be modelling a pre-P30 document, which is a different thing and is
// tested explicitly in p30_provenance_test.go.
func dataEdge(from, to string) discovery.IREdge {
	return discovery.IREdge{FromNodeID: from, ToNodeID: to, Kind: "data",
		Author: string(discovery.AuthorFrontend)}
}

func controlEdge(from, to string) discovery.IREdge {
	return discovery.IREdge{FromNodeID: from, ToNodeID: to, Kind: "control",
		Author: string(discovery.AuthorFrontend)}
}

func buildIR(nodes []discovery.IRNode, edges []discovery.IREdge) *discovery.IR {
	return &discovery.IR{
		IRVersion: discovery.IRVersion,
		Workflow: discovery.IRWorkflow{
			ID:       "fixture",
			Repo:     discovery.IRRepo{URL: "local://fixture", CommitSHA: "9f1c2ab"},
			Language: "python",
		},
		Nodes: nodes,
		Edges: edges,
	}
}
