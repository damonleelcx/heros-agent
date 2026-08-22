import type {
  ConversationFailureClass,
  ConversationFindingState,
  ConversationPhase,
  ConversationStepState,
  StopReason,
} from "@/lib/types.generated";

/**
 * conversationCopy.ts is every user-facing string the conversational surface renders (P31 §7).
 *
 * # Why the copy is a module and not inline in the components
 *
 * Because the hardest requirements in this phase are COPY requirements, and they are invisible when
 * they are spread across six components:
 *
 *   §7.1  `not_measured` must read as a STATE, not as an omission.
 *   §7.5  a budget-stopped run must read as a state with a NEXT ACTION — not a failure, not a completion.
 *   §7.6  `skipped` must name the reason. "Skipped" alone is the omission problem with a label on it.
 *   §7.2  an un-approvable request must name why.
 *
 * Each of those is one sentence somebody could weaken in a hurry without noticing what it cost. Here
 * they are together, next to the argument, where a reviewer reads them as a set.
 *
 * # 🔴 The noun dictionary (§7.4)
 *
 * `workflow`, `node`, `axis`, `proposal`, `run`. Exactly as the rest of the product uses them, and
 * `tests/conversation-copy.test.mjs` fails on the near-synonyms this surface invites — "pipeline",
 * "agent" for a workflow, "step" for a node, "dimension" for an axis, "suggestion" for a proposal,
 * "session"/"job" for a run. A chat surface is where product vocabulary goes to drift, because prose
 * makes a synonym feel like variety rather than like a second name for one thing.
 *
 * # What is deliberately absent
 *
 * An apology. "Sorry, I couldn't…" is the register a chat surface defaults to, and it converts every
 * one of the distinctions above into the same sentence. A refusal here states what happened and what
 * to do; it does not perform regret.
 */

/** PHASE_COPY names each phase in the words a person would use, not the words the code uses. */
export const PHASE_COPY: Record<ConversationPhase, { label: string; detail: string }> = {
  understand: { label: "Understanding", detail: "working out which surface answers this" },
  plan: { label: "Planning", detail: "deciding the steps and what they may spend" },
  act: { label: "Working", detail: "reading the surfaces this question needs" },
  verify: { label: "Verifying", detail: "checking each claim against the artifact behind it" },
  respond: { label: "Answering", detail: "reconciling every step that was planned" },
};

/**
 * FINDING_STATE_COPY is §7.1's requirement, and `not_measured` is the line that matters.
 *
 * A conversational surface makes absence feel like an answer: silence about a surface reads as "nothing
 * wrong with it". So the copy for `not_measured` says what IS true — a measurement was not taken, and
 * here is what was missing — and never apologises for it, because an apology reads as a failure and
 * this is a state.
 *
 * 🚫 Not "no data". Not "unavailable". Not "n/a". Each of those reads as an absence rather than as a
 * fact about what the platform was given.
 */
export const FINDING_STATE_COPY: Record<ConversationFindingState, { label: string; lede: string }> = {
  measured: { label: "Measured", lede: "" },
  not_measured: {
    label: "Not measured",
    lede: "This was examined and no measurement could be taken. What was missing:",
  },
  refused: {
    label: "Refused",
    lede: "The engine declined to answer here, in its own words:",
  },
  stale: {
    label: "Stale",
    lede: "Replayed from an earlier analysis. It describes this revision, not your current one:",
  },
};

/**
 * STEP_STATE_COPY is §7.6. Every state except `done` renders its reason beside it, and the label alone
 * is never the whole message — `skipped` with nothing after it is exactly the omission this vocabulary
 * exists to replace.
 */
export const STEP_STATE_COPY: Record<ConversationStepState, { label: string; needsReason: boolean }> = {
  done: { label: "Done", needsReason: false },
  skipped: { label: "Skipped", needsReason: true },
  refused: { label: "Refused", needsReason: true },
  not_measured: { label: "Not measured", needsReason: true },
};

/**
 * STOP_COPY is §7.5, and it is the hardest copy in the phase.
 *
 * A run stopped by its token budget is **not a failure** — nothing went wrong — and it is **not a
 * completion** — it did not finish what it planned. Both of the obvious registers are wrong, and the
 * one that is right is *a state with a next action*: here is what stopped, here is what you do about it.
 *
 * So every limit's copy has a `next`. A stop reason with no next action is the sentence that makes a
 * person wonder whether the product is broken.
 */
export const STOP_COPY: Record<StopReason, { label: string; body: string; next: string }> = {
  satisfied: {
    label: "Finished",
    // 🔴 Task 4.13: a run that finished normally SAYS so. `satisfied` is a stated outcome rather than
    // the absence of a limit, because "no error shown" and "it completed" look identical otherwise.
    //
    // 🚫 It does NOT say "every planned step ran", which is what it said until a browser run showed it
    // beside three `not_measured` rows. `satisfied` means the run hit no LIMIT; it says nothing about
    // what was measured, and a card that asserts completeness above a reconciliation showing none is
    // the "plausible short answer" §9.1 is about — the tone of a full answer over fewer facts.
    //
    // What actually happened is in the reconciliation below it and in the server's own summary, which
    // the card renders beside this line rather than at the bottom.
    body: "The run finished without reaching any of its limits.",
    next: "",
  },
  ceiling: {
    label: "Stopped at the turn ceiling",
    body: "The turn reached the number of agent turns it declared before it started.",
    next: "Ask a narrower question, or ask about one surface at a time.",
  },
  "single-shot": {
    label: "Finished in one turn",
    body: "This question needed a single turn by definition.",
    next: "",
  },
  "token-budget": {
    label: "Stopped at the token budget",
    body: "The turn spent the tokens its plan declared before it finished every step.",
    next: "The steps below that did not run are marked. Ask about one of them directly and it will have the whole budget.",
  },
  "tool-call-ceiling": {
    label: "Stopped at the tool-call ceiling",
    body: "The turn made as many reads as its plan declared it might.",
    next: "Ask about one surface at a time; each question gets its own ceiling.",
  },
  "wall-clock": {
    label: "Stopped at the time limit",
    body: "The turn reached its declared wall-clock limit. It had budget left, which usually means something it was reading was slow.",
    next: "Try again — and if it stops here twice, the surface it was reading is the thing to look at, not the question.",
  },
  cancelled: {
    label: "Cancelled",
    body: "You stopped this run.",
    next: "Anything it had already found is above and stays there.",
  },
  // 🔴 P34. The NODE's money ceiling, imposed by its execution envelope — deliberately worded so it is
  // not confused with `token-budget` above, which is a TURN's own token allowance. A reader who
  // conflated them would go and raise a per-turn number that is not what ran out, and the two `next`
  // lines are therefore two different actions in two different places.
  //
  // 🚫 The register is a STATE WITH A NEXT ACTION, not a failure: a run that stopped on budget produced
  // a real, partial answer under a known configuration, and calling it an error would file it beside
  // "the provider was down".
  "spend-ceiling": {
    label: "Stopped at the spend ceiling",
    body: "This node reached the amount its execution envelope allows it to spend in one run. What it had already produced is kept.",
    next: "The answer above is partial and says where it stopped. Raising the ceiling is a change to this node's envelope, which is set on the Harness surface — not something this question can ask for.",
  },
};

/**
 * FAILURE_COPY keeps P9's three classes three, inside a surface whose natural tendency is to flatten
 * every one of them into the same apologetic sentence (FR4).
 *
 * The test of this table is whether the three `next` lines are three different actions. They are: mount
 * something, check an identifier, retry. A person who gets the wrong one spends their afternoon on the
 * wrong question.
 */
export const FAILURE_COPY: Record<ConversationFailureClass, { label: string; next: string }> = {
  not_mounted: {
    label: "Not available in this deployment",
    next: "This capability is not installed here. Whoever runs this deployment can add it.",
  },
  not_found: {
    label: "Not found",
    next: "Check the identifier. This is not a business state — nothing here is empty; the subject does not exist.",
  },
  transport: {
    label: "Could not reach the platform",
    next: "The request did not complete. Retry — nothing was recorded.",
  },
};

/** ASK_TITLE and ASK_LEDE name the surface. `workflow` and `run`, from the noun dictionary. */
export const ASK_TITLE = "Ask";
export const ASK_LEDE =
  "Ask about a workflow in English. Every finding carries the evidence behind it, and anything that " +
  "would change your repository asks you first.";

/**
 * PERSISTENCE_FALLBACK is §7.3's boundary and task 4.10's visible consequence, for the case where the
 * platform sent no `persistence` string of its own.
 *
 * 🔴 A FALLBACK, not the source. The platform sends this sentence with every new conversation
 * (`conversationView.persistence`), so the day ADR-015's Q1 is revisited the console is not still
 * promising the old behaviour from a literal. This is what renders if that field is ever empty.
 */
export const PERSISTENCE_FALLBACK =
  "This conversation lives with its run. Reloading resumes the run and replays its messages; it does " +
  "not restore a history spanning runs.";

/**
 * NO_COMPOSITE_SCORE is program ruling R4, stated as a deliberate choice rather than discovered as a
 * gap (§7.3).
 *
 * Sales operations' note is exact: this is a differentiator when stated confidently and a weakness when
 * a customer finds it themselves. So it is on the surface, in the empty state, before anybody asks.
 */
export const NO_COMPOSITE_SCORE =
  "There is no overall score, by design. A single number over surfaces that were measured differently — " +
  "and some that were not measured at all — is a number nobody can check, and an unverifiable score is " +
  "worse than none.";

/**
 * MEMORY_BOUNDARY is FR21's refusal, said before it is needed rather than after.
 *
 * A surface that silently has no memory and one that has decided not to have it are indistinguishable
 * to a person, and only one of them will still be true after somebody adds a cache.
 */
export const MEMORY_BOUNDARY =
  "This surface carries no memory between conversations. A question that depends on what you asked " +
  "earlier will be refused rather than guessed at.";

/**
 * unapprovableCopy is §7.2: an approval request that cannot be approved NAMES THE REASON.
 *
 * The reason comes from the server — it is an entitlement or automation-level fact the browser must not
 * re-derive — and this only frames it. 🚫 The frame never says "not permitted", which tells a person
 * nothing and implies they did something wrong.
 */
export function unapprovableCopy(reason: string): string {
  const stated = reason.trim();
  if (!stated) {
    // Should be unreachable: the platform refuses an un-approvable request that names no reason. If it
    // ever renders, it says so plainly rather than inventing a plausible cause.
    return "This cannot be approved here, and the reason did not arrive with it. That is a defect — the trace id below will find it.";
  }
  return stated;
}

/** RESUME_NOTICE is what the surface says while it is catching up after a reconnect. */
export const RESUME_NOTICE = "Reconnected. Catching up from where you left off.";

/** STREAM_LOST is the honest state between a dropped stream and the next attempt. */
export const STREAM_LOST =
  "The connection dropped. The run keeps going — this is only the view of it — and it will pick up " +
  "from the last message you saw.";
