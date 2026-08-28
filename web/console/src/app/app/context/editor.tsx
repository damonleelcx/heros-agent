"use client";

import { Banner } from "@/components/primitives";
import { AxisEditor, ReadOn, type CurrentValueProps } from "@/components/editorKit";
import { contextVocabularyFrom } from "@/lib/axisVocabularyShapes";
import { AXIS_DOC, type AxisSubject } from "@/lib/axisSubject";

/**
 * ContextEditor binds the shared kit to the context axis (P37 FR5).
 *
 * # What is here rather than in the kit
 *
 * Exactly one thing: **the boundary**. Everything else — the picker, the params form, validation at save,
 * the preflight and the `unverified` stamp — is `AxisEditor`, unchanged, so a fix to any of them is one
 * fix rather than seven.
 *
 * The boundary is per-axis because it says something only this axis knows: what a lossy policy costs,
 * and that a policy over this node's tolerance is refused BEFORE any evaluation spend.
 *
 * # 🔴 The boundary is rendered ABOVE the picker (FR15)
 *
 * Not below the submit button, not in a tooltip, and not on the reading surface. A reader who composes a
 * change and only then meets a wall has been given a technically honest bait-and-switch, and on this
 * axis the wall is expensive: the policies readers most want — summarization, retrieval — are exactly the
 * ones that never reach source in any language.
 *
 * §8.1 reviews this wording as a customer-facing commitment rather than as layout text.
 */
export function ContextEditor({
  subject,
  coverage,
  setVersion,
  current,
}: {
  subject: AxisSubject;
  coverage: { policy: string; mode: string; reason?: string }[];
  setVersion: string;
  current: CurrentValueProps;
}) {
  const vocabulary = contextVocabularyFrom(coverage, setVersion);

  if (!vocabulary) {
    return (
      <div className="flex flex-col gap-4">
        <Banner tone="warn" title="This node's language has no context rewriter yet">
          <p>
            Applying a policy means rewriting how this code builds its message list, and the rewriter for{" "}
            <span className="mono">{subject.language || "this language"}</span> has not landed. Nothing in
            your code unlocks it and no plan changes it. This one is ours.
          </p>
        </Banner>
        <ReadOn href={AXIS_DOC.context}>What each policy does, and which languages have a rewriter</ReadOn>
      </div>
    );
  }

  return (
    <AxisEditor
      axis="context"
      subject={subject}
      vocabulary={vocabulary}
      current={current}
      boundary={
        <Banner tone="info" title="What a context change costs before it is worth anything">
          <p>
            A policy that summarises or compacts throws information away. One that would push this node
            past the loss its job can afford is <strong>refused when it is proposed</strong>, before it
            becomes a diff and before it costs an evaluation run. Both numbers are shown, because either
            could be the thing to change.
          </p>
          <ReadOn href={AXIS_DOC.context}>What a node tolerates, and why a drop ratio is not a saving</ReadOn>
        </Banner>
      }
    />
  );
}
