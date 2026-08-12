package herosagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/heros-foreal/agentd/internal/providercall"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// gatewaymodel.go is the ONE implementation of Model that reaches a provider (task 4.2).
//
// 🔴 It goes through `providergateway` and nothing else. A direct HTTP call here would compile and work
// and would bypass four things the gateway owns: the configured secrets source (so the credential would
// have to be held HERE), retries carrying an idempotency key (a retry the provider treats as a new
// request is a double charge), the observer that makes a provider outage visible to P2.5, and the
// normalisation that keeps this package from knowing which vendor spells `system` which way.
//
// `TestThisPackageConstructsNoHTTPClientOfItsOwn` is the static half.

// GatewayModel calls a provider through the gateway.
type GatewayModel struct {
	gw *providergateway.Gateway
	// entry is the registry model entry the gateway resolves the call against. Passed per call by the
	// gateway's own design so a tier selector remains a function that picks an entry.
	entry *registry.ModelEntry
	// prompt is the fully-rendered instruction. Rendered by the caller from the definition's prompt
	// registry entry, so this type does not carry a second copy of the template.
	prompt string
}

// NewGatewayModel wires the provider-backed model.
func NewGatewayModel(gw *providergateway.Gateway, entry *registry.ModelEntry, prompt string) (*GatewayModel, error) {
	switch {
	case gw == nil:
		return nil, fmt.Errorf("%w: a provider gateway is required", ErrInvalidDefinition)
	case entry == nil:
		return nil, fmt.Errorf("%w: a resolved model entry is required", ErrInvalidDefinition)
	case strings.TrimSpace(prompt) == "":
		return nil, fmt.Errorf("%w: a rendered prompt is required — an empty instruction would ask the "+
			"model to invent the task", ErrInvalidDefinition)
	}
	return &GatewayModel{gw: gw, entry: entry, prompt: prompt}, nil
}

// Infer performs one call and parses the answer.
//
// 🔴 It parses into the RAW types and validates NOTHING. Validation is the Runner's, in one place, so
// there is one answer to "is this output acceptable" — and so a second Model implementation (the
// customer-side runner, §7) cannot validate differently.
func (m *GatewayModel) Infer(ctx context.Context, in Input) (RawResult, providercall.Usage, error) {
	body, err := json.Marshal(residueForPrompt(in))
	if err != nil {
		return RawResult{}, providercall.Usage{}, fmt.Errorf("herosagent: encoding the residue: %w", err)
	}

	resp, err := m.gw.Complete(ctx, m.entry, providergateway.Request{
		System: m.prompt,
		Messages: []providergateway.Message{
			{Role: "user", Content: string(body)},
		},
		// The three-part key IS the idempotency key: a retry of the same inference is the same request,
		// which is exactly what the gateway's retry needs to avoid a double charge.
		IdempotencyKey: defaultInferenceID(in.WorkflowID, in.SourceRevision, ""),
	}, nil)
	if err != nil {
		return RawResult{}, providercall.Usage{}, err
	}

	var raw RawResult
	if err := json.Unmarshal([]byte(strings.TrimSpace(resp.Content)), &raw); err != nil {
		// 🚫 NOT repaired, and not retried with a nudge. An answer this layer cannot parse is an answer
		// the contract was not met on, and the Runner records it as such — a parser that salvaged what it
		// could would turn a detectable failure into an undetectable one (D8), one layer earlier.
		return RawResult{}, resp.Usage, fmt.Errorf("herosagent: the model's answer is not the agreed "+
			"shape: %w", err)
	}
	return raw, resp.Usage, nil
}

// residueForPrompt is what the model is SHOWN. Named and separate so the answer to "what did HEROS see"
// is one function rather than an argument about a prompt string.
//
// 🔴 It carries the residue, the node ids, and the frontend records — and NOT the source, NOT the
// prompts of the customer's nodes, and NOT anything outside the gap. There is no field here a whole
// repository could occupy, which is NFR1 restated at the wire.
type promptResidue struct {
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

func residueForPrompt(in Input) promptResidue {
	out := promptResidue{
		WorkflowID:        in.WorkflowID,
		Nodes:             []string{},
		Pairs:             in.Residue.Pairs,
		UnlabelledRegions: in.Residue.UnlabelledRegions,
		Frontends:         []frontendNote{},
	}
	if in.RuleIR != nil {
		for _, n := range in.RuleIR.Nodes {
			out.Nodes = append(out.Nodes, n.NodeID)
		}
	}
	for _, f := range in.Residue.Frontends {
		out.Frontends = append(out.Frontends, frontendNote{
			Language: f.Language, AnalysisKind: string(f.AnalysisKind),
		})
	}
	return out
}
