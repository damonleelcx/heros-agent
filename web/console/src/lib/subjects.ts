import "server-only";
import type { Session } from "./session";

// The canonical routes live in `routes.ts`, which is NOT server-only: a client component (the
// leaderboard, the command path) needs to build a link, and this module reads the session. Re-exported
// so a server call site that already has `subjects` does not need a second import.
export { routes } from "./routes";

/**
 * subjects.ts is the console's answer to a gap it is not allowed to close (task 5.12, FR30).
 *
 * # The gap
 *
 * FR10 requires the user to **select** a workflow, run, variant, board or transform from
 * platform-provided data. The platform exposes **no enumeration endpoint for any of them** — every
 * customer route is keyed by an identifier the caller must already hold. P9's standing constraint
 * forbids adding a platform endpoint, and 🔴 `careful-api-creation` makes one a one-way door belonging
 * to the phase that owns the data. So the gap is FILED (see
 * `openspec/changes/p9-web-console/surface-or-drop.md` §3), not papered over.
 *
 * # What this module legitimately has
 *
 * The subjects **this session has already opened**. That is a console-local fact — it is not a
 * platform statistic, it is not derived from one, and it says nothing the session could not already
 * see. It lives on the session record, so it is:
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
 * the list is scannable rather than a history to search. A longer list is a search problem, and the
 * console does not have search over subjects it cannot enumerate.
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
