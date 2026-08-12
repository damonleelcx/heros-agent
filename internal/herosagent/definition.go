package herosagent

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/confighash"
	"github.com/heros-foreal/agentd/internal/harnessruntime"
	"github.com/heros-foreal/agentd/internal/variantspec"
)

// NodeID is the single node HEROS's spec describes.
//
// 🔴 One node, and that is what makes the wiring axis vacuous rather than merely unused: there is no
// second node to order it against. A spec carrying an ordering would hash a configuration nobody can
// execute — see ErrWiringOverride.
const NodeID = "heros_analyst"

// Axis names the six dimensions an operator may author, plus the one they may not.
//
// It is a type rather than bare strings because every refusal in this package NAMES THE AXIS, and a
// refusal that says "invalid override" sends an operator to read the whole form.
type Axis string

const (
	AxisPrompt  Axis = "prompt"
	AxisModel   Axis = "model"
	AxisSkills  Axis = "skills"
	AxisTools   Axis = "tools"
	AxisContext Axis = "context"
	AxisMemory  Axis = "memory"
	AxisHarness Axis = "harness"
	// AxisWiring is FIXED. It is in the vocabulary precisely so the console can render it read-only
	// with a reason, rather than omitting it — a hidden axis is indistinguishable from one that does
	// not exist (the argument D11 makes about unavailable harness strategies, applied to an axis).
	AxisWiring Axis = "wiring"
)

// AuthorableAxes are the six an operator may set, in the order the console renders them.
func AuthorableAxes() []Axis {
	return []Axis{AxisPrompt, AxisModel, AxisSkills, AxisTools, AxisContext, AxisMemory, AxisHarness}
}

// Definition is HEROS as an operator authored it: references into the P2 registries, one per axis.
//
// 🔴 EVERY FIELD IS A REFERENCE, never an inlined value. A definition that inlined a prompt body or a
// strategy's params would be a configuration whose content lives outside any registry, so it could
// never be resolved back from a `config_hash` months later — the same rule `variantspec` enforces with
// ErrInlineDefinition for every ref it resolves.
//
// 🚫 There is no field here that can hold a provider key. See CredentialRef, and see the reflective
// fence that discovers this rather than reading a list.
type Definition struct {
	// PromptRef is a prompt-registry version_id. The template body and its derived slots live there.
	PromptRef string `json:"prompt_ref"`
	// ModelRef is a model id in the OPERATOR registry (internal/adminstore), validated at publish.
	ModelRef string `json:"model_ref"`
	// CredentialRef is a PROVIDER NAME — `anthropic`, `openai` — resolved through providergateway's
	// configured Secrets source at USE.
	//
	// 🔴 Never a key value, and the type system cannot say so, which is why Validate refuses anything
	// key-shaped and a reflective fence asserts no field in this package could hold one. D5: "It puts
	// plaintext keys in request bodies and audit trails and duplicates a secret store the deployment
	// already runs. Level 1 on the ladder is not tradeable against the convenience of a text field."
	CredentialRef string `json:"credential_ref"`
	// SkillRefs are skill-registry version_ids, in binding order — order is identity-bearing, exactly
	// as it is on a NodeOverride, because the call site binds them in that order.
	SkillRefs []string `json:"skill_refs,omitempty"`
	// ToolNames are entries in the tool index, as a SET. Sorted into the hash, because two definitions
	// that bind the same tools in different authoring order denote one configuration.
	ToolNames []string `json:"tool_names,omitempty"`
	// ContextRef is a context-registry version_id naming a sealed (policy, params) entry.
	ContextRef string `json:"context_ref"`
	// MemoryRef is a memory-registry version_id.
	MemoryRef string `json:"memory_ref,omitempty"`
	// HarnessRef is a harness-registry version_id.
	HarnessRef string `json:"harness_ref"`
	// CriticModelRef and CriticCredentialRef are set ONLY when the harness strategy is critic-loop.
	//
	// 🔴 A second model is a SECOND COST and a SECOND CREDENTIAL, and FR1.9 makes all three visible
	// rather than letting a dropdown quietly double the cost of an analysis. They are separate fields
	// rather than a reuse of ModelRef/CredentialRef so the spend meter can attribute them separately.
	CriticModelRef      string `json:"critic_model_ref,omitempty"`
	CriticCredentialRef string `json:"critic_credential_ref,omitempty"`
	// SetVersions records the VERSION of every closed vocabulary this definition references (task
	// 6b.14). Without it a stored `config_hash` stops being interpretable the moment a set is versioned
	// forward: `single-shot` in a definition published today and `single-shot` after the set gains a
	// member are the same string naming two different vocabularies.
	SetVersions map[string]string `json:"set_versions,omitempty"`
}

// Validate checks everything checkable WITHOUT the registries, the model catalogue or the secrets
// source — the same split `variantspec.Validate` makes, and for the same reason: "your definition is
// malformed" and "your definition is fine but points at something that isn't there" are different
// answers with different next actions.
func (d Definition) Validate() error {
	var problems []string
	need := func(ref string, axis Axis) {
		if strings.TrimSpace(ref) == "" {
			problems = append(problems, fmt.Sprintf("the %s axis is unset", axis))
		}
	}
	need(d.PromptRef, AxisPrompt)
	need(d.ModelRef, AxisModel)
	need(d.ContextRef, AxisContext)
	need(d.HarnessRef, AxisHarness)

	if strings.TrimSpace(d.CredentialRef) == "" {
		problems = append(problems, "the credential reference is unset — HEROS binds a PROVIDER NAME, "+
			"and an unset one would send an unauthenticated call rather than failing closed")
	}
	// 🔴 The key-shaped refusal SHORT-CIRCUITS, and that is not a style choice.
	//
	// Two reasons. It must be matchable with errors.Is at the call site — a caller deciding whether to
	// tell an operator "you pasted a secret into a name field" cannot do it on a substring, and folding
	// this into the joined `problems` string would lose the wrap chain. And a pasted key is the ONE
	// problem worth reporting alone: burying it under "the prompt axis is unset" invites somebody to
	// fix the prompt and resubmit the key.
	for field, value := range map[string]string{
		"credential_ref":        d.CredentialRef,
		"critic_credential_ref": d.CriticCredentialRef,
	} {
		if err := refuseKeyShaped(field, value); err != nil {
			return err
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidDefinition, strings.Join(problems, "; "))
	}
	return nil
}

// keyShapedPrefixes are the vendor prefixes a pasted key actually carries. A provider NAME never does.
var keyShapedPrefixes = []string{"sk-", "sk_", "pk-", "pk_", "xai-", "gsk_", "ghp_", "api-", "Bearer "}

// refuseKeyShaped rejects a value that is a KEY where a provider NAME belongs (D5).
//
// 🔴 Heuristic, and deliberately so, because the alternative is worse in one specific direction: there
// is no way to be certain a string is not a key, and a definition that ACCEPTS one has already written
// it to a request body and an audit trail by the time anybody notices. A false refusal costs an
// operator a confusing minute; a false acceptance is a credential in a database column.
//
// It is not the whole mechanism — the mechanism is that no field here is FOR a key, and the store has
// no column one could occupy. This is the second layer, for the case somebody pastes into the right
// field's wrong box.
func refuseKeyShaped(field, value string) error {
	v := strings.TrimSpace(value)
	if v == "" {
		return nil
	}
	for _, p := range keyShapedPrefixes {
		if strings.HasPrefix(v, p) {
			return fmt.Errorf("%w: %s looks like a key value (it begins %q). Bind the PROVIDER NAME — "+
				"the key is resolved at use from the deployment's configured secrets source and never "+
				"reaches this platform", ErrKeyValueOffered, field, p)
		}
	}
	// A provider name is an identifier. Anything long enough to be a secret is not one.
	if len(v) > 64 {
		return fmt.Errorf("%w: %s is %d characters. A provider name is an identifier like `anthropic`; "+
			"a value this long is a secret, and secrets are resolved at use rather than stored here",
			ErrKeyValueOffered, field, len(v))
	}
	if strings.ContainsAny(v, " \t\n") {
		return fmt.Errorf("%w: %s contains whitespace, which no provider name does", ErrKeyValueOffered, field)
	}
	return nil
}

// Spec projects the definition as a Variant Spec — the platform's own vocabulary, per D1.
//
// One node, no ordering, no edges. `WorkflowID` names the agent rather than a customer workflow,
// because this spec describes the ANALYSER and not anything it analyses.
func (d Definition) Spec() variantspec.VariantSpec {
	tools := append([]string(nil), d.ToolNames...)
	// A SET: two definitions binding the same tools in different authoring order denote one
	// configuration and must share a hash. SkillRefs is deliberately NOT sorted — its order is
	// identity-bearing, exactly as on a NodeOverride.
	sort.Strings(tools)
	return variantspec.VariantSpec{
		WorkflowID: "heros",
		// 🚫 EMPTY, and it is the whole of task 3.2. HEROS is one node; an ordering here would hash a
		// configuration nobody can execute, and accepting one from an operator would make the wiring
		// axis look authorable when it is not.
		Order: []string{NodeID},
		Nodes: map[string]variantspec.NodeOverride{
			NodeID: {
				ModelRef:      d.ModelRef,
				PromptRef:     d.PromptRef,
				SkillRefs:     d.SkillRefs,
				ContextPolicy: d.ContextRef,
				MemoryRef:     d.MemoryRef,
				HarnessRef:    d.HarnessRef,
				ToolSelection: tools,
			},
		},
	}
}

// ConfigHash is the definition's identity: content determines it, so there is no mutation API and
// re-publishing an identical definition is the same row rather than a second one (task 3.3).
//
// It hashes the DEFINITION rather than the projected spec, and the difference matters: the credential
// reference and the critic's model are part of what this agent IS — a definition that swapped its
// credential for another provider's is a different agent — and neither has a home on a NodeOverride.
// SetVersions is hashed too, so a definition published against one vocabulary and an identical-looking
// one published after the set moved forward are distinguishable (task 10.16).
func (d Definition) ConfigHash() (string, error) {
	b, err := json.Marshal(d.canonical())
	if err != nil {
		return "", fmt.Errorf("herosagent: encoding the definition for hashing: %w", err)
	}
	// SumBytes rather than Sum: it canonicalises the JSON (RFC 8785 ordering, minimal escaping) exactly
	// as every other config_hash in this product is computed, so two definitions that differ only in
	// key order or number spelling cannot hash differently. `Sum` walks a Go value and accepts only
	// []any/map[string]any, which would force this projection into `any` and lose its field names.
	h, err := confighash.SumBytes(b)
	if err != nil {
		return "", fmt.Errorf("herosagent: hashing the definition: %w", err)
	}
	return h, nil
}

// canonicalDefinition is the HASHED projection — every set sorted, every absent value spelled one way,
// so two definitions that denote one configuration cannot differ in their bytes.
//
// A separate type rather than hashing Definition directly, because the two have different jobs: the
// Definition is what an operator authored and what a store round-trips; this is what identity is
// computed over, and it normalises. A field added to Definition that is NOT identity-bearing (a note,
// an author) must not silently join the hash.
type canonicalDefinition struct {
	PromptRef           string   `json:"prompt_ref"`
	ModelRef            string   `json:"model_ref"`
	CredentialRef       string   `json:"credential_ref"`
	SkillRefs           []string `json:"skill_refs"`
	ToolNames           []string `json:"tool_names"`
	ContextRef          string   `json:"context_ref"`
	MemoryRef           string   `json:"memory_ref"`
	HarnessRef          string   `json:"harness_ref"`
	CriticModelRef      string   `json:"critic_model_ref"`
	CriticCredentialRef string   `json:"critic_credential_ref"`
	// SetVersions as a sorted []string of "name=version": flat, hashable, and readable in a stored
	// spec — which matters, because this is the field somebody reads when asking why two hashes differ.
	SetVersions []string `json:"set_versions"`
}

func (d Definition) canonical() canonicalDefinition {
	// A SET: two definitions binding the same tools in different authoring order denote one
	// configuration and must share a hash.
	tools := append([]string(nil), d.ToolNames...)
	sort.Strings(tools)
	if tools == nil {
		tools = []string{}
	}
	// 🚫 SkillRefs is NOT sorted. Its order is identity-bearing — the call site binds them in that
	// order — exactly as on a NodeOverride.
	skills := append([]string(nil), d.SkillRefs...)
	if skills == nil {
		skills = []string{}
	}
	setKeys := make([]string, 0, len(d.SetVersions))
	for k := range d.SetVersions {
		setKeys = append(setKeys, k)
	}
	sort.Strings(setKeys)
	sets := make([]string, 0, len(setKeys))
	for _, k := range setKeys {
		sets = append(sets, k+"="+d.SetVersions[k])
	}
	return canonicalDefinition{
		PromptRef: d.PromptRef, ModelRef: d.ModelRef, CredentialRef: d.CredentialRef,
		SkillRefs: skills, ToolNames: tools, ContextRef: d.ContextRef,
		MemoryRef: d.MemoryRef, HarnessRef: d.HarnessRef,
		CriticModelRef: d.CriticModelRef, CriticCredentialRef: d.CriticCredentialRef,
		SetVersions: sets,
	}
}

// RunnerHosts declares which host services the HEROS RUNNER actually supplies.
//
// 🔴 It is computed from the runner rather than declared as a policy, because that is the only version
// of this that cannot rot: the day the runner gains a tool executor, `react-loop` becomes available
// with no edit to a list somebody has to remember. D11's whole argument is that the console must
// compute availability from what the runner supplies, and this is the value it computes it from.
type RunnerHosts struct {
	// ToolInvoker: the runner can execute a tool the model asked for. FALSE in P30.
	ToolInvoker bool
	// Planner: the runner can produce and execute a plan. FALSE in P30.
	Planner bool
	// Critic: the runner can call a separate critic model. Becomes true when a critic model and its
	// credential are bound — which is a second cost and a second credential, made visible rather than
	// inherited from a dropdown.
	Critic bool
	// Summarizer: the runner can summarise, which is a MODEL CALL and therefore a second spend line.
	Summarizer bool
	// Embedder + a pinned embedding_ref: required by vector-recall, which "is only reproducible
	// against a pinned embedding" — the same reasoning as D2, arrived at independently by
	// memoryruntime.
	Embedder bool
	// EmbeddingRef is the pinned embedding this deployment recalls against. Empty means unpinned, and
	// vector-recall is refused without it even when an embedder exists.
	EmbeddingRef string
}

// Availability is one strategy's availability, with the service it would need.
//
// 🚫 An unavailable strategy is SHOWN, not hidden. "A hidden option is indistinguishable from one that
// does not exist" (D11), and an operator who cannot see `react-loop` cannot ask for the tool executor
// that would enable it.
type Availability struct {
	Name string `json:"name"`
	// Available reports whether the runner supplies what this strategy needs.
	Available bool `json:"available"`
	// Needs names the host service, in words an operator can act on. Empty when the strategy needs
	// none.
	Needs string `json:"needs,omitempty"`
	// Reason explains an unavailability, including what supplying the service means. 🚫 It never
	// suggests a neighbouring strategy: the whole point of the runtime's refusal is that the neighbour
	// is a DIFFERENT strategy.
	Reason string `json:"reason,omitempty"`
	// SecondSpendLine marks a strategy whose availability costs a second metered model call. `critic-loop`
	// and `summary-buffer` both do, and a dropdown that did not say so would quietly double a bill.
	SecondSpendLine bool `json:"second_spend_line,omitempty"`
}

// HarnessAvailability computes availability for each of the five builtin harness strategies.
func HarnessAvailability(h RunnerHosts) []Availability {
	out := make([]Availability, 0, len(harnessruntime.StrategyNames()))
	for _, name := range harnessruntime.StrategyNames() {
		svc := harnessruntime.HostServiceFor(name)
		a := Availability{Name: name, Needs: string(svc), Available: true}
		switch svc {
		case harnessruntime.HostNone:
			// single-shot and reflexion need no second actor.
		case harnessruntime.HostToolInvoker:
			a.Available = h.ToolInvoker
			a.Reason = "This strategy continues by RUNNING the tool the model asked for. Supplying it " +
				"means giving the HEROS runner a tool executor — which this phase's runner does not have. " +
				"🚫 It is not degraded to a loop that skips the tool: that loop is a different strategy."
		case harnessruntime.HostPlanner:
			a.Available = h.Planner
			a.Reason = "This strategy's first turn produces a plan and the rest EXECUTE its steps. " +
				"Supplying it means giving the runner a planner and step executor, which this phase's " +
				"runner does not have. 🚫 It is not degraded to re-asking the same question."
		case harnessruntime.HostCritic:
			a.Available = h.Critic
			a.SecondSpendLine = true
			a.Reason = "This strategy calls a SEPARATE model to judge the answer. Supplying it means " +
				"binding a critic model and its own credential — a second model is a second cost and a " +
				"second credential resolution. 🚫 It is not degraded to self-critique: a critic-loop " +
				"without a critic IS reflexion, and running it under critic-loop's config_hash would " +
				"report one strategy as another."
		}
		if a.Available {
			a.Reason = ""
		}
		out = append(out, a)
	}
	return out
}

// MemoryStrategyNames is the five-member set, in the order the console renders them. A literal list
// because `internal/registry`'s builtins are the source of truth for their PARAMS, and this is the
// order and nothing else.
func MemoryStrategyNames() []string {
	return []string{"none", "scratchpad", "entity-memory", "summary-buffer", "vector-recall"}
}

// MemoryAvailability computes availability for the five memory strategies (task 6b.8a).
func MemoryAvailability(h RunnerHosts) []Availability {
	out := make([]Availability, 0, 5)
	for _, name := range MemoryStrategyNames() {
		a := Availability{Name: name, Available: true}
		switch name {
		case "none", "scratchpad", "entity-memory":
			// No host service. Available.
		case "summary-buffer":
			a.Needs = "summarizer"
			a.Available = h.Summarizer
			a.SecondSpendLine = true
			a.Reason = "A rolling summary is produced by a MODEL CALL, so this strategy adds a second " +
				"metered spend line to every analysis. Supplying it means giving the runner a summarizer. " +
				"🚫 It never degrades: a summary-buffer that quietly truncates IS scratchpad, running " +
				"under the wrong config_hash."
		case "vector-recall":
			a.Needs = "embedder and a pinned embedding_ref"
			a.Available = h.Embedder && h.EmbeddingRef != ""
			switch {
			case !h.Embedder:
				a.Reason = "Recall is embedding-backed, and no embedder was supplied. 🚫 It never " +
					"degrades to recency: that is a different retrieval and would report itself as this one."
			case h.EmbeddingRef == "":
				a.Reason = "An embedder is available and no embedding_ref is PINNED. Recall is only " +
					"reproducible against a pinned embedding — the same argument D2 makes about the " +
					"inference key, arrived at independently by memoryruntime."
			}
		}
		if a.Available {
			a.Reason = ""
		}
		out = append(out, a)
	}
	return out
}

// RequireAvailable refuses a selection whose host service is not supplied, NAMING the service and what
// supplying it means (tasks 6b.11, 6b.20).
//
// 🚫 It never offers a neighbouring strategy as a substitute.
func RequireAvailable(kind string, name string, list []Availability) error {
	for _, a := range list {
		if a.Name != name {
			continue
		}
		if a.Available {
			return nil
		}
		return fmt.Errorf("%w: %s strategy %q needs %s. %s",
			ErrHostServiceMissing, kind, name, a.Needs, a.Reason)
	}
	return fmt.Errorf("%w: %q is not a %s strategy this deployment knows", ErrInvalidDefinition, name, kind)
}

// AxisEdit is one axis an operator set, as the console and the API submit it.
//
// A map keyed by Axis rather than a struct with seven fields, for one reason that matters: an edit
// that names an axis this platform does not author must be REFUSED BY NAME, and a struct silently
// drops an unknown JSON key. The failure a struct produces is a definition published without the thing
// the operator thought they set.
type AxisEdit map[Axis]string

// ListEdit carries the two axes whose value is a list.
type ListEdit struct {
	SkillRefs []string
	ToolNames []string
}

// DefinitionFromAxes assembles a Definition from an operator's edit, refusing what it may not accept.
//
// 🔴 TASK 3.2 — A WIRING OVERRIDE IS REFUSED, AND THE REFUSAL NAMES THE AXIS.
//
// HEROS is one node. There is no ordering to author, so an ordering in an edit is not merely ignorable:
// accepting it would hash a configuration nobody can execute, and DROPPING it silently would let an
// operator believe they had changed something. The console renders this axis fixed and read-only
// (task 6b.13); this is the half that holds when the request does not come from the console.
func DefinitionFromAxes(edit AxisEdit, lists ListEdit, credentialRef string) (Definition, error) {
	if _, present := edit[AxisWiring]; present {
		return Definition{}, fmt.Errorf("%w: the `%s` axis was set. HEROS is a single node — there is no "+
			"ordering to author — so a wiring override would hash a configuration nothing can execute. "+
			"It is rendered fixed and read-only rather than hidden, because a hidden axis is "+
			"indistinguishable from one that does not exist", ErrWiringOverride, AxisWiring)
	}
	authorable := map[Axis]bool{}
	for _, a := range AuthorableAxes() {
		authorable[a] = true
	}
	unknown := make([]string, 0)
	for a := range edit {
		if !authorable[a] {
			unknown = append(unknown, string(a))
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return Definition{}, fmt.Errorf("%w: this edit names axis/axes %s, which this platform does not "+
			"author. An unknown key silently dropped is a definition published without the thing the "+
			"operator thought they set", ErrInvalidDefinition, strings.Join(unknown, ", "))
	}
	return Definition{
		PromptRef:     edit[AxisPrompt],
		ModelRef:      edit[AxisModel],
		ContextRef:    edit[AxisContext],
		MemoryRef:     edit[AxisMemory],
		HarnessRef:    edit[AxisHarness],
		SkillRefs:     lists.SkillRefs,
		ToolNames:     lists.ToolNames,
		CredentialRef: credentialRef,
	}, nil
}
