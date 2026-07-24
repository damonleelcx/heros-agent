import Link from "next/link";
import { AlertTriangle, ArrowLeft } from "lucide-react";

export const dynamic = "force-dynamic";

/**
 * The sign-in page.
 *
 * # Why it brings its own shell
 *
 * It sits outside both the public layout and the console layout. The console's shell requires a
 * session and the public shell advertises the product; this page is neither, and giving it a "Sign in"
 * button in its own header would be the kind of detail that survives three redesigns because nobody
 * looks at this page twice.
 *
 * # The heading is the REASON, not the word "Sign in"
 *
 * A reader arrives here for one of three reasons — never signed in, session ended, credential
 * rejected — and each has a different next action. Rendering the same heading for all three throws
 * away the only thing the page knows that the reader does not.
 *
 * # Why the composition is deliberately dark
 *
 * It shares the public surface's tokens rather than the console's theme. This page is the seam between
 * the two, it renders before any tenant preference is known, and a sign-in that flips appearance
 * depending on a cookie the reader may not have yet is a page that looks broken half the time.
 */
const REASONS: Record<string, { title: string; body: string; tone?: "error" }> = {
  session_ended: {
    title: "Your session ended",
    body: "Sessions are bounded and can be revoked. Signing in again issues a new one — nothing you were looking at was lost.",
  },
  no_session: {
    title: "Sign in to continue",
    body: "This surface renders your tenant's data, so it is not served without a session.",
  },
  rejected: {
    title: "That credential was not accepted",
    body: "Check the value and try again. If it was issued for a different environment, it will not resolve here.",
    tone: "error",
  },
};

function safeNext(next: string | undefined): string {
  // Mirrors `safeNext` in the route handler. Checked in both places on purpose: the value reaching
  // the form must already be safe, and the handler must not trust the form.
  if (!next || !next.startsWith("/") || next.startsWith("//")) return "/app";
  return next;
}

export default async function SignInPage({
  searchParams,
}: {
  searchParams: Promise<{ reason?: string; next?: string }>;
}) {
  const params = await searchParams;
  const reason = REASONS[params.reason ?? ""] ?? REASONS.no_session;
  const next = safeNext(params.next);

  return (
    <div className="relative flex min-h-screen flex-col items-center justify-center bg-marketing-canvas px-4 py-16 text-marketing-ink">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <div
        className="absolute inset-0 bg-[length:48px_48px]"
        style={{ backgroundImage: "var(--marketing-grid)" }}
        aria-hidden="true"
      />

      <Link
        className="absolute left-6 top-6 inline-flex items-center gap-2 text-sm text-marketing-ink/50 transition-colors hover:text-marketing-ink"
        href="/"
      >
        <ArrowLeft className="size-4" aria-hidden="true" />
        Back
      </Link>

      {/* The one-form page is measured to its FORM, not to the viewport. A credential field stretched
          across a 1440px display reads as a text area and invites a paragraph. */}
      <main className="relative z-10 w-full" id="main" style={{ maxInlineSize: "var(--measure-form)" }}>
        <p className="mb-8 text-center font-display text-4xl font-light uppercase tracking-[0.15em] text-marketing-ink">
          Heros
        </p>

        <div className="rounded-2xl border border-marketing-ink/10 bg-marketing-ink/4 p-8 shadow-2xl backdrop-blur">
          <h1 className="font-display text-2xl font-normal text-marketing-ink">{reason.title}</h1>
          <p className="mt-2 text-sm leading-relaxed text-marketing-ink/50">{reason.body}</p>

          <form className="mt-7 flex flex-col gap-4" method="post" action="/api/session">
            <input type="hidden" name="next" value={next} />
            <div className="flex flex-col gap-2">
              <label
                className="font-mono text-[10px] uppercase tracking-widest text-marketing-ink/50"
                htmlFor="assertion"
              >
                Tenant credential
              </label>
              <input
                className="w-full rounded-lg border border-marketing-ink/12 bg-marketing-canvas px-4 py-3 font-mono text-sm text-marketing-ink outline-none transition-colors placeholder:text-marketing-ink/25 focus:border-marketing-accent/60"
                id="assertion"
                name="assertion"
                type="password"
                autoComplete="off"
                spellCheck={false}
                required
                aria-describedby="assertion-hint"
                aria-invalid={reason.tone === "error" ? true : undefined}
              />
            </div>

            {reason.tone === "error" ? (
              <p
                className="flex items-start gap-2 rounded-lg border border-bad/30 bg-bad/10 px-3 py-2 text-xs text-bad"
                role="alert"
              >
                <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                {reason.title}
              </p>
            ) : null}

            <button
              className="w-full cursor-pointer rounded-lg bg-marketing-accent py-3 text-sm font-semibold text-marketing-accent-ink transition-opacity hover:opacity-90 active:opacity-80"
              type="submit"
            >
              Continue
            </button>
          </form>

          <p className="mt-5 text-center text-xs leading-relaxed text-marketing-ink/40" id="assertion-hint">
            The console exchanges this once for a session held on the server. Your browser receives a
            cookie it cannot read, and never a platform key.
          </p>
        </div>
      </main>
    </div>
  );
}
