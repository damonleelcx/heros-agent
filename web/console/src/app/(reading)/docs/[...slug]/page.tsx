import Link from "next/link";
import { notFound } from "next/navigation";
import { DocumentFrame } from "@/components/reading/documentFrame";
import { cachedDocs } from "@/lib/reading/cache";
import { sectionLabel } from "@/lib/reading/content";

/**
 * `/docs/{section}/{page}` — one documentation page.
 *
 * # Every page states its version and its boundary (task 5.5)
 *
 * Both are rendered from front matter rather than written into the prose, for the same reason the
 * capability manifest keeps `boundary` as a field: a boundary written in a paragraph is a boundary that
 * gets edited out when the paragraph is rewritten. As a field it is REQUIRED — a page that does not
 * declare what the capability deliberately does not do fails the build in `parseDocsFrontMatter`.
 *
 * # The `v` segment is reserved (decisions/p23-one-way-doors.md §1.6)
 *
 * `/docs/v/{platform_version}/…` is reserved for versioned documentation and serves nothing today. No
 * section may be named `v`, so the day versioned docs ship, no published URL has to move. `scan-links`
 * enforces the reservation; discovering the collision after the URLs are in a customer's bookmarks —
 * and inside a shipped binary's error messages — is the failure that reservation prevents.
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

export default async function DocsPage({ params }: { params: Promise<{ slug: string[] }> }) {
  const { slug } = await params;
  const wanted = slug.join("/");
  if (slug[0] === "v") notFound();
  const pages = await cachedDocs();
  const page = pages.find((candidate) => candidate.slug === wanted);
  if (!page) notFound();

  return (
    <DocumentFrame
      eyebrow={`Documentation · ${sectionLabel(page.section)}`}
      title={page.frontMatter.title}
      lede={page.frontMatter.summary}
      identity={
        <>
          <span>documents platform {page.frontMatter.platform_version}</span>
          <span className="reading__hash">sha256:{page.contentHash.slice(0, 16)}</span>
        </>
      }
      headings={page.headings}
      blocks={page.parsed.blocks}
      printFooter={`${page.frontMatter.title} · documents platform ${page.frontMatter.platform_version} · sha256:${page.contentHash}`}
      footer={
        <div className="rounded-xl border border-border bg-card p-4">
          <p className="stat__label">Boundary</p>
          <p className="mt-2 text-sm text-foreground/90">{page.frontMatter.boundary}</p>
          <p className="hint mt-3 max-w-none">
            What this capability deliberately does not do, stated on every page — because the sentence a
            reader needs is the one that stops them building on an assumption the product does not hold.
          </p>
          <p className="caption mt-3">
            <Link className="prose-link" href="/docs">
              All documentation
            </Link>
          </p>
        </div>
      }
    />
  );
}
