/**
 * status.ts is the status vocabulary and its fallback (R3, FR19/FR20, task 5.3).
 *
 * # The two defects this closes
 *
 * `p2.html` builds its CSS class by string interpolation — `state-${status}` — against a stylesheet
 * that knows eight values. A ninth status from the server produces a class no rule matches, so it
 * renders **unstyled and uncoloured while still looking like a rendered state**. The failure is silent
 * in both directions: the user sees no signal and nothing is logged.
 *
 * `p25monitor.html` has the mirror defect with the worse failure mode: an unknown status falls back to
 * the **`running`** style, so an unmodelled state actively **impersonates a known one**. The first
 * loses information; the second asserts something false.
 *
 * # The resolution
 *
 * A closed map from status value to `{ tone, word }`, plus an explicit `unknown` fallback that is
 * VISUALLY DISTINCT from every modelled tone (dashed border, no status hue) and renders the raw value.
 * An unmodelled status is therefore visible, honest, and impossible to mistake for a state the design
 * intended.
 *
 * # Why a word as well as a tone
 *
 * A status is never carried by colour alone: colour fails for a colourblind reader, in greyscale, in a
 * printout, and in a screenshot pasted into a black-and-white document. The word is the status; the
 * tone is the accelerator.
 */

export type Tone = "ok" | "warn" | "bad" | "halt" | "info" | "neutral" | "unknown";

type StatusMeaning = { tone: Tone; word: string };

/**
 * KNOWN is every status value the platform's read models actually return, in one table.
 *
 * One table rather than one per view, because the same value must read the same way everywhere — a
 * `halted` run that is purple on one screen and red on another has told the reader they are two
 * different things.
 */
const KNOWN: Record<string, StatusMeaning> = {
  // Run record (P2 `runView.status`, P2.5 `RunMonitor.status`)
  running: { tone: "info", word: "running" },
  succeeded: { tone: "ok", word: "succeeded" },
  failed: { tone: "bad", word: "failed" },
  // `halted` is its OWN tone, not a shade of failed. A run stopped because a node's output violated
  // its typed I/O contract — before the bad output went downstream — is a different event from a run
  // that failed, and the two have different next actions. Collapsing them is the R3 defect.
  halted: { tone: "halt", word: "halted" },
  "build-rejected": { tone: "warn", word: "build-rejected" },
  queued: { tone: "neutral", word: "queued" },

  // Per-node state (P2.5 `RunMonitorNode.state`, P2 `nodeView.status`)
  ok: { tone: "ok", word: "ok" },
  timed_out: { tone: "warn", word: "timed out" },
  pending: { tone: "neutral", word: "pending" },

  // Transform record (P2 `transformView.status`)
  built: { tone: "ok", word: "built" },

  // Verification strength (P2 `transformView.verification_strength`) — the distinction ADR-003 makes,
  // and one a reviewer must be able to see without asking.
  "type-checked": { tone: "ok", word: "type-checked" },
  "syntax-checked": { tone: "warn", word: "syntax-checked" },

  // Board state (P4 `View.state`)
  empty: { tone: "neutral", word: "empty" },
  partial: { tone: "warn", word: "partial" },
  // `complete` has no distinct rendering on the legacy board, so "finished" looks like "in progress
  // with nothing left" — opposite next actions. Surfaced per the surface-or-drop decision.
  complete: { tone: "ok", word: "complete" },
  error: { tone: "bad", word: "error" },

  // P5.5 surface state
  ready: { tone: "ok", word: "ready" },
  verifying: { tone: "info", word: "verifying" },
};

/**
 * statusOf resolves a status value, or returns the fallback with the raw value preserved.
 *
 * `known: false` is what a component keys the distinct fallback styling on. It is returned rather
 * than inferred from a missing tone, because "we do not model this" is a fact the caller must be able
 * to act on, not an absence it has to notice.
 */
export function statusOf(value: string | null | undefined): {
  tone: Tone;
  word: string;
  known: boolean;
  raw: string;
} {
  const raw = (value ?? "").trim();
  if (!raw) return { tone: "neutral", word: "not reported", known: true, raw: "" };
  const known = KNOWN[raw];
  if (known) return { ...known, known: true, raw };
  // The raw value is the word. The console does not know what it means and says so by rendering it
  // exactly as received rather than by translating it into something it is not.
  return { tone: "unknown", word: raw, known: false, raw };
}

/** TONE_CLASS maps a tone to its chip modifier. The `unknown` class is deliberately not a status hue. */
export const TONE_CLASS: Record<Tone, string> = {
  ok: "chip--ok",
  warn: "chip--warn",
  bad: "chip--bad",
  halt: "chip--halt",
  info: "chip--info",
  neutral: "",
  unknown: "chip--unknown",
};
