import { requireSession } from "@/lib/session";
import { recordVisit, routes } from "@/lib/subjects";
import { SubjectLink, SubjectStrip } from "@/components/nav";

/**
 * The run's subject strip — the record and the live view, one click apart.
 *
 * The two are genuinely different views of one subject rather than one view with a toggle: the record
 * is what happened, the live view is what is happening, and a run that has finished has only the
 * first. Keeping them as separate routes means each is a shareable link (R9), which a toggle inside one
 * page would not be.
 */
export default async function RunLayout({
  children,
  params,
}: {
  children: React.ReactNode;
  params: Promise<{ runId: string }>;
}) {
  const session = await requireSession();
  const { runId } = await params;
  const id = decodeURIComponent(runId);
  recordVisit(session, { kind: "run", id, label: id, href: routes.run(id) });
  return (
    <>
      <SubjectStrip kind="Run" id={id}>
        <SubjectLink href={routes.run(id)}>Record</SubjectLink>
        <SubjectLink href={routes.runLive(id)}>Live</SubjectLink>
      </SubjectStrip>
      {children}
    </>
  );
}
