import { PageFrame } from "@/components/primitives";
import { HarnessAuthoring } from "@/app/app/harness/authoring";

/**
 * A self-contained preview of the P18 harness picker (task 12.3).
 *
 * # Why a preview page exists at all
 *
 * The harness picker's two hard parts are things a string assertion cannot check:
 *
 *   - the TURN METER, which exists precisely because a number in a sentence is skippable and a filled
 *     bar is not. Whether it actually renders at a size a reader notices, and whether "up to 16×" reads
 *     as bigger than "up to 3×", is a question about pixels;
 *   - the LANGUAGE SWITCH, whose whole purpose is that the badges CHANGE when it moves. A green build is
 *     entirely compatible with a switch that re-renders nothing.
 *
 * `tests/harness.test.mjs` proves the surface says the right words. This page is where someone can see
 * that it shows them. It uses only the root layout — no session, no BFF — which is why it lives outside
 * `/app`: a surface that needs a live platform to look at is a surface nobody looks at.
 *
 * # What is shared with the real surface
 *
 * `HarnessAuthoring` is imported from `/app/harness`, not reimplemented. A copy would drift the first
 * time the picker changed, and then this page would be a picture of a surface rather than the surface.
 */
export const dynamic = "force-dynamic";

export default function P18Preview() {
  return (
    <PageFrame
      eyebrow="Preview · P18"
      title="How many calls this node makes, and what makes it stop"
      lede={
        <>
          The harness picker, rendered without a session so it can be looked at. Move the language switch
          and watch the badges change: that per-cell answer is the whole design, and three of the five
          strategies do not move — they are refused in every language, permanently.
        </>
      }
    >
      <HarnessAuthoring />
    </PageFrame>
  );
}
