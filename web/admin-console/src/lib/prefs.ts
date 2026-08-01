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

/**
 * THEME_COOKIE is the operator's colour-theme choice (R17 / FR39).
 *
 * # Why this joins density rather than inventing a second mechanism
 *
 * Density is already read on the server so the first paint is correct. Theme has the same requirement
 * and a sharper failure: a theme applied after hydration is a page the operator watches change colour,
 * and the usual workaround — a blocking inline script in `<head>` — is unavailable here because the
 * console's CSP has no `unsafe-inline`. One mechanism, read in one place, applied in one attribute.
 *
 * # 🔴 Why the operator console has a light theme at all, and what it must not cost
 *
 * FR23 requires this console to be distinguishable from the customer console **at a glance**, and that
 * is a safety requirement — an operator with both open, acting cross-tenant while believing the view is
 * single-tenant, is the named failure. So the light theme is not a free addition: the violet operator
 * chrome and its dark horizon band must stay unmistakable in light as well as dark, which is why the
 * chrome band is darker than the page in **both** themes rather than inverting with them.
 */
export const THEME_COOKIE = "heros_admin_theme";

export const THEMES = ["system", "dark", "light"] as const;
export type Theme = (typeof THEMES)[number];

export const THEME_LABEL: Record<Theme, string> = {
  system: "System",
  dark: "Dark",
  light: "Light",
};

export function isTheme(value: string | undefined | null): value is Theme {
  return typeof value === "string" && (THEMES as readonly string[]).includes(value);
}

/** THEME_COOKIE_OPTIONS mirrors the density preference: same origin, same lifetime, no identity. */
export const THEME_COOKIE_OPTIONS = DENSITY_COOKIE_OPTIONS;

/** readTheme returns the operator's theme, defaulting to following the system. */
export async function readTheme(): Promise<Theme> {
  const jar = await cookies();
  const raw = jar.get(THEME_COOKIE)?.value;
  return isTheme(raw) ? raw : "system";
}
