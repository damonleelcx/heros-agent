import { redirect } from "next/navigation";
import { requireSession } from "@/lib/session";
import { visitedSubjects, routes } from "@/lib/subjects";
import { PageFrame } from "@/components/primitives";
import { SubjectPicker } from "@/components/subjectPicker";

/**
 * Workflow selection.
 *
 * 🔴 Opening this with no workflow shows SELECTION, never a workflow. The `'wf-demo'` default in
 * `p4board.html` is not ported: a confidently-rendered board for someone else's workflow is strictly
 * worse than an empty state, because an empty state tells the truth (P4-0, FR10, R8).
 */
export const dynamic = "force-dynamic";

export default async function WorkflowsPage({
  searchParams,
}: {
  searchParams: Promise<{ workflow_id?: string }>;
}) {
  const session = await requireSession();
  const params = await searchParams;

  // The picker's form is a GET to this route, so a typed identifier resolves to the canonical route
  // rather than becoming a query parameter the view then reads. That keeps R9's shareable link and
  // R8's no-hand-typed-entry compatible: the subject ends up in the PATH.
  const typed = params.workflow_id?.trim();
  if (typed) redirect(routes.workflow(typed));

  return (
    <PageFrame
      eyebrow="Workflows"
      title="Open a workflow"
      lede="A workflow has a classified graph, an eval board, and — once the platform has something to say — proposals."
    >
      <SubjectPicker
        kind="workflow"
        visited={visitedSubjects(session, "workflow")}
        action="/app/workflows"
        field={{ name: "workflow_id", label: "Workflow id", placeholder: "owner/repository" }}
        help="The identifier the CLI prints and the one your discovery run was keyed by. Opening it here makes it available from the command path for the rest of this session."
      />
    </PageFrame>
  );
}
