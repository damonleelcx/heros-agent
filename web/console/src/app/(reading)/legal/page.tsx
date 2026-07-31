import Link from "next/link";
import { LEGAL_KINDS, type LegalKind } from "@/lib/reading/content";
import { KIND_LABEL, loadLegalCorpus } from "@/lib/reading/legal";

/**
 * `/legal` — the version-history page (task 8.4).
 *
 * It is a small page with one job that is easy to underestimate: making the **materiality declaration
 * visible**. `material: true|false` is set in a reviewed pull request and it decides whether every
 * existing customer is asked to accept again. A field that only ever appears inside a JSON manifest is a
 * field nobody reviews; rendering it in a column, next to the date and the hash, is what makes the
 * declaration attributable in practice rather than only in principle (Decision 3).
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

export default async function LegalIndexPage() {
  const corpus = await loadLegalCorpus();

  return (
    <div className="reading__frame">
      <div className="reading__doc">
        <p className="stat__label">Legal</p>
        <h1 className="page__title font-display font-light tracking-tight">Documents and versions</h1>
        <p className="hint mt-3 max-w-none text-sm">
          Every version ever published stays at its own permanent address. A recorded acceptance points at a{" "}
          <strong className="font-semibold">kind, a version and a content hash</strong> — never at a URL — so
          what a customer agreed to can still be read years later, exactly as it was.
        </p>

        <div className="mt-8 flex flex-col gap-10">
          {LEGAL_KINDS.map((kind) => {
            const entries = corpus.manifest.kinds[kind] ?? [];
            const live = corpus.current[kind as LegalKind];
            return (
              <section key={kind} className="flex flex-col gap-3">
                <h2 className="section__title font-display font-light tracking-tight">{KIND_LABEL[kind]}</h2>
                {entries.length === 0 ? (
                  <p className="hint max-w-none">
                    Not published yet. Nothing is asserted here until the document exists — an empty section
                    is honest, and a placeholder agreement would not be.
                  </p>
                ) : (
                  <>
                    {live ? (
                      <p className="text-sm text-foreground">
                        In force:{" "}
                        <Link className="prose-link" href={live.route}>
                          version {live.frontMatter.version}
                        </Link>{" "}
                        since {live.frontMatter.effective_date}.
                      </p>
                    ) : null}
                    <div className="prose-table-frame">
                      <table className="prose-table">
                        <thead>
                          <tr>
                            <th scope="col">Version</th>
                            <th scope="col">Effective</th>
                            <th scope="col">Change</th>
                            <th scope="col">Supersedes</th>
                            <th scope="col">Content hash</th>
                          </tr>
                        </thead>
                        <tbody>
                          {entries.map((entry) => (
                            <tr key={entry.version}>
                              <td>
                                <Link className="prose-link" href={entry.route}>
                                  {entry.version}
                                </Link>
                                {entry.current ? <span className="caption"> · in force</span> : null}
                              </td>
                              <td>{entry.effective_date}</td>
                              {/*
                                The word, not a colour and not an icon. "Material" is the declaration that
                                decides whether every existing customer is asked to accept again, and a
                                reader must be able to see which it was without hovering anything.
                              */}
                              <td>{entry.material ? "Material" : "Non-material"}</td>
                              <td>{entry.supersedes}</td>
                              <td className="mono reading__hash text-xs">{entry.hash}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </div>
                  </>
                )}
              </section>
            );
          })}
        </div>

        <p className="hint mt-10 max-w-none">
          The same table is available as machine-readable JSON at{" "}
          <Link className="prose-link" href="/legal/manifest.json">
            /legal/manifest.json
          </Link>
          , resolvable with no session.
        </p>
      </div>
    </div>
  );
}
