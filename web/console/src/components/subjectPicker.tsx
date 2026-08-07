import Link from "next/link";
import { ChevronRight, CircleSlash, TriangleAlert } from "lucide-react";
import type { SubjectKind } from "@/lib/subjects";
import { SUBJECT_LABELS } from "@/lib/subjects";
import type { Enumeration } from "@/lib/enumeration";
import { Section } from "./primitives";

/**
 * SubjectPicker is how the console asks "which one?" (FR10, R8).
 *
 * # What it offers, in order
 *
 * 1. **Everything this organization has**, from the platform's own subject index (P29 §4), with the
 *    ones this session opened ordered first.
 *
 *    🔴 That first clause is new, and the sentence it replaces is worth keeping in view: this component
 *    used to list only *"subjects this session already opened"*, because the platform exposed no
 *    enumeration endpoint for any of them. A developer who linked a run, closed the tab and came back
 *    the next day found a console that had forgotten their workflow existed — the data was durable the
 *    whole time and nothing could ask for it.
 *
 *    The session list is now an ORDERING HINT. A remembered subject the enumeration does not contain is
 *    DISCARDED rather than rendered: a session's memory is not evidence that a subject still exists, and
 *    a picker that offers a door which does not open is worse than one that offers fewer doors.
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
  available,
  discarded = 0,
  action,
  field,
  help,
  children,
}: {
  kind: SubjectKind;
  /**
   * available is the platform's own list, already ordered (session-visited first). Its STATE is carried
   * with it, unflattened, so this component renders three different sentences rather than one empty
   * list for three different facts.
   */
  available: Enumeration;
  /** discarded counts remembered subjects the enumeration does not contain — surfaced, never silent. */
  discarded?: number;
  /** action is the route that resolves a typed identifier to its canonical route. */
  action: string;
  field: { name: string; label: string; placeholder?: string };
  help: string;
  /** children carries any additional field a two-part subject needs (a transform's revision). */
  children?: React.ReactNode;
}) {
  const label = SUBJECT_LABELS[kind];
  const subjects = available.subjects;
  return (
    <>
      <Section
        title={`Your ${label}s`}
        aside={
          subjects.length > 0
            ? `${subjects.length} · the ones you opened this session are first`
            : undefined
        }
        id="recent"
      >
        {/*
          🔴 THREE STATES, THREE SENTENCES, and never one empty list for all of them.

          `empty` is a fact about the reader's data and its next action is to produce some.
          `read-failed` is a fact about US and its next action is to wait — rendering it as "you have
          none" tells a returning customer their history is gone when it is not, which is precisely how
          a release reads as data loss.
          `not-mounted` is a POLICY answer: this deployment does not carry the capability at all.
        */}
        {available.state === "not-mounted" ? (
          <div className="state state--empty flex flex-col items-center">
            <CircleSlash className="mb-3 size-6 text-muted-foreground/40" aria-hidden="true" />
            <p className="state__title text-foreground">
              This deployment does not carry a {label} index
            </p>
            <div className="state__body text-center">
              <p className="hint">
                Nothing is missing and nothing failed — the capability is not served here. You can still
                open a {label} by identifier below.
              </p>
            </div>
          </div>
        ) : available.state === "read-failed" ? (
          <div className="state state--empty flex flex-col items-center">
            <TriangleAlert className="mb-3 size-6 text-[var(--warn-fg,theme(colors.amber.500))]" aria-hidden="true" />
            <p className="state__title text-foreground">Your {label}s could not be read</p>
            <div className="state__body text-center">
              <p className="hint">
                This is not the same as having none. Nothing has been lost; the platform did not answer.
                Retrying is safe, and the identifier field below still works.
              </p>
              {available.detail ? <p className="hint mono mt-2">{available.detail}</p> : null}
            </div>
          </div>
        ) : subjects.length === 0 ? (
          <p className="hint">
            No {label}s yet for this organization. This list comes from the platform, not from your
            browser — so it will be here on any machine you sign in from.
          </p>
        ) : (
          <ul className="divide-y divide-border/50 overflow-hidden rounded-xl border border-border">
            {subjects.map((subject) => (
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
        {discarded > 0 ? (
          <p className="hint">
            {discarded} {discarded === 1 ? "shortcut" : "shortcuts"} from this session{" "}
            {discarded === 1 ? "is" : "are"} not shown: the platform no longer lists{" "}
            {discarded === 1 ? "it" : "them"}. They are discarded rather than offered, because a link
            that does not open is worse than one that is absent.
          </p>
        ) : null}
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
