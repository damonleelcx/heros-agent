package harnessruntime

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ONE definition of each builtin strategy's loop behaviour (P18 task 10.2, FR22).
//
// 🔴 One definition, called by every consumer including the generated artifact, because the alternative
// is a per-language re-derivation of the same turn semantics — seven places `max_turns` can be off by
// one, each running under the sealed strategy's config_hash. That is the single-source-of-truth rule the
// memory runtime's D5 already had to establish, applied to a loop instead of a retention rule.
//
// The dispatch is keyed by strategy NAME rather than declared on registry.HarnessStrategy; see the
// package doc for why, and TestRuntimeAndVocabularyNameTheSameSet for what binds them.

// Params is a strategy's sealed params, decoded. Every field is optional at this layer — which fields are
// REQUIRED is the registry schema's answer, enforced at seal, and re-stating it here would be a second
// validator to keep true.
type Params struct {
	MaxTurns         int    `json:"max_turns"`
	StopCondition    string `json:"stop_condition"`
	AnswerMarker     string `json:"answer_marker"`
	ReflectionPrompt string `json:"reflection_prompt"`
	RetryBudget      int    `json:"retry_budget"`
	CriticModelRef   string `json:"critic_model_ref"`
}

// DecodeParams reads sealed params JSON into Params. A caller holding a resolved projection's
// `map[string]any` should marshal it and pass it here rather than reading fields by hand, so there is one
// decoder and one set of field names.
func DecodeParams(raw json.RawMessage) (Params, error) {
	var p Params
	if len(raw) == 0 {
		return p, nil
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, fmt.Errorf("%w: %v", ErrInvalidParams, err)
	}
	return p, nil
}

// Decision is what Plan decides after a turn: whether to take another, and what to append if so.
type Decision struct {
	// Continue reports whether another turn should run — subject to the ceiling, which Run enforces
	// independently. A strategy cannot talk its way past the bound.
	Continue bool
	// Append is what the continuation adds to the next turn's message list. Empty when Continue is false.
	Append []Message
}

// TurnCeiling is the hard upper bound on any strategy's max_turns. It mirrors registry.MaxTurnsCeiling
// and is asserted equal to it by a test — spelled here rather than imported so the runtime keeps
// depending only on names and params, and pinned by the test so the two cannot drift silently.
const TurnCeiling = 16

// HostService names an actor a strategy needs and that the runtime will not be. Empty means none.
type HostService string

const (
	// HostNone — the strategy needs no second actor.
	HostNone HostService = ""
	// HostToolInvoker — the loop continues by RUNNING the tool the model asked for.
	HostToolInvoker HostService = "tool executor"
	// HostPlanner — the loop's first turn produces a plan and the rest execute its steps.
	HostPlanner HostService = "planner and step executor"
	// HostCritic — the loop continues by calling a SEPARATE model to judge the answer.
	HostCritic HostService = "critic model"
)

// loop is one strategy's behaviour: its ceiling, the host service it needs, and its per-turn decision.
type loop struct {
	// ceiling returns the bounded turn count, or ErrInvalidParams. It is a function rather than a field
	// because the identity's ceiling is fixed at one while every other strategy's comes from params.
	ceiling func(Params) (int, error)
	// hostService names the actor this strategy needs, or HostNone.
	hostService func() HostService
	// plan decides whether to take another turn after `turn` produced `answer`.
	plan func(p Params, turn int, answer string) Decision
}

// loops is the closed dispatch. 🔴 A strategy in the sealed vocabulary with no entry here fails LOUDLY at
// Run (ErrUnknownStrategy) rather than silently running one turn, and a conformance test asserts the two
// sets are equal so the loud failure is never reached in production.
var loops = map[string]loop{
	"single-shot": {
		// The identity: exactly one turn, always. `max_turns` is inexpressible on it at the registry layer,
		// so there is nothing to read and nothing that can make it more than one.
		ceiling:     func(Params) (int, error) { return 1, nil },
		hostService: func() HostService { return HostNone },
		plan:        func(Params, int, string) Decision { return Decision{} },
	},
	"reflexion": {
		ceiling:     boundedCeiling,
		hostService: func() HostService { return HostNone },
		// 🔴 The only multi-turn strategy that needs no second actor, and that is precisely why it is the
		// one a generated call-site module can run: the critique is produced by ANOTHER TURN OF THE SAME
		// CALL, with the previous answer and the author's declared reflection instruction appended.
		plan: func(p Params, _ int, answer string) Decision {
			if p.StopCondition == stopAnswerMarker && p.AnswerMarker != "" &&
				strings.Contains(answer, p.AnswerMarker) {
				return Decision{}
			}
			return Decision{Continue: true, Append: []Message{
				{Role: "assistant", Content: answer},
				{Role: "user", Content: p.ReflectionPrompt},
			}}
		},
	},
	"react-loop": {
		ceiling:     boundedCeiling,
		hostService: func() HostService { return HostToolInvoker },
		// Reached only when a tool executor was injected — Run refuses otherwise. The loop continues while
		// the host reports the model asked for a tool; the host, not this package, runs it.
		plan: func(p Params, _ int, answer string) Decision {
			if p.StopCondition == stopNoToolCall && !mentionsToolCall(answer) {
				return Decision{}
			}
			return Decision{Continue: true, Append: []Message{{Role: "assistant", Content: answer}}}
		},
	},
	"plan-execute": {
		ceiling:     boundedCeiling,
		hostService: func() HostService { return HostPlanner },
		plan: func(p Params, _ int, answer string) Decision {
			if p.StopCondition == stopPlanComplete && !mentionsRemainingStep(answer) {
				return Decision{}
			}
			return Decision{Continue: true, Append: []Message{{Role: "assistant", Content: answer}}}
		},
	},
	"critic-loop": {
		ceiling:     boundedCeiling,
		hostService: func() HostService { return HostCritic },
		// Reached only when a critic was injected. The critique itself is the HOST's call, never this
		// package's — a generated file that reached a provider would put a credential in the customer's
		// process (decisions.md D-10).
		plan: func(_ Params, _ int, answer string) Decision {
			return Decision{Continue: true, Append: []Message{{Role: "assistant", Content: answer}}}
		},
	},
}

// The closed stop-condition vocabulary, spelled once. These strings are also the registry schema's enum
// values; a test pins them, so a rename in one place cannot silently stop a loop from ever stopping.
const (
	stopAnswerMarker = "answer-marker"
	stopNoToolCall   = "no-tool-call"
	stopPlanComplete = "plan-complete"
	stopMaxTurns     = "max-turns"
)

// boundedCeiling reads max_turns and refuses anything that is not a bound.
//
// 🔴 Zero and negative are rejected rather than defaulted to one. A run with no turns is not a run, and
// silently reading `max_turns: 0` as one would execute a single shot under a loop's config_hash. Above
// TurnCeiling is rejected for the reason the schema rejects it: the ceiling is a policy about blast
// radius, and a runtime that honoured a params value the registry would not seal would be a second,
// looser gate.
func boundedCeiling(p Params) (int, error) {
	if p.MaxTurns < 1 {
		return 0, fmt.Errorf("%w: max_turns is %d; a loop with no turns is not a loop, and reading it as 1 "+
			"would run a single shot under a multi-turn config_hash", ErrInvalidParams, p.MaxTurns)
	}
	if p.MaxTurns > TurnCeiling {
		return 0, fmt.Errorf("%w: max_turns is %d, above the ceiling of %d. The ceiling is a policy about "+
			"how much autonomous tool-calling one node may do, and honouring a value the registry would not "+
			"seal would make this a second and looser gate", ErrInvalidParams, p.MaxTurns, TurnCeiling)
	}
	return p.MaxTurns, nil
}

// mentionsToolCall / mentionsRemainingStep are the runtime's ONLY reads of an answer's content, and they
// are deliberately crude: the host that injected a tool executor is the component that actually knows
// whether a tool was requested, and these exist so a loop has a definition at all when the stop condition
// asks for one. A richer parser here would be this package inventing a provider's response format.
func mentionsToolCall(answer string) bool {
	return strings.Contains(answer, "tool_call") || strings.Contains(answer, "tool_use")
}

func mentionsRemainingStep(answer string) bool {
	return strings.Contains(answer, "next_step") || strings.Contains(answer, "remaining")
}

func strategyFor(name string) (loop, bool) {
	l, ok := loops[name]
	return l, ok
}

// StrategyNames lists the strategies this runtime defines a loop for, sorted. Read by the conformance
// test that pins it against the sealed vocabulary, and by the ErrUnknownStrategy message.
func StrategyNames() []string {
	out := make([]string, 0, len(loops))
	for n := range loops {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// HostServiceFor reports the host service a strategy needs, or HostNone. Exported because the transform's
// call-site refusal asks the same question, and two answers to it would be two things to keep true.
func HostServiceFor(strategy string) HostService {
	l, ok := loops[strategy]
	if !ok {
		return HostNone
	}
	return l.hostService()
}

// StopConditions lists the closed stop-condition vocabulary, sorted. Pinned against the registry schemas
// by a test.
func StopConditions() []string {
	return []string{stopAnswerMarker, stopMaxTurns, stopNoToolCall, stopPlanComplete}
}
