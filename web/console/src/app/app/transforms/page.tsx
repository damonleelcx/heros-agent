import { redirect } from "next/navigation";
import { requireSession } from "@/lib/session";
import { visitedSubjects, routes } from "@/lib/subjects";
import { listTransforms, orderByRecentlyVisited, discardedVisits } from "@/lib/enumeration";
import { Banner, PageFrame } from "@/components/primitives";
import { SubjectPicker } from "@/components/subjectPicker";

/**
 * Transform selection.
 *
 * A transform is a TWO-PART subject: a config hash means nothing without the source revision it was
 * applied to. Both are required, and a request carrying only one gets its own message — distinct from
 * "not found", which is what P2-12 already gets right and the port must not flatten.
 */
export const dynamic = "force-dynamic";

export default async function TransformsPage({
  searchParams,
}: {
  searchParams: Promise<{ config_hash?: string; source_revision?: string }>;
}) {
  const session = await requireSession();
  const params = await searchParams;
  const hash = params.config_hash?.trim();
  const revision = params.source_revision?.trim();
  if (hash && revision) redirect(routes.transform(hash, revision));

  const incomplete = Boolean(hash) !== Boolean(revision);

  // 🔴 The list comes from the PLATFORM (P29 §4), not from this browser. The session's own list is
  // demoted to an ORDERING HINT, and a remembered subject the enumeration does not contain is DISCARDED
  // rather than offered — a session's memory is not evidence that a subject still exists.
  const visited = visitedSubjects(session, "transform");
  const enumeration = await listTransforms();
  const available =
    enumeration.state === "ok"
      ? { ...enumeration, subjects: orderByRecentlyVisited(enumeration.subjects, visited) }
      : enumeration;
  const discarded = discardedVisits(available.subjects, visited);

  return (
    <PageFrame
      eyebrow="Transforms"
      title="Open a transform"
      lede="The reviewable diff a variant produced, with what the build gate was able to prove about it."
    >
      {incomplete ? (
        <Banner tone="warn" title="A transform needs both halves of its key">
          <p>
            A config hash identifies a configuration; a source revision identifies the code it was
            applied to. One without the other does not name a transform — which is a different thing
            from a transform that does not exist.
          </p>
        </Banner>
      ) : null}
      <SubjectPicker
        kind="transform"
        available={available}
        discarded={discarded}
        action="/app/transforms"
        field={{ name: "config_hash", label: "Config hash", placeholder: "sha256…" }}
        help="Both fields are required. A run's record carries this pair, so opening a run and following its link is usually faster than typing them."
      >
        <div className="field">
          <label htmlFor="source_revision">Source revision</label>
          <input
            className="input mono"
            id="source_revision"
            name="source_revision"
            type="text"
            required
            spellCheck={false}
            autoComplete="off"
          />
        </div>
      </SubjectPicker>
    </PageFrame>
  );
}
