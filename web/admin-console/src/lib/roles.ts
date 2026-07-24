/**
 * roles.ts holds the role vocabulary shared by server and CLIENT code.
 *
 * It is deliberately free of any server-only import. `session.ts` is server-only (it reads the
 * platform credential and the cookie jar), so a client component that needs a role label cannot import
 * from it without dragging the whole server module — and the credential — toward the bundle. This file
 * is the client-safe half: names and labels, no secrets, no I/O.
 */

export type Role = "support" | "billing_ops" | "platform_sre" | "superadmin";

/** ROLE_LABELS is the operator-facing role name. English, Title Case, matching the backend. */
export const ROLE_LABELS: Record<Role, string> = {
  support: "Support",
  billing_ops: "Billing-Ops",
  platform_sre: "Platform-SRE",
  superadmin: "Superadmin",
};
