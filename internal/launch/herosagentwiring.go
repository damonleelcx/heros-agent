package launch

import (
	"context"
	"errors"

	"github.com/heros-foreal/agentd/internal/registry"
)

// herosagentwiring.go adapts the P2 registries onto the two resolvers `herosagent.PlatformSource`
// needs, and holds the two defaults a deployment runs the agent under.
//
// # Why the adapters are here rather than in internal/herosagent
//
// `herosagent` declares `PromptResolver` and `ModelResolver` as interfaces so it can be tested without a
// database, and so the ONE place a prompt is rendered stays visible. If it imported `internal/registry`
// to satisfy them itself, that seam would close and the customer-side runner's "has no template engine"
// property would be a fact about the CLI's build rather than about the design.

// registryPrompts renders a prompt-registry version into the instruction a runner sends.
type registryPrompts struct{ reg *registry.Store }

// Render resolves the version and renders its template with NO BINDINGS.
//
// 🔴 No bindings, and that is a statement about HEROS rather than a shortcut. Its prompt is the
// analyst's standing instruction — what a residue is, which vocabularies are closed, that a rule-derived
// edge is immutable — and none of that varies per analysis. The per-analysis input is the RESIDUE, and
// it travels as the user message that `AssembleModelInput` builds. A slot filled here would be a second
// channel for context, assembled somewhere the anti-skew fence cannot see.
//
// A template that DECLARES a slot therefore cannot be rendered, and `Render` refuses rather than
// substituting an empty string: a prompt with a hole where an instruction should be is one the model
// answers anyway.
func (p registryPrompts) Render(ctx context.Context, promptRef string) (string, bool, error) {
	if promptRef == "" {
		return "", false, nil
	}
	entry, err := p.reg.ResolvePrompt(ctx, promptRef)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			// NOT PUBLISHED is a state, not a failure — a definition whose prompt version was never
			// registered has nothing to run, and the customer's CLI says exactly that.
			return "", false, nil
		}
		return "", false, err
	}
	body, err := entry.Template.Render(nil)
	if err != nil {
		return "", false, err
	}
	return body, true, nil
}

// registryModels resolves the definition's model ref to a provider and model id.
type registryModels struct{ reg *registry.Store }

// Resolve reads the operator registry so the CUSTOMER's machine does not have to.
func (m registryModels) Resolve(ctx context.Context, modelRef string) (string, string, bool, error) {
	if modelRef == "" {
		return "", "", false, nil
	}
	entry, err := m.reg.ResolveModel(ctx, modelRef)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	return entry.Spec.Provider, entry.Spec.ModelID, true, nil
}
