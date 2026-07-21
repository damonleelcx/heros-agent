package patternclassifier

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// The HAND-LABELED FIXTURE SET — the baseline every detector is calibrated against (task 3.9 / 8.9).
//
// Each fixture states, by hand, which patterns the IR DOES implement (want) and which it must NOT be
// labeled with (mustNot). The near-miss fixtures carry an empty `want` and a populated `mustNot`:
// they are the discriminative-power evidence, because a detector that fires on everything has no
// discriminative power at all, and only a near-miss can show the difference.
type fixture struct {
	name string
	ir   *discovery.IR
	// skills is the registry snapshot this fixture is classified against.
	skills SkillResolver
	roles  map[string]SkillRole
	// want maps pattern → the node set it must be labeled against.
	want []wantLabel
	// mustNot lists patterns that must NOT appear anywhere in the result.
	mustNot []Pattern
	// note records what the fixture is evidence FOR, in words.
	note string
}

type wantLabel struct {
	pattern Pattern
	nodeIDs []string
	// candidate asserts the label is emitted as an unconfirmed structural candidate.
	candidate bool
}

func (f fixture) opts() Options {
	s := f.skills
	if s == nil {
		s = NewStaticSkillResolver()
	}
	return Options{Skills: s, SkillRoles: f.roles}
}

// ref returns the subgraph_ref a wantLabel should be found under: the content-addressed subgraph id
// for a region, or the node id itself for a node-scoped capability.
func (w wantLabel) ref() string {
	if w.pattern == ToolUse {
		return w.nodeIDs[0]
	}
	ids := append([]string(nil), w.nodeIDs...)
	sort.Strings(ids)
	return SubgraphIDFor(ids)
}

// --- individual signature fixtures (task 8.1: one IR per structural signature in isolation) ------

func fxPromptChaining() fixture {
	return fixture{
		name: "prompt_chaining/linear_three_node_data_chain",
		ir: buildIR(
			[]discovery.IRNode{node("n_extract"), node("n_summarize"), node("n_draft")},
			[]discovery.IREdge{dataEdge("n_extract", "n_summarize"), dataEdge("n_summarize", "n_draft")},
		),
		want: []wantLabel{{pattern: PromptChaining, nodeIDs: []string{"n_extract", "n_summarize", "n_draft"}}},
		// The near-miss that matters most: a chain must never read as a route.
		mustNot: []Pattern{Routing, Parallelization, ToolUse, Reflection},
		note:    "≥2 LLM nodes, linear data edges, no fan-out/fan-in/control/loop",
	}
}

func fxRouting() fixture {
	return fixture{
		name: "routing/conditional_control_fanout_to_specialists",
		ir: buildIR(
			[]discovery.IRNode{
				node("n_router", withPrompt("classify the ticket")),
				node("n_billing", withPrompt("handle billing"), withSemantics("conditional", false)),
				node("n_tech", withPrompt("handle tech support"), withSemantics("conditional", false)),
			},
			[]discovery.IREdge{controlEdge("n_router", "n_billing"), controlEdge("n_router", "n_tech")},
		),
		want:    []wantLabel{{pattern: Routing, nodeIDs: []string{"n_router", "n_billing", "n_tech"}}},
		mustNot: []Pattern{PromptChaining, Parallelization, ResourceAwareOptimization, MultiAgentCollaboration},
		note:    "control fan-out, ≥2 conditional targets, ≥2 distinct prompts (specialists)",
	}
}

func fxParallelization() fixture {
	return fixture{
		name: "parallelization/fanout_two_independent_then_merge",
		ir: buildIR(
			[]discovery.IRNode{node("n_split"), node("n_left"), node("n_right"), node("n_merge")},
			[]discovery.IREdge{
				dataEdge("n_split", "n_left"), dataEdge("n_split", "n_right"),
				dataEdge("n_left", "n_merge"), dataEdge("n_right", "n_merge"),
			},
		),
		want:    []wantLabel{{pattern: Parallelization, nodeIDs: []string{"n_split", "n_left", "n_right", "n_merge"}}},
		mustNot: []Pattern{Routing, PromptChaining, Reflection},
		note:    "data fan-out to ≥2 independent nodes reconverging at a merge",
	}
}

func fxReflection() fixture {
	return fixture{
		name: "reflection/critique_loops_back_to_generate",
		ir: buildIR(
			[]discovery.IRNode{
				node("n_generate", withSemantics("loop", true)),
				node("n_critique", withSemantics("loop", true)),
			},
			[]discovery.IREdge{dataEdge("n_generate", "n_critique"), dataEdge("n_critique", "n_generate")},
		),
		want:    []wantLabel{{pattern: Reflection, nodeIDs: []string{"n_generate", "n_critique"}, candidate: true}},
		mustNot: []Pattern{PromptChaining, Routing, Parallelization},
		note:    "a cycle back to a generate node — CANDIDATE only; iteration>1 is behavioral (P5)",
	}
}

func fxToolUse() fixture {
	return fixture{
		name:   "tool_use/node_bound_to_registered_skills",
		ir:     buildIR([]discovery.IRNode{node("n_agent", withTools("search_kb", "issue_lookup"))}, nil),
		skills: NewStaticSkillResolver("search_kb", "issue_lookup"),
		want:   []wantLabel{{pattern: ToolUse, nodeIDs: []string{"n_agent"}}},
		note:   "tools_skills non-empty AND resolvable in the Skill Registry",
	}
}

func fxMultiAgent() fixture {
	shared := withPolicy("full-history", "the whole shared transcript")
	return fixture{
		name: "multi_agent/manager_dispatches_roles_over_shared_context",
		ir: buildIR(
			[]discovery.IRNode{
				node("n_manager", withPrompt("assign the work"), shared),
				node("n_researcher", withPrompt("research the topic"), shared),
				node("n_writer", withPrompt("write the report"), shared),
			},
			[]discovery.IREdge{controlEdge("n_manager", "n_researcher"), controlEdge("n_manager", "n_writer")},
		),
		want:    []wantLabel{{pattern: MultiAgentCollaboration, nodeIDs: []string{"n_manager", "n_researcher", "n_writer"}}},
		mustNot: []Pattern{Routing, ResourceAwareOptimization},
		note:    "control dispatch to non-conditional role nodes sharing one context policy",
	}
}

func fxRAG() fixture {
	return fixture{
		name: "retrieval_rag/retriever_embed_rerank_into_generator",
		ir: buildIR(
			[]discovery.IRNode{
				node("n_embed", withTools("embed_query")),
				node("n_retrieve", withPolicy("rag-retrieval", "top-k over the KB index")),
				node("n_rerank", withTools("cross_encoder_rerank")),
				node("n_answer", withPrompt("answer using the passages")),
			},
			[]discovery.IREdge{
				dataEdge("n_embed", "n_retrieve"), dataEdge("n_retrieve", "n_rerank"), dataEdge("n_rerank", "n_answer"),
			},
		),
		skills: NewStaticSkillResolver("embed_query", "cross_encoder_rerank"),
		roles:  map[string]SkillRole{"embed_query": SkillRoleEmbedding, "cross_encoder_rerank": SkillRoleRerank},
		want: []wantLabel{
			{pattern: RetrievalRAG, nodeIDs: []string{"n_embed", "n_retrieve", "n_rerank", "n_answer"}},
			// The embed and rerank nodes are ALSO tool-bound: a capability co-exists on the node.
			{pattern: ToolUse, nodeIDs: []string{"n_embed"}},
			{pattern: ToolUse, nodeIDs: []string{"n_rerank"}},
		},
		mustNot: []Pattern{Routing, Parallelization},
		note:    "full retriever+embed+rerank pipeline feeding a generator",
	}
}

func fxResourceAware() fixture {
	same := withPrompt("answer the question")
	return fixture{
		name: "resource_aware/complexity_branch_selects_model_tier",
		ir: buildIR(
			[]discovery.IRNode{
				node("n_triage", withPrompt("estimate complexity")),
				node("n_cheap", same, withModel("anthropic", "claude-haiku-4-5"), withSemantics("conditional", false)),
				node("n_strong", same, withModel("anthropic", "claude-opus-4-8"), withSemantics("conditional", false)),
			},
			[]discovery.IREdge{controlEdge("n_triage", "n_cheap"), controlEdge("n_triage", "n_strong")},
		),
		want:    []wantLabel{{pattern: ResourceAwareOptimization, nodeIDs: []string{"n_triage", "n_cheap", "n_strong"}}},
		mustNot: []Pattern{Routing, MultiAgentCollaboration},
		note:    "same work, ≥2 distinct model bindings behind a conditional branch",
	}
}

// --- near-miss fixtures (task 3.10) — the discriminative-power evidence --------------------------

func fxNearMissChainIsNotRouting() fixture {
	f := fxPromptChaining()
	f.name = "near_miss/linear_chain_is_not_routing"
	f.note = "NEAR-MISS: no control edges at all, so Routing must not fire"
	return f
}

func fxNearMissEmptyToolsIsNotToolUse() fixture {
	return fixture{
		name:    "near_miss/empty_tools_skills_is_not_tool_use",
		ir:      buildIR([]discovery.IRNode{node("n_plain")}, nil),
		skills:  NewStaticSkillResolver("search_kb"),
		mustNot: []Pattern{ToolUse},
		note:    "NEAR-MISS: tools_skills is empty — a registry full of skills changes nothing",
	}
}

func fxNearMissUnresolvableToolIsNotToolUse() fixture {
	return fixture{
		name: "near_miss/unresolvable_tool_binding_is_not_tool_use",
		ir:   buildIR([]discovery.IRNode{node("n_stale", withTools("deleted_skill"))}, nil),
		// The registry knows a different skill: the binding is stale.
		skills:  NewStaticSkillResolver("search_kb"),
		mustNot: []Pattern{ToolUse},
		note:    "NEAR-MISS: bound to a skill the registry does not have — not Tool Use, and diagnosed",
	}
}

func fxNearMissFanOutWithoutMerge() fixture {
	return fixture{
		name: "near_miss/fanout_without_merge_is_not_parallelization",
		ir: buildIR(
			[]discovery.IRNode{node("n_split"), node("n_left"), node("n_right")},
			[]discovery.IREdge{dataEdge("n_split", "n_left"), dataEdge("n_split", "n_right")},
		),
		mustNot: []Pattern{Parallelization, PromptChaining},
		note:    "NEAR-MISS: nothing reconverges, so there is nothing to be merge-consistent about",
	}
}

func fxNearMissUnconditionalFanOutIsNotRouting() fixture {
	shared := withPolicy("full-history", "shared transcript")
	f := fixture{
		name: "near_miss/unconditional_control_fanout_is_not_routing",
		ir: buildIR(
			[]discovery.IRNode{
				node("n_manager", withPrompt("assign"), shared),
				node("n_a", withPrompt("do a"), shared),
				node("n_b", withPrompt("do b"), shared),
			},
			[]discovery.IREdge{controlEdge("n_manager", "n_a"), controlEdge("n_manager", "n_b")},
		),
		want:    []wantLabel{{pattern: MultiAgentCollaboration, nodeIDs: []string{"n_manager", "n_a", "n_b"}}},
		mustNot: []Pattern{Routing},
		note:    "NEAR-MISS: dispatch that always fires is coordination, not a conditional route",
	}
	return f
}

func fxNearMissSameModelBranchIsNotResourceAware() fixture {
	f := fxRouting()
	f.name = "near_miss/specialist_branches_are_not_tier_selection"
	f.mustNot = []Pattern{ResourceAwareOptimization, MultiAgentCollaboration, PromptChaining}
	f.note = "NEAR-MISS: branches do DIFFERENT work, so this is Routing, not a model-tier choice"
	return f
}

func fxNearMissEmbedOnlyIsNotRAG() fixture {
	return fixture{
		name: "near_miss/embed_step_alone_is_not_rag",
		ir: buildIR(
			[]discovery.IRNode{node("n_embed", withTools("embed_query")), node("n_answer")},
			[]discovery.IREdge{dataEdge("n_embed", "n_answer")},
		),
		skills: NewStaticSkillResolver("embed_query"),
		roles:  map[string]SkillRole{"embed_query": SkillRoleEmbedding},
		want:   []wantLabel{{pattern: ToolUse, nodeIDs: []string{"n_embed"}}},
		// Nor is it a prompt chain: an embedding step is not an LLM prompt step, so "≥2 LLM nodes in
		// a linear chain" is not satisfied either. It is a tool-bound node feeding a generator, and
		// the honest answer is to label only what is provable.
		mustNot: []Pattern{RetrievalRAG, PromptChaining},
		note:    "NEAR-MISS: an embedding step with no retriever is not a retrieval pipeline",
	}
}

// --- composite fixture (task 8.1/8.3): TWO patterns on TWO subgraphs of ONE workflow -------------

func fxComposite() fixture {
	return fixture{
		name: "composite/routing_on_A_and_rag_on_B",
		ir: buildIR(
			[]discovery.IRNode{
				// Subgraph A — a router with two conditional specialists.
				node("n_router", withPrompt("classify the ticket")),
				node("n_billing", withPrompt("handle billing"), withSemantics("conditional", false)),
				node("n_tech", withPrompt("handle tech support"), withSemantics("conditional", false), withTools("issue_lookup")),
				// Subgraph B — a retrieval pipeline feeding a generator.
				node("n_embed", withTools("embed_query")),
				node("n_retrieve", withPolicy("rag-retrieval", "top-k over the KB index")),
				node("n_rerank", withTools("cross_encoder_rerank")),
				node("n_answer", withPrompt("answer using the passages")),
			},
			[]discovery.IREdge{
				controlEdge("n_router", "n_billing"), controlEdge("n_router", "n_tech"),
				dataEdge("n_embed", "n_retrieve"), dataEdge("n_retrieve", "n_rerank"), dataEdge("n_rerank", "n_answer"),
			},
		),
		skills: NewStaticSkillResolver("embed_query", "cross_encoder_rerank", "issue_lookup"),
		roles:  map[string]SkillRole{"embed_query": SkillRoleEmbedding, "cross_encoder_rerank": SkillRoleRerank},
		want: []wantLabel{
			{pattern: Routing, nodeIDs: []string{"n_router", "n_billing", "n_tech"}},
			{pattern: RetrievalRAG, nodeIDs: []string{"n_embed", "n_retrieve", "n_rerank", "n_answer"}},
			// A capability co-existing on a node INSIDE a routing branch — both labels, not a contest.
			{pattern: ToolUse, nodeIDs: []string{"n_tech"}},
			{pattern: ToolUse, nodeIDs: []string{"n_embed"}},
			{pattern: ToolUse, nodeIDs: []string{"n_rerank"}},
		},
		note: "one workflow, two regions, two DIFFERENT metric-sets — the whole point of the phase",
	}
}

// --- ambiguous fixture (task 8.1): drives the LLM fallback --------------------------------------

func fxAmbiguous() fixture {
	return fixture{
		name: "ambiguous/no_structural_signature_matches",
		ir: buildIR(
			[]discovery.IRNode{
				node("n_guard", withPrompt("check the request against policy")),
				node("n_solo", withPrompt("answer"), withSemantics("conditional", false)),
			},
			// A single control edge: not a fan-out, not a chain, not a loop. No signature matches.
			[]discovery.IREdge{controlEdge("n_guard", "n_solo")},
		),
		note: "the ambiguous residue — the ONLY thing the constrained LLM fallback ever sees",
	}
}

// allFixtures is the calibration set. Every detector's confidence is checked against it.
func allFixtures() []fixture {
	return []fixture{
		fxPromptChaining(), fxRouting(), fxParallelization(), fxReflection(),
		fxToolUse(), fxMultiAgent(), fxRAG(), fxResourceAware(),
		fxNearMissChainIsNotRouting(), fxNearMissEmptyToolsIsNotToolUse(),
		fxNearMissUnresolvableToolIsNotToolUse(), fxNearMissFanOutWithoutMerge(),
		fxNearMissUnconditionalFanOutIsNotRouting(), fxNearMissSameModelBranchIsNotResourceAware(),
		fxNearMissEmbedOnlyIsNotRAG(),
		fxComposite(), fxAmbiguous(),
	}
}
