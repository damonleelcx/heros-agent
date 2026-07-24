import Link from "next/link";
import { ArrowRight, FileCode, GitBranch, Layers, Play, Settings } from "lucide-react";
import { requireSession } from "@/lib/session";
import { visitedSubjects, routes, SUBJECT_LABELS } from "@/lib/subjects";
import { PageFrame, Section, Empty } from "@/components/primitives";

export const dynamic = "force-dynamic";

/**
 * The overview.
 *
 * It answers one question — *where was I, and where can I go?* — and it answers it without making a
 * single upstream call. That is deliberate: it is the first surface after sign-in, and a landing page
 * that cannot render while the platform is slow is a landing page that makes the platform's problems
 * look like the console's.
 *
 * The design system's version of this screen offers four "recently opened" cards with a status chip on
 * each. The status chip is dropped here and the omission is the point: this console knows a subject was
 * OPENED, from its own session, and knows nothing about its current state. Rendering a status beside it
 * would mean either re-fetching four subjects to decorate a menu, or — far worse — showing a state that
 * was true when the reader last looked.
 */
const ENTRIES = [
  {
    href: routes.configure(),
    icon: <Settings className="size-5 text-primary" />,
    title: "Change one call site and run it",
    body: "Override a model, prompt, skill set or context policy for a single node — everything else stays as it is.",
  },
  {
    href: "/app/workflows",
    icon: <GitBranch className="size-5 text-primary" />,
    title: "Open a workflow's graph, board or proposals",
    body: "What the classifier found, how the variants compare, and what the platform proposes.",
  },
  {
    href: "/app/runs",
    icon: <Play className="size-5 text-primary" />,
    title: "Inspect or watch a run",
    body: "Per-node input and output, live while it executes.",
  },
  {
    href: "/app/variants",
    icon: <Layers className="size-5 text-primary" />,
    title: "Read a variant's scorecard",
    body: "Why it scored what it scored — per node, per failure cluster, with the ablation that was actually run.",
  },
  {
    href: "/app/transforms",
    icon: <FileCode className="size-5 text-primary" />,
    title: "Review the diff a variant produced",
    body: "The reviewable change, and what the build gate was able to prove about it.",
  },
];

export default async function OverviewPage() {
  const session = await requireSession();
  const visited = visitedSubjects(session);

  return (
    <PageFrame
      eyebrow="Console"
      title="Where to?"
      lede="Pick up something you were looking at, or start by configuring one call site and running it."
    >
      <Section title="Opened in this session" aside="held for this session only, never shared">
        {visited.length === 0 ? (
          <Empty title="Nothing opened yet">
            <p>
              This console shows a workflow&apos;s graph, the diff a variant produced, the run it
              produced, and the board that scored it. Start by opening a subject you already have an
              identifier for, or configure a variant and submit it — the result carries you into both
              views without retyping anything.
            </p>
            <p>
              The platform does not currently expose a way to list your workflows and runs, so this
              console cannot offer subjects it has never seen. That gap is recorded against the phases
              that own the data rather than worked around here.
            </p>
          </Empty>
        ) : (
          <ul className="grid list-none grid-cols-1 gap-3 p-0 sm:grid-cols-2 lg:grid-cols-4">
            {visited.map((subject) => (
              <li key={`${subject.kind}:${subject.id}`}>
                <Link
                  className="flex h-full flex-col gap-3 rounded-xl border border-border bg-card p-4 transition-colors hover:border-primary/30"
                  href={subject.href}
                >
                  <span className="font-mono text-[10px] uppercase tracking-widest text-primary">
                    {SUBJECT_LABELS[subject.kind]}
                  </span>
                  <span className="min-w-0">
                    <span className="mono block truncate text-sm text-foreground">{subject.label}</span>
                    {subject.hint ? (
                      <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                        {subject.hint}
                      </span>
                    ) : null}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        )}
      </Section>

      <Section title="Start somewhere">
        <ul className="grid list-none grid-cols-1 gap-3 p-0 sm:grid-cols-2">
          {ENTRIES.map((entry) => (
            <li key={entry.href}>
              <Link
                className="group flex h-full items-start gap-4 rounded-xl border border-border bg-card p-5 transition-colors hover:border-primary/30"
                href={entry.href}
              >
                <span
                  className="mt-0.5 flex size-9 shrink-0 items-center justify-center rounded-lg border border-primary/20 bg-primary/10"
                  aria-hidden="true"
                >
                  {entry.icon}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block text-sm font-semibold text-foreground">{entry.title}</span>
                  <span className="mt-1 block text-xs leading-relaxed text-muted-foreground">
                    {entry.body}
                  </span>
                </span>
                <ArrowRight
                  className="mt-0.5 size-4 shrink-0 text-muted-foreground/40 transition-colors group-hover:text-primary"
                  aria-hidden="true"
                />
              </Link>
            </li>
          ))}
        </ul>
      </Section>
    </PageFrame>
  );
}
