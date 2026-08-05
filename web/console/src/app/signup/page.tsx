import { SIGNUP_COPY, REFUSAL_COPY } from "@/lib/organizationCopy";
import { SignUpForm } from "@/components/signup";
import { selfServeEnabled } from "@/lib/posture";
import { readSessionToken, resolveSession } from "@/lib/session";

/**
 * Creating an organization.
 *
 * # Why the name is asked for rather than derived
 *
 * Deriving it from the email domain produces "Gmail" for every independent developer and the wrong legal
 * entity for half of everyone else — and an editable wrong name is a name most people never edit. One
 * question, once, is cheaper than a support conversation about why their organization is called
 * "outlook".
 *
 * # 🔴 The posture is checked BEFORE the form renders, not after it is submitted
 *
 * Self-serve is declared on the platform and off by default: an air-gapped or single-customer install
 * must not grow a registration form by upgrading. The first version of this page always rendered the
 * form and surfaced the refusal on submit — safe, because nothing could be created, and bad copy: a
 * form that collects a name and then says "no" teaches somebody the product is broken, where a page
 * that never offered one says "ask whoever runs this install", which is a next action.
 *
 * The posture is read from `/readyz`, where the platform reports it as a value. Unknown resolves to OFF
 * (see `posture.ts`) — a console that cannot ask must not offer.
 *
 * The submit-time refusal is KEPT. This check races nothing important, but a deployment can turn
 * self-serve off between the render and the press, and the platform is the one that decides.
 *
 * # 🔴 There are TWO preconditions, and only one of them used to be checked here
 *
 * Creating an organization needs self-serve ON **and a person**: the platform takes the issuer, subject
 * and email from the session this console issued, never from the request body, so with no session there
 * is nobody to create it for. That second precondition was unchecked, and the paragraph above says
 * exactly what that costs — a signed-out visitor got the full form, typed a name, pressed Create it, and
 * received `no session`. Two technical words and no next action, on the first screen of the product.
 *
 * It was found by walking the flow in a browser, and nothing had failed: the page rendered, the button
 * worked, and the refusal was correct. Which is the argument for walking it.
 */
export const dynamic = "force-dynamic";

export default async function SignUpPage() {
  const enabled = await selfServeEnabled();
  // Read, never require: `requireSession` REDIRECTS, and bouncing somebody who asked to create an
  // organization onto a sign-in screen with no explanation is the same dead end wearing a different URL.
  // The page says why, and links onward carrying `next` so they land back here.
  const session = enabled ? await resolveSession(await readSessionToken()) : null;

  return (
    <main className="mx-auto flex min-h-screen max-w-lg flex-col justify-center gap-5 px-6">
      <div>
        <p className="font-mono text-[10px] uppercase tracking-[0.18em] text-primary">Get started</p>
        <h1 className="mt-1 font-display text-2xl">
          {!enabled
            ? REFUSAL_COPY.self_serve_disabled.title
            : session
              ? SIGNUP_COPY.title
              : SIGNUP_COPY.signInFirstTitle}
        </h1>
        <p className="mt-2 text-sm text-muted-foreground">
          {!enabled
            ? REFUSAL_COPY.self_serve_disabled.body
            : session
              ? SIGNUP_COPY.lede
              : ""}
        </p>
      </div>
      {enabled && !session ? (
        <>
          <p className="text-sm text-muted-foreground">{SIGNUP_COPY.signInFirstLede}</p>
          <a
            href={`/signin?next=${encodeURIComponent("/signup")}`}
            className="rounded-md border px-4 py-2 text-center text-sm font-medium"
          >
            {SIGNUP_COPY.signInFirstCta}
          </a>
          <p className="text-xs text-muted-foreground">{SIGNUP_COPY.freePlanNote}</p>
        </>
      ) : enabled ? (
        <>
          <SignUpForm />
          <p className="text-xs text-muted-foreground">{SIGNUP_COPY.freePlanNote}</p>
        </>
      ) : (
        <p className="text-xs text-muted-foreground">
          Nothing is wrong — this install does not create organizations on request.
        </p>
      )}
    </main>
  );
}
