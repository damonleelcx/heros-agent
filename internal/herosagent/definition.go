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

// DefaultNodeID is the node id a definition gets when an operator authors one without asking for more
// (design D2). It is the id the pre-P36 package constant carried, and it stays that spelling for a
// reason that is not sentiment: it is the id every stored single-node definition round-trips through,
// so changing it would move the compatibility encoding's bytes and orphan every pin filed under them.
//
// 🚫 It is NOT the node id. Node identity is DATA now (D2) — a definition declares its nodes, and this
// constant is only the default the single-node case is given.
const DefaultNodeID = "heros_analyst"

// Axis names the nine dimensions an operator may author.
//
// It is a type rather than bare strings because every refusal in this package NAMES THE AXIS, and a
// refusal that says "invalid override" sends an operator to read the whole form.
//
// 🔴 The nine are the product's nine, not this package's own list. `AuthorableAxes` derives eight of
// them from `variantspec.Dimensions()` and appends `graph`, which is spec-level rather than a
// Dimension (P34 design D3). A hand-written list here would be a tenth place the vocabulary is
// spelled, and task 10.3 is that the nine are named identically on the operator console, the customer
// console, the CLI and the docs.
type Axis string

const (
	AxisPrompt  Axis = Axis(variantspec.DimPrompt)
	AxisModel   Axis = Axis(variantspec.DimModel)
	AxisSkills  Axis = Axis(variantspec.DimSkills)
	AxisTools   Axis = Axis(variantspec.DimTools)
	AxisContext Axis = Axis(variantspec.DimContext)
	AxisMemory  Axis = Axis(variantspec.DimMemory)
	AxisHarness Axis = Axis(variantspec.DimHarness)
	// AxisLoop is the ITERATION POLICY — which control loop a node runs and what stops it. A
	// Dimension since P34 (ADR-014), and authorable here since P36.
	AxisLoop Axis = Axis(variantspec.DimLoop)
	// AxisGraph is the TOPOLOGY — the ordering, the edges, the concurrency and the merge.
	//
	// 🔴 It replaces the spelling `wiring` this package used while the axis was vacuous. It is a
	// rename to the product's own noun (task 10.3): `assessment.AxisGraph`, `variantspec`'s `graphDim`
	// and `improvementrun`'s synonym table all already say `graph`, and this package was the last
	// surface saying `wiring`. `AxisWiring` survives below as a REFUSED legacy spelling so a caller
	// submitting it is told the new name rather than told the axis is unknown.
	AxisGraph Axis = "graph"
	// AxisWiring is the pre-P36 spelling of AxisGraph, kept ONLY so an edit naming it is refused BY
	// NAME with the rename stated.
	//
	// 🚫 It is never silently translated to AxisGraph. A rename that quietly accepts the old spelling
	// is a rename that never finishes — the two names live on for ever, the noun dictionary has two
	// entries, and nobody can tell which surfaces moved.
	AxisWiring Axis = "wiring"
)

// AuthorableAxes are the NINE an operator may set, in the order the console renders them.
//
// 🔴 Nine, and `loop` and `graph` are registry references and declarations like every other axis —
// never inlined values (task 3.2). The list is DERIVED from `variantspec.Dimensions()` so a dimension
// added there cannot be silently missing here; `graph` is appended because it is a property BETWEEN
// nodes and therefore not a Dimension (P34 design D3).
func AuthorableAxes() []Axis {
	dims := variantspec.Dimensions()
	out := make([]Axis, 0, len(dims)+1)
	for _, d := range dims {
		out = append(out, Axis(d))
	}
	return append(out, AxisGraph)
}

// PerNodeAxes are the eight an operator sets ON A NODE. `graph` is the ninth and is definition-level,
// because topology is a property between nodes rather than of one.
func PerNodeAxes() []Axis {
	dims := variantspec.Dimensions()
	out := make([]Axis, 0, len(dims))
	for _, d := range dims {
		out = append(out, Axis(d))
	}
	return out
}

// Node is ONE call site of the platform's own agent: its id, and its bindings on the eight per-node
// axes.
//
// 🔴 EVERY FIELD IS A REFERENCE, never an inlined value. A node that inlined a prompt body or a
// strategy's params would be a configuration whose content lives outside any registry, so it could
// never be resolved back from a `config_hash` months later — the same rule `variantspec` enforces with
// ErrInlineDefinition for every ref it resolves.
//
// 🚫 There is no field here that can hold a provider key. See CredentialRef, and see the reflective
// fence that discovers this rather than reading a list.
type Node struct {
	// NodeID is this node's identity. DATA since P36 (D2) — it was a package constant, which is what
	// made the topology axis vacuous rather than merely unused.
	NodeID string `json:"node_id"`
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
	//
	// 🔴 PER NODE, and that is PRD §14 Q1's answer (decisions.md D-36.1). A node binds a model, a
	// model is served by ONE vendor, and a definition-level credential would force every node onto one
	// vendor — which is a topology decision made by a field that is not about topology. The
	// CriticModelRef/CriticCredentialRef pair is the precedent: a second model already carried a
	// second credential, on the same definition, for the same reason.
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
	// HarnessRef is a harness-registry version_id. After P34 it is the ENVELOPE — the imposed policy
	// (ceilings, host services, sandbox posture) — and the chosen iteration policy is LoopRef.
	HarnessRef string `json:"harness_ref"`
	// LoopRef is a loop-registry version_id naming a sealed (strategy, params) entry — the ITERATION
	// POLICY (P34, ADR-014).
	//
	// 🔴 Additive and `omitempty`, and that is load-bearing rather than tidy: a node that binds no loop
	// emits NO `loop_ref` key, which is one of the four conditions the compatibility encoding requires
	// (D4). A definition that bound a loop cannot serialise as its pre-P36 self, because the pre-P36
	// shape had nowhere to put it.
	LoopRef string `json:"loop_ref,omitempty"`
	// CriticModelRef and CriticCredentialRef are set ONLY when this node's loop or harness strategy is
	// critic-loop.
	//
	// 🔴 A second model is a SECOND COST and a SECOND CREDENTIAL, and FR1.9 makes all three visible
	// rather than letting a dropdown quietly double the cost of an analysis. They are separate fields
	// rather than a reuse of ModelRef/CredentialRef so the spend meter can attribute them separately.
	CriticModelRef      string `json:"critic_model_ref,omitempty"`
	CriticCredentialRef string `json:"critic_credential_ref,omitempty"`
}

// Definition is HEROS as an operator authored it: one or more nodes, each a set of references into the
// P2 registries, plus the topology between them.
//
// # 🔴 THE SHAPE IS HASHED, AND THE HASH KEYS EVERY PIN
//
// Every stored inference is filed under `(workflow_id, source_revision, agent_config_hash)`. A shape
// change that moved the hash would orphan every pin — silently, with nothing going red, and with the
// console continuing to render results produced by a configuration that no longer exists. So a
// definition with exactly one node, no ordering, no edges, no graph declaration and no loop ref
// serialises and hashes EXACTLY as its pre-P36 form did. See MarshalJSON and canonical().
type Definition struct {
	// Nodes are the call sites this agent is. At least one; one is the default (D2).
	Nodes []Node `json:"nodes"`
	// Order is the sequence the runner walks. Meaningful only when there is more than one node —
	// see ErrWiringOverride, which is narrowed rather than deleted (D3).
	Order []string `json:"order,omitempty"`
	// Edges are the declared graph edges, including conditional ones. `variantspec.Edge` UNCHANGED:
	// the agent's topology is the same type a customer's Variant Spec carries, because D1 is that
	// there is one validator and a second type is how a second validator begins.
	Edges []variantspec.Edge `json:"edges,omitempty"`
	// GraphGroups declare concurrency and merge (P34 FR12/FR14). Same type, same reason.
	GraphGroups []variantspec.GraphGroup `json:"graph_groups,omitempty"`
	// SetVersions records the VERSION of every closed vocabulary this definition references (task
	// 6b.14). Without it a stored `config_hash` stops being interpretable the moment a set is versioned
	// forward: `single-shot` in a definition published today and `single-shot` after the set gains a
	// member are the same string naming two different vocabularies.
	SetVersions map[string]string `json:"set_versions,omitempty"`
}

// SingleNode builds the default definition: one node, no topology (D2).
//
// A constructor rather than a struct literal at every call site, because the single-node case must
// carry DefaultNodeID exactly — a definition whose one node has some other id cannot use the
// compatibility encoding, so it would hash differently for a reason nobody typed.
func SingleNode(n Node) Definition {
	if strings.TrimSpace(n.NodeID) == "" {
		n.NodeID = DefaultNodeID
	}
	return Definition{Nodes: []Node{n}}
}

// Primary is the definition's first node.
//
// 🔴 It is meaningful for a SINGLE-NODE definition and is a lie for any other: "the agent's model" is
// not a question a graph has an answer to. Callers that render or diff a multi-node definition iterate
// Nodes; this exists for the paths that are genuinely about the one-node default, and every one of
// them says so.
func (d Definition) Primary() Node {
	if len(d.Nodes) == 0 {
		return Node{}
	}
	return d.Nodes[0]
}

// MultiNode reports whether this definition declares a topology to author.
func (d Definition) MultiNode() bool { return len(d.Nodes) > 1 }

// Ordering is the sequence the runner walks: the declared Order, or the single node when none is
// declared. Never empty for a valid definition.
func (d Definition) Ordering() []string {
	if len(d.Order) > 0 {
		return append([]string(nil), d.Order...)
	}
	ids := make([]string, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		ids = append(ids, n.NodeID)
	}
	return ids
}

// NodeByID returns one node by id.
func (d Definition) NodeByID(id string) (Node, bool) {
	for _, n := range d.Nodes {
		if n.NodeID == id {
			return n, true
		}
	}
	return Node{}, false
}

// ── The compatibility encoding (D4, task 1.1) ───────────────────────────────────────────────────
//
// # The finding task 1.1 asks for, recorded where it will be read
//
// A nested `nodes` array CANNOT do this. The pre-P36 shape is a flat object whose keys are the axes;
// any nesting changes the bytes, and `confighash.SumBytes` canonicalises key ORDER but not structure,
// so the hash moves for every definition in existence. The question is not "can we be clever with
// omitempty" — a nested array is a different document.
//
// So a compatibility ENCODING is required, and it is the narrow one: a definition that is
// EXACTLY the pre-P36 shape — one node carrying DefaultNodeID, no ordering, no edges, no graph
// declaration, no loop ref — marshals and hashes as the flat pre-P36 document. Anything else marshals
// and hashes as the new one.
//
// 🔴 The discontinuity is deliberate and it is not a hazard. Both encodings are pure functions of
// content, so identical content still produces an identical hash; a definition that gains a second
// node changes its hash because it IS a different configuration. What must never happen — a
// definition whose content did not change acquiring a new hash because the CODE changed — is exactly
// what this prevents.

// legacyShaped reports whether this definition is expressible in the pre-P36 document.
//
// All five conditions, and each one is here because the old document had nowhere to put it.
func (d Definition) legacyShaped() bool {
	return len(d.Nodes) == 1 &&
		d.Nodes[0].NodeID == DefaultNodeID &&
		d.Nodes[0].LoopRef == "" &&
		len(d.Order) == 0 &&
		len(d.Edges) == 0 &&
		len(d.GraphGroups) == 0
}

// legacyDefinition is the pre-P36 wire document, field for field and tag for tag.
//
// 🔴 It is a FROZEN mirror. Nothing may be added to it, reordered in it, or re-tagged: its field order
// is the key order `encoding/json` emits, and `testdata/p36-pre-confighash.json` carries the bytes it
// produced in a tree that had never heard of P36.
type legacyDefinition struct {
	PromptRef           string            `json:"prompt_ref"`
	ModelRef            string            `json:"model_ref"`
	CredentialRef       string            `json:"credential_ref"`
	SkillRefs           []string          `json:"skill_refs,omitempty"`
	ToolNames           []string          `json:"tool_names,omitempty"`
	ContextRef          string            `json:"context_ref"`
	MemoryRef           string            `json:"memory_ref,omitempty"`
	HarnessRef          string            `json:"harness_ref"`
	CriticModelRef      string            `json:"critic_model_ref,omitempty"`
	CriticCredentialRef string            `json:"critic_credential_ref,omitempty"`
	SetVersions         map[string]string `json:"set_versions,omitempty"`
}

// extendedDefinition is the P36 wire document. A named type rather than marshalling Definition
// directly, because Definition has a MarshalJSON and marshalling it from inside one recurses.
type extendedDefinition struct {
	Nodes       []Node                   `json:"nodes"`
	Order       []string                 `json:"order,omitempty"`
	Edges       []variantspec.Edge       `json:"edges,omitempty"`
	GraphGroups []variantspec.GraphGroup `json:"graph_groups,omitempty"`
	SetVersions map[string]string        `json:"set_versions,omitempty"`
}

func (d Definition) legacy() legacyDefinition {
	n := d.Nodes[0]
	return legacyDefinition{
		PromptRef: n.PromptRef, ModelRef: n.ModelRef, CredentialRef: n.CredentialRef,
		SkillRefs: n.SkillRefs, ToolNames: n.ToolNames, ContextRef: n.ContextRef,
		MemoryRef: n.MemoryRef, HarnessRef: n.HarnessRef,
		CriticModelRef: n.CriticModelRef, CriticCredentialRef: n.CriticCredentialRef,
		SetVersions: d.SetVersions,
	}
}

// MarshalJSON emits the pre-P36 document for a pre-P36-shaped definition, and the P36 one otherwise.
func (d Definition) MarshalJSON() ([]byte, error) {
	if d.legacyShaped() {
		return json.Marshal(d.legacy())
	}
	// 🔴 A CONVERSION, not a struct literal, and it is load-bearing rather than a lint preference.
	//
	// `extendedDefinition` exists only to escape this method — marshalling `Definition` from inside its
	// own `MarshalJSON` recurses — so the two must stay field-identical. A struct literal would keep
	// compiling if somebody added a field to `Definition` and forgot this line, and the new field would
	// silently vanish from the wire. The conversion stops compiling instead, which is the outcome worth
	// having for a type whose bytes are the compatibility guarantee.
	return json.Marshal(extendedDefinition(d))
}

// UnmarshalJSON reads BOTH documents, so a `spec_json` written by the previous binary still decodes.
//
// 🔴 The discriminator is the PRESENCE of `nodes`, not a version field. A version field would be a
// second fact to keep true, and the rows that need reading were written before anybody could have set
// it — which is the whole population this has to serve.
func (d *Definition) UnmarshalJSON(b []byte) error {
	var probe struct {
		Nodes json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return fmt.Errorf("herosagent: decoding the definition: %w", err)
	}
	if len(probe.Nodes) > 0 {
		var ext extendedDefinition
		if err := json.Unmarshal(b, &ext); err != nil {
			return fmt.Errorf("herosagent: decoding the definition: %w", err)
		}
		// The same conversion in reverse, for the same reason: a field added to one shape and not the
		// other must break the build rather than be dropped in silence.
		*d = Definition(ext)
		return nil
	}
	var leg legacyDefinition
	if err := json.Unmarshal(b, &leg); err != nil {
		return fmt.Errorf("herosagent: decoding the pre-P36 definition: %w", err)
	}
	*d = Definition{
		Nodes: []Node{{
			NodeID: DefaultNodeID, PromptRef: leg.PromptRef, ModelRef: leg.ModelRef,
			CredentialRef: leg.CredentialRef, SkillRefs: leg.SkillRefs, ToolNames: leg.ToolNames,
			ContextRef: leg.ContextRef, MemoryRef: leg.MemoryRef, HarnessRef: leg.HarnessRef,
			CriticModelRef: leg.CriticModelRef, CriticCredentialRef: leg.CriticCredentialRef,
		}},
		SetVersions: leg.SetVersions,
	}
	return nil
}

// ── Validation ──────────────────────────────────────────────────────────────────────────────────

// Validate checks everything checkable WITHOUT the registries, the model catalogue or the secrets
// source — the same split `variantspec.Validate` makes, and for the same reason: "your definition is
// malformed" and "your definition is fine but points at something that isn't there" are different
// answers with different next actions.
//
// 🔴 Every refusal names the NODE as well as the axis once there is more than one node. On a
// single-node definition the node is not named, so the pre-P36 sentences are unchanged — a message
// that gained a node prefix nobody asked for would be a second thing to re-baseline in every test.
func (d Definition) Validate() error {
	if len(d.Nodes) == 0 {
		return fmt.Errorf("%w: a definition declares no nodes. A definition IS its nodes — an empty one "+
			"names a configuration with nothing to run", ErrInvalidDefinition)
	}

	// 🔴 The key-shaped refusal SHORT-CIRCUITS, and that is not a style choice.
	//
	// Two reasons. It must be matchable with errors.Is at the call site — a caller deciding whether to
	// tell an operator "you pasted a secret into a name field" cannot do it on a substring, and folding
	// this into the joined `problems` string would lose the wrap chain. And a pasted key is the ONE
	// problem worth reporting alone: burying it under "the prompt axis is unset" invites somebody to
	// fix the prompt and resubmit the key.
	//
	// 🔴 It runs over EVERY node's credential fields (task 3.3). A check that enumerated the pre-P36
	// two would pass vacuously on a definition whose second node pasted a key into its own.
	if err := d.refuseKeyShapedFields(); err != nil {
		return err
	}

	var problems []string
	seen := map[string]bool{}
	for i, n := range d.Nodes {
		where := d.nodeLabel(n, i)
		if strings.TrimSpace(n.NodeID) == "" {
			problems = append(problems, fmt.Sprintf("nodes[%d] has no node_id — a node with no identity "+
				"cannot be ordered, referenced by an edge, or named on the inference it produced", i))
		} else if seen[n.NodeID] {
			problems = append(problems, fmt.Sprintf("node %q is declared twice; two nodes of one id make "+
				"every edge naming it ambiguous", n.NodeID))
		}
		seen[n.NodeID] = true

		need := func(ref string, axis Axis) {
			if strings.TrimSpace(ref) == "" {
				problems = append(problems, fmt.Sprintf("the %s axis is unset%s", axis, where))
			}
		}
		need(n.PromptRef, AxisPrompt)
		need(n.ModelRef, AxisModel)
		need(n.ContextRef, AxisContext)
		need(n.HarnessRef, AxisHarness)
		if strings.TrimSpace(n.CredentialRef) == "" {
			problems = append(problems, "the credential reference is unset"+where+" — HEROS binds a "+
				"PROVIDER NAME, and an unset one would send an unauthenticated call rather than failing closed")
		}
	}

	if err := d.validateTopologyShape(); err != nil {
		return err
	}
	if len(problems) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidDefinition, strings.Join(problems, "; "))
	}
	return nil
}

// nodeLabel is the " on node x" suffix a refusal carries once there is more than one node.
func (d Definition) nodeLabel(n Node, i int) string {
	if len(d.Nodes) == 1 {
		return ""
	}
	if strings.TrimSpace(n.NodeID) == "" {
		return fmt.Sprintf(" on nodes[%d]", i)
	}
	return " on node " + n.NodeID
}

// refuseKeyShapedFields refuses a KEY offered anywhere a provider NAME belongs, across every node.
func (d Definition) refuseKeyShapedFields() error {
	for i, n := range d.Nodes {
		where := d.nodeLabel(n, i)
		for _, f := range []struct{ field, value string }{
			{"credential_ref" + where, n.CredentialRef},
			{"critic_credential_ref" + where, n.CriticCredentialRef},
		} {
			if err := refuseKeyShaped(f.field, f.value); err != nil {
				return err
			}
		}
	}
	return nil
}

// validateTopologyShape is D3: the wiring refusal NARROWED, not deleted.
//
// 🔴 A single-node definition still refuses an ordering, and the reason it always gave — "there is no
// second node to order it against" — is still TRUE in that case. Deleting the rule because a new case
// appeared would discard a correct check for the old case, and the old case is still the default (D2),
// so it would be discarded for the majority of definitions.
func (d Definition) validateTopologyShape() error {
	if !d.MultiNode() {
		var set []string
		if len(d.Order) > 0 {
			set = append(set, "an ordering")
		}
		if len(d.Edges) > 0 {
			set = append(set, "edges")
		}
		if len(d.GraphGroups) > 0 {
			set = append(set, "graph groups")
		}
		if len(set) == 0 {
			return nil
		}
		return fmt.Errorf("%w: this definition declares one node and carries %s. There is no second node "+
			"to order it against, so a topology here would hash a configuration nothing can execute. The "+
			"`%s` axis is rendered with this reason rather than hidden, because a hidden axis is "+
			"indistinguishable from one that does not exist. Declare a second node to author a topology",
			ErrWiringOverride, strings.Join(set, " and "), AxisGraph)
	}

	// Multi-node: the ordering is REQUIRED and must contain every node exactly once. The executor walks
	// `Order`; a node outside it is one that never runs, declared by somebody who believes it does.
	if len(d.Order) == 0 {
		return fmt.Errorf("%w: this definition declares %d nodes and no ordering. `Order` is the sequence "+
			"the runner walks and the sequence a replay visits — concurrency is declared OVER it, never "+
			"instead of it — so a multi-node definition without one has no defined execution and no "+
			"reproducible replay", ErrInvalidDefinition, len(d.Nodes))
	}
	declared := map[string]bool{}
	for _, n := range d.Nodes {
		declared[n.NodeID] = true
	}
	inOrder := map[string]bool{}
	for _, id := range d.Order {
		if !declared[id] {
			return fmt.Errorf("%w: the ordering names %q, which this definition does not declare as a node",
				ErrInvalidDefinition, id)
		}
		if inOrder[id] {
			return fmt.Errorf("%w: the ordering names %q twice, which would silently run it twice",
				ErrInvalidDefinition, id)
		}
		inOrder[id] = true
	}
	var missing []string
	for _, n := range d.Nodes {
		if !inOrder[n.NodeID] {
			missing = append(missing, n.NodeID)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("%w: node(s) %s are declared and absent from the ordering. Concurrency is "+
			"declared OVER the ordering and never instead of it: the ordering still lists every node, so "+
			"a replay visits them in that sequence even when the live run overlapped them",
			ErrInvalidDefinition, strings.Join(missing, ", "))
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
// `WorkflowID` names the agent rather than a customer workflow, because this spec describes the
// ANALYSER and not anything it analyses.
//
// 🔴 The topology travels as `variantspec.Edge` and `variantspec.GraphGroup`, UNCHANGED. That is what
// makes `ValidateTopology` a shared code path rather than a shared intention: there is no conversion
// step where the agent's edges could acquire different semantics from a customer's.
func (d Definition) Spec() variantspec.VariantSpec {
	nodes := make(map[string]variantspec.NodeOverride, len(d.Nodes))
	for _, n := range d.Nodes {
		tools := append([]string(nil), n.ToolNames...)
		// A SET: two definitions binding the same tools in different authoring order denote one
		// configuration and must share a hash. SkillRefs is deliberately NOT sorted — its order is
		// identity-bearing, exactly as on a NodeOverride.
		sort.Strings(tools)
		nodes[n.NodeID] = variantspec.NodeOverride{
			ModelRef:      n.ModelRef,
			PromptRef:     n.PromptRef,
			SkillRefs:     n.SkillRefs,
			ContextPolicy: n.ContextRef,
			MemoryRef:     n.MemoryRef,
			HarnessRef:    n.HarnessRef,
			LoopRef:       n.LoopRef,
			ToolSelection: tools,
		}
	}
	return variantspec.VariantSpec{
		WorkflowID:  "heros",
		Order:       d.Ordering(),
		Nodes:       nodes,
		Edges:       d.Edges,
		GraphGroups: d.GraphGroups,
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

// canonicalDefinition is the pre-P36 HASHED projection — every set sorted, every absent value spelled
// one way, so two definitions that denote one configuration cannot differ in their bytes.
//
// 🔴 FROZEN, exactly as legacyDefinition is, and for the same reason: `testdata/p36-pre-confighash.json`
// carries the bytes it produced in a tree that had never heard of P36, and every pinned inference in
// existence is filed under a hash of these bytes.
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

// canonicalNode is one node's hashed projection. Same normalisation rules as canonicalDefinition, one
// level down.
type canonicalNode struct {
	NodeID              string   `json:"node_id"`
	PromptRef           string   `json:"prompt_ref"`
	ModelRef            string   `json:"model_ref"`
	CredentialRef       string   `json:"credential_ref"`
	SkillRefs           []string `json:"skill_refs"`
	ToolNames           []string `json:"tool_names"`
	ContextRef          string   `json:"context_ref"`
	MemoryRef           string   `json:"memory_ref"`
	HarnessRef          string   `json:"harness_ref"`
	LoopRef             string   `json:"loop_ref"`
	CriticModelRef      string   `json:"critic_model_ref"`
	CriticCredentialRef string   `json:"critic_credential_ref"`
}

// canonicalGraph is the multi-node hashed projection.
//
// 🔴 `Order`, `Edges` and `GraphGroups` are NOT sorted. Their order is identity-bearing: a run over
// a→b→c is not the computation c→b→a, and `Order` is also the REPLAY sequence, so sorting it would
// claim two different replay sequences are one configuration. `Nodes` IS sorted by node id, because
// the node LIST is a set — the sequence lives in `Order`.
type canonicalGraph struct {
	Nodes       []canonicalNode          `json:"nodes"`
	Order       []string                 `json:"order"`
	Edges       []variantspec.Edge       `json:"edges"`
	GraphGroups []variantspec.GraphGroup `json:"graph_groups"`
	SetVersions []string                 `json:"set_versions"`
}

// canonical returns whichever hashed projection this definition's SHAPE calls for (D4).
//
// The return type is `any` because the two projections are different documents. That is the honest
// signature: a single struct able to spell both would need every graph field `omitempty`, and an
// `omitempty` slice and an absent one are the same bytes — so a definition that declared an empty
// ordering and one that declared none would hash identically, which is a distinction the ordering
// refusal depends on.
func (d Definition) canonical() any {
	if d.legacyShaped() {
		return d.canonicalLegacy()
	}
	return d.canonicalGraph()
}

func (d Definition) canonicalLegacy() canonicalDefinition {
	n := d.Nodes[0]
	return canonicalDefinition{
		PromptRef: n.PromptRef, ModelRef: n.ModelRef, CredentialRef: n.CredentialRef,
		SkillRefs: nonNilStrings(n.SkillRefs), ToolNames: sortedNonNil(n.ToolNames),
		ContextRef: n.ContextRef, MemoryRef: n.MemoryRef, HarnessRef: n.HarnessRef,
		CriticModelRef: n.CriticModelRef, CriticCredentialRef: n.CriticCredentialRef,
		SetVersions: flattenSetVersions(d.SetVersions),
	}
}

func (d Definition) canonicalGraph() canonicalGraph {
	nodes := make([]canonicalNode, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		nodes = append(nodes, canonicalNode{
			NodeID: n.NodeID, PromptRef: n.PromptRef, ModelRef: n.ModelRef,
			CredentialRef: n.CredentialRef,
			// 🚫 SkillRefs is NOT sorted. Its order is identity-bearing — the call site binds them in
			// that order — exactly as on a NodeOverride.
			SkillRefs: nonNilStrings(n.SkillRefs), ToolNames: sortedNonNil(n.ToolNames),
			ContextRef: n.ContextRef, MemoryRef: n.MemoryRef, HarnessRef: n.HarnessRef,
			LoopRef:        n.LoopRef,
			CriticModelRef: n.CriticModelRef, CriticCredentialRef: n.CriticCredentialRef,
		})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].NodeID < nodes[j].NodeID })
	edges := d.Edges
	if edges == nil {
		edges = []variantspec.Edge{}
	}
	groups := d.GraphGroups
	if groups == nil {
		groups = []variantspec.GraphGroup{}
	}
	return canonicalGraph{
		Nodes: nodes, Order: nonNilStrings(d.Ordering()), Edges: edges, GraphGroups: groups,
		SetVersions: flattenSetVersions(d.SetVersions),
	}
}

func nonNilStrings(in []string) []string {
	out := append([]string(nil), in...)
	if out == nil {
		return []string{}
	}
	return out
}

func sortedNonNil(in []string) []string {
	out := nonNilStrings(in)
	sort.Strings(out)
	return out
}

func flattenSetVersions(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+m[k])
	}
	return out
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
// A map keyed by Axis rather than a struct with N fields, for one reason that matters: an edit that
// names an axis this platform does not author must be REFUSED BY NAME, and a struct silently drops an
// unknown JSON key. The failure a struct produces is a definition published without the thing the
// operator thought they set.
type AxisEdit map[Axis]string

// ListEdit carries the two axes whose value is a list.
type ListEdit struct {
	SkillRefs []string
	ToolNames []string
}

// NodeEdit is ONE node as the console and the API submit it: its id, its eight per-node axes, and the
// credential it binds.
type NodeEdit struct {
	NodeID string
	Axes   AxisEdit
	Lists  ListEdit
	// CredentialRef is a PROVIDER NAME. Per node (decisions.md D-36.1) — a node binds a model, and a
	// model is served by one vendor.
	CredentialRef string
}

// TopologyEdit is the definition-level `graph` axis as an operator submits it.
type TopologyEdit struct {
	Order       []string
	Edges       []variantspec.Edge
	GraphGroups []variantspec.GraphGroup
}

// Declared reports whether this edit says anything at all about the topology.
func (t TopologyEdit) Declared() bool {
	return len(t.Order) > 0 || len(t.Edges) > 0 || len(t.GraphGroups) > 0
}

// DefinitionFromAxes assembles a single-node Definition from an operator's edit.
//
// 🔴 It is the SINGLE-NODE door and it stays one (D2): an operator who wants what they have today does
// not author a graph to keep it. `DefinitionFromNodes` is the multi-node door.
func DefinitionFromAxes(edit AxisEdit, lists ListEdit, credentialRef string) (Definition, error) {
	return DefinitionFromNodes([]NodeEdit{{
		NodeID: DefaultNodeID, Axes: edit, Lists: lists, CredentialRef: credentialRef,
	}}, TopologyEdit{}, nil)
}

// DefinitionFromNodes assembles a Definition from N node edits and a topology edit, refusing what it
// may not accept.
//
// 🔴 TASK 3.4 — A TOPOLOGY ON A SINGLE-NODE DEFINITION IS REFUSED, AND THE REFUSAL NAMES THE AXIS.
//
// The refusal NARROWS rather than disappears (D3). For one node there is still no ordering to author,
// so an ordering in an edit is not merely ignorable: accepting it would hash a configuration nobody can
// execute, and DROPPING it silently would let an operator believe they had changed something. The
// console renders the axis with this reason rather than hiding it; this is the half that holds when the
// request does not come from the console.
func DefinitionFromNodes(nodes []NodeEdit, topo TopologyEdit, setVersions map[string]string) (Definition, error) {
	if len(nodes) == 0 {
		return Definition{}, fmt.Errorf("%w: this edit declares no nodes. A definition IS its nodes",
			ErrInvalidDefinition)
	}

	authorable := map[Axis]bool{}
	for _, a := range PerNodeAxes() {
		authorable[a] = true
	}
	out := Definition{Nodes: make([]Node, 0, len(nodes)), SetVersions: setVersions}
	multi := len(nodes) > 1

	for i, ne := range nodes {
		where := ""
		if multi {
			where = fmt.Sprintf(" on nodes[%d]", i)
			if strings.TrimSpace(ne.NodeID) != "" {
				where = " on node " + ne.NodeID
			}
		}
		// 🔴 The `graph` axis is DEFINITION-LEVEL. Naming it inside a node's axis map is refused rather
		// than hoisted: topology is a property BETWEEN nodes (P34 design D3), and silently moving it
		// would let an operator believe one node owns the graph.
		if _, present := ne.Axes[AxisGraph]; present {
			return Definition{}, fmt.Errorf("%w: the `%s` axis was set%s. Topology is a property BETWEEN "+
				"nodes, so it is declared once for the definition rather than on a node — declare it in the "+
				"topology edit", ErrInvalidDefinition, AxisGraph, where)
		}
		if _, present := ne.Axes[AxisWiring]; present {
			return Definition{}, fmt.Errorf("%w: the `%s` axis was set%s. It is the pre-P36 spelling of "+
				"`%s` and is refused BY NAME rather than translated: a rename that quietly accepts the old "+
				"spelling never finishes, and the noun dictionary ends up with two entries for one axis. "+
				"Declare the topology in the topology edit, under `%s`",
				ErrWiringOverride, AxisWiring, where, AxisGraph, AxisGraph)
		}
		unknown := make([]string, 0)
		for a := range ne.Axes {
			if !authorable[a] {
				unknown = append(unknown, string(a))
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return Definition{}, fmt.Errorf("%w: this edit names axis/axes %s%s, which this platform does "+
				"not author. An unknown key silently dropped is a definition published without the thing "+
				"the operator thought they set",
				ErrInvalidDefinition, strings.Join(unknown, ", "), where)
		}
		id := strings.TrimSpace(ne.NodeID)
		if id == "" && !multi {
			id = DefaultNodeID
		}
		out.Nodes = append(out.Nodes, Node{
			NodeID:        id,
			PromptRef:     ne.Axes[AxisPrompt],
			ModelRef:      ne.Axes[AxisModel],
			ContextRef:    ne.Axes[AxisContext],
			MemoryRef:     ne.Axes[AxisMemory],
			HarnessRef:    ne.Axes[AxisHarness],
			LoopRef:       ne.Axes[AxisLoop],
			SkillRefs:     ne.Lists.SkillRefs,
			ToolNames:     ne.Lists.ToolNames,
			CredentialRef: ne.CredentialRef,
		})
	}

	if topo.Declared() && !multi {
		return Definition{}, fmt.Errorf("%w: the `%s` axis was set and this edit declares one node. There "+
			"is no second node to order it against, so a topology would hash a configuration nothing can "+
			"execute. It is rendered with this reason rather than hidden, because a hidden axis is "+
			"indistinguishable from one that does not exist", ErrWiringOverride, AxisGraph)
	}
	out.Order, out.Edges, out.GraphGroups = topo.Order, topo.Edges, topo.GraphGroups
	// A multi-node edit that declared no ordering gets the node order it was submitted in. 🚫 NOT a
	// silent default for the graph: `Validate` still refuses a fan-in with no merge and an edge to a
	// node nobody declared. This only spares an operator from restating a sequence they already wrote.
	if multi && len(out.Order) == 0 {
		for _, n := range out.Nodes {
			out.Order = append(out.Order, n.NodeID)
		}
	}
	return out, nil
}
