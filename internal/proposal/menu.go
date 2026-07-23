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
	}
	if o.SkillRefs != nil {
		out.SkillRefs = append([]string(nil), o.SkillRefs...)
	}
	return out
}
