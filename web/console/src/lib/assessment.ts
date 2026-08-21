import type { AssessmentAxis, AssessmentOrigin, AssessmentState, FindingView } from "./types.generated";
import type { Tone } from "./status";

/**
 * assessment.ts is P33's render vocabulary: the thirty-six cells of nine axes × four states, and the
 * origin marker that cuts across them.
 *
 * # 🔴 Why this is a table and not a set of ternaries in the view
 *
 * Task 5.1 requires every one of the thirty-six cells to have a design, and §9.4 names how that
 * erodes: *"the temptation is to render `not_measured` as a greyed-out version of `observed`; it is a
 * different message, not a dimmer one."* A ternary in a component is where the dimming happens,
 * because it is one character to write and nobody reviewing a diff sees a decision. A table makes each
 * cell a row somebody has to fill in.
 *
 * # Why the tones reuse existing tokens and introduce none
 *
 * `--not-reported` already exists, for P29's *"a node the platform was not told about renders `not
 * reported`, never 0"* — which is the same statement about absence this phase extends to a whole
 * report. `--llm` already means "a model produced this". Inventing a P33 palette would be a second
 * language for a distinction the console already draws, and the craft rules forbid a hue that means
 * one thing on one screen and another elsewhere.
 *
 * # 🚫 The hazard palette is `refused` only (task 5.6)
 *
 * `not_measured` is NOT a hazard. An assessment of a repository the platform has just met is mostly
 * absence, so painting it in hazard colours would make the first report a wall of red — and a palette
 * that is not rare is a palette that means nothing.
 */

/** STATE_TONE maps a state to the console's existing tone vocabulary. */
export const STATE_TONE: Record<AssessmentState, Tone> = {
  // A measurement is INFORMATION, not approval. `ok` (green) would say the number is good, and this
  // report makes no such claim about any number — it says how the number was obtained.
  measured: "info",
  // Read from the code. Neutral: true by construction is neither good nor bad.
  observed: "neutral",
  // 🔴 The P29 token, reused rather than re-chosen. Absence has one appearance in this console.
  not_measured: "unknown",
  // The only hazard.
  refused: "halt",
};

/**
 * STATE_LABEL is the WORD. A state is never carried by colour alone: colour fails for a colourblind
 * reader, in greyscale, in a printout, and in a screenshot pasted into a black-and-white document.
 */
export const STATE_LABEL: Record<AssessmentState, string> = {
  measured: "measured",
  observed: "observed",
  not_measured: "not measured",
  refused: "refused",
};

/**
 * STATE_MEANING is the sentence under the word — what the state SAYS, in the reader's terms.
 *
 * 🔴 Four sentences that could not be swapped. If two of these could be exchanged without a reader
 * noticing, the four states are three.
 */
export const STATE_MEANING: Record<AssessmentState, string> = {
  measured: "A number from an eval run, with the interval and the size of the set behind it.",
  observed: "Read from your code. True by construction — you can check it in your editor.",
  not_measured:
    "We looked and could not establish this. The finding names what was missing, and what would make it answerable.",
  refused:
    "This build cannot assess this surface for this target. That is a limit on our side, and it names which part of ours.",
};

/**
 * AXIS_LABEL is the noun dictionary (task 8.4). The axes are named EXACTLY as the console's own rail
 * names them, because a report that calls a surface one thing where the editor calls it another makes
 * the reader do a translation the platform should have done.
 */
export const AXIS_LABEL: Record<AssessmentAxis, string> = {
  model: "Model",
  prompt: "Prompt",
  skills: "Skills",
  context: "Context",
  tools: "Tools",
  memory: "Memory",
  harness: "Harness",
  loop: "Loop",
  graph: "Graph",
};

/** AXIS_QUESTION is what each axis answers, so a reader scanning nine rows knows what they are reading. */
export const AXIS_QUESTION: Record<AssessmentAxis, string> = {
  model: "which model each call site uses, and with what parameters",
  prompt: "where each call site's prompt comes from",
  skills: "what platform capabilities are bound at each call site",
  context: "how a single call builds the message list it sends",
  tools: "what the model is offered, and whether that list is fixed",
  memory: "what is carried between turns and sessions",
  harness: "the scaffold around a call — turns, ceilings, retries",
  loop: "the control loop a multi-turn node runs in",
  graph: "how the call sites are connected",
};

/**
 * ORIGIN_LABEL marks an inference PERSISTENTLY and non-decoratively (task 5.2, FR3).
 *
 * 🚫 Not a tooltip. §9.4: *"a reader scanning the report must be able to see, without hovering, how
 * much of it a model wrote."* A `title` attribute is invisible to touch, to keyboard navigation, and
 * to every screen reader that does not announce tooltips — so a marker that lives there is a marker
 * most readers never see.
 */
export const ORIGIN_LABEL: Record<AssessmentOrigin, string> = {
  structural: "read from your code",
  inferred: "inferred by a model",
};

/** ORIGIN_TONE — `--llm` is the console's existing token for a model-sourced fact. */
export const ORIGIN_TONE: Record<AssessmentOrigin, Tone | undefined> = {
  structural: undefined,
  inferred: "info",
};

/**
 * MISSING_INPUT_LABEL renders each named missing input as a short phrase.
 *
 * 🔴 Every member of the platform's closed set has an entry, and `assessment.test.mjs` asserts the two
 * are equal. A member added in Go and missing here renders as its raw snake_case identifier — which
 * reads as a leaked internal, and on the one axis a reader most needs to understand.
 *
 * The four eval reasons are FOUR DISTINCT phrases and must stay so: a reader does four different things
 * about them, and one shared phrase tells them to do none.
 */
export const MISSING_INPUT_LABEL: Record<string, string> = {
  no_runnable_entry_point: "nothing here we can run",
  missing_credential: "a provider credential",
  sandbox_refusal: "our sandbox declined it",
  unsupported_language: "a runner for this language",
  frontend_emits_no_edges: "our parser emits no edges",
  unresolved_in_ir: "a value we could not follow",
  no_source_snapshot: "your source",
  no_call_sites_discovered: "a call site we recognise",
  not_visible_in_static_ir: "evidence between turns",
  budget_exhausted: "budget",
  inference_abstained: "the analysis declined to conclude",
};

/**
 * REFUSAL_CAUSE_LABEL names which of exactly three things this build lacks. Never a generic
 * "unsupported": the three send a reader to three different places, and only one is theirs.
 */
export const REFUSAL_CAUSE_LABEL: Record<string, string> = {
  frontend: "our parser for this language",
  analysis: "this analysis, in this build",
  language: "support for this language",
};

/**
 * groupByRank splits the already-ordered findings into the evidence-strength bands the page renders as
 * sections.
 *
 * 🔴 It GROUPS, it does not SORT. The order arrives from the platform, which computed it from the
 * evidence-strength ladder (FR5), and a console that re-sorted would eventually sort by a severity
 * somebody guessed — the one ordering the requirement names as forbidden. This function asserts the
 * incoming order rather than establishing it: `rank` is a field, and the bands are read off it.
 */
export type Band = { key: string; heading: string; note: string; findings: FindingView[] };

export function groupByRank(findings: readonly FindingView[]): Band[] {
  const bands: Band[] = [
    {
      key: "measured",
      heading: "Measured",
      note: "A number from an eval run. Read the decisiveness beside it before you read the number.",
      findings: [],
    },
    {
      key: "observed",
      heading: "Read from your code",
      note: "True by construction. You can check every one of these in your editor.",
      findings: [],
    },
    {
      key: "inferred",
      heading: "Inferred by a model",
      note: "A model read your source and concluded this. Weigh it accordingly — it is not a parse.",
      findings: [],
    },
    {
      key: "absent",
      heading: "Not measured",
      note: "We looked and could not establish these. Each names what was missing.",
      findings: [],
    },
    {
      key: "refused",
      heading: "This build cannot assess",
      note: "Limits on our side, not findings about your repository.",
      findings: [],
    },
  ];
  for (const f of findings) {
    const index =
      f.state === "measured" ? 0
      : f.state === "observed" && f.origin === "structural" ? 1
      : f.state === "observed" ? 2
      : f.state === "not_measured" ? 3
      : 4;
    bands[index].findings.push(f);
  }
  return bands.filter((b) => b.findings.length > 0);
}
