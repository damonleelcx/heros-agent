import Link from "next/link";
import { redirect } from "next/navigation";
import { AlertTriangle, CheckCircle2 } from "lucide-react";
import { passwordSignInEnabled } from "@/lib/identity";
import { confirmEmail } from "@/lib/idp/password";
import { PASSWORD_COPY } from "@/content/passwordAccount";

/**
 * Confirming an email address.
 *
 * # ⚠️ It spends the token on a GET, and that is a considered exception
 *
 * The console's own rule is that no mutating route is a GET — it is what provides the CSRF property the
 * session cookie's `SameSite=Lax` does not, and `tests/session-cookie.test.mjs` enforces it. This page
 * breaks it, once, deliberately, and the reasoning is worth stating rather than leaving as an oversight
 * somebody later "fixes" into a worse shape:
 *
 *   - The thing being confirmed is possession of the link itself. There is no session, no cookie and no
 *     ambient authority — the token IS the whole authorization — so there is nothing for a cross-site
 *     request to forge. An attacker who can make a victim's browser visit this URL is an attacker who
 *     already has the URL, and could simply visit it themselves.
 *   - The alternative is a page with a "Confirm" button that posts. That is what mail clients with link
 *     prefetching would otherwise require, and it costs a click on the one flow whose whole point is to
 *     be effortless. ⚠️ It also does not actually help: a prefetching mail client that spends the token
 *     produces a confirmed address, which is the intended outcome reached slightly early.
 *
 * What the exception does NOT extend to is the reset page, which sets a credential and therefore posts.
 *
 * # The refusal is one message for four causes
 *
 * Spent, expired, wrong purpose and never-existed all render the same thing, matching the platform. The
 * difference helps only somebody enumerating tokens, and a real reader's next action — ask for another —
 * is the same in all four.
 */
export const dynamic = "force-dynamic";

export default async function VerifyEmailPage({
  searchParams,
}: {
  searchParams: Promise<{ t?: string }>;
}) {
  if (!passwordSignInEnabled()) redirect("/signin");
  const params = await searchParams;
  const token = (params.t ?? "").trim();
  const outcome = token ? await confirmEmail(token) : { ok: false, email: "", reason: "" };

  return (
    <>
      <h1 className="font-display text-2xl font-normal text-marketing-ink">
        {outcome.ok ? PASSWORD_COPY.verify.okTitle : PASSWORD_COPY.verify.failTitle}
      </h1>
      <p className="mt-2 text-sm leading-relaxed text-marketing-ink/50">
        {outcome.ok ? PASSWORD_COPY.verify.okBody : PASSWORD_COPY.verify.failBody}
      </p>
      {outcome.ok ? (
        <p className="mt-5 flex items-start gap-2 rounded-lg border border-good/30 bg-good/10 px-3 py-2 text-xs text-good" role="status">
          <CheckCircle2 className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          {outcome.email}
        </p>
      ) : (
        <p className="mt-5 flex items-start gap-2 rounded-lg border border-bad/30 bg-bad/10 px-3 py-2 text-xs text-bad" role="alert">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          {PASSWORD_COPY.reset.expired}
        </p>
      )}
      <p className="mt-7 text-center text-xs text-marketing-ink/40">
        <Link className="underline underline-offset-4 hover:text-marketing-ink/70" href="/app">
          Continue to the console
        </Link>
      </p>
    </>
  );
}
