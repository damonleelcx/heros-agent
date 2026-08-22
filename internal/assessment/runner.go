package assessment

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/heros-foreal/agentd/internal/errorcode"
	"github.com/heros-foreal/agentd/internal/eventname"
	"github.com/heros-foreal/agentd/internal/telemetry"
)

// runner.go assembles one assessment: structural extraction first, then inference on whatever is left,
// then persistence — in that order, because design D2's ordering is what keeps the inferred proportion
// of a report as small as the repository allows.
//
// # Where the money is stopped
//
// The cap is checked BEFORE each provider call and never after (task 2.5). A cap enforced afterwards is
// an accounting record: the tokens are spent, the bill is incurred, and what the check produces is a
// slightly faster stop next time — which is the behaviour of having no cap at all on the run that
// mattered. `caps.go` in `internal/herosagent` makes the same argument for the tenant ceiling; this is
// the per-assessment containment inside it.
//
// # What happens when the money runs out
//
// Every axis that has not yet been reached degrades to `not_measured` with `budget_exhausted`, and the
// report says it is partial. 🔴 It does NOT return an error, and it does not return a shorter report.
// §7.3 makes budget refusal a first-class outcome, and the reason is legible from the alternative: an
// error page loses the six axes that DID resolve, and a shorter report presents a partial answer as a
// complete one.

// DefaultSpendCapUSD is the per-assessment ceiling a deployment gets when it names none.
//
// # Why a number is here at all, and why this number
//
// §7.3 requires spend to be bounded per assessment. A default is the difference between a bound that
// exists on every deployment and one that exists on the deployments somebody remembered to configure —
// and `NewRunner` refuses a zero cap, so the alternative to a default is a deployment that will not
// start.
//
// One US dollar, because the ceiling has to be BELOW the point where a surprise matters and ABOVE the
// cost of a complete assessment. An assessment's provider work is bounded by construction: inference
// runs only on the residue (design D2) and there are at most nine axes, so its cost is proportional to
// how much of a repository our parsers could not read rather than to the repository's size. A dollar
// is roughly an order of magnitude above that and roughly two below "somebody needs to be told".
//
// 🚫 It is a default, not a policy. A deployment that wants a different ceiling sets one; a tenant's
// own ceiling still applies on top through `herosagent.CapStore`, and the two compose as a floor —
// whichever is reached first stops the run.
const DefaultSpendCapUSD = 1.00

// Clock supplies the current time in milliseconds. Injected rather than read from the wall, for
// `tests-must-not-have-a-second-clock`'s reason: a runner that reads `time.Now()` makes every
// assertion about elapsed time an assertion about the day the test ran.
type Clock func() int64

// Inference is the seam the AI-engineer stage plugs into. It is asked ONLY about axes structural
// extraction left as `not_measured` — design D2's residue, expressed as an interface that cannot be
// handed anything else.
//
// 🔴 There is no method that takes a whole `Subject`. A caller cannot ask for a whole-repository pass
// because there is nowhere to put the request, which is the same shape `herosagent.Residue` uses and
// for the same reason: a scope the type system holds beats a scope somebody must remember to honour.
type Inference interface {
	// Infer answers for one axis, or reports that it declined. The returned finding MUST carry
	// `origin: inferred`; `Run` refuses one that does not, because a structural finding arriving
	// through this seam would be a model's reading wearing a parser's confidence.
	//
	// 🔴 There is deliberately no `replayed` return. Replay does not happen HERE: the pin is the
	// ASSESSMENT, keyed `(workflow, source_revision, agent config_hash)`, and `Service.Run` returns it
	// before a runner is ever entered. A boolean on this seam would have exactly one value on every
	// code path that can reach it, and a field with one value is a field that stops being checked.
	Infer(ctx context.Context, axis Axis, s Subject) (finding Finding, spentUSD float64, err error)
}

// Measurer is the third source of findings: a number from an eval run. Implemented by `*Measurement`.
//
// An interface rather than the concrete type so the runner's dependency list stays three seams the
// tests can drive independently, and so a deployment can mount measurement over a harness this
// process does not own.
type Measurer interface {
	// Axes declares which axes this measurer can produce a number for. Empty is the honest answer for
	// a deployment that measures nothing, and it is the default.
	Axes() []Axis
	// Measure returns a finding for one axis: `measured` when the eval ran, or `not_measured` naming
	// which of exactly four reasons applied when it did not.
	Measure(ctx context.Context, axis Axis, s Subject) (Finding, error)
}

// Store persists a completed assessment.
type Store interface {
	// Put writes the assessment and its nine findings ATOMICALLY. A partial write is worse than a
	// failed one: a report missing three findings validates against nothing and renders as a report.
	Put(ctx context.Context, a Assessment) error
}

// EvidenceResolver reports whether an evidence reference points at something that exists (task 2.4).
//
// 🔴 A reference that does not resolve FAILS THE WRITE. The alternative — persist it and let the
// reader discover it — turns design D5's "the assessment is an index over existing evidence" from a
// property into a hope, and the discovery happens weeks later to the one person least able to fix it.
type EvidenceResolver interface {
	Resolves(ctx context.Context, tenantID string, ref EvidenceRef) (bool, error)
}

// Config is one assessment's inputs.
type Config struct {
	AssessmentID    string
	TenantID        string
	SourceRevision  string
	AgentConfigHash string
	// SpendCapUSD bounds the provider spend of THIS assessment. 🔴 Zero is not "unlimited" — `Run`
	// refuses it. `herosagent.CapStore` makes the same call for the same reason: `0` is ambiguous
	// between "spend nothing" and "no limit", and the ambiguity resolves to whichever the reader
	// assumed.
	SpendCapUSD float64
}

// Runner assembles assessments.
type Runner struct {
	extractors []Extractor
	inference  Inference
	measurer   Measurer
	store      Store
	resolver   EvidenceResolver
	clock      Clock
	log        *slog.Logger
	// metrics is the health accumulator. Nil DROPS the signal rather than failing: telemetry is not a
	// precondition for assessing anything, and a runner that refused to start without an observer would
	// make an observability dependency into an availability one.
	metrics Observer
}

// NewRunner wires a runner.
//
// `inference` may be nil — that is rollout stage 1 (PRD §12), "structural only", and it is a supported
// deployment rather than a broken one. Every axis structural extraction cannot answer then stays
// `not_measured` with its own missing input, which is the honest report for that stage.
func NewRunner(store Store, resolver EvidenceResolver, inference Inference, clock Clock, log *slog.Logger) (*Runner, error) {
	if store == nil {
		return nil, errors.New("assessment: a runner with no store would return a report nobody can read again")
	}
	if resolver == nil {
		return nil, errors.New("assessment: a runner with no evidence resolver cannot keep D5's promise, " +
			"so every finding it wrote would carry a reference nobody had checked")
	}
	if clock == nil {
		return nil, errors.New("assessment: a runner needs a clock")
	}
	if log == nil {
		log = slog.Default()
	}
	return &Runner{extractors: Extractors(), inference: inference, store: store, resolver: resolver, clock: clock, log: log}, nil
}

// WithMetrics attaches the health accumulator (task 6.1).
func (r *Runner) WithMetrics(m Observer) *Runner {
	r.metrics = m
	return r
}

// WithMeasurement attaches the eval stage (rollout stage 3, PRD §12).
//
// A separate call rather than a constructor argument, because the three stages arrive in three
// releases and a constructor that took all three would make every stage-1 deployment pass two nils —
// which reads as "we forgot" rather than as "we have not shipped that yet".
func (r *Runner) WithMeasurement(m Measurer) *Runner {
	r.measurer = m
	return r
}

// Run produces, validates and persists one assessment.
func (r *Runner) Run(ctx context.Context, cfg Config, s Subject) (Assessment, error) {
	if cfg.SpendCapUSD <= 0 {
		return Assessment{}, fmt.Errorf("assessment: %s declares a spend cap of %.2f; zero is not "+
			"\"unlimited\" and an assessment with no ceiling is an unbounded provider bill against a "+
			"repository nobody has read yet", cfg.AssessmentID, cfg.SpendCapUSD)
	}
	out := Assessment{
		AssessmentID:    cfg.AssessmentID,
		TenantID:        cfg.TenantID,
		WorkflowID:      s.WorkflowID,
		SourceRevision:  cfg.SourceRevision,
		AgentConfigHash: cfg.AgentConfigHash,
		StartedAtMS:     r.clock(),
		SpendCapUSD:     cfg.SpendCapUSD,
	}
	r.observeStarted()
	r.info(ctx, eventname.AssessmentRunStarted, "an assessment began",
		"tenant_id", cfg.TenantID, "workflow_id", s.WorkflowID,
		"source_revision", cfg.SourceRevision, "spend_cap_usd", cfg.SpendCapUSD)

	// ── Stage 1 · structural, always, for every axis ─────────────────────────────────────────────
	findings := make([]Finding, 0, len(r.extractors))
	for _, e := range r.extractors {
		f, err := e.Extract(s)
		if err != nil {
			return Assessment{}, fmt.Errorf("assessment: extracting %s for %s: %w", e.Axis(), s.WorkflowID, err)
		}
		if f.Axis() != e.Axis() {
			// A programming error, caught rather than persisted: an extractor returning another
			// axis's finding produces a report that is one axis short and one axis doubled, and
			// `Validate` would report the omission while pointing at the wrong cause.
			return Assessment{}, fmt.Errorf("assessment: the %s extractor returned a finding for %s",
				e.Axis(), f.Axis())
		}
		findings = append(findings, f)
	}

	// ── Stage 2 · inference, on the residue only ─────────────────────────────────────────────────
	//
	// The residue is exactly the axes stage 1 left as `not_measured` AND that were not refused. A
	// `refused` axis is not residue: this build cannot assess it, and asking a model about it would
	// be paying to have the refusal contradicted.
	budgetExhausted := false
	for i, f := range findings {
		if r.inference == nil || f.State() != StateNotMeasured || f.Origin() == OriginInferred {
			continue
		}
		if f.MissingInput() == MissingSourceRevision || f.MissingInput() == MissingNoNodes {
			// Nothing to read. Inference over an absent snapshot is a provider call that can only
			// hallucinate, and it would be billed.
			continue
		}
		if budgetExhausted || out.SpendUSD >= cfg.SpendCapUSD {
			// 🔴 The check is HERE, before the call, and it re-reads the accumulated spend each
			// iteration rather than trusting a flag set once.
			budgetExhausted = true
			degraded, err := NotMeasured(f.Axis(), MissingBudgetExhausted, fmt.Sprintf(
				"this assessment reached its $%.2f cap before %s could be inferred; re-running with a "+
					"higher cap will answer it", cfg.SpendCapUSD, f.Axis()), s.Evidence())
			if err != nil {
				return Assessment{}, err
			}
			findings[i] = degraded
			continue
		}

		inferred, spent, err := r.inference.Infer(ctx, f.Axis(), s)
		if err != nil {
			// 🔴 An inference failure leaves the STRUCTURAL finding in place. It does not fail the
			// assessment and it does not blank the axis: stage 1's answer is still true, and losing
			// eight good findings because a ninth provider call timed out is the shape of failure the
			// whole rollout plan is built to avoid ("rollback is disabling the newest source of
			// findings; the report shrinks in state, never in axis count").
			r.warn(ctx, eventname.AssessmentAxisNotMeasured, errorcode.ProviderError,
				"inference failed for an axis; the structural finding stands",
				"axis", string(f.Axis()), "workflow_id", s.WorkflowID, "error", err.Error())
			continue
		}
		out.SpendUSD += spent
		if inferred.Origin() != OriginInferred {
			return Assessment{}, fmt.Errorf("assessment: the inference seam returned a %s finding for %s; "+
				"a model's reading may not arrive wearing a parser's confidence", inferred.Origin(), f.Axis())
		}
		if inferred.Axis() != f.Axis() {
			return Assessment{}, fmt.Errorf("assessment: inference for %s returned a finding for %s",
				f.Axis(), inferred.Axis())
		}
		findings[i] = inferred

		r.info(ctx, eventname.AssessmentInferencePinned, "an inference ran and was pinned",
			"axis", string(f.Axis()), "workflow_id", s.WorkflowID, "source_revision", cfg.SourceRevision,
			"agent_config_hash", cfg.AgentConfigHash, "inference_address", inferred.InferenceAddress(),
			"provider_model_version", inferred.ProviderModelVersion(), "spend_usd", spent)
	}
	if budgetExhausted {
		r.warn(ctx, eventname.AssessmentBudgetExhausted, errorcode.EntitlementDenied,
			"an assessment reached its spend cap; the remaining axes degraded to not_measured",
			"workflow_id", s.WorkflowID, "spend_usd", out.SpendUSD, "spend_cap_usd", cfg.SpendCapUSD)
	}

	// ── Stage 3 · measurement, where a runnable baseline exists ──────────────────────────────────
	//
	// 🔴 A measurement result is applied ONLY IF IT IS STRONGER than what is already there, and that
	// rule is the whole of this block. The failure it prevents: structural extraction reports
	// "all four call sites name gpt-4o-mini" (an observation a reader can check), the eval cannot run
	// because a credential is missing, and a naive assembler overwrites the observation with
	// `not_measured`. The report would then be WORSE for having tried to measure — losing a true claim
	// to report a failure of ours.
	//
	// `rank` is the same ladder FR5 orders the report by, used here as a comparison. One ladder, two
	// uses, no second definition of "stronger".
	if r.measurer != nil {
		// The position of each axis in the slice, built once. A local map rather than a helper
		// function: `composite_fence_test.go` refuses any function that takes a `[]Finding` and returns
		// a bare number, and that fence is worth more than the three lines a helper would save.
		position := make(map[Axis]int, len(findings))
		for i, f := range findings {
			position[f.Axis()] = i
		}
		for _, axis := range r.measurer.Axes() {
			i, ok := position[axis]
			if !ok {
				continue
			}
			measured, err := r.measurer.Measure(ctx, axis, s)
			if err != nil {
				// Same posture as a failed inference: the existing finding stands. Losing eight good
				// findings because a ninth eval could not start is the shape the rollout plan is built
				// to avoid.
				r.warn(ctx, eventname.AssessmentAxisNotMeasured, errorcode.ProviderError,
					"measurement failed for an axis; the existing finding stands",
					"axis", string(axis), "workflow_id", s.WorkflowID, "error", err.Error())
				continue
			}
			if measured.Axis() != axis {
				return Assessment{}, fmt.Errorf("assessment: measurement for %s returned a finding for %s",
					axis, measured.Axis())
			}
			if rank(measured) < rank(findings[i]) {
				findings[i] = measured
			}
		}
	}

	out.Findings = findings
	out.CompletedAtMS = r.clock()

	// ── Stage 3 · the WARN every absence owes ────────────────────────────────────────────────────
	//
	// Emitted after assembly rather than inside stage 1, so an axis that structural extraction left
	// absent and inference then answered does not log an absence that is no longer true.
	for _, f := range out.Findings {
		if f.State() != StateNotMeasured {
			continue
		}
		r.warn(ctx, eventname.AssessmentAxisNotMeasured, "",
			"an axis could not be measured", "axis", string(f.Axis()),
			"missing_input", string(f.MissingInput()), "origin", string(f.Origin()),
			"workflow_id", s.WorkflowID, "tenant_id", cfg.TenantID)
	}

	if err := out.Validate(); err != nil {
		r.observeRefused()
		return Assessment{}, err
	}
	if err := r.resolveEvidence(ctx, out); err != nil {
		r.observeRefused()
		return Assessment{}, err
	}
	if err := r.store.Put(ctx, out); err != nil {
		r.observeRefused()
		return Assessment{}, fmt.Errorf("assessment: persisting %s: %w", out.AssessmentID, err)
	}
	// 🔴 Counted AFTER the write, never before. An assessment counted on the way in would count INTENT
	// rather than effect, and a 201 is not evidence of a write — which is the same rule the conversation
	// events follow and the same one `e2e-acceptance-live-events` states for the whole product.
	r.observeCompleted(out)
	return out, nil
}

func (r *Runner) observeStarted() {
	if r.metrics != nil {
		r.metrics.Started()
	}
}

func (r *Runner) observeRefused() {
	if r.metrics != nil {
		r.metrics.Refused()
	}
}

func (r *Runner) observeCompleted(a Assessment) {
	if r.metrics != nil {
		r.metrics.Completed(a)
	}
}

// resolveEvidence is task 2.4. Every reference is checked before ANY of them is written.
func (r *Runner) resolveEvidence(ctx context.Context, a Assessment) error {
	for _, f := range a.Findings {
		ok, err := r.resolver.Resolves(ctx, a.TenantID, f.Evidence())
		if err != nil {
			return fmt.Errorf("assessment: resolving the evidence for %s: %w", f.Axis(), err)
		}
		if !ok {
			return fmt.Errorf("assessment: %s points at %s/%s, which does not resolve. Design D5 makes "+
				"this report an INDEX over existing evidence; a reference that resolves to nothing is a "+
				"dead link a reader finds instead of a claim they can check",
				f.Axis(), f.Evidence().Surface, f.Evidence().Locator)
		}
	}
	return nil
}

func (r *Runner) info(ctx context.Context, ev eventname.Name, msg string, kv ...any) {
	if !ev.Valid() {
		return
	}
	r.log.InfoContext(ctx, msg, append(logBase(ctx, ev, ""), kv...)...)
}

func (r *Runner) warn(ctx context.Context, ev eventname.Name, code errorcode.Code, msg string, kv ...any) {
	if !ev.Valid() {
		return
	}
	r.log.WarnContext(ctx, msg, append(logBase(ctx, ev, code), kv...)...)
}

func logBase(ctx context.Context, ev eventname.Name, code errorcode.Code) []any {
	out := []any{"event", ev.String()}
	if code != "" {
		out = append(out, "error_code", string(code))
	}
	if tid := telemetry.TraceIDFromContext(ctx); tid != "" {
		out = append(out, "trace_id", tid, "request_id", tid)
	}
	return out
}
