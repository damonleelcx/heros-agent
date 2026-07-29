package registry

import "encoding/json"

// The builtin harness-strategy vocabulary (P18 task 2.4, decisions.md D-1, D-2).
//
// # Why the set is CLOSED and VERSIONED
//
// Identical in mechanism to the memory vocabulary next door, and for an identical reason: a stored
// `config_hash` must still MEAN the same thing months from now, and a stored strategy name is only
// interpretable against the vocabulary it was written under. An open set of free-form scaffold strings
// would make a stored `react-loop` un-interpretable the moment the vocabulary drifted, and open params
// could not be validated at all — a malformed strategy would be discovered when a run reached the node,
// by whoever was unlucky, instead of at seal.
//
// So: exactly five strategies per strategy-set version, each declaring a ParamsSchema, and a cardinality
// assertion (HarnessStrategySetSize) that fails LOUDLY if a sixth is added without a version bump.
//
// # Why these five
//
// A deliberate spread across the scaffold design space — they are the HYPOTHESES the eval harness will
// adjudicate, which is the whole reason harness becomes a Dimension:
//
//	single-shot   the identity. One call, today's implicit default made explicit — the baseline.
//	react-loop    reason and act: alternate model turns with tool calls until the task is done.
//	plan-execute  plan the steps first, then execute them — separates deciding from doing.
//	reflexion     answer, then re-answer against a declared reflection instruction until satisfied.
//	critic-loop   a generator paired with a SEPARATE critic model (the salvaged pattern from the
//	              removed runtime harness, carried as data rather than resurrected as code).
//
// 🚫 None of these is a running service, and this file makes no claim that one is. A strategy here is a
// DESCRIPTION — a name plus validated params that a `config_hash` can pin. What EXECUTES the loop is
// internal/harnessruntime, and what materializes it at a call site is the transform's harness rewriter;
// both are deliberately elsewhere, so that a sealed vocabulary carries no dependency on either.
//
// # The one thing every multi-turn strategy MUST declare
//
// 🔴 A bounded `max_turns`. NFR5 and the standing L1 blast-radius note both turn on it: no strategy may
// express an unbounded loop, so `max_turns` is REQUIRED for every strategy that runs more than one turn,
// bounded above by MaxTurnsCeiling in the schema itself rather than by a check someone can forget.

// HarnessStrategySetVersion is the version of the builtin strategy vocabulary. A stored harness entry's
// strategy name is interpretable only against this version — bump it when the set changes, exactly as
// MemoryStrategySetVersion is bumped when the memory vocabulary changes.
const HarnessStrategySetVersion = "1.0.0"

// HarnessStrategySetSize is the fixed cardinality of the vocabulary at HarnessStrategySetVersion. It is
// asserted against len(BuiltinHarnessStrategies()) in a test: a sixth strategy added without a version
// bump fails loudly rather than silently changing what a stored strategy name means.
const HarnessStrategySetSize = 5

// StrategySingleShot is the identity strategy. A node whose strategy is `single-shot` runs exactly one
// call — what every node already does — and its resolved projection is EMPTY, byte-identical to a node
// that declares no harness at all (decisions.md D-3, D-8). The constant exists so that identity is
// written once and compared everywhere, rather than being a string literal repeated across resolve, the
// transform, discovery, and the console.
const StrategySingleShot = "single-shot"

// MaxTurnsCeiling is the hard upper bound on any strategy's declared `max_turns`, and it is ONE constant
// rather than a per-strategy guess (PRD §14 open question 3).
//
// 🔴 It is expressed in each ParamsSchema's `maximum`, so the bound is enforced by the same schema check
// that rejects `{"max_turns":"lots"}` — at seal, before an entry acquires a version_id. A bound enforced
// only by a runtime check would be a bound that a spec can be authored against and then violate.
//
// The value is a policy choice, not a technical limit: sixteen turns of autonomous tool-calling is
// already a materially larger blast radius than one (see the standing L1 note in decisions.md), and a
// ceiling a user has to argue past is the point of having one.
const MaxTurnsCeiling = 16

// RetryBudgetCeiling bounds a strategy's retry budget, for the same reason and by the same mechanism.
// Retries multiply turns, so an unbounded budget would defeat MaxTurnsCeiling from the side.
const RetryBudgetCeiling = 8

// HarnessStrategy is one harness strategy in the closed builtin set.
//
// It mirrors MemoryStrategy deliberately: a Name the entry's spec selects, a ParamsSchema its params must
// satisfy so `{"max_turns":"lots"}` is rejected when the entry is REGISTERED rather than when a run
// reaches the node, plus a Title and Description.
//
// 🔴 Title/Description are part of the interface, not console decoration. The wire name, the strategy
// entity, and the human label are three separate layers, and collapsing them is how a rename becomes a
// breaking change to stored hashes. `react-loop` is the wire name forever; "Reason and act" is what a
// person reads, and it may be reworded freely.
//
// 🚫 There is no Run/Plan/Step method here, and its absence is deliberate rather than incomplete. The
// loop semantics live in internal/harnessruntime, keyed by strategy NAME, with a conformance test
// asserting the sealed vocabulary and the runtime name exactly the same set. Putting them here would drag
// a host-service dependency into every consumer of a sealed definition — the correction the memory axis
// already had to make once (p18-memory-runtime tasks 1.6), and there is no reason to re-learn it.
type HarnessStrategy interface {
	Name() string
	Title() string
	Description() string
	ParamsSchema() json.RawMessage
}

// BuiltinHarnessStrategies returns every builtin harness strategy, in a stable order. Adding one is a new
// entry here AND a HarnessStrategySetSize bump AND a HarnessStrategySetVersion bump — the cardinality
// assertion exists to make skipping the second two impossible to do quietly.
func BuiltinHarnessStrategies() []HarnessStrategy {
	return []HarnessStrategy{
		SingleShotHarness{},
		ReactLoopHarness{},
		PlanExecuteHarness{},
		ReflexionHarness{},
		CriticLoopHarness{},
	}
}

// ── single-shot — the identity ───────────────────────────────────────────────────────────────────

// SingleShotHarness is one call, named. It is a real member of the vocabulary rather than a null, because
// "this node deliberately runs exactly one turn" is a configuration a user may want to state, compare and
// pin — and because the baseline every other strategy is scored against has to be expressible.
type SingleShotHarness struct{}

func (SingleShotHarness) Name() string  { return StrategySingleShot }
func (SingleShotHarness) Title() string { return "Single shot" }
func (SingleShotHarness) Description() string {
	return "One model call and done — exactly what the node does today, named explicitly. The baseline " +
		"the other strategies are measured against, and the only one that costs what the un-rewritten " +
		"call costs."
}

// ParamsSchema takes no params and says so precisely.
//
// 🔴 `max_turns` is INEXPRESSIBLE here, not defaulted to 1 (FR3). `{"max_turns":3}` on `single-shot` is a
// loud error at registration rather than a param silently ignored — the exact mistake someone makes when
// they think they selected a different strategy, and one that would otherwise be discovered only by
// reading a bill.
func (SingleShotHarness) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false}`)
}

// ── react-loop — reason and act ──────────────────────────────────────────────────────────────────

// ReactLoopHarness alternates model turns with tool calls until the model stops asking for one. The
// classic agent scaffold, and the one whose blast radius is most visibly larger than a single shot's:
// every extra turn is another opportunity for the model to call a tool.
type ReactLoopHarness struct{}

func (ReactLoopHarness) Name() string  { return "react-loop" }
func (ReactLoopHarness) Title() string { return "Reason and act" }
func (ReactLoopHarness) Description() string {
	return "The model alternates thinking with calling tools until it stops asking for one, or the turn " +
		"ceiling is reached. Strongest on tasks that need to look something up mid-answer; every extra " +
		"turn is another tool-calling opportunity, so the ceiling is the containment."
}

func (ReactLoopHarness) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_turns":{"type":"integer","minimum":2,"maximum":16,"description":"The hard turn ceiling. Reaching it terminates the run and is recorded."},
			"stop_condition":{"type":"string","enum":["no-tool-call","max-turns"],"description":"What ends the loop before the ceiling."},
			"retry_budget":{"type":"integer","minimum":0,"maximum":8,"description":"How many failed turns may be retried before the run gives up."}
		},
		"required":["max_turns","stop_condition"],
		"additionalProperties":false
	}`)
}

// ── plan-execute — decide first, then do ─────────────────────────────────────────────────────────

// PlanExecuteHarness plans the steps in one turn and executes them in the turns that follow. It separates
// deciding from doing, which is what makes it stronger than react-loop on tasks whose steps interact —
// and weaker on tasks whose right next step only becomes visible after the previous one.
type PlanExecuteHarness struct{}

func (PlanExecuteHarness) Name() string  { return "plan-execute" }
func (PlanExecuteHarness) Title() string { return "Plan, then execute" }
func (PlanExecuteHarness) Description() string {
	return "One turn produces a plan; the turns after it execute the steps. Separating deciding from " +
		"doing helps when the steps interact, and hurts when the right next step only becomes visible " +
		"after the previous one has run."
}

func (PlanExecuteHarness) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_turns":{"type":"integer","minimum":2,"maximum":16,"description":"The hard turn ceiling, counting the planning turn."},
			"stop_condition":{"type":"string","enum":["plan-complete","max-turns"],"description":"What ends the loop before the ceiling."},
			"retry_budget":{"type":"integer","minimum":0,"maximum":8,"description":"How many failed steps may be retried before the run gives up."}
		},
		"required":["max_turns","stop_condition"],
		"additionalProperties":false
	}`)
}

// ── reflexion — answer, then re-answer ───────────────────────────────────────────────────────────

// ReflexionHarness re-asks the SAME call with the previous answer and a declared reflection instruction
// appended, until the stop condition is satisfied or the ceiling is reached.
//
// 🔴 It is the only multi-turn strategy that needs no second actor, and that is why it is the only one a
// generated call-site module can run (design.md Addendum Decision 10): the critique is produced by
// another turn of the call the author already wrote, not by a second model the generated code would have
// to reach. `reflection_prompt` is REQUIRED because a reflexion loop with no instruction is just the same
// question asked twice.
type ReflexionHarness struct{}

func (ReflexionHarness) Name() string  { return "reflexion" }
func (ReflexionHarness) Title() string { return "Answer and revise" }
func (ReflexionHarness) Description() string {
	return "The node answers, then answers again with its previous attempt and your reflection " +
		"instruction appended, until the stop condition is met or the ceiling is reached. Needs no " +
		"second model and no tool, which is why it is the multi-turn strategy that can be applied to " +
		"source today."
}

// ParamsSchema requires the reflection instruction and the stop condition.
//
// `answer_marker` is optional in the schema and REQUIRED by the cross-field check in
// ValidateHarnessParams when `stop_condition` is `answer-marker` — a dependency JSON Schema can express
// but which reads far more clearly, and fails far more usefully, as a named Go check.
func (ReflexionHarness) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_turns":{"type":"integer","minimum":2,"maximum":16,"description":"The hard turn ceiling, counting the first answer."},
			"stop_condition":{"type":"string","enum":["answer-marker","max-turns"],"description":"What ends the loop before the ceiling."},
			"answer_marker":{"type":"string","minLength":1,"description":"The text whose presence in an answer ends the loop. Required when stop_condition is answer-marker."},
			"reflection_prompt":{"type":"string","minLength":1,"description":"The instruction appended alongside the previous answer on every turn after the first."}
		},
		"required":["max_turns","stop_condition","reflection_prompt"],
		"additionalProperties":false
	}`)
}

// ── critic-loop — a generator and a SEPARATE critic ──────────────────────────────────────────────

// CriticLoopHarness pairs the node's own call with a separate critic model that judges each answer.
//
// 🔴 `critic_model_ref` is REQUIRED and is a REFERENCE, not a model name. Which model criticised is part
// of what makes the result reproducible: the same loop judged by a different critic is a different
// configuration that would score differently, so a `config_hash` that did not pin it would claim two
// different computations were one. The ref is resolved against the MODEL registry at registration
// (RegisterHarness), so an entry naming a critic nobody published never acquires a version_id.
type CriticLoopHarness struct{}

func (CriticLoopHarness) Name() string  { return "critic-loop" }
func (CriticLoopHarness) Title() string { return "Generate and critique" }
func (CriticLoopHarness) Description() string {
	return "The node answers and a separate critic model judges the answer, until the critic accepts or " +
		"the ceiling is reached. The only strategy that pins a second model, so its result is only as " +
		"reproducible as the critic it names."
}

func (CriticLoopHarness) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_turns":{"type":"integer","minimum":2,"maximum":16,"description":"The hard turn ceiling, counting each generate/critique pair as one turn."},
			"critic_model_ref":{"type":"string","minLength":1,"description":"The model registry version_id of the critic. Pinned because a loop judged by a different critic is a different configuration."},
			"retry_budget":{"type":"integer","minimum":0,"maximum":8,"description":"How many rejected answers may be retried before the run gives up."}
		},
		"required":["max_turns","critic_model_ref"],
		"additionalProperties":false
	}`)
}
