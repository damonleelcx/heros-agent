"use client";

import { useActionState } from "react";
import { beginSignIn } from "./signInAction";

/**
 * SignInForm drives the SSO + MFA exchange.
 *
 * The console never collects a password: it asks the BFF to begin the exchange, the BFF talks to the
 * admin IdP (which enforces SSO + MFA), and the operator returns with a session. In a test-mode
 * deployment the BFF's test IdP issues a fixture assertion — an operator picks the principal to sign
 * in as, and the backend still requires the MFA factor evidence, so the login path under test is the
 * production one.
 */
export function SignInForm() {
  const [state, action, pending] = useActionState(beginSignIn, null);
  return (
    <form className="action" action={action}>
      <fieldset disabled={pending}>
        <legend>Authenticate</legend>
        <p className="hint">
          Continue to the admin identity provider. You will complete SSO and your MFA factor there,
          then return here with a signed assertion.
        </p>
        <label htmlFor="subject">Admin principal (SSO subject)</label>
        <p className="hint">
          In production this is filled by the IdP redirect. In a test-mode deployment, enter the
          fixture subject for the principal you are signing in as.
        </p>
        <input id="subject" name="subject" type="text" autoComplete="off" placeholder="sso|platform_sre" required />

        <label htmlFor="factor">MFA factor</label>
        <select id="factor" name="factor" defaultValue="webauthn">
          <option value="webauthn">WebAuthn (hardware key)</option>
          <option value="totp">TOTP (authenticator app)</option>
        </select>

        <button type="submit" className="primary" disabled={pending}>
          {pending ? "Contacting the identity provider…" : "Continue with SSO + MFA"}
        </button>
      </fieldset>
      {state && !state.ok ? (
        <div className="state state--denied" role="alert">
          <p className="state__title">Authentication failed</p>
          <p className="state__body">{state.message}</p>
        </div>
      ) : null}
    </form>
  );
}
