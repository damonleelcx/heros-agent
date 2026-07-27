"use client";

import type { ReactNode } from "react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { cx } from "@/lib/cx";

/**
 * NavLink marks the current surface with `aria-current="page"` (FR9).
 *
 * A client component for one reason: only the browser knows the current path, and the shell renders
 * on the server. It carries no data and makes no request.
 *
 * The current item is marked by a WORD (`aria-current`) as well as by a surface and a hue — the same
 * rule that governs every status in this console. A navigation whose current item is distinguished
 * only by colour tells a screen-reader user nothing about where they are, and tells a colour-blind
 * reader very little.
 */
export function NavLink({
  href,
  children,
  icon,
  exact,
}: {
  href: string;
  children: ReactNode;
  icon?: ReactNode;
  exact?: boolean;
}) {
  const pathname = usePathname();
  const current = exact ? pathname === href : pathname === href || pathname.startsWith(`${href}/`);
  return (
    <Link
      className={cx(
        "flex items-center gap-2.5 rounded-lg px-2.5 py-1.5 text-xs transition-colors",
        current
          ? "bg-primary/10 font-medium text-primary"
          : "text-muted-foreground hover:bg-muted/40 hover:text-foreground",
      )}
      href={href}
      aria-current={current ? "page" : undefined}
    >
      {icon ? (
        <span className="shrink-0 [&>svg]:size-3.5" aria-hidden="true">
          {icon}
        </span>
      ) : null}
      {children}
    </Link>
  );
}

/**
 * SubjectLink is the cross-surface navigation for a subject (§7.5).
 *
 * The point is that moving graph → board → proposals never re-asks which workflow. The subject is in
 * the path, so each of these is a link rather than a URL edit — which is what the four legacy pages
 * required, because they had no links between them at all.
 */
export function SubjectLink({ href, children }: { href: string; children: ReactNode }) {
  const pathname = usePathname();
  const current = pathname === href;
  return (
    <Link
      className={cx(
        "rounded-md px-2.5 py-1 font-mono text-xs transition-colors",
        current
          ? "bg-primary/10 text-primary"
          : "text-muted-foreground hover:bg-muted/40 hover:text-foreground",
      )}
      href={href}
      aria-current={current ? "page" : undefined}
    >
      {children}
    </Link>
  );
}

/**
 * SubjectStrip is the band a subject's surfaces hang off, directly under the shell header.
 *
 * It is sticky beneath the header rather than scrolling away, because its whole job is to answer
 * "which one am I looking at, and where else can I take it?" — a question that arrives most often
 * halfway down a long table.
 *
 * # 🔴 Why the offset is top-0 and not top-12
 *
 * It used to be `top-12`, to sit below a 3rem shell header — correct while the DOCUMENT scrolled and
 * this strip shared a scroll container with that header. The viewport-first shell (NFR17) moved the
 * header OUT of the scrolling region: `<main>` is now `md:overflow-hidden` with a bounded height, so it
 * is the containing block a sticky child resolves against, and the header is its sibling.
 *
 * Against that container `top-12` displaces the strip 48px DOWNWARD from its flow position, where it
 * covers the first 48px of whatever the page renders next — which on every workflow surface is
 * PageFrame's eyebrow and the top half of its <h1>. The page then looks like it has no eyebrow and a
 * cropped title, and nothing in PageFrame is wrong, so that is where the search does not end.
 *
 * `top-0` pins it to the top of its own scroll container, which is what "sticky beneath the header"
 * means now that the header is not in that container.
 */
export function SubjectStrip({
  kind,
  id,
  children,
}: {
  kind: string;
  id: string;
  children: ReactNode;
}) {
  return (
    <nav
      className="sticky top-0 z-10 flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-border bg-background/90 px-5 py-2 backdrop-blur md:px-10"
      aria-label={`This ${kind.toLowerCase()}`}
    >
      <span className="font-mono text-[10px] uppercase tracking-widest text-muted-foreground">{kind}</span>
      <span className="mono max-w-[16rem] truncate text-xs text-foreground/80">{id}</span>
      <span className="flex flex-wrap items-center gap-1">{children}</span>
    </nav>
  );
}
