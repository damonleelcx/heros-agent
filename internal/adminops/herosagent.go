package adminops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/heros-foreal/agentd/internal/adminaudit"
	"github.com/heros-foreal/agentd/internal/adminrbac"
	"github.com/heros-foreal/agentd/internal/herosagent"
)

// herosagent.go is the operator's read/write surface for the platform's own analysis agent (P30 §6).
//
// # 🔴 What this surface must never let happen
//
// Three things, and each has already happened somewhere in this product:
//
//  1. A version rendered as ACTIVE before its rehearsal gate passed. The surface always names which
//     definition is actually serving inference (task 6.3), separately from whichever one an operator
//     is looking at.
//  2. `unpriced` rendered as `0`. A model with no published price produces a real token count and NO
//     cost, and a zero there is the most reassuring possible lie about a bill (task 6.5).
//  3. A tenant's placement rendered as `disabled` without saying whether somebody CHOSE that or
//     inherited it. Q2 made `disabled` the default precisely so enabling is deliberate — and a
//     surface that cannot tell a default from a decision cannot tell an operator whether anybody has
//     considered this tenant (task 6.7).

// PlacementSource distinguishes a DEFAULT from a DECISION.
type PlacementSource string

const (
	// PlacementDefaulted: nobody has set this tenant's placement. It is `disabled` because Q2 made
	// that the default, not because anybody looked.
	PlacementDefaulted PlacementSource = "defaulted"
	// PlacementExplicit: somebody set it. Including setting it to `disabled`, which is a decision and
	// must not read the same as never having been considered.
	PlacementExplicit PlacementSource = "explicit"
)

// AxisStatus is the three-valued state task 6.10 requires of every axis.
type AxisStatus string

const (
	// AxisSet: the operator bound a value.
	AxisSet AxisStatus = "set"
	// AxisDefaulted: no value bound; the runtime's default applies.
	AxisDefaulted AxisStatus = "defaulted"
	// AxisNotInEffect: a value is bound and CANNOT take effect. It always carries a reason — an axis
	// that is inert for an unstated reason is the state an operator cannot act on.
	AxisNotInEffect AxisStatus = "not_in_effect"
)

// AgentAxisRow is one axis as the console renders it.
type AgentAxisRow struct {
	// NodeID is the node this axis belongs to, or "" for the definition-level `graph` axis.
	//
	// 🔴 Carried on every row, including a single-node definition's. A field that appeared only when a
	// definition had two nodes would make the console's row key change shape underneath it, and the one
	// question this surface exists to answer after P36 is WHICH NODE.
	NodeID string     `json:"node_id"`
	Axis   string     `json:"axis"`
	Status AxisStatus `json:"status"`
	// Value is the bound reference, or the default's name. Never a secret and never a key.
	Value string `json:"value"`
	// Reason is REQUIRED when Status is not_in_effect and empty otherwise.
	Reason string `json:"reason,omitempty"`
	// Editable is false for the wiring axis, which is fixed. It is SHOWN rather than omitted: a hidden
	// axis is indistinguishable from one that does not exist.
	Editable bool `json:"editable"`
}

// AgentOverview is the `/agent` read model (task 6.1).
type AgentOverview struct {
	// Serving is the definition ACTUALLY serving inference, by hash. Empty when none is active.
	//
	// 🔴 Named separately from everything else on this view, because "the definition I am looking at"
	// and "the definition answering analyses right now" are different questions and a surface that
	// conflates them tells an operator they shipped something they did not.
	Serving string `json:"serving_config_hash"`
	// ServingSince is when it was activated, epoch ms. Zero when none is active.
	ServingSinceMS int64 `json:"serving_since_ms"`
	// State says why nothing is serving, when nothing is: `none_published` | `pending_rehearsal` |
	// `rehearsal_failed` | `serving`. A closed set the console switches on.
	State string `json:"state"`
	// Sentence explains State. Always populated.
	Sentence string `json:"sentence"`
	// Axes is every axis with its three-valued status.
	Axes []AgentAxisRow `json:"axes"`
	// RehearsalState and RehearsalReport are the gate's verdict for the newest published version.
	RehearsalState  string `json:"rehearsal_state"`
	RehearsalReport string `json:"rehearsal_report,omitempty"`
	// StoredInferences is how many pinned results exist fleet-wide. The number that says whether this
	// agent has ever done anything.
	StoredInferences int `json:"stored_inferences"`
	// InferencesKnown is false when no inference store is wired: the count is then meaningless and
	// renders as unknown rather than as zero. Same discipline as AxisView.AdoptionKnown.
	InferencesKnown bool `json:"inferences_known"`
	// Harness and Memory availability, computed from what the RUNNER supplies (D11, D13).
	HarnessAvailability []herosagent.Availability `json:"harness_availability"`
	MemoryAvailability  []herosagent.Availability `json:"memory_availability"`
	// Versions is every published definition, newest first.
	Versions []AgentVersionRow `json:"versions"`
	// Nodes is the SERVING definition's node list with its live per-node numbers (P36 tasks 6.4, 8.1).
	//
	// 🔴 Per node and never only as an aggregate. An aggregate over a graph says the agent is slow; it
	// does not say WHICH NODE is slow, and that is the only form of the answer anybody can act on.
	Nodes []AgentNodeRow `json:"nodes"`
	// NodesKnown is false when no per-node source is wired: the numbers are then meaningless and render
	// as unknown rather than as zero. Same discipline as InferencesKnown.
	NodesKnown bool `json:"nodes_known"`
	// KillSwitchArmed reports whether HEROS is halted through the platform's existing durable switch
	// (task 6.8). Carried on this view so the agent surface cannot show a healthy agent that is in
	// fact halted.
	KillSwitchArmed bool   `json:"kill_switch_armed"`
	KillSwitchNote  string `json:"kill_switch_note,omitempty"`
	CanAdmin        bool   `json:"can_admin"`
}

// AgentNodeRow is one node of the serving definition, with what it has done (P36 tasks 6.4, 8.1).
type AgentNodeRow struct {
	NodeID string `json:"node_id"`
	// The axis bindings, so the operator surface can answer "which node is which" without a second read.
	PromptRef  string `json:"prompt_ref"`
	ModelRef   string `json:"model_ref"`
	LoopRef    string `json:"loop_ref,omitempty"`
	HarnessRef string `json:"harness_ref"`
	// Inferences, ProviderCalls, TokensIn, TokensOut, Failures, Skips and LatencyMS are the live
	// counters. 🔴 They are IN-PROCESS, not a database aggregate: a per-request query over
	// `heros_inference.nodes_json` would be the real-time read against the events table a CQRS split
	// exists to prevent, and a health surface that goes slow when the database does reports the wrong
	// thing at the worst moment.
	Inferences    int64 `json:"inferences"`
	ProviderCalls int64 `json:"provider_calls"`
	TokensIn      int64 `json:"tokens_in"`
	TokensOut     int64 `json:"tokens_out"`
	Failures      int64 `json:"failures"`
	Skips         int64 `json:"skips"`
	// LatencyMS is the mean over this node's completed calls. 🔴 Zero with `Inferences == 0` is NOT a
	// fast node; the console renders it as "not yet run" for that reason.
	LatencyMS int64 `json:"latency_ms"`
}

// AgentVersionRow is one published definition in the list.
type AgentVersionRow struct {
	ConfigHash string `json:"config_hash"`
	// Display is the shortened hash an operator reads.
	Display        string `json:"display"`
	ModelRef       string `json:"model_ref"`
	CredentialRef  string `json:"credential_ref"`
	RehearsalState string `json:"rehearsal_state"`
	// Active is TRUE for at most one row. 🔴 A `pending` row is never Active, and the console must not
	// derive activity from recency.
	Active      bool  `json:"active"`
	CreatedAtMS int64 `json:"created_at_ms"`
	// Nodes is how many nodes this version declares (P36). Two versions differing only in TOPOLOGY are
	// otherwise indistinguishable in this list — same model, same credential, different agent.
	Nodes int `json:"nodes"`
	// RollbackTarget is true when this version could be rolled back TO: it passed and is not serving.
	//
	// 🔴 Computed here rather than in the console, so the control is offered exactly where the backend
	// would accept it. A console that decided this itself would eventually offer a button the backend
	// refuses, which is the "offered and then refused" failure the surfaces map exists to prevent.
	RollbackTarget bool `json:"rollback_target"`
}

// AgentSpendRow is one tenant's meter (task 6.5).
type AgentSpendRow struct {
	TenantID   string `json:"tenant_id"`
	Inferences int    `json:"inferences"`
	TokensIn   int64  `json:"tokens_in"`
	TokensOut  int64  `json:"tokens_out"`
	// EstimatedCost is meaningful ONLY when Priced is true.
	EstimatedCost float64 `json:"estimated_cost"`
	// 🔴 Priced=false means UNPRICED, and the console renders the word rather than a number. A model
	// with no published price produces real tokens and no cost; showing `0` there reports a spend
	// nobody incurred.
	Priced bool `json:"priced"`
	// Placement and PlacementSource, so the meter and the switch cannot disagree.
	Placement       string          `json:"placement"`
	PlacementSource PlacementSource `json:"placement_source"`
	// Cap is this tenant's ceiling in tokens. Zero means no per-tenant cap; the fleet cap still applies.
	Cap int64 `json:"cap_tokens"`
}

// AgentSpendView is the `/agent/spend` read model.
type AgentSpendView struct {
	// Estimated is TRUE and stated on the wire. Every cost here is derived from a price REFERENCE and
	// a token count; it is not an invoice, and a renderer must not present it as one.
	Estimated bool            `json:"estimated"`
	Rows      []AgentSpendRow `json:"rows"`
	// FleetCap is the fleet-wide token ceiling. Zero means none is set — which is a real and dangerous
	// state, and the console says so rather than rendering an empty cell.
	FleetCap int64 `json:"fleet_cap_tokens"`
	// UnpricedTenants counts rows whose cost cannot be estimated, so a reader knows the total below is
	// incomplete rather than small.
	UnpricedTenants int  `json:"unpriced_tenants"`
	CanAdmin        bool `json:"can_admin"`
	// Placements is the closed set an operator may choose between, sent by the package that OWNS the
	// vocabulary.
	//
	// 🔴 On the wire rather than typed into the console's markup, because a placement editor listing
	// `platform, customer, disabled` in its own `<select>` would be the FOURTH copy of a closed set —
	// after the parser, the runner's gate, and the store. [AgentService.SetPlacement] already carries
	// the note about the third copy and how it failed quietly; an editor is the worst place for the
	// next one, since the symptom is that a newly added placement is unreachable from the one surface
	// that exists to set it, and the surface looks entirely healthy while being wrong.
	//
	// `herosagent.Placements` is documented as existing for exactly this: "a console can render every
	// option rather than the ones somebody remembered".
	Placements []string `json:"placements"`
}

// PublishPreview is what a publish WOULD do (task 6.2).
type PublishPreview struct {
	// ConfigHash the edit resolves to.
	ConfigHash string `json:"config_hash"`
	Display    string `json:"display"`
	// Changes is the resolved diff against the ACTIVE definition, axis by axis. Empty when the edit
	// resolves to the active definition.
	Changes []AxisChange `json:"changes"`
	// NoChange is TRUE when this edit resolves to a definition that already exists. 🔴 It creates no
	// version and says so: a duplicate row gives an operator two identities for one configuration.
	NoChange bool `json:"no_change"`
	// AlreadyPublished is true when the hash exists but is not the active one — a previously published
	// definition an operator has re-authored their way back to.
	AlreadyPublished bool `json:"already_published"`
	// DeprecatedModel names a model that is registered AND deprecated. A NOTICE (task 3.8).
	DeprecatedModel string `json:"deprecated_model,omitempty"`
	// Refusals are the reasons this edit cannot be published, each naming its axis. Non-empty means
	// the publish would fail, and the console shows them BEFORE the confirmation rather than after.
	Refusals []string `json:"refusals"`
}

// AxisChange is one axis moving between two definitions.
type AxisChange struct {
	// Node names the node this change is on, or "" for the definition-level `graph` axis.
	Node string `json:"node_id,omitempty"`
	Axis string `json:"axis"`
	From string `json:"from"`
	To   string `json:"to"`
}

// ── Ports ───────────────────────────────────────────────────────────────────────────────────────

// AgentVersions is the published-definition store this service reads and writes through.
type AgentVersions interface {
	Active(ctx context.Context) (herosagent.Version, bool, error)
	List(ctx context.Context) ([]herosagent.Version, error)
	Get(ctx context.Context, configHash string) (herosagent.Version, bool, error)
}

// AgentPublisher publishes and activates. Separate from the store so this service cannot write a
// version row without going through the validation the publisher owns.
type AgentPublisher interface {
	Publish(ctx context.Context, d herosagent.Definition) (herosagent.PublishResult, error)
	Activate(ctx context.Context, configHash string) error
}

// AgentSpendSource reports per-tenant spend and the caps.
type AgentSpendSource interface {
	Spend(ctx context.Context) ([]AgentSpendRow, error)
	FleetCap(ctx context.Context) (int64, error)
	SetFleetCap(ctx context.Context, tokens int64) error
	SetTenantCap(ctx context.Context, tenantID string, tokens int64) error
	SetPlacement(ctx context.Context, tenantID, placement string) error
}

// AgentNodeHealth reports the live per-node counters (P36 tasks 6.4, 8.1).
//
// 🔴 Nil is legal and reads as UNKNOWN rather than as zero — a deployment with no observation sink has
// no numbers, and a zero would say "this node has never run", which is a different and false claim.
//
// 🔴 It returns `herosagent.NodeCounters` rather than a type of its own. A second counter struct here
// would be a second definition of the same six numbers, and the two would drift the first time one of
// them gained a field — with the symptom being a console column that is always zero.
type AgentNodeHealth interface {
	NodeHealth(nodeID string) herosagent.NodeCounters
}

func nodeCountersInto(c herosagent.NodeCounters, row AgentNodeRow) AgentNodeRow {
	row.Inferences, row.ProviderCalls = c.Inferences, c.ProviderCalls
	row.TokensIn, row.TokensOut = c.TokensIn, c.TokensOut
	row.Failures, row.Skips = c.Failures, c.Skips
	if c.ProviderCalls > 0 {
		row.LatencyMS = c.LatencyTotalMS / c.ProviderCalls
	}
	return row
}

// AgentInferenceCounter reports how many pinned inferences exist. Nil is legal and reads as unknown.
type AgentInferenceCounter interface {
	CountFor(ctx context.Context, tenantID string) (int, error)
}

// AgentKillSwitch reports and operates the platform's existing durable brake, applied to HEROS
// (task 6.8).
//
// 🔴 It is the EXISTING switch, not a second one. A subsystem with its own private halt is a subsystem
// an operator halts twice and unhalts once — and the fleet-wide brake already exists, is durable, and
// is the control an incident reaches for.
type AgentKillSwitch interface {
	Armed(ctx context.Context) (bool, string, error)
}

// ── Service ─────────────────────────────────────────────────────────────────────────────────────

// RehearseFunc runs the pinned calibration set against ONE published definition and records the
// verdict on its version row. It returns an error when the definition did not meet the floor, naming
// the fixtures that failed.
//
// # 🔴 Why this is a function handed in rather than a Rehearsal held here
//
// A rehearsal needs a live model, and WHICH model is a property of the definition being rehearsed —
// its `model_ref` and `prompt_ref`, resolved through the operator registry, called through the
// provider gateway under the deployment's own credential. None of that is knowledge this package has
// or should acquire: `adminops` owns authorisation, audit and the shape of an operator action. So the
// gate is injected as one function, and the launch path that already holds the registry, the gateway
// and the fixture root builds it.
//
// 🚫 A nil RehearseFunc is a deployment that cannot measure, NOT a deployment that activates freely:
// `Publisher.Activate` still refuses any version whose rehearsal state is not `passed`, and nothing
// else can set it. The absence closes the door rather than opening it.
type RehearseFunc func(ctx context.Context, configHash string) error

// AgentService serves the P30 operator surface.
type AgentService struct {
	exec      *Executor
	versions  AgentVersions
	publisher AgentPublisher
	spend     AgentSpendSource
	counter   AgentInferenceCounter
	kill      AgentKillSwitch
	hosts     herosagent.RunnerHosts
	rehearse  RehearseFunc
	// nodes is the live per-node counter source (P36 task 8.1). Nil reads as unknown.
	nodes AgentNodeHealth
	// prompts is the platform's OWN prompt-authoring path (see platformprompt.go). Optional for the
	// same reason `rehearse` is: a deployment with no platform database has no registry to write to,
	// and that is an absence the surface reports rather than a construction error.
	prompts PlatformPromptRegistrar
}

// WithNodeHealth wires the live per-node counters.
//
// Separate from NewAgentService, like WithRehearsal: a deployment that observes nothing is correct
// without one, and it then renders the numbers as unknown rather than as zero.
func (s *AgentService) WithNodeHealth(n AgentNodeHealth) *AgentService {
	s.nodes = n
	return s
}

// WithPlatformPrompts wires the platform prompt-authoring path.
//
// Separate from NewAgentService, like WithRehearsal: every existing caller is correct without one, and
// a required parameter would make them all pass nil to say so.
func (s *AgentService) WithPlatformPrompts(p PlatformPromptRegistrar) *AgentService {
	s.prompts = p
	return s
}

// WithRehearsal returns the service with the activation gate wired.
//
// Separate from NewAgentService because every existing caller — the demo binary, the tests, a
// deployment with no provider credential — is correct without one, and a required parameter would
// make them all pass nil to say so.
func (s *AgentService) WithRehearsal(f RehearseFunc) *AgentService {
	s.rehearse = f
	return s
}

// NewAgentService wires the surface.
//
// `counter` and `kill` may be nil — each then reads as UNKNOWN rather than as zero or as disarmed,
// which is the same discipline AxisService applies to its adoption source. `versions` may not: a
// surface that cannot read what is published has nothing honest to render.
func NewAgentService(exec *Executor, versions AgentVersions, publisher AgentPublisher,
	spend AgentSpendSource, counter AgentInferenceCounter, kill AgentKillSwitch,
	hosts herosagent.RunnerHosts) (*AgentService, error) {
	switch {
	case exec == nil:
		return nil, errors.New("adminops: the agent surface needs the command path")
	case versions == nil:
		return nil, errors.New("adminops: the agent surface needs the version store — without it it " +
			"cannot say which definition is serving, which is the one question it exists to answer")
	}
	return &AgentService{exec: exec, versions: versions, publisher: publisher, spend: spend,
		counter: counter, kill: kill, hosts: hosts}, nil
}

// Overview returns the agent picture (task 6.1).
func (s *AgentService) Overview(ctx context.Context) (AgentOverview, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAgentRead, TargetGlobal)
	if err != nil {
		return AgentOverview{}, err
	}
	if _, aerr := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: TargetGlobal, Action: adminaudit.ActionCrossTenantView,
		Reason: "analysis-agent oversight read", Result: "viewed",
		Evidence: map[string]string{"read_model": "heros_agent"}, CreatedAt: s.exec.Now(),
	}); aerr != nil {
		return AgentOverview{}, errors.New("adminops: agent read refused — it could not be logged: " + aerr.Error())
	}

	out := AgentOverview{
		Axes: []AgentAxisRow{}, Versions: []AgentVersionRow{},
		HarnessAvailability: herosagent.HarnessAvailability(s.hosts),
		MemoryAvailability:  herosagent.MemoryAvailability(s.hosts),
		// 🔴 Probed through the SAME gate the write path uses, never derived from a role name here. A
		// console that decided admin-ness itself would eventually offer a control the backend refuses,
		// which is the "offered and then refused" failure the surfaces map exists to prevent.
		CanAdmin: s.canAdmin(ctx),
	}

	versions, err := s.versions.List(ctx)
	if err != nil {
		return AgentOverview{}, fmt.Errorf("adminops: reading published definitions: %w", err)
	}
	for _, v := range versions {
		out.Versions = append(out.Versions, AgentVersionRow{
			ConfigHash: v.ConfigHash, Display: displayHash(v.ConfigHash),
			ModelRef: v.ModelRef, CredentialRef: v.CredentialRef,
			RehearsalState: string(v.RehearsalState),
			// 🔴 From the STORE's activation timestamp, never derived from recency or from
			// rehearsal_state. A `passed` definition nobody activated is not serving anything.
			Active: v.Active(), CreatedAtMS: v.CreatedAtMS,
			Nodes: len(v.Definition.Nodes),
			// Passed AND not serving. A pending version is not a state to return to, and the serving one
			// is not something to roll back to.
			RollbackTarget: v.RehearsalState == herosagent.RehearsalPassed && !v.Active(),
		})
	}

	active, hasActive, err := s.versions.Active(ctx)
	if err != nil {
		return AgentOverview{}, fmt.Errorf("adminops: reading the active definition: %w", err)
	}
	out.State, out.Sentence = agentState(hasActive, versions)
	if hasActive {
		out.Serving, out.ServingSinceMS = active.ConfigHash, active.ActivatedAtMS
		out.RehearsalState = string(active.RehearsalState)
		out.RehearsalReport = active.RehearsalReport
		out.Axes = axisRows(active.Definition, s.hosts)
	} else {
		// No active definition: the axes are reported against the NEWEST published one so an operator
		// can see what would serve, clearly labelled by State above as not serving.
		if len(versions) > 0 {
			out.RehearsalState = string(versions[0].RehearsalState)
			out.RehearsalReport = versions[0].RehearsalReport
			out.Axes = axisRows(versions[0].Definition, s.hosts)
		} else {
			out.Axes = axisRows(herosagent.Definition{}, s.hosts)
		}
	}

	// The serving definition's nodes, with their live numbers.
	shape := active.Definition
	if !hasActive && len(versions) > 0 {
		shape = versions[0].Definition
	}
	for _, n := range shape.Nodes {
		row := AgentNodeRow{NodeID: n.NodeID, PromptRef: n.PromptRef, ModelRef: n.ModelRef,
			LoopRef: n.LoopRef, HarnessRef: n.HarnessRef}
		if s.nodes != nil {
			row = nodeCountersInto(s.nodes.NodeHealth(n.NodeID), row)
			out.NodesKnown = true
		}
		out.Nodes = append(out.Nodes, row)
	}

	if s.counter != nil {
		n, cerr := s.counter.CountFor(ctx, "")
		if cerr != nil {
			return AgentOverview{}, fmt.Errorf("adminops: counting stored inferences: %w", cerr)
		}
		out.StoredInferences, out.InferencesKnown = n, true
	}
	if s.kill != nil {
		armed, note, kerr := s.kill.Armed(ctx)
		if kerr != nil {
			return AgentOverview{}, fmt.Errorf("adminops: reading the kill switch: %w", kerr)
		}
		out.KillSwitchArmed, out.KillSwitchNote = armed, note
	}
	return out, nil
}

// agentState resolves the four-valued surface state.
//
// 🔴 `pending_rehearsal` and `none_published` are DIFFERENT, and so are `rehearsal_failed` and
// `pending`. Each sends an operator somewhere else: publish something, wait for the gate, read the
// report, activate.
func agentState(hasActive bool, versions []herosagent.Version) (string, string) {
	switch {
	case hasActive:
		return "serving", "One definition is active and is answering analyses. Everything below names " +
			"it explicitly; a version shown in the list is not serving unless it says so."
	case len(versions) == 0:
		return "none_published", "No definition has been published. HEROS is not configured on this " +
			"deployment, which is different from being disabled for a tenant — nothing exists to enable."
	case versions[0].RehearsalState == herosagent.RehearsalFailed:
		return "rehearsal_failed", rehearsalFailureSentence(versions[0].RehearsalReport)
	default:
		return "pending_rehearsal", "A definition is published and has NOT been measured. It is not " +
			"active and will not become active until it meets the floor on every fixture individually. " +
			"Nothing is serving inference."
	}
}

// rehearsalFailureSentence separates the two unrelated facts `RehearsalFailed` carries.
//
// 🔴 One state, two situations with different causes and different next actions:
//
//   - The gate MEASURED the definition and it scored below the floor. The model is the problem, and the
//     per-fixture numbers say where.
//   - The run never reached the model at all. Nothing was measured, there are no numbers, and the
//     problem is somewhere between this deployment and the provider.
//
// This said the FIRST for both. Found in production on the first real activation: the provider account
// had no credits, every attempt came back `429 insufficient_quota`, and this page reported a definition
// that "ran against the pinned fixtures and did not meet the floor on every one" whose report "names
// which failed and by how much". It ran against nothing and named nothing. An operator reading that
// goes looking for per-fixture scores that do not exist, and concludes the model is bad when the real
// answer is a billing page.
//
// That is this file's own rule turned on itself: `unpriced` must not render as `0` because an absence
// is not a measurement — and neither is this.
//
// # Why the REPORT is the discriminator, and not a new state
//
// `GateActivation` already writes two different documents: `{"error": …}` when the run itself failed,
// and the serialised `RehearsalReport` when it produced numbers. The distinction is therefore already
// recorded, and reading it here needs no new state on the wire, no schema change, and no second thing
// to keep in sync. `rehearsal_failed` stays the closed-set value the console switches on — the rehearsal
// did fail either way, and it is only the CLAIM ABOUT WHAT WAS MEASURED that was wrong.
func rehearsalFailureSentence(report string) string {
	const nothingServing = " Nothing is serving inference."

	var probe struct {
		Error string `json:"error"`
	}
	trimmed := strings.TrimSpace(report)
	switch {
	case trimmed == "":
		// 🚫 Neither claim, because neither is available. A missing report cannot be read as "it scored
		// badly" — that is the assumption this function exists to stop making.
		return "The newest definition did not pass its rehearsal, and no report was stored for it. " +
			"Whether it was measured and scored low, or never reached the model at all, cannot be " +
			"told from here." + nothingServing

	case json.Unmarshal([]byte(trimmed), &probe) != nil:
		// Same reasoning. An unreadable report is an unknown, not a bad score.
		return "The newest definition did not pass its rehearsal, and its stored report could not be " +
			"read. It is shown below verbatim so it can be inspected; until it can be parsed, whether " +
			"anything was measured is unknown." + nothingServing

	case probe.Error != "":
		// The error text itself is NOT inlined here. The console renders the full report below this
		// sentence already, and a 429 body pasted mid-paragraph makes the one line an operator reads
		// first unreadable.
		return "The newest definition was NOT measured. Its rehearsal could not complete, so there are " +
			"no per-fixture scores and nothing here is a judgement about the definition — the report " +
			"below is the failure that stopped the run, and it names the cause." + nothingServing

	default:
		return "The newest definition ran against the pinned fixtures and did not meet the floor on " +
			"every one. The per-fixture report below names which failed and by how much." + nothingServing
	}
}

// axisRows renders every axis of every node with the three-valued status task 6.10 requires.
//
// 🔴 `not_in_effect` ALWAYS carries a reason. An axis that is inert for an unstated reason is a
// configuration an operator cannot act on — and the ways to be inert are quite different: a topology
// on a definition with nothing to order, and a memory or harness strategy whose host service this
// runner does not supply.
//
// 🔴 EIGHT PER-NODE AXES AND ONE DEFINITION-LEVEL ONE (P36 task 6.2). Collapse, do not omit: every
// node contributes its eight rows, and `graph` is emitted exactly once because topology is a property
// BETWEEN nodes. A console that rendered only the first node's axes would be showing a configuration
// that is not the one running, and nothing on the page would say so.
func axisRows(d herosagent.Definition, hosts herosagent.RunnerHosts) []AgentAxisRow {
	out := []AgentAxisRow{}
	nodes := d.Nodes
	if len(nodes) == 0 {
		// Nothing published yet. One node's worth of defaulted rows, so the editor has the same shape
		// before and after the first publish rather than appearing from nowhere.
		nodes = []herosagent.Node{{NodeID: herosagent.DefaultNodeID}}
	}
	for _, n := range nodes {
		row := func(axis herosagent.Axis, value, dflt string) AgentAxisRow {
			if strings.TrimSpace(value) == "" {
				return AgentAxisRow{NodeID: n.NodeID, Axis: string(axis), Status: AxisDefaulted,
					Value: dflt, Editable: true}
			}
			return AgentAxisRow{NodeID: n.NodeID, Axis: string(axis), Status: AxisSet,
				Value: value, Editable: true}
		}
		out = append(out,
			row(herosagent.AxisPrompt, n.PromptRef, "none — the agent has no instruction"),
			row(herosagent.AxisModel, n.ModelRef, "none — no model is bound"),
			row(herosagent.AxisSkills, strings.Join(n.SkillRefs, ", "), "none bound"),
			row(herosagent.AxisTools, strings.Join(n.ToolNames, ", "), "none bound"),
			row(herosagent.AxisContext, n.ContextRef, "none — no context policy is bound"),
			row(herosagent.AxisMemory, n.MemoryRef, "none"),
			row(herosagent.AxisHarness, n.HarnessRef, "single-shot"),
			row(herosagent.AxisLoop, n.LoopRef, "none — this node runs one turn"),
		)
	}
	out = append(out, graphAxisRow(d))
	return out
}

// graphAxisRow is the definition-level topology axis.
//
// 🔴 It is SHOWN in both states, and the single-node reason text is NOT removed merely because
// multi-node definitions now exist (P36 task 6.3, D3). Hiding it would make it indistinguishable from
// an axis that does not exist, and an operator would keep looking for it; deleting the reason would
// discard a correct explanation for what is still the default shape.
func graphAxisRow(d herosagent.Definition) AgentAxisRow {
	if !d.MultiNode() {
		return AgentAxisRow{
			Axis: string(herosagent.AxisGraph), Status: AxisNotInEffect, Value: "fixed",
			Reason: "This definition declares one node, so there is no ordering to author. A topology " +
				"would hash a configuration nothing can execute, and is refused at publish rather than " +
				"accepted and ignored. Declare a second node to author one.",
			Editable: false,
		}
	}
	return AgentAxisRow{
		Axis: string(herosagent.AxisGraph), Status: AxisSet, Value: topologySummary(d), Editable: true,
	}
}

// topologySummary is the one-line rendering of a definition's graph.
func topologySummary(d herosagent.Definition) string {
	parts := []string{strings.Join(d.Ordering(), " → ")}
	if n := len(d.Edges); n > 0 {
		parts = append(parts, fmt.Sprintf("%d edge(s)", n))
	}
	for _, g := range d.GraphGroups {
		what := "group"
		if g.Concurrent {
			what = "concurrent group"
		}
		if g.Merge != nil {
			parts = append(parts, fmt.Sprintf("%s [%s] → %s merged %s on %s", what,
				strings.Join(g.Nodes, ", "), g.Merge.Into, g.Merge.Strategy, g.Merge.OnNodeFailure))
			continue
		}
		parts = append(parts, fmt.Sprintf("%s [%s]", what, strings.Join(g.Nodes, ", ")))
	}
	return strings.Join(parts, "; ")
}

// Spend returns the per-tenant meter (task 6.5).
func (s *AgentService) Spend(ctx context.Context) (AgentSpendView, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAgentRead, TargetGlobal)
	if err != nil {
		return AgentSpendView{}, err
	}
	if _, aerr := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: TargetGlobal, Action: adminaudit.ActionCrossTenantView,
		Reason: "analysis-agent spend read", Result: "viewed",
		Evidence: map[string]string{"read_model": "heros_spend"}, CreatedAt: s.exec.Now(),
	}); aerr != nil {
		return AgentSpendView{}, errors.New("adminops: agent spend read refused — it could not be logged: " + aerr.Error())
	}

	// 🔴 Estimated is TRUE unconditionally. Every number here is a token count multiplied through a
	// price REFERENCE; it is not an invoice, and the label is on the wire so a renderer cannot decide
	// to leave it off.
	out := AgentSpendView{
		Estimated: true, Rows: []AgentSpendRow{}, CanAdmin: s.canAdmin(ctx),
		Placements: placementNames(),
	}
	if s.spend == nil {
		return out, nil
	}
	rows, err := s.spend.Spend(ctx)
	if err != nil {
		return AgentSpendView{}, fmt.Errorf("adminops: reading agent spend: %w", err)
	}
	for _, r := range rows {
		// 🔴 A tenant that has run NOTHING is not "unpriced" — nothing ran, so there is nothing to
		// price, and counting it here would inflate the "the total below is incomplete" warning with
		// rows that contribute no missing cost at all. Found by reading the rendered page: `initech`
		// had zero inferences and was being reported as a pricing gap.
		if !r.Priced && r.Inferences > 0 {
			out.UnpricedTenants++
		}
		if !r.Priced {
			// 🔴 An unpriced row carries NO cost. The store's CHECK refuses one too; this is the second
			// layer, because a row that arrived from anywhere else must not render a number either.
			r.EstimatedCost = 0
		}
		out.Rows = append(out.Rows, r)
	}
	sort.SliceStable(out.Rows, func(i, j int) bool { return out.Rows[i].TenantID < out.Rows[j].TenantID })
	cap, err := s.spend.FleetCap(ctx)
	if err != nil {
		return AgentSpendView{}, fmt.Errorf("adminops: reading the fleet cap: %w", err)
	}
	out.FleetCap = cap
	return out, nil
}

// Preview resolves an edit WITHOUT publishing it (task 6.2).
//
// 🔴 It is a read: it makes no version, it writes no row, and it is what the confirmation renders.
// "Confirmed before it happens" is only true if there is something to look at before it happens.
func (s *AgentService) Preview(ctx context.Context, d herosagent.Definition) (PublishPreview, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAgentAdmin, TargetGlobal)
	if err != nil {
		return PublishPreview{}, err
	}
	_ = sess

	out := PublishPreview{Changes: []AxisChange{}, Refusals: []string{}}
	if verr := d.Validate(); verr != nil {
		out.Refusals = append(out.Refusals, verr.Error())
	}
	hash, herr := d.ConfigHash()
	if herr != nil {
		return PublishPreview{}, herr
	}
	out.ConfigHash, out.Display = hash, displayHash(hash)

	active, hasActive, err := s.versions.Active(ctx)
	if err != nil {
		return PublishPreview{}, fmt.Errorf("adminops: reading the active definition: %w", err)
	}
	if hasActive {
		out.Changes = diffDefinitions(active.Definition, d)
		out.NoChange = active.ConfigHash == hash
	}
	if _, exists, gerr := s.versions.Get(ctx, hash); gerr != nil {
		return PublishPreview{}, fmt.Errorf("adminops: reading version %s: %w", hash, gerr)
	} else if exists {
		out.AlreadyPublished = true
		out.NoChange = true
	}
	return out, nil
}

// diffDefinitions is the resolved, axis-by-axis diff the confirmation renders.
//
// 🔴 Keyed by NODE as well as axis (P36). A diff that compared two definitions axis-by-axis without
// naming the node would report "model: A → B" for a change on one node of five, and an operator would
// approve a change to the wrong call site believing they had read it.
//
// A node that appears on one side only is rendered as an ADDED or REMOVED node rather than as eight
// axis changes from `none`: adding a node is one act with one consequence, and spelling it as eight
// unrelated rows buries the only fact that matters.
func diffDefinitions(from, to herosagent.Definition) []AxisChange {
	out := []AxisChange{}
	fromIDs, toIDs := nodeIDsOf(from), nodeIDsOf(to)
	for _, id := range union(fromIDs, toIDs) {
		fn, inFrom := from.NodeByID(id)
		tn, inTo := to.NodeByID(id)
		switch {
		case !inFrom:
			out = append(out, AxisChange{Node: id, Axis: "node", From: "absent", To: "declared"})
			continue
		case !inTo:
			out = append(out, AxisChange{Node: id, Axis: "node", From: "declared", To: "absent"})
			continue
		}
		add := func(axis herosagent.Axis, a, b string) {
			if a != b {
				out = append(out, AxisChange{Node: id, Axis: string(axis), From: orNone(a), To: orNone(b)})
			}
		}
		add(herosagent.AxisPrompt, fn.PromptRef, tn.PromptRef)
		add(herosagent.AxisModel, fn.ModelRef, tn.ModelRef)
		add(herosagent.AxisSkills, strings.Join(fn.SkillRefs, ", "), strings.Join(tn.SkillRefs, ", "))
		add(herosagent.AxisTools, strings.Join(sortedCopy(fn.ToolNames), ", "),
			strings.Join(sortedCopy(tn.ToolNames), ", "))
		add(herosagent.AxisContext, fn.ContextRef, tn.ContextRef)
		add(herosagent.AxisMemory, fn.MemoryRef, tn.MemoryRef)
		add(herosagent.AxisHarness, fn.HarnessRef, tn.HarnessRef)
		add(herosagent.AxisLoop, fn.LoopRef, tn.LoopRef)
		// The credential is an axis-adjacent identity fact and a change to it changes the agent. It is
		// shown as a change so an operator cannot repoint the credential without seeing that they did.
		add("credential", fn.CredentialRef, tn.CredentialRef)
	}
	// The definition-level topology, once.
	if a, b := topologySummary(from), topologySummary(to); a != b {
		out = append(out, AxisChange{Axis: string(herosagent.AxisGraph), From: orNone(a), To: orNone(b)})
	}
	return out
}

func nodeIDsOf(d herosagent.Definition) []string {
	out := make([]string, 0, len(d.Nodes))
	for _, n := range d.Nodes {
		out = append(out, n.NodeID)
	}
	return out
}

// union is the sorted set of node ids across both sides of a diff.
func union(a, b []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, xs := range [][]string{a, b} {
		for _, x := range xs {
			if !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	sort.Strings(out)
	return out
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "none"
	}
	return s
}

// Publish records a new version after the operator confirmed the preview.
func (s *AgentService) Publish(ctx context.Context, d herosagent.Definition, reason string) (PublishPreview, error) {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAgentAdmin, TargetGlobal)
	if err != nil {
		return PublishPreview{}, err
	}
	if s.publisher == nil {
		return PublishPreview{}, errors.New("adminops: this deployment mounts no agent publisher")
	}
	if strings.TrimSpace(reason) == "" {
		return PublishPreview{}, errors.New("adminops: publishing an agent definition requires a reason — " +
			"it changes what the platform infers about every customer's source")
	}

	res, err := s.publisher.Publish(ctx, d)
	if err != nil {
		return PublishPreview{}, err
	}
	out := PublishPreview{
		ConfigHash: res.ConfigHash, Display: displayHash(res.ConfigHash),
		NoChange: !res.Created, DeprecatedModel: res.DeprecatedModel,
		Changes: []AxisChange{}, Refusals: []string{},
	}
	result := "published"
	if !res.Created {
		// 🔴 Task 6.2's second half: an edit resolving to no change SAYS SO and creates no version.
		result = "no_change"
	}
	if _, aerr := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: TargetGlobal, Action: adminaudit.ActionCrossTenantView,
		Reason: reason, Result: result,
		Evidence: map[string]string{"config_hash": res.ConfigHash,
			// The DISTINCT set of models this definition binds, not the first node's. An audit row naming
			// one model for a five-node graph records a change nobody made.
			"model_ref": herosagent.DenormalisedModelRef(d), "nodes": strconv.Itoa(len(d.Nodes))},
		CreatedAt: s.exec.Now(),
	}); aerr != nil {
		return out, errors.New("adminops: the definition was published and the action could not be logged: " + aerr.Error())
	}
	return out, nil
}

// Rollback returns a PREVIOUSLY SERVING definition to service (P36 task 5.5).
//
// 🔴 It is a separate ACTION from Activate rather than the same one with a different label, and the
// reason is the audit trail. "An operator activated version X" and "an operator rolled back to version
// X" are the same database write and two different events, and the second is the one somebody
// reconstructs an incident from. A single action would make a rollback indistinguishable from a routine
// activation in the record — at exactly the moment the record matters most.
//
// 🚫 It takes no definition. Rollback activates a version that already exists; re-authoring the older
// shape means retyping a configuration under pressure, and any transcription error produces a different
// `config_hash` — a third configuration nobody has measured, activated in place of the one known to
// work.
func (s *AgentService) Rollback(ctx context.Context, configHash, reason string) error {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAgentAdmin, TargetGlobal)
	if err != nil {
		return err
	}
	if s.publisher == nil {
		return errors.New("adminops: this deployment mounts no agent publisher")
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("adminops: rolling back an agent definition requires a reason — it changes " +
			"what the platform infers about every customer's source, and a rollback with no stated cause " +
			"is the hardest kind of change to reconstruct afterwards")
	}
	roller, ok := s.publisher.(interface {
		Rollback(ctx context.Context, configHash string) error
	})
	if !ok {
		// 🔴 Named rather than silently falling back to Activate. The two are the same write and
		// different events; a fallback would record a rollback as a routine activation.
		return errors.New("adminops: this deployment's publisher cannot roll back")
	}
	if err := roller.Rollback(ctx, configHash); err != nil {
		return err
	}
	if _, aerr := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: TargetGlobal, Action: adminaudit.ActionCrossTenantView,
		Reason: reason, Result: "rolled_back",
		Evidence:  map[string]string{"config_hash": configHash, "act": "rollback"},
		CreatedAt: s.exec.Now(),
	}); aerr != nil {
		return errors.New("adminops: the rollback succeeded and could not be logged: " + aerr.Error())
	}
	return nil
}

// Activate makes a rehearsed definition the one serving inference.
func (s *AgentService) Activate(ctx context.Context, configHash, reason string) error {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAgentAdmin, TargetGlobal)
	if err != nil {
		return err
	}
	if s.publisher == nil {
		return errors.New("adminops: this deployment mounts no agent publisher")
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("adminops: activating an agent definition requires a reason")
	}

	// 🔴 THE GATE RUNS HERE, and it is the only place it can run in the product (D7).
	//
	// Before this, `NewRehearsal` was constructed in exactly one file in the repository and that file
	// was `cmd/proof/acceptance` — a proof binary. So the gate existed, had fences that went red, and
	// no deployed path could reach it: a published definition stayed `pending` for ever and Activate
	// refused it for ever. The refusal was correct and the capability was dark.
	//
	// ⚠️ IT SPENDS. One provider call per calibration fixture, on the deployment's own credential, at
	// the moment an operator presses activate. That is deliberate rather than hidden behind a schedule:
	// the operator who decides to activate is the one who should see the bill for measuring it.
	//
	// Skipped when the definition already PASSED — re-measuring an unchanged `config_hash` spends
	// tokens to reproduce a number that is already on the row, and D2 is explicit that the difference
	// between two runs of one hash is noise rather than signal.
	if s.rehearse != nil {
		v, ok, verr := s.versions.Get(ctx, configHash)
		if verr != nil {
			return verr
		}
		if !ok {
			return fmt.Errorf("adminops: no definition is published under %s", displayHash(configHash))
		}
		if v.RehearsalState != herosagent.RehearsalPassed {
			if rerr := s.rehearse(ctx, configHash); rerr != nil {
				// The report is already stored on the row by the gate itself, pass or fail, so an
				// operator reading the console sees the per-fixture numbers behind this refusal.
				return rerr
			}
		}
	}

	if err := s.publisher.Activate(ctx, configHash); err != nil {
		return err
	}
	_, aerr := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: TargetGlobal, Action: adminaudit.ActionCrossTenantView,
		Reason: reason, Result: "activated",
		Evidence: map[string]string{"config_hash": configHash}, CreatedAt: s.exec.Now(),
	})
	return aerr
}

// SetPlacement sets one tenant's placement (task 6.7, 9.3).
//
// 🔴 Setting it to `platform` is what makes the platform read that tenant's source under a
// platform-held credential. It requires a reason and is audited, because Q2 made the default
// `disabled` precisely so this is a deliberate act.
func (s *AgentService) SetPlacement(ctx context.Context, tenantID, placement, reason string) error {
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAgentAdmin, TenantTarget(tenantID))
	if err != nil {
		return err
	}
	// 🔴 ONE vocabulary, parsed by the package that owns it. This was a local `switch` over three string
	// literals — a second copy of a closed set, in a different package from the gate that branches on it,
	// and the failure mode is the quiet one: `herosagent` gains a fourth placement, this switch keeps
	// accepting exactly three, and an operator setting the new one is told it is not a placement.
	if _, err := herosagent.ParsePlacement(placement); err != nil {
		return err
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("adminops: setting a placement requires a reason — `platform` makes this " +
			"platform read that tenant's source under a platform-held credential")
	}
	if s.spend == nil {
		return errors.New("adminops: this deployment mounts no placement store")
	}
	if err := s.spend.SetPlacement(ctx, tenantID, placement); err != nil {
		return err
	}
	_, aerr := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: TenantTarget(tenantID),
		Action: adminaudit.ActionCrossTenantView, Reason: reason, Result: "placement_" + placement,
		Evidence: map[string]string{"placement": placement}, CreatedAt: s.exec.Now(),
	})
	return aerr
}

// SetCap edits a cap. An empty tenantID sets the FLEET cap (task 6.6).
func (s *AgentService) SetCap(ctx context.Context, tenantID string, tokens int64, reason string) error {
	target := TargetGlobal
	if tenantID != "" {
		target = TenantTarget(tenantID)
	}
	sess, _, err := s.exec.Authorize(ctx, adminrbac.CapAgentAdmin, target)
	if err != nil {
		return err
	}
	if tokens < 0 {
		return errors.New("adminops: a cap cannot be negative")
	}
	if strings.TrimSpace(reason) == "" {
		return errors.New("adminops: editing a cap requires a reason")
	}
	if s.spend == nil {
		return errors.New("adminops: this deployment mounts no cap store")
	}
	if tenantID == "" {
		err = s.spend.SetFleetCap(ctx, tokens)
	} else {
		err = s.spend.SetTenantCap(ctx, tenantID, tokens)
	}
	if err != nil {
		return err
	}
	_, aerr := s.exec.Audit().Append(adminaudit.Entry{
		ActorAdminID: sess.AdminID, Target: target, Action: adminaudit.ActionCrossTenantView,
		Reason: reason, Result: "cap_set",
		Evidence: map[string]string{"tokens": fmt.Sprint(tokens)}, CreatedAt: s.exec.Now(),
	})
	return aerr
}

// canAdmin reports whether the acting session may publish, activate, cap or place.
//
// It ASKS THE GATE rather than inspecting a role, so the console can never offer a control the backend
// would refuse. A refused probe is not an error here — it is the answer.
func (s *AgentService) canAdmin(ctx context.Context) bool {
	_, _, err := s.exec.Authorize(ctx, adminrbac.CapAgentAdmin, TargetGlobal)
	return err == nil
}

// placementNames renders the closed set for the wire, in the order the owning package declares it.
//
// The ORDER is carried through rather than sorted: `Placements` lists the two placements that run
// something before the one that runs nothing, and an alphabetical sort would put `customer, disabled,
// platform` on the editor — burying the default in the middle of the list for no reason but the
// alphabet.
func placementNames() []string {
	ps := herosagent.Placements()
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, string(p))
	}
	return out
}

// displayHash shortens a config hash for a screen. Long enough to identify, short enough to read.
func displayHash(h string) string {
	if len(h) <= 12 {
		return h
	}
	return h[:12]
}
