package launch

import (
	"context"
	"errors"
	"strings"

	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/providergateway"

	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/runlink"
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

// Models lists the registry as the PUBLISHER needs it — the catalogue a `model_ref` is validated
// against before a definition is recorded.
//
// 🔴 `RegisteredModel.ModelID` carries the VERSION ID, not the vendor's model name, because that is
// what a `model_ref` is and what `Resolve` below reads. The two must be the same key space: a
// publisher validating against one and a runner resolving against the other accepts references that
// cannot be run, and says so only at the first analysis.
//
// `Deprecated` is always false here: deprecation lives in the operator PRICE registry
// (`adminops.ModelRegistry`), which is keyed by vendor model id and is a different question — "should
// anybody choose this model" rather than "does this reference resolve". Reporting a guess would make
// the publisher's deprecation notice a statement about nothing.
func (m registryModels) Models(ctx context.Context) ([]herosagent.RegisteredModel, error) {
	entries, err := m.reg.AllModelVersions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]herosagent.RegisteredModel, 0, len(entries))
	for _, e := range entries {
		out = append(out, herosagent.RegisteredModel{ModelID: e.VersionID, Provider: e.Spec.Provider})
	}
	return out, nil
}

// Resolve reads the operator registry so the CUSTOMER's machine does not have to.
func (m registryModels) Resolve(ctx context.Context, modelRef string) (herosagent.ResolvedModel, bool, error) {
	if modelRef == "" {
		return herosagent.ResolvedModel{}, false, nil
	}
	entry, err := m.reg.ResolveModel(ctx, modelRef)
	if err != nil {
		if errors.Is(err, registry.ErrNotFound) {
			return herosagent.ResolvedModel{}, false, nil
		}
		return herosagent.ResolvedModel{}, false, err
	}
	return resolvedFrom(entry), true, nil
}

// resolvedFrom maps a registry entry onto what a customer-side runner needs to CALL it.
//
// 🔴 Extracted as a pure function so it can be fenced WITHOUT a database. The defect this file had —
// returning provider and model id and letting `entry.Spec.Params` fall on the floor — lived exactly
// here, and every test that could have caught it went through a hand-written fake resolver instead of
// this code. A fence over a fake proves the contract, never the adapter, and the adapter was the bug.
func resolvedFrom(entry *registry.ModelEntry) herosagent.ResolvedModel {
	return herosagent.ResolvedModel{
		Provider: entry.Spec.Provider,
		ModelID:  entry.Spec.ModelID,
		Params:   wireParams(entry.Spec.Params),
	}
}

// wireParams maps the registry's domain params onto the versioned wire type.
//
// The ONE place the two shapes meet, deliberately: a contract that shared the domain struct would make
// any future field on it an unversioned change to what every customer CLI receives.
//
// Returns nil when the version pins nothing, rather than an empty struct. Nil is what "this model
// declares no parameters" means on the wire, and an empty object would be indistinguishable from a
// platform that sent the field and forgot to fill it.
func wireParams(p registry.ModelParams) *runlink.ModelParams {
	out := runlink.ModelParams{
		MaxTokens:      p.MaxTokens,
		Temperature:    p.Temperature,
		ThinkingBudget: p.ThinkingBudget,
		Seed:           p.Seed,
		TimeoutSeconds: p.TimeoutSeconds,
	}
	if out.MaxTokens == nil && out.Temperature == nil && out.ThinkingBudget == nil &&
		out.Seed == nil && out.TimeoutSeconds == nil {
		return nil
	}
	return &out
}

// secretsResolver adapts the gateway's own Secrets source onto the readiness check's resolver.
//
// 🔴 IT PERFORMS THE RESOLUTION. That is the whole of task 9.1's "not asserted from configuration":
// this asks the same source, for the same provider, that the runner will ask at use — so a reference
// pointing at a secret nobody provisioned reports `credential_unresolved` here rather than looking
// identical to a working one until the first customer analysis.
//
// 🚫 It discards the credential immediately. `Resolve` returns an error or nil and never the value: a
// readiness surface that held one would be a readiness surface that could leak one, and nothing on
// `/readyz` needs it.
type secretsResolver struct{ secrets providergateway.Secrets }

func (r secretsResolver) Resolve(ctx context.Context, provider string) error {
	if r.secrets == nil {
		return errors.New("this deployment has no configured secrets source, so no provider credential " +
			"can be resolved at all")
	}
	if strings.TrimSpace(provider) == "" {
		return errors.New("the active definition binds no provider name")
	}
	_, err := r.secrets.Credential(ctx, provider)
	return err
}

func (r secretsResolver) Describe() string {
	if r.secrets == nil {
		return "none"
	}
	return string(r.secrets.Describe().Kind)
}
