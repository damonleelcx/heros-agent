import { SignInForm } from "./signInForm";
import { FederatedSignIn } from "./federatedSignIn";
import { isFederated } from "@/lib/idpConfig";
import { FactorStep } from "./factorStep";

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
  searchParams: Promise<{ reason?: string; step?: string }>;
}) {
  const { reason, step } = await searchParams;
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
        {reason === "idp_unreachable" ? (
          <div className="state state--denied" role="alert">
            <p className="state__title">The identity provider could not be reached</p>
            <p className="state__body">
              No session is issued when that happens, on purpose — this surface would rather sign
              nobody in than sign the wrong person in. Nothing is wrong with your account.
            </p>
          </div>
        ) : null}
        {reason === "rejected" ? (
          <div className="state state--denied" role="alert">
            <p className="state__title">That sign-in was not accepted</p>
            <p className="state__body">
              The exchange did not verify, or no platform-verified second factor was presented. A
              factor enrolled at your identity provider is not sufficient here — this platform
              verifies the factor itself.
            </p>
          </div>
        ) : null}
        {/* Federated deployments get the IdP redirect; a test-mode deployment gets the fixture
            control. Never both: two sign-in paths on one screen is two ways in, and the operator
            surface is the last place to offer a choice of doors. */}
        {/* Three states, one screen: present a factor (the IdP has just returned), begin a federated
            sign-in, or use the fixture control. Never two at once — a choice of doors on the operator
            surface is a second way in. */}
        {step === "factor" ? <FactorStep /> : isFederated() ? <FederatedSignIn /> : <SignInForm />}
      </main>
    </>
  );
}
