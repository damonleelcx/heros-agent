package patternclassifier

import (
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
)

// A detector is a PURE function of IR topology + node metadata (+ the pre-resolved registry
// snapshot). No clock, no randomness, no map-iteration order, no I/O: the same IR must yield
// identical proposals on every run, because "classify the same IR twice → byte-identical labels" is
// a hard requirement and non-determinism anywhere in this layer makes it unreachable.
//
// A detector proposes REGIONS; it never mints a Label and never looks at another detector's output.
// Arbitration is the partitioner's job (precedence.go), which is what keeps each signature a local,
// readable predicate.
type detector interface {
	// id is versioned: a detector whose predicate changes gets a new id, so a stored label always
	// names the exact rule that produced it.
	id() string
	pattern() Pattern
	detect(g *graph, env *detectEnv) []RegionProposal
}

// detectEnv carries the resolved, read-only facts detectors need beyond topology.
type detectEnv struct {
	skills SkillResolver
	// skillRoles maps a registered skill name to its retrieval-pipeline role. Empty is legitimate:
	// it means no skill in this deployment declares a role, and the RAG detector then relies on the
	// registered context-assembly policy alone. It is NOT a default that pretends to know.
	skillRoles map[string]SkillRole
	diags      *diagSink
}

// detectors is the ordered registry of the eight structural detectors that ship in P3.5. Order here
// does not affect the outcome (arbitration is order-independent, proven in TestResolveIsOrderIndependent);
// it only fixes the order proposals are generated in, which keeps debug dumps stable.
func detectors() []detector {
	return []detector{
		routingDetector{},
		resourceAwareDetector{},
		multiAgentDetector{},
		parallelizationDetector{},
		promptChainingDetector{},
		reflectionDetector{},
		retrievalRAGDetector{},
		toolUseDetector{},
	}
}

// ---------------------------------------------------------------------------------------------
// Shared node predicates. Every one of these reads a field the IR actually states — never a name it
// guesses at.
// ---------------------------------------------------------------------------------------------

// isConditional reports the IR's OWN declaration that a node fires zero-or-once on a runtime branch.
// This is what "fanning CONDITIONALLY" means structurally: it is stated in invocation_semantics, not
// inferred from the shape of the fan-out.
func isConditional(n *discovery.IRNode) bool {
	return n != nil && n.InvocationSemantics.Type == "conditional"
}

// promptKey identifies the WORK a node does. Two nodes with the same prompt do the same job; two
// nodes with different prompts are specialists. This is the discriminator between Routing (different
// work per branch) and Resource-Aware Optimization (same work, different model tier).
func promptKey(n *discovery.IRNode) string {
	return n.Prompt.Inline
}

// modelKey identifies the model binding — the thing a tier selection varies.
func modelKey(n *discovery.IRNode) string {
	return n.Model.Provider + "/" + n.Model.ModelID
}

// retrievalPolicies are the REGISTERED context-assembly policies that assemble context by retrieving
// (internal/registry/context_policies.go). Membership here is a registry fact, which is why it is a
// usable structural signal; a free-text policy string would not be.
var retrievalPolicies = map[string]bool{
	"rag-retrieval": true,
}

// distinctBy counts distinct values of key over nodes, deterministically.
func distinctBy(g *graph, ids []string, key func(*discovery.IRNode) string) int {
	seen := map[string]bool{}
	for _, id := range ids {
		if n := g.nodes[id]; n != nil {
			seen[key(n)] = true
		}
	}
	return len(seen)
}

// ---------------------------------------------------------------------------------------------
// 3.2 Routing — one node with CONTROL edges fanning conditionally to N ≥ 2 specialists.
// ---------------------------------------------------------------------------------------------

type routingDetector struct{}

func (routingDetector) id() string       { return "routing.conditional_control_fanout.v1" }
func (routingDetector) pattern() Pattern { return Routing }

func (d routingDetector) detect(g *graph, env *detectEnv) []RegionProposal {
	var out []RegionProposal
	for _, r := range g.order {
		targets := g.controlOut[r]
		if len(targets) < 2 {
			continue // NEAR-MISS GUARD: a linear chain has no control fan-out and is never Routing.
		}
		conditional := 0
		for _, t := range targets {
			if isConditional(g.nodes[t]) {
				conditional++
			}
		}
		if conditional < 2 {
			continue // dispatch that always fires is coordination, not a conditional route.
		}
		// SPECIALISTS: the branches must do DIFFERENT work. Branches that do the same work on
		// different models are a tier selection, not a route — that is resourceAwareDetector's
		// signature, and this guard is what keeps the two mutually exclusive rather than contesting.
		if distinctBy(g, targets, promptKey) < 2 {
			continue
		}
		out = append(out, RegionProposal{
			Pattern: Routing, DetectorID: d.id(), Scope: ScopeRegion,
			NodeIDs: append([]string{r}, targets...), Confidence: ConfidenceTopologyDetermined,
		})
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// 3.8 Resource-Aware Optimization — a control branch selecting among MODEL TIERS on a
// cost/complexity condition.
// ---------------------------------------------------------------------------------------------

type resourceAwareDetector struct{}

func (resourceAwareDetector) id() string       { return "resource_aware.tier_select_branch.v1" }
func (resourceAwareDetector) pattern() Pattern { return ResourceAwareOptimization }

func (d resourceAwareDetector) detect(g *graph, env *detectEnv) []RegionProposal {
	var out []RegionProposal
	for _, r := range g.order {
		targets := g.controlOut[r]
		if len(targets) < 2 {
			continue
		}
		conditional := 0
		for _, t := range targets {
			if isConditional(g.nodes[t]) {
				conditional++
			}
		}
		if conditional < 2 {
			continue
		}
		// The signature: SAME work, DIFFERENT model. One prompt across all branches means the branch
		// is not choosing what to do, only what to do it with — which is a cost/complexity decision.
		// Two or more distinct model bindings are the tiers being selected among.
		if distinctBy(g, targets, promptKey) != 1 || distinctBy(g, targets, modelKey) < 2 {
			continue
		}
		out = append(out, RegionProposal{
			Pattern: ResourceAwareOptimization, DetectorID: d.id(), Scope: ScopeRegion,
			NodeIDs: append([]string{r}, targets...),
			// Discounted band: the IR states the models differ, but that the CONDITION is about cost
			// or complexity is an inference from "same work, different tier", not something the IR
			// says outright.
			Confidence: ConfidenceTopologyStrong,
		})
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// 3.6 Multi-Agent Collaboration — a manager node dispatching (control edges) to role nodes over
// shared context.
// ---------------------------------------------------------------------------------------------

type multiAgentDetector struct{}

func (multiAgentDetector) id() string       { return "multi_agent.manager_dispatch_shared_context.v1" }
func (multiAgentDetector) pattern() Pattern { return MultiAgentCollaboration }

func (d multiAgentDetector) detect(g *graph, env *detectEnv) []RegionProposal {
	var out []RegionProposal
	for _, m := range g.order {
		roles := g.controlOut[m]
		if len(roles) < 2 {
			continue
		}
		// NOT a conditional route: roles all participate. A branch that fires zero-or-once is a
		// router's branch, and this guard is what stops a router from also reading as a manager.
		for _, t := range roles {
			if isConditional(g.nodes[t]) {
				roles = nil
				break
			}
		}
		if len(roles) < 2 {
			continue
		}
		// ROLES: distinct work per agent. Identical work across agents is fan-out, not collaboration.
		if distinctBy(g, roles, promptKey) < 2 {
			continue
		}
		// SHARED CONTEXT: every role assembles context under the SAME registered policy — the
		// checkable structural stand-in for "over shared context".
		if distinctBy(g, roles, func(n *discovery.IRNode) string { return n.ContextAssembly.Policy }) != 1 {
			continue
		}
		out = append(out, RegionProposal{
			Pattern: MultiAgentCollaboration, DetectorID: d.id(), Scope: ScopeRegion,
			NodeIDs: append([]string{m}, roles...),
			// Discounted band: "shared context" is read off a shared policy name rather than proven.
			Confidence: ConfidenceTopologyStrong,
		})
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// 3.3 Parallelization — fan-out to ≥ 2 INDEPENDENT nodes reconverging at a merge node.
// ---------------------------------------------------------------------------------------------

type parallelizationDetector struct{}

func (parallelizationDetector) id() string       { return "parallelization.fanout_merge.v1" }
func (parallelizationDetector) pattern() Pattern { return Parallelization }

func (d parallelizationDetector) detect(g *graph, env *detectEnv) []RegionProposal {
	var out []RegionProposal
	for _, f := range g.order {
		branches := g.dataOut[f]
		if len(branches) < 2 {
			continue
		}
		// INDEPENDENCE: no data path from one branch to another. Two "branches" where one feeds the
		// other are a chain drawn sideways, not parallel work.
		independent := make([]string, 0, len(branches))
		for _, b := range branches {
			dependent := false
			for _, other := range branches {
				if b != other && g.dataReaches(b, other) {
					dependent = true
					break
				}
			}
			if !dependent {
				independent = append(independent, b)
			}
		}
		if len(independent) < 2 {
			continue
		}
		// RECONVERGENCE: a merge node fed by ≥ 2 of the independent branches.
		// NEAR-MISS GUARD: a fan-out with no merge is NOT Parallelization — there is nothing to be
		// merge-consistent about, so dispatching merge-consistency metrics at it would be nonsense.
		merges := map[string][]string{}
		for _, b := range independent {
			for _, m := range g.dataOut[b] {
				merges[m] = append(merges[m], b)
			}
		}
		mergeIDs := make([]string, 0, len(merges))
		for m := range merges {
			mergeIDs = append(mergeIDs, m)
		}
		sort.Strings(mergeIDs) // map iteration is unordered; the proposal must not be.
		for _, m := range mergeIDs {
			feeders := normalizeNodeIDs(merges[m])
			if len(feeders) < 2 || m == f {
				continue
			}
			out = append(out, RegionProposal{
				Pattern: Parallelization, DetectorID: d.id(), Scope: ScopeRegion,
				NodeIDs: append(append([]string{f}, feeders...), m), Confidence: ConfidenceTopologyDetermined,
			})
		}
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// 3.1 Prompt Chaining — ≥ 2 LLM nodes in a linear data-edge chain, no fan-out/fan-in/loop.
// ---------------------------------------------------------------------------------------------

type promptChainingDetector struct{}

func (promptChainingDetector) id() string       { return "prompt_chaining.linear_data_chain.v1" }
func (promptChainingDetector) pattern() Pattern { return PromptChaining }

// linear reports whether a node can sit inside a linear chain: at most one data edge in, at most one
// out, and NO control edges at all. A node touched by a control edge belongs to a routed or
// coordinated region, and folding it into a "chain" would misdescribe the region.
//
// NEAR-MISS GUARD: a node playing a retrieval-pipeline role is not an LLM prompt step. The signature
// is "≥ 2 LLM nodes in a linear chain", and a retriever → rerank → generator line is a RETRIEVAL
// pipeline that happens to be drawn as a line. Without this guard every RAG region would also read
// as Prompt Chaining and would drag handoff-validity metrics onto a subgraph that needs
// relevance@k — exactly the mis-dispatch this phase exists to prevent.
func (promptChainingDetector) linear(g *graph, env *detectEnv, id string) bool {
	return len(g.dataIn[id]) <= 1 && len(g.dataOut[id]) <= 1 &&
		len(g.controlIn[id]) == 0 && len(g.controlOut[id]) == 0 &&
		len(roleOf(g.nodes[id], env)) == 0
}

func (d promptChainingDetector) detect(g *graph, env *detectEnv) []RegionProposal {
	var out []RegionProposal
	visited := map[string]bool{}
	for _, start := range g.order {
		if visited[start] || !d.linear(g, env, start) {
			continue
		}
		// Start only at a chain HEAD: nothing linear feeds it. This makes each maximal chain be
		// walked exactly once, which is what "maximal region matching a single signature" requires.
		if in := g.dataIn[start]; len(in) == 1 && d.linear(g, env, in[0]) {
			continue
		}
		chain := []string{start}
		visited[start] = true
		for cur := start; ; {
			next := g.dataOut[cur]
			if len(next) != 1 || !d.linear(g, env, next[0]) || visited[next[0]] {
				break // visited[next] also breaks a cycle: a loop is not a chain.
			}
			cur = next[0]
			visited[cur] = true
			chain = append(chain, cur)
		}
		if len(chain) < 2 {
			continue // a single node is not a chain.
		}
		out = append(out, RegionProposal{
			Pattern: PromptChaining, DetectorID: d.id(), Scope: ScopeRegion,
			NodeIDs: chain, Confidence: ConfidenceTopologyDetermined,
		})
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// 3.4 Reflection — output loops back to a generate node. STRUCTURAL CANDIDATE ONLY.
// ---------------------------------------------------------------------------------------------

type reflectionDetector struct{}

func (reflectionDetector) id() string       { return "reflection.loopback_cycle.v1" }
func (reflectionDetector) pattern() Pattern { return Reflection }

func (d reflectionDetector) detect(g *graph, env *detectEnv) []RegionProposal {
	var out []RegionProposal
	for _, comp := range g.cycles() {
		out = append(out, RegionProposal{
			Pattern: Reflection, DetectorID: d.id(), Scope: ScopeRegion, NodeIDs: comp,
			// The honesty boundary, priced in. A cycle proves the loop EXISTS; it cannot prove the
			// loop ITERATES more than once, and iteration is what Reflection means. So this is a
			// candidate at the capped band — never a confirmed fact — and Label.Validate enforces
			// the cap independently, so a future edit here cannot quietly promote it.
			Confidence: BehavioralCandidateCap, Candidate: true,
		})
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// 3.7 Retrieval / RAG — retriever + embed + rerank chain feeding a generator.
// ---------------------------------------------------------------------------------------------

type retrievalRAGDetector struct{}

func (retrievalRAGDetector) id() string       { return "retrieval_rag.retriever_chain_to_generator.v1" }
func (retrievalRAGDetector) pattern() Pattern { return RetrievalRAG }

// roleOf reads a node's retrieval-pipeline role from REGISTRY facts: the registered context-assembly
// policy it uses, or the declared role of a registered skill it is bound to. Never from its name.
func roleOf(n *discovery.IRNode, env *detectEnv) map[SkillRole]bool {
	roles := map[SkillRole]bool{}
	if retrievalPolicies[n.ContextAssembly.Policy] {
		roles[SkillRoleRetrieval] = true
	}
	for _, t := range n.ToolsSkills {
		if r, ok := env.skillRoles[t]; ok && env.skills.ResolvesSkill(t) {
			roles[r] = true
		}
	}
	return roles
}

func (d retrievalRAGDetector) detect(g *graph, env *detectEnv) []RegionProposal {
	var out []RegionProposal
	for _, gen := range g.order {
		genNode := g.nodes[gen]
		// A generator is an LLM node playing NO retrieval-pipeline role. Excluding only the retriever
		// here was a real bug: a rerank node would then qualify as a "generator", and the pipeline
		// {embed→retrieve→rerank} would be proposed as a second, nested RAG region on top of the true
		// one. Reranking is a step INSIDE the pipeline, not the thing the pipeline feeds.
		if len(roleOf(genNode, env)) > 0 {
			continue
		}
		// Walk upstream over data edges collecting the retrieval pipeline that feeds this generator.
		pipeline := map[string]map[SkillRole]bool{}
		var walk func(id string, depth int)
		seen := map[string]bool{gen: true}
		walk = func(id string, depth int) {
			if depth > len(g.order) {
				return
			}
			for _, up := range g.dataIn[id] {
				if seen[up] {
					continue
				}
				r := roleOf(g.nodes[up], env)
				if len(r) == 0 {
					continue // a plain upstream LLM node ends the retrieval pipeline.
				}
				seen[up] = true
				pipeline[up] = r
				walk(up, depth+1)
			}
		}
		walk(gen, 0)
		if len(pipeline) == 0 {
			continue
		}
		ids := make([]string, 0, len(pipeline))
		has := map[SkillRole]bool{}
		for id, r := range pipeline {
			ids = append(ids, id)
			for role := range r {
				has[role] = true
			}
		}
		if !has[SkillRoleRetrieval] {
			continue // NEAR-MISS GUARD: an embed step alone is not a retrieval pipeline.
		}
		// Calibration: the FULL signature (retriever + embed + rerank → generator) is the one the
		// spec names, and it is unambiguous. A bare retriever → generator is still RAG but has fewer
		// distinguishing steps, so it carries the discounted band rather than pretending to the same
		// certainty.
		conf := ConfidenceTopologyStrong
		if has[SkillRoleEmbedding] && has[SkillRoleRerank] {
			conf = ConfidenceTopologyDetermined
		}
		out = append(out, RegionProposal{
			Pattern: RetrievalRAG, DetectorID: d.id(), Scope: ScopeRegion,
			NodeIDs: append(ids, gen), Confidence: conf,
		})
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// 3.5 Tool Use — node tools_skills non-empty AND resolvable against the Skill Registry.
// ---------------------------------------------------------------------------------------------

type toolUseDetector struct{}

func (toolUseDetector) id() string       { return "tool_use.registry_bound_skills.v1" }
func (toolUseDetector) pattern() Pattern { return ToolUse }

func (d toolUseDetector) detect(g *graph, env *detectEnv) []RegionProposal {
	var out []RegionProposal
	for _, id := range g.order {
		n := g.nodes[id]
		if len(n.ToolsSkills) == 0 {
			continue // NEAR-MISS GUARD: an empty tools_skills node is NOT Tool Use.
		}
		var resolved, unresolved []string
		for _, t := range n.ToolsSkills {
			if env.skills.ResolvesSkill(t) {
				resolved = append(resolved, t)
			} else {
				unresolved = append(unresolved, t)
			}
		}
		if len(unresolved) > 0 {
			// A binding to a skill the registry does not have is a real finding, not noise: it is a
			// node that will fail at runtime. Recorded either way — visible, never silent.
			env.diags.add(Diagnostic{
				Stage: StagePartition, SubgraphRef: id, RawPattern: string(ToolUse), Source: SourceRule,
				Reason: fmt.Sprintf("tools_skills %v do not resolve against the Skill Registry", unresolved),
			})
		}
		if len(resolved) == 0 {
			continue // NEAR-MISS GUARD: bound to nothing that exists is not Tool Use either.
		}
		out = append(out, RegionProposal{
			Pattern: ToolUse, DetectorID: d.id(),
			// NODE-scoped: a capability co-exists with whatever region owns the node, so a tool-bound
			// node inside a Routing branch yields BOTH labels rather than contesting.
			Scope: ScopeNode, NodeIDs: []string{id}, Confidence: ConfidenceTopologyDetermined,
		})
	}
	return out
}

// ---------------------------------------------------------------------------------------------
// Topology helpers.
// ---------------------------------------------------------------------------------------------

// dataReaches reports whether there is a data path from → to.
func (g *graph) dataReaches(from, to string) bool {
	seen := map[string]bool{from: true}
	stack := []string{from}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, nb := range g.dataOut[cur] {
			if nb == to {
				return true
			}
			if !seen[nb] {
				seen[nb] = true
				stack = append(stack, nb)
			}
		}
	}
	return false
}

// cycles returns every cyclic region of the graph — each non-trivial strongly-connected component
// (Tarjan) plus each self-edge — over data AND control edges, in a deterministic order.
//
// SCCs rather than enumerated elementary cycles: an SCC is the maximal region the loop can traverse,
// which matches the "maximal region matching one signature" partition contract, and its count is
// bounded by the node count (elementary cycles can be exponential).
func (g *graph) cycles() [][]string {
	index := map[string]int{}
	low := map[string]int{}
	onStack := map[string]bool{}
	var stack []string
	next := 0
	var comps [][]string

	var strongConnect func(v string)
	strongConnect = func(v string) {
		index[v] = next
		low[v] = next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range g.anyOut[v] { // already sorted: traversal order is deterministic
			if _, ok := index[w]; !ok {
				strongConnect(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] {
				if index[w] < low[v] {
					low[v] = index[w]
				}
			}
		}
		if low[v] != index[v] {
			return
		}
		var comp []string
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			comp = append(comp, w)
			if w == v {
				break
			}
		}
		selfLoop := false
		for _, w := range g.anyOut[v] {
			if w == v {
				selfLoop = true
			}
		}
		// A single node with no self-edge is an SCC too, but it is not a cycle.
		if len(comp) > 1 || selfLoop {
			comps = append(comps, normalizeNodeIDs(comp))
		}
	}
	for _, v := range g.order {
		if _, ok := index[v]; !ok {
			strongConnect(v)
		}
	}
	sort.Slice(comps, func(i, j int) bool {
		return strings.Join(comps[i], "\x00") < strings.Join(comps[j], "\x00")
	})
	return comps
}
