import { loadLegalCorpus } from "@/lib/reading/legal";

/**
 * `/legal/manifest.json` — the machine-readable index of every legal document and version (task 8.5).
 *
 * # Who reads this, and why it must resolve with no session
 *
 * Three callers, and none of them can be asked to sign in:
 *
 *   - **The consent endpoint's server-side hash validation** (task 9.2). A client that submits a hash for
 *     a version it was not shown is rejected against this. Without that check the consent record says
 *     whatever the browser said, and its audit value is zero.
 *   - **An auditor or a customer's counsel**, who wants to know what was live on a given deployment
 *     without asking anybody. "Which text is live on this cluster" should be a `curl`, not an
 *     investigation (task 11.2).
 *   - **The build-time fence**, which fails on a manifest entry whose document no longer resolves.
 *
 * # Why it is a route handler rather than a file in `public/`
 *
 * A file in `public/` is written by hand or by a script, and either way it is a SECOND source of truth
 * that can disagree with the pages. This is derived from the same corpus load the pages use, so a
 * document and its manifest entry cannot drift apart — which is the whole property task 8.6 fences.
 */
/*
 * Dynamic for the same reason the pages are, plus one of its own: `current` is a comparison against
 * today's date, and a manifest baked at build time would keep saying an older version is in force after
 * a scheduled effective date passed. A manifest that disagrees with the page it describes is worse than
 * no manifest — it is the artifact the consent endpoint validates hashes against.
 */
export const dynamic = "force-dynamic";

export async function GET() {
  const corpus = await loadLegalCorpus();
  return Response.json(corpus.manifest, {
    headers: {
      // The manifest changes only when the image does (ADR-011), so it is cacheable — but a stale
      // manifest is a wrong answer to "what is live", so it revalidates rather than being immutable.
      "cache-control": "public, max-age=0, must-revalidate",
    },
  });
}
