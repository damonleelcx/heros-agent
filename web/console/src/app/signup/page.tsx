import { redirect } from "next/navigation";
import { SIGNUP_COPY, REFUSAL_COPY } from "@/lib/organizationCopy";
import { SignUpForm } from "@/components/signup";
import { selfServeEnabled } from "@/lib/posture";
import { readSessionToken, resolveSession } from "@/lib/session";
import { passwordSignInEnabled } from "@/lib/identity";

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
 *
 * # 🔴 P28: on the `password` seam the second precondition is CIRCULAR, so it does not apply
 *
 * "You need a session to create an organization" is correct for the federated seams, where an identity
 * provider vouches for you before you ever reach us — the platform takes the issuer, subject and email
 * from a session this console issued, and there is no other way for it to learn them.
 *
 * On the password seam there is no such provider, and signing up is HOW you get a session. Requiring one
 * first means `/signup` sends you to `/signin`, which sends you to `/signup`: a loop with no exit, on the
 * first screen of the product. So this page renders `PasswordSignUpForm` instead, which supplies the
 * identity in the same request that creates the organization — the platform's own
 * `POST /api/v1/auth/password/signup` writes the person, the organization, the owner membership, the free
 * account and the password in one transaction, or none of them.
 *
 * The posture check is UNCHANGED and still comes first: an air-gapped install must not grow a registration
 * form by upgrading, whichever seam it runs.
 */
export const dynamic = "force-dynamic";

export default async function SignUpPage() {
  const enabled = await selfServeEnabled();
  // The password seam's own sign-up, which needs no prior session. Rendered by its own route so this
  // file keeps the federated flow exactly as it was — see the header.
  if (enabled && passwordSignInEnabled()) redirect("/create-account");
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
