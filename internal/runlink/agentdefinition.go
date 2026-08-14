package runlink

// agentdefinition.go is the one thing that travels PLATFORM → CUSTOMER on this seam: enough of the
// active agent definition for a customer-placed tenant to run it themselves (P30 task 7.1).
//
// # Why the definition has to cross at all, when D1 says it is operator-only
//
// D1 leaves "whether HEROS's definition is visible to customers" undecided and calls it operator-only in
// P30. That governs SURFACES — no customer console renders it — and it cannot govern this, because D6
// says a `customer`-placed tenant runs the platform's own definition on their own machine. There is no
// way to execute a prompt on a machine without the prompt being on that machine. That is inherent to
// the placement, not something this file introduces.
//
// What IS a decision here: the definition is served ONLY to a tenant whose placement is `customer`. A
// `platform`-placed or `disabled` tenant asking for it gets its placement and nothing else. The prompt
// crosses exactly where executing it is the point, and nowhere else.
//
// # 🚫 What is not here and has no field to occupy
//
// A provider key. `Provider` is a NAME — `anthropic`, `openai` — and the customer's own secrets source
// resolves it on their machine. The platform holds no customer provider key under any placement (Q1),
// and this direction of travel is where somebody would be tempted to "help" by shipping one down.

// AgentDefinitionPath is where a CLI fetches the active definition. FLAT and `Exact`-publishable, per
// P29's lesson: an identifier in the path is an identifier the ingress cannot match on.
const AgentDefinitionPath = "/api/v1/agent-definition"

// AgentDefinitionContractVersion versions this response independently of the payloads that go the other
// way. They move for different reasons.
const AgentDefinitionContractVersion = "p30.agent-definition.v1"

// AgentDefinition is what a customer-side runner needs to run the platform's agent.
type AgentDefinition struct {
	ContractVersion string `json:"contract_version"`

	// Placement is the tenant's setting, ALWAYS present — including when it refuses everything below.
	// A CLI that got a 403 with no placement could not tell "you are disabled" from "you are analysed
	// platform-side", and those send a developer to different places.
	Placement string `json:"placement"`

	// ConfigHash identifies the definition. The value the submission comes back under, and the value the
	// ingest refuses by name when it names nothing published.
	//
	// Absent — with the fields below — when the placement is not `customer`.
	ConfigHash string `json:"config_hash,omitempty"`

	// Prompt is the fully rendered system instruction, resolved from the prompt registry version the
	// definition binds. Rendered PLATFORM-SIDE so the customer's machine holds no template engine and
	// no registry: two renderers is two prompts, which is the skew D6 is about, one layer up from the
	// context assembler.
	Prompt string `json:"prompt,omitempty"`

	// Provider is the provider NAME the customer must have a credential for — never a key value, and
	// there is no field here one could occupy.
	Provider string `json:"provider,omitempty"`
	// ModelID is the model the definition binds, resolved out of the operator registry so the customer's
	// machine does not need to read it.
	ModelID string `json:"model_id,omitempty"`

	// ConfidenceFloor is the PLATFORM's floor. Sent so a customer-side run declines the same things a
	// platform-side one would; the ingest applies it again on arrival, because a floor enforced only
	// where the number is produced binds everyone except the participant who would gain by ignoring it.
	ConfidenceFloor float64 `json:"confidence_floor,omitempty"`

	// MaxTokens and MaxWallSeconds are the per-run budget. A customer spending their own credential
	// still gets a ceiling: an unbounded run is how a repository-shaped cost arrives on a bill nobody
	// approved, and whose bill it is does not change that.
	//
	// 🔴 NOT the model's `max_tokens`. This one is CUMULATIVE across the whole analysis and is checked
	// against `usage.InputTokens + usage.OutputTokens` after each call; the model's is a ceiling on ONE
	// completion and goes on the wire to the vendor. They are different numbers with the same name, and
	// `ModelParams` below carries the other one. Conflating them would either truncate every answer to
	// a run budget or let one completion spend the entire run.
	MaxTokens      int `json:"max_tokens,omitempty"`
	MaxWallSeconds int `json:"max_wall_seconds,omitempty"`

	// ModelParams are the inference parameters PINNED on the model version this definition binds.
	//
	// 🔴 Why they have to cross. `ModelSpec` bundles provider, model id and params as ONE versioned
	// unit precisely so that a `model_ref` resolves all of them exactly as stored — that is what makes
	// a `config_hash` fully determine a run. This seam used to send the first two and drop the third,
	// so a customer-placed run used the operator's model with NOBODY's parameters: the temperature and
	// seed an operator pinned were silently ignored, and the run did not match the definition its own
	// config_hash names.
	//
	// For Anthropic it was not silent, it was fatal. `providergateway` refuses a call whose spec sets
	// no `max_tokens` rather than inventing a ceiling on somebody's bill, so `heros analyse` against
	// any `claude-*` model failed at the first inference — on the customer's machine, after the CLI
	// had already read their repository.
	//
	// Optional, and it must stay optional: a CLI newer than its platform receives nil here, which is a
	// state to report clearly rather than a reason to refuse a definition that an OpenAI model runs
	// fine. See `analyse.go`, which names the platform as the thing to upgrade.
	ModelParams *ModelParams `json:"model_params,omitempty"`
}

// ModelParams is the wire form of the inference parameters a model version pins.
//
// 🚫 Deliberately NOT `registry.ModelParams`. This is a versioned CONTRACT across a trust boundary and
// the registry type is an internal domain type; sharing one struct would make any future field on the
// domain type an unversioned change to what the platform sends every customer CLI. The mapping lives in
// exactly one place, the adapter that already resolves the ref.
//
// Every field is a pointer because "unset" and "zero" are different: a temperature of 0 is a real,
// deliberate setting, and a `max_tokens` of 0 is not a ceiling.
type ModelParams struct {
	MaxTokens      *int     `json:"max_tokens,omitempty"`
	Temperature    *float64 `json:"temperature,omitempty"`
	ThinkingBudget *int     `json:"thinking_budget,omitempty"`
	Seed           *int64   `json:"seed,omitempty"`
	TimeoutSeconds *int     `json:"timeout_seconds,omitempty"`
}

// Runnable reports whether this response actually carries a definition to run.
//
// 🔴 It checks the FIELDS rather than the placement, and the difference is what it catches: a platform
// that answered `customer` and sent an empty prompt would otherwise have a CLI proceed to call a
// provider with no instruction, which produces a bill and an answer to a question nobody asked.
func (d AgentDefinition) Runnable() bool {
	return d.ConfigHash != "" && d.Prompt != "" && d.Provider != "" && d.ModelID != "" &&
		d.ConfidenceFloor > 0
}
