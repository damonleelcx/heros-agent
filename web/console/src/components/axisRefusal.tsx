import type { ReactNode } from "react";

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
  // 🔴 Rewritten for P16. The previous note said context could not be rewritten at all, which stopped
  // being true the moment the Go materialiser landed — and a console that keeps saying "impossible"
  // about something the platform now does is worse than one that says nothing: the reader stops
  // believing the notes. Two things get a refusal on this axis now, and they are different problems
  // with different answers, so the note names both.
  context:
    "Context assembly is not a call-site argument — it is how the surrounding code builds the message list — so applying a policy means rewriting that code. Go and Python do this for policies that SELECT among the turns already written (a window, a compaction). It is declined for a policy that assembles at run time — a summary or a retrieval has no answer in your source to select — for a call site that does not write its messages out (an unpacked kwargs dict has no turns to select among), and for a language whose rewriter has not landed yet. The policy still runs where it belongs; what is declined is writing it into your source.",
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
 * Each applied change carries a sentence a reviewer can trust ALONE — the property the engine checked
 * before offering the diff. For a wiring transposition that is "the same lines, reordered"; for a
 * context window it is "no line added or removed, nothing constructed". A reviewer who believes only
 * that sentence already knows what the change cannot have done.
 *
 * 🔴 It is a REQUIRED PROP, not a constant. It was a constant until P16, hard-coded to the wiring
 * axis — and the first caller from another axis rendered "the file's lines were reordered" over a diff
 * that had deleted list elements, which is simply false. An invariant that is asserted by the component
 * rather than supplied by the caller is an invariant nobody checked against the change it describes,
 * and a false safety claim is worse than no claim: it is the sentence a reviewer stops reading the diff
 * because of. Requiring it means a new axis cannot inherit another axis's guarantee by omission.
 */
export function AxisApplied({
  axis,
  nodeId,
  what,
  invariant,
  diff,
  filename,
}: {
  axis: string;
  nodeId?: string;
  what: string;
  /** The property the engine CHECKED before offering this diff, in the caller's own words. */
  invariant: ReactNode;
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
        <p>{invariant}</p>
      </Banner>
      <Diff patch={diff} filename={filename} />
    </div>
  );
}
