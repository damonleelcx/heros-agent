import Link from "next/link";
import { redirect } from "next/navigation";
import { cookies } from "next/headers";
import { AlertTriangle, Building2, CheckCircle2, KeyRound, Mail } from "lucide-react";
import { passwordSignInEnabled } from "@/lib/identity";
import { signUpWithPassword } from "@/lib/idp/password";
import { selfServeEnabled } from "@/lib/posture";
import { issueSession, sessionTtlSeconds } from "@/lib/session";
import { SESSION_COOKIE, SESSION_COOKIE_OPTIONS } from "@/lib/cookies";
import { PASSWORD_COPY } from "@/content/passwordAccount";
import { REFUSAL_COPY } from "@/lib/organizationCopy";
import { PasswordField } from "@/components/passwordField";

/**
 * Creating an account on the `password` seam — the screen S1 in the PRD walks through.
 *
 * # Why this is a separate route from `/signup`
 *
 * `/signup` is the FEDERATED flow: it requires a session, because the platform takes the issuer, subject
 * and email from one, and there is no other way for it to learn them. That precondition is correct there
 * and circular here — signing up is how you get a session — so `/signup` redirects to this page on this
 * seam rather than growing a branch that makes both flows harder to read. Neither file changes the other's
 * behaviour.
 *
 * # Three fields, and why none of them is derived
 *
 * The organization name is ASKED FOR rather than derived from the email domain, which `/signup`'s header
 * already argues: deriving produces "Gmail" for every independent developer and the wrong legal entity for
 * half of everyone else, and an editable wrong name is a name most people never edit.
 *
 * # 🔴 What a duplicate address does
 *
 * The same thing a new one does, visually. The platform answers 201 with no organization, and this page
 * shows the neutral acknowledgement — because registration must not answer "does this address have an
 * account here". The person who holds the address gets an email saying somebody tried; nobody else learns
 * anything. That asymmetry is the entire point and is easy to undo by making this page "more helpful".
 */
export const dynamic = "force-dynamic";

async function submit(formData: FormData) {
  "use server";
  const name = String(formData.get("name") ?? "");
  const email = String(formData.get("email") ?? "");
  const password = String(formData.get("password") ?? "");

  const outcome = await signUpWithPassword(name, email, password);
  if (!outcome.ok) {
    if (outcome.reasonCode === "neutral_ack") {
      // The duplicate-address path. 🔴 Indistinguishable from success as far as this page is concerned,
      // and the address is NOT put in the query string — see forgot-password for why.
      redirect("/create-account?check=1");
    }
    redirect(`/create-account?reason=${encodeURIComponent(outcome.reasonCode || "refused")}` +
      `&detail=${encodeURIComponent(outcome.reason)}`);
  }

  // Signed in immediately. The address is unconfirmed and that limits exactly two actions (inviting
  // people, moving to a paid plan) — the banner in the console says so and offers a resend. Blocking the
  // door until an email arrives would fail for every recipient whose mail is slow, which on a first
  // impression is the same as failing.
  const { token } = await issueSession(outcome.principal);
  (await cookies()).set(SESSION_COOKIE, token, { ...SESSION_COOKIE_OPTIONS, maxAge: sessionTtlSeconds() });
  redirect("/app");
}

export default async function CreateAccountPage({
  searchParams,
}: {
  searchParams: Promise<{ reason?: string; detail?: string; check?: string }>;
}) {
  if (!passwordSignInEnabled()) redirect("/signup");
  // 🔴 The posture is checked BEFORE the form renders, exactly as `/signup` does. A form that collects a
  // name, an address and a password and then says "no" teaches somebody the product is broken; a page that
  // never offered one says "ask whoever runs this install", which is a next action.
  if (!(await selfServeEnabled())) {
    return (
      <>
        <h1 className="font-display text-2xl font-normal text-marketing-ink">{REFUSAL_COPY.self_serve_disabled.title}</h1>
        <p className="mt-2 text-sm leading-relaxed text-marketing-ink/50">{REFUSAL_COPY.self_serve_disabled.body}</p>
        <p className="mt-7 text-center text-xs text-marketing-ink/40">
          <Link className="underline underline-offset-4 hover:text-marketing-ink/70" href="/signin">
            {PASSWORD_COPY.signUp.haveAccount}
          </Link>
        </p>
      </>
    );
  }

  const params = await searchParams;
  if (params.check === "1") {
    return (
      <>
        <h1 className="font-display text-2xl font-normal text-marketing-ink">Check your email</h1>
        <p className="mt-5 flex items-start gap-2 rounded-lg border border-good/30 bg-good/10 px-3 py-2 text-xs text-good" role="status">
          <CheckCircle2 className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          {PASSWORD_COPY.forgot.sent}
        </p>
        <p className="mt-7 text-center text-xs text-marketing-ink/40">
          <Link className="underline underline-offset-4 hover:text-marketing-ink/70" href="/signin">
            {PASSWORD_COPY.signUp.haveAccount}
          </Link>
        </p>
      </>
    );
  }

  const detail = (params.detail ?? "").trim();

  return (
    <>
      <h1 className="font-display text-2xl font-normal text-marketing-ink">{PASSWORD_COPY.signUp.title}</h1>
      <p className="mt-2 text-sm leading-relaxed text-marketing-ink/50">{PASSWORD_COPY.signUp.body}</p>

      {detail ? (
        <p className="mt-5 flex items-start gap-2 rounded-lg border border-bad/30 bg-bad/10 px-3 py-2 text-xs text-bad" role="alert">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          {detail}
        </p>
      ) : null}

      <form className="mt-7 flex flex-col gap-4" action={submit}>
        <div className="flex flex-col gap-2">
          <label
            className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-marketing-ink/50"
            htmlFor="name"
          >
            <Building2 className="size-3" aria-hidden="true" />
            {PASSWORD_COPY.signUp.nameLabel}
          </label>
          <input
            className="w-full rounded-lg border border-marketing-ink/12 bg-marketing-canvas px-4 py-3 text-sm text-marketing-ink outline-none transition-colors placeholder:text-marketing-ink/25 focus:border-marketing-accent/60"
            id="name"
            name="name"
            type="text"
            required
            aria-describedby="name-hint"
          />
          <p className="text-xs text-marketing-ink/40" id="name-hint">
            {PASSWORD_COPY.signUp.nameHelp}
          </p>
        </div>

        <div className="flex flex-col gap-2">
          <label
            className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-marketing-ink/50"
            htmlFor="email"
          >
            <Mail className="size-3" aria-hidden="true" />
            {PASSWORD_COPY.signUp.emailLabel}
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
          />
        </div>

        <div className="flex flex-col gap-2">
          <label
            className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-marketing-ink/50"
            htmlFor="password"
          >
            <KeyRound className="size-3" aria-hidden="true" />
            {PASSWORD_COPY.signUp.passwordLabel}
          </label>
          <PasswordField
            className="w-full rounded-lg border border-marketing-ink/12 bg-marketing-canvas px-4 py-3 text-sm text-marketing-ink outline-none transition-colors placeholder:text-marketing-ink/25 focus:border-marketing-accent/60"
            id="password"
            name="password"
            autoComplete="new-password"
            minLength={12}
            required
            aria-describedby="password-hint"
          />
          {/* 🔴 The rule, BEFORE submission. A password refused for a rule the reader was never shown is
              the archetypal error with no next action. `minLength` above makes the browser say it too. */}
          <p className="text-xs text-marketing-ink/40" id="password-hint">
            {PASSWORD_COPY.signUp.passwordHelp}
          </p>
        </div>

        <button
          className="w-full cursor-pointer rounded-lg bg-marketing-accent py-3 text-sm font-semibold text-marketing-accent-ink transition-opacity hover:opacity-90 active:opacity-80"
          type="submit"
        >
          {PASSWORD_COPY.signUp.submit}
        </button>
      </form>

      <p className="mt-5 text-center text-xs text-marketing-ink/40">
        <Link className="underline underline-offset-4 hover:text-marketing-ink/70" href="/signin">
          {PASSWORD_COPY.signUp.haveAccount}
        </Link>
      </p>
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
  );
}
