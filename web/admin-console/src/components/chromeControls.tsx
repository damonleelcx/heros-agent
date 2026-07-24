"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useTransition } from "react";
import { setDensity } from "@/lib/actions";
import type { Surface } from "@/lib/surfaces";

/**
 * chromeControls.tsx holds the two chrome pieces that need to know the browser's state.
 *
 * `PrimaryNav` marks the current section with `aria-current` — which the stylesheet renders as a rule
 * as well as a colour, because a current-section cue carried by colour alone is invisible to a
 * colour-blind operator and to anyone reading the accessibility tree.
 *
 * `DensityToggle` switches the operator's row density (FR29). It is a preference, so it carries no
 * reason, no confirmation and no audit entry: friction is for the write path to the PLATFORM, and
 * spending it here would train operators to click through the confirmations that matter.
 */

export function PrimaryNav({ items }: { items: Surface[] }) {
  const pathname = usePathname();
  return (
    <>
      {items.map((item) => {
        const current = item.href === "/" ? pathname === "/" : pathname.startsWith(item.href);
        return (
          <Link key={item.href} href={item.href} aria-current={current ? "page" : undefined}>
            {item.label}
          </Link>
        );
      })}
    </>
  );
}

export function DensityToggle({ density }: { density: "comfortable" | "compact" }) {
  const [pending, start] = useTransition();
  const next = density === "compact" ? "comfortable" : "compact";
  return (
    <button
      type="button"
      className="palette-trigger"
      disabled={pending}
      aria-pressed={density === "compact"}
      onClick={() => start(() => setDensity(next))}
      title="Row density. Compact tightens the rhythm; it never hides information."
    >
      {density === "compact" ? "Compact" : "Comfortable"}
    </button>
  );
}
