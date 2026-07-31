import "server-only";

/**
 * idpConfig.ts is the operator console's RUNTIME identity configuration.
 *
 * Two values, both read from the running process's environment rather than baked into the image: one
 * image must be able to federate against a different IdP without a rebuild, and the image an operator
 * verified must be the image they run (ADR-004 fail-static).
 *
 * Neither is a secret. The client secret is the platform's, resolved through its `Secrets` seam at the
 * moment of the code exchange — this process never holds it.
 */

/** ADMIN_IDENTITY_MODE mirrors the platform's own selector. `oidc`/`saml` are federated. */
const MODE = (process.env.ADMIN_IDENTITY_MODE ?? "").trim().toLowerCase();

/** isFederated reports whether this deployment signs operators in through a real IdP. */
export function isFederated(): boolean {
  return MODE === "oidc" || MODE === "saml";
}

/** adminIdentityModeName is for the health surface and the sign-in page. Never a credential. */
export function adminIdentityModeName(): string {
  return MODE || "unset";
}

/**
 * callbackURL is the exact URL the IdP returns the browser to.
 *
 * Configured, not derived. The customer console's `redirect.ts` records the defect that makes this
 * non-negotiable: the host a Next process believes it has and the host in the operator's address bar
 * are not reliably the same one, and an identity flow that guesses wrong fails in a browser while
 * passing every server-side check. It must also be on the platform's redirect allowlist AND registered
 * at the IdP — three places that have to agree, so it is stated once here rather than computed thrice.
 */
export function callbackURL(): string {
  const value = (process.env.ADMIN_IDP_CALLBACK_URL ?? "").trim();
  if (!value) {
    throw new Error(
      "ADMIN_IDP_CALLBACK_URL is unset — the operator console cannot federate without the exact callback URL " +
        "registered at the identity provider and allowlisted on the platform",
    );
  }
  return value;
}
