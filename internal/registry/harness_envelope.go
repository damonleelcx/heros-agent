package registry

import (
	"encoding/json"
	"sort"
	"strings"
)

// THE EXECUTION ENVELOPE — what the harness axis narrows to (P34 FR5, task 4.1; ADR-014)
// ──────────────────────────────────────────────────────────────────────────────────────
//
// # The problem this file solves, and the one it deliberately does not
//
// After ADR-014 the harness axis carries the EXECUTION ENVELOPE: sandbox posture, host-service
// provision, turn ceiling, spend ceiling, retries, timeouts, concurrency limit, and the guardrail and
// approval-gate bindings. The loop axis carries the iteration policy. The split line is **imposed vs
// chosen** — a policy is imposed on an author, a value is chosen by them, and putting both on one axis
// is what made the previous model unreviewable.
//
// 🔴 The envelope is a NEW STRATEGY IN THE EXISTING HARNESS VOCABULARY, not a new field on HarnessSpec
// and not a new Kind. That is the whole of ADR-014's expand-only ruling made concrete:
//
//   - A new FIELD on HarnessSpec would change the canonical bytes of every existing harness entry, hence
//     its version_id, hence the config_hash of every spec referencing it — the orphaning chain.
//   - A new KIND would be a second registry for a thing the harness axis already IS.
//   - A new STRATEGY moves nothing. Adding to BuiltinHarnessStrategies() changes no stored entry's
//     content, because the strategy SET is not hashed into an entry — only (Kind, Name, Spec) is.
//     TestPreP34ConfigHashesAreReproducedExactly is the fence on that claim.
//
// # What makes a legacy harness entry "loop-bearing"
//
// One mechanical rule, in one place: a harness entry is loop-bearing iff its strategy is a member of the
// LOOP vocabulary. `envelope` is not; the five relocated strategies are. That is what
// HarnessEntry.IsLoopBearing answers, and it is what P34 task 3.6's ambiguity refusal keys on.
//
// 🔴 `single-shot` counts as loop-bearing, and that is deliberate rather than an oversight. A harness
// entry naming `single-shot` states an ITERATION POLICY — "this node runs exactly one turn" — so a spec
// that also sets a loop_ref has stated the iteration policy twice, and the two may disagree. Refusing it
// is the same rule as every other loop strategy, with no special case; a carve-out here would be an `if`
// deciding which of two contracts a ref was honouring.

// StrategyEnvelope is the execution-envelope strategy: the harness axis's post-P34 shape.
//
// It is a wire name and is frozen forever, like every other strategy name. The human label may be
// reworded freely.
const StrategyEnvelope = "envelope"

// Host services an envelope may provide. These are the strings the params schema enumerates and the
// names a resolve-time refusal prints, spelled once so the schema, the check and the message agree.
const (
	HostServiceToolExecutor = "tool-executor"
	HostServicePlanner      = "planner"
	HostServiceCritic       = "critic"
)

// HostServiceNames lists the closed set, sorted. Read by the schema conformance test and by the console.
func HostServiceNames() []string {
	return []string{HostServiceCritic, HostServicePlanner, HostServiceToolExecutor}
}

// SandboxPostures is the closed set of sandbox postures an envelope may declare, sorted.
//
// `no-network` is the DEFAULT NOWHERE: the schema requires the field, because "what may this node
// reach" is not a question the platform may answer on an author's behalf.
func SandboxPostures() []string {
	return []string{"no-network", "provider-egress-only", "unrestricted-egress"}
}

// EnvelopeHarness is the execution envelope, expressed as a harness strategy.
type EnvelopeHarness struct{}

func (EnvelopeHarness) Name() string  { return StrategyEnvelope }
func (EnvelopeHarness) Title() string { return "Execution envelope" }
func (EnvelopeHarness) Description() string {
	return "What this node is allowed to do and inside what walls: where it may reach on the network, " +
		"which host services it is given, the ceilings on turns and spend it may not exceed, how many " +
		"of its steps may overlap, and which guardrail and approval gate it answers to. It imposes; the " +
		"loop chooses inside it."
}

// ParamsSchema is the envelope's content.
//
// 🔴 `sandbox_posture`, `turn_ceiling` and `spend_ceiling_usd` are REQUIRED. Each of the three is a
// blast-radius statement, and the honest default for a blast-radius statement is that there isn't one —
// an omitted ceiling reads as "unbounded" to a reader and would have to be read as *some* number by the
// code, and those two readings differing is exactly how a policy stops being a policy.
//
// 🔴 `max_turns` is INEXPRESSIBLE here, and `turn_ceiling` is inexpressible on a loop. The pair is the
// mechanical form of design D2: an operator raising a ceiling must change no loop entry's content and no
// loop entry's version_id, which is only structurally true while the two live in different sealed units.
func (EnvelopeHarness) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"sandbox_posture":{"type":"string","enum":["no-network","provider-egress-only","unrestricted-egress"],"description":"Where this node may reach. Required: there is no safe default for what a node is allowed to talk to."},
			"host_services":{"type":"array","items":{"type":"string","enum":["tool-executor","planner","critic"]},"uniqueItems":true,"description":"The second actors this envelope PROVIDES. A loop needing one the envelope does not provide is refused at resolve, not at run."},
			"turn_ceiling":{"type":"integer","minimum":1,"maximum":16,"description":"The most turns any loop under this envelope may take. A policy, imposed; the loop chooses a value at or below it."},
			"spend_ceiling_usd":{"type":"number","minimum":0,"description":"The most this node may spend in one run. Checked BEFORE each provider call; exhaustion is a named stopping condition, not an error."},
			"retry_budget":{"type":"integer","minimum":0,"maximum":8,"description":"How many failed turns may be retried. Here rather than on the loop because retries multiply turns, so an unbounded budget would defeat the turn ceiling from the side."},
			"timeout_seconds":{"type":"integer","minimum":1,"maximum":3600,"description":"The wall-clock bound on one run of this node."},
			"concurrency_limit":{"type":"integer","minimum":1,"maximum":32,"description":"The most nodes of a concurrent group that may overlap. Enforced by the sandbox at execution, independently of what the spec declared."},
			"guardrail_ref":{"type":"string","minLength":1,"description":"The guardrail this node answers to."},
			"approval_gate_ref":{"type":"string","minLength":1,"description":"The approval gate this node answers to."}
		},
		"required":["sandbox_posture","turn_ceiling","spend_ceiling_usd"],
		"additionalProperties":false
	}`)
}

// Envelope is a harness entry's envelope params, decoded. Every field is optional AT THIS LAYER —
// which fields are REQUIRED is the schema's answer, enforced at seal, and re-stating it here would be a
// second validator to keep true.
//
// 🔴 Pointers on the three required numbers, not bare values. A zero spend ceiling and an ABSENT one are
// different facts — the first forbids every call, the second means this entry is not an envelope at all
// — and a bare `float64` cannot tell them apart. Reading an absent ceiling as 0 would silently forbid
// every call; reading it as "unbounded" would silently permit every call. Neither is acceptable, so the
// distinction is carried.
type Envelope struct {
	SandboxPosture   string   `json:"sandbox_posture"`
	HostServices     []string `json:"host_services"`
	TurnCeiling      *int     `json:"turn_ceiling"`
	SpendCeilingUSD  *float64 `json:"spend_ceiling_usd"`
	RetryBudget      *int     `json:"retry_budget"`
	TimeoutSeconds   *int     `json:"timeout_seconds"`
	ConcurrencyLimit *int     `json:"concurrency_limit"`
	GuardrailRef     string   `json:"guardrail_ref"`
	ApprovalGateRef  string   `json:"approval_gate_ref"`
}

// Provides reports whether the envelope supplies a host service.
func (e Envelope) Provides(svc string) bool {
	for _, s := range e.HostServices {
		if s == svc {
			return true
		}
	}
	return false
}

// MissingServices lists the services in `want` this envelope does not provide, sorted, for a refusal
// that names what to supply rather than only that something was absent.
func (e Envelope) MissingServices(want ...string) []string {
	var out []string
	for _, w := range want {
		if !e.Provides(w) {
			out = append(out, w)
		}
	}
	sort.Strings(out)
	return out
}

// IsEnvelope reports whether this harness entry carries an execution envelope.
func (e *HarnessEntry) IsEnvelope() bool { return e != nil && e.Spec.Strategy == StrategyEnvelope }

// IsLoopBearing reports whether this harness entry states an ITERATION POLICY — the legacy shape P34
// keeps resolvable indefinitely and refuses to combine with a loop_ref (task 3.6).
//
// One rule, one place: loop-bearing iff the strategy is a member of the loop vocabulary.
func (e *HarnessEntry) IsLoopBearing() bool { return e != nil && IsLoopStrategy(e.Spec.Strategy) }

// EnvelopeOf decodes a harness entry's envelope params, or reports that this entry is not an envelope.
//
// 🔴 It fails closed on a decode error rather than returning a zero Envelope. A zero Envelope provides
// no host service and imposes no ceiling, which would make a corrupt entry read as the most permissive
// possible policy at exactly the moment the ceilings are being checked.
func EnvelopeOf(e *HarnessEntry) (Envelope, bool, error) {
	if !e.IsEnvelope() {
		return Envelope{}, false, nil
	}
	var env Envelope
	if len(e.Spec.Params) == 0 {
		return Envelope{}, true, errInvalid("harness entry %s names strategy %q but carries no params; "+
			"an envelope with no posture and no ceilings is not an envelope", e.VersionID, StrategyEnvelope)
	}
	if err := json.Unmarshal(e.Spec.Params, &env); err != nil {
		return Envelope{}, true, errInvalid("harness entry %s: envelope params are not decodable: %v",
			e.VersionID, err)
	}
	return env, true, nil
}

// HostServicesForLoop names the host services a loop strategy requires, or nil.
//
// 🔴 It is the LOOP axis's own statement of what it needs, and it must agree with
// internal/harnessruntime's HostServiceFor — the function the RUN path refuses on. A conformance test
// pins the two, because two answers to "what does react-loop need" is how a resolve-time gate ends up
// permitting something the run then refuses.
func HostServicesForLoop(strategy string) []string {
	switch strategy {
	case "react-loop":
		return []string{HostServiceToolExecutor}
	case "plan-execute":
		return []string{HostServicePlanner}
	case "critic-loop":
		return []string{HostServiceCritic}
	default:
		return nil
	}
}

// HostServiceDisplay renders a service list for a refusal message.
func HostServiceDisplay(svcs []string) string { return strings.Join(svcs, ", ") }
