import Link from "next/link";
import { redirect } from "next/navigation";
import { AlertTriangle, CheckCircle2, KeyRound, ShieldAlert } from "lucide-react";
import { passwordSignInEnabled, verifyPasswordCredentials } from "@/lib/identity";
import { completePasswordReset } from "@/lib/idp/password";
import { issueSession, sessionTtlSeconds } from "@/lib/session";
import { SESSION_COOKIE, SESSION_COOKIE_OPTIONS } from "@/lib/cookies";
import { PASSWORD_COPY } from "@/content/passwordAccount";
import { cookies } from "next/headers";

/**
 * Setting a new password from a reset link.
 *
 * # This one POSTS, unlike the confirmation page
 *
 * It sets a credential, so the console's rule that no mutating route is a GET applies with full force. The
 * confirmation page's exception (see its header) rests on the act being idempotent and authority-free;
 * neither is true here.
 *
 * # 🔴 What the form SAYS before it is submitted
 *
 * That completing this ends every session and every personal credential the reader holds. It is the single
 * most consequential thing on the page and, for many readers, the reason they are here — so it is stated on
 * the form rather than reported afterwards. A consequence disclosed only in the confirmation is a
 * consequence somebody discovers when their other laptop stops working.
 *
 * # Why it signs them in afterwards
 *
 * Because the alternative is to bounce somebody who has just proved control of their address and chosen a
 * password to a sign-in form, to type the password they typed thirty seconds ago. The sign-in is a real one
 * — the same `verifyPasswordCredentials` any other sign-in uses, against the password just stored — rather
 * than a session minted on trust, so nothing here is a second way to obtain a session.
 */
export const dynamic = "force-dynamic";

async function submit(formData: FormData) {
  "use server";
  const token = String(formData.get("token") ?? "");
  const password = String(formData.get("password") ?? "");

  const outcome = await completePasswordReset(token, password);
  if (!outcome.ok) {
    // The reason travels as a CODE, never as the token and never as the password.
    redirect(`/reset-password?t=${encodeURIComponent(token)}&reason=${encodeURIComponent(outcome.reasonCode || "link_unusable")}`);
  }

  // Sign them in with what they just chose. If this fails for any reason the password change still stands,
  // so they are sent to sign in rather than told the reset did not work — which would be false.
  const signIn = await verifyPasswordCredentials(outcome.email, password);
  if (signIn.ok) {
    const { token: sessionToken } = await issueSession(signIn.principal);
    (await cookies()).set(SESSION_COOKIE, sessionToken, { ...SESSION_COOKIE_OPTIONS, maxAge: sessionTtlSeconds() });
  }
  const left = outcome.machineCredentials.map((c) => c.label).join(", ");
  redirect(`/reset-password?done=1&sessions=${outcome.sessionsRevoked}&creds=${outcome.credentialsRevoked}` +
    (left ? `&kept=${encodeURIComponent(left)}` : ""));
}

export default async function ResetPasswordPage({
  searchParams,
}: {
  searchParams: Promise<{ t?: string; reason?: string; done?: string; sessions?: string; creds?: string; kept?: string }>;
}) {
  if (!passwordSignInEnabled()) redirect("/signin");
  const params = await searchParams;

  if (params.done === "1") {
    const kept = (params.kept ?? "").trim();
    return (
      <>
        <h1 className="font-display text-2xl font-normal text-marketing-ink">Password changed</h1>
        <p className="mt-2 text-sm leading-relaxed text-marketing-ink/50">
          You are signed in on this browser. {params.sessions ?? "0"} other session(s) and{" "}
          {params.creds ?? "0"} personal access credential(s) were ended.
        </p>
        {/* 🔴 What was NOT revoked, by name. A screen that lists what it ended and hides what it left
            running tells somebody who is resetting because they were compromised that they are now safe.
            The platform returns this list precisely so this page can render it. */}
        {kept ? (
          <p className="mt-5 flex items-start gap-2 rounded-lg border border-warn/30 bg-warn/10 px-3 py-2 text-xs text-warn" role="status">
            <ShieldAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
            Still running, untouched by this reset: {kept}. These are your organization&apos;s machine
            credentials — revoke them from Settings if they should not be.
          </p>
        ) : (
          <p className="mt-5 flex items-start gap-2 rounded-lg border border-good/30 bg-good/10 px-3 py-2 text-xs text-good" role="status">
            <CheckCircle2 className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
            Your organization holds no machine credentials, so nothing was left running.
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

  const token = (params.t ?? "").trim();
  const refused = (params.reason ?? "").trim();

  if (!token) {
    return (
      <>
        <h1 className="font-display text-2xl font-normal text-marketing-ink">{PASSWORD_COPY.verify.failTitle}</h1>
        <p className="mt-2 text-sm leading-relaxed text-marketing-ink/50">{PASSWORD_COPY.reset.expired}</p>
        <p className="mt-7 text-center text-xs text-marketing-ink/40">
          <Link className="underline underline-offset-4 hover:text-marketing-ink/70" href="/forgot-password">
            Request a new link
          </Link>
        </p>
      </>
    );
  }

  return (
    <>
      <h1 className="font-display text-2xl font-normal text-marketing-ink">{PASSWORD_COPY.reset.title}</h1>
      <p className="mt-2 text-sm leading-relaxed text-marketing-ink/50">{PASSWORD_COPY.signUp.passwordHelp}</p>

      {refused ? (
        <p className="mt-5 flex items-start gap-2 rounded-lg border border-bad/30 bg-bad/10 px-3 py-2 text-xs text-bad" role="alert">
          <AlertTriangle className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          {refused === "weak_password"
            ? "That password was refused. Choose a longer one — this link has been used, so request another if you need one."
            : PASSWORD_COPY.reset.expired}
        </p>
      ) : null}

      {/* 🔴 Before the button, not after it. */}
      <p className="mt-5 flex items-start gap-2 rounded-lg border border-warn/30 bg-warn/10 px-3 py-2 text-xs text-warn">
        <ShieldAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
        {PASSWORD_COPY.reset.warning}
      </p>

      <form className="mt-7 flex flex-col gap-4" action={submit}>
        <input type="hidden" name="token" value={token} />
        <div className="flex flex-col gap-2">
          <label
            className="flex items-center gap-2 font-mono text-[10px] uppercase tracking-widest text-marketing-ink/50"
            htmlFor="password"
          >
            <KeyRound className="size-3" aria-hidden="true" />
            {PASSWORD_COPY.reset.passwordLabel}
          </label>
          <input
            className="w-full rounded-lg border border-marketing-ink/12 bg-marketing-canvas px-4 py-3 text-sm text-marketing-ink outline-none transition-colors placeholder:text-marketing-ink/25 focus:border-marketing-accent/60"
            id="password"
            name="password"
            type="password"
            autoComplete="new-password"
            minLength={12}
            required
          />
        </div>
        <button
          className="w-full cursor-pointer rounded-lg bg-marketing-accent py-3 text-sm font-semibold text-marketing-accent-ink transition-opacity hover:opacity-90 active:opacity-80"
          type="submit"
        >
          {PASSWORD_COPY.reset.submit}
        </button>
      </form>
    </>
  );
}
