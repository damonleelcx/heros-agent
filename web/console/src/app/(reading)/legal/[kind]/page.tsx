import Link from "next/link";
import { notFound } from "next/navigation";
import { DocumentFrame } from "@/components/reading/documentFrame";
import { LEGAL_KINDS, type LegalKind } from "@/lib/reading/content";
import { KIND_LABEL, loadLegalCorpus, printIdentity } from "@/lib/reading/legal";

/**
 * `/legal/{kind}` — the version currently in force.
 *
 * # Availability (NFR1)
 *
 * The legal surface must serve **when the platform does not** — which is exactly when a customer goes
 * looking for it. This page has no upstream to be down and no session store to consult: it reads the
 * corpus that shipped inside this container. The harness asserts it by stopping the platform stub and
 * watching the upstream-request counter stay still (task 12.1).
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

export default async function LegalKindPage({ params }: { params: Promise<{ kind: string }> }) {
  const { kind } = await params;
  if (!LEGAL_KINDS.includes(kind as LegalKind)) notFound();
  const corpus = await loadLegalCorpus();
  const document = corpus.current[kind as LegalKind];
  if (!document) notFound();

  const versions = corpus.manifest.kinds[kind] ?? [];

  return (
    <DocumentFrame
      eyebrow="Legal"
      title={document.frontMatter.title}
      lede={`The version in force. Every earlier version stays published at its own permanent address — a consent record points at a version and a hash, never at this URL.`}
      identity={
        <>
          <span>version {document.frontMatter.version}</span>
          <span>effective {document.frontMatter.effective_date}</span>
          <span>{document.frontMatter.material ? "material change" : "non-material change"}</span>
          <span>authoritative language: {document.frontMatter.authoritative_language}</span>
          <span className="reading__hash">sha256:{document.contentHash}</span>
        </>
      }
      headings={document.parsed.headings}
      blocks={document.parsed.blocks}
      printFooter={printIdentity(document)}
      aside={
        <div className="mb-4 flex flex-col gap-2 rounded-xl border border-border bg-card p-4">
          <p className="stat__label">Versions</p>
          <ul className="flex list-none flex-col gap-1 p-0">
            {versions.map((entry) => (
              <li key={entry.version}>
                <Link className="prose-link text-sm" href={entry.route}>
                  {entry.version}
                </Link>
                <span className="caption"> · {entry.effective_date}</span>
                {entry.current ? <span className="caption"> · in force</span> : null}
              </li>
            ))}
          </ul>
          <Link className="prose-link text-sm" href="/legal">
            All documents and versions
          </Link>
        </div>
      }
      footer={
        <p className="hint max-w-none">
          {KIND_LABEL[kind as LegalKind]} · This page is served from the console&rsquo;s own container and makes
          no request to the platform or to any third party. The content hash above is computed at build time
          over the document source, and it is what a recorded acceptance refers to.
        </p>
      }
    />
  );
}
