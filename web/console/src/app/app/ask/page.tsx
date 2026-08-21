import { PageFrame, Empty, Banner } from "@/components/primitives";
import { listWorkflows } from "@/lib/enumeration";
import { Conversation } from "@/components/conversation/conversation";
import { ASK_LEDE, ASK_TITLE, NO_COMPOSITE_SCORE } from "@/lib/conversationCopy";
import { routes } from "@/lib/routes";
import Link from "next/link";

/**
 * `/app/ask` — the conversational console (P31 task 4.1).
 *
 * # 🔴 No existing route is modified
 *
 * This is a NEW surface beside the fifty that exist, and every one of them is untouched. That is scope
 * fidelity, and it is deliberate rather than incidental: the temptation with a conversational surface is
 * to fold it into the overview "while we're here", at which point a phase that adds a surface has
 * redesigned three. The redesign of `/app/workflows` is [P37](../../../../../openspec/changes/p37-source-bound-editors/),
 * decided separately.
 *
 * # Why the workflow list is resolved server-side
 *
 * Because the composer needs a subject before it can ask anything, and a client that fetched the list
 * itself would need a second BFF route whose only job is to enumerate — widening the closed proxy set
 * for a list the server already has in hand. The page renders the frame synchronously and streams the
 * list into it, so the subject is on screen before the data resolves.
 *
 * # The empty state is the important one
 *
 * A deployment with no reported workflow cannot answer any of the fourteen questions, and the honest
 * response is to say what to run rather than to render a composer that will refuse everything. That is
 * the same `not-reported` discipline the coverage page uses, applied one surface over.
 */
export const dynamic = "force-dynamic";

export default async function AskPage() {
  const workflows = await listWorkflows();

  return (
    <PageFrame eyebrow="Conversation" lede={ASK_LEDE} title={ASK_TITLE}>
      {workflows.state === "ok" ? (
        <Conversation workflowIds={workflows.subjects.map((s) => s.id)} />
      ) : workflows.state === "not-mounted" ? (
        <Banner title="This deployment does not enumerate workflows" tone="warn">
          {/* A 503 stays a 503. The capability is absent here — which is something an operator installs,
              not something the reader can fix by checking an identifier. */}
          <p>{workflows.detail ?? "The subject index is not installed on this deployment."}</p>
        </Banner>
      ) : workflows.state === "read-failed" ? (
        <Banner title="The workflow list could not be read" tone="bad">
          {/* 🔴 NOT rendered as "you have no workflows". That would tell a customer their data was never
              received, on a day the platform was merely unreachable. */}
          <p>{workflows.detail ?? "The platform could not be reached. Nothing about your data has changed."}</p>
        </Banner>
      ) : (
        <Empty title="Nothing has been reported yet">
          <p>
            This surface answers questions about a workflow the platform has been told about. Run{" "}
            <code className="mono">heros link --with-ir</code> from your repository and the questions
            below become answerable.
          </p>
          <p>{NO_COMPOSITE_SCORE}</p>
          <p>
            <Link className="text-primary underline underline-offset-2" href={routes.install()}>
              How to install the CLI
            </Link>
          </p>
        </Empty>
      )}
    </PageFrame>
  );
}
