package herosagent

import (
	"context"
	"fmt"
	"time"

	"github.com/heros-foreal/agentd/internal/runlink"
)

// platformsource.go is the platform half of §7: it answers "what may this tenant do, and what should it
// run", and it accepts what a customer-placed tenant produced.
//
// It implements `api.HerosAgentSource` WITHOUT importing `internal/api` — a store package that imported
// the HTTP layer would invert the dependency and make this untestable without a server.

// PlacementReader is the placement half. Both stores in placementstore.go satisfy it.
type PlacementReader interface {
	Get(ctx context.Context, tenantID string) (TenantPlacement, error)
}

// PromptResolver renders a prompt-registry version into the instruction a runner sends.
//
// 🔴 An interface rather than a `*registry.Store`, because the rendering must happen PLATFORM-SIDE and
// the seam is where that becomes checkable. Two renderers is two prompts — the same skew D6 is about,
// one layer above the context assembler — so the customer's machine gets a rendered string and has no
// template engine, no registry and nothing to render differently with.
type PromptResolver interface {
	// Render returns the prompt body for a prompt-registry version_id. ok=false is NOT PUBLISHED.
	Render(ctx context.Context, promptRef string) (body string, ok bool, err error)
}

// ModelResolver turns the definition's operator-registry model ref into the provider and model id a
// customer-side runner needs. Platform-side for the same reason: the customer has no operator registry
// and must not need one.
type ModelResolver interface {
	Resolve(ctx context.Context, modelRef string) (provider, modelID string, ok bool, err error)
}

// PlatformSource serves the definition and accepts submissions.
type PlatformSource struct {
	placements PlacementReader
	versions   VersionLookup
	active     activeReader
	prompts    PromptResolver
	models     ModelResolver
	ingester   *Ingester
	inferences InferenceStore
	budget     Budget
	floor      float64
	caps       *CapChecker
	meter      SpendReader
}

type activeReader interface {
	Active(ctx context.Context) (Version, bool, error)
}

// PlatformSourceConfig wires the source. Every field is required and the refusals say what breaks
// without it — a source assembled with a nil collaborator would answer a customer's CLI with a
// definition it cannot run, at the moment they run it.
type PlatformSourceConfig struct {
	Placements PlacementReader
	Versions   interface {
		VersionLookup
		activeReader
	}
	Prompts    PromptResolver
	Models     ModelResolver
	Inferences InferenceStore
	Floor      float64
	Budget     Budget
	NowMS      func() int64
	// Caps is the ceiling checker a platform-side runner enforces before every provider call (9.2).
	// Nil means NO CEILING — reported by `Readiness` rather than hidden, because a deployment with
	// unwired caps looks identical on every other signal to one whose caps are merely generous.
	Caps *CapChecker
	// Meter records what a run spent, so the next check reads it.
	Meter SpendReader
}

// NewPlatformSource wires the platform half.
func NewPlatformSource(cfg PlatformSourceConfig) (*PlatformSource, error) {
	switch {
	case cfg.Placements == nil:
		return nil, fmt.Errorf("%w: a placement store is required — without one every tenant would read "+
			"as the default, and an operator's decision to enable a tenant would have nowhere to live",
			ErrInvalidDefinition)
	case cfg.Versions == nil:
		return nil, fmt.Errorf("%w: a version store is required", ErrInvalidDefinition)
	case cfg.Prompts == nil:
		return nil, fmt.Errorf("%w: a prompt resolver is required — a definition whose instruction cannot "+
			"be rendered is one a customer-side runner would execute empty", ErrInvalidDefinition)
	case cfg.Models == nil:
		return nil, fmt.Errorf("%w: a model resolver is required", ErrInvalidDefinition)
	}
	if err := cfg.Budget.Validate(); err != nil {
		return nil, err
	}
	ing, err := NewIngester(cfg.Versions, cfg.Inferences, cfg.Floor, cfg.NowMS)
	if err != nil {
		return nil, err
	}
	return &PlatformSource{
		placements: cfg.Placements, versions: cfg.Versions, active: cfg.Versions,
		prompts: cfg.Prompts, models: cfg.Models, inferences: cfg.Inferences,
		ingester: ing, budget: cfg.Budget, floor: cfg.Floor,
		caps: cfg.Caps, meter: cfg.Meter,
	}, nil
}

// PlacementFor returns a tenant's effective placement — `disabled` when nobody has set one.
func (p *PlatformSource) PlacementFor(ctx context.Context, tenantID string) (Placement, error) {
	tp, err := p.placements.Get(ctx, tenantID)
	if err != nil {
		return PlacementDisabled, err
	}
	return tp.Placement, nil
}

// ActiveDefinition renders the active definition for a customer-side runner.
//
// 🔴 It returns ok=false rather than an error for every "nothing to run" state — no active version, a
// prompt that no longer resolves, a model the registry has dropped. A customer's CLI renders those as
// "this deployment has published no active agent definition", which is true and actionable; a 502 would
// send a developer to look for an outage that is not happening.
func (p *PlatformSource) ActiveDefinition(ctx context.Context) (runlink.AgentDefinition, bool, error) {
	v, ok, err := p.active.Active(ctx)
	if err != nil || !ok {
		return runlink.AgentDefinition{}, false, err
	}
	body, ok, err := p.prompts.Render(ctx, v.Definition.PromptRef)
	if err != nil {
		return runlink.AgentDefinition{}, false, err
	}
	if !ok || body == "" {
		return runlink.AgentDefinition{}, false, nil
	}
	provider, modelID, ok, err := p.models.Resolve(ctx, v.Definition.ModelRef)
	if err != nil {
		return runlink.AgentDefinition{}, false, err
	}
	if !ok {
		return runlink.AgentDefinition{}, false, nil
	}
	// 🔴 The model's provider and the credential reference must AGREE, and a disagreement is an error
	// rather than a preference between them. A definition binding an Anthropic model and an `openai`
	// credential is incoherent, and the failure it produces on a customer's machine is a 401 from a
	// vendor they never configured — which reads as "my key is wrong" and is not. Publishing does not
	// catch it today: the two axes are validated separately, against different registries.
	if provider != "" && v.Definition.CredentialRef != "" && provider != v.Definition.CredentialRef {
		return runlink.AgentDefinition{}, false, fmt.Errorf(
			"%w: the active definition binds a model served by %q and a credential reference of %q. "+
				"Running it would authenticate against one vendor and call another — on a customer's "+
				"machine that surfaces as a rejected key they did not get wrong",
			ErrInvalidDefinition, provider, v.Definition.CredentialRef)
	}
	return runlink.AgentDefinition{
		ContractVersion: runlink.AgentDefinitionContractVersion,
		ConfigHash:      v.ConfigHash,
		Prompt:          body,
		// 🚫 The CREDENTIAL REFERENCE, which is a provider NAME. Never a key value — the definition has
		// no field that could hold one (see Definition.CredentialRef) and neither does this response.
		Provider:        v.Definition.CredentialRef,
		ModelID:         modelID,
		ConfidenceFloor: p.floor,
		MaxTokens:       p.budget.MaxTokens,
		MaxWallSeconds:  int(p.budget.MaxWall / time.Second),
	}, true, nil
}

// Accept ingests a customer-placed submission.
func (p *PlatformSource) Accept(ctx context.Context, sub Submission) (IngestResult, error) {
	return p.ingester.Accept(ctx, sub)
}

// RunnerOptions returns the options a platform-side runner should be built with, so the ceiling and
// the meter reach it from the same wiring that reported readiness about them.
//
// 🔴 A method rather than two exported fields, because the cap checker and the meter must travel
// TOGETHER: `NewCapChecker` already refuses a checker with no meter ("a ceiling with nothing to measure
// against is a number nobody is under"), and handing a runner one without the other would reintroduce
// exactly that at the next layer up.
func (p *PlatformSource) RunnerOptions() []RunnerOption {
	if p.caps == nil || p.meter == nil {
		return nil
	}
	return []RunnerOption{WithCaps(p.caps, p.meter)}
}

// NarrativeFor returns the agent's prose about one workflow (task 8.2).
//
// 🔴 It reads the LATEST inference for the workflow rather than one keyed by a revision, and the reason
// is what the caller has: a graph page knows the workflow it is drawing and not which revision the
// stored analysis ran against. Reading the latest is honest about that — it is the analysis whose facts
// are on the page, because the same store is what put them there.
//
// ok=false is NO NARRATIVE and is a NORMAL outcome: the agent may produce edges and no prose, and an
// abstention-only inference is a real row with nothing to say. 🚫 Never a summary this platform writes.
func (p *PlatformSource) NarrativeFor(ctx context.Context, tenantID, workflowID string) (string, bool, error) {
	reader, ok := p.inferences.(latestReader)
	if !ok {
		// A store that cannot answer "the latest for this workflow" reports NO narrative rather than an
		// error. The prose is the least load-bearing thing on the page and its absence renders as
		// nothing; failing the read would turn a missing paragraph into a panel-level fault.
		return "", false, nil
	}
	st, found, err := reader.LatestFor(ctx, tenantID, workflowID)
	if err != nil || !found {
		return "", false, err
	}
	return st.Narrative, st.Narrative != "", nil
}

// latestReader is the optional half of an InferenceStore that can answer "the most recent inference for
// this workflow". Optional rather than on the interface, because only the narrative needs it and every
// other caller addresses an inference by D2's three-part key — which is the addressing the whole design
// rests on, and which this must not look like an alternative to.
type latestReader interface {
	LatestFor(ctx context.Context, tenantID, workflowID string) (Stored, bool, error)
}
