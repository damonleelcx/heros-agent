import Link from "next/link";
import { ArrowRight, BarChart2, GitPullRequest, Share2 } from "lucide-react";
import { PageFrame } from "@/components/primitives";
import { routes } from "@/lib/subjects";

export const dynamic = "force-dynamic";

/**
 * A workflow's home.
 *
 * It is a router, not a dashboard, and it holds no fetch. The temptation is to summarise the graph, the
 * board and the proposals here — node counts, a top score, a proposal count — which would mean three
 * upstream calls before the reader has said which question they came with, and three numbers stripped
 * of everything that qualifies them. Each surface carries its own qualifications; a summary is where
 * those get lost.
 *
 * The three cards are phrased as QUESTIONS because that is how a reader arrives: they do not want
 * "the board", they want to know which variant is actually better.
 */
export default async function WorkflowPage({ params }: { params: Promise<{ workflowId: string }> }) {
  const { workflowId } = await params;
  const id = decodeURIComponent(workflowId);

  const cards = [
    {
      href: routes.graph(id),
      icon: <Share2 className="size-5 text-primary" />,
      kind: "Graph",
      question: "What shape is this workflow?",
      body: "Nodes, edges, and the patterns the classifier could and could not name — with why, for the ones it could not.",
    },
    {
      href: routes.board(id),
      icon: <BarChart2 className="size-5 text-primary" />,
      kind: "Board",
      question: "Which variant is actually better?",
      body: "Scores with intervals, gate outcomes, the cost/quality frontier, and what the eval set does and does not cover.",
    },
    {
      href: routes.proposals(id),
      icon: <GitPullRequest className="size-5 text-primary" />,
      kind: "Proposals",
      question: "What does the platform propose, and is it verified?",
      body: "Each proposal with its rationale, its verified delta, and the full diff a human merges.",
    },
  ];

  return (
    <PageFrame
      eyebrow="Workflow"
      title={id}
      lede="Three questions about this workflow, and where each is answered."
    >
      <ul className="grid list-none grid-cols-1 gap-4 p-0 sm:grid-cols-3">
        {cards.map((card) => (
          <li key={card.href}>
            <Link
              className="group flex h-full flex-col gap-4 rounded-xl border border-border bg-card p-6 transition-colors hover:border-primary/30"
              href={card.href}
            >
              <span
                className="flex size-10 shrink-0 items-center justify-center rounded-lg border border-primary/20 bg-primary/10"
                aria-hidden="true"
              >
                {card.icon}
              </span>
              <span className="flex-1">
                <span className="font-mono text-[10px] uppercase tracking-widest text-muted-foreground">
                  {card.kind}
                </span>
                <span className="mt-1 block text-sm font-semibold text-foreground">{card.question}</span>
                <span className="mt-1.5 block text-xs leading-relaxed text-muted-foreground">
                  {card.body}
                </span>
              </span>
              <span className="flex items-center gap-1 text-xs text-primary/70 transition-colors group-hover:text-primary">
                Open
                <ArrowRight className="size-3.5" aria-hidden="true" />
              </span>
            </Link>
          </li>
        ))}
      </ul>
    </PageFrame>
  );
}
