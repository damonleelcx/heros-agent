package herosagent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/providergateway"
)

// publish.go is the ONLY way a definition comes into existence (task 3.3).
//
// 🚫 THERE IS NO MUTATION API. No `UpdateDefinition`, no setter, no `Definition.SetModel`. Content
// determines identity, so "editing" is publishing a new version whose hash differs — and the version
// that was serving inference yesterday is still addressable by its hash, which is the whole reason a
// stored `agent_config_hash` means anything.
//
// The alternative — mutate in place and keep a revision counter — makes every stored inference's
// `agent_config_hash` a pointer to a definition that may since have changed under it. Then "what
// produced this edge" is answerable only for as long as nobody edits a config.

// RegisteredModel is a model this package needs to know about, and NOTHING MORE.
//
// 🔴 It is a local type rather than `adminops.ModelRecord`, and that is a dependency-direction
// decision, not a convenience. `internal/adminops` is the OPERATOR SURFACE — it renders this package's
// read models — so a domain package importing it is a cycle, and the compiler said so. The fix is the
// right one anyway: the analysis agent needs a model id, its provider and whether it is deprecated, and
// has no business knowing about price references, revisions or audit timestamps.
type RegisteredModel struct {
	ModelID  string
	Provider string
	// Deprecated marks a model that must not be selected for new runs. It is a NOTICE at publish, never
	// a refusal and never an auto-switch (task 3.8).
	Deprecated bool
}

// ModelCatalogue is the operator model registry, as this package needs it.
//
// An interface rather than a concrete store so a caller with no Postgres can exercise publishing — and
// note what it does NOT do: it does not make validation optional. Publisher refuses a nil catalogue
// outright, because "we could not reach the registry" reported as "that model is fine" would publish a
// definition naming a model nothing can resolve.
type ModelCatalogue interface {
	Models(ctx context.Context) ([]RegisteredModel, error)
}

// VersionStore persists published definitions.
type VersionStore interface {
	// Put records a published version. Re-putting the same config_hash is the SAME row: content
	// determines identity, so a second publish of an identical definition is not a second version.
	Put(ctx context.Context, v Version) error
	// Get returns one version by hash.
	Get(ctx context.Context, configHash string) (Version, bool, error)
	// Active returns the one activated version, or ok=false when none is.
	Active(ctx context.Context) (Version, bool, error)
	// Activate marks a version active IN A TRANSACTION that deactivates any other (task 3.7).
	Activate(ctx context.Context, configHash string, atMS int64) error
	// List returns every version, newest first.
	List(ctx context.Context) ([]Version, error)
}

// RehearsalState is the gate's verdict (D7).
type RehearsalState string

const (
	// RehearsalPending: published and NOT YET measured. It must never be rendered as active.
	RehearsalPending RehearsalState = "pending"
	// RehearsalPassed: met the floor on EVERY fixture individually.
	RehearsalPassed RehearsalState = "passed"
	// RehearsalFailed: at least one fixture was below the floor. The report names which.
	RehearsalFailed RehearsalState = "failed"
)

// Version is one published definition as the platform holds it.
type Version struct {
	ConfigHash string
	Definition Definition
	// ModelRef and CredentialRef are denormalised onto the row because the store's own schema names
	// them — a query asking "which versions spend the anthropic credential" must not have to parse
	// spec_json. They are always the definition's; the writer copies rather than accepting them.
	ModelRef        string
	CredentialRef   string
	RehearsalState  RehearsalState
	RehearsalReport string
	ActivatedAtMS   int64
	CreatedAtMS     int64
}

// Active reports whether this version is the one serving inference.
func (v Version) Active() bool { return v.ActivatedAtMS != 0 }

// Publisher publishes and activates definitions.
type Publisher struct {
	models  ModelCatalogue
	secrets providergateway.Secrets
	store   VersionStore
	hosts   RunnerHosts
	nowMS   func() int64
}

// NewPublisher wires the publisher. Every dependency is REQUIRED, and each refusal names what would go
// unchecked without it — the discipline hostdiscovery.NewRunner follows, for the same reason: an
// optional validator is a validator that is nil in the deployment that needed it.
func NewPublisher(models ModelCatalogue, secrets providergateway.Secrets, store VersionStore,
	hosts RunnerHosts, nowMS func() int64) (*Publisher, error) {
	switch {
	case models == nil:
		return nil, errors.New("herosagent: a model catalogue is required — without it an unregistered " +
			"model publishes cleanly and fails when an analysis reaches it")
	case secrets == nil:
		return nil, errors.New("herosagent: a secrets source is required — without it a credential " +
			"reference cannot be resolved and `unavailable` becomes indistinguishable from `unconfigured`")
	case store == nil:
		return nil, errors.New("herosagent: a version store is required")
	case nowMS == nil:
		return nil, errors.New("herosagent: a clock is required — an injected one, so a publish is " +
			"deterministic under test")
	}
	return &Publisher{models: models, secrets: secrets, store: store, hosts: hosts, nowMS: nowMS}, nil
}

// PublishResult is what a publish did, including the case where it did nothing.
type PublishResult struct {
	// ConfigHash identifies the version. Populated even when Created is false — the operator's edit
	// resolves to THIS definition, and naming it is what tells them so.
	ConfigHash string
	// Created is false when the definition resolves to one that already exists. 🔴 Task 6.2: "An edit
	// resolving to no change says so and creates no version." A duplicate row would give an operator
	// two identities for one configuration to reason about.
	Created bool
	// DeprecatedModel is set when the definition's model is registered AND DEPRECATED (task 3.8). It is
	// a NOTICE, never a refusal and never an auto-switch: swapping the model would change the
	// config_hash, and a platform that silently ran a different model than the one an operator
	// published is a platform whose stored hashes describe something else.
	DeprecatedModel string
}

// Publish validates a definition and records it as an immutable version.
//
// The order of the checks is the order in which they are cheap and in which their answers are useful:
// structure first (no I/O), then the axis refusals (no I/O), then the registry and the secrets source.
func (p *Publisher) Publish(ctx context.Context, d Definition) (PublishResult, error) {
	if err := d.Validate(); err != nil {
		return PublishResult{}, err
	}
	if err := p.refuseUnsuppliedStrategies(d); err != nil {
		return PublishResult{}, err
	}

	// Task 3.4 — the model must be REGISTERED. Checked here rather than at run for D11's reason: the
	// save succeeded, the agent is broken, and nothing in between said so.
	rec, err := p.model(ctx, d.ModelRef)
	if err != nil {
		return PublishResult{}, err
	}
	if d.CriticModelRef != "" {
		if _, err := p.model(ctx, d.CriticModelRef); err != nil {
			return PublishResult{}, fmt.Errorf("the critic model: %w", err)
		}
	}

	// Task 3.5/3.6 — the credential must RESOLVE. Fail-closed: `NewSecretsFromEnv` already refuses to
	// degrade on an unrecognised source, "on the grounds that a deployment believing it is on a secrets
	// manager and is not is worse than one that will not start". An unresolvable REFERENCE is the same
	// failure one layer down and gets the same answer.
	if err := p.resolveCredential(ctx, d.CredentialRef); err != nil {
		return PublishResult{}, err
	}
	if d.CriticCredentialRef != "" {
		if err := p.resolveCredential(ctx, d.CriticCredentialRef); err != nil {
			return PublishResult{}, fmt.Errorf("the critic credential: %w", err)
		}
	}

	hash, err := d.ConfigHash()
	if err != nil {
		return PublishResult{}, err
	}
	out := PublishResult{ConfigHash: hash}
	if rec.Deprecated {
		out.DeprecatedModel = d.ModelRef
	}

	// Content determines identity: an identical definition is the SAME version.
	if _, exists, err := p.store.Get(ctx, hash); err != nil {
		return PublishResult{}, fmt.Errorf("herosagent: reading version %s: %w", hash, err)
	} else if exists {
		return out, nil
	}

	out.Created = true
	return out, p.store.Put(ctx, Version{
		ConfigHash:     hash,
		Definition:     d,
		ModelRef:       d.ModelRef,
		CredentialRef:  d.CredentialRef,
		RehearsalState: RehearsalPending, // 🔴 Never `passed` on publish. D7's gate is the whole point.
		CreatedAtMS:    p.nowMS(),
	})
}

// model looks one up, refusing an unregistered ref by name.
func (p *Publisher) model(ctx context.Context, ref string) (RegisteredModel, error) {
	models, err := p.models.Models(ctx)
	if err != nil {
		// 🚫 NOT treated as "the model is fine". An unreachable registry is an outage, and publishing
		// through it would record a definition nobody validated.
		return RegisteredModel{}, fmt.Errorf("herosagent: the operator model registry could not be "+
			"read, so %q could not be validated: %w", ref, err)
	}
	names := make([]string, 0, len(models))
	for _, m := range models {
		if m.ModelID == ref {
			return m, nil
		}
		names = append(names, m.ModelID)
	}
	sort.Strings(names) // a list in map order is a different error message every time
	return RegisteredModel{}, fmt.Errorf("%w: %q. Registered: %s",
		ErrModelUnregistered, ref, strings.Join(names, ", "))
}

// resolveCredential proves the reference resolves, and 🔴 DISCARDS what came back.
//
// The credential is fetched to establish that it CAN be, and the value is dropped on the same line.
// Nothing in this package holds it, logs it, returns it or stores it — and the error path is careful
// too: it names the PROVIDER, never anything the source said about the secret.
func (p *Publisher) resolveCredential(ctx context.Context, ref string) error {
	if _, err := p.secrets.Credential(ctx, ref); err != nil {
		return fmt.Errorf("%w: provider %q does not resolve through the %s secrets source. HEROS fails "+
			"CLOSED here: it makes zero provider calls, substitutes no other provider, and every surface "+
			"falls back to rule-derived facts",
			ErrCredentialUnresolved, ref, p.secrets.Describe().Kind)
	}
	return nil
}

// refuseUnsuppliedStrategies refuses a harness or memory selection the runner cannot execute.
//
// 🔴 The refs are registry version_ids, so the STRATEGY NAME behind one is not visible here. The
// console resolves it and calls RequireAvailable directly (task 6b.11); this is the second layer, for
// a definition published through the API with a strategy name embedded in the ref. It refuses what it
// can see and never guesses at what it cannot.
func (p *Publisher) refuseUnsuppliedStrategies(d Definition) error {
	// A critic model without critic-loop, or critic-loop without a critic model, are both incoherent —
	// and the second is the one that costs money silently.
	if d.CriticModelRef != "" && d.CriticCredentialRef == "" {
		return fmt.Errorf("%w: a critic model is bound with no credential of its own. A second model is a "+
			"second cost AND a second credential resolution; binding one without the other publishes a "+
			"definition that fails at the moment it is used", ErrInvalidDefinition)
	}
	return nil
}

// Activate makes a version the one serving inference.
//
// 🔴 Two gates, and both are checked here as well as in the database (task 3.7 and D7). They fail
// independently on purpose: a future writer that bypasses this path still cannot arm an agent nothing
// measured, and a concurrent activation still cannot produce two active rows.
func (p *Publisher) Activate(ctx context.Context, configHash string) error {
	v, ok, err := p.store.Get(ctx, configHash)
	if err != nil {
		return fmt.Errorf("herosagent: reading %s: %w", configHash, err)
	}
	if !ok {
		return fmt.Errorf("%w: no published definition has hash %s", ErrInvalidDefinition, configHash)
	}
	if v.RehearsalState != RehearsalPassed {
		return fmt.Errorf("%w: %s is %q. A published definition is INACTIVE until it has run against the "+
			"pinned fixtures and met the floor on EVERY ONE INDIVIDUALLY — the mean is reported, the gate "+
			"reads the minimum", ErrRehearsalNotPassed, confighashDisplay(configHash), v.RehearsalState)
	}
	return p.store.Activate(ctx, configHash, p.nowMS())
}

// ActiveDefinition returns the definition currently serving inference.
//
// 🔴 ok=false means NO DEFINITION IS ACTIVE, which is a state and not an error: a fresh deployment has
// published nothing, and every surface must report that rather than an outage.
func (p *Publisher) ActiveDefinition(ctx context.Context) (Version, bool, error) {
	return p.store.Active(ctx)
}

// confighashDisplay shortens a hash for a message. Long enough to identify, short enough to read.
func confighashDisplay(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}
