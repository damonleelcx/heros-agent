package proposal

import (
	"sort"

	"github.com/heros-foreal/agentd/internal/variantspec"
)

// Menu is the set of registry entries an operator may select from, each addressed by its immutable
// registry version_id. An operator NEVER injects inline configuration — it references an entry by ID
// (FR3), exactly like an authored spec. The Menu is the platform's "what is available", supplied by
// the engine's caller from the four registries.
type Menu struct {
	Models          []ModelChoice
	Skills          []SkillChoice
	ContextPolicies []ContextChoice
	// MemoryStrategies are the sealed memory-registry entries OpMemoryPolicy may select from (P17).
	// Its own slice, not folded into ContextPolicies, because a memory ref and a context ref resolve in
	// different registries — one pasted into the other's dimension fails closed, which is exactly the
	// guarantee a shared slice would invite a caller to break.
	MemoryStrategies []MemoryChoice
	// HarnessStrategies are the sealed harness-registry entries OpHarnessStrategy may select from (P18).
	// Its own slice for the same reason MemoryStrategies is: a harness ref and a memory ref resolve in
	// different registries, and one pasted into the other's dimension fails closed.
	HarnessStrategies []HarnessChoice
	// LoopStrategies are the sealed LOOP-registry entries OpLoopStrategy may select from (P34).
	//
	// 🔴 Its own slice, and NOT a rename of HarnessStrategies. After ADR-014's split the two registries
	// hold different things — a harness entry is an execution ENVELOPE, a loop entry is an ITERATION
	// POLICY — and one slice holding both would let an operator write an envelope ref into `loop_ref`,
	// which fails closed at resolve but only after a candidate has been built, hashed and queued.
	//
	// 🚫 A menu that carries no loop entries makes OpLoopStrategy emit nothing, which is correct: an
	// operator with an empty menu has nothing admissible to offer, and that is not an error.
	LoopStrategies []LoopChoice
}

// LoopChoice is one loop registry entry available for an iteration-policy swap (P34).
//
// It mirrors HarnessChoice field for field, and the duplication is deliberate rather than lazy: the two
// menus feed two operators writing two different refs into two different dimensions, and a shared type
// would be one edit away from letting either write the other's ref.
type LoopChoice struct {
	Ref      string // loop registry version_id (64-hex content address)
	Strategy string // "single-shot" | "react-loop" | "plan-execute" | "reflexion" | "critic-loop"
	Title    string
	// MaxTurns is the entry's CHOSEN turn count; 1 for the identity. Platform metadata, never hashed.
	//
	// 🔴 It is what makes one candidate HEAVIER than another, which is the only input the cost/quality
	// admissibility gate needs beyond the measurements — and post-split it is a property of the LOOP
	// rather than of the envelope, so it is read from here.
	MaxTurns int
}

// loopStrategiesExcept returns the menu's loop entries other than the one the node already binds, in a
// deterministic order.
//
// 🔴 The identity's treatment is harnessStrategiesExcept's, unchanged and for its reasons: proposing
// `single-shot` against a scaffold mismatch is a real and often correct answer — a node running a
// five-turn loop its cases never needed is burning money — but proposing it at a node that ALREADY runs
// the identity is proposing NOTHING, because the candidate resolves to the baseline's own config_hash
// and would occupy a verification slot measuring a configuration against itself.
func (m Menu) loopStrategiesExcept(currentRef string) []LoopChoice {
	currentIsIdentity := currentRef == ""
	var out []LoopChoice
	for _, c := range m.LoopStrategies {
		if c.Ref == currentRef {
			continue
		}
		if currentIsIdentity && c.Strategy == harnessStrategySingleShot {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// HarnessChoice is one harness registry entry available for a scaffold swap (P18).
//
// 🔴 MaxTurns is carried, and it is the one piece of metadata this menu could not do without: the
// admissibility gate needs to know whether a candidate is HEAVIER than the baseline, and "heavier" is
// the turn ceiling. Deriving it downstream would mean re-parsing sealed params in the gate, which is a
// second reader of a schema and therefore a second thing that can be wrong.
//
// Strategy and Title are metadata exactly as ModelChoice.Provider is: the operator references the entry
// by Ref and never inlines a strategy name, so a candidate is always resolvable back from its
// config_hash.
type HarnessChoice struct {
	Ref      string // harness registry version_id (64-hex content address)
	Strategy string // "single-shot" | "react-loop" | "plan-execute" | "reflexion" | "critic-loop"
	Title    string
	// MaxTurns is the entry's sealed turn ceiling; 1 for the identity. Platform metadata, never hashed.
	MaxTurns int
}

// harnessStrategiesExcept returns the menu's harness entries other than the one the node already binds,
// in a deterministic order — the candidates for a swap.
//
// 🚫 Unlike memoryStrategiesExcept, the identity is not excluded WHOLESALE, and the asymmetry is
// deliberate. Proposing `none` against a stale-read signal answers "your recall is stale" with "then
// recall nothing" — the removal of the capability being diagnosed. Proposing `single-shot` against a
// scaffold mismatch is a real and often correct answer: a node running a five-turn loop its cases never
// needed is burning money, and "run one turn" is the cheapest fix on the table. The admissibility gate
// only bites on candidates that are HEAVIER, so a lighter candidate is judged on quality alone.
//
// 🔴 It IS excluded when it is already in force, and that exclusion is the second half of the same rule.
// A node with NO harness ref is implicitly `single-shot`, so proposing the identity there is proposing
// NOTHING: the candidate resolves to the baseline's own config_hash, and it would occupy a verification
// slot measuring a configuration against itself. Found by running the operator against a real repository,
// where every node is implicitly single-shot and the first candidate emitted was the baseline.
func (m Menu) harnessStrategiesExcept(currentRef string) []HarnessChoice {
	// An absent ref means the node runs the identity already — the discovered default everywhere.
	currentIsIdentity := currentRef == ""

	var out []HarnessChoice
	for _, c := range m.HarnessStrategies {
		if c.Ref == currentRef {
			continue
		}
		if currentIsIdentity && c.Strategy == harnessStrategySingleShot {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// harnessStrategySingleShot mirrors registry.StrategySingleShot. Spelled here rather than imported so
// this package's operator layer keeps depending only on refs and metadata — the registry is the
// resolver's dependency, not the catalog's — and it is pinned to the registry constant by a test, so the
// two cannot drift silently.
const harnessStrategySingleShot = "single-shot"

// ModelChoice is one model registry entry available for a model up/down-grade, with the cheap signals
// an operator ranks by. Tier and CostPerRun are platform metadata (from the registry / a pricing
// table), not something the operator invents.
type ModelChoice struct {
	Ref      string // registry version_id (64-hex content address)
	Provider string
	ModelID  string
	// Tier is a capability ordering; a higher tier is a stronger model. Upgrade climbs it, downgrade
	// descends it.
	Tier int
	// CostPerRun is the estimated $ per run at a node using this model (pre-verification estimate).
	CostPerRun float64
	// LatencyMS is the estimated per-run latency at a node using this model, for the latency-SLA
	// constraint check (pre-verification estimate).
	LatencyMS float64
	// Thinking marks a model whose registry entry enables an extended-thinking budget.
	Thinking bool
	// Params is the entry's provider parameters (temperature, max_tokens, …). Metadata only — the
	// operator references the entry by Ref; these let paramTuneOp tell a parameter-tuned variant of the
	// current model apart from the model itself. Never hashed here.
	Params map[string]any
}

// SkillChoice is one skill registry entry (a tool, retriever, embedding, or rerank stage).
type SkillChoice struct {
	Ref  string // registry version_id
	Name string
	// Kind is the role this skill plays, so the RAG/tool operators can pick the right one: "rerank",
	// "retriever", "embedding", or "tool".
	Kind string
}

// ContextChoice is one context-policy registry entry (summarization, sliding window, a top-k tune).
type ContextChoice struct {
	Ref    string // registry version_id
	Policy string // "summarization" | "sliding_window" | "topk" | …
	// TopK is carried for the RAG top-k tune so the operator can prefer a larger window; 0 when N/A.
	TopK int
	// ChunkSize and EmbeddingModel are the other two retrieval knobs OpRAGTune proposes over (P16 task
	// 6.2). Metadata only — the operator references the entry by Ref and never inlines these; they exist
	// so a rationale can say WHAT changed and so two entries of the same policy are distinguishable.
	// Zero/empty when the entry does not set them.
	ChunkSize      int
	EmbeddingModel string
	// Lossy marks a policy that can drop information (summarization, compaction, extraction). It is the
	// same distinction registry.AssembledContext.Lossy draws: a lossless policy's zero drop is a property
	// of the policy, not a measurement, and the drop gate must not read the two alike (P16 task 5.3).
	Lossy bool
	// ExpectedDrop is the pre-verification estimate of the fraction of context this entry would drop,
	// in [0,1]. It is platform metadata of exactly the kind ModelChoice.CostPerRun already is — an
	// estimate that lets the drop gate judge a policy nothing has run yet. A MEASURED drop for this node
	// always beats it (DropGate.Observed). Meaningless unless Lossy.
	ExpectedDrop float64
}

// MemoryChoice is one memory registry entry available for a strategy swap (P17).
//
// Strategy is metadata, exactly as ModelChoice.Provider is: the operator references the entry by Ref and
// never inlines a strategy name, so a candidate is always resolvable back from its config_hash. It exists
// so a rationale can say WHAT would change and so two entries of the same strategy — a 2 000-token
// summary buffer and a 6 000-token one — are distinguishable to a human reading the proposal.
type MemoryChoice struct {
	Ref      string // memory registry version_id (64-hex content address)
	Strategy string // "none" | "scratchpad" | "summary-buffer" | "vector-recall" | "entity-memory"
	// Title is the strategy's human label, carried so a proposal rationale reads as a sentence rather
	// than a wire name (registry.MemoryStrategy keeps the two layers separate for the same reason).
	Title string
}

// memoryStrategiesExcept returns the menu's memory entries other than the one the node already binds,
// in a deterministic order — the candidates for a swap.
//
// 🔴 `none` is excluded as a TARGET. Proposing "remove this node's memory" against a stale-read signal
// would be answering "your recall is stale" with "then recall nothing", which is not a fix; it is the
// removal of the capability being diagnosed. A user may of course author `none` themselves — that is
// their call to make about their own agent (memory-authoring), and it is a different act from the
// platform recommending it.
func (m Menu) memoryStrategiesExcept(currentRef string) []MemoryChoice {
	var out []MemoryChoice
	for _, c := range m.MemoryStrategies {
		if c.Ref == currentRef || c.Strategy == memoryStrategyNone {
			continue
		}
		out = append(out, c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// memoryStrategyNone mirrors registry.StrategyNone. It is spelled here rather than imported so this
// package's operator layer keeps depending only on refs and metadata — the registry is the resolver's
// dependency, not the catalog's — and it is pinned to the registry constant by a test, so the two cannot
// drift silently.
const memoryStrategyNone = "none"

const (
	skillKindRerank    = "rerank"
	skillKindRetriever = "retriever"
	skillKindEmbedding = "embedding"
	skillKindTool      = "tool"
)

// strongerModels returns menu models above tier, cheapest-tier-first (a minimal upgrade beats a
// maximal one when both clear the bar — smaller blast radius, design Q4). Deterministic order.
func (m Menu) strongerModels(tier int) []ModelChoice {
	var out []ModelChoice
	for _, c := range m.Models {
		if c.Tier > tier {
			out = append(out, c)
		}
	}
	sortModels(out)
	return out
}

// cheaperModels returns menu models below tier, most-expensive-first among the cheaper ones (the
// largest safe saving first). Deterministic order.
func (m Menu) cheaperModels(tier int) []ModelChoice {
	var out []ModelChoice
	for _, c := range m.Models {
		if c.Tier < tier {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Tier != out[j].Tier {
			return out[i].Tier > out[j].Tier
		}
		return out[i].Ref < out[j].Ref
	})
	return out
}

// thinkingModels returns menu models that enable extended thinking and are at least as strong as tier.
func (m Menu) thinkingModels(tier int) []ModelChoice {
	var out []ModelChoice
	for _, c := range m.Models {
		if c.Thinking && c.Tier >= tier {
			out = append(out, c)
		}
	}
	sortModels(out)
	return out
}

func (m Menu) skillsOfKind(kind string) []SkillChoice {
	var out []SkillChoice
	for _, s := range m.Skills {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func (m Menu) contextPoliciesOfKind(policy string) []ContextChoice {
	var out []ContextChoice
	for _, c := range m.ContextPolicies {
		if c.Policy == policy {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// paramTunedVariants returns menu models that are the SAME provider+model_id as cur but carry different
// provider parameters — the candidates for a parameter tune (P13 §3.5). Deterministic order. Returns
// nothing when cur is empty or no tuned variant exists, so paramTuneOp declines cleanly.
func (m Menu) paramTunedVariants(cur ModelChoice) []ModelChoice {
	if cur.Ref == "" {
		return nil
	}
	var out []ModelChoice
	for _, c := range m.Models {
		if c.Ref == cur.Ref {
			continue
		}
		if c.Provider == cur.Provider && c.ModelID == cur.ModelID && !paramsEqual(c.Params, cur.Params) {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

// skillNameByRef maps a spec's skill_ref back to the registered NAME the trace records when that skill
// is called. It is how the selection operators join "what the spec pinned" to "what the eval exercised"
// without either side inventing an identity for the other. Empty when the menu does not carry the ref.
func (m Menu) skillNameByRef(ref string) string {
	for _, s := range m.Skills {
		if s.Ref == ref {
			return s.Name
		}
	}
	return ""
}

// modelByRef returns the menu choice with the given ref, or a zero value when absent.
func (m Menu) modelByRef(ref string) ModelChoice {
	for _, c := range m.Models {
		if c.Ref == ref {
			return c
		}
	}
	return ModelChoice{}
}

// paramsEqual reports whether two provider-param maps hold the same keys and values.
func paramsEqual(a, b map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if bv, ok := b[k]; !ok || av != bv {
			return false
		}
	}
	return true
}

func sortModels(cs []ModelChoice) {
	sort.SliceStable(cs, func(i, j int) bool {
		if cs[i].Tier != cs[j].Tier {
			return cs[i].Tier < cs[j].Tier
		}
		return cs[i].Ref < cs[j].Ref
	})
}

// cloneSpec deep-copies a Variant Spec so an operator can derive a candidate without ever aliasing the
// baseline's backing storage. A shallow copy would share the Nodes map and mutate the baseline every
// caller holds.
func cloneSpec(s *variantspec.VariantSpec) *variantspec.VariantSpec {
	if s == nil {
		return &variantspec.VariantSpec{Nodes: map[string]variantspec.NodeOverride{}}
	}
	out := &variantspec.VariantSpec{
		WorkflowID:      s.WorkflowID,
		ParentVariantID: s.ParentVariantID,
		SourceRevision:  s.SourceRevision,
	}
	if s.Order != nil {
		out.Order = append([]string(nil), s.Order...)
	}
	out.Nodes = make(map[string]variantspec.NodeOverride, len(s.Nodes))
	for id, o := range s.Nodes {
		out.Nodes[id] = cloneOverride(o)
	}
	if s.Edges != nil {
		out.Edges = append([]variantspec.Edge(nil), s.Edges...)
	}
	if s.InsertedAdapters != nil {
		out.InsertedAdapters = append([]variantspec.InsertedAdapter(nil), s.InsertedAdapters...)
	}
	// Group harnesses ride along unchanged (P18). A node-scoped operator must not drop a group the
	// baseline declared — that would be the cloneOverride defect one level up, and with a larger blast
	// radius, since a group spans several nodes.
	if s.HarnessGroups != nil {
		out.HarnessGroups = make([]variantspec.HarnessGroup, 0, len(s.HarnessGroups))
		for _, g := range s.HarnessGroups {
			cp := variantspec.HarnessGroup{HarnessRef: g.HarnessRef}
			cp.Edges = append([]variantspec.Edge(nil), g.Edges...)
			out.HarnessGroups = append(out.HarnessGroups, cp)
		}
	}
	return out
}

func cloneOverride(o variantspec.NodeOverride) variantspec.NodeOverride {
	out := variantspec.NodeOverride{
		ModelRef:      o.ModelRef,
		PromptRef:     o.PromptRef,
		ContextPolicy: o.ContextPolicy,
		ApplyMode:     o.ApplyMode,
		// 🔴 MemoryRef was MISSING here until P18, and its absence was a real defect rather than an
		// omission of a decorative field: a proposal derived from a baseline that binds a memory strategy
		// silently UNBOUND it, so the candidate's config_hash differed from the baseline in TWO dimensions
		// while the candidate's Dimensions() claimed one. The eval would then attribute the whole delta to
		// the dimension the operator named. Caught by TestCloneOverrideCarriesEveryDimension, which
		// enumerates the fields rather than listing the ones someone remembered.
		MemoryRef: o.MemoryRef,
		// HarnessRef, for the same reason and from the start (P18).
		HarnessRef: o.HarnessRef,
	}
	// 🔴 The drop tolerance is COPIED BY VALUE, not aliased (P16 task 5.3). A candidate that shared the
	// baseline's pointer would let a later mutation of one reach the other; a candidate that dropped it
	// entirely would be a proposal the drop gate cannot judge — it would read "no tolerance" and admit a
	// change the node's author had already ruled out.
	if o.ContextDropTolerance != nil {
		t := *o.ContextDropTolerance
		out.ContextDropTolerance = &t
	}
	if o.SkillRefs != nil {
		out.SkillRefs = append([]string(nil), o.SkillRefs...)
	}
	if o.ToolSelection != nil {
		out.ToolSelection = append([]string(nil), o.ToolSelection...)
	}
	if o.Bindings != nil {
		out.Bindings = make(map[string]variantspec.BindingSource, len(o.Bindings))
		for k, v := range o.Bindings {
			out.Bindings[k] = v
		}
	}
	return out
}
