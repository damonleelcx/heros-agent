"use client";

import { Banner, Section } from "@/components/primitives";
import { AxisEditor, CurrentValue, ReadOn, type AxisVocabulary, type CurrentValueProps } from "@/components/editorKit";
import { AXIS_DOC, type AxisSubject } from "@/lib/axisSubject";

/**
 * MemoryEditor is the memory axis, bound to the reader's own node (P17 20c §10, re-bound by P37).
 *
 * # 🔴 This file is where the editor kit came FROM
 *
 * The three rules it encoded are now in `components/editorKit.tsx`, unchanged, so all seven axes get
 * them and a fix to any of them is one fix:
 *
 *   1. THE BOUNDARY IS STATED BEFORE THE CHOICE — above the picker, naming the missing artifact. A
 *      reader who composes a change and only then meets a wall has been given a technically honest
 *      bait-and-switch.
 *   2. THE CONTROL IS LIVE, NOT DISABLED. A greyed-out control says nothing about WHY, and invites the
 *      belief that some other strategy, language or plan would unlock it.
 *   3. A REFUSAL IS NEVER RENDERED AS SUCCESS.
 *
 * What is left here is the ONE thing only this axis knows: what its boundary says. And it now says it
 * about the reader's own language, read from the engine's own coverage table at request time, rather
 * than from a mirrored constant that had to be edited when the runtime landed.
 *
 * # What the reader gains, and why it is not nothing
 *
 * Selecting a strategy resolves, hashes, seals a registry entry, records who authored it, and diffs
 * against the parent variant — a real configuration they can pin, compare and hand to a colleague, which
 * materializes unchanged the day the rewriter lands. Withholding all of that because a codemod is
 * missing would confuse *"we cannot write this into your source"* with *"you may not express this"*.
 */

/** MemoryBoundary is the platform's own statement, derived from `transform.CoverageFor("memory")`. */
export type MemoryBoundary = {
  applicable: boolean;
  missing_artifact?: string;
  reason?: string;
  language_is_the_blocker: boolean;
  authorable_anyway: boolean;
};

export function MemoryEditor({
  subject,
  vocabulary,
  boundary,
  current,
  readFailed,
}: {
  subject: AxisSubject;
  vocabulary: AxisVocabulary | null;
  boundary: MemoryBoundary | null;
  current: CurrentValueProps;
  readFailed?: string;
}) {
  if (!vocabulary || !boundary) {
    return (
      <div className="flex flex-col gap-4">
        <Section title="What this node carries today">
          <CurrentValue {...current} />
        </Section>
        <Banner tone="warn" title="The strategy vocabulary could not be read">
          {/*
            🔴 A read failure is NOT an empty vocabulary. "You have no strategies" and "we could not ask"
            are opposite facts with opposite next actions, and only one of them is about the reader.
          */}
          <p>Nothing has been lost, and retrying is safe.</p>
          {readFailed ? <p className="mono">{readFailed}</p> : null}
        </Banner>
        <ReadOn href={AXIS_DOC.memory}>The five strategies, and where a memory change reaches source</ReadOn>
      </div>
    );
  }

  return (
    <AxisEditor
      axis="memory"
      subject={subject}
      vocabulary={vocabulary}
      current={current}
      boundary={
        /*
          🔴 RULE 1 — the boundary, ABOVE the picker (FR15). It is derived from the ENGINE's coverage
          table rather than written here, which is what keeps it true: the day a rewriter lands for this
          language it starts saying yes without anyone remembering to edit copy. Its wording is reviewed
          as a customer-facing commitment (§8.1), not as layout text.
        */
        <Banner tone="info" title="Where a memory change reaches your source, and where it does not">
          <p>
            Choosing below is a real change wherever you are: it resolves, it produces a{" "}
            <span className="mono">config_hash</span>, and it is recorded against your identity.
          </p>
          <p>
            {boundary.applicable ? (
              <>
                A change on this node <strong>is</strong> written into your source. Two preconditions
                still apply: the call site must write its message list and assign the call&apos;s result,
                because memory is a read <em>and</em> a write.
              </>
            ) : (
              <>
                It is <strong>not</strong> written into{" "}
                <span className="mono">{subject.language || "this node's language"}</span> source yet.
                What is missing is{" "}
                <strong className="font-medium">
                  {boundary.missing_artifact || "that language's memory module and its call-site rewriter"}
                </strong>
                {boundary.language_is_the_blocker ? " — this language's, specifically" : ""}. It is refused
                by name rather than quietly applied.
              </>
            )}
          </p>
          <p>
            There is no plan, role or flag that changes this, and the controls below stay usable
            everywhere — a disabled control would tell you none of the above.
          </p>
          <p className="hint">
            {/*
              🔴 A LINK to the sibling axis rather than a second explanation of it (P17 D2). Memory and
              context are the pair readers conflate most often, and a surface that re-explains the other
              one is how a product acquires two definitions of each.
            */}
            On the wrong axis?{" "}
            <a className="prose-link" href="/app/context">
              Context is how one call builds its message list
            </a>
            .
          </p>
          <ReadOn href={AXIS_DOC.memory}>The five strategies, their preconditions, and the worked example</ReadOn>
        </Banner>
      }
    />
  );
}
