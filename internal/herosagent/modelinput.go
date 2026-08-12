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
	// Nodes are ids ONLY. The vocabulary the answer is validated against — and the reason an injected
	// instruction cannot make the agent name something that does not exist.
	Nodes []string `json:"nodes"`
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
		Nodes:             []string{},
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
			w.Nodes = append(w.Nodes, n.NodeID)
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
