import Link from "next/link";
import { redirect } from "next/navigation";
import { CheckCircle2, Mail } from "lucide-react";
import { passwordSignInEnabled } from "@/lib/identity";
import { requestPasswordReset } from "@/lib/idp/password";
import { PASSWORD_COPY } from "@/content/passwordAccount";

/**
 * Forgot your password.
 *
 * # 🔴 One answer, always
 *
 * The response is byte-identical whether or not the address has an account, whether or not it is even
 * well-formed. That is the entire design of this page: a form that said "no such account" would answer a
 * question the product deliberately declines to answer, and it would do so on the one screen an attacker
 * enumerating a customer list would reach first.
 *
 * The consequence a reader lives with is small and stated: somebody who mistypes their address gets the
 * same confirmation and no email. The copy says "if that address has an account", which is true, and the
 * alternative — telling every visitor which addresses are registered — is not a trade worth making for it.
 *
 * # Why this is a server action and not a client fetch
 *
 * The same three reasons `/api/session` gives for the sign-in form: it works before hydration and without
 * it, no client component ever holds the value, and it is exercisable end to end by anything that can post
 * a form. There is nothing interactive to gain here — the page has one field and one outcome.
 */
export const dynamic = "force-dynamic";

async function submit(formData: FormData) {
  "use server";
  const email = String(formData.get("email") ?? "");
  await requestPasswordReset(email);
  // 🔴 The redirect carries `sent`, never the address. An address in a query string is an address in the
  // browser's history, in the referrer of every subsequent request, and in any proxy log between here and
  // the reader — for a page whose whole purpose is to disclose nothing about who has an account.
  redirect("/forgot-password?sent=1");
}

export default async function ForgotPasswordPage({
  searchParams,
}: {
  searchParams: Promise<{ sent?: string }>;
}) {
  if (!passwordSignInEnabled()) {
    // A deployment that federates has no password to reset, and a form that collected an address and did
    // nothing would be worse than absent. The reader is sent where their sign-in actually happens.
    redirect("/signin");
  }
  const params = await searchParams;
  const sent = params.sent === "1";

  if (sent) {
    return (
      <>
        <h1 className="font-display text-2xl font-normal text-marketing-ink">{PASSWORD_COPY.forgot.title}</h1>
        <p className="mt-5 flex items-start gap-2 rounded-lg border border-good/30 bg-good/10 px-3 py-2 text-xs text-good" role="status">
          <CheckCircle2 className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          {PASSWORD_COPY.forgot.sent}
        </p>
        <p className="mt-5 text-center text-xs text-marketing-ink/40">
          <Link className="underline underline-offset-4 hover:text-marketing-ink/70" href="/signin">
            {PASSWORD_COPY.forgot.backToSignIn}
          </Link>
        </p>
      </>
    );
  }

  return (
    <>
      <h1 className="font-display text-2xl font-normal text-marketing-ink">{PASSWORD_COPY.forgot.title}</h1>
      <p className="mt-2 text-sm leading-relaxed text-marketing-ink/50">{PASSWORD_COPY.forgot.body}</p>
      <form className="mt-7 flex flex-col gap-4" action={submit}>
        <div className="flex flex-col gap-2">
          <label
            className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-marketing-ink/50"
            htmlFor="email"
          >
            <Mail className="size-3" aria-hidden="true" />
            {PASSWORD_COPY.forgot.emailLabel}
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
        <button
          className="w-full cursor-pointer rounded-lg bg-marketing-accent py-3 text-sm font-semibold text-marketing-accent-ink transition-opacity hover:opacity-90 active:opacity-80"
          type="submit"
        >
          {PASSWORD_COPY.forgot.submit}
        </button>
      </form>
      <p className="mt-5 text-center text-xs text-marketing-ink/40">
        <Link className="underline underline-offset-4 hover:text-marketing-ink/70" href="/signin">
          {PASSWORD_COPY.forgot.backToSignIn}
        </Link>
      </p>
    </>
  );
}
