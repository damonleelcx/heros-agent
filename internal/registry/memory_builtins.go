package registry

import "encoding/json"

// The builtin memory-strategy vocabulary (P17 task 5.3, decisions.md D5).
//
// # Why the set is CLOSED and VERSIONED
//
// A `config_hash` stored today must still MEAN the same thing months from now. A stored strategy name is
// only interpretable against the vocabulary it was written under, which is exactly the problem
// `TaxonomyVersion` exists to prevent for pattern labels (internal/patternclassifier/taxonomy.go). An
// open set of free-form strategy strings would make a stored `summary-buffer` un-interpretable the moment
// the vocabulary drifted, and open params could not be validated at all — a malformed strategy would be
// discovered at run time, by whoever was unlucky, instead of at seal.
//
// So: exactly five strategies per strategy-set version, each declaring a ParamsSchema, and a cardinality
// assertion (MemoryStrategySetSize) that fails LOUDLY if a sixth is added without a version bump.
//
// # Why these five
//
// A deliberate spread across the design space, not an arbitrary list — they are the HYPOTHESES the eval
// harness will adjudicate, which is the whole reason memory becomes a Dimension:
//
//	none            the identity. No memory at all — the baseline every other strategy is measured against.
//	scratchpad      ephemeral working notes, kept whole, bounded by count.
//	summary-buffer  a rolling summary: trades fidelity for tokens.
//	vector-recall   embedding-backed retrieval of prior turns: trades tokens for a retrieval step.
//	entity-memory   structured key facts: trades generality for precision on the things that matter.
//
// 🚫 None of these is a running service. A strategy is a DESCRIPTION — a name plus validated params that
// a `config_hash` can pin. The store that would implement `vector-recall` at run time is a future
// memory-runtime phase's job, and the transform refuses a memory change until it exists (decisions.md D4).
// Shipping the vocabulary now is what lets a configuration be authored, hashed, and compared in the
// meantime; it is not a claim that the strategy runs.

// MemoryStrategySetVersion is the version of the builtin strategy vocabulary. A stored memory entry's
// strategy name is interpretable only against this version — bump it when the set changes, exactly as
// TaxonomyVersion is bumped when the pattern vocabulary changes.
const MemoryStrategySetVersion = "1.0.0"

// MemoryStrategySetSize is the fixed cardinality of the vocabulary at MemoryStrategySetVersion. It is
// asserted against len(BuiltinMemoryStrategies()) in a test: a sixth strategy added without a version bump
// fails loudly rather than silently changing what a stored strategy name means.
const MemoryStrategySetSize = 5

// StrategyNone is the identity strategy. A node whose strategy is `none` carries no memory, and its
// resolved projection is EMPTY — byte-identical to a node that declares no memory strategy at all
// (decisions.md D3). The constant exists so that identity is written once and compared everywhere,
// rather than being a string literal repeated across resolve, transform, discovery, and the console.
const StrategyNone = "none"

// MemoryStrategy is one memory strategy in the closed builtin set.
//
// It mirrors Policy (context.go) deliberately: a Name the entry's spec selects, a ParamsSchema its params
// must satisfy so `{"max_tokens":"lots"}` is rejected when the entry is REGISTERED rather than when a run
// reaches the node, and — unlike Policy — a Title and Description.
//
// 🔴 Title/Description are part of the interface, not console decoration. The wire name, the strategy
// entity, and the human label are three separate layers, and collapsing them is how a rename becomes a
// breaking change to stored hashes. `summary-buffer` is the wire name forever; "Rolling summary" is what a
// person reads, and it may be reworded freely.
//
// 🚫 There is no Apply/Read/Write method here, and its absence is honest rather than incomplete: at M20 no
// memory RUNTIME exists, so a method would have to be a stub, and a stub in an interface is a promise the
// package cannot keep. When a memory-runtime phase lands, it adds the operation — the same way P3 added
// Assemble to Policy after P2 shipped the interface without it.
type MemoryStrategy interface {
	Name() string
	Title() string
	Description() string
	ParamsSchema() json.RawMessage
}

// BuiltinMemoryStrategies returns every builtin memory strategy, in a stable order. Adding one is a new
// entry here AND a MemoryStrategySetSize bump AND a MemoryStrategySetVersion bump — the cardinality
// assertion exists to make skipping the second two impossible to do quietly.
func BuiltinMemoryStrategies() []MemoryStrategy {
	return []MemoryStrategy{
		NoneMemory{},
		ScratchpadMemory{},
		SummaryBufferMemory{},
		VectorRecallMemory{},
		EntityMemory{},
	}
}

// ── none — the identity ──────────────────────────────────────────────────────────────────────────

// NoneMemory is the absence of memory, named. It is a real member of the vocabulary rather than a null,
// because "this node deliberately carries nothing across turns" is a configuration a user may want to
// state, compare, and pin — and because the baseline every other strategy is scored against has to be
// expressible.
type NoneMemory struct{}

func (NoneMemory) Name() string  { return StrategyNone }
func (NoneMemory) Title() string { return "No memory" }
func (NoneMemory) Description() string {
	return "The node carries nothing across invocations. Every call starts from the same blank state — " +
		"the baseline the other strategies are measured against."
}

// ParamsSchema takes no params and says so precisely: additionalProperties:false means `{"max_tokens":10}`
// on `none` is a loud error at registration rather than a param silently ignored — the exact mistake
// someone makes when they think they selected a different strategy.
func (NoneMemory) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false}`)
}

// ── scratchpad — ephemeral working notes ─────────────────────────────────────────────────────────

// ScratchpadMemory keeps recent working notes verbatim, bounded by entry count. Lossless per entry and
// lossy at the boundary: the oldest note is dropped whole rather than compressed, which is the trade that
// distinguishes it from summary-buffer.
type ScratchpadMemory struct{}

func (ScratchpadMemory) Name() string  { return "scratchpad" }
func (ScratchpadMemory) Title() string { return "Scratchpad" }
func (ScratchpadMemory) Description() string {
	return "Recent working notes, kept verbatim and bounded by count. The oldest note is dropped whole " +
		"rather than summarized, so what survives is exact and what is lost is gone."
}

func (ScratchpadMemory) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_entries":{"type":"integer","minimum":1,"description":"How many notes to retain before the oldest is dropped."}
		},
		"required":["max_entries"],
		"additionalProperties":false
	}`)
}

// ── summary-buffer — fidelity traded for tokens ──────────────────────────────────────────────────

// SummaryBufferMemory keeps a rolling summary of everything older than the retained tail. It is the
// token-cheapest of the retaining strategies and the most lossy in a way that cannot be audited from the
// configuration alone — which is precisely why it is a hypothesis for the harness rather than a default.
type SummaryBufferMemory struct{}

func (SummaryBufferMemory) Name() string  { return "summary-buffer" }
func (SummaryBufferMemory) Title() string { return "Rolling summary" }
func (SummaryBufferMemory) Description() string {
	return "Everything older than the retained tail is folded into a rolling summary. Cheapest in tokens " +
		"and the most lossy: what the summary omits cannot be recovered from the configuration."
}

func (SummaryBufferMemory) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_tokens":{"type":"integer","minimum":1,"description":"Token budget for the rolling summary."},
			"keep_last_turns":{"type":"integer","minimum":0,"description":"Turns kept verbatim before the summary begins."}
		},
		"required":["max_tokens"],
		"additionalProperties":false
	}`)
}

// ── vector-recall — tokens traded for a retrieval step ───────────────────────────────────────────

// VectorRecallMemory retrieves prior turns by embedding similarity.
//
// 🔴 embedding_ref is REQUIRED and is a reference, not a model name. Which embedding produced the vectors
// is part of what makes a recall reproducible: the same top_k over vectors from a different embedding is a
// different configuration that would score differently, so a config_hash that did not pin it would claim
// two different computations were one.
type VectorRecallMemory struct{}

func (VectorRecallMemory) Name() string  { return "vector-recall" }
func (VectorRecallMemory) Title() string { return "Vector recall" }
func (VectorRecallMemory) Description() string {
	return "Prior turns are retrieved by embedding similarity rather than recency. Pays a retrieval step " +
		"to avoid carrying everything, and is only as reproducible as the embedding it pins."
}

func (VectorRecallMemory) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"top_k":{"type":"integer","minimum":1,"description":"How many prior turns to recall per invocation."},
			"embedding_ref":{"type":"string","minLength":1,"description":"The embedding that produced the vectors; pinned because recall is only reproducible against one embedding."}
		},
		"required":["top_k","embedding_ref"],
		"additionalProperties":false
	}`)
}

// ── entity-memory — generality traded for precision ──────────────────────────────────────────────

// EntityMemory keeps structured facts about named entities and nothing else. The narrowest strategy and
// the only one whose loss is legible from the configuration: it carries exactly the declared keys, so what
// it will and will not remember is readable without running it.
type EntityMemory struct{}

func (EntityMemory) Name() string  { return "entity-memory" }
func (EntityMemory) Title() string { return "Entity facts" }
func (EntityMemory) Description() string {
	return "Structured facts about named entities, and nothing else. The narrowest strategy, and the only " +
		"one whose loss is readable from the configuration: it carries exactly the keys it declares."
}

func (EntityMemory) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"entity_keys":{"type":"array","minItems":1,"items":{"type":"string","minLength":1},"description":"The entity attributes carried across invocations."}
		},
		"required":["entity_keys"],
		"additionalProperties":false
	}`)
}
