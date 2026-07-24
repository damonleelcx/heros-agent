import { signOut } from "@/lib/actions";

/**
 * The sign-out route.
 *
 * A POST form target rather than a client action, so the "Sign out" button in the chrome works with
 * no JavaScript — a keyboard-only operator during an incident must be able to leave. It revokes the
 * session server-side (denied at the next request, no grace) and clears the HttpOnly cookie.
 */
export async function POST() {
  await signOut();
}
