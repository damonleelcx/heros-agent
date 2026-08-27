package herosagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/patternclassifier"
	"github.com/heros-foreal/agentd/internal/providercall"
)

// runner.go performs one inference (tasks 4.1–4.10).

// Input is the residue and nothing else.
//
// 🔴 A CALLER CANNOT ASK FOR A WHOLE-REPOSITORY PASS: there is no field for it. NFR1 stays true by
// construction rather than by review — see residue.go.
type Input struct {
	TenantID       string
	WorkflowID     string
	SourceRevision string
	// RuleIR is what the frontends already established. It is the VOCABULARY the output is validated
	// against (node ids must already be in it) and the SOURCE of D3's first fence.
	RuleIR *discovery.IR
	// Residue is the gap. Nothing outside it is offered to the agent.
	Residue Residue
	// Budget bounds this run. Exceeding it aborts, records the abort, and writes NO partial IR.
	Budget Budget
	// AgentConfigHash is WHICH DEFINITION is running. Set by [Runner.Infer] from its own argument;
	// callers constructing an Input do not fill it.
	//
	// 🔴 It exists for the idempotency key, and its absence was a real defect. `GatewayModel` built that
	// key as `defaultInferenceID(workflowID, sourceRevision, "")` under a comment reading "the three-part
	// key IS the idempotency key" — the third part was empty, so two DIFFERENT definitions analysing the
	// same workflow at the same revision sent the SAME `Idempotency-Key` header to the provider. A
	// provider that honours the header answers the second from the first, and the gate then scores
	// definition B on definition A's answers while looking entirely healthy.
	//
	// That is the worst shape this can take: the activation gate exists to tell definitions apart, and
	// this made it unable to. It was found on the rehearsal, where WorkflowID is a fixture name and
	// SourceRevision is the constant "fixture" — so every definition ever measured collided on all nine.
	//
	// 🚫 NOT part of the model's input wire. `AssembleModelInput` does not read it, and must not: the
	// bytes the model sees are what `config_hash` is computed over, so feeding the hash into them would
	// make a definition's identity depend on itself.
	AgentConfigHash string
}

// Budget is the per-run ceiling (task 4.9).
//
// 🔴 Both halves are required, and a zero is not "unlimited": Validate refuses one. An unbounded run is
// how a repository-shaped cost arrives on a bill nobody approved, and "unlimited" spelled as the zero
// value is the version of that which nobody chose.
type Budget struct {
	MaxTokens int
	MaxWall   time.Duration
}

// Validate refuses an unbounded budget.
func (b Budget) Validate() error {
	switch {
	case b.MaxTokens <= 0:
		return fmt.Errorf("%w: a run needs a token ceiling — a zero here would read as `unlimited`, "+
			"which is a cost nobody chose", ErrInvalidDefinition)
	case b.MaxWall <= 0:
		return fmt.Errorf("%w: a run needs a wall-clock ceiling", ErrInvalidDefinition)
	}
	return nil
}

// ProvenancedEdge is an edge HEROS wrote, carrying everything D4 requires of a `heros` fact.
type ProvenancedEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Kind is validated against the closed set {data, control}. 🚫 Rejected, never repaired.
	Kind       string  `json:"kind"`
	Confidence float64 `json:"confidence"`
	// ProducedByNode names the node whose call produced this edge (P36 task 3.8, spec "an operator can
	// resolve a customer-visible finding to that node").
	//
	// 🔴 Fact-level, and that is the granularity the requirement asks for. A per-INFERENCE attribution
	// answers "which definition", which `agent_config_hash` already answered; the question a graph
	// creates is which of five nodes wrote the edge somebody is disputing.
	//
	// `omitempty`: an edge stored before P36 carries none and decodes unchanged.
	ProducedByNode string `json:"produced_by_node,omitempty"`
}

// Abstention is a subject the agent declined. FR3.4: not knowing is an OUTPUT.
type Abstention struct {
	Subject string           `json:"subject"`
	Reason  AbstentionReason `json:"reason"`
	// Confidence is the value that fell below the floor, when there was one. A pointer because a
	// decline with NO candidate is a different thing from one at 0.0, and a float cannot say which.
	Confidence *float64 `json:"confidence,omitempty"`
}

// Result is one inference: what was produced, and what was declined. BOTH are stored.
type Result struct {
	InferenceID string
	Code        Code
	Edges       []ProvenancedEdge
	// Labels are patternclassifier.RegionProposal — the EXISTING type (task 4.6), so HEROS's proposals
	// enter through the same partitioner and the same precedence rule as every detector's.
	// 🚫 There is no second arbitration path, which is what keeps "an LLM label never overrides a rule
	// label" true by construction rather than by a second implementation of the same rule.
	Labels      []patternclassifier.RegionProposal
	Abstentions []Abstention
	// Narrative is ASSESSED, never measured. Rendered visually distinct, and absent rather than
	// fabricated when the agent produced none.
	Narrative string
	Usage     providercall.Usage
	// ProviderCalls is the COUNT, surfaced so a test can assert zero rather than "no error" (task 10.3).
	ProviderCalls int
	// Cause carries a failure's reason. 🚫 A failed inference is `analysis failed` WITH THIS — never an
	// empty graph, which reads as a finding about the customer's workflow (task 4.10).
	Cause string
	// Nodes is what each node of the producing definition did (P36 task 3.8).
	//
	// 🔴 On the RESULT and not only on the stored row, because the rehearsal reads it: a gate that had
	// to re-read the store to find out which node contributed would be measuring a row it just wrote
	// rather than the run it just performed.
	Nodes []NodeRun
}

// RawEdge and RawLabel are what the MODEL returns, BEFORE validation — the pattern as a plain string,
// the node ids unchecked. Representable so an out-of-vocabulary answer can be explicitly REJECTED
// rather than failing to parse into something that looks legitimate (the shape patternclassifier's
// RawLabel already uses, for the same reason).
type RawEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	// Pointer: a MISSING confidence is distinguishable from 0.0. A model that answers without one has
	// not met the contract and must not be read as "confidently zero".
	Confidence *float64 `json:"confidence"`
}

type RawLabel struct {
	Pattern    string   `json:"pattern"`
	NodeIDs    []string `json:"node_ids"`
	Confidence *float64 `json:"confidence"`
}

// RawResult is one model answer.
type RawResult struct {
	Edges     []RawEdge  `json:"edges"`
	Labels    []RawLabel `json:"labels"`
	Narrative string     `json:"narrative"`
}

// Model is the seam the agent's provider call is made through.
//
// 🔴 An INTERFACE with no default implementation, exactly as `patternclassifier.FallbackModel` is and
// for the same reason: "a stub wired in by default is how a classifier comes to look like it is working
// while classifying nothing, and the resulting labels would be indistinguishable from real ones."
//
// The only implementation that reaches a provider goes through `providergateway` (task 4.2), and a
// static fence asserts this package constructs no `http.Client` of its own.
type Model interface {
	Infer(ctx context.Context, in Input) (RawResult, providercall.Usage, error)
}

// InferenceStore is the pinned-result store (D2).
type InferenceStore interface {
	// Get reads a stored inference by its three-part key. ok=false is NOT INFERRED — never an error.
	Get(ctx context.Context, workflowID, sourceRevision, agentConfigHash string) (Stored, bool, error)
	// Put records one. It must be idempotent on the key: the UNIQUE index is the fence, and a writer
	// that raced must resolve to the row that won rather than failing the caller.
	Put(ctx context.Context, s Stored) error
	// Replace overwrites a stored inference AFTER a confirmed re-inference (task 4.8).
	//
	// 🔴 A SEPARATE METHOD from Put, and that is the whole of "replacing only on confirmation": Put is
	// idempotent and refuses to overwrite, so no ordinary inference path can replace a stored answer
	// by accident. Replacing needs a caller that meant to, holding a diff somebody looked at.
	Replace(ctx context.Context, s Stored) error
}

// Stored is one pinned inference.
type Stored struct {
	InferenceID     string
	TenantID        string
	WorkflowID      string
	SourceRevision  string
	AgentConfigHash string
	// Placement is which host produced this — the value the graph attributes (task 8.6). Typed, so a
	// reader of a stored row cannot find a fourth word here.
	Placement   Placement
	Edges       []ProvenancedEdge
	Labels      []patternclassifier.RegionProposal
	Abstentions []Abstention
	Narrative   string
	TokensIn    int
	TokensOut   int
	CreatedAtMS int64
	// StaleReason marks an inference nothing is maintaining any more (task 9.5). EMPTY means NOT
	// STALE. 🚫 The facts are kept and still attributed — see stale.go for why retention beats deletion.
	StaleReason StaleReason
	StaleAtMS   int64
	// Nodes is what each node of the producing definition did (P36 task 3.8).
	//
	// 🔴 Present for a SINGLE-node definition too, carrying one entry. An attribution that appeared
	// only for graphs would make "which node produced this" a question with two shapes of answer, and
	// the console would need a branch to ask it — which is where a surface starts rendering one shape
	// and silently omitting the other.
	Nodes []NodeRun
}

// NodeRun is one node's contribution to one inference: what it produced, what it spent, how long it
// took, and whether it failed.
//
// # 🔴 Why per-node and not an aggregate
//
// An aggregate over a graph says the agent is slow. It does not say WHICH NODE is slow, and that is
// the only form of the answer anybody can act on (task 8.1). The same argument applies to spend: a
// definition whose bill doubled did so at one node, and an aggregate hides which.
//
// 🚫 No field here can hold a credential. The node is named by ID; what it spent is a token count.
type NodeRun struct {
	NodeID string `json:"node_id"`
	// ProviderCalls is the COUNT, so a test can assert zero rather than "no error".
	ProviderCalls int `json:"provider_calls"`
	TokensIn      int `json:"tokens_in"`
	TokensOut     int `json:"tokens_out"`
	// LatencyMS is wall time for this node's call. Measured from an injected clock, like everything
	// else in this package, so a test asserts on elapsed milliseconds rather than on when it ran.
	LatencyMS int64 `json:"latency_ms"`
	// Edges, Labels and Abstentions are what this node CONTRIBUTED, before the merge.
	Edges       int `json:"edges"`
	Labels      int `json:"labels"`
	Abstentions int `json:"abstentions"`
	// Failed and Cause: a node that did not complete. 🚫 Never an empty contribution silently — a node
	// that returned nothing because the provider refused is a different fact from a node that found
	// nothing, and only one of them is a finding about the customer's workflow.
	Failed bool   `json:"failed"`
	Cause  string `json:"cause,omitempty"`
	// Skipped is a node the topology did not enter — a predicate edge that did not hold. It made no
	// call and produced nothing, which is not a failure and must not read as one.
	Skipped bool `json:"skipped"`
	// SkipReason names the predicate that routed around it.
	SkipReason string `json:"skip_reason,omitempty"`
}

// Runner performs one inference. It is the ONLY thing in the package that reaches a provider.
type Runner struct {
	model Model
	store InferenceStore
	// host is WHICH RUNNER this is. Set at construction and never afterwards, because the alternative —
	// a host passed per call — puts the answer to "am I allowed to run this" in the hands of the caller
	// who wants to run it (task 7.5).
	host Host
	// Floor is the confidence below which an output becomes a stored ABSTENTION rather than a fact
	// (task 4.4). Required: a zero floor accepts everything, and a floor nobody set is a floor that is
	// zero.
	floor float64
	nowMS func() int64
	newID func(workflowID, sourceRevision, configHash string) string
	// caps is the per-tenant and fleet ceiling, checked BEFORE the provider call (task 9.2). Nil means
	// NO CEILING IS ENFORCED — an honest and dangerous state that `Readiness` reports rather than
	// hides, because a deployment whose caps are unwired looks identical to one whose caps are simply
	// generous.
	caps *CapChecker
	// meter records what a run spent, so the next check has something to read. A cap with no meter is
	// a number nobody is under, which is why `NewCapChecker` refuses that combination — this field is
	// set from the same wiring.
	meter SpendReader
	// emit receives events. Nil is legal and drops them: telemetry is not a precondition for analysis,
	// and a runner that refused to start without an event sink would make an observability dependency
	// into an availability one.
	emit func(Event, map[string]any)
	// nodeModel resolves the Model for ONE node of a graph — each node binds its own prompt, model and
	// credential (decisions.md D-36.1), so one Model cannot serve them all.
	//
	// 🔴 Nil is legal for a SINGLE-node definition and FATAL for a graph. A runner that fell back to
	// `model` for every node would run five nodes against one model while the `config_hash` named five
	// different ones — a configuration nobody authored, reported under the identity of one somebody
	// did. `Infer` refuses a multi-node binding when this is nil.
	nodeModel func(Node) (Model, error)
	// health receives per-node observations. Nil drops them, on the same terms as `emit`.
	health func(NodeRun)
}

// WithNodeModels wires per-node model resolution, which a multi-node definition requires.
//
// 🔴 An OPTION rather than a constructor argument, because every single-node deployment is correct
// without one and a required parameter would make them all pass nil to say so. The asymmetry is made
// safe by the refusal rather than by the caller's care: `Infer` refuses a graph when this is unset.
func WithNodeModels(resolve func(Node) (Model, error)) RunnerOption {
	return func(r *Runner) { r.nodeModel = resolve }
}

// WithNodeHealth wires the per-node observation sink read by the health endpoint (task 8.1).
func WithNodeHealth(observe func(NodeRun)) RunnerOption {
	return func(r *Runner) { r.health = observe }
}

// RunnerOption configures a Runner.
type RunnerOption func(*Runner)

// WithCaps enforces per-tenant and fleet token ceilings before every provider call (task 9.2).
//
// 🔴 An OPTION rather than a required argument, and the asymmetry is deliberate. The customer-side
// runner spends the customer's own credential, so a platform ceiling on it would be this platform
// limiting somebody else's bill — which is not ours to limit. The platform-side runner should always
// have one, and `Readiness` reports when it does not rather than this constructor guessing.
func WithCaps(c *CapChecker, meter SpendReader) RunnerOption {
	return func(r *Runner) { r.caps, r.meter = c, meter }
}

// WithEvents wires a telemetry sink.
func WithEvents(emit func(Event, map[string]any)) RunnerOption {
	return func(r *Runner) { r.emit = emit }
}

// NewRunner wires the PLATFORM-side runner (task 7.5). See NewCustomerRunner for the other host.
func NewRunner(m Model, store InferenceStore, floor float64, nowMS func() int64, opts ...RunnerOption) (*Runner, error) {
	return newRunner(HostPlatform, m, store, floor, nowMS, opts...)
}

// NewCustomerRunner wires the runner that executes on the CUSTOMER's machine, under the customer's own
// credential (task 7.1).
//
// 🔴 It is the same Runner type with the same validation and the same confidence floor — a second
// implementation is exactly what D6 says produces two agents that diverge in the first month. What
// differs is one field, and that field only decides which placements it may run.
//
// The floor is passed in rather than defaulted here, because the floor is the PLATFORM's: a customer
// runner that chose its own would submit facts the platform would have declined, under a `config_hash`
// that claims otherwise.
func NewCustomerRunner(m Model, store InferenceStore, floor float64, nowMS func() int64, opts ...RunnerOption) (*Runner, error) {
	return newRunner(HostCustomer, m, store, floor, nowMS, opts...)
}

func newRunner(host Host, m Model, store InferenceStore, floor float64, nowMS func() int64,
	opts ...RunnerOption) (*Runner, error) {
	switch {
	case m == nil:
		return nil, errors.New("herosagent: a model is required — there is deliberately no default stub, " +
			"because a stub returning plausible edges is indistinguishable from a working agent")
	case store == nil:
		return nil, errors.New("herosagent: an inference store is required — D2's guarantee is a property " +
			"of the store, and a runner without one re-infers on every read")
	case floor <= 0 || floor > 1:
		return nil, fmt.Errorf("herosagent: the confidence floor must be in (0,1]; got %v. A zero floor "+
			"accepts everything, and a floor nobody set is a floor that is zero", floor)
	case nowMS == nil:
		return nil, errors.New("herosagent: a clock is required")
	case host != HostPlatform && host != HostCustomer:
		return nil, fmt.Errorf("herosagent: %q is not a host", host)
	}
	r := &Runner{model: m, store: store, host: host, floor: floor, nowMS: nowMS, newID: defaultInferenceID}
	for _, o := range opts {
		o(r)
	}
	return r, nil
}

// event emits to the sink when one is wired.
func (r *Runner) event(e Event, fields map[string]any) {
	if r.emit != nil {
		r.emit(e, fields)
	}
}

// CapsEnforced reports whether this runner checks a ceiling. Read by `Readiness`, so a deployment with
// unwired caps says so instead of looking identical to one whose caps are generous.
func (r *Runner) CapsEnforced() bool { return r.caps != nil }

// Host reports which runner this is, for a caller narrating what it is about to do.
func (r *Runner) Host() Host { return r.host }

// Infer runs one inference, READ-THROUGH on the three-part key (task 4.7).
//
// 🔴 D2: `inference_key = (workflow_id, source_revision, agent_config_hash)`. First request infers and
// stores; every later request READS. "The same revision always shows you the same graph" is therefore a
// property of the store, provable by a test that counts provider calls — and it survives changing
// vendors, which temperature-0-and-a-seed does not.
// 🔴 P36 — the third argument is an `AssessmentBinding` rather than a bare hash, and that IS PRD §14
// Q4's answer (decisions.md D-36.4). The definition is resolved ONCE and travels with the run as a
// value, so an activation mid-assessment cannot change what this run is executing: there is no read of
// "the active definition" inside a run that could return a different answer.
func (r *Runner) Infer(ctx context.Context, in Input, binding AssessmentBinding, placement Placement) (Result, error) {
	agentConfigHash := binding.ConfigHash
	// 🔴 TASK 7.5, AND IT IS FIRST — before the budget, before the cache read, before the residue.
	//
	// Ahead of the cache specifically: a `customer`-placed tenant whose answer is already stored
	// platform-side must still refuse here. Serving that row would be the platform answering for a
	// tenant it is not allowed to analyse, and it would look exactly like a healthy cache hit — the
	// stored result is real, it is just an artifact of a placement that has since changed.
	if err := r.host.MayRun(placement); err != nil {
		code := CodeDisabled
		if placement != PlacementDisabled {
			// Not `disabled`: this host simply is not the one. A surface must not render it as HEROS being
			// off, because for this tenant it is on — somewhere else.
			code = CodeWrongPlacement
		}
		return Result{Code: code, ProviderCalls: 0, Cause: err.Error()}, err
	}
	if err := in.Budget.Validate(); err != nil {
		return Result{Code: CodeBudgetExceeded, Cause: err.Error()}, err
	}

	// The cache read comes FIRST, before the residue is even considered: a stored answer is the answer,
	// and computing a residue to discard it would be work nobody asked for.
	if stored, ok, err := r.store.Get(ctx, in.WorkflowID, in.SourceRevision, agentConfigHash); err != nil {
		return Result{Code: CodeProviderFailed, Cause: "the inference store could not be read"}, err
	} else if ok {
		return Result{
			InferenceID: stored.InferenceID, Code: CodeOK,
			Edges: stored.Edges, Labels: stored.Labels, Abstentions: stored.Abstentions,
			Narrative: stored.Narrative,
			// 🔴 ZERO. This is the number task 10.4 asserts, and it is why the count is on the Result
			// rather than left for a caller to infer from the absence of an error.
			ProviderCalls: 0,
		}, nil
	}

	// 🔴 TASK 9.2 — THE CEILING, BEFORE THE PROVIDER CALL.
	//
	// It sits AFTER the cache read and that ordering is a decision. A cache hit makes zero provider
	// calls, so it costs nothing — refusing one under a cap would deny a customer an answer that spends
	// nothing, which is a cap acting as an availability limit rather than a spend limit. The cap is
	// about money, and a read of a stored row is not money.
	//
	// It sits BEFORE the residue and before the model, because a cap enforced afterwards is an
	// accounting record: the tokens are spent, the bill is incurred, and what the check buys is a
	// slightly faster stop on the NEXT run — which is the behaviour of having no cap at all on the run
	// that mattered.
	if r.caps != nil {
		// Nothing has been spent yet on this assessment, so there is nothing the meter cannot see.
		verdict, err := r.caps.Check(ctx, in.TenantID, 0)
		if err != nil {
			return Result{Code: CodeProviderFailed, Cause: "the token ceiling could not be read"}, err
		}
		if !verdict.Allowed {
			capErr := verdict.CapError()
			// 🔴 The EVENT is emitted here and not by the caller. A cap that stops spend silently is a
			// cap nobody knows is binding, and "which tenant is capped, and since when" is the question
			// asked the moment a customer reports that analysis stopped.
			r.event(EventCapReached, map[string]any{
				"tenant_id": in.TenantID, "scope": verdict.Scope,
				"limit": verdict.Limit, "spent": verdict.Spent,
			})
			return Result{Code: CodeCapReached, ProviderCalls: 0, Cause: capErr.Error()}, capErr
		}
	}

	// 🔴 An empty residue makes ZERO provider calls. A fully rule-covered repository costs nothing,
	// which is D3's "cost proportional to the gap" made countable (task 10.3).
	if in.Residue.Empty() {
		return Result{Code: CodeNothingToInfer, ProviderCalls: 0}, nil
	}

	ctx, cancel := context.WithTimeout(ctx, in.Budget.MaxWall)
	defer cancel()

	// The definition travels with the input, so the model layer can name WHICH definition this call is
	// for — see Input.AgentConfigHash for what went wrong when it could not.
	in.AgentConfigHash = agentConfigHash

	// 🔴 A GRAPH takes the other path. Everything above — the placement gate, the budget, the cache
	// read, the ceiling, the residue — is shape-independent and runs once for the whole assessment,
	// which is D6/D-36.6 exactly: the ceiling is per ASSESSMENT, so adding a node cannot raise it.
	if binding.Graph() {
		return r.inferGraph(ctx, in, binding, placement)
	}
	raw, usage, err := r.model.Infer(ctx, in)
	res := Result{Code: CodeOK, ProviderCalls: 1, Usage: usage}
	if err != nil {
		// Task 4.10 — the cause travels. 🚫 Never an empty graph: a caller that rendered one would be
		// reporting a provider outage as a finding about the customer's workflow.
		code := CodeProviderFailed
		if errors.Is(err, context.DeadlineExceeded) {
			code = CodeBudgetExceeded
		}
		return Result{Code: code, ProviderCalls: 1, Usage: usage, Cause: err.Error()}, err
	}

	// Task 4.9 — the token ceiling is checked against what the call actually used. Exceeding it aborts
	// and 🚫 WRITES NO PARTIAL IR: a half-applied inference is a graph nobody can reproduce from its key.
	if spent := usage.InputTokens + usage.OutputTokens; spent > in.Budget.MaxTokens {
		return Result{
			Code: CodeBudgetExceeded, ProviderCalls: 1, Usage: usage,
			Cause: fmt.Sprintf("the run spent %d tokens against a ceiling of %d; nothing was written",
				spent, in.Budget.MaxTokens),
		}, nil
	}

	res.Edges, res.Labels, res.Abstentions, res.Narrative = r.validate(in, raw)

	// 🔴 TASK 3.8 — per-node attribution on EVERY inference, including a single-node one.
	//
	// 🚫 Only when the binding carries a definition. The customer-side runner binds a hash alone
	// (`BindHash`), because the producing node is operator-side only and a node id has no business on
	// that wire — so its record stays ABSENT, which is the honest value: nobody observed which node
	// produced those edges on that machine. NULL means NOT RECORDED, and a stamped `heros_analyst`
	// would be this platform asserting a provenance it did not witness.
	var nodeRuns []NodeRun
	if n := binding.Definition.Primary(); n.NodeID != "" {
		for i := range res.Edges {
			res.Edges[i].ProducedByNode = n.NodeID
		}
		run := NodeRun{
			NodeID: n.NodeID, ProviderCalls: 1,
			TokensIn: usage.InputTokens, TokensOut: usage.OutputTokens,
			Edges: len(res.Edges), Labels: len(res.Labels), Abstentions: len(res.Abstentions),
		}
		nodeRuns = []NodeRun{run}
		res.Nodes = nodeRuns
		r.observe(run)
	}

	stored := Stored{
		InferenceID:     r.newID(in.WorkflowID, in.SourceRevision, agentConfigHash),
		TenantID:        in.TenantID,
		WorkflowID:      in.WorkflowID,
		SourceRevision:  in.SourceRevision,
		AgentConfigHash: agentConfigHash,
		// 🔴 The HOST's placement, not the argument. They agree — MayRun has already refused every
		// combination where they would not — and taking it from the host is what makes that agreement
		// structural: a stored inference cannot claim a placement its writer could not have run under,
		// which is what the graph attributes in task 8.6.
		Placement: r.host.PlacementOf(),
		Edges:     res.Edges, Labels: res.Labels, Abstentions: res.Abstentions,
		Narrative: res.Narrative,
		TokensIn:  usage.InputTokens, TokensOut: usage.OutputTokens,
		CreatedAtMS: r.nowMS(),
		Nodes:       nodeRuns,
	}
	if err := r.store.Put(ctx, stored); err != nil {
		return Result{Code: CodeProviderFailed, ProviderCalls: 1, Usage: usage,
			Cause: "the inference completed and could not be stored"}, err
	}
	res.InferenceID = stored.InferenceID

	// 🔴 The meter is written AFTER the store and its failure does NOT fail the run. The inference is
	// stored and correct; losing its meter reading costs accuracy on the next cap check, and failing
	// the caller would throw away a completed analysis to report a bookkeeping problem. The loss is
	// emitted rather than swallowed, so it is visible as what it is.
	if r.meter != nil {
		if err := r.meter.Record(ctx, Spend{
			TenantID: in.TenantID, InferenceID: stored.InferenceID,
			TokensIn: int64(usage.InputTokens), TokensOut: int64(usage.OutputTokens),
			// 🚫 No cost and Priced=false. This runner does not hold a price list — pricing is the
			// operator surface's, and a zero written here with Priced=true would report a spend nobody
			// incurred, which is exactly what task 6.5's `unpriced` word exists to prevent.
			CreatedAtMS: stored.CreatedAtMS,
		}); err != nil {
			r.event(EventInferenceStored, map[string]any{
				"inference_id": stored.InferenceID, "meter_write_failed": err.Error(),
			})
		}
	}
	r.event(EventInferenceStored, map[string]any{"inference_id": stored.InferenceID})
	return res, nil
}

// validate turns a raw model answer into facts and abstentions.
//
// 🔴 REJECTED, NEVER REPAIRED (D8). "A validator that coerces a near-miss into the nearest legal value
// would turn a detectable failure into an undetectable one." Every rejection becomes a stored
// abstention with a closed-enum reason, so the rejection has a trace and can be aggregated.
func (r *Runner) validate(in Input, raw RawResult) (
	[]ProvenancedEdge, []patternclassifier.RegionProposal, []Abstention, string) {

	edges := []ProvenancedEdge{}
	labels := []patternclassifier.RegionProposal{}
	abstain := []Abstention{}
	add := func(subject string, reason AbstentionReason, conf *float64) {
		abstain = append(abstain, Abstention{Subject: subject, Reason: reason, Confidence: conf})
	}

	for _, e := range raw.Edges {
		subject := e.From + "→" + e.To
		switch {
		// Node ids must ALREADY EXIST in the IR. This is the sentence D8 rests on: the only thing HEROS
		// can express is a graph over nodes that already exist, so the worst an injected instruction in
		// the customer's source achieves is a wrong edge.
		case !NodeExists(in.RuleIR, e.From) || !NodeExists(in.RuleIR, e.To):
			add(subject, AbstainUnknownNode, e.Confidence)
		// The closed edge vocabulary. 🚫 A `kind` of "dataflow" is not coerced to "data".
		case e.Kind != "data" && e.Kind != "control":
			add(subject, AbstainOutOfVocabulary, e.Confidence)
		// 🔴 D3 FENCE 1 at the WRITE boundary. The residue never offered this pair, so an answer naming
		// it is an answer about something it was not asked — recorded, never applied.
		case !EdgeIsAvailable(in.RuleIR, e.From, e.To):
			add(subject, AbstainFrontendOwns, e.Confidence)
		case e.Confidence == nil:
			add(subject, AbstainNoCandidate, nil)
		case *e.Confidence < r.floor:
			c := *e.Confidence
			add(subject, AbstainBelowFloor, &c)
		default:
			edges = append(edges, ProvenancedEdge{
				From: e.From, To: e.To, Kind: e.Kind, Confidence: *e.Confidence,
			})
		}
	}

	for _, l := range raw.Labels {
		p := patternclassifier.Pattern(l.Pattern)
		subject := l.Pattern
		switch {
		// The 20-pattern taxonomy, closed. `Info` is the vocabulary's own membership test, so this
		// package does not carry a second copy of the list.
		case !patternTaxonomyHas(p):
			add(subject, AbstainOutOfVocabulary, l.Confidence)
		case len(l.NodeIDs) == 0 || !allNodesExist(in.RuleIR, l.NodeIDs):
			add(subject, AbstainUnknownNode, l.Confidence)
		case l.Confidence == nil:
			add(subject, AbstainNoCandidate, nil)
		case *l.Confidence < r.floor:
			c := *l.Confidence
			add(subject, AbstainBelowFloor, &c)
		default:
			ids := append([]string(nil), l.NodeIDs...)
			sort.Strings(ids)
			labels = append(labels, patternclassifier.RegionProposal{
				Pattern:    p,
				DetectorID: HerosDetectorID,
				Scope:      patternclassifier.ScopeRegion,
				NodeIDs:    ids,
				Confidence: *l.Confidence,
				Candidate:  patternclassifier.IsBehavioral(p),
				// 🔴 THE AUTHOR. Without it these enter the partitioner indistinguishable from a rule
				// detector's — which they are by design in every other respect (D3, task 4.6).
				Author: discovery.AuthorHEROS,
			})
		}
	}

	sort.SliceStable(abstain, func(i, j int) bool {
		if abstain[i].Subject != abstain[j].Subject {
			return abstain[i].Subject < abstain[j].Subject
		}
		return abstain[i].Reason < abstain[j].Reason
	})
	return edges, labels, abstain, raw.Narrative
}

// HerosDetectorID identifies the agent in a label's `detector_id`, so a stored label names WHICH
// producer without a reader having to consult the author field.
const HerosDetectorID = "heros-agent"

func patternTaxonomyHas(p patternclassifier.Pattern) bool {
	_, ok := patternclassifier.Info(p)
	return ok
}

func allNodesExist(ir *discovery.IR, ids []string) bool {
	for _, id := range ids {
		if !NodeExists(ir, id) {
			return false
		}
	}
	return true
}

// defaultInferenceID derives an id from the three-part key, so the same key always names the same
// inference and a retry after a lost response resolves to the row that already exists.
func defaultInferenceID(workflowID, sourceRevision, configHash string) string {
	return "inf_" + shortHash(workflowID+"\x00"+sourceRevision+"\x00"+configHash)
}

// shortHash is a stable 16-hex digest, used only to derive readable ids.
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

// MemorySessionID is the session id HEROS supplies to `memoryruntime` (D13, task 6b.8b).
//
// 🔴 IT IS THE INFERENCE ID, and that is the whole of D13. `memoryruntime.Key` is `{NodeID, SessionID}`
// and the runtime NEVER invents a session id — its own comment says a defaulted one "silently merges
// conversations that should be separate". So whatever HEROS supplies here decides the blast radius.
//
// Two things follow, and both are load-bearing:
//
//  1. CROSS-TENANT LEAKAGE BECOMES STRUCTURALLY IMPOSSIBLE rather than policy-prevented. There is no
//     key under which two tenants' entries can meet, because an inference id belongs to exactly one
//     inference of one workflow of one tenant. A tenant id here — or a workflow id — would create one.
//
//  2. D2'S THREE-PART CACHE KEY STAYS HONEST. Memory carried between inferences would add a fourth,
//     invisible input: what HEROS happened to analyse first. Two tenants analysed in different orders
//     would get different graphs, the stored result would no longer be a function of its own key, and
//     re-inference would diff against something the key cannot explain.
//
// What it costs, stated rather than discovered: HEROS cannot learn across analyses. A repository
// analysed twice starts cold both times. That is a real capability given up, and it is the right trade —
// the alternative buys learning with a cross-tenant surface and a false determinism claim.
//
// 🚫 It is a FUNCTION rather than a field an assembler fills in, so there is one place the scope is
// decided and no call site can widen it.
func MemorySessionID(inferenceID string) string { return inferenceID }

// observe forwards one node's numbers to the health sink when one is wired.
func (r *Runner) observe(run NodeRun) {
	if r.health != nil {
		r.health(run)
	}
}

// inferGraph runs a MULTI-NODE definition (P36 §4).
//
// It is reached only from `Infer`, after the placement gate, the budget check, the cache read, the
// ceiling and the residue — all of which are properties of the ASSESSMENT and run once for it. That
// ordering is D-36.6: adding a node does not raise the budget, because the budget was never per node.
//
// 🔴 What this function does NOT do is as important as what it does. It never appends a node's
// contribution to a shared slice, never merges in completion order, and never reads "the active
// definition". The result is a pure function of (binding, per-node outputs), and the per-node outputs
// are a pure function of each node's own call.
func (r *Runner) inferGraph(ctx context.Context, in Input, binding AssessmentBinding, _ Placement) (Result, error) {
	d := binding.Definition
	if r.nodeModel == nil {
		// 🚫 REFUSED, never fallen back to `r.model`. Running five nodes against one model while the
		// config_hash names five different ones is a configuration nobody authored, reported under the
		// identity of one somebody did — and every number taken from it would be filed against the wrong
		// definition.
		err := fmt.Errorf("%w: this definition declares %d nodes and this runner resolves no per-node "+
			"model. Each node binds its own prompt, model and credential, so one model cannot serve them "+
			"all — and falling back to one would run a configuration the config_hash does not name",
			ErrInvalidDefinition, len(d.Nodes))
		return Result{Code: CodeProviderFailed, ProviderCalls: 0, Cause: err.Error()}, err
	}

	preds := incomingPredicates(d)
	outputs := map[string]NodeOutput{}
	res := Result{Code: CodeOK}
	var totalUsage providercall.Usage

	for _, id := range d.Ordering() {
		node, ok := d.NodeByID(id)
		if !ok {
			// Unreachable for a validated definition — `Validate` refuses an ordering naming an
			// undeclared node — and refused rather than skipped, because a silent skip here is a node an
			// operator believes ran.
			err := fmt.Errorf("%w: the ordering names %q, which the definition does not declare",
				ErrInvalidDefinition, id)
			return Result{Code: CodeProviderFailed, ProviderCalls: res.ProviderCalls, Cause: err.Error()}, err
		}
		if enter, why := shouldEnter(id, preds, outputs); !enter {
			// 🚫 A skipped node is RECORDED with its reason. A node absent from the record is
			// indistinguishable from one that ran and found nothing.
			out := NodeOutput{NodeID: id, Skipped: true, SkipReason: why}
			outputs[id] = out
			r.observe(out.run())
			continue
		}

		// 🔴 THE CEILING, BEFORE EVERY PROVIDER CALL ON EVERY NODE (task 5.2).
		//
		// Re-checked per node rather than once for the assessment, and the two are not the same check:
		// what the earlier nodes cost is passed as PENDING spend, so a definition that would blow the
		// ceiling stops AT the node that crosses it instead of after the last one. A cap enforced once
		// at the top is an accounting record for every node after the first.
		//
		// 🔴 The pending argument is load-bearing and was found by a fence rather than by review. The
		// meter is written ONCE, after the assessment — `Spend` is keyed by inference, and a
		// half-finished assessment has no inference id — so without it every node read the same stale
		// total and passed. A four-node definition under a ten-token ceiling spent thirty-two: the check
		// ran four times and learned nothing between them.
		if r.caps != nil {
			pending := int64(totalUsage.InputTokens + totalUsage.OutputTokens)
			verdict, cerr := r.caps.Check(ctx, in.TenantID, pending)
			if cerr != nil {
				return Result{Code: CodeProviderFailed, ProviderCalls: res.ProviderCalls,
					Cause: "the token ceiling could not be read"}, cerr
			}
			if !verdict.Allowed {
				capErr := verdict.CapError()
				r.event(EventCapReached, map[string]any{
					"tenant_id": in.TenantID, "scope": verdict.Scope,
					"limit": verdict.Limit, "spent": verdict.Spent, "node_id": id,
				})
				// 🔴 `not_measured` with `budget exhausted` — the state P33 already defines (D6) — and it
				// NAMES THE NODE it stopped at. "The assessment ran out of budget" is not actionable;
				// "it ran out at the critic, having completed the analyst" is.
				return Result{
					Code: CodeCapReached, ProviderCalls: res.ProviderCalls, Usage: totalUsage,
					Cause: fmt.Sprintf("%s The assessment stopped at node %q, after %d of %d nodes; "+
						"nothing was written.", capErr.Error(), id, len(outputs), len(d.Nodes)),
				}, capErr
			}
		}

		out := r.runNode(ctx, in, node)
		outputs[id] = out
		res.ProviderCalls += out.Calls
		totalUsage.InputTokens += out.Usage.InputTokens
		totalUsage.OutputTokens += out.Usage.OutputTokens
		r.observe(out.run())

		// 🔴 A node that spent past the assessment's ceiling aborts and WRITES NO PARTIAL IR, exactly as
		// the single-node path does: a half-applied inference is a graph nobody can reproduce from its key.
		if spent := totalUsage.InputTokens + totalUsage.OutputTokens; spent > in.Budget.MaxTokens {
			return Result{
				Code: CodeBudgetExceeded, ProviderCalls: res.ProviderCalls, Usage: totalUsage,
				Cause: fmt.Sprintf("the assessment spent %d tokens against a ceiling of %d, at node %q; "+
					"nothing was written. The ceiling is per ASSESSMENT rather than per node, so adding a "+
					"node does not raise it", spent, in.Budget.MaxTokens, id),
			}, nil
		}
	}

	edges, labels, abstentions, narrative, runs := mergeOutputs(d, outputs)
	res.Edges, res.Labels, res.Abstentions, res.Narrative = edges, labels, abstentions, narrative
	res.Usage = totalUsage
	res.Nodes = runs

	stored := Stored{
		InferenceID:     r.newID(in.WorkflowID, in.SourceRevision, binding.ConfigHash),
		TenantID:        in.TenantID,
		WorkflowID:      in.WorkflowID,
		SourceRevision:  in.SourceRevision,
		AgentConfigHash: binding.ConfigHash,
		Placement:       r.host.PlacementOf(),
		Edges:           edges, Labels: labels, Abstentions: abstentions,
		Narrative:   narrative,
		TokensIn:    totalUsage.InputTokens,
		TokensOut:   totalUsage.OutputTokens,
		CreatedAtMS: r.nowMS(),
		Nodes:       runs,
	}
	if err := r.store.Put(ctx, stored); err != nil {
		return Result{Code: CodeProviderFailed, ProviderCalls: res.ProviderCalls, Usage: totalUsage,
			Cause: "the inference completed and could not be stored"}, err
	}
	res.InferenceID = stored.InferenceID

	if r.meter != nil {
		if err := r.meter.Record(ctx, Spend{
			TenantID: in.TenantID, InferenceID: stored.InferenceID,
			TokensIn: int64(totalUsage.InputTokens), TokensOut: int64(totalUsage.OutputTokens),
			CreatedAtMS: stored.CreatedAtMS,
		}); err != nil {
			r.event(EventInferenceStored, map[string]any{
				"inference_id": stored.InferenceID, "meter_write_failed": err.Error(),
			})
		}
	}
	r.event(EventInferenceStored, map[string]any{
		"inference_id": stored.InferenceID, "nodes": len(runs),
	})
	return res, nil
}

// runNode makes ONE node's provider call and validates its answer.
//
// 🔴 The validation is `r.validate` — the SAME function the single-node path uses. A second validator
// for a graph's nodes is where the confidence floor quietly becomes two floors, and the floor is the
// thing standing between a guess and a stored fact.
func (r *Runner) runNode(ctx context.Context, in Input, node Node) NodeOutput {
	out := NodeOutput{NodeID: node.NodeID}
	started := r.nowMS()

	model, err := r.nodeModel(node)
	if err != nil {
		out.Failed, out.Cause = true, fmt.Sprintf("this node's model could not be resolved: %v", err)
		out.LatencyMS = r.nowMS() - started
		return out
	}
	out.Calls = 1
	raw, usage, err := model.Infer(ctx, in)
	out.Usage = usage
	out.LatencyMS = r.nowMS() - started
	if err != nil {
		// Task 4.10 — the cause travels. 🚫 Never an empty contribution: a caller that folded one in
		// would be reporting a provider outage as a finding about the customer's workflow.
		out.Failed, out.Cause = true, err.Error()
		return out
	}
	out.Edges, out.Labels, out.Abstentions, out.Narrative = r.validate(in, raw)
	// 🔴 Stamp the producing node on every edge, HERE, where the producer is known. Doing it in the
	// merge would mean the merge had to be told, and a merge that is told its inputs' provenance is a
	// merge that can be told the wrong one.
	for i := range out.Edges {
		out.Edges[i].ProducedByNode = node.NodeID
	}
	return out
}
