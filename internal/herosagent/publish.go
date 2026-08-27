package herosagent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/providergateway"
	"github.com/heros-foreal/agentd/internal/registry"
	"github.com/heros-foreal/agentd/internal/variantspec"
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
	// ActivatedAtMS is when this version was activated, and NIL WHEN IT NEVER WAS.
	//
	// # 🔴 A pointer, because zero is a real instant and the database already says so
	//
	// The column is `activated_at_ms BIGINT` — nullable, documented "NULL unless active", with a
	// partial unique index on `(activated_at_ms IS NOT NULL) WHERE activated_at_ms IS NOT NULL`. The
	// database has always drawn the distinction; this field used to discard it, because `scanVersion`
	// read `activated.Int64` and dropped `activated.Valid`.
	//
	// What that produced was two halves of one question disagreeing about the same row: SQL selects the
	// active version `WHERE activated_at_ms IS NOT NULL`, which a 0 satisfies, while `Active()` was
	// `ActivatedAtMS != 0`, which a 0 fails. `PGVersionStore.Active` would hand back a row that reported
	// itself as not the thing it was asked for.
	//
	// 🚫 The fix is not a wider comparison — `>= 0` or a special case for zero. Those keep the two
	// predicates as two independent statements that happen to agree today, which is what let them drift
	// in the first place. A pointer makes "never activated" unrepresentable as a number, so there is
	// nothing left to disagree about: `Active()` IS `ActivatedAtMS != nil`, which IS `IS NOT NULL`.
	//
	// It is the shape this codebase already uses for exactly this problem — `Abstention.Confidence`
	// ("a decline with NO candidate is a different thing from one at 0.0") and
	// `registry.Envelope.TurnCeiling` ("a zero spend ceiling and an ABSENT one are different facts").
	ActivatedAtMS *int64
	CreatedAtMS   int64
}

// Active reports whether this version is the one serving inference.
//
// 🔴 It is `!= nil`, which is `activated_at_ms IS NOT NULL` — the SAME predicate the store selects on
// and the same one the partial unique index is built over. One question, one answer, in both languages.
func (v Version) Active() bool { return v.ActivatedAtMS != nil }

// ActivatedAt is when this version was activated, and whether it ever was.
//
// 🔴 Two returns rather than a bare int64, so a caller cannot render "never activated" as 1 January
// 1970. `AgentOverview.ServingSinceMS` reads this, and a surface saying a definition is serving
// without saying since when cannot answer the first question an incident asks.
func (v Version) ActivatedAt() (int64, bool) {
	if v.ActivatedAtMS == nil {
		return 0, false
	}
	return *v.ActivatedAtMS, true
}

// ActivatedAtOrZero is the timestamp for a caller that has already established the version is active.
//
// A named accessor rather than letting call sites dereference, so the nil case is handled in one place
// rather than at every reader — and so a reader who has NOT established it gets 0 with no panic.
func (v Version) ActivatedAtOrZero() int64 {
	at, _ := v.ActivatedAt()
	return at
}

// Publisher publishes and activates definitions.
type Publisher struct {
	models  ModelCatalogue
	secrets providergateway.Secrets
	store   VersionStore
	hosts   RunnerHosts
	nowMS   func() int64
	// axes resolves the loop and harness registry entries a node binds, so the loop axis can be
	// refused at PUBLISH rather than at run (task 3.5) and a loop's turn count can be checked against
	// its node's envelope ceiling (task 3.6).
	//
	// 🔴 Optional to WIRE and NOT optional to HAVE. A definition that binds no loop_ref publishes
	// without one — that is every pre-P36 definition, and requiring a registry to publish them would
	// break a path that works. A definition that DOES bind one is REFUSED when this is nil, because a
	// loop nobody could validate is a loop whose host service is discovered by whoever the run reaches.
	axes AxisRegistry
}

// AxisRegistry resolves the two P34 axes a node may bind, as this package needs them.
//
// A narrow interface over `*registry.Store` rather than the store itself: the publisher needs to
// resolve two refs, and handing it the whole registry would let a later edit reach for the other five
// and make this package a second configuration resolver.
type AxisRegistry interface {
	ResolveLoop(ctx context.Context, versionID string) (*registry.LoopEntry, error)
	ResolveHarness(ctx context.Context, versionID string) (*registry.HarnessEntry, error)
}

// WithAxisRegistry wires the loop and harness resolution the publish-time refusals need.
//
// Separate from NewPublisher, like `AgentService.WithRehearsal`: every pre-P36 caller is correct
// without one, and a required parameter would make them all pass nil to say so.
func (p *Publisher) WithAxisRegistry(a AxisRegistry) *Publisher {
	p.axes = a
	return p
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
// structure first (no I/O), then the topology (no I/O beyond the definition itself), then the axis
// refusals, then the registry and the secrets source.
func (p *Publisher) Publish(ctx context.Context, d Definition) (PublishResult, error) {
	if err := d.Validate(); err != nil {
		return PublishResult{}, err
	}
	// 🔴 TASK 4.1 / DESIGN D1 — THE AGENT'S TOPOLOGY GOES THROUGH THE CUSTOMER'S VALIDATOR.
	//
	// Not a lookalike. `variantspec.ValidateTopology` is the same exported function
	// `variantspec.Resolve` calls for a customer's Variant Spec, so a fan-in with no merge, a predicate
	// naming an unavailable symbol and a merge that violates a downstream typed contract are refused
	// here with the SAME sentinel and the SAME sentence a customer would read.
	if err := p.validateTopology(ctx, d); err != nil {
		return PublishResult{}, err
	}
	if err := p.refuseUnsuppliedStrategies(d); err != nil {
		return PublishResult{}, err
	}
	// Tasks 3.5 and 3.6 — the LOOP axis, refused at PUBLISH rather than at run.
	if err := p.checkLoopAxis(ctx, d); err != nil {
		return PublishResult{}, err
	}

	// Task 3.4 — every node's model must be REGISTERED. Checked here rather than at run for D11's
	// reason: the save succeeded, the agent is broken, and nothing in between said so.
	//
	// 🔴 PER NODE. A check that read `d.Primary()` would validate the first node and publish a graph
	// whose second node names a model nothing can resolve — which fails when a run reaches that node,
	// which is the failure this check exists to move left.
	deprecated := []string{}
	for i, n := range d.Nodes {
		where := d.nodeLabel(n, i)
		rec, err := p.model(ctx, n.ModelRef)
		if err != nil {
			return PublishResult{}, fmt.Errorf("the model%s: %w", where, err)
		}
		if rec.Deprecated {
			deprecated = append(deprecated, n.ModelRef)
		}
		if n.CriticModelRef != "" {
			if _, err := p.model(ctx, n.CriticModelRef); err != nil {
				return PublishResult{}, fmt.Errorf("the critic model%s: %w", where, err)
			}
		}
		// The credential must RESOLVE. Fail-closed: `NewSecretsFromEnv` already refuses to degrade on an
		// unrecognised source, "on the grounds that a deployment believing it is on a secrets manager and
		// is not is worse than one that will not start". An unresolvable REFERENCE is the same failure one
		// layer down and gets the same answer.
		if err := p.resolveCredential(ctx, n.CredentialRef); err != nil {
			return PublishResult{}, fmt.Errorf("the credential%s: %w", where, err)
		}
		if n.CriticCredentialRef != "" {
			if err := p.resolveCredential(ctx, n.CriticCredentialRef); err != nil {
				return PublishResult{}, fmt.Errorf("the critic credential%s: %w", where, err)
			}
		}
	}

	hash, err := d.ConfigHash()
	if err != nil {
		return PublishResult{}, err
	}
	out := PublishResult{ConfigHash: hash}
	if len(deprecated) > 0 {
		out.DeprecatedModel = strings.Join(dedupeSorted(deprecated), ", ")
	}

	// 🔴 TASK 3.7 / ErrNoChange — content determines identity, so an identical definition is the SAME
	// version. Unchanged by the shape change, and the fence on it is unchanged too: a duplicate row
	// would give an operator two identities for one configuration to reason about.
	if _, exists, err := p.store.Get(ctx, hash); err != nil {
		return PublishResult{}, fmt.Errorf("herosagent: reading version %s: %w", hash, err)
	} else if exists {
		return out, nil
	}

	out.Created = true
	return out, p.store.Put(ctx, Version{
		ConfigHash:     hash,
		Definition:     d,
		ModelRef:       DenormalisedModelRef(d),
		CredentialRef:  DenormalisedCredentialRef(d),
		RehearsalState: RehearsalPending, // 🔴 Never `passed` on publish. D7's gate is the whole point.
		CreatedAtMS:    p.nowMS(),
	})
}

// DenormalisedModelRef and DenormalisedCredentialRef are what the version row's query columns carry.
//
// 🔴 The DISTINCT SET, sorted and comma-joined — not the first node's value. For a single-node
// definition that is byte-identically the pre-P36 value, so every existing row and every existing
// query still reads the same. For a graph it is the honest answer to the question the columns exist to
// serve — "which versions spend the anthropic credential" — which the first node cannot answer, and
// which a NULL would answer with silence.
func DenormalisedModelRef(d Definition) string {
	refs := make([]string, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		refs = append(refs, n.ModelRef)
	}
	return strings.Join(dedupeSorted(refs), ", ")
}

// DenormalisedCredentialRef is the credential half. See DenormalisedModelRef.
func DenormalisedCredentialRef(d Definition) string {
	refs := make([]string, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		refs = append(refs, n.CredentialRef)
	}
	return strings.Join(dedupeSorted(refs), ", ")
}

func dedupeSorted(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// validateTopology routes the agent's graph through `variantspec.ValidateTopology` — D1's one code
// path (task 4.1).
//
// A single-node definition declares no topology (its ordering is implicit and `Validate` has already
// refused one), so there is nothing for the validator to see and it is skipped. That is not a second
// path: the validator's own answer for a spec with no groups and no edges is "nothing to check", and
// calling it to receive that answer would only cost an IR nobody needs.
func (p *Publisher) validateTopology(ctx context.Context, d Definition) error {
	if !d.MultiNode() {
		return nil
	}
	spec := d.Spec()
	if _, _, err := variantspec.ValidateTopology(ctx, &spec, AgentIR(d), nil); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidDefinition, err.Error())
	}
	return nil
}

// AgentIR is the agent's own nodes expressed as a discovery IR, so the shared topology validator has
// the same input shape it gets for a customer's workflow.
//
// # 🔴 Why the agent needs an IR at all, and why this one is honest
//
// `ValidateTopology` answers two questions that are properties of the NODES being wired: is a
// predicate's symbol in scope at the producing call site, and does a merge satisfy the downstream
// node's typed input contract. For a customer those come from Discovery reading their source. The
// agent has no source to read — it IS the analyser — so its contract is the one this package already
// enforces at runtime: every HEROS node consumes the assessment input and produces edges, labels and a
// narrative.
//
// 🚫 It is NOT an empty or permissive IR. An empty `output_schema` would make every merge trivially
// satisfiable, which would be the "quietly weaker internal path" D1 exists to prevent — the agent
// would pass a check a customer fails, on the same declaration.
func AgentIR(d Definition) *discovery.IR {
	nodes := make([]discovery.IRNode, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		nodes = append(nodes, discovery.IRNode{
			NodeID: n.NodeID, Kind: "static_definition",
			CallSite: discovery.IRCallSite{
				Symbol: n.NodeID, File: "internal/herosagent/runner.go",
				// 🔴 TASK 4.4 — THE PREDICATE VOCABULARY, RECORDED AS SCOPE.
				//
				// This is what makes a conditional edge in the agent's definition validated by the
				// EXISTING expression path rather than by a second one. `variantspec.validatePredicates`
				// refuses a predicate the producing call site does not record as in scope — the same rule
				// that governs a prompt slot's `expr` binding (ADR-004) — so declaring the closed set here
				// makes `analyst.made_up_thing` a publish-time refusal with no new code.
				//
				// 🚫 Recording a NIL scope would DEFER the check rather than pass it: `InScopeRecorded()`
				// is false for nil, and resolve then accepts every predicate. That is the vacuous pass
				// this phase keeps finding, arriving through an empty slice.
				InScope: AgentPredicateSymbols(),
			},
			IOContract: agentIOContract(),
		})
	}
	edges := make([]discovery.IREdge, 0, len(d.Edges))
	for _, e := range d.Edges {
		edges = append(edges, discovery.IREdge{
			FromNodeID: e.FromNodeID, ToNodeID: e.ToNodeID, Kind: e.Kind,
			// 🔴 `operator`, and it is the first thing in the product to write that value.
			//
			// The other three are wrong and each in a way worth naming: `frontend` claims a parser read
			// this out of source, and there is no source — the agent is not discovered, it is authored.
			// `detector` claims a rule established it from topology, and this IS the topology. `heros`
			// claims the agent inferred its own shape, which is precisely what D5 refuses.
			//
			// A human operator authored this edge in the axis editor and the version row records who.
			// That is exactly what `AuthorOperator` was reserved for.
			Author: string(discovery.AuthorOperator),
		})
	}
	return &discovery.IR{Workflow: discovery.IRWorkflow{ID: "heros"}, Nodes: nodes, Edges: edges}
}

// agentIOContract is what every HEROS node consumes FROM UPSTREAM and produces.
//
// # 🔴 The input contract describes what a PREDECESSOR delivers, not what the node needs to run
//
// This is the correction the agent's first real fan-in forced, and it is worth the paragraph because
// the obvious version is wrong in a way that only shows up on a graph.
//
// The first attempt declared `workflow_id`, `source_revision` and `residue` as REQUIRED input — which
// is what a HEROS node genuinely needs. That made every fan-in unsatisfiable: a `namespaced` merge
// delivers `{left: …, right: …}`, which supplies none of those names, so the shared validator refused
// a topology that is perfectly runnable.
//
// The validator was right and the contract was wrong. In `internal/typedcontract`, a node's input
// contract is what its PREDECESSORS must supply — that is the whole meaning of checking a merge against
// it. The assessment input is AMBIENT: the runner hands the same `Input` to every node, in every
// topology, including the single-node one where there is no predecessor at all. Declaring it as
// required input asserts a delivery obligation no topology can meet, on any edge, ever.
//
// 🚫 So the input requires NOTHING, and that is not the trivially-satisfiable contract D1 warns about.
// The OUTPUT contract is where the force is, and the check it powers is live: two HEROS nodes both
// declare `edges` and `labels`, so an `all-fields` merge between them COLLIDES and is refused —
// `namespaced` is the answer for a fan-in whose nodes produce the same field names, which is exactly
// what `MergeNamespaced`'s own comment says it is for. `TestAnAllFieldsMergeBetweenTwoAgentNodesIsRefused`
// asserts the refusal, so the contract's teeth are proved rather than assumed.
func agentIOContract() discovery.IRIOContract {
	nodeFacts := map[string]any{
		"edges":     map[string]any{"type": "array"},
		"labels":    map[string]any{"type": "array"},
		"narrative": map[string]any{"type": "string"},
	}
	return discovery.IRIOContract{
		InputSchema: map[string]any{
			"type": "object",
			// What an upstream node can hand this one. Optional, because a node with no predecessor is
			// the DEFAULT shape and it runs on the ambient assessment input alone.
			"properties": nodeFacts,
			"required":   []any{},
		},
		OutputSchema: map[string]any{
			"type":       "object",
			"properties": nodeFacts,
			// 🔴 REQUIRED, and this is what makes the merge check bite. A node that declared no required
			// output would collide with nothing and satisfy anything.
			"required": []any{"edges", "labels"},
		},
	}
}

// checkLoopAxis is tasks 3.5 and 3.6: the loop axis refused at PUBLISH, not at run.
//
// # 🔴 Why publish and not run
//
// D11's argument, one axis later. A loop whose host service this runner cannot supply, discovered when
// an analysis reaches the node, is discovered by somebody who did not choose it and cannot tell whether
// it is a bug or a configuration. The operator who bound it has moved on. `ErrHostServiceMissing`
// already said this for harness and memory; the loop axis arrives under the same rule rather than
// under a new one.
//
// # 🚫 Both checks are the REGISTRY's, not this package's
//
// `registry.HostServicesForLoop` and the envelope's `TurnCeiling` are the same values
// `variantspec.checkEnvelopeAdmits` reads for a customer's node. This is a second GATE in front of the
// same rule — earlier — and never a second rule.
func (p *Publisher) checkLoopAxis(ctx context.Context, d Definition) error {
	for i, n := range d.Nodes {
		if strings.TrimSpace(n.LoopRef) == "" {
			continue
		}
		where := d.nodeLabel(n, i)
		if p.axes == nil {
			// 🔴 REFUSED, never published-and-hoped. A loop nobody could validate is one whose missing
			// host service is discovered by whoever the run reaches — which is exactly the failure this
			// check exists to move left.
			return fmt.Errorf("%w: a loop_ref is bound%s and this deployment wired no axis registry, so "+
				"the loop's host services and turn count could not be checked. Publishing it would defer "+
				"that check to whoever an analysis reaches", ErrHostServiceMissing, where)
		}
		loop, err := p.axes.ResolveLoop(ctx, n.LoopRef)
		if err != nil {
			return fmt.Errorf("%w: the loop_ref %q%s does not resolve: %s",
				ErrInvalidDefinition, n.LoopRef, where, err.Error())
		}
		var env *registry.Envelope
		if strings.TrimSpace(n.HarnessRef) != "" {
			h, herr := p.axes.ResolveHarness(ctx, n.HarnessRef)
			if herr != nil {
				return fmt.Errorf("%w: the harness_ref %q%s does not resolve: %s",
					ErrInvalidDefinition, n.HarnessRef, where, herr.Error())
			}
			e, isEnvelope, eerr := registry.EnvelopeOf(h)
			if eerr != nil {
				return fmt.Errorf("%w: the envelope%s could not be read: %s",
					ErrInvalidDefinition, where, eerr.Error())
			}
			if isEnvelope {
				env = &e
			}
		}
		// Task 3.5 — the host services, naming the loop AND what is missing.
		if err := p.refuseMissingLoopHosts(loop, env, where); err != nil {
			return err
		}
		// Task 3.6 — the turn count against the envelope's ceiling, NAMING BOTH VALUES.
		if err := refuseOverCeiling(loop, env, where); err != nil {
			return err
		}
	}
	return nil
}

// refuseMissingLoopHosts refuses a loop needing a second actor nothing supplies.
//
// It reads BOTH sources of a host service and requires both to agree: the node's envelope (what the
// configuration declares) and `RunnerHosts` (what this deployment's runner actually has). Either one
// missing is a refusal, because a loop that has a declaration and no runner is as unrunnable as one
// with a runner and no declaration.
func (p *Publisher) refuseMissingLoopHosts(loop *registry.LoopEntry, env *registry.Envelope, where string) error {
	need := registry.HostServicesForLoop(loop.Spec.Strategy)
	if len(need) == 0 {
		return nil
	}
	var missing []string
	if env == nil {
		missing = append(missing, need...)
	} else {
		missing = append(missing, env.MissingServices(need...)...)
	}
	for _, svc := range need {
		if !p.hosts.supplies(svc) && !contains(missing, svc) {
			missing = append(missing, svc)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return fmt.Errorf("%w: the %q loop%s needs %s, and this deployment supplies %s. 🚫 It is NOT "+
		"degraded to a strategy that needs no second actor — a critic-loop without a critic IS reflexion, "+
		"and running it under critic-loop's config_hash would report one strategy as another. Refused at "+
		"publish rather than at run, so the operator who chose it is the one who reads this",
		ErrHostServiceMissing, loop.Spec.Strategy, where,
		registry.HostServiceDisplay(need), missingDisplay(missing))
}

func missingDisplay(missing []string) string {
	return "none of " + registry.HostServiceDisplay(missing)
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

// supplies maps a registry host-service name onto what this runner actually has.
//
// 🔴 A switch over the CLOSED registry vocabulary with no default-true branch. An unknown service name
// is NOT supplied — a default that answered "yes" for a service nobody has implemented would make every
// future strategy publishable and unrunnable.
func (h RunnerHosts) supplies(service string) bool {
	switch service {
	case registry.HostServiceToolExecutor:
		return h.ToolInvoker
	case registry.HostServicePlanner:
		return h.Planner
	case registry.HostServiceCritic:
		return h.Critic
	default:
		return false
	}
}

// refuseOverCeiling is task 3.6: a loop asking for more turns than its node's envelope permits, NAMING
// BOTH VALUES.
//
// 🔴 Naming both is the requirement, not a nicety. "Too many turns" leaves an operator unable to tell
// whether to lower their value or ask for a higher policy — and those are requests to two different
// people. It is `variantspec.checkTurnCeiling`'s sentence, at publish.
func refuseOverCeiling(loop *registry.LoopEntry, env *registry.Envelope, where string) error {
	if env == nil || env.TurnCeiling == nil {
		return nil
	}
	// 🔴 The bool, not a fabricated 1. A loop that CHOSE no turn count (`single-shot`, whose schema
	// forbids the key) has nothing to compare, and comparing a made-up value would make this check pass
	// on a shape it never examined.
	chosen, didChoose := loop.MaxTurns()
	if !didChoose || chosen <= *env.TurnCeiling {
		return nil
	}
	return fmt.Errorf("%w: the loop%s asks for max_turns=%d and its harness envelope's turn_ceiling is "+
		"%d. The ceiling is imposed and the turn count is chosen, so either lower max_turns to %d or "+
		"less, or ask whoever owns the envelope to raise turn_ceiling",
		ErrInvalidDefinition, where, chosen, *env.TurnCeiling, *env.TurnCeiling)
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
	//
	// 🔴 PER NODE. The pre-P36 version read one pair off the definition; a graph carries one pair per
	// node, and a check that read only the first would let a second node bind a critic with no
	// credential — the exact failure this refusal exists to catch, one node further in.
	for i, n := range d.Nodes {
		if n.CriticModelRef != "" && n.CriticCredentialRef == "" {
			return fmt.Errorf("%w: a critic model is bound with no credential of its own%s. A second "+
				"model is a second cost AND a second credential resolution; binding one without the other "+
				"publishes a definition that fails at the moment it is used",
				ErrInvalidDefinition, d.nodeLabel(n, i))
		}
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
		// 🔴 P36 task 5.4 — the gate is UNCHANGED IN FORCE for a multi-node definition, and the sentence
		// says why it matters more there. It is the same rule; a graph is where somebody would want an
		// exception, because rehearsing five nodes costs five times as much.
		graph := ""
		if v.Definition.MultiNode() {
			graph = fmt.Sprintf(" This definition declares %d nodes, so it is %d configurations' worth "+
				"of behaviour that nothing has measured — and the blast radius is every tenant at once, "+
				"because this is the platform's own agent rather than a per-tenant configuration.",
				len(v.Definition.Nodes), len(v.Definition.Nodes))
		}
		return fmt.Errorf("%w: %s is %q. A published definition is INACTIVE until it has run against the "+
			"pinned fixtures and met the floor on EVERY ONE INDIVIDUALLY — the mean is reported, the gate "+
			"reads the minimum.%s", ErrRehearsalNotPassed, confighashDisplay(configHash),
			v.RehearsalState, graph)
	}
	return p.store.Activate(ctx, configHash, p.nowMS())
}

// Rollback makes a PREVIOUSLY PUBLISHED version the one serving inference again (P36 task 5.5).
//
// # 🔴 It is ONE ACT, and it is deliberately a thin call rather than a new mechanism
//
// Rolling back is activating a version that already exists. It is NOT re-authoring the older shape,
// and the difference is the whole requirement: re-authoring means an operator retypes a configuration
// under pressure, during an incident, from a rendering of it — and any transcription error produces a
// DIFFERENT `config_hash`, which is a third configuration nobody has ever measured, activated in place
// of the one that was known to work.
//
// It also would not be the same definition even when retyped perfectly: a shape that can no longer be
// authored (`ClassifyPin` reports this as `authorable: false`) cannot be retyped at all, so
// re-authoring is not merely risky, it is sometimes impossible for exactly the version somebody most
// wants back.
//
// 🚫 It does NOT re-run the rehearsal. The version already passed — that is why it was serving — and
// its verdict is on its immutable row. Re-measuring it would spend provider tokens during an incident
// to reproduce a number that is already recorded, and it would make rollback slow at the moment speed
// is the point.
//
// 🚫 It does NOT re-run pinned inferences, for the reason activation never does (D4): a configuration
// change is a pinning event, not a re-inference.
func (p *Publisher) Rollback(ctx context.Context, configHash string) error {
	v, ok, err := p.store.Get(ctx, configHash)
	if err != nil {
		return fmt.Errorf("herosagent: reading %s: %w", configHash, err)
	}
	if !ok {
		// 🔴 Named as a ROLLBACK failure rather than as "no such definition". An operator reaching for
		// a rollback has a hash from a version list or an incident note, and "that version does not
		// exist on this deployment" sends them somewhere quite different from "that hash is malformed".
		return fmt.Errorf("%w: no published definition on this deployment has hash %s, so there is "+
			"nothing to roll back TO. Rollback activates a version that already exists; it never "+
			"re-authors one, because a retyped configuration is a third configuration nobody has "+
			"measured", ErrInvalidDefinition, confighashDisplay(configHash))
	}
	if v.RehearsalState != RehearsalPassed {
		return fmt.Errorf("%w: %s is %q, so it never served and is not a state to return to",
			ErrRehearsalNotPassed, confighashDisplay(configHash), v.RehearsalState)
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
