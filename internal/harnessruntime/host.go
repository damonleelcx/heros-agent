package harnessruntime

import "fmt"

// Host services (P18 tasks 10.5/10.6, decisions.md D-10)
// ──────────────────────────────────────────────────────
//
// Three of the five strategies want a second actor: `react-loop` needs the customer's TOOL EXECUTOR,
// `plan-execute` needs a PLANNER and a step executor, and `critic-loop` needs a call to a SEPARATE
// critic model. This runtime performs none of them.
//
// 🚫 It could. Reusing the client the caller already has would work today, and it is the shortcut this
// file exists to refuse: a generated file that reached a provider would put a CREDENTIAL IN THE
// CUSTOMER'S PROCESS and spend it on turns the author never wrote — a new egress surface created by a
// codemod, which is exactly what the standing L1 blast-radius note forbids compounding. And a failure in
// that second call would fail a call the author cannot see or retry.
//
// 🔴 So a strategy whose service is absent REFUSES rather than degrading. That is the load-bearing half:
// running `reflexion`'s loop when `critic-loop` was asked for would execute one strategy under another's
// `config_hash`, which is the DRIVE-AND-DECIDE failure (D-9) arriving by a different route. The refusal
// names the service, because "this strategy is unsupported" tells the caller nothing about what to supply.

// ToolInvoker runs a tool the model asked for and returns what it produced. Supplied by a HOST that knows
// the customer's tool dispatch; never implemented in this package or in a generated artifact.
type ToolInvoker interface {
	InvokeTool(name string, arguments string) (string, error)
}

// Planner produces a plan and executes its steps. Supplied by a host.
type Planner interface {
	Plan(goal string) ([]string, error)
	ExecuteStep(step string) (string, error)
}

// Critic judges an answer and returns its critique plus whether it accepts. Supplied by a host that owns
// the critic model's credential — never this package.
type Critic interface {
	Critique(answer string) (critique string, accepted bool, err error)
}

// Hosts is the set of injected services a run may use. A nil field is an absent service, and an absent
// service a strategy needs is a refusal — never a substitution.
type Hosts struct {
	ToolInvoker ToolInvoker
	Planner     Planner
	Critic      Critic
	// SpendMeter reports what this run has spent so far, for the envelope's spend ceiling (P34).
	//
	// 🔴 A HOST service like the three above, and for the same reason: only the host knows what a call
	// cost. This runtime makes no provider call, so it has no way to price one, and a runtime that
	// estimated spend would be publishing a number nobody can reconcile against a bill.
	SpendMeter SpendMeter
}

// SpendMeter reports the running cost of the current node, in USD. Supplied by a host that owns the
// metering path; never implemented in this package or in a generated artifact.
//
// 🚫 It reports SPENT, not REMAINING. The ceiling is the envelope's and belongs to whoever owns the
// envelope; a meter that returned "remaining" would be a second place the ceiling lives, and the two
// could disagree about a policy at exactly the moment it is being enforced.
type SpendMeter interface {
	SpentUSD() float64
}

// require refuses when the strategy's service was not supplied.
//
// The message names the service AND says what supplying it means, because the caller reading it is
// deciding whether to wire something up or to pick a different strategy, and both are legitimate.
func (h Hosts) require(svc HostService) error {
	switch svc {
	case HostNone:
		return nil
	case HostToolInvoker:
		if h.ToolInvoker == nil {
			return fmt.Errorf("%w: this strategy continues by RUNNING the tool the model asked for, and no "+
				"tool executor was supplied. 🚫 It is not degraded to a loop that skips the tool: that loop "+
				"is a different strategy, and running it here would report one scaffold under another's "+
				"config_hash", ErrMissingHostService)
		}
	case HostPlanner:
		if h.Planner == nil {
			return fmt.Errorf("%w: this strategy's first turn produces a plan and the rest EXECUTE its "+
				"steps, and no planner was supplied. 🚫 It is not degraded to re-asking the same question",
				ErrMissingHostService)
		}
	case HostCritic:
		if h.Critic == nil {
			return fmt.Errorf("%w: this strategy continues by calling a SEPARATE model to judge the answer, "+
				"and no critic was supplied. 🚫 It is not degraded to self-critique: a critic-loop without a "+
				"critic IS reflexion, and running it under critic-loop's config_hash would report one "+
				"strategy as another. The critic is a host service because a generated file may not reach a "+
				"provider — that would put your credential in your own process, spent on turns you did not "+
				"write", ErrMissingHostService)
		}
	}
	return nil
}

// exhausted reports whether the envelope's spend ceiling has been reached, so the next turn must not be
// taken. False when no ceiling was declared — an absent ceiling is not a ceiling of zero.
//
// 🔴 `>=`, not `>`. A ceiling of one dollar means a run may spend up to a dollar; a run that has already
// spent exactly a dollar has used its budget, and admitting one more turn on the strength of an
// equality would make every declared ceiling one turn looser than it reads.
func exhausted(cfg Config, hosts Hosts) bool {
	if cfg.SpendCeilingUSD == nil || hosts.SpendMeter == nil {
		return false
	}
	return hosts.SpendMeter.SpentUSD() >= *cfg.SpendCeilingUSD
}
