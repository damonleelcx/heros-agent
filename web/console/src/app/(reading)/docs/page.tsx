import Link from "next/link";
import { DocSearch } from "@/components/reading/search";
import { cachedDocs } from "@/lib/reading/cache";
import { SECTIONS } from "@/lib/reading/content";

/**
 * `/docs` — the section index, and the surface's browsable fallback.
 *
 * # Why the index is a real page rather than a redirect to the quickstart
 *
 * Two readers arrive here and they want opposite things. One has never seen the product and wants the
 * one path that works; the other has used it for a month and wants the page about the one thing they are
 * stuck on. A redirect serves the first and abandons the second.
 *
 * It is also the **JavaScript-off degradation for search** (Decision 9): with scripting disabled the
 * search box is inert, and what remains is this — a browsable table of contents, not a blank panel and
 * not a spinner.
 */
/**
 * # 🔴 Why this route is DYNAMIC and not prerendered
 *
 * It was `force-static`, and clicking it in a real browser is what caught the defect: **both client
 * islands were dead on every reading page.**
 *
 * `middleware.ts` sets a per-request, nonce-based CSP — `script-src 'self' 'nonce-…' 'strict-dynamic'`.
 * A nonce is minted per REQUEST, so it cannot exist in HTML generated at BUILD time. The prerendered
 * page shipped script tags with no nonce, the response carried a CSP that admits only nonced scripts,
 * and the browser refused all of them. The build was green, the page rendered perfectly, and the table of
 * contents never marked a section. `curl` confirms it: the static page has zero `nonce=` attributes and
 * `/signin` — which is dynamic — has one on every script.
 *
 * The two alternatives were both worse. Relaxing to `'unsafe-inline'` for this route group buys a
 * scroll-spy by removing the control that protects the page where a customer reads what they are agreeing
 * to (level 1, security). Dropping the islands means no "you are here" and no search at all (level 3).
 *
 * **Dynamic rendering costs nothing that matters here.** NFR1's availability property is "this surface
 * makes no platform call", not "this surface is static HTML" — and that property is unchanged and still
 * asserted by the harness's upstream-request counter. The corpus is read from the image's own filesystem
 * and memoised for the process's lifetime (`lib/reading/cache.ts`), because content cannot change under a
 * running container.
 */
export const dynamic = "force-dynamic";

export default async function DocsIndexPage() {
  const pages = await cachedDocs();
  const bySection = new Map<string, typeof pages>();
  for (const page of pages) {
    if (page.slug === "index") continue;
    const list = bySection.get(page.section) ?? [];
    list.push(page);
    bySection.set(page.section, list);
  }
  for (const list of bySection.values()) {
    list.sort((a, b) => {
      const ao = a.frontMatter.order ?? Number.MAX_SAFE_INTEGER;
      const bo = b.frontMatter.order ?? Number.MAX_SAFE_INTEGER;
      return ao === bo ? a.frontMatter.title.localeCompare(b.frontMatter.title) : ao - bo;
    });
  }

  return (
    <div className="reading__frame">
      <div className="reading__doc">
        <p className="stat__label">Documentation</p>
        <h1 className="page__title font-display font-light tracking-tight">Heros documentation</h1>
        <p className="hint mt-3 max-w-none text-sm">
          Install the CLI, build a discovery graph on your own repository, and read the evidence behind a
          change. Every page states the platform version it documents and the boundary of what the
          capability deliberately does not do.
        </p>

        <div className="mt-8 flex flex-col gap-10">
          {SECTIONS.map((section) => {
            const list = bySection.get(section.id) ?? [];
            return (
              <section key={section.id} className="flex flex-col gap-3">
                <h2 className="section__title font-display font-light tracking-tight">{section.label}</h2>
                <p className="hint max-w-none">{section.blurb}</p>
                {list.length === 0 ? (
                  <p className="caption">No pages in this section yet.</p>
                ) : (
                  <ul className="flex list-none flex-col gap-2 p-0">
                    {list.map((page) => (
                      <li key={page.route} className="rounded-xl border border-border bg-card p-4">
                        <Link className="prose-link text-sm font-medium" href={page.route}>
                          {page.frontMatter.title}
                        </Link>
                        <p className="caption mt-1">{page.frontMatter.summary}</p>
                      </li>
                    ))}
                  </ul>
                )}
              </section>
            );
          })}
        </div>
      </div>

      <aside className="reading__aside">
        <DocSearch />
      </aside>
    </div>
  );
}
