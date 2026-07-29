package variantspec

import (
	"fmt"
	"sort"

	"github.com/heros-foreal/agentd/internal/confighash"
)

// ResolvedConfig is the fully-resolved configuration that `config_hash` is computed over.
//
// # This shape is FROZEN, not ours to design
//
// It is P0's contract — docs/decisions/config-hash-spec.md §3, with golden vectors in
// schemas/samples/config-hash.golden.json. P0 says of it: "the P2 Config Layer computes it; every
// store keys on it," and internal/confighash says the live P2 producer "MUST reproduce these vectors
// bit-for-bit." So the field names, their JSON spellings, and the fact that every one of them is
// always present are a contract with P4/P5.5 and with every row already keyed by a config_hash —
// not a struct to tidy. resolved_config_golden_test.go asserts this type reproduces the frozen
// canonical bytes exactly; if you change a tag here, that test is what tells you.
//
// # Why this is a PROJECTION of a Variant Spec, not the Variant Spec itself
//
// A Variant Spec references registry entries by content-addressed version_id (FR3). resolved_config
// records what those refs RESOLVE TO — `openai/gpt-4o-mini`, not a 64-hex id. The two are different
// contracts on purpose:
//
//   - config_hash must denote a CONFIGURATION, not a set of registry rows. Two specs that pin
//     different model entries with identical provider+id+params describe the same computation and
//     must share a hash, or every eval comparison fragments.
//   - P0 froze this shape before P2's registries existed. Hashing version_ids instead would produce
//     different bytes for the same configuration and break every golden vector.
//
// No `omitempty` on any field: the golden's canonical bytes carry all seven node fields on every
// node, including `"skill_refs":[]` and `"context_params":{}`. An omitted field is a different
// config_hash.
type ResolvedConfig struct {
	// IRVersion is the IR contract version the config was built against. Included because the same
	// bindings under a different IR major may mean something different (config-hash-spec §3).
	IRVersion string         `json:"ir_version"`
	Nodes     []ResolvedNode `json:"nodes"`
	Edges     []ResolvedEdge `json:"edges"`
}

// ResolvedNode is one static definition with every dimension resolved to an exact value.
type ResolvedNode struct {
	NodeID string `json:"node_id"`
	// ModelRef is "<provider>/<model_id>", e.g. "anthropic/claude-sonnet-5".
	ModelRef string `json:"model_ref"`
	// PromptRef is "prompt://<name>@<version>", pinned to a version so the hash always resolves the
	// exact template bytes (config-hash-spec §3, FR14).
	PromptRef string `json:"prompt_ref"`
	// SkillRefs are "<name>@<version>", in the spec's declared order. Order is identity-bearing:
	// JCS does not sort arrays, and the golden's ["search_kb@2","issue_lookup@1"] is not alphabetical.
	SkillRefs     []string `json:"skill_refs"`
	ContextPolicy string   `json:"context_policy"`
	// ContextParams and ProviderParams are free-form by P0's contract. ProviderParams carries only
	// params that change what the model produces — see providerParams() for what is deliberately left
	// out and why.
	ContextParams  map[string]any `json:"context_params"`
	ProviderParams map[string]any `json:"provider_params"`
	// Bindings is the resolved per-slot binding source (P10 task 3.8, decisions.md D-1.4). ADDITIVE and
	// omitempty with a nil-when-empty map: a node that binds no slot emits NO `bindings` key, so its
	// canonical bytes are byte-identical to a pre-P10 node and the frozen golden vectors keep
	// reproducing. When present, JCS sorts the keys, so `config_hash` depends on the binding SET, not
	// its authoring order. The hash therefore changes iff a binding changes.
	//
	// Contrast the sibling maps above, which are always-present `{}`: their emptiness is part of the
	// frozen bytes. This field is new, so its ABSENCE is what must stay byte-compatible — achieved by
	// omission, not by an empty object.
	Bindings map[string]ResolvedBinding `json:"bindings,omitempty"`
	// ToolSelection is the kept subset of the node's discovered tools (P14, DimTools; decisions.md
	// D-14.2). ADDITIVE and omitempty with a nil-when-empty slice: a node that prunes nothing emits NO
	// `tool_selection` key, so its canonical bytes are byte-identical to a pre-P14 node and the frozen
	// golden vectors keep reproducing — the same discipline Bindings above follows.
	//
	// It is SORTED, unlike SkillRefs. That asymmetry is the contract, not an inconsistency: skill order
	// is identity-bearing because the call site binds them in that order, while a tool selection denotes
	// a SET — two specs keeping the same tools in different authoring order describe one configuration
	// and must share a config_hash, or every eval comparison fragments on a formatting difference.
	ToolSelection []string `json:"tool_selection,omitempty"`
	// ContextDropTolerance is the node's frozen drop tolerance (P16 task 1.5, decisions.md D-2).
	// ADDITIVE and omitempty with a NIL-when-absent pointer: a node that declares no tolerance emits NO
	// `context_drop_tolerance` key, so its canonical bytes are byte-identical to a pre-P16 node and the
	// frozen golden vectors keep reproducing — the third application of the discipline Bindings and
	// ToolSelection above follow.
	//
	// When set it participates in config_hash additively (JCS includes the present key), which is the
	// correct identity: two variants that differ only in the drop a node tolerates are different
	// configurations, because the second admits proposals the first rejects.
	ContextDropTolerance *float64 `json:"context_drop_tolerance,omitempty"`
	// Memory is the node's resolved memory strategy — what it carries ACROSS invocations (P17 task 4.2,
	// decisions.md D3). ADDITIVE and omitempty with a NIL-when-absent pointer: a node with no memory
	// strategy emits NO `memory` key, so its canonical bytes are byte-identical to a pre-P17 node and the
	// frozen golden vectors keep reproducing — the fourth application of the discipline Bindings,
	// ToolSelection and ContextDropTolerance above follow.
	//
	// 🔴 `none` ≡ ABSENT. The identity strategy resolves to a NIL pointer here, not to
	// `{"strategy":"none"}`, so a node that explicitly selects `none` and a node that never mentioned
	// memory produce the same bytes and the same config_hash. That equality is what lets a user back out
	// of an authored memory change with no residue in the hash, and it is asserted rather than assumed
	// (TestNoneMemoryHashesAsAbsent).
	//
	// When present it participates in config_hash structurally — JCS sorts the params keys, so the hash
	// depends on the params SET rather than their authoring order, and it changes iff the strategy or a
	// param changes. That is the correct identity: two variants differing in what a node remembers between
	// turns are different configurations, whatever else they share.
	//
	// 🚫 It is NOT the memory registry's version_id. Like every other field here this is a PROJECTION —
	// config_hash denotes a CONFIGURATION, not a set of registry rows, so two specs pinning different
	// entries with the same strategy and params describe one computation and must share a hash.
	Memory *ResolvedMemory `json:"memory,omitempty"`
}

// ResolvedMemory is a node's resolved memory strategy: which strategy, and the params it runs with. This
// is the hashed projection of a memory registry entry — the strategy NAME and the params, never the
// version_id, for the reason ResolvedNode's doc comment gives.
type ResolvedMemory struct {
	Strategy string         `json:"strategy"`
	Params   map[string]any `json:"params"`
}

// ResolvedBinding is one slot's resolved binding: its kind and value, recorded explicitly (never
// inferred from the value's shape). This is the hashed projection of a spec BindingSource.
type ResolvedBinding struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

// ResolvedEdge is one graph edge. Edges are in the hash because re-arrangement (P5) changes the
// graph, and the wiring is part of a configuration's identity.
type ResolvedEdge struct {
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	Kind       string `json:"kind"` // "data" | "control"
}

// Canonical returns the exact bytes config_hash is computed over: RFC 8785 canonical JSON.
//
// It normalizes nil to empty first. A nil slice marshals to `null` and an empty one to `[]` — two
// spellings of "this node binds no skills" that would hash differently and silently fork one
// configuration into two config_hashes. Normalizing here rather than trusting every construction
// path is what makes that impossible; TestCanonical_NilAndEmptyHashIdentically pins it.
func (rc ResolvedConfig) Canonical() ([]byte, error) {
	norm := rc.normalized()
	raw, err := jsonMarshal(norm)
	if err != nil {
		return nil, fmt.Errorf("variantspec: marshal resolved_config: %w", err)
	}
	// The SAME canonicalizer that produces registry version_ids (internal/confighash), so a spec and
	// the refs inside it can never disagree about what canonical means.
	canon, err := confighash.CanonicalizeBytes(raw)
	if err != nil {
		return nil, fmt.Errorf("variantspec: canonicalize resolved_config: %w", err)
	}
	return canon, nil
}

// Hash returns the config_hash: SHA-256 over the canonical bytes, lowercase hex, full 64 chars
// (config-hash-spec §2). Never store a truncation — Display() is for the UI only.
func (rc ResolvedConfig) Hash() (string, error) {
	canon, err := rc.Canonical()
	if err != nil {
		return "", err
	}
	return sha256Hex(canon), nil
}

// normalized returns a copy with nil slices/maps replaced by empty ones. It does NOT sort anything:
// node order and skill_refs order are identity-bearing, and edges are emitted in the caller's order.
func (rc ResolvedConfig) normalized() ResolvedConfig {
	out := ResolvedConfig{IRVersion: rc.IRVersion}
	out.Nodes = make([]ResolvedNode, 0, len(rc.Nodes))
	for _, n := range rc.Nodes {
		if n.SkillRefs == nil {
			n.SkillRefs = []string{}
		}
		if n.ContextParams == nil {
			n.ContextParams = map[string]any{}
		}
		if n.ProviderParams == nil {
			n.ProviderParams = map[string]any{}
		}
		out.Nodes = append(out.Nodes, n)
	}
	out.Edges = make([]ResolvedEdge, 0, len(rc.Edges))
	out.Edges = append(out.Edges, rc.Edges...)
	return out
}

// Display returns the 12-char prefix for the UI. Never stored (config-hash-spec §2 / OQ2).
func Display(hash string) string { return confighash.Display(hash) }

// providerParams projects a model entry's inference params into the hash's provider_params.
//
// # Two params are deliberately EXCLUDED, and both exclusions are load-bearing
//
// `seed` — P0 config-hash-spec §5 requires it: "The same configuration under seeds 1..5 MUST share
// one config_hash so multi-seed results roll up under one config for confidence intervals." Golden
// vector (d) asserts no run-time field exists in resolved_config at all. This sits in real tension
// with P2's FR9, which pins seed INSIDE the model entry as part of its versioned unit — so a model
// entry does carry a seed, and it must not reach the hash. The coherent reading, and the one the
// rest of P2 assumes: the entry's seed is a DEFAULT, while the reproducibility unit is
// `config_hash + seed` (PRD §7, FR16) with the run supplying the seed. Two model entries differing
// only in seed therefore share a config_hash, which is exactly what P4's multi-seed scoring needs.
//
// `timeout_seconds` — an operational knob, not an inference param: it changes how long we wait, not
// what the model produces. P0's include set is "inference params that change model behavior".
// Hashing it would fragment one configuration across two timeouts for no analytical gain.
//
// Both are absences a reader would otherwise read as an oversight, so both are tested
// (TestProviderParams_ExcludesSeedAndTimeout) rather than left to this comment.
func providerParams(p modelParamsView) map[string]any {
	out := map[string]any{}
	if p.Temperature != nil {
		out["temperature"] = *p.Temperature
	}
	if p.MaxTokens != nil {
		out["max_tokens"] = *p.MaxTokens
	}
	if p.ThinkingBudget != nil {
		out["thinking_budget"] = *p.ThinkingBudget
	}
	return out
}

// modelParamsView is the subset of registry.ModelParams this projection reads. Declared here rather
// than importing the registry type directly so that adding a param to the registry is a deliberate
// decision about the hash — a new field cannot silently start (or stop) being identity-bearing.
type modelParamsView struct {
	Temperature    *float64
	MaxTokens      *int
	ThinkingBudget *int
}

// sortedKeys is used only for deterministic error messages, never for hash input (JCS sorts keys
// itself, and array order must be preserved).
func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
