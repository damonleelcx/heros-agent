import { requireSession } from "@/lib/session";
import { PageFrame, Section } from "@/components/primitives";
import { ImproveControls } from "./controls";

/**
 * The improvement-run surface (P35 §8).
 *
 * # What this page is for
 *
 * A person asked *fix it, and open a pull request.* This is where that happens — and where it visibly
 * stops happening whenever a gate says no.
 *
 * The whole flow is three explicit acts: ask for a plan, run the plan, decide on each proposal. None of
 * them happens on a page load, and none of them is bundled with another.
 *
 * # 🚫 What is not on this page, by decision
 *
 * **An "approve all".** Design D4 predicts the request and refuses it in advance: a bundle approval is
 * one click that means several things, and the person will read the first item and accept the rest. It
 * is the most predictable and most dangerous convenience in this phase, so the refusal is structural —
 * there is no component here that takes a list of proposals.
 *
 * **A merge button.** The platform opens pull requests. Auto-merge is the Autonomous automation level
 * and is Enterprise-only, and it is not reachable from this surface at any plan.
 *
 * **A number this console computed.** Every delta, interval, sentence and per-axis count arrives from
 * the platform. P9's founding rule holds with more force here than anywhere else on the console,
 * because these are the numbers somebody acts on by merging a change into their repository.
 *
 * # The three things the layout has to make legible
 *
 *  1. **The plan exists before the money.** It is drawn as a projection, in its own treatment, and the
 *     button under it is a separate press.
 *  2. **A decided proposal stays.** Declined ones are recessed, not removed — a proposal that vanished
 *     when it was declined looks exactly like one that was never made.
 *  3. **Approved → applied → withdrawn is a sequence, not a failure.** It has its own designed state,
 *     carrying BOTH measurements at equal weight, because a withdrawal with one number looks like a bug
 *     and with two it is a finding.
 */
export const dynamic = "force-dynamic";

export default async function ImprovePage() {
  await requireSession();

  return (
    <PageFrame
      eyebrow="Improve"
      title="Ask for a change you can prove"
      wide
    >
      <p className="max-w-prose text-sm leading-relaxed text-muted-foreground">
        Ask in English. A question becomes a bounded plan — what will be looked at, how many changes will
        be tried, what it may spend, and when it stops — and you see the plan before anything runs. Then
        each change that proves itself on held-out data is offered to you on its own, with its measured
        delta and its diff, and you approve them one at a time.
      </p>

      <Section title="One question, one plan, one decision at a time">
        <ImproveControls />
      </Section>

      <Section title="What this does not do">
        <ul className="flex max-w-prose list-disc flex-col gap-2 pl-4 text-sm leading-relaxed text-muted-foreground">
          <li>
            <strong className="text-foreground">It does not merge.</strong> It opens a pull request with
            the evidence attached. You review it and merge it yourself. Merging automatically is a
            different automation level and it is not reachable from this page.
          </li>
          <li>
            <strong className="text-foreground">It does not approve in bulk.</strong> Each change is its
            own decision with its own diff. There is no control here that accepts several at once, and
            that is deliberate rather than unfinished.
          </li>
          <li>
            <strong className="text-foreground">It can withdraw a change after you approve it.</strong>{" "}
            Every approved change is re-measured after it is applied, and one that fails to reproduce its
            verified delta is stopped before it reaches your repository — with both measurements shown.
            That reads like a failure and is the product working.
          </li>
          <li>
            <strong className="text-foreground">It does not run unbounded.</strong> A question that
            cannot be turned into a plan with a budget and a stopping condition is refused, not run with
            bounds nobody chose.
          </li>
        </ul>
      </Section>
    </PageFrame>
  );
}
