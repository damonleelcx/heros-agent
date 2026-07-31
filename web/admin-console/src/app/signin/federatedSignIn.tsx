/**
 * FederatedSignIn is the operator's entry to a REAL identity provider.
 *
 * # Why a link and not a form
 *
 * `/auth/login` answers with a redirect to the IdP — a different origin — and browsers apply
 * `form-action` to a form submission's redirect chain. A posted sign-in would be blocked by the
 * console's own policy in a real browser while passing every server-side check. The route's comment
 * carries the rest.
 *
 * # 🔴 There is no MFA control here, and the flow is UNFINISHED because of it
 *
 * The second factor belongs AFTER the IdP returns, not before: presenting a factor from a browser that
 * has not yet proved whose it is is a factor presented by anybody. That step does not exist yet, so
 * this panel begins a sign-in that the platform will correctly refuse with `ErrMFARequired`.
 *
 * The refusal is right; the missing step is not. See `app/auth/callback/route.ts` for exactly what is
 * outstanding.
 */
export function FederatedSignIn() {
  return (
    <div className="action">
      <fieldset>
        <legend>Authenticate</legend>
        <p className="hint">
          Continue to your organization&rsquo;s identity provider. You will complete SSO there and
          return here. This console never sees your password, and it does not keep the proof the
          provider gives it.
        </p>
        <p className="hint">
          Your second factor is verified by this platform, not by the identity provider &mdash; a
          factor your IdP considers satisfied is not sufficient on the surface that can halt the
          fleet.
        </p>
        <a className="primary" href="/auth/login" role="button">
          Continue with SSO
        </a>
      </fieldset>
    </div>
  );
}
