import { Check } from "lucide-react";
import { capability } from "@/content/capabilities";

/**
 * Claim renders one capability claim, and is the ONLY way the public surface may make one (FR33).
 *
 * # Two gates, one at each end of the build
 *
 * `scripts/scan-claims.mjs` reads the `id` out of the source before the build and fails on one that is
 * unlisted or not shipped. `capability()` THROWS at render time on the same condition. Belt and
 * braces, because the two catch different mistakes: the scan catches a claim added without a manifest
 * entry, and the throw catches a manifest entry that was flipped to `shipped: false` while a page still
 * claims it.
 *
 * # Why the boundary renders beside the claim, not below the fold
 *
 * A benefit without its boundary is the sentence a customer quotes back during an escalation. Stating
 * what the capability deliberately does NOT do, in the same block, is what turns an overstated claim
 * into a qualified lead — and the questions a prospect arrives with are the next round's requirements
 * input.
 *
 * The design system draws this as a tick beside a benefit. The tick is kept and the boundary is kept
 * at a readable size beneath it — not greyed to the point of being decorative, because a boundary
 * nobody reads is a boundary that was not stated.
 */
export function Claim({ id }: { id: string }) {
  const entry = capability(id);
  return (
    <li className="claim flex gap-4 rounded-xl border border-marketing-ink/10 bg-marketing-ink/3 p-5">
      <span
        className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full border border-marketing-accent/25 bg-marketing-accent/15"
        aria-hidden="true"
      >
        <Check className="size-3 text-marketing-accent" />
      </span>
      <span className="min-w-0">
        <span className="claim__what block text-sm font-medium text-marketing-ink/85">{entry.claim}</span>
        <span className="claim__boundary mt-1.5 block text-xs leading-relaxed text-marketing-ink/45">
          <span className="mr-1.5 font-mono text-marketing-ink/30">limit</span>
          {entry.boundary}
        </span>
      </span>
    </li>
  );
}
