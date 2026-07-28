import { Banner, Chip, Row } from "@/components/primitives";
import { Disclosure } from "@/components/figure";
import { Diff } from "@/components/diff";

/**
 * axisRefusal.tsx renders the outcome where the platform DECLINED a change it fully understood.
 *
 * # Why a refusal needs its own shape rather than the failure banner
 *
 * A failure means something went wrong and a retry might work. A refusal means the opposite: the spec
 * was read, resolved, and hashed, and then the platform said no — because materialising that one axis
 * at the call site is not something it can do safely. Re-submitting produces the same answer.
 *
 * Rendering the second as the first is not a cosmetic mistake. It sends the reader to retry a thing
 * that will never be accepted, and it hides the sentence that would have told them what to do instead.
 * The P14 review surface already learned this for proposals (`RefusalNotice`); this is the same
 * distinction on the SUBMIT path, where the refusal arrives as an HTTP 400 naming a dimension.
 *
 * # Why the axis note is a table and not a sentence per screen
 *
 * Each axis is refused for its OWN reason, and the reason is what makes the refusal actionable. A
 * single generic line ("this change could not be applied") would be true of all of them and useful for
 * none. The verbatim platform sentence is still shown — under a disclosure, because it is written for
 * the person who has to act on it and must never be re-worded, but it is long.
 */
export const AXIS_NOTE: Record<string, string> = {
  wiring:
    "The node graph is part of a configuration and part of its hash, so this spec is a real, different configuration. What does not exist yet is a rewriter that moves, fuses, or deletes a call at the source. Applying it as a no-op would let this configuration be scored against code that was never rearranged, so it is declined instead.",
  skills:
    "Binding a skill means constructing SDK-specific tool values at the call site. That materialiser exists for Go; for this call site's language it does not, so the binding is declined rather than dropped from the diff.",
  context:
    "Context assembly is not a call-site argument — it is how the surrounding code builds the message list — so there is no expression to rewrite here.",
};

/**
 * AxisRefusal is the declined-change card.
 *
 * `axis` is the dimension the platform named. An axis with no note still renders — the platform's own
 * sentence carries it — because a refusal the console cannot annotate is still a refusal the reader
 * must see, and silently swallowing an unrecognised one would be the worst of both.
 */
export function AxisRefusal({
  axis,
  nodeId,
  message,
}: {
  axis: string;
  nodeId?: string;
  message: string;
}) {
  const note = AXIS_NOTE[axis];
  return (
    <Banner tone="warn" title={`This change was declined on the ${axis} axis`}>
      <Row>
        <Chip tone="warn" title="the axis the platform declined">
          {axis}
        </Chip>
        {nodeId ? <Chip title="the node the refusal is anchored to">{nodeId}</Chip> : null}
      </Row>
      <p>
        <strong>Declined, not attempted — and not a failure to retry.</strong> Nothing was persisted: no
        spec, no transform, no run. Re-submitting the same spec will be declined again.
      </p>
      {note ? <p>{note}</p> : null}
      <Disclosure summary="What the platform said, verbatim">
        <p className="mono break-words text-sm leading-relaxed">{message}</p>
      </Disclosure>
    </Banner>
  );
}

/**
 * AxisApplied is the declined card's counterpart: a wiring change the platform DID materialize.
 *
 * # Why the applied state needs a card of its own
 *
 * Until this shipped, every wiring change ended in a refusal, and a surface that only ever says no
 * teaches its reader that the axis does not work. But the answer is not to soften the refusals — it is
 * to show the one shape that applies, with the diff it produced, so the two outcomes are visibly
 * different things rather than two shades of "something happened".
 *
 * # The invariant is stated, because it is the whole safety argument
 *
 * A reorder is control-flow surgery in general. This one is not: it exchanges two adjacent statements,
 * and the file that comes out has THE SAME LINES as the file that went in, reordered. A reviewer who
 * trusts only that sentence already knows the change cannot have altered what any line says — which is
 * why the sentence is on the card rather than in a design document.
 */
export function AxisApplied({
  axis,
  nodeId,
  what,
  diff,
  filename,
}: {
  axis: string;
  nodeId?: string;
  what: string;
  diff: string;
  filename?: string;
}) {
  return (
    <div className="flex flex-col gap-4">
      <Banner tone="info" title={`This change was applied on the ${axis} axis`}>
        <Row>
          <Chip tone="ok" title="the axis this change moved">
            {axis}
          </Chip>
          {nodeId ? <Chip title="the node the change is anchored to">{nodeId}</Chip> : null}
        </Row>
        <p>{what}</p>
        <p>
          <strong>The file&apos;s lines were reordered and nothing else changed.</strong> Same line
          count, same lines — so this change cannot have altered what any line says, only when it runs.
          That is checked before the diff is offered, not asserted here.
        </p>
      </Banner>
      <Diff patch={diff} filename={filename} />
    </div>
  );
}
