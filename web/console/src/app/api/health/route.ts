import { identityHealth, identitySecretsSource } from "@/lib/identity";
import { platformApiBase, upstreamTimeoutMs } from "@/lib/platformApi";
import { loadLegalCorpus } from "@/lib/reading/legal";

/**
 * The console component's machine-readable health (FR25, 🔴 `health-signal-surface`).
 *
 * # Why this endpoint exists separately from the rendered console
 *
 * A UI dashboard is never a health judgement. An operator or a probe asking "is the console up" must
 * get an answer from a machine-readable endpoint, on the box that is misbehaving, now — not by
 * loading a page and looking at it. The platform's `/readyz` reads THIS route and reports not-ready
 * when it cannot, naming the console as the degraded component, so a healthy Go service in front of a
 * dead console cannot report ready.
 *
 * # What it reports, and what it deliberately does not
 *
 * It reports the console process's own liveness and its CONFIGURATION IDENTITY: which platform origin
 * it is pointed at, which identity provider is in force, and whether a credential is configured
 * (**a boolean, never the value**). It does NOT probe the platform API. Probing upstream on every
 * health check would make a platform latency spike look like a console outage — the opposite of the
 * signal being asked for — and the platform's own readiness already covers the platform.
 *
 * # Why it DOES probe the identity provider (P22 task 7.1)
 *
 * The IdP is the one upstream this console genuinely depends on for its primary function, and the
 * exception is deliberate rather than an inconsistency with the paragraph above. The distinction is
 * which way the failure runs: a slow platform API makes pages slow, while an unreachable IdP means
 * **nobody can sign in at all**, and the platform's `/readyz` reads this block to report
 * `identity_provider` as its own named component.
 *
 * It measures REACHABILITY, not traffic — a console with no logins all night is not unhealthy — and it
 * does not depend on the traffic it gates, so readiness cannot deadlock on the very sign-ins it admits.
 *
 * `configured` and `dev` federate with nobody and are always reachable: the statement is "this
 * console's identity mechanism is serviceable", and for a static map it always is. Reporting `false`
 * there would page an operator about a dependency the deployment does not have.
 *
 * It is public because it carries no tenant data and because a health endpoint behind authentication
 * cannot be probed by the thing that most needs to probe it.
 *
 * # Why the LEGAL DOCUMENT IDENTITIES are here (P23 task 11.2)
 *
 * "Which legal text is live on this cluster" must be a `curl`, not an investigation. During an incident,
 * a dispute or an audit, the question is asked about a specific deployment — and answering it by reading
 * a git tag is answering a different question, because content ships in the console IMAGE and a cluster
 * can be running a previous one.
 *
 * `/legal/manifest.json` is the full answer and is also public. This carries the same identities beside
 * the deployment's other configuration facts, so one probe reports what an operator actually needs
 * together: which platform, which identity provider, which documents.
 *
 * It reads the corpus from this container's own filesystem — no upstream call, so a platform outage
 * cannot make the health endpoint slow or wrong.
 */
export const dynamic = "force-dynamic";

export async function GET() {
  // The KIND, the ISSUER and the verdict — never a client id, never an allowlist, never a secret's
  // logical name. This endpoint is public by necessity, so everything it says is said to everybody.
  const identity = await identityHealth();

  /*
   * A corpus that cannot be read is REPORTED, not omitted. An absent `documents` key would be
   * indistinguishable from a deployment with no legal documents, and those are very different states —
   * the second is a configuration choice, the first is a broken image.
   */
  let documents: Record<string, { version: string; hash: string; effective_date: string }> | { error: string };
  try {
    const corpus = await loadLegalCorpus();
    documents = {};
    for (const [kind, live] of Object.entries(corpus.current)) {
      if (!live) continue;
      documents[kind] = {
        version: live.frontMatter.version,
        hash: live.contentHash,
        effective_date: live.frontMatter.effective_date,
      };
    }
  } catch (error) {
    documents = { error: error instanceof Error ? error.message : "the legal corpus could not be read" };
  }
  return Response.json(
    {
      component: "console",
      status: "ok",
      // An origin, not a credential. It is here because "the console is up but pointed at the wrong
      // platform" is a real incident that is otherwise diagnosed by reading someone's shell history.
      platform_api_base: platformApiBase(),
      identity_provider: identity,
      // Which SOURCE identity credentials resolve from, so the claim is checkable from the running
      // system rather than from a manifest (secrets-baseline.md §1.1). The source, never a secret.
      identity_secrets_source: identitySecretsSource(),
      credential_configured: Boolean(process.env.CONSOLE_PLATFORM_CREDENTIAL),
      upstream_timeout_ms: upstreamTimeoutMs(),
      // Which legal text is live on THIS deployment. The full manifest, with every historical version,
      // is at /legal/manifest.json.
      legal_documents: documents,
    },
    { headers: { "cache-control": "no-store" } },
  );
}
