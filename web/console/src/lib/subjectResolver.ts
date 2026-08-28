import "server-only";
import { cookies } from "next/headers";
import { load } from "./view";
import { listWorkflows } from "./enumeration";
import { recordSubjectOutcome } from "./subjectHealth";
import {
  SUBJECT_COOKIE,
  decodeSubject,
  isAxisSubject,
  type AxisSubject,
  type SubjectOutcome,
} from "./axisSubject";

/**
 * subjectResolver.ts answers "which node is this surface bound to" — ONCE, in the shell (P37 FR2, D-37.2).
 *
 * # Why this is not a picker on each page
 *
 * `lib/projection.ts` already argues it one level up: *"Putting a workflow picker on each of them would
 * make seven surfaces ask a question the reader did not come there with"*. The same argument applies to
 * the node. A reader who arrives at `/app/memory` from `/app/context` has already chosen.
 *
 * # 🔴 The resolution is never SILENT, and that is the whole safety argument
 *
 * A silently defaulted subject is a reader editing the wrong node — R4 in the PRD's risk table, rated
 * High. So `resolved` carries `sole`, and the shell renders the name even when there was exactly one
 * candidate. Being told which node was chosen is not the same as being defaulted into one.
 *
 * # The order (D-37.2)
 *
 *   1. an EXPLICIT selection — the `?workflow=&node=` a `finding` carries (FR18), or the shell's control
 *   2. the remembered choice, VALIDATED against the live node list
 *   3. `not_connected` — no structure reported at all
 *   4. exactly one candidate → resolved, `sole: true`, and still named
 *   5. more than one and none chosen → `ambiguous`, asked once
 *
 * Step 2's validation is `enumeration.ts`'s discard rule applied one level down: *"a remembered subject
 * the enumeration does not contain is DISCARDED rather than rendered ... a picker must never offer a
 * door that does not open."* A cookie is not evidence that a node still exists.
 */

/** NodeRow is the shape `GET /api/v1/workflows/{id}/nodes` answers with. */
type NodeRow = {
  node_id: string;
  symbol?: string;
  file?: string;
  language?: string;
};

type NodesEnvelope = { state?: string; nodes?: NodeRow[]; workflow_id?: string; detail?: string };

/** SubjectSelection is what a caller may pass from a URL. Both halves or neither. */
export type SubjectSelection = { workflow?: string; node?: string };

/**
 * resolveSubject reads the reader's candidate nodes and picks one.
 *
 * `selection` comes from the request's own search params and is the highest-priority input — a link a
 * `finding` produced names a node, and nothing outranks a person clicking it.
 */
export async function resolveSubject(selection: SubjectSelection = {}): Promise<SubjectOutcome> {
  // 🔴 Counted at the ONE place every answer leaves this function (§7.1). Wrapping the body rather than
  // sprinkling `recordSubjectOutcome` at eight return sites is what makes "every outcome is counted" a
  // property of the code instead of a habit — the eighth call site is the one that gets forgotten, and
  // the state it silently stops counting is the one nobody notices is missing.
  const outcome = await resolve(selection);
  recordSubjectOutcome(outcome.state);
  return outcome;
}

async function resolve(selection: SubjectSelection): Promise<SubjectOutcome> {
  const workflows = await listWorkflows();
  if (workflows.state === "not-mounted") {
    return { state: "not_mounted", detail: workflows.detail };
  }
  if (workflows.state === "read-failed") {
    return { state: "read_failed", detail: workflows.detail };
  }
  if (workflows.subjects.length === 0) {
    // 🔴 The customer's own boundary, and NOT a transport failure. D-37.5: it arrives as a 200 carrying
    // the word, because a 404 would send the reader to look for a broken deployment when the truth is
    // that they have not connected anything.
    return { state: "not_connected" };
  }

  // The workflow to enumerate nodes from: an explicit one if it is real, otherwise the most recently
  // reported — the same choice `loadProjection` already makes, extended one level down rather than
  // replaced.
  const known = workflows.subjects.map((w) => w.id);
  const workflowId =
    selection.workflow && known.includes(selection.workflow) ? selection.workflow : known[0];

  const { outcome } = await load<NodesEnvelope>((paths) => paths.studioNodes(workflowId));
  if (!outcome.ok) {
    if (outcome.kind === "not-mounted") return { state: "not_mounted", detail: outcome.error };
    return { state: "read_failed", detail: outcome.error };
  }
  const body = outcome.data;
  const candidates: AxisSubject[] = (body.nodes ?? [])
    .filter((n) => typeof n.node_id === "string" && n.node_id.length > 0)
    .map((n) => ({
      workflow_id: workflowId,
      node_id: n.node_id,
      symbol: n.symbol,
      file: n.file,
      language: n.language,
    }));

  if (body.state === "not-reported" || candidates.length === 0) {
    // A workflow the platform knows the NAME of but not the SHAPE of. The reader has connected
    // something; what is missing is the structure — which is `not_connected`'s sibling and gets the same
    // treatment here, because the next action from an axis surface is identical: there is no node of
    // theirs to bind to.
    return { state: "not_connected" };
  }

  // 1 — an explicit selection wins, if it names a node that exists.
  const explicit = candidates.find((c) => c.node_id === selection.node);
  if (explicit) return { state: "resolved", subject: explicit, candidates, sole: candidates.length === 1 };

  // 2 — the remembered choice, validated against the live list.
  const remembered = decodeSubject((await cookies()).get(SUBJECT_COOKIE)?.value);
  if (remembered && remembered.workflow_id === workflowId) {
    const still = candidates.find((c) => c.node_id === remembered.node_id);
    if (still) return { state: "resolved", subject: still, candidates, sole: candidates.length === 1 };
    // Fall through deliberately. A remembered node the platform no longer reports is DISCARDED, not
    // rendered — the console's own memory is not evidence that a node still exists.
  }

  // 4 — exactly one candidate: chosen without asking, and still named on screen.
  if (candidates.length === 1) {
    return { state: "resolved", subject: candidates[0], candidates, sole: true };
  }

  // 5 — several, and none chosen. Asked ONCE, in the shell.
  return { state: "ambiguous", candidates };
}

/**
 * subjectFromSearchParams reads the two identifiers a `finding` link carries (FR18).
 *
 * 🔴 Both or neither. A URL naming a node without its workflow is the ambiguity D-37.1 exists to
 * prevent, arriving through the one door that is not under the resolver's control — so a half-filled
 * pair is treated as no selection at all rather than as a partial one.
 */
export function subjectFromSearchParams(
  params: Record<string, string | string[] | undefined>,
): SubjectSelection {
  const one = (v: string | string[] | undefined) => (Array.isArray(v) ? v[0] : v);
  const workflow = one(params.workflow);
  const node = one(params.node);
  if (!workflow || !node) return {};
  return { workflow, node };
}

/** subjectOf returns the resolved subject, or null in every other state. */
export function subjectOf(outcome: SubjectOutcome): AxisSubject | null {
  return outcome.state === "resolved" && isAxisSubject(outcome.subject) ? outcome.subject : null;
}
