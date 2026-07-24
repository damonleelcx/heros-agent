import { redirect } from "next/navigation";
import { requireSession } from "@/lib/session";
import { visitedSubjects, routes } from "@/lib/subjects";
import { PageFrame } from "@/components/primitives";
import { SubjectPicker } from "@/components/subjectPicker";

/**
 * Run selection.
 *
 * `p25monitor.html` has no input field at all — the only way in is to edit the URL, and its error copy
 * is literally instructions for doing so: *"No run_id in the URL. Append ?run_id=…"*. That is a syntax
 * lesson where a next action belongs (R8).
 */
export const dynamic = "force-dynamic";

export default async function RunsPage({ searchParams }: { searchParams: Promise<{ run_id?: string }> }) {
  const session = await requireSession();
  const typed = (await searchParams).run_id?.trim();
  if (typed) redirect(routes.run(typed));

  return (
    <PageFrame
      eyebrow="Runs"
      title="Open a run"
      lede="A run has a record with per-node input and output, and a live view while it executes."
    >
      <SubjectPicker
        kind="run"
        visited={visitedSubjects(session, "run")}
        action="/app/runs"
        field={{ name: "run_id", label: "Run id", placeholder: "run-…" }}
        help="Submitting a variant from Configure carries you straight into its run, so this field is for a run somebody else produced or one from an earlier session."
      />
    </PageFrame>
  );
}
