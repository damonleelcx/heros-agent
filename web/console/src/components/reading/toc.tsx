"use client";

import { useEffect, useState } from "react";
import { cx } from "@/lib/cx";
import type { Heading } from "@/lib/reading/markdown";

/**
 * Toc is the reading surface's table of contents — a `nav` LANDMARK with scroll-spy (task 3.3).
 *
 * # 🔴 The current section is marked by `aria-current` AND BY A WORD, never by colour alone
 *
 * This is the console's existing rule (a status carries a word; an unknown status cannot impersonate a
 * known one) applied to a table of contents, and it is the reason this component exists rather than a
 * list of links with a highlighted item. A reader with a colour-vision deficiency, a reader on a
 * monochrome print-out and a screen-reader user all get the same answer to "where am I" — from the
 * markup, not from a hue.
 *
 * The word is `Reading` and it is `visually-hidden` for sighted readers, who already have the bar and
 * the weight change. It is not hidden from assistive technology, which is the point.
 *
 * # Why this is a client island, and why it is one of only two
 *
 * Scroll position is a browser fact. Nothing else on this surface needs the browser: every document is
 * server-rendered Markdown, which is what keeps the payload budget unchanged and what makes the surface
 * READABLE WITH JAVASCRIPT DISABLED (task 3.6).
 *
 * With JavaScript off, this renders as a plain list of anchor links that all work — the browser's own
 * fragment navigation does the job. Nothing spins, nothing is blank, and the only thing lost is the
 * "you are here" mark. That degradation is deliberate and is asserted by a test.
 *
 * # What it deliberately does not do
 *
 * It does not rewrite the URL as the reader scrolls. A history entry per heading makes the back button
 * useless — one press should leave the document, not walk back up it.
 */
export function Toc({ headings, label = "On this page" }: { headings: Heading[]; label?: string }) {
  const [active, setActive] = useState<string | null>(null);

  useEffect(() => {
    if (headings.length === 0) return;
    const targets = headings
      .map((heading) => document.getElementById(heading.slug))
      .filter((el): el is HTMLElement => el !== null);
    if (targets.length === 0) return;

    /*
     * The topmost heading whose top has passed the reading line is the current one.
     *
     * An IntersectionObserver that simply takes "the first intersecting entry" gets this wrong in the
     * case that matters most: a short section wholly above the fold stops intersecting while the reader
     * is still in it, and the mark jumps ahead of them. Computing from `getBoundingClientRect` on the
     * observer's callback keeps the answer to the question a reader is actually asking.
     */
    function recompute() {
      const line = window.innerHeight * 0.25;
      let current: string | null = targets[0]?.id ?? null;
      for (const target of targets) {
        if (target.getBoundingClientRect().top <= line) current = target.id;
        else break;
      }
      setActive(current);
    }

    recompute();
    const observer = new IntersectionObserver(recompute, {
      rootMargin: "0px 0px -70% 0px",
      threshold: [0, 1],
    });
    for (const target of targets) observer.observe(target);
    window.addEventListener("scroll", recompute, { passive: true });
    window.addEventListener("resize", recompute);
    return () => {
      observer.disconnect();
      window.removeEventListener("scroll", recompute);
      window.removeEventListener("resize", recompute);
    };
  }, [headings]);

  if (headings.length === 0) return null;

  return (
    <nav className="toc" aria-labelledby="toc-label">
      <p className="toc__label" id="toc-label">
        {label}
      </p>
      <ol className="toc__list">
        {headings.map((heading) => {
          const current = heading.slug === active;
          return (
            <li key={heading.slug} className={cx("toc__item", heading.level > 2 && "toc__item--nested")}>
              <a
                className={cx("toc__link", current && "toc__link--current")}
                href={`#${heading.slug}`}
                // `aria-current="location"` is the specified value for "the current item within a set of
                // navigation links". `true` would be a weaker, vaguer claim about the same thing.
                aria-current={current ? "location" : undefined}
              >
                {heading.text}
                {current ? <span className="visually-hidden"> — Reading</span> : null}
              </a>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
