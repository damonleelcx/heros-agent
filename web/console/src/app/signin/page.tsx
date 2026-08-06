import Link from "next/link";
import { AlertTriangle, ArrowLeft, ArrowRight, KeyRound, Mail, ShieldCheck } from "lucide-react";
import { identityProvider } from "@/lib/identity";
import { signInState, IDENTITY_ASSURANCES, PASSWORD_ASSURANCES, SIGNIN_LEDE } from "@/content/identity";
import { PASSWORD_COPY } from "@/content/passwordAccount";
import { PasswordField } from "@/components/passwordField";

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
  // 🔴 P28. The password seam replaces the credential FIELD, and nothing else on this page: the same
  // shell, the same five reason states, the same form posting to the same route with no client
  // JavaScript. `ui-redesign-feature-and-visual-consistency` forbids a redesign dropping a capability,
  // and the reverse applies too — a new capability does not get to quietly rebuild the page around it.
  const password = identityProvider() === "password";
  // The assurance list is kind-aware, and that is not cosmetic. The federated list opens with "No
  // password database", which would be rendered three centimetres from a password field — a false claim
  // on the one screen where a reader is thinking about exactly this question.
  const assurances = password ? PASSWORD_ASSURANCES : IDENTITY_ASSURANCES;

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
            {password ? SIGNIN_LEDE.password : SIGNIN_LEDE.federated}
          </p>
          <ul className="mt-10 flex flex-col gap-6">
            {assurances.map((assurance) => (
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

            {password ? (
              /*
               * Two fields, one native form, no client JavaScript — the same three properties the
               * credential form has always had (see the route handler's header for why they are worth
               * more than the interactivity given up). `autoComplete` is set so a password manager
               * recognises this as a sign-in, which is the single most effective thing this page can do
               * for the strength of the passwords people actually choose.
               */
              <>
                <form className="mt-7 flex flex-col gap-4" method="post" action="/api/session">
                  <input type="hidden" name="next" value={next} />
                  <div className="flex flex-col gap-2">
                    <label
                      className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-marketing-ink/50"
                      htmlFor="email"
                    >
                      <Mail className="size-3" aria-hidden="true" />
                      {PASSWORD_COPY.signIn.emailLabel}
                    </label>
                    <input
                      className="w-full rounded-lg border border-marketing-ink/12 bg-marketing-canvas px-4 py-3 text-sm text-marketing-ink outline-none transition-colors placeholder:text-marketing-ink/25 focus:border-marketing-accent/60"
                      id="email"
                      name="email"
                      type="email"
                      autoComplete="username"
                      autoCapitalize="none"
                      spellCheck={false}
                      required
                      aria-invalid={state.tone === "error" ? true : undefined}
                    />
                  </div>
                  <div className="flex flex-col gap-2">
                    <label
                      className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-marketing-ink/50"
                      htmlFor="password"
                    >
                      <KeyRound className="size-3" aria-hidden="true" />
                      {PASSWORD_COPY.signIn.passwordLabel}
                    </label>
                    <PasswordField
                      className="w-full rounded-lg border border-marketing-ink/12 bg-marketing-canvas px-4 py-3 text-sm text-marketing-ink outline-none transition-colors placeholder:text-marketing-ink/25 focus:border-marketing-accent/60"
                      id="password"
                      name="password"
                      autoComplete="current-password"
                      required
                      aria-describedby="password-hint"
                      aria-invalid={state.tone === "error" ? true : undefined}
                    />
                  </div>
                  <button
                    className="w-full cursor-pointer rounded-lg bg-marketing-accent py-3 text-sm font-semibold text-marketing-accent-ink transition-opacity hover:opacity-90 active:opacity-80"
                    type="submit"
                  >
                    {PASSWORD_COPY.signIn.submit}
                  </button>
                </form>

                <div className="mt-5 flex items-center justify-between text-xs text-marketing-ink/40">
                  <Link className="underline underline-offset-4 hover:text-marketing-ink/70" href="/forgot-password">
                    {PASSWORD_COPY.signIn.forgot}
                  </Link>
                  <Link className="underline underline-offset-4 hover:text-marketing-ink/70" href="/signup">
                    {PASSWORD_COPY.signIn.noAccount}
                  </Link>
                </div>

                <p className="mt-4 text-center text-xs leading-relaxed text-marketing-ink/40" id="password-hint">
                  {PASSWORD_COPY.signIn.hint}
                </p>

                {/* P23 task 8.7 — the same legal line the credential form carries. Signing in is not an
                    acceptance; the commitment gate records that, against a version and a content hash. */}
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
            ) : federated ? (
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
                    <PasswordField
                      className="w-full rounded-lg border border-marketing-ink/12 bg-marketing-canvas px-4 py-3 font-mono text-sm text-marketing-ink outline-none transition-colors placeholder:text-marketing-ink/25 focus:border-marketing-accent/60"
                      id="assertion"
                      name="assertion"
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
                  {/*
                    🔴 In `platform` mode this field takes the SAME token `heros login` uses, and saying
                    so is the point of the mode. The console used to demand a second secret that no CLI
                    command could produce, so `heros link` ended by printing a dashboard URL that refused
                    the credential the user had just authenticated with. A field labelled only "tenant
                    credential" is what let that gap sit unnoticed: it is true for every mode and
                    therefore tells the reader nothing about which credential they hold.
                  */}
                  {identityProvider() === "platform" ? (
                    <>
                      This is your <span className="font-mono">heros</span> platform token — the same one{" "}
                      <span className="font-mono">heros login</span> stores. The console exchanges it once
                      for a session held on the server; your browser receives a cookie it cannot read, and
                      never the token itself.
                    </>
                  ) : identityProvider() === "configured" ? (
                    /*
                     * 🔴 WHERE IT COMES FROM, not only what we do with it.
                     *
                     * The paragraph above says the generic hint "is true for every mode and therefore
                     * tells the reader nothing about which credential they hold" — and then fixed that
                     * for `platform` only. `configured` kept the generic sentence, and it is the mode
                     * where the question bites hardest: this is the single-customer and air-gapped shape,
                     * where people are added by CONFIGURATION rather than by invitation, so a reader who
                     * does not already have the value has no way to work out who to ask.
                     *
                     * Asked by somebody looking at this screen: "how do my users know what to use?".
                     * That is the whole defect, and the answer is one sentence long.
                     */
                    <>
                      This install issues its own credentials rather than federating with an identity
                      provider — whoever runs it gives you this value. The console exchanges it once for a
                      session held on the server; your browser receives a cookie it cannot read, and never
                      a platform key.
                    </>
                  ) : (
                    <>
                      The console exchanges this once for a session held on the server. Your browser
                      receives a cookie it cannot read, and never a platform key.
                    </>
                  )}
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
