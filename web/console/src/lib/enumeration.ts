import "server-only";
import { load } from "./view";
import { routes } from "./routes";
import type { Subject, SubjectKind } from "./subjects";

/**
 * enumeration.ts reads the platform's subject index (P29 §4).
 *
 * # What changed, and why `subjects.ts` is no longer the answer
 *
 * `subjects.ts` opens by describing a gap: *"The platform exposes **no enumeration endpoint for any of
 * them**"*. So every picker in this console offered only the subjects THIS BROWSER SESSION had already
 * opened — which meant a developer who linked a run, closed the tab and came back the next day found a
 * console that had forgotten their workflow existed. The data was durable the whole time; nothing could
 * ask for it.
 *
 * That gap is closed. `GET /api/v1/workflows`, `/variants` and `/transforms` list what the authenticated
 * organization actually has, and `/api/v1/runs` merges executed and linked runs into one list.
 *
 * # 🔴 `visited` is demoted to an ORDERING HINT, not deleted
 *
 * The session list still says something true and useful — *which of these you were looking at* — and
 * that is worth keeping as an ordering. What it may no longer do is BE the list.
 *
 * And a remembered subject the enumeration does not contain is **discarded, not rendered**. It is the
 * one rule that makes the demotion safe: a subject can disappear from the platform (a revision is
 * forgotten, an organization is switched, a receipt is rolled back), and a session that keeps offering
 * it would send the reader to a page that 404s — with the console's own memory as the only reason they
 * believed it was there. A picker must never offer a door that does not open.
 *
 * # Three states, carried through unflattened
 *
 * The platform answers `empty`, `read-failed` or `not-mounted`, and every one of them is a different
 * sentence on the screen. They are returned here as a union so a caller cannot render one as another —
 * the same discipline `view.ts` applies to fetch outcomes, for the same reason.
 */

export type EnumerationState = "ok" | "empty" | "read-failed" | "not-mounted";

export type EnumeratedSubject = {
  kind: SubjectKind;
  id: string;
  label: string;
  href: string;
  /** hint is one short clause of context. Never a status. */
  hint?: string;
  /** at is when the platform recorded it, for ordering. An ISO instant, or undefined. */
  at?: string;
};

export type Enumeration =
  | { state: "ok"; subjects: EnumeratedSubject[] }
  | { state: "empty"; subjects: [] }
  | { state: "read-failed"; subjects: []; detail?: string }
  | { state: "not-mounted"; subjects: []; detail?: string };

export type WorkflowRow = {
  workflow_id: string;
  source_revision_display?: string;
  reported_at?: string;
  nodes?: number;
  edges?: number;
};

type VariantRow = {
  config_hash: string;
  config_hash_display?: string;
  workflow_id?: string;
  runs?: number;
  latest_linked_at?: string;
};

type TransformRow = {
  config_hash: string;
  config_hash_display?: string;
  source_revision: string;
  source_revision_display?: string;
  workflow_id?: string;
  status?: string;
  reported_at?: string;
  files_changed?: number;
};

type RunRow = {
  run_id: string;
  origin?: string;
  at?: string;
  config_hash?: string;
  linked?: { workflow_id?: string; config_hash_display?: string; linked_at?: string };
};

type Envelope = { state?: string; error?: string };

/**
 * fromOutcome maps a platform outcome onto the three states.
 *
 * 🔴 A failure NEVER becomes `empty`. "You have none" and "we could not read" are opposite facts with
 * opposite next actions, and the whole reason the platform distinguishes them is so this function does
 * not have to guess.
 */
function fromOutcome<T extends Envelope>(
  outcome: Awaited<ReturnType<typeof load<T>>>["outcome"],
  toSubjects: (body: T) => EnumeratedSubject[],
): Enumeration {
  if (outcome.ok !== true) {
    // 🔴 `not-mounted` is the ONE failure kind that is a policy answer rather than a fault. Everything
    // else — not-found, gated, upstream, transport — is "we could not read", and none of them may
    // become `empty`.
    if (outcome.kind === "not-mounted") {
      return { state: "not-mounted", subjects: [], detail: outcome.error };
    }
    return { state: "read-failed", subjects: [], detail: outcome.error };
  }
  const body = outcome.data;
  // The platform's own state wins where it disagrees with the row count — it is the only side that can
  // tell "we read and found none" from "we could not read".
  if (body.state === "read-failed") return { state: "read-failed", subjects: [], detail: body.error };
  if (body.state === "not-mounted") return { state: "not-mounted", subjects: [], detail: body.error };
  const subjects = toSubjects(body);
  return subjects.length === 0 ? { state: "empty", subjects: [] } : { state: "ok", subjects };
}

/** listWorkflows enumerates the workflows this organization has reported. */
export async function listWorkflows(): Promise<Enumeration> {
  const { outcome } = await load<{ workflows?: WorkflowRow[] } & Envelope>((paths) => paths.workflows());
  return fromOutcome(outcome, (body) =>
    (body.workflows ?? []).map((w) => ({
      kind: "workflow" as const,
      id: w.workflow_id,
      label: w.workflow_id,
      href: routes.workflow(w.workflow_id),
      hint:
        w.nodes !== undefined
          ? `${w.nodes} node${w.nodes === 1 ? "" : "s"}${w.source_revision_display ? ` · ${w.source_revision_display}` : ""}`
          : w.source_revision_display,
      at: w.reported_at,
    })),
  );
}

/** listVariants enumerates the configurations this organization has reported a run for. */
export async function listVariants(): Promise<Enumeration> {
  const { outcome } = await load<{ variants?: VariantRow[] } & Envelope>((paths) => paths.variants());
  return fromOutcome(outcome, (body) =>
    (body.variants ?? []).map((v) => ({
      kind: "variant" as const,
      id: v.config_hash,
      label: v.config_hash_display ?? v.config_hash,
      href: routes.scorecard(v.config_hash),
      hint:
        v.runs !== undefined
          ? `${v.runs} run${v.runs === 1 ? "" : "s"}${v.workflow_id ? ` · ${v.workflow_id}` : ""}`
          : v.workflow_id,
      at: v.latest_linked_at,
    })),
  );
}

/** listTransforms enumerates the transform receipts this organization has reported. */
export async function listTransforms(): Promise<Enumeration> {
  const { outcome } = await load<{ transforms?: TransformRow[] } & Envelope>((paths) => paths.transforms());
  return fromOutcome(outcome, (body) =>
    (body.transforms ?? []).map((t) => ({
      kind: "transform" as const,
      id: `${t.config_hash}/${t.source_revision}`,
      label: `${t.config_hash_display ?? t.config_hash} · ${t.source_revision_display ?? t.source_revision}`,
      href: routes.transform(t.config_hash, t.source_revision),
      hint: [t.workflow_id, t.status, t.files_changed !== undefined ? `${t.files_changed} file(s)` : undefined]
        .filter(Boolean)
        .join(" · "),
      at: t.reported_at,
    })),
  );
}

/**
 * runsToSubjects maps an already-fetched runs body onto subjects.
 *
 * Exported so `/app/runs` — which renders the full merged list itself, above the picker — can reuse the
 * ONE fetch it already made rather than issuing a second identical request just to fill a picker. Two
 * requests for one list is two chances for them to disagree, and the reader would see a run in the list
 * that the picker below did not offer.
 */
export function runsToSubjects(body: { runs?: RunRow[] }): EnumeratedSubject[] {
  return (body.runs ?? []).map((r) => ({
    kind: "run" as const,
    id: r.run_id,
    label: r.run_id,
    href: routes.run(r.run_id),
    hint: [r.origin, r.linked?.workflow_id].filter(Boolean).join(" · ") || undefined,
    at: r.at,
  }));
}

/** listRuns enumerates this organization's runs, executed and linked, in one list. */
export async function listRuns(): Promise<Enumeration> {
  const { outcome } = await load<{ runs?: RunRow[] } & Envelope>((paths) => paths.runs());
  return fromOutcome(outcome, runsToSubjects);
}

/**
 * orderByRecentlyVisited puts the subjects this session opened first, and DISCARDS any remembered
 * subject the enumeration does not contain.
 *
 * 🔴 The discard is the point. A session's memory is not evidence that a subject still exists, and a
 * picker that offers a door which does not open is worse than one that offers fewer doors — the reader
 * has no way to tell "the platform lost it" from "the console was wrong", and the console was wrong.
 */
export function orderByRecentlyVisited(
  subjects: EnumeratedSubject[],
  visited: Subject[],
): EnumeratedSubject[] {
  const rank = new Map<string, number>();
  visited.forEach((v, i) => rank.set(v.id, i));
  return [...subjects].sort((a, b) => {
    const ra = rank.get(a.id);
    const rb = rank.get(b.id);
    if (ra !== undefined && rb !== undefined) return ra - rb;
    if (ra !== undefined) return -1;
    if (rb !== undefined) return 1;
    // Neither was opened in this session: the platform's own recency, newest first.
    return (b.at ?? "").localeCompare(a.at ?? "");
  });
}

/**
 * discardedVisits counts remembered subjects the enumeration does not contain.
 *
 * Surfaced rather than silently dropped: a reader whose shortcut list shrank deserves to know it was a
 * deliberate discard and not a rendering bug — and if the number is ever large, that is a signal about
 * the enumeration rather than about the session.
 */
export function discardedVisits(subjects: EnumeratedSubject[], visited: Subject[]): number {
  const have = new Set(subjects.map((s) => s.id));
  return visited.filter((v) => !have.has(v.id)).length;
}
