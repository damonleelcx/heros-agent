import Link from "next/link";
import { notFound } from "next/navigation";
import { DocumentFrame } from "@/components/reading/documentFrame";
import { LEGAL_KINDS, type LegalKind } from "@/lib/reading/content";
import { KIND_LABEL, loadLegalCorpus, printIdentity } from "@/lib/reading/legal";

/**
 * `/legal/{kind}/v/{version}` — a permanent, immutable address for one version.
 *
 * # 🔴 This route may never 404 for a version that has been published, and it may never REDIRECT
 *
 * Two rules, and each prevents a different failure:
 *
 * 1. **Never deleted.** Every consent record stores `(kind, version, content_hash)`. Deleting an archived
 *    version orphans every record referencing it — the row still says "they agreed to v1.0.0" and nothing
 *    can any longer say what v1.0.0 said. That is a one-way door with no recovery, so `scan-legal-manifest`
 *    fails the build on a manifest entry whose document no longer resolves (task 8.6).
 *
 * 2. **Never redirected to the current version.** A superseded page SAYS it is superseded, names the
 *    current version and links to it — and keeps showing its own text. Redirecting would mean a reader
 *    following a link from a consent record lands on words the record does not refer to, which is the
 *    exact confusion the version route exists to prevent.
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

export default async function LegalVersionPage({
  params,
}: {
  params: Promise<{ kind: string; version: string }>;
}) {
  const { kind, version } = await params;
  if (!LEGAL_KINDS.includes(kind as LegalKind)) notFound();
  const corpus = await loadLegalCorpus();
  const document = corpus.documents.find(
    (d) => d.frontMatter.kind === kind && d.frontMatter.version === version,
  );
  if (!document) notFound();

  const live = corpus.current[kind as LegalKind];
  const superseded = live !== undefined && live.frontMatter.version !== document.frontMatter.version;

  return (
    <DocumentFrame
      eyebrow={`Legal · archived version ${document.frontMatter.version}`}
      title={document.frontMatter.title}
      lede={
        superseded
          ? `This version is superseded. It is published permanently because a recorded acceptance refers to it, and it is shown here exactly as it was accepted.`
          : `This is the version in force, at its permanent address.`
      }
      identity={
        <>
          <span>version {document.frontMatter.version}</span>
          <span>effective {document.frontMatter.effective_date}</span>
          <span>supersedes {document.frontMatter.supersedes}</span>
          <span>{document.frontMatter.material ? "material change" : "non-material change"}</span>
          <span className="reading__hash">sha256:{document.contentHash}</span>
        </>
      }
      headings={document.parsed.headings}
      blocks={document.parsed.blocks}
      printFooter={printIdentity(document)}
      footer={
        superseded && live ? (
          <div className="rounded-xl border border-border bg-card p-4">
            <p className="text-sm text-foreground">
              This version has been superseded. The {KIND_LABEL[kind as LegalKind]} in force is version{" "}
              <strong className="font-semibold">{live.frontMatter.version}</strong>, effective{" "}
              {live.frontMatter.effective_date}.
            </p>
            <p className="hint mt-2 max-w-none">
              You have not been redirected on purpose: a link from a consent record must land on the words
              that record refers to.
            </p>
            <Link className="prose-link mt-3 inline-block text-sm" href={live.route}>
              Read the current {KIND_LABEL[kind as LegalKind]}
            </Link>
          </div>
        ) : null
      }
    />
  );
}
