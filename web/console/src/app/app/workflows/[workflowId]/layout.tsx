import { requireSession } from "@/lib/session";
import { recordVisit, routes } from "@/lib/subjects";
import { SubjectLink, SubjectStrip } from "@/components/nav";

/**
 * The workflow's subject strip.
 *
 * It exists so that moving graph → board → proposals never re-asks which workflow. The subject is in
 * the path and each surface is a link, which is what the four legacy pages could not do: they had no
 * links between them at all, so the only way across was to edit a query parameter by hand.
 *
 * `recordVisit` runs here rather than on each page because the strip is the one thing every workflow
 * surface shares — so opening any of them puts the workflow in the command path for the rest of the
 * session, and the reader never types it twice.
 */
export default async function WorkflowLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ workflowId: string }>;
}) {
  const session = await requireSession();
  const { workflowId } = await params;
  const id = decodeURIComponent(workflowId);
  recordVisit(session, { kind: "workflow", id, label: id, href: routes.workflow(id) });
  return (
    <>
      <SubjectStrip kind="Workflow" id={id}>
        <SubjectLink href={routes.workflow(id)}>Overview</SubjectLink>
        <SubjectLink href={routes.graph(id)}>Graph</SubjectLink>
        <SubjectLink href={routes.board(id)}>Board</SubjectLink>
        <SubjectLink href={routes.proposals(id)}>Proposals</SubjectLink>
      </SubjectStrip>
      {children}
    </>
  );
}
