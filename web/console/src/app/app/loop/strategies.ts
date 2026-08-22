/**
 * strategies.ts mirrors the platform's closed harness-strategy vocabulary
 * (`registry.BuiltinHarnessStrategies()`, `internal/registry/harness_builtins.go`) and the engine's
 * per-cell coverage (`transform.CoverageFor("harness")`).
 *
 * # Why a mirror and not a fetch
 *
 * The vocabulary is a property of the BUILD, not of a tenant — no entitlement, plan, or role can change
 * it. Mirroring it here lets the surface render its full explanation without a live platform, which is
 * what the browser-checkable preview needs.
 *
 * 🔴 A mirror with no gate is a second source of truth, and its failure is silent: the page keeps
 * rendering, confidently, a vocabulary that has moved. `tests/harness.test.mjs` reads the engine's own Go
 * source and asserts this file agrees with it, strategy for strategy — that test is the gate, and it is
 * the reason this comment is not just a promise.
 */

export type HarnessStrategy = {
  /** The wire name stored in the registry entry. Stable forever; a rename orphans stored hashes. */
  strategy: string;
  /** The human layer, free to be reworded because it is never hashed. */
  title: string;
  /** What this scaffold does, and what it trades — the thing a person actually chooses between. */
  tradeoff: string;
  /** The parameters the schema bounds, as {name, hint} pairs the form renders. */
  params: { name: string; hint: string; required: boolean }[];
  /**
   * maxTurnCeiling is the largest turn count this strategy's schema permits; 1 for the identity.
   *
   * 🔴 It IS the cost. A surface that could not state it would be asking a user for a blank cheque, so
   * it is a first-class field rather than something the form derives from a JSON Schema.
   */
  maxTurnCeiling: number;
  /**
   * identity marks `single-shot`. It is the one strategy that is never refused, in any language, because
   * one turn is exactly the un-rewritten call site — and selecting it with no params is indistinguishable
   * from clearing.
   */
  identity?: boolean;
  /**
   * hostService names the second actor this strategy needs and that a CALL SITE cannot supply. Non-empty
   * means it is refused everywhere, permanently — not pending.
   */
  hostService?: string;
};

export const HARNESS_STRATEGIES: HarnessStrategy[] = [
  {
    strategy: "single-shot",
    title: "Single shot",
    tradeoff:
      "One model call and done — exactly what the node does today, named explicitly. The baseline the other four are measured against, and the only one that costs what the un-rewritten call costs.",
    params: [],
    maxTurnCeiling: 1,
    identity: true,
  },
  {
    strategy: "critic-loop",
    title: "Generate and critique",
    tradeoff:
      "The node answers and a separate critic model judges the answer, until the critic accepts or the ceiling is reached. The only strategy that pins a second model, so its result is only as reproducible as the critic it names.",
    params: [
      { name: "max_turns", hint: "The hard turn ceiling, counting each generate/critique pair as one turn.", required: true },
      {
        name: "critic_model_ref",
        hint: "The model registry version_id of the critic. Pinned because a loop judged by a different critic is a different configuration.",
        required: true,
      },
      { name: "retry_budget", hint: "How many rejected answers may be retried before the run gives up.", required: false },
    ],
    maxTurnCeiling: 16,
    hostService: "a separate critic model",
  },
  {
    strategy: "plan-execute",
    title: "Plan, then execute",
    tradeoff:
      "One turn produces a plan; the turns after it execute the steps. Separating deciding from doing helps when the steps interact, and hurts when the right next step only becomes visible after the previous one has run.",
    params: [
      { name: "max_turns", hint: "The hard turn ceiling, counting the planning turn.", required: true },
      { name: "stop_condition", hint: "plan-complete, or max-turns.", required: true },
      { name: "retry_budget", hint: "How many failed steps may be retried before the run gives up.", required: false },
    ],
    maxTurnCeiling: 16,
    hostService: "a planner and a step executor",
  },
  {
    strategy: "react-loop",
    title: "Reason and act",
    tradeoff:
      "The model alternates thinking with calling tools until it stops asking for one, or the turn ceiling is reached. Strongest on tasks that need to look something up mid-answer; every extra turn is another tool-calling opportunity, so the ceiling is the containment.",
    params: [
      { name: "max_turns", hint: "The hard turn ceiling. Reaching it terminates the run and is recorded.", required: true },
      { name: "stop_condition", hint: "no-tool-call, or max-turns.", required: true },
      { name: "retry_budget", hint: "How many failed turns may be retried before the run gives up.", required: false },
    ],
    maxTurnCeiling: 16,
    hostService: "a tool executor",
  },
  {
    strategy: "reflexion",
    title: "Answer and revise",
    tradeoff:
      "The node answers, then answers again with its previous attempt and your reflection instruction appended, until the stop condition is met or the ceiling is reached. Needs no second model and no tool, which is why it is the multi-turn strategy that can be applied to source today.",
    params: [
      { name: "max_turns", hint: "The hard turn ceiling, counting the first answer.", required: true },
      { name: "stop_condition", hint: "answer-marker, or max-turns.", required: true },
      { name: "answer_marker", hint: "The text whose presence in an answer ends the loop. Required when stop_condition is answer-marker.", required: false },
      {
        name: "reflection_prompt",
        hint: "The instruction appended alongside the previous answer on every turn after the first.",
        required: true,
      },
    ],
    maxTurnCeiling: 16,
  },
];

/** The hard cap the registry schema enforces on any strategy's `max_turns`. */
export const MAX_TURNS_CEILING = 16;

/** The languages whose call sites can drive the emitted harness module. */
export const MATERIALIZED_LANGUAGES = ["python"] as const;

/**
 * THE BOUNDARY, mirrored from `transform.CoverageFor("harness")`.
 *
 * 🔴 It is PER CELL, and a single sentence for the axis would be wrong in BOTH directions: it would tell
 * a Python user that `reflexion` is unavailable, and it would tell every user that `react-loop` is merely
 * pending. Three different answers live here, and telling them apart is the whole value of the read.
 */
export const HARNESS_BOUNDARY = {
  /** The identity applies everywhere: one turn IS the un-rewritten call site. */
  identityAppliesEverywhere: true,
  /** `reflexion` is the only multi-turn strategy a language can materialize, and only where it can read an answer. */
  materializedIn: MATERIALIZED_LANGUAGES,
  /**
   * 🔴 Permanent, not owed. Three of the five need a second actor a CALL SITE has nowhere to inject, and
   * the generated module makes no provider call and dispatches no tool by design — a generated file that
   * reached a provider would put your credential in your own process, spent on turns you did not write.
   */
  permanentlyRefused: ["react-loop", "plan-execute", "critic-loop"] as const,
  /**
   * 🔴 Go's refusal of `reflexion` is permanent too, and for a different reason: deciding whether to take
   * another turn means reading the ANSWER's text, and a Go response is your SDK's own type. Generated code
   * would have to import your SDK to read a field off it. Python's responses are message-like.
   */
  answerBlindLanguages: ["go"] as const,
  missingArtifact: "that language's harness module and its call-site rewriter",
  reason:
    "The harness runtime has landed. Python call sites materialize the one multi-turn strategy that needs no second actor; a remaining language is waiting for the emitted module a rewritten call site would drive, and the rewriter that emits it.",
  /** Modeling is never refused; only materialization is. This is what keeps the control live. */
  authorableAnyway: true,
  /**
   * The preconditions a materializing cell still carries. A reader who meets these at apply time instead
   * of here has been told half the truth.
   */
  preconditions: [
    "The call site must write its message list. A loop takes another turn by APPENDING the previous answer to that list, so a call that passes **kwargs has nothing to append to — and the only loop we could emit would re-ask the identical question, which is a single shot at N times the price.",
    "Your program must be able to read an answer's text. The generated module reads message-like responses and raises rather than guessing; supply a reader with agentharness.set_answer_reader if your SDK's response is not one.",
    "A heavier scaffold costs more on EVERY case, including the ones that already pass. Whether that buys enough task_success to be worth it is decided by verification on held-out cases, never by the selection.",
  ],
} as const;

/**
 * costWarningFor is the sentence a reader must see BEFORE choosing a heavier scaffold.
 *
 * 🔴 It mirrors `authoring.harnessCostWarning` — the same two halves, in the same order: the multiplier
 * (a fact, statable before anything runs) and who decides whether it is worth it (verification, not this
 * control). Stating the first and withholding the second is what keeps a picker from reading as advice.
 */
export function costWarningFor(ceiling: number): string {
  if (ceiling <= 1) return "";
  return `This scaffold may run up to ${ceiling} turns, so it can multiply this node's per-run cost and latency by up to ${ceiling} — on every case, including the ones that already pass. Whether that buys enough task_success to be worth it is decided by verification on held-out cases, not by this selection.`;
}
