import "server-only";

/**
 * identity.ts is the operator console's identity SEAM guard (ADR-008, P8 Decision 11, P19
 * admin-console-deploy).
 *
 * The operator console authenticates through the admin IdP (SSO + a verified MFA factor); the actual
 * assertion→principal exchange happens in the Go admin API. What lives HERE is the one rule the deploy
 * spec makes load-bearing: a **development identity mode must not be able to run in production**.
 *
 * A demo/fixture deployment (p8hermes) accepts fixture SSO subjects like `sso|superadmin` with any MFA
 * factor so the console is runnable without standing up a real IdP. That mode is `dev`. In a real
 * deployment the mode is `configured` and only the deployment's real IdP is accepted. Shipping `dev`
 * to production would put operator sign-in one fixture subject away from anyone — exactly the
 * highest-blast-radius surface this guard protects.
 *
 * Read once at module load, and refuse to BOOT rather than refuse at sign-in: a process that starts
 * and then rejects every login looks like a broken deployment; a process that will not start says
 * exactly what is wrong, once, to the person doing the deploy.
 */

const MODE = (process.env.ADMIN_IDENTITY_MODE ?? "configured").toLowerCase();
const IS_PRODUCTION = process.env.NODE_ENV === "production";

if (MODE === "dev" && IS_PRODUCTION) {
  throw new Error(
    "ADMIN_IDENTITY_MODE=dev is a development-only identity mode (fixture SSO subjects) and must not run in production",
  );
}

/** adminIdentityMode names the mode in force, for the health surface. Never a credential. */
export function adminIdentityMode(): string {
  return MODE;
}
