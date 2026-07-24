import "server-only";
import { cookies } from "next/headers";

/**
 * prefs.ts holds the operator's display preferences (FR29).
 *
 * # Why density is read on the SERVER
 *
 * The density preference is applied to the root element during server rendering, so the first paint is
 * already at the operator's density. The alternative — reading it in the browser after hydration —
 * ships a comfortable-density page and then reflows it, which is a layout shift on every navigation
 * and precisely what FR34 forbids for live views. A preference that causes a visible jump is a
 * preference the operator learns to dread.
 *
 * # Why a cookie and not local storage
 *
 * Local storage is unreadable at render time. A cookie is on the request, so the server can honour it
 * before it writes a byte. It carries no identity and no secret: it is one of two words.
 */

export const DENSITY_COOKIE = "heros_admin_density";

export type Density = "comfortable" | "compact";

/** DENSITY_COOKIE_OPTIONS keeps the preference for a year and never leaves this origin. */
export const DENSITY_COOKIE_OPTIONS = {
  httpOnly: true,
  sameSite: "strict",
  secure: true,
  path: "/",
  maxAge: 60 * 60 * 24 * 365,
} as const;

/** readDensity returns the operator's density, defaulting to comfortable. */
export async function readDensity(): Promise<Density> {
  const jar = await cookies();
  return jar.get(DENSITY_COOKIE)?.value === "compact" ? "compact" : "comfortable";
}
