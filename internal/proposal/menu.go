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
}

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
	return out
}

func cloneOverride(o variantspec.NodeOverride) variantspec.NodeOverride {
	out := variantspec.NodeOverride{
		ModelRef:      o.ModelRef,
		PromptRef:     o.PromptRef,
		ContextPolicy: o.ContextPolicy,
		ApplyMode:     o.ApplyMode,
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
