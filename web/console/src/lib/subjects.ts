import "server-only";
import type { Session } from "./session";

// The canonical routes live in `routes.ts`, which is NOT server-only: a client component (the
// leaderboard, the command path) needs to build a link, and this module reads the session. Re-exported
// so a server call site that already has `subjects` does not need a second import.
export { routes } from "./routes";

/**
 * subjects.ts remembers which subjects THIS SESSION opened. As of P29 that is all it does.
 *
 * # 🔴 What this file used to be, and why the change matters
 *
 * It opened by describing a gap: FR10 required the user to SELECT a workflow, run, variant, board or
 * transform from platform-provided data, and the platform exposed no enumeration endpoint for any of
 * them. P9's standing constraint forbade adding one, so this module — the subjects this browser session
 * had already opened — WAS the picker's list.
 *
 * The consequence was quiet and expensive: a developer who linked a run, closed the tab and came back
 * the next day found a console that had forgotten their workflow existed. The data was durable the whole
 * time; nothing could ask for it. Every picker looked broken, for a reason no screen stated.
 *
 * P29 §4 closed the gap — `GET /api/v1/workflows`, `/variants`, `/transforms`, and a `/api/v1/runs` that
 * merges executed and linked runs. See `lib/enumeration.ts`, which is now what fills a picker.
 *
 * # What this module is now: an ORDERING HINT
 *
 * "Which of these were you just looking at" is still true, still useful, and still console-local. What
 * it may no longer do is BE the list — and a remembered subject the enumeration does not contain is
 * DISCARDED rather than rendered (`enumeration.ts`, `orderByRecentlyVisited`). A session's memory is not
 * evidence that a subject still exists, and a picker that offers a door which does not open is worse
 * than one that offers fewer doors.
 *
 * # What it legitimately has
 *
 * A console-local fact — not a platform statistic, not derived from one, and saying nothing the session
 * could not already see. It lives on the session record, so it is:
 *
 *   - **per session**, and disappears with it;
 *   - **never shared** between sessions or tenants;
 *   - **server-side**, so no page script can read or forge it.
 *
 * # What it must never become
 *
 * A cache of platform data. It stores a label, a route and a kind — the things needed to offer "you
 * were looking at this" — and never a score, a status, a hash's meaning, or anything a view would then
 * be tempted to render instead of asking the platform. A stale status offered from here would be the
 * console telling the user something that was true once, which is the failure mode the whole
 * render-as-received rule exists to prevent.
 */

export type SubjectKind = "workflow" | "run" | "transform" | "variant" | "proposal";

export type Subject = {
  kind: SubjectKind;
  /** id is the platform identifier. It is rendered as text and used to build a canonical route. */
  id: string;
  /** label is what to show. Usually the id; a workflow may carry a friendlier one. */
  label: string;
  /** href is the canonical route (see information-architecture.md). */
  href: string;
  /** hint is one short clause of context — the workflow a run belongs to, say. Never a status. */
  hint?: string;
  /** at is when this session last opened it, for ordering. Never rendered as a platform fact. */
  at: number;
};

/**
 * VISITED_LIMIT bounds the list.
 *
 * Twelve: enough to cover the handful of subjects an investigation moves between, small enough that
 * the list is scannable rather than a history to search. A longer list is a search problem — and now
 * that the platform enumerates, a search belongs over THAT list rather than over this one.
 */
const VISITED_LIMIT = 12;

/** SessionSubjects is the mutable slot on a session record. */
export type SessionSubjects = { visited?: Subject[] };

/**
 * recordVisit remembers that this session opened a subject.
 *
 * Most-recent-first, de-duplicated by kind+id, bounded. It cannot fail in a way that matters: an
 * unrecorded visit costs a convenience, so nothing here throws and nothing above it checks.
 */
export function recordVisit(session: Session & SessionSubjects, subject: Omit<Subject, "at">): void {
  const visited = session.visited ?? [];
  const next = [
    { ...subject, at: Date.now() },
    ...visited.filter((s) => !(s.kind === subject.kind && s.id === subject.id)),
  ];
  session.visited = next.slice(0, VISITED_LIMIT);
}

/** visitedSubjects returns this session's subjects, most recent first. */
export function visitedSubjects(session: Session & SessionSubjects, kind?: SubjectKind): Subject[] {
  const visited = session.visited ?? [];
  return kind ? visited.filter((s) => s.kind === kind) : visited;
}

/** SUBJECT_LABELS names each kind in the singular, for copy that reads as a sentence. */
export const SUBJECT_LABELS: Record<SubjectKind, string> = {
  workflow: "workflow",
  run: "run",
  transform: "transform",
  variant: "variant",
  proposal: "proposal",
};
