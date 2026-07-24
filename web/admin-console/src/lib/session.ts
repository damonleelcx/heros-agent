import "server-only";
import { cookies } from "next/headers";
import { redirect } from "next/navigation";
import { ADMIN_SESSION_COOKIE, AdminApiError, adminFetch } from "./adminApi";
import type { AdminIdentity } from "./types";
import type { Role } from "./roles";

/**
 * session.ts is the console's fail-closed entry check (FR20) and its disjoint-session boundary (FR21).
 *
 * # Why every page calls requireIdentity() before rendering anything
 *
 * The requirement is that an unauthenticated request REDIRECTS rather than rendering a shell that
 * then fails each of its requests. A shell-first console is worse than useless during an incident:
 * it looks like the platform is broken when the operator is simply signed out, and it leaks the
 * console's structure to anyone who loads the URL. So the identity call happens before the first
 * byte of a page, and its failure is a redirect.
 *
 * # Why the admin cookie is named distinctly and never read for tenant identity
 *
 * The admin and tenant session domains are disjoint. Different origin, different cookie jar, and —
 * belt and braces — a cookie name that could not be mistaken for the customer console's. Nothing in
 * this application reads a tenant cookie, and the admin cookie authorizes nothing on the customer
 * side because the customer console does not accept it.
 */

/** SESSION_COOKIE_OPTIONS is the only place the admin cookie's flags are set. */
export const SESSION_COOKIE_OPTIONS = {
  httpOnly: true,
  sameSite: "strict",
  secure: true,
  path: "/",
} as const;

/** readSessionToken returns the admin session token from the HttpOnly cookie, or null. */
export async function readSessionToken(): Promise<string | null> {
  const jar = await cookies();
  return jar.get(ADMIN_SESSION_COOKIE)?.value ?? null;
}

/**
 * requireIdentity resolves the acting admin principal, or redirects to sign-in.
 *
 * It resolves LIVE on every request: roles, capabilities and the active impersonation banner all come
 * back fresh, so a revoked role or an expired impersonation disappears from the screen at the next
 * navigation rather than at the next login.
 */
export async function requireIdentity(): Promise<{ identity: AdminIdentity; sessionToken: string }> {
  const sessionToken = await readSessionToken();
  if (!sessionToken) redirect("/signin?reason=no_session");
  try {
    const identity = await adminFetch<AdminIdentity>("/admin/api/me", { sessionToken });
    return { identity, sessionToken };
  } catch (error) {
    if (error instanceof AdminApiError && error.kind === "auth") {
      // Expired or revoked: the next request is denied with no grace, and the console says so rather
      // than rendering a shell whose every panel then fails.
      redirect("/signin?reason=session_ended");
    }
    throw error;
  }
}

/** hasCapability reports whether the acting admin holds a capability. */
export function hasCapability(identity: AdminIdentity, capability: string): boolean {
  return identity.capabilities.includes(capability);
}

/**
 * holdersOf names the roles that hold a capability, from the SAME permission map the backend
 * enforces. This is what turns a denial into an escalation path rather than a bare refusal (FR22).
 */
export function holdersOf(identity: AdminIdentity, capability: string): Role[] {
  const roles = Object.keys(identity.permission_map) as Role[];
  return roles.filter((role) => identity.permission_map[role]?.includes(capability));
}

/** frictionFor returns a capability's confirmation classification, as the server classified it. */
export function frictionFor(identity: AdminIdentity, capability: string) {
  return identity.friction.find((f) => f.capability === capability);
}

export type { Role } from "./roles";
export { ROLE_LABELS } from "./roles";
