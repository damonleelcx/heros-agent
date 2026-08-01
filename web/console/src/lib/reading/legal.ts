import "server-only";
import { cachedLegal } from "./cache";
import { compareVersions, currentLegal, LEGAL_KINDS, type LegalDocument, type LegalKind } from "./content";

/**
 * legal.ts is the manifest builder — the server-side view of "which text is live, and what is its hash".
 *
 * # Which version is "in force", and when it changes
 *
 * The routes render per request (see any `(reading)` page for why — the nonce-based CSP requires it), so
 * `today` is the REQUEST date. A version dated for next Tuesday therefore becomes the one in force at
 * midnight UTC on Tuesday, in a running container, with no deploy.
 *
 * That is the behaviour a scheduled legal update actually wants, and it costs nothing here: the corpus
 * itself is memoised for the process's lifetime (`cache.ts`) because the FILES cannot change under a
 * running container. Only the date comparison is re-evaluated.
 */

export type ManifestEntry = {
  version: string;
  effective_date: string;
  hash: string;
  route: string;
  material: boolean;
  supersedes: string;
  current: boolean;
};

export type LegalManifest = {
  /** The manifest's own shape version. A consumer that pins this cannot be broken by an added field. */
  schema: "heros.legal-manifest/v1";
  generated_from: "web/console/content/legal/en/**";
  kinds: Record<string, ManifestEntry[]>;
};

/** today is the current date as YYYY-MM-DD in UTC — the clock the "in force" comparison reads. */
export function today(): string {
  return new Date().toISOString().slice(0, 10);
}

export type LegalCorpus = {
  documents: LegalDocument[];
  current: Partial<Record<LegalKind, LegalDocument>>;
  manifest: LegalManifest;
};

/** loadLegalCorpus reads every version of every legal document and derives the manifest from them. */
export async function loadLegalCorpus(asOf = today()): Promise<LegalCorpus> {
  const documents = await cachedLegal();
  const current: Partial<Record<LegalKind, LegalDocument>> = {};
  const kinds: Record<string, ManifestEntry[]> = {};

  for (const kind of LEGAL_KINDS) {
    const live = currentLegal(documents, kind, asOf);
    if (live) current[kind] = live;
    const entries = documents
      .filter((d) => d.frontMatter.kind === kind)
      .sort((a, b) => compareVersions(b.frontMatter.version, a.frontMatter.version))
      .map((d) => ({
        version: d.frontMatter.version,
        effective_date: d.frontMatter.effective_date,
        hash: d.contentHash,
        route: d.versionRoute,
        material: d.frontMatter.material,
        supersedes: d.frontMatter.supersedes,
        current: live !== null && d.frontMatter.version === live.frontMatter.version,
      }));
    if (entries.length > 0) kinds[kind] = entries;
  }

  return {
    documents,
    current,
    manifest: { schema: "heros.legal-manifest/v1", generated_from: "web/console/content/legal/en/**", kinds },
  };
}

/** KIND_LABEL is the human name for a kind. One place, so the page and the manifest cannot disagree. */
export const KIND_LABEL: Record<LegalKind, string> = {
  terms: "Terms of Service",
  privacy: "Privacy Notice",
  "sub-processors": "Sub-processors",
};

/**
 * printIdentity is the exact string the print running footer carries (task 3.4).
 *
 * It is built here rather than in the page so that the printed identity and the manifest entry are
 * produced from one function — a printed agreement whose hash disagrees with the manifest is worse than
 * one with no hash at all, because it looks checkable and is not.
 */
export function printIdentity(document: LegalDocument): string {
  const { kind, version, effective_date } = document.frontMatter;
  return `${KIND_LABEL[kind]} · version ${version} · effective ${effective_date} · sha256:${document.contentHash}`;
}
