package studio

import (
	"context"

	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
)

// EchoCompleter is a DETERMINISTIC stand-in for the provider gateway, for deployments without provider
// credentials (task M4.2). It echoes the rendered prompt as the output, with token counts derived from
// length, so the studio test-run surface is demonstrable without spending real money.
//
// 🚫 It is NOT a measurement path. A real deployment injects the provider gateway (`*providergateway.
// Gateway`), which satisfies the same Completer interface. The echo exists so the matrix's test-run
// cell renders real output/cost/latency/tokens in a local demo, not so anyone measures anything with it.
type EchoCompleter struct{}

// Complete returns the last user message prefixed with a marker, and deterministic token counts.
func (EchoCompleter) Complete(_ context.Context, entry *registry.ModelEntry, req providergateway.Request, _ *int64) (*providergateway.Response, error) {
	var prompt string
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			prompt = req.Messages[i].Content
			break
		}
	}
	if prompt == "" && req.System != "" {
		prompt = req.System
	}
	provider, modelID := "echo", "echo"
	if entry != nil {
		provider, modelID = entry.Spec.Provider, entry.Spec.ModelID
	}
	// Deterministic token accounting: ~4 chars/token, a stable convention for the demo.
	inTok := (len(prompt) + 3) / 4
	out := "[echo:" + modelID + "] " + prompt
	outTok := (len(out) + 3) / 4
	return &providergateway.Response{
		Content:  out,
		Provider: provider,
		ModelID:  modelID,
		Usage:    providergateway.Usage{InputTokens: inTok, OutputTokens: outTok},
		Attempts: 1,
	}, nil
}

// FlatPricer prices a call at a fixed dollar rate per 1000 total tokens — a simple, deterministic price
// book for the demo. A real deployment supplies its own Pricer from a real price source.
func FlatPricer(usdPer1kTokens float64) Pricer {
	return func(_, _ string, u providergateway.Usage) float64 {
		return usdPer1kTokens * float64(u.InputTokens+u.OutputTokens) / 1000.0
	}
}
