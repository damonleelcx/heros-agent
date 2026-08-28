"use client";

import { Banner, Card, Chip, Row, Section } from "@/components/primitives";
import { ApplyModeNote, ProviderBoundary } from "@/components/authoring";
import { AxisEditor, CurrentValue, ReadOn, type AxisVocabulary, type CurrentValueProps } from "@/components/editorKit";
import { AXIS_DOC, type AxisSubject } from "@/lib/axisSubject";

/**
 * StudioAuthoring is the MODEL axis, bound to the reader's own node (P37, §1.6 folded `/app/studio` into
 * full scope).
 *
 * # What this stopped being
 *
 * A table of three fixture nodes — `classify`, `summarize`, `answer` — with a hard-coded list of three
 * anthropic models and a fixture refusal. Every one of them was the platform's own demonstration, and
 * every one has moved to `/docs/concepts/prompt-and-model-studio`, labelled as such.
 *
 * # 🔴 What did NOT move
 *
 *   · the reader's own node, its current model, and its apply mode — now read from the IR
 *   · the PROVIDER BOUNDARY, stated above the picker (FR15): a call site written against one SDK does
 *     not become another by changing a model string
 *   · a model from another provider rendered DISABLED, naming the SDK it needs (FR7) — never hidden
 *   · `unverified` on everything (FR16)
 *   · "authoring is not an evaluator"
 *
 * The provider boundary is the sharpest one in the console: offering the wrong provider's model would
 * produce a diff that compiles and then calls the wrong provider in production. Silent, and in
 * production.
 */
export function StudioAuthoring({
  subject,
  vocabulary,
  current,
  applyMode,
}: {
  subject: AxisSubject;
  /** vocabulary is the registered catalogue, with other providers' models disabled and named. */
  vocabulary: AxisVocabulary | null;
  current: CurrentValueProps;
  /** applyMode decides whether provider parameters are data or code at this call site. */
  applyMode: "inline" | "bound" | "unknown";
}) {
  if (!vocabulary) {
    return (
      <div className="flex flex-col gap-4">
        <Section title="What this node runs today">
          <CurrentValue {...current} />
        </Section>
        <Banner tone="warn" title="The model catalogue could not be read">
          <p>
            Not the same as an empty catalogue. Nothing has been lost; retrying is safe.
          </p>
        </Banner>
        <ReadOn href={AXIS_DOC.model}>Why the model list offered for a call site is short</ReadOn>
      </div>
    );
  }

  return (
    <div className="flex flex-col gap-6">
      <AxisEditor
        axis="model"
        subject={subject}
        vocabulary={vocabulary}
        current={current}
        boundary={
          <Banner tone="info" title="What this call site can be changed to, and what it cannot">
            {/*
              🔴 FR15 — above the picker. And FR7's other half: the models this call site cannot take are
              LISTED AND DISABLED below, naming the SDK each would need. A silently short list reads as an
              incomplete catalogue, and the reader's next move is to file a bug.
            */}
            <p>
              A call site written against one SDK does not become another by changing a model string.
              Models from other providers are shown, disabled, naming the SDK they need.
            </p>
            <ProviderBoundary provider={current.detail ?? "this node's"} />
            <ReadOn href={AXIS_DOC.model}>Why the model list is short, and what a bound node can carry</ReadOn>
          </Banner>
        }
      />

      <Section title="What this node can carry">
        <Card>
          <Row>
            <Chip title="inline writes values at the call site; bound carries them as data">{applyMode}</Chip>
            <Chip title="whether provider parameters can be set on this node">
              {applyMode === "bound" ? "parameters can be carried" : "parameters are code here"}
            </Chip>
          </Row>
          {applyMode === "unknown" ? null : <ApplyModeNote mode={applyMode} />}
        </Card>
        <ReadOn href={AXIS_DOC.model}>Bound and inline, and what each one lets you change</ReadOn>
      </Section>
    </div>
  );
}
