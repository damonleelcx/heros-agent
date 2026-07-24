import { SignInForm } from "./signInForm";

/**
 * The sign-in page.
 *
 * The console authenticates through a DEDICATED admin identity provider with SSO + MFA (FR1). In
 * production the operator is redirected to that IdP and returns with a signed assertion; the BFF
 * exchanges it for a session. This page renders the return step and, in a test-mode deployment, a
 * control to submit a fixture assertion the backend's test-mode IdP issues — the same code path,
 * with the same MFA requirement, never a bypass.
 *
 * No password is ever entered here. SSO + MFA happen at the IdP; the console holds only the resulting
 * session, in an HttpOnly cookie it cannot read.
 *
 * It carries the operator chrome band but NOT the principal identification the rest of the console
 * shows — there is no principal yet. What it must still do is look unmistakably like this console
 * (FR23), so an operator who is redirected here mid-incident knows immediately which of the two
 * consoles just signed them out.
 */
export default async function SignInPage({
  searchParams,
}: {
  searchParams: Promise<{ reason?: string }>;
}) {
  const { reason } = await searchParams;
  return (
    <>
      <header className="chrome">
        <span className="chrome__mark">
          <span className="chrome__glyph" aria-hidden="true">
            OP
          </span>
          Operator Console · Internal
        </span>
      </header>
      <main id="main">
        <p className="page__eyebrow">Access</p>
        <h1>Operator sign-in</h1>
        <p className="lede">
          This is the platform&rsquo;s highest-blast-radius surface. Access requires SSO and a verified
          MFA factor through the admin identity provider, and every action you take is permission-gated
          and audited.
        </p>
        {reason === "session_ended" ? (
          <div className="state state--denied" role="alert">
            <p className="state__title">Your session ended</p>
            <p className="state__body">
              It expired or was revoked. Re-authenticate with SSO and MFA to continue — there is no
              grace period on this surface.
            </p>
          </div>
        ) : null}
        {reason === "signed_out" ? (
          <div className="state state--empty">
            <p className="state__title">Signed out</p>
            <p className="state__body">Your session has been revoked.</p>
          </div>
        ) : null}
        <SignInForm />
      </main>
    </>
  );
}
