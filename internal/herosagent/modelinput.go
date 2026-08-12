package herosagent

import (
	"encoding/json"
	"fmt"
)

// modelinput.go is THE context-assembly path (task 7.2), and it is the anti-skew fence.
//
// # The failure being designed against
//
// D6: "Two runners with one prompt is the classic shape that produces train/serve skew: the context each
// assembles diverges, and the divergence is invisible because both `work`." Nobody writes a second
// assembler on purpose. What happens is that the customer-side runner needs one field shaped slightly
// differently — a node list already sorted, a frontend note flattened — and the natural fix is four
// lines in the package that needed it. Both runners then produce plausible graphs from different
// contexts, at ONE `config_hash`, and D2's promise that a key determines a result quietly stops being
// true across placements.
//
// # Why the type is opaque rather than a struct both callers fill in
//
// A shared struct is not a shared path: a second caller can construct one field by field, and the
// compiler is happy. So `ModelInput` carries the CANONICAL BYTES and nothing else, its wire shape is
// unexported, and `Bytes` refuses a value the assembler did not produce. To get bytes to send, a runner
// must call AssembleModelInput — there is no second way to obtain them, which is the difference between
// a rule reviewers enforce and one the type system does.
//
// The parity test (task 7.6) asserts the consequence: both hosts send byte-identical input for the same
// Input, and their narratives differ — so a test that had quietly started comparing prose would fail.

// modelInputWire is what the model is SHOWN. Unexported, so the JSON shape cannot be built anywhere but
// here.
//
// 🔴 It carries the residue, the node ids and the frontend records — and NOT the source, NOT the prompts
// of the customer's nodes, and NOT anything outside the gap. There is no field here a whole repository
// could occupy, which is NFR1 restated at the wire.
type modelInputWire struct {
	WorkflowID string `json:"workflow_id"`
	// Nodes are the call sites, each with the facts that say WHERE it is — and nothing that says what
	// it contains.
	//
	// 🔴 THIS CARRIED IDS ONLY, and the first live rehearsal is what found it. The comment that stood
	// here said "Nodes are ids ONLY. The vocabulary the answer is validated against" — which is a true
	// statement about VALIDATION that had been mistaken for a statement about EVIDENCE. The model was
	// shown `n_494e69b8f47d6257` and `n_77e56db62b2208b3` and asked which depends on which, and there is
	// no answer to that question in two hashes. Every calibration fixture with real edges scored 0.00;
	// the only fixtures that "passed" were the near-misses whose correct answer is no edges at all.
	//
	// 🚫 What is added is exactly the field set `runlink.WorkflowIRAllowlist` already ratified as
	// structure rather than content, with the argument made there: "a path and a line range say where a
	// call is; they do not carry what it says". Not the prompt text, not the source lines, not the
	// I/O-contract schemas, not the tool names — a COUNT crosses for those, as it does on that wire.
	Nodes []modelInputNode `json:"nodes"`
	// Pairs are the pairs no frontend established. The only pairs an edge may be proposed for.
	Pairs []Pair `json:"candidate_pairs"`
	// UnlabelledRegions are the subgraphs no rule detector covered.
	UnlabelledRegions []string `json:"unlabelled_regions"`
	// Frontends tells the analyser WHY the gap exists — "the python frontend is syntactic and cannot
	// follow a value across a statement" is the single most useful thing it can be told.
	Frontends []frontendNote `json:"frontends"`
}

type frontendNote struct {
	Language     string `json:"language"`
	AnalysisKind string `json:"analysis_kind"`
}

// modelInputNode is one call site, as the model is shown it.
//
// Every field here is on the P29 opt-in allowlist, which ratified each one against the same question
// this file asks: does it say WHERE the call is, or WHAT it says? `Symbol` is the enclosing function
// name — "without it every node is an opaque hash, which is the whole reason this payload exists", in
// that allowlist's own words about the same field.
type modelInputNode struct {
	ID     string `json:"id"`
	Symbol string `json:"symbol,omitempty"`
	File   string `json:"file,omitempty"`
	// Line is the first line of the call span. A LOCATION, never the line itself.
	Line int `json:"line,omitempty"`
	// Provider and Model name the vendor and model this call site targets, or carry the documented
	// `unresolved` sentinel — which is itself the signal FR3.4 asks the agent to reason about.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	// ContextPolicy is a strategy LABEL, never assembled context.
	ContextPolicy string `json:"context_policy,omitempty"`
	// Tools is a COUNT. The names are call-site identifiers from the customer's source and do not
	// cross — the same rule the structure payload follows.
	Tools int `json:"tools,omitempty"`
}

// ModelInput is assembled context, as bytes. Obtainable only from AssembleModelInput.
type ModelInput struct {
	// canonical is the exact JSON both hosts send. A zero ModelInput has none, and Bytes says so rather
	// than returning an empty document that would read as "this repository has nothing in it".
	canonical []byte
}

// Bytes returns the assembled context. It refuses a ModelInput the assembler did not produce.
func (m ModelInput) Bytes() ([]byte, error) {
	if len(m.canonical) == 0 {
		return nil, fmt.Errorf("%w: a zero ModelInput carries no context. Sending it would ask the model "+
			"about an empty repository and read the answer as a finding", ErrAssemblerBypassed)
	}
	out := make([]byte, len(m.canonical))
	copy(out, m.canonical)
	return out, nil
}

// AssembleModelInput builds the context for one inference. 🔴 BOTH PLACEMENTS CALL THIS.
func AssembleModelInput(in Input) (ModelInput, error) {
	w := modelInputWire{
		WorkflowID:        in.WorkflowID,
		Nodes:             []modelInputNode{},
		Pairs:             in.Residue.Pairs,
		UnlabelledRegions: in.Residue.UnlabelledRegions,
		Frontends:         []frontendNote{},
	}
	if w.Pairs == nil {
		w.Pairs = []Pair{}
	}
	if w.UnlabelledRegions == nil {
		w.UnlabelledRegions = []string{}
	}
	if in.RuleIR != nil {
		for _, n := range in.RuleIR.Nodes {
			w.Nodes = append(w.Nodes, modelInputNode{
				ID: n.NodeID, Symbol: n.CallSite.Symbol, File: n.CallSite.File,
				Line: n.CallSite.LineStart, Provider: n.Model.Provider, Model: n.Model.ModelID,
				ContextPolicy: n.ContextAssembly.Policy, Tools: len(n.ToolsSkills),
			})
		}
	}
	for _, f := range in.Residue.Frontends {
		w.Frontends = append(w.Frontends, frontendNote{
			Language: f.Language, AnalysisKind: string(f.AnalysisKind),
		})
	}
	b, err := json.Marshal(w)
	if err != nil {
		return ModelInput{}, fmt.Errorf("herosagent: encoding the residue: %w", err)
	}
	return ModelInput{canonical: b}, nil
}
