import Link from "next/link";
import { ChevronRight } from "lucide-react";
import type { Subject, SubjectKind } from "@/lib/subjects";
import { SUBJECT_LABELS } from "@/lib/subjects";
import { Section } from "./primitives";

/**
 * SubjectPicker is how the console asks "which one?" (FR10, R8).
 *
 * # What it offers, in order
 *
 * 1. **Subjects this session already opened.** The thing the user is most likely to want, offered
 *    without typing. This is the console's answer to a gap it is not permitted to close: the platform
 *    exposes no enumeration endpoint for workflows, runs, variants or transforms, so the console
 *    cannot list "all of yours" — but it can always offer the ones you have opened, and it must never
 *    ask you to retype one of them.
 * 2. **Direct identifier entry, as an accelerator.** Explicitly permitted by task 7.2 and required by
 *    R9's shareability — but never the ONLY path, which is what all four legacy pages made it.
 *
 * # 🔴 What it never does
 *
 * Substitute a default. `p4board.html` does `params.get('workflow') || 'wf-demo'`, so a user who opens
 * the board with no parameter is shown a **fully rendered, confident board for a workflow that is not
 * theirs**. That is strictly worse than an empty state: an empty state tells the truth, a wrong
 * default asserts a falsehood with the full authority of a populated UI.
 *
 * # Why there is no search box
 *
 * The design system offers one, over a list the component holds. This console cannot: the list is
 * whatever this session has opened, at most a dozen rows, and a filter over a dozen rows a reader can
 * already see is a control that costs a client component and buys nothing. The identifier field below
 * is the real accelerator, and it reaches subjects the session has never seen — which is the case a
 * filter cannot help with at all.
 *
 * # Why the enumeration gap is stated on screen
 *
 * Because the honest empty state carries the NEXT ACTION, not a syntax lesson. `p25monitor.html`'s
 * error copy is literally instructions for editing a URL — *"No run_id in the URL. Append ?run_id=…"*
 * — which tells the user what the developer would have typed rather than what they should do.
 */
export function SubjectPicker({
  kind,
  visited,
  action,
  field,
  help,
  children,
}: {
  kind: SubjectKind;
  visited: Subject[];
  /** action is the route that resolves a typed identifier to its canonical route. */
  action: string;
  field: { name: string; label: string; placeholder?: string };
  help: string;
  /** children carries any additional field a two-part subject needs (a transform's revision). */
  children?: React.ReactNode;
}) {
  const label = SUBJECT_LABELS[kind];
  return (
    <>
      <Section title="Opened in this session" aside="held for this session only, never shared" id="recent">
        {visited.length === 0 ? (
          /*
           * 🔴 This sentence used to say "the platform does not expose a way to list your {label}s".
           *
           * That was true when it was written, and P27 made it false for RUNS: runs now carry an owning
           * organization and `/api/v1/runs` lists them. It is still true for the other three subjects,
           * which have no collection route — so the copy names what this list actually is rather than
           * making a claim about the platform that is now half wrong. A sentence that quietly stops
           * being true is worse than one that never said much.
           */
          <p className="hint">
            Nothing yet. This is a per-session shortcut list, held in your session and never shared —
            open a {label} below and it will be here next time.
          </p>
        ) : (
          <ul className="divide-y divide-border/50 overflow-hidden rounded-xl border border-border">
            {visited.map((subject) => (
              <li key={`${subject.kind}:${subject.id}`}>
                <Link
                  className="group flex items-center gap-3 px-4 py-3 transition-colors hover:bg-muted/30"
                  href={subject.href}
                >
                  <span className="min-w-0 flex-1">
                    <span className="mono block truncate text-sm text-foreground">{subject.label}</span>
                    {subject.hint ? (
                      <span className="mt-0.5 block truncate text-xs text-muted-foreground">
                        {subject.hint}
                      </span>
                    ) : null}
                  </span>
                  <ChevronRight
                    className="size-4 shrink-0 text-muted-foreground/40 transition-colors group-hover:text-primary"
                    aria-hidden="true"
                  />
                </Link>
              </li>
            ))}
          </ul>
        )}
      </Section>

      <form className="flex flex-col gap-4" method="get" action={action} aria-labelledby="open-title">
        <h2 className="font-display text-lg font-normal text-foreground" id="open-title">
          Open a {label} by identifier
        </h2>
        <div className="flex max-w-xl flex-col gap-4 rounded-xl border border-border bg-card p-5">
          <div className="field">
            <label htmlFor={field.name}>{field.label}</label>
            <input
              className="input mono"
              id={field.name}
              name={field.name}
              type="text"
              required
              spellCheck={false}
              autoComplete="off"
              placeholder={field.placeholder}
              aria-describedby={`${field.name}-help`}
            />
          </div>
          {children}
          <p className="hint" id={`${field.name}-help`}>
            {help}
          </p>
          <div>
            <button className="button button--primary" type="submit">
              Open
            </button>
          </div>
        </div>
      </form>
    </>
  );
}
