package assessment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/heros-foreal/agentd/internal/discovery"
	"github.com/heros-foreal/agentd/internal/eventname"
)

// service.go is what the API mounts: "assess this workflow" in one call, with the revision, the IR and
// the discovery report resolved on this side of the boundary.
//
// # Why the handler does not do this
//
// The handler's job is to establish who is asking. Resolving a workflow to a revision, materialising a
// snapshot, running discovery and assembling nine findings are four failures with four different
// meanings, and a handler that inlined them would have to choose a status code for each in the middle
// of a request. Here they are named once and the handler maps two of them.

// ErrNoSource is returned when the tenant holds no snapshot for the workflow.
//
// 🔴 A named error rather than an empty assessment. "We have never seen your code" and "we saw your
// code and could determine nothing" are different sentences to a customer, and the second one costs
// them an afternoon if we say it when the first is true. `Runner` also has a `no_source_snapshot`
// missing input, and the two are NOT redundant: this one is reached when the workflow has no snapshot
// at all, and that one when a source was expected and the IR came back nil mid-run.
var ErrNoSource = errors.New("assessment: no source snapshot is held for this workflow")

// SourceReader resolves a workflow to the material an assessment reads.
//
// One method, and it returns the report as well as the IR. That pairing is the whole reason this
// interface exists rather than reusing an IR-only reader: without the discovery report the graph axis
// cannot tell "your calls are independent" from "our parser emits no edges", which is design D6.
type SourceReader interface {
	Analyse(ctx context.Context, tenantID, workflowID string) (revision string, ir *discovery.IR, rep discovery.DiscoveryReport, err error)
}

// IDGenerator mints assessment ids. Injected for the reason the clock is: an id derived from the wall
// clock makes a test's assertions depend on the millisecond it ran in.
type IDGenerator func() string

// ConfigHasher returns the agent configuration hash that identifies WHAT produced an assessment.
//
// 🔴 A function rather than a stored string, because the value must be read at RUN time. A hash
// captured at start-up would keep naming a configuration an operator has since replaced, and design
// D7's whole point is that a finding stays attributable to the configuration that actually made it.
type ConfigHasher func(ctx context.Context) (string, error)

// ServiceConfig wires the service.
type ServiceConfig struct {
	Runner      *Runner
	Store       *PGStore
	Source      SourceReader
	NewID       IDGenerator
	ConfigHash  ConfigHasher
	SpendCapUSD float64
	// Log receives the replay event. Nil takes the default logger: telemetry is not a precondition for
	// assessing anything, and a service that refused to start without a sink would make an
	// observability dependency into an availability one.
	Log *slog.Logger
}

// Service implements the API's runner interface.
type Service struct {
	runner      *Runner
	store       *PGStore
	source      SourceReader
	newID       IDGenerator
	configHash  ConfigHasher
	spendCapUSD float64
	log         *slog.Logger
}

// NewService validates the wiring and returns the service.
func NewService(cfg ServiceConfig) (*Service, error) {
	switch {
	case cfg.Runner == nil:
		return nil, errors.New("assessment: a service with no runner cannot assess anything")
	case cfg.Store == nil:
		return nil, errors.New("assessment: a service with no store would produce reports nobody can read again")
	case cfg.Source == nil:
		return nil, errors.New("assessment: a service with no source reader has nothing to read")
	case cfg.NewID == nil:
		return nil, errors.New("assessment: a service needs an id generator")
	case cfg.ConfigHash == nil:
		return nil, errors.New("assessment: a service with no config hasher would write findings that " +
			"cannot be attributed to the configuration that produced them (FR16)")
	case cfg.SpendCapUSD <= 0:
		return nil, fmt.Errorf("assessment: the per-assessment spend cap is %.2f; zero is not "+
			"\"unlimited\" and a service with no ceiling is an unbounded provider bill", cfg.SpendCapUSD)
	}
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		runner: cfg.Runner, store: cfg.Store, source: cfg.Source,
		newID: cfg.NewID, configHash: cfg.ConfigHash, spendCapUSD: cfg.SpendCapUSD, log: log,
	}, nil
}

// Run assesses a workflow at the tenant's current revision.
//
// 🔴 It returns the PINNED assessment when one already exists for this exact
// `(workflow, revision, agent config_hash)`, and makes no provider call. That is FR15 and QA task 7.5,
// and it is enforced here rather than inside the inference seam because it must hold for an assessment
// with NO inferred findings too: re-running a structural-only assessment should not write a second row
// that differs only in its id and its timestamps.
func (s *Service) Run(ctx context.Context, tenantID, workflowID string) (Assessment, error) {
	revision, ir, rep, err := s.source.Analyse(ctx, tenantID, workflowID)
	if err != nil {
		return Assessment{}, err
	}
	if revision == "" {
		return Assessment{}, ErrNoSource
	}
	hash, err := s.configHash(ctx)
	if err != nil {
		return Assessment{}, fmt.Errorf("assessment: reading the agent configuration hash: %w", err)
	}

	// 🔴 THE PIN, and the one condition on it.
	//
	// A COMPLETE assessment for this exact key is returned unchanged, with no provider call — FR15 and
	// QA task 7.5. A PARTIAL one is not: it stopped at a spend cap, and a caller re-running after
	// raising the cap is asking for the axes that were never reached. Returning the partial report
	// would answer "why is my report still incomplete?" with the incomplete report, forever, and the
	// only way out would be a cache nobody can see.
	if pinned, ok, err := s.store.FindPin(ctx, tenantID, workflowID, revision, hash); err != nil {
		return Assessment{}, err
	} else if ok && !pinned.Partial() {
		s.log.InfoContext(ctx, "a pinned assessment answered without a provider call",
			"event", eventname.AssessmentInferenceReplayed.String(),
			"assessment_id", pinned.AssessmentID, "tenant_id", tenantID, "workflow_id", workflowID,
			"source_revision", revision, "agent_config_hash", hash,
			"inferred_findings", pinned.Tally().Inferred)
		return pinned, nil
	}

	return s.runner.Run(ctx, Config{
		AssessmentID:    s.newID(),
		TenantID:        tenantID,
		SourceRevision:  revision,
		AgentConfigHash: hash,
		SpendCapUSD:     s.spendCapUSD,
	}, Subject{WorkflowID: workflowID, IR: ir, Report: rep})
}

// Latest returns the newest assessment of a workflow, or ok=false when there is none.
func (s *Service) Latest(ctx context.Context, tenantID, workflowID string) (Assessment, bool, error) {
	return s.store.Latest(ctx, tenantID, workflowID)
}

// Reinfer is FR9 and task 3.2's second half: **re-inference is an explicit act whose output is shown
// as a diff against the pin.**
//
// # 🔴 Why it is a separate method and not a flag on Run
//
// `Run` is idempotent and free on a pinned key — that is FR15, and it is what lets a console call it on
// every page load. Re-inference is the opposite: it deliberately ignores the pin and SPENDS MONEY. A
// boolean parameter on one method would put "does this cost anything" behind an argument a caller can
// default, and the caller who defaults it is a retry loop.
//
// # What the diff is FOR
//
// Not novelty. It answers the only question a changed report raises — *whose fault is it* — by naming
// which of the three inputs moved. See `diff.go`; without it a provider's routine upgrade renders as
// the customer's repository getting worse.
func (s *Service) Reinfer(ctx context.Context, tenantID, workflowID string) (Assessment, []AxisDiff, error) {
	before, hadPrior, err := s.store.Latest(ctx, tenantID, workflowID)
	if err != nil {
		return Assessment{}, nil, err
	}

	revision, ir, rep, err := s.source.Analyse(ctx, tenantID, workflowID)
	if err != nil {
		return Assessment{}, nil, err
	}
	if revision == "" {
		return Assessment{}, nil, ErrNoSource
	}
	hash, err := s.configHash(ctx)
	if err != nil {
		return Assessment{}, nil, fmt.Errorf("assessment: reading the agent configuration hash: %w", err)
	}

	after, err := s.runner.Run(ctx, Config{
		AssessmentID:    s.newID(),
		TenantID:        tenantID,
		SourceRevision:  revision,
		AgentConfigHash: hash,
		SpendCapUSD:     s.spendCapUSD,
	}, Subject{WorkflowID: workflowID, IR: ir, Report: rep})
	if err != nil {
		return Assessment{}, nil, err
	}

	if !hadPrior {
		// Nothing to diff against. 🔴 Returning an empty diff here would say "nothing changed", which
		// is false in the most misleading direction: everything is new. The caller distinguishes the
		// two by the nil, and the console says "first assessment" rather than "no changes".
		return after, nil, nil
	}
	diff, err := Diff(before, after)
	if err != nil {
		return Assessment{}, nil, err
	}
	return after, diff, nil
}
