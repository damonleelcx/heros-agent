import "server-only";
import { load } from "./view";
import {
  memoryVocabularyFrom,
  modelVocabularyFrom,
  type MemoryStrategyRow,
  type ModelRow,
} from "./axisVocabularyShapes";
import type { AxisVocabulary } from "./axisKit";

/**
 * axisVocabulary.ts FETCHES each axis's closed set — from the endpoint that already owns it (P37 FR5).
 *
 * # 🔴 Why this composes existing endpoints rather than adding one
 *
 * `decisions.md` D-37.4 records that this phase adds no new endpoint shape, and the vocabularies were
 * already reachable — they were simply reachable in three different shapes:
 *
 *   memory   `GET /api/v1/memory?language=` — the closed set WITH each entry's `params_schema` and the
 *            boundary, both derived server-side from `transform.CoverageFor("memory")`
 *   context  the live `context_coverage` the axis read already carries (P37 §5.2)
 *   model    `GET /api/v1/models` — the registered catalogue
 *
 * A single `GET /api/v1/axis-vocabulary?axis=` would be tidier and would be a NEW fact on the wire for
 * something three endpoints already answer. So the adapting happens in the console, where it is one
 * module rather than seven pickers.
 *
 * # 🔴 An axis with NO reachable vocabulary gets none, and the surface says so
 *
 * Four axes — harness, loop, prompt, skills — have no vocabulary endpoint, and `graph`'s three forms
 * refuse in every language. There is no loader for them here, and there must not be a hand-written array
 * standing in for one: the surface renders read-only WITH ITS REASON (P34 §7.3). A control that cannot
 * produce a diff reads as a bug, and a picker bound to a literal in a TSX file is the second source of
 * truth this module exists to avoid.
 *
 * That is not a gap this phase hid. It is the honest shape of the product today, and the reading surface
 * carries the explanation of why, per axis.
 */

/** MemoryBoundary is the axis's stated boundary, derived server-side from the coverage table. */
export type MemoryBoundary = {
  applicable: boolean;
  missing_artifact?: string;
  reason?: string;
  language_is_the_blocker: boolean;
  authorable_anyway: boolean;
};

export type MemoryVocabularyOutcome =
  | { state: "ok"; vocabulary: AxisVocabulary; boundary: MemoryBoundary; language: string }
  | { state: "read_failed"; detail?: string }
  | { state: "not_mounted"; detail?: string };

/**
 * loadMemoryVocabulary reads the memory strategies and the boundary for one language.
 *
 * 🔴 `language` is REQUIRED and is the reader's node's own. The platform's handler refuses to guess:
 * *"No silent default to `go`. A boundary computed for the wrong language is a claim about code the
 * reader does not have."* Passing an empty language inherits that fail-closed path rather than working
 * around it.
 */
export async function loadMemoryVocabulary(language: string): Promise<MemoryVocabularyOutcome> {
  const { outcome } = await load<{
    boundary?: MemoryBoundary;
    strategies?: MemoryStrategyRow[];
    language?: string;
    dimension?: string;
  }>((paths) => paths.memoryVocabulary(language));

  if (!outcome.ok) {
    if (outcome.kind === "not-mounted") return { state: "not_mounted", detail: outcome.error };
    return { state: "read_failed", detail: outcome.error };
  }
  const body = outcome.data;
  return {
    state: "ok",
    language: body.language ?? language,
    boundary: body.boundary ?? {
      applicable: false,
      language_is_the_blocker: false,
      authorable_anyway: true,
    },
    vocabulary: memoryVocabularyFrom(body.strategies ?? [], "registry builtins"),
  };
}

export type ModelVocabularyOutcome =
  | { state: "ok"; vocabulary: AxisVocabulary }
  | { state: "read_failed"; detail?: string }
  | { state: "not_mounted"; detail?: string };

/** loadModelVocabulary reads the registered models a node may be changed to. */
export async function loadModelVocabulary(provider: string): Promise<ModelVocabularyOutcome> {
  const { outcome } = await load<{ models?: ModelRow[] }>((paths) => paths.studioModels());
  if (!outcome.ok) {
    if (outcome.kind === "not-mounted") return { state: "not_mounted", detail: outcome.error };
    return { state: "read_failed", detail: outcome.error };
  }
  const rows = outcome.data.models ?? [];
  return {
    state: "ok",
    vocabulary: modelVocabularyFrom(
      rows,
      provider,
      `${rows.length} registered model${rows.length === 1 ? "" : "s"}`,
    ),
  };
}
