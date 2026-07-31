import Link from "next/link";
import { DocSearch } from "@/components/reading/search";
import { SECTIONS } from "@/lib/reading/content";

/**
 * The documentation 404 — designed as a page, not left as a default (task 5.7).
 *
 * # Why an unhappy path gets design effort
 *
 * The reader who lands here is in the worst position of anybody on this surface: they followed a link —
 * from a search engine, from a colleague, from a CLI error message that shipped inside a binary — and it
 * did not resolve. A blank "404" tells them the documentation is gone. The section index and a search box
 * tell them it moved, and give them the two ways to find it.
 *
 * This is also why removing or renaming a slug FAILS THE BUILD unless the same change adds a redirect
 * (Decision 8): this page is the safety net, not the plan.
 */
export default function DocsNotFound() {
  return (
    <div className="reading__frame">
      <div className="reading__doc">
        <p className="stat__label">Documentation</p>
        <h1 className="page__title font-display font-light tracking-tight">This page is not here</h1>
        <p className="hint mt-3 max-w-none text-sm">
          The address did not resolve to a documentation page. That is either a link that moved or a
          typo — either way, the two things below are how to find what you were reading.
        </p>

        <h2 className="section__title mt-8 font-display font-light tracking-tight">Sections</h2>
        <ul className="mt-3 flex list-none flex-col gap-2 p-0">
          {SECTIONS.map((section) => (
            <li key={section.id} className="rounded-xl border border-border bg-card p-4">
              <Link className="prose-link text-sm font-medium" href="/docs">
                {section.label}
              </Link>
              <p className="caption mt-1">{section.blurb}</p>
            </li>
          ))}
        </ul>

        <p className="hint mt-8 max-w-none">
          If you arrived from a command&rsquo;s output or an error message, the address it printed is a
          published contract and a broken one is a defect worth reporting — not something you should have
          to work around.
        </p>
      </div>

      <aside className="reading__aside">
        <DocSearch />
      </aside>
    </div>
  );
}
