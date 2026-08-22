package registry

import "encoding/json"

// The builtin LOOP-strategy vocabulary (P34 FR2, ADR-014, decisions.md D-34.1).
//
// # This vocabulary is RELOCATED, not extended
//
// 🔴 The five strategies and the four stop conditions here are the ones `harness_builtins.go` already
// sealed. P34 adds no strategy and no stop condition — it moves the ITERATION POLICY onto its own axis
// so that an operator tightening a spend ceiling and an engineer changing a reflection prompt stop
// editing the same registry kind. `TestLoopVocabularyIsTheHarnessVocabularyRelocated` pins the two sets
// equal, so "relocated, not extended" is mechanical rather than a claim in a comment.
//
// # What each strategy carries HERE that it does not carry on the harness axis
//
// The split line is **imposed vs chosen** (design D2, decisions.md D-34.1). A loop entry carries only
// what an AUTHOR chooses:
//
//	max_turns          a chosen value, bounded above by the envelope's ceiling at RESOLVE
//	stop_condition     what ends the loop before the ceiling
//	answer_marker      the text whose presence ends a reflexion loop
//	reflection_prompt  the instruction appended on every turn after the first
//	critic_model_ref   which model judges — pinned, because a different critic is a different computation
//
// 🚫 `retry_budget` is NOT here. Retries multiply turns, which makes a retry budget a statement about
// blast radius — imposed, not chosen — so it sits on the envelope beside the ceilings it would otherwise
// defeat from the side. A loop entry declaring one is refused at seal by `additionalProperties:false`,
// which is a loud error at registration rather than a param silently ignored.
//
// 🚫 There is no `max_turns` ceiling expressed in these schemas beyond the platform's own
// MaxTurnsCeiling. The ENVELOPE's ceiling is checked at resolve, not at seal, because a loop entry is
// authored independently of the envelope it will run under — baking a specific envelope's ceiling into a
// sealed entry would make the entry un-reusable and would re-hash it every time a policy moved.

// LoopStrategySetVersion is the version of the builtin loop vocabulary. A stored loop entry's strategy
// name is interpretable only against this version.
//
// 🔴 It starts at 1.0.0 rather than inheriting HarnessStrategySetVersion. The two sets are equal TODAY
// and are free to diverge — the harness set has already gained `envelope`, which is not a loop — so
// sharing one version string would make a bump on one axis silently claim something about the other.
const LoopStrategySetVersion = "1.0.0"

// LoopStrategySetSize is the fixed cardinality at LoopStrategySetVersion, asserted against
// len(BuiltinLoopStrategies()) by a test: a sixth strategy added without a version bump fails loudly
// rather than silently changing what a stored strategy name means.
const LoopStrategySetSize = 5

// LoopStrategy is one loop strategy in the closed builtin set. It mirrors HarnessStrategy exactly —
// name, human labels, params schema, no Run method — because the loop semantics live in
// internal/harnessruntime, keyed by strategy NAME, and dragging a host-service dependency into every
// consumer of a sealed definition is a correction this codebase has already had to make once.
type LoopStrategy interface {
	Name() string
	Title() string
	Description() string
	ParamsSchema() json.RawMessage
}

// BuiltinLoopStrategies returns every builtin loop strategy, in a stable order. Adding one is a new
// entry here AND a LoopStrategySetSize bump AND a LoopStrategySetVersion bump.
func BuiltinLoopStrategies() []LoopStrategy {
	return []LoopStrategy{
		SingleShotLoop{},
		ReactLoop{},
		PlanExecuteLoop{},
		ReflexionLoop{},
		CriticLoop{},
	}
}

// ── single-shot — the identity ───────────────────────────────────────────────────────────────────

// SingleShotLoop is one call, named. A real member rather than a null, because "this node deliberately
// runs exactly one turn" is a configuration a user may want to state, compare and pin.
type SingleShotLoop struct{}

func (SingleShotLoop) Name() string  { return StrategySingleShot }
func (SingleShotLoop) Title() string { return "Single shot" }
func (SingleShotLoop) Description() string {
	return "One model call and done — exactly what the node does today, named explicitly. The baseline " +
		"the other strategies are measured against, and the only one that costs what the un-rewritten " +
		"call costs."
}

// ParamsSchema takes no params and says so precisely.
//
// 🔴 `max_turns` is INEXPRESSIBLE here, not defaulted to 1 (P34 task 3.5). `{"max_turns":3}` on
// `single-shot` is a loud error at registration rather than a param silently ignored — the exact mistake
// someone makes when they think they selected a different strategy, and one that would otherwise be
// discovered only by reading a bill. Nothing can make this loop run more than one turn.
func (SingleShotLoop) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false}`)
}

// ── react-loop — reason and act ──────────────────────────────────────────────────────────────────

// ReactLoop alternates model turns with tool calls until the model stops asking for one.
type ReactLoop struct{}

func (ReactLoop) Name() string  { return "react-loop" }
func (ReactLoop) Title() string { return "Reason and act" }
func (ReactLoop) Description() string {
	return "The model alternates thinking with calling tools until it stops asking for one, or the turn " +
		"ceiling is reached. Strongest on tasks that need to look something up mid-answer; every extra " +
		"turn is another tool-calling opportunity, so the ceiling is the containment."
}

func (ReactLoop) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_turns":{"type":"integer","minimum":2,"maximum":16,"description":"The turn count you are choosing, checked against the envelope's ceiling at resolve."},
			"stop_condition":{"type":"string","enum":["no-tool-call","max-turns"],"description":"What ends the loop before the ceiling."}
		},
		"required":["max_turns","stop_condition"],
		"additionalProperties":false
	}`)
}

// ── plan-execute — decide first, then do ─────────────────────────────────────────────────────────

// PlanExecuteLoop plans the steps in one turn and executes them in the turns that follow.
type PlanExecuteLoop struct{}

func (PlanExecuteLoop) Name() string  { return "plan-execute" }
func (PlanExecuteLoop) Title() string { return "Plan, then execute" }
func (PlanExecuteLoop) Description() string {
	return "One turn produces a plan; the turns after it execute the steps. Separating deciding from " +
		"doing helps when the steps interact, and hurts when the right next step only becomes visible " +
		"after the previous one has run."
}

func (PlanExecuteLoop) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_turns":{"type":"integer","minimum":2,"maximum":16,"description":"The turn count you are choosing, counting the planning turn."},
			"stop_condition":{"type":"string","enum":["plan-complete","max-turns"],"description":"What ends the loop before the ceiling."}
		},
		"required":["max_turns","stop_condition"],
		"additionalProperties":false
	}`)
}

// ── reflexion — answer, then re-answer ───────────────────────────────────────────────────────────

// ReflexionLoop re-asks the SAME call with the previous answer and a declared reflection instruction
// appended. The only multi-turn strategy that needs no second actor, which is why it is the only one a
// generated call-site module can run.
type ReflexionLoop struct{}

func (ReflexionLoop) Name() string  { return "reflexion" }
func (ReflexionLoop) Title() string { return "Answer and revise" }
func (ReflexionLoop) Description() string {
	return "The node answers, then answers again with its previous attempt and your reflection " +
		"instruction appended, until the stop condition is met or the ceiling is reached. Needs no " +
		"second model and no tool, which is why it is the multi-turn strategy that can be applied to " +
		"source today."
}

// ParamsSchema requires the reflection instruction and the stop condition. `answer_marker` is optional
// here and REQUIRED by the cross-field check in ValidateLoopParams when `stop_condition` is
// `answer-marker` — a dependency JSON Schema can express and which reads far more clearly, and fails
// far more usefully, as a named Go check.
func (ReflexionLoop) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_turns":{"type":"integer","minimum":2,"maximum":16,"description":"The turn count you are choosing, counting the first answer."},
			"stop_condition":{"type":"string","enum":["answer-marker","max-turns"],"description":"What ends the loop before the ceiling."},
			"answer_marker":{"type":"string","minLength":1,"description":"The text whose presence in an answer ends the loop. Required when stop_condition is answer-marker."},
			"reflection_prompt":{"type":"string","minLength":1,"description":"The instruction appended alongside the previous answer on every turn after the first."}
		},
		"required":["max_turns","stop_condition","reflection_prompt"],
		"additionalProperties":false
	}`)
}

// ── critic-loop — a generator and a SEPARATE critic ──────────────────────────────────────────────

// CriticLoop pairs the node's own call with a separate critic model that judges each answer.
//
// 🔴 `critic_model_ref` is REQUIRED and is a REFERENCE, not a model name: which model criticised is part
// of what makes the result reproducible, so a `config_hash` that did not pin it would claim two
// different computations were one. The ref is resolved against the MODEL registry at registration.
type CriticLoop struct{}

func (CriticLoop) Name() string  { return "critic-loop" }
func (CriticLoop) Title() string { return "Generate and critique" }
func (CriticLoop) Description() string {
	return "The node answers and a separate critic model judges the answer, until the critic accepts or " +
		"the ceiling is reached. The only strategy that pins a second model, so its result is only as " +
		"reproducible as the critic it names."
}

func (CriticLoop) ParamsSchema() json.RawMessage {
	return json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_turns":{"type":"integer","minimum":2,"maximum":16,"description":"The turn count you are choosing, counting each generate/critique pair as one turn."},
			"critic_model_ref":{"type":"string","minLength":1,"description":"The model registry version_id of the critic. Pinned because a loop judged by a different critic is a different configuration."}
		},
		"required":["max_turns","critic_model_ref"],
		"additionalProperties":false
	}`)
}
