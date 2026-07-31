import Link from "next/link";
import { AlertTriangle, ArrowLeft, ArrowRight, KeyRound, ShieldCheck } from "lucide-react";
import { identityProvider } from "@/lib/identity";
import { signInState, IDENTITY_ASSURANCES } from "@/content/identity";

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
 * A reader arrives here for one of five reasons — never signed in, session ended, credential rejected,
 * their IdP is unreachable, or they are not provisioned for a tenant — and each has a different next
 * action. Rendering the same heading for all five throws away the only thing the page knows that the
 * reader does not. The five live in `content/identity.ts`, single-sourced, because the same
 * distinctions otherwise get re-spelled in a support macro and a docs page until they are four states
 * with six names.
 *
 * # What P22 changed here, and what it deliberately did not
 *
 * The composition is now a SPLIT: an assurance panel beside the action, rather than one card floating
 * in the middle. The reason is specific to this phase. Before P22 this page asked for a credential
 * and had nothing to say; now it asks a reader to send their corporate identity somewhere, which is
 * the moment they are entitled to know what happens to it. The three lines on the left are properties
 * the code has (`content/identity.ts` `IDENTITY_ASSURANCES`) — not positioning — and the middle one is
 * the one that matters most: their browser never holds a key.
 *
 * What did not change is the credential path. A deployment federating with nobody — the open-core
 * case, and every local console — still signs in with a tenant credential through the same form
 * posting to the same route, with no client JavaScript. 🔴 `ui-redesign-feature-and-visual-consistency`
 * is explicit that a redesign may not drop a capability, and "we federate now" is not a reason to
 * strand the deployments that do not.
 *
 * # Why the composition is deliberately dark
 *
 * It shares the public surface's tokens rather than the console's theme. This page is the seam between
 * the two, it renders before any tenant preference is known, and a sign-in that flips appearance
 * depending on a cookie the reader may not have yet is a page that looks broken half the time.
 */

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
  const state = signInState(params.reason);
  const next = safeNext(params.next);
  // The provider KIND, never a credential and never an issuer. It decides which of the two sign-in
  // paths this deployment offers — not what the reader is told about our internals.
  const federated = identityProvider() === "oidc" || identityProvider() === "saml";

  return (
    <div className="relative min-h-screen bg-marketing-canvas text-marketing-ink">
      <a className="skip-link" href="#main">
        Skip to content
      </a>
      <div
        className="absolute inset-0 bg-[length:48px_48px]"
        style={{ backgroundImage: "var(--marketing-grid)" }}
        aria-hidden="true"
      />

      <Link
        className="absolute left-6 top-6 z-20 inline-flex items-center gap-2 text-sm text-marketing-ink/50 transition-colors hover:text-marketing-ink"
        href="/"
      >
        <ArrowLeft className="size-4" aria-hidden="true" />
        Back
      </Link>

      <main
        className="relative z-10 mx-auto flex min-h-screen max-w-5xl flex-col items-center justify-center gap-12 px-6 py-24 lg:flex-row lg:items-stretch lg:gap-16"
        id="main"
      >
        {/* The assurance panel. Below the action on a narrow viewport, beside it on a wide one — the
            reader on a phone came here to sign in, not to read. */}
        <section className="order-2 flex w-full flex-col justify-center lg:order-1 lg:flex-1">
          <p className="font-display text-4xl font-light uppercase tracking-[0.15em] text-marketing-ink">
            Heros
          </p>
          <p className="mt-4 max-w-md text-sm leading-relaxed text-marketing-ink/50">
            The console renders one tenant&apos;s work. Signing in binds this browser to that tenant
            and to nothing else.
          </p>
          <ul className="mt-10 flex flex-col gap-6">
            {IDENTITY_ASSURANCES.map((assurance) => (
              <li className="flex gap-3" key={assurance.label}>
                <ShieldCheck className="mt-0.5 size-4 shrink-0 text-marketing-accent" aria-hidden="true" />
                <div>
                  <p className="text-sm font-semibold text-marketing-ink">{assurance.label}</p>
                  <p className="mt-1 max-w-sm text-xs leading-relaxed text-marketing-ink/40">
                    {assurance.body}
                  </p>
                </div>
              </li>
            ))}
          </ul>
        </section>

        {/* The action. Measured to its FORM, not to the viewport: a credential field stretched across
            a 1440px display reads as a text area and invites a paragraph. */}
        <section className="order-1 w-full lg:order-2 lg:flex lg:items-center" style={{ maxInlineSize: "var(--measure-form)" }}>
          <div className="w-full rounded-2xl border border-marketing-ink/10 bg-marketing-ink/4 p-8 shadow-2xl backdrop-blur">
            <h1 className="font-display text-2xl font-normal text-marketing-ink">{state.title}</h1>
            <p className="mt-2 text-sm leading-relaxed text-marketing-ink/50">{state.body}</p>

            {state.alert ? (
              <p
                className="mt-5 flex items-start gap-2 rounded-lg border border-bad/30 bg-bad/10 px-3 py-2 text-xs text-bad"
                role="alert"
              >
                <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
                {state.alert}
              </p>
            ) : null}

            {federated ? (
              /*
               * A LINK, not a form. `/auth/login` answers with a redirect to the identity provider —
               * a different origin — and this console serves `form-action 'self'`, which Firefox
               * applies to a form submission's redirect chain. A posted sign-in would be blocked by
               * our own policy in a real browser while passing every server-side check. The route's
               * own comment carries the rest of the reasoning.
               */
              <>
                <Link
                  className="mt-7 flex w-full cursor-pointer items-center justify-center gap-2 rounded-lg bg-marketing-accent py-3 text-sm font-semibold text-marketing-accent-ink transition-opacity hover:opacity-90 active:opacity-80"
                  href={`/auth/login?next=${encodeURIComponent(next)}`}
                  prefetch={false}
                >
                  Continue with your organization
                  <ArrowRight className="size-4" aria-hidden="true" />
                </Link>
                <p className="mt-5 text-center text-xs leading-relaxed text-marketing-ink/40">
                  You will be sent to your organization&apos;s identity provider and returned here. We
                  never see your password, and we do not keep the proof it gives us.
                </p>
              </>
            ) : (
              <>
                <form className="mt-7 flex flex-col gap-4" method="post" action="/api/session">
                  <input type="hidden" name="next" value={next} />
                  <div className="flex flex-col gap-2">
                    <label
                      className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-marketing-ink/50"
                      htmlFor="assertion"
                    >
                      <KeyRound className="size-3" aria-hidden="true" />
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
                      aria-invalid={state.tone === "error" ? true : undefined}
                    />
                  </div>

                  <button
                    className="w-full cursor-pointer rounded-lg bg-marketing-accent py-3 text-sm font-semibold text-marketing-accent-ink transition-opacity hover:opacity-90 active:opacity-80"
                    type="submit"
                  >
                    Continue
                  </button>
                </form>

                <p className="mt-5 text-center text-xs leading-relaxed text-marketing-ink/40" id="assertion-hint">
                  The console exchanges this once for a session held on the server. Your browser
                  receives a cookie it cannot read, and never a platform key.
                </p>

                {/*
                  P23 task 8.7 — the legal line on the sign-in page.
                  🔴 It states what is true TODAY and nothing more. Signing in is not an acceptance: the
                  commitment gate (task 10.2) records acceptance explicitly, against a version and a
                  content hash, and it is the only thing that does. A sign-in page that says "by
                  continuing you agree to our Terms" while nothing records that agreement is asserting a
                  contract the system cannot evidence — which is precisely the failure this phase exists
                  to remove, so the sentence below OFFERS the documents rather than claiming assent.
                */}
                <p className="mt-3 text-center text-xs leading-relaxed text-marketing-ink/35">
                  <Link className="underline underline-offset-4 hover:text-marketing-ink/60" href="/legal/terms">
                    Terms of Service
                  </Link>
                  {" · "}
                  <Link className="underline underline-offset-4 hover:text-marketing-ink/60" href="/legal/privacy">
                    Privacy Notice
                  </Link>
                </p>
              </>
            )}
          </div>
        </section>
      </main>
    </div>
  );
}
