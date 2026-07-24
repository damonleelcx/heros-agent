"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useEffect, useState, useTransition } from "react";
import { setDensity, setTheme } from "@/lib/actions";
import { clock } from "@/lib/format";
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

/**
 * ConsoleClock is the wall clock in the chrome band.
 *
 * # Why the console shows the time at all
 *
 * Every live figure here states the instant it is current as of (FR34), and that statement is only
 * readable against a *now*. "As of 14:09:31 UTC" answers nothing on its own; beside a clock reading
 * 14:31:02 it answers loudly. Both are UTC and both are formatted at the single locale swap point, so
 * there is no timezone arithmetic between the two.
 *
 * # Why it renders a placeholder first
 *
 * The server has no idea what second the browser will paint on, so rendering a time during SSR would
 * be a hydration mismatch — and, worse, would put a frozen time on screen that looks live. The dashes
 * are the honest first paint: the clock is not running yet, and it says so.
 */
export function ConsoleClock() {
  const [now, setNow] = useState<Date | null>(null);
  useEffect(() => {
    setNow(new Date());
    const tick = setInterval(() => setNow(new Date()), 1000);
    return () => clearInterval(tick);
  }, []);
  return (
    <span className="chrome__clock">
      <span className="visually-hidden">Console clock: </span>
      {now ? clock(now) : "--:--:--"} UTC
    </span>
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

/**
 * ThemeToggle cycles the operator's colour theme (R17 / FR39).
 *
 * # Why a cycle rather than three buttons
 *
 * It sits in the chrome beside the density toggle, which is already a cycle, and the operator chrome is
 * the one place on this console where space is genuinely scarce — every view carries it. Three settings
 * with one control is the same trade the density toggle already made, and `aria-pressed` is not used
 * here precisely because this is not a binary: the label states the current setting, which a screen
 * reader announces on change.
 *
 * # 🔴 What must remain true in every setting (FR23)
 *
 * The operator console must stay distinguishable from the customer console at a glance in **all three**.
 * That is why the chrome band's identity is defined per theme rather than inverted: an operator with
 * both consoles open, acting cross-tenant while believing the view is single-tenant, is the named
 * failure this requirement exists to prevent — and a light theme that made the two look alike would
 * have introduced it by a new route.
 */
export function ThemeToggle({ theme }: { theme: "system" | "dark" | "light" }) {
  const [pending, start] = useTransition();
  const order = ["system", "dark", "light"] as const;
  const next = order[(order.indexOf(theme) + 1) % order.length];
  const label = { system: "System", dark: "Dark", light: "Light" }[theme];
  return (
    <button
      type="button"
      className="palette-trigger"
      disabled={pending}
      onClick={() => start(() => setTheme(next))}
      title="Colour theme. System follows the operating system; Dark and Light are explicit."
    >
      <span className="visually-hidden">Colour theme: </span>
      {label}
    </button>
  );
}
