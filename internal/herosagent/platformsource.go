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

// ModelResolver turns the definition's operator-registry model ref into everything a customer-side
// runner needs to CALL that model. Platform-side for the same reason: the customer has no operator
// registry and must not need one.
type ModelResolver interface {
	Resolve(ctx context.Context, modelRef string) (ResolvedModel, bool, error)
}

// ResolvedModel is a model ref resolved into the three things a call needs.
//
// 🔴 It is a struct rather than the three return values this interface used to have, because the third
// one is what was missing. `Resolve` returned (provider, modelID) and the adapter dropped
// `entry.Spec.Params` on the floor — a `ModelSpec` bundles all three as ONE versioned unit so that a
// ref resolves exactly what was stored, and this seam un-bundled it. A struct is also what stops the
// next parameter from being added by growing a signature nobody wants to touch.
type ResolvedModel struct {
	// Provider and ModelID are what the definition binds.
	Provider string
	ModelID  string
	// Params are the pinned inference parameters, or nil when the model version pins none. Nil is a
	// legitimate state — an OpenAI entry needs no `max_tokens` — so it is carried, not defaulted.
	Params *runlink.ModelParams
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
	// 🔴 A MULTI-NODE definition is REFUSED over this contract, not flattened to its first node.
	//
	// `runlink.AgentDefinition` carries ONE prompt and ONE model — it is the single-node link, and it
	// predates node identity being data. Serving a graph's first node through it would run a different
	// configuration from the one the `config_hash` names, on a customer's machine, and submit the result
	// back under that hash. Every surface would then attribute a one-node answer to a definition that
	// declares several, and nothing would look wrong.
	//
	// 🚫 It is not an ok=false either: "no active definition" is a state a customer's CLI renders as
	// "this deployment has published none", which is FALSE here — one is published and this link cannot
	// carry it. Naming that is the difference between a deployment somebody configures and a deployment
	// somebody debugs.
	if v.Definition.MultiNode() {
		return runlink.AgentDefinition{}, false, fmt.Errorf(
			"%w: the active definition declares %d nodes and the customer-side link contract (v%s) carries "+
				"one prompt and one model. Serving its first node would run a configuration the config_hash "+
				"does not name and submit the answer back under that hash. Place this tenant `%s` while the "+
				"active definition is a graph, or activate a single-node definition",
			ErrInvalidDefinition, len(v.Definition.Nodes), runlink.AgentDefinitionContractVersion,
			PlacementPlatform)
	}
	node := v.Definition.Primary()

	body, ok, err := p.prompts.Render(ctx, node.PromptRef)
	if err != nil {
		return runlink.AgentDefinition{}, false, err
	}
	if !ok || body == "" {
		return runlink.AgentDefinition{}, false, nil
	}
	model, ok, err := p.models.Resolve(ctx, node.ModelRef)
	if err != nil {
		return runlink.AgentDefinition{}, false, err
	}
	if !ok {
		return runlink.AgentDefinition{}, false, nil
	}
	provider, modelID := model.Provider, model.ModelID
	// 🔴 The model's provider and the credential reference must AGREE, and a disagreement is an error
	// rather than a preference between them. A definition binding an Anthropic model and an `openai`
	// credential is incoherent, and the failure it produces on a customer's machine is a 401 from a
	// vendor they never configured — which reads as "my key is wrong" and is not. Publishing does not
	// catch it today: the two axes are validated separately, against different registries.
	if provider != "" && node.CredentialRef != "" && provider != node.CredentialRef {
		return runlink.AgentDefinition{}, false, fmt.Errorf(
			"%w: the active definition binds a model served by %q and a credential reference of %q. "+
				"Running it would authenticate against one vendor and call another — on a customer's "+
				"machine that surfaces as a rejected key they did not get wrong",
			ErrInvalidDefinition, provider, node.CredentialRef)
	}
	return runlink.AgentDefinition{
		ContractVersion: runlink.AgentDefinitionContractVersion,
		ConfigHash:      v.ConfigHash,
		Prompt:          body,
		// 🚫 The CREDENTIAL REFERENCE, which is a provider NAME. Never a key value — the definition has
		// no field that could hold one (see Definition.CredentialRef) and neither does this response.
		Provider:        node.CredentialRef,
		ModelID:         modelID,
		ConfidenceFloor: p.floor,
		MaxTokens:       p.budget.MaxTokens,
		MaxWallSeconds:  int(p.budget.MaxWall / time.Second),
		// The parameters the model version pins, carried rather than dropped. Without them a
		// customer-placed run uses the operator's model with nobody's settings — and on Anthropic it
		// does not run at all, because the gateway refuses a call that sets no max_tokens.
		ModelParams: model.Params,
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

// ── The PLATFORM-SIDE view of the active definition (P36 §4) ─────────────────────────────────────

// ActiveBinding returns the active definition as an AssessmentBinding — the whole definition, not the
// single-node wire projection.
//
// # 🔴 Why this exists beside ActiveDefinition rather than replacing it
//
// `ActiveDefinition` serves `runlink.AgentDefinition`: one prompt, one model, over a wire, to a
// customer's machine. That contract is single-node and stays single-node (decisions.md D-36.3), and it
// carries no node id because the producing node is operator-side only (D-36.2).
//
// The platform-side runner is IN PROCESS. It needs the node list, the ordering, the edges and the graph
// groups, and there is no wire between it and this store — so it reads the definition rather than a
// projection of it. Two methods, two audiences, and the difference between them is exactly the
// difference the two decisions draw.
//
// 🔴 ok=false means NO DEFINITION IS ACTIVE, which is a state and not an error: a fresh deployment has
// published nothing.
func (p *PlatformSource) ActiveBinding(ctx context.Context) (AssessmentBinding, bool, error) {
	v, ok, err := p.active.Active(ctx)
	if err != nil || !ok {
		return AssessmentBinding{}, false, err
	}
	return BindDefinition(v.ConfigHash, v.Definition), true, nil
}

// RenderNode resolves ONE node's instruction and model, for a platform-side runner assembling a graph.
//
// 🔴 Rendering stays PLATFORM-SIDE for the reason `PromptResolver` gives: two renderers is two prompts.
// A graph does not change that; it multiplies it by the node count.
//
// 🔴 It checks the model's provider against the node's credential reference, node by node. The
// single-node path has done this since P30 — "running it would authenticate against one vendor and call
// another" — and a graph is where a mismatch becomes likely rather than exotic, because per-node
// credentials exist precisely so different nodes can use different vendors (decisions.md D-36.1).
//
// ok=false means the node's prompt is not published or its model is not registered. Both are states a
// surface reports rather than outages.
func (p *PlatformSource) RenderNode(ctx context.Context, n Node) (string, ResolvedModel, bool, error) {
	body, ok, err := p.prompts.Render(ctx, n.PromptRef)
	if err != nil {
		return "", ResolvedModel{}, false, err
	}
	if !ok || body == "" {
		return "", ResolvedModel{}, false, nil
	}
	model, ok, err := p.models.Resolve(ctx, n.ModelRef)
	if err != nil {
		return "", ResolvedModel{}, false, err
	}
	if !ok {
		return "", ResolvedModel{}, false, nil
	}
	if model.Provider != "" && n.CredentialRef != "" && model.Provider != n.CredentialRef {
		return "", ResolvedModel{}, false, fmt.Errorf(
			"%w: node %q binds a model served by %q and a credential reference of %q. Running it would "+
				"authenticate against one vendor and call another, and the failure surfaces as a rejected "+
				"key nobody got wrong",
			ErrInvalidDefinition, n.NodeID, model.Provider, n.CredentialRef)
	}
	return body, model, true, nil
}
