// Package platformanalyse runs the platform's own agent over a pushed snapshot, for a tenant whose
// placement is `platform`.
//
// # What was missing, and what was not
//
// Almost nothing here is new. `herosagent.NewRunner` is documented as "the PLATFORM-side runner", the
// source already reaches the platform (`PushSource` writes a snapshot, `hostdiscovery.Runner`
// materializes and discovers it), the durable store exists (`herosagent.PGInferenceStore`), and the
// ceiling and meter are handed out by `PlatformSource.RunnerOptions`.
//
// What did not exist was a CALLER. `NewRunner` had exactly two, the rehearsal gate and a proof binary,
// and both passed `NewMemInferenceStore()` — a store that records and forgets. So under `platform`
// placement nothing started an analysis and nothing could have kept one. This package is the caller.
//
// # 🔴 It resolves the definition through the SAME path the customer does
//
// `PlatformSource.ActiveDefinition` renders the prompt, resolves the model, and carries the pinned
// model parameters. Using it here rather than reaching into the registries directly is deliberate: D6
// exists to stop the platform and the customer running two implementations of one definition, and the
// cheapest way to grow a second one is to resolve "the same" definition twice by different means. One
// consequence worth naming — this path inherits the max_tokens carriage, so an Anthropic definition
// runs here for the same reason it now runs on a customer's machine.
//
// # Why this is a BYPASS and not part of discovery
//
// Discovery is the main line: it produces the graph every page draws. An analysis is an enrichment on
// top of it — it adds inferred edges and a narrative, and the graph renders honestly without them.
// So a failure to analyse must NOT fail the discovery that succeeded, or one unavailable provider
// would take out the picture as well as the commentary. The failure is reported loudly instead of
// silently: see Outcome, which distinguishes every reason nothing was produced.
package platformanalyse

import (
	"context"
	"errors"
	"fmt"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/herosagent"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/runlink"
	"github.com/heros-foreal/agentd/internal/sourceingest"
)

// Definitions resolves the active agent definition. *herosagent.PlatformSource satisfies it.
type Definitions interface {
	ActiveDefinition(ctx context.Context) (runlink.AgentDefinition, bool, error)
}

// Placements answers what a tenant is allowed to have done. *herosagent.PlatformSource satisfies it.
type Placements interface {
	PlacementFor(ctx context.Context, tenantID string) (herosagent.Placement, error)
}

// Source hands over a materialized snapshot and its IR. *hostdiscovery.Runner satisfies it.
//
// 🔴 `WithSource` rather than `IR`, even though only the IR is read today. `IR` releases the extracted
// directory BEFORE returning, so anything that later needs the files — a codemod, a second pass — would
// have to materialize again and could get a different tree if a push landed in between. Taking the
// callback shape now costs nothing and keeps that door open.
type Source interface {
	WithSource(ctx context.Context, ref sourceingest.Ref, fn func(dir string, ir *discovery.IR) error) error
}

// Reason names why an analysis produced nothing. Each is a DIFFERENT operator action, which is the
// whole point of not collapsing them into an error string.
type Reason string

const (
	// ReasonNotPlaced: the tenant is not `platform`-placed. Not a fault — it is the default (Q2), and
	// under `customer` placement the analysis happens on the customer's machine by design.
	ReasonNotPlaced Reason = "not_placed"
	// ReasonNoDefinition: no active definition. An operator publishes and activates one.
	ReasonNoDefinition Reason = "no_definition"
	// ReasonNoSource: nothing has been pushed for this revision.
	ReasonNoSource Reason = "no_source"
	// ReasonNothingToInfer: every pair and region already carries a rule-derived fact. A HEALTHY
	// outcome that costs nothing — D3's "cost proportional to the gap" — and it must never read as a
	// failure.
	ReasonNothingToInfer Reason = "nothing_to_infer"
	// ReasonFailed: the run was attempted and did not complete.
	ReasonFailed Reason = "failed"
	// ReasonAnalysed: an inference was produced and stored.
	ReasonAnalysed Reason = "analysed"
)

// Outcome is what one attempt did. Returned rather than logged so a caller can render it, and so the
// difference between "nothing to do" and "could not do it" survives to whoever asks.
type Outcome struct {
	Reason Reason
	// ConfigHash is the definition the run used, when there was one.
	ConfigHash string
	// Detail explains a Reason that needs it. Never a credential, never a prompt body.
	Detail string
	// Result is the run's result when Reason is ReasonAnalysed.
	Result herosagent.Result
}

// Analysed reports whether an inference was produced and stored.
func (o Outcome) Analysed() bool { return o.Reason == ReasonAnalysed }

// Service runs the platform's agent over pushed snapshots.
type Service struct {
	defs      Definitions
	places    Placements
	source    Source
	gateway   *providergateway.Gateway
	store     herosagent.InferenceStore
	floor     float64
	budget    herosagent.Budget
	runnerOpt []herosagent.RunnerOption
	nowMS     func() int64
}

// Config wires the service. Every field is required: a service assembled with a nil collaborator would
// answer a platform-placed tenant with silence at the moment their source lands.
type Config struct {
	Definitions Definitions
	Placements  Placements
	Source      Source
	Gateway     *providergateway.Gateway
	// Inferences MUST be durable. The two existing callers of herosagent.NewRunner both pass an
	// in-memory store, which is exactly why a platform-placed analysis could not have been kept even if
	// something had started one.
	Inferences   herosagent.InferenceStore
	Floor        float64
	Budget       herosagent.Budget
	RunnerOption []herosagent.RunnerOption
	NowMS        func() int64
}

// New wires the service.
func New(cfg Config) (*Service, error) {
	switch {
	case cfg.Definitions == nil:
		return nil, errors.New("platformanalyse: a definition source is required")
	case cfg.Placements == nil:
		return nil, errors.New("platformanalyse: a placement reader is required — without one every " +
			"tenant would be analysed, including the ones deliberately left disabled")
	case cfg.Source == nil:
		return nil, errors.New("platformanalyse: a snapshot source is required")
	case cfg.Gateway == nil:
		return nil, errors.New("platformanalyse: a provider gateway is required")
	case cfg.Inferences == nil:
		return nil, errors.New("platformanalyse: an inference store is required")
	case cfg.NowMS == nil:
		return nil, errors.New("platformanalyse: a clock is required")
	}
	if err := cfg.Budget.Validate(); err != nil {
		return nil, err
	}
	return &Service{
		defs: cfg.Definitions, places: cfg.Placements, source: cfg.Source,
		gateway: cfg.Gateway, store: cfg.Inferences, floor: cfg.Floor,
		budget: cfg.Budget, runnerOpt: cfg.RunnerOption, nowMS: cfg.NowMS,
	}, nil
}

// Analyse runs the active definition over one pushed revision, for one tenant.
//
// 🔴 The placement is checked FIRST, before anything is materialized and before a definition is read.
// Ordering matters: `disabled` is the default for every tenant (Q2), so the common case must cost
// nothing, and a tenant nobody has enabled must never have their source extracted on our disk to
// discover that we were not allowed to.
// 🔴 The tenant comes from the REF, not from a parameter beside it. A Ref already carries a TenantID
// and every store keyed by tenant reads it from there; accepting a second one would let a caller pass a
// tenant that disagrees with the snapshot's, and the disagreement would resolve as "analyse tenant A's
// permission over tenant B's source". One identity, one source.
func (s *Service) Analyse(ctx context.Context, ref sourceingest.Ref) (Outcome, error) {
	if err := ref.Validate(); err != nil {
		return Outcome{}, err
	}
	tenantID := ref.TenantID
	place, err := s.places.PlacementFor(ctx, tenantID)
	if err != nil {
		return Outcome{}, fmt.Errorf("platformanalyse: reading the placement for %q: %w", tenantID, err)
	}
	if place != herosagent.PlacementPlatform {
		return Outcome{
			Reason: ReasonNotPlaced,
			Detail: fmt.Sprintf("this tenant's placement is %q; the platform analyses only `platform`-placed tenants", place),
		}, nil
	}

	def, ok, err := s.defs.ActiveDefinition(ctx)
	if err != nil {
		return Outcome{}, fmt.Errorf("platformanalyse: reading the active definition: %w", err)
	}
	if !ok || !def.Runnable() {
		return Outcome{
			Reason: ReasonNoDefinition,
			Detail: "this deployment has published no active agent definition, so there is nothing to run",
		}, nil
	}

	// The model, assembled from what the definition resolved — including its PINNED PARAMETERS. An
	// Anthropic model is refused by the gateway without max_tokens, so a spec built from provider and id
	// alone would fail at the first call; that is the same defect the customer-side path carried.
	params, perr := modelParams(def)
	if perr != nil {
		return Outcome{Reason: ReasonFailed, ConfigHash: def.ConfigHash, Detail: perr.Error()}, nil
	}
	model, err := herosagent.NewGatewayModel(s.gateway, &registry.ModelEntry{
		Name: def.ModelID,
		Spec: registry.ModelSpec{Provider: def.Provider, ModelID: def.ModelID, Params: params},
	}, def.Prompt)
	if err != nil {
		return Outcome{Reason: ReasonFailed, ConfigHash: def.ConfigHash, Detail: err.Error()}, nil
	}

	// 🔴 NewRunner, not NewCustomerRunner, and the DURABLE store. The host distinction is recorded on
	// every inference — an analysis this platform ran must never be attributable to the customer's
	// machine — and a memory store here would reproduce the exact gap this package closes.
	runner, err := herosagent.NewRunner(model, s.store, s.floor, s.nowMS, s.runnerOpt...)
	if err != nil {
		return Outcome{}, fmt.Errorf("platformanalyse: wiring the runner: %w", err)
	}

	out := Outcome{ConfigHash: def.ConfigHash}
	serr := s.source.WithSource(ctx, ref, func(_ string, ir *discovery.IR) error {
		// An empty DiscoveryReport, exactly as the customer path passes: its only contribution to a
		// residue is the frontend run list, which is provenance for the report rather than an input the
		// agent is shown. Passing a synthesized one would be inventing provenance.
		residue := herosagent.SelectResidue(ir, discovery.DiscoveryReport{}, nil)
		res, rerr := runner.Infer(ctx, herosagent.Input{
			TenantID:       tenantID,
			WorkflowID:     ref.WorkflowID,
			SourceRevision: ref.SourceRevision,
			RuleIR:         ir,
			Residue:        residue,
			Budget:         s.budget,
		}, def.ConfigHash, herosagent.PlacementPlatform)
		if rerr != nil {
			return rerr
		}
		out.Result = res
		// A fully rule-covered repository makes no provider call and produces no inference. Healthy, and
		// reported as its own reason so nobody reads it as a failure or as an empty answer.
		if res.Code == herosagent.CodeNothingToInfer {
			out.Reason = ReasonNothingToInfer
			return nil
		}
		out.Reason = ReasonAnalysed
		return nil
	})
	if serr != nil {
		if errors.Is(serr, sourceingest.ErrNoSource) {
			return Outcome{
				Reason: ReasonNoSource, ConfigHash: def.ConfigHash,
				Detail: fmt.Sprintf("no source is pushed for %s", ref),
			}, nil
		}
		return Outcome{Reason: ReasonFailed, ConfigHash: def.ConfigHash, Detail: serr.Error()}, nil
	}
	return out, nil
}

// modelParams maps the definition's pinned parameters onto the spec, and refuses a provider that cannot
// be called without them.
//
// The refusal names the PLATFORM as the thing to fix, exactly as the customer-side mapping does — the
// difference being that here the operator reading the log is the person who can fix it.
func modelParams(def runlink.AgentDefinition) (registry.ModelParams, error) {
	var out registry.ModelParams
	if p := def.ModelParams; p != nil {
		out = registry.ModelParams{
			MaxTokens:      p.MaxTokens,
			Temperature:    p.Temperature,
			ThinkingBudget: p.ThinkingBudget,
			Seed:           p.Seed,
			TimeoutSeconds: p.TimeoutSeconds,
		}
	}
	if equalFoldASCII(def.Provider, providergateway.ProviderAnthropic) && out.MaxTokens == nil {
		return registry.ModelParams{}, fmt.Errorf("the active definition binds %q, served by %s, and "+
			"the model version pins no max_tokens. The gateway refuses that call rather than choosing a "+
			"ceiling on this deployment's bill — publish a model catalog entry carrying "+
			`"params": {"max_tokens": <n>} and re-seed, or activate a definition on a model that needs none`,
			def.ModelID, def.Provider)
	}
	return out, nil
}

// equalFoldASCII avoids pulling strings in for one comparison whose inputs are both ASCII identifiers.
func equalFoldASCII(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
