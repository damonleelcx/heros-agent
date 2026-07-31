import "server-only";
import { cache } from "react";
import { loadDocs, loadLegal, type DocsPage, type LegalDocument } from "./corpus.ts";

/**
 * cache.ts memoises the corpus for the process's lifetime.
 *
 * # Why a process-lifetime memo is correct here and would be a bug anywhere else
 *
 * Content ships INSIDE the console image (ADR-011). The files this reads cannot change while the process
 * lives — changing them means building a new image and deploying it. So caching for the process's
 * lifetime is not a staleness risk; it is a statement of the deployment model.
 *
 * The same memo over, say, tenant data would be a serious defect. The distinction is not "is it slow to
 * re-read" but "can the underlying value change under a running process", and here it provably cannot.
 *
 * `React.cache` deduplicates within one request; the module-level promise deduplicates ACROSS requests.
 * Both are needed: the first stops one page reading the corpus three times, the second stops every reader
 * of a busy console re-parsing every document.
 */

let legalPromise: Promise<LegalDocument[]> | null = null;
let docsPromise: Promise<DocsPage[]> | null = null;

export const cachedLegal = cache(async (): Promise<LegalDocument[]> => {
  legalPromise ??= loadLegal();
  return legalPromise;
});

export const cachedDocs = cache(async (): Promise<DocsPage[]> => {
  docsPromise ??= loadDocs();
  return docsPromise;
});
